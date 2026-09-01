/*
 * dump-binder.ts
 *
 * Runs pyright's own (unmodified) analyzer/binder.ts over a file and emits
 * everything it produces -- the scope tree, every symbol's flags and
 * declarations, the code-flow graph, the __all__ info and the bind-time
 * diagnostics -- in the same JSON shape cmd/tokenserver/binder.go produces from
 * the Go port, so the two can be diffed over a corpus.
 *
 * ANALYZER-PLAN.md calls for this to exist before the binder is written, for a
 * specific reason: a single wrong code-flow edge produces no visible symptom
 * until some narrowing test tens of thousands of lines later fails for reasons
 * nobody can trace back.
 *
 * Three things are renumbered, because all three are per-process counters that
 * would never line up between two implementations (or even between two files in
 * the same process):
 *
 *   - parse nodes, by pre-order index, as in dump-parsetreeutils.ts;
 *   - flow nodes, by first-sight order in a fixed traversal;
 *   - symbols, likewise.
 *
 * The renumbering is deliberately traversal-order dependent rather than
 * id-order dependent: it means a graph that differs in *shape* produces a
 * difference, while one that merely allocated ids in a different interleaving
 * does not. The binder allocates in a deterministic order anyway, so in
 * practice a difference in numbering is a difference in structure.
 *
 * The import resolver is Stage C, so nothing here can resolve a real import.
 * Instead the harness installs a synthetic ImportResult on every ModuleName
 * node, identical on both sides, in one of two modes (see makeImportResult):
 * every import resolves, or none does. Running the corpus in both modes covers
 * both families of branches in visitModuleName / visitImportAs /
 * visitImportFromAs. What it cannot cover until Stage C is anything that
 * depends on the *content* of a resolved import: implicit submodule imports,
 * py.typed detection, the missing-stub diagnostic's namespace-package
 * suppression, and wildcard imports (which call importLookup).
 */

import { ExecutionEnvironment, getStandardDiagnosticRuleSet } from '@pyright/common/configOptions';
import { TextRangeDiagnosticSink } from '@pyright/common/diagnosticSink';
import { latestStablePythonVersion } from '@pyright/common/pythonVersion';
import { Uri } from '@pyright/common/uri/uri';
import { AnalyzerFileInfo, ImportLookupResult } from '@pyright/analyzer/analyzerFileInfo';
import * as AnalyzerNodeInfo from '@pyright/analyzer/analyzerNodeInfo';
import { Binder } from '@pyright/analyzer/binder';
import { FlowFlags, FlowNode } from '@pyright/analyzer/codeFlowTypes';
import { Declaration, DeclarationType } from '@pyright/analyzer/declaration';
import { ImportResult, ImportType } from '@pyright/analyzer/importResult';
import { getChildNodes } from '@pyright/analyzer/parseTreeWalker';
import { Scope } from '@pyright/analyzer/scope';
import { Symbol as PyrightSymbol } from '@pyright/analyzer/symbol';
import { ModuleNode, ParseNode, ParseNodeType } from '@pyright/parser/parseNodes';
import { ParseOptions, Parser } from '@pyright/parser/parser';

export interface DumpOptions {
    // When false, every synthetic import reports isImportFound: false.
    importsResolve: boolean;
}

function encodeString(value: string): number[] {
    const out: number[] = [];
    for (let i = 0; i < value.length; i++) {
        out.push(value.charCodeAt(i));
    }
    return out;
}

// A single fixed URI stands in for every path. The binder only ever copies it
// into declarations, so its content is irrelevant as long as both sides agree;
// what matters is that a declaration carries the file URI rather than, say, an
// import's resolved URI.
const fileUri = Uri.empty();

function makeImportResult(nameParts: string[], options: DumpOptions): ImportResult {
    const importName = nameParts.join('.');
    return {
        importName,
        isRelative: false,
        isNativeLib: false,
        isImportFound: options.importsResolve,
        isPartlyResolved: false,
        isNamespacePackage: false,
        isInitFilePresent: options.importsResolve,
        isStubPackage: false,
        importFailureInfo: [],
        importType: ImportType.Local,
        resolvedUris: nameParts.map(() => fileUri),
        searchPath: fileUri,
        isStubFile: false,
        isThirdPartyPyTypedPresent: false,
        isPyTypedPresent: false,
        implicitImports: new Map(),
        filteredImplicitImports: new Map(),
        nonStubImportResult: undefined,
    } as unknown as ImportResult;
}

// Walk the tree once, before binding, and give every ModuleName node a
// synthetic import result. The real pipeline does this from program.ts after
// import resolution; the assert at the top of visitModuleName means the binder
// cannot run without it.
function installImportResults(node: ParseNode, options: DumpOptions): void {
    if (node.nodeType === ParseNodeType.ModuleName) {
        AnalyzerNodeInfo.setImportInfo(
            node,
            makeImportResult(
                node.d.nameParts.map((part) => part.d.value),
                options
            )
        );
    }

    for (const child of getChildNodes(node)) {
        if (child) {
            installImportResults(child, options);
        }
    }
}

// describeUri collapses a Uri to one of three values, because the harness uses
// a single constant URI for everything and only the distinction matters.
function describeUri(uri: Uri | undefined): string | null {
    if (uri === undefined) {
        return null;
    }
    return uri.equals(fileUri) ? 'file' : 'other';
}

// SymbolFlags is a const enum and Symbol._flags is private with no accessor, so
// rebuild the bit set from the predicates. Every flag has one.
function symbolFlags(symbol: PyrightSymbol): number {
    let flags = 0;
    const bits: [() => boolean, number][] = [
        [() => symbol.isInitiallyUnbound(), 1 << 0],
        [() => symbol.isExternallyHidden(), 1 << 1],
        [() => symbol.isClassMember(), 1 << 2],
        [() => symbol.isInstanceMember(), 1 << 3],
        [() => symbol.isSlotsMember(), 1 << 4],
        [() => symbol.isPrivateMember(), 1 << 5],
        [() => symbol.isIgnoredForProtocolMatch(), 1 << 6],
        [() => symbol.isClassVar(), 1 << 7],
        [() => symbol.isInDunderAll(), 1 << 8],
        [() => symbol.isPrivatePyTypedImport(), 1 << 9],
        [() => symbol.isInitVar(), 1 << 10],
        [() => symbol.isNamedTupleMemberMember(), 1 << 11],
        [() => symbol.isIgnoredForOverrideChecks(), 1 << 12],
        [() => symbol.isFinalVarInClassBody(), 1 << 13],
        [() => symbol.isDataClassKeywordOnly(), 1 << 14],
    ];
    for (const [predicate, bit] of bits) {
        if (predicate()) {
            flags |= bit;
        }
    }
    return flags;
}

export function dump(text: string, options: DumpOptions): any {
    const parser = new Parser();
    const parseOptions = new ParseOptions();
    const diagSink = new TextRangeDiagnosticSink([]);
    const parseResults = parser.parseSourceFile(text, parseOptions, diagSink);
    const module = parseResults.parserOutput.parseTree;

    // Pre-order parse node index, exactly as in dump-parsetreeutils.ts.
    const order: ParseNode[] = [];
    const nodeIndex = new Map<ParseNode, number>();
    const collect = (node: ParseNode) => {
        nodeIndex.set(node, order.length);
        order.push(node);
        for (const child of getChildNodes(node)) {
            if (child) {
                collect(child);
            }
        }
    };
    collect(module);

    const nodeIdx = (node: ParseNode | undefined): number => {
        if (!node) {
            return -1;
        }
        const found = nodeIndex.get(node);
        // -2 means "the binder produced a node that is not in the parse tree",
        // which would be a real finding rather than a harness bug.
        return found === undefined ? -2 : found;
    };

    installImportResults(module, options);

    const lines = parseResults.tokenizerOutput.lines;
    const bindDiagnostics = new TextRangeDiagnosticSink(lines);

    const fileInfo: AnalyzerFileInfo = {
        importLookup: (_target: any): ImportLookupResult | undefined => undefined,
        futureImports: new Set<string>(),
        builtinsScope: undefined,
        diagnosticSink: bindDiagnostics,
        executionEnvironment: new ExecutionEnvironment(
            'python',
            fileUri,
            getStandardDiagnosticRuleSet(),
            latestStablePythonVersion,
            /* defaultPythonPlatform */ undefined,
            /* defaultExtraPaths */ undefined
        ),
        diagnosticRuleSet: getStandardDiagnosticRuleSet(),
        lines,
        typingSymbolAliases: parseResults.parserOutput.typingSymbolAliases,
        definedConstants: new Map<string, boolean | string>(),
        fileId: 'file',
        fileUri,
        moduleName: 'mod',
        isStubFile: false,
        isTypingStubFile: false,
        isTypingExtensionsStubFile: false,
        isTypeshedStubFile: false,
        isBuiltInStubFile: false,
        isInPyTypedPackage: false,
        ipythonMode: 0,
        accessedSymbolSet: new Set<number>(),
    } as unknown as AnalyzerFileInfo;

    AnalyzerNodeInfo.setFileInfo(module, fileInfo);

    const binder = new Binder(fileInfo, /* moduleSymbolOnly */ false, /* cellChainIndex */ undefined);
    binder.bindModule(module);

    // ---------------------------------------------------------------- registries

    const flowIndex = new Map<FlowNode, number>();
    const flowOrder: FlowNode[] = [];
    const flowIdx = (flow: FlowNode | undefined): number => {
        if (!flow) {
            return -1;
        }
        const existing = flowIndex.get(flow);
        if (existing !== undefined) {
            return existing;
        }
        const index = flowOrder.length;
        flowIndex.set(flow, index);
        flowOrder.push(flow);
        return index;
    };

    const symbolIndex = new Map<PyrightSymbol, number>();
    const symbolIdxById = new Map<number, number>();
    const symbolIdx = (symbol: PyrightSymbol): number => {
        const existing = symbolIndex.get(symbol);
        if (existing !== undefined) {
            return existing;
        }
        const index = symbolIndex.size;
        symbolIndex.set(symbol, index);
        symbolIdxById.set(symbol.id, index);
        return index;
    };

    const scopeIndex = new Map<Scope, number>();
    const scopeOrder: Scope[] = [];
    const scopeIdx = (scope: Scope | undefined): number => {
        if (!scope) {
            return -1;
        }
        const existing = scopeIndex.get(scope);
        if (existing !== undefined) {
            return existing;
        }
        const index = scopeOrder.length;
        scopeIndex.set(scope, index);
        scopeOrder.push(scope);
        return index;
    };

    // ------------------------------------------------------------------ per node

    // Registering in pre-order fixes the numbering of scopes and flow nodes.
    // Everything discovered later (a scope's parent, a flow node's antecedent)
    // is appended, and both registries are drained below, so nothing reachable
    // is missed.
    const nodes = order.map((node) => {
        const entry: any = {
            s: scopeIdx(AnalyzerNodeInfo.getScope(node)),
            f: flowIdx(AnalyzerNodeInfo.getFlowNode(node)),
            af: flowIdx(AnalyzerNodeInfo.getAfterFlowNode(node)),
            u: AnalyzerNodeInfo.isCodeUnreachable(node),
        };

        const decl = AnalyzerNodeInfo.getDeclaration(node);
        if (decl) {
            entry.d = dumpDeclaration(decl);
        }

        // getCodeFlowExpressions / getCodeFlowComplexity are only set on
        // execution scopes; the accessors are typed to accept only those.
        if (
            node.nodeType === ParseNodeType.Module ||
            node.nodeType === ParseNodeType.Function ||
            node.nodeType === ParseNodeType.Lambda ||
            node.nodeType === ParseNodeType.Comprehension ||
            node.nodeType === ParseNodeType.TypeParameterList
        ) {
            const expressions = AnalyzerNodeInfo.getCodeFlowExpressions(node as any);
            if (expressions) {
                // A Set's iteration order is insertion order in both languages,
                // but the insertion order here is not load bearing anywhere the
                // evaluator reads it, so sort to avoid a spurious difference.
                entry.cfe = [...expressions].sort();
            }
            const complexity = AnalyzerNodeInfo.getCodeFlowComplexity(node as any);
            if (complexity) {
                entry.cfc = complexity;
            }
        }

        if (node.nodeType === ParseNodeType.If) {
            const staticValue = AnalyzerNodeInfo.getStaticConditionValue(node);
            if (staticValue !== undefined) {
                entry.scv = staticValue;
            }
        }

        return entry;
    });

    // ---------------------------------------------------------------- declarations

    function dumpDeclaration(decl: Declaration): any {
        const entry: any = {
            t: DeclarationType[decl.type],
            n: nodeIdx(decl.node as ParseNode),
            r: [decl.range.start.line, decl.range.start.character, decl.range.end.line, decl.range.end.character],
            m: decl.moduleName,
            // The URI is a single constant here, so all this can report is
            // whether the binder used the file's own URI, something else, or
            // nothing at all. It is genuinely undefined on some alias
            // declarations built from an import whose resolvedUris array is
            // shorter than the dotted name -- which the Go side must reproduce
            // as a nil Uri rather than an empty one.
            uri: describeUri(decl.uri),
            ex: decl.isInExceptSuite,
            itd: (decl as any).isInInlinedTypedDict ?? false,
        };

        switch (decl.type) {
            case DeclarationType.Intrinsic:
                entry.name = decl.name;
                entry.it = decl.intrinsicType;
                break;

            case DeclarationType.Function:
                entry.isMethod = decl.isMethod;
                entry.isGenerator = decl.isGenerator;
                entry.returns = (decl.returnStatements ?? []).map(nodeIdx);
                entry.yields = (decl.yieldStatements ?? []).map(nodeIdx);
                entry.raises = (decl.raiseStatements ?? []).map(nodeIdx);
                break;

            case DeclarationType.Param:
                entry.inferredName = decl.inferredName;
                entry.inferredTypeNodes = (decl.inferredTypeNodes ?? []).map(nodeIdx);
                break;

            case DeclarationType.TypeAlias:
                entry.docString = decl.docString === undefined ? undefined : encodeString(decl.docString);
                break;

            case DeclarationType.Variable:
                entry.typeAnnotationNode = nodeIdx(decl.typeAnnotationNode);
                entry.inferredTypeSource = nodeIdx(decl.inferredTypeSource as ParseNode | undefined);
                entry.isConstant = decl.isConstant ?? false;
                entry.isFinal = decl.isFinal ?? false;
                entry.isDefinedBySlots = decl.isDefinedBySlots ?? false;
                entry.isInferenceAllowedInPyTyped = decl.isInferenceAllowedInPyTyped ?? false;
                entry.isRuntimeTypeExpression = decl.isRuntimeTypeExpression ?? false;
                entry.typeAliasName = nodeIdx(decl.typeAliasName);
                entry.isDefinedByMemberAccess = decl.isDefinedByMemberAccess ?? false;
                entry.docString = decl.docString === undefined ? undefined : encodeString(decl.docString);
                entry.alternativeTypeNode = nodeIdx(decl.alternativeTypeNode);
                entry.isExplicitBinding = (decl as any).isExplicitBinding ?? false;
                break;

            case DeclarationType.Alias:
                entry.usesLocalName = decl.usesLocalName;
                entry.loadSymbolsFromPath = decl.loadSymbolsFromPath;
                entry.symbolName = decl.symbolName;
                entry.submoduleFallback = decl.submoduleFallback
                    ? dumpDeclaration(decl.submoduleFallback)
                    : undefined;
                entry.firstNamePart = decl.firstNamePart;
                entry.implicitImports = dumpLoaderActions(decl.implicitImports);
                entry.isUnresolved = decl.isUnresolved ?? false;
                entry.isNativeLib = decl.isNativeLib ?? false;
                entry.isLazy = (decl as any).isLazy ?? false;
                break;

            default:
                break;
        }

        return entry;
    }

    function dumpLoaderActions(actions: Map<string, any> | undefined): any {
        if (!actions) {
            return undefined;
        }
        const out: any[] = [];
        actions.forEach((action, name) => {
            out.push({
                name,
                uri: describeUri(action.uri),
                isUnresolved: action.isUnresolved ?? false,
                loadSymbolsFromPath: action.loadSymbolsFromPath,
                implicitImports: dumpLoaderActions(action.implicitImports),
            });
        });
        return out;
    }

    // ---------------------------------------------------------------- the drains

    // Scopes reach other scopes through parent and proxy; symbols reach
    // declarations. Draining rather than recursing keeps the numbering in a
    // single deterministic order.
    const scopes: any[] = [];
    for (let i = 0; i < scopeOrder.length; i++) {
        const scope = scopeOrder[i];
        const symbols: any[] = [];
        scope.symbolTable.forEach((symbol, name) => {
            symbols.push({
                name,
                id: symbolIdx(symbol),
                // Symbol._flags is private and there is no accessor, so read it
                // through the predicates. Anything the binder can set has one.
                flags: symbolFlags(symbol),
                decls: symbol.getDeclarations().map(dumpDeclaration),
            });
        });

        const notLocalBindings: any[] = [];
        scope.notLocalBindings.forEach((bindingType, name) => {
            notLocalBindings.push([name, bindingType]);
        });

        scopes.push({
            type: scope.type,
            parent: scopeIdx(scope.parent),
            proxy: scopeIdx(scope.proxy),
            hasChainedLookup: scope.chainedModuleLevelScopeLookup !== undefined,
            symbols,
            notLocalBindings,
            slotsNames: scope.slotsNames,
            hasNonEmptySlots: scope.hasNonEmptySlots,
            hasPotentiallyDynamicSymbolTable: scope.hasPotentiallyDynamicSymbolTable,
        });
    }

    // Dumping a flow node registers its antecedents, which appends to
    // flowOrder, so this walks a list that grows underneath it until it
    // settles. That is the drain: everything reachable from a flow node the
    // parse tree points at ends up numbered and dumped, in one deterministic
    // order.
    const flows: any[] = [];
    for (let i = 0; i < flowOrder.length; i++) {
        const flow: any = flowOrder[i];
        const entry: any = { flags: flow.flags };

        if (flow.flags & (FlowFlags.BranchLabel | FlowFlags.LoopLabel | FlowFlags.PostContextManager)) {
            entry.antecedents = (flow.antecedents as FlowNode[]).map(flowIdx);
            entry.affected = flow.affectedExpressions ? [...flow.affectedExpressions].sort() : undefined;
        }
        if (flow.flags & FlowFlags.BranchLabel) {
            entry.preBranch = flowIdx(flow.preBranchAntecedent);
        }
        if (flow.flags & FlowFlags.PostContextManager) {
            entry.expressions = (flow.expressions as ParseNode[]).map(nodeIdx);
            entry.isAsync = flow.isAsync;
            entry.blockIfSwallows = flow.blockIfSwallowsExceptions;
        }
        if (flow.antecedent !== undefined) {
            entry.antecedent = flowIdx(flow.antecedent);
        }
        if (flow.node !== undefined) {
            entry.node = nodeIdx(flow.node);
        }
        if (flow.flags & FlowFlags.Assignment) {
            // The target symbol id is a per-process counter, so report the
            // renumbered index. indeterminateSymbolId (0) has no symbol; -2
            // means the id belongs to no symbol in any scope, which would be a
            // real finding.
            entry.target = flow.targetSymbolId === 0 ? -1 : symbolIdxById.get(flow.targetSymbolId) ?? -2;
        }
        if (flow.names !== undefined) {
            entry.names = flow.names;
        }
        if (flow.expression !== undefined) {
            entry.expression = nodeIdx(flow.expression);
        }
        if (flow.reference !== undefined) {
            entry.reference = nodeIdx(flow.reference);
        }
        if (flow.subjectExpression !== undefined) {
            entry.subject = nodeIdx(flow.subjectExpression);
        }
        if (flow.statement !== undefined) {
            entry.statement = nodeIdx(flow.statement);
        }
        if (flow.finallyNode !== undefined) {
            entry.finallyNode = nodeIdx(flow.finallyNode);
        }
        if (flow.preFinallyGate !== undefined) {
            entry.preFinallyGate = flowIdx(flow.preFinallyGate);
        }

        flows.push(entry);
    }

    // ------------------------------------------------------------------ the rest

    const dunderAll = AnalyzerNodeInfo.getDunderAllInfo(module);

    const diagnostics = bindDiagnostics.fetchAndClear().map((diag) => ({
        category: diag.category,
        message: encodeString(diag.message),
        range: [
            diag.range.start.line,
            diag.range.start.character,
            diag.range.end.line,
            diag.range.end.character,
        ],
        rule: diag.getRule(),
        actions: (diag.getActions() ?? []).map((action: any) => action.action),
    }));

    return {
        nodes,
        scopes,
        flows,
        dunderAll: dunderAll
            ? {
                  names: dunderAll.names,
                  stringNodes: dunderAll.stringNodes.map(nodeIdx),
                  usesUnsupportedDunderAllForm: dunderAll.usesUnsupportedDunderAllForm,
              }
            : undefined,
        diagnostics,
    };
}
