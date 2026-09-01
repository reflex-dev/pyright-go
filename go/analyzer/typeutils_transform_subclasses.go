/*
 * typeutils_transform_subclasses.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The TypeVarTransformer subclasses that do not need applySolvedTypeVars, and
 * the public wrappers that drive them.
 *
 * Transliterated from analyzer/typeUtils.ts (pyright 1.1.412), lines 4045-4058
 * and 4121-4177 plus the wrappers at 1581-1610 and 1625-1637. See the header of
 * typeutils_transform.go for how subclassing works.
 *
 * UniqueFunctionSignatureTransformer (4060), ApplySolvedTypeVarsTransformer
 * (4181) and UnificationTypeTransformer (4510) are not here: each needs
 * applySolvedTypeVars or convertToInstance/convertToInstantiable, which are not
 * ported yet. See STATUS.md.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
)

// ---------------------------------------------------------------------------
// TypeVarDefaultValidator
// ---------------------------------------------------------------------------

// typeVarDefaultValidator validates whether a TypeVar's default type uses any
// other TypeVars that are not currently in scope.
type typeVarDefaultValidator struct {
	*TypeVarTransformer

	liveTypeParams  []*TypeVarType
	invalidTypeVars *common.OrderedSet[string]
}

func newTypeVarDefaultValidator(
	liveTypeParams []*TypeVarType,
	invalidTypeVars *common.OrderedSet[string],
) *typeVarDefaultValidator {
	v := &typeVarDefaultValidator{
		TypeVarTransformer: &TypeVarTransformer{},
		liveTypeParams:     liveTypeParams,
		invalidTypeVars:    invalidTypeVars,
	}
	InitTypeVarTransformer(v.TypeVarTransformer, v)
	return v
}

func (v *typeVarDefaultValidator) TransformTypeVar(typeVar *TypeVarType, recursionCount int) Type {
	var replacementType *TypeVarType
	for _, param := range v.liveTypeParams {
		if param.Shared.Name == typeVar.Shared.Name {
			replacementType = param
			break
		}
	}

	if replacementType == nil || IsParamSpec(replacementType) != IsParamSpec(typeVar) {
		v.invalidTypeVars.Add(typeVar.Shared.Name)
	}

	return UnknownTypeCreate(false)
}

// ValidateTypeVarDefault validates that a default type associated with a
// TypeVar does not refer to other TypeVars or ParamSpecs that are out of scope.
func ValidateTypeVarDefault(
	typeVar *TypeVarType,
	liveTypeParams []*TypeVarType,
	invalidTypeVars *common.OrderedSet[string],
) {
	// If there is no default type or the default type is concrete, there's no
	// need to do any more work here.
	if typeVar.Shared.IsDefaultExplicit && RequiresSpecialization(typeVar.Shared.DefaultType, nil, 0) {
		validator := newTypeVarDefaultValidator(liveTypeParams, invalidTypeVars)
		validator.Apply(typeVar.Shared.DefaultType, 0)
	}
}

// ---------------------------------------------------------------------------
// BoundTypeVarTransform
// ---------------------------------------------------------------------------

// boundTypeVarTransform replaces the free type vars within a type with their
// corresponding bound type vars if they are in one of the specified scopes. If
// scopeIds is nil, all free type vars are replaced.
//
// A nil scopeIds slice stands in for `undefined`; an empty non-nil slice is a
// different thing, and MakeTypeVarsBound distinguishes them.
type boundTypeVarTransform struct {
	*TypeVarTransformer

	scopeIDs    []TypeVarScopeId
	hasScopeIDs bool
}

func newBoundTypeVarTransform(scopeIDs []TypeVarScopeId, hasScopeIDs bool) *boundTypeVarTransform {
	t := &boundTypeVarTransform{
		TypeVarTransformer: &TypeVarTransformer{},
		scopeIDs:           scopeIDs,
		hasScopeIDs:        hasScopeIDs,
	}
	InitTypeVarTransformer(t.TypeVarTransformer, t)
	return t
}

func (t *boundTypeVarTransform) TransformTypeVar(typeVar *TypeVarType, recursionCount int) Type {
	if t.isTypeVarInScope(typeVar) {
		return TypeVarTypeCloneAsBound(typeVar)
	}

	return nil
}

func (t *boundTypeVarTransform) isTypeVarInScope(typeVar *TypeVarType) bool {
	if typeVar.Priv.ScopeID == "" {
		return false
	}

	// If no scopeIds were specified, transform all TypeVars.
	if !t.hasScopeIDs {
		return true
	}

	for _, id := range t.scopeIDs {
		if id == typeVar.Priv.ScopeID {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// FreeTypeVarTransform
// ---------------------------------------------------------------------------

// freeTypeVarTransform replaces the bound type vars within a type with their
// corresponding free type vars.
type freeTypeVarTransform struct {
	*TypeVarTransformer

	scopeIDs []TypeVarScopeId
}

func newFreeTypeVarTransform(scopeIDs []TypeVarScopeId) *freeTypeVarTransform {
	t := &freeTypeVarTransform{
		TypeVarTransformer: &TypeVarTransformer{},
		scopeIDs:           scopeIDs,
	}
	InitTypeVarTransformer(t.TypeVarTransformer, t)
	return t
}

func (t *freeTypeVarTransform) TransformTypeVar(typeVar *TypeVarType, recursionCount int) Type {
	if typeVar.Priv.FreeTypeVar != nil && t.isTypeVarInScope(typeVar.Priv.FreeTypeVar) {
		return typeVar.Priv.FreeTypeVar
	}

	return nil
}

func (t *freeTypeVarTransform) isTypeVarInScope(typeVar *TypeVarType) bool {
	if typeVar.Priv.ScopeID == "" {
		return false
	}

	for _, id := range t.scopeIDs {
		if id == typeVar.Priv.ScopeID {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Wrappers
// ---------------------------------------------------------------------------

// MakeFunctionTypeVarsBound corresponds to makeFunctionTypeVarsBound. The type
// is a FunctionType or OverloadedType.
func MakeFunctionTypeVarsBound(t Type) Type {
	scopeIDs := []TypeVarScopeId{}
	DoForEachSignature(t, func(signature *FunctionType, index int) {
		localScopeID := GetTypeVarScopeID(signature)
		if localScopeID != "" {
			scopeIDs = append(scopeIDs, localScopeID)
		}
	})

	return MakeTypeVarsBound(t, scopeIDs, true)
}

// MakeTypeVarsBound corresponds to makeTypeVarsBound.
//
// The TypeScript's scopeIds parameter is `TypeVarScopeId[] | undefined`, and
// undefined ("transform every TypeVar") behaves differently from an empty array
// ("transform nothing, return the type unchanged"). Go cannot distinguish a nil
// slice from an absent argument at the call site reliably, so hasScopeIDs makes
// it explicit: pass false for the `undefined` case.
func MakeTypeVarsBound(t Type, scopeIDs []TypeVarScopeId, hasScopeIDs bool) Type {
	if hasScopeIDs && len(scopeIDs) == 0 {
		return t
	}

	transformer := newBoundTypeVarTransform(scopeIDs, hasScopeIDs)
	return transformer.Apply(t, 0)
}

// MakeTypeVarsFree corresponds to makeTypeVarsFree.
func MakeTypeVarsFree(t Type, scopeIDs []TypeVarScopeId) Type {
	if len(scopeIDs) == 0 {
		return t
	}

	transformer := newFreeTypeVarTransform(scopeIDs)
	return transformer.Apply(t, 0)
}
