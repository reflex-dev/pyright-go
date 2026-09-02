/*
 * typeevaluator_assigntarget.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * assignTypeToExpression.
 *
 * Writing an evaluated type onto an assignment target. getDeclaredTypeForExpression
 * asks what the target expects; this delivers what it got, and the two together
 * are what an assignment does.
 *
 * Eight target shapes, each with its own destination for the type: a name writes
 * to the symbol's cache entry, a member access writes through the class, an
 * index goes through __setitem__ with a 'set' usage carrying the value, a
 * list or tuple destructures, an annotation evaluates the annotation and then
 * recurses on the value expression, and an unpack wraps the type in a list.
 *
 * The TypeVar-call check at the top is not part of the assignment: it verifies
 * that `T = TypeVar("T")` binds the TypeVar to a name matching its own, which is
 * a naming rule rather than a type rule, and it runs before any target shape is
 * considered.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// assignTypeToExpression corresponds to the function of the same name.
func (e *typeEvaluator) assignTypeToExpression(
	target parser.ExpressionNode,
	typeResult *TypeResult,
	srcExpr parser.ExpressionNode,
	ignoreEmptyContainers bool,
	allowAssignmentToFinalVar bool,
	expectedTypeDiagAddendum *common.DiagnosticAddendum,
) {
	e.checkTypeVarAssignedName(target, typeResult, srcExpr)

	// The original's comment: if the type was partially unbound, an error will
	// have already been logged. Remove the unbound before assigning to the
	// target expression so the unbound error doesn't propagate.
	//
	// The spread copies rather than mutating, since typeResult may be the cached
	// one.
	if FindSubtype(typeResult.Type, func(subtype Type) bool { return IsUnbound(subtype) }) != nil {
		copied := *typeResult
		copied.Type = RemoveUnbound(typeResult.Type)
		typeResult = &copied
	}

	switch node := target.(type) {
	case *parser.NameNode:
		e.assignTypeToNameNode(node, typeResult, ignoreEmptyContainers, srcExpr,
			allowAssignmentToFinalVar, expectedTypeDiagAddendum)

	case *parser.MemberAccessNode:
		e.assignTypeToMemberAccessNode(node, typeResult, srcExpr, expectedTypeDiagAddendum)

	case *parser.IndexNode:
		baseTypeResult := e.getTypeOfExpression(node.D.LeftExpr, EvalFlagsIndexBaseDefaults, nil)

		e.getTypeOfIndexWithBaseType(node, baseTypeResult, &EvaluatorUsage{
			Method:              "set",
			SetType:             typeResult,
			SetErrorNode:        srcExpr,
			SetExpectedTypeDiag: expectedTypeDiagAddendum,
		}, EvalFlagsNone)

		e.writeTypeCache(node, typeResult, evalFlagsNonePtr(), nil, false)

	case *parser.ListNode:
		e.assignTypeToTupleOrListNode(node, typeResult, srcExpr)

	case *parser.TupleNode:
		e.assignTypeToTupleOrListNode(node, typeResult, srcExpr)

	case *parser.TypeAnnotationNode:
		e.GetTypeOfAnnotation(node.D.Annotation, &ExpectedTypeOptions{
			VarTypeAnnotation: true,
			AllowFinal:        e.isFinalAllowedForAssignmentTarget(node.D.ValueExpr),
			AllowClassVar:     e.isClassVarAllowedForAssignmentTarget(node.D.ValueExpr),
		})

		e.assignTypeToExpression(node.D.ValueExpr, typeResult, srcExpr,
			ignoreEmptyContainers, allowAssignmentToFinalVar, expectedTypeDiagAddendum)

	case *parser.UnpackNode:
		// `*x, y = ...` binds x to a list of the element type.
		e.assignTypeToExpression(
			node.D.Expr,
			&TypeResult{
				Type:         e.GetBuiltInObject(node.D.Expr, "list", []Type{typeResult.Type}),
				IsIncomplete: typeResult.IsIncomplete,
			},
			srcExpr, ignoreEmptyContainers, allowAssignmentToFinalVar, expectedTypeDiagAddendum,
		)

	case *parser.ErrorNode:
		// The original's comment: evaluate the child expression as best we can
		// so the type information is cached for the completion handler.
		if node.D.Child != nil {
			child := node.D.Child
			e.SuppressDiagnostics(child, func() {
				e.getTypeOfExpression(child, EvalFlagsNone, nil)
			})
		}

	default:
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.AssignmentTargetExpr(), target, nil)
	}
}

// checkTypeVarAssignedName is the original's `if (isTypeVar(typeResult.type))`
// block: a TypeVar created by a TypeVar()/TypeVarTuple()/ParamSpec() call must be
// assigned to a name matching the name it was given.
func (e *typeEvaluator) checkTypeVarAssignedName(
	target parser.ExpressionNode,
	typeResult *TypeResult,
	srcExpr parser.ExpressionNode,
) {
	if !IsTypeVar(typeResult.Type) {
		return
	}

	call, ok := srcExpr.(*parser.CallNode)
	if !ok {
		return
	}

	callType := e.getTypeOfExpression(call.D.LeftExpr, EvalFlagsCallBaseDefaults, nil).Type
	if !IsInstantiableClass(callType) ||
		!ClassTypeIsBuiltInNamed(callType.(*ClassType), "TypeVar", "TypeVarTuple", "ParamSpec") {
		return
	}

	typeVarTarget := target
	if annotation, ok := target.(*parser.TypeAnnotationNode); ok {
		typeVarTarget = annotation.D.ValueExpr
	}

	typeVar := typeResult.Type.(*TypeVarType)
	nameNode, isName := typeVarTarget.(*parser.NameNode)
	if isName && nameNode.D.Value == typeVar.Shared.Name {
		return
	}

	name := TypeVarTypeGetReadableName(typeVar, false)
	message := localization.LocMessage.TypeVarAssignedName().Format(name)
	if IsParamSpec(typeVar) {
		message = localization.LocMessage.ParamSpecAssignedName().Format(name)
	}

	e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, message, typeVarTarget, nil)
}

/*
 * The three target-shape handlers this reaches.
 */

// assignTypeToMemberAccessNode corresponds to the function of the same name.
func (e *typeEvaluator) assignTypeToMemberAccessNode(
	_ *parser.MemberAccessNode, _ *TypeResult, _ parser.ExpressionNode, _ *common.DiagnosticAddendum,
) {
	e.unported("assignTypeToMemberAccessNode")
}

// assignTypeToTupleOrListNode corresponds to the function of the same name: the
// destructuring case, which matches the source's elements against the targets
// and handles a starred target absorbing the remainder.
func (e *typeEvaluator) assignTypeToTupleOrListNode(
	_ parser.ExpressionNode, _ *TypeResult, _ parser.ExpressionNode,
) {
	e.unported("assignTypeToTupleOrListNode")
}
