#!/usr/bin/env node
/*
 * compare-corpus.js
 *
 * Differential test: tokenizes every file in a corpus with both the original
 * TypeScript tokenizer and the Go port, and reports any difference in the
 * resulting token stream, line ranges, ignore-comment maps or the derived
 * "predominant" statistics.
 *
 * The unit tests pin the behavior the pyright authors chose to assert; this
 * pins everything else, over real Python source.
 *
 * Usage:
 *   node compare-corpus.js --ref <pyright-internal/src> \
 *                          --server <tokenserver> \
 *                          --esbuild <esbuild binary> \
 *                          [--glob 'tests/samples/*.py'] [--limit N]
 */

'use strict';

const { execFileSync, spawnSync } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

function parseArgs(argv) {
    const args = {};
    for (let i = 2; i < argv.length; i += 2) {
        args[argv[i].slice(2)] = argv[i + 1];
    }
    return args;
}

const args = parseArgs(process.argv);
const refSrc = path.resolve(args.ref);
const serverPath = path.resolve(args.server);
const esbuildPath = path.resolve(args.esbuild);
const limit = args.limit ? Number(args.limit) : Infinity;

const bridgeDir = __dirname;
const outDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pyright-go-corpus-'));
const entry = path.join(outDir, 'entry.ts');
const bundle = path.join(outDir, 'bundle.js');

// The comparison driver runs inside the bundle so it can import both the real
// tokenizer and the Go-backed shim in the same process.
fs.writeFileSync(
    entry,
    `
import * as fs from 'fs';
import { Tokenizer as RealTokenizer } from ${JSON.stringify(path.join(refSrc, 'parser/tokenizer'))};
import { Tokenizer as GoTokenizer } from ${JSON.stringify(path.join(bridgeDir, 'shim-tokenizer.ts'))};

const files: string[] = JSON.parse(fs.readFileSync(process.env.CORPUS_LIST!, 'utf8'));

function codeUnits(s: string): number[] {
    const a: number[] = [];
    for (let i = 0; i < s.length; i++) a.push(s.charCodeAt(i));
    return a;
}

// Normalizes a TokenizerOutput into a plain, comparable value. Strings become
// code-unit arrays so the comparison cannot be fooled by UTF-8 round-tripping,
// and bigints become tagged strings so JSON.stringify accepts them.
function normalize(r: any) {
    const tokens: any[] = [];
    for (let i = 0; i < r.tokens.count; i++) {
        const t: any = r.tokens.getItemAt(i);
        const e: any = { type: t.type, start: t.start, length: t.length };
        for (const key of ['keywordType', 'operatorType', 'newLineType', 'flags', 'prefixLength',
                           'quoteMarkLength', 'isInteger', 'isImaginary', 'indentAmount',
                           'matchesIndent', 'isIndentAmbiguous', 'isDedentAmbiguous']) {
            if (t[key] !== undefined) e[key] = t[key];
        }
        if (t.escapedValue !== undefined) e.escapedValue = codeUnits(t.escapedValue);
        if (t.type === 7 /* Identifier */ && t.value !== undefined) e.value = codeUnits(t.value);
        if (t.type === 6 /* Number */ && t.value !== undefined) {
            e.value = typeof t.value === 'bigint' ? { big: t.value.toString() } : { bits: bits(t.value) };
        }
        if (t.comments) {
            e.comments = t.comments.map((c: any) => ({ type: c.type, start: c.start, length: c.length, value: codeUnits(c.value) }));
        }
        tokens.push(e);
    }

    const lines: number[][] = [];
    for (let i = 0; i < r.lines.count; i++) {
        const l = r.lines.getItemAt(i);
        lines.push([l.start, l.length]);
    }

    const ignore = (c: any) => c === undefined ? null : ({
        range: [c.range.start, c.range.length],
        rules: c.rulesList ? c.rulesList.map((x: any) => ({ text: codeUnits(x.text), range: [x.range.start, x.range.length] })) : null,
    });
    const mapOf = (m: Map<number, any>) => {
        const o: any = {};
        for (const k of [...m.keys()].sort((a, b) => a - b)) o[k] = ignore(m.get(k));
        return o;
    };

    return {
        tokens, lines,
        eol: codeUnits(r.predominantEndOfLineSequence),
        hasTab: r.hasPredominantTabSequence,
        tab: codeUnits(r.predominantTabSequence),
        quote: codeUnits(r.predominantSingleQuoteCharacter),
        typeIgnoreAll: ignore(r.typeIgnoreAll),
        typeIgnoreLines: mapOf(r.typeIgnoreLines),
        pyrightIgnoreLines: mapOf(r.pyrightIgnoreLines),
    };
}

function bits(v: number): string {
    const view = new DataView(new ArrayBuffer(8));
    view.setFloat64(0, v);
    let s = '';
    for (let i = 0; i < 8; i++) s += view.getUint8(i).toString(16).padStart(2, '0');
    return s;
}

let compared = 0;
const mismatches: string[] = [];

for (const file of files) {
    const text = fs.readFileSync(file, 'utf8');

    const expected = JSON.stringify(normalize(new RealTokenizer().tokenize(text)));
    const actual = JSON.stringify(normalize(new GoTokenizer().tokenize(text)));
    compared++;

    if (expected !== actual) {
        mismatches.push(file);
        if (mismatches.length <= 3) {
            console.log('MISMATCH ' + file);
            console.log('  ' + firstDifference(expected, actual));
        }
    }

    if (compared % 200 === 0) {
        console.log('  ... ' + compared + '/' + files.length);
    }
}

function firstDifference(a: string, b: string): string {
    let i = 0;
    while (i < a.length && i < b.length && a[i] === b[i]) i++;
    const from = Math.max(0, i - 60);
    return 'at offset ' + i + '\\n    TS: ' + a.slice(from, i + 90) + '\\n    Go: ' + b.slice(from, i + 90);
}

console.log('');
console.log(compared + ' files compared, ' + mismatches.length + ' mismatched');
if (mismatches.length > 0) {
    process.exitCode = 1;
}
`
);

const aliasPlugin = `
const path = require('path');
module.exports = {
    name: 'go-bridge-alias',
    setup(build) {
        build.onResolve({ filter: /^@pyright\\// }, (a) => ({
            path: path.join(${JSON.stringify(refSrc)}, a.path.slice('@pyright/'.length)) + '.ts',
        }));
    },
};
`;
fs.writeFileSync(path.join(outDir, 'alias-plugin.js'), aliasPlugin);

function findEsbuildPackage(binPath) {
    let dir = path.dirname(binPath);
    while (dir !== path.dirname(dir)) {
        const candidate = path.join(dir, 'node_modules', 'esbuild');
        if (fs.existsSync(candidate)) return candidate;
        dir = path.dirname(dir);
    }
    throw new Error(`could not locate the esbuild package near ${binPath}`);
}

const buildScript = path.join(outDir, 'build.js');
fs.writeFileSync(
    buildScript,
    `
const esbuild = require(${JSON.stringify(findEsbuildPackage(esbuildPath))});
esbuild.build({
    entryPoints: [${JSON.stringify(entry)}],
    bundle: true, platform: 'node', format: 'cjs',
    outfile: ${JSON.stringify(bundle)},
    plugins: [require(${JSON.stringify(path.join(outDir, 'alias-plugin.js'))})],
    logLevel: 'error',
}).catch((e) => { console.error(e); process.exit(1); });
`
);
execFileSync(process.execPath, [buildScript], { stdio: 'inherit' });

// Build the corpus list.
const globPattern = args.glob || 'tests/samples';
const corpusDir = path.join(refSrc, globPattern);
let files = fs
    .readdirSync(corpusDir)
    .filter((f) => f.endsWith('.py') || f.endsWith('.pyi'))
    .map((f) => path.join(corpusDir, f))
    .sort();
if (files.length > limit) files = files.slice(0, limit);

const corpusList = path.join(outDir, 'corpus.json');
fs.writeFileSync(corpusList, JSON.stringify(files));

console.log(`Comparing ${files.length} files\n`);

const result = spawnSync(process.execPath, [bundle], {
    stdio: 'inherit',
    env: {
        ...process.env,
        CORPUS_LIST: corpusList,
        PYRIGHT_GO_TOKENSERVER: serverPath,
        PYRIGHT_SAMPLES_DIR: path.join(refSrc, 'tests', 'samples'),
    },
});

fs.rmSync(outDir, { recursive: true, force: true });
process.exit(result.status ?? 1);
