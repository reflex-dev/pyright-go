/*
 * pyright-go
 *
 * A command-line front end over the ported analyzer, shaped to be comparable
 * with pyright's own CLI rather than to replace it. It takes an execution root,
 * runs the same AnalyzerService the language server would, and prints the
 * diagnostics as JSON in pyright's `--outputjson` shape.
 *
 * This exists to answer one question: run against a real project, does the port
 * report the same things the original does, and how long does it take? Anything
 * beyond that -- watch mode, the language server, --verifytypes, exit-code
 * conventions -- is deliberately absent, because a fuller CLI would invite the
 * comparison to be read as a product claim rather than as a differential.
 *
 * The diagnostic categories are pyright's own numbering, mapped to the same
 * strings its JSON output uses, so the two outputs can be diffed directly.
 */

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/microsoft/pyright/go/analyzer"
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/parser"
	"github.com/microsoft/pyright/go/realfs"
)

type jsonRange struct {
	Start jsonPosition `json:"start"`
	End   jsonPosition `json:"end"`
}

type jsonPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type jsonDiagnostic struct {
	File     string     `json:"file"`
	Severity string     `json:"severity"`
	Message  string     `json:"message"`
	Range    *jsonRange `json:"range,omitempty"`
	Rule     string     `json:"rule,omitempty"`
}

type jsonSummary struct {
	FilesAnalyzed    int     `json:"filesAnalyzed"`
	ErrorCount       int     `json:"errorCount"`
	WarningCount     int     `json:"warningCount"`
	InformationCount int     `json:"informationCount"`
	TimeInSec        float64 `json:"timeInSec"`
}

type jsonOutput struct {
	Version            string           `json:"version"`
	Time               string           `json:"time"`
	GeneralDiagnostics []jsonDiagnostic `json:"generalDiagnostics"`
	Summary            jsonSummary      `json:"summary"`
}

// severityName maps the diagnostic categories pyright emits in JSON output.
// Categories that the CLI does not surface (unused, unreachable, deprecated --
// the tagged hints) return "" and are dropped, which is what the original does.
func severityName(category common.DiagnosticCategory) string {
	switch category {
	case common.DiagnosticCategoryError:
		return "error"
	case common.DiagnosticCategoryWarning:
		return "warning"
	case common.DiagnosticCategoryInformation:
		return "information"
	}
	return ""
}

func main() {
	outputJSON := flag.Bool("outputjson", false, "emit diagnostics as JSON")
	projectRoot := flag.String("project", "", "directory containing the project's config")
	rootDir := flag.String("rootdir", "", "directory holding typeshed-fallback (defaults to the binary's ../..)")
	pythonPath := flag.String("pythonpath", "", "path to the Python interpreter")
	noInterpreter := flag.Bool("nointerpreter", false,
		"never run a Python interpreter; use a NoAccessHost, so search paths come only from config")
	flag.Parse()

	if *projectRoot == "" {
		fmt.Fprintln(os.Stderr,
			"usage: pyright-go --project <dir> --rootdir <dir> [--outputjson] "+
				"[--pythonpath <file>] [--nointerpreter] [file...]")
		os.Exit(2)
	}

	absRoot, err := filepath.Abs(*projectRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	// The original locates its bundled typeshed relative to the running script
	// (`global.__rootDirectory`). The Go binary's location has nothing to do
	// with the reference tree, so the caller says where it is.
	if *rootDir == "" {
		fmt.Fprintln(os.Stderr, "--rootdir is required: the directory holding typeshed-fallback")
		os.Exit(2)
	}
	typeshedRoot, err := filepath.Abs(*rootDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	started := time.Now()

	fs := realfs.New(uri.UriExFile(typeshedRoot, true, false), true)
	rootUri := uri.UriExFile(absRoot, true, false)

	commandLineOptions := analyzer.NewCommandLineOptions(absRoot, rootUri, true, false)
	if files := flag.Args(); len(files) > 0 {
		override := append([]string{}, files...)
		commandLineOptions.ConfigSettings.IncludeFileSpecsOverride = &override
	} else {
		commandLineOptions.ConfigSettings.IncludeFileSpecs = []string{absRoot}
	}
	if *pythonPath != "" {
		// The original resolves this against the process's cwd before storing
		// it: `combinePaths(process.cwd(), normalizePath(args['pythonpath']))`.
		resolved := common.NormalizePath(*pythonPath)
		if cwd, err := os.Getwd(); err == nil {
			resolved = common.CombinePaths(cwd, resolved)
		}
		commandLineOptions.ConfigSettings.PythonPath = &resolved
	}

	// The original's CLI passes `hostFactory: () => new FullAccessHost(...)`, so
	// the import resolver can ask the interpreter where its search paths are.
	// Without it the only paths are the ones the config names, and every
	// third-party import in a virtualenv goes unresolved -- which is the whole
	// difference the --nointerpreter flag exists to demonstrate.
	hostFactory := func() analyzer.Host {
		return analyzer.NewFullAccessHost(fs, uri.UriExDetector(true))
	}
	if *noInterpreter {
		hostFactory = func() analyzer.Host { return analyzer.NewNoAccessHost() }
	}

	service := analyzer.NewAnalyzerService("pyright-go", analyzer.AnalyzerServiceOptions{
		FileSystem:  fs,
		Console:     common.NewStandardConsole(common.LogLevelError),
		HostFactory: hostFactory,
	})
	// The Program has no evaluator or checker until they are installed. The
	// original does this inside Program itself; the port keeps the factories at
	// the seam so the earlier stages could be exercised without them.
	installFactories(service.Program())

	service.SetOptions(commandLineOptions)

	// Analyze returns false once there is nothing left to do.
	for service.Analyze() {
	}

	configOptions := service.GetConfigOptions()
	fileDiags := service.Program().GetDiagnostics(configOptions, false)

	out := jsonOutput{
		Version:            "go-port",
		Time:               fmt.Sprint(time.Now().UnixMilli()),
		GeneralDiagnostics: []jsonDiagnostic{},
	}

	for _, fd := range fileDiags {
		path := fd.FileUri.String()
		for _, d := range fd.Diagnostics {
			severity := severityName(d.Category)
			if severity == "" {
				continue
			}

			r := d.Range
			entry := jsonDiagnostic{
				File:     path,
				Severity: severity,
				Message:  d.Message,
				Range: &jsonRange{
					Start: jsonPosition{Line: r.Start.Line, Character: r.Start.Character},
					End:   jsonPosition{Line: r.End.Line, Character: r.End.Character},
				},
			}
			if rule := d.GetRule(); rule != nil {
				entry.Rule = *rule
			}

			switch severity {
			case "error":
				out.Summary.ErrorCount++
			case "warning":
				out.Summary.WarningCount++
			case "information":
				out.Summary.InformationCount++
			}

			out.GeneralDiagnostics = append(out.GeneralDiagnostics, entry)
		}
	}

	out.Summary.FilesAnalyzed = len(service.GetUserFiles())
	out.Summary.TimeInSec = time.Since(started).Seconds()

	// Sort so two runs are comparable regardless of the order files were
	// enumerated in.
	sort.Slice(out.GeneralDiagnostics, func(i, j int) bool {
		a, b := out.GeneralDiagnostics[i], out.GeneralDiagnostics[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Range.Start.Line != b.Range.Start.Line {
			return a.Range.Start.Line < b.Range.Start.Line
		}
		if a.Range.Start.Character != b.Range.Start.Character {
			return a.Range.Start.Character < b.Range.Start.Character
		}
		return a.Message < b.Message
	})

	if *outputJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else {
		for _, d := range out.GeneralDiagnostics {
			fmt.Printf("  %s:%d:%d - %s: %s\n", d.File, d.Range.Start.Line+1,
				d.Range.Start.Character+1, d.Severity, d.Message)
		}
		fmt.Printf("%d error%s, %d warning%s, %d information (%.3fs)\n",
			out.Summary.ErrorCount, plural(out.Summary.ErrorCount),
			out.Summary.WarningCount, plural(out.Summary.WarningCount),
			out.Summary.InformationCount, out.Summary.TimeInSec)
	}

	if out.Summary.ErrorCount > 0 {
		os.Exit(1)
	}
}

// installFactories mirrors cmd/tokenserver/stagedfactories.go: the evaluator and
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

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
