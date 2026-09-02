/*
 * typeevaluator_incontext.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * evaluateTypesForExpressionInContext.
 *
 * Given any expression, this finds the smallest enclosing construct that can be
 * evaluated without further context and evaluates that; getType then reads the
 * type back out of the cache. It is a dispatch rather than an evaluator -- every
 * arm hands off to a getTypeOf* or evaluateTypesFor* that lives elsewhere -- so
 * porting it decomposes one frontier entry into the twenty behind it.
 *
 * The walk itself is faithful, including the parts that look redundant. Two are
 * worth naming because a reader will otherwise assume they are transliteration
 * slips:
 *
 *   - The string-enclosure check runs on `parent`, not on `nodeToEvaluate`, so a
 *     node already inside a StringList walks to the same StringList again on
 *     the next iteration and the loop relies on the assignment at the bottom to
 *     make progress.
 *   - The Index arm sets flags but does not break, unlike the Call and
 *     MemberAccess arm directly above it, so the walk continues past the base
 *     expression of an index.
 *
 * Both change which node ends up evaluated, so both are preserved.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// evaluateTypesForExpressionInContext corresponds to the function of the same
// name.
func (e *typeEvaluator) evaluateTypesForExpressionInContext(node parser.ExpressionNode) {
	// The original's comment: check for a couple of special cases where the
	// node is a NameNode but is technically not part of an expression. We'll
	// handle these here so callers don't need to include special-case logic.
	if nameNode, ok := node.(*parser.NameNode); ok && nameNode.NodeBase().Parent != nil {
		if e.evaluateNameInNonExpressionContext(nameNode) {
			return
		}
	}

	// The original's comment: if the expression is part of a type annotation,
	// we need to evaluate it with special evaluation flags.
	if annotationNode := GetParentAnnotationNode(node); annotationNode != nil {
		e.evaluateAnnotationInContext(annotationNode)
		return
	}

	// The original's comment: see if the expression is part of a pattern used
	// in a case statement.
	if possibleCaseNode := GetParentNodeOfType(node, parser.ParseNodeTypeCase); possibleCaseNode != nil {
		caseNode := possibleCaseNode.(*parser.CaseNode)
		if IsNodeContainedWithin(node, caseNode.D.Pattern) {
			e.EvaluateTypesForCaseStatement(caseNode)
			return
		}
	}

	nodeToEvaluate, flags, handled := e.walkToEvaluableNode(node)
	if handled {
		return
	}

	e.evaluateResolvedNode(node, nodeToEvaluate, flags)
}

// evaluateNameInNonExpressionContext is the original's first block. It answers
// whether the name was handled here.
func (e *typeEvaluator) evaluateNameInNonExpressionContext(node *parser.NameNode) bool {
	parent := node.NodeBase().Parent

	switch p := parent.(type) {
	case *parser.FunctionNode:
		if p.D.Name == node {
			e.GetTypeOfFunction(p)
			return true
		}
	case *parser.ClassNode:
		if p.D.Name == node {
			e.GetTypeOfClass(p)
			return true
		}
	case *parser.ImportFromAsNode:
		e.evaluateTypesForImportFromAs(p)
		return true
	case *parser.ImportAsNode:
		e.evaluateTypesForImportAs(p)
		return true
	case *parser.TypeAliasNode:
		if p.D.Name == node {
			e.getTypeOfTypeAlias(p)
			return true
		}
	}

	switch parent.GetNodeType() {
	case parser.ParseNodeTypeGlobal, parser.ParseNodeTypeNonlocal:
		// The original's comment: for global and nonlocal statements, allow
		// forward references so we don't use code flow during symbol lookups.
		e.getTypeOfExpression(node, EvalFlagsForwardRefs, nil)
		return true
	case parser.ParseNodeTypeModuleName:
		// The original's comment: a name within a module name isn't an
		// expression, so there's nothing we can evaluate here.
		return true
	}

	return false
}

// evaluateAnnotationInContext is the original's annotation block. The original
// asserts that the annotation node has a parent; a nil parent here would be a
// binder bug, and falling through to the general case would evaluate the wrong
// node, so it is recorded rather than ignored.
func (e *typeEvaluator) evaluateAnnotationInContext(annotationNode parser.ExpressionNode) {
	annotationParent := annotationNode.NodeBase().Parent
	if annotationParent == nil {
		e.unported("evaluateTypesForExpressionInContext.annotationWithoutParent")
		return
	}

	if assignment, ok := annotationParent.(*parser.AssignmentNode); ok {
		if parser.ParseNode(annotationNode) == parser.ParseNode(assignment.D.AnnotationComment) {
			e.GetTypeOfAnnotation(annotationNode, &ExpectedTypeOptions{
				VarTypeAnnotation: true,
				AllowFinal:        e.isFinalAllowedForAssignmentTarget(assignment.D.LeftExpr),
				AllowClassVar:     e.isClassVarAllowedForAssignmentTarget(assignment.D.LeftExpr),
			})
		} else {
			e.evaluateTypesForAssignmentStatement(assignment)
		}
		return
	}

	if typeAnnotation, ok := annotationParent.(*parser.TypeAnnotationNode); ok {
		e.evaluateTypesForTypeAnnotationNode(typeAnnotation)
		return
	}

	if fn, ok := annotationParent.(*parser.FunctionNode); ok {
		if parser.ParseNode(annotationNode) == parser.ParseNode(fn.D.ReturnAnnotation) {
			e.GetTypeOfAnnotation(annotationNode, &ExpectedTypeOptions{TypeVarGetsCurScope: true})
			return
		}
	}

	options := &ExpectedTypeOptions{}
	if annotationNode.NodeBase().Parent != nil {
		options.VarTypeAnnotation = annotationNode.NodeBase().Parent.GetNodeType() == parser.ParseNodeTypeTypeAnnotation
	}
	if param, ok := annotationParent.(*parser.ParameterNode); ok {
		options.AllowUnpackedTuple = param.D.Category == parser.ParamCategoryArgsList
		options.AllowUnpackedTypedDict = param.D.Category == parser.ParamCategoryKwargsDict
	}
	e.GetTypeOfAnnotation(annotationNode, options)
}

// walkToEvaluableNode is the original's `while (true)` loop: scan up the parse
// tree until we find a node that doesn't require any context to be evaluated.
// The third result reports that an arm of the loop already did the evaluation
// and returned.
func (e *typeEvaluator) walkToEvaluableNode(node parser.ExpressionNode) (parser.ExpressionNode, EvalFlags, bool) {
	nodeToEvaluate := node
	flags := EvalFlagsNone

	for {
		// The original's comment: if we're within an argument node in a call or
		// index expression, skip all of the nodes between because the entire
		// argument expression needs to be evaluated contextually.
		argumentNode := GetParentNodeOfType(nodeToEvaluate, parser.ParseNodeTypeArgument)
		if argumentNode != nil && argumentNode != parser.ParseNode(nodeToEvaluate) {
			argParent := argumentNode.NodeBase().Parent
			if argParent != nil {
				switch argParent.GetNodeType() {
				case parser.ParseNodeTypeCall, parser.ParseNodeTypeIndex:
					nodeToEvaluate = argParent.(parser.ExpressionNode)
					continue
				case parser.ParseNodeTypeClass:
					// The original's comment: if this is an argument node
					// within a class declaration, evaluate the full class
					// declaration node.
					e.GetTypeOfClass(argParent.(*parser.ClassNode))
					return nodeToEvaluate, flags, true
				}
			}
		}

		parent := nodeToEvaluate.NodeBase().Parent
		if parent == nil {
			break
		}

		// The original's comment: if this is the target of an assignment
		// expression, evaluate the assignment expression node instead.
		if assignExpr, ok := parent.(*parser.AssignmentExpressionNode); ok && parser.ParseNode(nodeToEvaluate) == parser.ParseNode(assignExpr.D.Name) {
			nodeToEvaluate = assignExpr
			continue
		}

		// The original's comment: forward-declared type annotation expressions
		// need to be evaluated in context so they have the appropriate flags
		// set. Most of these cases will have been detected above when calling
		// getParentAnnotationNode, but TypeAlias expressions are not handled
		// there.
		//
		// Note this searches from `parent`, not from nodeToEvaluate.
		if stringEnclosure := GetParentNodeOfType(parent, parser.ParseNodeTypeStringList); stringEnclosure != nil {
			nodeToEvaluate = stringEnclosure.(*parser.StringListNode)
			continue
		}

		// The original's comment: the left expression of a call or member
		// access expression is not generally contextual.
		if parent.GetNodeType() == parser.ParseNodeTypeCall || parent.GetNodeType() == parser.ParseNodeTypeMemberAccess {
			if parser.ParseNode(nodeToEvaluate) == parser.ParseNode(leftExprOf(parent)) {
				// The original's comment: handle the special case where the LHS
				// is a call to super().
				if call, ok := nodeToEvaluate.(*parser.CallNode); ok {
					if name, ok := call.D.LeftExpr.(*parser.NameNode); ok && name.D.Value == "super" {
						nodeToEvaluate = parent.(parser.ExpressionNode)
						continue
					}
				}

				// The original's comment: handle the special case where the LHS
				// is a call to a lambda.
				if parent.GetNodeType() == parser.ParseNodeTypeCall &&
					nodeToEvaluate.GetNodeType() == parser.ParseNodeTypeLambda {
					nodeToEvaluate = parent.(parser.ExpressionNode)
					continue
				}

				flags = EvalFlagsCallBaseDefaults
				break
			}
		} else if parent.GetNodeType() == parser.ParseNodeTypeIndex {
			// The original's comment: the base expression of an index
			// expression is not contextual. Note that unlike the arm above,
			// this one does not break.
			if parser.ParseNode(nodeToEvaluate) == parser.ParseNode(leftExprOf(parent)) {
				flags = EvalFlagsIndexBaseDefaults
			}
		}

		if !parser.IsExpressionNode(parent) {
			// The original's comment: if we've hit a non-expression node, we
			// generally want to stop. However, there are a few special "pass
			// through" node types that we can skip over to get to a known
			// expression node.
			switch parent.GetNodeType() {
			case parser.ParseNodeTypeDictionaryKeyEntry,
				parser.ParseNodeTypeDictionaryExpandEntry,
				parser.ParseNodeTypeComprehensionFor,
				parser.ParseNodeTypeComprehensionIf:
				parent = parent.NodeBase().Parent
			case parser.ParseNodeTypeParameter:
				// Parameters are contextual for lambdas.
				if parent.NodeBase().Parent != nil &&
					parent.NodeBase().Parent.GetNodeType() == parser.ParseNodeTypeLambda {
					parent = parent.NodeBase().Parent
				} else {
					return nodeToEvaluate, flags, false
				}
			case parser.ParseNodeTypeTypeParameter:
				// The original's comment: if this is a bound or default
				// expression in a type parameter list, we need to evaluate it
				// in the context of the type parameter.
				//
				// Note the comparison is against the *original* node, not
				// nodeToEvaluate.
				typeParam := parent.(*parser.TypeParameterNode)
				if parser.ParseNode(node) == parser.ParseNode(typeParam.D.BoundExpr) ||
					parser.ParseNode(node) == parser.ParseNode(typeParam.D.DefaultExpr) {
					e.getTypeOfTypeParam(typeParam)
					return nodeToEvaluate, flags, true
				}
				return nodeToEvaluate, flags, false
			default:
				return nodeToEvaluate, flags, false
			}
		}

		if parent == nil || !parser.IsExpressionNode(parent) {
			// A pass-through arm above can leave parent non-expression or nil,
			// which the original's assertions rule out; treat it as the end of
			// the walk rather than crashing on the assignment below.
			return nodeToEvaluate, flags, false
		}

		nodeToEvaluate = parent.(parser.ExpressionNode)
	}

	return nodeToEvaluate, flags, false
}

// evaluateResolvedNode is the original's final switch on the parent of whatever
// the walk settled on.
func (e *typeEvaluator) evaluateResolvedNode(node parser.ExpressionNode, nodeToEvaluate parser.ExpressionNode, flags EvalFlags) {
	parent := nodeToEvaluate.NodeBase().Parent
	if parent == nil {
		// The original asserts here.
		e.unported("evaluateTypesForExpressionInContext.noParent")
		return
	}

	switch p := parent.(type) {
	case *parser.DelNode:
		e.VerifyDeleteExpression(nodeToEvaluate)
		return

	case *parser.TypeParameterNode:
		// The original's comment: if this is the name node within a type
		// parameter list, see if it's a type alias definition. If so, we need
		// to evaluate the type alias contextually.
		if parser.ParseNode(nodeToEvaluate) == parser.ParseNode(p.D.Name) &&
			p.NodeBase().Parent != nil &&
			p.NodeBase().Parent.GetNodeType() == parser.ParseNodeTypeTypeParameterList &&
			p.NodeBase().Parent.NodeBase().Parent != nil &&
			p.NodeBase().Parent.NodeBase().Parent.GetNodeType() == parser.ParseNodeTypeTypeAlias {
			e.getTypeOfTypeAlias(p.NodeBase().Parent.NodeBase().Parent.(*parser.TypeAliasNode))
			return
		}

	case *parser.TypeAliasNode:
		e.getTypeOfTypeAlias(p)
		return

	case *parser.DecoratorNode:
		if p.NodeBase().Parent != nil {
			switch grand := p.NodeBase().Parent.(type) {
			case *parser.ClassNode:
				e.GetTypeOfClass(grand)
			case *parser.FunctionNode:
				e.GetTypeOfFunction(grand)
			}
		}
		return

	case *parser.ParameterNode:
		if parser.ParseNode(nodeToEvaluate) != parser.ParseNode(p.D.DefaultValue) {
			e.EvaluateTypeOfParam(p)
			return
		}

	case *parser.ArgumentNode:
		if parser.ParseNode(nodeToEvaluate) == parser.ParseNode(p.D.Name) {
			// The original's comment: a name used to specify a named parameter
			// in an argument isn't an expression, so there's nothing we can
			// evaluate here.
			return
		}

		if p.NodeBase().Parent != nil {
			if classNode, ok := p.NodeBase().Parent.(*parser.ClassNode); ok {
				// The original's comment: a class argument must be evaluated in
				// the context of the class declaration.
				e.GetTypeOfClass(classNode)
				return
			}
		}

	case *parser.ReturnNode:
		// The original's comment: return expressions must be evaluated in the
		// context of the expected return type.
		if p.D.Expr != nil {
			var declaredReturnType Type
			if enclosingFunctionNode := GetEnclosingFunction(node); enclosingFunctionNode != nil {
				declaredReturnType = e.GetDeclaredReturnType(enclosingFunctionNode)
			}
			if declaredReturnType != nil {
				liveScopeIds := GetTypeVarScopesForNode(node)
				declaredReturnType = MakeTypeVarsBound(declaredReturnType, liveScopeIds, true)
			}
			e.getTypeOfExpression(p.D.Expr, EvalFlagsNone, makeInferenceContext(declaredReturnType))
			return
		}

	case *parser.TypeAnnotationNode:
		e.evaluateTypesForTypeAnnotationNode(p)
		return

	case *parser.AssignmentNode:
		e.evaluateTypesForAssignmentStatement(p)
		return

	case *parser.AugmentedAssignmentNode:
		e.evaluateTypesForAugmentedAssignment(p)
		return
	}

	if typeAnnotation, ok := nodeToEvaluate.(*parser.TypeAnnotationNode); ok {
		e.evaluateTypesForTypeAnnotationNode(typeAnnotation)
		return
	}

	e.getTypeOfExpression(nodeToEvaluate, flags, nil)
}

// leftExprOf reads `d.leftExpr` from the three node kinds the walk asks it of.
func leftExprOf(node parser.ParseNode) parser.ExpressionNode {
	switch n := node.(type) {
	case *parser.CallNode:
		return n.D.LeftExpr
	case *parser.MemberAccessNode:
		return n.D.LeftExpr
	case *parser.IndexNode:
		return n.D.LeftExpr
	}
	return nil
}

/*
 * The dispatch targets.
 *
 * Each of these is a distinct piece of typeEvaluator.ts that this walk hands
 * off to. They are separate functions rather than one shared stub so that the
 * frontier names them individually: what the corpus needs is the ranking among
 * them, not the fact that the walk reached something unported.
 */

func (e *typeEvaluator) evaluateTypesForImportFromAs(_ *parser.ImportFromAsNode) {
	e.unported("evaluateTypesForImportFromAs")
}

func (e *typeEvaluator) evaluateTypesForImportAs(_ *parser.ImportAsNode) {
	e.unported("evaluateTypesForImportAs")
}

func (e *typeEvaluator) evaluateTypesForAugmentedAssignment(_ *parser.AugmentedAssignmentNode) {
	e.unported("evaluateTypesForAugmentedAssignment")
}

// isClassVarAllowedForAssignmentTarget corresponds to the evaluator-local
// function of the same name, which shadows no parseTreeUtils counterpart.
func (e *typeEvaluator) isClassVarAllowedForAssignmentTarget(targetNode parser.ExpressionNode) bool {
	// The original's comment: ClassVar is allowed only in a class body.
	classNode := GetEnclosingClass(targetNode, true)
	if classNode == nil {
		return false
	}

	// The original's comment: ClassVar is not allowed in a TypedDict or a
	// NamedTuple class.
	return !e.isInTypedDictOrNamedTuple(classNode)
}

// isFinalAllowedForAssignmentTarget corresponds to the evaluator-local function
// of the same name, which shadows the parseTreeUtils one it delegates to.
func (e *typeEvaluator) isFinalAllowedForAssignmentTarget(targetNode parser.ExpressionNode) bool {
	classNode := GetEnclosingClass(targetNode, true)

	// The original's comment: Final is not allowed in the body of a TypedDict
	// or NamedTuple class.
	if classNode != nil && e.isInTypedDictOrNamedTuple(classNode) {
		return false
	}

	return IsFinalAllowedForAssignmentTarget(targetNode)
}

// isInTypedDictOrNamedTuple corresponds to the function of the same name.
func (e *typeEvaluator) isInTypedDictOrNamedTuple(classNode *parser.ClassNode) bool {
	classTypeInfo := e.GetTypeOfClass(classNode)
	if classTypeInfo == nil || classTypeInfo.ClassType == nil {
		return false
	}

	classType := classTypeInfo.ClassType
	return ClassTypeIsTypedDictClass(classType) || classType.Shared.NamedTupleEntries != nil
}

// makeInferenceContext corresponds to the function of the same name in
// typeUtils.ts, which returns undefined for an absent expected type.
func makeInferenceContext(expectedType Type) *InferenceContext {
	if expectedType == nil {
		return nil
	}
	return &InferenceContext{ExpectedType: expectedType}
}
