/*
 * typeutils_applysolved.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * ApplySolvedTypeVarsTransformer, UnificationTypeTransformer,
 * UniqueFunctionSignatureTransformer and the public entry points that drive
 * them.
 *
 * Transliterated from analyzer/typeUtils.ts (pyright 1.1.412), lines 4060-4118
 * and 4181-4528, plus the wrappers at 1572-1578, 1615-1622, 1645-1653, and the
 * two specialization helpers at 2176-2186 and 2231-2244 that call
 * applySolvedTypeVars. See the header of typeutils_transform.go for how
 * subclassing works.
 */

package analyzer

import (
	"strconv"
	"strings"
)

// applySolvedTypeVarsTransformer specializes a (potentially generic) type by
// substituting type variables from a solution.
type applySolvedTypeVarsTransformer struct {
	*TypeVarTransformer

	solution *ConstraintSolution
	options  *ApplyTypeVarOptions

	isSolvingDefaultType bool

	// activeConstraintSetIndex is nil where the TypeScript has `undefined`,
	// which doForEachConstraintSet distinguishes from index 0.
	activeConstraintSetIndex *int
}

func newApplySolvedTypeVarsTransformer(
	solution *ConstraintSolution,
	options *ApplyTypeVarOptions,
) *applySolvedTypeVarsTransformer {
	t := &applySolvedTypeVarsTransformer{
		TypeVarTransformer: &TypeVarTransformer{},
		solution:           solution,
		options:            options,
	}
	InitTypeVarTransformer(t.TypeVarTransformer, t)
	return t
}

// activeSolutionSet corresponds to
// `this._solution.getSolutionSet(this._activeConstraintSetIndex ?? 0)`.
func (t *applySolvedTypeVarsTransformer) activeSolutionSet() *ConstraintSolutionSet {
	index := 0
	if t.activeConstraintSetIndex != nil {
		index = *t.activeConstraintSetIndex
	}
	return t.solution.GetSolutionSet(index)
}

func (t *applySolvedTypeVarsTransformer) TransformTypeVar(typeVar *TypeVarType, recursionCount int) Type {
	solutionSet := t.activeSolutionSet()

	// If we're solving a default type, handle type variables with no scope ID.
	if t.isSolvingDefaultType && typeVar.Priv.ScopeID == "" {
		replacement := t.getReplacementForDefaultByName(typeVar, solutionSet)
		if replacement != nil {
			return replacement
		}

		if typeVar.Shared.IsDefaultExplicit {
			return t.Apply(typeVar.Shared.DefaultType, recursionCount)
		}

		return UnknownTypeCreate(false)
	}

	if !t.shouldReplaceTypeVar(typeVar) {
		return nil
	}

	replacement := solutionSet.GetType(typeVar)

	// DIVERGENCE from the original, forced by a real hang. See
	// tests/samples/solverHigherOrder3.py, whose whole subject is a generic
	// function passed to itself.
	//
	// A solution set can map a TypeVar to a type that mentions that same TypeVar
	// at a strictly nested position. For `func2(func1, func2)`, T@func2's lower
	// bound is func1's signature, T@func1 solves to T@func2, and substituting the
	// dependent solutions in solveTypeVarRecursive produces
	// `T@func2 := (x: T@func2, y: U@func1) -> ...`.
	//
	// assignTypeVar applies an occurs check when *recording* a lower bound -- see
	// widenLowerBound and microsoft/pyright#11413 -- for exactly the reason stated
	// there: a cyclic constraint has no finite solution, and later substitution
	// rounds expand it into an exponentially growing type. That check cannot see
	// this cycle, because the cycle is created after the bound was recorded.
	//
	// Expanding such a solution here re-enters the same TypeVar at every nesting
	// level of the function it is substituted into. The recursion cap makes that
	// finite rather than infinite, which is why it presents as a hang: for this
	// one sample the port issues billions of Apply calls and never returns.
	// Refusing to expand a self-referential replacement restores the invariant the
	// occurs check is there to maintain -- no solution mentions the TypeVar it
	// solves -- and leaves the TypeVar in place, which is what the original ends up
	// producing for this file anyway.
	if replacement != nil && typeVarOccursIn(typeVar, replacement) {
		return nil
	}

	if replacement != nil {
		// No more processing is needed for ParamSpecs.
		if IsParamSpec(typeVar) {
			return replacement
		}

		if typeVar.IsInstantiable() {
			typeClass := t.options.TypeClassType
			if IsAnyOrUnknown(replacement) && typeClass != nil && IsInstantiableClass(typeClass) {
				replacement = ClassTypeSpecialize(
					ClassTypeCloneAsInstance(typeClass, true),
					[]Type{replacement},
					nil, false, nil, nil,
				)
			} else {
				replacement = ConvertToInstantiable(replacement, false)
			}
		} else {
			// If the TypeVar is not instantiable (i.e. not a type[T]), then it
			// represents an instance of a type. If the replacement includes a
			// generic class that has not been specialized, specialize it now
			// with default type arguments.
			replacement = MapSubtypes(replacement, func(subtype Type) Type {
				if cls, ok := AsClassInstance(subtype); ok {
					// If includeSubclasses wasn't set, force it to be set by
					// converting to/from an instantiable.
					if !cls.Priv.IncludeSubclasses {
						cls = ClassTypeCloneAsInstance(ClassTypeCloneAsInstantiable(cls, true), true)
						subtype = cls
					}

					// The original writes
					// `subtype.shared.typeParams && !subtype.priv.typeArgs`.
					// An empty array is truthy in JavaScript, and typeParams is
					// always an array, so the first operand is always true and
					// the condition reduces to the typeArgs test.
					if cls.Priv.TypeArgs == nil {
						if t.options.ReplaceUnsolved != nil {
							if t.options.ReplaceUnsolved.UseUnknown {
								return SpecializeWithUnknownTypeArgs(cls, t.options.ReplaceUnsolved.TupleClassType)
							}
							return SpecializeWithDefaultTypeArgs(cls)
						}
					}
				}

				return subtype
			}, nil)
		}

		if IsTypeVarTuple(replacement) && IsTypeVarTuple(typeVar) && typeVar.Priv.IsUnpacked {
			return TypeVarTypeCloneForUnpacked(replacement.(*TypeVarType), typeVar.Priv.IsInUnion)
		}

		if !IsTypeVarTuple(replacement) && IsTypeVar(replacement) && IsTypeVar(typeVar) && typeVar.Priv.IsUnpacked {
			return TypeVarTypeCloneForUnpacked(replacement.(*TypeVarType), false)
		}

		// If this isn't a TypeVarTuple, combine all of the tuple type args into
		// a common type.
		if !IsTypeVarTuple(typeVar) {
			if cls, ok := AsClassInstance(replacement); ok && cls.Priv.TupleTypeArgs != nil && cls.Priv.IsUnpacked {
				replacement = CombineTupleTypeArgs(cls.Priv.TupleTypeArgs)
			}
		}

		if IsUnpackedTypeVar(typeVar) {
			if cls, ok := AsClass(replacement); ok {
				replacement = ClassTypeCloneForUnpacked(cls)
			}
		}

		replacementTypeVar, isTypeVarReplacement := AsTypeVar(replacement)
		if !isTypeVarReplacement ||
			!TypeVarTypeIsUnification(replacementTypeVar) ||
			t.options.ReplaceUnsolved == nil {
			return replacement
		}
	}

	if !t.shouldReplaceUnsolvedTypeVar(typeVar) {
		return nil
	}

	// Use the default value if there is one.
	useUnknown := t.options.ReplaceUnsolved != nil && t.options.ReplaceUnsolved.UseUnknown
	if typeVar.Shared.IsDefaultExplicit && !useUnknown {
		return t.solveDefaultType(typeVar, recursionCount)
	}

	var tupleClassType *ClassType
	if t.options.ReplaceUnsolved != nil {
		tupleClassType = t.options.ReplaceUnsolved.TupleClassType
	}
	return GetUnknownForTypeVar(typeVar, tupleClassType)
}

func (t *applySolvedTypeVarsTransformer) TransformUnionSubtype(preTransform, postTransform Type, recursionCount int) Type {
	// If a union contains unsolved TypeVars within scope, eliminate them unless
	// this results in an empty union. This elimination is needed in cases where
	// TypeVars can go unsolved due to unions in parameter annotations, like
	// this:
	//   def test(x: Union[str, T]) -> Union[str, T]
	if t.options.ReplaceUnsolved == nil || !t.options.ReplaceUnsolved.EliminateUnsolvedInUnions {
		return postTransform
	}

	solutionSet := t.activeSolutionSet()

	if preTypeVar, ok := AsTypeVar(preTransform); ok {
		if !t.shouldReplaceTypeVar(preTypeVar) || !t.shouldReplaceUnsolvedTypeVar(preTypeVar) {
			return postTransform
		}

		typeVarType := solutionSet.GetType(preTypeVar)

		// Did the TypeVar remain unsolved?
		if typeVarType != nil {
			solvedTypeVar, isTypeVarSolution := AsTypeVar(typeVarType)
			if !isTypeVarSolution || !TypeVarTypeIsUnification(solvedTypeVar) {
				return postTransform
			}
		}

		// If the TypeVar was not transformed, then it was unsolved, and we'll
		// eliminate it.
		if preTransform == postTransform {
			return nil
		}

		// If useDefaultForUnsolved or useUnknownForUnsolved is true, the
		// postTransform type will be Unknown, which we want to eliminate.
		if t.options.ReplaceUnsolved != nil && IsUnknown(postTransform) {
			return nil
		}
	} else if preTransform.Base().Props != nil && preTransform.Base().Props.Condition != nil {
		// If this is a type that is conditioned on a unification TypeVar, see
		// if the TypeVar was solved. If not, eliminate the type.
		for _, condition := range preTransform.Base().Props.Condition {
			if TypeVarTypeIsUnification(condition.TypeVar) && solutionSet.GetType(condition.TypeVar) == nil {
				return nil
			}
		}
	}

	return postTransform
}

func (t *applySolvedTypeVarsTransformer) TransformTupleTypeVar(typeVar *TypeVarType, recursionCount int) []*TupleTypeArg {
	if !t.shouldReplaceTypeVar(typeVar) {
		defaultType := typeVar.Shared.DefaultType

		if typeVar.Shared.IsDefaultExplicit {
			if cls, ok := AsClassInstance(defaultType); ok && cls.Priv.TupleTypeArgs != nil {
				return cls.Priv.TupleTypeArgs
			}
		}

		return nil
	}

	solutionSet := t.activeSolutionSet()
	value := solutionSet.GetType(typeVar)
	if value != nil {
		if cls, ok := AsClassInstance(value); ok && cls.Priv.TupleTypeArgs != nil && IsUnpackedClass(value) {
			return cls.Priv.TupleTypeArgs
		}
	}
	return nil
}

func (t *applySolvedTypeVarsTransformer) TransformConditionalType(typ Type, recursionCount int) Type {
	if typ.Base().Props == nil || typ.Base().Props.Condition == nil {
		return typ
	}

	solutionSet := t.activeSolutionSet()

	for _, condition := range typ.Base().Props.Condition {
		// This doesn't apply to bound type variables.
		if !TypeVarTypeHasConstraints(condition.TypeVar) {
			continue
		}

		conditionTypeVar := condition.TypeVar
		if condition.TypeVar.Priv.FreeTypeVar != nil {
			conditionTypeVar = condition.TypeVar.Priv.FreeTypeVar
		}

		replacement := solutionSet.GetType(conditionTypeVar)
		if replacement == nil || condition.ConstraintIndex >= len(conditionTypeVar.Shared.Constraints) {
			continue
		}

		// The original reads the same value twice; the second read is
		// redundant but harmless.
		value := solutionSet.GetType(conditionTypeVar)
		if value == nil {
			continue
		}

		constraintType := conditionTypeVar.Shared.Constraints[condition.ConstraintIndex]

		// If this violates the constraint, substitute a Never type.
		if !IsTypeSame(constraintType, value, TypeSameOptions{}, 0) {
			return NeverTypeCreateNever()
		}
	}
	return typ
}

func (t *applySolvedTypeVarsTransformer) DoForEachConstraintSet(callback func() *FunctionType) Type {
	solutionSets := t.solution.GetSolutionSets()

	// Handle the common case where there are not multiple signature contexts.
	if len(solutionSets) <= 1 {
		return callback()
	}

	// Handle the case where we're already processing one of the signature
	// contexts and are called recursively. Don't loop over all the signature
	// contexts again.
	if t.activeConstraintSetIndex != nil {
		return callback()
	}

	// Loop through all of the signature contexts in the type var context to
	// create an overload type.
	overloadTypes := make([]Type, 0, len(solutionSets))
	for index := range solutionSets {
		i := index
		t.activeConstraintSetIndex = &i
		overloadTypes = append(overloadTypes, callback())
	}
	t.activeConstraintSetIndex = nil

	filteredOverloads := []*FunctionType{}
	DoForEachSubtype(CombineTypes(overloadTypes, nil), func(subtype Type, index int, allSubtypes []Type) {
		fn, ok := AsFunction(subtype)
		assert(ok, "")
		fn = FunctionTypeCloneWithNewFlags(fn, fn.Shared.Flags|FunctionTypeFlagsOverloaded)
		filteredOverloads = append(filteredOverloads, fn)
	})

	if len(filteredOverloads) == 1 {
		return filteredOverloads[0]
	}

	return OverloadedTypeCreate(filteredOverloads, nil)
}

// getReplacementForDefaultByName handles the case where we need the default
// replacement value for a typeVar that has no scope and therefore doesn't have
// an assigned scopeID. We look it up by name in the solution set.
//
// The original notes this is a bit hacky because there could be multiple
// typeVars with the same name, but assumes that this won't happen.
func (t *applySolvedTypeVarsTransformer) getReplacementForDefaultByName(
	typeVar *TypeVarType,
	solutionSet *ConstraintSolutionSet,
) Type {
	var replacementValue Type
	partialScopeID := typeVar.Shared.Name + "."

	solutionSet.DoForEachTypeVar(func(value Type, typeVarID string) {
		if strings.HasPrefix(typeVarID, partialScopeID) {
			replacementValue = value
		}
	})

	return replacementValue
}

func (t *applySolvedTypeVarsTransformer) shouldReplaceTypeVar(typeVar *TypeVarType) bool {
	if typeVar.Priv.ScopeID == "" || TypeVarTypeIsBound(typeVar) {
		return false
	}

	return true
}

func (t *applySolvedTypeVarsTransformer) shouldReplaceUnsolvedTypeVar(typeVar *TypeVarType) bool {
	// Never replace nested TypeVars with unknown.
	if t.PendingTypeVarTransformations().Size() > 0 {
		return false
	}

	if typeVar.Priv.ScopeID == "" {
		return false
	}

	if t.options.ReplaceUnsolved == nil {
		return false
	}

	found := false
	for _, id := range t.options.ReplaceUnsolved.ScopeIDs {
		if id == typeVar.Priv.ScopeID {
			found = true
			break
		}
	}
	if !found {
		return false
	}

	exemptTypeVars := t.options.ReplaceUnsolved.UnsolvedExemptTypeVars
	if exemptTypeVars != nil {
		for _, exempt := range exemptTypeVars {
			if IsTypeSame(exempt, typeVar, TypeSameOptions{IgnoreTypeFlags: true}, 0) {
				return false
			}
		}
	}

	return true
}

func (t *applySolvedTypeVarsTransformer) solveDefaultType(typeVar *TypeVarType, recursionCount int) Type {
	defaultType := typeVar.Shared.DefaultType
	wasSolvingDefaultType := t.isSolvingDefaultType
	t.isSolvingDefaultType = true
	result := t.Apply(defaultType, recursionCount)
	t.isSolvingDefaultType = wasSolvingDefaultType
	return result
}

// ---------------------------------------------------------------------------
// UnificationTypeTransformer
// ---------------------------------------------------------------------------

type unificationTypeTransformer struct {
	*TypeVarTransformer

	liveTypeVarScopes []TypeVarScopeId

	// usageOffset is 0 where the TypeScript has `undefined`; both are falsy in
	// the check inside cloneAsUnificationVar.
	usageOffset int
}

func newUnificationTypeTransformer(liveTypeVarScopes []TypeVarScopeId, usageOffset int) *unificationTypeTransformer {
	t := &unificationTypeTransformer{
		TypeVarTransformer: &TypeVarTransformer{},
		liveTypeVarScopes:  liveTypeVarScopes,
		usageOffset:        usageOffset,
	}
	InitTypeVarTransformer(t.TypeVarTransformer, t)
	return t
}

func (t *unificationTypeTransformer) TransformTypeVar(typeVar *TypeVarType, recursionCount int) Type {
	if !t.isTypeVarLive(typeVar) {
		return TypeVarTypeCloneAsUnificationVar(typeVar, t.usageOffset)
	}

	return nil
}

func (t *unificationTypeTransformer) isTypeVarLive(typeVar *TypeVarType) bool {
	for _, scopeID := range t.liveTypeVarScopes {
		if typeVar.Priv.ScopeID == scopeID {
			return true
		}
		if typeVar.Priv.FreeTypeVar != nil && typeVar.Priv.FreeTypeVar.Priv.ScopeID == scopeID {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// UniqueFunctionSignatureTransformer
// ---------------------------------------------------------------------------

type uniqueFunctionSignatureTransformer struct {
	*TypeVarTransformer

	signatureTracker *UniqueSignatureTracker
	expressionOffset int
}

func newUniqueFunctionSignatureTransformer(
	signatureTracker *UniqueSignatureTracker,
	expressionOffset int,
) *uniqueFunctionSignatureTransformer {
	t := &uniqueFunctionSignatureTransformer{
		TypeVarTransformer: &TypeVarTransformer{},
		signatureTracker:   signatureTracker,
		expressionOffset:   expressionOffset,
	}
	InitTypeVarTransformer(t.TypeVarTransformer, t)
	return t
}

// TransformGenericTypeAlias doesn't transform type aliases.
func (t *uniqueFunctionSignatureTransformer) TransformGenericTypeAlias(typ Type, recursionCount int) Type {
	return typ
}

// TransformTypeVarsInClassType doesn't transform classes.
func (t *uniqueFunctionSignatureTransformer) TransformTypeVarsInClassType(classType *ClassType, recursionCount int) Type {
	return classType
}

func (t *uniqueFunctionSignatureTransformer) TransformTypeVarsInFunctionType(
	sourceType *FunctionType,
	recursionCount int,
) Type {
	// If this function is not generic, there's no need to check for uniqueness.
	if len(sourceType.Shared.TypeParams) == 0 {
		return t.TypeVarTransformer.TransformTypeVarsInFunctionType(sourceType, recursionCount)
	}

	var updatedSourceType Type = sourceType
	existingSignature := t.signatureTracker.FindSignature(sourceType)
	if existingSignature != nil {
		offsetIndex := -1
		for i, offset := range existingSignature.ExpressionOffsets {
			if offset == t.expressionOffset {
				offsetIndex = i
				break
			}
		}
		if offsetIndex < 0 {
			offsetIndex = len(existingSignature.ExpressionOffsets)
		}

		if offsetIndex > 0 {
			solution := NewConstraintSolution(nil)

			// Create new type variables with the same scope but with different
			// (unique) names.
			for _, typeParam := range sourceType.Shared.TypeParams {
				if typeParam.Priv.ScopeType != nil && *typeParam.Priv.ScopeType == TypeVarScopeTypeFunction {
					replacement := TypeVarTypeCloneForNewName(
						typeParam,
						typeParam.Shared.Name+"("+strconv.Itoa(offsetIndex)+")",
					)

					solution.SetType(typeParam, replacement)
				}
			}

			updatedSourceType = ApplySolvedTypeVars(sourceType, solution, nil)
			assert(IsFunctionOrOverloaded(updatedSourceType), "")
		}
	}

	t.signatureTracker.AddSignature(sourceType, t.expressionOffset)

	return updatedSourceType
}

// ---------------------------------------------------------------------------
// Public entry points
// ---------------------------------------------------------------------------

// ApplySolvedTypeVars specializes a (potentially generic) type by substituting
// type variables from a solution. A nil options stands in for the TypeScript's
// `{}` default.
func ApplySolvedTypeVars(t Type, solution *ConstraintSolution, options *ApplyTypeVarOptions) Type {
	if options == nil {
		options = &ApplyTypeVarOptions{}
	}

	// Use a shortcut if constraints is empty and no transform is necessary.
	if solution.IsEmpty() && options.ReplaceUnsolved == nil {
		return t
	}

	transformer := newApplySolvedTypeVarsTransformer(solution, options)
	return transformer.Apply(t, 0)
}

// EnsureSignaturesAreUnique corresponds to ensureSignaturesAreUnique.
func EnsureSignaturesAreUnique(t Type, signatureTracker *UniqueSignatureTracker, expressionOffset int) Type {
	transformer := newUniqueFunctionSignatureTransformer(signatureTracker, expressionOffset)
	return transformer.Apply(t, 0)
}

// TransformExpectedType replaces TypeVars that are not part of the context of
// the class being constructed with "unification" type variables.
//
// The original notes: during bidirectional type inference for constructors, an
// "expected type" is used to prepopulate the type var map. This is problematic
// when the expected type uses TypeVars that are not part of the context of the
// class we are constructing.
//
// A zero usageOffset stands in for `undefined`.
func TransformExpectedType(expectedType Type, liveTypeVarScopes []TypeVarScopeId, usageOffset int) Type {
	transformer := newUnificationTypeTransformer(liveTypeVarScopes, usageOffset)
	return transformer.Apply(expectedType, 0)
}

// SpecializeWithDefaultTypeArgs corresponds to specializeWithDefaultTypeArgs.
func SpecializeWithDefaultTypeArgs(t *ClassType) *ClassType {
	if len(t.Shared.TypeParams) == 0 || t.Priv.TypeArgs != nil || t.Shared.TypeVarScopeID == "" {
		return t
	}

	solution := NewConstraintSolution(nil)

	return ApplySolvedTypeVars(t, solution, &ApplyTypeVarOptions{
		ReplaceUnsolved: &ReplaceUnsolvedOptions{
			ScopeIDs:       []TypeVarScopeId{t.Shared.TypeVarScopeID},
			TupleClassType: nil,
		},
	}).(*ClassType)
}

// SpecializeForBaseClass determines the specialized base class type that
// srcType derives from.
func SpecializeForBaseClass(srcType, baseClass *ClassType) *ClassType {
	typeParams := ClassTypeGetTypeParams(baseClass)

	// If there are no type parameters for the specified base class, no
	// specialization is required.
	if len(typeParams) == 0 {
		return baseClass
	}

	solution := BuildSolutionFromSpecializedClass(srcType)
	specializedType := ApplySolvedTypeVars(baseClass, solution, nil)
	assert(IsInstantiableClass(specializedType), "")
	return specializedType.(*ClassType)
}

// TransformPossibleRecursiveTypeAlias recursively transforms all top-level
// TypeVars that represent recursive type aliases into their actual types. The
// TypeScript defaults recursionCount to 0.
func TransformPossibleRecursiveTypeAlias(t Type, recursionCount int) Type {
	if recursionCount >= MaxTypeRecursionCount {
		return t
	}
	recursionCount++

	if t != nil {
		var aliasInfo *TypeAliasInfo
		if t.Base().Props != nil {
			aliasInfo = t.Base().Props.TypeAliasInfo
		}

		if tv, ok := AsTypeVar(t); ok &&
			tv.Shared.RecursiveAlias != nil &&
			tv.Shared.RecursiveAlias.Name != "" &&
			tv.Shared.BoundType != nil {
			unspecializedType := tv.Shared.BoundType
			if tv.IsInstance() {
				unspecializedType = ConvertToInstance(tv.Shared.BoundType, true)
			}

			if aliasInfo == nil || aliasInfo.TypeArgs == nil || tv.Shared.RecursiveAlias.TypeParams == nil {
				return TransformPossibleRecursiveTypeAlias(unspecializedType, recursionCount)
			}

			solution := BuildSolution(tv.Shared.RecursiveAlias.TypeParams, aliasInfo.TypeArgs)
			return TransformPossibleRecursiveTypeAlias(
				ApplySolvedTypeVars(unspecializedType, solution, nil),
				recursionCount,
			)
		}

		if union, ok := AsUnion(t); ok && union.Priv.IncludesRecursiveTypeAlias {
			newType := MapSubtypes(t, func(subtype Type) Type {
				return TransformPossibleRecursiveTypeAlias(subtype, recursionCount)
			}, nil)

			if newType != t && aliasInfo != nil {
				// Copy the type alias information if present.
				newType = CloneForTypeAlias(newType, aliasInfo)
			}

			return newType
		}
	}

	return t
}

// GetSpecializedTupleType determines whether the type derives from tuple. If
// so, it returns the specialized tuple type; otherwise nil.
func GetSpecializedTupleType(t Type) *ClassType {
	var classType *ClassType

	if cls, ok := AsInstantiableClass(t); ok {
		classType = cls
	} else if cls, ok := AsClassInstance(t); ok {
		classType = ClassTypeCloneAsInstantiable(cls, true)
	}

	if classType == nil {
		return nil
	}

	// See if this class derives from Tuple or tuple. If it does, we'll assume
	// that it hasn't been overridden in a way that changes the behavior of the
	// tuple class.
	var tupleClass *ClassType
	for _, mroClass := range classType.Shared.Mro {
		if cls, ok := AsInstantiableClass(mroClass); ok && IsTupleClass(cls) {
			tupleClass = cls
			break
		}
	}
	if tupleClass == nil {
		return nil
	}

	if ClassTypeIsSameGenericClass(classType, tupleClass, 0) {
		return classType
	}

	solution := BuildSolutionFromSpecializedClass(classType)
	return ApplySolvedTypeVars(tupleClass, solution, nil).(*ClassType)
}
