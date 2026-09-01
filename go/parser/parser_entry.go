/*
 * parser_entry.go
 *
 * The Parser's public entry points, transliterated from parser/parser.ts
 * (pyright 1.1.412).
 *
 * ParseTextExpression is complete. ParseSourceFile is not exported yet: it
 * drives _parseStatement, which is still being transliterated (see PORTING.md),
 * and exporting it now would mean shipping something that silently returns a
 * truncated tree.
 */

package parser

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// ParseSourceFile corresponds to parseSourceFile().
func (p *Parser) ParseSourceFile(
	fileContents common.Text,
	parseOptions *ParseOptions,
	diagSink *common.DiagnosticSink,
) *ParseFileResults {
	p.hasTypeAnnotations = false
	common.TimingStatsInstance.TokenizeFileTime.TimeOperation(func() {
		p.startNewParse(fileContents, 0, fileContents.Length(), parseOptions, diagSink, 0)
	})

	moduleNode := NewModuleNode(common.NewTextRange(0, fileContents.Length()))

	common.TimingStatsInstance.ParseFileTime.TimeOperation(func() {
		for !p.atEof() {
			if !p.consumeTokenIfType(TokenTypeNewLine) {
				// Handle a common error case and try to recover.
				nextToken := p.peekToken(0)
				if nextToken.GetType() == TokenTypeIndent {
					p.getNextToken()
					indentToken := nextToken.(*IndentToken)
					if indentToken.IsIndentAmbiguous {
						p.addSyntaxError(localization.LocMessage.InconsistentTabs(), indentToken.GetRange())
					} else {
						p.addSyntaxError(localization.LocMessage.UnexpectedIndent(), nextToken.GetRange())
					}
				}

				statement := p.parseStatement()
				if statement == nil {
					// Perform basic error recovery to get to the next line.
					p.consumeTokensUntilType([]TokenType{TokenTypeNewLine})
				} else {
					setParent(statement, moduleNode)
					moduleNode.D.Statements = append(moduleNode.D.Statements, statement)
				}
			}
		}
	})

	common.Assert(p.tokenizerOutput != nil, "")
	return &ParseFileResults{
		Text:        fileContents,
		ContentHash: common.HashText(fileContents),
		ParserOutput: &ParserOutput{
			ParseTree:              moduleNode,
			ImportedModules:        p.importedModules,
			FutureImports:          p.futureImports,
			ContainsWildcardImport: p.containsWildcardImport,
			TypingSymbolAliases:    p.typingSymbolAliases,
			HasTypeAnnotations:     p.hasTypeAnnotations,
			Lines:                  p.tokenizerOutput.Lines,
		},
		TokenizerOutput: p.tokenizerOutput,
	}
}

// ParseTextExpression corresponds to parseTextExpression().
//
// The TypeScript version has three overloads that differ only in which
// ParseTextMode they accept and, correspondingly, whether the parse tree is an
// ExpressionNode or a FunctionAnnotationNode. Go has no overloading, so this is
// the single implementation signature; ParseTree is a ParseNode that callers
// narrow according to the mode they passed.
//
// Pass nil for typingSymbolAliases to omit it.
func (p *Parser) ParseTextExpression(
	fileContents common.Text,
	textOffset int,
	textLength int,
	parseOptions *ParseOptions,
	parseTextMode ParseTextMode,
	initialParenDepth int,
	typingSymbolAliases map[string]string,
) *ParseExpressionTextResults {
	diagSink := common.NewDiagnosticSink()
	p.startNewParse(fileContents, textOffset, textLength, parseOptions, diagSink, initialParenDepth)

	if typingSymbolAliases != nil {
		// `new Map(typingSymbolAliases)` -- a copy, not an alias.
		copied := make(map[string]string, len(typingSymbolAliases))
		for k, v := range typingSymbolAliases {
			copied[k] = v
		}
		p.typingSymbolAliases = copied
	}

	var parseTree ParseNode
	switch parseTextMode {
	case ParseTextModeVariableAnnotation:
		p.isParsingQuotedText = true
		parseTree = p.parseTypeAnnotation(false /* allowUnpack */)

	case ParseTextModeFunctionAnnotation:
		p.isParsingQuotedText = true
		// A nil *FunctionAnnotationNode must not be boxed into a non-nil
		// ParseNode interface value, or the `if (!parseResults.parseTree)`
		// checks at the call sites would stop firing.
		if annotation := p.parseFunctionTypeAnnotation(); annotation != nil {
			parseTree = annotation
		}

	default:
		exprListResult := p.parseTestOrStarExpressionList(
			false, /* allowAssignmentExpression */
			true,  /* allowMultipleUnpack */
		)
		if exprListResult.parseError != nil {
			parseTree = exprListResult.parseError
		} else {
			if len(exprListResult.list) == 0 {
				p.addSyntaxError(localization.LocMessage.ExpectedExpr(), p.peekToken(0).GetRange())
			}
			parseTree = p.makeExpressionOrTuple(exprListResult, false /* enclosedInParens */)
		}
	}

	if p.peekTokenType() == TokenTypeNewLine {
		p.getNextToken()
	}

	if !p.atEof() {
		p.addSyntaxError(localization.LocMessage.UnexpectedExprToken(), p.peekToken(0).GetRange())
	}

	return &ParseExpressionTextResults{
		ParseTree:   parseTree,
		Lines:       p.tokenizerOutput.Lines,
		Diagnostics: diagSink.FetchAndClear(),
	}
}
