/*
 * typeguards_dispatch.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeGuards.ts (pyright 1.1.412):
 * getTypeNarrowingCallback, getTypeNarrowingCallbackForAliasedCondition,
 * getDeclsForLocalVar, getTypeNarrowingCallbackForAssignmentExpression,
 * isNameSameScope, isMatchingExpressionOrWalrusRhs.
 *
 * This is the pattern matcher. The code flow engine hands it a reference
 * expression (the thing whose type we want to narrow) and a test expression
 * (the condition that was just proven true or false), and it answers with a
 * callback that performs the narrowing -- or nil, meaning "this condition tells
 * us nothing about that reference".
 *
 * The shape of the original is a long sequence of `if` blocks, each recognizing
 * one syntactic form (`x is None`, `isinstance(x, C)`, `x[0] == 3`, ...) and
 * returning a closure the moment it matches. The order matters: earlier, more
 * specific forms shadow later, more general ones, and the final fallthrough is
 * plain truthiness narrowing on the reference itself. The transliteration keeps
 * the sequence and the early returns exactly as written.
 *
 * Every arm evaluates the operand types eagerly, outside the returned closure,
 * and captures them. That is deliberate in the original: the closure may run
 * many times as the flow engine walks join points, and re-evaluating the test
 * expression each time would be both slow and (because evaluation has cache
 * side effects) not idempotent.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// getTypeNarrowingCallback corresponds to the function of the same name.
//
// Returning nil is the original's `undefined`: no narrowing applies, so the
// caller keeps the unnarrowed type.
func getTypeNarrowingCallback(
	evaluator TypeEvaluator,
	reference parser.ExpressionNode,
	testExpression parser.ExpressionNode,
	isPositiveTest bool,
	recursionCount int,
) func(Type) *TypeResult {
	if recursionCount > MaxTypeRecursionCount {
		return nil
	}

	recursionCount++

	if assignExpr, ok := testExpression.(*parser.AssignmentExpressionNode); ok {
		return getTypeNarrowingCallbackForAssignmentExpression(
			evaluator, reference, assignExpr, isPositiveTest, recursionCount)
	}

	if binaryOp, ok := testExpression.(*parser.BinaryOperationNode); ok {
		if cb := narrowingCallbackForBinaryOp(
			evaluator, reference, binaryOp, isPositiveTest); cb != nil {
			return cb
		}
	}

	if callNode, ok := testExpression.(*parser.CallNode); ok {
		if cb := narrowingCallbackForCall(evaluator, reference, callNode, isPositiveTest); cb != nil {
			return cb
		}
	}

	if IsMatchingExpression(reference, testExpression, func(ref, expr *parser.NameNode) bool {
		return isNameSameScope(evaluator, ref, expr)
	}) {
		return func(t Type) *TypeResult {
			return &TypeResult{Type: narrowTypeForTruthiness(evaluator, t, isPositiveTest)}
		}
	}

	// The original's comment: is this a reference to an aliased conditional
	// expression (a local variable that was assigned a value that can inform type
	// narrowing of the reference expression)?
	narrowingCallback := getTypeNarrowingCallbackForAliasedCondition(
		evaluator, reference, testExpression, isPositiveTest, recursionCount)
	if narrowingCallback != nil {
		return narrowingCallback
	}

	// The original's comment: we normally won't find a "not" operator here because
	// they are stripped out by the binder when it creates condition flow nodes, but
	// we can find this in the case of local variables type narrowing.
	if _, ok := reference.(*parser.NameNode); ok {
		if unaryOp, ok := testExpression.(*parser.UnaryOperationNode); ok {
			if unaryOp.D.Operator == parser.OperatorTypeNot {
				return getTypeNarrowingCallback(
					evaluator, reference, unaryOp.D.Expr, !isPositiveTest, recursionCount)
			}
		}
	}

	return nil
}

// narrowingCallbackForBinaryOp is the body of the original's
// `testExpression.nodeType === ParseNodeType.BinaryOperation` block, lifted into
// its own function so that each arm can `return` the way the original does
// without needing a labelled break out of the enclosing switch.
func narrowingCallbackForBinaryOp(
	evaluator TypeEvaluator,
	reference parser.ExpressionNode,
	testExpression *parser.BinaryOperationNode,
	isPositiveTest bool,
) func(Type) *TypeResult {
	op := testExpression.D.Operator

	isOrIsNotOperator := op == parser.OperatorTypeIs || op == parser.OperatorTypeIsNot
	equalsOrNotEqualsOperator := op == parser.OperatorTypeEquals || op == parser.OperatorTypeNotEquals
	comparisonOperator := equalsOrNotEqualsOperator ||
		op == parser.OperatorTypeLessThan ||
		op == parser.OperatorTypeLessThanOrEqual ||
		op == parser.OperatorTypeGreaterThan ||
		op == parser.OperatorTypeGreaterThanOrEqual

	sameScope := func(ref, expr *parser.NameNode) bool {
		return isNameSameScope(evaluator, ref, expr)
	}

	if isOrIsNotOperator || equalsOrNotEqualsOperator {
		// The original's comment: invert the "isPositiveTest" value if this is an
		// "is not" operation.
		adjIsPositiveTest := isPositiveTest
		if op != parser.OperatorTypeIs && op != parser.OperatorTypeEquals {
			adjIsPositiveTest = !isPositiveTest
		}

		// The original's comment: look for "X is None", "X is not None", "X == None",
		// and "X != None". These are commonly-used patterns used in control flow.
		if constNode, ok := testExpression.D.RightExpr.(*parser.ConstantNode); ok &&
			constNode.D.ConstType == parser.KeywordTypeNone {
			// The original's comment: allow the LHS to be a simple expression or an
			// assignment expression. For assignment expressions, narrow both the target
			// and the RHS (consistent with truthiness narrowing).
			if isMatchingExpressionOrWalrusRhs(evaluator, reference, testExpression.D.LeftExpr) {
				return func(t Type) *TypeResult {
					return &TypeResult{Type: narrowTypeForIsNone(evaluator, t, adjIsPositiveTest)}
				}
			}

			if leftExpression, ok := testExpression.D.LeftExpr.(*parser.IndexNode); ok &&
				IsMatchingExpression(reference, leftExpression.D.LeftExpr, sameScope) &&
				len(leftExpression.D.Items) == 1 &&
				!leftExpression.D.TrailingComma &&
				leftExpression.D.Items[0].D.ArgCategory == parser.ArgCategorySimple &&
				leftExpression.D.Items[0].D.Name == nil {
				if numberNode, ok := leftExpression.D.Items[0].D.ValueExpr.(*parser.NumberNode); ok &&
					numberNode.D.IsInteger && !numberNode.D.IsImaginary {
					// The original guards on `typeof indexValue === 'number'`, which is the
					// non-bigint arm of the parser's `number | bigint` value union.
					if !numberNode.D.Value.IsBigInt {
						indexValue := int(numberNode.D.Value.Float)
						return func(t Type) *TypeResult {
							return &TypeResult{
								Type: narrowTupleTypeForIsNone(evaluator, t, adjIsPositiveTest, indexValue),
							}
						}
					}
				}
			}
		}

		// The original's comment: look for "X is ...", "X is not ...", "X == ...", and
		// "X != ...".
		if _, ok := testExpression.D.RightExpr.(*parser.EllipsisNode); ok {
			if isMatchingExpressionOrWalrusRhs(evaluator, reference, testExpression.D.LeftExpr) {
				return func(t Type) *TypeResult {
					return &TypeResult{
						Type: narrowTypeForIsEllipsis(evaluator, testExpression, t, adjIsPositiveTest),
					}
				}
			}
		}

		// The original's comment: look for "type(X) is Y", "type(X) is not Y",
		// "type(X) == Y" or "type(X) != Y".
		if leftCall, ok := testExpression.D.LeftExpr.(*parser.CallNode); ok {
			if len(leftCall.D.Args) == 1 &&
				leftCall.D.Args[0].D.ArgCategory == parser.ArgCategorySimple {
				arg0Expr := leftCall.D.Args[0].D.ValueExpr
				if isMatchingExpressionOrWalrusRhs(evaluator, reference, arg0Expr) {
					callType := evaluator.GetTypeOfExpression(
						leftCall.D.LeftExpr, EvalFlagsCallBaseDefaults, nil).Type

					if IsInstantiableClass(callType) &&
						ClassTypeIsBuiltInNamed(callType.(*ClassType), "type") {
						rhsResult := evaluator.GetTypeOfExpression(testExpression.D.RightExpr, EvalFlagsNone, nil)
						classTypes := []*ClassType{}
						isClassType := true

						evaluator.MapSubtypesExpandTypeVars(rhsResult.Type, nil,
							func(expandedSubtype Type, _ Type) Type {
								if IsInstantiableClass(expandedSubtype) {
									classTypes = append(classTypes, expandedSubtype.(*ClassType))
								} else {
									isClassType = false
								}
								return nil
							})

						if isClassType && len(classTypes) > 0 {
							return func(t Type) *TypeResult {
								return &TypeResult{
									Type:         narrowTypeForTypeIs(evaluator, t, classTypes, adjIsPositiveTest),
									IsIncomplete: rhsResult.IsIncomplete,
								}
							}
						}
					}
				}
			}
		}

		if isOrIsNotOperator {
			if isMatchingExpressionOrWalrusRhs(evaluator, reference, testExpression.D.LeftExpr) {
				rightTypeResult := evaluator.GetTypeOfExpression(testExpression.D.RightExpr, EvalFlagsNone, nil)
				rightType := rightTypeResult.Type

				// The original's comment: look for "X is Y" or "X is not Y" where Y is a
				// literal.
				if IsClassInstance(rightType) && rightType.(*ClassType).Priv.LiteralValue != nil {
					rightClass := rightType.(*ClassType)
					return func(t Type) *TypeResult {
						return &TypeResult{
							Type: narrowTypeForLiteralComparison(
								evaluator, t, rightClass, adjIsPositiveTest, true),
							IsIncomplete: rightTypeResult.IsIncomplete,
						}
					}
				}

				// The original's comment: look for X is <class> or X is not <class>.
				if IsInstantiableClass(rightType) {
					rightClass := rightType.(*ClassType)
					return func(t Type) *TypeResult {
						return &TypeResult{
							Type: narrowTypeForClassComparison(
								evaluator, t, rightClass, adjIsPositiveTest),
							IsIncomplete: rightTypeResult.IsIncomplete,
						}
					}
				}
			}

			// The original's comment: look for X[<literal>] is <literal> or
			// X[<literal>] is not <literal>.
			if leftIndex, ok := testExpression.D.LeftExpr.(*parser.IndexNode); ok &&
				len(leftIndex.D.Items) == 1 &&
				!leftIndex.D.TrailingComma &&
				leftIndex.D.Items[0].D.ArgCategory == parser.ArgCategorySimple &&
				IsMatchingExpression(reference, leftIndex.D.LeftExpr, sameScope) {
				indexTypeResult := evaluator.GetTypeOfExpression(
					leftIndex.D.Items[0].D.ValueExpr, EvalFlagsNone, nil)
				indexType := indexTypeResult.Type

				if IsClassInstance(indexType) && IsLiteralType(indexType.(*ClassType)) {
					indexClass := indexType.(*ClassType)

					if ClassTypeIsBuiltInNamed(indexClass, "str") {
						rightType := evaluator.GetTypeOfExpression(
							testExpression.D.RightExpr, EvalFlagsNone, nil).Type
						if IsClassInstance(rightType) && rightType.(*ClassType).Priv.LiteralValue != nil {
							return func(t Type) *TypeResult {
								return &TypeResult{
									Type: narrowTypeForDiscriminatedDictEntryComparison(
										evaluator, t, indexClass, rightType, adjIsPositiveTest),
									IsIncomplete: indexTypeResult.IsIncomplete,
								}
							}
						}
					} else if ClassTypeIsBuiltInNamed(indexClass, "int") {
						rightTypeResult := evaluator.GetTypeOfExpression(
							testExpression.D.RightExpr, EvalFlagsNone, nil)
						rightType := rightTypeResult.Type

						if IsClassInstance(rightType) && rightType.(*ClassType).Priv.LiteralValue != nil {
							canNarrow := false
							// The original's comment: narrowing can be applied only for bool or
							// enum literals.
							if ClassTypeIsBuiltInNamed(rightType.(*ClassType), "bool") {
								canNarrow = true
							} else if _, isEnum := rightType.(*ClassType).Priv.LiteralValue.(*EnumLiteral); isEnum {
								canNarrow = true
							}

							if canNarrow {
								return func(t Type) *TypeResult {
									return &TypeResult{
										Type: narrowTypeForDiscriminatedTupleComparison(
											evaluator, t, indexClass, rightType, adjIsPositiveTest),
										IsIncomplete: rightTypeResult.IsIncomplete,
									}
								}
							}
						}
					}
				}
			}
		}

		if equalsOrNotEqualsOperator {
			// The original's comment: look for X == <literal> or X != <literal>.
			//
			// This shadows the outer adjIsPositiveTest with an identically-computed
			// one; the original declares it again here and so does the port.
			adjIsPositiveTest := isPositiveTest
			if op != parser.OperatorTypeEquals {
				adjIsPositiveTest = !isPositiveTest
			}

			if isMatchingExpressionOrWalrusRhs(evaluator, reference, testExpression.D.LeftExpr) {
				rightTypeResult := evaluator.GetTypeOfExpression(testExpression.D.RightExpr, EvalFlagsNone, nil)
				rightType := rightTypeResult.Type

				if IsClassInstance(rightType) && rightType.(*ClassType).Priv.LiteralValue != nil {
					rightClass := rightType.(*ClassType)
					return func(t Type) *TypeResult {
						return &TypeResult{
							Type: narrowTypeForLiteralComparison(
								evaluator, t, rightClass, adjIsPositiveTest, false),
							IsIncomplete: rightTypeResult.IsIncomplete,
						}
					}
				}
			}

			// The original's comment: look for X[<literal>] == <literal> or
			// X[<literal>] != <literal>.
			if leftIndex, ok := testExpression.D.LeftExpr.(*parser.IndexNode); ok &&
				len(leftIndex.D.Items) == 1 &&
				!leftIndex.D.TrailingComma &&
				leftIndex.D.Items[0].D.ArgCategory == parser.ArgCategorySimple &&
				IsMatchingExpression(reference, leftIndex.D.LeftExpr, sameScope) {
				indexTypeResult := evaluator.GetTypeOfExpression(
					leftIndex.D.Items[0].D.ValueExpr, EvalFlagsNone, nil)
				indexType := indexTypeResult.Type

				if IsClassInstance(indexType) && IsLiteralType(indexType.(*ClassType)) {
					indexClass := indexType.(*ClassType)

					if ClassTypeIsBuiltInNamed(indexClass, "str", "int") {
						rightTypeResult := evaluator.GetTypeOfExpression(
							testExpression.D.RightExpr, EvalFlagsNone, nil)
						rightType := rightTypeResult.Type

						if IsLiteralTypeOrUnion(rightType, false) {
							return func(t Type) *TypeResult {
								var narrowedType Type

								if ClassTypeIsBuiltInNamed(indexClass, "str") {
									narrowedType = narrowTypeForDiscriminatedDictEntryComparison(
										evaluator, t, indexClass, rightType, adjIsPositiveTest)
								} else {
									narrowedType = narrowTypeForDiscriminatedTupleComparison(
										evaluator, t, indexClass, rightType, adjIsPositiveTest)
								}

								return &TypeResult{
									Type:         narrowedType,
									IsIncomplete: indexTypeResult.IsIncomplete || rightTypeResult.IsIncomplete,
								}
							}
						}
					}
				}
			}
		}

		// The original's comment: look for X.Y == <literal> or X.Y != <literal>.
		if equalsOrNotEqualsOperator {
			if leftMember, ok := testExpression.D.LeftExpr.(*parser.MemberAccessNode); ok &&
				IsMatchingExpression(reference, leftMember.D.LeftExpr, sameScope) {
				rightTypeResult := evaluator.GetTypeOfExpression(testExpression.D.RightExpr, EvalFlagsNone, nil)
				rightType := rightTypeResult.Type
				memberName := leftMember.D.Member

				if IsClassInstance(rightType) {
					rightClass := rightType.(*ClassType)
					if rightClass.Priv.LiteralValue != nil || IsNoneInstance(rightType) {
						return func(t Type) *TypeResult {
							return &TypeResult{
								Type: narrowTypeForDiscriminatedLiteralFieldComparison(
									evaluator, t, memberName.D.Value, rightClass, adjIsPositiveTest),
								IsIncomplete: rightTypeResult.IsIncomplete,
							}
						}
					}
				}
			}
		}

		// The original's comment: look for X.Y is <literal> or X.Y is not <literal>
		// where <literal> is an enum or bool literal.
		if leftMember, ok := testExpression.D.LeftExpr.(*parser.MemberAccessNode); ok &&
			IsMatchingExpression(reference, leftMember.D.LeftExpr, sameScope) {
			rightTypeResult := evaluator.GetTypeOfExpression(testExpression.D.RightExpr, EvalFlagsNone, nil)
			rightType := rightTypeResult.Type
			memberName := leftMember.D.Member

			if IsClassInstance(rightType) {
				rightClass := rightType.(*ClassType)
				if (ClassTypeIsEnumClass(rightClass) || ClassTypeIsBuiltInNamed(rightClass, "bool")) &&
					rightClass.Priv.LiteralValue != nil {
					return func(t Type) *TypeResult {
						return &TypeResult{
							Type: narrowTypeForDiscriminatedLiteralFieldComparison(
								evaluator, t, memberName.D.Value, rightClass, adjIsPositiveTest),
							IsIncomplete: rightTypeResult.IsIncomplete,
						}
					}
				}
			}
		}

		// The original's comment: look for X.Y is None or X.Y is not None. These are
		// commonly-used patterns used in control flow.
		if leftMember, ok := testExpression.D.LeftExpr.(*parser.MemberAccessNode); ok &&
			IsMatchingExpression(reference, leftMember.D.LeftExpr, sameScope) {
			if constNode, ok := testExpression.D.RightExpr.(*parser.ConstantNode); ok &&
				constNode.D.ConstType == parser.KeywordTypeNone {
				memberName := leftMember.D.Member
				return func(t Type) *TypeResult {
					return &TypeResult{
						Type: narrowTypeForDiscriminatedFieldNoneComparison(
							evaluator, t, memberName.D.Value, adjIsPositiveTest),
					}
				}
			}
		}
	}

	// The original's comment: look for len(x) == <literal>, len(x) != <literal>,
	// len(x) < <literal>, etc.
	if comparisonOperator {
		if leftCall, ok := testExpression.D.LeftExpr.(*parser.CallNode); ok && len(leftCall.D.Args) == 1 {
			arg0Expr := leftCall.D.Args[0].D.ValueExpr

			if IsMatchingExpression(reference, arg0Expr, sameScope) {
				callTypeResult := evaluator.GetTypeOfExpression(
					leftCall.D.LeftExpr, EvalFlagsCallBaseDefaults, nil)
				callType := callTypeResult.Type

				if IsFunction(callType) && callType.(*FunctionType).Shared.FullName == "builtins.len" {
					rightTypeResult := evaluator.GetTypeOfExpression(
						testExpression.D.RightExpr, EvalFlagsNone, nil)
					rightType := rightTypeResult.Type

					// `typeof literalValue === 'number' && literalValue >= 0` -- the
					// non-bigint arm of the literal union, non-negative.
					if IsClassInstance(rightType) {
						if literal, ok := rightType.(*ClassType).Priv.LiteralValue.(LiteralFloat); ok &&
							float64(literal) >= 0 {
							tupleLength := int(literal)

							// The original's comment: we'll treat <, <= and == as positive tests
							// with >=, > and != as their negative counterparts.
							isLessOrEqual := op == parser.OperatorTypeEquals ||
								op == parser.OperatorTypeLessThan ||
								op == parser.OperatorTypeLessThanOrEqual

							adjIsPositiveTest := isPositiveTest
							if !isLessOrEqual {
								adjIsPositiveTest = !isPositiveTest
							}

							// The original's comment: for <= (or its negative counterpart >),
							// adjust the tuple length by 1.
							if op == parser.OperatorTypeLessThanOrEqual ||
								op == parser.OperatorTypeGreaterThan {
								tupleLength++
							}

							isEqualityCheck := op == parser.OperatorTypeEquals ||
								op == parser.OperatorTypeNotEquals

							return func(t Type) *TypeResult {
								return &TypeResult{
									Type: narrowTypeForTupleLength(
										evaluator, t, tupleLength, adjIsPositiveTest, !isEqualityCheck),
									IsIncomplete: callTypeResult.IsIncomplete || rightTypeResult.IsIncomplete,
								}
							}
						}
					}
				}
			}
		}
	}

	if op == parser.OperatorTypeIn || op == parser.OperatorTypeNotIn {
		// The original's comment: look for "x in y" or "x not in y" where y is one of
		// several built-in types.
		if isMatchingExpressionOrWalrusRhs(evaluator, reference, testExpression.D.LeftExpr) {
			rightTypeResult := evaluator.GetTypeOfExpression(testExpression.D.RightExpr, EvalFlagsNone, nil)
			rightType := rightTypeResult.Type
			adjIsPositiveTest := isPositiveTest
			if op != parser.OperatorTypeIn {
				adjIsPositiveTest = !isPositiveTest
			}

			return func(t Type) *TypeResult {
				return &TypeResult{
					Type:         narrowTypeForContainerType(evaluator, t, rightType, adjIsPositiveTest),
					IsIncomplete: rightTypeResult.IsIncomplete,
				}
			}
		}

		if IsMatchingExpression(reference, testExpression.D.RightExpr, sameScope) {
			// The original's comment: look for <string literal> in y where y is a union
			// that contains one or more TypedDicts.
			leftTypeResult := evaluator.GetTypeOfExpression(testExpression.D.LeftExpr, EvalFlagsNone, nil)
			leftType := leftTypeResult.Type

			if IsClassInstance(leftType) &&
				ClassTypeIsBuiltInNamed(leftType.(*ClassType), "str") &&
				IsLiteralType(leftType.(*ClassType)) {
				leftClass := leftType.(*ClassType)
				adjIsPositiveTest := isPositiveTest
				if op != parser.OperatorTypeIn {
					adjIsPositiveTest = !isPositiveTest
				}

				return func(t Type) *TypeResult {
					return &TypeResult{
						Type: narrowTypeForTypedDictKey(
							evaluator, t, ClassTypeCloneAsInstantiable(leftClass, true), adjIsPositiveTest),
						IsIncomplete: leftTypeResult.IsIncomplete,
					}
				}
			}
		}
	}

	return nil
}

// narrowingCallbackForCall is the body of the original's
// `testExpression.nodeType === ParseNodeType.Call` block.
func narrowingCallbackForCall(
	evaluator TypeEvaluator,
	reference parser.ExpressionNode,
	testExpression *parser.CallNode,
	isPositiveTest bool,
) func(Type) *TypeResult {
	// The original's comment: look for "isinstance(X, Y)" or "issubclass(X, Y)".
	if len(testExpression.D.Args) == 2 {
		// The original's comment: make sure the first parameter is a supported
		// expression type and the second parameter is a valid class type or a tuple
		// of valid class types.
		arg0Expr := testExpression.D.Args[0].D.ValueExpr
		arg1Expr := testExpression.D.Args[1].D.ValueExpr

		if isMatchingExpressionOrWalrusRhs(evaluator, reference, arg0Expr) {
			callTypeResult := evaluator.GetTypeOfExpression(
				testExpression.D.LeftExpr, EvalFlagsCallBaseDefaults, nil)
			callType := callTypeResult.Type

			if IsFunction(callType) &&
				FunctionTypeIsBuiltIn(callType.(*FunctionType), "isinstance", "issubclass") {
				isInstanceCheck := FunctionTypeIsBuiltIn(callType.(*FunctionType), "isinstance")
				arg1TypeResult := evaluator.GetTypeOfExpression(
					arg1Expr, EvalFlagsIsInstanceArgDefaults, nil)
				arg1Type := arg1TypeResult.Type

				classTypeList, found := GetIsInstanceClassTypes(evaluator, arg1Type)
				isIncomplete := callTypeResult.IsIncomplete || arg1TypeResult.IsIncomplete

				if found {
					return func(t Type) *TypeResult {
						return &TypeResult{
							Type: NarrowTypeForInstanceOrSubclass(
								evaluator, t, classTypeList, isInstanceCheck,
								false, isPositiveTest, testExpression),
							IsIncomplete: isIncomplete,
						}
					}
				} else if isIncomplete {
					// The original's comment: if the type is incomplete, it may include
					// unknowns, which will result in classTypeList being undefined.
					return func(t Type) *TypeResult {
						return &TypeResult{Type: t, IsIncomplete: true}
					}
				}
			}
		}
	}

	// The original's comment: look for "bool(X)".
	if len(testExpression.D.Args) == 1 && testExpression.D.Args[0].D.Name == nil {
		if isMatchingExpressionOrWalrusRhs(evaluator, reference, testExpression.D.Args[0].D.ValueExpr) {
			callTypeResult := evaluator.GetTypeOfExpression(
				testExpression.D.LeftExpr, EvalFlagsCallBaseDefaults, nil)
			callType := callTypeResult.Type

			if IsInstantiableClass(callType) && ClassTypeIsBuiltInNamed(callType.(*ClassType), "bool") {
				return func(t Type) *TypeResult {
					return &TypeResult{
						Type:         narrowTypeForTruthiness(evaluator, t, isPositiveTest),
						IsIncomplete: callTypeResult.IsIncomplete,
					}
				}
			}
		}
	}

	// The original's comment: look for a TypeGuard function.
	if len(testExpression.D.Args) >= 1 {
		arg0Expr := testExpression.D.Args[0].D.ValueExpr
		if isMatchingExpressionOrWalrusRhs(evaluator, reference, arg0Expr) {
			// The original's comment: does this look like it's a custom type guard
			// function?
			isPossiblyTypeGuard := false

			isFunctionReturnTypeGuard := func(t *FunctionType) bool {
				declared := t.Shared.DeclaredReturnType
				return declared != nil && IsClassInstance(declared) &&
					ClassTypeIsBuiltInNamed(declared.(*ClassType), "TypeGuard", "TypeIs")
			}

			callTypeResult := evaluator.GetTypeOfExpression(
				testExpression.D.LeftExpr, EvalFlagsCallBaseDefaults, nil)
			callType := callTypeResult.Type

			if IsFunction(callType) && isFunctionReturnTypeGuard(callType.(*FunctionType)) {
				isPossiblyTypeGuard = true
			} else if IsOverloaded(callType) {
				for _, o := range OverloadedTypeGetOverloads(callType.(*OverloadedType)) {
					if isFunctionReturnTypeGuard(o) {
						isPossiblyTypeGuard = true
						break
					}
				}
			} else if IsClassInstance(callType) {
				isPossiblyTypeGuard = true
			}

			if isPossiblyTypeGuard {
				// The original's comment: evaluate the type guard call expression.
				functionReturnTypeResult := evaluator.GetTypeOfExpression(testExpression, EvalFlagsNone, nil)
				functionReturnType := functionReturnTypeResult.Type

				if IsClassInstance(functionReturnType) {
					returnClass := functionReturnType.(*ClassType)
					if ClassTypeIsBuiltInNamed(returnClass, "TypeGuard", "TypeIs") &&
						len(returnClass.Priv.TypeArgs) > 0 {
						isStrictTypeGuard := ClassTypeIsBuiltInNamed(returnClass, "TypeIs")
						typeGuardType := returnClass.Priv.TypeArgs[0]
						isIncomplete := callTypeResult.IsIncomplete || functionReturnTypeResult.IsIncomplete

						return func(t Type) *TypeResult {
							return &TypeResult{
								Type: narrowTypeForUserDefinedTypeGuard(
									evaluator, t, typeGuardType, isPositiveTest,
									isStrictTypeGuard, testExpression),
								IsIncomplete: isIncomplete,
							}
						}
					}
				}
			}
		}
	}

	return nil
}

// getTypeNarrowingCallbackForAliasedCondition corresponds to the function of the
// same name.
//
// This handles the `is_none = val is None` / `if is_none:` shape: the test
// expression is a plain name whose only assignment was a narrowing condition, so
// the narrowing that condition would have performed is applied here instead.
func getTypeNarrowingCallbackForAliasedCondition(
	evaluator TypeEvaluator,
	reference parser.ExpressionNode,
	testExpression parser.ExpressionNode,
	isPositiveTest bool,
	recursionCount int,
) func(Type) *TypeResult {
	testName, testIsName := testExpression.(*parser.NameNode)
	referenceName, refIsName := reference.(*parser.NameNode)

	if !testIsName || !refIsName || parser.ParseNode(testExpression) == parser.ParseNode(reference) {
		return nil
	}

	// The original's comment: make sure the reference expression is a constant
	// parameter or variable. If the reference expression is modified within the
	// scope multiple times, we need to validate that it is not modified between the
	// test expression evaluation and the conditional check.
	testExprDecl := getDeclsForLocalVar(evaluator, testName, testName, true)
	if len(testExprDecl) != 1 || testExprDecl[0].DeclBase().Type != DeclarationTypeVariable {
		return nil
	}

	referenceDecls := getDeclsForLocalVar(evaluator, referenceName, testName, false)
	if referenceDecls == nil {
		return nil
	}

	modifyingDecls := []Declaration{}
	if len(referenceDecls) > 1 {
		// The original's comment: if there is more than one assignment to the
		// reference variable within the local scope, make sure that none of these
		// assignments are done after the test expression but before the condition
		// check.
		//
		// This is OK:
		//  val = None
		//  is_none = val is None
		//  if is_none: ...
		//
		// This is not OK:
		//  val = None
		//  is_none = val is None
		//  val = 1
		//  if is_none: ...
		for _, decl := range referenceDecls {
			if evaluator.IsNodeReachable(testExpression, decl.DeclBase().Node) &&
				evaluator.IsNodeReachable(decl.DeclBase().Node, testExprDecl[0].DeclBase().Node) {
				modifyingDecls = append(modifyingDecls, decl)
			}
		}
	}

	if len(modifyingDecls) != 0 {
		return nil
	}

	varDecl, ok := testExprDecl[0].(*VariableDeclaration)
	if !ok {
		return nil
	}
	initNode := varDecl.InferredTypeSource

	if initNode == nil || IsNodeContainedWithin(testExpression, initNode) || !parser.IsExpressionNode(initNode) {
		return nil
	}

	return getTypeNarrowingCallback(
		evaluator, reference, initNode.(parser.ExpressionNode), isPositiveTest, recursionCount)
}

// getDeclsForLocalVar corresponds to the function of the same name.
//
// The original's comment: determines whether the symbol is a local variable or
// parameter within the current scope. If requireUnique is true, there can be
// only one declaration (assignment) of the symbol, otherwise it is rejected.
func getDeclsForLocalVar(
	evaluator TypeEvaluator,
	name *parser.NameNode,
	reachableFrom parser.ParseNode,
	requireUnique bool,
) []Declaration {
	scope := GetScopeForNode(name)
	if scope == nil || (scope.Type != ScopeTypeFunction && scope.Type != ScopeTypeModule) {
		return nil
	}

	symbol := scope.LookUpSymbol(name.D.Value)
	if symbol == nil {
		return nil
	}

	decls := symbol.GetDeclarations()
	if requireUnique && len(decls) > 1 {
		return nil
	}

	if len(decls) == 0 {
		return nil
	}
	for _, decl := range decls {
		declType := decl.DeclBase().Type
		if declType != DeclarationTypeVariable && declType != DeclarationTypeParam {
			return nil
		}
	}

	// The original's comment: if there are any assignments within different scopes
	// (e.g. via a "global" or "nonlocal" reference), don't consider it a local
	// variable.
	var prevDeclScope parser.ExecutionScopeNode
	havePrevScope := false
	for _, decl := range decls {
		nodeToConsider := decl.DeclBase().Node
		if decl.DeclBase().Type == DeclarationTypeParam {
			// The original reads `decl.node.d.name!` -- a parameter declaration's node
			// is a ParameterNode and the name is asserted non-null.
			if paramNode, ok := nodeToConsider.(*parser.ParameterNode); ok && paramNode.D.Name != nil {
				nodeToConsider = paramNode.D.Name
			}
		}

		declScopeNode := GetExecutionScopeNode(nodeToConsider)
		if havePrevScope && declScopeNode != prevDeclScope {
			return nil
		}
		prevDeclScope = declScopeNode
		havePrevScope = true
	}

	reachableDecls := []Declaration{}
	for _, decl := range decls {
		if evaluator.IsNodeReachable(reachableFrom, decl.DeclBase().Node) {
			reachableDecls = append(reachableDecls, decl)
		}
	}

	if len(reachableDecls) > 0 {
		return reachableDecls
	}
	return nil
}

// getTypeNarrowingCallbackForAssignmentExpression corresponds to the function of
// the same name.
func getTypeNarrowingCallbackForAssignmentExpression(
	evaluator TypeEvaluator,
	reference parser.ExpressionNode,
	testExpression *parser.AssignmentExpressionNode,
	isPositiveTest bool,
	recursionCount int,
) func(Type) *TypeResult {
	if cb := getTypeNarrowingCallback(
		evaluator, reference, testExpression.D.RightExpr, isPositiveTest, recursionCount); cb != nil {
		return cb
	}
	return getTypeNarrowingCallback(
		evaluator, reference, testExpression.D.Name, isPositiveTest, recursionCount)
}

// isNameSameScope corresponds to the function of the same name.
//
// The original's comment: determines whether the expression name node is in the
// same scope or an outer scope from the reference name node. This allows
// isMatchingExpression to determine whether two name nodes are referring to the
// same symbol.
func isNameSameScope(evaluator TypeEvaluator, reference *parser.NameNode, expression *parser.NameNode) bool {
	refSymbol := evaluator.LookUpSymbolRecursive(reference, reference.D.Value, false)
	exprSymbol := evaluator.LookUpSymbolRecursive(expression, expression.D.Value, false)

	if refSymbol == nil || exprSymbol == nil {
		// The original's comment: this shouldn't happen, but just to be safe...
		return true
	}

	refScope := refSymbol.Scope
	exprScope := exprSymbol.Scope

	if refScope == exprScope {
		return true
	}

	return IsScopeContainedWithin(refScope, exprScope)
}

// isMatchingExpressionOrWalrusRhs corresponds to the function of the same name.
//
// The original's comment: matches a reference against an expression, including
// the RHS of an assignment expression. This keeps walrus narrowing consistent
// with truthiness handling in getTypeNarrowingCallbackForAssignmentExpression.
func isMatchingExpressionOrWalrusRhs(
	evaluator TypeEvaluator,
	reference parser.ExpressionNode,
	expression parser.ExpressionNode,
) bool {
	compareName := func(ref, expr *parser.NameNode) bool {
		return isNameSameScope(evaluator, ref, expr)
	}

	if IsMatchingExpression(reference, expression, compareName) {
		return true
	}

	if assignExpr, ok := expression.(*parser.AssignmentExpressionNode); ok {
		return IsMatchingExpression(reference, assignExpr.D.RightExpr, compareName)
	}

	return false
}
