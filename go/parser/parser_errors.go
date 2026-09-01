/*
 * parser_errors.go
 *
 * Expression-level error recovery, transliterated from parser/parser.ts
 * (pyright 1.1.412).
 */

package parser

import "github.com/microsoft/pyright/go/common"

// handleExpressionParseError corresponds to _handleExpressionParseError().
//
// It allocates a dummy "error expression" and consumes the remainder of the
// tokens on the line for error recovery. A partially-completed child node can
// be passed to help the completion provider determine what to do.
//
// Pass nil for targetToken, childNode and additionalStopTokens to omit them.
func (p *Parser) handleExpressionParseError(
	category ErrorExpressionCategory,
	errorMsg string,
	targetToken Token,
	childNode ExpressionNode,
	additionalStopTokens []TokenType,
) *ErrorNode {
	errorRange := p.peekToken(0).GetRange()
	if targetToken != nil {
		errorRange = targetToken.GetRange()
	}
	p.addSyntaxError(errorMsg, errorRange)

	stopTokens := []TokenType{TokenTypeNewLine}
	if additionalStopTokens != nil {
		stopTokens = append(stopTokens, additionalStopTokens...)
	}

	// Using a token that is not included in the error node creates problems.
	// Sibling nodes in the parse tree shouldn't overlap each other.
	nextToken := p.peekToken(0)
	atStopToken := false
	for _, k := range stopTokens {
		if nextToken.GetType() == k {
			atStopToken = true
			break
		}
	}

	var initialRange common.TextRange
	if atStopToken {
		// `targetToken ?? childNode ?? TextRange.create(nextToken.start, 0)`
		switch {
		case targetToken != nil:
			initialRange = targetToken.GetRange()
		case childNode != nil:
			initialRange = childNode.GetRange()
		default:
			initialRange = common.NewTextRange(nextToken.GetRange().Start, 0)
		}
	} else {
		initialRange = nextToken.GetRange()
	}

	expr := NewErrorNode(initialRange, category, childNode, nil)
	p.consumeTokensUntilType(stopTokens)

	return expr
}
