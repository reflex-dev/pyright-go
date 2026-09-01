# Stage B status — **complete**

Stage B is "the binder" from ANALYZER-PLAN.md: `binder.ts` (4,912 lines) plus
`importStatementUtils.ts`, `staticExpressions.ts`, `cellChainIndex.ts`,
`importResult.ts` and `commentUtils.ts`.

Everything is ported and the differential gate is green over 4,190 file-runs.

Reference sources: `$REF/analyzer/*.ts` at pyright 1.1.412, where `$REF` is
`packages/pyright-internal/src` extracted by `make ref`.

## Ported

| TypeScript | Go | notes |
| --- | --- | --- |
| `analyzer/binder.ts` | `analyzer/binder*.go` | complete -- all 43 visit methods and all 68 private helpers, split across 8 files |
| `analyzer/staticExpressions.ts` | `analyzer/staticexpressions.go` | complete |
| `analyzer/commentUtils.ts` | `analyzer/commentutils.go` | complete |
| `common/configOptions.ts` | `analyzer/configoptions_gen.go` | `DiagnosticRule`, `DiagnosticRuleSet`, the four preset rule sets and the rule lists -- **generated** by `analyzer/gen/generate_configoptions.py` |
| `analyzer/cellChainIndex.ts` | `analyzer/cellchainindex.go` | **partial** -- the `CellChainIndexProvider` interface only |
| `analyzer/importStatementUtils.ts` | `analyzer/importstatementutils.go` | **partial** -- `getWildcardImportNames` only |
| `analyzer/importResult.ts` | `analyzer/importresult.go` | complete (landed in Stage A) |
| `common/pathUtils.ts` | `common/uri/baseuri.go` | `stripFileExtension` / `getFileExtension`, which `visitImportFrom` needs |

`binder.ts` is split as: `binder.go` (state, constructor, `BindModule`, the
deferred queue, diagnostics, and the three trailing walkers `YieldFinder`,
`ReturnFinder`, `DummyScopeGenerator`), `binder_flow.go` (flow-node
constructors, conditional binding, `_isNarrowingExpression`),
`binder_scopes.go` (name binding, scope creation, the cell-chain lookup),
`binder_visit_scopes.go` (class / function / lambda / type-param list / type
alias / call / module name), `binder_visit_stmts.go`, `binder_visit_exprs.go`,
`binder_imports.go`, `binder_decls.go`.

### Why configOptions is generated

`DiagnosticRuleSet` has 96 fields and there are four preset rule sets, so the
presets alone are nearly 400 hand-transcribed values. A single mistyped
`'warning'` would produce a diagnostic-count difference thousands of lines
downstream with no way to trace it back.

The generator also solves a second problem. `commentUtils.ts` reaches into a
rule set by name — `(ruleSet as any)[ruleName]` — which Go cannot do without
reflection. `configoptions_gen.go` emits a name → field accessor map for
exactly the fields whose names appear in the `DiagnosticRule` enum, and
`configoptions_test.go` asserts that those maps hold exactly the rules
`getBooleanDiagnosticRules()` and `getDiagLevelDiagnosticRules()` list. That
makes the substitution provably equivalent rather than approximately so.

### Deliberately deferred

- The rest of `importStatementUtils.ts` — `getTopLevelImports`,
  `getTextEditsForAutoImportInsertion`, `getRelativeModuleName` and the other
  auto-import edit machinery. It exists for the language server, which
  ANALYZER-PLAN.md puts out of scope, and it depends on `ConfigOptions`,
  `ReadOnlyFileSystem` and `importResolver` besides.
- The `CellChainIndex` class. It walks `SourceFileInfo` chains, which arrive
  with the program in Stage C. The binder only consumes the provider interface.

## Verification

```
make bridge-binder REF=<path to pyright-internal/src>
=== imports resolve: 1343 files      1343 identical, 0 different
=== imports unresolved: 1343 files   1343 identical, 0 different

make bridge-binder-typeshed REF=<path to pyright-internal/src>
=== imports resolve: 752 files       752 identical, 0 different
=== imports unresolved: 752 files    752 identical, 0 different
```

`binder.ts` has no bridgeable test — the tests that exercise it drive the
fourslash harness — so, as with `parseTreeUtils`, a corpus differential stands
in. It was built and validated against the unmodified TypeScript binder *before*
the Go binder was written, which is what ANALYZER-PLAN.md asks for.

It covers, per file:

- every parse node's scope, flow node, after-flow node, declaration,
  reachability, code-flow expression set and code-flow complexity;
- the whole scope tree: type, parent, proxy, symbol table (names, flags, every
  declaration in full), `notLocalBindings`, `slotsNames`, `hasNonEmptySlots`,
  `hasPotentiallyDynamicSymbolTable`;
- the whole code-flow graph: flags, antecedents, affected expressions, and every
  subtype's extra fields;
- the `__all__` info and every bind-time diagnostic (message, range, rule,
  actions).

Three things are renumbered because all three are per-process counters that
would never line up between implementations: parse nodes by pre-order index (as
in the parseTreeUtils differential), flow nodes and symbols by first-sight order
in a fixed traversal. The renumbering is traversal-order dependent rather than
id-order dependent, so a graph that differs in *shape* produces a difference
while one that merely allocated ids in a different interleaving does not.

Verified not to be vacuous: dropping `SymbolFlags.ClassMember` from the one
`addSymbol` call in `_bindNameValueToScope` turns all 40 of the first 20
file-runs red, on `.scopes[N].symbols[M].flags`.

### What the differential cannot cover yet

The import resolver is Stage C, and `visitModuleName` asserts that every
`ModuleName` node carries an `ImportResult`, so the harness synthesizes one —
identically on both sides — and runs each corpus twice: once where every import
resolves and once where none does. That covers both families of branches through
`visitModuleName`, `visitImportAs` and `visitImportFromAs`.

What it cannot reach until Stage C is anything that depends on the *content* of
a resolved import: implicit submodule imports, py.typed detection, the
missing-stub diagnostic's namespace-package suppression, and wildcard imports
(which call `importLookup` and get `undefined` here).

## Traps found here, worth carrying into Stage C

- **`array[array.length - 1]` on an empty array.** JavaScript answers
  `undefined`; Go panics. `visitImportFrom` does exactly this, and an empty
  `resolvedUris` is reachable — `from . import x` has no name parts. This
  crashed the first full-corpus run after 300 files had already passed.
- **`_finishFlowLabel`'s collapse test is on the whole flags value**, not a bit,
  so a context-manager label (which carries `PostContextManager | BranchLabel`)
  never collapses. Turning it into a bit test is the obvious "cleanup" and is
  wrong — though as it happens no context-manager label is ever passed to it, so
  the differential does not catch this one. It is written as the original writes
  it.
- **Flags versus types when dumping.** The harness's TypeScript side keys
  `preBranch` off the `BranchLabel` *flag*; the Go side originally keyed it off
  the node's Go type, which a context-manager label does not have. That is a
  harness bug rather than a port bug, but it is the same confusion in the other
  direction.
- **`if (this._currentExceptTargets)` is an always-true array truthiness test.**
  Dropping the guard is correct; turning it into a length check is not.
- **`_dunderAllNames` and `_dunderSlotsEntries` each need a companion bool.**
  The original distinguishes "never seen" (`undefined`) from "seen and empty"
  (`[]`, which is truthy), and a nil Go slice cannot.
- **Go closures capture the variable, not the value.** Every callback helper in
  the binder saves a field, calls back, and restores it; each is written as an
  explicit save/restore rather than a `defer`, because `_bindNeverCondition`
  restores conditionally.

## Known deviation

`staticExpressions.convertTupleToVersion` truncates non-integer version
components. `PythonVersion`'s fields are Go `int`s; the original stores whatever
`d.value` holds, and it never checks `d.isInteger`, so `sys.version_info >= (3,
12.5)` compares against minor `12.5` there and minor `12` here. The two disagree
only when the execution environment's minor version equals the truncated value.
Nothing in either corpus writes such a comparison, and the differential would
find it if something did.
