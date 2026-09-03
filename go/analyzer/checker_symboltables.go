/*
 * checker_symboltables.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateSymbolTables and the seven per-symbol reporters it drives, plus
 * _conditionallyReportUnusedDeclaration, _isSymbolPrivate and
 * _validateOverloadImplementation.
 *
 * This is the checker's second pass. The walk reports what it sees statement by
 * statement; this pass reports what can only be known once a whole scope is
 * built -- that a name was never read, that two declarations of one name
 * disagree, that a Final was assigned twice or never.
 *
 * It runs over scopedNodes, which the walk accumulates, so it sees exactly the
 * scopes the walk entered.
 *
 * Two of the reporters are worth reading closely.
 *
 * _reportIncompatibleDeclarations decides which declarations are allowed to
 * coexist under one name. Its filter is the interesting part: overloads are
 * exempt, and so are property setters and deleters, which are legitimately
 * several `def`s with the same name. Those are told apart from a genuine
 * redeclaration by typeSourceId -- the setter's decorated type is a *clone* of
 * the getter's property object and carries the same id, so an equal id means
 * "same property" rather than "duplicate definition".
 *
 * _conditionallyReportUnusedDeclaration emits two things per unused name, not
 * one: an unused-code hint that the editor greys out, always, and a diagnostic,
 * only when the rule is enabled. That is why nameNode and diagnosticLevel are
 * tracked separately and both consulted at the end.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateSymbolTables corresponds to _validateSymbolTables.
func (c *Checker) validateSymbolTables() {
	var dependentFileInfo []*AnalyzerFileInfo
	for _, p := range c.dependentFiles {
		dependentFileInfo = append(dependentFileInfo, GetFileInfo(p.ParseTree))
	}

	for _, scopedNode := range c.scopedNodes {
		scope := GetScope(scopedNode)
		if scope == nil {
			continue
		}

		for _, name := range scope.SymbolTable.Keys() {
			symbol, _ := scope.SymbolTable.Get(name)

			c.conditionallyReportUnusedSymbol(name, symbol, scope.Type, dependentFileInfo)

			c.reportIncompatibleDeclarations(name, symbol)

			c.reportOverwriteOfImportedFinal(name, symbol)
			c.reportOverwriteOfBuiltinsFinal(name, symbol, scope)
			c.reportMultipleFinalDeclarations(name, symbol, scope.Type)

			c.reportFinalInLoop(symbol)

			c.reportMultipleTypeAliasDeclarations(name, symbol)

			c.reportInvalidOverload(name, symbol)
		}
	}

	// The original's comment: report unaccessed type parameters.
	accessedSymbolSet := c.fileInfo.AccessedSymbolSet
	for _, paramList := range c.typeParamLists {
		typeParamScope := GetScope(paramList)

		for _, param := range paramList.D.Params {
			var symbol *Symbol
			if typeParamScope != nil {
				symbol, _ = typeParamScope.SymbolTable.Get(param.D.Name.D.Value)
			}
			if symbol == nil {
				// The original's comment: this can happen if the code is
				// unreachable. Note that it returns from the whole function
				// rather than continuing, which also abandons any later
				// parameter lists.
				return
			}

			if !accessedSymbolSet.Has(symbol.ID) {
				for _, decl := range symbol.GetDeclarations() {
					c.conditionallyReportUnusedDeclaration(decl, false)
				}
			}
		}
	}
}

// reportInvalidOverload corresponds to _reportInvalidOverload.
func (c *Checker) reportInvalidOverload(name string, symbol *Symbol) {
	typedDecls := symbol.GetTypedDeclarations()
	if len(typedDecls) == 0 {
		return
	}

	primaryDecl, isFunctionDecl := typedDecls[0].(*FunctionDeclaration)
	if !isFunctionDecl {
		return
	}

	t := c.evaluator.GetEffectiveTypeOfSymbol(symbol)

	var overloads []*FunctionType
	switch {
	case IsOverloaded(t):
		overloads = OverloadedTypeGetOverloads(t.(*OverloadedType))
	case IsFunction(t) && FunctionTypeIsOverloaded(t.(*FunctionType)):
		overloads = []*FunctionType{t.(*FunctionType)}
	}

	// The original's comment: if the implementation has no name, it was
	// synthesized probably by a decorator that used a callable with a ParamSpec
	// that captured the overloaded signature. We'll exempt it from this check.
	if IsOverloaded(t) {
		allOverloads := OverloadedTypeGetOverloads(t.(*OverloadedType))
		if len(allOverloads) > 0 && allOverloads[0].Shared.Name == "" {
			return
		}
	} else if IsFunction(t) {
		if t.(*FunctionType).Shared.Name == "" {
			return
		}
	}

	if len(overloads) == 1 {
		// The original's comment: there should never be a single overload.
		c.evaluator.AddDiagnostic(DiagnosticRuleReportInconsistentOverload,
			localization.LocMessage.SingleOverload().Format(name),
			checkerFunctionNode(primaryDecl).D.Name, nil)
	}

	// The original's comment: if the file is not a stub and this is the first
	// overload, verify that there is an implementation.
	if c.fileInfo.IsStubFile || len(overloads) == 0 {
		return
	}

	var implementation Type
	if IsOverloaded(t) {
		implementation = OverloadedTypeGetImplementation(t.(*OverloadedType))
	} else if IsFunction(t) && !FunctionTypeIsOverloaded(t.(*FunctionType)) {
		implementation = t
	}

	if implementation == nil {
		c.reportMissingOverloadImplementation(t, primaryDecl, overloads)
		return
	}

	if !IsOverloaded(t) {
		return
	}

	if c.fileInfo.DiagnosticRuleSet.ReportInconsistentOverload == DiagnosticLevelNone {
		return
	}

	implFn, implIsFunction := implementation.(*FunctionType)

	// The original's comment: verify that all overload signatures are assignable
	// to implementation signature.
	for index, overload := range OverloadedTypeGetOverloads(t.(*OverloadedType)) {
		diag := common.NewDiagnosticAddendum()
		if !implIsFunction || c.validateOverloadImplementation(overload, implFn, diag) {
			continue
		}

		if implFn.Shared.Declaration == nil {
			continue
		}

		diagnostic := c.evaluator.AddDiagnostic(DiagnosticRuleReportInconsistentOverload,
			localization.LocMessage.OverloadImplementationMismatch().Format(name, index+1)+diag.GetString(),
			checkerFunctionNode(implFn.Shared.Declaration).D.Name, nil)

		if diagnostic != nil && overload.Shared.Declaration != nil {
			diagnostic.AddRelatedInfo(localization.LocAddendum.OverloadSignature(),
				overload.Shared.Declaration.Uri, overload.Shared.Declaration.Range)
		}
	}
}

// reportMissingOverloadImplementation is the original's `if (!implementation)`
// block, lifted out because it is long enough to obscure the cascade above it.
func (c *Checker) reportMissingOverloadImplementation(
	t Type, primaryDecl *FunctionDeclaration, overloads []*FunctionType,
) {
	// The original's comment: if this is a method within a protocol class, don't
	// require that there is an implementation.
	containingClassNode := GetEnclosingClassOrFunction(primaryDecl.Node)
	if classNode, ok := containingClassNode.(*parser.ClassNode); ok {
		if classType := c.evaluator.GetTypeOfClass(classNode); classType != nil {
			if ClassTypeIsProtocolClass(classType.ClassType) {
				return
			}

			if ClassTypeSupportsAbstractMethods(classType.ClassType) && IsOverloaded(t) {
				allAbstract := true
				for _, overload := range OverloadedTypeGetOverloads(t.(*OverloadedType)) {
					if !FunctionTypeIsAbstractMethod(overload) {
						allAbstract = false
						break
					}
				}
				if allAbstract {
					return
				}
			}
		}
	}

	// The original's comment: if the declaration isn't associated with any of the
	// overloads in the type, the overloads came from a decorator that captured
	// the overload from somewhere else.
	found := false
	for _, overload := range overloads {
		if overload.Shared.Declaration == primaryDecl {
			found = true
			break
		}
	}
	if !found {
		return
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportNoOverloadImplementation,
		localization.LocMessage.OverloadWithoutImplementation().
			Format(checkerFunctionNode(primaryDecl).D.Name.D.Value),
		checkerFunctionNode(primaryDecl).D.Name, nil)
}

// reportFinalInLoop corresponds to _reportFinalInLoop.
func (c *Checker) reportFinalInLoop(symbol *Symbol) {
	if !c.evaluator.IsFinalVariable(symbol) {
		return
	}

	decls := symbol.GetDeclarations()
	if len(decls) == 0 {
		return
	}

	if IsWithinLoop(decls[0].DeclBase().Node) {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.FinalInLoop(), decls[0].DeclBase().Node, nil)
	}
}

// reportOverwriteOfImportedFinal corresponds to
// _reportOverwriteOfImportedFinal. The original's comment: if a variable that is
// marked Final in one module is imported by another module, an attempt to
// overwrite the imported symbol should generate an error.
func (c *Checker) reportOverwriteOfImportedFinal(name string, symbol *Symbol) {
	if c.evaluator.IsFinalVariable(symbol) {
		return
	}

	decls := symbol.GetDeclarations()

	var finalImportDecl Declaration
	for _, decl := range decls {
		aliasDecl, ok := decl.(*AliasDeclaration)
		if !ok {
			continue
		}
		resolvedDecl := c.evaluator.ResolveAliasDeclaration(aliasDecl, true, nil)
		if varDecl, ok := resolvedDecl.(*VariableDeclaration); ok && varDecl.IsFinal {
			finalImportDecl = decl
			break
		}
	}

	if finalImportDecl == nil {
		return
	}

	for _, decl := range decls {
		if decl == finalImportDecl {
			continue
		}
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.FinalReassigned().Format(name),
			declErrorNode(decl), nil)
	}
}

// checkerFunctionNode, declClassNode and declTypeParamNode read back the concrete
// node a declaration is documented to carry. The original's discriminated union
// types decl.node per arm; DeclarationBase stores one ParseNode, so the arm's
// guarantee has to be spelled as an assertion.
func checkerFunctionNode(decl *FunctionDeclaration) *parser.FunctionNode {
	node, _ := decl.Node.(*parser.FunctionNode)
	return node
}

func declClassNode(decl *ClassDeclaration) *parser.ClassNode {
	node, _ := decl.Node.(*parser.ClassNode)
	return node
}

func declTypeParamNode(decl *TypeParamDeclaration) *parser.TypeParameterNode {
	node, _ := decl.Node.(*parser.TypeParameterNode)
	return node
}

// declErrorNode is the original's `getNameNodeForDeclaration(decl) ?? decl.node`.
func declErrorNode(decl Declaration) parser.ParseNode {
	if nameNode := GetNameNodeForDeclaration(decl); nameNode != nil {
		return nameNode
	}
	return decl.DeclBase().Node
}

// reportOverwriteOfBuiltinsFinal corresponds to
// _reportOverwriteOfBuiltinsFinal. The original's comment: if the builtins
// module (or any implicitly chained module) defines a Final variable, an attempt
// to overwrite it should generate an error.
func (c *Checker) reportOverwriteOfBuiltinsFinal(name string, symbol *Symbol, scope *Scope) {
	if scope.Type != ScopeTypeModule || scope.Parent == nil {
		return
	}

	shadowedSymbolInfo := scope.Parent.LookUpSymbolRecursive(name, nil)
	if shadowedSymbolInfo == nil {
		return
	}

	if !c.evaluator.IsFinalVariable(shadowedSymbolInfo.Symbol) {
		return
	}

	for _, decl := range symbol.GetDeclarations() {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.FinalReassigned().Format(name),
			declErrorNode(decl), nil)
	}
}

// reportMultipleFinalDeclarations corresponds to
// _reportMultipleFinalDeclarations. The original's comment: if a variable is
// marked Final, it should receive only one assigned value.
func (c *Checker) reportMultipleFinalDeclarations(name string, symbol *Symbol, scopeType ScopeType) {
	if !c.evaluator.IsFinalVariable(symbol) {
		return
	}

	decls := symbol.GetDeclarations()
	sawFinal := false
	sawAssignment := false

	for _, decl := range decls {
		if c.evaluator.IsFinalVariableDeclaration(decl) {
			if sawFinal {
				c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.FinalRedeclaration().Format(name),
					decl.DeclBase().Node, nil)
			}
			sawFinal = true
		}

		reportRedeclaration := false

		if varDecl, ok := decl.(*VariableDeclaration); ok {
			if varDecl.InferredTypeSource != nil {
				if sawAssignment {
					exemptAssignment := false

					if scopeType == ScopeTypeClass {
						// The original's comment: we check for assignment of Final
						// instance and class variables in the type evaluator
						// because we need to take into account whether the
						// assignment is within an `__init__` method, so ignore
						// class scopes here.
						classOrFunc := GetEnclosingClassOrFunction(decl.DeclBase().Node)
						if _, isFunc := classOrFunc.(*parser.FunctionNode); isFunc {
							exemptAssignment = true
						}
					}

					if !exemptAssignment {
						reportRedeclaration = true
					}
				}
				sawAssignment = true
			}
		} else {
			reportRedeclaration = true
		}

		if reportRedeclaration {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.FinalReassigned().Format(name),
				declErrorNode(decl), nil)
		}
	}

	// The original's comment: if it's not a stub file, an assignment must be
	// provided.
	if sawAssignment || c.fileInfo.IsStubFile {
		return
	}

	var firstDecl *VariableDeclaration
	for _, decl := range decls {
		if varDecl, ok := decl.(*VariableDeclaration); ok && varDecl.IsFinal {
			firstDecl = varDecl
			break
		}
	}
	if firstDecl == nil {
		return
	}

	// The original's comment: is this an instance variable declared within a
	// dataclass? If so, it is implicitly initialized by the synthesized
	// `__init__` method and therefore has an implied assignment. And: is this a
	// class variable within a protocol class? If so, it can be marked final
	// without providing a value.
	isImplicitlyAssigned := false
	isProtocolClass := false

	if symbol.IsClassMember() && !symbol.IsClassVar() {
		if containingClass := GetEnclosingClass(firstDecl.Node, true); containingClass != nil {
			classType := c.evaluator.GetTypeOfClass(containingClass)
			if classType != nil && IsClass(classType.DecoratedType) {
				decorated := classType.DecoratedType.(*ClassType)
				if ClassTypeIsDataClass(decorated) {
					isImplicitlyAssigned = true
				}
				if ClassTypeIsProtocolClass(decorated) {
					isProtocolClass = true
				}
			}
		}
	}

	if !isImplicitlyAssigned && !isProtocolClass {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.FinalUnassigned().Format(name), firstDecl.Node, nil)
	}
}

// reportMultipleTypeAliasDeclarations corresponds to
// _reportMultipleTypeAliasDeclarations.
func (c *Checker) reportMultipleTypeAliasDeclarations(name string, symbol *Symbol) {
	decls := symbol.GetDeclarations()

	var typeAliasDecl Declaration
	for _, decl := range decls {
		if c.evaluator.IsExplicitTypeAliasDeclaration(decl) {
			typeAliasDecl = decl
			break
		}
	}

	// The original's comment: if this is a type alias, there should be only one
	// declaration.
	if typeAliasDecl == nil || len(decls) <= 1 {
		return
	}

	for _, decl := range decls {
		if decl == typeAliasDecl {
			continue
		}
		c.evaluator.AddDiagnostic(DiagnosticRuleReportRedeclaration,
			localization.LocMessage.TypeAliasRedeclared().Format(name),
			decl.DeclBase().Node, nil)
	}
}

// conditionallyReportUnusedSymbol corresponds to
// _conditionallyReportUnusedSymbol.
func (c *Checker) conditionallyReportUnusedSymbol(
	name string, symbol *Symbol, scopeType ScopeType, dependentFileInfo []*AnalyzerFileInfo,
) {
	accessedSymbolSet := c.fileInfo.AccessedSymbolSet
	if symbol.IsIgnoredForProtocolMatch() || accessedSymbolSet.Has(symbol.ID) {
		return
	}

	// The original's comment: if this file is implicitly imported by other files,
	// we need to make sure the symbol defined in the current file is not accessed
	// from those other files.
	for _, info := range dependentFileInfo {
		if info.AccessedSymbolSet.Has(symbol.ID) {
			return
		}
	}

	// The original's comment: a name of "_" means "I know this symbol isn't
	// used", so don't report it as unused.
	if name == "_" {
		return
	}

	if IsDunderName(name) {
		return
	}

	isPrivate := c.isSymbolPrivate(name, scopeType)
	for _, decl := range symbol.GetDeclarations() {
		c.conditionallyReportUnusedDeclaration(decl, isPrivate)
	}
}

// conditionallyReportUnusedDeclaration corresponds to
// _conditionallyReportUnusedDeclaration.
func (c *Checker) conditionallyReportUnusedDeclaration(decl Declaration, isPrivate bool) {
	var diagnosticLevel DiagnosticLevel
	var nameNode *parser.NameNode
	message := ""
	rule := DiagnosticRule("")
	hasRule := false

	switch d := decl.(type) {
	case *AliasDeclaration:
		diagnosticLevel = c.fileInfo.DiagnosticRuleSet.ReportUnusedImport
		rule = DiagnosticRuleReportUnusedImport
		hasRule = true

		switch node := d.Node.(type) {
		case *parser.ImportAsNode:
			if node.D.Alias != nil {
				// The original's comment: for statements of the form "import x as
				// x", don't mark "x" as unaccessed because it's assumed to be
				// re-exported.
				if node.D.Alias.D.Value != d.ModuleName {
					nameNode = node.D.Alias
				}
			} else {
				nameParts := node.D.Module.D.NameParts
				// The original's comment: multi-part imports are handled
				// separately, so ignore those here.
				if len(nameParts) == 1 {
					nameNode = nameParts[0]
					// The original emits this diagnostic here *and* again at the
					// bottom of the function, so a single-part unused import is
					// reported twice. Reproduced rather than deduplicated.
					c.evaluator.AddDiagnostic(DiagnosticRuleReportUnusedImport,
						localization.LocMessage.UnaccessedImport().Format(nameNode.D.Value),
						nameNode, nil)
					message = localization.LocMessage.UnaccessedImport().Format(nameNode.D.Value)
				}
			}

		case *parser.ImportFromAsNode:
			importFrom, _ := node.NodeBase().Parent.(*parser.ImportFromNode)

			// The original's comment: for statements of the form "from y import x
			// as x", don't mark "x" as unaccessed because it's assumed to be
			// re-exported.
			isReexport := node.D.Alias != nil && node.D.Alias.D.Value == node.D.Name.D.Value

			// The original's comment: if this is a __future__ import, it's OK for
			// the import symbol to be unaccessed.
			isFuture := importFrom != nil &&
				len(importFrom.D.Module.D.NameParts) == 1 &&
				importFrom.D.Module.D.NameParts[0].D.Value == "__future__"

			if !isReexport && !isFuture {
				if node.D.Alias != nil {
					nameNode = node.D.Alias
				} else {
					nameNode = node.D.Name
				}
			}
		}

		if nameNode != nil {
			message = localization.LocMessage.UnaccessedImport().Format(nameNode.D.Value)
		}

	case *TypeAliasDeclaration, *VariableDeclaration, *ParamDeclaration:
		if !isPrivate {
			return
		}

		if c.fileInfo.IsStubFile {
			// The original's comment: don't mark variables or parameters as
			// unaccessed in stub files. It's typical for them to be unaccessed
			// here.
			return
		}

		diagnosticLevel = c.fileInfo.DiagnosticRuleSet.ReportUnusedVariable

		switch node := decl.DeclBase().Node.(type) {
		case *parser.NameNode:
			nameNode = node

			// The original's comment: don't emit a diagnostic if the name starts
			// with an underscore. This indicates that the variable is unused.
			if strings.HasPrefix(nameNode.D.Value, "_") {
				diagnosticLevel = DiagnosticLevelNone
			}

		case *parser.ParameterNode:
			nameNode = node.D.Name

			// The original's comment: don't emit a diagnostic for unused
			// parameters or type parameters.
			diagnosticLevel = DiagnosticLevelNone
		}

		if nameNode != nil {
			rule = DiagnosticRuleReportUnusedVariable
			hasRule = true
			message = localization.LocMessage.UnaccessedVariable().Format(nameNode.D.Value)
		}

	case *ClassDeclaration:
		if !isPrivate {
			return
		}

		// The original's comment: if a stub is exporting a private type, we'll
		// assume that the author knows what he or she is doing.
		if c.fileInfo.IsStubFile {
			return
		}

		diagnosticLevel = c.fileInfo.DiagnosticRuleSet.ReportUnusedClass
		nameNode = declClassNode(d).D.Name
		rule = DiagnosticRuleReportUnusedClass
		hasRule = true
		message = localization.LocMessage.UnaccessedClass().Format(nameNode.D.Value)

	case *FunctionDeclaration:
		if !isPrivate {
			return
		}

		// The original's comment: if a stub is exporting a private type, we'll
		// assume that the author knows what he or she is doing.
		if c.fileInfo.IsStubFile {
			return
		}

		diagnosticLevel = c.fileInfo.DiagnosticRuleSet.ReportUnusedFunction
		nameNode = checkerFunctionNode(d).D.Name
		rule = DiagnosticRuleReportUnusedFunction
		hasRule = true
		message = localization.LocMessage.UnaccessedFunction().Format(nameNode.D.Value)

	case *TypeParamDeclaration:
		// The original's comment: never report a diagnostic for an unused
		// TypeParam.
		diagnosticLevel = DiagnosticLevelNone
		nameNode = declTypeParamNode(d).D.Name

	default:
		// IntrinsicDeclaration and SpecialBuiltInClassDeclaration return; the
		// original's default arm is assertNever.
		return
	}

	if nameNode == nil {
		return
	}

	var action common.DiagnosticAction
	if hasRule && rule == DiagnosticRuleReportUnusedImport {
		action = unusedImportAction{}
	}

	c.fileInfo.DiagnosticSink.AddUnusedCodeWithTextRange(
		localization.LocMessage.UnaccessedSymbol().Format(nameNode.D.Value),
		nameNode.GetRange(), action)

	if hasRule && message != "" && diagnosticLevel != DiagnosticLevelNone {
		c.evaluator.AddDiagnostic(rule, message, nameNode, nil)
	}
}

// unusedImportAction is the original's `{ action: Commands.unusedImport }`.
type unusedImportAction struct{}

func (unusedImportAction) ActionName() string { return commandUnusedImport }

// isSymbolPrivate corresponds to _isSymbolPrivate.
func (c *Checker) isSymbolPrivate(nameValue string, scopeType ScopeType) bool {
	// The original's comment: all variables within the scope of a function or a
	// list comprehension are considered private.
	if scopeType == ScopeTypeFunction || scopeType == ScopeTypeComprehension {
		return true
	}

	// The original's comment: see if the symbol is private.
	if IsPrivateName(nameValue) {
		return true
	}

	if IsProtectedName(nameValue) {
		// The original's comment: protected names outside of a class scope are
		// considered private.
		return scopeType != ScopeTypeClass
	}

	return false
}

// validateOverloadImplementation corresponds to _validateOverloadImplementation:
// is this overload signature actually satisfiable by the implementation?
//
// Parameters and return type are checked separately and against opposite
// variances. The parameter check runs Contravariant with the overload as the
// destination, because the implementation must accept everything the overload
// promises to accept; the return check runs covariantly the usual way. The
// constraint tracker is shared between the two so a TypeVar solved from the
// parameters is already solved when the return types are compared.
func (c *Checker) validateOverloadImplementation(
	overload *FunctionType, implementation *FunctionType, diag *common.DiagnosticAddendum,
) bool {
	constraints := NewConstraintTracker()

	implBound := implementation
	overloadBound := overload

	if implementation.Shared.Declaration != nil && implementation.Shared.Declaration.Node != nil {
		if implNode := implementation.Shared.Declaration.Node.NodeBase().Parent; implNode != nil {
			liveScopeIds := GetTypeVarScopesForNode(implNode)
			if bound, ok := MakeTypeVarsBound(implementation, liveScopeIds, false).(*FunctionType); ok {
				implBound = bound
			}
		}
	}

	if overload.Shared.Declaration != nil && overload.Shared.Declaration.Node != nil {
		liveScopeIds := GetTypeVarScopesForNode(overload.Shared.Declaration.Node)
		if bound, ok := MakeTypeVarsBound(overload, liveScopeIds, false).(*FunctionType); ok {
			overloadBound = bound
		}
	}

	// The original's comment: first check the parameters to see if they are
	// assignable.
	isConsistent := c.evaluator.AssignType(
		overloadBound, implBound, diag, constraints,
		AssignTypeFlagsSkipReturnTypeCheck|AssignTypeFlagsContravariant|
			AssignTypeFlagsSkipSelfClsTypeCheck|AssignTypeFlagsDisallowExtraKwargsForTd,
		0)

	// The original's comment: now check the return types.
	overloadReturn := FunctionTypeGetEffectiveReturnType(overloadBound, true)
	if overloadReturn == nil {
		overloadReturn = c.evaluator.GetInferredReturnType(overloadBound, nil)
	}
	overloadReturnType := c.evaluator.SolveAndApplyConstraints(overloadReturn, constraints, nil, nil)

	implReturn := FunctionTypeGetEffectiveReturnType(implBound, true)
	if implReturn == nil {
		implReturn = c.evaluator.GetInferredReturnType(implBound, nil)
	}
	implReturnType := c.evaluator.SolveAndApplyConstraints(implReturn, constraints, nil, nil)

	returnDiag := common.NewDiagnosticAddendum()
	if !IsNever(overloadReturnType) &&
		!c.evaluator.AssignType(implReturnType, overloadReturnType, returnDiag.CreateAddendum(),
			constraints, AssignTypeFlagsDefault, 0) {
		returnDiag.AddMessage(localization.LocAddendum.FunctionReturnTypeMismatch().Format(
			c.evaluator.PrintType(overloadReturnType, nil),
			c.evaluator.PrintType(implReturnType, nil)))
		if diag != nil {
			diag.AddAddendum(returnDiag)
		}
		isConsistent = false
	}

	return isConsistent
}

// reportIncompatibleDeclarations corresponds to _reportIncompatibleDeclarations.
// The original's comment: if there's one or more declaration with a declared
// type, all other declarations should match. The only exception is for functions
// that have an overload.
func (c *Checker) reportIncompatibleDeclarations(name string, symbol *Symbol) {
	primaryDecl := GetLastTypedDeclarationForSymbol(symbol)

	// The original's comment: if there's no declaration with a declared type,
	// we're done.
	if primaryDecl == nil {
		return
	}

	// The original's comment: special case the '_' symbol, which is used in
	// single dispatch code and other cases where the name does not matter.
	if name == "_" {
		return
	}

	var otherDecls []Declaration
	for _, decl := range symbol.GetDeclarations() {
		if decl != primaryDecl {
			otherDecls = append(otherDecls, decl)
		}
	}

	// The original's comment: if it's a function, we can skip any other
	// declarations that are overloads or property setters/deleters.
	if primaryFuncDecl, ok := primaryDecl.(*FunctionDeclaration); ok {
		primaryDeclTypeInfo := c.evaluator.GetTypeOfFunction(checkerFunctionNode(primaryFuncDecl))

		var kept []Declaration
		for _, decl := range otherDecls {
			if c.declObscuresFunction(decl, primaryDeclTypeInfo) {
				kept = append(kept, decl)
			}
		}
		otherDecls = kept
	}

	// The original's comment: if there are no other declarations to consider,
	// we're done.
	if len(otherDecls) == 0 {
		return
	}

	primaryDeclInfo := primaryDeclAddendum(primaryDecl)

	// addPrimaryDeclInfo corresponds to the local closure of the same name.
	addPrimaryDeclInfo := func(diag *common.Diagnostic) {
		if diag == nil {
			return
		}

		var primaryDeclNode parser.ParseNode
		switch d := primaryDecl.(type) {
		case *FunctionDeclaration:
			primaryDeclNode = checkerFunctionNode(d).D.Name
		case *ClassDeclaration:
			primaryDeclNode = declClassNode(d).D.Name
		case *VariableDeclaration:
			if nameNode, ok := d.Node.(*parser.NameNode); ok {
				primaryDeclNode = nameNode
			}
		case *ParamDeclaration:
			if paramNode, ok := d.Node.(*parser.ParameterNode); ok && paramNode.D.Name != nil {
				primaryDeclNode = paramNode.D.Name
			}
		case *TypeParamDeclaration:
			if typeParamNode := declTypeParamNode(d); typeParamNode != nil && typeParamNode.D.Name != nil {
				primaryDeclNode = typeParamNode.D.Name
			}
		}

		if primaryDeclNode != nil {
			diag.AddRelatedInfo(primaryDeclInfo,
				primaryDecl.DeclBase().Uri, primaryDecl.DeclBase().Range)
		}
	}

	// A type parameter's redeclaration is reported elsewhere, so every arm below
	// exempts it.
	_, primaryIsTypeParam := primaryDecl.(*TypeParamDeclaration)

	for _, otherDecl := range otherDecls {
		switch d := otherDecl.(type) {
		case *ClassDeclaration:
			if primaryIsTypeParam {
				continue
			}
			diag := c.evaluator.AddDiagnostic(DiagnosticRuleReportRedeclaration,
				localization.LocMessage.ObscuredClassDeclaration().Format(name),
				declClassNode(d).D.Name, nil)
			addPrimaryDeclInfo(diag)

		case *FunctionDeclaration:
			duplicateIsOk := primaryIsTypeParam

			var primaryType, otherType Type
			if info := c.evaluator.GetTypeForDeclaration(primaryDecl); info != nil {
				primaryType = info.Type
			}
			// The original's comment: if the return type has not yet been
			// inferred, do so now.
			if primaryType != nil && IsFunction(primaryType) {
				c.evaluator.GetInferredReturnType(primaryType.(*FunctionType), nil)
			}

			if info := c.evaluator.GetTypeForDeclaration(otherDecl); info != nil {
				otherType = info.Type
			}
			if otherType != nil && IsFunction(otherType) {
				c.evaluator.GetInferredReturnType(otherType.(*FunctionType), nil)
			}

			// The original's comment: allow same-signature overrides in cases
			// where the declarations are not within the same statement suite (e.g.
			// one in the "if" and another in the "else").
			isInSameStatementList := GetEnclosingSuite(primaryDecl.DeclBase().Node) ==
				GetEnclosingSuite(otherDecl.DeclBase().Node)

			// The original's comment: if both declarations are functions, it's OK
			// if they both have the same signatures.
			if !isInSameStatementList && primaryType != nil && otherType != nil &&
				IsTypeSame(primaryType, otherType, TypeSameOptions{}, 0) {
				duplicateIsOk = true
			}

			if duplicateIsOk {
				continue
			}

			message := localization.LocMessage.ObscuredFunctionDeclaration().Format(name)
			if d.IsMethod {
				message = localization.LocMessage.ObscuredMethodDeclaration().Format(name)
			}
			diag := c.evaluator.AddDiagnostic(DiagnosticRuleReportRedeclaration,
				message, checkerFunctionNode(d).D.Name, nil)
			addPrimaryDeclInfo(diag)

		case *ParamDeclaration:
			paramNode, ok := d.Node.(*parser.ParameterNode)
			if !ok || paramNode.D.Name == nil || primaryIsTypeParam {
				continue
			}
			diag := c.evaluator.AddDiagnostic(DiagnosticRuleReportRedeclaration,
				localization.LocMessage.ObscuredParameterDeclaration().Format(name),
				paramNode.D.Name, nil)
			addPrimaryDeclInfo(diag)

		case *VariableDeclaration:
			if d.TypeAnnotationNode == nil {
				continue
			}
			if _, ok := d.Node.(*parser.NameNode); !ok {
				continue
			}

			duplicateIsOk := primaryIsTypeParam

			// The original's comment: it's OK if they both have the same declared
			// type.
			var primaryType, otherType Type
			if info := c.evaluator.GetTypeForDeclaration(primaryDecl); info != nil {
				primaryType = info.Type
			}
			if info := c.evaluator.GetTypeForDeclaration(otherDecl); info != nil {
				otherType = info.Type
			}
			if primaryType != nil && otherType != nil &&
				IsTypeSame(primaryType, otherType, TypeSameOptions{}, 0) {
				duplicateIsOk = true
			}

			if duplicateIsOk {
				continue
			}

			diag := c.evaluator.AddDiagnostic(DiagnosticRuleReportRedeclaration,
				localization.LocMessage.ObscuredVariableDeclaration().Format(name),
				d.Node, nil)
			addPrimaryDeclInfo(diag)

		case *TypeAliasDeclaration:
			var nameNode parser.ParseNode
			if aliasNode, ok := d.Node.(*parser.TypeAliasNode); ok {
				nameNode = aliasNode.D.Name
			} else {
				nameNode = d.Node
			}
			diag := c.evaluator.AddDiagnostic(DiagnosticRuleReportRedeclaration,
				localization.LocMessage.ObscuredTypeAliasDeclaration().Format(name),
				nameNode, nil)
			addPrimaryDeclInfo(diag)
		}
	}
}

// declObscuresFunction is the original's filter over otherDecls when the primary
// declaration is a function: keep only declarations that genuinely obscure it.
//
// The property case is the subtle one. A property's setter and deleter are
// separate `def`s with the same name, so they must not be reported as
// redeclarations -- but a genuine duplicate must be. They are told apart by
// typeSourceId: the setter's decorated type is a clone of the getter's property
// object and carries the same id, so an equal id means "same property".
func (c *Checker) declObscuresFunction(
	decl Declaration, primaryDeclTypeInfo *FunctionTypeResult,
) bool {
	funcDecl, ok := decl.(*FunctionDeclaration)
	if !ok {
		return true
	}

	funcTypeInfo := c.evaluator.GetTypeOfFunction(checkerFunctionNode(funcDecl))
	if funcTypeInfo == nil {
		return true
	}

	var decoratedType Type
	if primaryDeclTypeInfo != nil {
		decoratedType = c.evaluator.MakeTopLevelTypeVarsConcrete(primaryDeclTypeInfo.DecoratedType, false)
	}

	if decoratedType != nil && IsClassInstance(decoratedType) &&
		ClassTypeIsPropertyClass(decoratedType.(*ClassType)) &&
		IsClassInstance(funcTypeInfo.DecoratedType) &&
		ClassTypeIsPropertyClass(funcTypeInfo.DecoratedType.(*ClassType)) {
		return funcTypeInfo.DecoratedType.(*ClassType).Shared.TypeSourceID !=
			decoratedType.(*ClassType).Shared.TypeSourceID
	}

	return !FunctionTypeIsOverloaded(funcTypeInfo.FunctionType)
}

// primaryDeclAddendum is the original's chain selecting the "see ... declaration"
// addendum for the declaration kind.
func primaryDeclAddendum(primaryDecl Declaration) string {
	switch d := primaryDecl.(type) {
	case *FunctionDeclaration:
		if d.IsMethod {
			return localization.LocAddendum.SeeMethodDeclaration()
		}
		return localization.LocAddendum.SeeFunctionDeclaration()
	case *ClassDeclaration:
		return localization.LocAddendum.SeeClassDeclaration()
	case *ParamDeclaration:
		return localization.LocAddendum.SeeParameterDeclaration()
	case *VariableDeclaration:
		return localization.LocAddendum.SeeVariableDeclaration()
	case *TypeAliasDeclaration:
		return localization.LocAddendum.SeeTypeAliasDeclaration()
	}
	return localization.LocAddendum.SeeDeclaration()
}
