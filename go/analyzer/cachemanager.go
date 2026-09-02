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
 * PARTIAL, and the missing part is measurement rather than logic. The original
 * reads V8 heap statistics through `v8.getHeapStatistics()` and shares a usage
 * figure across worker threads with a SharedArrayBuffer. Go's runtime exposes
 * no heap *limit*, only a live-heap size, so there is no ratio to compare
 * against a high-water mark -- and there are no worker threads to share with.
 *
 * GetUsedHeapRatio therefore answers -1, which is the same value the original
 * answers while tracking is paused and which every caller already treats as
 * "don't act on this". The registry, the pause counter and the cache-emptying
 * are all real; when the type evaluator arrives and registers itself, emptying
 * the cache will do what it does upstream. What will need revisiting is the
 * trigger, and that is recorded here rather than left to be discovered.
 */

package analyzer

import "github.com/microsoft/pyright/go/common"

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

// GetUsedHeapRatio returns the ratio of used bytes to total bytes -- or, here,
// always -1. See the header.
func (m *CacheManager) GetUsedHeapRatio() float64 {
	return -1
}
