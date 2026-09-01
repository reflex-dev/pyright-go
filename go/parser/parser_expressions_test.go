package parser

import (
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/pyright/go/common"
)

// These drive the operator precedence chain over the real atom grammar. They
// cover precedence, associativity, the comparison operator forms and the depth
// guard, which is what the chain itself is responsible for; the atoms below it
// have their own tests in parser_atoms_test.go.
//
// withStubAtoms used to swap in a stand-in atom parser while the atom grammar
// was still unported. It is kept as a plain pass-through so the test bodies
// still read as a group, and so the diff that removed the stub stayed small.
func withStubAtoms(t *testing.T, fn func()) {
	t.Helper()
	fn()
}

// render produces a fully parenthesized form so the tree shape is directly
// comparable.
func render(node ParseNode) string {
	switch n := node.(type) {
	case *NameNode:
		return n.D.Value
	case *NumberNode:
		if n.D.Value.IsBigInt {
			return n.D.Value.BigInt.String()
		}
		return strconv.FormatFloat(n.D.Value.Float, 'g', -1, 64)
	case *BinaryOperationNode:
		return "(" + render(n.D.LeftExpr) + " " + OperatorTypeNameMap[n.D.Operator] + " " + render(n.D.RightExpr) + ")"
	case *UnaryOperationNode:
		return "(" + strings.TrimSpace(OperatorTypeNameMap[n.D.Operator]) + " " + render(n.D.Expr) + ")"
	case *ErrorNode:
		return "<error>"
	}
	return "<?>"
}

func parseChain(t *testing.T, source string) (ExpressionNode, *common.DiagnosticSink) {
	t.Helper()
	p, sink := newParserWithSink(t, source)
	return p.parseOrTest(), sink
}

func TestPrecedenceAndAssociativity(t *testing.T) {
	withStubAtoms(t, func() {
		for _, tt := range []struct{ source, want string }{
			// Arithmetic binds tighter than comparison, which binds tighter
			// than and/or.
			{"a + b * c\n", "(a + (b * c))"},
			{"a * b + c\n", "((a * b) + c)"},
			{"a + b - c\n", "((a + b) - c)"},
			{"a * b / c\n", "((a * b) / c)"},

			// ** is right-associative and binds tighter than unary minus on
			// its left operand.
			{"a ** b ** c\n", "(a ** (b ** c))"},
			{"-a ** b\n", "(- (a ** b))"},

			// Unary operators.
			{"-a + b\n", "((- a) + b)"},
			{"~a\n", "(~ a)"},
			{"not a\n", "(not a)"},
			{"not not a\n", "(not (not a))"},

			// Shifts sit between arithmetic and bitwise-and.
			{"a << b + c\n", "(a << (b + c))"},
			{"a & b << c\n", "(a & (b << c))"},
			{"a | b ^ c & d\n", "(a | (b ^ (c & d)))"},

			// Comparison is looser than bitwise-or.
			{"a | b < c\n", "((a | b) < c)"},

			// and binds tighter than or.
			{"a or b and c\n", "(a or (b and c))"},
			{"a and b or c\n", "((a and b) or c)"},

			// not is looser than comparison.
			{"not a < b\n", "(not (a < b))"},
		} {
			got, sink := parseChain(t, tt.source)
			if rendered := render(got); rendered != tt.want {
				t.Errorf("%q parsed as %s, want %s", tt.source, rendered, tt.want)
			}
			if len(sink.GetErrors()) != 0 {
				t.Errorf("%q produced %d unexpected errors", tt.source, len(sink.GetErrors()))
			}
		}
	})
}

func TestComparisonOperatorForms(t *testing.T) {
	withStubAtoms(t, func() {
		for _, tt := range []struct{ source, want string }{
			{"a in b\n", "(a in b)"},
			{"a not in b\n", "(a not in b)"},
			{"a is b\n", "(a is b)"},
			{"a is not b\n", "(a is not b)"},
			{"a < b\n", "(a < b)"},
			{"a != b\n", "(a != b)"},
		} {
			got, sink := parseChain(t, tt.source)
			if rendered := render(got); rendered != tt.want {
				t.Errorf("%q parsed as %s, want %s", tt.source, rendered, tt.want)
			}
			if len(sink.GetErrors()) != 0 {
				t.Errorf("%q produced %d unexpected errors", tt.source, len(sink.GetErrors()))
			}
		}
	})
}

func TestChainedComparisonNestsRight(t *testing.T) {
	// _parseComparison recurses into itself for the right operand, so chained
	// comparisons nest to the right rather than folding left.
	withStubAtoms(t, func() {
		got, _ := parseChain(t, "a < b < c\n")
		if rendered := render(got); rendered != "(a < (b < c))" {
			t.Errorf("chained comparison parsed as %s, want (a < (b < c))", rendered)
		}
	})
}

func TestLessOrGreaterThanIsDeprecatedAndRewritten(t *testing.T) {
	// `<>` reports a diagnostic and is rewritten to !=.
	withStubAtoms(t, func() {
		got, sink := parseChain(t, "a <> b\n")
		if rendered := render(got); rendered != "(a != b)" {
			t.Errorf("`<>` parsed as %s, want (a != b)", rendered)
		}
		if len(sink.GetErrors()) != 1 {
			t.Errorf("expected one deprecation error, got %d", len(sink.GetErrors()))
		}
	})
}

func TestBareNotWithoutInIsNotAComparison(t *testing.T) {
	// A `not` that is not followed by `in` must not be consumed as a
	// comparison operator; the chain stops instead.
	withStubAtoms(t, func() {
		got, _ := parseChain(t, "a not b\n")
		if rendered := render(got); rendered != "a" {
			t.Errorf("parsed as %s, want just `a`", rendered)
		}
	})
}

func TestMaxParseDepthProducesErrorNode(t *testing.T) {
	// Deeply nested unary operators trip the depth guard, which replaces the
	// offending node with an ErrorNode rather than letting the binder recurse
	// forever.
	//
	// Note that the guard does not truncate the whole expression: the ErrorNode
	// it returns has no entry in maxChildDepthMap, so the depth count restarts
	// at zero and the remaining outer levels wrap it in ordinary
	// UnaryOperationNodes. The root is therefore still a UnaryOperationNode,
	// with an ErrorNode buried inside. That is what the original does too.
	withStubAtoms(t, func() {
		source := strings.Repeat("-", maxChildNodeDepth+10) + "a\n"
		got, sink := parseChain(t, source)

		if !containsErrorNode(got) {
			t.Error("expected an ErrorNode somewhere in the tree at the depth limit")
		}
		if len(sink.GetErrors()) == 0 {
			t.Error("expected a max-parse-depth diagnostic")
		}
	})
}

func containsErrorNode(node ParseNode) bool {
	switch n := node.(type) {
	case *ErrorNode:
		return true
	case *UnaryOperationNode:
		return containsErrorNode(n.D.Expr)
	case *BinaryOperationNode:
		return containsErrorNode(n.D.LeftExpr) || containsErrorNode(n.D.RightExpr)
	}
	return false
}

func TestShallowNestingDoesNotTripDepthGuard(t *testing.T) {
	withStubAtoms(t, func() {
		source := strings.Repeat("-", 10) + "a\n"
		got, sink := parseChain(t, source)
		if got.GetNodeType() == ParseNodeTypeError {
			t.Error("10 levels should be well under the limit")
		}
		if len(sink.GetErrors()) != 0 {
			t.Errorf("unexpected errors: %d", len(sink.GetErrors()))
		}
	})
}

func TestChainReachesRealAtoms(t *testing.T) {
	// The precedence chain now bottoms out at the real atom grammar rather than
	// a stub, so a call and a subscript parse through it.
	got, sink := parseChain(t, "f(1) + a[0]\n")

	binary, ok := got.(*BinaryOperationNode)
	if !ok {
		t.Fatalf("expected a BinaryOperationNode, got %T", got)
	}
	if _, ok := binary.D.LeftExpr.(*CallNode); !ok {
		t.Errorf("expected the left operand to be a CallNode, got %T", binary.D.LeftExpr)
	}
	if _, ok := binary.D.RightExpr.(*IndexNode); !ok {
		t.Errorf("expected the right operand to be an IndexNode, got %T", binary.D.RightExpr)
	}
	if len(sink.GetErrors()) != 0 {
		t.Errorf("unexpected errors: %d", len(sink.GetErrors()))
	}
}
