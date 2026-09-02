/*
 * typeevaluator_typevarscope.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * assignTypeVarScopeId and findScopedTypeVar.
 *
 * Deciding which generic construct a TypeVar belongs to. A legacy
 * `T = TypeVar("T")` is a module-level object with no owner; it acquires one by
 * being used in a function or class signature, and two different functions using
 * the same T get two independently-solved copies. That is what a scope ID is:
 * the identity of the construct that will solve it.
 *
 * findScopedTypeVar answers "who already owns this name". It walks outward
 * through enclosing type-parameter scopes, asking each class and function for
 * its own type parameter list and matching by NAME rather than by identity --
 * the T in a signature has been cloned with a scope, so it is no longer the same
 * object as the module-level one. Failing that, it looks for an enclosing type
 * alias assignment, since a recursive alias owns its parameters too.
 *
 * assignTypeVarScopeId then decides what to do with that answer, and the three
 * branches are three different questions:
 *
 *   - NoTypeVarWithScopeId: the TypeVar must NOT already be owned. This is a
 *     declaration site, and reusing an outer scope's T there is the classic
 *     error. PEP 695 syntax is exempt, since its parameters are declared where
 *     they are used.
 *   - TypeVarGetsCurScope: the TypeVar takes THIS scope. An intervening class
 *     between the use and the owner makes that impossible.
 *   - Otherwise: the TypeVar must ALREADY be owned, and an unowned one in a type
 *     expression is reported.
 *
 * foundInterveningClass carries the one case a scope ID alone cannot express. In
 *
 *     class Outer(Generic[T]):
 *         class Inner:
 *             x: T
 *
 * T is owned by Outer, but Inner is not generic in T and has no way to solve it,
 * so the reference is invalid despite the scope being found. PEP 695 syntax does
 * not have this problem, which is why the flag is suppressed there.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// scopedTypeVarResult corresponds to the ScopedTypeVarResult interface.
type scopedTypeVarResult struct {
	Type                  *TypeVarType
	ScopeNode             parser.ParseNode
	FoundInterveningClass bool
}

// assignTypeVarScopeID corresponds to assignTypeVarScopeId.
func (e *typeEvaluator) assignTypeVarScopeID(
	node parser.ExpressionNode, typeVar *TypeVarType, flags EvalFlags,
) *TypeVarType {
	scopedTypeVarInfo := e.findScopedTypeVar(node, typeVar)
	typeVar = scopedTypeVarInfo.Type

	if (flags&EvalFlagsNoTypeVarWithScopeId) != 0 && typeVar.Priv.ScopeID != "" {
		return e.reportTypeVarAlreadyScoped(node, typeVar)
	}

	if (flags & EvalFlagsTypeVarGetsCurScope) != 0 {
		return e.giveTypeVarCurrentScope(node, typeVar, scopedTypeVarInfo)
	}

	if (flags & EvalFlagsAllowTypeVarWithoutScopeId) == 0 {
		if typeVar.Priv.ScopeID != "" && !scopedTypeVarInfo.FoundInterveningClass {
			return typeVar
		}

		if !typeVar.Shared.IsSynthesized && (flags&EvalFlagsInstantiableType) != 0 {
			message := localization.LocMessage.TypeVarNotUsedByOuterScope().Format(typeVar.Shared.Name)
			if IsParamSpec(typeVar) {
				message = localization.LocMessage.ParamSpecNotUsedByOuterScope().Format(typeVar.Shared.Name)
			}
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, message, node, nil)
		}
	}

	return typeVar
}

// reportTypeVarAlreadyScoped is the NoTypeVarWithScopeId branch: this is a
// declaration site, and the TypeVar already belongs to an outer scope.
func (e *typeEvaluator) reportTypeVarAlreadyScoped(
	node parser.ExpressionNode, typeVar *TypeVarType,
) *TypeVarType {
	if typeVar.Shared.IsSynthesized || IsParamSpec(typeVar) {
		return typeVar
	}

	// The original's comment: this TypeVar already has a scope ID assigned to it.
	// See if it originates from type parameter syntax. If so, allow it.
	if typeVar.Shared.IsTypeParamSyntax {
		return typeVar
	}

	// The original's comment: if this type variable expression is used within a
	// generic class, function, or type alias that uses type parameter syntax,
	// there is no need to report an error here.
	if typeVarScopeNode := GetTypeVarScopeNode(node); typeVarScopeNode != nil {
		if typeParams := typeParamListOf(typeVarScopeNode); typeParams != nil {
			isOwnDeclaration := false
			for _, param := range typeParams.D.Params {
				if parser.ParseNode(param.D.Name) == parser.ParseNode(node) {
					isOwnDeclaration = true
					break
				}
			}
			if !isOwnDeclaration {
				return typeVar
			}
		}
	}

	e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.TypeVarUsedByOuterScope().Format(typeVar.Shared.Name), node, nil)

	return typeVar
}

// giveTypeVarCurrentScope is the TypeVarGetsCurScope branch.
func (e *typeEvaluator) giveTypeVarCurrentScope(
	node parser.ExpressionNode, typeVar *TypeVarType, scopedTypeVarInfo *scopedTypeVarResult,
) *TypeVarType {
	if typeVar.Priv.ScopeID != "" {
		return typeVar
	}

	if scopedTypeVarInfo.FoundInterveningClass {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeVarUsedByOuterScope().Format(typeVar.Shared.Name), node, nil)
		return typeVar
	}

	enclosingScope := GetEnclosingClassOrFunction(node)

	// The original's comment: handle P.args and P.kwargs as a special case for
	// inner functions.
	//
	// `P.args` inside a nested function refers to the OUTER function's ParamSpec,
	// because the inner function is the one being parameterized by it.
	if enclosingScope != nil {
		if memberAccess, ok := node.NodeBase().Parent.(*parser.MemberAccessNode); ok &&
			parser.ParseNode(memberAccess.D.LeftExpr) == parser.ParseNode(node) {
			memberName := memberAccess.D.Member.D.Value
			if memberName == "args" || memberName == "kwargs" {
				outerFunctionScope := GetEnclosingClassOrFunction(enclosingScope)

				if outerFunctionScope != nil &&
					outerFunctionScope.GetNodeType() == parser.ParseNodeTypeFunction {
					enclosingScope = outerFunctionScope
				} else if scopedTypeVarInfo.Type.Priv.ScopeID == "" {
					e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
						localization.LocMessage.ParamSpecNotUsedByOuterScope().Format(typeVar.Shared.Name),
						node, nil)
				}
			}
		}
	}

	if enclosingScope == nil {
		// The original calls fail() here.
		common.Fail("AssociateTypeVarsWithCurrentScope flag was set but enclosing scope not found")
		return typeVar
	}

	scopeName, scopeTypeIsFunction := enclosingScopeNameAndKind(enclosingScope)

	// The original's comment: if the enclosing scope is using type parameter
	// syntax, traditional type variables can't be used in this context.
	if typeParams := typeParamListOf(enclosingScope); typeParams != nil {
		declared := false
		for _, param := range typeParams.D.Params {
			if param.D.Name.D.Value == typeVar.Shared.Name {
				declared = true
				break
			}
		}
		if !declared {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeParameterNotDeclared().Format(typeVar.Shared.Name, scopeName),
				node, nil)
		}
	}

	scopeIdToAssign := GetScopeIdForNode(enclosingScope)
	scopeType := TypeVarScopeTypeClass
	if scopeTypeIsFunction {
		scopeType = TypeVarScopeTypeFunction
	}

	return TypeVarTypeCloneForScopeID(typeVar, scopeIdToAssign, &scopeName, &scopeType)
}

// findScopedTypeVar corresponds to the function of the same name.
func (e *typeEvaluator) findScopedTypeVar(
	node parser.ExpressionNode, typeVar *TypeVarType,
) *scopedTypeVarResult {
	if result := e.findScopedTypeVarInEnclosingScopes(node, typeVar); result != nil {
		return result
	}

	// The original's comment: see if this is part of an assignment statement that
	// is defining a type alias.
	if result := e.findScopedTypeVarInTypeAlias(node, typeVar); result != nil {
		return result
	}

	// The original's comment: return the original type.
	return &scopedTypeVarResult{Type: typeVar}
}

// findScopedTypeVarInEnclosingScopes is the first loop: walk outward through
// type-parameter scopes looking for one whose parameter list names this TypeVar.
func (e *typeEvaluator) findScopedTypeVarInEnclosingScopes(
	node parser.ExpressionNode, typeVar *TypeVarType,
) *scopedTypeVarResult {
	var curNode parser.ParseNode = node
	nestedClassCount := 0

	for curNode != nil {
		scopeNode := GetTypeVarScopeNode(curNode)
		if scopeNode == nil {
			return nil
		}
		curNode = scopeNode

		var typeParamsForScope []*TypeVarType
		scopeUsesTypeParamSyntax := false

		switch scopeTyped := curNode.(type) {
		case *parser.ClassNode:
			if classTypeInfo := e.GetTypeOfClass(scopeTyped); classTypeInfo != nil &&
				!ClassTypeIsPartiallyEvaluated(classTypeInfo.ClassType) {
				typeParamsForScope = classTypeInfo.ClassType.Shared.TypeParams
			}
			scopeUsesTypeParamSyntax = scopeTyped.D.TypeParams != nil
			nestedClassCount++

		case *parser.FunctionNode:
			if functionType := e.getTypeOfFunctionPredecorated(scopeTyped); functionType != nil {
				typeParamsForScope = functionType.Shared.TypeParams
			}
			scopeUsesTypeParamSyntax = scopeTyped.D.TypeParams != nil

		case *parser.TypeAliasNode:
			scopeUsesTypeParamSyntax = scopeTyped.D.TypeParams != nil
		}

		for _, candidate := range typeParamsForScope {
			if candidate.Shared.Name != typeVar.Shared.Name {
				continue
			}

			if candidate.Priv.ScopeID == "" || candidate.Priv.ScopeName == nil ||
				candidate.Priv.ScopeType == nil {
				continue
			}

			// The original's comment: use the scoped version of the TypeVar rather
			// than the (unscoped) original type.
			scoped := TypeVarTypeCloneForScopeID(typeVar,
				candidate.Priv.ScopeID, candidate.Priv.ScopeName, candidate.Priv.ScopeType)
			scoped.Shared.DeclaredVariance = candidate.Shared.DeclaredVariance

			return &scopedTypeVarResult{
				Type:      scoped,
				ScopeNode: scopeNode,
				// See the file header: a class between the use and the owner cannot
				// solve the TypeVar, and only legacy syntax can produce that shape.
				FoundInterveningClass: nestedClassCount > 1 && !scopeUsesTypeParamSyntax,
			}
		}

		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// findScopedTypeVarInTypeAlias is the second loop: a recursive type alias owns
// its own type parameters, and the placeholder written to the cache while it is
// being resolved is how they are found.
func (e *typeEvaluator) findScopedTypeVarInTypeAlias(
	node parser.ExpressionNode, typeVar *TypeVarType,
) *scopedTypeVarResult {
	var curNode parser.ParseNode = node

	for curNode != nil {
		var leftType Type
		var typeAliasNode *parser.TypeAliasNode
		var scopeNode parser.ParseNode

		switch stmt := curNode.(type) {
		case *parser.TypeAliasNode:
			leftType = e.readTypeCache(stmt.D.Name, evalFlagsNonePtr())
			typeAliasNode = stmt
			scopeNode = stmt

		case *parser.AssignmentNode:
			leftType = e.readTypeCache(stmt.D.LeftExpr, evalFlagsNonePtr())
			scopeNode = stmt
		}

		if leftType != nil && scopeNode != nil {
			// The original's comment: is this a placeholder that was temporarily
			// written to the cache for purposes of resolving type aliases?
			if leftTypeVar, ok := leftType.(*TypeVarType); ok && leftTypeVar.Shared.RecursiveAlias != nil {
				return e.scopeTypeVarToRecursiveAlias(node, typeVar, leftTypeVar, typeAliasNode, scopeNode)
			}
		}

		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// scopeTypeVarToRecursiveAlias is the body of that match.
func (e *typeEvaluator) scopeTypeVarToRecursiveAlias(
	node parser.ExpressionNode,
	typeVar *TypeVarType,
	leftTypeVar *TypeVarType,
	typeAliasNode *parser.TypeAliasNode,
	scopeNode parser.ParseNode,
) *scopedTypeVarResult {
	recursiveAlias := leftTypeVar.Shared.RecursiveAlias

	props := typeVar.Base().Props
	hasAliasInfo := props != nil && props.TypeAliasInfo != nil

	if typeAliasNode != nil && !typeVar.Shared.IsTypeParamSyntax && !hasAliasInfo {
		// The original's comment: type alias statements cannot be used with
		// old-style type variables.
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeParameterNotDeclared().Format(
				typeVar.Shared.Name, typeAliasNode.D.Name.D.Value),
			node, nil)
	} else if recursiveAlias.TypeParams != nil {
		// The original's comment: if this is a TypeAliasType call, the recursive
		// type parameters will already be populated, and we need to verify that the
		// type parameter is in the list of allowed type parameters.
		allowed := false
		for _, param := range recursiveAlias.TypeParams {
			if param.Shared.Name == typeVar.Shared.Name {
				allowed = true
				break
			}
		}
		if !allowed {
			// The original's comment: return the original type.
			return &scopedTypeVarResult{Type: typeVar, ScopeNode: scopeNode}
		}
	}

	scopeType := TypeVarScopeTypeTypeAlias
	return &scopedTypeVarResult{
		Type: TypeVarTypeCloneForScopeID(typeVar,
			recursiveAlias.TypeVarScopeId, &recursiveAlias.Name, &scopeType),
		ScopeNode: scopeNode,
	}
}

/*
 * Two shapes the original reads off a union type that Go has to switch on.
 */

// typeParamListOf reads `.d.typeParams` from any TypeParameterScopeNode.
func typeParamListOf(node parser.ParseNode) *parser.TypeParameterListNode {
	switch typed := node.(type) {
	case *parser.ClassNode:
		return typed.D.TypeParams
	case *parser.FunctionNode:
		return typed.D.TypeParams
	case *parser.TypeAliasNode:
		return typed.D.TypeParams
	}
	return nil
}

// enclosingScopeNameAndKind reads `.d.name.d.value` and the node type from the
// class-or-function union getEnclosingClassOrFunction returns.
func enclosingScopeNameAndKind(node parser.ParseNode) (string, bool) {
	switch typed := node.(type) {
	case *parser.ClassNode:
		return typed.D.Name.D.Value, false
	case *parser.FunctionNode:
		return typed.D.Name.D.Value, true
	}
	return "", false
}
