/*
 * declarationutils_resolve.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The rest of analyzer/declarationUtils.ts (pyright 1.1.412), lines 119-419:
 * the name accessors and resolveAliasDeclaration.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/parser"
)

// GetNameFromDeclaration corresponds to getNameFromDeclaration. The TypeScript
// return type is `string | undefined`; nil stands in for undefined.
func GetNameFromDeclaration(declaration Declaration) *string {
	switch declaration.DeclBase().Type {
	case DeclarationTypeAlias:
		return declaration.(*AliasDeclaration).SymbolName

	case DeclarationTypeClass:
		return strPtr(declaration.DeclBase().Node.(*parser.ClassNode).D.Name.D.Value)

	case DeclarationTypeFunction:
		return strPtr(declaration.DeclBase().Node.(*parser.FunctionNode).D.Name.D.Value)

	case DeclarationTypeTypeParam:
		return strPtr(declaration.DeclBase().Node.(*parser.TypeParameterNode).D.Name.D.Value)

	case DeclarationTypeTypeAlias:
		return strPtr(declaration.DeclBase().Node.(*parser.TypeAliasNode).D.Name.D.Value)

	case DeclarationTypeParam:
		name := declaration.DeclBase().Node.(*parser.ParameterNode).D.Name
		if name == nil {
			return nil
		}
		return strPtr(name.D.Value)

	case DeclarationTypeVariable:
		if name, ok := declaration.DeclBase().Node.(*parser.NameNode); ok {
			return strPtr(name.D.Value)
		}
		return nil

	case DeclarationTypeIntrinsic, DeclarationTypeSpecialBuiltInClass:
		annotation, ok := declaration.DeclBase().Node.(*parser.TypeAnnotationNode)
		if !ok {
			return nil
		}
		if name, ok := annotation.D.ValueExpr.(*parser.NameNode); ok {
			return strPtr(name.D.Value)
		}
		return nil
	}

	common.AssertNever(declaration, "")
	return nil
}

// GetNameNodeForDeclaration corresponds to getNameNodeForDeclaration.
func GetNameNodeForDeclaration(declaration Declaration) *parser.NameNode {
	if declaration.DeclBase().Node == nil {
		return nil
	}

	switch declaration.DeclBase().Type {
	case DeclarationTypeAlias:
		switch node := declaration.DeclBase().Node.(type) {
		case *parser.ImportAsNode:
			if node.D.Alias != nil {
				return node.D.Alias
			}
			return node.D.Module.D.NameParts[0]
		case *parser.ImportFromAsNode:
			if node.D.Alias != nil {
				return node.D.Alias
			}
			return node.D.Name
		case *parser.ImportFromNode:
			return node.D.Module.D.NameParts[0]
		}
		return nil

	case DeclarationTypeClass:
		return declaration.DeclBase().Node.(*parser.ClassNode).D.Name

	case DeclarationTypeFunction:
		return declaration.DeclBase().Node.(*parser.FunctionNode).D.Name

	case DeclarationTypeTypeParam:
		return declaration.DeclBase().Node.(*parser.TypeParameterNode).D.Name

	case DeclarationTypeParam:
		return declaration.DeclBase().Node.(*parser.ParameterNode).D.Name

	case DeclarationTypeTypeAlias:
		return declaration.DeclBase().Node.(*parser.TypeAliasNode).D.Name

	case DeclarationTypeVariable:
		if name, ok := declaration.DeclBase().Node.(*parser.NameNode); ok {
			return name
		}
		return nil

	case DeclarationTypeIntrinsic, DeclarationTypeSpecialBuiltInClass:
		return nil
	}

	common.AssertNever(declaration, "")
	return nil
}

// IsDefinedInFile corresponds to isDefinedInFile.
func IsDefinedInFile(decl Declaration, fileUri uri.Uri) bool {
	if _, ok := IsAliasDeclaration(decl); ok {
		// Alias decl's path points to the original symbol the alias is pointing
		// to. So, we need to get the filepath in that the alias is defined
		// from the node.
		fileInfo := GetFileInfoFromNode(decl.DeclBase().Node)
		if fileInfo == nil {
			return false
		}
		return fileInfo.FileUri.Equals(fileUri)
	}

	// Other decls, the path points to the file the symbol is defined in.
	return decl.DeclBase().Uri.Equals(fileUri)
}

// GetDeclarationsWithUsesLocalNameRemoved corresponds to the function of the
// same name.
func GetDeclarationsWithUsesLocalNameRemoved(decls []Declaration) []Declaration {
	// Make a shallow copy and clear the "usesLocalName" field.
	result := make([]Declaration, 0, len(decls))
	for _, localDecl := range decls {
		aliasDecl, ok := IsAliasDeclaration(localDecl)
		if !ok {
			result = append(result, localDecl)
			continue
		}

		nonLocalDecl := *aliasDecl
		nonLocalDecl.UsesLocalName = false
		result = append(result, &nonLocalDecl)
	}
	return result
}

// SynthesizeAliasDeclaration corresponds to synthesizeAliasDeclaration.
//
// The only time this decl is used is for IDE services such as find-all-
// references and the hover provider.
func SynthesizeAliasDeclaration(u uri.Uri) *AliasDeclaration {
	return &AliasDeclaration{
		DeclarationBase: DeclarationBase{
			Type:            DeclarationTypeAlias,
			Node:            nil,
			Uri:             u,
			Range:           common.GetEmptyRange(),
			ModuleName:      "",
			IsInExceptSuite: false,
		},
		LoadSymbolsFromPath: false,
		ImplicitImports:     common.NewOrderedMap[string, *ModuleLoaderActions](),
		UsesLocalName:       false,
	}
}

// ResolveAliasOptions corresponds to the interface of the same name.
type ResolveAliasOptions struct {
	ResolveLocalNames           bool
	AllowExternallyHiddenAccess bool
	SkipFileNeededCheck         bool
}

// ResolveAliasDeclaration resolves an alias declaration that points to a
// symbol, looks up the symbol, and returns a declaration (typically the last)
// associated with that symbol. It does this recursively if necessary. If a
// symbol lookup fails, nil is returned. If ResolveLocalNames is true, aliases
// are resolved through local renames ("as" clauses found in import statements).
func ResolveAliasDeclaration(
	importLookup ImportLookup,
	declaration Declaration,
	options ResolveAliasOptions,
) *ResolvedAliasInfo {
	curDeclaration := declaration
	alreadyVisited := []Declaration{}
	isPrivate := false

	// These variables are used to find a transition from a non-py.typed to a
	// py.typed resolution chain. In this case, if the imported symbol is a
	// private symbol (i.e. not intended to be re-exported), we store the name
	// of the importer and imported modules so the caller can report an error.
	sawPyTypedTransition := false
	var privatePyTypedImported *string
	var privatePyTypedImporter *string

	for {
		curAlias, isAlias := IsAliasDeclaration(curDeclaration)
		if !isAlias || curAlias.SymbolName == nil || *curAlias.SymbolName == "" {
			return &ResolvedAliasInfo{
				Declaration:            curDeclaration,
				IsPrivate:              isPrivate,
				PrivatePyTypedImported: privatePyTypedImported,
				PrivatePyTypedImporter: privatePyTypedImporter,
			}
		}

		// If we are not supposed to follow local alias names and this is a
		// local name, don't continue to follow the alias.
		if !options.ResolveLocalNames && curAlias.UsesLocalName {
			return &ResolvedAliasInfo{
				Declaration:            curDeclaration,
				IsPrivate:              isPrivate,
				PrivatePyTypedImported: privatePyTypedImported,
				PrivatePyTypedImporter: privatePyTypedImporter,
			}
		}

		var lookupResult *ImportLookupResult
		if !uri.IsEmpty(curAlias.Uri) && curAlias.LoadSymbolsFromPath {
			lookupResult = importLookup(curAlias.Uri, nil, &LookupImportOptions{
				SkipFileNeededCheck: options.SkipFileNeededCheck,
			})
		}

		var symbol *Symbol
		if lookupResult != nil {
			symbol, _ = lookupResult.SymbolTable.Get(*curAlias.SymbolName)
		}

		if symbol == nil {
			if curAlias.SubmoduleFallback != nil {
				// See if we are resolving a specific imported symbol name and
				// the submodule fallback cannot be resolved. For example,
				// `from a import b`. If b is both a symbol in `a/__init__.py`
				// and a submodule `a/b.py` and we are not using type
				// information from this library (e.g. a non-py.typed library
				// source file when useLibraryCodeForTypes is disabled), b
				// should be evaluated as Unknown, not as a module.
				if !uri.IsEmpty(curAlias.Uri) && !uri.IsEmpty(curAlias.SubmoduleFallback.Uri) {
					fallbackLookup := importLookup(curAlias.SubmoduleFallback.Uri, nil, &LookupImportOptions{
						SkipFileNeededCheck: options.SkipFileNeededCheck,
						SkipParsing:         true,
					})
					if fallbackLookup == nil {
						return nil
					}
				}

				// The original guards the copy with
				// `if (curDeclaration.symbolName)`, which is always true here:
				// the top of the loop already returned when symbolName was
				// falsy. So the shallow copy always happens.
				copied := *curAlias.SubmoduleFallback
				submoduleFallback := &copied
				baseModuleName := submoduleFallback.ModuleName

				if baseModuleName != "" {
					baseModuleName = baseModuleName + "."
				}

				submoduleFallback.ModuleName = baseModuleName + *curAlias.SymbolName

				return ResolveAliasDeclaration(importLookup, submoduleFallback, options)
			}

			// If the symbol comes from a native library, we won't be able to
			// resolve its type directly.
			if curAlias.IsNativeLib {
				return &ResolvedAliasInfo{
					Declaration: nil,
					IsPrivate:   isPrivate,
				}
			}

			return nil
		}

		if symbol.IsPrivateMember() && !sawPyTypedTransition {
			isPrivate = true
		}

		if symbol.IsExternallyHidden() && !options.AllowExternallyHiddenAccess {
			return nil
		}

		// Prefer declarations with specified types. If we don't have any of
		// those, fall back on declarations with inferred types.
		declarations := symbol.GetTypedDeclarations()

		// Try not to use declarations within an except suite even if it's a
		// typed declaration. These are typically used for fallback exception
		// handling.
		declarations = filterNotInExceptSuite(declarations)

		if len(declarations) == 0 {
			declarations = filterNotInExceptSuite(symbol.GetDeclarations())
		}

		if len(declarations) == 0 {
			// Use declarations within except clauses if there are no
			// alternatives.
			declarations = symbol.GetDeclarations()
		}

		if len(declarations) == 0 {
			return nil
		}

		prevDeclaration := curDeclaration

		// Prefer the last unvisited declaration in the list. This ensures that
		// we use all of the overloads if it's an overloaded function.
		unvisitedDecls := []Declaration{}
		for _, decl := range declarations {
			if !containsDeclaration(alreadyVisited, decl) {
				unvisitedDecls = append(unvisitedDecls, decl)
			}
		}
		if len(unvisitedDecls) > 0 {
			curDeclaration = unvisitedDecls[len(unvisitedDecls)-1]
		} else {
			curDeclaration = declarations[len(declarations)-1]
		}

		if lookupResult != nil && lookupResult.IsInPyTypedPackage {
			if !sawPyTypedTransition {
				if symbol.IsPrivatePyTypedImport() {
					privatePyTypedImporter = strPtr(prevDeclaration.DeclBase().ModuleName)
				}

				// Note that we've seen a transition from a non-py.typed to a
				// py.typed import. No further check is needed.
				sawPyTypedTransition = true
			} else {
				// If we've already seen a transition, look for the first
				// non-private symbol that is resolved so we can tell the user
				// to import from this location instead.
				if !symbol.IsPrivatePyTypedImport() && privatePyTypedImported == nil {
					privatePyTypedImported = strPtr(curDeclaration.DeclBase().ModuleName)
				}
			}
		}

		// Make sure we don't follow a circular list indefinitely.
		if containsDeclaration(alreadyVisited, curDeclaration) {
			// If the path of the alias points back to the original path, use
			// the submodule fallback instead. This happens in the case where a
			// module's __init__.py file imports a submodule using itself as the
			// import target. For example, if the module is foo, and the
			// foo.__init__.py file contains the statement "from foo import
			// bar", we want to import the foo/bar.py submodule.
			if alias, ok := IsAliasDeclaration(curDeclaration); ok && alias.SubmoduleFallback != nil {
				return ResolveAliasDeclaration(importLookup, alias.SubmoduleFallback, options)
			}
			return &ResolvedAliasInfo{
				Declaration:            declaration,
				IsPrivate:              isPrivate,
				PrivatePyTypedImported: privatePyTypedImported,
				PrivatePyTypedImporter: privatePyTypedImporter,
			}
		}
		alreadyVisited = append(alreadyVisited, curDeclaration)
	}
}

// filterNotInExceptSuite corresponds to
// `declarations.filter((decl) => !decl.isInExceptSuite)`.
func filterNotInExceptSuite(declarations []Declaration) []Declaration {
	result := []Declaration{}
	for _, decl := range declarations {
		if !decl.DeclBase().IsInExceptSuite {
			result = append(result, decl)
		}
	}
	return result
}

// containsDeclaration corresponds to `alreadyVisited.includes(decl)`, which
// compares by reference.
func containsDeclaration(declarations []Declaration, target Declaration) bool {
	for _, decl := range declarations {
		if decl == target {
			return true
		}
	}
	return false
}

// strPtr returns a pointer to a copy of s, standing in for the TypeScript
// returning the string value itself where the Go signature needs
// `*string` to express `string | undefined`.
func strPtr(s string) *string { return &s }
