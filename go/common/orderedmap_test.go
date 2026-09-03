package common

import (
	"fmt"
	"reflect"
	"testing"
)

// These pin the JavaScript Map semantics the tombstone representation has to
// preserve. Before it, Delete was a linear scan and every one of these passed
// trivially; the representation is what makes them worth asserting.

func keysOf(m *OrderedMap[int, string]) []int {
	out := []int{}
	m.ForEach(func(_ string, k int) { out = append(out, k) })
	return out
}

func TestOrderedMapDeletePreservesOrder(t *testing.T) {
	m := NewOrderedMap[int, string]()
	for i := 1; i <= 5; i++ {
		m.Set(i, fmt.Sprint(i))
	}

	m.Delete(2)
	m.Delete(4)

	if want := []int{1, 3, 5}; !reflect.DeepEqual(keysOf(m), want) {
		t.Errorf("ForEach order = %v, want %v", keysOf(m), want)
	}
	if !reflect.DeepEqual(m.Keys(), []int{1, 3, 5}) {
		t.Errorf("Keys() = %v, want [1 3 5]", m.Keys())
	}
	if m.Size() != 3 {
		t.Errorf("Size() = %d, want 3", m.Size())
	}
}

// A key deleted and then set again goes to the end, as `map.delete(k);
// map.set(k, v)` does in JavaScript -- not back to its original position. The
// stale slot left behind must not be visited.
func TestOrderedMapReinsertMovesToEnd(t *testing.T) {
	m := NewOrderedMap[int, string]()
	m.Set(1, "a")
	m.Set(2, "b")
	m.Set(3, "c")

	m.Delete(1)
	m.Set(1, "a2")

	if want := []int{2, 3, 1}; !reflect.DeepEqual(keysOf(m), want) {
		t.Errorf("order after reinsert = %v, want %v", keysOf(m), want)
	}
	if v, _ := m.Get(1); v != "a2" {
		t.Errorf("Get(1) = %q, want %q", v, "a2")
	}
}

// Set on an existing key overwrites the value and leaves the key where it is.
func TestOrderedMapSetExistingKeepsPosition(t *testing.T) {
	m := NewOrderedMap[int, string]()
	m.Set(1, "a")
	m.Set(2, "b")
	m.Set(1, "a2")

	if want := []int{1, 2}; !reflect.DeepEqual(keysOf(m), want) {
		t.Errorf("order = %v, want %v", keysOf(m), want)
	}
}

// JavaScript's forEach does not visit an entry deleted before the walk reaches
// it.
func TestOrderedMapForEachSkipsEntriesDeletedDuringWalk(t *testing.T) {
	m := NewOrderedMap[int, string]()
	for i := 1; i <= 4; i++ {
		m.Set(i, fmt.Sprint(i))
	}

	visited := []int{}
	m.ForEach(func(_ string, k int) {
		visited = append(visited, k)
		if k == 1 {
			m.Delete(3)
		}
	})

	if want := []int{1, 2, 4}; !reflect.DeepEqual(visited, want) {
		t.Errorf("visited = %v, want %v", visited, want)
	}
}

// Compaction must not run underneath an in-flight walk: it renumbers the index
// the walk is reading. Deleting far more than the compaction threshold from
// inside ForEach is what would trip it.
func TestOrderedMapForEachSurvivesMassDeletion(t *testing.T) {
	m := NewOrderedMap[int, string]()
	for i := 0; i < 200; i++ {
		m.Set(i, fmt.Sprint(i))
	}

	visited := []int{}
	m.ForEach(func(_ string, k int) {
		visited = append(visited, k)
		if k%2 == 1 {
			m.Delete(k)
		}
	})

	if len(visited) != 200 {
		t.Fatalf("visited %d entries, want 200", len(visited))
	}
	for i, k := range visited {
		if k != i {
			t.Fatalf("visited[%d] = %d, want %d", i, k, i)
		}
	}
	if m.Size() != 100 {
		t.Errorf("Size() = %d, want 100", m.Size())
	}
	if want := 100; len(m.Keys()) != want {
		t.Errorf("len(Keys()) = %d, want %d", len(m.Keys()), want)
	}
}

// Churn: repeated add/delete cycles must not let the keys slice grow without
// bound, which is the thing compaction exists to prevent.
func TestOrderedMapChurnDoesNotGrowStorage(t *testing.T) {
	m := NewOrderedMap[int, string]()
	for i := 0; i < 100000; i++ {
		m.Set(i, "x")
		m.Delete(i)
	}

	if m.Size() != 0 {
		t.Fatalf("Size() = %d, want 0", m.Size())
	}
	if len(m.keys) > 64 {
		t.Errorf("keys slice held %d stale slots after churn", len(m.keys))
	}
}

func TestOrderedMapValuesAndClone(t *testing.T) {
	m := NewOrderedMap[int, string]()
	m.Set(1, "a")
	m.Set(2, "b")
	m.Set(3, "c")
	m.Delete(2)

	if want := []string{"a", "c"}; !reflect.DeepEqual(m.Values(), want) {
		t.Errorf("Values() = %v, want %v", m.Values(), want)
	}

	clone := m.Clone()
	if want := []int{1, 3}; !reflect.DeepEqual(clone.Keys(), want) {
		t.Errorf("Clone().Keys() = %v, want %v", clone.Keys(), want)
	}

	clone.Set(4, "d")
	if m.Has(4) {
		t.Error("Clone is not independent of the original")
	}
}

func TestOrderedMapClear(t *testing.T) {
	m := NewOrderedMap[int, string]()
	m.Set(1, "a")
	m.Delete(1)
	m.Set(2, "b")
	m.Clear()

	if m.Size() != 0 || len(m.Keys()) != 0 {
		t.Errorf("after Clear: Size()=%d Keys()=%v", m.Size(), m.Keys())
	}

	m.Set(9, "z")
	if want := []int{9}; !reflect.DeepEqual(m.Keys(), want) {
		t.Errorf("Keys() after reuse = %v, want %v", m.Keys(), want)
	}
}

// The zero value has to work: several fields in the analyzer declare an
// OrderedMap without calling NewOrderedMap.
func TestOrderedMapZeroValue(t *testing.T) {
	var m OrderedMap[string, int]
	m.Set("a", 1)
	m.Set("b", 2)
	m.Delete("a")

	if want := []string{"b"}; !reflect.DeepEqual(m.Keys(), want) {
		t.Errorf("Keys() = %v, want %v", m.Keys(), want)
	}
}

func TestOrderedSetForEachSkipsDeleted(t *testing.T) {
	s := NewOrderedSetFrom([]int{1, 2, 3, 4})

	visited := []int{}
	s.ForEach(func(v int) {
		visited = append(visited, v)
		if v == 1 {
			s.Delete(3)
		}
	})

	if want := []int{1, 2, 4}; !reflect.DeepEqual(visited, want) {
		t.Errorf("visited = %v, want %v", visited, want)
	}
}

// The regression this whole representation exists for: N deletes must be O(N),
// not O(N^2). At 200k entries the old linear scan took minutes.
func BenchmarkOrderedMapDelete(b *testing.B) {
	for n := 0; n < b.N; n++ {
		m := NewOrderedMap[int, string]()
		const count = 200000
		for i := 0; i < count; i++ {
			m.Set(i, "x")
		}
		for i := 0; i < count; i++ {
			m.Delete(i)
		}
	}
}
