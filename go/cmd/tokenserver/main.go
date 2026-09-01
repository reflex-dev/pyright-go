/*
 * tokenserver
 *
 * A thin JSON bridge over the Go tokenizer, so pyright's own TypeScript test
 * suite can be run against this port instead of the tests being transliterated.
 * The Node harness in tools/ts-bridge shims parser/tokenizer.ts and
 * parser/stringTokenUtils.ts to talk to this process, then runs the unmodified
 * src/tests/*.test.ts files.
 *
 * Protocol: one JSON request per line on stdin, one JSON response per line on
 * stdout. Strings crossing the boundary are carried as UTF-16 code unit arrays
 * so nothing is lost to UTF-8 round-tripping.
 */

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

type request struct {
	Op                string   `json:"op"`
	Text              []uint16 `json:"text"`
	Start             *int     `json:"start"`
	Length            *int     `json:"length"`
	InitialParenDepth int      `json:"initialParenDepth"`
	UseNotebookMode   bool     `json:"useNotebookMode"`

	// unescape
	Flags        int      `json:"flags"`
	EscapedValue []uint16 `json:"escapedValue"`
	ElideCrlf    *bool    `json:"elideCrlf"`

	// parse
	StringsAsText                       bool   `json:"stringsAsText"`
	IsStubFile                          bool   `json:"isStubFile"`
	PythonVersion                       string `json:"pythonVersion"`
	ReportErrorsForParsedStringContents bool   `json:"reportErrorsForParsedStringContents"`

	// binder
	ImportsResolve bool `json:"importsResolve"`

	// symbolnameutils
	Name string `json:"name"`

	// statics
	Which               string `json:"which"`
	OperatorType        int    `json:"operatorType"`
	TokenType           int    `json:"tokenType"`
	IncludeSoftKeywords bool   `json:"includeSoftKeywords"`

	// types
	Payload json.RawMessage `json:"payload"`
}

type commentJSON struct {
	Type   int      `json:"type"`
	Start  int      `json:"start"`
	Length int      `json:"length"`
	Value  []uint16 `json:"value"`
}

type tokenJSON struct {
	Type   int `json:"type"`
	Start  int `json:"start"`
	Length int `json:"length"`

	Comments []commentJSON `json:"comments,omitempty"`

	Value        *string     `json:"value,omitempty"`
	KeywordType  *int        `json:"keywordType,omitempty"`
	OperatorType *int        `json:"operatorType,omitempty"`
	NewLineType  *int        `json:"newLineType,omitempty"`
	Flags        *int        `json:"flags,omitempty"`
	EscapedValue *[]uint16   `json:"escapedValue,omitempty"`
	PrefixLength *int        `json:"prefixLength,omitempty"`
	QuoteMark    *int        `json:"quoteMarkLength,omitempty"`
	IsInteger    *bool       `json:"isInteger,omitempty"`
	IsImaginary  *bool       `json:"isImaginary,omitempty"`
	NumberValue  *numberJSON `json:"numberValue,omitempty"`
	IndentAmount *int        `json:"indentAmount,omitempty"`
	Ambiguous    *bool       `json:"ambiguous,omitempty"`
	MatchesInd   *bool       `json:"matchesIndent,omitempty"`
}

// numberJSON carries the `number | bigint` union across the wire. Big is a
// decimal string so arbitrary-precision integers survive. Num is the raw IEEE
// 754 bit pattern in hex rather than a decimal rendering, so the comparison
// against JavaScript is exact and does not depend on either language's
// float-formatting rules; it also distinguishes -0 from 0.
type numberJSON struct {
	Big *string `json:"big,omitempty"`
	Num *string `json:"num,omitempty"`
}

// formatJSNumber renders a double as its 64-bit big-endian pattern in hex.
func formatJSNumber(v float64) string {
	return fmt.Sprintf("%016x", math.Float64bits(v))
}

type ignoreRuleJSON struct {
	Text  []uint16 `json:"text"`
	Range [2]int   `json:"range"`
}

type ignoreCommentJSON struct {
	Range [2]int            `json:"range"`
	Rules *[]ignoreRuleJSON `json:"rules"`
}

type tokenizeResponse struct {
	Tokens                          []tokenJSON                  `json:"tokens"`
	Lines                           [][2]int                     `json:"lines"`
	PredominantEndOfLineSequence    string                       `json:"eol"`
	HasPredominantTabSequence       bool                         `json:"hasTab"`
	PredominantTabSequence          string                       `json:"tab"`
	PredominantSingleQuoteCharacter string                       `json:"quote"`
	TypeIgnoreAll                   *ignoreCommentJSON           `json:"typeIgnoreAll"`
	TypeIgnoreLines                 map[string]ignoreCommentJSON `json:"typeIgnoreLines"`
	PyrightIgnoreLines              map[string]ignoreCommentJSON `json:"pyrightIgnoreLines"`
}

type unescapeResponse struct {
	Value           []uint16 `json:"value"`
	NonAsciiInBytes bool     `json:"nonAsciiInBytes"`
	Errors          []struct {
		Offset    int `json:"offset"`
		Length    int `json:"length"`
		ErrorType int `json:"errorType"`
	} `json:"unescapeErrors"`
}

func intPtr(v int) *int       { return &v }
func boolPtr(v bool) *bool    { return &v }
func strPtr(v string) *string { return &v }

func convertComments(comments []*parser.Comment) []commentJSON {
	if len(comments) == 0 {
		return nil
	}
	out := make([]commentJSON, 0, len(comments))
	for _, c := range comments {
		out = append(out, commentJSON{
			Type:   int(c.Type),
			Start:  c.Start,
			Length: c.Length,
			Value:  []uint16(c.Value),
		})
	}
	return out
}

func convertToken(tok parser.Token) tokenJSON {
	r := tok.GetRange()
	entry := tokenJSON{
		Type:     int(tok.GetType()),
		Start:    r.Start,
		Length:   r.Length,
		Comments: convertComments(tok.GetComments()),
	}

	switch t := tok.(type) {
	case *parser.IdentifierToken:
		entry.Value = strPtr(t.Value)
	case *parser.KeywordToken:
		entry.KeywordType = intPtr(int(t.KeywordType))
	case *parser.OperatorToken:
		entry.OperatorType = intPtr(int(t.OperatorType))
	case *parser.NewLineToken:
		entry.NewLineType = intPtr(int(t.NewLineType))
	case *parser.NumberToken:
		entry.IsInteger = boolPtr(t.IsInteger)
		entry.IsImaginary = boolPtr(t.IsImaginary)
		if t.Value.IsBigInt {
			entry.NumberValue = &numberJSON{Big: strPtr(t.Value.BigInt.String())}
		} else {
			entry.NumberValue = &numberJSON{Num: strPtr(formatJSNumber(t.Value.Float))}
		}
	case *parser.StringToken:
		entry.Flags = intPtr(int(t.Flags))
		ev := []uint16(t.EscapedValue)
		if ev == nil {
			ev = []uint16{}
		}
		entry.EscapedValue = &ev
		entry.PrefixLength = intPtr(t.PrefixLength)
		entry.QuoteMark = intPtr(t.QuoteMarkLength)
	case *parser.FStringStartToken:
		entry.Flags = intPtr(int(t.Flags))
		entry.PrefixLength = intPtr(t.PrefixLength)
		entry.QuoteMark = intPtr(t.QuoteMarkLength)
	case *parser.FStringMiddleToken:
		entry.Flags = intPtr(int(t.Flags))
		ev := []uint16(t.EscapedValue)
		if ev == nil {
			ev = []uint16{}
		}
		entry.EscapedValue = &ev
	case *parser.FStringEndToken:
		entry.Flags = intPtr(int(t.Flags))
	case *parser.IndentToken:
		entry.IndentAmount = intPtr(t.IndentAmount)
		entry.Ambiguous = boolPtr(t.IsIndentAmbiguous)
	case *parser.DedentToken:
		entry.IndentAmount = intPtr(t.IndentAmount)
		entry.Ambiguous = boolPtr(t.IsDedentAmbiguous)
		entry.MatchesInd = boolPtr(t.MatchesIndent)
	}

	return entry
}

func convertIgnore(c *parser.IgnoreComment) *ignoreCommentJSON {
	if c == nil {
		return nil
	}
	out := &ignoreCommentJSON{Range: [2]int{c.Range.Start, c.Range.Length}}
	if c.RulesList != nil {
		rules := make([]ignoreRuleJSON, 0, len(c.RulesList))
		for _, rule := range c.RulesList {
			rules = append(rules, ignoreRuleJSON{
				Text:  []uint16(rule.Text),
				Range: [2]int{rule.Range.Start, rule.Range.Length},
			})
		}
		out.Rules = &rules
	}
	return out
}

func handleTokenize(req *request) (resp any, err string) {
	defer func() {
		if r := recover(); r != nil {
			resp = nil
			err = fmt.Sprint(r)
		}
	}()

	text := common.Text(req.Text)
	start := 0
	if req.Start != nil {
		start = *req.Start
	}
	length := text.Length()
	if req.Length != nil {
		length = *req.Length
	}

	t := parser.NewTokenizer()
	out := t.TokenizeRange(text, start, length, req.InitialParenDepth, req.UseNotebookMode)

	tokens := make([]tokenJSON, 0, out.Tokens.Count())
	for i := 0; i < out.Tokens.Count(); i++ {
		tokens = append(tokens, convertToken(out.Tokens.GetItemAt(i)))
	}

	lines := make([][2]int, 0, out.Lines.Count())
	for i := 0; i < out.Lines.Count(); i++ {
		ln := out.Lines.GetItemAt(i)
		lines = append(lines, [2]int{ln.Start, ln.Length})
	}

	typeIgnoreLines := map[string]ignoreCommentJSON{}
	for k, v := range out.TypeIgnoreLines {
		typeIgnoreLines[fmt.Sprint(k)] = *convertIgnore(v)
	}
	pyrightIgnoreLines := map[string]ignoreCommentJSON{}
	for k, v := range out.PyrightIgnoreLines {
		pyrightIgnoreLines[fmt.Sprint(k)] = *convertIgnore(v)
	}

	return &tokenizeResponse{
		Tokens:                          tokens,
		Lines:                           lines,
		PredominantEndOfLineSequence:    out.PredominantEndOfLineSequence,
		HasPredominantTabSequence:       out.HasPredominantTabSequence,
		PredominantTabSequence:          out.PredominantTabSequence,
		PredominantSingleQuoteCharacter: out.PredominantSingleQuoteCharacter,
		TypeIgnoreAll:                   convertIgnore(out.TypeIgnoreAll),
		TypeIgnoreLines:                 typeIgnoreLines,
		PyrightIgnoreLines:              pyrightIgnoreLines,
	}, ""
}

func handleUnescape(req *request) (resp any, errMsg string) {
	defer func() {
		if r := recover(); r != nil {
			resp = nil
			errMsg = fmt.Sprint(r)
		}
	}()

	elideCrlf := true
	if req.ElideCrlf != nil {
		elideCrlf = *req.ElideCrlf
	}

	token := parser.NewFStringMiddleToken(0, 0, parser.StringTokenFlags(req.Flags), common.Text(req.EscapedValue))
	result := parser.GetUnescapedStringEx(token, elideCrlf)

	out := &unescapeResponse{
		Value:           []uint16(result.Value),
		NonAsciiInBytes: result.NonAsciiInBytes,
	}
	if out.Value == nil {
		out.Value = []uint16{}
	}
	for _, e := range result.UnescapeErrors {
		out.Errors = append(out.Errors, struct {
			Offset    int `json:"offset"`
			Length    int `json:"length"`
			ErrorType int `json:"errorType"`
		}{Offset: e.Offset, Length: e.Length, ErrorType: int(e.ErrorType)})
	}
	if out.Errors == nil {
		out.Errors = []struct {
			Offset    int `json:"offset"`
			Length    int `json:"length"`
			ErrorType int `json:"errorType"`
		}{}
	}
	return out, ""
}

// handleStatics exposes the Tokenizer statics, so the shim reports what the Go
// port computes rather than delegating back to the TypeScript module.
func handleStatics(req *request) (resp any, errMsg string) {
	defer func() {
		if r := recover(); r != nil {
			resp = nil
			errMsg = fmt.Sprint(r)
		}
	}()

	switch req.Which {
	case "getOperatorInfo":
		return int(parser.GetOperatorInfo(parser.OperatorType(req.OperatorType))), ""
	case "isPythonKeyword":
		return parser.IsPythonKeyword(common.Text(req.Text).String(), req.IncludeSoftKeywords), ""
	case "isPythonIdentifier":
		return parser.IsPythonIdentifier(common.Text(req.Text)), ""
	case "isOperatorAssignment":
		return parser.IsOperatorAssignment(parser.OperatorType(req.OperatorType)), ""
	case "isOperatorComparison":
		return parser.IsOperatorComparison(parser.OperatorType(req.OperatorType)), ""
	case "isWhitespace":
		return req.TokenType == int(parser.TokenTypeNewLine) ||
			req.TokenType == int(parser.TokenTypeIndent) ||
			req.TokenType == int(parser.TokenTypeDedent), ""
	}
	return nil, "unknown static: " + req.Which
}

func main() {
	reader := bufio.NewReaderSize(os.Stdin, 1<<20)
	writer := bufio.NewWriterSize(os.Stdout, 1<<20)
	decoder := json.NewDecoder(reader)
	encoder := json.NewEncoder(writer)

	for {
		var req request
		if err := decoder.Decode(&req); err != nil {
			break
		}

		var result any
		var errMsg string
		switch req.Op {
		case "tokenize":
			result, errMsg = handleTokenize(&req)
		case "unescape":
			result, errMsg = handleUnescape(&req)
		case "statics":
			result, errMsg = handleStatics(&req)
		case "parse":
			result, errMsg = handleParse(&req)
		case "types":
			result, errMsg = handleTypes(req.Payload)
		case "parsetreeutils":
			result, errMsg = handleParseTreeUtils(&req)
		case "binder":
			result, errMsg = handleBinder(&req)
		case "symbolnameutils":
			result, errMsg = handleSymbolNameUtils(&req)
		case "typecacheutils":
			result, errMsg = handleTypeCacheUtils(req.Payload)
		default:
			errMsg = "unknown op: " + req.Op
		}

		envelope := map[string]any{}
		if errMsg != "" {
			envelope["error"] = errMsg
		} else {
			envelope["result"] = result
		}
		if err := encoder.Encode(envelope); err != nil {
			fmt.Fprintln(os.Stderr, "encode error:", err)
			os.Exit(1)
		}
		writer.Flush()
	}
}
