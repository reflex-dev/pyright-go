package parser

import (
	"testing"

	"github.com/microsoft/pyright/go/common"
)

// The factories in parseNodes.ts do two things beyond storing fields: they wire
// parent pointers and they extend the node's range over specific children. Both
// are easy to get subtly wrong and neither is covered by the tokenizer tests,
// so they are pinned here.

func tok(start, length int) Token {
	return NewToken(TokenTypeKeyword, start, length, nil)
}

func name(start, length int, value string) *NameNode {
	return NewNameNode(&IdentifierToken{
		TokenBase: TokenBase{
			TextRange: common.TextRange{Start: start, Length: length},
			Type:      TokenTypeIdentifier,
		},
		Value: value,
	})
}

func checkRange(t *testing.T, label string, node ParseNode, start, length int) {
	t.Helper()
	r := node.GetRange()
	if r.Start != start || r.Length != length {
		t.Errorf("%s range = {start:%d len:%d}, want {start:%d len:%d}", label, r.Start, r.Length, start, length)
	}
}

func TestExtendRange(t *testing.T) {
	node := NewPassNode(common.TextRange{Start: 10, Length: 4})
	checkRange(t, "initial", node, 10, 4)

	// Extending backward moves the start and grows the length.
	ExtendRange(node, common.TextRange{Start: 4, Length: 2})
	checkRange(t, "after extending backward", node, 4, 10)

	// Extending forward only grows the length.
	ExtendRange(node, common.TextRange{Start: 20, Length: 3})
	checkRange(t, "after extending forward", node, 4, 19)

	// An interior range changes nothing.
	ExtendRange(node, common.TextRange{Start: 6, Length: 1})
	checkRange(t, "after an interior extend", node, 4, 19)
}

func TestIfNodeRangeAndParents(t *testing.T) {
	// if <test> : <suite>   with an else suite
	testExpr := name(3, 4, "cond")
	ifSuite := NewSuiteNode(common.TextRange{Start: 9, Length: 5})
	elseSuite := NewSuiteNode(common.TextRange{Start: 20, Length: 6})

	node := NewIfNode(tok(0, 2), testExpr, ifSuite, elseSuite)

	// Starts at the `if` token and covers through the else suite.
	checkRange(t, "IfNode", node, 0, 26)

	if testExpr.Parent != ParseNode(node) {
		t.Error("test expression parent not set")
	}
	if ifSuite.Parent != ParseNode(node) {
		t.Error("if suite parent not set")
	}
	if elseSuite.Parent != ParseNode(node) {
		t.Error("else suite parent not set")
	}
}

func TestIfNodeWithoutElse(t *testing.T) {
	testExpr := name(3, 4, "cond")
	ifSuite := NewSuiteNode(common.TextRange{Start: 9, Length: 5})

	node := NewIfNode(tok(0, 2), testExpr, ifSuite, nil)
	checkRange(t, "IfNode without else", node, 0, 14)
	if node.D.ElseSuite != nil {
		t.Error("expected a nil else suite")
	}
}

func TestWhileNodeDoesNotExtendOverTestExpr(t *testing.T) {
	// WhileNode.create extends over whileSuite only -- not over testExpr, unlike
	// IfNode. Since the suite always follows the test this is unobservable for
	// well-formed input, but the asymmetry is real and is preserved.
	testExpr := name(6, 4, "cond")
	whileSuite := NewSuiteNode(common.TextRange{Start: 12, Length: 5})

	node := NewWhileNode(tok(0, 5), testExpr, whileSuite)
	checkRange(t, "WhileNode", node, 0, 17)
}

func TestBinaryOperationNodeRange(t *testing.T) {
	left := name(0, 1, "a")
	right := name(4, 1, "b")
	node := NewBinaryOperationNode(left, right, tok(2, 1), OperatorTypeAdd)

	// Starts at the left expression, ends at the right.
	checkRange(t, "BinaryOperationNode", node, 0, 5)
	if left.Parent != ParseNode(node) || right.Parent != ParseNode(node) {
		t.Error("operand parents not set")
	}
}

func TestCallNodeRangeWithAndWithoutArgs(t *testing.T) {
	callee := name(0, 3, "foo")
	noArgs := NewCallNode(callee, []*ArgumentNode{}, false)
	// With no arguments the range stays that of the callee.
	checkRange(t, "CallNode with no args", noArgs, 0, 3)

	callee2 := name(0, 3, "foo")
	arg := NewArgumentNode(nil, name(4, 1, "x"), ArgCategorySimple)
	withArgs := NewCallNode(callee2, []*ArgumentNode{arg}, false)
	checkRange(t, "CallNode with args", withArgs, 0, 5)
}

func TestIndexNodeExtendsOverCloseBracket(t *testing.T) {
	// IndexNode extends over the closing bracket token, not over its items.
	leftExpr := name(0, 1, "a")
	item := NewArgumentNode(nil, name(2, 1, "i"), ArgCategorySimple)
	node := NewIndexNode(leftExpr, []*ArgumentNode{item}, false, tok(3, 1))

	checkRange(t, "IndexNode", node, 0, 4)
}

func TestErrorNodeWithDecorators(t *testing.T) {
	// ErrorNode extends over decorators[0], which precedes the initial range.
	decorator := NewDecoratorNode(tok(0, 1), name(1, 3, "dec"))
	node := NewErrorNode(common.TextRange{Start: 10, Length: 2}, ErrorExpressionCategoryMissingExpression, nil, []*DecoratorNode{decorator})

	checkRange(t, "ErrorNode", node, 0, 12)
	if decorator.Parent != ParseNode(node) {
		t.Error("decorator parent not set")
	}

	// An empty (non-nil) decorator slice enters the branch but extends nothing,
	// matching `if (decorators)` on an empty array in JavaScript.
	node2 := NewErrorNode(common.TextRange{Start: 10, Length: 2}, ErrorExpressionCategoryMissingExpression, nil, []*DecoratorNode{})
	checkRange(t, "ErrorNode with empty decorators", node2, 10, 2)
}

func TestArgumentNodeRangeFallsBackToValueExpr(t *testing.T) {
	valueExpr := name(5, 3, "abc")
	withoutToken := NewArgumentNode(nil, valueExpr, ArgCategorySimple)
	checkRange(t, "ArgumentNode without start token", withoutToken, 5, 3)

	valueExpr2 := name(5, 3, "abc")
	withToken := NewArgumentNode(tok(3, 1), valueExpr2, ArgCategorySimple)
	// Starts at the token, extends over the value.
	checkRange(t, "ArgumentNode with start token", withToken, 3, 5)
}

func TestPatternSequenceFindsStarEntry(t *testing.T) {
	starRange := common.TextRange{Start: 4, Length: 1}
	plain := NewPatternAsNode([]PatternAtomNode{NewPatternCaptureNode(name(0, 1, "a"), nil)}, nil)
	starred := NewPatternAsNode([]PatternAtomNode{NewPatternCaptureNode(name(5, 1, "b"), &starRange)}, nil)

	node := NewPatternSequenceNode(common.TextRange{Start: 0, Length: 1}, []*PatternAsNode{plain, starred})

	if node.D.StarEntryIndex == nil {
		t.Fatal("expected the star entry to be found")
	}
	if *node.D.StarEntryIndex != 1 {
		t.Errorf("star entry index = %d, want 1", *node.D.StarEntryIndex)
	}

	// With no starred entry the index stays absent, as `undefined` does.
	plainOnly := NewPatternSequenceNode(common.TextRange{Start: 0, Length: 1},
		[]*PatternAsNode{NewPatternAsNode([]PatternAtomNode{NewPatternCaptureNode(name(0, 1, "a"), nil)}, nil)})
	if plainOnly.D.StarEntryIndex != nil {
		t.Errorf("expected no star entry, got %d", *plainOnly.D.StarEntryIndex)
	}
}

func TestPatternCaptureWildcard(t *testing.T) {
	wildcard := NewPatternCaptureNode(name(0, 1, "_"), nil)
	if !wildcard.D.IsWildcard {
		t.Error(`a target named "_" is a wildcard`)
	}
	if wildcard.D.IsStar {
		t.Error("no star token was passed")
	}

	named := NewPatternCaptureNode(name(0, 1, "x"), nil)
	if named.D.IsWildcard {
		t.Error(`only "_" is a wildcard`)
	}
}

func TestDummyClassForDecorators(t *testing.T) {
	decorator := NewDecoratorNode(tok(4, 1), name(5, 3, "dec"))
	node := NewClassNodeDummyForDecorators([]*DecoratorNode{decorator})

	// Range starts at the first decorator and covers it.
	checkRange(t, "dummy ClassNode", node, 4, 4)

	// The synthesized name and suite carry id 0, as in the original.
	if node.D.Name.ID != 0 || node.D.Suite.ID != 0 {
		t.Errorf("synthesized name/suite ids = %d/%d, want 0/0", node.D.Name.ID, node.D.Suite.ID)
	}
	if node.D.Name.D.Value != "" {
		t.Errorf("synthesized name value = %q, want empty", node.D.Name.D.Value)
	}
	if node.D.Name.Parent != ParseNode(node) || node.D.Suite.Parent != ParseNode(node) {
		t.Error("synthesized name/suite parents not set")
	}
}

func TestIsExpressionNode(t *testing.T) {
	if !IsExpressionNode(name(0, 1, "a")) {
		t.Error("a name is an expression")
	}
	if IsExpressionNode(NewPassNode(common.TextRange{Start: 0, Length: 4})) {
		t.Error("pass is not an expression")
	}

	// The original's switch omits Assignment and AugmentedAssignment even
	// though the ExpressionNode union contains them.
	assignment := NewAssignmentNode(name(0, 1, "a"), name(4, 1, "b"))
	if IsExpressionNode(assignment) {
		t.Error("the original returns false for Assignment; this must match")
	}
}

func TestNodeIdsAreUnique(t *testing.T) {
	seen := map[int]bool{}
	for i := 0; i < 100; i++ {
		id := NewPassNode(common.TextRange{Start: 0, Length: 4}).ID
		if seen[id] {
			t.Fatalf("duplicate node id %d", id)
		}
		seen[id] = true
	}
}

func TestParseNodeTypeNumbering(t *testing.T) {
	// The numbering is asserted against in pyright's tests and crosses the
	// language server boundary, so the decade markers from the original are
	// pinned here.
	for _, tt := range []struct {
		nodeType ParseNodeType
		want     int
	}{
		{ParseNodeTypeError, 0},
		{ParseNodeTypeClass, 10},
		{ParseNodeTypeDictionaryKeyEntry, 20},
		{ParseNodeTypeFormatString, 30},
		{ParseNodeTypeNumber, 40},
		{ParseNodeTypeSuite, 50},
		{ParseNodeTypeYield, 60},
		{ParseNodeTypePatternMapping, 70},
		{ParseNodeTypeTypeAlias, 77},
	} {
		if int(tt.nodeType) != tt.want {
			t.Errorf("node type = %d, want %d", int(tt.nodeType), tt.want)
		}
	}
}
