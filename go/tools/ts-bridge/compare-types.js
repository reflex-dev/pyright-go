#!/usr/bin/env node
/*
 * compare-types.js
 *
 * Per-node type differential. For every file in a corpus it asks both the
 * original TypeScript evaluator and the Go port for the type of every name, and
 * diffs the answers one name at a time.
 *
 * This is the check that makes a 30,000-line evaluator tractable. The Stage D
 * gate (make bridge-evaluator-tests) tells you a file reported three errors
 * instead of two; that is nearly useless for finding out which of the
 * expressions in it went wrong. This says the name is `x` at pre-order index
 * 412, that pyright called it `list[int]` and the port called it `list[Unknown]`.
 *
 * The walk is pyright's own. testUtils.ts installs a pre-check callback that
 * runs NameTypeWalker over every file before checking it -- so the corpus is
 * already exercised this way by pyright's own tests -- and dump-types.ts and
 * cmd/tokenserver/nodetypes.go both apply exactly that filter.
 *
 * Two scoreboards, because they fail for different reasons and conflating them
 * would hide the more basic failure behind the more interesting one:
 *
 *   - the *node sets*, which say both sides walked the same tree and picked out
 *     the same names.
 *   - the *types*, which is the number that climbs as the evaluator arrives.
 *
 * The node sets were expected to be green from the first day, on the grounds
 * that this is a syntactic question and bridge-ast already shows the parse trees
 * agree over the whole corpus. They are not, and the reason is worth knowing:
 * **the evaluator mutates the parse tree**. typeEvaluator.ts:30085 parses a
 * string annotation on demand and grafts the result onto the StringListNode --
 *
 *     node.d.annotation = parseResults.parseTree;
 *
 * -- so after analysis the tree has sub-expressions in it that were not there
 * when the parser finished. In `a: Annotated[Annotated[int, "hi"], "hi"]` the
 * TypeScript side ends up with two extra NameNodes for `hi`, and every pre-order
 * index after them shifts by two.
 *
 * So node-set agreement is not a fixed property of the two parsers; it is a
 * measure of how much of the evaluator's tree-grafting has been ported. It will
 * reach every file when the string-annotation path lands, and until then a
 * disagreement here means "that file has a forward reference", not "the parsers
 * differ". The parser's own share of this is already ported --
 * parser.ts:5217 is parser_strings.go:302 -- it is the evaluator's share that
 * is missing.
 *
 * Usage:
 *   node compare-types.js --ref <pyright-internal/src> \
 *                         --server <tokenserver> \
 *                         --esbuild <esbuild binary> \
 *                         [--dir <corpus dir>] [--limit N] [--ts-only 1]
 *
 * --ts-only runs the TypeScript oracle alone and reports what it produced. It
 * exists so the harness can be validated before the Go evaluator does anything,
 * which is the same discipline the binder differential was built under.
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
const outDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pyright-go-types-'));
const entry = path.join(outDir, 'entry.ts');
const bundle = path.join(outDir, 'bundle.js');

fs.writeFileSync(
    entry,
    `
import * as fs from 'fs';
import { dumpTypes } from ${JSON.stringify(path.join(bridgeDir, 'dump-types.ts'))};

const files: string[] = JSON.parse(fs.readFileSync(process.env.CORPUS_LIST!, 'utf8'));
const out = fs.openSync(process.env.CORPUS_OUT!, 'w');

for (const file of files) {
    let record: any;
    try {
        record = { file, result: dumpTypes(file) };
    } catch (e: any) {
        record = { file, error: String(e && e.stack ? e.stack : e) };
    }
    fs.writeSync(out, JSON.stringify(record) + '\\n');
}

fs.closeSync(out);
`
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

const nodeModulesDir = findNodeModules(esbuildPath);

// jsonc-parser resolves a submodule with a dynamic require and smol-toml is
// loaded with a dynamic import, neither of which esbuild can follow; both are
// left external so Node resolves them at runtime. The evaluator's import chain
// reaches configOptions, which reaches both.
const externalPackages = {
    'jsonc-parser': path.join(nodeModulesDir, 'jsonc-parser', 'lib', 'umd', 'main.js'),
    'smol-toml': path.join(nodeModulesDir, 'smol-toml', 'dist', 'index.cjs'),
};

const aliasPlugin = `
const path = require('path');
const externalPackages = ${JSON.stringify(externalPackages)};
module.exports = {
    name: 'pyright-alias',
    setup(build) {
        build.onResolve({ filter: /.*/ }, (a) => {
            if (externalPackages[a.path]) {
                return { path: externalPackages[a.path], external: true };
            }
            if (a.path.startsWith('@pyright/')) {
                return { path: path.join(${JSON.stringify(refSrc)}, a.path.slice('@pyright/'.length)) + '.ts' };
            }
            return undefined;
        });
    },
};
`;

fs.writeFileSync(path.join(outDir, 'alias-plugin.js'), aliasPlugin);
fs.writeFileSync(
    path.join(bridgeDir, 'esbuild-shim.js'),
    `module.exports = require(${JSON.stringify(findEsbuildPackage(esbuildPath))});\n`
);

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
    nodePaths: [${JSON.stringify(nodeModulesDir)}],
    tsconfigRaw: { compilerOptions: { experimentalDecorators: true, useDefineForClassFields: false } },
    logLevel: 'error',
}).catch((e) => { console.error(e); process.exit(1); });
`
);

function listCorpus(dir) {
    const out = [];
    for (const dirEntry of fs.readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, dirEntry.name);
        if (dirEntry.isDirectory()) {
            out.push(...listCorpus(full));
        } else if (dirEntry.name.endsWith('.py') || dirEntry.name.endsWith('.pyi')) {
            out.push(full);
        }
    }
    return out;
}

const files = listCorpus(corpusDir).sort().slice(0, limit);
const listPath = path.join(outDir, 'files.json');
fs.writeFileSync(listPath, JSON.stringify(files));

execFileSync(process.execPath, [buildScript], { stdio: 'inherit' });

const tsOutPath = path.join(outDir, 'ts.jsonl');
console.log(`=== ${files.length} files`);

// The oracle runs with the reference tree as the working directory, because
// testUtils.ts sets global.__rootDirectory from it and that is what locates the
// bundled typeshed.
execFileSync(process.execPath, ['--max-old-space-size=8192', bundle], {
    stdio: 'inherit',
    cwd: path.dirname(refSrc),
    env: { ...process.env, CORPUS_LIST: listPath, CORPUS_OUT: tsOutPath },
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
    let names = 0;
    let unreachable = 0;
    let none = 0;
    let errors = 0;
    let failures = 0;
    const distinct = new Set();

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
        for (const node of record.result) {
            names++;
            if (node.type === '<unreachable>') unreachable++;
            else if (node.type === '<none>') none++;
            else if (node.type.startsWith('<error>')) errors++;
            else distinct.add(node.type);
        }
    }

    console.log(
        `oracle typed ${names} names (${distinct.size} distinct types); ` +
            `${unreachable} unreachable, ${none} untyped, ${errors} threw; ` +
            `${failures} files failed`
    );
    process.exit(failures === 0 ? 0 : 1);
}

const requests = files.map((file) => JSON.stringify({ op: 'nodetypes', payload: { filePath: file } })).join('\n');

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

let nodesMatched = 0;
let nodesDiffered = 0;
let filesWithDifferentNodeSets = 0;
// Node-set failures get their own list. They are the more basic failure and a
// shared cap would let a noisy type diff crowd them out of the report entirely
// -- which is the conflation the two scoreboards exist to avoid.
const nodeSetExamples = [];
const examples = [];

for (let i = 0; i < files.length; i++) {
    const goEnvelope = JSON.parse(goLines[i]);
    const tsRecord = JSON.parse(tsLines[i]);
    const relative = path.relative(corpusDir, files[i]);

    if (goEnvelope.error || tsRecord.error) {
        filesWithDifferentNodeSets++;
        if (nodeSetExamples.length < 25) {
            nodeSetExamples.push(
                `ERROR ${relative}\n    go: ${goEnvelope.error ?? '-'}\n    ts: ${(tsRecord.error ?? '-').split('\n')[0]}`
            );
        }
        continue;
    }

    const goNodes = goEnvelope.result.nodes;
    const tsNodes = tsRecord.result;

    // The node sets first: if the two sides did not pick out the same names,
    // comparing their types position by position would compare unrelated
    // things and report a misleading number.
    const goKeys = goNodes.map((n) => `${n.index}:${n.name}`).join(',');
    const tsKeys = tsNodes.map((n) => `${n.index}:${n.name}`).join(',');
    if (goKeys !== tsKeys) {
        filesWithDifferentNodeSets++;
        if (nodeSetExamples.length < 25) {
            nodeSetExamples.push(
                `NODES ${relative}: go walked ${goNodes.length} names, ts walked ${tsNodes.length}\n` +
                    `    first divergence: ${firstDivergence(goNodes, tsNodes)}`
            );
        }
        nodesDiffered += Math.max(goNodes.length, tsNodes.length);
        continue;
    }

    for (let n = 0; n < tsNodes.length; n++) {
        if (goNodes[n].type === tsNodes[n].type) {
            nodesMatched++;
        } else {
            nodesDiffered++;
            if (examples.length < 25) {
                examples.push(
                    `${relative}:${tsNodes[n].index} ${tsNodes[n].name}\n` +
                        `    go=${JSON.stringify(goNodes[n].type)}\n    ts=${JSON.stringify(tsNodes[n].type)}`
                );
            }
        }
    }
}

for (const example of nodeSetExamples) {
    console.log(example);
}
for (const example of examples) {
    console.log(example);
}

function firstDivergence(goNodes, tsNodes) {
    for (let i = 0; i < Math.max(goNodes.length, tsNodes.length); i++) {
        const g = goNodes[i] ? `${goNodes[i].index}:${goNodes[i].name}` : '-';
        const t = tsNodes[i] ? `${tsNodes[i].index}:${tsNodes[i].name}` : '-';
        if (g !== t) {
            return `at position ${i}, go=${g} ts=${t}`;
        }
    }
    return 'none';
}

const total = nodesMatched + nodesDiffered;
console.log('');
console.log(
    `node sets: ${files.length - filesWithDifferentNodeSets} of ${files.length} files agree on which names to type`
);
console.log(`types: ${nodesMatched} of ${total} names match`);

if (!process.env.KEEP_OUT) fs.rmSync(outDir, { recursive: true, force: true });
else console.log('kept: ' + outDir);
process.exit(nodesDiffered === 0 && filesWithDifferentNodeSets === 0 ? 0 : 1);
