/*
 * configoptions.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The two enums of common/configOptions.ts. DiagnosticRuleSet and the preset
 * rule sets are generated into configoptions_gen.go; the ExecutionEnvironment
 * and ConfigOptions classes are in configoptions_class.go.
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
