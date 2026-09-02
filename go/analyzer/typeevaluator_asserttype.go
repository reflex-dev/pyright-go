/*
 * typeevaluator_asserttype.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * normalizeEnumTypes, getTypeOfAssertType, getTypeOfLambdaForCall,
 * createAsyncFunction and isTypeComparable.
 *
 * getTypeOfAssertType is the reason normalizeEnumTypes exists. assert_type
 * demands exact structural identity rather than assignability, so it compares
 * with isTypeSame -- but assignType, as it recurses, applies
 * expandEnumTypeForLiteralComparison, which means two types that assignType
 * would call equivalent can differ under isTypeSame purely in how an enum
 * literal is spelled. normalizeEnumTypes puts both sides through the same
 * expansion before the second comparison, keeping the equivalence relation
 * aligned. It recurses structurally -- through unions, function parameters and
 * returns, overloads, tuple arguments and ordinary type arguments -- and
 * rebuilds a type only when something below it actually changed.
 *
 * isTypeComparable answers "could `x == y` ever be true", and its shape is a
 * cascade of reasons to say yes. Anything callable is comparable, because the
 * other operand might be a callable object. Two classes are comparable if
 * either assigns to the other generically, or if a metaclass or class __eq__
 * exists. The bool/int case is the one carve-out: they are disjoint builtins
 * that nonetheless compare equal for the values 0 and 1, so literal values are
 * examined rather than just the classes.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// normalizeEnumTypes corresponds to the function of the same name.
func (e *typeEvaluator) normalizeEnumTypes(
	typeToNormalize Type, literalEnumClasses []*ClassType, recursionCount int,
) Type {
	if recursionCount > MaxTypeRecursionCount {
		return typeToNormalize
	}
	recursionCount++

	typeToNormalize = e.expandEnumTypeForLiteralClasses(typeToNormalize, literalEnumClasses)

	if IsUnion(typeToNormalize) {
		return MapSubtypes(typeToNormalize, func(subtype Type) Type {
			return e.normalizeEnumTypes(subtype, literalEnumClasses, recursionCount)
		}, nil)
	}

	if IsFunction(typeToNormalize) {
		fn := typeToNormalize.(*FunctionType)
		typeChanged := false

		parameterTypes := make([]Type, 0, len(fn.Shared.Parameters))
		for index := range fn.Shared.Parameters {
			parameterType := FunctionTypeGetParamType(fn, index)
			normalizedType := e.normalizeEnumTypes(parameterType, literalEnumClasses, recursionCount)
			if normalizedType != parameterType {
				typeChanged = true
			}
			parameterTypes = append(parameterTypes, normalizedType)
		}

		returnType := FunctionTypeGetEffectiveReturnType(fn, false)
		var normalizedReturnType Type
		if returnType != nil {
			normalizedReturnType = e.normalizeEnumTypes(returnType, literalEnumClasses, recursionCount)
		}
		if normalizedReturnType != returnType {
			typeChanged = true
		}

		if !typeChanged {
			return typeToNormalize
		}

		var paramDefaultTypes []Type
		if fn.Priv.SpecializedTypes != nil {
			paramDefaultTypes = fn.Priv.SpecializedTypes.ParameterDefaultTypes
		}

		return FunctionTypeSpecialize(fn, &SpecializedFunctionTypes{
			ParameterTypes:        parameterTypes,
			ParameterDefaultTypes: paramDefaultTypes,
			ReturnType:            normalizedReturnType,
		})
	}

	if IsOverloaded(typeToNormalize) {
		overloadedType := typeToNormalize.(*OverloadedType)
		typeChanged := false

		originalOverloads := OverloadedTypeGetOverloads(overloadedType)
		overloads := make([]*FunctionType, 0, len(originalOverloads))
		for _, overload := range originalOverloads {
			normalizedType := e.normalizeEnumTypes(overload, literalEnumClasses, recursionCount)
			if normalizedType != Type(overload) {
				typeChanged = true
			}
			if normalizedFn, ok := normalizedType.(*FunctionType); ok {
				overloads = append(overloads, normalizedFn)
			} else {
				overloads = append(overloads, overload)
			}
		}

		implementation := OverloadedTypeGetImplementation(overloadedType)
		var normalizedImplementation Type
		if implementation != nil {
			normalizedImplementation = e.normalizeEnumTypes(
				implementation, literalEnumClasses, recursionCount)
		}
		if normalizedImplementation != implementation {
			typeChanged = true
		}

		if !typeChanged {
			return typeToNormalize
		}
		return OverloadedTypeCreate(overloads, normalizedImplementation)
	}

	if !IsClass(typeToNormalize) || typeToNormalize.(*ClassType).Priv.TypeArgs == nil {
		return typeToNormalize
	}

	cls := typeToNormalize.(*ClassType)

	if cls.Priv.TupleTypeArgs != nil {
		typeChanged := false
		tupleTypeArgs := make([]*TupleTypeArg, 0, len(cls.Priv.TupleTypeArgs))
		for _, typeArg := range cls.Priv.TupleTypeArgs {
			normalizedType := e.normalizeEnumTypes(typeArg.Type, literalEnumClasses, recursionCount)
			if normalizedType != typeArg.Type {
				typeChanged = true
			}

			copied := *typeArg
			copied.Type = normalizedType
			tupleTypeArgs = append(tupleTypeArgs, &copied)
		}

		if !typeChanged {
			return typeToNormalize
		}

		isTypeArgExplicit := cls.Priv.IsTypeArgExplicit != nil && *cls.Priv.IsTypeArgExplicit
		return SpecializeTupleClass(cls, tupleTypeArgs, isTypeArgExplicit, cls.Priv.IsUnpacked)
	}

	typeChanged := false
	typeArgs := make([]Type, 0, len(cls.Priv.TypeArgs))
	for _, typeArg := range cls.Priv.TypeArgs {
		normalizedType := e.normalizeEnumTypes(typeArg, literalEnumClasses, recursionCount)
		if normalizedType != typeArg {
			typeChanged = true
		}
		typeArgs = append(typeArgs, normalizedType)
	}

	if !typeChanged {
		return typeToNormalize
	}
	return ClassTypeSpecialize(cls, typeArgs, cls.Priv.IsTypeArgExplicit, false, nil, nil)
}

// getTypeOfAssertType corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfAssertType(
	node *parser.CallNode, inferenceContext *InferenceContext,
) *TypeResult {
	// The original's condition tests args[0].argCategory twice, almost certainly
	// meaning args[1] the second time. The duplicate is a provable no-op, so
	// collapsing it is behavior-identical; go vet rejects it written out. The
	// consequence of the presumed typo is upstream's: `assert_type(x, *ts)` is
	// accepted here rather than rejected.
	if len(node.D.Args) != 2 ||
		node.D.Args[0].D.ArgCategory != parser.ArgCategorySimple ||
		node.D.Args[0].D.Name != nil ||
		node.D.Args[1].D.Name != nil {
		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.AssertTypeArgs(), node, nil)
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	arg0TypeResult := e.getTypeOfExpression(node.D.Args[0].D.ValueExpr, EvalFlagsNone, inferenceContext)
	if arg0TypeResult.IsIncomplete {
		return &TypeResult{Type: UnknownTypeCreate(true), IsIncomplete: true}
	}

	assertedType := ConvertToInstance(
		e.getTypeOfArgExpectingType(
			e.ConvertNodeToArg(node.D.Args[1]),
			&ExpectedTypeOptions{TypeExpression: true}).Type,
		false)

	// The original's comment: we'll replace TypeGuard and TypeIs with bool for
	// purposes of assert_type testing. The spec is unclear on whether this is the
	// correct behavior, but it seems to be what mypy does -- and what various
	// library authors expect.
	arg0Type := e.StripTypeGuard(arg0TypeResult.Type)

	typeSameOptions := TypeSameOptions{
		TreatAnySameAsUnknown: true,
		IgnorePseudoGeneric:   true,
		IgnoreConditions:      true,
	}
	typesMatch := IsTypeSame(assertedType, arg0Type, typeSameOptions, 0)

	if !typesMatch {
		// The original's comment: unlike assignType, assert_type requires exact
		// structural identity, so normalize both types recursively. Keep this
		// equivalence relation aligned with expandEnumTypeForLiteralComparison,
		// which assignType applies as it recurses.
		literalEnumClasses := e.collectLiteralEnumClasses(assertedType, nil, true, 0)
		literalEnumClasses = e.collectLiteralEnumClasses(arg0Type, literalEnumClasses, true, 0)
		normalizedAssertedType := e.normalizeEnumTypes(assertedType, literalEnumClasses, 0)
		normalizedArg0Type := e.normalizeEnumTypes(arg0Type, literalEnumClasses, 0)
		typesMatch = IsTypeSame(normalizedAssertedType, normalizedArg0Type, typeSameOptions, 0)
	}

	if !typesMatch {
		srcDestTypes := e.printSrcDestTypes(arg0TypeResult.Type, assertedType,
			&PrintTypeOptions{ExpandTypeAlias: true})

		e.AddDiagnostic(DiagnosticRuleReportAssertTypeFailure,
			localization.LocMessage.AssertTypeTypeMismatch().Format(
				srcDestTypes.DestType, srcDestTypes.SourceType),
			node.D.Args[0].D.ValueExpr, nil)
	}

	return &TypeResult{Type: arg0TypeResult.Type}
}

// getTypeOfLambdaForCall corresponds to the function of the same name. The
// original's comment: used where a lambda is defined and immediately called, so
// normal bidirectional inference cannot determine its type -- it is inferred
// from the argument types instead.
func (e *typeEvaluator) getTypeOfLambdaForCall(
	node *parser.CallNode,
	lambdaNode *parser.LambdaNode,
	inferenceContext *InferenceContext,
) *TypeResult {
	expectedType := FunctionTypeCreateSynthesizedInstance("", FunctionTypeFlagsNone)
	if inferenceContext != nil {
		expectedType.Shared.DeclaredReturnType = inferenceContext.ExpectedType
	} else {
		expectedType.Shared.DeclaredReturnType = UnknownTypeCreate(false)
	}

	isArgTypeIncomplete := false
	for index, arg := range node.D.Args {
		argTypeResult := e.getTypeOfExpression(arg.D.ValueExpr, EvalFlagsNone, nil)
		if argTypeResult.IsIncomplete {
			isArgTypeIncomplete = true
		}

		paramName := "p" + itoa(index)
		FunctionTypeAddParam(expectedType, FunctionParamCreate(
			parser.ParamCategorySimple,
			argTypeResult.Type,
			FunctionParamFlagsNameSynthesized|FunctionParamFlagsTypeDeclared,
			&paramName,
			nil,
			nil,
		))
	}

	// The original's comment: if the lambda's param list ends with a "/"
	// positional parameter separator, add a corresponding separator to the
	// expected type.
	lambdaParams := lambdaNode.D.Params
	if len(lambdaParams) > 0 {
		lastParam := lambdaParams[len(lambdaParams)-1]
		if lastParam.D.Category == parser.ParamCategorySimple && lastParam.D.Name == nil {
			FunctionTypeAddPositionOnlyParamSeparator(expectedType)
		}
	}

	var typeResult *TypeResult
	getLambdaType := func() {
		typeResult = e.getTypeOfExpression(lambdaNode, EvalFlagsCallBaseDefaults,
			MakeInferenceContext(expectedType, false, nil))
	}

	// The original's comment: if one or more of the arguments are incomplete, use
	// speculative mode for the lambda evaluation because it may need to be
	// reevaluated once the arg types are complete.
	if isArgTypeIncomplete || e.IsSpeculativeModeInUse(node) ||
		(inferenceContext != nil && inferenceContext.IsTypeIncomplete) {
		e.UseSpeculativeMode(lambdaNode, getLambdaType, nil)
	} else {
		getLambdaType()
	}

	// The original's comment: if bidirectional type inference failed, use normal
	// type inference instead.
	if typeResult.TypeErrors {
		typeResult = e.getTypeOfExpression(lambdaNode, EvalFlagsCallBaseDefaults, nil)
	}

	return typeResult
}

// createAsyncFunction corresponds to the function of the same name. The
// original's comment: clone the original function and replace its return type
// with an Awaitable[<returnType>], marking the new function as no longer async.
func (e *typeEvaluator) createAsyncFunction(
	node *parser.FunctionNode, functionType *FunctionType,
) Type {
	// The original asserts FunctionType.isAsync(functionType) here.
	awaitableFunctionType := FunctionTypeCloneWithNewFlags(functionType,
		functionType.Shared.Flags & ^(FunctionTypeFlagsAsync|FunctionTypeFlagsPartiallyEvaluated))

	isGenerator := FunctionTypeIsGenerator(functionType)

	if functionType.Shared.DeclaredReturnType != nil {
		awaitableFunctionType.Shared.DeclaredReturnType = e.createAwaitableReturnType(
			node, functionType.Shared.DeclaredReturnType, isGenerator, true)
	} else {
		awaitableFunctionType.Shared.InferredReturnType = &InferredReturnTypeInfo{
			Type: e.createAwaitableReturnType(
				node, e.GetInferredReturnType(functionType, nil), isGenerator, true),
		}
	}

	return awaitableFunctionType
}

// IsTypeComparable corresponds to isTypeComparable: could `left == right` ever
// be true? The original's assumeIsOperator default is false; the interface
// always passes it explicitly.
func (e *typeEvaluator) IsTypeComparable(leftType Type, rightType Type, assumeIsOperator bool) bool {
	if IsAnyOrUnknown(leftType) || IsAnyOrUnknown(rightType) {
		return true
	}

	if IsNever(leftType) || IsNever(rightType) {
		return false
	}

	if IsModule(leftType) || IsModule(rightType) {
		return IsTypeSame(leftType, rightType, TypeSameOptions{IgnoreConditions: true}, 0)
	}

	// The original's comment: if either type is a function, assume that it may be
	// comparable. The other operand might be a callable object, an 'object'
	// instance, etc. We could make this more precise for specific cases (e.g. if
	// the other operand is None or a literal or an instance of a nominal class
	// that doesn't override __call__ and is marked final, etc.), but coming up
	// with a comprehensive list is probably not feasible.
	if IsFunctionOrOverloaded(leftType) || IsFunctionOrOverloaded(rightType) {
		return true
	}

	// isTypeOrInstantiable is the original's repeated
	// `isInstantiableClass(t) || (isClassInstance(t) && ClassType.isBuiltIn(t, 'type'))`.
	isTypeOrInstantiable := func(t Type) bool {
		if IsInstantiableClass(t) {
			return true
		}
		return IsClassInstance(t) && ClassTypeIsBuiltInNamed(t.(*ClassType), "type")
	}

	// genericAssignsEitherWay is the original's pair of assignType calls between
	// the two classes stripped of their type arguments.
	genericAssignsEitherWay := func(left *ClassType, right *ClassType) bool {
		genericLeftType := ClassTypeSpecialize(left, nil, nil, false, nil, nil)
		genericRightType := ClassTypeSpecialize(right, nil, nil, false, nil, nil)

		return e.AssignType(genericLeftType, genericRightType, nil, nil, AssignTypeFlagsDefault, 0) ||
			e.AssignType(genericRightType, genericLeftType, nil, nil, AssignTypeFlagsDefault, 0)
	}

	if isTypeOrInstantiable(leftType) {
		leftClass := leftType.(*ClassType)

		if isTypeOrInstantiable(rightType) {
			if genericAssignsEitherWay(leftClass, rightType.(*ClassType)) {
				return true
			}
		}

		// The original's comment: does the class have an operator overload for eq?
		metaclass := leftClass.Shared.EffectiveMetaclass
		if metaclass != nil && IsClass(metaclass) {
			if LookUpClassMember(metaclass.(*ClassType), "__eq__",
				MemberAccessFlagsSkipObjectBaseClass, nil) != nil {
				return true
			}
		}

		return false
	}

	if IsClassInstance(leftType) {
		leftClass := leftType.(*ClassType)

		if IsClass(rightType) {
			rightClass := rightType.(*ClassType)

			if genericAssignsEitherWay(leftClass, rightClass) {
				return true
			}

			// The original's comment: check for the "is None" or "is not None" case.
			if assumeIsOperator && IsNoneInstance(rightType) {
				if IsNoneInstance(leftType) {
					return true
				}

				// The original's comment: the LHS could be a protocol or 'object',
				// in which case None is potentially comparable to it. In other
				// cases, None is not comparable because the types are disjoint.
				return e.AssignType(leftType, rightType, nil, nil, AssignTypeFlagsDefault, 0)
			}

			// The original's comment: assume that if the types are disjoint and
			// built-in classes that they will never be comparable.
			if ClassTypeIsBuiltIn(leftClass) && ClassTypeIsBuiltIn(rightClass) &&
				rightType.Base().IsInstance() {
				// The original's comment: we need to be careful with bool and int
				// literals because they are comparable under certain circumstances.
				var boolType, intType *ClassType
				if ClassTypeIsBuiltInNamed(leftClass, "bool") &&
					ClassTypeIsBuiltInNamed(rightClass, "int") {
					boolType, intType = leftClass, rightClass
				} else if ClassTypeIsBuiltInNamed(rightClass, "bool") &&
					ClassTypeIsBuiltInNamed(leftClass, "int") {
					boolType, intType = rightClass, leftClass
				}

				if boolType != nil && intType != nil {
					if intType.Priv.LiteralValue == nil {
						return true
					}

					// The original compares the literal against 0 and 1 with `!==`,
					// which for a bigint literal is always true and so answers
					// false. LiteralFloat is the `number` arm.
					intVal, isNumber := intType.Priv.LiteralValue.(LiteralFloat)
					if !isNumber || (intVal != 0 && intVal != 1) {
						return false
					}

					boolVal, isBool := boolType.Priv.LiteralValue.(LiteralBool)
					if !isBool {
						return true
					}

					return bool(boolVal) == (intVal == 1)
				}

				return false
			}
		}

		// The original's comment: does the class have an operator overload for eq?
		eqMethod := LookUpClassMember(
			ClassTypeCloneAsInstantiable(leftClass, false), "__eq__",
			MemberAccessFlagsSkipObjectBaseClass, nil)

		if eqMethod != nil {
			// The original's comment: if this is a synthesized method for a
			// dataclass, we can assume that other dataclass types will not be
			// comparable.
			if ClassTypeIsDataClass(leftClass) && eqMethod.Symbol.GetSynthesizedType() != nil {
				return false
			}

			return true
		}

		return false
	}

	return true
}
