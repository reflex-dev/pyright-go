/*
 * typeevaluator_iterator.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfAwaitable, getTypeOfIterator, getTypeOfIterable,
 * getTypeOfAwaitOperator, createAwaitableReturnType.
 *
 * The iteration protocol. Almost every statement that binds a name from
 * something other than a plain assignment goes through getTypeOfIterator: `for`
 * loops, comprehensions, tuple unpacking, `yield from`, `*args` splats. It was a
 * stub, so all of those produced Unknown regardless of how well the surrounding
 * expression evaluated.
 *
 * The `__iter__` / `__next__` dance has one subtlety worth naming: when
 * `__iter__` is missing entirely, the original falls back to the legacy
 * `__getitem__` protocol -- but only for the synchronous case and only for class
 * *instances*. That fallback is what makes old-style sequence classes iterate,
 * and it is the reason the missing-`__iter__` message is recorded into a
 * diagnostic addendum rather than reported at the point of discovery.
 *
 * Both functions signal failure by returning nil, which is the original's
 * `undefined`, and callers everywhere treat that as "not iterable" rather than
 * as "iterates Unknown". The distinction matters: it is what suppresses a
 * cascade of secondary errors after the first one is reported here.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getTypeOfAwaitable corresponds to the function of the same name.
//
// The original's comment: applies an "await" operation to the specified type and
// returns the result. According to PEP 492, await operates on an Awaitable
// (object that provides an __await__ that returns a generator object). If
// errorNode is undefined, no errors are reported.
func (e *typeEvaluator) getTypeOfAwaitable(
	typeResult *TypeResult, errorNode parser.ExpressionNode,
) *TypeResult {
	if e.prefetched == nil || e.prefetched.AwaitableClass == nil ||
		!IsInstantiableClass(e.prefetched.AwaitableClass) ||
		len(e.prefetched.AwaitableClass.(*ClassType).Shared.TypeParams) != 1 {
		return &TypeResult{Type: UnknownTypeCreate(false), IsIncomplete: typeResult.IsIncomplete}
	}

	awaitableProtocolObj := ClassTypeCloneAsInstance(e.prefetched.AwaitableClass.(*ClassType), true)
	isIncomplete := typeResult.IsIncomplete

	t := MapSubtypes(typeResult.Type, func(subtype Type) Type {
		subtype = e.MakeTopLevelTypeVarsConcrete(subtype, false)

		if IsAnyOrUnknown(subtype) {
			return subtype
		}

		var diag *common.DiagnosticAddendum
		if errorNode != nil {
			diag = common.NewDiagnosticAddendum()
		}

		if IsClassInstance(subtype) {
			constraints := NewConstraintTracker()

			if e.AssignType(awaitableProtocolObj, subtype, diag, constraints, AssignTypeFlagsDefault, 0) {
				specializedType := e.SolveAndApplyConstraints(awaitableProtocolObj, constraints, nil, nil)

				if IsClass(specializedType) && len(specializedType.(*ClassType).Priv.TypeArgs) > 0 {
					return specializedType.(*ClassType).Priv.TypeArgs[0]
				}

				return UnknownTypeCreate(false)
			}
		}

		if errorNode != nil && !typeResult.IsIncomplete {
			message := localization.LocMessage.TypeNotAwaitable().Format(e.PrintType(subtype, nil))
			if diag != nil {
				message += diag.GetString()
			}
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, message, errorNode, nil)
		}

		return UnknownTypeCreate(false)
	}, nil)

	return &TypeResult{Type: t, IsIncomplete: isIncomplete}
}

// GetTypeOfIterator corresponds to getTypeOfIterator.
//
// The original's comment: validates that the type is an iterator and returns the
// iterated type (i.e. the type returned from the '__next__' or '__anext__'
// method).
//
// emitNotIterableError defaults to true; nil selects the default.
func (e *typeEvaluator) GetTypeOfIterator(
	typeResult *TypeResult,
	isAsync bool,
	errorNode parser.ExpressionNode,
	emitNotIterableError *bool,
) *TypeResult {
	emitError := true
	if emitNotIterableError != nil {
		emitError = *emitNotIterableError
	}

	iterMethodName := "__iter__"
	nextMethodName := "__next__"
	if isAsync {
		iterMethodName = "__aiter__"
		nextMethodName = "__anext__"
	}

	isValidIterator := true
	isIncomplete := typeResult.IsIncomplete

	t := TransformPossibleRecursiveTypeAlias(typeResult.Type, 0)
	t = e.MakeTopLevelTypeVarsConcrete(t, false)
	t = RemoveUnbound(t)

	if IsOptionalType(t) && emitError {
		if !typeResult.IsIncomplete {
			e.AddDiagnostic(DiagnosticRuleReportOptionalIterable,
				localization.LocMessage.NoneNotIterable(), errorNode, nil)
		}
		t = RemoveNoneFromUnion(t)
	}

	iterableType := MapSubtypes(t, func(subtype Type) Type {
		subtype = e.MakeTopLevelTypeVarsConcrete(subtype, false)

		if IsAnyOrUnknown(subtype) {
			return subtype
		}

		diag := common.NewDiagnosticAddendum()
		if IsClass(subtype) {
			subtypeClass := subtype.(*ClassType)

			// The original's comment: handle an empty tuple specially.
			if subtype.Base().IsInstance() && IsTupleClass(subtypeClass) &&
				subtypeClass.Priv.TupleTypeArgs != nil && len(subtypeClass.Priv.TupleTypeArgs) == 0 {
				return NeverTypeCreateNever()
			}

			var iterReturnType Type
			if result := e.getTypeOfMagicMethodCall(
				subtype, iterMethodName, []*TypeResult{}, errorNode, nil, nil); result != nil {
				iterReturnType = result.Type
			}

			if iterReturnType == nil {
				// The original's comment: there was no __iter__. See if we can fall back to
				// the __getitem__ method instead.
				if !isAsync && IsClassInstance(subtype) {
					var indexArgType Type = UnknownTypeCreate(false)
					if e.prefetched != nil && e.prefetched.IntClass != nil &&
						IsInstantiableClass(e.prefetched.IntClass) {
						indexArgType = ClassTypeCloneAsInstance(e.prefetched.IntClass.(*ClassType), true)
					}

					if result := e.getTypeOfMagicMethodCall(
						subtype, "__getitem__", []*TypeResult{{Type: indexArgType}},
						errorNode, nil, nil); result != nil && result.Type != nil {
						return result.Type
					}
				}

				diag.AddMessage(localization.LocMessage.MethodNotDefined().Format(iterMethodName))
			} else {
				iterReturnTypeDiag := common.NewDiagnosticAddendum()

				returnType := e.MapSubtypesExpandTypeVars(iterReturnType, nil, func(subtype Type, _ Type) Type {
					if IsAnyOrUnknown(subtype) {
						return subtype
					}

					var nextReturnType Type
					if result := e.getTypeOfMagicMethodCall(
						subtype, nextMethodName, []*TypeResult{}, errorNode, nil, nil); result != nil {
						nextReturnType = result.Type
					}

					if nextReturnType == nil {
						iterReturnTypeDiag.AddMessage(
							localization.LocMessage.MethodNotDefinedOnType().Format(
								nextMethodName, e.PrintType(subtype, nil)))
						return nil
					}

					// The original's comment: convert any unpacked TypeVarTuples into object
					// instances. We don't know anything more about them.
					nextReturnType = MapSubtypes(nextReturnType, func(returnSubtype Type) Type {
						if IsTypeVar(returnSubtype) && IsUnpackedTypeVarTuple(returnSubtype) {
							return e.GetObjectType()
						}

						return returnSubtype
					}, nil)

					if !isAsync {
						return nextReturnType
					}

					// The original's comment: if it's an async iteration, there's an implicit
					// 'await' operator applied.
					awaitableResult := e.getTypeOfAwaitable(
						&TypeResult{Type: nextReturnType, IsIncomplete: typeResult.IsIncomplete}, errorNode)
					if awaitableResult.IsIncomplete {
						isIncomplete = true
					}
					return awaitableResult.Type
				})

				if iterReturnTypeDiag.IsEmpty() {
					return returnType
				}

				diag.AddAddendum(iterReturnTypeDiag)
			}
		}

		if !isIncomplete && emitError {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeNotIterable().Format(e.PrintType(subtype, nil))+diag.GetString(),
				errorNode, nil)
		}

		isValidIterator = false
		return nil
	}, nil)

	if !isValidIterator {
		return nil
	}
	return &TypeResult{Type: iterableType, IsIncomplete: isIncomplete}
}

// GetTypeOfIterable corresponds to getTypeOfIterable.
//
// The original's comment: validates that the type is an iterable and returns the
// iterable type argument.
func (e *typeEvaluator) GetTypeOfIterable(
	typeResult *TypeResult,
	isAsync bool,
	errorNode parser.ExpressionNode,
	emitNotIterableError *bool,
) *TypeResult {
	emitError := true
	if emitNotIterableError != nil {
		emitError = *emitNotIterableError
	}

	iterMethodName := "__iter__"
	if isAsync {
		iterMethodName = "__aiter__"
	}
	isValidIterable := true

	t := e.MakeTopLevelTypeVarsConcrete(typeResult.Type, false)

	if IsOptionalType(t) {
		if !typeResult.IsIncomplete && emitError {
			e.AddDiagnostic(DiagnosticRuleReportOptionalIterable,
				localization.LocMessage.NoneNotIterable(), errorNode, nil)
		}
		t = RemoveNoneFromUnion(t)
	}

	iterableType := MapSubtypes(t, func(subtype Type) Type {
		if IsAnyOrUnknown(subtype) {
			return subtype
		}

		if IsClass(subtype) {
			if result := e.getTypeOfMagicMethodCall(
				subtype, iterMethodName, []*TypeResult{}, errorNode, nil, nil); result != nil &&
				result.Type != nil {
				return e.MakeTopLevelTypeVarsConcrete(result.Type, false)
			}
		}

		if emitError {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeNotIterable().Format(e.PrintType(subtype, nil)),
				errorNode, nil)
		}

		isValidIterable = false
		return nil
	}, nil)

	if !isValidIterable {
		return nil
	}
	return &TypeResult{Type: iterableType, IsIncomplete: typeResult.IsIncomplete}
}

// getTypeOfAwaitOperator corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfAwaitOperator(
	node *parser.AwaitNode, flags EvalFlags, inferenceContext *InferenceContext,
) *TypeResult {
	if flags&EvalFlagsTypeExpression != 0 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.AwaitNotAllowed(), node, nil)
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	var expectedType Type
	if inferenceContext != nil {
		expectedType = e.createAwaitableReturnType(node, inferenceContext.ExpectedType, false, false)
	}

	exprTypeResult := e.getTypeOfExpression(node.D.Expr, flags, MakeInferenceContext(expectedType, false, nil))
	awaitableResult := e.getTypeOfAwaitable(exprTypeResult, node.D.Expr)
	typeResult := &TypeResult{
		Type:         awaitableResult.Type,
		IsIncomplete: exprTypeResult.IsIncomplete || awaitableResult.IsIncomplete,
		TypeErrors:   exprTypeResult.TypeErrors,
	}

	// The original re-checks and re-sets isIncomplete here even though the
	// expression above already covers it. Kept as written.
	if exprTypeResult.IsIncomplete {
		typeResult.IsIncomplete = true
	}
	return typeResult
}

// createAwaitableReturnType corresponds to the function of the same name.
//
// useCoroutine defaults to true in the original; every Go caller passes it
// explicitly.
func (e *typeEvaluator) createAwaitableReturnType(
	node parser.ParseNode, returnType Type, isGenerator bool, useCoroutine bool,
) Type {
	var awaitableReturnType Type

	if IsClassInstance(returnType) {
		returnClass := returnType.(*ClassType)
		if ClassTypeIsBuiltIn(returnClass) {
			switch {
			case returnClass.Shared.Name == "Generator":
				// The original's comment: if the return type is a Generator, change it to an
				// AsyncGenerator.
				asyncGeneratorType := e.getTypingType(node, "AsyncGenerator")
				if asyncGeneratorType != nil && IsInstantiableClass(asyncGeneratorType) {
					typeArgs := []Type{}
					generatorTypeArgs := returnClass.Priv.TypeArgs
					if len(generatorTypeArgs) > 0 {
						typeArgs = append(typeArgs, generatorTypeArgs[0])
					}
					if len(generatorTypeArgs) > 1 {
						typeArgs = append(typeArgs, generatorTypeArgs[1])
					}
					awaitableReturnType = ClassTypeCloneAsInstance(
						ClassTypeSpecialize(
							asyncGeneratorType.(*ClassType), typeArgs, nil, false, nil, nil), true)
				}

			case returnClass.Shared.Name == "AsyncIterator" || returnClass.Shared.Name == "AsyncIterable":
				// The original's comment: if it's already an AsyncIterator or AsyncIterable,
				// leave it as is.
				awaitableReturnType = returnType

			case returnClass.Shared.Name == "AsyncGenerator":
				// The original's comment: if it's already an AsyncGenerator and the function
				// is a generator, leave it as is.
				if isGenerator {
					awaitableReturnType = returnType
				}
			}
		}
	}

	if awaitableReturnType == nil || !isGenerator {
		// The original's comment: wrap in either an Awaitable or a CoroutineType, which
		// is a subclass of Awaitable.
		var awaitableType Type
		if useCoroutine {
			awaitableType = e.getTypesType(node, "CoroutineType")
		} else {
			awaitableType = e.getTypingType(node, "Awaitable")
		}

		if awaitableType != nil && IsInstantiableClass(awaitableType) {
			var typeArgs []Type
			if useCoroutine {
				typeArgs = []Type{AnyTypeCreate(false), AnyTypeCreate(false), returnType}
			} else {
				typeArgs = []Type{returnType}
			}
			awaitableReturnType = ClassTypeCloneAsInstance(
				ClassTypeSpecialize(awaitableType.(*ClassType), typeArgs, nil, false, nil, nil), true)
		} else {
			awaitableReturnType = UnknownTypeCreate(false)
		}
	}

	return awaitableReturnType
}
