/*
 * typebridge_dispatch.go
 *
 * Maps the TypeScript namespace functions the tests call onto the Go port.
 *
 * The TypeScript signatures have optional parameters with defaults; the Go port
 * makes them required (see PORTING.md). The defaults are supplied here, and
 * each one names the TypeScript default it stands for so the two can be
 * checked against each other.
 */

package main

import (
	"fmt"

	"github.com/microsoft/pyright/go/analyzer"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/parser"
)

func (b *typeBridge) dispatch(target string, args []any) (any, error) {
	switch target {
	case "AnyType.create":
		// The TypeScript defaults isEllipsis to false.
		return analyzer.AnyTypeCreate(argBool(args, 0, false)), nil

	case "UnknownType.create":
		// The TypeScript defaults isIncomplete to false.
		return analyzer.UnknownTypeCreate(argBool(args, 0, false)), nil

	case "UnboundType.create":
		return analyzer.UnboundTypeCreate(), nil

	case "NeverType.createNever":
		return analyzer.NeverTypeCreateNever(), nil

	case "NeverType.createNoReturn":
		return analyzer.NeverTypeCreateNoReturn(), nil

	case "ModuleType.create":
		// The TypeScript defaults symbolTable to undefined.
		return analyzer.ModuleTypeCreate(argString(args, 0, ""), argUri(args, 1), nil), nil

	case "TypeVarType.createInstance":
		// The TypeScript defaults kind to TypeVarKind.TypeVar.
		return analyzer.TypeVarTypeCreateInstance(
			argString(args, 0, ""),
			analyzer.TypeVarKind(argInt(args, 1, int(analyzer.TypeVarKindTypeVar))),
		), nil

	case "TypeVarType.createInstantiable":
		return analyzer.TypeVarTypeCreateInstantiable(
			argString(args, 0, ""),
			analyzer.TypeVarKind(argInt(args, 1, int(analyzer.TypeVarKindTypeVar))),
		), nil

	case "TypeVarType.cloneForUnpacked":
		// The TypeScript defaults isInUnion to false.
		return analyzer.TypeVarTypeCloneForUnpacked(argTypeVar(args, 0), argBool(args, 1, false)), nil

	case "ClassType.createInstantiable":
		return analyzer.ClassTypeCreateInstantiable(
			argString(args, 0, ""),
			argString(args, 1, ""),
			argString(args, 2, ""),
			argUri(args, 3),
			analyzer.ClassTypeFlags(argInt(args, 4, 0)),
			analyzer.TypeSourceId(argInt(args, 5, 0)),
			argType(args, 6),
			argType(args, 7),
			argStringPtr(args, 8),
		), nil

	case "ClassType.cloneAsInstance":
		// The TypeScript defaults includeSubclasses to true.
		return analyzer.ClassTypeCloneAsInstance(argClass(args, 0), argBool(args, 1, true)), nil

	case "ClassType.specialize":
		// The TypeScript leaves isTypeArgExplicit and isEmptyContainer
		// undefined and defaults includeSubclasses to false. tupleTypeArgs has
		// no decoder yet -- no test passes one -- so refuse rather than drop it.
		if at(args, 4) != nil {
			return nil, fmt.Errorf("types: ClassType.specialize tupleTypeArgs is not supported by the bridge")
		}
		return analyzer.ClassTypeSpecialize(
			argClass(args, 0),
			argTypeSlice(args, 1),
			argBoolPtr(args, 2),
			argBool(args, 3, false),
			nil,
			argBoolPtr(args, 5),
		), nil

	case "FunctionType.createInstance":
		return analyzer.FunctionTypeCreateInstance(
			argString(args, 0, ""),
			argString(args, 1, ""),
			argString(args, 2, ""),
			analyzer.FunctionTypeFlags(argInt(args, 3, 0)),
			argStringPtr(args, 4),
		), nil

	case "FunctionType.addParam":
		param, ok := at(args, 1).(analyzer.FunctionParam)
		if !ok {
			return nil, fmt.Errorf("types: FunctionType.addParam expects a FunctionParam")
		}
		analyzer.FunctionTypeAddParam(argFunction(args, 0), param)
		return nil, nil

	case "FunctionType.addPositionOnlyParamSeparator":
		analyzer.FunctionTypeAddPositionOnlyParamSeparator(argFunction(args, 0))
		return nil, nil

	case "FunctionType.addKeywordOnlyParamSeparator":
		analyzer.FunctionTypeAddKeywordOnlyParamSeparator(argFunction(args, 0))
		return nil, nil

	case "FunctionType.addParamSpecVariadics":
		analyzer.FunctionTypeAddParamSpecVariadics(argFunction(args, 0), argTypeVar(args, 1))
		return nil, nil

	case "FunctionParam.create":
		return analyzer.FunctionParamCreate(
			parser.ParamCategory(argInt(args, 0, 0)),
			argType(args, 1),
			analyzer.FunctionParamFlags(argInt(args, 2, 0)),
			argStringPtr(args, 3),
			argType(args, 4),
			nil,
		), nil

	case "combineTypes":
		// The TypeScript leaves options undefined.
		return analyzer.CombineTypes(argTypeSlice(args, 0), nil), nil
	}

	return nil, fmt.Errorf("types: unsupported target %q", target)
}

func at(args []any, index int) any {
	if index < 0 || index >= len(args) {
		return nil
	}
	return args[index]
}

func argBool(args []any, index int, def bool) bool {
	if v, ok := at(args, index).(bool); ok {
		return v
	}
	return def
}

func argBoolPtr(args []any, index int) *bool {
	if v, ok := at(args, index).(bool); ok {
		return &v
	}
	return nil
}

func argInt(args []any, index int, def int) int {
	if v, ok := at(args, index).(float64); ok {
		return int(v)
	}
	return def
}

func argString(args []any, index int, def string) string {
	if v, ok := at(args, index).(string); ok {
		return v
	}
	return def
}

func argStringPtr(args []any, index int) *string {
	if v, ok := at(args, index).(string); ok {
		return &v
	}
	return nil
}

func argUri(args []any, index int) uri.Uri {
	if v, ok := at(args, index).(uri.Uri); ok {
		return v
	}
	return uri.Empty()
}

func argType(args []any, index int) analyzer.Type {
	if v, ok := at(args, index).(analyzer.Type); ok {
		return v
	}
	return nil
}

func argClass(args []any, index int) *analyzer.ClassType {
	v, ok := at(args, index).(*analyzer.ClassType)
	if !ok {
		panic(fmt.Sprintf("types: argument %d is not a class", index))
	}
	return v
}

func argFunction(args []any, index int) *analyzer.FunctionType {
	v, ok := at(args, index).(*analyzer.FunctionType)
	if !ok {
		panic(fmt.Sprintf("types: argument %d is not a function", index))
	}
	return v
}

func argTypeVar(args []any, index int) *analyzer.TypeVarType {
	v, ok := at(args, index).(*analyzer.TypeVarType)
	if !ok {
		panic(fmt.Sprintf("types: argument %d is not a TypeVar", index))
	}
	return v
}

func argTypeSlice(args []any, index int) []analyzer.Type {
	raw, ok := at(args, index).([]any)
	if !ok {
		return nil
	}
	out := make([]analyzer.Type, 0, len(raw))
	for i, element := range raw {
		t, ok := element.(analyzer.Type)
		if !ok {
			panic(fmt.Sprintf("types: element %d of argument %d is not a type", i, index))
		}
		out = append(out, t)
	}
	return out
}
