/*
 * checker_enummembers.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateEnumMembers.
 *
 * Whether a name in an enum body is a *member* or an ordinary class attribute
 * is decided by the runtime, not by annotations -- which is why the membership
 * test passes ignoreAnnotation and then treats a present annotation as an error
 * rather than as evidence. `RED: int = 1` is a member with an illegal
 * annotation, not a plain attribute.
 *
 * Once a member is identified there are two ways its assigned value gets
 * checked, and they are mutually exclusive. If the enum defines its own
 * `__new__` or `__init__`, the value is validated by *calling* that method with
 * it -- unpacked if it is a tuple, since `RED = (1, "red")` passes two
 * arguments. Otherwise, if `_value_` has a declared type, the value is checked
 * against that type directly.
 *
 * A `__new__` or `__init__` inherited from `Enum` itself is discarded: it is not
 * the author's, and calling the value against it would be meaningless. That is
 * what the two isBuiltIn tests do, and it is separate from the
 * SkipObjectBaseClass flag, which only excludes `object`.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateEnumMembers corresponds to _validateEnumMembers.
func (c *Checker) validateEnumMembers(classType *ClassType, node *parser.ClassNode) {
	if !ClassTypeIsEnumClass(classType) || ClassTypeIsBuiltIn(classType) {
		return
	}

	// The original's comment: does the "_value_" field have a declared type? If
	// so, we'll enforce it.
	declaredValueType := GetEnumDeclaredValueType(c.evaluator, classType, true)

	// The original's comment: is there a custom "__new__" and/or "__init__"
	// method? If so, we'll verify that the signature of these calls is compatible
	// with the values.
	newMemberTypeResult := GetBoundNewMethod(c.evaluator, node.D.Name, classType,
		nil, MemberAccessFlagsSkipObjectBaseClass)

	// The original's comment: if this __new__ comes from a built-in class like
	// Enum, we'll ignore it.
	if newMemberTypeResult != nil && newMemberTypeResult.ClassType != nil {
		if IsClass(newMemberTypeResult.ClassType) &&
			ClassTypeIsBuiltIn(newMemberTypeResult.ClassType.(*ClassType)) {
			newMemberTypeResult = nil
		}
	}

	initMemberTypeResult := GetBoundInitMethod(c.evaluator, node.D.Name,
		ClassTypeCloneAsInstance(classType, true), nil, MemberAccessFlagsSkipObjectBaseClass)

	// The original's comment: if this __init__ comes from a built-in class like
	// Enum, we'll ignore it.
	if initMemberTypeResult != nil && initMemberTypeResult.ClassType != nil {
		if IsClass(initMemberTypeResult.ClassType) &&
			ClassTypeIsBuiltIn(initMemberTypeResult.ClassType.(*ClassType)) {
			initMemberTypeResult = nil
		}
	}

	symbolTable := ClassTypeGetSymbolTable(classType)
	for _, name := range symbolTable.Keys() {
		symbol, _ := symbolTable.Get(name)
		c.validateOneEnumMember(classType, name, symbol,
			declaredValueType, newMemberTypeResult, initMemberTypeResult)
	}
}

// validateOneEnumMember is the body of the original's per-symbol forEach.
func (c *Checker) validateOneEnumMember(
	classType *ClassType,
	name string,
	symbol *Symbol,
	declaredValueType Type,
	newMemberTypeResult *TypeResult,
	initMemberTypeResult *TypeResult,
) {
	// The original's comment: determine whether this is an enum member. We ignore
	// the presence of an annotation in this case because the runtime does. From a
	// type checking perspective, if the runtime treats the assignment as an enum
	// member but there is a type annotation present, it is considered a type
	// checking error.
	symbolType := transformTypeForEnumMember(c.evaluator, classType, name, true, 0)

	// The original's comment: is this symbol a literal instance of the enum
	// class?
	if symbolType == nil || !IsClassInstance(symbolType) ||
		!ClassTypeIsSameGenericClass(symbolType.(*ClassType),
			ClassTypeCloneAsInstance(classType, true), 0) {
		return
	}
	enumLiteral, isEnumLiteral := symbolType.(*ClassType).Priv.LiteralValue.(*EnumLiteral)
	if !isEnumLiteral {
		return
	}

	// The original's comment: enum members should not have type annotations.
	typedDecls := symbol.GetTypedDeclarations()
	if len(typedDecls) > 0 {
		if varDecl, ok := typedDecls[0].(*VariableDeclaration); ok && varDecl.InferredTypeSource != nil {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.EnumMemberTypeAnnotation(), varDecl.Node, nil)
		}
		return
	}

	decls := symbol.GetDeclarations()
	if len(decls) == 0 {
		// The original indexes decls[0] unconditionally below; a symbol reaching
		// here always has a declaration, but Go would panic rather than read
		// undefined.
		return
	}

	// The original's comment: look for a duplicate assignment.
	if len(decls) >= 2 {
		if _, ok := decls[0].(*VariableDeclaration); ok {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.DuplicateEnumMember().Format(name),
				decls[1].DeclBase().Node, nil)
			return
		}
	}

	firstDecl, ok := decls[0].(*VariableDeclaration)
	if !ok {
		return
	}

	// The original's comment: look for an enum attribute annotated with "Final".
	if firstDecl.IsFinal {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.EnumMemberTypeAnnotation(), firstDecl.Node, nil)
	}

	declNode := firstDecl.Node
	assignedValueType := enumLiteral.ItemType

	var errorNode parser.ParseNode = declNode
	if assignmentNode := GetParentNodeOfType(declNode, parser.ParseNodeTypeAssignment); assignmentNode != nil {
		errorNode = assignmentNode.(*parser.AssignmentNode).D.RightExpr
	}

	errorExpr, isExpr := errorNode.(parser.ExpressionNode)
	if !isExpr {
		return
	}

	// The original's comment: validate the __new__ and __init__ methods if
	// present.
	if newMemberTypeResult != nil || initMemberTypeResult != nil {
		if IsAnyOrUnknown(assignedValueType) {
			return
		}

		// The original's comment: construct an argument list. If the assigned type
		// is a tuple, we'll unpack it. Otherwise, only one argument is passed.
		argCategory := parser.ArgCategorySimple
		if IsClassInstance(assignedValueType) && IsTupleClass(assignedValueType.(*ClassType)) {
			argCategory = parser.ArgCategoryUnpackedList
		}

		argList := []*Arg{{
			ArgCategory: argCategory,
			TypeResult:  &TypeResult{Type: assignedValueType},
		}}

		if newMemberTypeResult != nil {
			c.evaluator.ValidateCallArgs(errorExpr, argList, newMemberTypeResult, nil, false, nil)
		}

		if initMemberTypeResult != nil {
			c.evaluator.ValidateCallArgs(errorExpr, argList, initMemberTypeResult, nil, false, nil)
		}

		return
	}

	if declaredValueType == nil {
		return
	}

	// The original's comment: if the assigned value is already an instance of
	// this enum class, skip this check.
	if IsClassInstance(assignedValueType) &&
		ClassTypeIsSameGenericClass(assignedValueType.(*ClassType), classType, 0) {
		return
	}

	diag := common.NewDiagnosticAddendum()
	if c.evaluator.AssignType(declaredValueType, assignedValueType, diag,
		nil, AssignTypeFlagsDefault, 0) {
		return
	}

	srcDest := c.evaluator.PrintSrcDestTypes(assignedValueType, declaredValueType)
	c.evaluator.AddDiagnostic(DiagnosticRuleReportAssignmentType,
		localization.LocMessage.TypeAssignmentMismatch().Format(srcDest.SourceType, srcDest.DestType)+
			diag.GetString(),
		errorExpr, nil)
}
