/*
 * constraintsolver_expected.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/constraintSolver.ts (pyright 1.1.412):
 * addConstraintsForExpectedType.
 *
 * Working BACKWARDS from an expected type. Ordinary constraint solving goes
 * forwards -- an argument is assigned to a parameter and the TypeVars in the
 * parameter get bounds. This does the reverse: given `x: Sequence[int] = [...]`,
 * it asks what `list`'s own type parameter must be for `list[T]` to satisfy
 * `Sequence[int]`, and records T := int before a single element is evaluated.
 *
 * Three cases, in increasing order of work:
 *
 *   - The expected type is Any. Every parameter is pinned to Any and that is the
 *     whole answer.
 *   - The expected type is the SAME generic class. Its type arguments are read
 *     straight off and copied across, with variance deciding which bound each
 *     one lands on -- a covariant parameter constrains only from below, a
 *     contravariant one only from above.
 *   - The expected type is a DIFFERENT class, the `list[T]` versus
 *     `Sequence[int]` case. There is no direct correspondence between the two
 *     parameter lists, so it has to be discovered: both sides are re-specialized
 *     with freshly synthesized TypeVars that carry their own index, an
 *     assignment is run between them, and wherever a synthesized source variable
 *     comes back as the solution for a synthesized destination variable, the two
 *     indices name a correspondence. The real expected type argument is then
 *     assigned to the real target parameter.
 *
 * The synthesized variables are marked exempt from bound checks, because they
 * exist only to be matched and would otherwise fail against the real
 * parameters' bounds. Any solution that survives is passed through
 * transformExpectedType, which strips TypeVars belonging to scopes that are
 * still live at the usage site -- they would otherwise leak into the answer.
 */

package analyzer

// AddConstraintsForExpectedType corresponds to addConstraintsForExpectedType.
func AddConstraintsForExpectedType(
	evaluator TypeEvaluator,
	t *ClassType,
	expectedType Type,
	constraints *ConstraintTracker,
	liveTypeVarScopes []TypeVarScopeId,
	usageOffset int,
) bool {
	if IsAny(expectedType) {
		for _, typeParam := range t.Shared.TypeParams {
			constraints.SetBounds(typeParam, expectedType, expectedType, false)
		}
		return true
	}

	if tv, ok := expectedType.(*TypeVarType); ok && IsTypeVar(expectedType) &&
		TypeVarTypeIsSelf(tv) && tv.Shared.BoundType != nil {
		expectedType = tv.Shared.BoundType
	}

	expectedClass, ok := expectedType.(*ClassType)
	if !ok || !IsClass(expectedType) {
		return false
	}

	// The original's comment: if the expected type is generic (but not
	// specialized), we can't proceed.
	expectedTypeArgs := expectedClass.Priv.TypeArgs
	if expectedTypeArgs == nil {
		return evaluator.AssignType(t, expectedClass, nil, constraints,
			AssignTypeFlagsPopulateExpectedType, 0)
	}

	evaluator.InferVarianceForClass(t)

	// The original's comment: if the expected type is the same as the target type
	// (commonly the case), we can use a faster method.
	if ClassTypeIsSameGenericClass(expectedClass, t, 0) {
		copyExpectedTypeArgsDirectly(expectedClass, constraints, liveTypeVarScopes, usageOffset)
		return true
	}

	return matchExpectedTypeThroughSynthesizedVars(
		evaluator, t, expectedClass, expectedTypeArgs, constraints, liveTypeVarScopes, usageOffset)
}

// copyExpectedTypeArgsDirectly is the same-generic-class fast path. Variance
// decides which bound each argument lands on.
func copyExpectedTypeArgsDirectly(
	expectedType *ClassType,
	constraints *ConstraintTracker,
	liveTypeVarScopes []TypeVarScopeId,
	usageOffset int,
) {
	solution := BuildSolutionFromSpecializedClass(expectedType)

	for _, typeParam := range ClassTypeGetTypeParams(expectedType) {
		typeArgValue := solution.GetMainSolutionSet().GetType(typeParam)

		if typeArgValue != nil && liveTypeVarScopes != nil {
			typeArgValue = TransformExpectedType(typeArgValue, liveTypeVarScopes, usageOffset)
		}

		if typeArgValue == nil {
			continue
		}

		variance := TypeVarTypeGetVariance(typeParam)

		var lowerBound, upperBound Type
		if variance != VarianceCovariant {
			lowerBound = typeArgValue
		}
		if variance != VarianceContravariant {
			upperBound = typeArgValue
		}

		constraints.SetBounds(typeParam, lowerBound, upperBound, false)
	}
}

// matchExpectedTypeThroughSynthesizedVars is the general path, where the two
// classes are different and the correspondence between their parameter lists has
// to be discovered rather than assumed.
func matchExpectedTypeThroughSynthesizedVars(
	evaluator TypeEvaluator,
	t *ClassType,
	expectedType *ClassType,
	expectedTypeArgs []Type,
	constraints *ConstraintTracker,
	liveTypeVarScopes []TypeVarScopeId,
	usageOffset int,
) bool {
	// The original's comment: create a generic version of the expected type.
	expectedTypeScopeId := GetTypeVarScopeID(expectedType)
	expectedParams := ClassTypeGetTypeParams(expectedType)
	synthExpectedTypeArgs := make([]Type, len(expectedParams))
	for index, typeParam := range expectedParams {
		kind := TypeVarKindTypeVar
		if IsParamSpec(typeParam) {
			kind = TypeVarKindParamSpec
		}
		typeVar := TypeVarTypeCreateInstance(synthesizedName("__dest", index), kind)
		typeVar.Shared.IsSynthesized = true

		// The original's comment: use invariance here so we set the lower and upper
		// bound on the TypeVar.
		typeVar.Shared.DeclaredVariance = VarianceInvariant
		typeVar.Priv.ScopeID = expectedTypeScopeId
		synthExpectedTypeArgs[index] = typeVar
	}
	genericExpectedType := ClassTypeSpecialize(expectedType, synthExpectedTypeArgs, nil, false, nil, nil)

	// The original's comment: for each type param in the target type, create a
	// placeholder type variable.
	targetParams := ClassTypeGetTypeParams(t)
	typeArgs := make([]Type, len(targetParams))
	for index, typeParam := range targetParams {
		kind := TypeVarKindTypeVar
		if IsParamSpec(typeParam) {
			kind = TypeVarKindParamSpec
		}
		typeVar := TypeVarTypeCreateInstance(synthesizedName("__source", index), kind)
		typeVar.Shared.IsSynthesized = true
		synthIndex := index
		typeVar.Shared.SynthesizedIndex = &synthIndex
		typeVar.Shared.IsExemptFromBoundCheck = true
		typeArgs[index] = TypeVarTypeCloneAsUnificationVar(typeVar, 0)
	}

	specializedType := ClassTypeSpecialize(t, typeArgs, nil, false, nil, nil)
	syntheticConstraints := NewConstraintTracker()
	if !evaluator.AssignType(genericExpectedType, specializedType, nil, syntheticConstraints,
		AssignTypeFlagsPopulateExpectedType, 0) {
		return false
	}

	isResultValid := true

	for index, typeVar := range synthExpectedTypeArgs {
		synthTypeVar, otherSubtypes := resolveSynthesizedMatch(
			evaluator, syntheticConstraints, typeVar.(*TypeVarType))

		// The original's comment: is this one of the synthesized type vars we
		// allocated above? If so, the type arg that corresponds to this type var maps
		// back to the target type.
		resolved, ok := synthTypeVar.(*TypeVarType)
		if !ok || !IsTypeVar(synthTypeVar) || !resolved.Shared.IsSynthesized ||
			resolved.Shared.SynthesizedIndex == nil {
			continue
		}

		targetTypeVar := ClassTypeGetTypeParams(specializedType)[*resolved.Shared.SynthesizedIndex]
		if index >= len(expectedTypeArgs) {
			continue
		}

		typeArgValue := TransformPossibleRecursiveTypeAlias(expectedTypeArgs[index], 0)

		if len(otherSubtypes) > 0 {
			typeArgValue = CombineTypes(append([]Type{typeArgValue}, otherSubtypes...), nil)
		}

		if liveTypeVarScopes != nil {
			typeArgValue = TransformExpectedType(typeArgValue, liveTypeVarScopes, usageOffset)
		}

		if typeArgValue == nil || !AssignTypeVar(evaluator, targetTypeVar, typeArgValue, nil,
			constraints, AssignTypeFlagsRetainLiteralsForTypeVar, 0) {
			isResultValid = false
		}
	}

	return isResultValid
}

// resolveSynthesizedMatch reads the solution for one synthesized destination
// variable.
//
// The original's comment: if the resulting type is a union, try to find a
// matching type var and move the remaining subtypes to the "otherSubtypes" array.
func resolveSynthesizedMatch(
	evaluator TypeEvaluator, syntheticConstraints *ConstraintTracker, typeVar *TypeVarType,
) (Type, []Type) {
	synthTypeVar := getTypeVarType(evaluator, syntheticConstraints.GetMainConstraintSet(), typeVar, false)
	otherSubtypes := []Type{}

	if synthTypeVar == nil {
		return nil, otherSubtypes
	}

	if IsParamSpec(typeVar) {
		if fn, ok := synthTypeVar.(*FunctionType); ok {
			synthTypeVar = SimplifyFunctionToParamSpec(fn)
		}
	}

	union, ok := synthTypeVar.(*UnionType)
	if !ok || !IsUnion(synthTypeVar) {
		return synthTypeVar, otherSubtypes
	}

	var foundSynthTypeVar *TypeVarType

	for _, subtype := range SortTypes(unionableToTypes(union.Priv.Subtypes)) {
		tv, isTypeVar := subtype.(*TypeVarType)
		if isTypeVar && IsTypeVar(subtype) && tv.Shared.IsSynthesized &&
			tv.Shared.SynthesizedIndex != nil && foundSynthTypeVar == nil {
			foundSynthTypeVar = tv
		} else {
			otherSubtypes = append(otherSubtypes, subtype)
		}
	}

	if foundSynthTypeVar != nil {
		return foundSynthTypeVar, otherSubtypes
	}

	return synthTypeVar, otherSubtypes
}

// synthesizedName is the original's “ `__dest${index}` “ and
// “ `__source${index}` “.
func synthesizedName(prefix string, index int) string {
	return prefix + itoa(index)
}
