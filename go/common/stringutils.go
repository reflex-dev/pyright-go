/*
 * stringutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Utility methods for manipulating and comparing strings.
 *
 * Transliterated from common/stringUtils.ts (pyright 1.1.412).
 */

package common

import "strings"

// IsPatternInSymbol determines if a typed string matches a symbol name.
// Characters must appear in order. Returns true if all typed characters are in
// the symbol.
func IsPatternInSymbol(typedValue, symbolName string) bool {
	typedLower := NewText(strings.ToLower(typedValue))
	symbolLower := NewText(strings.ToLower(symbolName))
	typedLength := typedLower.Length()
	symbolLength := symbolLower.Length()
	typedPos := 0
	symbolPos := 0
	for typedPos < typedLength && symbolPos < symbolLength {
		if typedLower[typedPos] == symbolLower[symbolPos] {
			typedPos += 1
		}
		symbolPos += 1
	}
	return typedPos == typedLength
}

// HashString is a simple, non-cryptographic hash function for text. It
// reproduces the JavaScript version exactly, including the `| 0` truncation to
// a signed 32-bit value and the iteration over UTF-16 code units.
func HashString(contents string) int32 {
	return HashText(NewText(contents))
}

// HashText is HashString over text that is already in its UTF-16 form. Callers
// that hold a Text must use this rather than converting to a Go string first:
// the round trip would replace unpaired surrogates and change the hash.
func HashText(text Text) int32 {
	var hash int32

	for i := 0; i < text.Length(); i++ {
		hash = (hash << 5) - hash + int32(text[i])
	}
	return hash
}

// CompareStringsCaseInsensitive compares two strings using a case-insensitive
// ordinal comparison. Nil stands in for `undefined`.
func CompareStringsCaseInsensitive(a, b *string) Comparison {
	if a == nil && b == nil {
		return ComparisonEqualTo
	}
	if a != nil && b != nil && *a == *b {
		return ComparisonEqualTo
	}
	if a == nil {
		return ComparisonLessThan
	}
	if b == nil {
		return ComparisonGreaterThan
	}
	aUpper := strings.ToUpper(*a)
	bUpper := strings.ToUpper(*b)
	return CompareComparableStrings(&aUpper, &bUpper)
}

// CompareStringsCaseSensitive compares two strings using a case-sensitive
// ordinal comparison.
func CompareStringsCaseSensitive(a, b *string) Comparison {
	return CompareComparableStrings(a, b)
}

// GetStringComparer corresponds to getStringComparer().
func GetStringComparer(ignoreCase bool) func(a, b *string) Comparison {
	if ignoreCase {
		return CompareStringsCaseInsensitive
	}
	return CompareStringsCaseSensitive
}

// EquateStringsCaseInsensitive corresponds to equateStringsCaseInsensitive().
func EquateStringsCaseInsensitive(a, b string) bool {
	return CompareStringsCaseInsensitive(&a, &b) == ComparisonEqualTo
}

// EquateStringsCaseSensitive corresponds to equateStringsCaseSensitive().
func EquateStringsCaseSensitive(a, b string) bool {
	return CompareStringsCaseSensitive(&a, &b) == ComparisonEqualTo
}

// GetCharacterCount corresponds to getCharacterCount(). It counts UTF-16 code
// units, as the TypeScript version does.
func GetCharacterCount(value string, ch Char) int {
	result := 0
	text := NewText(value)
	for i := 0; i < text.Length(); i++ {
		if text.CharCodeAt(i) == ch {
			result++
		}
	}
	return result
}

// GetLastDottedString corresponds to getLastDottedString().
func GetLastDottedString(text string) string {
	index := strings.LastIndex(text, ".")
	if index > 0 {
		return text[index+1:]
	}
	return text
}

// Truncate corresponds to truncate(). Lengths are in UTF-16 code units.
func Truncate(text string, maxLength int) string {
	t := NewText(text)
	if t.Length() > maxLength {
		return t.Substring(0, maxLength-len("...")).String() + "..."
	}
	return text
}

// EscapeRegExp corresponds to escapeRegExp().
func EscapeRegExp(text string) string {
	var sb strings.Builder
	for _, r := range text {
		if strings.ContainsRune(`\^$.*+?()[]{}|`, r) {
			sb.WriteByte('\\')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}
