/*
 * codeflowengine_noreturn.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/codeFlowEngine.ts (pyright 1.1.412):
 * isCallNoReturn and isFunctionNoReturn.
 *
 * This is what makes the code after `sys.exit()` unreachable, and it was the
 * single largest entry on the frontier -- 4,309 hits over a 60-file sample --
 * because the reachability walk asks it about every call it passes.
 *
 * A call is NoReturn only when EVERY subtype of the callee is NoReturn, which is
 * why the function counts subtypes rather than short-circuiting. For an
 * overloaded callee where only some overloads are NoReturn it falls through to
 * full argument matching to find out which overload actually applies.
 *
 * Two things are cached on the engine rather than the evaluator: the per-call
 * answer, and the recursion depth. The cache is seeded with `false` before the
 * work begins, which is the recursion guard -- a call that reaches itself
 * answers "returns" rather than looping.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// inferNoReturnForUnannotatedFunctions corresponds to the module-level constant
// of the same name. It is `false` in the shipped source, which makes the whole
// declaration-inspecting branch of isFunctionNoReturn dead code. That branch is
// transliterated anyway -- it is the only record of the "raise
// NotImplementedError" idiom the original recognizes, and the constant is
// plainly a switch someone intends to flip.
const inferNoReturnForUnannotatedFunctions = false

// isCallNoReturn corresponds to the function of the same name.
func (c *codeFlowReachability) isCallNoReturn(evaluator TypeEvaluator, flowNode *FlowCall) bool {
	node := flowNode.Node
	fileInfo := GetFileInfo(node)

	// The original's comment: assume that calls within a pyi file are not
	// "NoReturn" calls.
	if fileInfo.IsStubFile {
		return false
	}

	// The original logs here when enablePrintCallNoReturn, a constant that is
	// false in the shipped source.

	// The original's comment: see if this information is cached already.
	if result, ok := c.callIsNoReturnCache[node.NodeBase().ID]; ok {
		return result
	}

	// The original's comment: see if we've exceeded the max recursion depth.
	if c.noReturnAnalysisDepth > MaxTypeRecursionCount {
		return false
	}

	// The original's comment: don't attempt to evaluate a lambda call. We need
	// to evaluate these in the context of its arguments.
	if node.D.LeftExpr.GetNodeType() == parser.ParseNodeTypeLambda {
		return false
	}

	// The original's comment: initially set to false to avoid recursion.
	c.callIsNoReturnCache[node.NodeBase().ID] = false

	c.noReturnAnalysisDepth++
	defer func() { c.noReturnAnalysisDepth-- }()

	noReturnTypeCount := 0
	subtypeCount := 0

	// The original's comment: evaluate the call base type.
	callTypeResult := evaluator.GetTypeOfExpression(node.D.LeftExpr, EvalFlagsCallBaseDefaults, nil)
	callType := callTypeResult.Type

	DoForEachSubtype(callType, func(callSubtype Type, _ int, _ []Type) {
		// The original's comment: track the number of subtypes we've examined.
		subtypeCount++

		if IsInstantiableClass(callSubtype) {
			// The original's comment: does the class have a custom metaclass
			// that implements a `__call__` method? If so, it will be called
			// instead of `__init__` or `__new__`. We'll assume in this case that
			// the __call__ method is not a NoReturn type.
			if metaclassCallResult := GetBoundCallMethod(evaluator, node, callSubtype.(*ClassType)); metaclassCallResult != nil {
				return
			}

			if newMethodResult := GetBoundNewMethod(evaluator, node, callSubtype.(*ClassType), nil, MemberAccessFlagsSkipObjectBaseClass); newMethodResult != nil {
				if IsFunctionOrOverloaded(newMethodResult.Type) {
					callSubtype = newMethodResult.Type
				}
			}
		} else if IsClassInstance(callSubtype) {
			if callMethodType := evaluator.GetBoundMagicMethod(callSubtype.(*ClassType), "__call__", nil, nil, nil, 0); callMethodType != nil {
				callSubtype = callMethodType
			}
		}

		isCallAwaited := node.NodeBase().Parent != nil &&
			node.NodeBase().Parent.GetNodeType() == parser.ParseNodeTypeAwait

		if IsFunction(callSubtype) {
			if c.isFunctionNoReturn(evaluator, callSubtype.(*FunctionType), isCallAwaited) {
				noReturnTypeCount++
			}
			return
		}

		if IsOverloaded(callSubtype) {
			overloadCount := 0
			noReturnOverloadCount := 0

			for _, overload := range OverloadedTypeGetOverloads(callSubtype.(*OverloadedType)) {
				overloadCount++
				if c.isFunctionNoReturn(evaluator, overload, isCallAwaited) {
					noReturnOverloadCount++
				}
			}

			// The original's comment: was at least one of the overloaded return
			// types NoReturn?
			if noReturnOverloadCount == 0 {
				return
			}

			// The original's comment: do all of the overloads return NoReturn?
			if noReturnOverloadCount == overloadCount {
				noReturnTypeCount++
				return
			}

			// The original's comment: perform a more complete evaluation to
			// determine whether the applicable overload returns a NoReturn.
			argList := []*Arg{}
			for _, arg := range node.D.Args {
				argList = append(argList, evaluator.ConvertNodeToArg(arg))
			}

			callResult := evaluator.ValidateOverloadedArgTypes(
				node,
				argList,
				&TypeResult{Type: callSubtype, IsIncomplete: callTypeResult.IsIncomplete},
				nil,
				false,
				nil,
			)

			if callResult != nil && callResult.ReturnType != nil && IsNever(callResult.ReturnType) {
				noReturnTypeCount++
			}
		}
	})

	// The original's comment: the call is considered NoReturn if all subtypes
	// evaluate to NoReturn.
	callIsNoReturn := subtypeCount > 0 && noReturnTypeCount == subtypeCount

	// The original's comment: cache the value for next time.
	c.callIsNoReturnCache[node.NodeBase().ID] = callIsNoReturn

	return callIsNoReturn
}

// isFunctionNoReturn corresponds to the function of the same name.
func (c *codeFlowReachability) isFunctionNoReturn(
	evaluator TypeEvaluator,
	functionType *FunctionType,
	isCallAwaited bool,
) bool {
	returnType := FunctionTypeGetEffectiveReturnType(functionType, false)
	if returnType != nil {
		if IsClassInstance(returnType) &&
			ClassTypeIsBuiltInNamed(returnType.(*ClassType), "Coroutine", "CoroutineType") {
			typeArgs := returnType.(*ClassType).Priv.TypeArgs
			if len(typeArgs) >= 3 {
				if IsNever(typeArgs[2]) && isCallAwaited {
					return true
				}
			}
		}

		return IsNever(returnType)
	}

	if !inferNoReturnForUnannotatedFunctions {
		return false
	}

	// Everything below is unreachable while inferNoReturnForUnannotatedFunctions
	// is false. See the constant.
	functionDecl := functionType.Shared.Declaration
	if functionDecl == nil {
		return false
	}

	// The original's comment: if the function is a generator (i.e. it has yield
	// statements) then it is not a "no return" call. Also, don't infer a "no
	// return" type for abstract methods.
	if functionDecl.IsGenerator ||
		FunctionTypeIsAbstractMethod(functionType) ||
		FunctionTypeIsStubDefinition(functionType) ||
		FunctionTypeIsPyTypedDefinition(functionType) {
		return false
	}

	// The original's comment: check specifically for a common idiom where the
	// only statement (other than a possible docstring) is a "raise
	// NotImplementedError".
	functionNode, ok := functionDecl.Node.(*parser.FunctionNode)
	if !ok {
		return false
	}

	foundRaiseNotImplemented := false
	for _, statement := range functionNode.D.Suite.D.Statements {
		statementList, ok := statement.(*parser.StatementListNode)
		if !ok || len(statementList.D.Statements) != 1 {
			break
		}

		simpleStatement := statementList.D.Statements[0]
		if simpleStatement.GetNodeType() == parser.ParseNodeTypeStringList {
			continue
		}

		if raiseNode, ok := simpleStatement.(*parser.RaiseNode); ok && raiseNode.D.Expr != nil {
			// The original's comment: check for a raising about
			// 'NotImplementedError' or a subtype thereof.
			exceptionType := evaluator.GetType(raiseNode.D.Expr)

			if exceptionType != nil && IsClass(exceptionType) &&
				DerivesFromStdlibClass(exceptionType.(*ClassType), "NotImplementedError") {
				foundRaiseNotImplemented = true
			}
		}

		break
	}

	if !foundRaiseNotImplemented && !isAfterFunctionNodeReachable(evaluator, functionType) {
		return true
	}

	return false
}

// isAfterFunctionNodeReachable corresponds to the file-local isAfterNodeReachable
// in codeFlowEngine.ts, renamed because the evaluator interface already carries
// an IsAfterNodeReachable with a different shape.
func isAfterFunctionNodeReachable(evaluator TypeEvaluator, functionType *FunctionType) bool {
	if functionType.Shared.Declaration == nil {
		return true
	}

	return evaluator.IsAfterNodeReachable(functionType.Shared.Declaration.Node)
}

/*
 * The two constructors.ts helpers this reaches.
 */
