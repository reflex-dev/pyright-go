/*
 * typeevaluator_assigntypeargs.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * assignTypeArgs.
 *
 * Comparing two specializations of the same generic class, argument by
 * argument, honoring each type parameter's variance. This is the bottom of the
 * chain assignType -> assignToInstantiableClass -> assignClass ->
 * assignClassWithTypeArgs -> here, and it is where `list[int]` is finally
 * decided to be incompatible with `list[str]`.
 *
 * Variance decides both the direction and the strictness of each comparison:
 *
 *   covariant      dest <- src, as-is
 *   contravariant  src <- dest, arguments SWAPPED
 *   invariant      dest <- src, with the Invariant flag
 *
 * The swap is the part that is easy to get wrong, and it is the reason
 * Callable's parameters accept supertypes while its return accepts subtypes.
 *
 * Two behaviours worth naming:
 *
 *   - When the source has more arguments than the destination, the extra ones
 *     are all compared against the destination's LAST argument. The original
 *     notes this handles `Tuple[X, Y, Z]` against `tuple[W]`.
 *   - Protocol variance validation pushes an assumed variance onto a stack, and
 *     while it is active every parameter is treated as invariant regardless of
 *     its declaration.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// assignTypeArgs corresponds to the function of the same name.
func (e *typeEvaluator) assignTypeArgs(
	destType *ClassType,
	srcType *ClassType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	// The original asserts the two are the same generic class.

	e.InferVarianceForClass(destType)

	destTypeParams := ClassTypeGetTypeParams(destType)

	// The original's comment: are we performing protocol variance validation for
	// this class? If so, treat all of the type parameters as invariant even if
	// they are declared otherwise.
	var assumedVariance *Variance
	for _, info := range e.assignClassToSelfStack {
		if ClassTypeIsSameGenericClass(info.ClassType, destType, 0) {
			v := info.AssumedVariance
			assumedVariance = &v
			break
		}
	}

	// The original's comment: if either source or dest type arguments are
	// missing, they are treated as "Any", so they are assumed to be assignable.
	if destType.Priv.TypeArgs == nil || srcType.Priv.TypeArgs == nil {
		return true
	}

	var destTypeArgs, srcTypeArgs []Type
	if ClassTypeIsTupleClass(destType) {
		destTypeArgs = tupleArgTypes(destType.Priv.TupleTypeArgs)
		if srcType.Priv.TupleTypeArgs != nil {
			srcTypeArgs = tupleArgTypes(srcType.Priv.TupleTypeArgs)
		}
	} else {
		destTypeArgs = destType.Priv.TypeArgs
		srcTypeArgs = srcType.Priv.TypeArgs
	}

	isCompatible := true

	for srcArgIndex, srcTypeArg := range srcTypeArgs {
		// The original's comment: in most cases, the number of type args should
		// match the number of type arguments, but there are a few special cases
		// where this isn't true (e.g. assigning a Tuple[X, Y, Z] to a tuple[W]).
		destArgIndex := srcArgIndex
		if srcArgIndex >= len(destTypeArgs) {
			destArgIndex = len(destTypeArgs) - 1
		}

		var destTypeArg Type = UnknownTypeCreate(false)
		if destArgIndex >= 0 {
			destTypeArg = destTypeArgs[destArgIndex]
		}

		var destTypeParam *TypeVarType
		if destArgIndex >= 0 && destArgIndex < len(destTypeParams) {
			destTypeParam = destTypeParams[destArgIndex]
		}

		assignmentDiag := common.NewDiagnosticAddendum()

		variance := VarianceCovariant
		if assumedVariance != nil {
			variance = *assumedVariance
		} else if destTypeParam != nil {
			variance = TypeVarTypeGetVariance(destTypeParam)
		}

		effectiveFlags := flags | AssignTypeFlagsRetainLiteralsForTypeVar
		includeDiagAddendum := true
		var errorSource func(name, sourceType, destType string) string

		switch variance {
		case VarianceCovariant:
			errorSource = func(n, s, d string) string {
				return localization.LocAddendum.TypeVarIsCovariant().Format(n, s, d)
			}
		case VarianceContravariant:
			effectiveFlags |= AssignTypeFlagsContravariant
			errorSource = func(n, s, d string) string {
				return localization.LocAddendum.TypeVarIsContravariant().Format(n, s, d)
			}
		default:
			effectiveFlags |= AssignTypeFlagsInvariant
			errorSource = func(n, s, d string) string {
				return localization.LocAddendum.TypeVarIsInvariant().Format(n, s, d)
			}
			// The original's comment: omit the diagnostic addendum for the
			// invariant case because it's obvious why two types are not the same.
			includeDiagAddendum = false
		}

		// The original's comment: special-case TypeForm to retain literals when
		// solving TypeVars.
		//
		// RetainLiteralsForTypeVar is already set unconditionally above, so this
		// is a no-op in the shipped source; carried because the original states
		// it as a distinct rule.
		if ClassTypeIsBuiltInNamed(destType, "TypeForm") {
			effectiveFlags |= AssignTypeFlagsRetainLiteralsForTypeVar
		}

		// Contravariance swaps the two sides; see the file header.
		assignDest, assignSrc := destTypeArg, srcTypeArg
		if variance == VarianceContravariant {
			assignDest, assignSrc = srcTypeArg, destTypeArg
		}

		if e.AssignType(assignDest, assignSrc, assignmentDiag, constraints, effectiveFlags, recursionCount) {
			continue
		}

		// The original's comment: don't report errors with type variables in
		// "pseudo-random" classes since these type variables are not real.
		if ClassTypeIsPseudoGenericClass(destType) {
			continue
		}

		if diag != nil {
			if destTypeParam != nil {
				childDiag := diag.CreateAddendum()
				types := e.PrintSrcDestTypes(srcTypeArg, destTypeArg)

				childDiag.AddMessage(errorSource(
					TypeVarTypeGetReadableName(destTypeParam, false),
					types.SourceType,
					types.DestType,
				))

				if includeDiagAddendum {
					childDiag.AddAddendum(assignmentDiag)
				}

				// The original's comment: add additional notes to help the user
				// if this is a common type mismatch. Note the isCompatible test
				// happens BEFORE it is set false below, so the suggestion is
				// attached only to the first failing argument.
				if isCompatible && ClassTypeIsSameGenericClass(destType, srcType, 0) {
					switch {
					case ClassTypeIsBuiltInNamed(destType, "dict") && srcArgIndex == 1:
						childDiag.AddMessage(localization.LocAddendum.InvariantSuggestionDict())
					case ClassTypeIsBuiltInNamed(destType, "list"):
						childDiag.AddMessage(localization.LocAddendum.InvariantSuggestionList())
					case ClassTypeIsBuiltInNamed(destType, "set"):
						childDiag.AddMessage(localization.LocAddendum.InvariantSuggestionSet())
					}
				}
			} else {
				diag.AddAddendum(assignmentDiag)
			}
		}

		isCompatible = false
	}

	return isCompatible
}

// tupleArgTypes is the original's `priv.tupleTypeArgs?.map((t) => t.type) ?? []`.
func tupleArgTypes(tupleTypeArgs []*TupleTypeArg) []Type {
	if tupleTypeArgs == nil {
		return []Type{}
	}
	out := make([]Type, 0, len(tupleTypeArgs))
	for _, t := range tupleTypeArgs {
		out = append(out, t.Type)
	}
	return out
}

// AssignTypeArgs is the interface method; the original exposes assignTypeArgs
// on the evaluator interface under the same name.
func (e *typeEvaluator) AssignTypeArgs(
	destType *ClassType,
	srcType *ClassType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	return e.assignTypeArgs(destType, srcType, diag, constraints, flags, recursionCount)
}
