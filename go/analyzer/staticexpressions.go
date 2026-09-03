/*
 * staticexpressions.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Functions that operate on expressions (parse node trees)
 * whose values can be evaluated statically.
 *
 * Transliterated from analyzer/staticExpressions.ts (pyright 1.1.412).
 *
 * The original's `boolean | undefined` return becomes `(value, ok bool)`
 * throughout: ok reports "not undefined".
 *
 * Two JavaScript behaviors this file depends on:
 *
 *   - An empty array is truthy. `if (typingImportAliases)` is taken for `[]`,
 *     so the alias-list parameters map to "nil means undefined", never to a
 *     length check.
 *   - A defaulted parameter takes its default only for `undefined`, not for an
 *     empty array, so `_isSysVersionInfoExpression(node, [])` matches nothing
 *     while `_isSysVersionInfoExpression(node, nil)` matches `sys`.
 */

package analyzer

import (
	"math/big"
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// EvaluateStaticBoolExpression returns ok=false if the expression cannot be
// evaluated statically as a bool value, or true/false if it can.
//
// The TypeScript leaves typingImportAliases and sysImportAliases undefined;
// pass nil for that.
func EvaluateStaticBoolExpression(
	node parser.ExpressionNode,
	execEnv *ExecutionEnvironment,
	definedConstants *common.OrderedMap[string, DefinedConstantValue],
	typingImportAliases []string,
	sysImportAliases []string,
) (bool, bool) {
	return evaluateStaticBoolOrBoolLikeExpression(
		node,
		execEnv,
		definedConstants,
		typingImportAliases,
		sysImportAliases,
		evaluateBoolConstant,
	)
}

// EvaluateStaticBoolLikeExpression is similar to EvaluateStaticBoolExpression
// except that it handles other non-bool values that are statically falsy or
// truthy (like "None", "...", and numeric/string/container literals).
//
// The TypeScript leaves typingImportAliases and sysImportAliases undefined;
// pass nil for that.
func EvaluateStaticBoolLikeExpression(
	node parser.ExpressionNode,
	execEnv *ExecutionEnvironment,
	definedConstants *common.OrderedMap[string, DefinedConstantValue],
	typingImportAliases []string,
	sysImportAliases []string,
) (bool, bool) {
	return evaluateStaticBoolOrBoolLikeExpression(
		node,
		execEnv,
		definedConstants,
		typingImportAliases,
		sysImportAliases,
		evaluateBoolLikeLiteral,
	)
}

// evaluateStaticBoolOrBoolLikeExpression is the shared implementation of the
// two functions above. The evaluateLeafAsBool callback evaluates leaf
// expressions.
func evaluateStaticBoolOrBoolLikeExpression(
	node parser.ExpressionNode,
	execEnv *ExecutionEnvironment,
	definedConstants *common.OrderedMap[string, DefinedConstantValue],
	typingImportAliases []string,
	sysImportAliases []string,
	evaluateLeafAsBool func(node parser.ExpressionNode) (bool, bool),
) (bool, bool) {
	if assignment, ok := node.(*parser.AssignmentExpressionNode); ok {
		return evaluateStaticBoolOrBoolLikeExpression(
			assignment.D.RightExpr,
			execEnv,
			definedConstants,
			typingImportAliases,
			sysImportAliases,
			evaluateLeafAsBool,
		)
	}

	switch typed := node.(type) {
	case *parser.UnaryOperationNode:
		if typed.D.Operator == parser.OperatorTypeNot {
			value, ok := evaluateStaticBoolOrBoolLikeExpression(
				typed.D.Expr,
				execEnv,
				definedConstants,
				typingImportAliases,
				sysImportAliases,
				// `not x` always forces a truthiness context on its operand and
				// yields real `bool`. So the operand is folded with the
				// bool-like leaf even for strict callers.
				evaluateBoolLikeLiteral,
			)
			if ok {
				return !value, true
			}
		}

	case *parser.BinaryOperationNode:
		// Is it an OR or AND expression?
		if typed.D.Operator == parser.OperatorTypeOr || typed.D.Operator == parser.OperatorTypeAnd {
			leftValue, leftOk := evaluateStaticBoolOrBoolLikeExpression(
				typed.D.LeftExpr,
				execEnv,
				definedConstants,
				typingImportAliases,
				sysImportAliases,
				evaluateLeafAsBool,
			)
			rightValue, rightOk := evaluateStaticBoolOrBoolLikeExpression(
				typed.D.RightExpr,
				execEnv,
				definedConstants,
				typingImportAliases,
				sysImportAliases,
				evaluateLeafAsBool,
			)

			if !leftOk || !rightOk {
				return false, false
			}

			if typed.D.Operator == parser.OperatorTypeOr {
				return leftValue || rightValue, true
			}
			return leftValue && rightValue, true
		}

		if rightTuple, ok := typed.D.RightExpr.(*parser.TupleNode); ok &&
			isSysVersionInfoExpression(typed.D.LeftExpr, sysImportAliases) {
			// Handle the special case of "sys.version_info >= (3, x)".
			comparisonVersion, comparisonOk := convertTupleToVersion(rightTuple)
			return evaluateVersionBinaryOperation(
				typed.D.Operator,
				execEnv.PythonVersion, true,
				comparisonVersion, comparisonOk,
			)
		}

		if leftIndex, ok := typed.D.LeftExpr.(*parser.IndexNode); ok {
			if rightNumber, ok := typed.D.RightExpr.(*parser.NumberNode); ok &&
				isSysVersionInfoExpression(leftIndex.D.LeftExpr, sysImportAliases) &&
				len(leftIndex.D.Items) == 1 &&
				!leftIndex.D.TrailingComma &&
				leftIndex.D.Items[0].D.Name == nil &&
				leftIndex.D.Items[0].D.ArgCategory == parser.ArgCategorySimple &&
				isZeroIndexNumber(leftIndex.D.Items[0].D.ValueExpr) &&
				rightNumber.D.IsInteger &&
				!rightNumber.D.Value.IsBigInt {
				// Handle the special case of "sys.version_info[0] >= X".
				return evaluateVersionBinaryOperation(
					typed.D.Operator,
					common.NewPythonVersionMajorMinor(execEnv.PythonVersion.Major, 0), true,
					common.NewPythonVersionMajorMinor(int(rightNumber.D.Value.Float), 0), true,
				)
			}
		}

		if rightStrings, ok := typed.D.RightExpr.(*parser.StringListNode); ok &&
			isSysPlatformInfoExpression(typed.D.LeftExpr, sysImportAliases) {
			// Handle the special case of "sys.platform != 'X'".
			comparisonPlatform := joinStringList(rightStrings)
			expectedPlatformName, expectedOk := getExpectedPlatformNameFromPlatform(execEnv)
			return evaluateStringBinaryOperation(
				typed.D.Operator,
				expectedPlatformName, expectedOk,
				comparisonPlatform, true,
			)
		}

		if isOsNameInfoExpression(typed.D.LeftExpr) {
			if rightStrings, ok := typed.D.RightExpr.(*parser.StringListNode); ok {
				// Handle the special case of "os.name == 'X'".
				comparisonOsName := joinStringList(rightStrings)
				expectedOsName, expectedOk := getExpectedOsNameFromPlatform(execEnv)
				if expectedOk {
					return evaluateStringBinaryOperation(
						typed.D.Operator,
						expectedOsName, true,
						comparisonOsName, true,
					)
				}
			}
		} else {
			// Handle the special case of <definedConstant> == 'X' or
			// <definedConstant> != 'X'.
			if rightStrings, ok := typed.D.RightExpr.(*parser.StringListNode); ok {
				var constantValue DefinedConstantValue
				constantOk := false

				switch left := typed.D.LeftExpr.(type) {
				case *parser.NameNode:
					constantValue, constantOk = definedConstants.Get(left.D.Value)
				case *parser.MemberAccessNode:
					constantValue, constantOk = definedConstants.Get(left.D.Member.D.Value)
				}

				if constantOk && constantValue.IsString {
					comparisonStringName := joinStringList(rightStrings)
					return evaluateStringBinaryOperation(
						typed.D.Operator,
						constantValue.String, true,
						comparisonStringName, true,
					)
				}
			}
		}

	case *parser.NameNode:
		if typed.D.Value == "TYPE_CHECKING" {
			return true, true
		}

		if constant, ok := definedConstants.Get(typed.D.Value); ok {
			return definedConstantTruthy(constant), true
		}

	case *parser.MemberAccessNode:
		if typingImportAliases != nil && typed.D.Member.D.Value == "TYPE_CHECKING" {
			if leftName, ok := typed.D.LeftExpr.(*parser.NameNode); ok {
				for _, alias := range typingImportAliases {
					if alias == leftName.D.Value {
						return true, true
					}
				}
			}
		}

		if constant, ok := definedConstants.Get(typed.D.Member.D.Value); ok {
			return definedConstantTruthy(constant), true
		}
	}

	return evaluateLeafAsBool(node)
}

// definedConstantTruthy corresponds to `!!constant` over the
// `boolean | string` union: a string is truthy when it is non-empty.
func definedConstantTruthy(constant DefinedConstantValue) bool {
	if constant.IsString {
		return constant.String != ""
	}
	return constant.Bool
}

// isZeroIndexNumber corresponds to the three conjuncts that check the subscript
// of "sys.version_info[0]": a Number node, not imaginary, with value 0.
func isZeroIndexNumber(node parser.ExpressionNode) bool {
	number, ok := node.(*parser.NumberNode)
	if !ok || number.D.IsImaginary {
		return false
	}

	// The original compares `d.value === 0`, which is true for both the number
	// 0 and -- because `===` between a bigint and a number is not applied here,
	// the comparison being `0n === 0` -- false for 0n. JavaScript's `===`
	// across types is false, so a bigint literal never matches.
	if number.D.Value.IsBigInt {
		return false
	}
	return number.D.Value.Float == 0
}

// joinStringList corresponds to `node.d.strings.map((s) => s.d.value).join(”)`.
func joinStringList(node *parser.StringListNode) string {
	var builder strings.Builder
	for _, s := range node.D.Strings {
		builder.WriteString(stringOrFormatValue(s))
	}
	return builder.String()
}

func evaluateBoolConstant(node parser.ExpressionNode) (bool, bool) {
	if constant, ok := node.(*parser.ConstantNode); ok {
		if constant.D.ConstType == parser.KeywordTypeTrue {
			return true, true
		}
		if constant.D.ConstType == parser.KeywordTypeFalse {
			return false, true
		}
	}

	return false, false
}

func evaluateBoolLikeLiteral(node parser.ExpressionNode) (bool, bool) {
	switch typed := node.(type) {
	case *parser.ConstantNode:
		if typed.D.ConstType == parser.KeywordTypeTrue {
			return true, true
		}
		if typed.D.ConstType == parser.KeywordTypeFalse || typed.D.ConstType == parser.KeywordTypeNone {
			return false, true
		}
		return false, false

	case *parser.EllipsisNode:
		return true, true

	case *parser.NumberNode:
		return evaluateNumberTruthiness(typed), true

	case *parser.StringListNode:
		return evaluateStringListTruthiness(typed)

	case *parser.ListNode:
		return evaluateSequenceTruthiness(typed.D.Items)

	case *parser.SetNode:
		return evaluateSequenceTruthiness(typed.D.Items)

	case *parser.TupleNode:
		return evaluateSequenceTruthiness(typed.D.Items)

	case *parser.DictionaryNode:
		return evaluateDictTruthiness(typed.D.Items)

	default:
		return false, false
	}
}

func evaluateNumberTruthiness(node *parser.NumberNode) bool {
	// bool(v) is False iff v == 0:
	// - zero (0, 0.0, 0j) is falsy;
	// - everything else is truthy, including infinity and NaN.
	if node.D.Value.IsBigInt {
		return node.D.Value.BigInt.Cmp(big.NewInt(0)) != 0
	}
	// NaN != 0 is true in both languages, so this keeps NaN truthy.
	return node.D.Value.Float != 0
}

func evaluateStringListTruthiness(node *parser.StringListNode) (bool, bool) {
	// If any segment is an f-string, the concatenated value is runtime-dependent
	// (e.g. f"{x}" may be empty or not).
	for _, str := range node.D.Strings {
		if str.GetNodeType() == parser.ParseNodeTypeFormatString {
			return false, false
		}
	}

	// Truthy iff any segment is non-empty.
	for _, str := range node.D.Strings {
		// Every segment is a StringNode here, so the length is the UTF-16
		// length of `d.value`.
		if s, ok := str.(*parser.StringNode); ok && s.D.Value.Length() > 0 {
			return true, true
		}
	}
	return false, true
}

func evaluateSequenceTruthiness(items []parser.ExpressionNode) (bool, bool) {
	if len(items) == 0 {
		return false, true
	}

	// A concrete element (not an unpack "*x" or a comprehension) guarantees the
	// sequence is non-empty.
	for _, item := range items {
		nodeType := item.GetNodeType()
		if nodeType != parser.ParseNodeTypeUnpack && nodeType != parser.ParseNodeTypeComprehension {
			return true, true
		}
	}

	// Only unpacks/comprehensions remain (e.g. "[*x]", "[i for i in y]").
	return false, false
}

func evaluateDictTruthiness(items []parser.DictionaryEntryNode) (bool, bool) {
	if len(items) == 0 {
		return false, true
	}

	// A concrete key/value entry guarantees the dict is non-empty.
	for _, item := range items {
		if item.GetNodeType() == parser.ParseNodeTypeDictionaryKeyEntry {
			return true, true
		}
	}

	// Only "**" expansions or comprehensions remain.
	return false, false
}

// convertTupleToVersion corresponds to _convertTupleToVersion.
//
// The components of a PythonVersion are Go ints, but the original stores
// whatever `d.value` holds, which for a literal like `(3, 12.5)` is a
// non-integer. Such a tuple is nonsense as a version comparison and the
// original does not reject it either -- it never checks `d.isInteger` -- so
// this truncates too.
func convertTupleToVersion(node *parser.TupleNode) (common.PythonVersion, bool) {
	if len(node.D.Items) >= 2 {
		majorNode, majorOk := node.D.Items[0].(*parser.NumberNode)
		minorNode, minorOk := node.D.Items[1].(*parser.NumberNode)
		if majorOk && !majorNode.D.IsImaginary && minorOk && !minorNode.D.IsImaginary {
			if majorNode.D.Value.IsBigInt || minorNode.D.Value.IsBigInt {
				return common.PythonVersion{}, false
			}

			major := int(majorNode.D.Value.Float)
			minor := int(minorNode.D.Value.Float)

			var micro *int
			if len(node.D.Items) >= 3 {
				if microNode, ok := node.D.Items[2].(*parser.NumberNode); ok &&
					!microNode.D.IsImaginary && !microNode.D.Value.IsBigInt {
					value := int(microNode.D.Value.Float)
					micro = &value
				}
			}

			var releaseLevel *common.PythonReleaseLevel
			if len(node.D.Items) >= 4 {
				if stringList, ok := node.D.Items[3].(*parser.StringListNode); ok &&
					len(stringList.D.Strings) == 1 {
					// The original casts `d.value` to PythonReleaseLevel
					// without checking it is one of the four levels, so an
					// arbitrary string reaches PythonVersion.
					if str, ok := stringList.D.Strings[0].(*parser.StringNode); ok {
						value := common.PythonReleaseLevel(str.D.Value.String())
						releaseLevel = &value
					}
				}
			}

			var serial *int
			if len(node.D.Items) >= 5 {
				if serialNode, ok := node.D.Items[4].(*parser.NumberNode); ok &&
					!serialNode.D.IsImaginary && !serialNode.D.Value.IsBigInt {
					value := int(serialNode.D.Value.Float)
					serial = &value
				}
			}

			return common.NewPythonVersion(major, minor, micro, releaseLevel, serial), true
		}
	} else if len(node.D.Items) == 1 {
		// The original casts items[0] to NumberNode without checking the node
		// type, then guards on `typeof major.d.value === 'number'`. NumberNode
		// is the only expression node whose `d.value` is a number, so the cast
		// is unchecked but not reachable with anything else. Note that unlike
		// the multi-element branch there is no isImaginary check here, so
		// `(3j,)` is read as version 3.0.
		if major, ok := node.D.Items[0].(*parser.NumberNode); ok && !major.D.Value.IsBigInt {
			return common.NewPythonVersionMajorMinor(int(major.D.Value.Float), 0), true
		}
	}

	return common.PythonVersion{}, false
}

func evaluateVersionBinaryOperation(
	operatorType parser.OperatorType,
	leftValue common.PythonVersion, leftOk bool,
	rightValue common.PythonVersion, rightOk bool,
) (bool, bool) {
	if leftOk && rightOk {
		switch operatorType {
		case parser.OperatorTypeLessThan:
			return leftValue.IsLessThan(rightValue), true
		case parser.OperatorTypeLessThanOrEqual:
			return leftValue.IsLessOrEqualTo(rightValue), true
		case parser.OperatorTypeGreaterThan:
			return leftValue.IsGreaterThan(rightValue), true
		case parser.OperatorTypeGreaterThanOrEqual:
			return leftValue.IsGreaterOrEqualTo(rightValue), true
		case parser.OperatorTypeEquals:
			return leftValue.IsEqualTo(rightValue), true
		case parser.OperatorTypeNotEquals:
			return !leftValue.IsEqualTo(rightValue), true
		}
	}

	return false, false
}

func evaluateStringBinaryOperation(
	operatorType parser.OperatorType,
	leftValue string, leftOk bool,
	rightValue string, rightOk bool,
) (bool, bool) {
	if leftOk && rightOk {
		if operatorType == parser.OperatorTypeEquals {
			return leftValue == rightValue, true
		} else if operatorType == parser.OperatorTypeNotEquals {
			return leftValue != rightValue, true
		}
	}

	return false, false
}

// isSysVersionInfoExpression corresponds to _isSysVersionInfoExpression. The
// TypeScript defaults sysImportAliases to ['sys']; pass nil for that.
func isSysVersionInfoExpression(node parser.ExpressionNode, sysImportAliases []string) bool {
	return isSysMemberExpression(node, "version_info", sysImportAliases)
}

// isSysPlatformInfoExpression corresponds to _isSysPlatformInfoExpression. The
// TypeScript defaults sysImportAliases to ['sys']; pass nil for that.
func isSysPlatformInfoExpression(node parser.ExpressionNode, sysImportAliases []string) bool {
	return isSysMemberExpression(node, "platform", sysImportAliases)
}

// isSysMemberExpression is the body the two functions above share verbatim in
// the original.
func isSysMemberExpression(node parser.ExpressionNode, member string, sysImportAliases []string) bool {
	if sysImportAliases == nil {
		sysImportAliases = []string{"sys"}
	}

	if memberAccess, ok := node.(*parser.MemberAccessNode); ok {
		if leftName, ok := memberAccess.D.LeftExpr.(*parser.NameNode); ok &&
			memberAccess.D.Member.D.Value == member {
			for _, alias := range sysImportAliases {
				if alias == leftName.D.Value {
					return true
				}
			}
		}
	}

	return false
}

func isOsNameInfoExpression(node parser.ExpressionNode) bool {
	if memberAccess, ok := node.(*parser.MemberAccessNode); ok {
		if leftName, ok := memberAccess.D.LeftExpr.(*parser.NameNode); ok &&
			leftName.D.Value == "os" &&
			memberAccess.D.Member.D.Value == "name" {
			return true
		}
	}

	return false
}

func getExpectedPlatformNameFromPlatform(execEnv *ExecutionEnvironment) (string, bool) {
	switch execEnv.PythonPlatform {
	case PythonPlatformDarwin:
		return "darwin", true
	case PythonPlatformWindows:
		return "win32", true
	case PythonPlatformLinux:
		return "linux", true
	case PythonPlatformIOS:
		return "ios", true
	case PythonPlatformAndroid:
		// Python >= 3.13 reports Android as 'android', earlier used to report
		// it as 'linux'.
		if execEnv.PythonVersion.Major == 3 && execEnv.PythonVersion.Minor >= 13 {
			return "android", true
		}
		return "linux", true
	}

	return "", false
}

func getExpectedOsNameFromPlatform(execEnv *ExecutionEnvironment) (string, bool) {
	switch execEnv.PythonPlatform {
	case PythonPlatformDarwin, PythonPlatformLinux, PythonPlatformIOS, PythonPlatformAndroid:
		return "posix", true
	case PythonPlatformWindows:
		return "nt", true
	}

	return "", false
}
