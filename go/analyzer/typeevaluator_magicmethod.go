/*
 * typeevaluator_magicmethod.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getBoundMagicMethod, getTypeOfMagicMethodCall.
 *
 * Dunder methods, which almost every operator and several statements bottom out
 * in: `a + b` is `a.__add__(b)`, `x in y` is `y.__contains__(x)`, `for` is
 * `__iter__` and `__next__`.
 *
 * getBoundMagicMethod looks the method up on the CLASS, never the instance --
 * Python's operator protocols ignore instance attributes, so `a.__add__ = f`
 * does not change what `a + b` does, and SkipInstanceMembers says so. Three
 * things other than a function can come back and each is handled: a callable
 * OBJECT is followed through its own `__call__`, an Any/Unknown becomes a
 * callable that accepts anything, and anything else means the method is absent.
 *
 * getTypeOfMagicMethodCall then simulates the call, per subtype. Two
 * substitutions make the union arms work: `None` is handled as `object`, since
 * that is where its dunders actually live, and `type[None]` as `type`.
 *
 * The call runs SPECULATIVELY, and if an expected type was supplied and the
 * call failed, it runs a second time without one. That is what lets
 * `x: float = a + b` succeed through an overload set where passing the expected
 * type would have picked a worse overload. A single unsupported subtype fails
 * the whole call -- returning undefined rather than a partial union -- because
 * the caller needs to fall back to a different operator protocol entirely.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// GetBoundMagicMethod corresponds to getBoundMagicMethod. It returns nil where
// the original returns undefined.
func (e *typeEvaluator) GetBoundMagicMethod(
	classType *ClassType,
	memberName string,
	selfType Type,
	errorNode parser.ExpressionNode,
	diag *common.DiagnosticAddendum,
	recursionCount int,
) Type {
	boundMethodResult := e.getTypeOfBoundMember(errorNode, classType, memberName, nil, diag,
		MemberAccessFlagsSkipInstanceMembers|MemberAccessFlagsSkipAttributeAccessOverride,
		selfType, recursionCount)

	if boundMethodResult == nil || boundMethodResult.TypeErrors {
		return nil
	}

	if IsFunctionOrOverloaded(boundMethodResult.Type) {
		return boundMethodResult.Type
	}

	if IsClassInstance(boundMethodResult.Type) {
		// A callable object: the magic method is whatever its own __call__ is.
		if recursionCount > MaxTypeRecursionCount {
			return nil
		}
		recursionCount++

		return e.GetBoundMagicMethod(boundMethodResult.Type.(*ClassType), "__call__",
			nil, errorNode, diag, recursionCount)
	}

	if IsAnyOrUnknown(boundMethodResult.Type) {
		return GetUnknownTypeForCallable()
	}

	return nil
}

// GetTypeOfMagicMethodCall corresponds to getTypeOfMagicMethodCall. It returns
// nil where the original returns undefined, which the callers read as "this
// protocol does not apply" rather than as an error.
func (e *typeEvaluator) GetTypeOfMagicMethodCall(
	objType Type,
	methodName string,
	argList []*TypeResult,
	errorNode parser.ExpressionNode,
	inferenceContext *InferenceContext,
) *TypeResult {
	return e.getTypeOfMagicMethodCall(objType, methodName, argList, errorNode, inferenceContext, nil)
}

func (e *typeEvaluator) getTypeOfMagicMethodCall(
	objType Type,
	methodName string,
	argList []*TypeResult,
	errorNode parser.ExpressionNode,
	inferenceContext *InferenceContext,
	diag *common.DiagnosticAddendum,
) *TypeResult {
	magicMethodSupported := true
	isIncomplete := false
	var deprecationInfo *MagicMethodDeprecationInfo
	overloadsUsedForCall := []*FunctionType{}

	// The original's comment: create a helper lambda for object subtypes.
	handleSubtype := func(subtype Type) Type {
		var magicMethodType Type
		concreteSubtype := e.MakeTopLevelTypeVarsConcrete(subtype, false)

		if concreteClass, ok := concreteSubtype.(*ClassType); ok && IsClass(concreteSubtype) {
			magicMethodType = e.GetBoundMagicMethod(concreteClass, methodName, subtype, errorNode, diag, 0)
		}

		if magicMethodType == nil {
			magicMethodSupported = false
			return nil
		}

		functionArgs := make([]*Arg, len(argList))
		for i, arg := range argList {
			functionArgs[i] = &Arg{ArgCategory: parser.ArgCategorySimple, TypeResult: arg}
		}

		var callResult *CallResult
		e.UseSpeculativeMode(errorNode, func() {
			callResult = e.ValidateCallArgs(errorNode, functionArgs, &TypeResult{Type: magicMethodType},
				nil, true, inferenceContext)
		}, nil)

		// The original's comment: if there were errors with the expected type, try
		// to evaluate without the expected type.
		if callResult != nil && callResult.ArgumentErrors && inferenceContext != nil {
			e.UseSpeculativeMode(errorNode, func() {
				callResult = e.ValidateCallArgs(errorNode, functionArgs, &TypeResult{Type: magicMethodType},
					nil, true, nil)
			}, nil)
		}

		if callResult == nil {
			magicMethodSupported = false
			return nil
		}

		if callResult.ArgumentErrors {
			magicMethodSupported = false
		} else {
			for _, overload := range callResult.OverloadsUsedForCall {
				overloadsUsedForCall = append(overloadsUsedForCall, overload)

				// The original's comment: if one of the overloads is deprecated, note
				// the message.
				if overload.Shared.DeprecatedMessage != nil && IsClass(concreteSubtype) {
					deprecationInfo = &MagicMethodDeprecationInfo{
						DeprecatedMessage: *overload.Shared.DeprecatedMessage,
						ClassName:         concreteSubtype.(*ClassType).Shared.Name,
						MethodName:        methodName,
					}
				}
			}
		}

		if callResult.IsTypeIncomplete {
			isIncomplete = true
		}

		return callResult.ReturnType
	}

	returnType := MapSubtypes(objType, func(subtype Type) Type {
		if IsAnyOrUnknown(subtype) {
			return subtype
		}

		if IsClassInstance(subtype) || IsInstantiableClass(subtype) || IsTypeVar(subtype) {
			return handleSubtype(subtype)
		}

		if IsNoneInstance(subtype) {
			// The original's comment: use 'object' for 'None'.
			if e.prefetched != nil && e.prefetched.ObjectClass != nil &&
				IsInstantiableClass(e.prefetched.ObjectClass) {
				return handleSubtype(ClassTypeCloneAsInstance(e.prefetched.ObjectClass.(*ClassType), true))
			}
		}

		if IsNoneTypeClass(subtype) {
			// The original's comment: use 'type' for 'type[None]'.
			if e.prefetched != nil && e.prefetched.TypeClass != nil &&
				IsInstantiableClass(e.prefetched.TypeClass) {
				return handleSubtype(ClassTypeCloneAsInstance(e.prefetched.TypeClass.(*ClassType), true))
			}
		}

		magicMethodSupported = false
		return nil
	}, nil)

	if !magicMethodSupported {
		return nil
	}

	return &TypeResult{
		Type:                       returnType,
		IsIncomplete:               isIncomplete,
		MagicMethodDeprecationInfo: deprecationInfo,
		OverloadsUsedForCall:       overloadsUsedForCall,
	}
}
