/*
 * parser_statements.go
 *
 * Transliterated from parser/parser.ts (pyright 1.1.412).
 *
 * These are the recursive-descent methods that do not reach back into the
 * expression grammar, so they can be landed -- and tested -- ahead of the rest
 * of parser.ts. See PORTING.md for the full remaining inventory.
 */

package parser

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// parsePassStatement corresponds to _parsePassStatement().
func (p *Parser) parsePassStatement() *PassNode {
	return NewPassNode(p.getKeywordToken(KeywordTypePass).GetRange())
}

// parseBreakStatement corresponds to _parseBreakStatement().
func (p *Parser) parseBreakStatement() *BreakNode {
	breakToken := p.getKeywordToken(KeywordTypeBreak)

	if !p.isInLoop {
		p.addSyntaxError(localization.LocMessage.BreakOutsideLoop(), breakToken.GetRange())
	} else if p.isInExceptionGroup {
		p.addSyntaxError(localization.LocMessage.BreakInExceptionGroup(), breakToken.GetRange())
	}

	if p.isInFinallyLoop && p.getLanguageVersion().IsGreaterOrEqualTo(common.PythonVersion3_14) {
		p.addSyntaxError(localization.LocMessage.FinallyBreak(), breakToken.GetRange())
	}

	return NewBreakNode(breakToken.GetRange())
}

// parseContinueStatement corresponds to _parseContinueStatement().
func (p *Parser) parseContinueStatement() *ContinueNode {
	continueToken := p.getKeywordToken(KeywordTypeContinue)

	if !p.isInLoop {
		p.addSyntaxError(localization.LocMessage.ContinueOutsideLoop(), continueToken.GetRange())
	} else if p.isInExceptionGroup {
		p.addSyntaxError(localization.LocMessage.ContinueInExceptionGroup(), continueToken.GetRange())
	}

	if p.isInFinallyLoop && p.getLanguageVersion().IsGreaterOrEqualTo(common.PythonVersion3_14) {
		p.addSyntaxError(localization.LocMessage.FinallyContinue(), continueToken.GetRange())
	}

	return NewContinueNode(continueToken.GetRange())
}

// parseNameList corresponds to _parseNameList().
func (p *Parser) parseNameList() []*NameNode {
	nameList := []*NameNode{}

	for {
		name := p.getTokenIfIdentifier()
		if name == nil {
			p.addSyntaxError(localization.LocMessage.ExpectedIdentifier(), p.peekToken(0).GetRange())
			break
		}

		nameList = append(nameList, NewNameNode(name))

		if !p.consumeTokenIfType(TokenTypeComma) {
			break
		}
	}

	return nameList
}

// parseGlobalStatement corresponds to _parseGlobalStatement().
func (p *Parser) parseGlobalStatement() *GlobalNode {
	globalToken := p.getKeywordToken(KeywordTypeGlobal)

	globalNode := NewGlobalNode(globalToken.GetRange())
	globalNode.D.Targets = p.parseNameList()
	if len(globalNode.D.Targets) > 0 {
		for _, name := range globalNode.D.Targets {
			setParent(name, globalNode)
		}
		ExtendRange(globalNode, globalNode.D.Targets[len(globalNode.D.Targets)-1].GetRange())
	}
	return globalNode
}

// parseNonlocalStatement corresponds to _parseNonlocalStatement().
func (p *Parser) parseNonlocalStatement() *NonlocalNode {
	nonlocalToken := p.getKeywordToken(KeywordTypeNonlocal)

	nonlocalNode := NewNonlocalNode(nonlocalToken.GetRange())
	nonlocalNode.D.Targets = p.parseNameList()
	if len(nonlocalNode.D.Targets) > 0 {
		for _, name := range nonlocalNode.D.Targets {
			setParent(name, nonlocalNode)
		}
		ExtendRange(nonlocalNode, nonlocalNode.D.Targets[len(nonlocalNode.D.Targets)-1].GetRange())
	}
	return nonlocalNode
}

// isNameOrMemberAccessExpression corresponds to
// _isNameOrMemberAccessExpression().
func (p *Parser) isNameOrMemberAccessExpression(expression ExpressionNode) bool {
	switch node := expression.(type) {
	case *NameNode:
		return true
	case *MemberAccessNode:
		return p.isNameOrMemberAccessExpression(node.D.LeftExpr)
	}

	return false
}

// isNextTokenNeverExpression corresponds to _isNextTokenNeverExpression().
func (p *Parser) isNextTokenNeverExpression() bool {
	nextToken := p.peekToken(0)
	switch nextToken.GetType() {
	case TokenTypeKeyword:
		if kw, ok := p.peekKeywordType(); ok {
			switch kw {
			case KeywordTypeFor, KeywordTypeIn, KeywordTypeIf:
				return true
			}
		}

	case TokenTypeOperator:
		if op, ok := p.peekOperatorType(); ok {
			switch op {
			case OperatorTypeAddEqual,
				OperatorTypeSubtractEqual,
				OperatorTypeMultiplyEqual,
				OperatorTypeDivideEqual,
				OperatorTypeModEqual,
				OperatorTypeBitwiseAndEqual,
				OperatorTypeBitwiseOrEqual,
				OperatorTypeBitwiseXorEqual,
				OperatorTypeLeftShiftEqual,
				OperatorTypeRightShiftEqual,
				OperatorTypePowerEqual,
				OperatorTypeFloorDivideEqual,
				OperatorTypeAssign:
				return true
			}
		}

	case TokenTypeIndent,
		TokenTypeDedent,
		TokenTypeNewLine,
		TokenTypeEndOfStream,
		TokenTypeSemicolon,
		TokenTypeCloseParenthesis,
		TokenTypeCloseBracket,
		TokenTypeCloseCurlyBrace,
		TokenTypeComma,
		TokenTypeColon,
		TokenTypeExclamationMark,
		TokenTypeFStringMiddle,
		TokenTypeFStringEnd:
		return true
	}

	return false
}
