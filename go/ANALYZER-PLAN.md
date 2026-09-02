# Plan: porting the analyzer

The front end is done (see PORTING.md). What remains under the goal is the
analyzer: ~100k lines of TypeScript under `packages/pyright-internal/src/analyzer`
plus the parts of `common/` it drags in.

This document is the plan, not a status report. Nothing below is implemented.

## Two structural facts that shape everything

### 1. The analyzer is a single 98-file import cycle

Running Tarjan over the import graph of `pyright-internal/src` (excluding
`tests/`) produces one strongly-connected component of **98 files and 109,393
lines**. It contains the entire analyzer, `parser/parser.ts`,
`common/configOptions.ts`, `common/diagnostic.ts`, the `common/uri` tree, and the
service-provider plumbing. TypeScript does not care; Go forbids import cycles
between packages.

Sampling the cross-directory back-edges shows almost all of them are type-only:

| edge | what crosses |
| --- | --- |
| `common/diagnostic.ts` → `common/configOptions.ts` | the `DiagnosticLevel` type |
| `common/extensibility.ts` → `parser/parser.ts` | `ParseNode`, `ParseFileResults`, `ParserOutput` types |
| `analyzer/analyzerFileInfo.ts` → `analyzer/sourceFile.ts` | types |
| `analyzer/types.ts` ↔ `analyzer/symbol.ts` | genuine mutual data recursion |

The one real runtime back-edge out of `common/` is
`common/configOptions.ts` → `analyzer/pythonPathUtils`, `analyzer/importLogger`.

**Decision: `go/analyzer` is one Go package.** Files inside a Go package see
each other freely, which is exactly the property the TypeScript relies on. Do
not try to sub-package it by concern — every attempt will hit a cycle and the
port will turn into a refactor of pyright's architecture, which is the opposite
of the goal.

Two consequences:

- `configOptions` moves into `go/analyzer`. It is analyzer configuration; only
  the `DiagnosticLevel` type needs to live down in `common` so `common/diagnostic`
  can keep using it.
- The DI plumbing (`extensibility.ts`, `serviceKeys.ts`, `serviceProvider*.ts`,
  `host.ts`) mostly evaporates. In TypeScript it exists to break cycles and to
  swap implementations at runtime; in Go it becomes small interfaces declared at
  the point of consumption. This is a divergence and must be documented as one.

### 2. The evaluator cannot be landed incrementally

`typeEvaluator.ts` is 30,246 lines, and its satellites — `typeGuards`,
`patternMatching`, `codeFlowEngine`, `constraintSolver`, `dataClasses`,
`typedDicts`, `protocols`, `operations`, `constructors`, `enums`, `properties`,
`tuples`, `decorators`, `namedTuples`, `functionTransform`,
`constructorTransform` — are mutually recursive with it through a closure
exposing the 88-method `TypeEvaluator` interface in `typeEvaluatorTypes.ts`.

This is the same all-or-nothing property `parser.ts` had, six times larger.
There is no subset that compiles and does something useful. The mitigation is
not decomposition, it is **making progress measurable** — see Stage D.

## Stepping stones

Four stages. A and B are hard prerequisites and each has a rigorous oracle. C is
plumbing. D is the wall.

### Stage A — type model and parse-tree utilities (~14k lines TS) — **DONE**

`types.ts`, `typeUtils.ts`, `symbol.ts`, `symbolUtils.ts`, `symbolNameUtils.ts`,
`declaration.ts`, `declarationUtils.ts`, `scope.ts`, `scopeUtils.ts`,
`typePrinter.ts`, `parseTreeUtils.ts`, `parseTreeWalker.ts`, `typeWalker.ts`,
`analyzerNodeInfo.ts`, `analyzerFileInfo.ts`, `codeFlowTypes.ts`,
`typeCacheUtils.ts`, `typeComplexity.ts`.

Pure data structures and pure functions. Compiles and tests standalone.

The risk here is **representation choice, not logic**. `Type` is a discriminated
union of nine categories over a shared `TypeBase`, with a `shared` / `priv`
field split, `TypeFlags` bit sets, heavy structural sharing between instances,
and a lazily computed type id used for cache keys. Getting this wrong is
ruinous to undo once 30k lines of evaluator sit on top of it. Model `Type` as a
Go interface with a category discriminant and keep the `shared`/`priv` split
verbatim; resist "simplifying" it.

Verified by `typePrinter.test.ts` run unmodified through the bridge (6/6) plus a
`parseTreeUtils` corpus differential over all 1,343 files (0 different).
`parseTreeUtils.test.ts` turned out not to be bridgeable — it drives the
fourslash harness, which needs the binder and the import resolver — so the
differential stands in for it. See `analyzer/STATUS.md`.

`typePrinter.test.ts` was the important one, as expected: it constructs types
directly and prints them, so it exercises the representation without needing the
evaluator at all. The bridge for it replays the test's construction calls against
the Go type model, so it covers `types.ts` too.

### Stage B — the binder (~12k lines TS including its deps) — **DONE**

`binder.ts` (4,913) plus `importStatementUtils.ts`, `staticExpressions.ts`,
`cellChainIndex.ts`, `importResult.ts`, `commentUtils.ts`.

This is the highest-value stepping stone. The binder is a pure function of
(parse tree, file info) producing scopes, symbols, declarations and the
code-flow graph. It does not import `types.ts` and never touches the evaluator.
Its 13 same-directory dependencies are all in Stage A.

**It is differentiable exactly like the AST was.** Extend the bridge: dump the
scope tree, every symbol's declarations and flags, and the code-flow graph
(node ids, flow flags, antecedents) from the TypeScript binder and from Go, then
diff over all 1,296 sample files plus the 753 bundled stdlib stubs. Build the
harness *before* writing the binder so it is written against a live oracle.

This matters because a single wrong code-flow edge produces no visible symptom
until some narrowing test 40k lines later fails for reasons nobody can trace.
Catching it here is worth a great deal.

That is how it went. The harness was built and validated against the unmodified
TypeScript binder before the Go binder existed, and it is green over 1,343
sample files and 752 typeshed stdlib stubs in both import modes -- 4,190
file-runs, 0 different. See `analyzer/STATUS-STAGE-B.md`, which also lists what
the differential cannot reach until the import resolver arrives in Stage C.

### Stage C — import resolver, program, filesystem (~9k lines TS) — **DONE**

`importResolver.ts`, `importResolverFileSystem.ts`, `pythonPathUtils.ts`,
`typeshedInfoProvider.ts`, `sourceFile.ts`, `sourceFileInfo.ts`, `program.ts`,
`service.ts`, `parentDirectoryCache.ts`, `circularDependency.ts`, plus
`common/uri/*` and a filesystem abstraction.

Required before *any* real type test can run, because every sample file imports
from typeshed (5,410 bundled files, 753 of them stdlib).

`importResolver.test.ts` (1,281 lines) runs against an in-memory fake
filesystem, which makes it directly bridgeable — ship the fake FS contents in
the request and let the Go resolver read from it.

This is the one stage where deliberate divergence is right. Port `Uri`
*semantics* faithfully (case sensitivity, the `.key` normalization, root
handling) because import resolution depends on them, but not `RealFileSystem`,
the chokidar watchers, the background-thread machinery, or the service provider.
Those are Node-isms with no bearing on results.

That is how it went for the resolver half. `importResolver.ts` and everything
under it are ported and `importResolver.test.ts` runs unmodified: 34/34. `Uri`
came with more than expected -- the parts of `vscode-uri` pyright's Uri classes
reach had to be ported too, because the exact percent-encoding it produces ends
up in a Uri's key. `uri.test.ts` (95/95) and `pathUtils.test.ts` (63/63) are
gates in their own right. See `analyzer/STATUS-STAGE-C.md`.

The program half went the same way. `sourceFile.ts`, `sourceFileInfo.ts`,
`program.ts` and `service.ts` all reach the evaluator and the checker, so they
landed with the check phase stubbed -- the "skeleton first, then fatten" step
below. The seams are narrow: the evaluator is an opaque value nothing in Stage C
calls a method on, and the checker comes from a factory that may be nil. With
nil factories the program parses, binds, resolves imports, walks the import
graph, detects cycles and reports parse and bind diagnostics.

`config.test.ts` is not bridgeable -- it mutates `ConfigOptions` in place and
asserts on object identity -- so a differential stands in for it, over every
project directory in the corpus in both command-line and language-server mode:
78/78 identical. What it cannot reach is `program.analyze()` itself, which stays
ungated until Stage D.

### Stage D — evaluator and checker (~55k lines TS)

No way to split it. But there are three oracles that turn a binary
"does it work yet" into a number that climbs from day one:

1. **`expected_text`, and it is free.** The sample corpus contains **3,330**
   `reveal_type(x, expected_text="...")` assertions across 503 files. Each is a
   per-expression type assertion checked by pyright itself in `internalTestMode`.
   No TypeScript side needed. The scoreboard is "N / 3330 correct" from the
   first day the evaluator returns anything.
2. **Per-node type differential.** Reuse the AST bridge shape: walk every
   expression node, ask both implementations for `printType(getType(node))`, and
   diff. Pyright already does this walk in test mode (`NameTypeWalker` in
   `testWalker.ts`). This localizes failures to a single expression instead of
   "this file reported 3 errors instead of 2".
3. **The real scoreboard.** 1,279 test cases across `typeEvaluator1-8.test.ts`
   and `checker.test.ts`, each asserting error/warning counts over a sample file,
   run unmodified through the bridge — plus the 2,576 `# This should generate an
   error` markers those counts come from.

Order within D that keeps things runnable: the `typeEvaluatorTypes` interface
first, then the evaluator core (name / member-access / call / binary-operation /
index resolution), then `codeFlowEngine`, `typeGuards`, `constraintSolver`, then
the class-shape satellites (`dataClasses`, `typedDicts`, `enums`, `namedTuples`,
`protocols`, `properties`, `constructors`, `decorators`), then `checker.ts` last.
Each satellite can be stubbed to return `Unknown` and lit up one at a time; the
`expected_text` count measures each one's arrival.

## Sequencing: skeleton first, then fatten

Stages A and B are prerequisites regardless of order. After that, prefer going
**vertical before horizontal**: stand up Stage C's program loop with a
deliberately incomplete evaluator that only handles literals and simple
assignments, on a tiny corpus with no typeshed. That gets pyright's real test
harness running end to end early, so from that point every commit moves a
number instead of moving toward a number.

The alternative — finishing the evaluator before anything can execute it — means
tens of thousands of lines written with no feedback at all. The front-end port
worked precisely because the differential existed the whole way through.

## Out of scope

Not needed for the goal, and worth naming so they do not get picked up by
accident: `languageService/` (11.2k lines), `typeServer/` (6.5k),
`commands/` (0.4k), `backgroundAnalysis*`, the LSP server, `packageTypeVerifier`,
`typeStubWriter`, `sourceMapper`, `docStringConversion`. Skipping these removes
roughly 25k lines and most of the remaining Node-specific plumbing.

## Sizing, honestly

The front end was 12,205 lines of TypeScript (parser directory, excluding the
generated unicode table) against 18,745 lines of hand-written Go including
tests — about 1.5×. The analyzer is 100,857 lines of TypeScript, and
`typeEvaluator.ts` alone is 2.5× the entire front end.

Stage A is roughly the size of the tokenizer port. Stage B is roughly the size
of the parser port and has a comparable oracle. Stage C is smaller but fiddlier.
Stage D is larger than everything already done, several times over.
