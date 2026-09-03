/*
 * configoptions_class.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Class that holds the configuration options for the analyzer.
 *
 * Transliterated from common/configOptions.ts (pyright 1.1.412): the
 * ExecutionEnvironment and ConfigOptions classes, matchFileSpecs, and the
 * DiagnosticSeverityOverrides slice of commandLineOptions.ts that
 * applyDiagnosticOverrides reads.
 *
 * The DiagnosticRule enum, the DiagnosticRuleSet struct, the four preset rule
 * sets and the rule lists are generated -- see configoptions_gen.go and
 * gen/generate_configoptions.py. The two enums are in configoptions.go.
 *
 * initializeFromJson and setupExecutionEnvironments -- the 550 lines that read
 * pyrightconfig.json and pyproject.toml -- landed separately with the service,
 * in configoptions_json.go, because their only caller is service.ts and
 * config.test.ts is what covers them.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// SignatureDisplayType corresponds to the enum of the same name.
type SignatureDisplayType = string

const (
	SignatureDisplayCompact   SignatureDisplayType = "compact"
	SignatureDisplayFormatted SignatureDisplayType = "formatted"
)

// TypeCheckingMode is the type of ConfigOptions.effectiveTypeCheckingMode,
// which the original spells as an inline union.
type TypeCheckingMode = string

const (
	TypeCheckingModeStrict   TypeCheckingMode = "strict"
	TypeCheckingModeBasic    TypeCheckingMode = "basic"
	TypeCheckingModeOff      TypeCheckingMode = "off"
	TypeCheckingModeStandard TypeCheckingMode = "standard"
)

// DiagnosticSeverityOverrides corresponds to the const enum of the same name in
// common/commandLineOptions.ts.
type DiagnosticSeverityOverrides = string

const (
	DiagnosticSeverityOverrideError       DiagnosticSeverityOverrides = "error"
	DiagnosticSeverityOverrideWarning     DiagnosticSeverityOverrides = "warning"
	DiagnosticSeverityOverrideInformation DiagnosticSeverityOverrides = "information"
	DiagnosticSeverityOverrideNone        DiagnosticSeverityOverrides = "none"
)

// GetDiagnosticSeverityOverrides corresponds to the function of the same name.
func GetDiagnosticSeverityOverrides() []DiagnosticSeverityOverrides {
	return []DiagnosticSeverityOverrides{
		DiagnosticSeverityOverrideError,
		DiagnosticSeverityOverrideWarning,
		DiagnosticSeverityOverrideInformation,
		DiagnosticSeverityOverrideNone,
	}
}

// DiagnosticOverrides stands in for the two override maps, which the original
// declares separately and then reads through a single parameter typed as their
// union. A rule appears in at most one of them.
type DiagnosticOverrides struct {
	Severity map[DiagnosticRule]DiagnosticSeverityOverrides
	Boolean  map[DiagnosticRule]bool
}

// ExecutionEnvironment corresponds to the class of the same name.
type ExecutionEnvironment struct {
	// Root is the root directory for execution. It is nil for a rootless
	// environment (e.g. open file mode).
	Root uri.Uri

	// Name of a virtual environment if there is one, otherwise just the path
	// to the python executable.
	Name string

	// PythonVersion always defaults to the latest stable version of the
	// language.
	PythonVersion common.PythonVersion

	// PythonPlatform defaults to no platform; "" is the original's undefined.
	PythonPlatform string

	// ExtraPaths defaults to none.
	ExtraPaths []uri.Uri

	// DiagnosticRuleSet holds the diagnostic rules with overrides.
	DiagnosticRuleSet *DiagnosticRuleSet

	// SkipNativeLibraries skips import resolution attempts for native
	// libraries. These can be expensive and are not needed for some use cases
	// (e.g. web-based tools or playgrounds).
	SkipNativeLibraries bool
}

// NewExecutionEnvironment corresponds to the constructor. The TypeScript
// defaults skipNativeLibraries to false.
//
// defaultPythonVersion is `PythonVersion | undefined`, so it is a pointer here;
// defaultExtraPaths is `Uri[] | undefined`, where nil and empty behave the
// same because the original copies it with `Array.from(x ?? [])`.
func NewExecutionEnvironment(
	name string,
	root uri.Uri,
	defaultDiagRuleSet *DiagnosticRuleSet,
	defaultPythonVersion *common.PythonVersion,
	defaultPythonPlatform string,
	defaultExtraPaths []uri.Uri,
	skipNativeLibraries bool,
) *ExecutionEnvironment {
	pythonVersion := common.LatestStablePythonVersion
	if defaultPythonVersion != nil {
		pythonVersion = *defaultPythonVersion
	}

	return &ExecutionEnvironment{
		Name:                name,
		Root:                root,
		PythonVersion:       pythonVersion,
		PythonPlatform:      defaultPythonPlatform,
		ExtraPaths:          append([]uri.Uri{}, defaultExtraPaths...),
		DiagnosticRuleSet:   CloneDiagnosticRuleSet(defaultDiagRuleSet),
		SkipNativeLibraries: skipNativeLibraries,
	}
}

// MatchFileSpecs corresponds to the function of the same name. The TypeScript
// defaults isFile to true.
func MatchFileSpecs(configOptions *ConfigOptions, u uri.Uri, isFile bool) bool {
	for _, includeSpec := range configOptions.Include {
		if uri.FileSpecMatchIncludeFileSpec(includeSpec.RegExp, configOptions.Exclude, u, isFile) {
			return true
		}
	}

	return false
}

// ConfigOptions holds the internal configuration options, derived from a
// combination of the command line and a JSON-based config file.
//
// Every `T | undefined` field is a pointer or a nilable reference type, because
// the original distinguishes "not set" from the zero value throughout -- most
// visibly in ensureDefaultPythonVersion and ensureDefaultPythonPlatform, which
// do nothing at all when their field is already defined.
type ConfigOptions struct {
	// ProjectRoot is the absolute directory of the project. All relative paths
	// in the config are based on this path.
	ProjectRoot uri.Uri

	// PythonPath is the path to the python interpreter.
	PythonPath uri.Uri

	// PythonEnvironmentName is the name of the python environment.
	PythonEnvironmentName string

	// TypeshedPath is the path to use for typeshed definitions.
	TypeshedPath uri.Uri

	// StubPath is the path to custom typings (stub) modules.
	StubPath uri.Uri

	// Include is a list of file specs to include in the analysis. It can
	// contain directories, in which case all "*.py" files within those
	// directories are included.
	Include []uri.FileSpec

	// Exclude is a list of file specs to exclude from the analysis (overriding
	// Include if necessary).
	Exclude []uri.FileSpec

	// AutoExcludeVenv automatically detects virtual environment folders and
	// excludes them. The original's comment: this property is for internal use
	// and not exposed externally as a config setting. It is used to store
	// whether the user has specified directories in the exclude setting, which
	// is later modified to include a default set. This setting is true when the
	// user has not specified any exclude.
	AutoExcludeVenv *bool

	// Ignore is a list of file specs whose errors and warnings should be
	// ignored even if they are included in the transitive closure of included
	// files.
	Ignore []uri.FileSpec

	// Strict is a list of file specs that should be analyzed using "strict"
	// mode.
	Strict []uri.FileSpec

	// DefineConstant is a set of defined constants used by the binder to
	// determine whether runtime conditions should evaluate to True or False.
	// The value is `boolean | string`.
	DefineConstant *common.OrderedMap[string, any]

	// VerboseOutput emits verbose information to the console.
	VerboseOutput *bool

	// CheckOnlyOpenFiles performs type checking and reports diagnostics only
	// for open files.
	CheckOnlyOpenFiles *bool

	// UseLibraryCodeForTypes uses library implementations to extract type
	// information in the absence of type stubs.
	UseLibraryCodeForTypes *bool

	// AutoImportCompletions offers auto-import completions.
	AutoImportCompletions bool

	// Indexing uses indexing.
	Indexing bool

	// LogTypeEvaluationTime uses type evaluator call tracking.
	LogTypeEvaluationTime bool

	// TypeEvaluationTimeThreshold is the minimum threshold for type eval
	// logging.
	TypeEvaluationTimeThreshold int

	// InitializedFromJson records whether this config was initialized from JSON
	// (pyrightconfig / pyproject).
	InitializedFromJson bool

	// DisableTaggedHints filters out any hint diagnostics with tags.
	DisableTaggedHints bool

	// DiagnosticRuleSet is the diagnostics rule set.
	DiagnosticRuleSet *DiagnosticRuleSet

	// TaskListTokens are the TaskList tokens used by diagnostics.
	TaskListTokens []common.TaskListToken

	// ExecutionEnvironments are the parameters that specify the execution
	// environment for the files being analyzed.
	ExecutionEnvironments []*ExecutionEnvironment

	// VenvPath is the path to a directory containing one or more virtual
	// environment directories. It is used in conjunction with the "venv" name
	// in the config file to identify the python environment used for resolving
	// third-party modules.
	VenvPath uri.Uri

	// Venv is the default venv environment.
	Venv string

	// DefaultPythonVersion can be overridden by an executionEnvironment.
	DefaultPythonVersion *common.PythonVersion

	// DefaultPythonPlatform can be overridden by an executionEnvironment.
	DefaultPythonPlatform string

	// DefaultExtraPaths can be overridden by an executionEnvironment.
	DefaultExtraPaths []uri.Uri

	// SkipNativeLibraries skips native library import resolutions.
	SkipNativeLibraries bool

	// InternalTestMode runs additional analysis as part of test cases.
	InternalTestMode *bool

	// IndexGenerationMode runs the program in index generation mode.
	IndexGenerationMode *bool

	// EvaluateUnknownImportsAsAny treats a symbol that cannot be resolved from
	// an import as Any rather than Unknown.
	EvaluateUnknownImportsAsAny *bool

	// FunctionSignatureDisplay controls how hover and completion function
	// signatures are displayed.
	FunctionSignatureDisplay SignatureDisplayType

	// ConfigFileSource determines whether there is a config file
	// (pyrightconfig.json or pyproject.toml).
	ConfigFileSource uri.Uri

	// EffectiveTypeCheckingMode determines the effective default type checking
	// mode.
	EffectiveTypeCheckingMode TypeCheckingMode
}

// NewConfigOptions corresponds to the constructor. The field initializers the
// TypeScript writes inline are applied here.
func NewConfigOptions(projectRoot uri.Uri) *ConfigOptions {
	return &ConfigOptions{
		ProjectRoot:                 projectRoot,
		Include:                     []uri.FileSpec{},
		Exclude:                     []uri.FileSpec{},
		Ignore:                      []uri.FileSpec{},
		Strict:                      []uri.FileSpec{},
		DefineConstant:              common.NewOrderedMap[string, any](),
		AutoImportCompletions:       true,
		Indexing:                    false,
		LogTypeEvaluationTime:       false,
		TypeEvaluationTimeThreshold: 50,
		InitializedFromJson:         false,
		DisableTaggedHints:          false,
		DiagnosticRuleSet:           GetConfigDiagnosticRuleSet(""),
		ExecutionEnvironments:       []*ExecutionEnvironment{},
		FunctionSignatureDisplay:    SignatureDisplayFormatted,
		EffectiveTypeCheckingMode:   TypeCheckingModeStandard,
	}
}

// GetConfigDiagnosticRuleSet corresponds to the static
// ConfigOptions.getDiagnosticRuleSet. The empty string stands in for the
// `typeCheckingMode?: string` argument, which falls through to standard along
// with every unrecognized value.
func GetConfigDiagnosticRuleSet(typeCheckingMode string) *DiagnosticRuleSet {
	switch typeCheckingMode {
	case TypeCheckingModeStrict:
		return GetStrictDiagnosticRuleSet()
	case TypeCheckingModeBasic:
		return GetBasicDiagnosticRuleSet()
	case TypeCheckingModeOff:
		return GetOffDiagnosticRuleSet()
	}

	return GetStandardDiagnosticRuleSet()
}

func (c *ConfigOptions) GetDefaultExecEnvironment() *ExecutionEnvironment {
	return NewExecutionEnvironment(
		c.getEnvironmentName(),
		c.ProjectRoot,
		c.DiagnosticRuleSet,
		c.DefaultPythonVersion,
		c.DefaultPythonPlatform,
		c.DefaultExtraPaths,
		c.SkipNativeLibraries,
	)
}

// FindExecEnvironment finds the best execution environment for a given file
// uri. The specified file path should be absolute. If no matching execution
// environment can be found, a default execution environment is used.
func (c *ConfigOptions) FindExecEnvironment(file uri.Uri) *ExecutionEnvironment {
	for _, env := range c.ExecutionEnvironments {
		// `Uri.is(env.root) ? env.root : this.projectRoot.resolvePaths(env.root || '')`
		envRoot := env.Root
		if envRoot == nil {
			envRoot = c.ProjectRoot.ResolvePaths("")
		}
		if file.StartsWith(envRoot) {
			return env
		}
	}
	return c.GetDefaultExecEnvironment()
}

func (c *ConfigOptions) GetExecutionEnvironments() []*ExecutionEnvironment {
	if len(c.ExecutionEnvironments) > 0 {
		return c.ExecutionEnvironments
	}

	return []*ExecutionEnvironment{c.GetDefaultExecEnvironment()}
}

// InitializeTypeCheckingMode corresponds to the method of the same name.
// severityOverrides is optional in the original; a nil DiagnosticOverrides is
// the absence.
func (c *ConfigOptions) InitializeTypeCheckingMode(typeCheckingMode string, severityOverrides *DiagnosticOverrides) {
	c.DiagnosticRuleSet = GetConfigDiagnosticRuleSet(typeCheckingMode)
	c.EffectiveTypeCheckingMode = typeCheckingMode

	if severityOverrides != nil {
		c.ApplyDiagnosticOverrides(severityOverrides)
	}
}

// ApplyDiagnosticOverrides corresponds to the method of the same name.
//
// The original reaches into the rule set by name -- `(ruleSet as any)[rule]` --
// which is what the generated accessor maps in configoptions_gen.go stand in
// for; configoptions_test.go asserts they hold exactly the rules the two rule
// lists name.
func (c *ConfigOptions) ApplyDiagnosticOverrides(diagnosticOverrides *DiagnosticOverrides) {
	if diagnosticOverrides == nil {
		return
	}

	validSeverities := GetDiagnosticSeverityOverrides()

	for _, ruleName := range GetDiagLevelDiagnosticRules() {
		severity, ok := diagnosticOverrides.Severity[ruleName]
		if !ok {
			continue
		}
		valid := false
		for _, candidate := range validSeverities {
			if candidate == severity {
				valid = true
				break
			}
		}
		if !valid {
			continue
		}
		if field := diagnosticRuleLevelFields[ruleName]; field != nil {
			*field(c.DiagnosticRuleSet) = severity
		}
	}

	for _, ruleName := range GetBooleanDiagnosticRules(true) {
		value, ok := diagnosticOverrides.Boolean[ruleName]
		if !ok {
			continue
		}
		if field := diagnosticRuleBoolFields[ruleName]; field != nil {
			*field(c.DiagnosticRuleSet) = value
		}
	}
}

// EnsureDefaultPythonPlatform assumes the current platform if no default python
// platform was specified.
func (c *ConfigOptions) EnsureDefaultPythonPlatform(host Host, console common.ConsoleInterface) {
	if c.DefaultPythonPlatform != "" {
		return
	}

	c.DefaultPythonPlatform = host.GetPythonPlatform(nil)
	if c.DefaultPythonPlatform != "" {
		console.Log("Assuming Python platform " + c.DefaultPythonPlatform)
	}
}

// EnsureDefaultPythonVersion retrieves the version from the currently-selected
// python interpreter if no default python version was specified.
func (c *ConfigOptions) EnsureDefaultPythonVersion(host Host, console common.ConsoleInterface) {
	if c.DefaultPythonVersion != nil {
		return
	}

	importLogger := NewImportLogger()
	c.DefaultPythonVersion = host.GetPythonVersion(c.PythonPath, importLogger)
	if c.DefaultPythonVersion != nil {
		console.Info("Assuming Python version " + c.DefaultPythonVersion.String())
	}

	for _, log := range importLogger.GetLogs() {
		console.Info(log)
	}
}

// EnsureDefaultExtraPaths corresponds to the method of the same name.
func (c *ConfigOptions) EnsureDefaultExtraPaths(fs uri.FileSystem, autoSearchPaths bool, extraPaths []string) {
	paths := []uri.Uri{}

	if autoSearchPaths {
		// Auto-detect the common scenario where the sources are under the src
		// folder.
		srcPath := c.ProjectRoot.ResolvePaths(common.Src)
		if fs.ExistsSync(srcPath) && !fs.ExistsSync(srcPath.ResolvePaths("__init__.py")) {
			paths = append(paths, fs.RealCasePath(srcPath))
		}
	}

	if len(extraPaths) > 0 {
		for _, p := range extraPaths {
			path := c.ProjectRoot.ResolvePaths(p)
			paths = append(paths, fs.RealCasePath(path))
			if uri.IsDirectory(fs, path) {
				paths = append(paths, GetPathsFromPthFiles(fs, path)...)
			}
		}
	}

	if len(paths) > 0 {
		c.DefaultExtraPaths = paths
	}
}

// getEnvironmentName corresponds to the private method of the same name.
func (c *ConfigOptions) getEnvironmentName() string {
	if c.PythonEnvironmentName != "" {
		return c.PythonEnvironmentName
	}
	if c.PythonPath != nil {
		return c.PythonPath.String()
	}
	return "python"
}

// ParseDiagLevel corresponds to the function of the same name. It takes the
// `string | boolean` argument as two, since only one is ever meaningful, and
// returns ("", false) where the original returns undefined.
func ParseDiagLevel(value string, boolValue *bool) (DiagnosticSeverityOverrides, bool) {
	if boolValue != nil {
		if *boolValue {
			return DiagnosticSeverityOverrideError, true
		}
		return DiagnosticSeverityOverrideNone, true
	}

	switch value {
	case "none":
		return DiagnosticSeverityOverrideNone, true
	case "error":
		return DiagnosticSeverityOverrideError, true
	case "warning":
		return DiagnosticSeverityOverrideWarning, true
	case "information":
		return DiagnosticSeverityOverrideInformation, true
	}

	return "", false
}
