/*
 * typeevaluator_listset.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfListOrSet, getTypeOfListOrSetWithContext, getTypeOfListOrSetInferred,
 * getExpectedEntryTypeForIterable, inferTypeArgFromExpectedEntryType,
 * verifySetEntryOrDictKeyIsHashable, isTypeHashable.
 *
 * `[a, b]` and `{a, b}`. The literal is the same shape either way; only the
 * class name and whether keys must be hashable differ.
 *
 * The hard part is that the answer depends on what the value is being assigned
 * TO. `[1, 2]` alone is `list[int]`, but as `x: list[float] = [1, 2]` it must be
 * `list[float]`, and as `x: Sequence[int] = [1, 2]` the element type has to be
 * worked backwards through `Sequence`. So the expected type is tried FIRST, and
 * inference is the fallback rather than the main path.
 *
 * A union expected type is tried one member at a time, speculatively, and the
 * first that both produces a type and accepts it wins -- with a result that has
 * no errors preferred over an earlier one that did.
 *
 * Without an expected type, inference has a deliberate asymmetry. Combining
 * elements into a union is only done under strictListInference /
 * strictSetInference; otherwise a heterogeneous list infers `list[Unknown]`
 * rather than `list[int | str]`, because the latter is usually not what the
 * author meant and produces errors on a later `append`. A homogeneous list still
 * gets its element type. Only the first 64 elements are considered, and literals
 * are stripped -- `[1, 2]` is `list[int]`, not `list[Literal[1, 2]]`.
 *
 * An EMPTY list is marked as such on the resulting class rather than being typed
 * `list[Unknown]` and forgotten, so that a later assignment can fill it in.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getTypeOfListOrSet corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfListOrSet(
	node parser.ExpressionNode, flags EvalFlags, inferenceContext *InferenceContext,
) *TypeResult {
	if (flags&EvalFlagsTypeExpression) != 0 && node.GetNodeType() == parser.ParseNodeTypeList &&
		node.NodeBase().Parent != nil &&
		node.NodeBase().Parent.GetNodeType() != parser.ParseNodeTypeArgument {
		diag := common.NewDiagnosticAddendum()
		diag.AddMessage(localization.LocAddendum.UseListInstead())
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.ListInAnnotation()+diag.GetString(), node, nil)
	}

	flags &^= EvalFlagsTypeExpression | EvalFlagsStrLiteralAsType | EvalFlagsInstantiableType

	// The original's comment: if the expected type is a union, recursively call for
	// each of the subtypes to find one that matches.
	var expectedType Type
	if inferenceContext != nil {
		expectedType = inferenceContext.ExpectedType
	}

	if inferenceContext != nil && IsUnion(inferenceContext.ExpectedType) {
		expectedType = e.pickListOrSetUnionMember(node, flags, inferenceContext)
	}

	var expectedTypeDiagAddendum *common.DiagnosticAddendum
	if expectedType != nil {
		result := e.getTypeOfListOrSetWithContext(node, flags, MakeInferenceContext(expectedType, false, nil))
		if result != nil && !result.TypeErrors {
			return result
		}

		if result != nil {
			expectedTypeDiagAddendum = result.ExpectedTypeDiagAddendum
		}
	}

	hasExpectedType := inferenceContext != nil && inferenceContext.ExpectedType != nil
	typeResult := e.getTypeOfListOrSetInferred(node, flags, hasExpectedType)

	copied := *typeResult
	copied.ExpectedTypeDiagAddendum = expectedTypeDiagAddendum
	return &copied
}

// pickListOrSetUnionMember is the original's union arm: try each member
// speculatively and keep the best match.
func (e *typeEvaluator) pickListOrSetUnionMember(
	node parser.ExpressionNode, flags EvalFlags, inferenceContext *InferenceContext,
) Type {
	var matchingSubtype Type
	var matchingSubtypeResult *TypeResult

	DoForEachSubtypeSorted(inferenceContext.ExpectedType, func(subtype Type, _ int, _ []Type) {
		// The original's comment: use shortcut if we've already found a match.
		if matchingSubtypeResult != nil && !matchingSubtypeResult.TypeErrors {
			return
		}

		var subtypeResult *TypeResult
		e.UseSpeculativeMode(node, func() {
			subtypeResult = e.getTypeOfListOrSetWithContext(node, flags,
				MakeInferenceContext(subtype, false, nil))
		}, nil)

		if subtypeResult == nil ||
			!e.AssignType(subtype, subtypeResult.Type, nil, nil, AssignTypeFlagsDefault, 0) {
			return
		}

		// The original's comment: if this is the first result we're seeing or it's
		// the first result without errors, select it as the match.
		if matchingSubtypeResult == nil ||
			(matchingSubtypeResult.TypeErrors && !subtypeResult.TypeErrors) {
			matchingSubtype = subtype
			matchingSubtypeResult = subtypeResult
		}
	})

	return matchingSubtype
}

// getTypeOfListOrSetWithContext corresponds to the function of the same name.
//
// Its comment: attempts to determine the type of a list or set statement based
// on an expected type. Returns undefined if that type cannot be honored.
func (e *typeEvaluator) getTypeOfListOrSetWithContext(
	node parser.ExpressionNode, flags EvalFlags, inferenceContext *InferenceContext,
) *TypeResult {
	isList := node.GetNodeType() == parser.ParseNodeTypeList
	builtInClassName := "set"
	if isList {
		builtInClassName = "list"
	}
	inferenceContext.ExpectedType = TransformPossibleRecursiveTypeAlias(inferenceContext.ExpectedType, 0)

	isIncomplete := false
	typeErrors := false
	verifyHashable := !isList

	expectedEntryType := e.getExpectedEntryTypeForIterable(node,
		e.GetBuiltInType(node, builtInClassName), inferenceContext)
	if expectedEntryType == nil {
		return nil
	}

	entryTypes := []Type{}
	expectedTypeDiagAddendum := common.NewDiagnosticAddendum()

	for _, entry := range listOrSetItems(node) {
		var entryTypeResult *TypeResult

		if comprehension, ok := entry.(*parser.ComprehensionNode); ok {
			entryTypeResult = e.getElementTypeFromComprehension(comprehension,
				flags|EvalFlagsStripTupleLiterals, expectedEntryType, nil)
		} else {
			entryTypeResult = e.GetTypeOfExpression(entry, flags|EvalFlagsStripTupleLiterals,
				MakeInferenceContext(expectedEntryType, false, nil))
		}

		entryTypes = append(entryTypes, entryTypeResult.Type)

		if entryTypeResult.IsIncomplete {
			isIncomplete = true
		}

		if entryTypeResult.TypeErrors {
			typeErrors = true
		}

		if entryTypeResult.ExpectedTypeDiagAddendum != nil {
			expectedTypeDiagAddendum.AddAddendum(entryTypeResult.ExpectedTypeDiagAddendum)
		}

		if verifyHashable && !entryTypeResult.IsIncomplete && !entryTypeResult.TypeErrors {
			e.verifySetEntryOrDictKeyIsHashable(entry, entryTypeResult.Type, false)
		}
	}

	isTypeInvariant := false

	if expectedClass, ok := inferenceContext.ExpectedType.(*ClassType); ok &&
		IsClassInstance(inferenceContext.ExpectedType) {
		e.InferVarianceForClass(expectedClass)

		for _, t := range expectedClass.Shared.TypeParams {
			if TypeVarTypeGetVariance(t) == VarianceInvariant {
				isTypeInvariant = true
				break
			}
		}
	}

	specializedEntryType := e.inferTypeArgFromExpectedEntryType(
		MakeInferenceContext(expectedEntryType, false, nil), entryTypes, !isTypeInvariant)
	if specializedEntryType == nil {
		return &TypeResult{
			Type:                     UnknownTypeCreate(false),
			IsIncomplete:             isIncomplete,
			TypeErrors:               true,
			ExpectedTypeDiagAddendum: expectedTypeDiagAddendum,
		}
	}

	return &TypeResult{
		Type:                     e.GetBuiltInObject(node, builtInClassName, []Type{specializedEntryType}),
		IsIncomplete:             isIncomplete,
		TypeErrors:               typeErrors,
		ExpectedTypeDiagAddendum: expectedTypeDiagAddendum,
	}
}

// getExpectedEntryTypeForIterable corresponds to the function of the same name:
// what element type the expected container implies. It returns nil where the
// original returns undefined.
func (e *typeEvaluator) getExpectedEntryTypeForIterable(
	node parser.ExpressionNode, expectedClassType Type, inferenceContext *InferenceContext,
) Type {
	if inferenceContext == nil {
		return nil
	}

	expectedClass, ok := expectedClassType.(*ClassType)
	if !ok || !IsInstantiableClass(expectedClassType) {
		return nil
	}

	if IsAnyOrUnknown(inferenceContext.ExpectedType) {
		return inferenceContext.ExpectedType
	}

	if !IsClassInstance(inferenceContext.ExpectedType) {
		return nil
	}

	constraints := NewConstraintTracker()
	if !AddConstraintsForExpectedType(e, ClassTypeCloneAsInstance(expectedClass, true),
		inferenceContext.ExpectedType, constraints, GetTypeVarScopesForNode(node),
		node.NodeBase().TextRange.Start) {
		return nil
	}

	solved := e.SolveAndApplyConstraints(expectedClass, constraints, nil, nil)
	specializedListOrSet, ok := solved.(*ClassType)
	if !ok || specializedListOrSet.Priv.TypeArgs == nil {
		return nil
	}

	return specializedListOrSet.Priv.TypeArgs[0]
}

// getTypeOfListOrSetInferred corresponds to the function of the same name.
//
// Its comment: attempts to infer the type of a list or set statement with no
// "expected type".
func (e *typeEvaluator) getTypeOfListOrSetInferred(
	node parser.ExpressionNode, flags EvalFlags, hasExpectedType bool,
) *TypeResult {
	isList := node.GetNodeType() == parser.ParseNodeTypeList
	builtInClassName := "set"
	if isList {
		builtInClassName = "list"
	}
	verifyHashable := !isList
	isEmptyContainer := false
	isIncomplete := false
	typeErrors := false

	entryTypes := []Type{}
	for index, entry := range listOrSetItems(node) {
		var entryTypeResult *TypeResult

		if comprehension, ok := entry.(*parser.ComprehensionNode); ok && !comprehension.D.IsGenerator {
			entryTypeResult = e.getElementTypeFromComprehension(comprehension,
				flags|EvalFlagsStripTupleLiterals, nil, nil)
		} else {
			entryTypeResult = e.GetTypeOfExpression(entry, flags|EvalFlagsStripTupleLiterals, nil)
		}

		entryTypeResult.Type = StripTypeForm(
			e.convertSpecialFormToRuntimeValueEx(entryTypeResult.Type, flags, !hasExpectedType))

		if entryTypeResult.IsIncomplete {
			isIncomplete = true
		}

		if entryTypeResult.TypeErrors {
			typeErrors = true
		}

		if hasExpectedType || index < maxEntriesToUseForInference {
			entryTypes = append(entryTypes, entryTypeResult.Type)
		}

		if verifyHashable && !entryTypeResult.IsIncomplete && !entryTypeResult.TypeErrors {
			e.verifySetEntryOrDictKeyIsHashable(entry, entryTypeResult.Type, false)
		}
	}

	for i, t := range entryTypes {
		entryTypes[i] = e.StripLiteralValue(t)
	}

	var inferredEntryType Type = UnknownTypeCreate(false)
	if hasExpectedType {
		inferredEntryType = AnyTypeCreate(false)
	}

	if len(entryTypes) > 0 {
		fileInfo := GetFileInfo(node)
		// The original's comment: if there was an expected type or we're using
		// strict list inference, combine the types into a union.
		strict := (builtInClassName == "list" && fileInfo.DiagnosticRuleSet.StrictListInference) ||
			(builtInClassName == "set" && fileInfo.DiagnosticRuleSet.StrictSetInference)

		if strict || hasExpectedType {
			maxCount := maxSubtypesForInferredType
			inferredEntryType = CombineTypes(entryTypes, &CombineTypesOptions{MaxSubtypeCount: &maxCount})
		} else if AreTypesSame(entryTypes, TypeSameOptions{IgnorePseudoGeneric: true}) {
			// The original's comment: is the list or set homogeneous? If so, use
			// stricter rules. Otherwise relax the rules.
			inferredEntryType = entryTypes[0]
		}
	} else {
		isEmptyContainer = true
	}

	listOrSetClass := e.GetBuiltInType(node, builtInClassName)
	var t Type = UnknownTypeCreate(false)
	if IsInstantiableClass(listOrSetClass) {
		isTypeArgExplicit := true
		t = ClassTypeCloneAsInstance(ClassTypeSpecialize(listOrSetClass.(*ClassType),
			[]Type{inferredEntryType}, &isTypeArgExplicit, false, nil, &isEmptyContainer), true)
	}

	if isIncomplete && GetContainerDepth(t, 0) > maxInferredContainerDepth {
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	return &TypeResult{Type: t, IsIncomplete: isIncomplete, TypeErrors: typeErrors}
}

// listOrSetItems reads `node.d.items` from either node type, which the original
// reaches through a union.
func listOrSetItems(node parser.ExpressionNode) []parser.ExpressionNode {
	switch typed := node.(type) {
	case *parser.ListNode:
		return typed.D.Items
	case *parser.SetNode:
		return typed.D.Items
	}
	return nil
}

// verifySetEntryOrDictKeyIsHashable corresponds to the function of the same
// name.
func (e *typeEvaluator) verifySetEntryOrDictKeyIsHashable(
	entry parser.ExpressionNode, t Type, isDictKey bool,
) {
	// The original's comment: verify that the type is hashable.
	if e.isTypeHashable(t) {
		return
	}

	diag := common.NewDiagnosticAddendum()
	diag.AddMessage(localization.LocAddendum.UnhashableType().Format(e.PrintType(t, nil)))

	message := localization.LocMessage.UnhashableSetEntry()
	if isDictKey {
		message = localization.LocMessage.UnhashableDictKey()
	}

	e.AddDiagnostic(DiagnosticRuleReportUnhashable, message+diag.GetString(), entry, nil)
}

// isTypeHashable corresponds to the function of the same name. The answer is
// cached on the class, since the lookup is not cheap and cannot change.
func (e *typeEvaluator) isTypeHashable(t Type) bool {
	isTypeHashable := true

	DoForEachSubtype(e.MakeTopLevelTypeVarsConcrete(t, false), func(subtype Type, _ int, _ []Type) {
		if !IsClassInstance(subtype) {
			return
		}
		classType := subtype.(*ClassType)

		// The original's comment: assume the class is hashable.
		isObjectHashable := true

		// The original's comment: have we already computed and cached the
		// hashability?
		if classType.Shared.IsInstanceHashable != nil {
			isObjectHashable = *classType.Shared.IsInstanceHashable
		} else {
			isObjectHashable = e.computeInstanceHashable(classType)

			// The original's comment: cache the hashability for next time.
			cached := isObjectHashable
			classType.Shared.IsInstanceHashable = &cached
		}

		if !isObjectHashable {
			isTypeHashable = false
		}
	})

	return isTypeHashable
}

// computeInstanceHashable is the original's uncached branch. It answers from the
// SHAPE of the __hash__ declaration rather than from its type: a variable
// declaration means the class set `__hash__ = None`, and a function declaration
// means it is hashable. Evaluating the full type is not needed here.
func (e *typeEvaluator) computeInstanceHashable(classType *ClassType) bool {
	hashMember := LookUpObjectMember(classType, "__hash__", MemberAccessFlagsSkipObjectBaseClass, nil)
	if hashMember == nil || !hashMember.IsTypeDeclared {
		return true
	}

	decls := hashMember.Symbol.GetTypedDeclarations()
	synthesizedType := hashMember.Symbol.GetSynthesizedType()

	// The original's comment: handle the case where the type is synthesized (used
	// for dataclasses).
	if synthesizedType != nil {
		return !IsNoneInstance(synthesizedType.Type)
	}

	// The original's comment: assume that if '__hash__' is declared as a variable,
	// it is not hashable. If it's declared as a function, it is. We'll skip
	// evaluating its full type because that's not needed in this case.
	for _, decl := range decls {
		if _, isVar := decl.(*VariableDeclaration); !isVar {
			return true
		}
	}
	return len(decls) == 0
}

// inferTypeArgFromExpectedEntryType corresponds to the function of the same
// name. It returns nil where the original returns undefined.
func (e *typeEvaluator) inferTypeArgFromExpectedEntryType(
	inferenceContext *InferenceContext, entryTypes []Type, isNarrowable bool,
) Type {
	// The original's comment: if the expected type is Any, the resulting type
	// becomes Any.
	if IsAny(inferenceContext.ExpectedType) {
		return inferenceContext.ExpectedType
	}

	constraints := NewConstraintTracker()
	expectedType := inferenceContext.ExpectedType
	isCompatible := true

	for _, entryType := range entryTypes {
		if isCompatible && !e.AssignType(expectedType, entryType, nil, constraints,
			AssignTypeFlagsDefault, 0) {
			isCompatible = false
		}
	}

	if !isCompatible {
		return nil
	}

	if isNarrowable && len(entryTypes) > 0 {
		combinedTypes := CombineTypes(entryTypes, nil)
		if ContainsLiteralType(inferenceContext.ExpectedType, false) {
			return combinedTypes
		}
		return e.StripLiteralValue(combinedTypes)
	}

	solved := e.SolveAndApplyConstraints(inferenceContext.ExpectedType, constraints,
		&ApplyTypeVarOptions{
			ReplaceUnsolved: &ReplaceUnsolvedOptions{
				ScopeIDs:       []TypeVarScopeId{},
				TupleClassType: e.GetTupleClassType(),
			},
		}, nil)

	return MapSubtypes(solved, func(subtype Type) Type {
		if len(entryTypes) != 1 {
			return subtype
		}
		entryType := entryTypes[0]

		// The original's comment: if the entry type is a TypedDict instance, clone
		// it with additional information.
		entryClass, entryIsClass := entryType.(*ClassType)
		subtypeClass, subtypeIsClass := subtype.(*ClassType)
		if subtypeIsClass && entryIsClass &&
			IsTypeSame(subtype, entryType, TypeSameOptions{IgnoreTypedDictNarrowEntries: true}, 0) &&
			IsClass(subtype) && IsClass(entryType) && ClassTypeIsTypedDictClass(entryClass) {
			return ClassTypeCloneForNarrowedTypedDictEntries(subtypeClass,
				entryClass.Priv.TypedDictNarrowedEntries())
		}

		return subtype
	}, nil)
}

// getElementTypeFromComprehension corresponds to the comprehensions.ts function
// of the same name, which produces the type of the expression a comprehension
// yields per iteration.
