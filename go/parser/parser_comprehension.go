/*
 * parser_comprehension.go
 *
 * Comprehension clauses (`for ... in ... if ...`), transliterated from
 * parser/parser.ts (pyright 1.1.412).
 *
 * These are reached from every bracketed display and from the argument list,
 * and they recurse back into the expression grammar, so they belong with the
 * atom batch.
 */

package parser

import (
	"github.com/microsoft/pyright/go/localization"
)

// tryParseComprehension corresponds to _tryParseComprehension(). It returns nil
// where the TypeScript version returns undefined.
func (p *Parser) tryParseComprehension(target ParseNode, isGenerator bool) *ComprehensionNode {
	compFor := p.tryParseCompForStatement()

	if compFor == nil {
		return nil
	}

	if target.GetNodeType() == ParseNodeTypeUnpack {
		p.addSyntaxError(localization.LocMessage.UnpackIllegalInComprehension(), target.GetRange())
	} else if target.GetNodeType() == ParseNodeTypeDictionaryExpandEntry {
		p.addSyntaxError(localization.LocMessage.DictExpandIllegalInComprehension(), target.GetRange())
	}

	compNode := NewComprehensionNode(target, isGenerator)

	forIfList := []ComprehensionForIfNode{compFor}
	for {
		// `this._tryParseCompForStatement() || this._tryParseCompIfStatement()`
		var compIter ComprehensionForIfNode
		if forNode := p.tryParseCompForStatement(); forNode != nil {
			compIter = forNode
		} else if ifNode := p.tryParseCompIfStatement(); ifNode != nil {
			compIter = ifNode
		} else {
			break
		}

		setParent(compIter, compNode)
		forIfList = append(forIfList, compIter)
	}

	compNode.D.ForIfNodes = forIfList
	if len(forIfList) > 0 {
		for _, comp := range forIfList {
			setParent(comp, compNode)
		}
		ExtendRange(compNode, forIfList[len(forIfList)-1].GetRange())
	}
	return compNode
}

// tryParseCompForStatement corresponds to _tryParseCompForStatement().
//
// comp_for: ['async'] 'for' exprlist 'in' or_test [comp_iter]
func (p *Parser) tryParseCompForStatement() *ComprehensionForNode {
	startTokenKeywordType, haveKeyword := p.peekKeywordType()

	if haveKeyword && startTokenKeywordType == KeywordTypeAsync {
		nextToken := p.peekToken(1)
		if nextToken.GetType() != TokenTypeKeyword || nextToken.(*KeywordToken).KeywordType != KeywordTypeFor {
			return nil
		}
	} else if !haveKeyword || startTokenKeywordType != KeywordTypeFor {
		return nil
	}

	var asyncToken *KeywordToken
	if kw, ok := p.peekKeywordType(); ok && kw == KeywordTypeAsync {
		asyncToken = p.getKeywordToken(KeywordTypeAsync)
	}

	forToken := p.getKeywordToken(KeywordTypeFor)

	targetExpr := p.parseExpressionListAsPossibleTuple(
		ErrorExpressionCategoryMissingExpression,
		func() string { return localization.LocMessage.ExpectedExpr() },
		forToken,
	)
	var seqExpr ExpressionNode

	if !p.consumeTokenIfKeyword(KeywordTypeIn) {
		seqExpr = p.handleExpressionParseError(
			ErrorExpressionCategoryMissingIn,
			localization.LocMessage.ExpectedIn(),
			nil, nil, nil,
		)
	} else {
		p.disallowAssignmentExpression(func() {
			seqExpr = p.parseOrTest()
		})
	}

	// `asyncToken || forToken`
	var startToken Token = forToken
	if asyncToken != nil {
		startToken = asyncToken
	}

	compForNode := NewComprehensionForNode(startToken, targetExpr, seqExpr)

	if asyncToken != nil {
		compForNode.D.IsAsync = true
		compForNode.D.AsyncToken = asyncToken
	}

	return compForNode
}

// tryParseCompIfStatement corresponds to _tryParseCompIfStatement().
//
// comp_if: 'if' test_nocond [comp_iter]
// comp_iter: comp_for | comp_if
func (p *Parser) tryParseCompIfStatement() *ComprehensionIfNode {
	if kw, ok := p.peekKeywordType(); !ok || kw != KeywordTypeIf {
		return nil
	}

	ifToken := p.getKeywordToken(KeywordTypeIf)

	// `this._tryParseLambdaExpression() || this._parseAssignmentExpression(true)`
	var ifExpr ExpressionNode
	if lambda := p.tryParseLambdaExpression(true /* allowConditional */); lambda != nil {
		ifExpr = lambda
	} else {
		ifExpr = p.parseAssignmentExpression(true /* disallowAssignmentExpression */)
	}

	return NewComprehensionIfNode(ifToken, ifExpr)
}
