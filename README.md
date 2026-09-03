# pyright, transliterated to Go

A transliteration of [pyright](https://github.com/microsoft/pyright) **1.1.412**
(git tag `1.1.412`) from TypeScript to Go, done bottom-up through the dependency
graph.

The goal is behavioral identity with the TypeScript sources, not a redesign.
Where the Go differs, the reason is written down at the point of difference.

The port lives in [`go/`](go/); everything else in the repository is the
unmodified pyright 1.1.412 tree it was transliterated from and is verified
against. [`go/PORTING.md`](go/PORTING.md) is the authoritative status
document; [`go/BENCHMARKS.md`](go/BENCHMARKS.md) has the performance
numbers and methodology; [`go/UPSTREAM-BUGS.md`](go/UPSTREAM-BUGS.md)
collects the pyright bugs found along the way.

## Performance

Measured on a ~3,150-file production codebase with its virtual environment
active (all third-party imports resolving), against pyright 1.1.412 under
Node, identical diagnostics in every row — full tables and methodology in
[`go/BENCHMARKS.md`](go/BENCHMARKS.md):

| invocation | pyright 1.1.412 | Go port |
| --- | --- | --- |
| full check, no cache | 59 s (`--threads`) | 163 s |
| `--cachedir`, nothing changed | n/a — pyright has no cache | **5.9 s / 0.6 GB** |
| `--cachedir`, typical (leaf) file changed | n/a | **6.8 s** |
| pre-commit file list (1,779 files), warm | 107 s every run | **5.8 s** |

The two implementations differ in shape, not just speed. Uncached, pyright
is faster on this heavily-interconnected project. The port's result is
`--cachedir`: a run-to-run cache keyed by each file's transitive dependency
closure, so unchanged files replay their diagnostics and a changed file
re-checks only its reverse import closure — while import resolution reruns
fresh every time, which is what keeps a new file shadowing a module from
serving stale results. The recurring invocation — CI on a mostly-unchanged
tree, a pre-commit hook — answers in seconds:

```bash
pyright-go --threads 4 --cachedir .pyright-cache
```

## How it is verified

Pyright's own TypeScript tests are run **against the Go implementation**, rather
than being transliterated into Go tests. `cmd/tokenserver` exposes the Go
tokenizer and parser over a JSON protocol; `tools/ts-bridge` swaps the modules
under test for shims that forward to it, then runs the unmodified test files.

`src/tests/tokenizer.test.ts`:

    91 passed, 0 failed, 0 skipped, 91 total

`src/tests/parser.test.ts`:

    23 passed, 0 failed, 4 skipped, 27 total

`src/tests/typePrinter.test.ts`, `src/tests/symbolNameUtils.test.ts` and
`src/tests/typeCacheUtils.test.ts`:

    6 passed, 0 failed, 0 skipped, 6 total
    7 passed, 0 failed, 0 skipped, 7 total
    2 passed, 0 failed, 0 skipped, 2 total

`src/tests/pathUtils.test.ts`, `src/tests/uri.test.ts` and
`src/tests/importResolver.test.ts`:

    63 passed, 0 failed, 0 skipped, 63 total
    95 passed, 0 failed, 0 skipped, 95 total
    34 passed, 0 failed, 0 skipped, 34 total

The 4 skipped cases drive the fourslash harness, which needs a live
in-process `Program` that a stateless bridge cannot provide. They are
reported as `SKIP` with a reason rather than being dropped or counted as
passing.

On top of that, corpus differentials run **both** implementations over every
file in `src/tests/samples` and diff the full output.

`compare-corpus.js` covers the tokenizer — every token field, the line ranges,
the `type: ignore` / `pyright: ignore` maps, and the derived predominant
line-ending/indent/quote statistics:

    1302 files compared, 0 mismatched

`compare-ast.js` covers the parser — every node type, every range, every flag,
every string, every numeric literal (compared as IEEE bit patterns, so no float
formatting is involved), plus every diagnostic message and position:

    1343 identical, 0 different, 1343 total

This is the check that carries the most weight, and it earns it: it is what
caught the diagnostic-addendum indentation being two ordinary spaces in the Go
port where `diagnostic.ts` writes two non-breaking spaces (U+00A0). Nothing else
in the suite would have noticed.

`compare-config.js` covers the service's config path, which `config.test.ts`
cannot: that test mutates `ConfigOptions` in place and asserts on object
identity, neither of which survives a stateless bridge. Instead both
implementations build an `AnalyzerService` over every project directory under
`src/tests/samples`, in command-line and language-server mode, and the whole
resulting `ConfigOptions` is diffed — every scalar, every file spec's compiled
regular expression, the 96-field diagnostic rule set, every execution
environment, and the files the source enumerator finds:

    78 identical, 0 different, 78 total

The Go-native tests in `common/`, `parser/`, `analyzer/` and `vfs/` cover the
pieces the TypeScript suite does not reach directly, such as the identifier
lookup tables and the in-memory file system.

### Running the checks

Everything below runs from the `go/` directory.

Go tests:

```bash
go test ./...
```

The TypeScript-against-Go bridge needs the 1.1.412 sources, an `esbuild`
binary, and a handful of Node packages -- install them in one command, because
`--no-save` prunes anything not named:

```bash
npm install --no-save esbuild vscode-uri vscode-jsonrpc vscode-languageserver vscode-languageserver-textdocument jsonc-parser smol-toml tmp fs-extra @yarnpkg/fslib @yarnpkg/libzip
```

Extract the reference tree once, then run everything:

```bash
make ref
```

```bash
make bridge
```

Individual checks have their own targets (`make bridge-tests`,
`make bridge-ast`, `make bridge-binder`, `make bridge-config`, ... -- see the
Makefile).

`make bridge-evaluator-tests` is the evaluator gate: pyright's own type
evaluator and checker tests -- 1,279 cases across nine files -- run
unmodified: 1,269 pass, 0 fail, 10 skipped (the skips drive the fourslash
harness, which needs a live in-process Program).

`make bridge-types-full` is the per-node type differential: the printed type
of **every name in every corpus file** -- 88,477 names over 1,343 files --
diffed one name at a time, which is what localizes an evaluator failure to a
single expression rather than an error count. `make bridge-types` is the
faster sampled variant.

Beyond the corpus, go/PORTING.md documents the whole-project differential: the
CLI run against a real 3,138-file project alongside pyright 1.1.412, zero
diagnostic differences in both single-threaded and `--threads` modes.

`make bridge-binder-oracle`, `make bridge-config-oracle`,
`make bridge-evaluator-oracle` and `make bridge-types-oracle` run those checks'
TypeScript side alone and
report what it produced. They need no Go server, so they validate the harness
independently of the port — which is how two defects in the config oracle were
caught before anything was compared against it.

`typePrinter.test.ts` works differently from the others: rather than shipping
data to Go, the shim records the test's type-construction calls and replays
them on the Go side, so it exercises the Go `types.ts` port as well as the
printer.

## Transliteration conventions

**Text is UTF-16.** This is the single most consequential decision. JavaScript
strings are sequences of UTF-16 code units, and every offset pyright records —
token starts, node ranges, diagnostic positions — is a code unit offset. Go
strings are UTF-8, so using them would silently shift every offset in any file
containing non-ASCII text. `common.Text` is therefore `[]uint16`, with
`CharCodeAt`/`Length`/`Substring` carrying JavaScript's semantics.

This is not merely about offsets. `stringTokenUtils.ts` builds its output with
`String.fromCharCode`, which truncates to 16 bits, so `"\U0001f600"` unescapes
to the single code unit `0xf600` rather than the astral code point, and
`"\ud800"` produces an unpaired surrogate. Neither survives a UTF-8 round trip.
Identifier values are the exception and are plain Go `string`s: an identifier
cannot contain an unpaired surrogate, so that conversion is lossless, and every
consumer wants a comparable map key.

**Optionality.** TypeScript's `undefined` becomes a Go pointer, a nil slice/map,
or an explicit `ok` return, chosen so that code branching on `=== undefined`
keeps branching the same way. `PythonVersion.Micro` is a `*int` for exactly this
reason.

**Discriminated unions.** Token shapes become a `Token` interface plus concrete
structs embedding `TokenBase`; `token as StringToken` becomes a type assertion.

**Numbers.** `NumberToken.value` is `number | bigint` in TypeScript, and which
arm the tokenizer picks changes the stored value of large integer literals.
`NumberValue` keeps the distinction. `jsnumber.go` reimplements `parseInt`,
`parseFloat` and `BigInt` because the exact rounding and accepted-prefix rules
decide what gets stored.

**Generated files.** `localization/localize_gen.go` (9.3k lines) and
`parser/unicode_gen.go` (3.7k lines) are generated from the TypeScript sources
by `localization/gen/generate.js` and `parser/gen/generate_unicode.js`. Both
inputs are bulk data — 847 message accessors and 3241 Unicode ranges — where
retyping by hand would be the least reliable option available. Regenerate rather
than edit.

**Concurrency.** JavaScript is single-threaded, so the TypeScript sources use
unguarded module-level mutable state freely. Where Go callers could reasonably
parse in parallel, that state is guarded (`sync.Once` for the identifier lookup
tables, a mutex for the localization tables). The one place this changes
structure is documented on `ensureIdentifierCharMap`, and
`TestFastTableUnchangedByFullBuild` pins the equivalence.

**Deliberate divergences.** There are three, all commented in place:

1. `ParameterizedString.format` uses a literal replacement. The TypeScript
   version passes the substituted value as the replacement argument of
   `String.prototype.replace`, which gives `$&`, `$1` and friends special
   meaning inside a substituted type or symbol name. That is an artifact of the
   JavaScript API rather than intended behavior.
2. The lazy identifier-table build does not rewrite the 256-entry fast table,
   because it would rewrite it with the values already there while racing
   readers. See `ensureIdentifierCharMap`.
3. `shallowCopyWithNewID` uses reflection where `_parseExpressionStatement` uses
   `Object.assign({}, leftExpr)`. The TypeScript is itself type-agnostic there,
   and a 78-case type switch would only add somewhere for the two to drift apart.

Performance comments in the TypeScript sources that describe V8-specific
behavior (`detachSubstring`, `cloneStr`, the two-shape token objects) were
evaluated individually rather than copied: `detachSubstring` and `cloneStr`
survive because Go subslices retain their backing array for the same reason V8
sliced strings retain their parent, while the two-shape object pattern does not,
because a nil slice header costs nothing.
