/*
 * typeevaluator_assignclassargs.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * assignClassWithTypeArgs.
 *
 * The nominal half of class assignability. assignClass has established that the
 * source derives from the destination and produced the inheritance chain; this
 * walks that chain from the ancestor down, specializing the source for each base
 * class in turn, and then compares the type arguments that land at the bottom.
 *
 * The walk is where the specialization happens and it is easy to mistake for
 * bookkeeping. `class Foo(dict[str, int])` assigned to `Mapping[str, int]` needs
 * Foo re-expressed as dict[str, int], then as Mapping[str, int], before the
 * arguments can be compared at all -- specializeForBaseClass at each step is
 * what carries the arguments through.
 *
 * Three details are load bearing:
 *
 *   - An Unknown anywhere in the chain means "assume assignable", with None the
 *     single exception. The original explains why it cannot use @final for this:
 *     it breaks assumptions about typeshed's NotImplemented.
 *   - A NamedTuple whose ancestor is `tuple` specializes from the PREVIOUS
 *     source rather than the current one, because a NamedTuple carries type
 *     parameters from its parent.
 *   - When the destination is unspecialized but the source is not, the source's
 *     arguments are recorded as constraint bounds -- upper, lower, or both,
 *     depending on the parameter's variance.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
)

// assignClassWithTypeArgs corresponds to the function of the same name.
func (e *typeEvaluator) assignClassWithTypeArgs(
	destType *ClassType,
	srcType *ClassType,
	inheritanceChain InheritanceChain,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	curSrcType := srcType
	var prevSrcType *ClassType

	e.InferVarianceForClass(destType)

	// The original's comment: if we're enforcing invariance, literal types must
	// match.
	if (flags & AssignTypeFlagsInvariant) != 0 {
		if IsLiteralLikeType(srcType) != IsLiteralLikeType(destType) {
			return false
		}
	}

	for ancestorIndex := len(inheritanceChain) - 1; ancestorIndex >= 0; ancestorIndex-- {
		ancestorType := inheritanceChain[ancestorIndex]

		// The original's comment: if we've hit an "unknown", all bets are off,
		// and we need to assume that the type is assignable. If the destType is
		// marked "@final", we should be able to assume that it's not assignable,
		// but we can't do this in the general case because it breaks assumptions
		// with the NotImplemented symbol exported by typeshed's builtins.pyi.
		// Instead, we'll special-case only None.
		if IsUnknown(ancestorType) {
			return !IsNoneTypeClass(destType)
		}

		ancestorClass, ok := ancestorType.(*ClassType)
		if !ok {
			continue
		}

		// The original's comment: if this isn't the first time through the loop,
		// specialize for the next ancestor in the chain.
		if ancestorIndex < len(inheritanceChain)-1 {
			// The original's comment: if the curSrcType is a NamedTuple and the
			// ancestorType is a tuple, we need to handle this as a special case
			// because the NamedTuple may include typeParams from its parent
			// class.
			effectiveCurSrcType := curSrcType
			if ClassTypeIsBuiltInNamed(curSrcType, "NamedTuple") &&
				ClassTypeIsBuiltInNamed(ancestorClass, "tuple") &&
				prevSrcType != nil {
				effectiveCurSrcType = prevSrcType
			}

			curSrcType = SpecializeForBaseClass(effectiveCurSrcType, ancestorClass)
		}

		// The original's comment: if there are no type parameters on this class,
		// we're done.
		if len(ClassTypeGetTypeParams(ancestorClass)) == 0 {
			continue
		}

		// The original's comment: if the dest type isn't specialized, there are
		// no type args to validate.
		if ancestorClass.Priv.TypeArgs == nil {
			return true
		}

		prevSrcType = curSrcType
	}

	// The original's comment: handle tuple, which supports a variable number of
	// type arguments.
	if destType.Priv.TupleTypeArgs != nil && curSrcType.Priv.TupleTypeArgs != nil {
		return e.assignTupleTypeArgs(destType, curSrcType, diag, constraints, flags, recursionCount)
	}

	if destType.Priv.TypeArgs != nil {
		// The original's comment: if the dest type is specialized, make sure the
		// specialized source type arguments are assignable to the dest type
		// arguments. Don't emit a diag addendum if we're in an invariant
		// context: it's sufficient to simply indicate that the types are not the
		// same in this case, and adding more information is unnecessary and
		// confusing.
		var effectiveDiag *common.DiagnosticAddendum
		if (flags & AssignTypeFlagsInvariant) == 0 {
			effectiveDiag = diag
		}

		return e.assignTypeArgs(destType, curSrcType, effectiveDiag, constraints, flags, recursionCount)
	}

	if constraints != nil && curSrcType.Priv.TypeArgs != nil {
		// The original's comment: populate the typeVar map with type arguments
		// of the source.
		srcTypeArgs := curSrcType.Priv.TypeArgs

		for i, typeParam := range destType.Shared.TypeParams {
			var typeArgType Type
			variance := TypeVarTypeGetVariance(typeParam)

			if curSrcType.Priv.TupleTypeArgs != nil {
				typeArgType = ConvertToInstance(
					MakeTupleObject(e, curSrcType.Priv.TupleTypeArgs, true), false)
			} else if i < len(srcTypeArgs) {
				typeArgType = srcTypeArgs[i]
			} else {
				typeArgType = UnknownTypeCreate(false)
			}

			// The bound the argument becomes depends on variance: a covariant
			// parameter records only a lower bound, a contravariant one only an
			// upper bound, and an invariant one both.
			var lowerBound, upperBound Type
			if variance != VarianceContravariant {
				lowerBound = typeArgType
			}
			if variance != VarianceCovariant {
				upperBound = typeArgType
			}

			constraints.SetBounds(typeParam, lowerBound, upperBound, true)
		}
	}

	return true
}

/*
 * The two type-argument comparisons this reaches.
 */

// assignTupleTypeArgs reaches the tuples.ts function of the same name, which
// handles the variable-length matching a tuple requires.
func (e *typeEvaluator) assignTupleTypeArgs(
	destType *ClassType, srcType *ClassType, diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker, flags AssignTypeFlags, recursionCount int,
) bool {
	return AssignTupleTypeArgs(e, destType, srcType, diag, constraints, flags, recursionCount)
}
