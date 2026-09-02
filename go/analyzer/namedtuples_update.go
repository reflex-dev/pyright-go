/*
 * namedtuples_update.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/namedTuples.ts (pyright 1.1.412):
 * updateNamedTupleBaseClass.
 *
 * A NamedTuple subclass inherits from `NamedTuple`, which inherits from a bare
 * `tuple`. Neither of those carries the field types, so after the fields are
 * known the base has to be rewritten to a `tuple[T1, T2, ...]`. That is what
 * this does, and it is why it returns a bool: the caller has to recompute the
 * MRO afterwards, since the base class object it walked is no longer the one in
 * the list.
 *
 * The clone of `shared` is load-bearing and is not an optimization detail. The
 * `NamedTuple` class object is shared across every NamedTuple in the program --
 * it comes from typeshed and is cached. Specializing its tuple base in place
 * would give every other NamedTuple in the run this class's field types. The
 * original writes `clonedNamedTupleClass.shared = { ...shared }` for exactly
 * that reason, and the Go equivalent must copy the struct through a new pointer
 * rather than assign through the existing one.
 */

package analyzer

// UpdateNamedTupleBaseClass corresponds to updateNamedTupleBaseClass. It reports
// whether any base class was replaced, which tells the caller the MRO is stale.
func UpdateNamedTupleBaseClass(classType *ClassType, typeArgs []Type, isTypeArgExplicit bool) bool {
	isUpdateNeeded := false

	updatedBases := make([]Type, 0, len(classType.Shared.BaseClasses))
	for _, baseClass := range classType.Shared.BaseClasses {
		if !IsInstantiableClass(baseClass) || !ClassTypeIsBuiltInNamed(baseClass.(*ClassType), "NamedTuple") {
			updatedBases = append(updatedBases, baseClass)
			continue
		}

		tupleTypeArgs := []*TupleTypeArg{}

		if !isTypeArgExplicit {
			// A NamedTuple whose entry types are not yet pinned down is a
			// homogeneous tuple of whatever the entries have in common, not a
			// zero-length one.
			entryType := Type(UnknownTypeCreate(false))
			if len(typeArgs) > 0 {
				entryType = CombineTypes(typeArgs, nil)
			}
			tupleTypeArgs = append(tupleTypeArgs, &TupleTypeArg{Type: entryType, IsUnbounded: true})
		} else {
			for _, t := range typeArgs {
				tupleTypeArgs = append(tupleTypeArgs, &TupleTypeArg{Type: t, IsUnbounded: false})
			}
		}

		// The original's comment: create a copy of the NamedTuple class that
		// replaces the tuple base class.
		clonedNamedTupleClass := ClassTypeSpecialize(baseClass.(*ClassType), nil, &isTypeArgExplicit, false, nil, nil)

		// See the file header: this copy keeps typeshed's cached `NamedTuple`
		// from acquiring this class's field types.
		sharedCopy := *clonedNamedTupleClass.Shared
		clonedNamedTupleClass.Shared = &sharedCopy

		updatedNamedTupleBases := make([]Type, 0, len(clonedNamedTupleClass.Shared.BaseClasses))
		for _, namedTupleBaseClass := range clonedNamedTupleClass.Shared.BaseClasses {
			if !IsInstantiableClass(namedTupleBaseClass) ||
				!ClassTypeIsBuiltInNamed(namedTupleBaseClass.(*ClassType), "tuple") {
				updatedNamedTupleBases = append(updatedNamedTupleBases, namedTupleBaseClass)
				continue
			}

			updatedNamedTupleBases = append(updatedNamedTupleBases,
				SpecializeTupleClass(namedTupleBaseClass.(*ClassType), tupleTypeArgs, isTypeArgExplicit, false))
		}
		clonedNamedTupleClass.Shared.BaseClasses = updatedNamedTupleBases

		ComputeMroLinearization(clonedNamedTupleClass)

		isUpdateNeeded = true
		updatedBases = append(updatedBases, clonedNamedTupleClass)
	}

	classType.Shared.BaseClasses = updatedBases

	return isUpdateNeeded
}
