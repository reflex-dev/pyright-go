/*
 * codeflowengine_constrained.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/codeFlowEngine.ts (pyright 1.1.412):
 * narrowConstrainedTypeVar and isCompatibleWithConstrainedTypeVar.
 *
 * A *constrained* TypeVar -- `T = TypeVar("T", int, str)` -- is not narrowed the
 * way an ordinary type is. Inside a generic function T stands for exactly one of
 * its constraints, chosen per call, and an `isinstance(x, int)` guard on a value
 * of type T therefore does not narrow the value: it narrows *which constraint T
 * could be* on that branch. That is a different question from ordinary narrowing
 * and needs its own walk, which is what this is.
 *
 * The walk runs backwards from a flow node accumulating the constraints still
 * possible, and answers only when exactly one survives -- because only then is T
 * pinned. Two survivors means the branch is reachable under either.
 *
 * Two constructs narrow the constraint set: an `isinstance` call and a `case`
 * with a class pattern. Both are checked against isCompatibleWithConstrainedTypeVar
 * first, which asks whether the tested expression is actually *this* TypeVar --
 * either literally or as a type conditioned on it. Without that check an
 * unrelated isinstance in the same function would narrow T.
 *
 * visitedFlowNodeMap is added and then *removed* around each recursive descent
 * rather than being left set. It is a cycle guard for the current path only; a
 * node reachable by two different paths must be analyzed on both.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// narrowConstrainedTypeVar corresponds to the codeFlowEngine.ts function of the
// same name. It returns nil where the original returns undefined.
func (e *typeEvaluator) narrowConstrainedTypeVarForFlowNode(
	flowNode FlowNode, typeVar *TypeVarType,
) *ClassType {
	startingConstraints := []*ClassType{}

	for _, constraint := range typeVar.Shared.Constraints {
		if IsClassInstance(constraint) {
			startingConstraints = append(startingConstraints, constraint.(*ClassType))
			continue
		}
		// The original's comment: if one or more constraints are Unknown, Any,
		// union types, etc., we can't narrow them.
		return nil
	}

	w := &constrainedTypeVarWalker{
		evaluator:           e,
		typeVar:             typeVar,
		startingConstraints: startingConstraints,
		visitedFlowNodeMap:  map[int]bool{},
	}

	narrowedConstrainedType := w.walk(flowNode)

	// The original's comment: have we narrowed the typeVar to a single
	// constraint?
	if len(narrowedConstrainedType) == 1 {
		return narrowedConstrainedType[0]
	}
	return nil
}

// constrainedTypeVarWalker carries what the original's inner recursive function
// closes over.
type constrainedTypeVarWalker struct {
	evaluator           *typeEvaluator
	typeVar             *TypeVarType
	startingConstraints []*ClassType
	visitedFlowNodeMap  map[int]bool
}

// walk corresponds to narrowConstrainedTypeVarRecursive.
func (w *constrainedTypeVarWalker) walk(flowNode FlowNode) []*ClassType {
	curFlowNode := flowNode

	for {
		base := curFlowNode.FlowBase()

		if w.visitedFlowNodeMap[base.ID] {
			return w.startingConstraints
		}

		if base.Flags&(FlowFlagsUnreachableStaticCondition|
			FlowFlagsUnreachableStructural|FlowFlagsStart) != 0 {
			return w.startingConstraints
		}

		// These node kinds say nothing about which constraint T holds, so the walk
		// simply steps past them.
		if base.Flags&(FlowFlagsVariableAnnotation|FlowFlagsAssignment|
			FlowFlagsWildcardImport|FlowFlagsTrueNeverCondition|FlowFlagsFalseNeverCondition|
			FlowFlagsExhaustedMatch|FlowFlagsPostFinally|FlowFlagsPreFinallyGate|
			FlowFlagsCall) != 0 {
			antecedent := constrainedAntecedentOf(curFlowNode)
			if antecedent == nil {
				return w.startingConstraints
			}
			curFlowNode = antecedent
			continue
		}

		// The original's comment: handle a case statement with a class pattern.
		if base.Flags&FlowFlagsNarrowForPattern != 0 {
			narrowForPatternFlowNode := curFlowNode.(*FlowNarrowForPattern)
			if result, handled := w.narrowForPattern(narrowForPatternFlowNode); handled {
				return result
			}

			curFlowNode = narrowForPatternFlowNode.Antecedent
			continue
		}

		// The original's comment: handle an isinstance type guard.
		if base.Flags&(FlowFlagsTrueCondition|FlowFlagsFalseCondition) != 0 {
			conditionFlowNode := curFlowNode.(*FlowCondition)
			isPositiveTest := base.Flags&FlowFlagsTrueCondition != 0

			if result, handled := w.narrowForIsinstance(conditionFlowNode, isPositiveTest); handled {
				return result
			}

			curFlowNode = conditionFlowNode.Antecedent
			continue
		}

		if base.Flags&(FlowFlagsBranchLabel|FlowFlagsLoopLabel) != 0 {
			return w.narrowForLabel(labelOf(curFlowNode))
		}

		// The original's comment: we shouldn't get here. The original calls
		// fail(); the port returns the release-build answer rather than aborting.
		return w.startingConstraints
	}
}

// narrowForPattern is the original's NarrowForPattern arm. The second result
// reports that it produced an answer rather than continuing the walk.
func (w *constrainedTypeVarWalker) narrowForPattern(
	node *FlowNarrowForPattern,
) ([]*ClassType, bool) {
	caseNode, ok := node.Statement.(*parser.CaseNode)
	if !ok {
		return nil, false
	}

	subjectType := w.evaluator.GetTypeOfExpression(node.SubjectExpression, EvalFlagsNone, nil).Type

	if !isCompatibleWithConstrainedTypeVar(subjectType, w.typeVar) {
		return nil, false
	}

	patternNode, ok := caseNode.D.Pattern.(*parser.PatternAsNode)
	if !ok || len(patternNode.D.OrPatterns) != 1 {
		return nil, false
	}

	classPatternNode, ok := patternNode.D.OrPatterns[0].(*parser.PatternClassNode)
	if !ok {
		return nil, false
	}

	classType := w.evaluator.GetTypeOfExpression(
		classNameExpr(classPatternNode.D.ClassName), EvalFlagsCallBaseDefaults, nil).Type

	if !IsInstantiableClass(classType) {
		return nil, false
	}

	priorRemainingConstraints := w.walk(node.Antecedent)

	remaining := []*ClassType{}
	for _, subtype := range priorRemainingConstraints {
		if ClassTypeIsSameGenericClass(subtype,
			ClassTypeCloneAsInstance(classType.(*ClassType), false), 0) {
			remaining = append(remaining, subtype)
		}
	}
	return remaining, true
}

// narrowForIsinstance is the original's TrueCondition/FalseCondition arm.
func (w *constrainedTypeVarWalker) narrowForIsinstance(
	node *FlowCondition, isPositiveTest bool,
) ([]*ClassType, bool) {
	testExpression, ok := node.Expression.(*parser.CallNode)
	if !ok {
		return nil, false
	}
	if testExpression.D.LeftExpr.GetNodeType() != parser.ParseNodeTypeName ||
		testExpression.D.LeftExpr.(*parser.NameNode).D.Value != "isinstance" ||
		len(testExpression.D.Args) != 2 {
		return nil, false
	}

	arg0Expr := testExpression.D.Args[0].D.ValueExpr
	arg0Type := w.evaluator.GetTypeOfExpression(arg0Expr, EvalFlagsNone, nil).Type

	if !isCompatibleWithConstrainedTypeVar(arg0Type, w.typeVar) {
		return nil, false
	}

	// The original's comment: prevent infinite recursion by noting that we've
	// been here before. It is removed again afterwards -- this guards the current
	// path, not the node globally.
	w.visitedFlowNodeMap[node.FlowBase().ID] = true
	priorRemainingConstraints := w.walk(node.Antecedent)
	delete(w.visitedFlowNodeMap, node.FlowBase().ID)

	arg1Expr := testExpression.D.Args[1].D.ValueExpr
	arg1Type := w.evaluator.GetTypeOfExpression(arg1Expr,
		EvalFlagsAllowMissingTypeArgs|EvalFlagsStrLiteralAsType|EvalFlagsNoParamSpec|
			EvalFlagsNoTypeVarTuple|EvalFlagsNoFinal|EvalFlagsNoSpecialize, nil).Type

	if !IsInstantiableClass(arg1Type) {
		return nil, false
	}

	remaining := []*ClassType{}
	for _, subtype := range priorRemainingConstraints {
		matches := ClassTypeIsSameGenericClass(subtype,
			ClassTypeCloneAsInstance(arg1Type.(*ClassType), false), 0)
		if matches == isPositiveTest {
			remaining = append(remaining, subtype)
		}
	}
	return remaining, true
}

// narrowForLabel is the original's BranchLabel/LoopLabel arm: the union of what
// every antecedent path allows.
func (w *constrainedTypeVarWalker) narrowForLabel(labelNode *FlowLabel) []*ClassType {
	newConstraints := []*ClassType{}

	// The original's comment: prevent infinite recursion by noting that we've
	// been here before.
	w.visitedFlowNodeMap[labelNode.FlowBase().ID] = true
	for _, antecedent := range labelNode.Antecedents {
		for _, constraint := range w.walk(antecedent) {
			alreadyPresent := false
			for _, t := range newConstraints {
				if IsTypeSame(t, constraint, TypeSameOptions{}, 0) {
					alreadyPresent = true
					break
				}
			}
			if !alreadyPresent {
				newConstraints = append(newConstraints, constraint)
			}
		}
	}
	delete(w.visitedFlowNodeMap, labelNode.FlowBase().ID)

	return newConstraints
}

// isCompatibleWithConstrainedTypeVar corresponds to the function of the same
// name. The original's comment: determines whether a specified type is the same
// as a constrained TypeVar or is conditioned on that same TypeVar or is some
// union of the above.
func isCompatibleWithConstrainedTypeVar(t Type, typeVar *TypeVarType) bool {
	isCompatible := true

	DoForEachSubtype(t, func(subtype Type, _ int, _ []Type) {
		if IsTypeVar(subtype) {
			if !IsTypeSame(subtype, typeVar, TypeSameOptions{}, 0) {
				isCompatible = false
			}
			return
		}

		base := subtype.Base()
		if base.Props == nil || base.Props.Condition == nil {
			isCompatible = false
			return
		}

		found := false
		for _, condition := range base.Props.Condition {
			if TypeVarTypeHasConstraints(condition.TypeVar) &&
				condition.TypeVar.Priv.NameWithScope == typeVar.Priv.NameWithScope {
				found = true
				break
			}
		}
		if !found {
			isCompatible = false
		}
	})

	return isCompatible
}

// NarrowConstrainedTypeVar corresponds to the typeEvaluator.ts wrapper of the
// same name, which finds the flow node for a parse node and delegates.
func (e *typeEvaluator) NarrowConstrainedTypeVar(node parser.ParseNode, typeVar *TypeVarType) Type {
	flowNode := GetFlowNode(node)
	if flowNode == nil {
		return nil
	}

	result := e.narrowConstrainedTypeVarForFlowNode(flowNode, typeVar)
	if result == nil {
		// See STATUS-STAGE-D.md: a nil *ClassType must not be widened by
		// assignment, or the interface value is non-nil and every reader faults.
		return nil
	}
	return result
}

// constrainedAntecedentOf reads the antecedent of the node kinds this walk steps
// past. It is wider than antecedentOf in codeflowengine_reachability.go, which
// covers only the four kinds that function's caller can see; the original spells
// this out as a union cast at each site.
func constrainedAntecedentOf(node FlowNode) FlowNode {
	switch n := node.(type) {
	case *FlowVariableAnnotation:
		return n.Antecedent
	case *FlowAssignment:
		return n.Antecedent
	case *FlowWildcardImport:
		return n.Antecedent
	case *FlowExhaustedMatch:
		return n.Antecedent
	case *FlowPostFinally:
		return n.Antecedent
	case *FlowPreFinallyGate:
		return n.Antecedent
	case *FlowCall:
		return n.Antecedent
	case *FlowCondition:
		return n.Antecedent
	}
	return nil
}
