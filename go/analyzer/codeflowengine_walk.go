/*
 * codeflowengine_walk.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/codeFlowEngine.ts (pyright 1.1.412):
 * getTypeFromCodeFlow and everything nested inside it -- setCacheEntry,
 * setIncompleteSubtype, getCacheEntry, deleteCacheEntry,
 * cleanIncompleteUnknownForCacheEntry, evaluateAssignmentFlowNode,
 * preventRecursion, getTypeFromFlowNode, getTypeFromBranchFlowNode,
 * getTypeFromLoopFlowNode, getTypeFromPreFinallyGateFlowNode and
 * getTypeFromPostFinallyFlowNode.
 *
 * The narrowing walk. Starting at the reference's flow node it moves backwards
 * through antecedents; each node kind either answers, transforms, or steps back
 * one link. Assignments answer with the assigned type, conditions apply a
 * narrowing callback, branch labels union their antecedents, and the Start node
 * answers with typeAtStart.
 *
 * The whole difficulty is loops, which make the graph cyclic. A loop label
 * cannot be evaluated by recursion alone, so:
 *
 *   - Before evaluating an antecedent, its slot is marked pending. Re-entering
 *     it finds the pending mark and returns an incomplete Unknown instead of
 *     recursing forever.
 *   - Each antecedent's contribution is stored separately in incompleteSubtypes,
 *     and the label's type is their union, recomputed on every write.
 *   - Whether a stored partial answer is still valid is decided by comparing its
 *     generationCount against a counter that is bumped whenever anything that
 *     could change an incomplete type changes. A stale entry is recomputed.
 *   - The loop runs until nothing is incomplete or until every antecedent has
 *     had a turn, and a per-antecedent evaluation count caps pathological cases
 *     that never converge at 256 attempts.
 *
 * `reference` being absent switches the whole walk into reachability mode: any
 * non-Never type proves reachability, so branch and loop evaluation stop at the
 * first antecedent that produces one rather than computing a union.
 *
 * The incomplete-Unknown cleaning deserves a note. An incomplete Unknown inside
 * a union is a placeholder for "not yet computed", not an answer; leaving it in
 * would resolve the cycle to a type contaminated by Unknown. It is stripped
 * whenever a partial answer is handed out.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// maxConvergenceAttemptLimit is the original's constant, with its comment: in
// rare circumstances, it's possible for types in a loop not to converge. This can
// happen, for example, if there are many symbols that depend on each other and
// their types depend on complex overloads that can resolve to Any under certain
// circumstances. This defines the max number of times we'll attempt to evaluate
// an antecedent in a loop before we give up and "pin" the evaluated type for that
// antecedent. The number is somewhat arbitrary. Too low and it will cause
// incorrect types to be evaluated even when types could converge. Too high, and
// it will cause long hangs before giving up.
const maxConvergenceAttemptLimit = 256

// codeFlowWalk holds what the original's getTypeFromCodeFlow closes over for one
// call: the reference being narrowed, its cache, and its key.
type codeFlowWalk struct {
	analyzer  *CodeFlowAnalyzer
	evaluator *typeEvaluator

	reference CodeFlowReferenceExpressionNode
	options   *flowTypeOptions

	referenceKey             string
	hasReference             bool
	referenceKeyWithSymbolID string
	cache                    *codeFlowTypeCache

	// subexpressionReferenceKeys is computed lazily, as the original's is; the
	// branch and loop skip checks are the only consumers and most walks never
	// reach one.
	subexpressionReferenceKeys []string
	haveSubexpressionKeys      bool
}

// GetTypeFromCodeFlow corresponds to the method of the same name.
//
// The original's comment: this function has two primary modes. The first is used
// to determine the narrowed type of a reference expression based on code flow
// analysis. The second (when reference is undefined) is used to determine whether
// the specified flowNode is reachable when "never narrowing" is applied.
func (a *CodeFlowAnalyzer) GetTypeFromCodeFlow(
	flowNode FlowNode, reference CodeFlowReferenceExpressionNode, options *flowTypeOptions,
) *FlowNodeTypeResult {
	hasReference := reference != nil

	referenceKey := ""
	if hasReference {
		referenceKey = CreateKeyForReference(reference)
	}

	keyWithSymbolID := referenceKeyWithSymbolID(referenceKey, options)

	w := &codeFlowWalk{
		analyzer:                 a,
		evaluator:                a.evaluator,
		reference:                reference,
		options:                  options,
		referenceKey:             referenceKey,
		hasReference:             hasReference,
		referenceKeyWithSymbolID: keyWithSymbolID,
		cache:                    a.getFlowNodeTypeCacheForReference(keyWithSymbolID),
	}

	if flowNode == nil {
		// The original's comment: this should happen only in cases where we're
		// evaluating parse nodes that are created after the initial parse (namely,
		// string literals that are used for forward referenced types).
		return NewFlowNodeTypeResult(w.typeAtStart(), w.typeAtStartIsIncomplete(), nil, nil)
	}

	return w.getTypeFromFlowNode(flowNode)
}

func (w *codeFlowWalk) typeAtStart() Type {
	if w.options == nil || w.options.TypeAtStart == nil {
		return nil
	}
	return w.options.TypeAtStart.Type
}

func (w *codeFlowWalk) typeAtStartIsIncomplete() bool {
	return w.options != nil && w.options.TypeAtStart != nil && w.options.TypeAtStart.IsIncomplete
}

func (w *codeFlowWalk) skipConditionalNarrowing() bool {
	return w.options != nil && w.options.SkipConditionalNarrowing
}

/*
 * The cache.
 */

// setCacheEntry corresponds to the function of the same name. The original's
// comment: caches the type of the flow node in our local cache, keyed by the flow
// node ID.
func (w *codeFlowWalk) setCacheEntry(flowNode FlowNode, t Type, isIncomplete bool) *FlowNodeTypeResult {
	if !isIncomplete {
		w.evaluator.codeFlowReachability.flowIncompleteGeneration++
	} else if t != nil {
		// A previously-recorded incomplete type that now differs means something
		// downstream may need recomputing, so the generation moves.
		if prev, ok := w.cache.Cache.Get(flowNode.FlowBase().ID); ok {
			if prevIncomplete, ok := prev.(*IncompleteType); ok {
				if prevIncomplete.Type != nil &&
					!IsTypeSame(prevIncomplete.Type, t, TypeSameOptions{}, 0) {
					w.evaluator.codeFlowReachability.flowIncompleteGeneration++
				}
			}
		}
	}

	// The original's comment: for speculative or incomplete types, we'll create a
	// separate object. For non-speculative and complete types, we'll store the
	// type directly.
	var entry CachedType
	var incompleteSubtypes []*IncompleteSubtypeInfo
	if isIncomplete {
		entry = &IncompleteType{
			Type:               t,
			IncompleteSubtypes: []*IncompleteSubtypeInfo{},
			GenerationCount:    w.evaluator.codeFlowReachability.flowIncompleteGeneration,
		}
		incompleteSubtypes = []*IncompleteSubtypeInfo{}
	} else {
		entry = t
	}

	w.cache.Cache.Set(flowNode.FlowBase().ID, entry)
	w.evaluator.speculativeTypeTracker.TrackEntry(w.cache.Cache, flowNode.FlowBase().ID)

	generation := w.evaluator.codeFlowReachability.flowIncompleteGeneration
	return NewFlowNodeTypeResult(t, isIncomplete, &generation, incompleteSubtypes)
}

// setIncompleteSubtype corresponds to the function of the same name: record one
// antecedent's contribution to a loop label and recompute the label's union.
func (w *codeFlowWalk) setIncompleteSubtype(
	flowNode FlowNode, index int, t Type, isIncomplete bool, isPending bool, evaluationCount int,
) *FlowNodeTypeResult {
	cached, ok := w.cache.Cache.Get(flowNode.FlowBase().ID)
	cachedEntry, isIncompleteEntry := cached.(*IncompleteType)
	if !ok || !isIncompleteEntry {
		// The original calls fail() here with a message naming the arguments.
		common.Fail("setIncompleteSubtype can be called only on a valid incomplete cache entry")
		return NewFlowNodeTypeResult(nil, true, nil, nil)
	}

	newInfo := &IncompleteSubtypeInfo{
		Type: t, IsIncomplete: isIncomplete, IsPending: isPending, EvaluationCount: evaluationCount,
	}

	if index < len(cachedEntry.IncompleteSubtypes) {
		oldEntry := cachedEntry.IncompleteSubtypes[index]
		if oldEntry.IsIncomplete != isIncomplete || !IsTypeSame(oldEntry.Type, t, TypeSameOptions{}, 0) {
			cachedEntry.IncompleteSubtypes[index] = newInfo
			w.evaluator.codeFlowReachability.flowIncompleteGeneration++
		} else if oldEntry.IsPending != isPending {
			// A pending-flag change alone does not move the generation: nothing
			// that depends on this entry's TYPE has changed.
			cachedEntry.IncompleteSubtypes[index] = newInfo
		}
	} else {
		common.Assert(len(cachedEntry.IncompleteSubtypes) == index, "")
		cachedEntry.IncompleteSubtypes = append(cachedEntry.IncompleteSubtypes, newInfo)
		w.evaluator.codeFlowReachability.flowIncompleteGeneration++
	}

	// The original's comment: recompute the effective type based on all of the
	// incomplete types we've accumulated so far.
	var combinedType Type
	if len(cachedEntry.IncompleteSubtypes) > 0 {
		var typesToCombine []Type
		for _, info := range cachedEntry.IncompleteSubtypes {
			if info.Type != nil {
				typesToCombine = append(typesToCombine, info.Type)
			}
		}
		if len(typesToCombine) > 0 {
			combinedType = CombineTypes(typesToCombine, nil)
		}
	}

	cachedEntry.Type = combinedType
	cachedEntry.GenerationCount = w.evaluator.codeFlowReachability.flowIncompleteGeneration

	return w.getCacheEntry(flowNode)
}

// getCacheEntry corresponds to the function of the same name.
//
// The original's comment: cache either contains a type or an object that
// represents an incomplete type. Incomplete types are types that haven't gone
// through all flow nodes yet. Incomplete only happens for branch and loop nodes.
//
// It returns nil for "no entry" and a result with a nil Type for "an entry
// holding undefined". The two are different: the second means the walk already
// established that nothing flows here.
func (w *codeFlowWalk) getCacheEntry(flowNode FlowNode) *FlowNodeTypeResult {
	cached, ok := w.cache.Cache.Get(flowNode.FlowBase().ID)
	if !ok {
		return nil
	}

	if cached == nil {
		return NewFlowNodeTypeResult(nil, false, nil, nil)
	}

	if incomplete, isIncomplete := cached.(*IncompleteType); isIncomplete {
		generation := incomplete.GenerationCount
		return NewFlowNodeTypeResult(incomplete.Type, true, &generation, incomplete.IncompleteSubtypes)
	}

	return NewFlowNodeTypeResult(cached.(Type), false, nil, nil)
}

func (w *codeFlowWalk) deleteCacheEntry(flowNode FlowNode) {
	w.cache.Cache.Delete(flowNode.FlowBase().ID)
}

// cleanIncompleteUnknownForCacheEntry corresponds to the function of the same
// name. The original's comment: cleans any "incomplete unknowns" from the
// specified set of entries to compute the final type.
func (w *codeFlowWalk) cleanIncompleteUnknownForCacheEntry(cacheEntry *FlowNodeTypeResult) Type {
	if cacheEntry.Type == nil {
		return nil
	}

	if len(cacheEntry.IncompleteSubtypes) == 0 {
		return CleanIncompleteUnknown(cacheEntry.Type, 0)
	}

	typesToCombine := []Type{}
	for _, entry := range cacheEntry.IncompleteSubtypes {
		if entry.Type != nil && !IsIncompleteUnknown(entry.Type) {
			typesToCombine = append(typesToCombine, CleanIncompleteUnknown(entry.Type, 0))
		}
	}

	return CombineTypes(typesToCombine, nil)
}

// preventRecursion corresponds to the function of the same name. The original
// uses try/catch rather than try/finally, with a comment saying the TypeScript
// debugger handles "step out" poorly with finally; defer is the Go equivalent and
// has no such caveat.
func (w *codeFlowWalk) preventRecursion(flowNode FlowNode, callback func() *FlowNodeTypeResult) *FlowNodeTypeResult {
	w.cache.PendingNodes.Add(flowNode.FlowBase().ID)
	defer w.cache.PendingNodes.Delete(flowNode.FlowBase().ID)
	return callback()
}

// evaluateAssignmentFlowNode corresponds to the function of the same name.
func (w *codeFlowWalk) evaluateAssignmentFlowNode(flowNode *FlowAssignment) *TypeResult {
	// The original's comment: for function and class nodes, the reference node is
	// the name node, but we need to use the parent node (the FunctionNode or
	// ClassNode) to access the decorated type in the type cache.
	nodeForCacheLookup := parser.ParseNode(flowNode.Node)
	if parentNode := flowNode.Node.NodeBase().Parent; parentNode != nil {
		if parentNode.GetNodeType() == parser.ParseNodeTypeFunction ||
			parentNode.GetNodeType() == parser.ParseNodeTypeClass {
			nodeForCacheLookup = parentNode
		}
	}

	return w.evaluator.EvaluateTypeForSubnode(nodeForCacheLookup, func() {
		w.evaluator.EvaluateTypesForStatement(flowNode.Node)
	})
}

/*
 * The walk.
 */

// getTypeFromFlowNode corresponds to the function of the same name.
//
// The original's comment: if this flow has no knowledge of the target
// expression, it returns undefined. If the start flow node for this scope is
// reachable, the typeAtStart value is returned.
func (w *codeFlowWalk) getTypeFromFlowNode(flowNode FlowNode) *FlowNodeTypeResult {
	curFlowNode := flowNode

	// The original's comment: this is a frequently-called routine, so it's a good
	// place to call the cancellation check. If the operation is canceled, an
	// exception will be thrown at this point.
	w.evaluator.CheckForCancellation()

	for {
		// The original's comment: have we already been here? If so, use the cached
		// value.
		cachedEntry := w.getCacheEntry(curFlowNode)
		if cachedEntry != nil {
			if !cachedEntry.IsIncomplete {
				return cachedEntry
			}

			// The original's comment: if the cached entry is incomplete, we can use
			// it only if nothing has changed that may cause the previously-reported
			// incomplete type to change.
			if cachedEntry.GenerationCount != nil &&
				*cachedEntry.GenerationCount == w.evaluator.codeFlowReachability.flowIncompleteGeneration {
				return NewFlowNodeTypeResult(w.cleanIncompleteUnknownForCacheEntry(cachedEntry), true, nil, nil)
			}
		}

		// The original's comment: check for recursion.
		if w.cache.PendingNodes.Has(curFlowNode.FlowBase().ID) {
			t := Type(UnknownTypeCreate(true))
			if cachedEntry != nil && cachedEntry.Type != nil {
				t = cachedEntry.Type
			}
			return NewFlowNodeTypeResult(t, true, nil, nil)
		}

		flags := curFlowNode.FlowBase().Flags

		if flags&(FlowFlagsUnreachableStaticCondition|FlowFlagsUnreachableStructural) != 0 {
			// The original's comment: we can get here if there are nodes in a
			// compound logical expression (e.g. "False and x") that are never
			// executed but are evaluated.
			return w.setCacheEntry(curFlowNode, NeverTypeCreateNever(), false)
		}

		if flags&FlowFlagsVariableAnnotation != 0 {
			curFlowNode = curFlowNode.(*FlowVariableAnnotation).Antecedent
			continue
		}

		if flags&FlowFlagsCall != 0 {
			callFlowNode := curFlowNode.(*FlowCall)

			// The original's comment: if this function returns a "NoReturn" type,
			// that means it always raises an exception or otherwise doesn't return,
			// so we can assume that the code before this is unreachable.
			if w.evaluator.codeFlowReachability.isCallNoReturn(w.evaluator, callFlowNode) {
				return w.setCacheEntry(curFlowNode, nil, false)
			}

			curFlowNode = callFlowNode.Antecedent
			continue
		}

		if flags&FlowFlagsAssignment != 0 {
			assignmentFlowNode := curFlowNode.(*FlowAssignment)
			if result, done := w.handleAssignment(assignmentFlowNode); done {
				return result
			}
			curFlowNode = assignmentFlowNode.Antecedent
			continue
		}

		if flags&FlowFlagsBranchLabel != 0 {
			// A FlowPostContextManagerLabel also carries the BranchLabel flag but
			// embeds FlowLabel directly rather than FlowBranchLabel. The original
			// casts to FlowBranchLabel unconditionally and reads preBranchAntecedent
			// off it as undefined; Go cannot, so the two are distinguished here.
			label, branchLabel := asFlowLabel(curFlowNode)
			if label == nil {
				common.Fail("BranchLabel flag on a node that is not a flow label")
				return NewFlowNodeTypeResult(nil, false, nil, nil)
			}

			if result, next, done := w.handleBranchLabel(curFlowNode, label, branchLabel); done {
				return result
			} else if next != nil {
				curFlowNode = next
				continue
			}
			return w.getTypeFromBranchFlowNode(label)
		}

		if flags&FlowFlagsLoopLabel != 0 {
			loopNode := curFlowNode.(*FlowLabel)

			// The original's comment: is the current symbol modified in any way
			// within the loop? If not, we can skip all processing within the loop
			// and assume that the type comes from the first antecedent, which feeds
			// the loop.
			if w.hasReference && !w.referenceAffectedBy(loopNode.AffectedExpressions) {
				curFlowNode = loopNode.Antecedents[0]
				continue
			}

			return w.getTypeFromLoopFlowNode(loopNode, cachedEntry)
		}

		if flags&(FlowFlagsTrueCondition|FlowFlagsFalseCondition) != 0 {
			conditionalFlowNode := curFlowNode.(*FlowCondition)

			if !w.skipConditionalNarrowing() && w.hasReference {
				if narrowed := w.narrowForCondition(conditionalFlowNode); narrowed != nil {
					return narrowed
				}
			}

			curFlowNode = conditionalFlowNode.Antecedent
			continue
		}

		if flags&(FlowFlagsTrueNeverCondition|FlowFlagsFalseNeverCondition) != 0 {
			conditionalFlowNode := curFlowNode.(*FlowCondition)

			if result := w.narrowForNeverCondition(conditionalFlowNode); result != nil {
				return result
			}

			curFlowNode = conditionalFlowNode.Antecedent
			continue
		}

		if flags&FlowFlagsExhaustedMatch != 0 {
			exhaustedMatchFlowNode := curFlowNode.(*FlowExhaustedMatch)
			if result := w.handleExhaustedMatch(exhaustedMatchFlowNode); result != nil {
				return result
			}
			curFlowNode = exhaustedMatchFlowNode.Antecedent
			continue
		}

		if flags&FlowFlagsNarrowForPattern != 0 {
			patternFlowNode := curFlowNode.(*FlowNarrowForPattern)
			if result := w.handleNarrowForPattern(patternFlowNode); result != nil {
				return result
			}
			curFlowNode = patternFlowNode.Antecedent
			continue
		}

		if flags&FlowFlagsPreFinallyGate != 0 {
			return w.getTypeFromPreFinallyGateFlowNode(curFlowNode.(*FlowPreFinallyGate))
		}

		if flags&FlowFlagsPostFinally != 0 {
			return w.getTypeFromPostFinallyFlowNode(curFlowNode.(*FlowPostFinally))
		}

		if flags&FlowFlagsStart != 0 {
			return w.setCacheEntry(curFlowNode, w.typeAtStart(), w.typeAtStartIsIncomplete())
		}

		if flags&FlowFlagsWildcardImport != 0 {
			wildcardImportFlowNode := curFlowNode.(*FlowWildcardImport)

			if nameNode, ok := w.reference.(*parser.NameNode); w.hasReference && ok {
				nameValue := nameNode.D.Value
				for _, name := range wildcardImportFlowNode.Names {
					if name == nameValue {
						return w.preventRecursion(curFlowNode, func() *FlowNodeTypeResult {
							t := w.getTypeFromWildcardImport(wildcardImportFlowNode, nameValue)
							return w.setCacheEntry(curFlowNode, t, false)
						})
					}
				}
			}

			curFlowNode = wildcardImportFlowNode.Antecedent
			continue
		}

		// The original's comment: we shouldn't get here.
		common.Fail("Unexpected flow node flags")
		return NewFlowNodeTypeResult(nil, false, nil, nil)
	}
}

// referenceAffectedBy answers the branch and loop skip checks: does any
// subexpression of the reference appear in the set of expressions this label
// affects. The subexpression keys are computed once and reused.
func (w *codeFlowWalk) referenceAffectedBy(affected *common.OrderedSet[string]) bool {
	if !w.haveSubexpressionKeys {
		w.subexpressionReferenceKeys = CreateKeysForReferenceSubexpressions(w.reference)
		w.haveSubexpressionKeys = true
	}

	if affected == nil {
		return false
	}

	for _, key := range w.subexpressionReferenceKeys {
		if affected.Has(key) {
			return true
		}
	}
	return false
}

// handleAssignment is the FlowFlags.Assignment arm. The bool reports whether the
// walk is finished; when false the caller steps to the antecedent.
func (w *codeFlowWalk) handleAssignment(assignmentFlowNode *FlowAssignment) (*FlowNodeTypeResult, bool) {
	if !w.hasReference {
		return nil, false
	}

	targetNode := assignmentFlowNode.Node

	// The original's comment: are we targeting the same symbol? We need to do this
	// extra check because the same symbol name might refer to different symbols in
	// different scopes (e.g. a list comprehension introduces a new scope).
	if w.options != nil && w.options.TargetSymbolID == assignmentFlowNode.TargetSymbolID &&
		IsMatchingExpression(w.reference, targetNode, nil) {
		return w.handleMatchingAssignment(assignmentFlowNode, targetNode), true
	}

	// The original's comment: is this a simple assignment to an index expression?
	// If so, it could be assigning to a TypedDict, which requires narrowing of the
	// expression's base type.
	if indexNode, ok := targetNode.(*parser.IndexNode); ok &&
		IsMatchingExpression(w.reference, indexNode.D.LeftExpr, nil) {
		if result, done := w.handleTypedDictKeyAssignment(assignmentFlowNode, indexNode); done {
			return result, true
		}
	}

	if IsPartialMatchingExpression(w.reference, targetNode) {
		// The original's comment: if the node partially matches the reference, we
		// need to "kill" any narrowed types further above this point. For example,
		// if we see the sequence
		//    a.b = 3
		//    a = Foo()
		//    x = a.b
		// The type of "a.b" can no longer be assumed to be Literal[3].
		return NewFlowNodeTypeResult(w.typeAtStart(), w.typeAtStartIsIncomplete(), nil, nil), true
	}

	return nil, false
}

// handleMatchingAssignment is the branch taken when the assignment writes to the
// very expression being narrowed.
func (w *codeFlowWalk) handleMatchingAssignment(
	assignmentFlowNode *FlowAssignment, targetNode CodeFlowReferenceExpressionNode,
) *FlowNodeTypeResult {
	// The original's comment: is this a special "unbind" assignment? If so, we can
	// handle it immediately without any further evaluation.
	if assignmentFlowNode.Flags&FlowFlagsUnbind != 0 {
		// The original's comment: don't treat unbound assignments to indexed
		// expressions (i.e. "del x[0]") as true deletions. The most common use case
		// for "del x[0]" is in a list, and the list class treats this as an element
		// deletion, not an assignment.
		//
		// ...and likewise for member access (i.e. "del a.x"), which may go through
		// a descriptor object __delete__ method or a __delattr__ method.
		switch w.reference.GetNodeType() {
		case parser.ParseNodeTypeIndex, parser.ParseNodeTypeMemberAccess:
			return w.setCacheEntry(assignmentFlowNode, nil, false)
		}

		return w.setCacheEntry(assignmentFlowNode, UnboundTypeCreate(), false)
	}

	var flowTypeResult *TypeResult
	w.preventRecursion(assignmentFlowNode, func() *FlowNodeTypeResult {
		flowTypeResult = w.evaluateAssignmentFlowNode(assignmentFlowNode)
		return nil
	})

	if flowTypeResult != nil {
		if IsTypeAliasPlaceholder(flowTypeResult.Type) {
			// The original's comment: don't cache a recursive type alias placeholder.
			return NewFlowNodeTypeResult(flowTypeResult.Type, true, nil, nil)
		}

		if w.reference.GetNodeType() == parser.ParseNodeTypeMemberAccess &&
			w.evaluator.IsAsymmetricAccessorAssignment(targetNode) {
			// An asymmetric accessor -- a property whose setter takes a different
			// type than its getter returns -- cannot be narrowed by the assignment,
			// because the value read back is not the value written.
			flowTypeResult = nil
		}
	}

	if flowTypeResult == nil {
		return w.setCacheEntry(assignmentFlowNode, nil, false)
	}
	return w.setCacheEntry(assignmentFlowNode, flowTypeResult.Type, flowTypeResult.IsIncomplete)
}

// handleTypedDictKeyAssignment is the `d["key"] = value` case: the assignment
// narrows the TypedDict itself, not the indexed element.
func (w *codeFlowWalk) handleTypedDictKeyAssignment(
	assignmentFlowNode *FlowAssignment, targetNode *parser.IndexNode,
) (*FlowNodeTypeResult, bool) {
	keyValue, ok := simpleStringSubscript(targetNode)
	if !ok {
		return nil, false
	}

	narrowedResult := w.preventRecursion(assignmentFlowNode, func() *FlowNodeTypeResult {
		flowTypeResult := w.getTypeFromFlowNode(assignmentFlowNode.Antecedent)

		if flowTypeResult.Type != nil {
			flowTypeResult.Type = MapSubtypes(flowTypeResult.Type, func(subtype Type) Type {
				if cls, ok := subtype.(*ClassType); ok && ClassTypeIsTypedDictClass(cls) {
					return w.narrowForKeyAssignment(cls, keyValue)
				}
				return subtype
			}, nil)
		}

		return flowTypeResult
	})

	if narrowedResult == nil {
		return w.setCacheEntry(assignmentFlowNode, nil, false), true
	}
	return w.setCacheEntry(assignmentFlowNode, narrowedResult.Type, narrowedResult.IsIncomplete), true
}

// simpleStringSubscript recognizes `x["literal"]` on the left of an assignment,
// which is the only index shape the TypedDict narrowing applies to. The original
// spells the nine conditions out inline.
func simpleStringSubscript(targetNode *parser.IndexNode) (string, bool) {
	parent := targetNode.NodeBase().Parent
	if parent == nil || parent.GetNodeType() != parser.ParseNodeTypeAssignment {
		return "", false
	}

	if len(targetNode.D.Items) != 1 || targetNode.D.TrailingComma {
		return "", false
	}

	item := targetNode.D.Items[0]
	if item.D.Name != nil || item.D.ArgCategory != parser.ArgCategorySimple {
		return "", false
	}

	stringList, ok := item.D.ValueExpr.(*parser.StringListNode)
	if !ok || len(stringList.D.Strings) != 1 {
		return "", false
	}

	stringNode, ok := stringList.D.Strings[0].(*parser.StringNode)
	if !ok {
		return "", false
	}

	return stringNode.D.Value.String(), true
}

// handleBranchLabel is the FlowFlags.BranchLabel arm's two early exits. It
// returns (result, nil, true) to finish, (nil, next, false) to step, or
// (nil, nil, false) to fall through to getTypeFromBranchFlowNode.
func (w *codeFlowWalk) handleBranchLabel(
	flowNode FlowNode, label *FlowLabel, branchLabel *FlowBranchLabel,
) (*FlowNodeTypeResult, FlowNode, bool) {
	if contextMgrNode, ok := flowNode.(*FlowPostContextManagerLabel); ok {
		// The original's comment: determine whether any of the context managers
		// support exception suppression. If not, none of its antecedents are
		// reachable.
		contextManagerSwallowsExceptions := false
		for _, expr := range contextMgrNode.Expressions {
			if w.evaluator.codeFlowReachability.isExceptionContextManager(
				w.evaluator, expr, contextMgrNode.IsAsync) {
				contextManagerSwallowsExceptions = true
				break
			}
		}

		if contextManagerSwallowsExceptions == contextMgrNode.BlockIfSwallowsExceptions {
			// The original's comment: do not explore any further along this code
			// flow path.
			return w.setCacheEntry(flowNode, nil, false), nil, true
		}
	}

	// The original's comment: is the current symbol modified in any way within the
	// scope of the branch? If not, we can skip all processing within the branch
	// scope.
	if branchLabel == nil {
		return nil, nil, false
	}

	if w.hasReference && branchLabel.PreBranchAntecedent != nil && label.AffectedExpressions != nil {
		if !w.referenceAffectedBy(label.AffectedExpressions) &&
			w.evaluator.codeFlowReachability.GetFlowNodeReachability(
				w.evaluator, flowNode, branchLabel.PreBranchAntecedent, false) ==
				ReachabilityReachable {
			return nil, branchLabel.PreBranchAntecedent, false
		}
	}

	return nil, nil, false
}

// asFlowLabel narrows a flow node carrying the BranchLabel or LoopLabel flag to
// its embedded FlowLabel, and to FlowBranchLabel where it has one.
func asFlowLabel(flowNode FlowNode) (*FlowLabel, *FlowBranchLabel) {
	switch typed := flowNode.(type) {
	case *FlowBranchLabel:
		return &typed.FlowLabel, typed
	case *FlowPostContextManagerLabel:
		return &typed.FlowLabel, nil
	case *FlowLabel:
		return typed, nil
	}
	return nil, nil
}

// narrowForCondition is the TrueCondition/FalseCondition arm. A nil answer means
// no narrowing applied and the walk should step to the antecedent.
func (w *codeFlowWalk) narrowForCondition(conditionalFlowNode *FlowCondition) *FlowNodeTypeResult {
	return w.preventRecursion(conditionalFlowNode, func() *FlowNodeTypeResult {
		typeNarrowingCallback := getTypeNarrowingCallback(
			w.evaluator,
			w.reference,
			conditionalFlowNode.Expression,
			conditionalFlowNode.Flags&(FlowFlagsTrueCondition|FlowFlagsTrueNeverCondition) != 0,
			0,
		)

		if typeNarrowingCallback == nil {
			return nil
		}

		flowTypeResult := w.getTypeFromFlowNode(conditionalFlowNode.Antecedent)
		flowType := flowTypeResult.Type
		isIncomplete := flowTypeResult.IsIncomplete

		if flowType != nil {
			if narrowed := typeNarrowingCallback(flowType); narrowed != nil {
				flowType = narrowed.Type
				if narrowed.IsIncomplete {
					isIncomplete = true
				}
			}
		}

		return w.setCacheEntry(conditionalFlowNode, flowType, isIncomplete)
	})
}

// narrowForNeverCondition is the TrueNeverCondition/FalseNeverCondition arm.
//
// These nodes carry a reference OTHER than the one being narrowed, and exist to
// answer a single question: does the condition make this path dead. If narrowing
// that other reference yields Never, nothing flows through, so the walk stops.
func (w *codeFlowWalk) narrowForNeverCondition(conditionalFlowNode *FlowCondition) *FlowNodeTypeResult {
	if w.skipConditionalNarrowing() || conditionalFlowNode.Reference == nil {
		return nil
	}

	// The original's comment: don't allow apply if the conditional expression
	// references the expression we're already narrowing. This case will be handled
	// by the TrueCondition or FalseCondition node.
	if CreateKeyForReference(conditionalFlowNode.Reference) == w.referenceKey {
		return nil
	}

	// The original's comment: make sure the reference type has a declared type. If
	// not, don't bother trying to infer its type because that would be too
	// expensive.
	symbolWithScope := w.evaluator.LookUpSymbolRecursive(
		conditionalFlowNode.Reference, conditionalFlowNode.Reference.D.Value, false)
	if symbolWithScope == nil || !symbolWithScope.Symbol.HasTypedDeclarations() {
		return nil
	}

	return w.preventRecursion(conditionalFlowNode, func() *FlowNodeTypeResult {
		typeNarrowingCallback := getTypeNarrowingCallback(
			w.evaluator,
			conditionalFlowNode.Reference,
			conditionalFlowNode.Expression,
			conditionalFlowNode.Flags&(FlowFlagsTrueCondition|FlowFlagsTrueNeverCondition) != 0,
			0,
		)

		if typeNarrowingCallback == nil {
			return nil
		}

		refTypeInfo := w.evaluator.GetTypeOfExpression(conditionalFlowNode.Reference, EvalFlagsNone, nil)

		narrowedType := refTypeInfo.Type
		isIncomplete := refTypeInfo.IsIncomplete

		if narrowedTypeResult := typeNarrowingCallback(refTypeInfo.Type); narrowedTypeResult != nil {
			narrowedType = narrowedTypeResult.Type
			if narrowedTypeResult.IsIncomplete {
				isIncomplete = true
			}
		}

		// The original's comment: if the narrowed type is "never", don't allow
		// further exploration.
		if IsNever(narrowedType) {
			return w.setCacheEntry(conditionalFlowNode, nil, isIncomplete)
		}

		return nil
	})
}

// handleExhaustedMatch is the FlowFlags.ExhaustedMatch arm.
func (w *codeFlowWalk) handleExhaustedMatch(node *FlowExhaustedMatch) *FlowNodeTypeResult {
	narrowedTypeResult := w.evaluator.EvaluateTypeForSubnode(node.Node, func() {
		w.evaluator.EvaluateTypesForMatchStatement(node.Node)
	})

	if narrowedTypeResult == nil {
		return nil
	}

	// The original's comment: if the narrowed type is "never", don't allow further
	// exploration.
	if IsNever(narrowedTypeResult.Type) {
		return w.setCacheEntry(node, narrowedTypeResult.Type, narrowedTypeResult.IsIncomplete)
	}

	if !w.hasReference {
		return nil
	}

	// The original's comment: see if the reference is a subexpression within the
	// subject expression.
	typeNarrowingCallback := getPatternSubtypeNarrowingCallback(
		w.evaluator, w.reference, node.SubjectExpression)
	if typeNarrowingCallback == nil {
		return nil
	}

	subexpressionTypeResult := typeNarrowingCallback(narrowedTypeResult.Type)
	if subexpressionTypeResult == nil {
		return nil
	}

	return w.setCacheEntry(node, subexpressionTypeResult.Type,
		narrowedTypeResult.IsIncomplete || subexpressionTypeResult.IsIncomplete)
}

// handleNarrowForPattern is the FlowFlags.NarrowForPattern arm.
func (w *codeFlowWalk) handleNarrowForPattern(node *FlowNarrowForPattern) *FlowNodeTypeResult {
	if !w.hasReference || IsMatchingExpression(w.reference, node.SubjectExpression, nil) {
		typeResult := w.evaluator.EvaluateTypeForSubnode(node.Statement, func() {
			if caseNode, ok := node.Statement.(*parser.CaseNode); ok {
				w.evaluator.EvaluateTypesForCaseStatement(caseNode)
			} else if matchNode, ok := node.Statement.(*parser.MatchNode); ok {
				w.evaluator.EvaluateTypesForMatchStatement(matchNode)
			}
		})

		if typeResult == nil {
			return nil
		}

		if !w.hasReference {
			if IsNever(typeResult.Type) {
				return w.setCacheEntry(node, nil, typeResult.IsIncomplete)
			}
			return nil
		}

		return w.setCacheEntry(node, typeResult.Type, typeResult.IsIncomplete)
	}

	caseStatement, ok := node.Statement.(*parser.CaseNode)
	if !ok {
		return nil
	}

	// The original's comment: see if the reference is a subexpression within the
	// subject expression.
	typeNarrowingCallback := getPatternSubtypeNarrowingCallback(
		w.evaluator, w.reference, node.SubjectExpression)
	if typeNarrowingCallback == nil {
		return nil
	}

	typeResult := w.evaluator.EvaluateTypeForSubnode(caseStatement, func() {
		w.evaluator.EvaluateTypesForCaseStatement(caseStatement)
	})
	if typeResult == nil {
		return nil
	}

	narrowedTypeResult := typeNarrowingCallback(typeResult.Type)
	if narrowedTypeResult == nil {
		return nil
	}

	return w.setCacheEntry(node, narrowedTypeResult.Type,
		typeResult.IsIncomplete || narrowedTypeResult.IsIncomplete)
}

/*
 * The three label kinds.
 */

// getTypeFromBranchFlowNode corresponds to the function of the same name: the
// union of every antecedent's contribution.
func (w *codeFlowWalk) getTypeFromBranchFlowNode(branchNode *FlowLabel) *FlowNodeTypeResult {
	var typesToCombine []Type
	sawIncomplete := false

	for _, antecedent := range branchNode.Antecedents {
		flowTypeResult := w.getTypeFromFlowNode(antecedent)

		if !w.hasReference && flowTypeResult.Type != nil && !IsNever(flowTypeResult.Type) {
			// The original's comment: if we're solving for "reachability", and we
			// have now proven reachability, there's no reason to do more work. The
			// type we return here doesn't matter as long as it's not undefined.
			return w.setCacheEntry(branchNode, UnknownTypeCreate(false), false)
		}

		if flowTypeResult.IsIncomplete {
			sawIncomplete = true
		}

		if flowTypeResult.Type != nil {
			typesToCombine = append(typesToCombine, flowTypeResult.Type)
		}
	}

	var effectiveType Type
	if len(typesToCombine) > 0 {
		effectiveType = CombineTypes(typesToCombine, nil)
	}

	return w.setCacheEntry(branchNode, effectiveType, sawIncomplete)
}

// getTypeFromLoopFlowNode corresponds to the function of the same name. See the
// file header for why it is shaped the way it is.
func (w *codeFlowWalk) getTypeFromLoopFlowNode(
	loopNode *FlowLabel, cacheEntry *FlowNodeTypeResult,
) *FlowNodeTypeResult {
	// The original's comment: the type result from one antecedent may depend on
	// the type result from another, so loop up to one time for each antecedent in
	// the loop.
	maxAttemptCount := len(loopNode.Antecedents)

	if cacheEntry == nil {
		// The original's comment: we haven't been here before, so create a new
		// incomplete cache entry.
		var initial Type
		if !w.hasReference {
			initial = UnknownTypeCreate(false)
		}
		cacheEntry = w.setCacheEntry(loopNode, initial, true)
	} else if len(cacheEntry.IncompleteSubtypes) == len(loopNode.Antecedents) &&
		anySubtypePending(cacheEntry.IncompleteSubtypes) {
		// The original's comment: if entries have been added for all antecedents
		// and there are pending entries that have not been evaluated even once,
		// treat it as incomplete. We clean any incomplete unknowns from the type
		// here to assist with type convergence.
		return NewFlowNodeTypeResult(w.cleanIncompleteUnknownForCacheEntry(cacheEntry), true, nil, nil)
	}

	attemptCount := 0

	for {
		sawIncomplete := false
		sawPending := false
		isProvenReachable := !w.hasReference && anySubtypeHasType(cacheEntry.IncompleteSubtypes)
		firstAntecedentTypeIsIncomplete := false
		firstAntecedentTypeIsPending := false

		for index, antecedent := range loopNode.Antecedents {
			// The original's comment: if we've trying to determine reachability and
			// we've already proven reachability, then we're done.
			if !w.hasReference && isProvenReachable {
				continue
			}

			if firstAntecedentTypeIsPending && index > 0 {
				continue
			}

			cacheEntry = w.getCacheEntry(loopNode)

			// The original's comment: is this entry marked "pending"? If so, we
			// have recursed and there is another call on the stack that is actively
			// evaluating this antecedent. Skip it here to avoid infinite recursion
			// but note that we skipped a "pending" antecedent.
			if index < len(cacheEntry.IncompleteSubtypes) && cacheEntry.IncompleteSubtypes[index].IsPending {
				// The original's comment: in rare circumstances, it's possible for a
				// code flow graph with nested loops to hit the case where the first
				// antecedent is marked as pending. In this case, we'll evaluate only
				// the first antecedent again even though it's pending. We're
				// guaranteed to make forward progress with the first antecedent, and
				// that will allow us to establish an initial type for this
				// expression, but we don't want to evaluate any other antecedents in
				// this case because this could result in infinite recursion.
				if index == 0 {
					firstAntecedentTypeIsPending = true
				} else {
					sawIncomplete = true
					sawPending = true
					continue
				}
			}

			// The original's comment: have we already been here (i.e. does the entry
			// exist and is not marked "pending")? If so, we can use the type that
			// was already computed if it is complete.
			var subtypeEntry *IncompleteSubtypeInfo
			if index < len(cacheEntry.IncompleteSubtypes) {
				subtypeEntry = cacheEntry.IncompleteSubtypes[index]
			}

			if subtypeEntry == nil || (!subtypeEntry.IsPending && subtypeEntry.IsIncomplete) {
				entryEvaluationCount := 0
				if subtypeEntry != nil {
					entryEvaluationCount = subtypeEntry.EvaluationCount
				}

				// The original's comment: does it look like this will never
				// converge? If so, stick with the previously-computed type for this
				// entry.
				//
				// The original also sets a maxConvergenceLimitHit flag here, read
				// only by a console.log behind a compile-time-false debug constant.
				if entryEvaluationCount >= maxConvergenceAttemptLimit {
					continue
				}

				// The original's comment: set this entry to "pending" to prevent
				// infinite recursion. We'll mark it "not pending" below.
				var pendingType Type = UnknownTypeCreate(true)
				if subtypeEntry != nil && subtypeEntry.Type != nil {
					pendingType = subtypeEntry.Type
				}
				cacheEntry = w.setIncompleteSubtype(
					loopNode, index, pendingType, true, true, entryEvaluationCount)

				// The original wraps the evaluation in try/catch purely to clear the
				// pending mark before rethrowing; defer does the same for a panic.
				func() {
					completed := false
					defer func() {
						if !completed {
							cacheEntry = w.setIncompleteSubtype(loopNode, index,
								UnknownTypeCreate(true), true, firstAntecedentTypeIsPending,
								entryEvaluationCount+1)
						}
					}()

					flowTypeResult := w.getTypeFromFlowNode(antecedent)

					if flowTypeResult.IsIncomplete {
						sawIncomplete = true
						if index == 0 {
							firstAntecedentTypeIsIncomplete = true
						}
					}

					resolvedType := flowTypeResult.Type
					if resolvedType == nil {
						if flowTypeResult.IsIncomplete {
							resolvedType = UnknownTypeCreate(true)
						} else {
							resolvedType = NeverTypeCreateNever()
						}
					}

					completed = true
					cacheEntry = w.setIncompleteSubtype(loopNode, index, resolvedType,
						flowTypeResult.IsIncomplete, firstAntecedentTypeIsPending,
						entryEvaluationCount+1)
				}()
			}

			if !w.hasReference && cacheEntry != nil && cacheEntry.Type != nil {
				isProvenReachable = true
			}
		}

		if isProvenReachable {
			// The original's comment: if we saw a pending entry, do not save over
			// the top of the cache entry because we'll overwrite a pending
			// evaluation. The type that we return here doesn't matter as long as
			// it's not undefined.
			if sawPending {
				return NewFlowNodeTypeResult(UnknownTypeCreate(false), false, nil, nil)
			}
			return w.setCacheEntry(loopNode, UnknownTypeCreate(false), false)
		}

		effectiveType := cacheEntry.Type
		if sawIncomplete && effectiveType != nil {
			// The original's comment: if there is an incomplete "Unknown" type
			// within a union type, remove it. Otherwise we might end up resolving
			// the cycle with a type that includes an undesirable unknown.
			effectiveType = CleanIncompleteUnknown(effectiveType, 0)
		}

		if !sawIncomplete || attemptCount >= maxAttemptCount {
			// The original's comment: if we were able to evaluate a type along at
			// least one antecedent path, mark it as complete. If we couldn't
			// evaluate a type along any antecedent path, assume that some recursive
			// call further up the stack will be able to produce a valid type.
			reportIncomplete := sawIncomplete
			if sawIncomplete && !sawPending &&
				!w.analyzer.isGetTypeFromCodeFlowPending(w.referenceKeyWithSymbolID) &&
				effectiveType != nil && !IsIncompleteUnknown(effectiveType) &&
				!firstAntecedentTypeIsIncomplete {
				reportIncomplete = false
			}

			// The original's comment: if we saw a pending or incomplete entry, do
			// not save over the top of the cache entry because we'll overwrite the
			// partial result.
			if sawPending || sawIncomplete {
				if !reportIncomplete {
					// The original's comment: bump the generation count because we
					// need to recalculate other incomplete types based on this
					// now-complete type.
					w.evaluator.codeFlowReachability.flowIncompleteGeneration++
				}

				return NewFlowNodeTypeResult(effectiveType, reportIncomplete, nil, nil)
			}

			// The original's comment: if the first antecedent was pending, we
			// skipped all of the other antecedents, so the type is incomplete.
			if firstAntecedentTypeIsPending {
				return NewFlowNodeTypeResult(effectiveType, true, nil, nil)
			}

			return w.setCacheEntry(loopNode, effectiveType, false)
		}

		attemptCount++
	}
}

func anySubtypePending(subtypes []*IncompleteSubtypeInfo) bool {
	for _, subtype := range subtypes {
		if subtype.IsPending {
			return true
		}
	}
	return false
}

func anySubtypeHasType(subtypes []*IncompleteSubtypeInfo) bool {
	for _, subtype := range subtypes {
		if subtype.Type != nil {
			return true
		}
	}
	return false
}

// getTypeFromPreFinallyGateFlowNode corresponds to the function of the same name.
func (w *codeFlowWalk) getTypeFromPreFinallyGateFlowNode(
	preFinallyFlowNode *FlowPreFinallyGate,
) *FlowNodeTypeResult {
	// The original's comment: is the finally gate closed?
	if w.cache.ClosedFinallyGateNodes.Has(preFinallyFlowNode.ID) {
		return NewFlowNodeTypeResult(nil, false, nil, nil)
	}

	flowTypeResult := w.getTypeFromFlowNode(preFinallyFlowNode.Antecedent)

	// The original's comment: we want to cache the type only if we're evaluating
	// the "gate closed" path.
	w.deleteCacheEntry(preFinallyFlowNode)

	return NewFlowNodeTypeResult(flowTypeResult.Type, flowTypeResult.IsIncomplete, nil, nil)
}

// getTypeFromPostFinallyFlowNode corresponds to the function of the same name.
func (w *codeFlowWalk) getTypeFromPostFinallyFlowNode(
	postFinallyFlowNode *FlowPostFinally,
) *FlowNodeTypeResult {
	gateID := postFinallyFlowNode.PreFinallyGate.ID
	wasGateClosed := w.cache.ClosedFinallyGateNodes.Has(gateID)

	defer func() {
		if !wasGateClosed {
			w.cache.ClosedFinallyGateNodes.Delete(gateID)
		}
	}()

	w.cache.ClosedFinallyGateNodes.Add(gateID)

	var flowTypeResult *FlowNodeTypeResult

	// The original's comment: use speculative mode for the remainder of the
	// finally suite because the final types within this parse node block should be
	// evaluated when the gate is open.
	w.evaluator.UseSpeculativeMode(postFinallyFlowNode.FinallyNode, func() {
		flowTypeResult = w.getTypeFromFlowNode(postFinallyFlowNode.Antecedent)
	}, nil)

	// The original's comment: if the type is incomplete, don't write back to the
	// cache.
	if flowTypeResult == nil {
		return NewFlowNodeTypeResult(nil, false, nil, nil)
	}
	if flowTypeResult.IsIncomplete {
		return flowTypeResult
	}
	return w.setCacheEntry(postFinallyFlowNode, flowTypeResult.Type, false)
}

/*
 * The three things this reaches.
 */

// getPatternSubtypeNarrowingCallback corresponds to the patternMatching.ts
// function of the same name. A nil answer means the reference is not a
// subexpression of the subject, so no narrowing applies.
func getPatternSubtypeNarrowingCallback(
	evaluator TypeEvaluator, reference CodeFlowReferenceExpressionNode, subjectExpression parser.ExpressionNode,
) func(Type) *TypeResult {
	return GetPatternSubtypeNarrowingCallback(evaluator, reference, subjectExpression)
}

// narrowForKeyAssignment corresponds to the typedDicts.ts function of the same
// name, which records that a TypedDict's key is now definitely present.
func (w *codeFlowWalk) narrowForKeyAssignment(classType *ClassType, _ string) Type {
	w.evaluator.unported("typedDicts.narrowForKeyAssignment")
	return classType
}

// getTypeFromWildcardImport corresponds to the function of the same name, which
// resolves what `from x import *` bound a given name to.
func (w *codeFlowWalk) getTypeFromWildcardImport(_ *FlowWildcardImport, _ string) Type {
	w.evaluator.unported("codeFlowEngine.getTypeFromWildcardImport")
	return UnknownTypeCreate(false)
}
