/*
 * codeflowengine_reachability.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/codeFlowEngine.ts (pyright 1.1.412):
 * getFlowNodeReachability and the evaluator entry points that use it.
 *
 * This is the first piece of the code flow engine, and it is first because the
 * differential's work-remaining map named it: with the evaluator installed and
 * everything else stubbed, every one of the 88,487 names in the corpus reported
 * IsNodeReachable and nothing else, because it is the first thing asked and it
 * short-circuits the rest.
 *
 * Reachability is *mostly* a graph walk. Structural unreachability, static
 * conditions, assignment and annotation chains, branch and loop labels, the
 * start node and the finally gates are all answerable from the flow graph
 * alone, which the binder already built and which the binder differential says
 * is identical to pyright's over the whole corpus.
 *
 * Three cases are not, and they call back into the evaluator:
 *
 *   - NarrowForPattern, which evaluates the match or case statement;
 *   - a condition whose reference has a declared type, which narrows it and
 *     asks whether the result is Never;
 *   - Call, which asks whether the callee returns NoReturn;
 *   - PostContextManager, which asks whether any context manager suppresses
 *     exceptions.
 *
 * Those four go through the evaluator interface, so today they reach stubs and
 * count themselves. A caller that cares -- the differential does -- brackets the
 * call with the unported counter and reports the answer as unported rather than
 * as reachability. Everything else is now genuinely answered.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// maxFlowNodeReachableRecursionCount cuts off the recursion at some point to
// prevent a stack overflow. The original declares it inside the recursive
// helper; it is a constant either way.
const maxFlowNodeReachableRecursionCount = 64

// reachabilityCacheEntry corresponds to the interface the reachability cache
// stores. Reachability is nil where the original leaves it undefined, which it
// distinguishes from any of the enum's values.
type reachabilityCacheEntry struct {
	Reachability     *Reachability
	ReachabilityFrom map[int]Reachability
}

// codeFlowReachability holds getFlowNodeReachability's caches. In the original
// these are locals of createCodeFlowEngine, alongside the rest of the engine;
// they live here until the rest of the engine arrives.
type codeFlowReachability struct {
	reachabilityCache       map[int]*reachabilityCacheEntry
	isReachableRecursionSet map[int]bool

	// callIsNoReturnCache and noReturnAnalysisDepth belong to isCallNoReturn;
	// see codeflowengine_noreturn.go.
	callIsNoReturnCache   map[int]bool
	noReturnAnalysisDepth int

	// isExceptionContextManagerCache and contextManagerAnalysisDepth belong to
	// isExceptionContextManager, below.
	isExceptionContextManagerCache map[int]bool
	contextManagerAnalysisDepth    int

	// flowIncompleteGeneration is the original's counter of the same name. It is
	// shared by every CodeFlowAnalyzer, because an incomplete type recorded in
	// one analyzer can be invalidated by work done through another; see
	// codeflowengine_walk.go. The original starts it at 1.
	flowIncompleteGeneration int
}

func newCodeFlowReachability() *codeFlowReachability {
	return &codeFlowReachability{
		flowIncompleteGeneration: 1,
		reachabilityCache:        map[int]*reachabilityCacheEntry{},
		isReachableRecursionSet:  map[int]bool{},
		callIsNoReturnCache:      map[int]bool{},

		isExceptionContextManagerCache: map[int]bool{},
	}
}

// GetFlowNodeReachability corresponds to the function of the same name.
//
// sourceFlowNode is nil where the original's is undefined.
func (c *codeFlowReachability) GetFlowNodeReachability(
	evaluator TypeEvaluator,
	flowNode FlowNode,
	sourceFlowNode FlowNode,
	ignoreNoReturn bool,
) Reachability {
	visitedFlowNodeSet := map[int]bool{}
	closedFinallyGateSet := map[int]bool{}

	sourceFlowNodeID := -1
	if sourceFlowNode != nil {
		sourceFlowNodeID = sourceFlowNode.FlowBase().ID
	}

	cacheReachabilityResult := func(reachability Reachability) Reachability {
		// The original's comment: if there is a finally gate set, we will not
		// cache the results because this can affect the reachability.
		if len(closedFinallyGateSet) > 0 {
			return reachability
		}

		cacheEntry := c.reachabilityCache[flowNode.FlowBase().ID]
		if cacheEntry == nil {
			cacheEntry = &reachabilityCacheEntry{ReachabilityFrom: map[int]Reachability{}}
			c.reachabilityCache[flowNode.FlowBase().ID] = cacheEntry
		}

		if sourceFlowNode == nil {
			value := reachability
			cacheEntry.Reachability = &value
		} else {
			cacheEntry.ReachabilityFrom[sourceFlowNodeID] = reachability
		}

		return reachability
	}

	var recurse func(node FlowNode, recursionCount int) Reachability
	recurse = func(node FlowNode, recursionCount int) Reachability {
		if recursionCount > maxFlowNodeReachableRecursionCount {
			return ReachabilityReachable
		}
		recursionCount++

		curFlowNode := node

		for {
			// The original names this function's parameter `flowNode`, shadowing
			// the enclosing query's parameter of the same name, and reads the
			// cache with the *shadowed* one -- so the check is on the node this
			// recursion started from. cacheReachabilityResult, defined in the
			// outer scope, sees the outer `flowNode` instead and writes under
			// the top-level query node.
			//
			// The asymmetry is load-bearing. A branch label recurses into each
			// antecedent in turn, and reading the cache under the outer node
			// would let the first antecedent's result answer for every sibling:
			// one unreachable antecedent would make the whole label unreachable,
			// which is the opposite of what the label loop computes.
			if cacheEntry := c.reachabilityCache[node.FlowBase().ID]; cacheEntry != nil && len(closedFinallyGateSet) == 0 {
				if sourceFlowNode == nil {
					if cacheEntry.Reachability != nil {
						return *cacheEntry.Reachability
					}
				} else {
					if reachabilityFrom, ok := cacheEntry.ReachabilityFrom[sourceFlowNodeID]; ok {
						return reachabilityFrom
					}
				}
			}

			// The original's comment: if we've already visited this node, we
			// can assume it wasn't reachable.
			if visitedFlowNodeSet[curFlowNode.FlowBase().ID] {
				return cacheReachabilityResult(ReachabilityUnreachableStructural)
			}

			// Note that we've been here before.
			visitedFlowNodeSet[curFlowNode.FlowBase().ID] = true

			flags := curFlowNode.FlowBase().Flags

			if flags&FlowFlagsUnreachableStructural != 0 {
				return cacheReachabilityResult(ReachabilityUnreachableStructural)
			}

			if flags&FlowFlagsUnreachableStaticCondition != 0 {
				return cacheReachabilityResult(ReachabilityUnreachableStaticCondition)
			}

			if sourceFlowNode != nil && curFlowNode == sourceFlowNode {
				return cacheReachabilityResult(ReachabilityReachable)
			}

			if flags&(FlowFlagsVariableAnnotation|FlowFlagsAssignment|FlowFlagsWildcardImport|FlowFlagsExhaustedMatch) != 0 {
				curFlowNode = antecedentOf(curFlowNode)
				continue
			}

			if flags&FlowFlagsNarrowForPattern != 0 {
				patternFlowNode := curFlowNode.(*FlowNarrowForPattern)

				typeResult := evaluator.EvaluateTypeForSubnode(patternFlowNode.Statement, func() {
					if patternFlowNode.Statement.GetNodeType() == parser.ParseNodeTypeCase {
						evaluator.EvaluateTypesForCaseStatement(patternFlowNode.Statement.(*parser.CaseNode))
					} else {
						evaluator.EvaluateTypesForMatchStatement(patternFlowNode.Statement.(*parser.MatchNode))
					}
				})

				if typeResult != nil && IsNever(typeResult.Type) {
					return cacheReachabilityResult(ReachabilityUnreachableByAnalysis)
				}

				curFlowNode = patternFlowNode.Antecedent
				continue
			}

			if flags&(FlowFlagsTrueCondition|FlowFlagsFalseCondition|FlowFlagsTrueNeverCondition|FlowFlagsFalseNeverCondition) != 0 {
				conditionalFlowNode := curFlowNode.(*FlowCondition)
				if conditionalFlowNode.Reference != nil {
					if c.conditionNarrowsToNever(evaluator, conditionalFlowNode) {
						return cacheReachabilityResult(ReachabilityUnreachableByAnalysis)
					}
				}

				curFlowNode = conditionalFlowNode.Antecedent
				continue
			}

			if flags&FlowFlagsCall != 0 {
				callFlowNode := curFlowNode.(*FlowCall)

				// The original's comment: if this function returns a "NoReturn"
				// type, that means it always raises an exception or otherwise
				// doesn't return, so we can assume that the code before this is
				// unreachable.
				if !ignoreNoReturn && c.isCallNoReturn(evaluator, callFlowNode) {
					return cacheReachabilityResult(ReachabilityUnreachableByAnalysis)
				}

				curFlowNode = callFlowNode.Antecedent
				continue
			}

			if flags&(FlowFlagsBranchLabel|FlowFlagsLoopLabel) != 0 {
				if flags&FlowFlagsPostContextManager != 0 {
					// The original's comment: determine whether any of the
					// context managers support exception suppression. If not,
					// none of its antecedents are reachable.
					contextMgrNode := curFlowNode.(*FlowPostContextManagerLabel)
					suppresses := false
					for _, expr := range contextMgrNode.Expressions {
						if c.isExceptionContextManager(evaluator, expr, contextMgrNode.IsAsync) {
							suppresses = true
							break
						}
					}
					if !suppresses {
						return cacheReachabilityResult(ReachabilityUnreachableByAnalysis)
					}
				}

				labelNode := labelOf(curFlowNode)
				unreachableByType := false
				unreachableByStaticCondition := false
				for _, antecedent := range labelNode.Antecedents {
					reachability := recurse(antecedent, recursionCount)
					if reachability == ReachabilityReachable {
						return cacheReachabilityResult(reachability)
					} else if reachability == ReachabilityUnreachableByAnalysis {
						unreachableByType = true
					} else if reachability == ReachabilityUnreachableStaticCondition {
						unreachableByStaticCondition = true
					}
				}

				if unreachableByType {
					return cacheReachabilityResult(ReachabilityUnreachableByAnalysis)
				}
				if unreachableByStaticCondition {
					return cacheReachabilityResult(ReachabilityUnreachableStaticCondition)
				}
				return cacheReachabilityResult(ReachabilityUnreachableStructural)
			}

			if flags&FlowFlagsStart != 0 {
				// The original's comment: if we hit the start but were looking
				// for a particular source flow node, return false. Otherwise,
				// the start is what we're looking for.
				if sourceFlowNode != nil {
					return cacheReachabilityResult(ReachabilityUnreachableByAnalysis)
				}
				return cacheReachabilityResult(ReachabilityReachable)
			}

			if flags&FlowFlagsPreFinallyGate != 0 {
				preFinallyFlowNode := curFlowNode.(*FlowPreFinallyGate)
				if closedFinallyGateSet[preFinallyFlowNode.FlowBase().ID] {
					return cacheReachabilityResult(ReachabilityUnreachableByAnalysis)
				}

				curFlowNode = preFinallyFlowNode.Antecedent
				continue
			}

			if flags&FlowFlagsPostFinally != 0 {
				postFinallyFlowNode := curFlowNode.(*FlowPostFinally)
				gateID := postFinallyFlowNode.PreFinallyGate.FlowBase().ID
				wasGateClosed := closedFinallyGateSet[gateID]

				closedFinallyGateSet[gateID] = true
				result := cacheReachabilityResult(recurse(postFinallyFlowNode.Antecedent, recursionCount))
				if !wasGateClosed {
					delete(closedFinallyGateSet, gateID)
				}
				return result
			}

			// We shouldn't get here.
			common.Fail("Unexpected flow node flags")
			return cacheReachabilityResult(ReachabilityReachable)
		}
	}

	// Protect against infinite recursion.
	if c.isReachableRecursionSet[flowNode.FlowBase().ID] {
		return ReachabilityReachable
	}
	c.isReachableRecursionSet[flowNode.FlowBase().ID] = true
	defer delete(c.isReachableRecursionSet, flowNode.FlowBase().ID)

	return recurse(flowNode, 0)
}

// conditionNarrowsToNever is the body of the original's TrueCondition /
// FalseCondition arm, lifted out because Go has no early `continue` from inside
// the nested block the original writes it in.
func (c *codeFlowReachability) conditionNarrowsToNever(evaluator TypeEvaluator, conditionalFlowNode *FlowCondition) bool {
	// The original's comment: make sure the reference type has a declared type.
	// If not, don't bother trying to infer its type because that would be too
	// expensive.
	symbolWithScope := evaluator.LookUpSymbolRecursive(
		conditionalFlowNode.Reference,
		conditionalFlowNode.Reference.D.Value,
		false,
	)

	if symbolWithScope == nil || !symbolWithScope.Symbol.HasTypedDeclarations() {
		return false
	}

	isPositiveTest := conditionalFlowNode.FlowBase().Flags&(FlowFlagsTrueCondition|FlowFlagsTrueNeverCondition) != 0

	typeNarrowingCallback := getTypeNarrowingCallback(
		evaluator,
		conditionalFlowNode.Reference,
		conditionalFlowNode.Expression,
		isPositiveTest,
		0,
	)

	if typeNarrowingCallback == nil {
		return false
	}

	refTypeInfo := evaluator.GetTypeOfExpression(conditionalFlowNode.Reference, EvalFlagsNone, nil)

	narrowedTypeResult := typeNarrowingCallback(refTypeInfo.Type)
	narrowedType := refTypeInfo.Type
	if narrowedTypeResult != nil {
		narrowedType = narrowedTypeResult.Type
	}

	return IsNever(narrowedType) && !refTypeInfo.IsIncomplete
}

// antecedentOf reads the antecedent of the four node kinds the original handles
// with a union type and one field access.
func antecedentOf(node FlowNode) FlowNode {
	switch n := node.(type) {
	case *FlowVariableAnnotation:
		return n.Antecedent
	case *FlowAssignment:
		return n.Antecedent
	case *FlowWildcardImport:
		return n.Antecedent
	case *FlowExhaustedMatch:
		return n.Antecedent
	}
	common.Fail("Unexpected flow node kind for an antecedent read")
	return nil
}

// labelOf reads the FlowLabel a branch or loop label node embeds.
func labelOf(node FlowNode) *FlowLabel {
	switch n := node.(type) {
	case *FlowLabel:
		return n
	case *FlowBranchLabel:
		return &n.FlowLabel
	case *FlowPostContextManagerLabel:
		return &n.FlowLabel
	}
	common.Fail("Unexpected flow node kind for a label read")
	return nil
}

// isExceptionContextManager corresponds to the function of the same name in
// codeFlowEngine.ts: does this context manager's __exit__ suppress exceptions?
// Only a declared return of `bool` or `Literal[True]` counts -- `Literal[False]`
// and `None` do not -- because that is what tells the flow engine whether code
// after a `with` block is reachable when the body raises.
//
// Its cache is not just an optimization. The entry is set to false before the
// analysis runs, so a context manager whose own type mentions the `with`
// statement it appears in answers false rather than recursing forever. The depth
// counter is the second guard, for chains rather than cycles.
func (c *codeFlowReachability) isExceptionContextManager(
	evaluator TypeEvaluator, node parser.ExpressionNode, isAsync bool,
) bool {
	// The original's comment: see if this information is cached already.
	if cached, ok := c.isExceptionContextManagerCache[node.NodeBase().ID]; ok {
		return cached
	}

	// The original's comment: initially set to false to avoid infinite recursion.
	c.isExceptionContextManagerCache[node.NodeBase().ID] = false

	// The original's comment: see if we've exceeded the max recursion depth.
	if c.contextManagerAnalysisDepth > MaxTypeRecursionCount {
		return false
	}

	c.contextManagerAnalysisDepth++
	cmSwallowsExceptions := false

	func() {
		defer func() { c.contextManagerAnalysisDepth-- }()

		cmType := evaluator.GetTypeOfExpression(node, EvalFlagsNone, nil).Type
		if cmType == nil || !IsClassInstance(cmType) {
			return
		}

		cmClass := cmType.(*ClassType)
		exitMethodName := "__exit__"
		if isAsync {
			exitMethodName = "__aexit__"
		}

		exitType := evaluator.GetBoundMagicMethod(cmClass, exitMethodName, nil, nil, nil, 0)
		if exitType == nil || !IsFunction(exitType) ||
			exitType.(*FunctionType).Shared.DeclaredReturnType == nil {
			return
		}

		returnType := exitType.(*FunctionType).Shared.DeclaredReturnType

		// The original's comment: if it's an __aexit__ method, its return type
		// will typically be wrapped in a Coroutine, so we need to extract the
		// return type from the third type argument.
		if isAsync {
			if IsClassInstance(returnType) &&
				ClassTypeIsBuiltInNamed(returnType.(*ClassType), "Coroutine", "CoroutineType") &&
				len(returnType.(*ClassType).Priv.TypeArgs) >= 3 {
				returnType = returnType.(*ClassType).Priv.TypeArgs[2]
			}
		}

		// The original's comment: generic context managers can declare __exit__ as
		// returning a TypeVar that isn't necessarily the first type parameter.
		// Specialize the declared return type using the context manager instance's
		// type arguments.
		if len(cmClass.Shared.TypeParams) > 0 && cmClass.Priv.TypeArgs != nil {
			returnType = ApplySolvedTypeVars(returnType,
				BuildSolution(cmClass.Shared.TypeParams, cmClass.Priv.TypeArgs), nil)
		}

		if IsClassInstance(returnType) && ClassTypeIsBuiltInNamed(returnType.(*ClassType), "bool") {
			literal := returnType.(*ClassType).Priv.LiteralValue
			if literal == nil {
				cmSwallowsExceptions = true
			} else if boolLiteral, ok := literal.(LiteralBool); ok && bool(boolLiteral) {
				cmSwallowsExceptions = true
			}
		}
	}()

	// The original's comment: cache the value for next time.
	c.isExceptionContextManagerCache[node.NodeBase().ID] = cmSwallowsExceptions

	return cmSwallowsExceptions
}
