/*
 * typeevaluator_decl.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getDeclaredTypeOfSymbol, getTypeForDeclaration and the three small helpers
 * the parameter case needs.
 *
 * This is the declared half of the effective-type fork. getDeclaredTypeOfSymbol
 * picks which of a symbol's typed declarations to believe -- newest first,
 * skipping any already being resolved, guarded by the symbol resolution stack
 * so a property setter referring to its own getter terminates. getTypeForDeclaration
 * then turns the chosen declaration into a type, which is one switch over the
 * nine declaration kinds.
 *
 * Every arm of that switch is a separate piece of the original, so porting the
 * pair replaces one frontier entry with one per declaration kind. The class and
 * function arms are the two that matter: they are where name resolution meets
 * class and function creation, and they are what the rest of the evaluator is
 * waiting on.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// getDeclaredTypeOfSymbol corresponds to the function of the same name.
func (e *typeEvaluator) getDeclaredTypeOfSymbol(symbol *Symbol, usageNode *parser.NameNode) *DeclaredSymbolTypeInfo {
	if synthesized := symbol.GetSynthesizedType(); synthesized != nil && synthesized.Type != nil {
		return &DeclaredSymbolTypeInfo{Type: synthesized.Type}
	}

	typedDecls := symbol.GetTypedDeclarations()

	if len(typedDecls) == 0 {
		// The original's comment: if the symbol has no type declaration but is
		// assigned many times, treat it as though it has an explicit type
		// annotation of "Unknown". This will avoid a pathological performance
		// condition for unannotated code that reassigns the same variable
		// hundreds of times. If the symbol effectively has an "Any" annotation,
		// it won't be narrowed.
		if len(symbol.GetDeclarations()) > maxDeclarationsToUseForInference {
			return &DeclaredSymbolTypeInfo{Type: UnknownTypeCreate(false)}
		}

		// The original's comment: there was no declaration with a defined type.
		return &DeclaredSymbolTypeInfo{}
	}

	// The original's comment: if there is more than one typed decl, filter out
	// any that are not reachable from the usage node (if specified). This can
	// happen in cases where a property symbol is redefined to add a setter,
	// deleter, etc.
	exceedsMaxDecls := false
	if usageNode != nil && len(typedDecls) > 1 {
		if len(typedDecls) > maxTypedDeclsPerSymbol {
			// The original's comment: if there are too many typed decls, don't
			// bother filtering them because this can be very expensive. Simply
			// use the last one in this case.
			typedDecls = []Declaration{typedDecls[len(typedDecls)-1]}
			exceedsMaxDecls = true
		} else {
			filteredTypedDecls := make([]Declaration, 0, len(typedDecls))
			for _, decl := range typedDecls {
				if decl.DeclBase().Type != DeclarationTypeAlias {
					// The original's comment: is the declaration in the same
					// execution scope as the "usageNode" node?
					usageScope := GetExecutionScopeNode(usageNode)
					declScope := GetExecutionScopeNode(decl.DeclBase().Node)

					if usageScope == declScope {
						// The original's comment: for typed declarations we use
						// the precise flow-graph reachability check rather than
						// a simple position comparison, because typed decls
						// (e.g. explicit annotations) can legitimately appear
						// after the usage in the source text (e.g. a class
						// attribute annotated below a method that references
						// it) and must not be excluded by position alone.
						if !e.isFlowPathBetweenNodes(decl.DeclBase().Node, usageNode, false) {
							continue
						}
					}
				}
				filteredTypedDecls = append(filteredTypedDecls, decl)
			}

			if len(filteredTypedDecls) == 0 {
				return &DeclaredSymbolTypeInfo{Type: UnboundTypeCreate()}
			}

			typedDecls = filteredTypedDecls
		}
	}

	// The original's comment: start with the last decl. If that's already being
	// resolved, use the next-to-last decl, etc. This can happen when resolving
	// property methods. Often the setter method is defined in reference to the
	// initial property, which defines the getter method with the same symbol
	// name.
	for declIndex := len(typedDecls) - 1; declIndex >= 0; declIndex-- {
		decl := typedDecls[declIndex]

		// The original's comment: if there's a partially-constructed type that
		// is allowed for recursive symbol resolution, return it as the resolved
		// type.
		if partialType := e.getSymbolResolutionPartialType(symbol, decl); partialType != nil {
			return &DeclaredSymbolTypeInfo{Type: partialType}
		}

		if e.getIndexOfSymbolResolution(symbol, decl) < 0 {
			if e.pushSymbolResolution(symbol, decl) {
				// The original wraps the body in try/finally-by-hand: on an
				// exception it pops the stack and rethrows. Go's panics unwind
				// the same way only with a defer, and the evaluator's
				// cancellation panic must not leave the stack unbalanced.
				declaredTypeInfo, popped := e.resolveDeclWithinSymbolResolution(symbol, decl)

				// The original's comment: if there was recursion detected, don't
				// use this declaration. The exception is it's a class
				// declaration because getTypeOfClass handles recursion by
				// populating a partially-created class type in the type cache.
				// This exception is required to handle the circular dependency
				// between the "type" and "object" classes in builtins.pyi (since
				// "object" is a "type" and "type" is an "object").
				if popped || decl.DeclBase().Type == DeclarationTypeClass {
					return declaredTypeInfo
				}
			}
		}
	}

	return &DeclaredSymbolTypeInfo{ExceedsMaxDecls: exceedsMaxDecls}
}

// resolveDeclWithinSymbolResolution is the original's try/catch around
// getTypeForDeclaration. It returns the declaration's type info together with
// the result of popSymbolResolution, and guarantees the stack is popped even
// when getTypeForDeclaration panics.
func (e *typeEvaluator) resolveDeclWithinSymbolResolution(
	symbol *Symbol,
	decl Declaration,
) (info *DeclaredSymbolTypeInfo, popped bool) {
	completed := false
	defer func() {
		if !completed {
			// The original's catch clause: clean up the stack before
			// rethrowing. The panic keeps unwinding once this defer returns.
			e.popSymbolResolution(symbol)
		}
	}()

	info = e.getTypeForDeclaration(decl)
	completed = true
	popped = e.popSymbolResolution(symbol)
	return info, popped
}

// GetDeclaredTypeOfSymbol is the TypeEvaluator interface's single-argument
// form; the original's second parameter is internal.
func (e *typeEvaluator) GetDeclaredTypeOfSymbol(symbol *Symbol) *DeclaredSymbolTypeInfo {
	return e.getDeclaredTypeOfSymbol(symbol, nil)
}

// getTypeForDeclaration corresponds to the function of the same name.
func (e *typeEvaluator) getTypeForDeclaration(declaration Declaration) *DeclaredSymbolTypeInfo {
	switch decl := declaration.(type) {
	case *IntrinsicDeclaration:
		return e.getTypeForIntrinsicDeclaration(decl)

	case *ClassDeclaration:
		classTypeInfo := e.GetTypeOfClass(decl.Node.(*parser.ClassNode))
		if classTypeInfo == nil {
			return &DeclaredSymbolTypeInfo{}
		}
		return &DeclaredSymbolTypeInfo{Type: classTypeInfo.DecoratedType}

	case *SpecialBuiltInClassDeclaration:
		annotationNode := decl.Node.(*parser.TypeAnnotationNode).D.Annotation
		return &DeclaredSymbolTypeInfo{Type: e.GetTypeOfAnnotation(annotationNode, nil)}

	case *FunctionDeclaration:
		functionTypeInfo := e.GetTypeOfFunction(decl.Node.(*parser.FunctionNode))
		if functionTypeInfo == nil {
			return &DeclaredSymbolTypeInfo{}
		}
		return &DeclaredSymbolTypeInfo{Type: functionTypeInfo.DecoratedType}

	case *TypeAliasDeclaration:
		return &DeclaredSymbolTypeInfo{Type: e.getTypeOfTypeAlias(decl.Node.(*parser.TypeAliasNode))}

	case *ParamDeclaration:
		return e.getTypeForParamDeclaration(decl)

	case *TypeParamDeclaration:
		return &DeclaredSymbolTypeInfo{Type: e.getTypeOfTypeParam(decl.Node.(*parser.TypeParameterNode))}

	case *VariableDeclaration:
		return e.getTypeForVariableDeclaration(decl)

	case *AliasDeclaration:
		return &DeclaredSymbolTypeInfo{}
	}

	// The original's switch is exhaustive over the DeclarationType union, so
	// there is no fall-through in the TypeScript. Go needs a return.
	return &DeclaredSymbolTypeInfo{}
}

// getTypeForIntrinsicDeclaration is the DeclarationType.Intrinsic arm.
func (e *typeEvaluator) getTypeForIntrinsicDeclaration(decl *IntrinsicDeclaration) *DeclaredSymbolTypeInfo {
	switch decl.IntrinsicType {
	case IntrinsicTypeAny:
		return &DeclaredSymbolTypeInfo{Type: AnyTypeCreate(false)}

	case IntrinsicTypeDunderClass:
		classNode := GetEnclosingClass(decl.Node, false)
		classTypeInfo := e.GetTypeOfClass(classNode)
		if classTypeInfo != nil {
			return &DeclaredSymbolTypeInfo{
				Type: SpecializeWithUnknownTypeArgs(classTypeInfo.ClassType, e.GetTupleClassType()),
			}
		}
		return &DeclaredSymbolTypeInfo{Type: UnknownTypeCreate(false)}
	}

	strType := e.GetBuiltInObject(decl.Node, "str", nil)
	intType := e.GetBuiltInObject(decl.Node, "int", nil)
	if IsClassInstance(intType) && IsClassInstance(strType) {
		switch decl.IntrinsicType {
		case IntrinsicTypeStr:
			return &DeclaredSymbolTypeInfo{Type: strType}

		case IntrinsicTypeStrOrNone:
			return &DeclaredSymbolTypeInfo{Type: CombineTypes([]Type{strType, e.GetNoneType()}, nil)}

		case IntrinsicTypeInt:
			return &DeclaredSymbolTypeInfo{Type: intType}

		case IntrinsicTypeMutableSequenceStr:
			sequenceType := e.GetBuiltInType(decl.Node, "MutableSequence")
			if IsInstantiableClass(sequenceType) {
				specialized := ClassTypeSpecialize(sequenceType.(*ClassType), []Type{strType}, nil, false, nil, nil)
				return &DeclaredSymbolTypeInfo{Type: ClassTypeCloneAsInstance(specialized, false)}
			}

		case IntrinsicTypeDictStrAny:
			dictType := e.GetBuiltInType(decl.Node, "dict")
			if IsInstantiableClass(dictType) {
				specialized := ClassTypeSpecialize(
					dictType.(*ClassType),
					[]Type{strType, AnyTypeCreate(false)},
					nil, false, nil, nil,
				)
				return &DeclaredSymbolTypeInfo{Type: ClassTypeCloneAsInstance(specialized, false)}
			}
		}
	}

	return &DeclaredSymbolTypeInfo{Type: UnknownTypeCreate(false)}
}

// getTypeForParamDeclaration is the DeclarationType.Param arm.
func (e *typeEvaluator) getTypeForParamDeclaration(decl *ParamDeclaration) *DeclaredSymbolTypeInfo {
	paramNode := decl.Node.(*parser.ParameterNode)

	typeAnnotationNode := paramNode.D.Annotation
	if typeAnnotationNode == nil {
		typeAnnotationNode = paramNode.D.AnnotationComment
	}

	// The original's comment: if there wasn't an annotation, see if the parent
	// function has a function-level annotation comment that provides this
	// parameter's annotation type.
	if typeAnnotationNode == nil {
		if functionNode, ok := paramNode.NodeBase().Parent.(*parser.FunctionNode); ok {
			if functionNode.D.FuncAnnotationComment != nil && !functionNode.D.FuncAnnotationComment.D.IsEllipsis {
				paramIndex := -1
				for i, param := range functionNode.D.Params {
					if param == paramNode {
						paramIndex = i
						break
					}
				}
				typeAnnotationNode = GetTypeAnnotationForParam(functionNode, paramIndex)
			}
		}
	}

	if typeAnnotationNode != nil {
		declaredType := e.getTypeOfParamAnnotation(typeAnnotationNode, paramNode.D.Category)

		liveTypeVarScopes := GetTypeVarScopesForNode(paramNode)
		declaredType = MakeTypeVarsBound(declaredType, liveTypeVarScopes, true)

		return &DeclaredSymbolTypeInfo{
			Type: e.transformVariadicParamType(
				paramNode,
				paramNode.D.Category,
				e.adjustParamAnnotatedType(paramNode, declaredType),
			),
		}
	}

	return &DeclaredSymbolTypeInfo{}
}

// getTypeForVariableDeclaration is the DeclarationType.Variable arm.
func (e *typeEvaluator) getTypeForVariableDeclaration(decl *VariableDeclaration) *DeclaredSymbolTypeInfo {
	typeAnnotationNode := decl.TypeAnnotationNode
	if typeAnnotationNode == nil {
		return &DeclaredSymbolTypeInfo{}
	}

	var declaredType Type

	if decl.IsRuntimeTypeExpression {
		declaredType = ConvertToInstance(e.GetTypeOfExpressionExpectingType(typeAnnotationNode, &ExpectedTypeOptions{
			AllowFinal:            true,
			AllowRequired:         true,
			AllowReadOnly:         true,
			RuntimeTypeExpression: true,
		}).Type, false)
	} else {
		declNode := decl.Node
		if decl.IsDefinedByMemberAccess {
			if memberAccess, ok := decl.Node.NodeBase().Parent.(*parser.MemberAccessNode); ok {
				declNode = memberAccess
			}
		}

		declExpr, _ := declNode.(parser.ExpressionNode)
		allowClassVar := e.isClassVarAllowedForAssignmentTarget(declExpr)
		allowFinal := e.isFinalAllowedForAssignmentTarget(declExpr)
		allowRequired := IsRequiredAllowedForAssignmentTarget(declExpr) || decl.IsInInlinedTypedDict

		declaredType = e.GetTypeOfAnnotation(typeAnnotationNode, &ExpectedTypeOptions{
			VarTypeAnnotation:        true,
			AllowClassVar:            allowClassVar,
			AllowFinal:               allowFinal,
			AllowRequired:            allowRequired,
			AllowReadOnly:            allowRequired,
			EnforceClassTypeVarScope: decl.IsDefinedByMemberAccess,
		})
	}

	if declaredType == nil {
		return &DeclaredSymbolTypeInfo{}
	}

	// The original's comment: if this is a declaration for a member variable
	// within a method, we need to convert any bound TypeVars associated with the
	// class to their free counterparts.
	if decl.IsDefinedByMemberAccess {
		if enclosingClass := GetEnclosingClass(decl.Node, false); enclosingClass != nil {
			declaredType = MakeTypeVarsFree(declaredType, []TypeVarScopeId{GetScopeIdForNode(enclosingClass)})
		}
	}

	if IsClassInstance(declaredType) && ClassTypeIsBuiltInNamed(declaredType.(*ClassType), "TypeAlias") {
		return &DeclaredSymbolTypeInfo{IsTypeAlias: true}
	}

	return &DeclaredSymbolTypeInfo{Type: declaredType}
}

/*
 * The three small helpers the parameter arm needs.
 */

// getTypeOfParamAnnotation corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfParamAnnotation(
	paramTypeNode parser.ExpressionNode,
	paramCategory parser.ParamCategory,
) Type {
	return e.GetTypeOfAnnotation(paramTypeNode, &ExpectedTypeOptions{
		TypeVarGetsCurScope:    true,
		AllowUnpackedTuple:     paramCategory == parser.ParamCategoryArgsList,
		AllowUnpackedTypedDict: paramCategory == parser.ParamCategoryKwargsDict,
	})
}

// adjustParamAnnotatedType corresponds to the function of the same name.
func (e *typeEvaluator) adjustParamAnnotatedType(param *parser.ParameterNode, t Type) Type {
	// The original's comment: PEP 484 indicates that if a parameter has a
	// default value of 'None' the type checker should assume that the type is
	// optional (i.e. a union of the specified type and 'None'). Skip this step
	// if the type is already optional to avoid losing alias names when combining
	// the types.
	if constant, ok := param.D.DefaultValue.(*parser.ConstantNode); ok &&
		constant.D.ConstType == parser.KeywordTypeNone &&
		!IsOptionalType(t) &&
		!GetFileInfo(param).DiagnosticRuleSet.StrictParameterNoneValue {
		return CombineTypes([]Type{t, e.GetNoneType()}, nil)
	}

	return t
}

// transformVariadicParamType corresponds to the function of the same name.
func (e *typeEvaluator) transformVariadicParamType(
	node parser.ParseNode,
	paramCategory parser.ParamCategory,
	t Type,
) Type {
	switch paramCategory {
	case parser.ParamCategorySimple:
		return t

	case parser.ParamCategoryArgsList:
		if IsParamSpec(t) && t.(*TypeVarType).Priv.ParamSpecAccess != ParamSpecAccessNone {
			return t
		}

		if IsUnpackedClass(t) {
			return ClassTypeCloneForPacked(t.(*ClassType))
		}

		return MakeTupleObject(e, []*TupleTypeArg{{Type: t, IsUnbounded: !IsTypeVarTuple(t)}}, false)

	case parser.ParamCategoryKwargsDict:
		// The original's comment: leave a ParamSpec alone.
		if IsParamSpec(t) && t.(*TypeVarType).Priv.ParamSpecAccess != ParamSpecAccessNone {
			return t
		}

		// The original's comment: is this an unpacked TypedDict? If so, return
		// its packed version.
		if IsClassInstance(t) && ClassTypeIsTypedDictClass(t.(*ClassType)) && t.(*ClassType).Priv.IsUnpacked {
			return ClassTypeCloneForPacked(t.(*ClassType))
		}

		// The original's comment: wrap the type in a dict with str keys.
		dictType := e.GetBuiltInType(node, "dict")
		strType := e.GetBuiltInObject(node, "str", nil)

		if IsInstantiableClass(dictType) && IsClassInstance(strType) {
			specialized := ClassTypeSpecialize(dictType.(*ClassType), []Type{strType, t}, nil, false, nil, nil)
			return ClassTypeCloneAsInstance(specialized, false)
		}

		return UnknownTypeCreate(false)
	}

	// The original's switch is exhaustive over ParamCategory.
	return t
}
