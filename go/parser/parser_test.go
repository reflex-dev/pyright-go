package parser

import (
	"testing"

	"github.com/microsoft/pyright/go/common"
)

// The token cursor helpers drive every recursive-descent method, so their
// boundary behavior (what happens at end-of-stream, how far peeking clamps,
// which tokens are consumed on a failed match) is pinned here against a real
// token stream from the ported tokenizer.

func newParserFor(t *testing.T, source string) *Parser {
	t.Helper()
	p := NewParser()
	text := common.NewText(source)
	p.startNewParse(text, 0, text.Length(), NewParseOptions(), common.NewDiagnosticSink(), 0)
	return p
}

func TestPeekAndConsume(t *testing.T) {
	p := newParserFor(t, "if x:\n    pass\n")

	if kw, ok := p.peekKeywordType(); !ok || kw != KeywordTypeIf {
		t.Fatalf("expected to peek the `if` keyword, got %v ok=%v", kw, ok)
	}
	// Peeking must not advance.
	if kw, ok := p.peekKeywordType(); !ok || kw != KeywordTypeIf {
		t.Fatal("peeking advanced the cursor")
	}

	if !p.consumeTokenIfKeyword(KeywordTypeIf) {
		t.Fatal("expected to consume `if`")
	}
	if p.peekTokenType() != TokenTypeIdentifier {
		t.Fatalf("expected an identifier after `if`, got %v", p.peekTokenType())
	}

	// A failed match must not consume.
	if p.consumeTokenIfKeyword(KeywordTypeWhile) {
		t.Fatal("consumed a keyword that did not match")
	}
	if p.peekTokenType() != TokenTypeIdentifier {
		t.Fatal("a failed keyword match consumed a token")
	}
	if p.consumeTokenIfType(TokenTypeColon) {
		t.Fatal("consumed a token type that did not match")
	}
	if p.peekTokenType() != TokenTypeIdentifier {
		t.Fatal("a failed type match consumed a token")
	}
}

func TestPeekClampsAtBothEnds(t *testing.T) {
	p := newParserFor(t, "x\n")

	// Negative offsets clamp to the first token.
	first := p.peekToken(0)
	if p.peekToken(-5).GetRange() != first.GetRange() {
		t.Error("a negative peek should clamp to the first token")
	}

	// Offsets past the end clamp to the last token, which is end-of-stream.
	if p.peekToken(1000).GetType() != TokenTypeEndOfStream {
		t.Error("a peek past the end should clamp to the end-of-stream token")
	}
}

func TestGetNextTokenStopsAtEndOfStream(t *testing.T) {
	p := newParserFor(t, "x\n")

	// Drain the stream. getNextToken must not advance past the last token, so
	// this cannot run away.
	for i := 0; i < 100; i++ {
		p.getNextToken()
	}

	if !p.atEof() {
		t.Error("expected to be at end of stream")
	}
	if p.peekTokenType() != TokenTypeEndOfStream {
		t.Errorf("expected end-of-stream, got %v", p.peekTokenType())
	}
}

func TestAtEofIsTrueOnTheEndOfStreamToken(t *testing.T) {
	// atEof reports true while still pointing *at* the end-of-stream token,
	// not after consuming it.
	p := newParserFor(t, "")

	// An empty file tokenizes to an implied NewLine plus EndOfStream.
	if p.tokenCount != 2 {
		t.Fatalf("expected 2 implicit tokens, got %d", p.tokenCount)
	}
	if p.atEof() {
		t.Error("should not be at eof while pointing at the implied newline")
	}
	p.getNextToken()
	if !p.atEof() {
		t.Error("should be at eof while pointing at the end-of-stream token")
	}
}

func TestConsumeTokensUntilType(t *testing.T) {
	p := newParserFor(t, "a b c\nd\n")

	if !p.consumeTokensUntilType([]TokenType{TokenTypeNewLine}) {
		t.Fatal("expected to find a newline terminator")
	}
	if p.peekTokenType() != TokenTypeNewLine {
		t.Errorf("expected to stop on the newline, got %v", p.peekTokenType())
	}

	// With no reachable terminator it stops at end-of-stream and reports false.
	p2 := newParserFor(t, "a b c\n")
	if p2.consumeTokensUntilType([]TokenType{TokenTypeBacktick}) {
		t.Error("expected false when the terminator is never found")
	}
	if p2.peekTokenType() != TokenTypeEndOfStream {
		t.Errorf("expected to stop at end-of-stream, got %v", p2.peekTokenType())
	}
}

func TestGetTokenIfIdentifier(t *testing.T) {
	p := newParserFor(t, "value\n")
	tok := p.getTokenIfIdentifier()
	if tok == nil || tok.Value != "value" {
		t.Fatalf("expected the identifier `value`, got %v", tok)
	}

	// A hard keyword is not an identifier.
	p2 := newParserFor(t, "class\n")
	if got := p2.getTokenIfIdentifier(); got != nil {
		t.Errorf("a hard keyword must not convert to an identifier, got %q", got.Value)
	}
}

func TestGetTokenIfIdentifierConvertsSoftKeywords(t *testing.T) {
	// Soft keywords (match, case, type, lazy) convert to identifiers, taking
	// their text from the source rather than from the token.
	for _, kw := range []string{"match", "case", "type", "lazy"} {
		p := newParserFor(t, kw+"\n")
		tok := p.getTokenIfIdentifier()
		if tok == nil {
			t.Errorf("expected the soft keyword %q to convert to an identifier", kw)
			continue
		}
		if tok.Value != kw {
			t.Errorf("converted identifier value = %q, want %q", tok.Value, kw)
		}
		// The token must have been consumed.
		if p.peekTokenType() == TokenTypeKeyword {
			t.Errorf("the soft keyword %q was not consumed", kw)
		}
	}
}

func TestGetTokenIfIdentifierReportsInvalidChar(t *testing.T) {
	sink := common.NewDiagnosticSink()
	p := NewParser()
	text := common.NewText("$\n")
	p.startNewParse(text, 0, text.Length(), NewParseOptions(), sink, 0)

	tok := p.getTokenIfIdentifier()
	if tok == nil {
		t.Fatal("an invalid token should be treated as an empty identifier")
	}
	if tok.Value != "" {
		t.Errorf("expected an empty identifier value, got %q", tok.Value)
	}
	if len(sink.GetErrors()) != 1 {
		t.Fatalf("expected one syntax error, got %d", len(sink.GetErrors()))
	}
}

func TestSuppressErrors(t *testing.T) {
	sink := common.NewDiagnosticSink()
	p := NewParser()
	text := common.NewText("x\n")
	p.startNewParse(text, 0, text.Length(), NewParseOptions(), sink, 0)

	r := common.TextRange{Start: 0, Length: 1}

	p.suppressErrors(func() {
		p.addSyntaxError("suppressed", r)
	})
	if len(sink.GetErrors()) != 0 {
		t.Fatal("errors raised inside suppressErrors must be dropped")
	}

	// The flag is restored afterward.
	p.addSyntaxError("reported", r)
	if len(sink.GetErrors()) != 1 {
		t.Fatalf("expected the suppression to be lifted, got %d errors", len(sink.GetErrors()))
	}
}

func TestSuppressErrorsRestoresOnPanic(t *testing.T) {
	// The TypeScript version uses try/finally, so a throw still restores the
	// flag. A panic must do the same here.
	p := newParserFor(t, "x\n")

	func() {
		defer func() { _ = recover() }()
		p.suppressErrors(func() {
			panic("boom")
		})
	}()

	if p.areErrorsSuppressed {
		t.Error("the suppression flag leaked after a panic")
	}
}

func TestAddSyntaxErrorUsesLineRanges(t *testing.T) {
	sink := common.NewDiagnosticSink()
	p := NewParser()
	text := common.NewText("aaa\nbbb\n")
	p.startNewParse(text, 0, text.Length(), NewParseOptions(), sink, 0)

	// Offset 4 is the start of the second line.
	p.addSyntaxError("problem", common.TextRange{Start: 4, Length: 3})

	errors := sink.GetErrors()
	if len(errors) != 1 {
		t.Fatalf("expected one error, got %d", len(errors))
	}
	got := errors[0].Range
	want := common.Range{Start: common.Position{Line: 1, Character: 0}, End: common.Position{Line: 1, Character: 3}}
	if !common.RangesAreEqual(got, want) {
		t.Errorf("diagnostic range = %v, want %v", got, want)
	}
}

func TestGetKeywordToken(t *testing.T) {
	p := newParserFor(t, "while x:\n    pass\n")
	tok := p.getKeywordToken(KeywordTypeWhile)
	if tok.KeywordType != KeywordTypeWhile {
		t.Errorf("keyword type = %v", tok.KeywordType)
	}

	// A mismatch fails the assertion, as it does in the original.
	p2 := newParserFor(t, "while x:\n    pass\n")
	defer func() {
		if recover() == nil {
			t.Error("expected a mismatched keyword to fail the assertion")
		}
	}()
	p2.getKeywordToken(KeywordTypeIf)
}

func TestParseOptionsDefaults(t *testing.T) {
	opts := NewParseOptions()
	if opts.IsStubFile || opts.ReportInvalidStringEscapeSequence || opts.SkipFunctionAndClassBody ||
		opts.UseNotebookMode || opts.ReportErrorsForParsedStringContents {
		t.Error("all boolean options default to false")
	}
	if !opts.PythonVersion.IsEqualTo(common.LatestStablePythonVersion) {
		t.Errorf("default python version = %v, want %v", opts.PythonVersion, common.LatestStablePythonVersion)
	}
}
