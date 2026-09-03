/*
 * typeevaluator_descriptor.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * applyDescriptorAccessMethod, bindMethodForMemberAccess, applyAttributeAccessOverride,
 * getTypeOfMember, getTypeOfMemberInternal, isAsymmetricDescriptorClass,
 * isClassWithAsymmetricAttributeAccessor.
 *
 * The descriptor protocol, and the two overrides that stand in for it.
 *
 * A descriptor is an object whose class defines __get__, __set__ or __delete__;
 * reading `obj.attr` where attr holds one does not yield the descriptor but the
 * result of CALLING its access method. applyDescriptorAccessMethod simulates
 * that call rather than modelling it, synthesizing an argument list and running
 * it through validateCallArgs. The synthesized `obj` argument is None for a class
 * access, the instance for an instance access, and -- for a "class property" --
 * the class in both cases.
 *
 * Properties are descriptors with one extra problem. A property is generic in
 * practice, since fget's return type varies with the class it was declared in,
 * but `property` itself is not declared generic. Pyright works around this by
 * putting type variables scoped to the DECLARING class into the synthesized
 * __get__, so the specialization here has to be against fget's class, not against
 * the property class -- solved by assigning the declaring class to a self-
 * specialized copy of it and applying the result.
 *
 * Failure is reported differently for properties, because "this object has no
 * __set__" is a confusing way to say "this property has no setter".
 *
 * The two asymmetry predicates answer a narrower question: does __set__ accept a
 * different type than __get__ returns? If so, narrowing on assignment must not
 * assume the value written is the value read back. Both cache their answer on the
 * class, since the walk is not cheap and the answer cannot change.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// applyDescriptorAccessMethod corresponds to the function of the same name.
func (e *typeEvaluator) applyDescriptorAccessMethod(
	memberType Type,
	concreteMemberType *ClassType,
	memberInfo *ClassMember,
	classType *ClassType,
	selfType Type,
	flags MemberAccessFlags,
	errorNode parser.ExpressionNode,
	memberName string,
	usage *EvaluatorUsage,
	diag *common.DiagnosticAddendum,
) *MemberAccessTypeResult {
	isAccessedThroughObject := classType.Base().IsInstance()

	var accessMethodName string
	switch usage.Method {
	case "get":
		accessMethodName = "__get__"
	case "set":
		accessMethodName = "__set__"
	default:
		accessMethodName = "__delete__"
	}

	var subDiag *common.DiagnosticAddendum
	if diag != nil {
		subDiag = common.NewDiagnosticAddendum()
	}

	methodTypeResult := e.GetTypeOfBoundMember(errorNode, concreteMemberType, accessMethodName,
		nil, subDiag,
		MemberAccessFlagsSkipInstanceMembers|MemberAccessFlagsSkipAttributeAccessOverride, nil)

	if methodTypeResult == nil || methodTypeResult.TypeErrors {
		// The original's comment: provide special error messages for properties.
		if ClassTypeIsPropertyClass(concreteMemberType) && usage.Method != "get" {
			message := localization.LocAddendum.PropertyMissingDeleter().Format(memberName)
			if usage.Method == "set" {
				message = localization.LocAddendum.PropertyMissingSetter().Format(memberName)
			}
			if diag != nil {
				diag.AddMessage(message)
			}
			return &MemberAccessTypeResult{Type: AnyTypeCreate(false), TypeErrors: true}
		}

		if classType.Shared.TypeVarScopeID != "" {
			memberType = MakeTypeVarsBound(memberType, []TypeVarScopeId{classType.Shared.TypeVarScopeID}, true)
		}

		return &MemberAccessTypeResult{Type: memberType}
	}

	methodClassType := methodTypeResult.ClassType
	methodType := methodTypeResult.Type

	if methodTypeResult.TypeErrors || methodClassType == nil {
		if diag != nil && subDiag != nil {
			diag.AddAddendum(subDiag)
		}
		return &MemberAccessTypeResult{Type: UnknownTypeCreate(false), TypeErrors: true}
	}

	if !IsFunctionOrOverloaded(methodType) {
		if IsAnyOrUnknown(methodType) {
			return &MemberAccessTypeResult{Type: methodType}
		}

		// The original's comment: TODO - emit an error for this condition.
		return &MemberAccessTypeResult{Type: memberType, TypeErrors: true}
	}

	// The original's comment: special-case logic for properties.
	if ClassTypeIsPropertyClass(concreteMemberType) && memberInfo != nil &&
		IsInstantiableClass(memberInfo.ClassType) && methodType != nil {
		// The original's comment: if the property is being accessed from a protocol
		// class (not an instance), flag this as an error because a property within a
		// protocol is meant to be interpreted as a read-only attribute rather than a
		// protocol, so accessing it directly from the class has an ambiguous meaning.
		if (flags&MemberAccessFlagsSkipInstanceMembers) != 0 && ClassTypeIsProtocolClass(classType) {
			if diag != nil {
				diag.AddMessage(localization.LocAddendum.PropertyAccessFromProtocolClass())
			}
			return &MemberAccessTypeResult{Type: memberType, TypeErrors: true}
		}

		// The original's comment: infer return types before specializing. Otherwise
		// a generic inferred return type won't be properly specialized.
		e.InferReturnTypeIfNecessary(methodType)

		methodType = e.specializePropertyAccessMethod(
			methodType, concreteMemberType, memberInfo.ClassType.(*ClassType), classType, selfType, usage)
	}

	// The original's comment: determine if we're calling __set__ on an asymmetric
	// descriptor or property.
	isAsymmetricAccessor := false
	if usage.Method == "set" && IsClass(methodClassType) {
		if e.isAsymmetricDescriptorClass(methodClassType.(*ClassType)) {
			isAsymmetricAccessor = true
		}
	}

	if methodType == nil {
		if diag != nil {
			diag.AddMessage(localization.LocAddendum.DescriptorAccessBindingFailed().Format(
				accessMethodName, e.PrintType(ConvertToInstance(methodClassType, true), nil)))
		}

		return &MemberAccessTypeResult{
			Type:                 UnknownTypeCreate(false),
			TypeErrors:           true,
			IsDescriptorApplied:  true,
			IsAsymmetricAccessor: isAsymmetricAccessor,
		}
	}

	// The original's comment: simulate a call to the access method.
	argList := []*Arg{}

	// The original's comment: provide "obj" argument.
	var objArgType Type
	if ClassTypeIsClassProperty(concreteMemberType) {
		// The original's comment: handle "class properties" as a special case. We
		// need to pass the class rather than the object instance in this case.
		if isAccessedThroughObject {
			objArgType = ClassTypeCloneAsInstantiable(classType, true)
		} else {
			objArgType = classType
		}
	} else if isAccessedThroughObject {
		objArgType = selfType
		if objArgType == nil {
			objArgType = ClassTypeCloneAsInstance(classType, true)
		}
	} else {
		objArgType = e.GetNoneType()
	}

	argList = append(argList, &Arg{
		ArgCategory: parser.ArgCategorySimple,
		TypeResult:  &TypeResult{Type: objArgType},
	})

	if usage.Method == "get" {
		var classArgType Type
		if selfType != nil {
			classArgType = ConvertToInstantiable(selfType, true)
		} else if isAccessedThroughObject {
			classArgType = ClassTypeCloneAsInstantiable(classType, true)
		} else {
			classArgType = classType
		}

		// The original's comment: provide "owner" argument.
		argList = append(argList, &Arg{
			ArgCategory: parser.ArgCategorySimple,
			TypeResult:  &TypeResult{Type: classArgType},
		})
	} else if usage.Method == "set" {
		// The original's comment: provide "value" argument.
		var valueType Type = UnknownTypeCreate(false)
		valueIncomplete := false
		if usage.SetType != nil {
			valueType = usage.SetType.Type
			valueIncomplete = usage.SetType.IsIncomplete
		}

		argList = append(argList, &Arg{
			ArgCategory: parser.ArgCategorySimple,
			TypeResult:  &TypeResult{Type: valueType, IsIncomplete: valueIncomplete},
		})
	}

	// The original's comment: suppress diagnostics for these method calls because
	// they would be redundant.
	var callResult *CallResult
	e.suppressDiagnostics(errorNode, func() {
		callResult = e.ValidateCallArgs(errorNode, argList, &TypeResult{Type: methodType},
			nil, true, nil)
	}, func(suppressedDiags []string) {
		// The original's comment: if diagnostics were recorded when suppressed, add
		// them to the diagnostic as messages.
		if diag != nil {
			for _, message := range suppressedDiags {
				diag.AddMessageMultiline(message)
			}
		}
	})

	// The original's comment: collect deprecation information associated with the
	// member access method.
	var deprecationInfo *MemberAccessDeprecationInfo
	if callResult != nil && len(callResult.OverloadsUsedForCall) >= 1 {
		overloadUsed := callResult.OverloadsUsedForCall[0]
		if overloadUsed.Shared.DeprecatedMessage != nil {
			accessType := "descriptor"
			if ClassTypeIsPropertyClass(concreteMemberType) {
				accessType = "property"
			}
			deprecationInfo = &MemberAccessDeprecationInfo{
				DeprecatedMessage: *overloadUsed.Shared.DeprecatedMessage,
				AccessType:        accessType,
				AccessMethod:      usage.Method,
			}
		}
	}

	if callResult != nil && !callResult.ArgumentErrors {
		// The original's comment: for set or delete, always return Any.
		var resultType Type = AnyTypeCreate(false)
		if usage.Method == "get" {
			resultType = callResult.ReturnType
			if resultType == nil {
				resultType = UnknownTypeCreate(false)
			}
		}

		return &MemberAccessTypeResult{
			Type:                        resultType,
			IsDescriptorApplied:         true,
			IsAsymmetricAccessor:        isAsymmetricAccessor,
			MemberAccessDeprecationInfo: deprecationInfo,
		}
	}

	return &MemberAccessTypeResult{
		Type:                        UnknownTypeCreate(false),
		TypeErrors:                  true,
		IsDescriptorApplied:         true,
		IsAsymmetricAccessor:        isAsymmetricAccessor,
		MemberAccessDeprecationInfo: deprecationInfo,
	}
}

// specializePropertyAccessMethod is the original's property specialization.
//
// Its comment: this specialization is required specifically for properties,
// which should be generic but are not defined that way. Because of this, we use
// type variables in the synthesized methods (e.g. __get__) for the property
// class that are defined in the class that declares the fget method.
func (e *typeEvaluator) specializePropertyAccessMethod(
	methodType Type,
	concreteMemberType *ClassType,
	declaringClass *ClassType,
	classType *ClassType,
	selfType Type,
	usage *EvaluatorUsage,
) Type {
	var accessMethodClass *ClassType
	switch usage.Method {
	case "get":
		if concreteMemberType.Priv.FgetInfo() != nil {
			accessMethodClass = concreteMemberType.Priv.FgetInfo().ClassType
		}
	case "set":
		if concreteMemberType.Priv.FsetInfo() != nil {
			accessMethodClass = concreteMemberType.Priv.FsetInfo().ClassType
		}
	default:
		if concreteMemberType.Priv.FdelInfo() != nil {
			accessMethodClass = concreteMemberType.Priv.FdelInfo().ClassType
		}
	}

	if accessMethodClass == nil {
		return methodType
	}

	constraints := NewConstraintTracker()
	accessMethodClass = SelfSpecializeClass(accessMethodClass, nil)
	e.AssignType(ClassTypeCloneAsInstance(accessMethodClass, true),
		ClassTypeCloneAsInstance(declaringClass, true), nil, constraints, AssignTypeFlagsDefault, 0)

	solved := e.SolveAndApplyConstraints(accessMethodClass, constraints, nil, nil)
	solvedClass, ok := solved.(*ClassType)
	if !ok {
		return methodType
	}

	var selfClass Type = classType
	if selfType != nil {
		selfClass = ConvertToInstantiable(selfType, true)
	}

	specializedType := PartiallySpecializeType(methodType, solvedClass, e.GetTypeClassType(), selfClass)

	if IsFunctionOrOverloaded(specializedType) {
		return specializedType
	}
	return methodType
}

// bindMethodForMemberAccess corresponds to the function of the same name.
func (e *typeEvaluator) bindMethodForMemberAccess(
	memberType Type,
	concreteType Type,
	memberInfo *ClassMember,
	classType *ClassType,
	selfType Type,
	flags MemberAccessFlags,
	memberName string,
	usage *EvaluatorUsage,
	diag *common.DiagnosticAddendum,
	recursionCount int,
) *TypeResult {
	// The original's comment: check for an attempt to overwrite a final method.
	if usage.Method == "set" {
		var impl Type
		if fn, ok := concreteType.(*FunctionType); ok {
			impl = fn
		} else if overloaded, ok := concreteType.(*OverloadedType); ok {
			impl = OverloadedTypeGetImplementation(overloaded)
		}

		if implFn, ok := impl.(*FunctionType); ok && FunctionTypeIsFinal(implFn) &&
			memberInfo != nil && IsClass(memberInfo.ClassType) {
			if diag != nil {
				diag.AddMessage(localization.LocMessage.FinalMethodOverride().Format(
					memberName, memberInfo.ClassType.(*ClassType).Shared.Name))
			}

			return &TypeResult{Type: UnknownTypeCreate(false), TypeErrors: true}
		}
	}

	// The original's comment: if this function is an instance member (e.g. a
	// lambda that was assigned to an instance variable), don't perform any
	// binding.
	if classType.Base().IsInstance() {
		if memberInfo == nil || memberInfo.IsInstanceMember {
			return &TypeResult{Type: memberType}
		}
	}

	var memberClass *ClassType
	if memberInfo != nil && IsInstantiableClass(memberInfo.ClassType) {
		memberClass = memberInfo.ClassType.(*ClassType)
	}

	effectiveSelfType := selfType
	if selfType != nil && IsClass(selfType) {
		effectiveSelfType = ClassTypeCloneIncludeSubclasses(selfType.(*ClassType), true)
	}

	boundType := e.BindFunctionToClassOrObject(classType, concreteType, memberClass,
		(flags&MemberAccessFlagsTreatConstructorAsClassMethod) != 0,
		effectiveSelfType, diag, recursionCount)

	if boundType == nil {
		return &TypeResult{Type: UnknownTypeCreate(false), TypeErrors: true}
	}
	return &TypeResult{Type: boundType}
}

// applyAttributeAccessOverride corresponds to the function of the same name.
//
// Its comment: applies the __getattr__, __setattr__ or __delattr__ method if
// present. If it's not applicable, returns undefined.
func (e *typeEvaluator) applyAttributeAccessOverride(
	errorNode parser.ExpressionNode,
	classType *ClassType,
	usage *EvaluatorUsage,
	memberName string,
	selfType Type,
) *MemberAccessTypeResult {
	getAttributeAccessMember := func(name string) Type {
		result := e.GetTypeOfBoundMember(errorNode, classType, name, nil, nil,
			MemberAccessFlagsSkipInstanceMembers|MemberAccessFlagsSkipObjectBaseClass|
				MemberAccessFlagsSkipTypeBaseClass|MemberAccessFlagsSkipAttributeAccessOverride,
			selfType)
		if result == nil {
			return nil
		}
		return result.Type
	}

	var accessMemberType Type
	switch usage.Method {
	case "get":
		accessMemberType = getAttributeAccessMember("__getattribute__")
		if accessMemberType == nil {
			accessMemberType = getAttributeAccessMember("__getattr__")
		}
	case "set":
		accessMemberType = getAttributeAccessMember("__setattr__")
	default:
		assert(usage.Method == "del", "expected a 'del' usage")
		accessMemberType = getAttributeAccessMember("__delattr__")
	}

	if accessMemberType == nil {
		return nil
	}

	argList := []*Arg{}

	// The original's comment: provide "name" argument.
	var nameArgType Type
	if strClass := e.GetStrClassType(); strClass != nil {
		nameArgType = ClassTypeCloneWithLiteral(ClassTypeCloneAsInstance(strClass, true), LiteralString(memberName))
	} else {
		nameArgType = AnyTypeCreate(false)
	}
	argList = append(argList, &Arg{
		ArgCategory: parser.ArgCategorySimple,
		TypeResult:  &TypeResult{Type: nameArgType},
	})

	if usage.Method == "set" {
		// The original's comment: provide "value" argument.
		var valueType Type = UnknownTypeCreate(false)
		valueIncomplete := false
		if usage.SetType != nil {
			valueType = usage.SetType.Type
			valueIncomplete = usage.SetType.IsIncomplete
		}
		argList = append(argList, &Arg{
			ArgCategory: parser.ArgCategorySimple,
			TypeResult:  &TypeResult{Type: valueType, IsIncomplete: valueIncomplete},
		})
	}

	if !IsFunctionOrOverloaded(accessMemberType) {
		if IsAnyOrUnknown(accessMemberType) {
			return &MemberAccessTypeResult{Type: accessMemberType}
		}

		// The original's comment: TODO - emit an error for this condition.
		return nil
	}

	callResult := e.ValidateCallArgs(errorNode, argList, &TypeResult{Type: accessMemberType},
		nil, true, nil)

	isAsymmetricAccessor := false
	if usage.Method == "set" {
		isAsymmetricAccessor = e.isClassWithAsymmetricAttributeAccessor(classType)
	}

	var returnType Type = UnknownTypeCreate(false)
	argumentErrors := false
	if callResult != nil {
		if callResult.ReturnType != nil {
			returnType = callResult.ReturnType
		}
		argumentErrors = callResult.ArgumentErrors
	}

	return &MemberAccessTypeResult{
		Type:                 returnType,
		TypeErrors:           argumentErrors,
		IsAsymmetricAccessor: isAsymmetricAccessor,
	}
}

// GetTypeOfMember corresponds to getTypeOfMember.
func (e *typeEvaluator) GetTypeOfMember(member *ClassMember) Type {
	if IsInstantiableClass(member.ClassType) {
		return PartiallySpecializeType(e.GetEffectiveTypeOfSymbol(member.Symbol),
			member.ClassType.(*ClassType), e.GetTypeClassType(), nil)
	}
	return UnknownTypeCreate(false)
}

// getTypeOfMemberInternal corresponds to the function of the same name. It
// returns nil where the original returns undefined.
func (e *typeEvaluator) getTypeOfMemberInternal(
	errorNode parser.ExpressionNode,
	member *ClassMember,
	selfClass Type,
	flags MemberAccessFlags,
) *TypeResult {
	if IsAnyOrUnknown(member.ClassType) {
		return &TypeResult{Type: member.ClassType}
	}

	if !IsInstantiableClass(member.ClassType) {
		return nil
	}

	typeResult := e.GetEffectiveTypeOfSymbolForUsage(member.Symbol, nil, false)
	if typeResult == nil {
		return nil
	}

	resolvedType := typeResult.Type

	// The original's comment: report inappropriate use of variables in type
	// expressions.
	if (flags&MemberAccessFlagsTypeExpression) != 0 && errorNode != nil {
		resolvedType = e.validateSymbolIsTypeExpression(errorNode, resolvedType, typeResult.IncludesVariableDecl)
	}

	// The original's comment: if the type is a function or overloaded function,
	// infer and cache the return type if necessary. This needs to be done prior to
	// specializing.
	e.InferReturnTypeIfNecessary(resolvedType)

	// The original's comment: check for ambiguous accesses to attributes with
	// generic types?
	if errorNode != nil && selfClass != nil && IsClass(selfClass) && member.IsInstanceMember &&
		IsClass(member.UnspecializedClassType) &&
		(flags&MemberAccessFlagsDisallowGenericInstanceVariableAccess) != 0 &&
		RequiresSpecialization(resolvedType,
			&RequiresSpecializationOptions{IgnoreSelf: true, IgnoreImplicitTypeArgs: true}, 0) {
		specializedType := PartiallySpecializeType(resolvedType,
			member.UnspecializedClassType.(*ClassType), e.GetTypeClassType(),
			SelfSpecializeClass(selfClass.(*ClassType), &SelfSpecializeOptions{OverrideTypeArgs: true}))

		if FindSubtype(specializedType, func(subtype Type) bool {
			return !IsFunctionOrOverloaded(subtype) &&
				RequiresSpecialization(subtype,
					&RequiresSpecializationOptions{IgnoreSelf: true, IgnoreImplicitTypeArgs: true}, 0)
		}) != nil {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.GenericInstanceVariableAccess(), errorNode, nil)
		}
	}

	return &TypeResult{
		Type: PartiallySpecializeType(resolvedType, member.ClassType.(*ClassType),
			e.GetTypeClassType(), selfClass),
		IsIncomplete: typeResult.IsIncomplete,
	}
}

// isAsymmetricDescriptorClass corresponds to the function of the same name. The
// answer is cached on the class because the walk is not cheap and the answer
// cannot change.
func (e *typeEvaluator) isAsymmetricDescriptorClass(classType *ClassType) bool {
	// The original's comment: if the value has already been cached in this type,
	// return the cached value.
	if classType.Priv.IsAsymmetricDescriptor() != nil {
		return *classType.Priv.IsAsymmetricDescriptor()
	}

	isAsymmetric := false

	getterSymbolResult := LookUpClassMember(classType, "__get__", MemberAccessFlagsSkipBaseClasses, nil)
	setterSymbolResult := LookUpClassMember(classType, "__set__", MemberAccessFlagsSkipBaseClasses, nil)

	if getterSymbolResult == nil || setterSymbolResult == nil {
		isAsymmetric = false
	} else {
		getterType := e.GetTypeOfMember(getterSymbolResult)
		setterType := e.GetTypeOfMember(setterSymbolResult)

		// The original's comment: if this is an overload, find the appropriate
		// overload.
		if overloaded, ok := getterType.(*OverloadedType); ok {
			getOverloads := []*FunctionType{}
			for _, overload := range OverloadedTypeGetOverloads(overloaded) {
				if len(overload.Shared.Parameters) < 2 {
					continue
				}
				if !IsNoneInstance(FunctionTypeGetParamType(overload, 1)) {
					getOverloads = append(getOverloads, overload)
				}
			}

			if len(getOverloads) == 1 {
				getterType = getOverloads[0]
			} else {
				isAsymmetric = true
			}
		}

		// The original's comment: if this is an overload, find the appropriate
		// overload.
		if IsOverloaded(setterType) {
			isAsymmetric = true
		}

		// The original's comment: if either the setter or getter is an overload (or
		// some other non-function type), conservatively assume that it's not
		// asymmetric.
		if accessorsDisagree(getterType, setterType) {
			isAsymmetric = true
		}
	}

	// The original's comment: cache the value for next time.
	classType.Priv.ensureRare().IsAsymmetricDescriptor = &isAsymmetric
	return isAsymmetric
}

// isClassWithAsymmetricAttributeAccessor corresponds to the function of the same
// name.
func (e *typeEvaluator) isClassWithAsymmetricAttributeAccessor(classType *ClassType) bool {
	// The original's comment: if the value has already been cached in this type,
	// return the cached value.
	if classType.Priv.IsAsymmetricAttributeAccessor() != nil {
		return *classType.Priv.IsAsymmetricAttributeAccessor()
	}

	isAsymmetric := false

	getterSymbolResult := LookUpClassMember(classType, "__getattr__", MemberAccessFlagsSkipBaseClasses, nil)
	setterSymbolResult := LookUpClassMember(classType, "__setattr__", MemberAccessFlagsSkipBaseClasses, nil)

	if getterSymbolResult != nil && setterSymbolResult != nil {
		getterType := e.GetEffectiveTypeOfSymbol(getterSymbolResult.Symbol)
		setterType := e.GetEffectiveTypeOfSymbol(setterSymbolResult.Symbol)

		// The original's comment: if either the setter or getter is an overload (or
		// some other non-function type), conservatively assume that it's not
		// asymmetric.
		if accessorsDisagree(getterType, setterType) {
			isAsymmetric = true
		}
	}

	// The original's comment: cache the value for next time.
	classType.Priv.ensureRare().IsAsymmetricAttributeAccessor = &isAsymmetric
	return isAsymmetric
}

// accessorsDisagree is the shared tail of the two asymmetry predicates: the
// value the setter accepts is not the value the getter returns.
//
// The original's comment: if there's no declared return type on the getter,
// assume it's symmetric.
func accessorsDisagree(getterType, setterType Type) bool {
	getterFn, getterOk := getterType.(*FunctionType)
	setterFn, setterOk := setterType.(*FunctionType)
	if !getterOk || !setterOk {
		return false
	}

	if len(setterFn.Shared.Parameters) < 3 || getterFn.Shared.DeclaredReturnType == nil {
		return false
	}

	setterValueType := FunctionTypeGetParamType(setterFn, 2)
	getterReturnType := FunctionTypeGetEffectiveReturnType(getterFn, true)
	if getterReturnType == nil {
		getterReturnType = UnknownTypeCreate(false)
	}

	return !IsTypeSame(setterValueType, getterReturnType, TypeSameOptions{}, 0)
}
