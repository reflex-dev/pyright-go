/*
 * orderedmap.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Stand-ins for JavaScript's Map and Set.
 *
 * These have no counterpart in the pyright sources -- they exist because Go's
 * built-in map deliberately randomizes iteration order and JavaScript's Map and
 * Set iterate in insertion order. Pyright depends on that: symbol tables are
 * walked to produce completion lists, diagnostics and printed types, and a
 * different order would change user-visible output (and make it unstable
 * between runs).
 *
 * Semantics match JavaScript: Set on an existing key overwrites the value and
 * leaves the key in its original position; Delete removes it entirely.
 */

package common

// OrderedMap is an insertion-ordered map, standing in for a JavaScript Map.
// Every operation is O(1) amortized, as JavaScript's are.
//
// This file used to say that Delete could afford to be O(n) because "pyright
// deletes from these maps rarely (symbol tables are built up and then read)".
// That is true of symbol tables and false of the one map that matters: the type
// evaluator's cache. The speculative type tracker writes into it and then undoes
// every write when the speculative context ends, which happens on every overload
// attempt and every bidirectional inference retry. Against a 1,193-file project
// that single linear scan was **33% of total run time** -- 34 seconds out of 80.
//
// How the order is kept without paying for it: `keys` is append-only and may
// hold stale slots, `index` maps a live key to its slot, and `dead` counts the
// slots that no longer correspond to anything. A slot at position i is live only
// when `index[k] == i`, which is also what makes delete-then-reinsert land at
// the end the way JavaScript does rather than in the original position.
// Compaction runs when more than half the slots are stale, so the amortized cost
// of a delete stays constant.
//
// `index` is built on the first Delete rather than maintained from the start.
// Most of these maps -- every symbol table, and there is one per scope -- never
// see a delete at all, and for those the second map would be a second allocation
// and a second write per Set for nothing. Absent `index` therefore means "no key
// has ever been deleted", which makes every slot live by definition.
type OrderedMap[K comparable, V any] struct {
	keys  []K
	items map[K]V
	index map[K]int

	// dead is the number of entries in keys that no longer name a live key.
	dead int

	// iterDepth is non-zero while a ForEach is running. Compaction replaces the
	// keys slice and renumbers index, which would invalidate an in-flight walk,
	// so it is deferred until the walk finishes.
	iterDepth int
}

// NewOrderedMap returns an empty map, like `new Map()`. index stays nil until
// something is deleted; see the type comment.
func NewOrderedMap[K comparable, V any]() *OrderedMap[K, V] {
	return &OrderedMap[K, V]{items: map[K]V{}}
}

func (m *OrderedMap[K, V]) ensure() {
	if m.items == nil {
		m.items = map[K]V{}
	}
}

// buildIndex populates index from the live keys. It runs at most once per map,
// on the first Delete.
func (m *OrderedMap[K, V]) buildIndex() {
	m.index = make(map[K]int, len(m.keys))
	for i, k := range m.keys {
		m.index[k] = i
	}
}

// compact rebuilds keys with the stale slots removed. It is O(n) and runs at
// most once per n deletions.
func (m *OrderedMap[K, V]) compact() {
	live := make([]K, 0, len(m.items))
	for i, k := range m.keys {
		if pos, ok := m.index[k]; ok && pos == i {
			live = append(live, k)
		}
	}

	m.keys = live
	for i, k := range live {
		m.index[k] = i
	}
	m.dead = 0
}

// isLive reports whether the slot at position i holds a live key. A key that was
// deleted is absent from index; one that was deleted and re-added has an index
// pointing at its newer slot.
func (m *OrderedMap[K, V]) isLive(i int, k K) bool {
	if m.index == nil {
		return true
	}
	pos, ok := m.index[k]
	return ok && pos == i
}

// Get corresponds to Map.get. The second result is false where the TypeScript
// would see `undefined`.
func (m *OrderedMap[K, V]) Get(key K) (V, bool) {
	v, ok := m.items[key]
	return v, ok
}

// Set corresponds to Map.set.
func (m *OrderedMap[K, V]) Set(key K, value V) {
	m.ensure()
	if _, exists := m.items[key]; !exists {
		if m.index != nil {
			m.index[key] = len(m.keys)
		}
		m.keys = append(m.keys, key)
	}
	m.items[key] = value
}

// Has corresponds to Map.has.
func (m *OrderedMap[K, V]) Has(key K) bool {
	_, ok := m.items[key]
	return ok
}

// Delete corresponds to Map.delete, returning whether the key was present.
func (m *OrderedMap[K, V]) Delete(key K) bool {
	if _, ok := m.items[key]; !ok {
		return false
	}

	if m.index == nil {
		m.buildIndex()
	}

	delete(m.items, key)
	delete(m.index, key)
	m.dead++

	// Compact once the stale slots outnumber the live ones, which bounds both
	// the memory held by keys and the work any single walk has to skip.
	if m.iterDepth == 0 && m.dead > len(m.items) && m.dead > 8 {
		m.compact()
	}

	return true
}

// Size corresponds to Map.size.
func (m *OrderedMap[K, V]) Size() int {
	return len(m.items)
}

// Keys returns the keys in insertion order. The result aliases the map's own
// storage, so callers must not modify it.
func (m *OrderedMap[K, V]) Keys() []K {
	// Compacting here keeps the aliasing contract: callers get the backing slice
	// itself, so it has to hold exactly the live keys.
	if m.dead > 0 && m.iterDepth == 0 {
		m.compact()
	}
	return m.keys
}

// Values returns the values in insertion order.
func (m *OrderedMap[K, V]) Values() []V {
	out := make([]V, 0, len(m.items))
	for i, k := range m.keys {
		if m.isLive(i, k) {
			out = append(out, m.items[k])
		}
	}
	return out
}

// ForEach corresponds to Map.forEach, iterating in insertion order.
//
// JavaScript's forEach does not visit an entry deleted before it is reached, and
// does visit one added during the walk. Reading the liveness of each slot as the
// walk arrives at it -- rather than snapshotting up front -- is what reproduces
// that.
func (m *OrderedMap[K, V]) ForEach(fn func(value V, key K)) {
	m.iterDepth++
	defer func() {
		m.iterDepth--
		if m.iterDepth == 0 && m.dead > len(m.items) && m.dead > 8 {
			m.compact()
		}
	}()

	for i := 0; i < len(m.keys); i++ {
		k := m.keys[i]
		if !m.isLive(i, k) {
			continue
		}
		fn(m.items[k], k)
	}
}

// Clear corresponds to Map.clear.
func (m *OrderedMap[K, V]) Clear() {
	m.keys = nil
	m.items = map[K]V{}
	m.index = nil
	m.dead = 0
}

// Clone returns a shallow copy, standing in for `new Map(other)`.
func (m *OrderedMap[K, V]) Clone() *OrderedMap[K, V] {
	out := NewOrderedMap[K, V]()
	for i, k := range m.keys {
		if m.isLive(i, k) {
			out.Set(k, m.items[k])
		}
	}
	return out
}

// OrderedSet is an insertion-ordered set, standing in for a JavaScript Set.
type OrderedSet[T comparable] struct {
	m *OrderedMap[T, struct{}]
}

// NewOrderedSet returns an empty set, like `new Set()`.
func NewOrderedSet[T comparable]() *OrderedSet[T] {
	return &OrderedSet[T]{m: NewOrderedMap[T, struct{}]()}
}

// NewOrderedSetFrom returns a set containing values, like `new Set(values)`.
func NewOrderedSetFrom[T comparable](values []T) *OrderedSet[T] {
	s := NewOrderedSet[T]()
	for _, v := range values {
		s.Add(v)
	}
	return s
}

// Add corresponds to Set.add.
func (s *OrderedSet[T]) Add(value T) {
	s.m.Set(value, struct{}{})
}

// Has corresponds to Set.has.
func (s *OrderedSet[T]) Has(value T) bool {
	return s.m.Has(value)
}

// Delete corresponds to Set.delete.
func (s *OrderedSet[T]) Delete(value T) bool {
	return s.m.Delete(value)
}

// Size corresponds to Set.size.
func (s *OrderedSet[T]) Size() int {
	return s.m.Size()
}

// Values returns the members in insertion order.
func (s *OrderedSet[T]) Values() []T {
	return s.m.Keys()
}

// ForEach corresponds to Set.forEach.
func (s *OrderedSet[T]) ForEach(fn func(value T)) {
	// Delegated rather than ranging over Keys() so that a member deleted by fn
	// before the walk reaches it is not visited, which is what Set.forEach does.
	s.m.ForEach(func(_ struct{}, value T) { fn(value) })
}

// Clear corresponds to Set.clear.
func (s *OrderedSet[T]) Clear() {
	s.m.Clear()
}
