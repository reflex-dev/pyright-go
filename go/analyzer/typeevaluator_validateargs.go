/*
 * typeevaluator_validateargs.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * validateArgs, validateArgTypesWithContext, validateArgTypesWithExpectedType,
 * validateArgTypes, validateArgType, validateArgTypesForParamSpec,
 * validateArgTypesForParamSpecSignature, getUnknownExemptTypeVarsForReturnType,
 * adjustCallableReturnType, validateCallForFunction.
 *
 * Checking the argument types, once matchArgsToParams has decided which argument
 * goes where. This is also where a call's return type is computed, because the
 * two are the same problem: solving the function's type variables against the
 * arguments is what specializes the return.
 *
 * The solving runs in TWO PASSES when there are type variables to solve, and the
 * reason is ordering. `f(x, y)` where both parameters mention `T` needs every
 * argument's contribution before any of them can be checked, so the first pass
 * establishes constraints with diagnostics suppressed and the second does the
 * real check against the solved types. A pass that skipped a bare TypeVar
 * expected type schedules an extra one.
 *
 * The EXPECTED TYPE is applied before either pass, and applying it is a guess
 * rather than a deduction: `x: Sequence[int] = f()` says something about what
 * `f`'s type variables should solve to, but the expected type may be a union and
 * only one member may work. Each candidate is tried speculatively and scored --
 * 3 for a match with no Any or Unknown anywhere, 2 for one containing Any, 1 for
 * Unknown, 0 for no match -- and the best wins.
 *
 * validateArgType is the per-argument worker. Its subtlety is that the argument
 * expression is evaluated WITH the parameter type as its expected type, so a
 * lambda or a list literal can be inferred in context; that means the expected
 * type has to be solved before the argument is evaluated, which is why the
 * first pass exists at all.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateCallForFunction corresponds to the function of the same name.
func (e *typeEvaluator) validateCallForFunction(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	fnType *FunctionType,
	isCallTypeIncomplete bool,
	constraints *ConstraintTracker,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
) *CallResult {
	if fnType.Base().IsInstantiable() {
		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.CallableNotInstantiable().Format(e.PrintType(fnType, nil)),
			errorNode, nil)
		return &CallResult{ArgumentErrors: true}
	}

	// The original's comment: the stdlib collections/__init__.pyi stub file
	// defines namedtuple as a function rather than a class, so we need to check for
	// it here.
	if FunctionTypeIsBuiltIn(fnType, "namedtuple") {
		e.AddDiagnostic(DiagnosticRuleReportUntypedNamedTuple,
			localization.LocMessage.NamedTupleNoTypes(), errorNode, nil)

		result := &CallResult{
			ReturnType: CreateNamedTupleType(e, errorNode, argList, false),
		}

		e.validateArgs(errorNode, argList, &TypeResult{Type: fnType}, constraints,
			skipUnknownArgCheck, inferenceContext)

		return result
	}

	// The original's comment: handle the NewType specially, replacing the normal
	// return type.
	if FunctionTypeIsBuiltIn(fnType, "NewType") {
		return &CallResult{ReturnType: e.createNewType(errorNode, argList)}
	}

	functionResult := e.validateArgs(errorNode, argList,
		&TypeResult{Type: fnType, IsIncomplete: isCallTypeIncomplete},
		constraints, skipUnknownArgCheck, inferenceContext)

	isTypeIncomplete := functionResult.IsTypeIncomplete
	returnType := functionResult.ReturnType
	argumentErrors := functionResult.ArgumentErrors

	if !argumentErrors {
		// The original's comment: call the function transform logic to handle
		// special-cased functions.
		fallback := functionResult.ReturnType
		if fallback == nil {
			fallback = UnknownTypeCreate(isTypeIncomplete)
		}

		transformed := ApplyFunctionTransform(e, errorNode, argList, fnType, &CallResult{
			ArgumentErrors:   functionResult.ArgumentErrors,
			ReturnType:       fallback,
			IsTypeIncomplete: isTypeIncomplete,
		})

		returnType = transformed.ReturnType
		if transformed.IsTypeIncomplete {
			isTypeIncomplete = true
		}
		if transformed.ArgumentErrors {
			argumentErrors = true
		}
	}

	if FunctionTypeIsBuiltIn(fnType, "__import__") {
		// The original's comment: for the special __import__ type, we'll override the
		// return type to be "Any". This is required because we don't know what module
		// was imported, and we don't want to fail type checks when accessing members
		// of the resulting module type.
		returnType = AnyTypeCreate(false)
	}

	return &CallResult{
		ReturnType:              returnType,
		IsTypeIncomplete:        isTypeIncomplete,
		ArgumentErrors:          argumentErrors,
		OverloadsUsedForCall:    functionResult.OverloadsUsedForCall,
		SpecializedInitSelfType: functionResult.SpecializedInitSelfType,
	}
}

// validateArgs corresponds to the function of the same name.
//
// Its comment: tries to assign the call arguments to the function parameter list
// and reports any mismatches in types or counts. Returns the specialized return
// type of the call.
func (e *typeEvaluator) validateArgs(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	typeResult *TypeResult,
	constraints *ConstraintTracker,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
) *CallResult {
	matchResults := e.matchArgsToParams(errorNode, argList, typeResult, 0)

	if matchResults.ArgumentErrors {
		e.evaluateArgsForDiagnostics(argList, matchResults)

		// The original's comment: use a return type of Unknown but attach a
		// "possible type" to it so the completion provider can suggest better
		// completions.
		possibleType := FunctionTypeGetEffectiveReturnType(typeResult.Type.(*FunctionType), false)
		var returnType Type
		if possibleType != nil && !IsAnyOrUnknown(possibleType) {
			returnType = UnknownTypeCreatePossibleType(possibleType, false)
		}

		return &CallResult{
			ReturnType:           returnType,
			ArgumentErrors:       true,
			ActiveParam:          matchResults.ActiveParam,
			OverloadsUsedForCall: []*FunctionType{},
		}
	}

	effectiveConstraints := constraints
	if effectiveConstraints == nil {
		effectiveConstraints = NewConstraintTracker()
	}

	var expectedType Type
	isTypeIncomplete := false
	var returnTypeOverride Type
	if inferenceContext != nil {
		expectedType = inferenceContext.ExpectedType
		isTypeIncomplete = inferenceContext.IsTypeIncomplete
		returnTypeOverride = inferenceContext.ReturnTypeOverride
	}

	return e.validateArgTypesWithContext(errorNode, matchResults, effectiveConstraints,
		skipUnknownArgCheck, MakeInferenceContext(expectedType, isTypeIncomplete, returnTypeOverride))
}

// evaluateArgsForDiagnostics is the original's argument-error block.
//
// Its comment: evaluate types of all args. This will ensure that referenced
// symbols are not reported as unaccessed. Also pass the expected parameter type
// as inference context to enable proper completions even when there are errors.
func (e *typeEvaluator) evaluateArgsForDiagnostics(argList []*Arg, matchResults *MatchArgsToParamsResult) {
	for _, argParam := range matchResults.ArgParams {
		if argParam.Argument.ValueExpression != nil &&
			!e.IsSpeculativeModeInUse(argParam.Argument.ValueExpression) {
			e.GetTypeOfExpression(argParam.Argument.ValueExpression, EvalFlagsNone,
				MakeInferenceContext(argParam.ParamType, false, nil))
		}
	}

	// The original's comment: also evaluate any arguments that weren't matched to
	// parameters.
	for _, arg := range argList {
		if arg.ValueExpression == nil || e.IsSpeculativeModeInUse(arg.ValueExpression) {
			continue
		}

		// The original's comment: check if this argument was already evaluated above.
		wasEvaluated := false
		for _, argParam := range matchResults.ArgParams {
			if argParam.Argument == arg {
				wasEvaluated = true
				break
			}
		}
		if !wasEvaluated {
			e.GetTypeOfExpression(arg.ValueExpression, EvalFlagsNone, nil)
		}
	}
}

// validateArgTypesWithContext corresponds to the function of the same name.
//
// Its comment: after having matched arguments with parameters, this function
// evaluates the types of each argument expression and validates that the
// resulting type is compatible with the declared type of the corresponding
// parameter.
func (e *typeEvaluator) validateArgTypesWithContext(
	errorNode parser.ExpressionNode,
	matchResults *MatchArgsToParamsResult,
	constraints *ConstraintTracker,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
) *CallResult {
	fnType := matchResults.Overload

	var expectedType Type
	if inferenceContext != nil {
		expectedType = inferenceContext.ExpectedType
	}

	// The original's comment: can we safely ignore the inference context, either
	// because it's not provided or will have no effect? If so, avoid the extra work.
	var returnType Type
	if inferenceContext != nil && inferenceContext.ReturnTypeOverride != nil {
		returnType = inferenceContext.ReturnTypeOverride
	} else {
		returnType = e.getEffectiveReturnType(fnType)
	}
	if returnType == nil || !RequiresSpecialization(returnType, nil, 0) {
		expectedType = nil
	}

	// The original's comment: refine the expected type by speculatively evaluating
	// arg types. If the expected type is a union, we may need to perform multiple
	// evaluations to determine whether one of the subtypes works.
	if expectedType != nil {
		expectedType = e.refineExpectedTypeForCall(errorNode, matchResults, constraints,
			expectedType, returnType, inferenceContext)
	}

	// The original's comment: if there is no expected type, or the expected type is
	// Any or Unknown, there's nothing left to do here.
	if expectedType == nil || IsAnyOrUnknown(expectedType) || IsNever(expectedType) {
		return e.validateArgTypes(errorNode, matchResults, constraints, skipUnknownArgCheck)
	}

	return e.validateArgTypesWithExpectedType(errorNode, matchResults, constraints,
		skipUnknownArgCheck, expectedType, returnType)
}

// refineExpectedTypeForCall is the original's speculative scoring of expected
// type candidates.
func (e *typeEvaluator) refineExpectedTypeForCall(
	errorNode parser.ExpressionNode,
	matchResults *MatchArgsToParamsResult,
	constraints *ConstraintTracker,
	expectedType Type,
	returnType Type,
	inferenceContext *InferenceContext,
) Type {
	// tryExpectedType corresponds to the closure of the same name. Its comment:
	// use a heuristic to pick a subtype that is most likely to be correct. We'll
	// look for a subtype that produces no argument errors and has no Unknowns in
	// the return type.
	tryExpectedType := func(expectedSubtype Type) int {
		clonedConstraints := constraints.Clone()
		callResult := e.validateArgTypesWithExpectedType(errorNode, matchResults, clonedConstraints,
			true, expectedSubtype, returnType)

		if callResult.ArgumentErrors || callResult.ReturnType == nil {
			return 0
		}

		resultReturnType := callResult.ReturnType
		if inferenceContext != nil && inferenceContext.ReturnTypeOverride != nil {
			resultReturnType = e.SolveAndApplyConstraints(inferenceContext.ReturnTypeOverride,
				clonedConstraints, nil, nil)
		}

		if !e.AssignType(expectedSubtype, resultReturnType, nil, nil, AssignTypeFlagsDefault, 0) {
			return 0
		}

		anyOrUnknown := ContainsAnyOrUnknown(callResult.ReturnType, true)
		// The original's comment: prefer return types that have no unknown or Any.
		if anyOrUnknown == nil {
			return 3
		}

		// The original's comment: prefer Any over Unknown.
		if IsAny(anyOrUnknown) {
			return 2
		}
		return 1
	}

	var validExpectedSubtype Type

	e.UseSpeculativeMode(getSpeculativeNodeForCall(errorNode), func() {
		bestSubtypeScore := -1

		// The original's comment: if the expected type is a union, we don't know
		// which type is expected. We may or may not be able to make use of the
		// expected type. We'll evaluate speculatively to see if using one of the
		// expected subtypes works.
		if IsUnion(expectedType) {
			DoForEachSubtypeSorted(expectedType, func(expectedSubtype Type, _ int, _ []Type) {
				if bestSubtypeScore < 3 {
					score := tryExpectedType(expectedSubtype)
					if score > 0 && score > bestSubtypeScore {
						validExpectedSubtype = expectedSubtype
						bestSubtypeScore = score
					}
				}
			})
		}

		if bestSubtypeScore < 3 {
			score := tryExpectedType(expectedType)
			if score > 0 && score > bestSubtypeScore {
				validExpectedSubtype = expectedType
			}
		}
	}, nil)

	return validExpectedSubtype
}

// validateArgTypesWithExpectedType corresponds to the function of the same name.
func (e *typeEvaluator) validateArgTypesWithExpectedType(
	errorNode parser.ExpressionNode,
	matchResults *MatchArgsToParamsResult,
	constraints *ConstraintTracker,
	skipUnknownArgCheck bool,
	expectedType Type,
	returnType Type,
) *CallResult {
	liveTypeVarScopes := GetTypeVarScopesForNode(errorNode)
	assignFlags := AssignTypeFlagsPopulateExpectedType
	if ContainsLiteralType(expectedType, true) {
		assignFlags |= AssignTypeFlagsRetainLiteralsForTypeVar
	}

	// The original's comment: prepopulate the constraints based on the specialized
	// expected type. This will allow us to more closely match the expected type if
	// possible.
	if returnClass, ok := returnType.(*ClassType); ok && IsClassInstance(returnType) &&
		IsClassInstance(expectedType) && !IsTypeSame(returnType, expectedType, TypeSameOptions{}, 0) {
		tempConstraints := NewConstraintTracker()
		if AddConstraintsForExpectedType(e, returnClass, expectedType, tempConstraints,
			liveTypeVarScopes, errorNode.NodeBase().TextRange.Start) {
			genericReturnType := SelfSpecializeClass(returnClass, &SelfSpecializeOptions{OverrideTypeArgs: true})

			expectedType = e.SolveAndApplyConstraints(genericReturnType, tempConstraints,
				&ApplyTypeVarOptions{
					ReplaceUnsolved: &ReplaceUnsolvedOptions{
						ScopeIDs:       GetTypeVarScopeIDs(returnType),
						UseUnknown:     true,
						TupleClassType: e.GetTupleClassType(),
					},
				}, nil)

			assignFlags |= AssignTypeFlagsSkipPopulateUnknownExpectedType
		}
	}

	expectedType = TransformExpectedType(expectedType, liveTypeVarScopes, errorNode.NodeBase().TextRange.Start)

	e.AssignType(returnType, expectedType, nil, constraints, assignFlags, 0)

	return e.validateArgTypes(errorNode, matchResults, constraints, skipUnknownArgCheck)
}

// validateArgTypes corresponds to the function of the same name.
func (e *typeEvaluator) validateArgTypes(
	errorNode parser.ExpressionNode,
	matchResults *MatchArgsToParamsResult,
	constraints *ConstraintTracker,
	skipUnknownArgCheck bool,
) *CallResult {
	fnType := matchResults.Overload
	isTypeIncomplete := matchResults.IsTypeIncomplete
	argumentErrors := false
	argumentMatchScore := 0
	var specializedInitSelfType Type

	var anyOrUnknownArg Type
	if fnType.Priv.BoundToType != nil {
		anyOrUnknownArg = e.getAnyOrUnknownInInvariantPosition(fnType.Priv.BoundToType, 0)
	}

	speculativeNode := getSpeculativeNodeForCall(errorNode)
	typeCondition := GetTypeCondition(fnType)
	paramSpec := FunctionTypeGetParamSpecFromArgsKwargs(fnType)

	e.reportAbstractMethodInvocation(errorNode, fnType)
	specializedInitSelfType = e.applyInitSelfAnnotation(fnType, constraints)

	// The original's comment: special-case a few built-in calls that are often
	// used for casting or checking for unknown types.
	if FunctionTypeIsBuiltIn(fnType, "typing.cast", "typing_extensions.cast",
		"builtins.isinstance", "builtins.issubclass") {
		skipUnknownArgCheck = true
	}

	e.solveTypeVarsForArgs(matchResults, constraints, speculativeNode, typeCondition,
		skipUnknownArgCheck, &isTypeIncomplete)

	sawParamSpecArgs := false
	sawParamSpecKwargs := false
	condition := []TypeCondition{}
	argResults := []*ArgResult{}

	for argParamIndex, argParam := range matchResults.ArgParams {
		argResult := e.validateArgType(argParam, constraints,
			&TypeResult{Type: fnType, IsIncomplete: matchResults.IsTypeIncomplete},
			&ValidateArgTypeOptions{
				SkipUnknownArgCheck: skipUnknownArgCheck,
				ConditionFilter:     typeCondition,
			})

		argResults = append(argResults, argResult)

		if !argResult.IsCompatible {
			argumentErrors = true

			// The original's comment: add the inverse index so earlier parameters
			// represent larger errors. This will help the heuristics in the overload
			// error paths to pick the most likely intended overload if none of them
			// match.
			argumentMatchScore += 1 + (len(matchResults.ArgParams) - argParamIndex)
		}

		if argResult.IsTypeIncomplete {
			isTypeIncomplete = true
		}

		if argResult.Condition != nil {
			condition = TypeConditionCombine(condition, derefConditions(argResult.Condition))
			if condition == nil {
				condition = []TypeCondition{}
			}
		}

		var argAnyOrUnknown Type
		if IsAnyOrUnknown(argResult.ArgType) {
			argAnyOrUnknown = argResult.ArgType
		} else {
			argAnyOrUnknown = e.getAnyOrUnknownInInvariantPosition(argResult.ArgType, 0)
		}
		if argAnyOrUnknown != nil && !argParam.IsDefaultArg {
			if anyOrUnknownArg != nil {
				anyOrUnknownArg = PreserveUnknown(argAnyOrUnknown, anyOrUnknownArg)
			} else {
				anyOrUnknownArg = argAnyOrUnknown
			}
		}

		if paramSpec != nil {
			e.checkDuplicateParamSpecArgs(argParam, argResult, paramSpec,
				&sawParamSpecArgs, &sawParamSpecKwargs)
		}
	}

	paramSpecConstraints := []*ConstraintTracker{}

	// The original's comment: handle the assignment of additional arguments that
	// map to a param spec.
	if matchResults.ParamSpecArgList != nil && matchResults.ParamSpecTarget != nil {
		paramSpecArgResult := e.validateArgTypesForParamSpec(errorNode,
			matchResults.ParamSpecArgList, matchResults.ParamSpecTarget, constraints)

		if paramSpecArgResult.ArgumentErrors {
			argumentErrors = true
			argumentMatchScore++
		}

		paramSpecConstraints = paramSpecArgResult.ConstraintTrackers
	} else if paramSpec != nil && (!sawParamSpecArgs || !sawParamSpecKwargs) {
		if !isTypeIncomplete {
			e.AddDiagnostic(DiagnosticRuleReportCallIssue,
				localization.LocMessage.ParamSpecArgsMissing().Format(e.PrintType(paramSpec, nil)),
				errorNode, nil)
		}
		argumentErrors = true
		argumentMatchScore++
	}

	specializedReturnType := e.computeCallReturnType(errorNode, matchResults, constraints,
		fnType, typeCondition, condition, paramSpecConstraints, &isTypeIncomplete)

	if specializedInitSelfType != nil {
		specializedInitSelfType = e.SolveAndApplyConstraints(specializedInitSelfType, constraints, nil, nil)
	}

	matchResults.ArgumentMatchScore = argumentMatchScore

	overloadsUsedForCall := []*FunctionType{fnType}
	if argumentErrors {
		overloadsUsedForCall = []*FunctionType{}
	}

	return &CallResult{
		ArgumentErrors:          argumentErrors,
		ArgResults:              argResults,
		AnyOrUnknownArg:         anyOrUnknownArg,
		ReturnType:              specializedReturnType,
		IsTypeIncomplete:        isTypeIncomplete,
		ActiveParam:             matchResults.ActiveParam,
		SpecializedInitSelfType: specializedInitSelfType,
		OverloadsUsedForCall:    overloadsUsedForCall,
	}
}

// reportAbstractMethodInvocation is the original's block whose comment reads:
// check for an attempt to invoke an unimplemented abstract method.
func (e *typeEvaluator) reportAbstractMethodInvocation(
	errorNode parser.ExpressionNode, fnType *FunctionType,
) {
	if fnType.Priv.BoundToType == nil || fnType.Priv.BoundToType.Priv.IncludeSubclasses ||
		fnType.Shared.MethodClass == nil {
		return
	}

	abstractSymbolInfo := e.getAbstractSymbolInfo(fnType.Shared.MethodClass, fnType.Shared.Name)
	if abstractSymbolInfo == nil || abstractSymbolInfo.HasImplementation {
		return
	}

	reportNode := errorNode
	if callNode, ok := errorNode.(*parser.CallNode); ok {
		reportNode = callNode.D.LeftExpr
	}

	e.AddDiagnostic(DiagnosticRuleReportAbstractUsage,
		localization.LocMessage.AbstractMethodInvocation().Format(fnType.Shared.Name),
		reportNode, nil)
}

// applyInitSelfAnnotation is the original's block whose comment reads: the type
// annotation for the "self" parameter in an __init__ method to can influence the
// type being constructed.
func (e *typeEvaluator) applyInitSelfAnnotation(
	fnType *FunctionType, constraints *ConstraintTracker,
) Type {
	strippedFirst, strippedIsClass := fnType.Priv.StrippedFirstParamType.(*ClassType)

	if fnType.Shared.Name != "__init__" || !strippedIsClass ||
		fnType.Priv.BoundToType == nil ||
		!IsClassInstance(fnType.Priv.StrippedFirstParamType) ||
		!IsClassInstance(fnType.Priv.BoundToType) ||
		!ClassTypeIsSameGenericClass(strippedFirst, fnType.Priv.BoundToType, 0) ||
		strippedFirst.Priv.TypeArgs == nil {
		return nil
	}

	typeParams := strippedFirst.Shared.TypeParams

	for index, typeArg := range strippedFirst.Priv.TypeArgs {
		if index >= len(typeParams) {
			continue
		}
		typeParam := typeParams[index]
		if !IsTypeSame(typeParam, typeArg, TypeSameOptions{IgnorePseudoGeneric: true}, 0) {
			constraints.SetBounds(typeParams[index], typeArg, nil, false)
		}
	}

	return strippedFirst
}

// solveTypeVarsForArgs is the original's two-pass constraint-establishing loop.
//
// Its comment: run through all args and validate them against their matched
// parameter. We'll do two phases. The first one establishes constraints for type
// variables. The second perform type validation using the solved types. We can
// skip the first pass if there are no type vars to solve.
func (e *typeEvaluator) solveTypeVarsForArgs(
	matchResults *MatchArgsToParamsResult,
	constraints *ConstraintTracker,
	speculativeNode parser.ParseNode,
	typeCondition []TypeCondition,
	skipUnknownArgCheck bool,
	isTypeIncomplete *bool,
) {
	typeVarCount := 0
	for _, arg := range matchResults.ArgParams {
		if arg.RequiresTypeVarMatching {
			typeVarCount++
		}
	}
	if typeVarCount == 0 {
		return
	}

	// The original's comment: do up to two passes.
	passCount := typeVarCount
	if passCount > 2 {
		passCount = 2
	}

	for i := 0; i < passCount; i++ {
		e.UseSpeculativeMode(speculativeNode, func() {
			for _, argParam := range matchResults.ArgParams {
				if !argParam.RequiresTypeVarMatching {
					continue
				}

				argResult := e.validateArgType(argParam, constraints,
					&TypeResult{Type: matchResults.Overload, IsIncomplete: matchResults.IsTypeIncomplete},
					&ValidateArgTypeOptions{
						SkipUnknownArgCheck: skipUnknownArgCheck,
						IsArgFirstPass:      passCount > 1 && i == 0,
						ConditionFilter:     typeCondition,
						SkipReportError:     true,
					})

				if argResult.IsTypeIncomplete {
					*isTypeIncomplete = true
				}

				// The original's comment: if we skipped a bare type var during the
				// first pass, add another pass to ensure that we handle all of the type
				// variables.
				if i == 0 && passCount < 2 && argResult.SkippedBareTypeVarExpectedType {
					passCount++
				}
			}
		}, nil)
	}
}

// checkDuplicateParamSpecArgs is the original's `if (paramSpec)` block inside
// the per-argument loop.
func (e *typeEvaluator) checkDuplicateParamSpecArgs(
	argParam *ValidateArgTypeParams,
	argResult *ArgResult,
	paramSpec *TypeVarType,
	sawParamSpecArgs *bool,
	sawParamSpecKwargs *bool,
) {
	if argParam.Argument.ArgCategory == parser.ArgCategoryUnpackedList &&
		IsParamSpecArgs(paramSpec, argResult.ArgType) {
		if *sawParamSpecArgs {
			e.AddDiagnostic(DiagnosticRuleReportCallIssue,
				localization.LocMessage.ParamSpecArgsKwargsDuplicate().Format(e.PrintType(paramSpec, nil)),
				argParam.ErrorNode, nil)
		}
		*sawParamSpecArgs = true
	}

	if argParam.Argument.ArgCategory == parser.ArgCategoryUnpackedDictionary &&
		IsParamSpecKwargs(paramSpec, argResult.ArgType) {
		if *sawParamSpecKwargs {
			e.AddDiagnostic(DiagnosticRuleReportCallIssue,
				localization.LocMessage.ParamSpecArgsKwargsDuplicate().Format(e.PrintType(paramSpec, nil)),
				argParam.ErrorNode, nil)
		}
		*sawParamSpecKwargs = true
	}
}

// computeCallReturnType is the original's tail: solve the declared return type
// against everything the arguments established.
func (e *typeEvaluator) computeCallReturnType(
	errorNode parser.ExpressionNode,
	matchResults *MatchArgsToParamsResult,
	constraints *ConstraintTracker,
	fnType *FunctionType,
	typeCondition []TypeCondition,
	condition []TypeCondition,
	paramSpecConstraints []*ConstraintTracker,
	isTypeIncomplete *bool,
) Type {
	returnTypeResult := e.getEffectiveReturnTypeResult(fnType, &EffectiveReturnTypeOptions{
		CallSiteInfo: &CallSiteEvaluationInfo{Args: matchResults.ArgParams, ErrorNode: errorNode},
	})
	returnType := returnTypeResult.Type
	if returnTypeResult.IsIncomplete {
		*isTypeIncomplete = true
	}

	if len(condition) > 0 {
		returnType = CloneForCondition(returnType, condition)
	}

	eliminateUnsolvedInUnions := true

	// The original's comment: if the function is returning a callable, don't
	// eliminate unsolved type vars within a union. There are legit uses for
	// unsolved type vars within a callable.
	if IsFunctionOrOverloaded(returnType) {
		eliminateUnsolvedInUnions = false
	}

	specializedReturnType := e.SolveAndApplyConstraints(returnType, constraints, &ApplyTypeVarOptions{
		ReplaceUnsolved: &ReplaceUnsolvedOptions{
			ScopeIDs:                  GetTypeVarScopeIDs(fnType),
			UnsolvedExemptTypeVars:    e.getUnknownExemptTypeVarsForReturnType(fnType, returnType),
			TupleClassType:            e.GetTupleClassType(),
			EliminateUnsolvedInUnions: eliminateUnsolvedInUnions,
		},
	}, nil)
	specializedReturnType = AddConditionToType(specializedReturnType, typeCondition,
		&AddConditionOptions{SkipBoundTypeVars: true})

	// The original's comment: if the function includes a ParamSpec and the captured
	// signature(s) includes generic types, we may need to apply those solved
	// TypeVars.
	for _, paramSpecConstraint := range paramSpecConstraints {
		if paramSpecConstraint == nil {
			continue
		}
		specializedReturnType = e.SolveAndApplyConstraints(specializedReturnType, paramSpecConstraint, nil, nil)

		// The original's comment: it's possible that one or more of the TypeVars or
		// ParamSpecs in the constraints refer to TypeVars that were solved in the
		// paramSpecConstraints. Apply these solved TypeVars accordingly.
		ApplySourceSolutionToConstraints(constraints, SolveConstraints(e, paramSpecConstraint, nil))
	}

	// The original's comment: if the final return type is an unpacked tuple, turn
	// it into a normal (unpacked) tuple.
	if unpacked, ok := specializedReturnType.(*ClassType); ok && IsUnpackedClass(specializedReturnType) {
		specializedReturnType = ClassTypeCloneForPacked(unpacked)
	}

	liveTypeVarScopes := GetTypeVarScopesForNode(errorNode)
	return e.adjustCallableReturnType(errorNode, specializedReturnType, liveTypeVarScopes)
}

// getUnknownExemptTypeVarsForReturnType corresponds to the function of the same
// name.
//
// Its comment: in general, all in-scope type variables left in a return type
// should be replaced with Unknown. However, if the return type is a callable that
// uses type vars that are found nowhere within the function's input parameters,
// we'll treat these as though they're scoped to the callable and leave them
// unsolved.
func (e *typeEvaluator) getUnknownExemptTypeVarsForReturnType(
	functionType *FunctionType, returnType Type,
) []*TypeVarType {
	returnFn, ok := returnType.(*FunctionType)
	if !ok || !IsFunction(returnType) || returnFn.Shared.Name != "" {
		return nil
	}

	returnTypeScopeId := returnFn.Shared.TypeVarScopeID

	// The original's comment: if one or more type vars found within the return type
	// are scoped to the functionType but don't appear anywhere else within the
	// functionType's input parameters, rescope them to the return type callable so
	// they are not replaced with Unknown.
	if returnTypeScopeId == "" || functionType.Shared.TypeVarScopeID == "" {
		return nil
	}

	typeVarsInReturnType := GetTypeVarArgsRecursive(returnType, 0)

	// The original's comment: remove any type variables that appear in the
	// function's input parameters.
	for index, param := range functionType.Shared.Parameters {
		if !FunctionParamIsTypeDeclared(param) {
			continue
		}

		typeVarsInInputParam := GetTypeVarArgsRecursive(FunctionTypeGetParamType(functionType, index), 0)
		remaining := []*TypeVarType{}
		for _, returnTypeVar := range typeVarsInReturnType {
			found := false
			for _, inputTypeVar := range typeVarsInInputParam {
				if IsTypeSame(returnTypeVar, inputTypeVar, TypeSameOptions{}, 0) {
					found = true
					break
				}
			}
			if !found {
				remaining = append(remaining, returnTypeVar)
			}
		}
		typeVarsInReturnType = remaining
	}

	return typeVarsInReturnType
}

// adjustCallableReturnType corresponds to the function of the same name.
//
// Its comment: if the return type includes a generic Callable type, set the type
// var scope to the scope of the function it was originally associated with to
// allow these type vars to be solved. This won't work with overloads or unions of
// callables. It's intended for a specific use case. We may need to make this more
// sophisticated in the future.
func (e *typeEvaluator) adjustCallableReturnType(
	callNode parser.ExpressionNode, returnType Type, liveTypeVarScopes []TypeVarScopeId,
) Type {
	returnFn, ok := returnType.(*FunctionType)
	if !ok || !IsFunction(returnType) {
		return returnType
	}

	// The original's comment: what type variables are referenced in the callable
	// return type? Do not include any live type variables.
	typeParams := []*TypeVarType{}
	for _, t := range GetTypeVarArgsRecursive(returnType, 0) {
		if !containsScopeID(liveTypeVarScopes, t.Priv.ScopeID) {
			typeParams = append(typeParams, t)
		}
	}

	// The original's comment: if there are no unsolved type variables, we're done.
	// If there are unsolved type variables, rescope them to the callable.
	if len(typeParams) == 0 {
		return returnType
	}

	e.InferReturnTypeIfNecessary(returnType)

	// The original's comment: create a new scope ID based on the caller's position.
	// This will guarantee uniqueness. If another caller uses the same call and
	// arguments, the type vars will not conflict.
	newScopeId := TypeVarScopeId(GetScopeIdForNode(callNode))
	solution := NewConstraintSolution(nil)

	newTypeParams := make([]*TypeVarType, len(typeParams))
	for i, typeVar := range typeParams {
		scopeType := TypeVarScopeTypeFunction
		newTypeParam := TypeVarTypeCloneForScopeID(typeVar, newScopeId, typeVar.Priv.ScopeName, &scopeType)
		solution.SetType(typeVar, newTypeParam)
		newTypeParams[i] = newTypeParam
	}

	return ApplySolvedTypeVars(
		FunctionTypeCloneWithNewTypeVarScopeID(returnFn, newScopeId, TypeVarScopeId(""), newTypeParams),
		solution, nil)
}

// validateArgTypesForParamSpec corresponds to the function of the same name.
//
// Its comment: determines whether the specified argument list satisfies the
// function signature bound to the specified ParamSpec. Return value indicates
// success.
func (e *typeEvaluator) validateArgTypesForParamSpec(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	paramSpec *TypeVarType,
	destConstraints *ConstraintTracker,
) *ParamSpecArgResult {
	sets := destConstraints.GetConstraintSets()

	// The original's comment: handle the common case where there is only one
	// signature context.
	if len(sets) == 1 {
		return e.validateArgTypesForParamSpecSignature(errorNode, argList, paramSpec, sets[0])
	}

	filteredSets := []*ConstraintSet{}
	constraintTrackers := []*ConstraintTracker{}
	speculativeNode := getSpeculativeNodeForCall(errorNode)

	for _, context := range sets {
		// The original's comment: use speculative mode to avoid emitting errors or
		// caching types.
		e.UseSpeculativeMode(speculativeNode, func() {
			paramSpecArgResult := e.validateArgTypesForParamSpecSignature(errorNode, argList, paramSpec, context)

			if !paramSpecArgResult.ArgumentErrors {
				filteredSets = append(filteredSets, context)
			}

			constraintTrackers = append(constraintTrackers, paramSpecArgResult.ConstraintTrackers...)
		}, nil)
	}

	// The original's comment: copy back any compatible signature contexts if any
	// were compatible.
	if len(filteredSets) > 0 {
		destConstraints.AddConstraintSets(filteredSets)
	}

	// The original's comment: evaluate non-speculatively to produce a final result
	// and cache types.
	chosen := sets[0]
	if len(filteredSets) > 0 {
		chosen = filteredSets[0]
	}
	paramSpecArgResult := e.validateArgTypesForParamSpecSignature(errorNode, argList, paramSpec, chosen)

	return &ParamSpecArgResult{
		ArgumentErrors:     paramSpecArgResult.ArgumentErrors,
		ConstraintTrackers: constraintTrackers,
	}
}

// validateArgTypesForParamSpecSignature corresponds to the function of the same
// name.
func (e *typeEvaluator) validateArgTypesForParamSpecSignature(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	paramSpec *TypeVarType,
	constraintSet *ConstraintSet,
) *ParamSpecArgResult {
	solutionSet := SolveConstraintSet(e, constraintSet, nil)
	paramSpecType := solutionSet.GetType(paramSpec)
	var converted *FunctionType
	if paramSpecType != nil {
		converted = ConvertTypeToParamSpecValue(paramSpecType)
	} else {
		converted = ConvertTypeToParamSpecValue(paramSpec)
	}

	matchResults := e.matchArgsToParams(errorNode, argList, &TypeResult{Type: converted}, 0)
	functionType := matchResults.Overload
	constraints := NewConstraintTracker()

	if matchResults.ArgumentErrors {
		// The original's comment: evaluate types of all args. This will ensure that
		// referenced symbols are not reported as unaccessed.
		for _, arg := range argList {
			if arg.ValueExpression != nil && !e.IsSpeculativeModeInUse(arg.ValueExpression) {
				e.GetTypeOfExpression(arg.ValueExpression, EvalFlagsNone, nil)
			}
		}

		return &ParamSpecArgResult{ArgumentErrors: true, ConstraintTrackers: []*ConstraintTracker{constraints}}
	}

	functionParamSpec := FunctionTypeGetParamSpecFromArgsKwargs(functionType)
	functionWithoutParamSpec := FunctionTypeCloneRemoveParamSpecArgsKwargs(functionType, false)

	// The original's comment: handle the recursive case where we're passing
	// (*args: P.args, **kwargs: P.args) a remaining function of type (*P).
	if functionParamSpec != nil && len(functionWithoutParamSpec.Shared.Parameters) == 0 &&
		IsTypeSame(functionParamSpec, paramSpec, TypeSameOptions{}, 0) {
		return e.validateRecursiveParamSpecArgs(errorNode, argList, paramSpec, functionParamSpec, constraints)
	}

	result := e.validateArgTypes(errorNode, matchResults, constraints, false)
	return &ParamSpecArgResult{
		ArgumentErrors:     result.ArgumentErrors,
		ConstraintTrackers: []*ConstraintTracker{constraints},
	}
}

// validateRecursiveParamSpecArgs is the original's recursive-ParamSpec block:
// the only legal argument list is exactly `*args, **kwargs` forwarding the same
// ParamSpec.
func (e *typeEvaluator) validateRecursiveParamSpecArgs(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	paramSpec *TypeVarType,
	functionParamSpec *TypeVarType,
	constraints *ConstraintTracker,
) *ParamSpecArgResult {
	// The original's comment: if there are any arguments other than *args: P.args
	// or **kwargs: P.kwargs, report an error.
	argsCount := 0
	kwargsCount := 0
	argumentErrors := false
	var argErrorNode parser.ExpressionNode

	for _, arg := range argList {
		argType := e.GetTypeOfArg(arg, nil).Type

		switch arg.ArgCategory {
		case parser.ArgCategoryUnpackedList:
			if IsParamSpecArgs(paramSpec, argType) {
				argsCount++
			}
		case parser.ArgCategoryUnpackedDictionary:
			if IsParamSpecKwargs(paramSpec, argType) {
				kwargsCount++
			}
		default:
			if argErrorNode == nil {
				argErrorNode = arg.ValueExpression
			}
			argumentErrors = true
		}
	}

	if argsCount != 1 || kwargsCount != 1 {
		argumentErrors = true
	}

	if argumentErrors {
		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.ParamSpecArgsMissing().Format(e.PrintType(functionParamSpec, nil)),
			errorNodeOr(argErrorNode, errorNode), nil)
	}

	return &ParamSpecArgResult{
		ArgumentErrors:     argumentErrors,
		ConstraintTrackers: []*ConstraintTracker{constraints},
	}
}

// validateArgType corresponds to the function of the same name: check one
// argument against its matched parameter.
//
// The subtlety is that the argument expression is evaluated WITH the parameter
// type as its expected type, so a lambda or a container literal can be inferred
// in context. That means the expected type must be solved before the argument is
// evaluated, which is what the caller's first pass is for. On the first pass a
// "bare" in-scope TypeVar is skipped as an expected type -- it carries no
// information yet and would only mislead the inference.
func (e *typeEvaluator) validateArgType(
	argParam *ValidateArgTypeParams,
	constraints *ConstraintTracker,
	typeResult *TypeResult,
	options *ValidateArgTypeOptions,
) *ArgResult {
	var argType Type
	var expectedTypeDiag *common.DiagnosticAddendum
	isTypeIncomplete := typeResult != nil && typeResult.IsIncomplete
	isCompatible := true
	functionName := ""
	if typeResult != nil {
		functionName = typeResult.Type.(*FunctionType).Shared.Name
	}
	skippedBareTypeVarExpectedType := false

	if argParam.Argument.ValueExpression != nil {
		argType, expectedTypeDiag, skippedBareTypeVarExpectedType =
			e.evaluateArgExpression(argParam, constraints, typeResult, options,
				&isTypeIncomplete, &isCompatible)
	} else {
		// The original's comment: was the argument's type precomputed by the caller?
		if argParam.ArgType != nil {
			argType = argParam.ArgType
		} else {
			argTypeResult := e.GetTypeOfArg(argParam.Argument,
				MakeInferenceContext(argParam.ParamType, isTypeIncomplete, nil))
			argType = argTypeResult.Type
			if argTypeResult.IsIncomplete {
				isTypeIncomplete = true
			}
		}

		// The original's comment: if the argument came from a parameter's default
		// argument value, we may need to specialize the type.
		if argParam.IsDefaultArg {
			argType = e.SolveAndApplyConstraints(argType, constraints, nil, nil)
		}
	}

	// The original's comment: if we're assigning to a var arg dictionary with a
	// TypeVar type, strip literals before performing the assignment. This is used in
	// places like a dict constructor.
	if argParam.ParamCategory == parser.ParamCategoryKwargsDict && IsTypeVar(argParam.ParamType) {
		argType = e.StripLiteralValue(argType)
	}

	// The original's comment: if there's a constraint filter, apply it to top-level
	// type variables if appropriate. This doesn't properly handle non-top-level
	// constrained type variables.
	if options.ConditionFilter != nil {
		argType = e.MapSubtypesExpandTypeVars(argType,
			&EvaluatorMapSubtypesOptions{ConditionFilter: refConditions(options.ConditionFilter)},
			func(expandedSubtype Type, _ Type) Type {
				return expandedSubtype
			})
	}

	var condition []*TypeCondition
	if props := argType.Base().Props; props != nil {
		condition = refConditions(props.Condition)
	}

	if paramSpec, ok := argParam.ParamType.(*TypeVarType); ok && IsParamSpec(argParam.ParamType) {
		// The original's comment: handle the case where we're assigning a *args or
		// **kwargs argument to a *P.args or **P.kwargs parameter.
		if paramSpec.Priv.ParamSpecAccess != ParamSpecAccessNone {
			return &ArgResult{IsCompatible: isCompatible, ArgType: argType,
				IsTypeIncomplete: isTypeIncomplete, Condition: condition}
		}

		// The original's comment: handle the case where we're assigning a *P.args or
		// **P.kwargs argument to a *P.args or **P.kwargs parameter.
		if argSpec, ok := argType.(*TypeVarType); ok && IsParamSpec(argType) &&
			argSpec.Priv.ParamSpecAccess != ParamSpecAccessNone {
			return &ArgResult{IsCompatible: isCompatible, ArgType: argType,
				IsTypeIncomplete: isTypeIncomplete, Condition: condition}
		}
	}

	assignTypeFlags := AssignTypeFlagsDefault
	if argParam.IsinstanceParam {
		assignTypeFlags |= AssignTypeFlagsAllowIsinstanceSpecialForms
	}
	if options.IsArgFirstPass {
		assignTypeFlags |= AssignTypeFlagsArgAssignmentFirstPass
	}

	var diag *common.DiagnosticAddendum
	if !options.SkipReportError {
		diag = common.NewDiagnosticAddendum()
	}

	if !e.AssignType(argParam.ParamType, argType, createAddendumOrNil(diag),
		constraints, assignTypeFlags, 0) {
		if !options.SkipReportError {
			e.reportArgAssignmentFailure(argParam, argType, functionName, diag,
				expectedTypeDiag, isTypeIncomplete)
		}

		return &ArgResult{
			IsCompatible:                   false,
			ArgType:                        argType,
			IsTypeIncomplete:               isTypeIncomplete,
			SkippedBareTypeVarExpectedType: skippedBareTypeVarExpectedType,
			Condition:                      condition,
		}
	}

	if !options.SkipUnknownArgCheck {
		e.reportUnknownArgType(argParam, argType, functionName, isTypeIncomplete)
	}

	return &ArgResult{
		IsCompatible:                   isCompatible,
		ArgType:                        argType,
		IsTypeIncomplete:               isTypeIncomplete,
		SkippedBareTypeVarExpectedType: skippedBareTypeVarExpectedType,
		Condition:                      condition,
	}
}

// evaluateArgExpression is the original's `argParam.argument.valueExpression`
// branch: solve the expected type, evaluate the argument against it, and feed
// the result back into the constraints.
func (e *typeEvaluator) evaluateArgExpression(
	argParam *ValidateArgTypeParams,
	constraints *ConstraintTracker,
	typeResult *TypeResult,
	options *ValidateArgTypeOptions,
	isTypeIncomplete *bool,
	isCompatible *bool,
) (Type, *common.DiagnosticAddendum, bool) {
	var expectedType Type
	skippedBareTypeVarExpectedType := false

	// The original's comment: is the expected type a "bare" in-scope TypeVar or a
	// union of bare in-scope TypeVars?
	isExpectedTypeBareTypeVar := true
	DoForEachSubtype(argParam.ParamType, func(subtype Type, _ int, _ []Type) {
		tv, ok := subtype.(*TypeVarType)
		if !ok || !IsTypeVar(subtype) || typeResult == nil ||
			tv.Priv.ScopeID != typeResult.Type.(*FunctionType).Shared.TypeVarScopeID {
			isExpectedTypeBareTypeVar = false
		}
	})

	if !options.IsArgFirstPass || !isExpectedTypeBareTypeVar {
		expectedType = argParam.ParamType

		// The original's comment: if the parameter type is a function with a
		// ParamSpec, don't apply the solved TypeVars if the constraint tracker has
		// more than one signature. This will expand the ParamSpec into an overload,
		// which will cause problems.
		skipApplySolvedTypeVars := false
		if paramFn, ok := argParam.ParamType.(*FunctionType); ok && IsFunction(argParam.ParamType) &&
			FunctionTypeGetParamSpecFromArgsKwargs(paramFn) != nil &&
			len(constraints.GetConstraintSets()) > 1 {
			skipApplySolvedTypeVars = true
		}

		if !skipApplySolvedTypeVars {
			expectedType = e.SolveAndApplyConstraints(expectedType, constraints, nil,
				&SolveConstraintsOptions{UseLowerBoundOnly: options.IsArgFirstPass})
		}
	} else {
		skippedBareTypeVarExpectedType = true
	}

	// The original's comment: if the expected type is unknown, don't use an
	// expected type. Instead, use default rules for evaluating the expression type.
	if expectedType != nil && IsUnknown(expectedType) {
		expectedType = nil
	}

	var argType Type
	var expectedTypeDiag *common.DiagnosticAddendum

	// The original's comment: was the argument's type precomputed by the caller?
	if argParam.ArgType != nil {
		argType = argParam.ArgType
	} else {
		flags := EvalFlagsNoFinal | EvalFlagsNoSpecialize
		if argParam.IsinstanceParam {
			flags = EvalFlagsIsInstanceArgDefaults
		}

		callerIncomplete := typeResult != nil && typeResult.IsIncomplete
		exprTypeResult := e.GetTypeOfExpression(argParam.Argument.ValueExpression, flags,
			MakeInferenceContext(expectedType, callerIncomplete, nil))

		argType = exprTypeResult.Type

		// The original's comment: if the argument is unpacked and we are supposed to
		// enforce that it's an iterator, do so now.
		if argParam.Argument.ArgCategory == parser.ArgCategoryUnpackedList &&
			argParam.Argument.EnforceIterable {
			iteratorType := e.GetTypeOfIterator(exprTypeResult, false,
				argParam.Argument.ValueExpression, nil)
			// The original's comment: try to prevent cascading errors if it was not
			// iterable.
			if iteratorType != nil {
				argType = iteratorType.Type
			} else {
				argType = UnknownTypeCreate(false)
			}
		}

		if exprTypeResult.IsIncomplete {
			*isTypeIncomplete = true
		}

		if expectedType != nil && RequiresSpecialization(expectedType, nil, 0) {
			// The original's comment: assign the argument type back to the expected
			// type to assign values to any unification variables.
			clonedConstraints := constraints.Clone()
			assignFlags := AssignTypeFlagsDefault
			if options.IsArgFirstPass {
				assignFlags = AssignTypeFlagsArgAssignmentFirstPass
			}

			if e.AssignType(expectedType, argType, nil, clonedConstraints, assignFlags, 0) {
				constraints.CopyFromClone(clonedConstraints)
			} else {
				*isCompatible = false
			}
		}

		expectedTypeDiag = exprTypeResult.ExpectedTypeDiagAddendum
	}

	if argParam.Argument.Name != nil && !e.IsSpeculativeModeInUse(argParam.ErrorNode) {
		cached := expectedType
		if cached == nil {
			cached = argType
		}
		noFlags := EvalFlagsNone
		e.writeTypeCache(argParam.Argument.Name,
			&TypeResult{Type: cached, IsIncomplete: *isTypeIncomplete}, &noFlags, nil, false)
	}

	return argType, expectedTypeDiag, skippedBareTypeVarExpectedType
}

// reportArgAssignmentFailure is the original's diagnostic block for a failed
// argument assignment. It picks among four messages depending on whether the
// parameter and the function have usable names.
func (e *typeEvaluator) reportArgAssignmentFailure(
	argParam *ValidateArgTypeParams,
	argType Type,
	functionName string,
	diag *common.DiagnosticAddendum,
	expectedTypeDiag *common.DiagnosticAddendum,
	isTypeIncomplete bool,
) {
	// The original's comment: mismatching parameter types are common in untyped
	// code; don't bother spending time printing types if the diagnostic is disabled.
	fileInfo := GetFileInfo(argParam.ErrorNode)
	if fileInfo.DiagnosticRuleSet.ReportArgumentType == DiagnosticLevelNone ||
		e.canSkipDiagnosticForNode(argParam.ErrorNode) || isTypeIncomplete {
		return
	}

	argTypeText := e.PrintType(argType, nil)
	paramTypeText := e.PrintType(argParam.ParamType, nil)

	var message string
	if argParam.ParamName != "" && !argParam.IsParamNameSynthesized {
		if functionName != "" {
			message = localization.LocMessage.ArgAssignmentParamFunction().Format(
				argTypeText, paramTypeText, functionName, argParam.ParamName)
		} else {
			message = localization.LocMessage.ArgAssignmentParam().Format(
				argTypeText, paramTypeText, argParam.ParamName)
		}
	} else {
		if functionName != "" {
			message = localization.LocMessage.ArgAssignmentFunction().Format(
				argTypeText, paramTypeText, functionName)
		} else {
			message = localization.LocMessage.ArgAssignment().Format(argTypeText, paramTypeText)
		}
	}

	// The original's comment: if we have an expected type diagnostic addendum, use
	// that instead of the local diagnostic addendum because it will be more
	// informative.
	if expectedTypeDiag != nil {
		diag = expectedTypeDiag
	}

	e.AddDiagnostic(DiagnosticRuleReportArgumentType, message+addendumString(diag),
		argParam.ErrorNode, effectiveRangeOrNil(diag, argParam.ErrorNode))
}

// reportUnknownArgType is the original's `!options.skipUnknownArgCheck` block.
func (e *typeEvaluator) reportUnknownArgType(
	argParam *ValidateArgTypeParams, argType Type, functionName string, isTypeIncomplete bool,
) {
	simplifiedType := e.MakeTopLevelTypeVarsConcrete(RemoveUnbound(argType), false)
	fileInfo := GetFileInfo(argParam.ErrorNode)

	// getDiagAddendum corresponds to the closure of the same name. The original
	// appends `diagAddendum.getString()` to its own first message, which is empty
	// at that point; reproduced as a plain message.
	getDiagAddendum := func() *common.DiagnosticAddendum {
		diagAddendum := common.NewDiagnosticAddendum()
		if argParam.ParamName != "" {
			if functionName != "" {
				diagAddendum.AddMessage(localization.LocAddendum.ArgParamFunction().Format(
					argParam.ParamName, functionName))
			} else {
				diagAddendum.AddMessage(localization.LocAddendum.ArgParam().Format(argParam.ParamName))
			}
		}
		return diagAddendum
	}

	// The original's comment: do not check for unknown types if the expected type
	// is "Any". Don't print types if reportUnknownArgumentType is disabled for
	// performance.
	if fileInfo.DiagnosticRuleSet.ReportUnknownArgumentType == DiagnosticLevelNone ||
		IsAny(argParam.ParamType) || isTypeIncomplete {
		return
	}

	if IsUnknown(simplifiedType) {
		diagAddendum := getDiagAddendum()
		e.AddDiagnostic(DiagnosticRuleReportUnknownArgumentType,
			localization.LocMessage.ArgTypeUnknown()+diagAddendum.GetString(),
			argParam.ErrorNode, nil)
		return
	}

	if IsPartlyUnknown(simplifiedType, 0) {
		// The original's comment: if the parameter type is also partially unknown,
		// don't report the error because it's likely that the partially-unknown type
		// arose due to bidirectional type matching.
		if IsPartlyUnknown(argParam.ParamType, 0) {
			return
		}

		diagAddendum := getDiagAddendum()
		diagAddendum.AddMessage(localization.LocAddendum.ArgumentType().Format(
			e.PrintType(simplifiedType, &PrintTypeOptions{ExpandTypeAlias: true})))
		e.AddDiagnostic(DiagnosticRuleReportUnknownArgumentType,
			localization.LocMessage.ArgTypePartiallyUnknown()+diagAddendum.GetString(),
			argParam.ErrorNode, nil)
	}
}

// addendumString is the original's `diag?.getString()`.
func addendumString(diag *common.DiagnosticAddendum) string {
	if diag == nil {
		return ""
	}
	return diag.GetString()
}

// effectiveRangeOrNil is the original's
// `diag?.getEffectiveTextRange() ?? argParam.errorNode`.
func effectiveRangeOrNil(diag *common.DiagnosticAddendum, node parser.ParseNode) *common.TextRange {
	if diag != nil {
		if r := diag.GetEffectiveTextRange(); r != nil {
			return r
		}
	}
	textRange := node.NodeBase().TextRange
	return &textRange
}

// getAbstractSymbolInfo corresponds to the function of the same name, which
// decides whether a symbol is an unimplemented abstract method. In an ABC the
// rule is the @abstractmethod decorator; in a protocol it is more involved and
// depends on whether the declaration is in a stub file.
func (e *typeEvaluator) getAbstractSymbolInfo(classType *ClassType, symbolName string) *AbstractSymbol {
	e.unported("getAbstractSymbolInfo")
	return nil
}

// refConditions is the inverse of derefConditions: the evaluator's options and
// ArgResult carry conditions by pointer while types.ts carries them by value.
func refConditions(conditions []TypeCondition) []*TypeCondition {
	if conditions == nil {
		return nil
	}
	out := make([]*TypeCondition, 0, len(conditions))
	for i := range conditions {
		out = append(out, &conditions[i])
	}
	return out
}

// ApplyFunctionTransform corresponds to the functionTransform.ts function of the
// same name, which rewrites the result of a call to one of the handful of
// functions whose return type cannot be expressed in the type system --
// `functools.total_ordering` and the `dataclass_transform` family.
func ApplyFunctionTransform(
	evaluator TypeEvaluator,
	_ parser.ExpressionNode,
	_ []*Arg,
	_ *FunctionType,
	result *CallResult,
) *CallResult {
	noteEvaluatorUnported(evaluator, "functionTransform.applyFunctionTransform")
	return result
}
