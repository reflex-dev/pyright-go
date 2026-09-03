/*
 * typeutils_specialize.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * TypeVar scope helpers and the "specialize with Unknown" family from
 * analyzer/typeUtils.ts (pyright 1.1.412). See the header of typeutils.go for
 * the file split.
 */

package analyzer

// GetTypeVarScopeID corresponds to getTypeVarScopeId. It returns "" where the
// TypeScript returns undefined, matching TypeVarDetailsPriv.ScopeID.
func GetTypeVarScopeID(t Type) TypeVarScopeId {
	if cls, ok := AsClass(t); ok {
		return cls.Shared.TypeVarScopeID
	}

	if fn, ok := AsFunction(t); ok {
		return fn.Shared.TypeVarScopeID
	}

	if tv, ok := AsTypeVar(t); ok {
		return tv.Priv.ScopeID
	}

	return ""
}

// GetTypeVarScopeIDs is similar to GetTypeVarScopeID except that it includes
// the secondary scope IDs for functions.
func GetTypeVarScopeIDs(t Type) []TypeVarScopeId {
	scopeIDs := []TypeVarScopeId{}

	scopeID := GetTypeVarScopeID(t)
	if scopeID != "" {
		scopeIDs = append(scopeIDs, scopeID)
	}

	if fn, ok := AsFunction(t); ok {
		if fn.Priv.ConstructorTypeVarScopeID != "" {
			scopeIDs = append(scopeIDs, fn.Priv.ConstructorTypeVarScopeID)
		}
	}

	return scopeIDs
}

// SpecializeWithUnknownTypeArgs specializes the class with "Unknown" type args
// (or the equivalent for ParamSpecs or TypeVarTuples). A nil tupleClassType
// stands in for the omitted optional argument.
func SpecializeWithUnknownTypeArgs(t *ClassType, tupleClassType *ClassType) *ClassType {
	if len(t.Shared.TypeParams) == 0 {
		return t
	}

	if IsTupleClass(t) {
		return ClassTypeCloneIncludeSubclasses(
			SpecializeTupleClass(
				t,
				[]*TupleTypeArg{{Type: UnknownTypeCreate(false), IsUnbounded: true}},
				// isTypeArgExplicit. specializeTupleClass defaults this to true
				// and the original overrides it to false right here -- these
				// Unknown arguments were manufactured because the class was used
				// bare, so nothing about them was written down.
				//
				// The distinction is load-bearing two files away. isinstance
				// narrowing refuses to re-specialize a filter class whose type
				// arguments were explicit, so `isinstance(x, tuple)` against an
				// `x: Sequence[Block]` narrowed to `tuple[Unknown, ...]` instead
				// of `tuple[Block, ...]`, and every element read out of it was
				// Unknown.
				false,
				false, // isUnpacked, defaulted in the original
			),
			t.Priv.IncludeSubclasses,
		)
	}

	typeArgs := make([]Type, 0, len(t.Shared.TypeParams))
	for _, param := range t.Shared.TypeParams {
		typeArgs = append(typeArgs, GetUnknownForTypeVar(param, tupleClassType))
	}

	isTypeArgExplicit := false
	return ClassTypeSpecialize(t, typeArgs, &isTypeArgExplicit, t.Priv.IncludeSubclasses, nil, nil)
}

// GetUnknownForTypeVar returns "Unknown" for simple TypeVars or the equivalent
// for a ParamSpec. A nil tupleClassType stands in for the omitted optional
// argument.
func GetUnknownForTypeVar(typeVar *TypeVarType, tupleClassType *ClassType) Type {
	if IsParamSpec(typeVar) {
		return ParamSpecTypeGetUnknown()
	}

	if IsTypeVarTuple(typeVar) && tupleClassType != nil {
		return GetUnknownForTypeVarTuple(tupleClassType)
	}

	return UnknownTypeCreate(false)
}

// GetUnknownForTypeVarTuple corresponds to getUnknownForTypeVarTuple.
func GetUnknownForTypeVarTuple(tupleClassType *ClassType) Type {
	assert(IsInstantiableClass(tupleClassType) && ClassTypeIsBuiltInNamed(tupleClassType, "tuple"), "")

	return ClassTypeCloneAsInstance(
		SpecializeTupleClass(
			tupleClassType,
			[]*TupleTypeArg{{Type: UnknownTypeCreate(false), IsUnbounded: true}},
			true, // isTypeArgExplicit
			true, // isUnpacked
		),
		true, // includeSubclasses, defaulted in the original
	)
}

// GetUnknownTypeForCallable returns the equivalent of "Callable[..., Unknown]".
func GetUnknownTypeForCallable() *FunctionType {
	newFunction := FunctionTypeCreateSynthesizedInstance("", FunctionTypeFlagsGradualCallableForm)
	FunctionTypeAddDefaultParams(newFunction, false)
	newFunction.Shared.DeclaredReturnType = UnknownTypeCreate(false)
	return newFunction
}

// SelfSpecializeClass "self specializes" a generic class that is not already
// specialized, filling in its own type parameters as type arguments. A nil
// options stands in for the omitted optional argument.
func SelfSpecializeClass(t *ClassType, options *SelfSpecializeOptions) *ClassType {
	// We can't use RequiresTypeArgs(t) here because it returns false if the
	// type parameters have default values.
	if len(t.Shared.TypeParams) == 0 {
		return t
	}

	if t.Priv.TypeArgs != nil && (options == nil || !options.OverrideTypeArgs) {
		return t
	}

	typeParams := make([]Type, 0, len(t.Shared.TypeParams))
	for _, typeParam := range t.Shared.TypeParams {
		if IsTypeVarTuple(typeParam) {
			typeParam = TypeVarTypeCloneForUnpacked(typeParam, false)
		}

		if options != nil && options.UseBoundTypeVars {
			typeParam = TypeVarTypeCloneAsBound(typeParam)
		}
		typeParams = append(typeParams, typeParam)
	}

	// The original calls ClassType.specialize with only two arguments, so
	// isTypeArgExplicit is inferred from the presence of typeArgs and the rest
	// take their defaults.
	return ClassTypeSpecialize(t, typeParams, nil, false, nil, nil)
}

// GetTypeVarScopeIds corresponds to getTypeVarScopeIds. A function carries a
// second scope id for the constructor it was synthesized from.
func GetTypeVarScopeIds(t Type) []TypeVarScopeId {
	scopeIds := []TypeVarScopeId{}

	if scopeId := GetTypeVarScopeID(t); scopeId != "" {
		scopeIds = append(scopeIds, scopeId)
	}

	if IsFunction(t) {
		if constructorScopeID := t.(*FunctionType).Priv.ConstructorTypeVarScopeID; constructorScopeID != "" {
			scopeIds = append(scopeIds, constructorScopeID)
		}
	}

	return scopeIds
}
