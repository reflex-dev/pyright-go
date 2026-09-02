/*
 * typeevaluator_callsiteinfer.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * inferReturnTypeForCallSite.
 *
 * This re-infers an unannotated function's return type *using the argument types
 * at one particular call site*, which is how `def f(x): return x + 1` can yield
 * `int` at one call and `str` at another. It is expensive -- it re-analyzes the
 * whole function body -- so almost half the function is bail-out conditions, and
 * each one guards a different failure mode:
 *
 *   - code-flow complexity, argument count and stack depth are cost bounds;
 *   - an argument not matched to a named parameter means an unpacked value
 *     spanning several parameters, which this cannot model;
 *   - a function already on the inference stack means recursion, direct or
 *     mutual, which would not terminate.
 *
 * The temporary type cache is the load-bearing part. Parameters are written with
 * their call-site types and the body is re-analyzed, but those types are *only*
 * true for this call, so they must not leak into the main cache where other call
 * sites would see them. Swapping the cache out and restoring it in a deferred
 * block is what keeps the two separate.
 *
 * Literal argument types are stripped when the call is inside a loop, for the
 * same reason literal math is not evaluated in loops: the value on the second
 * iteration is not the value the literal records, so keeping it would produce a
 * type that is wrong for every iteration but the first.
 *
 * The per-function result cache is keyed on the *parameter type tuple*, not the
 * call node, so two syntactically different calls with the same argument types
 * share an entry. It is capped and evicted oldest-first.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// inferReturnTypeForCallSite corresponds to the function of the same name. It
// returns nil where the original returns undefined.
func (e *typeEvaluator) inferReturnTypeForCallSite(
	t *FunctionType, callSiteInfo *CallSiteEvaluationInfo,
) Type {
	args := callSiteInfo.Args
	var contextualReturnType Type

	if t.Shared.Declaration == nil {
		return nil
	}
	functionNode, ok := t.Shared.Declaration.DeclBase().Node.(*parser.FunctionNode)
	if !ok {
		return nil
	}

	if GetCodeFlowComplexity(functionNode) >= maxReturnCallSiteTypeInferenceCodeFlowComplexity {
		return nil
	}

	// The original's comment: if an arg hasn't been matched to a specific named
	// parameter, it's an unpacked value that corresponds to multiple parameters.
	// That's an edge case that we don't handle here.
	for _, arg := range args {
		if arg.ParamName == "" {
			return nil
		}
	}

	// The original's comment: detect recurrence. If a function invokes itself
	// either directly or indirectly, we won't attempt to infer contextual return
	// types any further.
	for _, context := range e.returnTypeInferenceContextStack {
		if context.FunctionNode == functionNode {
			return nil
		}
	}

	functionTypeResult := e.GetTypeOfFunction(functionNode)
	if functionTypeResult == nil {
		return nil
	}

	// The original's comment: very complex functions with many arguments can take
	// a long time to analyze, so we'll use a heuristic and avoiding this inference
	// technique for any call site that involves too many arguments.
	if len(args) > maxReturnTypeInferenceArgCount {
		return nil
	}

	// The original's comment: don't explore arbitrarily deep in the call graph.
	if len(e.returnTypeInferenceContextStack) >= maxReturnTypeInferenceStackSize {
		return nil
	}

	paramTypes := []Type{}
	isResultFromCache := false

	// The original's comment: if the call is located in a loop, don't use literal
	// argument types for the same reason we don't do literal math in loops.
	stripLiteralArgTypes := IsWithinLoop(callSiteInfo.ErrorNode)

	// The original's comment: suppress diagnostics because we don't want to
	// generate errors.
	e.suppressDiagnostics(functionNode, func() {
		// The original's comment: allocate a new temporary type cache for the
		// context of just this function so we can analyze it separately without
		// polluting the main type cache.
		prevTypeCache := e.returnTypeInferenceTypeCache
		prevTypeFormTypeCache := e.returnTypeInferenceTypeFormTypeCache
		e.returnTypeInferenceContextStack = append(e.returnTypeInferenceContextStack,
			&ReturnTypeInferenceContext{
				FunctionNode:     functionNode,
				CodeFlowAnalyzer: e.createCodeFlowAnalyzer(),
			})

		defer func() {
			e.returnTypeInferenceContextStack =
				e.returnTypeInferenceContextStack[:len(e.returnTypeInferenceContextStack)-1]
			e.returnTypeInferenceTypeCache = prevTypeCache
			e.returnTypeInferenceTypeFormTypeCache = prevTypeFormTypeCache
		}()

		e.returnTypeInferenceTypeCache = newTypeCacheMap()
		e.returnTypeInferenceTypeFormTypeCache =
			common.NewOrderedMap[int, []*TypeFormTypeCacheEntry]()

		allArgTypesAreUnknown := e.writeCallSiteParamTypes(
			functionNode, functionTypeResult, args, stripLiteralArgTypes, &paramTypes)

		// The original's comment: don't bother trying to determine the contextual
		// return type if none of the argument types are known.
		if allArgTypesAreUnknown {
			return
		}

		// The original's comment: see if the return type is already cached. If so,
		// skip the inference step, which is potentially very expensive.
		if cached := findCallSiteCacheEntry(functionTypeResult.FunctionType, paramTypes); cached != nil {
			contextualReturnType = cached.ReturnType
			isResultFromCache = true
			return
		}

		if result := e.inferFunctionReturnType(functionNode,
			FunctionTypeIsAbstractMethod(t), callSiteInfo.ErrorNode); result != nil {
			contextualReturnType = result.Type
		}
	}, nil)

	if IsNilType(contextualReturnType) {
		return nil
	}

	contextualReturnType = RemoveUnbound(contextualReturnType)

	if !isResultFromCache {
		// The original's comment: cache the resulting type.
		cache := functionTypeResult.FunctionType.Priv.CallSiteReturnTypeCache
		if len(cache) >= maxCallSiteReturnTypeCacheSize {
			cache = cache[1:]
		}
		functionTypeResult.FunctionType.Priv.CallSiteReturnTypeCache = append(cache,
			&CallSiteInferenceTypeCacheEntry{
				ParamTypes: paramTypes,
				ReturnType: contextualReturnType,
			})
	}

	return contextualReturnType
}

// writeCallSiteParamTypes is the original's params.forEach: it writes each
// parameter's call-site type into the temporary cache and collects them. It
// reports whether every argument type came out Unknown.
func (e *typeEvaluator) writeCallSiteParamTypes(
	functionNode *parser.FunctionNode,
	functionTypeResult *FunctionTypeResult,
	args []*ValidateArgTypeParams,
	stripLiteralArgTypes bool,
	paramTypes *[]Type,
) bool {
	allArgTypesAreUnknown := true

	for index, param := range functionNode.D.Params {
		if param.D.Name == nil {
			continue
		}

		var paramType Type

		var matchingArg *ValidateArgTypeParams
		for _, arg := range args {
			if param.D.Name.D.Value == arg.ParamName {
				matchingArg = arg
				break
			}
		}

		switch {
		case matchingArg != nil && matchingArg.Argument != nil &&
			matchingArg.Argument.ValueExpression != nil:
			paramType = e.GetTypeOfExpression(
				matchingArg.Argument.ValueExpression, EvalFlagsNone, nil).Type
			if !IsUnknown(paramType) {
				allArgTypesAreUnknown = false
			}

		case param.D.DefaultValue != nil:
			paramType = e.GetTypeOfExpression(param.D.DefaultValue, EvalFlagsNone, nil).Type
			if !IsUnknown(paramType) {
				allArgTypesAreUnknown = false
			}

		case index == 0:
			// The original's comment: if this is an instance or class method, use
			// the implied parameter type for the "self" or "cls" parameter.
			if FunctionTypeIsInstanceMethod(functionTypeResult.FunctionType) ||
				FunctionTypeIsClassMethod(functionTypeResult.FunctionType) {
				if len(functionTypeResult.FunctionType.Shared.Parameters) > 0 &&
					functionNode.D.Params[0].D.Name != nil {
					paramType = FunctionTypeGetParamType(functionTypeResult.FunctionType, 0)
				}
			}
		}

		if IsNilType(paramType) {
			paramType = UnknownTypeCreate(false)
		}

		if stripLiteralArgTypes {
			paramType = StripTypeForm(e.convertSpecialFormToRuntimeValueEx(
				e.StripLiteralValue(paramType), EvalFlagsNone, true))
		}

		*paramTypes = append(*paramTypes, paramType)
		e.writeTypeCache(param.D.Name, &TypeResult{Type: paramType}, evalFlagsNonePtr(), nil, false)
	}

	return allArgTypesAreUnknown
}

// findCallSiteCacheEntry is the original's `callSiteReturnTypeCache?.find(...)`.
// The key is the parameter type tuple, so two textually different calls with the
// same argument types share an entry.
func findCallSiteCacheEntry(
	functionType *FunctionType, paramTypes []Type,
) *CallSiteInferenceTypeCacheEntry {
	for _, entry := range functionType.Priv.CallSiteReturnTypeCache {
		if len(entry.ParamTypes) != len(paramTypes) {
			continue
		}
		matches := true
		for i, t := range entry.ParamTypes {
			if !IsTypeSame(t, paramTypes[i], TypeSameOptions{}, 0) {
				matches = false
				break
			}
		}
		if matches {
			return entry
		}
	}
	return nil
}
