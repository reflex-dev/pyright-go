package parser

import "testing"

// handleExpressionParseError is the parser's expression-level recovery path.
// Its range selection is subtle: the error node must not cover a token that
// belongs to a sibling, so when the cursor is already sitting on a stop token
// it falls back to the target, then the child, then a zero-length range.

func TestHandleExpressionParseErrorConsumesToStopToken(t *testing.T) {
	p, sink := newParserWithSink(t, "a b c\nnext\n")
	node := p.handleExpressionParseError(ErrorExpressionCategoryMissingExpression, "boom", nil, nil, nil)

	if node.D.Category != ErrorExpressionCategoryMissingExpression {
		t.Errorf("category = %v", node.D.Category)
	}
	if len(sink.GetErrors()) != 1 {
		t.Fatalf("expected one error, got %d", len(sink.GetErrors()))
	}
	// Recovery stops at the newline rather than running to end of stream.
	if p.peekTokenType() != TokenTypeNewLine {
		t.Errorf("expected to stop at the newline, got %v", p.peekTokenType())
	}
}

func TestHandleExpressionParseErrorZeroLengthAtStopToken(t *testing.T) {
	// Sitting on a newline with no target or child, the node collapses to a
	// zero-length range so it cannot overlap a sibling.
	p, _ := newParserWithSink(t, "\n")
	node := p.handleExpressionParseError(ErrorExpressionCategoryMissingExpression, "boom", nil, nil, nil)

	if node.Length != 0 {
		t.Errorf("expected a zero-length error node, got length %d", node.Length)
	}
}

func TestHandleExpressionParseErrorPrefersTargetThenChild(t *testing.T) {
	// At a stop token, the target token wins.
	p, _ := newParserWithSink(t, "\n")
	target := NewToken(TokenTypeKeyword, 5, 3, nil)
	node := p.handleExpressionParseError(ErrorExpressionCategoryMissingExpression, "boom", target, nil, nil)
	checkRange(t, "with target", node, 5, 3)

	// With no target, the child node's range is used, and the child is adopted.
	p2, _ := newParserWithSink(t, "\n")
	child := name(7, 2, "ab")
	node2 := p2.handleExpressionParseError(ErrorExpressionCategoryMissingExpression, "boom", nil, child, nil)
	checkRange(t, "with child", node2, 7, 2)
	if node2.D.Child != ExpressionNode(child) {
		t.Error("the child node should be attached to the error node")
	}
	if child.Parent != ParseNode(node2) {
		t.Error("the child's parent should be the error node")
	}
}

func TestHandleExpressionParseErrorUsesNextTokenWhenNotAtStop(t *testing.T) {
	// Not at a stop token: the error node covers the offending token itself,
	// even when a target was supplied.
	p, _ := newParserWithSink(t, "abc\n")
	target := NewToken(TokenTypeKeyword, 100, 3, nil)
	node := p.handleExpressionParseError(ErrorExpressionCategoryMissingExpression, "boom", target, nil, nil)
	checkRange(t, "not at stop token", node, 0, 3)
}

func TestHandleExpressionParseErrorAdditionalStopTokens(t *testing.T) {
	// An extra stop token halts recovery earlier than the newline would.
	p, _ := newParserWithSink(t, "a , b\n")
	p.handleExpressionParseError(ErrorExpressionCategoryMissingExpression, "boom", nil, nil,
		[]TokenType{TokenTypeComma})

	if p.peekTokenType() != TokenTypeComma {
		t.Errorf("expected to stop at the comma, got %v", p.peekTokenType())
	}
}

func TestParseAtomReportsMissingExpression(t *testing.T) {
	// parseAtom's fallback now runs for real rather than panicking.
	p, sink := newParserWithSink(t, "= 1\n")
	got := p.parseAtom()

	if got.GetNodeType() != ParseNodeTypeError {
		t.Errorf("expected an ErrorNode, got node type %d", got.GetNodeType())
	}
	if len(sink.GetErrors()) != 1 {
		t.Errorf("expected one error, got %d", len(sink.GetErrors()))
	}
}
