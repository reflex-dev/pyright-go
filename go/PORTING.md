# Porting status

Transliterated from pyright at tag `1.1.412` (`3c1c5b64e833d343cbbbe12b675ea597c6612d88`).

## Done

The **front end is complete**: everything in `packages/pyright-internal/src` that
turns Python source text into an AST.

| module | TypeScript | status |
| --- | --- | --- |
| `common/*` | text ranges, positions, diagnostics, Python versions, timing, string utils | complete (the parts the front end uses) |
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

`Parser.ParseSourceFile` and `Parser.ParseTextExpression` are both exported and
produce the same tree the TypeScript does.

## Not done

The **analyzer** — roughly 103k lines under `packages/pyright-internal/src/analyzer`:
binder, type evaluator, checker, import resolver, and the type system itself.
Nothing of it is ported. Neither is the language server, the CLI, or the
configuration layer.

So this parses Python exactly as pyright does, and type-checks nothing.

ANALYZER-PLAN.md is the plan for closing that gap: the package-layout decision
the import cycle forces, four staged milestones, and the oracle for each.

## How the front end is verified

Four checks, all against the real pyright 1.1.412 sources rather than against
expectations written by hand. Run them with `make bridge` (see README.md for the
prerequisites).

| check | what it covers | result |
| --- | --- | --- |
| `make bridge-tests` | pyright's own `tokenizer.test.ts`, unmodified, run against the Go tokenizer | 91 / 91 pass |
| `make bridge-parser-tests` | pyright's own `parser.test.ts`, unmodified, run against the Go parser | 23 / 23 runnable pass, 4 skipped |
| `make bridge-corpus` | token stream compared to the TypeScript tokenizer over the sample corpus | 1302 / 1302 identical |
| `make bridge-ast` | parse tree *and diagnostics* compared to the TypeScript parser over the sample corpus | 1343 / 1343 identical |
| `make test` | the Go unit tests | pass |

The 4 skipped `parser.test.ts` cases (`ModuleName range`, `Inline TypedDict dict
key is not a forward-reference annotation`, and two `AliasDeclaration.isLazy`
cases) drive the fourslash harness, which needs the binder and the import
resolver. They are reported as `SKIP` with a reason rather than being quietly
dropped or counted as passing.

The AST differential is the strongest of the four: it compares every node type,
every range, every flag, every string, every numeric literal (as IEEE bit
patterns, so no float formatting is involved) and every diagnostic message and
position, over 1343 real Python files.

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
