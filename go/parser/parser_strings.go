/*
 * parser_strings.go
 *
 * String literals, f-strings and `# type:` annotation comments, transliterated
 * from parser/parser.ts (pyright 1.1.412).
 *
 * A string in a type-annotation position is re-parsed as an expression by a
 * fresh Parser, which is why this file reaches ParseTextExpression.
 */

package parser

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// reportStringTokenErrors corresponds to _reportStringTokenErrors(). Pass a nil
// unescapedResult to omit it.
func (p *Parser) reportStringTokenErrors(flags StringTokenFlags, r common.TextRange, unescapedResult *UnescapedString) {
	if flags&StringTokenFlagsUnterminated != 0 {
		p.addSyntaxError(localization.LocMessage.StringUnterminated(), r)
	}

	if unescapedResult != nil && unescapedResult.NonAsciiInBytes {
		p.addSyntaxError(localization.LocMessage.StringNonAsciiBytes(), r)
	}

	if flags&StringTokenFlagsFormat != 0 {
		if p.getLanguageVersion().IsLessThan(common.PythonVersion3_6) {
			p.addSyntaxError(localization.LocMessage.FormatStringIllegal(), r)
		}

		if flags&StringTokenFlagsBytes != 0 {
			p.addSyntaxError(localization.LocMessage.FormatStringBytes(), r)
		}

		if flags&StringTokenFlagsUnicode != 0 {
			p.addSyntaxError(localization.LocMessage.FormatStringUnicode(), r)
		}

		if flags&StringTokenFlagsTemplate != 0 {
			p.addSyntaxError(localization.LocMessage.FormatStringTemplate(), r)
		}
	}

	if flags&StringTokenFlagsTemplate != 0 {
		if p.getLanguageVersion().IsLessThan(common.PythonVersion3_14) {
			p.addSyntaxError(localization.LocMessage.TemplateStringIllegal(), r)
		}

		if flags&StringTokenFlagsBytes != 0 {
			p.addSyntaxError(localization.LocMessage.TemplateStringBytes(), r)
		}

		if flags&StringTokenFlagsUnicode != 0 {
			p.addSyntaxError(localization.LocMessage.TemplateStringUnicode(), r)
		}
	}
}

// makeStringNode corresponds to _makeStringNode().
func (p *Parser) makeStringNode(stringToken *StringToken) *StringNode {
	unescapedResult := GetUnescapedString(stringToken)
	p.reportStringTokenErrors(stringToken.Flags, stringToken.GetRange(), &unescapedResult)
	return NewStringNode(stringToken, unescapedResult.Value)
}

// parseFStringReplacementField corresponds to _parseFStringReplacementField().
//
// The three slices are appended to in place, so they are passed by pointer;
// TypeScript mutates the arrays the caller handed in.
func (p *Parser) parseFStringReplacementField(
	fieldExpressions *[]ExpressionNode,
	middleTokens *[]*FStringMiddleToken,
	formatExpressions *[]ExpressionNode,
	nestingDepth int,
) bool {
	nextToken := p.getNextToken()

	// The caller should have already confirmed that the next token is an open brace.
	common.Assert(nextToken.GetType() == TokenTypeOpenCurlyBrace, "")

	// Consume the expression.
	expr := p.tryParseYieldExpression()
	if expr == nil {
		expr = p.parseTestOrStarListAsExpression(
			true, /* allowAssignmentExpression */
			true, /* allowMultipleUnpack */
			ErrorExpressionCategoryMissingExpression,
			func() string { return localization.LocMessage.ExpectedExpr() },
		)
	}

	*fieldExpressions = append(*fieldExpressions, expr)

	if expr.GetNodeType() == ParseNodeTypeError {
		return false
	}

	// Consume an optional "=" token after the expression.
	nextToken = p.peekToken(0)
	if nextToken.GetType() == TokenTypeOperator && nextToken.(*OperatorToken).OperatorType == OperatorTypeAssign {
		// This feature requires Python 3.8 or newer.
		if p.parseOptions.PythonVersion.IsLessThan(common.PythonVersion3_8) {
			p.addSyntaxError(localization.LocMessage.FormatStringDebuggingIllegal(), nextToken.GetRange())
		}

		p.getNextToken()
		nextToken = p.peekToken(0)
	}

	// Consume an optional !r, !s, or !a token.
	if nextToken.GetType() == TokenTypeExclamationMark {
		p.getNextToken()
		nextToken = p.peekToken(0)

		if nextToken.GetType() != TokenTypeIdentifier {
			p.addSyntaxError(localization.LocMessage.FormatStringExpectedConversion(), nextToken.GetRange())
		} else {
			p.getNextToken()
			nextToken = p.peekToken(0)
		}
	}

	if nextToken.GetType() == TokenTypeColon {
		p.getNextToken()
		p.parseFStringFormatString(fieldExpressions, middleTokens, formatExpressions, nestingDepth)
		nextToken = p.peekToken(0)
	}

	if nextToken.GetType() != TokenTypeCloseCurlyBrace {
		p.addSyntaxError(localization.LocMessage.FormatStringUnterminated(), nextToken.GetRange())
		return false
	}
	p.getNextToken()

	// Indicate success.
	return true
}

// parseFStringFormatString corresponds to _parseFStringFormatString().
func (p *Parser) parseFStringFormatString(
	fieldExpressions *[]ExpressionNode,
	middleTokens *[]*FStringMiddleToken,
	formatExpressions *[]ExpressionNode,
	nestingDepth int,
) {
	for {
		nextToken := p.peekToken(0)

		if nextToken.GetType() == TokenTypeCloseCurlyBrace || nextToken.GetType() == TokenTypeFStringEnd {
			break
		}

		if nextToken.GetType() == TokenTypeFStringMiddle {
			p.getNextToken()
			continue
		}

		if nextToken.GetType() == TokenTypeOpenCurlyBrace {
			// The Python interpreter reports an error at the point where the
			// nesting level exceeds 1. Don't report the error again for deeper nestings.
			if nestingDepth == 2 {
				p.addSyntaxError(localization.LocMessage.FormatStringNestedFormatSpecifier(), nextToken.GetRange())
			}

			p.parseFStringReplacementField(fieldExpressions, middleTokens, formatExpressions, nestingDepth+1)
			continue
		}

		break
	}
}

// parseFormatString corresponds to _parseFormatString().
func (p *Parser) parseFormatString(startToken *FStringStartToken) *FormatStringNode {
	middleTokens := []*FStringMiddleToken{}
	fieldExpressions := []ExpressionNode{}
	formatExpressions := []ExpressionNode{}
	var endToken *FStringEndToken

	// Consume middle tokens and expressions until we hit a "{" or "}" token.
	for {
		nextToken := p.peekToken(0)

		if nextToken.GetType() == TokenTypeFStringEnd {
			endToken = nextToken.(*FStringEndToken)

			if endToken.Flags&StringTokenFlagsUnterminated != 0 {
				p.addSyntaxError(localization.LocMessage.StringUnterminated(), startToken.GetRange())
			}
			p.getNextToken()
			break
		}

		if nextToken.GetType() == TokenTypeFStringMiddle {
			middleTokens = append(middleTokens, nextToken.(*FStringMiddleToken))
			p.getNextToken()
			continue
		}

		if nextToken.GetType() == TokenTypeOpenCurlyBrace {
			if !p.parseFStringReplacementField(&fieldExpressions, &middleTokens, &formatExpressions, 0) {
				// An error was reported. Try to recover the parse.
				if p.consumeTokensUntilType([]TokenType{TokenTypeFStringEnd, TokenTypeNewLine}) {
					if p.peekToken(0).GetType() == TokenTypeFStringEnd {
						p.getNextToken()
					}
				}
				break
			}
			continue
		}

		// We've hit an error. Try to recover as gracefully as possible.
		if nextToken.GetType() != TokenTypeNewLine {
			// Consume tokens until we find the end.
			if p.consumeTokensUntilType([]TokenType{TokenTypeFStringEnd}) {
				p.getNextToken()
			}
		}

		message := localization.LocMessage.StringUnterminated()
		if nextToken.GetType() == TokenTypeCloseCurlyBrace {
			message = localization.LocMessage.FormatStringBrace()
		}
		p.addSyntaxError(message, nextToken.GetRange())
		break
	}

	p.reportStringTokenErrors(startToken.Flags, startToken.GetRange(), nil)

	return NewFormatStringNode(startToken, endToken, middleTokens, fieldExpressions, formatExpressions)
}

// parseStringList corresponds to _parseStringList().
func (p *Parser) parseStringList() *StringListNode {
	stringList := []StringOrFormatStringNode{}

	for {
		nextToken := p.peekToken(0)
		if nextToken.GetType() == TokenTypeString {
			stringList = append(stringList, p.makeStringNode(p.getNextToken().(*StringToken)))
		} else if nextToken.GetType() == TokenTypeFStringStart {
			stringList = append(stringList, p.parseFormatString(p.getNextToken().(*FStringStartToken)))
		} else {
			break
		}
	}

	stringNode := NewStringListNode(stringList)

	// If we're parsing a type annotation, parse the contents of the string.
	if p.isParsingTypeAnnotation {
		// Don't allow multiple strings because we have no way of reporting
		// parse errors that span strings.
		if len(stringNode.D.Strings) > 1 {
			if p.isParsingQuotedText {
				p.addSyntaxError(localization.LocMessage.AnnotationSpansStrings(), stringNode.GetRange())
			}
		} else if stringNode.D.Strings[0].GetNodeType() == ParseNodeTypeFormatString {
			if p.isParsingQuotedText {
				p.addSyntaxError(localization.LocMessage.AnnotationFormatString(), stringNode.GetRange())
			}
		} else {
			stringToken := stringNode.D.Strings[0].(*StringNode).D.Token
			stringValue := GetUnescapedStringEx(stringToken, false /* elideCrlf */)
			unescapedString := stringValue.Value
			tokenOffset := stringToken.Start
			prefixLength := stringToken.PrefixLength + stringToken.QuoteMarkLength

			// Don't allow escape characters because we have no way of mapping
			// error ranges back to the escaped text.
			if unescapedString.Length() != stringToken.Length-prefixLength-stringToken.QuoteMarkLength {
				if p.isParsingQuotedText {
					p.addSyntaxError(localization.LocMessage.AnnotationStringEscape(), stringNode.GetRange())
				}
			} else if stringToken.Flags&(StringTokenFlagsRaw|StringTokenFlagsBytes|StringTokenFlagsFormat|StringTokenFlagsTemplate) == 0 {
				initialParenDepth := 0
				if stringToken.Flags&StringTokenFlagsTriplicate != 0 {
					initialParenDepth = 1
				}

				subParser := NewParser()
				parseResults := subParser.ParseTextExpression(
					p.fileContents,
					tokenOffset+prefixLength,
					unescapedString.Length(),
					p.parseOptions,
					ParseTextModeVariableAnnotation,
					initialParenDepth,
					p.typingSymbolAliases,
				)

				if len(parseResults.Diagnostics) == 0 || p.parseOptions.ReportErrorsForParsedStringContents {
					for _, diag := range parseResults.Diagnostics {
						p.addSyntaxError(diag.Message, stringNode.GetRange())
					}

					if parseResults.ParseTree != nil {
						stringNode.D.Annotation = parseResults.ParseTree.(ExpressionNode)
						setParent(stringNode.D.Annotation, stringNode)
					}
				}
			}
		}
	}

	return stringNode
}

// getTypeAnnotationCommentText corresponds to _getTypeAnnotationCommentText().
// It returns nil where the TypeScript version returns undefined.
func (p *Parser) getTypeAnnotationCommentText() *StringToken {
	if p.tokenIndex == 0 {
		return nil
	}

	curToken := p.tokenizerOutput.Tokens.GetItemAt(p.tokenIndex - 1)
	nextToken := p.tokenizerOutput.Tokens.GetItemAt(p.tokenIndex)

	curRange := curToken.GetRange()
	if curRange.Start+curRange.Length == nextToken.GetRange().Start {
		return nil
	}

	interTokenContents := p.fileContents.Substring(curRange.Start+curRange.Length, nextToken.GetRange().Start)
	prefixLen, typeString, ok := matchTypeComment(interTokenContents)
	if !ok {
		return nil
	}

	// Ignore all "ignore" comments. Include "[" in the regular
	// expression because mypy supports ignore comments of the
	// form ignore[errorCode, ...]. We'll treat these as regular
	// ignore statements (as though no errorCodes were included).
	if matchIgnoreComment(trimText(typeString)) {
		return nil
	}

	// Synthesize a string token and StringNode.
	tokenOffset := curRange.Start + curRange.Length + prefixLen
	return NewStringToken(
		tokenOffset,
		typeString.Length(),
		StringTokenFlagsNone,
		typeString,
		0,
		nil, /* comments */
	)
}

// parseVariableTypeAnnotationComment corresponds to
// _parseVariableTypeAnnotationComment(). It returns nil where the TypeScript
// version returns undefined.
func (p *Parser) parseVariableTypeAnnotationComment() ExpressionNode {
	stringToken := p.getTypeAnnotationCommentText()
	if stringToken == nil {
		return nil
	}

	stringNode := p.makeStringNode(stringToken)
	stringListNode := NewStringListNode([]StringOrFormatStringNode{stringNode})
	subParser := NewParser()
	parseResults := subParser.ParseTextExpression(
		p.fileContents,
		stringToken.Start,
		stringToken.Length,
		p.parseOptions,
		ParseTextModeVariableAnnotation,
		0, /* initialParenDepth */
		p.typingSymbolAliases,
	)

	for _, diag := range parseResults.Diagnostics {
		p.addSyntaxError(diag.Message, stringListNode.GetRange())
	}

	if parseResults.ParseTree == nil {
		return nil
	}

	return parseResults.ParseTree.(ExpressionNode)
}

// parseFunctionTypeAnnotationComment corresponds to
// _parseFunctionTypeAnnotationComment().
func (p *Parser) parseFunctionTypeAnnotationComment(stringToken *StringToken, functionNode *FunctionNode) {
	stringNode := p.makeStringNode(stringToken)
	stringListNode := NewStringListNode([]StringOrFormatStringNode{stringNode})
	subParser := NewParser()
	parseResults := subParser.ParseTextExpression(
		p.fileContents,
		stringToken.Start,
		stringToken.Length,
		p.parseOptions,
		ParseTextModeFunctionAnnotation,
		0, /* initialParenDepth */
		p.typingSymbolAliases,
	)

	for _, diag := range parseResults.Diagnostics {
		p.addSyntaxError(diag.Message, stringListNode.GetRange())
	}

	if parseResults.ParseTree == nil {
		return
	}

	functionAnnotation := parseResults.ParseTree.(*FunctionAnnotationNode)

	functionNode.D.FuncAnnotationComment = functionAnnotation
	setParent(functionAnnotation, functionNode)
	ExtendRange(functionNode, functionAnnotation.GetRange())
}

// -----------------------------------------------------------------------------
// The two regular expressions parser.ts uses over type comments.
//
// These are hand-matched rather than handed to Go's regexp package because the
// match lengths feed directly into token offsets, which are UTF-16 code unit
// counts. Running a rune- or byte-oriented matcher over the text would give
// lengths in the wrong unit for any non-BMP character earlier in the line.
// -----------------------------------------------------------------------------

// matchTypeComment reproduces /^(\s*#\s*type:\s*)([^\r\n]*)/. It returns the
// length of group 1 and the text of group 2.
func matchTypeComment(text common.Text) (prefixLength int, typeString common.Text, ok bool) {
	i := 0
	length := text.Length()

	i = skipWhitespace(text, i)
	if i >= length || text.CharCodeAt(i) != common.CharHash {
		return 0, common.Text{}, false
	}
	i++

	i = skipWhitespace(text, i)
	const keyword = "type:"
	for k := 0; k < len(keyword); k++ {
		if i >= length || text.CharCodeAt(i) != common.Char(keyword[k]) {
			return 0, common.Text{}, false
		}
		i++
	}

	i = skipWhitespace(text, i)
	prefixLength = i

	// `[^\r\n]*` is greedy and cannot backtrack into anything, so it runs to
	// the first CR or LF.
	end := i
	for end < length {
		c := text.CharCodeAt(end)
		if c == common.CharCarriageReturn || c == common.CharLineFeed {
			break
		}
		end++
	}

	return prefixLength, text.Substring(i, end), true
}

// matchIgnoreComment reproduces /^ignore(\s|\[|$)/.
func matchIgnoreComment(text common.Text) bool {
	const keyword = "ignore"
	if text.Length() < len(keyword) {
		return false
	}
	for k := 0; k < len(keyword); k++ {
		if text.CharCodeAt(k) != common.Char(keyword[k]) {
			return false
		}
	}

	if text.Length() == len(keyword) {
		// The `$` alternative.
		return true
	}

	c := text.CharCodeAt(len(keyword))
	return c == common.CharOpenBracket || isJSRegExpWhitespace(c)
}

// isJSRegExpWhitespace reports whether c is matched by `\s` in a JavaScript
// regular expression: WhiteSpace plus LineTerminator.
func isJSRegExpWhitespace(c common.Char) bool {
	switch c {
	case 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return c >= 0x2000 && c <= 0x200a
}

func skipWhitespace(text common.Text, i int) int {
	for i < text.Length() && isJSRegExpWhitespace(text.CharCodeAt(i)) {
		i++
	}
	return i
}

// trimText reproduces String.prototype.trim(), which strips the same set of
// characters `\s` matches.
func trimText(text common.Text) common.Text {
	start := 0
	end := text.Length()
	for start < end && isJSRegExpWhitespace(text.CharCodeAt(start)) {
		start++
	}
	for end > start && isJSRegExpWhitespace(text.CharCodeAt(end-1)) {
		end--
	}
	return text.Substring(start, end)
}
