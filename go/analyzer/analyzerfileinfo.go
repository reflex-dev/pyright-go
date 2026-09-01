/*
 * analyzerfileinfo.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Input type common to multiple analyzer passes.
 *
 * Transliterated from analyzer/analyzerFileInfo.ts (pyright 1.1.412), plus the
 * two declarations it pulls in from elsewhere: IPythonMode from sourceFile.ts
 * and ExecutionEnvironment from common/configOptions.ts. Both are here rather
 * than in their own files because the rest of sourceFile.ts and configOptions.ts
 * belong to Stage C; see STATUS.md.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// IPythonMode indicates whether IPython syntax is supported and if so, what
// type of notebook support is in use.
//
// From analyzer/sourceFile.ts.
type IPythonMode int

const (
	// IPythonModeNone means this is not a notebook. It is the only falsy enum
	// value, so the original tests for IPython support with `if (ipythonMode)`.
	IPythonModeNone IPythonMode = 0

	// IPythonModeCellDocs means each cell is its own document.
	IPythonModeCellDocs IPythonMode = 1
)

// ExecutionEnvironment corresponds to the class of the same name in
// common/configOptions.ts.
type ExecutionEnvironment struct {
	// Root is the root directory for execution. Nil if this is a rootless
	// environment (e.g. open file mode).
	Root uri.Uri

	// Name is the name of a virtual environment if there is one, otherwise just
	// the path to the python executable.
	Name string

	// PythonVersion always defaults to the latest stable version of the
	// language.
	PythonVersion common.PythonVersion

	// PythonPlatform defaults to no platform.
	PythonPlatform string

	// ExtraPaths defaults to no extra paths.
	ExtraPaths []uri.Uri

	// DiagnosticRuleSet holds the diagnostic rules with overrides.
	DiagnosticRuleSet *DiagnosticRuleSet

	// SkipNativeLibraries skips import resolution attempts for native
	// libraries. These can be expensive and are not needed for some use cases
	// (e.g. web-based tools or playgrounds).
	SkipNativeLibraries bool
}

// NewExecutionEnvironment corresponds to the ExecutionEnvironment constructor.
// A nil defaultPythonVersion falls back to the latest stable version, and the
// diagnostic rule set is copied rather than aliased, as in the original.
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

	ruleSetCopy := *defaultDiagRuleSet

	return &ExecutionEnvironment{
		Name:                name,
		Root:                root,
		PythonVersion:       pythonVersion,
		PythonPlatform:      defaultPythonPlatform,
		ExtraPaths:          sliceCopy(defaultExtraPaths),
		DiagnosticRuleSet:   &ruleSetCopy,
		SkipNativeLibraries: skipNativeLibraries,
	}
}

// AbsoluteModuleDescriptor maps import paths to the symbol table for the
// imported module.
type AbsoluteModuleDescriptor struct {
	ImportingFileUri uri.Uri
	NameParts        []string
}

// LookupImportOptions corresponds to the interface of the same name.
type LookupImportOptions struct {
	SkipFileNeededCheck bool
	SkipParsing         bool
}

// ImportLookup corresponds to the type of the same name. The TypeScript takes
// `Uri | AbsoluteModuleDescriptor`; exactly one of the two parameters here is
// non-nil. It returns nil where the TypeScript returns undefined.
type ImportLookup func(
	fileUri uri.Uri,
	moduleDescriptor *AbsoluteModuleDescriptor,
	options *LookupImportOptions,
) *ImportLookupResult

// ImportLookupResult corresponds to the interface of the same name.
type ImportLookupResult struct {
	SymbolTable                  SymbolTable
	DunderAllNames               []string
	UsesUnsupportedDunderAllForm bool
	DocString                    *string
	IsInPyTypedPackage           bool
}

// AnalyzerFileInfo corresponds to the interface of the same name.
type AnalyzerFileInfo struct {
	ImportLookup         ImportLookup
	FutureImports        *common.OrderedSet[string]
	BuiltinsScope        *Scope
	DiagnosticSink       *common.TextRangeDiagnosticSink
	ExecutionEnvironment *ExecutionEnvironment
	DiagnosticRuleSet    *DiagnosticRuleSet
	Lines                *common.TextRangeCollection[common.TextRange]
	TypingSymbolAliases  *common.OrderedMap[string, string]

	// DefinedConstants holds `Map<string, boolean | string>`; the value is
	// DefinedConstantValue.
	DefinedConstants *common.OrderedMap[string, DefinedConstantValue]

	FileID                     string
	FileUri                    uri.Uri
	ModuleName                 string
	IsStubFile                 bool
	IsTypingStubFile           bool
	IsTypingExtensionsStubFile bool
	IsTypeshedStubFile         bool
	IsBuiltInStubFile          bool
	IsInPyTypedPackage         bool
	IPythonMode                IPythonMode
	AccessedSymbolSet          *common.OrderedSet[int]
}

// DefinedConstantValue corresponds to the `boolean | string` value type of
// AnalyzerFileInfo.definedConstants.
type DefinedConstantValue struct {
	IsString bool
	Bool     bool
	String   string
}

// IsAnnotationEvaluationPostponed corresponds to
// isAnnotationEvaluationPostponed.
func IsAnnotationEvaluationPostponed(fileInfo *AnalyzerFileInfo) bool {
	if fileInfo.IsStubFile {
		return true
	}

	if fileInfo.FutureImports.Has("annotations") {
		return true
	}

	// The original notes: as of May 2023, the Python steering council has
	// approved PEP 649 for Python 3.13. It was tentatively approved for 3.12,
	// but they decided to defer until the next release to reduce the risk. As
	// of May 8, 2024, the change did not make it into Python 3.13beta1, so it
	// has been deferred to Python 3.14.
	// https://discuss.python.org/t/pep-649-deferred-evaluation-of-annotations-tentatively-accepted/21331
	if fileInfo.ExecutionEnvironment.PythonVersion.IsGreaterOrEqualTo(common.PythonVersion3_14) {
		return true
	}

	return false
}
