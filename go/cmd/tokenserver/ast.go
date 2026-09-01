/*
 * ast.go
 *
 * Serialization of the Go parser's AST for the TypeScript bridge.
 *
 * The shape produced here mirrors what `JSON.stringify` gives for pyright's own
 * parse nodes: `{nodeType, start, length, d: {...}}`, with `d` field names
 * lower-camel-cased from the Go field names. Node IDs and parent pointers are
 * deliberately left out -- IDs are allocation-order counters that carry no
 * meaning across implementations, and parents would make the structure cyclic.
 *
 * Serialization is reflective rather than a 78-case switch. The point of this
 * dump is to be compared against the TypeScript, so a hand-written serializer
 * would just be one more place for the two to drift apart silently; reflection
 * cannot omit a field that exists.
 */

package main

import (
	"math/big"
	"reflect"
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// parseResponse is what the `parse` op returns.
type parseResponse struct {
	ParseTree   any               `json:"parseTree"`
	Diagnostics []diagnosticJSON  `json:"diagnostics"`
	Imports     []moduleImportSON `json:"importedModules"`
	Future      []string          `json:"futureImports"`
	Wildcard    bool              `json:"containsWildcardImport"`
	Annotations bool              `json:"hasTypeAnnotations"`
	Aliases     map[string]string `json:"typingSymbolAliases"`
}

type diagnosticJSON struct {
	Category int    `json:"category"`
	Message  string `json:"message"`
	Start    [2]int `json:"start"`
	End      [2]int `json:"end"`
}

type moduleImportSON struct {
	LeadingDots int      `json:"leadingDots"`
	NameParts   []string `json:"nameParts"`
	Symbols     []string `json:"importedSymbols"`
}

// nodeTokenJSON is how a Token stored in a node's `d` crosses the wire. Only
// the discriminating fields are included: the tokenizer differential already
// compares every token field, so repeating them here would only slow the AST
// comparison down.
type nodeTokenJSON struct {
	Type   int `json:"type"`
	Start  int `json:"start"`
	Length int `json:"length"`
}

var (
	parseNodeType  = reflect.TypeOf((*parser.ParseNode)(nil)).Elem()
	tokenType      = reflect.TypeOf((*parser.Token)(nil)).Elem()
	textType       = reflect.TypeOf(common.Text{})
	numberValType  = reflect.TypeOf(parser.NumberValue{})
	bigIntPtrType  = reflect.TypeOf((*big.Int)(nil))
	textRangeType  = reflect.TypeOf(common.TextRange{})
	stringSliceTyp = reflect.TypeOf([]string{})
)

// lowerFirst maps a Go field name onto the TypeScript property name. Every
// field in parseNodes.go was named by upper-casing the first letter of the
// TypeScript name, so undoing that is the whole mapping -- except for `ID`,
// which Go style capitalizes fully.
func lowerFirst(name string) string {
	if name == "ID" {
		return "id"
	}
	return strings.ToLower(name[:1]) + name[1:]
}

// serializeNode converts a parse node to the JSON shape described above.
// It returns nil for a nil node so the field is omitted, matching `undefined`.
func serializeNode(node parser.ParseNode) any {
	if node == nil {
		return nil
	}
	v := reflect.ValueOf(node)
	if v.Kind() == reflect.Ptr && v.IsNil() {
		return nil
	}

	base := node.NodeBase()
	out := map[string]any{
		"nodeType": int(node.GetNodeType()),
		"start":    base.Start,
		"length":   base.Length,
	}

	elem := v.Elem()
	details := elem.FieldByName("D")
	if details.IsValid() {
		out["d"] = serializeDetails(details)
	} else {
		// A node with no extra fields still carries an empty `d` in the
		// TypeScript, so emit one.
		out["d"] = map[string]any{}
	}

	return out
}

func serializeDetails(details reflect.Value) map[string]any {
	out := map[string]any{}
	t := details.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue // unexported
		}
		value := serializeValue(details.Field(i))
		if value == nil {
			// Omitted, matching an `undefined` property.
			continue
		}
		out[lowerFirst(field.Name)] = value
	}
	return out
}

// stringsAsText makes the serializer emit strings as JSON strings rather than
// as UTF-16 code unit arrays. The corpus differential leaves it off, because
// code units are the only representation that survives every input unchanged.
// The test bridge turns it on: it has to hand real JavaScript strings back to
// pyright's own test code, and a code-unit array would not be one.
var stringsAsText bool

func encodeString(s string) any {
	if stringsAsText {
		return s
	}
	return []uint16(common.NewText(s))
}

func serializeValue(v reflect.Value) any {
	t := v.Type()

	switch {
	case t == textType:
		text := v.Interface().(common.Text)
		if stringsAsText {
			return text.String()
		}
		return []uint16(text)

	case t == numberValType:
		nv := v.Interface().(parser.NumberValue)
		if nv.IsBigInt {
			s := nv.BigInt.String()
			return numberJSON{Big: &s}
		}
		s := formatJSNumber(nv.Float)
		return numberJSON{Num: &s}

	case t == textRangeType:
		r := v.Interface().(common.TextRange)
		return [2]int{r.Start, r.Length}

	case t == bigIntPtrType:
		if v.IsNil() {
			return nil
		}
		return v.Interface().(*big.Int).String()

	case t == stringSliceTyp:
		return v.Interface().([]string)
	}

	// A node (concrete pointer or union interface).
	if t.Implements(parseNodeType) {
		if v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
			if v.IsNil() {
				return nil
			}
		}
		return serializeNode(v.Interface().(parser.ParseNode))
	}

	// A token. Checked after ParseNode because no type implements both.
	if t.Implements(tokenType) {
		if v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
			if v.IsNil() {
				return nil
			}
		}
		tok := v.Interface().(parser.Token)
		r := tok.GetRange()
		return nodeTokenJSON{Type: int(tok.GetType()), Start: r.Start, Length: r.Length}
	}

	switch v.Kind() {
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	case reflect.String:
		return encodeString(v.String())

	case reflect.Ptr:
		if v.IsNil() {
			return nil
		}
		return serializeValue(v.Elem())

	case reflect.Slice:
		// A nil slice is `undefined` in the TypeScript; an empty one is `[]`.
		if v.IsNil() {
			return nil
		}
		out := make([]any, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			out = append(out, serializeValue(v.Index(i)))
		}
		return out

	case reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return serializeValue(v.Elem())
	}

	return nil
}

func handleParse(req *request) (any, string) {
	stringsAsText = req.StringsAsText
	text := common.Text(req.Text)

	options := parser.NewParseOptions()
	if req.IsStubFile {
		options.IsStubFile = true
	}
	if req.PythonVersion != "" {
		version := common.PythonVersionFromString(req.PythonVersion)
		if version == nil {
			return nil, "unrecognized pythonVersion: " + req.PythonVersion
		}
		options.PythonVersion = *version
	}
	options.UseNotebookMode = req.UseNotebookMode
	options.ReportErrorsForParsedStringContents = req.ReportErrorsForParsedStringContents

	sink := common.NewDiagnosticSink()
	p := parser.NewParser()
	results := p.ParseSourceFile(text, options, sink)

	diagnostics := []diagnosticJSON{}
	for _, diag := range sink.FetchAndClear() {
		diagnostics = append(diagnostics, diagnosticJSON{
			Category: int(diag.Category),
			Message:  diag.Message,
			Start:    [2]int{diag.Range.Start.Line, diag.Range.Start.Character},
			End:      [2]int{diag.Range.End.Line, diag.Range.End.Character},
		})
	}

	imports := []moduleImportSON{}
	for _, imp := range results.ParserOutput.ImportedModules {
		var symbols []string
		if imp.ImportedSymbols != nil {
			symbols = sortedKeys(imp.ImportedSymbols)
		}
		imports = append(imports, moduleImportSON{
			LeadingDots: imp.LeadingDots,
			NameParts:   imp.NameParts,
			Symbols:     symbols,
		})
	}

	return parseResponse{
		ParseTree:   serializeNode(results.ParserOutput.ParseTree),
		Diagnostics: diagnostics,
		Imports:     imports,
		Future:      sortedKeys(results.ParserOutput.FutureImports),
		Wildcard:    results.ParserOutput.ContainsWildcardImport,
		Annotations: results.ParserOutput.HasTypeAnnotations,
		Aliases:     results.ParserOutput.TypingSymbolAliases,
	}, ""
}

// sortedKeys makes set contents order-independent for comparison; the
// TypeScript side sorts too.
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
