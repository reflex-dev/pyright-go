/*
 * dump-config.ts
 *
 * The TypeScript oracle for the config differential: builds an AnalyzerService
 * over a project directory exactly as config.test.ts does, and dumps everything
 * the resulting ConfigOptions and file enumeration contain.
 *
 * config.test.ts itself is not bridgeable. It constructs ExecutionEnvironments
 * in TypeScript, mutates ConfigOptions and CommandLineOptions in place, and
 * asserts on object *identity* --
 *
 *     assert.strictEqual(configOptions.findExecEnvironment(file1), execEnv1);
 *
 * -- none of which survives a stateless per-call bridge the way an immutable
 * Uri does. So a differential stands in for it, the same way one stands in for
 * parseTreeUtils.test.ts and binder.ts. It is run over the same fixtures the
 * test uses, which makes it broader rather than narrower: every project
 * directory under tests/samples is exercised, in both language-server and
 * command-line mode, rather than the twenty or so the test names.
 *
 * What it covers: config file discovery (pyrightconfig.json, pyproject.toml,
 * and the walk up the directory tree), the "extends" chain, initializeFromJson,
 * setupExecutionEnvironments, the command-line/config merge, ensureDefaultOptions,
 * getFileSpec, and the source enumerator.
 */

import { AnalyzerService } from '@pyright/analyzer/service';
import { CommandLineOptions } from '@pyright/common/commandLineOptions';
import { ConfigOptions } from '@pyright/common/configOptions';
import { NullConsole } from '@pyright/common/console';
import { PythonVersion } from '@pyright/common/pythonVersion';
import { ensureTomlModuleLoaded } from '@pyright/common/tomlUtils';
import { RealTempFile, createFromRealFileSystem } from '@pyright/common/realFileSystem';
import { createServiceProvider } from '@pyright/common/serviceProviderExtensions';
import { Uri } from '@pyright/common/uri/uri';
import { TestAccessHost } from '@pyright/tests/harness/testAccessHost';

function uriToJson(uri: Uri | undefined): string | null {
    if (!uri) {
        return null;
    }
    return uri.isEmpty() ? '' : uri.getFilePath();
}

function fileSpecsToJson(specs: { wildcardRoot: Uri; regExp: RegExp; hasDirectoryWildcard: boolean }[]) {
    return specs.map((spec) => ({
        wildcardRoot: uriToJson(spec.wildcardRoot),
        // RegExp.prototype.source escapes every forward slash so the result can
        // be pasted back into a regular expression literal, which Go's
        // Regexp.String does not do. That is a rendering difference in the
        // accessor, not in the pattern -- `new RegExp('a/b').source` is
        // 'a\\/b' and matches exactly what 'a/b' matches -- so it is undone
        // here rather than imitated on the Go side.
        source: spec.regExp.source.replace(/\\\//g, '/'),
        flags: spec.regExp.flags,
        hasDirectoryWildcard: spec.hasDirectoryWildcard,
    }));
}

function execEnvToJson(env: any) {
    return {
        name: env.name,
        root: uriToJson(env.root),
        pythonVersion: env.pythonVersion ? PythonVersion.toString(env.pythonVersion) : null,
        pythonPlatform: env.pythonPlatform ?? '',
        extraPaths: env.extraPaths.map(uriToJson),
        skipNativeLibraries: !!env.skipNativeLibraries,
        diagnosticRuleSet: env.diagnosticRuleSet,
    };
}

function configToJson(configOptions: ConfigOptions) {
    return {
        projectRoot: uriToJson(configOptions.projectRoot),
        pythonPath: uriToJson(configOptions.pythonPath),
        pythonEnvironmentName: configOptions.pythonEnvironmentName ?? '',
        typeshedPath: uriToJson(configOptions.typeshedPath),
        stubPath: uriToJson(configOptions.stubPath),
        venvPath: uriToJson(configOptions.venvPath),
        venv: configOptions.venv ?? '',
        include: fileSpecsToJson(configOptions.include),
        exclude: fileSpecsToJson(configOptions.exclude),
        ignore: fileSpecsToJson(configOptions.ignore),
        strict: fileSpecsToJson(configOptions.strict),
        autoExcludeVenv: configOptions.autoExcludeVenv ?? null,
        defineConstant: Array.from(configOptions.defineConstant.entries()),
        verboseOutput: configOptions.verboseOutput ?? null,
        checkOnlyOpenFiles: configOptions.checkOnlyOpenFiles ?? null,
        useLibraryCodeForTypes: configOptions.useLibraryCodeForTypes ?? null,
        autoImportCompletions: configOptions.autoImportCompletions,
        indexing: configOptions.indexing,
        logTypeEvaluationTime: configOptions.logTypeEvaluationTime,
        typeEvaluationTimeThreshold: configOptions.typeEvaluationTimeThreshold,
        initializedFromJson: configOptions.initializedFromJson,
        disableTaggedHints: configOptions.disableTaggedHints,
        diagnosticRuleSet: configOptions.diagnosticRuleSet,
        executionEnvironments: configOptions.executionEnvironments.map(execEnvToJson),
        defaultPythonVersion: configOptions.defaultPythonVersion
            ? PythonVersion.toString(configOptions.defaultPythonVersion)
            : null,
        defaultPythonPlatform: configOptions.defaultPythonPlatform ?? '',
        defaultExtraPaths: (configOptions.defaultExtraPaths ?? []).map(uriToJson),
        skipNativeLibraries: !!configOptions.skipNativeLibraries,
        functionSignatureDisplay: configOptions.functionSignatureDisplay,
        configFileSource: uriToJson(configOptions.configFileSource),
        effectiveTypeCheckingMode: configOptions.effectiveTypeCheckingMode,
    };
}

// tomlUtils loads smol-toml with a dynamic import and exposes a promise for it.
// Without awaiting that, _parsePyprojectTomlFile throws "TOML module not
// loaded" on every attempt and the service silently behaves as though no
// pyproject.toml existed -- so the oracle has to await it before dumping
// anything.
export async function prepare() {
    await ensureTomlModuleLoaded();
}

export function dumpConfig(projectRoot: string, fromLanguageServer: boolean) {
    const tempFile = new RealTempFile();
    const console = new NullConsole();
    const fs = createFromRealFileSystem(tempFile, console);
    const serviceProvider = createServiceProvider(fs, console, tempFile);
    const host = new TestAccessHost();

    const service = new AnalyzerService('<default>', serviceProvider, {
        console,
        hostFactory: () => host,
        // The service would otherwise start its analysis timer.
        shouldRunAnalysis: () => false,
    });

    const commandLineOptions = new CommandLineOptions(projectRoot, fromLanguageServer);
    service.setOptions(commandLineOptions);

    const configOptions = service.test_getConfigOptions(commandLineOptions);
    const files = service
        .test_getFileNamesFromFileSpecs()
        .map((uri) => uri.getFilePath())
        .sort();

    const result = { config: configToJson(configOptions), files };

    service.dispose();
    tempFile.dispose();

    return result;
}
