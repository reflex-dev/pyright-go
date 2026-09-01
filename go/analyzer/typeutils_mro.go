/*
 * typeutils_mro.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * MRO linearization, variance compatibility and declaring-module collection
 * from analyzer/typeUtils.ts (pyright 1.1.412), lines 3122-3390. This is the
 * last range of the file. See the header of typeutils.go for the file split.
 */

package analyzer

// IsVarianceOfTypeArgCompatible determines if the variance of the type argument
// for a generic class is compatible with the declared variance of the
// corresponding type parameter.
func IsVarianceOfTypeArgCompatible(t Type, typeParamVariance Variance) bool {
	if typeParamVariance == VarianceUnknown || typeParamVariance == VarianceAuto {
		return true
	}

	if tv, ok := AsTypeVar(t); ok && !IsParamSpec(t) && !IsTypeVarTuple(t) {
		typeArgVariance := tv.Shared.DeclaredVariance

		if typeArgVariance == VarianceContravariant || typeArgVariance == VarianceCovariant {
			return typeArgVariance == typeParamVariance
		}
	} else if cls, ok := AsClassInstance(t); ok {
		if len(cls.Shared.TypeParams) > 0 {
			for index, typeParam := range cls.Shared.TypeParams {
				if IsParamSpec(typeParam) || IsTypeVarTuple(typeParam) {
					continue
				}

				var typeArgType Type
				if cls.Priv.TypeArgs != nil && index < len(cls.Priv.TypeArgs) {
					typeArgType = cls.Priv.TypeArgs[index]
				}

				declaredVariance := typeParam.Shared.DeclaredVariance
				if declaredVariance == VarianceAuto {
					continue
				}

				effectiveVariance := VarianceInvariant
				if declaredVariance == VarianceCovariant {
					// If the declared variance is covariant, the effective
					// variance is simply copied from the type param variance.
					effectiveVariance = typeParamVariance
				} else if declaredVariance == VarianceContravariant {
					// If the declared variance is contravariant, it flips the
					// effective variance from contravariant to covariant or
					// vice versa.
					if typeParamVariance == VarianceCovariant {
						effectiveVariance = VarianceContravariant
					} else if typeParamVariance == VarianceContravariant {
						effectiveVariance = VarianceCovariant
					}
				}

				if typeArgType == nil {
					typeArgType = UnknownTypeCreate(false)
				}
				if !IsVarianceOfTypeArgCompatible(typeArgType, effectiveVariance) {
					return false
				}
			}
			return true
		}
	}

	return true
}

// ComputeMroLinearization computes the method resolution ordering for a class
// whose base classes have already been filled in. The algorithm is described
// here: https://www.python.org/download/releases/2.3/mro/. It returns true if
// an MRO was possible, false otherwise.
//
// It writes the result into classType.Shared.Mro, as the original does.
func ComputeMroLinearization(classType *ClassType) bool {
	isMroFound := true

	// Clear out any existing MRO information.
	classType.Shared.Mro = []Type{}

	filteredBaseClasses := []Type{}
	for index, baseClass := range classType.Shared.BaseClasses {
		keep := true

		if cls, ok := AsInstantiableClass(baseClass); ok {
			// Generic has some special-case logic (see description of
			// __mro_entries__ in PEP 560) that we need to account for here.
			if ClassTypeIsBuiltInNamed(cls, "Generic") {
				// If the class is a Protocol or TypedDict, the generic is
				// ignored for the purposes of computing the MRO.
				if ClassTypeIsProtocolClass(classType) || ClassTypeIsTypedDictClass(classType) {
					keep = false
				} else {
					// If the class contains any specialized generic classes
					// after the Generic base, the Generic base is ignored for
					// purposes of computing the MRO.
					for innerIndex, innerBaseClass := range classType.Shared.BaseClasses {
						if innerIndex <= index {
							continue
						}
						innerCls, ok := AsInstantiableClass(innerBaseClass)
						if ok && innerCls.Priv.TypeArgs != nil &&
							innerCls.Priv.IsTypeArgExplicit != nil && *innerCls.Priv.IsTypeArgExplicit {
							keep = false
							break
						}
					}
				}
			}
		}

		if keep {
			filteredBaseClasses = append(filteredBaseClasses, baseClass)
		}
	}

	// Construct the list of class lists that need to be merged.
	classListsToMerge := [][]Type{}

	for _, baseClass := range filteredBaseClasses {
		if cls, ok := AsInstantiableClass(baseClass); ok {
			solution := BuildSolutionFromSpecializedClass(cls)
			merged := make([]Type, 0, len(cls.Shared.Mro))
			for _, mroClass := range cls.Shared.Mro {
				merged = append(merged, ApplySolvedTypeVars(mroClass, solution, nil))
			}
			classListsToMerge = append(classListsToMerge, merged)
		} else {
			classListsToMerge = append(classListsToMerge, []Type{baseClass})
		}
	}

	lastList := make([]Type, 0, len(filteredBaseClasses))
	for _, baseClass := range filteredBaseClasses {
		// The original rebuilds the solution inside the loop; it does not
		// depend on baseClass, so every iteration produces the same one.
		solution := BuildSolutionFromSpecializedClass(classType)
		lastList = append(lastList, ApplySolvedTypeVars(baseClass, solution, nil))
	}
	classListsToMerge = append(classListsToMerge, lastList)

	// The first class in the MRO is the class itself.
	solution := BuildSolutionFromSpecializedClass(classType)
	specializedClassType := ApplySolvedTypeVars(classType, solution, nil)
	if !IsClass(specializedClassType) && !IsAnyOrUnknown(specializedClassType) {
		specializedClassType = UnknownTypeCreate(false)
	}

	classType.Shared.Mro = append(classType.Shared.Mro, specializedClassType)

	// isInTail returns true if the specified searchClass is found in the "tail"
	// (i.e. in elements 1 through n) of any of the class lists.
	isInTail := func(searchClass *ClassType, classLists [][]Type) bool {
		for _, classList := range classLists {
			for i, value := range classList {
				if cls, ok := AsInstantiableClass(value); ok && ClassTypeIsSameGenericClass(cls, searchClass, 0) {
					if i > 0 {
						return true
					}
					break
				}
			}
		}
		return false
	}

	// filterClass removes any duplicate entries of the specified class from the
	// class lists. This is used once the class has been added to the MRO.
	filterClass := func(classToFilter *ClassType, classLists [][]Type) {
		for i := range classLists {
			filtered := classLists[i][:0]
			for _, value := range classLists[i] {
				cls, ok := AsInstantiableClass(value)
				if !ok || !ClassTypeIsSameGenericClass(cls, classToFilter, 0) {
					filtered = append(filtered, value)
				}
			}
			classLists[i] = filtered
		}
	}

	for {
		foundValidHead := false
		nonEmptyListIndex := -1

		for i := range classListsToMerge {
			classList := classListsToMerge[i]

			if len(classList) > 0 {
				if nonEmptyListIndex < 0 {
					nonEmptyListIndex = i
				}

				headCls, headIsClass := AsInstantiableClass(classList[0])
				if !headIsClass {
					foundValidHead = true
					head := classList[0]
					if !IsClass(head) && !IsAnyOrUnknown(head) {
						head = UnknownTypeCreate(false)
					}
					classType.Shared.Mro = append(classType.Shared.Mro, head)
					classListsToMerge[i] = classList[1:]
					break
				}

				if !isInTail(headCls, classListsToMerge) {
					foundValidHead = true
					classType.Shared.Mro = append(classType.Shared.Mro, classList[0])
					filterClass(headCls, classListsToMerge)
					break
				}
			}
		}

		// If all lists are empty, we are done.
		if nonEmptyListIndex < 0 {
			break
		}

		// We made it all the way through the list of class lists without
		// finding a valid head, but there is at least one list that's not yet
		// empty. This means there's no valid MRO order.
		if !foundValidHead {
			isMroFound = false

			// Handle the situation by pulling the head off the first non-empty
			// list. This allows us to make forward progress.
			nonEmptyList := classListsToMerge[nonEmptyListIndex]
			headCls, headIsClass := AsInstantiableClass(nonEmptyList[0])
			if !headIsClass {
				head := nonEmptyList[0]
				if !IsClass(head) && !IsAnyOrUnknown(head) {
					head = UnknownTypeCreate(false)
				}
				classType.Shared.Mro = append(classType.Shared.Mro, head)
				classListsToMerge[nonEmptyListIndex] = nonEmptyList[1:]
			} else {
				classType.Shared.Mro = append(classType.Shared.Mro, nonEmptyList[0])
				filterClass(headCls, classListsToMerge)
			}
		}
	}

	return isMroFound
}

// GetDeclaringModulesForType returns zero or more unique module names that
// point to the place(s) where the type is declared. Unions, for example, can
// result in more than one result. Type arguments are not included.
func GetDeclaringModulesForType(t Type) []string {
	moduleList := []string{}
	moduleList = addDeclaringModuleNamesForType(t, moduleList, 0)
	return moduleList
}

// addDeclaringModuleNamesForType corresponds to the unexported function of the
// same name. The TypeScript mutates moduleList in place; Go slices are values,
// so this returns the result.
func addDeclaringModuleNamesForType(t Type, moduleList []string, recursionCount int) []string {
	if recursionCount > MaxTypeRecursionCount {
		return moduleList
	}
	recursionCount++

	addIfUnique := func(moduleName string) {
		if moduleName == "" {
			return
		}
		for _, n := range moduleList {
			if n == moduleName {
				return
			}
		}
		moduleList = append(moduleList, moduleName)
	}

	switch t.Base().Category {
	case TypeCategoryClass:
		addIfUnique(t.(*ClassType).Shared.ModuleName)

	case TypeCategoryFunction:
		addIfUnique(t.(*FunctionType).Shared.ModuleName)

	case TypeCategoryOverloaded:
		overloaded := t.(*OverloadedType)
		for _, overload := range OverloadedTypeGetOverloads(overloaded) {
			moduleList = addDeclaringModuleNamesForType(overload, moduleList, recursionCount)
		}
		impl := OverloadedTypeGetImplementation(overloaded)
		if impl != nil {
			moduleList = addDeclaringModuleNamesForType(impl, moduleList, recursionCount)
		}

	case TypeCategoryUnion:
		DoForEachSubtype(t, func(subtype Type, index int, allSubtypes []Type) {
			moduleList = addDeclaringModuleNamesForType(subtype, moduleList, recursionCount)
		})

	case TypeCategoryModule:
		addIfUnique(t.(*ModuleType).Priv.ModuleName)
	}

	return moduleList
}
