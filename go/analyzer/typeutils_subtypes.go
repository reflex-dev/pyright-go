/*
 * typeutils_subtypes.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The subtype mapping and ordering helpers from analyzer/typeUtils.ts
 * (pyright 1.1.412). See the header of typeutils.go for the file split.
 */

package analyzer

import (
	"sort"
)

// MapSubtypesOptions corresponds to the interface of the same name.
type MapSubtypesOptions struct {
	// SortSubtypes sorts subtypes in a union before iteration.
	SortSubtypes bool

	// SkipElideRedundantLiterals retains redundant literal types in unions if
	// they are present in the original type.
	SkipElideRedundantLiterals bool

	// RetainTypeAlias retains the type alias as is. This is safe only if the
	// caller has already transformed the associated type alias in a way that is
	// compatible with transforms applied to the type.
	RetainTypeAlias bool
}

// MapSubtypes calls a callback for each subtype and combines the results into a
// final type. It performs no memory allocations if the transformed type is the
// same as the original.
//
// The callback returns nil where the TypeScript returns undefined, meaning the
// subtype is dropped. A nil options stands in for the omitted argument.
func MapSubtypes(t Type, callback func(Type) Type, options *MapSubtypesOptions) Type {
	if options == nil {
		options = &MapSubtypesOptions{}
	}

	if union, ok := AsUnion(t); ok {
		var subtypes []Type
		if options.SortSubtypes {
			subtypes = SortTypes(unionableToTypes(union.Priv.Subtypes))
		} else {
			subtypes = unionableToTypes(union.Priv.Subtypes)
		}

		for i := 0; i < len(subtypes); i++ {
			subtype := subtypes[i]
			transformedType := callback(subtype)

			// Avoid doing any memory allocations until a change is detected.
			if subtype != transformedType {
				typesToCombine := sliceCopy(subtypes[:i])

				// A helper that accumulates transformed subtypes.
				accumulateSubtype := func(newSubtype Type) {
					if newSubtype != nil {
						typesToCombine = append(typesToCombine, AddConditionToType(newSubtype, GetTypeCondition(t), nil))
					}
				}

				accumulateSubtype(transformedType)

				for i++; i < len(subtypes); i++ {
					accumulateSubtype(callback(subtypes[i]))
				}

				newType := CombineTypes(typesToCombine, &CombineTypesOptions{
					SkipElideRedundantLiterals: options.SkipElideRedundantLiterals,
				})

				if options.RetainTypeAlias {
					if t.Base().Props != nil && t.Base().Props.TypeAliasInfo != nil {
						newType = CloneForTypeAlias(newType, t.Base().Props.TypeAliasInfo)
					}
				} else {
					// Do our best to retain type aliases.
					if newUnion, ok := AsUnion(newType); ok {
						UnionTypeAddTypeAliasSource(newUnion, t)
					}
				}

				return newType
			}
		}

		return t
	}

	transformedSubtype := callback(t)
	if transformedSubtype == nil {
		return NeverTypeCreateNever()
	}
	return transformedSubtype
}

// unionableToTypes widens a []UnionableType to []Type. The TypeScript needs no
// such conversion because its unions are structural.
func unionableToTypes(subtypes []UnionableType) []Type {
	out := make([]Type, len(subtypes))
	for i, s := range subtypes {
		out[i] = s
	}
	return out
}

// MapSignatures iterates over each signature in a function or overload,
// allowing the caller to replace one or more signatures with new ones.
//
// It returns nil where the TypeScript returns undefined.
func MapSignatures(t Type, callback func(*FunctionType) *FunctionType) Type {
	if fn, ok := AsFunction(t); ok {
		result := callback(fn)
		if result == nil {
			return nil
		}
		return result
	}

	overloaded := t.(*OverloadedType)
	newSignatures := []*FunctionType{}
	changeMade := false

	for _, overload := range OverloadedTypeGetOverloads(overloaded) {
		newOverload := callback(overload)
		if newOverload != overload {
			changeMade = true
		}

		if newOverload != nil {
			newSignatures = append(newSignatures, newOverload)
		}
	}

	if len(newSignatures) == 0 {
		return nil
	}

	// Add the unmodified implementation if it's present.
	implementation := OverloadedTypeGetImplementation(overloaded)
	newImplementation := implementation

	if implementation != nil {
		if implFn, ok := AsFunction(implementation); ok {
			transformed := callback(implFn)
			if transformed != nil {
				newImplementation = transformed
				changeMade = true
			} else {
				// The original assigns the (undefined) result unconditionally
				// and only sets changeMade when it is defined. See
				// UPSTREAM-BUGS.md #6.
				newImplementation = nil
			}
		}
	}

	if !changeMade {
		return t
	}

	if len(newSignatures) == 1 {
		return newSignatures[0]
	}

	return OverloadedTypeCreate(newSignatures, newImplementation)
}

// CleanIncompleteUnknown removes incomplete unknowns that are union'ed with
// other types.
//
// The original notes: the code flow engine uses a special form of the
// UnknownType (with the isIncomplete flag set) to distinguish between an
// unknown that was generated in a loop because it was temporarily incomplete
// versus an unknown that is permanently incomplete. Once an unknown appears
// within a loop, it is often propagated to other types during code flow
// analysis.
//
// The TypeScript defaults recursionCount to 0.
func CleanIncompleteUnknown(t Type, recursionCount int) Type {
	if recursionCount >= MaxTypeRecursionCount {
		return t
	}
	recursionCount++

	result := MapSubtypes(t, func(subtype Type) Type {
		// If it's an incomplete unknown, eliminate it.
		if unknown, ok := AsUnknown(subtype); ok && unknown.Priv.IsIncomplete {
			return nil
		}

		if cls, ok := AsClass(subtype); ok && cls.Priv.TypeArgs != nil {
			typeChanged := false

			if cls.Priv.TupleTypeArgs != nil {
				updatedTupleTypeArgs := make([]*TupleTypeArg, 0, len(cls.Priv.TupleTypeArgs))
				for _, tupleTypeArg := range cls.Priv.TupleTypeArgs {
					newTypeArg := CleanIncompleteUnknown(tupleTypeArg.Type, recursionCount)
					if newTypeArg != tupleTypeArg.Type {
						typeChanged = true
					}
					updatedTupleTypeArgs = append(updatedTupleTypeArgs, &TupleTypeArg{
						Type:        newTypeArg,
						IsUnbounded: tupleTypeArg.IsUnbounded,
						IsOptional:  tupleTypeArg.IsOptional,
					})
				}

				if typeChanged {
					isTypeArgExplicit := cls.Priv.IsTypeArgExplicit != nil && *cls.Priv.IsTypeArgExplicit
					return SpecializeTupleClass(cls, updatedTupleTypeArgs, isTypeArgExplicit, cls.Priv.IsUnpacked)
				}
			} else {
				updatedTypeArgs := make([]Type, 0, len(cls.Priv.TypeArgs))
				for _, typeArg := range cls.Priv.TypeArgs {
					newTypeArg := CleanIncompleteUnknown(typeArg, recursionCount)
					if newTypeArg != typeArg {
						typeChanged = true
					}
					updatedTypeArgs = append(updatedTypeArgs, newTypeArg)
				}

				if typeChanged {
					isTypeArgExplicit := cls.Priv.IsTypeArgExplicit != nil && *cls.Priv.IsTypeArgExplicit
					return ClassTypeSpecialize(cls, updatedTypeArgs, &isTypeArgExplicit, false, nil, nil)
				}
			}
		}

		// TODO - this doesn't currently handle function types.

		return subtype
	}, nil)

	// If we eliminated everything, don't return a Never.
	if IsNever(result) {
		return t
	}
	return result
}

// SortTypes sorts types into a deterministic order.
//
// sort.SliceStable because Array.prototype.sort is stable and compareTypes
// returns 0 for many pairs; an unstable sort would reorder them and change the
// printed output.
func SortTypes(types []Type) []Type {
	sorted := sliceCopy(types)
	sort.SliceStable(sorted, func(i, j int) bool {
		return compareTypes(sorted[i], sorted[j], 0) < 0
	})
	return sorted
}

// compareTypes corresponds to the unexported compareTypes. The TypeScript
// defaults recursionCount to 0.
//
// Note that most recursive calls in the original omit recursionCount, so it
// restarts at 0; only the class type-argument loop threads it through. That is
// reproduced here rather than corrected.
func compareTypes(a, b Type, recursionCount int) int {
	if recursionCount > MaxTypeRecursionCount {
		return 0
	}
	recursionCount++

	if a.Base().Category != b.Base().Category {
		return int(b.Base().Category) - int(a.Base().Category)
	}

	switch a.Base().Category {
	case TypeCategoryUnbound, TypeCategoryUnknown, TypeCategoryAny, TypeCategoryNever, TypeCategoryUnion:
		return 0

	case TypeCategoryFunction:
		aFunc := a.(*FunctionType)
		bFunc := b.(*FunctionType)

		aParamCount := len(aFunc.Shared.Parameters)
		bParamCount := len(bFunc.Shared.Parameters)
		if aParamCount != bParamCount {
			return bParamCount - aParamCount
		}

		for i := 0; i < aParamCount; i++ {
			aParam := aFunc.Shared.Parameters[i]
			bParam := bFunc.Shared.Parameters[i]
			if aParam.Category != bParam.Category {
				return int(bParam.Category) - int(aParam.Category)
			}

			typeComparison := compareTypes(
				FunctionTypeGetParamType(aFunc, i),
				FunctionTypeGetParamType(bFunc, i),
				0,
			)

			if typeComparison != 0 {
				return typeComparison
			}
		}

		aReturn := FunctionTypeGetEffectiveReturnType(aFunc, true)
		if aReturn == nil {
			aReturn = UnknownTypeCreate(false)
		}
		bReturn := FunctionTypeGetEffectiveReturnType(bFunc, true)
		if bReturn == nil {
			bReturn = UnknownTypeCreate(false)
		}

		returnTypeComparison := compareTypes(aReturn, bReturn, 0)
		if returnTypeComparison != 0 {
			return returnTypeComparison
		}

		aName := aFunc.Shared.Name
		bName := bFunc.Shared.Name

		if aName < bName {
			return -1
		} else if aName > bName {
			return 1
		}

		return 0

	case TypeCategoryOverloaded:
		aOver := a.(*OverloadedType)
		bOver := b.(*OverloadedType)

		aOverloads := OverloadedTypeGetOverloads(aOver)
		bOverloads := OverloadedTypeGetOverloads(bOver)
		aOverloadCount := len(aOverloads)
		bOverloadCount := len(bOverloads)
		if aOverloadCount != bOverloadCount {
			return bOverloadCount - aOverloadCount
		}

		for i := 0; i < aOverloadCount; i++ {
			typeComparison := compareTypes(aOverloads[i], bOverloads[i], 0)
			if typeComparison != 0 {
				return typeComparison
			}
		}

		return 0

	case TypeCategoryClass:
		aClass := a.(*ClassType)
		bClass := b.(*ClassType)

		// Sort instances before instantiables.
		if IsClassInstance(aClass) && IsInstantiableClass(bClass) {
			return -1
		} else if IsInstantiableClass(aClass) && IsClassInstance(bClass) {
			return 1
		}

		// Sort literals before non-literals.
		if IsLiteralType(aClass) {
			if !IsLiteralType(bClass) {
				return -1
			} else if ClassTypeIsSameGenericClass(aClass, bClass, 0) {
				// Sort by literal value.
				//
				// The original compares only when both are the JavaScript
				// `string` or both the JavaScript `number` arm. Integer
				// literals are bigints, so they fall through uncompared; that
				// is reproduced here.
				aLiteralValue := aClass.Priv.LiteralValue
				bLiteralValue := bClass.Priv.LiteralValue

				if aStr, ok := aLiteralValue.(LiteralString); ok {
					if bStr, ok := bLiteralValue.(LiteralString); ok {
						if aStr < bStr {
							return -1
						} else if aStr > bStr {
							return 1
						}
					}
				} else if aNum, ok := aLiteralValue.(LiteralFloat); ok {
					if bNum, ok := bLiteralValue.(LiteralFloat); ok {
						if aNum < bNum {
							return -1
						} else if aNum > bNum {
							return 1
						}
					}
				}
			}
		} else if IsLiteralType(bClass) {
			return 1
		}

		// Always sort NoneType at the end.
		if ClassTypeIsBuiltInNamed(aClass, "NoneType") {
			return 1
		} else if ClassTypeIsBuiltInNamed(bClass, "NoneType") {
			return -1
		}

		// Sort non-generics before generics.
		if len(aClass.Shared.TypeParams) > 0 || IsTupleClass(aClass) {
			if len(bClass.Shared.TypeParams) == 0 {
				return 1
			}
		} else if len(bClass.Shared.TypeParams) > 0 || IsTupleClass(bClass) {
			return -1
		}

		// Sort by class name.
		aName := aClass.Shared.Name
		bName := bClass.Shared.Name

		if aName < bName {
			return -1
		} else if aName > bName {
			return 1
		}

		// Sort by type argument count.
		aTypeArgCount := len(aClass.Priv.TypeArgs)
		bTypeArgCount := len(bClass.Priv.TypeArgs)

		if aTypeArgCount < bTypeArgCount {
			return -1
		} else if aTypeArgCount > bTypeArgCount {
			return 1
		}

		// Sort by type argument.
		for i := 0; i < aTypeArgCount; i++ {
			typeComparison := compareTypes(aClass.Priv.TypeArgs[i], bClass.Priv.TypeArgs[i], recursionCount)
			if typeComparison != 0 {
				return typeComparison
			}
		}

		return 0

	case TypeCategoryModule:
		aName := a.(*ModuleType).Priv.ModuleName
		bName := b.(*ModuleType).Priv.ModuleName
		if aName < bName {
			return -1
		} else if aName == bName {
			return 0
		}
		return 1

	case TypeCategoryTypeVar:
		aName := a.(*TypeVarType).Shared.Name
		bName := b.(*TypeVarType).Shared.Name
		if aName < bName {
			return -1
		} else if aName == bName {
			return 0
		}
		return 1
	}

	return 1
}

// DoForEachSubtype corresponds to doForEachSubtype. The TypeScript defaults
// sortSubtypes to false; see DoForEachSubtypeSorted for the other form.
func DoForEachSubtype(t Type, callback func(t Type, index int, allSubtypes []Type)) {
	doForEachSubtype(t, callback, false)
}

// DoForEachSubtypeSorted is doForEachSubtype with sortSubtypes set to true.
func DoForEachSubtypeSorted(t Type, callback func(t Type, index int, allSubtypes []Type)) {
	doForEachSubtype(t, callback, true)
}

func doForEachSubtype(t Type, callback func(t Type, index int, allSubtypes []Type), sortSubtypes bool) {
	if union, ok := AsUnion(t); ok {
		subtypes := unionableToTypes(union.Priv.Subtypes)
		if sortSubtypes {
			subtypes = SortTypes(subtypes)
		}
		for index, subtype := range subtypes {
			callback(subtype, index, subtypes)
		}
	} else {
		callback(t, 0, []Type{t})
	}
}

// SomeSubtypes corresponds to someSubtypes.
func SomeSubtypes(t Type, callback func(Type) bool) bool {
	if union, ok := AsUnion(t); ok {
		for _, subtype := range union.Priv.Subtypes {
			if callback(subtype) {
				return true
			}
		}
		return false
	}
	return callback(t)
}

// AllSubtypes corresponds to allSubtypes.
//
// Note the original's callback body: `every((subtype) => { callback(subtype); })`
// is a block-bodied arrow that returns undefined, so `every` stops at the first
// subtype and the function answers false for any non-empty union regardless of
// what the callback says. That is a bug in the original, but it is load-bearing
// for behavior, so it is reproduced exactly rather than fixed. See
// UPSTREAM-BUGS.md #1.
func AllSubtypes(t Type, callback func(Type) bool) bool {
	if union, ok := AsUnion(t); ok {
		for _, subtype := range union.Priv.Subtypes {
			callback(subtype)
			return false
		}
		return true
	}
	return callback(t)
}

// DoForEachSignature corresponds to doForEachSignature. The type is a
// FunctionType or OverloadedType.
func DoForEachSignature(t Type, callback func(t *FunctionType, index int)) {
	if fn, ok := AsFunction(t); ok {
		callback(fn, 0)
	} else {
		for index, overload := range OverloadedTypeGetOverloads(t.(*OverloadedType)) {
			callback(overload, index)
		}
	}
}

// AreTypesSame determines if all of the types in the slice are the same.
func AreTypesSame(types []Type, options TypeSameOptions) bool {
	if len(types) < 2 {
		return true
	}

	for i := 1; i < len(types); i++ {
		if !IsTypeSame(types[0], types[i], options, 0) {
			return false
		}
	}

	return true
}
