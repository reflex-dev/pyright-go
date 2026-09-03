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

`cmd/pyright-go` is a transliteration of `packages/pyright/src/pyright.ts`: the
same option table, the same parsing rules, the same text and `--outputjson`
reporters, the same exit codes. It is meant to stand in for the pyright CLI in a
script or a CI step. Four of pyright's modes are refused rather than silently
ignored, because each rests on a module listed below as out of scope:
`--watch`, `--createstub`, `--verifytypes` and `--dependencies`.

`--threads` is supported (cmd/pyright-go/threads.go). It transliterates
`runMultiThreaded` and `runWorkerMessageLoop`: the same affinity queues over
the tracked file list, the same work stealing, the same per-worker service
running `checkOnlyOpenFiles` with one file opened at a time, the same merge and
sort. Worker goroutines stand in for the original's forked processes -- which
is why a pass over the port's package-level state was needed first; see
"Deliberate divergences".

`--cachedir <dir>` (cmd/pyright-go/cache.go) is an extension with no pyright
counterpart: a run-to-run diagnostic cache, off unless the flag is given. It
rests on the property the --threads port established -- under per-file
isolation, a file's diagnostics are a function of its transitive dependency
closure plus configuration -- so each tracked file is fingerprinted over the
contents of its closure (typeshed and site-packages included, resolution
recomputed every run), unchanged files replay their stored diagnostics, and
the rest go through the --threads worker pool. The cache also stores each
file's *import descriptors* keyed by its pure content hash -- imports are a
function of content alone -- so unchanged files skip even the parse;
resolution still reruns fresh every time, which is what catches a new file
shadowing a module. Reverting an edit is a cache hit again -- the store is
content-addressed, not mtime-based. Cached output is byte-identical to the
equivalent uncached --threads run and carries the same isolation semantics
(UPSTREAM-BUGS.md #17); the reference remains the single-threaded mode.
BENCHMARKS.md has the numbers.

It finds its typeshed via `--rootdir`, `$PYRIGHT_GO_ROOTDIR`, or a search
upward from the executable — the original reads `global.__rootDirectory`, which
a Go binary has no counterpart for.

## Still not ported

Everything below is deliberately out of scope, not pending. None of it
changes a diagnostic.

| module | why |
| --- | --- |
| `languageServerBase.ts`, `server.ts`, the providers (hover, completion, rename, …) | the language server |
| `backgroundAnalysisProgram.ts`, `analysis.ts` worker threads, `cancellationUtils.ts` | language-server scheduling and cancellation; the port analyzes synchronously. The CLI's `--threads` does not rest on these -- upstream implements it in pyright.ts with forked processes, ported as goroutines |
| `common/chokidarFileWatcherProvider.ts`, the service's watchers and reanalysis timer | decide *when* to analyze, not what |
| `analyzer/typeStubWriter.ts` (`--createstub`) | separate CLI feature |
| `analyzer/packageTypeVerifier.ts` (`--verifytypes`) | separate CLI feature |
| `analyzer/sourceMapper.ts`, `docStringConversion.ts`, `typeDocStringUtils.ts` | serve hover and go-to-definition |
| `analyzer/codeFlowUtils.ts` (`formatControlFlowGraph`) | a debug dump behind a verbose logging flag |
| `common/serviceProvider.ts` and friends | the DI container; the port passes dependencies directly |
| `Host.runScript` / `runSnippet` / `spawnProcess` | asynchronous and cancellable; only the language server and the stub writer call them |
| `CacheManager`'s cross-worker heap sharing | a SharedArrayBuffer read by worker threads that do not exist here. The heap ratio itself is ported, against `GOMEMLIMIT` — see analyzer/cachemanager.go |

## How it is verified

Every check runs against the real pyright 1.1.412 sources rather than against
expectations written by hand. Run them with `make bridge` (see the repository
README for the prerequisites).

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

Beyond the corpus, `cmd/pyright-go` is run against a real project alongside
pyright 1.1.412 — same working directory, same command line, same config file,
no arguments invented for either side. In both of pyright's modes:

| | files | errors | warnings | differences |
| --- | --- | --- | --- | --- |
| pyright 1.1.412 | 3138 | 10192 | 31697 | — |
| Go port | 3138 | 10192 | 31697 | **0 missing, 0 spurious** |
| pyright `--threads` | 3138 | 10190 | 31697 | — |
| Go port `--threads` | 3138 | 10190 | 31697 | **0 missing, 0 spurious** |

(The project has grown since earlier revisions of this file quoted 3135 files.)

The threaded rows agreeing at 10190 rather than 10192 is not sloppiness; it is
fidelity. Upstream's `--threads` deterministically loses two diagnostics
(UPSTREAM-BUGS.md #17), and the transliterated worker model loses **exactly the
same two**, which localizes the upstream bug to the per-file isolation
semantics rather than to partitioning or a race. The port's threaded output is
identical run to run, and `go build -race` over the full threaded project run
reports zero data races.

Text output is byte-identical; JSON output differs only in `version` and the
timestamp; exit codes match across `--level`, `--warnings`, `-p`, stdin file
lists and the error paths.

This is the only check that exercises a real file system and a real virtualenv,
and it has earned its place twice: `realfs.RealCasePath` was caught silently
following symlinks, and `specializeWithUnknownTypeArgs` was caught passing
`isTypeArgExplicit: true` where the original passes `false` — which cost
`isinstance(x, tuple)` its type argument. Neither the 1,279 evaluator tests nor
the 88,477-name type differential contains either construct.

### Speed and memory

BENCHMARKS.md is the performance document: the numbers, the scenarios, the
worker-count scaling and the methodology all live there. What belongs here is
the performance *character* of the transliteration, because it is a porting
lesson as much as a result:

- **The front end is several times faster than the original** (tokenize,
  parse, bind, resolve) and totals a few seconds; **checking dominates every
  run**, and its relative speed depends on the workload -- ahead of pyright
  on stub-light closures, currently behind it on heavy third-party inference
  (BENCHMARKS.md has both). Anyone optimizing this code should profile the
  check phase and nothing else.
- **When the port is slower than it should be, the algorithm is usually
  faithful and the constant factor is not**, because the original leans on a
  native JavaScript primitive -- `indexOf`, `RegExp`, `Map.delete`, V8's
  pay-for-what-you-set object layout -- that Go has no equal of. Every such
  finding so far (the quadratic type-cache deletes, the tokenizer's
  unbounded scans, the include-spec scan, the always-full structs) had this
  shape.
- **Memory trades against time rather than being free to fix.** The port
  runs faster than pyright at higher RSS; `GOMEMLIMIT` bounds it with
  identical diagnostics at every setting. A profile of an unbounded run is
  more than half garbage collection with no application hot spot left: the
  remaining cost is pyright's clone-on-specialize type model as represented
  in Go, not a specific mistake.
- **GC behavior differs by mode.** Single-threaded, the collector runs on
  otherwise-idle cores and is nearly free, so GOGC tuning buys little.
  Under --threads, the workers share one heap several times the
  single-threaded live set and the collector competes with them, so the
  threaded mode raises GOGC itself (see threads.go) -- upstream needs no
  such knob because each forked worker brings its own V8 heap.
- **Parallel checking duplicates work by design.** pyright's worker model
  gives each worker its own program, so every worker re-infers the
  dependency closure of its files; scaling stops well short of the core
  count in both implementations, and no partition of the user files can fix
  it (the duplicated work is the typeshed and third-party closure, which
  every worker needs).

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
- `--threads` shares one address space where the original forks processes, so
  every piece of module-level mutable state the original relies on had to be
  found and made goroutine-safe. The full inventory, each commented at the
  site: the interned `Uri` instances' lazy fields (common/uri -- the
  `combineChildren` map would panic outright under concurrent use); the type
  singletons' `cached` slots (pre-filled at init by analyzer/prewarm.go so
  runtime only reads them); the `anySpecialForm` wiring in
  typeevaluator_prefetch.go (upstream re-mutates the singleton per evaluator,
  last write wins; here first write wins, once, under a mutex); the
  `enumEvalStack` and `protocolAssignmentStack` recursion stacks (module-level
  upstream, moved onto the typeEvaluator, which is what "module-level" means
  under a process-per-worker model); the `programNextId` / `nextUniqueFileId`
  counters (now atomic); and `TimingStatsInstance` (now internally locked; its
  numbers are still not meaningful under --threads, which upstream acknowledges
  by rejecting --stats with --threads). Found by `go build -race` over a full
  threaded project run, which now reports zero races. Single-threaded behavior
  is unchanged -- every gate below was re-run after the pass.
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
