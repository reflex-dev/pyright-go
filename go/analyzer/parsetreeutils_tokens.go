/*
 * parsetreeutils_tokens.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Token-level helpers from analyzer/parseTreeUtils.ts (pyright 1.1.412), lines
 * 1738-1990 and 2718-2756.
 *
 * The original's out-of-range accesses through TextRangeCollection.getItemAt
 * throw; the Go port panics through common.Fail at the same points, so the
 * behavior matches. Two of those are latent bugs and are marked.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// CallNodeAndActiveParamIndex corresponds to the object literal returned by
// getCallNodeAndActiveParamIndex.
type CallNodeAndActiveParamIndex struct {
	CallNode     *parser.CallNode
	ActiveIndex  int
	ActiveOrFake bool
}

// GetCallNodeAndActiveParamIndex corresponds to the function of the same name.
// The TypeScript returns `undefined` when there is no enclosing call; that
// becomes a nil result here.
func GetCallNodeAndActiveParamIndex(
	node parser.ParseNode,
	insertionOffset int,
	tokens *common.TextRangeCollection[parser.Token],
) *CallNodeAndActiveParamIndex {
	// Find the call node that contains the specified node.
	curNode := node
	var callNode *parser.CallNode

	for curNode != nil {
		// make sure we only look at callNodes when we are inside their arguments
		if call, ok := curNode.(*parser.CallNode); ok {
			if isOffsetInsideCallArgs(tokens, call, insertionOffset) {
				callNode = call
				break
			}
		}
		curNode = curNode.NodeBase().Parent
	}

	// The original also tests `!callNode.d.args`, which is never falsy because
	// an empty array is truthy in JavaScript and the field is always assigned.
	if callNode == nil {
		return nil
	}

	endPosition := nodeRange(callNode).End()
	if insertionOffset > endPosition {
		return nil
	}

	tokenAtEnd := GetTokenAt(tokens, endPosition-1)
	if insertionOffset == endPosition && tokenAtEnd != nil && tokenAtEnd.GetType() == parser.TokenTypeCloseParenthesis {
		return nil
	}

	addedActive := false
	activeIndex := -1
	activeOrFake := false

	for index, arg := range callNode.D.Args {
		if addedActive {
			break
		}

		// Calculate the argument's bounds including whitespace and colons.
		start := arg.NodeBase().Start
		startTokenIndex := tokens.GetItemAtPosition(start)
		if startTokenIndex >= 0 {
			// startTokenIndex == 0 would index -1 here and throw in the
			// original too; reproduced rather than guarded.
			start = tokens.GetItemAt(startTokenIndex - 1).GetRange().End()
		}

		end := nodeRange(arg).End()
		endTokenIndex := tokens.GetItemAtPosition(end)
		if endTokenIndex >= 0 {
			// Find the true end of the argument by searching for the
			// terminating comma or parenthesis.
			for i := endTokenIndex; i < tokens.Count(); i++ {
				tok := tokens.GetItemAt(i)

				switch tok.GetType() {
				case parser.TokenTypeComma, parser.TokenTypeCloseParenthesis:
				default:
					continue
				}

				end = tok.GetRange().End()
				break
			}
		}

		// If no terminating comma or close paren was found (e.g., an incomplete
		// call with no closing parenthesis), extend end past the call boundary
		// so the argument is still considered active at the cursor position.
		if end == nodeRange(arg).End() {
			end = endPosition + 1
		}

		if insertionOffset < end {
			activeIndex = index
			activeOrFake = insertionOffset >= start
			addedActive = true
		}
	}

	if !addedActive {
		activeIndex = len(callNode.D.Args) + 1
	}

	return &CallNodeAndActiveParamIndex{
		CallNode:     callNode,
		ActiveIndex:  activeIndex,
		ActiveOrFake: activeOrFake,
	}
}

// isOffsetInsideCallArgs corresponds to the nested function of the same name.
func isOffsetInsideCallArgs(
	tokens *common.TextRangeCollection[parser.Token],
	node *parser.CallNode,
	offset int,
) bool {
	leftExprRange := nodeRange(node.D.LeftExpr)
	argumentStart := leftExprRange.Start
	if leftExprRange.Length > 0 {
		argumentStart = leftExprRange.End() - 1
	}

	// Handle obvious case first.
	callEndOffset := nodeRange(node).End()
	if offset < argumentStart || callEndOffset < offset {
		return false
	}

	if len(node.D.Args) > 0 {
		start := node.D.Args[0].NodeBase().Start
		end := nodeRange(node.D.Args[len(node.D.Args)-1]).End()
		if start <= offset && offset < end {
			return true
		}
	}

	index := tokens.GetItemAtPosition(argumentStart)
	if index < 0 || tokens.Count() <= index {
		return true
	}

	// index may be the last token, in which case index+1 is out of range and
	// the original throws here too.
	nextToken := tokens.GetItemAt(index + 1)
	if nextToken.GetType() == parser.TokenTypeOpenParenthesis && offset < nextToken.GetRange().End() {
		// Position must be after '('.
		return false
	}

	return true
}

// GetTokenIndexAtLeft corresponds to getTokenIndexAtLeft. The TypeScript
// defaults includeWhitespace and includeZeroLengthToken to false.
func GetTokenIndexAtLeft(
	tokens *common.TextRangeCollection[parser.Token],
	position int,
	includeWhitespace bool,
	includeZeroLengthToken bool,
) int {
	index := tokens.GetItemAtPosition(position)
	if index < 0 {
		return -1
	}

	for i := index; i >= 0; i-- {
		token := tokens.GetItemAt(i)
		if !includeZeroLengthToken && token.GetRange().Length == 0 {
			continue
		}

		if !includeWhitespace && parser.IsWhitespaceToken(token) {
			continue
		}

		if token.GetRange().End() <= position {
			return i
		}
	}

	return -1
}

// GetTokenAtLeft corresponds to getTokenAtLeft. The TypeScript defaults
// includeWhitespace and includeZeroLengthToken to false.
func GetTokenAtLeft(
	tokens *common.TextRangeCollection[parser.Token],
	position int,
	includeWhitespace bool,
	includeZeroLengthToken bool,
) parser.Token {
	index := GetTokenIndexAtLeft(tokens, position, includeWhitespace, includeZeroLengthToken)
	if index < 0 {
		return nil
	}

	return tokens.GetItemAt(index)
}

// GetTokenIndexAfter corresponds to getTokenIndexAfter.
//
// The loop bound is `tokens.length`, which is the character span of the
// collection, not `tokens.count`, which is the number of tokens. When the
// predicate never matches, getItemAt runs past the end and throws.
// See UPSTREAM-BUGS.md #11.
func GetTokenIndexAfter(
	tokens *common.TextRangeCollection[parser.Token],
	position int,
	predicate func(t parser.Token) bool,
) int {
	index := tokens.GetItemAtPosition(position)
	if index < 0 {
		return -1
	}

	for i := index; i < tokens.Length(); i++ {
		token := tokens.GetItemAt(i)
		if predicate(token) {
			return i
		}
	}

	return -1
}

// GetTokenAfter corresponds to getTokenAfter.
func GetTokenAfter(
	tokens *common.TextRangeCollection[parser.Token],
	position int,
	predicate func(t parser.Token) bool,
) parser.Token {
	index := GetTokenIndexAfter(tokens, position, predicate)
	if index < 0 {
		return nil
	}

	return tokens.GetItemAt(index)
}

// GetTokenAtIndex corresponds to getTokenAtIndex.
func GetTokenAtIndex(tokens *common.TextRangeCollection[parser.Token], index int) parser.Token {
	if index < 0 {
		return nil
	}

	return tokens.GetItemAt(index)
}

// GetTokenAt corresponds to getTokenAt.
func GetTokenAt(tokens *common.TextRangeCollection[parser.Token], position int) parser.Token {
	return GetTokenAtIndex(tokens, tokens.GetItemAtPosition(position))
}

// GetTokenOverlapping corresponds to getTokenOverlapping.
func GetTokenOverlapping(tokens *common.TextRangeCollection[parser.Token], position int) parser.Token {
	index := GetIndexOfTokenOverlapping(tokens, position)
	return GetTokenAtIndex(tokens, index)
}

// GetIndexOfTokenOverlapping corresponds to getIndexOfTokenOverlapping.
func GetIndexOfTokenOverlapping(tokens *common.TextRangeCollection[parser.Token], position int) int {
	index := tokens.GetItemAtPosition(position)
	if index < 0 {
		return -1
	}

	token := tokens.GetItemAt(index)

	if token.GetRange().Overlaps(position) {
		return index
	}
	return -1
}

// GetCommentsAtTokenIndex corresponds to getCommentsAtTokenIndex. The
// TypeScript returns `undefined` when the index names no token; a nil slice
// stands in.
func GetCommentsAtTokenIndex(tokens *common.TextRangeCollection[parser.Token], index int) []*parser.Comment {
	token := GetTokenAtIndex(tokens, index)
	if token == nil {
		return nil
	}

	// If the preceding token has the same start offset (in other words, when
	// tokens have zero length and they're piled on top of each other) look back
	// through the tokens until we find the first token with that start offset.
	// That's where the comments (if any) will be.
	for precedingIndex := index - 1; precedingIndex >= 0; precedingIndex-- {
		precedingToken := GetTokenAtIndex(tokens, precedingIndex)
		if precedingToken != nil && precedingToken.GetRange().Start == token.GetRange().Start {
			token = precedingToken
		} else {
			break
		}
	}

	return token.GetComments()
}

// PrintParseNodeType corresponds to printParseNodeType.
func PrintParseNodeType(nodeType parser.ParseNodeType) string {
	if name, ok := parser.ParseNodeTypeNameMap[nodeType]; ok {
		return name
	}
	return "Unknown"
}

// GetPreviousNonWhitespaceToken corresponds to getPreviousNonWhitespaceToken.
func GetPreviousNonWhitespaceToken(
	tokens *common.TextRangeCollection[parser.Token],
	offset int,
) parser.Token {
	tokenIndex := tokens.GetItemAtPosition(offset)

	for tokenIndex >= 0 {
		token := tokens.GetItemAt(tokenIndex)
		if !parser.IsWhitespaceToken(token) {
			return token
		}

		tokenIndex--
	}

	return nil
}

// GetNextNonWhitespaceToken corresponds to getNextNonWhitespaceToken.
func GetNextNonWhitespaceToken(
	tokens *common.TextRangeCollection[parser.Token],
	offset int,
) parser.Token {
	return GetNextMatchingToken(tokens, offset, func(token parser.Token) bool {
		return !parser.IsWhitespaceToken(token)
	}, nil)
}

// GetNextMatchingToken corresponds to getNextMatchingToken. The TypeScript
// defaults exit to `() => false`; pass nil for that.
func GetNextMatchingToken(
	tokens *common.TextRangeCollection[parser.Token],
	offset int,
	match func(token parser.Token) bool,
	exit func(token parser.Token) bool,
) parser.Token {
	tokenIndex := tokens.GetItemAtPosition(offset) + 1
	for tokenIndex < tokens.Count() {
		token := tokens.GetItemAt(tokenIndex)
		if match(token) {
			return token
		}
		if exit != nil && exit(token) {
			return nil
		}
		tokenIndex++
	}

	return nil
}
