# Parked: multiple-inheritance override checks

`checker_multiinherit.go.txt` is a finished port of
`_validateMultipleInheritanceCompatibility`,
`_validateMultipleInheritanceOverride`,
`_validateMultipleInheritancePropertyOverride`,
`_getCallableVariableOverrideComparison`, `_markFunctionStatic` and
`_addMultipleInheritanceRelatedInfo` (~500 lines). Drop it into `analyzer/`
and delete the matching stubs from `checker_validators_unported.go` to land it.

## Why it is not landed

It is blocked on one primitive: **`ValidateOverrideMethod` is still a stub that
returns `false`.** Every override comparison therefore answers "incompatible",
and wiring these checks in cost 3 gate tests and produced 26 spurious
diagnostics — 13 "define method in incompatible way", 8 "define variable in
incompatible way", 5 "incorrectly overrides property" — plus knock-on
abstract-class reports.

Landing it requires porting, from `typeEvaluator.ts`:

| function | lines |
| --- | --- |
| `validateOverrideMethod` | 137 |
| `validateOverrideMethodInternal` | 424 |
| `validateOverloadedMethodOverride` | 689 |

That is roughly 1,250 lines of upstream and deserves a dedicated pass. It is
also on the critical path for `_validateBaseClassOverrides`, the largest
remaining checker validator, which uses the same primitive — so porting it
unblocks two checks at once and is the highest-value single item left.

## The process lesson

Earlier in this session I audited every stub for a *dangerous default return
value* and found only `IsEnumClassWithMembers`. That audit asked the wrong
question. The hazard here was not a stub's default being wrong in the abstract
— it was **new code calling a stub at all**. `false` is a perfectly sensible
default for an unported predicate right up until something depends on it
answering truthfully.

The check that would have caught it costs nothing: before porting a validator,
grep its dependencies for `unported(`. The gate already ranks unported paths,
and `ValidateOverrideMethod` was sitting in that ranking the whole time.
