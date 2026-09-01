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
 * PARTIAL: only the CellChainIndexProvider interface is here, which is all the
 * binder consumes. The CellChainIndex class itself walks SourceFileInfo chains
 * and so lands with the program in Stage C; see analyzer/STATUS.md.
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
