/*
 * types_same.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * isTypeSame, combineTypes and the union-manipulation helpers.
 *
 * Transliterated from analyzer/types.ts (pyright 1.1.412). Split out of
 * types.go only so no single Go file has to carry all 4000 lines.
 */

package analyzer

import (
	"sort"

	"github.com/microsoft/pyright/go/common"
)

// withIgnoreTypeFlagsFalse reproduces `{ ...options, ignoreTypeFlags: false }`,
// which isTypeSame writes at every recursive call into a component type.
func withIgnoreTypeFlagsFalse(options TypeSameOptions) TypeSameOptions {
	options.IgnoreTypeFlags = false
	return options
}

// IsTypeSame determines whether two types are the same. If IgnorePseudoGeneric
// is true, type arguments for "pseudo-generic" classes (non-generic classes
// whose init methods are not annotated and are therefore treated as generic)
// are ignored.
//
// The TypeScript defaults options to {} and recursionCount to 0.
func IsTypeSame(type1, type2 Type, options TypeSameOptions, recursionCount int) bool {
	if type1 == type2 {
		return true
	}

	if type1.Base().Category != type2.Base().Category {
		if options.TreatAnySameAsUnknown {
			if type1.Base().Category == TypeCategoryAny && type2.Base().Category == TypeCategoryUnknown {
				return true
			}
			if type1.Base().Category == TypeCategoryUnknown && type2.Base().Category == TypeCategoryAny {
				return true
			}
		}

		return false
	}

	if !options.IgnoreTypeFlags {
		if (type1.Base().Flags & TypeFlagsTypeCompatibilityMask) != (type2.Base().Flags & TypeFlagsTypeCompatibilityMask) {
			return false
		}
	}

	if recursionCount > MaxTypeRecursionCount {
		return true
	}
	recursionCount++

	if options.HonorTypeForm {
		var typeForm1, typeForm2 Type
		if type1.Base().Props != nil {
			typeForm1 = type1.Base().Props.TypeForm
		}
		if type2.Base().Props != nil {
			typeForm2 = type2.Base().Props.TypeForm
		}

		if typeForm1 != nil {
			if typeForm2 == nil {
				return false
			}

			if !IsTypeSame(typeForm1, typeForm2, options, recursionCount) {
				return false
			}
		} else if typeForm2 != nil {
			return false
		}
	}

	switch type1.Base().Category {
	case TypeCategoryClass:
		classType1 := type1.(*ClassType)
		classType2 := type2.(*ClassType)

		// If the details are not the same it's not the same class.
		if !ClassTypeIsSameGenericClass(classType1, classType2, recursionCount) {
			return false
		}

		if !options.IgnoreConditions {
			var cond1, cond2 []TypeCondition
			if classType1.Props != nil {
				cond1 = classType1.Props.Condition
			}
			if classType2.Props != nil {
				cond2 = classType2.Props.Condition
			}
			if !TypeConditionIsSame(cond1, cond2) {
				return false
			}
		}

		if !options.IgnorePseudoGeneric || !ClassTypeIsPseudoGenericClass(classType1) {
			// Make sure the type args match.
			if classType1.Priv.TupleTypeArgs != nil && classType2.Priv.TupleTypeArgs != nil {
				type1TupleTypeArgs := classType1.Priv.TupleTypeArgs
				type2TupleTypeArgs := classType2.Priv.TupleTypeArgs
				if len(type1TupleTypeArgs) != len(type2TupleTypeArgs) {
					return false
				}

				for i := range type1TupleTypeArgs {
					if !IsTypeSame(
						type1TupleTypeArgs[i].Type,
						type2TupleTypeArgs[i].Type,
						withIgnoreTypeFlagsFalse(options),
						recursionCount,
					) {
						return false
					}

					if type1TupleTypeArgs[i].IsUnbounded != type2TupleTypeArgs[i].IsUnbounded {
						return false
					}
				}
			} else {
				type1TypeArgs := classType1.Priv.TypeArgs
				type2TypeArgs := classType2.Priv.TypeArgs
				typeArgCount := len(type1TypeArgs)
				if len(type2TypeArgs) > typeArgCount {
					typeArgCount = len(type2TypeArgs)
				}

				for i := 0; i < typeArgCount; i++ {
					// Assume that missing type args are "Unknown".
					var typeArg1, typeArg2 Type = UnknownTypeCreate(false), UnknownTypeCreate(false)
					if i < len(type1TypeArgs) {
						typeArg1 = type1TypeArgs[i]
					}
					if i < len(type2TypeArgs) {
						typeArg2 = type2TypeArgs[i]
					}

					if !IsTypeSame(typeArg1, typeArg2, withIgnoreTypeFlagsFalse(options), recursionCount) {
						return false
					}
				}
			}
		}

		if !ClassTypeIsLiteralValueSame(classType1, classType2) {
			return false
		}

		if classType1.Priv.IsUnpacked != classType2.Priv.IsUnpacked {
			return false
		}

		if classType1.Priv.IsTypedDictPartial() != classType2.Priv.IsTypedDictPartial() {
			return false
		}

		if options.HonorIsTypeArgExplicit {
			explicit1 := classType1.Priv.IsTypeArgExplicit != nil && *classType1.Priv.IsTypeArgExplicit
			explicit2 := classType2.Priv.IsTypeArgExplicit != nil && *classType2.Priv.IsTypeArgExplicit
			if explicit1 != explicit2 {
				return false
			}
		}

		if !options.IgnoreTypedDictNarrowEntries && !ClassTypeIsTypedDictNarrowedEntriesSame(classType1, classType2) {
			return false
		}

		return true

	case TypeCategoryFunction:
		// Make sure the parameter counts match.
		functionType1 := type1.(*FunctionType)
		functionType2 := type2.(*FunctionType)
		params1 := functionType1.Shared.Parameters
		params2 := functionType2.Shared.Parameters

		if len(params1) != len(params2) {
			return false
		}

		// If one function is ... and the other is not, they are not the same.
		if FunctionTypeIsGradualCallableForm(functionType1) != FunctionTypeIsGradualCallableForm(functionType2) {
			return false
		}

		positionOnlyIndex1 := findParamIndex(params1, IsPositionOnlySeparator)
		positionOnlyIndex2 := findParamIndex(params2, IsPositionOnlySeparator)

		// Make sure the parameter details match.
		for i := range params1 {
			param1 := params1[i]
			param2 := params2[i]

			if param1.Category != param2.Category {
				return false
			}

			// The original guards each of these with
			// `positionOnlyIndex !== undefined`, but findIndex returns -1
			// rather than undefined, so that guard is always true. Reproduced
			// as written: with no separator the index is -1 and every name is
			// "relevant". See UPSTREAM-BUGS.md #4.
			isName1Relevant := i > positionOnlyIndex1
			isName2Relevant := i > positionOnlyIndex2

			if isName1Relevant != isName2Relevant {
				return false
			}

			if isName1Relevant {
				if !stringPtrEqual(param1.Name, param2.Name) {
					return false
				}
			} else if IsPositionOnlySeparator(param1) && IsPositionOnlySeparator(param2) {
				continue
			} else if IsKeywordOnlySeparator(param1) && IsKeywordOnlySeparator(param2) {
				continue
			}

			param1Type := FunctionTypeGetParamType(functionType1, i)
			param2Type := FunctionTypeGetParamType(functionType2, i)
			if !IsTypeSame(param1Type, param2Type, withIgnoreTypeFlagsFalse(options), recursionCount) {
				return false
			}
		}

		// Make sure the return types match.
		return1Type := functionType1.Shared.DeclaredReturnType
		if functionType1.Priv.SpecializedTypes != nil && functionType1.Priv.SpecializedTypes.ReturnType != nil {
			return1Type = functionType1.Priv.SpecializedTypes.ReturnType
		}
		if return1Type == nil && functionType1.Shared.InferredReturnType != nil {
			return1Type = functionType1.Shared.InferredReturnType.Type
		}

		return2Type := functionType2.Shared.DeclaredReturnType
		if functionType2.Priv.SpecializedTypes != nil && functionType2.Priv.SpecializedTypes.ReturnType != nil {
			return2Type = functionType2.Priv.SpecializedTypes.ReturnType
		}
		if return2Type == nil && functionType2.Shared.InferredReturnType != nil {
			return2Type = functionType2.Shared.InferredReturnType.Type
		}

		if return1Type != nil || return2Type != nil {
			if return1Type == nil ||
				return2Type == nil ||
				!IsTypeSame(return1Type, return2Type, withIgnoreTypeFlagsFalse(options), recursionCount) {
				return false
			}
		}

		return true

	case TypeCategoryOverloaded:
		// Make sure the overload counts match.
		overloaded1 := type1.(*OverloadedType)
		overloaded2 := type2.(*OverloadedType)
		if len(overloaded1.Priv.Overloads) != len(overloaded2.Priv.Overloads) {
			return false
		}

		// We assume here that overloaded functions always appear in the same
		// order from one analysis pass to another.
		for i := range overloaded1.Priv.Overloads {
			if !IsTypeSame(overloaded1.Priv.Overloads[i], overloaded2.Priv.Overloads[i], options, recursionCount) {
				return false
			}
		}

		return true

	case TypeCategoryUnion:
		unionType1 := type1.(*UnionType)
		unionType2 := type2.(*UnionType)
		subtypes1 := unionType1.Priv.Subtypes
		subtypes2 := unionType2.Priv.Subtypes

		if len(subtypes1) != len(subtypes2) {
			return false
		}

		// The types do not have a particular order, so we need to do the
		// comparison in an order-independent manner.
		exclusionSet := common.NewOrderedSet[int]()
		found := FindSubtype(unionType1, func(subtype Type) bool {
			return !UnionTypeContainsType(unionType2, subtype, options, exclusionSet, recursionCount)
		})
		return found == nil

	case TypeCategoryTypeVar:
		typeVar1 := type1.(*TypeVarType)
		typeVar2 := type2.(*TypeVarType)

		if typeVar1.Priv.ScopeID != typeVar2.Priv.ScopeID {
			return false
		}

		if typeVar1.Priv.NameWithScope != typeVar2.Priv.NameWithScope {
			return false
		}

		// Handle the case where this is a generic recursive type alias. Make
		// sure that the type argument types match.
		if typeVar1.Shared.RecursiveAlias != nil && typeVar2.Shared.RecursiveAlias != nil {
			var type1TypeArgs, type2TypeArgs []Type
			if typeVar1.Props != nil && typeVar1.Props.TypeAliasInfo != nil {
				type1TypeArgs = typeVar1.Props.TypeAliasInfo.TypeArgs
			}
			if typeVar2.Props != nil && typeVar2.Props.TypeAliasInfo != nil {
				type2TypeArgs = typeVar2.Props.TypeAliasInfo.TypeArgs
			}
			typeArgCount := len(type1TypeArgs)
			if len(type2TypeArgs) > typeArgCount {
				typeArgCount = len(type2TypeArgs)
			}

			for i := 0; i < typeArgCount; i++ {
				// Assume that missing type args are "Any".
				var typeArg1, typeArg2 Type = AnyTypeCreate(false), AnyTypeCreate(false)
				if i < len(type1TypeArgs) {
					typeArg1 = type1TypeArgs[i]
				}
				if i < len(type2TypeArgs) {
					typeArg2 = type2TypeArgs[i]
				}

				if !IsTypeSame(typeArg1, typeArg2, withIgnoreTypeFlagsFalse(options), recursionCount) {
					return false
				}
			}
		}

		if IsTypeVarTuple(typeVar1) && IsTypeVarTuple(typeVar2) {
			if typeVar1.Priv.IsInUnion != typeVar2.Priv.IsInUnion {
				return false
			}
		}

		if typeVar1.Shared == typeVar2.Shared {
			return true
		}

		if IsParamSpec(typeVar1) != IsParamSpec(typeVar2) {
			return false
		}

		if IsTypeVarTuple(typeVar1) != IsTypeVarTuple(typeVar2) {
			return false
		}

		if typeVar1.Shared.Name != typeVar2.Shared.Name ||
			typeVar1.Shared.IsSynthesized != typeVar2.Shared.IsSynthesized ||
			typeVar1.Shared.DeclaredVariance != typeVar2.Shared.DeclaredVariance ||
			typeVar1.Priv.ScopeID != typeVar2.Priv.ScopeID {
			return false
		}

		boundType1 := typeVar1.Shared.BoundType
		boundType2 := typeVar2.Shared.BoundType
		if boundType1 != nil {
			if boundType2 == nil ||
				!IsTypeSame(boundType1, boundType2, withIgnoreTypeFlagsFalse(options), recursionCount) {
				return false
			}
		} else {
			if boundType2 != nil {
				return false
			}
		}

		constraints1 := typeVar1.Shared.Constraints
		constraints2 := typeVar2.Shared.Constraints
		if len(constraints1) != len(constraints2) {
			return false
		}

		for i := range constraints1 {
			if !IsTypeSame(constraints1[i], constraints2[i], withIgnoreTypeFlagsFalse(options), recursionCount) {
				return false
			}
		}

		return true

	case TypeCategoryModule:
		module1 := type1.(*ModuleType)
		module2 := type2.(*ModuleType)

		// Module types are the same if they share the same module symbol
		// table.
		if module1.Priv.Fields == module2.Priv.Fields {
			return true
		}

		// If both symbol tables are empty, we can also assume they're equal.
		if module1.Priv.Fields.Size() == 0 && module2.Priv.Fields.Size() == 0 {
			return true
		}

		return false

	case TypeCategoryUnknown:
		unknown1 := type1.(*UnknownType)
		unknown2 := type2.(*UnknownType)

		return unknown1.Priv.IsIncomplete == unknown2.Priv.IsIncomplete
	}

	return true
}

// findParamIndex stands in for Array.prototype.findIndex, returning -1 when no
// element matches.
func findParamIndex(params []FunctionParam, predicate func(FunctionParam) bool) int {
	for i, param := range params {
		if predicate(param) {
			return i
		}
	}
	return -1
}

// stringPtrEqual compares two `string | undefined` values the way `!==` does.
func stringPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// RemoveUnknownFromUnion removes an "unknown" type from the union, returning
// only the known types, if the type is a union.
func RemoveUnknownFromUnion(t Type) Type {
	return RemoveFromUnion(t, func(t Type) bool { return IsUnknown(t) })
}

// RemoveUnbound removes an "unbound" type from the union, returning only the
// known types, if the type is a union.
func RemoveUnbound(t Type) Type {
	if IsUnion(t) {
		return RemoveFromUnion(t, func(t Type) bool { return IsUnbound(t) })
	}

	if IsUnbound(t) {
		return UnknownTypeCreate(false)
	}

	return t
}

// RemoveFromUnion corresponds to removeFromUnion.
func RemoveFromUnion(t Type, removeFilter func(Type) bool) Type {
	if union, ok := AsUnion(t); ok {
		remainingTypes := []Type{}
		for _, subtype := range union.Priv.Subtypes {
			if !removeFilter(subtype) {
				remainingTypes = append(remainingTypes, subtype)
			}
		}
		if len(remainingTypes) < len(union.Priv.Subtypes) {
			newType := CombineTypes(remainingTypes, nil)

			if newUnion, ok := AsUnion(newType); ok {
				UnionTypeAddTypeAliasSource(newUnion, t)
			}

			return newType
		}
	}

	return t
}

// FindSubtype corresponds to findSubtype. It returns nil where the TypeScript
// returns undefined.
func FindSubtype(t Type, filter func(Type) bool) Type {
	if union, ok := AsUnion(t); ok {
		for _, subtype := range union.Priv.Subtypes {
			if filter(subtype) {
				return subtype
			}
		}
		return nil
	}

	if filter(t) {
		return t
	}
	return nil
}

// CombineTypesOptions corresponds to the interface of the same name.
type CombineTypesOptions struct {
	// SkipElideRedundantLiterals skips the default behavior of eliding
	// (removing) literals from a union when the non-literal subtype is
	// present.
	SkipElideRedundantLiterals bool

	// MaxSubtypeCount, if set, is the maximum number of subtypes that should
	// be allowed in the union before it is converted to an "Any" type. Nil
	// stands in for `undefined`.
	MaxSubtypeCount *int
}

// CombineTypes combines multiple types into a single type. If the types are the
// same, only one is returned. If they differ, they are combined into a
// UnionType. NeverTypes are filtered out. If no types remain in the end, a
// NeverType is returned.
//
// A nil options stands in for the omitted optional argument.
func CombineTypes(subtypes []Type, options *CombineTypesOptions) Type {
	// Filter out any Never or NoReturn types.
	neverTypes, subtypes := common.Partition(subtypes, func(t Type) bool { return IsNever(t) })

	if len(subtypes) == 0 {
		if len(neverTypes) > 0 {
			// Prefer NoReturn over Never. This approach preserves type alias
			// information if present.
			for _, t := range neverTypes {
				if t.(*NeverType).Priv.IsNoReturn {
					return t
				}
			}
			return neverTypes[0]
		}

		return NeverTypeCreateNever()
	}

	// Handle the common case where there is only one type. Also handle the
	// common case where there are multiple copies of the same type.
	allSubtypesAreSame := true
	if len(subtypes) > 1 {
		for index := 1; index < len(subtypes); index++ {
			if subtypes[index] != subtypes[0] {
				allSubtypesAreSame = false
				break
			}
		}
	}

	if allSubtypesAreSame {
		return subtypes[0]
	}

	// Expand all union types.
	var expandedTypes []Type
	haveExpanded := false
	typeAliasSources := common.NewOrderedSet[*UnionType]()

	for i := 0; i < len(subtypes); i++ {
		subtype := subtypes[i]
		if union, ok := AsUnion(subtype); ok {
			if !haveExpanded {
				expandedTypes = sliceCopy(subtypes[:i])
				haveExpanded = true
			}
			for _, s := range union.Priv.Subtypes {
				expandedTypes = append(expandedTypes, s)
			}

			if union.Props != nil && union.Props.TypeAliasInfo != nil {
				typeAliasSources.Add(union)
			} else if union.Priv.TypeAliasSources != nil {
				union.Priv.TypeAliasSources.ForEach(func(source *UnionType) {
					typeAliasSources.Add(source)
				})
			}
		} else if haveExpanded {
			expandedTypes = append(expandedTypes, subtype)
		}
	}

	if !haveExpanded {
		expandedTypes = subtypes
	}

	// Sort all of the literal and empty types to the end.
	//
	// sort.SliceStable because Array.prototype.sort is stable, and this
	// comparator returns 0 for most pairs, so an unstable sort would reorder
	// unrelated subtypes and change the printed union.
	sort.SliceStable(expandedTypes, func(i, j int) bool {
		return combineTypesSortCompare(expandedTypes[i], expandedTypes[j]) < 0
	})

	// If removing all NoReturn types results in no remaining types, convert it
	// to an unknown.
	if len(expandedTypes) == 0 {
		return UnknownTypeCreate(false)
	}

	newUnionType := UnionTypeCreate()
	if typeAliasSources.Size() > 0 {
		newUnionType.Priv.TypeAliasSources = typeAliasSources
	}

	hitMaxSubtypeCount := false

	for index, subtype := range expandedTypes {
		if index == 0 {
			UnionTypeAddType(newUnionType, subtype.(UnionableType))
		} else {
			if options == nil || options.MaxSubtypeCount == nil ||
				len(newUnionType.Priv.Subtypes) < *options.MaxSubtypeCount {
				elideRedundantLiterals := options == nil || !options.SkipElideRedundantLiterals
				addTypeIfUnique(newUnionType, subtype.(UnionableType), elideRedundantLiterals)
			} else {
				hitMaxSubtypeCount = true
			}
		}
	}

	if hitMaxSubtypeCount {
		return AnyTypeCreate(false)
	}

	// If only one type remains, convert it from a union to a simple type.
	if len(newUnionType.Priv.Subtypes) == 1 {
		return newUnionType.Priv.Subtypes[0]
	}

	return newUnionType
}

// combineTypesSortCompare is the comparator CombineTypes passes to sort.
func combineTypesSortCompare(type1, type2 Type) int {
	if cls, ok := AsClass(type1); ok && cls.Priv.LiteralValue != nil {
		return 1
	}

	if cls, ok := AsClass(type2); ok && cls.Priv.LiteralValue != nil {
		return -1
	}

	if cls, ok := AsClassInstance(type1); ok && cls.Priv.IsEmptyContainer {
		return 1
	} else if cls, ok := AsClassInstance(type2); ok && cls.Priv.IsEmptyContainer {
		return -1
	}

	return 0
}

// IsSameWithoutLiteralValue determines whether the dest type is the same as the
// source type with the possible exception that the source type has a literal
// value when the dest does not.
func IsSameWithoutLiteralValue(destType, srcType Type) bool {
	// If it's the same with literals, great.
	if IsTypeSame(destType, srcType, TypeSameOptions{}, 0) {
		return true
	}

	if cls, ok := AsInstantiableClass(srcType); ok && cls.Priv.LiteralValue != nil {
		// Strip the literal.
		srcType = ClassTypeCloneWithLiteral(cls, nil)
		return IsTypeSame(destType, srcType, TypeSameOptions{}, 0)
	}

	if cls, ok := AsClassInstance(srcType); ok && cls.Priv.LiteralValue != nil {
		// Strip the literal.
		srcType = ClassTypeCloneWithLiteral(cls, nil)
		return IsTypeSame(destType, srcType, TypeSameOptions{IgnoreConditions: true}, 0)
	}

	return false
}

// jsTruthy reproduces JavaScript's truthiness test on a LiteralValue. It is
// needed only by addTypeIfUnique's bool-literal merge, which negates a literal
// value with `!` and compares the result to another literal.
func jsTruthy(value LiteralValue) bool {
	switch v := value.(type) {
	case nil:
		return false
	case LiteralBool:
		return bool(v)
	case LiteralString:
		return v != ""
	case LiteralFloat:
		return v != 0
	case LiteralInt:
		return v.Value != nil && v.Value.Sign() != 0
	default:
		// EnumLiteral and SentinelLiteral are objects, which are always truthy.
		return true
	}
}

// addTypeIfUnique corresponds to the unexported _addTypeIfUnique.
func addTypeIfUnique(unionType *UnionType, typeToAdd UnionableType, elideRedundantLiterals bool) {
	// Handle the addition of a string literal in a special manner to avoid n^2
	// behavior in unions that contain hundreds of string literal types. Skip
	// this for constrained types.
	if cls, ok := AsClass(typeToAdd); ok && (cls.Props == nil || cls.Props.Condition == nil) {
		var literalMaps *LiteralTypes
		if IsClassInstance(typeToAdd) {
			literalMaps = &unionType.Priv.LiteralInstances
		} else {
			literalMaps = &unionType.Priv.LiteralClasses
		}

		if ClassTypeIsBuiltInNamed(cls, "str") &&
			cls.Priv.LiteralValue != nil &&
			literalMaps.LiteralStrMap != nil {
			if !literalMaps.LiteralStrMap.Has(string(cls.Priv.LiteralValue.(LiteralString))) {
				UnionTypeAddType(unionType, typeToAdd)
			}
			return
		} else if ClassTypeIsBuiltInNamed(cls, "int") &&
			cls.Priv.LiteralValue != nil &&
			literalMaps.LiteralIntMap != nil {
			if !literalMaps.LiteralIntMap.Has(literalNumberKey(cls.Priv.LiteralValue)) {
				UnionTypeAddType(unionType, typeToAdd)
			}
			return
		} else if ClassTypeIsEnumClass(cls) &&
			cls.Priv.LiteralValue != nil &&
			literalMaps.LiteralEnumMap != nil {
			enumLiteral := cls.Priv.LiteralValue.(*EnumLiteral)
			if !literalMaps.LiteralEnumMap.Has(enumLiteral.GetName()) {
				UnionTypeAddType(unionType, typeToAdd)
			}
			return
		}
	}

	addCls, addIsClass := AsClass(typeToAdd)
	isPseudoGeneric := addIsClass && ClassTypeIsPseudoGenericClass(addCls)

	for i := 0; i < len(unionType.Priv.Subtypes); i++ {
		t := unionType.Priv.Subtypes[i]

		// Does this type already exist in the types array?
		if IsTypeSame(t, typeToAdd, TypeSameOptions{HonorTypeForm: true}, 0) {
			return
		}

		// Handle the case where pseudo-generic classes with different type
		// arguments are being combined. Rather than add multiple specialized
		// types, we will replace them with a single specialized type that is
		// specialized with Unknowns. This is important because we can hit
		// recursive cases (where a pseudo-generic class is parameterized with
		// its own class) ad infinitum.
		if isPseudoGeneric {
			if IsTypeSame(t, typeToAdd, TypeSameOptions{IgnorePseudoGeneric: true, HonorTypeForm: true}, 0) {
				unknownArgs := make([]Type, len(addCls.Shared.TypeParams))
				for j := range unknownArgs {
					unknownArgs[j] = UnknownTypeCreate(false)
				}
				unionType.Priv.Subtypes[i] = ClassTypeSpecialize(addCls, unknownArgs, nil, false, nil, nil)
				return
			}
		}

		existingCls, existingIsInstance := AsClassInstance(t)
		addInstance, addIsInstance := AsClassInstance(typeToAdd)
		if existingIsInstance && addIsInstance {
			// If the typeToAdd is a literal value and there's already a
			// non-literal type that matches, don't add the literal value.
			if elideRedundantLiterals && IsSameWithoutLiteralValue(t, typeToAdd) {
				if existingCls.Priv.LiteralValue == nil {
					return
				}
			}

			// If we're adding Literal[False] or Literal[True] to its opposite,
			// combine them into a non-literal 'bool' type.
			if ClassTypeIsBuiltInNamed(existingCls, "bool") &&
				(existingCls.Props == nil || existingCls.Props.Condition == nil) &&
				ClassTypeIsBuiltInNamed(addInstance, "bool") &&
				(addInstance.Props == nil || addInstance.Props.Condition == nil) {
				// The original writes
				// `!typeToAdd.priv.literalValue === type.priv.literalValue`,
				// which negates the added literal for truthiness and then
				// compares that boolean to the existing literal with `===`.
				// So the branch fires exactly when the two are opposite
				// booleans.
				if addInstance.Priv.LiteralValue != nil {
					negated := LiteralBool(!jsTruthy(addInstance.Priv.LiteralValue))
					if existingBool, ok := existingCls.Priv.LiteralValue.(LiteralBool); ok && existingBool == negated {
						unionType.Priv.Subtypes[i] = ClassTypeCloneWithLiteral(existingCls, nil)
						return
					}
				}
			}

			// If the typeToAdd is a TypedDict that is the same class as the
			// existing type, see if one of them is a proper subset of the
			// other.
			if ClassTypeIsTypedDictClass(existingCls) && ClassTypeIsSameGenericClass(existingCls, addInstance, 0) {
				// Do not proceed if the TypedDicts are generic and have
				// different type arguments.
				if existingCls.Priv.TypeArgs == nil && addInstance.Priv.TypeArgs == nil {
					if ClassTypeIsTypedDictNarrower(addInstance, existingCls) {
						return
					} else if ClassTypeIsTypedDictNarrower(existingCls, addInstance) {
						unionType.Priv.Subtypes[i] = typeToAdd
						return
					}
				}
			}
		}

		// If the typeToAdd is an empty container and there's already a
		// non-empty container of the same type, don't add the empty container.
		if addIsInstance && addInstance.Priv.IsEmptyContainer {
			if existingIsInstance && ClassTypeIsSameGenericClass(existingCls, addInstance, 0) {
				return
			}
		}
	}

	UnionTypeAddType(unionType, typeToAdd)
}
