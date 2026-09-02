/*
 * typeevaluator_literals.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412): the leaf
 * expression kinds, the signature tracker, and makeTopLevelTypeVarsConcrete.
 *
 * These have nothing in common except that each was near the top of the
 * frontier and each turned out to depend only on things already ported. Between
 * them they were 38,000 hits over the gate corpus:
 *
 *   useSignatureTracker             19,560
 *   MakeTopLevelTypeVarsConcrete    13,368
 *   getTypeOfEllipsis                2,958
 *   ensureSignatureIsUnique          2,322
 *
 * makeTopLevelTypeVarsConcrete is the interesting one. It is what turns a
 * TypeVar into something a comparison can be made against -- a constrained
 * TypeVar into the union of its constraints, a bound one into its bound, a
 * ParamSpec's .args into tuple[object, ...] -- and almost everything downstream
 * of a generic call goes through it.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

/*
 * The leaf expression kinds.
 */

// getTypeOfEllipsis corresponds to the function of the same name. The original
// threads an in-out typeResult parameter through; the Go form returns it, since
// every caller overwrites it unconditionally.
func (e *typeEvaluator) getTypeOfEllipsis(flags EvalFlags, node parser.ExpressionNode) *TypeResult {
	if (flags & EvalFlagsConvertEllipsisToAny) != 0 {
		return &TypeResult{Type: AnyTypeCreate(true)}
	}

	if (flags&EvalFlagsTypeExpression) != 0 && (flags&EvalFlagsAllowEllipsis) == 0 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.EllipsisContext(), node, nil)
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	ellipsisType := e.GetBuiltInObject(node, "EllipsisType", nil)
	if ellipsisType == nil {
		ellipsisType = e.GetBuiltInObject(node, "ellipsis", nil)
	}
	if ellipsisType == nil {
		ellipsisType = AnyTypeCreate(false)
	}

	return &TypeResult{Type: ellipsisType}
}

// getTypeOfNumber corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfNumber(node *parser.NumberNode) *TypeResult {
	if node.D.IsImaginary {
		return &TypeResult{Type: e.GetBuiltInObject(node, "complex", nil)}
	}

	if node.D.IsInteger {
		return &TypeResult{Type: e.cloneBuiltinObjectWithLiteral(node, "int", numberNodeLiteralValue(node.D.Value))}
	}

	return &TypeResult{Type: e.GetBuiltInObject(node, "float", nil)}
}

// getTypeOfConstant corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfConstant(node *parser.ConstantNode, flags EvalFlags) *TypeResult {
	var t Type

	switch node.D.ConstType {
	case parser.KeywordTypeNone:
		if e.prefetched != nil && e.prefetched.NoneTypeClass != nil {
			if (flags & EvalFlagsInstantiableType) != 0 {
				t = e.prefetched.NoneTypeClass
			} else {
				t = ConvertToInstance(e.prefetched.NoneTypeClass, false)
			}

			t = CloneWithTypeForm(t, ConvertToInstance(t, false))
		}

	case parser.KeywordTypeTrue, parser.KeywordTypeFalse, parser.KeywordTypeDebug:
		t = e.GetBuiltInObject(node, "bool", nil)

		// The original's comment: for True and False, we can create truthy and
		// falsy versions of 'bool'.
		if t != nil && IsClassInstance(t) {
			switch node.D.ConstType {
			case parser.KeywordTypeTrue:
				t = ClassTypeCloneWithLiteral(t.(*ClassType), LiteralBool(true))
			case parser.KeywordTypeFalse:
				t = ClassTypeCloneWithLiteral(t.(*ClassType), LiteralBool(false))
			}
		}
	}

	if t == nil {
		t = UnknownTypeCreate(false)
	}

	return &TypeResult{Type: t}
}

// numberNodeLiteralValue converts the tokenizer's NumberValue union to the type
// model's LiteralValue union. Both carry the same `bigint | number` split; only
// the integer arm is reached from getTypeOfNumber, since the float and imaginary
// paths do not produce literal types.
func numberNodeLiteralValue(value parser.NumberValue) LiteralValue {
	if value.IsBigInt {
		return LiteralInt{Value: value.BigInt}
	}
	return LiteralFloat(value.Float)
}

// cloneBuiltinObjectWithLiteral corresponds to the function of the same name.
func (e *typeEvaluator) cloneBuiltinObjectWithLiteral(
	node parser.ParseNode,
	builtInName string,
	value LiteralValue,
) Type {
	t := e.GetBuiltInObject(node, builtInName, nil)
	if IsClassInstance(t) {
		return ClassTypeCloneWithLiteral(ClassTypeCloneRemoveTypePromotions(t.(*ClassType)), value)
	}

	return UnknownTypeCreate(false)
}

/*
 * The signature tracker.
 */

// getSignatureTrackerForNode corresponds to the function of the same name. It
// returns nil where the original returns undefined.
func (e *typeEvaluator) getSignatureTrackerForNode(node parser.ParseNode) *UniqueSignatureTracker {
	for i := len(e.signatureTrackerStack) - 1; i >= 0; i-- {
		if IsNodeContainedWithin(node, e.signatureTrackerStack[i].RootNode) {
			return e.signatureTrackerStack[i].Tracker
		}
	}

	return nil
}

// withSignatureTracker is the original's useSignatureTracker, which is generic
// in the callback's return type. Go methods cannot be generic, so the result is
// carried out through the closure and the typed wrappers adapt it.
//
// The original does the pop by hand in both the success and the throw path
// rather than in a finally, with a comment that the TypeScript debugger handles
// finally poorly when single stepping. A defer is the Go equivalent and covers
// both paths without the duplication.
func (e *typeEvaluator) withSignatureTracker(node parser.ParseNode, callback func()) {
	tracker := e.getSignatureTrackerForNode(node)

	// The original's comment: if a signature tracker doesn't already exist,
	// allocate one.
	if tracker == nil {
		e.signatureTrackerStack = append(e.signatureTrackerStack, &SignatureTrackerStackEntry{
			Tracker:  NewUniqueSignatureTracker(),
			RootNode: node,
		})
		defer func() {
			e.signatureTrackerStack = e.signatureTrackerStack[:len(e.signatureTrackerStack)-1]
		}()
	}

	callback()
}

// ensureSignatureIsUnique corresponds to the function of the same name.
func (e *typeEvaluator) ensureSignatureIsUnique(t Type, node parser.ParseNode) Type {
	tracker := e.getSignatureTrackerForNode(node)
	if tracker == nil {
		return t
	}

	if IsFunctionOrOverloaded(t) {
		return EnsureSignaturesAreUnique(t, tracker, node.NodeBase().Start)
	}

	return t
}

/*
 * makeTopLevelTypeVarsConcrete.
 */

// MakeTopLevelTypeVarsConcrete corresponds to makeTopLevelTypeVarsConcrete. The
// original's makeParamSpecsConcrete defaults to false and conditionFilter is
// optional; a nil slice here is the absent filter.
func (e *typeEvaluator) MakeTopLevelTypeVarsConcrete(t Type, makeParamSpecsConcrete bool) Type {
	return e.makeTopLevelTypeVarsConcrete(t, makeParamSpecsConcrete, nil)
}

func (e *typeEvaluator) makeTopLevelTypeVarsConcrete(
	t Type,
	makeParamSpecsConcrete bool,
	conditionFilter []TypeCondition,
) Type {
	t = TransformPossibleRecursiveTypeAlias(t, 0)

	return MapSubtypes(t, func(subtype Type) Type {
		if IsParamSpec(subtype) {
			switch subtype.(*TypeVarType).Priv.ParamSpecAccess {
			case ParamSpecAccessArgs:
				return MakeTupleObject(e, []*TupleTypeArg{{Type: e.GetObjectType(), IsUnbounded: true}}, false)

			case ParamSpecAccessKwargs:
				if e.prefetched != nil &&
					e.prefetched.DictClass != nil && IsInstantiableClass(e.prefetched.DictClass) &&
					e.prefetched.StrClass != nil && IsInstantiableClass(e.prefetched.StrClass) {
					specialized := ClassTypeSpecialize(
						e.prefetched.DictClass.(*ClassType),
						[]Type{ConvertToInstance(e.prefetched.StrClass, false), e.GetObjectType()},
						nil, false, nil, nil,
					)
					return ClassTypeCloneAsInstance(specialized, false)
				}

				return UnknownTypeCreate(false)
			}
		}

		// The original's comment: if this is a function that contains only a
		// ParamSpec (no additional parameters), convert it to a concrete type of
		// (*args: Unknown, **kwargs: Unknown).
		if makeParamSpecsConcrete && IsFunction(subtype) {
			convertedType := SimplifyFunctionToParamSpec(subtype.(*FunctionType))
			if IsParamSpec(convertedType) {
				return ParamSpecTypeGetUnknown()
			}
		}

		if IsTypeVarTuple(subtype) {
			tv := subtype.(*TypeVarType)

			// The original's comment: if it's in a union, convert to type or
			// object.
			if tv.Priv.IsInUnion {
				if tv.Base().IsInstantiable() {
					if e.prefetched != nil && e.prefetched.TypeClass != nil &&
						IsInstantiableClass(e.prefetched.TypeClass) {
						return e.prefetched.TypeClass
					}
				} else {
					return e.GetObjectType()
				}

				return AnyTypeCreate(false)
			}

			// The original's comment: fall back to "*tuple[object, ...]".
			return MakeTupleObject(e, []*TupleTypeArg{{Type: e.GetObjectType(), IsUnbounded: true}}, true)
		}

		if IsTypeVar(subtype) {
			return e.makeTypeVarConcrete(subtype.(*TypeVarType), conditionFilter)
		}

		return subtype
	}, nil)
}

// makeTypeVarConcrete is the original's `if (isTypeVar(subtype))` arm.
func (e *typeEvaluator) makeTypeVarConcrete(tv *TypeVarType, conditionFilter []TypeCondition) Type {
	// The original's comment: if this is a recursive type alias placeholder that
	// hasn't yet been resolved, return it as is.
	if tv.Shared.RecursiveAlias != nil {
		return tv
	}

	if TypeVarTypeHasConstraints(tv) {
		typesToCombine := []Type{}

		// The original's comment: expand the list of constrained subtypes,
		// filtering out any that are disallowed by the conditionFilter.
		for constraintIndex, constraintType := range tv.Shared.Constraints {
			if conditionFilter != nil {
				typeVarName := TypeVarTypeGetNameWithScope(tv)
				skip := false
				for _, filter := range conditionFilter {
					if filter.TypeVar.Priv.NameWithScope == typeVarName {
						// The original's comment: if this type variable is being
						// constrained to a single index, don't include the other
						// indices.
						if filter.ConstraintIndex != constraintIndex {
							skip = true
						}
						break
					}
				}
				if skip {
					continue
				}
			}

			expanded := constraintType
			if tv.Base().IsInstantiable() {
				expanded = ConvertToInstantiable(expanded, false)
			}

			typesToCombine = append(typesToCombine, AddConditionToType(
				expanded,
				[]TypeCondition{{TypeVar: tv, ConstraintIndex: constraintIndex}},
				nil,
			))
		}

		return CombineTypes(typesToCombine, nil)
	}

	if tv.Shared.IsExemptFromBoundCheck {
		return AnyTypeCreate(false)
	}

	// The original's comment: fall back to a bound of "object" if no bound is
	// provided.
	boundType := tv.Shared.BoundType
	if boundType == nil {
		boundType = e.GetObjectType()
	}

	// The original's comment: if this is a synthesized self/cls type var,
	// self-specialize its type arguments.
	if TypeVarTypeIsSelf(tv) && IsClass(boundType) && !ClassTypeIsPseudoGenericClass(boundType.(*ClassType)) {
		boundType = SelfSpecializeClass(boundType.(*ClassType), &SelfSpecializeOptions{
			UseBoundTypeVars: TypeVarTypeIsBound(tv),
		})
	}

	if tv.Priv.IsUnpacked && IsClass(boundType) {
		boundType = ClassTypeCloneForUnpacked(boundType.(*ClassType))
	}

	if tv.Base().IsInstantiable() {
		boundType = ConvertToInstantiable(boundType, false)
	}

	return AddConditionToType(boundType, []TypeCondition{{TypeVar: tv, ConstraintIndex: 0}}, nil)
}
