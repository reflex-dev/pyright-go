/*
 * typeevaluator_assignclass.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412): assignClass.
 *
 * Whether one class is assignable to another. This is where assignType's
 * class-to-class routes end, and it decides between three mechanisms:
 *
 *   - structural, for TypedDicts (PEP 589) and protocols (PEP 544), which
 *     compare member by member rather than by inheritance;
 *   - the implicit numeric tower, via the typePromotions table, so int is
 *     assignable to float;
 *   - nominal, via the inheritance chain, which is the ordinary case.
 *
 * The order matters and is preserved. A TypedDict source is rewritten into a
 * Mapping or dict equivalent BEFORE the nominal check, so `TypedDict` assigned
 * to `Mapping[str, int]` succeeds structurally rather than failing nominally.
 * Promotions are checked before the inheritance chain and skipped entirely under
 * invariance.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// assignClass corresponds to the function of the same name.
func (e *typeEvaluator) assignClass(
	destType *ClassType,
	srcType *ClassType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
	reportErrorsUsingObjType bool,
) bool {
	// The original's comment: if the source or dest types are partially
	// evaluated (i.e. they are in the process of being constructed), assume they
	// are assignable rather than risk emitting false positives.
	if ClassTypeIsHierarchyPartiallyEvaluated(destType) || ClassTypeIsHierarchyPartiallyEvaluated(srcType) {
		return true
	}

	// The original's comment: handle typed dicts. They also use a form of
	// structural typing for type checking, as defined in PEP 589.
	if ClassTypeIsTypedDictClass(srcType) {
		if handled, result, rewritten := e.assignTypedDictSrc(destType, srcType, diag, constraints, flags, recursionCount); handled {
			return result
		} else {
			srcType = rewritten
		}
	}

	// The original's comment: handle special-case type promotions.
	if boolValue(destType.Priv.IncludePromotions) {
		if promotionList, ok := typePromotions[destType.Shared.FullName]; ok {
			for _, srcName := range promotionList {
				for _, mroClass := range srcType.Shared.Mro {
					if IsClass(mroClass) && srcName == mroClass.(*ClassType).Shared.FullName {
						if (flags & AssignTypeFlagsInvariant) == 0 {
							return true
						}
					}
				}
			}
		}
	}

	srcType = e.applyDefaultTypeArgsForComparison(srcType)

	// The original's comment: is it a structural type (i.e. a protocol)? If so,
	// we need to perform a member-by-member check.
	inheritanceChain := InheritanceChain{}
	isDerivedFrom := ClassTypeIsDerivedFrom(srcType, destType, &inheritanceChain)

	// The original's comment: use the slow path for protocols if the dest
	// doesn't explicitly derive from the source. We also need to use this path
	// if we're testing to see if the metaclass matches the protocol.
	if ClassTypeIsProtocolClass(destType) && !isDerivedFrom {
		var addendum *common.DiagnosticAddendum
		if diag != nil {
			addendum = diag.CreateAddendum()
		}

		if !e.assignClassToProtocol(destType, ClassTypeCloneAsInstance(srcType, false),
			addendum, constraints, flags, recursionCount) {
			if diag != nil {
				diag.AddMessage(localization.LocAddendum.ProtocolIncompatible().Format(
					e.PrintType(ConvertToInstance(srcType, false), nil),
					e.PrintType(ConvertToInstance(destType, false), nil),
				))
			}
			return false
		}

		return true
	}

	if (flags&AssignTypeFlagsInvariant) == 0 || ClassTypeIsSameGenericClass(srcType, destType, 0) {
		if isDerivedFrom {
			// The original asserts the inheritance chain is non-empty here.
			var addendum *common.DiagnosticAddendum
			if diag != nil {
				addendum = diag.CreateAddendum()
			}

			if e.assignClassWithTypeArgs(destType, srcType, inheritanceChain, addendum, constraints, flags, recursionCount) {
				return true
			}
		}
	}

	// The original's comment: everything is assignable to an object.
	if ClassTypeIsBuiltInNamed(destType, "object") {
		if (flags & AssignTypeFlagsInvariant) == 0 {
			return true
		}
	}

	e.reportClassIncompatible(destType, srcType, diag, reportErrorsUsingObjType)

	return false
}

// assignTypedDictSrc is the original's `if (ClassType.isTypedDictClass(srcType))`
// block. It reports whether it produced an answer; when it did not, the third
// result is the (possibly rewritten) source type to continue with.
func (e *typeEvaluator) assignTypedDictSrc(
	destType *ClassType,
	srcType *ClassType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) (bool, bool, *ClassType) {
	if ClassTypeIsTypedDictClass(destType) && !ClassTypeIsSameGenericClass(destType, srcType, 0) {
		if !e.assignTypedDictToTypedDict(destType, srcType, diag, constraints, flags, recursionCount) {
			return true, false, srcType
		}

		// The original's comment: if invariance is being enforced, the two
		// TypedDicts must be assignable to each other.
		if (flags & AssignTypeFlagsInvariant) != 0 {
			return true, e.assignTypedDictToTypedDict(srcType, destType, nil, nil, flags, recursionCount), srcType
		}

		return true, true, srcType
	}

	// The original's comment: handle some special cases where a TypedDict can
	// act like a Mapping[str, T] or a dict[str, T].
	if ClassTypeIsBuiltInNamed(destType, "Mapping") {
		mappingValueType := e.getTypedDictMappingEquivalent(srcType)

		if mappingValueType != nil && e.prefetched != nil &&
			e.prefetched.MappingClass != nil && IsInstantiableClass(e.prefetched.MappingClass) &&
			e.prefetched.StrClass != nil && IsInstantiableClass(e.prefetched.StrClass) {
			srcType = ClassTypeSpecialize(
				e.prefetched.MappingClass.(*ClassType),
				[]Type{ClassTypeCloneAsInstance(e.prefetched.StrClass.(*ClassType), false), mappingValueType},
				nil, false, nil, nil,
			)
		}
	} else if ClassTypeIsBuiltInNamed(destType, "dict", "MutableMapping") {
		dictValueType := e.getTypedDictDictEquivalent(srcType, recursionCount)

		if dictValueType != nil && e.prefetched != nil &&
			e.prefetched.DictClass != nil && IsInstantiableClass(e.prefetched.DictClass) &&
			e.prefetched.StrClass != nil && IsInstantiableClass(e.prefetched.StrClass) {
			srcType = ClassTypeSpecialize(
				e.prefetched.DictClass.(*ClassType),
				[]Type{ClassTypeCloneAsInstance(e.prefetched.StrClass.(*ClassType), false), dictValueType},
				nil, false, nil, nil,
			)
		}
	}

	return false, false, srcType
}

// applyDefaultTypeArgsForComparison is the original's block whose comment reads:
// a class value normally remains unspecialized so it can be subscripted. If
// every type parameter has an explicit default, apply those defaults when
// comparing it against a type specialization. Classes with defaultless
// parameters or defaults that resolve directly to Any or Unknown retain their
// unspecialized behavior, so they don't degrade inference.
func (e *typeEvaluator) applyDefaultTypeArgsForComparison(srcType *ClassType) *ClassType {
	props := srcType.Base().Props
	if !srcType.Base().IsInstantiable() || props == nil || props.TypeForm == nil ||
		srcType.Priv.TypeArgs != nil || srcType.Priv.IncludeSubclasses ||
		len(srcType.Shared.TypeParams) == 0 {
		return srcType
	}

	for _, typeParam := range srcType.Shared.TypeParams {
		if !typeParam.Shared.IsDefaultExplicit {
			return srcType
		}
	}

	specializedSrcType := SpecializeWithDefaultTypeArgs(srcType)

	// `priv.typeArgs ?? priv.tupleTypeArgs?.map(...)`
	specializedTypeArgs := specializedSrcType.Priv.TypeArgs
	if specializedTypeArgs == nil && specializedSrcType.Priv.TupleTypeArgs != nil {
		specializedTypeArgs = make([]Type, 0, len(specializedSrcType.Priv.TupleTypeArgs))
		for _, tupleTypeArg := range specializedSrcType.Priv.TupleTypeArgs {
			specializedTypeArgs = append(specializedTypeArgs, tupleTypeArg.Type)
		}
	}

	// The original computes `hasGradualTypeArg` as `some(...)`, which is
	// `undefined` when specializedTypeArgs is undefined, and then tests
	// `=== false`. So an absent argument list does NOT apply the defaults --
	// only a present list with no gradual member does.
	if specializedTypeArgs == nil {
		return srcType
	}

	for _, typeArg := range specializedTypeArgs {
		if ContainsAnyOrUnknown(typeArg, false) != nil {
			return srcType
		}
		if IsFunction(typeArg) && FunctionTypeIsGradualCallableForm(typeArg.(*FunctionType)) {
			return srcType
		}
		if IsUnpackedClass(typeArg) && ContainsAnyOrUnknown(typeArg, true) != nil {
			return srcType
		}
	}

	return specializedSrcType
}

// reportClassIncompatible is the original's final `if (diag)` block.
func (e *typeEvaluator) reportClassIncompatible(
	destType *ClassType,
	srcType *ClassType,
	diag *common.DiagnosticAddendum,
	reportErrorsUsingObjType bool,
) {
	if diag == nil {
		return
	}

	var destErrorType, srcErrorType Type = destType, srcType
	if reportErrorsUsingObjType {
		destErrorType = ClassTypeCloneAsInstance(destType, false)
		srcErrorType = ClassTypeCloneAsInstance(srcType, false)
	}

	destErrorTypeText := e.PrintType(destErrorType, nil)
	srcErrorTypeText := e.PrintType(srcErrorType, nil)

	// The original's comment: if the text is the same, use the fully-qualified
	// name rather than the short name.
	if destErrorTypeText == srcErrorTypeText && destType.Shared.FullName != "" && srcType.Shared.FullName != "" {
		destErrorTypeText = destType.Shared.FullName
		srcErrorTypeText = srcType.Shared.FullName
	}

	diag.AddMessage(localization.LocAddendum.TypeIncompatible().Format(srcErrorTypeText, destErrorTypeText))

	// The original's comment: tell the user about the disableBytesTypePromotions
	// if that is involved.
	if ClassTypeIsBuiltInNamed(destType, "bytes") {
		if promotions, ok := typePromotions[destType.Shared.FullName]; ok {
			for _, name := range promotions {
				if name == srcType.Shared.FullName {
					diag.AddMessage(localization.LocAddendum.BytesTypePromotions())
					break
				}
			}
		}
	}
}

/*
 * The four things assignClass reaches that are separate units of work.
 */

// assignTypedDictToTypedDict corresponds to the typedDicts.ts function of the
// same name.
func (e *typeEvaluator) assignTypedDictToTypedDict(
	_ *ClassType, _ *ClassType, _ *common.DiagnosticAddendum,
	_ *ConstraintTracker, _ AssignTypeFlags, _ int,
) bool {
	e.unported("typedDicts.assignTypedDictToTypedDict")
	return false
}

// getTypedDictMappingEquivalent corresponds to the typedDicts.ts function of the
// same name. It returns nil where the original returns undefined, meaning the
// TypedDict cannot act as a Mapping.
func (e *typeEvaluator) getTypedDictMappingEquivalent(_ *ClassType) Type {
	e.unported("typedDicts.getTypedDictMappingEquivalent")
	return nil
}

// getTypedDictDictEquivalent corresponds to the typedDicts.ts function of the
// same name.
func (e *typeEvaluator) getTypedDictDictEquivalent(_ *ClassType, _ int) Type {
	e.unported("typedDicts.getTypedDictDictEquivalent")
	return nil
}
