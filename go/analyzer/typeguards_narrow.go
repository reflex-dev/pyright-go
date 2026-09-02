/*
 * typeguards_narrow.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeGuards.ts (pyright 1.1.412):
 * narrowTypeForUserDefinedTypeGuard, narrowTypeForTruthiness,
 * narrowTupleTypeForIsNone, getInnermostNewTypeBaseInstance,
 * narrowTypeForIsNone, narrowTypeForIsEllipsis.
 *
 * The singleton narrowers. `x is None` and `x is ...` are structurally the same
 * problem -- eliminate everything that cannot be the singleton on the positive
 * branch, eliminate the singleton itself on the negative branch -- and the
 * original writes them as two near-identical functions rather than one
 * parameterized one. The port keeps them separate for the same reason: they
 * differ in enough small ways (Any handling, TypeVar expansion) that merging
 * them would obscure which behavior belongs to which.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// propsCondition reads `type.props?.condition`, which the original does at
// several points in this file. GetTypeCondition is not a substitute: it answers
// nil for TypeVars and unions by category, where the raw property read does not.
func propsCondition(t Type) []TypeCondition {
	if t.Base().Props == nil {
		return nil
	}
	return t.Base().Props.Condition
}

// narrowTypeForUserDefinedTypeGuard corresponds to the function of the same name.
func narrowTypeForUserDefinedTypeGuard(
	evaluator TypeEvaluator,
	t Type,
	typeGuardType Type,
	isPositiveTest bool,
	isStrictTypeGuard bool,
	errorNode parser.ExpressionNode,
) Type {
	// The original's comment: for non-strict type guards, always narrow to the
	// typeGuardType in the positive case and don't narrow in the negative case.
	if !isStrictTypeGuard {
		result := t

		if isPositiveTest {
			result = typeGuardType

			// The original's comment: if the type guard is a non-constrained TypeVar,
			// add a condition to the resulting type.
			if IsTypeVar(t) && !IsParamSpec(t) && !TypeVarTypeHasConstraints(t.(*TypeVarType)) {
				result = AddConditionToType(result,
					[]TypeCondition{{TypeVar: t.(*TypeVarType), ConstraintIndex: 0}}, nil)
			}
			return result
		}

		return result
	}

	filterTypes := []Type{}
	DoForEachSubtype(typeGuardType, func(typeGuardSubtype Type, _ int, _ []Type) {
		filterTypes = append(filterTypes, ConvertToInstantiable(typeGuardSubtype, true))
	})

	return NarrowTypeForInstanceOrSubclass(
		evaluator, t, filterTypes, true, true, isPositiveTest, errorNode)
}

// narrowTypeForTruthiness corresponds to the function of the same name.
//
// The original's comment: narrow the type based on whether the subtype can be
// true or false.
func narrowTypeForTruthiness(evaluator TypeEvaluator, t Type, isPositiveTest bool) Type {
	return MapSubtypes(t, func(subtype Type) Type {
		if isPositiveTest {
			if evaluator.CanBeTruthy(subtype) {
				return evaluator.RemoveFalsinessFromType(subtype)
			}
		} else {
			if evaluator.CanBeFalsy(subtype) {
				return evaluator.RemoveTruthinessFromType(subtype)
			}
		}
		return nil
	}, nil)
}

// narrowTupleTypeForIsNone corresponds to the function of the same name.
//
// The original's comment: handle type narrowing for expressions of the form
// "a[I] is None" and "a[I] is not None" where I is an integer and a is a union
// of Tuples (or subtypes thereof) with known lengths and entry types.
func narrowTupleTypeForIsNone(
	evaluator TypeEvaluator, t Type, isPositiveTest bool, indexValue int,
) Type {
	return evaluator.MapSubtypesExpandTypeVars(t, nil, func(subtype Type, _ Type) Type {
		tupleType := GetSpecializedTupleType(subtype)
		if tupleType == nil || IsUnboundedTupleClass(tupleType) || tupleType.Priv.TupleTypeArgs == nil {
			return subtype
		}

		tupleLength := len(tupleType.Priv.TupleTypeArgs)
		if indexValue < 0 || indexValue >= tupleLength {
			return subtype
		}

		typeOfEntry := evaluator.MakeTopLevelTypeVarsConcrete(
			tupleType.Priv.TupleTypeArgs[indexValue].Type, false)

		if isPositiveTest {
			if !evaluator.AssignType(typeOfEntry, evaluator.GetNoneType(), nil, nil, AssignTypeFlagsDefault, 0) {
				return nil
			}
		} else {
			if IsNoneInstance(typeOfEntry) {
				return nil
			}
		}

		return subtype
	})
}

// getInnermostNewTypeBaseInstance corresponds to the function of the same name.
//
// The original's comment: walks a chain of NewType instances down to the
// innermost non-NewType base instance (e.g. NewType("Y", NewType("X", bool)) ->
// an instance of bool). Returns undefined if `subtype` is not a NewType instance
// or its base cannot be resolved to a class.
func getInnermostNewTypeBaseInstance(subtype Type) Type {
	if !IsClassInstance(subtype) || !ClassTypeIsNewTypeClass(subtype.(*ClassType)) {
		return nil
	}

	var currentType Type = subtype
	for IsClassInstance(currentType) && ClassTypeIsNewTypeClass(currentType.(*ClassType)) {
		cls := currentType.(*ClassType)
		if len(cls.Shared.BaseClasses) == 0 {
			return nil
		}

		baseClass := cls.Shared.BaseClasses[0]
		if !IsClass(baseClass) {
			return nil
		}

		currentType = ClassTypeCloneAsInstance(baseClass.(*ClassType), true)
	}

	return currentType
}

// narrowTypeForIsNone corresponds to the function of the same name.
//
// The original's comment: handle type narrowing for expressions of the form
// "x is None" and "x is not None".
func narrowTypeForIsNone(evaluator TypeEvaluator, t Type, isPositiveTest bool) Type {
	expandedType := MapSubtypes(t, func(subtype Type) Type {
		return TransformPossibleRecursiveTypeAlias(subtype, 0)
	}, nil)

	resultIncludesNoneSubtype := false

	result := evaluator.MapSubtypesExpandTypeVars(expandedType, nil,
		func(subtype Type, unexpandedSubtype Type) Type {
			if IsAnyOrUnknown(subtype) {
				// The original's comment: narrow to None in positive tests, matching the
				// behavior of other narrowing functions (narrowTypeForInstanceOrSubclass,
				// narrowTypeForLiteralComparison).
				if isPositiveTest {
					resultIncludesNoneSubtype = true
					return AddConditionToType(evaluator.GetNoneType(), propsCondition(subtype), nil)
				}
				// The original's comment: for negative tests, keep the original type.
				return subtype
			}

			useExpandedSubtype := false
			if IsTypeVar(unexpandedSubtype) && !TypeVarTypeIsSelf(unexpandedSubtype.(*TypeVarType)) {
				unexpandedTypeVar := unexpandedSubtype.(*TypeVarType)

				// The original's comment: if the TypeVar has value constraints and one or
				// more of them are possibly compatible with None, use the expanded subtypes.
				for _, constraint := range unexpandedTypeVar.Shared.Constraints {
					if evaluator.AssignType(
						constraint, evaluator.GetNoneType(), nil, nil, AssignTypeFlagsDefault, 0) {
						useExpandedSubtype = true
						break
					}
				}

				// The original's comment: if the TypeVar han an explicit bound that is
				// possibly compatible with None (e.g. "T: int | None"), use the expanded
				// subtypes.
				if unexpandedTypeVar.Shared.BoundType != nil &&
					evaluator.AssignType(unexpandedTypeVar.Shared.BoundType, evaluator.GetNoneType(),
						nil, nil, AssignTypeFlagsDefault, 0) {
					useExpandedSubtype = true
				}
			}

			adjustedSubtype := unexpandedSubtype
			if useExpandedSubtype {
				adjustedSubtype = subtype
			}

			// The original's comment: a NewType whose innermost base is exactly None
			// (e.g. NewType("Apple", NoneType)) can be identity-compared with None: keep
			// the NewType identity on the positive branch and eliminate it on the negative
			// branch. A NewType whose base is merely None-compatible (e.g.
			// NewType("Obj", object)) is intentionally not handled here; it falls through
			// to the generic checks below so that "is not None" does not incorrectly
			// collapse to Never.
			newTypeBaseInstance := getInnermostNewTypeBaseInstance(adjustedSubtype)
			if newTypeBaseInstance != nil && IsNoneInstance(newTypeBaseInstance) {
				resultIncludesNoneSubtype = true
				if isPositiveTest {
					return adjustedSubtype
				}
				return nil
			}

			// The original's comment: is it an exact match for None?
			if IsNoneInstance(subtype) {
				resultIncludesNoneSubtype = true
				if isPositiveTest {
					return adjustedSubtype
				}
				return nil
			}

			// The original's comment: is it potentially None?
			if evaluator.AssignType(subtype, evaluator.GetNoneType(), nil, nil, AssignTypeFlagsDefault, 0) {
				resultIncludesNoneSubtype = true
				if isPositiveTest {
					return AddConditionToType(evaluator.GetNoneType(), propsCondition(subtype), nil)
				}
				return adjustedSubtype
			}

			if isPositiveTest {
				return nil
			}
			return adjustedSubtype
		})

	// The original's comment: if this is a positive test and the result is a union
	// that includes None, we can eliminate all the non-None subtypes include Any or
	// Unknown. If some of the subtypes are None types with conditions, retain those.
	if isPositiveTest && resultIncludesNoneSubtype {
		return MapSubtypes(result, func(subtype Type) Type {
			baseInstance := getInnermostNewTypeBaseInstance(subtype)
			if IsNoneInstance(subtype) || (baseInstance != nil && IsNoneInstance(baseInstance)) {
				return subtype
			}
			return nil
		}, nil)
	}

	return result
}

// narrowTypeForIsEllipsis corresponds to the function of the same name.
//
// The original's comment: handle type narrowing for expressions of the form
// "x is ..." and "x is not ...".
func narrowTypeForIsEllipsis(
	evaluator TypeEvaluator, node parser.ExpressionNode, t Type, isPositiveTest bool,
) Type {
	expandedType := MapSubtypes(t, func(subtype Type) Type {
		return TransformPossibleRecursiveTypeAlias(subtype, 0)
	}, nil)

	resultIncludesEllipsisSubtype := false

	ellipsisType := evaluator.GetBuiltInObject(node, "EllipsisType", nil)
	if ellipsisType == nil {
		ellipsisType = evaluator.GetBuiltInObject(node, "ellipsis", nil)
	}
	if ellipsisType == nil {
		ellipsisType = AnyTypeCreate(false)
	}

	isEllipsisInstance := func(subtype Type) bool {
		return IsClassInstance(subtype) &&
			ClassTypeIsBuiltInNamed(subtype.(*ClassType), "EllipsisType", "ellipsis")
	}

	result := evaluator.MapSubtypesExpandTypeVars(expandedType, nil,
		func(subtype Type, unexpandedSubtype Type) Type {
			if IsAnyOrUnknown(subtype) {
				// The original's comment: we need to assume that "Any" is always both
				// ellipsis and not ellipsis, so it matches regardless of whether the test is
				// positive or negative.
				return subtype
			}

			// The original's comment: if this is a TypeVar that isn't constrained, use
			// the unexpanded TypeVar. For all other cases (including constrained
			// TypeVars), use the expanded subtype.
			adjustedSubtype := subtype
			if IsTypeVar(unexpandedSubtype) &&
				!TypeVarTypeHasConstraints(unexpandedSubtype.(*TypeVarType)) {
				adjustedSubtype = unexpandedSubtype
			}

			// The original's comment: only a NewType whose innermost base is exactly the
			// ellipsis type is treated as the singleton (see narrowTypeForIsNone for the
			// rationale); a merely ellipsis-compatible base falls through so "is not ..."
			// does not collapse to Never.
			newTypeBaseInstance := getInnermostNewTypeBaseInstance(adjustedSubtype)
			if newTypeBaseInstance != nil && isEllipsisInstance(newTypeBaseInstance) {
				resultIncludesEllipsisSubtype = true
				if isPositiveTest {
					return adjustedSubtype
				}
				return nil
			}

			// The original's comment: is it an exact match for ellipsis?
			if isEllipsisInstance(subtype) {
				resultIncludesEllipsisSubtype = true
				if isPositiveTest {
					return adjustedSubtype
				}
				return nil
			}

			// The original's comment: is it potentially ellipsis?
			if evaluator.AssignType(subtype, ellipsisType, nil, nil, AssignTypeFlagsDefault, 0) {
				resultIncludesEllipsisSubtype = true
				if isPositiveTest {
					return AddConditionToType(ellipsisType, propsCondition(subtype), nil)
				}
				return adjustedSubtype
			}

			if isPositiveTest {
				return nil
			}
			return adjustedSubtype
		})

	// The original's comment: if this is a positive test and the result is a union
	// that includes ellipsis, we can eliminate all the non-ellipsis subtypes include
	// Any or Unknown. If some of the subtypes are ellipsis types with conditions,
	// retain those.
	if isPositiveTest && resultIncludesEllipsisSubtype {
		return MapSubtypes(result, func(subtype Type) Type {
			baseInstance := getInnermostNewTypeBaseInstance(subtype)
			if isEllipsisInstance(subtype) || (baseInstance != nil && isEllipsisInstance(baseInstance)) {
				return subtype
			}
			return nil
		}, nil)
	}

	return result
}
