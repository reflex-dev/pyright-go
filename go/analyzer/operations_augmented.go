/*
 * operations_augmented.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/operations.ts (pyright 1.1.412):
 * getTypeOfAugmentedAssignment, isExpressionLocalVariable.
 *
 * `a += b`. Python tries the IN-PLACE method first -- `a.__iadd__(b)` -- and
 * falls back on the ordinary `a + b` when the type does not define one, which is
 * why a list grows in place but a tuple is rebuilt.
 *
 * The in-place attempt is made up to three times per subtype pair, against the
 * unexpanded and expanded forms of each operand, for the same reason the binary
 * operators do it: a TypeVar's bound may define `__iadd__` when the TypeVar does
 * not.
 *
 * Two guards on the fallback are worth naming. LITERAL MATH is disabled inside a
 * loop, because `x += 1` in a loop has a different literal value on each pass and
 * folding it would freeze the first one; it is also disabled when the target is
 * not a local variable, since anything else may be observed elsewhere. And the
 * tuple `__add__` special case is disabled when the left operand is a union,
 * because rebuilding a tuple type inside a loop can grow it without bound.
 *
 * `|=` passes the left operand as the expected type for the right, which is what
 * lets a TypedDict be updated from a dict literal.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// augmentedOperatorMap corresponds to the operatorMap in
// getTypeOfAugmentedAssignment: the in-place magic method, and the binary
// operator to fall back on.
var augmentedOperatorMap = map[parser.OperatorType]struct {
	MethodName     string
	BinaryOperator parser.OperatorType
}{
	parser.OperatorTypeAddEqual:            {"__iadd__", parser.OperatorTypeAdd},
	parser.OperatorTypeSubtractEqual:       {"__isub__", parser.OperatorTypeSubtract},
	parser.OperatorTypeMultiplyEqual:       {"__imul__", parser.OperatorTypeMultiply},
	parser.OperatorTypeFloorDivideEqual:    {"__ifloordiv__", parser.OperatorTypeFloorDivide},
	parser.OperatorTypeDivideEqual:         {"__itruediv__", parser.OperatorTypeDivide},
	parser.OperatorTypeModEqual:            {"__imod__", parser.OperatorTypeMod},
	parser.OperatorTypePowerEqual:          {"__ipow__", parser.OperatorTypePower},
	parser.OperatorTypeMatrixMultiplyEqual: {"__imatmul__", parser.OperatorTypeMatrixMultiply},
	parser.OperatorTypeBitwiseAndEqual:     {"__iand__", parser.OperatorTypeBitwiseAnd},
	parser.OperatorTypeBitwiseOrEqual:      {"__ior__", parser.OperatorTypeBitwiseOr},
	parser.OperatorTypeBitwiseXorEqual:     {"__ixor__", parser.OperatorTypeBitwiseXor},
	parser.OperatorTypeLeftShiftEqual:      {"__ilshift__", parser.OperatorTypeLeftShift},
	parser.OperatorTypeRightShiftEqual:     {"__irshift__", parser.OperatorTypeRightShift},
}

// GetTypeOfAugmentedAssignment corresponds to getTypeOfAugmentedAssignment.
func GetTypeOfAugmentedAssignment(
	evaluator TypeEvaluator,
	node *parser.AugmentedAssignmentNode,
	inferenceContext *InferenceContext,
) *TypeResult {
	var t Type
	var typeResult *TypeResult
	diag := common.NewDiagnosticAddendum()
	var deprecatedInfo *MagicMethodDeprecationInfo

	leftTypeResult := evaluator.GetTypeOfExpression(node.D.LeftExpr, EvalFlagsNone, nil)
	leftType := leftTypeResult.Type

	var expectedOperandType Type
	if node.D.Operator == parser.OperatorTypeBitwiseOrEqual {
		// The original's comment: if this is a bitwise or ("|="), use the type of the
		// left operand. This allows us to support the case where a TypedDict is being
		// updated with a dict expression.
		expectedOperandType = leftType
	}

	rightTypeResult := evaluator.GetTypeOfExpression(node.D.RightExpr, EvalFlagsNone,
		MakeInferenceContext(expectedOperandType, false, nil))
	rightType := rightTypeResult.Type
	isIncomplete := rightTypeResult.IsIncomplete || leftTypeResult.IsIncomplete

	if IsNever(leftType) || IsNever(rightType) {
		typeResult = &TypeResult{Type: NeverTypeCreateNever(), IsIncomplete: isIncomplete}
	} else {
		t = evaluator.MapSubtypesExpandTypeVars(leftType, nil,
			func(leftSubtypeExpanded Type, leftSubtypeUnexpanded Type) Type {
				return evaluator.MapSubtypesExpandTypeVars(rightType,
					&EvaluatorMapSubtypesOptions{
						ConditionFilter: refConditions(GetTypeCondition(leftSubtypeExpanded)),
					},
					func(rightSubtypeExpanded Type, rightSubtypeUnexpanded Type) Type {
						if IsAnyOrUnknown(leftSubtypeUnexpanded) || IsAnyOrUnknown(rightSubtypeUnexpanded) {
							return PreserveUnknown(leftSubtypeUnexpanded, rightSubtypeUnexpanded)
						}

						returnTypeResult := tryInPlaceOperator(evaluator, node, inferenceContext,
							rightTypeResult, leftSubtypeExpanded, leftSubtypeUnexpanded,
							rightSubtypeExpanded, rightSubtypeUnexpanded)

						if returnTypeResult == nil {
							// The original's comment: if the LHS class didn't support the
							// magic method for augmented assignment, fall back on the normal
							// binary expression evaluator.
							returnTypeResult = fallBackToBinaryOperator(evaluator, node,
								inferenceContext, diag, leftTypeResult, rightTypeResult,
								leftType, rightType, leftSubtypeUnexpanded, rightSubtypeUnexpanded)
						}

						if returnTypeResult != nil && returnTypeResult.MagicMethodDeprecationInfo != nil {
							deprecatedInfo = returnTypeResult.MagicMethodDeprecationInfo
						}

						if returnTypeResult == nil {
							return nil
						}
						return returnTypeResult.Type
					})
			})

		// The original's comment: if the LHS class didn't support the magic method
		// for augmented assignment, fall back on the normal binary expression
		// evaluator.
		if !diag.IsEmpty() || t == nil || IsNever(t) {
			if !isIncomplete {
				evaluator.AddDiagnostic(DiagnosticRuleReportOperatorIssue,
					localization.LocMessage.TypeNotSupportBinaryOperator().Format(
						PrintOperator(node.D.Operator),
						evaluator.PrintType(leftType, nil),
						evaluator.PrintType(rightType, nil))+diag.GetString(),
					node, nil)
			}
		}

		typeResult = &TypeResult{
			Type:                       t,
			IsIncomplete:               isIncomplete,
			MagicMethodDeprecationInfo: deprecatedInfo,
		}
	}

	evaluator.AssignTypeToExpression(node.D.DestExpr, typeResult, node.D.RightExpr)

	return typeResult
}

// tryInPlaceOperator is the original's three attempts at the `__i*__` method.
func tryInPlaceOperator(
	evaluator TypeEvaluator,
	node *parser.AugmentedAssignmentNode,
	inferenceContext *InferenceContext,
	rightTypeResult *TypeResult,
	leftSubtypeExpanded, leftSubtypeUnexpanded Type,
	rightSubtypeExpanded, rightSubtypeUnexpanded Type,
) *TypeResult {
	magicMethodName := augmentedOperatorMap[node.D.Operator].MethodName

	returnTypeResult := evaluator.GetTypeOfMagicMethodCall(leftSubtypeUnexpanded, magicMethodName,
		[]*TypeResult{{Type: rightSubtypeUnexpanded, IsIncomplete: rightTypeResult.IsIncomplete}},
		node, inferenceContext)

	if returnTypeResult == nil && leftSubtypeUnexpanded != leftSubtypeExpanded {
		// The original's comment: try with the expanded left type.
		returnTypeResult = evaluator.GetTypeOfMagicMethodCall(leftSubtypeExpanded, magicMethodName,
			[]*TypeResult{{Type: rightSubtypeUnexpanded, IsIncomplete: rightTypeResult.IsIncomplete}},
			node, inferenceContext)
	}

	if returnTypeResult == nil && rightSubtypeUnexpanded != rightSubtypeExpanded {
		// The original's comment: try with the expanded left and right type.
		returnTypeResult = evaluator.GetTypeOfMagicMethodCall(leftSubtypeExpanded, magicMethodName,
			[]*TypeResult{{Type: rightSubtypeExpanded, IsIncomplete: rightTypeResult.IsIncomplete}},
			node, inferenceContext)
	}

	return returnTypeResult
}

// fallBackToBinaryOperator is the original's fallback, with the two guards that
// keep it from misbehaving inside a loop.
func fallBackToBinaryOperator(
	evaluator TypeEvaluator,
	node *parser.AugmentedAssignmentNode,
	inferenceContext *InferenceContext,
	diag *common.DiagnosticAddendum,
	leftTypeResult, rightTypeResult *TypeResult,
	leftType, rightType Type,
	leftSubtypeUnexpanded, rightSubtypeUnexpanded Type,
) *TypeResult {
	binaryOperator := augmentedOperatorMap[node.D.Operator].BinaryOperator

	// The original's comment: don't use literal math if the operation is within a
	// loop because the literal values may change each time.
	isLiteralMathAllowed := !IsWithinLoop(node) &&
		isExpressionLocalVariable(evaluator, node.D.LeftExpr) &&
		GetUnionSubtypeCount(leftType)*GetUnionSubtypeCount(rightType) < maxLiteralMathSubtypeCount

	// The original's comment: don't special-case tuple __add__ if the left type is
	// a union. This can result in an infinite loop if we keep creating new tuple
	// types within a loop construct using __add__.
	isTupleAddAllowed := !IsUnion(leftType)

	return ValidateBinaryOperation(evaluator, binaryOperator,
		&TypeResult{Type: leftSubtypeUnexpanded, IsIncomplete: leftTypeResult.IsIncomplete},
		&TypeResult{Type: rightSubtypeUnexpanded, IsIncomplete: rightTypeResult.IsIncomplete},
		node, inferenceContext, diag,
		&BinaryOperationOptions{
			IsLiteralMathAllowed: isLiteralMathAllowed,
			IsTupleAddAllowed:    isTupleAddAllowed,
		})
}

// isExpressionLocalVariable corresponds to the function of the same name.
//
// Its comment: determines whether the expression refers to a variable that is
// defined within the current scope or some outer scope.
func isExpressionLocalVariable(evaluator TypeEvaluator, node parser.ExpressionNode) bool {
	nameNode, ok := node.(*parser.NameNode)
	if !ok {
		return false
	}

	symbolWithScope := evaluator.LookUpSymbolRecursive(nameNode, nameNode.D.Value, false)
	if symbolWithScope == nil {
		return false
	}

	currentScope := GetScopeForNode(nameNode)
	return currentScope == symbolWithScope.Scope
}
