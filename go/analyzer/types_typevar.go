/*
 * types_typevar.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * TypeVarType and the TypeVarType namespace.
 *
 * Transliterated from analyzer/types.ts (pyright 1.1.412). Split out of
 * types.go only so no single Go file has to carry all 4000 lines.
 *
 * The TypeScript declares ParamSpecType and TypeVarTupleType as interfaces that
 * extend TypeVarType with a narrowed `shared.kind` and a few extra `priv`
 * fields. At runtime they are the same object -- the distinction is purely a
 * compile-time narrowing driven by the kind discriminant. Go has no interface
 * extension of that shape, so there is one TypeVarType struct here whose priv
 * carries every field, and ParamSpecType and TypeVarTupleType are aliases.
 * IsParamSpec and IsTypeVarTuple remain the way to test the kind, exactly as in
 * the original.
 */

package analyzer

import "strconv"

// TypeVarDetailsShared corresponds to the interface of the same name.
type TypeVarDetailsShared struct {
	Kind              TypeVarKind
	Name              string
	Constraints       []Type
	BoundType         Type
	IsDefaultExplicit bool
	DefaultType       Type

	DeclaredVariance Variance

	// IsSynthesized marks a TypeVar created internally (e.g. for
	// pseudo-generic classes).
	IsSynthesized     bool
	IsSynthesizedSelf bool

	// SynthesizedIndex is nil where the TypeScript has `number | undefined`.
	SynthesizedIndex       *int
	IsExemptFromBoundCheck bool

	// IsTypeParamSyntax reports whether this type variable originates from
	// PEP 695 type parameter syntax.
	IsTypeParamSyntax bool

	// RecursiveAlias holds information about recursive type aliases.
	RecursiveAlias *TypeAliasSharedInfo
}

// ParamSpecAccess corresponds to the string union `'args' | 'kwargs'` plus
// undefined.
type ParamSpecAccess int

const (
	// ParamSpecAccessNone stands in for `undefined`.
	ParamSpecAccessNone ParamSpecAccess = iota
	ParamSpecAccessArgs
	ParamSpecAccessKwargs
)

// TypeVarScopeType corresponds to the const enum of the same name.
type TypeVarScopeType int

const (
	TypeVarScopeTypeClass TypeVarScopeType = iota
	TypeVarScopeTypeFunction
	TypeVarScopeTypeTypeAlias
)

// TypeVarDetailsPriv corresponds to the interface of the same name, merged with
// ParamSpecDetailsPriv and TypeVarTupleDetailsPriv. See the file header.
type TypeVarDetailsPriv struct {
	// ScopeID uniquely identifies the scope to which this TypeVar is bound. An
	// empty string stands in for `undefined`.
	ScopeID TypeVarScopeId

	// ScopeName is a human-readable name of the function, class, or type alias
	// that provides the scope to which this type variable is bound. Unlike
	// ScopeID, this might not be unique, so it should be used only for error
	// messages. Nil stands in for `undefined`, which cloneForScopeId
	// distinguishes from the empty string.
	ScopeName *string

	// ScopeType is the scope type, if the TypeVar is bound to a scope.
	ScopeType *TypeVarScopeType

	// NameWithScope is formatted as <name>.<scopeId>.<scopeName>.
	NameWithScope string

	// ComputedVariance may be different from DeclaredVariance if declared as
	// Auto. Nil means "not computed".
	ComputedVariance *Variance

	// IsUnificationVar marks a TypeVar cloned for bidirectional type
	// inference. When a TypeVar appears within an expected type, it needs to
	// be solved along with the in-scope TypeVars.
	IsUnificationVar bool

	// FreeTypeVar refers to the corresponding free TypeVar, if this is the
	// bound form of a TypeVar.
	FreeTypeVar *TypeVarType

	// IsUnpacked reports whether this TypeVar or TypeVarTuple is unpacked
	// (i.e. Unpack or the * operator has been applied).
	IsUnpacked bool

	// ParamSpecAccess represents access to "args" or "kwargs" of a ParamSpec.
	// From ParamSpecDetailsPriv.
	ParamSpecAccess ParamSpecAccess

	// IsInUnion reports whether this TypeVarTuple is included in a Union[].
	// This allows us to differentiate between Unpack[Vs] and
	// Union[Unpack[Vs]]. From TypeVarTupleDetailsPriv.
	IsInUnion bool
}

// TypeVarType corresponds to the interface of the same name.
type TypeVarType struct {
	TypeBase
	Shared *TypeVarDetailsShared
	Priv   TypeVarDetailsPriv
}

func (t *TypeVarType) cloneSelf() Type {
	clone := *t
	clone.cloneBaseInto()
	return &clone
}

func (t *TypeVarType) isUnionable() {}

// ParamSpecType is TypeVarType with Shared.Kind == TypeVarKindParamSpec.
type ParamSpecType = TypeVarType

// TypeVarTupleType is TypeVarType with Shared.Kind == TypeVarKindTypeVarTuple.
type TypeVarTupleType = TypeVarType

// ParamSpecTypeGetUnknown returns the "Unknown" equivalent for a ParamSpec.
func ParamSpecTypeGetUnknown() *FunctionType {
	newFunction := FunctionTypeCreateInstance(
		"", "", "",
		FunctionTypeFlagsParamSpecValue|FunctionTypeFlagsGradualCallableForm,
		nil,
	)
	FunctionTypeAddDefaultParams(newFunction, false)
	return newFunction
}

// TypeVarTypeCreateInstance corresponds to TypeVarType.createInstance. The
// TypeScript defaults kind to TypeVarKind.TypeVar.
func TypeVarTypeCreateInstance(name string, kind TypeVarKind) *TypeVarType {
	return typeVarTypeCreate(name, kind, TypeFlagsInstance)
}

// TypeVarTypeCreateInstantiable corresponds to
// TypeVarType.createInstantiable. The TypeScript defaults kind to
// TypeVarKind.TypeVar.
func TypeVarTypeCreateInstantiable(name string, kind TypeVarKind) *TypeVarType {
	return typeVarTypeCreate(name, kind, TypeFlagsInstantiable)
}

// TypeVarTypeCloneAsInstance corresponds to TypeVarType.cloneAsInstance.
func TypeVarTypeCloneAsInstance(t *TypeVarType) *TypeVarType {
	assert(t.IsInstantiable(), "")

	if t.Cached != nil && t.Cached.TypeBaseInstanceType != nil {
		return t.Cached.TypeBaseInstanceType.(*TypeVarType)
	}

	newInstance := CloneTypeAsInstance(t, true)
	if newInstance.Props != nil && newInstance.Props.SpecialForm != nil {
		newInstance.SetSpecialForm(nil)
	}

	if newInstance.Priv.FreeTypeVar != nil {
		newInstance.Priv.FreeTypeVar = TypeVarTypeCloneAsInstance(newInstance.Priv.FreeTypeVar)
	}

	return newInstance
}

// TypeVarTypeCloneAsInstantiable corresponds to
// TypeVarType.cloneAsInstantiable.
func TypeVarTypeCloneAsInstantiable(t *TypeVarType) *TypeVarType {
	if t.Cached != nil && t.Cached.TypeBaseInstantiableType != nil {
		return t.Cached.TypeBaseInstantiableType.(*TypeVarType)
	}

	newInstance := CloneTypeAsInstantiable(t, true)

	if newInstance.Priv.FreeTypeVar != nil {
		newInstance.Priv.FreeTypeVar = TypeVarTypeCloneAsInstantiable(newInstance.Priv.FreeTypeVar)
	}

	return newInstance
}

// TypeVarTypeCloneForNewName corresponds to TypeVarType.cloneForNewName.
func TypeVarTypeCloneForNewName(t *TypeVarType, name string) *TypeVarType {
	newInstance := CloneType(t)
	sharedCopy := *t.Shared
	newInstance.Shared = &sharedCopy
	newInstance.Shared.Name = name

	if newInstance.Priv.ScopeID != "" {
		scopeName := ""
		if newInstance.Priv.ScopeName != nil {
			scopeName = *newInstance.Priv.ScopeName
		}
		newInstance.Priv.NameWithScope = TypeVarTypeMakeNameWithScope(name, newInstance.Priv.ScopeID, scopeName)
	}

	return newInstance
}

// TypeVarTypeCloneForScopeID corresponds to TypeVarType.cloneForScopeId.
func TypeVarTypeCloneForScopeID(
	t *TypeVarType,
	scopeID string,
	scopeName *string,
	scopeType *TypeVarScopeType,
) *TypeVarType {
	newInstance := CloneType(t)
	name := ""
	if scopeName != nil {
		name = *scopeName
	}
	newInstance.Priv.NameWithScope = TypeVarTypeMakeNameWithScope(t.Shared.Name, scopeID, name)
	newInstance.Priv.ScopeID = scopeID
	newInstance.Priv.ScopeName = scopeName
	newInstance.Priv.ScopeType = scopeType
	return newInstance
}

// TypeVarTypeCloneForUnpacked corresponds to TypeVarType.cloneForUnpacked. The
// TypeScript defaults isInUnion to false.
func TypeVarTypeCloneForUnpacked(t *TypeVarType, isInUnion bool) *TypeVarType {
	newInstance := CloneType(t)
	newInstance.Priv.IsUnpacked = true

	if IsTypeVarTuple(newInstance) && isInUnion {
		newInstance.Priv.IsInUnion = isInUnion
	}

	if newInstance.Priv.FreeTypeVar != nil {
		newInstance.Priv.FreeTypeVar = TypeVarTypeCloneForUnpacked(newInstance.Priv.FreeTypeVar, isInUnion)
	}
	return newInstance
}

// TypeVarTypeCloneForPacked corresponds to TypeVarType.cloneForPacked.
func TypeVarTypeCloneForPacked(t *TypeVarType) *TypeVarType {
	newInstance := CloneType(t)
	newInstance.Priv.IsUnpacked = false

	if IsTypeVarTuple(newInstance) {
		newInstance.Priv.IsInUnion = false
	}

	if newInstance.Priv.FreeTypeVar != nil {
		newInstance.Priv.FreeTypeVar = TypeVarTypeCloneForPacked(newInstance.Priv.FreeTypeVar)
	}
	return newInstance
}

// TypeVarTypeCloneAsInvariant creates a "simplified" version of the TypeVar
// with invariance and no bound or constraints. ParamSpecs and TypeVarTuples are
// left unmodified. So are auto-variant type variables.
func TypeVarTypeCloneAsInvariant(t *TypeVarType) *TypeVarType {
	if IsParamSpec(t) || IsTypeVarTuple(t) {
		return t
	}

	if t.Shared.DeclaredVariance == VarianceAuto {
		return t
	}

	if t.Shared.DeclaredVariance == VarianceInvariant {
		if !TypeVarTypeHasBound(t) && !TypeVarTypeHasConstraints(t) {
			return t
		}
	}

	newInstance := CloneType(t)
	sharedCopy := *newInstance.Shared
	newInstance.Shared = &sharedCopy
	newInstance.Shared.DeclaredVariance = VarianceInvariant
	newInstance.Shared.BoundType = nil
	newInstance.Shared.Constraints = []Type{}
	return newInstance
}

// TypeVarTypeCloneForParamSpecAccess corresponds to
// TypeVarType.cloneForParamSpecAccess.
func TypeVarTypeCloneForParamSpecAccess(t *ParamSpecType, access ParamSpecAccess) *ParamSpecType {
	newInstance := CloneType(t)
	newInstance.Priv.ParamSpecAccess = access
	return newInstance
}

// TypeVarTypeCloneAsSpecializedSelf corresponds to
// TypeVarType.cloneAsSpecializedSelf.
func TypeVarTypeCloneAsSpecializedSelf(t *TypeVarType, specializedBoundType Type) *TypeVarType {
	assert(TypeVarTypeIsSelf(t), "")
	newInstance := CloneType(t)
	sharedCopy := *newInstance.Shared
	newInstance.Shared = &sharedCopy
	newInstance.Shared.BoundType = specializedBoundType
	return newInstance
}

// TypeVarTypeCloneAsUnificationVar corresponds to
// TypeVarType.cloneAsUnificationVar. A zero usageOffset stands in for the
// omitted optional argument, matching the original's falsy check.
func TypeVarTypeCloneAsUnificationVar(t *TypeVarType, usageOffset int) *TypeVarType {
	if TypeVarTypeIsUnification(t) {
		return t
	}

	// If the caller specified a usage offset, append it to the TypeVar
	// internal name. This allows us to distinguish it from other uses of the
	// same TypeVar. For example nested calls to a generic function like
	// `foo(foo(1))`.
	newNameWithScope := t.Priv.NameWithScope
	if usageOffset != 0 {
		newNameWithScope = t.Priv.NameWithScope + "-" + strconv.Itoa(usageOffset)
	}

	newInstance := CloneType(t)
	newInstance.Priv.IsUnificationVar = true
	newInstance.Priv.ScopeID = UnificationScopeId
	newInstance.Priv.NameWithScope = newNameWithScope
	return newInstance
}

// TypeVarTypeCloneWithComputedVariance corresponds to
// TypeVarType.cloneWithComputedVariance.
func TypeVarTypeCloneWithComputedVariance(t *TypeVarType, computedVariance Variance) *TypeVarType {
	newInstance := CloneType(t)
	newInstance.Priv.ComputedVariance = &computedVariance
	return newInstance
}

// TypeVarTypeMakeNameWithScope corresponds to TypeVarType.makeNameWithScope.
//
// The original notes: we include the scopeName here even though it's normally
// already part of the scopeId. There are cases where it can diverge,
// specifically in scenarios involving higher-order functions that return
// generic callable types. See adjustCallableReturnType for details.
func TypeVarTypeMakeNameWithScope(name, scopeID, scopeName string) string {
	return name + "." + scopeID + "." + scopeName
}

// TypeVarTypeMakeBoundScopeID corresponds to TypeVarType.makeBoundScopeId.
//
// When solving the TypeVars for a callable, we need to distinguish between the
// externally-visible "free" type vars and the internal "bound" type vars. The
// distinction is important for recursive calls (e.g. calling a constructor for
// a generic class within the class implementation).
func TypeVarTypeMakeBoundScopeID(scopeID TypeVarScopeId) TypeVarScopeId {
	if scopeID == "" {
		return ""
	}

	// Append an asterisk to denote a bound scope.
	return scopeID + "*"
}

// TypeVarTypeCloneAsBound corresponds to TypeVarType.cloneAsBound.
func TypeVarTypeCloneAsBound(t *TypeVarType) *TypeVarType {
	if t.Priv.ScopeID == "" || t.Priv.FreeTypeVar != nil {
		return t
	}

	clone := TypeVarTypeCloneForScopeID(
		t,
		TypeVarTypeMakeBoundScopeID(t.Priv.ScopeID),
		t.Priv.ScopeName,
		t.Priv.ScopeType,
	)

	clone.Priv.FreeTypeVar = t

	return clone
}

// TypeVarTypeIsBound indicates that the type var is a "free" or unbound type
// var. Free type variables can be solved whereas bound type vars are already
// bound to a value.
func TypeVarTypeIsBound(t *TypeVarType) bool {
	// If the type var has an associated free type var, then it's considered
	// bound. If it has no associated free var, then it's considered free.
	return t.Priv.FreeTypeVar != nil
}

func TypeVarTypeIsUnification(t *TypeVarType) bool {
	return t.Priv.IsUnificationVar
}

// typeVarTypeCreate corresponds to the unexported create.
func typeVarTypeCreate(name string, kind TypeVarKind, typeFlags TypeFlags) *TypeVarType {
	return &TypeVarType{
		TypeBase: TypeBase{
			Category: TypeCategoryTypeVar,
			Flags:    typeFlags,
		},
		Shared: &TypeVarDetailsShared{
			Kind:             kind,
			Name:             name,
			Constraints:      []Type{},
			DefaultType:      UnknownTypeCreate(false),
			DeclaredVariance: VarianceInvariant,
		},
	}
}

// TypeVarTypeAddConstraint corresponds to TypeVarType.addConstraint.
func TypeVarTypeAddConstraint(t *TypeVarType, constraintType Type) {
	t.Shared.Constraints = append(t.Shared.Constraints, constraintType)
}

// TypeVarTypeGetNameWithScope corresponds to TypeVarType.getNameWithScope.
func TypeVarTypeGetNameWithScope(typeVarType *TypeVarType) string {
	// If there is no name with scope, fall back on the (unscoped) name.
	if typeVarType.Priv.NameWithScope != "" {
		return typeVarType.Priv.NameWithScope
	}
	return typeVarType.Shared.Name
}

// TypeVarTypeGetReadableName corresponds to TypeVarType.getReadableName. The
// TypeScript defaults includeScope to true.
func TypeVarTypeGetReadableName(t *TypeVarType, includeScope bool) string {
	if t.Priv.ScopeName != nil && *t.Priv.ScopeName != "" && includeScope {
		return t.Shared.Name + "@" + *t.Priv.ScopeName
	}

	return t.Shared.Name
}

// TypeVarTypeGetVariance corresponds to TypeVarType.getVariance.
func TypeVarTypeGetVariance(t *TypeVarType) Variance {
	variance := t.Shared.DeclaredVariance
	if t.Priv.ComputedVariance != nil {
		variance = *t.Priv.ComputedVariance
	}

	// By this point, the variance should have been inferred.
	assert(variance != VarianceAuto, "Expected variance to be inferred")

	// If we're in the process of computing variance, it will still be unknown.
	// Default to covariant in this case.
	if variance == VarianceUnknown {
		return VarianceCovariant
	}

	return variance
}

// TypeVarTypeIsTypeAliasPlaceholder indicates whether the specified type is a
// recursive type alias placeholder that has not yet been resolved.
func TypeVarTypeIsTypeAliasPlaceholder(t *TypeVarType) bool {
	return t.Shared.RecursiveAlias != nil && t.Shared.BoundType == nil
}

func TypeVarTypeIsSelf(t *TypeVarType) bool {
	return t.Shared.IsSynthesizedSelf
}

func TypeVarTypeHasConstraints(t *TypeVarType) bool {
	return len(t.Shared.Constraints) > 0
}

func TypeVarTypeHasBound(t *TypeVarType) bool {
	return t.Shared.BoundType != nil
}
