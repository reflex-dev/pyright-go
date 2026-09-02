/*
 * typeevaluator_lambda.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfLambda, getTypeOfLambdaWithExpectedType.
 *
 * A lambda has no annotations, so its parameter types come entirely from
 * context. The original's strategy, stated in its own comment, is deliberately
 * unsophisticated: assume the lambda's signature matches the expected callable
 * position-for-position, and fall back to Unknown for any parameter where that
 * assumption breaks. Once one parameter mismatches, `sawParamMismatch` latches
 * and every later parameter is Unknown too -- positional correspondence is gone,
 * so nothing after the mismatch can be trusted.
 *
 * The return expression is evaluated under speculative mode whenever the
 * parameter types were guessed, because those guesses were written into the type
 * cache and the speculative cache has no way to record that the return type
 * depends on them. Caching a return type derived from a guess that a later
 * candidate overturns would be wrong for every subsequent read.
 *
 * When several expected callables are in play (an overloaded or union-typed
 * parameter), each is tried under forced speculation and the first that produces
 * no type errors wins; if none does, the first candidate is used anyway so that
 * the diagnostics the user sees come from a concrete attempt.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// getTypeOfLambda corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfLambda(
	node *parser.LambdaNode, inferenceContext *InferenceContext,
) *TypeResult {
	expectedFunctionTypes := []*FunctionType{}
	if inferenceContext != nil {
		MapSubtypes(inferenceContext.ExpectedType, func(subtype Type) Type {
			if IsFunction(subtype) {
				expectedFunctionTypes = append(expectedFunctionTypes, subtype.(*FunctionType))
			}

			if IsClassInstance(subtype) {
				boundMethod := e.GetBoundMagicMethod(
					subtype.(*ClassType), "__call__", nil, nil, nil, 0)
				if boundMethod != nil && IsFunction(boundMethod) {
					expectedFunctionTypes = append(expectedFunctionTypes, boundMethod.(*FunctionType))
				}
			}

			return nil
		}, nil)
	}

	var expectedSubtype *FunctionType

	// The original's comment: if there's more than one type, try each in turn until
	// we find one that works.
	if len(expectedFunctionTypes) > 1 {
		// The original's comment: sort the expected types for deterministic results.
		asTypes := make([]Type, 0, len(expectedFunctionTypes))
		for _, t := range expectedFunctionTypes {
			asTypes = append(asTypes, t)
		}
		sorted := SortTypes(asTypes)
		expectedFunctionTypes = expectedFunctionTypes[:0]
		for _, t := range sorted {
			expectedFunctionTypes = append(expectedFunctionTypes, t.(*FunctionType))
		}

		for _, subtype := range expectedFunctionTypes {
			result := e.getTypeOfLambdaWithExpectedType(node, subtype, inferenceContext, true)

			if !result.TypeErrors {
				expectedSubtype = subtype
				break
			}
		}
	}

	if expectedSubtype == nil && len(expectedFunctionTypes) > 0 {
		expectedSubtype = expectedFunctionTypes[0]
	}

	return e.getTypeOfLambdaWithExpectedType(node, expectedSubtype, inferenceContext, false)
}

// getTypeOfLambdaWithExpectedType corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfLambdaWithExpectedType(
	node *parser.LambdaNode,
	expectedType *FunctionType,
	inferenceContext *InferenceContext,
	forceSpeculative bool,
) *TypeResult {
	isIncomplete := inferenceContext != nil && inferenceContext.IsTypeIncomplete
	paramsArePositionOnly := true

	var expectedReturnType Type
	var expectedParamDetails *ParamListDetails

	if expectedType != nil {
		liveTypeVarScopes := GetTypeVarScopesForNode(node)
		transformed := TransformExpectedType(expectedType, liveTypeVarScopes, node.NodeBase().TextRange.Start)
		if fn, ok := transformed.(*FunctionType); ok {
			expectedType = fn
		}

		expectedParamDetails = GetParamListDetails(expectedType, nil)
		expectedReturnType = e.getEffectiveReturnType(expectedType)
	}

	functionType := FunctionTypeCreateInstance("", "", "", FunctionTypeFlagsPartiallyEvaluated, nil)
	functionType.Shared.TypeVarScopeID = TypeVarScopeId(GetScopeIdForNode(node))

	// The original's comment: pre-cache the incomplete function type in case the
	// evaluation of the lambda depends on itself.
	e.writeTypeCache(node, &TypeResult{Type: functionType, IsIncomplete: true}, evalFlagsNonePtr(), nil, false)

	// The original's comment: we assume for simplicity that the parameter signature
	// of the lambda is the same as the expected type. If this isn't the case, we'll
	// use object for any lambda parameters that don't match. We could make this
	// more sophisticated in the future, but it becomes very complex to handle all
	// of the permutations.
	sawParamMismatch := false

	for index, param := range node.D.Params {
		var paramType Type

		if expectedParamDetails != nil && !sawParamMismatch {
			if index < len(expectedParamDetails.Params) {
				expectedParam := expectedParamDetails.Params[index]

				// The original's comment: if the parameter category matches and both of the
				// parameters are either separators (/ or *) or not separators, copy the type
				// from the expected parameter.
				if expectedParam.Param.Category == param.D.Category &&
					(param.D.Name == nil) == (expectedParam.Param.Name == nil) {
					paramType = expectedParam.Type
				} else {
					sawParamMismatch = true
				}
			} else if param.D.DefaultValue != nil {
				// The original's comment: if the lambda param has a default value but there
				// is no associated parameter in the expected type, assume that the default
				// value is being used to explicitly capture a value from an outer scope.
				// Infer its type from the default value expression.
				paramType = e.getTypeOfExpression(param.D.DefaultValue, EvalFlagsNone, inferenceContext).Type
			}
		} else if param.D.DefaultValue != nil {
			// The original's comment: if there is no inference context but we have a
			// default value, use the default value to infer the parameter's type.
			paramType = e.inferParamTypeFromDefaultValue(param.D.DefaultValue)
		}

		if param.D.Name != nil {
			cachedType := paramType
			if cachedType == nil {
				cachedType = UnknownTypeCreate(false)
			}
			e.writeTypeCache(param.D.Name,
				&TypeResult{Type: e.transformVariadicParamType(node, param.D.Category, cachedType)},
				evalFlagsNonePtr(), nil, false)
		}

		if param.D.DefaultValue != nil {
			// The original's comment: evaluate the default value if it's present.
			e.getTypeOfExpression(param.D.DefaultValue, EvalFlagsConvertEllipsisToAny, nil)
		}

		// The original's comment: determine whether we need to insert an implied
		// position-only parameter. This is needed when a function's parameters are
		// named using the old-style way of specifying position-only parameters.
		//
		// The original guards this on `index >= 0`, which is always true; the guard
		// is kept because removing it would silently change nothing but reads as an
		// edit to logic.
		if index >= 0 {
			isImplicitPositionOnlyParam := false

			if param.D.Category == parser.ParamCategorySimple && param.D.Name != nil {
				if IsPrivateName(param.D.Name.D.Value) {
					isImplicitPositionOnlyParam = true
				}
			} else {
				paramsArePositionOnly = false
			}

			if paramsArePositionOnly && !isImplicitPositionOnlyParam &&
				len(functionType.Shared.Parameters) > 0 {
				FunctionTypeAddPositionOnlyParamSeparator(functionType)
			}

			if !isImplicitPositionOnlyParam {
				paramsArePositionOnly = false
			}
		}

		effectiveParamType := paramType
		if effectiveParamType == nil {
			effectiveParamType = UnknownTypeCreate(false)
		}
		var paramName *string
		if param.D.Name != nil {
			name := param.D.Name.D.Value
			paramName = &name
		}
		var defaultType Type
		if param.D.DefaultValue != nil {
			defaultType = AnyTypeCreate(true)
		}

		functionParam := FunctionParamCreate(
			param.D.Category,
			effectiveParamType,
			FunctionParamFlagsTypeDeclared,
			paramName,
			defaultType,
			param.D.DefaultValue,
		)

		FunctionTypeAddParam(functionType, functionParam)
	}

	if paramsArePositionOnly && len(functionType.Shared.Parameters) > 0 {
		FunctionTypeAddPositionOnlyParamSeparator(functionType)
	}

	typeErrors := false

	// The original's comment: if we're speculatively evaluating the lambda, create
	// another speculative evaluation scope for the return expression and do not
	// allow retention of the cached types. We need to set allowCacheRetention to
	// false because we don't want to cache the type of the lambda return expression
	// because it depends on the parameter types that we set above, and the
	// speculative type cache doesn't know about that context.
	var speculativeNode parser.ParseNode
	if forceSpeculative || e.IsSpeculativeModeInUse(node) ||
		(inferenceContext != nil && inferenceContext.IsTypeIncomplete) {
		speculativeNode = node.D.Expr
	}

	var dependentType Type
	if inferenceContext != nil {
		dependentType = inferenceContext.ExpectedType
	}

	e.UseSpeculativeMode(speculativeNode, func() {
		returnTypeResult := e.getTypeOfExpression(
			node.D.Expr, EvalFlagsNone, MakeInferenceContext(expectedReturnType, false, nil))

		functionType.Shared.InferredReturnType = &InferredReturnTypeInfo{Type: returnTypeResult.Type}
		if returnTypeResult.IsIncomplete {
			isIncomplete = true
		}

		if returnTypeResult.TypeErrors {
			typeErrors = true
		} else if expectedReturnType != nil {
			// The original's comment: if the expectedReturnType is generic, see if the
			// actual return type provides types for some or all type variables.
			if RequiresSpecialization(expectedReturnType, nil, 0) {
				constraints := NewConstraintTracker()
				if e.AssignType(expectedReturnType, returnTypeResult.Type, nil, constraints,
					AssignTypeFlagsDefault, 0) {
					solved := e.SolveAndApplyConstraints(functionType, constraints, &ApplyTypeVarOptions{
						ReplaceUnsolved: &ReplaceUnsolvedOptions{
							ScopeIDs:       []TypeVarScopeId{},
							TupleClassType: e.GetTupleClassType(),
						},
					}, nil)
					if fn, ok := solved.(*FunctionType); ok {
						functionType = fn
					}
				}
			}
		}
	}, &SpeculativeModeOptions{
		DependentType: dependentType,
		AllowDiagnostics: !forceSpeculative && !e.canSkipDiagnosticForNode(node) &&
			(inferenceContext == nil || !inferenceContext.IsTypeIncomplete),
	})

	// The original's comment: mark the function type as no longer being evaluated.
	functionType.Shared.Flags &^= FunctionTypeFlagsPartiallyEvaluated

	// The original's comment: is the resulting function compatible with the
	// expected type?
	if expectedType != nil &&
		!e.AssignType(expectedType, functionType, nil, nil, AssignTypeFlagsDefault, 0) {
		typeErrors = true
	}

	return &TypeResult{Type: functionType, IsIncomplete: isIncomplete, TypeErrors: typeErrors}
}
