/*
 * typeevaluator_typealiasdecl.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfTypeAlias, getTypeOfTypeAliasCommon, inferVarianceForTypeAlias,
 * updateUsageVariancesRecursive.
 *
 * The `type X = ...` statement, and the variance a generic alias's parameters
 * end up with.
 *
 * A type alias may refer to itself -- `type Json = int | list[Json]` -- so the
 * evaluation cannot simply be "evaluate the right-hand side". A PLACEHOLDER
 * TypeVar standing for the alias goes into the cache first, and the recursive
 * reference resolves to that placeholder rather than re-entering. Once the real
 * type is known it becomes the placeholder's bound, which is what makes later
 * references to the alias resolve to something.
 *
 * That leaves the case the placeholder cannot save: an alias whose definition
 * refers to itself with nothing else around it, `type X = X`. isTypeAliasRecursive
 * catches it and the result becomes Unknown.
 *
 * The variance half answers a question PEP 695 aliases raise and older ones did
 * not: `type Alias[T] = list[T]` never declares whether T is covariant, so it
 * has to be INFERRED from how the alias uses it. The walk starts covariant at
 * the top and flips at every contravariant position -- a function parameter, or
 * a type argument to a contravariant class parameter -- combining what it finds
 * at each occurrence. Two occurrences at opposite variances combine to
 * invariant, which is why `Mapping[T, T]` leaves T invariant.
 *
 * The cache is written BEFORE the walk, not after, for the same reason as the
 * placeholder above: a recursive alias would otherwise walk forever.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getTypeOfTypeAlias corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfTypeAlias(node *parser.TypeAliasNode) Type {
	var typeParamNodes []*parser.TypeParameterNode
	if node.D.TypeParams != nil {
		typeParamNodes = node.D.TypeParams.D.Params
	}

	return e.getTypeOfTypeAliasCommon(node, node.D.Name, node.D.Expr, true, typeParamNodes,
		func() []*TypeVarType {
			if node.D.TypeParams != nil {
				return e.evaluateTypeParamList(node.D.TypeParams)
			}
			return nil
		})
}

// getTypeOfTypeAliasCommon corresponds to the function of the same name.
//
// Its comment: this function is common to the handling of "type" statements and
// explicit calls to the TypeAliasType constructor.
func (e *typeEvaluator) getTypeOfTypeAliasCommon(
	declNode parser.ParseNode,
	nameNode *parser.NameNode,
	valueNode parser.ExpressionNode,
	isPep695Syntax bool,
	typeParamNodes []*parser.TypeParameterNode,
	getTypeParamCallback func() []*TypeVarType,
) Type {
	noFlags := EvalFlagsNone
	if cachedType := e.readTypeCache(nameNode, &noFlags); cachedType != nil {
		return cachedType
	}

	// The original's comment: synthesize a type variable that represents the type
	// alias while we're evaluating it. This allows us to handle recursive
	// definitions.
	typeAliasTypeVar := e.synthesizeTypeAliasPlaceholder(nameNode, true)

	// The original's comment: write the type to the type cache to support
	// recursive type alias definitions.
	e.writeTypeCache(nameNode, &TypeResult{Type: typeAliasTypeVar}, nil, nil, false)

	// The original's comment: set a partial type to handle recursive
	// (self-referential) type aliases.
	scope := GetScopeForNode(declNode)
	var typeAliasSymbol *SymbolWithScope
	if scope != nil {
		typeAliasSymbol = scope.LookUpSymbolRecursive(nameNode.D.Value, nil)
	}
	typeAliasDecl := GetDeclaration(declNode)
	if typeAliasDecl != nil && typeAliasSymbol != nil {
		e.setSymbolResolutionPartialType(typeAliasSymbol.Symbol, typeAliasDecl, typeAliasTypeVar)
	}

	typeParams := getTypeParamCallback()
	if typeAliasTypeVar.Shared.RecursiveAlias != nil {
		if typeParams == nil {
			typeParams = []*TypeVarType{}
		}
		typeAliasTypeVar.Shared.RecursiveAlias.TypeParams = typeParams
	}

	var aliasTypeResult *TypeResult
	if isPep695Syntax {
		aliasTypeResult = e.GetTypeOfExpressionExpectingType(valueNode,
			&ExpectedTypeOptions{ForwardRefs: true, TypeExpression: true})
	} else {
		flags := EvalFlagsInstantiableType |
			EvalFlagsTypeExpression |
			EvalFlagsStrLiteralAsType |
			EvalFlagsNoParamSpec |
			EvalFlagsNoTypeVarTuple |
			EvalFlagsNoClassVar
		aliasTypeResult = e.GetTypeOfExpression(valueNode, flags, nil)
	}

	isIncomplete := aliasTypeResult.IsIncomplete
	aliasType := aliasTypeResult.Type

	aliasType = e.transformTypeForTypeAliasEx(aliasType, nameNode, typeAliasTypeVar, true, typeParamNodes)

	// The original's comment: see if the type alias relies on itself in a way that
	// cannot be resolved.
	if IsTypeAliasRecursive(typeAliasTypeVar, aliasType) {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeAliasIsRecursiveDirect().Format(nameNode.D.Value),
			valueNode, nil)

		aliasType = UnknownTypeCreate(false)
	}

	// The original's comment: set the resulting type to the boundType of the
	// original type alias to support recursive type aliases.
	typeAliasTypeVar.Shared.BoundType = aliasType

	evalFlags := EvalFlagsNone
	e.writeTypeCache(nameNode, &TypeResult{Type: aliasType, IsIncomplete: isIncomplete},
		&evalFlags, nil, false)

	return aliasType
}

// inferVarianceForTypeAlias corresponds to the function of the same name. It
// returns nil where the original returns undefined.
func (e *typeEvaluator) inferVarianceForTypeAlias(t Type) []Variance {
	var aliasInfo *TypeAliasInfo
	if props := t.Base().Props; props != nil {
		aliasInfo = props.TypeAliasInfo
	}

	// The original's comment: if this isn't a generic type alias, there's nothing
	// to do.
	if aliasInfo == nil || aliasInfo.Shared == nil || aliasInfo.Shared.TypeParams == nil {
		return nil
	}

	// The original's comment: is the computed variance info already cached?
	if aliasInfo.Shared.ComputedVariance != nil {
		return aliasInfo.Shared.ComputedVariance
	}

	typeParams := aliasInfo.Shared.TypeParams

	// The original's comment: start with all of the usage variances unknown.
	usageVariances := make([]Variance, len(typeParams))
	for i := range usageVariances {
		usageVariances[i] = VarianceUnknown
	}

	// The original's comment: prepopulate the cached value for the type alias to
	// handle recursive type aliases.
	aliasInfo.Shared.ComputedVariance = usageVariances

	// The original's comment: traverse the type alias type definition and adjust
	// the usage variances accordingly.
	e.updateUsageVariancesRecursive(t, typeParams, usageVariances, VarianceCovariant, nil, 0)

	return usageVariances
}

// updateUsageVariancesRecursive corresponds to the function of the same name.
//
// Its comment: looks at uses of the type parameters within the type and adjusts
// the variances accordingly. For example, if the type is `Mapping[T1, T2]`, then
// T1 will be set to invariant and T2 will be set to covariant.
func (e *typeEvaluator) updateUsageVariancesRecursive(
	t Type,
	typeAliasTypeParams []*TypeVarType,
	usageVariances []Variance,
	varianceContext Variance,
	pendingTypes []Type,
	recursionCount int,
) {
	if recursionCount > MaxTypeRecursionCount {
		return
	}

	transformedType := TransformPossibleRecursiveTypeAlias(t, 0)
	isRecursiveTypeAlias := transformedType != t

	// The original's comment: if this is a recursive type alias, see if we've
	// already recursed seen it once before in the recursion stack. If so, don't
	// recurse further.
	if isRecursiveTypeAlias {
		overlaps := 0
		for _, pendingType := range pendingTypes {
			if IsTypeSame(pendingType, t, TypeSameOptions{}, 0) {
				overlaps++
			}
		}
		if overlaps > 1 {
			return
		}

		pendingTypes = append(pendingTypes, t)
	}

	recursionCount++

	// The original's comment: define a helper function that performs the actual
	// usage variant update.
	updateUsageVarianceForType := func(t Type, variance Variance) {
		DoForEachSubtype(t, func(subtype Type, _ int, _ []Type) {
			typeParamIndex := -1
			for i, param := range typeAliasTypeParams {
				if IsTypeSame(param, subtype, TypeSameOptions{}, 0) {
					typeParamIndex = i
					break
				}
			}

			if typeParamIndex >= 0 {
				usageVariances[typeParamIndex] =
					CombineVariances(usageVariances[typeParamIndex], variance)
			} else {
				e.updateUsageVariancesRecursive(subtype, typeAliasTypeParams, usageVariances,
					variance, pendingTypes, recursionCount)
			}
		})
	}

	DoForEachSubtype(transformedType, func(subtype Type, _ int, _ []Type) {
		switch typed := subtype.(type) {
		case *FunctionType:
			for index := range typed.Shared.Parameters {
				paramType := FunctionTypeGetParamType(typed, index)
				updateUsageVarianceForType(paramType, InvertVariance(varianceContext))
			}

			if returnType := FunctionTypeGetEffectiveReturnType(typed, true); returnType != nil {
				updateUsageVarianceForType(returnType, varianceContext)
			}

		case *ClassType:
			if typed.Priv.TypeArgs == nil {
				return
			}

			// The original's comment: if the class includes type parameters that
			// uses auto variance, compute the calculated variance.
			e.InferVarianceForClass(typed)

			// The original's comment: is the class specialized using any type
			// arguments that correspond to the type alias' type parameters?
			for classParamIndex, typeArg := range typed.Priv.TypeArgs {
				if IsTupleClass(typed) {
					updateUsageVarianceForType(typeArg, varianceContext)
					continue
				}

				if classParamIndex >= len(typed.Shared.TypeParams) {
					continue
				}

				classTypeParam := typed.Shared.TypeParams[classParamIndex]

				if IsUnpackedClass(typeArg) && typeArg.(*ClassType).Priv.TupleTypeArgs != nil {
					for _, tupleTypeArg := range typeArg.(*ClassType).Priv.TupleTypeArgs {
						updateUsageVarianceForType(tupleTypeArg.Type, VarianceInvariant)
					}
					continue
				}

				effectiveVariance := classTypeParam.Shared.DeclaredVariance
				if classTypeParam.Priv.ComputedVariance != nil {
					effectiveVariance = *classTypeParam.Priv.ComputedVariance
				}

				if varianceContext == VarianceContravariant {
					effectiveVariance = InvertVariance(effectiveVariance)
				}

				updateUsageVarianceForType(typeArg, effectiveVariance)
			}
		}
	})

	// The original pops from the shared array here. Go's append gives each
	// recursion level its own view, so nothing outlives the call and the pop is
	// unnecessary.
}

// swapInComputedVariance is the original's auto-variance branch in getType. A
// TypeVar declared with Variance.Auto carries no useful variance until the class
// or alias that owns it has been evaluated, so this replaces it with the
// computed one once that has happened.
func (e *typeEvaluator) swapInComputedVariance(
	node parser.ExpressionNode, typeVarType *TypeVarType,
) Type {
	typeParamListNode := GetParentNodeOfType(node, parser.ParseNodeTypeTypeParameterList)
	if typeParamListNode == nil {
		return typeVarType
	}
	typeParamList, ok := typeParamListNode.(*parser.TypeParameterListNode)
	if !ok {
		return typeVarType
	}

	parent := typeParamList.NodeBase().Parent
	if parent == nil {
		return typeVarType
	}

	switch owner := parent.(type) {
	case *parser.ClassNode:
		classTypeResult := e.GetTypeOfClass(owner)
		if classTypeResult == nil {
			return typeVarType
		}

		e.InferVarianceForClass(classTypeResult.ClassType)

		for _, param := range classTypeResult.ClassType.Shared.TypeParams {
			if !IsTypeSame(param, typeVarType, TypeSameOptions{IgnoreTypeFlags: true}, 0) {
				continue
			}
			if param.Priv.ComputedVariance != nil {
				return TypeVarTypeCloneWithComputedVariance(typeVarType, *param.Priv.ComputedVariance)
			}
			break
		}

	case *parser.TypeAliasNode:
		typeAliasType := e.getTypeOfTypeAlias(owner)

		typeParamIndex := -1
		for i, param := range typeParamList.D.Params {
			if parser.ParseNode(param.D.Name) == parser.ParseNode(node) {
				typeParamIndex = i
				break
			}
		}

		if typeParamIndex < 0 {
			return typeVarType
		}

		e.inferVarianceForTypeAlias(typeAliasType)

		props := typeAliasType.Base().Props
		if props == nil || props.TypeAliasInfo == nil || props.TypeAliasInfo.Shared == nil {
			return typeVarType
		}
		computedVariance := props.TypeAliasInfo.Shared.ComputedVariance
		if computedVariance == nil || typeParamIndex >= len(computedVariance) {
			return typeVarType
		}

		return TypeVarTypeCloneWithComputedVariance(typeVarType, computedVariance[typeParamIndex])
	}

	return typeVarType
}
