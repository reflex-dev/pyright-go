/*
 * shim-evaluatorTestUtils.ts
 *
 * The Stage D gate: stands in for tests/testUtils.ts so that the 1,279 test
 * cases in typeEvaluator1-8.test.ts and checker.test.ts run against the Go
 * port. The .test.ts files themselves are the originals, unmodified.
 *
 * This works because those tests reach the analyzer through exactly two
 * functions -- 1,385 calls to typeAnalyzeSampleFiles and 1,383 to
 * validateResults -- and assert on nothing but the six diagnostic lists the
 * first of them returns. Only typeAnalyzeSampleFiles is reimplemented here;
 * validateResults is re-exported from the original module, so the code that
 * decides pass or fail is pyright's own.
 *
 * Two modes:
 *
 *   PYRIGHT_GO_BRIDGE_MODE=oracle   round-trip the request and the response
 *                                   through the wire format, but answer from
 *                                   the TypeScript evaluator.
 *   PYRIGHT_GO_BRIDGE_MODE=go       answer from the Go port (the default).
 *
 * Oracle mode is what validates the harness. Every serialization step the Go
 * path depends on is exercised, and any loss in it shows up as a test failure
 * with no Go code in the picture at all -- which is the same discipline the
 * binder differential was built under, and for the same reason: a harness that
 * has never been seen to fail is not evidence of anything.
 */

import { ConfigOptions } from '@pyright/common/configOptions';
import { PythonVersion } from '@pyright/common/pythonVersion';
import { Uri } from '@pyright/common/uri/uri';
import { UriEx } from '@pyright/common/uri/uriUtils';
import * as RealTestUtils from '@pyright/tests/testUtils';

import { call } from './client';

/*
 * validateResults delegates to the original -- every assertion that decides
 * pass or fail is pyright's own code -- and does one extra thing: it records
 * whether the test expected any diagnostic at all.
 *
 * Without that, the headline number is worthless. An implementation that
 * reports nothing passes every test that asserts zero errors, and roughly half
 * of these do; the port scored 619 of 1,269 before a single line of the
 * evaluator existed. Splitting the passes into the ones that demanded a
 * diagnostic and the ones satisfied by silence is what makes the scoreboard
 * mean something.
 */
export function validateResults(
    results: RealTestUtils.FileAnalysisResult[],
    errorCount: number,
    warningCount = 0,
    infoCount?: number,
    unusedCode?: number,
    unreachableCode?: number,
    deprecated?: number
) {
    const expected = [errorCount, warningCount, infoCount, unusedCode, unreachableCode, deprecated];
    if (expected.some((count) => count !== undefined && count > 0)) {
        (globalThis as any).__pyrightGoSubstantive = true;
    }

    return RealTestUtils.validateResults(
        results,
        errorCount,
        warningCount,
        infoCount,
        unusedCode,
        unreachableCode,
        deprecated
    );
}

export const resolveSampleFilePath = RealTestUtils.resolveSampleFilePath;
export const readSampleFile = RealTestUtils.readSampleFile;
export const parseText = RealTestUtils.parseText;
export const parseSampleFile = RealTestUtils.parseSampleFile;

const mode = process.env.PYRIGHT_GO_BRIDGE_MODE || 'go';

/*
 * The config wire format.
 *
 * Only four things on ConfigOptions are reachable from these tests:
 * defaultPythonVersion, defaultPythonPlatform, defineConstant and the 96-field
 * diagnosticRuleSet. Everything else is whatever `new ConfigOptions(Uri.empty())`
 * produced.
 *
 * That claim is checked rather than assumed -- see assertConfigIsCarried.
 */
interface ConfigWire {
    defaultPythonVersion: string | null;
    defaultPythonPlatform: string | null;
    defineConstant: [string, boolean | string][];
    diagnosticRuleSet: Record<string, any>;
}

function configToWire(configOptions: ConfigOptions): ConfigWire {
    return {
        defaultPythonVersion: configOptions.defaultPythonVersion
            ? PythonVersion.toString(configOptions.defaultPythonVersion)
            : null,
        defaultPythonPlatform: configOptions.defaultPythonPlatform ?? null,
        defineConstant: Array.from(configOptions.defineConstant.entries()),
        diagnosticRuleSet: { ...configOptions.diagnosticRuleSet },
    };
}

function wireToConfig(wire: ConfigWire): ConfigOptions {
    const configOptions = new ConfigOptions(Uri.empty());

    if (wire.defaultPythonVersion === null) {
        configOptions.defaultPythonVersion = undefined;
    } else {
        const version = PythonVersion.fromString(wire.defaultPythonVersion);
        if (version === undefined) {
            throw new Error(`unrecognized pythonVersion ${wire.defaultPythonVersion}`);
        }
        configOptions.defaultPythonVersion = version;
    }

    configOptions.defaultPythonPlatform = wire.defaultPythonPlatform ?? undefined;

    configOptions.defineConstant = new Map();
    for (const [key, value] of wire.defineConstant) {
        configOptions.defineConstant.set(key, value);
    }

    configOptions.diagnosticRuleSet = { ...wire.diagnosticRuleSet } as any;

    // typeAnalyzeSampleFiles sets this on whatever it is handed.
    configOptions.internalTestMode = true;

    return configOptions;
}

/*
 * canonicalize renders an arbitrary value as a stable string, so two
 * ConfigOptions can be compared without listing their fields.
 *
 * Listing them is the thing to avoid: a list is exactly what goes stale when a
 * test sets a field the wire format does not carry, and a stale list fails
 * silently -- the Go side would analyze under a config that quietly differs
 * from the one the test built, and the resulting pass or fail would mean
 * nothing.
 */
function canonicalize(value: any, seen = new Set<any>()): any {
    if (value === undefined) {
        return { __undefined: true };
    }
    if (value === null || typeof value !== 'object') {
        return typeof value === 'function' ? { __function: value.name } : value;
    }
    if (seen.has(value)) {
        return { __circular: true };
    }
    seen.add(value);

    try {
        if (Array.isArray(value)) {
            return value.map((v) => canonicalize(v, seen));
        }
        if (value instanceof Map) {
            return { __map: Array.from(value.entries()).map(([k, v]) => [k, canonicalize(v, seen)]) };
        }
        if (value instanceof Set) {
            return { __set: Array.from(value.values()).map((v) => canonicalize(v, seen)) };
        }
        if (value instanceof RegExp) {
            return { __regexp: value.source, flags: value.flags };
        }
        // Uri is compared by its key, which is the normalized form the whole
        // analyzer keys off; two Uris with the same key are interchangeable.
        if (typeof value.key === 'string' && typeof value.getFilePath === 'function') {
            return { __uri: value.key };
        }

        // A property that was never assigned and one assigned `undefined` read
        // the same way, but Object.keys tells them apart -- and the two sides
        // differ that way for real: the class declares
        // `defaultPythonVersion?: PythonVersion` and never assigns it, while
        // the reconstruction assigns undefined explicitly. Dropping undefined
        // members makes the comparison ask what a reader would see.
        const out: Record<string, any> = {};
        for (const key of Object.keys(value).sort()) {
            if (value[key] === undefined) {
                continue;
            }
            out[key] = canonicalize(value[key], seen);
        }
        return out;
    } finally {
        seen.delete(value);
    }
}

/*
 * assertConfigIsCarried is the completeness guard on the config wire format.
 *
 * It reconstructs a ConfigOptions from the wire and compares it, field for
 * field, against the one the test actually built. A test that sets anything the
 * wire does not carry fails here, loudly, instead of being analyzed under the
 * wrong config.
 *
 * It runs on every call in both modes, which is 1,385 checks per run rather
 * than a one-time survey of what the tests are currently seen to touch.
 */
function assertConfigIsCarried(original: ConfigOptions, reconstructed: ConfigOptions) {
    const before = JSON.stringify(canonicalize(original));
    const after = JSON.stringify(canonicalize(reconstructed));
    if (before === after) {
        return;
    }

    // Report the first differing key rather than two 40KB blobs.
    const a = canonicalize(original) as Record<string, any>;
    const b = canonicalize(reconstructed) as Record<string, any>;
    const keys = Array.from(new Set([...Object.keys(a), ...Object.keys(b)])).sort();
    const differing = keys.filter((k) => JSON.stringify(a[k]) !== JSON.stringify(b[k]));

    throw new Error(
        `the config wire format does not carry ${differing.join(', ')}: ` +
            differing
                .map((k) => `${k} is ${JSON.stringify(a[k])} but came back ${JSON.stringify(b[k])}`)
                .join('; ')
    );
}

/*
 * The result wire format.
 */
interface DiagnosticWire {
    category: number;
    message: string;
    range: { start: { line: number; character: number }; end: { line: number; character: number } };
    rule: string | null;
}

interface FileResultWire {
    fileUri: string;
    hasParseResults: boolean;
    errors: DiagnosticWire[];
    warnings: DiagnosticWire[];
    infos: DiagnosticWire[];
    unusedCodes: DiagnosticWire[];
    unreachableCodes: DiagnosticWire[];
    deprecateds: DiagnosticWire[];
}

const diagnosticKinds = [
    'errors',
    'warnings',
    'infos',
    'unusedCodes',
    'unreachableCodes',
    'deprecateds',
] as const;

function diagnosticToWire(diag: any): DiagnosticWire {
    return {
        category: diag.category,
        message: diag.message,
        range: {
            start: { line: diag.range.start.line, character: diag.range.start.character },
            end: { line: diag.range.end.line, character: diag.range.end.character },
        },
        rule: diag.getRule() ?? null,
    };
}

function resultsToWire(results: RealTestUtils.FileAnalysisResult[]): FileResultWire[] {
    return results.map((result) => {
        const wire: any = {
            fileUri: result.fileUri.getFilePath(),
            hasParseResults: result.parseResults !== undefined,
        };
        for (const kind of diagnosticKinds) {
            wire[kind] = (result as any)[kind].map(diagnosticToWire);
        }
        return wire as FileResultWire;
    });
}

/*
 * The parse tree does not cross the bridge.
 *
 * One assertion in the whole suite checks that `parseResults` is defined, and
 * one reaches through it for the module scope. The first is answerable from a
 * flag; the second is not answerable at all, so touching the object raises the
 * marker the harness reports as a skip rather than a failure. Returning a
 * plausible-looking empty object instead would turn an untested thing into a
 * passing test, which is worse than an honest gap.
 */
function makeParseResultsStandIn(): any {
    return new Proxy(
        {},
        {
            get(_target, property) {
                throw new Error(
                    'PYRIGHT_GO_BRIDGE_UNSUPPORTED: the parse tree is not carried across the ' +
                        `bridge, so ${String(property)} cannot be read from parseResults`
                );
            },
        }
    );
}

function wireToResults(wire: FileResultWire[]): RealTestUtils.FileAnalysisResult[] {
    return wire.map((fileResult) => {
        const result: any = {
            fileUri: UriEx.file(fileResult.fileUri),
            parseResults: fileResult.hasParseResults ? makeParseResultsStandIn() : undefined,
        };
        for (const kind of diagnosticKinds) {
            result[kind] = fileResult[kind].map((diag) => ({
                category: diag.category,
                message: diag.message,
                range: diag.range,
                getRule: () => diag.rule ?? undefined,
            }));
        }
        return result as RealTestUtils.FileAnalysisResult;
    });
}

export function typeAnalyzeSampleFiles(
    fileNames: string[],
    configOptions = new ConfigOptions(Uri.empty()),
    console?: any
): RealTestUtils.FileAnalysisResult[] {
    // The original mutates the caller's object here, and the tests reuse one
    // ConfigOptions across several calls, so this has to happen before the
    // comparison below rather than on a copy.
    configOptions.internalTestMode = true;

    const wire = configToWire(configOptions);
    const reconstructed = wireToConfig(wire);
    assertConfigIsCarried(configOptions, reconstructed);

    if (mode === 'oracle') {
        // Answer from the TypeScript evaluator, but from the reconstructed
        // config and through the response wire format, so that everything the
        // Go path depends on is on the critical path here too.
        const results = RealTestUtils.typeAnalyzeSampleFiles(fileNames, reconstructed, console);
        const wire = resultsToWire(results);
        // Tally the oracle's rules the same way, so `bridge-evaluator-oracle`
        // produces the baseline the Go tally is read against.
        for (const fileResult of wire) {
            for (const kind of diagnosticKinds) {
                for (const diag of fileResult[kind] ?? []) {
                    const rule = diag.rule || '(no rule)';
                    const tally = ((globalThis as any).__pyrightGoRules ??= {});
                    tally[rule] = (tally[rule] ?? 0) + 1;
                }
            }
        }
        return wireToResults(wire);
    }

    const response = call({
        op: 'analyze',
        payload: {
            // The original locates typeshed-fallback through
            // `global.__rootDirectory`, which testUtils.ts sets to the working
            // directory. The Go binary's own location is unrelated to the
            // reference tree, so it is passed instead of inferred.
            rootDirectory: (globalThis as any).__rootDirectory,
            fileNames: fileNames.map((name) => resolveSampleFilePath(name)),
            config: wire,
        },
    });

    // Tally the diagnostic rules the Go side emits. A partially-ported
    // evaluator produces both missing and spurious diagnostics, and the gate's
    // pass/fail counts cannot distinguish them -- this can, by being compared
    // against the same tally from an oracle run.
    for (const fileResult of response.results ?? []) {
        for (const kind of diagnosticKinds) {
            for (const diag of fileResult[kind] ?? []) {
                // Key by rule plus the first few words of the message, so a
                // dominant rule can be attributed to a specific check without a
                // second run.
                const rule = diag.rule || '(no rule)';
                const gist = String(diag.message).split(/\s+/).slice(0, 6).join(' ');
                const key = rule + ' :: ' + gist;
                const tally = ((globalThis as any).__pyrightGoRules ??= {});
                tally[key] = (tally[key] ?? 0) + 1;
            }
        }
    }

    // Accumulate the Go evaluator's and checker's unported counts across every
    // analyze call in the run, so the gate can report the same frontier the
    // per-node differential does. Without this the two harnesses disagree about
    // what is missing, and only one of them can say why a test failed.
    if (response.unported) {
        const totals = ((globalThis as any).__pyrightGoUnported ??= {});
        for (const [name, count] of Object.entries(response.unported)) {
            totals[name] = (totals[name] ?? 0) + (count as number);
        }
    }

    if (mode === 'diff') {
        reportDiff(fileNames, reconstructed, console, response.results ?? []);
    }

    return wireToResults(response.results);
}

/*
 * Diff mode: answer from the Go port, but also run the TypeScript evaluator over
 * the same files and print what the two disagree about.
 *
 * The gate reports "Expected 3 errors, got 2", which says a diagnostic is
 * missing but not which one. That is the difference between a day of bisecting a
 * sample file by hand and a one-line answer, and it is the same reason the
 * binder differential exists: a count tells you something is wrong, a diff tells
 * you what.
 *
 * Diagnostics are keyed by category, position and message, so a diagnostic that
 * merely moved is reported as one removal and one addition rather than silently
 * matching. Order is not part of the key -- pyright does not promise one, and
 * validateResults does not check it.
 */
function reportDiff(
    fileNames: string[],
    configOptions: ConfigOptions,
    console: any,
    goWire: FileResultWire[]
) {
    const oracleWire = resultsToWire(RealTestUtils.typeAnalyzeSampleFiles(fileNames, configOptions, console));

    const key = (kind: string, diag: DiagnosticWire) =>
        `${kind} [${diag.range.start.line + 1}:${diag.range.start.character}] ${diag.message.replace(/\n/g, ' | ')}`;

    const lines: string[] = [];
    for (let i = 0; i < Math.max(oracleWire.length, goWire.length); i++) {
        const expected = new Map<string, number>();
        const received = new Map<string, number>();
        const bump = (map: Map<string, number>, k: string) => map.set(k, (map.get(k) ?? 0) + 1);

        for (const kind of diagnosticKinds) {
            for (const diag of oracleWire[i]?.[kind] ?? []) bump(expected, key(kind, diag));
            for (const diag of goWire[i]?.[kind] ?? []) bump(received, key(kind, diag));
        }

        const fileLines: string[] = [];
        for (const [k, count] of expected) {
            const missing = count - (received.get(k) ?? 0);
            for (let n = 0; n < missing; n++) fileLines.push('  MISSING  ' + k);
        }
        for (const [k, count] of received) {
            const spurious = count - (expected.get(k) ?? 0);
            for (let n = 0; n < spurious; n++) fileLines.push('  SPURIOUS ' + k);
        }

        if (fileLines.length > 0) {
            lines.push(' ' + (fileNames[i] ?? oracleWire[i]?.fileUri ?? goWire[i]?.fileUri));
            // Sorted so the two halves of a moved diagnostic land together.
            lines.push(...fileLines.sort((a, b) => a.slice(10).localeCompare(b.slice(10))));
        }
    }

    if (lines.length > 0) {
        console?.log?.('');
        process.stdout.write(
            'DIFF ' + ((globalThis as any).__pyrightGoCurrentTest ?? fileNames.join(',')) + '\n'
        );
        process.stdout.write(lines.join('\n') + '\n');
    }
}

export function getAnalysisResults(): never {
    throw new Error('PYRIGHT_GO_BRIDGE_UNSUPPORTED: getAnalysisResults needs a live Program');
}
