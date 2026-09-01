/*
 * declarationutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Collection of static methods that operate on declarations.
 *
 * Transliterated from analyzer/declarationUtils.ts (pyright 1.1.412).
 *
 * This file holds the members that do not need import lookup:
 * hasTypeForDeclaration and areDeclarationsSame. The name accessors and
 * resolveAliasDeclaration are in declarationutils_resolve.go.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// ResolvedAliasInfo corresponds to the interface of the same name.
type ResolvedAliasInfo struct {
	Declaration            Declaration
	IsPrivate              bool
	PrivatePyTypedImported *string
	PrivatePyTypedImporter *string
}

// HasTypeForDeclaration corresponds to hasTypeForDeclaration.
func HasTypeForDeclaration(declaration Declaration) bool {
	switch declaration.DeclBase().Type {
	case DeclarationTypeIntrinsic,
		DeclarationTypeClass,
		DeclarationTypeSpecialBuiltInClass,
		DeclarationTypeFunction,
		DeclarationTypeTypeParam,
		DeclarationTypeTypeAlias:
		return true

	case DeclarationTypeParam:
		paramNode := declaration.DeclBase().Node.(*parser.ParameterNode)
		if paramNode.D.Annotation != nil || paramNode.D.AnnotationComment != nil {
			return true
		}

		// Handle function type comments.
		parameterParent := paramNode.NodeBase().Parent
		if funcNode, ok := parameterParent.(*parser.FunctionNode); ok {
			if funcNode.D.FuncAnnotationComment != nil && !funcNode.D.FuncAnnotationComment.D.IsEllipsis {
				paramAnnotations := funcNode.D.FuncAnnotationComment.D.ParamAnnotations

				// Handle the case where the annotation comment is missing an
				// annotation for the first parameter (self or cls).
				if len(funcNode.D.Params) > len(paramAnnotations) &&
					len(funcNode.D.Params) > 0 &&
					paramNode == funcNode.D.Params[0] {
					return false
				}

				return true
			}
		}
		return false

	case DeclarationTypeVariable:
		return declaration.(*VariableDeclaration).TypeAnnotationNode != nil

	case DeclarationTypeAlias:
		return false
	}

	return false
}

// AreDeclarationsSame corresponds to areDeclarationsSame. The TypeScript
// defaults treatModuleInImportAndFromImportSame and skipRangeForAliases to
// false.
func AreDeclarationsSame(
	decl1, decl2 Declaration,
	treatModuleInImportAndFromImportSame bool,
	skipRangeForAliases bool,
) bool {
	base1 := decl1.DeclBase()
	base2 := decl2.DeclBase()

	if base1.Type != base2.Type {
		return false
	}

	if !base1.Uri.Equals(base2.Uri) {
		return false
	}

	if !skipRangeForAliases || base1.Type != DeclarationTypeAlias {
		if base1.Range.Start.Line != base2.Range.Start.Line ||
			base1.Range.Start.Character != base2.Range.Start.Character {
			return false
		}
	}

	// Alias declarations refer to the entire import statement. We need to
	// further differentiate.
	alias1, isAlias1 := IsAliasDeclaration(decl1)
	alias2, isAlias2 := IsAliasDeclaration(decl2)
	if isAlias1 && isAlias2 {
		if !stringPtrEqual(alias1.SymbolName, alias2.SymbolName) || alias1.UsesLocalName != alias2.UsesLocalName {
			return false
		}

		if treatModuleInImportAndFromImportSame {
			// Treat "module" in "import [|module|]", "from [|module|] import ..."
			// or "from ... import [|module|]" same in IDE services.
			//
			// Some cases such as "from [|module|] import ...": the symbol for
			// [|module|] doesn't even exist and it can't be referenced inside of
			// a module, but nonetheless, the IDE still needs these sometimes for
			// things like hover tooltip, highlight references, find all
			// references and so on.
			return true
		}

		if alias1.Node != alias2.Node {
			return false
		}
	}

	return true
}
