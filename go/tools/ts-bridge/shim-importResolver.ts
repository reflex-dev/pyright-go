/*
 * shim-importResolver.ts
 *
 * Drop-in replacement for pyright-internal/src/analyzer/importResolver.ts that
 * forwards to the Go port, so pyright's own importResolver.test.ts runs
 * unmodified against it.
 *
 * The resolver reads three things and returns a fourth, so the bridge ships
 * exactly those:
 *
 *  1. A file system. The test builds a TestFileSystem, wraps it in a
 *     PyrightFileSystem, and hands that over. What the resolver can observe
 *     through it is which paths exist, what kind each is, where links point and
 *     what files contain -- so the whole tree is walked once per call and
 *     shipped. See go/vfs for why that is a snapshot rather than a
 *     transliteration of the 2,053-line harness file system.
 *
 *     It is re-walked on every call rather than cached, because one test
 *     mutates the file system between constructing the resolver and resolving
 *     ("import side by side file", which symlinks a directory in first).
 *
 *  2. A ConfigOptions and an ExecutionEnvironment, both plain data. The
 *     execution environment is chosen TypeScript-side by
 *     configOptions.findExecEnvironment and passed in, so what crosses is the
 *     one the test picked.
 *
 *  3. A Host. Every host in this test is a TestAccessHost, whose
 *     getPythonSearchPaths ignores all three of its arguments and answers a
 *     fixed list (filtered against the file system, if it was given one). So it
 *     is called once here and the answer shipped. That is exact for this host;
 *     a host that read its arguments would need a round trip, and the Go side
 *     would have to ask rather than be told.
 *
 *  4. An ImportResult, or a module name, or a list of Uris. Uris come back as
 *     file paths and are rebuilt here with the real Uri class, so what the test
 *     asserts on is a genuine Uri.
 */

import { call } from './client';
import { ImportType } from '@pyright/analyzer/importResult';
import { ConfigOptions, ExecutionEnvironment } from '@pyright/common/configOptions';
import { FileSystem } from '@pyright/common/fileSystem';
import { Host } from '@pyright/common/host';
import { PythonVersion } from '@pyright/common/pythonVersion';
import { Uri } from '@pyright/common/uri/uri';
import { UriEx } from '@pyright/common/uri/uriUtils';

// The subset of ServiceProvider the resolver uses. The test always passes a
// real one.
interface ServiceProviderLike {
    fs(): FileSystem;
    partialStubs?(): any;
}

/*
 * Serialization.
 */

interface UriJson {
    empty: boolean;
    filePath: string;
    caseSensitive: boolean;
}

function uriToJson(uri: Uri | undefined): UriJson | null {
    if (!uri) {
        return null;
    }
    if (uri.isEmpty()) {
        return { empty: true, filePath: '', caseSensitive: true };
    }
    if (uri.scheme !== 'file') {
        throw new Error(`PYRIGHT_GO_BRIDGE_UNSUPPORTED: only file URIs are bridged, got ${uri.toString()}`);
    }
    return { empty: false, filePath: uri.getFilePath(), caseSensitive: uri.isCaseSensitive };
}

function uriFromJson(json: UriJson | null): Uri | undefined {
    if (!json) {
        return undefined;
    }
    if (json.empty) {
        return Uri.empty();
    }
    return UriEx.file(json.filePath, json.caseSensitive);
}

function versionToJson(version: PythonVersion | undefined): string | null {
    return version ? PythonVersion.toString(version) : null;
}

function fileSpecsToJson(specs: { wildcardRoot: Uri; regExp: RegExp; hasDirectoryWildcard: boolean }[]) {
    return specs.map((spec) => ({
        wildcardRoot: uriToJson(spec.wildcardRoot),
        source: spec.regExp.source,
        ignoreCase: spec.regExp.flags.includes('i'),
        hasDirectoryWildcard: spec.hasDirectoryWildcard,
    }));
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
        defaultPythonVersion: versionToJson(configOptions.defaultPythonVersion),
        defaultPythonPlatform: configOptions.defaultPythonPlatform ?? '',
        defaultExtraPaths: (configOptions.defaultExtraPaths ?? []).map(uriToJson),
        skipNativeLibraries: !!configOptions.skipNativeLibraries,
        verboseOutput: !!configOptions.verboseOutput,
        include: fileSpecsToJson(configOptions.include),
        exclude: fileSpecsToJson(configOptions.exclude),
    };
}

function execEnvToJson(execEnv: ExecutionEnvironment) {
    return {
        root: uriToJson(execEnv.root),
        name: execEnv.name,
        pythonVersion: versionToJson(execEnv.pythonVersion),
        pythonPlatform: execEnv.pythonPlatform ?? '',
        extraPaths: execEnv.extraPaths.map(uriToJson),
        skipNativeLibraries: !!execEnv.skipNativeLibraries,
    };
}

function descriptorToJson(moduleDescriptor: {
    leadingDots: number;
    nameParts: string[];
    hasTrailingDot?: boolean;
    importedSymbols: Set<string> | undefined;
}) {
    return {
        leadingDots: moduleDescriptor.leadingDots,
        nameParts: moduleDescriptor.nameParts,
        hasTrailingDot: !!moduleDescriptor.hasTrailingDot,
        // `Set<string> | undefined`, and the difference is load-bearing in
        // filterImplicitImports, so null and [] are kept apart.
        importedSymbols: moduleDescriptor.importedSymbols ? [...moduleDescriptor.importedSymbols] : null,
    };
}

/*
 * The file system snapshot.
 */

function snapshotFileSystem(fs: FileSystem) {
    const entries: { path: string; kind: string; content: string; target: string }[] = [];
    const seenDirectories = new Set<string>();

    const walk = (dir: Uri) => {
        const key = dir.key;
        if (seenDirectories.has(key)) {
            // A symlinked directory can point back up; stop rather than loop.
            return;
        }
        seenDirectories.add(key);

        let dirEntries;
        try {
            dirEntries = fs.readdirEntriesSync(dir);
        } catch {
            return;
        }

        for (const entry of dirEntries) {
            if (entry.name === '.' || entry.name === '..') {
                continue;
            }

            const child = dir.combinePaths(entry.name);
            const path = child.getFilePath();

            if (entry.isSymbolicLink()) {
                let target = '';
                try {
                    target = fs.realpathSync(child).getFilePath();
                } catch {
                    // A broken link: record it with no target, which the Go
                    // side then fails to resolve, the same as here.
                }
                entries.push({ path, kind: 'symlink', content: '', target });
                continue;
            }

            if (entry.isDirectory()) {
                entries.push({ path, kind: 'dir', content: '', target: '' });
                walk(child);
                continue;
            }

            if (entry.isFile()) {
                let content = '';
                try {
                    content = fs.readFileSync(child, 'utf8');
                } catch {
                    // Unreadable: an empty file is what the resolver would see
                    // for anything it tries to read.
                }
                entries.push({ path, kind: 'file', content, target: '' });
            }
        }
    };

    walk(UriEx.file('/'));

    return {
        ignoreCase: false,
        cwd: '/',
        modulePath: fs.getModulePath()?.isEmpty() === false ? fs.getModulePath().getFilePath() : '',
        entries,
    };
}

/*
 * Result deserialization.
 */

function implicitImportsFromJson(json: any): Map<string, any> | undefined {
    if (!json) {
        return undefined;
    }
    const map = new Map<string, any>();
    for (const entry of json) {
        map.set(entry.name, {
            isStubFile: entry.isStubFile,
            isNativeLib: entry.isNativeLib,
            name: entry.name,
            uri: uriFromJson(entry.uri),
            pyTypedInfo: entry.pyTypedInfo
                ? { pyTypedPath: uriFromJson(entry.pyTypedInfo.pyTypedPath), isPartiallyTyped: entry.pyTypedInfo.isPartiallyTyped }
                : undefined,
        });
    }
    return map;
}

function importResultFromJson(json: any): any {
    if (!json) {
        return undefined;
    }
    return {
        importName: json.importName,
        isRelative: json.isRelative,
        isImportFound: json.isImportFound,
        isPartlyResolved: json.isPartlyResolved,
        isNamespacePackage: json.isNamespacePackage,
        isInitFilePresent: json.isInitFilePresent,
        isStubPackage: json.isStubPackage,
        importFailureInfo: json.importFailureInfo ?? undefined,
        importType: json.importType as ImportType,
        resolvedUris: json.resolvedUris.map((u: UriJson | null) => uriFromJson(u)),
        searchPath: uriFromJson(json.searchPath),
        isStubFile: json.isStubFile,
        isNativeLib: json.isNativeLib,
        isStdlibTypeshedFile: json.isStdlibTypeshedFile,
        isThirdPartyTypeshedFile: json.isThirdPartyTypeshedFile,
        isLocalTypingsFile: json.isLocalTypingsFile,
        implicitImports: implicitImportsFromJson(json.implicitImports),
        filteredImplicitImports: implicitImportsFromJson(json.filteredImplicitImports),
        nonStubImportResult: importResultFromJson(json.nonStubImportResult),
        pyTypedInfo: json.pyTypedInfo
            ? { pyTypedPath: uriFromJson(json.pyTypedInfo.pyTypedPath), isPartiallyTyped: json.pyTypedInfo.isPartiallyTyped }
            : undefined,
        packageDirectory: uriFromJson(json.packageDirectory),
    };
}

export class ImportResolver {
    constructor(readonly serviceProvider: ServiceProviderLike, private _configOptions: ConfigOptions, readonly host: Host) {}

    private _call(which: string, execEnv: ExecutionEnvironment, extra: any): any {
        // The search paths are read from the host here rather than in Go; see
        // the header.
        const searchPaths = this.host.getPythonSearchPaths();

        const response = call({
            op: 'importresolver',
            payload: {
                which,
                fs: snapshotFileSystem(this.serviceProvider.fs()),
                config: configToJson(this._configOptions),
                execEnv: execEnvToJson(execEnv),
                searchPaths: {
                    paths: searchPaths.paths.map(uriToJson),
                    prefix: uriToJson(searchPaths.prefix),
                },
                ...extra,
            },
        });

        this._applyPartialStubMappings(response.partialStubMappings);
        return response.value;
    }

    // ensurePartialStubPackages merges partial stub packages onto the libraries
    // they augment by mapping directories in the file system, and one test
    // reads that back through the file system it supplied: after resolving
    // "myLib.partialStub" it expects reading myLib/partialStub.pyi to answer
    // the contents of myLib-stubs/partialStub.pyi.
    //
    // The Go resolver works on a snapshot, so that side effect has to be
    // carried back. Which stub directory gets merged onto which package is
    // decided in Go -- that decision *is* processPartialStubPackages -- and
    // replayed here. The filter is the one from partialStubService.ts, repeated
    // because it is a closure and cannot cross the wire; it selects which paths
    // under the mapped directory are really mapped, not which directories are.
    private _applyPartialStubMappings(mappings: { mappedUri: UriJson; originalUri: UriJson }[]) {
        const fs = this.serviceProvider.fs();
        for (const mapping of mappings) {
            const mappedUri = uriFromJson(mapping.mappedUri)!;
            const originalUri = uriFromJson(mapping.originalUri)!;

            if (this._appliedMappings.has(`${mappedUri.key}|${originalUri.key}`)) {
                continue;
            }
            this._appliedMappings.add(`${mappedUri.key}|${originalUri.key}`);

            fs.mapDirectory(
                mappedUri,
                originalUri,
                (u, innerFs) => u.hasExtension('.pyi') || (innerFs.existsSync(u) && innerFs.statSync(u).isDirectory())
            );
        }
    }

    private _appliedMappings = new Set<string>();

    resolveImport(sourceFileUri: Uri, execEnv: ExecutionEnvironment, moduleDescriptor: any): any {
        return importResultFromJson(
            this._call('resolveImport', execEnv, {
                sourceFileUri: uriToJson(sourceFileUri),
                moduleDescriptor: descriptorToJson(moduleDescriptor),
            })
        );
    }

    getModuleNameForImport(fileUri: Uri, execEnv: ExecutionEnvironment, allowInvalidModuleName = false, detectPyTyped = false) {
        const result = this._call('getModuleNameForImport', execEnv, {
            sourceFileUri: uriToJson(fileUri),
            allowInvalidModuleName: !!allowInvalidModuleName,
            detectPyTyped: !!detectPyTyped,
        });

        return {
            moduleName: result.moduleName,
            importType: result.importType as ImportType,
            isTypeshedFile: result.isTypeshedFile,
            isLocalTypingsFile: result.isLocalTypingsFile,
            isThirdPartyPyTypedPresent: result.isThirdPartyPyTypedPresent,
        };
    }

    getSourceFilesFromStub(stubFileUri: Uri, execEnv: ExecutionEnvironment, mapCompiled: boolean): Uri[] {
        const result: (UriJson | null)[] = this._call('getSourceFilesFromStub', execEnv, {
            sourceFileUri: uriToJson(stubFileUri),
            mapCompiled,
        });
        return result.map((u) => uriFromJson(u)!);
    }

    getImportRoots(execEnv: ExecutionEnvironment, forLogging = false): Uri[] {
        const result: (UriJson | null)[] = this._call('getImportRoots', execEnv, { forLogging });
        return result.map((u) => uriFromJson(u)!);
    }

    getConfigOptions() {
        return this._configOptions;
    }
}

// Re-exported because other modules import them from here; none is reached by
// importResolver.test.ts, so each forwards to Go only when called.
export function formatImportName(moduleDescriptor: any): string {
    return '.'.repeat(moduleDescriptor.leadingDots) + moduleDescriptor.nameParts.join('.');
}

export function createImportedModuleDescriptor(moduleName: string) {
    if (moduleName.length === 0) {
        return { leadingDots: 0, nameParts: [], importedSymbols: new Set<string>() };
    }

    let startIndex = 0;
    let leadingDots = 0;
    for (; startIndex < moduleName.length; startIndex++) {
        if (moduleName[startIndex] !== '.') {
            break;
        }
        leadingDots++;
    }

    return { leadingDots, nameParts: moduleName.slice(startIndex).split('.'), importedSymbols: new Set<string>() };
}
