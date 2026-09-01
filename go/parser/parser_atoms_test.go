package parser

import "testing"

// parseAtom is fully transliterated. These cover the simple-atom branches; the
// bracketed and quoted branches are covered in parser_trailers_test.go.

func TestParseAtomSimpleForms(t *testing.T) {
	for _, tt := range []struct {
		source   string
		nodeType ParseNodeType
	}{
		{"...\n", ParseNodeTypeEllipsis},
		{"42\n", ParseNodeTypeNumber},
		{"3.5\n", ParseNodeTypeNumber},
		{"name\n", ParseNodeTypeName},
		{"True\n", ParseNodeTypeConstant},
		{"False\n", ParseNodeTypeConstant},
		{"None\n", ParseNodeTypeConstant},
		{"__debug__\n", ParseNodeTypeConstant},
	} {
		p, sink := newParserWithSink(t, tt.source)
		got := p.parseAtom()
		if got.GetNodeType() != tt.nodeType {
			t.Errorf("%q parsed as node type %d, want %d", tt.source, got.GetNodeType(), tt.nodeType)
		}
		if len(sink.GetErrors()) != 0 {
			t.Errorf("%q produced %d unexpected errors", tt.source, len(sink.GetErrors()))
		}
	}
}

func TestParseAtomConstantKeepsKeywordType(t *testing.T) {
	p, _ := newParserWithSink(t, "None\n")
	node, ok := p.parseAtom().(*ConstantNode)
	if !ok {
		t.Fatal("expected a ConstantNode")
	}
	if node.D.ConstType != KeywordTypeNone {
		t.Errorf("const type = %v, want None", node.D.ConstType)
	}
}

func TestParseAtomSoftKeywordBecomesName(t *testing.T) {
	// A soft keyword that is not one of the four constants is reinterpreted as
	// an identifier rather than failing.
	for _, kw := range []string{"match", "case", "type"} {
		p, sink := newParserWithSink(t, kw+"\n")
		got := p.parseAtom()
		name, ok := got.(*NameNode)
		if !ok {
			t.Errorf("%q parsed as %T, want *NameNode", kw, got)
			continue
		}
		if name.D.Value != kw {
			t.Errorf("name value = %q, want %q", name.D.Value, kw)
		}
		if len(sink.GetErrors()) != 0 {
			t.Errorf("%q produced %d unexpected errors", kw, len(sink.GetErrors()))
		}
	}
}

func TestParseAtomNumberValue(t *testing.T) {
	p, _ := newParserWithSink(t, "42\n")
	node, ok := p.parseAtom().(*NumberNode)
	if !ok {
		t.Fatal("expected a NumberNode")
	}
	if !node.D.IsInteger || node.D.IsImaginary {
		t.Errorf("42 should be a non-imaginary integer, got isInteger=%v isImaginary=%v",
			node.D.IsInteger, node.D.IsImaginary)
	}
	if node.D.Value.IsBigInt || node.D.Value.Float != 42 {
		t.Errorf("value = %+v, want the double 42", node.D.Value)
	}
}

func TestParseAtomRanges(t *testing.T) {
	p, _ := newParserWithSink(t, "...\n")
	checkRange(t, "EllipsisNode", p.parseAtom(), 0, 3)

	p2, _ := newParserWithSink(t, "abc\n")
	checkRange(t, "NameNode", p2.parseAtom(), 0, 3)
}
