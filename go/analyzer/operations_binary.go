/*
 * operations_binary.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/operations.ts (pyright 1.1.412):
 * getTypeOfBinaryOperation, customMetaclassSupportsMethod and booleanOperatorMap.
 *
 * `a + b` -- but most of this function is not about addition. It is about the
 * three things a binary operator can mean besides calling a magic method.
 *
 * Chained comparisons. `a < b < c` parses as `a < (b < c)`, which is not what it
 * means. The right side is evaluated for its own errors and then REPLACED by its
 * own left operand, so the comparison actually performed is `a < b`.
 *
 * `X | Y` as a union. In a type context the bitwise-or operator builds a union
 * rather than calling __or__, and the decision is made by asking whether either
 * operand has a custom metaclass that defines __or__ or __ror__ -- because if it
 * does, the user meant the operator. `int | None` is handled specially: None is
 * normally the singleton value, but here it is converted to its type.
 *
 * Expected-type propagation, which differs by operator. Most operators apply the
 * expected type to the RESULT of the magic method, so it does not reach the
 * operands at all. `and` and `or` have no magic method, so it applies to both.
 * Three heuristics add to that, each named in the original for the pattern it
 * serves: `x or []` and `my_list + [0]` take the left operand's type, and `|` on
 * a TypedDict does too so a dict display on the right is checked against it.
 *
 * Literal math is suppressed inside a loop or a lambda. `x = x + 1` in a loop
 * would otherwise produce a new literal type on every pass and never converge,
 * and a lambda is usually a callback whose value differs per call.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// booleanOperatorMap corresponds to the map of the same name: the operators
// whose result is a bool regardless of operand types, and which therefore skip
// the Optional checks.
var booleanOperatorMap = map[parser.OperatorType]bool{
	parser.OperatorTypeAnd:   true,
	parser.OperatorTypeOr:    true,
	parser.OperatorTypeIs:    true,
	parser.OperatorTypeIsNot: true,
	parser.OperatorTypeIn:    true,
	parser.OperatorTypeNotIn: true,
}

// GetTypeOfBinaryOperation corresponds to the function of the same name.
func GetTypeOfBinaryOperation(
	evaluator TypeEvaluator,
	node *parser.BinaryOperationNode,
	flags EvalFlags,
	inferenceContext *InferenceContext,
) *TypeResult {
	leftExpression := node.D.LeftExpr
	rightExpression := node.D.RightExpr
	isIncomplete := false
	typeErrors := false

	// The original's comment: if this is a comparison and the left expression is
	// also a comparison, we need to change the behavior to accommodate python's
	// "chained comparisons" feature.
	if OperatorSupportsChaining(node.D.Operator) {
		if rightBinary, ok := rightExpression.(*parser.BinaryOperationNode); ok &&
			!rightBinary.D.HasParens && OperatorSupportsChaining(rightBinary.D.Operator) {
			// The original's comment: evaluate the right expression so it is type
			// checked.
			GetTypeOfBinaryOperation(evaluator, rightBinary, flags, inferenceContext)

			// The original's comment: use the left side of the right expression for
			// comparison purposes.
			rightExpression = rightBinary.D.LeftExpr
		}
	}

	expectedOperandType, expectedLeftOperandType := binaryExpectedOperandTypes(node, inferenceContext)

	effectiveExpectedType := expectedOperandType
	if effectiveExpectedType == nil {
		effectiveExpectedType = expectedLeftOperandType
	}

	leftTypeResult := evaluator.GetTypeOfExpression(leftExpression, flags,
		MakeInferenceContext(effectiveExpectedType, false, nil))
	leftType := leftTypeResult.Type

	if expectedOperandType == nil {
		expectedOperandType = leftOperandTypeHeuristic(node, leftType)
	}

	rightTypeResult := evaluator.GetTypeOfExpression(rightExpression, flags,
		MakeInferenceContext(expectedOperandType, false, nil))
	rightType := rightTypeResult.Type

	if leftTypeResult.IsIncomplete || rightTypeResult.IsIncomplete {
		isIncomplete = true
	}

	// The original's comment: is this a "|" operator used in a context where it is
	// supposed to be interpreted as a union operator?
	if node.D.Operator == parser.OperatorTypeBitwiseOr &&
		!customMetaclassSupportsMethod(leftType, "__or__") &&
		!customMetaclassSupportsMethod(rightType, "__ror__") {
		if result := tryCreateUnionFromBitwiseOr(
			evaluator, node, flags, leftTypeResult, rightTypeResult, leftType, rightType); result != nil {
			return result
		}
	}

	if (flags & EvalFlagsTypeExpression) != 0 {
		// The original's comment: exempt "|" because it might be a union operation
		// involving unknowns.
		if node.D.Operator != parser.OperatorTypeBitwiseOr {
			evaluator.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.BinaryOperationNotAllowed(), node, nil)
			return &TypeResult{Type: UnknownTypeCreate(false)}
		}
	}

	// The original's comment: Optional checks apply to all operations except for
	// boolean operations.
	isLeftOptionalType := false
	if !booleanOperatorMap[node.D.Operator] {
		// The original's comment: None is a valid operand for == and != even if the
		// type stub says otherwise.
		if node.D.Operator == parser.OperatorTypeEquals || node.D.Operator == parser.OperatorTypeNotEquals {
			leftType = RemoveNoneFromUnion(leftType)
			rightType = RemoveNoneFromUnion(rightType)
		} else {
			isLeftOptionalType = IsOptionalType(leftType)
		}
	}

	diag := common.NewDiagnosticAddendum()

	// The original's comment: don't use literal math if the operation is within a
	// loop because the literal values may change each time. We also don't want to
	// apply literal math within the body of a lambda because they are often used
	// as callbacks where the value changes each time they are called.
	isLiteralMathAllowed := !IsWithinLoop(node) && GetEnclosingLambda(node) == nil

	// The original's comment: don't special-case tuple __add__ if the left type is
	// a union. This can result in an infinite loop if we keep creating new tuple
	// types within a loop construct using __add__.
	isTupleAddAllowed := !IsUnion(leftType)

	typeResult := ValidateBinaryOperation(
		evaluator, node.D.Operator,
		&TypeResult{Type: leftType, IsIncomplete: leftTypeResult.IsIncomplete},
		&TypeResult{Type: rightType, IsIncomplete: rightTypeResult.IsIncomplete},
		node, inferenceContext, diag,
		&BinaryOperationOptions{
			IsLiteralMathAllowed: isLiteralMathAllowed,
			IsTupleAddAllowed:    isTupleAddAllowed,
		})

	if typeResult.IsIncomplete {
		isIncomplete = true
	}

	if !diag.IsEmpty() {
		typeErrors = true
		if !isIncomplete {
			reportBinaryOperationFailure(evaluator, node, leftType, rightType, diag, isLeftOptionalType)
		}
	}

	return &TypeResult{
		Type:                       typeResult.Type,
		IsIncomplete:               isIncomplete,
		TypeErrors:                 typeErrors,
		MagicMethodDeprecationInfo: typeResult.MagicMethodDeprecationInfo,
	}
}

// binaryExpectedOperandTypes is the original's two expected-type locals.
//
// The multiply case is the original's, with its comment: handle the very special
// case where the expected type is a list and the operator is a multiply. This
// comes up in the common case of "x: List[Optional[X]] = [None] * y" where y is
// an integer literal.
func binaryExpectedOperandTypes(
	node *parser.BinaryOperationNode, inferenceContext *InferenceContext,
) (expectedOperandType Type, expectedLeftOperandType Type) {
	// The original's comment: for most binary operations, the "expected type" is
	// applied to the output of the magic method for that operation. However, the
	// "or" and "and" operators have no magic method, so we apply the expected type
	// directly to both operands.
	if node.D.Operator == parser.OperatorTypeOr || node.D.Operator == parser.OperatorTypeAnd {
		if inferenceContext != nil {
			expectedOperandType = inferenceContext.ExpectedType
		}
	}

	if node.D.Operator == parser.OperatorTypeMultiply && inferenceContext != nil &&
		IsClassInstance(inferenceContext.ExpectedType) &&
		ClassTypeIsBuiltInNamed(inferenceContext.ExpectedType.(*ClassType), "list") &&
		len(inferenceContext.ExpectedType.(*ClassType).Priv.TypeArgs) >= 1 &&
		node.D.LeftExpr.GetNodeType() == parser.ParseNodeTypeList {
		expectedLeftOperandType = inferenceContext.ExpectedType
	}

	return expectedOperandType, expectedLeftOperandType
}

// leftOperandTypeHeuristic is the original's `if (!expectedOperandType)` block:
// three cases where the left operand's own type is the best expectation for the
// right one.
func leftOperandTypeHeuristic(node *parser.BinaryOperationNode, leftType Type) Type {
	switch node.D.Operator {
	case parser.OperatorTypeOr, parser.OperatorTypeAnd:
		// The original's comment: for "or" and "and", use the type of the left
		// operand under certain circumstances. This allows us to infer a better
		// type for expressions like `x or []`. Do this only if it's a generic class
		// (like list or dict) or a TypedDict.
		if SomeSubtypes(leftType, func(subtype Type) bool {
			cls, ok := subtype.(*ClassType)
			if !ok || !IsClassInstance(subtype) {
				return false
			}
			return ClassTypeIsTypedDictClass(cls) || len(cls.Shared.TypeParams) > 0
		}) {
			return leftType
		}

	case parser.OperatorTypeAdd:
		// The original's comment: for the "+" operator, use this technique only if
		// the right operand is a list expression. This heuristic handles the common
		// case of `my_list + [0]`.
		if node.D.RightExpr.GetNodeType() == parser.ParseNodeTypeList {
			return leftType
		}

	case parser.OperatorTypeBitwiseOr:
		// The original's comment: if this is a bitwise or ("|"), use the type of
		// the left operand. This allows us to support the case where a TypedDict is
		// being updated with a dict expression.
		if IsClassInstance(leftType) && ClassTypeIsTypedDictClass(leftType.(*ClassType)) {
			return leftType
		}
	}

	return nil
}

// tryCreateUnionFromBitwiseOr is the union arm. It returns nil when the operands
// are not unionable, in which case `|` really is the bitwise operator.
func tryCreateUnionFromBitwiseOr(
	evaluator TypeEvaluator,
	node *parser.BinaryOperationNode,
	flags EvalFlags,
	leftTypeResult, rightTypeResult *TypeResult,
	leftType, rightType Type,
) *TypeResult {
	adjustedLeftType := leftType
	adjustedRightType := rightType

	// The original's comment: handle the special case where "None" is being added
	// to the union with something else. Even though "None" will normally be
	// interpreted as the None singleton object in contexts where a type annotation
	// isn't assumed, we'll allow it here.
	if !IsNoneInstance(leftType) && IsNoneInstance(rightType) {
		adjustedRightType = ConvertToInstantiable(evaluator.GetNoneType(), false)
	} else if !IsNoneInstance(rightType) && IsNoneInstance(leftType) {
		adjustedLeftType = ConvertToInstantiable(evaluator.GetNoneType(), false)
	}

	if !IsUnionableType([]Type{adjustedLeftType, adjustedRightType}) {
		return nil
	}

	if IsInstantiableClass(adjustedLeftType) {
		adjustedLeftType = SpecializeWithDefaultTypeArgs(adjustedLeftType.(*ClassType))
	}
	if IsInstantiableClass(adjustedRightType) {
		adjustedRightType = SpecializeWithDefaultTypeArgs(adjustedRightType.(*ClassType))
	}

	return CreateUnionTypeFromOperands(evaluator, node, flags,
		leftTypeResult, rightTypeResult, adjustedRightType, adjustedLeftType)
}

// reportBinaryOperationFailure is the original's `if (!diag.isEmpty())` block.
func reportBinaryOperationFailure(
	evaluator TypeEvaluator,
	node *parser.BinaryOperationNode,
	leftType, rightType Type,
	diag *common.DiagnosticAddendum,
	isLeftOptionalType bool,
) {
	if isLeftOptionalType && len(diag.GetMessages()) == 1 {
		// The original's comment: if the left was an optional type and there is
		// just one diagnostic, assume that it was due to a "None" not being
		// supported. Report this as a reportOptionalOperand diagnostic rather than
		// a reportGeneralTypeIssues diagnostic.
		evaluator.AddDiagnostic(DiagnosticRuleReportOptionalOperand,
			localization.LocMessage.NoneOperator().Format(PrintOperator(node.D.Operator)),
			node.D.LeftExpr, nil)
		return
	}

	// The original's comment: if neither the LHS or RHS are unions, don't include
	// a diagnostic addendum because it will be redundant with the main diagnostic
	// message. The addenda are useful only if union expansion was used for one or
	// both operands.
	diagString := ""
	if IsUnion(evaluator.MakeTopLevelTypeVarsConcrete(leftType, false)) ||
		IsUnion(evaluator.MakeTopLevelTypeVarsConcrete(rightType, false)) {
		diagString = diag.GetString()
	}

	evaluator.AddDiagnostic(
		DiagnosticRuleReportOperatorIssue,
		localization.LocMessage.TypeNotSupportBinaryOperator().Format(
			PrintOperator(node.D.Operator),
			evaluator.PrintType(leftType, nil),
			evaluator.PrintType(rightType, nil))+diagString,
		node,
		nil,
	)
}

// customMetaclassSupportsMethod corresponds to the function of the same name:
// does this class have a metaclass of its own that defines the named method.
//
// `type` itself does not count, and neither does a method inherited FROM type,
// which is why the member's owning class is checked as well as the metaclass. A
// metaclass deriving from Any is assumed not to support it -- the original's
// comment says this is the most likely case.
func customMetaclassSupportsMethod(t Type, methodName string) bool {
	if !IsInstantiableClass(t) {
		return false
	}

	metaclass := t.(*ClassType).Shared.EffectiveMetaclass
	if metaclass == nil || !IsInstantiableClass(metaclass) {
		return false
	}

	metaclassType := metaclass.(*ClassType)
	if ClassTypeIsBuiltInNamed(metaclassType, "type") {
		return false
	}

	memberInfo := LookUpClassMember(metaclassType, methodName, MemberAccessFlagsDefault, nil)
	if memberInfo == nil {
		return false
	}

	if IsAnyOrUnknown(memberInfo.ClassType) {
		return false
	}

	if IsInstantiableClass(memberInfo.ClassType) &&
		ClassTypeIsBuiltInNamed(memberInfo.ClassType.(*ClassType), "type") {
		return false
	}

	return true
}

/*
 * The two things this reaches.
 */

// BinaryOperationOptions corresponds to the interface of the same name.
type BinaryOperationOptions struct {
	IsLiteralMathAllowed bool
	IsTupleAddAllowed    bool
}

// CreateUnionTypeFromOperands corresponds to the operations.ts createUnionType,
// renamed because the evaluator already has a createUnionType for `Union[...]`.
//
// Three things happen here that a plain combineTypes would not do.
//
// Redundant literals are NOT elided. `Literal[1] | int` normally collapses to
// int, but a union the user wrote by hand should keep what they wrote.
//
// The result is marked as a special form of `types.UnionType`, so it prints and
// behaves as the runtime object `X | Y` evaluates to -- except under
// IsinstanceArg, where the second argument to isinstance is a tuple of classes
// rather than a type expression.
//
// And a stringified forward reference is rejected on either side, because `|`
// cannot see inside a string at runtime. The exception is a subscripted class:
// `list[int] | "Foo"` is legal because the index form already forced evaluation.
func CreateUnionTypeFromOperands(
	evaluator TypeEvaluator,
	node *parser.BinaryOperationNode,
	flags EvalFlags,
	leftTypeResult *TypeResult, rightTypeResult *TypeResult,
	adjustedRightType Type, adjustedLeftType Type,
) *TypeResult {
	leftExpression := node.D.LeftExpr
	rightExpression := node.D.RightExpr
	fileInfo := GetFileInfo(node)

	unionNotationSupported := fileInfo.IsStubFile ||
		(flags&EvalFlagsForwardRefs) != 0 ||
		fileInfo.ExecutionEnvironment.PythonVersion.IsGreaterOrEqualTo(common.PythonVersion3_10)

	if !unionNotationSupported {
		// The original's comment: if the left type is Any, we can't say for sure
		// whether this is an illegal syntax or a valid application of the "|"
		// operator.
		if !IsAnyOrUnknown(adjustedLeftType) {
			operatorRange := node.D.OperatorToken.GetRange()
			evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.UnionSyntaxIllegal(), node, &operatorRange)
		}
	}

	leftArg := *leftTypeResult
	rightArg := *rightTypeResult
	isLeftTypeArgValid := evaluator.ValidateTypeArg(
		&TypeResultWithNode{TypeResult: leftArg, Node: leftExpression}, nil)
	isRightTypeArgValid := evaluator.ValidateTypeArg(
		&TypeResultWithNode{TypeResult: rightArg, Node: rightExpression}, nil)

	if !isLeftTypeArgValid || !isRightTypeArgValid {
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	adjustedLeftType = evaluator.ReportMissingTypeArgs(
		node.D.LeftExpr, adjustedLeftType, flags|EvalFlagsInstantiableType)
	adjustedRightType = evaluator.ReportMissingTypeArgs(
		node.D.RightExpr, adjustedRightType, flags|EvalFlagsInstantiableType)

	newUnion := CombineTypes([]Type{adjustedLeftType, adjustedRightType},
		&CombineTypesOptions{SkipElideRedundantLiterals: true})

	unionClass := evaluator.GetUnionClassType()
	if IsInstantiableClass(unionClass) && (flags&EvalFlagsIsinstanceArg) == 0 {
		newUnion = CloneAsSpecialForm(newUnion, ClassTypeCloneAsInstance(unionClass.(*ClassType), false))
	}

	leftProps := leftTypeResult.Type.Base().Props
	rightProps := rightTypeResult.Type.Base().Props
	if leftProps != nil && leftProps.TypeForm != nil && rightProps != nil && rightProps.TypeForm != nil {
		newTypeForm := CombineTypes([]Type{leftProps.TypeForm, rightProps.TypeForm}, nil)
		newUnion = CloneWithTypeForm(newUnion, newTypeForm)
	}

	// The original's comment: check for "stringified" forward reference type
	// expressions. The "|" operator doesn't support these except in certain
	// circumstances. Notably, it can't be used with other strings or with types
	// that are not specialized using an index form.
	if !fileInfo.IsStubFile {
		reportStringifiedUnionOperand(evaluator, leftExpression, rightExpression, leftTypeResult, rightTypeResult)
	}

	return &TypeResult{Type: newUnion}
}

// reportStringifiedUnionOperand is the original's forward-reference check. Only
// one side can be a string: two strings leave otherType unset in the original,
// since the second branch is an else-if.
func reportStringifiedUnionOperand(
	evaluator TypeEvaluator,
	leftExpression, rightExpression parser.ExpressionNode,
	leftTypeResult, rightTypeResult *TypeResult,
) {
	var stringNode parser.ExpressionNode
	var otherType Type

	if leftExpression.GetNodeType() == parser.ParseNodeTypeStringList {
		stringNode = leftExpression
		otherType = rightTypeResult.Type
	} else if rightExpression.GetNodeType() == parser.ParseNodeTypeStringList {
		stringNode = rightExpression
		otherType = leftTypeResult.Type
	}

	if stringNode == nil || otherType == nil {
		return
	}

	isAllowed := true
	if cls, ok := otherType.(*ClassType); ok && IsClass(otherType) {
		// An explicitly subscripted instantiable class is allowed; a bare class or
		// an instance is not.
		if cls.Priv.IsTypeArgExplicit == nil || !*cls.Priv.IsTypeArgExplicit || IsClassInstance(otherType) {
			isAllowed = false
		}
	}

	if !isAllowed {
		evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.UnionForwardReferenceNotAllowed(), stringNode, nil)
	}
}
