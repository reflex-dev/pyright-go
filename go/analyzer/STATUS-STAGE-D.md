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

88,487 is the denominator Stage D climbs. The 63 files whose node sets disagree
are the ones below.

### Reading the two numbers

The headline count is split, and the split is the point:

    types: 532 of 2945 names match
      of those, 394 were computed and 138 are Unknown or unported

Only the **computed** number is un-gameable. An implementation that answers
Unknown everywhere agrees with pyright wherever pyright also says Unknown, and
that agreement is worth nothing. Every evaluator call in the differential is
bracketed by the unported counter, so an answer that touched a stub reports
`<unported>` and can never match.

This has made the honest result a falling headline more than once: total matches
drop while computed holds or rises, because vacuous Unknown-vs-Unknown
agreements are correctly reclassified as `<unported>`.

On the 60-file sample, computed matches over this session:

| after | computed | frontier hits |
| --- | --- | --- |
| the context walk and dispatch | 0 | — |
| `printType`, class creation, `assignType` | 341 | 6,809 |
| `assignTypeToNameNode` and `narrowTypeBasedOnAssignment` | 341 | 5,896 |
| `addOverloadsToFunctionType`, the typing-stub pair | 339 | 5,961 |
| `applyTypeArgToTypeVar`, the expected-type cache | 339 | 4,973 |
| `inferVarianceForClass`, the enum literal expansion | 339 | 4,045 |
| **`getTypeOfMemberAccess`** | **378** | 4,156 |
| the code flow walk, `evaluateTypeForSubnode` | 391 | 3,526 |
| return-type inference | **394** | 3,280 |

`getTypeOfMemberAccess` is the largest single jump since `printType`, which is
what one would expect: `a.b` is the most common expression in Python after a
bare name.

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
is one per expression statement in 1,343 files.

### The gate's split scoreboard, and why total passes fell

    446 passed, 823 failed, 10 skipped, 1279 total
      of the passes, 60 assert at least one diagnostic and 386 assert none

The second line is the one to read. A test that asserts zero diagnostics passes
against an implementation that reports nothing, so 386 of those passes say
nothing about the port. Substantive passes went **42 → 60** over this session
while total passes went **547 → 446**, and both movements have the same cause:
`reportAssignmentType`, `reportUnknownVariableType` and `reportUnknownMemberType`
became live. Diagnostics emitted went 1,182 → 2,358. Tests that need a real
diagnostic started passing; tests that had been passing on silence started
failing.

### Where the false positives come from

The two loudest rules were instrumented rather than guessed at:

- **987 × `reportInvalidTypeForm :: Variable not allowed in type expression`.**
  Every one is reported on a type that prints as `Unknown`.
  `isSymbolValidTypeExpression` is faithful — pyright reports on an Unknown
  there too. It simply never gets one, because its evaluation succeeded.
- **`TypeNarrowingIsNone1`, expected 0 errors, got 10.** `x.bit_length()` after
  `if x is not None` reports unknown-member, because the narrowing did not
  happen.

Both trace to the same missing piece rather than to a mistransliteration. The
false positives are a measure of how much of the evaluator is still stubbed,
not of how much of it is wrong, and they retire as the frontier does.

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
| 2 | evaluator core: name / member-access / call / index / binary-op | ~12k | name, member-access shell, assignment, annotation, class and function creation, `assignType` done; `validateArgTypes`, binary-op, string-list remain |
| 3 | `codeFlowEngine`, `typeGuards`, `constraintSolver`, `constraintTracker` | 6,716 | tracker, reachability, `isCallNoReturn`, the narrowing walk and return-type inference done; `typeGuards.getTypeNarrowingCallback` and the solver's four `assign*` arms remain |
| 4 | the class-shape satellites, lit one at a time | ~11k | `getTypeOfClass`, variance inference and the enum literal expansion done; decorators, dataClasses, typedDicts, protocols, properties remain |

### A stub can hide a bug, and porting it reveals one

Three times this session a *duplicate stub shadowed an already-ported function*
— `isVarianceOfTypeArgCompatible`, `evaluateTypeForSubnodeWithCache` and
`explodeGenericClass` were all fully implemented in the `typeutils_*` files
while the evaluator carried a second copy that called `unported()`. The call
sites reached the stub. Grep for a name in both places before writing it.

### The instrument is part of the system under test

Porting `createGenericType` faithfully dropped computed matches from 538 to
448, twice, in two different sessions. Both times it was held back, and the
second time the stub carried a confident comment naming
`buildTypeParamsFromTypeArgs` as the downstream consumer at fault.

That comment was wrong. So was the earlier conclusion. `createGenericType` was
correct the whole time and is worth **+186** computed matches.

The defect was in the oracle. `dump-types.ts` never set
`global.__rootDirectory`, which is what `RealFileSystem.getModulePath()` reads
to locate `typeshed-fallback`; unset, it returns `Uri.empty()` and the oracle's
import resolver finds no stubs at all. Every `from typing import ...` bound
Unknown, every builtin annotation evaluated to Unknown, and
`def f(x: list[int])` came back as `(x: Unknown) -> Unknown`. **1387 of 2012
reported differences were the oracle answering Unknown to a question the Go
side answered correctly.**

`Generic` in particular resolved to Unknown, so in the oracle no class was ever
generic and `class B(Generic[T])` printed as `type[B]`. A stubbed
`createGenericType` reproduced that by accident. Porting it made the Go side
*disagree with a broken oracle*, which the scoreboard reported as a
regression.

Fixing the one missing line moved the same sample from 880 matches / 759
computed to 1933 / 1900.

The rule the earlier version of this section stated -- *a port that lowers
computed matches is reporting a bug somewhere else* -- is right as far as it
goes. What it missed is that **"somewhere else" includes the instrument**. The
correct order is: reproduce the regression on the smallest possible input,
then confirm the oracle's answer is one pyright would actually give, and only
then go looking in the port. Two sessions of held-back work and a stub carrying
a fabricated explanation is what skipping that step cost.

### The frontier, and what it is not

Every unported evaluator member records itself through `e.unported(name)`, and
both harnesses print the ranked tally. It is a **frontier, not a map**: the
first stub short-circuits everything behind it, so porting one entry reveals a
layer that was invisible before. A count going *up* after a port is the normal
case, not a regression — `addOverloadsToFunctionType` removed 434 hits and
exposed 626 new ones behind it.

Free functions in `codeFlowEngine`, `constraintSolver` and `decorators` reach
the counter through an `interface{ noteUnported(string) }` assertion rather than
through the evaluator struct, since they take the interface.

The single largest remaining entry is `ValidateCallArgs`, at 32 percent of the
frontier on the sample and 20,813 hits on the gate corpus. Nothing else is
within a factor of four of it.
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

## The gate runs again, and it has a number

For most of Stage D the gate (`make bridge-evaluator-tests`, pyright's own 1,279
test cases) could not be run to completion. One sample file --
`tests/samples/solverHigherOrder3.py`, whose subject is a generic function
passed to itself -- made the analyzer spin at 100% CPU and 8 GB of resident
memory indefinitely. A single hanging file blocks the whole suite, so there was
no gate number at all, only the per-node differential.

Bisecting showed the hang predated the typeGuards work by several commits. The
cause is documented as upstream bug #16: `solveTypeVarRecursive` can produce a
solution that maps a TypeVar to a type mentioning that same TypeVar, because the
occurs check in `widenLowerBound` runs when the *bound* is recorded and the cycle
is created later, when dependent solutions are substituted in. Expanding such a
solution re-enters the TypeVar at every nesting level until the recursion cap:
finite, but billions of transformer calls.

Two lessons, both about instruments rather than about the port:

**A differential that samples cannot find a hang.** `make bridge-types
TYPES_SAMPLE=60` ran green throughout, because the corpus is sorted and the
hanging file sorts past the sample. The cheapest thing that would have caught it
is what eventually did: feed every corpus file to the server one at a time under
`timeout`, and report the ones that do not answer. That probe takes a few
minutes and should be run before every gate attempt, not after a gate stalls.

**`SIGQUIT` is the first thing to reach for, not the last.** `timeout -s QUIT`
on the Go server prints every goroutine's stack. That named the offending
subsystem in one command. An hour was spent watching a process consume CPU
before anyone asked it what it was doing.

The gate now completes: **453 of 1,279 passing.** That is the first honest
Stage D number. It is low, and the differential explains why -- the evaluator
answers most *types* correctly while the checker that turns those types into
diagnostics is barely started, and the gate asserts only on diagnostics.

## From 453 to 822: the evaluator finishes, the checker starts

The evaluator frontier is essentially clear. What remained after the hang fix
came down in a few batches -- typeGuards, the special-form subscript handlers,
forward references, destructuring, subscript evaluation, the protocol-binding
statements, `assert_type`, `super()`'s siblings -- and the 60-file per-node
differential moved from 2,226 computed matches at the start of this work to
2,848, with all 60 files agreeing on which names to type. Four evaluator paths
remain, worth 14 hits on that sample.

Three findings from this stretch are worth keeping.

**A sampling differential also cannot find a panic.** The hang lesson above has a
sibling. The harness reports a crashed file as a one-line error and moves on, so
54 of the 1,302 corpus files were dying on a nil dereference while the 60-file
sample surfaced exactly one of them. Feeding the whole corpus through the
`nodetypes` op and counting `error` envelopes is a separate check from the
timeout probe, and it has to be run separately. `PYRIGHT_GO_PANIC_STACK=1` now
makes the bridge server dump a goroutine stack instead of a bare message, which
is what localized all three underlying causes in minutes.

**Go's typed nil is the single most dangerous transliteration hazard in this
port.** All three panic causes were the same shape: a function declared to return
`*ClassType` returns nil, that nil is assigned to a `Type`, and the resulting
interface is non-nil while dereferencing to nothing -- so every `x === undefined`
check the original relies on silently fails. TypeScript's `undefined` does not
have this property. `IsNilType` and its use in `MakeInferenceContext` are the fix
at the chokepoint every "no expected type" path passes through; the rule for new
code is that any boundary where a concrete pointer becomes a `Type` needs
`IsNilType`, not `== nil`.

**Landing half a pass can lose ground.** The checker's symbol-table validation
went in before the visit methods that drive evaluation, and cost 13 gate tests.
The evaluator is lazy, and evaluating a name is also what marks its symbol
accessed -- so with no `visitClass`, `visitFunction` or `visitAssignment`,
nothing ever evaluated a base-class list, a decorator, an annotation or a
right-hand side, and essentially every import and local variable in the corpus
read as unused: 90 "Any is not accessed", 568 "x is not accessed". The pass was
correct; the walk underneath it was not there yet. The two must land together.

The checker's walk is now complete and its symbol-table pass runs.
**822 of 1,279 passing.** Of the 447 remaining failures, 241 over-report errors
and 183 under-report, which is the ratio to watch: over-reporting is usually one
eager check, under-reporting is usually a whole validator still on the frontier.

### What is left

The evaluator: `getTypeOfSuperCall`, `validateCallForClassInstance`,
`namedTuples.createNamedTupleType`.

The checker: roughly forty per-class and per-function validators, each named
individually on the frontier rather than hidden behind one gap, so the ranking
shows what each costs. The largest by reach are `validateBaseClassOverrides`,
`validateFunctionParams`, `validateFunctionTypeVarUsage`,
`validateInstanceVariableInitialization` and `validateEnumMembers`.

Whole files not yet started: `dataClasses.ts` beyond the decorator behaviors,
`patternMatching.ts`, `namedTuples.ts`, `sentinels.ts`, and the rest of
`typedDicts.ts`.

One class of bug found here is worth a standing check: **swapped arguments to a
generated message format**. Six operator diagnostics passed
`(operator, left, right)` into a `(leftType, rightType, operator)` signature.
Every parameter is a string, so the compiler is silent, and the diagnostic still
lands at the right node under the right rule, so count-based assertions pass --
only a text comparison sees it. The audit that found them matches each argument
expression against the generated parameter name and is worth re-running whenever
a batch of diagnostics lands.

## Check the dependencies before porting the caller

A validator built on an unported primitive is worse than no validator. The
multiple-inheritance override checks were written, were faithful, and cost three
gate tests, because `ValidateOverrideMethod` is still a stub returning `false` --
so every comparison answered "incompatible" and produced 26 spurious
diagnostics.

An earlier audit in this session swept every stub for a dangerous default return
value and found exactly one (`IsEnumClassWithMembers`, returning `true`, which
had already cost 12 tests when a new caller inverted its safe direction). That
audit asked the wrong question. `false` is a fine default for an unported
predicate right up until something depends on it answering truthfully. The
hazard is not the default -- it is a new caller.

The check is free and should precede every port: grep the target's dependencies
for `unported(`. The gate already ranks unported paths by hits, and the blocking
primitive was in that ranking the whole time.

This is the fourth member of a family that now dominates the port's real bugs,
and all four are invisible to the compiler:

- **typed nil** -- `x === undefined` silently fails for a nil pointer boxed in an
  interface.
- **slice-header aliasing** -- an array shared by reference in JavaScript is
  copied by value in Go, so later appends are lost.
- **non-zero argument defaults** -- `additionalFlags = SkipObjectBaseClass` does
  not translate to the zero value; "the original omits it" means passing that
  flag explicitly.
- **stub defaults with a new caller** -- the safe answer is a property of the
  caller, not of the stub.

## synthesizeDataClassMethods is the keystone, not a to-do

`ClassType.Shared.DataClassEntries` and `ClassType.Shared.NamedTupleEntries` are
both read in several places and **assigned nowhere in the Go tree**. Upstream,
both are populated by `dataClasses.synthesizeDataClassMethods` (761 lines), with
`namedTuples.createNamedTupleType` covering the functional NamedTuple form.

Four readers of `DataClassEntries` exist today and three are in already-landed
code:

- `typeutils_members.go` — member lookup over dataclass entries.
- `protocols_members.go` — protocol matching against a dataclass.
- `checker_multiinherit.go` — the frozen-dataclass exemption in
  `compareInheritedVariables`. Because the field is nil the exemption never
  fires, so an inherited frozen-dataclass field is required to be invariant when
  upstream does not require it. Narrow, but wrong, and landed.
- `dataclasses_entries.go` — `AddInheritedDataClassEntries`, which consequently
  always returns an empty list.

`NamedTupleEntries` is the reason `_validateInstanceVariableInitialization` is
parked under `wip/`.

So this is not one more validator on a list. Until it lands, several already-
ported checks are quietly answering from empty data, and any new check touching
dataclasses will inherit the same defect. It is the highest-value remaining item
by a wide margin, and it should be done before `_validateBaseClassOverride`,
which reads `dataClassEntries` too and would otherwise be built on the same
hole.

The general form of this, which the earlier field-write rule only half caught:
**an unwritten field does not fail loudly at its reader — it fails at every
reader at once, including ones already reviewed and committed.** Auditing
`grep -c '\.Field = '` across the whole `Shared`/`Priv` surface is worth doing
once, rather than per-validator.

### The whole-surface audit, run

Sweeping every `Shared`/`Priv` field for "read somewhere, assigned nowhere"
returned five candidates. Two are false positives worth naming so the check is
not re-run naively: `LiteralInstances` and `LiteralClasses` *are* populated in
`UnionTypeAddType`, but through a pointer alias --

```go
literalMaps := &unionType.Priv.LiteralInstances
literalMaps.LiteralStrMap = ...
```

-- so a `grep '\.Field ='` never sees the field name on the left of the
assignment. Any such audit under-reports writes through an alias and
over-reports gaps; treat its output as candidates, not findings.

The three real gaps are `DataClassBehaviors`, `DataClassEntries` and
`NamedTupleEntries`, and all three are populated by the same unported file:

| field | written upstream by |
| --- | --- |
| `DataClassEntries` | `dataClasses.synthesizeDataClassMethods` |
| `NamedTupleEntries` | same, plus `namedTuples.createNamedTupleType` |
| `DataClassBehaviors` | `dataClasses.applyDataClassClassBehaviorOverrides` |

So `dataClasses.ts` is not merely the largest unported file -- it is the one
whose absence silently degrades the most already-landed code, across member
lookup, protocol matching, multiple-inheritance variance and dataclass entry
inheritance. Everything it feeds is currently answering from empty data without
reporting anything unusual.
