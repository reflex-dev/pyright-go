/*
 * typeutils_protocols.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The protocol-symbol and class-field collection helpers from
 * analyzer/typeUtils.ts (pyright 1.1.412), lines 1658-1712 and 2034-2080. See
 * the header of typeutils.go for the file split.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
)

// GetProtocolSymbols returns a set of all the symbols (indexed by symbol name)
// that are part of a protocol class and its protocol parent classes. If a
// same-named symbol appears in a parent and a child, the child overrides the
// parent.
func GetProtocolSymbols(classType *ClassType) *common.OrderedMap[string, *ClassMember] {
	symbolMap := common.NewOrderedMap[string, *ClassMember]()

	if (classType.Shared.Flags & ClassTypeFlagsProtocolClass) != 0 {
		GetProtocolSymbolsRecursive(classType, symbolMap, ClassTypeFlagsProtocolClass, 0)
	}

	return symbolMap
}

// GetProtocolSymbolsRecursive corresponds to getProtocolSymbolsRecursive. The
// TypeScript defaults classFlags to ProtocolClass and recursionCount to 0.
func GetProtocolSymbolsRecursive(
	classType *ClassType,
	symbolMap *common.OrderedMap[string, *ClassMember],
	classFlags ClassTypeFlags,
	recursionCount int,
) {
	// Special-case the NamedTuple class because it's not really a separate
	// class at runtime. The typeshed stubs model it this way, and we don't want
	// it to be treated as a protocol or abstract class.
	if ClassTypeIsBuiltInNamed(classType, "NamedTuple") {
		return
	}

	if recursionCount > MaxTypeRecursionCount {
		return
	}

	for _, baseClass := range classType.Shared.BaseClasses {
		if cls, ok := AsClass(baseClass); ok && (cls.Shared.Flags&classFlags) != 0 {
			GetProtocolSymbolsRecursive(cls, symbolMap, classFlags, recursionCount+1)
		}
	}

	if (classType.Shared.Flags & classFlags) != 0 {
		ClassTypeGetSymbolTable(classType).ForEach(func(symbol *Symbol, name string) {
			if !symbol.IsIgnoredForProtocolMatch() {
				symbolMap.Set(name, &ClassMember{
					Symbol:                 symbol,
					ClassType:              classType,
					UnspecializedClassType: classType,
					IsInstanceMember:       symbol.IsInstanceMember(),
					IsClassMember:          symbol.IsClassMember(),
					IsSlotsMember:          symbol.IsSlotsMember(),
					IsClassVar:             IsEffectivelyClassVar(symbol, false),
					IsReadOnly:             false,
					IsTypeDeclared:         symbol.HasTypedDeclarations(),
					SkippedUndeclaredType:  false,
				})
			}
		})
	}
}

// GetClassFieldsRecursive corresponds to getClassFieldsRecursive.
func GetClassFieldsRecursive(classType *ClassType) *common.OrderedMap[string, *ClassMember] {
	memberMap := common.NewOrderedMap[string, *ClassMember]()

	// Evaluate the types of members from the end of the MRO to the beginning.
	for _, mroClass := range ClassTypeGetReverseMro(classType) {
		specializedMroClass := PartiallySpecializeType(mroClass, classType, nil, nil)

		if specializedCls, ok := AsClass(specializedMroClass); ok {
			ClassTypeGetSymbolTable(specializedCls).ForEach(func(symbol *Symbol, name string) {
				if !symbol.IsIgnoredForProtocolMatch() && symbol.HasTypedDeclarations() {
					memberMap.Set(name, &ClassMember{
						ClassType:              specializedCls,
						UnspecializedClassType: mroClass,
						Symbol:                 symbol,
						IsInstanceMember:       symbol.IsInstanceMember(),
						IsClassMember:          symbol.IsClassMember(),
						IsSlotsMember:          symbol.IsSlotsMember(),
						IsClassVar:             IsEffectivelyClassVar(symbol, ClassTypeIsDataClass(specializedCls)),
						IsReadOnly:             IsMemberReadOnly(specializedCls, name),
						IsTypeDeclared:         true,
						SkippedUndeclaredType:  false,
					})
				}
			})
		} else {
			// If this ancestor class is unknown, throw away all symbols found
			// so far because they could be overridden by the unknown class.
			memberMap.Clear()
		}
	}

	return memberMap
}

// AddTypeVarsToListIfUnique combines two lists of type var types, maintaining
// the combined order but removing any duplicates.
//
// The TypeScript mutates list1 in place; Go slices are values, so this returns
// the (possibly reallocated) result. An empty typeVarScopeID stands in for the
// omitted optional argument.
func AddTypeVarsToListIfUnique(
	list1 []*TypeVarType,
	list2 []*TypeVarType,
	typeVarScopeID TypeVarScopeId,
) []*TypeVarType {
	for _, type2 := range list2 {
		if typeVarScopeID != "" && type2.Priv.ScopeID != typeVarScopeID {
			continue
		}

		found := false
		for _, type1 := range list1 {
			if IsTypeSame(type1, type2, TypeSameOptions{}, 0) {
				found = true
				break
			}
		}
		if !found {
			list1 = append(list1, type2)
		}
	}

	return list1
}

// GetTypeVarArgsRecursive walks the type recursively (in a depth-first manner),
// finds all type variables that are referenced, and returns an ordered list of
// unique type variables.
//
// The original notes: for example, if the type is
// Union[List[Dict[_T1, _T2]], _T1, _T3], the result would be [_T1, _T2, _T3].
//
// The TypeScript defaults recursionCount to 0.
func GetTypeVarArgsRecursive(t Type, recursionCount int) []*TypeVarType {
	if recursionCount > MaxTypeRecursionCount {
		return []*TypeVarType{}
	}
	recursionCount++

	var aliasInfo *TypeAliasInfo
	if t.Base().Props != nil {
		aliasInfo = t.Base().Props.TypeAliasInfo
	}
	if aliasInfo != nil {
		combinedList := []*TypeVarType{}

		if aliasInfo.TypeArgs != nil {
			for _, typeArg := range aliasInfo.TypeArgs {
				combinedList = AddTypeVarsToListIfUnique(
					combinedList, GetTypeVarArgsRecursive(typeArg, recursionCount), "")
			}

			return combinedList
		}

		// An empty array is truthy in JavaScript and typeParams is always an
		// array, so the original's `if (aliasInfo.shared.typeParams)` is always
		// taken. The loop simply does nothing when the slice is empty.
		if aliasInfo.Shared.TypeParams != nil {
			for _, typeParam := range aliasInfo.Shared.TypeParams {
				combinedList = AddTypeVarsToListIfUnique(combinedList, []*TypeVarType{typeParam}, "")
			}

			return combinedList
		}
	}

	if tv, ok := AsTypeVar(t); ok {
		// Don't return any recursive type alias placeholders.
		if tv.Shared.RecursiveAlias != nil {
			return []*TypeVarType{}
		}

		// Don't return any bound type variables.
		if TypeVarTypeIsBound(tv) {
			return []*TypeVarType{}
		}

		// Don't return any P.args or P.kwargs types.
		if IsParamSpec(tv) && tv.Priv.ParamSpecAccess != ParamSpecAccessNone {
			return []*TypeVarType{TypeVarTypeCloneForParamSpecAccess(tv, ParamSpecAccessNone)}
		}

		if tv.IsInstantiable() {
			return []*TypeVarType{TypeVarTypeCloneAsInstance(tv)}
		}
		return []*TypeVarType{tv}
	}

	if cls, ok := AsClass(t); ok {
		combinedList := []*TypeVarType{}
		var typeArgs []Type
		if cls.Priv.TupleTypeArgs != nil {
			typeArgs = make([]Type, 0, len(cls.Priv.TupleTypeArgs))
			for _, e := range cls.Priv.TupleTypeArgs {
				typeArgs = append(typeArgs, e.Type)
			}
		} else {
			typeArgs = cls.Priv.TypeArgs
		}
		if typeArgs != nil {
			for _, typeArg := range typeArgs {
				combinedList = AddTypeVarsToListIfUnique(
					combinedList, GetTypeVarArgsRecursive(typeArg, recursionCount), "")
			}
		}

		return combinedList
	}

	if IsUnion(t) {
		combinedList := []*TypeVarType{}
		DoForEachSubtype(t, func(subtype Type, index int, allSubtypes []Type) {
			combinedList = AddTypeVarsToListIfUnique(
				combinedList, GetTypeVarArgsRecursive(subtype, recursionCount), "")
		})
		return combinedList
	}

	if fn, ok := AsFunction(t); ok {
		combinedList := []*TypeVarType{}

		for i := range fn.Shared.Parameters {
			combinedList = AddTypeVarsToListIfUnique(
				combinedList,
				GetTypeVarArgsRecursive(FunctionTypeGetParamType(fn, i), recursionCount),
				"",
			)
		}

		returnType := FunctionTypeGetEffectiveReturnType(fn, true)
		if returnType != nil {
			combinedList = AddTypeVarsToListIfUnique(
				combinedList, GetTypeVarArgsRecursive(returnType, recursionCount), "")
		}

		return combinedList
	}

	return []*TypeVarType{}
}
