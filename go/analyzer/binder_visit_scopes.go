/*
 * binder_visit_scopes.go
 *
 * The binder's visit methods for the constructs that create a scope --
 * class, function, lambda, type-parameter list, type alias -- plus visitCall
 * and visitModuleName. Transliterated from analyzer/binder.ts (pyright
 * 1.1.412).
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// commandCreateTypeStub corresponds to Commands.createTypeStub in
// commands/commands.ts. It is the only member of that enum the analyzer needs,
// so the rest of the file (which is language-server plumbing) is not ported.
const commandCreateTypeStub = "pyright.createtypestub"

// getCalledBuiltInName corresponds to the free function of the same name. The
// TypeScript defaults visitedSymbols to a new Set; pass nil for that. It
// returns "" and false where the TypeScript returns undefined.
func getCalledBuiltInName(
	scope *Scope,
	expression parser.ExpressionNode,
	visitedSymbols map[*Symbol]bool,
) (string, bool) {
	if visitedSymbols == nil {
		visitedSymbols = map[*Symbol]bool{}
	}

	switch typed := expression.(type) {
	case *parser.NameNode:
		symbolWithScope := scope.LookUpSymbolRecursive(typed.D.Value, nil)
		if symbolWithScope == nil || visitedSymbols[symbolWithScope.Symbol] {
			return "", false
		}
		visitedSymbols[symbolWithScope.Symbol] = true

		if symbolWithScope.Scope.Type == ScopeTypeBuiltin {
			return typed.D.Value, true
		}

		declarations := symbolWithScope.Symbol.GetDeclarations()
		var declaration Declaration
		if len(declarations) > 0 {
			declaration = declarations[len(declarations)-1]
		}

		if variable, ok := declaration.(*VariableDeclaration); ok && variable.InferredTypeSource != nil {
			sourceType := variable.InferredTypeSource.GetNodeType()
			if sourceType == parser.ParseNodeTypeName || sourceType == parser.ParseNodeTypeMemberAccess {
				// The original passes inferredTypeSource, a ParseNode, where an
				// ExpressionNode is expected; the two node types it just tested
				// for are both expressions.
				return getCalledBuiltInName(scope, variable.InferredTypeSource.(parser.ExpressionNode), visitedSymbols)
			}
		}

		if alias, ok := declaration.(*AliasDeclaration); ok &&
			alias.ModuleName == "builtins" &&
			alias.SymbolName != nil && *alias.SymbolName != "" {
			return *alias.SymbolName, true
		}

	case *parser.MemberAccessNode:
		leftName, ok := typed.D.LeftExpr.(*parser.NameNode)
		if !ok {
			break
		}

		symbolWithScope := scope.LookUpSymbolRecursive(leftName.D.Value, nil)
		var declarations []Declaration
		if symbolWithScope != nil {
			declarations = symbolWithScope.Symbol.GetDeclarations()
		}
		var declaration Declaration
		if len(declarations) > 0 {
			declaration = declarations[len(declarations)-1]
		}

		// `!decl.symbolName` is false for both an absent name and an empty one.
		if alias, ok := declaration.(*AliasDeclaration); ok &&
			alias.ModuleName == "builtins" &&
			(alias.SymbolName == nil || *alias.SymbolName == "") {
			return typed.D.Member.D.Value, true
		}
	}

	return "", false
}

// doesCallExposeClassNamespace corresponds to the free function of the same
// name.
func doesCallExposeClassNamespace(scope *Scope, node *parser.CallNode) bool {
	builtInName, ok := getCalledBuiltInName(scope, node.D.LeftExpr, nil)
	if !ok {
		return false
	}

	return builtInName == "exec" ||
		(builtInName == "eval" && len(node.D.Args) == 1) ||
		((builtInName == "locals" || builtInName == "vars") && len(node.D.Args) == 0)
}

// VisitModuleName corresponds to visitModuleName.
func (b *Binder) VisitModuleName(node *parser.ModuleNameNode) bool {
	importResult := GetImportInfo(node)
	assert(importResult != nil, "")

	if importResult.IsNativeLib {
		return true
	}

	if !importResult.IsImportFound && importResult.ImportName != "" {
		b.addDiagnostic(
			DiagnosticRuleReportMissingImports,
			localization.LocMessage.ImportResolveFailure().Format(
				importResult.ImportName,
				b.fileInfo.ExecutionEnvironment.Name,
			),
			node.GetRange(),
		)
		return true
	}

	// See if a source file was found but it's not part of a py.typed library
	// and no type stub is found.
	reportStubMissing := false
	if !importResult.IsStubFile &&
		importResult.ImportType == ImportTypeThirdParty &&
		importResult.PyTypedInfo == nil {
		reportStubMissing = true

		// If the import is a namespace package, it's possible that all of the
		// targeted import symbols are py.typed submodules. In this case,
		// suppress the missing stub diagnostic.
		if importResult.IsNamespacePackage {
			if importFrom, ok := node.NodeBase().Parent.(*parser.ImportFromNode); ok {
				allPyTyped := true
				for _, importAs := range importFrom.D.Imports {
					var implicitImport *ImplicitImport
					if importResult.FilteredImplicitImports != nil {
						implicitImport, _ = importResult.FilteredImplicitImports.Get(importAs.D.Name.D.Value)
					}
					if implicitImport == nil || implicitImport.PyTypedInfo == nil {
						allPyTyped = false
						break
					}
				}
				if allPyTyped {
					reportStubMissing = false
				}
			}
		}
	}

	if reportStubMissing {
		diagnostic := b.addDiagnostic(
			DiagnosticRuleReportMissingTypeStubs,
			localization.LocMessage.StubFileMissing().Format(importResult.ImportName),
			node.GetRange(),
		)
		if diagnostic != nil {
			// Add a diagnostic action for resolving this diagnostic.
			diagnostic.AddAction(&common.CreateTypeStubFileAction{
				Action:     commandCreateTypeStub,
				ModuleName: importResult.ImportName,
			})
		}
	}

	return true
}

// VisitClass corresponds to visitClass.
func (b *Binder) VisitClass(node *parser.ClassNode) bool {
	b.WalkMultiple(decoratorNodes(node.D.Decorators))

	classDeclaration := &ClassDeclaration{
		DeclarationBase: DeclarationBase{
			Type:            DeclarationTypeClass,
			Node:            node,
			Uri:             b.fileInfo.FileUri,
			Range:           common.ConvertTextRangeToRange(node.D.Name.GetRange(), b.fileInfo.Lines),
			ModuleName:      b.fileInfo.ModuleName,
			IsInExceptSuite: b.isInExceptSuite,
		},
	}

	symbol := b.bindNameToScope(b.currentScope, node.D.Name, nil)
	if symbol != nil {
		symbol.AddDeclaration(classDeclaration)
	}

	// Stash the declaration in the parse node for later access.
	SetDeclaration(node, classDeclaration)

	var typeParamScope *Scope
	if node.D.TypeParams != nil {
		b.Walk(node.D.TypeParams)
		typeParamScope = GetScope(node.D.TypeParams)
	}

	b.WalkMultiple(argumentNodes(node.D.Arguments))

	parentScope := typeParamScope
	if parentScope == nil {
		parentScope = b.getNonClassParentScope()
	}

	b.createNewScope(
		ScopeTypeClass,
		parentScope,
		nil, // proxyScope
		nil, // chainedModuleLevelScopeLookup
		func() {
			SetScope(node, b.currentScope)

			b.addImplicitSymbolToCurrentScope("__doc__", node, IntrinsicTypeStrOrNone, true)
			b.addImplicitSymbolToCurrentScope("__module__", node, IntrinsicTypeStr, true)

			b.dunderSlotsEntries = nil
			b.dunderSlotsEntriesSet = false
			if !b.moduleSymbolOnly {
				// Analyze the suite.
				b.Walk(node.D.Suite)
			}

			// `__qualname__` is exposed via the metaclass (`type`) rather than
			// as a class/instance attribute, unlike `__doc__`/`__module__`. We
			// handle it after walking the suite so we can tell whether the
			// class body already declared it.
			existingQualname := b.currentScope.LookUpSymbol("__qualname__")
			if existingQualname != nil {
				// The class explicitly declares `__qualname__` (e.g. typeshed
				// `type`, `function`, or a user `__qualname__ = "..."`). Keep
				// that real declaration untouched so hover, go-to-definition,
				// and completion all resolve to it. We must not append a
				// synthetic empty-range Intrinsic declaration on top, because
				// declaration-selecting consumers (e.g.
				// getLastTypedDeclarationForSymbol) would otherwise resolve to
				// the empty range instead of the real declaration. We still
				// mark it as ignored for protocol matching, matching the
				// implicit-dunder treatment.
				existingQualname.SetIsIgnoredForProtocolMatch()
			} else {
				// The class does not declare `__qualname__`. Add it as a
				// non-class member so it is name-resolvable within the class
				// body (e.g. `print(__qualname__)`) but is not exposed as a
				// class/instance attribute. Otherwise instance access
				// (`instance.__qualname__`) would incorrectly resolve instead
				// of reporting an attribute-access error.
				b.addImplicitSymbolToCurrentScope("__qualname__", node, IntrinsicTypeStr, false /* isClassMember */)
			}

			if b.dunderSlotsEntriesSet {
				b.addSlotsToCurrentScope(b.dunderSlotsEntries)
			}
			b.dunderSlotsEntries = nil
			b.dunderSlotsEntriesSet = false
		},
	)

	b.createAssignmentTargetFlowNodes(node.D.Name, false /* walkTargets */, false /* unbound */)

	return false
}

// VisitFunction corresponds to visitFunction.
func (b *Binder) VisitFunction(node *parser.FunctionNode) bool {
	b.createVariableAnnotationFlowNode()
	SetFlowNode(node, b.currentFlowNode)

	symbol := b.bindNameToScope(b.currentScope, node.D.Name, nil)
	containingClassNode := GetEnclosingClass(node, true /* stopAtFunction */)
	functionDeclaration := &FunctionDeclaration{
		DeclarationBase: DeclarationBase{
			Type:            DeclarationTypeFunction,
			Node:            node,
			Uri:             b.fileInfo.FileUri,
			Range:           common.ConvertTextRangeToRange(node.D.Name.GetRange(), b.fileInfo.Lines),
			ModuleName:      b.fileInfo.ModuleName,
			IsInExceptSuite: b.isInExceptSuite,
		},
		IsMethod:    containingClassNode != nil,
		IsGenerator: false,
	}

	if symbol != nil {
		symbol.AddDeclaration(functionDeclaration)
	}

	// Stash the declaration in the parse node for later access.
	SetDeclaration(node, functionDeclaration)

	// Walk the default values prior to the type parameters.
	for _, param := range node.D.Params {
		if param.D.DefaultValue != nil {
			b.Walk(param.D.DefaultValue)
		}
	}

	var typeParamScope *Scope
	if node.D.TypeParams != nil {
		b.Walk(node.D.TypeParams)
		typeParamScope = GetScope(node.D.TypeParams)
	}

	b.WalkMultiple(decoratorNodes(node.D.Decorators))

	for _, param := range node.D.Params {
		if param.D.Annotation != nil {
			b.Walk(param.D.Annotation)
		}

		if param.D.AnnotationComment != nil {
			b.Walk(param.D.AnnotationComment)
		}
	}

	if node.D.ReturnAnnotation != nil {
		b.Walk(node.D.ReturnAnnotation)
	}

	if node.D.FuncAnnotationComment != nil {
		b.Walk(node.D.FuncAnnotationComment)
	}

	parentScope := typeParamScope
	if parentScope == nil {
		parentScope = b.getNonClassParentScope()
	}

	// Don't walk the body of the function until we're done analyzing the
	// current scope.
	b.createNewScope(
		ScopeTypeFunction,
		parentScope,
		nil, // proxyScope
		nil, // chainedModuleLevelScopeLookup
		func() {
			SetScope(node, b.currentScope)

			enclosingClass := GetEnclosingClass(node, false)
			if enclosingClass != nil {
				// Add the implicit "__class__" symbol described in PEP 3135.
				b.addImplicitSymbolToCurrentScope("__class__", node, IntrinsicTypeDunderClass, true)
			}

			b.deferBinding(func() {
				// Create a start node for the function.
				b.currentFlowNode = b.createStartFlowNode()
				b.codeFlowComplexity = 0

				for _, paramNode := range node.D.Params {
					if paramNode.D.Name != nil {
						symbol := b.bindNameToScope(b.currentScope, paramNode.D.Name, nil)

						if symbol != nil {
							paramDeclaration := &ParamDeclaration{
								DeclarationBase: DeclarationBase{
									Type:            DeclarationTypeParam,
									Node:            paramNode,
									Uri:             b.fileInfo.FileUri,
									Range:           common.ConvertTextRangeToRange(paramNode.GetRange(), b.fileInfo.Lines),
									ModuleName:      b.fileInfo.ModuleName,
									IsInExceptSuite: b.isInExceptSuite,
								},
							}

							symbol.AddDeclaration(paramDeclaration)
							SetDeclaration(paramNode.D.Name, paramDeclaration)
						}

						b.createFlowAssignment(paramNode.D.Name, false)
					}
				}

				b.targetFunctionDeclaration = functionDeclaration
				b.currentReturnTarget = &b.createBranchLabel(nil).FlowLabel

				// Walk the statements that make up the function.
				b.Walk(node.D.Suite)

				b.targetFunctionDeclaration = nil

				// Associate the code flow node at the end of the suite with the
				// suite.
				SetAfterFlowNode(node.D.Suite, b.currentFlowNode)

				// Compute the final return flow node and associate it with the
				// function's parse node. If this node is unreachable, then the
				// function never returns.
				b.addAntecedent(b.currentReturnTarget, b.currentFlowNode)
				returnFlowNode := b.finishReturnTarget(b.currentReturnTarget)

				SetAfterFlowNode(node, returnFlowNode)

				SetCodeFlowExpressions(node, b.currentScopeCodeFlowExpressions)
				SetCodeFlowComplexity(node, b.codeFlowComplexity)
			})
		},
	)

	b.createAssignmentTargetFlowNodes(node.D.Name, false /* walkTargets */, false /* unbound */)

	// We'll walk the child nodes in a deferred manner, so don't walk them now.
	return false
}

// VisitLambda corresponds to visitLambda.
func (b *Binder) VisitLambda(node *parser.LambdaNode) bool {
	b.createVariableAnnotationFlowNode()
	SetFlowNode(node, b.currentFlowNode)

	// Analyze the parameter defaults in the context of the parent's scope
	// before we add any names from the function's scope.
	for _, param := range node.D.Params {
		if param.D.DefaultValue != nil {
			b.Walk(param.D.DefaultValue)
		}
	}

	b.createNewScope(
		ScopeTypeFunction,
		b.getNonClassParentScope(),
		nil, // proxyScope
		nil, // chainedModuleLevelScopeLookup
		func() {
			SetScope(node, b.currentScope)

			enclosingClass := GetEnclosingClass(node, false)
			if enclosingClass != nil {
				// Lambdas create the same implicit __class__ closure as named
				// functions.
				b.addImplicitSymbolToCurrentScope("__class__", node, IntrinsicTypeDunderClass, true)
			}

			b.deferBinding(func() {
				// Create a start node for the lambda.
				b.currentFlowNode = b.createStartFlowNode()

				for _, paramNode := range node.D.Params {
					if paramNode.D.Name != nil {
						symbol := b.bindNameToScope(b.currentScope, paramNode.D.Name, nil)
						if symbol != nil {
							paramDeclaration := &ParamDeclaration{
								DeclarationBase: DeclarationBase{
									Type:            DeclarationTypeParam,
									Node:            paramNode,
									Uri:             b.fileInfo.FileUri,
									Range:           common.ConvertTextRangeToRange(paramNode.GetRange(), b.fileInfo.Lines),
									ModuleName:      b.fileInfo.ModuleName,
									IsInExceptSuite: b.isInExceptSuite,
								},
							}

							symbol.AddDeclaration(paramDeclaration)
							SetDeclaration(paramNode.D.Name, paramDeclaration)
						}

						b.createFlowAssignment(paramNode.D.Name, false)
						b.Walk(paramNode.D.Name)
						SetFlowNode(paramNode, b.currentFlowNode)
					}
				}

				// Walk the expression that makes up the lambda body.
				b.Walk(node.D.Expr)

				SetCodeFlowExpressions(node, b.currentScopeCodeFlowExpressions)
			})
		},
	)

	// We'll walk the child nodes in a deferred manner.
	return false
}

// VisitCall corresponds to visitCall.
func (b *Binder) VisitCall(node *parser.CallNode) bool {
	if b.currentScope.Type == ScopeTypeClass && doesCallExposeClassNamespace(b.currentScope, node) {
		b.currentScope.HasPotentiallyDynamicSymbolTable = true
	}

	b.disableTrueFalseTargets(func() {
		b.Walk(node.D.LeftExpr)

		sortedArgs := GetArgsByRuntimeOrder(node)

		for _, argNode := range sortedArgs {
			if b.currentFlowNode != nil {
				SetFlowNode(argNode, b.currentFlowNode)
			}
			b.Walk(argNode)
		}
	})

	// Create a call flow node. We'll skip this if the call is part of a
	// decorator. We assume that decorators are not NoReturn functions. There
	// are libraries that make extensive use of unannotated decorators, and this
	// can lead to a performance issue when walking the control flow graph if we
	// need to evaluate every decorator.
	if !IsNodeContainedWithinNodeType(node, parser.ParseNodeTypeDecorator) {
		// Skip if we're in an 'Annotated' annotation because this creates
		// problems for "No Return" return type analysis when annotation
		// evaluation is deferred.
		if !b.isInAnnotatedAnnotation {
			b.createCallFlowNode(node)
		}
	}

	// Is this a manipulation of dunder all?
	if b.currentScope.Type == ScopeTypeModule {
		if memberAccess, ok := node.D.LeftExpr.(*parser.MemberAccessNode); ok {
			if leftName, ok := memberAccess.D.LeftExpr.(*parser.NameNode); ok && leftName.D.Value == "__all__" {
				b.visitDunderAllCall(node, memberAccess)
			}
		}
	}

	return false
}

// visitDunderAllCall is the `__all__.<member>(...)` branch of visitCall.
func (b *Binder) visitDunderAllCall(node *parser.CallNode, memberAccess *parser.MemberAccessNode) {
	emitDunderAllWarning := true

	switch {
	// Is this a call to "__all__.extend()"?
	case memberAccess.D.Member.D.Value == "extend" && len(node.D.Args) == 1:
		argExpr := node.D.Args[0].D.ValueExpr

		if list, ok := argExpr.(*parser.ListNode); ok {
			// Is this a call to "__all__.extend([<list>])"?
			//
			// The original pushes inside the `every` callback, so entries
			// before the first non-string one are added even when the overall
			// test fails and the warning is emitted.
			allStrings := true
			for _, listEntryNode := range list.D.Items {
				if str, ok := singleStringOfList(listEntryNode); ok {
					b.appendDunderAllName(str)
					continue
				}
				allStrings = false
				break
			}
			if allStrings {
				emitDunderAllWarning = false
			}
		} else if argMember, ok := argExpr.(*parser.MemberAccessNode); ok &&
			argMember.D.LeftExpr.GetNodeType() == parser.ParseNodeTypeName &&
			argMember.D.Member.D.Value == "__all__" {
			// Is this a call to "__all__.extend(<mod>.__all__)"?
			namesToAdd := b.getDunderAllNamesFromImport(argMember.D.LeftExpr.(*parser.NameNode).D.Value)
			if len(namesToAdd) > 0 {
				for _, name := range namesToAdd {
					b.pushDunderAllName(name)
				}
			}
			emitDunderAllWarning = false
		}

	// Is this a call to "__all__.remove()"?
	case memberAccess.D.Member.D.Value == "remove" && len(node.D.Args) == 1:
		argExpr := node.D.Args[0].D.ValueExpr
		if value, ok := singleStringValueOf(argExpr); ok && b.dunderAllNamesSet {
			filteredNames := []string{}
			for _, name := range b.dunderAllNames {
				if name != value {
					filteredNames = append(filteredNames, name)
				}
			}
			b.dunderAllNames = filteredNames

			filteredNodes := []*parser.StringNode{}
			for _, stringNode := range b.dunderAllStringNodes {
				if stringNode.D.Value.String() != value {
					filteredNodes = append(filteredNodes, stringNode)
				}
			}
			b.dunderAllStringNodes = filteredNodes
			emitDunderAllWarning = false
		}

	// Is this a call to "__all__.append()"?
	case memberAccess.D.Member.D.Value == "append" && len(node.D.Args) == 1:
		argExpr := node.D.Args[0].D.ValueExpr
		if str, ok := singleStringOfList(argExpr); ok {
			b.appendDunderAllName(str)
			emitDunderAllWarning = false
		}
	}

	if emitDunderAllWarning {
		b.usesUnsupportedDunderAllForm = true

		b.addDiagnostic(
			DiagnosticRuleReportUnsupportedDunderAll,
			localization.LocMessage.UnsupportedDunderAllOperation(),
			node.GetRange(),
		)
	}
}

// VisitTypeParameterList corresponds to visitTypeParameterList.
func (b *Binder) VisitTypeParameterList(node *parser.TypeParameterListNode) bool {
	typeParamScope := NewScope(ScopeTypeTypeParameter, b.getNonClassParentScope(), b.currentScope, nil)

	for _, param := range node.D.Params {
		if param.D.BoundExpr != nil {
			b.Walk(param.D.BoundExpr)
		}
	}

	typeParamsSeen := map[string]bool{}

	for _, param := range node.D.Params {
		name := param.D.Name
		symbol := typeParamScope.AddSymbol(name.D.Value, SymbolFlagsNone)
		paramDeclaration := &TypeParamDeclaration{
			DeclarationBase: DeclarationBase{
				Type: DeclarationTypeTypeParam,
				Node: param,
				Uri:  b.fileInfo.FileUri,
				// The range is the whole type parameter list, not the
				// individual parameter -- that is what the original passes.
				Range:           common.ConvertTextRangeToRange(node.GetRange(), b.fileInfo.Lines),
				ModuleName:      b.fileInfo.ModuleName,
				IsInExceptSuite: b.isInExceptSuite,
			},
		}

		symbol.AddDeclaration(paramDeclaration)
		SetDeclaration(name, paramDeclaration)

		if typeParamsSeen[name.D.Value] {
			b.addSyntaxError(
				localization.LocMessage.TypeParameterExistingTypeParameter().Format(name.D.Value),
				name.GetRange(),
			)
		} else {
			typeParamsSeen[name.D.Value] = true
		}
	}

	for _, param := range node.D.Params {
		if param.D.DefaultExpr != nil {
			b.Walk(param.D.DefaultExpr)
		}
	}

	SetScope(node, typeParamScope)

	return false
}

// VisitTypeAlias corresponds to visitTypeAlias.
func (b *Binder) VisitTypeAlias(node *parser.TypeAliasNode) bool {
	b.bindNameToScope(b.currentScope, node.D.Name, nil)

	b.Walk(node.D.Name)

	var typeParamScope *Scope
	if node.D.TypeParams != nil {
		b.Walk(node.D.TypeParams)
		typeParamScope = GetScope(node.D.TypeParams)
	}

	typeAliasDeclaration := &TypeAliasDeclaration{
		DeclarationBase: DeclarationBase{
			Type:            DeclarationTypeTypeAlias,
			Node:            node,
			Uri:             b.fileInfo.FileUri,
			Range:           common.ConvertTextRangeToRange(node.D.Name.GetRange(), b.fileInfo.Lines),
			ModuleName:      b.fileInfo.ModuleName,
			IsInExceptSuite: b.isInExceptSuite,
		},
		DocString: b.getVariableDocString(node.D.Expr),
	}

	// The original calls _bindNameToScope a second time here; the second call
	// finds the symbol the first created and returns it.
	symbol := b.bindNameToScope(b.currentScope, node.D.Name, nil)
	if symbol != nil {
		symbol.AddDeclaration(typeAliasDeclaration)
	}

	// Stash the declaration in the parse node for later access.
	SetDeclaration(node, typeAliasDeclaration)

	b.createAssignmentTargetFlowNodes(node.D.Name, true /* walkTargets */, false /* unbound */)

	prevScope := b.currentScope
	if typeParamScope != nil {
		b.currentScope = typeParamScope
	}
	b.Walk(node.D.Expr)
	b.currentScope = prevScope

	return false
}

// finishReturnTarget applies _finishFlowLabel to the current return target,
// which is a plain FlowLabel allocated as a branch label.
func (b *Binder) finishReturnTarget(label *FlowLabel) FlowNode {
	return b.finishFlowLabel(label, label)
}

// decoratorNodes widens a decorator slice for WalkMultiple.
func decoratorNodes(decorators []*parser.DecoratorNode) []parser.ParseNode {
	out := make([]parser.ParseNode, 0, len(decorators))
	for _, decorator := range decorators {
		out = append(out, decorator)
	}
	return out
}

// argumentNodes widens an argument slice for WalkMultiple.
func argumentNodes(args []*parser.ArgumentNode) []parser.ParseNode {
	out := make([]parser.ParseNode, 0, len(args))
	for _, arg := range args {
		out = append(out, arg)
	}
	return out
}

// singleStringOfList corresponds to the repeated test
// `x.nodeType === StringList && x.d.strings.length === 1 && x.d.strings[0].nodeType === String`,
// returning the StringNode.
func singleStringOfList(node parser.ParseNode) (*parser.StringNode, bool) {
	stringList, ok := node.(*parser.StringListNode)
	if !ok || len(stringList.D.Strings) != 1 {
		return nil, false
	}
	str, ok := stringList.D.Strings[0].(*parser.StringNode)
	if !ok {
		return nil, false
	}
	return str, true
}

// singleStringValueOf is singleStringOfList followed by reading `d.value`.
func singleStringValueOf(node parser.ParseNode) (string, bool) {
	str, ok := singleStringOfList(node)
	if !ok {
		return "", false
	}
	return str.D.Value.String(), true
}

// appendDunderAllName corresponds to the paired
// `this._dunderAllNames?.push(...)` / `this._dunderAllStringNodes?.push(...)`.
// The optional chaining makes both no-ops when __all__ has not been seen.
func (b *Binder) appendDunderAllName(str *parser.StringNode) {
	if b.dunderAllNamesSet {
		b.dunderAllNames = append(b.dunderAllNames, str.D.Value.String())
	}
	// _dunderAllStringNodes is never undefined -- it is initialized to [] and
	// only ever reassigned to an array -- so its optional chaining always runs.
	b.dunderAllStringNodes = append(b.dunderAllStringNodes, str)
}

// pushDunderAllName corresponds to a bare `this._dunderAllNames?.push(name)`
// with no matching string node.
func (b *Binder) pushDunderAllName(name string) {
	if b.dunderAllNamesSet {
		b.dunderAllNames = append(b.dunderAllNames, name)
	}
}
