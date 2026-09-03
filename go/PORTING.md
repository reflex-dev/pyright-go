# Porting status

Transliterated from pyright at tag `1.1.412` (`3c1c5b64e833d343cbbbe12b675ea597c6612d88`).

## Done

The **type checker is complete**: source text in, the same diagnostics pyright
reports out. Front end, binder, type evaluator, checker, import resolver, and
the configuration and service layers that decide what gets analyzed.

| module | TypeScript | status |
| --- | --- | --- |
| `common/*` | text ranges, positions, diagnostics, Uri, path utils, file system, config options | complete (see "Still not ported" for the exceptions) |
| `localization/localize.ts` | 847 message accessors, 15 locales | complete, generated from the TypeScript |
| `parser/unicode.ts` | 3241 identifier ranges | complete, generated from the TypeScript |
| `parser/characters.ts` | identifier character tables | complete |
| `parser/characterStream.ts` | | complete |
| `parser/tokenizerTypes.ts` | tokens, keywords, operators | complete |
| `parser/tokenizer.ts` | | complete |
| `parser/stringTokenUtils.ts` | escape handling | complete |
| `parser/parseNodes.ts` | all 78 node types and factories | complete |
| `parser/parseNodeUtils.ts` | | complete |
| `parser/parser.ts` | **all 131 methods** | complete |
| `analyzer/binder.ts` | scopes, declarations, the code flow graph | complete |
| `analyzer/types.ts` and satellites | the type model, typeUtils, typePrinter | complete |
| `analyzer/typeEvaluator.ts` | | complete |
| `analyzer/codeFlowEngine.ts`, `typeGuards.ts`, `constraintSolver.ts` | | complete |
| `analyzer/checker.ts` | | complete |
| `analyzer/importResolver.ts` | | complete |
| `analyzer/sourceFile.ts`, `program.ts`, `service.ts` | | complete for batch analysis |
| `common/host.ts`, `common/fullAccessHost.ts` | the interpreter queries | complete (`runScript`/`runSnippet`/`spawnProcess` excepted) |

`cmd/pyright-go` is a small CLI over that: it takes an execution root and a file
list and prints diagnostics in pyright's `--outputjson` shape, so the two can be
diffed directly. It exists to answer "does this agree with the original on real
code", not to be a replacement for the pyright CLI.

## Still not ported

Everything below is out of scope by ANALYZER-PLAN.md, not pending. None of it
changes a diagnostic.

| module | why |
| --- | --- |
| `languageServerBase.ts`, `server.ts`, the providers (hover, completion, rename, …) | the language server |
| `backgroundAnalysisProgram.ts`, `analysis.ts` worker threads, `cancellationUtils.ts` | scheduling and cancellation; the port analyzes synchronously |
| `common/chokidarFileWatcherProvider.ts`, the service's watchers and reanalysis timer | decide *when* to analyze, not what |
| `analyzer/typeStubWriter.ts` (`--createstub`) | separate CLI feature |
| `analyzer/packageTypeVerifier.ts` (`--verifytypes`) | separate CLI feature |
| `analyzer/sourceMapper.ts`, `docStringConversion.ts`, `typeDocStringUtils.ts` | serve hover and go-to-definition |
| `analyzer/codeFlowUtils.ts` (`formatControlFlowGraph`) | a debug dump behind a verbose logging flag |
| `common/serviceProvider.ts` and friends | the DI container; the port passes dependencies directly |
| `Host.runScript` / `runSnippet` / `spawnProcess` | asynchronous and cancellable; only the language server and the stub writer call them |
| `CacheManager.getUsedHeapRatio` | reads V8 heap statistics. Go exposes no heap *limit*, so there is no ratio; it answers -1, which every caller already treats as "don't act on this" |

## How it is verified

Every check runs against the real pyright 1.1.412 sources rather than against
expectations written by hand. Run them with `make bridge` (see README.md for the
prerequisites).

| check | what it covers | result |
| --- | --- | --- |
| `make bridge-tests` | pyright's own `tokenizer.test.ts`, unmodified, run against the Go tokenizer | 91 / 91 pass |
| `make bridge-parser-tests` | pyright's own `parser.test.ts`, unmodified, run against the Go parser | 23 / 23 runnable pass, 4 skipped |
| `make bridge-evaluator-tests` | pyright's own `typeEvaluator1-8.test.ts` and `checker.test.ts`, unmodified | 1269 pass, 0 fail, 10 skipped |
| `make bridge-importresolver-tests` | pyright's own `importResolver.test.ts` | 34 / 34 pass |
| `make bridge-corpus` | token stream compared to the TypeScript tokenizer over the sample corpus | 1302 / 1302 identical |
| `make bridge-ast` | parse tree *and diagnostics* compared to the TypeScript parser | 1343 / 1343 identical |
| `make bridge-parsetreeutils` | every parseTreeUtils answer over the corpus | 1343 / 1343 identical |
| `make bridge-binder` | scopes, declarations and the flow graph | 752 × 2 modes identical |
| `make bridge-binder-typeshed` | the same over typeshed | 1504 identical |
| `make bridge-config` | `config.test.ts` project resolution | 78 / 78 identical |
| `make bridge-types-full` | the inferred type of **every name** in the corpus | 88,477 / 88,477 match over 1343 files |
| `make test` | the Go unit tests | pass |

The 4 skipped `parser.test.ts` cases and the 10 skipped evaluator cases drive the
fourslash harness, which needs a live in-process `Program`; a stateless bridge
cannot provide one. They are reported as `SKIP` with a reason rather than being
quietly dropped or counted as passing.

Two things the test suites do not reach, and so are transliterated without a
test behind them: `Program._checkDependentFiles` (notebook CellDocs chaining --
`ipythonMode.test.ts` is fourslash-only) and `FullAccessHost`'s Windows
shell-invocation branch.

The two differentials do more than the test suites can. The AST one compares
every node type, every range, every flag, every string, every numeric literal
(as IEEE bit patterns, so no float formatting is involved) and every diagnostic
message and position, over 1343 real Python files. The type one asks both
implementations for the inferred type of every name in those files and compares
the printed result — 88,477 names, no sampling.

Beyond the corpus, `cmd/pyright-go` has been run against a real project (1193
files, a virtualenv with several hundred third-party packages) alongside pyright
1.1.412 with an identical config and file list: **1193 files and 8074 errors on
both sides, zero differences**. This is the only check that exercises a real
file system, and it is where `realfs.RealCasePath` was caught silently following
symlinks — see analyzer/STATUS-STAGE-D.md.

On speed the honest summary is "comparable, and it depends on the size":

| | 84 files | 1193 files |
| --- | --- | --- |
| pyright 1.1.412 | 3.49 s / 343 MB | 58.7 s / 2814 MB |
| Go port | 0.98 s / 216 MB | 78.9 s / 3577 MB (no limit) |
| Go port, `GOMEMLIMIT=3GiB` | | 62.3 s / 2921 MB |

The port wins clearly on small runs and lands at rough parity on the large one.
Neither implementation is doing anything clever here — both are single-threaded
and neither caches across runs — so this measures batch analysis and says
nothing about language-server latency.

## Suspected bugs in the original

UPSTREAM-BUGS.md collects the places where pyright 1.1.412 looks wrong. All of
them are reproduced faithfully here -- the goal is identical behavior, so
"fixing" one would be a divergence -- and each site carries a comment pointing
at that file. Nothing there has been reported upstream yet.

## Deliberate divergences

Each is commented at the site.

- `localization`: `replaceAll` does a literal replacement. The TypeScript uses
  `String.prototype.replace` with a string argument, which gives `$&`, `` $` ``
  and friends special meaning inside the *replacement*. That is an accident of
  the JavaScript API, not intended behavior.
- `parser/characters.go`: the lazy full identifier table build does not rewrite
  the 256-entry fast table. Rewriting it would store identical values while
  racing readers. `TestFastTableUnchangedByFullBuild` pins the equivalence.
- Several lazily-initialized tables are guarded with `sync.Once` or a mutex.
  JavaScript is single-threaded and the original needs no guard.
- `shallowCopyWithNewID` (`parser/parsenodes.go`) uses reflection where the
  TypeScript uses `Object.assign({}, node)`. Same result; a 78-case type switch
  would only add somewhere for the two to drift apart.

## Things worth knowing before continuing

- **`common.Text` is `[]uint16`, and that is load-bearing.** Every offset in
  pyright is a UTF-16 code unit offset because JavaScript strings are UTF-16.
  Using Go strings would silently change every offset in any file containing a
  non-ASCII character. Do not "simplify" this.
- **Numbers cross the TypeScript bridge as IEEE bit patterns**, not as decimal
  text, so the comparison never depends on either language's float formatting
  and distinguishes `-0` from `0`.
- **Diagnostic addendum indentation is two non-breaking spaces (U+00A0)**, not
  two ordinary spaces. The original writes them literally in `diagnostic.ts`.
  The AST differential caught this; nothing else would have.
- `parser.ts` methods that return `undefined` return `nil` here; the ones that
  return an `ok` pair are noted at the declaration.
