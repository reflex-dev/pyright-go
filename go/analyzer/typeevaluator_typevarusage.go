/*
 * typeevaluator_typevarusage.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * validateTypeVarUsage and enforceClassTypeVarScope.
 *
 * What happens to `T` between the TypeVar object and the type a use of it
 * denotes. Three transformations, in order, and each is about the difference
 * between a TypeVar as a value and a TypeVar as a type variable:
 *
 *   - A scope. A bare `TypeVar("T")` has none; the scope is decided by where the
 *     name is USED -- the generic function or class it appears in -- so it is
 *     assigned on first reference rather than at creation.
 *   - Bound versus free. Inside the body of the function that owns T, T stands
 *     for one specific unknown type and is bound. Referring to a T owned by an
 *     enclosing scope from a nested one makes it bound there too, which is what
 *     the enclosing-suite containment test decides. The class exception exists
 *     because a class's own suite is where its type parameters are still free.
 *   - Packed versus unpacked. A TypeVarTuple's bare name refers to the packed
 *     form; `*Ts` is the unpacked one, and everywhere else it repacks.
 *
 * enforceClassTypeVarScope is a separate rule with its own message. A class
 * variable annotated with a TypeVar belonging to a method rather than to the
 * class has no meaning -- there is nothing to solve it against at instance level
 * -- so it is reported and the type becomes Unknown.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateTypeVarUsage corresponds to the function of the same name.
func (e *typeEvaluator) validateTypeVarUsage(
	node parser.ExpressionNode, t Type, flags EvalFlags,
) Type {
	typeVar, ok := t.(*TypeVarType)
	if !ok {
		return t
	}

	if !t.Base().IsInstantiable() || IsTypeAliasPlaceholder(t) {
		return t
	}

	// The original's comment: if the TypeVar doesn't have a scope ID, try to
	// assign one.
	if typeVar.Priv.ScopeID == "" {
		typeVar = e.assignTypeVarScopeID(node, typeVar, flags)
	}

	// The original's comment: if this is a free type var, see if we need to make
	// it into a bound type var.
	if typeVar.Priv.ScopeID != "" && !TypeVarTypeIsBound(typeVar) {
		typeVar = e.bindTypeVarIfFromOuterScope(node, typeVar)
	}

	// The original's comment: if this is a TypeVarTuple, the name refers to the
	// packed form. It must be unpacked in most contexts.
	if IsUnpackedTypeVarTuple(typeVar) {
		typeVar = TypeVarTypeCloneForPacked(typeVar)
	}

	if (flags&EvalFlagsEnforceClassTypeVarScope) != 0 && !e.enforceClassTypeVarScope(node, typeVar) {
		return UnknownTypeCreate(false)
	}

	return typeVar
}

// bindTypeVarIfFromOuterScope is the original's "if this is a reference to a
// TypeVar defined in an outer scope, mark it as bound" block.
//
// The class exception: within a class's OWN suite the type parameters are still
// free, because that is where they are being declared. Any other containment --
// a method body, a nested function -- means the TypeVar arrived from outside and
// is bound to whatever solved it there.
func (e *typeEvaluator) bindTypeVarIfFromOuterScope(
	node parser.ExpressionNode, typeVar *TypeVarType,
) *TypeVarType {
	scopedNode := e.findScopedTypeVarScopeNode(node, typeVar)
	if scopedNode == nil {
		return typeVar
	}

	enclosingSuite := GetEnclosingClassOrFunctionSuite(node)
	if enclosingSuite == nil || !IsNodeContainedWithin(enclosingSuite, scopedNode) {
		return typeVar
	}

	if classNode, ok := scopedNode.(*parser.ClassNode); ok && classNode.D.Suite == enclosingSuite {
		return typeVar
	}

	return TypeVarTypeCloneAsBound(typeVar)
}

// enforceClassTypeVarScope corresponds to the function of the same name.
func (e *typeEvaluator) enforceClassTypeVarScope(node parser.ExpressionNode, typeVar *TypeVarType) bool {
	// The free form carries the scope when the bound form has been rewritten, so
	// it is consulted first.
	scopeID := typeVar.Priv.ScopeID
	if typeVar.Priv.FreeTypeVar != nil && typeVar.Priv.FreeTypeVar.Priv.ScopeID != "" {
		scopeID = typeVar.Priv.FreeTypeVar.Priv.ScopeID
	}

	if scopeID == "" {
		return true
	}

	enclosingClass := GetEnclosingClass(node, false)
	if enclosingClass == nil {
		return true
	}

	for _, liveScopeID := range GetTypeVarScopesForNode(enclosingClass) {
		if liveScopeID == scopeID {
			return true
		}
	}

	e.AddDiagnostic(
		DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.TypeVarInvalidForMemberVariable().Format(
			TypeVarTypeGetReadableName(typeVar, false)),
		node,
		nil,
	)

	return false
}

/*
 * The two scope-resolution helpers this reaches.
 */

// assignTypeVarScopeID corresponds to assignTypeVarScopeId, which walks outward
// from the use looking for the generic function, class or type alias whose
// parameter list the TypeVar belongs to.
func (e *typeEvaluator) assignTypeVarScopeID(
	_ parser.ExpressionNode, typeVar *TypeVarType, _ EvalFlags,
) *TypeVarType {
	e.unported("assignTypeVarScopeId")
	return typeVar
}

// findScopedTypeVarScopeNode corresponds to `findScopedTypeVar(node, type)
// ?.scopeNode`. The original returns a record; only the scope node is read here.
func (e *typeEvaluator) findScopedTypeVarScopeNode(
	_ parser.ExpressionNode, _ *TypeVarType,
) parser.ParseNode {
	e.unported("findScopedTypeVar")
	return nil
}
