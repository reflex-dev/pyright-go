# Stage C status

Stage C is "import resolver, program, filesystem" from ANALYZER-PLAN.md. Both
halves are done: the import resolver, and the program loop from `sourceFile.ts`
up through `service.ts`.

The program loop stands up with the check phase stubbed, which is the
"skeleton first, then fatten" sequencing ANALYZER-PLAN.md asks for. What runs
today: parse, bind, resolve imports, walk the import graph, detect cycles, and
report parse and bind diagnostics. What does not: type evaluation and checking,
which are Stage D. See "The Stage D seams".

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
| `common/configOptions.ts` | `analyzer/configoptions_class.go`, `configoptions_json.go` | complete, including `initializeFromJson` and `setupExecutionEnvironments` |
| `common/commandLineOptions.ts` | `analyzer/commandlineoptions.go` | complete |
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
| `analyzer/parseTreeCleaner.ts` | `analyzer/parsetreecleaner.go` | complete |
| `common/logTracker.ts` | `analyzer/logtracker.go` | complete |
| `analyzer/cacheManager.ts` | `analyzer/cachemanager.go` | **partial** -- the registry and the free-pass; the heap-usage watchdog needs the evaluator's caches |
| `analyzer/sourceFile.ts` | `analyzer/sourcefile.go`, `sourcefile_diagnostics.go` | **partial** -- everything but `check()`; see "The Stage D seams" |
| `analyzer/sourceFileInfo.ts` | `analyzer/sourcefileinfo.go` | complete |
| `analyzer/program.ts` | `analyzer/program.go`, `program_analysis.go` | **partial** -- see "The Stage D seams" |
| `analyzer/service.ts` | `analyzer/service.go`, `service_config.go` | **partial** -- the config path and the program loop; not the watchers or the background thread |
| `analyzer/sourceEnumerator.ts` | `analyzer/sourceenumerator.go` | complete |
| *(not pyright)* | `vfs/vfs.go` | an in-memory file system for the bridge |
| `common/realFileSystem.ts` | `realfs/realfs.go` | the sanctioned divergence -- see "Deliberately deferred" |

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

### The Stage D seams

`sourceFile.ts` and `program.ts` reach the type evaluator and the checker, both
of which are Stage D. Rather than stub them with something that would have to be
torn out, each is a seam that Stage D fills in:

- **`TypeEvaluator` is an opaque `any`.** Nothing in Stage C calls a method on
  it; it is only stored, passed along and handed to the checker. So the port
  carries it as an untyped value and Stage D replaces the type without touching
  a call site.
- **`Checker` is an interface, supplied by a `CheckerFactory`.** `sourceFile.check()`
  needs a checker, and the only thing the program loop does with one is construct
  it and call `check()`. With a nil factory the check phase is skipped and the
  file is marked checked -- so the loop terminates and reports the diagnostics
  the binder produced.

The consequence: with nil factories the program parses, binds, resolves imports,
detects circular dependencies and reports parse and bind diagnostics. That is
enough to be exercised end to end, which is what the config differential below
does, and it is not enough to type-check anything.

## Verification

```
make bridge-importresolver-tests REF=<path to pyright-internal/src>
34 passed, 0 failed, 0 skipped, 34 total

make bridge-pathutils-tests   63 passed, 0 failed
make bridge-uri-tests         95 passed, 0 failed

make bridge-config
78 identical, 0 different, 78 total
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

### The config differential

`config.test.ts` is not bridgeable. It constructs `ExecutionEnvironment`s in
TypeScript, mutates `ConfigOptions` and `CommandLineOptions` in place, and
asserts on object *identity* --

```ts
assert.strictEqual(configOptions.findExecEnvironment(file1), execEnv1);
```

-- none of which survives a stateless per-call bridge the way an immutable `Uri`
does. So a differential stands in for it, the same way one stands in for
parseTreeUtils.test.ts and for the binder. It is broader rather than narrower:
every project directory under `tests/samples`, in both command-line and
language-server mode -- 39 × 2 = 78 runs -- rather than the twenty or so the
test names. Each run diffs the *whole* resulting `ConfigOptions`: every scalar,
every file spec's compiled regular expression, the 96-field diagnostic rule set,
every execution environment, and the list of files the source enumerator finds.

Both modes are run because they take different branches through
`_getConfigOptions`: the command line walks up the directory tree looking for a
config file and applies its own overrides afterwards, and a language server does
neither.

Two defects in the *oracle* had to be fixed before it could be trusted, which is
why it was validated on its own (`--ts-only`) first:

- `PythonVersion` is a plain object in 1.1.412, not a class, so `v.toString()`
  yields `"[object Object]"`. It needs `PythonVersion.toString(v)`.
- `tomlUtils` loads `smol-toml` with a dynamic import and exposes a promise for
  it. Without awaiting that, `_parsePyprojectTomlFile` throws "TOML module not
  loaded" on every attempt and the service silently behaves as though no
  `pyproject.toml` existed -- so every TOML fixture would have been compared
  against a config the real pyright never produces.

One rendering difference is normalized rather than imitated:
`RegExp.prototype.source` escapes every forward slash so the result can be
pasted back into a regular-expression literal, which Go's `Regexp.String` does
not do. `new RegExp('a/b').source` is `'a\\/b'` and matches exactly what `'a/b'`
matches, so the oracle undoes it.

### Checked for vacuity

Twenty-two probes. Sixteen turn a gate red:

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
| `typeCheckingMode` applied from a config file | config 40 of 78 different |
| the three default excludes (`**/node_modules` &c.) | config 68 of 78 different |
| `autoExcludeVenv` in the source enumerator | config 6 of 78 different |
| `extends` processing base configs first | config 4 of 78 different |
| `pythonVersion` read from the config file | config 12 of 78 different |
| the `pyproject.toml` search up the directory tree | config 2 of 78 different |

Six do not, and each is an honest gap rather than a harness defect:

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
- **`ignore` tolerating absolute paths.** `getFileSpec` takes a flag saying
  whether a spec may be absolute, and `ignore` passes true. Turning it off
  changes nothing, because no fixture in the corpus puts an absolute path in
  `ignore` -- the corpus is real project directories, and an absolute path there
  would not be portable.

## Deliberately deferred

- **`realFileSystem.ts`** -- ANALYZER-PLAN.md names it explicitly as the one
  place deliberate divergence is right. `realfs` implements `uri.FileSystem` on
  top of `os`, faithfully as to `Uri` semantics; the chokidar watchers, the zip
  reader and the temp-file machinery are not ported.
- **The service's background analysis and file watching.** `AnalyzerService`
  schedules re-analysis on a timer, watches the project tree and the library
  tree, and can hand the program to a background thread. None of that is
  ported: analysis is driven synchronously. `shouldRunAnalysis: () => false` is
  what the tests pass anyway.
- **`CacheManager`'s heap watchdog.** The registry, the free-pass and the
  emptying of caches are ported; `getUsedHeapRatio` and the partial-cache
  eviction it drives read the evaluator's caches, which do not exist yet.
- **`sourceFile.check()`, and everything in `program.ts` downstream of it** --
  the checker seam described above.
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

## What Stage D unblocks

The three tests that cover the program loop end to end are still dark, because
each of them needs the evaluator:

- `sourceFile.test.ts` -- two of its four tests need `RealFileSystem` and
  `FullAccessHost` (both out of scope), and the other two drive the fourslash
  harness through `service.test_program.analyze()`, i.e. the checker.
- `service.test.ts`, `ipythonMode.test.ts` -- likewise.

The config differential covers what can be covered without the checker: config
discovery, the `extends` chain, `initializeFromJson`, `setupExecutionEnvironments`,
the command-line/config merge, `getFileSpec` and the source enumerator. It does
not reach `program.analyze()`, so the analysis loop itself -- the work list, the
import-graph walk, cycle detection and the diagnostic sinks -- is ported but
ungated. That is the first thing Stage D makes testable, and the
`expected_text` scoreboard ANALYZER-PLAN.md describes starts counting at the
same moment.

## Traps found in the program half

- **`AnalyzerService` reads the process's working directory** in one branch of
  config discovery, so the differential runs both sides from
  `packages/pyright-internal` -- jest's `rootDir`. This is not a port bug, but
  it is invisible until both sides disagree about where "here" is.
- **`configOptions.defineConstant` iterates in insertion order.** It is a
  `Map`, and the values reach `AnalyzerFileInfo` as an ordered structure that
  static-expression evaluation reads, so it is an ordered map in Go too.
- **The 96 diagnostic-rule-set fields are compared by name.** The Go struct
  names are exported and the TypeScript ones are not, so the bridge lowercases
  the first letter of each by reflection rather than maintaining a mapping that
  could silently drop a rule.
- **`_getConfigOptions` reverses the `extends` chain by unshifting.** Base
  configs are pushed onto the *front* of the array so they are applied first
  and the deriving config wins. Appending instead reverses precedence, which
  the vacuity probe confirms is observable on four of the 78 runs.
