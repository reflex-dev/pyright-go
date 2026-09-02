/*
 * typeevaluator_declaredexpr.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getDeclaredTypeForExpression.
 *
 * The declared type of an assignment TARGET, which is what supplies the expected
 * type for bidirectional inference of the value. `x: list[int] = []` infers the
 * empty list as list[int] only because this answers list[int] for `x`.
 *
 * Four target shapes are handled, and each finds a symbol a different way: a
 * name through scope lookup, a member access through class member lookup, an
 * index through the __setitem__ signature, and a tuple by recursing into its
 * items. Everything except Index and Tuple converges on resolveDeclaredMemberType
 * at the bottom.
 *
 * The member-access arm carries a long piece of reasoning from the original
 * about union bases with divergent member types, which is preserved verbatim
 * because it documents a deliberate false-positive avoidance that the code alone
 * does not explain.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// GetDeclaredTypeForExpression corresponds to getDeclaredTypeForExpression. It
// returns nil where the original returns undefined.
func (e *typeEvaluator) GetDeclaredTypeForExpression(
	expression parser.ExpressionNode,
	usage *EvaluatorUsage,
) Type {
	state := &declaredExprState{bindFunction: true}

	switch node := expression.(type) {
	case *parser.NameNode:
		e.declaredTypeForName(node, expression, state)

	case *parser.TypeAnnotationNode:
		return e.GetDeclaredTypeForExpression(node.D.ValueExpr, usage)

	case *parser.MemberAccessNode:
		if handled, result := e.declaredTypeForMemberAccess(node, usage, state); handled {
			return result
		}

	case *parser.IndexNode:
		if handled, result := e.declaredTypeForIndex(node, usage); handled {
			return result
		}

	case *parser.TupleNode:
		if handled, result := e.declaredTypeForTuple(node, usage); handled {
			return result
		}
	}

	if state.symbol != nil {
		return e.resolveDeclaredMemberType(
			state.symbol,
			state.classOrObjectBase,
			state.memberAccessClass,
			state.useDescriptorSetterType,
			state.bindFunction,
			state.selfType,
		)
	}

	return nil
}

// declaredExprState carries the six bindings the original declares at the top
// of the function and mutates from several of the arms.
type declaredExprState struct {
	symbol                  *Symbol
	selfType                Type
	classOrObjectBase       *ClassType
	memberAccessClass       Type
	bindFunction            bool
	useDescriptorSetterType bool
}

// declaredTypeForName is the original's Name arm.
func (e *typeEvaluator) declaredTypeForName(
	node *parser.NameNode,
	expression parser.ExpressionNode,
	state *declaredExprState,
) {
	symbolWithScope := e.lookUpSymbolRecursive(expression, node.D.Value, true, false)
	if symbolWithScope == nil {
		return
	}

	state.symbol = symbolWithScope.Symbol

	// The original's comment: handle the case where the symbol is a class-level
	// variable where the type isn't declared in this class but is in a parent
	// class.
	declaredTypeInfo := e.getDeclaredTypeOfSymbol(state.symbol, node)
	if (declaredTypeInfo == nil || declaredTypeInfo.Type == nil) &&
		symbolWithScope.Scope.Type == ScopeTypeClass {
		enclosing := GetEnclosingClassOrFunction(expression)
		if classNode, ok := enclosing.(*parser.ClassNode); ok {
			if classTypeInfo := e.GetTypeOfClass(classNode); classTypeInfo != nil {
				classMemberInfo := LookUpClassMember(
					classTypeInfo.ClassType,
					node.D.Value,
					MemberAccessFlagsSkipInstanceMembers|MemberAccessFlagsDeclaredTypesOnly,
					nil,
				)
				if classMemberInfo != nil {
					state.symbol = classMemberInfo.Symbol
				}
			}
		}
	}
}

// declaredTypeForMemberAccess is the original's MemberAccess arm.
func (e *typeEvaluator) declaredTypeForMemberAccess(
	node *parser.MemberAccessNode,
	usage *EvaluatorUsage,
	state *declaredExprState,
) (bool, Type) {
	baseType := e.getTypeOfExpression(node.D.LeftExpr, EvalFlagsMemberAccessBaseDefaults, nil).Type
	baseTypeConcrete := e.makeTopLevelTypeVarsConcrete(baseType, false, nil)
	memberName := node.D.Member.D.Value

	if IsTypeVar(baseType) {
		state.selfType = baseType
	}

	// The original's comment, preserved in full because it documents a
	// deliberate false-positive avoidance:
	//
	//   Normally, baseType will not be a composite type (a union), but this can
	//   occur. In this case, we compute the declared type of the member for each
	//   subtype. If the subtypes declare the member with the same generic class
	//   but different (invariant) type arguments (e.g. "list[int]" vs.
	//   "list[str]"), there is no single declared type that can serve as the
	//   expected type for bidirectional inference of an assigned value.
	//   Committing to one subtype's declared type produces a false positive when
	//   assigning a value (such as an empty container) that is compatible with
	//   every subtype, so in that case we return undefined and let the value be
	//   evaluated without an expected type. We sort the subtypes for determinism.
	//
	//   This is deliberately limited to the "same class, differing type
	//   arguments" case. When the subtypes declare the member with unrelated
	//   types, we retain the previous behavior (use one subtype's declared type)
	//   so that genuine assignment errors are still reported at the same location
	//   and downstream inference is unchanged.
	//
	//   This handling is further limited to cases where the declared base type is
	//   itself a union. We intentionally don't apply it when the base is a type
	//   variable that merely concretizes to a union.
	isUnionBase := IsUnion(baseType)
	var firstMemberDeclaredType Type
	sawMemberDeclaredType := false
	hasDivergentMemberDeclaredTypes := false
	divergesOnlyByTypeArgs := true

	DoForEachSubtypeSorted(baseTypeConcrete, func(baseSubtype Type, _ int, _ []Type) {
		switch {
		case IsClassInstance(baseSubtype):
			classMemberInfo := LookUpObjectMember(baseSubtype.(*ClassType), memberName, MemberAccessFlagsDeclaredTypesOnly, nil)

			state.classOrObjectBase = baseSubtype.(*ClassType)
			state.memberAccessClass = nil
			state.symbol = nil
			if classMemberInfo != nil {
				state.memberAccessClass = classMemberInfo.ClassType
				state.symbol = classMemberInfo.Symbol
			}
			state.useDescriptorSetterType = true

			// The original's comment: if this is an instance member (e.g. a
			// dataclass field), don't bind it to the object if it's a function.
			state.bindFunction = classMemberInfo == nil || !classMemberInfo.IsInstanceMember

		case IsInstantiableClass(baseSubtype):
			classMemberInfo := LookUpClassMember(baseSubtype.(*ClassType), memberName,
				MemberAccessFlagsSkipInstanceMembers|MemberAccessFlagsDeclaredTypesOnly, nil)

			state.classOrObjectBase = baseSubtype.(*ClassType)
			state.memberAccessClass = nil
			state.symbol = nil
			if classMemberInfo != nil {
				state.memberAccessClass = classMemberInfo.ClassType
				state.symbol = classMemberInfo.Symbol
			}
			state.useDescriptorSetterType = false
			state.bindFunction = true

		case IsModule(baseSubtype):
			state.classOrObjectBase = nil
			state.memberAccessClass = nil
			state.symbol = ModuleTypeGetField(baseSubtype.(*ModuleType), memberName)
			if state.symbol != nil && !state.symbol.HasTypedDeclarations() {
				// The original's comment: do not use inferred types for the
				// declared type.
				state.symbol = nil
			}
			state.useDescriptorSetterType = false
			state.bindFunction = false
		}

		// The original's comment: if the base is a union, verify that the
		// subtypes agree on a single declared type for the member.
		if !isUnionBase {
			return
		}

		var subtypeDeclaredType Type
		if state.symbol != nil {
			subtypeDeclaredType = e.resolveDeclaredMemberType(
				state.symbol, state.classOrObjectBase, state.memberAccessClass,
				state.useDescriptorSetterType, state.bindFunction, state.selfType,
			)
		}

		if subtypeDeclaredType == nil {
			return
		}

		if !sawMemberDeclaredType {
			firstMemberDeclaredType = subtypeDeclaredType
			sawMemberDeclaredType = true
			return
		}

		if firstMemberDeclaredType != nil &&
			IsTypeSame(firstMemberDeclaredType, subtypeDeclaredType, TypeSameOptions{}, 0) {
			return
		}

		hasDivergentMemberDeclaredTypes = true

		// The original's comment: the false positive we're addressing is
		// specific to invariant type arguments of the same generic class. If the
		// divergent types aren't instances of the same generic class, don't
		// treat this as an ambiguous declared type.
		if firstMemberDeclaredType == nil ||
			!IsClassInstance(firstMemberDeclaredType) || !IsClassInstance(subtypeDeclaredType) ||
			!ClassTypeIsSameGenericClass(
				ClassTypeCloneAsInstantiable(firstMemberDeclaredType.(*ClassType), false),
				ClassTypeCloneAsInstantiable(subtypeDeclaredType.(*ClassType), false),
				0,
			) {
			divergesOnlyByTypeArgs = false
		}
	})

	if hasDivergentMemberDeclaredTypes && divergesOnlyByTypeArgs {
		return true, nil
	}

	return false, nil
}

// declaredTypeForIndex is the original's Index arm: the declared type of `x[k]`
// as an assignment target is the second parameter of __setitem__.
func (e *typeEvaluator) declaredTypeForIndex(
	node *parser.IndexNode,
	usage *EvaluatorUsage,
) (bool, Type) {
	baseType := e.makeTopLevelTypeVarsConcrete(
		e.getTypeOfExpression(node.D.LeftExpr, EvalFlagsIndexBaseDefaults, nil).Type, false, nil)

	if baseType == nil || !IsClassInstance(baseType) {
		return false, nil
	}
	baseClass := baseType.(*ClassType)

	if ClassTypeIsTypedDictClass(baseClass) {
		effectiveUsage := usage
		if effectiveUsage == nil {
			effectiveUsage = EvaluatorUsageGet()
		}
		if typeFromTypedDict := e.getTypeOfIndexedTypedDict(node, baseClass, effectiveUsage); typeFromTypedDict != nil {
			return true, typeFromTypedDict.Type
		}
	}

	setItemType := e.GetBoundMagicMethod(baseClass, "__setitem__", nil, nil, nil, 0)
	if setItemType == nil {
		return false, nil
	}

	if IsOverloaded(setItemType) {
		// The original's comment: determine whether we need to use the slice
		// overload.
		expectsSlice := len(node.D.Items) == 1 &&
			node.D.Items[0].D.ValueExpr.GetNodeType() == parser.ParseNodeTypeSlice

		var matched Type
		for _, overload := range OverloadedTypeGetOverloads(setItemType.(*OverloadedType)) {
			if len(overload.Shared.Parameters) < 2 {
				continue
			}

			keyType := FunctionTypeGetParamType(overload, 0)
			isSlice := IsClassInstance(keyType) && ClassTypeIsBuiltInNamed(keyType.(*ClassType), "slice")
			if expectsSlice == isSlice {
				matched = overload
				break
			}
		}

		if matched == nil {
			return false, nil
		}
		setItemType = matched
	}

	if IsFunction(setItemType) && len(setItemType.(*FunctionType).Shared.Parameters) >= 2 {
		paramType := FunctionTypeGetParamType(setItemType.(*FunctionType), 1)
		if !IsAnyOrUnknown(paramType) {
			return true, paramType
		}
	}

	return false, nil
}

// declaredTypeForTuple is the original's Tuple arm. Its comment: if this is a
// tuple expression with at least one item and no unpacked items, and all of the
// items have declared types, we can assume a declared type for the resulting
// tuple. This is needed to enable bidirectional type inference when assigning to
// an unpacked tuple.
func (e *typeEvaluator) declaredTypeForTuple(
	node *parser.TupleNode,
	usage *EvaluatorUsage,
) (bool, Type) {
	if len(node.D.Items) == 0 {
		return false, nil
	}

	for _, item := range node.D.Items {
		if item.GetNodeType() == parser.ParseNodeTypeUnpack {
			return false, nil
		}
	}

	itemTypes := []Type{}
	for _, expr := range node.D.Items {
		if itemType := e.GetDeclaredTypeForExpression(expr, usage); itemType != nil {
			itemTypes = append(itemTypes, itemType)
		}
	}

	if len(itemTypes) != len(node.D.Items) {
		return false, nil
	}

	// The original's comment: if all items have a declared type, return a tuple
	// of those types.
	tupleTypeArgs := make([]*TupleTypeArg, 0, len(itemTypes))
	for _, t := range itemTypes {
		tupleTypeArgs = append(tupleTypeArgs, &TupleTypeArg{Type: t, IsUnbounded: false})
	}

	return true, MakeTupleObject(e, tupleTypeArgs, false)
}

/*
 * The two things this reaches.
 */

// resolveDeclaredMemberType corresponds to the function of the same name. The
// original's comment: given a member symbol and the context in which it was
// accessed, computes the declared type of the member -- applying descriptor
// setter types, partial specialization, and function binding as appropriate.
func (e *typeEvaluator) resolveDeclaredMemberType(
	symbol *Symbol,
	classOrObjectBase *ClassType,
	memberAccessClass Type,
	useDescriptorSetterType bool,
	bindFunction bool,
	selfType Type,
) Type {
	declaredTypeInfo := e.getDeclaredTypeOfSymbol(symbol, nil)
	if declaredTypeInfo == nil || declaredTypeInfo.Type == nil {
		return nil
	}
	declaredType := declaredTypeInfo.Type

	// The original's comment: if it's a descriptor, we need to get the setter
	// type.
	if useDescriptorSetterType && IsClassInstance(declaredType) {
		setter := e.GetBoundMagicMethod(declaredType.(*ClassType), "__set__", nil, nil, nil, 0)
		if setter != nil && IsFunction(setter) && len(setter.(*FunctionType).Shared.Parameters) >= 2 {
			declaredType = FunctionTypeGetParamType(setter.(*FunctionType), 1)

			if IsAnyOrUnknown(declaredType) {
				return nil
			}
		}
	}

	if classOrObjectBase != nil {
		if memberAccessClass != nil && IsInstantiableClass(memberAccessClass) {
			declaredType = PartiallySpecializeType(
				declaredType, memberAccessClass.(*ClassType), e.GetTypeClassType(), selfType)
		}

		if IsFunctionOrOverloaded(declaredType) && bindFunction {
			declaredType = e.BindFunctionToClassOrObject(
				classOrObjectBase, declaredType, nil, false, selfType, nil, 0)
		}
	}

	return declaredType
}

// getTypeOfIndexedTypedDict corresponds to the typedDicts.ts function of the
// same name.
func (e *typeEvaluator) getTypeOfIndexedTypedDict(
	node *parser.IndexNode, baseType *ClassType, usage *EvaluatorUsage,
) *TypeResult {
	return GetTypeOfIndexedTypedDict(e, node, baseType, usage)
}
