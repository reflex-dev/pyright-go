#!/usr/bin/env node
/*
 * compare-config.js
 *
 * Differential test for analyzer/service.ts's config path. For every project
 * directory in the corpus it builds an AnalyzerService with both the original
 * TypeScript and the Go port and diffs the whole resulting ConfigOptions --
 * every scalar, every file spec's compiled regular expression, the 96-field
 * diagnostic rule set, and every execution environment -- plus the list of
 * files the source enumerator finds.
 *
 * This stands in for config.test.ts, which is not bridgeable: it constructs
 * ExecutionEnvironments in TypeScript, mutates ConfigOptions and
 * CommandLineOptions in place, and asserts on object identity. See
 * dump-config.ts.
 *
 * Each project is run twice, as a language server and as the command line,
 * because the two take different branches through _getConfigOptions: the
 * command line walks up the directory tree looking for a config file and
 * applies its own overrides afterwards, and a language server does neither.
 *
 * Usage:
 *   node compare-config.js --ref <pyright-internal/src> \
 *                          --server <tokenserver> \
 *                          --esbuild <esbuild binary> \
 *                          [--dir <corpus dir>] [--ts-only 1]
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
const tsOnly = args['ts-only'] === '1';
const corpusDir = path.resolve(args.dir || path.join(refSrc, 'tests', 'samples'));

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
const outDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pyright-go-config-'));
const entry = path.join(outDir, 'entry.ts');
const bundle = path.join(outDir, 'bundle.js');

fs.writeFileSync(
    entry,
    `
import * as fs from 'fs';
import { dumpConfig, prepare } from ${JSON.stringify(path.join(bridgeDir, 'dump-config.ts'))};

async function main() {
    await prepare();

    const projects: string[] = JSON.parse(fs.readFileSync(process.env.CORPUS_LIST!, 'utf8'));
    const out = fs.openSync(process.env.CORPUS_OUT!, 'w');

    for (const project of projects) {
        let record: any;
        try {
            record = { project, result: dumpConfig(project, process.env.FROM_LANGUAGE_SERVER === '1') };
        } catch (e: any) {
            record = { project, error: String(e && e.stack ? e.stack : e) };
        }
        fs.writeSync(out, JSON.stringify(record) + '\\n');
    }

    fs.closeSync(out);
}

main().catch((e) => { console.error(e); process.exit(1); });
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
fs.writeFileSync(
    path.join(bridgeDir, 'esbuild-shim.js'),
    `module.exports = require(${JSON.stringify(findEsbuildPackage(esbuildPath))});\n`
);

// jsonc-parser resolves a submodule with a dynamic require and smol-toml is
// loaded with a dynamic import, neither of which esbuild can follow. Both are
// rewritten to an absolute path and left external, so Node resolves them at
// runtime from wherever the bundle happens to live.
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

// The corpus is every directory under tests/samples that looks like a project:
// one that contains a pyrightconfig.json or a pyproject.toml, plus the handful
// config.test.ts drives that contain neither (so the "no config file found"
// path is covered too).
function listProjects(dir) {
    const out = [];
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        if (!entry.isDirectory()) {
            continue;
        }
        const full = path.join(dir, entry.name);
        if (entry.name.startsWith('project')) {
            out.push(full);
            // Sub-projects: project_with_empty_pyproject_toml/subproject and
            // friends exercise the walk up the directory tree.
            for (const sub of fs.readdirSync(full, { withFileTypes: true })) {
                if (sub.isDirectory() && !sub.name.startsWith('.')) {
                    out.push(path.join(full, sub.name));
                }
            }
        }
    }
    return out;
}

const projects = listProjects(corpusDir).sort();
if (projects.length === 0) {
    console.error(`no project directories found under ${corpusDir}`);
    process.exit(2);
}

const listPath = path.join(outDir, 'corpus.json');
fs.writeFileSync(listPath, JSON.stringify(projects));

execFileSync(process.execPath, [buildScript], { stdio: 'inherit' });

// `undefined` on one side is equal to `false`, `''` or `[]` on the other,
// because Go has a zero value where TypeScript simply omits the property. This
// is the same normalization compare-ast.js and compare-binder.js apply.
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
    if (out.length >= 8) {
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
            out.push(`${pathStr}: go=${JSON.stringify(na)?.slice(0, 200)} ts=${JSON.stringify(nb)?.slice(0, 200)}`);
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

const modes = [
    { label: 'command line', fromLanguageServer: false },
    { label: 'language server', fromLanguageServer: true },
];

let totalMatched = 0;
let totalMismatched = 0;

for (const mode of modes) {
    const tsOutPath = path.join(outDir, `ts-${mode.fromLanguageServer ? 'ls' : 'cli'}.jsonl`);

    console.log(`\n=== ${mode.label}: ${projects.length} projects`);

    execFileSync(process.execPath, [bundle], {
        stdio: 'inherit',
        env: {
            ...process.env,
            CORPUS_LIST: listPath,
            CORPUS_OUT: tsOutPath,
            FROM_LANGUAGE_SERVER: mode.fromLanguageServer ? '1' : '0',
            NODE_PATH: nodeModulesDir,
        },
        // config discovery is relative to the process's working directory in
        // one branch; jest runs from packages/pyright-internal.
        cwd: path.dirname(refSrc),
    });

    const tsLines = fs
        .readFileSync(tsOutPath, 'utf8')
        .split('\n')
        .filter((line) => line.length > 0);

    if (tsOnly) {
        let ok = 0;
        for (const line of tsLines) {
            const record = JSON.parse(line);
            if (record.error) {
                console.log(`ERROR ${record.project}\n     ${record.error.split('\n')[0]}`);
            } else {
                ok++;
            }
        }
        console.log(`${ok}/${tsLines.length} projects produced a config`);
        continue;
    }

    let matched = 0;
    let mismatched = 0;

    for (const line of tsLines) {
        const tsRecord = JSON.parse(line);

        const response = spawnSync(
            serverPath,
            [],
            {
                input: JSON.stringify({
                    op: 'config',
                    payload: { projectRoot: tsRecord.project, fromLanguageServer: mode.fromLanguageServer },
                }),
                maxBuffer: 256 * 1024 * 1024,
                encoding: 'utf8',
                cwd: path.dirname(refSrc),
            }
        );

        if (response.status !== 0) {
            console.log(`FAIL ${tsRecord.project}\n     go server exited ${response.status}: ${response.stderr}`);
            mismatched++;
            continue;
        }

        const parsed = JSON.parse(response.stdout);
        if (parsed.error !== undefined) {
            console.log(`FAIL ${tsRecord.project}\n     go: ${parsed.error}`);
            mismatched++;
            continue;
        }

        if (tsRecord.error) {
            console.log(`FAIL ${tsRecord.project}\n     ts: ${tsRecord.error.split('\n')[0]}`);
            mismatched++;
            continue;
        }

        const differences = [];
        diff(parsed.result, tsRecord.result, '', differences);

        if (differences.length === 0) {
            matched++;
        } else {
            mismatched++;
            console.log(`DIFF ${tsRecord.project}`);
            for (const d of differences) {
                console.log(`     ${d}`);
            }
        }
    }

    console.log(`${matched} identical, ${mismatched} different, ${tsLines.length} total`);
    totalMatched += matched;
    totalMismatched += mismatched;
}

fs.rmSync(outDir, { recursive: true, force: true });

if (!tsOnly) {
    console.log(`\n${totalMatched} identical, ${totalMismatched} different, ${totalMatched + totalMismatched} total`);
    process.exit(totalMismatched > 0 ? 1 : 0);
}
