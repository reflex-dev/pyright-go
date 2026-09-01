/*
 * shim-uri.ts
 *
 * Drop-in replacement for pyright-internal/src/common/uri/uri.ts that forwards
 * to the Go port, so pyright's own uri.test.ts runs unmodified against it.
 *
 * A Uri is an object with ~40 methods that flows into other TypeScript code, so
 * unlike the earlier shims this cannot be a plain per-call dispatch. Two
 * properties of Uri make a simple answer possible:
 *
 *   - Uris are immutable, and
 *   - every method is a pure function of the Uri and its arguments.
 *
 * So a Uri here is a *recipe*: how it was constructed, plus the chain of
 * derivations applied since. Deriving one Uri from another (getDirectory,
 * combinePaths, stripExtension, ...) appends to the recipe and costs no round
 * trip at all. Only reading a scalar sends the recipe to Go, which replays it
 * and answers. This matters because client.ts spawns a fresh Go process per
 * request: nothing can be held across calls, so there is no handle table to
 * keep.
 *
 * Three places need care.
 *
 * 1. Case sensitivity. Uri.file and Uri.parse call
 *    `detector.isCaseSensitive(normalized.str)`, and the detector is a
 *    TypeScript object -- often a RealTempFile, which consults the actual disk.
 *    Construction is therefore two steps: Go normalizes and hands back the
 *    exact string the original would pass, this calls the detector with it, and
 *    the answer is baked into the recipe. That is not an approximation; it is
 *    the same call with the same argument. Go reports a null string for the
 *    cases where the original never consults the detector -- an empty parse,
 *    and a parse that lands on a non-file scheme -- so it is not called then
 *    either.
 *
 * 2. matchesRegex takes a JavaScript RegExp. Go answers which string it would
 *    test -- the normalized path for a FileUri, the raw path for a WebUri --
 *    and the test runs here. The implementation choice, which is the part that
 *    could be wrong, still comes from Go.
 *
 * 3. Uri.constant compares by *reference*, so two constants with the same name
 *    are unequal and a constant equals itself. Replaying a recipe twice would
 *    otherwise build two distinct ConstantUris and break both halves of that.
 *    Each constant gets a serial number here, and Go interns by serial for the
 *    duration of the one request in which both operands are replayed.
 */

import { call } from './client';

type RootSpec =
    | { kind: 'parse'; value: string; caseSensitive: boolean }
    | { kind: 'file'; value: string; checkRelative: boolean; cwd: string; caseSensitive: boolean }
    | { kind: 'constant'; name: string; serial: number }
    | { kind: 'empty' };

interface Op {
    name: string;
    args: any[];
}

interface Recipe {
    root: RootSpec;
    ops: Op[];
}

let constantSerial = 0;

function uriCall(recipe: Recipe, method: string, args: any[]): any {
    return call({ op: 'uri', payload: { recipe, call: { name: method, args } } });
}

// normalizeFor runs the first half of Uri.file / Uri.parse: everything up to
// the detector call. It returns the string the original would pass to the
// detector, or null when the original would not call it at all.
function normalizeFor(which: 'file' | 'parse', value: string, checkRelative: boolean, cwd: string): string | null {
    return call({ op: 'urinormalize', payload: { which, value, checkRelative, cwd } });
}

export interface CaseSensitivityDetectorLike {
    isCaseSensitive(uri: string): boolean;
}

/*
 * UriProxy implements the Uri interface. Methods that answer a Uri extend the
 * recipe; methods that answer anything else make one call.
 */
class UriProxy {
    constructor(private readonly _recipe: Recipe) {}

    get recipe(): Recipe {
        return this._recipe;
    }

    private _derive(name: string, ...args: any[]): UriProxy {
        return new UriProxy({ root: this._recipe.root, ops: [...this._recipe.ops, { name, args }] });
    }

    private _read(name: string, ...args: any[]): any {
        return uriCall(this._recipe, name, args);
    }

    // Uri.is() in the real uri.ts sniffs for a string _key, and real pyright
    // code may be handed one of these.
    get _key(): string {
        return this._read('key');
    }

    get key(): string {
        return this._read('key');
    }
    get scheme(): string {
        return this._read('scheme');
    }
    get fileName(): string {
        return this._read('fileName');
    }
    get fileNameWithoutExtensions(): string {
        return this._read('fileNameWithoutExtensions');
    }
    get lastExtension(): string {
        return this._read('lastExtension');
    }
    get isCaseSensitive(): boolean {
        return this._read('isCaseSensitive');
    }
    get fragment(): string {
        return this._read('fragment');
    }
    get query(): string {
        return this._read('query');
    }

    get root(): UriProxy {
        return this._derive('root');
    }
    get packageUri(): UriProxy {
        return this._derive('packageUri');
    }
    get packageStubUri(): UriProxy {
        return this._derive('packageStubUri');
    }
    get initPyUri(): UriProxy {
        return this._derive('initPyUri');
    }
    get initPyiUri(): UriProxy {
        return this._derive('initPyiUri');
    }
    get pytypedUri(): UriProxy {
        return this._derive('pytypedUri');
    }

    isEmpty(): boolean {
        return this._read('isEmpty');
    }
    toString(): string {
        return this._read('toString');
    }
    toUserVisibleString(): string {
        return this._read('toUserVisibleString');
    }
    isRoot(): boolean {
        return this._read('isRoot');
    }
    isLocal(): boolean {
        return this._read('isLocal');
    }
    isUntitled(): boolean {
        return this._read('isUntitled');
    }
    getRootPathLength(): number {
        return this._read('getRootPathLength');
    }
    getPathLength(): number {
        return this._read('getPathLength');
    }
    getPathComponents(): string[] {
        return this._read('getPathComponents');
    }
    getPath(): string {
        return this._read('getPath');
    }
    getFilePath(): string {
        return this._read('getFilePath');
    }
    getShortenedFileName(maxDirLength = 15): string {
        return this._read('getShortenedFileName', maxDirLength);
    }
    pathStartsWith(name: string): boolean {
        return this._read('pathStartsWith', name);
    }
    pathEndsWith(name: string): boolean {
        return this._read('pathEndsWith', name);
    }
    pathIncludes(include: string): boolean {
        return this._read('pathIncludes', include);
    }
    hasExtension(ext: string): boolean {
        return this._read('hasExtension', ext);
    }
    containsExtension(ext: string): boolean {
        return this._read('containsExtension', ext);
    }

    isChild(parent: any): boolean {
        return this._read('isChild', recipeOf(parent));
    }
    equals(other: any): boolean {
        return this._read('equals', recipeOf(other));
    }
    startsWith(other: any): boolean {
        return this._read('startsWith', recipeOf(other));
    }
    getRelativePathComponents(to: any): string[] {
        return this._read('getRelativePathComponents', recipeOf(to));
    }
    getRelativePath(child: any): string | undefined {
        const result = this._read('getRelativePath', recipeOf(child));
        return result === null ? undefined : result;
    }

    // The regex cannot cross the wire, so Go answers which string it tests and
    // the test itself runs here.
    matchesRegex(regex: RegExp): boolean {
        return regex.test(this._read('matchesRegexTarget'));
    }

    addPath(extra: string): UriProxy {
        return this._derive('addPath', extra);
    }
    getDirectory(): UriProxy {
        return this._derive('getDirectory');
    }
    resolvePaths(...paths: string[]): UriProxy {
        return this._derive('resolvePaths', paths);
    }
    combinePaths(...paths: string[]): UriProxy {
        return this._derive('combinePaths', paths);
    }
    combinePathsUnsafe(...paths: string[]): UriProxy {
        return this._derive('combinePathsUnsafe', paths);
    }
    stripExtension(): UriProxy {
        return this._derive('stripExtension');
    }
    stripAllExtensions(): UriProxy {
        return this._derive('stripAllExtensions');
    }
    replaceExtension(ext: string): UriProxy {
        return this._derive('replaceExtension', ext);
    }
    addExtension(ext: string): UriProxy {
        return this._derive('addExtension', ext);
    }
    withFragment(fragment: string): UriProxy {
        return this._derive('withFragment', fragment);
    }
    withQuery(query: string): UriProxy {
        return this._derive('withQuery', query);
    }

    toJsonObj(): any {
        throw new Error('PYRIGHT_GO_BRIDGE_UNSUPPORTED: Uri serialization is not bridged');
    }
}

export function recipeOf(value: any): Recipe | null {
    if (value instanceof UriProxy) {
        return value.recipe;
    }
    if (value === undefined || value === null) {
        return null;
    }
    throw new Error('expected a bridged Uri');
}

/*
 * The two hooks shim-uriUtils.ts uses. uriUtils' Uri-taking functions are
 * derivations and reads like any other, so they extend a recipe rather than
 * needing a protocol of their own.
 */

export function deriveUri(uri: Uri, name: string, ...args: any[]): Uri {
    const recipe = recipeOf(uri)!;
    return new UriProxy({ root: recipe.root, ops: [...recipe.ops, { name, args }] }) as Uri;
}

export function readUri(uri: Uri, name: string, ...args: any[]): any {
    return uriCall(recipeOf(uri)!, name, args);
}

export interface Uri extends UriProxy {}

export namespace Uri {
    export const DefaultWorkspaceRootComponent = '<default workspace root>';
    export const DefaultWorkspaceRootPath = `/${DefaultWorkspaceRootComponent}`;

    export function maybeUri(value: string): boolean {
        return call({ op: 'urinormalize', payload: { which: 'maybeUri', value } });
    }

    export function file(path: string, detector: CaseSensitivityDetectorLike, checkRelative = false): Uri {
        const cwd = process.cwd();
        const normalized = normalizeFor('file', path, checkRelative, cwd);
        const caseSensitive = normalized === null ? true : detector.isCaseSensitive(normalized);
        return new UriProxy({
            root: { kind: 'file', value: path, checkRelative, cwd, caseSensitive },
            ops: [],
        }) as Uri;
    }

    export function parse(uriStr: string | undefined, detector: CaseSensitivityDetectorLike): Uri {
        const value = uriStr ?? '';
        const normalized = normalizeFor('parse', value, false, '');
        const caseSensitive = normalized === null ? true : detector.isCaseSensitive(normalized);
        return new UriProxy({ root: { kind: 'parse', value, caseSensitive }, ops: [] }) as Uri;
    }

    export function create(value: string, detector: CaseSensitivityDetectorLike, checkRelative = false): Uri {
        return maybeUri(value) ? parse(value, detector) : file(value, detector, checkRelative);
    }

    export function constant(markerName: string): Uri {
        return new UriProxy({ root: { kind: 'constant', name: markerName, serial: constantSerial++ }, ops: [] }) as Uri;
    }

    export function empty(): Uri {
        return new UriProxy({ root: { kind: 'empty' }, ops: [] }) as Uri;
    }

    export function defaultWorkspace(detector: CaseSensitivityDetectorLike): Uri {
        return file(DefaultWorkspaceRootPath, detector);
    }

    export function is(thing: any): thing is Uri {
        return thing instanceof UriProxy;
    }

    export function isEmpty(uri: Uri | undefined): boolean {
        return !uri || uri.isEmpty();
    }

    export function equals(a: Uri | undefined, b: Uri | undefined): boolean {
        if (a === b) {
            return true;
        }
        return a?.equals(b) ?? false;
    }

    export function isDefaultWorkspace(uri: Uri): boolean {
        return uri.fileName.includes(DefaultWorkspaceRootComponent);
    }

    export function fromJsonObj(_jsonObj: any): Uri {
        throw new Error('PYRIGHT_GO_BRIDGE_UNSUPPORTED: Uri deserialization is not bridged');
    }
}

// uri.ts re-exports these for other modules; kept so the shim is a drop-in.
export const enum UriKinds {
    file,
    web,
    empty,
}

export type SerializedType = [UriKinds, ...any[]];
