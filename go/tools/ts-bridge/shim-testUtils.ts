/*
 * shim-testUtils.ts
 *
 * Minimal stand-in for pyright-internal/src/tests/testUtils.ts. The real module
 * pulls in the program/service/analyzer stack, none of which is needed by the
 * tokenizer tests -- they use exactly one helper. Aliasing it here keeps the
 * bundle to the front end that is actually under test.
 */

import * as fs from 'fs';
import * as path from 'path';

const samplesFolder = process.env.PYRIGHT_SAMPLES_DIR;

export function resolveSampleFilePath(fileName: string): string {
    if (!samplesFolder) {
        throw new Error('PYRIGHT_SAMPLES_DIR must point at src/tests/samples');
    }
    return path.resolve(samplesFolder, fileName);
}

export function readSampleFile(fileName: string): string {
    const filePath = resolveSampleFilePath(fileName);

    try {
        return fs.readFileSync(filePath, { encoding: 'utf8' });
    } catch {
        console.error(`Could not read file "${fileName}"`);
        return '';
    }
}
