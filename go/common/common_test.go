package common

import "testing"

// The TypeScript-against-Go bridge covers the tokenizer, but nothing in
// pyright's test suite exercises these helpers directly, so they are tested
// here against the semantics of the TypeScript originals.

func TestTextIsUTF16(t *testing.T) {
	// The whole port depends on offsets being UTF-16 code unit offsets rather
	// than byte or rune offsets.
	text := NewText("aé😀b")

	// 'a', 'é', surrogate pair for U+1F600, 'b' == 5 code units.
	if got := text.Length(); got != 5 {
		t.Errorf("Length() = %d, want 5 (UTF-16 code units)", got)
	}
	if got := text.CharCodeAt(1); got != 0x00e9 {
		t.Errorf("CharCodeAt(1) = %#x, want 0xe9", got)
	}
	if got := text.CharCodeAt(2); got != 0xd83d {
		t.Errorf("CharCodeAt(2) = %#x, want the lead surrogate 0xd83d", got)
	}
	if got := text.CharCodeAt(3); got != 0xde00 {
		t.Errorf("CharCodeAt(3) = %#x, want the trail surrogate 0xde00", got)
	}
	if got := text.CharCodeAt(4); got != 'b' {
		t.Errorf("CharCodeAt(4) = %#x, want 'b'", got)
	}

	if got := text.String(); got != "aé😀b" {
		t.Errorf("round trip = %q", got)
	}
}

func TestCharCodeAtOutOfRange(t *testing.T) {
	text := NewText("ab")
	if got := text.CharCodeAt(-1); got != 0 {
		t.Errorf("CharCodeAt(-1) = %d, want 0", got)
	}
	if got := text.CharCodeAt(2); got != 0 {
		t.Errorf("CharCodeAt(2) = %d, want 0", got)
	}
}

func TestSubstringClamps(t *testing.T) {
	text := NewText("hello")
	if got := text.Substring(1, 3).String(); got != "el" {
		t.Errorf("Substring(1,3) = %q, want %q", got, "el")
	}
	if got := text.Substring(3, 1).Length(); got != 0 {
		t.Errorf("an inverted range should be empty, got %d units", got)
	}
	if got := text.Substring(0, 99).String(); got != "hello" {
		t.Errorf("Substring past the end = %q", got)
	}
}

func TestTextBuilderSurrogatePairs(t *testing.T) {
	var b TextBuilder
	b.WriteCodePoint(0x1f600)
	if b.Len() != 2 {
		t.Errorf("an astral code point should occupy 2 code units, got %d", b.Len())
	}
	b.Reset()
	b.WriteChar(0xf600)
	if b.Len() != 1 {
		t.Errorf("a BMP code unit should occupy 1 code unit, got %d", b.Len())
	}
}

func TestTextRangeOperations(t *testing.T) {
	r := NewTextRange(5, 10)
	if r.End() != 15 {
		t.Errorf("End() = %d, want 15", r.End())
	}
	if !r.Contains(5) || !r.Contains(14) || r.Contains(15) || r.Contains(4) {
		t.Error("Contains is wrong: the end offset is exclusive")
	}
	// Overlaps is inclusive of the end offset, unlike Contains.
	if !r.Overlaps(15) {
		t.Error("Overlaps should include the end offset")
	}

	if got := TextRangeFromBounds(2, 6); got.Start != 2 || got.Length != 4 {
		t.Errorf("TextRangeFromBounds(2,6) = %+v", got)
	}

	extended := NewTextRange(5, 5).Extend(NewTextRange(2, 2))
	if extended.Start != 2 || extended.End() != 10 {
		t.Errorf("Extend = %+v, want start 2 end 10", extended)
	}
}

func TestTextRangeFailsOnNegative(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected a negative start to fail, as debug.fail() throws")
		}
	}()
	NewTextRange(-1, 0)
}

func TestCombineTextRangesEmpty(t *testing.T) {
	if got := CombineTextRanges(nil); got != nil {
		t.Errorf("expected nil for an empty slice, got %+v", got)
	}
	got := CombineTextRanges([]TextRange{{Start: 10, Length: 2}, {Start: 1, Length: 3}})
	if got == nil || got.Start != 1 || got.End() != 12 {
		t.Errorf("CombineTextRanges = %+v", got)
	}
}

func TestTextRangeCollectionLookup(t *testing.T) {
	lines := NewTextRangeCollection([]TextRange{
		{Start: 0, Length: 5},
		{Start: 5, Length: 4},
		{Start: 9, Length: 3},
	})

	if lines.Count() != 3 {
		t.Fatalf("Count() = %d", lines.Count())
	}
	if lines.Start() != 0 || lines.End() != 12 || lines.Length() != 12 {
		t.Errorf("bounds wrong: start %d end %d length %d", lines.Start(), lines.End(), lines.Length())
	}

	for _, tt := range []struct{ pos, want int }{{0, 0}, {4, 0}, {5, 1}, {8, 1}, {9, 2}, {11, 2}} {
		if got := lines.GetItemContaining(tt.pos); got != tt.want {
			t.Errorf("GetItemContaining(%d) = %d, want %d", tt.pos, got, tt.want)
		}
	}

	if got := lines.GetItemContaining(-1); got != -1 {
		t.Errorf("GetItemContaining(-1) = %d, want -1", got)
	}
	if !lines.Contains(11) || lines.Contains(12) {
		t.Error("Contains should treat the end offset as exclusive")
	}
}

func TestTextRangeCollectionGetItemAtOutOfRange(t *testing.T) {
	lines := NewTextRangeCollection([]TextRange{{Start: 0, Length: 1}})
	defer func() {
		if recover() == nil {
			t.Error("expected an out-of-range index to fail")
		}
	}()
	lines.GetItemAt(5)
}

func TestConvertOffsetToPosition(t *testing.T) {
	lines := NewTextRangeCollection([]TextRange{
		{Start: 0, Length: 6}, // "line1\n"
		{Start: 6, Length: 6}, // "line2\n"
		{Start: 12, Length: 5},
	})

	for _, tt := range []struct {
		offset int
		line   int
		char   int
	}{
		{0, 0, 0}, {5, 0, 5}, {6, 1, 0}, {11, 1, 5}, {12, 2, 0}, {16, 2, 4},
		// Past the end clamps to the last line.
		{99, 2, 5},
	} {
		got := ConvertOffsetToPosition(tt.offset, lines)
		if got.Line != tt.line || got.Character != tt.char {
			t.Errorf("ConvertOffsetToPosition(%d) = %+v, want line %d char %d", tt.offset, got, tt.line, tt.char)
		}
	}
}

func TestConvertOffsetToPositionEmptyFile(t *testing.T) {
	empty := NewTextRangeCollection([]TextRange{})
	if got := ConvertOffsetToPosition(0, empty); got.Line != 0 || got.Character != 0 {
		t.Errorf("empty file should map to (0,0), got %+v", got)
	}
}

func TestConvertOffsetsToRangeFastPath(t *testing.T) {
	lines := NewTextRangeCollection([]TextRange{
		{Start: 0, Length: 6},
		{Start: 6, Length: 6},
	})

	// Same-line range takes the fast path; it must agree with the slow path.
	got := ConvertOffsetsToRange(1, 4, lines)
	want := Range{Start: Position{0, 1}, End: Position{0, 4}}
	if !RangesAreEqual(got, want) {
		t.Errorf("same-line range = %v, want %v", got, want)
	}

	// Cross-line range takes the general path.
	got = ConvertOffsetsToRange(1, 8, lines)
	want = Range{Start: Position{0, 1}, End: Position{1, 2}}
	if !RangesAreEqual(got, want) {
		t.Errorf("cross-line range = %v, want %v", got, want)
	}
}

func TestConvertPositionToOffsetOutOfRange(t *testing.T) {
	lines := NewTextRangeCollection([]TextRange{{Start: 0, Length: 6}})
	if _, ok := ConvertPositionToOffset(Position{Line: 5}, lines); ok {
		t.Error("expected an out-of-range line to report not-ok")
	}
	if got, ok := ConvertPositionToOffset(Position{Line: 0, Character: 3}, lines); !ok || got != 3 {
		t.Errorf("ConvertPositionToOffset = %d, %v", got, ok)
	}
}

func TestGetLineEndOffsetStripsNewline(t *testing.T) {
	text := NewText("abc\r\ndef")
	lines := NewTextRangeCollection([]TextRange{
		{Start: 0, Length: 5}, // "abc\r\n"
		{Start: 5, Length: 3}, // "def"
	})
	if got := GetLineEndOffsetInLines(lines, text, 0); got != 3 {
		t.Errorf("line 0 should end before the CRLF at offset 3, got %d", got)
	}
	if got := GetLineEndOffsetInLines(lines, text, 1); got != 8 {
		t.Errorf("line 1 should end at 8, got %d", got)
	}
}

func TestPythonVersionComparison(t *testing.T) {
	if !PythonVersion3_11.IsGreaterThan(PythonVersion3_9) {
		t.Error("3.11 > 3.9")
	}
	if PythonVersion3_9.IsGreaterThan(PythonVersion3_11) {
		t.Error("3.9 is not > 3.11")
	}
	if !PythonVersion3_10.IsEqualTo(PythonVersion3_10) {
		t.Error("3.10 == 3.10")
	}
	if !PythonVersion3_9.IsLessThan(PythonVersion3_10) {
		t.Error("3.9 < 3.10")
	}
	if !PythonVersion3_10.IsGreaterOrEqualTo(PythonVersion3_10) {
		t.Error("3.10 >= 3.10")
	}
}

func TestPythonVersionUndefinedMicroCompares(t *testing.T) {
	// An absent micro version makes the versions compare equal, and makes
	// IsGreaterThan false -- this is the behavior of the `=== undefined`
	// branches in the original, which is why Micro is a pointer.
	micro := 3
	withMicro := NewPythonVersion(3, 12, &micro, nil, nil)
	withoutMicro := PythonVersion3_12

	if !withMicro.IsEqualTo(withoutMicro) {
		t.Error("a missing micro should compare equal")
	}
	if withMicro.IsGreaterThan(withoutMicro) {
		t.Error("a missing micro on either side makes IsGreaterThan false")
	}
}

func TestPythonVersionFromString(t *testing.T) {
	if got := PythonVersionFromString("3"); got != nil {
		t.Errorf("a single component should not parse, got %v", got)
	}
	if got := PythonVersionFromString("x.y"); got != nil {
		t.Errorf("non-numeric components should not parse, got %v", got)
	}

	got := PythonVersionFromString("3.12.4.candidate.2")
	if got == nil {
		t.Fatal("expected 3.12.4.candidate.2 to parse")
	}
	if got.Major != 3 || got.Minor != 12 || got.Micro == nil || *got.Micro != 4 {
		t.Errorf("parsed %+v", got)
	}
	if got.ReleaseLevel == nil || *got.ReleaseLevel != PythonReleaseLevelCandidate {
		t.Errorf("release level = %v", got.ReleaseLevel)
	}
	if got.Serial == nil || *got.Serial != 2 {
		t.Errorf("serial = %v", got.Serial)
	}
	if s := got.String(); s != "3.12.4.candidate.2" {
		t.Errorf("String() = %q", s)
	}

	// An unrecognized release level is dropped rather than rejected.
	if got := PythonVersionFromString("3.12.4.bogus"); got == nil || got.ReleaseLevel != nil {
		t.Errorf("an unknown release level should be dropped, got %+v", got)
	}
}

func TestHashStringMatchesJavaScript(t *testing.T) {
	// Values produced by the TypeScript hashString for the same inputs; the
	// `| 0` truncation to int32 is the part that is easy to get wrong.
	cases := map[string]int32{
		"":    0,
		"a":   97,
		"abc": 96354,
		"pyright": func() int32 {
			var h int32
			for _, c := range "pyright" {
				h = (h << 5) - h + int32(c)
			}
			return h
		}(),
	}
	for input, want := range cases {
		if got := HashString(input); got != want {
			t.Errorf("HashString(%q) = %d, want %d", input, got, want)
		}
	}

	// A long string must wrap rather than grow without bound.
	long := ""
	for i := 0; i < 100; i++ {
		long += "abcdefghij"
	}
	_ = HashString(long) // must not panic or exceed int32
}

func TestDiagnosticSinkDeduplicates(t *testing.T) {
	sink := NewDiagnosticSink()
	r := Range{Start: Position{1, 2}, End: Position{1, 5}}

	sink.AddError("same message", r)
	sink.AddError("same message", r)
	sink.AddError("different message", r)

	if got := len(sink.GetErrors()); got != 2 {
		t.Errorf("expected duplicates to collapse, got %d errors", got)
	}
}

func TestDiagnosticAddendumNesting(t *testing.T) {
	root := NewDiagnosticAddendum()
	if !root.IsEmpty() {
		t.Error("a fresh addendum is empty")
	}

	root.AddMessage("outer")
	child := root.CreateAddendum()
	child.AddMessage("inner")

	if root.IsEmpty() {
		t.Error("an addendum with messages is not empty")
	}
	if got := child.GetNestLevel(); got != 1 {
		t.Errorf("nest level = %d, want 1", got)
	}

	// Each level of messages adds two non-breaking spaces (U+00A0) of indent.
	// The original writes them literally in diagnostic.ts; they are not
	// ordinary spaces, and the trailing "  ..." line that GetString appends
	// when the line cap is hit really does use ordinary spaces.
	if got := root.GetString(); got != "\n  outer\n    inner" {
		t.Errorf("GetString() = %q", got)
	}
}

func TestDiagnosticAddendumLineCap(t *testing.T) {
	root := NewDiagnosticAddendum()
	for i := 0; i < 20; i++ {
		root.AddMessage("line")
	}
	got := root.GetStringWithLimits(DefaultMaxDiagnosticDepth, 3)
	want := "\n  line\n  line\n  line\n  ..."
	if got != want {
		t.Errorf("GetStringWithLimits = %q, want %q", got, want)
	}
}

func TestConvertLevelToCategory(t *testing.T) {
	if ConvertLevelToCategory(DiagnosticLevelError) != DiagnosticCategoryError {
		t.Error("error level")
	}
	defer func() {
		if recover() == nil {
			t.Error("an unexpected level should throw, as in the original")
		}
	}()
	ConvertLevelToCategory(DiagnosticLevelNone)
}

// TestComparisonHelpers pins core.ts's compareComparableValues semantics,
// including the `undefined` ordering: undefined sorts before every defined
// value, and two undefineds are equal.
func TestComparisonHelpers(t *testing.T) {
	str := func(s string) *string { return &s }
	num := func(n int) *int { return &n }

	cases := []struct {
		a, b *string
		want Comparison
	}{
		{nil, nil, ComparisonEqualTo},
		{nil, str("a"), ComparisonLessThan},
		{str("a"), nil, ComparisonGreaterThan},
		{str("a"), str("a"), ComparisonEqualTo},
		{str("a"), str("b"), ComparisonLessThan},
		{str("b"), str("a"), ComparisonGreaterThan},
		{str("A"), str("a"), ComparisonLessThan},
	}
	for _, c := range cases {
		if got := CompareComparableStrings(c.a, c.b); got != c.want {
			t.Errorf("CompareComparableStrings(%v, %v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}

	if got := CompareValues(nil, nil); got != ComparisonEqualTo {
		t.Errorf("CompareValues(nil, nil) = %d", got)
	}
	if got := CompareValues(nil, num(0)); got != ComparisonLessThan {
		t.Errorf("CompareValues(nil, 0) = %d", got)
	}
	if got := CompareValues(num(2), num(10)); got != ComparisonLessThan {
		t.Errorf("CompareValues(2, 10) = %d", got)
	}

	if ToLowerCase("AbC") != "abc" {
		t.Error("ToLowerCase")
	}
}

// TestContainsOnlyWhitespace pins the JavaScript \s class, which is not the
// same set as Go's unicode.IsSpace: U+FEFF counts as whitespace to /^\s*$/.
func TestContainsOnlyWhitespace(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"   ", true},
		{"\t\n\r\v\f ", true},
		{"\u00a0\u1680\u2000\u200a\u2028\u2029\u202f\u205f\u3000\ufeff", true},
		{" x ", false},
		{"\u200b", false}, // zero-width space is not \s
	}
	for _, c := range cases {
		text := NewText(c.in)
		if got := ContainsOnlyWhitespace(text, 0, len(text)); got != c.want {
			t.Errorf("ContainsOnlyWhitespace(%q) = %v, want %v", c.in, got, c.want)
		}
	}

	// The start/end window is applied before the test.
	text := NewText("a   b")
	if !ContainsOnlyWhitespace(text, 1, 4) {
		t.Error("ContainsOnlyWhitespace should honor the start/end window")
	}
}
