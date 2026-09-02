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

// validateStubStatement corresponds to the method of the same name.
func (c *Checker) validateStubStatement(statement parser.StatementNode) {
	switch statement.GetNodeType() {
	case parser.ParseNodeTypeIf,
		parser.ParseNodeTypeFunction,
		parser.ParseNodeTypeClass,
		parser.ParseNodeTypeError:
		// The original's comment: these are allowed in a stub file.

	default:
		c.noteUnported("checker.validateStubStatement")
	}
}

/*
 * The checks that have not landed yet. Each is a method of the original and
 * records itself, so the frontier ranks them alongside the evaluator's.
 */

func (c *Checker) reportUnusedExpression(_ parser.ExpressionNode) {
	c.noteUnported("checker.reportUnusedExpression")
}

func (c *Checker) reportUnusedDunderAllSymbols(_ []*parser.StringNode) {
	c.noteUnported("checker.reportUnusedDunderAllSymbols")
}

func (c *Checker) validateSymbolTables() {
	c.noteUnported("checker.validateSymbolTables")
}

func (c *Checker) reportUnusedMultipartImports() {
	c.noteUnported("checker.reportUnusedMultipartImports")
}

func (c *Checker) reportDuplicateImports() {
	c.noteUnported("checker.reportDuplicateImports")
}

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
