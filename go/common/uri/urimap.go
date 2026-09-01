/*
 * urimap.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Map specifically made to support a URI as a key.
 *
 * Transliterated from common/uri/uriMap.ts (pyright 1.1.412).
 *
 * The original implements the whole ES Map interface, including three iterator
 * classes, because it is declared as `implements Map<Uri, T>`. Only the eight
 * methods its callers use are here; the iterator plumbing has no equivalent and
 * no consumer.
 */

package uri

import "github.com/microsoft/pyright/go/common"

// UriMap is a map keyed by a Uri's key, which keeps the Uri itself so that
// iteration can hand it back.
type UriMap[T any] struct {
	keys   *common.OrderedMap[string, Uri]
	values *common.OrderedMap[string, T]
}

func NewUriMap[T any]() *UriMap[T] {
	return &UriMap[T]{
		keys:   common.NewOrderedMap[string, Uri](),
		values: common.NewOrderedMap[string, T](),
	}
}

func (m *UriMap[T]) Size() int { return m.values.Size() }

func (m *UriMap[T]) Clear() {
	m.keys.Clear()
	m.values.Clear()
}

// Get takes a nilable key, as the original does, and answers the zero value and
// false where the original answers undefined.
func (m *UriMap[T]) Get(key Uri) (T, bool) {
	if key == nil {
		var zero T
		return zero, false
	}
	return m.values.Get(key.Key())
}

// Set ignores a nil key, as the original does.
func (m *UriMap[T]) Set(key Uri, value T) {
	if key == nil {
		return
	}
	m.keys.Set(key.Key(), key)
	m.values.Set(key.Key(), value)
}

func (m *UriMap[T]) Has(key Uri) bool {
	return m.values.Has(key.Key())
}

func (m *UriMap[T]) Delete(key Uri) bool {
	m.keys.Delete(key.Key())
	return m.values.Delete(key.Key())
}

// Keys returns the Uris in insertion order.
func (m *UriMap[T]) Keys() []Uri { return m.keys.Values() }

func (m *UriMap[T]) Values() []T { return m.values.Values() }

// ForEach visits in insertion order, with the callback argument order the
// original uses.
func (m *UriMap[T]) ForEach(fn func(value T, key Uri)) {
	for _, key := range m.keys.Values() {
		value, _ := m.values.Get(key.Key())
		fn(value, key)
	}
}
