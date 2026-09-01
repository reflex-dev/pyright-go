/*
 * parser_simple.go
 *
 * Simple (one-line) statements, imports and type aliases, transliterated from
 * parser/parser.ts (pyright 1.1.412).
 */

package parser

import (
	"strconv"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// parseSimpleStatement corresponds to _parseSimpleStatement().
//
// simple_stmt: small_stmt (';' small_stmt)* [';'] NEWLINE
func (p *Parser) parseSimpleStatement() *StatementListNode {
	statement := NewStatementListNode(p.peekToken(0))

	for {
		// Swallow invalid tokens to make sure we make forward progress.
		if p.peekTokenType() == TokenTypeInvalid {
			invalidToken := p.getNextToken()
			r := invalidToken.GetRange()
			text := p.fileContents.Substring(r.Start, r.Start+r.Length)

			firstCharCode := text.CharCodeAt(0)

			// If the invalid token is a line-continuation backslash at the end of the file,
			// report a clearer error message consistent with Python: "Unexpected EOF".
			nextTok := p.peekToken(0)
			nextNextTok := p.peekToken(1)
			isBackslash := firstCharCode == common.CharBackslash
			isAtEofLineContinuation := isBackslash &&
				nextTok.GetType() == TokenTypeNewLine &&
				nextNextTok.GetType() == TokenTypeEndOfStream

			if isAtEofLineContinuation {
				p.addSyntaxError(localization.LocMessage.UnexpectedEof(), invalidToken.GetRange())
			} else {
				// Remove any non-printable characters.
				p.addSyntaxError(
					localization.LocMessage.InvalidTokenChars().Format(
						`\u`+strconv.FormatInt(int64(firstCharCode), 16),
					),
					invalidToken.GetRange(),
				)
			}

			p.consumeTokensUntilType([]TokenType{TokenTypeNewLine})
			break
		}

		smallStatement := p.parseSmallStatement()
		statement.D.Statements = append(statement.D.Statements, smallStatement)
		setParent(smallStatement, statement)
		ExtendRange(statement, smallStatement.GetRange())

		if smallStatement.GetNodeType() == ParseNodeTypeError {
			// No need to log an error here. We assume that
			// it was already logged by _parseSmallStatement.
			break
		}

		// Consume the semicolon if present.
		if !p.consumeTokenIfType(TokenTypeSemicolon) {
			break
		}

		nextTokenType := p.peekTokenType()
		if nextTokenType == TokenTypeNewLine || nextTokenType == TokenTypeEndOfStream {
			break
		}
	}

	if !p.consumeTokenIfType(TokenTypeNewLine) {
		p.addSyntaxError(localization.LocMessage.ExpectedNewlineOrSemicolon(), p.peekToken(0).GetRange())
	}

	return statement
}

// parseSmallStatement corresponds to _parseSmallStatement().
//
//	small_stmt: (expr_stmt | del_stmt | pass_stmt | flow_stmt |
//	            import_stmt | global_stmt | nonlocal_stmt | assert_stmt)
//	flow_stmt: break_stmt | continue_stmt | return_stmt | raise_stmt | yield_stmt
//	import_stmt: import_name | import_from
func (p *Parser) parseSmallStatement() ParseNode {
	if kw, ok := p.peekKeywordType(); ok {
		switch kw {
		case KeywordTypePass:
			return p.parsePassStatement()

		case KeywordTypeBreak:
			return p.parseBreakStatement()

		case KeywordTypeContinue:
			return p.parseContinueStatement()

		case KeywordTypeReturn:
			return p.parseReturnStatement()

		case KeywordTypeFrom:
			return p.parseFromStatement()

		case KeywordTypeImport:
			return p.parseImportStatement()

		case KeywordTypeGlobal:
			return p.parseGlobalStatement()

		case KeywordTypeNonlocal:
			return p.parseNonlocalStatement()

		case KeywordTypeRaise:
			return p.parseRaiseStatement()

		case KeywordTypeAssert:
			return p.parseAssertStatement()

		case KeywordTypeDel:
			return p.parseDelStatement()

		case KeywordTypeYield:
			return p.parseYieldExpression()

		case KeywordTypeLazy:
			// Lazy is considered a "soft" keyword, so we will treat it
			// as an identifier if it is not followed by "import" or "from".
			peekToken1 := p.peekToken(1)
			if peekToken1.GetType() == TokenTypeKeyword {
				kw1 := peekToken1.(*KeywordToken).KeywordType
				if kw1 == KeywordTypeImport || kw1 == KeywordTypeFrom {
					return p.parseLazyImportStatement()
				}
			}

		case KeywordTypeType:
			// Type is considered a "soft" keyword, so we will treat it
			// as an identifier if it is followed by an unexpected token.
			peekToken1 := p.peekToken(1)
			peekToken2 := p.peekToken(2)
			isInvalidTypeToken := true

			if peekToken1.GetType() == TokenTypeIdentifier ||
				(peekToken1.GetType() == TokenTypeKeyword && peekToken1.(*KeywordToken).IsSoftKeyword()) {
				if peekToken2.GetType() == TokenTypeOpenBracket {
					isInvalidTypeToken = false
				} else if peekToken2.GetType() == TokenTypeOperator &&
					peekToken2.(*OperatorToken).OperatorType == OperatorTypeAssign {
					isInvalidTypeToken = false
				}
			}

			if !isInvalidTypeToken {
				return p.parseTypeAliasStatement()
			}
		}
	}

	return p.parseExpressionStatement()
}

// parseReturnStatement corresponds to _parseReturnStatement().
//
// return_stmt: 'return' [testlist]
func (p *Parser) parseReturnStatement() *ReturnNode {
	returnToken := p.getKeywordToken(KeywordTypeReturn)

	returnNode := NewReturnNode(returnToken.GetRange())

	if !p.isInFunction {
		p.addSyntaxError(localization.LocMessage.ReturnOutsideFunction(), returnToken.GetRange())
	} else if p.isInExceptionGroup {
		p.addSyntaxError(localization.LocMessage.ReturnInExceptionGroup(), returnToken.GetRange())
	}

	if p.isInFinallyBlock && p.getLanguageVersion().IsGreaterOrEqualTo(common.PythonVersion3_14) {
		p.addSyntaxError(localization.LocMessage.FinallyReturn(), returnToken.GetRange())
	}

	if !p.isNextTokenNeverExpression() {
		returnExpr := p.parseTestOrStarListAsExpression(
			true, /* allowAssignmentExpression */
			true, /* allowMultipleUnpack */
			ErrorExpressionCategoryMissingExpression,
			func() string { return localization.LocMessage.ExpectedReturnExpr() },
		)
		p.reportConditionalErrorForStarTupleElement(returnExpr, common.PythonVersion3_8)
		returnNode.D.Expr = returnExpr
		setParent(returnNode.D.Expr, returnNode)
		ExtendRange(returnNode, returnExpr.GetRange())
	}

	return returnNode
}

// parseRaiseStatement corresponds to _parseRaiseStatement().
//
// raise_stmt: 'raise' [test ['from' test]]
// (old) raise_stmt: 'raise' [test [',' test [',' test]]]
func (p *Parser) parseRaiseStatement() *RaiseNode {
	raiseToken := p.getKeywordToken(KeywordTypeRaise)

	raiseNode := NewRaiseNode(raiseToken.GetRange())
	if !p.isNextTokenNeverExpression() {
		raiseNode.D.Expr = p.parseTestExpression(true /* allowAssignmentExpression */)
		setParent(raiseNode.D.Expr, raiseNode)
		ExtendRange(raiseNode, raiseNode.D.Expr.GetRange())

		if p.consumeTokenIfKeyword(KeywordTypeFrom) {
			raiseNode.D.FromExpr = p.parseTestExpression(true /* allowAssignmentExpression */)
			setParent(raiseNode.D.FromExpr, raiseNode)
			ExtendRange(raiseNode, raiseNode.D.FromExpr.GetRange())
		}
	}

	return raiseNode
}

// parseAssertStatement corresponds to _parseAssertStatement().
//
// assert_stmt: 'assert' test [',' test]
func (p *Parser) parseAssertStatement() *AssertNode {
	assertToken := p.getKeywordToken(KeywordTypeAssert)

	expr := p.parseTestExpression(false /* allowAssignmentExpression */)
	assertNode := NewAssertNode(assertToken, expr)

	if p.consumeTokenIfType(TokenTypeComma) {
		exceptionExpr := p.parseTestExpression(false /* allowAssignmentExpression */)
		assertNode.D.ExceptionExpr = exceptionExpr
		setParent(assertNode.D.ExceptionExpr, assertNode)
		ExtendRange(assertNode, exceptionExpr.GetRange())
	}

	return assertNode
}

// parseDelStatement corresponds to _parseDelStatement().
//
// del_stmt: 'del' exprlist
func (p *Parser) parseDelStatement() *DelNode {
	delToken := p.getKeywordToken(KeywordTypeDel)

	exprListResult := p.parseExpressionList(true /* allowStar */)
	if exprListResult.parseError == nil && len(exprListResult.list) == 0 {
		p.addSyntaxError(localization.LocMessage.ExpectedDelExpr(), p.peekToken(0).GetRange())
	}
	delNode := NewDelNode(delToken)
	delNode.D.Targets = exprListResult.list
	if len(delNode.D.Targets) > 0 {
		for _, expr := range delNode.D.Targets {
			setParent(expr, delNode)
		}
		ExtendRange(delNode, delNode.D.Targets[len(delNode.D.Targets)-1].GetRange())
	}
	return delNode
}

// parseLazyImportStatement corresponds to _parseLazyImportStatement().
//
// lazy_import_stmt: "lazy" import_stmt | "lazy" from_import_stmt
func (p *Parser) parseLazyImportStatement() ParseNode {
	lazyToken := p.getKeywordToken(KeywordTypeLazy)

	if !p.parseOptions.IsStubFile && p.getLanguageVersion().IsLessThan(common.PythonVersion3_15) {
		p.addSyntaxError(localization.LocMessage.LazyImportIllegal(), lazyToken.GetRange())
	}

	nextToken := p.peekToken(0)

	if nextToken.GetType() == TokenTypeKeyword && nextToken.(*KeywordToken).KeywordType == KeywordTypeFrom {
		node := p.parseFromStatement()
		node.D.IsLazy = true
		node.D.LazyToken = lazyToken
		ExtendRange(node, lazyToken.GetRange())

		// PEP 810 disallows wildcard imports with 'lazy'.
		if node.D.IsWildcardImport {
			// `node.d.wildcardToken ?? lazyToken`
			var r common.TextRange = lazyToken.GetRange()
			if node.D.WildcardToken != nil {
				r = node.D.WildcardToken.GetRange()
			}
			p.addSyntaxError(localization.LocMessage.LazyImportWildcardIllegal(), r)
		}

		return node
	}

	node := p.parseImportStatement()
	node.D.IsLazy = true
	node.D.LazyToken = lazyToken
	ExtendRange(node, lazyToken.GetRange())

	return node
}

// parseFromStatement corresponds to _parseFromStatement().
//
//	import_from: ('from' (('.' | '...')* dotted_name | ('.' | '...')+)
//	            'import' ('*' | '(' import_as_names ')' | import_as_names))
//	import_as_names: import_as_name (',' import_as_name)* [',']
//	import_as_name: NAME ['as' NAME]
func (p *Parser) parseFromStatement() *ImportFromNode {
	fromToken := p.getKeywordToken(KeywordTypeFrom)

	modName := p.parseDottedModuleName(true /* allowJustDots */)
	importFromNode := NewImportFromNode(fromToken, modName)

	// Handle imports from __future__ specially because they can
	// change the way we interpret the rest of the file.
	isFutureImport := modName.D.LeadingDots == 0 &&
		len(modName.D.NameParts) == 1 &&
		modName.D.NameParts[0].D.Value == "__future__"

	possibleInputToken := p.peekToken(0)
	if !p.consumeTokenIfKeyword(KeywordTypeImport) {
		p.addSyntaxError(localization.LocMessage.ExpectedImport(), p.peekToken(0).GetRange())
		if !modName.D.HasTrailingDot {
			importFromNode.D.MissingImport = true
		}
	} else {
		ExtendRange(importFromNode, possibleInputToken.GetRange())

		// Look for "*" token.
		possibleStarToken := p.peekToken(0)
		if p.consumeTokenIfOperator(OperatorTypeMultiply) {
			ExtendRange(importFromNode, possibleStarToken.GetRange())
			importFromNode.D.IsWildcardImport = true
			importFromNode.D.WildcardToken = possibleStarToken
			p.containsWildcardImport = true
		} else {
			openParenToken := p.peekToken(0)
			inParen := p.consumeTokenIfType(TokenTypeOpenParenthesis)
			var trailingCommaToken Token

			for {
				importName := p.getTokenIfIdentifier()
				if importName == nil {
					break
				}

				trailingCommaToken = nil

				importFromAsNode := NewImportFromAsNode(NewNameNode(importName))

				if p.consumeTokenIfKeyword(KeywordTypeAs) {
					aliasName := p.getTokenIfIdentifier()
					if aliasName == nil {
						p.addSyntaxError(localization.LocMessage.ExpectedImportAlias(), p.peekToken(0).GetRange())
					} else {
						importFromAsNode.D.Alias = NewNameNode(aliasName)
						setParent(importFromAsNode.D.Alias, importFromAsNode)
						ExtendRange(importFromAsNode, aliasName.GetRange())
					}
				}

				importFromNode.D.Imports = append(importFromNode.D.Imports, importFromAsNode)
				setParent(importFromAsNode, importFromNode)
				ExtendRange(importFromNode, importFromAsNode.GetRange())

				if isFutureImport {
					// Add the future import by name.
					p.futureImports[importName.Value] = true
				}

				nextToken := p.peekToken(0)
				if !p.consumeTokenIfType(TokenTypeComma) {
					break
				}
				trailingCommaToken = nextToken
			}

			if len(importFromNode.D.Imports) == 0 {
				p.addSyntaxError(localization.LocMessage.ExpectedImportSymbols(), p.peekToken(0).GetRange())
			}

			if inParen {
				importFromNode.D.UsesParens = true

				nextToken := p.peekToken(0)
				if !p.consumeTokenIfType(TokenTypeCloseParenthesis) {
					p.addSyntaxError(localization.LocMessage.ExpectedCloseParen(), openParenToken.GetRange())
				} else {
					ExtendRange(importFromNode, nextToken.GetRange())
				}
			} else if trailingCommaToken != nil {
				p.addSyntaxError(localization.LocMessage.TrailingCommaInFromImport(), trailingCommaToken.GetRange())
			}
		}
	}

	importedSymbols := map[string]bool{}
	for _, imp := range importFromNode.D.Imports {
		importedSymbols[imp.D.Name.D.Value] = true
	}
	nameParts := make([]string, 0, len(importFromNode.D.Module.D.NameParts))
	for _, part := range importFromNode.D.Module.D.NameParts {
		nameParts = append(nameParts, part.D.Value)
	}
	p.importedModules = append(p.importedModules, &ModuleImport{
		NameNode:        importFromNode.D.Module,
		LeadingDots:     importFromNode.D.Module.D.LeadingDots,
		NameParts:       nameParts,
		ImportedSymbols: importedSymbols,
	})

	isTypingImport := false
	if len(importFromNode.D.Module.D.NameParts) == 1 {
		firstNamePartValue := importFromNode.D.Module.D.NameParts[0].D.Value
		if firstNamePartValue == "typing" || firstNamePartValue == "typing_extensions" {
			isTypingImport = true
		}
	}

	if isTypingImport {
		typingSymbolsOfInterest := []string{"Literal", "TypeAlias", "Annotated"}

		if importFromNode.D.IsWildcardImport {
			for _, s := range typingSymbolsOfInterest {
				p.typingSymbolAliases[s] = s
			}
		} else {
			for _, imp := range importFromNode.D.Imports {
				for _, s := range typingSymbolsOfInterest {
					if s == imp.D.Name.D.Value {
						// `imp.d.alias?.d.value || imp.d.name.d.value`
						key := imp.D.Name.D.Value
						if imp.D.Alias != nil && imp.D.Alias.D.Value != "" {
							key = imp.D.Alias.D.Value
						}
						p.typingSymbolAliases[key] = imp.D.Name.D.Value
						break
					}
				}
			}
		}
	}

	return importFromNode
}

// parseImportStatement corresponds to _parseImportStatement().
//
// import_name: 'import' dotted_as_names
// dotted_as_names: dotted_as_name (',' dotted_as_name)*
// dotted_as_name: dotted_name ['as' NAME]
func (p *Parser) parseImportStatement() *ImportNode {
	importToken := p.getKeywordToken(KeywordTypeImport)

	importNode := NewImportNode(importToken.GetRange())

	for {
		modName := p.parseDottedModuleName(false /* allowJustDots */)

		importAsNode := NewImportAsNode(modName)

		if p.consumeTokenIfKeyword(KeywordTypeAs) {
			aliasToken := p.getTokenIfIdentifier()
			if aliasToken != nil {
				importAsNode.D.Alias = NewNameNode(aliasToken)
				setParent(importAsNode.D.Alias, importAsNode)
				ExtendRange(importAsNode, importAsNode.D.Alias.GetRange())
			} else {
				p.addSyntaxError(localization.LocMessage.ExpectedImportAlias(), p.peekToken(0).GetRange())
			}
		}

		if importAsNode.D.Module.D.LeadingDots > 0 {
			p.addSyntaxError(localization.LocMessage.RelativeImportNotAllowed(), importAsNode.D.Module.GetRange())
		}

		importNode.D.List = append(importNode.D.List, importAsNode)
		setParent(importAsNode, importNode)

		nameParts := make([]string, 0, len(importAsNode.D.Module.D.NameParts))
		for _, part := range importAsNode.D.Module.D.NameParts {
			nameParts = append(nameParts, part.D.Value)
		}

		if importAsNode.D.Alias != nil ||
			importAsNode.D.Module.D.LeadingDots > 0 ||
			len(importAsNode.D.Module.D.NameParts) == 0 {
			p.importedModules = append(p.importedModules, &ModuleImport{
				NameNode:        importAsNode.D.Module,
				LeadingDots:     importAsNode.D.Module.D.LeadingDots,
				NameParts:       nameParts,
				ImportedSymbols: nil,
			})
		} else {
			// Implicitly import all modules in the multi-part name if we
			// are not assigning the final module to an alias.
			for index := range importAsNode.D.Module.D.NameParts {
				p.importedModules = append(p.importedModules, &ModuleImport{
					NameNode:        importAsNode.D.Module,
					LeadingDots:     importAsNode.D.Module.D.LeadingDots,
					NameParts:       nameParts[0 : index+1],
					ImportedSymbols: nil,
				})
			}
		}

		if len(modName.D.NameParts) == 1 {
			firstNamePartValue := modName.D.NameParts[0].D.Value
			if firstNamePartValue == "typing" || firstNamePartValue == "typing_extensions" {
				// `importAsNode.d.alias?.d.value || firstNamePartValue`
				alias := firstNamePartValue
				if importAsNode.D.Alias != nil && importAsNode.D.Alias.D.Value != "" {
					alias = importAsNode.D.Alias.D.Value
				}
				p.typingImportAliases = append(p.typingImportAliases, alias)
			}
		}

		if !p.consumeTokenIfType(TokenTypeComma) {
			break
		}
	}

	if len(importNode.D.List) > 0 {
		ExtendRange(importNode, importNode.D.List[len(importNode.D.List)-1].GetRange())
	}

	return importNode
}

// parseDottedModuleName corresponds to _parseDottedModuleName().
//
// ('.' | '...')* dotted_name | ('.' | '...')+
// dotted_name: NAME ('.' NAME)*
func (p *Parser) parseDottedModuleName(allowJustDots bool) *ModuleNameNode {
	moduleNameNode := NewModuleNameNode(p.peekToken(0).GetRange())

	for {
		// `this._getTokenIfType(Ellipsis) ?? this._getTokenIfType(Dot)`
		token := p.getTokenIfType(TokenTypeEllipsis)
		if token == nil {
			token = p.getTokenIfType(TokenTypeDot)
		}
		if token != nil {
			if token.GetType() == TokenTypeEllipsis {
				moduleNameNode.D.LeadingDots += 3
			} else {
				moduleNameNode.D.LeadingDots++
			}

			ExtendRange(moduleNameNode, token.GetRange())
		} else {
			break
		}
	}

	for {
		identifier := p.getTokenIfIdentifier()
		if identifier == nil {
			if !allowJustDots || moduleNameNode.D.LeadingDots == 0 || len(moduleNameNode.D.NameParts) > 0 {
				p.addSyntaxError(localization.LocMessage.ExpectedModuleName(), p.peekToken(0).GetRange())
				moduleNameNode.D.HasTrailingDot = true
			}
			break
		}

		namePart := NewNameNode(identifier)
		moduleNameNode.D.NameParts = append(moduleNameNode.D.NameParts, namePart)
		setParent(namePart, moduleNameNode)
		ExtendRange(moduleNameNode, namePart.GetRange())

		nextToken := p.peekToken(0)
		if !p.consumeTokenIfType(TokenTypeDot) {
			break
		}

		// Extend the module name to include the dot.
		ExtendRange(moduleNameNode, nextToken.GetRange())
	}

	return moduleNameNode
}

// parseTypeAliasStatement corresponds to _parseTypeAliasStatement().
//
// type_alias_stmt: "type" name [type_param_seq] = expr
func (p *Parser) parseTypeAliasStatement() *TypeAliasNode {
	typeToken := p.getKeywordToken(KeywordTypeType)

	if !p.parseOptions.IsStubFile && p.getLanguageVersion().IsLessThan(common.PythonVersion3_12) {
		p.addSyntaxError(localization.LocMessage.TypeAliasStatementIllegal(), typeToken.GetRange())
	}

	nameToken := p.getTokenIfIdentifier()
	common.Assert(nameToken != nil, "")
	name := NewNameNode(nameToken)

	var typeParameters *TypeParameterListNode
	if p.peekToken(0).GetType() == TokenTypeOpenBracket {
		typeParameters = p.parseTypeParameterList()
	}

	assignToken := p.peekToken(0)
	if assignToken.GetType() != TokenTypeOperator ||
		assignToken.(*OperatorToken).OperatorType != OperatorTypeAssign {
		p.addSyntaxError(localization.LocMessage.ExpectedEquals(), assignToken.GetRange())
	} else {
		p.getNextToken()
	}

	wasParsingTypeAnnotation := p.isParsingTypeAnnotation
	p.isParsingTypeAnnotation = true
	expression := p.parseTestExpression(false /* allowAssignmentExpression */)
	p.isParsingTypeAnnotation = wasParsingTypeAnnotation

	return NewTypeAliasNode(typeToken, name, expression, typeParameters)
}

// parseTypeParameterList corresponds to _parseTypeParameterList().
//
// type_param_seq: '[' (type_param ',')+ ']'
func (p *Parser) parseTypeParameterList() *TypeParameterListNode {
	typeVariableNodes := []*TypeParameterNode{}

	openBracketToken := p.getNextToken()
	common.Assert(openBracketToken.GetType() == TokenTypeOpenBracket, "")

	for {
		firstToken := p.peekToken(0)

		if firstToken.GetType() == TokenTypeCloseBracket {
			if len(typeVariableNodes) == 0 {
				p.addSyntaxError(localization.LocMessage.TypeParametersMissing(), p.peekToken(0).GetRange())
			}
			break
		}

		typeVarNode := p.parseTypeParameter()
		if typeVarNode == nil {
			break
		}

		typeVariableNodes = append(typeVariableNodes, typeVarNode)

		if !p.consumeTokenIfType(TokenTypeComma) {
			break
		}
	}

	closingToken := p.peekToken(0)
	if closingToken.GetType() != TokenTypeCloseBracket {
		p.addSyntaxError(localization.LocMessage.ExpectedCloseBracket(), p.peekToken(0).GetRange())
		p.consumeTokensUntilType([]TokenType{TokenTypeNewLine, TokenTypeCloseBracket, TokenTypeColon})
	} else {
		p.getNextToken()
	}

	return NewTypeParameterListNode(openBracketToken, closingToken, typeVariableNodes)
}

// parseTypeParameter corresponds to _parseTypeParameter(). It returns nil where
// the TypeScript version returns undefined.
//
// type_param: ['*' | '**'] NAME [':' bound_expr] ['=' default_expr]
func (p *Parser) parseTypeParameter() *TypeParameterNode {
	typeParamCategory := TypeParamKindTypeVar
	if p.consumeTokenIfOperator(OperatorTypeMultiply) {
		typeParamCategory = TypeParamKindTypeVarTuple
	} else if p.consumeTokenIfOperator(OperatorTypePower) {
		typeParamCategory = TypeParamKindParamSpec
	}

	nameToken := p.getTokenIfIdentifier()
	if nameToken == nil {
		p.addSyntaxError(localization.LocMessage.ExpectedTypeParameterName(), p.peekToken(0).GetRange())
		return nil
	}

	name := NewNameNode(nameToken)

	var boundExpression ExpressionNode
	if p.consumeTokenIfType(TokenTypeColon) {
		boundExpression = p.parseExpression(false /* allowUnpack */)

		if typeParamCategory != TypeParamKindTypeVar {
			p.addSyntaxError(localization.LocMessage.TypeParameterBoundNotAllowed(), boundExpression.GetRange())
		}
	}

	var defaultExpression ExpressionNode
	if p.consumeTokenIfOperator(OperatorTypeAssign) {
		defaultExpression = p.parseExpression(typeParamCategory == TypeParamKindTypeVarTuple /* allowUnpack */)

		if !p.parseOptions.IsStubFile && p.getLanguageVersion().IsLessThan(common.PythonVersion3_13) {
			p.addSyntaxError(localization.LocMessage.TypeVarDefaultIllegal(), defaultExpression.GetRange())
		}
	}

	return NewTypeParameterNode(name, typeParamCategory, boundExpression, defaultExpression)
}

// parseExpressionStatement corresponds to _parseExpressionStatement().
//
//	expr_stmt: testlist_star_expr (annassign | augassign (yield_expr | testlist) |
//	                    ('=' (yield_expr | testlist_star_expr))*)
//	testlist_star_expr: (test|star_expr) (',' (test|star_expr))* [',']
//	annassign: ':' test ['=' (yield_expr | testlist_star_expr)]
//	augassign: ('+=' | '-=' | '*=' | '@=' | '/=' | '%=' | '&=' | '|=' | '^=' |
//	            '<<=' | '>>=' | '**=' | '//=')
func (p *Parser) parseExpressionStatement() ExpressionNode {
	leftExpr := p.parseTestOrStarListAsExpression(
		false, /* allowAssignmentExpression */
		false, /* allowMultipleUnpack */
		ErrorExpressionCategoryMissingExpression,
		func() string { return localization.LocMessage.ExpectedExpr() },
	)
	var annotationExpr ExpressionNode

	if leftExpr.GetNodeType() == ParseNodeTypeError {
		return leftExpr
	}

	// Is this a type annotation assignment?
	if p.consumeTokenIfType(TokenTypeColon) {
		annotationExpr = p.parseTypeAnnotation(false /* allowUnpack */)
		leftExpr = NewTypeAnnotationNode(leftExpr, annotationExpr)

		if !p.parseOptions.IsStubFile && p.getLanguageVersion().IsLessThan(common.PythonVersion3_6) {
			p.addSyntaxError(localization.LocMessage.VarAnnotationIllegal(), annotationExpr.GetRange())
		}

		if !p.consumeTokenIfOperator(OperatorTypeAssign) {
			return leftExpr
		}

		// This is an unfortunate hack that's necessary to accommodate 'TypeAlias'
		// declarations properly. We need to treat this assignment differently than
		// most because the expression on the right side is treated like a type
		// annotation and therefore allows string-literal forward declarations.
		isTypeAliasDeclaration := p.isTypingAnnotation(annotationExpr, "TypeAlias")

		wasParsingTypeAnnotation := p.isParsingTypeAnnotation
		if isTypeAliasDeclaration {
			p.isParsingTypeAnnotation = true
		}

		rightExpr := p.tryParseYieldExpression()
		if rightExpr == nil {
			rightExpr = p.parseTestOrStarListAsExpression(
				false, /* allowAssignmentExpression */
				true,  /* allowMultipleUnpack */
				ErrorExpressionCategoryMissingExpression,
				func() string { return localization.LocMessage.ExpectedAssignRightHandExpr() },
			)
		}

		p.isParsingTypeAnnotation = wasParsingTypeAnnotation

		return NewAssignmentNode(leftExpr, rightExpr)
	}

	// Is this a simple assignment?
	if p.consumeTokenIfOperator(OperatorTypeAssign) {
		return p.parseChainAssignments(leftExpr)
	}

	if op, ok := p.peekOperatorType(); ok && IsOperatorAssignment(op) {
		operatorToken := p.getNextToken().(*OperatorToken)

		rightExpr := p.tryParseYieldExpression()
		if rightExpr == nil {
			rightExpr = p.parseTestOrStarListAsExpression(
				false, /* allowAssignmentExpression */
				true,  /* allowMultipleUnpack */
				ErrorExpressionCategoryMissingExpression,
				func() string { return localization.LocMessage.ExpectedBinaryRightHandExpr() },
			)
		}
		p.reportConditionalErrorForStarTupleElement(rightExpr, common.PythonVersion3_9)

		// Make a shallow copy of the dest expression but give it a new ID.
		destExpr := shallowCopyWithNewID(leftExpr)

		return NewAugmentedAssignmentNode(leftExpr, rightExpr, operatorToken.OperatorType, destExpr)
	}

	return leftExpr
}

// parseChainAssignments corresponds to _parseChainAssignments().
func (p *Parser) parseChainAssignments(leftExpr ExpressionNode) ExpressionNode {
	// Make a list of assignment targets.
	assignmentTargets := []ExpressionNode{leftExpr}
	var rightExpr ExpressionNode

	for {
		rightExpr = p.tryParseYieldExpression()
		if rightExpr == nil {
			rightExpr = p.parseTestOrStarListAsExpression(
				false, /* allowAssignmentExpression */
				true,  /* allowMultipleUnpack */
				ErrorExpressionCategoryMissingExpression,
				func() string { return localization.LocMessage.ExpectedAssignRightHandExpr() },
			)
		}

		if rightExpr.GetNodeType() == ParseNodeTypeError {
			break
		}

		// Continue until we've consumed the entire chain.
		if !p.consumeTokenIfOperator(OperatorTypeAssign) {
			break
		}

		assignmentTargets = append(assignmentTargets, rightExpr)
	}

	// Create a tree of assignment expressions starting with the first one.
	// The final RHS value is assigned to the targets left to right in Python.
	assignmentNode := NewAssignmentNode(assignmentTargets[0], rightExpr)

	// Look for a type annotation comment at the end of the line.
	typeAnnotationComment := p.parseVariableTypeAnnotationComment()
	if typeAnnotationComment != nil {
		if len(assignmentTargets) > 1 {
			// Type comments are not allowed for chained assignments for the
			// same reason that variable type annotations don't support
			// chained assignments. Note that a type comment was used here
			// so it can be later reported as an error by the binder.
			assignmentNode.D.ChainedAnnotationComment = typeAnnotationComment
		} else {
			assignmentNode.D.AnnotationComment = typeAnnotationComment
			setParent(assignmentNode.D.AnnotationComment, assignmentNode)
			ExtendRange(assignmentNode, assignmentNode.D.AnnotationComment.GetRange())
		}
	}

	for index, target := range assignmentTargets {
		if index > 0 {
			assignmentNode = NewAssignmentNode(target, assignmentNode)
		}
	}

	return assignmentNode
}
