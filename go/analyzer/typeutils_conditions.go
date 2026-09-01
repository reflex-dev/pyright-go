/*
 * typeutils_conditions.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The condition, literal and tuple helpers from analyzer/typeUtils.ts
 * (pyright 1.1.412). See the header of typeutils.go for the file split.
 */

package analyzer

// PreserveUnknown propagates the Unknown if either type is "Unknown" (versus
// Any), preserving the incomplete flag on the unknown if present. The caller
// should verify that one or the other type is Unknown or Any.
//
// It returns an AnyType or UnknownType.
func PreserveUnknown(type1, type2 Type) Type {
	if unknown1, ok := AsUnknown(type1); ok && unknown1.Priv.IsIncomplete {
		return type1
	} else if unknown2, ok := AsUnknown(type2); ok && unknown2.Priv.IsIncomplete {
		return type2
	} else if IsUnknown(type1) || IsUnknown(type2) {
		return UnknownTypeCreate(false)
	}
	return AnyTypeCreate(false)
}

// IsUnionableType determines whether the specified types can be combined with
// other types for a union.
func IsUnionableType(subtypes []Type) bool {
	// If all of the subtypes are TypeForm types, we know that they are
	// unionable.
	allTypeForm := true
	for _, t := range subtypes {
		if t.Base().Props == nil || t.Base().Props.TypeForm == nil {
			allTypeForm = false
			break
		}
	}
	if allTypeForm {
		return true
	}

	typeFlags := TypeFlagsInstance | TypeFlagsInstantiable

	for _, subtype := range subtypes {
		typeFlags &= subtype.Base().Flags
	}

	// All subtypes need to be instantiable. Some types (like Any and None) are
	// both instances and instantiable. It's OK to include some of these, but at
	// least one subtype needs to be definitively instantiable (not an
	// instance).
	return (typeFlags&TypeFlagsInstantiable) != 0 && (typeFlags&TypeFlagsInstance) == 0
}

// DerivesFromAnyOrUnknown corresponds to the free function of the same name.
//
// Note that the original's first branch tests `isAnyOrUnknown(type)` -- the
// whole type -- rather than `subtype`, so a union that is entirely Any/Unknown
// answers true and an individual Any/Unknown subtype of a mixed union does not.
// That looks like a typo in the original, but it is reproduced as written.
// See UPSTREAM-BUGS.md #2.
func DerivesFromAnyOrUnknown(t Type) bool {
	anyOrUnknown := false

	DoForEachSubtype(t, func(subtype Type, index int, allSubtypes []Type) {
		if IsAnyOrUnknown(t) {
			anyOrUnknown = true
		} else if cls, ok := AsInstantiableClass(subtype); ok {
			if ClassTypeDerivesFromAnyOrUnknown(cls) {
				anyOrUnknown = true
			}
		} else if cls, ok := AsClassInstance(subtype); ok {
			if ClassTypeDerivesFromAnyOrUnknown(cls) {
				anyOrUnknown = true
			}
		}
	})

	return anyOrUnknown
}

// GetFullNameOfType returns nil where the TypeScript returns undefined; an
// empty string result is not possible because the original tests the name for
// truthiness before returning it.
func GetFullNameOfType(t Type) *string {
	if t.Base().Props != nil &&
		t.Base().Props.TypeAliasInfo != nil &&
		t.Base().Props.TypeAliasInfo.Shared.FullName != "" {
		return &t.Base().Props.TypeAliasInfo.Shared.FullName
	}

	switch t.Base().Category {
	case TypeCategoryAny, TypeCategoryUnknown:
		name := "typing.Any"
		return &name

	case TypeCategoryClass:
		return &t.(*ClassType).Shared.FullName

	case TypeCategoryFunction:
		return &t.(*FunctionType).Shared.FullName

	case TypeCategoryModule:
		return &t.(*ModuleType).Priv.ModuleName

	case TypeCategoryOverloaded:
		overloaded := t.(*OverloadedType)
		overloads := OverloadedTypeGetOverloads(overloaded)
		if len(overloads) > 0 {
			return &overloads[0].Shared.FullName
		}

		impl := OverloadedTypeGetImplementation(overloaded)
		if impl != nil {
			if fn, ok := AsFunction(impl); ok {
				return &fn.Shared.FullName
			}
		}
	}

	return nil
}

// AddConditionToType corresponds to addConditionToType. A nil options stands in
// for the omitted argument.
func AddConditionToType(t Type, condition []TypeCondition, options *AddConditionOptions) Type {
	if condition == nil {
		return t
	}

	if options != nil && options.SkipSelfCondition {
		filtered := []TypeCondition{}
		for _, c := range condition {
			if !TypeVarTypeIsSelf(c.TypeVar) {
				filtered = append(filtered, c)
			}
		}
		condition = filtered
		if len(condition) == 0 {
			return t
		}
	}

	if options != nil && options.SkipBoundTypeVars {
		filtered := []TypeCondition{}
		for _, c := range condition {
			if len(c.TypeVar.Shared.Constraints) > 0 {
				filtered = append(filtered, c)
			}
		}
		condition = filtered
		if len(condition) == 0 {
			return t
		}
	}

	switch t.Base().Category {
	case TypeCategoryUnbound,
		TypeCategoryUnknown,
		TypeCategoryAny,
		TypeCategoryNever,
		TypeCategoryModule,
		TypeCategoryTypeVar:
		return t

	case TypeCategoryFunction:
		fn := t.(*FunctionType)
		var existing []TypeCondition
		if fn.Props != nil {
			existing = fn.Props.Condition
		}
		return CloneForCondition(fn, TypeConditionCombine(existing, condition))

	case TypeCategoryOverloaded:
		overloaded := t.(*OverloadedType)
		newOverloads := make([]*FunctionType, 0, len(OverloadedTypeGetOverloads(overloaded)))
		for _, overload := range OverloadedTypeGetOverloads(overloaded) {
			newOverloads = append(newOverloads, AddConditionToType(overload, condition, nil).(*FunctionType))
		}
		return OverloadedTypeCreate(newOverloads, nil)

	case TypeCategoryClass:
		cls := t.(*ClassType)
		var existing []TypeCondition
		if cls.Props != nil {
			existing = cls.Props.Condition
		}
		return CloneForCondition(cls, TypeConditionCombine(existing, condition))

	case TypeCategoryUnion:
		union := t.(*UnionType)
		newSubtypes := make([]Type, 0, len(union.Priv.Subtypes))
		for _, subtype := range union.Priv.Subtypes {
			newSubtypes = append(newSubtypes, AddConditionToType(subtype, condition, nil))
		}
		return CombineTypes(newSubtypes, nil)
	}

	// The original's switch is exhaustive over TypeCategory and has no fallthrough.
	return t
}

// GetTypeCondition returns nil where the TypeScript returns undefined.
func GetTypeCondition(t Type) []TypeCondition {
	switch t.Base().Category {
	case TypeCategoryUnbound,
		TypeCategoryUnknown,
		TypeCategoryAny,
		TypeCategoryNever,
		TypeCategoryModule,
		TypeCategoryTypeVar,
		TypeCategoryOverloaded,
		TypeCategoryUnion:
		return nil

	case TypeCategoryClass, TypeCategoryFunction:
		if t.Base().Props != nil {
			return t.Base().Props.Condition
		}
		return nil
	}

	return nil
}

// IsTypeAliasPlaceholder indicates whether the specified type is a recursive
// type alias placeholder that has not yet been resolved.
func IsTypeAliasPlaceholder(t Type) bool {
	tv, ok := AsTypeVar(t)
	return ok && TypeVarTypeIsTypeAliasPlaceholder(tv)
}

// IsLiteralType corresponds to isLiteralType.
func IsLiteralType(t *ClassType) bool {
	return t.IsInstance() && t.Priv.LiteralValue != nil
}

// IsTupleClass corresponds to isTupleClass.
func IsTupleClass(t *ClassType) bool {
	return ClassTypeIsBuiltInNamed(t, "tuple")
}

// CombineTupleTypeArgs corresponds to combineTupleTypeArgs.
func CombineTupleTypeArgs(typeArgs []*TupleTypeArg) Type {
	typesToCombine := []Type{}

	for _, t := range typeArgs {
		if tv, ok := AsTypeVar(t.Type); ok {
			if IsUnpackedTypeVarTuple(t.Type) {
				// Treat the unpacked TypeVarTuple as a union.
				typesToCombine = append(typesToCombine, TypeVarTypeCloneForUnpacked(tv, true))
				continue
			}

			if IsUnpackedTypeVar(t.Type) {
				if tv.Shared.BoundType != nil {
					if boundCls, ok := AsClassInstance(tv.Shared.BoundType); ok &&
						IsTupleClass(boundCls) &&
						boundCls.Priv.TupleTypeArgs != nil {
						typesToCombine = append(typesToCombine, CombineTupleTypeArgs(boundCls.Priv.TupleTypeArgs))
					}
				}
				continue
			}
		}

		typesToCombine = append(typesToCombine, t.Type)
	}

	return CombineTypes(typesToCombine, nil)
}

// SpecializeTupleClass handles the special specialization tuples require: it
// computes the "effective" type argument, which is a union of the variadic type
// arguments.
//
// The TypeScript defaults isTypeArgExplicit to true and isUnpacked to false.
func SpecializeTupleClass(
	classType *ClassType,
	typeArgs []*TupleTypeArg,
	isTypeArgExplicit bool,
	isUnpacked bool,
) *ClassType {
	clonedClassType := ClassTypeSpecialize(
		classType,
		[]Type{CombineTupleTypeArgs(typeArgs)},
		&isTypeArgExplicit,
		false, // includeSubclasses is undefined in the original, i.e. falsy
		typeArgs,
		nil,
	)

	if isUnpacked {
		clonedClassType.Priv.IsUnpacked = true
	}

	return clonedClassType
}
