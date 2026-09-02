/*
 * checker_params.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412): the parameter
 * validation block inside visitFunction.
 *
 * Four independent checks share one pass over the parameter list, which is why
 * the original writes them inline rather than as separate methods:
 *
 *   1. Unknown or missing parameter types, reported per parameter. A parameter
 *      named `_` is exempt, and so is every parameter of an overload
 *      *implementation* -- the typing spec says the implementation's signature
 *      is not part of the function's public type, so it is allowed to stay
 *      unannotated. That exemption is why isOverloadImplementation is computed
 *      before the loop rather than tested inside it.
 *   2. A `*args: P.args` parameter followed by a named parameter, which is
 *      meaningless because P.args already absorbs every positional. The check is
 *      a running flag rather than an index comparison, because a later
 *      `**kwargs` clears it.
 *   3. An unpacked TypedDict in `**kwargs` whose keys collide with declared
 *      keyword parameters.
 *   4. A ParamSpec used with exactly one of `P.args`/`P.kwargs`. They are only
 *      meaningful as a pair, so seeing one alone is an error -- hence the
 *      `length === 1` test rather than a per-parameter check.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateFunctionParams corresponds to the parameter-checking block of
// visitFunction.
func (c *Checker) validateFunctionParams(
	node *parser.FunctionNode,
	functionTypeResult *FunctionTypeResult,
	containingClassNode parser.ParseNode,
) {
	functionType := functionTypeResult.FunctionType

	// The original's comment: track whether we have seen a *args: P.args
	// parameter. Named parameters after this need to be flagged as an error.
	sawParamSpecArgs := false

	keywordNames := map[string]bool{}
	paramDetails := GetParamListDetails(functionType, nil)

	// The original's comment: if this function is the implementation of an
	// overloaded function, its signature is ignored by the type checker (only the
	// @overload signatures define the function's type). Per the typing spec, the
	// implementation's parameters are allowed to remain unannotated, so skip the
	// unknown/missing parameter type checks for it.
	isOverloadImplementation := IsOverloaded(functionTypeResult.DecoratedType) &&
		!FunctionTypeIsOverloaded(functionType)

	// The original's comment: report any unknown or missing parameter types.
	for index, param := range node.D.Params {
		c.validateOneParam(node, param, index, functionTypeResult, paramDetails,
			isOverloadImplementation, keywordNames, &sawParamSpecArgs)
	}

	c.validateUnpackedTypedDictParams(node, functionType, paramDetails, keywordNames)
	c.validateParamSpecArgsKwargsPairing(node, functionType)

	// The original's comment: if this is a stub, ensure that the return type is
	// specified.
	if c.fileInfo.IsStubFile {
		returnAnnotation := node.D.ReturnAnnotation
		if returnAnnotation == nil && node.D.FuncAnnotationComment != nil {
			returnAnnotation = node.D.FuncAnnotationComment.D.ReturnAnnotation
		}
		if returnAnnotation == nil {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportUnknownParameterType,
				localization.LocMessage.ReturnTypeUnknown(), node.D.Name, nil)
		}
	}

	if containingClassNode != nil {
		c.validateMethod(node, functionType, containingClassNode)
	}
}

// validateOneParam is the body of the original's per-parameter forEach.
func (c *Checker) validateOneParam(
	node *parser.FunctionNode,
	param *parser.ParameterNode,
	index int,
	functionTypeResult *FunctionTypeResult,
	paramDetails *ParamListDetails,
	isOverloadImplementation bool,
	keywordNames map[string]bool,
	sawParamSpecArgs *bool,
) {
	functionType := functionTypeResult.FunctionType

	if param.D.Name != nil {
		if param.D.Category == parser.ParamCategorySimple &&
			index >= paramDetails.PositionOnlyParamCount {
			keywordNames[param.D.Name.D.Value] = true
		}

		// The original's comment: determine whether this is a P.args parameter.
		switch param.D.Category {
		case parser.ParamCategoryArgsList:
			annotationExpr := param.D.Annotation
			if annotationExpr == nil {
				annotationExpr = param.D.AnnotationComment
			}
			if memberAccess, ok := annotationExpr.(*parser.MemberAccessNode); ok &&
				memberAccess.D.Member.D.Value == "args" {
				baseType := c.evaluator.GetType(memberAccess.D.LeftExpr)
				if baseType != nil && IsParamSpec(baseType) {
					*sawParamSpecArgs = true
				}
			}
		case parser.ParamCategoryKwargsDict:
			*sawParamSpecArgs = false
		}
	}

	if param.D.Name != nil && param.D.Category == parser.ParamCategorySimple && *sawParamSpecArgs {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.NamedParamAfterParamSpecArgs().Format(param.D.Name.D.Value),
			param.D.Name, nil)
	}

	// The original's comment: allow unknown and missing param types if the param
	// is named '_'.
	if param.D.Name == nil || param.D.Name.D.Value == "_" {
		return
	}

	paramIndex := -1
	for i, p := range functionType.Shared.Parameters {
		if p.Name != nil && *p.Name == param.D.Name.D.Value {
			paramIndex = i
			break
		}
	}
	if paramIndex < 0 {
		return
	}

	functionTypeParam := functionType.Shared.Parameters[paramIndex]
	paramType := FunctionTypeGetParamType(functionType, paramIndex)

	if !isOverloadImplementation &&
		c.fileInfo.DiagnosticRuleSet.ReportUnknownParameterType != DiagnosticLevelNone {
		isUnknownParam := IsUnknown(paramType) ||
			(IsTypeVar(paramType) && paramType.(*TypeVarType).Shared.IsSynthesized &&
				!TypeVarTypeIsSelf(paramType.(*TypeVarType)))

		if isUnknownParam {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportUnknownParameterType,
				localization.LocMessage.ParamTypeUnknown().Format(param.D.Name.D.Value),
				param.D.Name, nil)
		} else if IsPartlyUnknown(paramType, 0) {
			diagAddendum := common.NewDiagnosticAddendum()
			diagAddendum.AddMessage(localization.LocAddendum.ParamType().Format(
				c.evaluator.PrintType(paramType, &PrintTypeOptions{ExpandTypeAlias: true})))
			c.evaluator.AddDiagnostic(DiagnosticRuleReportUnknownParameterType,
				localization.LocMessage.ParamTypePartiallyUnknown().Format(param.D.Name.D.Value)+
					diagAddendum.GetString(),
				param.D.Name, nil)
		}
	}

	hasAnnotation := FunctionParamIsTypeDeclared(functionTypeParam)
	if !hasAnnotation {
		// The original's comment: see if this is a "self" and "cls" parameter.
		// They are exempt from this rule.
		if IsTypeVar(paramType) && TypeVarTypeIsSelf(paramType.(*TypeVarType)) {
			hasAnnotation = true
		}
	}

	if !isOverloadImplementation && !hasAnnotation &&
		c.fileInfo.DiagnosticRuleSet.ReportMissingParameterType != DiagnosticLevelNone {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportMissingParameterType,
			localization.LocMessage.ParamAnnotationMissing().Format(param.D.Name.D.Value),
			param.D.Name, nil)
	}
}

// validateUnpackedTypedDictParams corresponds to the original's comment: verify
// that an unpacked TypedDict doesn't overlap any keyword parameters.
func (c *Checker) validateUnpackedTypedDictParams(
	node *parser.FunctionNode,
	functionType *FunctionType,
	paramDetails *ParamListDetails,
	keywordNames map[string]bool,
) {
	if !paramDetails.HasUnpackedTypedDict {
		return
	}

	kwargsIndex := len(functionType.Shared.Parameters) - 1
	kwargsType := FunctionTypeGetParamType(functionType, kwargsIndex)

	if !IsClass(kwargsType) || kwargsType.(*ClassType).Shared.TypedDictEntries == nil {
		return
	}

	knownItems := kwargsType.(*ClassType).Shared.TypedDictEntries.KnownItems
	overlappingEntries := []string{}
	for _, name := range knownItems.Keys() {
		if keywordNames[name] {
			overlappingEntries = append(overlappingEntries, name)
		}
	}

	if len(overlappingEntries) == 0 {
		return
	}

	if kwargsIndex >= len(node.D.Params) {
		// The original indexes node.d.params[kwargsIndex] directly. The synthesized
		// parameter list can be longer than the parse tree's, so Go would panic.
		return
	}

	var errorNode parser.ParseNode = node.D.Params[kwargsIndex]
	if annotation := node.D.Params[kwargsIndex].D.Annotation; annotation != nil {
		errorNode = annotation
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.OverlappingKeywordArgs().Format(
			strings.Join(overlappingEntries, ", ")),
		errorNode, nil)
}

// validateParamSpecArgsKwargsPairing corresponds to the original's comment:
// check for invalid use of ParamSpec P.args and P.kwargs. They are meaningful
// only as a pair, so exactly one of them is the error case.
func (c *Checker) validateParamSpecArgsKwargsPairing(
	node *parser.FunctionNode, functionType *FunctionType,
) {
	var paramSpecParams []FunctionParam
	for index, param := range functionType.Shared.Parameters {
		paramType := FunctionTypeGetParamType(functionType, index)
		if !FunctionParamIsTypeDeclared(param) || !IsTypeVar(paramType) || !IsParamSpec(paramType) {
			continue
		}
		if param.Category != parser.ParamCategorySimple && param.Name != nil &&
			paramType.(*TypeVarType).Priv.ParamSpecAccess != ParamSpecAccessNone {
			paramSpecParams = append(paramSpecParams, param)
		}
	}

	if len(paramSpecParams) != 1 || paramSpecParams[0].Name == nil {
		return
	}

	var paramNode *parser.ParameterNode
	for _, param := range node.D.Params {
		if param.D.Name != nil && param.D.Name.D.Value == *paramSpecParams[0].Name {
			paramNode = param
			break
		}
	}
	if paramNode == nil {
		return
	}

	annotationNode := paramNode.D.Annotation
	if annotationNode == nil {
		annotationNode = paramNode.D.AnnotationComment
	}
	if annotationNode == nil {
		return
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.ParamSpecArgsKwargsUsage(), annotationNode, nil)
}
