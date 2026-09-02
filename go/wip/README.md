# Parked: validateInstanceVariableInitialization

`checker_instancevars.go.txt` is a finished port of
`_validateInstanceVariableInitialization` (156 lines). Drop it into `analyzer/`
and delete the matching stub from `checker_validators_unported.go` to land it.

## Why it is not landed

It reads `ClassType.Shared.NamedTupleEntries` -- via `ClassTypeHasNamedTupleEntry`
-- to exempt NamedTuple fields, whose bare annotations become instance variables
by synthesis. That field is populated in only two places upstream:

- `dataClasses.ts` `synthesizeDataClassMethods`, for the class syntax
  (`class A(NamedTuple): x: int`), and
- `namedTuples.ts` `createNamedTupleType`, for the functional form.

Both are still stubs here, so the field is always nil, the exemption never
fires, and every NamedTuple field is reported uninitialized. Cost: 1 gate test
(`UninitializedVariable3`) and 10 spurious diagnostics.

`synthesizeDataClassMethods` is the unblocking item; it is the bulk of the
"rest of dataClasses.ts" already on the remaining list.

## The refinement to the dependency check

The rule recorded in STATUS-STAGE-D.md was to grep a validator's dependencies
for `unported(` before porting it. That check ran here and passed: the only
evaluator call in this function is `addDiagnostic`.

It passed because the dependency is not a function this code calls. It is a
**field that unported code was supposed to fill in**. `ClassTypeHasNamedTupleEntry`
is a faithful two-line accessor; nothing about it is stubbed. The gap is that
nothing ever writes what it reads.

So the check needs a second half: for each `Shared.X` / `Priv.X` field a new
validator reads, confirm something in the Go tree actually assigns it. A
one-line grep for `\.X = ` catches it, and it would have caught this.
