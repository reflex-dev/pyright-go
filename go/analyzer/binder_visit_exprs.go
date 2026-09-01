/*
 * binder_visit_exprs.go
 *
 * The binder's expression and pattern visitors, transliterated from
 * analyzer/binder.ts (pyright 1.1.412): with, ternary, the logical operators,
 * comprehensions, match, and the three pattern visitors.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// VisitWith corresponds to visitWith.
//
// The original's comment on the model, kept because nothing else explains it:
//
// We need to treat the "with" body as though it is wrapped in a try/except
// block because some context managers catch and suppress exceptions. We'll make
// use of a special "context manager label" which acts like a regular branch
// label in most respects except that it is disabled if none of the context
// managers support exception suppression. We won't be able to determine whether
// any context managers support exception processing until the type evaluation
// phase.
//
//	(pre with suite)
//	       ^
//	       |<--------------------|
//	  (with suite)<--------------|
//	       ^                     |
//	       |    ContextManagerSwallowExceptionTarget
//	       |                     ^
//	       |          PostContextManagerLabel
//	       |                     ^
//	       |---------------------|
//	       |
//	 (after with)
//
// In addition to the ContextManagerSwallowExceptionTarget, we'll create a
// second target called ContextManagerForwardExceptionTarget that forwards
// exceptions to existing exception targets if they exist.
func (b *Binder) VisitWith(node *parser.WithNode) bool {
	for _, item := range node.D.WithItems {
		b.Walk(item.D.Expr)
		if item.D.Target != nil {
			b.bindPossibleTupleNamedTarget(item.D.Target, nil)
			b.addInferredTypeAssignmentForVariable(item.D.Target, item, false)
			b.createAssignmentTargetFlowNodes(item.D.Target, true /* walkTargets */, false /* unbound */)
		}
	}

	itemExprs := make([]parser.ExpressionNode, 0, len(node.D.WithItems))
	for _, item := range node.D.WithItems {
		itemExprs = append(itemExprs, item.D.Expr)
	}

	contextManagerSwallowExceptionTarget := b.createContextManagerLabel(
		itemExprs,
		node.D.IsAsync,
		false, // blockIfSwallowsExceptions
	)
	b.addAntecedent(&contextManagerSwallowExceptionTarget.FlowLabel, b.currentFlowNode)

	contextManagerForwardExceptionTarget := b.createContextManagerLabel(
		itemExprs,
		node.D.IsAsync,
		true, // blockIfSwallowsExceptions
	)
	for _, exceptionTarget := range b.currentExceptTargets {
		b.addAntecedent(exceptionTarget, contextManagerForwardExceptionTarget)
	}

	preWithSuiteNode := b.currentFlowNode
	postContextManagerLabel := b.createBranchLabel(preWithSuiteNode)
	b.addAntecedent(&postContextManagerLabel.FlowLabel, contextManagerSwallowExceptionTarget)

	postContextManagerLabel.AffectedExpressions = b.trackCodeFlowExpressions(func() {
		b.useExceptTargets(
			[]*FlowLabel{
				&contextManagerSwallowExceptionTarget.FlowLabel,
				&contextManagerForwardExceptionTarget.FlowLabel,
			},
			func() {
				b.Walk(node.D.Suite)
			},
		)

		b.addAntecedent(&postContextManagerLabel.FlowLabel, b.currentFlowNode)
		b.currentFlowNode = postContextManagerLabel

		// Model the call to `__exit__` as a potential exception generator.
		if !b.isCodeUnreachable() {
			b.addExceptTargets(b.currentFlowNode)
		}

		if node.D.AsyncToken != nil && b.fileInfo.IPythonMode == IPythonModeNone {
			// Top level async with is allowed in ipython mode.
			enclosingFunction := GetEnclosingFunction(node)
			if enclosingFunction == nil || !enclosingFunction.D.IsAsync {
				b.addSyntaxError(localization.LocMessage.AsyncNotInAsyncFunction(), node.D.AsyncToken.GetRange())
			}
		}
	})

	return false
}

// VisitTernary corresponds to visitTernary.
func (b *Binder) VisitTernary(node *parser.TernaryNode) bool {
	preTernaryFlowNode := b.currentFlowNode
	trueLabel := b.createBranchLabel(nil)
	falseLabel := b.createBranchLabel(nil)
	postExpressionLabel := b.createBranchLabel(preTernaryFlowNode)

	postExpressionLabel.AffectedExpressions = b.trackCodeFlowExpressions(func() {
		// Handle the test expression.
		b.bindConditional(node.D.TestExpr, &trueLabel.FlowLabel, &falseLabel.FlowLabel)

		// Handle the "true" portion (the "if" expression).
		b.currentFlowNode = b.finishBranchLabel(trueLabel)
		b.Walk(node.D.IfExpr)
		b.addAntecedent(&postExpressionLabel.FlowLabel, b.currentFlowNode)

		// Handle the "false" portion (the "else" expression).
		b.currentFlowNode = b.finishBranchLabel(falseLabel)
		b.Walk(node.D.ElseExpr)
		b.addAntecedent(&postExpressionLabel.FlowLabel, b.currentFlowNode)

		b.currentFlowNode = b.finishBranchLabel(postExpressionLabel)
	})

	return false
}

// VisitUnaryOperation corresponds to visitUnaryOperation.
func (b *Binder) VisitUnaryOperation(node *parser.UnaryOperationNode) bool {
	if node.D.Operator == parser.OperatorTypeNot && b.currentFalseTarget != nil && b.currentTrueTarget != nil {
		// Swap the existing true/false targets.
		b.bindConditional(node.D.Expr, b.currentFalseTarget, b.currentTrueTarget)
	} else {
		// Temporarily set the true/false targets to undefined because this
		// unary operation is not part of a chain of logical expressions
		// (AND/OR/NOT subexpressions).
		b.disableTrueFalseTargets(func() {
			// Evaluate the operand expression.
			b.Walk(node.D.Expr)
		})
	}

	return false
}

// VisitBinaryOperation corresponds to visitBinaryOperation.
func (b *Binder) VisitBinaryOperation(node *parser.BinaryOperationNode) bool {
	if node.D.Operator == parser.OperatorTypeAnd || node.D.Operator == parser.OperatorTypeOr {
		trueTarget := b.currentTrueTarget
		falseTarget := b.currentFalseTarget
		var postRightLabel *FlowBranchLabel

		if trueTarget == nil || falseTarget == nil {
			postRightLabel = b.createBranchLabel(nil)
			trueTarget = &postRightLabel.FlowLabel
			falseTarget = trueTarget
		}

		preRightLabel := b.createBranchLabel(nil)
		if node.D.Operator == parser.OperatorTypeAnd {
			b.bindConditional(node.D.LeftExpr, &preRightLabel.FlowLabel, falseTarget)
		} else {
			b.bindConditional(node.D.LeftExpr, trueTarget, &preRightLabel.FlowLabel)
		}
		b.currentFlowNode = b.finishBranchLabel(preRightLabel)
		b.bindConditional(node.D.RightExpr, trueTarget, falseTarget)
		if postRightLabel != nil {
			b.currentFlowNode = b.finishBranchLabel(postRightLabel)
		}
	} else {
		// Temporarily set the true/false targets to undefined because this
		// binary operation is not part of a chain of logical expressions
		// (AND/OR/NOT subexpressions).
		b.disableTrueFalseTargets(func() {
			b.Walk(node.D.LeftExpr)
			b.Walk(node.D.RightExpr)
		})
	}

	return false
}

// VisitComprehension corresponds to visitComprehension.
func (b *Binder) VisitComprehension(node *parser.ComprehensionNode) bool {
	enclosingFunction := GetEnclosingFunction(node)

	// The first iterable is executed outside of the comprehension scope.
	if len(node.D.ForIfNodes) > 0 {
		if first, ok := node.D.ForIfNodes[0].(*parser.ComprehensionForNode); ok {
			b.Walk(first.D.IterableExpr)
		}
	}

	b.createNewScope(
		ScopeTypeComprehension,
		b.getNonClassParentScope(),
		nil, // proxyScope
		nil, // chainedModuleLevelScopeLookup
		func() {
			SetScope(node, b.currentScope)

			falseLabel := b.createBranchLabel(nil)

			// We'll walk the forIfNodes list twice. The first time we'll bind
			// targets of for statements. The second time we'll walk expressions
			// and create the control flow graph.
			for _, forIfNode := range node.D.ForIfNodes {
				compr, ok := forIfNode.(*parser.ComprehensionForNode)
				if !ok {
					continue
				}

				// addedSymbols is populated but never read; the original
				// allocates it per iteration all the same.
				addedSymbols := common.NewOrderedMap[string, *Symbol]()
				b.bindPossibleTupleNamedTarget(compr.D.TargetExpr, addedSymbols)
				b.addInferredTypeAssignmentForVariable(compr.D.TargetExpr, compr, false)

				// Async for is not allowed outside of an async function unless
				// we're in ipython mode.
				if compr.D.AsyncToken != nil && b.fileInfo.IPythonMode == IPythonModeNone {
					if enclosingFunction == nil || !enclosingFunction.D.IsAsync {
						// Allow if it's within a generator expression.
						// Execution of generator expressions is deferred and
						// therefore can be run within the context of an async
						// function later.
						if parent := node.NodeBase().Parent; parent != nil {
							switch parent.GetNodeType() {
							case parser.ParseNodeTypeList,
								parser.ParseNodeTypeSet,
								parser.ParseNodeTypeDictionary:
								b.addSyntaxError(
									localization.LocMessage.AsyncNotInAsyncFunction(),
									compr.D.AsyncToken.GetRange(),
								)
							}
						}
					}
				}
			}

			for i, forIfNode := range node.D.ForIfNodes {
				if compr, ok := forIfNode.(*parser.ComprehensionForNode); ok {
					// We already walked the first iterable expression above, so
					// skip it here.
					if i != 0 {
						b.Walk(compr.D.IterableExpr)
					}

					b.createAssignmentTargetFlowNodes(
						compr.D.TargetExpr,
						true,  // walkTargets
						false, // unbound
					)
				} else {
					comprIf := forIfNode.(*parser.ComprehensionIfNode)
					trueLabel := b.createBranchLabel(nil)
					b.bindConditional(comprIf.D.TestExpr, &trueLabel.FlowLabel, &falseLabel.FlowLabel)
					b.currentFlowNode = b.finishBranchLabel(trueLabel)
				}
			}

			b.Walk(node.D.Expr)
			b.addAntecedent(&falseLabel.FlowLabel, b.currentFlowNode)
			b.currentFlowNode = b.finishBranchLabel(falseLabel)
		},
	)

	return false
}

// VisitMatch corresponds to visitMatch.
func (b *Binder) VisitMatch(node *parser.MatchNode) bool {
	// Evaluate the subject expression.
	b.Walk(node.D.Expr)

	expressionList := []CodeFlowReferenceExpressionNode{}
	isSubjectNarrowable := b.isNarrowingExpression(node.D.Expr, &expressionList, narrowExprOptions{})

	// We also support narrowing of individual tuple entries found within a
	// match subject expression, so add those here as well.
	if tuple, ok := node.D.Expr.(*parser.TupleNode); ok {
		for _, itemExpr := range tuple.D.Items {
			if b.isNarrowingExpression(itemExpr, &expressionList, narrowExprOptions{}) {
				isSubjectNarrowable = true
			}
		}
	}

	if isSubjectNarrowable {
		for _, expr := range expressionList {
			b.currentScopeCodeFlowExpressions.Add(CreateKeyForReference(expr))
		}
	}

	postMatchLabel := b.createBranchLabel(nil)
	foundIrrefutableCase := false

	// Model the match statement as a series of if/elif clauses each of which
	// tests for the specified pattern (and optionally for the guard condition).
	for _, caseStatement := range node.D.Cases {
		postCaseLabel := b.createBranchLabel(nil)
		preGuardLabel := b.createBranchLabel(nil)
		preSuiteLabel := b.createBranchLabel(nil)

		// Evaluate the pattern.
		b.addAntecedent(&preGuardLabel.FlowLabel, b.currentFlowNode)

		if !caseStatement.D.IsIrrefutable {
			b.addAntecedent(&postCaseLabel.FlowLabel, b.currentFlowNode)
		} else if caseStatement.D.GuardExpr == nil {
			foundIrrefutableCase = true
		}

		b.currentFlowNode = b.finishBranchLabel(preGuardLabel)

		// Note the active match subject expression prior to binding the
		// pattern. If the pattern involves any targets that overwrite the
		// subject expression, this will be set to undefined.
		b.currentMatchSubjExpr = node.D.Expr

		// Bind the pattern.
		b.Walk(caseStatement.D.Pattern)

		// If the pattern involves targets that overwrite the subject
		// expression, skip creating a flow node for narrowing the subject.
		if b.currentMatchSubjExpr != nil {
			b.createFlowNarrowForPattern(node.D.Expr, caseStatement)
			b.currentMatchSubjExpr = nil
		}

		// Apply the guard expression.
		if caseStatement.D.GuardExpr != nil {
			b.bindConditional(caseStatement.D.GuardExpr, &preSuiteLabel.FlowLabel, &postCaseLabel.FlowLabel)
		} else {
			b.addAntecedent(&preSuiteLabel.FlowLabel, b.currentFlowNode)
		}

		b.currentFlowNode = b.finishBranchLabel(preSuiteLabel)

		// Bind the body of the case statement.
		b.Walk(caseStatement.D.Suite)
		b.addAntecedent(&postMatchLabel.FlowLabel, b.currentFlowNode)

		b.currentFlowNode = b.finishBranchLabel(postCaseLabel)
	}

	// Add a final narrowing step for the subject expression for the entire
	// match statement. This will compute the narrowed type if no case
	// statements are matched.
	if isSubjectNarrowable {
		b.createFlowNarrowForPattern(node.D.Expr, node)
	}

	// Create an "implied else" to conditionally gate code flow based on whether
	// the narrowed type of the subject expression is Never at this point.
	if !foundIrrefutableCase {
		b.createFlowExhaustedMatch(node)
	}

	b.addAntecedent(&postMatchLabel.FlowLabel, b.currentFlowNode)
	b.currentFlowNode = b.finishBranchLabel(postMatchLabel)

	return false
}

// VisitPatternAs corresponds to visitPatternAs.
func (b *Binder) VisitPatternAs(node *parser.PatternAsNode) bool {
	postOrLabel := b.createBranchLabel(nil)

	for _, orPattern := range node.D.OrPatterns {
		b.Walk(orPattern)
		b.addAntecedent(&postOrLabel.FlowLabel, b.currentFlowNode)
	}

	b.currentFlowNode = b.finishBranchLabel(postOrLabel)

	if node.D.Target != nil {
		b.Walk(node.D.Target)
		symbol := b.bindNameToScope(b.currentScope, node.D.Target, nil)
		b.createAssignmentTargetFlowNodes(node.D.Target, false /* walkTargets */, false /* unbound */)

		if symbol != nil {
			symbol.AddDeclaration(&VariableDeclaration{
				DeclarationBase: DeclarationBase{
					Type:            DeclarationTypeVariable,
					Node:            node.D.Target,
					Uri:             b.fileInfo.FileUri,
					Range:           common.ConvertTextRangeToRange(node.D.Target.GetRange(), b.fileInfo.Lines),
					ModuleName:      b.fileInfo.ModuleName,
					IsInExceptSuite: b.isInExceptSuite,
				},
				IsConstant:         IsConstantName(node.D.Target.D.Value),
				InferredTypeSource: node,
				IsExplicitBinding:  b.currentScope.GetBindingType(node.D.Target.D.Value) != NameBindingTypeNone,
			})
		}
	}

	return false
}

// VisitPatternCapture corresponds to visitPatternCapture.
func (b *Binder) VisitPatternCapture(node *parser.PatternCaptureNode) bool {
	if !node.D.IsWildcard {
		b.addPatternCaptureTarget(node.D.Target)
	}

	return true
}

// VisitPatternMappingExpandEntry corresponds to
// visitPatternMappingExpandEntry.
func (b *Binder) VisitPatternMappingExpandEntry(node *parser.PatternMappingExpandEntryNode) bool {
	if node.D.Target.D.Value != "_" {
		b.addPatternCaptureTarget(node.D.Target)
	}

	return true
}
