/*
 * typeprinter_names.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * UniqueNameMap and the small formatting helpers from analyzer/typePrinter.ts
 * (pyright 1.1.412), lines 1386-1574.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
)

// printUnpack corresponds to the function of the same name.
func printUnpack(textToWrap string, flags PrintTypeFlags) string {
	if flags&PrintTypeFlagsUseTypingUnpack != 0 {
		return "Unpack[" + textToWrap + "]"
	}
	return "*" + textToWrap
}

// printNestedInstantiable surrounds a printed type with type[...] as many times
// as needed for the nested instantiable count.
func printNestedInstantiable(t Type, textToWrap string) string {
	nestedTypes := t.Base().GetInstantiableDepth() + 1

	for nestLevel := 0; nestLevel < nestedTypes; nestLevel++ {
		textToWrap = "type[" + textToWrap + "]"
	}

	return textToWrap
}

// getReadableTypeVarName corresponds to the function of the same name.
func getReadableTypeVarName(t *TypeVarType, includeScope bool) string {
	return TypeVarTypeGetReadableName(t, includeScope)
}

// getTypeVarVarianceText corresponds to the function of the same name.
func getTypeVarVarianceText(t *TypeVarType) string {
	computedVariance := t.Shared.DeclaredVariance
	if t.Priv.ComputedVariance != nil {
		computedVariance = *t.Priv.ComputedVariance
	}

	switch computedVariance {
	case VarianceInvariant:
		return "invariant"
	case VarianceCovariant:
		return "covariant"
	case VarianceContravariant:
		return "contravariant"
	}

	return ""
}

// UniqueNameMap represents a map of named types (classes and type aliases) that
// appear within a specified type, used to determine whether any of the names
// require disambiguation (i.e. their fully-qualified name is required).
type UniqueNameMap struct {
	entries *common.OrderedMap[string, []Type]

	printTypeFlags     PrintTypeFlags
	returnTypeCallback FunctionReturnTypeCallback
}

// NewUniqueNameMap corresponds to the constructor.
func NewUniqueNameMap(printTypeFlags PrintTypeFlags, returnTypeCallback FunctionReturnTypeCallback) *UniqueNameMap {
	return &UniqueNameMap{
		entries:            common.NewOrderedMap[string, []Type](),
		printTypeFlags:     printTypeFlags,
		returnTypeCallback: returnTypeCallback,
	}
}

// Build corresponds to UniqueNameMap.build. The TypeScript defaults
// recursionTypes to [] and recursionCount to 0.
//
// recursionTypes is threaded through as a pointer because the original pushes
// and pops the caller's array; a Go slice passed by value would lose the
// caller's view of those mutations.
func (m *UniqueNameMap) Build(t Type, recursionTypes *[]Type, recursionCount int) {
	if recursionTypes == nil {
		empty := []Type{}
		recursionTypes = &empty
	}

	if recursionCount > MaxTypeRecursionCount {
		return
	}
	recursionCount++

	var aliasInfo *TypeAliasInfo
	if t.Base().Props != nil {
		aliasInfo = t.Base().Props.TypeAliasInfo
	}

	if aliasInfo != nil {
		expandTypeAlias := true
		if (m.printTypeFlags & PrintTypeFlagsExpandTypeAlias) == 0 {
			expandTypeAlias = false
		} else {
			for _, rt := range *recursionTypes {
				if rt == t {
					expandTypeAlias = false
					break
				}
			}
		}

		if !expandTypeAlias {
			typeAliasName := aliasInfo.Shared.Name
			if (m.printTypeFlags & PrintTypeFlagsUseFullyQualifiedNames) != 0 {
				typeAliasName = aliasInfo.Shared.FullName
			}
			m.addIfUnique(typeAliasName, t, true)

			// Recursively add the type arguments if present.
			if aliasInfo.TypeArgs != nil {
				*recursionTypes = append(*recursionTypes, t)
				for _, typeArg := range aliasInfo.TypeArgs {
					m.Build(typeArg, recursionTypes, recursionCount)
				}
				*recursionTypes = (*recursionTypes)[:len(*recursionTypes)-1]
			}

			return
		}
	}

	*recursionTypes = append(*recursionTypes, t)

	switch t.Base().Category {
	case TypeCategoryFunction:
		fn := t.(*FunctionType)
		for index := range fn.Shared.Parameters {
			paramType := FunctionTypeGetParamType(fn, index)
			m.Build(paramType, recursionTypes, recursionCount)
		}

		returnType := m.returnTypeCallback(fn)
		m.Build(returnType, recursionTypes, recursionCount)

	case TypeCategoryOverloaded:
		for _, overload := range OverloadedTypeGetOverloads(t.(*OverloadedType)) {
			m.Build(overload, recursionTypes, recursionCount)
		}

	case TypeCategoryClass:
		cls := t.(*ClassType)
		if cls.Priv.LiteralValue != nil {
			break
		}

		className := ""
		if cls.Priv.AliasName() != nil {
			className = *cls.Priv.AliasName()
		}
		if className == "" {
			if (m.printTypeFlags & PrintTypeFlagsUseFullyQualifiedNames) != 0 {
				className = cls.Shared.FullName
			} else {
				className = cls.Shared.Name
			}
		}

		m.addIfUnique(className, t, false)

		if !ClassTypeIsPseudoGenericClass(cls) {
			if cls.Priv.TupleTypeArgs != nil {
				for _, typeArg := range cls.Priv.TupleTypeArgs {
					m.Build(typeArg.Type, recursionTypes, recursionCount)
				}
			} else if cls.Priv.TypeArgs != nil {
				for _, typeArg := range cls.Priv.TypeArgs {
					m.Build(typeArg, recursionTypes, recursionCount)
				}
			}
		}

	case TypeCategoryUnion:
		DoForEachSubtype(t, func(subtype Type, index int, allSubtypes []Type) {
			m.Build(subtype, recursionTypes, recursionCount)
		})

		union := t.(*UnionType)
		if union.Priv.TypeAliasSources != nil {
			union.Priv.TypeAliasSources.ForEach(func(typeAliasSource *UnionType) {
				m.Build(typeAliasSource, recursionTypes, recursionCount)
			})
		}
	}

	*recursionTypes = (*recursionTypes)[:len(*recursionTypes)-1]
}

// IsUnique corresponds to UniqueNameMap.isUnique.
func (m *UniqueNameMap) IsUnique(name string) bool {
	entry, ok := m.entries.Get(name)
	return !ok || len(entry) == 1
}

// addIfUnique corresponds to the private _addIfUnique. The TypeScript defaults
// useTypeAliasName to false.
func (m *UniqueNameMap) addIfUnique(name string, t Type, useTypeAliasName bool) {
	existingEntry, ok := m.entries.Get(name)
	if !ok {
		m.entries.Set(name, []Type{t})
		return
	}

	for _, existing := range existingEntry {
		if m.isSameTypeName(existing, t, useTypeAliasName) {
			return
		}
	}

	m.entries.Set(name, append(existingEntry, t))
}

// isSameTypeName corresponds to the private _isSameTypeName.
func (m *UniqueNameMap) isSameTypeName(type1, type2 Type, useTypeAliasName bool) bool {
	if useTypeAliasName {
		var name1, name2 *string
		if type1.Base().Props != nil && type1.Base().Props.TypeAliasInfo != nil {
			name1 = &type1.Base().Props.TypeAliasInfo.Shared.FullName
		}
		if type2.Base().Props != nil && type2.Base().Props.TypeAliasInfo != nil {
			name2 = &type2.Base().Props.TypeAliasInfo.Shared.FullName
		}
		return stringPtrEqual(name1, name2)
	}

	cls1, ok1 := AsClass(type1)
	cls2, ok2 := AsClass(type2)
	if ok1 && ok2 {
		// The original loops "while instantiable", which terminates after one
		// iteration because cloneAsInstance clears the Instantiable flag. The
		// loop shape is kept so the two read alike.
		for cls1.IsInstantiable() {
			cls1 = ClassTypeCloneAsInstance(cls1, true)
		}

		for cls2.IsInstantiable() {
			cls2 = ClassTypeCloneAsInstance(cls2, true)
		}

		return ClassTypeIsSameGenericClass(cls1, cls2, 0)
	}

	return false
}
