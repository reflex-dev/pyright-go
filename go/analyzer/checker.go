/*
 * checker.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412): the Checker class
 * itself -- its construction, check(), the walk override, and the two visit
 * methods that drive evaluation of every statement in a file.
 *
 * This is the missing half of the gate's supply chain. sourcefile.go runs the
 * checker and drains the diagnostic sink inside one `if s.checkerFactory != nil`
 * block, so without a checker installed, nothing walks a file to drive the
 * evaluator and nothing collects what the evaluator writes. Porting the
 * evaluator could never have moved the gate on its own.
 *
 * The Checker is a ParseTreeWalker subclass, and it overrides walk() rather than
 * only the per-node visit methods: code the binder marked unreachable is still
 * walked, but with diagnostics suppressed. That override has to be reached by
 * the walker's own recursion, so Walk is part of ParseTreeVisitorOverrides and
 * WalkMultiple dispatches through self -- see the regenerated
 * parsetreewalker.go.
 *
 * The 52 visit methods of the original are not all here. This lands the walk and
 * the two methods that make it do work; the remaining per-node checks arrive
 * with the machinery each one needs, and until then a node type falls through to
 * the walker's default of "visit my children", which is the original's behaviour
 * for a node it has no override for.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// Checker corresponds to the class of the same name.
type Checker struct {
	*ParseTreeWalker

	importResolver *ImportResolver
	evaluator      TypeEvaluator
	dependentFiles []*parser.ParserOutput

	moduleNode *parser.ModuleNode
	fileInfo   *AnalyzerFileInfo

	isUnboundCheckSuppressed bool

	// scopedNodes is the original's comment: a list of all nodes that are
	// defined within the module that have their own scopes.
	scopedNodes []parser.ParseNode

	// typeParamLists is the original's comment: a list of all visited type
	// parameter lists.
	typeParamLists []*parser.TypeParameterListNode

	// multipartImports is the original's comment: a list of all visited
	// multipart import statements.
	multipartImports []*parser.ImportAsNode
}

// NewChecker corresponds to the constructor.
func NewChecker(
	importResolver *ImportResolver,
	evaluator TypeEvaluator,
	parserOutput *parser.ParserOutput,
	dependentFiles []*parser.ParserOutput,
) *Checker {
	c := &Checker{
		importResolver: importResolver,
		evaluator:      evaluator,
		dependentFiles: dependentFiles,
		moduleNode:     parserOutput.ParseTree,
	}
	c.fileInfo = GetFileInfo(c.moduleNode)
	c.ParseTreeWalker = NewParseTreeWalker(c)
	return c
}

// Check corresponds to check().
func (c *Checker) Check() {
	c.scopedNodes = append(c.scopedNodes, c.moduleNode)

	// The original's comment: report code complexity issues for the module.
	codeComplexity := GetCodeFlowComplexity(c.moduleNode)

	// The original logs the complexity here when isPrintCodeComplexityEnabled,
	// a constant that is false in the shipped source.

	if codeComplexity > MaxCodeComplexity {
		c.evaluator.AddDiagnosticForTextRange(
			c.fileInfo,
			DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.CodeTooComplexToAnalyze(),
			common.TextRange{Start: 0, Length: 0},
		)
	}

	c.walkStatementsAndReportUnreachable(c.moduleNode.D.Statements)

	// The original's comment: mark symbols accessed by __all__ as accessed.
	if dunderAllInfo := GetDunderAllInfo(c.moduleNode); dunderAllInfo != nil {
		c.evaluator.MarkNamesAccessed(c.moduleNode, dunderAllInfo.Names)

		c.reportUnusedDunderAllSymbols(dunderAllInfo.StringNodes)
	}

	// The original's comment: perform a one-time validation of symbols in all
	// scopes defined in this module for things like unaccessed variables.
	c.validateSymbolTables()

	c.reportUnusedMultipartImports()

	c.reportDuplicateImports()
}

// Walk corresponds to the walk override. The original's shape: unreachable code
// is still walked, but with diagnostics suppressed, so that symbols in it are
// still marked accessed.
func (c *Checker) Walk(node parser.ParseNode) {
	if !IsCodeUnreachable(node) {
		c.ParseTreeWalker.Walk(node)
		return
	}

	c.evaluator.SuppressDiagnostics(node, func() {
		c.ParseTreeWalker.Walk(node)
	})
}

// VisitSuite corresponds to visitSuite.
func (c *Checker) VisitSuite(node *parser.SuiteNode) bool {
	c.walkStatementsAndReportUnreachable(node.D.Statements)
	return false
}

// VisitStatementList corresponds to visitStatementList.
func (c *Checker) VisitStatementList(node *parser.StatementListNode) bool {
	for _, statement := range node.D.Statements {
		if expr, ok := statement.(parser.ExpressionNode); ok && parser.IsExpressionNode(statement) {
			// The original's comment: evaluate the expression in case it wasn't
			// otherwise evaluated through lazy analysis. This will mark
			// referenced symbols as accessed and report any errors associated
			// with it.
			c.evaluator.GetType(expr)

			c.reportUnusedExpression(expr)
		}
	}

	return true
}

// walkStatementsAndReportUnreachable corresponds to the method of the same name.
func (c *Checker) walkStatementsAndReportUnreachable(statements []parser.StatementNode) {
	reportedUnreachable := false
	var prevStatement parser.StatementNode

	for _, statement := range statements {
		// The original's comment: no need to report unreachable more than once
		// since the first time covers all remaining statements in the statement
		// list.
		if !reportedUnreachable {
			var prevNode parser.ParseNode
			if prevStatement != nil {
				prevNode = prevStatement
			}
			reachability := c.evaluator.GetNodeReachability(statement, prevNode)
			if reachability != ReachabilityReachable {
				// The original's comment: create a text range that covers the
				// next statement through the end of the statement list.
				start := statement.NodeBase().Start
				lastStatement := statements[len(statements)-1]
				end := lastStatement.NodeBase().TextRange.End()
				textRange := common.TextRange{Start: start, Length: end - start}

				if reachability == ReachabilityUnreachableByAnalysis ||
					reachability == ReachabilityUnreachableStructural {
					message := localization.LocMessage.UnreachableCodeType()
					if reachability == ReachabilityUnreachableStructural {
						message = localization.LocMessage.UnreachableCodeStructure()
					}

					c.evaluator.AddDiagnosticForTextRange(
						c.fileInfo,
						DiagnosticRuleReportUnreachable,
						message,
						unreachableReportRange(statement),
					)
				}

				c.evaluator.AddUnreachableCode(statement, reachability, textRange)

				reportedUnreachable = true
			}
		}

		if !reportedUnreachable && c.fileInfo.IsStubFile {
			c.validateStubStatement(statement)
		}

		c.Walk(statement)

		prevStatement = statement
	}
}

// unreachableReportRange is the original's
// `statement.nodeType === ParseNodeType.Error ? statement : statement.d.firstToken`.
//
// The StatementNode union has eleven members; the ten that are not ErrorNode all
// carry a firstToken, which TypeScript reaches through the union. Go needs the
// switch. A statement outside the union is not reachable from the original's
// type, so falling back to the statement's own range is a defensive default
// rather than a behaviour.
func unreachableReportRange(statement parser.StatementNode) common.TextRange {
	switch s := statement.(type) {
	case *parser.ErrorNode:
		return s.NodeBase().TextRange
	case *parser.IfNode:
		return s.D.FirstToken.GetRange()
	case *parser.WhileNode:
		return s.D.FirstToken.GetRange()
	case *parser.ForNode:
		return s.D.FirstToken.GetRange()
	case *parser.TryNode:
		return s.D.FirstToken.GetRange()
	case *parser.FunctionNode:
		return s.D.FirstToken.GetRange()
	case *parser.ClassNode:
		return s.D.FirstToken.GetRange()
	case *parser.WithNode:
		return s.D.FirstToken.GetRange()
	case *parser.StatementListNode:
		return s.D.FirstToken.GetRange()
	case *parser.MatchNode:
		return s.D.FirstToken.GetRange()
	case *parser.TypeAliasNode:
		return s.D.FirstToken.GetRange()
	}

	return statement.NodeBase().TextRange
}

/*
 * The checks that have not landed yet. Each is a method of the original and
 * records itself, so the frontier ranks them alongside the evaluator's.
 */

// reportUnusedExpression corresponds to _reportUnusedExpression: a statement
// whose expression has no effect.
//
// The list is deliberately narrow. A call, an await, a yield and a string all
// have plausible reasons to stand alone -- side effects, a docstring, a type
// comment -- so only expression kinds that cannot possibly do anything are
// reported. A list, set or dict display qualifies too, except when it contains a
// comprehension, which can have side effects through its iterable.
func (c *Checker) reportUnusedExpression(node parser.ExpressionNode) {
	if c.fileInfo.DiagnosticRuleSet.ReportUnusedExpression == DiagnosticLevelNone {
		return
	}

	reportAsUnused := false

	switch node.GetNodeType() {
	case parser.ParseNodeTypeUnaryOperation,
		parser.ParseNodeTypeBinaryOperation,
		parser.ParseNodeTypeNumber,
		parser.ParseNodeTypeConstant,
		parser.ParseNodeTypeName,
		parser.ParseNodeTypeTuple:
		reportAsUnused = true

	case parser.ParseNodeTypeList, parser.ParseNodeTypeSet, parser.ParseNodeTypeDictionary:
		// The original's comment: exclude comprehensions.
		if !displayContainsComprehension(node) {
			reportAsUnused = true
		}
	}

	if reportAsUnused && c.fileInfo.IPythonMode == IPythonModeCellDocs &&
		isLastStatementOfModule(node) {
		// The original's comment: exclude an expression at the end of a notebook
		// cell, as that is treated as the cell's value.
		reportAsUnused = false
	}

	if reportAsUnused {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportUnusedExpression,
			localization.LocMessage.UnusedExpression(), node, nil)
	}
}

// displayContainsComprehension reads `.d.items` from the list/set/dict union and
// asks whether any entry is a comprehension.
func displayContainsComprehension(node parser.ExpressionNode) bool {
	var items []parser.ExpressionNode

	switch typed := node.(type) {
	case *parser.ListNode:
		items = typed.D.Items
	case *parser.SetNode:
		items = typed.D.Items
	case *parser.DictionaryNode:
		for _, entry := range typed.D.Items {
			if entry.GetNodeType() == parser.ParseNodeTypeComprehension {
				return true
			}
		}
		return false
	}

	for _, item := range items {
		if item.GetNodeType() == parser.ParseNodeTypeComprehension {
			return true
		}
	}
	return false
}

// isLastStatementOfModule is the original's four-way parent chain: the node is
// the last statement of the last statement list of the module.
func isLastStatementOfModule(node parser.ExpressionNode) bool {
	statementList, ok := node.NodeBase().Parent.(*parser.StatementListNode)
	if !ok || len(statementList.D.Statements) == 0 ||
		parser.ParseNode(statementList.D.Statements[len(statementList.D.Statements)-1]) != parser.ParseNode(node) {
		return false
	}

	moduleNode, ok := statementList.NodeBase().Parent.(*parser.ModuleNode)
	if !ok || len(moduleNode.D.Statements) == 0 {
		return false
	}

	return parser.ParseNode(moduleNode.D.Statements[len(moduleNode.D.Statements)-1]) ==
		parser.ParseNode(statementList)
}

// reportUnusedMultipartImports corresponds to _reportUnusedMultipartImports.
//
// `import a.b.c` binds only `a`, so the ordinary unused-symbol check cannot see
// whether `a.b.c` itself was ever used. This walks the module hierarchy to the
// last part and asks whether THAT symbol was accessed, and reports the whole
// dotted name if it was not.
func (c *Checker) reportUnusedMultipartImports() {
	for _, node := range c.multipartImports {
		if !c.isMultipartImportUnused(node) {
			continue
		}

		nameParts := node.D.Module.D.NameParts
		parts := make([]string, len(nameParts))
		for i, np := range nameParts {
			parts[i] = np.D.Value
		}
		multipartName := strings.Join(parts, ".")

		textRange := common.TextRange{
			Start:  nameParts[0].NodeBase().TextRange.Start,
			Length: nameParts[0].NodeBase().TextRange.Length,
		}
		textRange = textRange.Extend(nameParts[len(nameParts)-1].NodeBase().TextRange)

		c.fileInfo.DiagnosticSink.AddUnusedCodeWithTextRange(
			localization.LocMessage.UnaccessedSymbol().Format(multipartName),
			textRange,
			&common.CreateTypeStubFileAction{Action: commandUnusedImport})

		c.evaluator.AddDiagnosticForTextRange(c.fileInfo, DiagnosticRuleReportUnusedImport,
			localization.LocMessage.UnaccessedImport().Format(multipartName), textRange)
	}
}

// isMultipartImportUnused corresponds to _isMultipartImportUnused.
func (c *Checker) isMultipartImportUnused(node *parser.ImportAsNode) bool {
	nameParts := node.D.Module.D.NameParts
	assert(len(nameParts) > 1, "expected a multi-part import")

	// The original's comment: get the top-level module type associated with this
	// import.
	typeResult := c.evaluator.EvaluateTypeForSubnode(node, func() {
		c.evaluator.EvaluateTypesForStatement(node)
	})
	if typeResult == nil {
		return false
	}

	moduleType := typeResult.Type
	if !IsModule(moduleType) {
		return false
	}

	// The original's comment: walk the module hierarchy to get the submodules in
	// the multi-name import path until we get to the second-to-the-last part.
	for i := 1; i < len(nameParts)-1; i++ {
		symbol := ModuleTypeGetField(moduleType.(*ModuleType), nameParts[i].D.Value)
		if symbol == nil {
			return false
		}

		submoduleType := symbol.GetSynthesizedType()
		if submoduleType == nil || !IsModule(submoduleType.Type) {
			return false
		}

		moduleType = submoduleType.Type
	}

	// The original's comment: look up the last part of the import to get its
	// symbol ID.
	lastPartName := nameParts[len(nameParts)-1].D.Value
	symbol := ModuleTypeGetField(moduleType.(*ModuleType), lastPartName)

	if symbol == nil {
		return false
	}

	return !c.fileInfo.AccessedSymbolSet.Has(symbol.ID)
}

// reportDuplicateImports corresponds to _reportDuplicateImports.
//
// An ALIASED duplicate is not a duplicate: `import x as a` and `import x as b`
// bind two different names, and `from m import x as x` is the conventional
// re-export spelling. Both forms are skipped.
func (c *Checker) reportDuplicateImports() {
	importStatements := GetTopLevelImports(c.moduleNode, false)

	importModuleMap := common.NewOrderedMap[string, *parser.ImportAsNode]()

	for _, importStatement := range importStatements.OrderedImports {
		if importFrom, ok := importStatement.Node.(*parser.ImportFromNode); ok {
			symbolMap := common.NewOrderedMap[string, *parser.ImportFromAsNode]()

			for _, importFromAs := range importFrom.D.Imports {
				// The original's comment: ignore duplicates if they're aliased.
				if importFromAs.D.Alias != nil {
					continue
				}

				if _, exists := symbolMap.Get(importFromAs.D.Name.D.Value); exists {
					c.evaluator.AddDiagnostic(DiagnosticRuleReportDuplicateImport,
						localization.LocMessage.DuplicateImport().Format(importFromAs.D.Name.D.Value),
						importFromAs.D.Name, nil)
				} else {
					symbolMap.Set(importFromAs.D.Name.D.Value, importFromAs)
				}
			}
			continue
		}

		if importStatement.Subnode == nil {
			continue
		}

		// The original's comment: ignore duplicates if they're aliased.
		if importStatement.Subnode.D.Alias != nil {
			continue
		}

		if _, exists := importModuleMap.Get(importStatement.ModuleName); exists {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportDuplicateImport,
				localization.LocMessage.DuplicateImport().Format(importStatement.ModuleName),
				importStatement.Subnode, nil)
		} else {
			importModuleMap.Set(importStatement.ModuleName, importStatement.Subnode)
		}
	}
}

// commandUnusedImport corresponds to Commands.unusedImport.
const commandUnusedImport = "pyright.unusedImport"

// noteUnported records an unported checker path on the evaluator's counter, so
// the checker and the evaluator share one frontier. The evaluator is reached
// through an interface assertion rather than a new interface member, which is
// how codeflowengine_reachability.go does the same thing.
func (c *Checker) noteUnported(name string) {
	if reporter, ok := c.evaluator.(interface{ noteUnported(string) }); ok {
		reporter.noteUnported(name)
	}
}

// Compile-time assertion that the Checker satisfies the walker's override set.
var _ ParseTreeVisitorOverrides = (*Checker)(nil)
