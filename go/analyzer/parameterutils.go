/*
 * parameterutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Utility functions for parameters.
 *
 * Transliterated from analyzer/parameterUtils.ts (pyright 1.1.412).
 *
 * The optional index fields of ParamListDetails (kwargsIndex, argsIndex,
 * firstKeywordOnlyIndex) are `number | undefined` in the original, and the code
 * distinguishes "not set" from index 0, so they are pointers here rather than
 * a -1 sentinel.
 */

package analyzer

import (
	"strconv"
	"strings"

	"github.com/microsoft/pyright/go/parser"
)

// IsTypedKwargs corresponds to isTypedKwargs.
func IsTypedKwargs(param FunctionParam, effectiveParamType Type) bool {
	cls, ok := AsClassInstance(effectiveParamType)
	return param.Category == parser.ParamCategoryKwargsDict &&
		ok &&
		IsUnpackedClass(effectiveParamType) &&
		ClassTypeIsTypedDictClass(cls) &&
		cls.Shared.TypedDictEntries != nil
}

// ParamKind corresponds to the enum of the same name.
type ParamKind int

const (
	ParamKindPositional ParamKind = iota
	ParamKindStandard
	ParamKindKeyword
	ParamKindExpandedArgs
)

// VirtualParamDetails corresponds to the interface of the same name.
type VirtualParamDetails struct {
	Param        FunctionParam
	Type         Type
	DeclaredType Type
	DefaultType  Type
	Index        int
	Kind         ParamKind
}

// ParamListDetails corresponds to the interface of the same name.
type ParamListDetails struct {
	// Params is a virtual parameter list that refers to original parameters.
	Params []*VirtualParamDetails

	// PositionOnlyParamCount and PositionParamCount are counts of virtual
	// parameters.
	PositionOnlyParamCount int
	PositionParamCount     int

	// KwargsIndex, ArgsIndex and FirstKeywordOnlyIndex are indexes into the
	// virtual parameter list. Nil means unset.
	KwargsIndex           *int
	ArgsIndex             *int
	FirstKeywordOnlyIndex *int

	FirstPositionOrKeywordIndex int

	HasUnpackedTypeVarTuple     bool
	HasUnpackedTypedDict        bool
	UnpackedKwargsTypedDictType *ClassType
	ParamSpec                   *TypeVarType
}

// ParamListDetailsOptions corresponds to the interface of the same name.
type ParamListDetailsOptions struct {
	// DisallowExtraKwargsForTd disallows extra keyword arguments when the
	// function uses a **kwargs annotated with a (non-closed) unpacked
	// TypedDict.
	//
	// The original notes: by default this is allowed, but PEP 692 suggests that
	// it should be disallowed for calls whereas it explicitly says it is
	// allowed for callable assignment rules.
	DisallowExtraKwargsForTd bool
}

// GetParamListDetails examines the input parameters within a function signature
// and creates a "virtual list" of parameters, stripping out any markers and
// expanding any *args with unpacked tuples. A nil options stands in for the
// omitted argument.
func GetParamListDetails(t *FunctionType, options *ParamListDetailsOptions) *ParamListDetails {
	result := &ParamListDetails{
		FirstPositionOrKeywordIndex: 0,
		PositionParamCount:          0,
		PositionOnlyParamCount:      0,
		Params:                      []*VirtualParamDetails{},
		HasUnpackedTypeVarTuple:     false,
		HasUnpackedTypedDict:        false,
	}

	positionOnlyIndex := findParamIndex(t.Shared.Parameters, IsPositionOnlySeparator)

	// Handle the old (pre Python 3.8) way of specifying positional-only
	// parameters by naming them with "__".
	if positionOnlyIndex < 0 {
		for i := range t.Shared.Parameters {
			p := t.Shared.Parameters[i]
			if p.Category != parser.ParamCategorySimple {
				break
			}

			if p.Name == nil || *p.Name == "" {
				break
			}

			if IsDunderName(*p.Name) || !strings.HasPrefix(*p.Name, "__") {
				// We exempt "self" and "cls" in class and instance methods.
				if i > 0 || FunctionTypeIsStaticMethod(t) {
					break
				}

				continue
			}

			positionOnlyIndex = i + 1
		}
	}

	for i := 0; i < positionOnlyIndex; i++ {
		if FunctionTypeGetParamDefaultType(t, i) != nil {
			break
		}

		result.PositionOnlyParamCount++
	}

	sawKeywordOnlySeparator := false

	// addVirtualParam corresponds to the closure of the same name. A nil
	// sourceOverride stands in for `undefined`.
	addVirtualParam := func(
		param FunctionParam,
		index int,
		typeOverride Type,
		defaultTypeOverride Type,
		sourceOverride *ParamKind,
	) {
		if param.Name == nil || *param.Name == "" {
			return
		}

		var kind ParamKind
		if sourceOverride != nil {
			kind = *sourceOverride
		} else if param.Category == parser.ParamCategoryArgsList {
			kind = ParamKindPositional
		} else if sawKeywordOnlySeparator {
			kind = ParamKindKeyword
		} else if positionOnlyIndex >= 0 && index < positionOnlyIndex {
			kind = ParamKindPositional
		} else {
			kind = ParamKindStandard
		}

		paramType := typeOverride
		if paramType == nil {
			paramType = FunctionTypeGetParamType(t, index)
		}
		defaultType := defaultTypeOverride
		if defaultType == nil {
			defaultType = FunctionTypeGetParamDefaultType(t, index)
		}

		result.Params = append(result.Params, &VirtualParamDetails{
			Param:        param,
			Index:        index,
			Type:         paramType,
			DeclaredType: FunctionTypeGetDeclaredParamType(t, index),
			DefaultType:  defaultType,
			Kind:         kind,
		})
	}

	expandedArgs := ParamKindExpandedArgs

	for index := range t.Shared.Parameters {
		param := t.Shared.Parameters[index]

		switch param.Category {
		case parser.ParamCategoryArgsList:
			// If this is an unpacked tuple, expand the entries.
			paramType := FunctionTypeGetParamType(t, index)
			paramTypeCls, paramTypeIsClass := AsClass(paramType)
			hasName := param.Name != nil && *param.Name != ""

			if hasName && IsUnpackedClass(paramType) && paramTypeIsClass && paramTypeCls.Priv.TupleTypeArgs != nil {
				addToPositionalOnly := index < result.PositionOnlyParamCount

				for tupleIndex, tupleArg := range paramTypeCls.Priv.TupleTypeArgs {
					category := parser.ParamCategorySimple
					if IsTypeVarTuple(tupleArg.Type) || tupleArg.IsUnbounded {
						category = parser.ParamCategoryArgsList
					}

					if category == parser.ParamCategoryArgsList {
						argsIndex := len(result.Params)
						result.ArgsIndex = &argsIndex
					}

					if IsTypeVarTuple(FunctionTypeGetParamType(t, index)) {
						result.HasUnpackedTypeVarTuple = true
					}

					name := *param.Name + "[" + strconv.Itoa(tupleIndex) + "]"
					addVirtualParam(
						FunctionParamCreate(
							category,
							tupleArg.Type,
							FunctionParamFlagsNameSynthesized|FunctionParamFlagsTypeDeclared,
							&name,
							nil,
							nil,
						),
						index,
						tupleArg.Type,
						nil,
						&expandedArgs,
					)

					if category == parser.ParamCategorySimple {
						result.PositionParamCount++
					}

					if tupleIndex > 0 && addToPositionalOnly {
						result.PositionOnlyParamCount++
					}
				}

				// The original notes: normally, a VarArgList parameter (either
				// named or as an unnamed separator) would signify the start of
				// keyword-only parameters. However, we can construct callable
				// signatures that defy this rule by using Callable and
				// TypeVarTuples or unpacked tuples.
				if !sawKeywordOnlySeparator && (positionOnlyIndex < 0 || index >= positionOnlyIndex) {
					firstKwOnly := len(result.Params)
					result.FirstKeywordOnlyIndex = &firstKwOnly
					sawKeywordOnlySeparator = true
				}
			} else {
				if hasName && result.ArgsIndex == nil {
					argsIndex := len(result.Params)
					result.ArgsIndex = &argsIndex

					if IsTypeVarTuple(paramType) {
						result.HasUnpackedTypeVarTuple = true
					}
				}

				if !sawKeywordOnlySeparator && (positionOnlyIndex < 0 || index >= positionOnlyIndex) {
					firstKwOnly := len(result.Params)
					if hasName {
						firstKwOnly++
					}
					result.FirstKeywordOnlyIndex = &firstKwOnly
					sawKeywordOnlySeparator = true
				}

				addVirtualParam(param, index, nil, nil, nil)
			}

		case parser.ParamCategoryKwargsDict:
			sawKeywordOnlySeparator = true

			paramType := FunctionTypeGetParamType(t, index)
			paramTypeCls, isInstance := AsClassInstance(paramType)

			// Is this an unpacked TypedDict? If so, expand the entries.
			if isInstance && IsUnpackedClass(paramType) && paramTypeCls.Shared.TypedDictEntries != nil {
				if result.FirstKeywordOnlyIndex == nil {
					firstKwOnly := len(result.Params)
					result.FirstKeywordOnlyIndex = &firstKwOnly
				}

				typedDictType := paramTypeCls
				paramTypeCls.Shared.TypedDictEntries.KnownItems.ForEach(func(entry *TypedDictEntry, name string) {
					if paramTypeCls.Priv.TypedDictNarrowedEntries() != nil {
						if narrowed, ok := paramTypeCls.Priv.TypedDictNarrowedEntries().Get(name); ok {
							entry = narrowed
						}
					}

					specializedParamType := PartiallySpecializeType(entry.ValueType, typedDictType, nil, nil)

					var defaultParamType Type
					if !entry.IsRequired {
						defaultParamType = specializedParamType
					}

					entryName := name
					addVirtualParam(
						FunctionParamCreate(
							parser.ParamCategorySimple,
							specializedParamType,
							FunctionParamFlagsTypeDeclared,
							&entryName,
							defaultParamType,
							nil,
						),
						index,
						specializedParamType,
						defaultParamType,
						nil,
					)
				})

				var extraItemsType Type
				if paramTypeCls.Shared.TypedDictEntries.ExtraItems != nil {
					extraItemsType = paramTypeCls.Shared.TypedDictEntries.ExtraItems.ValueType
				}

				var addKwargsForExtraItems bool
				if extraItemsType != nil {
					addKwargsForExtraItems = !IsNever(extraItemsType)
				} else {
					addKwargsForExtraItems = options == nil || !options.DisallowExtraKwargsForTd
				}

				// Unless the TypedDict is completely closed (i.e. is not
				// allowed to have any extra items), add a virtual **kwargs
				// parameter to represent any additional items.
				if addKwargsForExtraItems {
					kwargsParamType := extraItemsType
					if kwargsParamType == nil {
						kwargsParamType = AnyTypeCreate(false)
					}
					kwargsName := "kwargs"
					addVirtualParam(
						FunctionParamCreate(
							parser.ParamCategoryKwargsDict,
							kwargsParamType,
							FunctionParamFlagsTypeDeclared,
							&kwargsName,
							nil,
							nil,
						),
						index,
						extraItemsType,
						nil,
						nil,
					)

					kwargsIndex := len(result.Params) - 1
					result.KwargsIndex = &kwargsIndex
				}

				result.HasUnpackedTypedDict = true
				result.UnpackedKwargsTypedDictType = paramTypeCls
			} else if param.Name != nil && *param.Name != "" {
				if result.KwargsIndex == nil {
					kwargsIndex := len(result.Params)
					result.KwargsIndex = &kwargsIndex
				}

				if result.FirstKeywordOnlyIndex == nil {
					firstKwOnly := len(result.Params)
					result.FirstKeywordOnlyIndex = &firstKwOnly
				}

				addVirtualParam(param, index, nil, nil, nil)
			}

		case parser.ParamCategorySimple:
			if param.Name != nil && *param.Name != "" && !sawKeywordOnlySeparator {
				result.PositionParamCount++
			}

			var defaultOverride Type
			if t.Priv.SpecializedTypes != nil && t.Priv.SpecializedTypes.ParameterDefaultTypes != nil {
				if index < len(t.Priv.SpecializedTypes.ParameterDefaultTypes) {
					defaultOverride = t.Priv.SpecializedTypes.ParameterDefaultTypes[index]
				}
			}

			addVirtualParam(param, index, nil, defaultOverride, nil)
		}
	}

	// If the signature ends in `*args: P.args, **kwargs: P.kwargs`, extract the
	// ParamSpec P.
	result.ParamSpec = FunctionTypeGetParamSpecFromArgsKwargs(t)

	result.FirstPositionOrKeywordIndex = -1
	for i, p := range result.Params {
		if p.Kind != ParamKindPositional && p.Kind != ParamKindExpandedArgs {
			result.FirstPositionOrKeywordIndex = i
			break
		}
	}
	if result.FirstPositionOrKeywordIndex < 0 {
		result.FirstPositionOrKeywordIndex = len(result.Params)
	}

	return result
}

// IsParamSpecArgs returns true if the type of the argument type is
// "*args: P.args" or "*args: Any". Both of these match a parameter of type
// "*args: P.args".
func IsParamSpecArgs(paramSpec *TypeVarType, argType Type) bool {
	isCompatible := true

	DoForEachSubtype(argType, func(argSubtype Type, index int, allSubtypes []Type) {
		if ps, ok := AsParamSpec(argSubtype); ok &&
			ps.Priv.ParamSpecAccess == ParamSpecAccessArgs &&
			IsTypeSame(argSubtype, paramSpec, TypeSameOptions{IgnoreTypeFlags: true}, 0) {
			return
		}

		if cls, ok := AsClassInstance(argSubtype); ok &&
			cls.Priv.TupleTypeArgs != nil &&
			len(cls.Priv.TupleTypeArgs) == 1 &&
			cls.Priv.TupleTypeArgs[0].IsUnbounded &&
			IsAnyOrUnknown(cls.Priv.TupleTypeArgs[0].Type) {
			return
		}

		if IsAnyOrUnknown(argSubtype) {
			return
		}

		isCompatible = false
	})

	return isCompatible
}

// IsParamSpecKwargs returns true if the type of the argument type is
// "**kwargs: P.kwargs" or "**kwargs: Any". Both of these match a parameter of
// type "**kwargs: P.kwargs".
func IsParamSpecKwargs(paramSpec *TypeVarType, argType Type) bool {
	isCompatible := true

	DoForEachSubtype(argType, func(argSubtype Type, index int, allSubtypes []Type) {
		if ps, ok := AsParamSpec(argSubtype); ok &&
			ps.Priv.ParamSpecAccess == ParamSpecAccessKwargs &&
			IsTypeSame(argSubtype, paramSpec, TypeSameOptions{IgnoreTypeFlags: true}, 0) {
			return
		}

		if cls, ok := AsClassInstance(argSubtype); ok &&
			ClassTypeIsBuiltInNamed(cls, "dict") &&
			cls.Priv.TypeArgs != nil &&
			len(cls.Priv.TypeArgs) == 2 {
			if keyCls, ok := AsClassInstance(cls.Priv.TypeArgs[0]); ok &&
				ClassTypeIsBuiltInNamed(keyCls, "str") &&
				IsAnyOrUnknown(cls.Priv.TypeArgs[1]) {
				return
			}
		}

		if IsAnyOrUnknown(argSubtype) {
			return
		}

		isCompatible = false
	})

	return isCompatible
}

// ParamAssignmentInfo corresponds to the interface of the same name.
type ParamAssignmentInfo struct {
	ParamDetails *VirtualParamDetails
	KeywordName  *string
	ArgsNeeded   int
	ArgsReceived int
}

// ParamAssignmentTracker tracks which parameters in a signature have been
// assigned arguments.
type ParamAssignmentTracker struct {
	Params []*ParamAssignmentInfo
}

// NewParamAssignmentTracker corresponds to the constructor.
func NewParamAssignmentTracker(paramInfos []*VirtualParamDetails) *ParamAssignmentTracker {
	params := make([]*ParamAssignmentInfo, 0, len(paramInfos))
	for _, p := range paramInfos {
		argsNeeded := 1
		if p.DefaultType != nil || p.Param.Category != parser.ParamCategorySimple {
			argsNeeded = 0
		}
		params = append(params, &ParamAssignmentInfo{ParamDetails: p, ArgsNeeded: argsNeeded, ArgsReceived: 0})
	}
	return &ParamAssignmentTracker{Params: params}
}

// AddKeywordParam adds a virtual keyword parameter for a keyword argument that
// targets a **kwargs parameter. This allows us to detect duplicate keyword
// arguments.
func (t *ParamAssignmentTracker) AddKeywordParam(name string, info *VirtualParamDetails) {
	t.Params = append(t.Params, &ParamAssignmentInfo{
		ParamDetails: info,
		KeywordName:  &name,
		ArgsNeeded:   1,
		ArgsReceived: 1,
	})
}

// LookupName returns nil where the TypeScript returns undefined.
func (t *ParamAssignmentTracker) LookupName(name string) *ParamAssignmentInfo {
	for _, p := range t.Params {
		// Don't return positional parameters because their names are
		// irrelevant.
		kind := p.ParamDetails.Kind
		if kind == ParamKindPositional || kind == ParamKindExpandedArgs {
			continue
		}

		effectiveName := p.ParamDetails.Param.Name
		if p.KeywordName != nil {
			effectiveName = p.KeywordName
		}
		if effectiveName != nil && *effectiveName == name {
			return p
		}
	}
	return nil
}

// LookupDetails corresponds to lookupDetails; the original asserts the entry
// exists.
func (t *ParamAssignmentTracker) LookupDetails(paramInfo *VirtualParamDetails) *ParamAssignmentInfo {
	for _, p := range t.Params {
		if p.ParamDetails == paramInfo {
			return p
		}
	}
	fail("Assertion failed.")
	return nil
}

func (t *ParamAssignmentTracker) MarkArgReceived(paramInfo *VirtualParamDetails) {
	entry := t.LookupDetails(paramInfo)
	entry.ArgsReceived++
}

// GetUnassignedParams returns a list of params that have not received their
// required number of arguments.
func (t *ParamAssignmentTracker) GetUnassignedParams() []string {
	unassignedParams := []string{}
	for _, p := range t.Params {
		if p.ParamDetails.Param.Name == nil || *p.ParamDetails.Param.Name == "" {
			continue
		}

		if p.ArgsReceived >= p.ArgsNeeded {
			continue
		}

		unassignedParams = append(unassignedParams, *p.ParamDetails.Param.Name)
	}

	return unassignedParams
}
