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

// smallCap is the size up to which an OrderedMap stays in its small
// representation. Eight linear key comparisons cost less than one hash on
// these key types, and the overwhelming majority of pyright's maps -- scope
// symbol tables, per-call trackers, narrowed-entry maps -- never grow past it.
const smallCap = 8

type orderedMapEntry[K comparable, V any] struct {
	key   K
	value V
}

// OrderedMap is an insertion-ordered map, standing in for a JavaScript Map.
//
// It has two representations. Small maps -- and the profile says most maps
// here die small -- live in a single entries slice, scanned linearly; nothing
// else is allocated, and a map that is created and never written (a constant
// pattern in the evaluator) allocates nothing at all. Crossing smallCap
// promotes to the map representation below, as does any mutation while a walk
// is in flight, so the subtle iteration semantics have a single home.
//
// The map representation: `keys` is append-only and may hold stale slots,
// `items` holds the live entries, `index` maps a live key to its slot, and
// `dead` counts slots that no longer correspond to anything. A slot at
// position i is live only when `index[k] == i`, which is also what makes
// delete-then-reinsert land at the end the way JavaScript does. Compaction
// runs when more than half the slots are stale, keeping every operation O(1)
// amortized, as JavaScript's are. Delete being O(1) matters: the speculative
// type tracker undoes its type-cache writes on every overload attempt, and an
// O(n) delete was once 33% of a whole-project run.
//
// `index` is built on the first Delete rather than maintained from the start.
// Most maps never see a delete, and for those the second map would be a
// second allocation and a second write per Set for nothing. Absent `index`
// therefore means "no key has ever been deleted", which makes every slot live
// by definition.
type OrderedMap[K comparable, V any] struct {
	// entries is the small representation: exactly the live entries, in
	// insertion order. It is in use whenever items is nil.
	entries []orderedMapEntry[K, V]

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

// NewOrderedMap returns an empty map, like `new Map()`. Nothing is allocated
// until the first Set.
func NewOrderedMap[K comparable, V any]() *OrderedMap[K, V] {
	return &OrderedMap[K, V]{}
}

// small reports whether the map is in its small representation.
func (m *OrderedMap[K, V]) small() bool { return m.items == nil }

// promote moves the map to the map representation.
func (m *OrderedMap[K, V]) promote() {
	m.items = make(map[K]V, len(m.entries)*2)
	m.keys = make([]K, 0, len(m.entries)*2)
	for _, e := range m.entries {
		m.keys = append(m.keys, e.key)
		m.items[e.key] = e.value
	}
	m.entries = nil
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
	if m.small() {
		for i := range m.entries {
			if m.entries[i].key == key {
				return m.entries[i].value, true
			}
		}
		var zero V
		return zero, false
	}
	v, ok := m.items[key]
	return v, ok
}

// Set corresponds to Map.set.
func (m *OrderedMap[K, V]) Set(key K, value V) {
	if m.small() {
		for i := range m.entries {
			if m.entries[i].key == key {
				m.entries[i].value = value
				return
			}
		}
		// A mutation during an in-flight walk goes through the map
		// representation, where the liveness machinery gives it JavaScript's
		// iteration semantics.
		if len(m.entries) < smallCap && m.iterDepth == 0 {
			if m.entries == nil {
				m.entries = make([]orderedMapEntry[K, V], 0, 4)
			}
			m.entries = append(m.entries, orderedMapEntry[K, V]{key, value})
			return
		}
		m.promote()
	}

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
	if m.small() {
		for i := range m.entries {
			if m.entries[i].key == key {
				return true
			}
		}
		return false
	}
	_, ok := m.items[key]
	return ok
}

// Delete corresponds to Map.delete, returning whether the key was present.
func (m *OrderedMap[K, V]) Delete(key K) bool {
	if m.small() {
		if m.iterDepth > 0 {
			// See Set: iteration-time mutations use the map representation.
			m.promote()
		} else {
			for i := range m.entries {
				if m.entries[i].key == key {
					m.entries = append(m.entries[:i], m.entries[i+1:]...)
					return true
				}
			}
			return false
		}
	}

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
	if m.small() {
		return len(m.entries)
	}
	return len(m.items)
}

// Keys returns the keys in insertion order. The result may alias the map's own
// storage, so callers must not modify it.
func (m *OrderedMap[K, V]) Keys() []K {
	if m.small() {
		out := make([]K, len(m.entries))
		for i := range m.entries {
			out[i] = m.entries[i].key
		}
		return out
	}
	// Compacting here keeps the aliasing contract: callers get the backing slice
	// itself, so it has to hold exactly the live keys.
	if m.dead > 0 && m.iterDepth == 0 {
		m.compact()
	}
	return m.keys
}

// Values returns the values in insertion order.
func (m *OrderedMap[K, V]) Values() []V {
	if m.small() {
		out := make([]V, len(m.entries))
		for i := range m.entries {
			out[i] = m.entries[i].value
		}
		return out
	}
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
// that. In the small representation the walk re-checks membership per step, and
// any mutation during the walk promotes to the map representation (see Set and
// Delete), whose liveness machinery the loop below then follows: after a
// promotion the remaining small entries have identical positions in keys, so
// the index carries over.
func (m *OrderedMap[K, V]) ForEach(fn func(value V, key K)) {
	m.iterDepth++
	defer func() {
		m.iterDepth--
		if m.iterDepth == 0 && m.dead > len(m.items) && m.dead > 8 {
			m.compact()
		}
	}()

	// The small representation cannot change under the walk: any mutation
	// from inside fn promotes first (see Set and Delete). Promotion preserves
	// positions -- the small entries become keys[0..n-1] in order -- so the
	// walk resumes in the map representation right after the last visited
	// entry, with the liveness checks picking up whatever fn changed.
	visited := 0
	for m.small() && visited < len(m.entries) {
		e := m.entries[visited]
		visited++
		fn(e.value, e.key)
	}
	if m.small() {
		return
	}

	for i := visited; i < len(m.keys); i++ {
		k := m.keys[i]
		if !m.isLive(i, k) {
			continue
		}
		fn(m.items[k], k)
	}
}

// Clear corresponds to Map.clear.
func (m *OrderedMap[K, V]) Clear() {
	m.entries = nil
	m.keys = nil
	m.items = nil
	m.index = nil
	m.dead = 0
}

// Clone returns a shallow copy, standing in for `new Map(other)`.
func (m *OrderedMap[K, V]) Clone() *OrderedMap[K, V] {
	out := NewOrderedMap[K, V]()
	if m.small() {
		if len(m.entries) > 0 {
			out.entries = append(make([]orderedMapEntry[K, V], 0, len(m.entries)), m.entries...)
		}
		return out
	}
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
