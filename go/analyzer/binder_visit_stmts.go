/*
 * binder_visit_stmts.go
 *
 * The binder's statement visitors, transliterated from analyzer/binder.ts
 * (pyright 1.1.412): assignment, for, if, while, try, with, match, the
 * jump statements, and the global/nonlocal declarations.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// VisitAssignment corresponds to visitAssignment.
func (b *Binder) VisitAssignment(node *parser.AssignmentNode) bool {
	if b.handleTypingStubAssignmentOrAnnotation(node) {
		return false
	}

	b.bindPossibleTupleNamedTarget(node.D.LeftExpr, nil)

	if node.D.AnnotationComment != nil {
		b.Walk(node.D.AnnotationComment)
		b.addTypeDeclarationForVariable(node.D.LeftExpr, node.D.AnnotationComment)
	}

	if node.D.ChainedAnnotationComment != nil {
		b.addDiagnostic(
			DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.AnnotationNotSupported(),
			node.D.ChainedAnnotationComment.GetRange(),
		)
	}

	// If the assignment target base expression is potentially a TypedDict, add
	// the base expression to the flow expressions set to accommodate TypedDict
	// type narrowing.
	if target, ok := node.D.LeftExpr.(*parser.IndexNode); ok {
		if len(target.D.Items) == 1 &&
			!target.D.TrailingComma &&
			target.D.Items[0].D.ValueExpr.GetNodeType() == parser.ParseNodeTypeStringList {
			if IsCodeFlowSupportedForReference(target.D.LeftExpr) {
				b.currentScopeCodeFlowExpressions.Add(CreateKeyForReference(target.D.LeftExpr))
			}
		}
	}

	b.Walk(node.D.RightExpr)

	isPossibleTypeAlias := true
	if GetEnclosingFunction(node) != nil {
		// We will assume that type aliases are defined only at the module level
		// or as class variables, not as local variables within a function.
		isPossibleTypeAlias = false
	} else if node.D.RightExpr.GetNodeType() == parser.ParseNodeTypeCall && b.fileInfo.IsTypingStubFile {
		// Some special built-in types defined in typing.pyi use assignments of
		// the form List = _Alias(). We don't want to treat these as type
		// aliases.
		isPossibleTypeAlias = false
	} else if IsWithinLoop(node) {
		// Assume that it's not a type alias if it's within a loop.
		isPossibleTypeAlias = false
	}

	b.addInferredTypeAssignmentForVariable(node.D.LeftExpr, node.D.RightExpr, isPossibleTypeAlias)

	// If we didn't create assignment target flow nodes above, do so now.
	b.createAssignmentTargetFlowNodes(node.D.LeftExpr, true /* walkTargets */, false /* unbound */)

	// Is this an assignment to dunder all?
	//
	// Process __all__ for both Module and Builtin scope types. The Builtin
	// scope type is used when binding without a builtins scope (e.g., in
	// indexing scenarios where typeshed may not be available). We still want to
	// extract __all__ information to properly filter wildcard imports.
	if b.currentScope.Type == ScopeTypeModule || b.currentScope.Type == ScopeTypeBuiltin {
		if isAssignmentToName(node.D.LeftExpr, "__all__") {
			b.bindDunderAllAssignment(node)
		}
	}

	// Is this an assignment to dunder slots?
	if b.currentScope.Type == ScopeTypeClass {
		if isAssignmentToName(node.D.LeftExpr, "__slots__") {
			b.bindDunderSlotsAssignment(node)
		}
	}

	return false
}

// isAssignmentToName corresponds to the repeated test
// `(left is Name && left.d.value === x) || (left is TypeAnnotation && left.d.valueExpr is Name && ... === x)`.
func isAssignmentToName(leftExpr parser.ExpressionNode, name string) bool {
	if nameNode, ok := leftExpr.(*parser.NameNode); ok {
		return nameNode.D.Value == name
	}
	if annotation, ok := leftExpr.(*parser.TypeAnnotationNode); ok {
		if nameNode, ok := annotation.D.ValueExpr.(*parser.NameNode); ok {
			return nameNode.D.Value == name
		}
	}
	return false
}

// bindDunderAllAssignment is the `__all__ = ...` branch of visitAssignment.
func (b *Binder) bindDunderAllAssignment(node *parser.AssignmentNode) {
	expr := node.D.RightExpr
	// `this._dunderAllNames = []` -- an empty array, which is truthy, so every
	// later `?.` on it runs and BindModule takes the "__all__ was seen" path.
	b.dunderAllNames = []string{}
	b.dunderAllNamesSet = true
	emitDunderAllWarning := false

	collect := func(items []parser.ExpressionNode) {
		for _, entryNode := range items {
			if str, ok := singleStringOfList(entryNode); ok {
				b.dunderAllNames = append(b.dunderAllNames, str.D.Value.String())
				b.dunderAllStringNodes = append(b.dunderAllStringNodes, str)
			} else {
				emitDunderAllWarning = true
			}
		}
	}

	switch typed := expr.(type) {
	case *parser.ListNode:
		collect(typed.D.Items)
	case *parser.TupleNode:
		collect(typed.D.Items)
	default:
		emitDunderAllWarning = true
	}

	if emitDunderAllWarning {
		b.usesUnsupportedDunderAllForm = true

		b.addDiagnostic(
			DiagnosticRuleReportUnsupportedDunderAll,
			localization.LocMessage.UnsupportedDunderAllOperation(),
			node.GetRange(),
		)
	}
}

// bindDunderSlotsAssignment is the `__slots__ = ...` branch of visitAssignment.
func (b *Binder) bindDunderSlotsAssignment(node *parser.AssignmentNode) {
	expr := node.D.RightExpr
	b.dunderSlotsEntries = []*parser.StringListNode{}
	b.dunderSlotsEntriesSet = true
	isExpressionUnderstood := true

	collect := func(items []parser.ExpressionNode) {
		for _, entryNode := range items {
			if stringList, ok := entryNode.(*parser.StringListNode); ok && len(stringList.D.Strings) == 1 {
				if _, ok := stringList.D.Strings[0].(*parser.StringNode); ok {
					b.dunderSlotsEntries = append(b.dunderSlotsEntries, stringList)
					continue
				}
			}
			isExpressionUnderstood = false
		}
	}

	switch typed := expr.(type) {
	case *parser.StringListNode:
		// Note that unlike every other arm, this one does not require a single
		// String segment; `__slots__ = "a" "b"` and `__slots__ = f"{x}"` both
		// land here.
		b.dunderSlotsEntries = append(b.dunderSlotsEntries, typed)

	case *parser.ListNode:
		collect(typed.D.Items)

	case *parser.TupleNode:
		collect(typed.D.Items)

	case *parser.DictionaryNode:
		for _, dictionaryEntryNode := range typed.D.Items {
			keyEntry, ok := dictionaryEntryNode.(*parser.DictionaryKeyEntryNode)
			if ok {
				if keyExpr, ok := keyEntry.D.KeyExpr.(*parser.StringListNode); ok && len(keyExpr.D.Strings) == 1 {
					if _, ok := keyExpr.D.Strings[0].(*parser.StringNode); ok {
						b.dunderSlotsEntries = append(b.dunderSlotsEntries, keyExpr)
						continue
					}
				}
			}
			isExpressionUnderstood = false
		}

	default:
		isExpressionUnderstood = false
	}

	hasNonEmptySlots := false
	for _, entry := range b.dunderSlotsEntries {
		if stringOrFormatValue(entry.D.Strings[0]) != "__dict__" {
			hasNonEmptySlots = true
			break
		}
	}
	b.currentScope.SetHasNonEmptySlots(hasNonEmptySlots)

	if !isExpressionUnderstood {
		b.dunderSlotsEntries = nil
		b.dunderSlotsEntriesSet = false
	}
}

// VisitAssignmentExpression corresponds to visitAssignmentExpression.
func (b *Binder) VisitAssignmentExpression(node *parser.AssignmentExpressionNode) bool {
	// Temporarily disable true/false targets in case this assignment expression
	// is located within an if/else conditional.
	b.disableTrueFalseTargets(func() {
		// Evaluate the operand expression.
		b.Walk(node.D.RightExpr)
	})

	evaluationNode := GetEvaluationNodeForAssignmentExpression(node)
	if evaluationNode == nil {
		b.addSyntaxError(localization.LocMessage.AssignmentExprContext(), node.GetRange())
		b.Walk(node.D.Name)
	} else {
		// Bind the name to the containing scope. This special logic is required
		// because of the behavior defined in PEP 572. Targets of assignment
		// expressions don't bind to a list comprehension's scope but instead
		// bind to its containing scope.
		containerScope := GetScope(evaluationNode)

		// If we're in a list comprehension (possibly nested), make sure that
		// local for targets don't collide with the target of the assignment
		// expression.
		curScope := b.currentScope
		for curScope != nil && curScope != containerScope {
			localSymbol := curScope.LookUpSymbol(node.D.Name.D.Value)
			if localSymbol != nil {
				b.addSyntaxError(
					localization.LocMessage.AssignmentExprComprehension().Format(node.D.Name.D.Value),
					node.D.Name.GetRange(),
				)
				break
			}

			curScope = curScope.Parent
		}

		b.bindNameToScope(containerScope, node.D.Name, nil)
		b.addInferredTypeAssignmentForVariable(node.D.Name, node.D.RightExpr, false)
		b.createAssignmentTargetFlowNodes(node.D.Name, true /* walkTargets */, false /* unbound */)
	}

	return false
}

// VisitAugmentedAssignment corresponds to visitAugmentedAssignment.
func (b *Binder) VisitAugmentedAssignment(node *parser.AugmentedAssignmentNode) bool {
	b.Walk(node.D.LeftExpr)
	b.Walk(node.D.RightExpr)

	b.bindPossibleTupleNamedTarget(node.D.DestExpr, nil)
	b.createAssignmentTargetFlowNodes(node.D.DestExpr, false /* walkTargets */, false /* unbound */)

	b.addInferredTypeAssignmentForVariable(node.D.DestExpr, node.D.RightExpr, false)

	// Is this an assignment to dunder all of the form __all__ += <expression>?
	leftName, isName := node.D.LeftExpr.(*parser.NameNode)
	if node.D.Operator == parser.OperatorTypeAddEqual &&
		b.currentScope.Type == ScopeTypeModule &&
		isName &&
		leftName.D.Value == "__all__" {
		expr := node.D.RightExpr
		emitDunderAllWarning := true

		if list, ok := expr.(*parser.ListNode); ok {
			// Is this the form __all__ += ["a", "b"]?
			for _, listEntryNode := range list.D.Items {
				if str, ok := singleStringOfList(listEntryNode); ok {
					b.appendDunderAllName(str)
				}
			}
			emitDunderAllWarning = false
		} else if member, ok := expr.(*parser.MemberAccessNode); ok &&
			member.D.LeftExpr.GetNodeType() == parser.ParseNodeTypeName &&
			member.D.Member.D.Value == "__all__" {
			// Is this using the form "__all__ += <mod>.__all__"?
			namesToAdd := b.getDunderAllNamesFromImport(member.D.LeftExpr.(*parser.NameNode).D.Value)
			if namesToAdd != nil {
				for _, name := range namesToAdd {
					b.pushDunderAllName(name)
				}

				emitDunderAllWarning = false
			}
		}

		if emitDunderAllWarning {
			b.usesUnsupportedDunderAllForm = true

			b.addDiagnostic(
				DiagnosticRuleReportUnsupportedDunderAll,
				localization.LocMessage.UnsupportedDunderAllOperation(),
				node.GetRange(),
			)
		}
	}

	return false
}

// VisitDel corresponds to visitDel.
func (b *Binder) VisitDel(node *parser.DelNode) bool {
	for _, expr := range node.D.Targets {
		b.bindPossibleTupleNamedTarget(expr, nil)
		b.Walk(expr)
		b.createAssignmentTargetFlowNodes(expr, false /* walkTargets */, true /* unbound */)
	}

	return false
}

// VisitTypeAnnotation corresponds to visitTypeAnnotation.
func (b *Binder) VisitTypeAnnotation(node *parser.TypeAnnotationNode) bool {
	if b.handleTypingStubAssignmentOrAnnotation(node) {
		return false
	}

	// If this is an annotated variable assignment within a class body, we need
	// to evaluate the type annotation first.
	bindVariableBeforeAnnotationEvaluation := false
	if parent := node.NodeBase().Parent; parent != nil && parent.GetNodeType() == parser.ParseNodeTypeAssignment {
		bindVariableBeforeAnnotationEvaluation = GetEnclosingClass(node, true /* stopAtFunction */) != nil
	}

	if !bindVariableBeforeAnnotationEvaluation {
		b.Walk(node.D.Annotation)
	}

	b.createVariableAnnotationFlowNode()

	b.bindPossibleTupleNamedTarget(node.D.ValueExpr, nil)
	b.addTypeDeclarationForVariable(node.D.ValueExpr, node.D.Annotation)

	if bindVariableBeforeAnnotationEvaluation {
		b.Walk(node.D.Annotation)
	}

	// For type annotations that are not part of assignments (e.g. simple
	// variable annotations), we need to populate the reference map. Otherwise
	// the type analyzer's code flow engine won't run and detect cases where the
	// variable is unbound.
	expressionList := []CodeFlowReferenceExpressionNode{}
	if b.isNarrowingExpression(node.D.ValueExpr, &expressionList, narrowExprOptions{}) {
		for _, expr := range expressionList {
			b.currentScopeCodeFlowExpressions.Add(CreateKeyForReference(expr))
		}
	}

	b.Walk(node.D.ValueExpr)

	return false
}

// VisitFor corresponds to visitFor.
func (b *Binder) VisitFor(node *parser.ForNode) bool {
	b.bindPossibleTupleNamedTarget(node.D.TargetExpr, nil)
	b.addInferredTypeAssignmentForVariable(node.D.TargetExpr, node, false)

	b.Walk(node.D.IterableExpr)

	preForLabel := b.createLoopLabel()
	preElseLabel := b.createBranchLabel(nil)
	postForLabel := b.createBranchLabel(nil)

	// Determine if this loop is guaranteed to execute at least once.
	isGuaranteedToExecute := b.isNonEmptyListOrTupleLiteral(node.D.IterableExpr)

	b.addAntecedent(preForLabel, b.currentFlowNode)
	b.currentFlowNode = preForLabel

	// Only add zero-iteration path for potentially-empty iterables.
	if !isGuaranteedToExecute {
		b.addAntecedent(&preElseLabel.FlowLabel, b.currentFlowNode)
	}

	targetExpressions := b.trackCodeFlowExpressions(func() {
		b.createAssignmentTargetFlowNodes(node.D.TargetExpr, true /* walkTargets */, false /* unbound */)
	})

	// Record antecedent count before the loop body to detect continue
	// back-edges.
	preBodyAntecedentCount := len(preForLabel.Antecedents)

	b.bindLoopStatement(preForLabel, &postForLabel.FlowLabel, func() {
		b.Walk(node.D.ForSuite)
		b.addAntecedent(preForLabel, b.currentFlowNode)

		// Add any target expressions since they are modified in the loop.
		for _, value := range targetExpressions.Values() {
			if b.currentScopeCodeFlowExpressions != nil {
				b.currentScopeCodeFlowExpressions.Add(value)
			}
		}
	})

	// For guaranteed loops, add post-body exit path to preElseLabel.
	// When currentFlowNode is reachable (normal completion or conditional
	// break), use it directly -- it carries the post-body type state.
	// When currentFlowNode is unreachable (all paths end with
	// break/continue/return/raise), we must distinguish the cause:
	//   - All break: preElseLabel gets nothing. Python's else doesn't run after
	//     break, and break already sent the assigned-state to postForLabel.
	//   - All continue: preForLabel accumulated continue back-edges. Use it as
	//     an approximation for the loop-completion state feeding into else.
	//   - All return/raise: preElseLabel gets nothing. Post-loop is
	//     unreachable.
	//   - Mix with continue: if any continues occurred, use preForLabel for the
	//     else path.
	if isGuaranteedToExecute {
		if b.currentFlowNode.FlowBase().Flags&
			(FlowFlagsUnreachableStructural|FlowFlagsUnreachableStaticCondition) != 0 {
			// Check if any continue statements added back-edges to preForLabel.
			hasContinueBackEdges := len(preForLabel.Antecedents) > preBodyAntecedentCount

			if hasContinueBackEdges {
				// Some paths continued -- use preForLabel (with accumulated
				// continue state) as the else-clause antecedent.
				//
				// The temporary reassignment of currentFlowNode is load
				// bearing: _addAntecedent tests the *current* flow node for
				// reachability, not the antecedent it is given.
				savedFlowNode := b.currentFlowNode
				b.currentFlowNode = preForLabel
				b.addAntecedent(&preElseLabel.FlowLabel, preForLabel)
				b.currentFlowNode = savedFlowNode
			}
			// Otherwise (all break / all return / all raise): preElseLabel gets
			// no antecedent. For break, the flow already reached postForLabel
			// directly. For return/raise, post-loop code is unreachable.
		} else {
			// Normal completion or conditional break -- use current flow node.
			b.addAntecedent(&preElseLabel.FlowLabel, b.currentFlowNode)
		}
	}

	b.currentFlowNode = b.finishBranchLabel(preElseLabel)
	if node.D.ElseSuite != nil {
		b.Walk(node.D.ElseSuite)
	}
	b.addAntecedent(&postForLabel.FlowLabel, b.currentFlowNode)

	b.currentFlowNode = b.finishBranchLabel(postForLabel)

	// Async for is not allowed outside of an async function unless we're in
	// ipython mode.
	if node.D.AsyncToken != nil && b.fileInfo.IPythonMode == IPythonModeNone {
		enclosingFunction := GetEnclosingFunction(node)
		if enclosingFunction == nil || !enclosingFunction.D.IsAsync {
			b.addSyntaxError(localization.LocMessage.AsyncNotInAsyncFunction(), node.D.AsyncToken.GetRange())
		}
	}

	return false
}

// VisitContinue corresponds to visitContinue.
func (b *Binder) VisitContinue(node *parser.ContinueNode) bool {
	if b.currentContinueTarget != nil {
		b.addAntecedent(b.currentContinueTarget, b.currentFlowNode)
	}
	b.currentFlowNode = unreachableStructuralFlowNode

	// Continue nodes don't have any children.
	return false
}

// VisitBreak corresponds to visitBreak.
func (b *Binder) VisitBreak(node *parser.BreakNode) bool {
	if b.currentBreakTarget != nil {
		b.addAntecedent(b.currentBreakTarget, b.currentFlowNode)
	}
	b.currentFlowNode = unreachableStructuralFlowNode

	// Break nodes don't have any children.
	return false
}

// VisitReturn corresponds to visitReturn.
func (b *Binder) VisitReturn(node *parser.ReturnNode) bool {
	if b.targetFunctionDeclaration != nil {
		// The original lazily allocates the array; a nil Go slice appends the
		// same way, so the guard is dropped. The differential dumps an absent
		// array and an empty one identically.
		b.targetFunctionDeclaration.ReturnStatements = append(b.targetFunctionDeclaration.ReturnStatements, node)
	}

	if node.D.Expr != nil {
		SetFlowNode(node.D.Expr, b.currentFlowNode)
		b.Walk(node.D.Expr)
	}

	SetFlowNode(node, b.currentFlowNode)
	if b.currentReturnTarget != nil {
		b.addAntecedent(b.currentReturnTarget, b.currentFlowNode)
	}
	for _, target := range b.finallyTargets {
		b.addAntecedent(target, b.currentFlowNode)
	}
	b.currentFlowNode = unreachableStructuralFlowNode
	return false
}

// VisitYield corresponds to visitYield.
func (b *Binder) VisitYield(node *parser.YieldNode) bool {
	if b.isInComprehension(node, true /* ignoreOutermostIterable */) {
		b.addSyntaxError(localization.LocMessage.YieldWithinComprehension(), node.GetRange())
	}

	b.bindYield(node, nil)
	return false
}

// VisitYieldFrom corresponds to visitYieldFrom.
func (b *Binder) VisitYieldFrom(node *parser.YieldFromNode) bool {
	if b.isInComprehension(node, true /* ignoreOutermostIterable */) {
		b.addSyntaxError(localization.LocMessage.YieldWithinComprehension(), node.GetRange())
	}

	b.bindYield(nil, node)
	return false
}

// VisitMemberAccess corresponds to visitMemberAccess.
func (b *Binder) VisitMemberAccess(node *parser.MemberAccessNode) bool {
	b.Walk(node.D.LeftExpr)
	SetFlowNode(node, b.currentFlowNode)
	return false
}

// VisitName corresponds to visitName.
func (b *Binder) VisitName(node *parser.NameNode) bool {
	SetFlowNode(node, b.currentFlowNode)
	return false
}

// VisitIndex corresponds to visitIndex.
func (b *Binder) VisitIndex(node *parser.IndexNode) bool {
	SetFlowNode(node, b.currentFlowNode)

	b.Walk(node.D.LeftExpr)

	// If we're within an 'Annotated' type annotation, set the flag.
	wasInAnnotatedAnnotation := b.isInAnnotatedAnnotation
	if b.isTypingAnnotation(node.D.LeftExpr, "Annotated") {
		b.isInAnnotatedAnnotation = true
	}

	for _, argNode := range node.D.Items {
		b.Walk(argNode)
	}

	b.isInAnnotatedAnnotation = wasInAnnotatedAnnotation

	return false
}

// VisitIf corresponds to visitIf.
func (b *Binder) VisitIf(node *parser.IfNode) bool {
	preIfFlowNode := b.currentFlowNode
	thenLabel := b.createBranchLabel(nil)
	elseLabel := b.createBranchLabel(nil)
	postIfLabel := b.createBranchLabel(preIfFlowNode)

	postIfLabel.AffectedExpressions = b.trackCodeFlowExpressions(func() {
		// Determine if the test condition is always true or always false. If
		// so, we can treat either the then or the else clause as
		// unconditional.
		constExprValue, constExprSet := EvaluateStaticBoolLikeExpression(
			node.D.TestExpr,
			b.fileInfo.ExecutionEnvironment,
			b.fileInfo.DefinedConstants,
			b.typingImportAliases,
			b.sysImportAliases,
		)
		SetStaticConditionValue(node, optionalBool(constExprValue, constExprSet))

		b.bindConditional(node.D.TestExpr, &thenLabel.FlowLabel, &elseLabel.FlowLabel)

		// Handle the if clause.
		if constExprSet && !constExprValue {
			b.currentFlowNode = unreachableStaticConditionFlowNode
		} else {
			b.currentFlowNode = b.finishBranchLabel(thenLabel)
		}
		b.Walk(node.D.IfSuite)
		b.addAntecedent(&postIfLabel.FlowLabel, b.currentFlowNode)

		// Now handle the else clause if it's present. If there are chained
		// "else if" statements, they'll be handled recursively here.
		if constExprSet && constExprValue {
			b.currentFlowNode = unreachableStaticConditionFlowNode
		} else {
			b.currentFlowNode = b.finishBranchLabel(elseLabel)
		}
		if node.D.ElseSuite != nil {
			b.Walk(node.D.ElseSuite)
		} else {
			b.bindNeverCondition(node.D.TestExpr, &postIfLabel.FlowLabel, false /* isPositiveTest */)
		}
		b.addAntecedent(&postIfLabel.FlowLabel, b.currentFlowNode)
		b.currentFlowNode = b.finishBranchLabel(postIfLabel)
	})

	return false
}

// VisitWhile corresponds to visitWhile.
func (b *Binder) VisitWhile(node *parser.WhileNode) bool {
	thenLabel := b.createBranchLabel(nil)
	elseLabel := b.createBranchLabel(nil)
	postWhileLabel := b.createBranchLabel(nil)

	// Determine if the test condition is always true or always false. If so, we
	// can treat either the while or the else clause as unconditional.
	constExprValue, constExprSet := EvaluateStaticBoolLikeExpression(
		node.D.TestExpr,
		b.fileInfo.ExecutionEnvironment,
		b.fileInfo.DefinedConstants,
		b.typingImportAliases,
		b.sysImportAliases,
	)

	preLoopLabel := b.createLoopLabel()
	b.addAntecedent(preLoopLabel, b.currentFlowNode)
	b.currentFlowNode = preLoopLabel

	b.bindConditional(node.D.TestExpr, &thenLabel.FlowLabel, &elseLabel.FlowLabel)

	// Handle the while clause.
	if constExprSet && !constExprValue {
		b.currentFlowNode = unreachableStaticConditionFlowNode
	} else {
		b.currentFlowNode = b.finishBranchLabel(thenLabel)
	}
	b.bindLoopStatement(preLoopLabel, &postWhileLabel.FlowLabel, func() {
		b.Walk(node.D.WhileSuite)
	})
	b.addAntecedent(preLoopLabel, b.currentFlowNode)

	if constExprSet && constExprValue {
		b.currentFlowNode = unreachableStaticConditionFlowNode
	} else {
		b.currentFlowNode = b.finishBranchLabel(elseLabel)
	}
	if node.D.ElseSuite != nil {
		b.Walk(node.D.ElseSuite)
	}
	b.addAntecedent(&postWhileLabel.FlowLabel, b.currentFlowNode)
	b.currentFlowNode = b.finishBranchLabel(postWhileLabel)
	return false
}

// VisitAssert corresponds to visitAssert.
func (b *Binder) VisitAssert(node *parser.AssertNode) bool {
	assertTrueLabel := b.createBranchLabel(nil)
	assertFalseLabel := b.createBranchLabel(nil)

	b.bindConditional(node.D.TestExpr, &assertTrueLabel.FlowLabel, &assertFalseLabel.FlowLabel)

	if node.D.ExceptionExpr != nil {
		b.currentFlowNode = b.finishBranchLabel(assertFalseLabel)
		b.Walk(node.D.ExceptionExpr)
	}

	b.currentFlowNode = b.finishBranchLabel(assertTrueLabel)
	return false
}

// VisitExcept corresponds to visitExcept.
func (b *Binder) VisitExcept(node *parser.ExceptNode) bool {
	if node.D.TypeExpr != nil {
		b.Walk(node.D.TypeExpr)
	}

	if node.D.Name != nil {
		b.Walk(node.D.Name)
		symbol := b.bindNameToScope(b.currentScope, node.D.Name, nil)
		b.createAssignmentTargetFlowNodes(node.D.Name, true /* walkTargets */, false /* unbound */)

		if symbol != nil {
			symbol.AddDeclaration(&VariableDeclaration{
				DeclarationBase: DeclarationBase{
					Type:            DeclarationTypeVariable,
					Node:            node.D.Name,
					Uri:             b.fileInfo.FileUri,
					Range:           common.ConvertTextRangeToRange(node.D.Name.GetRange(), b.fileInfo.Lines),
					ModuleName:      b.fileInfo.ModuleName,
					IsInExceptSuite: b.isInExceptSuite,
				},
				IsConstant:         IsConstantName(node.D.Name.D.Value),
				InferredTypeSource: node,
				IsExplicitBinding:  b.currentScope.GetBindingType(node.D.Name.D.Value) != NameBindingTypeNone,
			})
		}
	}

	wasInExceptSuite := b.isInExceptSuite
	b.isInExceptSuite = true
	b.Walk(node.D.ExceptSuite)
	b.isInExceptSuite = wasInExceptSuite

	if node.D.Name != nil {
		// The exception name is implicitly unbound at the end of the except
		// block.
		b.createFlowAssignment(node.D.Name, true /* unbound */)
	}

	return false
}

// VisitRaise corresponds to visitRaise.
func (b *Binder) VisitRaise(node *parser.RaiseNode) bool {
	if b.currentFlowNode != nil {
		b.addExceptTargets(b.currentFlowNode)
	}

	if b.targetFunctionDeclaration != nil {
		b.targetFunctionDeclaration.RaiseStatements = append(b.targetFunctionDeclaration.RaiseStatements, node)
	}

	if node.D.Expr != nil {
		b.Walk(node.D.Expr)
	}
	if node.D.FromExpr != nil {
		b.Walk(node.D.FromExpr)
	}

	for _, target := range b.finallyTargets {
		b.addAntecedent(target, b.currentFlowNode)
	}

	b.currentFlowNode = unreachableStructuralFlowNode
	return false
}

// VisitTry corresponds to visitTry.
//
// The original's comment, kept because it is the only description of the model:
//
// The try/except/else/finally statement is tricky to model using static code
// flow rules because the finally clause is executed regardless of whether an
// exception is raised or a return statement is executed. Code within the finally
// clause needs to be reachable always, and we conservatively assume that any
// statement within the try block can generate an exception, so we assume that
// its antecedent is the pre-try flow. We implement this with a "gate" node in
// the control flow graph. If analysis starts within the finally clause, the gate
// is opened, and all raise/return statements within try/except/else blocks are
// considered antecedents. If analysis starts outside (after) the finally clause,
// the gate is closed, and only paths that don't hit a raise/return statement in
// try/except/else blocks are considered.
//
//  1. PostElse
//     ^
//     |
//  3. TryExceptElseReturnOrExcept     |
//     ^                            |
//     |                            |     2. PostExcept (for each except)
//     |                            |            ^
//  4. ReturnOrRaiseLabel              |            |
//     ^                            |            |
//     |                            |   |---------
//  5. PreFinallyGate                  |   |
//     ^                            |   |
//     |------------------          |   |
//     |          |   |
//  6. PreFinallyLabel
//     ^
//     (finally block)
//     ^
//  7. PostFinally
//     ^    (only if isAfterElseAndExceptsReachable)
//     (after finally)
func (b *Binder) VisitTry(node *parser.TryNode) bool {
	// Create one flow label for every except clause.
	preTryFlowNode := b.currentFlowNode
	curExceptTargets := make([]*FlowLabel, 0, len(node.D.ExceptClauses))
	for range node.D.ExceptClauses {
		curExceptTargets = append(curExceptTargets, &b.createBranchLabel(nil).FlowLabel)
	}
	preFinallyLabel := b.createBranchLabel(preTryFlowNode)
	isAfterElseAndExceptsReachable := false

	// Create a label for all of the return or raise labels that are encountered
	// within the try/except/else blocks. This conditionally connects the
	// return/raise statement to the finally clause.
	preFinallyReturnOrRaiseLabel := b.createBranchLabel(preTryFlowNode)

	preFinallyGate := &FlowPreFinallyGate{
		FlowNodeBase: FlowNodeBase{
			Flags: FlowFlagsPreFinallyGate,
			ID:    b.getUniqueFlowNodeID(),
		},
		Antecedent: preFinallyReturnOrRaiseLabel,
	}

	preFinallyLabel.AffectedExpressions = b.trackCodeFlowExpressions(func() {
		if node.D.FinallySuite != nil {
			b.addAntecedent(&preFinallyLabel.FlowLabel, preFinallyGate)
		}

		// Add the finally target as an exception target unless there is a
		// "bare" except clause that accepts all exception types.
		hasBareExceptClause := false
		for _, except := range node.D.ExceptClauses {
			if except.D.TypeExpr == nil {
				hasBareExceptClause = true
				break
			}
		}
		if !hasBareExceptClause {
			curExceptTargets = append(curExceptTargets, &preFinallyReturnOrRaiseLabel.FlowLabel)
		}

		// An exception may be generated before the first flow node added by the
		// try block, so all of the exception targets must have the pre-try flow
		// node as an antecedent.
		for _, exceptLabel := range curExceptTargets {
			b.addAntecedent(exceptLabel, b.currentFlowNode)
		}

		// We don't perfectly handle nested finally clauses, which are not
		// possible to model fully within a static analyzer, but we do handle a
		// single level of finally statements, and we handle most cases
		// involving nesting. Returns or raises within the try/except/raise
		// block will execute the finally target(s).
		if node.D.FinallySuite != nil {
			b.finallyTargets = append(b.finallyTargets, &preFinallyReturnOrRaiseLabel.FlowLabel)
		}

		// Handle the try block.
		b.useExceptTargets(curExceptTargets, func() {
			b.Walk(node.D.TrySuite)
		})

		// Handle the else block, which is executed only if execution falls
		// through the try block.
		if node.D.ElseSuite != nil {
			b.Walk(node.D.ElseSuite)
		}
		b.addAntecedent(&preFinallyLabel.FlowLabel, b.currentFlowNode)
		if !b.isCodeUnreachable() {
			isAfterElseAndExceptsReachable = true
		}

		// Handle the except blocks.
		for index, exceptNode := range node.D.ExceptClauses {
			b.currentFlowNode = b.finishFlowLabel(curExceptTargets[index], curExceptTargets[index])
			b.Walk(exceptNode)
			b.addAntecedent(&preFinallyLabel.FlowLabel, b.currentFlowNode)
			if !b.isCodeUnreachable() {
				isAfterElseAndExceptsReachable = true
			}
		}

		if node.D.FinallySuite != nil {
			b.finallyTargets = b.finallyTargets[:len(b.finallyTargets)-1]
		}

		// Handle the finally block.
		b.currentFlowNode = b.finishBranchLabel(preFinallyLabel)
	})

	if node.D.FinallySuite != nil {
		b.Walk(node.D.FinallySuite)

		// Add a post-finally node at the end. If we traverse this node, we'll
		// set the "ignore" flag in the pre-finally node.
		postFinallyNode := &FlowPostFinally{
			FlowNodeBase: FlowNodeBase{
				Flags: FlowFlagsPostFinally,
				ID:    b.getUniqueFlowNodeID(),
			},
			FinallyNode:    node.D.FinallySuite,
			Antecedent:     b.currentFlowNode,
			PreFinallyGate: preFinallyGate,
		}
		if isAfterElseAndExceptsReachable {
			b.currentFlowNode = postFinallyNode
		} else {
			b.currentFlowNode = unreachableStructuralFlowNode
		}
	}

	return false
}

// VisitAwait corresponds to visitAwait.
func (b *Binder) VisitAwait(node *parser.AwaitNode) bool {
	// Make sure this is within an async lambda or function.
	execScopeNode := GetExecutionScopeNode(node)
	execFunction, isFunction := execScopeNode.(*parser.FunctionNode)

	if !isFunction || !execFunction.D.IsAsync {
		if b.fileInfo.IPythonMode != IPythonModeNone &&
			execScopeNode != nil && execScopeNode.GetNodeType() == parser.ParseNodeTypeModule {
			// Top level await is allowed in ipython mode.
			return true
		}

		// `node.parent?.parent?.nodeType !== X` is true when the grandparent is
		// undefined, so an absent grandparent counts as "not a list/set/dict".
		isInGenerator := false
		if parent := node.NodeBase().Parent; parent != nil &&
			parent.GetNodeType() == parser.ParseNodeTypeComprehension {
			grandparentType := parser.ParseNodeTypeError
			hasGrandparent := false
			if grandparent := parent.NodeBase().Parent; grandparent != nil {
				grandparentType = grandparent.GetNodeType()
				hasGrandparent = true
			}
			isInGenerator = !hasGrandparent ||
				(grandparentType != parser.ParseNodeTypeList &&
					grandparentType != parser.ParseNodeTypeSet &&
					grandparentType != parser.ParseNodeTypeDictionary)
		}

		// Allow if it's within a generator expression. Execution of generator
		// expressions is deferred and therefore can be run within the context
		// of an async function later.
		if !isInGenerator {
			b.addSyntaxError(localization.LocMessage.AwaitNotInAsync(), node.D.AwaitToken.GetRange())
		}
	}

	return true
}

// VisitGlobal corresponds to visitGlobal.
func (b *Binder) VisitGlobal(node *parser.GlobalNode) bool {
	globalScope := b.currentScope.GetGlobalScope().Scope

	for _, name := range node.D.Targets {
		nameValue := name.D.Value

		// Is the binding inconsistent?
		if b.currentScope.GetBindingType(nameValue) == NameBindingTypeNonlocal {
			b.addSyntaxError(
				localization.LocMessage.NonLocalRedefinition().Format(nameValue),
				name.GetRange(),
			)
		}

		valueWithScope := b.currentScope.LookUpSymbolRecursive(nameValue, nil)

		// Was the name already assigned within this scope before it was
		// declared global?
		if valueWithScope != nil && valueWithScope.Scope == b.currentScope {
			b.addSyntaxError(
				localization.LocMessage.GlobalReassignment().Format(nameValue),
				name.GetRange(),
			)
		}

		// Add it to the global scope if it's not already added.
		b.bindNameToScope(globalScope, name, nil)

		if b.currentScope != globalScope {
			b.currentScope.SetBindingType(nameValue, NameBindingTypeGlobal)
		}
	}

	return true
}

// VisitNonlocal corresponds to visitNonlocal.
func (b *Binder) VisitNonlocal(node *parser.NonlocalNode) bool {
	globalScope := b.currentScope.GetGlobalScope().Scope

	if b.currentScope == globalScope {
		b.addSyntaxError(localization.LocMessage.NonLocalInModule(), node.GetRange())
	} else {
		for _, name := range node.D.Targets {
			nameValue := name.D.Value

			// Is the binding inconsistent?
			if b.currentScope.GetBindingType(nameValue) == NameBindingTypeGlobal {
				b.addSyntaxError(
					localization.LocMessage.GlobalRedefinition().Format(nameValue),
					name.GetRange(),
				)
			}

			valueWithScope := b.currentScope.LookUpSymbolRecursive(nameValue, nil)

			// Was the name already assigned within this scope before it was
			// declared nonlocal?
			if valueWithScope != nil && valueWithScope.Scope == b.currentScope {
				b.addSyntaxError(
					localization.LocMessage.NonLocalReassignment().Format(nameValue),
					name.GetRange(),
				)
			} else if valueWithScope == nil || valueWithScope.Scope == globalScope {
				b.addSyntaxError(
					localization.LocMessage.NonLocalNoBinding().Format(nameValue),
					name.GetRange(),
				)
			}

			if valueWithScope != nil {
				b.currentScope.SetBindingType(nameValue, NameBindingTypeNonlocal)
			}
		}
	}

	return true
}

// optionalBool packs a (value, ok) pair back into the `boolean | undefined`
// the analyzer node info stores.
func optionalBool(value bool, ok bool) *bool {
	if !ok {
		return nil
	}
	return &value
}
