/*
 * parsetreecleaner.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * A parse tree walker that's used to clean any analysis information hanging off
 * the parse tree. It's used when dependent files have been modified and the
 * file requires reanalysis. Without this, we'd need to generate a fresh parse
 * tree from scratch.
 *
 * Transliterated from analyzer/parseTreeCleaner.ts (pyright 1.1.412).
 */

package analyzer

import "github.com/microsoft/pyright/go/parser"

// ParseTreeCleanerWalker corresponds to the class of the same name.
//
// It overrides visitNode, the walker's dispatch point, which means the `self`
// pointer the port uses for TypeScript inheritance has to be set -- see
// parsetreewalker.go.
type ParseTreeCleanerWalker struct {
	*ParseTreeWalker

	parseTree *parser.ModuleNode
}

func NewParseTreeCleanerWalker(parseTree *parser.ModuleNode) *ParseTreeCleanerWalker {
	w := &ParseTreeCleanerWalker{parseTree: parseTree}
	w.ParseTreeWalker = NewParseTreeWalker(w)
	return w
}

func (w *ParseTreeCleanerWalker) Clean() {
	w.Walk(w.parseTree)
}

func (w *ParseTreeCleanerWalker) VisitNode(node parser.ParseNode) []parser.ParseNode {
	CleanNodeAnalysisInfo(node)
	return w.ParseTreeWalker.VisitNode(node)
}
