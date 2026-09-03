/*
 * functiontransform.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/functionTransform.ts (pyright 1.1.412):
 * applyFunctionTransform, applyTotalOrderingTransform, getStructUnpackKind.
 *
 * The handful of functions whose effect on the program cannot be expressed in
 * the type system, and so has to be special-cased after the call is validated.
 *
 * `functools.total_ordering` is the one that matters here. It is a decorator
 * that fills in whichever of __lt__, __le__, __gt__ and __ge__ the class did not
 * define, deriving them from the one it did. Nothing about that is visible in
 * the decorator's declared type, so the methods are SYNTHESIZED into the class's
 * symbol table here.
 *
 * The synthesized methods take their second parameter's type from whichever
 * ordering method the class actually defined, so a class whose __lt__ accepts
 * only its own type gets a __gt__ that does too, rather than one accepting
 * `object`. Falling back to `object` happens only when the existing method has
 * no declared type for that parameter.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// ApplyFunctionTransform corresponds to applyFunctionTransform.
func ApplyFunctionTransform(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	argList []*Arg,
	functionType *FunctionType,
	result *CallResult,
) *CallResult {
	if IsFunction(functionType) {
		if functionType.Shared.FullName == "functools.total_ordering" {
			return applyTotalOrderingTransform(evaluator, errorNode, argList, result)
		}

		if kind := structUnpackKind(functionType.Shared.FullName); kind != "" {
			return applyStructUnpackTransformWithKind(evaluator, errorNode, argList, result,
				StructUnpackKind(kind))
		}
	}

	// The original's comment: by default, return the result unmodified.
	return result
}

// totalOrderingMethods is the set functools.total_ordering completes.
var totalOrderingMethods = []string{"__lt__", "__le__", "__gt__", "__ge__"}

// applyTotalOrderingTransform corresponds to the function of the same name.
func applyTotalOrderingTransform(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	argList []*Arg,
	result *CallResult,
) *CallResult {
	if len(argList) != 1 || argList[0].TypeResult == nil {
		return result
	}

	// The original's comment: this function is meant to apply to a concrete
	// instantiable class.
	classType, ok := argList[0].TypeResult.Type.(*ClassType)
	if !ok || !IsInstantiableClass(argList[0].TypeResult.Type) || classType.Priv.IncludeSubclasses {
		return result
	}

	instanceType := ClassTypeCloneAsInstance(classType, true)

	// The original's comment: verify that the class has at least one of the
	// required functions.
	var firstMemberFound *ClassMember
	missingMethods := []string{}
	for _, methodName := range totalOrderingMethods {
		memberInfo := LookUpObjectMember(instanceType, methodName,
			MemberAccessFlagsSkipInstanceMembers, nil)
		if memberInfo != nil {
			if firstMemberFound == nil {
				firstMemberFound = memberInfo
			}
			continue
		}
		missingMethods = append(missingMethods, methodName)
	}

	if firstMemberFound == nil {
		evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TotalOrderingMissingMethod(), errorNode, nil)
		return result
	}

	// The original's comment: determine what type to use for the parameter
	// corresponding to the second operand. This will be taken from the existing
	// method.
	var operandType Type

	firstMemberType := evaluator.GetTypeOfMember(firstMemberFound)
	if fn, ok := firstMemberType.(*FunctionType); ok && IsFunction(firstMemberType) &&
		len(fn.Shared.Parameters) >= 2 && FunctionParamIsTypeDeclared(fn.Shared.Parameters[1]) {
		operandType = FunctionTypeGetParamType(fn, 1)
	}

	// The original's comment: if there was no provided operand type, fall back to
	// object.
	if operandType == nil {
		objectType := evaluator.GetBuiltInObject(errorNode, "object", nil)
		if objectType == nil || !IsClassInstance(objectType) {
			return result
		}
		operandType = objectType
	}

	boolType := evaluator.GetBuiltInObject(errorNode, "bool", nil)
	if boolType == nil || !IsClassInstance(boolType) {
		return result
	}

	selfName := "self"
	selfParam := FunctionParamCreate(parser.ParamCategorySimple,
		SynthesizeTypeVarForSelfCls(classType, false),
		FunctionParamFlagsTypeDeclared, &selfName, nil, nil)

	objName := "__value"
	objParam := FunctionParamCreate(parser.ParamCategorySimple, operandType,
		FunctionParamFlagsTypeDeclared, &objName, nil, nil)

	// The original's comment: add the missing members to the class's symbol table.
	for _, methodName := range missingMethods {
		methodToAdd := FunctionTypeCreateSynthesizedInstance(methodName, FunctionTypeFlagsNone)
		FunctionTypeAddParam(methodToAdd, selfParam)
		FunctionTypeAddParam(methodToAdd, objParam)
		methodToAdd.Shared.DeclaredReturnType = boolType

		ClassTypeGetSymbolTable(classType).Set(methodName,
			SymbolCreateWithType(SymbolFlagsClassMember, methodToAdd, nil))
	}

	return result
}

// structUnpackKind corresponds to getStructUnpackKind. It returns "" where the
// original returns undefined.
//
// The original's comment: distinguishes between the `struct` functions whose
// return type can be synthesized from a literal format string. `unpack` and
// `unpack_from` return a tuple; `iter_unpack` returns an iterator of tuples.
//
// Only the module-level `_struct.*` free functions are handled. The reused-format
// API (`struct.Struct(fmt).unpack()` and friends) is out of scope: it would
// require threading the constructor's literal format through to the method calls,
// so those still infer the declared `tuple[Any, ...]`.
func structUnpackKind(fullName string) string {
	switch fullName {
	case "_struct.unpack", "_struct.unpack_from":
		return "tuple"
	case "_struct.iter_unpack":
		return "iterator"
	}
	return ""
}

// applyStructUnpackTransformWithKind delegates to the functionTransform.ts
// function of the same name. The Go port carries a CallResult at this seam where
// the original carries a FunctionResult; the two hold the same fields.
func applyStructUnpackTransformWithKind(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	argList []*Arg,
	result *CallResult,
	kind StructUnpackKind,
) *CallResult {
	transformed := ApplyStructUnpackTransform(evaluator, errorNode, argList, &FunctionResult{
		ReturnType:       result.ReturnType,
		ArgumentErrors:   result.ArgumentErrors,
		IsTypeIncomplete: result.IsTypeIncomplete,
	}, kind)

	if transformed == nil {
		return result
	}

	updated := *result
	updated.ReturnType = transformed.ReturnType
	return &updated
}
