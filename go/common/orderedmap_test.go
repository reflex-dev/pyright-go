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

// TestOrderedMapSmallModeSemantics pins the JavaScript Map semantics in the
// small representation and across the promotion boundary.
func TestOrderedMapSmallModeSemantics(t *testing.T) {
	// Delete + reinsert lands at the end, small mode.
	m := NewOrderedMap[string, int]()
	for _, k := range []string{"a", "b", "c"} {
		m.Set(k, 1)
	}
	m.Delete("a")
	m.Set("a", 2)
	if got := m.Keys(); got[0] != "b" || got[1] != "c" || got[2] != "a" {
		t.Fatalf("small-mode reinsert order = %v", got)
	}

	// Overwrite keeps position, small mode.
	m.Set("b", 9)
	if got := m.Keys(); got[0] != "b" {
		t.Fatalf("small-mode overwrite moved key: %v", got)
	}
	if v, _ := m.Get("b"); v != 9 {
		t.Fatalf("small-mode overwrite lost value")
	}

	// Promotion preserves order and contents.
	for i := 0; i < 20; i++ {
		m.Set(string(rune('h'+i)), i)
	}
	keys := m.Keys()
	if keys[0] != "b" || keys[1] != "c" || keys[2] != "a" || len(keys) != 23 {
		t.Fatalf("promotion broke order: %v", keys[:4])
	}

	// A walk that mutates in small mode: deletes ahead are skipped, adds are
	// visited, nothing is visited twice.
	m2 := NewOrderedMap[string, int]()
	for _, k := range []string{"p", "q", "r", "s"} {
		m2.Set(k, 1)
	}
	var visited []string
	m2.ForEach(func(_ int, k string) {
		visited = append(visited, k)
		if k == "p" {
			m2.Delete("r") // ahead: must not be visited
			m2.Set("t", 1) // added: must be visited
		}
	})
	want := []string{"p", "q", "s", "t"}
	if len(visited) != len(want) {
		t.Fatalf("mutating small walk visited %v", visited)
	}
	for i := range want {
		if visited[i] != want[i] {
			t.Fatalf("mutating small walk visited %v, want %v", visited, want)
		}
	}
}

// TestOrderedMapMatchesReferenceModel drives random operation sequences
// through the map and a naive slice-based model of a JavaScript Map, at sizes
// straddling the small/map boundary.
func TestOrderedMapMatchesReferenceModel(t *testing.T) {
	type refEntry struct {
		k string
		v int
	}
	seed := uint64(12345)
	next := func(n int) int {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int((seed >> 33) % uint64(n))
	}

	for trial := 0; trial < 200; trial++ {
		m := NewOrderedMap[string, int]()
		var ref []refEntry
		refSet := func(k string, v int) {
			for i := range ref {
				if ref[i].k == k {
					ref[i].v = v
					return
				}
			}
			ref = append(ref, refEntry{k, v})
		}
		refDelete := func(k string) {
			for i := range ref {
				if ref[i].k == k {
					ref = append(ref[:i], ref[i+1:]...)
					return
				}
			}
		}

		keyPool := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n"}
		for op := 0; op < 60; op++ {
			k := keyPool[next(len(keyPool))]
			switch next(3) {
			case 0, 1:
				v := next(1000)
				m.Set(k, v)
				refSet(k, v)
			case 2:
				m.Delete(k)
				refDelete(k)
			}

			if m.Size() != len(ref) {
				t.Fatalf("trial %d op %d: size %d, want %d", trial, op, m.Size(), len(ref))
			}
			keys := m.Keys()
			for i, e := range ref {
				if keys[i] != e.k {
					t.Fatalf("trial %d op %d: key order %v, want %v", trial, op, keys, ref)
				}
				if v, ok := m.Get(e.k); !ok || v != e.v {
					t.Fatalf("trial %d op %d: Get(%q) = %d,%v want %d", trial, op, e.k, v, ok, e.v)
				}
			}
		}
	}
}
