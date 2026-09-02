/*
 * typeevaluator_param.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * evaluateTypeOfParam.
 *
 * A parameter's type comes from one of four places, tried in order: its
 * annotation, the pseudo-generic type parameter synthesized for an unannotated
 * __init__, a same-named method in a base class, or nothing. The original's
 * comment explains why lambdas are handled separately -- a lambda parameter
 * never has an annotation but can be inferred from context, while a function
 * parameter sometimes has one but cannot be inferred that way -- so a lambda
 * short-circuits to the contextual evaluator before any of this runs.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// EvaluateTypeOfParam corresponds to evaluateTypeOfParam.
func (e *typeEvaluator) EvaluateTypeOfParam(node *parser.ParameterNode) {
	// The original's comment: if this parameter has no name, we have nothing to
	// do.
	if node.D.Name == nil {
		return
	}

	// The original's comment: we need to handle lambdas differently from
	// functions because the former never have parameter type annotations but can
	// be inferred, whereas the latter sometimes have type annotations but cannot
	// be inferred.
	parent := node.NodeBase().Parent
	if lambda, ok := parent.(*parser.LambdaNode); ok {
		e.evaluateTypesForExpressionInContext(lambda)
		return
	}

	functionNode, ok := parent.(*parser.FunctionNode)
	if !ok {
		// The original asserts the parent is a Function.
		common.Fail("Parameter node has no Function or Lambda parent")
		return
	}

	paramIndex := -1
	for i, param := range functionNode.D.Params {
		if param == node {
			paramIndex = i
			break
		}
	}

	typeAnnotation := GetTypeAnnotationForParam(functionNode, paramIndex)

	if typeAnnotation != nil {
		param := functionNode.D.Params[paramIndex]
		annotatedType := e.getTypeOfParamAnnotation(typeAnnotation, param.D.Category)

		liveTypeVarScopes := GetTypeVarScopesForNode(param)
		annotatedType = MakeTypeVarsBound(annotatedType, liveTypeVarScopes, true)

		adjType := e.transformVariadicParamType(
			node,
			node.D.Category,
			e.adjustParamAnnotatedType(param, annotatedType),
		)

		e.writeTypeCache(node.D.Name, &TypeResult{Type: adjType}, evalFlagsNonePtr(), nil, false)
		return
	}

	containingClassNode := GetEnclosingClass(functionNode, true)
	var classInfo *ClassTypeResult
	if containingClassNode != nil {
		classInfo = e.GetTypeOfClass(containingClassNode)
	}

	if classInfo != nil && classInfo.ClassType != nil &&
		ClassTypeIsPseudoGenericClass(classInfo.ClassType) &&
		functionNode.D.Name.D.Value == "__init__" {
		typeParamName := getPseudoGenericTypeVarName(node.D.Name.D.Value)

		for _, param := range classInfo.ClassType.Shared.TypeParams {
			if param.Shared.Name == typeParamName {
				e.writeTypeCache(
					node.D.Name,
					&TypeResult{Type: TypeVarTypeCloneAsBound(param)},
					evalFlagsNonePtr(),
					nil,
					false,
				)
				return
			}
		}
	}

	// The original's comment: see if the function is a method in a child class.
	// We may be able to infer the type of the parameter from a method of the
	// same name in a parent class if it has an annotated type.
	//
	// Note that the original passes isInClass=true unconditionally here, even
	// when containingClassNode is undefined -- unlike getTypeOfFunctionPredecorated,
	// which passes `!!containingClassNode`. Preserved as written.
	functionFlags := e.getFunctionInfoFromDecorators(functionNode, true).Flags

	var containingClassType *ClassType
	if classInfo != nil {
		containingClassType = classInfo.ClassType
	}

	inferredParamType := e.inferParamType(functionNode, functionFlags, paramIndex, containingClassType)
	if inferredParamType == nil {
		inferredParamType = UnknownTypeCreate(false)
	}

	liveTypeVarScopes := GetTypeVarScopesForNode(node)
	inferredParamType = MakeTypeVarsBound(inferredParamType, liveTypeVarScopes, true)

	e.writeTypeCache(
		node.D.Name,
		&TypeResult{Type: e.transformVariadicParamType(node, node.D.Category, inferredParamType)},
		evalFlagsNonePtr(),
		nil,
		false,
	)
}

// inferParamType corresponds to the function of the same name. The original's
// comment: attempts to infer an unannotated parameter type from available
// context -- a synthesized Self/cls TypeVar for the first parameter of a method,
// or the annotated type of the same parameter in a base class method.
func (e *typeEvaluator) inferParamType(
	_ *parser.FunctionNode,
	_ FunctionTypeFlags,
	_ int,
	_ *ClassType,
) Type {
	e.unported("inferParamType")
	return nil
}
