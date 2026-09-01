/*
 * binder_imports.go
 *
 * The binder's import handling, transliterated from analyzer/binder.ts (pyright
 * 1.1.412): visitImportAs, visitImportFrom, the multipart alias-declaration
 * builder, and the module-loader-action machinery.
 *
 * One structural note. In TypeScript `AliasDeclaration extends
 * ModuleLoaderActions`, so _addImplicitImportsToLoaderActions,
 * _cloneModuleLoaderActions and _mergeModuleLoaderActions are called with
 * either. Go has no such subtyping between two named structs, so the four
 * fields they share are reached through the loaderActions interface below,
 * which both implement.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// loaderActions is the set of fields ModuleLoaderActions and AliasDeclaration
// share. The original relies on structural subtyping for this.
type loaderActions interface {
	getUri() uri.Uri
	setUri(value uri.Uri)
	getLoadSymbolsFromPath() bool
	setLoadSymbolsFromPath(value bool)
	getIsUnresolved() bool
	setIsUnresolved(value bool)
	getImplicitImports() *common.OrderedMap[string, *ModuleLoaderActions]
	setImplicitImports(value *common.OrderedMap[string, *ModuleLoaderActions])
}

func (a *ModuleLoaderActions) getUri() uri.Uri               { return a.Uri }
func (a *ModuleLoaderActions) setUri(value uri.Uri)          { a.Uri = value }
func (a *ModuleLoaderActions) getLoadSymbolsFromPath() bool  { return a.LoadSymbolsFromPath }
func (a *ModuleLoaderActions) setLoadSymbolsFromPath(v bool) { a.LoadSymbolsFromPath = v }
func (a *ModuleLoaderActions) getIsUnresolved() bool         { return a.IsUnresolved }
func (a *ModuleLoaderActions) setIsUnresolved(value bool)    { a.IsUnresolved = value }
func (a *ModuleLoaderActions) getImplicitImports() *common.OrderedMap[string, *ModuleLoaderActions] {
	return a.ImplicitImports
}
func (a *ModuleLoaderActions) setImplicitImports(v *common.OrderedMap[string, *ModuleLoaderActions]) {
	a.ImplicitImports = v
}

func (d *AliasDeclaration) getUri() uri.Uri               { return d.Uri }
func (d *AliasDeclaration) setUri(value uri.Uri)          { d.Uri = value }
func (d *AliasDeclaration) getLoadSymbolsFromPath() bool  { return d.LoadSymbolsFromPath }
func (d *AliasDeclaration) setLoadSymbolsFromPath(v bool) { d.LoadSymbolsFromPath = v }
func (d *AliasDeclaration) getIsUnresolved() bool         { return d.IsUnresolved }
func (d *AliasDeclaration) setIsUnresolved(value bool)    { d.IsUnresolved = value }
func (d *AliasDeclaration) getImplicitImports() *common.OrderedMap[string, *ModuleLoaderActions] {
	return d.ImplicitImports
}
func (d *AliasDeclaration) setImplicitImports(v *common.OrderedMap[string, *ModuleLoaderActions]) {
	d.ImplicitImports = v
}

// VisitImportAs corresponds to visitImportAs.
func (b *Binder) VisitImportAs(node *parser.ImportAsNode) bool {
	if len(node.D.Module.D.NameParts) > 0 {
		firstNamePartValue := node.D.Module.D.NameParts[0].D.Value

		var symbolName string
		var symbolNameNode *parser.NameNode
		if node.D.Alias != nil {
			// The symbol name is defined by the alias.
			symbolName = node.D.Alias.D.Value
			symbolNameNode = node.D.Alias
		} else {
			// There was no alias, so we need to use the first element of the
			// name parts as the symbol.
			symbolName = firstNamePartValue
			symbolNameNode = node.D.Module.D.NameParts[0]
		}

		symbol := b.bindNameToScope(b.currentScope, symbolNameNode, nil)
		if symbol != nil &&
			(b.currentScope.Type == ScopeTypeModule || b.currentScope.Type == ScopeTypeBuiltin) &&
			(node.D.Alias == nil ||
				len(node.D.Module.D.NameParts) != 1 ||
				node.D.Module.D.NameParts[0].D.Value != node.D.Alias.D.Value) {
			if b.fileInfo.IsStubFile || b.fileInfo.IsInPyTypedPackage {
				// PEP 484 indicates that imported symbols should not be
				// considered "reexported" from a type stub file unless they are
				// imported using the "as" form and the aliased name is entirely
				// redundant.
				b.potentialHiddenSymbols.Set(symbolName, symbol)
			}
		}

		importInfo := GetImportInfo(node.D.Module)
		assert(importInfo != nil, "")

		if symbol != nil {
			b.createAliasDeclarationForMultipartImportName(node, node.D.Alias, importInfo, symbol)
		}

		if node.D.Alias != nil {
			b.createFlowAssignment(node.D.Alias, false)
		} else {
			b.createFlowAssignment(node.D.Module.D.NameParts[0], false)
		}

		if len(node.D.Module.D.NameParts) == 1 {
			aliasName := firstNamePartValue
			if node.D.Alias != nil {
				aliasName = node.D.Alias.D.Value
			}

			switch firstNamePartValue {
			case "typing", "typing_extensions":
				b.typingImportAliases = append(b.typingImportAliases, aliasName)
			case "sys":
				b.sysImportAliases = append(b.sysImportAliases, aliasName)
			case "dataclasses":
				b.dataclassesImportAliases = append(b.dataclassesImportAliases, aliasName)
			}
		}
	}

	return true
}

// VisitImportFrom corresponds to visitImportFrom.
func (b *Binder) VisitImportFrom(node *parser.ImportFromNode) bool {
	typingSymbolsOfInterest := []string{"Final", "ClassVar", "Annotated"}
	dataclassesSymbolsOfInterest := []string{"InitVar", "KW_ONLY"}
	importInfo := GetImportInfo(node.D.Module)

	SetFlowNode(node, b.currentFlowNode)

	resolvedPath := uri.Empty()
	if importInfo != nil && importInfo.IsImportFound && !importInfo.IsNativeLib {
		// `resolvedUris[resolvedUris.length - 1]` reads index -1 when the array
		// is empty, which JavaScript answers with undefined and Go answers with
		// a panic. An empty array is reachable: "from . import x" has no name
		// parts. The undefined is reproduced explicitly.
		if len(importInfo.ResolvedUris) > 0 {
			resolvedPath = importInfo.ResolvedUris[len(importInfo.ResolvedUris)-1]
		} else {
			resolvedPath = nil
		}
	}

	// If this file is a module __init__.py(i), relative imports of submodules
	// using the syntax "from .x import y" introduce a symbol x into the module
	// namespace. We do this first (before adding the individual imported
	// symbols below) in case one of the imported symbols is the same name as
	// the submodule. In that case, we want the symbol to appear later in the
	// declaration list because it should "win" when resolving the alias.
	fileName := uri.StripFileExtension(b.fileInfo.FileUri.FileName(), false)
	isModuleInitFile := fileName == "__init__" &&
		node.D.Module.D.LeadingDots == 1 &&
		len(node.D.Module.D.NameParts) == 1

	isTypingImport := false
	isDataclassesImport := false

	if len(node.D.Module.D.NameParts) == 1 {
		firstNamePartValue := node.D.Module.D.NameParts[0].D.Value
		if firstNamePartValue == "typing" || firstNamePartValue == "typing_extensions" {
			isTypingImport = true
		}

		if firstNamePartValue == "dataclasses" {
			isDataclassesImport = true
		}
	}

	if node.D.IsWildcardImport {
		b.visitWildcardImportFrom(node, importInfo, resolvedPath, isModuleInitFile, isTypingImport, isDataclassesImport,
			typingSymbolsOfInterest, dataclassesSymbolsOfInterest)
	} else {
		if isModuleInitFile {
			b.addImplicitFromImport(node, importInfo)
		}

		for _, importSymbolNode := range node.D.Imports {
			b.visitImportFromAsSymbol(node, importSymbolNode, importInfo, resolvedPath, fileName,
				isTypingImport, isDataclassesImport, typingSymbolsOfInterest, dataclassesSymbolsOfInterest)
		}
	}

	return true
}

// visitWildcardImportFrom is the `from x import *` branch of visitImportFrom.
func (b *Binder) visitWildcardImportFrom(
	node *parser.ImportFromNode,
	importInfo *ImportResult,
	resolvedPath uri.Uri,
	isModuleInitFile bool,
	isTypingImport bool,
	isDataclassesImport bool,
	typingSymbolsOfInterest []string,
	dataclassesSymbolsOfInterest []string,
) {
	if GetEnclosingClass(node, false) != nil || GetEnclosingFunction(node) != nil {
		b.addSyntaxError(localization.LocMessage.WildcardInFunction(), node.GetRange())
	}

	if importInfo == nil {
		return
	}

	names := []string{}

	// Note that this scope uses a wildcard import, so we cannot shortcut any
	// code flow checks. All expressions are potentially in play.
	if b.currentScopeCodeFlowExpressions != nil {
		b.currentScopeCodeFlowExpressions.Add(WildcardImportReferenceKey)
	}

	lookupInfo := b.fileInfo.ImportLookup(resolvedPath, nil, nil)
	if lookupInfo != nil {
		wildcardNames := GetWildcardImportNames(lookupInfo)

		if isModuleInitFile {
			// If the symbol is going to be immediately replaced with a
			// same-named imported symbol, skip this.
			isImmediatelyReplaced := false
			for _, name := range wildcardNames {
				if name == node.D.Module.D.NameParts[0].D.Value {
					isImmediatelyReplaced = true
					break
				}
			}

			if !isImmediatelyReplaced {
				b.addImplicitFromImport(node, importInfo)
			}
		}

		for _, name := range wildcardNames {
			localSymbol := b.bindNameValueToScope(b.currentScope, name, nil)
			if localSymbol == nil {
				continue
			}

			importedSymbol, _ := lookupInfo.SymbolTable.Get(name)

			if (b.currentScope.Type == ScopeTypeModule || b.currentScope.Type == ScopeTypeBuiltin) &&
				b.fileInfo.IsInPyTypedPackage &&
				!b.fileInfo.IsStubFile {
				// Wildcard imports are considered a re-export form. If this
				// module defines __all__, it determines the public interface, so
				// we may need to treat wildcard-imported names as private unless
				// listed.
				b.potentialWildcardReexportSymbols.Set(name, localSymbol)
			}

			// Is the symbol in the target module's symbol table? If so, alias
			// it.
			if importedSymbol != nil {
				if b.addWildcardImportedModuleAlias(node, localSymbol, importedSymbol) {
					names = append(names, name)
				} else {
					symbolName := name
					localSymbol.AddDeclaration(&AliasDeclaration{
						DeclarationBase: DeclarationBase{
							Type: DeclarationTypeAlias,
							Node: node,
							Uri:  resolvedPath,
							// Range is unknown for wildcard name import.
							Range:           common.GetEmptyRange(),
							ModuleName:      b.fileInfo.ModuleName,
							IsInExceptSuite: b.isInExceptSuite,
						},
						LoadSymbolsFromPath: true,
						UsesLocalName:       false,
						SymbolName:          &symbolName,
					})
					names = append(names, name)
				}
			} else {
				// The symbol wasn't in the target module's symbol table. It's
				// probably an implicitly-imported submodule referenced by
				// __all__.
				if importInfo.FilteredImplicitImports != nil {
					implicitImport, _ := importInfo.FilteredImplicitImports.Get(name)

					if implicitImport != nil {
						submoduleFallback := &AliasDeclaration{
							DeclarationBase: DeclarationBase{
								Type:            DeclarationTypeAlias,
								Node:            node,
								Uri:             implicitImport.Uri,
								Range:           common.GetEmptyRange(),
								ModuleName:      b.fileInfo.ModuleName,
								IsInExceptSuite: b.isInExceptSuite,
							},
							LoadSymbolsFromPath: true,
							UsesLocalName:       false,
						}

						symbolName := name
						localSymbol.AddDeclaration(&AliasDeclaration{
							DeclarationBase: DeclarationBase{
								Type:            DeclarationTypeAlias,
								Node:            node,
								Uri:             resolvedPath,
								Range:           common.GetEmptyRange(),
								ModuleName:      b.fileInfo.ModuleName,
								IsInExceptSuite: b.isInExceptSuite,
							},
							LoadSymbolsFromPath: true,
							UsesLocalName:       false,
							SymbolName:          &symbolName,
							SubmoduleFallback:   submoduleFallback,
						})
						names = append(names, name)
					}
				}
			}

			if isTypingImport {
				localSymbol.SetTypingSymbolAlias(name)
			}
		}
	}

	b.createFlowWildcardImport(node, names)

	if isTypingImport {
		for _, s := range typingSymbolsOfInterest {
			b.typingSymbolAliases.Set(s, s)
		}
	}

	if isDataclassesImport {
		for _, s := range dataclassesSymbolsOfInterest {
			b.dataclassesSymbolAliases.Set(s, s)
		}
	}
}

// visitImportFromAsSymbol is the per-symbol body of the non-wildcard branch of
// visitImportFrom.
func (b *Binder) visitImportFromAsSymbol(
	node *parser.ImportFromNode,
	importSymbolNode *parser.ImportFromAsNode,
	importInfo *ImportResult,
	resolvedPath uri.Uri,
	fileName string,
	isTypingImport bool,
	isDataclassesImport bool,
	typingSymbolsOfInterest []string,
	dataclassesSymbolsOfInterest []string,
) {
	importedName := importSymbolNode.D.Name.D.Value
	nameNode := importSymbolNode.D.Alias
	if nameNode == nil {
		nameNode = importSymbolNode.D.Name
	}

	SetFlowNode(importSymbolNode, b.currentFlowNode)

	symbol := b.bindNameToScope(b.currentScope, nameNode, nil)
	if symbol == nil {
		return
	}

	// All import statements of the form `from . import x` treat x as an
	// externally-visible (not hidden) symbol.
	if len(node.D.Module.D.NameParts) > 0 {
		if b.currentScope.Type == ScopeTypeModule || b.currentScope.Type == ScopeTypeBuiltin {
			if importSymbolNode.D.Alias == nil ||
				importSymbolNode.D.Alias.D.Value != importSymbolNode.D.Name.D.Value {
				if b.fileInfo.IsStubFile || b.fileInfo.IsInPyTypedPackage {
					// PEP 484 indicates that imported symbols should not be
					// considered "reexported" from a type stub file unless they
					// are imported using the "as" form using a redundant form.
					// Py.typed packages follow the same rule as PEP 484.
					b.potentialHiddenSymbols.Set(nameNode.D.Value, symbol)
				}
			}
		}
	}

	// Is the import referring to an implicitly-imported module?
	var implicitImport *ImplicitImport
	if importInfo != nil && importInfo.FilteredImplicitImports != nil {
		implicitImport, _ = importInfo.FilteredImplicitImports.Get(importedName)
	}

	var submoduleFallback *AliasDeclaration
	loadSymbolsFromPath := true
	if implicitImport != nil {
		submoduleFallback = &AliasDeclaration{
			DeclarationBase: DeclarationBase{
				Type:            DeclarationTypeAlias,
				Node:            importSymbolNode,
				Uri:             implicitImport.Uri,
				Range:           common.GetEmptyRange(),
				ModuleName:      b.formatModuleName(node.D.Module),
				IsInExceptSuite: b.isInExceptSuite,
			},
			LoadSymbolsFromPath: true,
			UsesLocalName:       false,
			IsLazy:              node.D.IsLazy,
		}

		// Handle the case where this is an __init__.py file and the imported
		// module name refers to itself. The most common situation where this
		// occurs is with a "from . import X" form, but it can also occur with an
		// absolute import (e.g. "from A.B.C import X"). In this case, we want to
		// always resolve to the submodule rather than the resolved path.
		if fileName == "__init__" {
			if node.D.Module.D.LeadingDots == 1 && len(node.D.Module.D.NameParts) == 0 {
				loadSymbolsFromPath = false
			} else if resolvedPath != nil && resolvedPath.Equals(b.fileInfo.FileUri) {
				loadSymbolsFromPath = false
			}
		}
	}

	symbolName := importedName
	isNativeLib := false
	if importInfo != nil {
		isNativeLib = importInfo.IsNativeLib
	}

	symbol.AddDeclaration(&AliasDeclaration{
		DeclarationBase: DeclarationBase{
			Type:            DeclarationTypeAlias,
			Node:            importSymbolNode,
			Uri:             resolvedPath,
			Range:           common.ConvertTextRangeToRange(nameNode.GetRange(), b.fileInfo.Lines),
			ModuleName:      b.formatModuleName(node.D.Module),
			IsInExceptSuite: b.isInExceptSuite,
		},
		LoadSymbolsFromPath: loadSymbolsFromPath,
		UsesLocalName:       importSymbolNode.D.Alias != nil,
		SymbolName:          &symbolName,
		SubmoduleFallback:   submoduleFallback,
		IsNativeLib:         isNativeLib,
		IsLazy:              node.D.IsLazy,
	})

	if importSymbolNode.D.Alias != nil {
		b.createFlowAssignment(importSymbolNode.D.Alias, false)
	} else {
		b.createFlowAssignment(importSymbolNode.D.Name, false)
	}

	if isTypingImport {
		for _, s := range typingSymbolsOfInterest {
			if s == importSymbolNode.D.Name.D.Value {
				b.typingSymbolAliases.Set(nameNode.D.Value, importSymbolNode.D.Name.D.Value)
				// The original re-tests isTypingImport here, inside a branch
				// that is only reachable when it is true.
				symbol.SetTypingSymbolAlias(nameNode.D.Value)
				break
			}
		}
	}

	if isDataclassesImport {
		for _, s := range dataclassesSymbolsOfInterest {
			if s == importSymbolNode.D.Name.D.Value {
				b.dataclassesSymbolAliases.Set(nameNode.D.Value, importSymbolNode.D.Name.D.Value)
				break
			}
		}
	}
}

// getDunderAllNamesFromImport corresponds to _getDunderAllNamesFromImport. It
// attempts to resolve the module name, import it, and return its __all__
// symbols. It returns nil where the TypeScript returns undefined.
func (b *Binder) getDunderAllNamesFromImport(varName string) []string {
	varSymbol := b.currentScope.LookUpSymbol(varName)
	if varSymbol == nil {
		return nil
	}

	// There should be only one declaration for the variable.
	var aliasDecl *AliasDeclaration
	for _, decl := range varSymbol.GetDeclarations() {
		if alias, ok := decl.(*AliasDeclaration); ok {
			aliasDecl = alias
			break
		}
	}

	var resolvedUri uri.Uri
	switch {
	case aliasDecl != nil && aliasDecl.Uri != nil && !aliasDecl.Uri.IsEmpty() && aliasDecl.LoadSymbolsFromPath:
		resolvedUri = aliasDecl.Uri
	case aliasDecl != nil && aliasDecl.SubmoduleFallback != nil &&
		aliasDecl.SubmoduleFallback.Uri != nil && !aliasDecl.SubmoduleFallback.Uri.IsEmpty() &&
		aliasDecl.SubmoduleFallback.LoadSymbolsFromPath:
		resolvedUri = aliasDecl.SubmoduleFallback.Uri
	}
	if resolvedUri == nil {
		return nil
	}

	lookupInfo := b.fileInfo.ImportLookup(resolvedUri, nil, nil)
	if lookupInfo != nil && lookupInfo.DunderAllNames != nil {
		return lookupInfo.DunderAllNames
	}

	if aliasDecl != nil && aliasDecl.SubmoduleFallback != nil &&
		aliasDecl.SubmoduleFallback.Uri != nil && !aliasDecl.SubmoduleFallback.Uri.IsEmpty() {
		lookupInfo = b.fileInfo.ImportLookup(aliasDecl.SubmoduleFallback.Uri, nil, nil)
		if lookupInfo == nil {
			return nil
		}
		return lookupInfo.DunderAllNames
	}

	return nil
}

// addImplicitFromImport corresponds to _addImplicitFromImport. The TypeScript
// leaves importInfo undefined; pass nil for that.
func (b *Binder) addImplicitFromImport(node *parser.ImportFromNode, importInfo *ImportResult) {
	symbolName := node.D.Module.D.NameParts[0].D.Value
	symbol := b.bindNameValueToScope(b.currentScope, symbolName, nil)
	if symbol != nil {
		b.createAliasDeclarationForMultipartImportName(node, nil /* importAlias */, importInfo, symbol)
	}

	b.createFlowAssignment(node.D.Module.D.NameParts[0], false)
}

// createAliasDeclarationForMultipartImportName corresponds to
// _createAliasDeclarationForMultipartImportName. node is an ImportAsNode or
// ImportFromNode; the TypeScript leaves importAlias and importInfo undefined.
func (b *Binder) createAliasDeclarationForMultipartImportName(
	node parser.ParseNode,
	importAlias *parser.NameNode,
	importInfo *ImportResult,
	symbol *Symbol,
) {
	module := moduleNameOf(node)
	firstNamePartValue := module.D.NameParts[0].D.Value

	SetFlowNode(node, b.currentFlowNode)

	var uriOfFirstSubmodule uri.Uri
	if importInfo != nil && importInfo.IsImportFound && !importInfo.IsNativeLib && len(importInfo.ResolvedUris) > 0 {
		uriOfFirstSubmodule = importInfo.ResolvedUris[0]
	}

	// See if there's already a matching alias declaration for this import. If
	// so, we'll update it rather than creating a new one. This is required to
	// handle cases where multiple import statements target the same starting
	// symbol such as "import a.b.c" and "import a.d". In this case, we'll build
	// a single declaration that describes the combined actions of both import
	// statements, thus reflecting the behavior of the python module loader.
	var existingDecl *AliasDeclaration
	for _, decl := range symbol.GetDeclarations() {
		alias, ok := decl.(*AliasDeclaration)
		if !ok {
			continue
		}
		if alias.FirstNamePart == nil || *alias.FirstNamePart != firstNamePartValue {
			continue
		}
		if uriOfFirstSubmodule != nil && !uriOfFirstSubmodule.Equals(alias.Uri) {
			continue
		}
		existingDecl = alias
		break
	}

	var uriOfLastSubmodule uri.Uri
	if importInfo != nil && importInfo.IsImportFound && !importInfo.IsNativeLib && len(importInfo.ResolvedUris) > 0 {
		uriOfLastSubmodule = importInfo.ResolvedUris[len(importInfo.ResolvedUris)-1]
	} else {
		uriOfLastSubmodule = UnresolvedModuleMarker
	}

	isResolved := importInfo != nil && importInfo.IsImportFound && !importInfo.IsNativeLib &&
		len(importInfo.ResolvedUris) > 0

	// Determine whether this import was declared with the "lazy" keyword
	// (PEP 810).
	isLazy := false
	if importAs, ok := node.(*parser.ImportAsNode); ok {
		if parent, ok := importAs.NodeBase().Parent.(*parser.ImportNode); ok {
			isLazy = parent.D.IsLazy
		}
	} else {
		isLazy = node.(*parser.ImportFromNode).D.IsLazy
	}

	var newDecl *AliasDeclaration
	switch {
	case existingDecl != nil:
		newDecl = existingDecl

		// Reconcile laziness: if any eager import path exists for this symbol,
		// the declaration is not lazy (PEP 810).
		if !isLazy {
			newDecl.IsLazy = false
		}

	case isResolved:
		moduleName := repeatString(".", module.D.LeadingDots) + firstNamePartValue
		if importAlias != nil {
			moduleName = b.formatModuleName(module)
		}
		newDecl = &AliasDeclaration{
			DeclarationBase: DeclarationBase{
				Type:            DeclarationTypeAlias,
				Node:            node,
				Uri:             uriOfLastSubmodule,
				Range:           common.GetEmptyRange(),
				ModuleName:      moduleName,
				IsInExceptSuite: b.isInExceptSuite,
			},
			LoadSymbolsFromPath: false,
			UsesLocalName:       importAlias != nil,
			FirstNamePart:       &firstNamePartValue,
			IsLazy:              isLazy,
		}

	default:
		// If we couldn't resolve the import, create a dummy declaration with a
		// bogus path so it gets an unknown type (rather than an unbound type) at
		// analysis time.
		//
		// Note that the original assigns the *formatted* module name to
		// firstNamePart in this branch when there is an alias, which does not
		// match the resolved branch. Reproduced as written.
		importName := ""
		if importInfo != nil {
			importName = importInfo.ImportName
		}
		firstNamePart := repeatString(".", module.D.LeadingDots) + firstNamePartValue
		if importAlias != nil {
			firstNamePart = b.formatModuleName(module)
		}
		newDecl = &AliasDeclaration{
			DeclarationBase: DeclarationBase{
				Type:            DeclarationTypeAlias,
				Node:            node,
				Uri:             uriOfLastSubmodule,
				Range:           common.GetEmptyRange(),
				ModuleName:      importName,
				IsInExceptSuite: b.isInExceptSuite,
			},
			LoadSymbolsFromPath: true,
			UsesLocalName:       importAlias != nil,
			FirstNamePart:       &firstNamePart,
			IsUnresolved:        true,
			IsLazy:              isLazy,
		}
	}

	// See if there is import info for this part of the path. This allows us to
	// implicitly import all of the modules in a multi-part module name.
	implicitImportInfo := GetImportInfo(module.D.NameParts[0])
	if implicitImportInfo != nil && len(implicitImportInfo.ResolvedUris) > 0 {
		newDecl.Uri = implicitImportInfo.ResolvedUris[0]
		newDecl.LoadSymbolsFromPath = true
		b.addImplicitImportsToLoaderActions(implicitImportInfo, newDecl)
	}

	// Add the implicit imports for this module if it's the last name part we're
	// resolving.
	if importAlias != nil || len(module.D.NameParts) == 1 {
		newDecl.Uri = uriOfLastSubmodule
		newDecl.LoadSymbolsFromPath = true
		newDecl.IsUnresolved = false

		if importInfo != nil {
			b.addImplicitImportsToLoaderActions(importInfo, newDecl)
		}
	} else {
		// Fill in the remaining name parts.
		var curLoaderActions loaderActions = newDecl

		for i := 1; i < len(module.D.NameParts); i++ {
			namePartValue := module.D.NameParts[i].D.Value

			// Is there an existing loader action for this name?
			var actions *ModuleLoaderActions
			if curLoaderActions.getImplicitImports() != nil {
				actions, _ = curLoaderActions.getImplicitImports().Get(namePartValue)
			}
			if actions == nil {
				loaderActionPath := UnresolvedModuleMarker
				if importInfo != nil && i < len(importInfo.ResolvedUris) {
					loaderActionPath = importInfo.ResolvedUris[i]
				}

				// Allocate a new loader action.
				actions = &ModuleLoaderActions{
					Uri:                 loaderActionPath,
					LoadSymbolsFromPath: false,
					ImplicitImports:     common.NewOrderedMap[string, *ModuleLoaderActions](),
					IsUnresolved:        !isResolved,
				}
				if curLoaderActions.getImplicitImports() == nil {
					curLoaderActions.setImplicitImports(common.NewOrderedMap[string, *ModuleLoaderActions]())
				}
				curLoaderActions.getImplicitImports().Set(namePartValue, actions)
			}

			if i == len(module.D.NameParts)-1 {
				// If this is the last name part we're resolving, add in the
				// implicit imports as well.
				if importInfo != nil && i < len(importInfo.ResolvedUris) {
					actions.Uri = importInfo.ResolvedUris[i]
					actions.LoadSymbolsFromPath = true
					b.addImplicitImportsToLoaderActions(importInfo, actions)
				}
			} else {
				// If this isn't the last name part we're resolving, see if there
				// is import info for this part of the path. This allows us to
				// implicitly import all of the modules in a multi-part module
				// name (e.g. "import a.b.c" imports "a" and "a.b" and "a.b.c").
				//
				// Note the index: the original reads resolvedUris[i] from the
				// *nested* import info, whose array is indexed by that module's
				// own name parts. Reproduced as written.
				nestedInfo := GetImportInfo(module.D.NameParts[i])
				if nestedInfo != nil && len(nestedInfo.ResolvedUris) > 0 {
					if i < len(nestedInfo.ResolvedUris) {
						actions.Uri = nestedInfo.ResolvedUris[i]
					} else {
						// resolvedUris[i] is undefined in the original, which
						// assigns undefined to a Uri field.
						actions.Uri = nil
					}
					actions.LoadSymbolsFromPath = true
					b.addImplicitImportsToLoaderActions(nestedInfo, actions)
				}
			}

			curLoaderActions = actions
		}
	}

	if existingDecl == nil {
		symbol.AddDeclaration(newDecl)
	}
}

// moduleNameOf reads `node.d.module` from either arm of
// `ImportAsNode | ImportFromNode`.
func moduleNameOf(node parser.ParseNode) *parser.ModuleNameNode {
	switch typed := node.(type) {
	case *parser.ImportAsNode:
		return typed.D.Module
	case *parser.ImportFromNode:
		return typed.D.Module
	}
	fail("moduleNameOf received unexpected node type")
	return nil
}

// addImplicitImportsToLoaderActions corresponds to
// _addImplicitImportsToLoaderActions.
func (b *Binder) addImplicitImportsToLoaderActions(importResult *ImportResult, actions loaderActions) {
	if importResult.FilteredImplicitImports == nil {
		return
	}

	importResult.FilteredImplicitImports.ForEach(func(implicitImport *ImplicitImport, _ string) {
		var existingLoaderAction *ModuleLoaderActions
		if actions.getImplicitImports() != nil {
			existingLoaderAction, _ = actions.getImplicitImports().Get(implicitImport.Name)
		}
		if existingLoaderAction != nil {
			existingLoaderAction.Uri = implicitImport.Uri
			existingLoaderAction.LoadSymbolsFromPath = true
		} else {
			if actions.getImplicitImports() == nil {
				actions.setImplicitImports(common.NewOrderedMap[string, *ModuleLoaderActions]())
			}
			actions.getImplicitImports().Set(implicitImport.Name, &ModuleLoaderActions{
				Uri:                 implicitImport.Uri,
				LoadSymbolsFromPath: true,
				ImplicitImports:     common.NewOrderedMap[string, *ModuleLoaderActions](),
			})
		}
	})
}

// addWildcardImportedModuleAlias corresponds to
// _addWildcardImportedModuleAlias.
func (b *Binder) addWildcardImportedModuleAlias(
	node *parser.ImportFromNode,
	localSymbol *Symbol,
	importedSymbol *Symbol,
) bool {
	importedModuleAliasDecl := getMultipartModuleAliasDeclaration(importedSymbol, nil, nil)
	if importedModuleAliasDecl == nil {
		return false
	}

	// The imported symbol may be both an implicitly-imported submodule and a
	// class/function/variable of the same name (e.g. a package that re-exports a
	// class whose name matches a submodule). In that case the non-module
	// declaration appears later in the declaration list and "wins" when the
	// symbol is resolved. Only treat this wildcard re-export as a pure submodule
	// re-export when the module alias is the symbol's last declaration;
	// otherwise fall through so a normal alias declaration is created that
	// resolves to the winning symbol.
	//
	// We compare against the raw last declaration (not
	// getLastTypedDeclarationForSymbol) on purpose: a module alias is a
	// DeclarationType.Alias, which hasTypeForDeclaration treats as untyped, so
	// it never appears among a symbol's typed declarations. The evaluator
	// resolves alias symbols (like this one, whose declarations are all imports)
	// by declaration order, so the last declaration is the relevant "winner"
	// here. When this guard falls through, the alias created by the caller
	// resolves to the winning (e.g. class) declaration and intentionally has no
	// submoduleFallback: the class shadows the submodule, so submodule member
	// access through the re-exported name is no longer offered. The
	// genuine-submodule case (where the module alias is the last declaration)
	// keeps its module/submodule behavior.
	importedDecls := importedSymbol.GetDeclarations()
	if len(importedDecls) == 0 || importedDecls[len(importedDecls)-1] != Declaration(importedModuleAliasDecl) {
		return false
	}

	existingModuleAliasDecl := getMultipartModuleAliasDeclaration(
		localSymbol,
		&importedModuleAliasDecl.ModuleName,
		importedModuleAliasDecl.FirstNamePart,
	)

	if existingModuleAliasDecl != nil {
		b.mergeModuleLoaderActions(existingModuleAliasDecl, importedModuleAliasDecl)
	} else {
		localSymbol.AddDeclaration(b.cloneMultipartModuleAliasDeclaration(node, importedModuleAliasDecl))
	}

	return true
}

// getMultipartModuleAliasDeclaration corresponds to
// _getMultipartModuleAliasDeclaration. It finds the latest alias declaration
// that represents the root of a multipart import chain, regardless of whether
// it originated from a direct import or wildcard merge. A nil moduleName or
// firstNamePart stands in for the omitted optional argument.
func getMultipartModuleAliasDeclaration(
	symbol *Symbol,
	moduleName *string,
	firstNamePart *string,
) *AliasDeclaration {
	declarations := symbol.GetDeclarations()

	for index := len(declarations) - 1; index >= 0; index-- {
		declaration, ok := declarations[index].(*AliasDeclaration)
		if !ok {
			continue
		}
		// `declaration.symbolName || !declaration.firstNamePart` -- both are
		// JavaScript string truthiness, so an empty string counts as absent.
		if declaration.SymbolName != nil && *declaration.SymbolName != "" {
			continue
		}
		if declaration.FirstNamePart == nil || *declaration.FirstNamePart == "" {
			continue
		}

		if moduleName != nil && declaration.ModuleName != *moduleName {
			continue
		}

		if firstNamePart != nil && (declaration.FirstNamePart == nil || *declaration.FirstNamePart != *firstNamePart) {
			continue
		}

		return declaration
	}

	return nil
}

// cloneMultipartModuleAliasDeclaration corresponds to
// _cloneMultipartModuleAliasDeclaration.
func (b *Binder) cloneMultipartModuleAliasDeclaration(
	node *parser.ImportFromNode,
	declaration *AliasDeclaration,
) *AliasDeclaration {
	clonedLoaderActions := cloneModuleLoaderActions(declaration)
	clonedDeclaration := &AliasDeclaration{
		DeclarationBase: DeclarationBase{
			Type:            DeclarationTypeAlias,
			Node:            node,
			Uri:             clonedLoaderActions.Uri,
			Range:           common.GetEmptyRange(),
			ModuleName:      declaration.ModuleName,
			IsInExceptSuite: b.isInExceptSuite,
		},
		LoadSymbolsFromPath: clonedLoaderActions.LoadSymbolsFromPath,
		UsesLocalName:       false,
		FirstNamePart:       declaration.FirstNamePart,
		ImplicitImports:     clonedLoaderActions.ImplicitImports,
	}

	if clonedLoaderActions.IsUnresolved {
		clonedDeclaration.IsUnresolved = true
	}

	if declaration.IsNativeLib {
		clonedDeclaration.IsNativeLib = true
	}

	return clonedDeclaration
}

// cloneModuleLoaderActions corresponds to _cloneModuleLoaderActions.
func cloneModuleLoaderActions(actions loaderActions) *ModuleLoaderActions {
	cloned := &ModuleLoaderActions{
		Uri:                 actions.getUri(),
		LoadSymbolsFromPath: actions.getLoadSymbolsFromPath(),
	}

	if actions.getIsUnresolved() {
		cloned.IsUnresolved = true
	}

	if actions.getImplicitImports() != nil {
		cloned.ImplicitImports = common.NewOrderedMap[string, *ModuleLoaderActions]()
		actions.getImplicitImports().ForEach(func(implicitImport *ModuleLoaderActions, name string) {
			cloned.ImplicitImports.Set(name, cloneModuleLoaderActions(implicitImport))
		})
	}

	return cloned
}

// mergeModuleLoaderActions corresponds to _mergeModuleLoaderActions.
func (b *Binder) mergeModuleLoaderActions(target loaderActions, source loaderActions) {
	if !source.getUri().IsEmpty() && (target.getUri().IsEmpty() || !target.getLoadSymbolsFromPath()) {
		target.setUri(source.getUri())
	}

	if source.getLoadSymbolsFromPath() {
		target.setLoadSymbolsFromPath(true)
	}

	if !source.getIsUnresolved() {
		// `delete target.isUnresolved` -- the property becomes absent, which
		// reads as false everywhere it is tested.
		target.setIsUnresolved(false)
	}

	if source.getImplicitImports() == nil {
		return
	}

	source.getImplicitImports().ForEach(func(implicitImport *ModuleLoaderActions, name string) {
		var targetImplicitImport *ModuleLoaderActions
		if target.getImplicitImports() != nil {
			targetImplicitImport, _ = target.getImplicitImports().Get(name)
		}
		if targetImplicitImport == nil {
			if target.getImplicitImports() == nil {
				target.setImplicitImports(common.NewOrderedMap[string, *ModuleLoaderActions]())
			}

			targetImplicitImport = cloneModuleLoaderActions(implicitImport)
			target.getImplicitImports().Set(name, targetImplicitImport)
			return
		}

		b.mergeModuleLoaderActions(targetImplicitImport, implicitImport)
	})
}
