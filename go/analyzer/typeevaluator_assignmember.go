/*
 * typeevaluator_assignmember.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * assignTypeToMemberAccessNode, assignTypeToMemberVariable.
 *
 * Writing to `obj.name`. Two things happen, in this order: if the write is
 * `self.x = ...` or `cls.x = ...` inside the class's own body, it DECLARES an
 * attribute as well as assigning to one, and that declaration is recorded first;
 * then the ordinary set-access runs, which is where descriptors, properties and
 * assignability get checked.
 *
 * The declaring half is assignTypeToMemberVariable, and it enforces three things
 * that only apply to a write through self:
 *
 *   - __slots__. A class with slots cannot grow attributes, so assigning a name
 *     that is not in the inherited slots is an error -- unless the name is a
 *     class-level descriptor, which slots do not govern. An EMPTY slots list on
 *     a non-final class is exempt, because that is the conventional way to write
 *     a mix-in.
 *   - Constant redefinition, for a name the class already declared as constant.
 *   - A class variable of the same name. Assigning `self.x` where `x` is already
 *     a class variable does not shadow it; the two types are unioned, because
 *     reading `self.x` may find either.
 *
 * A PROTOCOL is stricter still: assigning through self or cls is refused unless
 * the attribute is also declared at class level, since a protocol describes a
 * shape rather than an implementation.
 *
 * The result is cached twice, and not with the same type. The member NAME node
 * gets a version with the enclosing class's type variables made free, so that
 * the inferred type of the attribute is expressed in terms the class's other
 * members can use.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// assignTypeToMemberAccessNode corresponds to the function of the same name.
func (e *typeEvaluator) assignTypeToMemberAccessNode(
	target *parser.MemberAccessNode,
	typeResult *TypeResult,
	srcExpr parser.ExpressionNode,
	expectedTypeDiagAddendum *common.DiagnosticAddendum,
) {
	baseTypeResult := e.GetTypeOfExpression(target.D.LeftExpr, EvalFlagsMemberAccessBaseDefaults, nil)
	baseType := e.MakeTopLevelTypeVarsConcrete(baseTypeResult.Type, false)
	var enclosingClass *ClassType

	// The original's comment: handle member accesses (e.g. self.x or cls.y).
	if target.D.LeftExpr.GetNodeType() == parser.ParseNodeTypeName {
		enclosingClass = e.declareMemberVariable(target, typeResult, srcExpr, baseType)
	}

	setTypeResult := e.getTypeOfMemberAccessWithBaseType(target, baseTypeResult, &EvaluatorUsage{
		Method:              "set",
		SetType:             typeResult,
		SetErrorNode:        srcExpr,
		SetExpectedTypeDiag: expectedTypeDiagAddendum,
	}, EvalFlagsNone)

	if setTypeResult.IsAsymmetricAccessor {
		e.setAsymmetricDescriptorAssignment(target)
	}

	cachedType := setTypeResult.NarrowedTypeForSet
	if cachedType == nil {
		cachedType = typeResult.Type
	}

	resultToCache := &TypeResult{
		Type:                        cachedType,
		IsIncomplete:                typeResult.IsIncomplete,
		MemberAccessDeprecationInfo: setTypeResult.MemberAccessDeprecationInfo,
	}
	noFlags := EvalFlagsNone
	e.writeTypeCache(target, resultToCache, &noFlags, nil, false)

	// The original's comment: if the target is an instance or class variable,
	// update any class-scoped type variables so the inferred type of the variable
	// uses "external" type variables.
	memberResultToCache := resultToCache
	if enclosingClass != nil && enclosingClass.Shared.TypeVarScopeID != "" {
		copied := *resultToCache
		copied.Type = MakeTypeVarsFree(resultToCache.Type,
			[]TypeVarScopeId{enclosingClass.Shared.TypeVarScopeID})
		copied.MemberAccessDeprecationInfo = setTypeResult.MemberAccessDeprecationInfo
		memberResultToCache = &copied
	}
	e.writeTypeCache(target.D.Member, memberResultToCache, &noFlags, nil, false)
}

// declareMemberVariable is the original's `leftExpr is a Name` block. It returns
// the enclosing class when there is one, which the caller needs for the
// type-variable rewrite.
func (e *typeEvaluator) declareMemberVariable(
	target *parser.MemberAccessNode,
	typeResult *TypeResult,
	srcExpr parser.ExpressionNode,
	baseType Type,
) *ClassType {
	// The original's comment: determine whether we're writing to a class or
	// instance member.
	enclosingClassNode := GetEnclosingClass(target, false)
	if enclosingClassNode == nil {
		return nil
	}

	classTypeResults := e.GetTypeOfClass(enclosingClassNode)
	if classTypeResults == nil || !IsInstantiableClass(classTypeResults.ClassType) {
		return nil
	}

	enclosingClass := classTypeResults.ClassType

	if baseClass, ok := baseType.(*ClassType); ok {
		if IsClassInstance(baseType) {
			if ClassTypeIsSameGenericClass(ClassTypeCloneAsInstantiable(baseClass, false),
				classTypeResults.ClassType, 0) {
				e.assignTypeToMemberVariable(target, typeResult, true, srcExpr)
			}
		} else if IsInstantiableClass(baseType) {
			if ClassTypeIsSameGenericClass(baseClass, classTypeResults.ClassType, 0) {
				e.assignTypeToMemberVariable(target, typeResult, false, srcExpr)
			}
		}
	}

	// The original's comment: assignments to instance or class variables through
	// "self" or "cls" is not allowed for protocol classes unless it is also
	// declared within the class.
	if ClassTypeIsProtocolClass(classTypeResults.ClassType) {
		if memberSymbol, found := ClassTypeGetSymbolTable(classTypeResults.ClassType).Get(
			target.D.Member.D.Value); found {
			classLevelDecls := 0
			for _, decl := range memberSymbol.GetDeclarations() {
				if GetEnclosingFunction(decl.DeclBase().Node) == nil {
					classLevelDecls++
				}
			}
			if classLevelDecls == 0 {
				e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.AssignmentInProtocol(), target.D.Member, nil)
			}
		}
	}

	return enclosingClass
}

// assignTypeToMemberVariable corresponds to the function of the same name.
func (e *typeEvaluator) assignTypeToMemberVariable(
	node *parser.MemberAccessNode,
	typeResult *TypeResult,
	isInstanceMember bool,
	srcExprNode parser.ExpressionNode,
) {
	memberName := node.D.Member.D.Value
	fileInfo := GetFileInfo(node)

	classDef := GetEnclosingClass(node, false)
	if classDef == nil {
		return
	}

	classTypeInfo := e.GetTypeOfClass(classDef)
	if classTypeInfo == nil || !IsInstantiableClass(classTypeInfo.ClassType) {
		return
	}

	lookupFlags := MemberAccessFlagsSkipInstanceMembers
	if isInstanceMember {
		lookupFlags = MemberAccessFlagsDefault
	}
	memberInfo := LookUpClassMember(classTypeInfo.ClassType, memberName, lookupFlags, nil)

	memberFields := ClassTypeGetSymbolTable(classTypeInfo.ClassType)
	if memberInfo != nil {
		typeResult = e.checkExistingMemberDeclaration(node, typeResult, isInstanceMember,
			srcExprNode, memberInfo, memberFields, classTypeInfo.ClassType, memberName, fileInfo)
	}

	// The original's comment: look up the member info again, now that we've
	// potentially updated it.
	memberInfo = LookUpClassMember(classTypeInfo.ClassType, memberName,
		MemberAccessFlagsDeclaredTypesOnly, nil)

	if memberInfo == nil && srcExprNode != nil && !typeResult.IsIncomplete {
		e.reportPossibleUnknownAssignment(
			fileInfo.DiagnosticRuleSet.ReportUnknownMemberType,
			DiagnosticRuleReportUnknownMemberType,
			node.D.Member,
			typeResult.Type,
			node,
			true)
	}
}

// checkExistingMemberDeclaration is the original's `if (memberInfo)` block. It
// returns the type result, which the class-variable case widens.
func (e *typeEvaluator) checkExistingMemberDeclaration(
	node *parser.MemberAccessNode,
	typeResult *TypeResult,
	isInstanceMember bool,
	srcExprNode parser.ExpressionNode,
	memberInfo *ClassMember,
	memberFields SymbolTable,
	classType *ClassType,
	memberName string,
	fileInfo *AnalyzerFileInfo,
) *TypeResult {
	// The original's comment: are we accessing an existing member on this class,
	// or is it a member on a parent class?
	var memberClass *ClassType
	if IsInstantiableClass(memberInfo.ClassType) {
		memberClass = memberInfo.ClassType.(*ClassType)
	}
	isThisClass := memberClass != nil && ClassTypeIsSameGenericClass(classType, memberClass, 0)

	// The original's comment: check for an attempt to write to an instance
	// variable that is not defined by __slots__.
	if isThisClass && isInstanceMember && memberClass != nil {
		e.checkSlotsAssignment(node, memberClass, memberName)
	}

	if isThisClass && memberInfo.IsInstanceMember == isInstanceMember {
		e.checkConstantRedefinition(node, memberFields, memberName, srcExprNode)
		return typeResult
	}

	// The original's comment: is the target a property?
	var declaredType Type
	if declInfo := e.GetDeclaredTypeOfSymbol(memberInfo.Symbol); declInfo != nil {
		declaredType = declInfo.Type
	}
	if declaredType == nil || IsProperty(declaredType) {
		return typeResult
	}

	// The original's comment: handle the case where there is a class variable
	// defined with the same name, but there's also now an instance variable
	// introduced. Combine the type of the class variable with that of the new
	// instance variable.
	if !memberInfo.IsInstanceMember && isInstanceMember {
		// The original's comment: the class variable is accessed in this case.
		e.setSymbolAccessed(fileInfo, memberInfo.Symbol, node.D.Member)
		memberType := e.GetTypeOfMember(memberInfo)
		copied := *typeResult
		copied.Type = CombineTypes([]Type{typeResult.Type, memberType}, nil)
		return &copied
	}

	return typeResult
}

// checkSlotsAssignment is the original's __slots__ check.
//
// Its comment on the empty case: skip this check if the local slots is specified
// but empty and the class isn't final. This pattern is used in a legitimate
// manner for mix-in classes.
func (e *typeEvaluator) checkSlotsAssignment(
	node *parser.MemberAccessNode, memberClass *ClassType, memberName string,
) {
	inheritedSlotsNames := ClassTypeGetInheritedSlotsNames(memberClass)
	if inheritedSlotsNames == nil || memberClass.Shared.LocalSlotsNames == nil {
		return
	}

	if len(memberClass.Shared.LocalSlotsNames) == 0 && !ClassTypeIsFinal(memberClass) {
		return
	}

	if containsString(inheritedSlotsNames, memberName) {
		return
	}

	// The original's comment: determine whether the assignment corresponds to a
	// descriptor that was assigned as a class variable. If so, then slots will not
	// apply in this case.
	classMemberDetails := LookUpClassMember(memberClass, memberName,
		MemberAccessFlagsSkipInstanceMembers, nil)
	isPotentiallyDescriptor := false

	if classMemberDetails != nil {
		classMemberSymbolType := e.GetEffectiveTypeOfSymbol(classMemberDetails.Symbol)
		if IsAnyOrUnknown(classMemberSymbolType) || IsUnbound(classMemberSymbolType) ||
			IsMaybeDescriptorInstance(classMemberSymbolType, true) {
			isPotentiallyDescriptor = true
		}
	}

	if !isPotentiallyDescriptor {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.SlotsAttributeError().Format(memberName),
			node.D.Member, nil)
	}
}

// checkConstantRedefinition is the original's constant check.
func (e *typeEvaluator) checkConstantRedefinition(
	node *parser.MemberAccessNode,
	memberFields SymbolTable,
	memberName string,
	srcExprNode parser.ExpressionNode,
) {
	symbol, found := memberFields.Get(memberName)
	assert(found, "expected the member to be present in the class's symbol table")

	typedDecls := symbol.GetDeclarations()
	if len(typedDecls) == 0 || srcExprNode == nil {
		return
	}

	varDecl, isVar := typedDecls[0].(*VariableDeclaration)
	if !isVar {
		return
	}

	// The declaration that IS this assignment is not a redefinition of itself.
	if parser.ParseNode(node.D.Member) == varDecl.Node {
		return
	}

	if varDecl.IsConstant {
		e.AddDiagnostic(DiagnosticRuleReportConstantRedefinition,
			localization.LocMessage.ConstantRedefinition().Format(node.D.Member.D.Value),
			node.D.Member, nil)
	}
}
