/*
 * shim-uriUtils.ts
 *
 * Drop-in replacement for the part of
 * pyright-internal/src/common/uri/uriUtils.ts that uri.test.ts imports.
 *
 * uriUtils is half pure Uri arithmetic and half filesystem access. Only the
 * pure half is ported so far, so only the pure half is forwarded here:
 * getWildcardRegexPattern, getWildcardRoot, deduplicateFolders and UriEx.
 *
 * The first two ride on shim-uri.ts's recipes rather than needing a protocol of
 * their own -- getWildcardRoot is just another Uri-returning derivation, and
 * getWildcardRegexPattern just another scalar read.
 *
 * makeDirectories is re-exported from the real module. It takes a FileSystem,
 * which the test supplies as a hand-written JavaScript object with counters in
 * it -- there is nothing to forward it to. It is still not a hole in the gate:
 * everything makeDirectories does with the Uris it is handed (startsWith,
 * getPathComponents, combinePaths) runs against bridged Uris, so the Go
 * implementations are what the assertions see.
 */

import { call } from './client';
// Not rewritten by the alias plugin: it only rewrites relative imports, and
// only for the test file itself.
import { makeDirectories as realMakeDirectories } from '@pyright/common/uri/uriUtils';
import { deriveUri, readUri, recipeOf, Uri } from './shim-uri';

export const makeDirectories = realMakeDirectories;

export function getWildcardRegexPattern(root: Uri, fileSpec: string): string {
    // The pattern is a JavaScript regular expression source string, so what
    // crosses the wire is what Go generated and JavaScript is what judges it.
    return readUri(root, 'uriUtils.getWildcardRegexPattern', fileSpec);
}

export function getWildcardRoot(root: Uri, fileSpec: string): Uri {
    return deriveUri(root, 'uriUtils.getWildcardRoot', fileSpec);
}

export function deduplicateFolders(listOfFolders: Uri[][], excludes: Uri[] = []): Uri[] {
    // Go returns the survivors as indices into the flattened input, the same
    // shape shim-typeCacheUtils.ts uses: the caller's own objects cannot cross
    // the wire, and all of the filtering and ordering stays in Go.
    const flat = listOfFolders.flat();
    const survivors: number[] = call({
        op: 'uriutils',
        payload: {
            which: 'deduplicateFolders',
            folders: listOfFolders.map((folders) => folders.map(recipeOf)),
            excludes: excludes.map(recipeOf),
        },
    });
    return survivors.map((index) => flat[index]);
}

export namespace UriEx {
    export function file(path: string, isCaseSensitive = true, checkRelative = false): Uri {
        return Uri.file(path, { isCaseSensitive: () => isCaseSensitive }, checkRelative);
    }

    export function parse(value: string | undefined, isCaseSensitive = true): Uri {
        return Uri.parse(value, { isCaseSensitive: () => isCaseSensitive });
    }
}
