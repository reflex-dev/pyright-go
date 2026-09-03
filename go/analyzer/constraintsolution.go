/*
 * constraintsolution.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Transliterated from analyzer/constraintSolution.ts (pyright 1.1.412).
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
)

// ConstraintSolutionSet records the types associated with a set of type
// variables.
type ConstraintSolutionSet struct {
	// typeVarMap is indexed by TypeVar ID. A key can be present with a nil
	// value, which HasType distinguishes from absent, so this cannot collapse
	// into a plain lookup.
	// Held by value: constraint solution sets are created tens of millions
	// of times per large-project run, and a pointer field made each one two
	// allocations instead of one. The zero OrderedMap is ready to use.
	typeVarMap common.OrderedMap[string, Type]
}

func NewConstraintSolutionSet() *ConstraintSolutionSet {
	return &ConstraintSolutionSet{}
}

func (s *ConstraintSolutionSet) IsEmpty() bool {
	return s.typeVarMap.Size() == 0
}

// GetType corresponds to ConstraintSolutionSet.getType. It returns nil where
// the TypeScript returns undefined.
func (s *ConstraintSolutionSet) GetType(typeVar *TypeVarType) Type {
	key := TypeVarTypeGetNameWithScope(typeVar)
	t, _ := s.typeVarMap.Get(key)
	return t
}

func (s *ConstraintSolutionSet) SetType(typeVar *TypeVarType, t Type) {
	key := TypeVarTypeGetNameWithScope(typeVar)
	s.typeVarMap.Set(key, t)
}

func (s *ConstraintSolutionSet) HasType(typeVar *TypeVarType) bool {
	key := TypeVarTypeGetNameWithScope(typeVar)
	return s.typeVarMap.Has(key)
}

// DoForEachTypeVar corresponds to ConstraintSolutionSet.doForEachTypeVar. Note
// that it skips entries whose value is undefined.
func (s *ConstraintSolutionSet) DoForEachTypeVar(callback func(t Type, typeVarID string)) {
	s.typeVarMap.ForEach(func(t Type, key string) {
		if t != nil {
			callback(t, key)
		}
	})
}

// ConstraintSolution corresponds to the class of the same name.
type ConstraintSolution struct {
	solutionSets []*ConstraintSolutionSet
}

// NewConstraintSolution corresponds to the ConstraintSolution constructor. A
// nil or empty solutionSets yields a single empty set, as in the original.
func NewConstraintSolution(solutionSets []*ConstraintSolutionSet) *ConstraintSolution {
	if len(solutionSets) > 0 {
		return &ConstraintSolution{solutionSets: sliceCopy(solutionSets)}
	}
	// The empty-solution path runs tens of millions of times; co-allocating
	// the solution, its single set, and the slice backing turns three heap
	// objects into one. The set's address escapes only alongside the
	// solution's, so their lifetimes already coincide.
	combined := &struct {
		sol  ConstraintSolution
		set  ConstraintSolutionSet
		sets [1]*ConstraintSolutionSet
	}{}
	combined.sets[0] = &combined.set
	combined.sol.solutionSets = combined.sets[:]
	return &combined.sol
}

func (c *ConstraintSolution) IsEmpty() bool {
	for _, set := range c.solutionSets {
		if !set.IsEmpty() {
			return false
		}
	}
	return true
}

func (c *ConstraintSolution) SetType(typeVar *TypeVarType, t Type) {
	for _, set := range c.solutionSets {
		set.SetType(typeVar, t)
	}
}

func (c *ConstraintSolution) GetMainSolutionSet() *ConstraintSolutionSet {
	return c.GetSolutionSet(0)
}

func (c *ConstraintSolution) GetSolutionSets() []*ConstraintSolutionSet {
	return c.solutionSets
}

func (c *ConstraintSolution) DoForEachSolutionSet(callback func(solutionSet *ConstraintSolutionSet, index int)) {
	for index, set := range c.GetSolutionSets() {
		callback(set, index)
	}
}

func (c *ConstraintSolution) GetSolutionSet(index int) *ConstraintSolutionSet {
	assert(index >= 0 && index < len(c.solutionSets), "")
	return c.solutionSets[index]
}
