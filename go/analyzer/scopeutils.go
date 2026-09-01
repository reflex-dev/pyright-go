/*
 * scopeutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Static utility methods related to scopes and their related symbol tables.
 *
 * Transliterated from analyzer/scopeUtils.ts (pyright 1.1.412).
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// GetBuiltInScope corresponds to getBuiltInScope.
func GetBuiltInScope(currentScope *Scope) *Scope {
	// Starting at the current scope, find the built-in scope, which should be
	// the top-most parent.
	builtInScope := currentScope

	for builtInScope.Type != ScopeTypeBuiltin {
		builtInScope = builtInScope.Parent
	}

	return builtInScope
}

// GetScopeForNode locates the evaluation scope associated with the specified
// parse node.
func GetScopeForNode(node parser.ParseNode) *Scope {
	scopeNode := GetEvaluationScopeNode(node).Node
	return GetScope(scopeNode)
}

// GetScopeHierarchy returns a list of scopes associated with the node and its
// ancestor nodes. If stopScope is provided, the search stops at that scope and
// nil is returned if it is not found. The TypeScript leaves stopScope
// undefined; pass nil for that.
func GetScopeHierarchy(node parser.ParseNode, stopScope *Scope) []*Scope {
	scopeHierarchy := []*Scope{}
	curNode := node

	for curNode != nil {
		scopeNode := GetEvaluationScopeNode(curNode).Node
		curScope := GetScope(scopeNode)

		if curScope == nil {
			return nil
		}

		if len(scopeHierarchy) == 0 || scopeHierarchy[len(scopeHierarchy)-1] != curScope {
			scopeHierarchy = append(scopeHierarchy, curScope)
		}

		if curScope == stopScope {
			return scopeHierarchy
		}

		curNode = scopeNode.NodeBase().Parent
	}

	if stopScope != nil {
		return nil
	}
	return scopeHierarchy
}

// FindTopNodeInScope walks up the parse tree from the specified node to find
// the top-most node that is within the specified scope.
func FindTopNodeInScope(node parser.ParseNode, scope *Scope) parser.ParseNode {
	curNode := node
	var prevNode parser.ParseNode
	foundScope := false

	for curNode != nil {
		if GetScope(curNode) == scope {
			foundScope = true
		} else if foundScope {
			return prevNode
		}

		prevNode = curNode
		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// IsScopeContainedWithin corresponds to isScopeContainedWithin.
func IsScopeContainedWithin(scope *Scope, potentialParentScope *Scope) bool {
	curScope := scope

	for curScope != nil {
		if curScope.Parent == potentialParentScope {
			return true
		}

		curScope = curScope.Parent
	}

	return false
}
