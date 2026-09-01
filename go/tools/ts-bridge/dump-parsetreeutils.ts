/*
 * dump-parsetreeutils.ts
 *
 * Runs pyright's own (unmodified) analyzer/parseTreeUtils.ts over a file and
 * emits the results in the same JSON shape cmd/tokenserver/parsetreeutils.go
 * produces from the Go port, so the two can be diffed over a corpus.
 *
 * parseTreeUtils.test.ts drives the fourslash harness, which needs the binder
 * and the import resolver, so it cannot be bridged. This differential stands in
 * for it: it covers every parseTreeUtils function that does not need a bound
 * scope or file info, over every node of every corpus file.
 *
 * Deliberately not covered, because they call getScope or getFileInfo and so
 * need the binder: getEvaluationScopeNode, getExecutionScopeNode,
 * getEnclosingFunctionEvaluationScope, getEvaluationNodeForAssignmentExpression,
 * getScopeIdForNode, getTypeVarScopesForNode, getFileInfoFromNode.
 *
 * Nodes are identified by pre-order index, not by node id: ids come from a
 * per-process counter and would never line up between the two implementations.
 */

import { DiagnosticSink } from '@pyright/common/diagnosticSink';
import * as ParseTreeUtils from '@pyright/analyzer/parseTreeUtils';
import { getChildNodes } from '@pyright/analyzer/parseTreeWalker';
import { isExpressionNode, ParseNode, ParseNodeType } from '@pyright/parser/parseNodes';
import { ParseOptions, Parser } from '@pyright/parser/parser';

function encodeString(value: string): number[] {
    const out: number[] = [];
    for (let i = 0; i < value.length; i++) {
        out.push(value.charCodeAt(i));
    }
    return out;
}

export function dump(text: string): any {
    const parser = new Parser();
    const options = new ParseOptions();
    const parseResults = parser.parseSourceFile(text, options, new DiagnosticSink());
    const module = parseResults.parserOutput.parseTree;

    const order: ParseNode[] = [];
    const index = new Map<ParseNode, number>();
    const collect = (node: ParseNode) => {
        index.set(node, order.length);
        order.push(node);
        for (const child of getChildNodes(node)) {
            if (child) {
                collect(child);
            }
        }
    };
    collect(module);

    const idx = (node: ParseNode | undefined): number => {
        if (!node) {
            return -1;
        }
        const found = index.get(node);
        return found === undefined ? -2 : found;
    };

    const nodes = order.map((node) => {
        const entry: any = {
            t: ParseTreeUtils.printParseNodeType(node.nodeType),
            d: ParseTreeUtils.getNodeDepth(node),
            c: ParseTreeUtils.isCompliantWithNodeRangeRules(node),
            es: idx(ParseTreeUtils.getEnclosingSuite(node)),
            ec: idx(ParseTreeUtils.getEnclosingClass(node, /* stopAtFunction */ false)),
            ecf: idx(ParseTreeUtils.getEnclosingClass(node, /* stopAtFunction */ true)),
            // getEnclosingModule starts at the parent, so it fails on the root.
            em: node === module ? -1 : idx(ParseTreeUtils.getEnclosingModule(node)),
            ef: idx(ParseTreeUtils.getEnclosingFunction(node)),
            el: idx(ParseTreeUtils.getEnclosingLambda(node)),
            ecof: idx(ParseTreeUtils.getEnclosingClassOrFunction(node)),
            ecfs: idx(ParseTreeUtils.getEnclosingClassOrFunctionSuite(node)),
            esm: idx(ParseTreeUtils.getEnclosingSuiteOrModule(node, false, true)),
            ep: idx(ParseTreeUtils.getEnclosingParam(node)),
            tan: idx(ParseTreeUtils.getTypeAnnotationNode(node)),
            tvs: idx(ParseTreeUtils.getTypeVarScopeNode(node)),
            wdpi: ParseTreeUtils.isWithinDefaultParamInitializer(node),
            wta: ParseTreeUtils.isWithinTypeAnnotation(node, false),
            wtaq: ParseTreeUtils.isWithinTypeAnnotation(node, true),
            wac: ParseTreeUtils.isWithinAnnotationComment(node),
            wl: ParseTreeUtils.isWithinLoop(node),
            wae: ParseTreeUtils.isWithinAssertExpression(node),
            aw: ParseTreeUtils.containsAwaitNode(node),
        };

        if (isExpressionNode(node)) {
            entry.p = encodeString(ParseTreeUtils.printExpression(node));
            entry.pan = idx(ParseTreeUtils.getParentAnnotationNode(node));
            entry.sd = ParseTreeUtils.isSimpleDefault(node);
            entry.vds = idx(ParseTreeUtils.getVariableDocStringNode(node));
        }

        switch (node.nodeType) {
            case ParseNodeType.Name: {
                entry.wa = ParseTreeUtils.isWriteAccess(node);
                entry.imn = ParseTreeUtils.isImportModuleName(node);
                entry.ia = ParseTreeUtils.isImportAlias(node);
                entry.fimn = ParseTreeUtils.isFromImportModuleName(node);
                entry.fin = ParseTreeUtils.isFromImportName(node);
                entry.fia = ParseTreeUtils.isFromImportAlias(node);
                entry.lnm = ParseTreeUtils.isLastNameOfModuleName(node);
                entry.fnd = ParseTreeUtils.isFirstNameOfDottedName(node);
                entry.lnd = ParseTreeUtils.isLastNameOfDottedName(node);
                entry.cfn = idx(ParseTreeUtils.getCallForName(node));
                entry.dfn = idx(ParseTreeUtils.getDecoratorForName(node));
                entry.dnl = idx(ParseTreeUtils.getDottedNameWithGivenNodeAsLastName(node));
                const names = ParseTreeUtils.getDottedName(node);
                if (names) {
                    entry.dn = names.map(idx);
                }
                entry.fa = ParseTreeUtils.isFinalAllowedForAssignmentTarget(node);
                entry.ra = ParseTreeUtils.isRequiredAllowedForAssignmentTarget(node);
                break;
            }

            case ParseNodeType.MemberAccess: {
                entry.fa = ParseTreeUtils.isFinalAllowedForAssignmentTarget(node);
                const parent = node.parent;
                if (parent && isExpressionNode(parent)) {
                    entry.mp = ParseTreeUtils.isMatchingExpression(node, parent);
                    entry.pm = ParseTreeUtils.isPartialMatchingExpression(node, parent);
                }
                break;
            }

            case ParseNodeType.Suite:
                entry.se = ParseTreeUtils.isSuiteEmpty(node);
                break;

            case ParseNodeType.Function: {
                entry.fse = ParseTreeUtils.isFunctionSuiteEmpty(node);
                entry.ua = ParseTreeUtils.isUnannotatedFunction(node);
                const annotations: number[] = [];
                for (let i = 0; i <= node.d.params.length; i++) {
                    annotations.push(idx(ParseTreeUtils.getTypeAnnotationForParam(node, i)));
                }
                entry.pa = annotations;
                break;
            }

            case ParseNodeType.StatementList:
                entry.ids = ParseTreeUtils.isDocString(node);
                break;

            case ParseNodeType.Module: {
                const doc = ParseTreeUtils.getDocString(node.d.statements);
                if (doc !== undefined) {
                    entry.ds = encodeString(doc);
                }
                break;
            }

            case ParseNodeType.Decorator: {
                const name = ParseTreeUtils.getDecoratorName(node);
                if (name !== undefined) {
                    entry.dnm = name;
                }
                break;
            }

            case ParseNodeType.Class:
                entry.cfnm = ParseTreeUtils.getClassFullName(node, 'mod', node.d.name.d.value);
                break;

            case ParseNodeType.ImportFrom:
                entry.fio = ParseTreeUtils.isValidLocationForFutureImport(node);
                break;

            case ParseNodeType.String: {
                const range = ParseTreeUtils.getStringNodeValueRange(node);
                entry.svr = [range.start, range.length];
                break;
            }

            case ParseNodeType.Call:
                entry.aro = ParseTreeUtils.getArgsByRuntimeOrder(node).map(idx);
                entry.ntd = ParseTreeUtils.isAssignmentToDefaultsFollowingNamedTuple(node);
                break;

            case ParseNodeType.BinaryOperation:
                entry.ch = ParseTreeUtils.operatorSupportsChaining(node.d.operator);
                break;
        }

        return entry;
    });

    const offsets: number[] = [];
    for (let offset = 0; offset <= module.length; offset++) {
        offsets.push(idx(ParseTreeUtils.findNodeByOffset(module, offset)));
    }

    return { nodes, offsets };
}
