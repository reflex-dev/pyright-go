/*
 * constructors_synthesize.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/constructors.ts (pyright 1.1.412):
 * createFunctionFromConstructor, createFunctionFromMetaclassCall,
 * createFunctionFromNewMethod, createFunctionFromObjectNewMethod and
 * createFunctionFromInitMethod.
 *
 * These answer "what callable is this class, viewed as a function?" -- the
 * question that arises when a class is passed where a `Callable` is expected, or
 * used as a converter, rather than when it is called directly. Calling directly
 * goes through validateConstructorArgs; this builds a standalone signature.
 *
 * The three sources are consulted in a fixed order that mirrors what happens at
 * runtime. A metaclass `__call__` intercepts construction entirely, so it wins --
 * but only when it returns something other than an instance of the class, since
 * the ordinary `type.__call__` just forwards to `__new__`/`__init__` and would
 * tell us nothing. Then `__new__`, which likewise short-circuits `__init__` when
 * it returns an unrelated type. Then `__init__`.
 *
 * When both `__new__` and `__init__` are informative the result is a *union* of
 * the two signatures rather than a choice between them, because either may be
 * the one that constrains a given call. The exception is a `__new__` inherited
 * unchanged from `object`, which carries no information and is discarded.
 *
 * Two details in createFunctionFromInitMethod are easy to lose. The return type
 * comes from `strippedFirstParamType` when binding produced one -- that is the
 * actual `self` type, which matters for a `__init__` annotated with a
 * constrained Self. And a generic class with no type arguments is
 * self-specialized by seeding the solver with only those type variables that
 * *appear in parameters*, so that an unused parameter falls back to its default
 * rather than being solved to Unknown.
 */

package analyzer

// CreateFunctionFromConstructor corresponds to createFunctionFromConstructor. It
// returns nil where the original returns undefined.
func CreateFunctionFromConstructor(
	evaluator TypeEvaluator, classType *ClassType, selfType Type, recursionCount int,
) Type {
	if fromMetaclassCall := createFunctionFromMetaclassCall(
		evaluator, classType, recursionCount); fromMetaclassCall != nil {
		return fromMetaclassCall
	}

	fromNew := createFunctionFromNewMethod(evaluator, classType, selfType, recursionCount)

	if fromNew != nil {
		skipInitMethod := false

		DoForEachSignature(fromNew, func(signature *FunctionType, _ int) {
			newMethodReturnType := FunctionTypeGetEffectiveReturnType(signature, true)
			if !IsNilType(newMethodReturnType) &&
				shouldSkipInitEvaluation(evaluator, classType, newMethodReturnType) {
				skipInitMethod = true
			}
		})

		if skipInitMethod {
			return fromNew
		}
	}

	fromInit := createFunctionFromInitMethod(evaluator, classType, selfType, recursionCount)

	// The original's comment: if there is a valid __init__ method and the __new__
	// method is the default __new__ method provided by the object class, discard
	// the __new__ method.
	if fromInit != nil && fromNew != nil && isDefaultNewMethod(fromNew) {
		fromNew = nil
	}

	// The original's comment: if there is both a __new__ and __init__ method,
	// return a union comprised of both resulting function types.
	if fromNew != nil && fromInit != nil {
		return CombineTypes([]Type{fromInit, fromNew}, nil)
	}

	if fromNew != nil {
		return fromNew
	}
	if fromInit != nil {
		return fromInit
	}

	return createFunctionFromObjectNewMethod(classType)
}

// createFunctionFromMetaclassCall corresponds to the function of the same name.
func createFunctionFromMetaclassCall(
	evaluator TypeEvaluator, classType *ClassType, recursionCount int,
) Type {
	metaclass := classType.Shared.EffectiveMetaclass
	if metaclass == nil || !IsClass(metaclass) {
		return nil
	}

	callInfo := LookUpClassMember(metaclass.(*ClassType), "__call__",
		MemberAccessFlagsSkipInstanceMembers|MemberAccessFlagsSkipTypeBaseClass|
			MemberAccessFlagsSkipAttributeAccessOverride, nil)

	if callInfo == nil {
		return nil
	}

	callType := evaluator.GetTypeOfMember(callInfo)
	if !IsFunctionOrOverloaded(callType) {
		return nil
	}

	var memberClass *ClassType
	if IsInstantiableClass(callInfo.ClassType) {
		memberClass = callInfo.ClassType.(*ClassType)
	}

	boundCallType := evaluator.BindFunctionToClassOrObject(
		classType, callType, memberClass, false, classType, nil, recursionCount)

	if IsNilType(boundCallType) {
		return nil
	}

	useMetaclassCall := false

	// The original's comment: look at the signatures of all the __call__ methods to
	// determine whether any of them returns something other than the instance of
	// the class being constructed. An ordinary type.__call__ forwards to
	// __new__/__init__ and so tells us nothing.
	DoForEachSignature(boundCallType, func(signature *FunctionType, _ int) {
		if signature.Shared.DeclaredReturnType != nil {
			returnType := FunctionTypeGetEffectiveReturnType(signature, true)
			if !IsNilType(returnType) &&
				shouldSkipNewAndInitEvaluation(evaluator, classType, returnType) {
				useMetaclassCall = true
			}
		}
	})

	if useMetaclassCall {
		return boundCallType
	}
	return nil
}

// createFunctionFromNewMethod corresponds to the function of the same name.
func createFunctionFromNewMethod(
	evaluator TypeEvaluator, classType *ClassType, selfType Type, recursionCount int,
) Type {
	newInfo := LookUpClassMember(classType, "__new__",
		MemberAccessFlagsSkipInstanceMembers|MemberAccessFlagsSkipAttributeAccessOverride|
			MemberAccessFlagsSkipObjectBaseClass, nil)

	if newInfo == nil {
		return nil
	}

	newType := evaluator.GetTypeOfMember(newInfo)

	convertNewToConstructor := func(newSubtype *FunctionType) *FunctionType {
		// The original's comment: if there are no parameters that include
		// class-scoped type parameters, self-specialize the class because the type
		// arguments for the class can't be solved if there are no parameters to
		// supply them.
		hasParamsWithTypeVars := false
		for index, param := range newSubtype.Shared.Parameters {
			if index == 0 || param.Name == nil {
				continue
			}

			paramType := FunctionTypeGetParamType(newSubtype, index)
			for _, typeVar := range GetTypeVarArgsRecursive(paramType, 0) {
				if typeVar.Priv.ScopeID == GetTypeVarScopeID(classType) {
					hasParamsWithTypeVars = true
					break
				}
			}
			if hasParamsWithTypeVars {
				break
			}
		}

		bindTo := classType
		if hasParamsWithTypeVars {
			bindTo = SelfSpecializeClass(classType, nil)
		}

		var memberClass *ClassType
		if IsInstantiableClass(newInfo.ClassType) {
			memberClass = newInfo.ClassType.(*ClassType)
		}

		boundNew := evaluator.BindFunctionToClassOrObject(
			bindTo, newSubtype, memberClass, true, selfType, nil, recursionCount)

		if IsNilType(boundNew) {
			return nil
		}
		boundNewFn, ok := boundNew.(*FunctionType)
		if !ok {
			return nil
		}

		convertedNew := FunctionTypeClone(boundNewFn, false, nil)
		convertedNew.Shared.TypeVarScopeID = newSubtype.Shared.TypeVarScopeID

		if convertedNew.Shared.DocString == nil && classType.Shared.DocString != nil {
			convertedNew.Shared.DocString = classType.Shared.DocString
		}

		convertedNew.Shared.Flags &^= FunctionTypeFlagsStaticMethod | FunctionTypeFlagsConstructorMethod
		convertedNew.Priv.ConstructorTypeVarScopeID = GetTypeVarScopeID(classType)

		return convertedNew
	}

	if IsFunction(newType) {
		converted := convertNewToConstructor(newType.(*FunctionType))
		if converted == nil {
			return nil
		}
		return converted
	}

	if !IsOverloaded(newType) {
		return nil
	}

	newOverloads := []*FunctionType{}
	for _, overload := range OverloadedTypeGetOverloads(newType.(*OverloadedType)) {
		if converted := convertNewToConstructor(overload); converted != nil {
			newOverloads = append(newOverloads, converted)
		}
	}

	if len(newOverloads) == 0 {
		return nil
	}

	if len(newOverloads) == 1 {
		return newOverloads[0]
	}

	return OverloadedTypeCreate(newOverloads, nil)
}

// createFunctionFromObjectNewMethod corresponds to the function of the same name.
// The original's comment: return a fallback constructor based on the
// object.__new__ method.
func createFunctionFromObjectNewMethod(classType *ClassType) *FunctionType {
	constructorFunction := FunctionTypeCreateSynthesizedInstance("__new__", FunctionTypeFlagsNone)
	constructorFunction.Shared.DeclaredReturnType = ClassTypeCloneAsInstance(classType, false)

	// The original's comment: if this is type[T] or a protocol, we don't know what
	// parameters are accepted by the constructor, so add the default parameters.
	if classType.Priv.IncludeSubclasses || ClassTypeIsProtocolClass(classType) {
		FunctionTypeAddDefaultParams(constructorFunction, false)
	}

	if constructorFunction.Shared.DocString == nil && classType.Shared.DocString != nil {
		constructorFunction.Shared.DocString = classType.Shared.DocString
	}

	return constructorFunction
}

// createFunctionFromInitMethod corresponds to the function of the same name.
func createFunctionFromInitMethod(
	evaluator TypeEvaluator, classType *ClassType, selfType Type, recursionCount int,
) Type {
	// The original's comment: use the __init__ method if available. It's usually
	// more detailed.
	initInfo := LookUpClassMember(classType, "__init__",
		MemberAccessFlagsSkipInstanceMembers|MemberAccessFlagsSkipAttributeAccessOverride|
			MemberAccessFlagsSkipObjectBaseClass, nil)

	if initInfo == nil {
		return nil
	}

	initType := evaluator.GetTypeOfMember(initInfo)
	objectType := ClassTypeCloneAsInstance(classType, false)

	convertInitToConstructor := func(initSubtype *FunctionType) *FunctionType {
		var memberClass *ClassType
		if IsInstantiableClass(initInfo.ClassType) {
			memberClass = initInfo.ClassType.(*ClassType)
		}

		boundInit := evaluator.BindFunctionToClassOrObject(
			objectType, initSubtype, memberClass, false, selfType, nil, recursionCount)

		if IsNilType(boundInit) {
			return nil
		}
		boundInitFn, ok := boundInit.(*FunctionType)
		if !ok {
			return nil
		}

		convertedInit := FunctionTypeClone(boundInitFn, false, nil)
		returnType := selfType
		if IsNilType(returnType) {
			returnType = initConstructorReturnType(evaluator, objectType, convertedInit)
		}

		// The stripped first parameter is the real bound `self` type, which matters
		// when __init__ is annotated with a constrained Self.
		if !IsNilType(boundInitFn.Priv.StrippedFirstParamType) {
			convertedInit.Shared.DeclaredReturnType = boundInitFn.Priv.StrippedFirstParamType
		} else {
			convertedInit.Shared.DeclaredReturnType = returnType
		}

		if convertedInit.Priv.SpecializedTypes != nil {
			convertedInit.Priv.SpecializedTypes.ReturnType = returnType
		}

		if convertedInit.Shared.DocString == nil && classType.Shared.DocString != nil {
			convertedInit.Shared.DocString = classType.Shared.DocString
		}

		convertedInit.Shared.Flags &^= FunctionTypeFlagsStaticMethod
		convertedInit.Priv.ConstructorTypeVarScopeID = GetTypeVarScopeID(classType)

		return convertedInit
	}

	if IsFunction(initType) {
		converted := convertInitToConstructor(initType.(*FunctionType))
		if converted == nil {
			return nil
		}
		return converted
	}

	if !IsOverloaded(initType) {
		return nil
	}

	initOverloads := []*FunctionType{}
	for _, overload := range OverloadedTypeGetOverloads(initType.(*OverloadedType)) {
		if converted := convertInitToConstructor(overload); converted != nil {
			initOverloads = append(initOverloads, converted)
		}
	}

	if len(initOverloads) == 0 {
		return nil
	}

	if len(initOverloads) == 1 {
		return initOverloads[0]
	}

	return OverloadedTypeCreate(initOverloads, nil)
}

// initConstructorReturnType is the original's self-specialization block inside
// convertInitToConstructor.
func initConstructorReturnType(
	evaluator TypeEvaluator, objectType *ClassType, convertedInit *FunctionType,
) Type {
	// The original's comment: if this is a generic type, self-specialize the class
	// (i.e. fill in its own type parameters as type arguments).
	if len(objectType.Shared.TypeParams) == 0 || objectType.Priv.TypeArgs != nil {
		return objectType
	}

	constraints := NewConstraintTracker()

	// The original's comment: if a TypeVar is not used in any of the parameter
	// types, it should take on its default value (typically Unknown) in the
	// resulting specialized type. Seeding the solver only with the variables that
	// do appear is what produces that.
	typeVarsInParams := []*TypeVarType{}

	for index := range convertedInit.Shared.Parameters {
		paramType := FunctionTypeGetParamType(convertedInit, index)
		typeVarsInParams = AddTypeVarsToListIfUnique(typeVarsInParams, GetTypeVarArgsRecursive(paramType, 0), "")
	}

	for _, typeVar := range typeVarsInParams {
		constraints.SetBounds(typeVar, typeVar, nil, false)
	}

	return evaluator.SolveAndApplyConstraints(objectType, constraints, &ApplyTypeVarOptions{
		ReplaceUnsolved: &ReplaceUnsolvedOptions{
			ScopeIDs:       GetTypeVarScopeIDs(objectType),
			TupleClassType: evaluator.GetTupleClassType(),
		},
	}, nil)
}

// createFunctionFromConstructorForEvaluator adapts the evaluator method signature
// to the exported function.
func (e *typeEvaluator) createFunctionFromConstructor(
	classType *ClassType, selfType Type, recursionCount int,
) Type {
	return CreateFunctionFromConstructor(e, classType, selfType, recursionCount)
}
