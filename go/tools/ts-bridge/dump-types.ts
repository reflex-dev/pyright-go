/*
 * dump-types.ts
 *
 * The TypeScript oracle for the per-node type differential: runs pyright's own
 * evaluator over a sample file and emits the printed type of every name in it,
 * in the same JSON shape cmd/tokenserver/nodetypes.go produces from the Go port.
 *
 * ANALYZER-PLAN.md calls for this, for the reason the binder differential
 * existed: an error-count test tells you a file reported 3 errors instead of 2,
 * which is nearly useless for finding out why in a 30,000-line evaluator. This
 * says which expression is wrong and what each side thought it was.
 *
 * The walk is pyright's own choice of what is safe to evaluate. testUtils.ts
 * installs a pre-check callback that runs NameTypeWalker over every file before
 * checking it, precisely to exercise contextual evaluation ordering:
 *
 *     if (node.parent?.nodeType !== ParseNodeType.ImportFromAs &&
 *         node.parent?.nodeType !== ParseNodeType.ImportAs) {
 *         if (this._evaluator.isNodeReachable(node, undefined)) {
 *             this._evaluator.getType(node);
 *         }
 *     }
 *
 * The same filter is applied here. Note that walking this way is not passive --
 * asking for every name's type changes the order in which things are evaluated,
 * and pyright does it deliberately for that reason. Both sides do the identical
 * walk, so the comparison stays symmetric.
 *
 * Nodes are identified by pre-order index rather than by node id, as in
 * dump-parsetreeutils.ts and dump-binder.ts: ids come from a per-process
 * counter that would never line up between two implementations.
 *
 * The tree those indices are taken over is the one that exists *after*
 * analysis, which is not the one the parser produced: typeEvaluator.ts:30085
 * parses string annotations on demand and grafts them onto the StringListNode.
 * That is why the walk happens after program.analyze() rather than before, and
 * why the node sets do not yet agree with the Go side. See compare-types.js.
 *
 * Three non-type outcomes are reported as their own markers rather than being
 * dropped, so that a side which evaluates *fewer* nodes shows up as a
 * difference instead of as a smaller list:
 *
 *   <unreachable>  isNodeReachable said no
 *   <none>         getType returned undefined
 *   <error>        the evaluator threw
 */

import { ConfigOptions } from '@pyright/common/configOptions';
import { NullConsole } from '@pyright/common/console';
import { FullAccessHost } from '@pyright/common/fullAccessHost';
import { RealTempFile, createFromRealFileSystem } from '@pyright/common/realFileSystem';
import { createServiceProvider } from '@pyright/common/serviceProviderExtensions';
import { Uri } from '@pyright/common/uri/uri';
import { UriEx } from '@pyright/common/uri/uriUtils';
import { ImportResolver } from '@pyright/analyzer/importResolver';
import { Program } from '@pyright/analyzer/program';
import { getChildNodes } from '@pyright/analyzer/parseTreeWalker';
import { NameNode, ParseNode, ParseNodeType } from '@pyright/parser/parseNodes';

export interface NodeTypeDump {
    // Pre-order index of the NameNode within its file's parse tree.
    index: number;
    name: string;
    type: string;
}

export function dumpTypes(filePath: string): NodeTypeDump[] {
    const configOptions = new ConfigOptions(Uri.empty());
    configOptions.internalTestMode = true;

    const tempFile = new RealTempFile();
    const fs = createFromRealFileSystem(tempFile);
    const serviceProvider = createServiceProvider(fs, new NullConsole(), tempFile);
    const importResolver = new ImportResolver(serviceProvider, configOptions, new FullAccessHost(serviceProvider));

    const program = new Program(importResolver, configOptions, serviceProvider);
    const fileUri = UriEx.file(filePath);
    program.setTrackedFiles([fileUri]);

    while (program.analyze()) {
        // No timeout, so this completes on the first call.
    }

    const results: NodeTypeDump[] = [];
    const sourceFile = program.getSourceFile(fileUri);
    const parseResults = sourceFile?.getParseResults();
    const evaluator = program.evaluator;

    if (parseResults && evaluator) {
        let index = 0;
        const walk = (node: ParseNode) => {
            const myIndex = index++;

            if (node.nodeType === ParseNodeType.Name && isEvaluatableName(node)) {
                results.push({
                    index: myIndex,
                    name: node.d.value,
                    type: typeOfName(evaluator, node),
                });
            }

            for (const child of getChildNodes(node)) {
                if (child) {
                    walk(child);
                }
            }
        };
        walk(parseResults.parserOutput.parseTree);
    }

    program.dispose();
    serviceProvider.dispose();

    return results;
}

// The filter NameTypeWalker applies: the names in `import x as y` and
// `from m import x as y` are declarations rather than references, and asking
// for their types is not what the evaluator is for.
function isEvaluatableName(node: NameNode): boolean {
    return (
        node.parent?.nodeType !== ParseNodeType.ImportFromAs &&
        node.parent?.nodeType !== ParseNodeType.ImportAs
    );
}

function typeOfName(evaluator: any, node: NameNode): string {
    try {
        if (!evaluator.isNodeReachable(node, /* sourceNode */ undefined)) {
            return '<unreachable>';
        }

        const type = evaluator.getType(node);
        if (type === undefined) {
            return '<none>';
        }

        return evaluator.printType(type, { expandTypeAlias: false });
    } catch (e) {
        // An evaluator that throws is a difference worth seeing, not a reason
        // to abandon the file.
        return `<error> ${String((e as any)?.message ?? e)}`;
    }
}
