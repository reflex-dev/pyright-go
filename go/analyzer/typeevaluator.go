/*
 * typeevaluator.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412): the
 * evaluator's constants, its state, and the type-cache layer everything else
 * in the file is built on.
 *
 * The original is a 30,245-line closure. `createTypeEvaluator` declares two
 * dozen locals and then some seven hundred nested functions over them, and
 * returns an object literal exposing eighty-eight of those as the TypeEvaluator
 * interface. Here the locals are fields of `typeEvaluator` and the nested
 * functions are methods on it, which is the same shape with the captures made
 * explicit. Nothing else about the structure changes: the satellites already
 * take `evaluator: TypeEvaluator` as a parameter in the original, so they
 * become Go functions over the interface without rearrangement.
 *
 * This file is the state and the caches. The evaluation itself lands on top of
 * it. Until it does, no factory is installed, so `Program` still runs with a
 * nil evaluator and nothing here is reachable from the gate.
 *
 * Two things here are easy to get subtly wrong and expensive to debug later:
 *
 *  - The caches are keyed by node id and iterated in insertion order in a few
 *    places, so they are OrderedMaps rather than Go maps.
 *  - `incompleteGenCount` is a single global generation counter shared by the
 *    ordinary type cache and the TypeForm cache. The original bumps it from one
 *    helper for exactly that reason -- see updateIncompleteGenerationCount's
 *    comment, which says the two invalidation paths must not drift -- so the
 *    same single helper is kept here rather than being inlined at its two call
 *    sites.
 */

package analyzer

import (
	"fmt"

	"github.com/microsoft/pyright/go/localization"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

/*
 * The tuning constants. These are ported as constants rather than as judgement:
 * every one of them changes which type comes out, not merely how fast, so a
 * "reasonable" Go value would be a silent behaviour change.
 */

// maxReturnTypeInferenceStackSize is how many levels deep we should attempt to
// infer return types based on call-site argument types. The deeper we go, the
// more types we may be able to infer, but the worse the performance.
const maxReturnTypeInferenceStackSize = 2

// maxReturnTypeInferenceArgCount is the max number of input arguments we should
// allow for call-site return type inference. We've found that large, complex
// functions with many arguments can take too long to analyze.
const maxReturnTypeInferenceArgCount = 6

// maxReturnTypeInferenceCodeFlowComplexity is the max complexity of the code
// flow graph that we will analyze to determine the return type of a function
// when its parameters are unannotated. We want to keep this pretty low because
// this can be very costly.
const maxReturnTypeInferenceCodeFlowComplexity = 32

// maxReturnCallSiteTypeInferenceCodeFlowComplexity is the max complexity of the
// code flow graph for call-site type inference. This is very expensive, so we
// want to keep this very low.
const maxReturnCallSiteTypeInferenceCodeFlowComplexity = 8

// maxCallSiteReturnTypeCacheSize is the max number of return types cached per
// function when using call-site inference.
const maxCallSiteReturnTypeCacheSize = 8

// maxEntriesToUseForInference is how many entries in a list, set, or dict we
// should examine when inferring the type. We need to cut it off at some point
// to avoid excessive computation.
const maxEntriesToUseForInference = 64

// maxReturnTypeInferenceAttempts is how many times we should attempt to infer a
// return type of a function before giving up and assuming that it won't
// converge due to recursion.
const maxReturnTypeInferenceAttempts = 8

// maxDeclarationsToUseForInference is how many assignments to an unannotated
// variable should be used when inferring its type. We need to cut it off at
// some point to avoid excessive computation.
const maxDeclarationsToUseForInference = 64

// maxEffectiveTypeEvaluationAttempts is the maximum number of times to attempt
// effective type evaluation of a variable that has no type declaration.
const maxEffectiveTypeEvaluationAttempts = 16

// maxTotalOverloadArgTypeExpansionCount is the maximum number of combinatoric
// argument type expansions allowed when resolving an overload.
const maxTotalOverloadArgTypeExpansionCount = 256

// maxSingleOverloadArgTypeExpansionCount is the maximum size of an enum that
// will be expanded during overload argument type expansion.
const maxSingleOverloadArgTypeExpansionCount = 64

// maxInferFunctionReturnRecursionCount is the maximum number of recursive
// function return type inference attempts that can be concurrently pending
// before we give up.
const maxInferFunctionReturnRecursionCount = 12

// maxRecursiveTypeAliasRecursionCount is the maximum recursion amount when
// comparing two recursive type aliases. The original's comment: increasing this
// can greatly increase the time required to evaluate two recursive type aliases
// that have the same definition; decreasing it can increase the chance of false
// negatives for such recursive type aliases.
const maxRecursiveTypeAliasRecursionCount = 10

// maxTypedDeclsPerSymbol is the original's constant, with its comment: normally
// a symbol can have only one type declaration, but there are cases where
// multiple are possible (e.g. a property with a setter and a deleter). In
// extreme cases, we need to limit the number of type declarations we consider
// to avoid excessive computation.
const maxTypedDeclsPerSymbol = 16

// verifyTypeCacheEvaluatorFlags enables a special debug mode that attempts to
// catch bugs due to inconsistent evaluation flags used when reading types from
// the type cache. Off in the original.
const verifyTypeCacheEvaluatorFlags = false

// printExpressionTypes is a debugging option that prints each expression and
// its evaluated type. Off in the original.
const printExpressionTypes = false

// MaxCodeComplexity is the original's exported constant, with its comment: the
// following number is chosen somewhat arbitrarily. We need to cut off code flow
// analysis at some point for code flow graphs that are too complex. Otherwise
// we risk overflowing the stack or incurring extremely long analysis times.
// This number has been tuned empirically.
const MaxCodeComplexity = 768

// EvaluatorOptions corresponds to the interface of the same name.
type EvaluatorOptions struct {
	PrintTypeFlags                PrintTypeFlags
	LogCalls                      bool
	MinimumLoggingThreshold       int
	EvaluateUnknownImportsAsAny   bool
	VerifyTypeCacheEvaluatorFlags bool
}

// DeferredClassCompletion describes a "deferred class completion" that is run
// when a class type is fully created and the "PartiallyEvaluated" flag has just
// been cleared. This allows us to properly compute information like the MRO
// which depends on a full understanding of base classes.
type DeferredClassCompletion struct {
	DependsUpon       *ClassType
	ClassesToComplete []*parser.ClassNode
}

// TypeCacheEntry corresponds to the interface of the same name. Flags is a
// pointer because the original distinguishes an absent flag set from
// EvalFlags.None; readTypeCache only checks the flags when they were recorded.
type TypeCacheEntry struct {
	TypeResult         *TypeResult
	IncompleteGenCount int
	Flags              *EvalFlags
}

// TypeFormTypeCacheEntry corresponds to `interface TypeFormTypeCacheEntry
// extends TypeCacheEntry, ContextualTypeCacheEntry`.
type TypeFormTypeCacheEntry struct {
	TypeCacheEntry
	ExpectedType Type
}

// GetExpectedType satisfies ContextualTypeCacheEntry.
func (e *TypeFormTypeCacheEntry) GetExpectedType() Type { return e.ExpectedType }

// FunctionRecursionInfo corresponds to the interface of the same name.
type FunctionRecursionInfo struct {
	CallerNode parser.ExpressionNode
}

// SuppressedNodeStackEntry corresponds to the interface of the same name.
// SuppressedDiags is nil where the original leaves it undefined, which it
// distinguishes from an empty list.
type SuppressedNodeStackEntry struct {
	Node            parser.ParseNode
	SuppressedDiags []string
	HasSuppressed   bool
}

// ExpectedTypeCacheEntry corresponds to the interface of the same name.
type ExpectedTypeCacheEntry struct {
	Type       Type
	Candidates []Type
}

// SymbolResolutionStackEntry corresponds to the interface of the same name.
type SymbolResolutionStackEntry struct {
	// The symbol ID and declaration being resolved.
	SymbolID    int
	Declaration Declaration

	// IsResultValid is initially true; it's set to false if a recursion is
	// detected.
	IsResultValid bool

	// PartialType supports the limited forms of recursion that are allowed: in
	// those cases a partially-constructed type can be registered.
	PartialType Type
}

// ReturnTypeInferenceContext corresponds to the interface of the same name.
// CodeFlowAnalyzer is the codeFlowEngine's. It stays an opaque value here --
// the engine is ported, but naming its type would put an import back across a
// seam the package layout exists to keep open, and nothing on this side reads
// the field.
type ReturnTypeInferenceContext struct {
	FunctionNode     *parser.FunctionNode
	CodeFlowAnalyzer any
}

// typeCacheMap adapts a map to the SpeculativeCache interface, which is how the
// speculative tracker deletes entries when a speculative context ends.
//
// This is a plain Go map rather than the OrderedMap the port uses wherever the
// original writes a `Map`, and that is a deliberate exception. OrderedMap exists
// because JavaScript's Map iterates in insertion order and pyright's output
// depends on that -- but the type cache is never iterated. The original only
// ever calls get, set and size on it, and replaces it wholesale to empty it. So
// there is no order to preserve, and preserving one is not free: this is the
// largest map in the process, and the ordering bookkeeping costs both the keys
// slice and the index beside it.
//
// It was also the map whose deletes dominated the profile before OrderedMap's
// Delete became O(1) -- the speculative tracker undoes every write it made, on
// every overload attempt.
type typeCacheMap struct {
	m map[int]*TypeCacheEntry
}

func newTypeCacheMap() *typeCacheMap {
	return &typeCacheMap{m: map[int]*TypeCacheEntry{}}
}

func (c *typeCacheMap) Delete(id int) bool {
	if _, ok := c.m[id]; !ok {
		return false
	}
	delete(c.m, id)
	return true
}

func (c *typeCacheMap) Get(id int) *TypeCacheEntry { return c.m[id] }

func (c *typeCacheMap) Set(id int, entry *TypeCacheEntry) { c.m[id] = entry }

func (c *typeCacheMap) Size() int { return len(c.m) }

// typeEvaluator holds what createTypeEvaluator closes over. The field order is
// the original's declaration order.
type typeEvaluator struct {
	importLookup     ImportLookup
	evaluatorOptions EvaluatorOptions

	symbolResolutionStack  []*SymbolResolutionStackEntry
	speculativeTypeTracker *SpeculativeTypeTracker
	suppressedNodeStack    []*SuppressedNodeStackEntry
	assignClassToSelfStack []*AssignClassToSelfInfo

	// enumEvalStack and protocolAssignmentStack are module-level stacks in the
	// original (enums.ts, protocols.ts). They live here because --threads runs
	// one evaluator per worker goroutine, and a recursion stack shared between
	// workers is corrupted by interleaved push/pop; upstream's worker processes
	// each had their own module instances. Every use flows through an
	// `evaluator TypeEvaluator` parameter, so the stacks follow the evaluator.
	enumEvalStack           []enumEvalStackEntry
	protocolAssignmentStack []protocolAssignmentStackEntry

	functionRecursionMap              *common.OrderedMap[int, []*FunctionRecursionInfo]
	codeFlowAnalyzerCache             *common.OrderedMap[int, []*CodeFlowAnalyzerCacheEntry]
	typeCache                         *typeCacheMap
	typeFormTypeCache                 *common.OrderedMap[int, []*TypeFormTypeCacheEntry]
	effectiveTypeCache                *common.OrderedMap[int, *common.OrderedMap[string, *EffectiveTypeResult]]
	expectedTypeCache                 *common.OrderedMap[int, *ExpectedTypeCacheEntry]
	asymmetricAccessorAssignmentCache *common.OrderedSet[int]
	deferredClassCompletions          []*DeferredClassCompletion

	printExpressionSpaceCount int
	incompleteGenCount        int

	returnTypeInferenceContextStack      []*ReturnTypeInferenceContext
	returnTypeInferenceTypeCache         *typeCacheMap
	returnTypeInferenceTypeFormTypeCache *common.OrderedMap[int, []*TypeFormTypeCacheEntry]

	signatureTrackerStack []*SignatureTrackerStackEntry

	// prefetched is nil until initializePrefetchedTypes runs; the original's is
	// a Partial<PrefetchedTypes>, so individual fields may still be unset.
	prefetched *PrefetchedTypes

	// codeFlowReachability holds getFlowNodeReachability's caches, which in the
	// original are locals of createCodeFlowEngine.
	codeFlowReachability *codeFlowReachability

	// unportedCounts has no counterpart in the original; see unported below.
	unportedCounts *common.OrderedMap[string, int]
	unportedTotal  int
}

// AssignClassToSelfInfo corresponds to the interface of the same name. The
// original's field is named `class`, which is a reserved word in neither
// language but reads poorly as a Go field name.
type AssignClassToSelfInfo struct {
	ClassType       *ClassType
	AssumedVariance Variance
}

// CodeFlowAnalyzerCacheEntry corresponds to the interface of the same name.
// CodeFlowAnalyzer is the codeFlowEngine's; see ReturnTypeInferenceContext.
type CodeFlowAnalyzerCacheEntry struct {
	TypeAtStart      *TypeResult
	CodeFlowAnalyzer any
}

// SignatureTrackerStackEntry corresponds to the interface of the same name in
// typeEvaluator.ts.
type SignatureTrackerStackEntry struct {
	Tracker  *UniqueSignatureTracker
	RootNode parser.ParseNode
}

// NewTypeEvaluator corresponds to createTypeEvaluator. The original's third
// parameter, wrapWithLogger, wraps each returned method in the log tracker; it
// is a reporting facility rather than an evaluation one and is not carried.
func NewTypeEvaluator(importLookup ImportLookup, evaluatorOptions EvaluatorOptions) TypeEvaluator {
	e := &typeEvaluator{
		importLookup:           importLookup,
		evaluatorOptions:       evaluatorOptions,
		speculativeTypeTracker: NewSpeculativeTypeTracker(),
		codeFlowReachability:   newCodeFlowReachability(),
	}
	e.resetCaches()
	return e
}

// resetCaches is the body of disposeEvaluator, which the constructor also uses
// so the two cannot drift. The original's comment on disposeEvaluator: this
// function should be called immediately prior to discarding the type evaluator.
// It forcibly replaces existing cache maps with empty equivalents. This
// shouldn't be necessary, but there is apparently a bug in the v8 GC where it
// is unable to detect circular references in complex data structures, so it
// fails to clean up the objects if we don't help it out.
//
// None of that reasoning applies to Go's collector. It is kept because the
// evaluator is reused after disposal in some paths and the emptying is
// therefore observable, not because the GC needs the help.
func (e *typeEvaluator) resetCaches() {
	e.functionRecursionMap = common.NewOrderedMap[int, []*FunctionRecursionInfo]()
	e.codeFlowAnalyzerCache = common.NewOrderedMap[int, []*CodeFlowAnalyzerCacheEntry]()
	e.typeCache = newTypeCacheMap()
	e.typeFormTypeCache = common.NewOrderedMap[int, []*TypeFormTypeCacheEntry]()
	e.effectiveTypeCache = common.NewOrderedMap[int, *common.OrderedMap[string, *EffectiveTypeResult]]()
	e.expectedTypeCache = common.NewOrderedMap[int, *ExpectedTypeCacheEntry]()
	e.asymmetricAccessorAssignmentCache = common.NewOrderedSet[int]()
}

func (e *typeEvaluator) DisposeEvaluator() { e.resetCaches() }

func (e *typeEvaluator) GetTypeCacheEntryCount() int { return e.typeCache.Size() }

// CheckForCancellation does nothing. The original consults a CancellationToken;
// program.go does not thread one, so there is never a token to check. See
// typeevaluatortypes.go.
func (e *typeEvaluator) CheckForCancellation() {}

/*
 * The type cache.
 */

// readTypeCacheEntry returns nil where the original returns undefined.
func (e *typeEvaluator) readTypeCacheEntry(node parser.ParseNode) *TypeCacheEntry {
	// The original's comment: should we use a temporary cache associated with a
	// contextual analysis of a function, contextualized based on call-site
	// argument types?
	if e.returnTypeInferenceTypeCache != nil && e.isNodeInReturnTypeInferenceContext(node) {
		return e.returnTypeInferenceTypeCache.Get(node.NodeBase().ID)
	}
	return e.typeCache.Get(node.NodeBase().ID)
}

func (e *typeEvaluator) getTypeFormTypeCache(node parser.ParseNode) *common.OrderedMap[int, []*TypeFormTypeCacheEntry] {
	if e.returnTypeInferenceTypeFormTypeCache != nil && e.isNodeInReturnTypeInferenceContext(node) {
		return e.returnTypeInferenceTypeFormTypeCache
	}

	return e.typeFormTypeCache
}

func (e *typeEvaluator) readTypeFormTypeCacheEntry(node parser.ParseNode, expectedType Type) *TypeFormTypeCacheEntry {
	entries, ok := e.getTypeFormTypeCache(node).Get(node.NodeBase().ID)
	if !ok {
		return nil
	}
	for _, entry := range entries {
		if ContextualTypeCacheEntryMatches(entry, expectedType) {
			return entry
		}
	}
	return nil
}

// updateIncompleteGenerationCount bumps the incomplete generation count using
// the same rules for both the regular type cache and the TypeForm type cache so
// the two cache-invalidation paths cannot drift. A complete result always bumps
// the count (invalidating dependent incomplete entries); an incomplete result
// bumps only when its type differs from the previously-cached value.
func (e *typeEvaluator) updateIncompleteGenerationCount(typeResult *TypeResult, oldTypeResult *TypeResult) {
	if !typeResult.IsIncomplete {
		e.incompleteGenCount++
	} else if oldTypeResult != nil && !IsTypeSame(typeResult.Type, oldTypeResult.Type, TypeSameOptions{}, 0) {
		e.incompleteGenCount++
	}
}

// isTypeCached is used by runtime-evaluation guards. The original's comment: a
// contextual TypeForm entry does not prove that the ordinary runtime type was
// evaluated.
func (e *typeEvaluator) isTypeCached(node parser.ParseNode) bool {
	cacheEntry := e.readTypeCacheEntry(node)
	if cacheEntry == nil {
		return false
	}

	return !cacheEntry.TypeResult.IsIncomplete || cacheEntry.IncompleteGenCount == e.incompleteGenCount
}

// readTypeCache returns nil where the original returns undefined. flags is a
// pointer because the original's `flags: EvalFlags | undefined` distinguishes
// "no flags recorded" from EvalFlags.None.
func (e *typeEvaluator) readTypeCache(node parser.ParseNode, flags *EvalFlags) Type {
	cacheEntry := e.readTypeCacheEntry(node)
	if cacheEntry == nil || cacheEntry.TypeResult.IsIncomplete {
		return nil
	}

	// The flag-consistency debug mode. Both switches are off in the original,
	// so this never runs; it is carried because turning it on is how the
	// original's author finds flag mismatches, and the port should be
	// debuggable the same way.
	if e.evaluatorOptions.VerifyTypeCacheEvaluatorFlags || verifyTypeCacheEvaluatorFlags {
		if flags != nil {
			expectedFlags := cacheEntry.Flags
			if expectedFlags != nil && *flags != *expectedFlags {
				e.reportTypeCacheFlagMismatch(node, *expectedFlags, *flags)
			}
		}
	}

	return cacheEntry.TypeResult.Type
}

func (e *typeEvaluator) reportTypeCacheFlagMismatch(node parser.ParseNode, expectedFlags EvalFlags, flags EvalFlags) {
	fileInfo := GetFileInfo(node)
	position := common.ConvertOffsetToPosition(node.NodeBase().Start, fileInfo.Lines)

	// The original interpolates `node.nodeType` directly, which for a numeric
	// enum prints the number; the same number is printed here.
	parentType := "none"
	if parent := node.NodeBase().Parent; parent != nil {
		parentType = itoa(int(parent.GetNodeType()))
	}

	message := "Type cache flag mismatch for node type " + itoa(int(node.GetNodeType())) +
		" (parent " + parentType + "): " +
		"cached flags = " + itoa(int(expectedFlags)) + ", access flags = " + itoa(int(flags)) +
		", file = {" + fileInfo.FileUri.String() + " [" + itoa(position.Line+1) + ":" + itoa(position.Character+1) + "]}"

	if e.evaluatorOptions.VerifyTypeCacheEvaluatorFlags {
		common.Fail(message)
	} else {
		fmt.Println(message)
	}
}

// writeTypeCache corresponds to the function of the same name.
func (e *typeEvaluator) writeTypeCache(
	node parser.ParseNode,
	typeResult *TypeResult,
	flags *EvalFlags,
	inferenceContext *InferenceContext,
	allowSpeculativeCaching bool,
) {
	useTypeFormCache := (flags != nil && (*flags&EvalFlagsTypeFormArg) != 0) ||
		(inferenceContext != nil && expectedTypeWantsTypeForm(inferenceContext.ExpectedType))

	if useTypeFormCache {
		var expectedType Type
		if inferenceContext != nil {
			expectedType = inferenceContext.ExpectedType
		}

		// The original's comment: speculative TypeForm results are not
		// retained, so they must not invalidate persistent incomplete entries
		// through the global generation counter.
		if e.IsSpeculativeModeInUse(node) {
			return
		}

		typeFormCache := e.getTypeFormTypeCache(node)
		cacheEntries, _ := typeFormCache.Get(node.NodeBase().ID)

		var oldEntry *TypeFormTypeCacheEntry
		for _, entry := range cacheEntries {
			if ContextualTypeCacheEntryMatches(entry, expectedType) {
				oldEntry = entry
				break
			}
		}

		var oldTypeResult *TypeResult
		if oldEntry != nil {
			oldTypeResult = oldEntry.TypeResult
		}
		e.updateIncompleteGenerationCount(typeResult, oldTypeResult)

		typeFormCache.Set(node.NodeBase().ID, AddContextualTypeCacheEntry(cacheEntries, &TypeFormTypeCacheEntry{
			TypeCacheEntry: TypeCacheEntry{
				TypeResult:         typeResult,
				Flags:              flags,
				IncompleteGenCount: e.incompleteGenCount,
			},
			ExpectedType: expectedType,
		}, nil))
		return
	}

	// The original's comment: should we use a temporary cache associated with a
	// contextual analysis of a function, contextualized based on call-site
	// argument types?
	typeCacheToUse := e.typeCache
	if e.returnTypeInferenceTypeCache != nil && e.isNodeInReturnTypeInferenceContext(node) {
		typeCacheToUse = e.returnTypeInferenceTypeCache
	}

	oldValue := typeCacheToUse.Get(node.NodeBase().ID)
	var oldTypeResult *TypeResult
	if oldValue != nil {
		oldTypeResult = oldValue.TypeResult
	}
	e.updateIncompleteGenerationCount(typeResult, oldTypeResult)

	typeCacheToUse.Set(node.NodeBase().ID, &TypeCacheEntry{
		TypeResult:         typeResult,
		Flags:              flags,
		IncompleteGenCount: e.incompleteGenCount,
	})

	// The original's comment: if the entry is located within a part of the
	// parse tree that is currently being "speculatively" evaluated, track it so
	// we delete the cached entry when we leave this speculative context.
	if e.IsSpeculativeModeInUse(node) {
		e.speculativeTypeTracker.TrackEntry(typeCacheToUse, node.NodeBase().ID)
		if allowSpeculativeCaching {
			var expectedType Type
			if inferenceContext != nil {
				expectedType = inferenceContext.ExpectedType
			}
			e.speculativeTypeTracker.AddSpeculativeType(node, typeResult, e.incompleteGenCount, expectedType)
		}
	}
}

// SetTypeResultForNode corresponds to setTypeResultForNode.
func (e *typeEvaluator) SetTypeResultForNode(node parser.ParseNode, typeResult *TypeResult, flags EvalFlags) {
	e.writeTypeCache(node, typeResult, &flags, nil, false)
}

func (e *typeEvaluator) setAsymmetricDescriptorAssignment(node parser.ParseNode) {
	if e.IsSpeculativeModeInUse(nil) {
		return
	}

	e.asymmetricAccessorAssignmentCache.Add(node.NodeBase().ID)
}

func (e *typeEvaluator) IsAsymmetricAccessorAssignment(node parser.ParseNode) bool {
	return e.asymmetricAccessorAssignmentCache.Has(node.NodeBase().ID)
}

// isNodeInReturnTypeInferenceContext determines whether the specified node is
// contained within the function node corresponding to the function that we are
// currently analyzing in the context of parameter types defined by a call site.
func (e *typeEvaluator) isNodeInReturnTypeInferenceContext(node parser.ParseNode) bool {
	stackSize := len(e.returnTypeInferenceContextStack)
	if stackSize == 0 {
		return false
	}

	contextNode := e.returnTypeInferenceContextStack[stackSize-1]

	var curNode parser.ParseNode = node
	for curNode != nil {
		// Reference equality on the parse node, as in the original.
		if curNode == parser.ParseNode(contextNode.FunctionNode) {
			return true
		}
		curNode = curNode.NodeBase().Parent
	}

	return false
}

func (e *typeEvaluator) getCodeFlowAnalyzerForReturnTypeInferenceContext() any {
	stackSize := len(e.returnTypeInferenceContextStack)
	common.Assert(stackSize > 0, "")
	contextNode := e.returnTypeInferenceContextStack[stackSize-1]
	return contextNode.CodeFlowAnalyzer
}

/*
 * The symbol resolution stack.
 */

// getIndexOfSymbolResolution returns -1 where the original's findIndex does.
// The declaration comparison is reference equality in the original.
func (e *typeEvaluator) getIndexOfSymbolResolution(symbol *Symbol, declaration Declaration) int {
	for i, entry := range e.symbolResolutionStack {
		if entry.SymbolID == symbol.ID && entry.Declaration == declaration {
			return i
		}
	}
	return -1
}

func (e *typeEvaluator) pushSymbolResolution(symbol *Symbol, declaration Declaration) bool {
	index := e.getIndexOfSymbolResolution(symbol, declaration)
	if index >= 0 {
		// Mark all of the entries between these two as invalid.
		for i := index + 1; i < len(e.symbolResolutionStack); i++ {
			e.symbolResolutionStack[i].IsResultValid = false
		}
		return false
	}

	e.symbolResolutionStack = append(e.symbolResolutionStack, &SymbolResolutionStackEntry{
		SymbolID:      symbol.ID,
		Declaration:   declaration,
		IsResultValid: true,
	})
	return true
}

func (e *typeEvaluator) popSymbolResolution(symbol *Symbol) bool {
	// `symbolResolutionStack.pop()` on an empty array yields undefined, which
	// the assertion then reports; an empty slice here has to be checked before
	// indexing rather than after.
	if len(e.symbolResolutionStack) == 0 {
		common.Fail("Symbol resolution stack mismatch: expected symbol " + itoa(symbol.ID) + ", got empty stack")
		return false
	}

	poppedEntry := e.symbolResolutionStack[len(e.symbolResolutionStack)-1]
	e.symbolResolutionStack = e.symbolResolutionStack[:len(e.symbolResolutionStack)-1]

	common.Assert(
		poppedEntry.SymbolID == symbol.ID,
		"Symbol resolution stack mismatch: expected symbol "+itoa(symbol.ID)+", got "+itoa(poppedEntry.SymbolID),
	)

	return poppedEntry.IsResultValid
}

func (e *typeEvaluator) setSymbolResolutionPartialType(symbol *Symbol, declaration Declaration, t Type) {
	index := e.getIndexOfSymbolResolution(symbol, declaration)
	if index >= 0 {
		e.symbolResolutionStack[index].PartialType = t
	}
}

// getSymbolResolutionPartialType returns nil where the original returns
// undefined.
func (e *typeEvaluator) getSymbolResolutionPartialType(symbol *Symbol, declaration Declaration) Type {
	index := e.getIndexOfSymbolResolution(symbol, declaration)
	if index >= 0 {
		return e.symbolResolutionStack[index].PartialType
	}

	return nil
}

/*
 * Speculative mode.
 */

// IsSpeculativeModeInUse corresponds to isSpeculativeModeInUse.
func (e *typeEvaluator) IsSpeculativeModeInUse(node parser.ParseNode) bool {
	return e.speculativeTypeTracker.IsSpeculative(node, false)
}

/*
 * TypeForm predicates.
 *
 * These sit here rather than with the rest of the TypeForm code because the
 * cache layer above needs them: whether a write goes to the ordinary type cache
 * or the TypeForm one is decided by expectedTypeWantsTypeForm.
 */

// isTypeFormClass corresponds to the function of the same name.
func isTypeFormClass(t *ClassType) bool {
	return ClassTypeIsBuiltInNamed(t, "TypeForm") ||
		(ClassTypeIsSpecialBuiltIn(t) &&
			(t.Shared.Name == "TypeForm" || (t.Priv.AliasName != nil && *t.Priv.AliasName == "TypeForm")))
}

// isTypeFormType corresponds to the function of the same name.
func isTypeFormType(t Type) bool {
	if !IsClassInstance(t) {
		return false
	}
	return isTypeFormClass(t.(*ClassType))
}

// expectedTypeRequiresTypeForm corresponds to the function of the same name.
func expectedTypeRequiresTypeForm(expectedType Type) bool {
	return SomeSubtypes(expectedType, isTypeFormType) &&
		!SomeSubtypes(expectedType, func(subtype Type) bool { return !isTypeFormType(subtype) })
}

// expectedTypeWantsTypeForm corresponds to the function of the same name.
func expectedTypeWantsTypeForm(expectedType Type) bool {
	return SomeSubtypes(expectedType, isTypeFormType)
}

/*
 * Accounting for what is not ported yet.
 *
 * The evaluator is installed and reachable, so every gate exercises it. That is
 * only defensible if the parts that do not exist say so. Each unported path
 * records itself here, and the counts come back through the bridge, so "what is
 * left" is a measurement over the corpus rather than an impression from reading
 * the source. See typeevaluator_unported.go.
 */

// noteUnported is unported under the name other files in the package use to
// reach it through an interface assertion, so that code which does not hold a
// *typeEvaluator can still record what it could not do.
func (e *typeEvaluator) noteUnported(what string) { e.unported(what) }

// unported records that evaluation reached a path typeEvaluator.ts takes and
// this port does not implement yet.
func (e *typeEvaluator) unported(what string) {
	if e.unportedCounts == nil {
		e.unportedCounts = common.NewOrderedMap[string, int]()
	}
	count, _ := e.unportedCounts.Get(what)
	e.unportedCounts.Set(what, count+1)
	e.unportedTotal++
}

// UnportedReporter is how a caller asks an evaluator what it could not do. It
// has no counterpart in the original; it exists so the incompleteness of this
// port is measurable from outside the package while it is being built, and it
// goes away with the last stub.
type UnportedReporter interface {
	UnportedCounts() *common.OrderedMap[string, int]
	UnportedTotal() int
}

// UnportedCounts reports how many times each unported path was reached, in
// first-hit order.
func (e *typeEvaluator) UnportedCounts() *common.OrderedMap[string, int] {
	if e.unportedCounts == nil {
		return common.NewOrderedMap[string, int]()
	}
	return e.unportedCounts
}

// UnportedTotal is the sum of UnportedCounts' values.
func (e *typeEvaluator) UnportedTotal() int { return e.unportedTotal }

// Compile-time check that the evaluator satisfies the interface it is handed
// through. Nothing constructs this value.
var _ TypeEvaluator = (*typeEvaluator)(nil)

/*
 * Reachability.
 *
 * The first evaluator members answered for real rather than stubbed. See
 * codeflowengine_reachability.go.
 */

// checkCodeFlowTooComplex corresponds to the function of the same name. It
// reports a diagnostic as a side effect, which is why it is not a predicate on
// the node alone.
func (e *typeEvaluator) checkCodeFlowTooComplex(node parser.ParseNode) bool {
	var scopeNode parser.ParseNode
	if node.GetNodeType() == parser.ParseNodeTypeFunction {
		scopeNode = node
	} else {
		scopeNode = GetExecutionScopeNode(node)
	}

	codeComplexity := GetCodeFlowComplexity(scopeNode.(ScopedNode))

	if codeComplexity > MaxCodeComplexity {
		var errorRange common.TextRange = scopeNode.NodeBase().TextRange
		if fn, ok := scopeNode.(*parser.FunctionNode); ok {
			errorRange = fn.D.Name.NodeBase().TextRange
		} else if scopeNode.GetNodeType() == parser.ParseNodeTypeModule {
			errorRange = common.TextRange{Start: 0, Length: 0}
		}

		fileInfo := GetFileInfo(node)
		e.AddDiagnosticForTextRange(
			fileInfo,
			DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.CodeTooComplexToAnalyze(),
			errorRange,
		)

		return true
	}

	return false
}

// IsNodeReachable corresponds to isNodeReachable.
func (e *typeEvaluator) IsNodeReachable(node parser.ParseNode, sourceNode parser.ParseNode) bool {
	return e.GetNodeReachability(node, sourceNode) == ReachabilityReachable
}

// GetNodeReachability corresponds to getNodeReachability.
func (e *typeEvaluator) GetNodeReachability(node parser.ParseNode, sourceNode parser.ParseNode) Reachability {
	if e.checkCodeFlowTooComplex(node) {
		return ReachabilityReachable
	}

	flowNode := GetFlowNode(node)
	if flowNode == nil {
		if node.NodeBase().Parent != nil {
			return e.GetNodeReachability(node.NodeBase().Parent, sourceNode)
		}
		return ReachabilityUnreachableStructural
	}

	var sourceFlowNode FlowNode
	if sourceNode != nil {
		sourceFlowNode = GetFlowNode(sourceNode)
	}

	return e.codeFlowReachability.GetFlowNodeReachability(e, flowNode, sourceFlowNode, false)
}
