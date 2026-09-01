/*
 * shim-symbolNameUtils.ts
 *
 * Drop-in replacement for pyright-internal/src/analyzer/symbolNameUtils.ts that
 * forwards to the Go port, so pyright's own symbolNameUtils.test.ts runs
 * unmodified against it.
 *
 * Every function here is a pure string predicate, so this is the simplest shim
 * in the bridge: one request per call, no state, no handles.
 */

import { call } from './client';

function predicate(name: string, fn: string): boolean {
    return call({ op: 'symbolnameutils', which: fn, name });
}

export function isPrivateName(name: string): boolean {
    return predicate(name, 'isPrivateName');
}

export function isProtectedName(name: string): boolean {
    return predicate(name, 'isProtectedName');
}

export function isPrivateOrProtectedName(name: string): boolean {
    return predicate(name, 'isPrivateOrProtectedName');
}

export function isDunderName(name: string): boolean {
    return predicate(name, 'isDunderName');
}

export function isSingleDunderName(name: string): boolean {
    return predicate(name, 'isSingleDunderName');
}

export function isConstantName(name: string): boolean {
    return predicate(name, 'isConstantName');
}

export function isTypeAliasName(name: string): boolean {
    return predicate(name, 'isTypeAliasName');
}

export function isPublicConstantOrTypeAlias(name: string): boolean {
    return predicate(name, 'isPublicConstantOrTypeAlias');
}
