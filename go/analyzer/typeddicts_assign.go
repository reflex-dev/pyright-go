/*
 * typeddicts_assign.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typedDicts.ts (pyright 1.1.412):
 * assignToTypedDict.
 *
 * Matches a dictionary *expression* -- a list of (key, value) type pairs
 * gathered from the source -- against a declared TypedDict. This is what makes
 * `x: Movie = {"name": "Blade Runner", "year": 1982}` check the literal against
 * the declaration rather than inferring `dict[str, str | int]`.
 *
 * Two details that are load-bearing:
 *
 * - Keys must be *string literals*. A non-literal key is not an error reported
 *   here; it simply makes the whole match fail, and the caller falls back to
 *   ordinary dict inference. That is why isMatch is set without a diagnostic.
 *
 * - The tdEntries map is mutated as keys are matched (`symbolEntry.isProvided =
 *   true`) and then re-walked to find required keys that were never provided.
 *   getTypedDictMembersForClass hands back entries that may be shared through
 *   the class's cache, so the mutation is visible beyond this function; that is
 *   how the original behaves and the port keeps it.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// AssignToTypedDict corresponds to assignToTypedDict. It returns nil where the
// original returns undefined, meaning the dictionary expression does not match
// the TypedDict.
func AssignToTypedDict(
	evaluator TypeEvaluator,
	classType *ClassType,
	keyTypes []*TypeResultWithNode,
	valueTypes []*TypeResultWithNode,
	diagAddendum *common.DiagnosticAddendum,
) *ClassType {
	common.Assert(IsClassInstance(classType), "Expected a class instance")
	common.Assert(ClassTypeIsTypedDictClass(classType), "Expected a TypedDict class")
	common.Assert(len(keyTypes) == len(valueTypes), "Expected the same number of keys and values")

	isMatch := true
	narrowedEntries := common.NewOrderedMap[string, *TypedDictEntry]()

	var constraints *ConstraintTracker
	genericClassType := classType

	if len(classType.Shared.TypeParams) > 0 {
		constraints = NewConstraintTracker()

		// The original's comment: create a generic (nonspecialized version) of the
		// class.
		if classType.Priv.TypeArgs != nil {
			genericClassType = ClassTypeSpecialize(classType, nil, nil, false, nil, nil)
		}
	}

	tdEntries := GetTypedDictMembersForClass(evaluator, genericClassType, false)

	for index, keyTypeResult := range keyTypes {
		keyType := keyTypeResult.Type
		if !IsClassInstance(keyType) || !ClassTypeIsBuiltInNamed(keyType.(*ClassType), "str") ||
			!IsLiteralType(keyType.(*ClassType)) {
			isMatch = false
			continue
		}

		keyClass := keyType.(*ClassType)
		keyValue := string(keyClass.Priv.LiteralValue.(LiteralString))
		symbolEntry, found := tdEntries.KnownItems.Get(keyValue)

		if !found || symbolEntry == nil {
			if tdEntries.ExtraItems != nil {
				var subDiag *common.DiagnosticAddendum
				if diagAddendum != nil {
					subDiag = diagAddendum.CreateAddendum()
				}
				var nested *common.DiagnosticAddendum
				if subDiag != nil {
					nested = subDiag.CreateAddendum()
				}

				if !evaluator.AssignType(
					tdEntries.ExtraItems.ValueType,
					valueTypes[index].Type,
					nested,
					constraints,
					AssignTypeFlagsRetainLiteralsForTypeVar,
					0,
				) {
					if subDiag != nil {
						subDiag.AddMessage(
							localization.LocAddendum.TypedDictFieldTypeMismatch().Format(
								"extra_items", evaluator.PrintType(valueTypes[index].Type, nil)))

						subDiag.AddTextRange(keyTypeResult.Node.NodeBase().TextRange)
					}
					isMatch = false
				}
			} else {
				// The original's comment: the provided key name doesn't exist.
				isMatch = false
				if diagAddendum != nil {
					subDiag := diagAddendum.CreateAddendum()
					subDiag.AddMessage(
						localization.LocAddendum.TypedDictFieldUndefined().Format(
							keyValue, evaluator.PrintType(ClassTypeCloneAsInstance(classType, true), nil)))

					subDiag.AddTextRange(keyTypeResult.Node.NodeBase().TextRange)
				}
			}
			continue
		}

		// The original's comment: can we assign the value to the declared type?
		var subDiag *common.DiagnosticAddendum
		if diagAddendum != nil {
			subDiag = diagAddendum.CreateAddendum()
		}
		var nested *common.DiagnosticAddendum
		if subDiag != nil {
			nested = subDiag.CreateAddendum()
		}

		if !evaluator.AssignType(
			symbolEntry.ValueType,
			valueTypes[index].Type,
			nested,
			constraints,
			AssignTypeFlagsRetainLiteralsForTypeVar,
			0,
		) {
			if subDiag != nil {
				subDiag.AddMessage(
					localization.LocAddendum.TypedDictFieldTypeMismatch().Format(
						keyValue, evaluator.PrintType(valueTypes[index].Type, nil)))

				subDiag.AddTextRange(keyTypeResult.Node.NodeBase().TextRange)
			}
			isMatch = false
		}

		if !symbolEntry.IsRequired {
			narrowedEntries.Set(keyValue, &TypedDictEntry{
				ValueType:  valueTypes[index].Type,
				IsReadOnly: valueTypes[index].IsReadOnly,
				IsRequired: false,
				IsProvided: true,
			})
		}

		symbolEntry.IsProvided = true
	}

	if !isMatch {
		return nil
	}

	// The original's comment: see if any required keys are missing.
	for _, name := range tdEntries.KnownItems.Keys() {
		entry, _ := tdEntries.KnownItems.Get(name)
		if entry.IsRequired && !entry.IsProvided {
			if diagAddendum != nil {
				diagAddendum.AddMessage(
					localization.LocAddendum.TypedDictFieldRequired().Format(
						name, evaluator.PrintType(classType, nil)))
			}
			isMatch = false
		}
	}

	if !isMatch {
		return nil
	}

	specializedClassType := classType
	if constraints != nil {
		if solved := evaluator.SolveAndApplyConstraints(genericClassType, constraints, nil, nil); IsClass(solved) {
			specializedClassType = solved.(*ClassType)
		}
	}

	if narrowedEntries.Size() == 0 {
		return specializedClassType
	}
	return ClassTypeCloneForNarrowedTypedDictEntries(specializedClassType, narrowedEntries)
}
