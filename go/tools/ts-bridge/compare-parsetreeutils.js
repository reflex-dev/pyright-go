#!/usr/bin/env node
/*
 * compare-parsetreeutils.js
 *
 * Differential test for analyzer/parseTreeUtils.ts. For every file in a corpus
 * it walks every parse node and evaluates every parseTreeUtils function that
 * does not need a bound scope, with both the original TypeScript and the Go
 * port, then reports any difference.
 *
 * This exists because parseTreeUtils.test.ts cannot be bridged: it drives the
 * fourslash harness, which needs the binder and the import resolver. See
 * dump-parsetreeutils.ts for the list of functions this deliberately skips.
 *
 * The same two normalizations as compare-ast.js apply, because Go has zero
 * values where TypeScript has `undefined`:
 *
 *   - A missing property is equal to `false`.
 *   - A missing property is equal to an empty array.
 *
 * Usage:
 *   node compare-parsetreeutils.js --ref <pyright-internal/src> \
 *                                  --server <tokenserver> \
 *                                  --esbuild <esbuild binary> \
 *                                  [--dir <corpus dir>] [--limit N]
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
const corpusDir = path.resolve(args.dir || path.join(refSrc, 'tests', 'samples'));

for (const [label, target] of [
    ['--ref', refSrc],
    ['--server', serverPath],
    ['--esbuild', esbuildPath],
    ['--dir', corpusDir],
]) {
    if (!fs.existsSync(target)) {
        console.error(`${label} does not exist: ${target}`);
        process.exit(2);
    }
}

const bridgeDir = __dirname;
const outDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pyright-go-ptu-'));
const entry = path.join(outDir, 'entry.ts');
const bundle = path.join(outDir, 'bundle.js');

// The TypeScript side runs inside a bundle so it can import the real parser.
// It writes one JSON dump per corpus file; the comparison itself happens back
// here, in plain Node, against the Go server's output.
fs.writeFileSync(
    entry,
    `
import * as fs from 'fs';
import { dump } from ${JSON.stringify(path.join(bridgeDir, 'dump-parsetreeutils.ts'))};

const files: string[] = JSON.parse(fs.readFileSync(process.env.CORPUS_LIST!, 'utf8'));
const out = fs.openSync(process.env.CORPUS_OUT!, 'w');

for (const file of files) {
    const text = fs.readFileSync(file, 'utf8');
    let record: any;
    try {
        record = { file, result: dump(text) };
    } catch (e: any) {
        record = { file, error: String(e && e.stack ? e.stack : e) };
    }
    fs.writeSync(out, JSON.stringify(record) + '\\n');
}

fs.closeSync(out);
`
);

const aliasPlugin = `
const path = require('path');
module.exports = {
    name: 'pyright-alias',
    setup(build) {
        build.onResolve({ filter: /^@pyright\\// }, (a) => ({
            path: path.join(${JSON.stringify(refSrc)}, a.path.slice('@pyright/'.length)) + '.ts',
        }));
    },
};
`;

fs.writeFileSync(path.join(outDir, 'alias-plugin.js'), aliasPlugin);

const esbuildPkg = findEsbuildPackage(esbuildPath);
fs.writeFileSync(
    path.join(bridgeDir, 'esbuild-shim.js'),
    `module.exports = require(${JSON.stringify(esbuildPkg)});\n`
);

// The bundle entry point lives in a temp directory, so esbuild's own
// node_modules walk starts there and never reaches the installed packages. The
// dump's import chain reaches common/uri/uri.ts, which imports vscode-uri.
function findNodeModules(startPath) {
    let dir = path.dirname(startPath);
    while (dir !== path.dirname(dir)) {
        const candidate = path.join(dir, 'node_modules');
        if (fs.existsSync(candidate)) {
            return candidate;
        }
        dir = path.dirname(dir);
    }
    throw new Error(`could not locate node_modules near ${startPath}`);
}

function findEsbuildPackage(binPath) {
    let dir = path.dirname(binPath);
    while (dir !== path.dirname(dir)) {
        const candidate = path.join(dir, 'node_modules', 'esbuild');
        if (fs.existsSync(candidate)) {
            return candidate;
        }
        dir = path.dirname(dir);
    }
    throw new Error(`could not locate the esbuild package near ${binPath}`);
}

const buildScript = path.join(outDir, 'build.js');
fs.writeFileSync(
    buildScript,
    `
const esbuild = require(${JSON.stringify(path.join(bridgeDir, 'esbuild-shim.js'))});
esbuild.build({
    entryPoints: [${JSON.stringify(entry)}],
    bundle: true,
    platform: 'node',
    format: 'cjs',
    outfile: ${JSON.stringify(bundle)},
    plugins: [require(${JSON.stringify(path.join(outDir, 'alias-plugin.js'))})],
    nodePaths: [${JSON.stringify(findNodeModules(esbuildPath))}],
    // pyright's tsconfig enables legacy decorators; esbuild needs to be told,
    // because the bundle entry point lives outside the pyright source tree and
    // so does not pick up its tsconfig.
    tsconfigRaw: { compilerOptions: { experimentalDecorators: true, useDefineForClassFields: false } },
    logLevel: 'error',
}).catch((e) => { console.error(e); process.exit(1); });
`
);

function listCorpus(dir) {
    const out = [];
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, entry.name);
        if (entry.isDirectory()) {
            out.push(...listCorpus(full));
        } else if (entry.name.endsWith('.py') || entry.name.endsWith('.pyi')) {
            out.push(full);
        }
    }
    return out;
}

const files = listCorpus(corpusDir).sort().slice(0, limit);
if (files.length === 0) {
    console.error(`no .py files found under ${corpusDir}`);
    process.exit(2);
}

const listPath = path.join(outDir, 'corpus.json');
const tsOutPath = path.join(outDir, 'ts.jsonl');
fs.writeFileSync(listPath, JSON.stringify(files));

execFileSync(process.execPath, [buildScript], { stdio: 'inherit' });

console.log(`Comparing ${files.length} files\n`);

execFileSync(process.execPath, [bundle], {
    stdio: 'inherit',
    env: { ...process.env, CORPUS_LIST: listPath, CORPUS_OUT: tsOutPath },
});

// Ask the Go server for all of them in one batch: one JSON request per line.
function codeUnits(s) {
    const a = new Array(s.length);
    for (let i = 0; i < s.length; i++) {
        a[i] = s.charCodeAt(i);
    }
    return a;
}

const requests = files
    .map((file) => JSON.stringify({ op: 'parsetreeutils', text: codeUnits(fs.readFileSync(file, 'utf8')) }))
    .join('\n');

const goRun = spawnSync(serverPath, [], {
    input: requests,
    maxBuffer: 2 * 1024 * 1024 * 1024,
    encoding: 'utf8',
});
if (goRun.status !== 0) {
    console.error(`tokenserver exited with ${goRun.status}: ${goRun.stderr}`);
    process.exit(1);
}
const goLines = goRun.stdout.split('\n').filter((line) => line.length > 0);
const tsLines = fs.readFileSync(tsOutPath, 'utf8').split('\n').filter((line) => line.length > 0);

if (goLines.length !== files.length || tsLines.length !== files.length) {
    console.error(`response count mismatch: go=${goLines.length} ts=${tsLines.length} files=${files.length}`);
    process.exit(1);
}

// `undefined` on one side is equal to `false` or `[]` on the other, because Go
// has a zero value where TypeScript simply omits the property.
function normalized(value) {
    if (value === undefined || value === null) {
        return undefined;
    }
    if (value === false) {
        return undefined;
    }
    if (Array.isArray(value) && value.length === 0) {
        return undefined;
    }
    return value;
}

function diff(a, b, pathStr, out) {
    if (out.length >= 5) {
        return;
    }

    const na = normalized(a);
    const nb = normalized(b);

    if (na === undefined && nb === undefined) {
        return;
    }
    if (na === undefined || nb === undefined) {
        out.push(`${pathStr}: go=${JSON.stringify(a)} ts=${JSON.stringify(b)}`);
        return;
    }

    if (Array.isArray(na) || Array.isArray(nb)) {
        if (!Array.isArray(na) || !Array.isArray(nb) || na.length !== nb.length) {
            out.push(`${pathStr}: go=${JSON.stringify(na)?.slice(0, 120)} ts=${JSON.stringify(nb)?.slice(0, 120)}`);
            return;
        }
        for (let i = 0; i < na.length; i++) {
            diff(na[i], nb[i], `${pathStr}[${i}]`, out);
        }
        return;
    }

    if (typeof na === 'object' && typeof nb === 'object') {
        for (const key of new Set([...Object.keys(na), ...Object.keys(nb)])) {
            diff(na[key], nb[key], `${pathStr}.${key}`, out);
        }
        return;
    }

    if (na !== nb) {
        out.push(`${pathStr}: go=${JSON.stringify(na)} ts=${JSON.stringify(nb)}`);
    }
}

let matched = 0;
const mismatched = [];

for (let i = 0; i < files.length; i++) {
    const goEnvelope = JSON.parse(goLines[i]);
    const tsRecord = JSON.parse(tsLines[i]);

    if (goEnvelope.error || tsRecord.error) {
        mismatched.push({
            file: files[i],
            diffs: [`go error: ${goEnvelope.error ?? '-'}`, `ts error: ${tsRecord.error ?? '-'}`],
        });
        continue;
    }

    const diffs = [];
    diff(goEnvelope.result, tsRecord.result, '', diffs);
    if (diffs.length === 0) {
        matched++;
    } else {
        mismatched.push({ file: files[i], diffs });
    }
}

for (const entry of mismatched.slice(0, 25)) {
    console.log(`MISMATCH ${path.relative(corpusDir, entry.file)}`);
    for (const d of entry.diffs) {
        console.log(`    ${d}`);
    }
}

console.log('');
console.log(`${matched} identical, ${mismatched.length} different, ${files.length} total`);

if (!process.env.KEEP_OUT) fs.rmSync(outDir, { recursive: true, force: true });
else console.log("kept: " + outDir);
process.exit(mismatched.length === 0 ? 0 : 1);
