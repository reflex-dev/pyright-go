#!/usr/bin/env node
/*
 * compare-binder.js
 *
 * Differential test for analyzer/binder.ts. For every file in a corpus it binds
 * the module with both the original TypeScript and the Go port and diffs
 * everything the binder produces: the scope tree, every symbol's flags and
 * declarations, the code-flow graph, the __all__ info and the bind diagnostics.
 *
 * This existed before the binder was written, deliberately. A
 * single wrong code-flow edge produces no visible symptom until some narrowing
 * test tens of thousands of lines later fails for reasons nobody can trace, so
 * catching it here is worth a great deal.
 *
 * The run happens twice over the corpus, in two import modes -- every import
 * resolves, and none does -- because the harness synthesizes every
 * ImportResult itself and can answer either way. See dump-binder.ts for
 * what that leaves uncovered.
 *
 * The same two normalizations as compare-ast.js apply, because Go has zero
 * values where TypeScript has `undefined`:
 *
 *   - A missing property is equal to `false`.
 *   - A missing property is equal to an empty array.
 *   - A missing property is equal to an empty string. This one is new here:
 *     Go string fields like ParamDeclaration.InferredName have "" where the
 *     TypeScript leaves the property undefined, and a plain Go string cannot
 *     represent the difference.
 *
 * Usage:
 *   node compare-binder.js --ref <pyright-internal/src> \
 *                          --server <tokenserver> \
 *                          --esbuild <esbuild binary> \
 *                          [--dir <corpus dir>] [--limit N] [--ts-only 1]
 *
 * --ts-only runs the TypeScript oracle alone and reports what it produced. It
 * exists so the harness can be validated before the Go binder exists.
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
// --server is not needed in --ts-only mode, where the oracle runs alone.
const serverPath = args.server ? path.resolve(args.server) : undefined;
const esbuildPath = path.resolve(args.esbuild);
const limit = args.limit ? Number(args.limit) : Infinity;
const corpusDir = path.resolve(args.dir || path.join(refSrc, 'tests', 'samples'));
const tsOnly = args['ts-only'] === '1';

for (const [label, target] of [
    ['--ref', refSrc],
    ...(tsOnly ? [] : [['--server', serverPath]]),
    ['--esbuild', esbuildPath],
    ['--dir', corpusDir],
]) {
    if (!fs.existsSync(target)) {
        console.error(`${label} does not exist: ${target}`);
        process.exit(2);
    }
}

const bridgeDir = __dirname;
const outDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pyright-go-binder-'));
const entry = path.join(outDir, 'entry.ts');
const bundle = path.join(outDir, 'bundle.js');

// The TypeScript side runs inside a bundle so it can import the real parser.
// It writes one JSON dump per corpus file; the comparison itself happens back
// here, in plain Node, against the Go server's output.
fs.writeFileSync(
    entry,
    `
import * as fs from 'fs';
import { dump } from ${JSON.stringify(path.join(bridgeDir, 'dump-binder.ts'))};

const files: string[] = JSON.parse(fs.readFileSync(process.env.CORPUS_LIST!, 'utf8'));
const out = fs.openSync(process.env.CORPUS_OUT!, 'w');

for (const file of files) {
    const text = fs.readFileSync(file, 'utf8');
    let record: any;
    try {
        record = { file, result: dump(text, { importsResolve: process.env.IMPORTS_RESOLVE === '1' }) };
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

const nodeModulesDir = findNodeModules(esbuildPath);

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
    // The bundle entry point lives in a temp directory, so esbuild's own
    // node_modules walk starts there and never reaches the repo's. The binder's
    // import chain reaches cancellationUtils, which imports vscode-jsonrpc and
    // vscode-languageserver, so point esbuild at the real node_modules.
    nodePaths: [${JSON.stringify(nodeModulesDir)}],
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
fs.writeFileSync(listPath, JSON.stringify(files));

execFileSync(process.execPath, [buildScript], { stdio: 'inherit' });

// Ask the Go server for a whole mode in one batch: one JSON request per line.
function codeUnits(s) {
    const a = new Array(s.length);
    for (let i = 0; i < s.length; i++) {
        a[i] = s.charCodeAt(i);
    }
    return a;
}

const sources = files.map((file) => fs.readFileSync(file, 'utf8'));

// `undefined` on one side is equal to `false` or `[]` on the other, because Go
// has a zero value where TypeScript simply omits the property.
function normalized(value) {
    if (value === undefined || value === null) {
        return undefined;
    }
    if (value === false || value === '') {
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

// Both import modes, because the harness has to synthesize an ImportResult and
// the two answers drive different branches through visitModuleName and the
// import visitors.
const modes = [
    { label: 'imports resolve', resolve: true },
    { label: 'imports unresolved', resolve: false },
];

let totalMatched = 0;
let totalMismatched = 0;

for (const mode of modes) {
    const tsOutPath = path.join(outDir, `ts-${mode.resolve ? 'resolved' : 'unresolved'}.jsonl`);

    console.log(`\n=== ${mode.label}: ${files.length} files`);

    execFileSync(process.execPath, [bundle], {
        stdio: 'inherit',
        env: {
            ...process.env,
            CORPUS_LIST: listPath,
            CORPUS_OUT: tsOutPath,
            IMPORTS_RESOLVE: mode.resolve ? '1' : '0',
        },
    });

    const tsLines = fs
        .readFileSync(tsOutPath, 'utf8')
        .split('\n')
        .filter((line) => line.length > 0);

    if (tsLines.length !== files.length) {
        console.error(`ts response count mismatch: ts=${tsLines.length} files=${files.length}`);
        process.exit(1);
    }

    if (tsOnly) {
        // Validate the oracle on its own: report what it produced and which
        // files it could not bind at all.
        let scopes = 0;
        let symbols = 0;
        let flowNodes = 0;
        let decls = 0;
        let failures = 0;
        for (const line of tsLines) {
            const record = JSON.parse(line);
            if (record.error) {
                if (failures < 10) {
                    console.log(`ERROR ${path.relative(corpusDir, record.file)}`);
                    console.log(`    ${record.error.split('\n')[0]}`);
                }
                failures++;
                continue;
            }
            scopes += record.result.scopes.length;
            flowNodes += record.result.flows.length;
            for (const scope of record.result.scopes) {
                symbols += scope.symbols.length;
                for (const symbol of scope.symbols) {
                    decls += symbol.decls.length;
                }
            }
        }
        console.log(
            `oracle produced ${scopes} scopes, ${symbols} symbols, ${decls} declarations, ` +
                `${flowNodes} flow nodes; ${failures} files failed to bind`
        );
        totalMismatched += failures;
        totalMatched += files.length - failures;
        continue;
    }

    const requests = sources
        .map((text) => JSON.stringify({ op: 'binder', importsResolve: mode.resolve, text: codeUnits(text) }))
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
    if (goLines.length !== files.length) {
        console.error(`go response count mismatch: go=${goLines.length} files=${files.length}`);
        process.exit(1);
    }

    let matched = 0;
    const mismatched = [];

    for (let i = 0; i < files.length; i++) {
        const goEnvelope = JSON.parse(goLines[i]);
        const tsRecord = JSON.parse(tsLines[i]);

        if (goEnvelope.error || tsRecord.error) {
            mismatched.push({
                file: files[i],
                diffs: [`go error: ${goEnvelope.error ?? '-'}`, `ts error: ${(tsRecord.error ?? '-').split('\n')[0]}`],
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

    console.log(`${matched} identical, ${mismatched.length} different, ${files.length} total`);
    totalMatched += matched;
    totalMismatched += mismatched.length;
}

console.log('');
console.log(`${totalMatched} identical, ${totalMismatched} different, ${files.length * modes.length} total`);

if (!process.env.KEEP_OUT) fs.rmSync(outDir, { recursive: true, force: true });
else console.log("kept: " + outDir);
process.exit(totalMismatched === 0 ? 0 : 1);
