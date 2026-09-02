/*
 * patternmatching_mapping.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/patternMatching.ts (pyright 1.1.412):
 * narrowTypeBasedOnMappingPattern, getMappingPatternInfo and
 * narrowTypeBasedOnValuePattern.
 *
 * The mapping pattern's negative direction is almost entirely a no-op, and
 * deliberately so: a dict that failed to match `{"k": v}` may simply be missing
 * the key, which says nothing about its type. There are exactly two exceptions.
 * A bare `{**rest}` pattern matches every mapping, so the negative case keeps
 * only the non-mappings. And a single literal-key/literal-value entry can
 * discriminate a union of TypedDicts -- that is the tagged-union idiom, and it
 * is the reason the negative branch bothers to inspect TypedDict members at all.
 *
 * In the positive direction the TypedDict handling does something the dict
 * handling cannot: matching a not-required key *proves the key is present*, so
 * the subtype is replaced with a narrowed TypedDict recording that. That is what
 * makes a subsequent `d["k"]` access legal without a second check.
 *
 * The value pattern (`case Color.RED:`) compares by equality rather than by
 * type, which is why it ends by asking for `__eq__` rather than by testing
 * assignability. Its negative direction can eliminate an enum member only by
 * enumerating the literals of the subject and dropping the matched one -- there
 * is no general way to subtract a value from a type.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// narrowTypeBasedOnMappingPattern corresponds to the function of the same name.
func narrowTypeBasedOnMappingPattern(
	evaluator TypeEvaluator, t Type, pattern *parser.PatternMappingNode, isPositiveTest bool,
) Type {
	t = TransformPossibleRecursiveTypeAlias(t, 0)

	if !isPositiveTest {
		return narrowTypeBasedOnMappingPatternNegative(evaluator, t, pattern)
	}

	mappingInfo := getMappingPatternInfo(evaluator, t, pattern)

	// The original's comment: further narrow based on pattern entry types.
	filtered := mappingInfo[:0]
	for _, mappingSubtypeInfo := range mappingInfo {
		if mappingSubtypeInfo.IsDefinitelyNotMapping {
			continue
		}

		if narrowMappingEntries(evaluator, pattern, mappingSubtypeInfo, isPositiveTest) {
			filtered = append(filtered, mappingSubtypeInfo)
		}
	}
	mappingInfo = filtered

	subtypes := make([]Type, 0, len(mappingInfo))
	for _, entry := range mappingInfo {
		subtypes = append(subtypes, entry.Subtype)
	}
	return CombineTypes(subtypes, nil)
}

// narrowTypeBasedOnMappingPatternNegative is the original's `if (!isPositiveTest)`
// block.
func narrowTypeBasedOnMappingPatternNegative(
	evaluator TypeEvaluator, t Type, pattern *parser.PatternMappingNode,
) Type {
	// The original's comment: handle the case where the pattern consists only of a
	// "**x" entry. Such a pattern matches every mapping, so only non-mappings
	// survive.
	if len(pattern.D.Entries) == 1 &&
		pattern.D.Entries[0].GetNodeType() == parser.ParseNodeTypePatternMappingExpandEntry {
		mappingInfo := getMappingPatternInfo(evaluator, t, pattern)
		subtypes := []Type{}
		for _, m := range mappingInfo {
			if !m.IsDefinitelyMapping {
				subtypes = append(subtypes, m.Subtype)
			}
		}
		return CombineTypes(subtypes, nil)
	}

	if len(pattern.D.Entries) != 1 ||
		pattern.D.Entries[0].GetNodeType() != parser.ParseNodeTypePatternMappingKeyEntry {
		return t
	}

	// The original's comment: handle the case where the type is a union that
	// includes a TypedDict with a field discriminated by a literal.
	keyEntry := pattern.D.Entries[0].(*parser.PatternMappingKeyEntryNode)
	keyPattern := keyEntry.D.KeyPattern
	valuePattern := keyEntry.D.ValuePattern

	if keyPattern.GetNodeType() != parser.ParseNodeTypePatternLiteral ||
		valuePattern.GetNodeType() != parser.ParseNodeTypePatternAs {
		return t
	}
	valueAsPattern := valuePattern.(*parser.PatternAsNode)
	for _, orPattern := range valueAsPattern.D.OrPatterns {
		if orPattern.GetNodeType() != parser.ParseNodeTypePatternLiteral {
			return t
		}
	}

	keyType := evaluator.GetTypeOfExpression(
		keyPattern.(*parser.PatternLiteralNode).D.Expr, EvalFlagsNone, nil).Type

	// The original's comment: the key type must be a str literal.
	if !IsClassInstance(keyType) || !ClassTypeIsBuiltInNamed(keyType.(*ClassType), "str") ||
		keyType.(*ClassType).Priv.LiteralValue == nil {
		return t
	}
	keyLiteral, ok := keyType.(*ClassType).Priv.LiteralValue.(LiteralString)
	if !ok {
		return t
	}
	keyValue := string(keyLiteral)

	valueTypes := make([]Type, 0, len(valueAsPattern.D.OrPatterns))
	for _, orPattern := range valueAsPattern.D.OrPatterns {
		valueTypes = append(valueTypes, evaluator.GetTypeOfExpression(
			orPattern.(*parser.PatternLiteralNode).D.Expr, EvalFlagsNone, nil).Type)
	}

	return MapSubtypes(t, func(subtype Type) Type {
		if !IsClassInstance(subtype) || !ClassTypeIsTypedDictClass(subtype.(*ClassType)) {
			return subtype
		}

		typedDictMembers := GetTypedDictMembersForClass(evaluator, subtype.(*ClassType), true)
		member, found := typedDictMembers.KnownItems.Get(keyValue)

		if !found || member == nil || !(member.IsRequired || member.IsProvided) ||
			!IsClassInstance(member.ValueType) {
			return subtype
		}
		memberValueType := member.ValueType.(*ClassType)

		// The original's comment: if there's at least one literal value pattern
		// that matches the literal type of the member, we can eliminate this type.
		for _, valueType := range valueTypes {
			if IsClassInstance(valueType) &&
				ClassTypeIsSameGenericClass(valueType.(*ClassType), memberValueType, 0) &&
				literalValuesEqual(valueType.(*ClassType).Priv.LiteralValue,
					memberValueType.Priv.LiteralValue) {
				return nil
			}
		}

		return subtype
	}, nil)
}

// narrowMappingEntries is the body of the original's positive-case filter
// callback. It mutates mappingSubtypeInfo in place, as the original does.
func narrowMappingEntries(
	evaluator TypeEvaluator,
	pattern *parser.PatternMappingNode,
	mappingSubtypeInfo *MappingPatternInfo,
	isPositiveTest bool,
) bool {
	isPlausibleMatch := true

	for _, entry := range pattern.D.Entries {
		mappingEntry, ok := entry.(*parser.PatternMappingKeyEntryNode)
		if !ok {
			continue
		}

		if mappingSubtypeInfo.TypedDict != nil {
			narrowedKeyType := NarrowTypeBasedOnPattern(evaluator,
				evaluator.GetBuiltInObject(pattern, "str", nil),
				mappingEntry.D.KeyPattern, isPositiveTest)

			if IsNever(narrowedKeyType) {
				isPlausibleMatch = false
			}

			valueType := MapSubtypes(narrowedKeyType, func(keySubtype Type) Type {
				if IsAnyOrUnknown(keySubtype) {
					return keySubtype
				}

				if !IsClassInstance(keySubtype) ||
					!ClassTypeIsBuiltInNamed(keySubtype.(*ClassType), "str") {
					return nil
				}

				if !IsLiteralType(keySubtype.(*ClassType)) {
					return UnknownTypeCreate(false)
				}

				keyLiteral, ok := keySubtype.(*ClassType).Priv.LiteralValue.(LiteralString)
				if !ok {
					return nil
				}

				tdEntries := GetTypedDictMembersForClass(evaluator, mappingSubtypeInfo.TypedDict, false)
				valueEntry, found := tdEntries.KnownItems.Get(string(keyLiteral))
				if !found || valueEntry == nil {
					return nil
				}

				narrowedValueType := NarrowTypeBasedOnPattern(
					evaluator, valueEntry.ValueType, mappingEntry.D.ValuePattern, true)
				if IsNever(narrowedValueType) {
					return nil
				}

				// The original's comment: if this is a "NotRequired" entry that has
				// not yet been demonstrated to be present, we can mark it as
				// "provided" at this point. Matching the key is the proof.
				if !valueEntry.IsRequired && !valueEntry.IsProvided &&
					IsTypeSame(mappingSubtypeInfo.Subtype, mappingSubtypeInfo.TypedDict,
						TypeSameOptions{}, 0) {
					newNarrowedEntriesMap := common.NewOrderedMap[string, *TypedDictEntry]()
					if existing := mappingSubtypeInfo.TypedDict.Priv.TypedDictNarrowedEntries; existing != nil {
						existing.ForEach(func(v *TypedDictEntry, k string) {
							newNarrowedEntriesMap.Set(k, v)
						})
					}
					newNarrowedEntriesMap.Set(string(keyLiteral), &TypedDictEntry{
						ValueType:  valueEntry.ValueType,
						IsReadOnly: valueEntry.IsReadOnly,
						IsRequired: false,
						IsProvided: true,
					})

					// The original's comment: clone the TypedDict object with the
					// new entries.
					mappingSubtypeInfo.Subtype = ClassTypeCloneAsInstance(
						ClassTypeCloneForNarrowedTypedDictEntries(
							ClassTypeCloneAsInstantiable(mappingSubtypeInfo.TypedDict, false),
							newNarrowedEntriesMap), false)
					mappingSubtypeInfo.TypedDict = mappingSubtypeInfo.Subtype.(*ClassType)
				}

				return narrowedValueType
			}, nil)

			if IsNever(valueType) {
				isPlausibleMatch = false
			}
			continue
		}

		if mappingSubtypeInfo.DictTypeArgs != nil {
			narrowedKeyType := NarrowTypeBasedOnPattern(evaluator,
				mappingSubtypeInfo.DictTypeArgs.Key, mappingEntry.D.KeyPattern, isPositiveTest)
			narrowedValueType := NarrowTypeBasedOnPattern(evaluator,
				mappingSubtypeInfo.DictTypeArgs.Value, mappingEntry.D.ValuePattern, isPositiveTest)
			if IsNever(narrowedKeyType) || IsNever(narrowedValueType) {
				isPlausibleMatch = false
			}
		}
	}

	return isPlausibleMatch
}

// getMappingPatternInfo corresponds to the function of the same name. The
// original's comment: returns information about all subtypes that match the
// definition of a "mapping" as specified in PEP 634.
func getMappingPatternInfo(
	evaluator TypeEvaluator, t Type, node parser.ParseNode,
) []*MappingPatternInfo {
	mappingInfo := []*MappingPatternInfo{}

	DoForEachSubtype(t, func(subtype Type, _ int, _ []Type) {
		concreteSubtype := evaluator.MakeTopLevelTypeVarsConcrete(subtype, false)

		if IsAnyOrUnknown(concreteSubtype) {
			mappingInfo = append(mappingInfo, &MappingPatternInfo{
				Subtype:                subtype,
				IsDefinitelyMapping:    false,
				IsDefinitelyNotMapping: false,
				DictTypeArgs: &mappingDictTypeArgs{
					Key:   concreteSubtype,
					Value: concreteSubtype,
				},
			})
			return
		}

		if !IsClassInstance(concreteSubtype) {
			return
		}

		// The original's comment: is it a TypedDict?
		if ClassTypeIsTypedDictClass(concreteSubtype.(*ClassType)) {
			mappingInfo = append(mappingInfo, &MappingPatternInfo{
				Subtype:                subtype,
				IsDefinitelyMapping:    true,
				IsDefinitelyNotMapping: false,
				TypedDict:              concreteSubtype.(*ClassType),
			})
			return
		}

		mappingType := evaluator.GetTypingType(node, "Mapping")
		if mappingType == nil || !IsInstantiableClass(mappingType) {
			return
		}
		mappingObject := ClassTypeCloneAsInstance(mappingType.(*ClassType), false)

		// The original's comment: is it a subtype of Mapping?
		constraints := NewConstraintTracker()
		if evaluator.AssignType(mappingObject, subtype, nil, constraints, AssignTypeFlagsDefault, 0) {
			if specializedMapping, ok := evaluator.SolveAndApplyConstraints(
				mappingObject, constraints, nil, nil).(*ClassType); ok {
				if len(specializedMapping.Priv.TypeArgs) >= 2 {
					mappingInfo = append(mappingInfo, &MappingPatternInfo{
						Subtype:                subtype,
						IsDefinitelyMapping:    true,
						IsDefinitelyNotMapping: false,
						DictTypeArgs: &mappingDictTypeArgs{
							Key:   specializedMapping.Priv.TypeArgs[0],
							Value: specializedMapping.Priv.TypeArgs[1],
						},
					})
				}
			}
			return
		}

		// The original's comment: is it a superclass of Mapping? Such a subject
		// might be a mapping at runtime, so it is neither definitely one nor
		// definitely not.
		if evaluator.AssignType(subtype, mappingObject, nil, nil, AssignTypeFlagsDefault, 0) {
			mappingInfo = append(mappingInfo, &MappingPatternInfo{
				Subtype:                subtype,
				IsDefinitelyMapping:    false,
				IsDefinitelyNotMapping: false,
				DictTypeArgs: &mappingDictTypeArgs{
					Key:   UnknownTypeCreate(false),
					Value: UnknownTypeCreate(false),
				},
			})
			return
		}

		mappingInfo = append(mappingInfo, &MappingPatternInfo{
			Subtype:                subtype,
			IsDefinitelyMapping:    false,
			IsDefinitelyNotMapping: true,
		})
	})

	return mappingInfo
}

// narrowTypeBasedOnValuePattern corresponds to the function of the same name.
func narrowTypeBasedOnValuePattern(
	evaluator TypeEvaluator, subjectType Type, pattern *parser.PatternValueNode, isPositiveTest bool,
) Type {
	valueType := evaluator.GetTypeOfExpression(pattern.D.Expr, EvalFlagsNone, nil).Type
	narrowedSubtypes := []Type{}

	evaluator.MapSubtypesExpandTypeVars(valueType, nil,
		func(valueSubtypeExpanded Type, valueSubtypeUnexpanded Type) Type {
			narrowedSubtypes = append(narrowedSubtypes, evaluator.MapSubtypesExpandTypeVars(
				subjectType,
				&EvaluatorMapSubtypesOptions{ConditionFilter: typeConditionPtrs(GetTypeCondition(valueSubtypeExpanded))},
				func(subjectSubtypeExpanded Type, _ Type) Type {
					return narrowValuePatternSubtype(evaluator, pattern, isPositiveTest,
						valueSubtypeExpanded, valueSubtypeUnexpanded, subjectSubtypeExpanded)
				}))

			return nil
		})

	return CombineTypes(narrowedSubtypes, nil)
}

// narrowValuePatternSubtype is the innermost callback of the original's
// narrowTypeBasedOnValuePattern.
func narrowValuePatternSubtype(
	evaluator TypeEvaluator,
	pattern *parser.PatternValueNode,
	isPositiveTest bool,
	valueSubtypeExpanded Type,
	valueSubtypeUnexpanded Type,
	subjectSubtypeExpanded Type,
) Type {
	// The original's comment: if this is a negative test, see if it's an enum
	// value.
	if !isPositiveTest {
		if IsClassInstance(subjectSubtypeExpanded) && IsClassInstance(valueSubtypeExpanded) &&
			IsSameWithoutLiteralValue(subjectSubtypeExpanded, valueSubtypeExpanded) {
			subjectClass := subjectSubtypeExpanded.(*ClassType)
			valueClass := valueSubtypeExpanded.(*ClassType)

			if !IsLiteralType(subjectClass) && IsLiteralType(valueClass) {
				// Subtracting a value from a type is only possible when the type's
				// values can be enumerated -- i.e. an enum or a bool.
				if expandedLiterals := EnumerateLiteralsForType(evaluator, subjectClass); expandedLiterals != nil {
					remaining := []Type{}
					for _, enumType := range expandedLiterals {
						if !ClassTypeIsLiteralValueSame(valueClass, enumType) {
							remaining = append(remaining, enumType)
						}
					}
					return CombineTypes(remaining, nil)
				}
			}

			if IsLiteralType(subjectClass) && ClassTypeIsLiteralValueSame(valueClass, subjectClass) {
				return nil
			}
		}

		return subjectSubtypeExpanded
	}

	if IsNever(valueSubtypeExpanded) || IsNever(subjectSubtypeExpanded) {
		return NeverTypeCreateNever()
	}

	if IsAnyOrUnknown(valueSubtypeExpanded) || IsAnyOrUnknown(subjectSubtypeExpanded) {
		// The original's comment: if either type is "Unknown" (versus Any),
		// propagate the Unknown.
		if IsUnknown(valueSubtypeExpanded) || IsUnknown(subjectSubtypeExpanded) {
			return PreserveUnknown(valueSubtypeExpanded, subjectSubtypeExpanded)
		}
		return AnyTypeCreate(false)
	}

	// The original's comment: if both types are literals, we can compare the
	// literal values directly.
	if IsClassInstance(subjectSubtypeExpanded) && IsLiteralType(subjectSubtypeExpanded.(*ClassType)) &&
		IsClassInstance(valueSubtypeExpanded) && IsLiteralType(valueSubtypeExpanded.(*ClassType)) {
		if IsSameWithoutLiteralValue(subjectSubtypeExpanded, valueSubtypeExpanded) &&
			ClassTypeIsLiteralValueSame(valueSubtypeExpanded.(*ClassType),
				subjectSubtypeExpanded.(*ClassType)) {
			return valueSubtypeUnexpanded
		}
		return nil
	}

	// The original's comment: determine if assignment is supported for this
	// combination of value subtype and matching subtype. A value pattern compares
	// with `==` at runtime, so the question is whether `__eq__` accepts it.
	var returnType Type
	evaluator.UseSpeculativeMode(pattern.D.Expr, func() {
		if r := evaluator.GetTypeOfMagicMethodCall(
			valueSubtypeExpanded, "__eq__",
			[]*TypeResult{{Type: subjectSubtypeExpanded}},
			pattern.D.Expr, nil); r != nil {
			returnType = r.Type
		}
	}, nil)

	if IsNilType(returnType) {
		return nil
	}
	return valueSubtypeUnexpanded
}

// typeConditionPtrs adapts GetTypeCondition's []TypeCondition to the
// []*TypeCondition the options struct carries.
func typeConditionPtrs(conditions []TypeCondition) []*TypeCondition {
	if conditions == nil {
		return nil
	}
	result := make([]*TypeCondition, 0, len(conditions))
	for i := range conditions {
		result = append(result, &conditions[i])
	}
	return result
}
