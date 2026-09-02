/*
 * typeevaluator_context.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412): getType and
 * the contextual-evaluation entry points beneath it.
 *
 * This is the top of the wall rather than a way around it. getType does not
 * evaluate anything itself: it asks for the node's cached type, and if there
 * isn't one, runs a callback that evaluates whatever surrounding construct the
 * node belongs to and then reads the cache again. Everything interesting is in
 * that callback.
 *
 * Porting this layer does two things. It puts the type caches from
 * typeevaluator.go on a live path for the first time -- until now nothing had
 * ever read or written them. And it moves the frontier one level deeper: the
 * corpus stops reporting `GetType` and starts reporting
 * `evaluateTypesForExpressionInContext`, which is the actual next unit of work.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// GetType determines the type of the specified node by evaluating it in
// context, logging any errors in the process. This may require the type of
// surrounding statements to be evaluated.
//
// Returns nil where the original returns undefined.
func (e *typeEvaluator) GetType(node parser.ExpressionNode) Type {
	e.initializePrefetchedTypes(node)

	var t Type
	if typeResult := e.evaluateContextualTypeForSubnode(node, func() {
		e.evaluateTypesForExpressionInContext(node)
	}); typeResult != nil {
		t = typeResult.Type
	}

	// The original's comment: if this is a type parameter with a calculated
	// variance, see if we can swap it out for a version that has a computed
	// variance. That branch needs getTypeOfClass and getTypeOfTypeAlias, so it
	// is not reachable yet; it records itself rather than being skipped in
	// silence, because a TypeVar with Auto variance that comes back unswapped
	// is a wrong answer rather than a missing one.
	if t != nil && IsTypeVar(t) && t.(*TypeVarType).Shared.DeclaredVariance == VarianceAuto {
		e.unported("getType.autoVarianceTypeParam")
	}

	if t != nil {
		t = TransformPossibleRecursiveTypeAlias(t, 0)
	}

	return t
}

// GetTypeResult corresponds to getTypeResult.
func (e *typeEvaluator) GetTypeResult(node parser.ExpressionNode) *TypeResult {
	return e.evaluateContextualTypeForSubnode(node, func() {
		e.evaluateTypesForExpressionInContext(node)
	})
}

// GetTypeResultForDecorator corresponds to getTypeResultForDecorator.
func (e *typeEvaluator) GetTypeResultForDecorator(node *parser.DecoratorNode) *TypeResult {
	return e.evaluateContextualTypeForSubnode(node, func() {
		e.evaluateTypesForExpressionInContext(node.D.Expr)
	})
}

func (e *typeEvaluator) evaluateContextualTypeForSubnode(subnode parser.ParseNode, callback func()) *TypeResult {
	return e.evaluateTypeForSubnodeWithCache(subnode, callback, e.readContextualTypeCacheEntryForNode)
}

// evaluateTypeForSubnodeWithCache corresponds to the function of the same name.
func (e *typeEvaluator) evaluateTypeForSubnodeWithCache(
	subnode parser.ParseNode,
	callback func(),
	readCacheEntry func(node parser.ParseNode) *TypeCacheEntry,
) *TypeResult {
	// The original's comment: if the type cache is already populated with a
	// complete type, don't bother doing additional work.
	cacheEntry := readCacheEntry(subnode)
	if cacheEntry != nil && !cacheEntry.TypeResult.IsIncomplete {
		typeResult := cacheEntry.TypeResult

		// The original's comment: handle the special case where a function or
		// class is partially evaluated. Indicate that these are not complete
		// types.
		//
		// `{ ...typeResult, isIncomplete: true }` copies rather than mutates,
		// because the cached result must not be marked incomplete in place.
		if IsFunction(typeResult.Type) && FunctionTypeIsPartiallyEvaluated(typeResult.Type.(*FunctionType)) {
			copied := *typeResult
			copied.IsIncomplete = true
			return &copied
		}

		if IsClass(typeResult.Type) && ClassTypeIsPartiallyEvaluated(typeResult.Type.(*ClassType)) {
			copied := *typeResult
			copied.IsIncomplete = true
			return &copied
		}

		return typeResult
	}

	callback()
	cacheEntry = readCacheEntry(subnode)
	if cacheEntry != nil {
		return cacheEntry.TypeResult
	}

	return nil
}

// readContextualTypeCacheEntryForNode corresponds to the function of the same
// name. The original's comment: contextual reads consult expectedTypeCache and
// prefer a matching TypeForm result. Runtime-only consumers must use
// readTypeCacheEntry so this precedence is not accidentally applied where an
// ordinary runtime type is required.
func (e *typeEvaluator) readContextualTypeCacheEntryForNode(node parser.ParseNode) *TypeCacheEntry {
	var expectedType Type
	if entry, ok := e.expectedTypeCache.Get(node.NodeBase().ID); ok {
		expectedType = entry.Type
	}

	if expectedType != nil && expectedTypeWantsTypeForm(expectedType) {
		if entry := e.readTypeFormTypeCacheEntry(node, expectedType); entry != nil {
			return &entry.TypeCacheEntry
		}
		if entry := e.readTypeFormTypeCacheEntry(node, nil); entry != nil {
			return &entry.TypeCacheEntry
		}
		return e.readTypeCacheEntry(node)
	}

	if entry := e.readTypeCacheEntry(node); entry != nil {
		return entry
	}
	if entry := e.readTypeFormTypeCacheEntry(node, nil); entry != nil {
		return &entry.TypeCacheEntry
	}
	return nil
}

// initializePrefetchedTypes is in typeevaluator_prefetch.go, alongside the
// module and builtin lookups it is built from.
