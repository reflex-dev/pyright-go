/*
 * cachemanager.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * A singleton that tracks the size of caches and empties them if memory usage
 * approaches the max heap space.
 *
 * Transliterated from analyzer/cacheManager.ts (pyright 1.1.412).
 *
 * PARTIAL, and the missing part is the cross-worker half of the measurement.
 * The original shares a usage figure across worker threads with a
 * SharedArrayBuffer so a background thread can see the main thread's heap;
 * there are no worker threads here, so what it reads is this process's heap and
 * nothing else -- which is exactly what its `_getTotalHeapUsage` reduces to when
 * `_maxWorkers` is zero.
 *
 * The ratio itself is real. It needs a heap *limit* to divide by, and Go has
 * one whenever GOMEMLIMIT (or debug.SetMemoryLimit) is set -- that is the
 * counterpart of the `--max-old-space-size` the original's own CLI passes to
 * Node. With no limit set, Go's soft limit is math.MaxInt64, there is genuinely
 * nothing to be a ratio of, and GetUsedHeapRatio answers -1: the same value the
 * original gives while tracking is paused, and one every caller already treats
 * as "don't act on this".
 *
 * That distinction has teeth. Against a 1,193-file project the port peaked at
 * 3.6 GB with no limit set and 2.9 GB under GOMEMLIMIT=3GiB, against pyright's
 * 2.8 GB -- and it was *faster* with the limit than without (62s vs 79s).
 * Setting it tighter is not free: 2 GiB took 184s and 1 GiB took 234s, which is
 * the garbage collector working harder rather than the cache being emptied more
 * usefully. The diagnostics were byte-identical at every setting, which is what
 * one wants from a cache.
 */

package analyzer

import (
	"math"
	"runtime/debug"
	"runtime/metrics"

	"github.com/microsoft/pyright/go/common"
)

// CacheOwner corresponds to the interface of the same name.
type CacheOwner interface {
	// GetCacheUsage returns a number between 0 and 1 that indicates how full
	// the cache is.
	GetCacheUsage() float64

	// EmptyCache empties the cache, typically in response to a low-memory
	// condition.
	EmptyCache()
}

// CacheManager corresponds to the class of the same name.
type CacheManager struct {
	pausedCount int
	cacheOwners []CacheOwner
}

func NewCacheManager() *CacheManager { return &CacheManager{} }

func (m *CacheManager) RegisterCacheOwner(provider CacheOwner) {
	m.cacheOwners = append(m.cacheOwners, provider)
}

func (m *CacheManager) UnregisterCacheOwner(provider CacheOwner) {
	index := -1
	for i, p := range m.cacheOwners {
		if p == provider {
			index = i
			break
		}
	}
	if index < 0 {
		common.Fail("Specified cache provider not found")
		return
	}
	m.cacheOwners = append(m.cacheOwners[:index], m.cacheOwners[index+1:]...)
}

// PauseTracking returns the handle that resumes it, standing in for the
// original's `{ dispose() }`.
func (m *CacheManager) PauseTracking() func() {
	m.pausedCount++
	resumed := false
	return func() {
		if resumed {
			return
		}
		resumed = true
		m.pausedCount--
	}
}

func (m *CacheManager) GetCacheUsage() float64 {
	if m.pausedCount > 0 {
		return -1
	}

	totalUsage := 0.0
	for _, p := range m.cacheOwners {
		totalUsage += p.GetCacheUsage()
	}

	return totalUsage
}

// EmptyCache empties every registered cache. The original logs the heap
// statistics first; see the header for why there are none.
func (m *CacheManager) EmptyCache(console common.ConsoleInterface) {
	for _, p := range m.cacheOwners {
		p.EmptyCache()
	}
}

// GetUsedHeapRatio returns the ratio of used heap bytes to the heap limit, or
// -1 when there is no answer to give.
//
// The original takes an optional console and logs a heap-statistics line at
// most once a second when verbose output is on. That log has no counterpart
// worth reproducing -- half its fields (total_physical_size,
// total_available_size, cross_worker_used_heap_size) name V8 internals with no
// Go equivalent -- so the parameter is dropped rather than filled with
// approximations that read as measurements.
func (m *CacheManager) GetUsedHeapRatio() float64 {
	if m.pausedCount > 0 {
		return -1
	}

	// debug.SetMemoryLimit(-1) reads the current soft limit without changing it.
	// math.MaxInt64 is the "no limit set" value, and dividing by it would answer
	// a ratio indistinguishable from zero for any real heap.
	limit := debug.SetMemoryLimit(-1)
	if limit <= 0 || limit == math.MaxInt64 {
		return -1
	}

	usage := float64(liveHeapBytes())

	// The original's comment: total usage seems to be off by about 5%, so we'll
	// add that back in to make the ratio more accurate. (200MB at 4GB)
	usage += usage * 0.05

	return usage / float64(limit)
}

// liveHeapBytes reads the live heap size.
//
// runtime.ReadMemStats would be the obvious call and is the wrong one here:
// it stops the world, and this runs once per file checked. runtime/metrics
// samples the same counter without pausing anything.
func liveHeapBytes() uint64 {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)

	if sample[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return sample[0].Value.Uint64()
}
