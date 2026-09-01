/*
 * core.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Helpers from common/core.ts (pyright 1.1.412).
 *
 * Most of core.ts is JavaScript type-guard plumbing (isString, isArray,
 * isThenable) with no Go equivalent, so only the comparison helpers, the
 * case-folding helper and containsOnlyWhitespace are ported.
 */

package common

import "strings"

// Comparison corresponds to the const enum of the same name.
type Comparison int

const (
	ComparisonLessThan    Comparison = -1
	ComparisonEqualTo     Comparison = 0
	ComparisonGreaterThan Comparison = 1
)

// ToLowerCase corresponds to toLowerCase.
func ToLowerCase(x string) string {
	return strings.ToLower(x)
}

// CompareComparableStrings corresponds to the string overload of
// compareComparableValues. A nil pointer stands in for `undefined`, which the
// original orders before every defined value.
func CompareComparableStrings(a, b *string) Comparison {
	switch {
	case a == nil && b == nil:
		return ComparisonEqualTo
	case a == nil:
		return ComparisonLessThan
	case b == nil:
		return ComparisonGreaterThan
	case *a == *b:
		return ComparisonEqualTo
	case *a < *b:
		return ComparisonLessThan
	default:
		return ComparisonGreaterThan
	}
}

// CompareValues corresponds to compareValues, i.e. the number overload of
// compareComparableValues. A nil pointer stands in for `undefined`.
func CompareValues(a, b *int) Comparison {
	switch {
	case a == nil && b == nil:
		return ComparisonEqualTo
	case a == nil:
		return ComparisonLessThan
	case b == nil:
		return ComparisonGreaterThan
	case *a == *b:
		return ComparisonEqualTo
	case *a < *b:
		return ComparisonLessThan
	default:
		return ComparisonGreaterThan
	}
}

// ContainsOnlyWhitespace corresponds to containsOnlyWhitespace. The original
// tests /^\s*$/ against text.substring(start, end); the TypeScript leaves start
// and end undefined to mean the whole string, so pass 0 and len(text) for that.
//
// This matches exactly the code points JavaScript's \s class covers, which is
// not the same set as Go's unicode.IsSpace -- \s also includes U+FEFF.
func ContainsOnlyWhitespace(text Text, start, end int) bool {
	if start < 0 {
		start = 0
	}
	if end > len(text) {
		end = len(text)
	}
	for i := start; i < end; i++ {
		if !isJSWhitespaceCodeUnit(text[i]) {
			return false
		}
	}
	return true
}

// isJSWhitespaceCodeUnit reports whether a UTF-16 code unit is matched by the
// regular expression class \s in JavaScript.
func isJSWhitespaceCodeUnit(c uint16) bool {
	switch c {
	case '\t', '\n', '\v', '\f', '\r', ' ',
		0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return c >= 0x2000 && c <= 0x200a
}
