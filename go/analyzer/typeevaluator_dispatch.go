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
	"github.com/microsoft/pyright/go/localization"
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

	var typeResult *TypeResult

	switch n := node.(type) {
	case *parser.NameNode:
		typeResult = e.getTypeOfName(n, flags)

	case *parser.MemberAccessNode:
		typeResult = e.getTypeOfMemberAccess(n, flags)

	case *parser.IndexNode:
		typeResult = e.getTypeOfIndex(n, flags)

	case *parser.CallNode:
		typeResult = e.useSignatureTracker(n, func() *TypeResult {
			return e.getTypeOfCall(n, flags, inferenceContext)
		})

	case *parser.TupleNode:
		typeResult = e.getTypeOfTuple(n, flags, inferenceContext)

	case *parser.ConstantNode:
		typeResult = e.getTypeOfConstant(n, flags)

	case *parser.StringListNode:
		if (flags & EvalFlagsStrLiteralAsType) != 0 {
			// The original's comment: don't report expecting type errors again.
			// We will have already reported them when analyzing the contents of
			// the string.
			expectingInstantiable = false
		}
		typeResult = e.getTypeOfStringList(n, flags, inferenceContext)

	case *parser.NumberNode:
		typeResult = e.getTypeOfNumber(n)

	case *parser.EllipsisNode:
		typeResult = e.getTypeOfEllipsis(flags, n)

	case *parser.UnaryOperationNode:
		typeResult = e.getTypeOfUnaryOperation(n, flags, inferenceContext)

	case *parser.BinaryOperationNode:
		effectiveFlags := flags

		// The original's comment: if we're expecting an instantiable type and
		// this isn't a union operator, don't require that the two operands are
		// also instantiable types.
		if expectingInstantiable && n.D.Operator != parser.OperatorTypeBitwiseOr {
			effectiveFlags &^= EvalFlagsInstantiableType
		}

		typeResult = GetTypeOfBinaryOperation(e, n, effectiveFlags, inferenceContext)

	case *parser.AugmentedAssignmentNode:
		typeResult = e.getTypeOfAugmentedAssignment(n, inferenceContext)

	case *parser.ListNode:
		typeResult = e.getTypeOfListOrSet(node, flags, inferenceContext)

	case *parser.SetNode:
		typeResult = e.getTypeOfListOrSet(node, flags, inferenceContext)

	case *parser.SliceNode:
		typeResult = e.getTypeOfSlice(n)

	case *parser.AwaitNode:
		typeResult = e.getTypeOfAwaitOperator(n, flags, inferenceContext)

	case *parser.TernaryNode:
		typeResult = e.getTypeOfTernaryOperation(n, flags, inferenceContext)

	case *parser.ComprehensionNode:
		typeResult = e.getTypeOfComprehension(n, flags, inferenceContext)

	case *parser.DictionaryNode:
		typeResult = e.getTypeOfDictionary(n, flags, inferenceContext)

	case *parser.LambdaNode:
		typeResult = e.getTypeOfLambda(n, inferenceContext)

	case *parser.AssignmentNode:
		typeResult = e.getTypeOfExpression(n.D.RightExpr, flags, inferenceContext)
		e.assignTypeToExpression(n.D.LeftExpr, typeResult, n.D.RightExpr, true, true, nil)

	case *parser.AssignmentExpressionNode:
		if (flags & EvalFlagsTypeExpression) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.WalrusNotAllowed(), node, nil)
		}

		typeResult = e.getTypeOfExpression(n.D.RightExpr, flags, inferenceContext)
		e.assignTypeToExpression(n.D.Name, typeResult, n.D.RightExpr, true, false, nil)

	case *parser.YieldNode:
		typeResult = e.getTypeOfYield(n)

	case *parser.YieldFromNode:
		typeResult = e.getTypeOfYieldFrom(n)

	case *parser.UnpackNode:
		typeResult = e.getTypeOfUnpackOperator(n, flags, inferenceContext)

	case *parser.TypeAnnotationNode:
		typeResult = e.getTypeOfExpression(
			n.D.Annotation,
			EvalFlagsInstantiableType|
				EvalFlagsTypeExpression|
				EvalFlagsStrLiteralAsType|
				EvalFlagsNoParamSpec|
				EvalFlagsNoTypeVarTuple|
				EvalFlagsVarTypeAnnotation,
			nil)

	case *parser.StringNode:
		typeResult = e.getTypeOfString(n)

	case *parser.FormatStringNode:
		typeResult = e.getTypeOfString(n)

	case *parser.ErrorNode:
		// The original's comment: evaluate the child expression as best we can so
		// the type information is cached for the completion handler.
		e.suppressDiagnostics(node, func() {
			if n.D.Child != nil {
				e.getTypeOfExpression(n.D.Child, EvalFlagsNone, nil)
			}
		}, nil)
		typeResult = &TypeResult{Type: UnknownTypeCreate(false)}

	default:
		common.Fail("Illegal node type: " + parseNodeTypeLabel(node.GetNodeType()))
	}

	if typeResult == nil {
		// The original's comment: we shouldn't get here. If we do, report an error.
		common.Fail("Unhandled expression type '" + PrintExpression(node, PrintExpressionFlagsNone) + "'")
	}

	// The original's comment: do we need to validate that the type is
	// instantiable?
	if expectingInstantiable {
		e.validateTypeIsInstantiable(typeResult, flags, node)
	}

	// The original's comment: if this is a PEP 695 type alias, remove the special
	// form so the type printer prints it as its aliased type rather than
	// TypeAliasType.
	if (flags&EvalFlagsTypeExpression) != 0 &&
		(typeResult.Type.Base().Props == nil || typeResult.Type.Base().Props.TypeForm == nil) {
		if typeResult.Type.Base().Props != nil {
			specialForm := typeResult.Type.Base().Props.SpecialForm
			if specialForm != nil && ClassTypeIsBuiltInNamed(specialForm, "TypeAliasType") {
				typeResult.Type = CloneAsSpecialForm(typeResult.Type, nil)
			}
		}
	}

	return typeResult
}

// validateTypeIsInstantiable corresponds to the function of the same name.
func (e *typeEvaluator) validateTypeIsInstantiable(
	typeResult *TypeResult, flags EvalFlags, node parser.ExpressionNode,
) {
	// The original's comment: if the type is incomplete, don't log any diagnostics
	// yet.
	if typeResult.IsIncomplete {
		return
	}

	if (flags & EvalFlagsNoTypeVarTuple) != 0 {
		if IsTypeVarTuple(typeResult.Type) && !typeResult.Type.(*TypeVarType).Priv.IsInUnion {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.TypeVarTupleContext(), node, nil)
			typeResult.Type = UnknownTypeCreate(false)
		}
	}

	if IsEffectivelyInstantiable(typeResult.Type, &IsInstantiableOptions{HonorTypeVarBounds: true}, 0) {
		return
	}

	// The original's comment: exempt ellipses.
	if IsClassInstance(typeResult.Type) &&
		ClassTypeIsBuiltInNamed(typeResult.Type.(*ClassType), "EllipsisType", "ellipsis") {
		return
	}

	// The original's comment: emit these errors only if we know we're evaluating a
	// type expression.
	if (flags & EvalFlagsTypeExpression) != 0 {
		diag := common.NewDiagnosticAddendum()
		if IsUnion(typeResult.Type) {
			DoForEachSubtype(typeResult.Type, func(subtype Type, _ int, _ []Type) {
				if !IsEffectivelyInstantiable(subtype, &IsInstantiableOptions{HonorTypeVarBounds: true}, 0) {
					diag.AddMessage(localization.LocAddendum.TypeNotClass().Format(e.PrintType(subtype, nil)))
				}
			})
		}

		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeExpectedClass().Format(e.PrintType(typeResult.Type, nil))+diag.GetString(),
			node, nil)

		typeResult.Type = UnknownTypeCreate(false)
	}

	typeResult.TypeErrors = true
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
