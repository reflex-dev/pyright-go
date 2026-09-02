/*
 * typeevaluator_dispatch.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfExpression and getTypeOfExpressionCore.
 *
 * getTypeOfExpression is the evaluator's front door. Its own body is cache
 * management -- decide which of the two caches applies, consult it, consult the
 * speculative cache, evaluate on a miss, then write the result back -- and the
 * evaluation is one switch on the node kind, which is getTypeOfExpressionCore.
 *
 * Porting the pair is what turns a single frontier entry into a ranked list of
 * expression kinds. Each arm of the switch hands off to a getTypeOf* that lives
 * elsewhere in the original, and each of those is a separate piece of work, so
 * each records itself separately.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// GetTypeOfExpression corresponds to getTypeOfExpression.
func (e *typeEvaluator) GetTypeOfExpression(
	node parser.ExpressionNode,
	flags EvalFlags,
	inferenceContext *InferenceContext,
) *TypeResult {
	return e.getTypeOfExpression(node, flags, inferenceContext)
}

func (e *typeEvaluator) getTypeOfExpression(
	node parser.ExpressionNode,
	flags EvalFlags,
	inferenceContext *InferenceContext,
) *TypeResult {
	useTypeFormCache := (flags & EvalFlagsTypeFormArg) != 0
	if inferenceContext != nil {
		inferenceContext.ExpectedType = TransformPossibleRecursiveTypeAlias(inferenceContext.ExpectedType, 0)
		useTypeFormCache = useTypeFormCache || expectedTypeWantsTypeForm(inferenceContext.ExpectedType)

		if expectedTypeRequiresTypeForm(inferenceContext.ExpectedType) {
			flags |= EvalFlagsTypeFormArg
		}
	}

	if (flags&EvalFlagsTypeFormArg) != 0 && (flags&EvalFlagsNoConvertSpecialForm) == 0 {
		flags |= EvalFlagsNoParamSpec | EvalFlagsNoTypeVarTuple
	}

	// Is this type already cached?
	var cachedResult *TypeResult
	var cachedGenCount int
	if useTypeFormCache {
		var expectedType Type
		if inferenceContext != nil {
			expectedType = inferenceContext.ExpectedType
		}
		if entry := e.readTypeFormTypeCacheEntry(node, expectedType); entry != nil {
			cachedResult = entry.TypeResult
			cachedGenCount = entry.IncompleteGenCount
		}
	} else if entry := e.readTypeCacheEntry(node); entry != nil {
		cachedResult = entry.TypeResult
		cachedGenCount = entry.IncompleteGenCount
	}

	if cachedResult != nil {
		if !cachedResult.IsIncomplete || cachedGenCount == e.incompleteGenCount {
			return cachedResult
		}
	}

	// Is it cached in the speculative type cache?
	if !useTypeFormCache {
		var expectedType Type
		if inferenceContext != nil {
			expectedType = inferenceContext.ExpectedType
		}
		if specCacheEntry := e.speculativeTypeTracker.GetSpeculativeType(node, expectedType); specCacheEntry != nil {
			if !specCacheEntry.TypeResult.IsIncomplete ||
				specCacheEntry.IncompleteGenerationCount == e.incompleteGenCount {
				result := specCacheEntry.TypeResult
				return &result
			}
		}
	}

	// The original's comment: this is a frequently-called routine, so it's a
	// good place to call the cancellation check. If the operation is canceled,
	// an exception will be thrown at this point.
	e.CheckForCancellation()

	// The original's comment: if we haven't already fetched some core type
	// definitions from the typeshed stubs, do so here. It would be better to
	// fetch this when it's needed in assignType, but we don't have access to
	// the parse tree at that point.
	e.initializePrefetchedTypes(node)

	typeResult := e.getTypeOfExpressionCore(node, flags, inferenceContext)

	// Should we disable type promotions for bytes?
	if IsInstantiableClass(typeResult.Type) {
		classType := typeResult.Type.(*ClassType)
		// includePromotions is `boolean | undefined` and is tested for
		// truthiness, so an absent flag is false; includeSubclasses is a plain
		// bool in this port.
		if boolValue(classType.Priv.IncludePromotions) && !classType.Priv.IncludeSubclasses &&
			ClassTypeIsBuiltInNamed(classType, "bytes") {
			if GetFileInfo(node).DiagnosticRuleSet.DisableBytesTypePromotions {
				copied := *typeResult
				copied.Type = ClassTypeCloneRemoveTypePromotions(classType)
				typeResult = &copied
			}
		}
	}

	if inferenceContext != nil {
		// Handle TypeForm assignments.
		typeResult.Type = e.convertToTypeFormType(inferenceContext.ExpectedType, typeResult.Type)
	}

	// The original's comment: don't allow speculative caching for assignment
	// expressions because the target name node won't have a corresponding type
	// cached speculatively.
	allowSpeculativeCaching := node.GetNodeType() != parser.ParseNodeTypeAssignmentExpression

	e.writeTypeCache(node, typeResult, &flags, inferenceContext, allowSpeculativeCaching)

	if node.GetNodeType() == parser.ParseNodeTypeName || node.GetNodeType() == parser.ParseNodeTypeMemberAccess {
		// The original's comment: if this is a generic function and there is a
		// signature tracker, make sure the signature is unique.
		typeResult.Type = e.ensureSignatureIsUnique(typeResult.Type, node)
	}

	// The original's comment: if there was an expected type, make sure that the
	// result type is compatible.
	if inferenceContext != nil &&
		!IsAnyOrUnknown(inferenceContext.ExpectedType) &&
		!IsNever(inferenceContext.ExpectedType) {
		e.addExpectedTypeCacheEntry(node, inferenceContext.ExpectedType)

		if !typeResult.IsIncomplete && typeResult.ExpectedTypeDiagAddendum == nil {
			diag := common.NewDiagnosticAddendum()

			// The original's comment: make sure the resulting type is assignable
			// to the expected type.
			if !e.AssignType(inferenceContext.ExpectedType, typeResult.Type, diag, nil,
				AssignTypeFlagsDefault, 0) {
				// The original's comment: set the typeErrors to true, but first
				// make a copy of the type result because the (non-error) version
				// may already be cached.
				copied := *typeResult
				copied.TypeErrors = true
				copied.ExpectedTypeDiagAddendum = diag
				typeResult = &copied

				diag.AddTextRange(node.NodeBase().TextRange)
			}
		}
	}

	return typeResult
}

// getTypeOfExpressionCore implements the core of getTypeOfExpression.
func (e *typeEvaluator) getTypeOfExpressionCore(
	node parser.ExpressionNode,
	flags EvalFlags,
	inferenceContext *InferenceContext,
) *TypeResult {
	expectingInstantiable := (flags & EvalFlagsInstantiableType) != 0

	switch n := node.(type) {
	case *parser.NameNode:
		return e.getTypeOfName(n, flags)

	case *parser.MemberAccessNode:
		return e.getTypeOfMemberAccess(n, flags)

	case *parser.IndexNode:
		return e.getTypeOfIndex(n, flags)

	case *parser.CallNode:
		return e.useSignatureTracker(n, func() *TypeResult {
			return e.getTypeOfCall(n, flags, inferenceContext)
		})

	case *parser.TupleNode:
		return e.getTypeOfTuple(n, flags, inferenceContext)

	case *parser.ConstantNode:
		return e.getTypeOfConstant(n, flags)

	case *parser.StringListNode:
		if (flags & EvalFlagsStrLiteralAsType) != 0 {
			// The original's comment: don't report expecting type errors again.
			// We will have already reported them when analyzing the contents of
			// the string.
			expectingInstantiable = false
		}
		_ = expectingInstantiable
		return e.getTypeOfStringList(n, flags, inferenceContext)

	case *parser.NumberNode:
		return e.getTypeOfNumber(n)

	case *parser.EllipsisNode:
		return e.getTypeOfEllipsis(flags, n)

	case *parser.UnaryOperationNode:
		return e.getTypeOfUnaryOperation(n, flags, inferenceContext)

	case *parser.BinaryOperationNode:
		effectiveFlags := flags

		// The original's comment: if we're expecting an instantiable type and
		// this isn't a union operator, don't require that the two operands are
		// also instantiable types.
		if expectingInstantiable && n.D.Operator != parser.OperatorTypeBitwiseOr {
			effectiveFlags &^= EvalFlagsInstantiableType
		}

		return GetTypeOfBinaryOperation(e, n, effectiveFlags, inferenceContext)

	case *parser.AugmentedAssignmentNode:
		return e.getTypeOfAugmentedAssignment(n, inferenceContext)

	case *parser.ListNode:
		return e.getTypeOfListOrSet(node, flags, inferenceContext)

	case *parser.SetNode:
		return e.getTypeOfListOrSet(node, flags, inferenceContext)

	case *parser.SliceNode:
		return e.getTypeOfSlice(n)

	case *parser.AwaitNode:
		return e.getTypeOfAwaitOperator(n, flags, inferenceContext)

	case *parser.TernaryNode:
		return e.getTypeOfTernaryOperation(n, flags, inferenceContext)

	case *parser.ComprehensionNode:
		return e.getTypeOfComprehension(n, flags, inferenceContext)

	case *parser.DictionaryNode:
		return e.getTypeOfDictionary(n, flags, inferenceContext)

	case *parser.LambdaNode:
		return e.getTypeOfLambda(n, inferenceContext)

	case *parser.AssignmentNode:
		typeResult := e.getTypeOfExpression(n.D.RightExpr, flags, inferenceContext)
		e.assignTypeToExpression(n.D.LeftExpr, typeResult, n.D.RightExpr, true, true, nil)
		return typeResult
	}

	return e.getTypeOfExpressionCoreRest(node, flags, inferenceContext)
}

// getTypeOfExpressionCoreRest holds the arms of the switch below Assignment,
// which are not ported. Splitting them out keeps the ported arms above legible
// and lets the remainder record the node kind it was asked for, so the frontier
// ranks the missing expression kinds rather than lumping them together.
func (e *typeEvaluator) getTypeOfExpressionCoreRest(
	node parser.ExpressionNode,
	_ EvalFlags,
	_ *InferenceContext,
) *TypeResult {
	e.unported("getTypeOfExpressionCore." + parseNodeTypeLabel(node.GetNodeType()))
	return &TypeResult{Type: UnknownTypeCreate(false)}
}

/*
 * The switch arms.
 *
 * Each is a distinct piece of typeEvaluator.ts -- several are whole files in the
 * original, and getTypeOfCall alone is thousands of lines -- so each records
 * itself under its own name. The point is the ranking: which expression kinds
 * the corpus actually needs, in order.
 */

// getTypeOfUnaryOperation delegates to the operations.ts function of the same
// name, which the original reaches as a module import.
func (e *typeEvaluator) getTypeOfUnaryOperation(
	node *parser.UnaryOperationNode, flags EvalFlags, inferenceContext *InferenceContext,
) *TypeResult {
	return GetTypeOfUnaryOperation(e, node, flags, inferenceContext)
}

// getTypeOfAugmentedAssignment delegates to the operations.ts function of the
// same name, which the original reaches as a module import.
func (e *typeEvaluator) getTypeOfAugmentedAssignment(
	node *parser.AugmentedAssignmentNode, inferenceContext *InferenceContext,
) *TypeResult {
	return GetTypeOfAugmentedAssignment(e, node, inferenceContext)
}

func (e *typeEvaluator) getTypeOfTernaryOperation(_ *parser.TernaryNode, _ EvalFlags, _ *InferenceContext) *TypeResult {
	e.unported("getTypeOfTernaryOperation")
	return &TypeResult{Type: UnknownTypeCreate(false)}
}

func (e *typeEvaluator) getTypeOfLambda(_ *parser.LambdaNode, _ *InferenceContext) *TypeResult {
	e.unported("getTypeOfLambda")
	return &TypeResult{Type: UnknownTypeCreate(false)}
}

// useSignatureTracker corresponds to the function of the same name. It pushes a
// signature tracker for the duration of a call evaluation; with no call
// evaluation there is nothing for it to track, so it runs the callback plainly
// and records that the tracking is missing.
func (e *typeEvaluator) useSignatureTracker(node *parser.CallNode, callback func() *TypeResult) *TypeResult {
	var result *TypeResult
	e.withSignatureTracker(node, func() { result = callback() })
	return result
}

func (e *typeEvaluator) addExpectedTypeCacheEntry(node parser.ParseNode, expectedType Type) {
	cached, ok := e.expectedTypeCache.Get(node.NodeBase().ID)
	if !ok {
		e.expectedTypeCache.Set(node.NodeBase().ID, &ExpectedTypeCacheEntry{
			Type:       expectedType,
			Candidates: []Type{expectedType},
		})
		return
	}

	// The entry is mutated in place, as the original's is. The candidate list
	// accumulates every expected type this node has been evaluated against, and
	// is what getExpectedTypeForNode later reads to offer alternatives; only the
	// most recent one is the current expected type.
	cached.Type = expectedType

	for _, candidate := range cached.Candidates {
		if IsTypeSame(candidate, expectedType, TypeSameOptions{}, 0) {
			return
		}
	}
	cached.Candidates = append(cached.Candidates, expectedType)
}

// boolValue reads a `boolean | undefined` field the way JavaScript truthiness
// does: absent is false.
func boolValue(v *bool) bool { return v != nil && *v }

// parseNodeTypeLabel names a node kind for the frontier. The original prints
// the numeric enum value; a name is more useful in a ranking a person reads,
// and this is reporting rather than behaviour.
func parseNodeTypeLabel(nodeType parser.ParseNodeType) string {
	return "nodeType" + itoa(int(nodeType))
}
