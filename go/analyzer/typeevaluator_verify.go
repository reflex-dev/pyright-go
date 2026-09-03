/*
 * typeevaluator_verify.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * verifyRaiseExceptionType, verifyDeleteExpression, createClassFromMetaclass,
 * assignRecursiveTypeAliasToSelf, classGetItemReturnsGenericAlias and
 * rejectBareSpecialFormInTypeForm.
 *
 * verifyRaiseExceptionType checks both halves of what `raise` accepts: an
 * exception *instance*, or an exception *class* that the runtime will
 * instantiate with no arguments. The second is the interesting one -- raising a
 * class whose `__init__` requires arguments is a runtime TypeError, so the check
 * speculatively constructs it and reports if that fails.
 *
 * verifyDeleteExpression dispatches on the target form because `del` means
 * something different for each: a name is merely read (to mark it accessed and
 * to check boundness), while an attribute or subscript is a real `__delattr__` /
 * `__delitem__` operation with its own usage method. The `del` usage passed to
 * the member and index paths is what routes those to the deleter rather than the
 * getter.
 *
 * assignRecursiveTypeAliasToSelf compares a recursive alias against itself
 * argument by argument, applying each type parameter's *computed* variance. The
 * contravariant case flips the direction with XOR rather than setting a flag,
 * because the comparison may already be running contravariantly and two flips
 * cancel.
 *
 * classGetItemReturnsGenericAlias exists because `C[int]` on a class with a
 * custom `__class_getitem__` may produce a runtime `GenericAlias` rather than a
 * specialized class, and that changes what the subscript expression means. For an
 * overloaded `__class_getitem__` *every* overload must agree, since the call site
 * may select any of them.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// VerifyRaiseExceptionType corresponds to verifyRaiseExceptionType.
func (e *typeEvaluator) VerifyRaiseExceptionType(node parser.ExpressionNode, allowNone bool) {
	baseExceptionType := e.GetBuiltInType(node, "BaseException")
	exceptionType := e.GetTypeOfExpression(node, EvalFlagsNone, nil).Type

	// The original's comment: validate that the argument of "raise" is an
	// exception object or class. If it is a class, validate that the class's
	// constructor accepts zero arguments.
	if IsNilType(exceptionType) || baseExceptionType == nil || !IsInstantiableClass(baseExceptionType) {
		return
	}
	baseExceptionClass := baseExceptionType.(*ClassType)

	diag := common.NewDiagnosticAddendum()

	DoForEachSubtype(exceptionType, func(subtype Type, _ int, _ []Type) {
		concreteSubtype := e.MakeTopLevelTypeVarsConcrete(subtype, false)

		if IsAnyOrUnknown(concreteSubtype) || IsNever(concreteSubtype) {
			return
		}

		if allowNone && IsNoneInstance(concreteSubtype) {
			return
		}

		if IsInstantiableClass(concreteSubtype) &&
			concreteSubtype.(*ClassType).Priv.LiteralValue == nil {
			concreteClass := concreteSubtype.(*ClassType)

			if !DerivesFromClassRecursive(concreteClass, baseExceptionClass, false) {
				diag.AddMessage(localization.LocMessage.ExceptionTypeIncorrect().Format(
					e.PrintType(subtype, nil)))
				return
			}

			// Raising a class instantiates it with no arguments, so a constructor
			// that requires any is a runtime TypeError.
			var callResult *CallResult
			e.suppressDiagnostics(node, func() {
				callResult = ValidateConstructorArgs(e, node, nil, concreteClass, false, nil)
			}, nil)

			if callResult != nil && callResult.ArgumentErrors {
				diag.AddMessage(localization.LocMessage.ExceptionTypeNotInstantiable().Format(
					e.PrintType(subtype, nil)))
			}
			return
		}

		if IsClassInstance(concreteSubtype) {
			if !DerivesFromClassRecursive(
				ClassTypeCloneAsInstantiable(concreteSubtype.(*ClassType), false),
				baseExceptionClass, false) {
				diag.AddMessage(localization.LocMessage.ExceptionTypeIncorrect().Format(
					e.PrintType(subtype, nil)))
			}
			return
		}

		diag.AddMessage(localization.LocMessage.ExceptionTypeIncorrect().Format(
			e.PrintType(subtype, nil)))
	})

	if !diag.IsEmpty() {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.ExpectedExceptionClass()+diag.GetString(), node, nil)
	}
}

// VerifyDeleteExpression corresponds to verifyDeleteExpression.
func (e *typeEvaluator) VerifyDeleteExpression(node parser.ExpressionNode) {
	switch typed := node.(type) {
	case *parser.NameNode:
		// The original's comment: get the type to evaluate whether it's bound and
		// to mark it accessed.
		e.GetTypeOfExpression(typed, EvalFlagsNone, nil)

	case *parser.MemberAccessNode:
		baseTypeResult := e.getTypeOfExpression(typed.D.LeftExpr, EvalFlagsMemberAccessBaseDefaults, nil)
		delAccessResult := e.getTypeOfMemberAccessWithBaseType(
			typed, baseTypeResult, &EvaluatorUsage{Method: "del"}, EvalFlagsNone)
		resultToCache := &TypeResult{
			Type:                        delAccessResult.Type,
			MemberAccessDeprecationInfo: delAccessResult.MemberAccessDeprecationInfo,
		}
		e.writeTypeCache(typed.D.Member, resultToCache, evalFlagsNonePtr(), nil, false)
		e.writeTypeCache(typed, resultToCache, evalFlagsNonePtr(), nil, false)

	case *parser.IndexNode:
		baseTypeResult := e.getTypeOfExpression(typed.D.LeftExpr, EvalFlagsIndexBaseDefaults, nil)
		e.getTypeOfIndexWithBaseType(typed, baseTypeResult, &EvaluatorUsage{Method: "del"}, EvalFlagsNone)
		e.writeTypeCache(typed, &TypeResult{Type: UnboundTypeCreate()}, evalFlagsNonePtr(), nil, false)

	case *parser.TupleNode:
		for _, expr := range typed.D.Items {
			e.VerifyDeleteExpression(expr)
		}

	case *parser.ErrorNode:
		// The original's comment: evaluate the child expression as best we can so
		// the type information is cached for the completion handler.
		if typed.D.Child != nil {
			child := typed.D.Child
			e.suppressDiagnostics(child, func() {
				e.GetTypeOfExpression(child, EvalFlagsNone, nil)
			}, nil)
		}

	default:
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.DelTargetExpr(), node, nil)
	}
}

// createClassFromMetaclass corresponds to the function of the same name: the
// three-argument `type(name, bases, dict)` call.
func (e *typeEvaluator) createClassFromMetaclass(
	errorNode parser.ExpressionNode, argList []*Arg, metaclass *ClassType,
) *ClassType {
	fileInfo := GetFileInfo(errorNode)
	arg0Type := e.GetTypeOfArg(argList[0], nil).Type
	if !IsClassInstance(arg0Type) || !ClassTypeIsBuiltInNamed(arg0Type.(*ClassType), "str") {
		return nil
	}
	className := "_"
	if s, ok := arg0Type.(*ClassType).Priv.LiteralValue.(LiteralString); ok && string(s) != "" {
		className = string(s)
	}

	arg1Type := e.GetTypeOfArg(argList[1], nil).Type

	// The original's comment: TODO - properly handle case where tuple of base
	// classes is provided.
	if !IsClassInstance(arg1Type) || !IsTupleClass(arg1Type.(*ClassType)) ||
		arg1Type.(*ClassType).Priv.TupleTypeArgs == nil {
		return nil
	}
	arg1Class := arg1Type.(*ClassType)

	classType := ClassTypeCreateInstantiable(
		className,
		GetClassFullName(errorNode, fileInfo.ModuleName, className),
		fileInfo.ModuleName,
		fileInfo.FileUri,
		ClassTypeFlagsValidTypeAliasClass,
		GetTypeSourceID(errorNode),
		metaclass,
		arg1Class.Shared.EffectiveMetaclass,
		nil,
	)

	for _, typeArg := range arg1Class.Priv.TupleTypeArgs {
		specializedType := e.MakeTopLevelTypeVarsConcrete(typeArg.Type, false)

		if IsEffectivelyInstantiable(specializedType, nil, 0) {
			classType.Shared.BaseClasses = append(classType.Shared.BaseClasses, specializedType)
		} else {
			classType.Shared.BaseClasses = append(classType.Shared.BaseClasses, UnknownTypeCreate(false))
		}
	}

	if !ComputeMroLinearization(classType) {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.MethodOrdering(), errorNode, nil)
	}

	return classType
}

// assignRecursiveTypeAliasToSelf corresponds to the function of the same name.
func (e *typeEvaluator) assignRecursiveTypeAliasToSelf(
	destAliasInfo *TypeAliasInfo,
	srcAliasInfo *TypeAliasInfo,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	isAssignable := true
	srcTypeArgs := srcAliasInfo.TypeArgs
	var variances []Variance
	if destAliasInfo.Shared != nil {
		variances = destAliasInfo.Shared.ComputedVariance
	}

	for index, destTypeArg := range destAliasInfo.TypeArgs {
		var srcTypeArg Type = UnknownTypeCreate(false)
		if index < len(srcTypeArgs) {
			srcTypeArg = srcTypeArgs[index]
		}

		adjFlags := flags
		variance := VarianceCovariant
		if variances != nil && index < len(variances) {
			variance = variances[index]
		}

		if variance == VarianceInvariant {
			adjFlags |= AssignTypeFlagsInvariant
		} else if variance == VarianceContravariant {
			// XOR rather than OR: the comparison may already be running
			// contravariantly, and two flips cancel.
			adjFlags ^= AssignTypeFlagsContravariant
		}

		if !e.AssignType(destTypeArg, srcTypeArg, diag, constraints, adjFlags, recursionCount) {
			isAssignable = false
		}
	}

	return isAssignable
}

// classGetItemReturnsGenericAlias corresponds to the function of the same name.
func (e *typeEvaluator) classGetItemReturnsGenericAlias(classType *ClassType) bool {
	member := LookUpClassMember(classType, "__class_getitem__",
		MemberAccessFlagsSkipInstanceMembers, nil)
	if member == nil {
		return false
	}

	memberType := e.GetTypeOfMember(member)
	functionReturnsGenericAlias := func(functionType *FunctionType) bool {
		returnType := FunctionTypeGetEffectiveReturnType(functionType, true)
		return !IsNilType(returnType) && IsClassInstance(returnType) &&
			ClassTypeIsBuiltInNamed(returnType.(*ClassType), "GenericAlias")
	}

	if IsFunction(memberType) {
		return functionReturnsGenericAlias(memberType.(*FunctionType))
	}

	if IsOverloaded(memberType) {
		// Every overload must agree, since the call site may select any of them.
		for _, overload := range OverloadedTypeGetOverloads(memberType.(*OverloadedType)) {
			if !functionReturnsGenericAlias(overload) {
				return false
			}
		}
		return true
	}

	return false
}

// rejectBareSpecialFormInTypeForm corresponds to the function of the same name.
func (e *typeEvaluator) rejectBareSpecialFormInTypeForm(
	t *ClassType, node parser.ExpressionNode,
) Type {
	for _, entry := range typeFormSpecialFormDiagnosticFactories {
		if ClassTypeIsBuiltInNamed(t, entry.ClassNames...) {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, entry.Message(), node, nil)
			return UnknownTypeCreate(false)
		}
	}

	return nil
}

// typeFormSpecialFormDiagnosticEntry is one row of the original's
// `[string | string[], () => string][]` table. The name arm is always modeled as
// a slice, since ClassTypeIsBuiltInNamed is variadic and accepts either shape.
type typeFormSpecialFormDiagnosticEntry struct {
	ClassNames []string
	Message    func() string
}

// typeFormSpecialFormDiagnosticFactories corresponds to the table of the same
// name. These are the special forms that are meaningless bare inside a TypeForm:
// each needs subscripting or a context this one does not provide.
var typeFormSpecialFormDiagnosticFactories = []typeFormSpecialFormDiagnosticEntry{
	{[]string{"Final"}, func() string { return localization.LocMessage.FinalContext() }},
	{[]string{"Optional"}, func() string { return localization.LocMessage.OptionalExtraArgs() }},
	{[]string{"Protocol"}, func() string { return localization.LocMessage.ProtocolNotAllowed() }},
	{[]string{"TypedDict"}, func() string { return localization.LocMessage.TypedDictNotAllowed() }},
	{[]string{"TypeAlias"}, func() string { return localization.LocMessage.TypeAnnotationVariable() }},
	{[]string{"Literal"}, func() string { return localization.LocMessage.LiteralNotAllowed() }},
	{[]string{"TypeGuard", "TypeIs"}, func() string { return localization.LocMessage.TypeGuardArgCount() }},
	{[]string{"Union"}, func() string { return localization.LocMessage.UnionTypeArgCount() }},
	{[]string{"Annotated"}, func() string { return localization.LocMessage.AnnotatedTypeArgMissing() }},
	{[]string{"ClassVar"}, func() string { return localization.LocMessage.ClassVarNotAllowed() }},
	{[]string{"Required"}, func() string { return localization.LocMessage.RequiredArgCount() }},
	{[]string{"NotRequired"}, func() string { return localization.LocMessage.NotRequiredArgCount() }},
	{[]string{"ReadOnly"}, func() string { return localization.LocMessage.ReadOnlyArgCount() }},
	{[]string{"Unpack"}, func() string { return localization.LocMessage.UnpackArgCount() }},
	{[]string{"Concatenate"}, func() string { return localization.LocMessage.ConcatenateContext() }},
}
