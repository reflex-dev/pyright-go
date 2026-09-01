/*
 * client.ts
 *
 * Synchronous client for the Go tokenserver bridge.
 *
 * The TypeScript tests are synchronous, so this talks to the Go process with
 * spawnSync: one request per invocation. That is slower than a persistent pipe
 * but keeps the harness free of any async plumbing that could change how the
 * tests behave.
 */

import { spawnSync } from 'child_process';

const serverPath = process.env.PYRIGHT_GO_TOKENSERVER;
if (!serverPath) {
    throw new Error('PYRIGHT_GO_TOKENSERVER must point at the built tokenserver binary');
}

let requestCount = 0;

export function call(request: any): any {
    requestCount++;
    const result = spawnSync(serverPath!, [], {
        input: JSON.stringify(request),
        maxBuffer: 256 * 1024 * 1024,
        encoding: 'utf8',
    });

    if (result.error) {
        throw result.error;
    }
    if (result.status !== 0) {
        throw new Error(`tokenserver exited with ${result.status}: ${result.stderr}`);
    }

    const parsed = JSON.parse(result.stdout);
    if (parsed.error !== undefined) {
        // Panics on the Go side surface as thrown Errors, matching how the
        // TypeScript implementation reports the same conditions.
        throw new Error(parsed.error);
    }
    return parsed.result;
}

export function getRequestCount(): number {
    return requestCount;
}
