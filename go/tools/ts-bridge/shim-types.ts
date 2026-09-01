/*
 * shim-types.ts
 *
 * Drop-in replacement for pyright-internal/src/analyzer/types.ts that builds
 * types in the Go port instead of in TypeScript. Aliasing this module lets
 * pyright's own typePrinter.test.ts run unmodified against the Go
 * implementation.
 *
 * The client in client.ts starts a fresh Go process per request, so there is no
 * session to hold objects in. Instead of shipping objects, this records the
 * calls the test makes into a log and replays the whole log on the Go side with
 * each printType. Replay is deterministic, so the two are equivalent.
 *
 * The namespace functions here therefore return opaque handles, not types.
 * A handle is a Proxy so that the property mutations the tests perform --
 * `classType.shared.typeParams.push(...)`,
 * `funcType.shared.declaredReturnType = ...` -- are recorded as well; nothing
 * reads a property back, so nothing has to be resolved eagerly.
 *
 * The enums are re-exported from the original module. They are `const enum`s,
 * so esbuild inlines their values and the real types.ts does not survive into
 * the bundle.
 */

export {
    ClassTypeFlags,
    FunctionParamFlags,
    FunctionTypeFlags,
    TypeCategory,
    TypeFlags,
    TypeVarKind,
} from '@pyright/analyzer/types';
export type { ParamSpecType, TypeVarTupleType } from '@pyright/analyzer/types';

interface Command {
    cmd: 'call' | 'set' | 'push';
    id?: number;
    target?: string;
    args?: any[];
    handle?: number;
    path?: string[];
    value?: any;
    values?: any[];
}

const log: Command[] = [];
let nextHandle = 0;

export function getLog(): Command[] {
    return log;
}

export function handleOf(value: any): number {
    const id = value?.__goHandle;
    if (typeof id !== 'number') {
        throw new Error('expected a Go type handle');
    }
    return id;
}

function encode(value: any): any {
    if (value === undefined || value === null) {
        return null;
    }
    if (Array.isArray(value)) {
        return value.map(encode);
    }
    if (typeof value === 'object') {
        if (typeof value.__goHandle === 'number') {
            return { $h: value.__goHandle };
        }
        // The only other objects the tests pass are Uris.
        if (typeof value.key === 'string') {
            return { $uri: value.key };
        }
        throw new Error(`cannot send ${JSON.stringify(value)} to the Go bridge`);
    }
    return value;
}

// A proxy over a property path within a handle. It records writes and pushes;
// reading a property just extends the path.
function pathProxy(handle: number, path: string[]): any {
    return new Proxy(
        {},
        {
            get(_target, prop) {
                if (typeof prop !== 'string') {
                    return undefined;
                }
                if (prop === 'push') {
                    return (...values: any[]) => {
                        log.push({ cmd: 'push', handle, path, values: values.map(encode) });
                        return values.length;
                    };
                }
                return pathProxy(handle, [...path, prop]);
            },
            set(_target, prop, value) {
                log.push({ cmd: 'set', handle, path: [...path, String(prop)], value: encode(value) });
                return true;
            },
        }
    );
}

function makeHandle(id: number): any {
    return new Proxy(
        { __goHandle: id },
        {
            get(target, prop) {
                if (prop === '__goHandle') {
                    return id;
                }
                if (typeof prop !== 'string') {
                    return (target as any)[prop];
                }
                return pathProxy(id, [prop]);
            },
        }
    );
}

function record(target: string, args: any[]): any {
    const id = nextHandle++;
    log.push({ cmd: 'call', id, target, args: args.map(encode) });
    return makeHandle(id);
}

// The namespaces the tests use. Arguments are passed through untouched, so the
// TypeScript defaults are applied on the Go side (see typebridge_dispatch.go).
export const AnyType = {
    create: (...args: any[]) => record('AnyType.create', args),
};

export const UnknownType = {
    create: (...args: any[]) => record('UnknownType.create', args),
};

export const UnboundType = {
    create: (...args: any[]) => record('UnboundType.create', args),
};

export const NeverType = {
    createNever: (...args: any[]) => record('NeverType.createNever', args),
    createNoReturn: (...args: any[]) => record('NeverType.createNoReturn', args),
};

export const ModuleType = {
    create: (...args: any[]) => record('ModuleType.create', args),
};

export const TypeVarType = {
    createInstance: (...args: any[]) => record('TypeVarType.createInstance', args),
    createInstantiable: (...args: any[]) => record('TypeVarType.createInstantiable', args),
    cloneForUnpacked: (...args: any[]) => record('TypeVarType.cloneForUnpacked', args),
};

export const ClassType = {
    createInstantiable: (...args: any[]) => record('ClassType.createInstantiable', args),
    cloneAsInstance: (...args: any[]) => record('ClassType.cloneAsInstance', args),
    specialize: (...args: any[]) => record('ClassType.specialize', args),
};

export const FunctionType = {
    createInstance: (...args: any[]) => record('FunctionType.createInstance', args),
    addParam: (...args: any[]) => record('FunctionType.addParam', args),
    addPositionOnlyParamSeparator: (...args: any[]) =>
        record('FunctionType.addPositionOnlyParamSeparator', args),
    addKeywordOnlyParamSeparator: (...args: any[]) =>
        record('FunctionType.addKeywordOnlyParamSeparator', args),
    addParamSpecVariadics: (...args: any[]) => record('FunctionType.addParamSpecVariadics', args),
};

export const FunctionParam = {
    create: (...args: any[]) => record('FunctionParam.create', args),
};

export function combineTypes(...args: any[]): any {
    return record('combineTypes', args);
}
