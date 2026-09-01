/*
 * typeutils_convert.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * convertToInstance / convertToInstantiable and the member-collection helpers
 * from analyzer/typeUtils.ts (pyright 1.1.412), lines 2386-2575. See the header
 * of typeutils.go for the file split.
 */

package analyzer

// ConvertToInstance corresponds to convertToInstance. The TypeScript defaults
// includeSubclasses to true.
//
// Note that this memoizes onto the *original* type's cached slot, mutating it.
func ConvertToInstance(t Type, includeSubclasses bool) Type {
	// See if we've already performed this conversion and cached it.
	if t.Base().Cached != nil && t.Base().Cached.InstanceType != nil && includeSubclasses {
		return t.Base().Cached.InstanceType
	}

	result := MapSubtypes(t, func(subtype Type) Type {
		switch subtype.Base().Category {
		case TypeCategoryClass:
			cls := subtype.(*ClassType)

			// Handle type[x] as a special case.
			if ClassTypeIsBuiltInNamed(cls, "type") {
				if cls.IsInstance() {
					if cls.Priv.TypeArgs == nil || len(cls.Priv.TypeArgs) < 1 {
						return UnknownTypeCreate(false)
					}
					return cls.Priv.TypeArgs[0]
				}

				if cls.Priv.TypeArgs != nil && len(cls.Priv.TypeArgs) > 0 {
					if !IsAnyOrUnknown(cls.Priv.TypeArgs[0]) {
						return ConvertToInstantiable(cls.Priv.TypeArgs[0], true)
					}
				}
			}

			return ClassTypeCloneAsInstance(cls, includeSubclasses)

		case TypeCategoryFunction:
			if subtype.Base().IsInstantiable() {
				return FunctionTypeCloneAsInstance(subtype.(*FunctionType))
			}

		case TypeCategoryTypeVar:
			if subtype.Base().IsInstantiable() {
				return TypeVarTypeCloneAsInstance(subtype.(*TypeVarType))
			}

		case TypeCategoryAny:
			return AnyTypeConvertToInstance(subtype.(*AnyType))

		case TypeCategoryUnknown:
			return UnknownTypeConvertToInstance(subtype.(*UnknownType))

		case TypeCategoryNever:
			return NeverTypeConvertToInstance(subtype.(*NeverType))

		case TypeCategoryUnbound:
			return UnboundTypeConvertToInstance(subtype.(*UnboundType))
		}

		return subtype
	}, &MapSubtypesOptions{SkipElideRedundantLiterals: true})

	// Copy over any type alias information.
	var aliasInfo *TypeAliasInfo
	if t.Base().Props != nil {
		aliasInfo = t.Base().Props.TypeAliasInfo
	}
	if aliasInfo != nil && t != result {
		result = CloneForTypeAlias(result, aliasInfo)
	}

	if t != result && includeSubclasses {
		// Cache the converted value for next time.
		if t.Base().Cached == nil {
			t.Base().Cached = &CachedTypeInfo{}
		}
		t.Base().Cached.InstanceType = result
	}

	return result
}

// ConvertToInstantiable corresponds to convertToInstantiable. The TypeScript
// defaults includeSubclasses to true.
//
// Note that unlike ConvertToInstance, the cache read here is not conditioned on
// includeSubclasses, so a cached result computed with one value is returned for
// the other. That asymmetry is in the original.
func ConvertToInstantiable(t Type, includeSubclasses bool) Type {
	// See if we've already performed this conversion and cached it.
	if t.Base().Cached != nil && t.Base().Cached.InstantiableType != nil {
		return t.Base().Cached.InstantiableType
	}

	result := MapSubtypes(t, func(subtype Type) Type {
		switch subtype.Base().Category {
		case TypeCategoryClass:
			return ClassTypeCloneAsInstantiable(subtype.(*ClassType), includeSubclasses)

		case TypeCategoryFunction:
			return FunctionTypeCloneAsInstantiable(subtype.(*FunctionType))

		case TypeCategoryTypeVar:
			return TypeVarTypeCloneAsInstantiable(subtype.(*TypeVarType))
		}

		return subtype
	}, nil)

	if t != result {
		// Cache the converted value for next time.
		if t.Base().Cached == nil {
			t.Base().Cached = &CachedTypeInfo{}
		}
		t.Base().Cached.InstantiableType = result
	}

	return result
}
