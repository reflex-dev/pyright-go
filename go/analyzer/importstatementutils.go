/*
 * importstatementutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Utility routines for summarizing and manipulating
 * import statements in a python source file.
 *
 * Transliterated from analyzer/importStatementUtils.ts (pyright 1.1.412).
 *
 * PARTIAL: getWildcardImportNames (used by the binder) and getTopLevelImports
 * (used by the checker's duplicate-import report) are here. The rest of the file
 * -- getTextEditsForAutoImportInsertion, getRelativeModuleName and the other
 * auto-import edit machinery -- exists for the language server, which is out of
 * scope per ANALYZER-PLAN.md, and depends on ConfigOptions, ReadOnlyFileSystem
 * and importResolver besides. See analyzer/STATUS.md.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/parser"
)

// GetWildcardImportNames corresponds to getWildcardImportNames.
func GetWildcardImportNames(lookupInfo *ImportLookupResult) []string {
	namesToImport := []string{}

	// If a dunder all symbol is defined, it takes precedence.
	//
	// The original's condition is `if (lookupInfo.dunderAllNames)`, and an empty
	// JavaScript array is truthy, so an explicit `__all__ = []` takes this
	// branch and suppresses every name. That is why this is a nil check rather
	// than a length check.
	if lookupInfo.DunderAllNames != nil {
		if !lookupInfo.UsesUnsupportedDunderAllForm {
			return lookupInfo.DunderAllNames
		}

		namesToImport = append(namesToImport, lookupInfo.DunderAllNames...)
	}

	lookupInfo.SymbolTable.ForEach(func(symbol *Symbol, name string) {
		if !symbol.IsExternallyHidden() && !isUnderscoreName(name) {
			namesToImport = append(namesToImport, name)
		}
	})

	return namesToImport
}

// isUnderscoreName corresponds to `name.startsWith('_')`.
func isUnderscoreName(name string) bool {
	return len(name) > 0 && name[0] == '_'
}

/*
 * The top-level import survey, transliterated from
 * analyzer/importStatementUtils.ts (pyright 1.1.412): getTopLevelImports,
 * formatModuleName and the two per-node processors beneath them.
 *
 * A flat, ordered list of the module's import statements, which the checker uses
 * to find duplicates. Two details are load-bearing:
 *
 *   - `followsNonImportStatement` records whether anything other than an import
 *     appeared before this one. Import-sorting consumers use it to avoid moving
 *     an import across a statement that might have side effects.
 *   - `mapByFilePath` prefers `from x import y` over `import x` for the same
 *     resolved file, and among two `from` statements prefers the shorter module
 *     name. The `import` processor therefore refuses to overwrite an existing
 *     entry while the `from` processor is willing to.
 */

// ImportStatement corresponds to the interface of the same name.
type ImportStatement struct {
	// Node holds an ImportNode or an ImportFromNode.
	Node parser.ParseNode

	// Subnode is set only for the `import x` form.
	Subnode *parser.ImportAsNode

	ImportResult *ImportResult
	ResolvedPath uri.Uri
	ModuleName   string

	// FollowsNonImportStatement reports whether a non-import statement appeared
	// earlier in the module.
	FollowsNonImportStatement bool
}

// ImportStatements corresponds to the interface of the same name.
type ImportStatements struct {
	OrderedImports  []*ImportStatement
	MapByFilePath   *common.OrderedMap[string, *ImportStatement]
	ImplicitImports *common.OrderedMap[string, *parser.ImportFromAsNode]
}

// GetTopLevelImports corresponds to getTopLevelImports.
func GetTopLevelImports(parseTree *parser.ModuleNode, includeImplicitImports bool) *ImportStatements {
	localImports := &ImportStatements{
		OrderedImports: []*ImportStatement{},
		MapByFilePath:  common.NewOrderedMap[string, *ImportStatement](),
	}

	followsNonImportStatement := false
	foundFirstImportStatement := false

	for _, statement := range parseTree.D.Statements {
		statementList, ok := statement.(*parser.StatementListNode)
		if !ok {
			followsNonImportStatement = foundFirstImportStatement
			continue
		}

		for _, subStatement := range statementList.D.Statements {
			switch typed := subStatement.(type) {
			case *parser.ImportNode:
				foundFirstImportStatement = true
				processImportNode(typed, localImports, followsNonImportStatement)
				followsNonImportStatement = false

			case *parser.ImportFromNode:
				foundFirstImportStatement = true
				processImportFromNode(typed, localImports, followsNonImportStatement, includeImplicitImports)
				followsNonImportStatement = false

			default:
				followsNonImportStatement = foundFirstImportStatement
			}
		}
	}

	return localImports
}

// processImportNode corresponds to _processImportNode.
func processImportNode(
	node *parser.ImportNode, localImports *ImportStatements, followsNonImportStatement bool,
) {
	for _, importAsNode := range node.D.List {
		importResult := GetImportInfo(importAsNode.D.Module)
		var resolvedPath uri.Uri

		if importResult != nil && importResult.IsImportFound && len(importResult.ResolvedUris) > 0 {
			resolvedPath = importResult.ResolvedUris[len(importResult.ResolvedUris)-1]
		}

		localImport := &ImportStatement{
			Node:                      node,
			Subnode:                   importAsNode,
			ImportResult:              importResult,
			ResolvedPath:              resolvedPath,
			ModuleName:                FormatModuleName(importAsNode.D.Module),
			FollowsNonImportStatement: followsNonImportStatement,
		}

		localImports.OrderedImports = append(localImports.OrderedImports, localImport)

		// The original's comment: add it to the map. Don't overwrite existing import
		// or import from statements because we always want to prefer 'import from'
		// over 'import' in the map.
		if resolvedPath != nil && !resolvedPath.IsEmpty() {
			if _, exists := localImports.MapByFilePath.Get(resolvedPath.Key()); !exists {
				localImports.MapByFilePath.Set(resolvedPath.Key(), localImport)
			}
		}
	}
}

// processImportFromNode corresponds to _processImportFromNode.
func processImportFromNode(
	node *parser.ImportFromNode,
	localImports *ImportStatements,
	followsNonImportStatement bool,
	includeImplicitImports bool,
) {
	importResult := GetImportInfo(node.D.Module)
	var resolvedPath uri.Uri

	if importResult != nil && importResult.IsImportFound && len(importResult.ResolvedUris) > 0 {
		resolvedPath = importResult.ResolvedUris[len(importResult.ResolvedUris)-1]
	}

	if includeImplicitImports && importResult != nil {
		if localImports.ImplicitImports == nil {
			localImports.ImplicitImports = common.NewOrderedMap[string, *parser.ImportFromAsNode]()
		}

		if importResult.ImplicitImports != nil {
			importResult.ImplicitImports.ForEach(func(implicitImport *ImplicitImport, _ string) {
				for _, i := range node.D.Imports {
					if i.D.Name.D.Value == implicitImport.Name {
						localImports.ImplicitImports.Set(implicitImport.Uri.Key(), i)
						break
					}
				}
			})
		}
	}

	localImport := &ImportStatement{
		Node:                      node,
		ImportResult:              importResult,
		ResolvedPath:              resolvedPath,
		ModuleName:                FormatModuleName(node.D.Module),
		FollowsNonImportStatement: followsNonImportStatement,
	}

	localImports.OrderedImports = append(localImports.OrderedImports, localImport)

	// The original's comment: add it to the map. Overwrite existing import
	// statements because we always want to prefer 'import from' over 'import'.
	// Also, overwrite existing 'import from' if the module name is shorter.
	if resolvedPath != nil && !resolvedPath.IsEmpty() {
		prevEntry, exists := localImports.MapByFilePath.Get(resolvedPath.Key())
		if !exists || prevEntry.Node.GetNodeType() == parser.ParseNodeTypeImport ||
			len(prevEntry.ModuleName) > len(localImport.ModuleName) {
			localImports.MapByFilePath.Set(resolvedPath.Key(), localImport)
		}
	}
}

// FormatModuleName corresponds to formatModuleName.
func FormatModuleName(node *parser.ModuleNameNode) string {
	moduleName := strings.Repeat(".", node.D.LeadingDots)

	parts := make([]string, len(node.D.NameParts))
	for i, part := range node.D.NameParts {
		parts[i] = part.D.Value
	}

	return moduleName + strings.Join(parts, ".")
}
