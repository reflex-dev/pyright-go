/*
 * types_class.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * ClassType and the ClassType namespace.
 *
 * Transliterated from analyzer/types.ts (pyright 1.1.412). Split out of
 * types.go only so no single Go file has to carry all 4000 lines.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/parser"
)

// TypedDictEntry corresponds to the interface of the same name.
type TypedDictEntry struct {
	ValueType  Type
	IsRequired bool
	IsReadOnly bool
	IsProvided bool
}

// TypedDictEntries corresponds to the interface of the same name.
type TypedDictEntries struct {
	KnownItems *common.OrderedMap[string, *TypedDictEntry]
	ExtraItems *TypedDictEntry
}

// ClassTypeFlags corresponds to the const enum of the same name.
type ClassTypeFlags int

const (
	ClassTypeFlagsNone ClassTypeFlags = 0

	// ClassTypeFlagsBuiltIn means the class is defined in the "builtins" or
	// "typing" file.
	ClassTypeFlagsBuiltIn ClassTypeFlags = 1 << 0

	// ClassTypeFlagsSpecialBuiltIn means the class requires special-case
	// handling because it exhibits non-standard behavior or is not defined
	// formally as a class. Examples include 'Optional' and 'Union'.
	ClassTypeFlagsSpecialBuiltIn ClassTypeFlags = 1 << 1

	// ClassTypeFlagsTypedDictClass marks a TypedDict class, introduced in
	// PEP 589, which provides a way to specify type hints for dictionaries
	// with different value types and a limited set of static keys.
	ClassTypeFlagsTypedDictClass ClassTypeFlags = 1 << 2

	// ClassTypeFlagsTypedDictMarkedClosed, used in conjunction with
	// TypedDictClass, indicates that the TypedDict class is marked "closed".
	ClassTypeFlagsTypedDictMarkedClosed ClassTypeFlags = 1 << 3

	// ClassTypeFlagsTypedDictEffectivelyClosed, used in conjunction with
	// TypedDictClass, indicates that the TypedDict class is marked "closed" or
	// one or more of its superclasses is marked "closed".
	ClassTypeFlagsTypedDictEffectivelyClosed ClassTypeFlags = 1 << 4

	// ClassTypeFlagsCanOmitDictValues, used in conjunction with TypedDictClass,
	// indicates that the dictionary values can be omitted.
	ClassTypeFlagsCanOmitDictValues ClassTypeFlags = 1 << 5

	// ClassTypeFlagsSupportsAbstractMethods means the class derives from a
	// class that has the ABCMeta metaclass. Such classes are allowed to
	// contain @abstractmethod decorators.
	ClassTypeFlagsSupportsAbstractMethods ClassTypeFlags = 1 << 6

	// ClassTypeFlagsPropertyClass means the class derives from the property
	// class and has the semantics of a property (with optional setter,
	// deleter).
	ClassTypeFlagsPropertyClass ClassTypeFlags = 1 << 7

	// ClassTypeFlagsFinal means the class is decorated with a "@final"
	// decorator indicating that it cannot be subclassed.
	ClassTypeFlagsFinal ClassTypeFlags = 1 << 8

	// ClassTypeFlagsProtocolClass means the class derives directly from
	// "Protocol".
	ClassTypeFlagsProtocolClass ClassTypeFlags = 1 << 9

	// ClassTypeFlagsPseudoGenericClass marks a class whose constructor
	// (__init__ method) does not have annotated types and is treated as though
	// each parameter is a generic type for purposes of type inference.
	ClassTypeFlagsPseudoGenericClass ClassTypeFlags = 1 << 10

	// ClassTypeFlagsRuntimeCheckable marks a protocol class that is "runtime
	// checkable" and can be used in an isinstance call.
	ClassTypeFlagsRuntimeCheckable ClassTypeFlags = 1 << 11

	// ClassTypeFlagsTypingExtensionClass means the type is defined in the
	// typing_extensions.pyi file.
	ClassTypeFlagsTypingExtensionClass ClassTypeFlags = 1 << 12

	// ClassTypeFlagsPartiallyEvaluated means the class type is in the process
	// of being evaluated and is not yet complete. This allows us to detect
	// cases where the class refers to itself (e.g. uses itself as a type
	// argument to one of its generic base classes).
	ClassTypeFlagsPartiallyEvaluated ClassTypeFlags = 1 << 13

	// ClassTypeFlagsHasCustomClassGetItem means the class or one of its
	// ancestors defines a __class_getitem__ method that is used for
	// subscripting. This is not set if the class is generic, and therefore
	// supports standard subscripting semantics.
	ClassTypeFlagsHasCustomClassGetItem ClassTypeFlags = 1 << 14

	// ClassTypeFlagsTupleClass marks the tuple class, which requires
	// special-case handling for its type arguments.
	ClassTypeFlagsTupleClass ClassTypeFlags = 1 << 15

	// ClassTypeFlagsEnumClass means the class has a metaclass of EnumMeta or
	// derives from a class that has this metaclass.
	ClassTypeFlagsEnumClass ClassTypeFlags = 1 << 16

	// ClassTypeFlagsClassProperty marks properties that are defined using the
	// @classmethod decorator.
	ClassTypeFlagsClassProperty ClassTypeFlags = 1 << 17

	// ClassTypeFlagsDefinedInStub means the class is declared within a type
	// stub file.
	ClassTypeFlagsDefinedInStub ClassTypeFlags = 1 << 18

	// ClassTypeFlagsDisjointBase means the class is decorated with
	// @disjoint_base.
	ClassTypeFlagsDisjointBase ClassTypeFlags = 1 << 19

	// ClassTypeFlagsTypeCheckOnly means the class is decorated with
	// @type_check_only.
	ClassTypeFlagsTypeCheckOnly ClassTypeFlags = 1 << 20

	// ClassTypeFlagsNewTypeClass means the class was created with the NewType
	// call.
	ClassTypeFlagsNewTypeClass ClassTypeFlags = 1 << 21

	// ClassTypeFlagsValidTypeAliasClass means the class is allowed to be used
	// as an implicit type alias even though it is not defined using a `class`
	// statement.
	ClassTypeFlagsValidTypeAliasClass ClassTypeFlags = 1 << 22

	// ClassTypeFlagsSpecialFormClass marks a special form, which is not
	// compatible with type[T] and cannot be directly instantiated.
	ClassTypeFlagsSpecialFormClass ClassTypeFlags = 1 << 23

	// ClassTypeFlagsIllegalIsinstanceClass means this class is rejected when
	// used as the second argument to an isinstance or issubclass call.
	ClassTypeFlagsIllegalIsinstanceClass ClassTypeFlags = 1 << 24

	// ClassTypeFlagsEnumMemberSetMayBeIncomplete means the statically-known
	// enum members may not represent the complete runtime member set.
	ClassTypeFlagsEnumMemberSetMayBeIncomplete ClassTypeFlags = 1 << 25

	// ClassTypeFlagsEnumMemberSetMayBeDynamicallyModified means the enum class
	// body can modify its namespace in a way that the binder cannot represent
	// as statically-known member symbols.
	ClassTypeFlagsEnumMemberSetMayBeDynamicallyModified ClassTypeFlags = 1 << 26
)

// DataClassBehaviors corresponds to the interface of the same name.
type DataClassBehaviors struct {
	SkipGenerateInit bool
	SkipGenerateEq   bool
	GenerateOrder    bool
	GenerateSlots    bool
	GenerateHash     bool

	// MatchArgs is a pointer because the original declares it optional and
	// reads it as `matchArgs ?? true` -- absent means "synthesize
	// __match_args__", which is the opposite of the bool zero value. Every
	// other field here is read for truthiness, where undefined and false agree.
	MatchArgs            *bool
	KeywordOnly          bool
	Frozen               bool
	FrozenDefault        bool
	FieldDescriptorNames []string
}

// DataClassEntry corresponds to the interface of the same name.
type DataClassEntry struct {
	Name               string
	ClassType          *ClassType
	MroClass           *ClassType
	IsClassVar         bool
	IsKeywordOnly      bool
	Alias              *string
	HasDefault         bool
	IsDefaultFactory   bool
	NameNode           *parser.NameNode
	TypeAnnotationNode *parser.TypeAnnotationNode
	DefaultExpr        parser.ExpressionNode
	IncludeInInit      bool
	Type               Type
	Converter          *parser.ArgumentNode
}

// TupleTypeArg corresponds to the interface of the same name.
type TupleTypeArg struct {
	Type Type

	// IsUnbounded reports whether the type argument represents a single value
	// or an "unbounded" (zero or more) arguments.
	IsUnbounded bool

	// IsOptional indicates, for tuples captured from a callable, that the
	// corresponding positional parameter has a default argument and can
	// therefore be omitted.
	IsOptional bool
}

// PropertyMethodInfo corresponds to the interface of the same name.
type PropertyMethodInfo struct {
	// MethodType is the decorated function (fget, fset, fdel) for a property.
	// This is normally a single function, but it can be an OverloadedType if
	// the accessor (currently only the setter) is overloaded.
	MethodType Type

	// ClassType is the class that declared this function.
	ClassType *ClassType
}

// ClassDetailsShared corresponds to the interface of the same name. It is
// shared between clones; see the file header of types.go.
type ClassDetailsShared struct {
	Name         string
	FullName     string
	ModuleName   string
	FileUri      uri.Uri
	Flags        ClassTypeFlags
	TypeSourceID TypeSourceId
	BaseClasses  []Type

	// Mro holds `(ClassType | AnyType | UnknownType)[]`.
	Mro []Type

	// Declaration holds a ClassDeclaration or SpecialBuiltInClassDeclaration.
	Declaration Declaration

	// DeclaredMetaclass and EffectiveMetaclass hold
	// `ClassType | UnknownType | undefined`.
	DeclaredMetaclass  Type
	EffectiveMetaclass Type

	Fields         SymbolTable
	TypeParams     []*TypeVarType
	TypeVarScopeID TypeVarScopeId
	DocString      *string

	DataClassEntries        []*DataClassEntry
	DataClassBehaviors      *DataClassBehaviors
	NamedTupleEntries       *common.OrderedSet[string]
	TypedDictEntries        *TypedDictEntries
	TypedDictExtraItemsExpr parser.ExpressionNode
	LocalSlotsNames         []string
	HasNonEmptySlots        bool

	// DeprecatedMessage provides the message to be displayed when the class is
	// used, if the class is decorated with a @deprecated decorator.
	DeprecatedMessage *string

	// ProtocolCompatibility caches protocol classes (indexed by the class full
	// name) that have been determined to be compatible or incompatible with
	// this class. The original types it as `object` to avoid a circular
	// dependency; it is actually a map of ProtocolCompatibility objects, and
	// is left as `any` here for the same reason.
	ProtocolCompatibility any

	// ClassDataClassTransform holds transforms to apply if this class is used
	// as a metaclass or a base class.
	ClassDataClassTransform *DataClassBehaviors

	// RequiresVarianceInference indicates that one or more type parameters has
	// an autovariance, so variance must be inferred.
	RequiresVarianceInference bool

	// IsInstanceHashable is a cached value that indicates whether an instance
	// of this class is hashable (i.e. does not override "__hash__" with None).
	// Nil means "not yet computed".
	IsInstanceHashable *bool

	// SynthesizeMethodsDeferred is a callback for deferred synthesis of
	// methods in the symbol table.
	SynthesizeMethodsDeferred func()

	// SynthesizeDataClassSlotsDeferred is a callback for deferred calculation
	// of slots generated by a dataclass decorator.
	SynthesizeDataClassSlotsDeferred func()

	// CalculateInheritedSlotsNamesDeferred is a callback for calculating
	// inherited slots names.
	CalculateInheritedSlotsNamesDeferred func()
	InheritedSlotsNamesCached            []string
}

// ClassDetailsPriv corresponds to the interface of the same name. It is
// shallow-copied by every clone; see the file header of types.go.
type ClassDetailsPriv struct {
	// TypeArgs correspond to some or all of the type parameters, for a generic
	// class that has been completely or partially specialized.
	TypeArgs []Type

	// IsEmptyContainer allows us to elide the Unknown type arguments of a
	// generic container class (like a list or dict) that is known to contain
	// no elements, when it's safe to do so.
	IsEmptyContainer bool

	// TupleTypeArgs holds the individual type arguments for tuples, where the
	// class definition calls for a single type parameter but the spec allows
	// the programmer to provide an arbitrary number. The TypeArgs field holds
	// the derived non-variadic type argument, which is the union of the tuple
	// type arguments.
	TupleTypeArgs []*TupleTypeArg

	// IsUnpacked distinguishes the case where multiple types are packaged into
	// a tuple internally for matching against a variadic type variable or
	// another unpacked tuple, from a normal tuple.
	IsUnpacked bool

	// IsTypeArgExplicit records whether type arguments, if present, were
	// provided explicitly in the code.
	//
	// The original declares this optional and distinguishes undefined from
	// false in ClassType.specialize, so it is a pointer here.
	IsTypeArgExplicit *bool

	// IncludeSubclasses means this class type represents the class and any
	// classes that derive from it, as opposed to the original class only. This
	// distinction is important in certain scenarios like instantiation of
	// abstract or protocol classes.
	IncludeSubclasses bool

	// IncludePromotions means this class type represents the class and any
	// auto-promotion types that PEP 484 indicates should be treated as
	// subclasses when the type appears within a type annotation.
	//
	// The original distinguishes undefined from false in
	// cloneRemoveTypePromotions, so it is a pointer here.
	IncludePromotions *bool

	// LiteralValue further constrains some types to have literal types (e.g.
	// true or 'string' or 3).
	LiteralValue LiteralValue

	// rare holds the fields that exist only on a small minority of class
	// types: properties, narrowed TypedDicts, `functools.partial`, the
	// `deprecated` class, and the asymmetry caches. The original stores them
	// as optional properties, which V8 lays out only when set; keeping them
	// inline here charged every one of the millions of live class clones 96
	// bytes for fields it does not have. Access goes through the same-named
	// methods below, and cloneSelf copies the block, so a clone owns its
	// fields exactly as it did when they were inline.
	rare *classDetailsPrivRare
}

// classDetailsPrivRare holds the ClassDetailsPriv fields that most class
// types never set. See the `rare` field above.
type classDetailsPrivRare struct {
	// AliasName holds the alias name where the typing module defines aliases
	// for builtin types (e.g. Tuple, List, Dict).
	AliasName *string

	// TypedDictNarrowedEntries is used for "narrowing" of typed dicts where
	// some entries that are not required have been confirmed to be present
	// through the use of a guard expression.
	TypedDictNarrowedEntries *common.OrderedMap[string, *TypedDictEntry]

	// IsTypedDictPartial indicates that the typed dict class should be
	// considered "partial", i.e. all of its entries are effectively
	// NotRequired and only writable entries are considered present, and they
	// are marked read-only. This is used for the TypedDict "update" method.
	IsTypedDictPartial bool

	// IsAsymmetricDescriptor indicates whether the class is an asymmetric
	// descriptor or property -- one where the __get__ and __set__ types
	// differ. Nil means it hasn't been tested yet for asymmetry.
	IsAsymmetricDescriptor *bool

	// IsAsymmetricAttributeAccessor indicates whether the class has an
	// asymmetric __getattr__ and __setattr__ signature.
	IsAsymmetricAttributeAccessor *bool

	// FgetInfo, FsetInfo and FdelInfo are special-case fields for property
	// classes.
	FgetInfo *PropertyMethodInfo
	FsetInfo *PropertyMethodInfo
	FdelInfo *PropertyMethodInfo

	// DeprecatedInstanceMessage provides the deprecated message specifically
	// for instances of the "deprecated" class. This allows these instances to
	// be used as decorators for other classes or functions.
	DeprecatedInstanceMessage *string

	// PartialCallType is a special-case field for the partial class.
	PartialCallType Type
}

// The rare-field readers answer the zero value when the block is absent, which
// is what the inline fields answered before they moved.

func (p *ClassDetailsPriv) AliasName() *string {
	if p.rare == nil {
		return nil
	}
	return p.rare.AliasName
}

func (p *ClassDetailsPriv) TypedDictNarrowedEntries() *common.OrderedMap[string, *TypedDictEntry] {
	if p.rare == nil {
		return nil
	}
	return p.rare.TypedDictNarrowedEntries
}

func (p *ClassDetailsPriv) IsTypedDictPartial() bool {
	return p.rare != nil && p.rare.IsTypedDictPartial
}

func (p *ClassDetailsPriv) IsAsymmetricDescriptor() *bool {
	if p.rare == nil {
		return nil
	}
	return p.rare.IsAsymmetricDescriptor
}

func (p *ClassDetailsPriv) IsAsymmetricAttributeAccessor() *bool {
	if p.rare == nil {
		return nil
	}
	return p.rare.IsAsymmetricAttributeAccessor
}

func (p *ClassDetailsPriv) FgetInfo() *PropertyMethodInfo {
	if p.rare == nil {
		return nil
	}
	return p.rare.FgetInfo
}

func (p *ClassDetailsPriv) FsetInfo() *PropertyMethodInfo {
	if p.rare == nil {
		return nil
	}
	return p.rare.FsetInfo
}

func (p *ClassDetailsPriv) FdelInfo() *PropertyMethodInfo {
	if p.rare == nil {
		return nil
	}
	return p.rare.FdelInfo
}

func (p *ClassDetailsPriv) DeprecatedInstanceMessage() *string {
	if p.rare == nil {
		return nil
	}
	return p.rare.DeprecatedInstanceMessage
}

func (p *ClassDetailsPriv) PartialCallType() Type {
	if p.rare == nil {
		return nil
	}
	return p.rare.PartialCallType
}

// ensureRare allocates the rare block on first write.
func (p *ClassDetailsPriv) ensureRare() *classDetailsPrivRare {
	if p.rare == nil {
		p.rare = &classDetailsPrivRare{}
	}
	return p.rare
}

// ClassType corresponds to the interface of the same name.
type ClassType struct {
	TypeBase
	Shared *ClassDetailsShared
	Priv   ClassDetailsPriv
}

func (t *ClassType) cloneSelf() Type {
	clone := *t
	clone.cloneBaseInto()
	if clone.Priv.rare != nil {
		// The rare block's fields were inline in Priv before they moved (see
		// classDetailsPrivRare); copying the block keeps clone-owns-its-fields
		// semantics identical to the struct copy above.
		rareCopy := *clone.Priv.rare
		clone.Priv.rare = &rareCopy
	}
	return &clone
}

func (t *ClassType) isUnionable() {}

// ClassTypeCreateInstantiable corresponds to ClassType.createInstantiable.
func ClassTypeCreateInstantiable(
	name string,
	fullName string,
	moduleName string,
	fileUri uri.Uri,
	flags ClassTypeFlags,
	typeSourceID TypeSourceId,
	declaredMetaclass Type,
	effectiveMetaclass Type,
	docString *string,
) *ClassType {
	return &ClassType{
		TypeBase: TypeBase{
			Category: TypeCategoryClass,
			Flags:    TypeFlagsInstantiable,
		},
		Shared: &ClassDetailsShared{
			Name:               name,
			FullName:           fullName,
			ModuleName:         moduleName,
			FileUri:            fileUri,
			Flags:              flags,
			TypeSourceID:       typeSourceID,
			BaseClasses:        []Type{},
			DeclaredMetaclass:  declaredMetaclass,
			EffectiveMetaclass: effectiveMetaclass,
			Mro:                []Type{},
			Fields:             NewSymbolTable(),
			TypeParams:         []*TypeVarType{},
			DocString:          docString,
		},
	}
}

// ClassTypeCloneAsInstance corresponds to ClassType.cloneAsInstance. The
// TypeScript defaults includeSubclasses to true.
func ClassTypeCloneAsInstance(t *ClassType, includeSubclasses bool) *ClassType {
	if t.IsInstance() {
		return t
	}

	if includeSubclasses && t.Cached != nil && t.Cached.TypeBaseInstanceType != nil {
		return t.Cached.TypeBaseInstanceType.(*ClassType)
	}

	newInstance := CloneTypeAsInstance(t, includeSubclasses)
	if newInstance.Props != nil && newInstance.Props.SpecialForm != nil {
		newInstance.SetSpecialForm(nil)
	}

	if includeSubclasses {
		newInstance.Priv.IncludeSubclasses = true
	}

	return newInstance
}

// ClassTypeCloneAsInstantiable corresponds to ClassType.cloneAsInstantiable.
// The TypeScript defaults includeSubclasses to true.
func ClassTypeCloneAsInstantiable(t *ClassType, includeSubclasses bool) *ClassType {
	if includeSubclasses && t.Cached != nil && t.Cached.TypeBaseInstantiableType != nil {
		return t.Cached.TypeBaseInstantiableType.(*ClassType)
	}

	newInstance := CloneTypeAsInstantiable(t, includeSubclasses)
	if includeSubclasses {
		newInstance.Priv.IncludeSubclasses = true
	}

	return newInstance
}

// ClassTypeSpecialize corresponds to ClassType.specialize.
//
// isTypeArgExplicit and isEmptyContainer are pointers because the original
// distinguishes `undefined` from `false` for both: a nil isTypeArgExplicit is
// inferred from whether typeArgs was provided, and a nil isEmptyContainer
// leaves the existing value alone.
func ClassTypeSpecialize(
	classType *ClassType,
	typeArgs []Type,
	isTypeArgExplicit *bool,
	includeSubclasses bool,
	tupleTypeArgs []*TupleTypeArg,
	isEmptyContainer *bool,
) *ClassType {
	newClassType := CloneType(classType)

	if len(typeArgs) == 0 {
		newClassType.Priv.TypeArgs = nil
	} else {
		newClassType.Priv.TypeArgs = typeArgs
	}

	// If the user passed undefined for this argument, infer it based on
	// whether typeArgs was provided.
	if isTypeArgExplicit == nil {
		inferred := typeArgs != nil
		isTypeArgExplicit = &inferred
	}

	newClassType.Priv.IsTypeArgExplicit = isTypeArgExplicit

	if includeSubclasses {
		newClassType.Priv.IncludeSubclasses = true
	}

	if tupleTypeArgs != nil {
		newClassType.Priv.TupleTypeArgs = append([]*TupleTypeArg{}, tupleTypeArgs...)
	} else {
		newClassType.Priv.TupleTypeArgs = nil
	}

	if isEmptyContainer != nil {
		newClassType.Priv.IsEmptyContainer = *isEmptyContainer
	}

	return newClassType
}

// ClassTypeCloneIncludeSubclasses corresponds to
// ClassType.cloneIncludeSubclasses. The TypeScript defaults includeSubclasses
// to true.
func ClassTypeCloneIncludeSubclasses(classType *ClassType, includeSubclasses bool) *ClassType {
	if classType.Priv.IncludeSubclasses == includeSubclasses {
		return classType
	}

	newClassType := CloneType(classType)
	newClassType.Priv.IncludeSubclasses = includeSubclasses
	return newClassType
}

// ClassTypeCloneWithLiteral corresponds to ClassType.cloneWithLiteral. A nil
// value stands in for `undefined`.
func ClassTypeCloneWithLiteral(classType *ClassType, value LiteralValue) *ClassType {
	newClassType := CloneType(classType)
	newClassType.Priv.LiteralValue = value

	// Remove type alias information because the type will no longer match that
	// of the type alias definition if we change the literal type.
	if newClassType.Props != nil && newClassType.Props.TypeAliasInfo != nil {
		newClassType.SetTypeAliasInfo(nil)
	}

	return newClassType
}

// ClassTypeCloneForDeprecatedInstance corresponds to
// ClassType.cloneForDeprecatedInstance.
func ClassTypeCloneForDeprecatedInstance(t *ClassType, deprecatedMessage *string) *ClassType {
	newClassType := CloneType(t)
	newClassType.Priv.ensureRare().DeprecatedInstanceMessage = deprecatedMessage
	return newClassType
}

// ClassTypeCloneForTypingAlias corresponds to ClassType.cloneForTypingAlias.
func ClassTypeCloneForTypingAlias(classType *ClassType, aliasName string) *ClassType {
	newClassType := CloneType(classType)
	newClassType.Priv.ensureRare().AliasName = &aliasName
	return newClassType
}

// ClassTypeCloneForNarrowedTypedDictEntries corresponds to
// ClassType.cloneForNarrowedTypedDictEntries.
func ClassTypeCloneForNarrowedTypedDictEntries(
	classType *ClassType,
	narrowedEntries *common.OrderedMap[string, *TypedDictEntry],
) *ClassType {
	newClassType := CloneType(classType)
	newClassType.Priv.ensureRare().TypedDictNarrowedEntries = narrowedEntries
	return newClassType
}

// ClassTypeCloneForPartialTypedDict corresponds to
// ClassType.cloneForPartialTypedDict.
func ClassTypeCloneForPartialTypedDict(classType *ClassType) *ClassType {
	newClassType := CloneType(classType)
	newClassType.Priv.ensureRare().IsTypedDictPartial = true
	return newClassType
}

// ClassTypeCloneRemoveTypePromotions corresponds to
// ClassType.cloneRemoveTypePromotions.
func ClassTypeCloneRemoveTypePromotions(classType *ClassType) *ClassType {
	if classType.Priv.IncludePromotions == nil || !*classType.Priv.IncludePromotions {
		return classType
	}

	newClassType := CloneType(classType)
	if newClassType.Priv.IncludePromotions != nil {
		newClassType.Priv.IncludePromotions = nil
	}
	return newClassType
}

// ClassTypeCloneForPartial corresponds to ClassType.cloneForPartial.
func ClassTypeCloneForPartial(classType *ClassType, partialCallType Type) *ClassType {
	newClassType := CloneType(classType)
	newClassType.Priv.ensureRare().PartialCallType = partialCallType
	return newClassType
}

// ClassTypeCloneForUnpacked corresponds to ClassType.cloneForUnpacked.
func ClassTypeCloneForUnpacked(classType *ClassType) *ClassType {
	if classType.Priv.IsUnpacked {
		return classType
	}

	newClassType := CloneType(classType)
	newClassType.Priv.IsUnpacked = true
	return newClassType
}

// ClassTypeCloneForPacked corresponds to ClassType.cloneForPacked.
func ClassTypeCloneForPacked(classType *ClassType) *ClassType {
	if !classType.Priv.IsUnpacked {
		return classType
	}

	newClassType := CloneType(classType)
	newClassType.Priv.IsUnpacked = false
	return newClassType
}

// ClassTypeCloneWithNewFlags corresponds to ClassType.cloneWithNewFlags. Note
// that it replaces the shared object rather than mutating it, so the flag
// change is not visible through other clones.
func ClassTypeCloneWithNewFlags(classType *ClassType, newFlags ClassTypeFlags) *ClassType {
	newClassType := CloneType(classType)
	sharedCopy := *newClassType.Shared
	newClassType.Shared = &sharedCopy
	newClassType.Shared.Flags = newFlags
	return newClassType
}

// ClassTypeIsLiteralValueSame corresponds to ClassType.isLiteralValueSame.
func ClassTypeIsLiteralValueSame(type1, type2 *ClassType) bool {
	if type1.Priv.LiteralValue == nil {
		return type2.Priv.LiteralValue == nil
	} else if type2.Priv.LiteralValue == nil {
		return false
	}

	if enum1, ok := type1.Priv.LiteralValue.(*EnumLiteral); ok {
		if enum2, ok := type2.Priv.LiteralValue.(*EnumLiteral); ok {
			return enum1.ItemName == enum2.ItemName
		}
		return false
	}

	if sentinel1, ok := type1.Priv.LiteralValue.(*SentinelLiteral); ok {
		if sentinel2, ok := type2.Priv.LiteralValue.(*SentinelLiteral); ok {
			return sentinel1.ClassFullName == sentinel2.ClassFullName
		}
		return false
	}

	return literalValuesEqual(type1.Priv.LiteralValue, type2.Priv.LiteralValue)
}

// ClassTypeIsTypedDictNarrowedEntriesSame determines whether two typed dict
// classes are equivalent given that one or both have narrowed entries (i.e.
// entries that are guaranteed to be present).
func ClassTypeIsTypedDictNarrowedEntriesSame(type1, type2 *ClassType) bool {
	if type1.Priv.TypedDictNarrowedEntries() != nil {
		if type2.Priv.TypedDictNarrowedEntries() == nil {
			return false
		}

		tdEntries1 := type1.Priv.TypedDictNarrowedEntries()
		tdEntries2 := type2.Priv.TypedDictNarrowedEntries()

		if tdEntries1.Size() != tdEntries2.Size() {
			return false
		}

		for _, key := range tdEntries1.Keys() {
			entry1, _ := tdEntries1.Get(key)
			entry2, ok := tdEntries2.Get(key)
			if !ok {
				return false
			}
			if entry1.IsProvided != entry2.IsProvided {
				return false
			}
		}
	} else if type2.Priv.TypedDictNarrowedEntries() != nil {
		return false
	}

	return true
}

// ClassTypeIsTypedDictNarrower determines whether typed dict class type1 is a
// narrower form of type2, i.e. all of the "narrowed entries" found within
// type2 are also found within type1.
func ClassTypeIsTypedDictNarrower(type1, type2 *ClassType) bool {
	tdEntries2 := type2.Priv.TypedDictNarrowedEntries()
	if tdEntries2 == nil {
		return true
	}

	tdEntries1 := type1.Priv.TypedDictNarrowedEntries()
	if tdEntries1 == nil {
		tdEntries1 = common.NewOrderedMap[string, *TypedDictEntry]()
	}

	for _, key := range tdEntries2.Keys() {
		entry2, _ := tdEntries2.Get(key)
		if entry2.IsProvided {
			entry1, ok := tdEntries1.Get(key)
			if !ok || !entry1.IsProvided {
				return false
			}
		}
	}

	return true
}

// ClassTypeIsUnspecialized reports whether the class is generic but not
// specialized.
func ClassTypeIsUnspecialized(classType *ClassType) bool {
	return len(classType.Shared.TypeParams) > 0 && classType.Priv.TypeArgs == nil
}

// ClassTypeIsSpecialBuiltIn corresponds to ClassType.isSpecialBuiltIn. An
// empty className stands in for the optional parameter being omitted; see
// ClassTypeIsSpecialBuiltInNamed for the form that takes a name.
func ClassTypeIsSpecialBuiltIn(classType *ClassType) bool {
	return (classType.Shared.Flags&ClassTypeFlagsSpecialBuiltIn) != 0 || classType.Priv.AliasName() != nil
}

// ClassTypeIsSpecialBuiltInNamed corresponds to ClassType.isSpecialBuiltIn
// called with a className argument.
func ClassTypeIsSpecialBuiltInNamed(classType *ClassType, className string) bool {
	if !ClassTypeIsSpecialBuiltIn(classType) {
		return false
	}

	return classType.Shared.Name == className
}

// ClassTypeIsBuiltIn corresponds to ClassType.isBuiltIn called with no
// className argument.
func ClassTypeIsBuiltIn(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsBuiltIn) != 0
}

// ClassTypeIsBuiltInNamed corresponds to ClassType.isBuiltIn called with a
// className argument, which the original accepts as either a single name or an
// array of names.
func ClassTypeIsBuiltInNamed(classType *ClassType, classNames ...string) bool {
	if !ClassTypeIsBuiltIn(classType) {
		return false
	}

	// Calling with no names is the `className === undefined` case, which the
	// original answers with the flag check alone.
	if len(classNames) == 0 {
		return true
	}

	for _, name := range classNames {
		if name == classType.Shared.Name || name == classType.Shared.FullName {
			return true
		}
		if classType.Priv.AliasName() != nil && name == *classType.Priv.AliasName() {
			return true
		}
	}

	return false
}

func ClassTypeSupportsAbstractMethods(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsSupportsAbstractMethods) != 0
}

func ClassTypeIsDataClass(classType *ClassType) bool {
	return classType.Shared.DataClassBehaviors != nil
}

func ClassTypeIsDataClassSkipGenerateInit(classType *ClassType) bool {
	return classType.Shared.DataClassBehaviors != nil && classType.Shared.DataClassBehaviors.SkipGenerateInit
}

func ClassTypeIsDataClassSkipGenerateEq(classType *ClassType) bool {
	return classType.Shared.DataClassBehaviors != nil && classType.Shared.DataClassBehaviors.SkipGenerateEq
}

func ClassTypeIsDataClassFrozen(classType *ClassType) bool {
	return classType.Shared.DataClassBehaviors != nil && classType.Shared.DataClassBehaviors.Frozen
}

func ClassTypeIsDataClassGenerateOrder(classType *ClassType) bool {
	return classType.Shared.DataClassBehaviors != nil && classType.Shared.DataClassBehaviors.GenerateOrder
}

func ClassTypeIsDataClassKeywordOnly(classType *ClassType) bool {
	return classType.Shared.DataClassBehaviors != nil && classType.Shared.DataClassBehaviors.KeywordOnly
}

func ClassTypeIsDataClassGenerateSlots(classType *ClassType) bool {
	return classType.Shared.DataClassBehaviors != nil && classType.Shared.DataClassBehaviors.GenerateSlots
}

func ClassTypeIsDataClassGenerateHash(classType *ClassType) bool {
	return classType.Shared.DataClassBehaviors != nil && classType.Shared.DataClassBehaviors.GenerateHash
}

func ClassTypeIsTypeCheckOnly(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsTypeCheckOnly) != 0
}

func ClassTypeIsNewTypeClass(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsNewTypeClass) != 0
}

func ClassTypeIsValidTypeAliasClass(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsValidTypeAliasClass) != 0
}

func ClassTypeIsSpecialFormClass(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsSpecialFormClass) != 0
}

func ClassTypeIsIllegalIsinstanceClass(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsIllegalIsinstanceClass) != 0
}

func ClassTypeIsTypedDictClass(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsTypedDictClass) != 0
}

func ClassTypeIsCanOmitDictValues(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsCanOmitDictValues) != 0
}

func ClassTypeIsTypedDictMarkedClosed(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsTypedDictMarkedClosed) != 0
}

func ClassTypeIsTypedDictEffectivelyClosed(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsTypedDictEffectivelyClosed) != 0
}

func ClassTypeIsEnumClass(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsEnumClass) != 0
}

func ClassTypeIsEnumMemberSetMayBeIncomplete(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsEnumMemberSetMayBeIncomplete) != 0
}

func ClassTypeIsEnumMemberSetMayBeDynamicallyModified(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsEnumMemberSetMayBeDynamicallyModified) != 0
}

func ClassTypeIsPropertyClass(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsPropertyClass) != 0
}

func ClassTypeIsClassProperty(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsClassProperty) != 0
}

func ClassTypeIsFinal(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsFinal) != 0
}

// classTypeIsDisjointBase corresponds to the unexported isDisjointBase.
func classTypeIsDisjointBase(classType *ClassType) bool {
	// The disjoint-base property applies only to nominal classes.
	if ClassTypeIsProtocolClass(classType) || ClassTypeIsTypedDictClass(classType) {
		return false
	}

	if classType.Shared.SynthesizeDataClassSlotsDeferred != nil {
		classType.Shared.SynthesizeDataClassSlotsDeferred()
	}

	return (classType.Shared.Flags&ClassTypeFlagsDisjointBase) != 0 ||
		ClassTypeIsBuiltInNamed(classType, "object") ||
		classType.Shared.HasNonEmptySlots
}

// ClassTypeGetDisjointBase corresponds to ClassType.getDisjointBase.
func ClassTypeGetDisjointBase(classType *ClassType) *ClassType {
	if classTypeIsDisjointBase(classType) {
		return classType
	}

	// An unknown class in the MRO may introduce an unknown disjoint base, but
	// it cannot make two already-known disjoint bases compatible. Preserve the
	// most-derived known candidate so transitive conflicts are still reported.
	candidates := []*ClassType{}
	for _, mroClass := range classType.Shared.Mro {
		if cls, ok := AsInstantiableClass(mroClass); ok && classTypeIsDisjointBase(cls) {
			candidates = append(candidates, cls)
		}
	}

	return ClassTypeGetMostDerivedDisjointBase(candidates)
}

// ClassTypeGetMostDerivedDisjointBase applies the PEP 800 reduction rule to a
// set of disjoint base candidates: it returns the unique candidate that is a
// subclass of every other candidate, or nil if no such candidate exists.
func ClassTypeGetMostDerivedDisjointBase(candidates []*ClassType) *ClassType {
	for _, candidate := range candidates {
		all := true
		for _, otherCandidate := range candidates {
			found := false
			for _, mroClass := range candidate.Shared.Mro {
				if cls, ok := AsInstantiableClass(mroClass); ok && ClassTypeIsSameGenericClass(cls, otherCandidate, 0) {
					found = true
					break
				}
			}
			if !found {
				all = false
				break
			}
		}
		if all {
			return candidate
		}
	}
	return nil
}

func ClassTypeIsProtocolClass(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsProtocolClass) != 0
}

func ClassTypeIsDefinedInStub(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsDefinedInStub) != 0
}

func ClassTypeIsPseudoGenericClass(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsPseudoGenericClass) != 0
}

// ClassTypeGetDataClassEntries corresponds to ClassType.getDataClassEntries.
func ClassTypeGetDataClassEntries(classType *ClassType) []*DataClassEntry {
	if classType.Shared.SynthesizeMethodsDeferred != nil {
		classType.Shared.SynthesizeMethodsDeferred()
	}

	if classType.Shared.DataClassEntries != nil {
		return classType.Shared.DataClassEntries
	}
	return []*DataClassEntry{}
}

func ClassTypeIsRuntimeCheckable(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsRuntimeCheckable) != 0
}

func ClassTypeIsTypingExtensionClass(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsTypingExtensionClass) != 0
}

func ClassTypeIsPartiallyEvaluated(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsPartiallyEvaluated) != 0
}

func ClassTypeHasCustomClassGetItem(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsHasCustomClassGetItem) != 0
}

func ClassTypeIsTupleClass(classType *ClassType) bool {
	return (classType.Shared.Flags & ClassTypeFlagsTupleClass) != 0
}

func ClassTypeGetTypeParams(classType *ClassType) []*TypeVarType {
	return classType.Shared.TypeParams
}

func ClassTypeDerivesFromAnyOrUnknown(classType *ClassType) bool {
	for _, baseClass := range classType.Shared.Mro {
		if IsAnyOrUnknown(baseClass) {
			return true
		}
	}
	return false
}

// ClassTypeGetSymbolTable corresponds to ClassType.getSymbolTable.
func ClassTypeGetSymbolTable(classType *ClassType) SymbolTable {
	if classType.Shared.SynthesizeMethodsDeferred != nil {
		classType.Shared.SynthesizeMethodsDeferred()
	}

	return classType.Shared.Fields
}

// ClassTypeGetInheritedSlotsNames corresponds to
// ClassType.getInheritedSlotsNames.
func ClassTypeGetInheritedSlotsNames(classType *ClassType) []string {
	// First synthesize methods if needed. The slots entries can depend on
	// synthesized methods.
	if classType.Shared.SynthesizeMethodsDeferred != nil {
		classType.Shared.SynthesizeMethodsDeferred()
	}

	if classType.Shared.CalculateInheritedSlotsNamesDeferred != nil {
		classType.Shared.CalculateInheritedSlotsNamesDeferred()
	}

	return classType.Shared.InheritedSlotsNamesCached
}

// ClassTypeIsHierarchyPartiallyEvaluated is similar to
// ClassTypeIsPartiallyEvaluated except that it also looks at all of the classes
// in the MRO list for this class to see if any of them are still partially
// evaluated.
func ClassTypeIsHierarchyPartiallyEvaluated(classType *ClassType) bool {
	if ClassTypeIsPartiallyEvaluated(classType) {
		return true
	}
	for _, mroClass := range classType.Shared.Mro {
		if cls, ok := AsClass(mroClass); ok && ClassTypeIsPartiallyEvaluated(cls) {
			return true
		}
	}
	return false
}

func ClassTypeHasNamedTupleEntry(classType *ClassType, name string) bool {
	if classType.Shared.NamedTupleEntries == nil {
		return false
	}

	return classType.Shared.NamedTupleEntries.Has(name)
}

// ClassTypeIsSameGenericClass is the same as IsTypeSame except that it doesn't
// compare type arguments. The TypeScript defaults recursionCount to 0.
func ClassTypeIsSameGenericClass(classType, type2 *ClassType, recursionCount int) bool {
	if classType.Priv.IsTypedDictPartial() != type2.Priv.IsTypedDictPartial() {
		return false
	}

	if classType.IsInstance() != type2.IsInstance() {
		return false
	}

	if classType.GetInstantiableDepth() != type2.GetInstantiableDepth() {
		return false
	}

	class1Details := classType.Shared
	class2Details := type2.Shared

	if class1Details == class2Details {
		return true
	}

	// Compare most of the details fields. We intentionally skip the
	// isAbstractClass flag because it gets set dynamically.
	if class1Details.FullName != class2Details.FullName ||
		class1Details.Flags != class2Details.Flags ||
		class1Details.TypeSourceID != class2Details.TypeSourceID ||
		len(class1Details.BaseClasses) != len(class2Details.BaseClasses) ||
		len(class1Details.TypeParams) != len(class2Details.TypeParams) {
		return false
	}

	if recursionCount > MaxTypeRecursionCount {
		return true
	}
	recursionCount++

	// Special-case NamedTuple and Tuple classes because we rewrite the base
	// classes in these cases.
	if ClassTypeIsBuiltInNamed(classType, "NamedTuple") && ClassTypeIsBuiltInNamed(type2, "NamedTuple") {
		return true
	}
	if ClassTypeIsBuiltInNamed(classType, "tuple") && ClassTypeIsBuiltInNamed(type2, "tuple") {
		return true
	}

	ignorePseudoGeneric := TypeSameOptions{IgnorePseudoGeneric: true}

	// Make sure the base classes match.
	for i := range class1Details.BaseClasses {
		if !IsTypeSame(class1Details.BaseClasses[i], class2Details.BaseClasses[i], ignorePseudoGeneric, recursionCount) {
			return false
		}
	}

	if class1Details.DeclaredMetaclass != nil || class2Details.DeclaredMetaclass != nil {
		if class1Details.DeclaredMetaclass == nil ||
			class2Details.DeclaredMetaclass == nil ||
			!IsTypeSame(class1Details.DeclaredMetaclass, class2Details.DeclaredMetaclass, ignorePseudoGeneric, recursionCount) {
			return false
		}
	}

	for i := range class1Details.TypeParams {
		if !IsTypeSame(class1Details.TypeParams[i], class2Details.TypeParams[i], ignorePseudoGeneric, recursionCount) {
			return false
		}
	}

	return true
}

// ClassTypeIsDerivedFrom determines whether this is a subclass (derived class)
// of the specified class.
//
// The TypeScript fills in the caller's inheritanceChain array in place. Go
// slices are values, so this takes a pointer; nil means the caller did not pass
// one.
func ClassTypeIsDerivedFrom(
	subclassType *ClassType,
	parentClassType *ClassType,
	inheritanceChain *InheritanceChain,
) bool {
	// Is it the exact same class?
	if ClassTypeIsSameGenericClass(subclassType, parentClassType, 0) {
		// Handle literal types.
		if parentClassType.Priv.LiteralValue != nil {
			if subclassType.Priv.LiteralValue == nil ||
				!ClassTypeIsLiteralValueSame(parentClassType, subclassType) {
				return false
			}
		}

		if inheritanceChain != nil {
			*inheritanceChain = append(*inheritanceChain, subclassType)
		}
		return true
	}

	// Handle built-in types like 'dict' and 'list', which are all subclasses
	// of object even though they are not explicitly declared that way.
	if ClassTypeIsBuiltIn(subclassType) && ClassTypeIsBuiltInNamed(parentClassType, "object") {
		if inheritanceChain != nil {
			*inheritanceChain = append(*inheritanceChain, parentClassType)
		}
		return true
	}

	// Handle the case where the subclass is a type[type[T]] and the parent
	// class is type.
	subclassDepth := subclassType.GetInstantiableDepth()
	if subclassDepth > 0 {
		if ClassTypeIsBuiltInNamed(parentClassType, "type") && parentClassType.GetInstantiableDepth() < subclassDepth {
			if inheritanceChain != nil {
				*inheritanceChain = append(*inheritanceChain, parentClassType)
			}
			return true
		}
	}

	// Handle the case where both source and dest are property objects. This
	// special case is needed because we synthesize a new class for each
	// property declaration.
	if ClassTypeIsBuiltInNamed(subclassType, "property") && ClassTypeIsBuiltInNamed(parentClassType, "property") {
		if inheritanceChain != nil {
			*inheritanceChain = append(*inheritanceChain, subclassType)
		}
		return true
	}

	for _, baseClass := range subclassType.Shared.BaseClasses {
		if cls, ok := AsInstantiableClass(baseClass); ok {
			if ClassTypeIsDerivedFrom(cls, parentClassType, inheritanceChain) {
				if inheritanceChain != nil {
					*inheritanceChain = append(*inheritanceChain, subclassType)
				}
				return true
			}
		} else if IsAnyOrUnknown(baseClass) {
			if inheritanceChain != nil {
				*inheritanceChain = append(*inheritanceChain, UnknownTypeCreate(false))
			}
			return true
		}
	}

	return false
}

// ClassTypeGetReverseMro corresponds to ClassType.getReverseMro.
func ClassTypeGetReverseMro(classType *ClassType) []Type {
	reversed := make([]Type, len(classType.Shared.Mro))
	for i, t := range classType.Shared.Mro {
		reversed[len(classType.Shared.Mro)-1-i] = t
	}
	return reversed
}
