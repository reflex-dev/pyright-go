/*
 * parsetreeutils_nav.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Parse tree navigation from analyzer/parseTreeUtils.ts (pyright 1.1.412),
 * lines 66-188 and 577-1236: node lookup by position, the getEnclosingX
 * family, the evaluation and execution scope walks, and the containment
 * predicates.
 *
 * The TypeScript returns discriminated node unions (ClassNode | ModuleNode);
 * those become the corresponding Go union interfaces from parsenodes_unions.go,
 * or a concrete pointer where the union has one arm.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// EvaluationScopeInfo corresponds to the interface of the same name.
type EvaluationScopeInfo struct {
	Node parser.EvaluationScopeNode

	UseProxyScope               bool
	UseChainedModuleLevelScopes bool
}

// GetNodeDepth returns the depth of the node as measured from the root of the
// parse tree.
func GetNodeDepth(node parser.ParseNode) int {
	depth := 0
	curNode := node

	for curNode != nil {
		depth++
		curNode = curNode.NodeBase().Parent
	}

	return depth
}

// FindNodeByPosition returns the deepest node that contains the specified
// position.
func FindNodeByPosition(
	node parser.ParseNode,
	position common.Position,
	lines *common.TextRangeCollection[common.TextRange],
) parser.ParseNode {
	offset, ok := common.ConvertPositionToOffset(position, lines)
	if !ok {
		return nil
	}

	return FindNodeByOffset(node, offset)
}

// FindNodeByOffset returns the deepest node that contains the specified offset.
func FindNodeByOffset(node parser.ParseNode, offset int) parser.ParseNode {
	if !nodeRange(node).Overlaps(offset) {
		return nil
	}

	// The range is found within this node. See if we can localize it further by
	// checking its children.
	children := GetChildNodes(node)
	if IsCompliantWithNodeRangeRules(node) && len(children) > 20 {
		// Use binary search to find the child to visit. This should be helpful
		// when there are many siblings, such as statements in a module/suite or
		// expressions in a list, etc. Otherwise, we will have to traverse every
		// sibling before finding the correct one.
		index := common.GetIndexContaining(children, offset, func(item parser.ParseNode, position int) bool {
			if item == nil {
				return false
			}
			return nodeRange(item).Overlaps(position)
		})

		if index >= 0 {
			// Find first sibling that overlaps with the offset. This ensures
			// that our binary search result matches what we would have returned
			// via a linear search.
			searchIndex := index - 1
			for searchIndex >= 0 {
				previousChild := children[searchIndex]
				if previousChild != nil {
					if nodeRange(previousChild).Overlaps(offset) {
						index = searchIndex
					} else {
						break
					}
				}

				searchIndex--
			}

			children = []parser.ParseNode{children[index]}
		}
	}

	for _, child := range children {
		if child == nil {
			continue
		}

		containingChild := FindNodeByOffset(child, offset)
		if containingChild != nil {
			// For augmented assignments, prefer the dest expression, which is a
			// clone of the left expression but is used to hold the type of the
			// operation result.
			if aug, ok := node.(*parser.AugmentedAssignmentNode); ok {
				if parser.ParseNode(aug.D.LeftExpr) == containingChild {
					return aug.D.DestExpr
				}
			}

			return containingChild
		}
	}

	return node
}

// IsCompliantWithNodeRangeRules corresponds to the function of the same name.
func IsCompliantWithNodeRangeRules(node parser.ParseNode) bool {
	// ParseNode range rules are
	// 1. Children are all contained within the parent.
	// 2. Children have non-overlapping ranges.
	// 3. Children are listed in increasing order.
	nodeType := node.GetNodeType()
	return nodeType != parser.ParseNodeTypeAssignment && nodeType != parser.ParseNodeTypeStringList
}

// GetClassFullName corresponds to getClassFullName.
func GetClassFullName(classNode parser.ParseNode, moduleName string, className string) string {
	nameParts := []string{className}

	curNode := classNode

	// Walk the parse tree looking for classes.
	for curNode != nil {
		enclosing := GetEnclosingClass(curNode, false)
		if enclosing == nil {
			curNode = nil
			break
		}
		curNode = enclosing
		nameParts = append(nameParts, enclosing.D.Name.D.Value)
	}

	nameParts = append(nameParts, moduleName)

	// reverse().join('.')
	for i, j := 0, len(nameParts)-1; i < j; i, j = i+1, j-1 {
		nameParts[i], nameParts[j] = nameParts[j], nameParts[i]
	}

	result := ""
	for i, part := range nameParts {
		if i > 0 {
			result += "."
		}
		result += part
	}
	return result
}

// GetCallForName corresponds to getCallForName.
func GetCallForName(node *parser.NameNode) *parser.CallNode {
	parent := node.NodeBase().Parent

	if call, ok := parent.(*parser.CallNode); ok && parser.ParseNode(call.D.LeftExpr) == parser.ParseNode(node) {
		return call
	}

	if memberAccess, ok := parent.(*parser.MemberAccessNode); ok && memberAccess.D.Member == node {
		if call, ok := memberAccess.NodeBase().Parent.(*parser.CallNode); ok &&
			parser.ParseNode(call.D.LeftExpr) == parser.ParseNode(memberAccess) {
			return call
		}
	}

	return nil
}

// GetDecoratorForName corresponds to getDecoratorForName.
func GetDecoratorForName(node *parser.NameNode) *parser.DecoratorNode {
	parent := node.NodeBase().Parent

	if decorator, ok := parent.(*parser.DecoratorNode); ok &&
		parser.ParseNode(decorator.D.Expr) == parser.ParseNode(node) {
		return decorator
	}

	if memberAccess, ok := parent.(*parser.MemberAccessNode); ok && memberAccess.D.Member == node {
		if decorator, ok := memberAccess.NodeBase().Parent.(*parser.DecoratorNode); ok &&
			parser.ParseNode(decorator.D.Expr) == parser.ParseNode(memberAccess) {
			return decorator
		}
	}

	return nil
}

// GetEnclosingSuite corresponds to getEnclosingSuite.
func GetEnclosingSuite(node parser.ParseNode) *parser.SuiteNode {
	curNode := node.NodeBase().Parent

	for curNode != nil {
		if suite, ok := curNode.(*parser.SuiteNode); ok {
			return suite
		}
		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// GetEnclosingClass corresponds to getEnclosingClass. The TypeScript defaults
// stopAtFunction to false.
func GetEnclosingClass(node parser.ParseNode, stopAtFunction bool) *parser.ClassNode {
	curNode := node.NodeBase().Parent
	for curNode != nil {
		switch typed := curNode.(type) {
		case *parser.ClassNode:
			return typed
		case *parser.ModuleNode:
			return nil
		case *parser.FunctionNode:
			if stopAtFunction {
				return nil
			}
		}

		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// GetEnclosingModule corresponds to getEnclosingModule.
func GetEnclosingModule(node parser.ParseNode) *parser.ModuleNode {
	curNode := node.NodeBase().Parent
	for curNode != nil {
		if module, ok := curNode.(*parser.ModuleNode); ok {
			return module
		}

		curNode = curNode.NodeBase().Parent
	}

	common.Fail("Module node not found")
	return nil
}

// GetEnclosingClassOrModule corresponds to getEnclosingClassOrModule. The
// TypeScript defaults stopAtFunction to false.
//
// The TypeScript return type is ClassNode | ModuleNode | undefined; there is no
// union interface covering exactly those two, so this returns ParseNode and the
// callers narrow.
func GetEnclosingClassOrModule(node parser.ParseNode, stopAtFunction bool) parser.ParseNode {
	curNode := node.NodeBase().Parent
	for curNode != nil {
		switch curNode.(type) {
		case *parser.ClassNode:
			return curNode
		case *parser.ModuleNode:
			return curNode
		case *parser.FunctionNode:
			if stopAtFunction {
				return nil
			}
		}

		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// GetEnclosingFunction corresponds to getEnclosingFunction.
func GetEnclosingFunction(node parser.ParseNode) *parser.FunctionNode {
	curNode := node.NodeBase().Parent
	var prevNode parser.ParseNode

	for curNode != nil {
		if function, ok := curNode.(*parser.FunctionNode); ok {
			// Don't treat a decorator as being "enclosed" in the function.
			if !someDecoratorIs(function.D.Decorators, prevNode) {
				return function
			}
		}

		if _, ok := curNode.(*parser.ClassNode); ok {
			return nil
		}

		prevNode = curNode
		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// GetEnclosingFunctionEvaluationScope is similar to GetEnclosingFunction except
// that it uses evaluation scopes rather than the parse tree to determine whether
// the specified node is within the scope. That means if the node is within a
// class decorator (for example), it will be considered part of its parent node
// rather than the class node.
func GetEnclosingFunctionEvaluationScope(node parser.ParseNode) *parser.FunctionNode {
	curNode := GetEvaluationScopeNode(node).Node

	for curNode != nil {
		if function, ok := curNode.(*parser.FunctionNode); ok {
			return function
		}

		if _, ok := curNode.(*parser.ClassNode); ok {
			return nil
		}
		if curNode.NodeBase().Parent == nil {
			return nil
		}

		curNode = GetEvaluationScopeNode(curNode.NodeBase().Parent).Node
	}

	return nil
}

// GetEnclosingLambda corresponds to getEnclosingLambda.
func GetEnclosingLambda(node parser.ParseNode) *parser.LambdaNode {
	curNode := node.NodeBase().Parent
	for curNode != nil {
		if lambda, ok := curNode.(*parser.LambdaNode); ok {
			return lambda
		}

		if _, ok := curNode.(*parser.SuiteNode); ok {
			return nil
		}

		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// GetEnclosingClassOrFunction corresponds to getEnclosingClassOrFunction. The
// TypeScript return type is FunctionNode | ClassNode | undefined.
func GetEnclosingClassOrFunction(node parser.ParseNode) parser.ParseNode {
	curNode := node.NodeBase().Parent
	for curNode != nil {
		switch curNode.(type) {
		case *parser.FunctionNode:
			return curNode
		case *parser.ClassNode:
			return curNode
		}

		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// GetEnclosingClassOrFunctionSuite corresponds to
// getEnclosingClassOrFunctionSuite.
func GetEnclosingClassOrFunctionSuite(node parser.ParseNode) *parser.SuiteNode {
	curNode := node.NodeBase().Parent
	for curNode != nil {
		if suite, ok := curNode.(*parser.SuiteNode); ok {
			switch suite.NodeBase().Parent.(type) {
			case *parser.FunctionNode, *parser.ClassNode:
				return suite
			}
		}

		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// GetEnclosingSuiteOrModule corresponds to getEnclosingSuiteOrModule. The
// TypeScript defaults stopAtFunction to false and stopAtLambda to true. The
// return type is SuiteNode | ModuleNode | undefined.
func GetEnclosingSuiteOrModule(node parser.ParseNode, stopAtFunction bool, stopAtLambda bool) parser.ParseNode {
	curNode := node.NodeBase().Parent
	for curNode != nil {
		switch curNode.(type) {
		case *parser.SuiteNode:
			return curNode
		case *parser.ModuleNode:
			return curNode
		case *parser.LambdaNode:
			if stopAtLambda {
				return nil
			}
		case *parser.FunctionNode:
			if stopAtFunction {
				return nil
			}
		}

		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// GetEvaluationNodeForAssignmentExpression corresponds to the function of the
// same name. The TypeScript return type is
// LambdaNode | FunctionNode | ModuleNode | ClassNode | undefined.
func GetEvaluationNodeForAssignmentExpression(node *parser.AssignmentExpressionNode) parser.ParseNode {
	// PEP 572 indicates that the evaluation node for an assignment expression
	// target within a list comprehension is contained within a lambda, function
	// or module, but not a class.
	sawComprehension := false
	var curNode parser.ParseNode = GetEvaluationScopeNode(node).Node

	for curNode != nil {
		switch curNode.GetNodeType() {
		case parser.ParseNodeTypeFunction, parser.ParseNodeTypeLambda, parser.ParseNodeTypeModule:
			return curNode

		case parser.ParseNodeTypeClass:
			if sawComprehension {
				return nil
			}
			return curNode

		case parser.ParseNodeTypeComprehension:
			sawComprehension = true
			curNode = GetEvaluationScopeNode(curNode.NodeBase().Parent).Node

		default:
			return nil
		}
	}

	return nil
}

// GetEvaluationScopeNode returns the parse node corresponding to the scope that
// is used to evaluate a symbol referenced in the specified node.
func GetEvaluationScopeNode(node parser.ParseNode) EvaluationScopeInfo {
	var prevNode parser.ParseNode
	var prevPrevNode parser.ParseNode
	curNode := node
	isParamNameNode := false
	isParamDefaultNode := false
	useChainedModuleLevelScopes := false

	for curNode != nil {
		if param, ok := curNode.(*parser.ParameterNode); ok {
			if prevNode != nil && sameNode(prevNode, param.D.Name) {
				// Note that we passed through a parameter name node.
				isParamNameNode = true
			} else if prevNode != nil && sameNode(prevNode, param.D.DefaultValue) {
				// Note that we passed through a parameter default value node.
				isParamDefaultNode = true
			}
		}

		// We found a scope associated with this node. In most cases, we'll
		// return this scope, but in a few cases we need to return the enclosing
		// scope instead.
		switch typed := curNode.(type) {
		case *parser.TypeParameterListNode:
			return EvaluationScopeInfo{
				Node:                        typed,
				UseProxyScope:               true,
				UseChainedModuleLevelScopes: useChainedModuleLevelScopes,
			}

		case *parser.FunctionNode:
			if prevNode == nil {
				break
			}

			// Decorators are always evaluated outside of the function scope.
			if someDecoratorIs(typed.D.Decorators, prevNode) {
				break
			}

			// The name of the function is evaluated within the containing scope.
			if sameNode(prevNode, typed.D.Name) {
				break
			}

			if someParamIs(typed.D.Params, prevNode) {
				// Default argument expressions are evaluated outside of the
				// function scope.
				if isParamDefaultNode {
					break
				}

				if isParamNameNode {
					if GetScope(typed) != nil {
						return EvaluationScopeInfo{Node: typed}
					}
				}
			}

			if sameNode(prevNode, typed.D.Suite) {
				if GetScope(typed) != nil {
					return EvaluationScopeInfo{Node: typed, UseChainedModuleLevelScopes: true}
				}
			}

			// All other nodes in the function are evaluated in the context of
			// the type parameter scope if it's present. Otherwise, they are
			// evaluated within the function's parent scope.
			if typed.D.TypeParams != nil {
				scopeNode := typed.D.TypeParams
				if GetScope(scopeNode) != nil {
					return EvaluationScopeInfo{
						Node:                        scopeNode,
						UseProxyScope:               true,
						UseChainedModuleLevelScopes: useChainedModuleLevelScopes,
					}
				}
			}

		case *parser.LambdaNode:
			if someParamIs(typed.D.Params, prevNode) {
				if isParamNameNode {
					if GetScope(typed) != nil {
						return EvaluationScopeInfo{Node: typed}
					}
				}
			} else if prevNode == nil || sameNode(prevNode, typed.D.Expr) {
				if GetScope(typed) != nil {
					return EvaluationScopeInfo{Node: typed, UseChainedModuleLevelScopes: true}
				}
			}

		case *parser.ClassNode:
			if prevNode == nil {
				break
			}

			// Decorators are always evaluated outside of the class scope.
			if someDecoratorIs(typed.D.Decorators, prevNode) {
				break
			}

			if sameNode(prevNode, typed.D.Suite) {
				if GetScope(typed) != nil {
					return EvaluationScopeInfo{Node: typed}
				}
			}

			// Class header expressions (bases, keyword args, type params) are
			// evaluated in the enclosing scope. Enable chained lookup so that
			// earlier-cell globals are visible in those positions.
			useChainedModuleLevelScopes = true

			// All other nodes in the class are evaluated in the context of the
			// type parameter scope if it's present. Otherwise, they are
			// evaluated within the class' parent scope.
			if typed.D.TypeParams != nil {
				scopeNode := typed.D.TypeParams
				if GetScope(scopeNode) != nil {
					return EvaluationScopeInfo{
						Node:                        scopeNode,
						UseProxyScope:               true,
						UseChainedModuleLevelScopes: true,
					}
				}
			}

		case *parser.ComprehensionNode:
			if GetScope(typed) != nil {
				// The iterable expression of the first subnode of a list
				// comprehension is evaluated within the scope of its parent.
				isFirstIterableExpr := false
				if len(typed.D.ForIfNodes) > 0 && sameNode(prevNode, typed.D.ForIfNodes[0]) {
					if forNode, ok := typed.D.ForIfNodes[0].(*parser.ComprehensionForNode); ok {
						isFirstIterableExpr = parser.ParseNode(forNode.D.IterableExpr) == prevPrevNode
					}
				}

				if !isFirstIterableExpr {
					// Only enable chained scopes for comprehensions inside
					// functions/lambdas; module-level comprehensions already see
					// earlier-cell symbols through normal binding.
					return EvaluationScopeInfo{
						Node: typed,
						UseChainedModuleLevelScopes: GetEnclosingFunction(typed) != nil ||
							GetEnclosingLambda(typed) != nil,
					}
				}
			}

		case *parser.TypeAliasNode:
			if sameNode(prevNode, typed.D.Expr) && typed.D.TypeParams != nil {
				scopeNode := typed.D.TypeParams
				if GetScope(scopeNode) != nil {
					return EvaluationScopeInfo{Node: scopeNode}
				}
			}

		case *parser.ModuleNode:
			if GetScope(typed) != nil {
				return EvaluationScopeInfo{Node: typed, UseChainedModuleLevelScopes: useChainedModuleLevelScopes}
			}
		}

		prevPrevNode = prevNode
		prevNode = curNode
		curNode = curNode.NodeBase().Parent
	}

	common.Fail("Did not find evaluation scope")
	return EvaluationScopeInfo{}
}

// GetTypeVarScopeNode returns the parse node corresponding to the function,
// class, or type alias that potentially provides the scope for a type
// parameter.
func GetTypeVarScopeNode(node parser.ParseNode) parser.TypeParameterScopeNode {
	var prevNode parser.ParseNode
	curNode := node

	for curNode != nil {
		switch typed := curNode.(type) {
		case *parser.FunctionNode:
			if !someDecoratorIs(typed.D.Decorators, prevNode) {
				return typed
			}

		case *parser.ClassNode:
			if !someDecoratorIs(typed.D.Decorators, prevNode) {
				return typed
			}

		case *parser.TypeAliasNode:
			return typed
		}

		prevNode = curNode
		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// GetExecutionScopeNode returns the parse node corresponding to the scope that
// is used for executing the code referenced in the specified node.
func GetExecutionScopeNode(node parser.ParseNode) parser.ExecutionScopeNode {
	evaluationScope := GetEvaluationScopeNode(node).Node

	// Classes are not considered execution scope because they are executed
	// within the context of their containing module or function. Likewise, list
	// comprehensions are executed within their container. Type parameter scopes
	// are special because they act as proxies for their containing function or
	// class scope.
	for {
		switch evaluationScope.(type) {
		case *parser.TypeParameterListNode, *parser.ClassNode, *parser.ComprehensionNode:
			evaluationScope = GetEvaluationScopeNode(evaluationScope.NodeBase().Parent).Node
			continue
		}
		break
	}

	executionScope, ok := evaluationScope.(parser.ExecutionScopeNode)
	if !ok {
		common.Fail("Evaluation scope is not an execution scope")
		return nil
	}
	return executionScope
}

// GetTypeAnnotationNode returns, given a node within a type annotation
// expression, the type annotation node that contains it (if applicable).
func GetTypeAnnotationNode(node parser.ParseNode) *parser.TypeAnnotationNode {
	prevNode := node
	curNode := node.NodeBase().Parent

	for curNode != nil {
		if annotation, ok := curNode.(*parser.TypeAnnotationNode); ok {
			if sameNode(prevNode, annotation.D.Annotation) {
				return annotation
			}

			break
		}

		prevNode = curNode
		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// GetArgsByRuntimeOrder returns the call's arguments in the order the runtime
// evaluates them. In general, arguments passed to a call are evaluated
// left-to-right; the exception is an unpacked iterable used after a keyword
// argument.
func GetArgsByRuntimeOrder(node *parser.CallNode) []*parser.ArgumentNode {
	positionalArgs := []*parser.ArgumentNode{}
	keywordArgs := []*parser.ArgumentNode{}

	for _, arg := range node.D.Args {
		if arg.D.Name == nil && arg.D.ArgCategory != parser.ArgCategoryUnpackedDictionary {
			positionalArgs = append(positionalArgs, arg)
		}
		if arg.D.Name != nil || arg.D.ArgCategory == parser.ArgCategoryUnpackedDictionary {
			keywordArgs = append(keywordArgs, arg)
		}
	}

	return append(positionalArgs, keywordArgs...)
}

// IsFinalAllowedForAssignmentTarget corresponds to the function of the same
// name. PEP 591 spells out certain limited cases where an assignment target can
// be annotated with a "Final" annotation.
func IsFinalAllowedForAssignmentTarget(targetNode parser.ExpressionNode) bool {
	// Simple names always support Final.
	if _, ok := targetNode.(*parser.NameNode); ok {
		return true
	}

	// Member access expressions like "self.x" are permitted only within
	// __init__ methods.
	if memberAccess, ok := targetNode.(*parser.MemberAccessNode); ok {
		if _, ok := memberAccess.D.LeftExpr.(*parser.NameNode); !ok {
			return false
		}

		classNode := GetEnclosingClass(memberAccess, false)
		if classNode == nil {
			return false
		}

		methodNode := GetEnclosingFunction(memberAccess)
		if methodNode == nil {
			return false
		}

		if methodNode.D.Name.D.Value != "__init__" {
			return false
		}

		return true
	}

	return false
}

// IsRequiredAllowedForAssignmentTarget corresponds to the function of the same
// name.
func IsRequiredAllowedForAssignmentTarget(targetNode parser.ExpressionNode) bool {
	return GetEnclosingClass(targetNode, true) != nil
}

// IsNodeContainedWithin corresponds to isNodeContainedWithin.
func IsNodeContainedWithin(node parser.ParseNode, potentialContainer parser.ParseNode) bool {
	curNode := node
	for curNode != nil {
		if curNode == potentialContainer {
			return true
		}

		curNode = curNode.NodeBase().Parent
	}

	return false
}

// GetParentNodeOfType corresponds to getParentNodeOfType. The TypeScript
// generic parameter only casts the result, so this returns ParseNode.
func GetParentNodeOfType(node parser.ParseNode, containerType parser.ParseNodeType) parser.ParseNode {
	curNode := node
	for curNode != nil {
		if curNode.GetNodeType() == containerType {
			return curNode
		}

		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// GetParentAnnotationNode returns, if the specified node is contained within an
// expression that is intended to be interpreted as a type annotation, the
// annotation node.
func GetParentAnnotationNode(node parser.ExpressionNode) parser.ExpressionNode {
	var curNode parser.ParseNode = node
	var prevNode parser.ParseNode

	asExpression := func(n parser.ParseNode) parser.ExpressionNode {
		if n == nil {
			return nil
		}
		expr, _ := n.(parser.ExpressionNode)
		return expr
	}

	for curNode != nil {
		switch typed := curNode.(type) {
		case *parser.FunctionNode:
			if sameNode(prevNode, typed.D.ReturnAnnotation) {
				return asExpression(prevNode)
			}
			return nil

		case *parser.ParameterNode:
			if sameNode(prevNode, typed.D.Annotation) {
				return asExpression(prevNode)
			}
			if sameNode(prevNode, typed.D.AnnotationComment) {
				return asExpression(prevNode)
			}
			return nil

		case *parser.AssignmentNode:
			if sameNode(prevNode, typed.D.AnnotationComment) {
				return asExpression(prevNode)
			}
			return nil

		case *parser.TypeAnnotationNode:
			if sameNode(prevNode, typed.D.Annotation) {
				return asExpression(prevNode)
			}
			return nil

		case *parser.FunctionAnnotationNode:
			matches := sameNode(prevNode, typed.D.ReturnAnnotation)
			if !matches {
				for _, p := range typed.D.ParamAnnotations {
					if sameNode(prevNode, p) {
						matches = true
						break
					}
				}
			}
			if matches {
				if prevNode != nil {
					assert(parser.IsExpressionNode(prevNode), "")
				}
				return asExpression(prevNode)
			}
			return nil
		}

		prevNode = curNode
		curNode = curNode.NodeBase().Parent
	}

	return nil
}

// IsNodeContainedWithinNodeType corresponds to isNodeContainedWithinNodeType.
func IsNodeContainedWithinNodeType(node parser.ParseNode, containerType parser.ParseNodeType) bool {
	return GetParentNodeOfType(node, containerType) != nil
}

// IsSuiteEmpty corresponds to isSuiteEmpty.
func IsSuiteEmpty(node *parser.SuiteNode) bool {
	sawEllipsis := false

	for _, statement := range node.D.Statements {
		statementList, ok := statement.(*parser.StatementListNode)
		if !ok {
			return false
		}

		for _, substatement := range statementList.D.Statements {
			switch substatement.(type) {
			case *parser.EllipsisNode:
				// Allow an ellipsis
				sawEllipsis = true
			case *parser.StringListNode:
				// Allow doc strings
			default:
				return false
			}
		}
	}

	return sawEllipsis
}

// nodeRange adapts a parse node to the TextRange the original passes directly
// to TextRange.overlaps.
func nodeRange(node parser.ParseNode) common.TextRange {
	base := node.NodeBase()
	return common.TextRange{Start: base.Start, Length: base.Length}
}

// someDecoratorIs corresponds to `decorators.some((decorator) => decorator === prevNode)`.
func someDecoratorIs(decorators []*parser.DecoratorNode, prevNode parser.ParseNode) bool {
	for _, decorator := range decorators {
		if parser.ParseNode(decorator) == prevNode {
			return true
		}
	}
	return false
}

// someParamIs corresponds to `params.some((param) => param === prevNode)`.
func someParamIs(params []*parser.ParameterNode, prevNode parser.ParseNode) bool {
	for _, param := range params {
		if parser.ParseNode(param) == prevNode {
			return true
		}
	}
	return false
}
