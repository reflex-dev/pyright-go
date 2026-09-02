/*
 * tuples_assign.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/tuples.ts (pyright 1.1.412):
 * assignTupleTypeArgs and adjustTupleTypeArgs.
 *
 * Tuples are the one generic class whose type arguments are positional and
 * variable in number, so they cannot use the ordinary type-argument comparison.
 * assignTupleTypeArgs first tries to make the two argument lists the same
 * length, and only then compares them pairwise; if the lengths cannot be
 * reconciled the mismatch itself is the diagnostic, and which of the four
 * messages it emits depends on whether either side had indeterminate length.
 *
 * adjustTupleTypeArgs does that reconciliation, and it mutates both lists in
 * place -- the original splices into arrays the caller owns, so the Go port
 * takes *[]*TupleTypeArg and keeps the splice semantics rather than rebuilding
 * the lists functionally. There are four independent adjustments, applied in
 * this order:
 *
 *   1. An unbounded `Any` on either side expands or collapses to the other
 *      side's length. Any is compatible with any number of entries, so this is
 *      free.
 *   2. Optional entries (a captured callable's defaulted positional params) are
 *      dropped from the end of whichever list is longer.
 *   3. The entries matching the other side's TypeVarTuple are packaged into a
 *      single unpacked tuple, so the pairwise comparison that follows sees one
 *      entry against one entry. Which side gets packaged depends on the
 *      direction of the assignment, which is why the Contravariant flag
 *      selects a different branch rather than just swapping arguments.
 *   4. Failing that, the source entries matching a dest unbounded entry are
 *      combined into a union.
 *
 * Step 3's two branches both set skipAdjustSrc, which is what keeps step 4 from
 * re-packaging entries that step 3 already handled.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// tupleArgsSplice removes count entries starting at index and returns them,
// standing in for the original's Array.prototype.splice used for removal.
// TypeScript clamps an over-long removal to the end of the array and treats a
// negative count as zero; this reproduces both.
func tupleArgsSplice(args *[]*TupleTypeArg, index int, count int) []*TupleTypeArg {
	if count < 0 {
		count = 0
	}
	if index > len(*args) {
		index = len(*args)
	}
	if index+count > len(*args) {
		count = len(*args) - index
	}

	removed := append([]*TupleTypeArg(nil), (*args)[index:index+count]...)
	*args = append((*args)[:index], (*args)[index+count:]...)
	return removed
}

// tupleArgsInsert inserts one entry at index, standing in for the original's
// splice used for insertion.
func tupleArgsInsert(args *[]*TupleTypeArg, index int, entry *TupleTypeArg) {
	if index > len(*args) {
		index = len(*args)
	}

	*args = append(*args, nil)
	copy((*args)[index+1:], (*args)[index:])
	(*args)[index] = entry
}

// findTupleArgIndex corresponds to Array.prototype.findIndex over a tuple type
// argument list, returning -1 when nothing matches.
func findTupleArgIndex(args []*TupleTypeArg, predicate func(*TupleTypeArg) bool) int {
	for i, arg := range args {
		if predicate(arg) {
			return i
		}
	}
	return -1
}

// anyTupleArg corresponds to Array.prototype.some over a tuple type argument
// list.
func anyTupleArg(args []*TupleTypeArg, predicate func(*TupleTypeArg) bool) bool {
	return findTupleArgIndex(args, predicate) >= 0
}

// AssignTupleTypeArgs corresponds to assignTupleTypeArgs.
func AssignTupleTypeArgs(
	evaluator TypeEvaluator,
	destType *ClassType,
	srcType *ClassType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	destTypeArgs := append([]*TupleTypeArg(nil), destType.Priv.TupleTypeArgs...)
	srcTypeArgs := append([]*TupleTypeArg(nil), srcType.Priv.TupleTypeArgs...)

	if AdjustTupleTypeArgs(evaluator, &destTypeArgs, &srcTypeArgs, flags) {
		for argIndex := 0; argIndex < len(srcTypeArgs); argIndex++ {
			var entryDiag *common.DiagnosticAddendum
			if diag != nil {
				entryDiag = diag.CreateAddendum()
			}

			destArgType := destTypeArgs[argIndex].Type
			srcArgType := srcTypeArgs[argIndex].Type

			// The original's comment: handle the special case where the dest is a
			// TypeVarTuple and the source is a `*tuple[Any, ...]`. This is allowed.
			if IsTypeVarTuple(destArgType) {
				destArgTypeVar := destArgType.(*TypeVarType)
				if destArgTypeVar.Priv.IsUnpacked && !destArgTypeVar.Priv.IsInUnion &&
					IsTupleGradualForm(srcArgType) {
					return true
				}
			}

			var nestedDiag *common.DiagnosticAddendum
			if entryDiag != nil {
				nestedDiag = entryDiag.CreateAddendum()
			}

			if !evaluator.AssignType(
				destArgType, srcArgType, nestedDiag, constraints, flags, recursionCount) {
				if entryDiag != nil {
					entryDiag.AddMessage(
						localization.LocAddendum.TupleEntryTypeMismatch().Format(argIndex + 1))
				}
				return false
			}
		}
	} else {
		isIndeterminate := func(t *TupleTypeArg) bool {
			return t.IsUnbounded || IsTypeVarTuple(t.Type)
		}
		isDestIndeterminate := anyTupleArg(destTypeArgs, isIndeterminate)

		if anyTupleArg(srcTypeArgs, isIndeterminate) {
			if isDestIndeterminate {
				if diag != nil {
					diag.AddMessage(localization.LocAddendum.TupleSizeIndeterminateSrcDest().
						Format(len(destTypeArgs) - 1))
				}
			} else {
				if diag != nil {
					diag.AddMessage(localization.LocAddendum.TupleSizeIndeterminateSrc().
						Format(len(destTypeArgs)))
				}
			}
		} else {
			if isDestIndeterminate {
				if diag != nil {
					diag.AddMessage(localization.LocAddendum.TupleSizeMismatchIndeterminateDest().
						Format(len(destTypeArgs)-1, len(srcTypeArgs)))
				}
			} else {
				if diag != nil {
					diag.AddMessage(localization.LocAddendum.TupleSizeMismatch().
						Format(len(destTypeArgs), len(srcTypeArgs)))
				}
			}
		}

		return false
	}

	return true
}

// AdjustTupleTypeArgs corresponds to adjustTupleTypeArgs. The original's
// comment: adjusts the source and/or dest type arguments list to attempt to
// match the length of the src type arguments list if the dest or source contain
// entries with indeterminate length or unpacked TypeVarTuple entries. It returns
// true if the source is potentially compatible with the dest type, false
// otherwise.
func AdjustTupleTypeArgs(
	evaluator TypeEvaluator,
	destTypeArgs *[]*TupleTypeArg,
	srcTypeArgs *[]*TupleTypeArg,
	flags AssignTypeFlags,
) bool {
	destUnboundedOrVariadicIndex := findTupleArgIndex(*destTypeArgs, func(t *TupleTypeArg) bool {
		return t.IsUnbounded || IsUnpackedTypeVarTuple(t.Type) || IsUnpackedTypeVar(t.Type)
	})
	srcUnboundedIndex := findTupleArgIndex(*srcTypeArgs, func(t *TupleTypeArg) bool {
		return t.IsUnbounded
	})
	srcVariadicIndex := findTupleArgIndex(*srcTypeArgs, func(t *TupleTypeArg) bool {
		return IsUnpackedTypeVarTuple(t.Type) || IsUnpackedTypeVar(t.Type)
	})

	if srcUnboundedIndex >= 0 {
		if IsAnyOrUnknown((*srcTypeArgs)[srcUnboundedIndex].Type) {
			// The original's comment: if the source contains an unbounded Any,
			// expand it to match the dest length.
			//
			// The original's `srcTypeArgs.length > 0 ? ... : AnyType.create()`
			// ternary can only take its first arm, since srcUnboundedIndex >= 0
			// already implies a non-empty list.
			typeToReplicate := (*srcTypeArgs)[srcUnboundedIndex].Type

			for len(*srcTypeArgs) < len(*destTypeArgs) {
				tupleArgsInsert(srcTypeArgs, srcUnboundedIndex,
					&TupleTypeArg{Type: typeToReplicate, IsUnbounded: true})
			}

			if len(*srcTypeArgs) > len(*destTypeArgs) {
				tupleArgsSplice(srcTypeArgs, srcUnboundedIndex, 1)

				// The original's comment: invalidate the stale index after removal.
				srcUnboundedIndex = -1
			}
		} else if destUnboundedOrVariadicIndex < 0 {
			// The original's comment: if the source contains an unbounded type but
			// the dest does not, it's incompatible.
			return false
		}
	}

	// The original's comment: if the dest contains an unbounded Any, expand it to
	// match the source length.
	if destUnboundedOrVariadicIndex >= 0 &&
		(*destTypeArgs)[destUnboundedOrVariadicIndex].IsUnbounded &&
		IsAnyOrUnknown((*destTypeArgs)[destUnboundedOrVariadicIndex].Type) {
		for len(*destTypeArgs) < len(*srcTypeArgs) {
			tupleArgsInsert(destTypeArgs, destUnboundedOrVariadicIndex,
				(*destTypeArgs)[destUnboundedOrVariadicIndex])
		}
	}

	// The original's comment: remove any optional parameters from the end of the
	// two lists until the lengths match.
	for len(*srcTypeArgs) > len(*destTypeArgs) && (*srcTypeArgs)[len(*srcTypeArgs)-1].IsOptional {
		tupleArgsSplice(srcTypeArgs, len(*srcTypeArgs)-1, 1)
	}

	for len(*destTypeArgs) > len(*srcTypeArgs) && (*destTypeArgs)[len(*destTypeArgs)-1].IsOptional {
		tupleArgsSplice(destTypeArgs, len(*destTypeArgs)-1, 1)
	}

	srcArgsToCapture := len(*srcTypeArgs) - len(*destTypeArgs) + 1
	skipAdjustSrc := false

	// The original's comment: if we're doing reverse type mappings and the source
	// contains a TypeVarTuple, we need to adjust the dest so the reverse type
	// mapping assignment can be performed.
	if (flags & AssignTypeFlagsContravariant) != 0 {
		destArgsToCapture := len(*destTypeArgs) - len(*srcTypeArgs) + 1

		if srcVariadicIndex >= 0 && destArgsToCapture >= 0 {
			// The original's comment: if the only removed arg from the dest type
			// args is itself a variadic, don't bother adjusting it.
			//
			// destArgsToCapture == 1 means the two lists are the same length, so
			// srcVariadicIndex is in range for destTypeArgs whenever the second
			// operand is evaluated.
			skipAdjustment := destArgsToCapture == 1 &&
				IsTypeVarTuple((*destTypeArgs)[srcVariadicIndex].Type)
			tupleClass := evaluator.GetTupleClassType()

			if !skipAdjustment && tupleClass != nil && IsInstantiableClass(tupleClass) {
				removedArgs := tupleArgsSplice(destTypeArgs, srcVariadicIndex, destArgsToCapture)

				// The original's comment: package up the remaining type arguments
				// into a tuple object.
				packaged := make([]*TupleTypeArg, 0, len(removedArgs))
				for _, typeArg := range removedArgs {
					packaged = append(packaged, &TupleTypeArg{
						Type:        typeArg.Type,
						IsUnbounded: typeArg.IsUnbounded,
						IsOptional:  typeArg.IsOptional,
					})
				}

				variadicTuple := ClassTypeCloneAsInstance(
					SpecializeTupleClass(tupleClass, packaged, true, true), true)

				tupleArgsInsert(destTypeArgs, srcVariadicIndex,
					&TupleTypeArg{Type: variadicTuple, IsUnbounded: false})
			}

			skipAdjustSrc = true
		}
	} else {
		if destUnboundedOrVariadicIndex >= 0 && srcArgsToCapture >= 0 {
			// The original's comment: if the dest contains a variadic element,
			// determine which source args map to this element and package them up
			// into an unpacked tuple.
			if IsTypeVarTuple((*destTypeArgs)[destUnboundedOrVariadicIndex].Type) {
				tupleClass := evaluator.GetTupleClassType()

				if tupleClass != nil && IsInstantiableClass(tupleClass) {
					removedArgs := tupleArgsSplice(
						srcTypeArgs, destUnboundedOrVariadicIndex, srcArgsToCapture)

					var variadicTuple Type

					// The original's comment: if we're left with a single unpacked
					// variadic type var, there's no need to wrap it in a nested
					// tuple.
					if len(removedArgs) == 1 && IsUnpackedTypeVarTuple(removedArgs[0].Type) {
						variadicTuple = removedArgs[0].Type
					} else {
						// The original's comment: package up the remaining type
						// arguments into a tuple object.
						packaged := make([]*TupleTypeArg, 0, len(removedArgs))
						for _, typeArg := range removedArgs {
							packaged = append(packaged, &TupleTypeArg{
								Type:        typeArg.Type,
								IsUnbounded: typeArg.IsUnbounded,
								IsOptional:  typeArg.IsOptional,
							})
						}

						variadicTuple = ClassTypeCloneAsInstance(
							SpecializeTupleClass(tupleClass, packaged, true, true), true)
					}

					tupleArgsInsert(srcTypeArgs, destUnboundedOrVariadicIndex,
						&TupleTypeArg{Type: variadicTuple, IsUnbounded: false})
				}

				skipAdjustSrc = true
			}
		}
	}

	if !skipAdjustSrc && destUnboundedOrVariadicIndex >= 0 && srcArgsToCapture >= 0 {
		// The original's comment: if possible, package up the source entries that
		// correspond to the dest unbounded tuple. This isn't possible if the source
		// contains an unbounded tuple outside of this range.
		if srcUnboundedIndex < 0 ||
			(srcUnboundedIndex >= destUnboundedOrVariadicIndex &&
				srcUnboundedIndex < destUnboundedOrVariadicIndex+srcArgsToCapture) {
			removed := tupleArgsSplice(srcTypeArgs, destUnboundedOrVariadicIndex, srcArgsToCapture)

			removedArgTypes := make([]Type, 0, len(removed))
			for _, t := range removed {
				if IsTypeVar(t.Type) && IsUnpackedTypeVarTuple(t.Type) {
					removedArgTypes = append(
						removedArgTypes, TypeVarTypeCloneForUnpacked(t.Type.(*TypeVarType), true))
					continue
				}
				removedArgTypes = append(removedArgTypes, t.Type)
			}

			var combined Type
			if len(removedArgTypes) > 0 {
				combined = CombineTypes(removedArgTypes, nil)
			} else {
				combined = AnyTypeCreate(false)
			}

			tupleArgsInsert(srcTypeArgs, destUnboundedOrVariadicIndex,
				&TupleTypeArg{Type: combined, IsUnbounded: false})
		}
	}

	return len(*destTypeArgs) == len(*srcTypeArgs)
}
