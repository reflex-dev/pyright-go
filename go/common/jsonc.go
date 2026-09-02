/*
 * jsonc.go
 *
 * A reader for JSON with comments, standing in for the `jsonc-parser` package
 * that service.ts uses to read pyrightconfig.json.
 *
 * Not a transliteration: jsonc-parser is a full incremental scanner with error
 * recovery, and pyright uses one entry point of it --
 *
 *     const errors: JSONC.ParseError[] = [];
 *     const configObj = JSONC.parse(fileContents, errors, { allowTrailingComma: true });
 *     if (errors.length === 0) { return configObj; }
 *
 * -- and discards the result entirely if anything at all went wrong. So what is
 * needed is the accept/reject decision and the parsed value, not the recovery.
 *
 * StripJSONC removes comments and trailing commas, and then encoding/json does
 * the parsing. The two agree on which documents are valid except at the edges
 * jsonc-parser is deliberately lenient about and pyright then rejects anyway,
 * since it treats any reported error as a failure.
 */

package common

import (
	"encoding/json"
	"strings"
)

// StripJSONC removes // and /* */ comments and trailing commas from a JSON
// document, leaving something encoding/json will accept.
//
// Comment openers inside string literals are left alone, which is the only
// place the scan has to be careful. Escapes are tracked for the same reason:
// "a\\" ends the string but "a\"" does not.
func StripJSONC(text string) string {
	var out strings.Builder
	out.Grow(len(text))

	// commaIndex is the position in `out` of the most recent comma that has
	// been written and not yet followed by a value; -1 when there is none. A
	// trailing comma is one immediately followed by '}' or ']'.
	commaIndex := -1

	inString := false
	escaped := false

	for i := 0; i < len(text); i++ {
		c := text[i]

		if inString {
			out.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}

		switch {
		case c == '"':
			inString = true
			commaIndex = -1
			out.WriteByte(c)

		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			// Line comment: skip to the end of the line, leaving the newline so
			// nothing runs together.
			for i < len(text) && text[i] != '\n' {
				i++
			}
			i--

		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			// Block comment: skip to the closing delimiter. An unterminated one
			// runs to the end, which leaves a document encoding/json rejects --
			// the same outcome as jsonc-parser reporting an error.
			i += 2
			for i+1 < len(text) && !(text[i] == '*' && text[i+1] == '/') {
				i++
			}
			i++

		case c == ',':
			commaIndex = out.Len()
			out.WriteByte(c)

		case c == '}' || c == ']':
			if commaIndex >= 0 {
				// Drop the trailing comma. Rebuilding the string is fine here:
				// config files are small and this happens at most once per
				// closing brace.
				current := out.String()
				out.Reset()
				out.WriteString(current[:commaIndex])
				out.WriteString(current[commaIndex+1:])
			}
			commaIndex = -1
			out.WriteByte(c)

		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			// Whitespace does not end a pending trailing comma.
			out.WriteByte(c)

		default:
			commaIndex = -1
			out.WriteByte(c)
		}
	}

	return out.String()
}

// ParseJSONC parses a JSON document that may contain comments and trailing
// commas. The result is the `any` shape encoding/json produces: map[string]any,
// []any, string, float64, bool or nil.
func ParseJSONC(text string) (any, error) {
	var value any
	if err := json.Unmarshal([]byte(StripJSONC(text)), &value); err != nil {
		return nil, err
	}
	return value, nil
}
