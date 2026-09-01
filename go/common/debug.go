/*
 * debug.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Various debug helper methods.
 *
 * Transliterated from common/debug.ts (pyright 1.1.412).
 */

package common

import "fmt"

// Fail corresponds to debug.fail() in the TypeScript sources, which throws.
// Go has no exceptions, so this panics; callers that relied on the throw
// propagating behave the same way under a recover() at the same boundary.
func Fail(message string) {
	panic("Debug Failure. " + message)
}

// Assert corresponds to debug.assert().
func Assert(expression bool, message string) {
	if !expression {
		msg := "False expression."
		if message != "" {
			msg = message
		}
		Fail(msg)
	}
}

// AssertDefined corresponds to debug.assertDefined().
func AssertDefined[T any](value *T, message string) *T {
	if value == nil {
		msg := "Assertion failed."
		if message != "" {
			msg = message
		}
		Fail(msg)
	}
	return value
}

// AssertNever corresponds to debug.assertNever(); it reports an unexpected
// value reaching an exhaustive switch.
func AssertNever(value any, message string) {
	msg := message
	if msg == "" {
		msg = "Illegal value:"
	}
	Fail(fmt.Sprintf("%s %v", msg, value))
}
