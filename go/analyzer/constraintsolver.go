/*
 * constraintsolver.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/constraintSolver.ts (pyright 1.1.412):
 * assignTypeVar's dispatch, and the whole solving half -- solveConstraints,
 * solveConstraintSet, solveTypeVarRecursive, getTypeVarType and
 * applySourceSolutionToConstraints.
 *
 * The constraint solver is what four independent paths were waiting on: alias
 * specialization, class specialization, call validation and assignment all
 * reach assignTypeVar or solveConstraints. Landing the solving half first is
 * deliberate -- it is the part that turns recorded bounds into answers, and it
 * has no dependency on the four assign* arms, which can arrive separately.
 *
 * solveTypeVarRecursive is the piece worth reading twice. A solved value may
 * itself mention other unsolved TypeVars, so it recurses to solve those and then
 * substitutes them in. Recursion is bounded by writing `undefined` into the
 * solution set BEFORE solving -- hasType then answers true for a TypeVar
 * currently being solved, so a cycle returns undefined rather than looping.
 * That is why the map distinguishes "present with no value" from "absent", and
 * why it cannot be a plain lookup.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
)

// AssignTypeVar corresponds to assignTypeVar.
func AssignTypeVar(
	evaluator TypeEvaluator,
	destType *TypeVarType,
	srcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	// The original logs here when logConstraintsUpdates, a constant that is
	// false in the shipped source.

	// The original's comment: if both src and dest types are packed, unpack them
	// both.
	if IsUnpacked(destType) && IsUnpacked(srcType) {
		destType = TypeVarTypeCloneForPacked(destType)
		srcType = MakePacked(srcType)
	}

	// The original's comment: if the TypeVar doesn't have a scope ID, then it's
	// being used outside of a valid TypeVar scope. This will be reported as a
	// separate error. Just ignore this case to avoid redundant errors.
	if destType.Priv.ScopeID == "" {
		return true
	}

	if TypeVarTypeIsBound(destType) && !TypeVarTypeIsUnification(destType) {
		return assignBoundTypeVar(evaluator, destType, srcType, diag, flags)
	}

	// The original's comment: handle type[T] as a dest and a special form as a
	// source.
	if destType.Base().IsInstantiable() && IsInstantiableClass(srcType) &&
		evaluator.IsSpecialFormClass(srcType.(*ClassType), flags) {
		return false
	}

	// The original's comment: a TypeVar can always be assigned to itself, but we
	// won't record this in the constraints.
	if IsTypeSame(destType, srcType, TypeSameOptions{}, 0) {
		return true
	}

	if IsParamSpec(destType) {
		// The original's comment: handle ParamSpecs specially.
		return assignParamSpec(evaluator, destType, srcType, diag, constraints, recursionCount)
	}

	if IsTypeVarTuple(destType) && !destType.Priv.IsInUnion {
		if destType.Priv.IsUnpacked {
			tupleClassType := evaluator.GetTupleClassType()

			if !IsUnpacked(srcType) && tupleClassType != nil {
				// The original's comment: package up the type into a tuple.
				srcType = ConvertToInstance(SpecializeTupleClass(
					tupleClassType,
					[]*TupleTypeArg{{Type: srcType, IsUnbounded: false}},
					true,
					true,
				), false)
			}
		} else {
			srcType = MakeUnpacked(srcType)
		}
	}

	// The original's comment: if we're assigning an unpacked TypeVarTuple to a
	// regular TypeVar, we need to treat it as a union of the unpacked
	// TypeVarTuple.
	if IsTypeVarTuple(srcType) {
		srcTypeVar := srcType.(*TypeVarType)
		if srcTypeVar.Priv.IsUnpacked && !srcTypeVar.Priv.IsInUnion && !IsTypeVarTuple(destType) {
			srcType = TypeVarTypeCloneForUnpacked(srcTypeVar, true)
		}
	}

	// The original's comment: handle the constrained case. This case needs to be
	// handled specially because type narrowing isn't used in this case. For
	// example, if the source type is "Literal[1]" and the constraint list
	// includes the type "float", the resulting type is float.
	if TypeVarTypeHasConstraints(destType) {
		return assignConstrainedTypeVar(evaluator, destType, srcType, diag, constraints, flags, recursionCount)
	}

	return assignUnconstrainedTypeVar(evaluator, destType, srcType, diag, constraints, flags, recursionCount)
}

// SolveConstraints corresponds to solveConstraints. The original's comment:
// returns a solution for the type variables tracked by the constraint tracker.
func SolveConstraints(
	evaluator TypeEvaluator,
	constraints *ConstraintTracker,
	options *SolveConstraintsOptions,
) *ConstraintSolution {
	constraintSets := constraints.GetConstraintSets()
	if len(constraintSets) == 0 {
		return NewConstraintSolution(nil)
	}

	// The single-set case is the overwhelming majority (only literal-expansion
	// paths carry more than one constraint set); co-allocate the whole result
	// as one heap object. This path runs for every call-site solve.
	if len(constraintSets) == 1 {
		combined := &struct {
			sol  ConstraintSolution
			set  ConstraintSolutionSet
			sets [1]*ConstraintSolutionSet
		}{}
		combined.sets[0] = &combined.set
		combined.sol.solutionSets = combined.sets[:]
		solveConstraintSetInto(evaluator, constraintSets[0], options, &combined.set)
		return &combined.sol
	}

	// One backing array for all the solution sets rather than one heap object
	// per set.
	backing := make([]ConstraintSolutionSet, len(constraintSets))
	solutionSets := make([]*ConstraintSolutionSet, len(constraintSets))
	for i, constraintSet := range constraintSets {
		solveConstraintSetInto(evaluator, constraintSet, options, &backing[i])
		solutionSets[i] = &backing[i]
	}

	return &ConstraintSolution{solutionSets: solutionSets}
}

// ApplySourceSolutionToConstraints corresponds to the function of the same name.
// The original's comment: applies solved TypeVars from one context to this
// context.
func ApplySourceSolutionToConstraints(constraints *ConstraintTracker, srcSolution *ConstraintSolution) {
	if srcSolution.IsEmpty() {
		return
	}

	constraints.DoForEachConstraintSet(func(constraintSet *ConstraintSet, _ int) {
		for _, entry := range constraintSet.GetTypeVars() {
			var lowerBound, upperBound Type
			if entry.LowerBound != nil {
				lowerBound = ApplySolvedTypeVars(entry.LowerBound, srcSolution, nil)
			}
			if entry.UpperBound != nil {
				upperBound = ApplySolvedTypeVars(entry.UpperBound, srcSolution, nil)
			}
			constraintSet.SetBounds(entry.TypeVar, lowerBound, upperBound, entry.RetainLiterals)
		}
	})
}

// SolveConstraintSet corresponds to solveConstraintSet.
func SolveConstraintSet(
	evaluator TypeEvaluator,
	constraintSet *ConstraintSet,
	options *SolveConstraintsOptions,
) *ConstraintSolutionSet {
	solutionSet := NewConstraintSolutionSet()
	solveConstraintSetInto(evaluator, constraintSet, options, solutionSet)
	return solutionSet
}

// solveConstraintSetInto is SolveConstraintSet writing into caller-provided
// storage, which lets SolveConstraints batch its sets in one allocation.
func solveConstraintSetInto(
	evaluator TypeEvaluator,
	constraintSet *ConstraintSet,
	options *SolveConstraintsOptions,
	solutionSet *ConstraintSolutionSet,
) {
	// Solve the type variables.
	constraintSet.DoForEachTypeVar(func(entry *TypeVarConstraints) {
		solveTypeVarRecursive(evaluator, constraintSet, options, solutionSet, entry)
	})
}

// solveTypeVarRecursive corresponds to the function of the same name.
func solveTypeVarRecursive(
	evaluator TypeEvaluator,
	constraintSet *ConstraintSet,
	options *SolveConstraintsOptions,
	solutionSet *ConstraintSolutionSet,
	entry *TypeVarConstraints,
) Type {
	// The original's comment: if this TypeVar already has a solution, don't
	// attempt to re-solve it.
	if solutionSet.HasType(entry.TypeVar) {
		return solutionSet.GetType(entry.TypeVar)
	}

	// The original's comment: protect against infinite recursion by setting the
	// initial value to undefined. This relies on HasType distinguishing a key
	// present with a nil value from an absent one.
	solutionSet.SetType(entry.TypeVar, nil)

	useLowerBoundOnly := options != nil && options.UseLowerBoundOnly
	value := getTypeVarType(evaluator, constraintSet, entry.TypeVar, useLowerBoundOnly)

	if value != nil {
		// The original's comment: are there any unsolved TypeVars in this type?
		typeVars := GetTypeVarArgsRecursive(value, 0)

		if len(typeVars) > 0 {
			dependentSolution := NewConstraintSolution(nil)
			sawDependent := false

			for _, typeVar := range typeVars {
				// The original's comment: don't attempt to replace a TypeVar
				// with itself.
				if IsTypeSame(typeVar, entry.TypeVar, TypeSameOptions{IgnoreTypeFlags: true}, 0) {
					continue
				}

				// The original's comment: don't attempt to solve or replace
				// bound TypeVars.
				if TypeVarTypeIsBound(typeVar) {
					continue
				}

				dependentEntry := constraintSet.GetTypeVar(typeVar)
				if dependentEntry == nil {
					continue
				}

				dependentType := solveTypeVarRecursive(evaluator, constraintSet, options, solutionSet, dependentEntry)

				if dependentType != nil {
					dependentSolution.SetType(typeVar, dependentType)
					sawDependent = true
				}
			}

			// The original's comment: apply the dependent TypeVar values to the
			// current TypeVar value.
			//
			// The original tests `!dependentSolution.isEmpty()`. A fresh
			// ConstraintSolution here holds one empty solution set, so isEmpty
			// answers the same thing as "nothing was set"; sawDependent records
			// that directly rather than relying on the constructor's shape.
			if sawDependent {
				value = ApplySolvedTypeVars(value, dependentSolution, nil)
			}
		}
	}

	solutionSet.SetType(entry.TypeVar, value)
	return value
}

// getTypeVarType corresponds to the function of the same name: turn one
// TypeVar's recorded bounds into a single type.
func getTypeVarType(
	evaluator TypeEvaluator,
	constraintSet *ConstraintSet,
	typeVar *TypeVarType,
	useLowerBoundOnly bool,
) Type {
	entry := constraintSet.GetTypeVar(typeVar)
	if entry == nil {
		return nil
	}

	if IsParamSpec(typeVar) {
		if entry.LowerBound == nil {
			return nil
		}

		if IsFunction(entry.LowerBound) {
			return entry.LowerBound
		}

		if IsAnyOrUnknown(entry.LowerBound) {
			return ParamSpecTypeGetUnknown()
		}
	}

	lowerBound := entry.LowerBound
	if lowerBound != nil {
		if !entry.RetainLiterals {
			lowerNoLiterals := stripLiteralsForLowerBound(evaluator, typeVar, lowerBound)

			// The original's comment: if we can widen the lower bound to a
			// non-literal type without exceeding the upper bound, use the
			// widened type.
			if lowerNoLiterals != lowerBound {
				if entry.UpperBound == nil ||
					evaluator.AssignType(entry.UpperBound, lowerNoLiterals, nil, nil, AssignTypeFlagsDefault, 0) {
					if TypeVarTypeHasConstraints(typeVar) {
						// The original's comment: does it still match a value
						// constraint?
						for _, constraint := range typeVar.Shared.Constraints {
							if IsTypeSame(lowerNoLiterals, constraint, TypeSameOptions{}, 0) {
								lowerBound = lowerNoLiterals
								break
							}
						}
					} else {
						lowerBound = lowerNoLiterals
					}
				}
			}
		}

		return lowerBound
	}

	if !useLowerBoundOnly {
		return entry.UpperBound
	}

	return nil
}

// stripLiteralsForLowerBound corresponds to the function of the same name.
func stripLiteralsForLowerBound(evaluator TypeEvaluator, typeVar *TypeVarType, lowerBound Type) Type {
	if IsTypeVarTuple(typeVar) {
		return stripLiteralValueForUnpackedTuple(evaluator, lowerBound)
	}
	return StripTypeForm(evaluator.StripLiteralValue(lowerBound))
}

/*
 * The four assignment arms and the two tuple helpers, each a separate unit of
 * work. assignTypeVar above is pure dispatch, so each records itself and the
 * frontier ranks them.
 */

// noteConstraintSolverUnported records an unported constraintSolver path on the
// evaluator's counter. These are free functions rather than evaluator methods,
// matching the original's module shape, so they reach the counter through an
// interface assertion as codeflowengine does.
func noteConstraintSolverUnported(evaluator TypeEvaluator, name string) {
	if reporter, ok := evaluator.(interface{ noteUnported(string) }); ok {
		reporter.noteUnported(name)
	}
}
