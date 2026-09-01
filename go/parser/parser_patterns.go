/*
 * parser_patterns.go
 *
 * The `match` statement and PEP 634 patterns, transliterated from
 * parser/parser.ts (pyright 1.1.412).
 *
 * Several of these walk JavaScript Maps and Sets and then report diagnostics in
 * iteration order, which in JavaScript is insertion order. Go's map iteration
 * order is deliberately randomized, so the ordered helpers at the bottom of this
 * file stand in for them; using a plain Go map would make the diagnostics come
 * out in a different (and unstable) order.
 */

package parser

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// parseMatchStatement corresponds to _parseMatchStatement(). It returns nil
// where the TypeScript version returns undefined.
//
//	match_stmt: "match" subject_expr ':' NEWLINE INDENT case_block+ DEDENT
//	subject_expr:
//	    | star_named_expression ',' star_named_expressions?
//	    | named_expression
func (p *Parser) parseMatchStatement() *MatchNode {
	// Parse the subject expression with errors suppressed. If it's not
	// followed by a colon, we'll assume this is not a match statement.
	// We need to do this because "match" is considered a soft keyword,
	// and we need to distinguish between "match(2)" and "match (2):"
	// and between "match[2]" and "match [2]:"
	smellsLikeMatchStatement := false
	p.suppressErrors(func() {
		curTokenIndex := p.tokenIndex

		p.getKeywordToken(KeywordTypeMatch)
		expression := p.parseTestOrStarListAsExpression(
			true, /* allowAssignmentExpression */
			true, /* allowMultipleUnpack */
			ErrorExpressionCategoryMissingPatternSubject,
			func() string { return localization.LocMessage.ExpectedReturnExpr() },
		)
		smellsLikeMatchStatement = expression.GetNodeType() != ParseNodeTypeError &&
			p.peekToken(0).GetType() == TokenTypeColon

		// Set the token index back to the start.
		p.tokenIndex = curTokenIndex
	})

	if !smellsLikeMatchStatement {
		return nil
	}

	matchToken := p.getKeywordToken(KeywordTypeMatch)

	subjectExpression := p.parseTestOrStarListAsExpression(
		true, /* allowAssignmentExpression */
		true, /* allowMultipleUnpack */
		ErrorExpressionCategoryMissingPatternSubject,
		func() string { return localization.LocMessage.ExpectedReturnExpr() },
	)
	matchNode := NewMatchNode(matchToken, subjectExpression)

	nextToken := p.peekToken(0)

	if !p.consumeTokenIfType(TokenTypeColon) {
		p.addSyntaxError(localization.LocMessage.ExpectedColon(), nextToken.GetRange())

		// Try to perform parse recovery by consuming tokens until
		// we find the end of the line.
		if p.consumeTokensUntilType([]TokenType{TokenTypeNewLine, TokenTypeColon}) {
			p.getNextToken()
		}
	} else {
		ExtendRange(matchNode, nextToken.GetRange())

		if !p.consumeTokenIfType(TokenTypeNewLine) {
			p.addSyntaxError(localization.LocMessage.ExpectedNewline(), nextToken.GetRange())
		} else {
			possibleIndent := p.peekToken(0)
			if !p.consumeTokenIfType(TokenTypeIndent) {
				p.addSyntaxError(localization.LocMessage.ExpectedIndentedBlock(), p.peekToken(0).GetRange())
			} else {
				indentToken := possibleIndent.(*IndentToken)
				if indentToken.IsIndentAmbiguous {
					p.addSyntaxError(localization.LocMessage.InconsistentTabs(), indentToken.GetRange())
				}
			}

			for {
				// Handle a common error here and see if we can recover.
				possibleUnexpectedIndent := p.peekToken(0)
				if possibleUnexpectedIndent.GetType() == TokenTypeIndent {
					p.getNextToken()
					indentToken := possibleUnexpectedIndent.(*IndentToken)
					if indentToken.IsIndentAmbiguous {
						p.addSyntaxError(localization.LocMessage.InconsistentTabs(), indentToken.GetRange())
					} else {
						p.addSyntaxError(localization.LocMessage.UnexpectedIndent(), possibleUnexpectedIndent.GetRange())
					}
				}

				caseStatement := p.parseCaseStatement()
				if caseStatement == nil {
					// Perform basic error recovery to get to the next line.
					if p.consumeTokensUntilType([]TokenType{TokenTypeNewLine, TokenTypeColon}) {
						p.getNextToken()
					}
				} else {
					setParent(caseStatement, matchNode)
					matchNode.D.Cases = append(matchNode.D.Cases, caseStatement)
				}

				possibleDedent := p.peekToken(0)
				if p.consumeTokenIfType(TokenTypeDedent) {
					dedentToken := possibleDedent.(*DedentToken)
					if !dedentToken.MatchesIndent {
						p.addSyntaxError(localization.LocMessage.InconsistentIndent(), dedentToken.GetRange())
					}
					if dedentToken.IsDedentAmbiguous {
						p.addSyntaxError(localization.LocMessage.InconsistentTabs(), dedentToken.GetRange())
					}
					break
				}

				if p.peekTokenType() == TokenTypeEndOfStream {
					break
				}
			}
		}

		if len(matchNode.D.Cases) > 0 {
			ExtendRange(matchNode, matchNode.D.Cases[len(matchNode.D.Cases)-1].GetRange())
		} else {
			p.addSyntaxError(localization.LocMessage.ZeroCaseStatementsFound(), matchToken.GetRange())
		}
	}

	// This feature requires Python 3.10.
	if p.getLanguageVersion().IsLessThan(common.PythonVersion3_10) {
		p.addSyntaxError(localization.LocMessage.MatchIncompatible(), matchToken.GetRange())
	}

	// Validate that only the last entry uses an irrefutable pattern.
	for i := 0; i < len(matchNode.D.Cases)-1; i++ {
		caseNode := matchNode.D.Cases[i]
		if caseNode.D.GuardExpr == nil && caseNode.D.IsIrrefutable {
			p.addSyntaxError(localization.LocMessage.CasePatternIsIrrefutable(), caseNode.D.Pattern.GetRange())
		}
	}

	return matchNode
}

// parseCaseStatement corresponds to _parseCaseStatement(). It returns nil where
// the TypeScript version returns undefined.
//
// case_block: "case" patterns [guard] ':' block
// patterns: sequence_pattern | as_pattern
// guard: 'if' named_expression
func (p *Parser) parseCaseStatement() *CaseNode {
	caseToken := p.peekToken(0)

	if !p.consumeTokenIfKeyword(KeywordTypeCase) {
		p.addSyntaxError(localization.LocMessage.ExpectedCase(), caseToken.GetRange())
		return nil
	}

	patternList := p.parsePatternSequence()
	var casePattern PatternAtomNode

	switch {
	case patternList.parseError != nil:
		casePattern = patternList.parseError
	case len(patternList.list) == 0:
		p.addSyntaxError(localization.LocMessage.ExpectedPatternExpr(), p.peekToken(0).GetRange())
		casePattern = NewErrorNode(caseToken.GetRange(), ErrorExpressionCategoryMissingPattern, nil, nil)
	case len(patternList.list) == 1 && !patternList.trailingComma:
		pattern := patternList.list[0].D.OrPatterns[0]

		if capture, ok := pattern.(*PatternCaptureNode); ok && capture.D.IsStar {
			casePattern = NewPatternSequenceNode(patternList.list[0].GetRange(), patternList.list)
		} else {
			casePattern = patternList.list[0]
		}
	default:
		casePattern = NewPatternSequenceNode(patternList.list[0].GetRange(), patternList.list)
	}

	if casePattern.GetNodeType() != ParseNodeTypeError {
		globalNameMap := newOrderedNameMap()
		localNameMap := newOrderedNameMap()
		p.reportDuplicatePatternCaptureTargets(casePattern, globalNameMap, localNameMap)
	}

	var guardExpression ExpressionNode
	if p.consumeTokenIfKeyword(KeywordTypeIf) {
		guardExpression = p.parseTestExpression(true /* allowAssignmentExpression */)
	}

	suite := p.parseSuite(p.isInFunction, false /* skipBody */, nil)
	return NewCaseNode(caseToken.GetRange(), casePattern, p.isPatternIrrefutable(casePattern), guardExpression, suite)
}

// isPatternIrrefutable corresponds to _isPatternIrrefutable().
//
// PEP 634 defines the concept of an "irrefutable" pattern - a pattern that will
// always be matched.
func (p *Parser) isPatternIrrefutable(node PatternAtomNode) bool {
	if node.GetNodeType() == ParseNodeTypePatternCapture {
		return true
	}

	if asNode, ok := node.(*PatternAsNode); ok {
		for _, pattern := range asNode.D.OrPatterns {
			if p.isPatternIrrefutable(pattern) {
				return true
			}
		}
		return false
	}

	return false
}

// reportDuplicatePatternCaptureTargets corresponds to
// _reportDuplicatePatternCaptureTargets().
//
// Reports any situations where a capture target (a variable that receives part
// of a pattern) appears twice within the same pattern. This is complicated by
// the fact that duplicate targets are allowed in separate "or" clauses, so we
// need to track the targets we've seen globally as well as the targets we've
// seen locally within the current "or" clause.
func (p *Parser) reportDuplicatePatternCaptureTargets(node PatternAtomNode, globalNameMap, localNameMap *orderedNameMap) {
	reportTargetIfDuplicate := func(nameNode *NameNode) {
		if globalNameMap.has(nameNode.D.Value) || localNameMap.has(nameNode.D.Value) {
			p.addSyntaxError(
				localization.LocMessage.DuplicateCapturePatternTarget().Format(nameNode.D.Value),
				nameNode.GetRange(),
			)
		} else {
			localNameMap.set(nameNode.D.Value, nameNode)
		}
	}

	switch n := node.(type) {
	case *PatternSequenceNode:
		for _, subpattern := range n.D.Entries {
			p.reportDuplicatePatternCaptureTargets(subpattern, globalNameMap, localNameMap)
		}

	case *PatternClassNode:
		for _, arg := range n.D.Args {
			p.reportDuplicatePatternCaptureTargets(arg.D.Pattern, globalNameMap, localNameMap)
		}

	case *PatternAsNode:
		if n.D.Target != nil {
			reportTargetIfDuplicate(n.D.Target)
		}

		orLocalNameMaps := make([]*orderedNameMap, 0, len(n.D.OrPatterns))
		for _, subpattern := range n.D.OrPatterns {
			orLocalNameMap := newOrderedNameMap()
			p.reportDuplicatePatternCaptureTargets(subpattern, localNameMap, orLocalNameMap)
			orLocalNameMaps = append(orLocalNameMaps, orLocalNameMap)
		}

		combinedLocalOrNameMap := newOrderedNameMap()
		for _, orLocalNameMap := range orLocalNameMaps {
			for _, node := range orLocalNameMap.values() {
				if !combinedLocalOrNameMap.has(node.D.Value) {
					combinedLocalOrNameMap.set(node.D.Value, node)
					reportTargetIfDuplicate(node)
				}
			}
		}

	case *PatternCaptureNode:
		if !n.D.IsWildcard {
			reportTargetIfDuplicate(n.D.Target)
		}

	case *PatternMappingNode:
		for _, mapEntry := range n.D.Entries {
			if expand, ok := mapEntry.(*PatternMappingExpandEntryNode); ok {
				reportTargetIfDuplicate(expand.D.Target)
			} else {
				keyEntry := mapEntry.(*PatternMappingKeyEntryNode)
				p.reportDuplicatePatternCaptureTargets(keyEntry.D.KeyPattern.(PatternAtomNode), globalNameMap, localNameMap)
				p.reportDuplicatePatternCaptureTargets(keyEntry.D.ValuePattern.(PatternAtomNode), globalNameMap, localNameMap)
			}
		}

		// PatternLiteral, PatternValue and Error fall through with no action.
	}
}

// getPatternTargetNames corresponds to _getPatternTargetNames().
func (p *Parser) getPatternTargetNames(node PatternAtomNode, nameSet *orderedStringSet) {
	switch n := node.(type) {
	case *PatternSequenceNode:
		for _, subpattern := range n.D.Entries {
			p.getPatternTargetNames(subpattern, nameSet)
		}

	case *PatternClassNode:
		for _, arg := range n.D.Args {
			p.getPatternTargetNames(arg.D.Pattern, nameSet)
		}

	case *PatternAsNode:
		if n.D.Target != nil {
			nameSet.add(n.D.Target.D.Value)
		}
		for _, subpattern := range n.D.OrPatterns {
			p.getPatternTargetNames(subpattern, nameSet)
		}

	case *PatternCaptureNode:
		if !n.D.IsWildcard {
			nameSet.add(n.D.Target.D.Value)
		}

	case *PatternMappingNode:
		for _, mapEntry := range n.D.Entries {
			if expand, ok := mapEntry.(*PatternMappingExpandEntryNode); ok {
				nameSet.add(expand.D.Target.D.Value)
			} else {
				keyEntry := mapEntry.(*PatternMappingKeyEntryNode)
				p.getPatternTargetNames(keyEntry.D.KeyPattern.(PatternAtomNode), nameSet)
				p.getPatternTargetNames(keyEntry.D.ValuePattern.(PatternAtomNode), nameSet)
			}
		}

		// PatternLiteral, PatternValue and Error fall through with no action.
	}
}

// parsePatternSequence corresponds to _parsePatternSequence().
func (p *Parser) parsePatternSequence() listResult[*PatternAsNode] {
	patternList := parseExpressionListGeneric(p, func() *PatternAsNode { return p.parsePatternAs() }, nil, nil)

	// Check for more than one star entry.
	starEntries := []*PatternAsNode{}
	for _, entry := range patternList.list {
		if len(entry.D.OrPatterns) == 1 {
			if capture, ok := entry.D.OrPatterns[0].(*PatternCaptureNode); ok && capture.D.IsStar {
				starEntries = append(starEntries, entry)
			}
		}
	}

	if len(starEntries) > 1 {
		p.addSyntaxError(localization.LocMessage.DuplicateStarPattern(), starEntries[1].D.OrPatterns[0].GetRange())
	}

	return patternList
}

// parsePatternAs corresponds to _parsePatternAs().
//
// as_pattern: or_pattern ['as' NAME]
// or_pattern: '|'.pattern_atom+
func (p *Parser) parsePatternAs() *PatternAsNode {
	orPatterns := []PatternAtomNode{}

	for {
		patternAtom := p.parsePatternAtom()
		orPatterns = append(orPatterns, patternAtom)

		if !p.consumeTokenIfOperator(OperatorTypeBitwiseOr) {
			break
		}
	}

	if len(orPatterns) > 1 {
		// Star patterns cannot be ORed with other patterns.
		for _, patternAtom := range orPatterns {
			if capture, ok := patternAtom.(*PatternCaptureNode); ok && capture.D.IsStar {
				p.addSyntaxError(localization.LocMessage.StarPatternInOrPattern(), patternAtom.GetRange())
			}
		}
	}

	var target *NameNode
	if p.consumeTokenIfKeyword(KeywordTypeAs) {
		nameToken := p.getTokenIfIdentifier()
		if nameToken != nil {
			target = NewNameNode(nameToken)
		} else {
			p.addSyntaxError(localization.LocMessage.ExpectedNameAfterAs(), p.peekToken(0).GetRange())
		}
	}

	// Star patterns cannot be used with AS pattern.
	if target != nil && len(orPatterns) == 1 {
		if capture, ok := orPatterns[0].(*PatternCaptureNode); ok && capture.D.IsStar {
			p.addSyntaxError(localization.LocMessage.StarPatternInAsPattern(), orPatterns[0].GetRange())
		}
	}

	// Validate that irrefutable patterns are not in any entries other than the last.
	for index, orPattern := range orPatterns {
		if index < len(orPatterns)-1 && p.isPatternIrrefutable(orPattern) {
			p.addSyntaxError(localization.LocMessage.OrPatternIrrefutable(), orPattern.GetRange())
		}
	}

	// Validate that all bound variables are the same within all or patterns.
	fullNameSet := newOrderedStringSet()
	for _, orPattern := range orPatterns {
		p.getPatternTargetNames(orPattern, fullNameSet)
	}

	for _, orPattern := range orPatterns {
		localNameSet := newOrderedStringSet()
		p.getPatternTargetNames(orPattern, localNameSet)

		if localNameSet.size() < fullNameSet.size() {
			missingNames := []string{}
			for _, name := range fullNameSet.keys() {
				if !localNameSet.has(name) {
					missingNames = append(missingNames, `"`+name+`"`)
				}
			}
			diag := common.NewDiagnosticAddendum()
			diag.AddMessage(localization.LocAddendum.OrPatternMissingName().Format(strings.Join(missingNames, ", ")))
			p.addSyntaxError(
				localization.LocMessage.OrPatternMissingName()+diag.GetString(),
				orPattern.GetRange(),
			)
		}
	}

	return NewPatternAsNode(orPatterns, target)
}

// parsePatternAtom corresponds to _parsePatternAtom().
//
//	pattern_atom:
//	    | literal_pattern
//	    | name_or_attr
//	    | '(' as_pattern ')'
//	    | '[' [sequence_pattern] ']'
//	    | '(' [sequence_pattern] ')'
//	    | '{' [items_pattern] '}'
//	    | name_or_attr '(' [pattern_arguments ','?] ')'
//	name_or_attr: attr | NAME
//	attr: name_or_attr '.' NAME
//	sequence_pattern: ','.maybe_star_pattern+ ','?
//	maybe_star_pattern: '*' NAME | pattern
//	items_pattern: ','.key_value_pattern+ ','?
func (p *Parser) parsePatternAtom() PatternAtomNode {
	patternLiteral := p.parsePatternLiteral()
	if patternLiteral != nil {
		return patternLiteral
	}

	patternCaptureOrValue := p.parsePatternCaptureOrValue()
	if patternCaptureOrValue != nil {
		openParenToken := p.peekToken(0)
		if patternCaptureOrValue.GetNodeType() == ParseNodeTypeError ||
			!p.consumeTokenIfType(TokenTypeOpenParenthesis) {
			return patternCaptureOrValue
		}

		args := p.parseClassPatternArgList()

		var classNameExpr ClassNameNode
		if capture, ok := patternCaptureOrValue.(*PatternCaptureNode); ok {
			classNameExpr = capture.D.Target
		} else {
			classNameExpr = patternCaptureOrValue.(*PatternValueNode).D.Expr
		}
		classPattern := NewPatternClassNode(classNameExpr, args)

		if !p.consumeTokenIfType(TokenTypeCloseParenthesis) {
			p.addSyntaxError(localization.LocMessage.ExpectedCloseParen(), openParenToken.GetRange())

			// Consume the remainder of tokens on the line for error
			// recovery.
			p.consumeTokensUntilType([]TokenType{TokenTypeNewLine})

			// Extend the node's range to include the rest of the line.
			// This helps the signatureHelpProvider.
			ExtendRange(classPattern, p.peekToken(0).GetRange())
		}

		return classPattern
	}

	nextToken := p.peekToken(0)
	nextOperator, haveOperator := p.peekOperatorType()

	if haveOperator && nextOperator == OperatorTypeMultiply {
		starToken := p.getNextToken()
		identifierToken := p.getTokenIfIdentifier()
		if identifierToken == nil {
			p.addSyntaxError(localization.LocMessage.ExpectedIdentifier(), p.peekToken(0).GetRange())
			return NewErrorNode(starToken.GetRange(), ErrorExpressionCategoryMissingExpression, nil, nil)
		}
		starRange := starToken.GetRange()
		return NewPatternCaptureNode(NewNameNode(identifierToken), &starRange)
	}

	if nextToken.GetType() == TokenTypeOpenParenthesis || nextToken.GetType() == TokenTypeOpenBracket {
		startToken := p.getNextToken()
		patternList := p.parsePatternSequence()
		var casePattern PatternAtomNode

		switch {
		case patternList.parseError != nil:
			casePattern = patternList.parseError
		case len(patternList.list) == 1 &&
			!patternList.trailingComma &&
			startToken.GetType() == TokenTypeOpenParenthesis:
			pattern := patternList.list[0].D.OrPatterns[0]

			if capture, ok := pattern.(*PatternCaptureNode); ok && capture.D.IsStar {
				casePattern = NewPatternSequenceNode(startToken.GetRange(), patternList.list)
			} else {
				casePattern = patternList.list[0]
			}

			ExtendRange(casePattern, nextToken.GetRange())
		default:
			casePattern = NewPatternSequenceNode(startToken.GetRange(), patternList.list)
		}

		closeType := TokenTypeCloseBracket
		if nextToken.GetType() == TokenTypeOpenParenthesis {
			closeType = TokenTypeCloseParenthesis
		}

		endToken := p.peekToken(0)
		if p.consumeTokenIfType(closeType) {
			ExtendRange(casePattern, endToken.GetRange())
		} else {
			message := localization.LocMessage.ExpectedCloseBracket()
			if nextToken.GetType() == TokenTypeOpenParenthesis {
				message = localization.LocMessage.ExpectedCloseParen()
			}
			p.addSyntaxError(message, nextToken.GetRange())
			p.consumeTokensUntilType([]TokenType{TokenTypeColon, closeType})
		}

		return casePattern
	} else if nextToken.GetType() == TokenTypeOpenCurlyBrace {
		firstToken := p.getNextToken()
		mappingPattern := p.parsePatternMapping(firstToken)
		lastToken := p.peekToken(0)

		if p.consumeTokenIfType(TokenTypeCloseCurlyBrace) {
			ExtendRange(mappingPattern, lastToken.GetRange())
		} else {
			p.addSyntaxError(localization.LocMessage.ExpectedCloseBrace(), nextToken.GetRange())
			p.consumeTokensUntilType([]TokenType{TokenTypeColon, TokenTypeCloseCurlyBrace})
		}

		return mappingPattern
	}

	return p.handleExpressionParseError(
		ErrorExpressionCategoryMissingPattern,
		localization.LocMessage.ExpectedPatternExpr(),
		nil, nil, nil,
	)
}

// parseClassPatternArgList corresponds to _parseClassPatternArgList().
//
//	pattern_arguments:
//	    | positional_patterns [',' keyword_patterns]
//	    | keyword_patterns
//	positional_patterns: ','.as_pattern+
//	keyword_patterns: ','.keyword_pattern+
func (p *Parser) parseClassPatternArgList() []*PatternClassArgumentNode {
	argList := []*PatternClassArgumentNode{}
	sawKeywordArg := false

	for {
		nextTokenType := p.peekTokenType()
		if nextTokenType == TokenTypeCloseParenthesis ||
			nextTokenType == TokenTypeNewLine ||
			nextTokenType == TokenTypeEndOfStream {
			break
		}

		arg := p.parseClassPatternArgument()
		if arg.D.Name != nil {
			sawKeywordArg = true
		} else if sawKeywordArg && arg.D.Name == nil {
			p.addSyntaxError(localization.LocMessage.PositionArgAfterNamedArg(), arg.GetRange())
		}
		argList = append(argList, arg)

		if !p.consumeTokenIfType(TokenTypeComma) {
			break
		}
	}

	return argList
}

// parseClassPatternArgument corresponds to _parseClassPatternArgument().
//
// keyword_pattern: NAME '=' as_pattern
func (p *Parser) parseClassPatternArgument() *PatternClassArgumentNode {
	firstToken := p.peekToken(0)
	secondToken := p.peekToken(1)

	var keywordName *NameNode

	if (firstToken.GetType() == TokenTypeIdentifier || firstToken.GetType() == TokenTypeKeyword) &&
		secondToken.GetType() == TokenTypeOperator &&
		secondToken.(*OperatorToken).OperatorType == OperatorTypeAssign {
		classNameToken := p.getTokenIfIdentifier()
		if classNameToken != nil {
			keywordName = NewNameNode(classNameToken)
			p.getNextToken()
		}
	}

	pattern := p.parsePatternAs()

	return NewPatternClassArgumentNode(pattern, keywordName)
}

// parsePatternLiteral corresponds to _parsePatternLiteral(). It returns nil
// where the TypeScript version returns undefined.
//
//	literal_pattern:
//	    | signed_number
//	    | signed_number '+' NUMBER
//	    | signed_number '-' NUMBER
//	    | strings
//	    | 'None'
//	    | 'True'
//	    | 'False'
func (p *Parser) parsePatternLiteral() *PatternLiteralNode {
	nextToken := p.peekToken(0)
	nextOperator, haveOperator := p.peekOperatorType()

	if nextToken.GetType() == TokenTypeNumber || (haveOperator && nextOperator == OperatorTypeSubtract) {
		return p.parsePatternLiteralNumber()
	}

	if nextToken.GetType() == TokenTypeString {
		stringList := p.parseAtom().(*StringListNode)
		common.Assert(stringList.GetNodeType() == ParseNodeTypeStringList, "")

		// Check for f-strings, which are not allowed.
		for _, stringAtom := range stringList.D.Strings {
			if stringAtom.GetNodeType() == ParseNodeTypeFormatString {
				p.addSyntaxError(localization.LocMessage.FormatStringInPattern(), stringAtom.GetRange())
			}
		}

		return NewPatternLiteralNode(stringList)
	}

	if nextToken.GetType() == TokenTypeKeyword {
		keywordToken := nextToken.(*KeywordToken)
		if keywordToken.KeywordType == KeywordTypeFalse ||
			keywordToken.KeywordType == KeywordTypeTrue ||
			keywordToken.KeywordType == KeywordTypeNone {
			return NewPatternLiteralNode(p.parseAtom())
		}
	}

	return nil
}

// parsePatternLiteralNumber corresponds to _parsePatternLiteralNumber().
//
// signed_number: NUMBER | '-' NUMBER
func (p *Parser) parsePatternLiteralNumber() *PatternLiteralNode {
	expression := p.parseArithmeticExpression()
	var realValue ExpressionNode
	var imagValue ExpressionNode

	if binary, ok := expression.(*BinaryOperationNode); ok {
		if binary.D.Operator == OperatorTypeSubtract || binary.D.Operator == OperatorTypeAdd {
			realValue = binary.D.LeftExpr
			imagValue = binary.D.RightExpr
		}
	} else {
		realValue = expression
	}

	if realValue != nil {
		if unary, ok := realValue.(*UnaryOperationNode); ok && unary.D.Operator == OperatorTypeSubtract {
			realValue = unary.D.Expr
		}

		numberNode, isNumber := realValue.(*NumberNode)
		if !isNumber || (imagValue != nil && numberNode.D.IsImaginary) {
			p.addSyntaxError(localization.LocMessage.ExpectedComplexNumberLiteral(), expression.GetRange())
			imagValue = nil
		}
	}

	if imagValue != nil {
		if unary, ok := imagValue.(*UnaryOperationNode); ok && unary.D.Operator == OperatorTypeSubtract {
			imagValue = unary.D.Expr
		}

		numberNode, isNumber := imagValue.(*NumberNode)
		if !isNumber || !numberNode.D.IsImaginary {
			p.addSyntaxError(localization.LocMessage.ExpectedComplexNumberLiteral(), expression.GetRange())
		}
	}

	return NewPatternLiteralNode(expression)
}

// parsePatternMapping corresponds to _parsePatternMapping().
func (p *Parser) parsePatternMapping(firstToken Token) PatternAtomNode {
	itemList := parseExpressionListGeneric(p, func() PatternMappingEntryNode {
		return p.parsePatternMappingItem()
	}, nil, nil)

	if len(itemList.list) > 0 {
		// Verify there's at most one ** entry.
		starStarEntries := []PatternMappingEntryNode{}
		for _, entry := range itemList.list {
			if entry.GetNodeType() == ParseNodeTypePatternMappingExpandEntry {
				starStarEntries = append(starStarEntries, entry)
			}
		}
		if len(starStarEntries) > 1 {
			p.addSyntaxError(localization.LocMessage.DuplicateStarStarPattern(), starStarEntries[1].GetRange())
		}

		return NewPatternMappingNode(firstToken.GetRange(), itemList.list)
	}

	// `itemList.parseError || ErrorNode.create(...)`
	if itemList.parseError != nil {
		return itemList.parseError
	}
	return NewErrorNode(p.peekToken(0).GetRange(), ErrorExpressionCategoryMissingPattern, nil, nil)
}

// parsePatternMappingItem corresponds to _parsePatternMappingItem().
//
//	key_value_pattern:
//	    | (literal_pattern | attr) ':' as_pattern
//	    | '**' NAME
func (p *Parser) parsePatternMappingItem() PatternMappingEntryNode {
	var keyExpression PatternKeyNode
	doubleStar := p.peekToken(0)

	if p.consumeTokenIfOperator(OperatorTypePower) {
		identifierToken := p.getTokenIfIdentifier()
		if identifierToken == nil {
			p.addSyntaxError(localization.LocMessage.ExpectedIdentifier(), p.peekToken(0).GetRange())
			return NewErrorNode(p.peekToken(0).GetRange(), ErrorExpressionCategoryMissingPattern, nil, nil)
		}

		nameNode := NewNameNode(identifierToken)
		if identifierToken.Value == "_" {
			p.addSyntaxError(localization.LocMessage.StarStarWildcardNotAllowed(), nameNode.GetRange())
		}

		return NewPatternMappingExpandEntryNode(doubleStar.GetRange(), nameNode)
	}

	patternLiteral := p.parsePatternLiteral()
	if patternLiteral != nil {
		keyExpression = patternLiteral
	} else {
		patternCaptureOrValue := p.parsePatternCaptureOrValue()
		if patternCaptureOrValue != nil {
			if value, ok := patternCaptureOrValue.(*PatternValueNode); ok {
				keyExpression = value
			} else {
				p.addSyntaxError(localization.LocMessage.ExpectedPatternValue(), patternCaptureOrValue.GetRange())
				keyExpression = NewErrorNode(p.peekToken(0).GetRange(), ErrorExpressionCategoryMissingPattern, nil, nil)
			}
		}
	}

	if keyExpression == nil {
		p.addSyntaxError(localization.LocMessage.ExpectedPatternExpr(), p.peekToken(0).GetRange())
		keyExpression = NewErrorNode(p.peekToken(0).GetRange(), ErrorExpressionCategoryMissingPattern, nil, nil)
	}

	var valuePattern PatternValueTargetNode
	if !p.consumeTokenIfType(TokenTypeColon) {
		p.addSyntaxError(localization.LocMessage.ExpectedColon(), p.peekToken(0).GetRange())
		valuePattern = NewErrorNode(p.peekToken(0).GetRange(), ErrorExpressionCategoryMissingPattern, nil, nil)
	} else {
		valuePattern = p.parsePatternAs()
	}

	return NewPatternMappingKeyEntryNode(keyExpression, valuePattern)
}

// parsePatternCaptureOrValue corresponds to _parsePatternCaptureOrValue(). It
// returns nil where the TypeScript version returns undefined.
func (p *Parser) parsePatternCaptureOrValue() PatternAtomNode {
	nextToken := p.peekToken(0)

	if nextToken.GetType() == TokenTypeIdentifier || nextToken.GetType() == TokenTypeKeyword {
		var nameOrMember ExpressionNode

		for {
			identifierToken := p.getTokenIfIdentifier()
			if identifierToken != nil {
				nameNode := NewNameNode(identifierToken)
				if nameOrMember != nil {
					nameOrMember = NewMemberAccessNode(nameOrMember, nameNode)
				} else {
					nameOrMember = nameNode
				}
			} else {
				p.addSyntaxError(localization.LocMessage.ExpectedIdentifier(), p.peekToken(0).GetRange())
				break
			}

			if !p.consumeTokenIfType(TokenTypeDot) {
				break
			}
		}

		if nameOrMember == nil {
			p.addSyntaxError(localization.LocMessage.ExpectedIdentifier(), p.peekToken(0).GetRange())
			return NewErrorNode(p.peekToken(0).GetRange(), ErrorExpressionCategoryMissingPattern, nil, nil)
		}

		if memberAccess, ok := nameOrMember.(*MemberAccessNode); ok {
			return NewPatternValueNode(memberAccess)
		}

		return NewPatternCaptureNode(nameOrMember.(*NameNode), nil)
	}

	return nil
}

// -----------------------------------------------------------------------------
// Insertion-ordered stand-ins for the JavaScript Map and Set used above.
//
// The diagnostics these drive are emitted in iteration order, and JavaScript
// iterates a Map or Set in insertion order. A bare Go map would emit them in a
// randomized order that changes between runs.
// -----------------------------------------------------------------------------

type orderedNameMap struct {
	index map[string]*NameNode
	order []string
}

func newOrderedNameMap() *orderedNameMap {
	return &orderedNameMap{index: map[string]*NameNode{}}
}

func (m *orderedNameMap) has(key string) bool {
	_, ok := m.index[key]
	return ok
}

func (m *orderedNameMap) set(key string, value *NameNode) {
	if _, exists := m.index[key]; !exists {
		m.order = append(m.order, key)
	}
	m.index[key] = value
}

func (m *orderedNameMap) values() []*NameNode {
	result := make([]*NameNode, 0, len(m.order))
	for _, key := range m.order {
		result = append(result, m.index[key])
	}
	return result
}

type orderedStringSet struct {
	index map[string]bool
	order []string
}

func newOrderedStringSet() *orderedStringSet {
	return &orderedStringSet{index: map[string]bool{}}
}

func (s *orderedStringSet) add(key string) {
	if !s.index[key] {
		s.index[key] = true
		s.order = append(s.order, key)
	}
}

func (s *orderedStringSet) has(key string) bool { return s.index[key] }
func (s *orderedStringSet) size() int           { return len(s.order) }
func (s *orderedStringSet) keys() []string      { return s.order }
