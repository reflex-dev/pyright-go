/*
 * symbolutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Functions that operate on Symbol objects.
 *
 * Transliterated from analyzer/symbolUtils.ts (pyright 1.1.412).
 */

package analyzer

// GetLastTypedDeclarationForSymbol returns nil where the TypeScript returns
// undefined.
func GetLastTypedDeclarationForSymbol(symbol *Symbol) Declaration {
	typedDecls := symbol.GetTypedDeclarations()

	if len(typedDecls) > 0 {
		return typedDecls[len(typedDecls)-1]
	}

	return nil
}

// IsTypedDictMemberAccessedThroughIndex reports whether the symbol must be
// accessed through an index operation. Within TypedDict classes, member
// variables are not accessible as normal attributes.
func IsTypedDictMemberAccessedThroughIndex(symbol *Symbol) bool {
	typedDecls := symbol.GetTypedDeclarations()

	if len(typedDecls) > 0 {
		lastDecl := typedDecls[len(typedDecls)-1]
		if lastDecl.DeclBase().Type == DeclarationTypeVariable {
			return true
		}
	}

	return false
}

func IsVisibleExternally(symbol *Symbol) bool {
	return !symbol.IsExternallyHidden() && !symbol.IsPrivatePyTypedImport()
}

func IsEffectivelyClassVar(symbol *Symbol, isInDataclass bool) bool {
	if symbol.IsClassVar() {
		return true
	}

	if symbol.IsFinalVarInClassBody() {
		return !isInDataclass
	}

	return false
}
