/*
 * typeevaluator_assigntype.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412): assignType.
 *
 * The assignability relation -- "can a value of srcType be used where destType
 * is expected" -- and the function every diagnostic about a type mismatch
 * ultimately comes from. ~855 lines of the original, and the last of the four
 * structural pillars of the evaluator (the others being class creation, function
 * creation and call evaluation).
 *
 * It is a long cascade rather than a dispatch, and the order is the semantics:
 * each case is tried, and falling off the end means "not assignable". The shape
 * is preserved exactly, with the cases lifted into helpers only where a case is
 * self-contained.
 *
 * Two things about the cascade that a reader should not have to rediscover:
 *
 *   - The `isTypeVar(destType)` block does NOT always return. It returns only
 *     when the contravariant flag is clear or the source is not a TypeVar;
 *     otherwise it falls through to the `isTypeVar(srcType)` block below. Both
 *     blocks running for one call is intentional.
 *   - recursionCount is incremented once near the top, after several early
 *     returns that deliberately do not count against the limit.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// AssignType corresponds to assignType.
func (e *typeEvaluator) AssignType(
	destType Type,
	srcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	// The original's comment: handle the case where the dest and src types are
	// the same object. We can normally shortcut this and say that they are
	// compatible, but if the type includes TypeVars, we need to go through the
	// rest of the logic.
	if destType == srcType && !RequiresSpecialization(destType, nil, 0) {
		return true
	}

	srcType = e.unwrapSpecialFormForAssign(srcType, flags)

	if e.isGenericAliasAssignExempt(destType, srcType) {
		return true
	}

	// The original's comment: if the source is a class-like type created by a
	// call to NewType, treat it as a FunctionClass instance rather than an
	// instantiable class for purposes of assignability. This reflects its actual
	// runtime type.
	if IsInstantiableClass(srcType) && ClassTypeIsNewTypeClass(srcType.(*ClassType)) &&
		!srcType.(*ClassType).Priv.IncludeSubclasses {
		if e.prefetched != nil && e.prefetched.FunctionClass != nil && IsInstantiableClass(e.prefetched.FunctionClass) {
			srcType = ClassTypeCloneAsInstance(e.prefetched.FunctionClass.(*ClassType), false)
		}
	}

	if recursionCount > MaxTypeRecursionCount {
		return true
	}
	recursionCount++

	if handled, result := e.assignRecursiveAliases(destType, srcType, diag, constraints, &flags, recursionCount); handled {
		return result
	}

	// The original's comment: if one or both of the types has an instantiable
	// depth greater than zero, convert both to instances first.
	if destType.Base().IsInstantiable() && srcType.Base().IsInstantiable() {
		if destType.Base().GetInstantiableDepth() > 0 || srcType.Base().GetInstantiableDepth() > 0 {
			return e.AssignType(
				ConvertToInstance(destType, true),
				ConvertToInstance(srcType, true),
				diag, constraints, flags, recursionCount,
			)
		}
	}

	// Transform recursive type aliases if necessary.
	transformedDestType := TransformPossibleRecursiveTypeAlias(destType, 0)
	transformedSrcType := TransformPossibleRecursiveTypeAlias(srcType, 0)

	// The original's comment: did either the source or dest include recursive
	// type aliases? If so, we could be dealing with different recursive type
	// aliases or a recursive type alias and a recursive protocol definition.
	if (transformedDestType != destType && IsUnion(transformedDestType)) ||
		(transformedSrcType != srcType && IsUnion(transformedSrcType)) {
		// The original's comment: use a smaller recursive limit in this case to
		// prevent runaway recursion.
		if recursionCount > maxRecursiveTypeAliasRecursionCount {
			// The original's comment: add a special case for when the source is
			// a str, which is itself a recursive type (since it derives from
			// Sequence[str]).
			if IsClassInstance(srcType) && ClassTypeIsBuiltInNamed(srcType.(*ClassType), "str") &&
				IsUnion(transformedDestType) {
				for _, subtype := range unionableToTypes(transformedDestType.(*UnionType).Priv.Subtypes) {
					if IsClassInstance(subtype) && ClassTypeIsBuiltInNamed(subtype.(*ClassType), "object", "str") {
						return true
					}
				}
				return false
			}
			return true
		}
	}

	destType = transformedDestType
	srcType = transformedSrcType

	// The original's comment: if the source or dest is unbound, allow the
	// assignment. The error will be reported elsewhere.
	if IsUnbound(destType) || IsUnbound(srcType) {
		return true
	}

	if IsTypeVar(destType) {
		// This block does NOT always return; see the file header.
		if handled, result := e.assignToTypeVarDest(destType.(*TypeVarType), srcType, diag, constraints, flags, recursionCount); handled {
			return result
		}
	}

	if IsTypeVar(srcType) {
		if handled, result := e.assignFromTypeVarSrc(destType, srcType.(*TypeVarType), diag, constraints, flags, recursionCount); handled {
			return result
		}
	}

	if IsAnyOrUnknown(destType) {
		return true
	}

	if props := srcType.Base().Props; IsAnyOrUnknown(srcType) && (props == nil || props.SpecialForm == nil) {
		if constraints != nil {
			// The original's comment: if it's an ellipsis type, convert it to a
			// regular "Any" type. These are functionally equivalent, but "Any"
			// looks better in the text representation.
			var typeVarSubstitution Type = srcType
			if IsEllipsisType(srcType) {
				typeVarSubstitution = AnyTypeCreate(false)
			}
			e.setConstraintsForFreeTypeVars(destType, typeVarSubstitution, constraints)
		}
		if (flags & AssignTypeFlagsOverloadOverlap) == 0 {
			return true
		}
	}

	if IsNever(srcType) {
		if (flags & AssignTypeFlagsInvariant) != 0 {
			if IsNever(destType) {
				return true
			}

			e.addTypeMismatch(diag, srcType, destType)
			return false
		}

		if constraints != nil {
			e.setConstraintsForFreeTypeVars(destType, UnknownTypeCreate(false), constraints)
		}
		return true
	}

	isInvariant := (flags & AssignTypeFlagsInvariant) != 0
	srcType = e.expandEnumTypeForLiteralComparison(srcType, destType, !isInvariant)
	if isInvariant {
		destType = e.expandEnumTypeForLiteralComparison(destType, srcType, false)
	}

	if IsUnion(destType) {
		// The original's comment: if both the source and dest are unions, use
		// assignFromUnionType which has special-case logic to handle this case.
		if IsUnion(srcType) {
			return e.assignFromUnionType(destType, srcType.(*UnionType), diag, constraints, flags, recursionCount)
		}

		var clonedConstraints *ConstraintTracker
		if constraints != nil {
			clonedConstraints = constraints.Clone()
		}
		if e.assignToUnionType(destType.(*UnionType), srcType, nil, clonedConstraints, flags, recursionCount) {
			if constraints != nil && clonedConstraints != nil {
				constraints.CopyFromClone(clonedConstraints)
			}
			return true
		}
	}

	expandedSrcType := e.makeTopLevelTypeVarsConcrete(srcType, false, nil)
	if IsUnion(expandedSrcType) {
		return e.assignFromUnionType(destType, expandedSrcType.(*UnionType), diag, constraints, flags, recursionCount)
	}

	if IsUnion(destType) {
		return e.assignToUnionType(destType.(*UnionType), srcType, diag, constraints, flags, recursionCount)
	}

	if handled, result := e.assignFromSpecializedTypeObject(destType, srcType, expandedSrcType, diag, constraints, flags, recursionCount); handled {
		return result
	}

	if IsInstantiableClass(destType) && IsInstantiableClass(expandedSrcType) {
		return e.assignToInstantiableClass(
			destType.(*ClassType), srcType, expandedSrcType.(*ClassType), diag, constraints, flags, recursionCount,
		)
	}

	if IsClassInstance(destType) {
		if handled, result := e.assignToClassInstance(destType.(*ClassType), srcType, diag, constraints, flags, recursionCount); handled {
			return result
		}
	}

	if IsFunction(destType) {
		if handled, result := e.assignToFunction(destType.(*FunctionType), srcType, diag, constraints, flags, recursionCount); handled {
			return result
		}
	}

	if IsOverloaded(destType) {
		return e.assignToOverloaded(destType.(*OverloadedType), srcType, diag, constraints, flags, recursionCount)
	}

	if IsClass(destType) && ClassTypeIsBuiltInNamed(destType.(*ClassType), "object") {
		if (IsInstantiableClass(destType) && srcType.Base().IsInstantiable()) || IsClassInstance(destType) {
			if (flags & AssignTypeFlagsInvariant) == 0 {
				// The original's comment: all types (including None, Module,
				// Overloaded) derive from object.
				return true
			}
		}
	}

	// The original's comment: are we trying to assign None to a protocol?
	if IsNoneInstance(srcType) && IsClassInstance(destType) && ClassTypeIsProtocolClass(destType.(*ClassType)) {
		if e.prefetched != nil && e.prefetched.NoneTypeClass != nil && IsInstantiableClass(e.prefetched.NoneTypeClass) {
			return e.assignClassToProtocol(
				ClassTypeCloneAsInstantiable(destType.(*ClassType), false),
				ClassTypeCloneAsInstance(e.prefetched.NoneTypeClass.(*ClassType), false),
				diag, constraints, flags, recursionCount,
			)
		}
	}

	if IsNoneInstance(destType) {
		if diag != nil {
			diag.AddMessage(localization.LocAddendum.AssignToNone())
		}
		return false
	}

	e.addTypeMismatch(diag, srcType, destType)

	return false
}

// unwrapSpecialFormForAssign is the original's `const specialForm = srcType.props?.specialForm`
// block: a special form is compared as its literal class rather than its
// symbolic form, except for three that isinstance and issubclass exempt.
func (e *typeEvaluator) unwrapSpecialFormForAssign(srcType Type, flags AssignTypeFlags) Type {
	props := srcType.Base().Props
	if props == nil || props.SpecialForm == nil {
		return srcType
	}
	specialForm := props.SpecialForm

	// The original's comment: a few special forms that are normally not
	// compatible with type[T] are compatible specifically in the context of
	// isinstance and issubclass.
	if (flags & AssignTypeFlagsAllowIsinstanceSpecialForms) != 0 {
		if ClassTypeIsBuiltInNamed(specialForm, "Callable", "UnionType", "Generic") {
			return srcType
		}
	}

	if props.TypeForm != nil {
		if specialFormProps := specialForm.Base().Props; specialFormProps == nil || specialFormProps.TypeForm == nil {
			return CloneWithTypeForm(Type(specialForm), props.TypeForm)
		}
	}

	return specialForm
}

// isGenericAliasAssignExempt is the original's GenericAlias special case. Its
// comment: a subscripted runtime built-in such as list[int] is represented as
// its precise class type, but it is also a types.GenericAlias object at runtime.
func (e *typeEvaluator) isGenericAliasAssignExempt(destType Type, srcType Type) bool {
	if !IsClassInstance(destType) || !ClassTypeIsBuiltInNamed(destType.(*ClassType), "GenericAlias") {
		return false
	}

	if !IsInstantiableClass(srcType) {
		return false
	}

	srcClass := srcType.(*ClassType)
	srcProps := srcType.Base().Props

	return !ClassTypeIsSpecialBuiltIn(srcClass) &&
		srcClass.Priv.AliasName == nil &&
		srcClass.Shared.ModuleName == "builtins" &&
		srcClass.Priv.TypeArgs != nil &&
		srcProps != nil && srcProps.TypeForm != nil &&
		e.classGetItemReturnsGenericAlias(srcClass)
}

// assignRecursiveAliases is the original's block for when both sides are
// recursive type aliases. It may set the SkipRecursiveTypeCheck flag on the
// caller's flags rather than returning, which is why flags is a pointer.
func (e *typeEvaluator) assignRecursiveAliases(
	destType Type,
	srcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags *AssignTypeFlags,
	recursionCount int,
) (bool, bool) {
	if !IsTypeVar(destType) || destType.(*TypeVarType).Shared.RecursiveAlias == nil ||
		!IsTypeVar(srcType) || srcType.(*TypeVarType).Shared.RecursiveAlias == nil {
		return false, false
	}

	var destAliasInfo, srcAliasInfo *TypeAliasInfo
	if props := destType.Base().Props; props != nil {
		destAliasInfo = props.TypeAliasInfo
	}
	if props := srcType.Base().Props; props != nil {
		srcAliasInfo = props.TypeAliasInfo
	}

	// The original's comment: do the source and dest refer to the same recursive
	// type alias?
	if destAliasInfo != nil && destAliasInfo.TypeArgs != nil &&
		srcAliasInfo != nil && srcAliasInfo.TypeArgs != nil &&
		destType.(*TypeVarType).Shared.RecursiveAlias.TypeVarScopeId ==
			srcType.(*TypeVarType).Shared.RecursiveAlias.TypeVarScopeId {
		return true, e.assignRecursiveTypeAliasToSelf(destAliasInfo, srcAliasInfo, diag, constraints, *flags, recursionCount)
	}

	// The original's comment: have we already recursed once?
	if (*flags & AssignTypeFlagsSkipRecursiveTypeCheck) != 0 {
		return true, true
	}

	// The original's comment: note that we are comparing two recursive types and
	// do not recurse more than once.
	*flags |= AssignTypeFlagsSkipRecursiveTypeCheck
	return false, false
}

// assignToTypeVarDest is the original's `if (isTypeVar(destType))` block. It
// reports whether it produced an answer; falling through is deliberate.
func (e *typeEvaluator) assignToTypeVarDest(
	destType *TypeVarType,
	srcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) (bool, bool) {
	if e.isTypeVarSame(destType, srcType) {
		return true, true
	}

	// The original's comment: if the dest is a constrained or bound type
	// variable and all of the types in the source are conditioned on that same
	// type variable and have compatible types, we'll consider it assignable.
	if e.assignConditionalTypeToTypeVar(destType, srcType, recursionCount) {
		return true, true
	}

	// The original's comment: if the source is a conditional type associated
	// with a bound TypeVar and the bound TypeVar matches the condition, the
	// types are compatible.
	if destType.Base().IsInstantiable() == srcType.Base().IsInstantiable() {
		if props := srcType.Base().Props; props != nil {
			for _, cond := range props.Condition {
				if !TypeVarTypeHasConstraints(cond.TypeVar) &&
					cond.TypeVar.Priv.NameWithScope == destType.Priv.NameWithScope {
					return true, true
				}
			}
		}
	}

	if IsUnion(srcType) {
		srcWithoutAny := RemoveFromUnion(srcType, func(t Type) bool { return IsAnyOrUnknown(t) })
		if IsTypeSame(destType, srcWithoutAny, TypeSameOptions{}, 0) {
			return true, true
		}
	}

	// The original's comment: handle the special case where both types are Self
	// types. We'll allow them to be treated as equivalent to handle certain
	// common idioms.
	if IsTypeVar(srcType) {
		srcTypeVar := srcType.(*TypeVarType)
		if TypeVarTypeIsSelf(srcTypeVar) && TypeVarTypeHasBound(srcTypeVar) &&
			TypeVarTypeIsSelf(destType) && TypeVarTypeHasBound(destType) &&
			TypeVarTypeIsBound(destType) == TypeVarTypeIsBound(srcTypeVar) &&
			srcType.Base().IsInstance() == destType.Base().IsInstance() {
			if (flags&AssignTypeFlagsContravariant) == 0 && constraints != nil {
				AssignTypeVar(e, destType, srcType, diag, constraints, flags, recursionCount)
			}
			return true, true
		}
	}

	// The original's comment: if the dest is a TypeVarTuple, and the source is a
	// tuple with a single entry that is the same TypeVarTuple, it's a match.
	if IsTypeVarTuple(destType) && IsClassInstance(srcType) && IsTupleClass(srcType.(*ClassType)) {
		tupleTypeArgs := srcType.(*ClassType).Priv.TupleTypeArgs
		if len(tupleTypeArgs) == 1 {
			if IsTypeSame(destType, tupleTypeArgs[0].Type, TypeSameOptions{}, recursionCount) {
				return true, true
			}
		}
	}

	if (flags&AssignTypeFlagsContravariant) == 0 || !IsTypeVar(srcType) {
		if !AssignTypeVar(e, destType, srcType, diag, constraints, flags, recursionCount) {
			return true, false
		}

		if IsAnyOrUnknown(srcType) && (flags&AssignTypeFlagsOverloadOverlap) != 0 {
			return true, false
		}

		return true, true
	}

	// Falls through to the isTypeVar(srcType) block.
	return false, false
}

// assignFromTypeVarSrc is the original's `if (isTypeVar(srcType))` block.
func (e *typeEvaluator) assignFromTypeVarSrc(
	destType Type,
	srcType *TypeVarType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) (bool, bool) {
	if (flags & AssignTypeFlagsContravariant) != 0 {
		if TypeVarTypeIsBound(srcType) {
			return true, e.AssignType(
				e.makeTopLevelTypeVarsConcrete(destType, false, nil),
				e.makeTopLevelTypeVarsConcrete(srcType, false, nil),
				diag, nil, flags, recursionCount,
			)
		}

		if AssignTypeVar(e, srcType, destType, diag, constraints, flags, recursionCount) {
			return true, true
		}

		// The original's comment: if the dest type is a union, only one of the
		// subtypes needs to match.
		isAssignable := false
		if IsUnion(destType) {
			DoForEachSubtype(destType, func(destSubtype Type, _ int, _ []Type) {
				if AssignTypeVar(e, srcType, destSubtype, diag, constraints, flags, recursionCount) {
					isAssignable = true
				}
			})
		}
		return true, isAssignable
	}

	if (flags & AssignTypeFlagsInvariant) != 0 {
		if IsAnyOrUnknown(destType) {
			return true, true
		}

		// The original's comment: if the source is a ParamSpec and the dest is a
		// "...", this is effectively like an "Any" signature, so we'll treat it
		// as though it's Any.
		if IsParamSpec(srcType) && IsFunction(destType) &&
			FunctionTypeIsGradualCallableForm(destType.(*FunctionType)) &&
			len(destType.(*FunctionType).Shared.Parameters) <= 2 {
			return true, true
		}

		// The original's comment: if the source is an unpacked TypeVarTuple and
		// the dest is a *tuple[Any, ...], we'll treat it as compatible.
		if IsUnpackedTypeVarTuple(srcType) && IsClassInstance(destType) && IsUnpackedClass(destType) {
			tupleTypeArgs := destType.(*ClassType).Priv.TupleTypeArgs
			if len(tupleTypeArgs) == 1 && tupleTypeArgs[0].IsUnbounded && IsAnyOrUnknown(tupleTypeArgs[0].Type) {
				return true, true
			}
		}

		if !IsUnion(destType) {
			e.addTypeMismatch(diag, srcType, destType)
			return true, false
		}
	}

	return false, false
}

// assignFromSpecializedTypeObject is the original's "is the src a specialized
// `type` object?" block.
func (e *typeEvaluator) assignFromSpecializedTypeObject(
	destType Type,
	srcType Type,
	expandedSrcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) (bool, bool) {
	if !IsClassInstance(expandedSrcType) || !ClassTypeIsBuiltInNamed(expandedSrcType.(*ClassType), "type") {
		return false, false
	}

	srcTypeArgs := expandedSrcType.(*ClassType).Priv.TypeArgs
	var typeTypeArg Type = UnknownTypeCreate(false)
	if len(srcTypeArgs) >= 1 {
		typeTypeArg = srcTypeArgs[0]
	}

	if IsAnyOrUnknown(typeTypeArg) {
		if IsEffectivelyInstantiable(destType, nil, 0) {
			return true, true
		}
		return false, false
	}

	if IsClassInstance(typeTypeArg) || IsTypeVar(typeTypeArg) {
		var addendum *common.DiagnosticAddendum
		if diag != nil {
			addendum = diag.CreateAddendum()
		}
		if e.AssignType(destType, ConvertToInstantiable(typeTypeArg, true), addendum, constraints, flags, recursionCount) {
			return true, true
		}

		e.addTypeMismatch(diag, srcType, destType)
		return true, false
	}

	return false, false
}

// addTypeMismatch is the original's repeated
// `diag?.addMessage(LocAddendum.typeAssignmentMismatch().format(printSrcDestTypes(srcType, destType)))`.
func (e *typeEvaluator) addTypeMismatch(diag *common.DiagnosticAddendum, srcType Type, destType Type) {
	if diag == nil {
		return
	}
	types := e.PrintSrcDestTypes(srcType, destType)
	diag.AddMessage(localization.LocAddendum.TypeAssignmentMismatch().Format(types.SourceType, types.DestType))
}

/*
 * The dispatch targets. Each is a separate unit of work and records itself, so
 * the frontier ranks the assignability cases.
 */

// setConstraintsForFreeTypeVars corresponds to the function of the same name.
//
// Its comment: finds unsolved type variables in the destType and establishes
// constraints in the constraint tracker for them based on the srcType.
func (e *typeEvaluator) setConstraintsForFreeTypeVars(
	destType Type, srcType Type, constraints *ConstraintTracker,
) {
	for _, typeVar := range GetTypeVarArgsRecursive(destType, 0) {
		if TypeVarTypeIsBound(typeVar) ||
			constraints.GetMainConstraintSet().GetTypeVar(typeVar) != nil {
			continue
		}

		// The original's comment: don't set ParamSpecs or TypeVarTuples.
		if !IsParamSpec(srcType) && !IsTypeVarTuple(srcType) {
			constraints.SetBounds(typeVar, srcType, nil, false)
		}
	}
}

// getCallbackProtocolType corresponds to the function of the same name.
//
// Its comment at the call site: if the class is a protocol and it has a
// `__call__` method but no other methods or attributes that would be
// incompatible with a function, this method returns the signature of the call
// implied by the `__call__` method. Otherwise it returns undefined.
//
// "Incompatible" is decided by asking whether `types.FunctionType` itself
// declares the same name, so a protocol may carry `__name__` or `__doc__` and
// still be callable, but any member a function does not have disqualifies it.
func (e *typeEvaluator) getCallbackProtocolType(objType *ClassType, recursionCount int) Type {
	if !IsClassInstance(objType) || !ClassTypeIsProtocolClass(objType) {
		return nil
	}

	// The original's comment: make sure that the protocol class doesn't define
	// any fields that a normal function wouldn't be compatible with.
	if !e.protocolIsFunctionShaped(objType) {
		return nil
	}

	callType := e.GetBoundMagicMethod(objType, "__call__", nil, nil, nil, recursionCount)
	if callType == nil {
		return nil
	}

	return MakeFunctionTypeVarsBound(callType)
}

// protocolIsFunctionShaped is the original's MRO walk over the protocol's
// members.
func (e *typeEvaluator) protocolIsFunctionShaped(objType *ClassType) bool {
	isFunctionShaped := true

	for _, mroClass := range objType.Shared.Mro {
		mroClassType, ok := mroClass.(*ClassType)
		if !ok || !IsClass(mroClass) || !ClassTypeIsProtocolClass(mroClassType) {
			continue
		}

		ClassTypeGetSymbolTable(mroClassType).ForEach(func(fieldSymbol *Symbol, fieldName string) {
			if !isFunctionShaped {
				return
			}

			// The original's comment: we're expecting a __call__ method. We will also
			// ignore a __slots__ definition, which is (by convention) ignored for
			// protocol matching.
			if fieldName == "__call__" || fieldName == "__slots__" {
				return
			}

			if fieldSymbol.IsIgnoredForProtocolMatch() {
				return
			}

			fieldIsPartOfFunction := false
			if e.prefetched != nil && e.prefetched.FunctionClass != nil &&
				IsClass(e.prefetched.FunctionClass) {
				if _, has := ClassTypeGetSymbolTable(
					e.prefetched.FunctionClass.(*ClassType)).Get(fieldName); has {
					fieldIsPartOfFunction = true
				}
			}

			if !fieldIsPartOfFunction {
				isFunctionShaped = false
			}
		})
	}

	return isFunctionShaped
}

// assignModuleToProtocol delegates to the protocols.ts function of the same
// name, which the original reaches as a module import.
func (e *typeEvaluator) assignModuleToProtocol(
	destType *ClassType, srcType *ModuleType, diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker, flags AssignTypeFlags, recursionCount int,
) bool {
	return AssignModuleToProtocol(e, destType, srcType, diag, constraints, flags, recursionCount)
}

// assignClassToProtocol delegates to the protocols.ts function of the same name.
func (e *typeEvaluator) assignClassToProtocol(
	destType *ClassType, srcType *ClassType, diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker, flags AssignTypeFlags, recursionCount int,
) bool {
	return AssignClassToProtocol(e, destType, srcType, diag, constraints, flags, recursionCount)
}

// combineTupleTypeArgs reaches the typeUtils.ts function of the same name.
func (e *typeEvaluator) combineTupleTypeArgs(typeArgs []*TupleTypeArg) Type {
	return CombineTupleTypeArgs(typeArgs)
}

/*
 * The three destination-shape blocks, lifted out because each is long enough to
 * obscure the cascade.
 */

// assignToInstantiableClass is the original's `if (isInstantiableClass(destType))
// { if (isInstantiableClass(expandedSrcType))` block. It always produces an
// answer, which is why it returns a plain bool.
func (e *typeEvaluator) assignToInstantiableClass(
	destType *ClassType,
	srcType Type,
	expandedSrcType *ClassType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	// The original's comment: PEP 544 says that if the dest type is a
	// type[Proto] class, the source must be a "concrete" (non-protocol) class.
	if ClassTypeIsProtocolClass(destType) {
		if (flags&AssignTypeFlagsAllowProtocolClassSource) == 0 &&
			ClassTypeIsProtocolClass(expandedSrcType) &&
			IsInstantiableClass(srcType) && !srcType.(*ClassType).Priv.IncludeSubclasses {
			if diag != nil {
				diag.AddMessage(localization.LocAddendum.ProtocolSourceIsNotConcrete().Format(
					e.PrintType(ConvertToInstance(srcType, true), nil),
					e.PrintType(destType, nil),
				))
			}
			return false
		}
	}

	if ClassTypeIsBuiltInNamed(destType, "type") {
		if props := srcType.Base().Props; props != nil && props.InstantiableDepth != nil && *props.InstantiableDepth > 0 {
			return true
		}
	}

	if e.IsSpecialFormClass(expandedSrcType, flags) {
		// The original's comment: special form classes are compatible only with
		// other special form classes, not with 'object' or 'type'.
		var destSpecialForm Type = destType
		if props := destType.Base().Props; props != nil && props.SpecialForm != nil {
			destSpecialForm = props.SpecialForm
		}
		if asClass, ok := destSpecialForm.(*ClassType); ok && e.IsSpecialFormClass(asClass, flags) {
			return e.AssignType(destSpecialForm, expandedSrcType, diag, constraints, flags, recursionCount)
		}
	} else if e.assignClass(destType, expandedSrcType, diag, constraints, flags, recursionCount, false) {
		return true
	}

	e.addTypeMismatch(diag, srcType, destType)
	return false
}

// assignToClassInstance is the original's `if (isClassInstance(destType))`
// block. It reports whether it produced an answer.
func (e *typeEvaluator) assignToClassInstance(
	destType *ClassType,
	srcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) (bool, bool) {
	if ClassTypeIsBuiltInNamed(destType, "type") {
		if IsInstantiableClass(srcType) && e.IsSpecialFormClass(srcType.(*ClassType), flags) &&
			srcType.Base().GetInstantiableDepth() == 0 {
			return true, false
		}

		if IsAnyOrUnknown(srcType) && (flags&AssignTypeFlagsOverloadOverlap) != 0 {
			return true, false
		}

		destTypeArgs := destType.Priv.TypeArgs
		if len(destTypeArgs) >= 1 {
			if destTypeArgs[0].Base().IsInstance() && srcType.Base().IsInstantiable() {
				return true, e.AssignType(destTypeArgs[0], ConvertToInstance(srcType, true),
					diag, constraints, flags, recursionCount)
			}
		}

		// The original's comment: is the dest a "type" object? Assume that all
		// instantiable types are assignable to "type".
		if srcType.Base().IsInstantiable() {
			isLiteral := IsClass(srcType) && srcType.(*ClassType).Priv.LiteralValue != nil
			return true, !isLiteral
		}
	}

	concreteSrcType := e.makeTopLevelTypeVarsConcrete(srcType, false, nil)

	// The original's comment: handle the TypeForm special form. Add a special
	// case for type[T] to be assignable to TypeForm[T].
	if ClassTypeIsBuiltInNamed(destType, "TypeForm") {
		var destTypeArg Type = AnyTypeCreate(false)
		if len(destType.Priv.TypeArgs) > 0 {
			destTypeArg = destType.Priv.TypeArgs[0]
		}

		var srcTypeArg Type
		if IsClassInstance(concreteSrcType) && ClassTypeIsBuiltInNamed(concreteSrcType.(*ClassType), "type") {
			srcTypeArg = concreteSrcType
		} else if IsInstantiableClass(concreteSrcType) {
			srcTypeArg = ConvertToInstance(concreteSrcType, true)
		}

		if srcTypeArg != nil {
			return true, e.AssignType(destTypeArg, srcTypeArg, diag, constraints, flags, recursionCount)
		}
	}

	if IsClass(concreteSrcType) && concreteSrcType.Base().IsInstance() {
		return true, e.assignClassInstanceToClassInstance(destType, srcType, concreteSrcType.(*ClassType),
			diag, constraints, flags, recursionCount)
	}

	if IsFunctionOrOverloaded(concreteSrcType) {
		// The original's comment: is the destination a callback protocol
		// (defined in PEP 544)?
		if destCallbackType := e.getCallbackProtocolType(destType, recursionCount); destCallbackType != nil {
			return true, e.AssignType(destCallbackType, concreteSrcType, diag, constraints, flags, recursionCount)
		}

		// The original's comment: all functions are considered instances of
		// "types.FunctionType" or "types.MethodType".
		var altClass Type
		if e.prefetched != nil {
			if IsMethodType(concreteSrcType) {
				altClass = e.prefetched.MethodClass
			} else {
				altClass = e.prefetched.FunctionClass
			}
		}
		if altClass != nil {
			return true, e.AssignType(destType, ConvertToInstance(altClass, true),
				diag, constraints, flags, recursionCount)
		}
		return false, false
	}

	if IsModule(concreteSrcType) {
		// The original's comment: is the destination the built-in "ModuleType"?
		if ClassTypeIsBuiltInNamed(destType, "ModuleType") {
			return true, true
		}

		if ClassTypeIsProtocolClass(destType) {
			return true, e.assignModuleToProtocol(
				ClassTypeCloneAsInstantiable(destType, false),
				concreteSrcType.(*ModuleType),
				diag, constraints, flags, recursionCount,
			)
		}
		return false, false
	}

	if IsInstantiableClass(concreteSrcType) {
		// The original's comment: see if the destType is an instantiation of a
		// Protocol class that is effectively a function.
		if callbackType := e.getCallbackProtocolType(destType, recursionCount); callbackType != nil {
			return true, e.AssignType(callbackType, concreteSrcType, diag, constraints, flags, recursionCount)
		}

		// The original's comment: if the destType is an instantiation of a
		// Protocol, see if the class type itself satisfies the protocol.
		if ClassTypeIsProtocolClass(destType) {
			return true, e.assignClassToProtocol(
				ClassTypeCloneAsInstantiable(destType, false),
				concreteSrcType.(*ClassType),
				diag, constraints, flags, recursionCount,
			)
		}

		// The original's comment: determine if the metaclass can be assigned to
		// the object.
		if metaclass := concreteSrcType.(*ClassType).Shared.EffectiveMetaclass; metaclass != nil {
			if !IsAnyOrUnknown(metaclass) {
				if asClass, ok := metaclass.(*ClassType); ok {
					if e.assignClass(ClassTypeCloneAsInstantiable(destType, false), asClass,
						nil, constraints, flags, recursionCount, true) {
						return true, true
					}
				}
			}
		}
		return false, false
	}

	if props := concreteSrcType.Base().Props; IsAnyOrUnknown(concreteSrcType) &&
		(props == nil || props.SpecialForm == nil) {
		return true, (flags & AssignTypeFlagsOverloadOverlap) == 0
	}

	if IsUnion(concreteSrcType) {
		return true, e.AssignType(destType, concreteSrcType, diag, constraints, flags, recursionCount)
	}

	return false, false
}

// assignClassInstanceToClassInstance is the original's
// `if (isClass(concreteSrcType) && TypeBase.isInstance(concreteSrcType))` arm.
func (e *typeEvaluator) assignClassInstanceToClassInstance(
	destType *ClassType,
	srcType Type,
	concreteSrcType *ClassType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	// The original's comment: handle the case where the source is an unpacked
	// tuple.
	if !destType.Priv.IsUnpacked && concreteSrcType.Priv.IsUnpacked && concreteSrcType.Priv.TupleTypeArgs != nil {
		return e.AssignType(destType, e.combineTupleTypeArgs(concreteSrcType.Priv.TupleTypeArgs),
			diag, constraints, flags, recursionCount)
	}

	if destType.Priv.LiteralValue != nil && ClassTypeIsSameGenericClass(destType, concreteSrcType, 0) {
		if concreteSrcType.Priv.LiteralValue == nil || !ClassTypeIsLiteralValueSame(concreteSrcType, destType) {
			if diag != nil {
				diag.AddMessage(localization.LocAddendum.LiteralAssignmentMismatch().Format(
					e.PrintType(srcType, nil),
					e.PrintType(destType, nil),
				))
			}
			return false
		}
	}

	// The original's comment: handle LiteralString special form.
	if ClassTypeIsBuiltInNamed(destType, "LiteralString") {
		if ClassTypeIsBuiltInNamed(concreteSrcType, "str") && concreteSrcType.Priv.LiteralValue != nil {
			return (flags & AssignTypeFlagsInvariant) == 0
		}
		if ClassTypeIsBuiltInNamed(concreteSrcType, "LiteralString") {
			return true
		}
	} else if ClassTypeIsBuiltInNamed(concreteSrcType, "LiteralString") &&
		e.prefetched != nil && e.prefetched.StrClass != nil && IsInstantiableClass(e.prefetched.StrClass) &&
		(flags&AssignTypeFlagsInvariant) == 0 {
		concreteSrcType = ClassTypeCloneAsInstance(e.prefetched.StrClass.(*ClassType), false)
	}

	return e.assignClass(
		ClassTypeCloneAsInstantiable(destType, false),
		ClassTypeCloneAsInstantiable(concreteSrcType, false),
		diag, constraints, flags, recursionCount, true,
	)
}

// assignToFunction is the original's `if (isFunction(destType))` block.
func (e *typeEvaluator) assignToFunction(
	destType *FunctionType,
	srcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) (bool, bool) {
	concreteSrcType := e.makeTopLevelTypeVarsConcrete(srcType, false, nil)

	if IsClassInstance(concreteSrcType) {
		if boundMethod := e.GetBoundMagicMethod(concreteSrcType.(*ClassType), "__call__",
			nil, nil, nil, recursionCount); boundMethod != nil {
			concreteSrcType = boundMethod
		}
	}

	// The original's comment: if it's a class, use the constructor for type
	// compatibility checking.
	if IsInstantiableClass(concreteSrcType) && concreteSrcType.(*ClassType).Priv.LiteralValue == nil {
		var selfType Type
		if IsTypeVar(srcType) {
			selfType = ConvertToInstance(srcType, true)
		}
		if constructor := e.createFunctionFromConstructor(concreteSrcType.(*ClassType), selfType, recursionCount); constructor != nil {
			concreteSrcType = constructor

			// The original's comment: the constructor conversion may result in a
			// union of the __init__ and __new__ callables.
			if IsUnion(concreteSrcType) {
				return true, e.AssignType(destType, concreteSrcType, diag, constraints, flags, recursionCount)
			}
		}
	}

	if IsAnyOrUnknown(concreteSrcType) {
		return true, (flags & AssignTypeFlagsOverloadOverlap) == 0
	}

	if IsOverloaded(concreteSrcType) {
		return true, e.assignOverloadedToFunction(destType, concreteSrcType.(*OverloadedType),
			diag, constraints, flags, recursionCount)
	}

	if IsFunction(concreteSrcType) {
		var addendum *common.DiagnosticAddendum
		if diag != nil {
			addendum = diag.CreateAddendum()
		}
		effectiveConstraints := constraints
		if effectiveConstraints == nil {
			effectiveConstraints = NewConstraintTracker()
		}
		if e.assignFunction(destType, concreteSrcType.(*FunctionType), addendum, effectiveConstraints, flags, recursionCount) {
			return true, true
		}
	}

	return false, false
}

// assignOverloadedToFunction is the original's overloaded-source arm: filter the
// overloads to those assignable to the destination.
func (e *typeEvaluator) assignOverloadedToFunction(
	destType *FunctionType,
	concreteSrcType *OverloadedType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	// The original's comment: if this is the first pass of an argument
	// assignment, skip all attempts to assign an overloaded function to a
	// function because we probably don't have enough information to properly
	// filter the overloads at this time. We will do this work on subsequent
	// passes.
	if (flags & AssignTypeFlagsArgAssignmentFirstPass) != 0 {
		return true
	}

	// The original's comment: find all of the overloaded functions that match
	// the parameters.
	overloads := OverloadedTypeGetOverloads(concreteSrcType)
	filteredCount := 0
	typeVarSignatures := []*ConstraintSet{}

	for _, overload := range overloads {
		overloadScopeID := GetTypeVarScopeID(overload)
		var constraintsClone *ConstraintTracker
		if constraints != nil {
			constraintsClone = constraints.CloneWithSignature(overloadScopeID)
		}

		if e.AssignType(destType, overload, nil, constraintsClone, flags, recursionCount) {
			filteredCount++

			if constraintsClone != nil {
				typeVarSignatures = append(typeVarSignatures, constraintsClone.GetConstraintSets()...)
			}
		}
	}

	if filteredCount == 0 {
		if diag != nil {
			diag.AddMessage(localization.LocAddendum.NoOverloadAssignable().Format(e.PrintType(destType, nil)))
		}
		return false
	}

	if filteredCount == 1 || (flags&AssignTypeFlagsArgAssignmentFirstPass) == 0 {
		if constraints != nil {
			constraints.AddConstraintSets(typeVarSignatures)
		}
	}

	return true
}

// assignToOverloaded is the original's `if (isOverloaded(destType))` block. The
// original's comment: all overloads in the dest must be assignable.
func (e *typeEvaluator) assignToOverloaded(
	destType *OverloadedType,
	srcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	var overloadDiag *common.DiagnosticAddendum
	if diag != nil {
		overloadDiag = diag.CreateAddendum()
	}

	destOverloads := OverloadedTypeGetOverloads(destType)

	// The original's comment: if the source is also an overload with the same
	// number of overloads, there's a good chance that there's a one-to-one
	// mapping. Try this first before using an n^2 algorithm.
	if IsOverloaded(srcType) {
		srcOverloads := OverloadedTypeGetOverloads(srcType.(*OverloadedType))
		if len(destOverloads) == len(srcOverloads) {
			allMatch := true
			for index, destOverload := range destOverloads {
				if !e.AssignType(destOverload, srcOverloads[index], nil, constraints, flags, recursionCount) {
					allMatch = false
					break
				}
			}
			if allMatch {
				return true
			}
		}
	}

	for _, destOverload := range destOverloads {
		var addendum *common.DiagnosticAddendum
		if overloadDiag != nil {
			addendum = overloadDiag.CreateAddendum()
		}
		if !e.AssignType(destOverload, srcType, addendum, constraints, flags, recursionCount) {
			// The original re-reads the overload list here rather than reusing
			// destOverloads; the two are the same.
			overloads := OverloadedTypeGetOverloads(destType)
			if overloadDiag != nil && len(overloads) > 0 {
				overloadDiag.AddMessage(
					localization.LocAddendum.OverloadNotAssignable().Format(overloads[0].Shared.Name),
				)
			}
			return false
		}
	}

	return true
}
