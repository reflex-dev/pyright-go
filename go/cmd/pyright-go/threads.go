/*
 * threads.go
 *
 * The --threads mode, transliterated from runMultiThreaded and
 * runWorkerMessageLoop in pyright-internal/src/pyright.ts (pyright 1.1.412).
 *
 * The original forks child processes and feeds them one file at a time over
 * an IPC message loop; each worker builds its own AnalyzerService with
 * checkOnlyOpenFiles set, opens the file it was handed, and sends back only
 * that file's diagnostics. Goroutines and a mutex-guarded queue replace the
 * processes and the IPC, and each worker still gets its own file system,
 * console, host and service -- the workers share nothing but the queue and
 * immutable configuration, which is what made the fork model correct.
 *
 * The affinity queues are the original's scheduling verbatim: contiguous
 * slices of the (directory-ordered) file list, one per worker, on the theory
 * that neighboring files share imports and thus type-cache entries. A worker
 * that drains its own queue steals from the next one around the ring.
 *
 * The worker pool is shared with the --cachedir mode (cache.go), which runs
 * it over cache misses only.
 */

package main

import (
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/microsoft/pyright/go/analyzer"
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/realfs"
)

// parseThreadsArgValue corresponds to the function of the same name: null and
// 'auto' mean "pick a count", as does anything that does not parse to a
// positive integer. It reproduces parseInt(input, 10), which accepts a string
// with trailing garbage ("8x" is 8).
func parseThreadsArgValue(input string) (int, bool) {
	if input == "" || input == "auto" {
		return 0, false
	}

	end := 0
	if end < len(input) && (input[end] == '+' || input[end] == '-') {
		end++
	}
	for end < len(input) && input[end] >= '0' && input[end] <= '9' {
		end++
	}

	value, err := strconv.Atoi(input[:end])
	if err != nil || value < 1 {
		return 0, false
	}
	return value, true
}

// workerConfig carries what runWorkerMessageLoop's 'setOptions' message
// carries: everything a worker needs to build its own service.
type workerConfig struct {
	options       *analyzer.CommandLineOptions
	typeshedRoot  string
	verboseOutput bool
	noInterpreter bool
}

// newWorkerService builds the per-worker service exactly as the 'setOptions'
// arm of runWorkerMessageLoop does: its own file system, a stderr console (all
// of stdout belongs to the parent's report), a full-access host, and
// checkOnlyOpenFiles semantics inherited from the options.
func newWorkerService(config workerConfig) *analyzer.AnalyzerService {
	logLevel := common.LogLevelError
	if config.verboseOutput {
		logLevel = common.LogLevelInfo
	}
	output := common.NewStderrConsole(logLevel)

	fileSystem := realfs.New(uri.UriExFile(config.typeshedRoot, true, false), true)

	hostFactory := func() analyzer.Host {
		return analyzer.NewFullAccessHost(fileSystem, uri.UriExDetector(true))
	}
	if config.noInterpreter {
		hostFactory = func() analyzer.Host { return analyzer.NewNoAccessHost() }
	}

	service := analyzer.NewAnalyzerService("<default>", analyzer.AnalyzerServiceOptions{
		FileSystem:  fileSystem,
		Console:     output,
		HostFactory: hostFactory,
	})
	installFactories(service.Program())
	service.SetOptions(config.options)
	return service
}

// checkFilesInIsolation runs the worker pool over the given files: affinity
// chunks cut from the list's order, one goroutine per worker, work stealing
// around the ring, one file opened at a time under checkOnlyOpenFiles. It
// returns every produced FileDiagnostics (order unspecified; callers sort)
// and ExitNoErrors, or nil and the failure's exit status.
func checkFilesInIsolation(
	filesToCheck []uri.Uri,
	maxThreadCount int,
	config workerConfig,
	output common.ConsoleInterface,
) ([]common.FileDiagnostics, ExitStatus) {
	// The original's comment: don't create more workers than there are files.
	workerCount := min(maxThreadCount, len(filesToCheck))
	if workerCount == 0 {
		return nil, ExitNoErrors
	}

	// No counterpart upstream, where each forked worker gets its own V8 heap
	// and collector. Here the workers share one Go heap several times the
	// single-threaded live set, and at the default GOGC the collector's scan
	// work saturates the cores the workers need: measured on a 3135-file
	// project, GOGC=100 gave 1.03x over single-threaded and GOGC=200 gave
	// 2.3x. An explicit GOGC (or GOMEMLIMIT) from the environment wins.
	if workerCount > 1 && os.Getenv("GOGC") == "" && os.Getenv("GOMEMLIMIT") == "" {
		debug.SetGCPercent(200)
	}
	if workerCount == 1 {
		configureSingleThreadedGC()
	}

	// The param-details cache is written without synchronization, and a few
	// lazily-built type singletons are shared across workers.
	if workerCount > 1 {
		analyzer.ParamListDetailsCacheEnabled = false
	}

	// The original's comment: split the source files into affinity queues, one
	// for each worker. We assume that files that are next to each other in the
	// directory hierarchy probably have more common imports, so we want to
	// analyze them with the same worker if possible to maximize type cache
	// hits.
	affinityQueues := make([][]uri.Uri, workerCount)
	filesPerAffinityQueue := float64(len(filesToCheck)) / float64(workerCount)
	for i, fileUri := range filesToCheck {
		affinityIndex := int(float64(i) / filesPerAffinityQueue)
		affinityQueues[affinityIndex] = append(affinityQueues[affinityIndex], fileUri)
	}

	// The queue mutex stands in for the parent process's message loop, which
	// serialized assignment the same way. analyzeNextFile's stealing order is
	// kept: a worker looks at its own queue first, then the others around the
	// ring.
	var queueMu sync.Mutex
	takeNextFile := func(workerIndex int) (uri.Uri, bool) {
		queueMu.Lock()
		defer queueMu.Unlock()
		for i := 0; i < len(affinityQueues); i++ {
			affinityIndex := (workerIndex + i) % len(affinityQueues)
			if len(affinityQueues[affinityIndex]) > 0 {
				next := affinityQueues[affinityIndex][0]
				affinityQueues[affinityIndex] = affinityQueues[affinityIndex][1:]
				return next, true
			}
		}
		return nil, false
	}

	// Failures that the original surfaced as worker-process exits or messages.
	var fatalErrorOccurred atomic.Bool
	var configParseErrorOccurred atomic.Bool

	perWorkerDiagnostics := make([][]common.FileDiagnostics, workerCount)

	// analyzeOneFile is the 'analyzeFile' arm of runWorkerMessageLoop, plus the
	// completion callback's filter: of everything the pass changed, only the
	// file just opened belongs to this worker. It reports whether the worker
	// should continue.
	analyzeOneFile := func(workerIndex int, workerService *analyzer.AnalyzerService, fileUri uri.Uri) bool {
		workerFS := workerService.FS()

		// The original's comment: check the file's length before attempting to
		// read its full contents.
		if stat, err := workerFS.StatSync(fileUri); err == nil && stat.Size() > analyzer.MaxSourceFileSize {
			fmt.Fprintf(os.Stderr,
				"File length of \"%s\" is %d which exceeds the maximum supported file size of %d\n",
				fileUri.String(), stat.Size(), analyzer.MaxSourceFileSize)
			fatalErrorOccurred.Store(true)
			return false
		}

		fileContents, err := workerFS.ReadFileSync(fileUri)
		if err != nil {
			// The original's readFileSync throws, killing the worker process,
			// which the parent reports as a fatal error.
			fmt.Fprintf(os.Stderr, "Failed to read \"%s\": %v\n", fileUri.String(), err)
			fatalErrorOccurred.Store(true)
			return false
		}

		version := 1
		workerService.SetFileOpened(fileUri, &version, string(fileContents), nil)
		for workerService.Analyze() {
		}

		for _, fileDiag := range workerService.Program().GetDiagnostics(workerService.GetConfigOptions(), true) {
			if fileDiag.FileUri.Key() == fileUri.Key() {
				perWorkerDiagnostics[workerIndex] = append(perWorkerDiagnostics[workerIndex], fileDiag)
			}
		}
		return true
	}

	// Worker 0's first file is analyzed here, before any worker goroutine
	// exists. This is not an optimization but a memory-ordering requirement:
	// the first evaluator to see the builtins wires the anySpecialForm
	// singleton (typeevaluator_prefetch.go), and an evaluator can read the
	// singleton's unwired state while its own builtins are still partially
	// evaluated -- before its prefetch has taken the wiring mutex. Completing
	// one file's analysis on this goroutine performs the wiring, and spawning
	// the workers afterwards makes it visible to all of them. The work is not
	// duplicated; worker 0 simply starts from its second file.
	firstWorkerService := newWorkerService(config)
	if firstWorkerService.ConfigParseErrorOccurred() {
		return nil, ExitConfigFileParseError
	}
	if fileUri, ok := takeNextFile(0); ok {
		if !analyzeOneFile(0, firstWorkerService, fileUri) {
			output.Error("Fatal error from worker")
			return nil, ExitFatalError
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(workerIndex int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "Worker %d panicked: %v\n%s", workerIndex, r, debug.Stack())
					fatalErrorOccurred.Store(true)
				}
			}()

			workerService := firstWorkerService
			if workerIndex != 0 {
				workerService = newWorkerService(config)
				if workerService.ConfigParseErrorOccurred() {
					configParseErrorOccurred.Store(true)
					return
				}
			}

			for {
				if fatalErrorOccurred.Load() || configParseErrorOccurred.Load() {
					return
				}
				fileUri, ok := takeNextFile(workerIndex)
				if !ok {
					return
				}
				if !analyzeOneFile(workerIndex, workerService, fileUri) {
					return
				}
			}
		}(i)
	}

	wg.Wait()

	if configParseErrorOccurred.Load() {
		return nil, ExitConfigFileParseError
	}
	if fatalErrorOccurred.Load() {
		output.Error("Fatal error from worker")
		return nil, ExitFatalError
	}

	fileDiagnostics := []common.FileDiagnostics{}
	for _, workerDiags := range perWorkerDiagnostics {
		fileDiagnostics = append(fileDiagnostics, workerDiags...)
	}
	return fileDiagnostics, ExitNoErrors
}

// reportPooledDiagnostics is the tail every pooled mode shares: sort, report
// in the requested format, print the elapsed time, and derive the exit code.
func reportPooledDiagnostics(
	args *parsedArgs,
	fileDiagnostics []common.FileDiagnostics,
	filesAnalyzed int,
	startTime time.Time,
	minSeverityLevel SeverityLevel,
	output common.ConsoleInterface,
) ExitStatus {
	treatWarningsAsErrors := args.has("warnings")

	// The original's comment: sort all file diagnostics by the file URI so we
	// have a deterministic ordering.
	sort.SliceStable(fileDiagnostics, func(i, j int) bool {
		return fileDiagnostics[i].FileUri.String() < fileDiagnostics[j].FileUri.String()
	})

	// (Date.now() - startTime) / 1000: millisecond resolution, so the text
	// output prints "0.108", never a nanosecond tail.
	elapsedTime := float64(time.Since(startTime).Milliseconds()) / 1000
	errorCount := 0

	if args.has("outputjson") {
		report := reportDiagnosticsAsJSON(
			fileDiagnostics, minSeverityLevel, filesAnalyzed, elapsedTime)
		errorCount += report.errorCount
		if treatWarningsAsErrors {
			errorCount += report.warningCount
		}
	} else {
		report := reportDiagnosticsAsText(fileDiagnostics, minSeverityLevel)
		errorCount += report.errorCount
		if treatWarningsAsErrors {
			errorCount += report.warningCount
		}

		// The original's comment: print the total time. It formats the raw
		// elapsed seconds, not the TimingStats summary.
		output.Info("Completed in " + strconv.FormatFloat(elapsedTime, 'f', -1, 64) + "sec")
	}

	if errorCount > 0 {
		return ExitErrorsReported
	}
	return ExitNoErrors
}

// runMultiThreaded corresponds to the function of the same name.
func runMultiThreaded(
	args *parsedArgs,
	options *analyzer.CommandLineOptions,
	maxThreadCount int,
	service *analyzer.AnalyzerService,
	minSeverityLevel SeverityLevel,
	output common.ConsoleInterface,
	config workerConfig,
) ExitStatus {
	startTime := time.Now()

	// The original's comment: specify that only open files should be checked.
	// This will allow us to control which files are checked by which workers.
	checkOnlyOpenFiles := true
	options.LanguageServerSettings.CheckOnlyOpenFiles = &checkOnlyOpenFiles

	// The original's comment: this will trigger discovery of files in the
	// project.
	service.SetOptions(options)
	program := service.Program()

	// The original's comment: get the list of "tracked" source files -- those
	// that will be type checked.
	sourceFilesToAnalyze := []uri.Uri{}
	for _, info := range program.GetSourceFileInfoList() {
		if info.IsTracked() {
			sourceFilesToAnalyze = append(sourceFilesToAnalyze, info.Uri())
		}
	}

	output.Info(fmt.Sprintf("Found %d files to analyze", len(sourceFilesToAnalyze)))
	output.Info(fmt.Sprintf("Using %d threads", min(maxThreadCount, len(sourceFilesToAnalyze))))

	fileDiagnostics, status := checkFilesInIsolation(sourceFilesToAnalyze, maxThreadCount, config, output)
	if status != ExitNoErrors {
		return status
	}

	return reportPooledDiagnostics(args, fileDiagnostics, len(sourceFilesToAnalyze),
		startTime, minSeverityLevel, output)
}

// configureSingleThreadedGC sets the collector policy for a single-worker
// check when the environment doesn't. Whole-project analysis allocates tens
// of gigabytes of short-lived garbage over a multi-gigabyte live set, and at
// the default GOGC the collector consumes more CPU than the checker; letting
// the heap run to a memory limit instead was measured 10% faster wall-clock
// on a 3,150-file project. The limit is half of physical memory, and the
// policy only engages on machines with enough of it that the limit
// comfortably exceeds the live set -- elsewhere the defaults stand, because a
// limit below the live set makes the collector run continuously. An explicit
// GOGC or GOMEMLIMIT in the environment always wins, exactly as in
// runMultiThreaded.
func configureSingleThreadedGC() {
	if os.Getenv("GOGC") != "" || os.Getenv("GOMEMLIMIT") != "" {
		return
	}

	// Engage only where half of physical memory dwarfs any plausible live
	// set. Below this, GOGC=off with a limit close to the live set would make
	// the collector run continuously, which is worse than the default.
	total := totalPhysicalMemory()
	const minTotal = 32 << 30
	if total < minTotal {
		return
	}

	// Half of physical memory, capped: past ~24 GiB the extra headroom only
	// buys more first-touch page faults (measured: wall time flat from 12 to
	// 30 GiB, kernel time growing with the heap).
	limit := total / 2
	const maxLimit = 24 << 30
	if limit > maxLimit {
		limit = maxLimit
	}
	debug.SetMemoryLimit(int64(limit))
	debug.SetGCPercent(-1)
}

// totalPhysicalMemory reads MemTotal from /proc/meminfo, returning 0 when it
// cannot (non-Linux, restricted environments), which disables the policy.
func totalPhysicalMemory() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}
