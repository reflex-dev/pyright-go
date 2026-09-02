# Stage D — the type evaluator and checker

Stage C left two seams: `TypeEvaluator` is an opaque value nothing calls a
method on, and `Checker` comes from a factory that may be nil. Stage D fills
them. It is 59,031 lines of TypeScript across 21 files, `typeEvaluator.ts` alone
being 30,245 — larger than everything already ported, several times over.

Nothing of the evaluator is written yet. What exists is the gate.

## The gate

`tests/testUtils.ts` funnels every one of the 1,279 test cases in
`typeEvaluator1-8.test.ts` and `checker.test.ts` through two functions:
1,385 calls to `typeAnalyzeSampleFiles` and 1,383 to `validateResults`. Those
tests assert on nothing but the six diagnostic lists the first returns.

So the whole suite runs against this port by aliasing that one module. The nine
`.test.ts` files are the originals, byte for byte, and `validateResults` — the
code that decides pass or fail — is re-exported from pyright rather than
reimplemented.

    make bridge-evaluator-tests

The Go half is the `analyze` op in `cmd/tokenserver/analyzebridge.go`, which
builds a Program the way `typeAnalyzeSampleFiles` does, analyzes to completion,
and hands back the diagnostics. The evaluator and checker get wired in at
`cmd/tokenserver/stagedfactories.go`, which is deliberately one function.

### The scoreboard is split, because the obvious number is worthless

An implementation that reports nothing passes every test that asserts zero
diagnostics, and roughly half of these do. Before a single line of the evaluator
existed, the port scored **619 of 1,269**. That number measures nothing.

So `validateResults` records whether the test expected any diagnostic at all,
and the two kinds of pass are counted apart:

| | substantive | vacuous | failed | skipped |
| --- | --- | --- | --- | --- |
| TypeScript oracle | 680 | 589 | 0 | 10 |
| Go, no evaluator | **30** | 589 | 650 | 10 |

680 is the ceiling. 30 is where Stage D starts — those are tests asserting
syntax errors, which the ported parser already produces.

### Validated before it was trusted

`make bridge-evaluator-oracle` runs the same suite with the same wire format in
both directions, but answers from the TypeScript evaluator. It is 1,269 passed,
0 failed, 10 skipped. Anything the serialization loses shows up there with no Go
code in the picture — the same discipline the binder differential was built
under, and for the same reason: a harness that has never been seen to fail is
not evidence of anything.

The config wire format carries four things, because that is all these tests can
reach: `defaultPythonVersion`, `defaultPythonPlatform`, `defineConstant` and the
96-field `diagnosticRuleSet`. That claim is not a survey — every call
reconstructs a `ConfigOptions` from the wire and deep-compares it against the
one the test built, so a test that sets anything uncarried fails loudly instead
of being analyzed under a config that quietly differs. 1,385 checks per run.

## Vacuity

Six probes, all six turn the gate red.

| probe | result |
| --- | --- |
| the wire drops `defaultPythonVersion` | guard fires, 169 tests |
| the wire drops `defaultPythonPlatform` | guard fires (typeEvaluator7) |
| the wire drops `defineConstant` | guard fires (typeEvaluator7) |
| the wire carries a default rule set | 3 failed of 159 |
| the response drops the last diagnostic | 75 failed of 159 |
| the response drops the deprecated category | 2 failed of 154 |

Two of these had to be re-run on a different file before they went red: nothing
in `typeEvaluator1.test.ts` sets `defaultPythonPlatform` or `defineConstant`, so
dropping them there is a no-op. That is a fact about which file exercises what,
not about the guard, and it is why the probe set names the file each time.

## The ten skipped tests

Nine in `typeEvaluator8.test.ts` stand up a fourslash `TestState` and call
`state.program.evaluator.getType(node)` on a live in-process Program. One in
`typeEvaluator1.test.ts` reaches through `parseResults` for the module scope.
Neither survives a stateless per-call bridge, so both raise the marker the
harness reports as a skip. Returning something plausible instead would turn an
untested thing into a passing test.

## Divergences

**`NoAccessHost` in place of `FullAccessHost`.** The original shells out to a
Python interpreter for search paths; the interpreter-spawning `Host` members are
not ported. On this machine that is a live difference — `FullAccessHost` returns
`/usr/lib/python3.14{,/lib-dynload,/site-packages}` and the Go side returns
nothing.

It is verified inert rather than assumed inert: running the oracle with no
`python` reachable on `PATH`, so `FullAccessHost` finds no interpreter and
returns an empty list, gives an identical 1,269 / 0 / 10 and an identical 680
substantive. No sample in this corpus resolves an import through site-packages.
If one ever did, it would show up as an unresolved-import error in Go and a
clean run in the oracle.

## Still to build

- **`expected_text`** — 3,330 `reveal_type(x, expected_text="...")` assertions
  across 503 sample files. `internalTestMode` turns a mismatch into an error
  diagnostic inside the evaluator itself, so this needs no TypeScript side at
  all and starts counting the first day `getType` returns anything.
- **The per-node type differential** — walk every `NameNode` and diff
  `printType(getType(node))` against the TypeScript. This is the binder
  differential's trick again, and it is what localizes a failure to a single
  expression instead of "3 errors instead of 2". Pyright hands over the walk:
  `typeAnalyzeSampleFiles` already installs a pre-check callback that runs
  `NameTypeWalker` over every name before checking.

## Order

| step | files | lines |
| --- | --- | --- |
| 1 | `typeEvaluatorTypes.ts` — the 88-method interface | 900 |
| 2 | evaluator core: name / member-access / call / index / binary-op | ~12k |
| 3 | `codeFlowEngine`, `typeGuards`, `constraintSolver`, `constraintTracker` | 6,716 |
| 4 | the class-shape satellites, lit one at a time | ~11k |
| 5 | `checker.ts` | 7,859 |

Step 1 is the decision that matters, the way `Type`'s representation was in
Stage A. `createTypeEvaluator` is a closure over locals returning an interface;
the satellites already take `evaluator: TypeEvaluator` as a parameter, so they
translate to Go functions over an interface almost mechanically, and the
closure's locals become struct fields. Getting that wrong is ruinous once 30k
lines sit on top of it.

Evaluation order is observable through the type caches, so loops get
transliterated literally, and the recursion and inference limits get ported as
constants rather than as judgement.
