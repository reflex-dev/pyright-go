/*
 * typeevaluator_symbols.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * lookUpSymbolRecursive and isFlowPathBetweenNodes.
 *
 * Third on the frontier after reachability, and portable in full: name
 * resolution is a walk over the scope chain the binder built, filtered by the
 * reachability walk that landed in codeflowengine_reachability.go. No type
 * evaluation is involved, which is why it can be finished rather than stubbed.
 *
 * What it does *not* do is answer what a name's type is. That is
 * getEffectiveTypeOfSymbol, and it needs declaration resolution, which needs
 * class and function creation. This resolves which symbol a name refers to;
 * saying what that symbol is remains the wall.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// LookUpSymbolRecursive corresponds to lookUpSymbolRecursive. The original's
// fourth parameter, preferGlobalScope, defaults to false and is not part of the
// TypeEvaluator interface; it is exposed as a separate method below for the
// internal callers that pass it.
func (e *typeEvaluator) LookUpSymbolRecursive(node parser.ParseNode, name string, honorCodeFlow bool) *SymbolWithScope {
	return e.lookUpSymbolRecursive(node, name, honorCodeFlow, false)
}

func (e *typeEvaluator) lookUpSymbolRecursive(
	node parser.ParseNode,
	name string,
	honorCodeFlow bool,
	preferGlobalScope bool,
) *SymbolWithScope {
	scopeNodeInfo := GetEvaluationScopeNode(node)
	scope := GetScope(scopeNodeInfo.Node)

	var symbolWithScope *SymbolWithScope
	if scope != nil {
		symbolWithScope = scope.LookUpSymbolRecursive(name, &LookupSymbolOptions{
			UseProxyScope:               scopeNodeInfo.UseProxyScope,
			UseChainedModuleLevelScopes: scopeNodeInfo.UseChainedModuleLevelScopes,
		})
	}

	// `scope?.type ?? ScopeType.Module`.
	scopeType := ScopeTypeModule
	if scope != nil {
		scopeType = scope.Type
	}

	// The original's comment: functions and list comprehensions don't allow
	// access to implicitly aliased symbols in outer scopes if they haven't yet
	// been assigned within the local scope.
	scopeTypeHonorsCodeFlow := scopeType != ScopeTypeFunction && scopeType != ScopeTypeComprehension

	// Type parameter scopes don't honor code flow.
	if symbolWithScope != nil && symbolWithScope.Scope.Type == ScopeTypeTypeParameter {
		scopeTypeHonorsCodeFlow = false
	}

	if symbolWithScope != nil && honorCodeFlow && scopeTypeHonorsCodeFlow {
		symbolWithScope = e.filterByReachableDecl(node, name, symbolWithScope)
	}

	// The original's comment: PEP 563 indicates that if a forward reference can
	// be resolved in the module scope (or, by implication, in the builtins
	// scope), it should prefer that resolution over local resolutions.
	if symbolWithScope != nil && preferGlobalScope {
		curSymbolWithScope := symbolWithScope
		for curSymbolWithScope.Scope.Type != ScopeTypeModule &&
			curSymbolWithScope.Scope.Type != ScopeTypeBuiltin &&
			curSymbolWithScope.Scope.Type != ScopeTypeTypeParameter &&
			curSymbolWithScope.Scope.Parent != nil {
			next := curSymbolWithScope.Scope.Parent.LookUpSymbolRecursive(name, &LookupSymbolOptions{
				IsOutsideCallerModule: curSymbolWithScope.IsOutsideCallerModule,
				IsBeyondExecutionScope: curSymbolWithScope.IsBeyondExecutionScope ||
					curSymbolWithScope.Scope.IsIndependentlyExecutable(),
			})
			if next == nil {
				// The original assigns undefined and then breaks, so the loop
				// variable is left holding undefined and the check below fails.
				curSymbolWithScope = nil
				break
			}
			curSymbolWithScope = next
		}

		if curSymbolWithScope != nil &&
			(curSymbolWithScope.Scope.Type == ScopeTypeModule || curSymbolWithScope.Scope.Type == ScopeTypeBuiltin) {
			symbolWithScope = curSymbolWithScope
		}
	}

	return symbolWithScope
}

// filterByReachableDecl is the honorCodeFlow branch of lookUpSymbolRecursive:
// keep the symbol only if one of its declarations is reachable from the usage,
// and otherwise resume the search in an outer scope.
func (e *typeEvaluator) filterByReachableDecl(
	node parser.ParseNode,
	name string,
	symbolWithScope *SymbolWithScope,
) *SymbolWithScope {
	// `find` returns the first match; only its existence is used.
	hasReachableDecl := false
	for _, decl := range symbolWithScope.Symbol.GetDeclarations() {
		if e.isDeclReachableFromUsage(node, name, decl) {
			hasReachableDecl = true
			break
		}
	}

	if hasReachableDecl {
		return symbolWithScope
	}

	// The original's comment: if none of the declarations are reachable from
	// the current node, search for the symbol in outer scopes.
	if symbolWithScope.Scope.Type == ScopeTypeFunction {
		return nil
	}

	nextScopeToSearch := symbolWithScope.Scope.Parent
	isOutsideCallerModule := symbolWithScope.IsOutsideCallerModule ||
		symbolWithScope.Scope.Type == ScopeTypeModule
	isBeyondExecutionScope := symbolWithScope.IsBeyondExecutionScope ||
		symbolWithScope.Scope.IsIndependentlyExecutable()

	if symbolWithScope.Scope.Type == ScopeTypeClass {
		// The original's comment: there is an odd documented behavior for
		// classes in that symbol resolution skips to the global scope rather
		// than the next scope in the chain.
		globalScopeResult := symbolWithScope.Scope.GetGlobalScope()
		nextScopeToSearch = globalScopeResult.Scope
		if globalScopeResult.IsBeyondExecutionScope {
			isBeyondExecutionScope = true
		}
	}

	if nextScopeToSearch == nil {
		return nil
	}

	return nextScopeToSearch.LookUpSymbolRecursive(name, &LookupSymbolOptions{
		IsOutsideCallerModule:  isOutsideCallerModule,
		IsBeyondExecutionScope: isBeyondExecutionScope,
	})
}

// isDeclReachableFromUsage is the body of the original's `find` callback, which
// answers true for anything it does not rule out.
func (e *typeEvaluator) isDeclReachableFromUsage(node parser.ParseNode, name string, decl Declaration) bool {
	declType := decl.DeclBase().Type
	if declType == DeclarationTypeAlias || declType == DeclarationTypeIntrinsic {
		return true
	}

	// Determine if the declaration is in the same execution scope as the
	// "usageNode" node.
	var usageScopeNode parser.ParseNode = GetExecutionScopeNode(node)

	// For a class, function or type alias the original uses the *name* node
	// rather than the declaration node, because the declaration node's own
	// scope is the one it introduces.
	declNode := decl.DeclBase().Node
	switch declType {
	case DeclarationTypeClass, DeclarationTypeFunction, DeclarationTypeTypeAlias:
		if named := declarationNameNode(decl); named != nil {
			declNode = named
		}
	}
	declScopeNode := GetExecutionScopeNode(declNode)

	// The original's comment: if this is a type parameter scope, it will be a
	// proxy for its containing scope, so we need to use that instead.
	usageScope := scopeOfNode(usageScopeNode)
	if usageScope != nil && usageScope.Proxy != nil {
		// The original re-reads the same scope here rather than the proxy,
		// which makes the second lookup identical to the first; preserved
		// because the symbol-table check that follows is what has an effect.
		typeParamScope := scopeOfNode(usageScopeNode)
		if (typeParamScope == nil || !typeParamScope.SymbolTable.Has(name)) &&
			usageScopeNode.NodeBase().Parent != nil {
			usageScopeNode = GetExecutionScopeNode(usageScopeNode.NodeBase().Parent)
		}
	}

	if usageScopeNode == parser.ParseNode(declScopeNode) {
		if !e.isFlowPathBetweenNodes(declNode, node, true) {
			// The original's comment: if there was no control flow path from
			// the usage back to the source, see if the usage node is reachable
			// by any path.
			flowNode := GetFlowNode(node)
			isReachable := flowNode != nil &&
				e.codeFlowReachability.GetFlowNodeReachability(e, flowNode, nil, true) == ReachabilityReachable
			return !isReachable
		}
	}

	return true
}

// isFlowPathBetweenNodes corresponds to the function of the same name.
func (e *typeEvaluator) isFlowPathBetweenNodes(sourceNode parser.ParseNode, sinkNode parser.ParseNode, allowSelf bool) bool {
	if e.checkCodeFlowTooComplex(sourceNode) {
		return true
	}

	sourceFlowNode := GetFlowNode(sourceNode)
	sinkFlowNode := GetFlowNode(sinkNode)
	if sourceFlowNode == nil || sinkFlowNode == nil {
		return false
	}
	if sourceFlowNode == sinkFlowNode {
		return allowSelf
	}

	return e.codeFlowReachability.GetFlowNodeReachability(e, sinkFlowNode, sourceFlowNode, true) == ReachabilityReachable
}

// scopeOfNode is `AnalyzerNodeInfo.getScope(node)` for a node that is not
// statically known to carry a scope. The TypeScript reads the field off any
// node and gets undefined when it is absent; Go needs the assertion checked
// rather than assumed, because an unchecked one would panic mid-corpus.
func scopeOfNode(node parser.ParseNode) *Scope {
	scoped, ok := node.(ScopedNode)
	if !ok {
		return nil
	}
	return GetScope(scoped)
}

// declarationNameNode reads `decl.node.d.name` for the three declaration forms
// the original narrows to before doing so.
func declarationNameNode(decl Declaration) parser.ParseNode {
	switch node := decl.DeclBase().Node.(type) {
	case *parser.ClassNode:
		return node.D.Name
	case *parser.FunctionNode:
		return node.D.Name
	case *parser.TypeAliasNode:
		return node.D.Name
	}
	return nil
}
