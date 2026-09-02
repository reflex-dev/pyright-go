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
	functionNode *parser.FunctionNode,
	functionFlags FunctionTypeFlags,
	paramIndex int,
	containingClassType *ClassType,
) Type {
	// The original's comment: is the function a method within a class? If so, see
	// if a base class defines the same method and provides annotations.
	if containingClassType != nil {
		if paramIndex == 0 && (functionFlags&FunctionTypeFlagsStaticMethod) == 0 {
			hasClsParam := (functionFlags &
				(FunctionTypeFlagsClassMethod | FunctionTypeFlagsConstructorMethod)) != 0
			return SynthesizeTypeVarForSelfCls(containingClassType, hasClsParam)
		}

		if inherited := e.inferParamTypeFromBaseClass(
			functionNode, paramIndex, containingClassType); inherited != nil {
			return inherited
		}
	}

	// The original's comment: if the parameter has a default argument value, we
	// may be able to infer its type from this information.
	if paramValueExpr := functionNode.D.Params[paramIndex].D.DefaultValue; paramValueExpr != nil {
		return e.inferParamTypeFromDefaultValue(paramValueExpr)
	}

	return nil
}

// inferParamTypeFromBaseClass is the original's lookUpClassMember block: an
// unannotated parameter of an overriding method inherits the base method's
// annotation.
//
// The signature must match EXACTLY apart from annotations -- same parameter
// count, same names, same categories. Anything less and the positions would not
// correspond, so copying an annotation across would be a guess rather than an
// inference.
func (e *typeEvaluator) inferParamTypeFromBaseClass(
	functionNode *parser.FunctionNode, paramIndex int, containingClassType *ClassType,
) Type {
	methodName := functionNode.D.Name.D.Value

	baseClassMemberInfo := LookUpClassMember(containingClassType, methodName,
		MemberAccessFlagsSkipOriginalClass, nil)
	if baseClassMemberInfo == nil {
		return nil
	}

	memberDecls := baseClassMemberInfo.Symbol.GetDeclarations()
	if len(memberDecls) != 1 {
		return nil
	}
	funcDecl, ok := memberDecls[0].(*FunctionDeclaration)
	if !ok {
		return nil
	}
	baseClassMethodNode, ok := funcDecl.Node.(*parser.FunctionNode)
	if !ok {
		return nil
	}

	// The original's comment: does the signature match exactly with the exception
	// of annotations?
	if len(baseClassMethodNode.D.Params) != len(functionNode.D.Params) {
		return nil
	}
	for index, param := range baseClassMethodNode.D.Params {
		overrideParam := functionNode.D.Params[index]
		if paramNameValue(overrideParam) != paramNameValue(param) ||
			overrideParam.D.Category != param.D.Category {
			return nil
		}
	}

	baseClassParam := baseClassMethodNode.D.Params[paramIndex]
	baseClassParamAnnotation := baseClassParam.D.Annotation
	if baseClassParamAnnotation == nil {
		baseClassParamAnnotation = baseClassParam.D.AnnotationComment
	}
	if baseClassParamAnnotation == nil {
		return nil
	}

	inferredParamType := e.getTypeOfParamAnnotation(
		baseClassParamAnnotation, functionNode.D.Params[paramIndex].D.Category)

	// The original's comment: if the parameter type is generic, specialize it in
	// the context of the child class.
	if RequiresSpecialization(inferredParamType, nil, 0) && IsClass(baseClassMemberInfo.ClassType) {
		memberClass := baseClassMemberInfo.ClassType.(*ClassType)
		scopeIds := GetTypeVarScopeIds(memberClass)
		solution := BuildSolutionFromSpecializedClass(memberClass)

		scopeIds = append(scopeIds, GetScopeIdForNode(baseClassMethodNode))

		// The original's comment: replace any unsolved TypeVars with Unknown
		// (including all function-scoped TypeVars).
		inferredParamType = ApplySolvedTypeVars(inferredParamType, solution, &ApplyTypeVarOptions{
			ReplaceUnsolved: &ReplaceUnsolvedOptions{
				ScopeIDs:       scopeIds,
				TupleClassType: e.GetTupleClassType(),
			},
		})
	}

	// An inferred type crossing a py.typed boundary is marked ambiguous: the
	// package's authors did not declare it, so a consumer should not rely on it.
	fileInfo := GetFileInfo(functionNode)
	if fileInfo.IsInPyTypedPackage && !fileInfo.IsStubFile {
		inferredParamType = CloneForAmbiguousType(inferredParamType)
	}

	return inferredParamType
}

// paramNameValue is the original's `param.d.name?.d.value`, which is undefined
// for a bare `*` or `/` separator.
func paramNameValue(param *parser.ParameterNode) string {
	if param.D.Name == nil {
		return ""
	}
	return param.D.Name.D.Value
}

// inferParamTypeFromDefaultValue corresponds to the function of the same name,
// which reads a parameter's type from its default -- with None widened to
// Optional[Unknown] rather than taken literally.
func (e *typeEvaluator) inferParamTypeFromDefaultValue(_ parser.ExpressionNode) Type {
	e.unported("inferParamTypeFromDefaultValue")
	return nil
}
