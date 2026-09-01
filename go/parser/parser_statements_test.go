package parser

import (
	"testing"

	"github.com/microsoft/pyright/go/common"
)

func newParserWithSink(t *testing.T, source string) (*Parser, *common.DiagnosticSink) {
	t.Helper()
	sink := common.NewDiagnosticSink()
	p := NewParser()
	text := common.NewText(source)
	p.startNewParse(text, 0, text.Length(), NewParseOptions(), sink, 0)
	return p, sink
}

func TestParsePassStatement(t *testing.T) {
	p, sink := newParserWithSink(t, "pass\n")
	node := p.parsePassStatement()
	checkRange(t, "PassNode", node, 0, 4)
	if len(sink.GetErrors()) != 0 {
		t.Errorf("unexpected errors: %d", len(sink.GetErrors()))
	}
}

func TestParseBreakOutsideLoop(t *testing.T) {
	p, sink := newParserWithSink(t, "break\n")
	node := p.parseBreakStatement()
	checkRange(t, "BreakNode", node, 0, 5)

	errs := sink.GetErrors()
	if len(errs) != 1 {
		t.Fatalf("expected one error for break outside a loop, got %d", len(errs))
	}
	if errs[0].Message == "" {
		t.Error("expected a message")
	}
}

func TestParseBreakInsideLoopIsClean(t *testing.T) {
	p, sink := newParserWithSink(t, "break\n")
	p.isInLoop = true
	p.parseBreakStatement()
	if len(sink.GetErrors()) != 0 {
		t.Errorf("break inside a loop should be clean, got %d errors", len(sink.GetErrors()))
	}
}

func TestParseBreakInExceptionGroup(t *testing.T) {
	// The exception-group diagnostic is only reached when in a loop; the
	// outside-a-loop check is an `else if`.
	p, sink := newParserWithSink(t, "break\n")
	p.isInLoop = true
	p.isInExceptionGroup = true
	p.parseBreakStatement()
	if len(sink.GetErrors()) != 1 {
		t.Errorf("expected one error, got %d", len(sink.GetErrors()))
	}
}

func TestParseBreakInFinallyLoopIsVersionGated(t *testing.T) {
	// The finally diagnostic applies only from Python 3.14 on.
	for _, tt := range []struct {
		version   common.PythonVersion
		wantCount int
	}{
		{common.PythonVersion3_13, 0},
		{common.PythonVersion3_14, 1},
	} {
		p, sink := newParserWithSink(t, "break\n")
		p.isInLoop = true
		p.isInFinallyLoop = true
		p.parseOptions.PythonVersion = tt.version

		p.parseBreakStatement()
		if got := len(sink.GetErrors()); got != tt.wantCount {
			t.Errorf("python %v: got %d errors, want %d", tt.version, got, tt.wantCount)
		}
	}
}

func TestParseContinueStatement(t *testing.T) {
	p, sink := newParserWithSink(t, "continue\n")
	node := p.parseContinueStatement()
	checkRange(t, "ContinueNode", node, 0, 8)
	if len(sink.GetErrors()) != 1 {
		t.Errorf("expected one error for continue outside a loop, got %d", len(sink.GetErrors()))
	}
}

func TestParseNameList(t *testing.T) {
	p, sink := newParserWithSink(t, "a, b, c\n")
	names := p.parseNameList()

	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}
	for i, want := range []string{"a", "b", "c"} {
		if names[i].D.Value != want {
			t.Errorf("name %d = %q, want %q", i, names[i].D.Value, want)
		}
	}
	if len(sink.GetErrors()) != 0 {
		t.Errorf("unexpected errors: %d", len(sink.GetErrors()))
	}
}

func TestParseNameListReportsMissingIdentifier(t *testing.T) {
	// A trailing comma leaves nothing to parse, which is an error.
	p, sink := newParserWithSink(t, "a,\n")
	names := p.parseNameList()

	if len(names) != 1 {
		t.Errorf("expected 1 name, got %d", len(names))
	}
	if len(sink.GetErrors()) != 1 {
		t.Errorf("expected one error, got %d", len(sink.GetErrors()))
	}
}

func TestParseGlobalStatement(t *testing.T) {
	p, sink := newParserWithSink(t, "global a, b\n")
	node := p.parseGlobalStatement()

	if len(node.D.Targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(node.D.Targets))
	}
	// Starts at `global` and extends over the last name.
	checkRange(t, "GlobalNode", node, 0, 11)

	for _, target := range node.D.Targets {
		if target.Parent != ParseNode(node) {
			t.Error("target parent not set")
		}
	}
	if len(sink.GetErrors()) != 0 {
		t.Errorf("unexpected errors: %d", len(sink.GetErrors()))
	}
}

func TestParseNonlocalStatement(t *testing.T) {
	p, _ := newParserWithSink(t, "nonlocal x\n")
	node := p.parseNonlocalStatement()

	if len(node.D.Targets) != 1 || node.D.Targets[0].D.Value != "x" {
		t.Fatalf("unexpected targets: %+v", node.D.Targets)
	}
	checkRange(t, "NonlocalNode", node, 0, 10)
}

func TestIsNameOrMemberAccessExpression(t *testing.T) {
	p := NewParser()

	if !p.isNameOrMemberAccessExpression(name(0, 1, "a")) {
		t.Error("a bare name qualifies")
	}

	// a.b.c walks left until it reaches the name.
	inner := NewMemberAccessNode(name(0, 1, "a"), name(2, 1, "b"))
	outer := NewMemberAccessNode(inner, name(4, 1, "c"))
	if !p.isNameOrMemberAccessExpression(outer) {
		t.Error("a chain of member accesses rooted at a name qualifies")
	}

	// A call at the root does not.
	callRoot := NewMemberAccessNode(NewCallNode(name(0, 1, "f"), nil, false), name(4, 1, "b"))
	if p.isNameOrMemberAccessExpression(callRoot) {
		t.Error("a chain rooted at a call does not qualify")
	}
}

func TestIsNextTokenNeverExpression(t *testing.T) {
	for _, tt := range []struct {
		source string
		want   bool
	}{
		{"for x\n", true},
		{"in x\n", true},
		{"if x\n", true},
		{"while x\n", false}, // a keyword, but not one of the three
		{"+= 1\n", true},
		{"= 1\n", true},
		{"+ 1\n", false}, // a binary operator can start an expression
		{",\n", true},
		{":\n", true},
		{")\n", true},
		{"]\n", true},
		{"}\n", true},
		{";\n", true},
		{"\n", true}, // newline
		{"x\n", false},
		{"1\n", false},
		{"'s'\n", false},
	} {
		p, _ := newParserWithSink(t, tt.source)
		if got := p.isNextTokenNeverExpression(); got != tt.want {
			t.Errorf("isNextTokenNeverExpression() for %q = %v, want %v", tt.source, got, tt.want)
		}
	}
}

func TestIsNextTokenNeverExpressionAtEndOfStream(t *testing.T) {
	p, _ := newParserWithSink(t, "")
	// Skip the implied newline to land on end-of-stream.
	p.getNextToken()
	if !p.isNextTokenNeverExpression() {
		t.Error("end-of-stream can never begin an expression")
	}
}
