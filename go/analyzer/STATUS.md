# Stage A status — **complete**

Stage A is "type model and parse-tree utilities" from ANALYZER-PLAN.md. Every
file the plan lists for this stage is ported, and both of the stage's
verification gates are green. This file stays as the record of what was decided
and where the traps were; Stage B gets its own.

Reference sources: `$REF/analyzer/*.ts` at pyright 1.1.412, where `$REF` is
`packages/pyright-internal/src` extracted by `make ref`.

## Ported

| TypeScript | Go | notes |
| --- | --- | --- |
| `common/uri/uriInterface.ts`, `uri.ts` | `common/uri/uri.go` | interface + namespace helpers |
| `common/uri/baseUri.ts` | `common/uri/baseuri.go` | plus the two pathUtils helpers it needs |
| `common/uri/constantUri.ts` | `common/uri/constanturi.go` | |
| `common/uri/emptyUri.ts` | `common/uri/emptyuri.go` | |
| `common/core.ts` | `common/core.go` | the comparison helpers and `containsOnlyWhitespace` |
| `common/collectionUtils.ts` | `common/collectionutils.go` | only `appendArray`, `partition` |
| (none) | `common/orderedmap.go` | stands in for JS `Map`/`Set` |
| `analyzer/types.ts` | `analyzer/types*.go` | complete, split across 7 files |
| `analyzer/declaration.ts` | `analyzer/declaration.go` | complete |
| `analyzer/symbol.ts` | `analyzer/symbol.go` | complete |
| `analyzer/declarationUtils.ts` | `analyzer/declarationutils.go`, `declarationutils_resolve.go` | complete |
| `analyzer/symbolNameUtils.ts` | `analyzer/symbolnameutils.go` | complete |
| `analyzer/symbolUtils.ts` | `analyzer/symbolutils.go` | complete |
| `analyzer/scope.ts` | `analyzer/scope.go` | complete |
| `analyzer/scopeUtils.ts` | `analyzer/scopeutils.go` | complete |
| `analyzer/importResult.ts` | `analyzer/importresult.go` | complete (plus `PyTypedInfo`) |
| `analyzer/constraintSolution.ts` | `analyzer/constraintsolution.go` | complete |
| `analyzer/typeComplexity.ts` | `analyzer/typecomplexity.go` | complete |
| `analyzer/typeCacheUtils.ts` | `analyzer/typecacheutils.go` | complete |
| `analyzer/typeWalker.ts` | `analyzer/typewalker.go` | complete |
| `analyzer/codeFlowTypes.ts` | `analyzer/codeflowtypes.go` | complete |
| (new) | `parser/jsnumber.go` `NumberValue.String` | ECMA-262 `Number::toString`, pinned against node |
| `analyzer/analyzerNodeInfo.ts` | `analyzer/analyzernodeinfo.go` | complete |
| `analyzer/analyzerFileInfo.ts` | `analyzer/analyzerfileinfo.go` | complete, plus `IPythonMode` and `ExecutionEnvironment` |
| `analyzer/typeUtils.ts` | `analyzer/typeutils*.go` | complete -- all 106 exported functions, split across 14 files |
| `analyzer/typePrinterUtils.ts` | `analyzer/typeprinterutils.go` | complete, with tests pinned against node |
| `analyzer/parameterUtils.ts` | `analyzer/parameterutils.go` | complete |
| `analyzer/parseTreeWalker.ts` | `analyzer/parsetreewalker.go` | complete, **generated** by `analyzer/gen/generate_parsetreewalker.py` |
| `analyzer/parseTreeUtils.ts` | `analyzer/parsetreeutils_*.go` | complete, split across `_print`, `_nav`, `_match`, `_tokens`, `_misc` |
| `analyzer/typePrinter.ts` | `analyzer/typeprinter*.go` | complete -- `typeprinter.go` (flags, entry points, literal helpers), `typeprinter_print.go` (the six recursive printers), `typeprinter_names.go` (`UniqueNameMap`, name helpers) |
| `common/configOptions.ts` | `analyzer/configoptions.go` | **partial** -- `DiagnosticLevel` + all 96 `DiagnosticRuleSet` fields only; the `ConfigOptions` class itself is Stage C |

`types.ts` is split as: `types.go` (TypeBase, clone helpers, literals),
`types_simple.go` (Unbound/Unknown/Module), `types_class.go`,
`types_function.go`, `types_typevar.go`, `types_union.go`
(Overloaded/Never/Any/TypeCondition/Union/Variance), `types_guards.go`,
`types_same.go` (isTypeSame, combineTypes).

## Deliberately deferred

- `pyTypedUtils.ts` -- not on the plan's Stage A list. `getPyTypedInfo` and
  `getPyTypedInfoForPyTypedFile` read the filesystem through `FileSystem` and
  `uriUtils`, neither of which exists yet; they land with the import resolver in
  Stage C. `PyTypedInfo` itself is already in `importresult.go`.
- The `ConfigOptions` class. `getPrintTypeFlags` takes one, so a struct with the
  fields it reads is in `configoptions.go`; the rest is Stage C.

## Verification

Both gates are green.

### `typePrinter.test.ts`, unmodified, against the Go implementation

```
make bridge-typeprinter-tests REF=<path to pyright-internal/src>
6 passed, 0 failed, 0 skipped, 6 total
```

This bridge has a different shape from the tokenizer and parser ones. Those
alias one module and ship data over the wire. This one aliases `analyzer/types`
and `analyzer/typePrinter` (see `tools/ts-bridge/shim-types.ts`) and ships *the
test's construction calls*: `client.ts` starts a fresh Go process per request,
so there is nowhere to keep objects between calls. The shim records
`ClassType.specialize`, `FunctionType.addParam`,
`x.shared.declaredReturnType = y` and so on into a log, and `printType` sends
the whole log; `cmd/tokenserver/typebridge.go` replays it against the Go type
model and prints. Handles are Proxies, so the property mutations the test
performs are recorded too.

The consequence is that the gate exercises the Go `types.ts` port as well as the
Go `typePrinter.ts` port. Verified by hand: changing `"Unbound"` to
`"UnboundXX"` in `typeprinter_print.go` turns `SimpleTypes` red.

One documented deviation, guarded at runtime: the test's `returnTypeCallback` is
a TypeScript closure and the protocol is unidirectional, so it is reimplemented
in `typebridge.go`. `shim-typePrinter.ts` inspects the callback source and throws
if a future test passes a different one.

### `parseTreeUtils` corpus differential

```
make bridge-parsetreeutils REF=<path to pyright-internal/src>
1343 identical, 0 different, 1343 total
```

`parseTreeUtils.test.ts` cannot be bridged -- it drives the fourslash harness,
which needs the binder and the import resolver. So instead
`tools/ts-bridge/compare-parsetreeutils.js` walks every node of every corpus
file and evaluates every parseTreeUtils function that does not need a bound
scope, in both implementations, and diffs the results. Nodes are keyed by
pre-order index because node ids are per-process counters.

Not covered, because they call `getScope` or `getFileInfo`:
`getEvaluationScopeNode`, `getExecutionScopeNode`,
`getEnclosingFunctionEvaluationScope`, `getEvaluationNodeForAssignmentExpression`,
`getScopeIdForNode`, `getTypeVarScopesForNode`, `getFileInfoFromNode`. Those
become testable in Stage B, once the binder can produce scopes.

This differential earned its keep immediately: see UPSTREAM-BUGS.md #12.

## Traps found here, worth carrying into Stage B

- **`undefined === undefined` is true.** Five parseTreeUtils walks compare
  `prevNode` (undefined on the first iteration) against an optional child. When
  the child is also absent the comparison succeeds and the walk returns early.
  Writing the obvious Go nil guard silently changes behavior. The `sameNode`
  helper in `parsetreeutils_match.go` reproduces it. UPSTREAM-BUGS #12.
- **A nil `*XNode` in a `parser.ParseNode` is not nil.** The generated
  `getChildNodes` originally emitted typed nils, so every `child != nil` check
  in the port was silently true and the first corpus run segfaulted. Fixed at
  the generator with `childOrNil`; it is the same hazard anywhere a concrete
  node pointer is widened to the interface.
- **Empty JavaScript arrays are truthy.** `if (x.tupleTypeArgs)` is taken for
  `[]`, so those transliterate to `!= nil`, never `len(x) > 0`. Marked at each
  site in `typeprinter_print.go`.
- **JS `Map`/`Set` iteration order reaches printed output.** `printUnionType`
  drains three `Set<string>`s into arrays and joins them; a Go map would
  randomize the printed union and look like a logic bug. `common.OrderedSet`.

## Representation decisions already made

- `Type` is a Go interface; each category is a struct embedding `TypeBase`.
- `Shared` is a pointer (aliased by clones, replaceable by `cloneWithNewFlags`);
  `Priv` is a value, so a struct copy gives the shallow copy `{...type.priv}`
  produces; `Props` is a pointer so nil means absent.
- Every type value is a pointer, because reference identity is meaningful.
- Narrowing guards appear twice: `IsX` returns bool, `AsX` returns `(*X, bool)`.
- Namespaced TypeScript functions (`ClassType.isBuiltIn`) become prefixed free
  functions (`ClassTypeIsBuiltIn`).
- TypeScript optional parameters with defaults become required Go parameters;
  the default is recorded in the doc comment.
- `TypeWalker`, `TypeVarTransformer`, `ParseTreeWalker` and `BaseUri`
  subclassing uses a `self` pointer for virtual dispatch.
- TypeScript generators become `iter.Seq`, so laziness and early `break` are
  preserved.
