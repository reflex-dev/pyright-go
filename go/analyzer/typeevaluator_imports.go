/*
 * typeevaluator_imports.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * evaluateTypesForImportAs, evaluateTypesForImportFromAs, getAliasedSymbolTypeForName,
 * getAliasFromImport, getTypeOfRevealType, getTypeOfRevealLocals,
 * getTypeOfArgExpectingType, convertNodeToArg.
 *
 * Import statements, and the two `reveal_*` pseudo-functions.
 *
 * An import binds a name, and the type it binds is found by resolving an ALIAS
 * declaration -- the binder recorded one at the import site pointing at the
 * module or symbol. `import a.b.c` binds only `a`, and `import a.b.c as x` binds
 * `x`, which is why the two forms pick their symbol node differently.
 *
 * `from m import x` has one thing the other form does not: the symbol may not
 * exist in `m`. When the alias resolves to nothing, this looks for a module-level
 * `__getattr__` before reporting -- PEP 562 lets a module answer for names it
 * does not define, and typeshed uses it. Only if that is absent is the name
 * reported unknown.
 *
 * Two redundant-looking cases mark the symbol accessed rather than leaving it to
 * the unused-import check: `from m import x as x` is the conventional way to
 * re-export, and an import inside a class body is an attribute of that class.
 * Neither is a mistake, so neither should be reported as unused.
 *
 * reveal_type() is not a real function -- it is a call the evaluator intercepts
 * and answers with an information diagnostic. Its two keyword arguments,
 * `expected_text` and `expected_type`, are what pyright's own test suite is
 * written in: a sample file asserts the printed type of an expression, and the
 * evaluator reports a mismatch. Getting this right is what makes those tests
 * mean anything against this port.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"

	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// evaluateTypesForImportAs corresponds to the function of the same name.
func (e *typeEvaluator) evaluateTypesForImportAs(node *parser.ImportAsNode) {
	if e.isTypeCached(node) {
		return
	}

	var symbolNameNode *parser.NameNode
	if node.D.Alias != nil {
		// The original's comment: the symbol name is defined by the alias.
		symbolNameNode = node.D.Alias
	} else {
		// The original's comment: there was no alias, so we need to use the first
		// element of the name parts as the symbol.
		if len(node.D.Module.D.NameParts) > 0 {
			symbolNameNode = node.D.Module.D.NameParts[0]
		}
	}

	if symbolNameNode == nil {
		// The original's comment: this can happen in certain cases where there are
		// parse errors.
		return
	}

	// The original's comment: look up the symbol to find the alias declaration.
	symbolType := e.getAliasedSymbolTypeForName(node, symbolNameNode.D.Value)
	if symbolType == nil {
		symbolType = UnknownTypeCreate(false)
	}

	// The original's comment: is there a cached module type associated with this
	// node? If so, use it instead of the type we just created.
	noFlags := EvalFlagsNone
	if cachedModuleType := e.readTypeCache(node, &noFlags); cachedModuleType != nil {
		if IsModule(cachedModuleType) && IsTypeSame(symbolType, cachedModuleType, TypeSameOptions{}, 0) {
			symbolType = cachedModuleType
		}
	}

	e.assignTypeToNameNode(symbolNameNode, &TypeResult{Type: symbolType}, false, nil, false, nil)

	e.writeTypeCache(node, &TypeResult{Type: symbolType}, &noFlags, nil, false)
}

// evaluateTypesForImportFromAs corresponds to the function of the same name.
func (e *typeEvaluator) evaluateTypesForImportFromAs(node *parser.ImportFromAsNode) {
	if e.isTypeCached(node) {
		return
	}

	aliasNode := node.D.Alias
	if aliasNode == nil {
		aliasNode = node.D.Name
	}
	fileInfo := GetFileInfo(node)

	// The original's comment: if this is a redundant form of an import, assume it
	// is an intentional export and mark the symbol as accessed.
	if node.D.Alias != nil && node.D.Alias.D.Value == node.D.Name.D.Value {
		if symbolInScope := e.LookUpSymbolRecursive(node, node.D.Name.D.Value, true); symbolInScope != nil {
			e.setSymbolAccessed(fileInfo, symbolInScope.Symbol, node)
		}
	}

	// The original's comment: if this is an import into a class scope, mark the
	// symbol as accessed.
	if classNode := GetEnclosingClass(node, true); classNode != nil {
		if symbolInScope := e.LookUpSymbolRecursive(node, aliasNode.D.Value, true); symbolInScope != nil {
			e.setSymbolAccessed(fileInfo, symbolInScope.Symbol, node)
		}
	}

	symbolType := e.getAliasedSymbolTypeForName(node, aliasNode.D.Value)
	if symbolType == nil {
		symbolType = e.resolveMissingImportedSymbol(node, fileInfo)
	}

	e.assignTypeToNameNode(aliasNode, &TypeResult{Type: symbolType}, false, nil, false, nil)
	noFlags := EvalFlagsNone
	e.writeTypeCache(node, &TypeResult{Type: symbolType}, &noFlags, nil, false)
}

// resolveMissingImportedSymbol is the original's `if (!symbolType)` block: the
// name was not found in the imported module, so try PEP 562's module-level
// __getattr__ before reporting it unknown.
func (e *typeEvaluator) resolveMissingImportedSymbol(
	node *parser.ImportFromAsNode, fileInfo *AnalyzerFileInfo,
) Type {
	parentNode, ok := node.NodeBase().Parent.(*parser.ImportFromNode)
	assert(ok && parentNode != nil, "Expected parent of import-from-as to be import-from")
	assert(!parentNode.D.IsWildcardImport, "Expected a non-wildcard import")

	var symbolType Type

	importInfo := GetImportInfo(parentNode.D.Module)
	if importInfo != nil && importInfo.IsImportFound && !importInfo.IsNativeLib {
		resolvedPath := importInfo.ResolvedUris[len(importInfo.ResolvedUris)-1]

		importLookupInfo := e.importLookup(resolvedPath, nil, nil)
		reportError := false

		// The original's comment: if we were able to resolve the import, report the
		// error as an unresolved symbol.
		if importLookupInfo != nil {
			reportError = true

			// The original's comment: handle PEP 562 support for module-level
			// __getattr__ function, introduced in Python 3.7.
			if fileInfo.ExecutionEnvironment.PythonVersion.IsGreaterOrEqualTo(common.PythonVersion3_7) ||
				fileInfo.IsStubFile {
				if getAttrSymbol, found := importLookupInfo.SymbolTable.Get("__getattr__"); found {
					getAttrType := e.GetEffectiveTypeOfSymbol(getAttrSymbol)
					if fn, isFunc := getAttrType.(*FunctionType); isFunc {
						symbolType = e.GetEffectiveReturnType(fn)
						reportError = false
					}
				}
			}
		} else if resolvedPath.IsEmpty() {
			// The original's comment: this corresponds to the "from . import a" form.
			reportError = true
		}

		if reportError {
			e.AddDiagnostic(DiagnosticRuleReportAttributeAccessIssue,
				localization.LocMessage.ImportSymbolUnknown().Format(node.D.Name.D.Value),
				node.D.Name, nil)
		}
	}

	if symbolType == nil {
		symbolType = UnknownTypeCreate(false)
	}
	return symbolType
}

// getAliasedSymbolTypeForName corresponds to the function of the same name. It
// returns nil where the original returns undefined.
func (e *typeEvaluator) getAliasedSymbolTypeForName(node parser.ParseNode, name string) Type {
	symbolWithScope := e.LookUpSymbolRecursive(node, name, true)
	if symbolWithScope == nil {
		return nil
	}

	// The original's comment: normally there will be at most one decl associated
	// with the import node, but there can be multiple in the case of the
	// "from .X import X" statement. In such case, we want to choose the last
	// declaration.
	var aliasDecl Declaration
	for _, decl := range symbolWithScope.Symbol.GetDeclarations() {
		if _, isAlias := decl.(*AliasDeclaration); !isAlias {
			continue
		}
		if IsNodeContainedWithin(node, decl.DeclBase().Node) {
			aliasDecl = decl
		}
	}

	// The original's comment: if we didn't find an exact match, look for any alias
	// associated with this symbol. In cases where we have multiple ImportAs nodes
	// that share the same first-part name (e.g. "import asyncio" and
	// "import asyncio.tasks"), we may not find the declaration associated with this
	// node.
	if aliasDecl == nil {
		for _, decl := range symbolWithScope.Symbol.GetDeclarations() {
			if _, isAlias := decl.(*AliasDeclaration); isAlias {
				aliasDecl = decl
				break
			}
		}
	}

	if aliasDecl == nil {
		return nil
	}

	fileInfo := GetFileInfo(node)

	// The original's comment: try to resolve the alias while honoring external
	// visibility.
	resolvedAliasInfo := e.ResolveAliasDeclarationWithInfo(aliasDecl, true,
		&EvaluatorResolveAliasOptions{AllowExternallyHiddenAccess: fileInfo.IsStubFile})

	if resolvedAliasInfo == nil {
		return nil
	}

	if resolvedAliasInfo.Declaration == nil {
		if e.evaluatorOptions.EvaluateUnknownImportsAsAny {
			return AnyTypeCreate(false)
		}
		return UnknownTypeCreate(false)
	}

	if importFromAs, ok := node.(*parser.ImportFromAsNode); ok {
		e.reportPrivateImportUsage(importFromAs, resolvedAliasInfo)
	}

	return e.getInferredTypeOfDeclaration(symbolWithScope.Symbol, aliasDecl)
}

// reportPrivateImportUsage is the original's `node.nodeType === ImportFromAs`
// block, which reports the two ways an import can reach into a module's private
// surface.
func (e *typeEvaluator) reportPrivateImportUsage(
	node *parser.ImportFromAsNode, resolvedAliasInfo *ResolvedAliasInfo,
) {
	if resolvedAliasInfo.IsPrivate {
		e.AddDiagnostic(DiagnosticRuleReportPrivateUsage,
			localization.LocMessage.PrivateUsedOutsideOfModule().Format(node.D.Name.D.Value),
			node.D.Name, nil)
	}

	if resolvedAliasInfo.PrivatePyTypedImporter == nil {
		return
	}

	diag := common.NewDiagnosticAddendum()
	if resolvedAliasInfo.PrivatePyTypedImported != nil {
		diag.AddMessage(localization.LocAddendum.PrivateImportFromPyTypedSource().Format(
			*resolvedAliasInfo.PrivatePyTypedImported))
	}
	e.AddDiagnostic(DiagnosticRuleReportPrivateImportUsage,
		localization.LocMessage.PrivateImportFromPyTypedModule().Format(
			node.D.Name.D.Value, *resolvedAliasInfo.PrivatePyTypedImporter)+diag.GetString(),
		node.D.Name, nil)
}

// GetAliasFromImport corresponds to getAliasFromImport. The original's comment
// at its caller: if the node is part of a "from X import Y as Z" statement and
// the node is the "Y" (non-aliased) name, we need to look up the alias symbol
// since the non-aliased name is not in the symbol table.
func (e *typeEvaluator) getAliasFromImport(node *parser.NameNode) *parser.NameNode {
	if importFromAs, ok := node.NodeBase().Parent.(*parser.ImportFromAsNode); ok {
		if importFromAs.D.Alias != nil && node == importFromAs.D.Name {
			return importFromAs.D.Alias
		}
	}
	return nil
}

/*
 * reveal_type() and reveal_locals(), which are not functions but calls the
 * evaluator answers itself.
 */

// getTypeOfRevealType corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfRevealType(
	node *parser.CallNode, inferenceContext *InferenceContext,
) *TypeResult {
	var arg0Value parser.ExpressionNode
	var expectedRevealTypeNode parser.ExpressionNode
	var expectedRevealType Type
	var expectedTextNode parser.ExpressionNode
	var expectedText *string

	// The original's comment: make sure there is only one positional argument
	// passed as arg 0.
	for index, arg := range node.D.Args {
		if index == 0 {
			if arg.D.ArgCategory == parser.ArgCategorySimple && arg.D.Name == nil {
				arg0Value = arg.D.ValueExpr
			}
			continue
		}

		if arg.D.ArgCategory != parser.ArgCategorySimple || arg.D.Name == nil {
			arg0Value = nil
			continue
		}

		switch arg.D.Name.D.Value {
		case "expected_text":
			expectedTextNode = arg.D.ValueExpr
			expectedTextType := e.GetTypeOfExpression(arg.D.ValueExpr, EvalFlagsNone, nil).Type

			literal, isString := literalStringValue(expectedTextType)
			if !isString {
				e.AddDiagnostic(DiagnosticRuleReportCallIssue,
					localization.LocMessage.RevealTypeExpectedTextArg(), arg.D.ValueExpr, nil)
			} else {
				expectedText = &literal
			}

		case "expected_type":
			expectedRevealTypeNode = arg.D.ValueExpr
			expectedRevealType = ConvertToInstance(
				e.getTypeOfArgExpectingType(e.ConvertNodeToArg(arg),
					&ExpectedTypeOptions{TypeExpression: true}).Type, false)
		}
	}

	if arg0Value == nil {
		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.RevealTypeArgs(), node, nil)
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	typeResult := e.GetTypeOfExpression(arg0Value, EvalFlagsNone, inferenceContext)
	t := typeResult.Type

	exprString := PrintExpression(arg0Value, PrintExpressionFlagsNone)
	typeString := e.PrintType(t, &PrintTypeOptions{ExpandTypeAlias: true})

	if !typeResult.IsIncomplete {
		if expectedText != nil && *expectedText != typeString {
			errorNode := expectedTextNode
			if errorNode == nil {
				errorNode = arg0Value
			}
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.RevealTypeExpectedTextMismatch().Format(*expectedText, typeString),
				errorNode, nil)
		}

		if expectedRevealType != nil {
			if !IsTypeSame(expectedRevealType, t, TypeSameOptions{IgnorePseudoGeneric: true}, 0) {
				expectedRevealTypeText := e.PrintType(expectedRevealType, nil)
				errorNode := expectedRevealTypeNode
				if errorNode == nil {
					errorNode = arg0Value
				}
				e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.RevealTypeExpectedTypeMismatch().Format(
						expectedRevealTypeText, typeString),
					errorNode, nil)
			}
		}

		e.AddInformation(localization.LocAddendum.TypeOfSymbol().Format(exprString, typeString),
			node.D.Args[0], nil)
	}

	return &TypeResult{Type: t, IsIncomplete: typeResult.IsIncomplete}
}

// literalStringValue is the original's three-part test that a type is a `str`
// instance carrying a string literal.
func literalStringValue(t Type) (string, bool) {
	classType, ok := t.(*ClassType)
	if !ok || !IsClassInstance(t) || !ClassTypeIsBuiltInNamed(classType, "str") {
		return "", false
	}
	literal, isString := classType.Priv.LiteralValue.(LiteralString)
	if !isString {
		return "", false
	}
	return string(literal), true
}

// getTypeOfRevealLocals corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfRevealLocals(node *parser.CallNode) Type {
	var curNode parser.ParseNode = node
	var scope *Scope

	for curNode != nil {
		scope = GetScopeForNode(curNode)

		// The original's comment: stop when we get a valid scope that's not a list
		// comprehension scope. That includes lambdas, functions, classes, and
		// modules.
		if scope != nil && scope.Type != ScopeTypeComprehension {
			break
		}

		curNode = curNode.NodeBase().Parent
	}

	infoMessages := []string{}

	if scope != nil {
		scope.SymbolTable.ForEach(func(symbol *Symbol, name string) {
			if symbol.IsIgnoredForProtocolMatch() {
				return
			}
			typeOfSymbol := e.GetEffectiveTypeOfSymbol(symbol)
			infoMessages = append(infoMessages,
				localization.LocAddendum.TypeOfSymbol().Format(name,
					e.PrintType(typeOfSymbol, &PrintTypeOptions{ExpandTypeAlias: true})))
		})
	}

	if len(infoMessages) > 0 {
		e.AddInformation(strings.Join(infoMessages, "\n"), node, nil)
	} else {
		e.AddInformation(localization.LocMessage.RevealLocalsNone(), node, nil)
	}

	return e.GetNoneType()
}

// getTypeOfArgExpectingType corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfArgExpectingType(arg *Arg, options *ExpectedTypeOptions) *TypeResult {
	if arg.TypeResult != nil {
		return &TypeResult{Type: arg.TypeResult.Type, IsIncomplete: arg.TypeResult.IsIncomplete}
	}

	// The original's comment: if there was no defined type provided, there should
	// always be a value expression from which we can retrieve the type.
	assert(arg.ValueExpression != nil, "expected a value expression")
	return e.GetTypeOfExpressionExpectingType(arg.ValueExpression, options)
}

// ConvertNodeToArg corresponds to convertNodeToArg.
func (e *typeEvaluator) ConvertNodeToArg(node *parser.ArgumentNode) *Arg {
	return &Arg{
		ArgCategory:     node.D.ArgCategory,
		Name:            node.D.Name,
		ValueExpression: node.D.ValueExpr,
	}
}
