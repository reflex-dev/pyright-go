# Suspected bugs in pyright 1.1.412

Found while transliterating pyright to Go. **All of these are reproduced
faithfully in the Go port** — the goal is behavior identical to the original,
so "fixing" them here would be a divergence. Each site carries a comment
pointing back at this file.

Line numbers refer to tag `1.1.412` (`3c1c5b64e833d343cbbbe12b675ea597c6612d88`),
under `packages/pyright-internal/src`.

Nothing here has been reported upstream yet.

---

## 1. `allSubtypes` always returns false for a union

**`analyzer/typeUtils.ts:796`**

```ts
export function allSubtypes(type: Type, callback: (type: Type) => boolean): boolean {
    if (isUnion(type)) {
        return type.priv.subtypes.every((subtype) => {
            callback(subtype);
        });
    } else {
        return callback(type);
    }
}
```

The arrow passed to `every` has a *block* body and no `return`, so it evaluates
to `undefined`. `Array.prototype.every` stops at the first falsy result, so this
returns `false` for any non-empty union no matter what `callback` says. Only the
non-union branch behaves as the name implies.

The fix is `(subtype) => callback(subtype)` or just `callback`. Worth checking
what the call sites expect before changing it, since some may have been written
against the current behavior.

Go port: `analyzer/typeutils_subtypes.go`, `AllSubtypes`.

---

## 2. `derivesFromAnyOrUnknown` tests the whole type instead of the subtype

**`analyzer/typeUtils.ts:874`**

```ts
doForEachSubtype(type, (subtype) => {
    if (isAnyOrUnknown(type)) {          // <- `type`, not `subtype`
        anyOrUnknown = true;
    } else if (isInstantiableClass(subtype)) {
```

Every other branch in the callback uses `subtype`. As written, the first test is
loop-invariant: it answers true only when the *entire* type is Any/Unknown (which
for a union means every subtype is), and an `Any` subtype inside a mixed union is
never noticed. The following two branches then have to do the work.

Go port: `analyzer/typeutils_conditions.go`, `DerivesFromAnyOrUnknown`.

---

## 3. `getLiteralTypeClassName` never compares class names

**`analyzer/typeUtils.ts:1294`**

The doc comment says:

> If all of the subtypes are literals with the same built-in class (e.g. all
> 'int' or all 'str'), this function returns the name of that type. If some of
> the subtypes are not literals **or the literal classes don't match**, it
> returns undefined.

But the union branch is:

```ts
doForEachSubtype(type, (subtype) => {
    const subtypeLiteralTypeName = getLiteralTypeClassName(subtype);
    if (!subtypeLiteralTypeName) {
        foundMismatch = true;
    } else if (!className) {
        className = subtypeLiteralTypeName;
    }
});
```

`className` is only ever assigned once, and later names are never compared
against it. `Literal[1] | Literal["a"]` therefore returns `"int"` rather than
`undefined`. The `else if (!className)` presumably wants to be
`else if (!className) { className = ... } else if (className !== subtypeLiteralTypeName) { foundMismatch = true; }`.

Go port: `analyzer/typeutils_literals.go`, `GetLiteralTypeClassName`.

---

## 4. Dead `!== undefined` guard in `isTypeSame`

**`analyzer/types.ts:3546`**

```ts
const positionOnlyIndex1 = params1.findIndex((param) => isPositionOnlySeparator(param));
...
const isName1Relevant = positionOnlyIndex1 !== undefined && i > positionOnlyIndex1;
```

`Array.prototype.findIndex` returns `-1`, never `undefined`, so the guard is
always true. The effective behavior is `i > -1`, i.e. every parameter name is
"relevant" when there is no positional-only separator — which may well be what is
wanted, but the guard suggests the author expected a different sentinel.

Harmless today; worth simplifying so it doesn't read as a real condition.

Go port: `analyzer/types_same.go`, in the `TypeCategoryFunction` case.

---

## 5. `ModuleType.create` ORs `Instantiable` with itself

**`analyzer/types.ts:505`**

```ts
const newModuleType: ModuleType = {
    category: TypeCategory.Module,
    flags: TypeFlags.Instantiable | TypeFlags.Instantiable,
```

Almost certainly meant `TypeFlags.Instantiable | TypeFlags.Instance` — every
other singleton in the file (`UnboundType`, `UnknownType`, `NeverType`,
`AnyType`) sets both. As written the result is just `Instantiable`, so a module
type reports `TypeBase.isInstance() === false`.

This one has real behavioral weight, so it should be checked against the test
corpus before changing.

Go port: `analyzer/types_simple.go`, `ModuleTypeCreate`.

---

## 6. `mapSignatures` can drop an implementation without recording a change

**`analyzer/typeUtils.ts:495`**

```ts
if (implementation && isFunction(implementation)) {
    newImplementation = callback(implementation);

    if (newImplementation) {
        changeMade = true;
    }
}

if (!changeMade) {
    return type;
}
```

If the callback returns `undefined` for the implementation, `newImplementation`
becomes `undefined` — dropping it — but `changeMade` is not set. When no overload
changed either, the function returns the *original* `type` with its
implementation still attached, silently discarding the callback's decision.

Lower confidence than the others: it may be deliberate, on the theory that
dropping only the implementation is not a meaningful change.

Go port: `analyzer/typeutils_subtypes.go`, `MapSignatures`.

---

## 7. `transformGenericTypeAlias` compares the wrong operand

**`analyzer/typeUtils.ts:3700`**

```ts
const newTypeArgs = aliasInfo.typeArgs.map((typeArg) => {
    const updatedType = this.apply(typeArg, recursionCount);
    if (type !== updatedType) {          // <- `type`, not `typeArg`
        requiresUpdate = true;
    }
    return updatedType;
});
```

`requiresUpdate` is meant to record whether *any type argument changed*, but it
compares the containing `type` against each transformed argument. A type
argument is essentially never identical to the type that contains it, so the
flag is set whenever there is at least one type argument, and the function
clones unconditionally.

The effect is wasted allocation rather than a wrong answer — the clone carries
the same type args when nothing changed — but it defeats the "avoid allocating
until a change is detected" pattern used everywhere else in the file. Same class
of typo as #2.

Go port: `analyzer/typeutils_transform.go`, `TransformGenericTypeAlias`.

---

## 8. `printBytesLiteral` uses decimal 20 where 0x20 was meant

**`analyzer/typePrinterUtils.ts:36`**

```ts
if (charCode >= 20 && charCode <= 126) {
```

The upper bound is 126 — decimal, and correct: 0x7E is `~`, the last printable
ASCII character. The lower bound is 20 — also decimal, but the printable range
starts at 32 (0x20). Almost certainly `0x20` was intended and the `0x` was
dropped, since the surrounding code otherwise thinks in hex (the escape it
emits is `\x%x%x`).

The effect is that code units 20 through 31 — `DC4` through `US`, all
unprintable control characters — are emitted raw into the rendered bytes
literal instead of being escaped. So `b"\x14"` prints as a literal control
character rather than as `\x14`.

Go port: `analyzer/typeprinterutils.go`, `PrintBytesLiteral`, pinned by
`TestPrintBytesLiteral`.

---

## 9. `printExpression` set and slice rendering -- **already fixed upstream**

**`analyzer/parseTreeUtils.ts:467` and `:550`**

Two defects in `printExpression` at 1.1.412:

```ts
case ParseNodeType.Set: {
    return node.d.items.map((entry) => printExpression(entry, flags)).join(', ');
}
```

A set display renders with no braces, so `{1, 2}` prints as `1, 2`. And the
slice case emits `': '` for both the end and step separators while skipping
absent components entirely, so `x[1:]` prints as `x[1]` and `x[::2]` as
`x[: 2]`.

Both are **already fixed on main** by commit `e690634a9`, "Fix printExpression
output for slice expressions and set displays (#11598)", which lands after
1.1.412. Nothing to report upstream -- noted here so a future reader does not
rediscover it and file it.

The Go port reproduces the 1.1.412 behavior, since that is the version being
transliterated.

### A related one that is *not* fixed

The `Dictionary` case interpolates an array into a template literal:

```ts
const dictContents = `${node.d.items.map((entry) => { ... })}`;
```

Interpolating an array calls `Array.prototype.toString()`, which joins with a
bare comma and no space -- so `{a: 1, b: 2}` prints as `{ a: 1,b: 2 }`. Every
other collection case in the same function uses an explicit `.join(', ')`. This
one is still present on main.

Go port: `analyzer/parsetreeutils_print.go`, `PrintExpression`.

---

## 10. `printFunctionPartsInternal` stringifies a TypeVar object

`analyzer/typePrinter.ts:1352-1355`

```ts
const paramSpec = FunctionType.getParamSpecFromArgsKwargs(type);
...
if (printTypeFlags & PrintTypeFlags.PythonSyntax) {
    paramTypeStrings.push(`*args: ${paramSpec}.args`);
    paramTypeStrings.push(`**kwargs: ${paramSpec}.kwargs`);
}
```

`paramSpec` is a `TypeVarType`, not a string. Interpolating it calls
`Object.prototype.toString()`, so under `PrintTypeFlags.PythonSyntax` a
ParamSpec-carrying signature prints as

```
(*args: [object Object].args, **kwargs: [object Object].kwargs)
```

The intent is plainly `paramSpec.shared.name`, which is what the surrounding
code uses -- `printFunctionType` reaches for `paramSpec.shared.name` twice in
its own PythonSyntax branch (lines 906 and 910). The non-PythonSyntax branch
of the same function correctly calls `printTypeInternal(paramSpec, ...)`.

Go port: `analyzer/typeprinter_print.go`, `printFunctionPartsInternal`. The
literal `[object Object]` is reproduced, since correcting it would change
printed output.

---

## 11. `getTokenIndexAfter` bounds the loop with the wrong accessor

`analyzer/parseTreeUtils.ts:1908`

```ts
export function getTokenIndexAfter(tokens: TextRangeCollection<Token>, position: number, ...) {
    const index = tokens.getItemAtPosition(position);
    if (index < 0) return -1;

    for (let i = index; i < tokens.length; i++) {
        const token = tokens.getItemAt(i);
```

`TextRangeCollection.length` is the *character span* of the collection
(`this.end - this.start`); the number of items is `count`. Every other loop over
a token collection in this file uses `tokens.count` -- `getTokenIndexAtLeft` and
`getNextMatchingToken` both do.

Since a file's character length is almost always larger than its token count,
`getItemAt` runs past the end whenever the predicate never matches, and
`getItemAt` calls `fail('index is out of range')`. So a search that should
return `-1` throws instead.

Go port: `analyzer/parsetreeutils_tokens.go`, `GetTokenIndexAfter`. Reproduced,
including the panic, since `common.TextRangeCollection.GetItemAt` calls
`common.Fail` at the same point.

---

## 12. `prevNode === curNode.d.<optional>` matches when both are `undefined`

`analyzer/parseTreeUtils.ts`, in `isWithinTypeAnnotation` (1422),
`isWithinAnnotationComment` (1475), `isWithinDefaultParamInitializer` (1397),
`getParentAnnotationNode` (1184) and `isWriteAccess` (1991).

Each of these walks up from `node` with `prevNode` initialized to `undefined`
and compares it against an optional child:

```ts
let curNode: ParseNode | undefined = node;
let prevNode: ParseNode | undefined;

while (curNode) {
    if (curNode.nodeType === ParseNodeType.Function && prevNode === curNode.d.returnAnnotation) {
        return isQuoted || !requireQuotedAnnotation;
    }
    ...
```

On the **first** iteration `prevNode` is `undefined`. If `curNode` is itself a
function with no return annotation, `curNode.d.returnAnnotation` is also
`undefined`, and `undefined === undefined` is true. So

```py
def f(a: int = 3): ...
```

`isWithinTypeAnnotation(functionNode, ...)` returns `true` for the function
node, and `isWithinAnnotationComment(functionNode)` returns `true` as well —
neither is "within" anything. The same happens for a parameter node with no
annotation comment, an assignment with no annotation comment, and so on.

The guard the other walks in the same file use is an explicit `if (!prevNode)
break;` — `getEvaluationScopeNode` has exactly that in its Function and Class
cases.

Go port: `analyzer/parsetreeutils_match.go`, `parsetreeutils_nav.go` and
`parsetreeutils_misc.go`, all through the `sameNode` helper. Adding the obvious
nil guard instead was tried first, and the corpus differential in
`tools/ts-bridge/compare-parsetreeutils.js` caught the divergence immediately.

---

## 13. `TypeshedInfoProvider`'s negative cache entries are never read

`analyzer/typeshedInfoProvider.ts:53`, and the same shape at line 37.

```ts
const cached = this._typeshedSubdirectoryCache.get(key);
if (cached !== undefined) {
    return cached;
}

const typeshedRoot = this.getTypeshedRoot(customTypeshedPath, importLogger);
if (!typeshedRoot) {
    this._typeshedSubdirectoryCache.set(key, undefined);   // <-- never a hit
    return undefined;
}

const subdir = PythonPathUtils.getTypeshedSubdirectory(typeshedRoot, isStdLib);
if (!this._fileSystem.dirExists(subdir)) {
    this._typeshedSubdirectoryCache.set(key, undefined);   // <-- never a hit
    return undefined;
}
```

The intent is visible in the two `set(key, undefined)` calls: they exist to
record "searched, found nothing" so the answer is not recomputed. But the read
is `cached !== undefined`, and `Map.get` returns `undefined` both for a key
that was never set and for a key that was set to `undefined`, so a cached
absence is indistinguishable from a miss. Every one of those entries is written
and then never read.

The consequence is not a wrong answer, it is repeated file-system work: when
there is no typeshed root, or the `stdlib`/`stubs` subdirectory does not exist,
`dirExists` runs on every call rather than once. `getTypeshedSubdirectory` is
called from `_findTypeshedPath`, which the resolver reaches on every unresolved
import — which is the case where it matters most.

The fix upstream is `if (this._cache.has(key))`, or a sentinel.

`getTypeshedRoot` at line 37 has the same read, but no explicit negative write:
`_computeTypeshedRoot` returns `undefined` and it is stored by the normal
`set(key, root)`. Same effect, arrived at less deliberately.

Go port: `analyzer/typeshedinfoprovider.go`. A nil `uri.Uri` in a Go map has
exactly the same property — a missing key and a stored nil both read as nil — so
the behaviour carries over without any effort to reproduce it. It is recorded
here because the *fix* would be a divergence: it changes how often the file
system is touched, which the import logger records and which the resolver's
parent-directory cache is layered on top of.

## 14. `??` and `?:` precedence makes a `selfClass` computation always discard `selfType`

`analyzer/typeEvaluator.ts:6632`, inside `getTypeOfClassMemberName`.

```ts
const selfClass = selfType ?? memberName === '__new__' ? undefined : classType;
```

`===` binds tighter than `??`, and `??` binds tighter than the conditional, so
this parses as

```ts
const selfClass = (selfType ?? (memberName === '__new__')) ? undefined : classType;
```

`selfType` is a `ClassType | TypeVarType | undefined`, so whenever it is defined
it is a truthy object and the conditional takes the `undefined` branch. The
result is that `selfClass` is `undefined` whenever a `selfType` was supplied --
the exact case where the caller wanted it used.

The shape everywhere else in the same function is

```ts
selfType ? convertToInstantiable(selfType) : (memberName !== '__new__' ? classType : undefined)
```

so the intent was almost certainly

```ts
const selfClass = selfType ?? (memberName === '__new__' ? undefined : classType);
```

which differs whenever `selfType` is defined.

The reach is narrow: this line is only evaluated on a `set` through an object,
inside the declaring class body, to a symbol that is effectively a `ClassVar` --
and its result feeds a `getTypeOfMemberInternal` call whose only use is an
`isDescriptorInstance` test. So the practical effect is that a descriptor stored
in a `ClassVar` is specialized against the wrong self class in that one check.

The Go port reproduces the parsed behavior, with a comment at
`analyzer/typeevaluator_classmembername.go` pointing here.

---

---

## Non-bugs worth knowing about

These looked wrong at first and are not:

- **`Symbol.isNamedTupleMemberMember`** (`analyzer/symbol.ts:230`) — the doubled
  "Member" is just a name, not a typo with consequences.
- **`FunctionTypeFlags` skips bit 10** (`analyzer/types.ts:1680`) — `Async` is
  `1 << 9` and `StubDefinition` is `1 << 11`. A flag was presumably removed. The
  gap is harmless as long as nothing reuses the bit.
- **The doubled length check in `FunctionType.clone`**
  (`analyzer/types.ts:1882-1884`) — `if (type.shared.parameters.length > 0)`
  nested inside an identical check. Dead, not wrong.
- **The `else if` chain in `convertToTypeFormType`** (`analyzer/typeEvaluator.ts:26868`)
  — the function returns early unless `srcType.props?.typeForm` is truthy, then
  immediately re-tests the same condition. The first branch is therefore always
  taken and the three `else if` arms (isClass, isTypeVar, and the `type[T]`
  unwrap) are unreachable. Dead, not wrong. The Go port keeps the arms so a
  future change to the guard does not silently lose them.
- **The parenthesization in `isPossibleTypeDictFactoryCall`**
  (`analyzer/typeEvaluator.ts:29947`) — written as
  `(callLeftNode.nodeType === Name && callLeftNode.d.value) === 'TypedDict'`,
  which reads like a misplaced paren. It is not: `&&` yields `false` for a
  non-Name and the value string for a Name, so the comparison gives the same
  answer as the intended `A && B === 'TypedDict'`.
- **Diagnostic addendum indentation is two U+00A0 non-breaking spaces**
  (`common/diagnostic.ts`) — written literally in the source. Deliberate, and
  load-bearing: the Go port got this wrong initially and the AST differential
  caught it.

## 15. `getTypeOfYieldFrom` indexes past the end of `generatorTypeArgs`

`typeEvaluator.ts`, `getTypeOfYieldFrom`:

```ts
const generatorTypeArgs = getGeneratorTypeArgs(yieldFromSubtype);
if (generatorTypeArgs) {
    return generatorTypeArgs.length >= 2 ? generatorTypeArgs[2] : UnknownType.create();
}
```

The guard tests for at least *two* type arguments but reads index `[2]`, the
*third*. A `Generator` specialized with exactly two arguments therefore yields
`undefined`, which TypeScript happily returns as the subtype's narrowed type;
downstream code that expects a `Type` receives `undefined`.

The same pattern appears again a few lines below for the iterable fallback.

The Go port tightens the bound to the index actually read (`> 2`), which is what
the guard was evidently meant to say. Where the original produced `undefined`,
the port produces `Unknown` -- the value the other arm of the same conditional
already produces.

## 16. A solution set can map a TypeVar to a type mentioning that TypeVar

`constraintSolver.ts` applies an occurs check when *recording* a lower bound
(`widenLowerBound`, added for microsoft/pyright#11413), on the stated grounds
that a cyclic constraint has no finite solution and later substitution rounds
expand it into an exponentially growing type.

That check cannot see a cycle created *after* the bound is recorded, and
`solveTypeVarRecursive` creates exactly one. For `func2(func1, func2)` in
`tests/samples/solverHigherOrder3.py`:

- `T@func2`'s lower bound is `func1`'s signature, which mentions `T@func1`;
- `T@func1` solves to `T@func2`;
- substituting the dependent solutions yields
  `T@func2 := (x: T@func2, y: U@func1) -> ...`.

`applySolvedTypeVars` then re-enters `T@func2` at every nesting level of the
function it substitutes into. Pyright's own recursion cap bounds the depth, so
this is finite rather than infinite -- but the branching makes it billions of
transformer calls.

Pyright itself completes this file, so something in its evaluation order avoids
reaching the pathological substitution rather than the cycle being harmless. The
port hit the pathological path and hung. The Go port refuses to expand a
self-referential replacement in `applySolvedTypeVarsTransformer.transformTypeVar`,
which restores the invariant the occurs check exists to maintain and terminates.
This divergence is marked at the site.
