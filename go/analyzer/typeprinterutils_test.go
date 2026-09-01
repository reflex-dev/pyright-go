package analyzer

import "testing"

// TestJSONStringifyString pins the ECMA-262 QuoteJSONString behavior that
// PrintStringLiteral depends on. The expectations were produced by running the
// same inputs through node, not written by hand: Go's encoding/json differs
// from JSON.stringify on '<', '>', '&' and U+2028/U+2029, and those characters
// reach printed type names.
func TestJSONStringifyString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hi", `"hi"`},
		{"a\"b", `"a\"b"`},
		{"a\\b", `"a\\b"`},
		{"a\nb", `"a\nb"`},
		{"a\tb", `"a\tb"`},
		{"", `""`},
		{"<&>", `"<&>"`},
		{" ", `" "`},
		{"café", `"café"`},
		{"日本", `"日本"`},
		{"\x00\x1f", `"\u0000\u001f"`},
	}
	for _, c := range cases {
		if got := jsonStringifyString(c.in); got != c.want {
			t.Errorf("jsonStringifyString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPrintStringLiteralSingleQuote covers the ' branch, which re-unescapes the
// double quotes JSON.stringify added and escapes single quotes instead.
func TestPrintStringLiteralSingleQuote(t *testing.T) {
	if got := PrintStringLiteral(`he said "hi"`, "'"); got != `'he said "hi"'` {
		t.Errorf(`PrintStringLiteral("he said \"hi\"", "'") = %q`, got)
	}
	if got := PrintStringLiteral(`it's`, "'"); got != `'it\'s'` {
		t.Errorf(`PrintStringLiteral("it's", "'") = %q`, got)
	}
	if got := PrintStringLiteral(`plain`, `"`); got != `"plain"` {
		t.Errorf(`PrintStringLiteral("plain", "\"") = %q`, got)
	}
}

// TestPrintBytesLiteral pins the decimal-20 lower bound described in
// UPSTREAM-BUGS.md #8: code units 20 through 31 are emitted raw.
func TestPrintBytesLiteral(t *testing.T) {
	if got := PrintBytesLiteral("abc"); got != `b"abc"` {
		t.Errorf("PrintBytesLiteral(abc) = %q", got)
	}
	if got := PrintBytesLiteral("a\"b"); got != `b"a\"b"` {
		t.Errorf(`PrintBytesLiteral("a\"b") = %q`, got)
	}
	// 0x13 is below the bound and escapes; 0x14 (decimal 20) is not.
	if got := PrintBytesLiteral("\x13"); got != `b"\x13"` {
		t.Errorf("PrintBytesLiteral(0x13) = %q", got)
	}
	if got := PrintBytesLiteral("\x14"); got != "b\"\x14\"" {
		t.Errorf("PrintBytesLiteral(0x14) = %q, want the raw control character", got)
	}
}
