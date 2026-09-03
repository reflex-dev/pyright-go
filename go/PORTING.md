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

It finds its typeshed via `--rootdir`, `$PYRIGHT_GO_ROOTDIR`, or a search
upward from the executable — the original reads `global.__rootDirectory`, which
a Go binary has no counterpart for.

## Still not ported

Everything below is out of scope by ANALYZER-PLAN.md, not pending. None of it
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
the 88,477-name type differential contains either construct. See
analyzer/STATUS-STAGE-D.md.

### Speed and memory

| | 84 files | 3135 files |
| --- | --- | --- |
| pyright 1.1.412 | 2.34 s / 303 MB | 80.5 s / 3676 MB |
| Go port | 0.85 s / 202 MB | 39.0 s / 5966 MB |

With `--threads` on 16 logical CPUs (3138-file revision of the same project,
median of 3 runs, measured the same day on the same machine):

| | single-threaded | `--threads` | scaling |
| --- | --- | --- | --- |
| pyright 1.1.412 | 71.5 s / 3608 MB | 37.0 s | 1.9× |
| Go port | 35.1 s / 6.0 GB | 13.3 s / ~20 GB | 2.6× |

Neither implementation scales to the core count because every worker re-parses,
re-binds and re-infers the dependency closure of its own files; that redundancy
is the design being transliterated. The port scales further than the original
mostly because its front end (the part being redundantly repeated) is 5-9×
faster. The pyright `--threads` RSS is only the parent process -- its 16 forked
workers' heaps are not in that number, so the memory comparison across the two
rows is not apples to apples; the port's ~20 GB is the whole thing.

The GC finding from the single-threaded work inverts under --threads: with 16
worker goroutines mutating one shared heap several times the single-threaded
live set, the collector's scan work saturates the cores at the default GOGC,
and the threaded run was measured at 1.03× single-threaded -- no speedup at
all. threads.go therefore sets GOGC=200 for the threaded mode (only when the
environment sets neither GOGC nor GOMEMLIMIT), which is what produces the
table above; upstream needs no such knob because each forked worker brings its
own V8 heap and collector.

The port is faster at both sizes — 2.8× and 2.1×. It was *slower* than pyright
on the large input until two rounds of profiling: three caching problems (a
1193-file benchmark went from 79 s to 27 s) and then three constant-factor ones
(the whole project went from 43 s to 35.7 s). All six are written up in
analyzer/STATUS-STAGE-D.md. The recurring shape is worth knowing before
optimizing anything here: the algorithm is usually faithful and the constant
factor is not, because the original leans on a native JavaScript primitive —
`indexOf`, `RegExp`, `Map.delete` — that Go has no equal of.

GC tuning is *not* a lever, which is easy to assume from a profile that is more
than half runtime frames: `GOGC=off` buys 5% at 3× the memory. The collector runs
on other cores alongside a single-threaded mutator, so it is not on the critical
path.

Memory is the remaining weak side, and it trades against time rather than being
free to fix. `GOMEMLIMIT` is the knob, and diagnostics are identical at every
setting:

| GOMEMLIMIT | time | peak RSS |
| --- | --- | --- |
| unset | 47.1 s | 6330 MB |
| 4GiB | 81.4 s | 4414 MB |
| 3GiB | 137.7 s | 3705 MB |
| 2GiB | 228.9 s | 2989 MB |

At pyright's memory the port is slower; at pyright's speed it wants roughly
1.7× the memory. A profile of the unbounded run is more than half garbage
collection, with no application-level hot spot left, so this is the cost of the
type model as represented in Go rather than a specific mistake.

Two structure-layout changes later recovered part of that cost -- about 400 MB
of live heap (−12%), roughly 0.5 GB of single-threaded RSS and 2-4 GB of
16-worker RSS, at parity on wall time and with identical diagnostics in every
mode. Both exploit the same V8 asymmetry: a JavaScript object is laid out with
only the properties actually set, while the transliterated Go struct charged
every instance for every field.

- `AnalyzerNodeInfo` (analyzernodeinfo.go) was 104 bytes attached to millions
  of parse nodes that carry a flowNode and nothing else. The node's `A` slot
  now holds the FlowNode directly -- no allocation at all -- and upgrades to
  the full struct the moment any other field is set. The file is the slot's
  only reader and writer, so the hybrid is invisible outside it.
- `ClassType` (types_class.go) went from 232 to 152 bytes: the ten
  `ClassDetailsPriv` fields that exist only on properties, narrowed
  TypedDicts, `functools.partial` and the `deprecated` class moved behind a
  `rare` pointer that cloneSelf deep-copies, so a clone owns its fields
  exactly as before. Reads go through same-named accessor methods; writes
  through ensureRare().

Neither implementation caches across runs, so all of this measures batch
analysis and says nothing about language-server latency.

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
