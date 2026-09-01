/*
 * dump-ast.ts
 *
 * Dumps a parse tree from pyright's own (unmodified) TypeScript parser in the
 * same JSON shape cmd/tokenserver/ast.go produces from the Go parser, so the two
 * can be diffed over a corpus.
 *
 * Conventions, chosen so the comparison is exact rather than approximate:
 *
 *   - Strings are emitted as arrays of UTF-16 code units. Go strings are UTF-8
 *     and Go's Text is []uint16; code units are the one representation both
 *     sides agree on for every input, including lone surrogates.
 *   - A NumberNode's `value` is emitted as the raw IEEE 754 bit pattern (or a
 *     decimal string for a bigint) so the comparison never goes through either
 *     language's float formatting.
 *   - `id` and `parent` are skipped: IDs are allocation counters and parents
 *     would make the structure cyclic.
 */

import { DiagnosticSink } from '@pyright/common/diagnosticSink';
import { ParseNodeType } from '@pyright/parser/parseNodes';
import { ParseOptions, Parser } from '@pyright/parser/parser';
import { PythonVersion } from '@pyright/common/pythonVersion';

function encodeString(value: string): number[] {
    const out: number[] = [];
    for (let i = 0; i < value.length; i++) {
        out.push(value.charCodeAt(i));
    }
    return out;
}

function encodeDouble(value: number): string {
    const buffer = new ArrayBuffer(8);
    new DataView(buffer).setFloat64(0, value, /* littleEndian */ false);
    return Array.from(new Uint8Array(buffer))
        .map((b) => b.toString(16).padStart(2, '0'))
        .join('');
}

function isNode(value: any): boolean {
    return value !== null && typeof value === 'object' && typeof value.nodeType === 'number';
}

function isToken(value: any): boolean {
    return (
        value !== null &&
        typeof value === 'object' &&
        typeof value.type === 'number' &&
        typeof value.start === 'number' &&
        typeof value.length === 'number' &&
        value.nodeType === undefined
    );
}

function serializeValue(value: any, isNumberNodeValue: boolean): any {
    if (value === undefined || value === null) {
        return undefined;
    }

    if (typeof value === 'bigint') {
        return { big: value.toString() };
    }
    if (typeof value === 'number') {
        return isNumberNodeValue ? { num: encodeDouble(value) } : value;
    }
    if (typeof value === 'boolean') {
        return value;
    }
    if (typeof value === 'string') {
        return encodeString(value);
    }

    if (Array.isArray(value)) {
        return value.map((entry) => serializeValue(entry, /* isNumberNodeValue */ false));
    }

    if (isNode(value)) {
        return serializeNode(value);
    }
    if (isToken(value)) {
        return { type: value.type, start: value.start, length: value.length };
    }

    // A TextRange that is not a token (only PatternSequenceNode has none of
    // these, but keep the branch honest).
    if (typeof value.start === 'number' && typeof value.length === 'number') {
        return [value.start, value.length];
    }

    return undefined;
}

function serializeNode(node: any): any {
    const d: Record<string, any> = {};
    const details = node.d ?? {};
    const isNumberNode = node.nodeType === ParseNodeType.Number;

    for (const key of Object.keys(details)) {
        const serialized = serializeValue(details[key], isNumberNode && key === 'value');
        if (serialized !== undefined) {
            d[key] = serialized;
        }
    }

    return { nodeType: node.nodeType, start: node.start, length: node.length, d };
}

export interface DumpOptions {
    isStubFile?: boolean;
    pythonVersion?: string;
    useNotebookMode?: boolean;
    reportErrorsForParsedStringContents?: boolean;
}

export function dump(text: string, options: DumpOptions = {}): any {
    const parseOptions = new ParseOptions();
    if (options.isStubFile) {
        parseOptions.isStubFile = true;
    }
    if (options.pythonVersion) {
        const version = PythonVersion.fromString(options.pythonVersion);
        if (!version) {
            throw new Error(`unrecognized pythonVersion: ${options.pythonVersion}`);
        }
        parseOptions.pythonVersion = version;
    }
    parseOptions.useNotebookMode = !!options.useNotebookMode;
    parseOptions.reportErrorsForParsedStringContents = !!options.reportErrorsForParsedStringContents;

    const diagSink = new DiagnosticSink();
    const parser = new Parser();
    const results = parser.parseSourceFile(text, parseOptions, diagSink);

    const diagnostics = diagSink.fetchAndClear().map((diag: any) => ({
        category: diag.category,
        message: diag.message,
        start: [diag.range.start.line, diag.range.start.character],
        end: [diag.range.end.line, diag.range.end.character],
    }));

    const importedModules = results.parserOutput.importedModules.map((imp: any) => ({
        leadingDots: imp.leadingDots,
        nameParts: imp.nameParts,
        importedSymbols: imp.importedSymbols ? Array.from(imp.importedSymbols).sort() : undefined,
    }));

    const typingSymbolAliases: Record<string, string> = {};
    results.parserOutput.typingSymbolAliases.forEach((value: string, key: string) => {
        typingSymbolAliases[key] = value;
    });

    return {
        parseTree: serializeNode(results.parserOutput.parseTree),
        diagnostics,
        importedModules,
        futureImports: Array.from(results.parserOutput.futureImports).sort(),
        containsWildcardImport: results.parserOutput.containsWildcardImport,
        hasTypeAnnotations: results.parserOutput.hasTypeAnnotations,
        typingSymbolAliases,
    };
}
