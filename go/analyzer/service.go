/*
 * service.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * A persistent service that is able to analyze a collection of Python files.
 *
 * Transliterated from analyzer/service.ts (pyright 1.1.412), split across
 * service.go (the object, the file list, the public surface) and
 * service_config.go (config discovery and the command-line/config merge).
 *
 * PARTIAL, and the boundary is sharp: what is here is everything that decides
 * *what gets analyzed and how*. What is not here is everything that decides
 * *when*, which is the language server's:
 *
 *  - the analysis timer, scheduleReanalysis, recordUserInteractionTime and the
 *    deferred-analysis machinery;
 *  - the file watchers -- source, library and config -- and
 *    _shouldHandleSourceFileWatchChanges;
 *  - BackgroundAnalysisProgram and the worker threads behind it. The service
 *    talks to a Program directly here;
 *  - type-stub generation (writeTypeStub, _typeStubTargetImportName), which
 *    ANALYZER-PLAN.md lists as out of scope;
 *  - the cancellation provider.
 *
 * The ServiceProvider evaporates as it has everywhere else: the constructor
 * takes the file system, console and host directly.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"strconv"
)

// AnalyzerServiceOptions corresponds to the interface of the same name, reduced
// to the members the ported surface reads.
type AnalyzerServiceOptions struct {
	Console common.ConsoleInterface

	// FileSystem is the file system the service and its program read through.
	FileSystem uri.FileSystem

	// HostFactory builds the Host. The original defaults it to a NoAccessHost.
	HostFactory HostFactory

	// ImportResolverFactory builds the import resolver. The original defaults it
	// to AnalyzerService.createImportResolver.
	ImportResolverFactory func(fs uri.FileSystem, console common.ConsoleInterface, options *ConfigOptions, host Host) *ImportResolver

	// ConfigOptions is the initial config. The original defaults it to a
	// ConfigOptions rooted at the process's working directory.
	ConfigOptions *ConfigOptions

	// MaxAnalysisTime bounds one analysis pass; nil means unbounded.
	MaxAnalysisTime *MaxAnalysisTime

	// DisableChecker is passed through to the Program.
	DisableChecker bool
}

// AnalyzerService corresponds to the class of the same name.
type AnalyzerService struct {
	instanceName string

	console        common.ConsoleInterface
	fileSystem     uri.FileSystem
	options        AnalyzerServiceOptions
	host           Host
	importResolver *ImportResolver
	program        *Program

	configOptions      *ConfigOptions
	commandLineOptions *CommandLineOptions
	executionRootUri   uri.Uri

	// primaryConfigFileUri and extendedConfigFileUris record which config files
	// were read, so an "extends" cycle can be detected and the watcher (which is
	// not ported) would know what to watch.
	primaryConfigFileUri   uri.Uri
	extendedConfigFileUris []uri.Uri

	configParseErrorOccurred bool
	sourceEnumerator         *SourceEnumerator
}

// NewAnalyzerService corresponds to the constructor.
func NewAnalyzerService(instanceName string, options AnalyzerServiceOptions) *AnalyzerService {
	if options.Console == nil {
		options.Console = common.NewStandardConsole(common.LogLevelLog)
	}
	if options.HostFactory == nil {
		options.HostFactory = func() Host { return NewNoAccessHost() }
	}
	if options.ImportResolverFactory == nil {
		options.ImportResolverFactory = CreateImportResolver
	}
	if options.ConfigOptions == nil {
		// The original roots this at process.cwd(); a caller that wants that
		// supplies it, because reading the working directory here would make
		// the service's behaviour depend on where the process started.
		options.ConfigOptions = NewConfigOptions(uri.Empty())
	}

	s := &AnalyzerService{
		instanceName:     instanceName,
		console:          options.Console,
		fileSystem:       options.FileSystem,
		options:          options,
		configOptions:    options.ConfigOptions,
		executionRootUri: uri.Empty(),
	}

	s.host = options.HostFactory()
	s.importResolver = options.ImportResolverFactory(s.fileSystem, s.console, s.configOptions, s.host)
	s.program = NewProgram(s.importResolver, s.configOptions, s.console, nil, nil, options.DisableChecker, "")

	return s
}

// CreateImportResolver corresponds to the static of the same name.
func CreateImportResolver(fs uri.FileSystem, console common.ConsoleInterface, options *ConfigOptions, host Host) *ImportResolver {
	return NewImportResolver(fs, console, NewPartialStubService(fs), options, host, nil, nil, ImportResolverHooks{})
}

func (s *AnalyzerService) FS() uri.FileSystem { return s.importResolver.FileSystem() }

func (s *AnalyzerService) Console() common.ConsoleInterface { return s.console }

func (s *AnalyzerService) Program() *Program { return s.program }

func (s *AnalyzerService) ImportResolver() *ImportResolver { return s.importResolver }

func (s *AnalyzerService) Host() Host { return s.host }

func (s *AnalyzerService) GetConfigOptions() *ConfigOptions { return s.configOptions }

func (s *AnalyzerService) ExecutionRootUri() uri.Uri { return s.executionRootUri }

// SetOptions recomputes the configuration from the command-line options and
// applies it.
func (s *AnalyzerService) SetOptions(commandLineOptions *CommandLineOptions) {
	s.commandLineOptions = commandLineOptions

	host := s.options.HostFactory()
	configOptions := s.getConfigOptions(host, commandLineOptions)

	s.host = host
	s.setConfigOptions(configOptions)

	s.executionRootUri = configOptions.ProjectRoot
	s.applyConfigOptions(host)
}

// setConfigOptions corresponds to BackgroundAnalysisProgram.setConfigOptions,
// which the service reaches through in the original.
func (s *AnalyzerService) setConfigOptions(configOptions *ConfigOptions) {
	s.configOptions = configOptions
	s.program.SetConfigOptions(configOptions)
}

// applyConfigOptions corresponds to the protected method of the same name,
// minus the watchers it also installs.
func (s *AnalyzerService) applyConfigOptions(host Host) {
	// Rebuild the import resolver with the new config.
	s.importResolver = s.options.ImportResolverFactory(s.FS(), s.console, s.configOptions, host)
	s.program.SetImportResolver(s.importResolver)

	s.updateTrackedFileList(true)
}

// TestGetConfigOptions corresponds to test_getConfigOptions.
func (s *AnalyzerService) TestGetConfigOptions(commandLineOptions *CommandLineOptions) *ConfigOptions {
	return s.getConfigOptions(s.host, commandLineOptions)
}

// TestGetFileNamesFromFileSpecs corresponds to test_getFileNamesFromFileSpecs.
func (s *AnalyzerService) TestGetFileNamesFromFileSpecs() []uri.Uri {
	autoExcludeVenv := s.configOptions.AutoExcludeVenv != nil && *s.configOptions.AutoExcludeVenv
	enumerator := NewSourceEnumerator(
		s.configOptions.Include,
		s.configOptions.Exclude,
		autoExcludeVenv,
		s.FS(),
		s.console,
	)

	results := enumerator.Enumerate(0)
	return results.Matches.Values()
}

// updateTrackedFileList corresponds to the private method of the same name.
//
// The original's comment: if markFilesDirtyUnconditionally is true, we need to
// reparse and reanalyze all files in the program. If false, we will reparse and
// reanalyze only those files whose on-disk contents have changed. Unconditional
// dirtying is needed in the case where configuration options have changed.
//
// PARTIAL: the type-stub-generation arm is dropped along with the rest of that
// feature, and the enumeration runs to completion here rather than being
// resumable on a timer.
func (s *AnalyzerService) updateTrackedFileList(markFilesDirtyUnconditionally bool) {
	autoExcludeVenv := s.configOptions.AutoExcludeVenv != nil && *s.configOptions.AutoExcludeVenv
	s.sourceEnumerator = NewSourceEnumerator(
		s.configOptions.Include,
		s.configOptions.Exclude,
		autoExcludeVenv,
		s.FS(),
		s.console,
	)

	var results SourceEnumerateResult
	common.TimingStatsInstance.FindFilesTime.TimeOperation(func() {
		results = s.sourceEnumerator.Enumerate(0)
	})
	fileList := results.Matches.Values()

	s.program.SetTrackedFiles(fileList)

	// The original's comment: mark all files dirty so we can reanalyze them.
	s.program.MarkAllFilesDirty(markFilesDirtyUnconditionally)
}

func (s *AnalyzerService) IsTracked(u uri.Uri) bool { return s.program.Owns(u) }

// PrintStats corresponds to printStats().
func (s *AnalyzerService) PrintStats() {
	s.console.Info("")
	s.console.Info("Analysis stats")

	boundFileCount := s.program.GetFileCount(false)
	s.console.Info("Total files parsed and bound: " + strconv.Itoa(boundFileCount))

	checkedFileCount := s.program.GetUserFileCount()
	s.console.Info("Total files checked: " + strconv.Itoa(checkedFileCount))
}

func (s *AnalyzerService) GetUserFiles() []uri.Uri {
	out := []uri.Uri{}
	for _, i := range s.program.GetUserFiles() {
		out = append(out, i.Uri())
	}
	return out
}

func (s *AnalyzerService) GetOpenFiles() []uri.Uri {
	out := []uri.Uri{}
	for _, i := range s.program.GetOpened() {
		out = append(out, i.Uri())
	}
	return out
}

// SetFileOpened corresponds to the method of the same name.
func (s *AnalyzerService) SetFileOpened(u uri.Uri, version *int, contents string, options *OpenFileOptions) {
	s.program.SetFileOpened(u, version, contents, options)
}

func (s *AnalyzerService) SetFileClosed(u uri.Uri) []common.FileDiagnostics {
	return s.program.SetFileClosed(u)
}

// Analyze runs one analysis pass. It returns true if more work remains.
func (s *AnalyzerService) Analyze() bool {
	return s.program.Analyze(s.options.MaxAnalysisTime)
}

func (s *AnalyzerService) Dispose() {
	s.program.Dispose()
}

// reportConfigParseError corresponds to the private method of the same name.
// The original notifies the language client; here it records the fact, which is
// what analyzeProgram reports.
func (s *AnalyzerService) reportConfigParseError() {
	s.configParseErrorOccurred = true
}

func (s *AnalyzerService) ConfigParseErrorOccurred() bool { return s.configParseErrorOccurred }
