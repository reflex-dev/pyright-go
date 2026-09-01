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
 * PARTIAL: getWildcardImportNames is the only function the binder uses, and it
 * is the only one here. The rest of the file -- getTopLevelImports,
 * getTextEditsForAutoImportInsertion, getRelativeModuleName and the other
 * auto-import edit machinery -- exists for the language server, which is out of
 * scope per ANALYZER-PLAN.md, and depends on ConfigOptions, ReadOnlyFileSystem
 * and importResolver besides. See analyzer/STATUS.md.
 */

package analyzer

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
