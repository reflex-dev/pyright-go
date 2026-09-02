/*
 * constraintsolver_assign.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/constraintSolver.ts (pyright 1.1.412):
 * assignBoundTypeVar, assignUnconstrainedTypeVar, assignConstrainedTypeVar,
 * assignParamSpec, typeVarOccursIn, widenTypeForTypeVarTuple,
 * stripLiteralValueForUnpackedTuple.
 *
 * The four arms of assigning a type TO a type variable. Which arm runs is
 * decided by what kind of variable it is, and they behave quite differently.
 *
 * A BOUND TypeVar is one that has already been fixed to a concrete class -- the
 * `T` inside a method body of `class C(Generic[T])`. There is nothing to solve,
 * so assignBoundTypeVar is almost entirely a list of the things that are
 * assignable to anything: Any, Never outside an invariant context, a gradual
 * callable for a ParamSpec, `type[Any]`. Everything else is an error, except for
 * synthesized TypeVars where the message would confuse more than help.
 *
 * An UNCONSTRAINED TypeVar is the ordinary case, and the assignment narrows a
 * pair of bounds rather than picking a value. The lower bound is what has been
 * assigned so far and only ever widens; the upper bound is what is allowed and
 * only ever narrows. Which one this assignment touches depends on the variance:
 * covariantly the lower bound, contravariantly the upper, invariantly both, and
 * they must not cross. Widening two incompatible lower bounds means unioning
 * them -- with a cap, because in pathological cases the union grows until
 * performance collapses, and widening to `object` is still a valid solution.
 *
 * The occurs check in the lower-bound arm is the interesting part. `T := F[T]`
 * has no finite solution, and recording it makes later substitution rounds
 * expand forever. A bare `T := T` is fine, and so is a top-level union member --
 * only a strictly nested occurrence is fatal.
 *
 * A CONSTRAINED TypeVar (`AnyStr`) does not narrow anything: it SELECTS one of
 * its declared constraints. Every subtype of the source has to land on the same
 * constraint, unless it is conditioned on the TypeVar itself -- which is why
 * `str | bytes` cannot be assigned to `AnyStr` even though each half can.
 *
 * A PARAM SPEC has no bounds and no constraints, so with no tracker there is
 * nothing to do at all. With one, each constraint set independently decides
 * whether the new signature is narrower or wider than what it holds, preferring
 * a real signature over `...` in the same way normal TypeVars prefer a real type
 * over Any.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// maxSubtypeCountForTypeVarLowerBound corresponds to the constant of the same
// name.
const maxSubtypeCountForTypeVarLowerBound = 64

// assignBoundTypeVar corresponds to the function of the same name.
//
// The original's comment: handles an assignment to a TypeVar that is "bound"
// rather than "free". In general such assignments are not allowed, but there are
// some special cases to be handled.
func assignBoundTypeVar(
	evaluator TypeEvaluator,
	destType *TypeVarType,
	srcType Type,
	diag *common.DiagnosticAddendum,
	flags AssignTypeFlags,
) bool {
	// The original's comment: handle Any as a source.
	if IsAnyOrUnknown(srcType) ||
		(IsClass(srcType) && ClassTypeDerivesFromAnyOrUnknown(srcType.(*ClassType))) {
		return true
	}

	// The original's comment: is this the equivalent of an "Unknown" for a
	// ParamSpec?
	if IsParamSpec(destType) {
		if fn, ok := srcType.(*FunctionType); ok && FunctionTypeIsGradualCallableForm(fn) {
			return true
		}
	}

	// The original's comment: Never is always assignable except in an invariant
	// context.
	isInvariant := (flags & AssignTypeFlagsInvariant) != 0
	if IsNever(srcType) && !isInvariant {
		return true
	}

	// The original's comment: handle a type[Any] as a source.
	if IsClassInstance(srcType) && ClassTypeIsBuiltInNamed(srcType.(*ClassType), "type") {
		srcClass := srcType.(*ClassType)
		if len(srcClass.Priv.TypeArgs) < 1 || IsAnyOrUnknown(srcClass.Priv.TypeArgs[0]) {
			if destType.Base().IsInstantiable() {
				return true
			}
		}
	}

	// The original's comment: emit an error unless this is a synthesized type
	// variable used for pseudo-generic classes.
	if !destType.Shared.IsSynthesized || TypeVarTypeIsSelf(destType) {
		if diag != nil {
			types := evaluator.PrintSrcDestTypes(srcType, destType)
			diag.AddMessage(localization.LocAddendum.TypeAssignmentMismatch().Format(
				types.SourceType, types.DestType))
		}
	}

	return false
}

// assignUnconstrainedTypeVar corresponds to the function of the same name.
//
// Its comment: handles assignments to a TypeVarTuple or a TypeVar that does not
// have value constraints (but may have an upper bound).
func assignUnconstrainedTypeVar(
	evaluator TypeEvaluator,
	destType *TypeVarType,
	srcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	isInvariant := (flags & AssignTypeFlagsInvariant) != 0
	isContravariant := (flags&AssignTypeFlagsContravariant) != 0 && !isInvariant

	// The original's comment: handle the unconstrained (but possibly bound) case.
	var curEntry *TypeVarConstraints
	if constraints != nil {
		curEntry = constraints.GetMainConstraintSet().GetTypeVar(destType)
	}

	var curUpperBound Type
	if curEntry != nil {
		curUpperBound = curEntry.UpperBound
	}
	if curUpperBound == nil && !TypeVarTypeIsSelf(destType) {
		curUpperBound = destType.Shared.BoundType
	}
	var curLowerBound Type
	if curEntry != nil {
		curLowerBound = curEntry.LowerBound
	}
	newLowerBound := curLowerBound
	newUpperBound := curUpperBound

	var diagAddendum *common.DiagnosticAddendum
	if diag != nil {
		diagAddendum = common.NewDiagnosticAddendum()
	}

	adjSrcType := srcType

	// The original's comment: if the source is a class that is missing type
	// arguments, fill in missing type arguments with Unknown.
	if (flags & AssignTypeFlagsAllowUnspecifiedTypeArgs) == 0 {
		if cls, ok := adjSrcType.(*ClassType); ok && IsClass(adjSrcType) && cls.Priv.IncludeSubclasses {
			adjSrcType = SpecializeWithDefaultTypeArgs(cls)
		}
	}

	if destType.Base().IsInstantiable() {
		if IsEffectivelyInstantiable(adjSrcType, nil, 0) {
			adjSrcType = ConvertToInstance(adjSrcType, false)
		} else {
			// The original's comment: handle the case of a TypeVar that has a bound
			// of `type`.
			concreteAdjSrcType := evaluator.MakeTopLevelTypeVarsConcrete(adjSrcType, false)

			if IsEffectivelyInstantiable(concreteAdjSrcType, nil, 0) {
				adjSrcType = ConvertToInstance(concreteAdjSrcType, false)
			} else {
				if diag != nil {
					types := evaluator.PrintSrcDestTypes(srcType, destType)
					diag.AddMessage(localization.LocAddendum.TypeAssignmentMismatch().Format(
						types.SourceType, types.DestType))
				}
				return false
			}
		}
	} else if IsTypeVar(srcType) && srcType.Base().IsInstantiable() &&
		IsTypeSame(ConvertToInstance(srcType, false), destType, TypeSameOptions{}, 0) {
		if diag != nil {
			types := evaluator.PrintSrcDestTypes(adjSrcType, destType)
			diag.AddMessage(localization.LocAddendum.TypeAssignmentMismatch().Format(
				types.SourceType, types.DestType))
		}
		return false
	}

	if (flags & AssignTypeFlagsPopulateExpectedType) != 0 {
		if (flags&AssignTypeFlagsSkipPopulateUnknownExpectedType) != 0 && IsUnknown(adjSrcType) {
			return true
		}

		// The original's comment: if we're populating the expected type, constrain
		// either the lower type bound, upper type bound or both. Don't overwrite an
		// existing entry.
		if curEntry == nil {
			if isInvariant {
				newLowerBound = adjSrcType
				newUpperBound = adjSrcType
			} else if isContravariant {
				newLowerBound = adjSrcType
			} else {
				newUpperBound = adjSrcType
			}
		}
	} else if isContravariant {
		var ok bool
		newUpperBound, ok = narrowUpperBound(evaluator, destType, adjSrcType, curUpperBound, curLowerBound,
			diag, diagAddendum, recursionCount)
		if !ok {
			return false
		}
	} else {
		var ok bool
		newLowerBound, curLowerBound, ok = widenLowerBound(evaluator, destType, adjSrcType,
			curLowerBound, newUpperBound, curEntry, diag, diagAddendum, constraints,
			flags, isInvariant, recursionCount)
		if !ok {
			return false
		}

		// The original's comment: if this is an invariant context, make sure the
		// lower bound isn't too wide.
		if isInvariant && newLowerBound != nil {
			if !evaluator.AssignType(adjSrcType, newLowerBound, createAddendumOrNil(diag),
				nil, AssignTypeFlagsDefault, recursionCount) {
				if diag != nil && diagAddendum != nil {
					types := evaluator.PrintSrcDestTypes(newLowerBound, adjSrcType)
					diag.AddMessage(localization.LocAddendum.TypeAssignmentMismatch().Format(
						types.SourceType, types.DestType))
				}
				return false
			}
		}

		// The original's comment: make sure we don't exceed the upper bound.
		if curUpperBound != nil && newLowerBound != nil {
			if !IsTypeSame(curUpperBound, newLowerBound, TypeSameOptions{}, recursionCount) {
				if !evaluator.AssignType(curUpperBound, newLowerBound, createAddendumOrNil(diag),
					nil, AssignTypeFlagsDefault, recursionCount) {
					if diag != nil && diagAddendum != nil {
						types := evaluator.PrintSrcDestTypes(newLowerBound, curUpperBound)
						diag.AddMessage(localization.LocAddendum.TypeAssignmentMismatch().Format(
							types.SourceType, types.DestType))
					}
					return false
				}
			}
		}
	}

	if newUpperBound == nil && isInvariant {
		newUpperBound = newLowerBound
	}

	// The original's comment: if there's a bound type, make sure the source is
	// assignable to it.
	if destType.Shared.BoundType != nil {
		updatedType := newLowerBound
		if updatedType == nil {
			updatedType = newUpperBound
		}

		// The original's comment: if the dest is a Type[T] but the source is not a
		// valid Type, skip the assignType check and the diagnostic addendum, which
		// will be confusing and inaccurate.
		if destType.Base().IsInstantiable() &&
			!IsEffectivelyInstantiable(srcType, &IsInstantiableOptions{HonorTypeVarBounds: true}, 0) {
			return false
		}

		// The original's comment: in general, bound types cannot be generic, but the
		// "Self" type is an exception. In this case, we need to use the original
		// constraints to solve for the generic type variable(s) in the bound type.
		var effectiveConstraints *ConstraintTracker
		if TypeVarTypeIsSelf(destType) {
			effectiveConstraints = constraints
		}

		if !evaluator.AssignType(destType.Shared.BoundType,
			evaluator.MakeTopLevelTypeVarsConcrete(updatedType, false),
			createAddendumOrNil(diag), effectiveConstraints, AssignTypeFlagsDefault, recursionCount) {
			// The original's comment: avoid adding a message that will confuse users
			// if the TypeVar was synthesized for internal purposes.
			if !destType.Shared.IsSynthesized && diag != nil {
				diag.AddMessage(localization.LocAddendum.TypeBound().Format(
					evaluator.PrintType(updatedType, nil),
					evaluator.PrintType(destType.Shared.BoundType, nil),
					TypeVarTypeGetReadableName(destType, true)))
			}
			return false
		}
	}

	if constraints != nil {
		constraints.SetBounds(destType, newLowerBound, newUpperBound,
			(flags&(AssignTypeFlagsPopulateExpectedType|AssignTypeFlagsRetainLiteralsForTypeVar)) != 0)
	}

	return true
}

// narrowUpperBound is the original's contravariant arm. The second return
// reports whether the assignment succeeded.
func narrowUpperBound(
	evaluator TypeEvaluator,
	destType *TypeVarType,
	adjSrcType Type,
	curUpperBound Type,
	curLowerBound Type,
	diag *common.DiagnosticAddendum,
	diagAddendum *common.DiagnosticAddendum,
	recursionCount int,
) (Type, bool) {
	newUpperBound := curUpperBound

	// The original's comment: update the upper bound.
	if curUpperBound == nil || IsTypeSame(destType, curUpperBound, TypeSameOptions{}, 0) {
		newUpperBound = adjSrcType
	} else if !IsTypeSame(curUpperBound, adjSrcType, TypeSameOptions{}, recursionCount) {
		if evaluator.AssignType(curUpperBound, evaluator.MakeTopLevelTypeVarsConcrete(adjSrcType, false),
			diagAddendum, nil, AssignTypeFlagsDefault, recursionCount) {
			// The original's comment: the srcType is narrower than the current upper
			// bound, so replace it.
			newUpperBound = adjSrcType
		} else if !evaluator.AssignType(adjSrcType, curUpperBound, diagAddendum, nil,
			AssignTypeFlagsDefault, recursionCount) {
			if diag != nil && diagAddendum != nil {
				types := evaluator.PrintSrcDestTypes(curUpperBound, adjSrcType)
				diag.AddMessage(localization.LocAddendum.TypeAssignmentMismatch().Format(
					types.SourceType, types.DestType))
				diag.AddAddendum(diagAddendum)
			}
			return nil, false
		}
	}

	// The original's comment: make sure we haven't narrowed it beyond the current
	// lower bound.
	if curLowerBound != nil {
		if !evaluator.AssignType(newUpperBound, curLowerBound, nil, nil,
			AssignTypeFlagsDefault, recursionCount) {
			if diag != nil && diagAddendum != nil {
				types := evaluator.PrintSrcDestTypes(curLowerBound, newUpperBound)
				diag.AddMessage(localization.LocAddendum.TypeAssignmentMismatch().Format(
					types.SourceType, types.DestType))
				diag.AddAddendum(diagAddendum)
			}
			return nil, false
		}
	}

	return newUpperBound, true
}

// widenLowerBound is the original's covariant/invariant arm. It returns the new
// lower bound, the possibly-restripped current lower bound (the original mutates
// `curLowerBound` in one branch and the later invariant check reads it), and
// whether the assignment succeeded.
func widenLowerBound(
	evaluator TypeEvaluator,
	destType *TypeVarType,
	adjSrcType Type,
	curLowerBound Type,
	newUpperBound Type,
	curEntry *TypeVarConstraints,
	diag *common.DiagnosticAddendum,
	diagAddendum *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	isInvariant bool,
	recursionCount int,
) (Type, Type, bool) {
	if curLowerBound == nil || IsTypeSame(destType, curLowerBound, TypeSameOptions{}, 0) {
		// The original's comment: there was previously no lower bound. We've now
		// established one. Apply an occurs check: if `adjSrcType` references
		// `destType` (the TypeVar being solved) at a strictly nested position (e.g.
		// `R := F[R]` or `R := R | Awaitable[R]`), recording it as the lower bound
		// creates a cyclic constraint that subsequent widening / substitution rounds
		// expand into an exponentially growing recursive type. Such a constraint has
		// no finite solution, so report the assignment as a failure rather than
		// letting the analyzer hang. See microsoft/pyright#11413.
		//
		// Top-level union members that are exactly `destType` (e.g. `T := T | int`
		// arising from protocol matching against `T | int`) are *not* considered
		// cyclic - the original `adjSrcType` is recorded as the lower bound and
		// existing logic resolves it.
		if typeVarOccursIn(destType, adjSrcType) {
			if diag != nil {
				types := evaluator.PrintSrcDestTypes(adjSrcType, destType)
				diag.AddMessage(localization.LocAddendum.TypeAssignmentMismatch().Format(
					types.SourceType, types.DestType))
			}
			return nil, curLowerBound, false
		}
		return adjSrcType, curLowerBound, true
	}

	if IsTypeSame(curLowerBound, adjSrcType, TypeSameOptions{}, recursionCount) {
		// The original's comment: if this is an invariant context and there is
		// currently no upper bound established, use the "no literals" version of the
		// lower bound rather than a version that has literals.
		if newUpperBound == nil && isInvariant && curEntry != nil && !curEntry.RetainLiterals {
			return stripLiteralsForLowerBound(evaluator, destType, curLowerBound), curLowerBound, true
		}
		return curLowerBound, curLowerBound, true
	}

	if evaluator.AssignType(curLowerBound, adjSrcType, diagAddendum, constraints, flags, recursionCount) {
		// The original's comment: no need to widen. Stick with the existing type
		// unless it's unknown or partly unknown, in which case we'll replace it with
		// a known type as long as it doesn't violate the current lower bound.
		if IsPartlyUnknown(curLowerBound, 0) && !IsUnknown(adjSrcType) &&
			evaluator.AssignType(adjSrcType, curLowerBound, nil, constraints,
				AssignTypeFlagsDefault, recursionCount) {
			return adjSrcType, curLowerBound, true
		}

		newLowerBound := curLowerBound
		if constraints != nil {
			newLowerBound = evaluator.SolveAndApplyConstraints(newLowerBound, constraints, nil, nil)
		}
		return newLowerBound, curLowerBound, true
	}

	if IsTypeVar(curLowerBound) && !IsTypeVar(adjSrcType) &&
		evaluator.AssignType(evaluator.MakeTopLevelTypeVarsConcrete(curLowerBound, false),
			adjSrcType, diagAddendum, constraints, flags, recursionCount) {
		// The original's comment: if the existing lower bound was a TypeVar that is
		// not part of the current context we can replace it with the new source type.
		return adjSrcType, curLowerBound, true
	}

	if evaluator.AssignType(adjSrcType, curLowerBound, nil, constraints,
		AssignTypeFlagsDefault, recursionCount) {
		// The original's comment: if the source is a TypeVar that just got assigned
		// the value of the current lower bound, don't replace the current lower bound
		// with the TypeVar.
		if !IsTypeVar(adjSrcType) {
			return adjSrcType, curLowerBound, true
		}
		// The original leaves newLowerBound at curLowerBound in this case.
		return curLowerBound, curLowerBound, true
	}

	if IsTypeVarTuple(destType) {
		widenedType := widenTypeForTypeVarTuple(evaluator, curLowerBound, adjSrcType, isInvariant, recursionCount)
		if widenedType == nil {
			if diag != nil {
				types := evaluator.PrintSrcDestTypes(curLowerBound, adjSrcType)
				diag.AddMessage(localization.LocAddendum.TypeAssignmentMismatch().Format(
					types.SourceType, types.DestType))
			}
			return nil, curLowerBound, false
		}

		return widenedType, curLowerBound, true
	}

	objectType := evaluator.GetObjectType()

	// The original's comment: if this is an invariant context and there is
	// currently no upper bound established, use the "no literals" version of the
	// lower bound rather than a version that has literals.
	if newUpperBound == nil && isInvariant && curEntry != nil && !curEntry.RetainLiterals {
		curLowerBound = stripLiteralsForLowerBound(evaluator, destType, curLowerBound)
	}

	curSolvedLowerBound := curLowerBound
	if constraints != nil {
		curSolvedLowerBound = evaluator.SolveAndApplyConstraints(curLowerBound, constraints, nil, nil)
	}

	maxCount := maxSubtypeCountForTypeVarLowerBound

	// The original's comment: in some extreme edge cases, the lower bound can
	// become a union with so many subtypes that performance grinds to a halt.
	// We'll detect this case and widen the resulting type to an 'object' instead of
	// making the union even bigger. This is still a valid solution to the TypeVar.
	if union, ok := curSolvedLowerBound.(*UnionType); ok &&
		len(union.Priv.Subtypes) > maxSubtypesForInferredType &&
		TypeVarTypeHasBound(destType) && IsClassInstance(objectType) {
		return CombineTypes([]Type{curSolvedLowerBound, objectType},
			&CombineTypesOptions{MaxSubtypeCount: &maxCount}), curLowerBound, true
	}

	return CombineTypes([]Type{curSolvedLowerBound, adjSrcType},
		&CombineTypesOptions{MaxSubtypeCount: &maxCount}), curLowerBound, true
}

// assignConstrainedTypeVar corresponds to the function of the same name.
//
// Its comment: handles assignments to a TypeVar with value constraints.
func assignConstrainedTypeVar(
	evaluator TypeEvaluator,
	destType *TypeVarType,
	srcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	var constrainedType Type
	concreteSrcType := evaluator.MakeTopLevelTypeVarsConcrete(srcType, false)

	var curEntry *TypeVarConstraints
	if constraints != nil {
		curEntry = constraints.GetMainConstraintSet().GetTypeVar(destType)
	}

	var curUpperBound, curLowerBound Type
	if curEntry != nil {
		curUpperBound = curEntry.UpperBound
		curLowerBound = curEntry.LowerBound
	}
	retainLiterals := false

	if IsTypeVar(srcType) {
		if evaluator.AssignType(destType, concreteSrcType, nil, nil,
			AssignTypeFlagsDefault, recursionCount) {
			constrainedType = srcType

			// The original's comment: if the source and dest are both instantiables
			// (type[T]), then we need to convert to an instance (T).
			if srcType.Base().IsInstantiable() {
				constrainedType = ConvertToInstance(srcType, false)
			}
		}
	} else {
		constrainedType = selectConstraintForSource(
			evaluator, destType, concreteSrcType, flags, recursionCount)
	}

	// The original's comment: if there was no constrained type that was assignable
	// or there were multiple types that were assignable and they are not
	// conditional, it's an error.
	if constrainedType == nil {
		if diag != nil {
			diag.AddMessage(localization.LocAddendum.TypeConstrainedTypeVar().Format(
				evaluator.PrintType(srcType, nil), destType.Shared.Name))
		}
		return false
	} else if IsLiteralTypeOrUnion(constrainedType, false) {
		retainLiterals = true
	}

	if curLowerBound != nil && !IsAnyOrUnknown(curLowerBound) {
		if !evaluator.AssignType(curLowerBound, constrainedType, nil, nil,
			AssignTypeFlagsDefault, recursionCount) {
			// The original's comment: handle the case where one of the constrained
			// types is a wider version of another constrained type that was previously
			// assigned to the type variable.
			if evaluator.AssignType(constrainedType, curLowerBound, nil, nil,
				AssignTypeFlagsDefault, recursionCount) {
				if constraints != nil {
					constraints.SetBounds(destType, constrainedType, curUpperBound, false)
				}
			} else {
				if diag != nil {
					diag.AddMessage(localization.LocAddendum.TypeConstrainedTypeVar().Format(
						evaluator.PrintType(constrainedType, nil),
						evaluator.PrintType(curLowerBound, nil)))
				}
				return false
			}
		}
	} else {
		// The original's comment: assign the type to the type var.
		if constraints != nil {
			constraints.SetBounds(destType, constrainedType, curUpperBound, retainLiterals)
		}
	}

	return true
}

// selectConstraintForSource is the original's non-TypeVar arm of
// assignConstrainedTypeVar: pick the narrowest declared constraint that every
// subtype of the source lands on.
func selectConstraintForSource(
	evaluator TypeEvaluator,
	destType *TypeVarType,
	concreteSrcType Type,
	flags AssignTypeFlags,
	recursionCount int,
) Type {
	isCompatible := true

	// The original's comment: subtypes that are not conditionally dependent on the
	// dest type var must all map to the same constraint. For example, Union[str,
	// bytes] cannot be assigned to AnyStr.
	unconditionalConstraintIndex := -1

	adjustConstraint := func(constraint Type) Type {
		if destType.Base().IsInstantiable() {
			return ConvertToInstantiable(constraint, false)
		}
		return constraint
	}

	// The original's comment: find the narrowest constrained type that is
	// compatible.
	constrainedType := MapSubtypes(concreteSrcType, func(srcSubtype Type) Type {
		var constrainedSubtype Type

		if IsAnyOrUnknown(srcSubtype) {
			return srcSubtype
		}

		constraintIndexUsed := -1
		for i, constraint := range destType.Shared.Constraints {
			adjustedConstraint := adjustConstraint(constraint)
			if !evaluator.AssignType(adjustedConstraint, srcSubtype, nil, nil,
				AssignTypeFlagsDefault, recursionCount) {
				continue
			}

			if constrainedSubtype == nil ||
				evaluator.AssignType(adjustConstraint(constrainedSubtype), adjustedConstraint,
					nil, nil, AssignTypeFlagsDefault, recursionCount) {
				constrainedSubtype = AddConditionToType(constraint, GetTypeCondition(srcSubtype), nil)
				constraintIndexUsed = i
			}
		}

		if constrainedSubtype == nil {
			// The original's comment: we found a source subtype that is not
			// compatible with the dest. This is OK if we're handling the contravariant
			// case because only one subtype needs to be assignable in that case.
			if (flags & AssignTypeFlagsContravariant) == 0 {
				isCompatible = false
			}
		}

		// The original's comment: if this subtype isn't conditional, make sure it
		// maps to the same constraint index as previous unconditional subtypes.
		if constraintIndexUsed >= 0 && GetTypeCondition(srcSubtype) == nil {
			if unconditionalConstraintIndex >= 0 && unconditionalConstraintIndex != constraintIndexUsed {
				isCompatible = false
			}

			unconditionalConstraintIndex = constraintIndexUsed
		}

		return constrainedSubtype
	}, nil)

	if IsNever(constrainedType) || !isCompatible {
		constrainedType = nil
	}

	// The original's comment: if the type is a union, see if the entire union is
	// assignable to one of the constraints.
	if constrainedType == nil && IsUnion(concreteSrcType) {
		for _, constraint := range destType.Shared.Constraints {
			if evaluator.AssignType(adjustConstraint(constraint), concreteSrcType, nil, nil,
				AssignTypeFlagsDefault, recursionCount) {
				return constraint
			}
		}
	}

	return constrainedType
}

// assignParamSpec corresponds to the function of the same name.
//
// Its comment: handles assignments to a ParamSpec.
func assignParamSpec(
	evaluator TypeEvaluator,
	destType *TypeVarType,
	srcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	recursionCount int,
) bool {
	// The original's comment: if there is no constraint tracker, there's nothing
	// to do because param specs have no upper bounds or constraints.
	if constraints == nil {
		return true
	}

	isAssignable := true
	var adjSrcType Type
	if IsParamSpec(srcType) {
		adjSrcType = srcType
	} else {
		adjSrcType = ConvertTypeToParamSpecValue(srcType)
	}
	if fn, ok := adjSrcType.(*FunctionType); ok {
		adjSrcType = SimplifyFunctionToParamSpec(fn)
	}

	constraints.DoForEachConstraintSet(func(constraintSet *ConstraintSet, _ int) {
		if IsParamSpec(adjSrcType) {
			var existingType Type
			if entry := constraintSet.GetTypeVar(destType); entry != nil {
				existingType = entry.LowerBound
			}

			if existingType != nil {
				paramSpecValue := ConvertTypeToParamSpecValue(existingType)
				existingTypeParamSpec := FunctionTypeGetParamSpecFromArgsKwargs(paramSpecValue)
				existingTypeWithoutArgsKwargs := FunctionTypeCloneRemoveParamSpecArgsKwargs(paramSpecValue, false)

				if len(existingTypeWithoutArgsKwargs.Shared.Parameters) == 0 && existingTypeParamSpec != nil {
					// The original's comment: if there's an existing entry that
					// matches, that's fine.
					if IsTypeSame(existingTypeParamSpec, adjSrcType, TypeSameOptions{}, recursionCount) {
						return
					}
				}
			} else {
				constraintSet.SetBounds(destType, adjSrcType, nil, false)
				return
			}
		} else if newFunction, ok := adjSrcType.(*FunctionType); ok {
			updateContextWithNewFunction := false

			var existingType Type
			if entry := constraintSet.GetTypeVar(destType); entry != nil {
				existingType = entry.LowerBound
			}

			if existingType != nil {
				// The original's comment: convert the remaining portion of the
				// signature to a function for comparison purposes.
				existingFunction := SimplifyFunctionToParamSpec(ConvertTypeToParamSpecValue(existingType))

				isNewNarrower := evaluator.AssignType(existingFunction, newFunction, nil, nil,
					AssignTypeFlagsSkipReturnTypeCheck, recursionCount)

				isNewWider := evaluator.AssignType(newFunction, existingFunction, nil, nil,
					AssignTypeFlagsSkipReturnTypeCheck, recursionCount)

				// The original's comment: should we widen the type?
				if isNewNarrower && isNewWider {
					// The original's comment: the new type is both a supertype and a
					// subtype of the existing type. That means the two types are the
					// same or one (or both) have the type "..." (which is the ParamSpec
					// equivalent of "Any"). If only one has the type "...", we'll prefer
					// the other one. This is analogous to what we do with regular
					// TypeVars, where we prefer non-Any values.
					if !FunctionTypeIsGradualCallableForm(newFunction) {
						updateContextWithNewFunction = true
					} else {
						return
					}
				} else if isNewWider {
					updateContextWithNewFunction = true
				} else if isNewNarrower {
					// The original's comment: the existing function is already narrower
					// than the new function, so no need to narrow it further.
					return
				}
			} else {
				updateContextWithNewFunction = true
			}

			if updateContextWithNewFunction {
				constraintSet.SetBounds(destType, newFunction, nil, false)
				return
			}
		} else if IsAnyOrUnknown(adjSrcType) {
			return
		}

		if diag != nil {
			diag.AddMessage(localization.LocAddendum.TypeParamSpec().Format(
				evaluator.PrintType(adjSrcType, nil), destType.Shared.Name))
		}

		isAssignable = false
	})

	return isAssignable
}

// typeVarOccursIn corresponds to the function of the same name.
//
// Its comment: returns true if `typeVar` appears strictly inside `type` (i.e.
// nested within another type, not as the top-level type itself or as a top-level
// subtype of a union). Used as an occurs check to detect cyclic constraints
// during widening. A bare top-level reference is fine (`T := T` is the
// identity); a top-level union member can be subtracted before solving; only a
// strictly nested reference (`T := F[T]`) has no finite solution.
func typeVarOccursIn(typeVar *TypeVarType, t Type) bool {
	// The original's comment: a bare top-level TypeVar reference is not a cycle.
	// Compare by name + scope id rather than identity since pyright sometimes
	// clones TypeVars.
	if tv, ok := t.(*TypeVarType); ok && IsTypeVar(t) &&
		tv.Shared.Name == typeVar.Shared.Name && tv.Priv.ScopeID == typeVar.Priv.ScopeID {
		return false
	}

	// The original's comment: top-level union members that are exactly `typeVar`
	// are also fine; only count occurrences strictly inside other types.
	if union, ok := t.(*UnionType); ok {
		for _, subtype := range union.Priv.Subtypes {
			if typeVarOccursIn(typeVar, subtype) {
				return true
			}
		}
		return false
	}

	for _, tv := range GetTypeVarArgsRecursive(t, 0) {
		if tv.Shared.Name == typeVar.Shared.Name && tv.Priv.ScopeID == typeVar.Priv.ScopeID {
			return true
		}
	}
	return false
}

// widenTypeForTypeVarTuple corresponds to the function of the same name. It
// returns nil where the original returns undefined.
//
// Its comment: for normal TypeVars, the constraint solver can widen a type by
// combining two otherwise incompatible types into a union. For TypeVarTuples, we
// need to do the equivalent operation for unpacked tuples.
func widenTypeForTypeVarTuple(
	evaluator TypeEvaluator,
	type1 Type,
	type2 Type,
	isInvariant bool,
	recursionCount int,
) Type {
	// The original's comment: if the two types are not unpacked tuples, we can't
	// combine them.
	if !IsUnpackedClass(type1) || !IsUnpackedClass(type2) {
		return nil
	}

	class1 := type1.(*ClassType)
	class2 := type2.(*ClassType)

	// The original's comment: if the two unpacked tuples are not the same length,
	// we can't combine them.
	if class1.Priv.TupleTypeArgs == nil || class2.Priv.TupleTypeArgs == nil ||
		len(class1.Priv.TupleTypeArgs) != len(class2.Priv.TupleTypeArgs) {
		return nil
	}

	strippedType1 := stripLiteralValueForUnpackedTuple(evaluator, type1)
	strippedType2 := stripLiteralValueForUnpackedTuple(evaluator, type2)

	if !IsUnpackedClass(strippedType1) || !IsUnpackedClass(strippedType2) {
		return nil
	}

	strippedClass1 := strippedType1.(*ClassType)
	tupleTypeArgs1 := strippedClass1.Priv.TupleTypeArgs
	tupleTypeArgs2 := strippedType2.(*ClassType).Priv.TupleTypeArgs
	if tupleTypeArgs1 == nil || tupleTypeArgs2 == nil {
		return nil
	}

	for i := range tupleTypeArgs1 {
		typeArg1 := tupleTypeArgs1[i]
		typeArg2 := tupleTypeArgs2[i]

		if typeArg1.IsUnbounded != typeArg2.IsUnbounded || typeArg1.IsOptional != typeArg2.IsOptional {
			return nil
		}
	}

	if IsTypeSame(strippedType1, strippedType2, TypeSameOptions{}, 0) {
		return strippedType1
	}

	// The original's comment: the typing spec indicates that a TypeVarTuple bound
	// in multiple locations should resolve to "exactly the same type". In an
	// invariant context we honor that strictly and bail out when the tuples differ.
	// In non-invariant contexts, however, requiring an exact match is overly
	// restrictive and rejects valid heterogeneous bindings, so we instead widen
	// element-wise (mirroring how normal TypeVars widen incompatible lower bounds
	// into a union).
	if isInvariant {
		return nil
	}

	maxCount := maxSubtypeCountForTypeVarLowerBound
	tupleTypeArgs := make([]*TupleTypeArg, len(tupleTypeArgs1))
	for index, typeArg1 := range tupleTypeArgs1 {
		typeArg2 := tupleTypeArgs2[index]
		var widenedType Type

		if evaluator.AssignType(typeArg1.Type, typeArg2.Type, nil, nil,
			AssignTypeFlagsDefault, recursionCount) {
			widenedType = typeArg1.Type
		} else if evaluator.AssignType(typeArg2.Type, typeArg1.Type, nil, nil,
			AssignTypeFlagsDefault, recursionCount) {
			widenedType = typeArg2.Type
		} else {
			widenedType = CombineTypes([]Type{typeArg1.Type, typeArg2.Type},
				&CombineTypesOptions{MaxSubtypeCount: &maxCount})
		}

		tupleTypeArgs[index] = &TupleTypeArg{
			Type:        widenedType,
			IsUnbounded: typeArg1.IsUnbounded,
			IsOptional:  typeArg1.IsOptional,
		}
	}

	return SpecializeTupleClass(strippedClass1, tupleTypeArgs, true, true)
}

// stripLiteralValueForUnpackedTuple corresponds to the function of the same
// name.
//
// Its comment: if the provided type is an unpacked tuple, this function strips
// the literals from types of the corresponding elements.
func stripLiteralValueForUnpackedTuple(evaluator TypeEvaluator, t Type) Type {
	if !IsUnpackedClass(t) {
		return t
	}
	classType := t.(*ClassType)
	if classType.Priv.TupleTypeArgs == nil {
		return t
	}

	strippedLiteral := false
	tupleTypeArgs := make([]*TupleTypeArg, len(classType.Priv.TupleTypeArgs))
	for i, arg := range classType.Priv.TupleTypeArgs {
		strippedType := StripTypeForm(evaluator.StripLiteralValue(arg.Type))

		if strippedType != arg.Type {
			strippedLiteral = true
		}

		tupleTypeArgs[i] = &TupleTypeArg{
			IsUnbounded: arg.IsUnbounded,
			IsOptional:  arg.IsOptional,
			Type:        strippedType,
		}
	}

	if !strippedLiteral {
		return t
	}

	return SpecializeTupleClass(classType, tupleTypeArgs, true, true)
}
