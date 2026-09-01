/*
 * typeutils_paramspec.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * getContainerDepth and the ParamSpec conversions from analyzer/typeUtils.ts
 * (pyright 1.1.412). See the header of typeutils.go for the file split.
 *
 * Like typeutils_requires.go, these are ported ahead of turn because
 * TypeVarTransformer needs all three.
 */

package analyzer

// GetContainerDepth corresponds to getContainerDepth. The TypeScript defaults
// recursionCount to 0.
func GetContainerDepth(t Type, recursionCount int) int {
	if recursionCount > MaxTypeRecursionCount {
		return 1
	}

	recursionCount++

	cls, ok := AsClassInstance(t)
	if !ok {
		return 0
	}

	maxChildDepth := 0

	if cls.Priv.TupleTypeArgs != nil {
		for _, typeArgInfo := range cls.Priv.TupleTypeArgs {
			DoForEachSubtype(typeArgInfo.Type, func(subtype Type, index int, allSubtypes []Type) {
				childDepth := GetContainerDepth(subtype, recursionCount)
				if childDepth > maxChildDepth {
					maxChildDepth = childDepth
				}
			})
		}
	} else if cls.Priv.TypeArgs != nil {
		for _, typeArg := range cls.Priv.TypeArgs {
			DoForEachSubtype(typeArg, func(subtype Type, index int, allSubtypes []Type) {
				childDepth := GetContainerDepth(subtype, recursionCount)
				if childDepth > maxChildDepth {
					maxChildDepth = childDepth
				}
			})
		}
	} else {
		return 0
	}

	return 1 + maxChildDepth
}

// ConvertTypeToParamSpecValue corresponds to convertTypeToParamSpecValue.
func ConvertTypeToParamSpecValue(t Type) *FunctionType {
	if paramSpec, ok := AsParamSpec(t); ok {
		newFunction := FunctionTypeCreateInstance("", "", "", FunctionTypeFlagsParamSpecValue, nil)
		FunctionTypeAddParamSpecVariadics(newFunction, paramSpec)
		newFunction.Shared.TypeVarScopeID = GetTypeVarScopeID(t)
		return newFunction
	}

	if fn, ok := AsFunction(t); ok {
		// If it's already a ParamSpecValue, return it as is.
		if FunctionTypeIsParamSpecValue(fn) {
			return fn
		}

		newFunction := FunctionTypeCreateInstance(
			"", "", "",
			fn.Shared.Flags|FunctionTypeFlagsParamSpecValue,
			fn.Shared.DocString,
		)

		newFunction.Shared.DeprecatedMessage = fn.Shared.DeprecatedMessage

		for index, param := range fn.Shared.Parameters {
			FunctionTypeAddParam(newFunction, FunctionParamCreate(
				param.Category,
				FunctionTypeGetParamType(fn, index),
				param.Flags,
				param.Name,
				FunctionTypeGetParamDefaultType(fn, index),
				param.DefaultExpr,
			))
		}

		newFunction.Shared.TypeVarScopeID = fn.Shared.TypeVarScopeID
		newFunction.Priv.ConstructorTypeVarScopeID = fn.Priv.ConstructorTypeVarScopeID

		return newFunction
	}

	return ParamSpecTypeGetUnknown()
}

// SimplifyFunctionToParamSpec converts a FunctionType into a ParamSpec if it
// consists only of (*args: P.args, **kwargs: P.kwargs). Otherwise it returns
// the original type.
//
// The result is a *FunctionType or a *ParamSpecType, so it is typed as Type.
func SimplifyFunctionToParamSpec(t *FunctionType) Type {
	paramSpec := FunctionTypeGetParamSpecFromArgsKwargs(t)
	withoutParamSpec := FunctionTypeCloneRemoveParamSpecArgsKwargs(t, false)

	hasParams := len(withoutParamSpec.Shared.Parameters) > 0

	if len(withoutParamSpec.Shared.Parameters) == 1 {
		// If the ParamSpec has a position-only separator as its only parameter,
		// treat it as though there are no parameters.
		onlyParam := withoutParamSpec.Shared.Parameters[0]
		if IsPositionOnlySeparator(onlyParam) {
			hasParams = false
		}
	}

	// Can we simplify it to just a paramSpec?
	if !hasParams && paramSpec != nil {
		return paramSpec
	}

	return t
}
