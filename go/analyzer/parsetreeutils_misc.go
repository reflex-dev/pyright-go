/*
 * parsetreeutils_misc.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The remainder of analyzer/parseTreeUtils.ts (pyright 1.1.412), lines
 * 1991-2717: write-access detection, descendant search, import-position
 * predicates, dotted names, statement ranges, docstring lookup and scope IDs.
 */

package analyzer

import (
	"iter"
	"strconv"
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// IsWriteAccess corresponds to isWriteAccess.
func IsWriteAccess(node *parser.NameNode) bool {
	var prevNode parser.ParseNode = node
	curNode := prevNode.NodeBase().Parent

	for curNode != nil {
		switch typed := curNode.(type) {
		case *parser.AssignmentNode:
			return sameNode(prevNode, typed.D.LeftExpr)

		case *parser.AugmentedAssignmentNode:
			return sameNode(prevNode, typed.D.LeftExpr)

		case *parser.AssignmentExpressionNode:
			return sameNode(prevNode, typed.D.Name)

		case *parser.DelNode:
			return true

		case *parser.ForNode:
			return sameNode(prevNode, typed.D.TargetExpr)

		case *parser.ImportAsNode:
			if sameNode(prevNode, typed.D.Alias) {
				return true
			}
			return len(typed.D.Module.D.NameParts) > 0 &&
				sameNode(prevNode, typed.D.Module.D.NameParts[0])

		case *parser.ImportFromAsNode:
			if typed.D.Alias != nil {
				return sameNode(prevNode, typed.D.Alias)
			}
			return sameNode(prevNode, typed.D.Name)

		case *parser.MemberAccessNode:
			if !sameNode(prevNode, typed.D.Member) {
				return false
			}

		case *parser.ExceptNode:
			return sameNode(prevNode, typed.D.Name)

		case *parser.WithNode:
			for _, item := range typed.D.WithItems {
				if sameNode(prevNode, item) {
					return true
				}
			}
			return false

		case *parser.ComprehensionForNode:
			return sameNode(prevNode, typed.D.TargetExpr)

		case *parser.TypeAnnotationNode:
			if sameNode(prevNode, typed.D.Annotation) {
				return false
			}

		case *parser.FunctionNode, *parser.ClassNode, *parser.ModuleNode:
			return false
		}

		prevNode = curNode
		curNode = curNode.NodeBase().Parent
	}

	return false
}

// GetMatchingDescendants corresponds to getMatchingDescendants.
func GetMatchingDescendants(node parser.ParseNode, match func(n parser.ParseNode) bool) []parser.ParseNode {
	matches := []parser.ParseNode{}
	for _, child := range GetChildNodes(node) {
		if child == nil {
			continue
		}
		if match(child) {
			matches = append(matches, child)
		}
		matches = common.AppendArray(matches, GetMatchingDescendants(child, match))
	}
	return matches
}

// GetModuleNode corresponds to getModuleNode. The original returns the walked
// node, which is either a ModuleNode or undefined.
func GetModuleNode(node parser.ParseNode) *parser.ModuleNode {
	current := node
	for current != nil {
		if module, ok := current.(*parser.ModuleNode); ok {
			return module
		}
		current = current.NodeBase().Parent
	}

	return nil
}

// GetFileInfoFromNode corresponds to getFileInfoFromNode.
func GetFileInfoFromNode(node parser.ParseNode) *AnalyzerFileInfo {
	current := GetModuleNode(node)
	if current == nil {
		return nil
	}
	return GetFileInfo(current)
}

// IsFunctionSuiteEmpty corresponds to isFunctionSuiteEmpty.
func IsFunctionSuiteEmpty(node *parser.FunctionNode) bool {
	isEmpty := true

	for _, statement := range node.D.Suite.D.Statements {
		switch typed := statement.(type) {
		case *parser.ErrorNode:
			continue
		case *parser.StatementListNode:
			for _, subStatement := range typed.D.Statements {
				// Allow docstrings, ellipsis, and pass statements.
				switch subStatement.GetNodeType() {
				case parser.ParseNodeTypeEllipsis, parser.ParseNodeTypeStringList, parser.ParseNodeTypePass:
				default:
					isEmpty = false
				}
			}
		default:
			isEmpty = false
		}
	}

	return isEmpty
}

// GetTypeAnnotationForParam corresponds to getTypeAnnotationForParam.
func GetTypeAnnotationForParam(node *parser.FunctionNode, paramIndex int) parser.ExpressionNode {
	if paramIndex >= len(node.D.Params) {
		return nil
	}

	param := node.D.Params[paramIndex]
	if param.D.Annotation != nil {
		return param.D.Annotation
	} else if param.D.AnnotationComment != nil {
		return param.D.AnnotationComment
	}

	if node.D.FuncAnnotationComment == nil || node.D.FuncAnnotationComment.D.IsEllipsis {
		return nil
	}

	firstCommentAnnotationIndex := 0
	paramAnnotations := node.D.FuncAnnotationComment.D.ParamAnnotations
	if len(paramAnnotations) < len(node.D.Params) {
		firstCommentAnnotationIndex = 1
	}

	adjIndex := paramIndex - firstCommentAnnotationIndex
	if adjIndex < 0 || adjIndex >= len(paramAnnotations) {
		return nil
	}

	return paramAnnotations[adjIndex]
}

// IsImportModuleName corresponds to isImportModuleName.
func IsImportModuleName(node parser.ParseNode) bool {
	return parentNodeTypeOfFirstAncestorOfKind(node, parser.ParseNodeTypeModuleName) == parser.ParseNodeTypeImportAs
}

// IsImportAlias corresponds to isImportAlias.
func IsImportAlias(node parser.ParseNode) bool {
	importAs, ok := node.NodeBase().Parent.(*parser.ImportAsNode)
	return ok && importAs.D.Alias != nil && parser.ParseNode(importAs.D.Alias) == node
}

// IsFromImportModuleName corresponds to isFromImportModuleName.
func IsFromImportModuleName(node parser.ParseNode) bool {
	return parentNodeTypeOfFirstAncestorOfKind(node, parser.ParseNodeTypeModuleName) == parser.ParseNodeTypeImportFrom
}

// IsFromImportName corresponds to isFromImportName.
func IsFromImportName(node parser.ParseNode) bool {
	importFromAs, ok := node.NodeBase().Parent.(*parser.ImportFromAsNode)
	return ok && parser.ParseNode(importFromAs.D.Name) == node
}

// IsFromImportAlias corresponds to isFromImportAlias.
func IsFromImportAlias(node parser.ParseNode) bool {
	importFromAs, ok := node.NodeBase().Parent.(*parser.ImportFromAsNode)
	return ok && importFromAs.D.Alias != nil && parser.ParseNode(importFromAs.D.Alias) == node
}

// IsLastNameOfModuleName corresponds to isLastNameOfModuleName.
func IsLastNameOfModuleName(node *parser.NameNode) bool {
	module, ok := node.NodeBase().Parent.(*parser.ModuleNameNode)
	if !ok {
		return false
	}

	if len(module.D.NameParts) == 0 {
		return false
	}

	return module.D.NameParts[len(module.D.NameParts)-1] == node
}

// GetAncestorsIncludingSelf corresponds to the generator of the same name. It
// stays lazy so that GetFirstAncestorOrSelf stops walking as soon as the
// predicate matches, as the original does.
func GetAncestorsIncludingSelf(node parser.ParseNode) iter.Seq[parser.ParseNode] {
	return func(yield func(parser.ParseNode) bool) {
		for node != nil {
			if !yield(node) {
				return
			}
			node = node.NodeBase().Parent
		}
	}
}

// GetFirstAncestorOrSelfOfKind corresponds to getFirstAncestorOrSelfOfKind. The
// TypeScript generic parameter only casts the result.
func GetFirstAncestorOrSelfOfKind(node parser.ParseNode, nodeType parser.ParseNodeType) parser.ParseNode {
	return GetFirstAncestorOrSelf(node, func(n parser.ParseNode) bool {
		return n.GetNodeType() == nodeType
	})
}

// GetFirstAncestorOrSelf corresponds to getFirstAncestorOrSelf.
func GetFirstAncestorOrSelf(node parser.ParseNode, predicate func(node parser.ParseNode) bool) parser.ParseNode {
	var found parser.ParseNode
	GetAncestorsIncludingSelf(node)(func(current parser.ParseNode) bool {
		if predicate(current) {
			found = current
			return false
		}
		return true
	})
	return found
}

// GetDottedNameWithGivenNodeAsLastName corresponds to the function of the same
// name. The TypeScript return type is MemberAccessNode | NameNode.
func GetDottedNameWithGivenNodeAsLastName(node *parser.NameNode) parser.ExpressionNode {
	// Shape of dotted name is
	//    MemberAccess (ex, a.b)
	//  Name        Name
	// or
	//           MemberAccess (ex, a.b.c)
	//    MemberAccess     Name
	//  Name       Name
	memberAccess, ok := node.NodeBase().Parent.(*parser.MemberAccessNode)
	if !ok {
		return node
	}

	if parser.ParseNode(memberAccess.D.LeftExpr) == parser.ParseNode(node) {
		return node
	}

	return memberAccess
}

// GetDecoratorName returns the dotted name that makes up the expression for the
// decorator. For example
//
//	@pytest.fixture()
//	def my_fixture():
//	   pass
//
// returns `pytest.fixture`. The second result reports whether a name was found,
// standing in for the original's `string | undefined`.
func GetDecoratorName(decorator *parser.DecoratorNode) (string, bool) {
	return decoratorExpressionName(decorator.D.Expr)
}

// decoratorExpressionName corresponds to the nested getExpressionName.
func decoratorExpressionName(node parser.ExpressionNode) (string, bool) {
	switch typed := node.(type) {
	case *parser.NameNode:
		names, ok := GetDottedName(typed)
		if !ok {
			return "", false
		}
		return joinNameValues(names), true

	case *parser.MemberAccessNode:
		names, ok := GetDottedName(typed)
		if !ok {
			return "", false
		}
		return joinNameValues(names), true

	case *parser.CallNode:
		return decoratorExpressionName(typed.D.LeftExpr)
	}

	return "", false
}

// GetDottedName corresponds to getDottedName. The parameter is
// MemberAccessNode | NameNode in the original.
func GetDottedName(node parser.ExpressionNode) ([]*parser.NameNode, bool) {
	// ex) [a] or [a].b
	// simple case, [a]
	if name, ok := node.(*parser.NameNode); ok {
		return []*parser.NameNode{name}, true
	}

	// dotted name case.
	names := []*parser.NameNode{}
	if collectDottedName(node, &names) {
		for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
			names[i], names[j] = names[j], names[i]
		}
		return names, true
	}

	return nil, false
}

// collectDottedName corresponds to the nested _getDottedName.
func collectDottedName(node parser.ExpressionNode, names *[]*parser.NameNode) bool {
	if name, ok := node.(*parser.NameNode); ok {
		*names = append(*names, name)
		return true
	}

	memberAccess, ok := node.(*parser.MemberAccessNode)
	if !ok {
		return false
	}

	*names = append(*names, memberAccess.D.Member)

	switch memberAccess.D.LeftExpr.(type) {
	case *parser.NameNode, *parser.MemberAccessNode:
		return collectDottedName(memberAccess.D.LeftExpr, names)
	}

	return false
}

// GetFirstNameOfDottedName corresponds to getFirstNameOfDottedName.
func GetFirstNameOfDottedName(node parser.ExpressionNode) *parser.NameNode {
	// ex) [a] or [a].b
	if name, ok := node.(*parser.NameNode); ok {
		return name
	}

	memberAccess, ok := node.(*parser.MemberAccessNode)
	if !ok {
		return nil
	}

	switch memberAccess.D.LeftExpr.(type) {
	case *parser.NameNode, *parser.MemberAccessNode:
		return GetFirstNameOfDottedName(memberAccess.D.LeftExpr)
	}

	return nil
}

// IsFirstNameOfDottedName corresponds to isFirstNameOfDottedName.
func IsFirstNameOfDottedName(node *parser.NameNode) bool {
	// ex) [A] or [A].B.C.D
	memberAccess, ok := node.NodeBase().Parent.(*parser.MemberAccessNode)
	if !ok {
		return true
	}

	return parser.ParseNode(memberAccess.D.LeftExpr) == parser.ParseNode(node)
}

// IsLastNameOfDottedName corresponds to isLastNameOfDottedName.
func IsLastNameOfDottedName(node *parser.NameNode) bool {
	// ex) A or D.C.B.[A]
	memberAccess, ok := node.NodeBase().Parent.(*parser.MemberAccessNode)
	if !ok {
		return true
	}

	switch memberAccess.D.LeftExpr.(type) {
	case *parser.NameNode, *parser.MemberAccessNode:
	default:
		return false
	}

	if parser.ParseNode(memberAccess.D.LeftExpr) == parser.ParseNode(node) {
		return false
	}

	grandparent := memberAccess.NodeBase().Parent
	if grandparent == nil {
		return true
	}
	return grandparent.GetNodeType() != parser.ParseNodeTypeMemberAccess
}

// GetStringNodeValueRange corresponds to getStringNodeValueRange.
func GetStringNodeValueRange(node *parser.StringNode) common.TextRange {
	return GetStringValueRange(node.D.Token)
}

// GetStringValueRange corresponds to getStringValueRange.
func GetStringValueRange(token *parser.StringToken) common.TextRange {
	length := token.QuoteMarkLength
	hasEnding := (token.Flags & parser.StringTokenFlagsUnterminated) == 0
	trailing := 0
	if hasEnding {
		trailing = length
	}
	return common.NewTextRange(token.GetRange().Start+length, token.GetRange().Length-length-trailing)
}

// FullStatementRangeOptions corresponds to the options parameter of
// getFullStatementRange. The TypeScript leaves it undefined; pass nil.
type FullStatementRangeOptions struct {
	IncludeTrailingBlankLines bool
}

// GetFullStatementRange corresponds to getFullStatementRange.
func GetFullStatementRange(
	statementNode parser.ParseNode,
	parseFileResults *parser.ParseFileResults,
	options *FullStatementRangeOptions,
) common.Range {
	tokenizerOutput := parseFileResults.TokenizerOutput
	nodeTextRange := nodeRange(statementNode)
	rng := common.ConvertTextRangeToRange(nodeTextRange, tokenizerOutput.Lines)

	start, ok := startPositionIfMultipleStatementsAreOnSameLine(
		rng,
		statementNode.NodeBase().Start,
		tokenizerOutput,
	)
	if !ok {
		start = common.Position{Line: rng.Start.Line, Character: 0}
	}

	// First, see whether there are other tokens except semicolon or new line on
	// the same line.
	end, ok := endPositionIfMultipleStatementsAreOnSameLine(
		rng,
		nodeTextRange.End(),
		tokenizerOutput,
	)

	if ok {
		return common.Range{Start: start, End: end}
	}

	// If not, delete the whole line.
	if rng.End.Line == tokenizerOutput.Lines.Count()-1 {
		return common.Range{Start: start, End: rng.End}
	}

	lineDeltaToAdd := 1
	if options != nil && options.IncludeTrailingBlankLines {
		for i := lineDeltaToAdd; rng.End.Line+i < tokenizerOutput.Lines.Count(); i++ {
			if !IsBlankLine(tokenizerOutput, parseFileResults.Text, rng.End.Line+i) {
				lineDeltaToAdd = i
				break
			}
		}
	}

	return common.Range{Start: start, End: common.Position{Line: rng.End.Line + lineDeltaToAdd, Character: 0}}
}

// IsBlankLine corresponds to isBlankLine.
func IsBlankLine(tokenizerOutput *parser.TokenizerOutput, text common.Text, line int) bool {
	span := tokenizerOutput.Lines.GetItemAt(line)
	return common.ContainsOnlyWhitespace(text, span.Start, span.End())
}

// IsUnannotatedFunction corresponds to isUnannotatedFunction.
func IsUnannotatedFunction(node *parser.FunctionNode) bool {
	if node.D.ReturnAnnotation != nil {
		return false
	}
	for _, param := range node.D.Params {
		if param.D.Annotation != nil || param.D.AnnotationComment != nil {
			return false
		}
	}
	return true
}

// IsValidLocationForFutureImport verifies that an import of the form
// "from __future__ import x" occurs only at the top of a file. This mirrors the
// algorithm used in the CPython interpreter.
func IsValidLocationForFutureImport(node *parser.ImportFromNode) bool {
	module := GetModuleNode(node)
	assert(module != nil, "")

	sawDocString := false

	for _, statement := range module.D.Statements {
		statementList, ok := statement.(*parser.StatementListNode)
		if !ok {
			return false
		}

		for _, simpleStatement := range statementList.D.Statements {
			if parser.ParseNode(simpleStatement) == parser.ParseNode(node) {
				return true
			}

			switch typed := simpleStatement.(type) {
			case *parser.StringListNode:
				if sawDocString {
					return false
				}
				sawDocString = true

			case *parser.ImportFromNode:
				if typed.D.Module.D.LeadingDots != 0 ||
					len(typed.D.Module.D.NameParts) != 1 ||
					typed.D.Module.D.NameParts[0].D.Value != "__future__" {
					return false
				}

			default:
				return false
			}
		}
	}

	return false
}

// OperatorSupportsChaining corresponds to operatorSupportsChaining.
//
// "Chaining" is when binary operators can be chained together as a shorthand.
// For example, "a < b < c" is shorthand for "a < b and b < c".
func OperatorSupportsChaining(op parser.OperatorType) bool {
	switch op {
	case parser.OperatorTypeEquals,
		parser.OperatorTypeNotEquals,
		parser.OperatorTypeLessThan,
		parser.OperatorTypeLessThanOrEqual,
		parser.OperatorTypeGreaterThan,
		parser.OperatorTypeGreaterThanOrEqual,
		parser.OperatorTypeIs,
		parser.OperatorTypeIsNot,
		parser.OperatorTypeIn,
		parser.OperatorTypeNotIn:
		return true
	}

	return false
}

// startPositionIfMultipleStatementsAreOnSameLine corresponds to
// _getStartPositionIfMultipleStatementsAreOnSameLine. If the statement is part
// of multiple statements on the same line and is not the first statement on the
// line, it returns the appropriate start position.
//
//	ex) a = 1; [|b = 1|]
func startPositionIfMultipleStatementsAreOnSameLine(
	rng common.Range,
	tokenPosition int,
	tokenizerOutput *parser.TokenizerOutput,
) (common.Position, bool) {
	tokenIndex := tokenizerOutput.Tokens.GetItemAtPosition(tokenPosition)
	if tokenIndex < 0 {
		return common.Position{}, false
	}

	// Find the last token index on the previous line or the first token.
	currentIndex := tokenIndex
	for ; currentIndex > 0; currentIndex-- {
		token := tokenizerOutput.Tokens.GetItemAt(currentIndex)
		tokenRange := common.ConvertTextRangeToRange(token.GetRange(), tokenizerOutput.Lines)
		if tokenRange.End.Line != rng.Start.Line {
			break
		}
	}

	// Find the previous token of the first token of the statement.
	for index := tokenIndex - 1; index > currentIndex; index-- {
		token := tokenizerOutput.Tokens.GetItemAt(index)

		// Eat up indentation
		if token.GetType() == parser.TokenTypeIndent || token.GetType() == parser.TokenTypeDedent {
			continue
		}

		// If previous token is new line, use default.
		if token.GetType() == parser.TokenTypeNewLine {
			return common.Position{}, false
		}

		// Anything else (ex, semicolon), use statement start as it is.
		return rng.Start, true
	}

	return common.Position{}, false
}

// endPositionIfMultipleStatementsAreOnSameLine corresponds to
// _getEndPositionIfMultipleStatementsAreOnSameLine. If the statement is part of
// multiple statements on the same line and is not the last statement on the
// line, it returns the appropriate end position.
//
//	ex) [|a = 1|]; b = 1
func endPositionIfMultipleStatementsAreOnSameLine(
	rng common.Range,
	tokenPosition int,
	tokenizerOutput *parser.TokenizerOutput,
) (common.Position, bool) {
	tokenIndex := tokenizerOutput.Tokens.GetItemAtPosition(tokenPosition)
	if tokenIndex < 0 {
		return common.Position{}, false
	}

	// Find the first token index on the next line or the last token.
	currentIndex := tokenIndex
	for ; currentIndex < tokenizerOutput.Tokens.Count(); currentIndex++ {
		token := tokenizerOutput.Tokens.GetItemAt(currentIndex)
		tokenRange := common.ConvertTextRangeToRange(token.GetRange(), tokenizerOutput.Lines)
		if rng.End.Line != tokenRange.Start.Line {
			break
		}
	}

	// Find the next token of the last token of the statement.
	foundStatementEnd := false
	for index := tokenIndex; index < currentIndex; index++ {
		token := tokenizerOutput.Tokens.GetItemAt(index)

		// Eat up semicolon or new line.
		if token.GetType() == parser.TokenTypeSemicolon || token.GetType() == parser.TokenTypeNewLine {
			foundStatementEnd = true
			continue
		}

		if !foundStatementEnd {
			continue
		}

		tokenRange := common.ConvertTextRangeToRange(token.GetRange(), tokenizerOutput.Lines)
		return tokenRange.Start, true
	}

	return common.Position{}, false
}

// GetVariableDocStringNode corresponds to getVariableDocStringNode.
func GetVariableDocStringNode(node parser.ExpressionNode) *parser.StringListNode {
	// Walk up the parse tree to find an assignment or type alias statement.
	var curNode parser.ParseNode = node
	var annotationNode *parser.TypeAnnotationNode

	for curNode != nil {
		if curNode.GetNodeType() == parser.ParseNodeTypeAssignment {
			break
		}

		if curNode.GetNodeType() == parser.ParseNodeTypeTypeAlias {
			break
		}

		if curNode.GetNodeType() == parser.ParseNodeTypeSuite {
			break
		}

		if annotation, ok := curNode.(*parser.TypeAnnotationNode); ok && annotationNode == nil {
			annotationNode = annotation
		}

		curNode = curNode.NodeBase().Parent
	}

	isAssignmentOrTypeAlias := curNode != nil &&
		(curNode.GetNodeType() == parser.ParseNodeTypeAssignment ||
			curNode.GetNodeType() == parser.ParseNodeTypeTypeAlias)

	if !isAssignmentOrTypeAlias {
		// Allow a simple annotation statement to have a docstring even though
		// PEP 258 doesn't mention this case. This PEP pre-dated PEP 526, so it
		// didn't contemplate this situation.
		if annotationNode != nil {
			curNode = annotationNode
		} else {
			return nil
		}
	}

	// Chained assignments (e.g. `a = b = c = value`) parse into nested
	// Assignment nodes where only the outermost node's parent is the
	// StatementList that can hold the trailing PEP 258 attribute docstring. Walk
	// up through the chain so every target (a, b, c) resolves the same
	// docstring. This is a no-op for non-chained assignments.
	for curNode.GetNodeType() == parser.ParseNodeTypeAssignment &&
		curNode.NodeBase().Parent != nil &&
		curNode.NodeBase().Parent.GetNodeType() == parser.ParseNodeTypeAssignment {
		curNode = curNode.NodeBase().Parent
	}

	parentNode, ok := curNode.NodeBase().Parent.(*parser.StatementListNode)
	if !ok {
		return nil
	}

	suiteOrModuleStatements, ok := statementsOfModuleOrSuite(parentNode.NodeBase().Parent)
	if !ok {
		return nil
	}

	assignmentIndex := -1
	for index, statement := range suiteOrModuleStatements {
		if parser.ParseNode(statement) == parser.ParseNode(parentNode) {
			assignmentIndex = index
			break
		}
	}
	if assignmentIndex < 0 || assignmentIndex == len(suiteOrModuleStatements)-1 {
		return nil
	}

	nextStatement, ok := suiteOrModuleStatements[assignmentIndex+1].(*parser.StatementListNode)
	if !ok || !IsDocString(nextStatement) {
		return nil
	}

	// See if the assignment is within one of the contexts specified in PEP 258.
	isValidContext := false
	grandparent := parentNode.NodeBase().Parent
	if grandparent != nil && grandparent.GetNodeType() == parser.ParseNodeTypeModule {
		// If we're at the top level of a module, the attribute docstring is
		// valid.
		isValidContext = true
	} else if grandparent != nil && grandparent.GetNodeType() == parser.ParseNodeTypeSuite &&
		grandparent.NodeBase().Parent != nil &&
		grandparent.NodeBase().Parent.GetNodeType() == parser.ParseNodeTypeClass {
		// If we're at the top level of a class, the attribute docstring is
		// valid.
		isValidContext = true
	} else {
		function := GetEnclosingFunction(parentNode)

		// If we're within an __init__ method, the attribute docstring is valid.
		if function != nil && function.D.Name.D.Value == "__init__" &&
			GetEnclosingClass(function, true) != nil {
			isValidContext = true
		}
	}

	if !isValidContext {
		return nil
	}

	// A docstring can consist of multiple joined strings in a single expression.
	return nextStatement.D.Statements[0].(*parser.StringListNode)
}

// GetScopeIdForNode creates an ID that identifies this parse node in a way that
// will not change each time the file is parsed (unless, of course, the file
// contents change).
func GetScopeIdForNode(node parser.ParseNode) string {
	name := ""
	switch typed := node.(type) {
	case *parser.ClassNode:
		name = typed.D.Name.D.Value
	case *parser.FunctionNode:
		name = typed.D.Name.D.Value
	}

	fileInfo := GetFileInfo(node)
	return fileInfo.FileID + "." + strconv.Itoa(node.NodeBase().Start) + "-" + name
}

// GetTypeVarScopesForNode walks up the parse tree and finds all scopes that can
// provide a context for a TypeVar, returning the scope ID for each.
func GetTypeVarScopesForNode(node parser.ParseNode) []TypeVarScopeId {
	scopeIds := []TypeVarScopeId{}

	var curNode parser.ParseNode = node
	for curNode != nil {
		scopeNode := GetTypeVarScopeNode(curNode)
		if scopeNode == nil {
			break
		}

		scopeIds = append(scopeIds, TypeVarScopeId(GetScopeIdForNode(scopeNode)))
		curNode = scopeNode.NodeBase().Parent
	}

	return scopeIds
}

// CheckDecorator corresponds to checkDecorator.
func CheckDecorator(node *parser.DecoratorNode, value string) bool {
	name, ok := node.D.Expr.(*parser.NameNode)
	return ok && name.D.Value == value
}

// IsSimpleDefault corresponds to isSimpleDefault.
func IsSimpleDefault(node parser.ExpressionNode) bool {
	switch typed := node.(type) {
	case *parser.NumberNode, *parser.ConstantNode, *parser.MemberAccessNode:
		return true

	case *parser.StringNode:
		return (typed.D.Token.Flags & (parser.StringTokenFlagsFormat | parser.StringTokenFlagsTemplate)) == 0

	case *parser.StringListNode:
		for _, s := range typed.D.Strings {
			expr, ok := s.(parser.ExpressionNode)
			if !ok || !IsSimpleDefault(expr) {
				return false
			}
		}
		return true

	case *parser.UnaryOperationNode:
		return IsSimpleDefault(typed.D.Expr)

	case *parser.BinaryOperationNode:
		return IsSimpleDefault(typed.D.LeftExpr) && IsSimpleDefault(typed.D.RightExpr)
	}

	return false
}

// parentNodeTypeOfFirstAncestorOfKind corresponds to the
// `getFirstAncestorOrSelfOfKind(...)?.parent?.nodeType` chain used by the
// import predicates. It returns -1 when either optional link is absent, which
// no ParseNodeType uses.
func parentNodeTypeOfFirstAncestorOfKind(node parser.ParseNode, nodeType parser.ParseNodeType) parser.ParseNodeType {
	ancestor := GetFirstAncestorOrSelfOfKind(node, nodeType)
	if ancestor == nil {
		return -1
	}
	parent := ancestor.NodeBase().Parent
	if parent == nil {
		return -1
	}
	return parent.GetNodeType()
}

// joinNameValues corresponds to `.map((n) => n.d.value).join('.')`.
func joinNameValues(names []*parser.NameNode) string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name.D.Value)
	}
	return strings.Join(parts, ".")
}
