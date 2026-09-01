#!/usr/bin/env node
/*
 * run-ts-tests.js
 *
 * Runs pyright's own TypeScript test files against the Go port.
 *
 * Rather than transliterating src/tests/*.test.ts into Go, this bundles the
 * unmodified test file with esbuild, aliasing the modules under test
 * (parser/tokenizer, parser/stringTokenUtils) to shims that forward to the Go
 * tokenserver binary. Everything else -- the enums the tests assert against,
 * TextRangeCollection, the assertions themselves -- is the original code.
 *
 * The tests only use the `test()` global and node's `assert`, so a ~30 line
 * harness stands in for jest and no npm install into the pyright packages is
 * needed.
 *
 * Usage:
 *   node run-ts-tests.js --ref <path to pyright-internal/src> \
 *                        --server <path to tokenserver binary> \
 *                        --esbuild <path to esbuild binary> \
 *                        [--test tokenizer.test.ts]
 */

'use strict';

const { execFileSync } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

function parseArgs(argv) {
    const args = {};
    for (let i = 2; i < argv.length; i += 2) {
        if (!argv[i].startsWith('--')) {
            throw new Error(`unexpected argument ${argv[i]}`);
        }
        args[argv[i].slice(2)] = argv[i + 1];
    }
    return args;
}

const args = parseArgs(process.argv);
const refSrc = path.resolve(args.ref);
const serverPath = path.resolve(args.server);
const esbuildPath = path.resolve(args.esbuild);
const testFile = args.test || 'tokenizer.test.ts';

for (const [label, target] of [
    ['--ref', refSrc],
    ['--server', serverPath],
    ['--esbuild', esbuildPath],
]) {
    if (!fs.existsSync(target)) {
        console.error(`${label} does not exist: ${target}`);
        process.exit(2);
    }
}

const bridgeDir = __dirname;
const outDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pyright-go-ts-bridge-'));
const entry = path.join(outDir, 'entry.ts');
const bundle = path.join(outDir, 'bundle.js');

// The harness supplies the jest globals the tests use and reports results.
fs.writeFileSync(
    entry,
    `
import './harness';
import ${JSON.stringify(path.join(refSrc, 'tests', testFile))};
import { report } from './harness';
report();
`
);

fs.writeFileSync(
    path.join(outDir, 'harness.ts'),
    `
type TestFn = () => void | Promise<void>;
const tests: { name: string; fn: TestFn }[] = [];

(globalThis as any).test = (name: string, fn: TestFn) => {
    tests.push({ name, fn });
};
(globalThis as any).it = (globalThis as any).test;
(globalThis as any).describe = (_name: string, fn: () => void) => fn();

export function report() {
    let passed = 0;
    const failures: { name: string; error: any }[] = [];
    const skipped: { name: string; reason: string }[] = [];

    for (const t of tests) {
        try {
            const result = t.fn();
            if (result && typeof (result as any).then === 'function') {
                throw new Error('async tests are not supported by this harness');
            }
            passed++;
        } catch (error) {
            // Tests that reach the unported analyzer are reported as skipped
            // rather than failed, so they cannot be mistaken for either.
            const message = String((error as any)?.message ?? error);
            if (message.includes('PYRIGHT_GO_BRIDGE_UNSUPPORTED')) {
                skipped.push({ name: t.name, reason: message.split(': ').slice(1).join(': ') });
            } else {
                failures.push({ name: t.name, error });
            }
        }
    }

    for (const s of skipped) {
        console.log('SKIP ' + s.name);
        console.log('     ' + s.reason);
    }

    for (const f of failures) {
        console.log('FAIL ' + f.name);
        const message = f.error && f.error.stack ? f.error.stack : String(f.error);
        console.log('     ' + message.split('\\n').slice(0, 6).join('\\n     '));
    }

    console.log('');
    console.log(
        passed +
            ' passed, ' +
            failures.length +
            ' failed, ' +
            skipped.length +
            ' skipped, ' +
            tests.length +
            ' total'
    );
    if (failures.length > 0) {
        process.exitCode = 1;
    }
}
`
);

const aliases = {
    // The modules under test, swapped for Go-backed shims.
    [path.join(refSrc, 'parser/tokenizer')]: path.join(bridgeDir, 'shim-tokenizer.ts'),
    [path.join(refSrc, 'parser/stringTokenUtils')]: path.join(bridgeDir, 'shim-stringTokenUtils.ts'),
    // testUtils pulls in the whole analyzer for one file-reading helper.
    [path.join(refSrc, 'tests/testUtils')]: path.join(bridgeDir, 'shim-testUtils.ts'),
};

// parser.test.ts needs a testUtils that actually parses, and a stand-in for the
// fourslash harness (which drives the unported analyzer).
if (testFile === 'parser.test.ts') {
    aliases[path.join(refSrc, 'tests/testUtils')] = path.join(bridgeDir, 'shim-parserTestUtils.ts');
    aliases[path.join(refSrc, 'tests/harness/fourslash/testState')] = path.join(bridgeDir, 'shim-testState.ts');
}

// Aliases that apply only to imports made by the test file itself -- matched on
// the exact importer, not the tests directory, because the harness modules in
// there (testHost, vfs) import the same modules for their own purposes and must
// keep the real ones. The type shims re-export const enums from the real
// analyzer/types and analyzer/typePrinter; if those modules' own relative
// imports were rewritten too, the originals would end up wired to the shims
// instead of to each other.
const testOnlyAliases = {};
if (testFile === 'typePrinter.test.ts') {
    testOnlyAliases[path.join(refSrc, 'analyzer/types')] = path.join(bridgeDir, 'shim-types.ts');
    testOnlyAliases[path.join(refSrc, 'analyzer/typePrinter')] = path.join(bridgeDir, 'shim-typePrinter.ts');
}

// typeCacheUtils.test.ts builds TypeVars, so it needs the same construction-log
// shim; the cache shim reads that log.
if (testFile === 'typeCacheUtils.test.ts') {
    testOnlyAliases[path.join(refSrc, 'analyzer/types')] = path.join(bridgeDir, 'shim-types.ts');
    testOnlyAliases[path.join(refSrc, 'analyzer/typeCacheUtils')] = path.join(bridgeDir, 'shim-typeCacheUtils.ts');
}

// uri.test.ts drives the Uri classes, which are shimmed as recipes; see
// shim-uri.ts. pathUtils comes along because the test imports it directly, and
// uriUtils because it is the module the Uri-taking helpers live in.
if (testFile === 'uri.test.ts') {
    testOnlyAliases[path.join(refSrc, 'common/uri/uri')] = path.join(bridgeDir, 'shim-uri.ts');
    testOnlyAliases[path.join(refSrc, 'common/uri/uriUtils')] = path.join(bridgeDir, 'shim-uriUtils.ts');
    testOnlyAliases[path.join(refSrc, 'common/pathUtils')] = path.join(bridgeDir, 'shim-pathUtils.ts');
}

// pathUtils is pure string functions, like symbolNameUtils below.
if (testFile === 'pathUtils.test.ts') {
    testOnlyAliases[path.join(refSrc, 'common/pathUtils')] = path.join(bridgeDir, 'shim-pathUtils.ts');
}

// symbolNameUtils is pure string predicates, so the shim needs nothing else.
if (testFile === 'symbolNameUtils.test.ts') {
    testOnlyAliases[path.join(refSrc, 'analyzer/symbolNameUtils')] = path.join(
        bridgeDir,
        'shim-symbolNameUtils.ts'
    );
}

const aliasPlugin = `
const path = require('path');
const aliases = ${JSON.stringify(aliases)};
const testOnlyAliases = ${JSON.stringify(testOnlyAliases)};
const testFilePath = ${JSON.stringify(path.join(refSrc, 'tests', testFile))};
module.exports = {
    name: 'go-bridge-alias',
    setup(build) {
        build.onResolve({ filter: /.*/ }, (a) => {
            if (a.kind === 'entry-point' || !a.importer) return undefined;
            if (a.path.startsWith('@pyright/')) {
                return { path: path.join(${JSON.stringify(refSrc)}, a.path.slice('@pyright/'.length)) + '.ts' };
            }
            if (!a.path.startsWith('.')) return undefined;
            const resolved = path.resolve(path.dirname(a.importer), a.path);
            if (aliases[resolved]) return { path: aliases[resolved] };
            if (testOnlyAliases[resolved] && a.importer === testFilePath) {
                return { path: testOnlyAliases[resolved] };
            }
            return undefined;
        });
    },
};
`;

// esbuild's CLI cannot load plugins, so the alias mapping is applied by
// pre-resolving through a tiny JS build script that uses the esbuild JS API.
const nodeModulesDir = findNodeModules(esbuildPath);
const buildScript = path.join(outDir, 'build.js');
fs.writeFileSync(path.join(outDir, 'alias-plugin.js'), aliasPlugin);
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
    // The entry point lives in a temp directory, so Node's own resolution
    // would not find the repo's node_modules; uri.test.ts reaches fs-extra and
    // realFileSystem.ts reaches tmp.
    nodePaths: [${JSON.stringify(nodeModulesDir)}],
    // pyright's tsconfig enables legacy decorators; esbuild needs to be told,
    // because the entry point lives outside the pyright source tree and so does
    // not pick up its tsconfig.
    tsconfigRaw: { compilerOptions: { experimentalDecorators: true, useDefineForClassFields: false } },
    logLevel: 'error',
}).catch((e) => { console.error(e); process.exit(1); });
`
);

// Resolve the esbuild JS API next to the binary that was passed in.
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

function findEsbuildPackage(binPath) {
    // .../node_modules/@esbuild/<platform>/bin/esbuild -> .../node_modules/esbuild
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

execFileSync(process.execPath, [buildScript], { stdio: 'inherit' });

console.log(`Running ${testFile} against the Go implementation\n`);

const env = {
    ...process.env,
    PYRIGHT_GO_TOKENSERVER: serverPath,
    PYRIGHT_SAMPLES_DIR: path.join(refSrc, 'tests', 'samples'),
    ESBUILD_BINARY_PATH: esbuildPath,
};

// jest runs with rootDir -- packages/pyright-internal -- as the working
// directory, and a few tests reach the disk relative to it (uri.test.ts's
// Realcase pair reads process.cwd()/src/tests). Run the bundle the same way.
const result = require('child_process').spawnSync(process.execPath, [bundle], {
    stdio: 'inherit',
    cwd: path.dirname(refSrc),
    env,
});

fs.rmSync(outDir, { recursive: true, force: true });
process.exit(result.status ?? 1);
