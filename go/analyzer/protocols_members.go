/*
 * protocols_members.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/protocols.ts (pyright 1.1.412):
 * assignToProtocolInternal.
 *
 * The member-by-member walk that decides whether a class satisfies a protocol.
 * See protocols_assign.go for the caching and recursion handling around it.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// protocolWalk holds the state the original keeps in closures over the body of
// assignToProtocolInternal.
type protocolWalk struct {
	evaluator      TypeEvaluator
	destType       *ClassType
	srcType        Type
	diag           *common.DiagnosticAddendum
	constraints    *ConstraintTracker
	flags          AssignTypeFlags
	recursionCount int

	sourceIsClassObject bool
	protocolConstraints *ConstraintTracker
	selfSolution        *ConstraintSolution
	selfType            Type
	assignTypeFlags     AssignTypeFlags

	typesAreConsistent bool
	checkedSymbolSet   map[string]bool
}

// assignToProtocolInternal corresponds to the function of the same name.
func assignToProtocolInternal(
	evaluator TypeEvaluator,
	destType *ClassType,
	srcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	if (flags & AssignTypeFlagsInvariant) != 0 {
		return IsTypeSame(destType, srcType, TypeSameOptions{}, 0)
	}

	evaluator.InferVarianceForClass(destType)

	w := &protocolWalk{
		evaluator:           evaluator,
		destType:            destType,
		srcType:             srcType,
		diag:                diag,
		constraints:         constraints,
		flags:               flags,
		recursionCount:      recursionCount,
		protocolConstraints: createProtocolConstraints(evaluator, destType, constraints),
		selfSolution:        NewConstraintSolution(nil),
		typesAreConsistent:  true,
		checkedSymbolSet:    map[string]bool{},
	}

	w.sourceIsClassObject = IsClass(srcType) && srcType.Base().IsInstantiable()
	w.resolveSelfType()

	// The original's comment: if the source is a TypedDict, use the _TypedDict
	// placeholder class instead. We don't want to use the TypedDict members for
	// protocol comparison.
	if srcClass, ok := w.srcType.(*ClassType); ok && IsClass(w.srcType) &&
		ClassTypeIsTypedDictClass(srcClass) {
		if typedDictClassType := evaluator.GetTypedDictClassType(); typedDictClassType != nil &&
			IsInstantiableClass(typedDictClassType) {
			w.srcType = typedDictClassType
		}
	}

	w.assignTypeFlags = flags & (AssignTypeFlagsOverloadOverlap | AssignTypeFlagsPartialOverloadOverlap)
	if ContainsLiteralType(w.srcType, true) {
		w.assignTypeFlags |= AssignTypeFlagsRetainLiteralsForTypeVar
	}

	for _, mroClass := range destType.Shared.Mro {
		mroClassType, ok := mroClass.(*ClassType)
		if !ok || !IsInstantiableClass(mroClass) || !ClassTypeIsProtocolClass(mroClassType) {
			continue
		}

		// The original's comment: if we've already determined that the types are not
		// consistent and the caller hasn't requested detailed diagnostic output, we
		// can shortcut the remainder.
		if !w.typesAreConsistent && diag == nil {
			continue
		}

		ClassTypeGetSymbolTable(mroClassType).ForEach(func(destSymbol *Symbol, name string) {
			if !w.typesAreConsistent && diag == nil {
				return
			}
			w.checkOneMember(mroClassType, destSymbol, name)
		})
	}

	// The original's comment: if the dest protocol has type parameters, make sure
	// the source type arguments match.
	if w.typesAreConsistent && len(destType.Shared.TypeParams) > 0 {
		w.matchProtocolTypeArgs()
	}

	return w.typesAreConsistent
}

// resolveSelfType is the original's selfType block.
//
// Its comment: if the srcType is conditioned on "self", use "Self" as the
// selfType. Otherwise use the class type for selfType.
func (w *protocolWalk) resolveSelfType() {
	srcClass, ok := w.srcType.(*ClassType)
	if !ok || !IsClass(w.srcType) {
		return
	}

	var synthCond *TypeCondition
	if props := w.srcType.Base().Props; props != nil {
		for i := range props.Condition {
			if TypeVarTypeIsSelf(props.Condition[i].TypeVar) {
				synthCond = &props.Condition[i]
				break
			}
		}
	}

	if synthCond != nil {
		selfType := SynthesizeTypeVarForSelfCls(CloneForCondition(srcClass, nil), false)

		if TypeVarTypeIsBound(synthCond.TypeVar) {
			selfType = TypeVarTypeCloneAsBound(selfType)
		}
		w.selfType = selfType
	} else {
		w.selfType = srcClass
	}

	AddSolutionForSelfType(w.selfSolution, w.destType, w.selfType)
}

// checkOneMember is the body of the original's per-symbol loop.
func (w *protocolWalk) checkOneMember(mroClass *ClassType, destSymbol *Symbol, name string) {
	if !destSymbol.IsClassMember() || destSymbol.IsIgnoredForProtocolMatch() || w.checkedSymbolSet[name] {
		return
	}

	// The original's comment: special-case the `__class_getitem__` for normal
	// protocol comparison. This is a convention agreed upon by typeshed
	// maintainers.
	if !w.sourceIsClassObject && name == "__class_getitem__" {
		return
	}

	// The original's comment: special-case the `__slots__` entry for all protocol
	// comparisons. This is a convention agreed upon by typeshed maintainers.
	if name == "__slots__" {
		return
	}

	// The original's comment: note that we've already checked this symbol. It
	// doesn't need to be checked again even if it is declared by a subclass.
	w.checkedSymbolSet[name] = true

	declInfo := w.evaluator.GetDeclaredTypeOfSymbol(destSymbol)
	if declInfo == nil || declInfo.Type == nil {
		return
	}
	destMemberType := declInfo.Type

	member := w.resolveSourceMember(mroClass, name, &destMemberType)
	if member == nil {
		return
	}

	// The original's comment: replace any "Self" TypeVar within the dest with the
	// source type.
	destMemberType = ApplySolvedTypeVars(destMemberType, w.selfSolution, nil)

	// The original's comment: if the dest is a method, bind it.
	if !destSymbol.IsInstanceMember() && IsFunctionOrOverloaded(destMemberType) {
		// The original's comment: functions are considered read-only.
		member.IsDestReadOnly = true

		bound := w.bindDestMember(destMemberType, member)
		if bound == nil {
			w.typesAreConsistent = false
			return
		}
		destMemberType = bound
	}

	subDiag := createAddendumOrNil(w.diag)

	if symbolHasFinalVariableDecl(destSymbol) {
		member.IsDestReadOnly = true
	}
	if symbolHasFinalVariableDecl(member.SrcSymbol) {
		member.IsSrcReadOnly = true
	}

	if destClass, ok := destMemberType.(*ClassType); ok && IsClassInstance(destMemberType) &&
		ClassTypeIsPropertyClass(destClass) {
		w.compareProperty(mroClass, name, destClass, member, subDiag)
	} else {
		w.compareOrdinaryMember(name, destSymbol, destMemberType, member, subDiag)
	}

	if !member.IsDestReadOnly && member.IsSrcReadOnly {
		addMessageOrNil(subDiag, localization.LocAddendum.MemberIsNotReadOnlyInProtocol().Format(name))
		w.typesAreConsistent = false
	}

	w.compareClassVarAgreement(name, destSymbol, member, subDiag)
	w.compareVariableWritability(name, destSymbol, member, subDiag)
}

// protocolMember carries what the source side contributed for one name.
type protocolMember struct {
	SrcSymbol             *Symbol
	SrcMemberInfo         *ClassMember
	SrcMemberType         Type
	IsMemberFromMetaclass bool
	IsSrcReadOnly         bool
	IsDestReadOnly        bool
}

// resolveSourceMember is the original's source-lookup block. It returns nil when
// the member is missing, having already reported it.
func (w *protocolWalk) resolveSourceMember(
	mroClass *ClassType, name string, destMemberType *Type,
) *protocolMember {
	member := &protocolMember{}

	srcClass, srcIsClass := w.srcType.(*ClassType)
	if !srcIsClass || !IsClass(w.srcType) {
		srcModule := w.srcType.(*ModuleType)
		srcSymbol, found := srcModule.Priv.Fields.Get(name)
		if !found {
			addMessageOrNil(w.diag, localization.LocAddendum.ProtocolMemberMissing().Format(name))
			w.typesAreConsistent = false
			return nil
		}

		member.SrcSymbol = srcSymbol
		member.SrcMemberType = w.evaluator.GetEffectiveTypeOfSymbol(srcSymbol)
		return member
	}

	// The original's comment: look in the metaclass first if we're treating the
	// source as an instantiable class.
	if w.sourceIsClassObject && srcClass.Shared.EffectiveMetaclass != nil &&
		IsInstantiableClass(srcClass.Shared.EffectiveMetaclass) {
		member.SrcMemberInfo = LookUpClassMember(
			srcClass.Shared.EffectiveMetaclass.(*ClassType), name, MemberAccessFlagsDefault, nil)
		if member.SrcMemberInfo != nil {
			member.IsMemberFromMetaclass = true
		}
	}

	if member.SrcMemberInfo == nil {
		member.SrcMemberInfo = LookUpClassMember(srcClass, name, MemberAccessFlagsDefault, nil)
	}

	if member.SrcMemberInfo == nil {
		addMessageOrNil(w.diag, localization.LocAddendum.ProtocolMemberMissing().Format(name))
		w.typesAreConsistent = false
		return nil
	}

	member.SrcSymbol = member.SrcMemberInfo.Symbol

	// The original's comment: partially specialize the type of the symbol based on
	// the MRO class. We can skip this if it's the dest class because it is already
	// specialized.
	if !ClassTypeIsSameGenericClass(mroClass, w.destType, 0) {
		*destMemberType = PartiallySpecializeType(*destMemberType, mroClass,
			w.evaluator.GetTypeClassType(), w.selfType)
	}

	if IsInstantiableClass(member.SrcMemberInfo.ClassType) {
		symbolType := w.evaluator.GetEffectiveTypeOfSymbol(member.SrcMemberInfo.Symbol)

		// The original's comment: if this is a function, infer its return type prior
		// to specializing it.
		if IsFunction(symbolType) {
			w.evaluator.InferReturnTypeIfNecessary(symbolType)
		}

		member.SrcMemberType = PartiallySpecializeType(symbolType,
			member.SrcMemberInfo.ClassType.(*ClassType), w.evaluator.GetTypeClassType(), w.selfType)
	} else {
		member.SrcMemberType = UnknownTypeCreate(false)
	}

	// The original's comment: if the source is a method, bind it.
	if IsFunctionOrOverloaded(member.SrcMemberType) {
		if !w.bindSourceMethod(srcClass, name, member) {
			w.typesAreConsistent = false
			return nil
		}
	}

	if member.SrcMemberInfo.IsReadOnly {
		member.IsSrcReadOnly = true
	}

	return member
}

// bindSourceMethod is the original's source-binding block. It reports success.
func (w *protocolWalk) bindSourceMethod(srcClass *ClassType, name string, member *protocolMember) bool {
	if !member.IsMemberFromMetaclass && !IsInstantiableClass(member.SrcMemberInfo.ClassType) {
		return true
	}

	isInstanceMember := !member.SrcMemberInfo.Symbol.IsClassMember()

	// The original's comment: special-case dataclasses whose entries act like
	// instance members.
	if ClassTypeIsDataClass(srcClass) {
		for _, entry := range ClassTypeGetDataClassEntries(srcClass) {
			if entry.Name == name {
				isInstanceMember = true
				break
			}
		}
	}

	if member.IsMemberFromMetaclass {
		isInstanceMember = false
	}

	// The original's comment: if this is a callable stored in an instance member,
	// skip binding.
	if isInstanceMember {
		return true
	}

	baseType := ClassTypeCloneAsInstance(srcClass, false)
	if w.sourceIsClassObject && !member.IsMemberFromMetaclass {
		baseType = srcClass
	}

	var memberClass *ClassType
	if !member.IsMemberFromMetaclass {
		memberClass, _ = member.SrcMemberInfo.ClassType.(*ClassType)
	}

	firstParamType := w.selfType
	if member.IsMemberFromMetaclass {
		firstParamType = srcClass
	}

	boundSrcFunction := w.evaluator.BindFunctionToClassOrObject(baseType, member.SrcMemberType,
		memberClass, false, firstParamType, createAddendumOrNil(w.diag), w.recursionCount)

	if boundSrcFunction == nil {
		return false
	}

	member.SrcMemberType = boundSrcFunction
	return true
}

// bindDestMember is the original's dest-binding block.
func (w *protocolWalk) bindDestMember(destMemberType Type, member *protocolMember) Type {
	var boundDeclaredType Type

	if srcClass, ok := w.srcType.(*ClassType); ok && IsClass(w.srcType) {
		assert(member.SrcMemberInfo != nil, "expected source member info")

		if member.IsMemberFromMetaclass || IsInstantiableClass(member.SrcMemberInfo.ClassType) {
			var memberClass *ClassType
			if !member.IsMemberFromMetaclass {
				memberClass, _ = member.SrcMemberInfo.ClassType.(*ClassType)
			}

			firstParamType := w.selfType
			if member.IsMemberFromMetaclass {
				firstParamType = srcClass
			}

			boundDeclaredType = w.evaluator.BindFunctionToClassOrObject(
				ClassTypeCloneAsInstance(srcClass, false), destMemberType, memberClass,
				false, firstParamType, w.diag, w.recursionCount)
		}
	} else {
		boundDeclaredType = w.evaluator.BindFunctionToClassOrObject(
			ClassTypeCloneAsInstance(w.destType, false), destMemberType, w.destType,
			false, nil, w.diag, w.recursionCount)
	}

	if boundDeclaredType == nil {
		return nil
	}

	return MakeFunctionTypeVarsBound(boundDeclaredType)
}

// compareProperty is the original's property block.
//
// Its comment: properties require special processing.
func (w *protocolWalk) compareProperty(
	mroClass *ClassType, name string, destClass *ClassType,
	member *protocolMember, subDiag *common.DiagnosticAddendum,
) {
	srcClass, srcIsClass := member.SrcMemberType.(*ClassType)

	if srcIsClass && IsClassInstance(member.SrcMemberType) &&
		ClassTypeIsPropertyClass(srcClass) && !w.sourceIsClassObject {
		if !AssignProperty(w.evaluator,
			ClassTypeCloneAsInstantiable(destClass, false),
			ClassTypeCloneAsInstantiable(srcClass, false),
			mroClass, w.srcType, createAddendumOrNil(subDiag),
			w.protocolConstraints, w.selfSolution, w.recursionCount) {
			addMessageOrNil(subDiag, localization.LocAddendum.MemberTypeMismatch().Format(name))
			w.typesAreConsistent = false
		}
		return
	}

	// The original's comment: extract the property type from the property class.
	getterType := w.evaluator.GetGetterTypeFromProperty(destClass)

	if getterType != nil {
		getterType = PartiallySpecializeType(getterType, mroClass, w.evaluator.GetTypeClassType(), nil)
	}

	if getterType == nil || !w.evaluator.AssignType(getterType, member.SrcMemberType,
		createAddendumOrNil(subDiag), w.protocolConstraints, w.assignTypeFlags, w.recursionCount) {
		addMessageOrNil(subDiag, localization.LocAddendum.MemberTypeMismatch().Format(name))
		w.typesAreConsistent = false
	}

	// A property with neither a setter nor a deleter is read-only on the protocol
	// side.
	if LookUpClassMember(destClass, "__set__", MemberAccessFlagsSkipInstanceMembers, nil) == nil &&
		LookUpClassMember(destClass, "__delete__", MemberAccessFlagsSkipInstanceMembers, nil) == nil {
		member.IsDestReadOnly = true
	}

	if member.IsSrcReadOnly && !member.IsDestReadOnly {
		// The original's comment: the source attribute is read-only. Make sure the
		// setter is not defined in the dest property.
		addMessageOrNil(subDiag, localization.LocAddendum.MemberIsWritableInProtocol().Format(name))
		w.typesAreConsistent = false
	}
}

// compareOrdinaryMember is the original's non-property block.
//
// Its comment: class and instance variables that are mutable need to enforce
// invariance.
func (w *protocolWalk) compareOrdinaryMember(
	name string, destSymbol *Symbol, destMemberType Type,
	member *protocolMember, subDiag *common.DiagnosticAddendum,
) {
	isInvariant := false
	decls := destSymbol.GetDeclarations()
	if len(decls) > 0 {
		if varDecl, ok := decls[0].(*VariableDeclaration); ok && !varDecl.IsFinal {
			isInvariant = true
		}
	}

	// The original's comment: temporarily add the TypeVar scope ID for this method
	// to handle method-scoped TypeVars.
	protocolConstraintsClone := w.protocolConstraints.Clone()

	assignFlags := w.assignTypeFlags
	if isInvariant {
		assignFlags |= AssignTypeFlagsInvariant
	}

	if !w.evaluator.AssignType(destMemberType, member.SrcMemberType,
		createAddendumOrNil(subDiag), protocolConstraintsClone, assignFlags, w.recursionCount) {
		if subDiag != nil {
			if isInvariant {
				subDiag.AddMessage(localization.LocAddendum.MemberIsInvariant().Format(name))
			}
			subDiag.AddMessage(localization.LocAddendum.MemberTypeMismatch().Format(name))
		}
		w.typesAreConsistent = false
		return
	}

	w.protocolConstraints.CopyFromClone(protocolConstraintsClone)
}

// compareClassVarAgreement is the original's ClassVar block. The rule inverts
// when the source is a class object rather than an instance.
func (w *protocolWalk) compareClassVarAgreement(
	name string, destSymbol *Symbol, member *protocolMember, subDiag *common.DiagnosticAddendum,
) {
	isDestClassVar := IsEffectivelyClassVar(destSymbol, false)

	srcIsDataclass := false
	if srcClass, ok := w.srcType.(*ClassType); ok && IsClass(w.srcType) {
		srcIsDataclass = ClassTypeIsDataClass(srcClass)
	}
	isSrcClassVar := IsEffectivelyClassVar(member.SrcSymbol, srcIsDataclass)

	isSrcVariable := false
	for _, decl := range member.SrcSymbol.GetDeclarations() {
		if _, ok := decl.(*VariableDeclaration); ok {
			isSrcVariable = true
			break
		}
	}

	if w.sourceIsClassObject {
		// The original's comment: if the source is not marked as a ClassVar or the
		// dest (the protocol) is, the types are not consistent given that the source
		// is a class object.
		if isDestClassVar {
			addMessageOrNil(subDiag, localization.LocAddendum.MemberIsClassVarInProtocol().Format(name))
			w.typesAreConsistent = false
		} else if isSrcVariable && !isSrcClassVar && !member.IsMemberFromMetaclass {
			addMessageOrNil(subDiag, localization.LocAddendum.MemberIsNotClassVarInClass().Format(name))
			w.typesAreConsistent = false
		}
		return
	}

	// The original's comment: if the source is marked as a ClassVar but the dest
	// (the protocol) is not, or vice versa, the types are not consistent.
	if isDestClassVar != isSrcClassVar {
		if isDestClassVar {
			addMessageOrNil(subDiag, localization.LocAddendum.MemberIsClassVarInProtocol().Format(name))
		} else {
			addMessageOrNil(subDiag, localization.LocAddendum.MemberIsNotClassVarInProtocol().Format(name))
		}
		w.typesAreConsistent = false
	}
}

// compareVariableWritability is the original's final block, which compares the
// constant and Final markers on the two declarations.
func (w *protocolWalk) compareVariableWritability(
	name string, destSymbol *Symbol, member *protocolMember, subDiag *common.DiagnosticAddendum,
) {
	destPrimaryDecl, destIsVar := GetLastTypedDeclarationForSymbol(destSymbol).(*VariableDeclaration)
	srcPrimaryDecl, srcIsVar := GetLastTypedDeclarationForSymbol(member.SrcSymbol).(*VariableDeclaration)

	if !destIsVar || !srcIsVar {
		return
	}

	isDestReadOnly := destPrimaryDecl.IsConstant || destPrimaryDecl.IsFinal
	isSrcReadOnly := srcPrimaryDecl.IsConstant
	if member.SrcMemberInfo != nil && IsClass(member.SrcMemberInfo.ClassType) &&
		member.SrcMemberInfo.IsReadOnly {
		isSrcReadOnly = true
	}

	if !isDestReadOnly && isSrcReadOnly {
		addMessageOrNil(subDiag, localization.LocAddendum.MemberIsWritableInProtocol().Format(name))
		w.typesAreConsistent = false
	}
}

// matchProtocolTypeArgs is the original's tail.
//
// Its comment: create a specialized version of the protocol defined by the dest
// and make sure the resulting type args can be assigned.
func (w *protocolWalk) matchProtocolTypeArgs() {
	genericProtocolType := ClassTypeSpecialize(w.destType, nil, nil, false, nil, nil)
	specialized := w.evaluator.SolveAndApplyConstraints(genericProtocolType, w.protocolConstraints, nil, nil)
	specializedProtocolType, ok := specialized.(*ClassType)
	if !ok {
		return
	}

	if w.destType.Priv.TypeArgs != nil {
		if !w.evaluator.AssignTypeArgs(w.destType, specializedProtocolType, w.diag,
			w.constraints, w.flags, w.recursionCount) {
			w.typesAreConsistent = false
		}
		return
	}

	if w.constraints == nil {
		return
	}

	for _, typeParam := range w.destType.Shared.TypeParams {
		if typeArgEntry := w.protocolConstraints.GetMainConstraintSet().GetTypeVar(typeParam); typeArgEntry != nil {
			w.constraints.CopyBounds(typeArgEntry)
		}
	}
}

// symbolHasFinalVariableDecl is the original's
// `getTypedDeclarations().some((decl) => decl.type === Variable && !!decl.isFinal)`.
func symbolHasFinalVariableDecl(symbol *Symbol) bool {
	for _, decl := range symbol.GetTypedDeclarations() {
		if varDecl, ok := decl.(*VariableDeclaration); ok && varDecl.IsFinal {
			return true
		}
	}
	return false
}

// addMessageOrNil is the original's `diag?.addMessage(...)`.
func addMessageOrNil(diag *common.DiagnosticAddendum, message string) {
	if diag != nil {
		diag.AddMessage(message)
	}
}
