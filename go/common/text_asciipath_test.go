package common

import (
	"testing"
	"unicode/utf16"
)

// The ASCII fast paths in NewText and Text.String must agree with the general
// utf16 path on every input, including the ones that would make them diverge:
// non-ASCII within the BMP, astral characters that become surrogate pairs, and
// unpaired surrogates, which decode lossily to U+FFFD.
func TestTextASCIIFastPathMatchesGeneralPath(t *testing.T) {
	cases := []string{
		"",
		"abc",
		"def foo(x: int) -> str: ...",
		"café",
		"ÿ",
		"",
		"",
		"héllo wörld",
		"日本語",
		"\U0001F600 emoji",
		"mixed ascii \U0001F4A9 and not",
	}

	for _, input := range cases {
		got := NewText(input)
		want := Text(utf16.Encode([]rune(input)))

		if len(got) != len(want) {
			t.Fatalf("NewText(%q) length = %d, want %d", input, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("NewText(%q)[%d] = %d, want %d", input, i, got[i], want[i])
			}
		}

		general := string(utf16.Decode([]uint16(got)))
		if roundTrip := got.String(); roundTrip != general {
			t.Fatalf("Text.String() for %q = %q, want %q", input, roundTrip, general)
		}
		if input != "" && got.String() != input {
			t.Fatalf("round trip of %q gave %q", input, got.String())
		}
	}

	// An unpaired surrogate cannot be produced by NewText, so it is built
	// directly. Both paths must answer U+FFFD for it.
	lone := Text{0xD800, 'a'}
	if got, want := lone.String(), string(utf16.Decode([]uint16(lone))); got != want {
		t.Errorf("unpaired surrogate: got %q, want %q", got, want)
	}

	// 0x007f is the last ASCII code point and 0x0080 the first that is not;
	// the boundary is where an off-by-one in the fast path would show.
	boundary := Text{0x7f, 0x80}
	if got, want := boundary.String(), string(utf16.Decode([]uint16(boundary))); got != want {
		t.Errorf("ASCII boundary: got %q, want %q", got, want)
	}
}
