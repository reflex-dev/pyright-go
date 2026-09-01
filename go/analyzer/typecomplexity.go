/*
 * typecomplexity.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Transliterated from analyzer/typeComplexity.ts (pyright 1.1.412).
 */

package analyzer

// GetComplexityScoreForType returns a "score" for a type that captures the
// relative complexity of the type. Scores should all be between 0 and 1 where 0
// means very simple and 1 means complex. This is a heuristic, so there's often
// no objectively correct answer.
//
// The TypeScript defaults recursionCount to 0.
func GetComplexityScoreForType(t Type, recursionCount int) float64 {
	if recursionCount > MaxTypeRecursionCount {
		return 1
	}
	recursionCount++

	switch t.Base().Category {
	case TypeCategoryUnknown, TypeCategoryAny:
		return 0.5

	case TypeCategoryTypeVar:
		// Assume type[T] is more complex than T.
		if t.Base().IsInstantiable() {
			return 0.55
		}
		return 0.5

	case TypeCategoryFunction, TypeCategoryOverloaded:
		// Classes and unions should be preferred over functions, so make this
		// relatively high (more than 0.75).
		if t.Base().IsInstantiable() {
			return 0.85
		}
		return 0.8

	case TypeCategoryUnbound, TypeCategoryNever:
		return 1.0

	case TypeCategoryUnion:
		maxScore := 0.0
		union := t.(*UnionType)

		// If this union has a very large number of subtypes, don't bother
		// accurately computing the score. Assume a fixed value.
		if len(union.Priv.Subtypes) < 16 {
			for _, subtype := range union.Priv.Subtypes {
				subtypeScore := GetComplexityScoreForType(subtype, recursionCount)
				if subtypeScore > maxScore {
					maxScore = subtypeScore
				}
			}
		} else {
			maxScore = 0.5
		}

		return maxScore

	case TypeCategoryClass:
		return getComplexityScoreForClass(t.(*ClassType), recursionCount)
	}

	// For all other types, return a score of 0.
	return 0
}

func getComplexityScoreForClass(classType *ClassType, recursionCount int) float64 {
	typeArgScoreSum := 0.0
	typeArgCount := 0

	if classType.Priv.TupleTypeArgs != nil {
		for _, typeArg := range classType.Priv.TupleTypeArgs {
			typeArgScoreSum += GetComplexityScoreForType(typeArg.Type, recursionCount)
			typeArgCount++
		}
	} else if classType.Priv.TypeArgs != nil {
		for _, t := range classType.Priv.TypeArgs {
			typeArgScoreSum += GetComplexityScoreForType(t, recursionCount)
			typeArgCount++
		}
	} else if classType.Shared.TypeParams != nil {
		// Note that the original ignores the type parameter itself here and
		// scores Any once per parameter.
		for range classType.Shared.TypeParams {
			typeArgScoreSum += GetComplexityScoreForType(AnyTypeCreate(false), recursionCount)
			typeArgCount++
		}
	}

	averageTypeArgComplexity := 0.0
	if typeArgCount > 0 {
		averageTypeArgComplexity = typeArgScoreSum / float64(typeArgCount)
	}
	result := 0.5 + averageTypeArgComplexity*0.25

	// Assume type[T] is more complex than T.
	if IsInstantiableClass(classType) {
		result += 0.05
	}

	return result
}
