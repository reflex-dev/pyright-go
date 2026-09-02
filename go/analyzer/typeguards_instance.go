/*
 * typeguards_instance.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeGuards.ts (pyright 1.1.412):
 * getIsInstanceClassTypes, narrowTypeForInstanceOrSubclass,
 * narrowTypeForInstanceOrSubclassInternal, narrowTypeForInstance,
 * intersectSameClassType, intersectTupleTypes.
 *
 * isinstance() and issubclass() narrowing. This is the densest logic in
 * typeGuards.ts and the transliteration keeps its structure literally: the outer
 * mapSubtypesExpandTypeVars dispatches each subtype of the reference type to one
 * of three filters (class, function/overload, or a fallthrough), and each filter
 * loops over every entry in the isinstance filter list deciding what survives.
 *
 * Two things about the shape are worth stating, because they read as accidents
 * and are not:
 *
 * - narrowTypeForInstanceOrSubclass runs the whole thing twice. The first pass
 *   forbids synthesizing intersection classes; only if that produces Never does
 *   it retry with them allowed. That ordering is what keeps `isinstance(x, B)`
 *   on an unrelated `x: A` from silently inventing `<subclass of A and B>` when
 *   a plainer answer existed.
 *
 * - The filters accumulate into filteredTypes and, in the negative case, push
 *   the *unnarrowed* fallback whenever no filter was proven to always match.
 *   Failing to filter is not the same as filtering to nothing.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// GetIsInstanceClassTypes corresponds to getIsInstanceClassTypes.
//
// The original's comment: the "isinstance" and "issubclass" calls support two
// forms - a simple form that accepts a single class, and a more complex form
// that accepts a tuple of classes (including arbitrarily-nested tuples). This
// method determines which form and returns a list of classes or undefined.
//
// The second result stands in for the original's `undefined` return: false means
// a non-class type was found, so no narrowing is possible at all. An empty list
// with true is a distinct (and reachable) answer.
func GetIsInstanceClassTypes(evaluator TypeEvaluator, argType Type) ([]Type, bool) {
	foundNonClassType := false
	classTypeList := []Type{}

	// The original's comment: create a helper function that returns a list of class
	// types or undefined if any of the types are not valid.
	addClassTypesToList := func(types []Type) {
		for _, subtype := range types {
			if IsClass(subtype) {
				subtype = SpecializeWithUnknownTypeArgs(subtype.(*ClassType), evaluator.GetTupleClassType())

				if IsInstantiableClass(subtype) && ClassTypeIsBuiltInNamed(subtype.(*ClassType), "Callable") {
					subtype = ConvertToInstantiable(GetUnknownTypeForCallable(), true)
				}
			}

			switch {
			case IsInstantiableClass(subtype):
				cls := subtype.(*ClassType)
				// The original's comment: if this is a reference to a class that has type
				// promotions (e.g. float or complex), remove the promotions for purposes of
				// the isinstance check).
				if !cls.Priv.IncludeSubclasses && cls.Priv.IncludePromotions != nil && *cls.Priv.IncludePromotions {
					subtype = ClassTypeCloneRemoveTypePromotions(cls)
				}
				classTypeList = append(classTypeList, subtype)

			case IsTypeVar(subtype) && subtype.Base().IsInstantiable():
				classTypeList = append(classTypeList, subtype)

			case IsNoneTypeClass(subtype):
				common.Assert(IsInstantiableClass(subtype), "Expected NoneType to be an instantiable class")
				classTypeList = append(classTypeList, subtype)

			case IsFunction(subtype) &&
				len(subtype.(*FunctionType).Shared.Parameters) == 2 &&
				subtype.(*FunctionType).Shared.Parameters[0].Category == parser.ParamCategoryArgsList &&
				subtype.(*FunctionType).Shared.Parameters[1].Category == parser.ParamCategoryKwargsDict:
				classTypeList = append(classTypeList, subtype)

			default:
				foundNonClassType = true
			}
		}
	}

	var addClassTypesRecursive func(t Type, recursionCount int)
	addClassTypesRecursive = func(t Type, recursionCount int) {
		if recursionCount > MaxTypeRecursionCount {
			return
		}

		if IsClass(t) && t.Base().IsInstance() && IsTupleClass(t.(*ClassType)) {
			cls := t.(*ClassType)
			if cls.Priv.TupleTypeArgs != nil {
				for _, tupleEntry := range cls.Priv.TupleTypeArgs {
					addClassTypesRecursive(tupleEntry.Type, recursionCount+1)
				}
			}
		} else {
			DoForEachSubtype(t, func(subtype Type, _ int, _ []Type) {
				addClassTypesToList([]Type{subtype})
			})
		}
	}

	DoForEachSubtype(argType, func(subtype Type, _ int, _ []Type) {
		addClassTypesRecursive(subtype, 0)
	})

	if foundNonClassType {
		return nil, false
	}
	return classTypeList, true
}

// NarrowTypeForInstanceOrSubclass corresponds to narrowTypeForInstanceOrSubclass.
func NarrowTypeForInstanceOrSubclass(
	evaluator TypeEvaluator,
	t Type,
	filterTypes []Type,
	isInstanceCheck bool,
	isTypeIsCheck bool,
	isPositiveTest bool,
	errorNode parser.ExpressionNode,
) Type {
	// The original's comment: first try with intersection types disallowed.
	narrowedType := narrowTypeForInstanceOrSubclassInternal(
		evaluator, t, filterTypes, isInstanceCheck, isTypeIsCheck, isPositiveTest, false, errorNode)

	if !IsNever(narrowedType) {
		return narrowedType
	}

	// The original's comment: try again with intersection types allowed.
	return narrowTypeForInstanceOrSubclassInternal(
		evaluator, t, filterTypes, isInstanceCheck, isTypeIsCheck, isPositiveTest, true, errorNode)
}

func narrowTypeForInstanceOrSubclassInternal(
	evaluator TypeEvaluator,
	t Type,
	filterTypes []Type,
	isInstanceCheck bool,
	isTypeIsCheck bool,
	isPositiveTest bool,
	allowIntersections bool,
	errorNode parser.ExpressionNode,
) Type {
	return MapSubtypes(t, func(subtype Type) Type {
		adjSubtype := subtype
		resultRequiresAdj := false
		adjFilterTypes := filterTypes

		if !isInstanceCheck {
			isTypeInstance := IsClassInstance(subtype) && ClassTypeIsBuiltInNamed(subtype.(*ClassType), "type")

			// The original's comment: handle metaclass instances specially.
			if IsMetaclassInstance(subtype) && !isTypeInstance {
				adjFilterTypes = make([]Type, 0, len(filterTypes))
				for _, filterType := range filterTypes {
					adjFilterTypes = append(adjFilterTypes, ConvertToInstantiable(filterType, true))
				}
			} else {
				adjSubtype = ConvertToInstance(subtype, true)

				if !IsAnyOrUnknown(subtype) || isPositiveTest {
					resultRequiresAdj = true
				}
			}
		}

		narrowedResult := narrowTypeForInstance(
			evaluator, adjSubtype, adjFilterTypes, isTypeIsCheck,
			isPositiveTest, allowIntersections, errorNode)

		if !resultRequiresAdj {
			return narrowedResult
		}

		if IsAnyOrUnknown(narrowedResult) {
			typeClass := evaluator.GetTypeClassType()
			if typeClass != nil {
				return ClassTypeSpecialize(
					ClassTypeCloneAsInstance(typeClass, true), []Type{narrowedResult}, nil, false, nil, nil)
			}
		}

		return ConvertToInstantiable(narrowedResult, true)
	}, nil)
}

// narrowTypeForInstance corresponds to the function of the same name.
//
// The original's comment: narrows a type based on a call to isinstance. For
// example, if the original type of expression "x" is "Mammal" and the test
// expression is "isinstance(x, Cow)", (assuming "Cow" is a subclass of
// "Mammal"), we can narrow x to "Cow".
func narrowTypeForInstance(
	evaluator TypeEvaluator,
	t Type,
	filterTypes []Type,
	isTypeIsCheck bool,
	isPositiveTest bool,
	allowIntersections bool,
	errorNode parser.ExpressionNode,
) Type {
	expandedTypes := MapSubtypes(t, func(subtype Type) Type {
		return TransformPossibleRecursiveTypeAlias(subtype, 0)
	}, nil)

	expandedTypes = evaluator.ExpandPromotionTypes(errorNode, expandedTypes)

	convertVarTypeToFree := func(varType Type) Type {
		// The original's comment: if this is a TypeIs check, type variables should
		// remain bound.
		if isTypeIsCheck {
			return varType
		}

		// The original's comment: if this is an isinstance or issubclass check, the
		// type variables should be converted to "free" type variables.
		return MakeTypeVarsFree(varType, GetTypeVarScopesForNode(errorNode))
	}

	// The original's comment: filters the varType by the parameters of the
	// isinstance and returns the list of types the varType could be after applying
	// the filter.
	filterClassType := func(
		varType Type,
		concreteVarType *ClassType,
		conditions []TypeCondition,
		negativeFallbackType Type,
	) []Type {
		filteredTypes := []Type{}

		foundSuperclass := false
		isClassRelationshipIndeterminate := false

		for _, filterType := range filterTypes {
			concreteFilterType := evaluator.MakeTopLevelTypeVarsConcrete(filterType, false)

			if IsInstantiableClass(concreteFilterType) {
				concreteFilterClass := concreteFilterType.(*ClassType)
				filterMetaclass := concreteFilterClass.Shared.EffectiveMetaclass
				if IsInstantiableMetaclass(concreteVarType) &&
					concreteFilterClass.GetInstantiableDepth() > 0 &&
					filterMetaclass != nil && IsInstantiableClass(filterMetaclass) {
					filterMetaclassCls := filterMetaclass.(*ClassType)
					metaclassType := ConvertToInstance(concreteVarType, true)
					isMetaclassOverlap := evaluator.AssignType(
						convertVarTypeToFree(metaclassType),
						ClassTypeCloneAsInstance(filterMetaclassCls, true),
						nil, nil, AssignTypeFlagsDefault, 0)

					// The original's comment: handle the special case where the metaclass for
					// the filter is type. This will normally be treated as type[Any], which is
					// compatible with any metaclass, but we specifically want to treat type as
					// the class type[object] in this case.
					if ClassTypeIsBuiltInNamed(filterMetaclassCls, "type") &&
						(filterMetaclassCls.Priv.IsTypeArgExplicit == nil || !*filterMetaclassCls.Priv.IsTypeArgExplicit) {
						if !IsClass(metaclassType) ||
							!ClassTypeIsBuiltInNamed(metaclassType.(*ClassType), "type") {
							isMetaclassOverlap = false
						}
					}

					if isMetaclassOverlap {
						if isPositiveTest {
							filteredTypes = append(filteredTypes, filterType)
							foundSuperclass = true
						} else if !IsTypeSame(metaclassType, filterMetaclass, TypeSameOptions{}, 0) ||
							filterMetaclassCls.Priv.IncludeSubclasses {
							filteredTypes = append(filteredTypes, metaclassType)
							isClassRelationshipIndeterminate = true
						}
						continue
					}
				}

				runtimeVarType := concreteVarType

				// The original's comment: type variables are erased for runtime types, so
				// switch from bound to free type variables. We'll retain the bound type
				// variables for TypeIs checks.
				if !isTypeIsCheck {
					if freed := MakeTypeVarsFree(runtimeVarType, GetTypeVarScopesForNode(errorNode)); IsClass(freed) {
						runtimeVarType = freed.(*ClassType)
					}
				}

				// The original's comment: if the value is a TypedDict, convert it into its
				// runtime form, which is a dict[str, Any].
				if IsInstantiableClass(runtimeVarType) && ClassTypeIsTypedDictClass(runtimeVarType) {
					dictClass := evaluator.GetDictClassType()
					strType := evaluator.GetStrClassType()

					if dictClass != nil && strType != nil {
						runtimeVarType = ClassTypeSpecialize(dictClass, []Type{
							ClassTypeCloneAsInstance(strType, true),
							UnknownTypeCreate(false),
						}, nil, false, nil, nil)
					}
				}

				filterIsSuperclass := evaluator.AssignType(
					filterType, runtimeVarType, nil, nil,
					AssignTypeFlagsAllowIsinstanceSpecialForms|AssignTypeFlagsAllowProtocolClassSource, 0)

				filterIsSubclass := evaluator.AssignType(
					runtimeVarType, filterType, nil, nil,
					AssignTypeFlagsAllowIsinstanceSpecialForms|AssignTypeFlagsAllowProtocolClassSource, 0)

				if filterIsSuperclass {
					foundSuperclass = true
				}

				// The original's comment: special-case the TypeForm special form. This
				// represents a variety of runtime classes that will not appear to overlap
				// with TypeForm.
				if ClassTypeIsBuiltInNamed(runtimeVarType, "TypeForm") {
					isClassRelationshipIndeterminate = true
					filterIsSubclass = true
				}

				// The original's comment: normally, a type should never be both a subclass
				// and a superclass. This can happen if either of the class types derives from
				// a class whose type is unknown (e.g. an import failed). We'll note this case
				// specially so we don't do any narrowing, which will generate false
				// positives.
				if filterIsSuperclass {
					if !isTypeIsCheck && concreteFilterClass.Priv.IncludeSubclasses {
						// The original's comment: if the filter type includes subclasses, we
						// can't eliminate this type in the negative direction. We'll relax this
						// for TypeIs checks.
						isClassRelationshipIndeterminate = true
					}

					if filterIsSubclass &&
						!ClassTypeIsSameGenericClass(runtimeVarType, concreteFilterClass, 0) {
						// The original's comment: if the runtime variable type is a type[T],
						// handle a filter of 'type' as a special case.
						if !ClassTypeIsBuiltInNamed(concreteFilterClass, "type") ||
							runtimeVarType.GetInstantiableDepth() == 0 {
							isClassRelationshipIndeterminate = true
						}
					}
				}

				// The original's comment: if both the variable type and the filter type ar
				// generics, we can't determine the relationship between the two.
				if IsTypeVar(varType) && IsTypeVar(filterType) {
					isClassRelationshipIndeterminate = true
				}

				if isPositiveTest {
					if filterIsSuperclass {
						// The original's comment: if the variable type is a subclass of the
						// isinstance filter, we haven't learned anything new about the variable
						// type.
						//
						// If the varType is a Self or type[Self], retain the unnarrowedType.
						if IsTypeVar(varType) && TypeVarTypeIsSelf(varType.(*TypeVarType)) {
							filteredTypes = append(filteredTypes, AddConditionToType(varType, conditions, nil))
						} else {
							filteredTypes = append(filteredTypes,
								AddConditionToType(concreteVarType, conditions, nil))
						}
					} else if filterIsSubclass {
						// The original's comment: if the variable type is a superclass of the
						// isinstance filter, we can narrow the type to the subclass.
						specializedFilterType := filterType

						// The original's comment: try to retain the type arguments for the filter
						// type. This is important because a specialized version of the filter
						// cannot be passed to isinstance or issubclass.
						if IsClass(filterType) {
							filterClass := filterType.(*ClassType)
							if ClassTypeIsSpecialBuiltIn(filterClass) || len(filterClass.Shared.TypeParams) > 0 {
								if (filterClass.Priv.IsTypeArgExplicit == nil || !*filterClass.Priv.IsTypeArgExplicit) &&
									!ClassTypeIsSameGenericClass(concreteVarType, filterClass, 0) {
									constraints := NewConstraintTracker()
									unspecializedFilterType := ClassTypeSpecialize(
										filterClass, nil, nil, false, nil, nil)

									if AddConstraintsForExpectedType(
										evaluator,
										ClassTypeCloneAsInstance(unspecializedFilterType, true),
										ClassTypeCloneAsInstance(concreteVarType, true),
										constraints,
										nil,
										errorNode.GetRange().Start,
									) {
										specializedFilterType = evaluator.SolveAndApplyConstraints(
											unspecializedFilterType,
											constraints,
											&ApplyTypeVarOptions{
												ReplaceUnsolved: &ReplaceUnsolvedOptions{
													ScopeIDs:       GetTypeVarScopeIds(filterType),
													UseUnknown:     true,
													TupleClassType: evaluator.GetTupleClassType(),
												},
											},
											nil,
										)
									}
								}
							}
						}

						filteredTypes = append(filteredTypes,
							AddConditionToType(specializedFilterType, conditions, nil))
					} else if ClassTypeIsSameGenericClass(
						ClassTypeCloneAsInstance(concreteVarType, true),
						ClassTypeCloneAsInstance(concreteFilterClass, true), 0) {
						if !isTypeIsCheck {
							// The original's comment: don't attempt to narrow in this case.
							if concreteVarType.Priv.LiteralValue == nil &&
								concreteFilterClass.Priv.LiteralValue == nil {
								intersection := intersectSameClassType(
									evaluator, concreteVarType, concreteFilterClass)
								if intersection != nil {
									filteredTypes = append(filteredTypes, intersection)
								} else {
									filteredTypes = append(filteredTypes, varType)
								}
							}
						}
					} else if allowIntersections &&
						!ClassTypeIsFinal(concreteVarType) &&
						!ClassTypeIsFinal(concreteFilterClass) {
						// The original's comment: the two types appear to have no relation. It's
						// possible that the two types are protocols or the program is expecting
						// one type to be a mix-in class used with the other. In this case, we'll
						// synthesize a new class type that represents an intersection of the two
						// types.
						var newClassType Type = evaluator.CreateSubclass(
							errorNode, concreteVarType, concreteFilterClass)
						if IsTypeVar(varType) && !IsParamSpec(varType) &&
							!TypeVarTypeHasConstraints(varType.(*TypeVarType)) {
							newClassType = AddConditionToType(newClassType,
								[]TypeCondition{{TypeVar: varType.(*TypeVarType), ConstraintIndex: 0}}, nil)
						}

						filteredTypes = append(filteredTypes,
							AddConditionToType(newClassType, propsCondition(concreteVarType), nil))
					}
				} else {
					if IsAnyOrUnknown(varType) {
						filteredTypes = append(filteredTypes, AddConditionToType(varType, conditions, nil))
					} else if DerivesFromAnyOrUnknown(varType) &&
						!IsTypeSame(concreteVarType, concreteFilterType, TypeSameOptions{}, 0) {
						filteredTypes = append(filteredTypes, AddConditionToType(varType, conditions, nil))
					}
				}
			} else if IsTypeVar(filterType) && filterType.Base().IsInstantiable() {
				// The original's comment: handle the case where the filter type is Type[T]
				// and the unexpanded subtype is some instance type, possibly T.
				if varType.Base().IsInstance() {
					if IsTypeVar(varType) &&
						IsTypeSame(ConvertToInstance(filterType, true), varType, TypeSameOptions{}, 0) {
						// The original's comment: if the unexpanded subtype is T, we can
						// definitively filter in both the positive and negative cases.
						if isPositiveTest {
							filteredTypes = append(filteredTypes, varType)
						} else {
							foundSuperclass = true
						}
					} else {
						if isPositiveTest {
							filteredTypes = append(filteredTypes, ConvertToInstance(filterType, true))
						} else {
							// The original's comment: if the unexpanded subtype is some other
							// instance, we can't filter anything because it might be an instance.
							filteredTypes = append(filteredTypes, varType)
							isClassRelationshipIndeterminate = true
						}
					}
				}
			} else if IsFunction(filterType) {
				// The original's comment: handle an isinstance check against Callable.
				isCallable := false

				if IsClass(concreteVarType) {
					if varType.Base().IsInstantiable() {
						isCallable = true
					} else {
						isCallable = LookUpClassMember(
							concreteVarType, "__call__", MemberAccessFlagsSkipInstanceMembers, nil) != nil
					}
				}

				if isCallable {
					if isPositiveTest {
						filteredTypes = append(filteredTypes, ConvertToInstantiable(varType, true))
					} else {
						foundSuperclass = true
					}
				} else if evaluator.AssignType(
					convertVarTypeToFree(concreteVarType), filterType, nil, nil,
					AssignTypeFlagsAllowIsinstanceSpecialForms, 0) {
					if isPositiveTest {
						filteredTypes = append(filteredTypes,
							AddConditionToType(filterType, propsCondition(concreteVarType), nil))
					}
				} else if allowIntersections && isPositiveTest {
					// The original's comment: the type appears to not be callable. It's
					// possible that the two type is a subclass that is callable. We'll
					// synthesize a new intersection type.
					className := "<callable subtype of " + concreteVarType.Shared.Name + ">"
					fileInfo := GetFileInfo(errorNode)
					newClass := ClassTypeCreateInstantiable(
						className,
						GetClassFullName(errorNode, fileInfo.ModuleName, className),
						fileInfo.ModuleName,
						fileInfo.FileUri,
						ClassTypeFlagsNone,
						TypeSourceId(GetTypeSourceID(errorNode)),
						nil,
						concreteVarType.Shared.EffectiveMetaclass,
						concreteVarType.Shared.DocString,
					)
					newClass.Shared.BaseClasses = []Type{concreteVarType}
					ComputeMroLinearization(newClass)

					newClassType := AddConditionToType(newClass, propsCondition(concreteVarType), nil)
					newClass = newClassType.(*ClassType)

					// The original's comment: add a __call__ method to the new class.
					callMethod := FunctionTypeCreateSynthesizedInstance("__call__", FunctionTypeFlagsNone)
					selfName := "self"
					selfParam := FunctionParamCreate(
						parser.ParamCategorySimple,
						ClassTypeCloneAsInstance(newClass, true),
						FunctionParamFlagsTypeDeclared,
						&selfName,
						nil,
						nil,
					)
					FunctionTypeAddParam(callMethod, selfParam)
					FunctionTypeAddDefaultParams(callMethod, false)
					callMethod.Shared.DeclaredReturnType = UnknownTypeCreate(false)
					ClassTypeGetSymbolTable(newClass).Set(
						"__call__", SymbolCreateWithType(SymbolFlagsClassMember, callMethod, nil))

					filteredTypes = append(filteredTypes, ClassTypeCloneAsInstance(newClass, true))
				}
			}
		}

		// The original's comment: in the negative case, if one or more of the filters
		// always match the type (i.e. they are an exact match or a superclass of the
		// type), then there's nothing left after the filter is applied. If we didn't
		// find any superclass match, then the original variable type survives the
		// filter.
		if !isPositiveTest {
			if !foundSuperclass || isClassRelationshipIndeterminate {
				filteredTypes = append(filteredTypes, ConvertToInstantiable(negativeFallbackType, true))
			}
		}

		result := make([]Type, 0, len(filteredTypes))
		for _, ft := range filteredTypes {
			result = append(result, ConvertToInstance(ft, true))
		}
		return result
	}

	isFilterTypeCallbackProtocol := func(filterType Type) bool {
		return IsInstantiableClass(filterType) &&
			evaluator.GetCallbackProtocolType(
				ClassTypeCloneAsInstance(filterType.(*ClassType), true), 0) != nil
	}

	filterFunctionType := func(varType Type, unexpandedType Type) []Type {
		filteredTypes := []Type{}

		if isPositiveTest {
			for _, filterType := range filterTypes {
				concreteFilterType := evaluator.MakeTopLevelTypeVarsConcrete(filterType, false)

				if !isTypeIsCheck && isFilterTypeCallbackProtocol(concreteFilterType) {
					filteredTypes = append(filteredTypes, ConvertToInstance(varType, true))
				} else if evaluator.AssignType(
					convertVarTypeToFree(varType), ConvertToInstance(concreteFilterType, true),
					nil, nil, AssignTypeFlagsDefault, 0) {
					// The original's comment: if the filter type is a Callable, use the
					// original type. If the filter type is a callback protocol, use the filter
					// type.
					if IsFunction(filterType) {
						filteredTypes = append(filteredTypes, ConvertToInstance(unexpandedType, true))
					} else {
						filteredTypes = append(filteredTypes, ConvertToInstance(filterType, true))
					}
				} else {
					filterTypeInstance := ConvertToInstance(convertVarTypeToFree(concreteFilterType), true)
					if evaluator.AssignType(filterTypeInstance, varType, nil, nil, AssignTypeFlagsDefault, 0) {
						filteredTypes = append(filteredTypes, ConvertToInstance(varType, true))
					} else {
						// The original's comment: if this is a class instance that's not callable
						// and it's not @final, a subclass could be compatible with the filter
						// type.
						if IsClassInstance(filterTypeInstance) &&
							!ClassTypeIsFinal(filterTypeInstance.(*ClassType)) {
							gradualFunc := FunctionTypeCreateSynthesizedInstance(
								"", FunctionTypeFlagsGradualCallableForm)
							FunctionTypeAddDefaultParams(gradualFunc, false)

							// The original's comment: if the class is callable (i.e. can be
							// assigned to the generic gradual function signature), then the
							// assignment check above didn't fail because of a signature mismatch.
							// It failed because the class is not callable. We assume therefore that
							// a subclass might be.
							if !evaluator.AssignType(
								gradualFunc, filterTypeInstance, nil, nil, AssignTypeFlagsDefault, 0) {
								// The original's comment: the resulting type should be an
								// intersection of the filter type and the subtype, but we don't have a
								// way to encode that yet. For now, we'll use the filter type.
								filteredTypes = append(filteredTypes, ConvertToInstance(filterType, true))
							}
						}
					}
				}
			}
		} else {
			// The original's comment: if one or more filters does not always filter the
			// type, we can't eliminate the type in the negative case.
			allFilter := true
			for _, filterType := range filterTypes {
				concreteFilterType := evaluator.MakeTopLevelTypeVarsConcrete(filterType, false)

				// The original's comment: if the filter type is a callback protocol, the
				// runtime isinstance check will filter all objects that have a __call__
				// method regardless of their signature types.
				if !isTypeIsCheck && isFilterTypeCallbackProtocol(concreteFilterType) {
					allFilter = false
					break
				}

				if IsFunction(concreteFilterType) &&
					FunctionTypeIsGradualCallableForm(concreteFilterType.(*FunctionType)) {
					allFilter = false
					break
				}

				isSubtype := evaluator.AssignType(
					ConvertToInstance(convertVarTypeToFree(concreteFilterType), true),
					varType, nil, nil, AssignTypeFlagsDefault, 0)
				isSupertype := evaluator.AssignType(
					convertVarTypeToFree(varType),
					ConvertToInstance(concreteFilterType, true), nil, nil, AssignTypeFlagsDefault, 0)

				if isSubtype && !isSupertype {
					allFilter = false
					break
				}
			}

			if allFilter {
				filteredTypes = append(filteredTypes, ConvertToInstance(varType, true))
			}
		}

		return filteredTypes
	}

	classListContainsNoneType := func() bool {
		for _, ft := range filterTypes {
			if IsNoneTypeClass(ft) {
				return true
			}
			if IsInstantiableClass(ft) && ClassTypeIsBuiltInNamed(ft.(*ClassType), "NoneType") {
				return true
			}
		}
		return false
	}

	anyOrUnknownSubstitutions := []Type{}
	anyOrUnknown := []Type{}

	filteredType := evaluator.MapSubtypesExpandTypeVars(
		expandedTypes,
		&EvaluatorMapSubtypesOptions{
			ExpandCallback: func(t Type) Type {
				return evaluator.ExpandPromotionTypes(errorNode, t)
			},
		},
		func(subtype Type, unexpandedSubtype Type) Type {
			// The original's comment: if we fail to filter anything in the negative case,
			// we need to decide whether to retain the original TypeVar or replace it with
			// its specialized type(s). We'll assume that if someone is using isinstance or
			// issubclass on a constrained TypeVar that they want to filter based on its
			// constrained parts.
			negativeFallback := unexpandedSubtype
			if GetTypeCondition(subtype) != nil {
				negativeFallback = subtype
			}

			if isPositiveTest && IsAnyOrUnknown(subtype) {
				// The original's comment: if this is a positive test and the effective type
				// is Any or Unknown, we can assume that the type matches one of the specified
				// types.
				instances := make([]Type, 0, len(filterTypes))
				for _, classType := range filterTypes {
					instances = append(instances, ConvertToInstance(classType, true))
				}
				anyOrUnknownSubstitutions = append(anyOrUnknownSubstitutions, CombineTypes(instances, nil))

				anyOrUnknown = append(anyOrUnknown, subtype)
				return nil
			}

			if IsNoneInstance(subtype) {
				if classListContainsNoneType() == isPositiveTest {
					return subtype
				}
				return nil
			}

			if IsModule(subtype) ||
				(IsClassInstance(subtype) && ClassTypeIsBuiltInNamed(subtype.(*ClassType), "ModuleType")) {
				// The original's comment: handle type narrowing for runtime-checkable
				// protocols when applied to modules.
				if isPositiveTest {
					protocolFilters := []Type{}
					for _, classType := range filterTypes {
						concreteClassType := evaluator.MakeTopLevelTypeVarsConcrete(classType, false)
						if IsInstantiableClass(concreteClassType) &&
							ClassTypeIsProtocolClass(concreteClassType.(*ClassType)) {
							protocolFilters = append(protocolFilters, classType)
						}
					}

					if len(protocolFilters) > 0 {
						return ConvertToInstance(CombineTypes(protocolFilters, nil), true)
					}
				}
			}

			if IsClass(subtype) {
				return CombineTypes(filterClassType(
					unexpandedSubtype,
					ClassTypeCloneAsInstantiable(subtype.(*ClassType), true),
					GetTypeCondition(subtype),
					negativeFallback,
				), nil)
			}

			if IsFunctionOrOverloaded(subtype) {
				return CombineTypes(filterFunctionType(subtype, unexpandedSubtype), nil)
			}

			if isPositiveTest {
				return nil
			}
			return negativeFallback
		})

	// The original's comment: if the result is Any/Unknown and contains no other
	// subtypes and we have substitutions for Any/Unknown, use those instead. We
	// don't want to apply this if the filtering produced something other than
	// Any/Unknown. For example, if the statement is "isinstance(x, list)" and the
	// type of x is "List[str] | int | Any", the result should be "List[str]", not
	// "List[str] | List[Unknown]".
	if IsNever(filteredType) && len(anyOrUnknownSubstitutions) > 0 {
		return CombineTypes(anyOrUnknownSubstitutions, nil)
	}

	if IsNever(filteredType) && len(anyOrUnknown) > 0 {
		return CombineTypes(anyOrUnknown, nil)
	}

	return filteredType
}

// intersectSameClassType corresponds to the function of the same name.
//
// The original's comment: this function assumes that the caller has already
// verified that the two types are the same class and are not literals. It also
// assumes that the caller has verified that type1 is not assignable to type2 or
// vice versa. Returns undefined if there is no intersection between the two
// types.
func intersectSameClassType(evaluator TypeEvaluator, type1 *ClassType, type2 *ClassType) *ClassType {
	common.Assert(IsInstantiableClass(type1) && IsInstantiableClass(type2),
		"Expected both types to be instantiable classes")
	common.Assert(ClassTypeIsSameGenericClass(type1, type2, 0), "Expected the same generic class")
	common.Assert(type1.Priv.LiteralValue == nil, "Expected type1 to not be a literal")
	common.Assert(type2.Priv.LiteralValue == nil, "Expected type2 to not be a literal")

	// The original's comment: handle tuples specially.
	if ClassTypeIsBuiltInNamed(type1, "tuple") {
		// The original passes type1 twice here; it is not a typo the port is free to
		// correct, because doing so would change which type's arguments survive.
		return intersectTupleTypes(type1, type1)
	}

	// The original's comment: indicate that there is no intersection.
	return nil
}

func intersectTupleTypes(type1 *ClassType, type2 *ClassType) *ClassType {
	if type2.Priv.TupleTypeArgs == nil || IsTupleGradualForm(type2) {
		result := AddConditionToType(type1, propsCondition(type2), nil)
		if cls, ok := result.(*ClassType); ok {
			return cls
		}
		return nil
	}

	if type1.Priv.TupleTypeArgs == nil || IsTupleGradualForm(type1) {
		result := AddConditionToType(type2, propsCondition(type1), nil)
		if cls, ok := result.(*ClassType); ok {
			return cls
		}
		return nil
	}

	// The original's comment: for now, don't attempt to narrow in this case.
	// TODO - add more sophisticated logic here.
	return nil
}
