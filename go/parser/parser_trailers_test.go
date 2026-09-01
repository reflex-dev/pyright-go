package parser

import (
	"testing"

	"github.com/microsoft/pyright/go/common"
)

// These cover the atom grammar end to end through the public entry point. The
// corpus differential in tools/ts-bridge pins the whole tree shape against the
// TypeScript over 1343 files; these pin the specific shapes and diagnostics
// that are easy to get subtly wrong and are worth failing loudly on.

func parseSource(t *testing.T, source string) (*ParseFileResults, *common.DiagnosticSink) {
	t.Helper()
	sink := common.NewDiagnosticSink()
	p := NewParser()
	results := p.ParseSourceFile(common.NewText(source), NewParseOptions(), sink)
	return results, sink
}

// firstExpr returns the expression of a module whose only statement is a
// one-line statement holding a single expression.
func firstExpr(t *testing.T, source string) ParseNode {
	t.Helper()
	results, sink := parseSource(t, source)
	if errs := sink.GetErrors(); len(errs) != 0 {
		t.Fatalf("%q produced %d unexpected errors: %v", source, len(errs), errs[0].Message)
	}

	statements := results.ParserOutput.ParseTree.D.Statements
	if len(statements) != 1 {
		t.Fatalf("%q produced %d statements, want 1", source, len(statements))
	}
	list, ok := statements[0].(*StatementListNode)
	if !ok {
		t.Fatalf("%q produced %T, want *StatementListNode", source, statements[0])
	}
	if len(list.D.Statements) != 1 {
		t.Fatalf("%q produced %d small statements, want 1", source, len(list.D.Statements))
	}
	return list.D.Statements[0]
}

func TestTrailerShapes(t *testing.T) {
	for _, tt := range []struct {
		source string
		check  func(t *testing.T, node ParseNode)
	}{
		{"f(1, 2)\n", func(t *testing.T, node ParseNode) {
			call, ok := node.(*CallNode)
			if !ok {
				t.Fatalf("got %T, want *CallNode", node)
			}
			if len(call.D.Args) != 2 {
				t.Errorf("got %d args, want 2", len(call.D.Args))
			}
		}},
		{"f(a=1)\n", func(t *testing.T, node ParseNode) {
			call := node.(*CallNode)
			if call.D.Args[0].D.Name == nil || call.D.Args[0].D.Name.D.Value != "a" {
				t.Error("expected a keyword argument named a")
			}
		}},
		{"f(*a, **b)\n", func(t *testing.T, node ParseNode) {
			call := node.(*CallNode)
			if call.D.Args[0].D.ArgCategory != ArgCategoryUnpackedList {
				t.Error("expected *a to be an unpacked list")
			}
			if call.D.Args[1].D.ArgCategory != ArgCategoryUnpackedDictionary {
				t.Error("expected **b to be an unpacked dictionary")
			}
		}},
		{"a.b.c\n", func(t *testing.T, node ParseNode) {
			outer, ok := node.(*MemberAccessNode)
			if !ok {
				t.Fatalf("got %T, want *MemberAccessNode", node)
			}
			if outer.D.Member.D.Value != "c" {
				t.Errorf("outer member = %q, want c", outer.D.Member.D.Value)
			}
			if _, ok := outer.D.LeftExpr.(*MemberAccessNode); !ok {
				t.Error("expected the left side to be another member access")
			}
		}},
		{"a[0]\n", func(t *testing.T, node ParseNode) {
			if _, ok := node.(*IndexNode); !ok {
				t.Fatalf("got %T, want *IndexNode", node)
			}
		}},
		{"a[1:2:3]\n", func(t *testing.T, node ParseNode) {
			index := node.(*IndexNode)
			slice, ok := index.D.Items[0].D.ValueExpr.(*SliceNode)
			if !ok {
				t.Fatalf("got %T, want *SliceNode", index.D.Items[0].D.ValueExpr)
			}
			if slice.D.StartValue == nil || slice.D.EndValue == nil || slice.D.StepValue == nil {
				t.Error("expected all three slice values to be present")
			}
		}},
		{"a[:]\n", func(t *testing.T, node ParseNode) {
			slice := node.(*IndexNode).D.Items[0].D.ValueExpr.(*SliceNode)
			if slice.D.StartValue != nil || slice.D.EndValue != nil || slice.D.StepValue != nil {
				t.Error("expected a bare slice to have no values")
			}
		}},
		{"await f()\n", func(t *testing.T, node ParseNode) {
			await, ok := node.(*AwaitNode)
			if !ok {
				t.Fatalf("got %T, want *AwaitNode", node)
			}
			if _, ok := await.D.Expr.(*CallNode); !ok {
				t.Error("expected await to wrap the whole trailer chain")
			}
		}},
	} {
		tt.check(t, firstExpr(t, tt.source))
	}
}

func TestDisplayShapes(t *testing.T) {
	for _, tt := range []struct {
		source   string
		nodeType ParseNodeType
	}{
		{"()\n", ParseNodeTypeTuple},
		{"(1,)\n", ParseNodeTypeTuple},
		{"(1, 2)\n", ParseNodeTypeTuple},
		{"1, 2\n", ParseNodeTypeTuple},
		{"[]\n", ParseNodeTypeList},
		{"[1, 2]\n", ParseNodeTypeList},
		{"{}\n", ParseNodeTypeDictionary},
		{"{1: 2}\n", ParseNodeTypeDictionary},
		{"{**a}\n", ParseNodeTypeDictionary},
		{"{1, 2}\n", ParseNodeTypeSet},
		{"[x for x in y]\n", ParseNodeTypeList},
		{"(x for x in y)\n", ParseNodeTypeComprehension},
		{"{k: v for k, v in y}\n", ParseNodeTypeDictionary},
		{"lambda x: x\n", ParseNodeTypeLambda},
		{"'a' 'b'\n", ParseNodeTypeStringList},
		{"f'{x}'\n", ParseNodeTypeStringList},
	} {
		node := firstExpr(t, tt.source)
		if node.GetNodeType() != tt.nodeType {
			t.Errorf("%q parsed as node type %d, want %d", tt.source, node.GetNodeType(), tt.nodeType)
		}
	}
}

func TestParenthesizedExpressionIsMarked(t *testing.T) {
	// A single parenthesized expression is not a tuple; it is the expression
	// itself with hasParens set, so the comparison-chaining logic can tell the
	// difference between `(a < b) < c` and `a < b < c`.
	node := firstExpr(t, "(a < b)\n")
	binary, ok := node.(*BinaryOperationNode)
	if !ok {
		t.Fatalf("got %T, want *BinaryOperationNode", node)
	}
	if !binary.D.HasParens {
		t.Error("expected hasParens to be set")
	}
}

func TestSingleElementTupleNeedsTrailingComma(t *testing.T) {
	if got := firstExpr(t, "(1)\n").GetNodeType(); got != ParseNodeTypeNumber {
		t.Errorf("`(1)` parsed as node type %d, want a bare number", got)
	}
	if got := firstExpr(t, "(1,)\n").GetNodeType(); got != ParseNodeTypeTuple {
		t.Errorf("`(1,)` parsed as node type %d, want a tuple", got)
	}
}

func TestComprehensionForIfChain(t *testing.T) {
	node := firstExpr(t, "[x for x in y if x if x for z in x]\n")
	list := node.(*ListNode)
	comp, ok := list.D.Items[0].(*ComprehensionNode)
	if !ok {
		t.Fatalf("got %T, want *ComprehensionNode", list.D.Items[0])
	}
	if len(comp.D.ForIfNodes) != 4 {
		t.Fatalf("got %d for/if clauses, want 4", len(comp.D.ForIfNodes))
	}
	for i, want := range []ParseNodeType{
		ParseNodeTypeComprehensionFor,
		ParseNodeTypeComprehensionIf,
		ParseNodeTypeComprehensionIf,
		ParseNodeTypeComprehensionFor,
	} {
		if got := comp.D.ForIfNodes[i].GetNodeType(); got != want {
			t.Errorf("clause %d is node type %d, want %d", i, got, want)
		}
	}
}

func TestAsyncComprehension(t *testing.T) {
	node := firstExpr(t, "[x async for x in y]\n")
	comp := node.(*ListNode).D.Items[0].(*ComprehensionNode)
	forNode := comp.D.ForIfNodes[0].(*ComprehensionForNode)
	if !forNode.D.IsAsync || forNode.D.AsyncToken == nil {
		t.Error("expected the for clause to be marked async")
	}
	// The node starts at `async`, not at `for`.
	if got, want := forNode.GetRange().Start, len("[x "); got != want {
		t.Errorf("for clause starts at %d, want %d", got, want)
	}
}

func TestStatementShapes(t *testing.T) {
	for _, tt := range []struct {
		source   string
		nodeType ParseNodeType
	}{
		{"if a:\n    pass\n", ParseNodeTypeIf},
		{"while a:\n    pass\n", ParseNodeTypeWhile},
		{"for a in b:\n    pass\n", ParseNodeTypeFor},
		{"try:\n    pass\nfinally:\n    pass\n", ParseNodeTypeTry},
		{"with a:\n    pass\n", ParseNodeTypeWith},
		{"def f():\n    pass\n", ParseNodeTypeFunction},
		{"class C:\n    pass\n", ParseNodeTypeClass},
		{"async def f():\n    pass\n", ParseNodeTypeFunction},
		{"@d\ndef f():\n    pass\n", ParseNodeTypeFunction},
		{"match a:\n    case 1:\n        pass\n", ParseNodeTypeMatch},
	} {
		results, sink := parseSource(t, tt.source)
		if errs := sink.GetErrors(); len(errs) != 0 {
			t.Errorf("%q produced %d unexpected errors: %v", tt.source, len(errs), errs[0].Message)
			continue
		}
		statements := results.ParserOutput.ParseTree.D.Statements
		if len(statements) != 1 {
			t.Errorf("%q produced %d statements, want 1", tt.source, len(statements))
			continue
		}
		if got := statements[0].GetNodeType(); got != tt.nodeType {
			t.Errorf("%q parsed as node type %d, want %d", tt.source, got, tt.nodeType)
		}
	}
}

func TestTypeAliasIsASmallStatement(t *testing.T) {
	// `type X = int` comes back from _parseSmallStatement, so it is wrapped in
	// a StatementList like any other one-line statement rather than appearing
	// directly among the module's statements.
	node := firstExpr(t, "type X = int\n")
	alias, ok := node.(*TypeAliasNode)
	if !ok {
		t.Fatalf("got %T, want *TypeAliasNode", node)
	}
	if alias.D.Name.D.Value != "X" {
		t.Errorf("alias name = %q, want X", alias.D.Name.D.Value)
	}
}

func TestTypeAliasTypeParameters(t *testing.T) {
	node := firstExpr(t, "type X[T] = list[T]\n")
	alias := node.(*TypeAliasNode)
	if alias.D.TypeParams == nil || len(alias.D.TypeParams.D.Params) != 1 {
		t.Fatalf("expected one type parameter, got %#v", alias.D.TypeParams)
	}
	if alias.D.TypeParams.D.Params[0].D.Name.D.Value != "T" {
		t.Error("expected the type parameter to be named T")
	}
}

func TestImportsAreRecorded(t *testing.T) {
	results, sink := parseSource(t, "import os.path\nfrom typing import Literal as L\nfrom __future__ import annotations\n")
	if errs := sink.GetErrors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs[0].Message)
	}

	// `import os.path` with no alias implicitly imports both `os` and
	// `os.path`, so three module imports in total.
	if len(results.ParserOutput.ImportedModules) != 4 {
		t.Errorf("got %d imported modules, want 4", len(results.ParserOutput.ImportedModules))
	}
	if !results.ParserOutput.FutureImports["annotations"] {
		t.Error("expected the __future__ import to be recorded")
	}
	if alias := results.ParserOutput.TypingSymbolAliases["L"]; alias != "Literal" {
		t.Errorf("typing alias L = %q, want Literal", alias)
	}
}

func TestWildcardImportIsRecorded(t *testing.T) {
	results, _ := parseSource(t, "from m import *\n")
	if !results.ParserOutput.ContainsWildcardImport {
		t.Error("expected containsWildcardImport to be set")
	}
}

func TestTypeAnnotationsAreFlagged(t *testing.T) {
	if results, _ := parseSource(t, "x = 1\n"); results.ParserOutput.HasTypeAnnotations {
		t.Error("an unannotated assignment should not set hasTypeAnnotations")
	}
	if results, _ := parseSource(t, "x: int = 1\n"); !results.ParserOutput.HasTypeAnnotations {
		t.Error("an annotated assignment should set hasTypeAnnotations")
	}
}

func TestForwardReferenceInAnnotationIsReparsed(t *testing.T) {
	// A string in an annotation position is re-parsed as an expression by a
	// nested Parser, and the result is hung off the StringListNode.
	results, sink := parseSource(t, "x: \"int\" = 1\n")
	if errs := sink.GetErrors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs[0].Message)
	}

	list := results.ParserOutput.ParseTree.D.Statements[0].(*StatementListNode)
	assignment := list.D.Statements[0].(*AssignmentNode)
	annotation := assignment.D.LeftExpr.(*TypeAnnotationNode).D.Annotation
	stringList, ok := annotation.(*StringListNode)
	if !ok {
		t.Fatalf("got %T, want *StringListNode", annotation)
	}
	if stringList.D.Annotation == nil {
		t.Fatal("expected the string contents to be parsed into an annotation")
	}
	name, ok := stringList.D.Annotation.(*NameNode)
	if !ok || name.D.Value != "int" {
		t.Errorf("annotation = %#v, want a NameNode for int", stringList.D.Annotation)
	}
	// The nested parse reports offsets into the original file, not into the
	// unescaped string, so the name lands on the `int` inside the quotes.
	if name.GetRange().Start != 4 {
		t.Errorf("annotation starts at %d, want 4", name.GetRange().Start)
	}
}

func TestChainedAssignmentNestsRight(t *testing.T) {
	// `a = b = c` becomes Assignment(b, Assignment(a, c)): the final value is
	// assigned to the targets left to right.
	node := firstExpr(t, "a = b = c\n")
	outer, ok := node.(*AssignmentNode)
	if !ok {
		t.Fatalf("got %T, want *AssignmentNode", node)
	}
	if left, ok := outer.D.LeftExpr.(*NameNode); !ok || left.D.Value != "b" {
		t.Errorf("outer target = %#v, want b", outer.D.LeftExpr)
	}
	inner, ok := outer.D.RightExpr.(*AssignmentNode)
	if !ok {
		t.Fatalf("inner is %T, want *AssignmentNode", outer.D.RightExpr)
	}
	if left, ok := inner.D.LeftExpr.(*NameNode); !ok || left.D.Value != "a" {
		t.Errorf("inner target = %#v, want a", inner.D.LeftExpr)
	}
}

func TestAugmentedAssignmentCopiesDest(t *testing.T) {
	// The dest expression is a shallow copy of the left expression with a fresh
	// node ID, so the binder can treat it as a separate write target.
	node := firstExpr(t, "a += 1\n")
	augmented, ok := node.(*AugmentedAssignmentNode)
	if !ok {
		t.Fatalf("got %T, want *AugmentedAssignmentNode", node)
	}
	if augmented.D.DestExpr.NodeBase().ID == augmented.D.LeftExpr.NodeBase().ID {
		t.Error("expected destExpr to have a distinct node ID")
	}
	if augmented.D.DestExpr.GetRange() != augmented.D.LeftExpr.GetRange() {
		t.Error("expected destExpr to keep the same range")
	}
	if augmented.D.DestExpr.GetNodeType() != augmented.D.LeftExpr.GetNodeType() {
		t.Error("expected destExpr to keep the same node type")
	}
}

func TestDiagnosticsForCommonErrors(t *testing.T) {
	for _, tt := range []struct {
		source   string
		minCount int
	}{
		{"def f(:\n    pass\n", 1},
		{"[1, 2\n", 1},
		{"{1: 2\n", 1},
		{"f(1\n", 1},
		{"a[\n", 1},
		{"class C\n    pass\n", 1},
		{"break\n", 1},          // outside a loop
		{"return 1\n", 1},       // outside a function
		{"try:\n    pass\n", 1}, // no except or finally
	} {
		_, sink := parseSource(t, tt.source)
		if got := len(sink.GetErrors()); got < tt.minCount {
			t.Errorf("%q produced %d errors, want at least %d", tt.source, got, tt.minCount)
		}
	}
}

func TestParseTextExpression(t *testing.T) {
	p := NewParser()
	text := common.NewText("a + b")
	results := p.ParseTextExpression(
		text, 0, text.Length(), NewParseOptions(), ParseTextModeExpression, 0, nil,
	)

	if len(results.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", results.Diagnostics[0].Message)
	}
	if results.ParseTree == nil || results.ParseTree.GetNodeType() != ParseNodeTypeBinaryOperation {
		t.Errorf("got %#v, want a binary operation", results.ParseTree)
	}
}

func TestParseTextExpressionReportsTrailingTokens(t *testing.T) {
	p := NewParser()
	text := common.NewText("a b")
	results := p.ParseTextExpression(
		text, 0, text.Length(), NewParseOptions(), ParseTextModeExpression, 0, nil,
	)

	if len(results.Diagnostics) == 0 {
		t.Error("expected a diagnostic for the unexpected trailing token")
	}
}

func TestParseTextExpressionFunctionAnnotation(t *testing.T) {
	p := NewParser()
	text := common.NewText("(int, str) -> bool")
	results := p.ParseTextExpression(
		text, 0, text.Length(), NewParseOptions(), ParseTextModeFunctionAnnotation, 0, nil,
	)

	annotation, ok := results.ParseTree.(*FunctionAnnotationNode)
	if !ok {
		t.Fatalf("got %T, want *FunctionAnnotationNode", results.ParseTree)
	}
	if len(annotation.D.ParamAnnotations) != 2 {
		t.Errorf("got %d parameter annotations, want 2", len(annotation.D.ParamAnnotations))
	}
}

func TestMatchIsASoftKeyword(t *testing.T) {
	// `match(x)` is a call, not a match statement; `match x:` is a match
	// statement. The parser decides by speculatively parsing the subject with
	// errors suppressed and rewinding.
	if got := firstExpr(t, "match(x)\n").GetNodeType(); got != ParseNodeTypeCall {
		t.Errorf("`match(x)` parsed as node type %d, want a call", got)
	}

	results, _ := parseSource(t, "match x:\n    case 1:\n        pass\n")
	if got := results.ParserOutput.ParseTree.D.Statements[0].GetNodeType(); got != ParseNodeTypeMatch {
		t.Errorf("`match x:` parsed as node type %d, want a match statement", got)
	}
}

func TestOrPatternMustBindTheSameNames(t *testing.T) {
	_, sink := parseSource(t, "match a:\n    case [x] | []:\n        pass\n")
	errs := sink.GetErrors()
	if len(errs) == 0 {
		t.Fatal("expected a diagnostic for the mismatched or-pattern targets")
	}
	// The addendum is indented with non-breaking spaces, as diagnostic.ts
	// writes them.
	if want := "\u00a0\u00a0"; !containsString(errs[0].Message, want) {
		t.Errorf("message %q does not contain the non-breaking space indent", errs[0].Message)
	}
}

func containsString(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
