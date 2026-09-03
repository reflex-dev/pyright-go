/*
 * typeutils_solution.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The ConstraintSolution builders and isTypeAliasRecursive from
 * analyzer/typeUtils.ts (pyright 1.1.412). See the header of typeutils.go for
 * the file split.
 *
 * specializeWithDefaultTypeArgs (2176) and specializeForBaseClass (2231) are
 * not here: both call applySolvedTypeVars, and all three live in
 * typeutils_applysolved.go.
 */

package analyzer

// IsTypeAliasRecursive determines whether the type alias placeholder is used
// directly within the specified type. It's OK if it's used indirectly as a type
// argument.
func IsTypeAliasRecursive(typeAliasPlaceholder *TypeVarType, t Type) bool {
	if t.Base().Category != TypeCategoryUnion {
		if t == Type(typeAliasPlaceholder) {
			return true
		}

		if !IsUnbound(t) && !IsTypeAliasPlaceholder(t) {
			return false
		}

		// Handle the specific case where the type alias directly refers to
		// itself.
		if t.Base().Props == nil || t.Base().Props.TypeAliasInfo == nil {
			return false
		}
		if typeAliasPlaceholder.Shared.RecursiveAlias == nil {
			return false
		}
		return t.Base().Props.TypeAliasInfo.Shared.Name == typeAliasPlaceholder.Shared.RecursiveAlias.Name
	}

	return FindSubtype(t, func(subtype Type) bool {
		tv, ok := AsTypeVar(subtype)
		return ok && tv.Shared == typeAliasPlaceholder.Shared
	}) != nil
}

// BuildSolutionFromSpecializedClass builds a mapping between type parameters
// and their specialized types.
//
// The original notes: for example, if the generic type is Dict[_T1, _T2] and
// the specialized type is Dict[str, int], it returns a map that associates _T1
// with str and _T2 with int.
func BuildSolutionFromSpecializedClass(classType *ClassType) *ConstraintSolution {
	typeParams := ClassTypeGetTypeParams(classType)
	var typeArgs []Type

	if classType.Priv.TupleTypeArgs != nil {
		isTypeArgExplicit := classType.Priv.IsTypeArgExplicit != nil && *classType.Priv.IsTypeArgExplicit
		typeArgs = []Type{
			ConvertToInstance(
				SpecializeTupleClass(
					classType,
					classType.Priv.TupleTypeArgs,
					isTypeArgExplicit,
					true, // isUnpacked
				),
				true, // includeSubclasses, defaulted in the original
			),
		}
	} else {
		typeArgs = classType.Priv.TypeArgs
	}

	return BuildSolution(typeParams, typeArgs)
}

// BuildSolution corresponds to buildSolution. A nil typeArgs stands in for
// `undefined`.
func BuildSolution(typeParams []*TypeVarType, typeArgs []Type) *ConstraintSolution {
	solution := NewConstraintSolution(nil)

	if typeArgs == nil {
		return solution
	}

	for index, typeParam := range typeParams {
		if index < len(typeArgs) {
			solution.SetType(typeParam, typeArgs[index])
		}
	}

	return solution
}
