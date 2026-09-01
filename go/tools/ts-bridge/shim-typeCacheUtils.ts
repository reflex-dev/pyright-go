/*
 * shim-typeCacheUtils.ts
 *
 * Drop-in replacement for pyright-internal/src/analyzer/typeCacheUtils.ts that
 * forwards to the Go port, so pyright's own typeCacheUtils.test.ts runs
 * unmodified against it.
 *
 * The test builds TypeVars with TypeVarType.createInstance and puts them in
 * plain JavaScript objects, so this rides on shim-types.ts's construction log
 * the same way shim-typePrinter.ts does: the whole log is replayed on the Go
 * side with each call. That means this exercises the Go types.ts port and
 * isTypeSame as well as typeCacheUtils.
 *
 * addContextualTypeCacheEntry returns the surviving *entries*, which are the
 * caller's own objects. The Go side cannot return those, so it returns the
 * indices of the survivors -- into `[...entries, newEntry]`, so the appended
 * entry is the last index -- and this reassembles the array. All of the
 * matching, filtering, eviction and ordering happens in Go.
 *
 * One deviation, in the same family as shim-typePrinter.ts's: isEntryValid is a
 * TypeScript closure and the protocol is unidirectional, so it is evaluated
 * here, once per existing entry, and shipped as a boolean array. That is not an
 * approximation. The original's condition is
 *
 *     (!isEntryValid || isEntryValid(entry)) && !matches(entry, ...)
 *
 * so isEntryValid is the left operand of the `&&` and runs for every entry
 * regardless; only `matches` is short-circuited, and that stays on the Go side.
 */

import { call } from './client';
import { getLog } from './shim-types';

// The expectedType of an entry is either a shim handle or undefined.
function handleOrNull(value: any): number | null {
    if (value === undefined || value === null) {
        return null;
    }
    const id = value.__goHandle;
    if (typeof id !== 'number') {
        throw new Error('expected a Go type handle or undefined for expectedType');
    }
    return id;
}

export interface ContextualTypeCacheEntry {
    expectedType: any;
}

export function contextualTypeCacheEntryMatches(entry: ContextualTypeCacheEntry, expectedType: any): boolean {
    return call({
        op: 'typecacheutils',
        payload: {
            which: 'matches',
            log: getLog(),
            entry: handleOrNull(entry.expectedType),
            expected: handleOrNull(expectedType),
        },
    });
}

export function addContextualTypeCacheEntry<T extends ContextualTypeCacheEntry>(
    cacheEntries: T[],
    newEntry: T,
    isEntryValid?: (entry: T) => boolean
): T[] {
    const survivors: number[] = call({
        op: 'typecacheutils',
        payload: {
            which: 'add',
            log: getLog(),
            entries: cacheEntries.map((entry) => handleOrNull(entry.expectedType)),
            valid: isEntryValid ? cacheEntries.map((entry) => isEntryValid(entry)) : null,
            newEntry: handleOrNull(newEntry.expectedType),
        },
    });

    const all = [...cacheEntries, newEntry];
    return survivors.map((index) => all[index]);
}
