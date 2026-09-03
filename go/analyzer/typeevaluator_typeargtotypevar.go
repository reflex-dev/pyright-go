/*
 * typeevaluator_typeargtotypevar.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * applyTypeArgToTypeVar.
 *
 * Checking one supplied type argument against the type parameter it fills. This
 * is where `class Foo[T: int]` rejects `Foo[str]`, and where a constrained
 * TypeVar picks which of its constraints an argument lands in.
 *
 * It returns the type to USE, not merely a verdict, and the two differ for
 * constrained TypeVars: `AnyStr` filled with a `str` literal yields plain `str`,
 * the constraint, rather than the literal. Returning nil is the original's
 * `undefined`, meaning not assignable; the caller substitutes Unknown.
 *
 * A few of the early exits are load bearing rather than optimizations:
 *
 *   - A partially-evaluated class is assumed compatible. During class creation
 *     the bound may not be known yet, and checking it would report an error that
 *     disappears once evaluation finishes.
 *   - A synthesized TypeVar suppresses the bound message. Those TypeVars have
 *     names users never wrote, so naming one in a diagnostic is noise.
 *   - A type alias placeholder is exempt from both the bound and the constraint
 *     checks, since it stands for a type that does not exist yet.
 *
 * Bound and constraints are checked in that order and are not alternatives: a
 * TypeVar has one or the other, never both, and the bound check runs first
 * because ParamSpec and TypeVarTuple can carry a bound but no constraints.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// applyTypeArgToTypeVar corresponds to the function of the same name.
func (e *typeEvaluator) applyTypeArgToTypeVar(
	destType *TypeVarType,
	srcType Type,
	diag *common.DiagnosticAddendum,
) Type {
	if IsAnyOrUnknown(srcType) {
		return srcType
	}

	effectiveSrcType := TransformPossibleRecursiveTypeAlias(srcType, 0)

	if IsTypeVar(srcType) {
		if IsTypeSame(srcType, destType, TypeSameOptions{}, 0) {
			return srcType
		}

		effectiveSrcType = e.MakeTopLevelTypeVarsConcrete(srcType, false)
	}

	// The original's comment: if this is a partially-evaluated class, don't
	// perform any further checks. Assume in this case that the type is compatible
	// with the bound or constraint.
	if IsClass(effectiveSrcType) && ClassTypeIsPartiallyEvaluated(effectiveSrcType.(*ClassType)) {
		return srcType
	}

	// The original's comment: if there's a bound type, make sure the source is
	// derived from it.
	if destType.Shared.BoundType != nil && !IsTypeAliasPlaceholder(effectiveSrcType) {
		if !e.AssignType(destType.Shared.BoundType, effectiveSrcType,
			diag.CreateAddendum(), nil, AssignTypeFlagsDefault, 0) {
			// The original's comment: avoid adding a message that will confuse
			// users if the TypeVar was synthesized for internal purposes.
			if !destType.Shared.IsSynthesized {
				diag.AddMessage(localization.LocAddendum.TypeBound().Format(
					e.PrintType(effectiveSrcType, nil),
					e.PrintType(destType.Shared.BoundType, nil),
					TypeVarTypeGetReadableName(destType, true),
				))
			}
			return nil
		}
	}

	if IsParamSpec(destType) {
		return e.applyTypeArgToParamSpec(destType, srcType, diag)
	}

	if IsParamSpec(srcType) {
		diag.AddMessage(localization.LocMessage.ParamSpecContext())
		return nil
	}

	// The original's comment: if there are no constraints, we're done.
	constraints := destType.Shared.Constraints
	if len(constraints) == 0 {
		return srcType
	}

	if IsTypeAliasPlaceholder(srcType) {
		return srcType
	}

	if matched := e.matchSrcToConstraints(srcType, effectiveSrcType, constraints); matched != nil {
		return matched
	}

	diag.AddMessage(localization.LocAddendum.TypeConstrainedTypeVar().Format(
		e.PrintType(srcType, nil),
		TypeVarTypeGetReadableName(destType, true),
	))

	return nil
}

// applyTypeArgToParamSpec is the isParamSpec(destType) branch: only three things
// can fill a ParamSpec, and none of them go through the constraint machinery.
func (e *typeEvaluator) applyTypeArgToParamSpec(
	destType *TypeVarType, srcType Type, diag *common.DiagnosticAddendum,
) Type {
	if IsParamSpec(srcType) {
		return srcType
	}

	if fn, ok := srcType.(*FunctionType); ok && FunctionTypeIsParamSpecValue(fn) {
		return srcType
	}

	if IsClassInstance(srcType) && ClassTypeIsBuiltInNamed(srcType.(*ClassType), "Concatenate") {
		return srcType
	}

	diag.AddMessage(localization.LocAddendum.TypeParamSpec().Format(
		e.PrintType(srcType, nil),
		TypeVarTypeGetReadableName(destType, true),
	))

	return nil
}

// matchSrcToConstraints is the constrained-TypeVar branch, returning the matched
// constraint or nil if nothing matched.
//
// A constrained TypeVar filled with another constrained TypeVar is checked
// set-wise: every constraint of the source must be covered by some constraint of
// the destination, and the SOURCE is returned unchanged. Anything else picks the
// single narrowest destination constraint that accepts it.
func (e *typeEvaluator) matchSrcToConstraints(
	srcType Type, effectiveSrcType Type, constraints []Type,
) Type {
	if srcTypeVar, ok := srcType.(*TypeVarType); ok && TypeVarTypeHasConstraints(srcTypeVar) {
		for _, sourceConstraint := range srcTypeVar.Shared.Constraints {
			covered := false
			for _, destConstraint := range constraints {
				if e.AssignType(destConstraint, sourceConstraint, nil, nil, AssignTypeFlagsDefault, 0) {
					covered = true
					break
				}
			}
			if !covered {
				return nil
			}
		}
		return srcType
	}

	var bestConstraintSoFar Type

	// The original's comment: try to find the best (narrowest) match among the
	// constraints.
	for _, constraint := range constraints {
		if !e.AssignType(constraint, effectiveSrcType, nil, nil, AssignTypeFlagsDefault, 0) {
			continue
		}

		// The original's comment: don't allow Never to match unless the
		// constraint is also explicitly Never.
		if IsNever(effectiveSrcType) && !IsNever(constraint) {
			continue
		}

		// Narrower wins: the incumbent is replaced only if it accepts the
		// challenger, which makes the challenger the tighter of the two.
		if bestConstraintSoFar == nil ||
			e.AssignType(bestConstraintSoFar, constraint, nil, nil, AssignTypeFlagsDefault, 0) {
			bestConstraintSoFar = constraint
		}
	}

	return bestConstraintSoFar
}
