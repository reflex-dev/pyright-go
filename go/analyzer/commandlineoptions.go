/*
 * commandlineoptions.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Class that holds the command-line options (those that can be passed into the
 * main entry point of the command-line version of the analyzer).
 *
 * Transliterated from common/commandLineOptions.ts (pyright 1.1.412), minus the
 * DiagnosticSeverityOverrides enum, which landed with ConfigOptions in
 * configoptions_class.go because that is where its only reader is.
 *
 * Layout note: in `analyzer` rather than `common`, because ConfigOptions is.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// CommandLineConfigOptions holds the options that can be specified in a JSON
// config file. The original's comment: this list should match what is defined
// in the pyrightconfig.schema.json file.
//
// Every `T | undefined` field is a pointer or a nilable reference type, because
// service.ts distinguishes "not set" from the zero value for all of them --
// most of _getConfigOptions is a chain of `if (x !== undefined)`.
type CommandLineConfigOptions struct {
	// IncludeFileSpecs is a list of file specs to include in the analysis. It
	// can contain directories, in which case all "*.py" files within those
	// directories are included.
	IncludeFileSpecs []string

	// IncludeFileSpecsOverride, when set, overrides IncludeFileSpecs, rendering
	// it as ignored. The original's comment: this is used for the CLI "--files"
	// option, which should always override the "include" and "exclude" config
	// file settings.
	IncludeFileSpecsOverride *[]string

	// ExcludeFileSpecs is a list of file specs to exclude from the analysis.
	ExcludeFileSpecs []string

	// IgnoreFileSpecs is a list of file specs whose errors and warnings should
	// be ignored even if they are included in the transitive closure of
	// included files.
	IgnoreFileSpecs []string

	// VenvPath is the virtual environments directory.
	VenvPath *string

	// PythonPath is the path to the python interpreter.
	PythonPath *string

	// PythonEnvironmentName is the name for the virtual environment.
	PythonEnvironmentName *string

	// PythonPlatform is the platform indicator (darwin, linux, win32, ios,
	// android).
	PythonPlatform *string

	// PythonVersion is the version string (3.3, 3.4, etc.).
	PythonVersion *common.PythonVersion

	// TypeshedPath is the path of typeshed stubs.
	TypeshedPath *string

	// StubPath is the path of the typing folder.
	StubPath *string

	// UseLibraryCodeForTypes uses library implementations to extract type
	// information in the absence of type stubs.
	UseLibraryCodeForTypes *bool

	// AutoSearchPaths looks for common root folders such as 'src' and
	// automatically adds them as extra paths if the user has not explicitly
	// defined execution environments.
	AutoSearchPaths *bool

	// ExtraPaths are added to the default execution environment when the user
	// has not explicitly defined execution environments.
	ExtraPaths *[]string

	// TypeCheckingMode is the default type-checking rule set. The original's
	// comment: should be one of 'off', 'basic', 'standard', or 'strict'.
	TypeCheckingMode *string

	// DiagnosticOverrides holds the severity and boolean rule overrides, which
	// the original declares as two separate maps.
	DiagnosticOverrides *DiagnosticOverrides

	// AnalyzeUnannotatedFunctions analyzes functions and methods that have no
	// type annotations.
	AnalyzeUnannotatedFunctions *bool

	// VerboseOutput emits verbose information to the console.
	VerboseOutput *bool
}

// CommandLineLanguageServerOptions holds the options that are not specified in
// a JSON config file but apply to a language server.
type CommandLineLanguageServerOptions struct {
	// WatchForSourceChanges watches for changes in workspace source files.
	WatchForSourceChanges *bool

	// WatchForLibraryChanges watches for changes in environment
	// library/search paths.
	WatchForLibraryChanges *bool

	// WatchForConfigChanges watches for changes in config files.
	WatchForConfigChanges *bool

	// TypeStubTargetImportName is the type stub import target, for creation of
	// type stubs.
	TypeStubTargetImportName *string

	// CheckOnlyOpenFiles indicates that only open files should be checked.
	CheckOnlyOpenFiles *bool

	// AutoImportCompletions offers auto-import completions.
	AutoImportCompletions *bool

	// Indexing uses indexing.
	Indexing *bool

	// TaskListTokens are used for VS task list population.
	TaskListTokens *[]common.TaskListToken

	// LogTypeEvaluationTime uses type evaluator call tracking.
	LogTypeEvaluationTime bool

	// TypeEvaluationTimeThreshold is the minimum threshold for type eval
	// logging.
	TypeEvaluationTimeThreshold int

	// EnableAmbientAnalysis runs ambient analysis.
	EnableAmbientAnalysis bool

	// DisableTaggedHints disables reporting of hint diagnostics with tags.
	DisableTaggedHints *bool

	// PythonPath is the path to the python interpreter. The original's comment:
	// this is used when the language server gets the python path from the
	// client.
	PythonPath *string

	// VenvPath is the virtual environments directory.
	VenvPath *string
}

// CommandLineOptions corresponds to the class of the same name.
//
// The original's comment: some options can be specified from a source other
// than the pyright config file. This can be from command-line parameters or
// some other settings mechanism, like that provided through a language client
// like the VS Code editor. These options are later combined with those from the
// config file to produce the final configuration.
type CommandLineOptions struct {
	// ConfigSettings are the settings that are possible to set in a config.json
	// file.
	ConfigSettings CommandLineConfigOptions

	// LanguageServerSettings are the settings that are not.
	LanguageServerSettings CommandLineLanguageServerOptions

	// ConfigFilePath is the path of the config file. The original's comment:
	// this option cannot be combined with file specs.
	ConfigFilePath *string

	// ExecutionRoot is the absolute execution root (current working directory).
	// The original types it `string | Uri | undefined`; both forms occur, so
	// both are here and ExecutionRootUri wins when set.
	ExecutionRoot    string
	ExecutionRootUri uri.Uri
	HasExecutionRoot bool

	// FromLanguageServer indicates that the settings came from a language
	// server rather than from the command line. The original's comment: useful
	// for providing clearer error messages.
	FromLanguageServer bool
}

// NewCommandLineOptions corresponds to the constructor. The field initializers
// the TypeScript writes inline are applied here.
func NewCommandLineOptions(executionRoot string, executionRootUri uri.Uri, hasExecutionRoot bool, fromLanguageServer bool) *CommandLineOptions {
	return &CommandLineOptions{
		ConfigSettings: CommandLineConfigOptions{
			IncludeFileSpecs: []string{},
			ExcludeFileSpecs: []string{},
			IgnoreFileSpecs:  []string{},
		},
		LanguageServerSettings: CommandLineLanguageServerOptions{
			LogTypeEvaluationTime:       false,
			TypeEvaluationTimeThreshold: 50,
			EnableAmbientAnalysis:       true,
		},
		ExecutionRoot:      executionRoot,
		ExecutionRootUri:   executionRootUri,
		HasExecutionRoot:   hasExecutionRoot,
		FromLanguageServer: fromLanguageServer,
	}
}
