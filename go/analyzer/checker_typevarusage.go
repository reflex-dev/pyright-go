/*
 * checker_typevarusage.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateFunctionTypeVarUsage. The original's comment: verifies that each
 * local type variable is used more than once.
 *
 * A TypeVar that appears exactly once in a signature is almost always a
 * mistake -- it constrains nothing, and the author probably meant `object` or
 * the TypeVar's bound. Deciding that requires counting appearances across the
 * whole signature, which is why this walks the annotations with a NameNodeWalker
 * and accumulates into a map rather than inspecting the function type.
 *
 * The exemptions are where the real content is. A constrained TypeVar, one with
 * an explicit default, and a ParamSpec all have legitimate single uses. So does
 * a bound TypeVar used as a *type argument* -- but only in a parameter, not in
 * the return type, which is what exemptBoundTypeVar tracks as the walk moves
 * from parameters to the return annotation. And a type argument to a generic
 * type alias is exempt because the alias itself may repeat the TypeVar.
 *
 * The second check is subtler and looks for a TypeVar that can never be solved:
 * one that appears in the return type but, among parameters, only in ones
 * defaulted to `...`. Such a parameter is never supplied in a stub, so the
 * TypeVar has no source. A single top-level TypeVar in a return union is
 * exempted, because an unsolved member of a union can be dropped without
 * producing Unknown.
 *
 * A generic class's own TypeVars get the same unsolvable check when they appear
 * in __init__, which is what constructorClass and the second map are for.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// typeVarUsageInfo corresponds to the TypeVarUsageInfo interface.
type typeVarUsageInfo struct {
	Nodes                           []*parser.NameNode
	TypeVar                         *TypeVarType
	ParamTypeUsageCount             int
	ParamTypeWithEllipsisUsageCount int
	ReturnTypeUsageCount            int
	ParamWithEllipsis               string
	IsExempt                        bool
}

// validateFunctionTypeVarUsage corresponds to _validateFunctionTypeVarUsage.
func (c *Checker) validateFunctionTypeVarUsage(
	node *parser.FunctionNode, functionTypeResult *FunctionTypeResult,
) {
	// The original's comment: skip this check entirely if it's disabled.
	if c.fileInfo.DiagnosticRuleSet.ReportInvalidTypeVarUse == DiagnosticLevelNone {
		return
	}

	t := functionTypeResult.FunctionType
	localTypeVarUsage := common.NewOrderedMap[string, *typeVarUsageInfo]()
	classTypeVarUsage := common.NewOrderedMap[string, *typeVarUsageInfo]()
	exemptBoundTypeVar := true
	var curParamNode *parser.ParameterNode

	// The original's comment: is this a constructor (an __init__ method) for a
	// generic class?
	var constructorClass *ClassType
	if FunctionTypeIsInstanceMethod(t) && node.D.Name.D.Value == "__init__" {
		if classNode, ok := GetEnclosingClassOrFunction(node).(*parser.ClassNode); ok {
			if classType := c.evaluator.GetTypeOfClass(classNode); classType != nil &&
				IsClass(classType.ClassType) {
				constructorClass = classType.ClassType
			}
		}
	}

	nameWalker := NewNameNodeWalker(func(
		nameNode *parser.NameNode, subscriptIndex *int, baseExpression parser.ExpressionNode,
	) {
		nameType := c.evaluator.GetType(nameNode)
		if nameType == nil || !IsTypeVar(nameType) || TypeVarTypeIsSelf(nameType.(*TypeVarType)) {
			return
		}
		typeVar := nameType.(*TypeVarType)

		isParamTypeWithEllipsisUsage := curParamNode != nil &&
			curParamNode.D.DefaultValue != nil &&
			curParamNode.D.DefaultValue.GetNodeType() == parser.ParseNodeTypeEllipsis

		paramName := ""
		if curParamNode != nil && curParamNode.D.Name != nil {
			paramName = curParamNode.D.Name.D.Value
		}

		// The original's comment: does this name refer to a TypeVar that is scoped
		// to this function?
		if typeVar.Priv.ScopeID == GetScopeIdForNode(node) {
			// The original's comment: we exempt constrained TypeVars, TypeVars that
			// are type arguments of other types, and ParamSpecs. There are
			// legitimate uses for singleton instances in these particular cases.
			isExempt := TypeVarTypeHasConstraints(typeVar) ||
				typeVar.Shared.IsDefaultExplicit ||
				(exemptBoundTypeVar && subscriptIndex != nil) ||
				IsParamSpec(typeVar)

			if !isExempt && baseExpression != nil && subscriptIndex != nil {
				// The original's comment: is this a type argument for a generic type
				// alias? If so, exempt it from the check because the type alias may
				// repeat the TypeVar multiple times.
				baseType := c.evaluator.GetType(baseExpression)
				if aliasInfo := propsTypeAliasInfo(baseType); aliasInfo != nil &&
					aliasInfo.Shared != nil &&
					*subscriptIndex < len(aliasInfo.Shared.TypeParams) {
					isExempt = true
				}
			}

			c.recordTypeVarUsage(localTypeVarUsage, typeVar, nameNode, curParamNode != nil,
				curParamNode == nil, isParamTypeWithEllipsisUsage, paramName, isExempt)
		}

		// The original's comment: does this name refer to a TypeVar that is scoped
		// to the class associated with this constructor method?
		if constructorClass != nil && typeVar.Priv.ScopeID == constructorClass.Shared.TypeVarScopeID {
			// The class map never counts return-type usage, so its
			// returnTypeUsageCount stays zero however the TypeVar was reached.
			c.recordTypeVarUsage(classTypeVarUsage, typeVar, nameNode, curParamNode != nil,
				false, isParamTypeWithEllipsisUsage, paramName, typeVar.Shared.IsDefaultExplicit)
		}
	})

	// The original's comment: find all of the local type variables in signature.
	for _, param := range node.D.Params {
		annotation := param.D.Annotation
		if annotation == nil {
			annotation = param.D.AnnotationComment
		}
		if annotation != nil {
			curParamNode = param
			nameWalker.Walk(annotation)
		}
	}
	curParamNode = nil

	if node.D.ReturnAnnotation != nil {
		// The original's comment: don't exempt the use of a bound TypeVar when
		// used as a type argument within a return type. This exemption applies
		// only to input parameter annotations.
		exemptBoundTypeVar = false
		nameWalker.Walk(node.D.ReturnAnnotation)
	}

	if node.D.FuncAnnotationComment != nil {
		for _, expr := range node.D.FuncAnnotationComment.D.ParamAnnotations {
			nameWalker.Walk(expr)
		}

		if node.D.FuncAnnotationComment.D.ReturnAnnotation != nil {
			exemptBoundTypeVar = false
			nameWalker.Walk(node.D.FuncAnnotationComment.D.ReturnAnnotation)
		}
	}

	// The original's comment: skip this check if the function is overloaded
	// because the TypeVar will be solved in terms of the overload signatures.
	skipUnsolvableTypeVarCheck := IsOverloaded(functionTypeResult.DecoratedType) &&
		!FunctionTypeIsOverloaded(functionTypeResult.FunctionType)

	for _, name := range localTypeVarUsage.Keys() {
		usage, _ := localTypeVarUsage.Get(name)
		c.reportTypeVarUsedOnlyOnce(usage)
		c.reportLocalTypeVarUnsolvable(usage, t, skipUnsolvableTypeVarCheck)
	}

	// The original's comment: report error for a class type variable that appears
	// only within constructor parameters that have default values. These may go
	// unsolved.
	for _, name := range classTypeVarUsage.Keys() {
		usage, _ := classTypeVarUsage.Get(name)
		if usage.ParamTypeWithEllipsisUsageCount > 0 &&
			usage.ParamTypeUsageCount == usage.ParamTypeWithEllipsisUsageCount &&
			!usage.IsExempt {
			c.reportTypeVarPossiblyUnsolvable(usage)
		}
	}
}

// recordTypeVarUsage is the original's repeated create-or-update of a usage
// entry, written once for each of the two maps.
func (c *Checker) recordTypeVarUsage(
	usageMap *common.OrderedMap[string, *typeVarUsageInfo],
	typeVar *TypeVarType,
	nameNode *parser.NameNode,
	inParam bool,
	inReturn bool,
	isParamTypeWithEllipsisUsage bool,
	paramName string,
	isExempt bool,
) {
	existing, ok := usageMap.Get(typeVar.Shared.Name)
	if !ok {
		entry := &typeVarUsageInfo{
			Nodes:    []*parser.NameNode{nameNode},
			TypeVar:  typeVar,
			IsExempt: isExempt,
		}
		if inParam {
			entry.ParamTypeUsageCount = 1
		}
		if isParamTypeWithEllipsisUsage {
			entry.ParamTypeWithEllipsisUsageCount = 1
			entry.ParamWithEllipsis = paramName
		}
		if inReturn {
			entry.ReturnTypeUsageCount = 1
		}
		usageMap.Set(typeVar.Shared.Name, entry)
		return
	}

	existing.Nodes = append(existing.Nodes, nameNode)

	if !inParam {
		if inReturn {
			existing.ReturnTypeUsageCount++
		}
		return
	}

	existing.ParamTypeUsageCount++
	if isParamTypeWithEllipsisUsage {
		existing.ParamTypeWithEllipsisUsageCount++
		if existing.ParamWithEllipsis == "" {
			existing.ParamWithEllipsis = paramName
		}
	}
}

// reportTypeVarUsedOnlyOnce is the original's comment: report error for local
// type variable that appears only once.
func (c *Checker) reportTypeVarUsedOnlyOnce(usage *typeVarUsageInfo) {
	if len(usage.Nodes) != 1 || usage.IsExempt {
		return
	}

	altTypeText := `"object"`
	if IsTypeVarTuple(usage.TypeVar) {
		altTypeText = `"tuple[object, ...]"`
	} else if usage.TypeVar.Shared.BoundType != nil {
		altTypeText = `"` + c.evaluator.PrintType(
			ConvertToInstance(usage.TypeVar.Shared.BoundType, false), nil) + `"`
	}

	diag := common.NewDiagnosticAddendum()
	diag.AddMessage(localization.LocAddendum.TypeVarUnnecessarySuggestion().Format(altTypeText))

	c.evaluator.AddDiagnostic(DiagnosticRuleReportInvalidTypeVarUse,
		localization.LocMessage.TypeVarUsedOnlyOnce().Format(usage.Nodes[0].D.Value)+diag.GetString(),
		usage.Nodes[0], nil)
}

// reportLocalTypeVarUnsolvable is the original's comment: report error for local
// type variable that appears in return type (but not as a top-level TypeVar
// within a union) and appears only within parameters that have default values.
// These may go unsolved.
func (c *Checker) reportLocalTypeVarUnsolvable(
	usage *typeVarUsageInfo, t *FunctionType, skipUnsolvableTypeVarCheck bool,
) {
	isUsedInReturnType := usage.ReturnTypeUsageCount > 0

	if usage.ReturnTypeUsageCount == 1 && t.Shared.DeclaredReturnType != nil {
		// The original's comment: if the TypeVar appears only once in the return
		// type and it's a top-level TypeVar within a union, exempt it from this
		// check. Although these TypeVars may go unsolved, they can be safely
		// eliminated from the union without generating an Unknown type.
		returnType := t.Shared.DeclaredReturnType
		if IsUnion(returnType) {
			for _, subtype := range returnType.(*UnionType).Priv.Subtypes {
				if IsTypeVar(subtype) &&
					subtype.(*TypeVarType).Shared.Name == usage.Nodes[0].D.Value {
					isUsedInReturnType = false
					break
				}
			}
		}
	}

	if isUsedInReturnType && usage.ParamTypeWithEllipsisUsageCount > 0 &&
		usage.ParamTypeUsageCount == usage.ParamTypeWithEllipsisUsageCount &&
		!skipUnsolvableTypeVarCheck {
		c.reportTypeVarPossiblyUnsolvable(usage)
	}
}

// reportTypeVarPossiblyUnsolvable is the diagnostic both unsolvable checks emit.
func (c *Checker) reportTypeVarPossiblyUnsolvable(usage *typeVarUsageInfo) {
	diag := common.NewDiagnosticAddendum()
	diag.AddMessage(localization.LocAddendum.TypeVarUnsolvableRemedy())

	c.evaluator.AddDiagnostic(DiagnosticRuleReportInvalidTypeVarUse,
		localization.LocMessage.TypeVarPossiblyUnsolvable().
			Format(usage.Nodes[0].D.Value, usage.ParamWithEllipsis)+diag.GetString(),
		usage.Nodes[0], nil)
}

// propsTypeAliasInfo reads type.props?.typeAliasInfo, guarding the two nil hops
// the original spells with optional chaining.
func propsTypeAliasInfo(t Type) *TypeAliasInfo {
	if t == nil {
		return nil
	}
	props := t.Base().Props
	if props == nil {
		return nil
	}
	return props.TypeAliasInfo
}
