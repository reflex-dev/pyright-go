/*
 * typeprinter_print.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The six mutually-recursive printer functions from analyzer/typePrinter.ts
 * (pyright 1.1.412): printTypeInternal (196), printUnionType (670),
 * printFunctionType (827), printParamSpecValueForPythonSyntax (948),
 * printObjectTypeForClassInternal (989) and printFunctionPartsInternal (1162).
 *
 * Two transliteration hazards recur throughout this file.
 *
 * First, recursionTypes is threaded as a *[]Type. The original pushes the type
 * onto the caller's array and pops it in a `finally`; a Go slice passed by
 * value would hide those mutations from the caller. Where the original wraps a
 * `return` in try/finally, this uses `defer` (when the try block runs to the
 * end of the function) or an immediately-invoked closure (when it does not).
 *
 * Second, an empty JavaScript array is truthy. `if (x.tupleTypeArgs)` is taken
 * for `[]`, so those checks transliterate to `!= nil`, never to `len(x) > 0`.
 * Each such site is marked.
 */

package analyzer

import (
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// printTypeInternal corresponds to the function of the same name.
func printTypeInternal(
	t Type,
	printTypeFlags PrintTypeFlags,
	returnTypeCallback FunctionReturnTypeCallback,
	uniqueNameMap *UniqueNameMap,
	recursionTypes *[]Type,
	recursionCount int,
) string {
	if recursionCount > MaxTypeRecursionCount {
		if printTypeFlags&PrintTypeFlagsPythonSyntax != 0 {
			return "Any"
		}
		return "<Recursive>"
	}
	recursionCount++

	originalPrintTypeFlags := printTypeFlags
	parenthesizeUnion := (printTypeFlags & PrintTypeFlagsParenthesizeUnion) != 0
	printTypeFlags &^= PrintTypeFlagsParenthesizeUnion | PrintTypeFlagsParenthesizeCallable

	// If this is a type alias, see if we should use its name rather than
	// the type it represents.
	var aliasInfo *TypeAliasInfo
	if t.Base().Props != nil {
		aliasInfo = t.Base().Props.TypeAliasInfo
	}

	if aliasInfo != nil {
		expandTypeAlias := true
		if (printTypeFlags & PrintTypeFlagsExpandTypeAlias) == 0 {
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
			// The original's try/finally does not run to the end of the
			// function -- control falls through to the recursion check below
			// when the type is a TypeVar -- so this is a closure rather than a
			// deferred pop.
			aliasName, returned := func() (string, bool) {
				*recursionTypes = append(*recursionTypes, t)
				defer func() { *recursionTypes = (*recursionTypes)[:len(*recursionTypes)-1] }()

				aliasName := aliasInfo.Shared.Name
				if (printTypeFlags & PrintTypeFlagsUseFullyQualifiedNames) != 0 {
					aliasName = aliasInfo.Shared.FullName
				}

				// Use the fully-qualified name if the name isn't unique.
				if !uniqueNameMap.IsUnique(aliasName) {
					aliasName = aliasInfo.Shared.FullName
				}

				typeParams := aliasInfo.Shared.TypeParams

				if len(typeParams) > 0 {
					var argumentStrings []string
					haveArgumentStrings := false

					// If there is a type arguments array, it's a specialized
					// type alias. An empty array is truthy in JavaScript, so
					// this is a nil check.
					if aliasInfo.TypeArgs != nil {
						if (printTypeFlags&PrintTypeFlagsOmitTypeArgsIfUnknown) == 0 ||
							someTypeNotUnknown(aliasInfo.TypeArgs) {
							haveArgumentStrings = true
							argumentStrings = []string{}

							for index, typeArg := range aliasInfo.TypeArgs {
								// Which type parameter does this map to?
								var typeParam *TypeVarType
								if index < len(typeParams) {
									typeParam = typeParams[index]
								} else {
									typeParam = typeParams[len(typeParams)-1]
								}

								// If this type argument maps to a TypeVarTuple, unpack it.
								tupleArg, isTuple := AsClassInstance(typeArg)
								if IsTypeVarTuple(typeParam) && isTuple && IsTupleClass(tupleArg) &&
									tupleArg.Priv.TupleTypeArgs != nil &&
									everyTupleArgBounded(tupleArg.Priv.TupleTypeArgs) {
									for _, tupleTypeArg := range tupleArg.Priv.TupleTypeArgs {
										argumentStrings = append(argumentStrings, printTypeInternal(
											tupleTypeArg.Type,
											printTypeFlags,
											returnTypeCallback,
											uniqueNameMap,
											recursionTypes,
											recursionCount,
										))
									}
								} else {
									argumentStrings = append(argumentStrings, printTypeInternal(
										typeArg,
										printTypeFlags,
										returnTypeCallback,
										uniqueNameMap,
										recursionTypes,
										recursionCount,
									))
								}
							}
						}
					} else {
						if (printTypeFlags&PrintTypeFlagsOmitTypeArgsIfUnknown) == 0 ||
							someTypeVarNotUnknown(typeParams) {
							haveArgumentStrings = true
							argumentStrings = []string{}

							for _, typeParam := range typeParams {
								argumentStrings = append(argumentStrings, printTypeInternal(
									typeParam,
									printTypeFlags,
									returnTypeCallback,
									uniqueNameMap,
									recursionTypes,
									recursionCount,
								))
							}
						}
					}

					if haveArgumentStrings {
						if len(argumentStrings) == 0 {
							aliasName += "[()]"
						} else {
							aliasName += "[" + strings.Join(argumentStrings, ", ") + "]"
						}
					}
				}

				// If it's a TypeVar, don't use the alias name. Instead, use the
				// full name, which may have a scope associated with it.
				if t.Base().Category != TypeCategoryTypeVar {
					return aliasName, true
				}

				return "", false
			}()

			if returned {
				return aliasName
			}
		}
	}

	alreadyRecursed := false
	for _, rt := range *recursionTypes {
		if rt == t {
			alreadyRecursed = true
			break
		}
		rtProps := rt.Base().Props
		if rtProps != nil && rtProps.TypeAliasInfo != nil && aliasInfo != nil &&
			rtProps.TypeAliasInfo.Shared.FullName == aliasInfo.Shared.FullName {
			alreadyRecursed = true
			break
		}
	}

	if alreadyRecursed || len(*recursionTypes) > MaxTypeRecursionCount {
		// If this is a recursive TypeVar, we've already expanded it once, so
		// just print its name at this point.
		if typeVar, ok := AsTypeVar(t); ok && typeVar.Shared.IsSynthesized && typeVar.Shared.RecursiveAlias != nil {
			return typeVar.Shared.RecursiveAlias.Name
		}

		if aliasInfo != nil {
			// Empty arrays are truthy, so `!aliasInfo.shared.typeParams` is a
			// nil check, not a length check.
			if aliasInfo.Shared.TypeParams == nil {
				name := aliasInfo.Shared.Name
				if (printTypeFlags & PrintTypeFlagsUseFullyQualifiedNames) != 0 {
					name = aliasInfo.Shared.FullName
				}
				if !uniqueNameMap.IsUnique(name) {
					name = aliasInfo.Shared.FullName
				}
				return name
			}

			*recursionTypes = append(*recursionTypes, t)
			result := printTypeInternal(
				t,
				printTypeFlags&^PrintTypeFlagsExpandTypeAlias,
				returnTypeCallback,
				uniqueNameMap,
				recursionTypes,
				recursionCount,
			)
			*recursionTypes = (*recursionTypes)[:len(*recursionTypes)-1]
			return result
		}

		return "..."
	}

	// The original's try block extends to the end of the function, so the
	// finally becomes a defer.
	*recursionTypes = append(*recursionTypes, t)
	defer func() { *recursionTypes = (*recursionTypes)[:len(*recursionTypes)-1] }()

	includeConditionalIndicator :=
		(printTypeFlags & (PrintTypeFlagsOmitConditionalConstraint | PrintTypeFlagsPythonSyntax)) == 0
	getConditionalIndicator := func(subtype Type) string {
		props := subtype.Base().Props
		// `!!subtype.props?.condition` is true for an empty array.
		if props != nil && props.Condition != nil && includeConditionalIndicator {
			return "*"
		}
		return ""
	}
	printWrappedType := func(t Type, typeToWrap string) string {
		return printNestedInstantiable(t, typeToWrap) + getConditionalIndicator(t)
	}

	switch t.Base().Category {
	case TypeCategoryUnbound:
		if printTypeFlags&PrintTypeFlagsPythonSyntax != 0 {
			return "Any"
		}
		return "Unbound"

	case TypeCategoryUnknown:
		if printTypeFlags&(PrintTypeFlagsPythonSyntax|PrintTypeFlagsPrintUnknownWithAny) != 0 {
			return "Any"
		}
		return "Unknown"

	case TypeCategoryModule:
		if printTypeFlags&PrintTypeFlagsPythonSyntax != 0 {
			return "Any"
		}
		return `Module("` + t.(*ModuleType).Priv.ModuleName + `")`

	case TypeCategoryClass:
		classType := t.(*ClassType)

		if classType.IsInstance() {
			if classType.Priv.LiteralValue != nil {
				if IsLiteralValueTruncated(classType) && (printTypeFlags&PrintTypeFlagsPythonSyntax) != 0 {
					return PrintLiteralValueTruncated(classType)
				} else if sentinel, ok := classType.Priv.LiteralValue.(*SentinelLiteral); ok {
					return sentinel.ClassName
				} else {
					return "Literal[" + PrintLiteralValue(classType, "'") + "]"
				}
			}

			return printObjectTypeForClassInternal(
				classType,
				printTypeFlags,
				returnTypeCallback,
				uniqueNameMap,
				recursionTypes,
				recursionCount,
			) + getConditionalIndicator(t)
		}

		var typeToWrap string

		if classType.Priv.LiteralValue != nil {
			if IsLiteralValueTruncated(classType) && (printTypeFlags&PrintTypeFlagsPythonSyntax) != 0 {
				typeToWrap = PrintLiteralValueTruncated(classType)
			} else if sentinel, ok := classType.Priv.LiteralValue.(*SentinelLiteral); ok {
				return sentinel.ClassName
			} else {
				typeToWrap = "Literal[" + PrintLiteralValue(classType, "'") + "]"
			}

			return printWrappedType(t, typeToWrap)
		}

		if classType.Props != nil && classType.Props.SpecialForm != nil {
			return printTypeInternal(
				classType.Props.SpecialForm,
				printTypeFlags,
				returnTypeCallback,
				uniqueNameMap,
				recursionTypes,
				recursionCount,
			)
		}

		typeToWrap = printObjectTypeForClassInternal(
			classType,
			printTypeFlags,
			returnTypeCallback,
			uniqueNameMap,
			recursionTypes,
			recursionCount,
		)

		return printWrappedType(t, typeToWrap)

	case TypeCategoryFunction:
		functionType := t.(*FunctionType)

		if functionType.IsInstantiable() {
			typeString := printFunctionType(
				FunctionTypeCloneAsInstance(functionType),
				printTypeFlags,
				returnTypeCallback,
				uniqueNameMap,
				recursionTypes,
				recursionCount,
			)
			return "type[" + typeString + "]"
		}

		return printFunctionType(
			functionType,
			originalPrintTypeFlags,
			returnTypeCallback,
			uniqueNameMap,
			recursionTypes,
			recursionCount,
		)

	case TypeCategoryOverloaded:
		overloadTypes := OverloadedTypeGetOverloads(t.(*OverloadedType))
		overloads := make([]string, 0, len(overloadTypes))
		for _, overload := range overloadTypes {
			overloads = append(overloads, printTypeInternal(
				overload,
				printTypeFlags,
				returnTypeCallback,
				uniqueNameMap,
				recursionTypes,
				recursionCount,
			))
		}

		if (printTypeFlags & PrintTypeFlagsPythonSyntax) != 0 {
			return "Callable[..., Any]"
		}

		if len(overloads) == 1 {
			return overloads[0]
		}

		return "Overload[" + strings.Join(overloads, ", ") + "]"

	case TypeCategoryUnion:
		unionType := t.(*UnionType)

		// If this is a value expression that evaluates to a union type but is
		// not a type alias, simply print the special form ("UnionType").
		if unionType.IsInstantiable() && unionType.Props != nil && unionType.Props.SpecialForm != nil &&
			unionType.Props.TypeAliasInfo == nil {
			return printTypeInternal(
				unionType.Props.SpecialForm,
				printTypeFlags,
				returnTypeCallback,
				uniqueNameMap,
				recursionTypes,
				recursionCount,
			)
		}

		// If we're using "|" notation, enclose callable subtypes in parens.
		updatedPrintTypeFlags := printTypeFlags
		if printTypeFlags&PrintTypeFlagsPEP604 != 0 {
			updatedPrintTypeFlags = printTypeFlags | PrintTypeFlagsParenthesizeCallable
		}

		return printUnionType(
			unionType,
			updatedPrintTypeFlags,
			parenthesizeUnion,
			returnTypeCallback,
			uniqueNameMap,
			recursionTypes,
			recursionCount,
		)

	case TypeCategoryTypeVar:
		typeVar := t.(*TypeVarType)

		// If it's synthesized, don't expose the internal name we generated.
		// This will confuse users. The exception is if it's a bound synthesized
		// type, in which case we'll print the bound type. This is used for
		// "self" and "cls" parameters.
		if typeVar.Shared.IsSynthesized {
			// If it's a synthesized type var used to implement recursive type
			// aliases, return the type alias name.
			if typeVar.Shared.RecursiveAlias != nil {
				if (printTypeFlags&PrintTypeFlagsExpandTypeAlias) != 0 && typeVar.Shared.BoundType != nil {
					boundType := typeVar.Shared.BoundType
					if typeVar.IsInstance() {
						boundType = ConvertToInstance(boundType, true)
					}
					return printTypeInternal(
						boundType,
						printTypeFlags,
						returnTypeCallback,
						uniqueNameMap,
						recursionTypes,
						recursionCount,
					)
				}
				return typeVar.Shared.RecursiveAlias.Name
			}

			// If it's a synthesized type var used to implement `self` or `cls`
			// types, print the type with a special character that indicates
			// that the type is internally represented as a TypeVar.
			if TypeVarTypeIsSelf(typeVar) && typeVar.Shared.BoundType != nil {
				boundTypeString := printTypeInternal(
					typeVar.Shared.BoundType,
					printTypeFlags&^PrintTypeFlagsExpandTypeAlias,
					returnTypeCallback,
					uniqueNameMap,
					recursionTypes,
					recursionCount,
				)

				if !IsAnyOrUnknown(typeVar.Shared.BoundType) {
					if (printTypeFlags&PrintTypeFlagsPythonSyntax) == 0 &&
						(printTypeFlags&PrintTypeFlagsOmitTypeVarScope) == 0 {
						boundTypeString = "Self@" + boundTypeString
					} else {
						boundTypeString = "Self"
					}
				}

				if typeVar.IsInstantiable() {
					return printNestedInstantiable(t, boundTypeString)
				}

				return boundTypeString
			}

			if (printTypeFlags & (PrintTypeFlagsPrintUnknownWithAny | PrintTypeFlagsPythonSyntax)) != 0 {
				return "Any"
			}
			return "Unknown"
		}

		includeScope := (printTypeFlags&PrintTypeFlagsPythonSyntax) == 0 &&
			(printTypeFlags&PrintTypeFlagsOmitTypeVarScope) == 0

		if IsParamSpec(t) {
			paramSpecText := getReadableTypeVarName(typeVar, includeScope)

			if access := paramSpecAccessText(typeVar.Priv.ParamSpecAccess); access != "" {
				return paramSpecText + "." + access
			}
			return paramSpecText
		}

		typeVarName := getReadableTypeVarName(typeVar, includeScope)

		if typeVar.Priv.IsUnpacked {
			typeVarName = printUnpack(typeVarName, printTypeFlags)
		}

		if IsTypeVarTuple(t) && typeVar.Priv.IsInUnion {
			typeVarName = "Union[" + typeVarName + "]"
		}

		if typeVar.IsInstantiable() {
			typeVarName = printNestedInstantiable(t, typeVarName)
		}

		if !IsTypeVarTuple(t) && (printTypeFlags&PrintTypeFlagsPrintTypeVarVariance) != 0 {
			varianceText := getTypeVarVarianceText(typeVar)
			if varianceText != "" {
				typeVarName = typeVarName + " (" + varianceText + ")"
			}
		}

		return typeVarName

	case TypeCategoryNever:
		if t.(*NeverType).Priv.IsNoReturn {
			return "NoReturn"
		}
		return "Never"

	case TypeCategoryAny:
		if t.(*AnyType).Priv.IsEllipsis {
			return "..."
		}
		return "Any"
	}

	return ""
}

// printUnionType corresponds to the function of the same name.
//
// The three string collections below are JavaScript Sets whose iteration order
// reaches the printed output: each is drained into an array and joined. They
// must preserve insertion order, so they are OrderedSets rather than Go maps.
func printUnionType(
	t *UnionType,
	printTypeFlags PrintTypeFlags,
	parenthesizeUnion bool,
	returnTypeCallback FunctionReturnTypeCallback,
	uniqueNameMap *UniqueNameMap,
	recursionTypes *[]Type,
	recursionCount int,
) string {
	// Allocate a set that refers to subtypes in the union by their indices. If
	// the index is within the set, it is already accounted for in the output.
	subtypeHandledSet := common.NewOrderedSet[int]()

	// Allocate another set that represents the textual representations of the
	// subtypes in the union.
	subtypeStrings := common.NewOrderedSet[string]()

	// Start by matching possible type aliases to the subtypes.
	if (printTypeFlags&PrintTypeFlagsExpandTypeAlias) == 0 && t.Priv.TypeAliasSources != nil {
		for _, typeAliasSource := range t.Priv.TypeAliasSources.Values() {
			matchedAllSubtypes := true
			allSubtypesPreviouslyHandled := true
			indicesCoveredByTypeAlias := common.NewOrderedSet[int]()

			for _, sourceSubtype := range typeAliasSource.Priv.Subtypes {
				unionSubtypeIndex := 0
				foundMatch := false
				sourceSubtypeInstance := ConvertToInstance(sourceSubtype, true)

				for _, unionSubtype := range t.Priv.Subtypes {
					if IsTypeSame(sourceSubtypeInstance, unionSubtype, TypeSameOptions{}, 0) {
						if !subtypeHandledSet.Has(unionSubtypeIndex) {
							allSubtypesPreviouslyHandled = false
						}
						indicesCoveredByTypeAlias.Add(unionSubtypeIndex)
						foundMatch = true
						break
					}

					unionSubtypeIndex++
				}

				if !foundMatch {
					matchedAllSubtypes = false
					break
				}
			}

			if matchedAllSubtypes && !allSubtypesPreviouslyHandled {
				subtypeStrings.Add(printTypeInternal(
					typeAliasSource,
					printTypeFlags,
					returnTypeCallback,
					uniqueNameMap,
					recursionTypes,
					recursionCount,
				))
				indicesCoveredByTypeAlias.ForEach(func(index int) { subtypeHandledSet.Add(index) })
			}
		}
	}

	noneIndex := -1
	for index, subtype := range t.Priv.Subtypes {
		if IsNoneInstance(subtype) {
			noneIndex = index
			break
		}
	}

	if noneIndex >= 0 && !subtypeHandledSet.Has(noneIndex) {
		typeWithoutNone := RemoveNoneFromUnion(t)
		if IsNever(typeWithoutNone) {
			return "None"
		}

		optionalType := printTypeInternal(
			typeWithoutNone,
			printTypeFlags,
			returnTypeCallback,
			uniqueNameMap,
			recursionTypes,
			recursionCount,
		)

		if printTypeFlags&PrintTypeFlagsPEP604 != 0 {
			unionString := optionalType + " | None"
			if parenthesizeUnion {
				return "(" + unionString + ")"
			}
			return unionString
		}

		return "Optional[" + optionalType + "]"
	}

	literalObjectStrings := common.NewOrderedSet[string]()
	literalClassStrings := common.NewOrderedSet[string]()
	DoForEachSubtype(t, func(subtype Type, index int, allSubtypes []Type) {
		if subtypeHandledSet.Has(index) {
			return
		}

		if instance, ok := AsClassInstance(subtype); ok && instance.Priv.LiteralValue != nil && !IsSentinelLiteral(subtype) {
			if IsLiteralValueTruncated(instance) && (printTypeFlags&PrintTypeFlagsPythonSyntax) != 0 {
				subtypeStrings.Add(PrintLiteralValueTruncated(instance))
			} else {
				literalObjectStrings.Add(PrintLiteralValue(instance, "'"))
			}
		} else if instantiable, ok := AsInstantiableClass(subtype); ok && instantiable.Priv.LiteralValue != nil &&
			!IsSentinelLiteral(subtype) {
			if IsLiteralValueTruncated(instantiable) && (printTypeFlags&PrintTypeFlagsPythonSyntax) != 0 {
				subtypeStrings.Add("type[" + PrintLiteralValueTruncated(instantiable) + "]")
			} else {
				literalClassStrings.Add(PrintLiteralValue(instantiable, "'"))
			}
		} else {
			subtypeStrings.Add(printTypeInternal(
				subtype,
				printTypeFlags,
				returnTypeCallback,
				uniqueNameMap,
				recursionTypes,
				recursionCount,
			))
		}
	})

	dedupedSubtypeStrings := []string{}
	subtypeStrings.ForEach(func(s string) { dedupedSubtypeStrings = append(dedupedSubtypeStrings, s) })

	if literalObjectStrings.Size() > 0 {
		literalStrings := []string{}
		literalObjectStrings.ForEach(func(s string) { literalStrings = append(literalStrings, s) })
		dedupedSubtypeStrings = append(dedupedSubtypeStrings, "Literal["+strings.Join(literalStrings, ", ")+"]")
	}

	if literalClassStrings.Size() > 0 {
		literalStrings := []string{}
		literalClassStrings.ForEach(func(s string) { literalStrings = append(literalStrings, s) })
		dedupedSubtypeStrings = append(dedupedSubtypeStrings, "type[Literal["+strings.Join(literalStrings, ", ")+"]]")
	}

	if len(dedupedSubtypeStrings) == 1 {
		return dedupedSubtypeStrings[0]
	}

	if printTypeFlags&PrintTypeFlagsPEP604 != 0 {
		unionString := strings.Join(dedupedSubtypeStrings, " | ")
		if parenthesizeUnion {
			return "(" + unionString + ")"
		}
		return unionString
	}

	return "Union[" + strings.Join(dedupedSubtypeStrings, ", ") + "]"
}

// printFunctionType corresponds to the function of the same name.
func printFunctionType(
	t *FunctionType,
	printTypeFlags PrintTypeFlags,
	returnTypeCallback FunctionReturnTypeCallback,
	uniqueNameMap *UniqueNameMap,
	recursionTypes *[]Type,
	recursionCount int,
) string {
	if printTypeFlags&PrintTypeFlagsPythonSyntax != 0 {
		if FunctionTypeIsParamSpecValue(t) {
			if paramSpecValueString, ok := printParamSpecValueForPythonSyntax(
				t,
				printTypeFlags,
				returnTypeCallback,
				uniqueNameMap,
				recursionTypes,
				recursionCount,
			); ok {
				return paramSpecValueString
			}
		}

		paramSpec := FunctionTypeGetParamSpecFromArgsKwargs(t)
		typeWithoutParamSpec := t
		if paramSpec != nil {
			// The TypeScript defaults stripPositionOnlySeparator to false.
			typeWithoutParamSpec = FunctionTypeCloneRemoveParamSpecArgsKwargs(t, false)
		}

		// Callable works only in cases where all parameters are positional-only.
		isPositionalParamsOnly := false
		if len(typeWithoutParamSpec.Shared.Parameters) == 0 {
			isPositionalParamsOnly = true
		} else {
			allSimple := true
			for _, param := range typeWithoutParamSpec.Shared.Parameters {
				if param.Category != parser.ParamCategorySimple {
					allSimple = false
					break
				}
			}
			if allSimple {
				lastParam := typeWithoutParamSpec.Shared.Parameters[len(typeWithoutParamSpec.Shared.Parameters)-1]
				if !isTruthyName(lastParam.Name) {
					isPositionalParamsOnly = true
				}
			}
		}

		returnType := returnTypeCallback(typeWithoutParamSpec)
		returnTypeString := "Any"
		if returnType != nil {
			returnTypeString = printTypeInternal(
				returnType,
				printTypeFlags,
				returnTypeCallback,
				uniqueNameMap,
				recursionTypes,
				recursionCount,
			)
		}

		if isPositionalParamsOnly {
			paramTypes := []string{}

			for index, param := range typeWithoutParamSpec.Shared.Parameters {
				if !isTruthyName(param.Name) {
					continue
				}

				paramType := FunctionTypeGetParamType(typeWithoutParamSpec, index)
				if len(*recursionTypes) < MaxTypeRecursionCount {
					paramTypes = append(paramTypes, printTypeInternal(
						paramType,
						printTypeFlags,
						returnTypeCallback,
						uniqueNameMap,
						recursionTypes,
						recursionCount,
					))
				} else {
					paramTypes = append(paramTypes, "Any")
				}
			}

			if paramSpec != nil {
				if len(paramTypes) > 0 {
					return "Callable[Concatenate[" + strings.Join(paramTypes, ", ") + ", " +
						paramSpec.Shared.Name + "], " + returnTypeString + "]"
				}

				return "Callable[" + paramSpec.Shared.Name + ", " + returnTypeString + "]"
			}

			return "Callable[[" + strings.Join(paramTypes, ", ") + "], " + returnTypeString + "]"
		}

		// We can't represent this type using a Callable so default to a
		// "catch all" Callable.
		return "Callable[..., " + returnTypeString + "]"
	}

	params, returnTypeString := printFunctionPartsInternal(
		t,
		printTypeFlags,
		returnTypeCallback,
		uniqueNameMap,
		recursionTypes,
		recursionCount,
	)
	paramSignature := "(" + strings.Join(params, ", ") + ")"

	if FunctionTypeIsParamSpecValue(t) {
		if len(params) == 1 && params[0] == "..." {
			return params[0]
		}

		return paramSignature
	}

	fullSignature := paramSignature + " -> " + returnTypeString
	parenthesizeCallable := (printTypeFlags & PrintTypeFlagsParenthesizeCallable) != 0
	if parenthesizeCallable {
		return "(" + fullSignature + ")"
	}

	return fullSignature
}

// printParamSpecValueForPythonSyntax corresponds to the function of the same
// name. The original returns `string | undefined`, where undefined means the
// caller should fall through; that becomes a second boolean result.
func printParamSpecValueForPythonSyntax(
	t *FunctionType,
	printTypeFlags PrintTypeFlags,
	returnTypeCallback FunctionReturnTypeCallback,
	uniqueNameMap *UniqueNameMap,
	recursionTypes *[]Type,
	recursionCount int,
) (string, bool) {
	if FunctionTypeGetParamSpecFromArgsKwargs(t) != nil {
		return "", false
	}

	for _, param := range t.Shared.Parameters {
		if param.Category != parser.ParamCategorySimple {
			return "", false
		}
	}

	paramTypes := []string{}
	for index, param := range t.Shared.Parameters {
		if !isTruthyName(param.Name) {
			continue
		}

		paramType := FunctionTypeGetParamType(t, index)
		if len(*recursionTypes) < MaxTypeRecursionCount {
			paramTypes = append(paramTypes, printTypeInternal(
				paramType,
				printTypeFlags,
				returnTypeCallback,
				uniqueNameMap,
				recursionTypes,
				recursionCount,
			))
		} else {
			paramTypes = append(paramTypes, "Any")
		}
	}

	return "[" + strings.Join(paramTypes, ", ") + "]", true
}

// printObjectTypeForClassInternal corresponds to the function of the same name.
func printObjectTypeForClassInternal(
	t *ClassType,
	printTypeFlags PrintTypeFlags,
	returnTypeCallback FunctionReturnTypeCallback,
	uniqueNameMap *UniqueNameMap,
	recursionTypes *[]Type,
	recursionCount int,
) string {
	objName := ""
	if t.Priv.AliasName() != nil {
		objName = *t.Priv.AliasName()
	}
	if objName == "" {
		if (printTypeFlags & PrintTypeFlagsUseFullyQualifiedNames) != 0 {
			objName = t.Shared.FullName
		} else {
			objName = t.Shared.Name
		}
	}

	// Special-case NoneType to convert it to None.
	if ClassTypeIsBuiltInNamed(t, "NoneType") {
		objName = "None"
	}

	// Use the fully-qualified name if the name isn't unique.
	if !uniqueNameMap.IsUnique(objName) {
		objName = t.Shared.FullName
	}

	// If this is a pseudo-generic class, don't display the type arguments
	// or type parameters because it will confuse users.
	if !ClassTypeIsPseudoGenericClass(t) {
		typeParams := ClassTypeGetTypeParams(t)
		var lastTypeParam *TypeVarType
		if len(typeParams) > 0 {
			lastTypeParam = typeParams[len(typeParams)-1]
		}
		isVariadic := lastTypeParam != nil && IsTypeVarTuple(lastTypeParam)

		// If there is a type arguments array, it's a specialized class. Both
		// arms are nil checks rather than length checks: an empty array is
		// truthy in JavaScript, and `??` falls through only on nullish.
		var typeArgs []*TupleTypeArg
		haveTypeArgs := false
		if t.Priv.TupleTypeArgs != nil {
			typeArgs = t.Priv.TupleTypeArgs
			haveTypeArgs = true
		} else if t.Priv.TypeArgs != nil {
			typeArgs = make([]*TupleTypeArg, 0, len(t.Priv.TypeArgs))
			for _, typeArg := range t.Priv.TypeArgs {
				typeArgs = append(typeArgs, &TupleTypeArg{Type: typeArg, IsUnbounded: false})
			}
			haveTypeArgs = true
		}

		if haveTypeArgs {
			// Handle Tuple[()] as a special case.
			if len(typeArgs) > 0 {
				typeArgStrings := []string{}
				isAllUnknown := true

				for index, typeArg := range typeArgs {
					var typeParam *TypeVarType
					if index < len(typeParams) {
						typeParam = typeParams[index]
					}

					tupleArg, isTupleArg := AsClassInstance(typeArg.Type)
					if typeParam != nil && IsTypeVarTuple(typeParam) && isTupleArg &&
						ClassTypeIsBuiltInNamed(tupleArg, "tuple") && tupleArg.Priv.TupleTypeArgs != nil {
						// Expand the tuple type that maps to the TypeVarTuple.
						if len(tupleArg.Priv.TupleTypeArgs) == 0 {
							if !IsUnknown(typeArg.Type) {
								isAllUnknown = false
							}

							if index == 0 {
								typeArgStrings = append(typeArgStrings, printUnpack("tuple[()]", printTypeFlags))
							}
						} else {
							expanded := make([]string, 0, len(tupleArg.Priv.TupleTypeArgs))
							for _, innerTypeArg := range tupleArg.Priv.TupleTypeArgs {
								if !IsUnknown(innerTypeArg.Type) {
									isAllUnknown = false
								}

								typeArgText := printTypeInternal(
									innerTypeArg.Type,
									printTypeFlags,
									returnTypeCallback,
									uniqueNameMap,
									recursionTypes,
									recursionCount,
								)

								if innerTypeArg.IsUnbounded {
									expanded = append(expanded, printUnpack("tuple["+typeArgText+", ...]", printTypeFlags))
								} else {
									expanded = append(expanded, typeArgText)
								}
							}
							typeArgStrings = common.AppendArray(typeArgStrings, expanded)
						}
					} else {
						if !IsUnknown(typeArg.Type) {
							isAllUnknown = false
						}

						typeArgTypeText := printTypeInternal(
							typeArg.Type,
							printTypeFlags,
							returnTypeCallback,
							uniqueNameMap,
							recursionTypes,
							recursionCount,
						)

						if typeArg.IsUnbounded {
							if len(typeArgs) == 1 {
								typeArgStrings = append(typeArgStrings, typeArgTypeText, "...")
							} else {
								typeArgStrings = append(typeArgStrings,
									printUnpack("tuple["+typeArgTypeText+", ...]", printTypeFlags))
							}
						} else {
							typeArgStrings = append(typeArgStrings, typeArgTypeText)
						}
					}
				}

				if t.Priv.IsUnpacked {
					objName = printUnpack(objName, printTypeFlags)
				}

				if (printTypeFlags&PrintTypeFlagsOmitTypeArgsIfUnknown) == 0 || !isAllUnknown {
					// In PythonSyntax mode, omit type args for classes that are
					// generic only in stubs but not subscriptable at runtime
					// (e.g. operator.attrgetter, operator.itemgetter).
					if !((printTypeFlags&PrintTypeFlagsPythonSyntax) != 0 && IsStubOnlySubscriptable(t)) {
						objName += "[" + strings.Join(typeArgStrings, ", ") + "]"
					}
				}
			} else {
				if t.Priv.IsUnpacked {
					objName = printUnpack(objName, printTypeFlags)
				}

				if ClassTypeIsTupleClass(t) || isVariadic {
					objName += "[()]"
				}
			}
		} else {
			if t.Priv.IsUnpacked {
				objName = printUnpack(objName, printTypeFlags)
			}

			if len(typeParams) > 0 {
				if (printTypeFlags&PrintTypeFlagsOmitTypeArgsIfUnknown) == 0 ||
					someTypeVarNotUnknown(typeParams) {
					typeParamStrings := make([]string, 0, len(typeParams))
					for _, typeParam := range typeParams {
						typeParamStrings = append(typeParamStrings, printTypeInternal(
							typeParam,
							printTypeFlags,
							returnTypeCallback,
							uniqueNameMap,
							recursionTypes,
							recursionCount,
						))
					}
					objName += "[" + strings.Join(typeParamStrings, ", ") + "]"
				}
			}
		}
	}

	// Wrap in a "Partial" for TypedDict that has been synthesized as partial.
	if t.Priv.IsTypedDictPartial() {
		if (printTypeFlags & PrintTypeFlagsPythonSyntax) == 0 {
			objName = "Partial[" + objName + "]"
		}
	}

	return objName
}

// printFunctionPartsInternal corresponds to the function of the same name. The
// TypeScript returns the tuple [string[], string]; that becomes two results.
func printFunctionPartsInternal(
	t *FunctionType,
	printTypeFlags PrintTypeFlags,
	returnTypeCallback FunctionReturnTypeCallback,
	uniqueNameMap *UniqueNameMap,
	recursionTypes *[]Type,
	recursionCount int,
) ([]string, string) {
	paramTypeStrings := []string{}
	sawDefinedName := false

	// Remove the (*args: P.args, **kwargs: P.kwargs) from the end of the
	// parameter list.
	paramSpec := FunctionTypeGetParamSpecFromArgsKwargs(t)
	if paramSpec != nil {
		// The TypeScript defaults stripPositionOnlySeparator to false.
		t = FunctionTypeCloneRemoveParamSpecArgsKwargs(t, false)
	}

	for index, param := range t.Shared.Parameters {
		paramType := FunctionTypeGetParamType(t, index)
		defaultType := FunctionTypeGetParamDefaultType(t, index)

		// Handle specialized TypeVarTuples specially.
		if index == len(t.Shared.Parameters)-1 && param.Category == parser.ParamCategoryArgsList &&
			IsTypeVarTuple(paramType) {
			specializedParamType := FunctionTypeGetParamType(t, index)
			tupleType, isTuple := AsClassInstance(specializedParamType)
			if isTuple && ClassTypeIsBuiltInNamed(tupleType, "tuple") && tupleType.Priv.TupleTypeArgs != nil {
				for _, tupleTypeArg := range tupleType.Priv.TupleTypeArgs {
					paramString := printTypeInternal(
						tupleTypeArg.Type,
						printTypeFlags,
						returnTypeCallback,
						uniqueNameMap,
						recursionTypes,
						recursionCount,
					)
					paramTypeStrings = append(paramTypeStrings, paramString)
				}
				continue
			}
		}

		// Handle expanding TypedDict kwargs specially.
		if IsTypedKwargs(param, paramType) && printTypeFlags&PrintTypeFlagsExpandTypedDictArgs != 0 &&
			paramType.Base().Category == TypeCategoryClass {
			paramClassType := paramType.(*ClassType)
			paramClassType.Shared.TypedDictEntries.KnownItems.ForEach(func(v *TypedDictEntry, k string) {
				valueTypeString := printTypeInternal(
					v.ValueType,
					printTypeFlags,
					returnTypeCallback,
					uniqueNameMap,
					recursionTypes,
					recursionCount,
				)
				paramTypeStrings = append(paramTypeStrings, k+": "+valueTypeString)
			})

			var extraItemsType Type
			if entries := paramClassType.Shared.TypedDictEntries; entries != nil && entries.ExtraItems != nil {
				extraItemsType = entries.ExtraItems.ValueType
			}
			if extraItemsType != nil && !IsNever(extraItemsType) {
				valueTypeString := printTypeInternal(
					extraItemsType,
					printTypeFlags,
					returnTypeCallback,
					uniqueNameMap,
					recursionTypes,
					recursionCount,
				)
				paramTypeStrings = append(paramTypeStrings, "**kwargs: "+valueTypeString)
			}

			continue
		}

		paramString := ""
		if param.Category == parser.ParamCategoryArgsList {
			if !isTruthyName(param.Name) || !FunctionParamIsNameSynthesized(param) {
				paramString += "*"
			}
		} else if param.Category == parser.ParamCategoryKwargsDict {
			paramString += "**"
		}

		emittedParamName := false
		if isTruthyName(param.Name) && !FunctionParamIsNameSynthesized(param) {
			paramString += *param.Name
			sawDefinedName = true
			emittedParamName = true
		} else if printTypeFlags&PrintTypeFlagsPythonSyntax != 0 {
			paramString += "__p" + strconv.Itoa(index)
			sawDefinedName = true
			emittedParamName = true
		}

		defaultValueAssignment := "="
		isParamSpecArgsKwargsParam := false

		if isTruthyName(param.Name) {
			// Avoid printing type types if parameter have unknown type.
			if FunctionParamIsTypeDeclared(param) || FunctionParamIsTypeInferred(param) {
				paramType := FunctionTypeGetParamType(t, index)
				paramTypeString := ""
				if len(*recursionTypes) < MaxTypeRecursionCount {
					paramTypeString = printTypeInternal(
						paramType,
						printTypeFlags,
						returnTypeCallback,
						uniqueNameMap,
						recursionTypes,
						recursionCount,
					)
				}

				if emittedParamName {
					paramString += ": "
				} else if param.Category == parser.ParamCategoryArgsList && !IsUnpacked(paramType) {
					paramString += "*"
				}

				if param.Category == parser.ParamCategoryKwargsDict && IsUnpacked(paramType) {
					if printTypeFlags&PrintTypeFlagsPythonSyntax != 0 {
						// Use "Unpack" because ** isn't legal syntax prior to
						// Python 3.12.
						paramTypeString = "Unpack[" + substringFrom(paramTypeString, 1) + "]"
					} else {
						// If this is an unpacked TypeDict for a **kwargs
						// parameter, add another star.
						paramTypeString = "*" + paramTypeString
					}
				}

				paramString += paramTypeString

				if IsParamSpec(paramType) {
					if param.Category == parser.ParamCategoryArgsList ||
						param.Category == parser.ParamCategoryKwargsDict {
						isParamSpecArgsKwargsParam = true
					}
				}

				// PEP8 indicates that the "=" for the default value should have
				// surrounding spaces when used with a type annotation.
				defaultValueAssignment = " = "
			} else if (printTypeFlags & PrintTypeFlagsOmitTypeArgsIfUnknown) == 0 {
				if !FunctionParamIsNameSynthesized(param) {
					paramString += ": "
				}
				if printTypeFlags&(PrintTypeFlagsPrintUnknownWithAny|PrintTypeFlagsPythonSyntax) != 0 {
					paramString += "Any"
				} else {
					paramString += "Unknown"
				}
				defaultValueAssignment = " = "
			}
		} else if param.Category == parser.ParamCategorySimple {
			if sawDefinedName {
				paramString += "/"
			} else {
				continue
			}
		}

		if defaultType != nil {
			if param.DefaultExpr != nil {
				paramString += defaultValueAssignment +
					PrintExpression(param.DefaultExpr, PrintExpressionFlagsNone)
			} else {
				// If the function doesn't originate from a function declaration
				// (e.g. it is synthesized), we can't get to the default
				// declaration, but we can still indicate that there is a
				// default value provided.
				paramString += defaultValueAssignment + "..."
			}
		}

		// If this is a (...) signature, replace the *args, **kwargs with "...".
		if FunctionTypeIsGradualCallableForm(t) && !isParamSpecArgsKwargsParam {
			if param.Category == parser.ParamCategoryArgsList {
				paramString = "..."
			} else if param.Category == parser.ParamCategoryKwargsDict {
				continue
			}
		}

		paramTypeStrings = append(paramTypeStrings, paramString)
	}

	if paramSpec != nil {
		if printTypeFlags&PrintTypeFlagsPythonSyntax != 0 {
			// The original interpolates the TypeVarType object itself into the
			// template string, so JavaScript calls Object.prototype.toString on
			// it and both lines read "[object Object]". Reproduced rather than
			// corrected to paramSpec.shared.name, because correcting it would
			// change printed output. See UPSTREAM-BUGS.md #10.
			paramTypeStrings = append(paramTypeStrings, "*args: [object Object].args")
			paramTypeStrings = append(paramTypeStrings, "**kwargs: [object Object].kwargs")
		} else {
			paramTypeStrings = append(paramTypeStrings, "**"+printTypeInternal(
				paramSpec,
				printTypeFlags,
				returnTypeCallback,
				uniqueNameMap,
				recursionTypes,
				recursionCount,
			))
		}
	}

	returnType := returnTypeCallback(t)
	returnTypeString := ""
	if len(*recursionTypes) < MaxTypeRecursionCount {
		returnTypeString = printTypeInternal(
			returnType,
			printTypeFlags|PrintTypeFlagsParenthesizeUnion|PrintTypeFlagsParenthesizeCallable,
			returnTypeCallback,
			uniqueNameMap,
			recursionTypes,
			recursionCount,
		)
	}

	return paramTypeStrings, returnTypeString
}

// someTypeNotUnknown corresponds to `types.some((t) => !isUnknown(t))`.
func someTypeNotUnknown(types []Type) bool {
	for _, t := range types {
		if !IsUnknown(t) {
			return true
		}
	}
	return false
}

// someTypeVarNotUnknown is the same predicate over a TypeVar slice.
func someTypeVarNotUnknown(typeParams []*TypeVarType) bool {
	for _, typeParam := range typeParams {
		if !IsUnknown(typeParam) {
			return true
		}
	}
	return false
}

// everyTupleArgBounded corresponds to
// `tupleTypeArgs.every((typeArg) => !typeArg.isUnbounded)`.
func everyTupleArgBounded(typeArgs []*TupleTypeArg) bool {
	for _, typeArg := range typeArgs {
		if typeArg.IsUnbounded {
			return false
		}
	}
	return true
}

// isTruthyName reports whether `param.name` is truthy in JavaScript. A nil
// pointer stands in for undefined, and the empty string is falsy too.
func isTruthyName(name *string) bool {
	return name != nil && *name != ""
}

// paramSpecAccessText renders ParamSpecAccess back to the string the original
// stores. The empty string stands in for undefined, which is falsy.
func paramSpecAccessText(access ParamSpecAccess) string {
	switch access {
	case ParamSpecAccessArgs:
		return "args"
	case ParamSpecAccessKwargs:
		return "kwargs"
	}
	return ""
}

// substringFrom is String.prototype.substring(start) measured in UTF-16 code
// units, which is what the original counts.
func substringFrom(s string, start int) string {
	units := utf16.Encode([]rune(s))
	if start >= len(units) {
		return ""
	}
	return string(utf16.Decode(units[start:]))
}
