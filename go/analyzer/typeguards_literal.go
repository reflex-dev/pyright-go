/*
 * typeguards_literal.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeGuards.ts (pyright 1.1.412):
 * narrowTypeForTupleLength, expandUnboundedTupleElement,
 * narrowTypeForContainerType, getElementTypeForContainerNarrowing,
 * narrowTypeForContainerElementType, narrowTypeForTypedDictKey,
 * narrowTypeForDiscriminatedDictEntryComparison,
 * narrowTypeForDiscriminatedTupleComparison,
 * narrowTypeForDiscriminatedLiteralFieldComparison,
 * narrowTypeForDiscriminatedFieldNoneComparison, narrowTypeForTypeIs,
 * narrowTypeForClassComparison, isFilterSuperclass,
 * narrowTypeForLiteralComparison.
 *
 * The discriminated-union family. Everything here answers the same question in a
 * different syntactic dress: given that some observable property of `x` compared
 * equal (or unequal) to a literal, which members of the union that `x` could be
 * are still possible.
 *
 * A recurring idiom: several of these track a `canNarrow` flag that any subtype
 * can clear, and on the way out return the *original* type rather than the
 * narrowed one if it was cleared. That is all-or-nothing narrowing -- if one
 * member of the union is not discriminable on this property, the union is not
 * narrowed at all. Preserving that is the difference between correct behavior
 * and silently dropping union members.
 *
 * enumerateLiteralsForType lives in typeevaluator_enumexpand.go as
 * EnumerateLiteralsForType; several callers outside typeGuards.ts already
 * needed it, so it was ported ahead of this file.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
)

// narrowTypeForTupleLength corresponds to the function of the same name.
//
// The original's comment: attempts to narrow a union of tuples based on their
// known length.
func narrowTypeForTupleLength(
	evaluator TypeEvaluator,
	referenceType Type,
	lengthValue int,
	isPositiveTest bool,
	isLessThanCheck bool,
) Type {
	return MapSubtypes(referenceType, func(subtype Type) Type {
		concreteSubtype := evaluator.MakeTopLevelTypeVarsConcrete(subtype, false)

		// The original's comment: if it's not a tuple, we can't narrow it.
		if !IsClassInstance(concreteSubtype) ||
			!IsTupleClass(concreteSubtype.(*ClassType)) ||
			concreteSubtype.(*ClassType).Priv.TupleTypeArgs == nil {
			return subtype
		}

		concreteClass := concreteSubtype.(*ClassType)

		// The original's comment: if the tuple contains a TypeVarTuple, we can't narrow
		// it.
		for _, typeArg := range concreteClass.Priv.TupleTypeArgs {
			if IsUnpackedTypeVarTuple(typeArg.Type) {
				return subtype
			}
		}

		// The original's comment: if the tuple contains no unbounded elements, then we
		// know its length exactly.
		hasUnbounded := false
		for _, typeArg := range concreteClass.Priv.TupleTypeArgs {
			if typeArg.IsUnbounded {
				hasUnbounded = true
				break
			}
		}

		if !hasUnbounded {
			var tupleLengthMatches bool
			if isLessThanCheck {
				tupleLengthMatches = len(concreteClass.Priv.TupleTypeArgs) < lengthValue
			} else {
				tupleLengthMatches = len(concreteClass.Priv.TupleTypeArgs) == lengthValue
			}

			if tupleLengthMatches == isPositiveTest {
				return subtype
			}
			return nil
		}

		// The original's comment: the tuple contains a "...". We'll expand this into as
		// many elements as necessary to match the lengthValue.
		elementsToAdd := lengthValue - len(concreteClass.Priv.TupleTypeArgs) + 1

		if !isLessThanCheck {
			// The original's comment: if the specified length is smaller than the minimum
			// length of this tuple, we can rule it out for a positive test and rule it in
			// for a negative test.
			if elementsToAdd < 0 {
				if isPositiveTest {
					return nil
				}
				return subtype
			}

			if !isPositiveTest {
				// The original's comment: if this is an equality check for the minimum
				// length (e.g. "len(x) == 0"), we can expand the minimum length by one).
				minLen := len(concreteClass.Priv.TupleTypeArgs) - 1
				if lengthValue == minLen {
					return expandUnboundedTupleElement(concreteClass, 1, true)
				}
				return subtype
			}

			return expandUnboundedTupleElement(concreteClass, elementsToAdd, false)
		}

		// The original's comment: if this is a tuple related to an "*args: P.args"
		// parameter, don't expand it.
		if IsParamSpec(subtype) && subtype.(*TypeVarType).Priv.ParamSpecAccess != ParamSpecAccessNone {
			return subtype
		}

		// The original's comment: place an upper limit on the number of union subtypes
		// we will expand the tuple to.
		const maxTupleUnionExpansion = 32
		if elementsToAdd > maxTupleUnionExpansion {
			return subtype
		}

		if isPositiveTest {
			if elementsToAdd < 1 {
				return nil
			}

			typesToCombine := []Type{}
			for i := 0; i < elementsToAdd; i++ {
				typesToCombine = append(typesToCombine,
					expandUnboundedTupleElement(concreteClass, i, false))
			}

			return CombineTypes(typesToCombine, nil)
		}

		return expandUnboundedTupleElement(concreteClass, elementsToAdd, true)
	}, nil)
}

// expandUnboundedTupleElement corresponds to the function of the same name.
//
// The original's comment: expands a tuple type that contains an unbounded
// element to include multiple bounded elements of that same type in place of (or
// in addition to) the unbounded element.
func expandUnboundedTupleElement(tupleType *ClassType, elementsToAdd int, keepUnbounded bool) Type {
	tupleTypeArgs := []*TupleTypeArg{}

	for _, typeArg := range tupleType.Priv.TupleTypeArgs {
		if !typeArg.IsUnbounded {
			tupleTypeArgs = append(tupleTypeArgs, typeArg)
		} else {
			for i := 0; i < elementsToAdd; i++ {
				tupleTypeArgs = append(tupleTypeArgs, &TupleTypeArg{IsUnbounded: false, Type: typeArg.Type})
			}

			if keepUnbounded {
				tupleTypeArgs = append(tupleTypeArgs, typeArg)
			}
		}
	}

	return SpecializeTupleClass(tupleType, tupleTypeArgs, true, false)
}

// narrowTypeForContainerType corresponds to the function of the same name.
//
// The original's comment: attempts to narrow a type (make it more constrained)
// based on an "in" binary operator.
func narrowTypeForContainerType(
	evaluator TypeEvaluator,
	referenceType Type,
	containerType Type,
	isPositiveTest bool,
) Type {
	if isPositiveTest {
		elementType := GetElementTypeForContainerNarrowing(containerType)
		if elementType == nil {
			return referenceType
		}

		return NarrowTypeForContainerElementType(
			evaluator, referenceType, evaluator.MakeTopLevelTypeVarsConcrete(elementType, false))
	}

	// The original's comment: narrowing in the negative case is possible only with
	// tuples with a known length.
	if !IsClassInstance(containerType) ||
		!ClassTypeIsBuiltInNamed(containerType.(*ClassType), "tuple") ||
		containerType.(*ClassType).Priv.TupleTypeArgs == nil {
		return referenceType
	}

	// The original's comment: determine which tuple types can be eliminated. Only
	// "None" and literal types can be handled here.
	typesToEliminate := []Type{}
	for _, tupleEntry := range containerType.(*ClassType).Priv.TupleTypeArgs {
		if !tupleEntry.IsUnbounded {
			if IsNoneInstance(tupleEntry.Type) {
				typesToEliminate = append(typesToEliminate, tupleEntry.Type)
			} else if IsClassInstance(tupleEntry.Type) && IsLiteralType(tupleEntry.Type.(*ClassType)) {
				typesToEliminate = append(typesToEliminate, tupleEntry.Type)
			}
		}
	}

	if len(typesToEliminate) == 0 {
		return referenceType
	}

	return MapSubtypes(referenceType, func(referenceSubtype Type) Type {
		referenceSubtype = evaluator.MakeTopLevelTypeVarsConcrete(referenceSubtype, false)
		if IsClassInstance(referenceSubtype) && referenceSubtype.(*ClassType).Priv.LiteralValue == nil {
			// The original's comment: if we're able to enumerate all possible literal
			// values (for bool or enum), we can eliminate all others in a negative test.
			allLiteralTypes := EnumerateLiteralsForType(evaluator, referenceSubtype.(*ClassType))
			if len(allLiteralTypes) > 0 {
				retained := []Type{}
				for _, literalType := range allLiteralTypes {
					eliminated := false
					for _, t := range typesToEliminate {
						if IsTypeSame(t, literalType, TypeSameOptions{}, 0) {
							eliminated = true
							break
						}
					}
					if !eliminated {
						retained = append(retained, literalType)
					}
				}
				return CombineTypes(retained, nil)
			}
		}

		for _, t := range typesToEliminate {
			if IsTypeSame(t, referenceSubtype, TypeSameOptions{}, 0) {
				return nil
			}
		}

		return referenceSubtype
	}, nil)
}

// GetElementTypeForContainerNarrowing corresponds to
// getElementTypeForContainerNarrowing.
func GetElementTypeForContainerNarrowing(containerType Type) Type {
	// The original's comment: we support contains narrowing only for certain
	// built-in types that have been specialized.
	supportedContainers := []string{
		"list", "set", "frozenset", "deque", "tuple", "dict", "defaultdict", "OrderedDict",
	}
	if !IsClassInstance(containerType) ||
		!ClassTypeIsBuiltInNamed(containerType.(*ClassType), supportedContainers...) {
		return nil
	}

	containerClass := containerType.(*ClassType)
	if len(containerClass.Priv.TypeArgs) < 1 {
		return nil
	}

	elementType := containerClass.Priv.TypeArgs[0]
	if IsTupleClass(containerClass) && containerClass.Priv.TupleTypeArgs != nil {
		entryTypes := make([]Type, 0, len(containerClass.Priv.TupleTypeArgs))
		for _, t := range containerClass.Priv.TupleTypeArgs {
			entryTypes = append(entryTypes, t.Type)
		}
		elementType = CombineTypes(entryTypes, nil)
	}

	return elementType
}

// NarrowTypeForContainerElementType corresponds to
// narrowTypeForContainerElementType.
func NarrowTypeForContainerElementType(evaluator TypeEvaluator, referenceType Type, elementType Type) Type {
	return evaluator.MapSubtypesExpandTypeVars(referenceType, nil, func(referenceSubtype Type, _ Type) Type {
		return MapSubtypes(elementType, func(elementSubtype Type) Type {
			if IsAnyOrUnknown(elementSubtype) {
				return referenceSubtype
			}

			// The original's comment: if the two types are disjoint (i.e. are not
			// comparable), eliminate this subtype.
			if !evaluator.IsTypeComparable(elementSubtype, referenceSubtype, false) {
				return nil
			}

			// The original's comment: if one of the two types is a literal, we can narrow
			// to that type.
			if IsClassInstance(elementSubtype) &&
				(IsLiteralLikeType(elementSubtype.(*ClassType)) || IsNoneInstance(elementSubtype)) &&
				evaluator.AssignType(referenceSubtype, elementSubtype, nil, nil, AssignTypeFlagsDefault, 0) {
				return StripTypeForm(
					AddConditionToType(elementSubtype, propsCondition(referenceSubtype), nil))
			}

			if IsClassInstance(referenceSubtype) &&
				(IsLiteralLikeType(referenceSubtype.(*ClassType)) || IsNoneInstance(referenceSubtype)) &&
				evaluator.AssignType(elementSubtype, referenceSubtype, nil, nil, AssignTypeFlagsDefault, 0) {
				return StripTypeForm(
					AddConditionToType(referenceSubtype, propsCondition(elementSubtype), nil))
			}

			// The original's comment: if the element type is a known class object that is
			// assignable to the reference type, we can narrow to that class object.
			if IsInstantiableClass(elementSubtype) &&
				!elementSubtype.(*ClassType).Priv.IncludeSubclasses &&
				evaluator.AssignType(referenceSubtype, elementSubtype, nil, nil, AssignTypeFlagsDefault, 0) {
				return StripTypeForm(
					AddConditionToType(elementSubtype, propsCondition(referenceSubtype), nil))
			}

			// The original's comment: it's not safe to narrow.
			return referenceSubtype
		}, nil)
	})
}

// narrowTypeForTypedDictKey corresponds to the function of the same name.
//
// The original's comment: attempts to narrow a type based on whether it is a
// TypedDict with a literal key value.
func narrowTypeForTypedDictKey(
	evaluator TypeEvaluator,
	referenceType Type,
	literalKey *ClassType,
	isPositiveTest bool,
) Type {
	keyName := ""
	if s, ok := literalKey.Priv.LiteralValue.(LiteralString); ok {
		keyName = string(s)
	}

	return evaluator.MapSubtypesExpandTypeVars(referenceType, nil,
		func(subtype Type, unexpandedSubtype Type) Type {
			if IsParamSpec(unexpandedSubtype) {
				return unexpandedSubtype
			}

			if IsClassInstance(subtype) && ClassTypeIsTypedDictClass(subtype.(*ClassType)) {
				subtypeClass := subtype.(*ClassType)
				entries := GetTypedDictMembersForClass(evaluator, subtypeClass, true)
				tdEntry, found := entries.KnownItems.Get(keyName)
				if !found {
					tdEntry = entries.ExtraItems
				}

				if isPositiveTest {
					// The original's comment: the code that is commented out below implements
					// the behavior that is technically correct, but until we PEP 728 is ratified
					// and we have a way to express "extra items" and closed TypedDicts, we'll
					// preserve the older (less correct) behavior to enable narrowing of
					// TypedDicts based on checks for specific keys.
					// TODO - remove this behavior once PEP 728 is accepted and the feature is
					// no longer experimental.
					if tdEntry == nil {
						return nil
					}

					if IsNever(tdEntry.ValueType) {
						// The original's comment: if the entry is typed as Never or the "extra
						// items" is typed as Never, then this key cannot be present in the
						// TypedDict, and we can eliminate it. A closed TypedDict has an "extra
						// items" of Never, so this is what allows a key check to discriminate
						// between closed TypedDicts.
						return nil
					}

					// The original's comment: if the entry is currently not required and not
					// marked provided, we can mark it as provided after this guard expression
					// confirms it is.
					if tdEntry.IsRequired || tdEntry.IsProvided {
						return subtype
					}

					newNarrowedEntriesMap := common.NewOrderedMap[string, *TypedDictEntry]()
					if subtypeClass.Priv.TypedDictNarrowedEntries != nil {
						for _, key := range subtypeClass.Priv.TypedDictNarrowedEntries.Keys() {
							value, _ := subtypeClass.Priv.TypedDictNarrowedEntries.Get(key)
							newNarrowedEntriesMap.Set(key, value)
						}
					}

					// The original's comment: add the new entry.
					newNarrowedEntriesMap.Set(keyName, &TypedDictEntry{
						ValueType:  tdEntry.ValueType,
						IsReadOnly: tdEntry.IsReadOnly,
						IsRequired: false,
						IsProvided: true,
					})

					// The original's comment: clone the TypedDict object with the new entries.
					return ClassTypeCloneAsInstance(
						ClassTypeCloneForNarrowedTypedDictEntries(
							ClassTypeCloneAsInstantiable(subtypeClass, true), newNarrowedEntriesMap),
						true)
				}

				if tdEntry != nil && (tdEntry.IsRequired || tdEntry.IsProvided) {
					return nil
				}
				return subtype
			}

			return subtype
		})
}

// NarrowTypeForDiscriminatedDictEntryComparison corresponds to
// narrowTypeForDiscriminatedDictEntryComparison.
//
// The original's comment: attempts to narrow a TypedDict type based on a
// comparison (equal or not equal) between a discriminating entry type that has a
// declared literal type to a literal value.
func narrowTypeForDiscriminatedDictEntryComparison(
	evaluator TypeEvaluator,
	referenceType Type,
	indexLiteralType *ClassType,
	literalType Type,
	isPositiveTest bool,
) Type {
	canNarrow := true

	keyName := ""
	if s, ok := indexLiteralType.Priv.LiteralValue.(LiteralString); ok {
		keyName = string(s)
	}

	narrowedType := MapSubtypes(referenceType, func(subtype Type) Type {
		if IsClassInstance(subtype) && ClassTypeIsTypedDictClass(subtype.(*ClassType)) {
			symbolMap := GetTypedDictMembersForClass(evaluator, subtype.(*ClassType), false)
			tdEntry, found := symbolMap.KnownItems.Get(keyName)

			if found && tdEntry != nil && IsLiteralTypeOrUnion(tdEntry.ValueType, false) {
				if isPositiveTest {
					foundMatch := false

					DoForEachSubtype(literalType, func(literalSubtype Type, _ int, _ []Type) {
						if evaluator.AssignType(
							tdEntry.ValueType, literalSubtype, nil, nil, AssignTypeFlagsDefault, 0) {
							foundMatch = true
						}
					})

					if foundMatch {
						return subtype
					}
					return nil
				}

				foundNonMatch := false

				DoForEachSubtype(literalType, func(literalSubtype Type, _ int, _ []Type) {
					if !evaluator.AssignType(
						literalSubtype, tdEntry.ValueType, nil, nil, AssignTypeFlagsDefault, 0) {
						foundNonMatch = true
					}
				})

				if foundNonMatch {
					return subtype
				}
				return nil
			}
		}

		canNarrow = false
		return subtype
	}, nil)

	if canNarrow {
		return narrowedType
	}
	return referenceType
}

// narrowTypeForDiscriminatedTupleComparison corresponds to the function of the
// same name.
func narrowTypeForDiscriminatedTupleComparison(
	evaluator TypeEvaluator,
	referenceType Type,
	indexLiteralType *ClassType,
	literalType Type,
	isPositiveTest bool,
) Type {
	canNarrow := true

	// `typeof indexLiteralType.priv.literalValue === 'number'` -- the non-bigint arm.
	indexLiteral, indexIsNumber := indexLiteralType.Priv.LiteralValue.(LiteralFloat)

	narrowedType := MapSubtypes(referenceType, func(subtype Type) Type {
		if IsClassInstance(subtype) &&
			ClassTypeIsTupleClass(subtype.(*ClassType)) &&
			!IsUnboundedTupleClass(subtype.(*ClassType)) &&
			indexIsNumber &&
			IsClassInstance(literalType) {
			subtypeClass := subtype.(*ClassType)
			indexValue := int(indexLiteral)
			if subtypeClass.Priv.TupleTypeArgs != nil &&
				indexValue >= 0 && indexValue < len(subtypeClass.Priv.TupleTypeArgs) {
				tupleEntryType := subtypeClass.Priv.TupleTypeArgs[indexValue].Type
				if tupleEntryType != nil && IsLiteralTypeOrUnion(tupleEntryType, false) {
					if isPositiveTest {
						if evaluator.AssignType(
							tupleEntryType, literalType, nil, nil, AssignTypeFlagsDefault, 0) {
							return subtype
						}
						return nil
					}
					if evaluator.AssignType(
						literalType, tupleEntryType, nil, nil, AssignTypeFlagsDefault, 0) {
						return nil
					}
					return subtype
				}
			}
		}

		canNarrow = false
		return subtype
	}, nil)

	if canNarrow {
		return narrowedType
	}
	return referenceType
}

// narrowTypeForDiscriminatedLiteralFieldComparison corresponds to the function of
// the same name.
//
// The original's comment: attempts to narrow a type based on a comparison (equal
// or not equal) between a discriminating field that has a declared literal type
// to a literal value.
func narrowTypeForDiscriminatedLiteralFieldComparison(
	evaluator TypeEvaluator,
	referenceType Type,
	memberName string,
	literalType *ClassType,
	isPositiveTest bool,
) Type {
	return MapSubtypes(referenceType, func(subtype Type) Type {
		var memberInfo *ClassMember

		if IsClassInstance(subtype) {
			memberInfo = LookUpObjectMember(subtype.(*ClassType), memberName, MemberAccessFlagsDefault, nil)
		} else if IsInstantiableClass(subtype) {
			memberInfo = LookUpClassMember(subtype.(*ClassType), memberName, MemberAccessFlagsDefault, nil)
		}

		if memberInfo != nil && memberInfo.IsTypeDeclared {
			memberType := evaluator.GetTypeOfMember(memberInfo)

			// The original's comment: handle the case where the field is a property that
			// has a declared literal return type for its getter.
			if IsClassInstance(subtype) && IsClassInstance(memberType) && IsProperty(memberType) {
				fgetInfo := memberType.(*ClassType).Priv.FgetInfo
				if fgetInfo != nil && fgetInfo.MethodType != nil {
					getterType := fgetInfo.MethodType
					if IsFunction(getterType) && getterType.(*FunctionType).Shared.DeclaredReturnType != nil {
						getterReturnType := FunctionTypeGetEffectiveReturnType(
							getterType.(*FunctionType), true)
						if getterReturnType != nil {
							memberType = getterReturnType
						}
					}
				}
			}

			if IsLiteralTypeOrUnion(memberType, true) {
				if isPositiveTest {
					if evaluator.AssignType(memberType, literalType, nil, nil, AssignTypeFlagsDefault, 0) {
						return subtype
					}
					return nil
				}
				if evaluator.AssignType(literalType, memberType, nil, nil, AssignTypeFlagsDefault, 0) {
					return nil
				}
				return subtype
			}
		}

		return subtype
	}, nil)
}

// narrowTypeForDiscriminatedFieldNoneComparison corresponds to the function of
// the same name.
//
// The original's comment: attempts to narrow a type based on a comparison (equal
// or not equal) between a discriminating field that has a declared None type to
// a None.
func narrowTypeForDiscriminatedFieldNoneComparison(
	evaluator TypeEvaluator,
	referenceType Type,
	memberName string,
	isPositiveTest bool,
) Type {
	return MapSubtypes(referenceType, func(subtype Type) Type {
		var memberInfo *ClassMember
		if IsClassInstance(subtype) {
			memberInfo = LookUpObjectMember(subtype.(*ClassType), memberName, MemberAccessFlagsDefault, nil)
		} else if IsInstantiableClass(subtype) {
			memberInfo = LookUpClassMember(subtype.(*ClassType), memberName, MemberAccessFlagsDefault, nil)
		}

		if memberInfo != nil && memberInfo.IsTypeDeclared {
			// The original's comment: check the declared type before narrowing, since the
			// member type below will be concretized and lose descriptor identity.
			declaredInfo := evaluator.GetDeclaredTypeOfSymbol(memberInfo.Symbol)
			if declaredInfo == nil || declaredInfo.Type == nil {
				// The original's comment: isTypeDeclared is true but the type couldn't be
				// resolved (e.g. an unresolvable stub). Conservatively skip narrowing rather
				// than risk incorrectly eliminating a descriptor-typed member.
				return subtype
			}
			declaredType := declaredInfo.Type

			// The original's comment: check if any subtype of the declared type is a
			// descriptor or property. isMaybeDescriptorInstance handles declared types in
			// instance form (ClassInstance), while isMaybeDescriptorClass handles declared
			// types in instantiable form (InstantiableClass), which occurs when the
			// annotation refers to the class object itself. This check applies to both
			// positive and negative test paths: descriptor __get__ return values don't
			// reflect stored values regardless of test polarity.
			isDescriptorOrProperty := SomeSubtypes(declaredType, func(declaredSubtype Type) bool {
				return IsProperty(declaredSubtype) ||
					IsMaybeDescriptorInstance(declaredSubtype, false) ||
					IsMaybeDescriptorClass(declaredSubtype)
			})

			if isDescriptorOrProperty {
				return subtype
			}

			memberType := evaluator.MakeTopLevelTypeVarsConcrete(
				evaluator.GetTypeOfMember(memberInfo), false)
			canNarrow := true

			if isPositiveTest {
				DoForEachSubtype(memberType, func(memberSubtype Type, _ int, _ []Type) {
					memberSubtype = evaluator.MakeTopLevelTypeVarsConcrete(memberSubtype, false)

					if IsAnyOrUnknown(memberSubtype) || IsNoneInstance(memberSubtype) ||
						IsNever(memberSubtype) {
						canNarrow = false
					}
				})
			} else {
				canNarrow = IsNoneInstance(memberType)
			}

			if canNarrow {
				return nil
			}
		}

		return subtype
	}, nil)
}

// narrowTypeForTypeIs corresponds to the function of the same name.
//
// The original's comment: attempts to narrow a type based on a "type(x) is y" or
// "type(x) is not y" check.
func narrowTypeForTypeIs(
	evaluator TypeEvaluator, t Type, classTypes []*ClassType, isPositiveTest bool,
) Type {
	// The original's comment: we currently don't support narrowing in the negative
	// direction when there are more than one class types.
	if !isPositiveTest && len(classTypes) > 1 {
		return t
	}

	typesToCombine := make([]Type, 0, len(classTypes))
	for _, classType := range classTypes {
		typesToCombine = append(typesToCombine,
			evaluator.MapSubtypesExpandTypeVars(t, nil, func(subtype Type, unexpandedSubtype Type) Type {
				if IsClassInstance(subtype) {
					subtypeClass := subtype.(*ClassType)
					matches := ClassTypeIsDerivedFrom(
						classType, ClassTypeCloneAsInstantiable(subtypeClass, true), nil)
					if isPositiveTest {
						if matches {
							if ClassTypeIsSameGenericClass(
								ClassTypeCloneAsInstantiable(subtypeClass, true), classType, 0) {
								return AddConditionToType(subtype, GetTypeCondition(classType), nil)
							}

							return AddConditionToType(
								ClassTypeCloneAsInstance(classType, true), propsCondition(subtype), nil)
						}

						if !classType.Priv.IncludeSubclasses {
							return nil
						}

						if !IsTypeVar(unexpandedSubtype) ||
							!TypeVarTypeIsSelf(unexpandedSubtype.(*TypeVarType)) {
							return AddConditionToType(subtype, propsCondition(classType), nil)
						}
					}

					if !classType.Priv.IncludeSubclasses {
						// The original's comment: if the class if marked final and it matches,
						// then we can eliminate it in the negative case.
						if matches && ClassTypeIsFinal(subtypeClass) {
							return nil
						}

						// The original's comment: we can't eliminate the subtype in the negative
						// case because it could be a subclass of the type, in which case
						// `type(x) is y` would fail.
						return subtype
					}
				}

				if IsAnyOrUnknown(subtype) {
					if isPositiveTest {
						conditioned := AddConditionToType(classType, GetTypeCondition(subtype), nil)
						return ClassTypeCloneAsInstance(conditioned.(*ClassType), true)
					}
					return subtype
				}

				return unexpandedSubtype
			}))
	}

	return CombineTypes(typesToCombine, nil)
}

// narrowTypeForClassComparison corresponds to the function of the same name.
//
// The original's comment: attempts to narrow a type based on a comparison with a
// class using "is" or "is not". This pattern is sometimes used for sentinels.
func narrowTypeForClassComparison(
	evaluator TypeEvaluator,
	referenceType Type,
	classType *ClassType,
	isPositiveTest bool,
) Type {
	return MapSubtypes(referenceType, func(subtype Type) Type {
		concreteSubtype := evaluator.MakeTopLevelTypeVarsConcrete(subtype, false)

		if isPositiveTest {
			if IsClassInstance(concreteSubtype) && subtype.Base().IsInstance() &&
				ClassTypeIsBuiltInNamed(concreteSubtype.(*ClassType), "type") {
				concreteClass := concreteSubtype.(*ClassType)
				if len(concreteClass.Priv.TypeArgs) > 0 {
					concreteSubtype = ConvertToInstantiable(concreteClass.Priv.TypeArgs[0], true)
				} else {
					concreteSubtype = UnknownTypeCreate(false)
				}
			}

			if IsAnyOrUnknown(concreteSubtype) {
				return AddConditionToType(classType, GetTypeCondition(concreteSubtype), nil)
			}

			if IsClass(concreteSubtype) {
				concreteClass := concreteSubtype.(*ClassType)
				if concreteSubtype.Base().IsInstance() {
					if ClassTypeIsBuiltInNamed(concreteClass, "object") {
						return classType
					}
					return nil
				}

				isSuperType := isFilterSuperclass(subtype, concreteClass, classType, classType)

				if !classType.Priv.IncludeSubclasses {
					// The original's comment: handle the case where the LHS and RHS operands
					// are specific classes, as opposed to types that represent classes and
					// their subclasses.
					if !concreteClass.Priv.IncludeSubclasses {
						if ClassTypeIsSameGenericClass(concreteClass, classType, 0) {
							return classType
						}
						return nil
					}

					if isSuperType {
						return AddConditionToType(classType, GetTypeCondition(concreteSubtype), nil)
					}

					isSubType := ClassTypeIsDerivedFrom(classType, concreteClass, nil)
					if isSubType {
						return AddConditionToType(classType, GetTypeCondition(concreteSubtype), nil)
					}

					return nil
				}

				if ClassTypeIsFinal(concreteClass) && !isSuperType {
					return nil
				}
			}
		} else {
			if IsInstantiableClass(concreteSubtype) &&
				ClassTypeIsSameGenericClass(classType, concreteSubtype.(*ClassType), 0) &&
				ClassTypeIsFinal(classType) {
				return nil
			}
		}

		return subtype
	}, nil)
}

// isFilterSuperclass corresponds to the function of the same name.
func isFilterSuperclass(
	varType Type,
	concreteVarType *ClassType,
	filterType Type,
	concreteFilterType *ClassType,
) bool {
	if IsTypeVar(filterType) || concreteFilterType.Priv.LiteralValue != nil {
		return IsTypeSame(ConvertToInstance(filterType, true), varType, TypeSameOptions{}, 0)
	}

	// The original's comment: if the filter type represents all possible subclasses
	// of a type, we can't make any statements about its superclass relationship with
	// concreteVarType.
	if concreteFilterType.Priv.IncludeSubclasses {
		return false
	}

	if ClassTypeIsDerivedFrom(concreteVarType, concreteFilterType, nil) {
		return true
	}

	// The original's comment: handle the special case where the variable type is a
	// TypedDict and we're filtering against 'dict'. TypedDict isn't derived from
	// dict, but at runtime, isinstance returns True.
	if ClassTypeIsBuiltInNamed(concreteFilterType, "dict") && ClassTypeIsTypedDictClass(concreteVarType) {
		return true
	}

	return false
}

// narrowTypeForLiteralComparison corresponds to the function of the same name.
//
// The original's comment: attempts to narrow a type (make it more constrained)
// based on a comparison (equal or not equal) to a literal value. It also handles
// "is" or "is not" operators if isIsOperator is true.
func narrowTypeForLiteralComparison(
	evaluator TypeEvaluator,
	referenceType Type,
	literalType *ClassType,
	isPositiveTest bool,
	isIsOperator bool,
) Type {
	return evaluator.MapSubtypesExpandTypeVars(referenceType, nil, func(subtype Type, _ Type) Type {
		subtype = evaluator.MakeTopLevelTypeVarsConcrete(subtype, false)

		if IsAnyOrUnknown(subtype) {
			if isPositiveTest {
				return literalType
			}

			return subtype
		}

		if IsClassInstance(subtype) &&
			ClassTypeIsSameGenericClass(literalType, subtype.(*ClassType), 0) {
			subtypeClass := subtype.(*ClassType)
			if subtypeClass.Priv.LiteralValue != nil {
				literalValueMatches := ClassTypeIsLiteralValueSame(subtypeClass, literalType)
				if isPositiveTest {
					if literalValueMatches {
						return subtype
					}
					return nil
				}

				isSingleton := ClassTypeIsEnumClass(literalType) ||
					IsSentinelLiteral(subtype) ||
					ClassTypeIsBuiltInNamed(literalType, "bool")

				// The original's comment: for negative tests, we can eliminate the literal
				// value if it doesn't match, but only for equality tests or for 'is' tests
				// that involve enums, bools, or sentinels.
				if literalValueMatches && (isSingleton || !isIsOperator) {
					return nil
				}
				return subtype
			}

			if isPositiveTest {
				return literalType
			}

			// The original's comment: if we're able to enumerate all possible literal
			// values (for bool or enum), we can eliminate all others in a negative test.
			allLiteralTypes := EnumerateLiteralsForType(evaluator, subtypeClass)
			if len(allLiteralTypes) > 0 {
				retained := []Type{}
				for _, t := range allLiteralTypes {
					if !ClassTypeIsLiteralValueSame(t, literalType) {
						retained = append(retained, t)
					}
				}
				return CombineTypes(retained, nil)
			}

			return subtype
		}

		if isPositiveTest {
			if IsClassInstance(subtype) &&
				ClassTypeIsBuiltInNamed(subtype.(*ClassType), "LiteralString") {
				return literalType
			}

			if isIsOperator || IsNoneInstance(subtype) {
				compareType := getInnermostNewTypeBaseInstance(subtype)
				if compareType == nil {
					compareType = subtype
				}

				isSubtype := evaluator.AssignType(
					compareType, literalType, nil, nil, AssignTypeFlagsDefault, 0)
				if isSubtype {
					if IsClassInstance(subtype) && ClassTypeIsNewTypeClass(subtype.(*ClassType)) {
						return subtype
					}
					return literalType
				}
				return nil
			}
		}

		return subtype
	})
}
