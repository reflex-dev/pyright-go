/*
 * shim-testState.ts
 *
 * Stand-in for tests/harness/fourslash/testState.
 *
 * The tests that use it drive pyright's whole language service -- program,
 * import resolver, binder, checker -- none of which is ported. Rather than
 * quietly returning something plausible, these throw a marker the test harness
 * recognizes, so those tests are reported as skipped with a reason instead of
 * being silently counted as passing.
 */

export const UNSUPPORTED_MARKER = 'PYRIGHT_GO_BRIDGE_UNSUPPORTED';

function unsupported(name: string): never {
    throw new Error(`${UNSUPPORTED_MARKER}: ${name} requires the analyzer, which is not ported`);
}

export function parseAndGetTestState(..._args: any[]): any {
    unsupported('parseAndGetTestState');
}

export function getNodeAtMarker(..._args: any[]): any {
    unsupported('getNodeAtMarker');
}
