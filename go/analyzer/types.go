/*
 * types.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Representation of types used during type analysis within Python.
 *
 * Transliterated from analyzer/types.ts (pyright 1.1.412).
 *
 * ---------------------------------------------------------------------------
 * How Type is represented, and why
 * ---------------------------------------------------------------------------
 *
 * In the TypeScript, `Type` is a union of ten interfaces that all extend
 * `TypeBase<Category>`, and every type value is a plain object carrying five
 * slots: `category`, `flags`, `props`, `cached`, `shared` and `priv`. The
 * split between `shared` and `priv` is load-bearing and is preserved verbatim:
 *
 *   shared - fields common to every instance of the same declared type. Not
 *            copied by cloneType; two clones point at the same shared object,
 *            so a mutation through one is visible through the other. Some
 *            functions (ClassType.cloneWithNewFlags) deliberately replace the
 *            whole shared object to opt out of that.
 *   priv   - fields private to one type instance. Shallow-copied by every
 *            clone.
 *   props  - optional fields common to all categories. Shallow-copied when
 *            present; absent (nil) most of the time, which the code checks for.
 *   cached - memoized conversions. Dropped by every clone, and mutated on the
 *            *original* by cloneTypeAsInstance/cloneTypeAsInstantiable.
 *
 * Here each category is its own struct embedding TypeBase, and Type is an
 * interface. That keeps the static distinction between ClassType and
 * FunctionType that the TypeScript gets from its union, which matters a great
 * deal with the type evaluator still to come.
 *
 * The slots map onto Go as:
 *
 *   Shared *XDetailsShared - a pointer, so clones alias it and cloneWithNewFlags
 *                            can swap in a fresh one, exactly as the original.
 *   Priv   XDetailsPriv    - a value, so the struct copy in cloneSelf gives the
 *                            shallow copy `{...type.priv}` produces for free.
 *   Props  *TypeBaseProps  - a pointer so nil distinguishes "no props", which
 *                            the original tests with `type.props?.x`.
 *
 * Reference identity is meaningful in the original (the Unbound/Unknown/Never
 * singletons, and `type1 === type2` fast paths in isTypeSame), so every type
 * value is a pointer here.
 *
 * TypeScript's generic `cloneType<T extends TypeBase<any>>(type: T): T` becomes
 * a per-category cloneSelf method plus a generic wrapper that asserts back to
 * T. A reflective copy would work too but would silently do the wrong thing the
 * first time a category grew a field needing special handling.
 */

package analyzer

import (
	"math/big"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// assert is a local shorthand for common.Assert, which the analyzer sources
// import as `assert` from common/debug.
func assert(expression bool, message string) {
	common.Assert(expression, message)
}

// fail is a local shorthand for common.Fail.
func fail(message string) {
	common.Fail(message)
}

// TypeCategory corresponds to the TypeCategory const enum.
type TypeCategory int

const (
	// TypeCategoryUnbound is a name not bound to a value of any type.
	TypeCategoryUnbound TypeCategory = iota

	// TypeCategoryUnknown is the implicit Any type.
	TypeCategoryUnknown

	// TypeCategoryAny means the type can be anything.
	TypeCategoryAny

	// TypeCategoryNever is the bottom type, equivalent to an empty union.
	TypeCategoryNever

	// TypeCategoryFunction is a callable type.
	TypeCategoryFunction

	// TypeCategoryOverloaded is a set of functions defined with the
	// @overload decorator.
	TypeCategoryOverloaded

	// TypeCategoryClass is a class definition.
	TypeCategoryClass

	// TypeCategoryModule is a module instance.
	TypeCategoryModule

	// TypeCategoryUnion is a union of two or more other types.
	TypeCategoryUnion

	// TypeCategoryTypeVar is a type variable.
	TypeCategoryTypeVar
)

// TypeFlags corresponds to the TypeFlags const enum.
type TypeFlags int

const (
	TypeFlagsNone TypeFlags = 0

	// TypeFlagsInstantiable indicates this type refers to something that can
	// be instantiated.
	TypeFlagsInstantiable TypeFlags = 1 << 0

	// TypeFlagsInstance indicates this type refers to something that has
	// been instantiated.
	TypeFlagsInstance TypeFlags = 1 << 1

	// TypeFlagsAmbiguous indicates this type is inferred within a py.typed
	// source file and could be inferred differently by other type checkers.
	TypeFlagsAmbiguous TypeFlags = 1 << 2

	// TypeFlagsTypeCompatibilityMask indicates which flags should be
	// considered significant when comparing two types for equivalence.
	TypeFlagsTypeCompatibilityMask = TypeFlagsInstantiable | TypeFlagsInstance
)

// Type is the interface satisfied by every type category.
//
// The TypeScript writes this as `UnionableType | NeverType | UnionType`; see
// the file header for why it is an interface here.
type Type interface {
	// Base returns the embedded TypeBase. This stands in for reading
	// `.category`, `.flags`, `.props` and `.cached` off the union.
	Base() *TypeBase

	// cloneSelf implements TypeBase.cloneType for one category. It is
	// unexported so only this package can define a Type.
	cloneSelf() Type
}

// UnionableType is the subset of Type that can appear as a subtype of a union,
// i.e. everything except NeverType and UnionType.
type UnionableType interface {
	Type

	isUnionable()
}

// TypeVarScopeId uniquely identifies a TypeVar that is bound to a scope
// (a generic class, function, or type alias).
type TypeVarScopeId = string

// UnificationScopeId is the scope id used for unification TypeVars.
const UnificationScopeId TypeVarScopeId = "-"

// EnumLiteral holds information about an enum member that can be used within a
// Literal type annotation.
type EnumLiteral struct {
	ClassFullName string
	ClassName     string
	ItemName      string
	ItemType      Type
	IsReprEnum    bool
}

func NewEnumLiteral(classFullName, className, itemName string, itemType Type, isReprEnum bool) *EnumLiteral {
	return &EnumLiteral{
		ClassFullName: classFullName,
		ClassName:     className,
		ItemName:      itemName,
		ItemType:      itemType,
		IsReprEnum:    isReprEnum,
	}
}

func (e *EnumLiteral) GetName() string {
	return e.ClassFullName + "." + e.ItemName
}

func (e *EnumLiteral) isLiteralValue() {}

// SentinelLiteral holds information about a sentinel value.
type SentinelLiteral struct {
	ClassFullName string
	ClassName     string
}

func NewSentinelLiteral(classFullName, className string) *SentinelLiteral {
	return &SentinelLiteral{ClassFullName: classFullName, ClassName: className}
}

func (s *SentinelLiteral) GetName() string {
	return s.ClassName
}

func (s *SentinelLiteral) isLiteralValue() {}

// LiteralValue corresponds to
// `number | bigint | boolean | string | EnumLiteral | SentinelLiteral`.
//
// A nil LiteralValue stands in for `undefined`.
type LiteralValue interface {
	isLiteralValue()
}

// LiteralInt is the `bigint` arm. Python integer literals are arbitrary
// precision, so this is the arm that carries them.
type LiteralInt struct {
	Value *big.Int
}

func (LiteralInt) isLiteralValue() {}

// LiteralFloat is the `number` arm.
type LiteralFloat float64

func (LiteralFloat) isLiteralValue() {}

// LiteralBool is the `boolean` arm.
type LiteralBool bool

func (LiteralBool) isLiteralValue() {}

// LiteralString is the `string` arm.
type LiteralString string

func (LiteralString) isLiteralValue() {}

// literalValuesEqual implements the `===` the original applies to two
// LiteralValues once the EnumLiteral and SentinelLiteral cases have been
// handled: value equality for the primitive arms, reference equality
// otherwise.
//
// Note that JavaScript's `===` on two bigints compares by value, not by
// reference, which is why LiteralInt compares with big.Int.Cmp.
func literalValuesEqual(a, b LiteralValue) bool {
	switch av := a.(type) {
	case LiteralInt:
		bv, ok := b.(LiteralInt)
		if !ok {
			return false
		}
		if av.Value == nil || bv.Value == nil {
			return av.Value == bv.Value
		}
		return av.Value.Cmp(bv.Value) == 0
	case LiteralFloat:
		bv, ok := b.(LiteralFloat)
		return ok && av == bv
	case LiteralBool:
		bv, ok := b.(LiteralBool)
		return ok && av == bv
	case LiteralString:
		bv, ok := b.(LiteralString)
		return ok && av == bv
	default:
		return a == b
	}
}

// TypeSourceId corresponds to the TypeSourceId alias.
type TypeSourceId = int

// MaxTypeRecursionCount controls the maximum number of nested types (i.e.
// types used as type arguments or parameter types in other types) before we
// give up.
//
// The original notes: this constant was previously set to 32, but there were
// certain pathological recursive types where this resulted in a hang. It was
// also previously lowered to 10, but this caused some legitimate failures in
// code that used numpy. Even at 16, there are some legitimate failures in
// numpy.
const MaxTypeRecursionCount = 20

// InheritanceChain corresponds to `(ClassType | UnknownType)[]`.
type InheritanceChain = []Type

// TypeSameOptions holds the options used with the IsTypeSame function.
type TypeSameOptions struct {
	IgnorePseudoGeneric          bool
	IgnoreTypeFlags              bool
	IgnoreConditions             bool
	IgnoreTypedDictNarrowEntries bool
	HonorTypeForm                bool
	HonorIsTypeArgExplicit       bool
	TreatAnySameAsUnknown        bool
}

// TypeAliasSharedInfo corresponds to the interface of the same name.
type TypeAliasSharedInfo struct {
	Name       string
	FullName   string
	ModuleName string
	FileUri    uri.Uri

	TypeVarScopeId TypeVarScopeId

	// IsTypeAliasType reports whether the type alias is a PEP 695
	// TypeAliasType instance.
	IsTypeAliasType bool

	// TypeParams holds the type parameters, if the type alias is generic.
	TypeParams []*TypeVarType

	// ComputedVariance is the lazily-evaluated variance of type parameters
	// based on how they are used in the type alias.
	ComputedVariance []Variance
}

// TypeAliasInfo corresponds to the interface of the same name.
type TypeAliasInfo struct {
	Shared *TypeAliasSharedInfo

	// TypeArgs holds the type arguments, if the type alias is specialized.
	TypeArgs []Type
}

// CachedTypeInfo holds memoized conversions. It is never cloned.
type CachedTypeInfo struct {
	// InstantiableType and InstanceType are the type converted by
	// convertToInstance and convertToInstantiable.
	InstantiableType Type
	InstanceType     Type

	// TypeBaseInstantiableType and TypeBaseInstanceType are the type
	// converted by the TypeBase methods.
	TypeBaseInstantiableType Type
	TypeBaseInstanceType     Type

	// RequiresSpecialization is the cached requires-specialization flag.
	// Nil means "not yet computed", which the original expresses with an
	// optional boolean.
	RequiresSpecialization *bool
}

// TypeBaseProps holds the optional properties common to all types.
type TypeBaseProps struct {
	// InstantiableDepth handles nested references to instantiable classes
	// (e.g. type[type[type[T]]]). Nil is treated as zero.
	InstantiableDepth *int

	// SpecialForm is used in cases where the type is a special form when
	// used in a value expression such as UnionType, Literal, or Required.
	SpecialForm *ClassType

	// TypeForm is the evaluated form of a type expression used in a value
	// expression context.
	TypeForm Type

	// TypeAliasInfo is used only for type aliases.
	TypeAliasInfo *TypeAliasInfo

	// Condition is used only for types that are conditioned on a TypeVar.
	Condition []TypeCondition
}

// TypeBase is embedded by every type category.
type TypeBase struct {
	Category TypeCategory
	Flags    TypeFlags

	// Props holds the optional properties common to all types.
	Props *TypeBaseProps

	// Cached holds optional cached values, which are not cloned.
	Cached *CachedTypeInfo
}

// Base satisfies Type for every category through embedding.
func (b *TypeBase) Base() *TypeBase { return b }

func (b *TypeBase) IsInstantiable() bool {
	return (b.Flags & TypeFlagsInstantiable) != 0
}

func (b *TypeBase) IsInstance() bool {
	return (b.Flags & TypeFlagsInstance) != 0
}

func (b *TypeBase) IsAmbiguous() bool {
	return (b.Flags & TypeFlagsAmbiguous) != 0
}

// AddProps corresponds to TypeBase.addProps.
func (b *TypeBase) AddProps() *TypeBaseProps {
	if b.Props == nil {
		b.Props = &TypeBaseProps{}
	}
	return b.Props
}

// GetInstantiableDepth corresponds to TypeBase.getInstantiableDepth.
func (b *TypeBase) GetInstantiableDepth() int {
	if b.Props == nil || b.Props.InstantiableDepth == nil {
		return 0
	}
	return *b.Props.InstantiableDepth
}

// SetSpecialForm corresponds to TypeBase.setSpecialForm.
func (b *TypeBase) SetSpecialForm(specialForm *ClassType) {
	b.AddProps().SpecialForm = specialForm
}

// SetInstantiableDepth corresponds to TypeBase.setInstantiableDepth. A nil
// depth stands in for `undefined`.
func (b *TypeBase) SetInstantiableDepth(depth *int) {
	b.AddProps().InstantiableDepth = depth
}

// SetTypeAliasInfo corresponds to TypeBase.setTypeAliasInfo.
func (b *TypeBase) SetTypeAliasInfo(typeAliasInfo *TypeAliasInfo) {
	b.AddProps().TypeAliasInfo = typeAliasInfo
}

// SetTypeForm corresponds to TypeBase.setTypeForm.
func (b *TypeBase) SetTypeForm(typeForm Type) {
	b.AddProps().TypeForm = typeForm
}

// SetCondition corresponds to TypeBase.setCondition.
func (b *TypeBase) SetCondition(condition []TypeCondition) {
	b.AddProps().Condition = condition
}

// cloneBaseInto performs the parts of TypeBase.cloneType that apply to every
// category: props is shallow-copied when present, and cached is dropped. The
// caller has already copied the struct itself, which covers `{...type}` and
// `clone.priv = {...type.priv}`.
func (b *TypeBase) cloneBaseInto() {
	if b.Props != nil {
		props := *b.Props
		b.Props = &props
	}
	b.Cached = nil
}

// CloneType corresponds to TypeBase.cloneType.
func CloneType[T Type](t T) T {
	return t.cloneSelf().(T)
}

// CloneAsSpecialForm corresponds to TypeBase.cloneAsSpecialForm.
func CloneAsSpecialForm[T Type](t T, specialForm *ClassType) T {
	clone := CloneType(t)
	clone.Base().SetSpecialForm(specialForm)
	return clone
}

// CloneTypeAsInstance corresponds to TypeBase.cloneTypeAsInstance.
//
// Note that this mutates the *original*'s cached slot when cache is true,
// exactly as the original does.
func CloneTypeAsInstance[T Type](t T, cache bool) T {
	assert(t.Base().IsInstantiable(), "expected an instantiable type")

	newInstance := CloneType(t)
	base := newInstance.Base()

	// Remove type form information from the type.
	if base.Props != nil && base.Props.TypeForm != nil {
		base.SetTypeForm(nil)
	}

	var depth *int
	if base.Props != nil {
		depth = base.Props.InstantiableDepth
	}
	if depth == nil {
		base.Flags &^= TypeFlagsInstantiable
		base.Flags |= TypeFlagsInstance
	} else if *depth <= 1 {
		base.SetInstantiableDepth(nil)
	} else {
		newDepth := *depth - 1
		base.SetInstantiableDepth(&newDepth)
	}

	// Should we cache it for next time?
	if cache {
		if t.Base().Cached == nil {
			t.Base().Cached = &CachedTypeInfo{}
		}

		t.Base().Cached.TypeBaseInstanceType = newInstance
	}

	return newInstance
}

// CloneTypeAsInstantiable corresponds to TypeBase.cloneTypeAsInstantiable.
func CloneTypeAsInstantiable[T Type](t T, cache bool) T {
	newInstance := CloneType(t)
	base := newInstance.Base()

	if t.Base().IsInstance() {
		base.Flags &^= TypeFlagsInstance
		base.Flags |= TypeFlagsInstantiable
	} else {
		var oldDepth *int
		if t.Base().Props != nil {
			oldDepth = t.Base().Props.InstantiableDepth
		}
		if oldDepth == nil {
			one := 1
			base.SetInstantiableDepth(&one)
		} else {
			incremented := *oldDepth + 1
			base.SetInstantiableDepth(&incremented)
		}
	}

	// Remove type alias information because the type will no longer match
	// that of the type alias definition.
	if base.Props != nil && base.Props.TypeAliasInfo != nil {
		base.SetTypeAliasInfo(nil)
	}

	// Remove type form information from the type.
	if base.Props != nil && base.Props.TypeForm != nil {
		base.SetTypeForm(nil)
	}

	// Should we cache it for next time?
	if cache {
		if t.Base().Cached == nil {
			t.Base().Cached = &CachedTypeInfo{}
		}

		t.Base().Cached.TypeBaseInstantiableType = newInstance
	}

	return newInstance
}

// CloneForTypeAlias corresponds to TypeBase.cloneForTypeAlias.
func CloneForTypeAlias[T Type](t T, aliasInfo *TypeAliasInfo) T {
	typeClone := CloneType(t)
	typeClone.Base().SetTypeAliasInfo(aliasInfo)
	return typeClone
}

// CloneWithTypeForm corresponds to TypeBase.cloneWithTypeForm.
func CloneWithTypeForm[T Type](t T, typeForm Type) T {
	typeClone := CloneType(t)
	typeClone.Base().SetTypeForm(typeForm)
	return typeClone
}

// CloneForCondition corresponds to TypeBase.cloneForCondition.
func CloneForCondition[T Type](t T, condition []TypeCondition) T {
	// Handle the common case where there are no conditions. In this case,
	// cloning isn't necessary.
	if (t.Base().Props == nil || t.Base().Props.Condition == nil) && condition == nil {
		return t
	}

	typeClone := CloneType(t)
	typeClone.Base().SetCondition(condition)
	return typeClone
}

// CloneForAmbiguousType corresponds to TypeBase.cloneForAmbiguousType.
func CloneForAmbiguousType(t Type) Type {
	if t.Base().IsAmbiguous() {
		return t
	}

	typeClone := CloneType(t)
	typeClone.Base().Flags |= TypeFlagsAmbiguous
	return typeClone
}
