# Stage B status — **in progress**

Stage B is "the binder" from ANALYZER-PLAN.md: `binder.ts` (4,912 lines) plus
`importStatementUtils.ts`, `staticExpressions.ts`, `cellChainIndex.ts`,
`importResult.ts` and `commentUtils.ts`.

Everything except `binder.ts` itself is ported, and the differential harness the
plan calls for is built and validated against the real TypeScript binder.
`binder.ts` is not started.

Reference sources: `$REF/analyzer/*.ts` at pyright 1.1.412, where `$REF` is
`packages/pyright-internal/src` extracted by `make ref`.

## Ported

| TypeScript | Go | notes |
| --- | --- | --- |
| `analyzer/staticExpressions.ts` | `analyzer/staticexpressions.go` | complete |
| `analyzer/commentUtils.ts` | `analyzer/commentutils.go` | complete |
| `common/configOptions.ts` | `analyzer/configoptions_gen.go` | `DiagnosticRule`, `DiagnosticRuleSet`, the four preset rule sets and the rule lists -- **generated** by `analyzer/gen/generate_configoptions.py` |
| `analyzer/cellChainIndex.ts` | `analyzer/cellchainindex.go` | **partial** -- the `CellChainIndexProvider` interface only |
| `analyzer/importStatementUtils.ts` | `analyzer/importstatementutils.go` | **partial** -- `getWildcardImportNames` only |
| `analyzer/importResult.ts` | `analyzer/importresult.go` | complete (landed in Stage A) |
| `analyzer/binder.ts` | — | **not started** |

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

## The differential harness

Built, and validated against the unmodified TypeScript binder before writing a
line of the Go one — which is what ANALYZER-PLAN.md asks for, because a single
wrong code-flow edge produces no visible symptom until some narrowing test tens
of thousands of lines later fails for reasons nobody can trace back.

```
make bridge-binder-oracle REF=<path to pyright-internal/src>

=== imports resolve: 1343 files
oracle produced 11106 scopes, 56604 symbols, 58200 declarations, 63387 flow nodes; 0 files failed to bind

=== imports unresolved: 1343 files
oracle produced 11106 scopes, 56604 symbols, 58200 declarations, 63387 flow nodes; 0 files failed to bind
```

`make bridge-binder` runs the same thing against the Go port and diffs. It will
fail until `cmd/tokenserver/binder.go` exists.

`binder.ts` has no bridgeable test of its own — the tests that exercise it drive
the fourslash harness — so as with `parseTreeUtils`, a corpus differential
stands in. It covers, per file:

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

### What the harness cannot cover yet

The import resolver is Stage C, and `visitModuleName` asserts that every
`ModuleName` node carries an `ImportResult`, so the harness synthesizes one —
identically on both sides — and runs the corpus twice: once where every import
resolves and once where none does. That covers both families of branches through
`visitModuleName`, `visitImportAs` and `visitImportFromAs` (51 bind diagnostics
in the unresolved mode versus 4 in the resolved one, over the first 40 files).

What it cannot reach until Stage C is anything that depends on the *content* of
a resolved import: implicit submodule imports, py.typed detection, the
missing-stub diagnostic's namespace-package suppression, and wildcard imports
(which call `importLookup`).

## Known deviation

`staticExpressions.convertTupleToVersion` truncates non-integer version
components. `PythonVersion`'s fields are Go `int`s; the original stores whatever
`d.value` holds, and it never checks `d.isInteger`, so `sys.version_info >= (3,
12.5)` compares against minor `12.5` there and minor `12` here. The two disagree
only when the execution environment's minor version equals the truncated value.
Nothing in the corpus writes such a comparison, and the differential would find
it if something did.
