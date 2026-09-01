# Stage C status

Stage C is "import resolver, program, filesystem" from ANALYZER-PLAN.md.

The import resolver half is done and its test is green. The program half --
`sourceFile.ts`, `sourceFileInfo.ts`, `program.ts`, `service.ts` -- is not
started; see "Remaining" at the bottom.

Reference sources: `$REF/**.ts` at pyright 1.1.412, where `$REF` is
`packages/pyright-internal/src` extracted by `make ref`.

## Ported

| TypeScript | Go | notes |
| --- | --- | --- |
| `common/pathUtils.ts` | `common/pathutils.go` | complete, including the three Node `path` primitives it leans on |
| `common/uri/fileUri.ts` | `common/uri/fileuri.go` | complete |
| `common/uri/webUri.ts` | `common/uri/weburi.go` | complete |
| `common/uri/uri.ts` | `common/uri/urifactory.go` + `uri.go` | complete |
| `common/uri/memoization.ts` | `common/uri/memoization.go` | complete |
| `common/uri/uriMap.ts` | `common/uri/urimap.go` | the eight methods with callers |
| `common/uri/uriUtils.ts` | `common/uri/uriutils.go`, `uriutils_filesystem.go` | complete |
| `vscode-uri` (v3.1.0) | `common/uri/vscodeuri.go` | the parts pyright's Uri classes reach |
| `common/fileSystem.ts` | `common/uri/filesystem.go` | see "Layout" |
| `common/caseSensitivityDetector.ts` | `common/casesensitivitydetector.go` | complete |
| `common/console.ts` | `common/console.go` | **partial** -- the interface and the two implementations the analyzer reaches |
| `common/pathConsts.ts` | `common/pathconsts.go` | complete |
| `common/configOptions.ts` | `analyzer/configoptions_class.go` | **partial** -- see "Deliberately deferred" |
| `common/host.ts` | `analyzer/host.go` | **partial** -- the three synchronous queries |
| `analyzer/importLogger.ts` | `analyzer/importlogger.go` | complete |
| `analyzer/pythonPathUtils.ts` | `analyzer/pythonpathutils.go` | complete |
| `analyzer/pyTypedUtils.ts` | `analyzer/pytypedutils.go` | complete |
| `analyzer/importResolverTypes.ts` | `analyzer/importresolvertypes.go` | complete |
| `analyzer/importResolverFileSystem.ts` | `analyzer/importresolverfilesystem.go` | complete |
| `analyzer/typeshedInfoProvider.ts` | `analyzer/typeshedinfoprovider.go` | complete |
| `analyzer/parentDirectoryCache.ts` | `analyzer/parentdirectorycache.go` | complete |
| `analyzer/circularDependency.ts` | `analyzer/circulardependency.go` | complete |
| `analyzer/importResolver.ts` | `analyzer/importresolver*.go` | complete -- 5 files |
| `readonlyAugmentedFileSystem.ts` | `analyzer/readonlyaugmentedfilesystem.go` | complete |
| `pyrightFileSystem.ts` | `analyzer/pyrightfilesystem.go` | complete |
| `partialStubService.ts` | `analyzer/partialstubservice.go` | complete |
| *(not pyright)* | `vfs/vfs.go` | an in-memory file system for the bridge |

`importResolver.ts` is split as: `importresolver.go` (types, constructor, public
entry points, module-level helpers), `importresolver_resolve.go` (the PEP-420
algorithm), `importresolver_typeshed.go`, `importresolver_modulename.go` (the
inverse mapping from a path back to a module name) and
`importresolver_completions.go`.

### Layout

Two files move, both for the reason ANALYZER-PLAN.md gives for the analyzer
being one Go package.

`common/fileSystem.ts` lands in `common/uri`: fileSystem.ts imports uri/uri.ts
and uri/uriUtils.ts imports fileSystem.ts, a cycle TypeScript does not mind and
Go forbids between packages.

`common/host.ts`, `common/configOptions.ts`, `readonlyAugmentedFileSystem.ts`,
`pyrightFileSystem.ts` and `partialStubService.ts` land in `analyzer`, each
because it references something that is already there.

### The DI plumbing

`ImportResolver`'s constructor takes a `ServiceProvider` and pulls five things
out of it. ANALYZER-PLAN.md says this plumbing should become small interfaces at
the point of consumption, so the constructor takes them directly: the file
system, a console, the partial-stub service, and the two optional cache
overrides. `tmp` is dropped -- nothing in `ImportResolver` reads it.

The five `protected` methods documented as subclass extension points become a
struct of hook functions, where nil is the base class's behaviour. In every case
that behaviour is "return undefined".

## Verification

```
make bridge-importresolver-tests REF=<path to pyright-internal/src>
34 passed, 0 failed, 0 skipped, 34 total

make bridge-pathutils-tests   63 passed, 0 failed
make bridge-uri-tests         95 passed, 0 failed
```

`importResolver.test.ts` runs unmodified. It reads three things and returns a
fourth, and the bridge ships exactly those; see
`tools/ts-bridge/shim-importResolver.ts` for the protocol.

The one place it needed more than a request and a response is partial stubs.
`ensurePartialStubPackages` merges a partial stub package onto the library it
augments by mapping directories *in the file system it was given*, and one test
reads that back: after resolving `myLib.partialStub` it expects reading
`myLib/partialStub.pyi` to answer the contents of `myLib-stubs/partialStub.pyi`.
Because the Go resolver works on a snapshot, the mappings it installed are
reported back and replayed on the TypeScript side with `mapDirectory`. What
crosses is the *decision* -- which stub directory merges onto which package,
which is the whole job of `processPartialStubPackages`.

### Checked for vacuity

Fifteen probes. Ten turn a gate red:

| probe | effect |
| --- | --- |
| `getRootLength`'s DOS branch | pathUtils 1 failed |
| `'?'` widened to zero-or-more in the wildcard generator | pathUtils 1 failed |
| dropping the reserved-character escaping | pathUtils 2 failed |
| percent-encoding case (`%3A` → `%3a`) | uri 1 failed |
| `FileUri.startsWith`'s trailing-separator guard | uri 2 failed |
| `uriToFsPath`'s drive-letter lowering | uri 2 failed |
| `uriUtils.getWildcardRoot` | uri 2 failed |
| `deduplicateFolders`' replacement rule | uri 1 failed |
| the bridge's `Uri.constant` interning | uri 1 failed |
| `Utils.resolvePath`'s leading slash | uri 4 failed |
| the `-stubs` suffix in `_resolveAbsoluteImport` | resolver 1 failed |
| `__init__.pyi` preferred over `__init__.py` | resolver 8 failed |
| the `isImportFound` arity test | resolver 22 failed |
| the partial-stub `py.typed` test | resolver 7 failed |
| the vfs following symlinks in `stat` | resolver 2 failed |

Five do not, and each is an honest gap rather than a harness defect:

- **`WebUri.startsWith`'s trailing-separator guard.** Every `startsWith` case in
  uri.test.ts is a file URI.
- **`cacheStaticFunc`.** Interning changes object identity and eviction order,
  never an answer, so no test can see it.
- **greedy versus lazy in the wildcard `**` fragment.** `regex.test()` only asks
  whether a match exists, not which one.
- **`typing_extensions` as a special case in `_findTypeshedPath`.** No test in
  importResolver.test.ts imports it.
- **`_pickBestImport`'s "prefer pyi over py" rule, and the `-stubs` stripping in
  `_getModuleNameInfoFromPath`.** Neither is reached by any of the 34 cases.

## Deliberately deferred

- **`initializeFromJson` and `setupExecutionEnvironments`** -- 550 lines of
  `pyrightconfig.json` and `pyproject.toml` reading. Their only caller is
  `service.ts` and their test is `config.test.ts`; both land with the service.
  Nothing the import resolver does goes through them.
- **`realFileSystem.ts`** -- ANALYZER-PLAN.md names it explicitly as the one
  place deliberate divergence is right. `Uri` semantics are ported faithfully;
  the chokidar watchers, the zip reader and the temp-file machinery are not.
- **The async and streaming members of `FileSystem`**, the file watcher, and
  `createReadStream`/`createWriteStream`. `mapDirectory` is *not* deferred: it
  looks like plumbing but is how partial stubs work.
- **The three interpreter-spawning members of `Host`** -- `runScript`,
  `runSnippet` and `spawnProcess`. They are asynchronous and cancellable, and
  their callers are the language server and the stub generator.

## Traps found here

- **`Uri` cannot be a plain dispatch shim.** It is an object with ~40 methods
  that flows into other TypeScript code, so a bridged Uri is a *recipe*: how it
  was built plus the derivations applied since. Deriving costs no round trip;
  only reading a scalar sends the recipe to Go, which replays it. This works
  because Uris are immutable and every method is a pure function of the Uri.
- **`Uri.constant` compares by reference.** Two constants with the same name are
  unequal and a constant equals itself, so replaying a recipe twice would break
  both halves. Constants are interned by a serial number the shim assigns.
- **`cacheStaticFunc` is not an optimization.** It interns Uris, so two calls
  with the same arguments answer the same object and the per-instance
  `@cacheProperty` caches are shared. Several comparisons in the resolver are
  written as `!==` on Uris and rely on it.
- **JavaScript's `\w` is ASCII-only.** The wildcard regex generator escapes
  anything outside `\w` and `\s`, so a path component such as "日本" arrives as
  `\日\本` -- a no-op escape in JavaScript and a syntax error in RE2. The
  generator stays byte-faithful and `CompileWildcardRegexPattern` translates.
- **`CircularDependency.normalizeOrder` compares two Uri objects with `<`.**
  JavaScript coerces each side to a primitive first, so it is comparing
  `toString()`, not the file paths and not the keys.
- **`ParentDirectoryCache`'s `ImportPath` is a box around `Uri | undefined`, and
  the box is load-bearing.** `checked()` records that a directory was searched
  whether or not anything was found, so a bare nil could not tell "searched,
  found nothing" from "never searched".
- **`typeshedInfoProvider`'s root and subdirectory caches read `!== undefined`**,
  so a cached *absence* is indistinguishable from a miss and gets recomputed
  every time. A nil Uri has the same property, so the behaviour carries over --
  but "fixing" it would change how often the file system is touched.
- **`getSourceFilesFromStub` iterates the import-result cache and appends to an
  array the caller sees**, so that cache is an ordered map, not a Go map.
- **The completion-suggestion map is keyed by name, not by Uri.** Two different
  namespace packages both map to `Uri.empty()`.

## Remaining in Stage C

`sourceFile.ts` (1,607), `sourceFileInfo.ts` (258), `program.ts` (2,334) and
`service.ts` (1,968), plus `initializeFromJson`.

All four reach `typeEvaluator` and `checker`, which are Stage D, so they can
only land with the check phase stubbed -- which is exactly the "skeleton first,
then fatten" sequencing ANALYZER-PLAN.md asks for: stand up the program loop
with a deliberately incomplete evaluator so pyright's real test harness runs end
to end early.

**They have no gate before that point**, which is why they are not here. Every
stage so far landed against an oracle, and each of these four has one only once
the evaluator exists:

- `sourceFile.test.ts` -- two of its four tests need `RealFileSystem` and
  `FullAccessHost` (both out of scope), and the other two drive the fourslash
  harness through `service.test_program.analyze()`, i.e. the checker.
- `config.test.ts` -- constructs an `AnalyzerService`, so it needs all four
  files *and* the evaluator underneath them.
- `service.test.ts`, `ipythonMode.test.ts` -- likewise.

So the right order is to port them together with the first cut of the
evaluator, at which point `config.test.ts` and `sourceFile.test.ts` become
gates and the `expected_text` scoreboard ANALYZER-PLAN.md describes starts
counting. Landing 6,000 lines before then would be the first unverified step in
the port.
