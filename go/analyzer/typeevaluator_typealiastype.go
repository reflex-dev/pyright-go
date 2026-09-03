/*
 * typeevaluator_typealiastype.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * createTypeAliasType, getTypeOfTypeForm and validateCallForClassInstance.
 *
 * createTypeAliasType handles `X = TypeAlias("X", int)`, the PEP 695 explicit
 * form spelled as a call. Almost all of it is validation, because the call has
 * constraints the type system cannot express: it must be directly assigned to a
 * simple name, that name must match the string passed as the first argument, and
 * it must appear at class, module or builtin scope. Those are runtime
 * requirements -- `TypeAliasType` records the name it was given and the runtime
 * looks it up -- so a mismatch is a real bug rather than a style issue.
 *
 * The type parameters are rescoped rather than used as found. A TypeVar written
 * in the `type_params=` tuple has no scope of its own yet; binding it to the
 * alias's scope is what makes `T` inside the value expression refer to *this*
 * alias's parameter. A TypeVar that already has a scope is rejected, because
 * that would mean reusing a parameter belonging to something else.
 *
 * getTypeOfTypeForm evaluates `TypeForm(int)`. The argument is a type
 * *expression* rather than a value, so it is evaluated with typeFormArg and the
 * result is rewrapped as `TypeForm[int]` only when evaluation produced no
 * errors -- an errored argument keeps its own result so the diagnostic survives.
 *
 * validateCallForClassInstance is calling an *object*: `x()` where x is an
 * instance resolves through `__call__`. The final special case is worth naming:
 * calling a `type[T]` is presumed to instantiate a T, so the return type is
 * taken from the unexpanded callee rather than from `__call__`'s declared
 * return, which would only say `object`.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// createTypeAliasType corresponds to the function of the same name. It returns
// nil where the original returns undefined.
func (e *typeEvaluator) createTypeAliasType(errorNode parser.ExpressionNode, argList []*Arg) Type {
	callNode, isCall := errorNode.(*parser.CallNode)
	if !isCall || callNode.NodeBase().Parent == nil || len(argList) < 2 {
		return nil
	}

	parentNode := callNode.NodeBase().Parent
	assignment, isAssignment := parentNode.(*parser.AssignmentNode)
	if !isAssignment || assignment.D.RightExpr != parser.ExpressionNode(callNode) ||
		assignment.D.LeftExpr.GetNodeType() != parser.ParseNodeTypeName {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeAliasTypeMustBeAssigned(), errorNode, nil)
		return nil
	}

	if scope := GetScopeForNode(errorNode); scope != nil {
		if scope.Type != ScopeTypeClass && scope.Type != ScopeTypeModule && scope.Type != ScopeTypeBuiltin {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeAliasTypeBadScope(), assignment.D.LeftExpr, nil)
		}
	}

	nameNode := assignment.D.LeftExpr.(*parser.NameNode)

	firstArg := argList[0]
	if firstArg.ValueExpression != nil &&
		firstArg.ValueExpression.GetNodeType() == parser.ParseNodeTypeStringList {
		typeAliasName := joinStringListValue(firstArg.ValueExpression.(*parser.StringListNode))
		if typeAliasName != nameNode.D.Value {
			// The runtime records the name given here, so a mismatch is a real
			// inconsistency rather than a naming preference.
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeAliasTypeNameMismatch(), firstArg.ValueExpression, nil)
		}
	} else {
		var node parser.ExpressionNode = errorNode
		if firstArg.ValueExpression != nil {
			node = firstArg.ValueExpression
		}
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeAliasTypeNameArg(), node, nil)
		return nil
	}

	var valueExpr parser.ExpressionNode
	var typeParamsExpr parser.ExpressionNode

	// The original's comment: parse the remaining parameters.
	for i := 1; i < len(argList); i++ {
		paramName := ""
		if argList[i].Name != nil {
			paramName = argList[i].Name.D.Value
		}

		if paramName != "" {
			if paramName == "type_params" && typeParamsExpr == nil {
				typeParamsExpr = argList[i].ValueExpression
			} else if paramName == "value" && valueExpr == nil {
				valueExpr = argList[i].ValueExpression
			} else {
				return nil
			}
		} else if i == 1 {
			valueExpr = argList[i].ValueExpression
		} else {
			return nil
		}
	}

	// The original's comment: the value expression is not optional, so bail if
	// it's not present.
	if valueExpr == nil {
		return nil
	}

	var typeParams []*TypeVarType
	hasTypeParams := false
	if typeParamsExpr != nil {
		var ok bool
		typeParams, ok = e.typeAliasTypeParams(typeParamsExpr, nameNode)
		if !ok {
			return nil
		}
		hasTypeParams = true
	}

	return e.getTypeOfTypeAliasCommon(nameNode, nameNode, valueExpr, false, nil,
		func() []*TypeVarType {
			if !hasTypeParams {
				return nil
			}
			return typeParams
		})
}

// typeAliasTypeParams is the original's `if (typeParamsExpr)` block. The second
// result is false where the original returns undefined after reporting.
func (e *typeEvaluator) typeAliasTypeParams(
	typeParamsExpr parser.ExpressionNode, nameNode *parser.NameNode,
) ([]*TypeVarType, bool) {
	tupleNode, isTuple := typeParamsExpr.(*parser.TupleNode)
	if !isTuple {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeAliasTypeParamInvalid(), typeParamsExpr, nil)
		return nil, false
	}

	typeParams := []*TypeVarType{}
	isTypeParamListValid := true

	for _, expr := range tupleNode.D.Items {
		entryType := e.GetTypeOfExpression(expr,
			EvalFlagsInstantiableType|EvalFlagsAllowTypeVarWithoutScopeId, nil).Type

		if !IsTypeVar(entryType) {
			isTypeParamListValid = false
			continue
		}
		typeVar := entryType.(*TypeVarType)

		// A TypeVar that already carries a scope belongs to something else, and an
		// unpacked TypeVarTuple cannot be a bare alias parameter.
		if typeVar.Priv.ScopeID != "" || (IsTypeVarTuple(typeVar) && typeVar.Priv.IsUnpacked) {
			isTypeParamListValid = false
			typeParams = append(typeParams, typeVar)
			continue
		}

		scopeType := TypeVarScopeTypeTypeAlias
		typeParams = append(typeParams, TypeVarTypeCloneForScopeID(
			typeVar, GetScopeIdForNode(nameNode), &nameNode.D.Value, &scopeType))
	}

	if !isTypeParamListValid {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeAliasTypeParamInvalid(), typeParamsExpr, nil)
		return nil, false
	}

	return typeParams, true
}

// getTypeOfTypeForm corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfTypeForm(node *parser.CallNode, typeFormClass *ClassType) *TypeResult {
	if len(node.D.Args) != 1 || node.D.Args[0].D.ArgCategory != parser.ArgCategorySimple ||
		node.D.Args[0].D.Name != nil {
		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.TypeFormArgs(), node, nil)
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	typeFormResult := e.getTypeOfArgExpectingType(e.ConvertNodeToArg(node.D.Args[0]),
		&ExpectedTypeOptions{
			TypeFormArg:           true,
			NoNonTypeSpecialForms: true,
			TypeExpression:        true,
		})

	// An argument that failed to evaluate keeps its own result so the diagnostic
	// already recorded against it survives rather than being replaced.
	if !typeFormResult.TypeErrors {
		if base := typeFormResult.Type.Base(); base.Props != nil && base.Props.TypeForm != nil {
			tf := base.Props.TypeForm
			typeFormResult.Type = ConvertToInstance(ClassTypeSpecialize(
				typeFormClass, []Type{ConvertToInstance(tf, true)}, nil, false, nil, nil), true)
		}
	}

	return typeFormResult
}

// validateCallForClassInstance corresponds to the function of the same name:
// calling an object routes through its `__call__`.
func (e *typeEvaluator) validateCallForClassInstance(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	expandedCallType *ClassType,
	unexpandedCallType Type,
	constraints *ConstraintTracker,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
	recursionCount int,
) *CallResult {
	callDiag := common.NewDiagnosticAddendum()
	callMethodResult := e.getTypeOfBoundMember(
		errorNode,
		expandedCallType,
		"__call__",
		nil,
		callDiag,
		MemberAccessFlagsSkipInstanceMembers|MemberAccessFlagsSkipAttributeAccessOverride,
		nil,
		recursionCount,
	)

	if callMethodResult == nil || IsNilType(callMethodResult.Type) || callMethodResult.TypeErrors {
		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.ObjectNotCallable().Format(e.PrintType(expandedCallType, nil))+
				callDiag.GetString(),
			errorNode, nil)

		return &CallResult{ReturnType: UnknownTypeCreate(false), ArgumentErrors: true}
	}

	callResult := e.validateCallArgs(errorNode, argList,
		&TypeResult{Type: callMethodResult.Type}, constraints, skipUnknownArgCheck,
		inferenceContext, recursionCount)

	returnType := callResult.ReturnType
	if IsNilType(returnType) {
		returnType = UnknownTypeCreate(false)
	}

	if IsTypeVar(unexpandedCallType) && unexpandedCallType.Base().IsInstantiable() &&
		IsClass(expandedCallType) && ClassTypeIsBuiltInNamed(expandedCallType, "type") {
		// The original's comment: handle the case where a type[T] is being called.
		// We presume this will instantiate an object of type T. `__call__` on
		// `type` only promises `object`, which would lose T.
		returnType = ConvertToInstance(unexpandedCallType, true)
	}

	return &CallResult{
		ReturnType:           returnType,
		ArgumentErrors:       callResult.ArgumentErrors,
		OverloadsUsedForCall: callResult.OverloadsUsedForCall,
	}
}
