/*
 * binder_flow.go
 *
 * The binder's code-flow machinery, transliterated from analyzer/binder.ts
 * (pyright 1.1.412): the flow-node constructors, the conditional and
 * never-condition binding, and _isNarrowingExpression.
 *
 * Two things recur throughout:
 *
 *   - `this._currentFlowNode!` is a non-null assertion on a field that is only
 *     unset before the module's start node is created. It is a plain field read
 *     here; a nil would panic at the same place the original would throw.
 *   - Several helpers save a field, run a callback and restore it. Those are
 *     written as explicit save/restore rather than defer, matching the original
 *     line for line -- and _bindNeverCondition in particular restores
 *     conditionally, which a defer could not express.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// createStartFlowNode corresponds to _createStartFlowNode.
func (b *Binder) createStartFlowNode() FlowNode {
	return &FlowNodeBase{
		Flags: FlowFlagsStart,
		ID:    b.getUniqueFlowNodeID(),
	}
}

// createBranchLabel corresponds to _createBranchLabel. The TypeScript leaves
// preBranchAntecedent undefined; pass nil for that.
func (b *Binder) createBranchLabel(preBranchAntecedent FlowNode) *FlowBranchLabel {
	return &FlowBranchLabel{
		FlowLabel: FlowLabel{
			FlowNodeBase: FlowNodeBase{
				Flags: FlowFlagsBranchLabel,
				ID:    b.getUniqueFlowNodeID(),
			},
			Antecedents:         []FlowNode{},
			AffectedExpressions: nil,
		},
		PreBranchAntecedent: preBranchAntecedent,
	}
}

// createFlowNarrowForPattern corresponds to _createFlowNarrowForPattern. It
// creates a flow node that narrows the type of the subject expression for a
// specified case statement or the entire match statement (if the flow falls
// through the bottom of all cases). statement is a CaseNode or MatchNode.
func (b *Binder) createFlowNarrowForPattern(subjectExpression parser.ExpressionNode, statement parser.ParseNode) {
	b.currentFlowNode = &FlowNarrowForPattern{
		FlowNodeBase: FlowNodeBase{
			Flags: FlowFlagsNarrowForPattern,
			ID:    b.getUniqueFlowNodeID(),
		},
		SubjectExpression: subjectExpression,
		Statement:         statement,
		Antecedent:        b.currentFlowNode,
	}
}

// createContextManagerLabel corresponds to _createContextManagerLabel.
func (b *Binder) createContextManagerLabel(
	expressions []parser.ExpressionNode,
	isAsync bool,
	blockIfSwallowsExceptions bool,
) *FlowPostContextManagerLabel {
	return &FlowPostContextManagerLabel{
		FlowLabel: FlowLabel{
			FlowNodeBase: FlowNodeBase{
				Flags: FlowFlagsPostContextManager | FlowFlagsBranchLabel,
				ID:    b.getUniqueFlowNodeID(),
			},
			Antecedents:         []FlowNode{},
			AffectedExpressions: nil,
		},
		Expressions:               expressions,
		IsAsync:                   isAsync,
		BlockIfSwallowsExceptions: blockIfSwallowsExceptions,
	}
}

// createLoopLabel corresponds to _createLoopLabel.
func (b *Binder) createLoopLabel() *FlowLabel {
	return &FlowLabel{
		FlowNodeBase: FlowNodeBase{
			Flags: FlowFlagsLoopLabel,
			ID:    b.getUniqueFlowNodeID(),
		},
		Antecedents:         []FlowNode{},
		AffectedExpressions: nil,
	}
}

// finishFlowLabel corresponds to _finishFlowLabel.
//
// The parameter is typed FlowLabel in the original but is passed
// FlowBranchLabel and FlowPostContextManagerLabel too, and the return value is
// the label itself in those cases, so this takes the FlowNode and the embedded
// label separately: Go has no upcast that preserves the dynamic type.
func (b *Binder) finishFlowLabel(node FlowNode, label *FlowLabel) FlowNode {
	// If there were no antecedents, this is unreachable.
	if len(label.Antecedents) == 0 {
		return unreachableStructuralFlowNode
	}

	// If there was only one antecedent and this is a simple branch label,
	// there's no need for a label to exist.
	//
	// The test is on the whole flags value, not a bit test, so a context
	// manager label -- which carries PostContextManager too -- never collapses.
	if len(label.Antecedents) == 1 && label.Flags == FlowFlagsBranchLabel {
		return label.Antecedents[0]
	}

	// The cyclomatic complexity is the number of edges minus the number of
	// nodes in the graph. Add n-1 where n is the number of antecedents (edges)
	// and 1 represents the label node.
	b.codeFlowComplexity += float64(len(label.Antecedents) - 1)

	return node
}

// finishBranchLabel is the common case of finishFlowLabel where the label is a
// plain branch label.
func (b *Binder) finishBranchLabel(label *FlowBranchLabel) FlowNode {
	return b.finishFlowLabel(label, &label.FlowLabel)
}

// finishLoopLabel is the common case of finishFlowLabel where the label is a
// loop label.
func (b *Binder) finishLoopLabel(label *FlowLabel) FlowNode {
	return b.finishFlowLabel(label, label)
}

// bindNeverCondition corresponds to _bindNeverCondition. It creates a node that
// creates a "gate" that is closed (doesn't allow for code flow) if the
// specified expression is never once it is narrowed (in either the positive or
// negative case).
func (b *Binder) bindNeverCondition(node parser.ExpressionNode, target *FlowLabel, isPositiveTest bool) {
	expressionList := []CodeFlowReferenceExpressionNode{}

	if unary, ok := node.(*parser.UnaryOperationNode); ok && unary.D.Operator == parser.OperatorTypeNot {
		b.bindNeverCondition(unary.D.Expr, target, !isPositiveTest)
		return
	}

	if binary, ok := node.(*parser.BinaryOperationNode); ok &&
		(binary.D.Operator == parser.OperatorTypeAnd || binary.D.Operator == parser.OperatorTypeOr) {
		isAnd := binary.D.Operator == parser.OperatorTypeAnd
		if isPositiveTest {
			isAnd = !isAnd
		}

		if isAnd {
			// In the And case, we need to gate the synthesized else clause if
			// both of the operands evaluate to never once they are narrowed.
			savedCurrentFlowNode := b.currentFlowNode
			b.bindNeverCondition(binary.D.LeftExpr, target, isPositiveTest)
			b.currentFlowNode = savedCurrentFlowNode
			b.bindNeverCondition(binary.D.RightExpr, target, isPositiveTest)
		} else {
			initialCurrentFlowNode := b.currentFlowNode

			// In the Or case, we need to gate the synthesized else clause if
			// either of the operands evaluate to never.
			afterLabel := b.createBranchLabel(nil)
			b.bindNeverCondition(binary.D.LeftExpr, &afterLabel.FlowLabel, isPositiveTest)

			// If the condition didn't result in any new flow nodes, we can skip
			// checking the other condition.
			if initialCurrentFlowNode != b.currentFlowNode {
				b.currentFlowNode = b.finishBranchLabel(afterLabel)

				prevCurrentNode := b.currentFlowNode
				b.bindNeverCondition(binary.D.RightExpr, target, isPositiveTest)

				// If the second condition resulted in no new control flow node,
				// we can eliminate this entire subgraph.
				if prevCurrentNode == b.currentFlowNode {
					b.currentFlowNode = initialCurrentFlowNode
				}
			}
		}
		return
	}

	// Limit only to expressions that contain a narrowable subexpression that is
	// a name. This avoids complexities with composite expressions like member
	// access or index expressions.
	if b.isNarrowingExpression(node, &expressionList, narrowExprOptions{FilterForNeverNarrowing: true}) {
		filteredCount := 0
		for _, expr := range expressionList {
			if expr.GetNodeType() == parser.ParseNodeTypeName {
				filteredCount++
			}
		}
		if filteredCount > 0 {
			flags := FlowFlagsFalseNeverCondition
			if isPositiveTest {
				flags = FlowFlagsTrueNeverCondition
			}
			b.currentFlowNode = b.createFlowConditional(flags, b.currentFlowNode, node)
		}
	}

	b.addAntecedent(target, b.currentFlowNode)
}

// bindConditional corresponds to _bindConditional.
func (b *Binder) bindConditional(node parser.ExpressionNode, trueTarget *FlowLabel, falseTarget *FlowLabel) {
	b.setTrueFalseTargets(trueTarget, falseTarget, func() {
		b.Walk(node)
	})

	if !b.isLogicalExpression(node) {
		b.addAntecedent(trueTarget, b.createFlowConditional(FlowFlagsTrueCondition, b.currentFlowNode, node))
		b.addAntecedent(falseTarget, b.createFlowConditional(FlowFlagsFalseCondition, b.currentFlowNode, node))
	}
}

// disableTrueFalseTargets corresponds to _disableTrueFalseTargets.
func (b *Binder) disableTrueFalseTargets(callback func()) {
	b.setTrueFalseTargets(nil /* trueTarget */, nil /* falseTarget */, callback)
}

// setTrueFalseTargets corresponds to _setTrueFalseTargets.
func (b *Binder) setTrueFalseTargets(trueTarget *FlowLabel, falseTarget *FlowLabel, callback func()) {
	savedTrueTarget := b.currentTrueTarget
	savedFalseTarget := b.currentFalseTarget
	b.currentTrueTarget = trueTarget
	b.currentFalseTarget = falseTarget

	callback()

	b.currentTrueTarget = savedTrueTarget
	b.currentFalseTarget = savedFalseTarget
}

// createFlowConditional corresponds to _createFlowConditional.
func (b *Binder) createFlowConditional(
	flags FlowFlags,
	antecedent FlowNode,
	expression parser.ExpressionNode,
) FlowNode {
	if antecedent.FlowBase().Flags&(FlowFlagsUnreachableStructural|FlowFlagsUnreachableStaticCondition) != 0 {
		return antecedent
	}

	staticValue, staticValueSet := EvaluateStaticBoolLikeExpression(
		expression,
		b.fileInfo.ExecutionEnvironment,
		b.fileInfo.DefinedConstants,
		b.typingImportAliases,
		b.sysImportAliases,
	)
	if staticValueSet {
		if (staticValue && flags&FlowFlagsFalseCondition != 0) ||
			(!staticValue && flags&FlowFlagsTrueCondition != 0) {
			return unreachableStaticConditionFlowNode
		}
	}

	expressionList := []CodeFlowReferenceExpressionNode{}
	if !b.isNarrowingExpression(expression, &expressionList, narrowExprOptions{
		FilterForNeverNarrowing: flags&(FlowFlagsTrueNeverCondition|FlowFlagsFalseNeverCondition) != 0,
	}) {
		return antecedent
	}

	for _, expr := range expressionList {
		b.currentScopeCodeFlowExpressions.Add(CreateKeyForReference(expr))
	}

	// Select the first name expression.
	var reference *parser.NameNode
	for _, expr := range expressionList {
		if name, ok := expr.(*parser.NameNode); ok {
			reference = name
			break
		}
	}

	conditionalFlowNode := &FlowCondition{
		FlowNodeBase: FlowNodeBase{
			Flags: flags,
			ID:    b.getUniqueFlowNodeID(),
		},
		Reference:  reference,
		Expression: expression,
		Antecedent: antecedent,
	}

	b.addExceptTargets(conditionalFlowNode)

	return conditionalFlowNode
}

// isLogicalExpression corresponds to _isLogicalExpression. It indicates whether
// the expression is a NOT, AND or OR expression.
func (b *Binder) isLogicalExpression(expression parser.ExpressionNode) bool {
	switch typed := expression.(type) {
	case *parser.UnaryOperationNode:
		return typed.D.Operator == parser.OperatorTypeNot

	case *parser.BinaryOperationNode:
		return typed.D.Operator == parser.OperatorTypeAnd || typed.D.Operator == parser.OperatorTypeOr
	}

	return false
}

// isNarrowingExpression corresponds to _isNarrowingExpression. It determines
// whether the specified expression can be used for conditional type narrowing.
// The expression atoms (names, member accesses and index) are provided as an
// output in expressionList.
//
// If options.FilterForNeverNarrowing is true, some types of narrowing
// expressions are limited for performance reasons. options.IsComplexExpression
// is used internally to determine whether the call is an atom (name, member
// access, index -- plus a "not" form of these) or something more complex
// (binary operator, call, etc.).
//
// The TypeScript defaults options to {}; pass the zero value for that. The
// recursive calls spread `...options` and override one or two fields, so each
// call site here copies options and assigns.
func (b *Binder) isNarrowingExpression(
	expression parser.ExpressionNode,
	expressionList *[]CodeFlowReferenceExpressionNode,
	options narrowExprOptions,
) bool {
	switch typed := expression.(type) {
	case *parser.NameNode, *parser.MemberAccessNode, *parser.IndexNode:
		if options.FilterForNeverNarrowing {
			// Never narrowing doesn't support member access or index
			// expressions.
			if expression.GetNodeType() != parser.ParseNodeTypeName {
				return false
			}

			// Never narrowing doesn't support simple names (falsy or truthy
			// narrowing) because it's too expensive and provides relatively
			// little utility.
			if !options.IsComplexExpression {
				return false
			}
		}

		if IsCodeFlowSupportedForReference(expression) {
			*expressionList = append(*expressionList, expression)

			if !options.FilterForNeverNarrowing {
				// If the expression is a member access expression, add its
				// leftExpression to the expression list because that expression
				// can be narrowed based on the attribute type.
				if memberAccess, ok := expression.(*parser.MemberAccessNode); ok && options.AllowDiscriminatedNarrowing {
					if IsCodeFlowSupportedForReference(memberAccess.D.LeftExpr) {
						*expressionList = append(*expressionList, memberAccess.D.LeftExpr)
					}
				}

				// If the expression is an index expression with a supported
				// subscript, add its baseExpression to the expression list
				// because that expression can be narrowed.
				if index, ok := expression.(*parser.IndexNode); ok &&
					len(index.D.Items) == 1 &&
					!index.D.TrailingComma &&
					index.D.Items[0].D.ArgCategory == parser.ArgCategorySimple {
					if IsCodeFlowSupportedForReference(index.D.LeftExpr) {
						*expressionList = append(*expressionList, index.D.LeftExpr)
					}
				}
			}
			return true
		}

		return false

	case *parser.AssignmentExpressionNode:
		*expressionList = append(*expressionList, typed.D.Name)
		complexOptions := options
		complexOptions.IsComplexExpression = true
		b.isNarrowingExpression(typed.D.RightExpr, expressionList, complexOptions)
		return true

	case *parser.BinaryOperationNode:
		return b.isNarrowingBinaryOperation(typed, expressionList, options)

	case *parser.UnaryOperationNode:
		if typed.D.Operator != parser.OperatorTypeNot {
			return false
		}
		atomOptions := options
		atomOptions.IsComplexExpression = false
		return b.isNarrowingExpression(typed.D.Expr, expressionList, atomOptions)

	case *parser.AugmentedAssignmentNode:
		complexOptions := options
		complexOptions.IsComplexExpression = true
		return b.isNarrowingExpression(typed.D.RightExpr, expressionList, complexOptions)

	case *parser.CallNode:
		complexOptions := options
		complexOptions.IsComplexExpression = true

		if leftName, ok := typed.D.LeftExpr.(*parser.NameNode); ok {
			if (leftName.D.Value == "isinstance" || leftName.D.Value == "issubclass") && len(typed.D.Args) == 2 {
				return b.isNarrowingExpression(typed.D.Args[0].D.ValueExpr, expressionList, complexOptions)
			}

			if leftName.D.Value == "callable" && len(typed.D.Args) == 1 {
				return b.isNarrowingExpression(typed.D.Args[0].D.ValueExpr, expressionList, complexOptions)
			}
		}

		// Is this potentially a call to a user-defined type guard function?
		if len(typed.D.Args) >= 1 {
			// Never narrowing doesn't support type guards because they do not
			// offer negative narrowing.
			if options.FilterForNeverNarrowing {
				return false
			}

			return b.isNarrowingExpression(typed.D.Args[0].D.ValueExpr, expressionList, complexOptions)
		}

		// The original's Call case has no break and no return on this path, so
		// it falls out of the switch to the final `return false`.
		return false
	}

	return false
}

// isNarrowingBinaryOperation is the BinaryOperation case of
// _isNarrowingExpression, split out for readability.
func (b *Binder) isNarrowingBinaryOperation(
	expression *parser.BinaryOperationNode,
	expressionList *[]CodeFlowReferenceExpressionNode,
	options narrowExprOptions,
) bool {
	isOrIsNotOperator := expression.D.Operator == parser.OperatorTypeIs ||
		expression.D.Operator == parser.OperatorTypeIsNot
	equalsOrNotEqualsOperator := expression.D.Operator == parser.OperatorTypeEquals ||
		expression.D.Operator == parser.OperatorTypeNotEquals

	complexOptions := options
	complexOptions.IsComplexExpression = true

	discriminatedOptions := complexOptions
	discriminatedOptions.AllowDiscriminatedNarrowing = true

	if isOrIsNotOperator || equalsOrNotEqualsOperator {
		// Look for "X is None", "X is not None", "X == None", "X != None".
		// These are commonly-used patterns used in control flow.
		if constant, ok := expression.D.RightExpr.(*parser.ConstantNode); ok &&
			constant.D.ConstType == parser.KeywordTypeNone {
			return b.isNarrowingExpression(expression.D.LeftExpr, expressionList, discriminatedOptions)
		}

		// Look for "type(X) is Y" or "type(X) is not Y".
		if isOrIsNotOperator {
			if call, ok := expression.D.LeftExpr.(*parser.CallNode); ok {
				if callName, ok := call.D.LeftExpr.(*parser.NameNode); ok &&
					callName.D.Value == "type" &&
					len(call.D.Args) == 1 &&
					call.D.Args[0].D.ArgCategory == parser.ArgCategorySimple {
					return b.isNarrowingExpression(call.D.Args[0].D.ValueExpr, expressionList, complexOptions)
				}
			}
		}

		// Look for "X is Y" or "X is not Y".
		// Look for X == <literal> or X != <literal>
		// Look for len(X) == <literal> or len(X) != <literal>
		return b.isNarrowingExpression(expression.D.LeftExpr, expressionList, discriminatedOptions)
	}

	// Look for len(X) < <literal>, len(X) <= <literal>, len(X) > <literal>,
	// len(X) >= <literal>.
	if number, ok := expression.D.RightExpr.(*parser.NumberNode); ok && number.D.IsInteger {
		switch expression.D.Operator {
		case parser.OperatorTypeLessThan,
			parser.OperatorTypeLessThanOrEqual,
			parser.OperatorTypeGreaterThan,
			parser.OperatorTypeGreaterThanOrEqual:
			return b.isNarrowingExpression(expression.D.LeftExpr, expressionList, complexOptions)
		}
	}

	if expression.D.Operator == parser.OperatorTypeIn || expression.D.Operator == parser.OperatorTypeNotIn {
		// Look for "<string> in Y" or "<string> not in Y".
		//
		// Note that this runs before the general in/not-in case and shares its
		// expressionList: when the left side is a string list, the right side
		// is walked here and, if it narrows, the function returns true without
		// reaching the general case.
		if expression.D.LeftExpr.GetNodeType() == parser.ParseNodeTypeStringList {
			if b.isNarrowingExpression(expression.D.RightExpr, expressionList, complexOptions) {
				return true
			}
		}

		// Look for "X in Y" or "X not in Y".
		isLeftNarrowable := b.isNarrowingExpression(expression.D.LeftExpr, expressionList, complexOptions)
		isRightNarrowable := b.isNarrowingExpression(expression.D.RightExpr, expressionList, complexOptions)

		return isLeftNarrowable || isRightNarrowable
	}

	return false
}

// createAssignmentTargetFlowNodes corresponds to
// _createAssignmentTargetFlowNodes.
func (b *Binder) createAssignmentTargetFlowNodes(target parser.ExpressionNode, walkTargets bool, unbound bool) {
	switch typed := target.(type) {
	case *parser.NameNode, *parser.MemberAccessNode, *parser.IndexNode:
		b.createFlowAssignment(target, unbound)
		if walkTargets {
			b.Walk(target)
		}

	case *parser.TupleNode:
		for _, expr := range typed.D.Items {
			b.createAssignmentTargetFlowNodes(expr, walkTargets, unbound)
		}

	case *parser.TypeAnnotationNode:
		b.createAssignmentTargetFlowNodes(typed.D.ValueExpr, false /* walkTargets */, unbound)
		if walkTargets {
			b.Walk(target)
		}

	case *parser.UnpackNode:
		b.createAssignmentTargetFlowNodes(typed.D.Expr, false /* walkTargets */, unbound)
		if walkTargets {
			b.Walk(target)
		}

	case *parser.ListNode:
		for _, entry := range typed.D.Items {
			b.createAssignmentTargetFlowNodes(entry, walkTargets, unbound)
		}

	default:
		if walkTargets {
			b.Walk(target)
		}
	}
}

// createCallFlowNode corresponds to _createCallFlowNode.
func (b *Binder) createCallFlowNode(node *parser.CallNode) {
	if !b.isCodeUnreachable() {
		b.addExceptTargets(b.currentFlowNode)

		b.currentFlowNode = &FlowCall{
			FlowNodeBase: FlowNodeBase{
				Flags: FlowFlagsCall,
				ID:    b.getUniqueFlowNodeID(),
			},
			Node:       node,
			Antecedent: b.currentFlowNode,
		}
	}
}

// createVariableAnnotationFlowNode corresponds to
// _createVariableAnnotationFlowNode.
func (b *Binder) createVariableAnnotationFlowNode() {
	if !b.isCodeUnreachable() {
		b.currentFlowNode = &FlowVariableAnnotation{
			FlowNodeBase: FlowNodeBase{
				Flags: FlowFlagsVariableAnnotation,
				ID:    b.getUniqueFlowNodeID(),
			},
			Antecedent: b.currentFlowNode,
		}
	}
}

// createFlowAssignment corresponds to _createFlowAssignment. The TypeScript
// defaults unbound to false.
func (b *Binder) createFlowAssignment(node CodeFlowReferenceExpressionNode, unbound bool) {
	targetSymbolID := IndeterminateSymbolID
	if name, ok := node.(*parser.NameNode); ok {
		symbolWithScope := b.currentScope.LookUpSymbolRecursive(name.D.Value, nil)
		assert(symbolWithScope != nil, "")
		targetSymbolID = symbolWithScope.Symbol.ID
	}

	prevFlowNode := b.currentFlowNode
	if !b.isCodeUnreachable() && IsCodeFlowSupportedForReference(node) {
		flowNode := &FlowAssignment{
			FlowNodeBase: FlowNodeBase{
				Flags: FlowFlagsAssignment,
				ID:    b.getUniqueFlowNodeID(),
			},
			Node:           node,
			Antecedent:     b.currentFlowNode,
			TargetSymbolID: targetSymbolID,
		}

		b.currentScopeCodeFlowExpressions.Add(CreateKeyForReference(node))

		if unbound {
			flowNode.Flags |= FlowFlagsUnbind
		}

		// Assume that an assignment to a member access expression can
		// potentially generate an exception.
		if node.GetNodeType() == parser.ParseNodeTypeMemberAccess {
			b.addExceptTargets(flowNode)
		}
		b.currentFlowNode = flowNode
	}

	// If we're marking the node as unbound and there is already a flow node
	// associated with the node, don't replace it. This case applies for symbols
	// introduced in except clauses. If there is no use the previous flow node
	// associated, use the previous flow node (applies in the del case).
	// Otherwise, the node will be evaluated as unbound at this point in the
	// flow.
	if !unbound || GetFlowNode(node) == nil {
		if unbound {
			SetFlowNode(node, prevFlowNode)
		} else {
			SetFlowNode(node, b.currentFlowNode)
		}
	}
}

// createFlowWildcardImport corresponds to _createFlowWildcardImport.
func (b *Binder) createFlowWildcardImport(node *parser.ImportFromNode, names []string) {
	if !b.isCodeUnreachable() {
		flowNode := &FlowWildcardImport{
			FlowNodeBase: FlowNodeBase{
				Flags: FlowFlagsWildcardImport,
				ID:    b.getUniqueFlowNodeID(),
			},
			Node:       node,
			Names:      names,
			Antecedent: b.currentFlowNode,
		}

		b.addExceptTargets(flowNode)
		b.currentFlowNode = flowNode
	}

	SetFlowNode(node, b.currentFlowNode)
}

// createFlowExhaustedMatch corresponds to _createFlowExhaustedMatch.
func (b *Binder) createFlowExhaustedMatch(node *parser.MatchNode) {
	if !b.isCodeUnreachable() {
		b.currentFlowNode = &FlowExhaustedMatch{
			FlowNodeBase: FlowNodeBase{
				Flags: FlowFlagsExhaustedMatch,
				ID:    b.getUniqueFlowNodeID(),
			},
			Node:              node,
			Antecedent:        b.currentFlowNode,
			SubjectExpression: node.D.Expr,
		}
	}

	SetAfterFlowNode(node, b.currentFlowNode)
}

// isCodeUnreachable corresponds to _isCodeUnreachable.
func (b *Binder) isCodeUnreachable() bool {
	return b.currentFlowNode.FlowBase().Flags&
		(FlowFlagsUnreachableStaticCondition|FlowFlagsUnreachableStructural) != 0
}

// addExceptTargets corresponds to _addExceptTargets.
func (b *Binder) addExceptTargets(flowNode FlowNode) {
	// If there are any except targets, then we're in a try block, and we have
	// to assume that an exception can be raised after every assignment.
	//
	// The original's `if (this._currentExceptTargets)` is an array truthiness
	// test that is always true -- the field is initialized to [] and every
	// assignment to it is an array -- so the guard is dropped rather than
	// mistranslated into a length check.
	for _, label := range b.currentExceptTargets {
		b.addAntecedent(label, flowNode)
	}
}

// trackCodeFlowExpressions corresponds to _trackCodeFlowExpressions.
func (b *Binder) trackCodeFlowExpressions(callback func()) *common.OrderedSet[string] {
	savedExpressions := b.currentScopeCodeFlowExpressions
	b.currentScopeCodeFlowExpressions = common.NewOrderedSet[string]()
	callback()

	scopedExpressions := b.currentScopeCodeFlowExpressions

	if savedExpressions != nil {
		for _, value := range b.currentScopeCodeFlowExpressions.Values() {
			savedExpressions.Add(value)
		}
	}

	b.currentScopeCodeFlowExpressions = savedExpressions

	return scopedExpressions
}

// bindLoopStatement corresponds to _bindLoopStatement.
func (b *Binder) bindLoopStatement(preLoopLabel *FlowLabel, postLoopLabel *FlowLabel, callback func()) {
	savedContinueTarget := b.currentContinueTarget
	savedBreakTarget := b.currentBreakTarget

	b.currentContinueTarget = preLoopLabel
	b.currentBreakTarget = postLoopLabel

	preLoopLabel.AffectedExpressions = b.trackCodeFlowExpressions(callback)

	b.currentContinueTarget = savedContinueTarget
	b.currentBreakTarget = savedBreakTarget
}

// addAntecedent corresponds to _addAntecedent.
func (b *Binder) addAntecedent(label *FlowLabel, antecedent FlowNode) {
	if b.currentFlowNode.FlowBase().Flags&
		(FlowFlagsUnreachableStructural|FlowFlagsUnreachableStaticCondition) == 0 {
		// Don't add the same antecedent twice. The original compares ids, not
		// object identity.
		for _, existing := range label.Antecedents {
			if existing.FlowBase().ID == antecedent.FlowBase().ID {
				return
			}
		}
		label.Antecedents = append(label.Antecedents, antecedent)
	}
}
