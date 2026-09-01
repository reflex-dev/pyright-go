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
	return Text(utf16.Encode([]rune(s)))
}

// String converts back to a Go (UTF-8) string. Unpaired surrogates are
// replaced with U+FFFD, matching what a lossy UTF-16 -> UTF-8 conversion does.
func (t Text) String() string {
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
