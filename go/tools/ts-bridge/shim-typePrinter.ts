/*
 * shim-typePrinter.ts
 *
 * Drop-in replacement for pyright-internal/src/analyzer/typePrinter.ts that
 * forwards printType to the Go port.
 *
 * PrintTypeFlags is re-exported from the original module. It is a `const enum`,
 * so esbuild inlines the values and the real typePrinter.ts does not survive
 * into the bundle.
 *
 * One deviation from "run the original test unchanged": the returnTypeCallback
 * the test passes is a TypeScript closure, and the bridge protocol is
 * unidirectional -- the Go side cannot call back into Node mid-print. The
 * callback is therefore reimplemented in typebridge.go. It is the two lines at
 * the top of typePrinter.test.ts:
 *
 *     type.shared.declaredReturnType ?? UnknownType.create(true)
 *
 * If a future test passes a callback that does anything else, this shim will
 * silently use the wrong one, so it throws unless the callback is the only one
 * the current tests define.
 */

export { PrintTypeFlags } from '@pyright/analyzer/typePrinter';
export type { FunctionReturnTypeCallback } from '@pyright/analyzer/typePrinter';

import { call } from './client';
import { getLog, handleOf } from './shim-types';

export function printType(type: any, printTypeFlags: number, returnTypeCallback: any): string {
    assertKnownCallback(returnTypeCallback);

    return call({
        op: 'types',
        payload: {
            log: getLog(),
            handle: handleOf(type),
            flags: printTypeFlags,
        },
    });
}

// Guards the deviation described in the file header: the Go side hard-codes the
// callback, so a different one must not pass unnoticed.
function assertKnownCallback(callback: any): void {
    const source = String(callback).replace(/\s+/g, ' ');
    // esbuild renames imported bindings when it bundles, so UnknownType may
    // appear as UnknownType3; match the shape rather than the exact name.
    const isKnown =
        source.includes('declaredReturnType') &&
        /UnknownType\d*\.create\(/.test(source) &&
        (source.includes('??') || source.includes('null'));

    if (!isKnown) {
        throw new Error(
            'the Go bridge reimplements returnTypeCallback; this test passes an unrecognized one:\n' + source
        );
    }
}
