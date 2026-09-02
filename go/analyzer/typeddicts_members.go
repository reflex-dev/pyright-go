/*
 * typeddicts_members.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typedDicts.ts (pyright 1.1.412):
 * getTypedDictMembersForClass, getTypedDictMembersForClassRecursive,
 * getEffectiveExtraItemsEntryType, isRequiredTypedDictVariable,
 * isNotRequiredTypedDictVariable, isReadOnlyTypedDictVariable.
 *
 * Collecting a TypedDict's keys, which live in the class body as annotated
 * variables and are inherited along the base-class chain.
 *
 * The walk is done ONCE per class and cached in shared state, because the
 * unsolved entries do not depend on how the class was specialized. Every call
 * then builds a fresh, specialized copy from that cache -- the original notes
 * that the caller may mutate what it gets back, so a shared map would be a bug.
 *
 * Base classes are gathered first so a derived class's redeclaration overwrites
 * theirs, which is what a plain `set` into the same map gives.
 *
 * Three modifiers ride on the annotation rather than on the symbol, so deciding
 * whether a key is required means re-evaluating the annotation expression and
 * reading isRequired / isNotRequired / isReadOnly off the result. Absent both
 * Required and NotRequired, the default comes from the class: `total=False`
 * makes everything optional.
 *
 * Two whole-class modifiers apply on top. A CLOSED TypedDict has extraItems of
 * Never, meaning no key outside the declared set is allowed. A PARTIAL one
 * (the `Partial[TD]` form) makes every key optional and every already-read-only
 * key Never -- read-only entries cannot be partially updated, so they are
 * removed rather than relaxed.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
)

// GetTypedDictMembersForClass corresponds to getTypedDictMembersForClass.
func GetTypedDictMembersForClass(
	evaluator TypeEvaluator, classType *ClassType, allowNarrowed bool,
) *TypedDictEntries {
	// The original's comment: were the entries already calculated and cached?
	if classType.Shared.TypedDictEntries == nil {
		entries := &TypedDictEntries{
			KnownItems: common.NewOrderedMap[string, *TypedDictEntry](),
		}
		getTypedDictMembersForClassRecursive(evaluator, classType, entries, 0)

		if ClassTypeIsTypedDictMarkedClosed(classType) && entries.ExtraItems == nil {
			entries.ExtraItems = &TypedDictEntry{ValueType: NeverTypeCreateNever()}
		}

		// The original's comment: cache the entries for next time.
		classType.Shared.TypedDictEntries = entries
	}

	solution := BuildSolutionFromSpecializedClass(classType)

	// The original's comment: create a specialized copy of the entries so the
	// caller can mutate them.
	entries := common.NewOrderedMap[string, *TypedDictEntry]()
	classType.Shared.TypedDictEntries.KnownItems.ForEach(func(value *TypedDictEntry, key string) {
		tdEntry := *value
		tdEntry.ValueType = ApplySolvedTypeVars(tdEntry.ValueType, solution, nil)

		// The original's comment: if the class is "Partial", make all entries
		// optional and convert all read-only entries to Never.
		if classType.Priv.IsTypedDictPartial {
			tdEntry.IsRequired = false

			if tdEntry.IsReadOnly {
				tdEntry.ValueType = NeverTypeCreateNever()
			} else {
				tdEntry.IsReadOnly = true
			}
		}

		entries.Set(key, &tdEntry)
	})

	// The original's comment: apply narrowed types on top of existing entries if
	// present.
	if allowNarrowed && classType.Priv.TypedDictNarrowedEntries != nil {
		classType.Priv.TypedDictNarrowedEntries.ForEach(func(value *TypedDictEntry, key string) {
			tdEntry := *value
			tdEntry.ValueType = ApplySolvedTypeVars(tdEntry.ValueType, solution, nil)
			entries.Set(key, &tdEntry)
		})
	}

	var extraItems *TypedDictEntry
	if classType.Shared.TypedDictEntries.ExtraItems != nil {
		copied := *classType.Shared.TypedDictEntries.ExtraItems
		copied.ValueType = ApplySolvedTypeVars(copied.ValueType, solution, nil)
		extraItems = &copied
	}

	return &TypedDictEntries{KnownItems: entries, ExtraItems: extraItems}
}

// getTypedDictMembersForClassRecursive corresponds to the function of the same
// name. It fills `entries` in place.
func getTypedDictMembersForClassRecursive(
	evaluator TypeEvaluator, classType *ClassType, entries *TypedDictEntries, recursionCount int,
) {
	assert(ClassTypeIsTypedDictClass(classType), "expected a TypedDict class")
	if recursionCount > MaxTypeRecursionCount {
		return
	}
	recursionCount++

	for _, baseClassType := range classType.Shared.BaseClasses {
		baseClass, ok := baseClassType.(*ClassType)
		if !ok || !IsInstantiableClass(baseClassType) || !ClassTypeIsTypedDictClass(baseClass) {
			continue
		}

		specializedBaseClassType := PartiallySpecializeType(
			baseClass, classType, evaluator.GetTypeClassType(), nil)
		specializedBaseClass, ok := specializedBaseClassType.(*ClassType)
		assert(ok && IsClass(specializedBaseClassType), "expected a class")

		// The original's comment: recursively gather keys from parent classes. Don't
		// report any errors in these cases because they will be reported within that
		// class.
		getTypedDictMembersForClassRecursive(evaluator, specializedBaseClass, entries, recursionCount)
	}

	solution := BuildSolutionFromSpecializedClass(classType)

	if ClassTypeIsTypedDictMarkedClosed(classType) {
		entries.ExtraItems = &TypedDictEntry{ValueType: NeverTypeCreateNever()}
	} else if classType.Shared.TypedDictExtraItemsExpr != nil {
		extraItemsTypeResult := evaluator.GetTypeOfExpressionExpectingType(
			classType.Shared.TypedDictExtraItemsExpr, &ExpectedTypeOptions{AllowReadOnly: true})

		entries.ExtraItems = &TypedDictEntry{
			ValueType:  ConvertToInstance(extraItemsTypeResult.Type, false),
			IsReadOnly: extraItemsTypeResult.IsReadOnly,
			IsProvided: true,
		}
	}

	// The original's comment: add any new typed dict entries from this class.
	ClassTypeGetSymbolTable(classType).ForEach(func(symbol *Symbol, name string) {
		if symbol.IsIgnoredForProtocolMatch() {
			return
		}

		// The original's comment: only variables (not functions, classes, etc.) are
		// considered.
		lastDecl := GetLastTypedDeclarationForSymbol(symbol)
		if lastDecl == nil {
			return
		}
		if _, isVar := lastDecl.(*VariableDeclaration); !isVar {
			return
		}

		valueType := evaluator.GetEffectiveTypeOfSymbol(symbol)
		valueType = ApplySolvedTypeVars(valueType, solution, nil)

		isRequired := !ClassTypeIsCanOmitDictValues(classType)
		isReadOnly := false

		if typedDictVariableHas(evaluator, symbol, annotationIsRequired) {
			isRequired = true
		} else if typedDictVariableHas(evaluator, symbol, annotationIsNotRequired) {
			isRequired = false
		}

		if typedDictVariableHas(evaluator, symbol, annotationIsReadOnly) {
			isReadOnly = true
		}

		entries.KnownItems.Set(name, &TypedDictEntry{
			ValueType:  valueType,
			IsReadOnly: isReadOnly,
			IsRequired: isRequired,
		})
	})
}

// GetEffectiveExtraItemsEntryType corresponds to
// getEffectiveExtraItemsEntryType.
func GetEffectiveExtraItemsEntryType(evaluator TypeEvaluator, classType *ClassType) *TypedDictEntry {
	assert(ClassTypeIsTypedDictClass(classType), "expected a TypedDict class")

	// The original's comment: missing entries in a non-closed TypedDict class are
	// implicitly typed as ReadOnly[NotRequired[object]].
	if !ClassTypeIsTypedDictMarkedClosed(classType) {
		return &TypedDictEntry{ValueType: evaluator.GetObjectType(), IsReadOnly: true}
	}

	if classType.Shared.TypedDictEntries != nil && classType.Shared.TypedDictEntries.ExtraItems != nil {
		return classType.Shared.TypedDictEntries.ExtraItems
	}

	return &TypedDictEntry{ValueType: NeverTypeCreateNever(), IsReadOnly: true}
}

/*
 * The three annotation predicates, which the original writes out three times
 * with the same body and a different field read at the end.
 */

func annotationIsRequired(result *TypeResult) bool    { return result.IsRequired }
func annotationIsNotRequired(result *TypeResult) bool { return result.IsNotRequired }
func annotationIsReadOnly(result *TypeResult) bool    { return result.IsReadOnly }

// typedDictVariableHas corresponds to isRequiredTypedDictVariable,
// isNotRequiredTypedDictVariable and isReadOnlyTypedDictVariable, which differ
// only in which flag they read off the evaluated annotation.
func typedDictVariableHas(
	evaluator TypeEvaluator, symbol *Symbol, pick func(*TypeResult) bool,
) bool {
	for _, decl := range symbol.GetDeclarations() {
		varDecl, ok := decl.(*VariableDeclaration)
		if !ok || varDecl.TypeAnnotationNode == nil {
			continue
		}

		annotatedType := evaluator.GetTypeOfExpressionExpectingType(varDecl.TypeAnnotationNode,
			&ExpectedTypeOptions{AllowFinal: true, AllowRequired: true, AllowReadOnly: true})

		if pick(annotatedType) {
			return true
		}
	}
	return false
}
