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
			typeArgTypes = append(typeArgTypes, ConvertToInstance(t.Type, true))
		}
	}

	specialized := ClassTypeSpecialize(classType, typeArgTypes, &typeArgsPresent, false, nil, nil)

	var typeFormType Type
	if isValidTypeForm {
		typeFormType = ConvertToInstance(specialized, true)
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
		resultType = CloneWithTypeForm(resultType, ConvertToInstance(resultType, true))

		return &TypeResult{Type: resultType}, true

	case "Optional":
		return &TypeResult{Type: e.createOptionalType(classType, errorNode, typeArgs, flags)}, true

	case "Type":
		typeType := e.createSpecialType(classType, typeArgs, intPtr(1), nil, boolPtr(false))
		if IsInstantiableClass(typeType) {
			typeType = ExplodeGenericClass(typeType.(*ClassType))
		}
		typeType = CloneWithTypeForm(typeType, ConvertToInstance(typeType, true))
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
				typeType = ExplodeGenericClass(typeType.(*ClassType))
			}
			typeType = CloneWithTypeForm(typeType, ConvertToInstance(typeType, true))
			return &TypeResult{Type: typeType}
		}
	}

	// The original's comment: handle "tuple" specially, since it needs to act
	// like "Tuple" in Python 3.9 and newer.
	if IsTupleClass(classType) {
		specializedClass := e.createSpecialType(classType, typeArgs, nil, nil, boolPtr(false))
		specializedClass = CloneWithTypeForm(specializedClass, ConvertToInstance(specializedClass, true))
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
			inlinedTypeDict = CloneWithTypeForm(inlinedTypeDict, ConvertToInstance(inlinedTypeDict, true))
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

			typeArgType := ConvertToInstance(typeArgs[index].Type, true)
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
				ConvertToInstance(paramType.Type, true),
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

			if !IsVarianceOfTypeArgCompatible(adjustedTypeArgType, declaredVariance) {
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
 * The three special-form creators whose arguments are not ordinary type
 * arguments: Generic declares type parameters, Annotated carries uninterpreted
 * metadata, and Self names the enclosing class. The rest of the family lives in
 * typeevaluator_specialforms.go.
 */

// createGenericType corresponds to the function of the same name: `Generic[T]`
// in a base class list, whose arguments declare the class's type parameters
// rather than supplying values for them.
//
// The type arguments are validated but not consumed here -- every one must be a
// TypeVar, and no TypeVar twice -- and the list is then handed to
// createSpecialType, which is what actually records them as the class's
// parameters.
func (e *typeEvaluator) createGenericType(
	classType *ClassType,
	errorNode parser.ParseNode,
	typeArgs []*TypeResultWithNode,
	flags EvalFlags,
) Type {
	if typeArgs == nil {
		// The original's comment: if no type arguments are provided, the resulting
		// type depends on whether we're evaluating a type annotation or we're in
		// some other context.
		if (flags & (EvalFlagsTypeExpression | EvalFlagsNoNakedGeneric | EvalFlagsTypeFormArg)) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.GenericTypeArgMissing(), errorNode, nil)
		}

		if (flags & EvalFlagsTypeFormArg) != 0 {
			return UnknownTypeCreate(false)
		}
		return classType
	}

	if (flags & EvalFlagsTypeFormArg) != 0 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.GenericNotAllowed(), errorNode, nil)
		return UnknownTypeCreate(false)
	}

	// The original's comment: make sure there's at least one type arg.
	if len(typeArgs) == 0 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.GenericTypeArgMissing(), errorNode, nil)
	}

	// The original's comment: make sure that all of the type args are typeVars and
	// are unique.
	uniqueTypeVars := []Type{}
	for _, typeArg := range typeArgs {
		if !IsTypeVar(typeArg.Type) {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.GenericTypeArgTypeVar(), typeArg.Node, nil)
			continue
		}

		if containsSameType(uniqueTypeVars, typeArg.Type, TypeSameOptions{}) {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.GenericTypeArgUnique(), typeArg.Node, nil)
		}

		uniqueTypeVars = append(uniqueTypeVars, typeArg.Type)
	}

	return e.createSpecialType(classType, typeArgs, nil, boolPtr(true), nil)
}

// createAnnotatedType corresponds to the function of the same name: `Annotated[T, ...]`,
// whose first argument is the real type and whose remaining arguments are metadata
// the type system carries but does not interpret.
func (e *typeEvaluator) createAnnotatedType(
	classType *ClassType,
	errorNode parser.ExpressionNode,
	typeArgs []*TypeResultWithNode,
	flags EvalFlags,
) *TypeResult {
	var t Type

	// Outside a type expression, `Annotated` is an ordinary runtime object.
	typeExprFlags := EvalFlagsTypeExpression | EvalFlagsNoConvertSpecialForm
	if (flags & typeExprFlags) == 0 {
		t = ClassTypeCloneAsInstance(classType, false)

		if len(typeArgs) >= 1 {
			if props := typeArgs[0].Type.Base().Props; props != nil && props.TypeForm != nil {
				t = CloneWithTypeForm(t, props.TypeForm)
			}
		}

		return &TypeResult{Type: t}
	}

	if len(typeArgs) > 0 {
		t = typeArgs[0].Type

		if len(typeArgs) < 2 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.AnnotatedTypeArgMissing(), errorNode, nil)
		} else {
			t = e.validateAnnotatedMetadata(errorNode, typeArgs[0].Type, typeArgs[1:])
		}
	}

	if t == nil || len(typeArgs) == 0 {
		return &TypeResult{Type: AnyTypeCreate(false)}
	}

	if typeArgs[0].TypeList != nil {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.TypeArgListNotAllowed(), typeArgs[0].Node, nil)
	}

	return &TypeResult{
		Type:          CloneAsSpecialForm(t, ClassTypeCloneAsInstance(classType, false)),
		IsReadOnly:    typeArgs[0].IsReadOnly,
		IsRequired:    typeArgs[0].IsRequired,
		IsNotRequired: typeArgs[0].IsNotRequired,
	}
}

// validateAnnotatedMetadata corresponds to the function of the same name.
//
// Its comment: enforces metadata consistency as specified in PEP 746. The
// original's per-argument check was added for a draft of that PEP and its
// functionality has since been removed while the PEP is revised, so every
// argument is accepted.
func (e *typeEvaluator) validateAnnotatedMetadata(
	_ parser.ExpressionNode, baseType Type, _ []*TypeResultWithNode,
) Type {
	return baseType
}

// createSelfType corresponds to the function of the same name: the `Self` type,
// which stands for the class it appears in.
//
// Most of the function is about where `Self` is NOT allowed. It needs an
// enclosing class, and one reached through the class BODY rather than through a
// decorator, base-class list, metaclass argument or type parameter list -- all
// of which are lexically inside the class statement but evaluated outside it. A
// metaclass cannot use it at all, and neither can a static method, which has no
// self to refer to. An enclosing method that annotates its own first parameter
// with something other than Self contradicts it.
func (e *typeEvaluator) createSelfType(
	classType *ClassType,
	errorNode parser.ExpressionNode,
	typeArgs []*TypeResultWithNode,
	flags EvalFlags,
) Type {
	// The original's comment: Self doesn't support any type arguments.
	if len(typeArgs) > 0 {
		var reportNode parser.ParseNode = errorNode
		if typeArgs[0].Node != nil {
			reportNode = typeArgs[0].Node
		}
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeArguments,
			localization.LocMessage.TypeArgsExpectingNone().Format(classType.Shared.Name),
			reportNode, nil)
	}

	enclosingClass := GetEnclosingClass(errorNode, false)

	// The original's comment: if `Self` appears anywhere outside of the class body
	// (e.g. a decorator, base class list, metaclass argument, type parameter list),
	// it is considered illegal.
	if enclosingClass != nil && !IsNodeContainedWithin(errorNode, enclosingClass.D.Suite) {
		enclosingClass = nil
	}

	var enclosingClassTypeResult *ClassTypeResult
	if enclosingClass != nil {
		enclosingClassTypeResult = e.GetTypeOfClass(enclosingClass)
	}

	if enclosingClassTypeResult == nil {
		if (flags & (EvalFlagsTypeExpression | EvalFlagsInstantiableType | EvalFlagsTypeFormArg)) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.SelfTypeContext(), errorNode, nil)
		}

		return UnknownTypeCreate(false)
	}

	if IsInstantiableMetaclass(enclosingClassTypeResult.ClassType) {
		// The original's comment: if `Self` appears within a metaclass, it is
		// considered illegal.
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.SelfTypeMetaclass(), errorNode, nil)

		return UnknownTypeCreate(false)
	}

	if enclosingFunction := GetEnclosingFunction(errorNode); enclosingFunction != nil {
		if !e.checkSelfTypeInFunction(enclosingFunction, errorNode) {
			return UnknownTypeCreate(false)
		}
	}

	result := SynthesizeTypeVarForSelfCls(enclosingClassTypeResult.ClassType, true)

	if enclosingClass != nil {
		// The original's comment: if "Self" is used as a type expression within a
		// function suite, it needs to be marked as bound.
		enclosingSuite := GetEnclosingClassOrFunctionSuite(errorNode)

		if enclosingSuite != nil && IsNodeContainedWithin(enclosingSuite, enclosingClass) {
			if enclosingClass.D.Suite != enclosingSuite {
				result = TypeVarTypeCloneAsBound(result)
			}
		}
	}

	return result
}

// checkSelfTypeInFunction is the original's enclosing-function block. It reports
// whether `Self` is legal here.
func (e *typeEvaluator) checkSelfTypeInFunction(
	enclosingFunction *parser.FunctionNode, errorNode parser.ExpressionNode,
) bool {
	functionInfo := GetFunctionInfoFromDecorators(e, enclosingFunction, true)

	isInnerFunction := GetEnclosingFunction(enclosingFunction) != nil
	if isInnerFunction {
		return true
	}

	// The original's comment: check for static methods.
	if (functionInfo.Flags & FunctionTypeFlagsStaticMethod) != 0 {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.SelfTypeContext(), errorNode, nil)
		return false
	}

	if len(enclosingFunction.D.Params) == 0 {
		return true
	}

	firstParamTypeAnnotation := GetTypeAnnotationForParam(enclosingFunction, 0)
	if firstParamTypeAnnotation == nil || IsNodeContainedWithin(errorNode, firstParamTypeAnnotation) {
		return true
	}

	annotationType := e.GetTypeOfAnnotation(firstParamTypeAnnotation,
		&ExpectedTypeOptions{TypeVarGetsCurScope: true})
	tv, isTypeVar := annotationType.(*TypeVarType)
	if !isTypeVar || !IsTypeVar(annotationType) || !TypeVarTypeIsSelf(tv) {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.SelfTypeWithTypedSelfOrCls(), errorNode, nil)
	}

	return true
}

// explodeGenericClass corresponds to the function of the same name.
