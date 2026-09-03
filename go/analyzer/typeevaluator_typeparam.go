/*
 * typeevaluator_typeparam.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfTypeParam, verifyTypeVarDefaultIsCompatible, getTypeVarTupleDefaultType,
 * getParamSpecDefaultType, createCallableType, isTypeVarSame,
 * assignConditionalTypeToTypeVar.
 *
 * PEP 695 type parameters -- the `T` in `def f[T](...)` -- and `Callable[...]`.
 *
 * getTypeOfTypeParam builds the TypeVar from the syntax. The ordering matters
 * more than the content: the bare TypeVar is written into the cache BEFORE its
 * bound or default is evaluated, because either may refer to the parameter
 * itself, and without the early write that reference re-enters this function
 * and never terminates.
 *
 * The kind decides almost everything downstream. A bound expression that is a
 * tuple declares CONSTRAINTS rather than a bound, and a single-element tuple is
 * an error -- one constraint is not a choice. The default is read three
 * different ways per kind, and the fallback when there is no default is
 * different for each: Unknown for a TypeVar, `...` for a ParamSpec, and an
 * unbounded `*tuple[Unknown, ...]` for a TypeVarTuple.
 *
 * Scoping happens last and rewrites the TypeVar rather than annotating it,
 * because the scope id is part of its identity. A class-scoped or alias-scoped
 * TypeVar also gets variance Auto -- PEP 695 infers variance rather than making
 * the author declare it -- while ParamSpecs and TypeVarTuples stay invariant.
 *
 * createCallableType reads `Callable[[int, str], bool]` and produces the
 * function type it denotes. The first argument is a parameter LIST, which the
 * parser hands over as a typeList rather than as a type, and four other shapes
 * are accepted in its place: `...` for a gradual callable, a ParamSpec, a
 * `Concatenate[...]`, and nothing at all. The synthesized parameters are named
 * `__p0`, `__p1` and so on and marked positional-only, since `Callable` cannot
 * express keyword parameters.
 *
 * The two conditional-TypeVar helpers at the end deal with a subtlety of
 * narrowing inside a generic function: after `if isinstance(x, str)`, a value of
 * type `T` becomes `str` CONDITIONED on `T`, and that conditioned type still has
 * to be assignable back to `T`.
 */

package analyzer

import (
	"fmt"

	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getTypeOfTypeParam corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfTypeParam(node *parser.TypeParameterNode) Type {
	// The original's comment: is this type already cached?
	noFlags := EvalFlagsNone
	if cachedTypeVarType := e.readTypeCache(node.D.Name, &noFlags); cachedTypeVarType != nil {
		if IsTypeVar(cachedTypeVarType) {
			return cachedTypeVarType
		}
	}

	runtimeClassName := "TypeVar"
	kind := TypeVarKindTypeVar
	switch node.D.TypeParamKind {
	case parser.TypeParamKindTypeVarTuple:
		runtimeClassName = "TypeVarTuple"
		kind = TypeVarKindTypeVarTuple
	case parser.TypeParamKindParamSpec:
		runtimeClassName = "ParamSpec"
		kind = TypeVarKindParamSpec
	}

	runtimeType := e.GetTypingType(node, runtimeClassName)
	var runtimeClass *ClassType
	if runtimeType != nil && IsInstantiableClass(runtimeType) {
		runtimeClass = runtimeType.(*ClassType)
	}

	typeVar := TypeVarTypeCreateInstantiable(node.D.Name.D.Value, kind)
	if runtimeClass != nil {
		typeVar = CloneAsSpecialForm(typeVar, ClassTypeCloneAsInstance(runtimeClass, true))
	}
	typeVar.Shared.IsTypeParamSyntax = true

	// The original's comment: cache the value before we evaluate the bound or the
	// default type in case it refers to itself in a circular manner.
	e.writeTypeCache(node, &TypeResult{Type: typeVar}, nil, nil, false)
	e.writeTypeCache(node.D.Name, &TypeResult{Type: typeVar}, nil, nil, false)

	if node.D.BoundExpr != nil {
		e.applyTypeParamBound(node, typeVar)
	}

	e.applyTypeParamDefault(node, typeVar)

	// The original's comment: if a default is provided, make sure it is compatible
	// with the bound or constraint.
	if typeVar.Shared.IsDefaultExplicit && node.D.DefaultExpr != nil {
		e.verifyTypeVarDefaultIsCompatible(typeVar, node.D.DefaultExpr)
	}

	typeVar = e.scopeTypeParam(node, typeVar)

	e.writeTypeCache(node, &TypeResult{Type: typeVar}, nil, nil, false)
	e.writeTypeCache(node.D.Name, &TypeResult{Type: typeVar}, nil, nil, false)

	return typeVar
}

// applyTypeParamBound is the original's `node.d.boundExpr` block. A TUPLE bound
// expression declares constraints rather than a bound.
func (e *typeEvaluator) applyTypeParamBound(node *parser.TypeParameterNode, typeVar *TypeVarType) {
	boundOptions := &ExpectedTypeOptions{
		NoNonTypeSpecialForms: true,
		ForwardRefs:           true,
		TypeExpression:        true,
	}

	if tupleNode, ok := node.D.BoundExpr.(*parser.TupleNode); ok {
		constraints := make([]Type, 0, len(tupleNode.D.Items))
		for _, constraint := range tupleNode.D.Items {
			constraintType := e.GetTypeOfExpressionExpectingType(constraint, boundOptions).Type

			if RequiresSpecialization(constraintType, &RequiresSpecializationOptions{
				IgnorePseudoGeneric: true, IgnoreImplicitTypeArgs: true}, 0) {
				e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.TypeVarBoundGeneric(), constraint, nil)
			}

			constraints = append(constraints, ConvertToInstance(constraintType, true))
		}

		if len(constraints) < 2 {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeVarSingleConstraint(), node.D.BoundExpr, nil)
		} else if node.D.TypeParamKind == parser.TypeParamKindTypeVar {
			typeVar.Shared.Constraints = constraints
		}
		return
	}

	boundType := e.GetTypeOfExpressionExpectingType(node.D.BoundExpr, boundOptions).Type

	if RequiresSpecialization(boundType,
		&RequiresSpecializationOptions{IgnorePseudoGeneric: true}, 0) {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeVarConstraintGeneric(), node.D.BoundExpr, nil)
	}

	if node.D.TypeParamKind == parser.TypeParamKindTypeVar {
		typeVar.Shared.BoundType = ConvertToInstance(boundType, true)
	}
}

// applyTypeParamDefault is the original's three-way default handling. Each kind
// reads the default expression differently and falls back differently.
func (e *typeEvaluator) applyTypeParamDefault(node *parser.TypeParameterNode, typeVar *TypeVarType) {
	var defaultType Type

	switch node.D.TypeParamKind {
	case parser.TypeParamKindParamSpec:
		if node.D.DefaultExpr != nil {
			defaultType = e.getParamSpecDefaultType(node.D.DefaultExpr, true)
		}
		if defaultType == nil {
			typeVar.Shared.DefaultType = ParamSpecTypeGetUnknown()
			return
		}

	case parser.TypeParamKindTypeVarTuple:
		if node.D.DefaultExpr != nil {
			defaultType = e.getTypeVarTupleDefaultType(node.D.DefaultExpr, true)
		}
		if defaultType == nil {
			typeVar.Shared.DefaultType = MakeTupleObject(e,
				[]*TupleTypeArg{{Type: UnknownTypeCreate(false), IsUnbounded: true}}, false)
			return
		}

	default:
		if node.D.DefaultExpr != nil {
			defaultType = ConvertToInstance(e.GetTypeOfExpressionExpectingType(node.D.DefaultExpr,
				&ExpectedTypeOptions{ForwardRefs: true, TypeExpression: true}).Type, false)
		}
		if defaultType == nil {
			typeVar.Shared.DefaultType = UnknownTypeCreate(false)
			return
		}
	}

	typeVar.Shared.DefaultType = defaultType
	typeVar.Shared.IsDefaultExplicit = true
}

// scopeTypeParam is the original's tail, which associates the type variable with
// the owning scope. Scoping REWRITES the TypeVar because the scope id is part of
// its identity.
func (e *typeEvaluator) scopeTypeParam(
	node *parser.TypeParameterNode, typeVar *TypeVarType,
) *TypeVarType {
	scopeNode := GetTypeVarScopeNode(node)
	if scopeNode == nil {
		return typeVar
	}

	var scopeType TypeVarScopeType
	var scopeIdNode parser.ParseNode
	var scopeName string

	switch typed := scopeNode.(type) {
	case *parser.ClassNode:
		scopeType = TypeVarScopeTypeClass
		scopeIdNode = typed
		scopeName = typed.D.Name.D.Value

		// The original's comment: set the variance to "auto" for class-scoped
		// TypeVars.
		typeVar.Shared.DeclaredVariance = autoVarianceUnlessVariadic(typeVar)

	case *parser.FunctionNode:
		scopeType = TypeVarScopeTypeFunction
		scopeIdNode = typed
		scopeName = typed.D.Name.D.Value

	case *parser.TypeAliasNode:
		scopeType = TypeVarScopeTypeTypeAlias
		scopeIdNode = typed.D.Name
		scopeName = typed.D.Name.D.Value
		typeVar.Shared.DeclaredVariance = autoVarianceUnlessVariadic(typeVar)

	default:
		assert(false, "expected a class, function or type alias scope node")
		return typeVar
	}

	return TypeVarTypeCloneForScopeID(typeVar, GetScopeIdForNode(scopeIdNode), &scopeName, &scopeType)
}

// autoVarianceUnlessVariadic is the original's `isParamSpec(typeVar) ||
// isTypeVarTuple(typeVar) ? Variance.Invariant : Variance.Auto`.
func autoVarianceUnlessVariadic(typeVar *TypeVarType) Variance {
	if IsParamSpec(typeVar) || IsTypeVarTuple(typeVar) {
		return VarianceInvariant
	}
	return VarianceAuto
}

// verifyTypeVarDefaultIsCompatible corresponds to the function of the same name.
func (e *typeEvaluator) verifyTypeVarDefaultIsCompatible(
	typeVar *TypeVarType, defaultValueNode parser.ExpressionNode,
) {
	assert(typeVar.Shared.IsDefaultExplicit, "expected an explicit default")

	constraints := NewConstraintTracker()
	concreteDefaultType := e.MakeTopLevelTypeVarsConcrete(
		e.SolveAndApplyConstraints(typeVar.Shared.DefaultType, constraints,
			&ApplyTypeVarOptions{
				ReplaceUnsolved: &ReplaceUnsolvedOptions{
					ScopeIDs:       GetTypeVarScopeIds(typeVar),
					TupleClassType: e.GetTupleClassType(),
				},
			}, nil), false)

	if typeVar.Shared.BoundType != nil {
		if !e.AssignType(typeVar.Shared.BoundType, concreteDefaultType, nil, nil, AssignTypeFlagsDefault, 0) {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeVarDefaultBoundMismatch(), defaultValueNode, nil)
		}
		return
	}

	if !TypeVarTypeHasConstraints(typeVar) {
		return
	}

	isConstraintCompatible := true

	// The original's comment: if the default type is a constrained TypeVar, make
	// sure all of its constraints are also constraints in typeVar. If the default
	// type is not a constrained TypeVar, use its concrete type to compare against
	// the constraints.
	defaultTypeVar, defaultIsTypeVar := typeVar.Shared.DefaultType.(*TypeVarType)
	if defaultIsTypeVar && IsTypeVar(typeVar.Shared.DefaultType) &&
		TypeVarTypeHasConstraints(defaultTypeVar) {
		for _, constraint := range defaultTypeVar.Shared.Constraints {
			if !containsSameType(typeVar.Shared.Constraints, constraint, TypeSameOptions{}) {
				isConstraintCompatible = false
			}
		}
	} else if !containsSameType(typeVar.Shared.Constraints, concreteDefaultType,
		TypeSameOptions{IgnoreConditions: true}) {
		isConstraintCompatible = false
	}

	if !isConstraintCompatible {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeVarDefaultConstraintMismatch(), defaultValueNode, nil)
	}
}

// containsSameType is the original's `constraints.some((c) => isTypeSame(c, t))`.
func containsSameType(types []Type, target Type, options TypeSameOptions) bool {
	for _, t := range types {
		if IsTypeSame(t, target, options, 0) {
			return true
		}
	}
	return false
}

// getTypeVarTupleDefaultType corresponds to the function of the same name. It
// returns nil where the original returns undefined.
func (e *typeEvaluator) getTypeVarTupleDefaultType(node parser.ExpressionNode, isPep695Syntax bool) Type {
	argType := e.GetTypeOfExpressionExpectingType(node, &ExpectedTypeOptions{
		AllowUnpackedTuple:          true,
		AllowTypeVarsWithoutScopeId: true,
		ForwardRefs:                 isPep695Syntax,
		TypeExpression:              true,
	}).Type

	isUnpackedTuple := false
	if cls, ok := argType.(*ClassType); ok && IsClass(argType) {
		isUnpackedTuple = IsTupleClass(cls) && cls.Priv.IsUnpacked
	}
	isUnpackedTypeVar := IsUnpackedTypeVarTuple(argType)

	if !isUnpackedTuple && !isUnpackedTypeVar {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeVarTupleDefaultNotUnpacked(), node, nil)
		return nil
	}

	return ConvertToInstance(argType, true)
}

// getParamSpecDefaultType corresponds to the function of the same name. It
// returns nil where the original returns undefined.
func (e *typeEvaluator) getParamSpecDefaultType(node parser.ExpressionNode, isPep695Syntax bool) Type {
	functionType := FunctionTypeCreateSynthesizedInstance("", FunctionTypeFlagsParamSpecValue)

	if node.GetNodeType() == parser.ParseNodeTypeEllipsis {
		FunctionTypeAddDefaultParams(functionType, false)
		functionType.Shared.Flags |= FunctionTypeFlagsGradualCallableForm
		return functionType
	}

	if listNode, ok := node.(*parser.ListNode); ok {
		for index, paramExpr := range listNode.D.Items {
			typeResult := e.GetTypeOfExpressionExpectingType(paramExpr, &ExpectedTypeOptions{
				AllowTypeVarsWithoutScopeId: true,
				ForwardRefs:                 isPep695Syntax,
				TypeExpression:              true,
			})

			FunctionTypeAddParam(functionType, FunctionParamCreate(
				parser.ParamCategorySimple,
				ConvertToInstance(typeResult.Type, true),
				FunctionParamFlagsNameSynthesized|FunctionParamFlagsTypeDeclared,
				synthesizedParamName(index), nil, nil))
		}

		if len(listNode.D.Items) > 0 {
			FunctionTypeAddPositionOnlyParamSeparator(functionType)
		}

		// The original's comment: update the type cache so we don't attempt to
		// re-evaluate this node. The type doesn't matter, so use Any.
		e.writeTypeCache(node, &TypeResult{Type: AnyTypeCreate(false)}, nil, nil, false)
		return functionType
	}

	typeResult := e.GetTypeOfExpressionExpectingType(node, &ExpectedTypeOptions{
		AllowParamSpec:              true,
		AllowTypeVarsWithoutScopeId: true,
		AllowEllipsis:               true,
		TypeExpression:              true,
	})

	if typeResult.TypeErrors {
		return nil
	}

	if IsParamSpec(typeResult.Type) {
		FunctionTypeAddParamSpecVariadics(functionType, typeResult.Type.(*TypeVarType))
		return functionType
	}

	if IsClassInstance(typeResult.Type) &&
		ClassTypeIsBuiltInNamed(typeResult.Type.(*ClassType), "EllipsisType", "ellipsis") {
		FunctionTypeAddDefaultParams(functionType, false)
		return functionType
	}

	e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.ParamSpecDefaultNotTuple(), node, nil)

	return nil
}

// synthesizedParamName is the original's `__p${index}`.
func synthesizedParamName(index int) *string {
	name := fmt.Sprintf("__p%d", index)
	return &name
}

// createCallableType corresponds to the function of the same name.
func (e *typeEvaluator) createCallableType(
	classType *ClassType, typeArgs []*TypeResultWithNode, errorNode parser.ParseNode,
) Type {
	functionType := FunctionTypeCreateInstantiable(FunctionTypeFlagsNone, nil)
	var paramSpec *TypeVarType
	isValidTypeForm := true

	functionType.Base().SetSpecialForm(ClassTypeCloneAsInstance(classType, true))
	functionType.Shared.DeclaredReturnType = UnknownTypeCreate(false)
	functionType.Shared.TypeVarScopeID = TypeVarScopeId(GetScopeIdForNode(errorNode))

	if len(typeArgs) > 0 {
		functionType.Priv.IsCallableWithTypeArgs = true

		if typeArgs[0].TypeList != nil {
			e.addCallableParamsFromTypeList(functionType, typeArgs[0].TypeList, &isValidTypeForm)
		} else if IsEllipsisType(typeArgs[0].Type) {
			FunctionTypeAddDefaultParams(functionType, false)
			functionType.Shared.Flags |= FunctionTypeFlagsGradualCallableForm
		} else if IsParamSpec(typeArgs[0].Type) {
			paramSpec = typeArgs[0].Type.(*TypeVarType)
		} else if IsInstantiableClass(typeArgs[0].Type) &&
			ClassTypeIsBuiltInNamed(typeArgs[0].Type.(*ClassType), "Concatenate") {
			paramSpec = e.addCallableParamsFromConcatenate(functionType, typeArgs[0].Type.(*ClassType))
		} else {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.CallableFirstArg(), typeArgs[0].Node, nil)
			isValidTypeForm = false
		}

		if len(typeArgs) > 1 {
			typeArg1Type := typeArgs[1].Type
			if !e.ValidateTypeArg(typeArgs[1], nil) {
				typeArg1Type = UnknownTypeCreate(false)
			}
			functionType.Shared.DeclaredReturnType = ConvertToInstance(typeArg1Type, true)
		} else {
			e.AddDiagnostic(DiagnosticRuleReportMissingTypeArgument,
				localization.LocMessage.CallableSecondArg(), errorNode, nil)

			functionType.Shared.DeclaredReturnType = UnknownTypeCreate(false)
			isValidTypeForm = false
		}

		if len(typeArgs) > 2 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.CallableExtraArgs(), typeArgs[2].Node, nil)
			isValidTypeForm = false
		}
	} else {
		FunctionTypeAddDefaultParams(functionType, true)
		functionType.Shared.Flags |= FunctionTypeFlagsGradualCallableForm

		// A nil typeArgs means no subscript at all, which is fine; an EMPTY one
		// means `Callable[]`, which is not.
		if typeArgs != nil {
			isValidTypeForm = false
		}
	}

	if paramSpec != nil {
		FunctionTypeAddParamSpecVariadics(functionType, ConvertToInstance(paramSpec, true).(*TypeVarType))
	}

	if isValidTypeForm {
		return CloneWithTypeForm(functionType, ConvertToInstance(functionType, true))
	}

	return functionType
}

// addCallableParamsFromTypeList is the original's `typeArgs[0].typeList` branch,
// which turns `[int, str]` into two synthesized positional-only parameters.
func (e *typeEvaluator) addCallableParamsFromTypeList(
	functionType *FunctionType, typeList []*TypeResultWithNode, isValidTypeForm *bool,
) {
	sawUnpacked := false
	reportedUnpackedError := false

	// The original's comment: make sure we have at most one unpacked TypeVarTuple.
	noteSawUnpacked := func(entry *TypeResultWithNode) {
		if sawUnpacked && !reportedUnpackedError {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.VariadicTypeArgsTooMany(), entry.Node, nil)
			reportedUnpackedError = true
			*isValidTypeForm = false
		}
		sawUnpacked = true
	}

	for index, entry := range typeList {
		entryType := entry.Type
		paramCategory := parser.ParamCategorySimple

		if IsTypeVarTuple(entryType) {
			e.validateTypeVarTupleIsUnpacked(entryType.(*TypeVarType), entry.Node)
			paramCategory = parser.ParamCategoryArgsList
			noteSawUnpacked(entry)
		} else if e.ValidateTypeArg(entry, &ValidateTypeArgsOptions{AllowUnpackedTuples: true}) {
			if IsUnpackedClass(entryType) {
				paramCategory = parser.ParamCategoryArgsList

				for _, typeArg := range entryType.(*ClassType).Priv.TupleTypeArgs {
					if IsTypeVarTuple(typeArg.Type) || typeArg.IsUnbounded {
						noteSawUnpacked(entry)
						break
					}
				}
			}
		} else {
			entryType = UnknownTypeCreate(false)
		}

		FunctionTypeAddParam(functionType, FunctionParamCreate(
			paramCategory,
			ConvertToInstance(entryType, true),
			FunctionParamFlagsNameSynthesized|FunctionParamFlagsTypeDeclared,
			synthesizedParamName(index), nil, nil))
	}

	if len(typeList) > 0 {
		FunctionTypeAddPositionOnlyParamSeparator(functionType)
	}
}

// addCallableParamsFromConcatenate is the original's `Concatenate[...]` branch.
// The LAST argument is the tail -- a ParamSpec or `...` -- and the ones before
// it are ordinary parameters. It returns the ParamSpec if the tail was one.
func (e *typeEvaluator) addCallableParamsFromConcatenate(
	functionType *FunctionType, concatenate *ClassType,
) *TypeVarType {
	concatTypeArgs := concatenate.Priv.TypeArgs
	if len(concatTypeArgs) == 0 {
		return nil
	}

	var paramSpec *TypeVarType

	for index, typeArg := range concatTypeArgs {
		if index == len(concatTypeArgs)-1 {
			FunctionTypeAddPositionOnlyParamSeparator(functionType)

			if IsParamSpec(typeArg) {
				paramSpec = typeArg.(*TypeVarType)
			} else if IsEllipsisType(typeArg) {
				FunctionTypeAddDefaultParams(functionType, false)
				functionType.Shared.Flags |= FunctionTypeFlagsGradualCallableForm
			}
			continue
		}

		FunctionTypeAddParam(functionType, FunctionParamCreate(
			parser.ParamCategorySimple,
			typeArg,
			FunctionParamFlagsNameSynthesized|FunctionParamFlagsTypeDeclared,
			synthesizedParamName(index), nil, nil))
	}

	return paramSpec
}

// isTypeVarSame corresponds to typeUtils.isTypeVarSame.
//
// The original's comment on the union walk: a union of conditioned types is the
// same as the TypeVar it is conditioned on.
func (e *typeEvaluator) isTypeVarSame(type1 *TypeVarType, type2 Type) bool {
	if IsTypeSame(type1, type2, TypeSameOptions{}, 0) {
		return true
	}

	// The original's comment: if this isn't a bound TypeVar, return false.
	if IsParamSpec(type1) || IsTypeVarTuple(type1) || !TypeVarTypeHasBound(type1) {
		return false
	}

	// The original's comment: if the second type isn't a union, return false.
	if !IsUnion(type2) {
		return false
	}

	isCompatible := true
	DoForEachSubtype(type2, func(subtype Type, _ int, _ []Type) {
		if !isCompatible {
			return
		}

		if IsTypeSame(type1, subtype, TypeSameOptions{}, 0) {
			return
		}

		conditions := GetTypeCondition(subtype)
		matched := false
		for _, condition := range conditions {
			if condition.TypeVar.Priv.NameWithScope == type1.Priv.NameWithScope {
				matched = true
				break
			}
		}

		if !matched {
			isCompatible = false
		}
	})

	return isCompatible
}

// assignConditionalTypeToTypeVar corresponds to the function of the same name.
//
// After `if isinstance(x, str)` inside a generic function, a value of type `T`
// becomes `str` CONDITIONED on `T`, and that conditioned type still has to be
// assignable back to `T`.
func (e *typeEvaluator) assignConditionalTypeToTypeVar(
	destType *TypeVarType, srcType Type, recursionCount int,
) bool {
	destTypeVarName := TypeVarTypeGetNameWithScope(destType)

	// The original's comment: the srcType is assignable only if all of its
	// subtypes are assignable.
	return FindSubtype(srcType, func(srcSubtype Type) bool {
		if IsTypeSame(destType, srcSubtype,
			TypeSameOptions{IgnorePseudoGeneric: true}, recursionCount) {
			return false
		}

		if IsIncompleteUnknown(srcSubtype) {
			return false
		}

		// The original's comment: determine which conditions on this type apply to
		// this type variable. There might be more than one of them.
		applicableConditions := []TypeCondition{}
		for _, constraint := range GetTypeCondition(srcSubtype) {
			if constraint.TypeVar.Priv.NameWithScope == destTypeVarName {
				applicableConditions = append(applicableConditions, constraint)
			}
		}

		// The original's comment: if there are no applicable conditions, it's not
		// assignable.
		if len(applicableConditions) == 0 {
			return true
		}

		for _, condition := range applicableConditions {
			if e.conditionSatisfiesTypeVar(condition, destType, srcSubtype, recursionCount) {
				return false
			}
		}
		return true
	}) == nil
}

// conditionSatisfiesTypeVar is the original's inner `some` callback. Its outer
// name check is redundant -- applicableConditions already filtered on it -- and
// is kept for fidelity.
func (e *typeEvaluator) conditionSatisfiesTypeVar(
	condition TypeCondition, destType *TypeVarType, srcSubtype Type, recursionCount int,
) bool {
	if condition.TypeVar.Priv.NameWithScope != TypeVarTypeGetNameWithScope(destType) {
		return false
	}

	if destType.Shared.BoundType != nil {
		assert(condition.ConstraintIndex == 0,
			"Expected constraint for bound TypeVar to have index of 0")

		return e.AssignType(destType.Shared.BoundType, srcSubtype, nil, nil,
			AssignTypeFlagsDefault, recursionCount)
	}

	if TypeVarTypeHasConstraints(destType) {
		assert(condition.ConstraintIndex < len(destType.Shared.Constraints),
			"Constraint for constrained TypeVar is out of bounds")

		return e.AssignType(destType.Shared.Constraints[condition.ConstraintIndex], srcSubtype,
			nil, nil, AssignTypeFlagsDefault, recursionCount)
	}

	// The original's comment: this is a non-bound and non-constrained type
	// variable with a matching condition.
	return true
}
