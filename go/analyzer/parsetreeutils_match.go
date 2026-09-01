/*
 * parsetreeutils_match.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Expression matching, containment predicates, docstrings and the two small
 * walker classes from analyzer/parseTreeUtils.ts (pyright 1.1.412), lines
 * 1259-1720.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// ContainsAwaitNode corresponds to containsAwaitNode.
func ContainsAwaitNode(node parser.ParseNode) bool {
	walker := &awaitNodeWalker{}
	walker.ParseTreeWalker = *NewParseTreeWalker(walker)
	walker.Walk(node)
	return walker.foundAwait
}

type awaitNodeWalker struct {
	ParseTreeWalker
	foundAwait bool
}

func (w *awaitNodeWalker) VisitAwait(node *parser.AwaitNode) bool {
	w.foundAwait = true
	return false
}

// CompareNameCallback corresponds to the optional compareName parameter of
// isMatchingExpression.
type CompareNameCallback func(reference *parser.NameNode, expression *parser.NameNode) bool

// IsMatchingExpression determines whether two expressions match. Names are
// compared by value only unless a compareName function is provided; in that
// case it is called to determine whether the two names match, which lets the
// caller distinguish between names that are identical but defined in different
// scopes. The TypeScript leaves compareName undefined by default; pass nil.
func IsMatchingExpression(
	reference parser.ExpressionNode,
	expression parser.ExpressionNode,
	compareName CompareNameCallback,
) bool {
	if referenceName, ok := reference.(*parser.NameNode); ok {
		var nameToCompare *parser.NameNode

		switch typed := expression.(type) {
		case *parser.NameNode:
			nameToCompare = typed
		case *parser.AssignmentExpressionNode:
			nameToCompare = typed.D.Name
		}

		if nameToCompare != nil {
			if referenceName.D.Value != nameToCompare.D.Value {
				return false
			}

			if compareName != nil {
				return compareName(referenceName, nameToCompare)
			}

			return true
		}

		return false
	}

	if referenceMember, ok := reference.(*parser.MemberAccessNode); ok {
		if expressionMember, ok := expression.(*parser.MemberAccessNode); ok {
			return IsMatchingExpression(referenceMember.D.LeftExpr, expressionMember.D.LeftExpr, nil) &&
				referenceMember.D.Member.D.Value == expressionMember.D.Member.D.Value
		}
		return false
	}

	referenceIndex, isReferenceIndex := reference.(*parser.IndexNode)
	expressionIndex, isExpressionIndex := expression.(*parser.IndexNode)
	if !isReferenceIndex || !isExpressionIndex {
		return false
	}

	if !IsMatchingExpression(referenceIndex.D.LeftExpr, expressionIndex.D.LeftExpr, nil) {
		return false
	}

	if len(expressionIndex.D.Items) != 1 ||
		expressionIndex.D.TrailingComma ||
		expressionIndex.D.Items[0].D.Name != nil ||
		expressionIndex.D.Items[0].D.ArgCategory != parser.ArgCategorySimple {
		return false
	}

	expr := referenceIndex.D.Items[0].D.ValueExpr
	subscriptNode := expressionIndex.D.Items[0].D.ValueExpr

	if number, ok := expr.(*parser.NumberNode); ok {
		subscriptNumber, ok := subscriptNode.(*parser.NumberNode)
		if !ok || subscriptNumber.D.IsImaginary || !subscriptNumber.D.IsInteger {
			return false
		}

		return numberValuesStrictEqual(number.D.Value, subscriptNumber.D.Value)
	}

	if unary, ok := expr.(*parser.UnaryOperationNode); ok && unary.D.Operator == parser.OperatorTypeSubtract {
		if number, ok := unary.D.Expr.(*parser.NumberNode); ok {
			subscriptUnary, ok := subscriptNode.(*parser.UnaryOperationNode)
			if !ok || subscriptUnary.D.Operator != parser.OperatorTypeSubtract {
				return false
			}
			subscriptNumber, ok := subscriptUnary.D.Expr.(*parser.NumberNode)
			if !ok || subscriptNumber.D.IsImaginary || !subscriptNumber.D.IsInteger {
				return false
			}

			return numberValuesStrictEqual(number.D.Value, subscriptNumber.D.Value)
		}
	}

	if referenceStringListNode, ok := expr.(*parser.StringListNode); ok {
		subscriptStringList, ok := subscriptNode.(*parser.StringListNode)
		if len(referenceStringListNode.D.Strings) == 1 && ok && len(subscriptStringList.D.Strings) == 1 {
			referenceString, isReferenceString := referenceStringListNode.D.Strings[0].(*parser.StringNode)
			subscriptString, isSubscriptString := subscriptStringList.D.Strings[0].(*parser.StringNode)
			if isReferenceString && isSubscriptString {
				return textEqual(referenceString.D.Value, subscriptString.D.Value)
			}
		}
	}

	return false
}

// IsPartialMatchingExpression corresponds to isPartialMatchingExpression.
func IsPartialMatchingExpression(reference parser.ExpressionNode, expression parser.ExpressionNode) bool {
	switch typed := reference.(type) {
	case *parser.MemberAccessNode:
		return IsMatchingExpression(typed.D.LeftExpr, expression, nil) ||
			IsPartialMatchingExpression(typed.D.LeftExpr, expression)
	case *parser.IndexNode:
		return IsMatchingExpression(typed.D.LeftExpr, expression, nil) ||
			IsPartialMatchingExpression(typed.D.LeftExpr, expression)
	}

	return false
}

// IsWithinDefaultParamInitializer corresponds to
// isWithinDefaultParamInitializer.
func IsWithinDefaultParamInitializer(node parser.ParseNode) bool {
	curNode := node
	var prevNode parser.ParseNode

	for curNode != nil {
		if param, ok := curNode.(*parser.ParameterNode); ok {
			if sameNode(prevNode, param.D.DefaultValue) {
				return true
			}
		}

		if isScopeBoundaryNode(curNode) {
			return false
		}

		prevNode = curNode
		curNode = curNode.NodeBase().Parent
	}

	return false
}

// IsWithinTypeAnnotation corresponds to isWithinTypeAnnotation.
func IsWithinTypeAnnotation(node parser.ParseNode, requireQuotedAnnotation bool) bool {
	curNode := node
	var prevNode parser.ParseNode
	isQuoted := false

	for curNode != nil {
		switch typed := curNode.(type) {
		case *parser.ParameterNode:
			if sameNode(prevNode, typed.D.Annotation) || sameNode(prevNode, typed.D.AnnotationComment) {
				return isQuoted || !requireQuotedAnnotation
			}

		case *parser.FunctionNode:
			if sameNode(prevNode, typed.D.ReturnAnnotation) {
				return isQuoted || !requireQuotedAnnotation
			}
			if sameNode(prevNode, typed.D.FuncAnnotationComment) {
				// Type comments are always considered forward declarations even
				// though they're not "quoted".
				return true
			}

		case *parser.TypeAnnotationNode:
			if sameNode(prevNode, typed.D.Annotation) {
				return isQuoted || !requireQuotedAnnotation
			}

		case *parser.AssignmentNode:
			if sameNode(prevNode, typed.D.AnnotationComment) {
				// Type comments are always considered forward declarations even
				// though they're not "quoted".
				return true
			}

		case *parser.StringListNode:
			if sameNode(prevNode, typed.D.Annotation) {
				isQuoted = true
			}
		}

		if isScopeBoundaryNode(curNode) {
			return false
		}

		prevNode = curNode
		curNode = curNode.NodeBase().Parent
	}

	return false
}

// IsWithinAnnotationComment corresponds to isWithinAnnotationComment.
func IsWithinAnnotationComment(node parser.ParseNode) bool {
	curNode := node
	var prevNode parser.ParseNode

	for curNode != nil {
		switch typed := curNode.(type) {
		case *parser.FunctionNode:
			if sameNode(prevNode, typed.D.FuncAnnotationComment) {
				// Type comments are always considered forward declarations even
				// though they're not "quoted".
				return true
			}

		case *parser.AssignmentNode:
			if sameNode(prevNode, typed.D.AnnotationComment) {
				// Type comments are always considered forward declarations even
				// though they're not "quoted".
				return true
			}
		}

		if isScopeBoundaryNode(curNode) {
			return false
		}

		prevNode = curNode
		curNode = curNode.NodeBase().Parent
	}

	return false
}

// IsWithinLoop corresponds to isWithinLoop.
//
// Note that the original's `case ParseNodeType.Module: break;` breaks out of the
// switch, not the loop, so a Module node does not stop the walk. Reproduced.
func IsWithinLoop(node parser.ParseNode) bool {
	curNode := node

	for curNode != nil {
		switch curNode.GetNodeType() {
		case parser.ParseNodeTypeFor, parser.ParseNodeTypeWhile:
			return true
		}

		curNode = curNode.NodeBase().Parent
	}

	return false
}

// IsWithinAssertExpression corresponds to isWithinAssertExpression.
func IsWithinAssertExpression(node parser.ParseNode) bool {
	curNode := node
	var prevNode parser.ParseNode

	for curNode != nil {
		if assertNode, ok := curNode.(*parser.AssertNode); ok {
			return sameNode(prevNode, assertNode.D.TestExpr)
		}

		prevNode = curNode
		curNode = curNode.NodeBase().Parent
	}

	return false
}

// GetDocString corresponds to getDocString. The TypeScript returns
// `string | undefined`; the second result reports whether a docstring was
// found, since an empty docstring is possible.
func GetDocString(statements []parser.StatementNode) (string, bool) {
	// See if the first statement in the suite is a triple-quote string.
	if len(statements) == 0 {
		return "", false
	}

	statementList, ok := statements[0].(*parser.StatementListNode)
	if !ok {
		return "", false
	}

	if !IsDocString(statementList) {
		return "", false
	}

	// It's up to the user to convert normalize/convert this as needed.
	strs := statementList.D.Statements[0].(*parser.StringListNode).D.Strings
	if len(strs) == 1 {
		return stringOrFormatValue(strs[0]), true
	}

	parts := make([]string, 0, len(strs))
	for _, s := range strs {
		parts = append(parts, stringOrFormatValue(s))
	}
	return strings.Join(parts, ""), true
}

// IsDocString corresponds to isDocString.
func IsDocString(statementList *parser.StatementListNode) bool {
	// If the first statement in the suite isn't a StringNode, assume there is no
	// docString.
	if len(statementList.D.Statements) == 0 {
		return false
	}
	stringList, ok := statementList.D.Statements[0].(*parser.StringListNode)
	if !ok {
		return false
	}

	// A docstring can consist of multiple joined strings in a single expression.
	if len(stringList.D.Strings) == 0 {
		return false
	}

	// Any f-strings invalidate the entire docstring.
	for _, s := range stringList.D.Strings {
		if s.GetNodeType() == parser.ParseNodeTypeFormatString {
			return false
		}
	}

	// It's up to the user to convert normalize/convert this as needed.
	return true
}

// IsAssignmentToDefaultsFollowingNamedTuple corresponds to the function of the
// same name.
//
// Sometimes a NamedTuple assignment statement is followed by a statement that
// looks like the following:
//
//	MyNamedTuple.__new__.__defaults__ = ...
//
// This pattern is commonly used to set the default values that are not
// specified in the original list.
func IsAssignmentToDefaultsFollowingNamedTuple(callNode parser.ParseNode) bool {
	if callNode.GetNodeType() != parser.ParseNodeTypeCall {
		return false
	}

	assignment, ok := callNode.NodeBase().Parent.(*parser.AssignmentNode)
	if !ok {
		return false
	}

	leftName, ok := assignment.D.LeftExpr.(*parser.NameNode)
	if !ok {
		return false
	}

	statementList, ok := assignment.NodeBase().Parent.(*parser.StatementListNode)
	if !ok {
		return false
	}

	namedTupleAssignedName := leftName.D.Value

	if len(statementList.D.Statements) == 0 ||
		parser.ParseNode(statementList.D.Statements[0]) != parser.ParseNode(assignment) {
		return false
	}

	moduleOrSuiteStatements, ok := statementsOfModuleOrSuite(statementList.NodeBase().Parent)
	if !ok {
		return false
	}

	statementIndex := -1
	for index, s := range moduleOrSuiteStatements {
		if parser.ParseNode(s) == parser.ParseNode(statementList) {
			statementIndex = index
			break
		}
	}

	if statementIndex < 0 {
		return false
	}
	statementIndex++

	for statementIndex < len(moduleOrSuiteStatements) {
		nextStatement, ok := moduleOrSuiteStatements[statementIndex].(*parser.StatementListNode)
		if !ok {
			break
		}

		var firstStatement parser.ParseNode
		if len(nextStatement.D.Statements) > 0 {
			firstStatement = nextStatement.D.Statements[0]
		}

		if firstStatement != nil && firstStatement.GetNodeType() == parser.ParseNodeTypeStringList {
			// Skip over comments
			statementIndex++
			continue
		}

		if assignNode, ok := firstStatement.(*parser.AssignmentNode); ok {
			if leftMember, ok := assignNode.D.LeftExpr.(*parser.MemberAccessNode); ok &&
				leftMember.D.Member.D.Value == "__defaults__" {
				if defaultTarget, ok := leftMember.D.LeftExpr.(*parser.MemberAccessNode); ok &&
					defaultTarget.D.Member.D.Value == "__new__" {
					if targetName, ok := defaultTarget.D.LeftExpr.(*parser.NameNode); ok &&
						targetName.D.Value == namedTupleAssignedName {
						return true
					}
				}
			}
		}

		break
	}

	return false
}

// NameNodeCallback corresponds to the callback NameNodeWalker takes.
type NameNodeCallback func(node *parser.NameNode, subscriptIndex *int, baseExpression parser.ExpressionNode)

// NameNodeWalker is a simple parse tree walker that calls a callback function
// for each NameNode it encounters.
type NameNodeWalker struct {
	ParseTreeWalker

	callback       NameNodeCallback
	subscriptIndex *int
	baseExpression parser.ExpressionNode
}

// NewNameNodeWalker corresponds to the constructor.
func NewNameNodeWalker(callback NameNodeCallback) *NameNodeWalker {
	walker := &NameNodeWalker{callback: callback}
	walker.ParseTreeWalker = *NewParseTreeWalker(walker)
	return walker
}

func (w *NameNodeWalker) VisitName(node *parser.NameNode) bool {
	w.callback(node, w.subscriptIndex, w.baseExpression)
	return true
}

func (w *NameNodeWalker) VisitIndex(node *parser.IndexNode) bool {
	w.Walk(node.D.LeftExpr)

	prevSubscriptIndex := w.subscriptIndex
	prevBaseExpression := w.baseExpression
	w.baseExpression = node.D.LeftExpr

	for index, item := range node.D.Items {
		i := index
		w.subscriptIndex = &i
		w.Walk(item)
	}

	w.subscriptIndex = prevSubscriptIndex
	w.baseExpression = prevBaseExpression

	return false
}

// CallNodeWalker calls a callback for each CallNode it encounters.
type CallNodeWalker struct {
	ParseTreeWalker

	callback func(node *parser.CallNode)
}

// NewCallNodeWalker corresponds to the constructor.
func NewCallNodeWalker(callback func(node *parser.CallNode)) *CallNodeWalker {
	walker := &CallNodeWalker{callback: callback}
	walker.ParseTreeWalker = *NewParseTreeWalker(walker)
	return walker
}

func (w *CallNodeWalker) VisitCall(node *parser.CallNode) bool {
	w.callback(node)
	return true
}

// GetEnclosingParam corresponds to getEnclosingParam.
func GetEnclosingParam(node parser.ParseNode) *parser.ParameterNode {
	curNode := node

	for curNode != nil {
		if param, ok := curNode.(*parser.ParameterNode); ok {
			return param
		}

		if _, ok := curNode.(*parser.FunctionNode); ok {
			return nil
		}

		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// isScopeBoundaryNode corresponds to the repeated
// `Lambda | Function | Class | Module` test that stops several of the walks
// above.
func isScopeBoundaryNode(node parser.ParseNode) bool {
	switch node.GetNodeType() {
	case parser.ParseNodeTypeLambda, parser.ParseNodeTypeFunction,
		parser.ParseNodeTypeClass, parser.ParseNodeTypeModule:
		return true
	}
	return false
}

// statementsOfModuleOrSuite corresponds to the original's narrowing of
// `statementList.parent` to Module | Suite before reading `d.statements`.
func statementsOfModuleOrSuite(node parser.ParseNode) ([]parser.StatementNode, bool) {
	switch typed := node.(type) {
	case *parser.ModuleNode:
		return typed.D.Statements, true
	case *parser.SuiteNode:
		return typed.D.Statements, true
	}
	return nil, false
}

// stringOrFormatValue reads `d.value` from either arm of the
// StringOrFormatStringNode union. FormatStringNode carries a dummy value for
// exactly this reason.
func stringOrFormatValue(node parser.StringOrFormatStringNode) string {
	switch typed := node.(type) {
	case *parser.StringNode:
		return typed.D.Value.String()
	case *parser.FormatStringNode:
		return typed.D.Value.String()
	}
	return ""
}

// textEqual corresponds to `===` between two JavaScript strings. common.Text is
// a UTF-16 code unit slice, so this compares element by element rather than
// going through String(), which cannot represent an unpaired surrogate.
func textEqual(a, b common.Text) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// numberValuesStrictEqual corresponds to `===` between two `number | bigint`
// values. JavaScript's === is false when one side is a number and the other a
// bigint, even when they are numerically equal.
func numberValuesStrictEqual(a, b parser.NumberValue) bool {
	if a.IsBigInt != b.IsBigInt {
		return false
	}
	if a.IsBigInt {
		if a.BigInt == nil || b.BigInt == nil {
			return a.BigInt == b.BigInt
		}
		return a.BigInt.Cmp(b.BigInt) == 0
	}
	return a.Float == b.Float
}

// sameNode compares prevNode against an optional child the way the original's
// `===` does. Two `undefined`s are equal in JavaScript, so a node that has
// neither is a match -- which is load-bearing rather than incidental: prevNode
// is undefined on the first iteration of every one of these walks, so
// `prevNode === curNode.d.returnAnnotation` is true for a function with no
// return annotation. See UPSTREAM-BUGS.md #12.
func sameNode[T parser.ParseNode](prevNode parser.ParseNode, child T) bool {
	return prevNode == childOrNil(child)
}
