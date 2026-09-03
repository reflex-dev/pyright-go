/*
 * patternmatching_sequence.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/patternMatching.ts (pyright 1.1.412):
 * narrowTypeBasedOnSequencePattern, getSequencePatternInfo,
 * getTypeOfPatternSequenceEntry and wrapTypeInList.
 *
 * A sequence pattern matches by *length* as well as by element type, so most of
 * the work here is reconciling a pattern of known arity against a tuple whose
 * arity may be indeterminate. getSequencePatternInfo does that reconciliation:
 * an unbounded tuple entry is duplicated to pad a short tuple out to the
 * pattern's length, or spliced away to contract it, and a star entry in the
 * pattern collapses however many tuple entries it has to absorb.
 *
 * Whether a match is *definite* is tracked separately from whether it is
 * possible, and that distinction is what makes the negative direction sound. A
 * tuple that had to have an unbounded entry removed to fit could genuinely have
 * been a different length, so it is a potential no-match and cannot be
 * eliminated in the negative case -- hence removedIndeterminate.
 *
 * Only tuples are narrowed on a match. Other sequences are not, and the
 * original's comment says why: a list is mutable and therefore invariant, so
 * `case [int(), int()]` cannot narrow a `list[object]` to `list[int]` -- the
 * object is still the same list and can still hold anything.
 *
 * The negative-case tuple expansion is the reason maxSequencePatternTupleExpansionSubtypes
 * exists. Narrowing `tuple[A|B, C|D]` negatively against a two-element pattern
 * produces one tuple per narrowed dimension, and with large unions that product
 * explodes; the cap converts the result to Any rather than hanging.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// narrowTypeBasedOnSequencePattern corresponds to the function of the same name.
func narrowTypeBasedOnSequencePattern(
	evaluator TypeEvaluator, t Type, pattern *parser.PatternSequenceNode, isPositiveTest bool,
) Type {
	usingTupleExpansion := false
	t = TransformPossibleRecursiveTypeAlias(t, 0)
	sequenceInfo := getSequencePatternInfo(evaluator, pattern, t)

	// The original's comment: further narrow based on pattern entry types.
	filtered := sequenceInfo[:0]
	for _, entry := range sequenceInfo {
		keep := narrowSequenceEntry(evaluator, pattern, entry, isPositiveTest, &usingTupleExpansion)
		if keep {
			filtered = append(filtered, entry)
		}
	}
	sequenceInfo = filtered

	subtypes := make([]Type, 0, len(sequenceInfo))
	for _, entry := range sequenceInfo {
		subtypes = append(subtypes, entry.Subtype)
	}

	options := &CombineTypesOptions{}
	if usingTupleExpansion {
		maxCount := maxSequencePatternTupleExpansionSubtypes
		options.MaxSubtypeCount = &maxCount
	}
	return CombineTypes(subtypes, options)
}

// narrowSequenceEntry is the body of the original's filter callback. It mutates
// entry.Subtype in place, as the original does, and reports whether the entry
// survives the filter.
func narrowSequenceEntry(
	evaluator TypeEvaluator,
	pattern *parser.PatternSequenceNode,
	entry *SequencePatternInfo,
	isPositiveTest bool,
	usingTupleExpansion *bool,
) bool {
	if entry.IsDefiniteNoMatch {
		return !isPositiveTest
	}

	isPlausibleMatch := true
	isDefiniteMatch := true
	narrowedEntryTypes := []Type{}
	unnarrowedEntryTypes := []Type{}
	canNarrowTuple := entry.IsTuple

	// The original's comment: don't attempt to narrow tuples in the negative case
	// if the subject contains indeterminate-length entries or the tuple is of
	// indeterminate length.
	if !isPositiveTest {
		if entry.IsIndeterminateLength || entry.IsUnboundedTuple {
			canNarrowTuple = false
		}

		if IsClassInstance(entry.Subtype) && entry.Subtype.(*ClassType).Priv.TupleTypeArgs != nil {
			tupleArgs := entry.Subtype.(*ClassType).Priv.TupleTypeArgs
			unboundedIndex := -1
			for i, typeArg := range tupleArgs {
				if typeArg.IsUnbounded {
					unboundedIndex = i
					break
				}
			}

			if unboundedIndex >= 0 {
				// The original's comment: if the pattern includes a "star" entry
				// that aligns exactly with the corresponding unbounded entry in the
				// tuple, we can narrow the tuple type.
				if pattern.D.StarEntryIndex == nil || *pattern.D.StarEntryIndex != unboundedIndex {
					canNarrowTuple = false
				}
			}
		}
	}

	// The original's comment: if the subject has an indeterminate length but the
	// pattern does not accept an arbitrary number of entries or accepts at least
	// one non-star entry, we can't prove that it's a definite match.
	if entry.IsIndeterminateLength {
		if len(pattern.D.Entries) != 1 || pattern.D.StarEntryIndex == nil || *pattern.D.StarEntryIndex != 0 {
			isDefiniteMatch = false
		}
	}

	negativeNarrowedDims := []int{}
	for index, sequenceEntry := range pattern.D.Entries {
		entryType := getTypeOfPatternSequenceEntry(evaluator, pattern, entry, index,
			len(pattern.D.Entries), pattern.D.StarEntryIndex, true)

		unnarrowedEntryTypes = append(unnarrowedEntryTypes, entryType)
		narrowedEntryType := NarrowTypeBasedOnPattern(evaluator, entryType, sequenceEntry, isPositiveTest)

		isStarEntry := pattern.D.StarEntryIndex != nil && *pattern.D.StarEntryIndex == index

		if isPositiveTest {
			if isStarEntry {
				if IsClassInstance(narrowedEntryType) &&
					narrowedEntryType.(*ClassType).Priv.TupleTypeArgs != nil &&
					!IsUnboundedTupleClass(narrowedEntryType.(*ClassType)) {
					for _, tupleArg := range narrowedEntryType.(*ClassType).Priv.TupleTypeArgs {
						narrowedEntryTypes = append(narrowedEntryTypes, tupleArg.Type)
					}
				} else {
					narrowedEntryTypes = append(narrowedEntryTypes, narrowedEntryType)
					canNarrowTuple = false
				}
			} else {
				narrowedEntryTypes = append(narrowedEntryTypes, narrowedEntryType)

				if IsNever(narrowedEntryType) {
					isPlausibleMatch = false
				}
			}
			continue
		}

		if entry.IsPotentialNoMatch {
			isDefiniteMatch = false
		}

		if !IsNever(narrowedEntryType) {
			isDefiniteMatch = false

			// The original's comment: record which entries were narrowed in the
			// negative case by storing their indexes. If more than one is narrowed,
			// we need to perform tuple expansion to represent the resulting
			// narrowed type.
			negativeNarrowedDims = append(negativeNarrowedDims, index)
			narrowedEntryTypes = append(narrowedEntryTypes, narrowedEntryType)
		} else {
			narrowedEntryTypes = append(narrowedEntryTypes, entryType)
		}

		if isStarEntry {
			canNarrowTuple = false
		}
	}

	if len(pattern.D.Entries) == 0 {
		// The original's comment: if the pattern is an empty sequence, use the
		// entry types.
		if len(entry.EntryTypes) > 0 {
			narrowedEntryTypes = append(narrowedEntryTypes, CombineTypes(entry.EntryTypes, nil))
		}

		if entry.IsPotentialNoMatch {
			isDefiniteMatch = false
		}
	}

	if !isPositiveTest {
		// The original's comment: if the positive case is a definite match, the
		// negative case can eliminate this subtype entirely.
		if isDefiniteMatch {
			return false
		}

		// The original's comment: can we narrow a tuple?
		if canNarrowTuple && len(negativeNarrowedDims) > 0 {
			tupleClassType := evaluator.GetBuiltInType(pattern, "tuple")
			if tupleClassType != nil && IsInstantiableClass(tupleClassType) {
				// The original's comment: expand the tuple in the dimensions that
				// were narrowed. Start with the fully-narrowed set of entries.
				expanded := make([]Type, 0, len(negativeNarrowedDims))

				for _, dim := range negativeNarrowedDims {
					newEntryTypes := make([]Type, len(unnarrowedEntryTypes))
					copy(newEntryTypes, unnarrowedEntryTypes)
					newEntryTypes[dim] = narrowedEntryTypes[dim]

					tupleArgs := make([]*TupleTypeArg, 0, len(newEntryTypes))
					for _, et := range newEntryTypes {
						tupleArgs = append(tupleArgs, &TupleTypeArg{Type: et, IsUnbounded: false})
					}
					expanded = append(expanded, ClassTypeCloneAsInstance(
						SpecializeTupleClass(tupleClassType.(*ClassType), tupleArgs, true, false), true))
				}

				entry.Subtype = CombineTypes(expanded, nil)

				// The original's comment: note that we're using tuple expansion in
				// case we need to limit the number of subtypes generated.
				*usingTupleExpansion = true
			}
		}

		return true
	}

	if isPlausibleMatch {
		// The original's comment: if this is a tuple, we can narrow it to a
		// specific tuple type. Other sequences cannot be narrowed because we don't
		// know if they are immutable (covariant).
		if canNarrowTuple {
			tupleClassType := evaluator.GetBuiltInType(pattern, "tuple")
			if tupleClassType != nil && IsInstantiableClass(tupleClassType) {
				tupleArgs := make([]*TupleTypeArg, 0, len(narrowedEntryTypes))
				for _, et := range narrowedEntryTypes {
					tupleArgs = append(tupleArgs, &TupleTypeArg{Type: et, IsUnbounded: false})
				}
				entry.Subtype = ClassTypeCloneAsInstance(
					SpecializeTupleClass(tupleClassType.(*ClassType), tupleArgs, true, false), true)
			}
		}

		// The original's comment: if this is a supertype of Sequence, we can narrow
		// it to a Sequence type.
		if entry.IsPotentialNoMatch && !entry.IsTuple {
			sequenceType := evaluator.GetTypingType(pattern, "Sequence")
			if sequenceType != nil && IsInstantiableClass(sequenceType) {
				typeArgType := evaluator.StripLiteralValue(CombineTypes(narrowedEntryTypes, nil))

				// The original's comment: if the type is a union that contains Any
				// or Unknown, remove the other types before wrapping it in a
				// Sequence.
				if collapsed := ContainsAnyOrUnknown(typeArgType, false); collapsed != nil {
					typeArgType = collapsed
				}

				entry.Subtype = ClassTypeCloneAsInstance(ClassTypeSpecialize(
					sequenceType.(*ClassType), []Type{typeArgType}, nil, false, nil, nil), true)
			}
		}
	}

	return isPlausibleMatch
}

// getSequencePatternInfo corresponds to the function of the same name. The
// original's comment: returns information about all subtypes that match the
// definition of a "sequence" as specified in PEP 634. For types that are not
// sequences or sequences that are not of sufficient length, it sets
// definiteNoMatch to true.
func getSequencePatternInfo(
	evaluator TypeEvaluator, pattern *parser.PatternSequenceNode, t Type,
) []*SequencePatternInfo {
	patternEntryCount := len(pattern.D.Entries)
	patternStarEntryIndex := pattern.D.StarEntryIndex
	sequenceInfo := []*SequencePatternInfo{}

	DoForEachSubtype(t, func(subtype Type, _ int, _ []Type) {
		concreteSubtype := evaluator.MakeTopLevelTypeVarsConcrete(subtype, false)
		var mroClassToSpecialize *ClassType

		if IsClassInstance(concreteSubtype) {
			for _, mroClass := range concreteSubtype.(*ClassType).Shared.Mro {
				if !IsInstantiableClass(mroClass) {
					break
				}
				mroClassType := mroClass.(*ClassType)

				// The original's comment: strings, bytes, and bytearray are
				// explicitly excluded. A str is iterable but PEP 634 does not treat
				// it as a sequence pattern subject.
				if ClassTypeIsBuiltInNamed(mroClassType, "str") ||
					ClassTypeIsBuiltInNamed(mroClassType, "bytes") ||
					ClassTypeIsBuiltInNamed(mroClassType, "bytearray") {
					sequenceInfo = append(sequenceInfo, &SequencePatternInfo{
						Subtype:               subtype,
						EntryTypes:            []Type{},
						IsIndeterminateLength: true,
						IsDefiniteNoMatch:     true,
					})
					return
				}

				if ClassTypeIsBuiltInNamed(mroClassType, "Sequence") {
					mroClassToSpecialize = mroClassType
					break
				}

				if IsTupleClass(mroClassType) {
					mroClassToSpecialize = mroClassType
					break
				}
			}

			if mroClassToSpecialize != nil {
				specialized := PartiallySpecializeType(mroClassToSpecialize, concreteSubtype.(*ClassType),
					evaluator.GetTypeClassType(), nil)
				specializedSequence, ok := specialized.(*ClassType)
				if !ok {
					return
				}

				if IsTupleClass(specializedSequence) {
					if handled := appendTupleSequenceInfo(evaluator, pattern, subtype, specializedSequence,
						patternEntryCount, patternStarEntryIndex, &sequenceInfo); handled {
						return
					}
				} else {
					var entryType Type = UnknownTypeCreate(false)
					if specializedSequence.Priv.TypeArgs != nil && len(specializedSequence.Priv.TypeArgs) > 0 {
						entryType = specializedSequence.Priv.TypeArgs[0]
					}
					sequenceInfo = append(sequenceInfo, &SequencePatternInfo{
						Subtype:               subtype,
						EntryTypes:            []Type{entryType},
						IsIndeterminateLength: true,
						IsDefiniteNoMatch:     false,
					})
					return
				}
			}
		}

		if mroClassToSpecialize == nil {
			if appendAbstractSequenceInfo(evaluator, pattern, subtype, &sequenceInfo) {
				return
			}
		}

		// The original's comment: push an entry that indicates that this is
		// definitely not a match.
		sequenceInfo = append(sequenceInfo, &SequencePatternInfo{
			Subtype:               subtype,
			EntryTypes:            []Type{},
			IsIndeterminateLength: true,
			IsDefiniteNoMatch:     true,
		})
	})

	return sequenceInfo
}

// appendTupleSequenceInfo is the original's `if (isTupleClass(specializedSequence))`
// arm. It reports whether it produced an entry, which corresponds to the
// original's `return` from the forEach callback.
func appendTupleSequenceInfo(
	evaluator TypeEvaluator,
	pattern *parser.PatternSequenceNode,
	subtype Type,
	specializedSequence *ClassType,
	patternEntryCount int,
	patternStarEntryIndex *int,
	sequenceInfo *[]*SequencePatternInfo,
) bool {
	// The tuple type args are mutated below (spliced, padded), so a copy is taken
	// rather than aliasing the class's own slice. JavaScript's splice mutates the
	// array in place too, but that array is freshly produced by the specialize
	// call there; here Priv.TupleTypeArgs may be shared.
	var typeArgs []*TupleTypeArg
	if specializedSequence.Priv.TupleTypeArgs != nil {
		typeArgs = make([]*TupleTypeArg, len(specializedSequence.Priv.TupleTypeArgs))
		copy(typeArgs, specializedSequence.Priv.TupleTypeArgs)
	} else {
		typeArgs = []*TupleTypeArg{{Type: UnknownTypeCreate(false), IsUnbounded: true}}
	}

	tupleIndeterminateIndex := -1
	for i, ta := range typeArgs {
		if ta.IsUnbounded || IsUnpackedTypeVarTuple(ta.Type) || IsUnpackedTypeVar(ta.Type) {
			tupleIndeterminateIndex = i
			break
		}
	}

	tupleDeterminateEntryCount := len(typeArgs)

	// The original's comment: if the tuple contains an indeterminate entry, expand
	// or remove that entry to match the length of the pattern if possible.
	expandedIndeterminate := false
	// The original's comment: tracks whether the indeterminate entry was spliced
	// out to contract the tuple to fit a shorter pattern. This preserves "potential
	// match" semantics after the splice resets tupleIndeterminateIndex to -1.
	removedIndeterminate := false
	if tupleIndeterminateIndex >= 0 {
		tupleDeterminateEntryCount--

		for len(typeArgs) < patternEntryCount {
			typeArgs = tupleArgsInsertAt(typeArgs, tupleIndeterminateIndex, typeArgs[tupleIndeterminateIndex])
			tupleDeterminateEntryCount++
			tupleIndeterminateIndex++
			expandedIndeterminate = true
		}

		if len(typeArgs) > patternEntryCount && patternStarEntryIndex == nil {
			typeArgs = append(typeArgs[:tupleIndeterminateIndex], typeArgs[tupleIndeterminateIndex+1:]...)
			removedIndeterminate = true
			tupleIndeterminateIndex = -1
		}
	}

	// The original's comment: if the pattern contains a star entry and there are
	// too many entries in the tuple, we can collapse some of them into the star
	// entry.
	if patternStarEntryIndex != nil && len(typeArgs) >= 2 && len(typeArgs) > patternEntryCount {
		starIndex := *patternStarEntryIndex
		entriesToCombine := len(typeArgs) - patternEntryCount + 1

		removedEntries := make([]*TupleTypeArg, entriesToCombine)
		copy(removedEntries, typeArgs[starIndex:starIndex+entriesToCombine])
		typeArgs = append(typeArgs[:starIndex], typeArgs[starIndex+entriesToCombine:]...)

		combinedTypes := make([]Type, 0, len(removedEntries))
		allUnbounded := true
		for _, re := range removedEntries {
			combinedTypes = append(combinedTypes, re.Type)
			if !(re.IsUnbounded || IsUnpackedTypeVarTuple(re.Type) || IsUnpackedTypeVar(re.Type)) {
				allUnbounded = false
			}
		}
		typeArgs = tupleArgsInsertAt(typeArgs, starIndex, &TupleTypeArg{
			Type:        CombineTypes(combinedTypes, nil),
			IsUnbounded: allUnbounded,
		})

		tupleDeterminateEntryCount -= entriesToCombine
		if !typeArgs[starIndex].IsUnbounded {
			tupleDeterminateEntryCount++
		}

		// The original's comment: if the collapsed range included the
		// tupleIndeterminateIndex, adjust it to reflect the new collapsed entry.
		if tupleIndeterminateIndex >= starIndex && tupleIndeterminateIndex < starIndex+entriesToCombine {
			tupleIndeterminateIndex = starIndex
		}
	}

	if len(typeArgs) == patternEntryCount {
		isDefiniteNoMatch := false
		isPotentialNoMatch := tupleIndeterminateIndex >= 0 || removedIndeterminate

		// The original's comment: if we removed an unbounded entry to make the
		// lengths match, this is a potential match (not definite) because the
		// original tuple could have different lengths.
		if removedIndeterminate {
			isPotentialNoMatch = true
		}

		// The original's comment: if the pattern includes a "star entry" and the
		// tuple includes an indeterminate-length entry that aligns to the star
		// entry, we can assume it will always match.
		if !expandedIndeterminate && patternStarEntryIndex != nil && tupleIndeterminateIndex >= 0 &&
			len(pattern.D.Entries)-1 == tupleDeterminateEntryCount &&
			*patternStarEntryIndex == tupleIndeterminateIndex {
			isPotentialNoMatch = false
		}

		for i := 0; i < patternEntryCount; i++ {
			narrowedType := NarrowTypeBasedOnPattern(evaluator, typeArgs[i].Type, pattern.D.Entries[i], true)
			if IsNever(narrowedType) {
				isDefiniteNoMatch = true
			}
		}

		entryTypes := []Type{}
		if !isDefiniteNoMatch {
			for _, ta := range typeArgs {
				entryTypes = append(entryTypes, ta.Type)
			}
		}

		*sequenceInfo = append(*sequenceInfo, &SequencePatternInfo{
			Subtype:               subtype,
			EntryTypes:            entryTypes,
			IsIndeterminateLength: false,
			IsTuple:               true,
			IsUnboundedTuple:      removedIndeterminate || tupleIndeterminateIndex >= 0,
			IsDefiniteNoMatch:     isDefiniteNoMatch,
			IsPotentialNoMatch:    isPotentialNoMatch,
		})
		return true
	}

	// The original's comment: if the pattern contains a star entry and the pattern
	// associated with the star entry is unbounded, we can remove it completely
	// under the assumption that the star pattern will capture nothing.
	if patternStarEntryIndex == nil {
		return false
	}
	starIndex := *patternStarEntryIndex

	tryMatchStarSequence := false

	if len(typeArgs) == patternEntryCount-1 {
		tryMatchStarSequence = true
		typeArgs = tupleArgsInsertAt(typeArgs, starIndex, &TupleTypeArg{
			Type: AnyTypeCreate(false), IsUnbounded: true,
		})
	} else if len(typeArgs) == patternEntryCount && typeArgs[starIndex].IsUnbounded {
		tryMatchStarSequence = true
	}

	if !tryMatchStarSequence {
		return false
	}

	isDefiniteNoMatch := false

	for i := 0; i < patternEntryCount; i++ {
		if i == starIndex {
			continue
		}

		narrowedType := NarrowTypeBasedOnPattern(evaluator, typeArgs[i].Type, pattern.D.Entries[i], true)
		if IsNever(narrowedType) {
			isDefiniteNoMatch = true
		}
	}

	entryTypes := []Type{}
	if !isDefiniteNoMatch {
		for _, ta := range typeArgs {
			entryTypes = append(entryTypes, ta.Type)
		}
	}

	*sequenceInfo = append(*sequenceInfo, &SequencePatternInfo{
		Subtype:               subtype,
		EntryTypes:            entryTypes,
		IsIndeterminateLength: false,
		IsTuple:               true,
		IsUnboundedTuple:      tupleIndeterminateIndex >= 0,
		IsDefiniteNoMatch:     isDefiniteNoMatch,
	})
	return true
}

// appendAbstractSequenceInfo is the original's `if (!mroClassToSpecialize)` arm,
// which handles subjects that are Sequence-shaped without being a tuple or a
// concrete Sequence subclass.
func appendAbstractSequenceInfo(
	evaluator TypeEvaluator,
	pattern *parser.PatternSequenceNode,
	subtype Type,
	sequenceInfo *[]*SequencePatternInfo,
) bool {
	sequenceType := evaluator.GetTypingType(pattern, "Sequence")
	if sequenceType == nil || !IsInstantiableClass(sequenceType) {
		return false
	}
	sequenceClass := sequenceType.(*ClassType)
	sequenceObject := ClassTypeCloneAsInstance(sequenceClass, true)

	// The original's comment: is it a subtype of Sequence?
	constraints := NewConstraintTracker()
	if evaluator.AssignType(sequenceObject, subtype, nil, constraints, AssignTypeFlagsDefault, 0) {
		if specialized, ok := evaluator.SolveAndApplyConstraints(
			sequenceObject, constraints, nil, nil).(*ClassType); ok {
			if len(specialized.Priv.TypeArgs) > 0 {
				*sequenceInfo = append(*sequenceInfo, &SequencePatternInfo{
					Subtype:               subtype,
					EntryTypes:            []Type{specialized.Priv.TypeArgs[0]},
					IsIndeterminateLength: true,
					IsDefiniteNoMatch:     false,
					IsPotentialNoMatch:    false,
				})
				return true
			}
		}
	}

	// The original's comment: if it wasn't a subtype of Sequence, see if it's a
	// supertype. A supertype only *might* be a sequence at runtime, hence
	// isPotentialNoMatch below.
	sequenceConstraints := NewConstraintTracker()
	if AddConstraintsForExpectedType(evaluator, ClassTypeCloneAsInstance(sequenceClass, true), subtype,
		sequenceConstraints, GetTypeVarScopesForNode(pattern), pattern.NodeBase().TextRange.Start) {
		if specialized, ok := evaluator.SolveAndApplyConstraints(
			ClassTypeCloneAsInstantiable(sequenceClass, false), sequenceConstraints, nil, nil).(*ClassType); ok {
			if len(specialized.Priv.TypeArgs) > 0 {
				*sequenceInfo = append(*sequenceInfo, &SequencePatternInfo{
					Subtype:               subtype,
					EntryTypes:            []Type{specialized.Priv.TypeArgs[0]},
					IsIndeterminateLength: true,
					IsDefiniteNoMatch:     false,
					IsPotentialNoMatch:    true,
				})
				return true
			}
		}
	}

	if evaluator.AssignType(subtype, ClassTypeSpecialize(
		ClassTypeCloneAsInstance(sequenceClass, true),
		[]Type{UnknownTypeCreate(false)}, nil, false, nil, nil),
		nil, nil, AssignTypeFlagsDefault, 0) {
		*sequenceInfo = append(*sequenceInfo, &SequencePatternInfo{
			Subtype:               subtype,
			EntryTypes:            []Type{UnknownTypeCreate(false)},
			IsIndeterminateLength: true,
			IsDefiniteNoMatch:     false,
			IsPotentialNoMatch:    true,
		})
		return true
	}

	return false
}

// getTypeOfPatternSequenceEntry corresponds to the function of the same name.
func getTypeOfPatternSequenceEntry(
	evaluator TypeEvaluator,
	node parser.ParseNode,
	sequenceInfo *SequencePatternInfo,
	entryIndex int,
	entryCount int,
	starEntryIndex *int,
	unpackStarEntry bool,
) Type {
	if sequenceInfo.IsIndeterminateLength {
		entryType := sequenceInfo.EntryTypes[0]

		if !unpackStarEntry && starEntryIndex != nil && entryIndex == *starEntryIndex && !IsNever(entryType) {
			entryType = wrapTypeInList(evaluator, node, entryType)
		}

		return entryType
	}

	if starEntryIndex == nil || entryIndex < *starEntryIndex {
		return sequenceInfo.EntryTypes[entryIndex]
	}

	if entryIndex == *starEntryIndex {
		// The original's comment: create a list out of the entries that map to the
		// star entry. Note that we strip literal types here.
		end := *starEntryIndex + len(sequenceInfo.EntryTypes) - entryCount + 1
		starEntryTypes := make([]Type, 0, end-*starEntryIndex)
		for _, entryType := range sequenceInfo.EntryTypes[*starEntryIndex:end] {
			// The original's comment: if this is a TypeVarTuple, there's not much
			// we can say about its type other than it's "Unknown". We could
			// evaluate it as an "object", but that will cause problems given that
			// this type will be wrapped in a "list" below, and lists are invariant.
			if IsTypeVarTuple(entryType) && !entryType.(*TypeVarType).Priv.IsInUnion {
				starEntryTypes = append(starEntryTypes, UnknownTypeCreate(false))
				continue
			}

			starEntryTypes = append(starEntryTypes, evaluator.StripLiteralValue(entryType))
		}

		entryType := CombineTypes(starEntryTypes, nil)

		if !unpackStarEntry {
			entryType = wrapTypeInList(evaluator, node, entryType)
		}

		return entryType
	}

	// The original's comment: the entry index is past the index of the star entry,
	// so we need to index from the end of the sequence rather than the start.
	itemIndex := len(sequenceInfo.EntryTypes) - (entryCount - entryIndex)
	if itemIndex < 0 || itemIndex >= len(sequenceInfo.EntryTypes) {
		// The original asserts here. The port cannot abort the whole analysis on
		// an internal inconsistency, so it degrades to Unknown.
		return UnknownTypeCreate(false)
	}

	return sequenceInfo.EntryTypes[itemIndex]
}

// wrapTypeInList corresponds to the function of the same name: a star capture
// binds a list of the absorbed entries.
func wrapTypeInList(evaluator TypeEvaluator, node parser.ParseNode, t Type) Type {
	if IsNever(t) {
		return t
	}

	listObjectType := ConvertToInstance(evaluator.GetBuiltInObject(node, "list", nil), true)
	if listObjectType != nil && IsClassInstance(listObjectType) {
		// The original's comment: if the type is a union that contains an Any or
		// Unknown, eliminate the other types before wrapping it in a list.
		if collapsed := ContainsAnyOrUnknown(t, false); collapsed != nil {
			t = collapsed
		}

		return ClassTypeSpecialize(listObjectType.(*ClassType), []Type{t}, nil, false, nil, nil)
	}

	return UnknownTypeCreate(false)
}

// tupleArgsInsertAt reproduces `Array.prototype.splice(index, 0, item)`.
func tupleArgsInsertAt(args []*TupleTypeArg, index int, item *TupleTypeArg) []*TupleTypeArg {
	result := make([]*TupleTypeArg, 0, len(args)+1)
	result = append(result, args[:index]...)
	result = append(result, item)
	result = append(result, args[index:]...)
	return result
}
