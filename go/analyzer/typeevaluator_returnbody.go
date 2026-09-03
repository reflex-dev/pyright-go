/*
 * typeevaluator_returnbody.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * inferFunctionReturnType and methodAlwaysRaisesNotImplemented.
 *
 * Reading a function's return type out of its body: the union of every reachable
 * `return` expression, plus None where the end of the body is reachable.
 * Reachability is what makes it correct -- a `return 1` after an unconditional
 * raise contributes nothing.
 *
 * Three results are not a union of returns at all:
 *
 *   - A stub file infers Unknown. There is no body to read, and guessing from an
 *     empty one would be worse than admitting ignorance.
 *   - A function whose end is unreachable is NoReturn -- unless it is abstract or
 *     only ever raises NotImplementedError, where the raise is a placeholder for
 *     an implementation rather than a claim about control flow. Those infer
 *     Unknown, so an override is not compared against NoReturn.
 *   - A generator's returns become the Generator's third type argument, with the
 *     yields as the first. The send type, the second, is not inferrable: Any if
 *     the function ignores what is sent back, Unknown if it uses it, which is
 *     what keeps strict mode from complaining about a value nobody supplied.
 *
 * The result is cached on the function's SUITE node rather than on the function,
 * which is what makes the recursion map necessary: two functions that return
 * each other's results would otherwise recurse without bound, so an entry per
 * (function, caller) pair caps it.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// inferFunctionReturnType corresponds to the function of the same name.
func (e *typeEvaluator) inferFunctionReturnType(
	node *parser.FunctionNode, isAbstract bool, callerNode parser.ExpressionNode,
) *TypeResult {
	returnAnnotation := node.D.ReturnAnnotation
	if returnAnnotation == nil && node.D.FuncAnnotationComment != nil {
		returnAnnotation = node.D.FuncAnnotationComment.D.ReturnAnnotation
	}

	// The original's comment: this shouldn't be called if there is a declared
	// return type, but it can happen if there are unexpected cycles between
	// decorators and classes that they decorate. We'll just return an undefined
	// type in this case.
	if returnAnnotation != nil {
		return nil
	}

	// The original's comment: is this type already cached?
	if cached := e.readTypeCache(node.D.Suite, evalFlagsNonePtr()); cached != nil {
		return &TypeResult{Type: cached}
	}

	recursionEntry, _ := e.functionRecursionMap.Get(node.ID)

	if e.functionRecursionMap.Size() >= maxInferFunctionReturnRecursionCount {
		return &TypeResult{Type: UnknownTypeCreate(false), IsIncomplete: true}
	}

	for _, entry := range recursionEntry {
		if entry.CallerNode == callerNode {
			return &TypeResult{Type: UnknownTypeCreate(false), IsIncomplete: true}
		}
	}

	recursionEntry = append(recursionEntry, &FunctionRecursionInfo{CallerNode: callerNode})
	e.functionRecursionMap.Set(node.ID, recursionEntry)

	defer func() {
		// The original pops the entry it pushed and drops the map key when the
		// list empties. It reads back the list rather than reusing the local,
		// because a nested call may have appended to it.
		current, _ := e.functionRecursionMap.Get(node.ID)
		if len(current) > 0 {
			current = current[:len(current)-1]
		}
		if len(current) == 0 {
			e.functionRecursionMap.Delete(node.ID)
		} else {
			e.functionRecursionMap.Set(node.ID, current)
		}
	}()

	// The original also catches a stack-overflow exception here, logs it, and
	// returns undefined. Go's stack overflow is not recoverable, so there is
	// nothing to transliterate.
	inferredReturnType, isIncomplete := e.inferReturnTypeFromStatements(node, isAbstract)

	e.writeTypeCache(node.D.Suite,
		&TypeResult{Type: inferredReturnType, IsIncomplete: isIncomplete}, evalFlagsNonePtr(), nil, false)

	if inferredReturnType == nil {
		return nil
	}
	return &TypeResult{Type: inferredReturnType, IsIncomplete: isIncomplete}
}

// inferReturnTypeFromStatements is the original's try block.
func (e *typeEvaluator) inferReturnTypeFromStatements(
	node *parser.FunctionNode, isAbstract bool,
) (Type, bool) {
	var functionDecl *FunctionDeclaration
	if decl := GetDeclaration(node); decl != nil {
		// The original casts unconditionally rather than testing the tag.
		functionDecl, _ = decl.(*FunctionDeclaration)
	}

	functionNeverReturns := !e.IsAfterNodeReachable(node)
	implicitlyReturnsNone := e.IsAfterNodeReachable(node.D.Suite)

	// The original's comment: if a return type annotation is missing in a stub
	// file, assume it's an "unknown" type. In normal source files, we can infer
	// the type from the implementation.
	if GetFileInfo(node).IsStubFile {
		return UnknownTypeCreate(false), false
	}

	var inferredReturnType Type
	isIncomplete := false

	if functionNeverReturns {
		// The original's comment: if the function always raises and never returns,
		// assume a "NoReturn" type. Skip this for abstract methods which often are
		// implemented with "raise NotImplementedError()".
		if isAbstract || e.methodAlwaysRaisesNotImplemented(functionDecl) {
			inferredReturnType = UnknownTypeCreate(false)
		} else {
			inferredReturnType = NeverTypeCreateNoReturn()
		}
	} else {
		inferredReturnType, isIncomplete = e.combineReturnStatementTypes(functionDecl, implicitlyReturnsNone)
	}

	// The original's comment: is it a generator?
	if functionDecl != nil && functionDecl.IsGenerator {
		inferredReturnType = e.wrapReturnTypeInGenerator(node, functionDecl, inferredReturnType)
	}

	return inferredReturnType, isIncomplete
}

// combineReturnStatementTypes unions the reachable `return` expressions.
func (e *typeEvaluator) combineReturnStatementTypes(
	functionDecl *FunctionDeclaration, implicitlyReturnsNone bool,
) (Type, bool) {
	inferredReturnTypes := []Type{}
	isIncomplete := false

	if functionDecl != nil {
		for _, returnNode := range functionDecl.ReturnStatements {
			if !e.IsNodeReachable(returnNode, nil) {
				continue
			}

			if returnNode.D.Expr == nil {
				inferredReturnTypes = append(inferredReturnTypes, e.GetNoneType())
				continue
			}

			returnTypeResult := e.getTypeOfExpression(returnNode.D.Expr, EvalFlagsNone, nil)
			if returnTypeResult.IsIncomplete {
				isIncomplete = true
			}

			returnType := returnTypeResult.Type

			// The original's comment: if the type is a special form, use the
			// special form instead.
			if props := returnType.Base().Props; props != nil && props.SpecialForm != nil {
				returnType = props.SpecialForm
			}

			// The original's comment: if the return type includes an instance of a
			// class with isEmptyContainer set, clear that because we don't want this
			// flag to "leak" into the inferred return type.
			returnType = MapSubtypes(returnType, func(subtype Type) Type {
				if cls, ok := subtype.(*ClassType); ok && IsClassInstance(subtype) && cls.Priv.IsEmptyContainer {
					notEmpty := false
					return ClassTypeSpecialize(cls, cls.Priv.TypeArgs, cls.Priv.IsTypeArgExplicit,
						cls.Priv.IncludeSubclasses, cls.Priv.TupleTypeArgs, &notEmpty)
				}
				return subtype
			}, nil)

			// The original's comment: do not retain TypeForm types in inferred
			// return types.
			returnType = StripTypeForm(returnType)

			// The original's comment: if we're returning a function value (or
			// overload), force lazy return-type inference before caching this
			// function's inferred return. Without this, a partially evaluated
			// function can leak a temporary Any return type (for example,
			// immediately after an edit), and that Any then gets cached as though it
			// were final for the enclosing function.
			e.InferReturnTypeIfNecessary(returnType)

			inferredReturnTypes = append(inferredReturnTypes, returnType)
		}
	}

	if implicitlyReturnsNone {
		inferredReturnTypes = append(inferredReturnTypes, e.GetNoneType())
	}

	// The original's comment: remove any unbound values since those would
	// generate an exception before being returned.
	return RemoveUnbound(CombineTypes(inferredReturnTypes, nil)), isIncomplete
}

// wrapReturnTypeInGenerator is the original's generator block.
func (e *typeEvaluator) wrapReturnTypeInGenerator(
	node *parser.FunctionNode, functionDecl *FunctionDeclaration, inferredReturnType Type,
) Type {
	inferredYieldTypes := []Type{}
	useAwaitableGenerator := false
	isYieldResultUsed := false

	for _, yieldStatement := range functionDecl.YieldStatements {
		if !e.IsNodeReachable(yieldStatement, nil) {
			continue
		}

		if yieldFromNode, ok := yieldStatement.(*parser.YieldFromNode); ok {
			isYieldResultUsed = true
			iteratorTypeResult := e.getTypeOfExpression(yieldFromNode.D.Expr, EvalFlagsNone, nil)

			if IsClassInstance(iteratorTypeResult.Type) &&
				ClassTypeIsBuiltInNamed(iteratorTypeResult.Type.(*ClassType), "Coroutine", "CoroutineType") {
				// The original's comment: handle old-style (pre-await) Coroutines.
				cls := iteratorTypeResult.Type.(*ClassType)
				var yieldType Type = UnknownTypeCreate(false)
				if len(cls.Priv.TypeArgs) > 0 {
					yieldType = cls.Priv.TypeArgs[0]
				}
				inferredYieldTypes = append(inferredYieldTypes, yieldType)
				useAwaitableGenerator = true
			} else {
				var yieldType Type = UnknownTypeCreate(false)
				if result := e.GetTypeOfIterator(iteratorTypeResult, false, yieldFromNode, nil); result != nil {
					yieldType = result.Type
				}
				inferredYieldTypes = append(inferredYieldTypes, yieldType)
			}
			continue
		}

		yieldNode, ok := yieldStatement.(*parser.YieldNode)
		if !ok {
			continue
		}

		// The original's comment: if the yield expression is not by itself in a
		// statement list, assume that its result is consumed.
		if parent := yieldNode.NodeBase().Parent; parent == nil ||
			parent.GetNodeType() != parser.ParseNodeTypeStatementList {
			isYieldResultUsed = true
		}

		if yieldNode.D.Expr != nil {
			inferredYieldTypes = append(inferredYieldTypes,
				e.getTypeOfExpression(yieldNode.D.Expr, EvalFlagsNone, nil).Type)
		} else {
			inferredYieldTypes = append(inferredYieldTypes, e.GetNoneType())
		}
	}

	inferredYieldType := CombineTypes(inferredYieldTypes, nil)

	// The original's comment: inferred yield types need to be wrapped in a
	// Generator or AwaitableGenerator to produce the final result.
	var generatorType Type
	if useAwaitableGenerator {
		generatorType = e.getTypeCheckerInternalsType(node, "AwaitableGenerator")
		if generatorType == nil {
			generatorType = e.getTypingType(node, "AwaitableGenerator")
		}
	} else {
		generatorType = e.getTypingType(node, "Generator")
	}

	if generatorType == nil || !IsInstantiableClass(generatorType) {
		return UnknownTypeCreate(false)
	}

	// The original's comment: the "send type" for the generator (the second type
	// argument) is not generally inferrable, but we can assume that it's Any if
	// the function never uses the value and Unknown if it does. This eliminates
	// any "partially unknown" errors in strict mode in the common case.
	var sendType Type = AnyTypeCreate(false)
	if isYieldResultUsed {
		sendType = UnknownTypeCreate(false)
	}

	typeArgs := []Type{inferredYieldType, sendType, inferredReturnType}
	if useAwaitableGenerator {
		typeArgs = append(typeArgs, AnyTypeCreate(false))
	}

	return ClassTypeCloneAsInstance(
		ClassTypeSpecialize(generatorType.(*ClassType), typeArgs, nil, false, nil, nil), true)
}

// methodAlwaysRaisesNotImplemented corresponds to the function of the same name.
//
// It is deliberately narrow: only a method, only one whose body is a flat list of
// statements, only one with no returns and no yields, and every raise must be a
// bare `raise NotImplementedError` with no `from` clause. Anything more elaborate
// is treated as a real NoReturn.
func (e *typeEvaluator) methodAlwaysRaisesNotImplemented(functionDecl *FunctionDeclaration) bool {
	if functionDecl == nil || !functionDecl.IsMethod ||
		functionDecl.ReturnStatements != nil || functionDecl.YieldStatements != nil ||
		functionDecl.RaiseStatements == nil {
		return false
	}

	functionNode, ok := functionDecl.Node.(*parser.FunctionNode)
	if !ok {
		return false
	}

	for _, statement := range functionNode.D.Suite.D.Statements {
		if statement.GetNodeType() != parser.ParseNodeTypeStatementList {
			return false
		}
	}

	for _, raiseStatement := range functionDecl.RaiseStatements {
		if raiseStatement.D.Expr == nil || raiseStatement.D.FromExpr != nil {
			return false
		}

		raiseType := e.getTypeOfExpression(raiseStatement.D.Expr, EvalFlagsNone, nil).Type

		// The original's conditional expression selects raiseType in both the
		// instantiable and the instance case, so it amounts to "is it a class".
		classType, isClass := raiseType.(*ClassType)
		if !isClass || !DerivesFromStdlibClass(classType, "NotImplementedError") {
			return false
		}
	}

	return true
}
