/*
 * properties.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/properties.ts (pyright 1.1.412), in full.
 *
 * A `@property` is not modelled as a special kind of symbol. It is modelled as
 * an *instance of a synthesized class* that carries `__get__`, `__set__` and
 * `__delete__` methods built from the decorated functions, plus `getter`,
 * `setter` and `deleter` methods so the decorator chain keeps working. Once that
 * object exists, ordinary descriptor handling does the rest -- which is why
 * nothing outside this file needs to know that properties are special.
 *
 * `@x.setter` and `@x.deleter` do not mutate the property; they clone it. Each
 * clone rebuilds the whole synthesized symbol table from the accessors it
 * carries, because `__get__`'s declared return type depends on the getter and
 * `__set__`'s value parameter depends on the setter.
 *
 * Two details that repay attention:
 *
 * - The two `__get__` overloads are registered in the order (instance, class),
 *   not (class, instance). The original carries a comment explaining why: for
 *   NoneType, `None.__class__` is a property and None matches the `obj: None`
 *   parameter of the class-access overload, so listing that one first would win
 *   the wrong match.
 *
 * - Setters may be overloaded, so fsetInfo.methodType is a FunctionType *or* an
 *   OverloadedType, and combineSetterOverloads accumulates across successive
 *   `@x.setter` declarations. Everything downstream that reads it must handle
 *   both.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// ValidatePropertyMethod corresponds to validatePropertyMethod.
func ValidatePropertyMethod(evaluator TypeEvaluator, method *FunctionType, errorNode parser.ParseNode) {
	if FunctionTypeIsStaticMethod(method) {
		evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.PropertyStaticMethod(), errorNode, nil)
	}
}

// CreateProperty corresponds to createProperty.
func CreateProperty(
	evaluator TypeEvaluator,
	decoratorNode *parser.DecoratorNode,
	decoratorType *ClassType,
	fget *FunctionType,
) Type {
	fileInfo := GetFileInfo(decoratorNode)
	typeMetaclass := evaluator.GetBuiltInType(decoratorNode, "type")
	typeSourceID := decoratorType.Shared.TypeSourceID
	if ClassTypeIsBuiltInNamed(decoratorType, "property") {
		typeSourceID = TypeSourceId(GetTypeSourceID(decoratorNode))
	}

	var effectiveMetaclass Type = UnknownTypeCreate(false)
	if IsInstantiableClass(typeMetaclass) {
		effectiveMetaclass = typeMetaclass
	}

	propertyClass := ClassTypeCreateInstantiable(
		decoratorType.Shared.Name,
		GetClassFullName(decoratorNode, fileInfo.ModuleName, "__property_"+fget.Shared.Name),
		fileInfo.ModuleName,
		fileInfo.FileUri,
		ClassTypeFlagsPropertyClass|ClassTypeFlagsBuiltIn,
		typeSourceID,
		nil,
		effectiveMetaclass,
		nil,
	)

	propertyClass.Shared.Declaration = decoratorType.Shared.Declaration
	propertyClass.Shared.TypeVarScopeID = decoratorType.Shared.TypeVarScopeID
	objectType := evaluator.GetBuiltInType(decoratorNode, "object")
	if IsInstantiableClass(objectType) {
		propertyClass.Shared.BaseClasses = append(propertyClass.Shared.BaseClasses, objectType)
	} else {
		propertyClass.Shared.BaseClasses = append(propertyClass.Shared.BaseClasses, UnknownTypeCreate(false))
	}
	ComputeMroLinearization(propertyClass)

	// The original's comment: clone the symbol table of the old class type.
	fields := ClassTypeGetSymbolTable(propertyClass)
	decoratorFields := ClassTypeGetSymbolTable(decoratorType)
	for _, name := range decoratorFields.Keys() {
		symbol, _ := decoratorFields.Get(name)
		if symbol.IsIgnoredForProtocolMatch() {
			continue
		}
		if name == "__get__" || name == "__set__" || name == "__delete__" {
			continue
		}
		fields.Set(name, symbol)
	}

	propertyObject := ClassTypeCloneAsInstance(propertyClass, true)
	isAsymmetric := false
	propertyClass.Priv.IsAsymmetricDescriptor = &isAsymmetric

	// The original's comment: update the __set__ and __delete__ methods if present.
	updateGetSetDelMethodForClonedProperty(evaluator, propertyObject)

	// The original's comment: fill in the fget method.
	propertyObject.Priv.FgetInfo = &PropertyMethodInfo{
		MethodType: fget,
		ClassType:  fget.Shared.MethodClass,
	}

	if FunctionTypeIsClassMethod(fget) {
		propertyClass.Shared.Flags |= ClassTypeFlagsClassProperty
	}

	// The original's comment: fill in the __get__ method with an overload.
	addGetMethodToPropertySymbolTable(evaluator, propertyObject, fget)

	// The original's comment: fill in the getter, setter and deleter methods.
	addDecoratorMethodsToPropertySymbolTable(propertyObject)

	return propertyObject
}

// ClonePropertyWithSetter corresponds to clonePropertyWithSetter.
func ClonePropertyWithSetter(
	evaluator TypeEvaluator, prop Type, fset *FunctionType, errorNode *parser.FunctionNode,
) Type {
	if !IsProperty(prop) {
		return prop
	}

	classType := prop.(*ClassType)
	flagsToClone := classType.Shared.Flags
	isAsymmetricDescriptor := classType.Priv.IsAsymmetricDescriptor != nil &&
		*classType.Priv.IsAsymmetricDescriptor

	// The original's comment: verify parameters for fset. We'll skip this test if
	// the diagnostic rule is disabled because it can be somewhat expensive,
	// especially in code that is not annotated.
	fileInfo := GetFileInfo(errorNode)
	if len(errorNode.D.Params) >= 2 {
		typeAnnotation := GetTypeAnnotationForParam(errorNode, 1)
		if typeAnnotation != nil {
			// The original's comment: verify consistency of the type.
			fgetType := evaluator.GetGetterTypeFromProperty(classType)
			if fgetType != nil && !IsAnyOrUnknown(fgetType) {
				fsetType := evaluator.GetTypeOfAnnotation(typeAnnotation,
					&ExpectedTypeOptions{TypeVarGetsCurScope: true})

				// The original's comment: the setter type should be assignable to the
				// getter type.
				if fileInfo.DiagnosticRuleSet.ReportPropertyTypeMismatch != DiagnosticLevelNone {
					diag := common.NewDiagnosticAddendum()
					if !evaluator.AssignType(fgetType, fsetType, diag, nil, AssignTypeFlagsDefault, 0) {
						evaluator.AddDiagnostic(DiagnosticRuleReportPropertyTypeMismatch,
							localization.LocMessage.SetterGetterTypeMismatch()+diag.GetString(),
							typeAnnotation, nil)
					}
				}

				if !IsTypeSame(fgetType, fsetType, TypeSameOptions{}, 0) {
					isAsymmetricDescriptor = true
				}
			}
		}
	}

	propertyClass := ClassTypeCreateInstantiable(
		classType.Shared.Name,
		classType.Shared.FullName,
		classType.Shared.ModuleName,
		GetFileInfo(errorNode).FileUri,
		flagsToClone,
		classType.Shared.TypeSourceID,
		classType.Shared.DeclaredMetaclass,
		classType.Shared.EffectiveMetaclass,
		nil,
	)

	propertyClass.Shared.Declaration = classType.Shared.Declaration
	propertyClass.Shared.TypeVarScopeID = classType.Shared.TypeVarScopeID
	objectType := evaluator.GetBuiltInType(errorNode, "object")
	if IsInstantiableClass(objectType) {
		propertyClass.Shared.BaseClasses = append(propertyClass.Shared.BaseClasses, objectType)
	} else {
		propertyClass.Shared.BaseClasses = append(propertyClass.Shared.BaseClasses, UnknownTypeCreate(false))
	}
	ComputeMroLinearization(propertyClass)

	propertyClass.Priv.FgetInfo = classType.Priv.FgetInfo
	propertyClass.Priv.FdelInfo = classType.Priv.FdelInfo
	propertyClass.Priv.IsAsymmetricDescriptor = &isAsymmetricDescriptor
	propertyObject := ClassTypeCloneAsInstance(propertyClass, true)

	// The original's comment: clone the symbol table of the old class type.
	fields := ClassTypeGetSymbolTable(propertyClass)
	oldFields := ClassTypeGetSymbolTable(classType)
	for _, name := range oldFields.Keys() {
		symbol, _ := oldFields.Get(name)
		if !symbol.IsIgnoredForProtocolMatch() {
			fields.Set(name, symbol)
		}
	}

	// The original's comment: update the __get__ and __delete__ methods if present.
	updateGetSetDelMethodForClonedProperty(evaluator, propertyObject)

	// The original's comment: combine this setter with any overloaded setters
	// accumulated from previous declarations of this property. This supports
	// overloads on property setters.
	var prevSetter Type
	if classType.Priv.FsetInfo != nil {
		prevSetter = classType.Priv.FsetInfo.MethodType
	}
	combinedSetter := combineSetterOverloads(prevSetter, fset)

	// The original's comment: fill in the new fset method.
	propertyObject.Priv.FsetInfo = &PropertyMethodInfo{
		MethodType: combinedSetter,
		ClassType:  fset.Shared.MethodClass,
	}

	// The original's comment: fill in the __set__ method.
	addSetMethodToPropertySymbolTable(evaluator, propertyObject, combinedSetter)

	// The original's comment: fill in the getter, setter and deleter methods.
	addDecoratorMethodsToPropertySymbolTable(propertyObject)

	return propertyObject
}

// ClonePropertyWithDeleter corresponds to clonePropertyWithDeleter.
func ClonePropertyWithDeleter(
	evaluator TypeEvaluator, prop Type, fdel *FunctionType, errorNode *parser.FunctionNode,
) Type {
	if !IsProperty(prop) {
		return prop
	}

	classType := prop.(*ClassType)
	propertyClass := ClassTypeCreateInstantiable(
		classType.Shared.Name,
		classType.Shared.FullName,
		classType.Shared.ModuleName,
		GetFileInfo(errorNode).FileUri,
		classType.Shared.Flags,
		classType.Shared.TypeSourceID,
		classType.Shared.DeclaredMetaclass,
		classType.Shared.EffectiveMetaclass,
		nil,
	)

	propertyClass.Shared.Declaration = classType.Shared.Declaration
	propertyClass.Shared.TypeVarScopeID = classType.Shared.TypeVarScopeID
	objectType := evaluator.GetBuiltInType(errorNode, "object")
	if IsInstantiableClass(objectType) {
		propertyClass.Shared.BaseClasses = append(propertyClass.Shared.BaseClasses, objectType)
	} else {
		propertyClass.Shared.BaseClasses = append(propertyClass.Shared.BaseClasses, UnknownTypeCreate(false))
	}
	ComputeMroLinearization(propertyClass)

	propertyClass.Priv.FgetInfo = classType.Priv.FgetInfo
	propertyClass.Priv.FsetInfo = classType.Priv.FsetInfo
	propertyObject := ClassTypeCloneAsInstance(propertyClass, true)
	// The original sets isAsymmetricDescriptor on propertyClass *after* cloning,
	// so the clone does not see it. Kept as written.
	isAsymmetric := classType.Priv.IsAsymmetricDescriptor != nil && *classType.Priv.IsAsymmetricDescriptor
	propertyClass.Priv.IsAsymmetricDescriptor = &isAsymmetric

	// The original's comment: clone the symbol table of the old class type.
	fields := ClassTypeGetSymbolTable(propertyClass)
	oldFields := ClassTypeGetSymbolTable(classType)
	for _, name := range oldFields.Keys() {
		symbol, _ := oldFields.Get(name)
		if !symbol.IsIgnoredForProtocolMatch() {
			fields.Set(name, symbol)
		}
	}

	// The original's comment: update the __get__ and __set__ methods if present.
	updateGetSetDelMethodForClonedProperty(evaluator, propertyObject)

	// The original's comment: fill in the fdel method.
	propertyObject.Priv.FdelInfo = &PropertyMethodInfo{
		MethodType: fdel,
		ClassType:  fdel.Shared.MethodClass,
	}

	// The original's comment: fill in the __delete__ method.
	addDelMethodToPropertySymbolTable(evaluator, propertyObject, fdel)

	// The original's comment: fill in the getter, setter and deleter methods.
	addDecoratorMethodsToPropertySymbolTable(propertyObject)

	return propertyObject
}

func addGetMethodToPropertySymbolTable(
	evaluator TypeEvaluator, propertyObject *ClassType, fget *FunctionType,
) {
	fields := ClassTypeGetSymbolTable(propertyObject)

	selfName := "self"
	objName := "obj"
	objtypeName := "objtype"

	// The original's comment: the first overload is for accesses through a class
	// object (where the instance argument is None).
	getFunction1 := FunctionTypeCreateSynthesizedInstance("__get__", FunctionTypeFlagsOverloaded)
	FunctionTypeAddParam(getFunction1, FunctionParamCreate(
		parser.ParamCategorySimple, AnyTypeCreate(false), FunctionParamFlagsTypeDeclared, &selfName, nil, nil))
	FunctionTypeAddParam(getFunction1, FunctionParamCreate(
		parser.ParamCategorySimple, evaluator.GetNoneType(), FunctionParamFlagsTypeDeclared, &objName, nil, nil))
	FunctionTypeAddParam(getFunction1, FunctionParamCreate(
		parser.ParamCategorySimple, AnyTypeCreate(false), FunctionParamFlagsTypeDeclared, &objtypeName,
		AnyTypeCreate(true), nil))

	if FunctionTypeIsClassMethod(fget) {
		getFunction1.Shared.DeclaredReturnType = FunctionTypeGetEffectiveReturnType(fget, true)
	} else {
		getFunction1.Shared.DeclaredReturnType = propertyObject
	}
	getFunction1.Shared.Declaration = fget.Shared.Declaration
	getFunction1.Shared.DeprecatedMessage = fget.Shared.DeprecatedMessage
	getFunction1.Shared.MethodClass = fget.Shared.MethodClass

	// The original's comment: override the scope ID since we're using parameter
	// types from the decorated function.
	getFunction1.Shared.TypeVarScopeID = GetTypeVarScopeID(fget)

	// The original's comment: the second overload is for accesses through a class
	// instance.
	getFunction2 := FunctionTypeCreateSynthesizedInstance("__get__", FunctionTypeFlagsOverloaded)
	FunctionTypeAddParam(getFunction2, FunctionParamCreate(
		parser.ParamCategorySimple, AnyTypeCreate(false), FunctionParamFlagsTypeDeclared, &selfName, nil, nil))

	var objType Type = AnyTypeCreate(false)
	if len(fget.Shared.Parameters) > 0 {
		objType = FunctionTypeGetParamType(fget, 0)
	}

	FunctionTypeAddParam(getFunction2, FunctionParamCreate(
		parser.ParamCategorySimple, objType, FunctionParamFlagsTypeDeclared, &objName, nil, nil))

	FunctionTypeAddParam(getFunction2, FunctionParamCreate(
		parser.ParamCategorySimple, AnyTypeCreate(false), FunctionParamFlagsTypeDeclared, &objtypeName,
		AnyTypeCreate(true), nil))

	getFunction2.Shared.DeclaredReturnType = FunctionTypeGetEffectiveReturnType(fget, true)
	getFunction2.Shared.Declaration = fget.Shared.Declaration
	getFunction2.Shared.DeprecatedMessage = fget.Shared.DeprecatedMessage
	getFunction2.Shared.MethodClass = fget.Shared.MethodClass

	// The original's comment: override the scope ID since we're using parameter
	// types from the decorated function.
	getFunction2.Shared.TypeVarScopeID = GetTypeVarScopeID(fget)

	// The original's comment: we previously placed getFunction1 before
	// getFunction2, but this creates problems specifically for the `NoneType` class
	// because None.__class__ is a property, and both overloads match in this case
	// because None is passed for the "obj" parameter.
	getFunctionOverload := OverloadedTypeCreate([]*FunctionType{getFunction2, getFunction1}, nil)
	getSymbol := SymbolCreateWithType(SymbolFlagsClassMember, getFunctionOverload, nil)
	fields.Set("__get__", getSymbol)
}

// combineSetterOverloads corresponds to the function of the same name.
//
// The original's comment: combines a newly-decorated setter function with any
// setter overloads that were accumulated from previous declarations of the same
// property. The resulting type is a single FunctionType for a non-overloaded
// setter or an OverloadedType when the setter has overloads.
func combineSetterOverloads(prevSetter Type, newSetter *FunctionType) Type {
	// The original's comment: gather any overload signatures from the previous
	// setter. Note: a lone `@overload` setter with no implementation is represented
	// as a FunctionType with the Overloaded flag set, and is treated as a single
	// overload here. A malformed source with multiple implementations is not
	// specially handled; the most recent implementation wins, consistent with
	// normal functions.
	prevOverloads := []*FunctionType{}
	if prevSetter != nil {
		if IsOverloaded(prevSetter) {
			prevOverloads = append(prevOverloads,
				OverloadedTypeGetOverloads(prevSetter.(*OverloadedType))...)
		} else if IsFunction(prevSetter) && FunctionTypeIsOverloaded(prevSetter.(*FunctionType)) {
			prevOverloads = append(prevOverloads, prevSetter.(*FunctionType))
		}
	}

	if FunctionTypeIsOverloaded(newSetter) {
		// The original's comment: the new setter is an overload. Append it to the
		// accumulated overloads.
		overloads := append(append([]*FunctionType{}, prevOverloads...), newSetter)
		if len(overloads) > 1 {
			return OverloadedTypeCreate(overloads, nil)
		}
		return newSetter
	}

	// The original's comment: the new setter is not an overload, so it's either a
	// plain setter or the implementation for a set of setter overloads. If there
	// are accumulated overloads, treat it as the implementation; otherwise it's a
	// plain setter.
	if len(prevOverloads) > 0 {
		// The original's comment: per PEP 702, if the setter implementation is marked
		// @deprecated, all of its overloads inherit the deprecation. This mirrors the
		// behavior of addOverloadsToFunctionType for normal overloaded functions.
		overloads := prevOverloads
		if newSetter.Shared.DeprecatedMessage != nil {
			deprecationMessage := newSetter.Shared.DeprecatedMessage
			mapped := make([]*FunctionType, 0, len(overloads))
			for _, overload := range overloads {
				if overload.Shared.DeprecatedMessage == nil {
					mapped = append(mapped,
						FunctionTypeCloneWithDeprecatedMessage(overload, deprecationMessage))
				} else {
					mapped = append(mapped, overload)
				}
			}
			overloads = mapped
		}

		return OverloadedTypeCreate(overloads, newSetter)
	}

	return newSetter
}

func addSetMethodToPropertySymbolTable(
	evaluator TypeEvaluator, propertyObject *ClassType, fset Type,
) {
	fields := ClassTypeGetSymbolTable(propertyObject)

	var setMethod Type
	if IsOverloaded(fset) {
		// The original's comment: synthesize one __set__ overload per setter overload
		// (excluding the implementation, consistent with normal overloaded function
		// semantics). combineSetterOverloads only produces an OverloadedType when
		// there is at least one overload, so setOverloads is guaranteed to be
		// non-empty here.
		overloads := OverloadedTypeGetOverloads(fset.(*OverloadedType))
		setOverloads := make([]*FunctionType, 0, len(overloads))
		for _, overload := range overloads {
			setOverloads = append(setOverloads, createSetMethodFromSetter(evaluator, overload, true))
		}

		if len(setOverloads) > 1 {
			setMethod = OverloadedTypeCreate(setOverloads, nil)
		} else {
			setMethod = setOverloads[0]
		}
	} else {
		setMethod = createSetMethodFromSetter(evaluator, fset.(*FunctionType), false)
	}

	setSymbol := SymbolCreateWithType(SymbolFlagsClassMember, setMethod, nil)
	fields.Set("__set__", setSymbol)
}

func createSetMethodFromSetter(
	evaluator TypeEvaluator, fset *FunctionType, asOverload bool,
) *FunctionType {
	flags := FunctionTypeFlagsNone
	if asOverload {
		flags = FunctionTypeFlagsOverloaded
	}
	setFunction := FunctionTypeCreateSynthesizedInstance("__set__", flags)

	selfName := "self"
	objName := "obj"
	valueName := "value"

	FunctionTypeAddParam(setFunction, FunctionParamCreate(
		parser.ParamCategorySimple, AnyTypeCreate(false), FunctionParamFlagsTypeDeclared, &selfName, nil, nil))

	var objType Type = AnyTypeCreate(false)
	if len(fset.Shared.Parameters) > 0 {
		objType = FunctionTypeGetParamType(fset, 0)
	}
	if IsTypeVar(objType) && TypeVarTypeIsSelf(objType.(*TypeVarType)) {
		objType = evaluator.MakeTopLevelTypeVarsConcrete(objType, false)
	}

	FunctionTypeAddParam(setFunction, FunctionParamCreate(
		parser.ParamCategorySimple,
		CombineTypes([]Type{objType, evaluator.GetNoneType()}, nil),
		FunctionParamFlagsTypeDeclared, &objName, nil, nil))

	setFunction.Shared.DeclaredReturnType = evaluator.GetNoneType()

	// The original's comment: adopt the TypeVarScopeId of the fset function in case
	// it has any TypeVars that need to be solved.
	setFunction.Shared.TypeVarScopeID = GetTypeVarScopeID(fset)
	setFunction.Shared.DeprecatedMessage = fset.Shared.DeprecatedMessage
	setFunction.Shared.MethodClass = fset.Shared.MethodClass

	var setParamType Type = UnknownTypeCreate(false)

	if len(fset.Shared.Parameters) >= 2 &&
		fset.Shared.Parameters[1].Category == parser.ParamCategorySimple &&
		fset.Shared.Parameters[1].Name != nil {
		setParamType = FunctionTypeGetParamType(fset, 1)
	}
	FunctionTypeAddParam(setFunction, FunctionParamCreate(
		parser.ParamCategorySimple, setParamType, FunctionParamFlagsTypeDeclared, &valueName, nil, nil))

	return setFunction
}

func addDelMethodToPropertySymbolTable(
	evaluator TypeEvaluator, propertyObject *ClassType, fdel *FunctionType,
) {
	fields := ClassTypeGetSymbolTable(propertyObject)

	selfName := "self"
	objName := "obj"

	delFunction := FunctionTypeCreateSynthesizedInstance("__delete__", FunctionTypeFlagsNone)
	FunctionTypeAddParam(delFunction, FunctionParamCreate(
		parser.ParamCategorySimple, AnyTypeCreate(false), FunctionParamFlagsTypeDeclared, &selfName, nil, nil))

	// The original's comment: adopt the TypeVarScopeId of the fdel function in case
	// it has any TypeVars that need to be solved.
	delFunction.Shared.TypeVarScopeID = GetTypeVarScopeID(fdel)
	delFunction.Shared.DeprecatedMessage = fdel.Shared.DeprecatedMessage
	delFunction.Shared.MethodClass = fdel.Shared.MethodClass

	var objType Type = AnyTypeCreate(false)
	if len(fdel.Shared.Parameters) > 0 {
		objType = FunctionTypeGetParamType(fdel, 0)
	}

	if IsTypeVar(objType) && TypeVarTypeIsSelf(objType.(*TypeVarType)) {
		objType = evaluator.MakeTopLevelTypeVarsConcrete(objType, false)
	}

	FunctionTypeAddParam(delFunction, FunctionParamCreate(
		parser.ParamCategorySimple,
		CombineTypes([]Type{objType, evaluator.GetNoneType()}, nil),
		FunctionParamFlagsTypeDeclared, &objName, nil, nil))
	delFunction.Shared.DeclaredReturnType = evaluator.GetNoneType()
	delSymbol := SymbolCreateWithType(SymbolFlagsClassMember, delFunction, nil)
	fields.Set("__delete__", delSymbol)
}

func updateGetSetDelMethodForClonedProperty(evaluator TypeEvaluator, propertyObject *ClassType) {
	fgetInfo := propertyObject.Priv.FgetInfo
	if fgetInfo != nil && IsFunction(fgetInfo.MethodType) {
		addGetMethodToPropertySymbolTable(evaluator, propertyObject, fgetInfo.MethodType.(*FunctionType))
	}

	fsetInfo := propertyObject.Priv.FsetInfo
	if fsetInfo != nil && (IsFunction(fsetInfo.MethodType) || IsOverloaded(fsetInfo.MethodType)) {
		addSetMethodToPropertySymbolTable(evaluator, propertyObject, fsetInfo.MethodType)
	}

	fdelInfo := propertyObject.Priv.FdelInfo
	if fdelInfo != nil && IsFunction(fdelInfo.MethodType) {
		addDelMethodToPropertySymbolTable(evaluator, propertyObject, fdelInfo.MethodType.(*FunctionType))
	}
}

func addDecoratorMethodsToPropertySymbolTable(propertyObject *ClassType) {
	fields := ClassTypeGetSymbolTable(propertyObject)

	selfName := "self"
	accessorName := "accessor"

	// The original's comment: fill in the getter, setter and deleter methods.
	for _, name := range []string{"getter", "setter", "deleter"} {
		accessorFunction := FunctionTypeCreateSynthesizedInstance(name, FunctionTypeFlagsNone)
		FunctionTypeAddParam(accessorFunction, FunctionParamCreate(
			parser.ParamCategorySimple, AnyTypeCreate(false), FunctionParamFlagsTypeDeclared, &selfName, nil, nil))
		FunctionTypeAddParam(accessorFunction, FunctionParamCreate(
			parser.ParamCategorySimple, AnyTypeCreate(false), FunctionParamFlagsTypeDeclared, &accessorName, nil, nil))
		accessorFunction.Shared.DeclaredReturnType = propertyObject
		accessorSymbol := SymbolCreateWithType(SymbolFlagsClassMember, accessorFunction, nil)
		fields.Set(name, accessorSymbol)
	}
}

// AssignProperty corresponds to assignProperty.
func AssignProperty(
	evaluator TypeEvaluator,
	destPropertyType *ClassType,
	srcPropertyType *ClassType,
	destClass *ClassType,
	srcClass Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	selfSolution *ConstraintSolution,
	recursionCount int,
) bool {
	var srcObjectToBind *ClassType
	if IsClass(srcClass) {
		srcObjectToBind = ClassTypeCloneAsInstance(srcClass.(*ClassType), true)
	}
	destObjectToBind := ClassTypeCloneAsInstance(destClass, true)
	isAssignable := true

	type accessorInfo struct {
		getFunction         func(c *ClassType) Type
		missingDiagMsg      func() string
		incompatibleDiagMsg func() string
	}

	accessors := []accessorInfo{
		{
			getFunction: func(c *ClassType) Type {
				if c.Priv.FgetInfo == nil {
					return nil
				}
				return c.Priv.FgetInfo.MethodType
			},
			missingDiagMsg:      localization.LocAddendum.MissingGetter,
			incompatibleDiagMsg: localization.LocAddendum.IncompatibleGetter,
		},
		{
			getFunction: func(c *ClassType) Type {
				if c.Priv.FsetInfo == nil {
					return nil
				}
				return c.Priv.FsetInfo.MethodType
			},
			missingDiagMsg:      localization.LocAddendum.MissingSetter,
			incompatibleDiagMsg: localization.LocAddendum.IncompatibleSetter,
		},
		{
			getFunction: func(c *ClassType) Type {
				if c.Priv.FdelInfo == nil {
					return nil
				}
				return c.Priv.FdelInfo.MethodType
			},
			missingDiagMsg:      localization.LocAddendum.MissingDeleter,
			incompatibleDiagMsg: localization.LocAddendum.IncompatibleDeleter,
		},
	}

	for _, accessor := range accessors {
		destAccessType := accessor.getFunction(destPropertyType)

		// The original's comment: handle both single-function accessors and
		// overloaded accessors (e.g. overloaded property setters). assignType and
		// bindFunctionToClassOrObject both understand OverloadedType, so the
		// comparison logic below works for either form.
		if destAccessType == nil || (!IsFunction(destAccessType) && !IsOverloaded(destAccessType)) {
			continue
		}

		srcAccessType := accessor.getFunction(srcPropertyType)

		if srcAccessType == nil || (!IsFunction(srcAccessType) && !IsOverloaded(srcAccessType)) {
			if diag != nil {
				diag.AddMessage(accessor.missingDiagMsg())
			}
			isAssignable = false
			continue
		}

		evaluator.InferReturnTypeIfNecessary(srcAccessType)
		evaluator.InferReturnTypeIfNecessary(destAccessType)

		// The original's comment: if the caller provided a "self" TypeVar context,
		// replace any Self types. This is needed during protocol matching.
		if selfSolution != nil {
			destAccessType = ApplySolvedTypeVars(destAccessType, selfSolution, nil)
		}

		var destDiag, srcDiag *common.DiagnosticAddendum
		if diag != nil {
			destDiag = diag.CreateAddendum()
			srcDiag = diag.CreateAddendum()
		}

		boundDestAccessType := evaluator.BindFunctionToClassOrObject(
			destObjectToBind, destAccessType, nil, false, nil, destDiag, recursionCount)
		if boundDestAccessType == nil {
			boundDestAccessType = destAccessType
		}

		boundSrcAccessType := evaluator.BindFunctionToClassOrObject(
			srcObjectToBind, srcAccessType, nil, false, nil, srcDiag, recursionCount)
		if boundSrcAccessType == nil {
			boundSrcAccessType = srcAccessType
		}

		if !evaluator.AssignType(boundDestAccessType, boundSrcAccessType, diag, constraints,
			AssignTypeFlagsDefault, recursionCount) {
			isAssignable = false
		}
	}

	// incompatibleDiagMsg is carried on each accessor in the original but never
	// read; the diagnostic it would produce is left to assignType's addendum.
	_ = accessors

	return isAssignable
}
