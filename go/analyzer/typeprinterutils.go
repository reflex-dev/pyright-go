/*
 * typeprinterutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Simple utility functions used by the type printer.
 *
 * Transliterated from analyzer/typePrinterUtils.ts (pyright 1.1.412).
 */

package analyzer

import (
	"strconv"
	"strings"
	"unicode/utf16"
)

// PrintStringLiteral corresponds to printStringLiteral. The TypeScript defaults
// quotation to `"`.
func PrintStringLiteral(value string, quotation string) string {
	// JSON.stringify will perform proper escaping for the " case, so we only
	// need to do our own escaping for the ' case.
	literalStr := jsonStringifyString(value)
	if quotation != `"` {
		inner := literalStr[1 : len(literalStr)-1]
		inner = strings.ReplaceAll(inner, `\"`, `"`)
		inner = strings.ReplaceAll(inner, `'`, `\'`)
		literalStr = "'" + inner + "'"
	}

	return literalStr
}

// jsonStringifyString reproduces JavaScript's JSON.stringify applied to a
// string, which is what the original relies on for escaping.
//
// Go's encoding/json is not a drop-in substitute: it HTML-escapes <, > and &
// by default, and escapes U+2028 and U+2029 even with SetEscapeHTML(false),
// whereas JSON.stringify emits all five literally. Since the result is
// interpolated into printed type names (Literal["..."]), those differences
// would be user-visible, so the ECMA-262 QuoteJSONString algorithm is
// implemented directly.
//
// Caveat: QuoteJSONString operates on UTF-16 code units and escapes lone
// surrogates as \uXXXX. A Go string cannot hold a lone surrogate as valid
// UTF-8, so that case cannot arise here; if the literal representation ever
// moves to common.Text ([]uint16), this function should move with it.
func jsonStringifyString(value string) string {
	var sb strings.Builder
	sb.WriteByte('"')

	for _, r := range value {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\b':
			sb.WriteString(`\b`)
		case '\t':
			sb.WriteString(`\t`)
		case '\n':
			sb.WriteString(`\n`)
		case '\f':
			sb.WriteString(`\f`)
		case '\r':
			sb.WriteString(`\r`)
		default:
			if r < 0x20 {
				sb.WriteString(`\u`)
				sb.WriteString(padHex4(uint16(r)))
			} else if utf16.IsSurrogate(r) {
				// Unreachable for a well-formed Go string; kept so the
				// behavior is defined if one ever arrives.
				sb.WriteString(`\u`)
				sb.WriteString(padHex4(uint16(r)))
			} else {
				sb.WriteRune(r)
			}
		}
	}

	sb.WriteByte('"')
	return sb.String()
}

func padHex4(v uint16) string {
	s := strconv.FormatUint(uint64(v), 16)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

// PrintBytesLiteral corresponds to printBytesLiteral.
//
// The original notes: there's no good built-in conversion routine in JavaScript
// to convert bytes strings, so it determines on a character-by-character basis
// whether it can be rendered into an ASCII character, and uses an escape if not.
//
// Note the lower bound is decimal 20, not 0x20, so code units 20 through 31 are
// emitted raw rather than escaped. See ../UPSTREAM-BUGS.md #8.
func PrintBytesLiteral(value string) string {
	var bytesString strings.Builder

	// The original indexes the JavaScript string by UTF-16 code unit, so this
	// walks code units too rather than runes.
	units := utf16.Encode([]rune(value))
	for _, charCode := range units {
		if charCode >= 20 && charCode <= 126 {
			if charCode == 34 {
				bytesString.WriteByte('\\')
				bytesString.WriteByte(byte(charCode))
			} else {
				bytesString.WriteByte(byte(charCode))
			}
		} else {
			bytesString.WriteString(`\x`)
			bytesString.WriteString(strconv.FormatUint(uint64((charCode>>4)&0xf), 16))
			bytesString.WriteString(strconv.FormatUint(uint64(charCode&0xf), 16))
		}
	}

	return `b"` + bytesString.String() + `"`
}
