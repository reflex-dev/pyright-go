/*
 * typeevaluator_yield.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfYield, getTypeOfYieldFrom, getTypeOfUnpackOperator.
 *
 * The type of a `yield` expression is not what it yields -- it is what
 * `generator.send()` will push back in, which is the Generator's second type
 * argument. The yielded expression is still evaluated, with the declared yield
 * type as its expected type, but its result is discarded here; the return-type
 * inference machinery collects it separately.
 *
 * `yield from` is the mirror: its value is the *return* type of the delegated
 * generator, the third type argument. The original handles three shapes -- a
 * Generator, an old-style pre-await Coroutine (Unknown, because those carry no
 * return type in their annotation), and any plain iterable, which is first
 * resolved through the iteration protocol and then re-inspected for Generator
 * arguments.
 *
 * getTypeOfUnpackOperator lives here because it is the other expression form
 * whose meaning depends on the iteration protocol. Note the ordering of its
 * cases: an unpacked TypeVarTuple and an unpacked `tuple` are type-level forms
 * handled before the runtime iteration fallback, and unpacking inside an
 * annotation that permits neither is a diagnostic rather than an iteration.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getTypeOfYield corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfYield(node *parser.YieldNode) *TypeResult {
	var expectedYieldType Type
	var sentType Type
	isIncomplete := false

	enclosingFunction := GetEnclosingFunction(node)
	if enclosingFunction != nil {
		functionTypeInfo := e.GetTypeOfFunction(enclosingFunction)
		if functionTypeInfo != nil {
			returnType := FunctionTypeGetEffectiveReturnType(functionTypeInfo.FunctionType, true)
			if returnType != nil {
				liveScopeIds := GetTypeVarScopesForNode(node)
				returnType = MakeTypeVarsBound(returnType, liveScopeIds, true)

				expectedYieldType = GetGeneratorYieldType(returnType, enclosingFunction.D.IsAsync)

				generatorTypeArgs := GetGeneratorTypeArgs(returnType)
				if len(generatorTypeArgs) >= 2 {
					sentType = MakeTypeVarsBound(generatorTypeArgs[1], liveScopeIds, true)
				}
			}
		}
	}

	if node.D.Expr != nil {
		exprResult := e.getTypeOfExpression(
			node.D.Expr, EvalFlagsNone, MakeInferenceContext(expectedYieldType, false, nil))
		if exprResult.IsIncomplete {
			isIncomplete = true
		}
	}

	if sentType == nil {
		sentType = UnknownTypeCreate(false)
	}
	return &TypeResult{Type: sentType, IsIncomplete: isIncomplete}
}

// getTypeOfYieldFrom corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfYieldFrom(node *parser.YieldFromNode) *TypeResult {
	yieldFromTypeResult := e.getTypeOfExpression(node.D.Expr, EvalFlagsNone, nil)
	yieldFromType := yieldFromTypeResult.Type

	returnedType := MapSubtypes(yieldFromType, func(yieldFromSubtype Type) Type {
		// The original's comment: is the expression a Generator type?
		generatorTypeArgs := GetGeneratorTypeArgs(yieldFromSubtype)
		if generatorTypeArgs != nil {
			// The original tests `length >= 2` but indexes [2]; when the array has
			// exactly two entries this reads past the end and yields undefined, which
			// TypeScript then returns as the subtype. Go would panic, so the port
			// tightens the bound to the index actually read. This is upstream bug #15.
			if len(generatorTypeArgs) > 2 {
				return generatorTypeArgs[2]
			}
			return UnknownTypeCreate(false)
		}

		// The original's comment: handle old-style (pre-await) Coroutines as a special
		// case.
		if IsClassInstance(yieldFromSubtype) &&
			ClassTypeIsBuiltInNamed(yieldFromSubtype.(*ClassType), "Coroutine", "CoroutineType") {
			return UnknownTypeCreate(false)
		}

		// The original's comment: handle simple iterables.
		var iterableType Type = UnknownTypeCreate(false)
		if result := e.GetTypeOfIterable(yieldFromTypeResult, false, node, nil); result != nil {
			iterableType = result.Type
		}

		// The original's comment: does the iterable return a Generator?
		generatorTypeArgs = GetGeneratorTypeArgs(iterableType)
		if len(generatorTypeArgs) > 2 {
			return generatorTypeArgs[2]
		}
		return UnknownTypeCreate(false)
	}, nil)

	return &TypeResult{Type: returnedType}
}

// getTypeOfUnpackOperator corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfUnpackOperator(
	node *parser.UnpackNode, flags EvalFlags, inferenceContext *InferenceContext,
) *TypeResult {
	var typeResult *TypeResult
	var iterExpectedType Type

	if inferenceContext != nil {
		iterableType := e.GetBuiltInType(node, "Iterable")
		if iterableType != nil && IsInstantiableClass(iterableType) {
			iterExpectedType = ClassTypeCloneAsInstance(
				ClassTypeSpecialize(iterableType.(*ClassType),
					[]Type{inferenceContext.ExpectedType}, nil, false, nil, nil), true)
		}
	}

	iterTypeResult := e.getTypeOfExpression(
		node.D.Expr, flags, MakeInferenceContext(iterExpectedType, false, nil))
	iterType := iterTypeResult.Type

	switch {
	case (flags&EvalFlagsNoTypeVarTuple) == 0 && IsTypeVarTuple(iterType) &&
		!iterType.(*TypeVarType).Priv.IsUnpacked:
		typeResult = &TypeResult{Type: TypeVarTypeCloneForUnpacked(iterType.(*TypeVarType), false)}

	case (flags&EvalFlagsAllowUnpackedTuple) != 0 && IsInstantiableClass(iterType) &&
		ClassTypeIsBuiltInNamed(iterType.(*ClassType), "tuple"):
		typeResult = &TypeResult{Type: ClassTypeCloneForUnpacked(iterType.(*ClassType))}

	case (flags & EvalFlagsTypeExpression) != 0:
		starRange := node.D.StarToken.GetRange()
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.UnpackInAnnotation(), node, &starRange)
		typeResult = &TypeResult{Type: UnknownTypeCreate(false)}

	default:
		iteratorTypeResult := e.GetTypeOfIterator(iterTypeResult, false, node, nil)
		if iteratorTypeResult == nil {
			iteratorTypeResult = &TypeResult{
				Type:         UnknownTypeCreate(iterTypeResult.IsIncomplete),
				IsIncomplete: iterTypeResult.IsIncomplete,
			}
		}
		typeResult = &TypeResult{
			Type:         iteratorTypeResult.Type,
			TypeErrors:   iterTypeResult.TypeErrors,
			UnpackedType: iterType,
			IsIncomplete: iteratorTypeResult.IsIncomplete,
		}
	}

	return typeResult
}
