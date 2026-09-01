/*
 * memoization.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from common/uri/memoization.ts (pyright 1.1.412).
 *
 * The original is three TypeScript decorators. Go has no decorators, so:
 *
 *  - `@cacheProperty()` and `@cacheMethodWithNoArgs()` become an explicit
 *    nil-check-and-store field on the receiver. baseUri already does this for
 *    the five URIs it derives; FileUri and WebUri do it for their own.
 *
 *  - `@cacheStaticFunc()` is the one that needs machinery, because it is not
 *    an optimization: it interns URIs. Two calls to FileUri.createFileUri with
 *    the same arguments return the *same object*, so the per-instance caches
 *    above are shared, and a Uri built by two different routes has one
 *    identity. That is reproduced below.
 *
 * The cache is process-global mutable state, as in the original. The
 * TypeScript is single-threaded; the mutex here is so that a Go caller that
 * resolves imports on several goroutines does not corrupt the list.
 */

package uri

import (
	"container/list"
	"strings"
	"sync"
)

// maxStaticCacheEntries carries the original's sizing note: comfortably more
// than the distinct-Uri working set of a single import resolution across many
// extraPaths (monorepos can inject 100+ search roots, each descended a few
// levels). A smaller cache thrashes -- every resolution evicts the previous
// one's stable prefix Uris, forcing constant re-allocation and GC churn.
const maxStaticCacheEntries = 8192

type staticCacheEntry struct {
	key   string
	value any
}

var (
	staticCacheMutex sync.Mutex
	staticCacheOrder = list.New()
	staticCache      = map[string]*list.Element{}
)

// cacheStaticFunc looks up key, or calls compute and stores the result,
// evicting the least recently used entry when over capacity.
func cacheStaticFunc(key string, compute func() any) any {
	staticCacheMutex.Lock()
	defer staticCacheMutex.Unlock()

	if element, ok := staticCache[key]; ok {
		// Promote to most-recently used by re-inserting.
		staticCacheOrder.MoveToBack(element)
		return element.Value.(*staticCacheEntry).value
	}

	// Miss: compute and insert, evict LRU if over capacity.
	//
	// The original computes with the lock notionally held -- it is
	// single-threaded -- and createFileUri / createWebUri do not re-enter this
	// function, so holding it here cannot deadlock.
	result := compute()

	if len(staticCache) >= maxStaticCacheEntries {
		// Remove least-recently used (the first key in insertion order).
		if lru := staticCacheOrder.Front(); lru != nil {
			delete(staticCache, lru.Value.(*staticCacheEntry).key)
			staticCacheOrder.Remove(lru)
		}
	}

	staticCache[key] = staticCacheOrder.PushBack(&staticCacheEntry{key: key, value: result})
	return result
}

// staticCacheKey builds the original's `${functionName}+${args.join(',')}` key.
//
// The original maps each argument through `a?.toString()`, so undefined stays
// undefined, and Array.prototype.join renders undefined as the empty string.
// Callers here pass the already-stringified arguments, using "" for undefined,
// which is the same text.
func staticCacheKey(functionName string, args ...string) string {
	return functionName + "+" + strings.Join(args, ",")
}

// boolArg renders a boolean the way JavaScript's toString does.
func boolArg(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
