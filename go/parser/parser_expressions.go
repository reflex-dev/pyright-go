/*
 * parser_expressions.go
 *
 * The binary/unary operator precedence chain, transliterated from
 * parser/parser.ts (pyright 1.1.412).
 *
 * This is the linear part of the expression grammar:
 *
 *   or_test -> and_test -> not_test -> comparison -> bitwise_or -> bitwise_xor
 *   -> bitwise_and -> shift -> arithmetic -> term -> factor -> atom_expression
 *
 * Each level parses the next one down and folds left, so the chain can be
 * transliterated faithfully on its own. It bottoms out at parseAtomExpression,
 * which is where the grammar stops being linear (calls, subscripts, tuples,
 * dict/set displays, comprehensions, f-strings); that half lives in
 * parser_trailers.go.
 */

package parser

import (
	"github.com/microsoft/pyright/go/localization"
)

// or_test: and_test ('or' and_test)*
func (p *Parser) parseOrTest() ExpressionNode {
	leftExpr := p.parseAndTest()
	if leftExpr.GetNodeType() == ParseNodeTypeError {
		return leftExpr
	}

	for {
		peekToken := p.peekToken(0)
		if !p.consumeTokenIfKeyword(KeywordTypeOr) {
			break
		}
		rightExpr := p.parseAndTest()
		leftExpr = p.createBinaryOperationNode(leftExpr, rightExpr, peekToken, OperatorTypeOr)
	}

	return leftExpr
}

// and_test: not_test ('and' not_test)*
func (p *Parser) parseAndTest() ExpressionNode {
	leftExpr := p.parseNotTest()
	if leftExpr.GetNodeType() == ParseNodeTypeError {
		return leftExpr
	}

	for {
		peekToken := p.peekToken(0)
		if !p.consumeTokenIfKeyword(KeywordTypeAnd) {
			break
		}
		rightExpr := p.parseNotTest()
		leftExpr = p.createBinaryOperationNode(leftExpr, rightExpr, peekToken, OperatorTypeAnd)
	}

	return leftExpr
}

// not_test: 'not' not_test | comparison
func (p *Parser) parseNotTest() ExpressionNode {
	notToken := p.peekToken(0)
	if p.consumeTokenIfKeyword(KeywordTypeNot) {
		notExpr := p.parseNotTest()
		return p.createUnaryOperationNode(notToken, notExpr, OperatorTypeNot)
	}

	return p.parseComparison()
}

// comparison: expr (comp_op expr)*
// comp_op: '<'|'>'|'=='|'>='|'<='|'<>'|'!='|'in'|'not' 'in'|'is'|'is' 'not'
func (p *Parser) parseComparison() ExpressionNode {
	leftExpr := p.parseBitwiseOrExpression()
	if leftExpr.GetNodeType() == ParseNodeTypeError {
		return leftExpr
	}

	for {
		var comparisonOperator OperatorType
		haveOperator := false
		peekToken := p.peekToken(0)

		if op, ok := p.peekOperatorType(); ok && IsOperatorComparison(op) {
			comparisonOperator = op
			haveOperator = true
			if comparisonOperator == OperatorTypeLessOrGreaterThan {
				p.addSyntaxError(localization.LocMessage.OperatorLessOrGreaterDeprecated(), peekToken.GetRange())
				comparisonOperator = OperatorTypeNotEquals
			}
			p.getNextToken()
		} else if p.consumeTokenIfKeyword(KeywordTypeIn) {
			comparisonOperator = OperatorTypeIn
			haveOperator = true
		} else if p.consumeTokenIfKeyword(KeywordTypeIs) {
			if p.consumeTokenIfKeyword(KeywordTypeNot) {
				comparisonOperator = OperatorTypeIsNot
			} else {
				comparisonOperator = OperatorTypeIs
			}
			haveOperator = true
		} else if kw, ok := p.peekKeywordType(); ok && kw == KeywordTypeNot {
			tokenAfterNot := p.peekToken(1)
			if tokenAfterNot.GetType() == TokenTypeKeyword &&
				tokenAfterNot.(*KeywordToken).KeywordType == KeywordTypeIn {
				p.getNextToken()
				p.getNextToken()
				comparisonOperator = OperatorTypeNotIn
				haveOperator = true
			}
		}

		if !haveOperator {
			break
		}

		rightExpr := p.parseComparison()
		leftExpr = p.createBinaryOperationNode(leftExpr, rightExpr, peekToken, comparisonOperator)
	}

	return leftExpr
}

// expr: xor_expr ('|' xor_expr)*
func (p *Parser) parseBitwiseOrExpression() ExpressionNode {
	leftExpr := p.parseBitwiseXorExpression()
	if leftExpr.GetNodeType() == ParseNodeTypeError {
		return leftExpr
	}

	for {
		peekToken := p.peekToken(0)
		if !p.consumeTokenIfOperator(OperatorTypeBitwiseOr) {
			break
		}
		rightExpr := p.parseBitwiseXorExpression()
		leftExpr = p.createBinaryOperationNode(leftExpr, rightExpr, peekToken, OperatorTypeBitwiseOr)
	}

	return leftExpr
}

// xor_expr: and_expr ('^' and_expr)*
func (p *Parser) parseBitwiseXorExpression() ExpressionNode {
	leftExpr := p.parseBitwiseAndExpression()
	if leftExpr.GetNodeType() == ParseNodeTypeError {
		return leftExpr
	}

	for {
		peekToken := p.peekToken(0)
		if !p.consumeTokenIfOperator(OperatorTypeBitwiseXor) {
			break
		}
		rightExpr := p.parseBitwiseAndExpression()
		leftExpr = p.createBinaryOperationNode(leftExpr, rightExpr, peekToken, OperatorTypeBitwiseXor)
	}

	return leftExpr
}

// and_expr: shift_expr ('&' shift_expr)*
func (p *Parser) parseBitwiseAndExpression() ExpressionNode {
	leftExpr := p.parseShiftExpression()
	if leftExpr.GetNodeType() == ParseNodeTypeError {
		return leftExpr
	}

	for {
		peekToken := p.peekToken(0)
		if !p.consumeTokenIfOperator(OperatorTypeBitwiseAnd) {
			break
		}
		rightExpr := p.parseShiftExpression()
		leftExpr = p.createBinaryOperationNode(leftExpr, rightExpr, peekToken, OperatorTypeBitwiseAnd)
	}

	return leftExpr
}

// shift_expr: arith_expr (('<<'|'>>') arith_expr)*
func (p *Parser) parseShiftExpression() ExpressionNode {
	leftExpr := p.parseArithmeticExpression()
	if leftExpr.GetNodeType() == ParseNodeTypeError {
		return leftExpr
	}

	peekToken := p.peekToken(0)
	nextOperator, haveOperator := p.peekOperatorType()
	for haveOperator && (nextOperator == OperatorTypeLeftShift || nextOperator == OperatorTypeRightShift) {
		p.getNextToken()
		rightExpr := p.parseArithmeticExpression()
		leftExpr = p.createBinaryOperationNode(leftExpr, rightExpr, peekToken, nextOperator)
		peekToken = p.peekToken(0)
		nextOperator, haveOperator = p.peekOperatorType()
	}

	return leftExpr
}

// arith_expr: term (('+'|'-') term)*
func (p *Parser) parseArithmeticExpression() ExpressionNode {
	leftExpr := p.parseArithmeticTerm()
	if leftExpr.GetNodeType() == ParseNodeTypeError {
		return leftExpr
	}

	peekToken := p.peekToken(0)
	nextOperator, haveOperator := p.peekOperatorType()
	for haveOperator && (nextOperator == OperatorTypeAdd || nextOperator == OperatorTypeSubtract) {
		p.getNextToken()
		rightExpr := p.parseArithmeticTerm()
		// Note that, unlike the other levels, this one bails out on a failed
		// right operand rather than folding it in.
		if rightExpr.GetNodeType() == ParseNodeTypeError {
			return rightExpr
		}

		leftExpr = p.createBinaryOperationNode(leftExpr, rightExpr, peekToken, nextOperator)
		peekToken = p.peekToken(0)
		nextOperator, haveOperator = p.peekOperatorType()
	}

	return leftExpr
}

// term: factor (('*'|'@'|'/'|'%'|'//') factor)*
func (p *Parser) parseArithmeticTerm() ExpressionNode {
	leftExpr := p.parseArithmeticFactor()
	if leftExpr.GetNodeType() == ParseNodeTypeError {
		return leftExpr
	}

	peekToken := p.peekToken(0)
	nextOperator, haveOperator := p.peekOperatorType()
	for haveOperator && (nextOperator == OperatorTypeMultiply ||
		nextOperator == OperatorTypeMatrixMultiply ||
		nextOperator == OperatorTypeDivide ||
		nextOperator == OperatorTypeMod ||
		nextOperator == OperatorTypeFloorDivide) {
		p.getNextToken()
		rightExpr := p.parseArithmeticFactor()
		leftExpr = p.createBinaryOperationNode(leftExpr, rightExpr, peekToken, nextOperator)
		peekToken = p.peekToken(0)
		nextOperator, haveOperator = p.peekOperatorType()
	}

	return leftExpr
}

// factor: ('+'|'-'|'~') factor | power
// power: atom_expr ['**' factor]
func (p *Parser) parseArithmeticFactor() ExpressionNode {
	nextToken := p.peekToken(0)
	nextOperator, haveOperator := p.peekOperatorType()
	if haveOperator && (nextOperator == OperatorTypeAdd ||
		nextOperator == OperatorTypeSubtract ||
		nextOperator == OperatorTypeBitwiseInvert) {
		p.getNextToken()
		expression := p.parseArithmeticFactor()
		return p.createUnaryOperationNode(nextToken, expression, nextOperator)
	}

	leftExpr := p.parseAtomExpression()
	if leftExpr.GetNodeType() == ParseNodeTypeError {
		return leftExpr
	}

	peekToken := p.peekToken(0)
	if p.consumeTokenIfOperator(OperatorTypePower) {
		rightExpr := p.parseArithmeticFactor()
		return p.createBinaryOperationNode(leftExpr, rightExpr, peekToken, OperatorTypePower)
	}

	return leftExpr
}

// createBinaryOperationNode corresponds to _createBinaryOperationNode().
func (p *Parser) createBinaryOperationNode(leftExpression, rightExpression ExpressionNode, operatorToken Token, operator OperatorType) ExpressionNode {
	binaryNode := NewBinaryOperationNode(leftExpression, rightExpression, operatorToken, operator)

	// Determine if we're exceeding the max parse depth. If so, replace
	// the subnode with an error node. Otherwise we risk crashing in the binder
	// or type evaluator.
	leftMaxDepth := p.maxChildDepthMap[leftExpression.NodeBase().ID]
	rightMaxDepth := p.maxChildDepthMap[rightExpression.NodeBase().ID]

	if leftMaxDepth >= maxChildNodeDepth || rightMaxDepth >= maxChildNodeDepth {
		p.addSyntaxError(localization.LocMessage.MaxParseDepthExceeded(), binaryNode.GetRange())
		return NewErrorNode(binaryNode.GetRange(), ErrorExpressionCategoryMaxDepthExceeded, nil, nil)
	}

	p.maxChildDepthMap[binaryNode.ID] = max(leftMaxDepth, rightMaxDepth) + 1
	return binaryNode
}

// createUnaryOperationNode corresponds to _createUnaryOperationNode().
func (p *Parser) createUnaryOperationNode(operatorToken Token, expression ExpressionNode, operator OperatorType) ExpressionNode {
	unaryNode := NewUnaryOperationNode(operatorToken, expression, operator)

	// Determine if we're exceeding the max parse depth. If so, replace
	// the subnode with an error node. Otherwise we risk crashing in the binder
	// or type evaluator.
	maxDepth := p.maxChildDepthMap[expression.NodeBase().ID]
	if maxDepth >= maxChildNodeDepth {
		p.addSyntaxError(localization.LocMessage.MaxParseDepthExceeded(), unaryNode.GetRange())
		return NewErrorNode(unaryNode.GetRange(), ErrorExpressionCategoryMaxDepthExceeded, nil, nil)
	}

	p.maxChildDepthMap[unaryNode.ID] = maxDepth + 1
	return unaryNode
}
