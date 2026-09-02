/*
 * typeevaluator_assignunion.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * assignToUnionType.
 *
 * Assigning something to a union destination. Covariantly this is easy -- match
 * any one member -- and almost all of the code here is about the two cases where
 * it is not.
 *
 * Invariance. `list[int | str]` requires the source to satisfy EVERY member, not
 * one, so each is checked. But a union can contain a member subsumed by another
 * -- `int | bool` -- and failing against `bool` is harmless when `int` accepts
 * the source, so a failing member is forgiven if some other member accepts it.
 *
 * TypeVars in the destination. `T | None` cannot stop at the first match,
 * because matching `None` first would leave T unsolved while a later member
 * would have solved it. So every member is tried, each against a CLONE of the
 * constraints, and the clone that scores highest is copied back. Three things
 * bias that score:
 *
 *   - An exact match wins outright, scoring infinity.
 *   - A bare TypeVar with no existing constraints is handicapped very slightly,
 *     so a TypeVar that already has constraints is preferred.
 *   - If more than one bare TypeVar matched during the first pass of argument
 *     assignment, nothing is recorded at all. The original explains: it is
 *     dangerous to commit, and a later argument will usually constrain them.
 *
 * Two fast paths sit in front of that. `None` against an Optional matches
 * immediately without touching the TypeVar, and a literal source consults the
 * union's literal map rather than walking hundreds of literal members.
 */

package analyzer

import (
	"math"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// assignToUnionType corresponds to the function of the same name.
func (e *typeEvaluator) assignToUnionType(
	destType *UnionType,
	srcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	if (flags & AssignTypeFlagsInvariant) != 0 {
		return e.assignToUnionInvariant(destType, srcType, diag, constraints, flags, recursionCount)
	}

	// The original's comment: for union destinations, we just need to match one of
	// the types.
	var diagAddendum *common.DiagnosticAddendum
	if diag != nil {
		diagAddendum = common.NewDiagnosticAddendum()
	}

	foundMatch := false

	// The original's comment: does the union contain any type variables that need
	// to be solved? If so, we need to use a slower path.
	if !RequiresSpecialization(destType, nil, 0) {
		for _, subtype := range destType.Priv.Subtypes {
			if e.AssignType(subtype, srcType, createAddendumOrNil(diagAddendum),
				constraints, flags, recursionCount) {
				foundMatch = true
				break
			}
		}
	} else {
		matched, handled := e.assignToUnionWithTypeVars(
			destType, srcType, diagAddendum, constraints, flags, recursionCount)
		if handled {
			return true
		}
		foundMatch = matched
	}

	// The original's comment: if the source is a constrained TypeVar, see if we
	// can assign all of the constraints to the union.
	if !foundMatch {
		if srcTypeVar, ok := srcType.(*TypeVarType); ok && TypeVarTypeHasConstraints(srcTypeVar) {
			foundMatch = e.AssignType(destType, e.MakeTopLevelTypeVarsConcrete(srcType, false),
				createAddendumOrNil(diagAddendum), constraints, flags, recursionCount)
		}
	}

	if !foundMatch {
		if diag != nil && diagAddendum != nil {
			types := e.PrintSrcDestTypes(srcType, destType)
			diag.AddMessage(localization.LocAddendum.TypeAssignmentMismatch().Format(
				types.SourceType, types.DestType))
			diag.AddAddendum(diagAddendum)
		}
		return false
	}

	return true
}

// assignToUnionInvariant is the AssignTypeFlags.Invariant branch.
//
// The original's comment: if we need to enforce invariance, the source needs to
// be compatible with all subtypes in the dest, unless those subtypes are
// subclasses of other subtypes.
func (e *typeEvaluator) assignToUnionInvariant(
	destType *UnionType,
	srcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	isIncompatible := false

	DoForEachSubtype(destType, func(subtype Type, index int, _ []Type) {
		if isIncompatible {
			return
		}

		if e.AssignType(subtype, srcType, createAddendumOrNil(diag), constraints, flags, recursionCount) {
			return
		}

		// The original's comment: determine whether this subtype is subsumed by
		// some other subtype in the union. If so, we can ignore the
		// incompatibility.
		skipSubtype := false

		if !IsAnyOrUnknown(subtype) {
			// Both sides are made bound with no scope IDs, which transforms every
			// TypeVar rather than a selected set; comparing a free against a bound
			// TypeVar would never match.
			adjSubtype := MakeTypeVarsBound(subtype, nil, false)

			DoForEachSubtype(destType, func(otherSubtype Type, otherIndex int, _ []Type) {
				if index == otherIndex || skipSubtype {
					return
				}

				adjOtherSubtype := MakeTypeVarsBound(otherSubtype, nil, false)
				if e.AssignType(adjOtherSubtype, adjSubtype, nil, nil, AssignTypeFlagsDefault, recursionCount) {
					skipSubtype = true
				}
			})
		}

		if !skipSubtype {
			isIncompatible = true
		}
	})

	if isIncompatible {
		if diag != nil {
			types := e.PrintSrcDestTypes(srcType, destType)
			diag.AddMessage(localization.LocAddendum.TypeAssignmentMismatch().Format(
				types.SourceType, types.DestType))
		}
		return false
	}

	return true
}

// assignToUnionWithTypeVars is the slow path. The second return reports the
// original's early `return true` from the literal fast path.
//
// The original's comment: run through all subtypes in the union. Don't stop at
// the first match we find because we may need to match TypeVars in other
// subtypes. We special-case "None" so we can handle Optional[T] without matching
// the None to the type var.
func (e *typeEvaluator) assignToUnionWithTypeVars(
	destType *UnionType,
	srcType Type,
	diagAddendum *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) (foundMatch bool, handled bool) {
	if IsNoneInstance(srcType) && IsOptionalType(destType) {
		return true, false
	}

	// The original's comment: if the srcType is a literal, try to use the
	// fast-path lookup in case the destType is a union with hundreds of literals.
	if IsClassInstance(srcType) && IsLiteralType(srcType.(*ClassType)) &&
		UnionTypeContainsType(destType, srcType, TypeSameOptions{}, nil, recursionCount) {
		return true, true
	}

	var bestConstraints *ConstraintTracker
	var bestConstraintsScore float64
	haveBestScore := false
	nakedTypeVarMatches := 0

	DoForEachSubtypeSorted(destType, func(subtype Type, _ int, _ []Type) {
		// The original's comment: make a temporary clone of the constraints. We
		// don't want to modify the original constraints until we find the "optimal"
		// typeVar mapping.
		var constraintsClone *ConstraintTracker
		if constraints != nil {
			constraintsClone = constraints.Clone()
		}

		if !e.AssignType(subtype, srcType, createAddendumOrNil(diagAddendum),
			constraintsClone, flags, recursionCount) {
			return
		}

		foundMatch = true

		if constraintsClone == nil {
			return
		}

		// The original's comment: ask the constraints to compute a "score" for the
		// current contents of the table.
		constraintsScore := constraintsClone.GetScore()

		if subtypeTypeVar, ok := subtype.(*TypeVarType); ok {
			if constraints == nil || constraints.GetMainConstraintSet().GetTypeVar(subtypeTypeVar) == nil {
				nakedTypeVarMatches++

				// The original's comment: handicap the solution slightly so another
				// type var with existing constraints will be preferred.
				constraintsScore += 0.001
			}
		}

		// The original's comment: if the type matches exactly, prefer it over other
		// types.
		if IsTypeSame(subtype, e.StripLiteralValue(srcType), TypeSameOptions{}, 0) {
			constraintsScore = math.Inf(1)
		}

		// `<=` rather than `<`: a later subtype with an equal score wins, which
		// makes the sorted iteration order decide ties.
		if !haveBestScore || bestConstraintsScore <= constraintsScore {
			bestConstraintsScore = constraintsScore
			haveBestScore = true
			bestConstraints = constraintsClone
		}
	})

	// The original's comment: if we saw more than one "naked" type vars that have
	// no previous constraints recorded, it's dangerous for us to assign a value to
	// any of these type vars at this time. Typically, they will receive some
	// constraints via some later argument assignment.
	if nakedTypeVarMatches > 1 && (flags&AssignTypeFlagsArgAssignmentFirstPass) != 0 {
		bestConstraints = nil
	}

	// The original's comment: if we found a winning type var mapping, copy it back
	// to constraints.
	if constraints != nil && bestConstraints != nil {
		constraints.CopyFromClone(bestConstraints)
	}

	return foundMatch, false
}

// createAddendumOrNil is the original's `diag?.createAddendum()`.
func createAddendumOrNil(diag *common.DiagnosticAddendum) *common.DiagnosticAddendum {
	if diag == nil {
		return nil
	}
	return diag.CreateAddendum()
}
