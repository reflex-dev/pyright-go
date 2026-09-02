/*
 * typeevaluator_specialforms.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * createOptionalType, createClassVarType, createTypeFormType,
 * createTypeGuardType, createRequiredOrReadOnlyType, createUnpackType,
 * createFinalType, createConcatenateType, createUnionType and the small
 * applyUnpackToTupleLike that createUnpackType needs.
 *
 * These are the subscript handlers for the typing special forms whose meaning
 * is not ordinary generic specialization -- `Optional[X]` is a union, `Final[X]`
 * is X carrying a marker, `Required[X]` is X carrying three TypedDict flags.
 * They share one shape, and reading them together is what makes that shape
 * visible:
 *
 *   1. Decide what a bare, unsubscripted form means. Outside a type expression
 *      the class object itself is the answer; inside one it is a diagnostic.
 *      TypeFormArg is a third context, and it consistently answers Unknown
 *      where a type expression would answer the class -- a TypeForm argument
 *      must denote a type, and a bare special form does not.
 *   2. Check the argument count, with the same three-way context split.
 *   3. Check that the form is legal *here* -- ClassVar not under NoClassVar,
 *      Final not under NoFinal, Concatenate only under AllowConcatenate,
 *      Required/ReadOnly only inside a TypedDict annotation.
 *   4. Build the result, and separately maintain props.typeForm, which tracks
 *      what the expression would evaluate to at runtime.
 *
 * Step 4 is the one that is easy to lose. createOptionalType and createUnionType
 * both carry a typeForm through only when every operand had one, because a
 * union of types is a type only if all its members are; a single missing
 * typeForm clears the whole result's.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// typeFormOf returns type.props?.typeForm, guarding the two nil hops the
// original spells with optional chaining.
func typeFormOf(t Type) Type {
	if t == nil {
		return nil
	}
	props := t.Base().Props
	if props == nil {
		return nil
	}
	return props.TypeForm
}

// createOptionalType corresponds to the function of the same name.
func (e *typeEvaluator) createOptionalType(
	classType *ClassType,
	errorNode parser.ExpressionNode,
	typeArgs []*TypeResultWithNode,
	flags EvalFlags,
) Type {
	if typeArgs == nil {
		// The original's comment: if no type arguments are provided, the resulting
		// type depends on whether we're evaluating a type annotation or we're in
		// some other context.
		if (flags & (EvalFlagsTypeExpression | EvalFlagsTypeFormArg)) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.OptionalExtraArgs(), errorNode, nil)
			return UnknownTypeCreate(false)
		}

		return classType
	}

	if len(typeArgs) != 1 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.OptionalExtraArgs(), errorNode, nil)
		return UnknownTypeCreate(false)
	}

	typeArg0Type := typeArgs[0].Type
	if !e.ValidateTypeArg(typeArgs[0], nil) {
		typeArg0Type = UnknownTypeCreate(false)
	}

	var noneTypeClass Type = UnknownTypeCreate(false)
	if e.prefetched != nil && e.prefetched.NoneTypeClass != nil {
		noneTypeClass = e.prefetched.NoneTypeClass
	}

	optionalType := CombineTypes([]Type{typeArg0Type, noneTypeClass}, nil)
	if e.prefetched != nil && e.prefetched.UnionTypeClass != nil &&
		IsInstantiableClass(e.prefetched.UnionTypeClass) {
		optionalType = CloneAsSpecialForm(optionalType,
			ClassTypeCloneAsInstance(e.prefetched.UnionTypeClass.(*ClassType), true))
	}

	if typeForm := typeFormOf(typeArg0Type); typeForm != nil {
		typeFormType := CombineTypes(
			[]Type{typeForm, ConvertToInstance(noneTypeClass, false)}, nil)
		optionalType = CloneWithTypeForm(optionalType, typeFormType)
	}

	return optionalType
}

// createClassVarType corresponds to the function of the same name.
func (e *typeEvaluator) createClassVarType(
	classType *ClassType,
	errorNode parser.ExpressionNode,
	typeArgs []*TypeResultWithNode,
	flags EvalFlags,
) Type {
	if (flags & (EvalFlagsNoClassVar | EvalFlagsTypeFormArg)) != 0 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.ClassVarNotAllowed(), errorNode, nil)
		if (flags & EvalFlagsTypeFormArg) != 0 {
			return UnknownTypeCreate(false)
		}
		return AnyTypeCreate(false)
	}

	if typeArgs == nil {
		return classType
	} else if len(typeArgs) == 0 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.ClassVarFirstArgMissing(), errorNode, nil)
		return UnknownTypeCreate(false)
	} else if len(typeArgs) > 1 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.ClassVarTooManyArgs(), typeArgs[1].Node, nil)
		return UnknownTypeCreate(false)
	}

	t := typeArgs[0].Type

	// The original's comment: a ClassVar should not allow TypeVars or generic
	// types parameterized by TypeVars.
	if RequiresSpecialization(t,
		&RequiresSpecializationOptions{IgnorePseudoGeneric: true, IgnoreSelf: true}, 0) {
		node := typeArgs[0].Node
		if node == nil {
			node = errorNode
		}
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.ClassVarWithTypeVar(), node, nil)
	}

	return t
}

// createTypeFormType corresponds to the function of the same name.
func (e *typeEvaluator) createTypeFormType(
	classType *ClassType,
	errorNode parser.ExpressionNode,
	typeArgs []*TypeResultWithNode,
) Type {
	if typeArgs == nil {
		specializedType := ClassTypeSpecialize(
			classType, []Type{AnyTypeCreate(false)}, nil, false, nil, nil)
		return CloneWithTypeForm(specializedType,
			ClassTypeCloneAsInstance(specializedType, true))
	}

	name := classType.Shared.Name
	if classType.Priv.AliasName != nil && *classType.Priv.AliasName != "" {
		name = *classType.Priv.AliasName
	}

	if len(typeArgs) == 0 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.TypeArgsTooFew().Format(name, 1, 0), errorNode, nil)
		return UnknownTypeCreate(false)
	}

	if len(typeArgs) > 1 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.TypeArgsTooMany().Format(name, 1, len(typeArgs)),
			typeArgs[1].Node, nil)
		return UnknownTypeCreate(false)
	}

	convertedTypeArgs := e.convertTypeArgsToInstances(typeArgs)
	resultType := ClassTypeSpecialize(classType, convertedTypeArgs, nil, false, nil, nil)

	return CloneWithTypeForm[Type](resultType, ConvertToInstance(resultType, false))
}

// convertTypeArgsToInstances corresponds to the `typeArgs.map(...)` that
// createTypeFormType and createTypeGuardType both spell inline: validate each
// argument, substitute Unknown for an invalid one, and convert to an instance.
func (e *typeEvaluator) convertTypeArgsToInstances(typeArgs []*TypeResultWithNode) []Type {
	converted := make([]Type, 0, len(typeArgs))
	for _, typeArg := range typeArgs {
		var t Type = UnknownTypeCreate(false)
		if e.ValidateTypeArg(typeArg, nil) {
			t = typeArg.Type
		}
		converted = append(converted, ConvertToInstance(t, false))
	}
	return converted
}

// createTypeGuardType corresponds to the function of the same name. The
// original's comment: creates a "TypeGuard" and "TypeIs" type.
func (e *typeEvaluator) createTypeGuardType(
	classType *ClassType,
	errorNode parser.ExpressionNode,
	typeArgs []*TypeResultWithNode,
	flags EvalFlags,
) Type {
	// The original's comment: if no type arguments are provided, the resulting
	// type depends on whether we're evaluating a type annotation or we're in some
	// other context.
	if typeArgs == nil {
		if (flags & (EvalFlagsTypeExpression | EvalFlagsTypeFormArg)) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.TypeGuardArgCount(), errorNode, nil)
		}

		if (flags & EvalFlagsTypeFormArg) != 0 {
			return UnknownTypeCreate(false)
		}
		return classType
	} else if len(typeArgs) != 1 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.TypeGuardArgCount(), errorNode, nil)
		return UnknownTypeCreate(false)
	}

	convertedTypeArgs := e.convertTypeArgsToInstances(typeArgs)
	resultType := ClassTypeSpecialize(classType, convertedTypeArgs, nil, false, nil, nil)

	return CloneWithTypeForm[Type](resultType, ConvertToInstance(resultType, false))
}

// createRequiredOrReadOnlyType corresponds to the function of the same name. It
// returns a TypeResult rather than a Type because the three TypedDict flags it
// sets ride alongside the type rather than in it.
func (e *typeEvaluator) createRequiredOrReadOnlyType(
	classType *ClassType,
	errorNode parser.ExpressionNode,
	typeArgs []*TypeResultWithNode,
	flags EvalFlags,
) *TypeResult {
	// The original's comment: if no type arguments are provided, the resulting
	// type depends on whether we're evaluating a type annotation or we're in some
	// other context.
	typeExpressionFlags := EvalFlagsTypeExpression | EvalFlagsTypeFormArg

	if typeArgs == nil && (flags&typeExpressionFlags) == 0 {
		return &TypeResult{Type: classType}
	}

	// argCountMessage picks among the three messages by which special form this
	// is, exactly as the original's nested conditional does.
	argCountMessage := func() string {
		switch classType.Shared.Name {
		case "ReadOnly":
			return localization.LocMessage.ReadOnlyArgCount()
		case "Required":
			return localization.LocMessage.RequiredArgCount()
		default:
			return localization.LocMessage.NotRequiredArgCount()
		}
	}

	notInTypedDictMessage := func() string {
		switch classType.Shared.Name {
		case "ReadOnly":
			return localization.LocMessage.ReadOnlyNotInTypedDict()
		case "Required":
			return localization.LocMessage.RequiredNotInTypedDict()
		default:
			return localization.LocMessage.NotRequiredNotInTypedDict()
		}
	}

	// bareResult is the answer both failure exits give: Unknown under
	// TypeFormArg, otherwise the unsubscripted class.
	bareResult := func() *TypeResult {
		if (flags & EvalFlagsTypeFormArg) != 0 {
			return &TypeResult{Type: UnknownTypeCreate(false)}
		}
		return &TypeResult{Type: classType}
	}

	if typeArgs == nil || len(typeArgs) != 1 {
		if (flags & typeExpressionFlags) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, argCountMessage(), errorNode, nil)
		}

		return bareResult()
	}

	typeArgType := typeArgs[0].Type

	// The original's comment: make sure this is used only in a dataclass.
	containingClassNode := GetEnclosingClass(errorNode, true)
	var classTypeInfo *ClassTypeResult
	if containingClassNode != nil {
		classTypeInfo = e.GetTypeOfClass(containingClassNode)
	}

	isUsageLegal := false

	if classTypeInfo != nil && IsInstantiableClass(classTypeInfo.ClassType) &&
		ClassTypeIsTypedDictClass(classTypeInfo.ClassType) {
		// The original's comment: the only legal usage is when used in a type
		// annotation statement.
		if IsNodeContainedWithinNodeType(errorNode, parser.ParseNodeTypeTypeAnnotation) {
			isUsageLegal = true
		}
	}

	isReadOnly := typeArgs[0].IsReadOnly
	isRequired := typeArgs[0].IsRequired
	isNotRequired := typeArgs[0].IsNotRequired

	if classType.Shared.Name == "ReadOnly" {
		if (flags & EvalFlagsAllowReadOnly) != 0 {
			isUsageLegal = true
		}

		// The original's comment: nested ReadOnly are not allowed.
		if typeArgs[0].IsReadOnly {
			isUsageLegal = false
		}

		isReadOnly = true
	} else {
		if (flags & EvalFlagsAllowRequired) != 0 {
			isUsageLegal = true
		}

		// The original's comment: nested Required/NotRequired are not allowed.
		if typeArgs[0].IsRequired || typeArgs[0].IsNotRequired {
			isUsageLegal = false
		}

		isRequired = classType.Shared.Name == "Required"
		isNotRequired = classType.Shared.Name == "NotRequired"
	}

	if !isUsageLegal {
		if (flags & typeExpressionFlags) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, notInTypedDictMessage(), errorNode, nil)
		}

		return bareResult()
	}

	return &TypeResult{
		Type:          typeArgType,
		IsReadOnly:    isReadOnly,
		IsRequired:    isRequired,
		IsNotRequired: isNotRequired,
	}
}

// applyUnpackToTupleLike corresponds to the function of the same name: the
// unpacked form of the argument, or nil when the argument is not something that
// can be unpacked.
func (e *typeEvaluator) applyUnpackToTupleLike(t Type) Type {
	if IsTypeVarTuple(t) {
		if !t.(*TypeVarType).Priv.IsUnpacked {
			return TypeVarTypeCloneForUnpacked(t.(*TypeVarType), false)
		}

		return nil
	}

	if IsParamSpec(t) {
		return nil
	}

	// The original's comment: is this a TypeVar that has a tuple upper bound?
	if IsTypeVar(t) {
		upperBound := t.(*TypeVarType).Shared.BoundType

		if upperBound != nil && IsClassInstance(upperBound) &&
			IsTupleClass(upperBound.(*ClassType)) {
			return TypeVarTypeCloneForUnpacked(t.(*TypeVarType), false)
		}

		return nil
	}

	if IsInstantiableClass(t) && !t.(*ClassType).Priv.IncludeSubclasses {
		if IsTupleClass(t.(*ClassType)) {
			return ClassTypeCloneForUnpacked(t.(*ClassType))
		}
	}

	return nil
}

// createUnpackType corresponds to the function of the same name.
func (e *typeEvaluator) createUnpackType(
	classType *ClassType,
	errorNode parser.ExpressionNode,
	typeArgs []*TypeResultWithNode,
	flags EvalFlags,
) Type {
	if typeArgs == nil || len(typeArgs) != 1 {
		if (flags & (EvalFlagsTypeExpression | EvalFlagsTypeFormArg)) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.UnpackArgCount(), errorNode, nil)
		}
		if (flags & EvalFlagsTypeFormArg) != 0 {
			return UnknownTypeCreate(false)
		}
		return classType
	}

	typeArgType := typeArgs[0].Type

	if (flags & EvalFlagsAllowUnpackedTuple) != 0 {
		if unpackedType := e.applyUnpackToTupleLike(typeArgType); unpackedType != nil {
			return unpackedType
		}

		if (flags & (EvalFlagsTypeExpression | EvalFlagsTypeFormArg)) == 0 {
			return classType
		}
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.UnpackExpectedTypeVarTuple(), errorNode, nil)
		return UnknownTypeCreate(false)
	}

	if (flags & EvalFlagsAllowUnpackedTypedDict) != 0 {
		if IsInstantiableClass(typeArgType) && ClassTypeIsTypedDictClass(typeArgType.(*ClassType)) {
			return ClassTypeCloneForUnpacked(typeArgType.(*ClassType))
		}

		if (flags & EvalFlagsTypeExpression) == 0 {
			return classType
		}
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.UnpackExpectedTypedDict(), errorNode, nil)
		return UnknownTypeCreate(false)
	}

	if (flags & (EvalFlagsTypeExpression | EvalFlagsTypeFormArg)) == 0 {
		return classType
	}
	if (flags & EvalFlagsTypeFormArg) != 0 {
		if IsUnknown(typeArgType) {
			return typeArgType
		}
		if IsTypeVar(typeArgType) && typeArgType.(*TypeVarType).Priv.ScopeID == "" {
			return UnknownTypeCreate(false)
		}
	}
	e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.UnpackNotAllowed(), errorNode, nil)
	return UnknownTypeCreate(false)
}

// createFinalType corresponds to the function of the same name. The original's
// comment: creates a "Final" type.
func (e *typeEvaluator) createFinalType(
	classType *ClassType,
	errorNode parser.ExpressionNode,
	typeArgs []*TypeResultWithNode,
	flags EvalFlags,
) Type {
	if (flags & (EvalFlagsNoFinal | EvalFlagsTypeFormArg)) != 0 {
		if (flags & (EvalFlagsTypeExpression | EvalFlagsTypeFormArg)) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.FinalContext(), errorNode, nil)
		}
		if (flags & EvalFlagsTypeFormArg) != 0 {
			return UnknownTypeCreate(false)
		}
		return classType
	}

	if (flags&EvalFlagsTypeExpression) == 0 || len(typeArgs) == 0 {
		return classType
	}

	if len(typeArgs) > 1 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.FinalTooManyArgs(), errorNode, nil)
	}

	return CloneAsSpecialForm(typeArgs[0].Type, classType)
}

// createConcatenateType corresponds to the function of the same name.
func (e *typeEvaluator) createConcatenateType(
	classType *ClassType,
	errorNode parser.ExpressionNode,
	typeArgs []*TypeResultWithNode,
	flags EvalFlags,
) Type {
	if (flags & EvalFlagsAllowConcatenate) == 0 {
		if (flags & (EvalFlagsTypeExpression | EvalFlagsTypeFormArg)) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.ConcatenateContext(), errorNode, nil)
		}
		if (flags & EvalFlagsTypeFormArg) != 0 {
			return UnknownTypeCreate(false)
		}
		return classType
	}

	if len(typeArgs) == 0 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.ConcatenateTypeArgsMissing(), errorNode, nil)
	} else {
		for index, typeArg := range typeArgs {
			if index == len(typeArgs)-1 {
				// The last argument must be the ParamSpec (or `...`); every earlier
				// one is an ordinary prepended positional parameter, so the checks
				// are the mirror image of each other.
				if !IsParamSpec(typeArg.Type) && !IsEllipsisType(typeArg.Type) {
					e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
						localization.LocMessage.ConcatenateParamSpecMissing(), typeArg.Node, nil)
				}
				continue
			}

			switch {
			case IsParamSpec(typeArg.Type):
				e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
					localization.LocMessage.ParamSpecContext(), typeArg.Node, nil)
			case IsUnpackedTypeVarTuple(typeArg.Type):
				e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
					localization.LocMessage.TypeVarTupleContext(), typeArg.Node, nil)
			case IsUnpackedClass(typeArg.Type):
				e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
					localization.LocMessage.UnpackedArgInTypeArgument(), typeArg.Node, nil)
			}
		}
	}

	return e.createSpecialType(classType, typeArgs, nil, boolPtr(true), nil)
}

// createUnionType corresponds to the function of the same name.
func (e *typeEvaluator) createUnionType(
	classType *ClassType,
	errorNode parser.ExpressionNode,
	typeArgs []*TypeResultWithNode,
	flags EvalFlags,
) Type {
	fileInfo := GetFileInfo(errorNode)
	types := []Type{}
	allowSingleTypeArg := false
	isValidTypeForm := true

	if typeArgs == nil {
		// The original's comment: if no type arguments are provided, the resulting
		// type depends on whether we're evaluating a type annotation or we're in
		// some other context.
		if (flags & (EvalFlagsTypeExpression | EvalFlagsTypeFormArg)) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.UnionTypeArgCount(), errorNode, nil)
			return NeverTypeCreateNever()
		}

		return classType
	}

	experimental := fileInfo.DiagnosticRuleSet.EnableExperimentalFeatures

	for _, typeArg := range typeArgs {
		typeArgType := typeArg.Type

		// The original's comment: this is an experimental feature because Unions of
		// unpacked TypeVarTuples are not officially supported.
		if !e.ValidateTypeArg(typeArg, &ValidateTypeArgsOptions{AllowTypeVarTuple: experimental}) {
			typeArgType = UnknownTypeCreate(false)
		}

		if IsTypeVar(typeArgType) && IsUnpackedTypeVarTuple(typeArgType) {
			// The original's comment: this is an experimental feature because Unions
			// of unpacked TypeVarTuples are not officially supported.
			if experimental {
				// The original's comment: if this is an unpacked TypeVar, note that
				// it is in a union so we can differentiate between Unpack[Vs] and
				// Union[Unpack[Vs]].
				typeArgType = TypeVarTypeCloneForUnpacked(typeArgType.(*TypeVarType), true)
				allowSingleTypeArg = true
			} else {
				e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.UnionUnpackedTypeVarTuple(), errorNode, nil)

				typeArgType = UnknownTypeCreate(false)
				isValidTypeForm = false
			}
		}

		types = append(types, typeArgType)
	}

	// The original's comment: validate that we received at least two type
	// arguments. One type argument is allowed if it's an unpacked TypeVarTuple or
	// tuple. None is also allowed since it is used to define NoReturn in typeshed
	// stubs).
	if len(types) == 1 && !allowSingleTypeArg && !IsNoneInstance(types[0]) {
		if (flags & (EvalFlagsTypeExpression | EvalFlagsTypeFormArg)) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeArguments,
				localization.LocMessage.UnionTypeArgCount(), errorNode, nil)
		}
		isValidTypeForm = false
	}

	unionType := CombineTypes(types, &CombineTypesOptions{SkipElideRedundantLiterals: true})
	if e.prefetched != nil && e.prefetched.UnionTypeClass != nil &&
		IsInstantiableClass(e.prefetched.UnionTypeClass) {
		unionType = CloneAsSpecialForm(unionType,
			ClassTypeCloneAsInstance(e.prefetched.UnionTypeClass.(*ClassType), true))
	}

	anyMissingTypeForm := false
	for _, t := range types {
		if typeFormOf(t) == nil {
			anyMissingTypeForm = true
			break
		}
	}

	if !isValidTypeForm || anyMissingTypeForm {
		if typeFormOf(unionType) != nil {
			unionType = CloneWithTypeForm(unionType, nil)
		}
	} else {
		typeForms := make([]Type, 0, len(types))
		for _, t := range types {
			typeForms = append(typeForms, typeFormOf(t))
		}
		unionType = CloneWithTypeForm(unionType, CombineTypes(typeForms, nil))
	}

	return unionType
}
