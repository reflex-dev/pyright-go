/*
 * typeevaluator_comprehension.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfComprehension, evaluateComprehensionForIf,
 * getElementTypeFromComprehension, getTypeOfSlice.
 *
 * A comprehension is evaluated by "executing" its for/if clauses left to right,
 * binding each `for` target from the iterator type of its iterable, and only
 * then evaluating the element expression in the scope those bindings created.
 * evaluateComprehensionForIf is what performs the binding, and it is called for
 * its side effect on the type cache as much as for its return value.
 *
 * The `if` clauses are evaluated too, and the original explains why: an
 * assignment expression inside the condition can bind a name that the element
 * expression then reads, so skipping them would leave that name unevaluated.
 *
 * getElementTypeFromComprehension serves both plain comprehensions and dict
 * comprehensions. For a dict it synthesizes a two-element tuple of (key, value)
 * so that the single caller-facing "element type" can carry both.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// getTypeOfComprehension corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfComprehension(
	node *parser.ComprehensionNode, flags EvalFlags, inferenceContext *InferenceContext,
) *TypeResult {
	isIncomplete := false
	typeErrors := false

	// The original's comment: if any of the "for" clauses are marked async or any
	// of the "if" clauses or any clause other than the leftmost "for" contain an
	// "await" operator, it is treated as an async generator.
	isAsync := false
	for index, comp := range node.D.ForIfNodes {
		if forNode, ok := comp.(*parser.ComprehensionForNode); ok && forNode.D.IsAsync {
			isAsync = true
			break
		}
		if index > 0 && ContainsAwaitNode(comp) {
			isAsync = true
			break
		}
	}

	var t Type = UnknownTypeCreate(false)

	if ContainsAwaitNode(node.D.Expr) {
		isAsync = true
	}

	generatorName := "Generator"
	if isAsync {
		generatorName = "AsyncGenerator"
	}
	builtInIteratorType := e.getTypingType(node, generatorName)

	expectedEntryType := e.getExpectedEntryTypeForIterable(node, builtInIteratorType, inferenceContext)
	elementTypeResult := e.getElementTypeFromComprehension(
		node, flags|EvalFlagsStripTupleLiterals, expectedEntryType, nil)

	if elementTypeResult.IsIncomplete {
		isIncomplete = true
	}

	if elementTypeResult.TypeErrors {
		typeErrors = true
	}

	elementType := elementTypeResult.Type
	if expectedEntryType == nil || !ContainsLiteralType(expectedEntryType, false) {
		elementType = e.StripLiteralValue(elementType)
	}

	if builtInIteratorType != nil && IsInstantiableClass(builtInIteratorType) {
		var typeArgs []Type
		if isAsync {
			typeArgs = []Type{elementType, e.GetNoneType()}
		} else {
			typeArgs = []Type{elementType, e.GetNoneType(), e.GetNoneType()}
		}
		t = ClassTypeCloneAsInstance(
			ClassTypeSpecialize(builtInIteratorType.(*ClassType), typeArgs, nil, false, nil, nil), true)
	}

	return &TypeResult{Type: t, IsIncomplete: isIncomplete, TypeErrors: typeErrors}
}

// evaluateComprehensionForIf corresponds to the function of the same name. The
// original returns whether the iterable type was incomplete; the evaluator's
// other caller ignores the answer, so the interface method discards it.
func (e *typeEvaluator) evaluateComprehensionForIfWithResult(node parser.ComprehensionForIfNode) bool {
	isIncomplete := false

	if forNode, ok := node.(*parser.ComprehensionForNode); ok {
		iterableTypeResult := e.getTypeOfExpression(forNode.D.IterableExpr, EvalFlagsNone, nil)
		if iterableTypeResult.IsIncomplete {
			isIncomplete = true
		}
		iterableType := e.StripLiteralValue(iterableTypeResult.Type)
		itemTypeResult := e.GetTypeOfIterator(
			&TypeResult{Type: iterableType, IsIncomplete: iterableTypeResult.IsIncomplete},
			forNode.D.IsAsync,
			forNode.D.IterableExpr,
			nil,
		)
		if itemTypeResult == nil {
			itemTypeResult = &TypeResult{
				Type: UnknownTypeCreate(false), IsIncomplete: iterableTypeResult.IsIncomplete}
		}

		targetExpr := forNode.D.TargetExpr
		e.assignTypeToExpression(targetExpr, itemTypeResult, forNode.D.IterableExpr, false, false, nil)
	} else {
		// The original asserts the node is a ComprehensionIf here.
		ifNode := node.(*parser.ComprehensionIfNode)

		// The original's comment: evaluate the test expression to validate it and mark
		// symbols as referenced. This doesn't affect the type of the evaluated
		// comprehension, but it is important for evaluating intermediate expressions
		// such as assignment expressions that can affect other subexpressions.
		e.getTypeOfExpression(ifNode.D.TestExpr, EvalFlagsNone, nil)
	}

	return isIncomplete
}

// evaluateComprehensionForIf is the arity the expression-in-context walk calls
// with; it discards the incomplete flag the way that caller does.
func (e *typeEvaluator) evaluateComprehensionForIf(node parser.ParseNode) {
	if forIfNode, ok := node.(parser.ComprehensionForIfNode); ok {
		e.evaluateComprehensionForIfWithResult(forIfNode)
	}
}

// getElementTypeFromComprehension corresponds to the function of the same name.
//
// The original's comment: returns the type of one entry returned by the
// comprehension.
func (e *typeEvaluator) getElementTypeFromComprehension(
	node *parser.ComprehensionNode,
	flags EvalFlags,
	expectedValueOrElementType Type,
	expectedKeyType Type,
) *TypeResult {
	isIncomplete := false
	typeErrors := false

	// The original's comment: "execute" the list comprehensions from start to
	// finish.
	for _, forIfNode := range node.D.ForIfNodes {
		if e.evaluateComprehensionForIfWithResult(forIfNode) {
			isIncomplete = true
		}
	}

	var t Type = UnknownTypeCreate(false)

	switch expr := node.D.Expr.(type) {
	case *parser.DictionaryKeyEntryNode:
		// The original's comment: create a tuple with the key/value types.
		keyTypeResult := e.getTypeOfExpression(
			expr.D.KeyExpr, flags, MakeInferenceContext(expectedKeyType, false, nil))
		if keyTypeResult.IsIncomplete {
			isIncomplete = true
		}
		if keyTypeResult.TypeErrors {
			typeErrors = true
		}
		keyType := keyTypeResult.Type
		if expectedKeyType == nil || !ContainsLiteralType(expectedKeyType, false) {
			keyType = e.StripLiteralValue(keyType)
		}

		valueTypeResult := e.getTypeOfExpression(
			expr.D.ValueExpr, flags, MakeInferenceContext(expectedValueOrElementType, false, nil))
		if valueTypeResult.IsIncomplete {
			isIncomplete = true
		}
		if valueTypeResult.TypeErrors {
			typeErrors = true
		}
		valueType := valueTypeResult.Type
		if expectedValueOrElementType == nil || !ContainsLiteralType(expectedValueOrElementType, false) {
			valueType = e.StripLiteralValue(valueType)
		}

		t = MakeTupleObject(e, []*TupleTypeArg{
			{Type: keyType, IsUnbounded: false},
			{Type: valueType, IsUnbounded: false},
		}, false)

	case *parser.DictionaryExpandEntryNode:
		// The original's comment: the parser should have reported an error in this
		// case because it's not allowed.
		e.getTypeOfExpression(
			expr.D.Expr, flags, MakeInferenceContext(expectedValueOrElementType, false, nil))

	default:
		// The original tests `isExpressionNode(node)` -- the comprehension node, not
		// its expression -- before casting node.d.expr to an ExpressionNode. Kept as
		// written.
		if parser.IsExpressionNode(node) {
			exprNode, ok := node.D.Expr.(parser.ExpressionNode)
			if ok {
				exprTypeResult := e.getTypeOfExpression(
					exprNode, flags, MakeInferenceContext(expectedValueOrElementType, false, nil))
				if exprTypeResult.IsIncomplete {
					isIncomplete = true
				}
				if exprTypeResult.TypeErrors {
					typeErrors = true
				}
				t = exprTypeResult.Type
			}
		}
	}

	return &TypeResult{Type: t, IsIncomplete: isIncomplete, TypeErrors: typeErrors}
}

// getTypeOfSlice corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfSlice(node *parser.SliceNode) *TypeResult {
	noneType := e.GetNoneType()
	startType := noneType
	endType := noneType
	stepType := noneType
	isIncomplete := false

	// The original's comment: evaluate the expressions to report errors and record
	// symbol references.
	if node.D.StartValue != nil {
		startTypeResult := e.getTypeOfExpression(node.D.StartValue, EvalFlagsNone, nil)
		startType = startTypeResult.Type
		if startTypeResult.IsIncomplete {
			isIncomplete = true
		}
	}

	if node.D.EndValue != nil {
		endTypeResult := e.getTypeOfExpression(node.D.EndValue, EvalFlagsNone, nil)
		endType = endTypeResult.Type
		if endTypeResult.IsIncomplete {
			isIncomplete = true
		}
	}

	if node.D.StepValue != nil {
		stepTypeResult := e.getTypeOfExpression(node.D.StepValue, EvalFlagsNone, nil)
		stepType = stepTypeResult.Type
		if stepTypeResult.IsIncomplete {
			isIncomplete = true
		}
	}

	sliceType := e.GetBuiltInObject(node, "slice", nil)

	if !IsClassInstance(sliceType) {
		return &TypeResult{Type: sliceType}
	}

	return &TypeResult{
		Type: ClassTypeSpecialize(
			sliceType.(*ClassType), []Type{startType, endType, stepType}, nil, false, nil, nil),
		IsIncomplete: isIncomplete,
	}
}
