/*
 * typeevaluator_mapsubtypes.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * mapSubtypesExpandTypeVars, applyConditionFilterToType and markNamesAccessed.
 *
 * mapSubtypesExpandTypeVars is the workhorse of every operation that has to
 * consider a union one member at a time. The original's comment: creates a new
 * type by mapping an existing type (which could be a union) to another type or
 * types. The callback is called for each subtype. Top-level TypeVars are
 * expanded -- a bound TypeVar to its bound, a constrained one to its individual
 * constraints -- so the callback never has to deal with them.
 *
 * Three behaviours in it are easy to lose and all three matter:
 *
 *   - `typeChanged` is set by comparing the callback's result against the
 *     UNEXPANDED subtype, not the expanded one. A callback that returns its
 *     argument unchanged still counts as a change when the subtype was expanded,
 *     which is what makes the expansion stick.
 *   - Conditions from constrained TypeVars are re-attached to the transformed
 *     type, so `T@f` narrowing survives the round trip.
 *   - Consecutive duplicate subtypes are dropped before combineTypes, which the
 *     original notes is a cost optimization -- this path produces many.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// MapSubtypesExpandTypeVars corresponds to mapSubtypesExpandTypeVars. The
// interface's callback takes two arguments; the original's takes a third,
// isLastIteration, which no current caller reads.
func (e *typeEvaluator) MapSubtypesExpandTypeVars(
	t Type,
	options *EvaluatorMapSubtypesOptions,
	callback func(expandedSubtype Type, unexpandedSubtype Type) Type,
) Type {
	return e.mapSubtypesExpandTypeVars(t, options, func(expanded Type, unexpanded Type, _ bool) Type {
		return callback(expanded, unexpanded)
	}, 0)
}

func (e *typeEvaluator) mapSubtypesExpandTypeVars(
	t Type,
	options *EvaluatorMapSubtypesOptions,
	callback func(expandedSubtype Type, unexpandedSubtype Type, isLastIteration bool) Type,
	recursionCount int,
) Type {
	newSubtypes := []Type{}
	typeChanged := false

	expandSubtype := func(unexpandedType Type, isLastSubtype bool) {
		var expandedType Type
		if IsUnion(unexpandedType) {
			expandedType = unexpandedType
		} else {
			expandedType = e.makeTopLevelTypeVarsConcrete(unexpandedType, false, nil)
		}

		expandedType = TransformPossibleRecursiveTypeAlias(expandedType, 0)
		if options != nil && options.ExpandCallback != nil {
			expandedType = options.ExpandCallback(expandedType)
		}

		visit := func(subtype Type, index int, allSubtypes []Type) {
			if options != nil && options.ConditionFilter != nil {
				filteredType := e.applyConditionFilterToType(subtype, options.ConditionFilter, recursionCount)
				if filteredType == nil {
					return
				}

				subtype = filteredType
			}

			transformedType := callback(subtype, unexpandedType, isLastSubtype && index == len(allSubtypes)-1)

			// The comparison is against the UNEXPANDED subtype; see the header.
			if transformedType != unexpandedType {
				typeChanged = true
			}

			if transformedType == nil {
				return
			}

			// The original's comment: apply the type condition if it's
			// associated with a constrained TypeVar.
			var typeCondition []TypeCondition
			for _, condition := range GetTypeCondition(subtype) {
				if TypeVarTypeHasConstraints(condition.TypeVar) {
					typeCondition = append(typeCondition, condition)
				}
			}

			if len(typeCondition) > 0 {
				transformedType = AddConditionToType(transformedType, typeCondition, nil)
			}

			// The original's comment: this code path can often produce many
			// duplicate subtypes. We can reduce the cost of the combineTypes
			// call below by filtering out these duplicates proactively.
			if len(newSubtypes) == 0 ||
				!IsTypeSame(transformedType, newSubtypes[len(newSubtypes)-1], TypeSameOptions{}, 0) {
				newSubtypes = append(newSubtypes, transformedType)
			}
		}

		if options != nil && options.SortSubtypes {
			DoForEachSubtypeSorted(expandedType, visit)
		} else {
			DoForEachSubtype(expandedType, visit)
		}
	}

	if union, ok := AsUnion(t); ok {
		subtypes := unionableToTypes(union.Priv.Subtypes)
		total := len(subtypes)
		if options != nil && options.SortSubtypes {
			subtypes = SortTypes(subtypes)
		}
		for index, subtype := range subtypes {
			expandSubtype(subtype, index == total-1)
		}
	} else {
		expandSubtype(t, true)
	}

	if !typeChanged {
		return t
	}

	newType := CombineTypes(newSubtypes, nil)

	// The original's comment: do our best to retain type aliases.
	if newType.Base().Category == TypeCategoryUnion {
		UnionTypeAddTypeAliasSource(newType.(*UnionType), t)
	}
	return newType
}

// applyConditionFilterToType corresponds to the function of the same name. It
// returns nil where the original returns undefined, meaning "filtered out".
func (e *typeEvaluator) applyConditionFilterToType(
	t Type,
	conditionFilter []*TypeCondition,
	recursionCount int,
) Type {
	if recursionCount > MaxTypeRecursionCount {
		return t
	}
	recursionCount++

	// The original's comment: if the type has a condition associated with it,
	// make sure it's compatible.
	if !TypeConditionIsCompatible(GetTypeCondition(t), derefConditions(conditionFilter)) {
		return nil
	}

	// The original's comment: if the type is generic, see if any of its type
	// arguments should be filtered. This is possible only in cases where the
	// type parameter is covariant. It carries a TODO about functions and tuples.
	classType, ok := t.(*ClassType)
	if !ok || !IsClass(t) || classType.Priv.TypeArgs == nil || classType.Priv.TupleTypeArgs != nil {
		return t
	}

	e.InferVarianceForClass(classType)

	typeWasTransformed := false
	filteredTypeArgs := make([]Type, 0, len(classType.Priv.TypeArgs))

	for index, typeArg := range classType.Priv.TypeArgs {
		if index >= len(classType.Shared.TypeParams) {
			filteredTypeArgs = append(filteredTypeArgs, typeArg)
			continue
		}

		if TypeVarTypeGetVariance(classType.Shared.TypeParams[index]) != VarianceCovariant {
			filteredTypeArgs = append(filteredTypeArgs, typeArg)
			continue
		}

		// The original's comment: don't expand recursive type aliases because
		// they can cause infinite recursion.
		if IsTypeVar(typeArg) && typeArg.(*TypeVarType).Shared.RecursiveAlias != nil {
			filteredTypeArgs = append(filteredTypeArgs, typeArg)
			continue
		}

		filteredTypeArg := e.mapSubtypesExpandTypeVars(
			typeArg,
			&EvaluatorMapSubtypesOptions{ConditionFilter: conditionFilter},
			func(expandedSubtype Type, _ Type, _ bool) Type { return expandedSubtype },
			recursionCount,
		)

		if filteredTypeArg != typeArg {
			typeWasTransformed = true
		}

		filteredTypeArgs = append(filteredTypeArgs, filteredTypeArg)
	}

	if typeWasTransformed {
		return ClassTypeSpecialize(classType, filteredTypeArgs, nil, false, nil, nil)
	}

	return t
}

// derefConditions adapts the []*TypeCondition this port's options carry to the
// []TypeCondition the compatibility check takes. The original has one type here;
// the two shapes exist because TypeCondition is a value in typeutils and a
// pointer in the evaluator's options interface.
func derefConditions(conditions []*TypeCondition) []TypeCondition {
	if conditions == nil {
		return nil
	}
	out := make([]TypeCondition, 0, len(conditions))
	for _, condition := range conditions {
		if condition != nil {
			out = append(out, *condition)
		}
	}
	return out
}

// MarkNamesAccessed corresponds to markNamesAccessed.
func (e *typeEvaluator) MarkNamesAccessed(node parser.ParseNode, names []string) {
	fileInfo := GetFileInfo(node)
	scope := GetScopeForNode(node)

	if scope == nil {
		return
	}

	for _, symbolName := range names {
		if symbolInScope := scope.LookUpSymbolRecursive(symbolName, nil); symbolInScope != nil {
			e.setSymbolAccessed(fileInfo, symbolInScope.Symbol, node)
		}
	}
}
