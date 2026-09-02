/*
 * service_config.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Config discovery and the command-line / config-file merge, from
 * analyzer/service.ts (pyright 1.1.412). See service.go for the rest.
 *
 * This is the half of the service that decides what the ConfigOptions are, and
 * the order of operations in _getConfigOptions is load-bearing: the type
 * checking mode is set before the config file is read so the file can override
 * it, the command line is applied after the config file only when not running
 * as a language server, defaults are filled in after both, and the execution
 * environments are set up last because they inherit from the defaults. Each of
 * those is a behaviour config.test.ts asserts on.
 */

package analyzer

import (
	"regexp"
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/pelletier/go-toml/v2"
)

// uriSchemeRegex is `/^[a-zA-Z]{2,}[\w+.-]*:\/\/?/`, which the command-line
// overrides use to skip file specs that are really URIs (e.g. "memfs:/").
var uriSchemeRegex = regexp.MustCompile(`^[a-zA-Z]{2,}[\w+.-]*://?`)

// configFileContents corresponds to the interface of the same name.
type configFileContents struct {
	ConfigFileJsonObj any
	ConfigFileDirUri  uri.Uri
}

// getConfigOptions corresponds to the private method of the same name.
func (s *AnalyzerService) getConfigOptions(host Host, commandLineOptions *CommandLineOptions) *ConfigOptions {
	var executionRootUri uri.Uri
	switch {
	case commandLineOptions.ExecutionRootUri != nil:
		executionRootUri = commandLineOptions.ExecutionRootUri
	case commandLineOptions.ExecutionRoot != "":
		executionRootUri = uri.File(commandLineOptions.ExecutionRoot, s.caseDetector(), true)
	default:
		executionRootUri = uri.DefaultWorkspace(s.caseDetector())
	}

	executionRoot := s.FS().RealCasePath(executionRootUri)
	projectRoot := executionRoot
	var configFilePath uri.Uri
	var pyprojectFilePath uri.Uri

	hasExecutionRoot := commandLineOptions.ExecutionRootUri != nil || commandLineOptions.ExecutionRoot != ""

	if commandLineOptions.ConfigFilePath != nil && *commandLineOptions.ConfigFilePath != "" {
		// The original's comment: if the config file path was specified,
		// determine whether it's a directory (in which case the default config
		// file name is assumed) or a file.
		given := *commandLineOptions.ConfigFilePath
		if common.IsRootedDiskPath(given) {
			configFilePath = s.FS().RealCasePath(uri.File(given, s.caseDetector(), true))
		} else {
			configFilePath = s.FS().RealCasePath(projectRoot.ResolvePaths(given))
		}

		if !s.FS().ExistsSync(configFilePath) {
			s.console.Info("Configuration file not found at " + configFilePath.ToUserVisibleString() + ".")
			configFilePath = projectRoot
		} else {
			ext := configFilePath.LastExtension()
			if strings.HasSuffix(ext, ".json") || strings.HasSuffix(ext, ".toml") {
				projectRoot = configFilePath.GetDirectory()
			} else {
				projectRoot = configFilePath
				configFilePath = FindConfigFile(s.FS(), configFilePath)
				if configFilePath == nil {
					s.console.Info("Configuration file not found at " + projectRoot.ToUserVisibleString() + ".")
				}
			}
		}
	} else if hasExecutionRoot {
		// The original's comment: in a project-based IDE like VS Code, we should
		// assume that the project root directory contains the config file.
		configFilePath = FindConfigFile(s.FS(), projectRoot)

		// The original's comment: if pyright is being executed from the command
		// line, the working directory may be deep within a project, and we need
		// to walk up the directory hierarchy to find the project root.
		if configFilePath == nil && !commandLineOptions.FromLanguageServer {
			configFilePath = FindConfigFileHereOrUp(s.FS(), projectRoot)
		}

		if configFilePath != nil {
			projectRoot = configFilePath.GetDirectory()
		} else {
			s.console.Log("No configuration file found.")
		}
	}

	if configFilePath == nil {
		// See if we can find a pyproject.toml file in this directory.
		pyprojectFilePath = FindPyprojectTomlFile(s.FS(), projectRoot)

		if pyprojectFilePath == nil && !commandLineOptions.FromLanguageServer {
			pyprojectFilePath = FindPyprojectTomlFileHereOrUp(s.FS(), projectRoot)
		}

		if pyprojectFilePath != nil {
			projectRoot = pyprojectFilePath.GetDirectory()
			s.console.Log("pyproject.toml file found at " + projectRoot.ToUserVisibleString() + ".")
		} else {
			s.console.Log("No pyproject.toml file found.")
		}
	}

	configOptions := NewConfigOptions(projectRoot)

	// If we found a config file, load it and apply its settings.
	primary := configFilePath
	if primary == nil {
		primary = pyprojectFilePath
	}
	var pyprojectSearchDir uri.Uri
	if configFilePath == nil && pyprojectFilePath != nil {
		pyprojectSearchDir = pyprojectFilePath.GetDirectory()
	}
	configs := s.getExtendedConfigurations(primary, pyprojectSearchDir)

	if len(configs) > 0 {
		// The original's comment: with a pyrightconfig.json set, we want the
		// typeCheckingMode to always be standard as that's what the Pyright CLI
		// will expect. Command line options (if not a language server) and the
		// config file can override this.
		configOptions.InitializeTypeCheckingMode(TypeCheckingModeStandard, nil)

		// The original's comment: then we apply the config file settings. This
		// can update the typeCheckingMode.
		for _, config := range configs {
			configOptions.InitializeFromJson(config.ConfigFileJsonObj, config.ConfigFileDirUri, s.console)
		}

		// Set the configFileSource since we have a config file.
		configOptions.ConfigFileSource = s.primaryConfigFileUri

		// The original's comment: when not in language server mode, command line
		// options override config file options.
		if !commandLineOptions.FromLanguageServer {
			s.applyCommandLineOverrides(configOptions, &commandLineOptions.ConfigSettings, projectRoot, false)
		}
	} else {
		// The original's comment: initialize the type checking mode based on
		// whether this is for a language server or not. Language servers default
		// to 'off' when no config file is found.
		mode := TypeCheckingModeStandard
		if commandLineOptions.FromLanguageServer {
			mode = TypeCheckingModeOff
		}
		configOptions.InitializeTypeCheckingMode(mode, nil)

		// The original's comment: if there are no config files, we can then
		// directly apply the command line options.
		s.applyCommandLineOverrides(
			configOptions, &commandLineOptions.ConfigSettings, projectRoot, commandLineOptions.FromLanguageServer)
	}

	// The original's comment: apply the command line options that are not in the
	// config file. These settings only apply to the language server.
	s.applyLanguageServerOptions(configOptions, projectRoot, &commandLineOptions.LanguageServerSettings)

	// The original's comment: ensure that if no command line or config options
	// were applied, we have some defaults.
	s.ensureDefaultOptions(host, configOptions, projectRoot, executionRoot, commandLineOptions)

	// The original's comment: once we have defaults, we can then set up the
	// execution environments. Execution environments inherit from the defaults.
	for _, config := range configs {
		configOptions.SetupExecutionEnvironments(config.ConfigFileJsonObj, config.ConfigFileDirUri, s.console)
	}

	return configOptions
}

// caseDetector answers whether a Uri built by the service is case sensitive.
// The original reads a CaseSensitivityDetector out of the service provider,
// whose only implementations probe the file system once and cache the answer.
func (s *AnalyzerService) caseDetector() common.CaseSensitivityDetector {
	return uri.UriExDetector(true)
}

func (s *AnalyzerService) ensureDefaultOptions(
	host Host,
	configOptions *ConfigOptions,
	projectRoot uri.Uri,
	executionRoot uri.Uri,
	commandLineOptions *CommandLineOptions,
) {
	defaultExcludes := []string{"**/node_modules", "**/__pycache__", "**/.*"}

	// The original's comment: if no include paths were provided, assume that all
	// files within the project should be included.
	if len(configOptions.Include) == 0 {
		s.console.Info("No include entries specified; assuming " + projectRoot.ToUserVisibleString())
		configOptions.Include = append(configOptions.Include, uri.GetFileSpec(projectRoot, "."))
	}

	// The original's comment: if there was no explicit set of excludes, add a
	// few common ones to avoid long scan times.
	if len(configOptions.Exclude) == 0 {
		for _, exclude := range defaultExcludes {
			s.console.Info("Auto-excluding " + exclude)
			configOptions.Exclude = append(configOptions.Exclude, uri.GetFileSpec(projectRoot, exclude))
		}

		if configOptions.AutoExcludeVenv == nil {
			t := true
			configOptions.AutoExcludeVenv = &t
		}
	}

	if configOptions.DefaultExtraPaths == nil {
		autoSearchPaths := commandLineOptions.ConfigSettings.AutoSearchPaths != nil &&
			*commandLineOptions.ConfigSettings.AutoSearchPaths
		var extraPaths []string
		if commandLineOptions.ConfigSettings.ExtraPaths != nil {
			extraPaths = *commandLineOptions.ConfigSettings.ExtraPaths
		}
		configOptions.EnsureDefaultExtraPaths(s.FS(), autoSearchPaths, extraPaths)
	}

	if configOptions.DefaultPythonPlatform == "" && commandLineOptions.ConfigSettings.PythonPlatform != nil {
		configOptions.DefaultPythonPlatform = *commandLineOptions.ConfigSettings.PythonPlatform
	}
	if configOptions.DefaultPythonVersion == nil {
		configOptions.DefaultPythonVersion = commandLineOptions.ConfigSettings.PythonVersion
	}

	// The original's comment: if the caller specified that "typeshedPath" is the
	// root of the project, then we're presumably running in the typeshed project
	// itself. Auto-exclude stdlib packages that don't match the current Python
	// version.
	//
	// The comparison is `===` on two Uris, which is reference equality; Uris are
	// interned, so Go's == on the interface is the same test.
	if configOptions.TypeshedPath != nil &&
		configOptions.TypeshedPath == projectRoot &&
		configOptions.DefaultPythonVersion != nil {
		excludeList := s.importResolver.GetTypeshedStdlibExcludeList(
			configOptions.TypeshedPath,
			*configOptions.DefaultPythonVersion,
			configOptions.DefaultPythonPlatform,
		)

		s.console.Info("Excluding typeshed stdlib stubs according to VERSIONS file:")
		for _, exclude := range excludeList {
			s.console.Info("    " + exclude.String())
			configOptions.Exclude = append(configOptions.Exclude, uri.GetFileSpec(executionRoot, exclude.GetFilePath()))
		}
	}

	// If useLibraryCodeForTypes is unspecified, default it to true.
	if configOptions.UseLibraryCodeForTypes == nil {
		t := true
		configOptions.UseLibraryCodeForTypes = &t
	}

	if configOptions.StubPath != nil {
		// If there was a stub path specified, validate it.
		if !s.FS().ExistsSync(configOptions.StubPath) || !uri.IsDirectory(s.FS(), configOptions.StubPath) {
			s.console.Warn("stubPath " + configOptions.StubPath.String() + " is not a valid directory.")
		}
	} else {
		// If no stub path was specified, use a default path.
		configOptions.StubPath = configOptions.ProjectRoot.ResolvePaths(common.DefaultStubsDirectory)
	}

	// The original's comment: do some sanity checks on the specified settings
	// and report missing or inconsistent information.
	if configOptions.VenvPath != nil {
		if !s.FS().ExistsSync(configOptions.VenvPath) || !uri.IsDirectory(s.FS(), configOptions.VenvPath) {
			s.console.Error("venvPath " + configOptions.VenvPath.ToUserVisibleString() + " is not a valid directory.")
		}

		// The original's comment: venvPath without venv means it won't do
		// anything while resolveImport. So first, try to set venv from the
		// existing configOption if it is null. If both are null, then
		// resolveImport won't consider venv.
		if configOptions.Venv == "" {
			configOptions.Venv = s.configOptions.Venv
		}
		if configOptions.Venv != "" {
			fullVenvPath := configOptions.VenvPath.ResolvePaths(configOptions.Venv)

			if !s.FS().ExistsSync(fullVenvPath) || !uri.IsDirectory(s.FS(), fullVenvPath) {
				s.console.Error("venv " + configOptions.Venv +
					" subdirectory not found in venv path " + configOptions.VenvPath.ToUserVisibleString() + ".")
			} else {
				var importLogger *ImportLogger
				if configOptions.VerboseOutput != nil && *configOptions.VerboseOutput {
					importLogger = NewImportLogger()
				}
				// The original tests the result against undefined, which
				// findPythonSearchPaths never returns; the branch is dead there
				// and is dead here.
				FindPythonSearchPaths(s.FS(), configOptions, host, importLogger, false, nil)
			}
		}
	}

	// The original's comment: is there a reference to a venv? If so, there needs
	// to be a valid venvPath.
	if configOptions.Venv != "" {
		if configOptions.VenvPath == nil {
			s.console.Warn("venvPath not specified, so venv settings will be ignored.")
		}
	}

	if configOptions.TypeshedPath != nil {
		if !s.FS().ExistsSync(configOptions.TypeshedPath) || !uri.IsDirectory(s.FS(), configOptions.TypeshedPath) {
			s.console.Error("typeshedPath " + configOptions.TypeshedPath.ToUserVisibleString() +
				" is not a valid directory.")
		}
	}

	// The original's comment: this is a special case. It can be set in the
	// config file, but if it's set on the command line, we should always
	// override it.
	if commandLineOptions.ConfigSettings.VerboseOutput != nil {
		configOptions.VerboseOutput = commandLineOptions.ConfigSettings.VerboseOutput
	}

	// Ensure the default python version and platform.
	configOptions.EnsureDefaultPythonVersion(host, s.console)
	configOptions.EnsureDefaultPythonPlatform(host, s.console)
}

func (s *AnalyzerService) applyLanguageServerOptions(
	configOptions *ConfigOptions,
	projectRoot uri.Uri,
	languageServerOptions *CommandLineLanguageServerOptions,
) {
	configOptions.DisableTaggedHints = languageServerOptions.DisableTaggedHints != nil && *languageServerOptions.DisableTaggedHints
	if languageServerOptions.CheckOnlyOpenFiles != nil {
		configOptions.CheckOnlyOpenFiles = languageServerOptions.CheckOnlyOpenFiles
	}
	if languageServerOptions.AutoImportCompletions != nil {
		configOptions.AutoImportCompletions = *languageServerOptions.AutoImportCompletions
	}
	if languageServerOptions.Indexing != nil {
		configOptions.Indexing = *languageServerOptions.Indexing
	}
	if languageServerOptions.TaskListTokens != nil {
		configOptions.TaskListTokens = *languageServerOptions.TaskListTokens
	}
	if languageServerOptions.LogTypeEvaluationTime {
		configOptions.LogTypeEvaluationTime = languageServerOptions.LogTypeEvaluationTime
	}
	configOptions.TypeEvaluationTimeThreshold = languageServerOptions.TypeEvaluationTimeThreshold

	// The original's comment: special case, the language service can also set a
	// pythonPath. It should override any other setting.
	if languageServerOptions.PythonPath != nil && *languageServerOptions.PythonPath != "" {
		s.console.Info(`Setting pythonPath for service "` + s.instanceName + `": "` + *languageServerOptions.PythonPath + `"`)
		configOptions.PythonPath = s.FS().RealCasePath(uri.File(*languageServerOptions.PythonPath, s.caseDetector(), true))
	}
	if languageServerOptions.VenvPath != nil && *languageServerOptions.VenvPath != "" {
		if configOptions.VenvPath == nil {
			configOptions.VenvPath = projectRoot.ResolvePaths(*languageServerOptions.VenvPath)
		}
	}
}

func (s *AnalyzerService) applyCommandLineOverrides(
	configOptions *ConfigOptions,
	commandLineOptions *CommandLineConfigOptions,
	projectRoot uri.Uri,
	fromLanguageServer bool,
) {
	if commandLineOptions.TypeCheckingMode != nil && *commandLineOptions.TypeCheckingMode != "" {
		configOptions.InitializeTypeCheckingMode(*commandLineOptions.TypeCheckingMode, nil)
	}

	if commandLineOptions.ExtraPaths != nil {
		autoSearchPaths := commandLineOptions.AutoSearchPaths != nil && *commandLineOptions.AutoSearchPaths
		configOptions.EnsureDefaultExtraPaths(s.FS(), autoSearchPaths, *commandLineOptions.ExtraPaths)
	}

	if commandLineOptions.PythonVersion != nil || (commandLineOptions.PythonPlatform != nil && *commandLineOptions.PythonPlatform != "") {
		if commandLineOptions.PythonVersion != nil {
			configOptions.DefaultPythonVersion = commandLineOptions.PythonVersion
		}
		if commandLineOptions.PythonPlatform != nil && *commandLineOptions.PythonPlatform != "" {
			configOptions.DefaultPythonPlatform = *commandLineOptions.PythonPlatform
		}
	}

	if commandLineOptions.PythonPath != nil && *commandLineOptions.PythonPath != "" {
		s.console.Info(`Setting pythonPath for service "` + s.instanceName + `": "` + *commandLineOptions.PythonPath + `"`)
		configOptions.PythonPath = s.FS().RealCasePath(uri.File(*commandLineOptions.PythonPath, s.caseDetector(), true))
	}

	if commandLineOptions.PythonEnvironmentName != nil && *commandLineOptions.PythonEnvironmentName != "" {
		s.console.Info(`Setting environmentName for service "` + s.instanceName + `": "` +
			*commandLineOptions.PythonEnvironmentName + `"`)
		configOptions.PythonEnvironmentName = *commandLineOptions.PythonEnvironmentName
	}

	// The original's comment on each of the three: skip file specs that look
	// like URI schemes (e.g., "zowe-uss:", "memfs:").
	for _, fileSpec := range commandLineOptions.IncludeFileSpecs {
		if uriSchemeRegex.MatchString(fileSpec) {
			continue
		}
		configOptions.Include = append(configOptions.Include, uri.GetFileSpec(projectRoot, fileSpec))
	}

	for _, fileSpec := range commandLineOptions.ExcludeFileSpecs {
		if uriSchemeRegex.MatchString(fileSpec) {
			continue
		}
		configOptions.Exclude = append(configOptions.Exclude, uri.GetFileSpec(projectRoot, fileSpec))
	}

	for _, fileSpec := range commandLineOptions.IgnoreFileSpecs {
		if uriSchemeRegex.MatchString(fileSpec) {
			continue
		}
		configOptions.Ignore = append(configOptions.Ignore, uri.GetFileSpec(projectRoot, fileSpec))
	}

	configOptions.ApplyDiagnosticOverrides(commandLineOptions.DiagnosticOverrides)

	// The original's comment: override the analyzeUnannotatedFunctions setting
	// based on the command-line setting.
	if commandLineOptions.AnalyzeUnannotatedFunctions != nil {
		configOptions.DiagnosticRuleSet.AnalyzeUnannotatedFunctions = *commandLineOptions.AnalyzeUnannotatedFunctions
	}

	// The original's comment: override the include based on command-line
	// settings.
	if commandLineOptions.IncludeFileSpecsOverride != nil {
		configOptions.Include = []uri.FileSpec{}
		for _, include := range *commandLineOptions.IncludeFileSpecsOverride {
			// The original's comment: skip overrides that look like URI schemes
			// (e.g., "memfs:/", "zowe-uss://"). Otherwise they get passed to
			// Uri.file() and corrupt the include specs.
			if uriSchemeRegex.MatchString(include) {
				continue
			}
			configOptions.Include = append(configOptions.Include,
				uri.GetFileSpec(uri.File(include, s.caseDetector(), true), "."))
		}
	}

	// Override the venvPath based on the command-line setting.
	if commandLineOptions.VenvPath != nil && *commandLineOptions.VenvPath != "" {
		configOptions.VenvPath = projectRoot.ResolvePaths(*commandLineOptions.VenvPath)
	}

	reportDuplicateSetting := func(settingName string, configValue string) {
		settingSource := "a command-line option"
		if fromLanguageServer {
			settingSource = "the client settings"
		}
		s.console.Warn("The " + settingName + " has been specified in both the config file and " +
			settingSource + ". The value in the config file (" + configValue + ") will take precedence")
	}

	// The original's comment: apply the command-line options if the
	// corresponding item wasn't already set in the config file. Report any
	// duplicates.

	if commandLineOptions.TypeshedPath != nil && *commandLineOptions.TypeshedPath != "" {
		if configOptions.TypeshedPath == nil {
			configOptions.TypeshedPath = projectRoot.ResolvePaths(*commandLineOptions.TypeshedPath)
		} else {
			reportDuplicateSetting("typeshedPath", configOptions.TypeshedPath.ToUserVisibleString())
		}
	}

	// The original's comment: if useLibraryCodeForTypes was not specified in the
	// config, allow the command line to override it.
	if configOptions.UseLibraryCodeForTypes == nil {
		configOptions.UseLibraryCodeForTypes = commandLineOptions.UseLibraryCodeForTypes
	} else if commandLineOptions.UseLibraryCodeForTypes != nil {
		reportDuplicateSetting("useLibraryCodeForTypes", boolArg(*configOptions.UseLibraryCodeForTypes))
	}

	if commandLineOptions.StubPath != nil && *commandLineOptions.StubPath != "" {
		if configOptions.StubPath == nil {
			configOptions.StubPath = s.FS().RealCasePath(projectRoot.ResolvePaths(*commandLineOptions.StubPath))
		} else {
			reportDuplicateSetting("stubPath", configOptions.StubPath.ToUserVisibleString())
		}
	}
}

// getExtendedConfigurations walks the "extends" chain from a primary config
// file, returning the configs base-first.
func (s *AnalyzerService) getExtendedConfigurations(primaryConfigFileUri uri.Uri, pyprojectSearchDir uri.Uri) []configFileContents {
	s.primaryConfigFileUri = primaryConfigFileUri
	s.extendedConfigFileUris = []uri.Uri{}

	if primaryConfigFileUri == nil {
		return nil
	}

	curConfigFileUri := primaryConfigFileUri
	configJsonObjs := []configFileContents{}

	for {
		s.extendedConfigFileUris = append(s.extendedConfigFileUris, curConfigFileUri)

		var configFileJsonObj any

		// Is this a TOML or JSON file?
		if strings.HasSuffix(curConfigFileUri.LastExtension(), ".toml") {
			s.console.Info("Loading pyproject.toml file at " + curConfigFileUri.ToUserVisibleString())
			configFileJsonObj = s.parsePyprojectTomlFile(curConfigFileUri)
		} else {
			s.console.Info("Loading configuration file at " + curConfigFileUri.ToUserVisibleString())
			configFileJsonObj = s.parseJsonConfigFile(curConfigFileUri)
		}

		if configFileJsonObj == nil {
			break
		}

		// The original's comment: push onto the start of the array so base
		// configs are processed first.
		configJsonObjs = append([]configFileContents{{
			ConfigFileJsonObj: configFileJsonObj,
			ConfigFileDirUri:  curConfigFileUri.GetDirectory(),
		}}, configJsonObjs...)

		baseConfigUri := ResolveExtends(configFileJsonObj, curConfigFileUri.GetDirectory(), s.console)
		if baseConfigUri == nil {
			break
		}

		// Check for circular references.
		circular := false
		for _, u := range s.extendedConfigFileUris {
			if u.Equals(baseConfigUri) {
				circular = true
				break
			}
		}
		if circular {
			s.console.Error(`Circular reference in configuration file "extends" setting: ` +
				curConfigFileUri.ToUserVisibleString() + " extends " + baseConfigUri.ToUserVisibleString())
			break
		}

		curConfigFileUri = baseConfigUri
	}

	// The original's comment: if a pyproject.toml was found but had no
	// [tool.pyright] section, fall back to searching ancestor directories as if
	// that pyproject.toml didn't exist.
	if len(configJsonObjs) == 0 && pyprojectSearchDir != nil {
		parentDir := pyprojectSearchDir.GetDirectory()
		if !parentDir.Equals(pyprojectSearchDir) {
			fallback := FindConfigFileHereOrUp(s.FS(), parentDir)
			if fallback == nil {
				fallback = FindPyprojectTomlFileHereOrUp(s.FS(), parentDir)
			}
			if fallback != nil {
				// The original's comment: provide the next pyprojectSearchDir,
				// so we can continue searching upward.
				var nextSearchDir uri.Uri
				if strings.HasSuffix(fallback.LastExtension(), ".toml") {
					nextSearchDir = fallback.GetDirectory()
				}
				return s.getExtendedConfigurations(fallback, nextSearchDir)
			}
		}
	}

	return configJsonObjs
}

// parseJsonConfigFile returns nil where the original returns undefined.
func (s *AnalyzerService) parseJsonConfigFile(configPath uri.Uri) any {
	return s.attemptParseFile(configPath, func(fileContents string, attemptCount int) (any, bool) {
		result, err := common.ParseJSONC(fileContents)
		if err != nil {
			// The original throws `Errors parsing JSON file` when jsonc-parser
			// reports any error at all.
			return nil, false
		}
		return result, true
	})
}

// parsePyprojectTomlFile returns nil where the original returns undefined.
func (s *AnalyzerService) parsePyprojectTomlFile(pyprojectPath uri.Uri) any {
	return s.attemptParseFile(pyprojectPath, func(fileContents string, attemptCount int) (any, bool) {
		var configObj map[string]any
		if err := toml.Unmarshal([]byte(fileContents), &configObj); err != nil {
			s.console.Error("Pyproject file parse attempt " + itoa(attemptCount) + " error: " + err.Error())
			return nil, false
		}

		if tool, ok := configObj["tool"].(map[string]any); ok {
			if pyright, ok := tool["pyright"]; ok {
				return pyright, true
			}
		}

		s.console.Info(`Pyproject file "` + pyprojectPath.ToUserVisibleString() + `" has no "[tool.pyright]" section.`)
		// The original returns undefined *without* throwing here, which ends the
		// retry loop and answers undefined -- distinct from a parse failure.
		return nil, true
	})
}

// attemptParseFile corresponds to the private method of the same name. The
// callback answers (value, ok); ok == false is the original's throw.
func (s *AnalyzerService) attemptParseFile(fileUri uri.Uri, parseCallback func(contents string, attempt int) (any, bool)) any {
	parseAttemptCount := 0

	for {
		// Attempt to read the file contents.
		contents, err := s.FS().ReadFileSync(fileUri)
		if err != nil {
			s.console.Error(`Config file "` + fileUri.ToUserVisibleString() + `" could not be read.`)
			s.reportConfigParseError()
			return nil
		}

		// Attempt to parse the file.
		value, ok := parseCallback(string(contents), parseAttemptCount+1)
		if ok {
			return value
		}

		// The original's comment: if we attempt to read the file immediately
		// after it was saved, it may have been partially written when we read
		// it, resulting in parse errors. We'll give it a little more time and
		// try again.
		//
		// There is no waiting here -- the original does not wait either, it just
		// re-reads -- so the retry only helps if the file changed underneath.
		parseAttemptCount++
		if parseAttemptCount >= 5 {
			s.console.Error(`Config file "` + fileUri.ToUserVisibleString() +
				`" could not be parsed. Verify that format is correct.`)
			s.reportConfigParseError()
			return nil
		}
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return digits
}
