/*
 * typeevaluator_bindmethod.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * bindFunctionToClassOrObject, partiallySpecializeBoundMethod, suppressDiagnostics.
 *
 * Binding a method to the class or object it was reached through. `a.method`
 * where `a: C` does not have the type that `C.method` has: the `self` parameter
 * is consumed by the access, and whatever `self` was annotated with gets solved
 * against the accessing type, specializing the rest of the signature with it.
 *
 * Which of the three binding kinds applies is read off the function's flags, and
 * each supplies a different first argument:
 *
 *   - An instance method binds to the OBJECT. Reached through a class rather
 *     than an instance, the first parameter is kept rather than stripped --
 *     `C.method` is still a function of self. The exception is a member reached
 *     through a metaclass, where the class IS the instance and the parameter
 *     goes.
 *   - A class method binds to the CLASS, and always strips.
 *   - A static method binds to nothing and strips nothing, but is still
 *     specialized against the class so its type arguments are filled in.
 *
 * A function whose first parameter was already stripped is returned untouched;
 * binding twice would eat a real parameter.
 *
 * partiallySpecializeBoundMethod does the solving. Two shapes are special-cased
 * to avoid infinite recursion, both arising when a protocol refers to itself:
 * a `self` bound to a protocol class is assumed to match rather than checked,
 * and a callback protocol being bound to its own __call__ is refused outright.
 * A mismatch is only an ERROR when the parameter has a real declared, non-
 * synthesized name -- an unannotated `self` that fails to match is the normal
 * case, not a diagnostic.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// BindFunctionToClassOrObject corresponds to bindFunctionToClassOrObject.
// It returns nil where the original returns undefined.
func (e *typeEvaluator) BindFunctionToClassOrObject(
	baseType *ClassType,
	memberType Type,
	memberClass *ClassType,
	treatConstructorAsClassMethod bool,
	selfType Type,
	diag *common.DiagnosticAddendum,
	recursionCount int,
) Type {
	return MapSignatures(memberType, func(functionType *FunctionType) *FunctionType {
		// The original's comment: if the caller specified no base type, always
		// strip the first parameter. This is used in cases like constructors.
		if baseType == nil {
			return FunctionTypeClone(functionType, true, nil)
		}

		// The original's comment: if the first parameter was already stripped, it
		// has already been bound. Don't attempt to rebind.
		if functionType.Priv.StrippedFirstParamType != nil {
			return functionType
		}

		if FunctionTypeIsInstanceMethod(functionType) {
			// The original's comment: if the baseType is a metaclass, don't
			// specialize the function.
			if IsInstantiableMetaclass(baseType) {
				return functionType
			}

			var baseObj *ClassType
			if IsClassInstance(baseType) {
				baseObj = baseType
			} else {
				baseObj = ClassTypeCloneAsInstance(SpecializeWithDefaultTypeArgs(baseType), false)
			}

			stripFirstParam := false
			if IsClassInstance(baseType) {
				stripFirstParam = true
			} else if memberClass != nil && IsInstantiableMetaclass(memberClass) {
				stripFirstParam = true
			}

			firstParamType := selfType
			if firstParamType == nil {
				firstParamType = baseObj
			}

			return e.partiallySpecializeBoundMethod(
				baseType, functionType, diag, recursionCount, firstParamType, stripFirstParam)
		}

		if FunctionTypeIsClassMethod(functionType) ||
			(treatConstructorAsClassMethod && FunctionTypeIsConstructorMethod(functionType)) {
			baseClass := baseType
			if !IsInstantiableClass(baseType) {
				baseClass = ClassTypeCloneAsInstantiable(baseType, false)
			}

			var firstParamType Type = baseClass
			if selfType != nil {
				firstParamType = ConvertToInstantiable(selfType, true)
			}

			return e.partiallySpecializeBoundMethod(
				baseClass, functionType, diag, recursionCount, firstParamType, true)
		}

		if FunctionTypeIsStaticMethod(functionType) {
			baseClass := baseType
			if !IsInstantiableClass(baseType) {
				baseClass = ClassTypeCloneAsInstantiable(baseType, false)
			}

			return e.partiallySpecializeBoundMethod(
				baseClass, functionType, diag, recursionCount, nil, false)
		}

		return functionType
	})
}

// partiallySpecializeBoundMethod corresponds to the function of the same name.
//
// The original's comment: specializes the specified function for the specified
// class, optionally stripping the first first parameter (the "self" or "cls")
// off of the specialized function in the process. The baseType is the type used
// to reference the member.
func (e *typeEvaluator) partiallySpecializeBoundMethod(
	baseType *ClassType,
	memberType *FunctionType,
	diag *common.DiagnosticAddendum,
	recursionCount int,
	firstParamType Type,
	stripFirstParam bool,
) *FunctionType {
	constraints := NewConstraintTracker()

	if firstParamType != nil {
		if len(memberType.Shared.Parameters) > 0 {
			memberTypeFirstParam := memberType.Shared.Parameters[0]
			memberTypeFirstParamType := FunctionTypeGetParamType(memberType, 0)

			firstParamTypeVar, isTypeVar := memberTypeFirstParamType.(*TypeVarType)
			boundIsProtocol := false
			if isTypeVar && firstParamTypeVar.Shared.BoundType != nil &&
				IsClassInstance(firstParamTypeVar.Shared.BoundType) &&
				ClassTypeIsProtocolClass(firstParamTypeVar.Shared.BoundType.(*ClassType)) {
				boundIsProtocol = true
			}

			if boundIsProtocol {
				// The original's comment: handle the protocol class specially. Some
				// protocol classes contain references to themselves or their
				// subclasses, so if we attempt to call assignType, we'll risk
				// infinite recursion. Instead, we'll assume it's assignable.
				lowerBound := firstParamType
				if memberTypeFirstParamType.Base().IsInstantiable() {
					lowerBound = ConvertToInstance(firstParamType, true)
				}
				constraints.SetBounds(firstParamTypeVar, lowerBound, nil, false)
			} else {
				subDiag := createAddendumOrNil(diag)

				// The original's comment: protect against the case where a callback
				// protocol is being bound to its own __call__ method but the first
				// parameter is annotated with its own callable type. This can lead
				// to infinite recursion.
				if IsFunctionOrOverloaded(memberTypeFirstParamType) {
					if IsClassInstance(firstParamType) &&
						ClassTypeIsProtocolClass(firstParamType.(*ClassType)) {
						if subDiag != nil {
							subDiag.AddMessage(localization.LocMessage.BindTypeMismatch().Format(
								e.PrintType(firstParamType, nil),
								functionNameOrAnonymous(memberType),
								paramNameOrPositional(memberTypeFirstParam, "__p0"),
							))
						}
						return nil
					}
				}

				if !e.AssignType(memberTypeFirstParamType, firstParamType,
					createAddendumOrNil(subDiag), constraints,
					AssignTypeFlagsAllowUnspecifiedTypeArgs, recursionCount) {
					// An unannotated or synthesized `self` that fails to match is
					// the ordinary case rather than a diagnostic.
					if memberTypeFirstParam.Name != nil &&
						!FunctionParamIsNameSynthesized(memberTypeFirstParam) &&
						FunctionParamIsTypeDeclared(memberTypeFirstParam) {
						if subDiag != nil {
							subDiag.AddMessage(localization.LocMessage.BindTypeMismatch().Format(
								e.PrintType(firstParamType, nil),
								functionNameOrAnonymous(memberType),
								*memberTypeFirstParam.Name,
							))
						}
						return nil
					}
				}
			}
		} else {
			subDiag := createAddendumOrNil(diag)
			if subDiag != nil {
				subDiag.AddMessage(localization.LocMessage.BindParamMissing().Format(
					functionNameOrAnonymous(memberType)))
			}
			return nil
		}
	}

	// The original's comment: get the effective return type, which will have the
	// side effect of lazily evaluating (and caching) the inferred return type if
	// there is no defined return type.
	e.GetEffectiveReturnType(memberType)

	specializedFunction := e.SolveAndApplyConstraints(memberType, constraints, nil, nil)

	if fn, ok := specializedFunction.(*FunctionType); ok {
		return FunctionTypeClone(fn, stripFirstParam, baseType)
	}

	if overloaded, ok := specializedFunction.(*OverloadedType); ok {
		// The original's comment: for overloaded functions, use the first
		// overload. This isn't strictly correct, but this is an extreme edge case.
		overloads := OverloadedTypeGetOverloads(overloaded)
		if len(overloads) > 0 {
			return FunctionTypeClone(overloads[0], stripFirstParam, baseType)
		}
	}

	return nil
}

// functionNameOrAnonymous is the original's `memberType.shared.name ||
// '<anonymous>'`.
func functionNameOrAnonymous(fn *FunctionType) string {
	if fn.Shared.Name == "" {
		return "<anonymous>"
	}
	return fn.Shared.Name
}

// paramNameOrPositional is the original's `param.name || '__p0'`.
func paramNameOrPositional(param FunctionParam, fallback string) string {
	if param.Name == nil || *param.Name == "" {
		return fallback
	}
	return *param.Name
}

// SuppressDiagnostics corresponds to suppressDiagnostics without a diagnostic
// callback.
func (e *typeEvaluator) SuppressDiagnostics(node parser.ParseNode, callback func()) {
	e.suppressDiagnostics(node, callback, nil)
}

// suppressDiagnostics corresponds to the function of the same name. The stack
// entry records diagnostics only when a callback asked for them; HasSuppressed
// carries the original's distinction between an empty list and undefined.
//
// Go has no exceptions to unwind from, so the original's catch-and-rethrow
// collapses into a deferred pop.
func (e *typeEvaluator) suppressDiagnostics(
	node parser.ParseNode, callback func(), diagCallback func(suppressedDiags []string),
) {
	entry := &SuppressedNodeStackEntry{Node: node, HasSuppressed: diagCallback != nil}
	e.suppressedNodeStack = append(e.suppressedNodeStack, entry)

	popped := false
	pop := func() {
		if popped {
			return
		}
		popped = true
		e.suppressedNodeStack = e.suppressedNodeStack[:len(e.suppressedNodeStack)-1]
	}
	defer pop()

	callback()

	pop()
	if diagCallback != nil && entry.HasSuppressed {
		diagCallback(entry.SuppressedDiags)
	}
}
