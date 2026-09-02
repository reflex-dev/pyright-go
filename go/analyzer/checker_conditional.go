/*
 * checker_conditional.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateConditionalIsBool and _reportUnnecessaryConditionExpression.
 *
 * Both run on every `if`, `while`, ternary and comprehension guard, and they
 * catch opposite mistakes.
 *
 * _validateConditionalIsBool is about a type that cannot be used as a condition
 * at all. Python calls `__bool__` on the operand, and a class whose `__bool__`
 * returns something other than `bool` raises a TypeError at runtime. Note the
 * two early exits before the magic-method call: a literal `bool` is the common
 * case and short-circuits, and a `__bool__` that returns Any or Unknown is
 * accepted rather than assumed wrong.
 *
 * _reportUnnecessaryConditionExpression is about a condition that is always
 * true. A bare function name in a condition is almost always a forgotten call,
 * and so is a coroutine -- `if foo:` where the author meant `if foo():`, or
 * `if coro:` where they meant `if await coro:`. It recurses through `and`, `or`
 * and `not` so that each operand of a compound condition is judged separately,
 * and returns without checking the compound node itself, whose type is `bool`
 * and therefore uninteresting.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateConditionalIsBool corresponds to _validateConditionalIsBool.
func (c *Checker) validateConditionalIsBool(node parser.ExpressionNode) {
	operandType := c.evaluator.GetType(node)
	if operandType == nil {
		return
	}

	isTypeBool := true
	diag := common.NewDiagnosticAddendum()

	c.evaluator.MapSubtypesExpandTypeVars(operandType, nil,
		func(expandedSubtype Type, _ Type) Type {
			if IsAnyOrUnknown(expandedSubtype) {
				return nil
			}

			// The original's comment: if it's a bool (the common case), we're good.
			if IsClassInstance(expandedSubtype) &&
				ClassTypeIsBuiltInNamed(expandedSubtype.(*ClassType), "bool") {
				return nil
			}

			// The original's comment: invoke the __bool__ method on the type.
			var boolReturnType Type
			if result := c.evaluator.GetTypeOfMagicMethodCall(
				expandedSubtype, "__bool__", nil, node, nil); result != nil {
				boolReturnType = result.Type
			}

			if boolReturnType == nil || IsAnyOrUnknown(boolReturnType) {
				return nil
			}

			if IsClassInstance(boolReturnType) &&
				ClassTypeIsBuiltInNamed(boolReturnType.(*ClassType), "bool") {
				return nil
			}

			// The original's comment: all other types are problematic.
			isTypeBool = false

			diag.AddMessage(localization.LocAddendum.ConditionalRequiresBool().Format(
				c.evaluator.PrintType(expandedSubtype, nil),
				c.evaluator.PrintType(boolReturnType, nil)))

			return nil
		})

	if !isTypeBool {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.ConditionalOperandInvalid().
				Format(c.evaluator.PrintType(operandType, nil))+diag.GetString(),
			node, nil)
	}
}

// reportUnnecessaryConditionExpression corresponds to
// _reportUnnecessaryConditionExpression.
func (c *Checker) reportUnnecessaryConditionExpression(expression parser.ExpressionNode) {
	if binary, ok := expression.(*parser.BinaryOperationNode); ok {
		if binary.D.Operator == parser.OperatorTypeAnd || binary.D.Operator == parser.OperatorTypeOr {
			c.reportUnnecessaryConditionExpression(binary.D.LeftExpr)
			c.reportUnnecessaryConditionExpression(binary.D.RightExpr)
		}
		return
	}

	if unary, ok := expression.(*parser.UnaryOperationNode); ok {
		if unary.D.Operator == parser.OperatorTypeNot {
			c.reportUnnecessaryConditionExpression(unary.D.Expr)
		}
		return
	}

	exprTypeResult := c.evaluator.GetTypeOfExpression(expression, EvalFlagsNone, nil)

	// Both flags start true and are cleared by any subtype that is not of that
	// shape, so an empty union leaves both set -- which is the original's
	// behavior, since Never is not reached here.
	isExprFunction := true
	isCoroutine := true

	DoForEachSubtype(exprTypeResult.Type, func(subtype Type, _ int, _ []Type) {
		subtype = c.evaluator.MakeTopLevelTypeVarsConcrete(subtype, false)

		if !IsFunctionOrOverloaded(subtype) {
			isExprFunction = false
		}

		if !IsClassInstance(subtype) ||
			!ClassTypeIsBuiltInNamed(subtype.(*ClassType), "Coroutine", "CoroutineType") {
			isCoroutine = false
		}
	})

	if isExprFunction {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportUnnecessaryComparison,
			localization.LocMessage.FunctionInConditionalExpression(), expression, nil)
	}

	if isCoroutine {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportUnnecessaryComparison,
			localization.LocMessage.CoroutineInConditionalExpression(), expression, nil)
	}
}
