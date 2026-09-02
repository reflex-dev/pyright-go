/*
 * typeevaluatortypes.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluatorTypes.ts (pyright 1.1.412):
 * the abstract interface and helper types for the type evaluator module.
 *
 * This file is the Stage D seam. It replaces `type TypeEvaluator any` in
 * sourcefile.go with the real 88-method interface, so everything the evaluator
 * and its satellites need is declared before any of them is written.
 *
 * Three transliteration decisions apply throughout, and getting them wrong
 * would be expensive to undo once 30k lines sit on top:
 *
 *  - Optional parameters. TypeScript applies a default at the implementation,
 *    so a caller that omits an argument gets it; Go has no such thing and
 *    every call site must pass something. Where the original's default is the
 *    Go zero value the parameter is a plain type. Where it is *not* -- the
 *    `emitNotIterableError = true` of getTypeOfIterator and getTypeOfIterable,
 *    the `skipUnreachableCode = true` of getDeclInfoForNameNode, the
 *    `enforceParamNames = true` of validateOverrideMethod, the
 *    `{ method: 'get' }` of getTypeOfBoundMember -- the parameter is a pointer
 *    and the implementation substitutes the default for nil. That keeps the
 *    default in one place, as it is in the original, rather than copying it to
 *    every call site where it could rot.
 *
 *  - Optional struct fields. `foo?: Bar | undefined` becomes a nil-able field.
 *    For `boolean | undefined` fields the port's usual normalization applies
 *    (missing == false) unless the original distinguishes the two, which for
 *    the fields here it does not.
 *
 *  - Three names collide with ones already ported, because `go/analyzer` is
 *    one package and the TypeScript spreads these across modules.
 *    `ResolveAliasOptions` and `MapSubtypesOptions` exist in declarationUtils.ts
 *    and typeUtils.ts with different shapes, so this file's are
 *    `EvaluatorResolveAliasOptions` and `EvaluatorMapSubtypesOptions`.
 *    `TypeResult` is the other way round: typeCacheUtils.ts's is a structural
 *    subset of this one and the original passes the same object to both, so the
 *    cache now uses this one and its duplicate is gone.
 *
 *  - Cancellation is not carried. program.go already drops the
 *    CancellationToken threading -- "cancellation is the language server's" --
 *    so runWithCancellationToken is not part of this interface. checkForCancellation
 *    is kept because the evaluator calls it on its own inner loops; it has no
 *    token to consult and so does nothing, which is what a Program with no
 *    cancellation source would do anyway.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// maxSubtypesForInferredType is the maximum number of unioned subtypes for an
// inferred type (e.g. a list) before the type is considered an "Any".
const maxSubtypesForInferredType = 64

// maxInferredContainerDepth is the original's constant, with its comment: in
// certain loops, it's possible to construct arbitrarily-deep containers
// (tuples, lists, sets, or dicts) which can lead to infinite type analysis.
// This limits the depth.
const maxInferredContainerDepth = 8

// EvalFlags corresponds to the const enum of the same name.
//
// The original's last flag is `1 << 31`, which in JavaScript is a negative
// int32. Nothing ever compares these as numbers -- every use is a mask test --
// so the sign is not observable and the underlying type is left as the port's
// usual int.
type EvalFlags int

const (
	EvalFlagsNone EvalFlags = 0

	// EvalFlagsConvertEllipsisToAny interprets an ellipsis type annotation to
	// mean "Any".
	EvalFlagsConvertEllipsisToAny EvalFlags = 1 << 0

	// EvalFlagsNoSpecialize: normally a generic named type is specialized with
	// "Any" types. This flag indicates that specialization shouldn't take
	// place.
	EvalFlagsNoSpecialize EvalFlags = 1 << 1

	// EvalFlagsForwardRefs allows forward references. Don't report unbound
	// errors.
	EvalFlagsForwardRefs EvalFlags = 1 << 2

	// EvalFlagsStrLiteralAsType treats a string literal as a type.
	EvalFlagsStrLiteralAsType EvalFlags = 1 << 3

	// EvalFlagsNoFinal: 'Final' is not allowed in this context.
	EvalFlagsNoFinal EvalFlags = 1 << 4

	// EvalFlagsNoParamSpec: a ParamSpec isn't allowed in this context.
	EvalFlagsNoParamSpec EvalFlags = 1 << 5

	// EvalFlagsNoTypeVarTuple: a TypeVarTuple isn't allowed in this context.
	EvalFlagsNoTypeVarTuple EvalFlags = 1 << 6

	// EvalFlagsInstantiableType: expression is expected to be an instantiable
	// type rather than an instance (object).
	EvalFlagsInstantiableType EvalFlags = 1 << 7

	// EvalFlagsTypeExpression: a type expression imposes grammatical and
	// semantic limits on an expression. If this flag is set, illegal type
	// expressions are flagged as errors.
	EvalFlagsTypeExpression EvalFlags = 1 << 8

	// EvalFlagsAllowMissingTypeArgs suppresses the reportMissingTypeArgument
	// diagnostic in this context.
	EvalFlagsAllowMissingTypeArgs EvalFlags = 1 << 9

	// EvalFlagsAllowGeneric: the Generic class type is allowed in this context.
	// It is normally not allowed if ExpectingType is set.
	EvalFlagsAllowGeneric EvalFlags = 1 << 10

	// EvalFlagsNoTypeVarWithScopeId: TypeVars within this expression must not
	// refer to type vars used in an outer scope.
	EvalFlagsNoTypeVarWithScopeId EvalFlags = 1 << 11

	// EvalFlagsAllowTypeVarWithoutScopeId: TypeVars within this expression do
	// not need to refer to type vars used in an outer scope.
	EvalFlagsAllowTypeVarWithoutScopeId EvalFlags = 1 << 12

	// EvalFlagsTypeVarGetsCurScope: TypeVars within this expression that are
	// otherwise not associated with an outer scope should be associated with
	// the containing function's scope.
	EvalFlagsTypeVarGetsCurScope EvalFlags = 1 << 13

	// EvalFlagsEnforceVarianceConsistency: when a new class-scoped TypeVar is
	// used within a class declaration, make sure that it is not used to
	// parameterize a base class whose TypeVar variance is inconsistent.
	EvalFlagsEnforceVarianceConsistency EvalFlags = 1 << 14

	// EvalFlagsVarTypeAnnotation is used for PEP 526-style variable type
	// annotations.
	EvalFlagsVarTypeAnnotation EvalFlags = 1 << 15

	// EvalFlagsAllowEllipsis: an ellipsis is allowed even if TypeExpression is
	// set.
	EvalFlagsAllowEllipsis EvalFlags = 1 << 16

	// EvalFlagsNoClassVar: 'ClassVar' is not allowed in this context.
	EvalFlagsNoClassVar EvalFlags = 1 << 17

	// EvalFlagsNoNakedGeneric: 'Generic' cannot be used without type arguments
	// in this context.
	EvalFlagsNoNakedGeneric EvalFlags = 1 << 18

	// EvalFlagsNotParsed: the node is not parsed by the interpreter because it
	// is within a comment or a string literal.
	EvalFlagsNotParsed EvalFlags = 1 << 19

	// EvalFlagsAllowRequired: Required and NotRequired are allowed in this
	// context.
	EvalFlagsAllowRequired EvalFlags = 1 << 20

	// EvalFlagsAllowReadOnly: ReadOnly is allowed in this context.
	EvalFlagsAllowReadOnly EvalFlags = 1 << 21

	// EvalFlagsAllowUnpackedTuple allows an Unpack annotation for a tuple or
	// TypeVarTuple.
	EvalFlagsAllowUnpackedTuple EvalFlags = 1 << 22

	// EvalFlagsAllowUnpackedTypedDict allows an Unpack annotation for a
	// TypedDict.
	EvalFlagsAllowUnpackedTypedDict EvalFlags = 1 << 23

	// EvalFlagsParsesStringLiteral: even though an expression is enclosed in a
	// string literal, the interpreter (within a source file, not a stub) still
	// parses the expression and generates parse errors.
	EvalFlagsParsesStringLiteral EvalFlags = 1 << 24

	// EvalFlagsNoConvertSpecialForm does not convert special forms to their
	// corresponding runtime objects even when expecting a type expression.
	EvalFlagsNoConvertSpecialForm EvalFlags = 1 << 25

	// EvalFlagsNoNonTypeSpecialForms: certain special forms (Protocol,
	// TypedDict, etc.) are not allowed in this context.
	EvalFlagsNoNonTypeSpecialForms EvalFlags = 1 << 26

	// EvalFlagsAllowConcatenate allows use of the Concatenate special form.
	EvalFlagsAllowConcatenate EvalFlags = 1 << 27

	// EvalFlagsStripTupleLiterals does not infer literal types within a tuple
	// (used for tuples nested within other container classes).
	EvalFlagsStripTupleLiterals EvalFlags = 1 << 28

	// EvalFlagsIsinstanceArg interprets the expression using the specialized
	// behaviors associated with the second argument to isinstance and
	// issubclass calls.
	EvalFlagsIsinstanceArg EvalFlags = 1 << 29

	// EvalFlagsTypeFormArg interprets the expression using the behaviors
	// associated with the first argument to a TypeForm call.
	EvalFlagsTypeFormArg EvalFlags = 1 << 30

	// EvalFlagsEnforceClassTypeVarScope enforces that any type variables
	// referenced in this type are associated with the enclosing class or an
	// outer scope.
	EvalFlagsEnforceClassTypeVarScope EvalFlags = 1 << 31

	// EvalFlagsCallBaseDefaults are the defaults used for evaluating the LHS of
	// a call expression.
	EvalFlagsCallBaseDefaults = EvalFlagsNoSpecialize

	// EvalFlagsIndexBaseDefaults are the defaults used for evaluating the LHS
	// of a member access expression.
	EvalFlagsIndexBaseDefaults = EvalFlagsNoSpecialize

	// EvalFlagsMemberAccessBaseDefaults are the defaults used for evaluating
	// the LHS of a member access expression.
	EvalFlagsMemberAccessBaseDefaults = EvalFlagsNoSpecialize

	// EvalFlagsIsInstanceArgDefaults are the defaults used for evaluating the
	// second argument of an 'isinstance' or 'issubclass' call.
	EvalFlagsIsInstanceArgDefaults = EvalFlagsAllowMissingTypeArgs |
		EvalFlagsStrLiteralAsType |
		EvalFlagsNoParamSpec |
		EvalFlagsNoTypeVarTuple |
		EvalFlagsNoFinal |
		EvalFlagsNoSpecialize |
		EvalFlagsIsinstanceArg
)

// PrefetchedTypes holds types whose definitions are prefetched and cached by
// the type evaluator.
type PrefetchedTypes struct {
	NoneTypeClass               Type
	ObjectClass                 Type
	TypeClass                   Type
	UnionTypeClass              Type
	AwaitableClass              Type
	FunctionClass               Type
	MethodClass                 Type
	TupleClass                  Type
	BoolClass                   Type
	IntClass                    Type
	StrClass                    Type
	DictClass                   Type
	ModuleTypeClass             Type
	TypedDictClass              Type
	TypedDictPrivateClass       Type
	SupportsKeysAndGetItemClass Type
	MappingClass                Type
	TemplateClass               Type
}

// TypeResult corresponds to the interface of the same name. The original is
// generic in the type of `type`; Go's Type is already an interface, and the
// handful of places the original narrows it (TypeResult<OverloadedType>) assert
// on the concrete type instead.
type TypeResult struct {
	Type Type

	// IsIncomplete asks whether the type is incomplete (i.e. not fully
	// evaluated) because some of the paths involve cyclical dependencies.
	IsIncomplete bool

	// BindToSelfType is used for the output of "super" calls used on the LHS of
	// a member access. The original's comment: normally the type of the LHS is
	// the same as the class or object used to bind the member, but the "super"
	// call can specify a different class or object to bind. Holds a ClassType
	// or a TypeVarType.
	BindToSelfType Type

	UnpackedType Type
	TypeList     []*TypeResultWithNode
	// TypeListPresent distinguishes an empty type list from an absent one,
	// which the original does as `typeList !== undefined` at several points.
	TypeListPresent bool

	// TypeErrors records type consistency errors detected when evaluating this
	// type.
	TypeErrors bool

	// InlinedTypeDict is used for inlined TypedDict definitions.
	InlinedTypeDict *ClassType

	// ClassType is used for getTypeOfBoundMember to indicate the class that
	// declares the member. Holds a ClassType, UnknownType or AnyType.
	ClassType Type

	// IsEmptyTupleShorthand: tuple type arguments allow the shorthand "()" to
	// represent an empty tuple (i.e. Tuple[()]).
	IsEmptyTupleShorthand bool

	// ExpectedTypeDiagAddendum carries additional diagnostic information that
	// explains why the expression type is incompatible with the expected type.
	ExpectedTypeDiagAddendum *common.DiagnosticAddendum

	// IsAsymmetricAccessor asks whether the member is a descriptor object that
	// is asymmetric with respect to __get__ and __set__ types, or is accessed
	// through a __setattr__ method that is asymmetric with respect to the
	// corresponding __getattr__.
	IsAsymmetricAccessor bool

	// NarrowedTypeForSet is, for member access operations that are 'set', the
	// narrowed type when considering the declared type of the member.
	NarrowedTypeForSet Type

	// Is the type wrapped in a "Required", "NotRequired" or "ReadOnly" class?
	IsRequired    bool
	IsNotRequired bool
	IsReadOnly    bool

	// OverloadsUsedForCall records, if a call expression, which overloads were
	// used to satisfy it.
	OverloadsUsedForCall []*FunctionType

	// MemberAccessDeprecationInfo carries, for member access expressions,
	// deprecation messages related to magic methods invoked via the member
	// access.
	MemberAccessDeprecationInfo *MemberAccessDeprecationInfo

	// MagicMethodDeprecationInfo carries deprecation messages related to magic
	// methods.
	MagicMethodDeprecationInfo *MagicMethodDeprecationInfo
}

type TypeResultWithNode struct {
	TypeResult
	Node parser.ParseNode
}

// MemberAccessDeprecationInfo describes deprecation details about a symbol
// accessed via a member access expression, perhaps through a property or
// descriptor accessor method.
type MemberAccessDeprecationInfo struct {
	// AccessType is 'property' or 'descriptor'.
	AccessType string
	// AccessMethod is 'get', 'set' or 'del'.
	AccessMethod      string
	DeprecatedMessage string
}

type MagicMethodDeprecationInfo struct {
	ClassName         string
	MethodName        string
	DeprecatedMessage string
}

// EvaluatorUsage corresponds to the interface of the same name. Method is
// 'get', 'set' or 'del'.
type EvaluatorUsage struct {
	Method string

	// Used only for set methods.
	SetType             *TypeResult
	SetErrorNode        parser.ExpressionNode
	SetExpectedTypeDiag *common.DiagnosticAddendum
}

// EvaluatorUsageGet is the `{ method: 'get' }` the original uses as a default
// argument in several places.
func EvaluatorUsageGet() *EvaluatorUsage { return &EvaluatorUsage{Method: "get"} }

type ClassTypeResult struct {
	ClassType     *ClassType
	DecoratedType Type
}

type FunctionTypeResult struct {
	FunctionType  *FunctionType
	DecoratedType Type
}

type CallSignature struct {
	Type        *FunctionType
	ActiveParam *FunctionParam
}

type CallSignatureInfo struct {
	Signatures []*CallSignature
	CallNode   *parser.CallNode
}

// AbstractSymbol is used to determine whether an abstract method has been
// overridden by a non-abstract method.
type AbstractSymbol struct {
	Symbol            *Symbol
	SymbolName        string
	ClassType         Type
	HasImplementation bool
}

// Arg corresponds to the `ArgWithType | ArgWithExpression` union.
//
// The original expresses the union as two interfaces that each make one of
// ArgBase's optional fields required, which is a compile-time distinction with
// no run-time representation: both are the same object shape. Go has no
// equivalent, so this is the single struct and the two constructors below stand
// in for the narrowing.
type Arg struct {
	ArgCategory     parser.ArgCategory
	Node            *parser.ArgumentNode
	Name            *parser.NameNode
	TypeResult      *TypeResult
	ValueExpression parser.ExpressionNode
	Active          bool
	EnforceIterable bool
}

type EffectiveTypeResult struct {
	Type                         Type
	IsIncomplete                 bool
	IncludesVariableDecl         bool
	IncludesIllegalTypeAliasDecl bool
	IncludesSpeculativeResult    bool
	IsRecursiveDefinition        bool
	EvaluationAttempts           int
}

type ValidateArgTypeParams struct {
	ParamCategory           parser.ParamCategory
	ParamType               Type
	RequiresTypeVarMatching bool
	Argument                *Arg
	IsDefaultArg            bool
	ArgType                 Type
	ErrorNode               parser.ExpressionNode
	ParamName               string
	IsParamNameSynthesized  bool
	MapsToVarArgList        bool
	IsinstanceParam         bool
}

type ExpectedTypeOptions struct {
	AllowFinal                  bool
	AllowRequired               bool
	AllowReadOnly               bool
	AllowUnpackedTuple          bool
	AllowUnpackedTypedDict      bool
	AllowParamSpec              bool
	AllowClassVar               bool
	VarTypeAnnotation           bool
	TypeVarGetsCurScope         bool
	AllowTypeVarsWithoutScopeId bool
	EnforceClassTypeVarScope    bool
	ParsesStringLiteral         bool
	NotParsed                   bool
	NoNonTypeSpecialForms       bool
	TypeFormArg                 bool
	ForwardRefs                 bool
	TypeExpression              bool
	RuntimeTypeExpression       bool
	ConvertEllipsisToAny        bool
	AllowEllipsis               bool
}

type ExpectedTypeResult struct {
	Type       Type
	Node       parser.ParseNode
	Candidates []Type
}

// EnsureExpectedTypeCandidates corresponds to the function of the same name.
func EnsureExpectedTypeCandidates(t Type, candidates []Type) []Type {
	if len(candidates) > 0 {
		return append([]Type{}, candidates...)
	}
	return []Type{t}
}

type FunctionResult struct {
	ReturnType       Type
	ArgumentErrors   bool
	IsTypeIncomplete bool
}

type ArgResult struct {
	IsCompatible                   bool
	ArgType                        Type
	IsTypeIncomplete               bool
	Condition                      []*TypeCondition
	SkippedBareTypeVarExpectedType bool
}

type CallResult struct {
	// ReturnType is the specialized return type of the call.
	ReturnType Type

	// IsTypeIncomplete asks whether the return type is incomplete.
	IsTypeIncomplete bool

	// ArgumentErrors records whether any errors were discovered when
	// evaluating argument types.
	ArgumentErrors bool

	// AnyOrUnknownArg records whether one or more arguments evaluated to Any or
	// Unknown, and which. Holds an UnknownType or AnyType.
	AnyOrUnknownArg Type

	// UnpackedArgOfUnknownLength records whether one or more of the arguments
	// was an unpacked iterable or mapping whose length is unknown.
	UnpackedArgOfUnknownLength bool

	// ActiveParam is the parameter associated with the "active" argument (used
	// for the signature help provider).
	ActiveParam *FunctionParam

	// SpecializedInitSelfType: if the call is to an __init__ with an annotated
	// self parameter, this field indicates the specialized type of that self
	// type; this is used for overloaded constructors where the arguments to the
	// constructor influence the specialized type of the constructed object.
	SpecializedInitSelfType Type

	// OverloadsUsedForCall is the overload or overloads used to satisfy the
	// call. There can be multiple overloads in the case where the call type is
	// a union or we have used union expansion for arguments.
	OverloadsUsedForCall []*FunctionType

	// ArgResults holds the types of individual arguments.
	ArgResults []*ArgResult
}

type ClassMemberLookup struct {
	Symbol *Symbol

	// Type of symbol.
	Type             Type
	IsTypeIncomplete bool

	// IsDescriptorError is true if binding or descriptor access failed.
	IsDescriptorError bool

	// IsClassMember is true if a class member, false otherwise.
	IsClassMember bool

	// ClassType is the class that declares the accessed member. Holds a
	// ClassType, UnknownType or AnyType.
	ClassType Type

	// IsClassVar is true if the member is explicitly declared as ClassVar
	// within a Protocol.
	IsClassVar bool

	// IsAsymmetricAccessor asks whether the member is a descriptor object that
	// is asymmetric with respect to __get__ and __set__ types.
	IsAsymmetricAccessor bool

	// NarrowedTypeForSet is, for member access operations that are 'set', the
	// narrowed type when considering the declared type of the member.
	NarrowedTypeForSet Type

	// MemberAccessDeprecationInfo carries deprecation messages related to magic
	// methods invoked via the member access.
	MemberAccessDeprecationInfo *MemberAccessDeprecationInfo
}

type SolveConstraintsOptions struct {
	UseLowerBoundOnly bool
}

// Reachability corresponds to the enum of the same name.
type Reachability int

const (
	ReachabilityReachable Reachability = iota

	// ReachabilityUnreachableStructural: the node is unreachable in the code
	// flow graph and should be reported as an error. This includes situations
	// like code after return statements.
	ReachabilityUnreachableStructural

	// ReachabilityUnreachableStaticCondition: the node is unreachable in the
	// code flow graph due to a statically-evaluated condition such as a
	// TYPE_CHECKER or Python version check.
	ReachabilityUnreachableStaticCondition

	// ReachabilityUnreachableByAnalysis: the node is unreachable according to
	// code flow analysis. The type of one or more expressions has been narrowed
	// to never.
	ReachabilityUnreachableByAnalysis
)

type PrintTypeOptions struct {
	ExpandTypeAlias        bool
	EnforcePythonSyntax    bool
	UseFullyQualifiedNames bool
	UseTypingUnpack        bool
	PrintUnknownWithAny    bool
	PrintTypeVarVariance   bool
	OmitTypeArgsIfUnknown  bool
	DisablePep604          bool
}

type DeclaredSymbolTypeInfo struct {
	Type            Type
	IsTypeAlias     bool
	ExceedsMaxDecls bool
}

type EvaluatorResolveAliasOptions struct {
	AllowExternallyHiddenAccess bool
	SkipFileNeededCheck         bool
}

type ValidateTypeArgsOptions struct {
	AllowEmptyTuple     bool
	AllowTypeVarTuple   bool
	AllowParamSpec      bool
	AllowTypeArgList    bool
	AllowUnpackedTuples bool
}

type EvaluatorMapSubtypesOptions struct {
	ConditionFilter []*TypeCondition
	SortSubtypes    bool
	ExpandCallback  func(t Type) Type
}

type CallSiteEvaluationInfo struct {
	ErrorNode parser.ExpressionNode
	Args      []*ValidateArgTypeParams
}

type SymbolDeclInfo struct {
	Decls            []Declaration
	SynthesizedTypes []*SynthesizedTypeInfo
}

// AssignTypeFlags corresponds to the const enum of the same name. Note that the
// original skips bits 7 and 10; the gaps are preserved.
type AssignTypeFlags int

const (
	AssignTypeFlagsDefault AssignTypeFlags = 0

	// AssignTypeFlagsInvariant requires invariance with respect to class
	// matching. Normally subclasses are allowed.
	AssignTypeFlagsInvariant AssignTypeFlags = 1 << 0

	// AssignTypeFlagsContravariant: the caller has swapped the source and dest
	// types because the types are contravariant. Perform type var matching on
	// dest type vars rather than source type var.
	AssignTypeFlagsContravariant AssignTypeFlags = 1 << 1

	// AssignTypeFlagsSkipRecursiveTypeCheck: we're comparing type compatibility
	// of two distinct recursive types. This has the potential of recursing
	// infinitely. This flag allows us to detect the recursion after the first
	// level of checking.
	AssignTypeFlagsSkipRecursiveTypeCheck AssignTypeFlags = 1 << 2

	// AssignTypeFlagsArgAssignmentFirstPass: during TypeVar solving for a
	// function call, this flag is set if this is the first of multiple passes.
	// It adjusts certain heuristics for constraint solving.
	AssignTypeFlagsArgAssignmentFirstPass AssignTypeFlags = 1 << 3

	// AssignTypeFlagsOverloadOverlap: if the dest is not Any but the src is
	// Any, treat it as incompatible. Also, treat all source TypeVars as their
	// concrete counterparts. This option is used for validating whether
	// overload signatures overlap.
	AssignTypeFlagsOverloadOverlap AssignTypeFlags = 1 << 4

	// AssignTypeFlagsPartialOverloadOverlap: when used in conjunction with
	// OverloadOverlapCheck, look for partial overlaps. For example,
	// `int | list` overlaps partially with `int | str`.
	AssignTypeFlagsPartialOverloadOverlap AssignTypeFlags = 1 << 5

	// AssignTypeFlagsSkipReturnTypeCheck: for function types, skip the return
	// type check.
	AssignTypeFlagsSkipReturnTypeCheck AssignTypeFlags = 1 << 6

	// AssignTypeFlagsRetainLiteralsForTypeVar: in most cases, literals are
	// stripped when assigning to a type variable. This overrides the standard
	// behavior.
	AssignTypeFlagsRetainLiteralsForTypeVar AssignTypeFlags = 1 << 8

	// AssignTypeFlagsSkipSelfClsTypeCheck: when validating the type of a self
	// or cls parameter, allow a type mismatch. This is used in overload
	// consistency validation because overloads can provide explicit type
	// annotations for self or cls.
	AssignTypeFlagsSkipSelfClsTypeCheck AssignTypeFlags = 1 << 9

	// AssignTypeFlagsPopulateExpectedType: we're initially populating the
	// constraints with an expected type, so TypeVars should match the specified
	// type exactly rather than employing narrowing or widening. The variance
	// context determines whether the upper bound, lower bound, or both are
	// established.
	AssignTypeFlagsPopulateExpectedType AssignTypeFlags = 1 << 11

	// AssignTypeFlagsSkipPopulateUnknownExpectedType is used with
	// PopulatingExpectedType; this flag indicates that a TypeVar constraint
	// that is Unknown should be ignored.
	AssignTypeFlagsSkipPopulateUnknownExpectedType AssignTypeFlags = 1 << 12

	// AssignTypeFlagsAllowUnspecifiedTypeArgs: normally, when a class type is
	// assigned to a TypeVar and that class hasn't previously been specialized,
	// it will be specialized with default type arguments (typically "Unknown").
	// This flag skips this step.
	AssignTypeFlagsAllowUnspecifiedTypeArgs AssignTypeFlags = 1 << 13

	// AssignTypeFlagsAllowIsinstanceSpecialForms: normally all special form
	// classes are incompatible with type[T], but a few of them are allowed in
	// the context of an isinstance or issubclass call.
	AssignTypeFlagsAllowIsinstanceSpecialForms AssignTypeFlags = 1 << 14

	// AssignTypeFlagsSkipSelfClsParamCheck: when comparing two methods, skip
	// the type check for the "self" or "cls" parameters. This is used for
	// variance inference and validation.
	AssignTypeFlagsSkipSelfClsParamCheck AssignTypeFlags = 1 << 15

	// AssignTypeFlagsAllowProtocolClassSource: normally a protocol class object
	// cannot be used as a source type. This option overrides this behavior.
	AssignTypeFlagsAllowProtocolClassSource AssignTypeFlags = 1 << 16

	// AssignTypeFlagsDisallowExtraKwargsForTd: when assigning callables, should
	// a kwargs with an unpacked TypedDict disallow additional named arguments
	// if it does not have extraItems?
	AssignTypeFlagsDisallowExtraKwargsForTd AssignTypeFlags = 1 << 17
)

// SrcDestTypes is the anonymous return type of printSrcDestTypes.
type SrcDestTypes struct {
	SourceType string
	DestType   string
}

// TypeEvaluator corresponds to the interface of the same name.
//
// The order below is the original's. Where a method took optional arguments
// they are all present here; see the file header for which ones are pointers
// and why.
type TypeEvaluator interface {
	GetType(node parser.ExpressionNode) Type
	GetTypeResult(node parser.ExpressionNode) *TypeResult
	GetTypeResultForDecorator(node *parser.DecoratorNode) *TypeResult
	GetCachedType(node parser.ExpressionNode) Type
	GetTypeOfExpression(node parser.ExpressionNode, flags EvalFlags, context *InferenceContext) *TypeResult
	GetTypeOfAnnotation(node parser.ExpressionNode, options *ExpectedTypeOptions) Type
	GetTypeOfClass(node *parser.ClassNode) *ClassTypeResult
	CreateSubclass(errorNode parser.ExpressionNode, type1 *ClassType, type2 *ClassType) *ClassType
	GetTypeOfFunction(node *parser.FunctionNode) *FunctionTypeResult
	GetTypeOfExpressionExpectingType(node parser.ExpressionNode, options *ExpectedTypeOptions) *TypeResult
	EvaluateTypeForSubnode(subnode parser.ParseNode, callback func()) *TypeResult
	EvaluateTypesForStatement(node parser.ParseNode)
	EvaluateTypesForMatchStatement(node *parser.MatchNode)
	EvaluateTypesForCaseStatement(node *parser.CaseNode)
	EvaluateTypeOfParam(node *parser.ParameterNode)

	CanBeTruthy(t Type) bool
	CanBeFalsy(t Type) bool
	StripLiteralValue(t Type) Type
	RemoveTruthinessFromType(t Type) Type
	RemoveFalsinessFromType(t Type) Type
	StripTypeGuard(t Type) Type

	SolveAndApplyConstraints(
		t Type,
		constraints *ConstraintTracker,
		applyOptions *ApplyTypeVarOptions,
		solveOptions *SolveConstraintsOptions,
	) Type

	GetExpectedType(node parser.ExpressionNode) *ExpectedTypeResult
	VerifyRaiseExceptionType(node parser.ExpressionNode, allowNone bool)
	VerifyDeleteExpression(node parser.ExpressionNode)
	ValidateOverloadedArgTypes(
		errorNode parser.ExpressionNode,
		argList []*Arg,
		typeResult *TypeResult,
		constraints *ConstraintTracker,
		skipUnknownArgCheck bool,
		inferenceContext *InferenceContext,
	) *CallResult
	ValidateInitSubclassArgs(node *parser.ClassNode, classType *ClassType)

	IsNodeReachable(node parser.ParseNode, sourceNode parser.ParseNode) bool
	IsAfterNodeReachable(node parser.ParseNode) bool
	GetNodeReachability(node parser.ParseNode, sourceNode parser.ParseNode) Reachability
	GetAfterNodeReachability(node parser.ParseNode) Reachability

	IsAsymmetricAccessorAssignment(node parser.ParseNode) bool
	SuppressDiagnostics(node parser.ParseNode, callback func())
	IsSpecialFormClass(classType *ClassType, flags AssignTypeFlags) bool

	GetDeclInfoForStringNode(node *parser.StringNode) *SymbolDeclInfo
	// skipUnreachableCode defaults to true; nil selects the default.
	GetDeclInfoForNameNode(node *parser.NameNode, skipUnreachableCode *bool) *SymbolDeclInfo
	GetTypeForDeclaration(declaration Declaration) *DeclaredSymbolTypeInfo
	ResolveAliasDeclaration(
		declaration Declaration,
		resolveLocalNames bool,
		options *EvaluatorResolveAliasOptions,
	) Declaration
	ResolveAliasDeclarationWithInfo(
		declaration Declaration,
		resolveLocalNames bool,
		options *EvaluatorResolveAliasOptions,
	) *ResolvedAliasInfo
	// emitNotIterableError defaults to true; nil selects the default.
	GetTypeOfIterable(
		typeResult *TypeResult,
		isAsync bool,
		errorNode parser.ExpressionNode,
		emitNotIterableError *bool,
	) *TypeResult
	// emitNotIterableError defaults to true; nil selects the default.
	GetTypeOfIterator(
		typeResult *TypeResult,
		isAsync bool,
		errorNode parser.ExpressionNode,
		emitNotIterableError *bool,
	) *TypeResult
	GetGetterTypeFromProperty(propertyClass *ClassType) Type
	GetTypeOfArg(arg *Arg, inferenceContext *InferenceContext) *TypeResult
	ConvertNodeToArg(node *parser.ArgumentNode) *Arg
	BuildTupleTypesList(entryTypeResults []*TypeResult, stripLiterals bool, convertModules bool) []*TupleTypeArg
	MarkNamesAccessed(node parser.ParseNode, names []string)
	ExpandPromotionTypes(node parser.ParseNode, t Type) Type
	MakeTopLevelTypeVarsConcrete(t Type, makeParamSpecsConcrete bool) Type
	MapSubtypesExpandTypeVars(
		t Type,
		options *EvaluatorMapSubtypesOptions,
		callback func(expandedSubtype Type, unexpandedSubtype Type) Type,
	) Type
	IsTypeSubsumedByOtherType(t Type, otherType Type, allowAnyToSubsume bool) bool
	LookUpSymbolRecursive(node parser.ParseNode, name string, honorCodeFlow bool) *SymbolWithScope
	GetDeclaredTypeOfSymbol(symbol *Symbol) *DeclaredSymbolTypeInfo
	GetEffectiveTypeOfSymbol(symbol *Symbol) Type
	GetEffectiveTypeOfSymbolForUsage(symbol *Symbol, usageNode *parser.NameNode, useLastDecl bool) *EffectiveTypeResult
	GetInferredTypeOfDeclaration(symbol *Symbol, decl Declaration) Type
	GetDeclaredTypeForExpression(expression parser.ExpressionNode, usage *EvaluatorUsage) Type
	GetDeclaredReturnType(node *parser.FunctionNode) Type
	GetInferredReturnType(t *FunctionType, callSiteInfo *CallSiteEvaluationInfo) Type
	GetBestOverloadForArgs(
		errorNode parser.ExpressionNode,
		typeResult *TypeResult,
		argList []*Arg,
	) *FunctionType
	GetBuiltInType(node parser.ParseNode, name string) Type
	GetTypeOfMember(member *ClassMember) Type
	// usage defaults to {method:'get'}; nil selects the default.
	GetTypeOfBoundMember(
		errorNode parser.ExpressionNode,
		objectType *ClassType,
		memberName string,
		usage *EvaluatorUsage,
		diag *common.DiagnosticAddendum,
		flags MemberAccessFlags,
		selfType Type,
	) *TypeResult
	GetBoundMagicMethod(
		classType *ClassType,
		memberName string,
		selfType Type,
		errorNode parser.ExpressionNode,
		diag *common.DiagnosticAddendum,
		recursionCount int,
	) Type
	GetTypeOfMagicMethodCall(
		objType Type,
		methodName string,
		argList []*TypeResult,
		errorNode parser.ExpressionNode,
		inferenceContext *InferenceContext,
	) *TypeResult
	BindFunctionToClassOrObject(
		baseType *ClassType,
		memberType Type,
		memberClass *ClassType,
		treatConstructorAsClassMethod bool,
		selfType Type,
		diag *common.DiagnosticAddendum,
		recursionCount int,
	) Type
	GetCallbackProtocolType(objType *ClassType, recursionCount int) Type
	GetCallSignatureInfo(node *parser.CallNode, activeIndex int, activeOrFake bool) *CallSignatureInfo
	GetAbstractSymbols(classType *ClassType) []*AbstractSymbol
	NarrowConstrainedTypeVar(node parser.ParseNode, typeVar *TypeVarType) Type
	IsTypeComparable(leftType Type, rightType Type, assumeIsOperator bool) bool

	AssignType(
		destType Type,
		srcType Type,
		diag *common.DiagnosticAddendum,
		constraints *ConstraintTracker,
		flags AssignTypeFlags,
		recursionCount int,
	) bool
	// enforceParamNames defaults to true; nil selects the default.
	ValidateOverrideMethod(
		baseMethod Type,
		overrideMethod Type,
		baseClass *ClassType,
		diag *common.DiagnosticAddendum,
		enforceParamNames *bool,
	) bool
	ValidateCallArgs(
		errorNode parser.ExpressionNode,
		argList []*Arg,
		callTypeResult *TypeResult,
		constraints *ConstraintTracker,
		skipUnknownArgCheck bool,
		inferenceContext *InferenceContext,
	) *CallResult
	ValidateTypeArg(argResult *TypeResultWithNode, options *ValidateTypeArgsOptions) bool
	AssignTypeToExpression(target parser.ExpressionNode, typeResult *TypeResult, srcExpr parser.ExpressionNode)
	AssignClassToSelf(destType *ClassType, srcType *ClassType, assumedVariance Variance) bool
	GetBuiltInObject(node parser.ParseNode, name string, typeArgs []Type) Type
	GetTypedDictClassType() *ClassType
	GetTupleClassType() *ClassType
	GetDictClassType() *ClassType
	GetStrClassType() *ClassType
	GetObjectType() Type
	GetNoneType() Type
	GetUnionClassType() Type
	GetTypeClassType() *ClassType
	GetTypingType(node parser.ParseNode, symbolName string) Type
	GetTypeCheckerInternalsType(node parser.ParseNode, symbolName string) Type
	InferReturnTypeIfNecessary(t Type)
	InferVarianceForClass(t *ClassType)
	AssignTypeArgs(
		destType *ClassType,
		srcType *ClassType,
		diag *common.DiagnosticAddendum,
		constraints *ConstraintTracker,
		flags AssignTypeFlags,
		recursionCount int,
	) bool
	ReportMissingTypeArgs(node parser.ExpressionNode, t Type, flags EvalFlags) Type

	IsFinalVariable(symbol *Symbol) bool
	IsFinalVariableDeclaration(decl Declaration) bool
	IsExplicitTypeAliasDeclaration(decl Declaration) bool

	AddInformation(message string, node parser.ParseNode, textRange *common.TextRange) *common.Diagnostic
	AddUnreachableCode(node parser.ParseNode, reachability Reachability, textRange common.TextRange)
	AddDeprecated(message string, node parser.ParseNode)

	AddDiagnostic(
		rule DiagnosticRule,
		message string,
		node parser.ParseNode,
		textRange *common.TextRange,
	) *common.Diagnostic
	AddDiagnosticForTextRange(
		fileInfo *AnalyzerFileInfo,
		rule DiagnosticRule,
		message string,
		textRange common.TextRange,
	) *common.Diagnostic

	PrintType(t Type, options *PrintTypeOptions) string
	PrintSrcDestTypes(srcType Type, destType Type) SrcDestTypes
	PrintFunctionParts(t *FunctionType, extraFlags PrintTypeFlags) ([]string, string)

	GetTypeCacheEntryCount() int
	DisposeEvaluator()
	UseSpeculativeMode(speculativeNode parser.ParseNode, callback func(), options *SpeculativeModeOptions)
	IsSpeculativeModeInUse(node parser.ParseNode) bool
	SetTypeResultForNode(node parser.ParseNode, typeResult *TypeResult, flags EvalFlags)

	CheckForCancellation()
	PrintControlFlowGraph(
		flowNode FlowNode,
		reference CodeFlowReferenceExpressionNode,
		callName string,
		logger common.ConsoleInterface,
	)
}
