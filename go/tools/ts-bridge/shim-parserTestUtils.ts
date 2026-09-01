/*
 * shim-parserTestUtils.ts
 *
 * Stand-in for pyright-internal/src/tests/testUtils.ts that routes parsing
 * through the Go parser instead of the TypeScript one.
 *
 * The Go server returns the parse tree as plain JSON. This rebuilds it into the
 * object graph pyright's test code expects: parent pointers restored, node IDs
 * assigned, and the `number | bigint` union decoded back into real JavaScript
 * values. Everything the tests then touch -- ParseNodeType, getChildNodes,
 * findNodeByOffset, DiagnosticSink -- is the original, unmodified code running
 * over a Go-produced tree.
 *
 * The one thing not reproduced is the tokenizer output; no test in
 * parser.test.ts reads it, and the tokenizer already has its own bridge.
 */

import * as fs from 'fs';
import * as path from 'path';

import { DiagnosticSink } from '@pyright/common/diagnosticSink';
import { PythonVersion } from '@pyright/common/pythonVersion';
import { convertOffsetsToRange } from '@pyright/common/positionUtils';
import { TextRangeCollection } from '@pyright/common/textRangeCollection';

import { call } from './client';

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

function codeUnits(s: string): number[] {
    const a = new Array<number>(s.length);
    for (let i = 0; i < s.length; i++) {
        a[i] = s.charCodeAt(i);
    }
    return a;
}

function decodeDouble(hex: string): number {
    const view = new DataView(new ArrayBuffer(8));
    for (let i = 0; i < 8; i++) {
        view.setUint8(i, parseInt(hex.substr(i * 2, 2), 16));
    }
    return view.getFloat64(0, /* littleEndian */ false);
}

let nextNodeId = 1;

function isPlainObject(value: any): boolean {
    return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function rebuildValue(value: any, parent: any): any {
    if (value === null || value === undefined) {
        return undefined;
    }

    if (Array.isArray(value)) {
        return value.map((entry) => rebuildValue(entry, parent));
    }

    if (isPlainObject(value)) {
        // The `number | bigint` union.
        if (typeof value.num === 'string' && Object.keys(value).length === 1) {
            return decodeDouble(value.num);
        }
        if (typeof value.big === 'string' && Object.keys(value).length === 1) {
            return BigInt(value.big);
        }

        if (typeof value.nodeType === 'number') {
            return rebuildNode(value, parent);
        }

        // A token, kept as the plain {type, start, length} the server sends.
        return value;
    }

    return value;
}

function rebuildNode(raw: any, parent: any): any {
    const node: any = {
        start: raw.start,
        length: raw.length,
        nodeType: raw.nodeType,
        id: nextNodeId++,
        parent,
        d: {},
    };

    for (const key of Object.keys(raw.d ?? {})) {
        const rebuilt = rebuildValue(raw.d[key], node);
        if (rebuilt !== undefined) {
            node.d[key] = rebuilt;
        }
    }

    return node;
}

function buildLines(text: string): TextRangeCollection<any> {
    // The Go server does not send the line map back, so recompute it here. It
    // is only needed to convert diagnostic offsets to positions, and the
    // tokenizer bridge already pins the Go line map against the TypeScript one.
    const lines: { start: number; length: number }[] = [];
    let lineStart = 0;
    for (let i = 0; i < text.length; i++) {
        const c = text.charCodeAt(i);
        if (c === 0x0d) {
            const width = text.charCodeAt(i + 1) === 0x0a ? 2 : 1;
            lines.push({ start: lineStart, length: i - lineStart + width });
            i += width - 1;
            lineStart = i + 1;
        } else if (c === 0x0a) {
            lines.push({ start: lineStart, length: i - lineStart + 1 });
            lineStart = i + 1;
        }
    }
    lines.push({ start: lineStart, length: text.length - lineStart });
    return new TextRangeCollection(lines as any);
}

export function parseText(textToParse: string, diagSink: DiagnosticSink, parseOptions?: any): any {
    const request: any = {
        op: 'parse',
        stringsAsText: true,
        text: codeUnits(textToParse),
    };

    if (parseOptions) {
        request.isStubFile = !!parseOptions.isStubFile;
        request.useNotebookMode = !!parseOptions.useNotebookMode;
        request.reportErrorsForParsedStringContents = !!parseOptions.reportErrorsForParsedStringContents;
        if (parseOptions.pythonVersion) {
            // PythonVersion is a plain object with static helpers, not a class
            // with a toString.
            request.pythonVersion = PythonVersion.toString(parseOptions.pythonVersion);
        }
    }

    const result = call(request);

    nextNodeId = 1;
    const parseTree = rebuildNode(result.parseTree, undefined);
    const lines = buildLines(textToParse);

    for (const diag of result.diagnostics) {
        // The server reports positions; the sink wants a range object, which is
        // exactly what it already has.
        diagSink.addError(diag.message, {
            start: { line: diag.start[0], character: diag.start[1] },
            end: { line: diag.end[0], character: diag.end[1] },
        } as any);
    }

    const typingSymbolAliases = new Map<string, string>(Object.entries(result.typingSymbolAliases ?? {}));

    return {
        text: textToParse,
        parserOutput: {
            parseTree,
            importedModules: result.importedModules,
            futureImports: new Set<string>(result.futureImports ?? []),
            containsWildcardImport: result.containsWildcardImport,
            typingSymbolAliases,
            hasTypeAnnotations: result.hasTypeAnnotations,
            lines,
        },
        tokenizerOutput: { lines },
    };
}

export function parseSampleFile(fileName: string, diagSink: DiagnosticSink, execEnvironment?: any): any {
    const text = readSampleFile(fileName);
    const parseOptions: any = {};
    if (execEnvironment?.pythonVersion) {
        parseOptions.pythonVersion = execEnvironment.pythonVersion;
    }
    return parseText(text, diagSink, parseOptions);
}

// convertOffsetsToRange is re-exported so the bundle keeps the import alive;
// pyright's own testUtils exports a larger surface that the parser tests do not
// use.
export { convertOffsetsToRange };
