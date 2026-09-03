/*
 * constructors_validate.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/constructors.ts (pyright 1.1.412):
 * validateConstructorArgs, validateNewAndInitMethods, validateNewMethod,
 * validateInitMethod, validateFallbackConstructorCall, validateMetaclassCall,
 * applyExpectedSubtypeForConstructor, applyExpectedTypeForConstructor,
 * applyExpectedTypeForTupleConstructor, shouldSkipNewAndInitEvaluation,
 * shouldSkipInitEvaluation, isDefaultNewMethod, hasConstructorTransform.
 *
 * `C(...)`. Constructing an object is three calls, not one, and which of them
 * decides the answer depends on what the class defines.
 *
 * The METACLASS `__call__` runs first and can pre-empt everything: a metaclass
 * whose `__call__` returns something other than an instance of the class has
 * replaced the construction protocol, and `__new__`/`__init__` are not consulted
 * at all. That is checked speculatively, so that a metaclass that turns out not
 * to override the protocol costs no diagnostics.
 *
 * Then `__new__` and `__init__`, and the subtle part is which one gets to decide
 * the TYPE ARGUMENTS. `__new__` runs first, but three cases hand the decision
 * back to `__init__`: no `__new__` at all, the placeholder
 * `(cls, *args, **kwargs) -> Self` signature that typeshed gives `object`, and a
 * `__new__` that returns Unknown. Otherwise `__new__`'s return type wins and
 * `__init__` is skipped when it is not an instance of the class -- matching what
 * `type.__call__` does at runtime.
 *
 * Argument evaluation is where most of the care goes. `__new__`'s arguments are
 * evaluated SPECULATIVELY, because `__init__` may need to re-evaluate the same
 * expressions with different expected types, and evaluating twice for real would
 * double every diagnostic. If `__init__` never runs, `__new__` is re-run
 * non-speculatively so its errors are actually reported. The same shape appears
 * again around constructor transforms.
 *
 * If a class overrides neither, the fallback binds `object.__new__` and validates
 * against that, so `C(1, 2)` on a class with no constructor still reports the
 * extra arguments.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// ValidateConstructorArgs corresponds to validateConstructorArgs.
func ValidateConstructorArgs(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	argList []*Arg,
	classType *ClassType,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
) *CallResult {
	// The original's comment: if this is an unspecialized generic type alias,
	// specialize it now using default type argument values.
	if props := classType.Base().Props; props != nil && props.TypeAliasInfo != nil {
		aliasInfo := props.TypeAliasInfo
		if aliasInfo.Shared != nil && aliasInfo.Shared.TypeParams != nil && aliasInfo.TypeArgs == nil {
			specialized := ApplySolvedTypeVars(classType, NewConstraintSolution(nil), &ApplyTypeVarOptions{
				ReplaceUnsolved: &ReplaceUnsolvedOptions{
					ScopeIDs:       []TypeVarScopeId{aliasInfo.Shared.TypeVarScopeId},
					TupleClassType: evaluator.GetTupleClassType(),
				},
			})
			if specializedClass, ok := specialized.(*ClassType); ok {
				classType = specializedClass
			}
		}
	}

	metaclassResult := validateMetaclassCall(evaluator, errorNode, argList, classType,
		skipUnknownArgCheck, inferenceContext, true)

	if metaclassResult != nil {
		metaclassReturnType := metaclassResult.ReturnType
		if metaclassReturnType == nil {
			metaclassReturnType = UnknownTypeCreate(false)
		}

		// The original's comment: if there a custom `__call__` method on the
		// metaclass that returns something other than an instance of the class,
		// assume that it overrides the normal `type.__call__` logic and don't perform
		// the usual __new__ and __init__ validation.
		if metaclassResult.ArgumentErrors ||
			shouldSkipNewAndInitEvaluation(evaluator, classType, metaclassReturnType) {
			validateMetaclassCall(evaluator, errorNode, argList, classType,
				skipUnknownArgCheck, inferenceContext, false)

			return metaclassResult
		}
	}

	// The original's comment: determine whether the class overrides the
	// object.__new__ method.
	newMethodDiag := common.NewDiagnosticAddendum()
	newMethodTypeResult := GetBoundNewMethod(evaluator, errorNode, classType, newMethodDiag, MemberAccessFlagsSkipObjectBaseClass)
	if newMethodTypeResult != nil && newMethodTypeResult.TypeErrors {
		evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, newMethodDiag.GetString(), errorNode, nil)
	}

	useConstructorTransform := HasConstructorTransform(classType)

	// The original's comment: if there is a constructor transform, evaluate all
	// arguments speculatively so we can later re-evaluate them in the context of
	// the transform.
	var speculativeNode parser.ParseNode
	if useConstructorTransform {
		speculativeNode = errorNode
	}

	var returnResult *CallResult
	evaluator.UseSpeculativeMode(speculativeNode, func() {
		returnResult = validateNewAndInitMethods(evaluator, errorNode, argList, classType,
			skipUnknownArgCheck, inferenceContext, newMethodTypeResult)
	}, nil)

	validatedArgExpressions := !useConstructorTransform || returnResult.ArgumentErrors

	// The original's comment: apply a constructor transform if applicable.
	if useConstructorTransform {
		if returnResult.ArgumentErrors {
			// The original's comment: if there were errors when validating the __new__
			// and __init__ methods, we need to re-evaluate the arguments to generate
			// error messages because we previously evaluated them speculatively.
			validateNewAndInitMethods(evaluator, errorNode, argList, classType,
				skipUnknownArgCheck, inferenceContext, newMethodTypeResult)

			validatedArgExpressions = true
		} else if returnResult.ReturnType != nil {
			transformed := ApplyConstructorTransform(evaluator, errorNode, argList, classType, &CallResult{
				ArgumentErrors:   returnResult.ArgumentErrors,
				ReturnType:       returnResult.ReturnType,
				IsTypeIncomplete: returnResult.IsTypeIncomplete,
			})

			if transformed != nil {
				returnResult.ReturnType = transformed.ReturnType

				if transformed.IsTypeIncomplete {
					returnResult.IsTypeIncomplete = true
				}

				if transformed.ArgumentErrors {
					returnResult.ArgumentErrors = true
				}

				validatedArgExpressions = true
			}
		}
	}

	// The original's comment: if we weren't able to validate the args, analyze the
	// expressions here to mark symbols referenced and report expression evaluation
	// errors.
	if !validatedArgExpressions {
		for _, arg := range argList {
			if arg.ValueExpression != nil && !evaluator.IsSpeculativeModeInUse(arg.ValueExpression) {
				evaluator.GetTypeOfExpression(arg.ValueExpression, EvalFlagsNone, nil)
			}
		}
	}

	return returnResult
}

// validateNewAndInitMethods corresponds to the function of the same name.
func validateNewAndInitMethods(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	argList []*Arg,
	classType *ClassType,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
	newMethodTypeResult *TypeResult,
) *CallResult {
	var returnType Type
	validatedArgExpressions := false
	argumentErrors := false
	isTypeIncomplete := false
	overloadsUsedForCall := []*FunctionType{}
	var newMethodReturnType Type

	// The original's comment: validate __new__ if it is present.
	if newMethodTypeResult != nil {
		// The original's comment: use speculative mode for arg expressions because
		// we don't know whether we'll need to re-evaluate these expressions later for
		// __init__.
		newCallResult := validateNewMethod(evaluator, errorNode, argList, classType,
			skipUnknownArgCheck, inferenceContext, newMethodTypeResult, true)

		if newCallResult.ArgumentErrors {
			argumentErrors = true
		} else {
			overloadsUsedForCall = append(overloadsUsedForCall, newCallResult.OverloadsUsedForCall...)
		}

		if newCallResult.IsTypeIncomplete {
			isTypeIncomplete = true
		}

		newMethodReturnType = newCallResult.ReturnType
	}

	var newMethodType Type
	if newMethodTypeResult != nil {
		newMethodType = newMethodTypeResult.Type
	}

	if newMethodReturnType == nil || isDefaultNewMethod(newMethodType) {
		// The original's comment: if there is no __new__ method or it uses a default
		// signature, (cls, *args, **kwargs) -> Self, allow the __init__ method to
		// determine the specialized type of the class.
		newMethodReturnType = ClassTypeCloneAsInstance(classType, true)
	} else if IsUnknown(newMethodReturnType) || (newMethodType != nil && IsAny(newMethodType)) {
		// The original's comment: if the __new__ method returns Unknown, we'll ignore
		// its return type and assume that it returns Self.
		newMethodReturnType = ApplySolvedTypeVars(ClassTypeCloneAsInstance(classType, true),
			NewConstraintSolution(nil), &ApplyTypeVarOptions{
				ReplaceUnsolved: &ReplaceUnsolvedOptions{
					ScopeIDs:       GetTypeVarScopeIDs(classType),
					TupleClassType: evaluator.GetTupleClassType(),
				},
			})
	}

	var initMethodTypeResult *TypeResult

	// The original's comment: if there were errors evaluating the __new__ method,
	// assume that __new__ returns the class instance and proceed accordingly. This
	// may produce false positives in some cases, but it will prevent false
	// negatives if the __init__ method also produces type errors (perhaps unrelated
	// to the errors in the __new__ method).
	if argumentErrors {
		initMethodTypeResult = &TypeResult{Type: ConvertToInstance(classType, true)}
	}

	// The original's comment: validate __init__ if it's present.
	if !IsNever(newMethodReturnType) &&
		!shouldSkipInitEvaluation(evaluator, classType, newMethodReturnType) &&
		IsClassInstance(newMethodReturnType) {
		// The original's comment: if the __new__ method returned the same type as the
		// class it's constructing but didn't supply solved type arguments, we'll
		// ignore its specialized return type and rely on the __init__ method to supply
		// the type arguments instead.
		initMethodBindToType := newMethodReturnType.(*ClassType)
		if hasUnknownTypeArg(initMethodBindToType) {
			initMethodBindToType = ClassTypeCloneAsInstance(classType, true)
		}

		// The original's comment: determine whether the class overrides the
		// object.__init__ method.
		initMethodDiag := common.NewDiagnosticAddendum()
		initMethodTypeResult = GetBoundInitMethod(evaluator, errorNode, initMethodBindToType, initMethodDiag, MemberAccessFlagsSkipObjectBaseClass)
		if initMethodTypeResult != nil && initMethodTypeResult.TypeErrors {
			evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				initMethodDiag.GetString(), errorNode, nil)
		}

		if initMethodTypeResult != nil {
			initCallResult := validateInitMethod(evaluator, errorNode, argList, initMethodBindToType,
				skipUnknownArgCheck, inferenceContext, initMethodTypeResult.Type)

			if initCallResult.ArgumentErrors {
				argumentErrors = true
			} else {
				overloadsUsedForCall = append(overloadsUsedForCall, initCallResult.OverloadsUsedForCall...)
			}

			if initCallResult.IsTypeIncomplete {
				isTypeIncomplete = true
			}

			returnType = initCallResult.ReturnType
			validatedArgExpressions = true
			skipUnknownArgCheck = true
		}
	}

	if !validatedArgExpressions && newMethodTypeResult != nil {
		// The original's comment: if we skipped the __init__ method and the __new__
		// method was evaluated only speculatively, evaluate it non-speculatively now
		// so we can report errors.
		if !evaluator.IsSpeculativeModeInUse(errorNode) {
			validateNewMethod(evaluator, errorNode, argList, classType,
				skipUnknownArgCheck, inferenceContext, newMethodTypeResult, false)
		}

		validatedArgExpressions = true
		returnType = newMethodReturnType
	}

	// The original's comment: if the class doesn't override object.__new__ or
	// object.__init__, use the fallback constructor type evaluation for the
	// `object` class.
	if newMethodTypeResult == nil && initMethodTypeResult == nil {
		callResult := validateFallbackConstructorCall(evaluator, errorNode, argList, classType, inferenceContext)

		if callResult.ArgumentErrors {
			argumentErrors = true
		} else {
			overloadsUsedForCall = append(overloadsUsedForCall, callResult.OverloadsUsedForCall...)
		}

		if callResult.IsTypeIncomplete {
			isTypeIncomplete = true
		}

		returnType = callResult.ReturnType
		if returnType == nil {
			returnType = UnknownTypeCreate(false)
		}
	}

	return &CallResult{
		ArgumentErrors:       argumentErrors,
		ReturnType:           returnType,
		IsTypeIncomplete:     isTypeIncomplete,
		OverloadsUsedForCall: overloadsUsedForCall,
	}
}

// hasUnknownTypeArg is the original's `typeArgs.some((typeArg) => isUnknown(typeArg))`.
func hasUnknownTypeArg(classType *ClassType) bool {
	for _, typeArg := range classType.Priv.TypeArgs {
		if IsUnknown(typeArg) {
			return true
		}
	}
	return false
}

// validateNewMethod corresponds to the function of the same name.
//
// Its comment: evaluates the __new__ method for type correctness. If
// useSpeculativeModeForArgs is true, use speculative mode to evaluate the
// arguments (unless an argument error is produced, in which case it's OK to use
// speculative mode).
func validateNewMethod(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	argList []*Arg,
	classType *ClassType,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
	newMethodTypeResult *TypeResult,
	useSpeculativeModeForArgs bool,
) *CallResult {
	var newReturnType Type
	isTypeIncomplete := false
	argumentErrors := false
	overloadsUsedForCall := []*FunctionType{}

	constraints := NewConstraintTracker()

	var speculativeNode parser.ParseNode
	if useSpeculativeModeForArgs {
		speculativeNode = errorNode
	}

	var callResult *CallResult
	evaluator.UseSpeculativeMode(speculativeNode, func() {
		callResult = evaluator.ValidateCallArgs(errorNode, argList, newMethodTypeResult,
			constraints, skipUnknownArgCheck, inferenceContext)
	}, &SpeculativeModeOptions{DependentType: newMethodTypeResult.Type})

	if callResult.IsTypeIncomplete {
		isTypeIncomplete = true
	}

	if callResult.ArgumentErrors {
		argumentErrors = true

		// The original's comment: evaluate the arguments in a non-speculative manner
		// to generate any diagnostics.
		evaluator.ValidateCallArgs(errorNode, argList, newMethodTypeResult,
			constraints, skipUnknownArgCheck, inferenceContext)
	} else {
		newReturnType = callResult.ReturnType

		if len(overloadsUsedForCall) == 0 {
			overloadsUsedForCall = append(overloadsUsedForCall, callResult.OverloadsUsedForCall...)
		}
	}

	if newReturnType != nil {
		// The original's comment: special-case the 'tuple' type specialization to use
		// the homogenous arbitrary-length form.
		if returnClass, ok := newReturnType.(*ClassType); ok && IsClassInstance(newReturnType) &&
			IsTupleClass(returnClass) && returnClass.Priv.TupleTypeArgs == nil {
			if len(returnClass.Priv.TypeArgs) == 1 {
				returnClass = SpecializeTupleClass(returnClass,
					[]*TupleTypeArg{{Type: returnClass.Priv.TypeArgs[0], IsUnbounded: true}}, true, false)
			}

			newReturnType = applyExpectedTypeForTupleConstructor(returnClass, inferenceContext)
		}
	} else {
		newReturnType = applyExpectedTypeForConstructor(evaluator, classType, inferenceContext, constraints)
	}

	return &CallResult{
		ArgumentErrors:       argumentErrors,
		ReturnType:           newReturnType,
		IsTypeIncomplete:     isTypeIncomplete,
		OverloadsUsedForCall: overloadsUsedForCall,
	}
}

// validateInitMethod corresponds to the function of the same name.
func validateInitMethod(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	argList []*Arg,
	classType *ClassType,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
	initMethodType Type,
) *CallResult {
	isTypeIncomplete := false
	argumentErrors := false
	overloadsUsedForCall := []*FunctionType{}

	constraints := NewConstraintTracker()
	if classType.Priv.TypeArgs != nil {
		AddConstraintsForExpectedType(evaluator, classType, classType, constraints, nil, 0)
	}

	returnTypeOverride := SelfSpecializeClass(classType, nil)
	var effectiveContext *InferenceContext
	if inferenceContext != nil {
		copied := *inferenceContext
		copied.ReturnTypeOverride = returnTypeOverride
		effectiveContext = &copied
	}

	callResult := evaluator.ValidateCallArgs(errorNode, argList, &TypeResult{Type: initMethodType},
		constraints, skipUnknownArgCheck, effectiveContext)

	// The original's comment: overload evaluation keeps the ordinary __init__
	// return as a placeholder and carries argument-dependent constructed types
	// through this separate field, including when union-expanded argument lists are
	// combined.
	var returnType Type
	if callResult.SpecializedInitSelfType != nil {
		returnType = MapSubtypes(callResult.SpecializedInitSelfType,
			func(specializedInitSelfSubtype Type) Type {
				adjustedClassType := classType
				if subtypeClass, ok := specializedInitSelfSubtype.(*ClassType); ok &&
					IsClassInstance(specializedInitSelfSubtype) &&
					ClassTypeIsSameGenericClass(subtypeClass, adjustedClassType, 0) {
					adjustedClassType = ClassTypeCloneAsInstantiable(subtypeClass, false)
				}

				if !boolValue(classType.Priv.IsTypeArgExplicit) &&
					(IsAny(specializedInitSelfSubtype) || IsUnknown(specializedInitSelfSubtype)) {
					return specializedInitSelfSubtype
				}

				return applyExpectedTypeForConstructor(evaluator, adjustedClassType, nil, constraints)
			}, nil)
	} else {
		returnType = applyExpectedTypeForConstructor(evaluator, classType, nil, constraints)
	}

	if callResult.IsTypeIncomplete {
		isTypeIncomplete = true
	}

	if callResult.ArgumentErrors {
		argumentErrors = true
	} else {
		overloadsUsedForCall = append(overloadsUsedForCall, callResult.OverloadsUsedForCall...)
	}

	return &CallResult{
		ArgumentErrors:       argumentErrors,
		ReturnType:           returnType,
		IsTypeIncomplete:     isTypeIncomplete,
		OverloadsUsedForCall: overloadsUsedForCall,
	}
}

// validateFallbackConstructorCall corresponds to the function of the same name:
// the class overrides neither __new__ nor __init__, so object.__new__ is bound
// and validated against.
func validateFallbackConstructorCall(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	argList []*Arg,
	classType *ClassType,
	inferenceContext *InferenceContext,
) *CallResult {
	// The original's comment: bind the __new__ method from the object class.
	var newMethodType Type
	if result := GetBoundNewMethod(evaluator, errorNode, classType, nil, MemberAccessFlagsDefault); result != nil {
		newMethodType = result.Type
	}

	// The original's comment: if there was no object.__new__ or it's not a
	// callable, then something has gone terribly wrong in the typeshed stubs. To
	// avoid crashing, simply return the instance.
	if newMethodType == nil || !IsFunctionOrOverloaded(newMethodType) {
		return &CallResult{ReturnType: ConvertToInstance(classType, true)}
	}

	return validateNewMethod(evaluator, errorNode, argList, classType, false, inferenceContext,
		&TypeResult{Type: newMethodType}, false)
}

// validateMetaclassCall corresponds to the function of the same name. It returns
// nil where the original returns undefined, meaning "the metaclass does not
// override construction".
func validateMetaclassCall(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	argList []*Arg,
	classType *ClassType,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
	useSpeculativeModeForArgs bool,
) *CallResult {
	metaclassCallMethodInfo := GetBoundCallMethod(evaluator, errorNode, classType)

	if metaclassCallMethodInfo == nil {
		return nil
	}

	var speculativeNode parser.ParseNode
	if useSpeculativeModeForArgs {
		speculativeNode = errorNode
	}

	var callResult *CallResult
	evaluator.UseSpeculativeMode(speculativeNode, func() {
		callResult = evaluator.ValidateCallArgs(errorNode, argList, metaclassCallMethodInfo,
			nil, skipUnknownArgCheck, inferenceContext)
	}, nil)

	if !callResult.ArgumentErrors {
		// The original's comment: if the return type is unannotated, don't use the
		// inferred return type.
		if fn, ok := metaclassCallMethodInfo.Type.(*FunctionType); ok && fn.Shared.DeclaredReturnType == nil {
			return nil
		}

		// The original's comment: if the return type is unknown, ignore it.
		if callResult.ReturnType != nil && IsUnknown(callResult.ReturnType) {
			return nil
		}
	}

	return callResult
}

// applyExpectedSubtypeForConstructor corresponds to the function of the same
// name. It returns nil where the original returns undefined.
func applyExpectedSubtypeForConstructor(
	evaluator TypeEvaluator,
	classType *ClassType,
	expectedSubtype Type,
	constraints *ConstraintTracker,
) Type {
	specializedType := evaluator.SolveAndApplyConstraints(
		ClassTypeCloneAsInstance(classType, true), constraints, &ApplyTypeVarOptions{
			ReplaceUnsolved: &ReplaceUnsolvedOptions{
				ScopeIDs:       []TypeVarScopeId{},
				TupleClassType: evaluator.GetTupleClassType(),
			},
		}, nil)

	if !evaluator.AssignType(expectedSubtype, specializedType, nil, nil, AssignTypeFlagsDefault, 0) {
		return nil
	}

	// The original's comment: if the expected type is "Any", transform it to an Any.
	if IsAny(expectedSubtype) {
		return expectedSubtype
	}

	return specializedType
}

// applyExpectedTypeForConstructor corresponds to the function of the same name.
//
// Its comment: handles the case where a constructor is a generic type and the
// type arguments are not specified but can be provided by the expected type.
func applyExpectedTypeForConstructor(
	evaluator TypeEvaluator,
	classType *ClassType,
	inferenceContext *InferenceContext,
	constraints *ConstraintTracker,
) Type {
	defaultIfNotFound := true

	// The original's comment: if this isn't a generic type or it's a type that has
	// already been explicitly specialized, the expected type isn't applicable.
	if len(classType.Shared.TypeParams) == 0 || classType.Priv.TypeArgs != nil {
		return evaluator.SolveAndApplyConstraints(ClassTypeCloneAsInstance(classType, true),
			constraints, &ApplyTypeVarOptions{
				ReplaceUnsolved: &ReplaceUnsolvedOptions{
					ScopeIDs:       []TypeVarScopeId{},
					TupleClassType: evaluator.GetTupleClassType(),
				},
			}, nil)
	}

	if inferenceContext != nil {
		specializedExpectedType := MapSubtypes(inferenceContext.ExpectedType, func(expectedSubtype Type) Type {
			return applyExpectedSubtypeForConstructor(evaluator, classType, expectedSubtype, constraints)
		}, nil)

		if !IsNever(specializedExpectedType) {
			return specializedExpectedType
		}

		// The original's comment: if the expected type didn't provide TypeVar values,
		// remaining unsolved TypeVars should be considered Unknown unless they were
		// provided explicitly in the constructor call.
		//
		// This test can never pass: the enclosing branch already returned when
		// typeArgs was non-nil. Faithfully reproduced.
		if classType.Priv.TypeArgs != nil {
			defaultIfNotFound = false
		}
	}

	var applyOptions *ApplyTypeVarOptions
	if defaultIfNotFound {
		applyOptions = &ApplyTypeVarOptions{
			ReplaceUnsolved: &ReplaceUnsolvedOptions{
				ScopeIDs:       GetTypeVarScopeIDs(classType),
				TupleClassType: evaluator.GetTupleClassType(),
			},
		}
	} else {
		applyOptions = &ApplyTypeVarOptions{}
	}

	specializedType := evaluator.SolveAndApplyConstraints(classType, constraints, applyOptions, nil)
	if specializedClass, ok := specializedType.(*ClassType); ok {
		return ClassTypeCloneAsInstance(specializedClass, true)
	}
	return specializedType
}

// applyExpectedTypeForTupleConstructor corresponds to the function of the same
// name.
//
// Its comment: similar to applyExpectedTypeForConstructor, this function handles
// the special case of the tuple class.
func applyExpectedTypeForTupleConstructor(classType *ClassType, inferenceContext *InferenceContext) Type {
	if inferenceContext == nil {
		return classType
	}

	expectedClass, ok := inferenceContext.ExpectedType.(*ClassType)
	if !ok || !IsClassInstance(inferenceContext.ExpectedType) ||
		!IsTupleClass(expectedClass) || expectedClass.Priv.TupleTypeArgs == nil {
		return classType
	}

	return SpecializeTupleClass(classType, expectedClass.Priv.TupleTypeArgs, true, false)
}

// shouldSkipNewAndInitEvaluation corresponds to the function of the same name.
func shouldSkipNewAndInitEvaluation(
	evaluator TypeEvaluator, classType *ClassType, callMethodReturnType Type,
) bool {
	if !evaluator.AssignType(ConvertToInstance(classType, true), callMethodReturnType,
		nil, nil, AssignTypeFlagsDefault, 0) ||
		IsNever(callMethodReturnType) ||
		FindSubtype(callMethodReturnType, func(subtype Type) bool { return IsAny(subtype) }) != nil {
		return true
	}

	// The original's comment: handle the special case of an enum class, where the
	// __new__ and __init__ methods are replaced at runtime by the metaclass.
	return ClassTypeIsEnumClass(classType)
}

// shouldSkipInitEvaluation corresponds to the function of the same name.
//
// Its comment: if __new__ returns a type that is not an instance of the class,
// skip the __init__ method evaluation. This is consistent with the behavior of
// the type.__call__ runtime behavior.
func shouldSkipInitEvaluation(
	evaluator TypeEvaluator, classType *ClassType, newMethodReturnType Type,
) bool {
	returnType := evaluator.MakeTopLevelTypeVarsConcrete(newMethodReturnType, false)

	skipInitCheck := false
	DoForEachSubtype(returnType, func(subtype Type, _ int, _ []Type) {
		if IsUnknown(subtype) {
			return
		}

		if IsClassInstance(subtype) {
			inheritanceChain := []Type{}
			if !ClassTypeIsDerivedFrom(ClassTypeCloneAsInstantiable(subtype.(*ClassType), false),
				classType, &inheritanceChain) {
				skipInitCheck = true
			}
			return
		}

		skipInitCheck = true
	})

	return skipInitCheck
}

// isDefaultNewMethod corresponds to the function of the same name.
//
// Its comment: determine whether the __new__ method is the placeholder signature
// of "def __new__(cls, *args, **kwargs) -> Self" or
// "def __new__(cls, /, *args, **kwargs) -> Self".
func isDefaultNewMethod(newMethod Type) bool {
	fn, ok := newMethod.(*FunctionType)
	if newMethod == nil || !ok || !IsFunction(newMethod) {
		return false
	}

	params := fn.Shared.Parameters

	// The original's comment: after binding, cls is stripped. A positional-only
	// separator may remain if the original signature was
	// "def __new__(cls, /, *args, **kwargs)". Skip the separator when checking for
	// the default pattern.
	if len(params) > 0 && IsPositionOnlySeparator(params[0]) {
		params = params[1:]
	}

	if len(params) != 2 {
		return false
	}

	if params[0].Category != parser.ParamCategoryArgsList ||
		params[1].Category != parser.ParamCategoryKwargsDict {
		return false
	}

	returnType := fn.Shared.DeclaredReturnType
	if returnType == nil {
		if fn.Priv.SpecializedTypes != nil && fn.Priv.SpecializedTypes.ReturnType != nil {
			returnType = fn.Priv.SpecializedTypes.ReturnType
		} else if fn.Shared.InferredReturnType != nil {
			returnType = fn.Shared.InferredReturnType.Type
		}
	}

	tv, isTypeVar := returnType.(*TypeVarType)
	return returnType != nil && isTypeVar && IsTypeVar(returnType) && TypeVarTypeIsSelf(tv)
}

// HasConstructorTransform corresponds to the constructorTransform.ts function of
// the same name. Only `functools.partial` has one.
func HasConstructorTransform(classType *ClassType) bool {
	return classType.Shared.FullName == "functools.partial"
}
