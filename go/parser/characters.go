/*
 * characters.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Based on code from vscode-python repository:
 *  https://github.com/Microsoft/vscode-python
 *
 * Utility routines used by tokenizer.
 *
 * Transliterated from parser/characters.ts (pyright 1.1.412).
 */

package parser

import (
	"sync"

	"github.com/microsoft/pyright/go/common"
)

type charCategory int

const (
	// Character cannot appear in identifier
	charCategoryNotIdentifierChar charCategory = 0

	// Character can appear at beginning or within identifier
	charCategoryStartIdentifierChar charCategory = 1

	// Character can appear only within identifier, not at beginning
	charCategoryIdentifierChar charCategory = 2

	// Character is a surrogate, meaning that additional character
	// needs to be consulted.
	charCategorySurrogateChar charCategory = 3
)

// Table of first 256 character codes (the most common cases).
const identifierCharFastTableSize = 256

var identifierCharFastTable [identifierCharFastTableSize]charCategory

// Map of remaining characters that can appear within identifier.
type charCategoryMap = map[int]charCategory

var identifierCharMap = charCategoryMap{}

// Secondary character map based on the primary (surrogate) character.
var surrogateCharMap = map[int]charCategoryMap{}

// We do lazy initialization of this map because it's rarely used. The
// TypeScript version guards with a plain boolean, which is safe because
// JavaScript is single-threaded; sync.Once gives the same
// initialize-exactly-once behavior when callers tokenize in parallel.
var identifierCharMapOnce sync.Once

// ensureIdentifierCharMap corresponds to the lazy `_buildIdentifierLookupTable(false)`
// call guarded by `_identifierCharMapInitialized`.
//
// One deliberate difference: the lazy build does not rewrite the fast table.
// In TypeScript the full build refills it, but it refills it with exactly the
// values the startup (fastTableOnly) build already wrote -- the range tables
// are sorted ascending, so every code point below 256 is covered identically in
// both modes, and `fill` plus rebuild reproduces the same 256 entries.
// Rewriting it here would instead be a data race against tokenizers already
// running on the fast path, which JavaScript's single thread made impossible.
// TestFastTableUnchangedByFullBuild pins the equivalence.
//
// Entries below 256 that the lazy build therefore writes into
// identifierCharMap rather than the fast table are never read: every lookup
// consults identifierCharMap only once it knows char >= 256.
func ensureIdentifierCharMap() {
	identifierCharMapOnce.Do(func() {
		buildIdentifierLookupTable(false)
	})
}

// noNextChar is the sentinel for the optional `nextChar` parameter, which the
// TypeScript sources distinguish from any real code unit via `undefined`.
const noNextChar = -1

// IsIdentifierStartChar corresponds to isIdentifierStartChar(). Pass
// noNextChar for the surrogate follower when there is none.
func IsIdentifierStartChar(char common.Char, nextChar int) bool {
	if char < identifierCharFastTableSize {
		return identifierCharFastTable[char] == charCategoryStartIdentifierChar
	}

	// Lazy initialize the char map. We'll rarely get here.
	ensureIdentifierCharMap()

	var category charCategory
	if nextChar != noNextChar {
		category = lookUpSurrogate(int(char), nextChar)
	} else {
		category = identifierCharMap[int(char)]
	}

	return category == charCategoryStartIdentifierChar
}

// IsIdentifierChar corresponds to isIdentifierChar().
func IsIdentifierChar(char common.Char, nextChar int) bool {
	if char < identifierCharFastTableSize {
		return identifierCharFastTable[char] == charCategoryStartIdentifierChar ||
			identifierCharFastTable[char] == charCategoryIdentifierChar
	}

	// Lazy initialize the char map. We'll rarely get here.
	ensureIdentifierCharMap()

	var category charCategory
	if nextChar != noNextChar {
		category = lookUpSurrogate(int(char), nextChar)
	} else {
		category = identifierCharMap[int(char)]
	}

	return category == charCategoryStartIdentifierChar || category == charCategoryIdentifierChar
}

// IsSurrogateChar corresponds to isSurrogateChar().
func IsSurrogateChar(char common.Char) bool {
	if char < identifierCharFastTableSize {
		return false
	}

	// Lazy initialize the char map. We'll rarely get here.
	ensureIdentifierCharMap()

	return identifierCharMap[int(char)] == charCategorySurrogateChar
}

// IsWhiteSpace corresponds to isWhiteSpace().
func IsWhiteSpace(ch common.Char) bool {
	return ch == common.CharSpace || ch == common.CharTab || ch == common.CharFormFeed
}

// IsLineBreak corresponds to isLineBreak().
func IsLineBreak(ch common.Char) bool {
	return ch == common.CharCarriageReturn || ch == common.CharLineFeed
}

// IsNumber corresponds to isNumber().
func IsNumber(ch common.Char) bool {
	return (ch >= common.Char0 && ch <= common.Char9) || ch == common.CharUnderscore
}

// IsDecimal corresponds to isDecimal().
func IsDecimal(ch common.Char) bool {
	return (ch >= common.Char0 && ch <= common.Char9) || ch == common.CharUnderscore
}

// IsHex corresponds to isHex().
func IsHex(ch common.Char) bool {
	return IsDecimal(ch) ||
		(ch >= common.CharLowerA && ch <= common.CharLowerF) ||
		(ch >= common.CharA && ch <= common.CharF) ||
		ch == common.CharUnderscore
}

// IsOctal corresponds to isOctal().
func IsOctal(ch common.Char) bool {
	return (ch >= common.Char0 && ch <= common.Char7) || ch == common.CharUnderscore
}

// IsBinary corresponds to isBinary().
func IsBinary(ch common.Char) bool {
	return ch == common.Char0 || ch == common.Char1 || ch == common.CharUnderscore
}

func lookUpSurrogate(char int, nextChar int) charCategory {
	if identifierCharMap[char] != charCategorySurrogateChar {
		return charCategoryNotIdentifierChar
	}

	surrogateTable, ok := surrogateCharMap[char]
	if !ok {
		return charCategoryNotIdentifierChar
	}

	// A missing entry yields charCategoryNotIdentifierChar (0), matching the
	// `undefined` the TypeScript version returns for an absent key.
	return surrogateTable[nextChar]
}

// Underscore is explicitly allowed to start an identifier.
// Characters with the Other_ID_Start property.
var specialStartIdentifierChars = UnicodeRangeTable{
	{common.CharUnderscore, common.CharUnderscore},
	{0x1885, 0x1885},
	{0x1886, 0x1886},
	{0x2118, 0x2118},
	{0x212e, 0x212e},
	{0x309b, 0x309b},
	{0x309c, 0x309c},
}

var startIdentifierCharRanges = []UnicodeRangeTable{
	specialStartIdentifierChars,
	UnicodeLu,
	UnicodeLl,
	UnicodeLt,
	UnicodeLo,
	UnicodeLm,
	UnicodeNl,
}

var startCharSurrogateRanges = []UnicodeSurrogateRangeTable{
	UnicodeLuSurrogate,
	UnicodeLlSurrogate,
	UnicodeLoSurrogate,
	UnicodeLmSurrogate,
	UnicodeNlSurrogate,
}

// Characters with the Other_ID_Start property.
var specialIdentifierChars = UnicodeRangeTable{
	{0x00b7, 0x00b7},
	{0x0387, 0x0387},
	{0x1369, 0x1369},
	{0x136a, 0x136a},
	{0x136b, 0x136b},
	{0x136c, 0x136c},
	{0x136d, 0x136d},
	{0x136e, 0x136e},
	{0x136f, 0x136f},
	{0x1370, 0x1370},
	{0x1371, 0x1371},
	{0x19da, 0x19da},
}

var identifierCharRanges = []UnicodeRangeTable{
	specialIdentifierChars,
	UnicodeMn,
	UnicodeMc,
	UnicodeNd,
	UnicodePc,
}

var identifierCharSurrogateRanges = []UnicodeSurrogateRangeTable{
	UnicodeMnSurrogate,
	UnicodeMcSurrogate,
	UnicodeNdSurrogate,
}

// buildIdentifierLookupTableFromUnicodeRangeTable writes into the fast table
// for code points below identifierCharFastTableSize and into fullTable
// otherwise. fastTable is nil when the destination is a surrogate sub-table, in
// which case every entry goes into fullTable -- matching the TypeScript call
// that passes the same map for both arguments.
func buildIdentifierLookupTableFromUnicodeRangeTable(
	table UnicodeRangeTable,
	category charCategory,
	fastTableOnly bool,
	useFastTable bool,
	fullTable charCategoryMap,
) {
	for entryIndex := 0; entryIndex < len(table); entryIndex++ {
		entry := table[entryIndex]
		rangeStart := entry.Start
		rangeEnd := entry.End

		for i := rangeStart; i <= rangeEnd; i++ {
			if useFastTable && i < identifierCharFastTableSize {
				identifierCharFastTable[i] = category
			} else {
				fullTable[i] = category
			}
		}

		if fastTableOnly && rangeStart >= identifierCharFastTableSize {
			break
		}
	}
}

func buildIdentifierLookupTableFromSurrogateRangeTable(
	surrogateTable UnicodeSurrogateRangeTable,
	category charCategory,
) {
	for surrogateChar, ranges := range surrogateTable {
		if _, ok := surrogateCharMap[surrogateChar]; !ok {
			surrogateCharMap[surrogateChar] = charCategoryMap{}
			identifierCharMap[surrogateChar] = charCategorySurrogateChar
		}

		buildIdentifierLookupTableFromUnicodeRangeTable(
			ranges,
			category,
			/* fastTableOnly */ false,
			/* useFastTable */ false,
			surrogateCharMap[surrogateChar],
		)
	}
}

// buildIdentifierLookupTable builds a lookup table to speed up tokenization of
// identifiers. Only the startup call (fastTableOnly) populates the fast table;
// see ensureIdentifierCharMap for why.
func buildIdentifierLookupTable(fastTableOnly bool) {
	useFastTable := fastTableOnly

	if useFastTable {
		for i := range identifierCharFastTable {
			identifierCharFastTable[i] = charCategoryNotIdentifierChar
		}
	}

	for _, table := range identifierCharRanges {
		buildIdentifierLookupTableFromUnicodeRangeTable(
			table,
			charCategoryIdentifierChar,
			fastTableOnly,
			useFastTable,
			identifierCharMap,
		)
	}

	for _, table := range startIdentifierCharRanges {
		buildIdentifierLookupTableFromUnicodeRangeTable(
			table,
			charCategoryStartIdentifierChar,
			fastTableOnly,
			useFastTable,
			identifierCharMap,
		)
	}

	// Populate the surrogate tables for characters that require two
	// character codes.
	if !fastTableOnly {
		for _, surrogateTable := range identifierCharSurrogateRanges {
			buildIdentifierLookupTableFromSurrogateRangeTable(surrogateTable, charCategoryIdentifierChar)
		}

		for _, surrogateTable := range startCharSurrogateRanges {
			buildIdentifierLookupTableFromSurrogateRangeTable(surrogateTable, charCategoryStartIdentifierChar)
		}
	}
}

func init() {
	buildIdentifierLookupTable(true)
}
