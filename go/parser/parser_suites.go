/*
 * parser_suites.go
 *
 * Statement dispatch and the block-structured statements, transliterated from
 * parser/parser.ts (pyright 1.1.412): suites, if/while/for/try/with, function
 * and class definitions, and decorators.
 */

package parser

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// parseStatement corresponds to _parseStatement(). It returns nil where the
// TypeScript version returns undefined.
//
// stmt: simple_stmt | compound_stmt
// compound_stmt: if_stmt | while_stmt | for_stmt | try_stmt | with_stmt
//
//	| funcdef | classdef | decorated | async_stmt
func (p *Parser) parseStatement() StatementNode {
	// Handle the errant condition of a dedent token here to provide
	// better recovery.
	if p.consumeTokenIfType(TokenTypeDedent) {
		p.addSyntaxError(localization.LocMessage.UnexpectedUnindent(), p.peekToken(0).GetRange())
	}

	if kw, ok := p.peekKeywordType(); ok {
		switch kw {
		case KeywordTypeIf:
			return p.parseIfStatement(KeywordTypeIf)

		case KeywordTypeWhile:
			return p.parseWhileStatement()

		case KeywordTypeFor:
			return p.parseForStatement(nil)

		case KeywordTypeTry:
			return p.parseTryStatement()

		case KeywordTypeWith:
			return p.parseWithStatement(nil)

		case KeywordTypeDef:
			return p.parseFunctionDef(nil, nil)

		case KeywordTypeClass:
			return p.parseClassDef(nil)

		case KeywordTypeAsync:
			return p.parseAsyncStatement()

		case KeywordTypeMatch:
			// Match is considered a "soft" keyword, so we will treat
			// it as an identifier if it is followed by an unexpected
			// token.
			peekToken := p.peekToken(1)
			isInvalidMatchToken := false

			switch peekToken.GetType() {
			case TokenTypeColon,
				TokenTypeSemicolon,
				TokenTypeComma,
				TokenTypeDot,
				TokenTypeNewLine,
				TokenTypeEndOfStream:
				isInvalidMatchToken = true
			case TokenTypeOperator:
				operatorToken := peekToken.(*OperatorToken)
				if operatorToken.OperatorType != OperatorTypeMultiply &&
					operatorToken.OperatorType != OperatorTypeAdd &&
					operatorToken.OperatorType != OperatorTypeBitwiseInvert &&
					operatorToken.OperatorType != OperatorTypeSubtract {
					isInvalidMatchToken = true
				}
			}

			if !isInvalidMatchToken {
				// Try to parse the match statement. If it doesn't appear to
				// be a match statement, treat as a non-keyword and reparse.
				matchStatement := p.parseMatchStatement()
				if matchStatement != nil {
					return matchStatement
				}
			}
		}
	}

	if op, ok := p.peekOperatorType(); ok && op == OperatorTypeMatrixMultiply {
		return p.parseDecorated()
	}

	return p.parseSimpleStatement()
}

// parseAsyncStatement corresponds to _parseAsyncStatement(). It returns nil
// where the TypeScript version returns undefined.
//
// async_stmt: 'async' (funcdef | with_stmt | for_stmt)
func (p *Parser) parseAsyncStatement() StatementNode {
	asyncToken := p.getKeywordToken(KeywordTypeAsync)

	if kw, ok := p.peekKeywordType(); ok {
		switch kw {
		case KeywordTypeDef:
			return p.parseFunctionDef(asyncToken, nil)

		case KeywordTypeWith:
			return p.parseWithStatement(asyncToken)

		case KeywordTypeFor:
			return p.parseForStatement(asyncToken)
		}
	}

	p.addSyntaxError(localization.LocMessage.UnexpectedAsyncToken(), asyncToken.GetRange())

	return nil
}

// parseIfStatement corresponds to _parseIfStatement().
//
// if_stmt: 'if' test_suite ('elif' test_suite)* ['else' suite]
// test_suite: test suite
// test: or_test ['if' or_test 'else' test] | lambdef
func (p *Parser) parseIfStatement(keywordType KeywordType) *IfNode {
	ifOrElifToken := p.getKeywordToken(keywordType)

	test := p.parseTestExpression(true /* allowAssignmentExpression */)
	suite := p.parseSuite(p.isInFunction, false /* skipBody */, nil)
	ifNode := NewIfNode(ifOrElifToken, test, suite, nil)

	if p.consumeTokenIfKeyword(KeywordTypeElse) {
		ifNode.D.ElseSuite = p.parseSuite(p.isInFunction, false /* skipBody */, nil)
		setParent(ifNode.D.ElseSuite, ifNode)
		ExtendRange(ifNode, ifNode.D.ElseSuite.GetRange())
	} else if kw, ok := p.peekKeywordType(); ok && kw == KeywordTypeElif {
		// Recursively handle an "elif" statement.
		ifNode.D.ElseSuite = p.parseIfStatement(KeywordTypeElif)
		setParent(ifNode.D.ElseSuite, ifNode)
		ExtendRange(ifNode, ifNode.D.ElseSuite.GetRange())
	}

	return ifNode
}

// parseExceptSuite corresponds to _parseExceptSuite<T>(). The single call site
// passes a SuiteNode-producing callback, so this is not generic.
func (p *Parser) parseExceptSuite(isExceptionGroup bool, callback func() *SuiteNode) *SuiteNode {
	wasInExceptionGroup := p.isInExceptionGroup

	if isExceptionGroup {
		p.isInExceptionGroup = true
	}
	result := callback()

	p.isInExceptionGroup = wasInExceptionGroup

	return result
}

// parseLoopSuite corresponds to _parseLoopSuite().
func (p *Parser) parseLoopSuite() *SuiteNode {
	wasInLoop := p.isInLoop
	wasInExceptionGroup := p.isInExceptionGroup
	p.isInExceptionGroup = false
	p.isInLoop = true

	// Record the fact that we are no longer in a finally block
	// that is contained within a loop. A loop within the finally
	// block resets this. See PEP 765 for details.
	wasInFinallyLoop := p.isInFinallyLoop
	p.isInFinallyLoop = false

	var typeComment *StringToken
	suite := p.parseSuite(p.isInFunction, false /* skipBody */, func() {
		comment := p.getTypeAnnotationCommentText()
		if comment != nil {
			typeComment = comment
		}
	})

	p.isInLoop = wasInLoop
	p.isInFinallyLoop = wasInFinallyLoop
	p.isInExceptionGroup = wasInExceptionGroup

	if typeComment != nil {
		suite.D.TypeComment = typeComment
	}

	return suite
}

// parseSuite corresponds to _parseSuite(). Pass nil for postColonCallback to
// omit it.
//
// suite: ':' (simple_stmt | NEWLINE INDENT stmt+ DEDENT)
func (p *Parser) parseSuite(isFunction bool, skipBody bool, postColonCallback func()) *SuiteNode {
	nextToken := p.peekToken(0)
	suite := NewSuiteNode(nextToken.GetRange())

	if !p.consumeTokenIfType(TokenTypeColon) {
		p.addSyntaxError(localization.LocMessage.ExpectedColon(), nextToken.GetRange())

		// Try to perform parse recovery by consuming tokens.
		if p.consumeTokensUntilType([]TokenType{TokenTypeNewLine, TokenTypeColon}) {
			if p.peekTokenType() == TokenTypeColon {
				p.getNextToken()
			} else if p.peekToken(1).GetType() != TokenTypeIndent {
				// Bail so we resume at the next statement.
				// We can't parse as a simple statement as we've skipped all but the newline.
				p.getNextToken()
				return suite
			}
		}
	}

	if skipBody {
		if p.consumeTokenIfType(TokenTypeNewLine) {
			indent := 0
			for {
				nextToken := p.getNextToken()
				if nextToken.GetType() == TokenTypeIndent {
					indent++
				}

				if nextToken.GetType() == TokenTypeDedent {
					if nextToken.(*DedentToken).IsDedentAmbiguous {
						p.addSyntaxError(localization.LocMessage.InconsistentTabs(), nextToken.GetRange())
					}

					indent--

					if indent == 0 {
						break
					}
				}

				if nextToken.GetType() == TokenTypeEndOfStream {
					break
				}
			}
		} else {
			// consume tokens
			p.parseSimpleStatement()
		}

		if p.tokenIndex > 0 {
			ExtendRange(suite, p.tokenizerOutput.Tokens.GetItemAt(p.tokenIndex-1).GetRange())
		}

		return suite
	}

	if postColonCallback != nil {
		postColonCallback()
	}

	wasFunction := p.isInFunction
	p.isInFunction = isFunction

	if p.consumeTokenIfType(TokenTypeNewLine) {
		if postColonCallback != nil {
			postColonCallback()
		}

		possibleIndent := p.peekToken(0)
		if !p.consumeTokenIfType(TokenTypeIndent) {
			p.addSyntaxError(localization.LocMessage.ExpectedIndentedBlock(), p.peekToken(0).GetRange())
			return suite
		}

		// The cast is unconditional in the original, and reached only after the
		// Indent token was consumed, so it is always an IndentToken here.
		bodyIndentToken := possibleIndent.(*IndentToken)
		if bodyIndentToken.IsIndentAmbiguous {
			p.addSyntaxError(localization.LocMessage.InconsistentTabs(), bodyIndentToken.GetRange())
		}

		for {
			// Handle a common error here and see if we can recover.
			nextToken := p.peekToken(0)
			if nextToken.GetType() == TokenTypeIndent {
				p.getNextToken()
				indentToken := nextToken.(*IndentToken)
				if indentToken.IsIndentAmbiguous {
					p.addSyntaxError(localization.LocMessage.InconsistentTabs(), indentToken.GetRange())
				} else {
					p.addSyntaxError(localization.LocMessage.UnexpectedIndent(), nextToken.GetRange())
				}
			} else if nextToken.GetType() == TokenTypeDedent {
				// When we see a dedent, stop before parsing the dedented statement.
				dedentToken := nextToken.(*DedentToken)
				if !dedentToken.MatchesIndent {
					p.addSyntaxError(localization.LocMessage.InconsistentIndent(), dedentToken.GetRange())
				}
				if dedentToken.IsDedentAmbiguous {
					p.addSyntaxError(localization.LocMessage.InconsistentTabs(), dedentToken.GetRange())
				}

				// When the suite is incomplete (no statements), leave the dedent token for
				// recovery. This allows a single dedent token to cause us to break out of
				// multiple levels of nested suites. Also extend the suite's range in this
				// case so it is multi-line as this works better with indentationUtils.
				if len(suite.D.Statements) > 0 {
					p.consumeTokenIfType(TokenTypeDedent)
				} else {
					ExtendRange(suite, dedentToken.GetRange())
				}

				// Did this dedent take us to an indent amount that is less than the
				// initial indent of the suite body?
				//
				// (`!bodyIndentToken` in the original can never be true here: the
				// Indent token was consumed above, so the cast always succeeds.)
				if dedentToken.IndentAmount < bodyIndentToken.IndentAmount {
					break
				} else if dedentToken.IndentAmount == bodyIndentToken.IndentAmount {
					// If the next token is also a dedent that reduces the indent
					// level to a less than the initial indent of the suite body, swallow
					// the extra dedent to help recover the parse.
					nextToken := p.peekToken(0)
					if p.consumeTokenIfType(TokenTypeDedent) {
						ExtendRange(suite, nextToken.GetRange())
						break
					}
				}
			}

			statement := p.parseStatement()
			if statement == nil {
				// Perform basic error recovery to get to the next line.
				p.consumeTokensUntilType([]TokenType{TokenTypeNewLine})
			} else {
				setParent(statement, suite)
				suite.D.Statements = append(suite.D.Statements, statement)
			}

			if p.peekTokenType() == TokenTypeEndOfStream {
				break
			}
		}
	} else {
		simpleStatement := p.parseSimpleStatement()
		suite.D.Statements = append(suite.D.Statements, simpleStatement)
		setParent(simpleStatement, suite)
	}

	if len(suite.D.Statements) > 0 {
		ExtendRange(suite, suite.D.Statements[len(suite.D.Statements)-1].GetRange())
	}

	p.isInFunction = wasFunction

	return suite
}

// parseForStatement corresponds to _parseForStatement(). Pass nil for
// asyncToken to omit it.
//
// for_stmt: [async] 'for' exprlist 'in' testlist suite ['else' suite]
func (p *Parser) parseForStatement(asyncToken *KeywordToken) *ForNode {
	forToken := p.getKeywordToken(KeywordTypeFor)

	targetExpr := p.parseExpressionListAsPossibleTuple(
		ErrorExpressionCategoryMissingExpression,
		func() string { return localization.LocMessage.ExpectedExpr() },
		forToken,
	)

	var seqExpr ExpressionNode
	var forSuite *SuiteNode
	var elseSuite *SuiteNode

	if !p.consumeTokenIfKeyword(KeywordTypeIn) {
		seqExpr = p.handleExpressionParseError(
			ErrorExpressionCategoryMissingIn,
			localization.LocMessage.ExpectedIn(),
			nil, nil, nil,
		)
		forSuite = NewSuiteNode(p.peekToken(0).GetRange())
	} else {
		seqExpr = p.parseTestOrStarListAsExpression(
			false, /* allowAssignmentExpression */
			true,  /* allowMultipleUnpack */
			ErrorExpressionCategoryMissingExpression,
			func() string { return localization.LocMessage.ExpectedInExpr() },
		)

		forSuite = p.parseLoopSuite()

		// Versions of Python earlier than 3.9 didn't allow unpack operators if the
		// tuple wasn't enclosed in parentheses.
		if p.getLanguageVersion().IsLessThan(common.PythonVersion3_9) && !p.parseOptions.IsStubFile {
			if tupleNode, ok := seqExpr.(*TupleNode); ok && !tupleNode.D.HasParens {
				sawStar := false
				for _, expr := range tupleNode.D.Items {
					if expr.GetNodeType() == ParseNodeTypeUnpack && !sawStar {
						p.addSyntaxError(localization.LocMessage.UnpackOperatorNotAllowed(), expr.GetRange())
						sawStar = true
					}
				}
			}
		}

		if p.consumeTokenIfKeyword(KeywordTypeElse) {
			elseSuite = p.parseSuite(p.isInFunction, false /* skipBody */, nil)
		}
	}

	forNode := NewForNode(forToken, targetExpr, seqExpr, forSuite)
	forNode.D.ElseSuite = elseSuite
	if elseSuite != nil {
		ExtendRange(forNode, elseSuite.GetRange())
		setParent(elseSuite, forNode)
	}

	if asyncToken != nil {
		forNode.D.IsAsync = true
		forNode.D.AsyncToken = asyncToken
		ExtendRange(forNode, asyncToken.GetRange())
	}

	if forSuite.D.TypeComment != nil {
		forNode.D.TypeComment = forSuite.D.TypeComment
	}

	return forNode
}

// parseWhileStatement corresponds to _parseWhileStatement().
//
// while_stmt: 'while' test suite ['else' suite]
func (p *Parser) parseWhileStatement() *WhileNode {
	whileToken := p.getKeywordToken(KeywordTypeWhile)

	// Note that the original relies on JavaScript's left-to-right argument
	// evaluation: the test expression is parsed before the suite.
	testExpr := p.parseTestExpression(true /* allowAssignmentExpression */)
	whileNode := NewWhileNode(whileToken, testExpr, p.parseLoopSuite())

	if p.consumeTokenIfKeyword(KeywordTypeElse) {
		whileNode.D.ElseSuite = p.parseSuite(p.isInFunction, false /* skipBody */, nil)
		setParent(whileNode.D.ElseSuite, whileNode)
		ExtendRange(whileNode, whileNode.D.ElseSuite.GetRange())
	}

	return whileNode
}

// parseTryStatement corresponds to _parseTryStatement().
//
//	try_stmt: ('try' suite
//	        ((except_clause suite)+
//	            ['else' suite]
//	            ['finally' suite] |
//	        'finally' suite))
//	except_clause: 'except' [test ['as' NAME]]
func (p *Parser) parseTryStatement() *TryNode {
	tryToken := p.getKeywordToken(KeywordTypeTry)
	trySuite := p.parseSuite(p.isInFunction, false /* skipBody */, nil)
	tryNode := NewTryNode(tryToken, trySuite)
	sawCatchAllExcept := false
	reportedExceptGroupMismatch := false

	for {
		exceptToken := p.peekToken(0)
		if !p.consumeTokenIfKeyword(KeywordTypeExcept) {
			break
		}

		// See if this is a Python 3.11 exception group.
		possibleStarToken := p.peekToken(0)
		isExceptGroup := false
		if p.consumeTokenIfOperator(OperatorTypeMultiply) {
			if p.getLanguageVersion().IsLessThan(common.PythonVersion3_11) && !p.parseOptions.IsStubFile {
				p.addSyntaxError(localization.LocMessage.ExceptionGroupIncompatible(), possibleStarToken.GetRange())
			}

			isExceptGroup = true

			if !reportedExceptGroupMismatch && anyExceptClause(tryNode, func(c *ExceptNode) bool { return !c.D.IsExceptGroup }) {
				p.addSyntaxError(localization.LocMessage.ExceptGroupMismatch(), possibleStarToken.GetRange())
				reportedExceptGroupMismatch = true
			}
		} else {
			if !reportedExceptGroupMismatch && anyExceptClause(tryNode, func(c *ExceptNode) bool { return c.D.IsExceptGroup }) {
				p.addSyntaxError(localization.LocMessage.ExceptGroupMismatch(), possibleStarToken.GetRange())
				reportedExceptGroupMismatch = true
			}
		}

		var typeExpr ExpressionNode
		var symbolName *IdentifierToken
		isAsKeywordAllowed := true

		if p.peekTokenType() != TokenTypeColon {
			listResult := parseExpressionListGeneric(p, func() ExpressionNode {
				return p.parseTestExpression(true /* allowAssignmentExpression */)
			}, nil, nil)
			if listResult.parseError != nil {
				typeExpr = listResult.parseError
			} else {
				typeExpr = p.makeExpressionOrTuple(listResult, false /* enclosedInParens */)

				// Python 3.14 allows more than one exception type to be provided in
				// an except clause.
				if len(listResult.list) > 1 {
					if p.getLanguageVersion().IsLessThan(common.PythonVersion3_14) {
						p.addSyntaxError(localization.LocMessage.ExceptRequiresParens(), typeExpr.GetRange())
					}

					isAsKeywordAllowed = false
				}
			}

			if p.consumeTokenIfKeyword(KeywordTypeAs) {
				if !isAsKeywordAllowed {
					p.addSyntaxError(localization.LocMessage.ExceptWithAsRequiresParens(), typeExpr.GetRange())
				}

				symbolName = p.getTokenIfIdentifier()
				if symbolName == nil {
					p.addSyntaxError(localization.LocMessage.ExpectedNameAfterAs(), p.peekToken(0).GetRange())
				}
			}
		} else if isExceptGroup {
			p.addSyntaxError(localization.LocMessage.ExceptGroupRequiresType(), p.peekToken(0).GetRange())
		}

		if typeExpr == nil {
			if sawCatchAllExcept {
				p.addSyntaxError(localization.LocMessage.DuplicateCatchAll(), exceptToken.GetRange())
			}
			sawCatchAllExcept = true
		} else {
			if sawCatchAllExcept {
				p.addSyntaxError(localization.LocMessage.NamedExceptAfterCatchAll(), typeExpr.GetRange())
			}
		}

		exceptSuite := p.parseExceptSuite(isExceptGroup, func() *SuiteNode {
			return p.parseSuite(p.isInFunction, false /* skipBody */, nil)
		})
		exceptNode := NewExceptNode(exceptToken, exceptSuite, isExceptGroup)
		if typeExpr != nil {
			exceptNode.D.TypeExpr = typeExpr
			setParent(exceptNode.D.TypeExpr, exceptNode)
		}

		if symbolName != nil {
			exceptNode.D.Name = NewNameNode(symbolName)
			setParent(exceptNode.D.Name, exceptNode)
		}

		tryNode.D.ExceptClauses = append(tryNode.D.ExceptClauses, exceptNode)
		setParent(exceptNode, tryNode)
	}

	if len(tryNode.D.ExceptClauses) > 0 {
		ExtendRange(tryNode, tryNode.D.ExceptClauses[len(tryNode.D.ExceptClauses)-1].GetRange())

		if p.consumeTokenIfKeyword(KeywordTypeElse) {
			tryNode.D.ElseSuite = p.parseSuite(p.isInFunction, false /* skipBody */, nil)
			setParent(tryNode.D.ElseSuite, tryNode)
			ExtendRange(tryNode, tryNode.D.ElseSuite.GetRange())
		}
	}

	if p.consumeTokenIfKeyword(KeywordTypeFinally) {
		wasInFinallyBlock := p.isInFinallyBlock
		wasInFinallyLoop := p.isInFinallyLoop
		p.isInFinallyBlock = true
		p.isInFinallyLoop = p.isInLoop

		tryNode.D.FinallySuite = p.parseSuite(p.isInFunction, false /* skipBody */, nil)

		p.isInFinallyBlock = wasInFinallyBlock
		p.isInFinallyLoop = wasInFinallyLoop

		setParent(tryNode.D.FinallySuite, tryNode)
		ExtendRange(tryNode, tryNode.D.FinallySuite.GetRange())
	}

	if tryNode.D.FinallySuite == nil && len(tryNode.D.ExceptClauses) == 0 {
		p.addSyntaxError(localization.LocMessage.TryWithoutExcept(), tryToken.GetRange())
	}

	return tryNode
}

// anyExceptClause stands in for `tryNode.d.exceptClauses.some(...)`.
func anyExceptClause(tryNode *TryNode, predicate func(*ExceptNode) bool) bool {
	for _, clause := range tryNode.D.ExceptClauses {
		if predicate(clause) {
			return true
		}
	}
	return false
}

// parseFunctionDef corresponds to _parseFunctionDef(). Pass nil for asyncToken
// and decorators to omit them.
//
// funcdef: 'def' NAME parameters ['->' test] ':' suite
// parameters: '(' [typedargslist] ')'
func (p *Parser) parseFunctionDef(asyncToken *KeywordToken, decorators []*DecoratorNode) StatementNode {
	defToken := p.getKeywordToken(KeywordTypeDef)

	nameToken := p.getTokenIfIdentifier()
	if nameToken == nil {
		p.addSyntaxError(localization.LocMessage.ExpectedFunctionName(), defToken.GetRange())
		return NewErrorNode(
			defToken.GetRange(),
			ErrorExpressionCategoryMissingFunctionParameterList,
			nil,
			decorators,
		)
	}

	var typeParameters *TypeParameterListNode
	possibleOpenBracket := p.peekToken(0)
	if possibleOpenBracket.GetType() == TokenTypeOpenBracket {
		typeParameters = p.parseTypeParameterList()

		if !p.parseOptions.IsStubFile && p.getLanguageVersion().IsLessThan(common.PythonVersion3_12) {
			p.addSyntaxError(localization.LocMessage.FunctionTypeParametersIllegal(), typeParameters.GetRange())
		}
	}
	openParenToken := p.peekToken(0)
	if !p.consumeTokenIfType(TokenTypeOpenParenthesis) {
		p.addSyntaxError(localization.LocMessage.ExpectedOpenParen(), p.peekToken(0).GetRange())
		return NewErrorNode(
			nameToken.GetRange(),
			ErrorExpressionCategoryMissingFunctionParameterList,
			NewNameNode(nameToken),
			decorators,
		)
	}

	paramList := p.parseVarArgsList(TokenTypeCloseParenthesis, true /* allowAnnotations */)

	if !p.consumeTokenIfType(TokenTypeCloseParenthesis) {
		p.addSyntaxError(localization.LocMessage.ExpectedCloseParen(), openParenToken.GetRange())
		p.consumeTokensUntilType([]TokenType{TokenTypeColon})
	}

	var returnType ExpressionNode
	if p.consumeTokenIfType(TokenTypeArrow) {
		returnType = p.parseTypeAnnotation(false /* allowUnpack */)
	}

	var functionTypeAnnotationToken *StringToken
	wasInExceptionGroup := p.isInExceptionGroup
	p.isInExceptionGroup = false

	wasInFinallyBlock := p.isInFinallyBlock
	wasInFinallyLoop := p.isInFinallyLoop
	p.isInFinallyBlock = false
	p.isInFinallyLoop = false

	suite := p.parseSuite(true /* isFunction */, p.parseOptions.SkipFunctionAndClassBody, func() {
		if functionTypeAnnotationToken == nil {
			functionTypeAnnotationToken = p.getTypeAnnotationCommentText()
		}
	})

	p.isInExceptionGroup = wasInExceptionGroup
	p.isInFinallyBlock = wasInFinallyBlock
	p.isInFinallyLoop = wasInFinallyLoop

	functionNode := NewFunctionNode(defToken, NewNameNode(nameToken), suite, typeParameters)
	if asyncToken != nil {
		functionNode.D.IsAsync = true
		ExtendRange(functionNode, asyncToken.GetRange())
	}

	functionNode.D.Params = paramList
	for _, param := range paramList {
		setParent(param, functionNode)
	}

	if decorators != nil {
		functionNode.D.Decorators = decorators
		for _, decorator := range decorators {
			setParent(decorator, functionNode)
		}

		if len(decorators) > 0 {
			ExtendRange(functionNode, decorators[0].GetRange())
		}
	}

	if returnType != nil {
		functionNode.D.ReturnAnnotation = returnType
		setParent(functionNode.D.ReturnAnnotation, functionNode)
		ExtendRange(functionNode, returnType.GetRange())
	}

	// If there was a type annotation comment for the function,
	// parse it now.
	if functionTypeAnnotationToken != nil {
		p.parseFunctionTypeAnnotationComment(functionTypeAnnotationToken, functionNode)
	}

	return functionNode
}

// parseWithStatement corresponds to _parseWithStatement(). Pass nil for
// asyncToken to omit it.
//
// with_stmt: 'with' with_item (',' with_item)*  ':' suite
// Python 3.10 adds support for optional parentheses around with_item list.
func (p *Parser) parseWithStatement(asyncToken *KeywordToken) *WithNode {
	withToken := p.getKeywordToken(KeywordTypeWith)
	withItemList := []*WithItemNode{}

	possibleParen := p.peekToken(0)

	// If the expression starts with a paren, parse it as though the
	// paren is enclosing the list of "with items". This is done as a
	// "dry run" to determine whether the entire list of "with items"
	// is enclosed in parentheses.
	isParenthesizedWithItemList := false
	isParenthesizedDisallowed := false

	if possibleParen.GetType() == TokenTypeOpenParenthesis {
		openParenTokenIndex := p.tokenIndex

		p.suppressErrors(func() {
			p.getNextToken()
			for {
				withItemList = append(withItemList, p.parseWithItem())
				if !p.consumeTokenIfType(TokenTypeComma) {
					break
				}

				if p.peekToken(0).GetType() == TokenTypeCloseParenthesis {
					break
				}
			}

			if p.peekToken(0).GetType() == TokenTypeCloseParenthesis &&
				p.peekToken(1).GetType() == TokenTypeColon {
				isParenthesizedWithItemList = true

				// Some forms of parenthesized context with statements were not
				// allowed prior to Python 3.9. Is this such a form?
				isParenthesizedDisallowed = len(withItemList) != 1 || withItemList[0].D.Target != nil
			}

			p.tokenIndex = openParenTokenIndex
			withItemList = []*WithItemNode{}
		})
	}

	if isParenthesizedWithItemList {
		p.consumeTokenIfType(TokenTypeOpenParenthesis)
		if isParenthesizedDisallowed && p.getLanguageVersion().IsLessThan(common.PythonVersion3_9) {
			p.addSyntaxError(localization.LocMessage.ParenthesizedContextManagerIllegal(), possibleParen.GetRange())
		}
	}

	for {
		withItemList = append(withItemList, p.parseWithItem())

		if !p.consumeTokenIfType(TokenTypeComma) {
			break
		}

		if p.peekToken(0).GetType() == TokenTypeCloseParenthesis {
			break
		}
	}

	if isParenthesizedWithItemList {
		if !p.consumeTokenIfType(TokenTypeCloseParenthesis) {
			p.addSyntaxError(localization.LocMessage.ExpectedCloseParen(), possibleParen.GetRange())
		}
	}

	var typeComment *StringToken
	withSuite := p.parseSuite(p.isInFunction, false /* skipBody */, func() {
		comment := p.getTypeAnnotationCommentText()
		if comment != nil {
			typeComment = comment
		}
	})
	withNode := NewWithNode(withToken, withSuite)
	if asyncToken != nil {
		withNode.D.IsAsync = true
		withNode.D.AsyncToken = asyncToken
		ExtendRange(withNode, asyncToken.GetRange())
	}

	if typeComment != nil {
		withNode.D.TypeComment = typeComment
	}

	withNode.D.WithItems = withItemList
	for _, withItem := range withItemList {
		setParent(withItem, withNode)
	}

	return withNode
}

// parseWithItem corresponds to _parseWithItem().
//
// with_item: test ['as' expr]
func (p *Parser) parseWithItem() *WithItemNode {
	expr := p.parseTestExpression(true /* allowAssignmentExpression */)
	itemNode := NewWithItemNode(expr)

	if p.consumeTokenIfKeyword(KeywordTypeAs) {
		itemNode.D.Target = p.parseExpression(false /* allowUnpack */)
		setParent(itemNode.D.Target, itemNode)
		ExtendRange(itemNode, itemNode.D.Target.GetRange())
	}

	return itemNode
}

// parseDecorated corresponds to _parseDecorated(). It returns nil where the
// TypeScript version returns undefined -- which, as written, it never does.
//
// decorators: decorator+
// decorated: decorators (classdef | funcdef | async_funcdef)
func (p *Parser) parseDecorated() StatementNode {
	decoratorList := []*DecoratorNode{}

	for {
		if op, ok := p.peekOperatorType(); ok && op == OperatorTypeMatrixMultiply {
			decoratorList = append(decoratorList, p.parseDecorator())
		} else {
			break
		}
	}

	nextToken := p.peekToken(0)
	if nextToken.GetType() == TokenTypeKeyword {
		keywordToken := nextToken.(*KeywordToken)
		if keywordToken.KeywordType == KeywordTypeAsync {
			p.getNextToken()

			if kw, ok := p.peekKeywordType(); !ok || kw != KeywordTypeDef {
				p.addSyntaxError(localization.LocMessage.ExpectedFunctionAfterAsync(), p.peekToken(0).GetRange())
			} else {
				return p.parseFunctionDef(keywordToken, decoratorList)
			}
		} else if keywordToken.KeywordType == KeywordTypeDef {
			return p.parseFunctionDef(nil, decoratorList)
		} else if keywordToken.KeywordType == KeywordTypeClass {
			return p.parseClassDef(decoratorList)
		}
	}

	p.addSyntaxError(localization.LocMessage.ExpectedAfterDecorator(), p.peekToken(0).GetRange())

	// Return a dummy class declaration so the completion provider has
	// some parse nodes to work with.
	return NewClassNodeDummyForDecorators(decoratorList)
}

// parseDecorator corresponds to _parseDecorator().
//
// decorator: '@' dotted_name [ '(' [arglist] ')' ] NEWLINE
func (p *Parser) parseDecorator() *DecoratorNode {
	atOperator := p.getNextToken().(*OperatorToken)
	common.Assert(atOperator.OperatorType == OperatorTypeMatrixMultiply, "")

	expression := p.parseTestExpression(true /* allowAssignmentExpression */)

	// Versions of Python prior to 3.9 support a limited set of
	// expression forms.
	if p.getLanguageVersion().IsLessThan(common.PythonVersion3_9) {
		isSupportedExpressionForm := false
		if p.isNameOrMemberAccessExpression(expression) {
			isSupportedExpressionForm = true
		} else if callNode, ok := expression.(*CallNode); ok && p.isNameOrMemberAccessExpression(callNode.D.LeftExpr) {
			isSupportedExpressionForm = true
		}

		if !isSupportedExpressionForm {
			p.addSyntaxError(localization.LocMessage.ExpectedDecoratorExpr(), expression.GetRange())
		}
	}

	decoratorNode := NewDecoratorNode(atOperator, expression)

	if !p.consumeTokenIfType(TokenTypeNewLine) {
		p.addSyntaxError(localization.LocMessage.ExpectedDecoratorNewline(), p.peekToken(0).GetRange())
		p.consumeTokensUntilType([]TokenType{TokenTypeNewLine})
	}

	return decoratorNode
}

// parseClassDef corresponds to _parseClassDef(). Pass nil for decorators to
// omit them.
//
// classdef: 'class' NAME ['(' [arglist] ')'] suite
func (p *Parser) parseClassDef(decorators []*DecoratorNode) *ClassNode {
	classToken := p.getKeywordToken(KeywordTypeClass)

	nameToken := p.getTokenIfIdentifier()
	if nameToken == nil {
		p.addSyntaxError(localization.LocMessage.ExpectedClassName(), p.peekToken(0).GetRange())
		nameToken = NewIdentifierToken(0, 0, common.Text{}, nil /* comments */)
	}

	var typeParameters *TypeParameterListNode
	possibleOpenBracket := p.peekToken(0)
	if possibleOpenBracket.GetType() == TokenTypeOpenBracket {
		typeParameters = p.parseTypeParameterList()

		if !p.parseOptions.IsStubFile && p.getLanguageVersion().IsLessThan(common.PythonVersion3_12) {
			p.addSyntaxError(localization.LocMessage.ClassTypeParametersIllegal(), typeParameters.GetRange())
		}
	}

	argList := []*ArgumentNode{}
	openParenToken := p.peekToken(0)
	if p.consumeTokenIfType(TokenTypeOpenParenthesis) {
		argList = p.parseArgList().Args

		if !p.consumeTokenIfType(TokenTypeCloseParenthesis) {
			p.addSyntaxError(localization.LocMessage.ExpectedCloseParen(), openParenToken.GetRange())
		}
	}

	suite := p.parseSuite(false /* isFunction */, p.parseOptions.SkipFunctionAndClassBody, nil)

	classNode := NewClassNode(classToken, NewNameNode(nameToken), suite, typeParameters)
	classNode.D.Arguments = argList
	for _, arg := range argList {
		setParent(arg, classNode)
	}

	if decorators != nil {
		classNode.D.Decorators = decorators
		if len(decorators) > 0 {
			for _, decorator := range decorators {
				setParent(decorator, classNode)
			}
			ExtendRange(classNode, decorators[0].GetRange())
		}
	}

	return classNode
}
