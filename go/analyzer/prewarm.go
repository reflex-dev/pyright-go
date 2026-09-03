/*
 * prewarm.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * No TypeScript counterpart. The package-level type singletons (the Any,
 * Unknown, Never and Unbound instances) are shared by every Program, and the
 * original lazily memoizes conversions onto each type's `cached` slot --
 * safe on JavaScript's single thread, a data race once --threads runs one
 * Program per goroutine. Filling those slots here, through the exact code
 * paths that would fill them lazily, produces identical values and leaves
 * only reads at runtime. Every conversion caches only when the result
 * differs from the input and returns the cached value on a hit, so after
 * this fixpoint no code path writes to a singleton again.
 */

package analyzer

func init() {
	pending := []Type{
		unboundInstance,
		unknownInstance,
		unknownIncompleteInstance,
		neverInstance,
		noReturnInstance,
		anyInstanceSpecialForm,
		anyInstance,
		ellipsisInstance,
	}

	warmed := map[Type]bool{}
	for len(pending) > 0 {
		t := pending[0]
		pending = pending[1:]
		if warmed[t] {
			continue
		}
		warmed[t] = true

		ConvertToInstance(t, true)
		ConvertToInstantiable(t, true)
		RequiresSpecialization(t, nil, 0)

		// Whatever the conversions cached is itself shared now; warm it too.
		if cached := t.Base().Cached; cached != nil {
			for _, result := range []Type{cached.InstanceType, cached.InstantiableType} {
				if result != nil && !warmed[result] {
					pending = append(pending, result)
				}
			}
		}
	}
}
