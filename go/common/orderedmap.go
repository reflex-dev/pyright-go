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
//
// Delete is O(n) in the number of keys, where JavaScript's is amortized O(1).
// Pyright deletes from these maps rarely (symbol tables are built up and then
// read), so the simpler representation is worth more than the asymptotics.
type OrderedMap[K comparable, V any] struct {
	keys  []K
	items map[K]V
}

// NewOrderedMap returns an empty map, like `new Map()`.
func NewOrderedMap[K comparable, V any]() *OrderedMap[K, V] {
	return &OrderedMap[K, V]{items: map[K]V{}}
}

func (m *OrderedMap[K, V]) ensure() {
	if m.items == nil {
		m.items = map[K]V{}
	}
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
	delete(m.items, key)
	for i, k := range m.keys {
		if k == key {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			break
		}
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
	return m.keys
}

// Values returns the values in insertion order.
func (m *OrderedMap[K, V]) Values() []V {
	out := make([]V, 0, len(m.keys))
	for _, k := range m.keys {
		out = append(out, m.items[k])
	}
	return out
}

// ForEach corresponds to Map.forEach, iterating in insertion order.
func (m *OrderedMap[K, V]) ForEach(fn func(value V, key K)) {
	for _, k := range m.keys {
		fn(m.items[k], k)
	}
}

// Clear corresponds to Map.clear.
func (m *OrderedMap[K, V]) Clear() {
	m.keys = nil
	m.items = map[K]V{}
}

// Clone returns a shallow copy, standing in for `new Map(other)`.
func (m *OrderedMap[K, V]) Clone() *OrderedMap[K, V] {
	out := NewOrderedMap[K, V]()
	for _, k := range m.keys {
		out.Set(k, m.items[k])
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
	for _, v := range s.m.Keys() {
		fn(v)
	}
}
