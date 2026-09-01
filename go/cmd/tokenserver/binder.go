/*
 * binder.go
 *
 * The "binder" op, which backs the corpus differential in
 * tools/ts-bridge/compare-binder.js.
 *
 * binder.ts has no bridgeable test -- the tests that exercise it drive the
 * fourslash harness -- so, as with parseTreeUtils, this dumps everything the
 * binder produces and the Node side does the same with the original TypeScript.
 * See tools/ts-bridge/dump-binder.ts, which this mirrors field for field; the
 * JSON tags here are its property names.
 *
 * Parse nodes are keyed by pre-order index, and flow nodes and symbols are
 * renumbered by first-sight order in a fixed traversal, because all three
 * would otherwise be per-process counters that never line up.
 */

package main

import (
	"sort"

	"github.com/microsoft/pyright/go/analyzer"
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/parser"
)

// binderFileUri is the single fixed URI the harness uses for everything. The
// binder only ever copies it into declarations.
var binderFileUri = uri.Empty()

type binderNode struct {
	Scope         int       `json:"s"`
	FlowNode      int       `json:"f"`
	AfterFlowNode int       `json:"af"`
	Unreachable   bool      `json:"u"`
	Decl          any       `json:"d,omitempty"`
	CodeFlowExprs *[]string `json:"cfe,omitempty"`
	CodeFlowCmplx *float64  `json:"cfc,omitempty"`
	StaticCondVal *bool     `json:"scv,omitempty"`
}

type binderSymbol struct {
	Name  string `json:"name"`
	ID    int    `json:"id"`
	Flags int    `json:"flags"`
	Decls []any  `json:"decls"`
}

type binderScope struct {
	Type                             int            `json:"type"`
	Parent                           int            `json:"parent"`
	Proxy                            int            `json:"proxy"`
	HasChainedLookup                 bool           `json:"hasChainedLookup"`
	Symbols                          []binderSymbol `json:"symbols"`
	NotLocalBindings                 [][]any        `json:"notLocalBindings"`
	SlotsNames                       *[]string      `json:"slotsNames,omitempty"`
	HasNonEmptySlots                 bool           `json:"hasNonEmptySlots"`
	HasPotentiallyDynamicSymbolTable bool           `json:"hasPotentiallyDynamicSymbolTable"`
}

type binderDunderAll struct {
	Names                        []string `json:"names"`
	StringNodes                  []int    `json:"stringNodes"`
	UsesUnsupportedDunderAllForm bool     `json:"usesUnsupportedDunderAllForm"`
}

type binderDiagnostic struct {
	Category int         `json:"category"`
	Message  common.Text `json:"message"`
	Range    [4]int      `json:"range"`
	Rule     *string     `json:"rule"`
	Actions  []string    `json:"actions"`
}

type binderResult struct {
	Nodes       []binderNode       `json:"nodes"`
	Scopes      []binderScope      `json:"scopes"`
	Flows       []map[string]any   `json:"flows"`
	DunderAll   *binderDunderAll   `json:"dunderAll,omitempty"`
	Diagnostics []binderDiagnostic `json:"diagnostics"`
}

// makeImportResult mirrors dump-binder.ts's synthetic ImportResult. The import
// resolver is Stage C, and visitModuleName asserts that every ModuleName node
// carries one, so the harness supplies it on both sides.
func makeImportResult(nameParts []string, importsResolve bool) *analyzer.ImportResult {
	importName := ""
	for i, part := range nameParts {
		if i > 0 {
			importName += "."
		}
		importName += part
	}

	resolvedUris := make([]uri.Uri, 0, len(nameParts))
	for range nameParts {
		resolvedUris = append(resolvedUris, binderFileUri)
	}

	return &analyzer.ImportResult{
		ImportName:              importName,
		IsRelative:              false,
		IsNativeLib:             false,
		IsImportFound:           importsResolve,
		IsPartlyResolved:        false,
		IsNamespacePackage:      false,
		IsInitFilePresent:       importsResolve,
		IsStubPackage:           false,
		ImportFailureInfo:       []string{},
		ImportType:              analyzer.ImportTypeLocal,
		ResolvedUris:            resolvedUris,
		SearchPath:              binderFileUri,
		IsStubFile:              false,
		ImplicitImports:         common.NewOrderedMap[string, *analyzer.ImplicitImport](),
		FilteredImplicitImports: common.NewOrderedMap[string, *analyzer.ImplicitImport](),
	}
}

func installImportResults(node parser.ParseNode, importsResolve bool) {
	if moduleName, ok := node.(*parser.ModuleNameNode); ok {
		parts := make([]string, 0, len(moduleName.D.NameParts))
		for _, part := range moduleName.D.NameParts {
			parts = append(parts, part.D.Value)
		}
		analyzer.SetImportInfo(moduleName, makeImportResult(parts, importsResolve))
	}

	for _, child := range analyzer.GetChildNodes(node) {
		if child != nil {
			installImportResults(child, importsResolve)
		}
	}
}

func handleBinder(req *request) (any, string) {
	options := parser.NewParseOptions()
	sink := common.NewDiagnosticSink()
	p := parser.NewParser()
	parseResults := p.ParseSourceFile(common.Text(req.Text), options, sink)
	module := parseResults.ParserOutput.ParseTree

	// Pre-order parse node index, exactly as in the parseTreeUtils op.
	var order []parser.ParseNode
	nodeIndex := map[parser.ParseNode]int{}
	var collect func(node parser.ParseNode)
	collect = func(node parser.ParseNode) {
		nodeIndex[node] = len(order)
		order = append(order, node)
		for _, child := range analyzer.GetChildNodes(node) {
			if child != nil {
				collect(child)
			}
		}
	}
	collect(module)

	nodeIdx := func(node parser.ParseNode) int {
		if node == nil {
			return -1
		}
		if i, ok := nodeIndex[node]; ok {
			return i
		}
		// -2 means "the binder produced a node that is not in the parse tree",
		// which would be a real finding rather than a harness bug.
		return -2
	}

	installImportResults(module, req.ImportsResolve)

	lines := parseResults.TokenizerOutput.Lines
	bindDiagnostics := common.NewTextRangeDiagnosticSink(lines)

	fileInfo := &analyzer.AnalyzerFileInfo{
		ImportLookup: func(uri.Uri, *analyzer.AbsoluteModuleDescriptor, *analyzer.LookupImportOptions) *analyzer.ImportLookupResult {
			return nil
		},
		FutureImports:  common.NewOrderedSet[string](),
		BuiltinsScope:  nil,
		DiagnosticSink: bindDiagnostics,
		ExecutionEnvironment: analyzer.NewExecutionEnvironment(
			"python",
			binderFileUri,
			analyzer.GetStandardDiagnosticRuleSet(),
			nil,
			"",
			nil,
			false,
		),
		DiagnosticRuleSet:   analyzer.GetStandardDiagnosticRuleSet(),
		Lines:               lines,
		TypingSymbolAliases: typingSymbolAliasesOf(parseResults.ParserOutput.TypingSymbolAliases),
		DefinedConstants:    common.NewOrderedMap[string, analyzer.DefinedConstantValue](),
		FileID:              "file",
		FileUri:             binderFileUri,
		ModuleName:          "mod",
		IPythonMode:         analyzer.IPythonModeNone,
		AccessedSymbolSet:   common.NewOrderedSet[int](),
	}

	analyzer.SetFileInfo(module, fileInfo)

	binder := analyzer.NewBinder(fileInfo, false /* moduleSymbolOnly */, nil /* cellChainIndex */)
	binder.BindModule(module)

	// ---------------------------------------------------------- registries

	var flowOrder []analyzer.FlowNode
	flowIndex := map[analyzer.FlowNode]int{}
	flowIdx := func(flow analyzer.FlowNode) int {
		if flow == nil {
			return -1
		}
		if i, ok := flowIndex[flow]; ok {
			return i
		}
		i := len(flowOrder)
		flowIndex[flow] = i
		flowOrder = append(flowOrder, flow)
		return i
	}

	symbolIndex := map[*analyzer.Symbol]int{}
	symbolIdxByID := map[int]int{}
	symbolIdx := func(symbol *analyzer.Symbol) int {
		if i, ok := symbolIndex[symbol]; ok {
			return i
		}
		i := len(symbolIndex)
		symbolIndex[symbol] = i
		symbolIdxByID[symbol.ID] = i
		return i
	}

	var scopeOrder []*analyzer.Scope
	scopeIndex := map[*analyzer.Scope]int{}
	scopeIdx := func(scope *analyzer.Scope) int {
		if scope == nil {
			return -1
		}
		if i, ok := scopeIndex[scope]; ok {
			return i
		}
		i := len(scopeOrder)
		scopeIndex[scope] = i
		scopeOrder = append(scopeOrder, scope)
		return i
	}

	// ------------------------------------------------------- declarations

	var dumpDeclaration func(decl analyzer.Declaration) any
	var dumpLoaderActions func(actions *common.OrderedMap[string, *analyzer.ModuleLoaderActions]) any

	describeUri := func(u uri.Uri) any {
		if u == nil {
			return nil
		}
		if u.Equals(binderFileUri) {
			return "file"
		}
		return "other"
	}

	dumpLoaderActions = func(actions *common.OrderedMap[string, *analyzer.ModuleLoaderActions]) any {
		if actions == nil {
			return nil
		}
		out := []any{}
		actions.ForEach(func(action *analyzer.ModuleLoaderActions, name string) {
			out = append(out, map[string]any{
				"name":                name,
				"uri":                 describeUri(action.Uri),
				"isUnresolved":        action.IsUnresolved,
				"loadSymbolsFromPath": action.LoadSymbolsFromPath,
				"implicitImports":     dumpLoaderActions(action.ImplicitImports),
			})
		})
		return out
	}

	nodeIdxSlice := func(nodes []parser.ParseNode) []int {
		out := make([]int, 0, len(nodes))
		for _, node := range nodes {
			out = append(out, nodeIdx(node))
		}
		return out
	}

	dumpDeclaration = func(decl analyzer.Declaration) any {
		base := decl.DeclBase()
		entry := map[string]any{
			"t": declarationTypeName(base.Type),
			"n": nodeIdx(base.Node),
			"r": []int{
				base.Range.Start.Line, base.Range.Start.Character,
				base.Range.End.Line, base.Range.End.Character,
			},
			"m":   base.ModuleName,
			"uri": describeUri(base.Uri),
			"ex":  base.IsInExceptSuite,
			"itd": base.IsInInlinedTypedDict,
		}

		switch typed := decl.(type) {
		case *analyzer.IntrinsicDeclaration:
			entry["name"] = typed.Name
			entry["it"] = typed.IntrinsicType

		case *analyzer.FunctionDeclaration:
			entry["isMethod"] = typed.IsMethod
			entry["isGenerator"] = typed.IsGenerator
			returns := make([]int, 0, len(typed.ReturnStatements))
			for _, r := range typed.ReturnStatements {
				returns = append(returns, nodeIdx(r))
			}
			entry["returns"] = returns
			entry["yields"] = nodeIdxSlice(typed.YieldStatements)
			raises := make([]int, 0, len(typed.RaiseStatements))
			for _, r := range typed.RaiseStatements {
				raises = append(raises, nodeIdx(r))
			}
			entry["raises"] = raises

		case *analyzer.ParamDeclaration:
			entry["inferredName"] = typed.InferredName
			inferred := make([]int, 0, len(typed.InferredTypeNodes))
			for _, n := range typed.InferredTypeNodes {
				inferred = append(inferred, nodeIdx(n))
			}
			entry["inferredTypeNodes"] = inferred

		case *analyzer.TypeAliasDeclaration:
			entry["docString"] = encodeOptionalString(typed.DocString)

		case *analyzer.VariableDeclaration:
			entry["typeAnnotationNode"] = nodeIdx(exprOrNil(typed.TypeAnnotationNode))
			entry["inferredTypeSource"] = nodeIdx(typed.InferredTypeSource)
			entry["isConstant"] = typed.IsConstant
			entry["isFinal"] = typed.IsFinal
			entry["isDefinedBySlots"] = typed.IsDefinedBySlots
			entry["isInferenceAllowedInPyTyped"] = typed.IsInferenceAllowedInPyTyped
			entry["isRuntimeTypeExpression"] = typed.IsRuntimeTypeExpression
			if typed.TypeAliasName != nil {
				entry["typeAliasName"] = nodeIdx(typed.TypeAliasName)
			} else {
				entry["typeAliasName"] = -1
			}
			entry["isDefinedByMemberAccess"] = typed.IsDefinedByMemberAccess
			entry["docString"] = encodeOptionalString(typed.DocString)
			entry["alternativeTypeNode"] = nodeIdx(exprOrNil(typed.AlternativeTypeNode))
			entry["isExplicitBinding"] = typed.IsExplicitBinding

		case *analyzer.AliasDeclaration:
			entry["usesLocalName"] = typed.UsesLocalName
			entry["loadSymbolsFromPath"] = typed.LoadSymbolsFromPath
			entry["symbolName"] = typed.SymbolName
			if typed.SubmoduleFallback != nil {
				entry["submoduleFallback"] = dumpDeclaration(typed.SubmoduleFallback)
			}
			entry["firstNamePart"] = typed.FirstNamePart
			entry["implicitImports"] = dumpLoaderActions(typed.ImplicitImports)
			entry["isUnresolved"] = typed.IsUnresolved
			entry["isNativeLib"] = typed.IsNativeLib
			entry["isLazy"] = typed.IsLazy
		}

		return entry
	}

	// ------------------------------------------------------------ per node

	nodes := make([]binderNode, 0, len(order))
	for _, node := range order {
		entry := binderNode{
			Scope:         scopeIdx(analyzer.GetScope(node)),
			FlowNode:      flowIdx(analyzer.GetFlowNode(node)),
			AfterFlowNode: flowIdx(analyzer.GetAfterFlowNode(node)),
			Unreachable:   analyzer.IsCodeUnreachable(node),
		}

		if decl := analyzer.GetDeclaration(node); decl != nil {
			entry.Decl = dumpDeclaration(decl)
		}

		switch node.GetNodeType() {
		case parser.ParseNodeTypeModule,
			parser.ParseNodeTypeFunction,
			parser.ParseNodeTypeLambda,
			parser.ParseNodeTypeComprehension,
			parser.ParseNodeTypeTypeParameterList:
			scoped := node.(analyzer.ScopedNode)
			if expressions := analyzer.GetCodeFlowExpressions(scoped); expressions != nil {
				values := append([]string(nil), expressions.Values()...)
				sortStrings(values)
				entry.CodeFlowExprs = &values
			}
			if complexity := analyzer.GetCodeFlowComplexity(scoped); complexity != 0 {
				entry.CodeFlowCmplx = &complexity
			}

		case parser.ParseNodeTypeIf:
			if value := analyzer.GetStaticConditionValue(node.(*parser.IfNode)); value != nil {
				entry.StaticCondVal = value
			}
		}

		nodes = append(nodes, entry)
	}

	// -------------------------------------------------------- the drains

	scopes := []binderScope{}
	for i := 0; i < len(scopeOrder); i++ {
		scope := scopeOrder[i]

		symbols := []binderSymbol{}
		scope.SymbolTable.ForEach(func(symbol *analyzer.Symbol, name string) {
			decls := []any{}
			for _, decl := range symbol.GetDeclarations() {
				decls = append(decls, dumpDeclaration(decl))
			}
			symbols = append(symbols, binderSymbol{
				Name:  name,
				ID:    symbolIdx(symbol),
				Flags: symbolFlags(symbol),
				Decls: decls,
			})
		})

		notLocalBindings := [][]any{}
		scope.NotLocalBindings.ForEach(func(bindingType analyzer.NameBindingType, name string) {
			// NameBindingTypeNone occupies 0 here so getBindingType can return a
			// single value; the original enum starts at Nonlocal = 0, so shift.
			notLocalBindings = append(notLocalBindings, []any{name, int(bindingType) - 1})
		})

		entry := binderScope{
			Type:                             int(scope.Type),
			Parent:                           scopeIdx(scope.Parent),
			Proxy:                            scopeIdx(scope.Proxy),
			HasChainedLookup:                 scope.ChainedModuleLevelScopeLookup != nil,
			Symbols:                          symbols,
			NotLocalBindings:                 notLocalBindings,
			HasNonEmptySlots:                 scope.HasNonEmptySlots,
			HasPotentiallyDynamicSymbolTable: scope.HasPotentiallyDynamicSymbolTable,
		}
		if scope.SlotsNames != nil {
			names := scope.SlotsNames
			entry.SlotsNames = &names
		}
		scopes = append(scopes, entry)
	}

	// Dumping a flow node registers its antecedents, so this walks a list that
	// grows underneath it until it settles.
	flows := []map[string]any{}
	for i := 0; i < len(flowOrder); i++ {
		flow := flowOrder[i]
		flags := flow.FlowBase().Flags
		entry := map[string]any{"flags": int(flags)}

		if label := flowLabelOf(flow); label != nil &&
			flags&(analyzer.FlowFlagsBranchLabel|analyzer.FlowFlagsLoopLabel|analyzer.FlowFlagsPostContextManager) != 0 {
			antecedents := make([]int, 0, len(label.Antecedents))
			for _, antecedent := range label.Antecedents {
				antecedents = append(antecedents, flowIdx(antecedent))
			}
			entry["antecedents"] = antecedents
			if label.AffectedExpressions != nil {
				values := append([]string(nil), label.AffectedExpressions.Values()...)
				sortStrings(values)
				entry["affected"] = values
			}
		}

		// The TypeScript tests the BranchLabel *flag*, not the node's type, and
		// a context manager label carries that flag too -- so it emits
		// preBranch as -1 for one, having no such property. Matching on the
		// flag keeps the two dumps aligned.
		if flags&analyzer.FlowFlagsBranchLabel != 0 {
			var preBranch analyzer.FlowNode
			if branch, ok := flow.(*analyzer.FlowBranchLabel); ok {
				preBranch = branch.PreBranchAntecedent
			}
			entry["preBranch"] = flowIdx(preBranch)
		}

		switch typed := flow.(type) {
		case *analyzer.FlowPostContextManagerLabel:
			entry["expressions"] = exprIdxSlice(typed.Expressions, nodeIdx)
			entry["isAsync"] = typed.IsAsync
			entry["blockIfSwallows"] = typed.BlockIfSwallowsExceptions

		case *analyzer.FlowAssignment:
			entry["antecedent"] = flowIdx(typed.Antecedent)
			entry["node"] = nodeIdx(typed.Node)
			if typed.TargetSymbolID == analyzer.IndeterminateSymbolID {
				entry["target"] = -1
			} else if i, ok := symbolIdxByID[typed.TargetSymbolID]; ok {
				entry["target"] = i
			} else {
				entry["target"] = -2
			}

		case *analyzer.FlowVariableAnnotation:
			entry["antecedent"] = flowIdx(typed.Antecedent)

		case *analyzer.FlowWildcardImport:
			entry["antecedent"] = flowIdx(typed.Antecedent)
			entry["node"] = nodeIdx(typed.Node)
			entry["names"] = typed.Names

		case *analyzer.FlowCondition:
			entry["antecedent"] = flowIdx(typed.Antecedent)
			entry["expression"] = nodeIdx(typed.Expression)
			if typed.Reference != nil {
				entry["reference"] = nodeIdx(typed.Reference)
			}

		case *analyzer.FlowNarrowForPattern:
			entry["antecedent"] = flowIdx(typed.Antecedent)
			entry["subject"] = nodeIdx(typed.SubjectExpression)
			entry["statement"] = nodeIdx(typed.Statement)

		case *analyzer.FlowExhaustedMatch:
			entry["antecedent"] = flowIdx(typed.Antecedent)
			entry["node"] = nodeIdx(typed.Node)
			entry["subject"] = nodeIdx(typed.SubjectExpression)

		case *analyzer.FlowCall:
			entry["antecedent"] = flowIdx(typed.Antecedent)
			entry["node"] = nodeIdx(typed.Node)

		case *analyzer.FlowPreFinallyGate:
			entry["antecedent"] = flowIdx(typed.Antecedent)

		case *analyzer.FlowPostFinally:
			entry["antecedent"] = flowIdx(typed.Antecedent)
			entry["finallyNode"] = nodeIdx(typed.FinallyNode)
			entry["preFinallyGate"] = flowIdx(typed.PreFinallyGate)
		}

		flows = append(flows, entry)
	}

	// ---------------------------------------------------------- the rest

	var dunderAll *binderDunderAll
	if info := analyzer.GetDunderAllInfo(module); info != nil {
		stringNodes := make([]int, 0, len(info.StringNodes))
		for _, stringNode := range info.StringNodes {
			stringNodes = append(stringNodes, nodeIdx(stringNode))
		}
		dunderAll = &binderDunderAll{
			Names:                        info.Names,
			StringNodes:                  stringNodes,
			UsesUnsupportedDunderAllForm: info.UsesUnsupportedDunderAllForm,
		}
	}

	diagnostics := []binderDiagnostic{}
	for _, diag := range bindDiagnostics.FetchAndClear() {
		actions := []string{}
		for _, action := range diag.GetActions() {
			actions = append(actions, action.ActionName())
		}
		diagnostics = append(diagnostics, binderDiagnostic{
			Category: int(diag.Category),
			Message:  common.NewText(diag.Message),
			Range: [4]int{
				diag.Range.Start.Line, diag.Range.Start.Character,
				diag.Range.End.Line, diag.Range.End.Character,
			},
			Rule:    diag.GetRule(),
			Actions: actions,
		})
	}

	return binderResult{
		Nodes:       nodes,
		Scopes:      scopes,
		Flows:       flows,
		DunderAll:   dunderAll,
		Diagnostics: diagnostics,
	}, ""
}

// flowLabelOf returns the embedded FlowLabel for the three label forms and nil
// otherwise.
func flowLabelOf(flow analyzer.FlowNode) *analyzer.FlowLabel {
	switch typed := flow.(type) {
	case *analyzer.FlowLabel:
		return typed
	case *analyzer.FlowBranchLabel:
		return &typed.FlowLabel
	case *analyzer.FlowPostContextManagerLabel:
		return &typed.FlowLabel
	}
	return nil
}

func exprIdxSlice(exprs []parser.ExpressionNode, nodeIdx func(parser.ParseNode) int) []int {
	out := make([]int, 0, len(exprs))
	for _, expr := range exprs {
		out = append(out, nodeIdx(expr))
	}
	return out
}

// exprOrNil normalizes an absent optional expression to a nil interface, the
// same hazard childOrNil handles in the generated walker.
func exprOrNil(expr parser.ExpressionNode) parser.ParseNode {
	if expr == nil {
		return nil
	}
	return expr
}

func encodeOptionalString(value *string) any {
	if value == nil {
		return nil
	}
	return common.NewText(*value)
}

// typingSymbolAliasesOf converts the parser's plain map into the OrderedMap
// AnalyzerFileInfo holds. The keys are sorted so the conversion is
// deterministic; the binder never reads this field, and nothing in the dump
// depends on it.
func typingSymbolAliasesOf(aliases map[string]string) *common.OrderedMap[string, string] {
	out := common.NewOrderedMap[string, string]()
	keys := make([]string, 0, len(aliases))
	for key := range aliases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out.Set(key, aliases[key])
	}
	return out
}

// symbolFlags rebuilds the SymbolFlags bit set from the predicates, exactly as
// dump-binder.ts does. Symbol's flags field is private in both languages and
// neither exposes an accessor, so both sides reconstruct it the same way.
func symbolFlags(symbol *analyzer.Symbol) int {
	flags := 0
	bits := []struct {
		predicate func() bool
		bit       int
	}{
		{symbol.IsInitiallyUnbound, 1 << 0},
		{symbol.IsExternallyHidden, 1 << 1},
		{symbol.IsClassMember, 1 << 2},
		{symbol.IsInstanceMember, 1 << 3},
		{symbol.IsSlotsMember, 1 << 4},
		{symbol.IsPrivateMember, 1 << 5},
		{symbol.IsIgnoredForProtocolMatch, 1 << 6},
		{symbol.IsClassVar, 1 << 7},
		{symbol.IsInDunderAll, 1 << 8},
		{symbol.IsPrivatePyTypedImport, 1 << 9},
		{symbol.IsInitVar, 1 << 10},
		{symbol.IsNamedTupleMemberMember, 1 << 11},
		{symbol.IsIgnoredForOverrideChecks, 1 << 12},
		{symbol.IsFinalVarInClassBody, 1 << 13},
		{symbol.IsDataClassKeywordOnly, 1 << 14},
	}
	for _, entry := range bits {
		if entry.predicate() {
			flags |= entry.bit
		}
	}
	return flags
}

// declarationTypeName mirrors `DeclarationType[decl.type]` on the TypeScript
// side, which yields the enum member's name.
func declarationTypeName(declType analyzer.DeclarationType) string {
	switch declType {
	case analyzer.DeclarationTypeIntrinsic:
		return "Intrinsic"
	case analyzer.DeclarationTypeVariable:
		return "Variable"
	case analyzer.DeclarationTypeParam:
		return "Param"
	case analyzer.DeclarationTypeTypeParam:
		return "TypeParam"
	case analyzer.DeclarationTypeTypeAlias:
		return "TypeAlias"
	case analyzer.DeclarationTypeFunction:
		return "Function"
	case analyzer.DeclarationTypeClass:
		return "Class"
	case analyzer.DeclarationTypeSpecialBuiltInClass:
		return "SpecialBuiltInClass"
	case analyzer.DeclarationTypeAlias:
		return "Alias"
	}
	return "Unknown"
}
