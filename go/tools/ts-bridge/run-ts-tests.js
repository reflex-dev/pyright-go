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
// --test takes a comma-separated list, because the evaluator tests are split
// across nine files that make up a single scoreboard.
const testFiles = (args.test || 'tokenizer.test.ts').split(',').map((name) => name.trim());
const testFile = testFiles[0];
const testFilePaths = testFiles.map((name) => path.join(refSrc, 'tests', name));

// The Stage D gate. Only that suite reports the substantive/vacuous split,
// because only its shim knows what each test expected.
const isEvaluatorGate = testFiles.some((name) => /^(typeEvaluator\d*|checker)\.test\.ts$/.test(name));

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
// The test files are pulled in with require() rather than import so they run in
// sequence with the beginFile() calls between them; ESM imports are hoisted, so
// every file would be registered before the first marker ran.
fs.writeFileSync(
    entry,
    `
import { beginFile, report } from './harness';
${testFiles
    .map(
        (name, i) =>
            `beginFile(${JSON.stringify(name)});\nrequire(${JSON.stringify(testFilePaths[i])});`
    )
    .join('\n')}
report();
`
);

fs.writeFileSync(
    path.join(outDir, 'harness.ts'),
    `
type TestFn = () => void | Promise<void>;
const tests: { name: string; fn: TestFn; file: string }[] = [];

let currentFile = '';
export function beginFile(name: string) {
    currentFile = name;
}

(globalThis as any).test = (name: string, fn: TestFn) => {
    tests.push({ name, fn, file: currentFile });
};
(globalThis as any).it = (globalThis as any).test;
(globalThis as any).describe = (_name: string, fn: () => void) => fn();

// importResolver.test.ts registers an afterAll to dispose a temp file. The
// hooks run in registration order after every test has, which is what jest
// does for a suite that never fails to complete.
const afterAllHooks: TestFn[] = [];
(globalThis as any).afterAll = (fn: TestFn) => {
    afterAllHooks.push(fn);
};
(globalThis as any).beforeAll = (fn: TestFn) => {
    fn();
};
(globalThis as any).afterEach = (_fn: TestFn) => {};
(globalThis as any).beforeEach = (_fn: TestFn) => {};

// One test in typeEvaluator4.test.ts asserts an exact diagnostic message with
// jest's expect().toBe rather than node's assert. That is the only matcher any
// of these files uses.
(globalThis as any).expect = (received: any) => ({
    toBe(expected: any) {
        if (!Object.is(received, expected)) {
            throw new Error(
                'expect(received).toBe(expected)\\n\\nExpected: ' +
                    JSON.stringify(expected) +
                    '\\nReceived: ' +
                    JSON.stringify(received)
            );
        }
    },
});

export function report() {
    let passed = 0;
    let substantivePasses = 0;
    let vacuousPasses = 0;
    const failures: { name: string; error: any }[] = [];
    const skipped: { name: string; reason: string }[] = [];
    const perFile = new Map<string, { passed: number; failed: number; skipped: number }>();

    for (const t of tests) {
        if (!perFile.has(t.file)) {
            perFile.set(t.file, { passed: 0, failed: 0, skipped: 0 });
        }
        const tally = perFile.get(t.file)!;

        // Set by the evaluator gate's validateResults when the test expects at
        // least one diagnostic. A test that expects none is passed by an
        // implementation that reports nothing, so those are counted apart.
        (globalThis as any).__pyrightGoSubstantive = false;
        try {
            const result = t.fn();
            if (result && typeof (result as any).then === 'function') {
                throw new Error('async tests are not supported by this harness');
            }
            passed++;
            tally.passed++;
            if ((globalThis as any).__pyrightGoSubstantive) {
                substantivePasses++;
            } else {
                vacuousPasses++;
            }
        } catch (error) {
            // Tests that reach the unported analyzer are reported as skipped
            // rather than failed, so they cannot be mistaken for either.
            const message = String((error as any)?.message ?? error);
            if (message.includes('PYRIGHT_GO_BRIDGE_UNSUPPORTED')) {
                skipped.push({ name: t.name, reason: message.split(': ').slice(1).join(': ') });
                tally.skipped++;
            } else {
                failures.push({ name: t.name, error });
                tally.failed++;
            }
        }
    }

    for (const hook of afterAllHooks) {
        try {
            hook();
        } catch {
            // A failing cleanup hook is not a test failure.
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
    if (perFile.size > 1) {
        for (const [file, tally] of perFile) {
            console.log(
                '  ' +
                    file.padEnd(28) +
                    tally.passed +
                    ' passed, ' +
                    tally.failed +
                    ' failed, ' +
                    tally.skipped +
                    ' skipped'
            );
        }
        console.log('');
    }
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
    if (${isEvaluatorGate}) {
        console.log(
            '  of the passes, ' +
                substantivePasses +
                ' assert at least one diagnostic and ' +
                vacuousPasses +
                ' assert none (an implementation that reports nothing passes those)'
        );

        // The frontier, accumulated by the shim across every analyze call. This
        // is the same ranked list the per-node differential prints, so the two
        // harnesses agree about what is missing rather than each having its own
        // account of it.
        const unported = globalThis.__pyrightGoUnported;
        const entries = Object.entries(unported ?? {}).sort((a, b) => b[1] - a[1]);
        if (entries.length > 0) {
            const total = entries.reduce((sum, [, count]) => sum + count, 0);
            console.log('');
            console.log(
                'unported paths reached: ' + entries.length + ' distinct, ' + total + ' hits'
            );
            for (const [name, count] of entries.slice(0, 15)) {
                console.log('  ' + String(count).padStart(9) + '  ' + name);
            }
            if (entries.length > 15) {
                console.log('       ' + ' '.repeat(6) + '... and ' + (entries.length - 15) + ' more');
            }
        } else {
            console.log('');
            console.log('unported paths reached: none');
        }

        // The diagnostic rules actually emitted. Run the same target under
        // PYRIGHT_GO_BRIDGE_MODE=oracle to get the baseline to read this
        // against: a rule the Go side emits far more often than the oracle is a
        // false positive worth chasing, and one it emits far less often is a
        // check that has not landed.
        const rules = Object.entries(globalThis.__pyrightGoRules ?? {}).sort((a, b) => b[1] - a[1]);
        if (rules.length > 0) {
            const totalRules = rules.reduce((sum, [, count]) => sum + count, 0);
            console.log('');
            console.log('diagnostic rules emitted: ' + rules.length + ' distinct, ' + totalRules + ' total');
            for (const [name, count] of rules.slice(0, 12)) {
                console.log('  ' + String(count).padStart(9) + '  ' + name);
            }
        }
    }
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

// importResolver.test.ts builds a file system and a config in TypeScript and
// asserts on what the resolver makes of them; the shim ships all three across.
if (testFile === 'importResolver.test.ts') {
    testOnlyAliases[path.join(refSrc, 'analyzer/importResolver')] = path.join(bridgeDir, 'shim-importResolver.ts');
}

// pathUtils is pure string functions, like symbolNameUtils below.
if (testFile === 'pathUtils.test.ts') {
    testOnlyAliases[path.join(refSrc, 'common/pathUtils')] = path.join(bridgeDir, 'shim-pathUtils.ts');
}

// The Stage D gate. typeEvaluator1-8.test.ts and checker.test.ts reach the
// analyzer only through tests/testUtils, so aliasing that one module points the
// whole suite at the Go port; see shim-evaluatorTestUtils.ts.
if (isEvaluatorGate) {
    testOnlyAliases[path.join(refSrc, 'tests/testUtils')] = path.join(
        bridgeDir,
        'shim-evaluatorTestUtils.ts'
    );
    // The shim re-exports the original's validateResults, so the real module
    // has to stay reachable rather than being replaced globally.
    delete aliases[path.join(refSrc, 'tests/testUtils')];
    // The gate replaces the analyzer, not the front end. Leaving the tokenizer
    // shimmed would put the Go tokenizer under the TypeScript analyzer in
    // oracle mode, which is neither implementation.
    delete aliases[path.join(refSrc, 'parser/tokenizer')];
    delete aliases[path.join(refSrc, 'parser/stringTokenUtils')];
    // Eight tests in typeEvaluator8.test.ts do not go through testUtils at all:
    // they stand up a fourslash TestState and call
    // `state.program.evaluator.getType(node)` on a live in-process Program.
    // Nothing about that survives a stateless per-call bridge, so they raise the
    // marker and are reported as skipped rather than counted either way.
    testOnlyAliases[path.join(refSrc, 'tests/harness/fourslash/testState')] = path.join(
        bridgeDir,
        'shim-testState.ts'
    );
}

// symbolNameUtils is pure string predicates, so the shim needs nothing else.
if (testFile === 'symbolNameUtils.test.ts') {
    testOnlyAliases[path.join(refSrc, 'analyzer/symbolNameUtils')] = path.join(
        bridgeDir,
        'shim-symbolNameUtils.ts'
    );
}

// jsonc-parser resolves a submodule with a dynamic require and smol-toml is
// loaded with a dynamic import, neither of which esbuild can follow. Both are
// rewritten to an absolute path and left external, so Node resolves them at
// runtime from wherever the bundle happens to live. Only the bundles that reach
// configOptions -- the evaluator gate, through the real testUtils -- need them.
const externalPackages = {
    'jsonc-parser': path.join(findNodeModules(esbuildPath), 'jsonc-parser', 'lib', 'umd', 'main.js'),
    'smol-toml': path.join(findNodeModules(esbuildPath), 'smol-toml', 'dist', 'index.cjs'),
};

const aliasPlugin = `
const path = require('path');
const aliases = ${JSON.stringify(aliases)};
const externalPackages = ${JSON.stringify(externalPackages)};
const testOnlyAliases = ${JSON.stringify(testOnlyAliases)};
const testFilePaths = ${JSON.stringify(testFilePaths)};
module.exports = {
    name: 'go-bridge-alias',
    setup(build) {
        build.onResolve({ filter: /.*/ }, (a) => {
            if (a.kind === 'entry-point' || !a.importer) return undefined;
            if (externalPackages[a.path]) {
                return { path: externalPackages[a.path], external: true };
            }
            if (a.path.startsWith('@pyright/')) {
                return { path: path.join(${JSON.stringify(refSrc)}, a.path.slice('@pyright/'.length)) + '.ts' };
            }
            if (!a.path.startsWith('.')) return undefined;
            const resolved = path.resolve(path.dirname(a.importer), a.path);
            if (aliases[resolved]) return { path: aliases[resolved] };
            if (testOnlyAliases[resolved] && testFilePaths.includes(a.importer)) {
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
    // testUtils.ts locates the sample corpus with
    // \`path.resolve(path.dirname(module.filename), './samples/' + name)\`, which
    // in a bundle points at the temp directory the bundle lives in. Telling the
    // bundle where it would have been keeps that function the original.
    // Only module.filename, not __dirname: testUtils.ts is the one module in
    // this bundle that reads it, whereas __dirname is read by harness modules
    // that sit at other depths and would be sent to the wrong place by a single
    // global value.
    banner: ${JSON.stringify(
        isEvaluatorGate
            ? { js: `module.filename = ${JSON.stringify(path.join(refSrc, 'tests', 'testUtils.js'))};\n` }
            : {}
    )},
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

const target = process.env.PYRIGHT_GO_BRIDGE_MODE === 'oracle' ? 'the TypeScript oracle' : 'the Go implementation';
console.log(`Running ${testFiles.join(', ')} against ${target}\n`);

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
