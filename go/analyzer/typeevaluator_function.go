/*
 * typeevaluator_function.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfFunction, the outer half of function creation.
 *
 * The split in the original is between the shape of the `def` -- its
 * parameters, annotations and type parameters, which is
 * getTypeOfFunctionPredecorated -- and everything applied on top of that shape:
 * the async wrapper, the decorators in reverse source order, and the merge with
 * any previous overloads of the same name. This is the second half.
 *
 * The two halves are separated in the original because the predecorated type is
 * cached under the name node while the decorated type is cached under the
 * function node, and the recursion guard (PartiallyEvaluated) is set around the
 * decorator application only. That structure is preserved: the partially
 * evaluated flag goes on before the decorators run and comes off after, and a
 * re-entrant call that finds the flag set returns the undecorated type rather
 * than recursing.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// GetTypeOfFunction corresponds to getTypeOfFunction. It returns nil where the
// original returns undefined.
func (e *typeEvaluator) GetTypeOfFunction(node *parser.FunctionNode) *FunctionTypeResult {
	if node == nil {
		return nil
	}

	e.initializePrefetchedTypes(node)

	// Is this predecorated function type cached?
	var functionType *FunctionType
	if cached := e.readTypeCache(node.D.Name, evalFlagsNonePtr()); cached != nil {
		if !IsFunction(cached) {
			// The original's comment: this can happen in certain rare
			// circumstances where the function declaration falls within an
			// unreachable code block.
			return nil
		}

		functionType = cached.(*FunctionType)

		if FunctionTypeIsPartiallyEvaluated(functionType) {
			return &FunctionTypeResult{FunctionType: functionType, DecoratedType: functionType}
		}
	} else {
		functionType = e.getTypeOfFunctionPredecorated(node)
	}

	if functionType == nil {
		return nil
	}

	// Is the decorated function type cached?
	if cachedDecorated := e.readTypeCache(node, evalFlagsNonePtr()); cachedDecorated != nil {
		return &FunctionTypeResult{FunctionType: functionType, DecoratedType: cachedDecorated}
	}

	// The original's comment: populate the cache with a temporary value to
	// handle recursion.
	e.writeTypeCache(node, &TypeResult{Type: functionType}, nil, nil, false)

	// The original's comment: if it's an async function, wrap the return type in
	// an Awaitable or Generator. Set the "partially evaluated" flag around this
	// logic to detect recursion.
	functionType.Shared.Flags |= FunctionTypeFlagsPartiallyEvaluated
	var preDecoratedType Type = functionType
	if node.D.IsAsync {
		preDecoratedType = e.createAsyncFunction(node, functionType)
	}

	decoratedType := e.applyFunctionDecorators(node, functionType, preDecoratedType)

	// The original's comment: see if there are any overloads provided by
	// previous function declarations.
	if IsFunction(decoratedType) {
		asFunction := decoratedType.(*FunctionType)
		asFunction.Shared.DeprecatedMessage = functionType.Shared.DeprecatedMessage

		if FunctionTypeIsOverloaded(asFunction) {
			// The original's comment: mark all the parameters as accessed.
			for _, param := range node.D.Params {
				e.markParamAccessed(param)
			}
		}
	}

	decoratedType = AddOverloadsToFunctionType(e, node, decoratedType)

	e.writeTypeCache(node, &TypeResult{Type: decoratedType}, evalFlagsNonePtr(), nil, false)

	// The original's comment: now that the decorator has been applied, we can
	// clear the "partially evaluated" flag.
	functionType.Shared.Flags &^= FunctionTypeFlagsPartiallyEvaluated

	return &FunctionTypeResult{FunctionType: functionType, DecoratedType: decoratedType}
}

// applyFunctionDecorators is the original's decorator loop, which runs in
// reverse source order. It matches applyClassDecorators in shape; the two
// differ only in which decorator applier and which diagnostic rule they use.
func (e *typeEvaluator) applyFunctionDecorators(
	node *parser.FunctionNode,
	functionType *FunctionType,
	preDecoratedType Type,
) Type {
	decoratedType := preDecoratedType
	foundUnknown := false

	for i := len(node.D.Decorators) - 1; i >= 0; i-- {
		decorator := node.D.Decorators[i]

		var trackerNode parser.ParseNode = node.NodeBase().Parent
		if trackerNode == nil {
			trackerNode = node
		}

		captured := decoratedType
		var newDecoratedType Type
		e.withSignatureTracker(trackerNode, func() {
			// The original asserts decoratedType is defined here.
			newDecoratedType = e.applyFunctionDecorator(captured, functionType, decorator, node)
		})

		unknownOrAny := ContainsAnyOrUnknown(newDecoratedType, false)

		if unknownOrAny != nil && IsUnknown(unknownOrAny) {
			// The original's comment: report this error only on the first
			// unknown type.
			if !foundUnknown {
				e.AddDiagnostic(
					DiagnosticRuleReportUntypedFunctionDecorator,
					localization.LocMessage.FunctionDecoratorTypeUnknown(),
					node.D.Decorators[i].D.Expr,
					nil,
				)

				foundUnknown = true
			}
		} else {
			// The original's comment: apply the decorator only if the type is
			// known.
			decoratedType = newDecoratedType
		}
	}

	return decoratedType
}

/*
 * The five things function creation reaches that are separate units of work.
 */

// createAsyncFunction corresponds to the function of the same name. The
// original's comment: clone the original function and replace its return type
// with an Awaitable[<returnType>], marking the new function as no longer async.
func (e *typeEvaluator) createAsyncFunction(_ *parser.FunctionNode, functionType *FunctionType) Type {
	e.unported("createAsyncFunction")
	return functionType
}

// applyFunctionDecorator corresponds to the decorators.ts function of the same
// name. Returning the input type unchanged is what the original does when the
// decorator's own type is unknown, so an undecorated function is a shape the
// caller already handles.
func (e *typeEvaluator) applyFunctionDecorator(
	inputFunctionType Type,
	_ *FunctionType,
	_ *parser.DecoratorNode,
	_ *parser.FunctionNode,
) Type {
	e.unported("applyFunctionDecorator")
	return inputFunctionType
}
