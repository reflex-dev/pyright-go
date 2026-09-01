/*
 * typeutils_transform.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * TypeVarTransformer: recursively walks a type and calls a callback for each
 * TypeVar, allowing it to be replaced with something else.
 *
 * Transliterated from analyzer/typeUtils.ts (pyright 1.1.412), lines
 * 3469-4041. See the header of typeutils.go for the file split.
 *
 * Subclassing works the same way as TypeWalker: the base carries a `self`
 * pointer to the concrete transformer, set by InitTypeVarTransformer, and every
 * internal call goes through it so a subclass's override wins. The difference
 * from typewalker.go is that the default implementations live directly on
 * *TypeVarTransformer rather than in a separate Defaults struct -- a subclass
 * embeds *TypeVarTransformer, gets the defaults by promotion, shadows the ones
 * it overrides, and calls "super" as t.TypeVarTransformer.TransformX(...).
 */

package analyzer

import (
	"strconv"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// TypeVarTransformerOverrides is the set of methods a TypeVarTransformer
// subclass may override. *TypeVarTransformer implements all of them.
type TypeVarTransformerOverrides interface {
	Apply(t Type, recursionCount int) Type
	CanSkipTransform(t Type) bool
	TransformTypeVar(typeVar *TypeVarType, recursionCount int) Type
	TransformTupleTypeVar(paramSpec *TypeVarType, recursionCount int) []*TupleTypeArg
	TransformUnionSubtype(preTransform, postTransform Type, recursionCount int) Type
	DoForEachConstraintSet(callback func() *FunctionType) Type
	TransformGenericTypeAlias(t Type, recursionCount int) Type
	TransformConditionalType(t Type, recursionCount int) Type
	TransformTypeVarsInClassType(classType *ClassType, recursionCount int) Type
	TransformTypeVarsInFunctionType(sourceType *FunctionType, recursionCount int) Type
}

// TypeVarTransformer corresponds to the class of the same name.
type TypeVarTransformer struct {
	self TypeVarTransformerOverrides

	pendingTypeVarTransformations *common.OrderedSet[TypeVarScopeId]

	// pendingFunctionTransformations holds FunctionType and OverloadedType
	// values, compared by identity.
	pendingFunctionTransformations []Type
}

// InitTypeVarTransformer wires the base to the concrete transformer. Every
// constructor must call it, passing the outermost embedder.
func InitTypeVarTransformer(t *TypeVarTransformer, self TypeVarTransformerOverrides) {
	t.self = self
	t.pendingTypeVarTransformations = common.NewOrderedSet[TypeVarScopeId]()
	t.pendingFunctionTransformations = []Type{}
}

// NewTypeVarTransformer returns a transformer with no overrides, which is what
// the bare `new TypeVarTransformer()` gives.
func NewTypeVarTransformer() *TypeVarTransformer {
	t := &TypeVarTransformer{}
	InitTypeVarTransformer(t, t)
	return t
}

// PendingTypeVarTransformations corresponds to the getter of the same name.
func (t *TypeVarTransformer) PendingTypeVarTransformations() *common.OrderedSet[TypeVarScopeId] {
	return t.pendingTypeVarTransformations
}

// Apply corresponds to TypeVarTransformer.apply.
func (t *TypeVarTransformer) Apply(typ Type, recursionCount int) Type {
	if recursionCount > MaxTypeRecursionCount {
		return typ
	}
	recursionCount++

	typ = t.self.TransformGenericTypeAlias(typ, recursionCount)

	// If the type is conditioned on a type variable, see if the condition still
	// applies.
	if typ.Base().Props != nil && typ.Base().Props.Condition != nil {
		typ = t.self.TransformConditionalType(typ, recursionCount)
	}

	// Shortcut the operation if possible.
	if t.self.CanSkipTransform(typ) {
		return typ
	}

	if IsAnyOrUnknown(typ) {
		return typ
	}

	if IsNoneInstance(typ) {
		return typ
	}

	if typeVar, ok := AsTypeVar(typ); ok {
		// Handle recursive type aliases specially. In particular, we need to
		// specialize type arguments for generic recursive type aliases.
		var aliasInfo *TypeAliasInfo
		if typeVar.Props != nil {
			aliasInfo = typeVar.Props.TypeAliasInfo
		}

		if typeVar.Shared.RecursiveAlias != nil {
			if aliasInfo == nil || aliasInfo.TypeArgs == nil {
				return typ
			}

			requiresUpdate := false
			typeArgs := make([]Type, 0, len(aliasInfo.TypeArgs))
			for _, typeArg := range aliasInfo.TypeArgs {
				replacementType := t.self.Apply(typeArg, recursionCount)
				if replacementType != typeArg {
					requiresUpdate = true
				}
				typeArgs = append(typeArgs, replacementType)
			}

			if requiresUpdate {
				newAliasInfo := *aliasInfo
				newAliasInfo.TypeArgs = typeArgs
				return CloneForTypeAlias(typ, &newAliasInfo)
			}

			return typ
		}

		var replacementType Type = typ

		// Recursively transform the results, but ensure that we don't replace
		// any type variables in the same scope recursively by setting the scope
		// in the pendingTypeVarTransformations set.
		if !t.isTypeVarScopePending(typeVar.Priv.ScopeID) {
			paramSpecAccess := ParamSpecAccessNone

			// If this is a ParamSpec with a ".args" or ".kwargs" access, strip
			// it off for now. We'll add it back later if appropriate.
			if IsParamSpec(typeVar) && typeVar.Priv.ParamSpecAccess != ParamSpecAccessNone {
				paramSpecAccess = typeVar.Priv.ParamSpecAccess
				typeVar = TypeVarTypeCloneForParamSpecAccess(typeVar, ParamSpecAccessNone)
				typ = typeVar
			}

			replacementType = t.self.TransformTypeVar(typeVar, recursionCount)
			if replacementType == nil {
				replacementType = typ
			}

			if IsParamSpec(typeVar) && replacementType != typ {
				replacementType = SimplifyFunctionToParamSpec(ConvertTypeToParamSpecValue(replacementType))
			}

			// If the original type was a ParamSpec with a ".args" or ".kwargs"
			// access, preserve that information in the transformed type.
			if paramSpecAccess != ParamSpecAccessNone {
				if ps, ok := AsParamSpec(replacementType); ok {
					replacementType = TypeVarTypeCloneForParamSpecAccess(ps, paramSpecAccess)
				} else {
					replacementType = UnknownTypeCreate(false)
				}
			}

			// If we're transforming a TypeVarTuple that was in a union, expand
			// the union types.
			if IsTypeVarTuple(typeVar) && typeVar.Priv.IsInUnion {
				replacementType = expandUnpackedTypeVarTupleUnion(replacementType)
			}

			if typeVar.Priv.ScopeID != "" {
				t.pendingTypeVarTransformations.Add(typeVar.Priv.ScopeID)
				replacementType = t.self.Apply(replacementType, recursionCount)
				t.pendingTypeVarTransformations.Delete(typeVar.Priv.ScopeID)
			}
		}

		return replacementType
	}

	if IsUnion(typ) {
		newUnionType := MapSubtypes(typ, func(subtype Type) Type {
			transformedType := t.self.Apply(subtype, recursionCount)

			// If we're transforming a TypeVarTuple within a union, combine the
			// individual types within the TypeVarTuple.
			if IsTypeVarTuple(subtype) && !IsTypeVarTuple(transformedType) {
				subtypesToCombine := []Type{}
				DoForEachSubtype(transformedType, func(transformedSubtype Type, index int, allSubtypes []Type) {
					subtypesToCombine = append(subtypesToCombine, expandUnpackedTypeVarTupleUnion(transformedSubtype))
				})

				transformedType = CombineTypes(subtypesToCombine, nil)
			}

			// The original guards this with `if (this.transformUnionSubtype)`,
			// which is always true because the base class defines the method.
			return t.self.TransformUnionSubtype(subtype, transformedType, recursionCount)
		}, &MapSubtypesOptions{RetainTypeAlias: true})

		if !IsNever(newUnionType) {
			return newUnionType
		}
		return UnknownTypeCreate(false)
	}

	if cls, ok := AsClass(typ); ok {
		return t.self.TransformTypeVarsInClassType(cls, recursionCount)
	}

	if fn, ok := AsFunction(typ); ok {
		// Prevent recursion.
		for _, pending := range t.pendingFunctionTransformations {
			if pending == Type(fn) {
				return typ
			}
		}

		t.pendingFunctionTransformations = append(t.pendingFunctionTransformations, fn)
		result := t.self.TransformTypeVarsInFunctionType(fn, recursionCount)
		t.pendingFunctionTransformations = t.pendingFunctionTransformations[:len(t.pendingFunctionTransformations)-1]

		return result
	}

	if overloaded, ok := AsOverloaded(typ); ok {
		// Prevent recursion.
		for _, pending := range t.pendingFunctionTransformations {
			if pending == Type(overloaded) {
				return typ
			}
		}

		t.pendingFunctionTransformations = append(t.pendingFunctionTransformations, overloaded)

		requiresUpdate := false

		// Specialize each of the functions in the overload.
		overloads := OverloadedTypeGetOverloads(overloaded)
		newOverloads := []*FunctionType{}

		for _, entry := range overloads {
			replacementType := t.self.TransformTypeVarsInFunctionType(entry, recursionCount)

			if replacementFn, ok := AsFunction(replacementType); ok {
				newOverloads = append(newOverloads, replacementFn)
			} else {
				newOverloads = common.AppendArray(newOverloads,
					OverloadedTypeGetOverloads(replacementType.(*OverloadedType)))
			}

			if replacementType != Type(entry) {
				requiresUpdate = true
			}
		}

		impl := OverloadedTypeGetImplementation(overloaded)
		newImpl := impl

		if impl != nil {
			newImpl = t.self.Apply(impl, recursionCount)

			if newImpl != impl {
				requiresUpdate = true
			}
		}

		t.pendingFunctionTransformations = t.pendingFunctionTransformations[:len(t.pendingFunctionTransformations)-1]

		// Construct a new overload with the specialized function types.
		if requiresUpdate {
			return OverloadedTypeCreate(newOverloads, newImpl)
		}
		return typ
	}

	return typ
}

// CanSkipTransform corresponds to TypeVarTransformer.canSkipTransform.
func (t *TypeVarTransformer) CanSkipTransform(typ Type) bool {
	return !RequiresSpecialization(typ, nil, 0)
}

// TransformTypeVar corresponds to TypeVarTransformer.transformTypeVar. The base
// implementation returns nil, standing in for `undefined`.
func (t *TypeVarTransformer) TransformTypeVar(typeVar *TypeVarType, recursionCount int) Type {
	return nil
}

// TransformTupleTypeVar corresponds to
// TypeVarTransformer.transformTupleTypeVar.
func (t *TypeVarTransformer) TransformTupleTypeVar(paramSpec *TypeVarType, recursionCount int) []*TupleTypeArg {
	return nil
}

// TransformUnionSubtype corresponds to
// TypeVarTransformer.transformUnionSubtype.
func (t *TypeVarTransformer) TransformUnionSubtype(preTransform, postTransform Type, recursionCount int) Type {
	return postTransform
}

// DoForEachConstraintSet corresponds to
// TypeVarTransformer.doForEachConstraintSet. By default it simply returns the
// result of the callback; subclasses override it as they see fit.
func (t *TypeVarTransformer) DoForEachConstraintSet(callback func() *FunctionType) Type {
	return callback()
}

// TransformGenericTypeAlias corresponds to
// TypeVarTransformer.transformGenericTypeAlias.
//
// Note that the requiresUpdate test compares `type !== updatedType` -- the
// whole type against each transformed type argument -- rather than
// `typeArg !== updatedType`. Since a type argument is essentially never the
// containing type, this is true whenever there is at least one type argument.
// Reproduced as written. See ../UPSTREAM-BUGS.md #7.
func (t *TypeVarTransformer) TransformGenericTypeAlias(typ Type, recursionCount int) Type {
	var aliasInfo *TypeAliasInfo
	if typ.Base().Props != nil {
		aliasInfo = typ.Base().Props.TypeAliasInfo
	}
	if aliasInfo == nil || aliasInfo.Shared.TypeParams == nil || aliasInfo.TypeArgs == nil {
		return typ
	}

	requiresUpdate := false
	newTypeArgs := make([]Type, 0, len(aliasInfo.TypeArgs))
	for _, typeArg := range aliasInfo.TypeArgs {
		updatedType := t.self.Apply(typeArg, recursionCount)
		if typ != updatedType {
			requiresUpdate = true
		}
		newTypeArgs = append(newTypeArgs, updatedType)
	}

	if requiresUpdate {
		newAliasInfo := *aliasInfo
		newAliasInfo.TypeArgs = newTypeArgs
		return CloneForTypeAlias(typ, &newAliasInfo)
	}
	return typ
}

// TransformConditionalType corresponds to
// TypeVarTransformer.transformConditionalType. By default it performs no
// transform.
func (t *TypeVarTransformer) TransformConditionalType(typ Type, recursionCount int) Type {
	return typ
}

// TransformTypeVarsInClassType corresponds to
// TypeVarTransformer.transformTypeVarsInClassType.
func (t *TypeVarTransformer) TransformTypeVarsInClassType(classType *ClassType, recursionCount int) Type {
	typeParams := ClassTypeGetTypeParams(classType)

	// Handle the common case where the class has no type parameters.
	if len(typeParams) == 0 &&
		!ClassTypeIsSpecialBuiltIn(classType) &&
		!ClassTypeIsBuiltInNamed(classType, "type") {
		return classType
	}

	var newTypeArgs []Type
	var newTupleTypeArgs []*TupleTypeArg
	specializationNeeded := false
	isTypeArgExplicit := true

	// If type args were previously provided, specialize them.

	// Handle tuples specially.
	if ClassTypeIsTupleClass(classType) {
		// As a performance safeguard, bail out early on very deeply nested
		// tuples (the recursion count limit would eventually stop us, but
		// constructing such deep types is expensive). Only do this when there
		// are no type variables left to substitute; bailing out while type
		// variables remain would return the unspecialized class and let those
		// TypeVars "escape" unsolved (see microsoft/pyright#11472).
		if GetContainerDepth(classType, 0) > maxTupleTypeArgRecursionDepth &&
			!RequiresSpecialization(classType, nil, 0) {
			return classType
		}

		if classType.Priv.TupleTypeArgs != nil {
			newTupleTypeArgs = []*TupleTypeArg{}

			for _, oldTypeArgType := range classType.Priv.TupleTypeArgs {
				newTypeArgType := t.self.Apply(oldTypeArgType.Type, recursionCount)

				if newTypeArgType != oldTypeArgType.Type {
					specializationNeeded = true
				}

				newCls, newIsClassInstance := AsClassInstance(newTypeArgType)

				if IsUnpackedTypeVarTuple(oldTypeArgType.Type) &&
					newIsClassInstance &&
					IsTupleClass(newCls) &&
					newCls.Priv.TupleTypeArgs != nil {
					newTupleTypeArgs = common.AppendArray(newTupleTypeArgs, newCls.Priv.TupleTypeArgs)
				} else if IsUnpackedClass(newTypeArgType) && newTypeArgType.(*ClassType).Priv.TupleTypeArgs != nil {
					newTupleTypeArgs = common.AppendArray(newTupleTypeArgs, newTypeArgType.(*ClassType).Priv.TupleTypeArgs)
				} else {
					// Handle the special case where tuple[T, ...] is being
					// specialized to tuple[Never, ...]. This is equivalent to
					// tuple[()].
					isEmptyTuple := oldTypeArgType.IsUnbounded &&
						IsTypeVar(oldTypeArgType.Type) &&
						IsNever(newTypeArgType) &&
						len(classType.Priv.TupleTypeArgs) == 1

					if !isEmptyTuple {
						newTupleTypeArgs = append(newTupleTypeArgs, &TupleTypeArg{
							Type:        newTypeArgType,
							IsUnbounded: oldTypeArgType.IsUnbounded,
							IsOptional:  oldTypeArgType.IsOptional,
						})
					}
				}
			}
		} else if len(typeParams) > 0 {
			newTupleTypeArgs = t.self.TransformTupleTypeVar(typeParams[0], recursionCount)
			if newTupleTypeArgs != nil {
				specializationNeeded = true
			} else {
				newTypeArgType := t.self.Apply(typeParams[0], recursionCount)
				newTupleTypeArgs = []*TupleTypeArg{{Type: newTypeArgType, IsUnbounded: true}}

				// If this is the literal "tuple" class (as opposed to a type
				// that represents all subtypes of tuple), don't specialize if
				// the type arg is the same as the type param. This is the same
				// thing we do with non-tuple classes below.
				if newTypeArgType != Type(typeParams[0]) || classType.Priv.IncludeSubclasses {
					specializationNeeded = true
				}
				isTypeArgExplicit = false
			}
		}

		// If this is an empty tuple, don't recompute the non-tuple type
		// argument.
		if len(newTupleTypeArgs) > 0 {
			// Combine the tuple type args into a single non-tuple type
			// argument.
			newTypeArgs = []Type{CombineTupleTypeArgs(newTupleTypeArgs)}
		}
	}

	if newTypeArgs == nil {
		typeArgs := classType.Priv.TypeArgs
		if typeArgs == nil {
			typeArgs = make([]Type, 0, len(typeParams))
			for _, tp := range typeParams {
				typeArgs = append(typeArgs, tp)
			}
			isTypeArgExplicit = false
		}

		newTypeArgs = make([]Type, 0, len(typeArgs))
		for _, oldTypeArgType := range typeArgs {
			newTypeArgType := t.self.Apply(oldTypeArgType, recursionCount)
			if newTypeArgType != oldTypeArgType {
				specializationNeeded = true

				// If this was a TypeVarTuple that was part of a union (e.g.
				// Union[Unpack[Vs]]), expand the subtypes into a union here.
				if tv, ok := AsTypeVar(oldTypeArgType); ok && IsTypeVarTuple(oldTypeArgType) && tv.Priv.IsInUnion {
					newTypeArgType = expandUnpackedTypeVarTupleUnion(newTypeArgType)
				}
			}
			newTypeArgs = append(newTypeArgs, newTypeArgType)
		}
	}

	// If specialization wasn't needed, don't allocate a new class.
	if !specializationNeeded {
		return classType
	}

	return ClassTypeSpecialize(
		classType,
		newTypeArgs,
		&isTypeArgExplicit,
		false, // includeSubclasses is undefined in the original, i.e. falsy
		newTupleTypeArgs,
		nil,
	)
}

// TransformTypeVarsInFunctionType corresponds to
// TypeVarTransformer.transformTypeVarsInFunctionType. It returns a FunctionType
// or an OverloadedType.
func (t *TypeVarTransformer) TransformTypeVarsInFunctionType(sourceType *FunctionType, recursionCount int) Type {
	return t.self.DoForEachConstraintSet(func() *FunctionType {
		functionType := sourceType

		declaredReturnType := FunctionTypeGetEffectiveReturnType(functionType, true)
		var specializedReturnType Type
		if declaredReturnType != nil {
			specializedReturnType = t.self.Apply(declaredReturnType, recursionCount)
		}
		typesRequiredSpecialization := declaredReturnType != specializedReturnType

		specializedParams := &SpecializedFunctionTypes{
			ParameterTypes: []Type{},
			ReturnType:     specializedReturnType,
		}

		paramSpec := FunctionTypeGetParamSpecFromArgsKwargs(functionType)

		if paramSpec != nil {
			paramSpecType := t.self.TransformTypeVar(paramSpec, recursionCount)
			if paramSpecType != nil {
				paramSpecValue := ConvertTypeToParamSpecValue(paramSpecType)
				transformedParamSpec := FunctionTypeGetParamSpecFromArgsKwargs(paramSpecValue)

				if len(paramSpecValue.Shared.Parameters) > 0 ||
					transformedParamSpec == nil ||
					!IsTypeSame(paramSpec, transformedParamSpec, TypeSameOptions{}, 0) {
					functionType = FunctionTypeApplyParamSpecValue(functionType, paramSpecValue)
				}
			}
		}

		variadicParamIndex := -1
		var variadicTypesToUnpack []*TupleTypeArg
		specializedDefaultArgs := []Type{}

		for i := range functionType.Shared.Parameters {
			paramType := FunctionTypeGetParamType(functionType, i)
			specializedType := t.self.Apply(paramType, recursionCount)
			specializedParams.ParameterTypes = append(specializedParams.ParameterTypes, specializedType)

			// Do we need to specialize the default argument type for this
			// parameter?
			defaultArgType := FunctionTypeGetParamDefaultType(functionType, i)
			if defaultArgType != nil {
				specializedArgType := t.self.Apply(defaultArgType, recursionCount)
				if specializedArgType != defaultArgType {
					defaultArgType = specializedArgType
					typesRequiredSpecialization = true
				}
			}
			specializedDefaultArgs = append(specializedDefaultArgs, defaultArgType)

			if variadicParamIndex < 0 &&
				IsTypeVarTuple(paramType) &&
				functionType.Shared.Parameters[i].Category == parser.ParamCategoryArgsList {
				variadicParamIndex = i

				if cls, ok := AsClassInstance(specializedType); ok && IsTupleClass(cls) && cls.Priv.IsUnpacked {
					variadicTypesToUnpack = cls.Priv.TupleTypeArgs
				}
			}

			if paramType != specializedType {
				typesRequiredSpecialization = true
			}
		}

		if functionType.Shared.InferredReturnType != nil {
			specializedInferredReturnType := t.self.Apply(functionType.Shared.InferredReturnType.Type, recursionCount)
			if specializedInferredReturnType != functionType.Shared.InferredReturnType.Type {
				specializedParams.ReturnType = specializedInferredReturnType
				typesRequiredSpecialization = true
			}
		}

		// Do we need to update the boundToType?
		if functionType.Priv.BoundToType != nil {
			newBoundToType := t.self.Apply(functionType.Priv.BoundToType, recursionCount)
			if newBoundToType != Type(functionType.Priv.BoundToType) {
				if newBoundCls, ok := AsClass(newBoundToType); ok {
					functionType = FunctionTypeClone(functionType, false, newBoundCls)
				}
			}
		}

		// Do we need to update the strippedFirstParamType?
		if functionType.Priv.StrippedFirstParamType != nil && !IsAnyOrUnknown(functionType.Priv.StrippedFirstParamType) {
			newStrippedType := t.self.Apply(functionType.Priv.StrippedFirstParamType, recursionCount)
			if newStrippedType != functionType.Priv.StrippedFirstParamType {
				functionType = CloneType(functionType)
				functionType.Priv.StrippedFirstParamType = newStrippedType
			}
		}

		if !typesRequiredSpecialization {
			return functionType
		}

		for _, d := range specializedDefaultArgs {
			if d != nil {
				specializedParams.ParameterDefaultTypes = specializedDefaultArgs
				break
			}
		}

		// If there was no unpacked variadic type variable, we're done.
		if variadicTypesToUnpack == nil {
			return FunctionTypeSpecialize(functionType, specializedParams)
		}

		// Unpack the tuple and synthesize a new function in the process.
		var newFunctionType *FunctionType
		if functionType.IsInstantiable() {
			newFunctionType = FunctionTypeCreateInstantiable(
				functionType.Shared.Flags|FunctionTypeFlagsSynthesizedMethod, nil)
		} else {
			newFunctionType = FunctionTypeCreateSynthesizedInstance("", functionType.Shared.Flags)
		}
		insertKeywordOnlySeparator := false
		swallowPositionOnlySeparator := false

		for index, paramType := range specializedParams.ParameterTypes {
			if index == variadicParamIndex {
				sawUnboundedEntry := false

				// Unpack the tuple into individual parameters.
				for _, unpackedType := range variadicTypesToUnpack {
					category := parser.ParamCategorySimple
					if unpackedType.IsUnbounded || IsTypeVarTuple(unpackedType.Type) {
						category = parser.ParamCategoryArgsList
					}

					name := "__p" + strconv.Itoa(len(newFunctionType.Shared.Parameters))
					FunctionTypeAddParam(newFunctionType, FunctionParamCreate(
						category,
						unpackedType.Type,
						FunctionParamFlagsNameSynthesized|FunctionParamFlagsTypeDeclared,
						&name,
						nil,
						nil,
					))

					if unpackedType.IsUnbounded {
						sawUnboundedEntry = true
					}
				}

				if sawUnboundedEntry {
					swallowPositionOnlySeparator = true
				} else {
					insertKeywordOnlySeparator = true
				}
			} else {
				param := functionType.Shared.Parameters[index]

				if IsKeywordOnlySeparator(param) {
					insertKeywordOnlySeparator = false
				} else if param.Category == parser.ParamCategoryKwargsDict {
					insertKeywordOnlySeparator = false
				}

				// Insert a keyword-only separator parameter if we previously
				// unpacked a TypeVarTuple.
				hasName := param.Name != nil && *param.Name != ""
				if param.Category == parser.ParamCategorySimple && hasName && insertKeywordOnlySeparator {
					FunctionTypeAddKeywordOnlyParamSeparator(newFunctionType)
					insertKeywordOnlySeparator = false
				}

				if param.Category != parser.ParamCategorySimple || hasName || !swallowPositionOnlySeparator {
					paramName := param.Name
					if hasName && FunctionParamIsNameSynthesized(param) {
						synthesized := "__p" + strconv.Itoa(len(newFunctionType.Shared.Parameters))
						paramName = &synthesized
					}

					FunctionTypeAddParam(newFunctionType, FunctionParamCreate(
						param.Category,
						paramType,
						param.Flags,
						paramName,
						FunctionTypeGetParamDefaultType(functionType, index),
						param.DefaultExpr,
					))
				}
			}
		}

		newFunctionType.Shared.DeclaredReturnType = specializedParams.ReturnType

		return newFunctionType
	})
}

// isTypeVarScopePending corresponds to the private _isTypeVarScopePending.
func (t *TypeVarTransformer) isTypeVarScopePending(typeVarScopeID TypeVarScopeId) bool {
	return typeVarScopeID != "" && t.pendingTypeVarTransformations.Has(typeVarScopeID)
}
