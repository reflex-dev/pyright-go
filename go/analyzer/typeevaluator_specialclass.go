/*
 * typeevaluator_specialclass.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * createSpecializedClassType.
 *
 * Applying type arguments to a class. It is three functions wearing one name:
 *
 *   1. A switch over twenty-one special forms -- Callable, Optional, Union,
 *      Literal, Annotated, Concatenate, TypeGuard and the rest -- each of which
 *      has its own creator because none of them specialize like an ordinary
 *      generic class.
 *   2. The Python-3.9 builtins, where `type[T]` and `tuple[T, ...]` have to
 *      behave like typing.Type and typing.Tuple.
 *   3. Ordinary specialization: count the arguments, report too many or too
 *      few, convert each to an instance, fill missing ones from defaults, and
 *      check each against its type parameter's bound and variance.
 *
 * isValidTypeForm threads through all three and is the reason a single boolean
 * appears at the top and is read at the very bottom: the specialized class gets
 * a TypeForm only if nothing along the way was rejected.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// createSpecializedClassType corresponds to the function of the same name.
func (e *typeEvaluator) createSpecializedClassType(
	classType *ClassType,
	typeArgs []*TypeResultWithNode,
	flags EvalFlags,
	errorNode parser.ExpressionNode,
) *TypeResult {
	// typeArgsPresent distinguishes `Foo[]`-with-no-args from `Foo` used bare;
	// the original tests `typeArgs !== undefined` in four places and a nil slice
	// alone cannot carry that.
	typeArgsPresent := typeArgs != nil

	isValidTypeForm := true

	// The original's comment: handle the special-case classes that are not
	// defined in the type stubs.
	if ClassTypeIsSpecialBuiltIn(classType) {
		if result, handled := e.createSpecialFormType(classType, typeArgs, flags, errorNode, &isValidTypeForm); handled {
			return result
		}
	}

	if result := e.createPy39BuiltinType(classType, typeArgs, typeArgsPresent, flags, errorNode); result != nil {
		return result
	}

	typeArgCount := len(typeArgs)

	// The original's comment: make sure the argument list count is correct.
	var typeParams []*TypeVarType
	if !ClassTypeIsPseudoGenericClass(classType) {
		typeParams = ClassTypeGetTypeParams(classType)
	} else {
		typeParams = []*TypeVarType{}
	}

	// The original's comment: if there are no type parameters or args, the class
	// is already specialized. No need to do any more work.
	if len(typeParams) == 0 && typeArgCount == 0 {
		return &TypeResult{Type: classType}
	}

	variadicTypeParamIndex := -1
	for i, param := range typeParams {
		if IsTypeVarTuple(param) {
			variadicTypeParamIndex = i
			break
		}
	}

	if typeArgsPresent {
		if result, handled := e.validateClassTypeArgCount(
			classType, typeArgs, typeParams, flags, errorNode, &typeArgCount, &isValidTypeForm,
		); handled {
			return result
		}

		e.validateClassTypeArgs(typeArgs, typeParams, variadicTypeParamIndex, &isValidTypeForm)
	}

	typeArgTypes, adjustedTypeArgs := e.buildClassTypeArgTypes(classType, typeArgs, typeArgsPresent, errorNode, &isValidTypeForm)
	typeArgs = adjustedTypeArgs

	typeArgTypes = e.checkClassTypeArgsAgainstParams(
		classType, typeArgs, typeParams, typeArgTypes, typeArgCount, flags, &isValidTypeForm,
	)

	// The original's comment: if the class is partially constructed and doesn't
	// yet have type parameters, assume that the number and types of supplied
	// type arguments are correct.
	if typeArgsPresent && len(classType.Shared.TypeParams) == 0 && ClassTypeIsPartiallyEvaluated(classType) {
		typeArgTypes = make([]Type, 0, len(typeArgs))
		for _, t := range typeArgs {
			typeArgTypes = append(typeArgTypes, ConvertToInstance(t.Type, false))
		}
	}

	specialized := ClassTypeSpecialize(classType, typeArgTypes, &typeArgsPresent, false, nil, nil)

	var typeFormType Type
	if isValidTypeForm {
		typeFormType = ConvertToInstance(specialized, false)
	}

	return &TypeResult{Type: CloneWithTypeForm(Type(specialized), typeFormType)}
}

// createSpecialFormType is the original's switch over the special built-in
// names. The second result reports that the switch handled the class.
func (e *typeEvaluator) createSpecialFormType(
	classType *ClassType,
	typeArgs []*TypeResultWithNode,
	flags EvalFlags,
	errorNode parser.ExpressionNode,
	isValidTypeForm *bool,
) (*TypeResult, bool) {
	// `classType.priv.aliasName || classType.shared.name`
	aliasedName := classType.Shared.Name
	if classType.Priv.AliasName != nil && *classType.Priv.AliasName != "" {
		aliasedName = *classType.Priv.AliasName
	}

	nonTypeFormFlags := EvalFlagsNoNonTypeSpecialForms | EvalFlagsTypeExpression | EvalFlagsTypeFormArg

	switch aliasedName {
	case "Callable":
		return &TypeResult{Type: e.createCallableType(classType, typeArgs, errorNode)}, true

	case "Never", "NoReturn":
		if len(typeArgs) > 0 {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.TypeArgsExpectingNone().Format(aliasedName),
				typeArgs[0].Node,
				nil,
			)
		}

		var resultType Type = NeverTypeCreateNever()
		if aliasedName == "NoReturn" {
			resultType = NeverTypeCreateNoReturn()
		}
		resultType = CloneAsSpecialForm(resultType, classType)
		resultType = CloneWithTypeForm(resultType, ConvertToInstance(resultType, false))

		return &TypeResult{Type: resultType}, true

	case "Optional":
		return &TypeResult{Type: e.createOptionalType(classType, errorNode, typeArgs, flags)}, true

	case "Type":
		typeType := e.createSpecialType(classType, typeArgs, intPtr(1), nil, boolPtr(false))
		if IsInstantiableClass(typeType) {
			typeType = e.explodeGenericClass(typeType.(*ClassType))
		}
		typeType = CloneWithTypeForm(typeType, ConvertToInstance(typeType, false))
		return &TypeResult{Type: typeType}, true

	case "ClassVar":
		return &TypeResult{Type: e.createClassVarType(classType, errorNode, typeArgs, flags)}, true

	case "Protocol":
		if (flags & nonTypeFormFlags) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.ProtocolNotAllowed(), errorNode, nil)
		}

		for _, typeArg := range typeArgs {
			if typeArg.TypeListPresent || !IsTypeVar(typeArg.Type) {
				e.AddDiagnostic(
					DiagnosticRuleReportInvalidTypeForm,
					localization.LocMessage.ProtocolTypeArgMustBeTypeParam(),
					typeArg.Node,
					nil,
				)
			}
		}

		return &TypeResult{Type: e.createSpecialType(classType, typeArgs, nil, boolPtr(true), nil)}, true

	case "TypedDict":
		if (flags & nonTypeFormFlags) != 0 {
			isInlinedTypedDict := GetFileInfo(errorNode).DiagnosticRuleSet.EnableExperimentalFeatures &&
				typeArgs != nil

			if !isInlinedTypedDict {
				e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.TypedDictNotAllowed(), errorNode, nil)
			}
		}
		*isValidTypeForm = false
		return nil, false

	case "Literal":
		if (flags & nonTypeFormFlags) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.LiteralNotAllowed(), errorNode, nil)
		}
		*isValidTypeForm = false
		return nil, false

	case "TypeAlias":
		if (flags & EvalFlagsTypeFormArg) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.TypeAnnotationVariable(), errorNode, nil)
		}
		*isValidTypeForm = false
		return nil, false

	case "Tuple":
		return &TypeResult{Type: e.createSpecialType(classType, typeArgs, nil, boolPtr(false), boolPtr(false))}, true

	case "Union":
		return &TypeResult{Type: e.createUnionType(classType, errorNode, typeArgs, flags)}, true

	case "Generic":
		return &TypeResult{Type: e.createGenericType(classType, errorNode, typeArgs, flags)}, true

	case "Final":
		return &TypeResult{Type: e.createFinalType(classType, errorNode, typeArgs, flags)}, true

	case "Annotated":
		return e.createAnnotatedType(classType, errorNode, typeArgs, flags), true

	case "Concatenate":
		return &TypeResult{Type: e.createConcatenateType(classType, errorNode, typeArgs, flags)}, true

	case "TypeGuard", "TypeIs":
		return &TypeResult{Type: e.createTypeGuardType(classType, errorNode, typeArgs, flags)}, true

	case "Unpack":
		return &TypeResult{Type: e.createUnpackType(classType, errorNode, typeArgs, flags)}, true

	case "Required", "NotRequired", "ReadOnly":
		return e.createRequiredOrReadOnlyType(classType, errorNode, typeArgs, flags), true

	case "Self":
		return &TypeResult{Type: e.createSelfType(classType, errorNode, typeArgs, flags)}, true

	case "LiteralString":
		return &TypeResult{Type: e.createSpecialType(classType, typeArgs, intPtr(0), nil, nil)}, true

	case "TypeForm":
		return &TypeResult{Type: e.createTypeFormType(classType, errorNode, typeArgs)}, true
	}

	return nil, false
}

// createPy39BuiltinType is the original's `type` and `tuple` special cases,
// which have to behave like typing.Type and typing.Tuple from Python 3.9 on.
func (e *typeEvaluator) createPy39BuiltinType(
	classType *ClassType,
	typeArgs []*TypeResultWithNode,
	typeArgsPresent bool,
	flags EvalFlags,
	errorNode parser.ExpressionNode,
) *TypeResult {
	fileInfo := GetFileInfo(errorNode)
	if !fileInfo.IsStubFile &&
		!fileInfo.ExecutionEnvironment.PythonVersion.IsGreaterOrEqualTo(common.PythonVersion3_9) &&
		!IsAnnotationEvaluationPostponed(fileInfo) &&
		(flags&EvalFlagsForwardRefs) == 0 {
		return nil
	}

	// The original's comment: handle "type" specially, since it needs to act
	// like "Type" in Python 3.9 and newer.
	if ClassTypeIsBuiltInNamed(classType, "type") && typeArgsPresent {
		if len(typeArgs) >= 1 {
			// The original's comment: treat type[function] as illegal.
			if IsFunctionOrOverloaded(typeArgs[0].Type) {
				e.AddDiagnostic(
					DiagnosticRuleReportInvalidTypeForm,
					localization.LocMessage.TypeAnnotationWithCallable(),
					typeArgs[0].Node,
					nil,
				)

				return &TypeResult{Type: UnknownTypeCreate(false)}
			}
		}

		if e.prefetched != nil && e.prefetched.TypeClass != nil && IsInstantiableClass(e.prefetched.TypeClass) {
			typeType := e.createSpecialType(e.prefetched.TypeClass.(*ClassType), typeArgs, intPtr(1), nil, boolPtr(false))
			if IsInstantiableClass(typeType) {
				typeType = e.explodeGenericClass(typeType.(*ClassType))
			}
			typeType = CloneWithTypeForm(typeType, ConvertToInstance(typeType, false))
			return &TypeResult{Type: typeType}
		}
	}

	// The original's comment: handle "tuple" specially, since it needs to act
	// like "Tuple" in Python 3.9 and newer.
	if IsTupleClass(classType) {
		specializedClass := e.createSpecialType(classType, typeArgs, nil, nil, boolPtr(false))
		specializedClass = CloneWithTypeForm(specializedClass, ConvertToInstance(specializedClass, false))
		return &TypeResult{Type: specializedClass}
	}

	return nil
}

// validateClassTypeArgCount is the original's argument-count validation. The
// second result reports that an inlined TypedDict short-circuited the whole
// function.
func (e *typeEvaluator) validateClassTypeArgCount(
	classType *ClassType,
	typeArgs []*TypeResultWithNode,
	typeParams []*TypeVarType,
	flags EvalFlags,
	errorNode parser.ExpressionNode,
	typeArgCount *int,
	isValidTypeForm *bool,
) (*TypeResult, bool) {
	// `classType.priv.aliasName || classType.shared.name`
	name := classType.Shared.Name
	if classType.Priv.AliasName != nil && *classType.Priv.AliasName != "" {
		name = *classType.Priv.AliasName
	}

	minTypeArgCount := len(typeParams)
	for index, param := range typeParams {
		if param.Shared.IsDefaultExplicit {
			minTypeArgCount = index
			break
		}
	}

	// The original's comment: classes that accept inlined type dict type args
	// allow only one.
	if len(typeArgs) > 0 && typeArgs[0].InlinedTypeDict != nil {
		if len(typeArgs) > 1 {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeArguments,
				localization.LocMessage.TypeArgsTooMany().Format(name, 1, *typeArgCount),
				typeArgs[1].Node,
				nil,
			)
		}

		var inlinedTypeDict Type = typeArgs[0].InlinedTypeDict
		if (flags & EvalFlagsTypeFormArg) != 0 {
			inlinedTypeDict = CloneWithTypeForm(inlinedTypeDict, ConvertToInstance(inlinedTypeDict, false))
		}

		return &TypeResult{Type: inlinedTypeDict}, true
	}

	if *typeArgCount > len(typeParams) {
		if !ClassTypeIsPartiallyEvaluated(classType) && !ClassTypeIsTupleClass(classType) {
			if len(typeParams) == 0 {
				*isValidTypeForm = false
				e.AddDiagnostic(
					DiagnosticRuleReportInvalidTypeArguments,
					localization.LocMessage.TypeArgsExpectingNone().Format(name),
					typeArgs[len(typeParams)].Node,
					nil,
				)
			} else if len(typeParams) != 1 || !IsParamSpec(typeParams[0]) {
				*isValidTypeForm = false
				e.AddDiagnostic(
					DiagnosticRuleReportInvalidTypeArguments,
					localization.LocMessage.TypeArgsTooMany().Format(name, len(typeParams), *typeArgCount),
					typeArgs[len(typeParams)].Node,
					nil,
				)
			}

			*typeArgCount = len(typeParams)
		}
		return nil, false
	}

	if *typeArgCount < minTypeArgCount {
		*isValidTypeForm = false

		// `typeArgs.length > 0 ? typeArgs[0].node.parent! : errorNode`
		var diagNode parser.ParseNode = errorNode
		if len(typeArgs) > 0 {
			diagNode = typeArgs[0].Node.NodeBase().Parent
		}

		e.AddDiagnostic(
			DiagnosticRuleReportInvalidTypeArguments,
			localization.LocMessage.TypeArgsTooFew().Format(name, minTypeArgCount, *typeArgCount),
			diagNode,
			nil,
		)
	}

	return nil, false
}

// validateClassTypeArgs is the original's per-argument validation loop.
func (e *typeEvaluator) validateClassTypeArgs(
	typeArgs []*TypeResultWithNode,
	typeParams []*TypeVarType,
	variadicTypeParamIndex int,
	isValidTypeForm *bool,
) {
	for index, typeArg := range typeArgs {
		if props := typeArg.Type.Base().Props; props == nil || props.TypeForm == nil {
			*isValidTypeForm = false
		}

		if index == variadicTypeParamIndex {
			// The original's comment: the types that make up the tuple that maps
			// to the TypeVarTuple have already been validated when the tuple
			// object was created in adjustTypeArgsForTypeVarTuple.
			if IsClassInstance(typeArg.Type) && IsTupleClass(typeArg.Type.(*ClassType)) {
				continue
			}

			if IsTypeVarTuple(typeArg.Type) {
				if !e.validateTypeVarTupleIsUnpacked(typeArg.Type.(*TypeVarType), typeArg.Node) {
					*isValidTypeForm = false
				}
				continue
			}
		}

		isParamSpecTarget := index < len(typeParams) && IsParamSpec(typeParams[index])

		if !e.ValidateTypeArg(typeArg, &ValidateTypeArgsOptions{
			AllowParamSpec:   true,
			AllowTypeArgList: isParamSpecTarget,
		}) {
			*isValidTypeForm = false
		}
	}
}

// buildClassTypeArgTypes is the original's "handle ParamSpec arguments and fill
// in any missing type arguments with Unknown" block. It returns the possibly
// transformed typeArgs alongside the types, since transformTypeArgsForParamSpec
// can replace the list.
func (e *typeEvaluator) buildClassTypeArgTypes(
	classType *ClassType,
	typeArgs []*TypeResultWithNode,
	typeArgsPresent bool,
	errorNode parser.ExpressionNode,
	isValidTypeForm *bool,
) ([]Type, []*TypeResultWithNode) {
	typeArgTypes := []Type{}
	fullTypeParams := ClassTypeGetTypeParams(classType)

	transformed, transformedPresent := e.transformTypeArgsForParamSpec(fullTypeParams, typeArgs, typeArgsPresent, errorNode)
	if !transformedPresent {
		*isValidTypeForm = false
	}
	typeArgs = transformed
	typeArgsPresent = transformedPresent

	constraints := NewConstraintTracker()

	for index, typeParam := range fullTypeParams {
		if typeArgsPresent && index < len(typeArgs) {
			if IsParamSpec(typeParam) {
				if handled := e.buildParamSpecTypeArg(typeArgs[index], typeParam, constraints, &typeArgTypes); handled {
					continue
				}
			}

			typeArgType := ConvertToInstance(typeArgs[index].Type, false)
			typeArgTypes = append(typeArgTypes, typeArgType)
			constraints.SetBounds(typeParam, typeArgType, nil, false)
			continue
		}

		solvedDefaultType := e.SolveAndApplyConstraints(typeParam, constraints, &ApplyTypeVarOptions{
			ReplaceUnsolved: &ReplaceUnsolvedOptions{
				ScopeIDs:       GetTypeVarScopeIds(classType),
				TupleClassType: e.GetTupleClassType(),
			},
		}, nil)
		typeArgTypes = append(typeArgTypes, solvedDefaultType)
		constraints.SetBounds(typeParam, solvedDefaultType, nil, false)
	}

	return typeArgTypes, typeArgs
}

// buildParamSpecTypeArg is the original's ParamSpec arm, which turns an
// ellipsis, a type list, or a Concatenate into a synthesized function type. It
// reports whether it produced a type argument.
func (e *typeEvaluator) buildParamSpecTypeArg(
	typeArg *TypeResultWithNode,
	typeParam *TypeVarType,
	constraints *ConstraintTracker,
	typeArgTypes *[]Type,
) bool {
	functionType := FunctionTypeCreateSynthesizedInstance("", FunctionTypeFlagsParamSpecValue)

	if IsEllipsisType(typeArg.Type) {
		FunctionTypeAddDefaultParams(functionType, false)
		functionType.Shared.Flags |= FunctionTypeFlagsGradualCallableForm
		*typeArgTypes = append(*typeArgTypes, functionType)
		constraints.SetBounds(typeParam, functionType, nil, false)
		return true
	}

	if typeArg.TypeListPresent {
		for paramIndex, paramType := range typeArg.TypeList {
			name := "__p" + itoa(paramIndex)
			FunctionTypeAddParam(functionType, FunctionParamCreate(
				parser.ParamCategorySimple,
				ConvertToInstance(paramType.Type, false),
				FunctionParamFlagsNameSynthesized|FunctionParamFlagsTypeDeclared,
				&name,
				nil,
				nil,
			))
		}

		if len(typeArg.TypeList) > 0 {
			FunctionTypeAddPositionOnlyParamSeparator(functionType)
		}

		*typeArgTypes = append(*typeArgTypes, functionType)
		constraints.SetBounds(typeParam, functionType, nil, false)
		return true
	}

	if IsInstantiableClass(typeArg.Type) && ClassTypeIsBuiltInNamed(typeArg.Type.(*ClassType), "Concatenate") {
		concatTypeArgs := typeArg.Type.(*ClassType).Priv.TypeArgs
		for index, concatArg := range concatTypeArgs {
			if index == len(concatTypeArgs)-1 {
				if IsParamSpec(concatArg) {
					FunctionTypeAddParamSpecVariadics(functionType, concatArg.(*TypeVarType))
				} else if IsEllipsisType(concatArg) {
					FunctionTypeAddDefaultParams(functionType, false)
					functionType.Shared.Flags |= FunctionTypeFlagsGradualCallableForm
				}
				continue
			}

			name := "__p" + itoa(index)
			FunctionTypeAddParam(functionType, FunctionParamCreate(
				parser.ParamCategorySimple,
				concatArg,
				FunctionParamFlagsNameSynthesized|FunctionParamFlagsTypeDeclared,
				&name,
				nil,
				nil,
			))
		}

		// Note: unlike the two arms above, the original does NOT record bounds
		// for the Concatenate case.
		*typeArgTypes = append(*typeArgTypes, functionType)
		return true
	}

	return false
}

// checkClassTypeArgsAgainstParams is the original's final map over typeArgTypes,
// which checks each supplied argument against its type parameter's bound and,
// when required, its variance.
func (e *typeEvaluator) checkClassTypeArgsAgainstParams(
	classType *ClassType,
	typeArgs []*TypeResultWithNode,
	typeParams []*TypeVarType,
	typeArgTypes []Type,
	typeArgCount int,
	flags EvalFlags,
	isValidTypeForm *bool,
) []Type {
	for index := range typeArgTypes {
		if index >= typeArgCount || index >= len(typeParams) {
			continue
		}

		diag := common.NewDiagnosticAddendum()
		adjustedTypeArgType := e.applyTypeArgToTypeVar(typeParams[index], typeArgTypes[index], diag)

		// The original's comment: determine if the variance must match.
		if adjustedTypeArgType != nil && (flags&EvalFlagsEnforceVarianceConsistency) != 0 {
			declaredVariance := typeParams[index].Shared.DeclaredVariance

			if !e.isVarianceOfTypeArgCompatible(adjustedTypeArgType, declaredVariance) {
				diag.AddMessage(localization.LocAddendum.VarianceMismatchForClass().Format(
					e.PrintType(adjustedTypeArgType, nil),
					classType.Shared.Name,
				))
				adjustedTypeArgType = nil
			}
		}

		if adjustedTypeArgType != nil {
			typeArgTypes[index] = adjustedTypeArgType
			continue
		}

		// The original's comment: avoid emitting this error for a
		// partially-constructed class.
		if IsClassInstance(typeArgTypes[index]) && ClassTypeIsPartiallyEvaluated(typeArgTypes[index].(*ClassType)) {
			continue
		}

		// The original asserts typeArgs is defined here; index < typeArgCount
		// guarantees it.
		if index >= len(typeArgs) {
			continue
		}

		*isValidTypeForm = false
		e.AddDiagnostic(
			DiagnosticRuleReportInvalidTypeArguments,
			localization.LocMessage.TypeVarAssignmentMismatch().Format(
				e.PrintType(typeArgTypes[index], nil),
				TypeVarTypeGetReadableName(typeParams[index], false),
			)+diag.GetString(),
			typeArgs[index].Node,
			nil,
		)
	}

	return typeArgTypes
}

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

/*
 * The special-form creators and the three helpers. Each is a separate unit of
 * work and records itself.
 */

func (e *typeEvaluator) createCallableType(c *ClassType, _ []*TypeResultWithNode, _ parser.ExpressionNode) Type {
	e.unported("createCallableType")
	return c
}

func (e *typeEvaluator) createOptionalType(c *ClassType, _ parser.ExpressionNode, _ []*TypeResultWithNode, _ EvalFlags) Type {
	e.unported("createOptionalType")
	return c
}

// createSpecialType corresponds to the function of the same name. paramLimit,
// allowParamSpec and isSpecialForm are pointers because each of the original's
// defaults is not the Go zero value at every call site.
func (e *typeEvaluator) createSpecialType(
	c *ClassType,
	_ []*TypeResultWithNode,
	_ *int,
	_ *bool,
	_ *bool,
) Type {
	e.unported("createSpecialType")
	return c
}

func (e *typeEvaluator) createClassVarType(c *ClassType, _ parser.ExpressionNode, _ []*TypeResultWithNode, _ EvalFlags) Type {
	e.unported("createClassVarType")
	return c
}

func (e *typeEvaluator) createUnionType(c *ClassType, _ parser.ExpressionNode, _ []*TypeResultWithNode, _ EvalFlags) Type {
	e.unported("createUnionType")
	return c
}

func (e *typeEvaluator) createGenericType(c *ClassType, _ parser.ExpressionNode, _ []*TypeResultWithNode, _ EvalFlags) Type {
	e.unported("createGenericType")
	return c
}

func (e *typeEvaluator) createFinalType(c *ClassType, _ parser.ExpressionNode, _ []*TypeResultWithNode, _ EvalFlags) Type {
	e.unported("createFinalType")
	return c
}

func (e *typeEvaluator) createAnnotatedType(c *ClassType, _ parser.ExpressionNode, _ []*TypeResultWithNode, _ EvalFlags) *TypeResult {
	e.unported("createAnnotatedType")
	return &TypeResult{Type: c}
}

func (e *typeEvaluator) createConcatenateType(c *ClassType, _ parser.ExpressionNode, _ []*TypeResultWithNode, _ EvalFlags) Type {
	e.unported("createConcatenateType")
	return c
}

func (e *typeEvaluator) createTypeGuardType(c *ClassType, _ parser.ExpressionNode, _ []*TypeResultWithNode, _ EvalFlags) Type {
	e.unported("createTypeGuardType")
	return c
}

func (e *typeEvaluator) createUnpackType(c *ClassType, _ parser.ExpressionNode, _ []*TypeResultWithNode, _ EvalFlags) Type {
	e.unported("createUnpackType")
	return c
}

func (e *typeEvaluator) createRequiredOrReadOnlyType(c *ClassType, _ parser.ExpressionNode, _ []*TypeResultWithNode, _ EvalFlags) *TypeResult {
	e.unported("createRequiredOrReadOnlyType")
	return &TypeResult{Type: c}
}

// createSelfType corresponds to the function of the same name.
func (e *typeEvaluator) createSelfType(c *ClassType, _ parser.ExpressionNode, _ []*TypeResultWithNode, _ EvalFlags) Type {
	e.unported("createSelfType")
	return c
}

// createTypeFormType corresponds to the function of the same name.
func (e *typeEvaluator) createTypeFormType(c *ClassType, _ parser.ExpressionNode, _ []*TypeResultWithNode) Type {
	e.unported("createTypeFormType")
	return c
}

// explodeGenericClass corresponds to the function of the same name.
func (e *typeEvaluator) explodeGenericClass(c *ClassType) Type {
	e.unported("explodeGenericClass")
	return c
}

// transformTypeArgsForParamSpec corresponds to the function of the same name.
// The original's comment: PEP 612 says that if the class has only one type
// parameter consisting of a ParamSpec, the list of arguments does not need to be
// enclosed in a list. The second result is the original's `undefined` return,
// which means the arguments were invalid.
func (e *typeEvaluator) transformTypeArgsForParamSpec(
	_ []*TypeVarType,
	typeArgs []*TypeResultWithNode,
	typeArgsPresent bool,
	_ parser.ExpressionNode,
) ([]*TypeResultWithNode, bool) {
	e.unported("transformTypeArgsForParamSpec")
	return typeArgs, typeArgsPresent
}

// applyTypeArgToTypeVar corresponds to the function of the same name: it checks
// a supplied type argument against its type parameter's bound or constraints. It
// returns nil where the original returns undefined, meaning "not assignable".
func (e *typeEvaluator) applyTypeArgToTypeVar(
	_ *TypeVarType,
	typeArgType Type,
	_ *common.DiagnosticAddendum,
) Type {
	e.unported("applyTypeArgToTypeVar")
	return typeArgType
}

// isVarianceOfTypeArgCompatible corresponds to the function of the same name.
func (e *typeEvaluator) isVarianceOfTypeArgCompatible(_ Type, _ Variance) bool {
	e.unported("isVarianceOfTypeArgCompatible")
	return true
}
