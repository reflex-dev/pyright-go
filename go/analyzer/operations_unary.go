/*
 * operations_unary.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/operations.ts (pyright 1.1.412):
 * getTypeOfUnaryOperation, calcLiteralForUnaryOp.
 *
 * `-x`, `+x`, `~x` and `not x`. Each maps to a magic method, and the ordinary
 * path is to call it -- but literal operands get folded first, so `-1` is
 * `Literal[-1]` rather than `int`.
 *
 * The folding is refused in three situations, each for a reason worth naming.
 * An INCOMPLETE operand may be inside a loop whose literal value changes on each
 * pass, so folding it would freeze a value that is still moving. A CONDITIONED
 * operand carries a constraint that the folded literal would silently drop. And
 * a union of more than 64 literals is folded subtype by subtype, which is where
 * the cost stops being worth it.
 *
 * `not` is the odd one out twice over: it is the only operator that does not
 * report on an Optional operand, and its result is always `bool` regardless of
 * what `__bool__` was declared to return.
 *
 * Bitwise invert is computed as `-(x + 1)` in arbitrary precision rather than
 * with a machine `~`. The original's comment explains why -- JavaScript's `~`
 * truncates to 32 bits -- and Go has the same hazard in a different form, since
 * Python integers are unbounded and the port carries them as big.Int.
 */

package analyzer

import (
	"math/big"

	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// maxLiteralMathSubtypeCount corresponds to the constant of the same name: the
// point past which folding a union of literals costs more than it is worth.
const maxLiteralMathSubtypeCount = 64

// unaryOperatorMap corresponds to the map of the same name.
//
// The original's comment: map unary operators to magic functions. Note that the
// bitwise invert has two magic functions that are aliases of each other.
var unaryOperatorMap = map[parser.OperatorType]string{
	parser.OperatorTypeAdd:           "__pos__",
	parser.OperatorTypeSubtract:      "__neg__",
	parser.OperatorTypeBitwiseInvert: "__invert__",
	parser.OperatorTypeNot:           "__bool__",
}

// GetTypeOfUnaryOperation corresponds to getTypeOfUnaryOperation.
func GetTypeOfUnaryOperation(
	evaluator TypeEvaluator,
	node *parser.UnaryOperationNode,
	flags EvalFlags,
	inferenceContext *InferenceContext,
) *TypeResult {
	if (flags & EvalFlagsTypeExpression) != 0 {
		evaluator.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.UnaryOperationNotAllowed(), node, nil)
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	exprTypeResult := evaluator.GetTypeOfExpression(node.D.Expr, EvalFlagsNone, nil)
	exprType := evaluator.MakeTopLevelTypeVarsConcrete(
		TransformPossibleRecursiveTypeAlias(exprTypeResult.Type, 0), false)

	isIncomplete := exprTypeResult.IsIncomplete

	if IsNever(exprType) {
		return &TypeResult{Type: NeverTypeCreateNever(), IsIncomplete: isIncomplete}
	}

	var t Type
	var deprecatedInfo *MagicMethodDeprecationInfo

	if node.D.Operator != parser.OperatorTypeNot && IsOptionalType(exprType) {
		evaluator.AddDiagnostic(DiagnosticRuleReportOptionalOperand,
			localization.LocMessage.NoneOperator().Format(PrintOperator(node.D.Operator)),
			node.D.Expr, nil)
		exprType = RemoveNoneFromUnion(exprType)
	}

	// The original's comment: handle certain operations on certain literal types
	// using special-case math. Do not apply this if the input type is incomplete
	// because we may be evaluating an expression within a loop, so the literal value
	// may change each time.
	if !exprTypeResult.IsIncomplete {
		t = calcLiteralForUnaryOp(node.D.Operator, exprType)
	}

	if t != nil {
		return &TypeResult{Type: t, IsIncomplete: isIncomplete, MagicMethodDeprecationInfo: deprecatedInfo}
	}

	if IsAnyOrUnknown(exprType) {
		t = exprType
	} else {
		magicMethodName := unaryOperatorMap[node.D.Operator]
		isResultValid := true

		t = evaluator.MapSubtypesExpandTypeVars(exprType, nil,
			func(subtypeExpanded Type, _ Type) Type {
				typeResult := evaluator.GetTypeOfMagicMethodCall(subtypeExpanded, magicMethodName,
					[]*TypeResult{}, node, inferenceContext)

				if typeResult == nil {
					isResultValid = false
					return nil
				}

				if typeResult.MagicMethodDeprecationInfo != nil {
					deprecatedInfo = typeResult.MagicMethodDeprecationInfo
				}

				return typeResult.Type
			})

		if !isResultValid {
			t = nil
		}
	}

	// The original's comment: __not__ always returns a boolean.
	if node.D.Operator == parser.OperatorTypeNot {
		t = evaluator.GetBuiltInObject(node, "bool", nil)
		if t == nil {
			t = UnknownTypeCreate(false)
		}
	}

	if t == nil {
		if !isIncomplete {
			reportUnaryOperationFailure(evaluator, node, exprType, inferenceContext)
		}
		t = UnknownTypeCreate(isIncomplete)
	}

	return &TypeResult{Type: t, IsIncomplete: isIncomplete, MagicMethodDeprecationInfo: deprecatedInfo}
}

// reportUnaryOperationFailure is the original's diagnostic tail, which uses a
// different message when an expected type was supplied because the operator may
// have succeeded without one.
func reportUnaryOperationFailure(
	evaluator TypeEvaluator,
	node *parser.UnaryOperationNode,
	exprType Type,
	inferenceContext *InferenceContext,
) {
	if inferenceContext != nil && !IsAnyOrUnknown(inferenceContext.ExpectedType) {
		evaluator.AddDiagnostic(DiagnosticRuleReportOperatorIssue,
			localization.LocMessage.TypeNotSupportUnaryOperatorBidirectional().Format(
				PrintOperator(node.D.Operator),
				evaluator.PrintType(exprType, nil),
				evaluator.PrintType(inferenceContext.ExpectedType, nil)),
			node, nil)
		return
	}

	evaluator.AddDiagnostic(DiagnosticRuleReportOperatorIssue,
		localization.LocMessage.TypeNotSupportUnaryOperator().Format(
			PrintOperator(node.D.Operator), evaluator.PrintType(exprType, nil)),
		node, nil)
}

// calcLiteralForUnaryOp corresponds to the function of the same name. It returns
// nil where the original returns undefined, meaning "don't fold".
func calcLiteralForUnaryOp(operator parser.OperatorType, operandType Type) Type {
	if GetUnionSubtypeCount(operandType) >= maxLiteralMathSubtypeCount {
		return nil
	}

	if GetTypeCondition(operandType) != nil ||
		SomeSubtypes(operandType, func(subtype Type) bool { return GetTypeCondition(subtype) != nil }) {
		return nil
	}

	literalClassName := GetLiteralTypeClassName(operandType)
	if literalClassName == nil {
		return nil
	}

	switch *literalClassName {
	case "int":
		switch operator {
		case parser.OperatorTypeAdd:
			return operandType

		case parser.OperatorTypeSubtract:
			return MapSubtypes(operandType, func(subtype Type) Type {
				classSubtype := subtype.(*ClassType)
				// The original writes `-(literalValue as number | bigint)`, and
				// JavaScript negation preserves which of the two arms the value was
				// in. The port carries the same split: a Python integer small enough
				// for a JS number is a LiteralFloat, and only a large one is a
				// LiteralInt.
				switch literal := classSubtype.Priv.LiteralValue.(type) {
				case LiteralInt:
					return ClassTypeCloneWithLiteral(classSubtype,
						LiteralInt{Value: new(big.Int).Neg(literal.Value)})
				case LiteralFloat:
					return ClassTypeCloneWithLiteral(classSubtype, LiteralFloat(-float64(literal)))
				}
				return subtype
			}, nil)

		case parser.OperatorTypeBitwiseInvert:
			// The original's comment: Python defines bitwise invert (~x) as
			// -(x + 1). Use BigInt math to avoid JavaScript's 32-bit truncation when
			// using the ~ operator on Number values. Go has the same hazard in a
			// different form, since Python integers are unbounded.
			return MapSubtypes(operandType, func(subtype Type) Type {
				classSubtype := subtype.(*ClassType)

				bigVal := literalAsBigInt(classSubtype.Priv.LiteralValue)
				if bigVal == nil {
					return subtype
				}

				newValue := new(big.Int).Add(bigVal, big.NewInt(1))
				newValue.Neg(newValue)

				// The original narrows the result back to a JS number when it fits,
				// which is the LiteralFloat arm here.
				if newValue.IsInt64() {
					if n := newValue.Int64(); n >= minSafeInteger && n <= maxSafeInteger {
						return ClassTypeCloneWithLiteral(classSubtype, LiteralFloat(float64(n)))
					}
				}

				return ClassTypeCloneWithLiteral(classSubtype, LiteralInt{Value: newValue})
			}, nil)
		}

	case "bool":
		if operator == parser.OperatorTypeNot {
			return MapSubtypes(operandType, func(subtype Type) Type {
				classSubtype := subtype.(*ClassType)
				literal, ok := classSubtype.Priv.LiteralValue.(LiteralBool)
				if !ok {
					return subtype
				}
				return ClassTypeCloneWithLiteral(classSubtype, LiteralBool(!bool(literal)))
			}, nil)
		}
	}

	return nil
}

// minSafeInteger and maxSafeInteger correspond to JavaScript's
// Number.MIN_SAFE_INTEGER and Number.MAX_SAFE_INTEGER, which decide whether the
// original stores an integer literal in the `number` arm or the `bigint` arm.
const (
	minSafeInteger = -(1<<53 - 1)
	maxSafeInteger = 1<<53 - 1
)

// literalAsBigInt reads an integer literal from either arm of the union. A
// Python integer small enough for a JavaScript number is carried as
// LiteralFloat, exactly as in the original; only a large one is a LiteralInt.
// It returns nil for any other literal kind.
func literalAsBigInt(value LiteralValue) *big.Int {
	switch literal := value.(type) {
	case LiteralInt:
		return literal.Value
	case LiteralFloat:
		result, _ := big.NewFloat(float64(literal)).Int(nil)
		return result
	}
	return nil
}
