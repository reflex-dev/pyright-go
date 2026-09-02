/*
 * typecacheutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Utilities for managing type caches.
 *
 * Transliterated from analyzer/typeCacheUtils.ts (pyright 1.1.412).
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// SpeculativeCache stands in for the `Map<number, any>` that SpeculativeEntry
// holds. The tracker only ever deletes from it, and the concrete caches the
// evaluator passes have different value types, so an interface carrying just
// that operation avoids threading a type parameter through the whole tracker.
type SpeculativeCache interface {
	Delete(id int) bool
}

// speculativeEntry tracks a speculative entry that needs to be cleaned up when
// it goes out of scope.
type speculativeEntry struct {
	Cache SpeculativeCache
	ID    int
}

// SpeculativeContext corresponds to the interface of the same name. It is
// exported because DisableSpeculativeMode hands the stack back to the caller.
type SpeculativeContext struct {
	SpeculativeRootNode parser.ParseNode
	EntriesToUndo       []speculativeEntry
	DependentType       Type
	AllowDiagnostics    bool
}

// DependentType corresponds to the interface of the same name.
type DependentType struct {
	SpeculativeRootNode parser.ParseNode
	DependentType       Type
}

// typeCacheUtils.ts declares its own `TypeResult` -- `{ type, isIncomplete? }`
// -- which is a structural subset of the evaluator's, so in TypeScript the
// evaluator hands its own results straight to the cache and the same object
// satisfies both. Two types of the same name cannot coexist in one Go package,
// and duplicating the smaller one would mean copying at the boundary that the
// original does not do, so this file uses the evaluator's TypeResult. See
// typeevaluatortypes.go.

// ContextualTypeCacheEntry corresponds to the interface of the same name. The
// TypeScript uses structural typing and `T extends ContextualTypeCacheEntry`;
// Go needs the accessor spelled out.
type ContextualTypeCacheEntry interface {
	// GetExpectedType returns `expectedType`, which is nil where the
	// TypeScript has `undefined`.
	GetExpectedType() Type
}

// SpeculativeTypeEntry corresponds to the interface of the same name.
type SpeculativeTypeEntry struct {
	ExpectedType Type

	TypeResult                TypeResult
	IncompleteGenerationCount int
	DependentTypes            []DependentType
}

// GetExpectedType implements ContextualTypeCacheEntry.
func (e *SpeculativeTypeEntry) GetExpectedType() Type { return e.ExpectedType }

// SpeculativeModeOptions corresponds to the interface of the same name. The
// TypeScript leaves the whole options object undefined; pass nil for that.
type SpeculativeModeOptions struct {
	// DependentType, if set, is the dependent type the cached speculative
	// result depends on.
	DependentType Type

	// AllowDiagnostics overrides the usual suppression of diagnostics for nodes
	// under a speculative root.
	AllowDiagnostics bool
}

const maxContextualTypeCacheEntriesPerNode = 8

// ContextualTypeCacheEntryMatches corresponds to
// contextualTypeCacheEntryMatches.
func ContextualTypeCacheEntryMatches(entry ContextualTypeCacheEntry, expectedType Type) bool {
	if expectedType != nil {
		return entry.GetExpectedType() != nil &&
			IsTypeSame(expectedType, entry.GetExpectedType(), TypeSameOptions{}, 0)
	}
	return entry.GetExpectedType() == nil
}

// AddContextualTypeCacheEntry corresponds to addContextualTypeCacheEntry. The
// TypeScript leaves isEntryValid undefined; pass nil for that.
func AddContextualTypeCacheEntry[T ContextualTypeCacheEntry](
	cacheEntries []T,
	newEntry T,
	isEntryValid func(entry T) bool,
) []T {
	newCacheEntries := []T{}
	for _, entry := range cacheEntries {
		if (isEntryValid == nil || isEntryValid(entry)) &&
			!ContextualTypeCacheEntryMatches(entry, newEntry.GetExpectedType()) {
			newCacheEntries = append(newCacheEntries, entry)
		}
	}

	newCacheEntries = append(newCacheEntries, newEntry)
	if len(newCacheEntries) > maxContextualTypeCacheEntriesPerNode {
		newCacheEntries = newCacheEntries[len(newCacheEntries)-maxContextualTypeCacheEntriesPerNode:]
	}

	return newCacheEntries
}

// SpeculativeTypeTracker maintains a stack of "speculative type contexts". When
// a context is popped off the stack, all of the speculative type cache entries
// that were created within that context are removed from the corresponding type
// caches because they are no longer valid.
//
// The tracker also contains a map of "speculative types" that are contextually
// evaluated based on an "expected type" and potentially one or more "dependent
// types". The "expected type" applies in cases where the speculative root node
// is being evaluated with bidirectional type inference. Dependent types apply in
// cases where the type of many subnodes depends on the expected type of a parent
// node, as in the case of lambda type inference.
type SpeculativeTypeTracker struct {
	speculativeContextStack []*SpeculativeContext
	speculativeTypeCache    map[int][]*SpeculativeTypeEntry
	activeDependentTypes    []DependentType
}

// NewSpeculativeTypeTracker corresponds to the field initializers.
func NewSpeculativeTypeTracker() *SpeculativeTypeTracker {
	return &SpeculativeTypeTracker{
		speculativeContextStack: []*SpeculativeContext{},
		speculativeTypeCache:    map[int][]*SpeculativeTypeEntry{},
		activeDependentTypes:    []DependentType{},
	}
}

// EnterSpeculativeContext corresponds to enterSpeculativeContext.
func (t *SpeculativeTypeTracker) EnterSpeculativeContext(
	speculativeRootNode parser.ParseNode,
	options *SpeculativeModeOptions,
) {
	var dependentType Type
	allowDiagnostics := false
	if options != nil {
		dependentType = options.DependentType
		allowDiagnostics = options.AllowDiagnostics
	}

	t.speculativeContextStack = append(t.speculativeContextStack, &SpeculativeContext{
		SpeculativeRootNode: speculativeRootNode,
		EntriesToUndo:       []speculativeEntry{},
		DependentType:       dependentType,
		AllowDiagnostics:    allowDiagnostics,
	})

	// Retain a list of active dependent types. This information is already
	// contained within the speculative context stack, but we retain a copy in
	// this alternate form for performance reasons.
	if dependentType != nil {
		t.activeDependentTypes = append(t.activeDependentTypes, DependentType{
			SpeculativeRootNode: speculativeRootNode,
			DependentType:       dependentType,
		})
	}
}

// LeaveSpeculativeContext corresponds to leaveSpeculativeContext.
func (t *SpeculativeTypeTracker) LeaveSpeculativeContext() {
	assert(len(t.speculativeContextStack) > 0, "")
	context := t.speculativeContextStack[len(t.speculativeContextStack)-1]
	t.speculativeContextStack = t.speculativeContextStack[:len(t.speculativeContextStack)-1]

	if context.DependentType != nil {
		assert(len(t.activeDependentTypes) > 0, "")
		t.activeDependentTypes = t.activeDependentTypes[:len(t.activeDependentTypes)-1]
	}

	// Delete all of the speculative type cache entries that were tracked in
	// this context.
	for _, entry := range context.EntriesToUndo {
		entry.Cache.Delete(entry.ID)
	}
}

// IsSpeculative corresponds to isSpeculative. The TypeScript defaults
// ignoreIfDiagnosticsAllowed to false.
func (t *SpeculativeTypeTracker) IsSpeculative(node parser.ParseNode, ignoreIfDiagnosticsAllowed bool) bool {
	if len(t.speculativeContextStack) == 0 {
		return false
	}

	if node == nil {
		return true
	}

	for i := len(t.speculativeContextStack) - 1; i >= 0; i-- {
		stackEntry := t.speculativeContextStack[i]
		if IsNodeContainedWithin(node, stackEntry.SpeculativeRootNode) {
			if !ignoreIfDiagnosticsAllowed || !stackEntry.AllowDiagnostics {
				return true
			}
		}
	}

	return false
}

// TrackEntry corresponds to trackEntry.
func (t *SpeculativeTypeTracker) TrackEntry(cache SpeculativeCache, id int) {
	stackSize := len(t.speculativeContextStack)
	if stackSize > 0 {
		context := t.speculativeContextStack[stackSize-1]
		context.EntriesToUndo = append(context.EntriesToUndo, speculativeEntry{Cache: cache, ID: id})
	}
}

// DisableSpeculativeMode temporarily disables speculative mode, clearing the
// stack of speculative contexts. It returns the stack so the caller can later
// restore it by calling EnableSpeculativeMode.
func (t *SpeculativeTypeTracker) DisableSpeculativeMode() []*SpeculativeContext {
	stack := t.speculativeContextStack
	t.speculativeContextStack = []*SpeculativeContext{}
	return stack
}

// EnableSpeculativeMode corresponds to enableSpeculativeMode.
func (t *SpeculativeTypeTracker) EnableSpeculativeMode(stack []*SpeculativeContext) {
	assert(len(t.speculativeContextStack) == 0, "")
	t.speculativeContextStack = stack
}

// AddSpeculativeType corresponds to addSpeculativeType.
func (t *SpeculativeTypeTracker) AddSpeculativeType(
	node parser.ParseNode,
	typeResult TypeResult,
	incompleteGenerationCount int,
	expectedType Type,
) {
	assert(len(t.speculativeContextStack) > 0, "")

	newEntry := &SpeculativeTypeEntry{
		TypeResult:                typeResult,
		ExpectedType:              expectedType,
		IncompleteGenerationCount: incompleteGenerationCount,
	}

	if len(t.activeDependentTypes) > 0 {
		newEntry.DependentTypes = append([]DependentType(nil), t.activeDependentTypes...)
	}

	cacheEntries := AddContextualTypeCacheEntry(
		t.speculativeTypeCache[node.NodeBase().ID],
		newEntry,
		func(entry *SpeculativeTypeEntry) bool {
			return !entry.TypeResult.IsIncomplete ||
				entry.IncompleteGenerationCount == incompleteGenerationCount
		},
	)
	t.speculativeTypeCache[node.NodeBase().ID] = cacheEntries
}

// GetSpeculativeType corresponds to getSpeculativeType.
func (t *SpeculativeTypeTracker) GetSpeculativeType(
	node parser.ParseNode,
	expectedType Type,
) *SpeculativeTypeEntry {
	withinSpeculativeRoot := false
	for _, context := range t.speculativeContextStack {
		if IsNodeContainedWithin(node, context.SpeculativeRootNode) {
			withinSpeculativeRoot = true
			break
		}
	}

	if withinSpeculativeRoot {
		for _, entry := range t.speculativeTypeCache[node.NodeBase().ID] {
			if ContextualTypeCacheEntryMatches(entry, expectedType) && t.dependentTypesMatch(entry) {
				return entry
			}
		}
	}

	return nil
}

// dependentTypesMatch determines whether a cache entry matches the current set
// of active dependent types. If not, the cache entry can't be used in the
// current context.
func (t *SpeculativeTypeTracker) dependentTypesMatch(entry *SpeculativeTypeEntry) bool {
	cachedDependentTypes := entry.DependentTypes
	if len(cachedDependentTypes) != len(t.activeDependentTypes) {
		return false
	}

	for index, cachedDepType := range cachedDependentTypes {
		activeDepType := t.activeDependentTypes[index]
		if cachedDepType.SpeculativeRootNode != activeDepType.SpeculativeRootNode {
			return false
		}

		if !IsTypeSame(cachedDepType.DependentType, activeDepType.DependentType, TypeSameOptions{}, 0) {
			return false
		}
	}

	return true
}
