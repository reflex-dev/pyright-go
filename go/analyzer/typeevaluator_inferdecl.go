/*
 * typeevaluator_inferdecl.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getInferredTypeOfDeclaration and isUnambiguousInference.
 *
 * This is where the inference path actually produces a type. inferTypeOfSymbolForUsage
 * decides which declarations to believe; this decides what each one means. Three
 * kinds of answer come out of it:
 *
 *   - an alias that resolves to a module, which is synthesized here rather than
 *     looked up, by walking the loader actions the binder recorded and building
 *     a ModuleType with a field for each implicit import;
 *   - a parameter, whose type is evaluated by evaluating the parameter;
 *   - a variable, whose type is evaluated by evaluating the statement that
 *     assigned it, then possibly promoted to a type alias.
 *
 * The py.typed ambiguity handling is the part that is easy to mistake for
 * incidental. In a py.typed package an unannotated variable's inferred type is
 * marked ambiguous, because other type checkers may not infer the same thing --
 * unless it is Final, a constant, an enum member, or the result of one of seven
 * named builtin factory calls. That list is carried verbatim.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// exemptPyTypedBuiltins is the original's `exemptBuiltins` array: calls to these
// are unambiguous even in a py.typed package.
var exemptPyTypedBuiltins = []string{
	"TypeVar",
	"ParamSpec",
	"TypeVarTuple",
	"TypedDict",
	"NamedTuple",
	"NewType",
	"TypeAliasType",
}

// getInferredTypeOfDeclaration corresponds to the function of the same name. It
// returns nil where the original returns undefined.
func (e *typeEvaluator) getInferredTypeOfDeclaration(symbol *Symbol, decl Declaration) Type {
	resolvedDecl := e.ResolveAliasDeclaration(decl, true, &EvaluatorResolveAliasOptions{
		AllowExternallyHiddenAccess: GetFileInfo(decl.DeclBase().Node).IsStubFile,
	})

	// The original's comment: we couldn't resolve the alias. Substitute an
	// unknown type in this case.
	if resolvedDecl == nil {
		if e.evaluatorOptions.EvaluateUnknownImportsAsAny {
			return AnyTypeCreate(false)
		}
		return UnknownTypeCreate(false)
	}

	// The original's comment: if the resolved declaration is still an alias, the
	// alias is pointing at a module, and we need to synthesize a module type.
	if aliasDecl, ok := resolvedDecl.(*AliasDeclaration); ok {
		return e.synthesizeModuleTypeForAlias(aliasDecl)
	}

	declaredType := e.getTypeForDeclaration(resolvedDecl)
	if declaredType.Type != nil {
		return declaredType.Type
	}

	// The original's comment: if this is part of a "py.typed" package, don't
	// fall back on type inference unless it's marked Final, is a constant, or is
	// a declared type alias.
	fileInfo := GetFileInfo(resolvedDecl.DeclBase().Node)
	isUnambiguousType := !fileInfo.IsInPyTypedPackage || fileInfo.IsStubFile

	if !isUnambiguousType {
		isUnambiguousType = e.isPyTypedDeclUnambiguous(resolvedDecl)
	}

	// The original's comment: if the resolved declaration had no defined type,
	// use the inferred type for this node.
	if paramDecl, ok := resolvedDecl.(*ParamDeclaration); ok {
		paramNode := paramDecl.Node.(*parser.ParameterNode)
		// The original asserts that the parameter has a name.
		if paramNode.D.Name == nil {
			return nil
		}
		result := e.evaluateTypeForSubnode(paramNode.D.Name, func() {
			e.EvaluateTypeOfParam(paramNode)
		})
		if result == nil {
			return nil
		}
		return result.Type
	}

	if variableDecl, ok := resolvedDecl.(*VariableDeclaration); ok && variableDecl.InferredTypeSource != nil {
		return e.inferTypeOfVariableDecl(symbol, decl, variableDecl, fileInfo, isUnambiguousType)
	}

	return nil
}

// synthesizeModuleTypeForAlias is the original's `if (resolvedDecl.type ===
// DeclarationType.Alias)` arm.
func (e *typeEvaluator) synthesizeModuleTypeForAlias(resolvedDecl *AliasDeclaration) Type {
	var moduleType *ModuleType

	// The original's comment: see if this is an import that shares a ModuleType
	// with another import statement. If so, used the cached type. This happens
	// when multiple import statements start with the same module name, such as
	// "import a.b" and "import a.c".
	if importAs, ok := resolvedDecl.Node.(*parser.ImportAsNode); ok {
		if cachedType := e.readTypeCache(importAs.D.Module, evalFlagsNonePtr()); cachedType != nil {
			if asModule, ok := cachedType.(*ModuleType); ok && IsModule(cachedType) {
				moduleType = asModule
			}
		}
	}

	if moduleType == nil {
		// The original's comment: build a module type that corresponds to the
		// declaration and its associated loader actions.
		moduleType = ModuleTypeCreate(resolvedDecl.ModuleName, resolvedDecl.Uri, nil)

		if importAs, ok := resolvedDecl.Node.(*parser.ImportAsNode); ok {
			e.writeTypeCache(importAs.D.Module, &TypeResult{Type: moduleType}, evalFlagsNonePtr(), nil, false)
		}
	}

	// The original passes the submoduleFallback when both a symbol name and a
	// fallback are present, and the declaration itself otherwise. An
	// AliasDeclaration is structurally a ModuleLoaderActions in the original;
	// this port has them as separate types, so the declaration's fields are
	// lifted into one.
	actions := &ModuleLoaderActions{
		Uri:                 resolvedDecl.Uri,
		LoadSymbolsFromPath: resolvedDecl.LoadSymbolsFromPath,
		ImplicitImports:     resolvedDecl.ImplicitImports,
	}
	if resolvedDecl.SymbolName != nil && resolvedDecl.SubmoduleFallback != nil {
		fallback := resolvedDecl.SubmoduleFallback
		actions = &ModuleLoaderActions{
			Uri:                 fallback.Uri,
			LoadSymbolsFromPath: fallback.LoadSymbolsFromPath,
			ImplicitImports:     fallback.ImplicitImports,
		}
	}

	return e.applyLoaderActionsToModuleType(moduleType, actions)
}

// applyLoaderActionsToModuleType corresponds to the nested function of the same
// name. The original closes over importLookup; the evaluator holds it here.
func (e *typeEvaluator) applyLoaderActionsToModuleType(
	moduleType *ModuleType,
	loaderActions *ModuleLoaderActions,
) Type {
	if !loaderActions.Uri.IsEmpty() && loaderActions.LoadSymbolsFromPath {
		if lookupResults := e.importLookup(loaderActions.Uri, nil, nil); lookupResults != nil {
			moduleType.Priv.Fields = lookupResults.SymbolTable
			moduleType.Priv.DocString = lookupResults.DocString
		} else {
			// The original's comment: note that all module attributes that are
			// not found in the symbol table should be treated as Any or Unknown
			// rather than as an error.
			if e.evaluatorOptions.EvaluateUnknownImportsAsAny {
				moduleType.Priv.NotPresentFieldType = AnyTypeCreate(false)
			} else {
				moduleType.Priv.NotPresentFieldType = UnknownTypeCreate(false)
			}
		}
	}

	if loaderActions.ImplicitImports != nil {
		loaderActions.ImplicitImports.ForEach(func(implicitImport *ModuleLoaderActions, name string) {
			existingLoaderField, hasExisting := moduleType.Priv.LoaderFields.Get(name)

			// Recursively apply loader actions.
			var symbolType Type

			if implicitImport.IsUnresolved {
				symbolType = UnknownTypeCreate(false)
			} else {
				var importedModuleType *ModuleType

				if hasExisting && existingLoaderField != nil {
					if existingType := existingLoaderField.GetSynthesizedType(); existingType != nil &&
						existingType.Type != nil && IsModule(existingType.Type) {
						importedModuleType = existingType.Type.(*ModuleType)
					}
				}

				if importedModuleType == nil {
					// `moduleType.priv.moduleName ? moduleType.priv.moduleName + '.' + name : ''`
					// -- an empty module name yields an empty name, not ".name".
					moduleName := ""
					if moduleType.Priv.ModuleName != "" {
						moduleName = moduleType.Priv.ModuleName + "." + name
					}
					importedModuleType = ModuleTypeCreate(moduleName, implicitImport.Uri, nil)
				}

				symbolType = e.applyLoaderActionsToModuleType(importedModuleType, implicitImport)
			}

			if !hasExisting || existingLoaderField == nil {
				importedModuleSymbol := SymbolCreateWithType(SymbolFlagsNone, symbolType, nil)
				moduleType.Priv.LoaderFields.Set(name, importedModuleSymbol)
			}
		})
	}

	return moduleType
}

// isPyTypedDeclUnambiguous is the original's `if (!isUnambiguousType)` block:
// the exemptions that make an unannotated variable in a py.typed package
// unambiguous anyway.
func (e *typeEvaluator) isPyTypedDeclUnambiguous(resolvedDecl Declaration) bool {
	variableDecl, ok := resolvedDecl.(*VariableDeclaration)
	if !ok {
		return false
	}

	// The original's comment: special-case variables within an enum class. These
	// are effectively constants, so we'll treat them as unambiguous.
	if enclosingClass := GetEnclosingClass(variableDecl.Node, true); enclosingClass != nil {
		classTypeInfo := e.GetTypeOfClass(enclosingClass)
		if classTypeInfo != nil && ClassTypeIsEnumClass(classTypeInfo.ClassType) {
			return true
		}
	}

	// The original's comment: special-case constants, which are treated as
	// unambiguous.
	if e.IsFinalVariableDeclaration(resolvedDecl) || variableDecl.IsConstant {
		return true
	}

	// The original's comment: special-case calls to certain built-in type
	// functions.
	if call, ok := variableDecl.InferredTypeSource.(*parser.CallNode); ok {
		baseTypeResult := e.getTypeOfExpression(call.D.LeftExpr, EvalFlagsCallBaseDefaults, nil)
		callType := baseTypeResult.Type

		if IsInstantiableClass(callType) &&
			ClassTypeIsBuiltInNamed(callType.(*ClassType), exemptPyTypedBuiltins...) {
			return true
		}

		if IsFunction(callType) {
			for _, name := range exemptPyTypedBuiltins {
				if FunctionTypeIsBuiltIn(callType.(*FunctionType), name) {
					return true
				}
			}
		}
	}

	return false
}

// inferTypeOfVariableDecl is the original's `if (resolvedDecl.type ===
// DeclarationType.Variable && resolvedDecl.inferredTypeSource)` arm.
func (e *typeEvaluator) inferTypeOfVariableDecl(
	symbol *Symbol,
	decl Declaration,
	variableDecl *VariableDeclaration,
	fileInfo *AnalyzerFileInfo,
	isUnambiguousType bool,
) Type {
	isTypeAlias := e.isExplicitTypeAliasDeclaration(variableDecl) || e.isPossibleTypeAliasOrTypedDict(variableDecl)

	// The original's comment: if this is a type alias, evaluate types for the
	// entire assignment statement rather than just the RHS of the assignment.
	typeSource := variableDecl.InferredTypeSource
	if isTypeAlias && variableDecl.InferredTypeSource.NodeBase().Parent != nil {
		typeSource = variableDecl.InferredTypeSource.NodeBase().Parent
	}

	var inferredType Type
	if result := e.evaluateTypeForSubnode(variableDecl.Node, func() {
		e.EvaluateTypesForStatement(typeSource)
	}); result != nil {
		inferredType = result.Type
	}

	if inferredType != nil && isTypeAlias && variableDecl.TypeAliasName != nil {
		// The original's comment: if this was a speculative type alias, it
		// becomes a real type alias only in the event that its inferred type is
		// instantiable or explicitly Any (but not an ellipsis).
		if e.isLegalImplicitTypeAliasType(inferredType) {
			typeAliasTypeVar := e.synthesizeTypeAliasPlaceholder(variableDecl.TypeAliasName, false)

			inferredType = e.transformTypeForTypeAlias(
				inferredType,
				variableDecl.Node,
				typeAliasTypeVar,
				false,
			)

			isUnambiguousType = true
		}
	}

	// The original's comment: determine whether we need to mark the annotation
	// as ambiguous.
	if inferredType != nil && fileInfo.IsInPyTypedPackage && !fileInfo.IsStubFile {
		if !isUnambiguousType {
			// The original's comment: see if this particular inference can be
			// considered "unambiguous". Any symbol that is assigned more than
			// once is considered ambiguous.
			if e.isUnambiguousInference(symbol, decl, inferredType) {
				isUnambiguousType = true
			}
		}

		if !isUnambiguousType {
			inferredType = CloneForAmbiguousType(inferredType)
		}
	}

	return inferredType
}

// isUnambiguousInference corresponds to the function of the same name. The
// original's comment: applies some heuristics to determine whether it's likely
// that all Python type checkers will infer the same type.
func (e *typeEvaluator) isUnambiguousInference(symbol *Symbol, decl Declaration, inferredType Type) bool {
	nonSlotsDecls := []Declaration{}
	for _, d := range symbol.GetDeclarations() {
		variableDecl, ok := d.(*VariableDeclaration)
		if !ok || !variableDecl.IsInferenceAllowedInPyTyped {
			nonSlotsDecls = append(nonSlotsDecls, d)
		}
	}

	// The original's comment: any symbol with more than one assignment is
	// considered ambiguous.
	if len(nonSlotsDecls) > 1 {
		return false
	}

	if _, ok := decl.(*VariableDeclaration); !ok {
		return false
	}

	// The original's comment: if there are no non-slots declarations, don't mark
	// the inferred type as ambiguous.
	if len(nonSlotsDecls) == 0 {
		return true
	}

	// The original's comment: TypeVar definitions don't require a declaration.
	if IsTypeVar(inferredType) {
		return true
	}

	var assignmentNode *parser.AssignmentNode

	if parentNode := decl.DeclBase().Node.NodeBase().Parent; parentNode != nil {
		// The original's comment: is this a simple assignment (x = y) or an
		// assignment of an instance variable (self.x = y)?
		if assignment, ok := parentNode.(*parser.AssignmentNode); ok {
			assignmentNode = assignment
		} else if memberAccess, ok := parentNode.(*parser.MemberAccessNode); ok {
			if assignment, ok := memberAccess.NodeBase().Parent.(*parser.AssignmentNode); ok {
				assignmentNode = assignment
			}
		}
	}

	if assignmentNode == nil {
		return false
	}

	assignedType := e.getTypeOfExpression(assignmentNode.D.RightExpr, EvalFlagsNone, nil).Type

	// The original's comment: assume that literal values will always result in
	// the same inferred type.
	if IsClassInstance(assignedType) && IsLiteralType(assignedType.(*ClassType)) {
		return true
	}

	// The original's comment: if the assignment is a simple name corresponding
	// to an unambiguous type, we'll assume the resulting variable will receive
	// the same unambiguous type.
	if assignmentNode.D.RightExpr.GetNodeType() == parser.ParseNodeTypeName && !assignedType.Base().IsAmbiguous() {
		return true
	}

	return false
}

// evaluateTypeForSubnode corresponds to the function of the same name: the
// non-contextual counterpart of evaluateContextualTypeForSubnode.
func (e *typeEvaluator) evaluateTypeForSubnode(subnode parser.ParseNode, callback func()) *TypeResult {
	return e.evaluateTypeForSubnodeWithCache(subnode, callback, e.readTypeCacheEntry)
}

/*
 * The three type-alias satellites this layer reaches.
 */
