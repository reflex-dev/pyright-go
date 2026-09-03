/*
 * cellchainindex.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Maintains a lazily-built index that maps each CellDocs cell
 * to the tail of its chain, enabling efficient forward-cell
 * lookups for notebook inter-cell symbol resolution.
 *
 * Transliterated from analyzer/cellChainIndex.ts (pyright 1.1.412).
 *
 * The CellChainIndex class walks SourceFileInfo chains, so it landed with the
 * program; the interface alone was here while the binder was its
 * only consumer.
 */

package analyzer

import (
	"iter"

	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/parser"
)

// CellChainIndexProvider is the read-only provider surface for consumers that
// only need later-cell lookups.
//
// The original returns `Iterable<ModuleNode> | undefined` from a generator, and
// the caller stops as soon as it finds what it is looking for, so this is an
// iter.Seq rather than a slice: a slice would force every later cell's parse
// tree to be resolved. A nil sequence stands in for `undefined`.
type CellChainIndexProvider interface {
	GetLaterModuleNodes(fileUri uri.Uri) iter.Seq[*parser.ModuleNode]
}

// CellChainIndex corresponds to the class of the same name.
type CellChainIndex struct {
	getSourceFileList func() []*SourceFileInfo
	getSourceFileInfo func(u uri.Uri) *SourceFileInfo

	// tailMap is `Map<...> | undefined`; nil is the stale state.
	tailMap map[string]*SourceFileInfo
}

var _ CellChainIndexProvider = (*CellChainIndex)(nil)

func NewCellChainIndex(
	getSourceFileList func() []*SourceFileInfo,
	getSourceFileInfo func(u uri.Uri) *SourceFileInfo,
) *CellChainIndex {
	return &CellChainIndex{getSourceFileList: getSourceFileList, getSourceFileInfo: getSourceFileInfo}
}

// Invalidate marks the cached tail map as stale. Call when cell chains are
// mutated.
func (c *CellChainIndex) Invalidate() { c.tailMap = nil }

// GetLaterModuleNodes returns a sequence of module nodes from cells *later* in
// the chain than fileUri. It returns nil -- the original's undefined -- when
// there are no later cells, or when the file is not a CellDocs cell.
func (c *CellChainIndex) GetLaterModuleNodes(fileUri uri.Uri) iter.Seq[*parser.ModuleNode] {
	sourceFileInfo := c.getSourceFileInfo(fileUri)
	if sourceFileInfo == nil || sourceFileInfo.IPythonMode() != IPythonModeCellDocs {
		return nil
	}

	tailMap := c.ensureTailMap()
	chainTail := tailMap[fileUri.Key()]
	if chainTail == nil || chainTail == sourceFileInfo {
		return nil
	}

	laterFiles := c.getLaterCellChainFiles(sourceFileInfo, chainTail)
	if len(laterFiles) == 0 {
		return nil
	}

	// The original's comment: the later-files list is captured eagerly above;
	// parse trees are resolved lazily per-yield. Callers consume the iterable
	// synchronously during a single scope-lookup pass.
	return func(yield func(*parser.ModuleNode) bool) {
		for _, laterCellFileInfo := range laterFiles {
			if parserOutput := laterCellFileInfo.SourceFile.GetParserOutput(); parserOutput != nil {
				if !yield(parserOutput.ParseTree) {
					return
				}
			}
		}
	}
}

// GetCellChainFiles returns [sourceFileInfo, ...laterFiles] for the chain that
// sourceFileInfo belongs to. The original's comment: used by Program's
// dependent-file checker logic.
func (c *CellChainIndex) GetCellChainFiles(sourceFileInfo *SourceFileInfo) []*SourceFileInfo {
	tailMap := c.ensureTailMap()
	chainTail := tailMap[sourceFileInfo.Uri().Key()]
	if chainTail == nil || chainTail == sourceFileInfo {
		return []*SourceFileInfo{sourceFileInfo}
	}

	return append([]*SourceFileInfo{sourceFileInfo}, c.getLaterCellChainFiles(sourceFileInfo, chainTail)...)
}

func (c *CellChainIndex) ensureTailMap() map[string]*SourceFileInfo {
	if c.tailMap == nil {
		c.tailMap = c.buildTailMap()
	}
	return c.tailMap
}

// buildTailMap carries the original's comment: the tail map is rebuilt in O(n)
// over the source file list on first access after invalidation. This is
// acceptable because cell-chain mutations (open/close/reorder) are infrequent
// relative to lookups.
func (c *CellChainIndex) buildTailMap() map[string]*SourceFileInfo {
	tailMap := map[string]*SourceFileInfo{}
	sourceFileList := c.getSourceFileList()

	// The original's comment: build a "next-cell-in-chain" reverse map: for each
	// cell that is chained *to* another cell, record `chainedTo -> chainingCell`.
	nextCellInChainMap := map[string]*SourceFileInfo{}
	for _, sourceFileInfo := range sourceFileList {
		chained := sourceFileInfo.ChainedSourceFile()
		if sourceFileInfo.IPythonMode() != IPythonModeCellDocs ||
			chained == nil || chained.IPythonMode() != IPythonModeCellDocs {
			continue
		}

		nextCellInChainMap[chained.Uri().Key()] = sourceFileInfo
	}

	// The original's comment: walk from each tail (a cell with no next-cell)
	// backwards through the chain, recording the tail for every cell in that
	// chain.
	for _, sourceFileInfo := range sourceFileList {
		if sourceFileInfo.IPythonMode() != IPythonModeCellDocs {
			continue
		}
		if _, ok := nextCellInChainMap[sourceFileInfo.Uri().Key()]; ok {
			continue
		}

		current := sourceFileInfo
		for current != nil && current.IPythonMode() == IPythonModeCellDocs {
			tailMap[current.Uri().Key()] = sourceFileInfo
			current = current.ChainedSourceFile()
		}
	}

	return tailMap
}

// getLaterCellChainFiles walks backward from chainTail to sourceFileInfo,
// collecting every cell in between, in forward order and excluding
// sourceFileInfo.
func (c *CellChainIndex) getLaterCellChainFiles(sourceFileInfo *SourceFileInfo, chainTail *SourceFileInfo) []*SourceFileInfo {
	reversed := []*SourceFileInfo{}
	current := chainTail

	for current != nil && current != sourceFileInfo {
		reversed = append(reversed, current)
		current = current.ChainedSourceFile()
	}

	if current != sourceFileInfo {
		return []*SourceFileInfo{}
	}

	laterCellChainFiles := []*SourceFileInfo{}
	for i := len(reversed) - 1; i >= 0; i-- {
		laterCellChainFiles = append(laterCellChainFiles, reversed[i])
	}

	return laterCellChainFiles
}
