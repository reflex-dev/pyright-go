/*
 * typeutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Functions that operate on Type objects.
 *
 * Transliterated from analyzer/typeUtils.ts (pyright 1.1.412). The file is
 * split across several Go files because the original is 4528 lines:
 *
 *   typeutils.go            - options types, UniqueSignatureTracker, the
 *                             predicates and subtype helpers
 *   typeutils_specialize.go - specialization and TypeVar scope helpers
 *   typeutils_members.go    - class member lookup and the class iterators
 *   typeutils_mro.go        - MRO linearization and variance
 *   typeutils_transform.go  - TypeVarTransformer and its subclasses
 *
 * TypeScript's optional-property interfaces (RequiresSpecializationOptions and
 * friends) become Go structs whose zero value is the all-defaults case, so a
 * nil pointer and a zeroed struct mean the same thing -- matching `options?.x`
 * on an omitted argument.
 */

package analyzer

// ClassMember corresponds to the interface of the same name.
type ClassMember struct {
	// Symbol is the found symbol.
	Symbol *Symbol

	// ClassType is the partially-specialized class that contains the class
	// member. It holds a ClassType, UnknownType or AnyType.
	ClassType Type

	// UnspecializedClassType is the unspecialized class that contains the class
	// member. It holds a ClassType, UnknownType or AnyType.
	UnspecializedClassType Type

	// IsInstanceMember and IsClassMember record whether it is an instance or
	// class member. It can be both, in cases where a class variable is
	// overridden by an instance variable.
	IsInstanceMember bool
	IsClassMember    bool

	// IsSlotsMember reports whether the member is in __slots__.
	IsSlotsMember bool

	// IsClassVar is true if explicitly declared as "ClassVar" and is therefore
	// a type violation if it is overwritten by an instance variable.
	IsClassVar bool

	// IsReadOnly is true if the member is read-only, such as with named tuples
	// or frozen dataclasses.
	IsReadOnly bool

	// IsTypeDeclared is true if the member has a declared type, false if
	// inferred.
	IsTypeDeclared bool

	// SkippedUndeclaredType is true if member lookup skipped an undeclared
	// (inferred) type in a subclass before finding a declared type in a base
	// class.
	SkippedUndeclaredType bool
}

// MemberAccessFlags corresponds to the const enum of the same name.
type MemberAccessFlags int

const (
	MemberAccessFlagsDefault MemberAccessFlags = 0

	// MemberAccessFlagsSkipOriginalClass skips the original (derived) class and
	// searches only the base classes. By default, the original class is
	// searched along with its base classes.
	MemberAccessFlagsSkipOriginalClass MemberAccessFlags = 1 << 0

	// MemberAccessFlagsSkipBaseClasses performs no recursion. By default, base
	// classes are searched as well as the original (derived) class.
	MemberAccessFlagsSkipBaseClasses MemberAccessFlags = 1 << 1

	// MemberAccessFlagsSkipObjectBaseClass skips the 'object' base class in
	// particular.
	MemberAccessFlagsSkipObjectBaseClass MemberAccessFlags = 1 << 2

	// MemberAccessFlagsSkipTypeBaseClass skips the 'type' base class in
	// particular.
	MemberAccessFlagsSkipTypeBaseClass MemberAccessFlags = 1 << 3

	// MemberAccessFlagsSkipInstanceMembers skips the instance variables. By
	// default, both class and instance variables are searched.
	MemberAccessFlagsSkipInstanceMembers MemberAccessFlags = 1 << 4

	// MemberAccessFlagsSkipClassMembers skips the class variables. By default,
	// both class and instance variables are searched.
	MemberAccessFlagsSkipClassMembers MemberAccessFlags = 1 << 5

	// MemberAccessFlagsDeclaredTypesOnly looks only for symbols with declared
	// types. By default, the first symbol is returned even if it has only an
	// inferred type associated with it.
	MemberAccessFlagsDeclaredTypesOnly MemberAccessFlags = 1 << 6

	// MemberAccessFlagsDisallowClassVarWrites considers writes to symbols
	// flagged as ClassVars an error.
	MemberAccessFlagsDisallowClassVarWrites MemberAccessFlags = 1 << 7

	// MemberAccessFlagsTreatConstructorAsClassMethod makes __new__ act like a
	// class method. Normally it is treated as a static method, but when it is
	// invoked implicitly through a constructor call it acts like a class
	// method instead.
	MemberAccessFlagsTreatConstructorAsClassMethod MemberAccessFlags = 1 << 8

	// MemberAccessFlagsSkipAttributeAccessOverride disables the check where an
	// attribute access override method (__getattr__, etc.) may provide a
	// missing attribute type when an attribute cannot be found among instance
	// members.
	MemberAccessFlagsSkipAttributeAccessOverride MemberAccessFlags = 1 << 9

	// MemberAccessFlagsDisallowGenericInstanceVariableAccess reports an error
	// if a symbol is an instance variable whose type is parameterized by a
	// class TypeVar.
	MemberAccessFlagsDisallowGenericInstanceVariableAccess MemberAccessFlags = 1 << 10

	// MemberAccessFlagsTypeExpression treats the member access as if it's
	// within a type expression, reporting errors if it doesn't conform with
	// type expression rules.
	MemberAccessFlagsTypeExpression MemberAccessFlags = 1 << 11

	// MemberAccessFlagsSkipTypedDictEntries skips symbol table entries in the
	// class that correspond to TypedDict entries. These are not considered
	// attributes of the class and cannot be accessed using a member access
	// expression.
	MemberAccessFlagsSkipTypedDictEntries MemberAccessFlags = 1 << 12
)

// ClassIteratorFlags corresponds to the const enum of the same name.
type ClassIteratorFlags int

const (
	ClassIteratorFlagsDefault ClassIteratorFlags = 0

	// ClassIteratorFlagsSkipBaseClasses performs no recursion. By default, base
	// classes are searched as well as the original (derived) class.
	ClassIteratorFlagsSkipBaseClasses ClassIteratorFlags = 1 << 0

	// ClassIteratorFlagsSkipObjectBaseClass skips the 'object' base class in
	// particular.
	ClassIteratorFlagsSkipObjectBaseClass ClassIteratorFlags = 1 << 1

	// ClassIteratorFlagsSkipTypeBaseClass skips the 'type' base class in
	// particular.
	ClassIteratorFlagsSkipTypeBaseClass ClassIteratorFlags = 1 << 2
)

// InferenceContext corresponds to the interface of the same name.
type InferenceContext struct {
	ExpectedType       Type
	IsTypeIncomplete   bool
	ReturnTypeOverride Type
}

// RequiresSpecializationOptions corresponds to the interface of the same name.
type RequiresSpecializationOptions struct {
	// IgnorePseudoGeneric ignores pseudo-generic classes (those with the
	// PseudoGenericClass flag set) when determining whether the type requires
	// specialization.
	IgnorePseudoGeneric bool

	// IgnoreSelf ignores the Self type.
	IgnoreSelf bool

	// IgnoreImplicitTypeArgs ignores classes whose isTypeArgExplicit flag is
	// false.
	IgnoreImplicitTypeArgs bool
}

// IsInstantiableOptions corresponds to the interface of the same name.
type IsInstantiableOptions struct {
	HonorTypeVarBounds bool
}

// SelfSpecializeOptions corresponds to the interface of the same name.
type SelfSpecializeOptions struct {
	// OverrideTypeArgs overrides any existing type arguments. By default,
	// existing type arguments are left as is.
	OverrideTypeArgs bool

	// UseBoundTypeVars specializes with "bound" versions of the type
	// parameters.
	UseBoundTypeVars bool
}

// ReplaceUnsolvedOptions corresponds to the anonymous `replaceUnsolved` object
// in ApplyTypeVarOptions.
type ReplaceUnsolvedOptions struct {
	ScopeIDs                  []TypeVarScopeId
	TupleClassType            *ClassType
	UnsolvedExemptTypeVars    []*TypeVarType
	UseUnknown                bool
	EliminateUnsolvedInUnions bool
}

// ApplyTypeVarOptions corresponds to the interface of the same name.
type ApplyTypeVarOptions struct {
	TypeClassType *ClassType

	// ReplaceUnsolved is nil when the original omits the property, which the
	// transformer distinguishes from an all-defaults object.
	ReplaceUnsolved *ReplaceUnsolvedOptions
}

// AddConditionOptions corresponds to the interface of the same name.
type AddConditionOptions struct {
	SkipSelfCondition bool
	SkipBoundTypeVars bool
}

// maxTupleTypeArgRecursionDepth limits the depth of tuple type arguments.
//
// The original notes: there are cases where tuple types can be infinitely
// nested. The recursion count limit will eventually be hit, but this will
// create deep types that are expensive to construct. As a performance
// safeguard, we limit the depth of the tuple type arguments. This value is
// large enough that we should never hit it in legitimate circumstances.
const maxTupleTypeArgRecursionDepth = 10

// UniqueSignatureTracker tracks whether a function signature has been seen
// before within an expression.
//
// The original notes: for example, in the expression "foo(foo, foo)", the
// signature for "foo" will be seen three times at three different file offsets.
// If the signature is generic, we need to create unique type variables for each
// instance because they are independent of each other.
type UniqueSignatureTracker struct {
	trackedSignatures []*SignatureWithOffsets
}

func NewUniqueSignatureTracker() *UniqueSignatureTracker {
	return &UniqueSignatureTracker{trackedSignatures: []*SignatureWithOffsets{}}
}

func (t *UniqueSignatureTracker) GetTrackedSignatures() []*SignatureWithOffsets {
	return t.trackedSignatures
}

func (t *UniqueSignatureTracker) AddTrackedSignatures(signatures []*SignatureWithOffsets) {
	for _, s := range signatures {
		for _, offset := range s.ExpressionOffsets {
			t.AddSignature(s.Type, offset)
		}
	}
}

// FindSignature corresponds to UniqueSignatureTracker.findSignature. The
// signature is a FunctionType or OverloadedType; it returns nil where the
// TypeScript returns undefined.
func (t *UniqueSignatureTracker) FindSignature(signature Type) *SignatureWithOffsets {
	// Use the associated overload type if this is a function associated with an
	// overload.
	effectiveSignature := signature
	if fn, ok := AsFunction(signature); ok && fn.Priv.Overloaded != nil {
		effectiveSignature = fn.Priv.Overloaded
	}

	for _, s := range t.trackedSignatures {
		if IsTypeSame(effectiveSignature, s.Type, TypeSameOptions{}, 0) {
			return s
		}
	}
	return nil
}

// AddSignature corresponds to UniqueSignatureTracker.addSignature.
func (t *UniqueSignatureTracker) AddSignature(signature Type, offset int) {
	// If this function is part of a broader overload, use the overload instead.
	effectiveSignature := signature
	if fn, ok := AsFunction(signature); ok && fn.Priv.Overloaded != nil {
		effectiveSignature = fn.Priv.Overloaded
	}

	existingSignature := t.FindSignature(effectiveSignature)
	if existingSignature != nil {
		found := false
		for _, o := range existingSignature.ExpressionOffsets {
			if o == offset {
				found = true
				break
			}
		}
		if !found {
			existingSignature.ExpressionOffsets = append(existingSignature.ExpressionOffsets, offset)
		}
	} else {
		t.trackedSignatures = append(t.trackedSignatures, &SignatureWithOffsets{
			Type:              effectiveSignature,
			ExpressionOffsets: []int{offset},
		})
	}
}

// IsOptionalType corresponds to isOptionalType.
func IsOptionalType(t Type) bool {
	if IsUnion(t) {
		return FindSubtype(t, func(subtype Type) bool { return IsNoneInstance(subtype) }) != nil
	}

	return false
}

// IsNoneInstance corresponds to isNoneInstance.
func IsNoneInstance(t Type) bool {
	cls, ok := AsClassInstance(t)
	return ok && ClassTypeIsBuiltInNamed(cls, "NoneType")
}

// IsNoneTypeClass corresponds to isNoneTypeClass.
func IsNoneTypeClass(t Type) bool {
	cls, ok := AsInstantiableClass(t)
	return ok && ClassTypeIsBuiltInNamed(cls, "NoneType")
}

// RemoveNoneFromUnion removes a "None" type from the union, returning only the
// known types, if the type is a union.
func RemoveNoneFromUnion(t Type) Type {
	return RemoveFromUnion(t, func(t Type) bool { return IsNoneInstance(t) })
}

// IsIncompleteUnknown corresponds to isIncompleteUnknown.
func IsIncompleteUnknown(t Type) bool {
	unknown, ok := AsUnknown(t)
	return ok && unknown.Priv.IsIncomplete
}

// IsTypeVarSame is similar to IsTypeSame except that type1 is a TypeVar and
// type2 can be either a TypeVar of the same type or a union that includes
// conditional types associated with that bound TypeVar.
func IsTypeVarSame(type1 *TypeVarType, type2 Type) bool {
	if IsTypeSame(type1, type2, TypeSameOptions{}, 0) {
		return true
	}

	// If this isn't a bound TypeVar, return false.
	if IsParamSpec(type1) || IsTypeVarTuple(type1) || !TypeVarTypeHasBound(type1) {
		return false
	}

	// If the second type isn't a union, return false.
	if !IsUnion(type2) {
		return false
	}

	isCompatible := true
	DoForEachSubtype(type2, func(subtype Type, index int, allSubtypes []Type) {
		if !isCompatible {
			return
		}

		if !IsTypeSame(type1, subtype, TypeSameOptions{}, 0) {
			conditions := GetTypeCondition(subtype)

			matched := false
			for _, condition := range conditions {
				if condition.TypeVar.Priv.NameWithScope == type1.Priv.NameWithScope {
					matched = true
					break
				}
			}

			if conditions == nil || !matched {
				isCompatible = false
			}
		}
	})

	return isCompatible
}

// MakeInferenceContext corresponds to makeInferenceContext. A nil expectedType
// yields a nil context, which is what the TypeScript's first overload
// expresses.
func MakeInferenceContext(expectedType Type, isTypeIncomplete bool, returnTypeOverride Type) *InferenceContext {
	if expectedType == nil {
		return nil
	}

	return &InferenceContext{
		ExpectedType:       expectedType,
		IsTypeIncomplete:   isTypeIncomplete,
		ReturnTypeOverride: returnTypeOverride,
	}
}
