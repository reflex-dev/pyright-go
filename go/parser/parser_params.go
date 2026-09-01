/*
 * parser_params.go
 *
 * Parameter lists, lambdas, yield expressions and type annotations,
 * transliterated from parser/parser.ts (pyright 1.1.412).
 *
 * Parameter parsing is shared between `def` and `lambda`; the lambda path is
 * what pulls it into the expression batch.
 */

package parser

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// parseVarArgsList corresponds to _parseVarArgsList().
//
//	varargslist: (vfpdef ['=' test] (',' vfpdef ['=' test])* [',' [
//	     '*' [vfpdef] (',' vfpdef ['=' test])* [',' ['**' vfpdef [',']]]
//	   | '**' vfpdef [',']]]
//	 | '*' [vfpdef] (',' vfpdef ['=' test])* [',' ['**' vfpdef [',']]]
//	 | '**' vfpdef [','])
//	tfpdef: NAME [':' test]
//	vfpdef: NAME;
func (p *Parser) parseVarArgsList(terminator TokenType, allowAnnotations bool) []*ParameterNode {
	paramMap := map[string]string{}
	paramList := []*ParameterNode{}
	sawDefaultParam := false
	reportedNonDefaultParamErr := false
	sawKeywordOnlySeparator := false
	sawPositionOnlySeparator := false
	sawKeywordOnlyParamAfterSeparator := false
	sawArgs := false
	sawKwArgs := false

	for {
		if p.peekTokenType() == terminator {
			break
		}

		param := p.parseParameter(allowAnnotations)
		if param == nil {
			p.consumeTokensUntilType([]TokenType{terminator})
			break
		}

		if param.D.Name != nil {
			name := param.D.Name.D.Value
			if _, exists := paramMap[name]; exists {
				p.addSyntaxError(localization.LocMessage.DuplicateParam().Format(name), param.D.Name.GetRange())
			} else {
				paramMap[name] = name
			}
		} else if param.D.Category == ParamCategorySimple {
			if len(paramList) == 0 {
				p.addSyntaxError(localization.LocMessage.PositionOnlyFirstParam(), param.GetRange())
			}
		}

		if param.D.Category == ParamCategorySimple {
			if param.D.Name == nil {
				if sawPositionOnlySeparator {
					p.addSyntaxError(localization.LocMessage.DuplicatePositionOnly(), param.GetRange())
				} else if sawKeywordOnlySeparator {
					p.addSyntaxError(localization.LocMessage.PositionOnlyAfterKeywordOnly(), param.GetRange())
				} else if sawArgs {
					p.addSyntaxError(localization.LocMessage.PositionOnlyAfterArgs(), param.GetRange())
				}
				sawPositionOnlySeparator = true
			} else {
				if sawKeywordOnlySeparator {
					sawKeywordOnlyParamAfterSeparator = true
				}

				if param.D.DefaultValue != nil {
					sawDefaultParam = true
				} else if sawDefaultParam && !sawKeywordOnlySeparator && !sawArgs {
					// Report this error only once.
					if !reportedNonDefaultParamErr {
						p.addSyntaxError(localization.LocMessage.NonDefaultAfterDefault(), param.GetRange())
						reportedNonDefaultParamErr = true
					}
				}
			}
		}

		paramList = append(paramList, param)

		if param.D.Category == ParamCategoryArgsList {
			if param.D.Name == nil {
				if sawKeywordOnlySeparator {
					p.addSyntaxError(localization.LocMessage.DuplicateKeywordOnly(), param.GetRange())
				} else if sawArgs {
					p.addSyntaxError(localization.LocMessage.KeywordOnlyAfterArgs(), param.GetRange())
				}
				sawKeywordOnlySeparator = true
			} else {
				if sawKeywordOnlySeparator || sawArgs {
					p.addSyntaxError(localization.LocMessage.DuplicateArgsParam(), param.GetRange())
				}
				sawArgs = true
			}
		}

		if param.D.Category == ParamCategoryKwargsDict {
			if sawKwArgs {
				p.addSyntaxError(localization.LocMessage.DuplicateKwargsParam(), param.GetRange())
			}
			sawKwArgs = true

			// A **kwargs cannot immediately follow a keyword-only separator ("*").
			if sawKeywordOnlySeparator && !sawKeywordOnlyParamAfterSeparator {
				p.addSyntaxError(localization.LocMessage.KeywordParameterMissing(), param.GetRange())
			}
		} else if sawKwArgs {
			p.addSyntaxError(localization.LocMessage.ParamAfterKwargsParam(), param.GetRange())
		}

		foundComma := p.consumeTokenIfType(TokenTypeComma)

		if allowAnnotations && param.D.Annotation == nil {
			// Look for a type annotation comment at the end of the line.
			typeAnnotationComment := p.parseVariableTypeAnnotationComment()
			if typeAnnotationComment != nil {
				param.D.AnnotationComment = typeAnnotationComment
				setParent(param.D.AnnotationComment, param)
				ExtendRange(param, param.D.AnnotationComment.GetRange())
			}
		}

		if !foundComma {
			break
		}
	}

	if len(paramList) > 0 {
		lastParam := paramList[len(paramList)-1]
		if lastParam.D.Category == ParamCategoryArgsList && lastParam.D.Name == nil {
			p.addSyntaxError(localization.LocMessage.ExpectedNamedParameter(), lastParam.GetRange())
		}
	}

	return paramList
}

// parseParameter corresponds to _parseParameter().
//
// The TypeScript signature declares a non-optional ParameterNode return, but
// _parseVarArgsList still guards with `if (!param)`, so this can in principle
// return nil. It never does.
func (p *Parser) parseParameter(allowAnnotations bool) *ParameterNode {
	starCount := 0
	slashCount := 0
	firstToken := p.peekToken(0)

	if p.consumeTokenIfOperator(OperatorTypeMultiply) {
		starCount = 1
	} else if p.consumeTokenIfOperator(OperatorTypePower) {
		starCount = 2
	} else if p.consumeTokenIfOperator(OperatorTypeDivide) {
		if p.getLanguageVersion().IsLessThan(common.PythonVersion3_8) && !p.parseOptions.IsStubFile {
			p.addSyntaxError(localization.LocMessage.PositionOnlyIncompatible(), firstToken.GetRange())
		}
		slashCount = 1
	}

	paramName := p.getTokenIfIdentifier()
	if paramName == nil {
		if starCount == 1 {
			return NewParameterNode(firstToken, ParamCategoryArgsList)
		} else if slashCount == 1 {
			return NewParameterNode(firstToken, ParamCategorySimple)
		}

		// Check for the Python 2.x parameter sublist syntax and handle it gracefully.
		if p.peekTokenType() == TokenTypeOpenParenthesis {
			sublistStart := p.getNextToken()
			if p.consumeTokensUntilType([]TokenType{TokenTypeCloseParenthesis}) {
				p.getNextToken()
			}
			p.addSyntaxError(localization.LocMessage.SublistParamsIncompatible(), sublistStart.GetRange())
		} else {
			p.addSyntaxError(localization.LocMessage.ExpectedParamName(), p.peekToken(0).GetRange())
		}
	}

	paramType := ParamCategorySimple
	if starCount == 1 {
		paramType = ParamCategoryArgsList
	} else if starCount == 2 {
		paramType = ParamCategoryKwargsDict
	}
	paramNode := NewParameterNode(firstToken, paramType)
	if paramName != nil {
		paramNode.D.Name = NewNameNode(paramName)
		setParent(paramNode.D.Name, paramNode)
		ExtendRange(paramNode, paramName.GetRange())
	}

	if allowAnnotations && p.consumeTokenIfType(TokenTypeColon) {
		paramNode.D.Annotation = p.parseTypeAnnotation(paramType == ParamCategoryArgsList)
		setParent(paramNode.D.Annotation, paramNode)
		ExtendRange(paramNode, paramNode.D.Annotation.GetRange())
	}

	if p.consumeTokenIfOperator(OperatorTypeAssign) {
		paramNode.D.DefaultValue = p.parseTestExpression(false /* allowAssignmentExpression */)
		setParent(paramNode.D.DefaultValue, paramNode)
		ExtendRange(paramNode, paramNode.D.DefaultValue.GetRange())

		if starCount > 0 {
			p.addSyntaxError(localization.LocMessage.DefaultValueNotAllowed(), paramNode.D.DefaultValue.GetRange())
		}
	}

	return paramNode
}

// parseLambdaExpression corresponds to _parseLambdaExpression().
//
// lambdef: 'lambda' [varargslist] ':' test
func (p *Parser) parseLambdaExpression(allowConditional bool) *LambdaNode {
	lambdaToken := p.getKeywordToken(KeywordTypeLambda)

	argList := p.parseVarArgsList(TokenTypeColon, false /* allowAnnotations */)

	if !p.consumeTokenIfType(TokenTypeColon) {
		p.addSyntaxError(localization.LocMessage.ExpectedColon(), p.peekToken(0).GetRange())
	}

	var testExpr ExpressionNode
	if allowConditional {
		testExpr = p.parseTestExpression(false /* allowAssignmentExpression */)
	} else {
		// `this._tryParseLambdaExpression(false) || this._parseOrTest()`
		if nested := p.tryParseLambdaExpression(false /* allowConditional */); nested != nil {
			testExpr = nested
		} else {
			testExpr = p.parseOrTest()
		}
	}

	lambdaNode := NewLambdaNode(lambdaToken, testExpr)
	lambdaNode.D.Params = argList
	for _, arg := range argList {
		setParent(arg, lambdaNode)
	}
	return lambdaNode
}

// tryParseLambdaExpression corresponds to _tryParseLambdaExpression(). It
// returns nil where the TypeScript version returns undefined.
func (p *Parser) tryParseLambdaExpression(allowConditional bool) *LambdaNode {
	if kw, ok := p.peekKeywordType(); !ok || kw != KeywordTypeLambda {
		return nil
	}

	return p.parseLambdaExpression(allowConditional)
}

// parseYieldExpression corresponds to _parseYieldExpression().
//
// yield_expr: 'yield' [yield_arg]
// yield_arg: 'from' test | testlist
func (p *Parser) parseYieldExpression() ExpressionNode {
	yieldToken := p.getKeywordToken(KeywordTypeYield)

	nextToken := p.peekToken(0)
	if p.consumeTokenIfKeyword(KeywordTypeFrom) {
		if p.getLanguageVersion().IsLessThan(common.PythonVersion3_3) {
			p.addSyntaxError(localization.LocMessage.YieldFromIllegal(), nextToken.GetRange())
		}
		return NewYieldFromNode(yieldToken, p.parseTestExpression(false /* allowAssignmentExpression */))
	}

	var exprList ExpressionNode
	if !p.isNextTokenNeverExpression() {
		exprList = p.parseTestOrStarListAsExpression(
			false, /* allowAssignmentExpression */
			true,  /* allowMultipleUnpack */
			ErrorExpressionCategoryMissingExpression,
			func() string { return localization.LocMessage.ExpectedYieldExpr() },
		)
		p.reportConditionalErrorForStarTupleElement(exprList, common.PythonVersion3_8)
	}

	return NewYieldNode(yieldToken, exprList)
}

// tryParseYieldExpression corresponds to _tryParseYieldExpression(). It returns
// nil where the TypeScript version returns undefined.
func (p *Parser) tryParseYieldExpression() ExpressionNode {
	if kw, ok := p.peekKeywordType(); !ok || kw != KeywordTypeYield {
		return nil
	}

	return p.parseYieldExpression()
}

// parseTypeAnnotation corresponds to _parseTypeAnnotation().
func (p *Parser) parseTypeAnnotation(allowUnpack bool) ExpressionNode {
	// Temporarily set a flag that indicates we're parsing a type annotation.
	wasParsingTypeAnnotation := p.isParsingTypeAnnotation
	p.isParsingTypeAnnotation = true

	// Allow unpack operators.
	startToken := p.peekToken(0)
	isUnpack := p.consumeTokenIfOperator(OperatorTypeMultiply)

	if isUnpack &&
		allowUnpack &&
		!p.parseOptions.IsStubFile &&
		!p.isParsingQuotedText &&
		p.getLanguageVersion().IsLessThan(common.PythonVersion3_11) {
		p.addSyntaxError(localization.LocMessage.UnpackedSubscriptIllegal(), startToken.GetRange())
	}

	result := p.parseTestExpression(false /* allowAssignmentExpression */)
	if isUnpack {
		result = NewUnpackNode(startToken, result)
	}

	p.isParsingTypeAnnotation = wasParsingTypeAnnotation
	p.hasTypeAnnotations = true

	return result
}

// parseFunctionTypeAnnotation corresponds to _parseFunctionTypeAnnotation(). It
// returns nil where the TypeScript version returns undefined.
func (p *Parser) parseFunctionTypeAnnotation() *FunctionAnnotationNode {
	openParenToken := p.peekToken(0)
	if !p.consumeTokenIfType(TokenTypeOpenParenthesis) {
		p.addSyntaxError(localization.LocMessage.ExpectedOpenParen(), p.peekToken(0).GetRange())
		return nil
	}

	paramAnnotations := []ExpressionNode{}

	for {
		nextTokenType := p.peekTokenType()
		if nextTokenType == TokenTypeCloseParenthesis ||
			nextTokenType == TokenTypeNewLine ||
			nextTokenType == TokenTypeEndOfStream {
			break
		}

		// Consume "*" or "**" indicators but don't do anything with them.
		// (We don't enforce that these are present, absent, or match
		// the corresponding parameter types.)
		if !p.consumeTokenIfOperator(OperatorTypeMultiply) {
			p.consumeTokenIfOperator(OperatorTypePower)
		}

		paramAnnotation := p.parseTypeAnnotation(false /* allowUnpack */)
		paramAnnotations = append(paramAnnotations, paramAnnotation)

		if !p.consumeTokenIfType(TokenTypeComma) {
			break
		}
	}

	if !p.consumeTokenIfType(TokenTypeCloseParenthesis) {
		p.addSyntaxError(localization.LocMessage.ExpectedCloseParen(), openParenToken.GetRange())
		p.consumeTokensUntilType([]TokenType{TokenTypeColon})
	}

	if !p.consumeTokenIfType(TokenTypeArrow) {
		p.addSyntaxError(localization.LocMessage.ExpectedArrow(), p.peekToken(0).GetRange())
		return nil
	}

	returnType := p.parseTypeAnnotation(false /* allowUnpack */)

	isParamListEllipsis := false
	if len(paramAnnotations) == 1 && paramAnnotations[0].GetNodeType() == ParseNodeTypeEllipsis {
		paramAnnotations = []ExpressionNode{}
		isParamListEllipsis = true
	}

	return NewFunctionAnnotationNode(openParenToken, isParamListEllipsis, paramAnnotations, returnType)
}
