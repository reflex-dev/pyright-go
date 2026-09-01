/*
 * parser_atoms.go
 *
 * The atom grammar, transliterated from parser/parser.ts (pyright 1.1.412).
 *
 * parseAtom itself is complete. The branches that open a bracketed or quoted
 * construct delegate to methods that are not transliterated yet
 * (parser_pending.go); the simple atoms -- ellipsis, numbers, identifiers,
 * True/False/None/__debug__, and keywords reinterpreted as identifiers -- are
 * fully working.
 */

package parser

import (
	"github.com/microsoft/pyright/go/localization"
)

// parseAtom corresponds to _parseAtom().
func (p *Parser) parseAtom() ExpressionNode {
	nextToken := p.peekToken(0)

	if nextToken.GetType() == TokenTypeEllipsis {
		return NewEllipsisNode(p.getNextToken().GetRange())
	}

	if nextToken.GetType() == TokenTypeNumber {
		return NewNumberNode(p.getNextToken().(*NumberToken))
	}

	if nextToken.GetType() == TokenTypeIdentifier {
		return NewNameNode(p.getNextToken().(*IdentifierToken))
	}

	if nextToken.GetType() == TokenTypeString || nextToken.GetType() == TokenTypeFStringStart {
		return p.parseStringList()
	}

	if nextToken.GetType() == TokenTypeBacktick {
		p.getNextToken()

		// Atoms with backticks are no longer allowed in Python 3.x, but they
		// were a thing in Python 2.x. We'll parse them to improve parse recovery
		// and emit an error.
		p.addSyntaxError(localization.LocMessage.BackticksIllegal(), nextToken.GetRange())

		expressionNode := p.parseTestListAsExpression(
			ErrorExpressionCategoryMissingExpression,
			func() string { return localization.LocMessage.ExpectedExpr() },
		)

		p.consumeTokenIfType(TokenTypeBacktick)
		return expressionNode
	}

	if nextToken.GetType() == TokenTypeOpenParenthesis {
		possibleTupleNode := p.parseTupleAtom()

		switch node := possibleTupleNode.(type) {
		case *UnaryOperationNode:
			// Mark binary expressions as parenthesized so we don't attempt
			// to use comparison chaining, which isn't appropriate when the
			// expression is parenthesized. Unary and await expressions
			// are also marked to be able to display them unambiguously.
			node.D.HasParens = true
		case *AwaitNode:
			node.D.HasParens = true
		case *BinaryOperationNode:
			node.D.HasParens = true
		case *StringListNode:
			node.D.HasParens = true
		case *ComprehensionNode:
			node.D.HasParens = true
		case *AssignmentExpressionNode:
			node.D.HasParens = true
		}

		return possibleTupleNode
	} else if nextToken.GetType() == TokenTypeOpenBracket {
		return p.parseListAtom()
	} else if nextToken.GetType() == TokenTypeOpenCurlyBrace {
		return p.parseDictionaryOrSetAtom()
	}

	if nextToken.GetType() == TokenTypeKeyword {
		keywordToken := nextToken.(*KeywordToken)
		if keywordToken.KeywordType == KeywordTypeFalse ||
			keywordToken.KeywordType == KeywordTypeTrue ||
			keywordToken.KeywordType == KeywordTypeDebug ||
			keywordToken.KeywordType == KeywordTypeNone {
			return NewConstantNode(p.getNextToken().(*KeywordToken))
		}

		// Make an identifier out of the keyword.
		keywordAsIdentifier := p.getTokenIfIdentifier()
		if keywordAsIdentifier != nil {
			return NewNameNode(keywordAsIdentifier)
		}
	}

	return p.handleExpressionParseError(
		ErrorExpressionCategoryMissingExpression,
		localization.LocMessage.ExpectedExpr(),
		nil, nil, nil,
	)
}
