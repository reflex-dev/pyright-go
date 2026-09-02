/*
 * typeevaluator_classmembername.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfClassMemberName, applyDescriptorAccessMethod, bindMethodForMemberAccess,
 * applyAttributeAccessOverride, getTypeOfMemberInternal, getTypeOfMember,
 * isAsymmetricDescriptorClass, isClassWithAsymmetricAttributeAccessor.
 *
 * Resolving `x.name` once the class to look in is known. The MRO walk itself is
 * lookUpClassMember's job; this is everything that happens to what it finds.
 *
 * The lookup runs TWICE. The first pass asks for declared types only, so a
 * symbol with an annotation somewhere in the MRO wins over an inferred one lower
 * down. Only if that finds nothing does the second, unrestricted pass run.
 *
 * Finding nothing at all is not yet an error: __getattr__, __setattr__ and
 * __delattr__ get a chance to satisfy the access, and applyAttributeAccessOverride
 * simulates the call to whichever the usage calls for.
 *
 * Three transformations then apply to whatever was found, per subtype:
 *
 *   - A DESCRIPTOR (something with __get__/__set__/__delete__) has its access
 *     method called, with a synthesized argument list. The result of that call
 *     is the type of the access. Properties are descriptors with extra
 *     specialization work: they are generic in practice but not declared that
 *     way, so their synthesized __get__ is specialized against the class that
 *     declared fget rather than against the property class.
 *   - A FUNCTION is bound -- see bindFunctionToClassOrObject.
 *   - Anything else passes through.
 *
 * `set` and `del` do more work than `get`. A write has to be checked against
 * ClassVar (writing a class variable through an instance), Final (reassigning
 * something declared Final, allowed inside __init__), read-only members, frozen
 * dataclasses, and finally assignability of the value to the member's type. It
 * also computes narrowedTypeForSet, the type the target narrows to afterwards --
 * except through a descriptor, where the declared setter type is what the caller
 * sees.
 *
 * One subtlety in the `set` path: within the class body itself, only the DECLARED
 * type is used, because evaluating the inferred type would re-enter the
 * evaluation that is asking the question.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// MemberAccessTypeResult corresponds to the interface of the same name.
type MemberAccessTypeResult struct {
	Type                        Type
	IsDescriptorApplied         bool
	IsAsymmetricAccessor        bool
	MemberAccessDeprecationInfo *MemberAccessDeprecationInfo
	TypeErrors                  bool
}

// getTypeOfClassMemberName corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfClassMemberName(
	errorNode parser.ExpressionNode,
	classType *ClassType,
	memberName string,
	usage *EvaluatorUsage,
	diag *common.DiagnosticAddendum,
	flags MemberAccessFlags,
	selfType Type,
	recursionCount int,
) *ClassMemberLookup {
	isAccessedThroughObject := classType.Base().IsInstance()

	// The original's comment: always look for a member with a declared type first.
	memberInfo := LookUpClassMember(classType, memberName, flags|MemberAccessFlagsDeclaredTypesOnly, nil)

	// The original's comment: if we couldn't find a symbol with a declared type,
	// use a symbol with an inferred type.
	if memberInfo == nil {
		memberInfo = LookUpClassMember(classType, memberName, flags, nil)
	}

	if memberInfo == nil {
		// The original's comment: no attribute of that name was found. If this is a
		// member access through an object, see if there's an attribute access
		// override method ("__getattr__", etc.).
		if (flags&MemberAccessFlagsSkipAttributeAccessOverride) == 0 && errorNode != nil {
			generalAttrType := e.applyAttributeAccessOverride(errorNode, classType, usage, memberName, selfType)
			if generalAttrType != nil {
				return &ClassMemberLookup{
					Type:                 generalAttrType.Type,
					IsAsymmetricAccessor: generalAttrType.IsAsymmetricAccessor,
				}
			}
		}

		// The original's comment: report that the member could not be accessed.
		if diag != nil {
			diag.AddMessage(localization.LocAddendum.MemberUnknown().Format(memberName))
		}
		return nil
	}

	var resolvedType Type
	isTypeIncomplete := false
	var narrowedTypeForSet Type

	if memberInfo.Symbol.IsInitVar() {
		if diag != nil {
			diag.AddMessage(localization.LocAddendum.MemberIsInitVar().Format(memberName))
		}
		return nil
	}

	if usage.Method != "get" && errorNode != nil {
		resolvedType, flags = e.declaredTypeForMemberWrite(
			errorNode, classType, memberInfo, memberName, usage, flags, selfType, isAccessedThroughObject)
	}

	if resolvedType == nil {
		var selfClass Type

		if selfType != nil {
			selfClass = ConvertToInstantiable(selfType, false)
		} else {
			// The original's comment: skip this for __new__ methods because they are
			// not bound to the class but rather assume the type of the cls argument.
			if memberName != "__new__" {
				selfClass = classType
			}
		}

		typeResult := e.getTypeOfMemberInternal(errorNode, memberInfo, selfClass, flags)

		if typeResult != nil {
			resolvedType = typeResult.Type
			if typeResult.IsIncomplete {
				isTypeIncomplete = true
			}
		}
		if resolvedType == nil {
			resolvedType = UnknownTypeCreate(false)
		}
	}

	// The original's comment: don't include variables within typed dict classes.
	if IsClass(memberInfo.ClassType) && ClassTypeIsTypedDictClass(memberInfo.ClassType.(*ClassType)) {
		typedDecls := memberInfo.Symbol.GetTypedDeclarations()
		if len(typedDecls) > 0 {
			if _, isVar := typedDecls[0].(*VariableDeclaration); isVar {
				if diag != nil {
					diag.AddMessage(localization.LocAddendum.MemberUnknown().Format(memberName))
				}
				return nil
			}
		}
	}

	if usage.Method == "get" {
		// The original's comment: mark the member accessed if it's not coming from
		// a parent class.
		if errorNode != nil && IsInstantiableClass(memberInfo.ClassType) {
			comparisonClass := classType
			if isAccessedThroughObject {
				comparisonClass = ClassTypeCloneAsInstantiable(classType, false)
			}
			if ClassTypeIsSameGenericClass(memberInfo.ClassType.(*ClassType), comparisonClass, 0) {
				e.setSymbolAccessed(GetFileInfo(errorNode), memberInfo.Symbol, errorNode)
			}
		}

		// The original's comment: special-case `__init_subclass` and
		// `__class_getitem__` because these are always treated as class methods even
		// if they're not decorated as such.
		if memberName == "__init_subclass__" || memberName == "__class_getitem__" {
			if fn, ok := resolvedType.(*FunctionType); ok && !FunctionTypeIsClassMethod(fn) {
				resolvedType = FunctionTypeCloneWithNewFlags(fn,
					fn.Shared.Flags|FunctionTypeFlagsClassMethod)
			}
		}
	}

	// The original's comment: if the member is a descriptor object, apply the
	// descriptor protocol now. If the member is an instance or class method, bind
	// the method.
	isDescriptorError := false
	isAsymmetricAccessor := false
	isDescriptorApplied := false
	var memberAccessDeprecationInfo *MemberAccessDeprecationInfo

	resolvedType = MapSubtypes(resolvedType, func(subtype Type) Type {
		concreteSubtype := e.MakeTopLevelTypeVarsConcrete(subtype, false)
		isClassMember := memberInfo == nil || (memberInfo.IsClassMember && !memberInfo.IsSlotsMember)
		var resultType Type

		if IsClass(concreteSubtype) && isClassMember && errorNode != nil {
			descResult := e.applyDescriptorAccessMethod(subtype, concreteSubtype.(*ClassType), memberInfo,
				classType, selfType, flags, errorNode, memberName, usage, diag)

			if descResult.IsAsymmetricAccessor {
				isAsymmetricAccessor = true
			}

			if descResult.MemberAccessDeprecationInfo != nil {
				memberAccessDeprecationInfo = descResult.MemberAccessDeprecationInfo
			}

			if descResult.TypeErrors {
				isDescriptorError = true
			}

			if descResult.IsDescriptorApplied {
				isDescriptorApplied = true
			}

			resultType = descResult.Type
		} else if IsFunctionOrOverloaded(concreteSubtype) && concreteSubtype.Base().IsInstance() {
			typeResult := e.bindMethodForMemberAccess(subtype, concreteSubtype, memberInfo,
				classType, selfType, flags, memberName, usage, diag, recursionCount)

			resultType = typeResult.Type
			if typeResult.TypeErrors {
				isDescriptorError = true
			}
		} else {
			resultType = subtype
		}

		// The original's comment: if this is a "set" or "delete" operation, we have
		// a bit more work to do.
		if usage.Method == "get" {
			return resultType
		}

		if e.reportWriteToProtectedMember(memberInfo, classType, memberName, flags,
			errorNode, isDescriptorApplied, diag) {
			isDescriptorError = true
		}

		return resultType
	}, &MapSubtypesOptions{RetainTypeAlias: true})

	if !isDescriptorError && usage.Method == "set" && usage.SetType != nil {
		if errorNode != nil && memberInfo.Symbol.HasTypedDeclarations() {
			// The original's comment: this is an assignment to a member with a
			// declared type. Apply narrowing logic based on the assigned type. Skip
			// this for descriptor-based accesses.
			if isDescriptorApplied {
				narrowedTypeForSet = usage.SetType.Type
			} else {
				narrowedTypeForSet = e.narrowTypeBasedOnAssignment(resolvedType, usage.SetType).Type
			}
		}

		// The original's comment: verify that the assigned type is compatible.
		if !e.AssignType(resolvedType, usage.SetType.Type, createAddendumOrNil(diag),
			nil, AssignTypeFlagsDefault, 0) {
			if !usage.SetType.IsIncomplete && diag != nil {
				diag.AddMessage(localization.LocAddendum.MemberAssignment().Format(
					e.PrintType(usage.SetType.Type, nil),
					memberName,
					PrintObjectTypeForClass(classType, e.evaluatorOptions.PrintTypeFlags, e.getEffectiveReturnType),
				))
			}

			// The original's comment: do not narrow the type in this case. Assume
			// the declared type.
			narrowedTypeForSet = resolvedType
			isDescriptorError = true
		}

		if IsInstantiableClass(memberInfo.ClassType) &&
			ClassTypeIsDataClassFrozen(memberInfo.ClassType.(*ClassType)) && isAccessedThroughObject {
			if diag != nil {
				diag.AddMessage(localization.LocAddendum.DataClassFrozen().Format(
					e.PrintType(ClassTypeCloneAsInstance(memberInfo.ClassType.(*ClassType), false), nil)))
			}

			isDescriptorError = true
		}
	}

	return &ClassMemberLookup{
		Symbol:                      memberInfo.Symbol,
		Type:                        resolvedType,
		IsTypeIncomplete:            isTypeIncomplete,
		IsDescriptorError:           isDescriptorError,
		IsClassMember:               !memberInfo.IsInstanceMember,
		IsClassVar:                  memberInfo.IsClassVar,
		ClassType:                   memberInfo.ClassType,
		IsAsymmetricAccessor:        isAsymmetricAccessor,
		NarrowedTypeForSet:          narrowedTypeForSet,
		MemberAccessDeprecationInfo: memberAccessDeprecationInfo,
	}
}

// declaredTypeForMemberWrite is the original's `usage.method !== 'get'` block.
//
// Its comment: if the usage indicates a 'set' or 'delete' and the access is
// within the class definition itself, use only the declared type to avoid
// circular type evaluation.
//
// The returned flags carry the original's in-place mutation of `flags` in the
// descriptor case.
func (e *typeEvaluator) declaredTypeForMemberWrite(
	errorNode parser.ExpressionNode,
	classType *ClassType,
	memberInfo *ClassMember,
	memberName string,
	usage *EvaluatorUsage,
	flags MemberAccessFlags,
	selfType Type,
	isAccessedThroughObject bool,
) (Type, MemberAccessFlags) {
	containingClass := GetEnclosingClass(errorNode, false)
	if containingClass == nil {
		return nil, flags
	}

	classResult := e.GetTypeOfClass(containingClass)
	if classResult == nil || classResult.ClassType == nil {
		return nil, flags
	}
	containingClassType := classResult.ClassType

	comparisonClass := containingClassType
	if isAccessedThroughObject {
		comparisonClass = ClassTypeCloneAsInstance(containingClassType, false)
	}
	if !ClassTypeIsSameGenericClass(comparisonClass, classType, 0) {
		return nil, flags
	}

	var resolvedType Type
	if declInfo := e.GetDeclaredTypeOfSymbol(memberInfo.Symbol); declInfo != nil {
		resolvedType = declInfo.Type
	}
	if resolvedType != nil && IsInstantiableClass(memberInfo.ClassType) {
		resolvedType = PartiallySpecializeType(resolvedType, memberInfo.ClassType.(*ClassType), nil, selfType)
	}

	// The original's comment: if we're setting a class variable via a write
	// through an object, this is normally considered a type violation. But it is
	// allowed if the class variable is a descriptor object. In this case, we will
	// clear the flag that causes an error to be generated.
	if usage.Method == "set" &&
		IsEffectivelyClassVar(memberInfo.Symbol, ClassTypeIsDataClass(containingClassType)) &&
		isAccessedThroughObject {
		// The original's `selfType ?? memberName === '__new__' ? undefined :
		// classType` parses as `(selfType ?? memberName === '__new__') ? undefined
		// : classType`, so selfClass is undefined whenever selfType is set. See
		// UPSTREAM-BUGS.md.
		var selfClass Type
		if selfType == nil && memberName != "__new__" {
			selfClass = classType
		}

		typeResult := e.getTypeOfMemberInternal(errorNode, memberInfo, selfClass, flags)

		if typeResult != nil && IsDescriptorInstance(typeResult.Type, true) {
			resolvedType = typeResult.Type
			// The original assigns rather than masks off, leaving only this one
			// bit set. Faithfully reproduced.
			flags &= MemberAccessFlagsDisallowClassVarWrites
		}
	}

	if resolvedType == nil {
		resolvedType = UnknownTypeCreate(false)
	}

	return resolvedType, flags
}

// reportWriteToProtectedMember is the original's tail of the mapSubtypes
// callback, covering the three ways a write can be refused independently of
// assignability. It reports whether a diagnostic was added.
func (e *typeEvaluator) reportWriteToProtectedMember(
	memberInfo *ClassMember,
	classType *ClassType,
	memberName string,
	flags MemberAccessFlags,
	errorNode parser.ExpressionNode,
	isDescriptorApplied bool,
	diag *common.DiagnosticAddendum,
) bool {
	isError := false

	// The original's comment: check for an attempt to overwrite or delete a
	// ClassVar member from an instance.
	if !isDescriptorApplied && memberInfo != nil &&
		IsEffectivelyClassVar(memberInfo.Symbol, ClassTypeIsDataClass(classType)) &&
		(flags&MemberAccessFlagsDisallowClassVarWrites) != 0 {
		if diag != nil {
			diag.AddMessage(localization.LocAddendum.MemberSetClassVar().Format(memberName))
		}
		isError = true
	}

	// The original's comment: check for an attempt to overwrite or delete a final
	// member variable.
	var finalVarTypeDecl Declaration
	if memberInfo != nil {
		for _, decl := range memberInfo.Symbol.GetDeclarations() {
			if e.IsFinalVariableDeclaration(decl) {
				finalVarTypeDecl = decl
				break
			}
		}
	}

	if finalVarTypeDecl != nil && errorNode != nil &&
		!IsNodeContainedWithin(errorNode, finalVarTypeDecl.DeclBase().Node) {
		// The original's comment: if a Final instance variable is declared in the
		// class body but is being assigned within an __init__ method, it's allowed.
		enclosingFunctionNode := GetEnclosingFunction(errorNode)
		inferredTypeSourceSet := false
		if varDecl, ok := finalVarTypeDecl.(*VariableDeclaration); ok {
			inferredTypeSourceSet = varDecl.InferredTypeSource != nil
		}

		if enclosingFunctionNode == nil ||
			enclosingFunctionNode.D.Name.D.Value != "__init__" ||
			inferredTypeSourceSet ||
			IsInstantiableClass(classType) {
			if diag != nil {
				diag.AddMessage(localization.LocMessage.FinalReassigned().Format(memberName))
			}
			isError = true
		}
	}

	// The original's comment: check for an attempt to overwrite or delete an
	// instance variable that is read-only (e.g. in a named tuple).
	if memberInfo != nil && memberInfo.IsInstanceMember &&
		IsClass(memberInfo.ClassType) && memberInfo.IsReadOnly {
		if diag != nil {
			diag.AddMessage(localization.LocAddendum.ReadOnlyAttribute().Format(memberName))
		}
		isError = true
	}

	return isError
}
