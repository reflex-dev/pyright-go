/*
 * parsetreeutils_print.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * printExpression and its helpers from analyzer/parseTreeUtils.ts
 * (pyright 1.1.412), lines 55-64 and 192-573.
 *
 * This is the only part of parseTreeUtils that typePrinter needs, so it is
 * split out ahead of the rest of the file.
 */

package analyzer

import (
	"sort"
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// PrintExpressionFlags corresponds to the const enum of the same name.
type PrintExpressionFlags int

const (
	PrintExpressionFlagsNone PrintExpressionFlags = 0

	// PrintExpressionFlagsForwardDeclarations avoids using string literals for
	// forward declarations.
	PrintExpressionFlagsForwardDeclarations PrintExpressionFlags = 1 << 0

	// PrintExpressionFlagsDoNotLimitStringLength uses the full original string.
	// By default, strings are truncated.
	PrintExpressionFlagsDoNotLimitStringLength PrintExpressionFlags = 1 << 1
)

// GetTypeSourceID corresponds to getTypeSourceId.
func GetTypeSourceID(node parser.ParseNode) int {
	return node.NodeBase().Start
}

// PrintArg corresponds to printArg.
func PrintArg(node *parser.ArgumentNode, flags PrintExpressionFlags) string {
	argStr := ""
	if node.D.ArgCategory == parser.ArgCategoryUnpackedList {
		argStr = "*"
	} else if node.D.ArgCategory == parser.ArgCategoryUnpackedDictionary {
		argStr = "**"
	}
	if node.D.Name != nil {
		argStr += node.D.Name.D.Value + "="
	}
	argStr += PrintExpression(node.D.ValueExpr, flags)
	return argStr
}

// PrintExpression corresponds to printExpression. The TypeScript defaults flags
// to None.
func PrintExpression(node parser.ExpressionNode, flags PrintExpressionFlags) string {
	switch n := node.(type) {
	case *parser.NameNode:
		return n.D.Value

	case *parser.MemberAccessNode:
		return PrintExpression(n.D.LeftExpr, flags) + "." + n.D.Member.D.Value

	case *parser.CallNode:
		lhs := PrintExpression(n.D.LeftExpr, flags)

		// Some left-hand expressions must be parenthesized.
		leftType := n.D.LeftExpr.GetNodeType()
		if leftType != parser.ParseNodeTypeMemberAccess &&
			leftType != parser.ParseNodeTypeName &&
			leftType != parser.ParseNodeTypeIndex &&
			leftType != parser.ParseNodeTypeCall {
			lhs = "(" + lhs + ")"
		}

		args := make([]string, 0, len(n.D.Args))
		for _, arg := range n.D.Args {
			args = append(args, PrintArg(arg, flags))
		}
		return lhs + "(" + strings.Join(args, ", ") + ")"

	case *parser.IndexNode:
		items := make([]string, 0, len(n.D.Items))
		for _, item := range n.D.Items {
			items = append(items, PrintArg(item, flags))
		}
		trailing := ""
		if n.D.TrailingComma {
			trailing = ","
		}
		return PrintExpression(n.D.LeftExpr, flags) + "[" + strings.Join(items, ", ") + trailing + "]"

	case *parser.UnaryOperationNode:
		exprStr := PrintOperator(n.D.Operator) + PrintExpression(n.D.Expr, flags)
		if n.D.HasParens {
			return "(" + exprStr + ")"
		}
		return exprStr

	case *parser.BinaryOperationNode:
		exprStr := PrintExpression(n.D.LeftExpr, flags) + " " +
			PrintOperator(n.D.Operator) + " " +
			PrintExpression(n.D.RightExpr, flags)

		if n.D.HasParens {
			return "(" + exprStr + ")"
		}
		return exprStr

	case *parser.NumberNode:
		// NumberValue.String reproduces Number/BigInt toString. The original
		// then strips a trailing "n", which JavaScript never actually appends
		// -- BigInt.prototype.toString() has no suffix; only the literal syntax
		// does. The strip is preserved anyway in case a value ever arrives with
		// one.
		value := n.D.Value.String()
		value = strings.TrimSuffix(value, "n")

		if n.D.IsImaginary {
			value += "j"
		}
		return value

	case *parser.StringListNode:
		if (flags&PrintExpressionFlagsForwardDeclarations) != 0 && n.D.Annotation != nil {
			return PrintExpression(n.D.Annotation, flags)
		}
		strs := make([]string, 0, len(n.D.Strings))
		for _, str := range n.D.Strings {
			// StringOrFormatStringNode is a narrower union than
			// ExpressionNode in the Go port, so it needs re-widening here.
			strs = append(strs, PrintExpression(str.(parser.ExpressionNode), flags))
		}
		return strings.Join(strs, " ")

	case *parser.StringNode:
		exprString := ""
		tokenFlags := n.D.Token.Flags
		if tokenFlags&parser.StringTokenFlagsRaw != 0 {
			exprString += "r"
		}
		if tokenFlags&parser.StringTokenFlagsUnicode != 0 {
			exprString += "u"
		}
		if tokenFlags&parser.StringTokenFlagsBytes != 0 {
			exprString += "b"
		}
		if tokenFlags&parser.StringTokenFlagsFormat != 0 {
			exprString += "f"
		}
		if tokenFlags&parser.StringTokenFlagsTemplate != 0 {
			exprString += "t"
		}

		escapedString := n.D.Token.EscapedValue
		if (flags & PrintExpressionFlagsDoNotLimitStringLength) == 0 {
			const maxStringLength = 32
			if escapedString.Length() > maxStringLength {
				escapedString = escapedString.Substring(0, maxStringLength)
			}
		}

		return exprString + quoteForToken(tokenFlags, escapedString.String())

	case *parser.FormatStringNode:
		exprString := "f"

		// The original merges the middle tokens and field expressions and
		// sorts by start offset so the pieces come out in source order.
		type fstringPiece struct {
			start int
			text  string
		}
		pieces := make([]fstringPiece, 0, len(n.D.MiddleTokens)+len(n.D.FieldExprs))
		for _, tok := range n.D.MiddleTokens {
			pieces = append(pieces, fstringPiece{start: tok.Start, text: tok.EscapedValue.String()})
		}
		for _, expr := range n.D.FieldExprs {
			// Note that the original calls printExpression without passing
			// flags here, so nested field expressions always use the defaults.
			pieces = append(pieces, fstringPiece{
				start: expr.NodeBase().Start,
				text:  "{" + PrintExpression(expr, PrintExpressionFlagsNone) + "}",
			})
		}
		sort.SliceStable(pieces, func(i, j int) bool { return pieces[i].start < pieces[j].start })

		escapedString := ""
		for _, piece := range pieces {
			escapedString += piece.text
		}

		return exprString + quoteForToken(n.D.Token.Flags, escapedString)

	case *parser.AssignmentNode:
		return PrintExpression(n.D.LeftExpr, flags) + " = " + PrintExpression(n.D.RightExpr, flags)

	case *parser.AssignmentExpressionNode:
		return PrintExpression(n.D.Name, flags) + " := " + PrintExpression(n.D.RightExpr, flags)

	case *parser.TypeAnnotationNode:
		return PrintExpression(n.D.ValueExpr, flags) + ": " + PrintExpression(n.D.Annotation, flags)

	case *parser.AugmentedAssignmentNode:
		return PrintExpression(n.D.LeftExpr, flags) + " " +
			PrintOperator(n.D.Operator) + " " +
			PrintExpression(n.D.RightExpr, flags)

	case *parser.AwaitNode:
		exprStr := "await " + PrintExpression(n.D.Expr, flags)
		if n.D.HasParens {
			return "(" + exprStr + ")"
		}
		return exprStr

	case *parser.TernaryNode:
		return PrintExpression(n.D.IfExpr, flags) + " if " +
			PrintExpression(n.D.TestExpr, flags) + " else " +
			PrintExpression(n.D.ElseExpr, flags)

	case *parser.ListNode:
		expressions := make([]string, 0, len(n.D.Items))
		for _, expr := range n.D.Items {
			expressions = append(expressions, PrintExpression(expr, flags))
		}
		return "[" + strings.Join(expressions, ", ") + "]"

	case *parser.UnpackNode:
		return "*" + PrintExpression(n.D.Expr, flags)

	case *parser.TupleNode:
		expressions := make([]string, 0, len(n.D.Items))
		for _, expr := range n.D.Items {
			expressions = append(expressions, PrintExpression(expr, flags))
		}
		if len(expressions) == 1 {
			return "(" + expressions[0] + ", )"
		}
		return "(" + strings.Join(expressions, ", ") + ")"

	case *parser.YieldNode:
		if n.D.Expr != nil {
			return "yield " + PrintExpression(n.D.Expr, flags)
		}
		return "yield"

	case *parser.YieldFromNode:
		return "yield from " + PrintExpression(n.D.Expr, flags)

	case *parser.EllipsisNode:
		return "..."

	case *parser.ComprehensionNode:
		listStr := "<ListExpression>"

		if expr, ok := n.D.Expr.(parser.ExpressionNode); ok && parser.IsExpressionNode(n.D.Expr) {
			listStr = PrintExpression(expr, flags)
		} else if entry, ok := n.D.Expr.(*parser.DictionaryKeyEntryNode); ok {
			keyStr := PrintExpression(entry.D.KeyExpr, flags)
			valueStr := PrintExpression(entry.D.ValueExpr, flags)
			listStr = keyStr + ": " + valueStr
		}

		clauses := make([]string, 0, len(n.D.ForIfNodes))
		for _, expr := range n.D.ForIfNodes {
			if forNode, ok := expr.(*parser.ComprehensionForNode); ok {
				asyncStr := ""
				if forNode.D.IsAsync {
					asyncStr = "async "
				}
				clauses = append(clauses, asyncStr+"for "+
					PrintExpression(forNode.D.TargetExpr, flags)+
					" in "+PrintExpression(forNode.D.IterableExpr, flags))
			} else {
				ifNode := expr.(*parser.ComprehensionIfNode)
				clauses = append(clauses, "if "+PrintExpression(ifNode.D.TestExpr, flags))
			}
		}

		listStr = listStr + " " + strings.Join(clauses, " ")

		if n.D.HasParens {
			return "(" + listStr + ")"
		}
		return listStr

	case *parser.SliceNode:
		result := ""

		if n.D.StartValue != nil || n.D.EndValue != nil || n.D.StepValue != nil {
			if n.D.StartValue != nil {
				result += PrintExpression(n.D.StartValue, flags)
			}
			if n.D.EndValue != nil {
				result += ": " + PrintExpression(n.D.EndValue, flags)
			}
			if n.D.StepValue != nil {
				result += ": " + PrintExpression(n.D.StepValue, flags)
			}
		} else {
			result += ":"
		}

		return result

	case *parser.LambdaNode:
		params := make([]string, 0, len(n.D.Params))
		for _, param := range n.D.Params {
			paramStr := ""

			if param.D.Category == parser.ParamCategoryArgsList {
				paramStr += "*"
			} else if param.D.Category == parser.ParamCategoryKwargsDict {
				paramStr += "**"
			}

			if param.D.Name != nil {
				paramStr += param.D.Name.D.Value
			} else if param.D.Category == parser.ParamCategorySimple {
				paramStr += "/"
			}

			if param.D.DefaultValue != nil {
				paramStr += " = " + PrintExpression(param.D.DefaultValue, flags)
			}
			params = append(params, paramStr)
		}

		return "lambda " + strings.Join(params, ", ") + ": " + PrintExpression(n.D.Expr, flags)

	case *parser.ConstantNode:
		switch n.D.ConstType {
		case parser.KeywordTypeTrue:
			return "True"
		case parser.KeywordTypeFalse:
			return "False"
		case parser.KeywordTypeDebug:
			return "__debug__"
		case parser.KeywordTypeNone:
			return "None"
		}
		// The original breaks out of the switch here and falls through to the
		// trailing "<Expression>".

	case *parser.DictionaryNode:
		entries := make([]string, 0, len(n.D.Items))
		for _, entry := range n.D.Items {
			switch e := entry.(type) {
			case *parser.DictionaryKeyEntryNode:
				entries = append(entries,
					PrintExpression(e.D.KeyExpr, flags)+": "+PrintExpression(e.D.ValueExpr, flags))
			case *parser.DictionaryExpandEntryNode:
				entries = append(entries, "**"+PrintExpression(e.D.Expr, flags))
			default:
				entries = append(entries, PrintExpression(entry.(parser.ExpressionNode), flags))
			}
		}

		// The original interpolates the array into a template literal, which
		// calls Array.prototype.toString() -- a comma join with no space. See
		// ../UPSTREAM-BUGS.md #9.
		dictContents := strings.Join(entries, ",")

		if dictContents != "" {
			return "{ " + dictContents + " }"
		}

		return "{}"

	case *parser.SetNode:
		// Note there are no surrounding braces; see ../UPSTREAM-BUGS.md #9.
		entries := make([]string, 0, len(n.D.Items))
		for _, entry := range n.D.Items {
			entries = append(entries, PrintExpression(entry, flags))
		}
		return strings.Join(entries, ", ")

	case *parser.ErrorNode:
		return "<Parse Error>"

	default:
		common.AssertNever(node.GetNodeType(), "")
	}

	return "<Expression>"
}

// quoteForToken wraps the escaped string in the quote style the token's flags
// call for. The original repeats this block for String and FormatString.
func quoteForToken(tokenFlags parser.StringTokenFlags, escaped string) string {
	if tokenFlags&parser.StringTokenFlagsTriplicate != 0 {
		if tokenFlags&parser.StringTokenFlagsSingleQuote != 0 {
			return "'''" + escaped + "'''"
		}
		return `"""` + escaped + `"""`
	}

	if tokenFlags&parser.StringTokenFlagsSingleQuote != 0 {
		return "'" + escaped + "'"
	}
	return `"` + escaped + `"`
}

// PrintOperator corresponds to printOperator.
func PrintOperator(operator parser.OperatorType) string {
	if operatorName, ok := parser.OperatorTypeNameMap[operator]; ok && operatorName != "" {
		return operatorName
	}
	return "unknown"
}
