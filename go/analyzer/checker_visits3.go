/*
 * checker_visits3.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412): the fourteen visit
 * methods the walk was still missing -- visitReturn, visitYield, visitYieldFrom,
 * visitAssert, visitLambda, visitList, visitSet, visitDictionary,
 * visitStringList, visitFormatString, visitImportFrom, visitImportFromAs,
 * visitModuleName and visitTypeParameter -- plus _validateYieldType,
 * _getImportResult and _addMissingModuleSourceDiagnosticIfNeeded.
 *
 * A missing visit method is the quietest possible defect. The walk still runs,
 * every other check still fires, and the only evidence is a diagnostic that
 * never appears -- which is indistinguishable from a program that is correct.
 * visitReturn alone accounted for about seventy missing diagnostics across
 * twenty-five of the gate's failing tests: nothing in the port was checking that
 * a `return` expression matched the declared return type at all.
 *
 * visitReturn is the substantial one. Note the second attempt after the first
 * assignType fails: if the declared return type mentions constrained TypeVars,
 * the original narrows each of them to a single type against this specific
 * return statement and retries. That is not an optimization -- for
 * `def f(x: _T) -> _T` with `_T` constrained to `int | str`, the return
 * expression's type is only assignable once the TypeVar has been pinned to the
 * branch this return sits in, so without the retry every constrained-TypeVar
 * function reports a spurious error.
 *
 * The `makeTypeVarsBound` calls are the other thing worth reading. A TypeVar in
 * a return annotation is free in the signature but *bound* inside the body: the
 * caller has already chosen it. Comparing against the free form would let
 * `return 3` satisfy `-> _T`, which is exactly the bug this guards.
 *
 * visitImportFromAs carries the deprecation check for imported names, which is
 * where the PEP 585 aliases (`typing.List` and friends) are reported. Those have
 * no marker in typeshed -- the table in checker_deprecated.go is the only record
 * -- so nothing else in the walk could have produced them.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// VisitReturn corresponds to visitReturn.
func (c *Checker) VisitReturn(node *parser.ReturnNode) bool {
	var returnTypeResult *TypeResult

	enclosingFunctionNode := GetEnclosingFunction(node)
	var declaredReturnType Type
	if enclosingFunctionNode != nil {
		declaredReturnType = c.evaluator.GetDeclaredReturnType(enclosingFunctionNode)
	}

	if node.D.Expr != nil {
		returnTypeResult = c.evaluator.GetTypeResult(node.D.Expr)
		if returnTypeResult == nil {
			returnTypeResult = &TypeResult{Type: UnknownTypeCreate(false)}
		}
	} else {
		// The original's comment: there is no return expression, so "None" is
		// assumed.
		returnTypeResult = &TypeResult{Type: c.evaluator.GetNoneType()}
	}

	returnType := returnTypeResult.Type

	// The original's comment: if this type is a special form, use the special
	// form instead.
	if props := returnType.Base().Props; props != nil && props.SpecialForm != nil {
		returnType = props.SpecialForm
	}

	// The original's comment: if the enclosing function is async and a
	// generator, the return statement is not allowed to have an argument. A
	// syntax error occurs at runtime in this case.
	if enclosingFunctionNode != nil && enclosingFunctionNode.D.IsAsync && node.D.Expr != nil {
		functionDecl, isFunctionDecl := IsFunctionDeclaration(GetDeclaration(enclosingFunctionNode))
		if isFunctionDecl && functionDecl.IsGenerator {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.ReturnInAsyncGenerator(), node.D.Expr, nil)
		}
	}

	if !c.evaluator.IsNodeReachable(node, nil) || enclosingFunctionNode == nil {
		return true
	}

	if declaredReturnType != nil {
		if IsNever(declaredReturnType) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.NoReturnContainsReturn(), node, nil)
		} else {
			c.validateReturnTypeMatches(node, declaredReturnType, returnType, returnTypeResult)
		}
	}

	var errorNode parser.ParseNode = node
	if node.D.Expr != nil {
		errorNode = node.D.Expr
	}

	if IsUnknown(returnType) {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportUnknownVariableType,
			localization.LocMessage.ReturnTypeUnknown(), errorNode, nil)
	} else if IsPartlyUnknown(returnType, 0) {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportUnknownVariableType,
			localization.LocMessage.ReturnTypePartiallyUnknown().Format(
				c.evaluator.PrintType(returnType, &PrintTypeOptions{ExpandTypeAlias: true})),
			errorNode, nil)
	}

	return true
}

// validateReturnTypeMatches is visitReturn's else arm, split out because its
// retry against narrowed constrained TypeVars is long enough to bury the rest.
func (c *Checker) validateReturnTypeMatches(
	node *parser.ReturnNode, declaredReturnType Type, returnType Type, returnTypeResult *TypeResult,
) {
	liveScopes := GetTypeVarScopesForNode(node)
	declaredReturnType = c.evaluator.StripTypeGuard(declaredReturnType)
	adjReturnType := MakeTypeVarsBound(declaredReturnType, liveScopes, true)

	diagAddendum := common.NewDiagnosticAddendum()
	returnTypeMatches := false

	if c.evaluator.AssignType(adjReturnType, returnType, diagAddendum, nil, AssignTypeFlagsDefault, 0) {
		returnTypeMatches = true
	} else {
		// The original's comment: see if the declared return type includes one
		// or more constrained TypeVars. If so, try to narrow these TypeVars to a
		// single type.
		uniqueTypeVars := GetTypeVarArgsRecursive(declaredReturnType, 0)

		hasConstrained := false
		for _, typeVar := range uniqueTypeVars {
			if TypeVarTypeHasConstraints(typeVar) {
				hasConstrained = true
				break
			}
		}

		if hasConstrained {
			constraints := NewConstraintTracker()

			for _, typeVar := range uniqueTypeVars {
				if !TypeVarTypeHasConstraints(typeVar) {
					continue
				}
				narrowedType := c.evaluator.NarrowConstrainedTypeVar(node, TypeVarTypeCloneAsBound(typeVar))
				if narrowedType != nil {
					constraints.SetBounds(typeVar, narrowedType, nil, false)
				}
			}

			if !constraints.IsEmpty() {
				adjReturnType = c.evaluator.SolveAndApplyConstraints(declaredReturnType, constraints, nil, nil)
				adjReturnType = MakeTypeVarsBound(adjReturnType, liveScopes, true)

				if c.evaluator.AssignType(adjReturnType, returnType, diagAddendum, nil, AssignTypeFlagsDefault, 0) {
					returnTypeMatches = true
				}
			}
		}
	}

	if returnTypeMatches {
		return
	}

	// The original's comment: if we have more detailed diagnostic information
	// from bidirectional type inference, use that.
	if returnTypeResult.ExpectedTypeDiagAddendum != nil {
		diagAddendum = returnTypeResult.ExpectedTypeDiagAddendum
	}

	var errorNode parser.ParseNode = node
	if node.D.Expr != nil {
		errorNode = node.D.Expr
	}

	var textRange *common.TextRange
	if returnTypeResult.ExpectedTypeDiagAddendum != nil {
		textRange = returnTypeResult.ExpectedTypeDiagAddendum.GetEffectiveTextRange()
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportReturnType,
		localization.LocMessage.ReturnTypeMismatch().Format(
			c.evaluator.PrintType(returnType, nil),
			c.evaluator.PrintType(declaredReturnType, nil))+diagAddendum.GetString(),
		errorNode, textRange)
}

// VisitYield corresponds to visitYield.
func (c *Checker) VisitYield(node *parser.YieldNode) bool {
	var yieldTypeResult *TypeResult
	if node.D.Expr != nil {
		yieldTypeResult = c.evaluator.GetTypeResult(node.D.Expr)
	} else {
		yieldTypeResult = &TypeResult{Type: c.evaluator.GetNoneType()}
	}

	yieldType := Type(UnknownTypeCreate(false))
	var expectedDiag *common.DiagnosticAddendum
	if yieldTypeResult != nil {
		if yieldTypeResult.Type != nil {
			yieldType = yieldTypeResult.Type
		}
		expectedDiag = yieldTypeResult.ExpectedTypeDiagAddendum
	}

	c.validateYieldType(node, node.D.Expr, yieldType, expectedDiag, nil)
	return true
}

// VisitYieldFrom corresponds to visitYieldFrom.
func (c *Checker) VisitYieldFrom(node *parser.YieldFromNode) bool {
	yieldFromType := c.evaluator.GetType(node.D.Expr)
	if yieldFromType == nil {
		yieldFromType = UnknownTypeCreate(false)
	}

	var yieldType Type
	var sendType Type

	if IsClassInstance(yieldFromType) &&
		ClassTypeIsBuiltInNamed(yieldFromType.(*ClassType), "Coroutine", "CoroutineType") {
		// The original's comment: handle the case of old-style (pre-await)
		// coroutines.
		yieldType = UnknownTypeCreate(false)
	} else {
		yieldType = UnknownTypeCreate(false)
		if iterable := c.evaluator.GetTypeOfIterable(
			&TypeResult{Type: yieldFromType}, false, node, nil); iterable != nil && iterable.Type != nil {
			yieldType = iterable.Type
		}

		// The original's comment: does the iterator return a Generator? If so,
		// get the yield type from it. If the iterator doesn't return a
		// Generator, use the iterator return type directly.
		generatorTypeArgs := GetGeneratorTypeArgs(yieldType)
		if generatorTypeArgs != nil {
			yieldType = UnknownTypeCreate(false)
			if len(generatorTypeArgs) >= 1 {
				yieldType = generatorTypeArgs[0]
			}
			if len(generatorTypeArgs) >= 2 {
				sendType = generatorTypeArgs[1]
			}
		} else {
			yieldType = UnknownTypeCreate(false)
			if iterator := c.evaluator.GetTypeOfIterator(
				&TypeResult{Type: yieldFromType}, false, node, nil); iterator != nil && iterator.Type != nil {
				yieldType = iterator.Type
			}
		}
	}

	c.validateYieldType(node, node.D.Expr, yieldType, nil, sendType)

	return true
}

// validateYieldType corresponds to _validateYieldType. The original takes a
// `YieldNode | YieldFromNode` and reads `node.d.expr` off it; Go has no union,
// so the expression is passed alongside the node.
func (c *Checker) validateYieldType(
	node parser.ParseNode,
	yieldExpr parser.ExpressionNode,
	yieldType Type,
	expectedDiagAddendum *common.DiagnosticAddendum,
	sendType Type,
) {
	enclosingFunctionNode := GetEnclosingFunction(node)
	if enclosingFunctionNode == nil || enclosingFunctionNode.D.ReturnAnnotation == nil {
		return
	}

	functionTypeResult := c.evaluator.GetTypeOfFunction(enclosingFunctionNode)
	if functionTypeResult == nil {
		return
	}

	declaredReturnType := FunctionTypeGetEffectiveReturnType(functionTypeResult.FunctionType, true)
	if declaredReturnType == nil {
		return
	}

	liveScopes := GetTypeVarScopesForNode(node)
	declaredReturnType = MakeTypeVarsBound(declaredReturnType, liveScopes, true)

	var generatorType Type
	if !enclosingFunctionNode.D.IsAsync && IsClassInstance(declaredReturnType) &&
		ClassTypeIsBuiltInNamed(declaredReturnType.(*ClassType), "AwaitableGenerator") {
		// The original's comment: handle the old-style (pre-await) generator
		// case if the return type explicitly uses AwaitableGenerator.
		generatorType = c.evaluator.GetTypeCheckerInternalsType(node, "AwaitableGenerator")
		if generatorType == nil {
			generatorType = c.evaluator.GetTypingType(node, "AwaitableGenerator")
		}
	} else {
		name := "Generator"
		if enclosingFunctionNode.D.IsAsync {
			name = "AsyncGenerator"
		}
		generatorType = c.evaluator.GetTypingType(node, name)
	}

	if generatorType == nil || !IsInstantiableClass(generatorType) {
		return
	}

	if !c.evaluator.IsNodeReachable(node, nil) {
		return
	}

	if IsNever(declaredReturnType) {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.NoReturnContainsYield(), node, nil)
		return
	}

	adjSendType := sendType
	if adjSendType == nil {
		adjSendType = UnknownTypeCreate(false)
	}
	generatorTypeArgs := []Type{yieldType, adjSendType, UnknownTypeCreate(false)}
	specializedGenerator := ClassTypeCloneAsInstance(
		ClassTypeSpecialize(generatorType.(*ClassType), generatorTypeArgs, nil, false, nil, nil), true)

	diagAddendum := common.NewDiagnosticAddendum()
	if c.evaluator.AssignType(declaredReturnType, specializedGenerator, diagAddendum,
		nil, AssignTypeFlagsDefault, 0) {
		return
	}

	printedYieldType := c.evaluator.PrintType(yieldType, nil)
	errorMessage := localization.LocMessage.GeneratorSyncReturnType().Format(printedYieldType)
	if enclosingFunctionNode.D.IsAsync {
		errorMessage = localization.LocMessage.GeneratorAsyncReturnType().Format(printedYieldType)
	}

	addendumText := diagAddendum.GetString()
	if expectedDiagAddendum != nil {
		addendumText = expectedDiagAddendum.GetString()
	}

	var errorNode parser.ParseNode = node
	if yieldExpr != nil {
		errorNode = yieldExpr
	}

	// The original passes `expectedDiagAddendum?.getEffectiveTextRange() ??
	// node.d.expr ?? node` as the text range, so the fallback is the error
	// node's own range rather than nothing.
	textRange := errorNode.GetRange()
	if expectedDiagAddendum != nil {
		if effective := expectedDiagAddendum.GetEffectiveTextRange(); effective != nil {
			textRange = *effective
		}
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportReturnType,
		errorMessage+addendumText,
		errorNode, &textRange)
}

// VisitAssert corresponds to visitAssert.
func (c *Checker) VisitAssert(node *parser.AssertNode) bool {
	if node.D.ExceptionExpr != nil {
		c.evaluator.GetType(node.D.ExceptionExpr)
	}

	c.validateConditionalIsBool(node.D.TestExpr)

	// The original's comment: specifically look for a common programming error
	// where the two arguments to an assert are enclosed in parens and
	// interpreted as a two-element tuple.
	//   assert (x > 3, "bad value x")
	t := c.evaluator.GetType(node.D.TestExpr)
	if t == nil || !IsClassInstance(t) {
		return true
	}

	cls := t.(*ClassType)
	if !IsTupleClass(cls) || cls.Priv.TupleTypeArgs == nil || len(cls.Priv.TupleTypeArgs) == 0 {
		return true
	}

	if !IsUnboundedTupleClass(cls) {
		c.evaluator.AddDiagnosticForTextRange(c.fileInfo, DiagnosticRuleReportAssertAlwaysTrue,
			localization.LocMessage.AssertAlwaysTrue(), node.D.TestExpr.GetRange())
	}

	return true
}

// VisitLambda corresponds to visitLambda.
func (c *Checker) VisitLambda(node *parser.LambdaNode) bool {
	c.evaluator.GetType(node)

	// The original's comment: walk the children.
	children := make([]parser.ParseNode, 0, len(node.D.Params)+1)
	for _, param := range node.D.Params {
		children = append(children, param)
	}
	children = append(children, node.D.Expr)
	c.WalkMultiple(children)

	for _, param := range node.D.Params {
		if param.D.Name == nil {
			continue
		}

		paramType := c.evaluator.GetType(param.D.Name)
		if paramType == nil {
			continue
		}

		if IsUnknown(paramType) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportUnknownLambdaType,
				localization.LocMessage.ParamTypeUnknown().Format(param.D.Name.D.Value),
				param.D.Name, nil)
		} else if IsPartlyUnknown(paramType, 0) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportUnknownLambdaType,
				localization.LocMessage.ParamTypePartiallyUnknown().Format(param.D.Name.D.Value),
				param.D.Name, nil)
		}
	}

	returnType := c.evaluator.GetType(node.D.Expr)
	if returnType != nil {
		if IsUnknown(returnType) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportUnknownLambdaType,
				localization.LocMessage.LambdaReturnTypeUnknown(), node.D.Expr, nil)
		} else if IsPartlyUnknown(returnType, 0) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportUnknownLambdaType,
				localization.LocMessage.LambdaReturnTypePartiallyUnknown().Format(
					c.evaluator.PrintType(returnType, &PrintTypeOptions{ExpandTypeAlias: true})),
				node.D.Expr, nil)
		}
	}

	c.scopedNodes = append(c.scopedNodes, node)

	return false
}

// VisitList corresponds to visitList.
func (c *Checker) VisitList(node *parser.ListNode) bool {
	c.validateIllegalDefaultParamInitializer(node)
	return true
}

// VisitSet corresponds to visitSet.
func (c *Checker) VisitSet(node *parser.SetNode) bool {
	c.validateIllegalDefaultParamInitializer(node)
	return true
}

// VisitDictionary corresponds to visitDictionary.
func (c *Checker) VisitDictionary(node *parser.DictionaryNode) bool {
	c.validateIllegalDefaultParamInitializer(node)
	return true
}

// stringListToken carries what visitStringList reads off a string token.
// parser.StringTokenLike keeps its accessors unexported, so the two concrete
// token kinds are flattened into this rather than read through the interface.
type stringListToken struct {
	token        parser.StringTokenLike
	start        int
	escapedValue common.Text
}

// VisitStringList corresponds to visitStringList.
func (c *Checker) VisitStringList(node *parser.StringListNode) bool {
	// The original's comment: if this is Python 3.11 or older, there are several
	// restrictions associated with f-strings that we need to validate. Determine
	// whether we're within an f-string (or multiple f-strings if nesting is
	// used).
	var fStringContainers []*parser.FormatStringNode
	if c.fileInfo.ExecutionEnvironment.PythonVersion.IsLessThan(common.PythonVersion3_12) {
		var curNode parser.ParseNode = node
		for curNode != nil {
			if fstring, ok := curNode.(*parser.FormatStringNode); ok {
				fStringContainers = append(fStringContainers, fstring)
			}
			curNode = curNode.NodeBase().Parent
		}
	}

	// Whether any string in the list is a bytes literal decides which of the two
	// escape messages is used, and it is computed over the whole list rather
	// than per token.
	isBytes := false
	for _, stringNode := range node.D.Strings {
		if s, ok := stringNode.(*parser.StringNode); ok && (s.D.Token.Flags&parser.StringTokenFlagsBytes) != 0 {
			isBytes = true
			break
		}
	}

	for _, stringNode := range node.D.Strings {
		// StringTokenLike keeps its accessors unexported, so the two token kinds
		// are collected into a shape this loop can read rather than being read
		// through the interface.
		var stringTokens []stringListToken
		switch s := stringNode.(type) {
		case *parser.StringNode:
			// The original's comment: `start += token.prefixLength +
			// token.quoteMarkLength` applies only to a TokenType.String.
			stringTokens = []stringListToken{{
				token:        s.D.Token,
				start:        s.D.Token.Start + s.D.Token.PrefixLength + s.D.Token.QuoteMarkLength,
				escapedValue: s.D.Token.EscapedValue,
			}}
		case *parser.FormatStringNode:
			for _, token := range s.D.MiddleTokens {
				stringTokens = append(stringTokens, stringListToken{
					token:        token,
					start:        token.Start,
					escapedValue: token.EscapedValue,
				})
			}
		}

		for _, entry := range stringTokens {
			unescapedResult := parser.GetUnescapedString(entry.token)
			start := entry.start

			for _, err := range unescapedResult.UnescapeErrors {
				if err.ErrorType != parser.UnescapeErrorTypeInvalidEscapeSequence {
					continue
				}

				message := localization.LocMessage.StringUnsupportedEscape()
				if isBytes {
					message = localization.LocMessage.BytesUnsupportedEscape()
				}

				c.evaluator.AddDiagnosticForTextRange(c.fileInfo,
					DiagnosticRuleReportInvalidStringEscapeSequence, message,
					common.TextRange{Start: start + err.Offset, Length: err.Length})
			}

			// The original's comment: prior to Python 3.12, it was not allowed
			// to include a slash in an f-string.
			if len(fStringContainers) > 0 {
				if escapeOffset := entry.escapedValue.IndexOfString("\\"); escapeOffset >= 0 {
					c.evaluator.AddDiagnosticForTextRange(c.fileInfo,
						DiagnosticRuleReportGeneralTypeIssues,
						localization.LocMessage.FormatStringEscape(),
						common.TextRange{Start: start, Length: 1})
				}
			}
		}

		// The original's comment: prior to Python 3.12, it was not allowed to
		// nest strings that used the same quote scheme within an f-string.
		//
		// The original reads `stringNode.d.token.flags` for both node kinds,
		// which is only well typed because StringNode and FormatStringNode both
		// have a `token` with flags; only the StringNode case can actually
		// collide with an enclosing f-string's quotes.
		if len(fStringContainers) == 0 {
			continue
		}

		var nodeFlags parser.StringTokenFlags
		switch s := stringNode.(type) {
		case *parser.StringNode:
			nodeFlags = s.D.Token.Flags
		case *parser.FormatStringNode:
			nodeFlags = s.D.Token.Flags
		}

		quoteTypeMask := parser.StringTokenFlagsSingleQuote | parser.StringTokenFlagsDoubleQuote |
			parser.StringTokenFlagsTriplicate
		for _, fStringContainer := range fStringContainers {
			if (fStringContainer.D.Token.Flags & quoteTypeMask) == (nodeFlags & quoteTypeMask) {
				c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.FormatStringNestedQuote(), stringNode, nil)
				break
			}
		}
	}

	if node.D.Annotation != nil {
		c.evaluator.GetType(node)
	}

	if len(node.D.Strings) > 1 && !node.D.HasParens {
		c.evaluator.AddDiagnosticForTextRange(c.fileInfo,
			DiagnosticRuleReportImplicitStringConcatenation,
			localization.LocMessage.ImplicitStringConcat(), node.GetRange())
	}

	return true
}

// VisitFormatString corresponds to visitFormatString.
func (c *Checker) VisitFormatString(node *parser.FormatStringNode) bool {
	for _, expr := range node.D.FieldExprs {
		c.evaluator.GetType(expr)
	}

	for _, expr := range node.D.FormatExprs {
		c.evaluator.GetType(expr)
	}

	return true
}

// VisitImportFrom corresponds to visitImportFrom.
func (c *Checker) VisitImportFrom(node *parser.ImportFromNode) bool {
	// The original's comment: verify that any "__future__" import occurs at the
	// top of the file.
	if node.D.Module.D.LeadingDots == 0 && len(node.D.Module.D.NameParts) == 1 &&
		node.D.Module.D.NameParts[0].D.Value == "__future__" {
		if !IsValidLocationForFutureImport(node) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.FutureImportLocationNotAllowed(), node, nil)
		}
	}

	if !node.D.IsWildcardImport {
		for _, importAs := range node.D.Imports {
			c.evaluator.EvaluateTypesForStatement(importAs)
		}

		return true
	}

	c.evaluator.EvaluateTypesForStatement(node)

	importInfo := GetImportInfo(node.D.Module)
	if importInfo != nil && importInfo.IsImportFound &&
		importInfo.ImportType != ImportTypeLocal && !c.fileInfo.IsStubFile {
		textRange := node.GetRange()
		if node.D.WildcardToken != nil {
			textRange = node.D.WildcardToken.GetRange()
		}

		c.evaluator.AddDiagnosticForTextRange(c.fileInfo,
			DiagnosticRuleReportWildcardImportFromLibrary,
			localization.LocMessage.WildcardLibraryImport(), textRange)
	}

	return true
}

// VisitImportFromAs corresponds to visitImportFromAs.
func (c *Checker) VisitImportFromAs(node *parser.ImportFromAsNode) bool {
	if c.fileInfo.IsStubFile {
		return false
	}

	declInfo := c.evaluator.GetDeclInfoForNameNode(node.D.Name, nil)
	if declInfo == nil || declInfo.Decls == nil {
		return false
	}

	for _, decl := range declInfo.Decls {
		// The original's comment: if it is not implicitly imported module, move
		// to next.
		aliasDecl, isAlias := IsAliasDeclaration(decl)
		if !isAlias || aliasDecl.SubmoduleFallback == nil || aliasDecl.Node != parser.ParseNode(node) {
			continue
		}

		resolvedAlias := c.evaluator.ResolveAliasDeclaration(aliasDecl, true, nil)
		if resolvedAlias == nil {
			continue
		}
		resolvedAliasUri := resolvedAlias.DeclBase().Uri
		if resolvedAliasUri == nil || resolvedAliasUri.LastExtension() != ".pyi" {
			continue
		}

		importResult := c.getImportResult(node, resolvedAliasUri)
		if importResult == nil {
			continue
		}

		c.addMissingModuleSourceDiagnosticIfNeeded(importResult, node.D.Name)
		break
	}

	isImportFromTyping := false
	if importFrom, ok := node.NodeBase().Parent.(*parser.ImportFromNode); ok {
		if importFrom.D.Module.D.LeadingDots == 0 && len(importFrom.D.Module.D.NameParts) == 1 {
			namePart := importFrom.D.Module.D.NameParts[0].D.Value
			if namePart == "typing" || namePart == "typing_extensions" {
				isImportFromTyping = true
			}
		}
	}

	var nameNode parser.ExpressionNode = node.D.Name
	if node.D.Alias != nil {
		nameNode = node.D.Alias
	}
	t := c.evaluator.GetType(nameNode)
	c.reportDeprecatedUseForType(node.D.Name, t, isImportFromTyping)

	return false
}

// VisitModuleName corresponds to visitModuleName. The original asserts that the
// import info is present; a nil check stands in, because a failed assertion in
// the original aborts the whole file's analysis rather than this one check.
func (c *Checker) VisitModuleName(node *parser.ModuleNameNode) bool {
	if c.fileInfo.IsStubFile {
		return false
	}

	importResult := GetImportInfo(node)
	if importResult == nil {
		return false
	}

	c.addMissingModuleSourceDiagnosticIfNeeded(importResult, node)
	return false
}

// VisitTypeParameter corresponds to visitTypeParameter.
func (c *Checker) VisitTypeParameter(node *parser.TypeParameterNode) bool {
	// The original's comment: verify that there are no live type variables with
	// the same name in outer scopes.
	//
	// The three parent hops step off the TypeParameter, its list and the
	// declaration that owns the list, so the walk starts outside the scope the
	// parameter itself introduces.
	var curNode parser.ParseNode
	if p := node.NodeBase().Parent; p != nil {
		if pp := p.NodeBase().Parent; pp != nil {
			curNode = pp.NodeBase().Parent
		}
	}

	foundDuplicate := false

	for curNode != nil {
		typeVarScopeNode := GetTypeVarScopeNode(curNode)
		if typeVarScopeNode == nil {
			break
		}

		switch scopeNode := typeVarScopeNode.(type) {
		case *parser.ClassNode:
			if classResult := c.evaluator.GetTypeOfClass(scopeNode); classResult != nil {
				for _, param := range classResult.ClassType.Shared.TypeParams {
					if param.Shared.Name == node.D.Name.D.Value {
						foundDuplicate = true
						break
					}
				}
			}
		case *parser.FunctionNode:
			if functionResult := c.evaluator.GetTypeOfFunction(scopeNode); functionResult != nil {
				for _, param := range functionResult.FunctionType.Shared.TypeParams {
					if param.Shared.Name == node.D.Name.D.Value {
						foundDuplicate = true
						break
					}
				}
			}
		}

		if foundDuplicate {
			break
		}

		curNode = typeVarScopeNode.NodeBase().Parent
	}

	if foundDuplicate {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeVarUsedByOuterScope().Format(node.D.Name.D.Value),
			node.D.Name, nil)
	}

	return false
}

// getImportResult corresponds to _getImportResult.
func (c *Checker) getImportResult(node *parser.ImportFromAsNode, u uri.Uri) *ImportResult {
	configOptions := c.importResolver.GetConfigOptions()
	execEnv := configOptions.FindExecEnvironment(u)

	importFrom, ok := node.NodeBase().Parent.(*parser.ImportFromNode)
	if !ok {
		return nil
	}
	moduleNameNode := importFrom.D.Module

	// The original's comment: handle both absolute and relative imports.
	var moduleName string
	if moduleNameNode.D.LeadingDots == 0 {
		moduleName = c.importResolver.GetModuleNameForImport(u, execEnv, false, false).ModuleName
	} else {
		moduleName = GetRelativeModuleName(c.importResolver.FileSystem(), c.fileInfo.FileUri, u,
			configOptions, false, nil)
	}

	if moduleName == "" {
		return nil
	}

	return c.importResolver.ResolveImport(c.fileInfo.FileUri, execEnv,
		CreateImportedModuleDescriptor(moduleName))
}

// addMissingModuleSourceDiagnosticIfNeeded corresponds to
// _addMissingModuleSourceDiagnosticIfNeeded.
func (c *Checker) addMissingModuleSourceDiagnosticIfNeeded(
	importResult *ImportResult, node parser.ParseNode,
) {
	if importResult.IsNativeLib ||
		!importResult.IsStubFile ||
		importResult.ImportType == ImportTypeBuiltIn ||
		importResult.NonStubImportResult == nil ||
		importResult.NonStubImportResult.IsImportFound ||
		// The original's comment: the non-stub resolution can report
		// `isImportFound === false` yet `isNativeLib === true` when a compiled
		// extension (.pyd/.so) backs a path segment of the import. A compiled
		// extension has no Python source, but the module itself exists at
		// runtime via the native lib, so "could not be resolved from source" is
		// misleading.
		importResult.NonStubImportResult.IsNativeLib {
		return
	}

	// The original's comment: type stub found, but source is missing.
	c.evaluator.AddDiagnostic(DiagnosticRuleReportMissingModuleSource,
		localization.LocMessage.ImportSourceResolveFailure().Format(
			importResult.ImportName, c.fileInfo.ExecutionEnvironment.Name),
		node, nil)
}
