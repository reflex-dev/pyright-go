/*
 * configoptions.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The slice of common/configOptions.ts the analyzer needs so far: the two
 * enums, plus DiagnosticRuleSet and the preset rule sets in the generated
 * configoptions_gen.go. The rest of configOptions -- the ConfigOptions class,
 * file specs, command-line parsing -- lands with the import resolver in
 * Stage C; see analyzer/STATUS.md.
 *
 * All of it lives in the analyzer package rather than in common because
 * configOptions.ts imports analyzer/pythonPathUtils at runtime, which would be
 * an import cycle in Go. Nothing below depends on that part of the file.
 */

package analyzer

// PythonPlatform corresponds to the enum of the same name. The values are the
// enum's string values, not its member names, because ExecutionEnvironment
// stores `string | undefined` and compares against these directly.
type PythonPlatform = string

const (
	PythonPlatformDarwin  PythonPlatform = "Darwin"
	PythonPlatformWindows PythonPlatform = "Windows"
	PythonPlatformLinux   PythonPlatform = "Linux"
	PythonPlatformIOS     PythonPlatform = "iOS"
	PythonPlatformAndroid PythonPlatform = "Android"
)

// DiagnosticLevel corresponds to
// `'none' | 'information' | 'warning' | 'error'`.
type DiagnosticLevel = string

const (
	DiagnosticLevelNone        DiagnosticLevel = "none"
	DiagnosticLevelInformation DiagnosticLevel = "information"
	DiagnosticLevelWarning     DiagnosticLevel = "warning"
	DiagnosticLevelError       DiagnosticLevel = "error"
)

// ConfigOptions corresponds to the class of the same name.
//
// PARTIAL: only the DiagnosticRuleSet field is here, which is all
// getPrintTypeFlags reads. The rest of the class -- file specs, execution
// environments, command-line overrides, the default rule-set constructors --
// lands with the import resolver in Stage C. Since analyzer is a single Go
// package, growing this struct later is additive.
type ConfigOptions struct {
	DiagnosticRuleSet *DiagnosticRuleSet
}
