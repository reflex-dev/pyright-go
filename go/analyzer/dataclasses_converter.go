/*
 * dataclasses_converter.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/dataClasses.ts (pyright 1.1.412):
 * getDefaultArgValueForFieldSpecifier, getConverterInputType,
 * getConverterAsFunction, getDescriptorForConverterField,
 * transformDescriptorType and isDataclassFieldConstructor.
 *
 * These are the pieces that make `field(...)` and attrs-style converters work.
 *
 * getDefaultArgValueForFieldSpecifier answers a question that only arises for
 * dataclass_transform: a field specifier may declare `init` or `kw_only` with a
 * *literal bool type* or a literal default, and that value is the field's
 * behavior when the call site does not pass the argument. So the answer comes
 * from the parameter declaration, not from the call -- which is why it reaches
 * for the best-matching overload rather than the call's evaluated type.
 *
 * The converter machinery is the interesting part. A field with a converter has
 * two different types: the annotated type, which is what reading the attribute
 * gives you, and the converter's input type, which is what `__init__` accepts.
 * Python has no way to spell "this attribute is asymmetric", so pyright
 * synthesizes a descriptor class per field whose __get__ returns the declared
 * type and whose __set__ takes the converter input, and puts an instance of it
 * in the symbol table under the field name. That is what
 * getDescriptorForConverterField builds, and the descriptor is made generic over
 * a copy of the dataclass's own type parameters so a generic field keeps working.
 *
 * getConverterInputType finds that input type by construction rather than by
 * inspection: it builds `Callable[[__converterInput], fieldType]` and asks
 * whether the converter is assignable to it, then reads what the solver bound
 * `__converterInput` to. Doing it that way is what makes an overloaded converter
 * work -- each signature is tried separately and the accepted inputs are unioned.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// dataClassBoolResult is the original's `boolean | undefined` return.
type dataClassBoolResult struct {
	Value  bool
	Exists bool
}

// getDefaultArgValueForFieldSpecifier corresponds to the function of the same
// name.
func getDefaultArgValueForFieldSpecifier(
	evaluator TypeEvaluator,
	callNode *parser.CallNode,
	callTypeResult *TypeResult,
	paramName string,
) dataClassBoolResult {
	callType := callTypeResult.Type
	var callTarget *FunctionType

	switch {
	case IsFunction(callType):
		callTarget = callType.(*FunctionType)

	case IsOverloaded(callType):
		callTarget = evaluator.GetBestOverloadForArgs(
			callNode,
			&TypeResult{Type: callType, IsIncomplete: callTypeResult.IsIncomplete},
			convertCallArgs(evaluator, callNode),
		)

	case IsInstantiableClass(callType):
		initMethodResult := GetBoundInitMethod(evaluator, callNode, callType.(*ClassType), nil, MemberAccessFlagsSkipObjectBaseClass)
		if initMethodResult != nil {
			switch {
			case IsFunction(initMethodResult.Type):
				callTarget = initMethodResult.Type.(*FunctionType)
			case IsOverloaded(initMethodResult.Type):
				callTarget = evaluator.GetBestOverloadForArgs(
					callNode,
					&TypeResult{Type: initMethodResult.Type},
					convertCallArgs(evaluator, callNode),
				)
			}
		}
	}

	if callTarget == nil {
		return dataClassBoolResult{}
	}

	initParamIndex := -1
	for i, p := range callTarget.Shared.Parameters {
		if p.Name != nil && *p.Name == paramName {
			initParamIndex = i
			break
		}
	}
	if initParamIndex < 0 {
		return dataClassBoolResult{}
	}

	initParam := callTarget.Shared.Parameters[initParamIndex]

	// The original's comment: is the parameter type a literal bool?
	initParamType := FunctionTypeGetParamType(callTarget, initParamIndex)
	if FunctionParamIsTypeDeclared(initParam) && IsClass(initParamType) {
		if b, ok := initParamType.(*ClassType).Priv.LiteralValue.(LiteralBool); ok {
			return dataClassBoolResult{Value: bool(b), Exists: true}
		}
	}

	// The original's comment: is the default argument value a literal bool?
	initParamDefaultType := FunctionTypeGetParamDefaultType(callTarget, initParamIndex)
	if initParamDefaultType != nil && IsClass(initParamDefaultType) {
		if b, ok := initParamDefaultType.(*ClassType).Priv.LiteralValue.(LiteralBool); ok {
			return dataClassBoolResult{Value: bool(b), Exists: true}
		}
	}

	return dataClassBoolResult{}
}

// convertCallArgs is the original's inline
// `callNode.d.args.map((arg) => evaluator.convertNodeToArg(arg))`.
func convertCallArgs(evaluator TypeEvaluator, callNode *parser.CallNode) []*Arg {
	args := make([]*Arg, 0, len(callNode.D.Args))
	for _, arg := range callNode.D.Args {
		args = append(args, evaluator.ConvertNodeToArg(arg))
	}
	return args
}

// getConverterInputType corresponds to the function of the same name: it
// validates the converter and returns its input type, falling back to fieldType
// when the converter is unusable.
func getConverterInputType(
	evaluator TypeEvaluator,
	converterNode *parser.ArgumentNode,
	fieldType Type,
	fieldName string,
) Type {
	// The original's comment: use speculative mode here so we don't cache the
	// results. We'll want to re-evaluate this expression later, potentially with
	// different evaluation flags.
	var valueType Type
	evaluator.UseSpeculativeMode(converterNode.D.ValueExpr, func() {
		valueType = evaluator.GetTypeOfExpression(converterNode.D.ValueExpr, EvalFlagsNoSpecialize, nil).Type
	}, nil)

	converterType := getConverterAsFunction(evaluator, valueType)
	if converterType == nil {
		return fieldType
	}

	// The original's comment: create synthesized function of the form
	// Callable[[T], fieldType] which will be used to check compatibility of the
	// provided converter.
	typeVar := TypeVarTypeCreateInstance("__converterInput", TypeVarKindTypeVar)
	scopeID := TypeVarScopeId(GetScopeIdForNode(converterNode))
	typeVar.Priv.ScopeID = scopeID
	targetFunction := FunctionTypeCreateSynthesizedInstance("", FunctionTypeFlagsNone)
	targetFunction.Shared.TypeVarScopeID = scopeID
	targetFunction.Shared.DeclaredReturnType = fieldType
	inputName := "__input"
	FunctionTypeAddParam(targetFunction, FunctionParamCreate(
		parser.ParamCategorySimple,
		typeVar,
		FunctionParamFlagsTypeDeclared|FunctionParamFlagsNameSynthesized,
		&inputName,
		nil,
		nil,
	))
	FunctionTypeAddPositionOnlyParamSeparator(targetFunction)

	if !IsFunctionOrOverloaded(converterType) {
		return fieldType
	}

	acceptedTypes := []Type{}
	diagAddendum := common.NewDiagnosticAddendum()

	DoForEachSignature(converterType, func(signature *FunctionType, _ int) {
		returnConstraints := NewConstraintTracker()

		effectiveReturn := FunctionTypeGetEffectiveReturnType(signature, false)
		if effectiveReturn == nil {
			effectiveReturn = UnknownTypeCreate(false)
		}

		if evaluator.AssignType(effectiveReturn, fieldType, nil, returnConstraints, AssignTypeFlagsDefault, 0) {
			// The solver may pin type variables in the converter's own scope
			// from the field type; applying them makes the signature concrete
			// before the input direction is checked.
			if solved, ok := evaluator.SolveAndApplyConstraints(
				signature, returnConstraints, nil, nil).(*FunctionType); ok {
				signature = solved
			}
		}

		inputConstraints := NewConstraintTracker()

		if evaluator.AssignType(targetFunction, signature, diagAddendum, inputConstraints,
			AssignTypeFlagsDefault, 0) {
			overloadSolution := evaluator.SolveAndApplyConstraints(typeVar, inputConstraints,
				&ApplyTypeVarOptions{
					ReplaceUnsolved: &ReplaceUnsolvedOptions{
						ScopeIDs:       GetTypeVarScopeIDs(typeVar),
						TupleClassType: evaluator.GetTupleClassType(),
					},
				}, nil)
			acceptedTypes = append(acceptedTypes, overloadSolution)
		}
	})

	if len(acceptedTypes) > 0 {
		return CombineTypes(acceptedTypes, nil)
	}

	if IsFunction(converterType) {
		var textRange *common.TextRange
		if r := diagAddendum.GetEffectiveTextRange(); r != nil {
			textRange = r
		} else {
			r := converterNode.GetRange()
			textRange = &r
		}

		evaluator.AddDiagnostic(
			DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.DataClassConverterFunction().Format(
				evaluator.PrintType(converterType, nil),
				evaluator.PrintType(fieldType, nil),
				fieldName,
			)+diagAddendum.GetString(),
			converterNode,
			textRange,
		)
	} else {
		overloads := OverloadedTypeGetOverloads(converterType.(*OverloadedType))
		funcName := "<anonymous function>"
		if len(overloads) > 0 && overloads[0].Shared.Name != "" {
			funcName = overloads[0].Shared.Name
		}

		evaluator.AddDiagnostic(
			DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.DataClassConverterOverloads().Format(
				funcName,
				evaluator.PrintType(fieldType, nil),
				fieldName,
			)+diagAddendum.GetString(),
			converterNode,
			nil,
		)
	}

	return fieldType
}

// createFunctionFromConstructorForConverter reaches constructors.ts
// createFunctionFromConstructor, which handles the case of a converter written
// as a class rather than a function.
func createFunctionFromConstructorForConverter(evaluator TypeEvaluator, classType *ClassType) Type {
	return CreateFunctionFromConstructor(evaluator, classType, nil, 0)
}

// getConverterAsFunction corresponds to the function of the same name.
func getConverterAsFunction(evaluator TypeEvaluator, converterType Type) Type {
	if IsFunctionOrOverloaded(converterType) {
		return converterType
	}

	if IsClassInstance(converterType) {
		bound := evaluator.GetBoundMagicMethod(converterType.(*ClassType), "__call__", nil, nil, nil, 0)
		if bound == nil {
			return nil
		}
		return bound
	}

	if IsInstantiableClass(converterType) {
		fromConstructor := createFunctionFromConstructorForConverter(evaluator, converterType.(*ClassType))
		if fromConstructor != nil {
			// The original's comment: if conversion to a constructor resulted in
			// a union type, we'll choose the first of the two subtypes, which
			// typically corresponds to the __init__ method (rather than the
			// __new__ method).
			if IsUnion(fromConstructor) {
				fromConstructor = fromConstructor.(*UnionType).Priv.Subtypes[0]
			}

			if IsFunctionOrOverloaded(fromConstructor) {
				return fromConstructor
			}
		}
	}

	return nil
}

// getDescriptorForConverterField corresponds to the function of the same name.
// It synthesizes the asymmetric descriptor described in the file header and
// returns a symbol holding an instance of it.
func getDescriptorForConverterField(
	evaluator TypeEvaluator,
	dataclass *ClassType,
	dataclassNode parser.ParseNode,
	fieldNameNode *parser.NameNode,
	converterNode parser.ParseNode,
	fieldName string,
	getType Type,
	setType Type,
) *Symbol {
	fileInfo := GetFileInfo(dataclassNode)
	typeMetaclass := evaluator.GetBuiltInType(dataclassNode, "type")
	descriptorName := "__converterDescriptor_" + fieldName

	var effectiveMetaclass Type = UnknownTypeCreate(false)
	if IsInstantiableClass(typeMetaclass) {
		effectiveMetaclass = typeMetaclass
	}

	descriptorClass := ClassTypeCreateInstantiable(
		descriptorName,
		GetClassFullName(converterNode, fileInfo.ModuleName, descriptorName),
		fileInfo.ModuleName,
		fileInfo.FileUri,
		ClassTypeFlagsNone,
		GetTypeSourceID(converterNode),
		nil,
		effectiveMetaclass,
		nil,
	)

	scopeID := TypeVarScopeId(GetScopeIdForNode(converterNode))
	descriptorClass.Shared.TypeVarScopeID = scopeID

	// The original's comment: make the descriptor generic, copying the type
	// parameters from the dataclass.
	typeParams := make([]*TypeVarType, 0, len(dataclass.Shared.TypeParams))
	classScopeType := TypeVarScopeTypeClass
	for _, typeParm := range dataclass.Shared.TypeParams {
		typeParam := TypeVarTypeCloneForScopeID(
			typeParm, string(scopeID), &descriptorClass.Shared.Name, &classScopeType)
		covariant := VarianceCovariant
		typeParam.Priv.ComputedVariance = &covariant
		typeParams = append(typeParams, typeParam)
	}
	descriptorClass.Shared.TypeParams = typeParams

	typeParamsAsTypes := make([]Type, 0, len(descriptorClass.Shared.TypeParams))
	for _, tp := range descriptorClass.Shared.TypeParams {
		typeParamsAsTypes = append(typeParamsAsTypes, tp)
	}
	solution := BuildSolution(dataclass.Shared.TypeParams, typeParamsAsTypes)
	getType = ApplySolvedTypeVars(getType, solution, nil)
	setType = ApplySolvedTypeVars(setType, solution, nil)

	descriptorClass.Shared.BaseClasses = append(descriptorClass.Shared.BaseClasses,
		evaluator.GetBuiltInType(dataclassNode, "object"))
	ComputeMroLinearization(descriptorClass)

	fields := ClassTypeGetSymbolTable(descriptorClass)
	selfType := SynthesizeTypeVarForSelfCls(descriptorClass, false)

	selfName, objName, objtypeName, valueName := "self", "obj", "objtype", "value"

	setFunction := FunctionTypeCreateSynthesizedInstance("__set__", FunctionTypeFlagsNone)
	FunctionTypeAddParam(setFunction, FunctionParamCreate(
		parser.ParamCategorySimple, selfType, FunctionParamFlagsTypeDeclared, &selfName, nil, nil))
	FunctionTypeAddParam(setFunction, FunctionParamCreate(
		parser.ParamCategorySimple, AnyTypeCreate(false), FunctionParamFlagsTypeDeclared, &objName, nil, nil))
	FunctionTypeAddParam(setFunction, FunctionParamCreate(
		parser.ParamCategorySimple, setType, FunctionParamFlagsTypeDeclared, &valueName, nil, nil))
	setFunction.Shared.DeclaredReturnType = evaluator.GetNoneType()
	fields.Set("__set__", SymbolCreateWithType(SymbolFlagsClassMember, setFunction, nil))

	getFunction := FunctionTypeCreateSynthesizedInstance("__get__", FunctionTypeFlagsNone)
	FunctionTypeAddParam(getFunction, FunctionParamCreate(
		parser.ParamCategorySimple, selfType, FunctionParamFlagsTypeDeclared, &selfName, nil, nil))
	FunctionTypeAddParam(getFunction, FunctionParamCreate(
		parser.ParamCategorySimple, AnyTypeCreate(false), FunctionParamFlagsTypeDeclared, &objName, nil, nil))
	FunctionTypeAddParam(getFunction, FunctionParamCreate(
		parser.ParamCategorySimple, AnyTypeCreate(false), FunctionParamFlagsTypeDeclared, &objtypeName, nil, nil))
	getFunction.Shared.DeclaredReturnType = getType
	fields.Set("__get__", SymbolCreateWithType(SymbolFlagsClassMember, getFunction, nil))

	dataclassTypeParams := make([]Type, 0, len(dataclass.Shared.TypeParams))
	for _, tp := range dataclass.Shared.TypeParams {
		dataclassTypeParams = append(dataclassTypeParams, tp)
	}
	descriptorInstance := ClassTypeSpecialize(
		ClassTypeCloneAsInstance(descriptorClass, false), dataclassTypeParams, nil, false, nil, nil)

	return SymbolCreateWithType(SymbolFlagsClassMember, descriptorInstance, fieldNameNode)
}

// transformDescriptorType corresponds to the function of the same name: if the
// field's declared type is itself a descriptor, `__init__` takes what its
// __set__ takes, not the descriptor.
func transformDescriptorType(evaluator TypeEvaluator, t Type) Type {
	if !IsClassInstance(t) || IsMetaclassInstance(t) {
		return t
	}

	setMethodType := evaluator.GetBoundMagicMethod(t.(*ClassType), "__set__", nil, nil, nil, 0)
	if setMethodType == nil {
		return t
	}

	if !IsFunction(setMethodType) {
		return t
	}

	// The original's comment: the value parameter for a bound __set__ method is
	// parameter index 1.
	return FunctionTypeGetParamType(setMethodType.(*FunctionType), 1)
}

// isDataclassFieldConstructor corresponds to the function of the same name.
func isDataclassFieldConstructor(t Type, fieldDescriptorNames []string) bool {
	callName := ""

	switch {
	case IsFunction(t):
		callName = t.(*FunctionType).Shared.FullName
	case IsOverloaded(t):
		overloads := OverloadedTypeGetOverloads(t.(*OverloadedType))
		if len(overloads) > 0 {
			callName = overloads[0].Shared.FullName
		}
	case IsInstantiableClass(t):
		callName = t.(*ClassType).Shared.FullName
	}

	if callName == "" {
		return false
	}

	for _, name := range fieldDescriptorNames {
		if name == callName {
			return true
		}
	}
	return false
}
