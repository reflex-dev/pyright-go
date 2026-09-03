/*
 * typeddicts_assigntd.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typedDicts.ts (pyright 1.1.412):
 * assignTypedDictToTypedDict.
 *
 * TypedDict-to-TypedDict assignability is structural and per-item, and the
 * variance of each item is decided by whether it is read-only rather than by any
 * property of the TypedDicts themselves. A writable item must match
 * *invariantly*, because the destination reference can be written through: if
 * `A.x: int` accepted a source whose `x` is `bool`, code holding the `A` view
 * could store an arbitrary int into a dict the source still believes holds only
 * bools. A read-only item has no such path and matches covariantly.
 *
 * The same asymmetry drives the required/not-required checks. A mismatch in
 * required-ness is an error only when the destination item is writable; for a
 * read-only destination item, a source that always supplies the key is
 * perfectly usable.
 *
 * A missing key is likewise not automatically an error. If the destination item
 * is both not-required and read-only, the source may simply never have it, and
 * the check falls back to comparing the destination's type against the source's
 * *extra items* type -- what an unknown key in the source would hold.
 *
 * The closed-destination pass is the reverse direction: a closed TypedDict
 * constrains what a source is allowed to have that the destination does not
 * name. Without `extraItems`, any additional source key is rejected outright;
 * with it, each is checked against the extra-items type using the same
 * read-only-decides-variance rule.
 *
 * The two `if (!typesAreConsistent && !diag)` short-circuits are deliberate and
 * are reproduced: when the caller passed no addendum it wants only the boolean,
 * so once the answer is known the remaining comparisons are skipped. When an
 * addendum *was* passed the loops run to completion, because the caller wants
 * every reason, not the first one.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// AssignTypedDictToTypedDict corresponds to assignTypedDictToTypedDict.
func AssignTypedDictToTypedDict(
	evaluator TypeEvaluator,
	destType *ClassType,
	srcType *ClassType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	typesAreConsistent := true
	destEntries := GetTypedDictMembersForClass(evaluator, destType, false)
	srcEntries := GetTypedDictMembersForClass(evaluator, srcType, true)
	extraSrcEntries := srcEntries.ExtraItems
	if extraSrcEntries == nil {
		extraSrcEntries = GetEffectiveExtraItemsEntryType(evaluator, srcType)
	}

	destEntries.KnownItems.ForEach(func(destEntry *TypedDictEntry, name string) {
		// The original's comment: if we've already determined that the types are
		// inconsistent and the caller isn't interested in detailed diagnostics,
		// skip the remainder.
		if !typesAreConsistent && diag == nil {
			return
		}

		srcEntry, hasSrcEntry := srcEntries.KnownItems.Get(name)
		if !hasSrcEntry || srcEntry == nil {
			if destEntry.IsRequired || !destEntry.IsReadOnly {
				addTypedDictAddendum(diag, localization.LocAddendum.TypedDictFieldMissing().Format(
					name, evaluator.PrintType(ClassTypeCloneAsInstance(srcType, true), nil)))
				typesAreConsistent = false
				return
			}

			// A not-required read-only item the source lacks is answered by what
			// an unknown source key would hold.
			if IsClassInstance(extraSrcEntries.ValueType) {
				subDiag := createTypedDictSubAddendum(diag)
				if !evaluator.AssignType(destEntry.ValueType, extraSrcEntries.ValueType,
					createTypedDictSubAddendum(subDiag), constraints, flags, recursionCount) {
					addTypedDictMessage(subDiag,
						localization.LocAddendum.MemberTypeMismatch().Format(name))
					typesAreConsistent = false
				}
			}
			return
		}

		if destEntry.IsRequired != srcEntry.IsRequired && !destEntry.IsReadOnly {
			message := localization.LocAddendum.TypedDictFieldNotRequired().Format(
				name, evaluator.PrintType(ClassTypeCloneAsInstance(destType, true), nil))
			if destEntry.IsRequired {
				message = localization.LocAddendum.TypedDictFieldRequired().Format(
					name, evaluator.PrintType(ClassTypeCloneAsInstance(destType, true), nil))
			}
			addTypedDictAddendum(diag, message)
			typesAreConsistent = false
		}

		if !destEntry.IsReadOnly && srcEntry.IsReadOnly {
			addTypedDictAddendum(diag, localization.LocAddendum.TypedDictFieldNotReadOnly().Format(
				name, evaluator.PrintType(ClassTypeCloneAsInstance(destType, true), nil)))
			typesAreConsistent = false
		}

		subDiag := createTypedDictSubAddendum(diag)

		// See the file header: a writable item is invariant, a read-only one is
		// not.
		entryFlags := flags
		if !destEntry.IsReadOnly {
			entryFlags |= AssignTypeFlagsInvariant
		}

		if !evaluator.AssignType(destEntry.ValueType, srcEntry.ValueType,
			createTypedDictSubAddendum(subDiag), constraints, entryFlags, recursionCount) {
			addTypedDictMessage(subDiag, localization.LocAddendum.MemberTypeMismatch().Format(name))
			typesAreConsistent = false
		}
	})

	// The original's comment: if the types are not consistent and the caller isn't
	// interested in detailed diagnostics, don't do additional work.
	if !typesAreConsistent && diag == nil {
		return false
	}

	// The original's comment: if the destination TypedDict is closed, check any
	// extra entries in the source TypedDict to ensure that they don't violate the
	// "extra items" type.
	if !ClassTypeIsTypedDictEffectivelyClosed(destType) {
		return typesAreConsistent
	}

	extraDestEntries := destEntries.ExtraItems
	if extraDestEntries == nil {
		extraDestEntries = GetEffectiveExtraItemsEntryType(evaluator, destType)
	}

	srcEntries.KnownItems.ForEach(func(srcEntry *TypedDictEntry, name string) {
		// The original's comment: have we already checked this item in the loop
		// above?
		if destEntries.KnownItems.Has(name) {
			return
		}

		if destEntries.ExtraItems == nil {
			subDiag := createTypedDictSubAddendum(diag)
			addTypedDictMessage(subDiag,
				localization.LocAddendum.TypedDictExtraFieldNotAllowed().Format(
					name, evaluator.PrintType(ClassTypeCloneAsInstance(destType, true), nil)))
			typesAreConsistent = false
			return
		}

		if srcEntry.IsRequired && !destEntries.ExtraItems.IsReadOnly {
			addTypedDictAddendum(diag, localization.LocAddendum.TypedDictFieldNotRequired().Format(
				name, evaluator.PrintType(ClassTypeCloneAsInstance(destType, true), nil)))
			typesAreConsistent = false
		}

		subDiag := createTypedDictSubAddendum(diag)

		entryFlags := flags
		if !destEntries.ExtraItems.IsReadOnly {
			entryFlags |= AssignTypeFlagsInvariant
		}

		if !evaluator.AssignType(destEntries.ExtraItems.ValueType, srcEntry.ValueType,
			createTypedDictSubAddendum(subDiag), constraints, entryFlags, recursionCount) {
			addTypedDictMessage(subDiag,
				localization.LocAddendum.TypedDictExtraFieldTypeMismatch().Format(
					name, evaluator.PrintType(ClassTypeCloneAsInstance(destType, true), nil)))
			typesAreConsistent = false
		} else if !destEntries.ExtraItems.IsReadOnly && srcEntry.IsReadOnly {
			addTypedDictAddendum(diag, localization.LocAddendum.TypedDictFieldNotReadOnly().Format(
				name, evaluator.PrintType(ClassTypeCloneAsInstance(srcType, true), nil)))
			typesAreConsistent = false
		}
	})

	subDiag := createTypedDictSubAddendum(diag)
	extraFlags := flags
	if !extraDestEntries.IsReadOnly {
		extraFlags |= AssignTypeFlagsInvariant
	}

	if !evaluator.AssignType(extraDestEntries.ValueType, extraSrcEntries.ValueType,
		createTypedDictSubAddendum(subDiag), constraints, extraFlags, recursionCount) {
		addTypedDictMessage(subDiag,
			localization.LocAddendum.TypedDictExtraFieldTypeMismatch().Format(
				"extra_items", evaluator.PrintType(ClassTypeCloneAsInstance(srcType, true), nil)))
		typesAreConsistent = false
	} else if !extraDestEntries.IsReadOnly && extraSrcEntries.IsReadOnly {
		addTypedDictAddendum(diag, localization.LocAddendum.TypedDictFieldNotReadOnly().Format(
			"extra_items", evaluator.PrintType(ClassTypeCloneAsInstance(destType, true), nil)))
		typesAreConsistent = false
	}

	return typesAreConsistent
}

// createTypedDictSubAddendum is the original's `diag?.createAddendum()`: nil in,
// nil out. Every call site is optional-chained, so a nil addendum must propagate
// rather than fault.
func createTypedDictSubAddendum(diag *common.DiagnosticAddendum) *common.DiagnosticAddendum {
	if diag == nil {
		return nil
	}
	return diag.CreateAddendum()
}

// addTypedDictMessage is the original's `diag?.addMessage(...)`.
func addTypedDictMessage(diag *common.DiagnosticAddendum, message string) {
	if diag == nil {
		return
	}
	diag.AddMessage(message)
}

// addTypedDictAddendum is the original's
// `diag?.createAddendum().addMessage(...)`, which creates the child only when
// there is a parent.
func addTypedDictAddendum(diag *common.DiagnosticAddendum, message string) {
	if diag == nil {
		return
	}
	diag.CreateAddendum().AddMessage(message)
}
