/*
 * typeevaluator_declinfo.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getDeclInfoForNameNode, getDeclInfoForStringNode, getExpectedType,
 * getAliasFromImport, getDeclarationFromKeywordParam, _addSymbolDeclInfo,
 * _shouldFallBackToClassEntryForKeywordArg and
 * _addClassEntryDeclsForKeywordArgIfPresent.
 *
 * These answer "what does this name refer to?" for go-to-definition, hover and
 * find-all-references. That is a different question from "what type does this
 * name have", and the difference is why this is a separate path: a name can have
 * a perfectly good type while its *declaration* lives somewhere the type does
 * not record.
 *
 * The function is a dispatch over the syntactic position of the name, because
 * each position resolves through a different mechanism:
 *
 *   - `from x import Y as Z`, at the `Y`: the non-aliased name is not in any
 *     symbol table, so the alias symbol is looked up instead and its
 *     declarations filtered down to the ones this import statement produced.
 *   - a member access `a.b`, at the `b`: resolved through the base type's
 *     members, preferring declarations that carry an annotation.
 *   - a module name part: synthesized as an alias declaration pointing at the
 *     resolved file, since there is no declaration node to navigate to.
 *   - a keyword argument name: mapped to the corresponding parameter's
 *     declaration in whichever signature the call resolves to.
 *   - anything else: an ordinary scope lookup.
 *
 * The keyword-argument fallback exists because synthesized constructors --
 * TypedDict's especially -- have no meaningful `__init__` parameter to point at.
 * Falling back to the class entry is what lets rename and go-to-definition bind
 * to the field declaration instead of failing.
 *
 * getDeclInfoForStringNode covers the one case where a *string* has a
 * declaration: a literal key in a dict expression whose expected type is a
 * TypedDict. The entry check before the lookup is load-bearing -- without it,
 * `d["get"]` would resolve to the synthesized `get` method rather than to
 * nothing.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// GetDeclInfoForNameNode corresponds to getDeclInfoForNameNode.
func (e *typeEvaluator) GetDeclInfoForNameNode(
	node *parser.NameNode, skipUnreachableCode *bool,
) *SymbolDeclInfo {
	// The original's parameter defaults to true, so a nil pointer means true.
	skip := true
	if skipUnreachableCode != nil {
		skip = *skipUnreachableCode
	}

	if skip && IsCodeUnreachable(node) {
		return nil
	}

	info := &SymbolDeclInfo{}

	// The original's comment: if the node is part of a "from X import Y as Z"
	// statement and the node is the "Y" (non-aliased) name, we need to look up the
	// alias symbol since the non-aliased name is not in the symbol table.
	if alias := getAliasFromImport(node); alias != nil {
		e.declInfoForImportAlias(node, alias, info)
		return info
	}

	parentNode := node.NodeBase().Parent

	if memberAccess, ok := parentNode.(*parser.MemberAccessNode); ok && node == memberAccess.D.Member {
		e.declInfoForMemberAccess(memberAccess, info)
		return info
	}

	if moduleName, ok := parentNode.(*parser.ModuleNameNode); ok {
		e.declInfoForModuleNamePart(node, moduleName, info)
		return info
	}

	if argNode, ok := parentNode.(*parser.ArgumentNode); ok && node == argNode.D.Name {
		e.declInfoForKeywordArg(node, argNode, info)
		return info
	}

	fileInfo := GetFileInfo(node)

	// The original's comment: determine if this node is within a quoted type
	// annotation.
	isWithinTypeAnnotation := IsWithinTypeAnnotation(node, !IsAnnotationEvaluationPostponed(fileInfo))

	// The original's comment: determine if this is part of a "type" statement.
	isWithinTypeAliasStatement := GetParentNodeOfType(node, parser.ParseNodeTypeTypeAlias) != nil
	allowForwardReferences := isWithinTypeAnnotation || isWithinTypeAliasStatement || fileInfo.IsStubFile

	symbolWithScope := e.lookUpSymbolRecursive(node, node.D.Value, !allowForwardReferences,
		isWithinTypeAnnotation)

	if symbolWithScope != nil {
		addSymbolDeclInfo(symbolWithScope.Symbol, info, false)
	}

	return info
}

// declInfoForImportAlias is the original's `if (alias)` arm.
func (e *typeEvaluator) declInfoForImportAlias(
	node *parser.NameNode, alias *parser.NameNode, info *SymbolDeclInfo,
) {
	scope := GetScopeForNode(node)
	if scope == nil {
		return
	}

	symbolInScope := scope.LookUpSymbolRecursive(alias.D.Value, nil)
	if symbolInScope == nil {
		return
	}

	// The original's comment: the alias could have more decls that don't refer to
	// this import. Filter out the one(s) that specifically associated with this
	// import statement.
	declsForThisImport := []Declaration{}
	for _, decl := range symbolInScope.Symbol.GetDeclarations() {
		if aliasDecl, ok := decl.(*AliasDeclaration); ok &&
			aliasDecl.Node == node.NodeBase().Parent {
			declsForThisImport = append(declsForThisImport, decl)
		}
	}

	info.Decls = append(info.Decls,
		GetDeclarationsWithUsesLocalNameRemoved(declsForThisImport)...)
}

// declInfoForMemberAccess is the original's MemberAccess arm.
func (e *typeEvaluator) declInfoForMemberAccess(
	memberAccess *parser.MemberAccessNode, info *SymbolDeclInfo,
) {
	baseType := e.GetType(memberAccess.D.LeftExpr)
	if IsNilType(baseType) {
		return
	}

	baseType = e.MakeTopLevelTypeVarsConcrete(baseType, false)
	memberName := memberAccess.D.Member.D.Value

	DoForEachSubtype(baseType, func(subtype Type, _ int, _ []Type) {
		var symbol *Symbol

		subtype = e.MakeTopLevelTypeVarsConcrete(subtype, false)

		switch {
		case IsInstantiableClass(subtype):
			cls := subtype.(*ClassType)

			// The original's comment: try to find a member that has a declared
			// type. If so, that overrides any inferred types.
			member := LookUpClassMember(cls, memberName, MemberAccessFlagsDeclaredTypesOnly, nil)
			if member == nil {
				member = LookUpClassMember(cls, memberName, MemberAccessFlagsDefault, nil)
			}

			if member == nil {
				if metaclass := cls.Shared.EffectiveMetaclass; metaclass != nil &&
					IsInstantiableClass(metaclass) {
					member = LookUpClassMember(metaclass.(*ClassType), memberName,
						MemberAccessFlagsDefault, nil)
				}
			}

			if member != nil {
				symbol = member.Symbol
			}

		case IsClassInstance(subtype):
			cls := subtype.(*ClassType)

			// The original's comment: try to find a member that has a declared
			// type. If so, that overrides any inferred types.
			member := LookUpObjectMember(cls, memberName, MemberAccessFlagsDeclaredTypesOnly, nil)
			if member == nil {
				member = LookUpObjectMember(cls, memberName, MemberAccessFlagsDefault, nil)
			}
			if member != nil {
				symbol = member.Symbol
			}

		case IsModule(subtype):
			symbol = ModuleTypeGetField(subtype.(*ModuleType), memberName)
		}

		if symbol != nil {
			// The original's comment: by default, report only the declarations
			// that have type annotations. If there are none, then report all of the
			// unannotated declarations, which includes every assignment of that
			// symbol.
			addSymbolDeclInfo(symbol, info, true)
		}
	})
}

// declInfoForModuleNamePart is the original's ModuleName arm.
func (e *typeEvaluator) declInfoForModuleNamePart(
	node *parser.NameNode, moduleName *parser.ModuleNameNode, info *SymbolDeclInfo,
) {
	namePartIndex := -1
	for i, part := range moduleName.D.NameParts {
		if part == node {
			namePartIndex = i
			break
		}
	}

	importInfo := GetImportInfo(moduleName)
	if namePartIndex < 0 || importInfo == nil || importInfo.IsNativeLib ||
		namePartIndex >= len(importInfo.ResolvedUris) {
		return
	}

	if importInfo.ResolvedUris[namePartIndex].IsEmpty() {
		return
	}

	e.EvaluateTypesForStatement(node)

	// The original's comment: synthesize an alias declaration for this name part.
	// The only time this case is used is for IDE services such as the find all
	// references, hover provider and etc.
	info.Decls = append(info.Decls,
		SynthesizeAliasDeclaration(importInfo.ResolvedUris[namePartIndex]))
}

// declInfoForKeywordArg is the original's Argument arm. The original's comment:
// the target node is the name in a keyword argument. We need to determine whether
// the corresponding keyword parameter can be determined from the context.
func (e *typeEvaluator) declInfoForKeywordArg(
	node *parser.NameNode, argNode *parser.ArgumentNode, info *SymbolDeclInfo,
) {
	paramName := node.D.Value
	argParent := argNode.NodeBase().Parent

	if classNode, ok := argParent.(*parser.ClassNode); ok {
		classTypeResult := e.GetTypeOfClass(classNode)

		// The original's comment: validate the init subclass args for this class so
		// we can properly evaluate its custom keyword parameters.
		if classTypeResult != nil {
			e.ValidateInitSubclassArgs(classNode, classTypeResult.ClassType)
		}
		return
	}

	callNode, ok := argParent.(*parser.CallNode)
	if !ok {
		return
	}

	baseType := e.GetType(callNode.D.LeftExpr)
	if IsNilType(baseType) {
		return
	}

	switch {
	case IsFunction(baseType) && baseType.(*FunctionType).Shared.Declaration != nil:
		if paramDecl := e.declarationFromKeywordParam(
			baseType.(*FunctionType), paramName); paramDecl != nil {
			info.Decls = append(info.Decls, paramDecl)
		}

	case IsOverloaded(baseType):
		for _, f := range OverloadedTypeGetOverloads(baseType.(*OverloadedType)) {
			if paramDecl := e.declarationFromKeywordParam(f, paramName); paramDecl != nil {
				info.Decls = append(info.Decls, paramDecl)
			}
		}

	case IsInstantiableClass(baseType):
		baseClass := baseType.(*ClassType)
		var initMethodType Type
		if result := GetBoundInitMethod(e, callNode.D.LeftExpr,
			ClassTypeCloneAsInstance(baseClass, false), nil,
			MemberAccessFlagsSkipObjectBaseClass); result != nil {
			initMethodType = result.Type
		}

		if !IsNilType(initMethodType) && IsFunction(initMethodType) {
			paramDecl := e.declarationFromKeywordParam(initMethodType.(*FunctionType), paramName)
			if paramDecl != nil {
				info.Decls = append(info.Decls, paramDecl)
				return
			}
			if shouldFallBackToClassEntryForKeywordArg(baseClass, paramName) {
				addClassEntryDeclsForKeywordArgIfPresent(baseClass, paramName, info)
			}
			return
		}

		if shouldFallBackToClassEntryForKeywordArg(baseClass, paramName) {
			// The original's comment: some synthesized callables (notably TypedDict
			// "constructors") don't have a meaningful __init__ signature we can map
			// keyword arguments to. In these cases, treat the keyword as referring
			// to the class entry so IDE features like go-to-definition and rename
			// can bind to the field declaration.
			addClassEntryDeclsForKeywordArgIfPresent(baseClass, paramName, info)
		}
	}
}

// getAliasFromImport corresponds to the function of the same name.
func getAliasFromImport(node *parser.NameNode) *parser.NameNode {
	if importFromAs, ok := node.NodeBase().Parent.(*parser.ImportFromAsNode); ok &&
		importFromAs.D.Alias != nil && node == importFromAs.D.Name {
		return importFromAs.D.Alias
	}
	return nil
}

// declarationFromKeywordParam corresponds to getDeclarationFromKeywordParam.
func (e *typeEvaluator) declarationFromKeywordParam(
	t *FunctionType, paramName string,
) Declaration {
	if t.Shared.Declaration == nil {
		return nil
	}
	functionNode, ok := t.Shared.Declaration.Node.(*parser.FunctionNode)
	if !ok {
		return nil
	}

	functionScope := GetScope(functionNode)
	if functionScope == nil {
		return nil
	}

	if paramSymbol := functionScope.LookUpSymbol(paramName); paramSymbol != nil {
		for _, decl := range paramSymbol.GetDeclarations() {
			if _, ok := decl.(*ParamDeclaration); ok {
				return decl
			}
		}
		return nil
	}

	// An unpacked **kwargs TypedDict names its keywords as TypedDict fields, so
	// the declaration lives on the class rather than on a parameter.
	parameterDetails := GetParamListDetails(t, nil)
	if parameterDetails.UnpackedKwargsTypedDictType != nil {
		if lookupResults := LookUpClassMember(parameterDetails.UnpackedKwargsTypedDictType,
			paramName, MemberAccessFlagsDefault, nil); lookupResults != nil {
			decls := lookupResults.Symbol.GetDeclarations()
			if len(decls) > 0 {
				return decls[0]
			}
		}
	}

	return nil
}

// shouldFallBackToClassEntryForKeywordArg corresponds to
// _shouldFallBackToClassEntryForKeywordArg.
func shouldFallBackToClassEntryForKeywordArg(baseType *ClassType, paramName string) bool {
	return ClassTypeIsDataClass(baseType) || ClassTypeIsTypedDictClass(baseType) ||
		ClassTypeHasNamedTupleEntry(baseType, paramName)
}

// addClassEntryDeclsForKeywordArgIfPresent corresponds to
// _addClassEntryDeclsForKeywordArgIfPresent.
func addClassEntryDeclsForKeywordArgIfPresent(
	baseType *ClassType, paramName string, info *SymbolDeclInfo,
) {
	lookupResults := LookUpClassMember(baseType, paramName, MemberAccessFlagsDefault, nil)
	if lookupResults == nil {
		return
	}

	addSymbolDeclInfo(lookupResults.Symbol, info, false)
}

// addSymbolDeclInfo corresponds to _addSymbolDeclInfo.
func addSymbolDeclInfo(symbol *Symbol, info *SymbolDeclInfo, preferTypedDeclarations bool) {
	declCountBeforeAdd := len(info.Decls)

	var toAdd []Declaration
	if preferTypedDeclarations {
		toAdd = symbol.GetTypedDeclarations()
	}
	if len(toAdd) == 0 {
		toAdd = symbol.GetDeclarations()
	}
	info.Decls = append(info.Decls, toAdd...)

	synthTypeInfo := symbol.GetSynthesizedType()
	if synthTypeInfo == nil {
		return
	}
	info.SynthesizedTypes = append(info.SynthesizedTypes, synthTypeInfo)

	// The original's comment: some module members are represented only by a
	// synthesized module type, with no concrete declaration node to navigate to.
	// Synthesize a matching alias declaration so definition/declaration/reference
	// features can still bind to the submodule's file.
	if len(info.Decls) == declCountBeforeAdd && IsModule(synthTypeInfo.Type) &&
		!synthTypeInfo.Type.(*ModuleType).Priv.FileUri.IsEmpty() {
		info.Decls = append(info.Decls,
			SynthesizeAliasDeclaration(synthTypeInfo.Type.(*ModuleType).Priv.FileUri))
	}
}

// GetDeclInfoForStringNode corresponds to getDeclInfoForStringNode. The
// original's comment: in general, string nodes don't have any declarations
// associated with them, but we need to handle the special case of string literals
// used as keys within a dictionary expression where those keys are associated
// with a known TypedDict.
func (e *typeEvaluator) GetDeclInfoForStringNode(node *parser.StringNode) *SymbolDeclInfo {
	info := &SymbolDeclInfo{}

	var expectedType Type
	if result := e.GetExpectedType(node); result != nil {
		expectedType = result.Type
	}

	if IsNilType(expectedType) {
		return nil
	}

	nodeValue := node.D.Value.String()

	DoForEachSubtype(expectedType, func(subtype Type, _ int, _ []Type) {
		// The original's comment: if the expected type is a TypedDict then the node
		// is either a key expression or a single entry in a set. We then need to
		// check that the value of the node is a valid entry in the TypedDict to
		// avoid resolving declarations for synthesized symbols such as 'get'.
		if !IsClassInstance(subtype) || !ClassTypeIsTypedDictClass(subtype.(*ClassType)) {
			return
		}
		cls := subtype.(*ClassType)
		if cls.Shared.TypedDictEntries == nil {
			return
		}
		if entry, ok := cls.Shared.TypedDictEntries.KnownItems.Get(nodeValue); !ok || entry == nil {
			return
		}

		if member := LookUpObjectMember(cls, nodeValue, MemberAccessFlagsDefault, nil); member != nil {
			addSymbolDeclInfo(member.Symbol, info, false)
		}
	})

	if len(info.Decls) == 0 {
		return nil
	}
	return info
}

// GetExpectedType corresponds to getExpectedType.
func (e *typeEvaluator) GetExpectedType(node parser.ExpressionNode) *ExpectedTypeResult {
	// The original's comment: this is a primary entry point called by language
	// server providers, and it might be called before any other type evaluation has
	// occurred. Use this opportunity to do some initialization.
	e.initializePrefetchedTypes(node)

	// The original's comment: scan up the parse tree to find the top-most
	// expression node so we can evaluate the entire expression.
	var topExpression parser.ExpressionNode = node
	var curNode parser.ParseNode = node
	for curNode != nil {
		if parser.IsExpressionNode(curNode) {
			if expr, ok := curNode.(parser.ExpressionNode); ok {
				topExpression = expr
			}
		}

		curNode = curNode.NodeBase().Parent
	}

	// The original's comment: evaluate the expression. This will have the side
	// effect of storing an expected type in the expected type cache.
	e.evaluateTypesForExpressionInContext(topExpression)

	// The original's comment: look for the resulting expected type by scanning up
	// the parse tree.
	curNode = node
	for curNode != nil {
		if expectedType, ok := e.expectedTypeCache.Get(curNode.NodeBase().ID); ok && expectedType != nil {
			return &ExpectedTypeResult{
				Type:       expectedType.Type,
				Node:       curNode,
				Candidates: EnsureExpectedTypeCandidates(expectedType.Type, expectedType.Candidates),
			}
		}

		if curNode == parser.ParseNode(topExpression) {
			break
		}

		curNode = curNode.NodeBase().Parent
	}

	return nil
}
