/*
 * typeevaluator_validatecall.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * validateCallArgs, validateCallArgsForSubtype and getSpeculativeNodeForCall.
 *
 * The top of call validation. `f(x)` where `f` is a union of a function and a
 * class has to be validated once per member and the results unioned, and this is
 * the loop that does it; each member then dispatches by category to the
 * validator that knows how to call that kind of thing.
 *
 * The speculative wrapper on each iteration is the load-bearing detail. Every
 * member except the last is evaluated speculatively, so the diagnostics and cache
 * entries it produces are discarded: a union member that does not accept the
 * arguments must not leave an error behind, because another member may accept
 * them. The last iteration runs for real -- `useSpeculativeMode(undefined, ...)`
 * -- so whatever it concludes is what the user sees. `allowDiagnostics: true`
 * keeps the speculative iterations from suppressing diagnostics for nodes
 * underneath them, which would lose errors from nested calls in the arguments.
 *
 * touchArgTypes exists for the error paths. When a call is rejected outright --
 * calling a module, calling None -- the argument expressions still have to be
 * evaluated, because something later reads their types out of the cache and
 * would otherwise find nothing. It is skipped when the call type is incomplete,
 * since evaluating then would cache a half-formed answer.
 *
 * The Never coercion at the end is a false-positive guard the original spells
 * out: if every union member errored, the union of their (absent) return types
 * is Never, and Never propagates into every subsequent expression as a second
 * error. Unknown stops that -- but only when the Never came from errors rather
 * than from an honest NoReturn.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// ValidateCallArgs corresponds to validateCallArgs. The original's recursionCount
// defaults to 0; the interface method has no such parameter, so it delegates.
func (e *typeEvaluator) ValidateCallArgs(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	callTypeResult *TypeResult,
	constraints *ConstraintTracker,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
) *CallResult {
	return e.validateCallArgs(errorNode, argList, callTypeResult, constraints,
		skipUnknownArgCheck, inferenceContext, 0)
}

func (e *typeEvaluator) validateCallArgs(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	callTypeResult *TypeResult,
	constraints *ConstraintTracker,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
	recursionCount int,
) *CallResult {
	argumentErrors := false
	isTypeIncomplete := false
	var specializedInitSelfType Type
	overloadsUsedForCall := []*FunctionType{}

	if recursionCount > MaxTypeRecursionCount {
		return &CallResult{
			ReturnType:           UnknownTypeCreate(false),
			ArgumentErrors:       true,
			OverloadsUsedForCall: overloadsUsedForCall,
		}
	}
	recursionCount++

	// The original's comment: special forms are not callable.
	if props := callTypeResult.Type.Base().Props; props != nil && props.SpecialForm != nil {
		var exprNode parser.ExpressionNode = errorNode
		if callNode, ok := errorNode.(*parser.CallNode); ok {
			exprNode = callNode.D.LeftExpr
		}

		e.AddDiagnostic(
			DiagnosticRuleReportCallIssue,
			localization.LocMessage.ObjectNotCallable().Format(
				e.PrintType(props.SpecialForm, &PrintTypeOptions{ExpandTypeAlias: true})),
			exprNode,
			nil,
		)

		return &CallResult{
			ReturnType:           UnknownTypeCreate(false),
			ArgumentErrors:       true,
			OverloadsUsedForCall: overloadsUsedForCall,
		}
	}

	returnType := e.mapSubtypesExpandTypeVars(
		callTypeResult.Type,
		&EvaluatorMapSubtypesOptions{SortSubtypes: true},
		func(expandedSubtype Type, unexpandedSubtype Type, isLastIteration bool) Type {
			// Every iteration but the last is speculative; see the file header.
			var speculativeNode parser.ParseNode
			if !isLastIteration {
				speculativeNode = getSpeculativeNodeForCall(errorNode)
			}

			var subtypeReturnType Type

			e.UseSpeculativeMode(speculativeNode, func() {
				callResult := e.validateCallArgsForSubtype(
					errorNode, argList, expandedSubtype, unexpandedSubtype,
					callTypeResult.IsIncomplete, constraints, skipUnknownArgCheck,
					inferenceContext, recursionCount)

				if callResult.ArgumentErrors {
					argumentErrors = true
				}
				if callResult.IsTypeIncomplete {
					isTypeIncomplete = true
				}
				if callResult.OverloadsUsedForCall != nil {
					overloadsUsedForCall = append(overloadsUsedForCall, callResult.OverloadsUsedForCall...)
				}

				// Assigned rather than accumulated, as the original does: only the
				// last subtype's value survives.
				specializedInitSelfType = callResult.SpecializedInitSelfType

				subtypeReturnType = callResult.ReturnType
			}, &SpeculativeModeOptions{AllowDiagnostics: true})

			return subtypeReturnType
		},
		0,
	)

	// The original's comment: if we ended up with a "Never" type because all code
	// paths returned undefined due to argument errors, transform the result into
	// an Unknown to avoid subsequent false positives.
	if argumentErrors && IsNever(returnType) && !returnType.(*NeverType).Priv.IsNoReturn {
		returnType = UnknownTypeCreate(false)
	}

	return &CallResult{
		ArgumentErrors:          argumentErrors,
		ReturnType:              returnType,
		IsTypeIncomplete:        isTypeIncomplete,
		SpecializedInitSelfType: specializedInitSelfType,
		OverloadsUsedForCall:    overloadsUsedForCall,
	}
}

// validateCallArgsForSubtype corresponds to the function of the same name: one
// member of the callable union, dispatched by category.
func (e *typeEvaluator) validateCallArgsForSubtype(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	expandedCallType Type,
	unexpandedCallType Type,
	isCallTypeIncomplete bool,
	constraints *ConstraintTracker,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
	recursionCount int,
) *CallResult {
	// touchArgTypes is the original's nested function of the same name; see the
	// file header.
	touchArgTypes := func() {
		if isCallTypeIncomplete {
			return
		}
		for _, arg := range argList {
			if arg.ValueExpression != nil && !e.IsSpeculativeModeInUse(arg.ValueExpression) {
				e.GetTypeOfArg(arg, nil)
			}
		}
	}

	switch expandedCallType.Base().Category {
	case TypeCategoryNever, TypeCategoryUnknown, TypeCategoryAny:
		// The original's comment: create a dummy callable that accepts all
		// arguments and validate that the argument expressions are valid.
		//
		// Calling an Any is not an error, but its arguments still have to be
		// checked, and a signature that accepts anything is how that is arranged.
		dummyFunctionType := FunctionTypeCreateInstance("", "", "", FunctionTypeFlagsNone, nil)
		FunctionTypeAddDefaultParams(dummyFunctionType, false)

		dummyCallResult := e.validateCallForFunction(errorNode, argList, dummyFunctionType,
			isCallTypeIncomplete, constraints, skipUnknownArgCheck, inferenceContext)

		// `{ ...dummyCallResult, returnType: expandedCallType }` -- the dummy's
		// own return type is meaningless, but its argument errors are not.
		copied := *dummyCallResult
		copied.ReturnType = expandedCallType
		return &copied

	case TypeCategoryFunction:
		return e.validateCallForFunction(errorNode, argList, expandedCallType.(*FunctionType),
			isCallTypeIncomplete, constraints, skipUnknownArgCheck, inferenceContext)

	case TypeCategoryOverloaded:
		return e.validateCallForOverloaded(errorNode, argList, expandedCallType.(*OverloadedType),
			isCallTypeIncomplete, constraints, skipUnknownArgCheck, inferenceContext)

	case TypeCategoryClass:
		classType := expandedCallType.(*ClassType)

		if IsNoneInstance(expandedCallType) {
			e.AddDiagnostic(DiagnosticRuleReportOptionalCall,
				localization.LocMessage.NoneNotCallable(), errorNode, nil)

			touchArgTypes()
			return &CallResult{ArgumentErrors: true}
		}

		if expandedCallType.Base().IsInstantiable() {
			return e.validateCallForInstantiableClass(errorNode, argList, classType,
				unexpandedCallType, skipUnknownArgCheck, inferenceContext)
		}

		return e.validateCallForClassInstance(errorNode, argList, classType, unexpandedCallType,
			constraints, skipUnknownArgCheck, inferenceContext, recursionCount)

	case TypeCategoryTypeVar:
		// The original's comment: TypeVars should have been expanded in most
		// cases, but we still need to handle the case of Type[T] where T is a
		// constrained type that contains a union. We also need to handle recursive
		// type aliases.
		return e.validateCallArgs(errorNode, argList,
			&TypeResult{
				Type:         TransformPossibleRecursiveTypeAlias(expandedCallType, 0),
				IsIncomplete: isCallTypeIncomplete,
			},
			constraints, skipUnknownArgCheck, inferenceContext, recursionCount)

	case TypeCategoryModule:
		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.ModuleNotCallable(), errorNode, nil)

		touchArgTypes()
		return &CallResult{ArgumentErrors: true}
	}

	touchArgTypes()
	return &CallResult{ArgumentErrors: true}
}

// getSpeculativeNodeForCall corresponds to the function of the same name: which
// node the speculative context should be rooted at.
//
// It is widened past the error node in two cases, and both are about making the
// discard cover everything the speculative attempt wrote. An error node inside an
// argument expands to the whole call, and a class name expands to the class,
// since evaluating a class's arguments writes to the class node.
func getSpeculativeNodeForCall(errorNode parser.ExpressionNode) parser.ParseNode {
	// The original's comment: if the error node is within an arg, expand to
	// include the parent of the arg list.
	if argParent := GetParentNodeOfType(errorNode, parser.ParseNodeTypeArgument); argParent != nil {
		if argParent.NodeBase().Parent != nil {
			return argParent.NodeBase().Parent
		}
	}

	// The original's comment: if the error node is the name in a class
	// declaration, expand to include the class node.
	if nameNode, ok := errorNode.(*parser.NameNode); ok {
		if classNode, ok := nameNode.NodeBase().Parent.(*parser.ClassNode); ok {
			if classNode.D.Name == nameNode {
				return classNode
			}
		}
	}

	return errorNode
}

/*
 * The four category validators this reaches.
 */

// validateCallForFunction corresponds to the function of the same name, which
// handles the namedtuple and NewType special cases, runs validateArgs, and then
// applies the function transforms.
func (e *typeEvaluator) validateCallForFunction(
	_ parser.ExpressionNode, _ []*Arg, _ *FunctionType, _ bool,
	_ *ConstraintTracker, _ bool, _ *InferenceContext,
) *CallResult {
	e.unported("validateCallForFunction")
	return &CallResult{ArgumentErrors: true}
}

// validateCallForOverloaded corresponds to the function of the same name, which
// tries each overload in order and reports against the best match when none fits.
func (e *typeEvaluator) validateCallForOverloaded(
	_ parser.ExpressionNode, _ []*Arg, _ *OverloadedType, _ bool,
	_ *ConstraintTracker, _ bool, _ *InferenceContext,
) *CallResult {
	e.unported("validateCallForOverloaded")
	return &CallResult{ArgumentErrors: true}
}

// validateCallForInstantiableClass corresponds to the function of the same name:
// construction, which runs __call__ on the metaclass, then __new__, then
// __init__.
func (e *typeEvaluator) validateCallForInstantiableClass(
	_ parser.ExpressionNode, _ []*Arg, _ *ClassType, _ Type, _ bool, _ *InferenceContext,
) *CallResult {
	e.unported("validateCallForInstantiableClass")
	return &CallResult{ArgumentErrors: true}
}

// validateCallForClassInstance corresponds to the function of the same name:
// calling an object, which goes through its __call__ member.
func (e *typeEvaluator) validateCallForClassInstance(
	_ parser.ExpressionNode, _ []*Arg, _ *ClassType, _ Type,
	_ *ConstraintTracker, _ bool, _ *InferenceContext, _ int,
) *CallResult {
	e.unported("validateCallForClassInstance")
	return &CallResult{ArgumentErrors: true}
}
