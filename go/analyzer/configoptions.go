/*
 * configoptions.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The slice of common/configOptions.ts the analyzer needs so far:
 * DiagnosticLevel and DiagnosticRuleSet. The rest of configOptions -- the
 * ConfigOptions class, file specs, command-line parsing -- lands with the
 * import resolver in Stage C; see analyzer/STATUS.md.
 *
 * DiagnosticRuleSet lives in the analyzer package rather than in common because
 * configOptions.ts imports analyzer/pythonPathUtils at runtime, which would be
 * an import cycle in Go. Nothing below depends on that part of the file.
 */

package analyzer

// DiagnosticLevel corresponds to
// `'none' | 'information' | 'warning' | 'error'`.
type DiagnosticLevel = string

const (
	DiagnosticLevelNone        DiagnosticLevel = "none"
	DiagnosticLevelInformation DiagnosticLevel = "information"
	DiagnosticLevelWarning     DiagnosticLevel = "warning"
	DiagnosticLevelError       DiagnosticLevel = "error"
)

// DiagnosticRuleSet corresponds to the interface of the same name.
type DiagnosticRuleSet struct {
	// Should "Unknown" types be reported as "Any"?
	PrintUnknownAsAny bool

	// Should type arguments to a generic class be omitted
	// when printed if all arguments are Unknown?
	OmitTypeArgsIfUnknown bool

	// Should parameter type be omitted if it is not annotated?
	OmitUnannotatedParamType bool

	// Indicate when a type is conditional based on a constrained
	// type variable type?
	OmitConditionalConstraint bool

	// Should Union and Optional types be printed in PEP 604 format?
	Pep604Printing bool

	// Use strict inference rules for list expressions?
	StrictListInference bool

	// Use strict inference rules for set expressions?
	StrictSetInference bool

	// Use strict inference rules for dictionary expressions?
	StrictDictionaryInference bool

	// Analyze functions and methods that have no annotations?
	AnalyzeUnannotatedFunctions bool

	// Use strict type rules for parameters assigned default of None?
	StrictParameterNoneValue bool

	// Enable experimental features that are not yet part of the
	// official Python typing spec?
	EnableExperimentalFeatures bool

	// Enable support for type: ignore comments?
	EnableTypeIgnoreComments bool

	// Use tagged hints to identify unreachable code via type analysis?
	EnableReachabilityAnalysis bool

	// Treat old typing aliases as deprecated if pythonVersion >= 3.9?
	DeprecateTypingAliases bool

	// No longer treat bytearray and memoryview as subclasses of bytes?
	DisableBytesTypePromotions bool

	// Report general type issues?
	ReportGeneralTypeIssues DiagnosticLevel

	// Report mismatch in types between property getter and setter?
	ReportPropertyTypeMismatch DiagnosticLevel

	// Report the use of unknown member accesses on function objects?
	ReportFunctionMemberAccess DiagnosticLevel

	// Report missing imports?
	ReportMissingImports DiagnosticLevel

	// Report missing imported module source files?
	ReportMissingModuleSource DiagnosticLevel

	// Report invalid type annotation forms?
	ReportInvalidTypeForm DiagnosticLevel

	// Report missing type stub files?
	ReportMissingTypeStubs DiagnosticLevel

	// Report cycles in import graph?
	ReportImportCycles DiagnosticLevel

	// Report imported symbol that is not accessed?
	ReportUnusedImport DiagnosticLevel

	// Report private class that is not accessed?
	ReportUnusedClass DiagnosticLevel

	// Report private function or method that is not accessed?
	ReportUnusedFunction DiagnosticLevel

	// Report variable that is not accessed?
	ReportUnusedVariable DiagnosticLevel

	// Report symbol or module that is imported more than once?
	ReportDuplicateImport DiagnosticLevel

	// Report use of wildcard import for non-local imports?
	ReportWildcardImportFromLibrary DiagnosticLevel

	// Report use of abstract method or variable?
	ReportAbstractUsage DiagnosticLevel

	// Report argument type incompatibilities?
	ReportArgumentType DiagnosticLevel

	// Report failure of assert_type call?
	ReportAssertTypeFailure DiagnosticLevel

	// Report type incompatibility for assignments?
	ReportAssignmentType DiagnosticLevel

	// Report issues related to attribute access expressions?
	ReportAttributeAccessIssue DiagnosticLevel

	// Report issues related to call expressions?
	ReportCallIssue DiagnosticLevel

	// Report inconsistencies with function overload signatures?
	ReportInconsistentOverload DiagnosticLevel

	// Report issues with index operations and expressions?
	ReportIndexIssue DiagnosticLevel

	// Report invalid type argument usage?
	ReportInvalidTypeArguments DiagnosticLevel

	// Report missing overloaded function implementation?
	ReportNoOverloadImplementation DiagnosticLevel

	// Report issues related to the use of unary or binary operators?
	ReportOperatorIssue DiagnosticLevel

	// Report attempts to subscript (index) an Optional type?
	ReportOptionalSubscript DiagnosticLevel

	// Report attempts to access members on a Optional type?
	ReportOptionalMemberAccess DiagnosticLevel

	// Report attempts to call a Optional type?
	ReportOptionalCall DiagnosticLevel

	// Report attempts to use an Optional type as an iterable?
	ReportOptionalIterable DiagnosticLevel

	// Report attempts to use an Optional type in a "with" statement?
	ReportOptionalContextManager DiagnosticLevel

	// Report attempts to use an Optional type in a binary or unary operation?
	ReportOptionalOperand DiagnosticLevel

	// Report attempts to redeclare the type of a symbol?
	ReportRedeclaration DiagnosticLevel

	// Report return type mismatches?
	ReportReturnType DiagnosticLevel

	// Report accesses to non-required TypedDict fields?
	ReportTypedDictNotRequiredAccess DiagnosticLevel

	// Report untyped function decorators that obscure the function type?
	ReportUntypedFunctionDecorator DiagnosticLevel

	// Report untyped class decorators that obscure the class type?
	ReportUntypedClassDecorator DiagnosticLevel

	// Report untyped base class that obscure the class type?
	ReportUntypedBaseClass DiagnosticLevel

	// Report use of untyped namedtuple factory method?
	ReportUntypedNamedTuple DiagnosticLevel

	// Report usage of private variables and functions outside of
	// the owning class or module?
	ReportPrivateUsage DiagnosticLevel

	// Report usage of deprecated type comments.
	ReportTypeCommentUsage DiagnosticLevel

	// Report usage of an import from a py.typed module that is
	// not meant to be re-exported from that module.
	ReportPrivateImportUsage DiagnosticLevel

	// Report attempts to redefine variables that are in all-caps.
	ReportConstantRedefinition DiagnosticLevel

	// Report use of deprecated classes or functions.
	ReportDeprecated DiagnosticLevel

	// Report usage of method override that is incompatible with
	// the base class method of the same name?
	ReportIncompatibleMethodOverride DiagnosticLevel

	// Report usage of variable override that is incompatible with
	// the base class symbol of the same name?
	ReportIncompatibleVariableOverride DiagnosticLevel

	// Report inconsistencies between __init__ and __new__ signatures.
	ReportInconsistentConstructor DiagnosticLevel

	// Report function overloads that overlap in signature but have
	// incompatible return types.
	ReportOverlappingOverload DiagnosticLevel

	// Report usage of possibly unbound variables.
	ReportPossiblyUnboundVariable DiagnosticLevel

	// Report failure to call super().__init__() in __init__ method.
	ReportMissingSuperCall DiagnosticLevel

	// Report instance variables that are not initialized within
	// the constructor.
	ReportUninitializedInstanceVariable DiagnosticLevel

	// Report usage of invalid escape sequences in string literals?
	ReportInvalidStringEscapeSequence DiagnosticLevel

	// Report usage of unknown input or return parameters for functions?
	ReportUnknownParameterType DiagnosticLevel

	// Report usage of unknown arguments for function calls?
	ReportUnknownArgumentType DiagnosticLevel

	// Report usage of unknown input or return parameters for lambdas?
	ReportUnknownLambdaType DiagnosticLevel

	// Report usage of unknown input or return parameters?
	ReportUnknownVariableType DiagnosticLevel

	// Report usage of unknown input or return parameters?
	ReportUnknownMemberType DiagnosticLevel

	// Report input parameters that are missing type annotations?
	ReportMissingParameterType DiagnosticLevel

	// Report usage of generic class without explicit type arguments?
	ReportMissingTypeArgument DiagnosticLevel

	// Report improper usage of type variables within function signatures?
	ReportInvalidTypeVarUse DiagnosticLevel

	// Report usage of function call within default value
	// initialization expression?
	ReportCallInDefaultInitializer DiagnosticLevel

	// Report calls to isinstance or issubclass that are statically determined
	// to always be true.
	ReportUnnecessaryIsInstance DiagnosticLevel

	// Report calls to cast that are statically determined
	// to always unnecessary.
	ReportUnnecessaryCast DiagnosticLevel

	// Report == or != operators that always evaluate to True or False.
	ReportUnnecessaryComparison DiagnosticLevel

	// Report 'in' operations that always evaluate to True or False.
	ReportUnnecessaryContains DiagnosticLevel

	// Report assert expressions that will always evaluate to true.
	ReportAssertAlwaysTrue DiagnosticLevel

	// Report when "self" or "cls" parameter is missing or is misnamed.
	ReportSelfClsParameterName DiagnosticLevel

	// Report implicit concatenation of string literals.
	ReportImplicitStringConcatenation DiagnosticLevel

	// Report usage of undefined variables.
	ReportUndefinedVariable DiagnosticLevel

	// Report usage of unbound variables.
	ReportUnboundVariable DiagnosticLevel

	// Report use of unhashable type in a dictionary.
	ReportUnhashable DiagnosticLevel

	// Report statements that are syntactically correct but
	// have no semantic meaning within a type stub file.
	ReportInvalidStubStatement DiagnosticLevel

	// Report usage of __getattr__ at the module level in a stub.
	ReportIncompleteStub DiagnosticLevel

	// Report operations on __all__ symbol that are not supported
	// by a static type checker.
	ReportUnsupportedDunderAll DiagnosticLevel

	// Report cases where a call expression's return result is not
	// None and is not used in any way.
	ReportUnusedCallResult DiagnosticLevel

	// Report cases where a call expression's return result is Coroutine
	// and is not used in any way.
	ReportUnusedCoroutine DiagnosticLevel

	// Report except clause that is unreachable.
	ReportUnusedExcept DiagnosticLevel

	// Report cases where a simple expression result is not used in any way.
	ReportUnusedExpression DiagnosticLevel

	// Report cases where the removal of a "# type: ignore" or "# pyright: ignore"
	// comment would have no effect.
	ReportUnnecessaryTypeIgnoreComment DiagnosticLevel

	// Report cases where the a "match" statement is not exhaustive in
	// covering all possible cases.
	ReportMatchNotExhaustive DiagnosticLevel

	// Report code that is determined to be unreachable via type analysis.
	ReportUnreachable DiagnosticLevel

	// Report missing @override decorator.
	ReportImplicitOverride DiagnosticLevel
}

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
