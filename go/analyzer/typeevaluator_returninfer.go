/*
 * typeevaluator_returninfer.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * _getInferredReturnTypeResult and disableSpeculativeMode.
 *
 * What a `def` with no return annotation returns. The answer is cached on the
 * FunctionType itself rather than in the evaluator's node cache, because a
 * FunctionType outlives any one expression and the inference is expensive.
 *
 * Four kinds of function decline inference outright and answer Unknown: a stub
 * definition (there is no body to look at), a ParamSpec value, an unsynthesized
 * overload (the overloads carry the signatures, not the implementation), and --
 * further down -- a function whose file has analyzeUnannotatedFunctions off.
 *
 * Two guards bound the cost, and both are real limits rather than tuning knobs.
 * A function whose parameters are all unannotated and whose code flow complexity
 * exceeds 32 is not inferred at all, because inference there means walking a
 * large graph with nothing to constrain it. And a function that has been
 * evaluated more than 8 times without settling is answered Unknown, which is how
 * mutual recursion between two inferred return types terminates.
 *
 * Speculative mode is disabled around the inference. A speculative evaluation
 * discards its cache entries when it unwinds; the inferred return type is stored
 * on the shared FunctionType and must survive, so the tracker is emptied for the
 * duration and restored afterwards.
 *
 * The final block is the call-site refinement: if the inferred type is partly
 * Unknown and the function has unannotated parameters, the argument types at the
 * call site can do better. It is skipped for decorated and async functions,
 * because the decorator or the coroutine wrapper would have to be re-applied to
 * whatever came back.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// getInferredReturnTypeResult corresponds to _getInferredReturnTypeResult. The
// original wraps it with wrapWithLogger, which is a no-op unless logging is on.
func (e *typeEvaluator) getInferredReturnTypeResult(
	t *FunctionType, callSiteInfo *CallSiteEvaluationInfo,
) *TypeResult {
	// The original's comment: don't attempt to infer the return type for a stub
	// file.
	if FunctionTypeIsStubDefinition(t) {
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	// The original's comment: don't infer the return type for a ParamSpec value.
	if FunctionTypeIsParamSpecValue(t) {
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	// The original's comment: don't infer the return type for an overloaded
	// function (unless it's synthesized, which is needed for proper operation of
	// the __get__ method in properties).
	if FunctionTypeIsOverloaded(t) && !FunctionTypeIsSynthesizedMethod(t) {
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	var returnType Type
	isIncomplete := false

	evalCount := 0
	if t.Shared.InferredReturnType != nil {
		evalCount = t.Shared.InferredReturnType.EvaluationCount
	}

	switch {
	case t.Shared.InferredReturnType != nil && !t.Shared.InferredReturnType.IsIncomplete:
		// The original's comment: if the return type has already been lazily
		// evaluated, don't bother computing it again.
		returnType = t.Shared.InferredReturnType.Type

	case evalCount > maxReturnTypeInferenceAttempts:
		// The original's comment: detect a case where a return type won't converge
		// because of recursion.
		returnType = UnknownTypeCreate(false)

	default:
		returnType, isIncomplete = e.inferReturnTypeFromBody(t, callSiteInfo)

		if returnType == nil {
			returnType = UnknownTypeCreate(false)
		}

		// The original's comment: externalize any TypeVars that appear in the type.
		//
		// The function's own scope and its method class's scope: a TypeVar bound
		// within the body must be free again in the type the caller sees.
		var typeVarScopes []TypeVarScopeId
		if t.Shared.TypeVarScopeID != "" {
			typeVarScopes = append(typeVarScopes, t.Shared.TypeVarScopeID)
		}
		if t.Shared.MethodClass != nil && t.Shared.MethodClass.Shared.TypeVarScopeID != "" {
			typeVarScopes = append(typeVarScopes, t.Shared.MethodClass.Shared.TypeVarScopeID)
		}
		returnType = MakeTypeVarsFree(returnType, typeVarScopes)

		// The original's comment: cache the type for next time.
		t.Shared.InferredReturnType = &InferredReturnTypeInfo{
			Type: returnType, IsIncomplete: isIncomplete, EvaluationCount: evalCount + 1,
		}
	}

	if refined := e.refineReturnTypeFromCallSite(t, returnType, isIncomplete, callSiteInfo); refined != nil {
		returnType = refined
	}

	return &TypeResult{Type: returnType, IsIncomplete: isIncomplete}
}

// inferReturnTypeFromBody is the original's else branch: walk the function body.
// A nil type means inference was declined or produced nothing.
func (e *typeEvaluator) inferReturnTypeFromBody(
	t *FunctionType, callSiteInfo *CallSiteEvaluationInfo,
) (Type, bool) {
	// The original's comment: don't bother inferring the return type of __init__
	// because it's always None.
	if FunctionTypeIsInstanceMethod(t) && t.Shared.Name == "__init__" {
		return e.GetNoneType(), false
	}

	if t.Shared.Declaration == nil {
		return nil, false
	}

	functionNode, ok := t.Shared.Declaration.Node.(*parser.FunctionNode)
	if !ok {
		return nil, false
	}

	skipUnannotatedFunction := !GetFileInfo(functionNode).DiagnosticRuleSet.AnalyzeUnannotatedFunctions &&
		IsUnannotatedFunction(functionNode)

	// The original's comment: skip return type inference if we are in "skip
	// unannotated function" mode.
	if skipUnannotatedFunction || e.checkCodeFlowTooComplex(functionNode.D.Suite) {
		return nil, false
	}

	codeFlowComplexity := GetCodeFlowComplexity(functionNode)

	// The original's comment: for very complex functions that have no annotated
	// parameter types, don't attempt to infer the return type because it can be
	// extremely expensive.
	parametersAreAnnotated := len(t.Shared.Parameters) <= 1
	if !parametersAreAnnotated {
		for _, param := range t.Shared.Parameters {
			if FunctionParamIsTypeDeclared(param) {
				parametersAreAnnotated = true
				break
			}
		}
	}

	if !parametersAreAnnotated && codeFlowComplexity >= maxReturnTypeInferenceCodeFlowComplexity {
		return nil, false
	}

	// The original's comment: temporarily disable speculative mode while we
	// lazily evaluate the return type.
	var returnTypeResult *TypeResult
	e.disableSpeculativeMode(func() {
		var errorNode parser.ExpressionNode
		if callSiteInfo != nil {
			errorNode = callSiteInfo.ErrorNode
		}
		returnTypeResult = e.inferFunctionReturnType(
			functionNode, FunctionTypeIsAbstractMethod(t), errorNode)
	})

	if returnTypeResult == nil {
		return nil, false
	}
	return returnTypeResult.Type, returnTypeResult.IsIncomplete
}

// refineReturnTypeFromCallSite is the original's final block. It returns nil when
// the refinement does not apply or produced nothing.
//
// The original's comment: if the type is partially unknown and the function has
// one or more unannotated params, try to analyze the function with the provided
// argument types and attempt to do a better job at inference.
func (e *typeEvaluator) refineReturnTypeFromCallSite(
	t *FunctionType, returnType Type, isIncomplete bool, callSiteInfo *CallSiteEvaluationInfo,
) Type {
	// analyzeUnannotatedFunctions is a local `const ... = true` in the original,
	// so this arm of its condition is always taken.
	if isIncomplete || callSiteInfo == nil {
		return nil
	}

	if !IsPartlyUnknown(returnType, 0) || !FunctionTypeHasUnannotatedParams(t) ||
		FunctionTypeIsStubDefinition(t) || FunctionTypeIsPyTypedDefinition(t) {
		return nil
	}

	hasDecorators := false
	isAsync := false
	var declNode *parser.FunctionNode
	if t.Shared.Declaration != nil {
		declNode, _ = t.Shared.Declaration.Node.(*parser.FunctionNode)
	}
	if declNode != nil {
		hasDecorators = len(declNode.D.Decorators) > 0
		isAsync = declNode.D.IsAsync
	}

	// The original's comment: we can't use this technique if decorators or async
	// are used because they would need to be applied to the inferred return type.
	if hasDecorators || isAsync {
		return nil
	}

	contextualReturnType := e.inferReturnTypeForCallSite(t, callSiteInfo)
	if contextualReturnType == nil {
		return nil
	}

	if declNode != nil {
		// The original's comment: externalize any TypeVars that appear in the type.
		contextualReturnType = MakeTypeVarsFree(contextualReturnType, GetTypeVarScopesForNode(declNode))
	}

	return contextualReturnType
}

// disableSpeculativeMode corresponds to the function of the same name. The
// original uses try/catch rather than try/finally, with a comment saying the
// TypeScript debugger handles finally poorly when single stepping.
func (e *typeEvaluator) disableSpeculativeMode(callback func()) {
	stack := e.speculativeTypeTracker.DisableSpeculativeMode()
	defer e.speculativeTypeTracker.EnableSpeculativeMode(stack)
	callback()
}

/*
 * The two things this reaches.
 */

// inferFunctionReturnType corresponds to the function of the same name, which
// unions the types of every reachable return statement, folding in a generator's
// yields and an implicit None where the end of the body is reachable.
func (e *typeEvaluator) inferFunctionReturnType(
	_ *parser.FunctionNode, _ bool, _ parser.ExpressionNode,
) *TypeResult {
	e.unported("inferFunctionReturnType")
	return nil
}

// inferReturnTypeForCallSite corresponds to the function of the same name, which
// re-analyzes the function body with the call site's argument types substituted
// for its unannotated parameters.
func (e *typeEvaluator) inferReturnTypeForCallSite(
	_ *FunctionType, _ *CallSiteEvaluationInfo,
) Type {
	e.unported("inferReturnTypeForCallSite")
	return nil
}
