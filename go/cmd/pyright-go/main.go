/*
 * pyright-go
 *
 * The command-line type checker, transliterated from
 * packages/pyright/src/pyright.ts (pyright 1.1.412).
 *
 * It accepts pyright's command line, produces pyright's output in both text and
 * `--outputjson` form, and returns pyright's exit codes, so it can stand in for
 * the original in a script or a CI step without the caller knowing.
 *
 * PARTIAL, and the boundary is the same one the rest of the port draws. Four of
 * pyright's modes are separate features rather than type checking, and each
 * rests on a module that is out of scope for this port:
 *
 *   --watch        the file watchers and the reanalysis timer
 *   --createstub   typeStubWriter.ts
 *   --verifytypes  packageTypeVerifier.ts
 *   --dependencies the service's dependency report
 *
 * Each is rejected with a message saying so, rather than being accepted and
 * quietly ignored -- a drop-in that silently does less than it was asked is
 * worse than one that says what it cannot do.
 *
 * --threads is supported; see threads.go. The original forks worker processes,
 * this runs worker goroutines, one AnalyzerService each.
 */

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"

	"github.com/microsoft/pyright/go/analyzer"
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/parser"
	"github.com/microsoft/pyright/go/realfs"
)

// version is what --version prints and what the JSON report carries. The
// original reads it from its package.json.
//
// Release builds inject the release version via
// `-ldflags "-X main.version=..."` (see .github/workflows/release.yml):
// its first three segments name the pyright release whose behavior the port
// reproduces, and the fourth is the port's own revision -- 1.1.412.1 is the
// second cut of the 1.1.412 port. A build without the injection reports this
// development default, which names the transliteration source and marks
// itself so a consumer can tell the two apart.
var version = "1.1.412-go"

func main() {
	os.Exit(int(run(os.Args[1:])))
}

func run(argv []string) ExitStatus {
	args, err := parseArgs(argv)
	if err != nil {
		if unknown, ok := err.(unknownOptionError); ok {
			fmt.Fprintf(os.Stderr, "%s.\npyright-go --help for usage\n", unknown.Error())
			return ExitParameterError
		}
		fmt.Fprintf(os.Stderr, "%v\npyright-go --help for usage\n", err)
		return ExitParameterError
	}

	if args.has("help") {
		fmt.Print(usageText)
		return ExitNoErrors
	}

	if args.has("version") {
		fmt.Printf("pyright-go %s\n", version)
		return ExitNoErrors
	}

	// The modes that rest on an out-of-scope module. Rejected explicitly.
	for _, unsupported := range []struct{ name, why string }{
		{"watch", "the file watchers are not ported"},
		{"createstub", "type stub generation is not ported"},
		{"verifytypes", "package type verification is not ported"},
		{"dependencies", "the dependency report is not ported"},
	} {
		if args.has(unsupported.name) {
			fmt.Fprintf(os.Stderr, "'--%s' is not supported by pyright-go: %s\n",
				unsupported.name, unsupported.why)
			return ExitParameterError
		}
	}

	// The original's quirk, kept: the incompatibility check reads `if
	// (args.threads)`, so it fires only when an explicit COUNT was given --
	// a bare `--threads` or `--threads auto` skips it.
	explicitThreadCount, threadCountIsExplicit := parseThreadsArgValue(args.str("threads"))
	if threadCountIsExplicit {
		for _, arg := range []string{"watch", "stats", "dependencies"} {
			if args.has(arg) {
				fmt.Fprintf(os.Stderr, "'threads' option cannot be used with '%s' option\n", arg)
				return ExitParameterError
			}
		}
	}

	if args.has("ignoreexternal") {
		fmt.Fprintln(os.Stderr, "'--ignoreexternal' is valid only when used with '--verifytypes'")
		return ExitParameterError
	}

	if args.has("lib") {
		fmt.Fprintln(os.Stderr,
			"The --lib option is deprecated. Pyright now defaults to using library code to infer types.")
	}

	if stop := startProfiling(args); stop != nil {
		defer stop()
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitFatalError
	}

	// The original's comment: always enable autoSearchPaths when using the
	// command line. Missing this is not subtle -- it is what adds a `src`
	// directory to the search path, so without it a whole shape of project fails
	// to resolve its own imports.
	autoSearchPaths := true
	checkOnlyOpenFiles := false

	options := analyzer.NewCommandLineOptions(cwd, uri.UriExFile(cwd, true, false), true, false)
	options.ConfigSettings.AutoSearchPaths = &autoSearchPaths
	options.LanguageServerSettings.CheckOnlyOpenFiles = &checkOnlyOpenFiles

	// The original's comment: assume any relative paths are relative to the
	// working directory.
	if args.has("files") {
		fileSpecList := args.files

		// The original's comment: has the caller indicated that the file list
		// will be supplied by stdin?
		if len(fileSpecList) == 1 && fileSpecList[0] == "-" {
			fileSpecList, err = readFileListFromStdin()
			if err != nil {
				fmt.Fprintln(os.Stderr, "Invalid file list specified by stdin input")
				return ExitParameterError
			}
		}

		combined := make([]string, 0, len(fileSpecList))
		for _, f := range fileSpecList {
			combined = append(combined, common.CombinePaths(cwd, f))
		}
		options.ConfigSettings.IncludeFileSpecsOverride = &combined
	}

	if args.truthy("project") {
		configFilePath := common.CombinePaths(cwd, common.NormalizePath(args.str("project")))
		options.ConfigFilePath = &configFilePath
	}

	if args.truthy("pythonplatform") {
		platform, ok := parsePythonPlatform(args.str("pythonplatform"))
		if !ok {
			fmt.Fprintf(os.Stderr,
				"'%s' is not a supported Python platform; specify Darwin, Linux, Windows, iOS, or Android.\n",
				args.str("pythonplatform"))
			return ExitParameterError
		}
		options.ConfigSettings.PythonPlatform = &platform
	}

	if args.truthy("pythonversion") {
		pythonVersion := common.PythonVersionFromString(args.str("pythonversion"))
		if pythonVersion == nil {
			fmt.Fprintf(os.Stderr, "'%s' is not a supported Python version; specify 3.3, 3.4, etc.\n",
				args.str("pythonversion"))
			return ExitParameterError
		}
		options.ConfigSettings.PythonVersion = pythonVersion
	}

	if args.has("pythonpath") {
		for _, incompatible := range []string{"venv-path", "venvpath"} {
			if args.has(incompatible) {
				fmt.Fprintf(os.Stderr, "'pythonpath' option cannot be used with '%s' option\n", incompatible)
				return ExitParameterError
			}
		}

		pythonPath := common.CombinePaths(cwd, common.NormalizePath(args.str("pythonpath")))
		options.ConfigSettings.PythonPath = &pythonPath
	}

	if args.truthy("venv-path") {
		fmt.Fprintln(os.Stderr, "'venv-path' option is deprecated; use 'venvpath' instead")
		venvPath := common.CombinePaths(cwd, common.NormalizePath(args.str("venv-path")))
		options.ConfigSettings.VenvPath = &venvPath
	}

	if args.truthy("venvpath") {
		venvPath := common.CombinePaths(cwd, common.NormalizePath(args.str("venvpath")))
		options.ConfigSettings.VenvPath = &venvPath
	}

	if args.truthy("typeshed-path") {
		fmt.Fprintln(os.Stderr, "'typeshed-path' option is deprecated; use 'typeshedpath' instead")
		typeshedPath := common.CombinePaths(cwd, common.NormalizePath(args.str("typeshed-path")))
		options.ConfigSettings.TypeshedPath = &typeshedPath
	}

	if args.truthy("typeshedpath") {
		typeshedPath := common.CombinePaths(cwd, common.NormalizePath(args.str("typeshedpath")))
		options.ConfigSettings.TypeshedPath = &typeshedPath
	}

	if args.has("skipunannotated") {
		analyzeUnannotatedFunctions := false
		options.ConfigSettings.AnalyzeUnannotatedFunctions = &analyzeUnannotatedFunctions
	}

	if args.has("verbose") {
		verboseOutput := true
		options.ConfigSettings.VerboseOutput = &verboseOutput
	}

	minSeverityLevel := SeverityInformation
	if args.truthy("level") {
		switch strings.ToLower(args.str("level")) {
		case "error":
			minSeverityLevel = SeverityError
		case "warning":
			minSeverityLevel = SeverityWarning
		default:
			fmt.Fprintf(os.Stderr, "'%s' is not a valid value for --level; specify error or warning.\n",
				args.str("level"))
			return ExitParameterError
		}
	}

	if args.has("stats") && args.has("verbose") {
		options.LanguageServerSettings.LogTypeEvaluationTime = true
	}

	logLevel := common.LogLevelError
	if args.has("stats") || args.has("verbose") {
		logLevel = common.LogLevelInfo
	}

	// The original's comment: if using outputjson, redirect all console output
	// to stderr so it doesn't mess up the JSON output, which goes to stdout.
	outputJSON := args.has("outputjson")
	var console common.ConsoleInterface = common.NewStandardConsole(logLevel)
	if outputJSON {
		console = common.NewStderrConsole(logLevel)
	}

	typeshedRoot, ok := resolveTypeshedRoot(args)
	if !ok {
		fmt.Fprintln(os.Stderr,
			"Cannot locate typeshed-fallback. Pass --rootdir <dir>, or set PYRIGHT_GO_ROOTDIR.")
		return ExitFatalError
	}

	fileSystem := realfs.New(uri.UriExFile(typeshedRoot, true, false), true)

	// The original passes `hostFactory: () => new FullAccessHost(serviceProvider)`.
	hostFactory := func() analyzer.Host {
		return analyzer.NewFullAccessHost(fileSystem, uri.UriExDetector(true))
	}
	if args.has("nointerpreter") {
		hostFactory = func() analyzer.Host { return analyzer.NewNoAccessHost() }
	}

	service := analyzer.NewAnalyzerService("<default>", analyzer.AnalyzerServiceOptions{
		FileSystem:  fileSystem,
		Console:     console,
		HostFactory: hostFactory,
	})

	// The Program has no evaluator or checker until they are installed. The
	// original does this inside Program itself; the port keeps the factories at
	// the seam so the earlier stages could be exercised without them.
	installFactories(service.Program())

	// The original's comment: if the thread count was unspecified, use the
	// number of logical CPUs (i.e. hardware threads). We find empirically that
	// going below 4 threads usually doesn't help.
	threadCount := 1
	if args.has("threads") {
		threadCount = explicitThreadCount
		if !threadCountIsExplicit {
			threadCount = runtime.NumCPU()
			if threadCount < 4 {
				threadCount = 1
			}
		}
	}

	config := workerConfig{
		options:       options,
		typeshedRoot:  typeshedRoot,
		verboseOutput: args.has("verbose"),
		noInterpreter: args.has("nointerpreter"),
	}

	// --cachedir composes with --threads: the pool runs over the cache misses
	// with however many workers --threads asks for (one, if absent).
	if args.truthy("cachedir") {
		return runCached(args, options, args.str("cachedir"), threadCount, service,
			minSeverityLevel, console, config)
	}

	if threadCount > 1 {
		return runMultiThreaded(args, options, threadCount, service, minSeverityLevel, console, config)
	}

	configureSingleThreadedGC()

	service.SetOptions(options)

	// Analyze returns false once there is nothing left to do.
	for service.Analyze() {
	}

	configOptions := service.GetConfigOptions()
	fileDiags := service.Program().GetDiagnostics(configOptions, false)
	filesInProgram := len(service.GetUserFiles())
	timeInSec := common.TimingStatsInstance.GetTotalDuration()

	var report diagnosticResult
	if outputJSON {
		report = reportDiagnosticsAsJSON(fileDiags, minSeverityLevel, filesInProgram, timeInSec)
	} else {
		report = reportDiagnosticsAsText(fileDiags, minSeverityLevel)

		// The original's comment: print the total time.
		common.TimingStatsInstance.PrintSummary(console.Info)

		if args.has("stats") {
			// The original's comment: print the stats details.
			service.PrintStats()
			common.TimingStatsInstance.PrintDetails(console.Info)
		}
	}

	writeMemProfile(args)

	// The original's comment (on --warnings): use exit code of 1 if warnings are
	// reported.
	errorCount := report.errorCount
	if args.has("warnings") {
		errorCount += report.warningCount
	}

	if errorCount > 0 {
		return ExitErrorsReported
	}
	return ExitNoErrors
}

// resolveTypeshedRoot finds the directory holding typeshed-fallback.
//
// The original reads `global.__rootDirectory`, which its build sets to the
// directory the running script was loaded from. A Go binary has no such
// relationship to the reference tree, so this looks in the places a caller would
// reasonably have put it, in decreasing order of explicitness, and reports
// failure rather than analyzing every stdlib import as unresolved.
func resolveTypeshedRoot(args *parsedArgs) (string, bool) {
	candidates := []string{}

	if args.truthy("rootdir") {
		candidates = append(candidates, args.str("rootdir"))
	}
	if fromEnv := os.Getenv("PYRIGHT_GO_ROOTDIR"); fromEnv != "" {
		candidates = append(candidates, fromEnv)
	}

	// Beside the executable, then walking up from it -- which covers both a
	// binary shipped next to its typeshed and one left in a build directory
	// inside the source tree.
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		dir := filepath.Dir(exe)
		for i := 0; i < 8; i++ {
			candidates = append(candidates, dir)
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(filepath.Join(absolute, "typeshed-fallback")); err == nil && info.IsDir() {
			return absolute, true
		}
	}

	return "", false
}

func startProfiling(args *parsedArgs) func() {
	if !args.truthy("cpuprofile") {
		return nil
	}

	f, err := os.Create(args.str("cpuprofile"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil
	}

	if err := pprof.StartCPUProfile(f); err != nil {
		fmt.Fprintln(os.Stderr, err)
		f.Close()
		return nil
	}

	return func() {
		pprof.StopCPUProfile()
		f.Close()
	}
}

func writeMemProfile(args *parsedArgs) {
	if !args.truthy("memprofile") {
		return
	}

	f, err := os.Create(args.str("memprofile"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer f.Close()

	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

// installFactories mirrors cmd/tokenserver/factories.go: the evaluator and
// checker the Program builds per file.
func installFactories(program *analyzer.Program) {
	program.SetEvaluatorFactory(func(p *analyzer.Program) analyzer.TypeEvaluator {
		configOptions := p.ConfigOptions()
		return analyzer.NewTypeEvaluator(p.LookUpImport(), analyzer.EvaluatorOptions{
			PrintTypeFlags:              analyzer.GetPrintTypeFlags(configOptions),
			LogCalls:                    configOptions.LogTypeEvaluationTime,
			MinimumLoggingThreshold:     configOptions.TypeEvaluationTimeThreshold,
			EvaluateUnknownImportsAsAny: false,
			VerifyTypeCacheEvaluatorFlags: configOptions.InternalTestMode != nil &&
				*configOptions.InternalTestMode,
		})
	})

	program.SetCheckerFactory(func(
		importResolver *analyzer.ImportResolver,
		evaluator analyzer.TypeEvaluator,
		parserOutput *parser.ParserOutput,
		dependentFiles []*parser.ParserOutput,
	) *analyzer.Checker {
		return analyzer.NewChecker(importResolver, evaluator, parserOutput, dependentFiles)
	})
}
