/*
 * typeevaluator_dictionary.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfDictionary, getTypeOfDictionaryWithContext,
 * getTypeOfDictionaryInferred, getKeyAndValueTypesFromDictionary.
 *
 * A dictionary display is evaluated twice-over: once bidirectionally, against
 * whatever type the context expects, and -- if that fails or there is no context
 * -- once from the entries alone. The bidirectional pass is what makes a dict
 * literal check against a TypedDict annotation, and what lets
 * `dict[str, Sequence[int]] = {"a": [1]}` infer `list[int]` for the value rather
 * than widening.
 *
 * Two behaviors here look like bugs and are not:
 *
 * - When the expected type is a bare TypeVar, the code deliberately does NOT
 *   force strict inference on the fallback path. The original carries a long
 *   comment explaining that a TypeVar such as `_T` from `sorted(Iterable[_T],
 *   key=...)` constrains nothing useful, and forcing strictness there widens
 *   heterogeneous values into a union that produces false positives.
 *
 * - Without strict inference and without an expected type, a dict whose values
 *   are not all the same type infers its value type as Unknown rather than as
 *   their union. The original explains: a dict type cannot express a per-key
 *   value type, so a union would claim more than is true.
 *
 * getKeyAndValueTypesFromDictionary returns Any as its `type`; the answer is the
 * two slices it appends into, and the returned type is never read.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getTypeOfDictionary corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfDictionary(
	node *parser.DictionaryNode, flags EvalFlags, inferenceContext *InferenceContext,
) *TypeResult {
	if flags&EvalFlagsTypeExpression != 0 {
		parent := node.NodeBase().Parent
		if parent == nil || parent.GetNodeType() != parser.ParseNodeTypeArgument {
			diag := common.NewDiagnosticAddendum()
			diag.AddMessage(localization.LocAddendum.UseDictInstead())
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.DictInAnnotation()+diag.GetString(), node, nil)
		}
	}

	// The original's comment: if the expected type is a union, analyze for each of
	// the subtypes to find one that matches.
	var expectedType Type
	if inferenceContext != nil {
		expectedType = inferenceContext.ExpectedType
	}

	if inferenceContext != nil && IsUnion(inferenceContext.ExpectedType) {
		var matchingSubtype Type
		var matchingSubtypeResult *TypeResult

		DoForEachSubtypeSorted(inferenceContext.ExpectedType, func(subtype Type, _ int, _ []Type) {
			// The original's comment: use shortcut if we've already found a match.
			if matchingSubtypeResult != nil && !matchingSubtypeResult.TypeErrors {
				return
			}

			var subtypeResult *TypeResult
			e.UseSpeculativeMode(node, func() {
				subtypeResult = e.getTypeOfDictionaryWithContext(
					node, flags, MakeInferenceContext(subtype, false, nil), nil)
			}, nil)

			if subtypeResult != nil &&
				e.AssignType(subtype, subtypeResult.Type, nil, nil, AssignTypeFlagsDefault, 0) {
				// The original's comment: if this is the first result we're seeing or it's
				// the first result without errors, select it as the match.
				if matchingSubtypeResult == nil ||
					(matchingSubtypeResult.TypeErrors && !subtypeResult.TypeErrors) {
					matchingSubtype = subtype
					matchingSubtypeResult = subtypeResult
				}
			}
		})

		expectedType = matchingSubtype
	}

	var expectedTypeDiagAddendum *common.DiagnosticAddendum
	if expectedType != nil {
		expectedTypeDiagAddendum = common.NewDiagnosticAddendum()
		result := e.getTypeOfDictionaryWithContext(
			node, flags, MakeInferenceContext(expectedType, false, nil), expectedTypeDiagAddendum)
		if result != nil {
			return result
		}
	}

	// The original's comment: don't force strict inference when the expected type
	// is a TypeVar that couldn't constrain the dict's key/value structure. A TypeVar
	// like _T from sorted(Iterable[_T], key=...) doesn't provide useful type
	// information for dict inference, and forcing strict inference would widen
	// heterogeneous value types into a union that can cause false positives (e.g.,
	// dict[str, Path | str | dict[Any, Any]] when the user meant each key to have a
	// specific type).
	// Note: bounded/constrained TypeVars with dict-like bounds are handled by
	// getTypeOfDictionaryWithContext above (via makeTopLevelTypeVarsConcrete), so
	// they don't reach this fallback.
	hasUsefulExpectedType := inferenceContext != nil && inferenceContext.ExpectedType != nil &&
		!IsTypeVar(inferenceContext.ExpectedType)
	result := e.getTypeOfDictionaryInferred(node, flags, hasUsefulExpectedType)
	result.ExpectedTypeDiagAddendum = expectedTypeDiagAddendum
	return result
}

// getTypeOfDictionaryWithContext corresponds to the function of the same name.
// It returns nil where the original returns undefined.
func (e *typeEvaluator) getTypeOfDictionaryWithContext(
	node *parser.DictionaryNode,
	flags EvalFlags,
	inferenceContext *InferenceContext,
	expectedDiagAddendum *common.DiagnosticAddendum,
) *TypeResult {
	inferenceContext.ExpectedType = TransformPossibleRecursiveTypeAlias(inferenceContext.ExpectedType, 0)
	concreteExpectedType := e.MakeTopLevelTypeVarsConcrete(inferenceContext.ExpectedType, false)

	if !IsClassInstance(concreteExpectedType) {
		return nil
	}

	keyTypes := []*TypeResultWithNode{}
	valueTypes := []*TypeResultWithNode{}
	isIncomplete := false
	typeErrors := false

	// The original's comment: handle TypedDict's as a special case.
	if ClassTypeIsTypedDictClass(concreteExpectedType.(*ClassType)) {
		// The original's comment: remove any conditions associated with the type so
		// the resulting type isn't considered compatible with a bound TypeVar.
		expectedClass := CloneForCondition(concreteExpectedType, nil).(*ClassType)

		expectedTypedDictEntries := GetTypedDictMembersForClass(e, expectedClass, false)

		// The original's comment: infer the key and value types if possible.
		keyValueTypeResult := e.getKeyAndValueTypesFromDictionary(
			node, flags, &keyTypes, &valueTypes, true, true, nil, nil,
			expectedTypedDictEntries, expectedDiagAddendum)

		if keyValueTypeResult.IsIncomplete {
			isIncomplete = true
		}

		if keyValueTypeResult.TypeErrors {
			typeErrors = true
		}

		// The original's comment: don't overwrite existing expectedDiagAddendum
		// messages if they were already provided by getKeyValueTypesFromDictionary.
		var passedAddendum *common.DiagnosticAddendum
		if expectedDiagAddendum != nil && expectedDiagAddendum.IsEmpty() {
			passedAddendum = expectedDiagAddendum
		}

		resultTypedDict := AssignToTypedDict(e, expectedClass, keyTypes, valueTypes, passedAddendum)
		if resultTypedDict != nil {
			return &TypeResult{Type: resultTypedDict, IsIncomplete: isIncomplete}
		}

		return nil
	}

	var expectedKeyType Type
	var expectedValueType Type

	if IsAnyOrUnknown(inferenceContext.ExpectedType) {
		expectedKeyType = inferenceContext.ExpectedType
		expectedValueType = inferenceContext.ExpectedType
	} else {
		builtInDict := e.GetBuiltInObject(node, "dict", nil)
		if !IsClassInstance(builtInDict) {
			return nil
		}

		dictConstraints := NewConstraintTracker()
		if !AddConstraintsForExpectedType(
			e,
			builtInDict.(*ClassType),
			inferenceContext.ExpectedType,
			dictConstraints,
			GetTypeVarScopesForNode(node),
			node.NodeBase().TextRange.Start,
		) {
			return nil
		}

		solved := e.SolveAndApplyConstraints(
			ClassTypeCloneAsInstantiable(builtInDict.(*ClassType), true), dictConstraints, nil, nil)
		specializedDict, ok := solved.(*ClassType)
		if !ok || len(specializedDict.Priv.TypeArgs) != 2 {
			return nil
		}

		expectedKeyType = specializedDict.Priv.TypeArgs[0]
		expectedValueType = specializedDict.Priv.TypeArgs[1]
	}

	// The original's comment: Dict and MutableMapping types have invariant value
	// types, so they cannot be narrowed further. Other super-types like Mapping,
	// Collection, and Iterable use covariant value types, so they can be narrowed.
	isValueTypeInvariant := false
	if IsClassInstance(inferenceContext.ExpectedType) {
		expectedClass := inferenceContext.ExpectedType.(*ClassType)
		if len(expectedClass.Shared.TypeParams) >= 2 {
			valueTypeParam := expectedClass.Shared.TypeParams[1]
			if TypeVarTypeGetVariance(valueTypeParam) == VarianceInvariant {
				isValueTypeInvariant = true
			}
		}
	}

	// The original's comment: infer the key and value types if possible.
	keyValueResult := e.getKeyAndValueTypesFromDictionary(
		node, flags, &keyTypes, &valueTypes, true, isValueTypeInvariant,
		expectedKeyType, expectedValueType, nil, expectedDiagAddendum)

	if keyValueResult.IsIncomplete {
		isIncomplete = true
	}

	if keyValueResult.TypeErrors {
		typeErrors = true
	}

	keyTypeList := make([]Type, 0, len(keyTypes))
	for _, result := range keyTypes {
		keyTypeList = append(keyTypeList, result.Type)
	}
	valueTypeList := make([]Type, 0, len(valueTypes))
	for _, result := range valueTypes {
		valueTypeList = append(valueTypeList, result.Type)
	}

	specializedKeyType := e.inferTypeArgFromExpectedEntryType(
		MakeInferenceContext(expectedKeyType, false, nil), keyTypeList, false)
	specializedValueType := e.inferTypeArgFromExpectedEntryType(
		MakeInferenceContext(expectedValueType, false, nil), valueTypeList, !isValueTypeInvariant)
	if specializedKeyType == nil || specializedValueType == nil {
		return nil
	}

	t := e.GetBuiltInObject(node, "dict", []Type{specializedKeyType, specializedValueType})
	return &TypeResult{Type: t, IsIncomplete: isIncomplete, TypeErrors: typeErrors}
}

// getTypeOfDictionaryInferred corresponds to the function of the same name.
//
// The original's comment: attempts to infer the type of a dictionary statement.
// If hasExpectedType is true, strict inference is used for the subexpressions.
func (e *typeEvaluator) getTypeOfDictionaryInferred(
	node *parser.DictionaryNode, flags EvalFlags, hasExpectedType bool,
) *TypeResult {
	var fallbackType Type = UnknownTypeCreate(false)
	if hasExpectedType {
		fallbackType = AnyTypeCreate(false)
	}
	keyType := fallbackType
	valueType := fallbackType

	keyTypeResults := []*TypeResultWithNode{}
	valueTypeResults := []*TypeResultWithNode{}

	isEmptyContainer := false
	isIncomplete := false
	typeErrors := false

	// The original's comment: infer the key and value types if possible.
	keyValueResult := e.getKeyAndValueTypesFromDictionary(
		node, flags, &keyTypeResults, &valueTypeResults, hasExpectedType, false, nil, nil, nil, nil)

	if keyValueResult.IsIncomplete {
		isIncomplete = true
	}

	if keyValueResult.TypeErrors {
		typeErrors = true
	}

	// The original's comment: strip any literal values and TypeForm types.
	strip := func(results []*TypeResultWithNode) []Type {
		out := make([]Type, 0, len(results))
		for _, r := range results {
			out = append(out, StripTypeForm(
				e.convertSpecialFormToRuntimeValueEx(e.StripLiteralValue(r.Type), flags, !hasExpectedType)))
		}
		return out
	}
	keyTypes := strip(keyTypeResults)
	valueTypes := strip(valueTypeResults)

	strictDictInference := GetFileInfo(node).DiagnosticRuleSet.StrictDictionaryInference

	if len(keyTypes) > 0 {
		if strictDictInference || hasExpectedType {
			keyType = CombineTypes(keyTypes, nil)
		} else if AreTypesSame(keyTypes, TypeSameOptions{IgnorePseudoGeneric: true}) {
			keyType = keyTypes[0]
		} else {
			keyType = fallbackType
		}
	} else {
		keyType = fallbackType
	}

	// The original's comment: if the value type differs and we're not using "strict
	// inference mode", we need to back off because we can't properly represent the
	// mappings between different keys and associated value types. If all the values
	// are the same type, we'll assume that all values in this dictionary should be
	// the same.
	if len(valueTypes) > 0 {
		if strictDictInference || hasExpectedType {
			valueType = CombineTypes(valueTypes, nil)
		} else if AreTypesSame(valueTypes, TypeSameOptions{IgnorePseudoGeneric: true}) {
			valueType = valueTypes[0]
		} else {
			valueType = fallbackType
		}
	} else {
		valueType = fallbackType
		isEmptyContainer = true
	}

	dictClass := e.GetBuiltInType(node, "dict")
	var t Type = UnknownTypeCreate(false)
	if IsInstantiableClass(dictClass) {
		isTypeArgExplicit := true
		t = ClassTypeCloneAsInstance(
			ClassTypeSpecialize(dictClass.(*ClassType), []Type{keyType, valueType},
				&isTypeArgExplicit, false, nil, &isEmptyContainer), true)
	}

	if isIncomplete {
		if GetContainerDepth(t, 0) > maxInferredContainerDepth {
			return &TypeResult{Type: UnknownTypeCreate(false)}
		}
	}

	return &TypeResult{Type: t, IsIncomplete: isIncomplete, TypeErrors: typeErrors}
}

// getKeyAndValueTypesFromDictionary corresponds to the function of the same
// name. The two slices are appended into, so they are passed by pointer; the
// original mutates the arrays its caller owns.
func (e *typeEvaluator) getKeyAndValueTypesFromDictionary(
	node *parser.DictionaryNode,
	flags EvalFlags,
	keyTypes *[]*TypeResultWithNode,
	valueTypes *[]*TypeResultWithNode,
	forceStrictInference bool,
	isValueTypeInvariant bool,
	expectedKeyType Type,
	expectedValueType Type,
	expectedTypedDictEntries *TypedDictEntries,
	expectedDiagAddendum *common.DiagnosticAddendum,
) *TypeResult {
	isIncomplete := false
	typeErrors := false

	// The original's comment: mask out some of the flags that are not applicable
	// for a dictionary key even if it appears within an inlined TypedDict
	// annotation.
	keyFlags := flags &^ (EvalFlagsTypeExpression | EvalFlagsStrLiteralAsType | EvalFlagsInstantiableType)

	// The original's comment: infer the key and value types if possible.
	for index, entryNode := range node.D.Items {
		addUnknown := true

		switch entry := entryNode.(type) {
		case *parser.DictionaryKeyEntryNode:
			effectiveExpectedKey := expectedKeyType
			if effectiveExpectedKey == nil && forceStrictInference {
				effectiveExpectedKey = NeverTypeCreateNever()
			}

			keyTypeResult := e.getTypeOfExpression(
				entry.D.KeyExpr,
				keyFlags|EvalFlagsStripTupleLiterals,
				MakeInferenceContext(effectiveExpectedKey, false, nil))

			if keyTypeResult.IsIncomplete {
				isIncomplete = true
			}

			if keyTypeResult.TypeErrors {
				typeErrors = true
			}

			keyType := keyTypeResult.Type

			if !keyTypeResult.IsIncomplete && !keyTypeResult.TypeErrors {
				e.verifySetEntryOrDictKeyIsHashable(entry.D.KeyExpr, keyType, true)
			}

			if expectedDiagAddendum != nil && keyTypeResult.ExpectedTypeDiagAddendum != nil {
				expectedDiagAddendum.AddAddendum(keyTypeResult.ExpectedTypeDiagAddendum)
			}

			var valueTypeResult *TypeResult
			var entryInferenceContext *InferenceContext

			var tdValueType Type
			usedTypedDictEntry := false
			if expectedTypedDictEntries != nil && IsClassInstance(keyType) &&
				ClassTypeIsBuiltInNamed(keyType.(*ClassType), "str") &&
				IsLiteralType(keyType.(*ClassType)) {
				literalName := string(keyType.(*ClassType).Priv.LiteralValue.(LiteralString))
				known, hasKnown := expectedTypedDictEntries.KnownItems.Get(literalName)
				if hasKnown || expectedTypedDictEntries.ExtraItems != nil {
					usedTypedDictEntry = true
					if hasKnown && known != nil {
						tdValueType = known.ValueType
					} else if expectedTypedDictEntries.ExtraItems != nil {
						tdValueType = expectedTypedDictEntries.ExtraItems.ValueType
					}
				}
			}

			var effectiveValueType Type
			if usedTypedDictEntry {
				effectiveValueType = tdValueType
			} else {
				effectiveValueType = expectedValueType
				if effectiveValueType == nil && forceStrictInference {
					effectiveValueType = NeverTypeCreateNever()
				}
			}

			if effectiveValueType != nil {
				liveTypeVarScopes := GetTypeVarScopesForNode(node)
				effectiveValueType = TransformExpectedType(
					effectiveValueType, liveTypeVarScopes, node.NodeBase().TextRange.Start)
			}
			entryInferenceContext = MakeInferenceContext(effectiveValueType, false, nil)
			valueTypeResult = e.getTypeOfExpression(
				entry.D.ValueExpr, flags|EvalFlagsStripTupleLiterals, entryInferenceContext)

			if entryInferenceContext != nil && !valueTypeResult.TypeErrors {
				fromExpectedType := e.inferTypeArgFromExpectedEntryType(
					entryInferenceContext, []Type{valueTypeResult.Type}, !isValueTypeInvariant)

				if fromExpectedType != nil {
					clone := *valueTypeResult
					clone.Type = fromExpectedType
					valueTypeResult = &clone
				}
			}

			if expectedDiagAddendum != nil && valueTypeResult.ExpectedTypeDiagAddendum != nil {
				expectedDiagAddendum.AddAddendum(valueTypeResult.ExpectedTypeDiagAddendum)
			}

			valueType := valueTypeResult.Type
			if valueTypeResult.IsIncomplete {
				isIncomplete = true
			}

			if valueTypeResult.TypeErrors {
				typeErrors = true
			}

			if forceStrictInference || index < maxEntriesToUseForInference {
				// The original's comment: if an existing key has the same literal type,
				// delete the previous key since we're overwriting it here.
				if IsClass(keyType) && IsLiteralType(keyType.(*ClassType)) {
					existingIndex := -1
					for i, kt := range *keyTypes {
						if IsTypeSame(keyType, kt.Type, TypeSameOptions{}, 0) {
							existingIndex = i
							break
						}
					}
					if existingIndex >= 0 {
						*keyTypes = append((*keyTypes)[:existingIndex], (*keyTypes)[existingIndex+1:]...)
						*valueTypes = append((*valueTypes)[:existingIndex], (*valueTypes)[existingIndex+1:]...)
					}
				}

				*keyTypes = append(*keyTypes,
					&TypeResultWithNode{TypeResult: TypeResult{Type: keyType}, Node: entry.D.KeyExpr})
				*valueTypes = append(*valueTypes,
					&TypeResultWithNode{TypeResult: TypeResult{Type: valueType}, Node: entry.D.ValueExpr})
			}

			addUnknown = false

		case *parser.DictionaryExpandEntryNode:
			var expandExpectedType Type
			if expectedKeyType != nil && expectedValueType != nil {
				if e.prefetched != nil && e.prefetched.SupportsKeysAndGetItemClass != nil &&
					IsInstantiableClass(e.prefetched.SupportsKeysAndGetItemClass) {
					expandExpectedType = ClassTypeCloneAsInstance(
						ClassTypeSpecialize(
							e.prefetched.SupportsKeysAndGetItemClass.(*ClassType),
							[]Type{expectedKeyType, expectedValueType}, nil, false, nil, nil), true)
				}
			}

			entryInferenceContext := MakeInferenceContext(expandExpectedType, false, nil)
			unexpandedTypeResult := e.getTypeOfExpression(
				entry.D.Expr, flags|EvalFlagsStripTupleLiterals, entryInferenceContext)

			if entryInferenceContext != nil && !unexpandedTypeResult.TypeErrors {
				fromExpectedType := e.inferTypeArgFromExpectedEntryType(
					entryInferenceContext, []Type{unexpandedTypeResult.Type}, !isValueTypeInvariant)

				if fromExpectedType != nil {
					clone := *unexpandedTypeResult
					clone.Type = fromExpectedType
					unexpandedTypeResult = &clone
				}
			}

			if unexpandedTypeResult.IsIncomplete {
				isIncomplete = true
			}

			if unexpandedTypeResult.TypeErrors {
				typeErrors = true
			}

			unexpandedType := unexpandedTypeResult.Type

			switch {
			case IsAnyOrUnknown(unexpandedType):
				if forceStrictInference || index < maxEntriesToUseForInference {
					*keyTypes = append(*keyTypes,
						&TypeResultWithNode{TypeResult: TypeResult{Type: unexpandedType}, Node: entry})
					*valueTypes = append(*valueTypes,
						&TypeResultWithNode{TypeResult: TypeResult{Type: unexpandedType}, Node: entry})
				}
				addUnknown = false

			case IsClassInstance(unexpandedType) && ClassTypeIsTypedDictClass(unexpandedType.(*ClassType)):
				// The original's comment: handle dictionary expansion for a TypedDict.
				if e.prefetched != nil && e.prefetched.StrClass != nil &&
					IsInstantiableClass(e.prefetched.StrClass) {
					strObject := ClassTypeCloneAsInstance(e.prefetched.StrClass.(*ClassType), true)
					tdEntries := GetTypedDictMembersForClass(e, unexpandedType.(*ClassType), true)

					for _, name := range tdEntries.KnownItems.Keys() {
						tdEntry, _ := tdEntries.KnownItems.Get(name)
						if tdEntry.IsRequired || tdEntry.IsProvided {
							*keyTypes = append(*keyTypes, &TypeResultWithNode{
								TypeResult: TypeResult{
									Type: ClassTypeCloneWithLiteral(strObject, LiteralString(name))},
								Node: entry,
							})
							*valueTypes = append(*valueTypes, &TypeResultWithNode{
								TypeResult: TypeResult{Type: tdEntry.ValueType}, Node: entry})
						}
					}

					if expectedTypedDictEntries == nil {
						*keyTypes = append(*keyTypes, &TypeResultWithNode{
							TypeResult: TypeResult{Type: ClassTypeCloneAsInstance(strObject, true)},
							Node:       entry,
						})
						extraValueType := e.GetObjectType()
						if tdEntries.ExtraItems != nil {
							extraValueType = tdEntries.ExtraItems.ValueType
						}
						*valueTypes = append(*valueTypes, &TypeResultWithNode{
							TypeResult: TypeResult{Type: extraValueType}, Node: entry})
					}

					addUnknown = false
				}

			case e.prefetched != nil && e.prefetched.SupportsKeysAndGetItemClass != nil &&
				IsInstantiableClass(e.prefetched.SupportsKeysAndGetItemClass):
				mappingConstraints := NewConstraintTracker()

				supportsKeysAndGetItemClass := SelfSpecializeClass(
					e.prefetched.SupportsKeysAndGetItemClass.(*ClassType), nil)

				if e.AssignType(
					ClassTypeCloneAsInstance(supportsKeysAndGetItemClass, true),
					unexpandedType,
					nil,
					mappingConstraints,
					AssignTypeFlagsRetainLiteralsForTypeVar,
					0,
				) {
					solved := e.SolveAndApplyConstraints(
						supportsKeysAndGetItemClass, mappingConstraints, nil, nil)
					if specializedMapping, ok := solved.(*ClassType); ok {
						typeArgs := specializedMapping.Priv.TypeArgs
						if len(typeArgs) >= 2 {
							if forceStrictInference || index < maxEntriesToUseForInference {
								*keyTypes = append(*keyTypes, &TypeResultWithNode{
									TypeResult: TypeResult{Type: typeArgs[0]}, Node: entry})
								*valueTypes = append(*valueTypes, &TypeResultWithNode{
									TypeResult: TypeResult{Type: typeArgs[1]}, Node: entry})
							}
							addUnknown = false
						}
					}
				} else {
					e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
						localization.LocMessage.DictUnpackIsNotMapping(), entry, nil)
				}
			}

		case *parser.ComprehensionNode:
			dictEntryTypeResult := e.getElementTypeFromComprehension(
				entry, flags|EvalFlagsStripTupleLiterals, expectedValueType, expectedKeyType)
			dictEntryType := dictEntryTypeResult.Type
			if dictEntryTypeResult.IsIncomplete {
				isIncomplete = true
			}

			if dictEntryTypeResult.TypeErrors {
				typeErrors = true
			}

			// The original's comment: the result should be a tuple.
			if IsClassInstance(dictEntryType) && IsTupleClass(dictEntryType.(*ClassType)) {
				tupleArgs := dictEntryType.(*ClassType).Priv.TupleTypeArgs
				if len(tupleArgs) == 2 {
					if forceStrictInference || index < maxEntriesToUseForInference {
						*keyTypes = append(*keyTypes, &TypeResultWithNode{
							TypeResult: TypeResult{Type: tupleArgs[0].Type}, Node: entry})
						*valueTypes = append(*valueTypes, &TypeResultWithNode{
							TypeResult: TypeResult{Type: tupleArgs[1].Type}, Node: entry})
					}
					addUnknown = false
				}
			}
		}

		if addUnknown {
			if forceStrictInference || index < maxEntriesToUseForInference {
				*keyTypes = append(*keyTypes, &TypeResultWithNode{
					TypeResult: TypeResult{Type: UnknownTypeCreate(false)}, Node: entryNode})
				*valueTypes = append(*valueTypes, &TypeResultWithNode{
					TypeResult: TypeResult{Type: UnknownTypeCreate(false)}, Node: entryNode})
			}
		}
	}

	return &TypeResult{Type: AnyTypeCreate(false), IsIncomplete: isIncomplete, TypeErrors: typeErrors}
}
