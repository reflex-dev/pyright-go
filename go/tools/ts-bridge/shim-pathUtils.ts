/*
 * shim-pathUtils.ts
 *
 * Drop-in replacement for pyright-internal/src/common/pathUtils.ts that
 * forwards to the Go port, so pyright's own pathUtils.test.ts runs unmodified
 * against it.
 *
 * Every function the test imports is a pure function of strings, so this is a
 * plain dispatch like shim-symbolNameUtils.ts: one request per call, no state.
 *
 * getWildcardRegexPattern is the interesting one. It returns a *pattern string*
 * that the test then compiles with `new RegExp(...)` and matches paths against,
 * so what crosses the wire is the Go-generated JavaScript regular expression
 * source and JavaScript is what judges it. That is a stronger check than
 * comparing match results would be: the pattern has to be right character for
 * character, not merely equivalent on the handful of paths the test tries.
 *
 * Only the exports pathUtils.test.ts actually imports are here. The rest of the
 * module is ported but has no bridgeable test of its own; it is covered by
 * importResolver.test.ts further up the stack.
 */

import { call } from './client';

function pathutils(which: string, args: any[]): any {
    return call({ op: 'pathutils', payload: { which, args } });
}

export function getPathComponents(pathString: string): string[] {
    return pathutils('getPathComponents', [pathString]);
}

export function reducePathComponents(components: readonly string[]): string[] {
    return pathutils('reducePathComponents', [components]);
}

export function combinePathComponents(components: string[]): string {
    return pathutils('combinePathComponents', [components]);
}

export function combinePaths(pathString: string, ...paths: (string | undefined)[]): string {
    return pathutils('combinePaths', [pathString, paths.map((p) => p ?? '')]);
}

export function resolvePaths(pathString: string, ...paths: (string | undefined)[]): string {
    return pathutils('resolvePaths', [pathString, paths.map((p) => p ?? '')]);
}

export function normalizeSlashes(pathString: string): string {
    return pathutils('normalizeSlashes', [pathString]);
}

export function getRelativePath(dirPath: string, relativeTo: string): string | undefined {
    const result = pathutils('getRelativePath', [dirPath, relativeTo]);
    return result === null ? undefined : result;
}

export function ensureTrailingDirectorySeparator(pathString: string): string {
    return pathutils('ensureTrailingDirectorySeparator', [pathString]);
}

export function hasTrailingDirectorySeparator(pathString: string): boolean {
    return pathutils('hasTrailingDirectorySeparator', [pathString]);
}

export function stripTrailingDirectorySeparator(pathString: string): string {
    return pathutils('stripTrailingDirectorySeparator', [pathString]);
}

export function getFileExtension(fileName: string, multiDotExtension = false): string {
    return pathutils('getFileExtension', [fileName, multiDotExtension]);
}

export function getFileName(pathString: string): string {
    return pathutils('getFileName', [pathString]);
}

export function stripFileExtension(fileName: string, multiDotExtension = false): string {
    return pathutils('stripFileExtension', [fileName, multiDotExtension]);
}

export function getRootLength(pathString: string): number {
    return pathutils('getRootLength', [pathString]);
}

export function isRootedDiskPath(pathString: string): boolean {
    return pathutils('isRootedDiskPath', [pathString]);
}

export function isDiskPathRoot(pathString: string): boolean {
    return pathutils('isDiskPathRoot', [pathString]);
}

export function getWildcardRegexPattern(rootPath: string, fileSpec: string): string {
    return pathutils('getWildcardRegexPattern', [rootPath, fileSpec]);
}

export function isDirectoryWildcardPatternPresent(fileSpec: string): boolean {
    return pathutils('isDirectoryWildcardPatternPresent', [fileSpec]);
}

export function getWildcardRoot(rootPath: string, fileSpec: string): string {
    return pathutils('getWildcardRoot', [rootPath, fileSpec]);
}

// The third parameter is `currentDirectory: string | boolean | undefined`,
// discriminated by type at runtime. The Go port splits that into two functions,
// so the discrimination happens here instead.
export function containsPath(parent: string, child: string, ignoreCase?: boolean): boolean;
export function containsPath(parent: string, child: string, currentDirectory: string, ignoreCase?: boolean): boolean;
export function containsPath(
    parent: string,
    child: string,
    currentDirectory?: string | boolean,
    ignoreCase?: boolean
): boolean {
    if (typeof currentDirectory === 'string') {
        return pathutils('containsPathIn', [parent, child, currentDirectory, !!ignoreCase]);
    }
    return pathutils('containsPath', [parent, child, !!currentDirectory]);
}

// Likewise: `extensions` is `string | readonly string[]` and its presence
// selects the overload.
export function getAnyExtensionFromPath(pathString: string): string;
export function getAnyExtensionFromPath(
    pathString: string,
    extensions: string | readonly string[],
    ignoreCase: boolean
): string;
export function getAnyExtensionFromPath(
    pathString: string,
    extensions?: string | readonly string[],
    ignoreCase?: boolean
): string {
    if (extensions === undefined) {
        return pathutils('getAnyExtensionFromPath', [pathString]);
    }
    const list = typeof extensions === 'string' ? [extensions] : [...extensions];
    return pathutils('getAnyExtensionFromPathIn', [pathString, list, !!ignoreCase]);
}

export function getBaseFileName(pathString: string): string;
export function getBaseFileName(pathString: string, extensions: string | readonly string[], ignoreCase: boolean): string;
export function getBaseFileName(
    pathString: string,
    extensions?: string | readonly string[],
    ignoreCase?: boolean
): string {
    if (extensions === undefined) {
        return pathutils('getBaseFileName', [pathString]);
    }
    const list = typeof extensions === 'string' ? [extensions] : [...extensions];
    return pathutils('getBaseFileNameIn', [pathString, list, !!ignoreCase]);
}
