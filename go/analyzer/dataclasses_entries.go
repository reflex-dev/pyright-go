/*
 * dataclasses_entries.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/dataClasses.ts (pyright 1.1.412):
 * addInheritedDataClassEntries.
 *
 * A dataclass's synthesized `__init__` takes its fields in inheritance order,
 * base classes first, with a subclass's redeclaration of a field keeping the
 * base's *position* while taking the subclass's type. Walking the reverse MRO
 * and replacing in place rather than appending is what produces exactly that.
 *
 * The ClassVar case is the exception and it removes rather than replaces: a
 * subclass that redeclares an inherited field as a ClassVar is saying it is no
 * longer an instance field, so it must disappear from `__init__` entirely
 * instead of moving to the end.
 *
 * Each entry's type is rewritten through the MRO class's own solution, because a
 * generic base contributes its field in the base's type-parameter namespace and
 * the subclass needs it in its own.
 */

package analyzer

// AddInheritedDataClassEntries corresponds to addInheritedDataClassEntries. It
// appends to the caller's slice through a pointer, since the original mutates
// the array it is handed. The result reports whether every ancestor was a known
// class.
func AddInheritedDataClassEntries(classType *ClassType, entries *[]*DataClassEntry) bool {
	allAncestorsAreKnown := true

	for _, mroClass := range ClassTypeGetReverseMro(classType) {
		if !IsInstantiableClass(mroClass) {
			allAncestorsAreKnown = false
			continue
		}

		mroClassType := mroClass.(*ClassType)
		solution := BuildSolutionFromSpecializedClass(mroClassType)

		// The original's comment: add the entries to the end of the list,
		// replacing same-named entries if found.
		for _, entry := range ClassTypeGetDataClassEntries(mroClassType) {
			existingIndex := -1
			for i, e := range *entries {
				if e.Name == entry.Name {
					existingIndex = i
					break
				}
			}

			// The original's comment: if the type from the parent class is
			// generic, we need to convert to the type parameter namespace of the
			// child class.
			updatedEntry := *entry
			updatedEntry.MroClass = mroClassType
			updatedEntry.Type = ApplySolvedTypeVars(updatedEntry.Type, solution, nil)

			if entry.IsClassVar {
				// The original's comment: if this entry is a class variable, it
				// overrides an existing instance variable, so delete it.
				if existingIndex >= 0 {
					*entries = append((*entries)[:existingIndex], (*entries)[existingIndex+1:]...)
				}
				continue
			}

			if existingIndex >= 0 {
				(*entries)[existingIndex] = &updatedEntry
				continue
			}

			*entries = append(*entries, &updatedEntry)
		}
	}

	return allAncestorsAreKnown
}
