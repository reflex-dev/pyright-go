/*
 * typeevaluator_statement.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * evaluateTypesForStatement.
 *
 * This is the statement-level counterpart of getTypeOfExpressionCore, and like
 * that function it is pure dispatch: it walks up from a node until it finds an
 * enclosing construct it knows how to evaluate, then hands off. Porting it turns
 * one frontier entry into one per statement kind.
 *
 * It is reached from two directions, which is what makes it worth landing
 * before the arms it dispatches to. The checker's walk calls it through
 * getType, and getInferredTypeOfDeclaration calls it to compute a variable's
 * type by evaluating the statement that assigned it. Until now both stopped
 * here.
 *
 * The walk-up loop is the part to get right. Most cases return, but Assignment
 * does not always: an assignment whose parent is another assignment (a chain,
 * `a = b = c`) breaks out of the switch and continues walking up, so the whole
 * chain is evaluated from its outermost node rather than from the middle.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// EvaluateTypesForStatement corresponds to evaluateTypesForStatement.
func (e *typeEvaluator) EvaluateTypesForStatement(node parser.ParseNode) {
	e.initializePrefetchedTypes(node)

	for curNode := node; curNode != nil; curNode = curNode.NodeBase().Parent {
		switch n := curNode.(type) {
		case *parser.AssignmentNode:
			// The original's comment: see if the assignment is part of a chain
			// of assignments. If so, evaluate the entire chain.
			if !isRightExprOfAssignmentChain(n) {
				e.evaluateTypesForAssignmentStatement(n)
				return
			}
			// Falls through to the walk-up: this is the one case that does not
			// return.

		case *parser.TypeAliasNode:
			e.getTypeOfTypeAlias(n)
			return

		case *parser.AssignmentExpressionNode:
			e.evaluateTypesForExpressionInContext(n)
			return

		case *parser.AugmentedAssignmentNode:
			e.evaluateTypesForAugmentedAssignment(n)
			return

		case *parser.ClassNode:
			e.GetTypeOfClass(n)
			return

		case *parser.ParameterNode:
			e.EvaluateTypeOfParam(n)
			return

		case *parser.LambdaNode:
			e.evaluateTypesForExpressionInContext(n)
			return

		case *parser.FunctionNode:
			e.GetTypeOfFunction(n)
			return

		case *parser.ForNode:
			e.evaluateTypesForForStatement(n)
			return

		case *parser.ExceptNode:
			e.evaluateTypesForExceptStatement(n)
			return

		case *parser.WithItemNode:
			e.evaluateTypesForWithStatement(n)
			return

		case *parser.ComprehensionForNode:
			e.evaluateComprehensionForNode(n)
			return

		case *parser.ImportAsNode:
			e.evaluateTypesForImportAs(n)
			return

		case *parser.ImportFromAsNode:
			e.evaluateTypesForImportFromAs(n)
			return

		case *parser.ImportFromNode:
			e.evaluateTypesForImportFrom(n)
			return

		case *parser.CaseNode:
			e.evaluateTypesForCaseStatement(n)
			return
		}
	}

	common.Fail("Unexpected statement")
}

// isRightExprOfAssignmentChain is the original's isInAssignmentChain test: the
// node is the right-hand side of an enclosing assignment, so the chain should be
// evaluated from its outermost node instead.
func isRightExprOfAssignmentChain(node *parser.AssignmentNode) bool {
	switch parent := node.NodeBase().Parent.(type) {
	case *parser.AssignmentNode:
		return parser.ParseNode(parent.D.RightExpr) == parser.ParseNode(node)
	case *parser.AssignmentExpressionNode:
		return parser.ParseNode(parent.D.RightExpr) == parser.ParseNode(node)
	case *parser.AugmentedAssignmentNode:
		return parser.ParseNode(parent.D.RightExpr) == parser.ParseNode(node)
	}
	return false
}

// evaluateComprehensionForNode is the original's ComprehensionFor arm, which is
// the only one with a body rather than a single call.
func (e *typeEvaluator) evaluateComprehensionForNode(curNode *parser.ComprehensionForNode) {
	comprehension, ok := curNode.NodeBase().Parent.(*parser.ComprehensionNode)
	if !ok {
		// The original asserts the parent is a Comprehension.
		common.Fail("ComprehensionFor node has no Comprehension parent")
		return
	}

	if parser.ParseNode(comprehension.D.Expr) == parser.ParseNode(curNode) {
		e.evaluateTypesForExpressionInContext(comprehension)
		return
	}

	// The original's comment: evaluate the individual iterations starting with
	// the first up to the curNode.
	for _, forIfNode := range comprehension.D.ForIfNodes {
		e.evaluateComprehensionForIf(forIfNode)
		if parser.ParseNode(forIfNode) == parser.ParseNode(curNode) {
			break
		}
	}
}

/*
 * The five arms that are not yet ported. Each is a separate unit of work and
 * records itself, so the frontier ranks the statement kinds.
 */

// evaluateTypesForForStatement corresponds to the function of the same name.

// evaluateTypesForExceptStatement corresponds to the function of the same name.

// evaluateTypesForWithStatement corresponds to the function of the same name.

// evaluateComprehensionForIf corresponds to the function of the same name. Its
// parameter is the `ComprehensionForNode | ComprehensionIfNode` union, which is
// ComprehensionForIfNode here.
// evaluateTypesForImportFrom corresponds to the function of the same name. The
// other two import arms already had stubs from the context walk; this one is
// reached only from here.

// evaluateTypesForCaseStatement corresponds to the function of the same name.
func (e *typeEvaluator) evaluateTypesForCaseStatement(_ *parser.CaseNode) {
	e.unported("evaluateTypesForCaseStatement")
}
