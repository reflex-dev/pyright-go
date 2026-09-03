/*
 * operations_validatebinary.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/operations.ts (pyright 1.1.412):
 * validateBinaryOperation, validateContainmentOperation, validateArithmeticOperation,
 * calcLiteralForBinaryOp, convertFunctionToObject.
 *
 * What `a OP b` produces, once both operand types are known. Three families,
 * each with different rules.
 *
 * BOOLEAN operators short-circuit, and the type reflects that. `a and b` where
 * `a` cannot be truthy is just `a` -- `b` never runs. Where `a` cannot be falsy
 * it is just `b`. Otherwise it is the union of the two, with the impossible
 * half of `a` removed: after `a and b` succeeds, whatever made `a` falsy is
 * gone.
 *
 * CONTAINMENT (`in`, `not in`) tries `__contains__` and falls back on iterating
 * the container and checking whether the left operand could be an element. The
 * result is `bool` regardless of what `__contains__` was declared to return,
 * since Python coerces it.
 *
 * ARITHMETIC tries up to SIX magic-method calls per subtype pair. `a + b` is
 * `a.__add__(b)`, and if that fails `b.__radd__(a)` -- but each is tried with
 * both the expanded and unexpanded forms of the operands, because a TypeVar's
 * bound may implement the operator when the TypeVar itself does not. Functions
 * are converted to `object` first: every Python function inherits object's
 * methods, and that is where `__eq__` and friends come from.
 *
 * Literal math runs before any of it when the caller allows it, so
 * `Literal[1] + Literal[2]` is `Literal[3]` rather than `int`. Division by zero,
 * an over-large power and any other arithmetic failure make the fold decline
 * rather than produce a wrong answer.
 */

package analyzer

import (
	"math/big"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// ValidateBinaryOperation corresponds to validateBinaryOperation.
func ValidateBinaryOperation(
	evaluator TypeEvaluator,
	operator parser.OperatorType,
	leftTypeResult *TypeResult,
	rightTypeResult *TypeResult,
	errorNode parser.ExpressionNode,
	inferenceContext *InferenceContext,
	diag *common.DiagnosticAddendum,
	options *BinaryOperationOptions,
) *TypeResult {
	leftType := leftTypeResult.Type
	rightType := rightTypeResult.Type
	isIncomplete := leftTypeResult.IsIncomplete || rightTypeResult.IsIncomplete
	var t Type
	concreteLeftType := evaluator.MakeTopLevelTypeVarsConcrete(leftType, false)
	var deprecatedInfo *MagicMethodDeprecationInfo

	if _, isBoolean := booleanOperatorMap[operator]; isBoolean {
		// The original's comment: if it's an AND or OR, we need to handle
		// short-circuiting by eliminating any known-truthy or known-falsy types.
		if shortCircuit, adjusted, done := shortCircuitBooleanOperand(
			evaluator, operator, leftType, rightType, concreteLeftType); done {
			return shortCircuit
		} else if adjusted != nil {
			concreteLeftType = adjusted
		}

		if IsNever(leftType) || IsNever(rightType) {
			return &TypeResult{Type: NeverTypeCreateNever()}
		}

		// The original's comment: the "in" and "not in" operators make use of the
		// __contains__ magic method.
		if operator == parser.OperatorTypeIn || operator == parser.OperatorTypeNotIn {
			result := validateContainmentOperation(evaluator, operator, leftTypeResult,
				concreteLeftType, rightTypeResult, errorNode, diag)

			if result.MagicMethodDeprecationInfo != nil {
				deprecatedInfo = result.MagicMethodDeprecationInfo
			}

			t = result.Type

			// The original's comment: assume that a bool is returned even if the type
			// is unknown.
			if t != nil && !IsNever(t) {
				t = evaluator.GetBuiltInObject(errorNode, "bool", nil)
			}
		} else {
			t = evaluator.MapSubtypesExpandTypeVars(concreteLeftType, nil,
				func(leftSubtypeExpanded Type, leftSubtypeUnexpanded Type) Type {
					return evaluator.MapSubtypesExpandTypeVars(rightType,
						&EvaluatorMapSubtypesOptions{
							ConditionFilter: refConditions(GetTypeCondition(leftSubtypeExpanded)),
						},
						func(_ Type, rightSubtypeUnexpanded Type) Type {
							// The original's comment: if the operator is an AND or OR, we
							// need to combine the two types.
							if operator == parser.OperatorTypeAnd || operator == parser.OperatorTypeOr {
								return CombineTypes([]Type{leftSubtypeUnexpanded, rightSubtypeUnexpanded}, nil)
							}
							// The original's comment: the other boolean operators always
							// return a bool value.
							return evaluator.GetBuiltInObject(errorNode, "bool", nil)
						})
				})
		}
	} else if _, isBinary := binaryOperatorMap[operator]; isBinary {
		if IsNever(leftType) || IsNever(rightType) {
			return &TypeResult{Type: NeverTypeCreateNever()}
		}

		// The original's comment: handle certain operations on certain homogenous
		// literal types using special-case math. For example, Literal[1, 2] +
		// Literal[3, 4] should result in Literal[4, 5, 6].
		if options != nil && options.IsLiteralMathAllowed {
			t = calcLiteralForBinaryOp(operator, leftType, rightType)
		}

		if t == nil {
			result := validateArithmeticOperation(evaluator, operator, leftTypeResult,
				rightTypeResult, errorNode, inferenceContext, diag, options)

			if result.MagicMethodDeprecationInfo != nil {
				deprecatedInfo = result.MagicMethodDeprecationInfo
			}

			t = result.Type
		}
	}

	if t == nil {
		t = UnknownTypeCreate(isIncomplete)
	}
	return &TypeResult{Type: t, MagicMethodDeprecationInfo: deprecatedInfo}
}

// shortCircuitBooleanOperand is the original's `and`/`or` block. It returns a
// finished result when the operand's truthiness decides the whole expression,
// and otherwise the narrowed left type.
func shortCircuitBooleanOperand(
	evaluator TypeEvaluator,
	operator parser.OperatorType,
	leftType, rightType, concreteLeftType Type,
) (*TypeResult, Type, bool) {
	switch operator {
	case parser.OperatorTypeAnd:
		// The original's comment: if the LHS evaluates to falsy, the And expression
		// will always return the type of the left-hand side.
		if !evaluator.CanBeTruthy(concreteLeftType) {
			return &TypeResult{Type: leftType}, nil, true
		}

		// The original's comment: if the LHS evaluates to truthy, the And expression
		// will always return the type of the right-hand side.
		if !evaluator.CanBeFalsy(concreteLeftType) {
			return &TypeResult{Type: rightType}, nil, true
		}

		narrowed := evaluator.RemoveTruthinessFromType(concreteLeftType)

		if IsNever(rightType) {
			return &TypeResult{Type: narrowed}, nil, true
		}
		return nil, narrowed, false

	case parser.OperatorTypeOr:
		// The original's comment: if the LHS evaluates to truthy, the Or expression
		// will always return the type of the left-hand side.
		if !evaluator.CanBeFalsy(concreteLeftType) {
			return &TypeResult{Type: leftType}, nil, true
		}

		// The original's comment: if the LHS evaluates to falsy, the Or expression
		// will always return the type of the right-hand side.
		if !evaluator.CanBeTruthy(concreteLeftType) {
			return &TypeResult{Type: rightType}, nil, true
		}

		narrowed := evaluator.RemoveFalsinessFromType(concreteLeftType)

		if IsNever(rightType) {
			return &TypeResult{Type: narrowed}, nil, true
		}
		return nil, narrowed, false
	}

	return nil, nil, false
}

// validateContainmentOperation corresponds to the function of the same name.
//
// Note the nesting order: the RIGHT operand is the outer loop here, because it
// is the container and owns the `__contains__` method.
func validateContainmentOperation(
	evaluator TypeEvaluator,
	operator parser.OperatorType,
	leftTypeResult *TypeResult,
	concreteLeftType Type,
	rightTypeResult *TypeResult,
	errorNode parser.ExpressionNode,
	diag *common.DiagnosticAddendum,
) *TypeResult {
	var deprecatedInfo *MagicMethodDeprecationInfo

	t := evaluator.MapSubtypesExpandTypeVars(rightTypeResult.Type, nil,
		func(rightSubtypeExpanded Type, rightSubtypeUnexpanded Type) Type {
			return evaluator.MapSubtypesExpandTypeVars(concreteLeftType,
				&EvaluatorMapSubtypesOptions{
					ConditionFilter: refConditions(GetTypeCondition(rightSubtypeExpanded)),
				},
				func(leftSubtype Type, _ Type) Type {
					if IsAnyOrUnknown(leftSubtype) || IsAnyOrUnknown(rightSubtypeUnexpanded) {
						return PreserveUnknown(leftSubtype, rightSubtypeExpanded)
					}

					returnTypeResult := evaluator.GetTypeOfMagicMethodCall(rightSubtypeExpanded,
						"__contains__",
						[]*TypeResult{{Type: leftSubtype, IsIncomplete: leftTypeResult.IsIncomplete}},
						errorNode, nil)

					if returnTypeResult == nil {
						// The original's comment: if __contains__ was not supported, fall
						// back on an iterable.
						iterator := evaluator.GetTypeOfIterator(
							&TypeResult{Type: rightSubtypeExpanded, IsIncomplete: rightTypeResult.IsIncomplete},
							false, errorNode, boolPtr(false))

						if iterator != nil && evaluator.AssignType(iterator.Type, leftSubtype,
							nil, nil, AssignTypeFlagsDefault, 0) {
							returnTypeResult = &TypeResult{
								Type: evaluator.GetBuiltInObject(errorNode, "bool", nil),
							}
						}
					}

					if returnTypeResult == nil {
						diag.AddMessage(localization.LocMessage.TypeNotSupportBinaryOperator().Format(
							evaluator.PrintType(leftSubtype, nil),
							evaluator.PrintType(rightSubtypeExpanded, nil),
							PrintOperator(operator)))
						return evaluator.GetBuiltInObject(errorNode, "bool", nil)
					}

					if returnTypeResult.MagicMethodDeprecationInfo != nil {
						deprecatedInfo = returnTypeResult.MagicMethodDeprecationInfo
					}

					if returnTypeResult.Type == nil {
						return evaluator.GetBuiltInObject(errorNode, "bool", nil)
					}
					return returnTypeResult.Type
				})
		})

	return &TypeResult{Type: t, MagicMethodDeprecationInfo: deprecatedInfo}
}

// validateArithmeticOperation corresponds to the function of the same name.
func validateArithmeticOperation(
	evaluator TypeEvaluator,
	operator parser.OperatorType,
	leftTypeResult *TypeResult,
	rightTypeResult *TypeResult,
	errorNode parser.ExpressionNode,
	inferenceContext *InferenceContext,
	diag *common.DiagnosticAddendum,
	options *BinaryOperationOptions,
) *TypeResult {
	var deprecatedInfo *MagicMethodDeprecationInfo
	isIncomplete := leftTypeResult.IsIncomplete || rightTypeResult.IsIncomplete

	t := evaluator.MapSubtypesExpandTypeVars(leftTypeResult.Type, nil,
		func(leftSubtypeExpanded Type, leftSubtypeUnexpanded Type) Type {
			return evaluator.MapSubtypesExpandTypeVars(rightTypeResult.Type,
				&EvaluatorMapSubtypesOptions{
					ConditionFilter: refConditions(GetTypeCondition(leftSubtypeExpanded)),
				},
				func(rightSubtypeExpanded Type, rightSubtypeUnexpanded Type) Type {
					if IsAnyOrUnknown(leftSubtypeUnexpanded) || IsAnyOrUnknown(rightSubtypeUnexpanded) {
						return PreserveUnknown(leftSubtypeUnexpanded, rightSubtypeUnexpanded)
					}

					if combined := combineTupleAdd(evaluator, operator, options,
						leftSubtypeExpanded, rightSubtypeExpanded); combined != nil {
						return combined
					}

					resultTypeResult := tryArithmeticMagicMethods(evaluator, operator,
						leftTypeResult, rightTypeResult, errorNode, inferenceContext,
						leftSubtypeExpanded, leftSubtypeUnexpanded,
						rightSubtypeExpanded, rightSubtypeUnexpanded)

					if resultTypeResult == nil {
						reportArithmeticFailure(evaluator, operator, errorNode, inferenceContext,
							diag, leftSubtypeExpanded, rightSubtypeExpanded)
						return UnknownTypeCreate(isIncomplete)
					}

					if resultTypeResult.MagicMethodDeprecationInfo != nil {
						deprecatedInfo = resultTypeResult.MagicMethodDeprecationInfo
					}

					if resultTypeResult.Type == nil {
						return UnknownTypeCreate(isIncomplete)
					}
					return resultTypeResult.Type
				})
		})

	return &TypeResult{Type: t, MagicMethodDeprecationInfo: deprecatedInfo}
}

// combineTupleAdd is the original's tuple special case.
//
// Its comment: if at least one of the tuples is of fixed size, we can combine
// them into a precise new type. If both are unbounded (or contain an unbounded
// element), we cannot combine them in this manner because tuples can contain at
// most one unbounded element.
func combineTupleAdd(
	evaluator TypeEvaluator,
	operator parser.OperatorType,
	options *BinaryOperationOptions,
	leftSubtypeExpanded, rightSubtypeExpanded Type,
) Type {
	if options == nil || !options.IsTupleAddAllowed || operator != parser.OperatorTypeAdd {
		return nil
	}

	leftClass, leftOk := leftSubtypeExpanded.(*ClassType)
	rightClass, rightOk := rightSubtypeExpanded.(*ClassType)
	if !leftOk || !rightOk ||
		!IsClassInstance(leftSubtypeExpanded) || !IsTupleClass(leftClass) ||
		leftClass.Priv.TupleTypeArgs == nil ||
		!IsClassInstance(rightSubtypeExpanded) || !IsTupleClass(rightClass) ||
		rightClass.Priv.TupleTypeArgs == nil {
		return nil
	}

	tupleClassType := evaluator.GetTupleClassType()
	if tupleClassType == nil || !IsInstantiableClass(tupleClassType) {
		return nil
	}

	if IsUnboundedTupleClass(leftClass) && IsUnboundedTupleClass(rightClass) {
		return nil
	}

	combined := append(append([]*TupleTypeArg{}, leftClass.Priv.TupleTypeArgs...),
		rightClass.Priv.TupleTypeArgs...)

	return ClassTypeCloneAsInstance(
		SpecializeTupleClass(tupleClassType, combined, true, false), true)
}

// tryArithmeticMagicMethods is the original's six-attempt cascade: the forward
// method against three combinations of expanded and unexpanded operands, then the
// reflected method against three more.
func tryArithmeticMagicMethods(
	evaluator TypeEvaluator,
	operator parser.OperatorType,
	leftTypeResult, rightTypeResult *TypeResult,
	errorNode parser.ExpressionNode,
	inferenceContext *InferenceContext,
	leftSubtypeExpanded, leftSubtypeUnexpanded Type,
	rightSubtypeExpanded, rightSubtypeUnexpanded Type,
) *TypeResult {
	names := binaryOperatorMap[operator]
	magicMethodName := names[0]
	altMagicMethodName := names[1]

	call := func(receiver Type, name string, arg Type, argIncomplete bool) *TypeResult {
		return evaluator.GetTypeOfMagicMethodCall(
			convertFunctionToObject(evaluator, receiver), name,
			[]*TypeResult{{Type: arg, IsIncomplete: argIncomplete}},
			errorNode, inferenceContext)
	}

	resultTypeResult := call(leftSubtypeUnexpanded, magicMethodName,
		rightSubtypeUnexpanded, rightTypeResult.IsIncomplete)

	if resultTypeResult == nil && leftSubtypeUnexpanded != leftSubtypeExpanded {
		// The original's comment: try the expanded left type.
		resultTypeResult = call(leftSubtypeExpanded, magicMethodName,
			rightSubtypeUnexpanded, rightTypeResult.IsIncomplete)
	}

	if resultTypeResult == nil && rightSubtypeUnexpanded != rightSubtypeExpanded {
		// The original's comment: try the expanded left and right type.
		resultTypeResult = call(leftSubtypeExpanded, magicMethodName,
			rightSubtypeExpanded, rightTypeResult.IsIncomplete)
	}

	if resultTypeResult != nil {
		return resultTypeResult
	}

	// The original's comment: try the alternate form (swapping right and left).
	resultTypeResult = call(rightSubtypeUnexpanded, altMagicMethodName,
		leftSubtypeUnexpanded, leftTypeResult.IsIncomplete)

	if resultTypeResult == nil && rightSubtypeUnexpanded != rightSubtypeExpanded {
		// The original's comment: try the expanded right type.
		resultTypeResult = call(rightSubtypeExpanded, altMagicMethodName,
			leftSubtypeUnexpanded, leftTypeResult.IsIncomplete)
	}

	if resultTypeResult == nil && leftSubtypeUnexpanded != leftSubtypeExpanded {
		// The original's comment: try the expanded right and left type.
		resultTypeResult = call(rightSubtypeExpanded, altMagicMethodName,
			leftSubtypeExpanded, leftTypeResult.IsIncomplete)
	}

	return resultTypeResult
}

// reportArithmeticFailure is the original's diagnostic, which names the expected
// type when there was one because the operator may have succeeded without it.
func reportArithmeticFailure(
	evaluator TypeEvaluator,
	operator parser.OperatorType,
	_ parser.ExpressionNode,
	inferenceContext *InferenceContext,
	diag *common.DiagnosticAddendum,
	leftSubtypeExpanded, rightSubtypeExpanded Type,
) {
	if inferenceContext != nil && !IsAnyOrUnknown(inferenceContext.ExpectedType) {
		diag.AddMessage(localization.LocMessage.TypeNotSupportBinaryOperatorBidirectional().Format(
			evaluator.PrintType(leftSubtypeExpanded, nil),
			evaluator.PrintType(rightSubtypeExpanded, nil),
			evaluator.PrintType(inferenceContext.ExpectedType, nil),
			PrintOperator(operator)))
		return
	}

	diag.AddMessage(localization.LocMessage.TypeNotSupportBinaryOperator().Format(
		evaluator.PrintType(leftSubtypeExpanded, nil),
		evaluator.PrintType(rightSubtypeExpanded, nil),
		PrintOperator(operator)))
}

// convertFunctionToObject corresponds to the function of the same name.
//
// Its comment: all functions in Python derive from object, so they inherit all
// of the capabilities of an object. This function converts a function to an
// object instance.
func convertFunctionToObject(evaluator TypeEvaluator, t Type) Type {
	if IsFunctionOrOverloaded(t) {
		return evaluator.GetObjectType()
	}
	return t
}

// binaryOperatorMap corresponds to the map of the same name: each operator's
// forward and reflected magic-method names. The comparison operators pair with
// their MIRROR (`<` with `>`) rather than with an `__r`-prefixed name, because
// that is what Python's reflected-comparison protocol calls.
var binaryOperatorMap = map[parser.OperatorType][2]string{
	parser.OperatorTypeAdd:                {"__add__", "__radd__"},
	parser.OperatorTypeSubtract:           {"__sub__", "__rsub__"},
	parser.OperatorTypeMultiply:           {"__mul__", "__rmul__"},
	parser.OperatorTypeFloorDivide:        {"__floordiv__", "__rfloordiv__"},
	parser.OperatorTypeDivide:             {"__truediv__", "__rtruediv__"},
	parser.OperatorTypeMod:                {"__mod__", "__rmod__"},
	parser.OperatorTypePower:              {"__pow__", "__rpow__"},
	parser.OperatorTypeMatrixMultiply:     {"__matmul__", "__rmatmul__"},
	parser.OperatorTypeBitwiseAnd:         {"__and__", "__rand__"},
	parser.OperatorTypeBitwiseOr:          {"__or__", "__ror__"},
	parser.OperatorTypeBitwiseXor:         {"__xor__", "__rxor__"},
	parser.OperatorTypeLeftShift:          {"__lshift__", "__rlshift__"},
	parser.OperatorTypeRightShift:         {"__rshift__", "__rrshift__"},
	parser.OperatorTypeEquals:             {"__eq__", "__eq__"},
	parser.OperatorTypeNotEquals:          {"__ne__", "__ne__"},
	parser.OperatorTypeLessThan:           {"__lt__", "__gt__"},
	parser.OperatorTypeLessThanOrEqual:    {"__le__", "__ge__"},
	parser.OperatorTypeGreaterThan:        {"__gt__", "__lt__"},
	parser.OperatorTypeGreaterThanOrEqual: {"__ge__", "__le__"},
}

// intLiteralMathOps is the original's `supportedOps` list.
var intLiteralMathOps = map[parser.OperatorType]bool{
	parser.OperatorTypeAdd:         true,
	parser.OperatorTypeSubtract:    true,
	parser.OperatorTypeMultiply:    true,
	parser.OperatorTypeFloorDivide: true,
	parser.OperatorTypeMod:         true,
	parser.OperatorTypePower:       true,
	parser.OperatorTypeLeftShift:   true,
	parser.OperatorTypeRightShift:  true,
	parser.OperatorTypeBitwiseAnd:  true,
	parser.OperatorTypeBitwiseOr:   true,
	parser.OperatorTypeBitwiseXor:  true,
}

// calcLiteralForBinaryOp corresponds to the function of the same name.
//
// Its comment: attempts to apply "literal math" for two literal operands. It
// returns nil where the original returns undefined.
func calcLiteralForBinaryOp(operator parser.OperatorType, leftType, rightType Type) Type {
	leftLiteralClassName := GetLiteralTypeClassName(leftType)
	if leftLiteralClassName == nil || GetTypeCondition(leftType) != nil ||
		SomeSubtypes(leftType, func(subtype Type) bool { return GetTypeCondition(subtype) != nil }) {
		return nil
	}

	rightLiteralClassName := GetLiteralTypeClassName(rightType)
	if rightLiteralClassName == nil || *leftLiteralClassName != *rightLiteralClassName ||
		GetTypeCondition(rightType) != nil ||
		SomeSubtypes(rightType, func(subtype Type) bool { return GetTypeCondition(subtype) != nil }) ||
		GetUnionSubtypeCount(leftType)*GetUnionSubtypeCount(rightType) >= maxLiteralMathSubtypeCount {
		return nil
	}

	// The original's comment: handle str and bytes literals.
	if *leftLiteralClassName == "str" || *leftLiteralClassName == "bytes" {
		if operator != parser.OperatorTypeAdd {
			return nil
		}
		return MapSubtypes(leftType, func(leftSubtype Type) Type {
			return MapSubtypes(rightType, func(rightSubtype Type) Type {
				leftClassSubtype := leftSubtype.(*ClassType)
				rightClassSubtype := rightSubtype.(*ClassType)
				leftLiteral, leftOk := leftClassSubtype.Priv.LiteralValue.(LiteralString)
				rightLiteral, rightOk := rightClassSubtype.Priv.LiteralValue.(LiteralString)
				if !leftOk || !rightOk {
					return leftSubtype
				}
				return ClassTypeCloneWithLiteral(leftClassSubtype, leftLiteral+rightLiteral)
			}, nil)
		}, nil)
	}

	// The original's comment: handle int literals.
	if *leftLiteralClassName != "int" || !intLiteralMathOps[operator] {
		return nil
	}

	isValidResult := true

	t := MapSubtypes(leftType, func(leftSubtype Type) Type {
		return MapSubtypes(rightType, func(rightSubtype Type) Type {
			leftClassSubtype := leftSubtype.(*ClassType)
			rightClassSubtype := rightSubtype.(*ClassType)

			leftLiteralValue := literalAsBigInt(leftClassSubtype.Priv.LiteralValue)
			rightLiteralValue := literalAsBigInt(rightClassSubtype.Priv.LiteralValue)
			if leftLiteralValue == nil || rightLiteralValue == nil {
				isValidResult = false
				return nil
			}

			newValue := applyIntLiteralOp(operator, leftLiteralValue, rightLiteralValue)
			if newValue == nil {
				isValidResult = false
				return nil
			}

			// The original's comment: convert back to a simple number if it fits.
			// Leave as a bigint if it doesn't.
			if newValue.IsInt64() {
				if n := newValue.Int64(); n >= minSafeInteger && n <= maxSafeInteger {
					return ClassTypeCloneWithLiteral(leftClassSubtype, LiteralFloat(float64(n)))
				}
			}

			return ClassTypeCloneWithLiteral(leftClassSubtype, LiteralInt{Value: newValue})
		}, nil)
	}, nil)

	if isValidResult {
		return t
	}
	return nil
}

// applyIntLiteralOp is the original's operator dispatch over two BigInts. It
// returns nil where the original leaves `newValue` undefined, which makes the
// whole fold decline.
//
// Two of the arms exist because Python's semantics differ from truncating
// integer division: floor divide rounds toward negative infinity rather than
// zero, and modulo takes the sign of the RIGHT operand. Go's big.Int has both
// behaviors available and the original has to construct them, so this uses the
// operations that match Python directly and says so.
func applyIntLiteralOp(operator parser.OperatorType, left, right *big.Int) *big.Int {
	zero := big.NewInt(0)

	switch operator {
	case parser.OperatorTypeAdd:
		return new(big.Int).Add(left, right)

	case parser.OperatorTypeSubtract:
		return new(big.Int).Sub(left, right)

	case parser.OperatorTypeMultiply:
		return new(big.Int).Mul(left, right)

	case parser.OperatorTypeFloorDivide:
		if right.Sign() == 0 {
			return nil
		}
		// big.Int's Div is Euclidean; the original adjusts a truncating BigInt
		// division to round toward negative infinity. Both agree with Python.
		quotient := new(big.Int)
		remainder := new(big.Int)
		quotient.QuoRem(left, right, remainder)
		if remainder.Sign() != 0 && (left.Sign() < 0) != (right.Sign() < 0) {
			quotient.Sub(quotient, big.NewInt(1))
		}
		return quotient

	case parser.OperatorTypeMod:
		if right.Sign() == 0 {
			return nil
		}
		// The original's comment: BigInt always produces a remainder, but Python
		// produces a modulo result whose sign is always the same as the right
		// operand.
		result := new(big.Int).Rem(left, right)
		result.Add(result, right)
		result.Rem(result, right)
		return result

	case parser.OperatorTypePower:
		if right.Sign() < 0 {
			return nil
		}
		if !right.IsInt64() || right.Int64() > maxLiteralPowerExponent {
			// The original catches the BigInt exponentiation throwing when the
			// result exceeds the maximum representable value.
			return nil
		}
		return new(big.Int).Exp(left, right, nil)

	case parser.OperatorTypeLeftShift:
		if right.Sign() < 0 || !right.IsUint64() || right.Uint64() > maxLiteralShiftCount {
			return nil
		}
		return new(big.Int).Lsh(left, uint(right.Uint64()))

	case parser.OperatorTypeRightShift:
		if right.Sign() < 0 || !right.IsUint64() {
			return nil
		}
		if right.Uint64() > maxLiteralShiftCount {
			// Shifting further than the value's bit length yields 0 or -1; the
			// cap avoids allocating for an answer that is already determined.
			if left.Sign() < 0 {
				return big.NewInt(-1)
			}
			return zero
		}
		return new(big.Int).Rsh(left, uint(right.Uint64()))

	case parser.OperatorTypeBitwiseAnd:
		return new(big.Int).And(left, right)

	case parser.OperatorTypeBitwiseOr:
		return new(big.Int).Or(left, right)

	case parser.OperatorTypeBitwiseXor:
		return new(big.Int).Xor(left, right)
	}

	return nil
}

const (
	// maxLiteralPowerExponent and maxLiteralShiftCount stand in for the
	// original's reliance on BigInt exponentiation throwing once the result
	// exceeds what the engine can represent. Go's big.Int has no such limit and
	// would happily allocate gigabytes, so the fold declines past these instead.
	maxLiteralPowerExponent = 1024
	maxLiteralShiftCount    = 1024
)
