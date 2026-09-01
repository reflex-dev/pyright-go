/*
 * parsetreeutils.go
 *
 * The "parsetreeutils" op, which backs the corpus differential in
 * tools/ts-bridge/compare-parsetreeutils.js.
 *
 * parseTreeUtils.test.ts cannot run against this port: it drives the fourslash
 * harness, which needs the binder and the import resolver. Instead this parses
 * a file, walks every node in pre-order, and evaluates the parseTreeUtils
 * functions that do not need a bound scope or file info. The Node side does the
 * same with the original TypeScript and the two dumps are diffed.
 *
 * Nodes are identified by their pre-order index rather than by node id, because
 * ids come from a per-process counter and would never line up.
 */

package main

import (
	"github.com/microsoft/pyright/go/analyzer"
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// ptuNode is one node's worth of results. Fields are omitted when they do not
// apply to the node's type, so the JSON stays comparable field by field.
type ptuNode struct {
	Type  string `json:"t"`
	Depth int    `json:"d"`

	Compliant bool `json:"c"`

	// Enclosing-node lookups, as pre-order indices or -1.
	EnclosingSuite            int `json:"es"`
	EnclosingClass            int `json:"ec"`
	EnclosingClassStopAtFunc  int `json:"ecf"`
	EnclosingModule           int `json:"em"`
	EnclosingFunction         int `json:"ef"`
	EnclosingLambda           int `json:"el"`
	EnclosingClassOrFunction  int `json:"ecof"`
	EnclosingClassOrFuncSuite int `json:"ecfs"`
	EnclosingSuiteOrModule    int `json:"esm"`
	EnclosingParam            int `json:"ep"`
	TypeAnnotationNode        int `json:"tan"`
	TypeVarScopeNode          int `json:"tvs"`

	WithinDefaultParamInit  bool `json:"wdpi"`
	WithinTypeAnnotation    bool `json:"wta"`
	WithinQuotedAnnotation  bool `json:"wtaq"`
	WithinAnnotationComment bool `json:"wac"`
	WithinLoop              bool `json:"wl"`
	WithinAssert            bool `json:"wae"`
	ContainsAwait           bool `json:"aw"`

	// Expression-only.
	Printed        *common.Text `json:"p,omitempty"`
	ParentAnnot    *int         `json:"pan,omitempty"`
	SimpleDefault  *bool        `json:"sd,omitempty"`
	VariableDocStr *int         `json:"vds,omitempty"`

	// Name-only.
	WriteAccess       *bool  `json:"wa,omitempty"`
	ImportModuleName  *bool  `json:"imn,omitempty"`
	ImportAlias       *bool  `json:"ia,omitempty"`
	FromImportModName *bool  `json:"fimn,omitempty"`
	FromImportName    *bool  `json:"fin,omitempty"`
	FromImportAlias   *bool  `json:"fia,omitempty"`
	LastNameOfModule  *bool  `json:"lnm,omitempty"`
	FirstNameOfDotted *bool  `json:"fnd,omitempty"`
	LastNameOfDotted  *bool  `json:"lnd,omitempty"`
	CallForName       *int   `json:"cfn,omitempty"`
	DecoratorForName  *int   `json:"dfn,omitempty"`
	DottedNameAsLast  *int   `json:"dnl,omitempty"`
	DottedName        *[]int `json:"dn,omitempty"`

	// Node-type specific.
	SuiteEmpty         *bool        `json:"se,omitempty"`
	FunctionSuiteEmpty *bool        `json:"fse,omitempty"`
	Unannotated        *bool        `json:"ua,omitempty"`
	ParamAnnotations   *[]int       `json:"pa,omitempty"`
	DocString          *common.Text `json:"ds,omitempty"`
	IsDocString        *bool        `json:"ids,omitempty"`
	DecoratorName      *string      `json:"dnm,omitempty"`
	ClassFullName      *string      `json:"cfnm,omitempty"`
	FutureImportOK     *bool        `json:"fio,omitempty"`
	StringValueRange   *[2]int      `json:"svr,omitempty"`
	ArgsRuntimeOrder   *[]int       `json:"aro,omitempty"`
	NamedTupleDefault  *bool        `json:"ntd,omitempty"`
	Chaining           *bool        `json:"ch,omitempty"`
	FinalAllowed       *bool        `json:"fa,omitempty"`
	RequiredAllowed    *bool        `json:"ra,omitempty"`
	MatchesParent      *bool        `json:"mp,omitempty"`
	PartialMatches     *bool        `json:"pm,omitempty"`
}

type ptuResponse struct {
	Nodes   []ptuNode `json:"nodes"`
	Offsets []int     `json:"offsets"`
}

func handleParseTreeUtils(req *request) (any, string) {
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

	sink := common.NewDiagnosticSink()
	p := parser.NewParser()
	parseResults := p.ParseSourceFile(common.Text(req.Text), options, sink)
	module := parseResults.ParserOutput.ParseTree

	var order []parser.ParseNode
	index := map[parser.ParseNode]int{}
	var collect func(node parser.ParseNode)
	collect = func(node parser.ParseNode) {
		index[node] = len(order)
		order = append(order, node)
		for _, child := range analyzer.GetChildNodes(node) {
			if child != nil {
				collect(child)
			}
		}
	}
	collect(module)

	idx := func(node parser.ParseNode) int {
		if node == nil {
			return -1
		}
		if i, ok := index[node]; ok {
			return i
		}
		return -2
	}
	idxPtr := func(node parser.ParseNode) *int {
		v := idx(node)
		return &v
	}
	boolPtr := func(v bool) *bool { return &v }

	nodes := make([]ptuNode, 0, len(order))
	for _, node := range order {
		entry := ptuNode{
			Type:                      analyzer.PrintParseNodeType(node.GetNodeType()),
			Depth:                     analyzer.GetNodeDepth(node),
			Compliant:                 analyzer.IsCompliantWithNodeRangeRules(node),
			EnclosingSuite:            idx(nilIfNilSuite(analyzer.GetEnclosingSuite(node))),
			EnclosingClass:            idx(nilIfNilClass(analyzer.GetEnclosingClass(node, false))),
			EnclosingClassStopAtFunc:  idx(nilIfNilClass(analyzer.GetEnclosingClass(node, true))),
			EnclosingModule:           enclosingModuleIndex(node, module, idx),
			EnclosingFunction:         idx(nilIfNilFunction(analyzer.GetEnclosingFunction(node))),
			EnclosingLambda:           idx(nilIfNilLambda(analyzer.GetEnclosingLambda(node))),
			EnclosingClassOrFunction:  idx(analyzer.GetEnclosingClassOrFunction(node)),
			EnclosingClassOrFuncSuite: idx(nilIfNilSuite(analyzer.GetEnclosingClassOrFunctionSuite(node))),
			EnclosingSuiteOrModule:    idx(analyzer.GetEnclosingSuiteOrModule(node, false, true)),
			EnclosingParam:            idx(nilIfNilParam(analyzer.GetEnclosingParam(node))),
			TypeAnnotationNode:        idx(nilIfNilAnnotation(analyzer.GetTypeAnnotationNode(node))),
			TypeVarScopeNode:          idx(nilIfNilTypeVarScope(analyzer.GetTypeVarScopeNode(node))),
			WithinDefaultParamInit:    analyzer.IsWithinDefaultParamInitializer(node),
			WithinTypeAnnotation:      analyzer.IsWithinTypeAnnotation(node, false),
			WithinQuotedAnnotation:    analyzer.IsWithinTypeAnnotation(node, true),
			WithinAnnotationComment:   analyzer.IsWithinAnnotationComment(node),
			WithinLoop:                analyzer.IsWithinLoop(node),
			WithinAssert:              analyzer.IsWithinAssertExpression(node),
			ContainsAwait:             analyzer.ContainsAwaitNode(node),
		}

		// parser.IsExpressionNode, not a type assertion to
		// parser.ExpressionNode: the Go union interface is wider than the
		// original's predicate.
		if expr, ok := node.(parser.ExpressionNode); ok && parser.IsExpressionNode(node) {
			printed := common.NewText(analyzer.PrintExpression(expr, analyzer.PrintExpressionFlagsNone))
			entry.Printed = &printed
			entry.ParentAnnot = idxPtr(nilIfNilExpression(analyzer.GetParentAnnotationNode(expr)))
			entry.SimpleDefault = boolPtr(analyzer.IsSimpleDefault(expr))
			entry.VariableDocStr = idxPtr(nilIfNilStringList(analyzer.GetVariableDocStringNode(expr)))
		}

		switch typed := node.(type) {
		case *parser.NameNode:
			entry.WriteAccess = boolPtr(analyzer.IsWriteAccess(typed))
			entry.ImportModuleName = boolPtr(analyzer.IsImportModuleName(typed))
			entry.ImportAlias = boolPtr(analyzer.IsImportAlias(typed))
			entry.FromImportModName = boolPtr(analyzer.IsFromImportModuleName(typed))
			entry.FromImportName = boolPtr(analyzer.IsFromImportName(typed))
			entry.FromImportAlias = boolPtr(analyzer.IsFromImportAlias(typed))
			entry.LastNameOfModule = boolPtr(analyzer.IsLastNameOfModuleName(typed))
			entry.FirstNameOfDotted = boolPtr(analyzer.IsFirstNameOfDottedName(typed))
			entry.LastNameOfDotted = boolPtr(analyzer.IsLastNameOfDottedName(typed))
			entry.CallForName = idxPtr(nilIfNilCall(analyzer.GetCallForName(typed)))
			entry.DecoratorForName = idxPtr(nilIfNilDecorator(analyzer.GetDecoratorForName(typed)))
			entry.DottedNameAsLast = idxPtr(analyzer.GetDottedNameWithGivenNodeAsLastName(typed))
			if names, ok := analyzer.GetDottedName(typed); ok {
				indices := make([]int, 0, len(names))
				for _, name := range names {
					indices = append(indices, idx(name))
				}
				entry.DottedName = &indices
			}
			entry.FinalAllowed = boolPtr(analyzer.IsFinalAllowedForAssignmentTarget(typed))
			entry.RequiredAllowed = boolPtr(analyzer.IsRequiredAllowedForAssignmentTarget(typed))

		case *parser.MemberAccessNode:
			entry.FinalAllowed = boolPtr(analyzer.IsFinalAllowedForAssignmentTarget(typed))
			if parentExpr, ok := node.NodeBase().Parent.(parser.ExpressionNode); ok {
				entry.MatchesParent = boolPtr(analyzer.IsMatchingExpression(typed, parentExpr, nil))
				entry.PartialMatches = boolPtr(analyzer.IsPartialMatchingExpression(typed, parentExpr))
			}

		case *parser.SuiteNode:
			entry.SuiteEmpty = boolPtr(analyzer.IsSuiteEmpty(typed))

		case *parser.FunctionNode:
			entry.FunctionSuiteEmpty = boolPtr(analyzer.IsFunctionSuiteEmpty(typed))
			entry.Unannotated = boolPtr(analyzer.IsUnannotatedFunction(typed))
			annotations := make([]int, 0, len(typed.D.Params)+1)
			for i := 0; i <= len(typed.D.Params); i++ {
				annotations = append(annotations, idx(nilIfNilExpression(analyzer.GetTypeAnnotationForParam(typed, i))))
			}
			entry.ParamAnnotations = &annotations

		case *parser.StatementListNode:
			entry.IsDocString = boolPtr(analyzer.IsDocString(typed))

		case *parser.ModuleNode:
			if doc, ok := analyzer.GetDocString(typed.D.Statements); ok {
				text := common.NewText(doc)
				entry.DocString = &text
			}

		case *parser.DecoratorNode:
			if name, ok := analyzer.GetDecoratorName(typed); ok {
				entry.DecoratorName = &name
			}

		case *parser.ClassNode:
			fullName := analyzer.GetClassFullName(typed, "mod", typed.D.Name.D.Value)
			entry.ClassFullName = &fullName

		case *parser.ImportFromNode:
			entry.FutureImportOK = boolPtr(analyzer.IsValidLocationForFutureImport(typed))

		case *parser.StringNode:
			r := analyzer.GetStringNodeValueRange(typed)
			pair := [2]int{r.Start, r.Length}
			entry.StringValueRange = &pair

		case *parser.CallNode:
			args := analyzer.GetArgsByRuntimeOrder(typed)
			indices := make([]int, 0, len(args))
			for _, arg := range args {
				indices = append(indices, idx(arg))
			}
			entry.ArgsRuntimeOrder = &indices
			entry.NamedTupleDefault = boolPtr(analyzer.IsAssignmentToDefaultsFollowingNamedTuple(typed))

		case *parser.BinaryOperationNode:
			entry.Chaining = boolPtr(analyzer.OperatorSupportsChaining(typed.D.Operator))
		}

		nodes = append(nodes, entry)
	}

	moduleRange := module.NodeBase()
	offsets := make([]int, 0, moduleRange.Length+1)
	for offset := 0; offset <= moduleRange.Length; offset++ {
		offsets = append(offsets, idx(analyzer.FindNodeByOffset(module, offset)))
	}

	return ptuResponse{Nodes: nodes, Offsets: offsets}, ""
}

// enclosingModuleIndex skips GetEnclosingModule for the module node itself:
// the walk starts at the parent, so on the root it reaches the end and calls
// fail(), exactly as the original does.
func enclosingModuleIndex(node parser.ParseNode, module *parser.ModuleNode, idx func(parser.ParseNode) int) int {
	if node == parser.ParseNode(module) {
		return -1
	}
	return idx(nilIfNilModule(analyzer.GetEnclosingModule(node)))
}

// The nilIfNilX helpers exist because a typed nil pointer stored in a ParseNode
// interface is not equal to nil, so passing one straight to idx would look like
// a real node.
func nilIfNilSuite(n *parser.SuiteNode) parser.ParseNode {
	if n == nil {
		return nil
	}
	return n
}
func nilIfNilClass(n *parser.ClassNode) parser.ParseNode {
	if n == nil {
		return nil
	}
	return n
}
func nilIfNilModule(n *parser.ModuleNode) parser.ParseNode {
	if n == nil {
		return nil
	}
	return n
}
func nilIfNilFunction(n *parser.FunctionNode) parser.ParseNode {
	if n == nil {
		return nil
	}
	return n
}
func nilIfNilLambda(n *parser.LambdaNode) parser.ParseNode {
	if n == nil {
		return nil
	}
	return n
}
func nilIfNilParam(n *parser.ParameterNode) parser.ParseNode {
	if n == nil {
		return nil
	}
	return n
}
func nilIfNilAnnotation(n *parser.TypeAnnotationNode) parser.ParseNode {
	if n == nil {
		return nil
	}
	return n
}
func nilIfNilCall(n *parser.CallNode) parser.ParseNode {
	if n == nil {
		return nil
	}
	return n
}
func nilIfNilDecorator(n *parser.DecoratorNode) parser.ParseNode {
	if n == nil {
		return nil
	}
	return n
}
func nilIfNilStringList(n *parser.StringListNode) parser.ParseNode {
	if n == nil {
		return nil
	}
	return n
}
func nilIfNilExpression(n parser.ExpressionNode) parser.ParseNode {
	if n == nil {
		return nil
	}
	return n
}
func nilIfNilTypeVarScope(n parser.TypeParameterScopeNode) parser.ParseNode {
	if n == nil {
		return nil
	}
	return n
}
