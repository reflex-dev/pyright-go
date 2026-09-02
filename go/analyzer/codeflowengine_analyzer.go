/*
 * codeflowengine_analyzer.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/codeFlowEngine.ts (pyright 1.1.412):
 * FlowNodeTypeResult, FlowNodeTypeOptions, IncompleteType, IncompleteSubtypeInfo,
 * CodeFlowTypeCache, CodeFlowAnalyzer and createCodeFlowAnalyzer; and from
 * analyzer/typeEvaluator.ts: getFlowTypeOfReference and
 * getCodeFlowAnalyzerForNode.
 *
 * The entry into narrowing. `x.bit_length()` after `if x is not None` needs the
 * type of `x` AT THAT POINT rather than the type of the symbol, and that is what
 * this computes: walk the control flow graph backwards from the reference,
 * applying every narrowing condition on the way.
 *
 * A CodeFlowAnalyzer is per execution scope and per starting type, and it exists
 * to hold a cache keyed by flow node. The cache is not an optimization: a loop
 * makes the graph cyclic, so an entry has to be written before its own
 * computation finishes, and the incomplete-type machinery is how a partial
 * answer is recorded and then refined. That is why CachedType is a union of "a
 * type" and "an IncompleteType holding the subtypes seen so far".
 *
 * Two caches sit above it, and the distinction matters:
 *
 *   - codeFlowAnalyzerCache, on the evaluator, maps an execution scope to the
 *     analyzers built for it. There can be several, because a different starting
 *     type produces different answers; the lookup compares typeAtStart by
 *     isTypeSame, not by identity.
 *   - flowNodeTypeCacheSet, inside one analyzer, maps a reference key to that
 *     reference's per-flow-node cache. Separate references never share entries.
 *
 * getFlowTypeOfReference short-circuits before any of that when the binder
 * recorded no code flow expressions for this reference in this scope: nothing
 * assigns to it here, so there is nothing to narrow.
 */

package analyzer

import (
	"fmt"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// FlowNodeTypeResult corresponds to the interface of the same name. A nil Type
// means "code flow determined nothing", which leaves the caller's type in place.
type FlowNodeTypeResult struct {
	Type               Type
	IsIncomplete       bool
	GenerationCount    *int
	IncompleteSubtypes []*IncompleteSubtypeInfo
}

// NewFlowNodeTypeResult corresponds to FlowNodeTypeResult.create.
func NewFlowNodeTypeResult(
	t Type, isIncomplete bool, generationCount *int, incompleteSubtypes []*IncompleteSubtypeInfo,
) *FlowNodeTypeResult {
	return &FlowNodeTypeResult{
		Type:               t,
		IsIncomplete:       isIncomplete,
		GenerationCount:    generationCount,
		IncompleteSubtypes: incompleteSubtypes,
	}
}

// IncompleteSubtypeInfo corresponds to the interface of the same name: one
// antecedent's contribution to a loop label, and the bookkeeping that decides
// whether it needs re-evaluating.
type IncompleteSubtypeInfo struct {
	Type            Type
	IsIncomplete    bool
	IsPending       bool
	EvaluationCount int
}

// IncompleteType corresponds to the interface of the same name.
//
// The original distinguishes it from a plain Type by the presence of the
// isIncompleteType property; a distinct Go type makes the type switch explicit.
type IncompleteType struct {
	// Type is the original's comment: type computed so far.
	Type Type

	// IncompleteSubtypes is the original's comment: array of incomplete subtypes
	// that have been computed so far (used for loops).
	IncompleteSubtypes []*IncompleteSubtypeInfo

	// GenerationCount is the original's comment: tracks whether something has
	// changed since this cache entry was written that might change the incomplete
	// type; if this doesn't match the global "incomplete generation count", this
	// cached value is stale.
	GenerationCount int

	// IsRecursionSentinel is the original's comment: indicates that the cache
	// entry represents a sentinel value used to detect and prevent recursion.
	IsRecursionSentinel bool
}

// CachedType corresponds to `Type | IncompleteType`. Go has no untagged union,
// so entries are stored as `any` and discriminated by type assertion, which is
// what the original's isIncompleteType guard does.
type CachedType = any

// codeFlowTypeCache corresponds to the interface of the same name. The cache
// distinguishes an absent key from a key holding undefined, which is why its
// value type is CachedType rather than Type.
type codeFlowTypeCache struct {
	Cache                  *common.OrderedMap[int, CachedType]
	PendingNodes           *common.OrderedSet[int]
	ClosedFinallyGateNodes *common.OrderedSet[int]
}

// CodeFlowAnalyzer corresponds to the interface of the same name. The original
// is a closure over flowNodeTypeCacheSet; the struct holds the same state.
type CodeFlowAnalyzer struct {
	evaluator *typeEvaluator

	flowNodeTypeCacheSet *common.OrderedMap[string, *codeFlowTypeCache]
}

// createCodeFlowAnalyzer corresponds to the function of the same name. The
// original's comment: creates a new code flow analyzer that can be used to narrow
// the types of the expressions within an execution context. Each code flow
// analyzer instance maintains a cache of types it has already determined.
func (e *typeEvaluator) createCodeFlowAnalyzer() *CodeFlowAnalyzer {
	return &CodeFlowAnalyzer{
		evaluator:            e,
		flowNodeTypeCacheSet: common.NewOrderedMap[string, *codeFlowTypeCache](),
	}
}

// getFlowNodeTypeCacheForReference corresponds to the function of the same name.
func (a *CodeFlowAnalyzer) getFlowNodeTypeCacheForReference(referenceKey string) *codeFlowTypeCache {
	if cache, ok := a.flowNodeTypeCacheSet.Get(referenceKey); ok {
		return cache
	}

	cache := &codeFlowTypeCache{
		Cache:                  common.NewOrderedMap[int, CachedType](),
		PendingNodes:           common.NewOrderedSet[int](),
		ClosedFinallyGateNodes: common.NewOrderedSet[int](),
	}
	a.flowNodeTypeCacheSet.Set(referenceKey, cache)
	return cache
}

// isGetTypeFromCodeFlowPending corresponds to the function of the same name.
//
// The original's comment: determines whether any calls to getTypeFromCodeFlow are
// pending for an expression other than referenceKeyFilter. This is important in
// cases where the type of one expression depends on the type of another in a
// loop. If there are other pending evaluations, we will mark the current
// evaluation as incomplete and return back to the pending evaluation.
func (a *CodeFlowAnalyzer) isGetTypeFromCodeFlowPending(referenceKeyFilter string) bool {
	if referenceKeyFilter == "" {
		return false
	}

	pending := false
	a.flowNodeTypeCacheSet.ForEach(func(value *codeFlowTypeCache, key string) {
		if key != referenceKeyFilter && value.PendingNodes.Size() > 0 {
			pending = true
		}
	})

	return pending
}

/*
 * The evaluator's half.
 */

// getFlowTypeOfReference corresponds to the function of the same name.
func (e *typeEvaluator) getFlowTypeOfReference(
	reference CodeFlowReferenceExpressionNode,
	startNode parser.ParseNode,
	options *flowTypeOptions,
) *TypeResult {
	result := e.getFlowNodeTypeOfReference(reference, startNode, options)
	return &TypeResult{Type: result.Type, IsIncomplete: result.IsIncomplete}
}

// getFlowNodeTypeOfReference is getFlowTypeOfReference proper; the wrapper above
// narrows the result to the TypeResult the evaluator's callers expect.
func (e *typeEvaluator) getFlowNodeTypeOfReference(
	reference CodeFlowReferenceExpressionNode,
	startNode parser.ParseNode,
	options *flowTypeOptions,
) *FlowNodeTypeResult {
	// The original's comment: see if this execution scope requires code flow for
	// this reference expression.
	referenceKey := CreateKeyForReference(reference)

	scopeSearchNode := parser.ParseNode(reference)
	if startNode != nil && startNode.NodeBase().Parent != nil {
		scopeSearchNode = startNode.NodeBase().Parent
	}
	executionNode := GetExecutionScopeNode(scopeSearchNode)

	codeFlowExpressions := GetCodeFlowExpressions(executionNode)
	if codeFlowExpressions == nil ||
		(!codeFlowExpressions.Has(referenceKey) && !codeFlowExpressions.Has(WildcardImportReferenceKey)) {
		return NewFlowNodeTypeResult(nil, false, nil, nil)
	}

	if e.checkCodeFlowTooComplex(reference) {
		// Giving up on an unbound start produces Unknown rather than nothing, so
		// the caller does not go on to report an unbound-variable error it cannot
		// substantiate.
		var t Type
		if options != nil && options.TypeAtStart != nil && IsUnbound(options.TypeAtStart.Type) {
			t = UnknownTypeCreate(false)
		}
		return NewFlowNodeTypeResult(t, true, nil, nil)
	}

	// The original's comment: is there an code flow analyzer cached for this
	// execution scope?
	var analyzer *CodeFlowAnalyzer

	if e.isNodeInReturnTypeInferenceContext(executionNode) {
		// The original's comment: if we're performing the analysis within a
		// temporary context of a function for purposes of inferring its return
		// type for a specified set of arguments, use a temporary analyzer that
		// we'll use only for this context.
		analyzer, _ = e.getCodeFlowAnalyzerForReturnTypeInferenceContext().(*CodeFlowAnalyzer)
	} else {
		var typeAtStart *TypeResult
		if options != nil {
			typeAtStart = options.TypeAtStart
		}
		analyzer = e.getCodeFlowAnalyzerForNode(executionNode, typeAtStart)
	}

	if analyzer == nil {
		return NewFlowNodeTypeResult(nil, false, nil, nil)
	}

	flowSearchNode := parser.ParseNode(reference)
	if startNode != nil {
		flowSearchNode = startNode
	}

	flowNode := GetFlowNode(flowSearchNode)
	if flowNode == nil {
		return NewFlowNodeTypeResult(nil, false, nil, nil)
	}

	return analyzer.GetTypeFromCodeFlow(flowNode, reference, options)
}

// getCodeFlowAnalyzerForNode corresponds to the function of the same name.
//
// The cache is keyed by execution scope, but a scope can hold several analyzers:
// a different starting type yields different narrowed results, so an entry
// matches only when both are absent or both are present and the same.
func (e *typeEvaluator) getCodeFlowAnalyzerForNode(
	node parser.ExecutionScopeNode, typeAtStart *TypeResult,
) *CodeFlowAnalyzer {
	nodeID := node.NodeBase().ID
	entries, hasEntries := e.codeFlowAnalyzerCache.Get(nodeID)

	if hasEntries {
		for _, entry := range entries {
			if codeFlowTypeAtStartMatches(typeAtStart, entry.TypeAtStart) {
				if analyzer, ok := entry.CodeFlowAnalyzer.(*CodeFlowAnalyzer); ok {
					return analyzer
				}
			}
		}
	}

	// The original's comment: allocate a new code flow analyzer.
	analyzer := e.createCodeFlowAnalyzer()
	entry := &CodeFlowAnalyzerCacheEntry{TypeAtStart: typeAtStart, CodeFlowAnalyzer: analyzer}

	if hasEntries {
		e.codeFlowAnalyzerCache.Set(nodeID, append(entries, entry))
	} else {
		e.codeFlowAnalyzerCache.Set(nodeID, []*CodeFlowAnalyzerCacheEntry{entry})
	}

	return analyzer
}

// codeFlowTypeAtStartMatches is the original's cache-entry predicate.
func codeFlowTypeAtStartMatches(typeAtStart, cached *TypeResult) bool {
	if typeAtStart == nil || cached == nil {
		return typeAtStart == nil && cached == nil
	}

	if typeAtStart.IsIncomplete != cached.IsIncomplete {
		return false
	}

	return IsTypeSame(typeAtStart.Type, cached.Type, TypeSameOptions{}, 0)
}

// referenceKeyWithSymbolID is the key getTypeFromCodeFlow caches under. The
// original builds `referenceKey + '.' + targetSymbolId`, and falls back to the
// bare '.' when either is absent -- so a reachability-only query (no reference)
// shares one cache regardless of symbol.
//
// The original's targetSymbolId is optional; flowTypeOptions holds a plain int,
// so an absent options object is the only way it can be missing. Every caller
// that passes options sets it, including to indeterminateSymbolId.
func referenceKeyWithSymbolID(referenceKey string, options *flowTypeOptions) string {
	if referenceKey == "" || options == nil {
		return "."
	}
	return fmt.Sprintf("%s.%d", referenceKey, options.TargetSymbolID)
}
