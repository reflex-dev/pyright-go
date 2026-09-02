/*
 * typeevaluator_assignunionsrc.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * assignFromUnionType, isTypeSubsumedByOtherType, isProperSubtype.
 *
 * Assigning a union *source* to some destination. The naive rule -- every source
 * subtype must be assignable to the destination -- is the fallback at the bottom
 * of this function, and it is correct but loses information when the destination
 * is itself a union of TypeVars: assigning `int | str` to `T1 | T2` subtype by
 * subtype would solve both TypeVars to `int | str`.
 *
 * So when both sides are unions there is a "fast path" that pairs subtypes off:
 * exact matches are cancelled first, then same-generic-class pairs, then
 * whatever TypeVars remain absorb what is left. The pairing only counts as
 * having worked if it consumed everything; otherwise canUseFastPath is cleared
 * and the naive rule runs on whatever the pairing did not already check.
 *
 * isTypeSubsumedByOtherType is the escape hatch for a source subtype that fails
 * on its own but is redundant with a sibling: `int | bool` against `int` fails
 * for `bool` alone, but `bool` is subsumed by `int`. isProperSubtype checks
 * assignability in both directions precisely so that `tuple[Any]` and
 * `tuple[int]` are not treated as subsuming one another.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// assignFromUnionType corresponds to the function of the same name.
func (e *typeEvaluator) assignFromUnionType(
	destType Type,
	srcType *UnionType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	// The original's comment: start by checking for an exact match. This is needed
	// to handle unions that contain recursive type aliases.
	if IsTypeSame(srcType, destType, TypeSameOptions{}, recursionCount) {
		return true
	}

	srcSubtypes := unionableToTypes(srcType.Priv.Subtypes)

	if (flags & AssignTypeFlagsOverloadOverlap) != 0 {
		for _, subtype := range srcSubtypes {
			if IsAnyOrUnknown(subtype) {
				return false
			}
		}
	}

	// The original's comment: sort the subtypes so we have a deterministic order
	// for unions.
	sortedSrcTypes := SortTypes(srcSubtypes)
	matchedSomeSubtypes := false

	// The original's comment: handle the case where the source and dest are both
	// unions. Try to eliminate as many exact type matches between the src and dest.
	if destUnion, ok := AsUnion(destType); ok {
		destSubtypes := unionableToTypes(destUnion.Priv.Subtypes)

		// The original's comment: handle the special case where the dest is a union
		// of Any and a type variable. This occurs, for example, with the return type
		// of the getattr function.
		nonAnySubtypes := []Type{}
		for _, t := range destSubtypes {
			if !IsAnyOrUnknown(t) {
				nonAnySubtypes = append(nonAnySubtypes, t)
			}
		}
		if len(nonAnySubtypes) == 1 && IsTypeVar(nonAnySubtypes[0]) {
			e.AssignType(nonAnySubtypes[0], srcType, nil, constraints, flags, recursionCount)

			// The original's comment: this always succeeds because the destination
			// contains Any.
			return true
		}

		remainingDestSubtypes := []Type{}
		remainingSrcSubtypes := sortedSrcTypes
		canUseFastPath := true

		// The original's comment: first attempt to match all of the non-generic types
		// in the dest to non-generic types in the source.
		for _, destSubtype := range SortTypes(destSubtypes) {
			if RequiresSpecialization(destSubtype, nil, 0) {
				remainingDestSubtypes = append(remainingDestSubtypes, destSubtype)
				continue
			}

			srcTypeIndex := -1
			for i, srcSubtype := range remainingSrcSubtypes {
				if IsTypeSame(srcSubtype, destSubtype, TypeSameOptions{}, recursionCount) {
					srcTypeIndex = i
					break
				}
			}

			if srcTypeIndex >= 0 {
				remainingSrcSubtypes = append(
					append([]Type{}, remainingSrcSubtypes[:srcTypeIndex]...),
					remainingSrcSubtypes[srcTypeIndex+1:]...)
				matchedSomeSubtypes = true
			} else {
				remainingDestSubtypes = append(remainingDestSubtypes, destSubtype)
			}
		}

		// The original's comment: for all remaining source subtypes, attempt to find
		// a dest subtype whose primary type matches.
		//
		// The original iterates the array it is also filtering; forEach captures the
		// array reference at entry, so the iteration is over the snapshot taken here.
		srcSnapshot := append([]Type{}, remainingSrcSubtypes...)
		for _, srcSubtype := range srcSnapshot {
			destTypeIndex := -1
			for i, destSubtype := range remainingDestSubtypes {
				if IsTypeSame(destSubtype, srcSubtype, TypeSameOptions{}, 0) {
					destTypeIndex = i
					break
				}

				if IsClass(srcSubtype) && IsClass(destSubtype) &&
					srcSubtype.Base().IsInstance() == destSubtype.Base().IsInstance() {
					srcClass := srcSubtype.(*ClassType)
					destClass := destSubtype.(*ClassType)

					if ClassTypeIsSameGenericClass(srcClass, destClass, 0) {
						destTypeIndex = i
						break
					}

					// The original's comment: are they equivalent TypedDicts?
					if ClassTypeIsTypedDictClass(srcClass) && ClassTypeIsTypedDictClass(destClass) {
						if e.AssignType(srcSubtype, destSubtype, nil, nil, flags, recursionCount) {
							destTypeIndex = i
							break
						}
					}
				}

				if IsFunctionOrOverloaded(srcSubtype) && IsFunctionOrOverloaded(destSubtype) {
					destTypeIndex = i
					break
				}
			}

			if destTypeIndex >= 0 {
				if e.AssignType(remainingDestSubtypes[destTypeIndex], srcSubtype, nil, constraints,
					flags, recursionCount) {
					// The original's comment: note that we have matched at least one
					// subtype indicating there is at least some overlap.
					matchedSomeSubtypes = true
				} else {
					canUseFastPath = false
				}

				remainingDestSubtypes = append(
					append([]Type{}, remainingDestSubtypes[:destTypeIndex]...),
					remainingDestSubtypes[destTypeIndex+1:]...)

				filtered := remainingSrcSubtypes[:0:0]
				for _, t := range remainingSrcSubtypes {
					if t != srcSubtype {
						filtered = append(filtered, t)
					}
				}
				remainingSrcSubtypes = filtered
			}
		}

		// The original's comment: if there is are remaining dest subtypes and they're
		// all type variables, attempt to assign the remaining source subtypes to them.
		if canUseFastPath && (len(remainingDestSubtypes) != 0 || len(remainingSrcSubtypes) != 0) {
			if (flags & AssignTypeFlagsInvariant) != 0 {
				// The original's comment: if we have no src subtypes remaining but not
				// all dest types have been subsumed by other dest types, then the types
				// are not compatible if we're enforcing invariance.
				if len(remainingSrcSubtypes) == 0 {
					for _, destSubtype := range remainingDestSubtypes {
						if !e.isTypeSubsumedByOtherType(destSubtype, destType, true, recursionCount) {
							return false
						}
					}
					return true
				}
			}

			isContra := (flags & AssignTypeFlagsContravariant) != 0
			effectiveDestSubtypes := remainingDestSubtypes
			if isContra {
				effectiveDestSubtypes = remainingSrcSubtypes
			}

			someNotTypeVar := false
			for _, t := range effectiveDestSubtypes {
				if !IsTypeVar(t) {
					someNotTypeVar = true
					break
				}
			}

			switch {
			case len(effectiveDestSubtypes) == 0 || someNotTypeVar:
				canUseFastPath = false

				// The original's comment: we can avoid checking the source subtypes that
				// have already been checked.
				sortedSrcTypes = remainingSrcSubtypes

			case len(remainingDestSubtypes) == len(remainingSrcSubtypes):
				// The original's comment: if the number of remaining source subtypes is
				// the same as the number of dest TypeVars, try to assign each source
				// subtype to its own dest TypeVar.
				reorderedDestSubtypes := append([]Type{}, remainingDestSubtypes...)

				for srcIndex := 0; srcIndex < len(remainingSrcSubtypes); srcIndex++ {
					foundMatchForSrc := false

					for destIndex := 0; destIndex < len(reorderedDestSubtypes); destIndex++ {
						var childDiag *common.DiagnosticAddendum
						if diag != nil {
							childDiag = diag.CreateAddendum()
						}
						if e.AssignType(reorderedDestSubtypes[destIndex], remainingSrcSubtypes[srcIndex],
							childDiag, constraints, flags, recursionCount) {
							foundMatchForSrc = true
							// The original's comment: move the matched dest TypeVar to the end
							// of the list so the other dest TypeVars have a better chance of
							// being assigned to.
							matched := reorderedDestSubtypes[destIndex]
							reorderedDestSubtypes = append(
								append(append([]Type{}, reorderedDestSubtypes[:destIndex]...),
									reorderedDestSubtypes[destIndex+1:]...), matched)
							break
						}
					}

					if !foundMatchForSrc {
						canUseFastPath = false
						break
					}
				}

				// The original's comment: we can avoid checking the source subtypes that
				// have already been checked.
				sortedSrcTypes = remainingSrcSubtypes

			case len(remainingSrcSubtypes) == 0:
				if (flags & AssignTypeFlagsPopulateExpectedType) != 0 {
					// The original's comment: if we're populating an expected type, try
					// not to leave any TypeVars unsolved. Assign the full type to the
					// remaining dest TypeVars.
					for _, destSubtype := range remainingDestSubtypes {
						e.AssignType(destSubtype, srcType, nil, constraints, flags, recursionCount)
					}
				}

				// The original's comment: if we've assigned all of the source subtypes
				// but one or more dest TypeVars have gone unmatched, treat this as
				// success.

			default:
				// The original's comment: try to assign a union of the remaining source
				// types to the first destination TypeVar. If this is a contravariant
				// context, use the full dest type rather than the remaining dest subtypes
				// to keep the lower bound as wide as possible.
				var childDiag *common.DiagnosticAddendum
				if diag != nil {
					childDiag = diag.CreateAddendum()
				}

				assignDest := destType
				var assignSrc Type = CombineTypes(remainingSrcSubtypes, nil)
				if !isContra {
					assignDest = remainingDestSubtypes[0]
				} else {
					assignSrc = remainingSrcSubtypes[0]
				}

				if !e.AssignType(assignDest, assignSrc, childDiag, constraints, flags, recursionCount) {
					canUseFastPath = false
				}
			}
		}

		if canUseFastPath {
			return true
		}

		// The original's comment: if we're looking for type overlaps and at least one
		// type was matched, consider it as assignable.
		if (flags&AssignTypeFlagsPartialOverloadOverlap) != 0 && matchedSomeSubtypes {
			return true
		}
	}

	isIncompatible := false

	// The original passes sortSubtypes to forEach, which the array method ignores;
	// sortedSrcTypes is already sorted.
	for _, subtype := range sortedSrcTypes {
		if isIncompatible {
			break
		}

		if !e.AssignType(destType, subtype, nil, constraints, flags, recursionCount) {
			// The original's comment: determine if the current subtype is subsumed by
			// another subtype in the same union. If so, we can ignore this.
			isSubtypeSubsumed := e.isTypeSubsumedByOtherType(subtype, srcType, false, recursionCount)

			// The original's comment: try again with a concrete version of the subtype.
			var childDiag *common.DiagnosticAddendum
			if diag != nil {
				childDiag = diag.CreateAddendum()
			}
			if !isSubtypeSubsumed &&
				!e.AssignType(destType, subtype, childDiag, constraints, flags, recursionCount) {
				isIncompatible = true
			}
		} else {
			matchedSomeSubtypes = true
		}
	}

	if isIncompatible {
		// The original's comment: if we're looking for type overlaps and at least one
		// type was matched, consider it as assignable.
		if (flags&AssignTypeFlagsPartialOverloadOverlap) != 0 && matchedSomeSubtypes {
			return true
		}

		if diag != nil {
			types := e.PrintSrcDestTypes(srcType, destType)
			diag.AddMessage(localization.LocAddendum.TypeAssignmentMismatch().Format(
				types.SourceType, types.DestType))
		}
		return false
	}

	return true
}

// IsTypeSubsumedByOtherType is the interface method for
// isTypeSubsumedByOtherType. The original defaults recursionCount to 0.
func (e *typeEvaluator) IsTypeSubsumedByOtherType(t Type, otherType Type, allowAnyToSubsume bool) bool {
	return e.isTypeSubsumedByOtherType(t, otherType, allowAnyToSubsume, 0)
}

func (e *typeEvaluator) isTypeSubsumedByOtherType(
	t Type, otherType Type, allowAnyToSubsume bool, recursionCount int,
) bool {
	concreteType := e.MakeTopLevelTypeVarsConcrete(t, false)

	otherSubtypes := []Type{otherType}
	if union, ok := AsUnion(otherType); ok {
		otherSubtypes = unionableToTypes(union.Priv.Subtypes)
	}

	for _, otherSubtype := range otherSubtypes {
		if IsTypeSame(otherSubtype, t, TypeSameOptions{}, 0) {
			continue
		}

		if IsAnyOrUnknown(otherSubtype) {
			if allowAnyToSubsume {
				return true
			}
		} else if e.isProperSubtype(otherSubtype, concreteType, recursionCount) {
			return true
		}
	}

	return false
}

// isProperSubtype corresponds to the function of the same name.
//
// The original's comment: determines whether the srcType is a subtype of
// destType but the converse is not true. It's important that we check both
// directions to avoid matches for types like `tuple[Any]` and `tuple[int]` from
// being considered proper subtypes of each other.
func (e *typeEvaluator) isProperSubtype(destType Type, srcType Type, recursionCount int) bool {
	// The original's comment: if the destType has a condition, don't consider the
	// srcType a proper subtype.
	if destType.Base().Props != nil && destType.Base().Props.Condition != nil {
		return false
	}

	// The original's comment: shortcut the check if either type is Any or Unknown.
	if IsAnyOrUnknown(destType) || IsAnyOrUnknown(srcType) {
		return true
	}

	// The original's comment: shortcut the check if either type is a class whose
	// hierarchy contains an unknown type.
	if IsClass(destType) {
		for _, mro := range destType.(*ClassType).Shared.Mro {
			if IsAnyOrUnknown(mro) {
				return true
			}
		}
	}

	if IsClass(srcType) {
		for _, mro := range srcType.(*ClassType).Shared.Mro {
			if IsAnyOrUnknown(mro) {
				return true
			}
		}
	}

	return e.AssignType(destType, srcType, nil, nil, AssignTypeFlagsDefault, recursionCount) &&
		!e.AssignType(srcType, destType, nil, nil, AssignTypeFlagsDefault, recursionCount)
}
