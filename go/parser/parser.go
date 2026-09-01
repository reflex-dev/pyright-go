/*
 * parser.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Based on code from python-language-server repository:
 *  https://github.com/Microsoft/python-language-server
 *
 * Parser for the Python language. Converts a stream of tokens
 * into an abstract syntax tree (AST).
 *
 * Transliterated from parser/parser.ts (pyright 1.1.412).
 *
 * STATUS: this file currently holds the parser's public surface (ParseOptions,
 * ParserOutput, ParseFileResults, ...), the Parser's state, and the token
 * cursor helpers -- everything the recursive-descent methods are built on. The
 * recursive-descent methods themselves are not yet transliterated, so no
 * entry point (parseSourceFile / parseTextExpression) is exported yet; adding
 * one before the statement and expression methods exist would mean shipping a
 * parser that silently returns an empty tree.
 *
 * The helpers here are exercised by parser_test.go against the real token
 * stream produced by the ported tokenizer.
 */

package parser

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// listResult corresponds to the ListResult<T> interface. Go generics stand in
// for the type parameter; ParseError is nil where TypeScript has `undefined`.
type listResult[T any] struct {
	list          []T
	trailingComma bool
	parseError    *ErrorNode
}

// subscriptListResult corresponds to the SubscriptListResult interface.
type subscriptListResult struct {
	list          []*ArgumentNode
	trailingComma bool
}

// ParseOptions corresponds to the ParseOptions class.
type ParseOptions struct {
	IsStubFile                          bool
	PythonVersion                       common.PythonVersion
	ReportInvalidStringEscapeSequence   bool
	SkipFunctionAndClassBody            bool
	UseNotebookMode                     bool
	ReportErrorsForParsedStringContents bool
}

// NewParseOptions corresponds to the ParseOptions constructor.
func NewParseOptions() *ParseOptions {
	return &ParseOptions{
		IsStubFile:                          false,
		PythonVersion:                       common.LatestStablePythonVersion,
		ReportInvalidStringEscapeSequence:   false,
		SkipFunctionAndClassBody:            false,
		UseNotebookMode:                     false,
		ReportErrorsForParsedStringContents: false,
	}
}

// ParserOutput corresponds to the ParserOutput interface.
type ParserOutput struct {
	ParseTree              *ModuleNode
	ImportedModules        []*ModuleImport
	FutureImports          map[string]bool
	ContainsWildcardImport bool
	TypingSymbolAliases    map[string]string
	HasTypeAnnotations     bool
	Lines                  *common.TextRangeCollection[common.TextRange]
}

// ParseFileResults corresponds to the ParseFileResults interface.
type ParseFileResults struct {
	Text            common.Text
	ContentHash     int32
	ParserOutput    *ParserOutput
	TokenizerOutput *TokenizerOutput
}

// ParseExpressionTextResults corresponds to
// ParseExpressionTextResults<T extends ParseNode>. ParseTree is nil where the
// TypeScript version has `undefined`.
type ParseExpressionTextResults struct {
	ParseTree   ParseNode
	Lines       *common.TextRangeCollection[common.TextRange]
	Diagnostics []*common.Diagnostic
}

// ModuleImport corresponds to the ModuleImport interface.
type ModuleImport struct {
	NameNode    *ModuleNameNode
	LeadingDots int
	NameParts   []string

	// Used for "from X import Y" pattern. An empty set implies
	// "from X import *". Nil corresponds to `undefined`.
	ImportedSymbols map[string]bool
}

// ArgListResult corresponds to the ArgListResult interface.
type ArgListResult struct {
	Args          []*ArgumentNode
	TrailingComma bool
}

// ParseTextMode corresponds to the ParseTextMode const enum.
type ParseTextMode int

const (
	ParseTextModeExpression ParseTextMode = iota
	ParseTextModeVariableAnnotation
	ParseTextModeFunctionAnnotation
)

// maxChildNodeDepth limits the max child node depth to prevent stack overflows.
const maxChildNodeDepth = 256

// Parser corresponds to the Parser class.
type Parser struct {
	fileContents                 common.Text
	tokenizerOutput              *TokenizerOutput
	tokens                       *common.TextRangeCollection[Token]
	tokenCount                   int
	tokenIndex                   int
	areErrorsSuppressed          bool
	parseOptions                 *ParseOptions
	diagSink                     *common.DiagnosticSink
	isInLoop                     bool
	isInFunction                 bool
	isInExceptionGroup           bool
	isParsingTypeAnnotation      bool
	isParsingIndexTrailer        bool
	isParsingQuotedText          bool
	isInFinallyBlock             bool
	isInFinallyLoop              bool
	futureImports                map[string]bool
	importedModules              []*ModuleImport
	containsWildcardImport       bool
	assignmentExpressionsAllowed bool
	typingImportAliases          []string
	typingSymbolAliases          map[string]string
	maxChildDepthMap             map[int]int
	hasTypeAnnotations           bool
}

// NewParser constructs a Parser with the field initializers the TypeScript
// class declares inline.
func NewParser() *Parser {
	return &Parser{
		tokenCount:                   0,
		tokenIndex:                   0,
		areErrorsSuppressed:          false,
		parseOptions:                 NewParseOptions(),
		diagSink:                     common.NewDiagnosticSink(),
		isInLoop:                     false,
		isInFunction:                 false,
		isInExceptionGroup:           false,
		isParsingTypeAnnotation:      false,
		isParsingIndexTrailer:        false,
		isParsingQuotedText:          false,
		isInFinallyBlock:             false,
		isInFinallyLoop:              false,
		futureImports:                map[string]bool{},
		importedModules:              []*ModuleImport{},
		containsWildcardImport:       false,
		assignmentExpressionsAllowed: true,
		typingImportAliases:          []string{},
		typingSymbolAliases:          map[string]string{},
		maxChildDepthMap:             map[int]int{},
		hasTypeAnnotations:           false,
	}
}

// startNewParse corresponds to _startNewParse().
func (p *Parser) startNewParse(
	fileContents common.Text,
	textOffset int,
	textLength int,
	parseOptions *ParseOptions,
	diagSink *common.DiagnosticSink,
	initialParenDepth int,
) {
	p.fileContents = fileContents
	p.parseOptions = parseOptions
	p.diagSink = diagSink

	// Tokenize the file contents.
	tokenizer := NewTokenizer()
	p.tokenizerOutput = tokenizer.TokenizeRange(
		fileContents,
		textOffset,
		textLength,
		initialParenDepth,
		p.parseOptions.UseNotebookMode,
	)
	p.tokens = p.tokenizerOutput.Tokens
	p.tokenCount = p.tokens.Count()
	p.tokenIndex = 0
}

// TokenizerOutput exposes the token stream produced by the last startNewParse.
func (p *Parser) TokenizerOutput() *TokenizerOutput {
	return p.tokenizerOutput
}

// getNextToken corresponds to _getNextToken().
func (p *Parser) getNextToken() Token {
	token := p.tokens.GetItemAt(p.tokenIndex)
	if !p.atEof() {
		p.tokenIndex++
	}

	return token
}

// atEof corresponds to _atEof(). It reports whether we are pointing at the last
// token in the stream, which is assumed to be an end-of-stream token.
func (p *Parser) atEof() bool {
	return p.tokenIndex >= p.tokenCount-1
}

// peekToken corresponds to _peekToken(count).
func (p *Parser) peekToken(count int) Token {
	targetIndex := p.tokenIndex + count
	if targetIndex < 0 {
		return p.tokens.GetItemAt(0)
	}

	if targetIndex >= p.tokenCount {
		return p.tokens.GetItemAt(p.tokenCount - 1)
	}

	return p.tokens.GetItemAt(targetIndex)
}

// peekTokenType corresponds to _peekTokenType().
func (p *Parser) peekTokenType() TokenType {
	return p.peekToken(0).GetType()
}

// peekKeywordType corresponds to _peekKeywordType(). The second return value
// stands in for `undefined`.
func (p *Parser) peekKeywordType() (KeywordType, bool) {
	nextToken := p.peekToken(0)
	if nextToken.GetType() != TokenTypeKeyword {
		return 0, false
	}

	return nextToken.(*KeywordToken).KeywordType, true
}

// peekOperatorType corresponds to _peekOperatorType().
func (p *Parser) peekOperatorType() (OperatorType, bool) {
	nextToken := p.peekToken(0)
	if nextToken.GetType() != TokenTypeOperator {
		return 0, false
	}

	return nextToken.(*OperatorToken).OperatorType, true
}

// getTokenIfIdentifier corresponds to _getTokenIfIdentifier(). It returns nil
// where the TypeScript version returns undefined.
func (p *Parser) getTokenIfIdentifier() *IdentifierToken {
	nextToken := p.peekToken(0)
	if nextToken.GetType() == TokenTypeIdentifier {
		return p.getNextToken().(*IdentifierToken)
	}

	// If the next token is invalid, treat it as an identifier.
	if nextToken.GetType() == TokenTypeInvalid {
		p.getNextToken()
		p.addSyntaxError(localization.LocMessage.InvalidIdentifierChar(), nextToken.GetRange())
		r := nextToken.GetRange()
		return NewIdentifierToken(r.Start, r.Length, common.Text{}, nextToken.GetComments())
	}

	// If this is a "soft keyword", it can be converted into an identifier.
	if nextToken.GetType() == TokenTypeKeyword {
		keywordToken := nextToken.(*KeywordToken)
		if keywordToken.IsSoftKeyword() {
			r := nextToken.GetRange()
			keywordText := p.fileContents.Substring(r.Start, r.Start+r.Length)
			p.getNextToken()
			return NewIdentifierToken(r.Start, r.Length, keywordText, nextToken.GetComments())
		}
	}

	return nil
}

// consumeTokensUntilType consumes tokens until the next one in the stream is
// either a specified terminator or the end-of-stream token.
func (p *Parser) consumeTokensUntilType(terminators []TokenType) bool {
	for {
		token := p.peekToken(0)
		for _, term := range terminators {
			if term == token.GetType() {
				return true
			}
		}

		if token.GetType() == TokenTypeEndOfStream {
			return false
		}

		p.getNextToken()
	}
}

// getTokenIfType corresponds to _getTokenIfType(). It returns nil where the
// TypeScript version returns undefined.
func (p *Parser) getTokenIfType(tokenType TokenType) Token {
	if p.peekTokenType() == tokenType {
		return p.getNextToken()
	}

	return nil
}

// consumeTokenIfType corresponds to _consumeTokenIfType().
func (p *Parser) consumeTokenIfType(tokenType TokenType) bool {
	return p.getTokenIfType(tokenType) != nil
}

// consumeTokenIfKeyword corresponds to _consumeTokenIfKeyword().
func (p *Parser) consumeTokenIfKeyword(keywordType KeywordType) bool {
	if kw, ok := p.peekKeywordType(); ok && kw == keywordType {
		p.getNextToken()
		return true
	}

	return false
}

// consumeTokenIfOperator corresponds to _consumeTokenIfOperator().
func (p *Parser) consumeTokenIfOperator(operatorType OperatorType) bool {
	if op, ok := p.peekOperatorType(); ok && op == operatorType {
		p.getNextToken()
		return true
	}

	return false
}

// getKeywordToken corresponds to _getKeywordToken().
func (p *Parser) getKeywordToken(keywordType KeywordType) *KeywordToken {
	token := p.getNextToken()
	common.Assert(token.GetType() == TokenTypeKeyword, "")
	keywordToken := token.(*KeywordToken)
	common.Assert(keywordToken.KeywordType == keywordType, "")
	return keywordToken
}

// getLanguageVersion corresponds to _getLanguageVersion().
func (p *Parser) getLanguageVersion() common.PythonVersion {
	return p.parseOptions.PythonVersion
}

// suppressErrors corresponds to _suppressErrors(). The TypeScript version uses
// try/finally so the flag is restored even if the callback throws; the deferred
// restore here does the same for a panic.
func (p *Parser) suppressErrors(callback func()) {
	errorsWereSuppressed := p.areErrorsSuppressed
	defer func() {
		p.areErrorsSuppressed = errorsWereSuppressed
	}()
	p.areErrorsSuppressed = true
	callback()
}

// addSyntaxError corresponds to _addSyntaxError().
func (p *Parser) addSyntaxError(message string, r common.TextRange) {
	if !p.areErrorsSuppressed {
		p.diagSink.AddError(
			message,
			common.ConvertOffsetsToRange(r.Start, r.Start+r.Length, p.tokenizerOutput.Lines),
		)
	}
}
