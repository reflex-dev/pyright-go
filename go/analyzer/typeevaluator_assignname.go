/*
 * typeevaluator_assignname.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * assignTypeToNameNode.
 *
 * The common assignment target, and the site of the diagnostic most users see
 * most often: reportAssignmentType, "type X is not assignable to declared type
 * Y". It is the first place in this port where a declared type and an inferred
 * type are compared and a disagreement is reported.
 *
 * The two branches differ in what they write to the cache:
 *
 *   - With a declared type, the assigned type is checked against it. On success
 *     the DECLARED type narrowed by the assignment is cached; on failure the
 *     unnarrowed declared type is cached, so one bad assignment does not poison
 *     every later read of the name.
 *   - Without one, a class-scope name that is not a constant and not Final has
 *     its literal and TypeForm stripped, because a subclass may override it with
 *     a different value.
 *
 * Both TypeVar bindings are made bound before the comparison. Comparing a free
 * TypeVar against a bound one is always a mismatch, so skipping that step would
 * report an error on every generic method assignment.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// assignTypeToNameNode corresponds to the function of the same name.
func (e *typeEvaluator) assignTypeToNameNode(
	nameNode *parser.NameNode,
	typeResult *TypeResult,
	ignoreEmptyContainers bool,
	srcExpression parser.ExpressionNode,
	allowAssignmentToFinalVar bool,
	expectedTypeDiagAddendum *common.DiagnosticAddendum,
) {
	nameValue := nameNode.D.Value

	symbolWithScope := e.lookUpSymbolRecursive(nameNode, nameValue, false, false)
	if symbolWithScope == nil {
		// The original's comment: this can happen when we are evaluating a piece
		// of code that was determined to be unreachable by the binder.
		return
	}

	declarations := symbolWithScope.Symbol.GetDeclarations()
	fileInfo := GetFileInfo(nameNode)

	var declaredType Type
	if info := e.getDeclaredTypeOfSymbol(symbolWithScope.Symbol, nil); info != nil {
		declaredType = info.Type
	}

	// The original's comment: if this is a class scope and there is no type
	// declared for this class variable, see if a parent class has a type
	// declared.
	if declaredType == nil && symbolWithScope.Scope.Type == ScopeTypeClass {
		if containingClass := GetEnclosingClass(nameNode, false); containingClass != nil {
			if classType := e.GetTypeOfClass(containingClass); classType != nil {
				memberInfo := LookUpClassMember(classType.ClassType, nameValue,
					MemberAccessFlagsSkipOriginalClass, nil)
				if memberInfo != nil && memberInfo.IsTypeDeclared {
					declaredType = e.GetTypeOfMember(memberInfo)
				}
			}
		}
	}

	destType := e.narrowAssignedTypeToDeclared(
		nameNode, typeResult, declaredType, symbolWithScope, srcExpression, expectedTypeDiagAddendum)

	e.checkConstantAndFinalReassignment(nameNode, nameValue, declarations, allowAssignmentToFinalVar)

	if !typeResult.IsIncomplete {
		e.reportPossibleUnknownAssignment(
			fileInfo.DiagnosticRuleSet.ReportUnknownVariableType,
			DiagnosticRuleReportUnknownVariableType,
			nameNode,
			typeResult.Type,
			nameNode,
			ignoreEmptyContainers,
		)
	}

	e.writeTypeCache(nameNode,
		&TypeResult{Type: destType, IsIncomplete: typeResult.IsIncomplete},
		evalFlagsNonePtr(), nil, false)
}

// narrowAssignedTypeToDeclared is the original's declared-type check and its
// else branch. It returns the type to cache for the name.
func (e *typeEvaluator) narrowAssignedTypeToDeclared(
	nameNode *parser.NameNode,
	typeResult *TypeResult,
	declaredType Type,
	symbolWithScope *SymbolWithScope,
	srcExpression parser.ExpressionNode,
	expectedTypeDiagAddendum *common.DiagnosticAddendum,
) Type {
	destType := typeResult.Type

	isTypeAlias := declaredType != nil && IsClassInstance(declaredType) &&
		ClassTypeIsBuiltInNamed(declaredType.(*ClassType), "TypeAlias")

	if declaredType != nil && !isTypeAlias {
		diagAddendum := common.NewDiagnosticAddendum()

		// Both sides are made bound before comparing; see the file header.
		liveScopeIds := GetTypeVarScopesForNode(nameNode)
		boundDeclaredType := MakeTypeVarsBound(declaredType, liveScopeIds, true)
		srcType := MakeTypeVarsBound(typeResult.Type, liveScopeIds, true)

		if e.AssignType(boundDeclaredType, srcType, diagAddendum, nil, AssignTypeFlagsDefault, 0) {
			// The original's comment: constrain the resulting type to match the
			// declared type.
			return e.narrowTypeBasedOnAssignment(declaredType, typeResult).Type
		}

		// The original's comment: if there was an expected type mismatch, use
		// that diagnostic addendum because it will be more informative.
		if expectedTypeDiagAddendum != nil {
			diagAddendum = expectedTypeDiagAddendum
		}

		if !typeResult.IsIncomplete {
			// `srcExpression ?? nameNode` for the node, and
			// `diagAddendum.getEffectiveTextRange() ?? srcExpression ?? nameNode`
			// for the range -- the two can differ.
			var errorNode parser.ParseNode = nameNode
			if srcExpression != nil {
				errorNode = srcExpression
			}

			textRange := errorNode.NodeBase().TextRange
			if effective := diagAddendum.GetEffectiveTextRange(); effective != nil {
				textRange = *effective
			}

			types := e.PrintSrcDestTypes(typeResult.Type, declaredType)
			e.AddDiagnostic(
				DiagnosticRuleReportAssignmentType,
				localization.LocMessage.TypeAssignmentMismatch().Format(types.SourceType, types.DestType)+
					diagAddendum.GetString(),
				errorNode,
				&textRange,
			)
		}

		// The original's comment: replace the assigned type with the
		// (unnarrowed) declared type.
		return declaredType
	}

	// The original's comment: if this is a member name (within a class scope) and
	// the member name appears to be a constant, use the strict source type. If
	// it's a member variable that can be overridden by a child class, use the
	// more general version by stripping off the literal and TypeForm.
	if scope := GetScopeForNode(nameNode); scope != nil && scope.Type == ScopeTypeClass {
		if destType.Base().IsInstance() && !IsConstantName(nameNode.D.Value) &&
			!e.IsFinalVariable(symbolWithScope.Symbol) {
			destType = StripTypeForm(e.StripLiteralValue(destType))
		}
	}

	return destType
}

// checkConstantAndFinalReassignment is the original's varDecl block: a constant
// may be assigned only once, and a Final may not be reassigned at all.
func (e *typeEvaluator) checkConstantAndFinalReassignment(
	nameNode *parser.NameNode,
	nameValue string,
	declarations []Declaration,
	allowAssignmentToFinalVar bool,
) {
	varDeclIndex := -1
	for index, decl := range declarations {
		if decl.DeclBase().Type == DeclarationTypeVariable {
			varDeclIndex = index
			break
		}
	}

	if varDeclIndex < 0 {
		return
	}
	varDecl := declarations[varDeclIndex].(*VariableDeclaration)

	// The original's comment: are there any non-var decls before the var decl?
	//
	// The predicate actually tests `varDeclIndex < index`, so it finds a non-var
	// decl AFTER the var decl, not before. The comment and the code disagree;
	// the code is what is transliterated.
	nonVarDecl := false
	for index, decl := range declarations {
		if varDeclIndex < index && decl.DeclBase().Type != DeclarationTypeVariable {
			nonVarDecl = true
			break
		}
	}

	if varDecl.IsConstant {
		// The original's comment: a constant variable can be assigned only once.
		// If this isn't the first assignment, generate an error.
		firstDeclName := GetNameNodeForDeclaration(declarations[0])
		if parser.ParseNode(nameNode) != parser.ParseNode(firstDeclName) || nonVarDecl {
			e.AddDiagnostic(
				DiagnosticRuleReportConstantRedefinition,
				localization.LocMessage.ConstantRedefinition().Format(nameValue),
				nameNode,
				nil,
			)
		}
		return
	}

	if e.IsFinalVariableDeclaration(varDecl) && !allowAssignmentToFinalVar {
		e.AddDiagnostic(
			DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.FinalReassigned().Format(nameValue),
			nameNode,
			nil,
		)
	}
}
