/*
 * text.go
 *
 * There is no direct TypeScript counterpart to this file. JavaScript strings
 * are sequences of UTF-16 code units, and every offset pyright records (token
 * starts, node ranges, diagnostic positions) is a UTF-16 code unit offset.
 * Go strings are UTF-8 byte sequences, so using them directly would silently
 * change every offset for any source file containing non-ASCII text.
 *
 * Text is therefore a []uint16 of UTF-16 code units, giving `.charCodeAt(i)`,
 * `.length` and `.substring(a, b)` exactly the semantics the TypeScript code
 * assumes.
 */

package common

import (
	"strings"
	"unicode/utf16"
)

// Text is a string represented as UTF-16 code units, matching JavaScript's
// string representation.
type Text []uint16

// NewText converts a Go (UTF-8) string into UTF-16 code units.
func NewText(s string) Text {
	// The ASCII fast path. utf16.Encode takes []rune, so the general form
	// decodes the string into a rune slice and then re-encodes it -- two
	// allocations and two passes. Python source is overwhelmingly ASCII, and
	// there one code unit is one byte, so the widening is a single pass with no
	// intermediate. The result is identical; this only skips work that would
	// have been the identity.
	if isASCII(s) {
		out := make(Text, len(s))
		for i := 0; i < len(s); i++ {
			out[i] = uint16(s[i])
		}
		return out
	}

	return Text(utf16.Encode([]rune(s)))
}

// isASCII reports whether s contains only code points below 0x80.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// String converts back to a Go (UTF-8) string. Unpaired surrogates are
// replaced with U+FFFD, matching what a lossy UTF-16 -> UTF-8 conversion does.
func (t Text) String() string {
	// The ASCII fast path, for the same reason NewText has one: utf16.Decode
	// allocates a []rune and string() then allocates again to encode it. When
	// every code unit is below 0x80 the UTF-8 encoding is byte-for-byte the low
	// halves, so one pass into a byte slice gives the identical string.
	ascii := true
	for _, unit := range t {
		if unit >= 0x80 {
			ascii = false
			break
		}
	}

	if ascii {
		out := make([]byte, len(t))
		for i, unit := range t {
			out[i] = byte(unit)
		}
		return string(out)
	}

	return string(utf16.Decode([]uint16(t)))
}

// Length returns the number of UTF-16 code units, i.e. JavaScript's
// `String.prototype.length`.
func (t Text) Length() int {
	return len(t)
}

// CharCodeAt returns the code unit at index, or 0 when out of range. Note that
// JavaScript returns NaN for out-of-range indices; every call site in the
// tokenizer and parser guards the index first or compares the result against a
// specific character, and NaN compares unequal to everything, so 0 is
// equivalent here except where a caller explicitly compares against Char.Null,
// which the guarded call sites never do on an out-of-range index.
func (t Text) CharCodeAt(index int) Char {
	if index < 0 || index >= len(t) {
		return 0
	}
	return Char(t[index])
}

// Substring returns the code units in [start, end), clamped to the bounds of t
// and matching JavaScript's `String.prototype.substring` for in-range,
// non-inverted arguments.
func (t Text) Substring(start, end int) Text {
	if start < 0 {
		start = 0
	}
	if end > len(t) {
		end = len(t)
	}
	if start >= end {
		return Text{}
	}
	return t[start:end]
}

// Slice returns the code units from start to the end of t.
func (t Text) Slice(start int) Text {
	if start < 0 {
		start = 0
	}
	if start > len(t) {
		return Text{}
	}
	return t[start:]
}

// Equal reports whether two Texts hold the same code units.
func (t Text) Equal(other Text) bool {
	if len(t) != len(other) {
		return false
	}
	for i := range t {
		if t[i] != other[i] {
			return false
		}
	}
	return true
}

// EqualString reports whether t holds the same code units as the UTF-8 string s.
func (t Text) EqualString(s string) bool {
	return t.Equal(NewText(s))
}

// IndexOfString returns the code unit index of the first occurrence of s in t,
// or -1. Equivalent to JavaScript's `String.prototype.indexOf`.
func (t Text) IndexOfString(s string) int {
	needle := NewText(s)
	if len(needle) == 0 {
		return 0
	}
	for i := 0; i+len(needle) <= len(t); i++ {
		if t[i : i+len(needle)].Equal(needle) {
			return i
		}
	}
	return -1
}

// HasPrefixString reports whether t starts with the UTF-8 string prefix,
// matching JavaScript's `String.prototype.startsWith`.
func (t Text) HasPrefixString(prefix string) bool {
	needle := NewText(prefix)
	if len(needle) > len(t) {
		return false
	}
	return t[:len(needle)].Equal(needle)
}

// TrimStart matches JavaScript's `String.prototype.trimStart`, which strips
// WhiteSpace and LineTerminator code units -- the same set the regular
// expression class \s matches, and a superset of what unicode.IsSpace covers
// (it includes U+FEFF).
func (t Text) TrimStart() Text {
	i := 0
	for i < len(t) && isJSWhitespaceCodeUnit(t[i]) {
		i++
	}
	return t[i:]
}

// TrimEnd matches JavaScript's `String.prototype.trimEnd`.
func (t Text) TrimEnd() Text {
	end := len(t)
	for end > 0 && isJSWhitespaceCodeUnit(t[end-1]) {
		end--
	}
	return t[:end]
}

// Trim matches JavaScript's `String.prototype.trim`.
func (t Text) Trim() Text {
	return t.TrimStart().TrimEnd()
}

// SplitByChar matches JavaScript's `String.prototype.split` with a
// single-code-unit separator: the result always has one more element than the
// number of separators, so splitting "" yields one empty piece and splitting
// "a," yields two.
func (t Text) SplitByChar(sep Char) []Text {
	parts := []Text{}
	start := 0
	for i := 0; i < len(t); i++ {
		if Char(t[i]) == sep {
			parts = append(parts, t[start:i])
			start = i + 1
		}
	}
	return append(parts, t[start:])
}

// JoinText matches JavaScript's `Array.prototype.join`.
func JoinText(parts []Text, sep string) Text {
	sepText := NewText(sep)
	out := Text{}
	for i, part := range parts {
		if i > 0 {
			out = append(out, sepText...)
		}
		out = append(out, part...)
	}
	return out
}

// Repeat returns t concatenated with itself count times.
func (t Text) Repeat(count int) Text {
	if count <= 0 {
		return Text{}
	}
	out := make(Text, 0, len(t)*count)
	for i := 0; i < count; i++ {
		out = append(out, t...)
	}
	return out
}

// TextBuilder accumulates UTF-16 code units, standing in for the string
// concatenation the TypeScript sources perform.
type TextBuilder struct {
	buf Text
}

// WriteChar appends a single code unit.
func (b *TextBuilder) WriteChar(c Char) {
	b.buf = append(b.buf, uint16(c))
}

// WriteText appends code units.
func (b *TextBuilder) WriteText(t Text) {
	b.buf = append(b.buf, t...)
}

// WriteString appends a UTF-8 string, converting it to UTF-16.
func (b *TextBuilder) WriteString(s string) {
	b.buf = append(b.buf, NewText(s)...)
}

// WriteCodePoint appends a code point, encoding it as a surrogate pair when it
// lies outside the BMP. This matches JavaScript's String.fromCodePoint.
func (b *TextBuilder) WriteCodePoint(cp rune) {
	if cp < 0x10000 {
		b.buf = append(b.buf, uint16(cp))
		return
	}
	hi, lo := utf16.EncodeRune(cp)
	b.buf = append(b.buf, uint16(hi), uint16(lo))
}

// Len returns the number of code units written so far.
func (b *TextBuilder) Len() int {
	return len(b.buf)
}

// Text returns the accumulated code units.
func (b *TextBuilder) Text() Text {
	return b.buf
}

// String returns the accumulated code units as a UTF-8 string.
func (b *TextBuilder) String() string {
	return b.buf.String()
}

// Reset discards accumulated content.
func (b *TextBuilder) Reset() {
	b.buf = b.buf[:0]
}

// IsStringPrefix reports whether s starts with prefix, a small helper used in
// places where the TypeScript code calls String.prototype.startsWith.
func IsStringPrefix(s, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}
