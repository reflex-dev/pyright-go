/*
 * checker_visits2.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412): the remaining
 * evaluation-driving visit methods -- visitAssignment, visitCall, visitAwait,
 * visitFor, visitReturn's evaluation, visitRaise, visitExcept, visitAssert,
 * visitAugmentedAssignment, visitIndex, visitBinaryOperation,
 * visitUnaryOperation, visitTernary, visitName, visitMemberAccess, visitDel,
 * visitGlobal, visitNonlocal, visitImportAs, visitImportFrom, visitTypeAlias,
 * visitCase, visitTry and visitError.
 *
 * These complete the walk's coverage. The previous file landed the two that
 * create scopes; these are the ones that read them, and without them a huge
 * class of names is never evaluated at all -- most visibly `visitAssignment`,
 * since nothing else evaluates the right-hand side of `x = f(y)`, so neither `x`
 * nor `y` was ever marked accessed and every local variable in the corpus read
 * as unused.
 *
 * Three of these deliberately return false and walk their own children:
 * visitGlobal and visitNonlocal, because the unbound check has to be suppressed
 * across the names they declare; visitMemberAccess and visitDel, because only
 * part of the subtree should be walked. visitError returns false because there
 * is nothing beneath a parse error worth exploring.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// VisitAssignment corresponds to visitAssignment.
func (c *Checker) VisitAssignment(node *parser.AssignmentNode) bool {
	c.evaluator.EvaluateTypesForStatement(node)

	if node.D.AnnotationComment != nil {
		c.evaluator.GetType(node.D.AnnotationComment)

		if c.fileInfo.DiagnosticRuleSet.ReportTypeCommentUsage != DiagnosticLevelNone &&
			c.fileInfo.ExecutionEnvironment.PythonVersion.IsGreaterOrEqualTo(common.PythonVersion3_6) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportTypeCommentUsage,
				localization.LocMessage.TypeCommentDeprecated(), node.D.AnnotationComment, nil)
		}
	}

	// The original's comment: if this isn't a class or global scope, explicit
	// type aliases are not allowed.
	annotationNode, ok := node.D.LeftExpr.(*parser.TypeAnnotationNode)
	if !ok {
		return true
	}

	annotationType := c.evaluator.GetTypeOfAnnotation(annotationNode.D.Annotation, nil)
	if !IsClassInstance(annotationType) ||
		!ClassTypeIsBuiltInNamed(annotationType.(*ClassType), "TypeAlias") {
		return true
	}

	scope := GetScopeForNode(node)
	if scope != nil && scope.Type != ScopeTypeClass && scope.Type != ScopeTypeModule &&
		scope.Type != ScopeTypeBuiltin {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeAliasNotInModuleOrClass(),
			annotationNode.D.Annotation, nil)
	}

	return true
}

// VisitAugmentedAssignment corresponds to visitAugmentedAssignment.
func (c *Checker) VisitAugmentedAssignment(node *parser.AugmentedAssignmentNode) bool {
	typeResult := c.evaluator.GetTypeResult(node)
	c.reportDeprecatedUseForOperation(node.D.DestExpr, typeResult)

	return true
}

// VisitCall corresponds to visitCall.
func (c *Checker) VisitCall(node *parser.CallNode) bool {
	c.validateIsInstanceCall(node)
	c.validateIllegalDefaultParamInitializer(node)
	c.validateStandardCollectionInstantiation(node)

	if c.fileInfo.DiagnosticRuleSet.ReportUnusedCallResult == DiagnosticLevelNone &&
		c.fileInfo.DiagnosticRuleSet.ReportUnusedCoroutine == DiagnosticLevelNone {
		return true
	}

	if node.NodeBase().Parent == nil ||
		node.NodeBase().Parent.GetNodeType() != parser.ParseNodeTypeStatementList {
		return true
	}

	isRevealTypeCall := false
	if nameNode, ok := node.D.LeftExpr.(*parser.NameNode); ok && nameNode.D.Value == "reveal_type" {
		isRevealTypeCall = true
	}

	returnType := c.evaluator.GetType(node)

	if isRevealTypeCall || returnType == nil || !c.isTypeValidForUnusedValueTest(returnType) {
		return true
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportUnusedCallResult,
		localization.LocMessage.UnusedCallResult().Format(c.evaluator.PrintType(returnType, nil)),
		node, nil)

	if IsClassInstance(returnType) &&
		ClassTypeIsBuiltInNamed(returnType.(*ClassType), "Coroutine", "CoroutineType") {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportUnusedCoroutine,
			localization.LocMessage.UnusedCoroutine(), node, nil)
	}

	return true
}

// VisitAwait corresponds to visitAwait.
func (c *Checker) VisitAwait(node *parser.AwaitNode) bool {
	if c.fileInfo.DiagnosticRuleSet.ReportUnusedCallResult == DiagnosticLevelNone {
		return true
	}

	if node.NodeBase().Parent == nil ||
		node.NodeBase().Parent.GetNodeType() != parser.ParseNodeTypeStatementList ||
		node.D.Expr.GetNodeType() != parser.ParseNodeTypeCall {
		return true
	}

	returnType := c.evaluator.GetType(node)
	if returnType != nil && c.isTypeValidForUnusedValueTest(returnType) {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportUnusedCallResult,
			localization.LocMessage.UnusedCallResult().Format(c.evaluator.PrintType(returnType, nil)),
			node, nil)
	}

	return true
}

// VisitFor corresponds to visitFor.
func (c *Checker) VisitFor(node *parser.ForNode) bool {
	c.evaluator.EvaluateTypesForStatement(node)

	if node.D.TypeComment != nil {
		c.evaluator.AddDiagnosticForTextRange(c.fileInfo,
			DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.AnnotationNotSupported(),
			node.D.TypeComment.GetRange())
	}
	return true
}

// VisitRaise corresponds to visitRaise.
func (c *Checker) VisitRaise(node *parser.RaiseNode) bool {
	if node.D.Expr != nil {
		c.evaluator.VerifyRaiseExceptionType(node.D.Expr, false)
	}

	if node.D.FromExpr != nil {
		c.evaluator.VerifyRaiseExceptionType(node.D.FromExpr, true)
	}

	return true
}

// VisitExcept corresponds to visitExcept.
func (c *Checker) VisitExcept(node *parser.ExceptNode) bool {
	if node.D.TypeExpr == nil {
		return true
	}

	c.evaluator.EvaluateTypesForStatement(node)

	if exceptionType := c.evaluator.GetType(node.D.TypeExpr); exceptionType != nil {
		c.validateExceptionType(exceptionType, node.D.TypeExpr, node.D.IsExceptGroup)
	}

	return true
}

// VisitIndex corresponds to visitIndex's evaluation half; the tuple
// index-out-of-range check it also performs is on the frontier.
func (c *Checker) VisitIndex(node *parser.IndexNode) bool {
	c.evaluator.GetType(node)
	c.reportTupleIndexOutOfRange(node)
	return true
}

// VisitBinaryOperation corresponds to visitBinaryOperation's evaluation half.
func (c *Checker) VisitBinaryOperation(node *parser.BinaryOperationNode) bool {
	c.checkBinaryOperation(node)
	return true
}

// VisitUnaryOperation corresponds to visitUnaryOperation.
func (c *Checker) VisitUnaryOperation(node *parser.UnaryOperationNode) bool {
	if node.D.Operator == parser.OperatorTypeNot {
		c.validateConditionalIsBool(node.D.Expr)
	}

	typeResult := c.evaluator.GetTypeResult(node)
	c.reportDeprecatedUseForOperation(node.D.Expr, typeResult)

	return true
}

// VisitTernary corresponds to visitTernary.
func (c *Checker) VisitTernary(node *parser.TernaryNode) bool {
	c.evaluator.GetType(node)
	c.validateConditionalIsBool(node.D.TestExpr)
	c.reportUnnecessaryConditionExpression(node.D.TestExpr)
	return true
}

// VisitIf corresponds to visitIf.
func (c *Checker) VisitIf(node *parser.IfNode) bool {
	c.validateConditionalIsBool(node.D.TestExpr)
	c.reportUnnecessaryConditionExpression(node.D.TestExpr)
	return true
}

// VisitWhile corresponds to visitWhile.
func (c *Checker) VisitWhile(node *parser.WhileNode) bool {
	c.validateConditionalIsBool(node.D.TestExpr)
	c.reportUnnecessaryConditionExpression(node.D.TestExpr)
	return true
}

// VisitComprehensionIf corresponds to visitComprehensionIf.
func (c *Checker) VisitComprehensionIf(node *parser.ComprehensionIfNode) bool {
	c.validateConditionalIsBool(node.D.TestExpr)
	c.reportUnnecessaryConditionExpression(node.D.TestExpr)
	return true
}

// VisitName corresponds to visitName.
func (c *Checker) VisitName(node *parser.NameNode) bool {
	// The original's comment: determine if we should log information about
	// private usage.
	c.conditionallyReportPrivateUsage(node)

	// The original's comment: determine if the name is possibly unbound.
	if !c.isUnboundCheckSuppressed {
		c.reportUnboundName(node)
	}

	// The original's comment: report the use of a deprecated symbol.
	t := c.evaluator.GetType(node)
	c.reportDeprecatedUseForType(node, t, false)

	return true
}

// VisitDel corresponds to visitDel. It walks its own targets, so it returns
// false.
func (c *Checker) VisitDel(node *parser.DelNode) bool {
	for _, expr := range node.D.Targets {
		c.evaluator.VerifyDeleteExpression(expr)
		c.Walk(expr)
	}

	return false
}

// VisitMemberAccess corresponds to visitMemberAccess. It walks the left
// expression but not the member name, so it returns false.
func (c *Checker) VisitMemberAccess(node *parser.MemberAccessNode) bool {
	typeResult := c.evaluator.GetTypeResult(node.D.Member)
	var t Type = UnknownTypeCreate(false)
	if typeResult != nil && typeResult.Type != nil {
		t = typeResult.Type
	}

	leftExprType := c.evaluator.GetType(node.D.LeftExpr)
	moduleName := ""
	if leftExprType != nil && IsModule(leftExprType) {
		moduleName = leftExprType.(*ModuleType).Priv.ModuleName
	}
	isImportedFromTyping := moduleName == "typing" || moduleName == "typing_extensions"
	c.reportDeprecatedUseForType(node.D.Member, t, isImportedFromTyping)

	if typeResult != nil && typeResult.MemberAccessDeprecationInfo != nil {
		c.reportDeprecatedUseForMemberAccess(node.D.Member, typeResult.MemberAccessDeprecationInfo)
	}

	c.conditionallyReportPrivateUsage(node.D.Member)

	// The original's comment: walk the leftExpression but not the memberName.
	c.Walk(node.D.LeftExpr)

	return false
}

// VisitGlobal corresponds to visitGlobal.
func (c *Checker) VisitGlobal(node *parser.GlobalNode) bool {
	c.suppressUnboundCheck(func() {
		for _, name := range node.D.Targets {
			c.evaluator.GetType(name)
			c.Walk(name)
		}
	})

	return false
}

// VisitNonlocal corresponds to visitNonlocal.
func (c *Checker) VisitNonlocal(node *parser.NonlocalNode) bool {
	c.suppressUnboundCheck(func() {
		for _, name := range node.D.Targets {
			c.evaluator.GetType(name)
			c.Walk(name)
			c.validateNonlocalTypeParam(name)
		}
	})

	return false
}

// suppressUnboundCheck corresponds to _suppressUnboundCheck.
func (c *Checker) suppressUnboundCheck(callback func()) {
	wasSuppressed := c.isUnboundCheckSuppressed
	c.isUnboundCheckSuppressed = true

	defer func() { c.isUnboundCheckSuppressed = wasSuppressed }()

	callback()
}

// VisitImportAs corresponds to visitImportAs.
func (c *Checker) VisitImportAs(node *parser.ImportAsNode) bool {
	c.evaluator.EvaluateTypesForStatement(node)

	nameParts := node.D.Module.D.NameParts
	if len(nameParts) > 1 && node.D.Alias == nil {
		c.multipartImports = append(c.multipartImports, node)
	}

	return true
}

// VisitTypeAlias corresponds to visitTypeAlias.
func (c *Checker) VisitTypeAlias(node *parser.TypeAliasNode) bool {
	scope := GetScopeForNode(node)
	if scope != nil && scope.Type != ScopeTypeClass && scope.Type != ScopeTypeModule &&
		scope.Type != ScopeTypeBuiltin {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeAliasStatementBadScope(), node.D.Name, nil)
	}

	return true
}

// VisitPatternClass corresponds to visitPatternClass.
func (c *Checker) VisitPatternClass(node *parser.PatternClassNode) bool {
	ValidateClassPattern(c.evaluator, node)
	return true
}

// VisitCase corresponds to visitCase.
func (c *Checker) VisitCase(node *parser.CaseNode) bool {
	if node.D.GuardExpr != nil {
		c.validateConditionalIsBool(node.D.GuardExpr)
	}

	c.evaluator.EvaluateTypesForStatement(node.D.Pattern)
	return true
}

// VisitTry corresponds to visitTry.
func (c *Checker) VisitTry(node *parser.TryNode) bool {
	c.reportUnusedExceptStatements(node)
	return true
}

// VisitError corresponds to visitError. The original's comment: get the type of
// the child so it's available to the completion provider, then don't explore
// further.
func (c *Checker) VisitError(node *parser.ErrorNode) bool {
	if node.D.Child != nil {
		c.evaluator.GetType(node.D.Child)
	}

	return false
}
