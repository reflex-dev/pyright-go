/*
 * parser_exprlist.go
 *
 * Expression lists, tuples, ternaries and assignment (walrus) expressions,
 * transliterated from parser/parser.ts (pyright 1.1.412).
 *
 * This sits above the operator precedence chain in parser_expressions.go: it is
 * the layer that turns a comma-separated run of expressions into either a single
 * expression or a TupleNode, and the layer that handles `x if c else y` and
 * `x := y`.
 */

package parser

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// parseExpressionListGeneric corresponds to _parseExpressionListGeneric<T>().
//
// Go methods cannot carry type parameters, so this is a free function taking
// the receiver explicitly. Pass nil for terminalCheck or finalEntryCheck to get
// the defaults the TypeScript signature declares.
func parseExpressionListGeneric[T ParseNode](
	p *Parser,
	parser func() T,
	terminalCheck func() bool,
	finalEntryCheck func() bool,
) listResult[T] {
	if terminalCheck == nil {
		terminalCheck = p.isNextTokenNeverExpression
	}
	if finalEntryCheck == nil {
		finalEntryCheck = func() bool { return false }
	}

	trailingComma := false
	list := []T{}
	var parseError *ErrorNode

	for {
		if terminalCheck() {
			break
		}

		expr := parser()
		if expr.GetNodeType() == ParseNodeTypeError {
			// The TypeScript version casts here; the union guarantees that an
			// Error node type really is an ErrorNode.
			parseError = any(expr).(*ErrorNode)
			break
		}
		list = append(list, expr)

		// Should we stop without checking for a trailing comma?
		if finalEntryCheck() {
			break
		}

		if !p.consumeTokenIfType(TokenTypeComma) {
			trailingComma = false
			break
		}

		trailingComma = true
	}

	return listResult[T]{trailingComma: trailingComma, list: list, parseError: parseError}
}

// makeExpressionOrTuple corresponds to _makeExpressionOrTuple().
func (p *Parser) makeExpressionOrTuple(exprListResult listResult[ExpressionNode], enclosedInParens bool) ExpressionNode {
	// A single-element tuple with no trailing comma is simply an expression
	// that's surrounded by parens.
	if len(exprListResult.list) == 1 && !exprListResult.trailingComma {
		if exprListResult.list[0].GetNodeType() == ParseNodeTypeUnpack {
			p.addSyntaxError(localization.LocMessage.UnpackOperatorNotAllowed(), exprListResult.list[0].GetRange())
		}
		return exprListResult.list[0]
	}

	// To accommodate empty tuples ("()"), we will reach back to get
	// the opening parenthesis as the opening token.
	var tupleStartRange common.TextRange
	if len(exprListResult.list) > 0 {
		tupleStartRange = exprListResult.list[0].GetRange()
	} else {
		tupleStartRange = p.peekToken(-1).GetRange()
	}

	tupleNode := NewTupleNode(tupleStartRange, enclosedInParens)
	tupleNode.D.Items = exprListResult.list
	if len(exprListResult.list) > 0 {
		for _, expr := range exprListResult.list {
			setParent(expr, tupleNode)
		}
		ExtendRange(tupleNode, exprListResult.list[len(exprListResult.list)-1].GetRange())
	}

	return tupleNode
}

// parseExpressionListAsPossibleTuple corresponds to
// _parseExpressionListAsPossibleTuple().
func (p *Parser) parseExpressionListAsPossibleTuple(
	errorCategory ErrorExpressionCategory,
	getErrorString func() string,
	errorToken Token,
) ExpressionNode {
	if p.isNextTokenNeverExpression() {
		p.addSyntaxError(getErrorString(), errorToken.GetRange())
		return NewErrorNode(errorToken.GetRange(), errorCategory, nil, nil)
	}

	exprListResult := p.parseExpressionList(true /* allowStar */)
	if exprListResult.parseError != nil {
		return exprListResult.parseError
	}
	return p.makeExpressionOrTuple(exprListResult, false /* enclosedInParens */)
}

// parseTestListAsExpression corresponds to _parseTestListAsExpression().
func (p *Parser) parseTestListAsExpression(errorCategory ErrorExpressionCategory, getErrorString func() string) ExpressionNode {
	if p.isNextTokenNeverExpression() {
		return p.handleExpressionParseError(errorCategory, getErrorString(), nil, nil, nil)
	}

	exprListResult := p.parseTestExpressionList()
	if exprListResult.parseError != nil {
		return exprListResult.parseError
	}
	return p.makeExpressionOrTuple(exprListResult, false /* enclosedInParens */)
}

// parseTestOrStarListAsExpression corresponds to
// _parseTestOrStarListAsExpression().
func (p *Parser) parseTestOrStarListAsExpression(
	allowAssignmentExpression bool,
	allowMultipleUnpack bool,
	errorCategory ErrorExpressionCategory,
	getErrorString func() string,
) ExpressionNode {
	if p.isNextTokenNeverExpression() {
		return p.handleExpressionParseError(errorCategory, getErrorString(), nil, nil, nil)
	}

	exprListResult := p.parseTestOrStarExpressionList(allowAssignmentExpression, allowMultipleUnpack)
	if exprListResult.parseError != nil {
		return exprListResult.parseError
	}
	return p.makeExpressionOrTuple(exprListResult, false /* enclosedInParens */)
}

// parseExpressionList corresponds to _parseExpressionList().
func (p *Parser) parseExpressionList(allowStar bool) listResult[ExpressionNode] {
	return parseExpressionListGeneric(p, func() ExpressionNode { return p.parseExpression(allowStar) }, nil, nil)
}

// parseTestExpressionList corresponds to _parseTestExpressionList().
//
// testlist: test (',' test)* [',']
func (p *Parser) parseTestExpressionList() listResult[ExpressionNode] {
	return parseExpressionListGeneric(p, func() ExpressionNode {
		return p.parseTestExpression(false /* allowAssignmentExpression */)
	}, nil, nil)
}

// parseTestOrStarExpressionList corresponds to _parseTestOrStarExpressionList().
func (p *Parser) parseTestOrStarExpressionList(allowAssignmentExpression, allowMultipleUnpack bool) listResult[ExpressionNode] {
	exprListResult := parseExpressionListGeneric(p, func() ExpressionNode {
		return p.parseTestOrStarExpression(allowAssignmentExpression)
	}, nil, nil)

	if !allowMultipleUnpack && exprListResult.parseError == nil {
		sawStar := false
		for _, expr := range exprListResult.list {
			if expr.GetNodeType() == ParseNodeTypeUnpack {
				if sawStar {
					p.addSyntaxError(localization.LocMessage.DuplicateUnpack(), expr.GetRange())
					break
				}
				sawStar = true
			}
		}
	}

	return exprListResult
}

// parseExpression corresponds to _parseExpression().
//
// exp_or_star: expr | star_expr
// expr: xor_expr ('|' xor_expr)*
// star_expr: '*' expr
func (p *Parser) parseExpression(allowUnpack bool) ExpressionNode {
	startToken := p.peekToken(0)

	if allowUnpack && p.consumeTokenIfOperator(OperatorTypeMultiply) {
		return NewUnpackNode(startToken, p.parseExpression(false /* allowUnpack */))
	}

	return p.parseBitwiseOrExpression()
}

// parseTestOrStarExpression corresponds to _parseTestOrStarExpression().
//
// test_or_star: test | star_expr
func (p *Parser) parseTestOrStarExpression(allowAssignmentExpression bool) ExpressionNode {
	if op, ok := p.peekOperatorType(); ok && op == OperatorTypeMultiply {
		return p.parseExpression(true /* allowUnpack */)
	}

	return p.parseTestExpression(allowAssignmentExpression)
}

// parseTestExpression corresponds to _parseTestExpression().
//
// test: or_test ['if' or_test 'else' test] | lambdef
func (p *Parser) parseTestExpression(allowAssignmentExpression bool) ExpressionNode {
	if kw, ok := p.peekKeywordType(); ok && kw == KeywordTypeLambda {
		return p.parseLambdaExpression(true /* allowConditional */)
	}

	ifExpr := p.parseAssignmentExpression(!allowAssignmentExpression)
	if ifExpr.GetNodeType() == ParseNodeTypeError {
		return ifExpr
	}

	if !p.consumeTokenIfKeyword(KeywordTypeIf) {
		return ifExpr
	}

	testExpr := p.parseOrTest()
	if testExpr.GetNodeType() == ParseNodeTypeError {
		return testExpr
	}

	if !p.consumeTokenIfKeyword(KeywordTypeElse) {
		return NewTernaryNode(
			ifExpr,
			testExpr,
			p.handleExpressionParseError(
				ErrorExpressionCategoryMissingElse,
				localization.LocMessage.ExpectedElse(),
				nil, nil, nil,
			),
		)
	}

	elseExpr := p.parseTestExpression(true /* allowAssignmentExpression */)

	return NewTernaryNode(ifExpr, testExpr, elseExpr)
}

// parseAssignmentExpression corresponds to _parseAssignmentExpression().
//
// assign_expr: NAME := test
func (p *Parser) parseAssignmentExpression(disallowAssignmentExpression bool) ExpressionNode {
	leftExpr := p.parseOrTest()
	if leftExpr.GetNodeType() == ParseNodeTypeError {
		return leftExpr
	}

	if leftExpr.GetNodeType() != ParseNodeTypeName {
		return leftExpr
	}

	walrusToken := p.peekToken(0)
	if !p.consumeTokenIfOperator(OperatorTypeWalrus) {
		return leftExpr
	}

	if !p.assignmentExpressionsAllowed || disallowAssignmentExpression {
		p.addSyntaxError(localization.LocMessage.WalrusNotAllowed(), walrusToken.GetRange())
	}

	if p.getLanguageVersion().IsLessThan(common.PythonVersion3_8) {
		p.addSyntaxError(localization.LocMessage.WalrusIllegal(), walrusToken.GetRange())
	}

	rightExpr := p.parseTestExpression(false /* allowAssignmentExpression */)

	return NewAssignmentExpressionNode(leftExpr.(*NameNode), walrusToken, rightExpr)
}

// reportConditionalErrorForStarTupleElement corresponds to
// _reportConditionalErrorForStarTupleElement().
//
// Python 3.8 added support for star (unpack) expressions in tuples following a
// return or yield statement in cases where the tuple wasn't surrounded in
// parentheses.
func (p *Parser) reportConditionalErrorForStarTupleElement(possibleTupleExpr ExpressionNode, pythonVersion common.PythonVersion) {
	tupleNode, isTuple := possibleTupleExpr.(*TupleNode)
	if !isTuple {
		return
	}

	if tupleNode.D.HasParens {
		return
	}

	if p.parseOptions.PythonVersion.IsGreaterOrEqualTo(pythonVersion) {
		return
	}

	for _, expr := range tupleNode.D.Items {
		if expr.GetNodeType() == ParseNodeTypeUnpack {
			p.addSyntaxError(localization.LocMessage.UnpackTuplesIllegal(), expr.GetRange())
			return
		}
	}
}

// disallowAssignmentExpression corresponds to _disallowAssignmentExpression().
//
// Note that the TypeScript version restores the flag after the callback returns
// normally and does *not* use try/finally, so a throw leaves the flag cleared.
// This reproduces that: the restore is a plain statement, not a defer.
func (p *Parser) disallowAssignmentExpression(callback func()) {
	wasAllowed := p.assignmentExpressionsAllowed
	p.assignmentExpressionsAllowed = false

	callback()

	p.assignmentExpressionsAllowed = wasAllowed
}
