/*
 * parser_trailers.go
 *
 * The non-linear part of the expression grammar, transliterated from
 * parser/parser.ts (pyright 1.1.412): atom trailers (calls, subscripts, member
 * access), argument and subscript lists, slices, and the bracketed displays
 * (tuple, list, dict/set).
 *
 * Everything here recurses back to the top of the expression grammar, which is
 * why it lands as one batch rather than incrementally.
 */

package parser

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// isTypingAnnotation corresponds to _isTypingAnnotation().
//
// Determines whether the expression refers to a type exported by the typing or
// typing_extensions modules. We can directly evaluate the types at binding
// time. We assume here that the code isn't making use of some custom type alias
// to refer to the typing types.
func (p *Parser) isTypingAnnotation(typeAnnotation ExpressionNode, name string) bool {
	switch node := typeAnnotation.(type) {
	case *NameNode:
		if alias, ok := p.typingSymbolAliases[node.D.Value]; ok && alias == name {
			return true
		}
	case *MemberAccessNode:
		if leftName, ok := node.D.LeftExpr.(*NameNode); ok && node.D.Member.D.Value == name {
			baseName := leftName.D.Value
			for _, alias := range p.typingImportAliases {
				if alias == baseName {
					return true
				}
			}
			return false
		}
	}

	return false
}

// parseAtomExpression corresponds to _parseAtomExpression().
//
// atom_expr: ['await'] atom trailer*
// trailer: '(' [arglist] ')' | '[' subscriptlist ']' | '.' NAME
func (p *Parser) parseAtomExpression() ExpressionNode {
	var awaitToken *KeywordToken
	if kw, ok := p.peekKeywordType(); ok && kw == KeywordTypeAwait {
		awaitToken = p.getKeywordToken(KeywordTypeAwait)
		if p.getLanguageVersion().IsLessThan(common.PythonVersion3_5) {
			p.addSyntaxError(localization.LocMessage.AwaitIllegal(), awaitToken.GetRange())
		}
	}

	atomExpression := p.parseAtom()
	if atomExpression.GetNodeType() == ParseNodeTypeError {
		return atomExpression
	}

	// Consume trailers.
	for {
		// Is it a function call?
		startOfTrailerToken := p.peekToken(0)
		if p.consumeTokenIfType(TokenTypeOpenParenthesis) {
			// Generally, function calls are not allowed within type annotations,
			// but they are permitted in "Annotated" annotations.
			wasParsingTypeAnnotation := p.isParsingTypeAnnotation
			p.isParsingTypeAnnotation = false

			argListResult := p.parseArgList()
			callNode := NewCallNode(atomExpression, argListResult.Args, argListResult.TrailingComma)

			if len(argListResult.Args) > 1 || argListResult.TrailingComma {
				for _, arg := range argListResult.Args {
					if comp, ok := arg.D.ValueExpr.(*ComprehensionNode); ok {
						if !comp.D.HasParens {
							p.addSyntaxError(localization.LocMessage.GeneratorNotParenthesized(), comp.GetRange())
						}
					}
				}
			}

			nextToken := p.peekToken(0)
			isArgListTerminated := false
			if !p.consumeTokenIfType(TokenTypeCloseParenthesis) {
				p.addSyntaxError(localization.LocMessage.ExpectedCloseParen(), startOfTrailerToken.GetRange())

				// Consume the remainder of tokens on the line for error
				// recovery.
				p.consumeTokensUntilType([]TokenType{TokenTypeNewLine})

				// Extend the node's range to include the rest of the line.
				// This helps the signatureHelpProvider.
				ExtendRange(callNode, p.peekToken(0).GetRange())
			} else {
				ExtendRange(callNode, nextToken.GetRange())
				isArgListTerminated = true
			}

			p.isParsingTypeAnnotation = wasParsingTypeAnnotation

			maxDepth := p.maxChildDepthMap[atomExpression.NodeBase().ID]
			if maxDepth >= maxChildNodeDepth {
				atomExpression = NewErrorNode(callNode.GetRange(), ErrorExpressionCategoryMaxDepthExceeded, nil, nil)
				p.addSyntaxError(localization.LocMessage.MaxParseDepthExceeded(), atomExpression.GetRange())
			} else {
				atomExpression = callNode
				p.maxChildDepthMap[callNode.ID] = maxDepth + 1
			}

			// If the argument list wasn't terminated, break out of the loop
			if !isArgListTerminated {
				break
			}
		} else if p.consumeTokenIfType(TokenTypeOpenBracket) {
			// Is it an index operator?

			// This is an unfortunate hack that's necessary to accommodate 'Literal'
			// and 'Annotated' type annotations properly. We need to suspend treating
			// strings as type annotations within a Literal or Annotated subscript.
			wasParsingIndexTrailer := p.isParsingIndexTrailer
			wasParsingTypeAnnotation := p.isParsingTypeAnnotation

			if p.isTypingAnnotation(atomExpression, "Literal") ||
				p.isTypingAnnotation(atomExpression, "Annotated") {
				p.isParsingTypeAnnotation = false
			}

			p.isParsingIndexTrailer = true
			subscriptList := p.parseSubscriptList()
			p.isParsingTypeAnnotation = wasParsingTypeAnnotation
			p.isParsingIndexTrailer = wasParsingIndexTrailer

			closingToken := p.peekToken(0)

			indexNode := NewIndexNode(atomExpression, subscriptList.list, subscriptList.trailingComma, closingToken)

			// `extendRange(indexNode, indexNode)` in the original -- extending a
			// node over its own range is a no-op. Kept for the record.
			ExtendRange(indexNode, indexNode.GetRange())

			if !p.consumeTokenIfType(TokenTypeCloseBracket) {
				// Handle the error case, but don't use the error node in this
				// case because it creates problems for the completion provider.
				p.handleExpressionParseError(
					ErrorExpressionCategoryMissingIndexCloseBracket,
					localization.LocMessage.ExpectedCloseBracket(),
					startOfTrailerToken,
					indexNode,
					nil,
				)
			}

			maxDepth := p.maxChildDepthMap[atomExpression.NodeBase().ID]
			if maxDepth >= maxChildNodeDepth {
				atomExpression = NewErrorNode(indexNode.GetRange(), ErrorExpressionCategoryMaxDepthExceeded, nil, nil)
				p.addSyntaxError(localization.LocMessage.MaxParseDepthExceeded(), atomExpression.GetRange())
			} else {
				atomExpression = indexNode
				p.maxChildDepthMap[indexNode.ID] = maxDepth + 1
			}
		} else if p.consumeTokenIfType(TokenTypeDot) {
			// Is it a member access?
			memberName := p.getTokenIfIdentifier()
			if memberName == nil {
				return p.handleExpressionParseError(
					ErrorExpressionCategoryMissingMemberAccessName,
					localization.LocMessage.ExpectedMemberName(),
					startOfTrailerToken,
					atomExpression,
					[]TokenType{TokenTypeKeyword},
				)
			}

			memberAccessNode := NewMemberAccessNode(atomExpression, NewNameNode(memberName))

			maxDepth := p.maxChildDepthMap[atomExpression.NodeBase().ID]
			if maxDepth >= maxChildNodeDepth {
				atomExpression = NewErrorNode(memberAccessNode.GetRange(), ErrorExpressionCategoryMaxDepthExceeded, nil, nil)
				p.addSyntaxError(localization.LocMessage.MaxParseDepthExceeded(), atomExpression.GetRange())
			} else {
				atomExpression = memberAccessNode
				p.maxChildDepthMap[memberAccessNode.ID] = maxDepth + 1
			}
		} else {
			break
		}
	}

	if awaitToken != nil {
		return NewAwaitNode(awaitToken, atomExpression)
	}

	return atomExpression
}

// parseSubscriptList corresponds to _parseSubscriptList().
//
// subscriptlist: subscript (',' subscript)* [',']
func (p *Parser) parseSubscriptList() subscriptListResult {
	argList := []*ArgumentNode{}
	sawKeywordArg := false
	trailingComma := false

	for {
		firstToken := p.peekToken(0)

		if firstToken.GetType() != TokenTypeColon && p.isNextTokenNeverExpression() {
			break
		}

		argType := ArgCategorySimple
		if p.consumeTokenIfOperator(OperatorTypeMultiply) {
			argType = ArgCategoryUnpackedList
		} else if p.consumeTokenIfOperator(OperatorTypePower) {
			argType = ArgCategoryUnpackedDictionary
		}

		startOfSubscriptIndex := p.tokenIndex
		valueExpr := p.parsePossibleSlice()
		var nameIdentifier *IdentifierToken

		// Is this a keyword argument?
		if argType == ArgCategorySimple {
			if p.consumeTokenIfOperator(OperatorTypeAssign) {
				nameExpr := valueExpr
				valueExpr = p.parsePossibleSlice()

				if nameNode, ok := nameExpr.(*NameNode); ok {
					nameIdentifier = nameNode.D.Token
				} else {
					p.addSyntaxError(localization.LocMessage.ExpectedParamName(), nameExpr.GetRange())
				}
			} else if op, ok := p.peekOperatorType(); valueExpr.GetNodeType() == ParseNodeTypeName && ok && op == OperatorTypeWalrus {
				p.tokenIndex = startOfSubscriptIndex
				valueExpr = p.parseTestExpression(true /* allowAssignmentExpression */)

				// Python 3.10 and newer allow assignment expressions to be used inside of a subscript.
				if !p.parseOptions.IsStubFile && p.getLanguageVersion().IsLessThan(common.PythonVersion3_10) {
					p.addSyntaxError(localization.LocMessage.AssignmentExprInSubscript(), valueExpr.GetRange())
				}
			}
		}

		argNode := NewArgumentNode(firstToken, valueExpr, argType)
		if nameIdentifier != nil {
			argNode.D.Name = NewNameNode(nameIdentifier)
			setParent(argNode.D.Name, argNode)
		}

		if argNode.D.Name != nil {
			sawKeywordArg = true
		} else if sawKeywordArg && argNode.D.ArgCategory == ArgCategorySimple {
			p.addSyntaxError(localization.LocMessage.PositionArgAfterNamedArg(), argNode.GetRange())
		}
		argList = append(argList, argNode)

		if argNode.D.Name != nil {
			p.addSyntaxError(localization.LocMessage.KeywordSubscriptIllegal(), argNode.D.Name.GetRange())
		}

		if argType != ArgCategorySimple {
			unpackListAllowed := p.parseOptions.IsStubFile ||
				p.isParsingQuotedText ||
				p.getLanguageVersion().IsGreaterOrEqualTo(common.PythonVersion3_11)

			if argType == ArgCategoryUnpackedList && !unpackListAllowed {
				p.addSyntaxError(localization.LocMessage.UnpackedSubscriptIllegal(), argNode.GetRange())
			}

			if argType == ArgCategoryUnpackedDictionary {
				p.addSyntaxError(localization.LocMessage.UnpackedDictSubscriptIllegal(), argNode.GetRange())
			}
		}

		if !p.consumeTokenIfType(TokenTypeComma) {
			trailingComma = false
			break
		}

		trailingComma = true
	}

	// An empty subscript list is illegal.
	if len(argList) == 0 {
		errorNode := p.handleExpressionParseError(
			ErrorExpressionCategoryMissingIndexOrSlice,
			localization.LocMessage.ExpectedSliceIndex(),
			nil, /* targetToken */
			nil, /* childNode */
			[]TokenType{TokenTypeCloseBracket},
		)
		argList = append(argList, NewArgumentNode(p.peekToken(0), errorNode, ArgCategorySimple))
	}

	return subscriptListResult{list: argList, trailingComma: trailingComma}
}

// parsePossibleSlice corresponds to _parsePossibleSlice().
//
// subscript: test | [test] ':' [test] [sliceop]
// sliceop: ':' [test]
func (p *Parser) parsePossibleSlice() ExpressionNode {
	firstToken := p.peekToken(0)
	sliceExpressions := [3]ExpressionNode{nil, nil, nil}
	sliceIndex := 0
	sawColon := false

	for {
		nextTokenType := p.peekTokenType()
		if nextTokenType == TokenTypeCloseBracket || nextTokenType == TokenTypeComma {
			break
		}

		if nextTokenType != TokenTypeColon {
			// Python 3.10 and newer allow assignment expressions to be used inside of a subscript.
			allowAssignmentExpression := p.parseOptions.IsStubFile ||
				p.getLanguageVersion().IsGreaterOrEqualTo(common.PythonVersion3_10)
			sliceExpressions[sliceIndex] = p.parseTestExpression(allowAssignmentExpression)
		}
		sliceIndex++

		if sliceIndex >= 3 || !p.consumeTokenIfType(TokenTypeColon) {
			break
		}
		sawColon = true
	}

	// If this was a simple expression with no colons return it.
	if !sawColon {
		if sliceExpressions[0] != nil {
			return sliceExpressions[0]
		}

		return NewErrorNode(p.peekToken(0).GetRange(), ErrorExpressionCategoryMissingIndexOrSlice, nil, nil)
	}

	sliceNode := NewSliceNode(firstToken.GetRange())
	sliceNode.D.StartValue = sliceExpressions[0]
	if sliceNode.D.StartValue != nil {
		setParent(sliceNode.D.StartValue, sliceNode)
	}
	sliceNode.D.EndValue = sliceExpressions[1]
	if sliceNode.D.EndValue != nil {
		setParent(sliceNode.D.EndValue, sliceNode)
	}
	sliceNode.D.StepValue = sliceExpressions[2]
	if sliceNode.D.StepValue != nil {
		setParent(sliceNode.D.StepValue, sliceNode)
	}

	// `sliceExpressions[2] || sliceExpressions[1] || sliceExpressions[0]`
	var extension ExpressionNode
	switch {
	case sliceExpressions[2] != nil:
		extension = sliceExpressions[2]
	case sliceExpressions[1] != nil:
		extension = sliceExpressions[1]
	default:
		extension = sliceExpressions[0]
	}
	if extension != nil {
		ExtendRange(sliceNode, extension.GetRange())
	}

	return sliceNode
}

// parseArgList corresponds to _parseArgList().
//
// arglist: argument (',' argument)*  [',']
func (p *Parser) parseArgList() ArgListResult {
	argList := []*ArgumentNode{}
	sawKeywordArg := false
	sawUnpackedKeywordArg := false
	trailingComma := false

	for {
		nextTokenType := p.peekTokenType()
		if nextTokenType == TokenTypeCloseParenthesis ||
			nextTokenType == TokenTypeNewLine ||
			nextTokenType == TokenTypeEndOfStream {
			break
		}

		trailingComma = false
		arg := p.parseArgument()
		if arg.D.Name != nil {
			sawKeywordArg = true
		} else {
			if sawKeywordArg && arg.D.ArgCategory == ArgCategorySimple {
				p.addSyntaxError(localization.LocMessage.PositionArgAfterNamedArg(), arg.GetRange())
			}

			if sawUnpackedKeywordArg && arg.D.ArgCategory != ArgCategoryUnpackedDictionary {
				p.addSyntaxError(localization.LocMessage.PositionArgAfterUnpackedDictArg(), arg.GetRange())
			}
		}
		if arg.D.ArgCategory == ArgCategoryUnpackedDictionary {
			sawUnpackedKeywordArg = true
		}
		argList = append(argList, arg)

		if !p.consumeTokenIfType(TokenTypeComma) {
			break
		}

		trailingComma = true
	}

	return ArgListResult{Args: argList, TrailingComma: trailingComma}
}

// parseArgument corresponds to _parseArgument().
//
//	argument: ( test [comp_for] |
//	            test '=' test |
//	            '**' test |
//	            '*' test )
func (p *Parser) parseArgument() *ArgumentNode {
	firstToken := p.peekToken(0)

	argType := ArgCategorySimple
	if p.consumeTokenIfOperator(OperatorTypeMultiply) {
		argType = ArgCategoryUnpackedList
	} else if p.consumeTokenIfOperator(OperatorTypePower) {
		argType = ArgCategoryUnpackedDictionary
	}

	valueExpr := p.parseTestExpression(true /* allowAssignmentExpression */)
	var nameIdentifier *IdentifierToken

	if argType == ArgCategorySimple {
		if p.consumeTokenIfOperator(OperatorTypeAssign) {
			nameExpr := valueExpr
			valueExpr = p.parseTestExpression(false /* allowAssignmentExpression */)

			if nameNode, ok := nameExpr.(*NameNode); ok {
				nameIdentifier = nameNode.D.Token
			} else {
				p.addSyntaxError(localization.LocMessage.ExpectedParamName(), nameExpr.GetRange())
			}
		} else {
			comprehension := p.tryParseComprehension(valueExpr, true /* isGenerator */)
			if comprehension != nil {
				valueExpr = comprehension
			}
		}
	}

	argNode := NewArgumentNode(firstToken, valueExpr, argType)
	if nameIdentifier != nil {
		argNode.D.Name = NewNameNode(nameIdentifier)
		setParent(argNode.D.Name, argNode)
	}

	return argNode
}

// parseTupleAtom corresponds to _parseTupleAtom().
//
// ('(' [yield_expr | testlist_comp] ')'
// testlist_comp: (test | star_expr) (comp_for | (',' (test | star_expr))* [','])
func (p *Parser) parseTupleAtom() ExpressionNode {
	startParen := p.getNextToken()
	common.Assert(startParen.GetType() == TokenTypeOpenParenthesis, "")

	yieldExpr := p.tryParseYieldExpression()
	if yieldExpr != nil {
		if p.peekTokenType() != TokenTypeCloseParenthesis {
			return p.handleExpressionParseError(
				ErrorExpressionCategoryMissingTupleCloseParen,
				localization.LocMessage.ExpectedCloseParen(),
				startParen,
				yieldExpr,
				nil,
			)
		}
		ExtendRange(yieldExpr, p.getNextToken().GetRange())

		return yieldExpr
	}

	exprListResult := p.parseTestListWithComprehension(true /* isGenerator */)
	tupleOrExpression := p.makeExpressionOrTuple(exprListResult, true /* enclosedInParens */)

	ExtendRange(tupleOrExpression, startParen.GetRange())

	if p.peekTokenType() != TokenTypeCloseParenthesis {
		// `exprListResult.parseError ?? tupleOrExpression`
		childNode := tupleOrExpression
		if exprListResult.parseError != nil {
			childNode = exprListResult.parseError
		}

		return p.handleExpressionParseError(
			ErrorExpressionCategoryMissingTupleCloseParen,
			localization.LocMessage.ExpectedCloseParen(),
			startParen,
			childNode,
			nil,
		)
	}
	ExtendRange(tupleOrExpression, p.getNextToken().GetRange())

	return tupleOrExpression
}

// parseListAtom corresponds to _parseListAtom().
//
// '[' [testlist_comp] ']'
// testlist_comp: (test | star_expr) (comp_for | (',' (test | star_expr))* [','])
func (p *Parser) parseListAtom() ExpressionNode {
	startBracket := p.getNextToken()
	common.Assert(startBracket.GetType() == TokenTypeOpenBracket, "")

	exprListResult := p.parseTestListWithComprehension(false /* isGenerator */)
	closeBracket := p.peekToken(0)

	// The original declares this as a hoisted inner function so it can be
	// called from both the error path and the success path.
	createList := func() *ListNode {
		listAtom := NewListNode(startBracket.GetRange())

		if closeBracket != nil {
			ExtendRange(listAtom, closeBracket.GetRange())
		}

		if len(exprListResult.list) > 0 {
			for _, expr := range exprListResult.list {
				setParent(expr, listAtom)
			}
			ExtendRange(listAtom, exprListResult.list[len(exprListResult.list)-1].GetRange())
		}

		listAtom.D.Items = exprListResult.list
		return listAtom
	}

	if !p.consumeTokenIfType(TokenTypeCloseBracket) {
		// `exprListResult.parseError ?? _createList()`
		var childNode ExpressionNode = createList()
		if exprListResult.parseError != nil {
			childNode = exprListResult.parseError
		}

		return p.handleExpressionParseError(
			ErrorExpressionCategoryMissingListCloseBracket,
			localization.LocMessage.ExpectedCloseBracket(),
			startBracket,
			childNode,
			nil,
		)
	}

	return createList()
}

// parseTestListWithComprehension corresponds to
// _parseTestListWithComprehension().
func (p *Parser) parseTestListWithComprehension(isGenerator bool) listResult[ExpressionNode] {
	sawComprehension := false

	return parseExpressionListGeneric(
		p,
		func() ExpressionNode {
			expr := p.parseTestOrStarExpression(true /* allowAssignmentExpression */)
			comprehension := p.tryParseComprehension(expr, isGenerator)
			if comprehension != nil {
				expr = comprehension
				sawComprehension = true
			}
			return expr
		},
		p.isNextTokenNeverExpression,
		func() bool { return sawComprehension },
	)
}

// parseDictionaryOrSetAtom corresponds to _parseDictionaryOrSetAtom().
//
//	'{' [dictorsetmaker] '}'
//	dictorsetmaker: (
//	   (dictentry (comp_for | (',' dictentry)* [',']))
//	   | (setentry (comp_for | (',' setentry)* [',']))
//	)
//	dictentry: (test ':' test | '**' expr)
//	setentry: test | star_expr
func (p *Parser) parseDictionaryOrSetAtom() ExpressionNode {
	startBrace := p.getNextToken()
	common.Assert(startBrace.GetType() == TokenTypeOpenCurlyBrace, "")

	dictionaryEntries := []DictionaryEntryNode{}
	setEntries := []ExpressionNode{}
	isDictionary := false
	isSet := false
	sawComprehension := false
	isFirstEntry := true
	var trailingCommaToken Token

	for {
		if p.peekTokenType() == TokenTypeCloseCurlyBrace {
			break
		}

		trailingCommaToken = nil

		var doubleStarExpression ExpressionNode
		var keyExpression ExpressionNode
		var valueExpression ExpressionNode
		doubleStar := p.peekToken(0)

		if p.consumeTokenIfOperator(OperatorTypePower) {
			doubleStarExpression = p.parseExpression(false /* allowUnpack */)
		} else {
			// A dictionary key is never a forward-reference type annotation, even
			// when the dictionary appears within a type annotation (e.g. the field
			// names of an inline TypedDict such as `TypedDict[{'x': int}]`). Suspend
			// type-annotation parsing for the key so its string isn't parsed into a
			// forward-reference expression. The value remains a type annotation and
			// must continue to allow forward references.
			wasParsingTypeAnnotation := p.isParsingTypeAnnotation
			p.isParsingTypeAnnotation = false
			keyExpression = p.parseTestOrStarExpression(true /* allowAssignmentExpression */)
			p.isParsingTypeAnnotation = wasParsingTypeAnnotation

			// Allow walrus operators in this context only for Python 3.10 and newer.
			// Older versions of Python generated a syntax error in this context.
			isWalrusAllowed := p.getLanguageVersion().IsGreaterOrEqualTo(common.PythonVersion3_10)

			if p.consumeTokenIfType(TokenTypeColon) {
				valueExpression = p.parseTestExpression(false /* allowAssignmentExpression */)
				isWalrusAllowed = false
			}

			if assignExpr, ok := keyExpression.(*AssignmentExpressionNode); ok && !isWalrusAllowed && !assignExpr.D.HasParens {
				p.addSyntaxError(localization.LocMessage.WalrusNotAllowed(), assignExpr.D.WalrusToken.GetRange())
			}
		}

		if keyExpression != nil && valueExpression != nil {
			if keyExpression.GetNodeType() == ParseNodeTypeUnpack {
				p.addSyntaxError(localization.LocMessage.UnpackInDict(), keyExpression.GetRange())
			}

			if isSet {
				p.addSyntaxError(localization.LocMessage.KeyValueInSet(), valueExpression.GetRange())
			} else {
				keyEntryNode := NewDictionaryKeyEntryNode(keyExpression, valueExpression)
				var dictEntry DictionaryEntryNode = keyEntryNode
				comprehension := p.tryParseComprehension(keyEntryNode, false /* isGenerator */)
				if comprehension != nil {
					dictEntry = comprehension
					sawComprehension = true

					if !isFirstEntry {
						p.addSyntaxError(localization.LocMessage.ComprehensionInDict(), dictEntry.GetRange())
					}
				}
				dictionaryEntries = append(dictionaryEntries, dictEntry)
				isDictionary = true
			}
		} else if doubleStarExpression != nil {
			if isSet {
				p.addSyntaxError(localization.LocMessage.UnpackInSet(), doubleStarExpression.GetRange())
			} else {
				listEntryNode := NewDictionaryExpandEntryNode(doubleStarExpression)
				ExtendRange(listEntryNode, doubleStar.GetRange())
				var expandEntryNode DictionaryEntryNode = listEntryNode
				comprehension := p.tryParseComprehension(listEntryNode, false /* isGenerator */)
				if comprehension != nil {
					expandEntryNode = comprehension
					sawComprehension = true

					if !isFirstEntry {
						p.addSyntaxError(localization.LocMessage.ComprehensionInDict(), doubleStarExpression.GetRange())
					}
				}
				dictionaryEntries = append(dictionaryEntries, expandEntryNode)
				isDictionary = true
			}
		} else {
			common.Assert(keyExpression != nil, "")
			if keyExpression != nil {
				if isDictionary {
					missingValueErrorNode := NewErrorNode(
						p.peekToken(0).GetRange(),
						ErrorExpressionCategoryMissingDictValue,
						nil, nil,
					)
					keyEntryNode := NewDictionaryKeyEntryNode(keyExpression, missingValueErrorNode)
					dictionaryEntries = append(dictionaryEntries, keyEntryNode)
					p.addSyntaxError(localization.LocMessage.DictKeyValuePairs(), keyExpression.GetRange())
				} else {
					comprehension := p.tryParseComprehension(keyExpression, false /* isGenerator */)
					if comprehension != nil {
						keyExpression = comprehension
						sawComprehension = true

						if !isFirstEntry {
							p.addSyntaxError(localization.LocMessage.ComprehensionInSet(), keyExpression.GetRange())
						}
					}
					setEntries = append(setEntries, keyExpression)
					isSet = true
				}
			}
		}

		// List comprehension statements always end the list.
		if sawComprehension {
			break
		}

		if p.peekTokenType() != TokenTypeComma {
			break
		}

		trailingCommaToken = p.getNextToken()

		isFirstEntry = false
	}

	closeCurlyBrace := p.peekToken(0)
	if !p.consumeTokenIfType(TokenTypeCloseCurlyBrace) {
		p.addSyntaxError(localization.LocMessage.ExpectedCloseBrace(), startBrace.GetRange())
		closeCurlyBrace = nil
	}

	if isSet {
		setAtom := NewSetNode(startBrace.GetRange())
		if closeCurlyBrace != nil {
			ExtendRange(setAtom, closeCurlyBrace.GetRange())
		}

		if len(setEntries) > 0 {
			ExtendRange(setAtom, setEntries[len(setEntries)-1].GetRange())
		}

		for _, entry := range setEntries {
			setParent(entry, setAtom)
		}

		setAtom.D.Items = setEntries
		return setAtom
	}

	dictionaryAtom := NewDictionaryNode(startBrace.GetRange())

	if trailingCommaToken != nil {
		dictionaryAtom.D.TrailingCommaToken = trailingCommaToken
		ExtendRange(dictionaryAtom, trailingCommaToken.GetRange())
	}

	if closeCurlyBrace != nil {
		ExtendRange(dictionaryAtom, closeCurlyBrace.GetRange())
	}

	if len(dictionaryEntries) > 0 {
		for _, entry := range dictionaryEntries {
			setParent(entry, dictionaryAtom)
		}
		ExtendRange(dictionaryAtom, dictionaryEntries[len(dictionaryEntries)-1].GetRange())
	}
	dictionaryAtom.D.Items = dictionaryEntries
	return dictionaryAtom
}
