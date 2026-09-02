/*
 * jsonc_test.go
 *
 * StripJSONC is not a transliteration of anything, so nothing upstream
 * validates it. These cover the cases a pyrightconfig.json actually contains
 * and the two a naive scanner gets wrong: a comment opener inside a string, and
 * an escaped quote.
 */

package common

import (
	"reflect"
	"testing"
)

func TestParseJSONC(t *testing.T) {
	tests := []struct {
		name string
		text string
		want any
	}{
		{
			name: "plain",
			text: `{"include": ["src"]}`,
			want: map[string]any{"include": []any{"src"}},
		},
		{
			name: "line comment",
			text: "{\n // a comment\n \"typeCheckingMode\": \"strict\" // trailing\n}",
			want: map[string]any{"typeCheckingMode": "strict"},
		},
		{
			name: "block comment",
			text: `{/* one */ "a": 1, /* two
                    lines */ "b": 2}`,
			want: map[string]any{"a": float64(1), "b": float64(2)},
		},
		{
			name: "trailing comma in object",
			text: `{"a": 1,}`,
			want: map[string]any{"a": float64(1)},
		},
		{
			name: "trailing comma in array",
			text: `{"a": [1, 2, ]}`,
			want: map[string]any{"a": []any{float64(1), float64(2)}},
		},
		{
			name: "trailing comma before a newline",
			text: "{\n \"a\": 1,\n}",
			want: map[string]any{"a": float64(1)},
		},
		{
			name: "comment opener inside a string is not a comment",
			text: `{"a": "http://example.com", "b": "/* not a comment */"}`,
			want: map[string]any{"a": "http://example.com", "b": "/* not a comment */"},
		},
		{
			name: "escaped quote does not end the string",
			text: `{"a": "he said \"//\" here", "b": 2}`,
			want: map[string]any{"a": `he said "//" here`, "b": float64(2)},
		},
		{
			name: "escaped backslash does end the string",
			text: `{"a": "c:\\", "b": 2}`,
			want: map[string]any{"a": `c:\`, "b": float64(2)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseJSONC(test.text)
			if err != nil {
				t.Fatalf("ParseJSONC: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParseJSONCRejectsMalformed(t *testing.T) {
	// pyright discards the result if jsonc-parser reports any error, so the
	// only thing that has to match is that these are rejected.
	for _, text := range []string{
		`{"a": }`,
		`{"a" 1}`,
		`{`,
		`{"a": "unterminated}`,
	} {
		if _, err := ParseJSONC(text); err == nil {
			t.Fatalf("expected %q to be rejected", text)
		}
	}
}
