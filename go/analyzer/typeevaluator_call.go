/*
 * typeevaluator_call.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfCall.
 *
 * getTypeOfCall is the front of the call machinery and, like the other things
 * worth porting before their arms, it is mostly a dispatch: evaluate the callee,
 * collect the arguments, then route to one of seven handlers. Six of those
 * handle a specific builtin -- super, reveal_type twice over (implicit and
 * typing.reveal_type), assert_type, TypeForm, reveal_locals -- and the seventh,
 * validateCallArgs, is everything else and is where the ~3,000 lines of argument
 * matching live.
 *
 * Three details are load bearing and easy to lose:
 *
 *   - Arguments are collected via getArgsByRuntimeOrder, not source order. A
 *     call `f(*a, b=1, *c)` evaluates its unpacked positionals in a different
 *     order than they appear.
 *   - The "touch all the args so they're marked accessed" pass at the end is
 *     skipped for a TypeVar() call inside typing.pyi. The original's comment
 *     explains the cycle that would otherwise result: retrieving `str` pulls in
 *     Sequence, which pulls in Iterable, which uses a TypeVar.
 *   - A call in a type expression is an error AND its result is discarded --
 *     the diagnostic and the Unknown both happen, after everything above.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getTypeOfCall corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfCall(
	node *parser.CallNode,
	flags EvalFlags,
	inferenceContext *InferenceContext,
) *TypeResult {
	// The original's comment: check for the use of `type(x)` within a type
	// annotation. This isn't allowed, and it's a common mistake, so we want to
	// emit a diagnostic that guides the user to the right solution.
	if (flags & EvalFlagsTypeExpression) != 0 {
		if leftName, ok := node.D.LeftExpr.(*parser.NameNode); ok && leftName.D.Value == "type" {
			diag := common.NewDiagnosticAddendum()
			diag.AddMessage(localization.LocAddendum.UseTypeInstead())
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.TypeCallNotAllowed()+diag.GetString(),
				node,
				nil,
			)
		}
	}

	var baseTypeResult *TypeResult

	// The original's comment: handle immediate calls of lambdas specially.
	if lambda, ok := node.D.LeftExpr.(*parser.LambdaNode); ok {
		baseTypeResult = e.getTypeOfLambdaForCall(node, lambda, inferenceContext)
	} else {
		baseTypeResult = e.getTypeOfExpression(
			node.D.LeftExpr,
			EvalFlagsCallBaseDefaults|(flags&EvalFlagsForwardRefs),
			nil,
		)
	}

	argList := []*Arg{}
	for _, arg := range GetArgsByRuntimeOrder(node) {
		argList = append(argList, &Arg{
			ValueExpression: arg.D.ValueExpr,
			ArgCategory:     arg.D.ArgCategory,
			Node:            arg,
			Name:            arg.D.Name,
		})
	}

	typeResult := &TypeResult{Type: UnknownTypeCreate(false)}

	// The original mutates baseTypeResult.type in place; the cache may hold this
	// TypeResult, so the write is preserved rather than copied around it.
	baseTypeResult.Type = e.ensureSignatureIsUnique(baseTypeResult.Type, node)

	if IsTypeAliasPlaceholder(baseTypeResult.Type) {
		typeResult.IsIncomplete = true
	} else {
		typeResult = e.dispatchCall(node, argList, baseTypeResult, typeResult, inferenceContext)

		if baseTypeResult.IsIncomplete {
			typeResult.IsIncomplete = true
		}
	}

	e.touchCallArgs(node, argList, baseTypeResult)

	if (flags & EvalFlagsTypeExpression) != 0 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.TypeAnnotationCall(), node, nil)

		typeResult = &TypeResult{Type: UnknownTypeCreate(false)}
	}

	return typeResult
}

// dispatchCall is the original's if/else chain over the callee's type: the six
// builtin special cases, then validateCallArgs for everything else.
func (e *typeEvaluator) dispatchCall(
	node *parser.CallNode,
	argList []*Arg,
	baseTypeResult *TypeResult,
	typeResult *TypeResult,
	inferenceContext *InferenceContext,
) *TypeResult {
	baseType := baseTypeResult.Type
	leftName, leftIsName := node.D.LeftExpr.(*parser.NameNode)

	// The original's comment: handle the built-in "super" call specially.
	if leftIsName && leftName.D.Value == "super" {
		return e.getTypeOfSuperCall(node)
	}

	// The original's comment: handle the implicit "reveal_type" call.
	if IsAnyOrUnknown(baseType) && leftIsName && leftName.D.Value == "reveal_type" {
		return e.getTypeOfRevealType(node, inferenceContext)
	}

	if IsFunction(baseType) {
		fn := baseType.(*FunctionType)
		// The original's comment: handle the "typing.reveal_type" call.
		if FunctionTypeIsBuiltIn(fn, "reveal_type") {
			return e.getTypeOfRevealType(node, inferenceContext)
		}
		// The original's comment: handle the "typing.assert_type" call.
		if FunctionTypeIsBuiltIn(fn, "assert_type") {
			return e.getTypeOfAssertType(node, inferenceContext)
		}
	}

	// The original's comment: handle the "typing.TypeForm" call.
	if IsClass(baseType) && ClassTypeIsBuiltInNamed(baseType.(*ClassType), "TypeForm") {
		return e.getTypeOfTypeForm(node, baseType.(*ClassType))
	}

	if IsAnyOrUnknown(baseType) && leftIsName && leftName.D.Value == "reveal_locals" {
		if len(node.D.Args) == 0 {
			// The original's comment: handle the special-case "reveal_locals"
			// call.
			typeResult.Type = e.getTypeOfRevealLocals(node)
		} else {
			e.AddDiagnostic(DiagnosticRuleReportCallIssue, localization.LocMessage.RevealLocalsArgs(), node, nil)
		}
		return typeResult
	}

	callResult := e.ValidateCallArgs(node, argList, baseTypeResult, nil, false, inferenceContext)
	if callResult == nil {
		return typeResult
	}

	typeResult.Type = callResult.ReturnType
	if typeResult.Type == nil {
		typeResult.Type = UnknownTypeCreate(false)
	}

	if callResult.ArgumentErrors {
		typeResult.TypeErrors = true
	} else {
		typeResult.OverloadsUsedForCall = callResult.OverloadsUsedForCall
	}

	if callResult.IsTypeIncomplete {
		typeResult.IsIncomplete = true
	}

	return typeResult
}

// touchCallArgs is the original's final "mark the arguments accessed" pass.
func (e *typeEvaluator) touchCallArgs(node *parser.CallNode, argList []*Arg, baseTypeResult *TypeResult) {
	// The original's comment: don't bother evaluating the arguments if we're
	// speculatively evaluating the call or the base type is incomplete.
	if e.IsSpeculativeModeInUse(node) || baseTypeResult.IsIncomplete {
		return
	}

	// The original's comment: touch all of the args so they're marked accessed
	// even if there were errors. We skip this if it's a TypeVar() call in the
	// typing.pyi module because this results in a cyclical type resolution
	// problem whereby we try to retrieve the str class, which inherits from
	// Sequence, which inherits from Iterable, which uses a TypeVar. Without
	// this, Iterable and Sequence classes have invalid type parameters.
	isCyclicalTypeVarCall := IsInstantiableClass(baseTypeResult.Type) &&
		ClassTypeIsBuiltInNamed(baseTypeResult.Type.(*ClassType), "TypeVar") &&
		GetFileInfo(node).IsTypingStubFile
	isTypeFormCall := IsInstantiableClass(baseTypeResult.Type) &&
		isTypeFormClass(baseTypeResult.Type.(*ClassType))

	if isCyclicalTypeVarCall || isTypeFormCall {
		return
	}

	for _, arg := range argList {
		if arg.ValueExpression != nil &&
			arg.ValueExpression.GetNodeType() != parser.ParseNodeTypeStringList &&
			!e.isTypeCached(arg.ValueExpression) {
			e.getTypeOfExpression(arg.ValueExpression, EvalFlagsNone, nil)
		}
	}
}

/*
 * The six special-case handlers and the lambda-call helper. Each is a separate
 * unit of work and records itself.
 */

// getTypeOfSuperCall corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfSuperCall(_ *parser.CallNode) *TypeResult {
	e.unported("getTypeOfSuperCall")
	return &TypeResult{Type: UnknownTypeCreate(false)}
}

// getTypeOfTypeForm corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfTypeForm(_ *parser.CallNode, _ *ClassType) *TypeResult {
	e.unported("getTypeOfTypeForm")
	return &TypeResult{Type: UnknownTypeCreate(false)}
}
