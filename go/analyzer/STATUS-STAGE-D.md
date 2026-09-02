# Stage D — the type evaluator and checker

Stage C left two seams: `TypeEvaluator` is an opaque value nothing calls a
method on, and `Checker` comes from a factory that may be nil. Stage D fills
them. It is 59,031 lines of TypeScript across 21 files, `typeEvaluator.ts` alone
being 30,245 — larger than everything already ported, several times over.

The gate, the differential and the evaluator's state layer exist. The evaluator
is installed and reachable; almost all of what it does is still unported, and it
says so rather than guessing.

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

## The per-node type differential

    make bridge-types

The gate says a file reported three errors instead of two. That is nearly
useless for finding out which of the expressions in it went wrong, in a file
that has hundreds and an evaluator that has 30,000 lines. So this asks both
implementations for the type of every name in the corpus and diffs them one name
at a time: name `x`, pre-order index 412, pyright says `list[int]`, the port says
`list[Unknown]`.

The walk is pyright's own — `NameTypeWalker` from `analyzer/testWalker.ts`,
which `typeAnalyzeSampleFiles` already runs over every file before checking it.
Both sides apply the identical filter.

`make bridge-types-oracle` runs the TypeScript side alone. Over 300 files it
types 19,652 names into 1,835 distinct types, with all three non-type outcomes
exercised (15 unreachable, 921 untyped, 0 thrown) — so no branch of the dump is
dead code.

Over the whole corpus:

| | node sets | names matched |
| --- | --- | --- |
| no evaluator | 1280 of 1343 | 0 of 88,487 |
| evaluator installed, reachability ported | 1280 of 1343 | **158** of 88,487 |

On the 60-file sample the same sequence reads 0 → 3 → 74 as the contextual
layer, the context walk and the dispatch land. The 74 are the nodes both sides
agree have no type at all — a module name, a pattern, a name the walk returns
from without evaluating — which is a real agreement rather than two Unknowns
meeting.

All 158 were computed rather than inherited from an Unknown that happened to
agree. 88,487 is the denominator Stage D climbs. The 63 files whose node sets
disagree are the ones below.

### It found something on the first run

Node-set agreement — do both sides pick out the same names — was supposed to be
green from day one. It is a syntactic question, and `bridge-ast` already shows
the parse trees agree over the whole corpus.

It is not green, because **the evaluator mutates the parse tree**.
`typeEvaluator.ts:30085` parses a string annotation on demand and grafts the
result onto the `StringListNode`:

    node.d.annotation = parseResults.parseTree;

In `a: Annotated[Annotated[int, "hi"], "hi"]` the TypeScript side finishes
analysis with two extra `NameNode`s for `hi` that were not in the tree the
parser produced, and every pre-order index after them shifts by two. Forward
references do the same thing all over the corpus.

The parser's own share of this is already ported — `parser.ts:5217` is
`parser_strings.go:302`. It is the evaluator's share that is missing. So
node-set agreement is not a fixed property of the two parsers: it is a measure
of how much of the evaluator's tree-grafting has landed, and it reaches every
file when the string-annotation path does. 63 of 1,343 files are affected —
every one of them a file with a forward reference or a string type argument.

Worth knowing before writing the evaluator rather than after: it means parse
trees are not immutable during analysis, which is not what the front-end port
had any reason to assume.

## Why the gate could not move, and what closed it

The gate asserts on six diagnostic lists. Two separate things had to exist
before any of them could see anything the evaluator concluded, and both were
missing for the whole first half of Stage D:

1. **`addDiagnostic` was a stub.** The evaluator's only route to the diagnostic
   sink did nothing, so a conclusion could not become a diagnostic.
2. **No checker was installed.** `sourcefile.go` runs the checker *and* drains
   the sink inside one `if s.checkerFactory != nil` block. With no checker,
   nothing walked a file to drive the evaluator and nothing collected what the
   evaluator wrote.

Both are now closed, and the checker walk is verifiably live:
`checker.reportUnusedExpression` records 9,880 hits over the gate corpus, which
is one per expression statement in 1,343 files. The gate has not moved yet
because the evaluator still bottoms out in stubs before it reaches a
disagreement worth reporting -- but it also has not regressed, and no false
positives appeared, which is the result that matters when a walk over the whole
corpus is switched on for the first time.

## Still to build

- **`expected_text`** — 3,330 `reveal_type(x, expected_text="...")` assertions
  across 503 sample files. `internalTestMode` turns a mismatch into an error
  diagnostic inside the evaluator itself, so this needs no TypeScript side at
  all. It is deliberately deferred until the evaluator core: the honest
  numerator is a counter at the evaluator's own comparison site, because
  counting mismatch diagnostics alone scores 3,330 of 3,330 when the evaluator
  does nothing, and re-extracting the assertions syntactically would duplicate
  logic that lives inside the evaluator and could drift from it.

## Order

| step | files | lines | state |
| --- | --- | --- | --- |
| 1 | `typeEvaluatorTypes.ts` — the 109-member interface | 900 | done |
| 2 | evaluator core: name / member-access / call / index / binary-op | ~12k | name path done end to end; call, index, member-access, binary-op remain |
| 3 | `codeFlowEngine`, `typeGuards`, `constraintSolver`, `constraintTracker` | 6,716 | tracker + reachability done; `isCallNoReturn` and `getFlowTypeOfReference` remain |
| 4 | the class-shape satellites, lit one at a time | ~11k | `getTypeOfClass` done; decorators, dataClasses, typedDicts, enums, protocols remain |
| 5 | `checker.ts` | 7,859 | walk + `check()` done; 50 of 52 visit methods remain |

### What is complete, end to end

The **name path** now runs from a NameNode to a printed type with nothing
stubbed along the spine:

    GetType -> contextual cache -> context walk -> getTypeOfExpression
      -> node dispatch -> getTypeOfName -> lookUpSymbolRecursive
      -> getEffectiveTypeOfSymbolForUsage -> [ getDeclaredTypeOfSymbol
                                             | inferTypeOfSymbolForUsage ]
      -> getTypeForDeclaration / getInferredTypeOfDeclaration
      -> getTypeOfClass | getTypeOfAnnotation | ...
      -> printType

Underneath it: the prefetch bootstrap (`object`, `type`, `int`, `str`, `tuple`
resolved out of typeshed), class creation, the diagnostic-reporting layer, the
checker walk that drives all of it, and `makeTopLevelTypeVarsConcrete`.

### The next four, ranked by corpus hits

| entry | hits | what it needs |
| --- | --- | --- |
| `codeFlowEngine.isCallNoReturn` | 4,309 | call evaluation |
| `applyClassDecorator` | 1,020 | `decorators.ts` |
| `getTypeOfIndex` | 799 | `operations.ts` and type-argument handling |
| `evaluateTypesForAssignmentStatement` | 569 | assignment targets |

`getTypeOfFunctionPredecorated` (347) is the largest single remaining unit that
is self-contained; `getTypeOfCall` (~3,000 lines) and `assignType` (~2,000) are
the two that everything else eventually waits on.

### What "state layer done" means

`typeevaluator.go` has the evaluator's constants, its state and the type-cache
layer: `createTypeEvaluator`'s two dozen closure locals as struct fields, the
ordinary and TypeForm caches, the shared incomplete-generation counter, the
symbol-resolution stack, the return-type-inference context stack, and the
speculative-mode interaction.

The evaluator is **installed** — `stagedfactories.go` builds one, so the gate and
the differential exercise it for real. What it cannot do yet is 103 of the 109
interface members, each a stub in `typeevaluator_unported.go`.

### The stubs count themselves

Installing an evaluator that does almost nothing is only defensible if the
nothing is visible. Each stub records itself, the counts come back through the
bridge, and the differential prints them. The result is a work-remaining map
measured over the corpus rather than guessed at from reading the source: which
interface members the sample files actually reach, and how often.

It is a **frontier**, not a full map. The first stub a name touches
short-circuits the rest, so before reachability landed the whole corpus reported
one entry, `IsNodeReachable`. Each thing ported reveals the next layer:

| after | frontier |
| --- | --- |
| nothing | 1 entry: `IsNodeReachable` |
| reachability | 5 entries, led by `isCallNoReturn` and `GetType` |
| symbol lookup | 5 entries; `LookUpSymbolRecursive` gone, `getTypeNarrowingCallback` appears behind it |
| the contextual layer | 5 entries; `GetType` resolves into `evaluateTypesForExpressionInContext` |
| the context walk and the dispatch | **30 entries** |
| getTypeOfName | 40 entries |
| declaration resolution | 38 entries; `getDeclaredTypeOfSymbol` resolves into what it dispatches to |
| the prefetch bootstrap | 37 entries; `GetTypeOfClass` jumps 284 -> 1244 hits as the bootstrap reaches for `object` and `type` |
| class creation and the printer | 38 entries; `GetTypeOfClass` gone |
| the diagnostic layer | 38 entries; `AddDiagnostic` gone |
| the checker walk | **46 entries**, and the corpus-wide count becomes visible for the first time |
| the leaf expressions and `makeTopLevelTypeVarsConcrete` | 41 entries over the 60-file sample |
| getTypeOfName's outbound adjustments | 39 entries; differential 133 -> 151 |
| the inference fork and `getInferredTypeOfDeclaration` | 42 entries; 7 vacuous matches correctly lost |
| `getTypeOfAnnotation` and `getTypeOfFunction` | **45 entries** |

The last step is the one that pays. `evaluateTypesForExpressionInContext` and
`getTypeOfExpressionCore` are both pure dispatch — every arm hands off to
something that lives elsewhere — so porting the two of them turned one name into
a ranked list of the actual remaining units of work:

    unported paths reached: 46 distinct, 125922 hits    (the full gate corpus)
        21702  codeFlowEngine.isCallNoReturn
        19560  useSignatureTracker
        14348  applyClassDecorator
        13368  MakeTopLevelTypeVarsConcrete
        12304  getTypeOfIndex
         9880  checker.reportUnusedExpression
         5212  getTypeOfCall
         3343  inferTypeOfSymbolForUsage
         2958  getTypeOfEllipsis
         2322  ensureSignatureIsUnique
             ... and 36 more

That is the right shape for the work: it always names the thing to port next,
ranked by how much of the corpus is waiting on it. And it is a *ranking*, which
is why a sample of the corpus answers the same question as the whole of it —
`make bridge-types` runs 150 files in under a minute, `bridge-types-full` runs
all 1,343 in about ten. The first belongs in the edit loop; the second belongs
in a commit message.

This also closed a hazard the moment it opened. The unported `IsNodeReachable`
answers `false`, which renders as `<unreachable>` — a marker the TypeScript side
also produces for genuinely unreachable code. Left alone the port would have
scored matches on names it never evaluated. Every evaluator call in the
differential is now bracketed by the counter, and an answer that touched a stub
is reported as `<unported>`, which can never match. The type scoreboard is split
the same way the gate's is: matches that are a real type, against matches that
are `Unknown` or a marker.

### Reachability

`getFlowNodeReachability` is ported — the first evaluator member answered for
real. It was first because the frontier named it, not because it was planned.

Most of reachability is a graph walk over the flow graph the binder already
built, which the binder differential says matches pyright's over the whole
corpus. Four cases are not, and call back into the evaluator: the pattern-narrow
node, a condition whose reference has a declared type, a call that might return
`NoReturn`, and a context manager that might suppress exceptions. Those reach
stubs today and count themselves, which is how `isCallNoReturn` became the top
of the frontier.

One thing was transliterated rather than corrected. The recursive walk reads its
cache with the id of the *entry* node, not the node currently being visited:

    const cacheEntry = reachabilityCache.get(flowNode.id);   // not curFlowNode

Inside one walk that is a check on where the walk started rather than on where
it is. It looks like a slip, but changing it changes which results get reused
and therefore which answers come out, so it is preserved with a comment. It is
not in UPSTREAM-BUGS.md: without tracing pyright's behaviour on a case where the
two differ, "looks wrong" is not a defect report.

### Symbol lookup

`lookUpSymbolRecursive` and `isFlowPathBetweenNodes` are ported, and portable in
full: name resolution is a walk over the scope chain the binder built, filtered
by the reachability walk. No type evaluation is involved.

That resolves *which symbol* a name refers to. Saying what that symbol's type is
is `getEffectiveTypeOfSymbol`, and it is still the wall.

### Why there is no smaller step

`getTypeOfExpression`'s dispatch is easy, but the first case that produces a real
answer — an integer literal — goes `getTypeOfNumber` → `getBuiltInObject` →
`getBuiltInType` → `lookUpSymbolRecursive` → `getEffectiveTypeOfSymbol` →
declaration resolution → and, because `int`'s declaration is a class, the whole
class-creation path. There is no vertical slice that returns a type rather than
`Unknown`. That is the all-or-nothing property ANALYZER-PLAN.md predicted for
this stage, met in practice and now measured.

The two tuning-constant blocks and the flag-mismatch debug path are ported
verbatim even though both debug switches are off in the original. The constants
change *which* type comes out, not merely how fast, so a plausible-looking Go
value would be a silent behaviour change.

Step 1 is the decision that matters, the way `Type`'s representation was in
Stage A. `createTypeEvaluator` is a closure over locals returning an interface;
the satellites already take `evaluator: TypeEvaluator` as a parameter, so they
translate to Go functions over an interface almost mechanically, and the
closure's locals become struct fields. Getting that wrong is ruinous once 30k
lines sit on top of it.

Evaluation order is observable through the type caches, so loops get
transliterated literally, and the recursion and inference limits get ported as
constants rather than as judgement.
