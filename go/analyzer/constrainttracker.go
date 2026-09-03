/*
 * constrainttracker.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/constraintTracker.ts (pyright 1.1.412).
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
)

// maxConstraintSetCount is the original's constant, with its comment: the
// maximum number of constraint sets that can be associated with a constraint
// tracker. This equates to the number of overloads that can be captured by a
// ParamSpec (or multiple ParamSpecs). We should never hit this limit in
// practice, but there are certain pathological cases where we could, and we
// need to protect against this so it doesn't completely exhaust memory. This
// was previously set to 64, but we have seen cases where a library uses in
// excess of 300 overloads on a single function.
const maxConstraintSetCount = 1024

// TypeVarConstraints records constraint information about a single type
// variable.
type TypeVarConstraints struct {
	TypeVar *TypeVarType

	// Bounds for solved type variable as constraints are added. Nil where the
	// original has undefined.
	LowerBound Type
	UpperBound Type

	// RetainLiterals asks whether the lower bound should include literal values.
	RetainLiterals bool
}

// ConstraintSet records the constraints information for a set of type variables
// associated with a callee's signature.
type ConstraintSet struct {
	// typeVarMap maps type variable IDs to their current constraints. The
	// original is a Map, whose iteration order is insertion order, and that
	// order is observable through getScore and doForEachTypeVar.
	// Held by value for the same reason as ConstraintSolutionSet's map: one
	// allocation per set, not two, at tens of millions of sets per run.
	typeVarMap common.OrderedMap[string, *TypeVarConstraints]

	// scopeIds is a set of one or more TypeVar scope IDs that identify this
	// constraint set. The original's comment: this corresponds to the scope ID
	// of the overload signature. Normally there will be only one scope ID
	// associated with each signature, but we can have multiple if we are
	// solving for multiple ParamSpecs. If there are two ParamSpecs P1 and P2
	// and both are bound to 3 overloads, we'll have 9 sets of TypeVars that
	// we're solving, for all combinations of P1 and P2.
	//
	// Nil where the original leaves the field undefined; hasScopeId answers
	// false in that state rather than allocating.
	scopeIds *common.OrderedSet[string]
}

func NewConstraintSet() *ConstraintSet {
	return &ConstraintSet{}
}

func (c *ConstraintSet) Clone() *ConstraintSet {
	constraintSet := NewConstraintSet()
	c.cloneInto(constraintSet)
	return constraintSet
}

// cloneInto fills a zero-valued destination with a copy of this set. The
// cloned entries share one backing array instead of one allocation each --
// constraint sets are cloned tens of millions of times per large run -- and
// each clone owns its entries exactly as it did when SetBounds built them
// individually.
func (c *ConstraintSet) cloneInto(dst *ConstraintSet) {
	if n := c.typeVarMap.Size(); n > 0 {
		backing := make([]TypeVarConstraints, 0, n)
		c.typeVarMap.ForEach(func(value *TypeVarConstraints, key string) {
			backing = append(backing, *value)
			dst.typeVarMap.Set(key, &backing[len(backing)-1])
		})
	}

	if c.scopeIds != nil {
		c.scopeIds.ForEach(func(scopeId string) { dst.AddScopeId(scopeId) })
	}
}

func (c *ConstraintSet) IsSame(other *ConstraintSet) bool {
	if c.typeVarMap.Size() != other.typeVarMap.Size() {
		return false
	}

	// The original's inner function. Two absent bounds are the same; one
	// absent and one present are not.
	typesMatch := func(type1, type2 Type) bool {
		if type1 == nil || type2 == nil {
			return type1 == nil && type2 == nil
		}

		return IsTypeSame(type1, type2, TypeSameOptions{
			HonorIsTypeArgExplicit: true,
			HonorTypeForm:          true,
		}, 0)
	}

	// The original keeps walking the whole map after finding a mismatch, so
	// this does too; the extra comparisons have no side effects.
	isSame := true
	c.typeVarMap.ForEach(func(value *TypeVarConstraints, key string) {
		otherValue, ok := other.typeVarMap.Get(key)
		if !ok ||
			!typesMatch(value.LowerBound, otherValue.LowerBound) ||
			!typesMatch(value.UpperBound, otherValue.UpperBound) {
			isSame = false
		}
	})

	return isSame
}

func (c *ConstraintSet) IsEmpty() bool { return c.typeVarMap.Size() == 0 }

// GetScore provides a "score" -- a value that values completeness (number of
// type variables that are assigned) and simplicity.
func (c *ConstraintSet) GetScore() float64 {
	score := 0.0

	// Sum the scores for the defined type vars.
	c.typeVarMap.ForEach(func(entry *TypeVarConstraints, _ string) {
		// Add 1 to the score for each type variable defined.
		score += 1

		// The original's comment: add a fractional amount based on the
		// simplicity of the definition. The more complex, the lower the score.
		// In the spirit of Occam's Razor, we always want to favor simple
		// answers.
		typeVarType := entry.LowerBound
		if typeVarType == nil {
			typeVarType = entry.UpperBound
		}
		if typeVarType != nil {
			score += 1.0 - GetComplexityScoreForType(typeVarType, 0)
		}
	})

	return score
}

func (c *ConstraintSet) SetBounds(typeVar *TypeVarType, lowerBound Type, upperBound Type, retainLiterals bool) {
	key := TypeVarTypeGetNameWithScope(typeVar)
	c.typeVarMap.Set(key, &TypeVarConstraints{
		TypeVar:        typeVar,
		LowerBound:     lowerBound,
		UpperBound:     upperBound,
		RetainLiterals: retainLiterals,
	})
}

func (c *ConstraintSet) DoForEachTypeVar(cb func(entry *TypeVarConstraints)) {
	c.typeVarMap.ForEach(func(value *TypeVarConstraints, _ string) { cb(value) })
}

// GetTypeVar returns nil where the original returns undefined.
func (c *ConstraintSet) GetTypeVar(typeVar *TypeVarType) *TypeVarConstraints {
	key := TypeVarTypeGetNameWithScope(typeVar)
	entry, ok := c.typeVarMap.Get(key)
	if !ok {
		return nil
	}
	return entry
}

func (c *ConstraintSet) GetTypeVars() []*TypeVarConstraints {
	entries := []*TypeVarConstraints{}

	c.typeVarMap.ForEach(func(entry *TypeVarConstraints, _ string) {
		entries = append(entries, entry)
	})

	return entries
}

func (c *ConstraintSet) AddScopeId(scopeId TypeVarScopeId) {
	if c.scopeIds == nil {
		c.scopeIds = common.NewOrderedSet[string]()
	}

	c.scopeIds.Add(scopeId)
}

func (c *ConstraintSet) HasScopeId(scopeId TypeVarScopeId) bool {
	if c.scopeIds == nil {
		return false
	}

	return c.scopeIds.Has(scopeId)
}

// GetScopeIds returns a copy, matching `new Set(this._scopeIds)`. Note that the
// original produces an empty set rather than undefined when there are none.
func (c *ConstraintSet) GetScopeIds() *common.OrderedSet[string] {
	if c.scopeIds == nil {
		return common.NewOrderedSet[string]()
	}
	return common.NewOrderedSetFrom(c.scopeIds.Values())
}

func (c *ConstraintSet) HasUnificationVars() bool {
	for _, entry := range c.GetTypeVars() {
		if TypeVarTypeIsUnification(entry.TypeVar) {
			return true
		}
	}

	return false
}

// ConstraintTracker tracks the constraints for a set of type variables. It is
// used by the constraint solver to solve for the type of each type variable.
type ConstraintTracker struct {
	constraintSets []*ConstraintSet
}

func NewConstraintTracker() *ConstraintTracker {
	// Co-allocate the tracker, its initial set, and the slice backing: one
	// heap object instead of three, on a constructor that runs for nearly
	// every call-site validation.
	combined := &struct {
		tracker ConstraintTracker
		set     ConstraintSet
		sets    [1]*ConstraintSet
	}{}
	combined.sets[0] = &combined.set
	combined.tracker.constraintSets = combined.sets[:]
	return &combined.tracker
}

func (t *ConstraintTracker) Clone() *ConstraintTracker {
	// The sets share one backing array, and the tracker is built directly --
	// going through NewConstraintTracker here would allocate a constraint set
	// only to overwrite it.
	backing := make([]ConstraintSet, len(t.constraintSets))
	sets := make([]*ConstraintSet, 0, len(t.constraintSets))
	for i, set := range t.constraintSets {
		set.cloneInto(&backing[i])
		sets = append(sets, &backing[i])
	}
	return &ConstraintTracker{constraintSets: sets}
}

func (t *ConstraintTracker) CloneWithSignature(scopeId TypeVarScopeId) *ConstraintTracker {
	cloned := t.Clone()

	// `if (scopeId)` -- the empty string is falsy, and TypeVarScopeId is a
	// string alias, so an empty scope id skips this entirely.
	if scopeId != "" {
		filteredSets := []*ConstraintSet{}
		for _, context := range t.constraintSets {
			if context.HasScopeId(scopeId) {
				filteredSets = append(filteredSets, context)
			}
		}

		if len(filteredSets) > 0 {
			// The original assigns the *unfiltered* sets from this tracker,
			// not their clones, so the clone shares them with the original.
			cloned.constraintSets = filteredSets
		} else {
			for _, context := range cloned.constraintSets {
				context.AddScopeId(scopeId)
			}
		}
	}

	return cloned
}

// CopyFromClone copies a cloned type var context back into this object.
func (t *ConstraintTracker) CopyFromClone(clone *ConstraintTracker) {
	backing := make([]ConstraintSet, len(clone.constraintSets))
	sets := make([]*ConstraintSet, 0, len(clone.constraintSets))
	for i, context := range clone.constraintSets {
		context.cloneInto(&backing[i])
		sets = append(sets, &backing[i])
	}
	t.constraintSets = sets
}

func (t *ConstraintTracker) CopyBounds(entry *TypeVarConstraints) {
	for _, set := range t.constraintSets {
		set.SetBounds(entry.TypeVar, entry.LowerBound, entry.UpperBound, entry.RetainLiterals)
	}
}

// AddConstraintSets copies the specified constraint sets into this type var
// context.
func (t *ConstraintTracker) AddConstraintSets(contexts []*ConstraintSet) {
	common.Assert(len(contexts) > 0, "")

	// The original's comment: limit the number of constraint sets. There are
	// rare circumstances where this can grow to unbounded numbers and exhaust
	// memory.
	if len(contexts) < maxConstraintSetCount {
		t.constraintSets = append([]*ConstraintSet{}, contexts...)
	}
}

func (t *ConstraintTracker) IsSame(other *ConstraintTracker) bool {
	if len(other.constraintSets) != len(t.constraintSets) {
		return false
	}

	for index, set := range t.constraintSets {
		if !set.IsSame(other.constraintSets[index]) {
			return false
		}
	}

	return true
}

func (t *ConstraintTracker) IsEmpty() bool {
	for _, set := range t.constraintSets {
		if !set.IsEmpty() {
			return false
		}
	}
	return true
}

func (t *ConstraintTracker) SetBounds(typeVar *TypeVarType, lowerBound Type, upperBound Type, retainLiterals bool) {
	for _, set := range t.constraintSets {
		set.SetBounds(typeVar, lowerBound, upperBound, retainLiterals)
	}
}

func (t *ConstraintTracker) GetScore() float64 {
	total := 0.0

	for _, set := range t.constraintSets {
		total += set.GetScore()
	}

	// Return the average score among all constraint sets.
	return total / float64(len(t.constraintSets))
}

func (t *ConstraintTracker) GetMainConstraintSet() *ConstraintSet { return t.constraintSets[0] }

func (t *ConstraintTracker) GetConstraintSets() []*ConstraintSet { return t.constraintSets }

func (t *ConstraintTracker) DoForEachConstraintSet(callback func(constraintSet *ConstraintSet, index int)) {
	for index, set := range t.GetConstraintSets() {
		callback(set, index)
	}
}

func (t *ConstraintTracker) GetConstraintSet(index int) *ConstraintSet {
	common.Assert(index >= 0 && index < len(t.constraintSets), "")
	return t.constraintSets[index]
}
