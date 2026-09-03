/*
 * typeutils_requires.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * requiresSpecialization and the pack/unpack helpers from
 * analyzer/typeUtils.ts (pyright 1.1.412). See the header of typeutils.go for
 * the file split.
 *
 * These are ported ahead of TypeVarTransformer because that class calls
 * requiresSpecialization from canSkipTransform and
 * _expandUnpackedTypeVarTupleUnion from its union handling.
 */

package analyzer

// expandUnpackedTypeVarTupleUnion corresponds to the unexported
// _expandUnpackedTypeVarTupleUnion.
func expandUnpackedTypeVarTupleUnion(t Type) Type {
	if cls, ok := AsClassInstance(t); ok && IsTupleClass(cls) && cls.Priv.TupleTypeArgs != nil && cls.Priv.IsUnpacked {
		types := make([]Type, 0, len(cls.Priv.TupleTypeArgs))
		for _, arg := range cls.Priv.TupleTypeArgs {
			types = append(types, arg.Type)
		}
		return CombineTypes(types, nil)
	}

	return t
}

// MakePacked returns the type as no longer unpacked, if it is an unpacked type.
func MakePacked(t Type) Type {
	if IsUnpackedClass(t) {
		return ClassTypeCloneForPacked(t.(*ClassType))
	}

	if tv, ok := AsTypeVarTuple(t); ok && IsUnpackedTypeVarTuple(t) && !tv.Priv.IsInUnion {
		return TypeVarTypeCloneForPacked(tv)
	}

	if IsUnpackedTypeVar(t) {
		return TypeVarTypeCloneForPacked(t.(*TypeVarType))
	}

	return t
}

// MakeUnpacked corresponds to makeUnpacked.
func MakeUnpacked(t Type) Type {
	if cls, ok := AsClass(t); ok {
		return ClassTypeCloneForUnpacked(cls)
	}

	if tv, ok := AsTypeVarTuple(t); ok && !tv.Priv.IsInUnion {
		return TypeVarTypeCloneForUnpacked(tv, false)
	}

	if tv, ok := AsTypeVar(t); ok {
		return TypeVarTypeCloneForUnpacked(tv, false)
	}

	return t
}

// RequiresTypeArgs corresponds to requiresTypeArgs.
func RequiresTypeArgs(classType *ClassType) bool {
	if len(classType.Shared.TypeParams) > 0 {
		firstTypeParam := classType.Shared.TypeParams[0]

		// If there are type parameters, type arguments are needed. The
		// exception is if type parameters have been synthesized for classes
		// that have untyped constructors.
		if firstTypeParam.Shared.IsSynthesized {
			return false
		}

		// If the first type parameter has a default type, then no type
		// arguments are needed.
		if firstTypeParam.Shared.IsDefaultExplicit {
			return false
		}

		return true
	}

	// There are a few built-in special classes that require type arguments even
	// though typeParams is empty.
	if ClassTypeIsSpecialBuiltIn(classType) {
		specialClasses := []string{
			"Tuple",
			"Callable",
			"Generic",
			"Type",
			"Optional",
			"Union",
			"Literal",
			"Annotated",
			"TypeGuard",
			"TypeIs",
		}

		name := classType.Shared.Name
		if classType.Priv.AliasName() != nil && *classType.Priv.AliasName() != "" {
			name = *classType.Priv.AliasName()
		}

		for _, t := range specialClasses {
			if t == name {
				return true
			}
		}
	}

	return false
}

// RequiresSpecialization corresponds to requiresSpecialization. A nil options
// stands in for the omitted argument; the TypeScript defaults recursionCount
// to 0.
//
// Note that this memoizes onto the *original* type's cached slot, mutating it.
func RequiresSpecialization(t Type, options *RequiresSpecializationOptions, recursionCount int) bool {
	if recursionCount > MaxTypeRecursionCount {
		return false
	}
	recursionCount++

	// Is the answer cached?
	canUseCache := options == nil || (!options.IgnorePseudoGeneric && !options.IgnoreSelf)
	if canUseCache && t.Base().Cached != nil && t.Base().Cached.RequiresSpecialization != nil {
		return *t.Base().Cached.RequiresSpecialization
	}

	result := requiresSpecializationImpl(t, options, recursionCount)

	if canUseCache {
		if t.Base().Cached == nil {
			t.Base().Cached = &CachedTypeInfo{}
		}
		t.Base().Cached.RequiresSpecialization = &result
	}

	return result
}

// requiresSpecializationImpl corresponds to the unexported
// _requiresSpecialization.
//
// Note that the original declares a recursionCount parameter defaulting to 0
// but never increments it here; every recursive call goes back through
// requiresSpecialization, which does the incrementing.
func requiresSpecializationImpl(t Type, options *RequiresSpecializationOptions, recursionCount int) bool {
	// If the type is conditioned on a TypeVar, it may need to be specialized.
	if t.Base().Props != nil && t.Base().Props.Condition != nil {
		return true
	}

	switch t.Base().Category {
	case TypeCategoryClass:
		cls := t.(*ClassType)

		if ClassTypeIsPseudoGenericClass(cls) && options != nil && options.IgnorePseudoGeneric {
			return false
		}

		isTypeArgExplicit := cls.Priv.IsTypeArgExplicit != nil && *cls.Priv.IsTypeArgExplicit
		if !isTypeArgExplicit && options != nil && options.IgnoreImplicitTypeArgs {
			return false
		}

		if cls.Priv.TupleTypeArgs != nil {
			for _, typeArg := range cls.Priv.TupleTypeArgs {
				if RequiresSpecialization(typeArg.Type, options, recursionCount) {
					return true
				}
			}
		}

		if cls.Priv.TypeArgs != nil {
			for _, typeArg := range cls.Priv.TypeArgs {
				if RequiresSpecialization(typeArg, options, recursionCount) {
					return true
				}
			}
			return false
		}

		return len(ClassTypeGetTypeParams(cls)) > 0

	case TypeCategoryFunction:
		fn := t.(*FunctionType)

		for i := range fn.Shared.Parameters {
			if RequiresSpecialization(FunctionTypeGetParamType(fn, i), options, recursionCount) {
				return true
			}
		}

		declaredReturnType := fn.Shared.DeclaredReturnType
		if fn.Priv.SpecializedTypes != nil && fn.Priv.SpecializedTypes.ReturnType != nil {
			declaredReturnType = fn.Priv.SpecializedTypes.ReturnType
		}
		if declaredReturnType != nil {
			if RequiresSpecialization(declaredReturnType, options, recursionCount) {
				return true
			}
		} else if fn.Shared.InferredReturnType != nil {
			if RequiresSpecialization(fn.Shared.InferredReturnType.Type, options, recursionCount) {
				return true
			}
		}

		return false

	case TypeCategoryOverloaded:
		overloaded := t.(*OverloadedType)

		for _, overload := range OverloadedTypeGetOverloads(overloaded) {
			if RequiresSpecialization(overload, options, recursionCount) {
				return true
			}
		}

		impl := OverloadedTypeGetImplementation(overloaded)
		if impl != nil {
			return RequiresSpecialization(impl, options, recursionCount)
		}

		return false

	case TypeCategoryUnion:
		union := t.(*UnionType)
		for _, subtype := range union.Priv.Subtypes {
			if RequiresSpecialization(subtype, options, recursionCount) {
				return true
			}
		}
		return false

	case TypeCategoryTypeVar:
		tv := t.(*TypeVarType)

		// Most TypeVar types need to be specialized.
		if tv.Shared.RecursiveAlias == nil {
			if TypeVarTypeIsSelf(tv) && options != nil && options.IgnoreSelf {
				return false
			}

			return true
		}

		// If this is a recursive type alias, it may need to be specialized if
		// it has generic type arguments.
		//
		// Note that the original's case falls through to the final `return
		// false` when there are no type args, because the `if` has no else.
		if tv.Props != nil && tv.Props.TypeAliasInfo != nil && tv.Props.TypeAliasInfo.TypeArgs != nil {
			for _, typeArg := range tv.Props.TypeAliasInfo.TypeArgs {
				if RequiresSpecialization(typeArg, options, recursionCount) {
					return true
				}
			}
			return false
		}
	}

	return false
}
