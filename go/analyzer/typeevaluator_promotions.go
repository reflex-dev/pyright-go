/*
 * typeevaluator_promotions.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * expandPromotionTypes; and from analyzer/operations.ts:
 * getTypeOfTernaryOperation.
 *
 * expandPromotionTypes turns an implicit promotion back into the union it
 * stands for. `float` in an annotation implicitly accepts `int`, and the type
 * model records that as a flag on the class rather than as a union; narrowing
 * and exhaustiveness checks need the union spelled out, so this rewrites
 * `float` into `float | int` and `complex` into `complex | float | int`. The
 * literalValue guard is what keeps `Literal[1.0]` from being expanded -- a
 * literal float is not promotable, only the bare class is -- and excludeBytes
 * exists because the bytes promotion is separately configurable.
 *
 * getTypeOfTernaryOperation is here because it is the other place that combines
 * types from two arms. Its shape is the static-condition elision: a test
 * expression that evaluates statically to True or False removes the opposite
 * arm entirely, which is how `sys.version_info >= (3, 12)` guards avoid
 * reporting errors in the branch that will never run on this Python version.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// ExpandPromotionTypes corresponds to expandPromotionTypes. The original's
// comment: if the type includes promotion types, expand these to their
// constituent types. Its excludeBytes parameter defaults to false, and the
// interface exposes no such parameter.
func (e *typeEvaluator) ExpandPromotionTypes(node parser.ParseNode, t Type) Type {
	return e.expandPromotionTypes(node, t, false)
}

// expandPromotionTypes is the original with its excludeBytes parameter.
func (e *typeEvaluator) expandPromotionTypes(
	node parser.ParseNode, t Type, excludeBytes bool,
) Type {
	return MapSubtypes(t, func(subtype Type) Type {
		if !IsClass(subtype) {
			return subtype
		}

		cls := subtype.(*ClassType)
		if cls.Priv.IncludePromotions == nil || !*cls.Priv.IncludePromotions ||
			cls.Priv.LiteralValue != nil {
			return subtype
		}

		if excludeBytes && ClassTypeIsBuiltInNamed(cls, "bytes") {
			return subtype
		}

		typesToCombine := []Type{ClassTypeCloneRemoveTypePromotions(cls)}

		for _, promotionTypeName := range typePromotions[cls.Shared.FullName] {
			nameSplit := strings.Split(promotionTypeName, ".")
			promotionSubtype := e.GetBuiltInType(node, nameSplit[len(nameSplit)-1])

			if promotionSubtype != nil && IsInstantiableClass(promotionSubtype) {
				promoted := Type(ClassTypeCloneRemoveTypePromotions(promotionSubtype.(*ClassType)))
				promoted = ConvertToInstance(promoted, true)
				promoted = AddConditionToType(promoted, propsCondition(subtype), nil)
				typesToCombine = append(typesToCombine, promoted)
			}
		}

		return CombineTypes(typesToCombine, nil)
	}, nil)
}

// GetTypeOfTernaryOperation corresponds to the operations.ts function of the
// same name.
func GetTypeOfTernaryOperation(
	evaluator TypeEvaluator,
	node *parser.TernaryNode,
	flags EvalFlags,
	inferenceContext *InferenceContext,
) *TypeResult {
	fileInfo := GetFileInfo(node)

	if (flags & EvalFlagsTypeExpression) != 0 {
		evaluator.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.TernaryNotAllowed(), node, nil)
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	evaluator.GetTypeOfExpression(node.D.TestExpr, EvalFlagsNone, nil)

	typesToCombine := []Type{}
	isIncomplete := false
	typeErrors := false

	// The original passes only three arguments; the two alias lists are optional
	// there and absent here. `known` is false where the original answers
	// undefined, meaning the test is not statically decidable -- in which case
	// `!== false` and `!== true` both hold and both arms are evaluated.
	constExprValue, known := EvaluateStaticBoolExpression(
		node.D.TestExpr,
		fileInfo.ExecutionEnvironment,
		fileInfo.DefinedConstants,
		nil,
		nil,
	)

	if (!known || constExprValue) && evaluator.IsNodeReachable(node.D.IfExpr, nil) {
		ifType := evaluator.GetTypeOfExpression(node.D.IfExpr, flags, inferenceContext)
		typesToCombine = append(typesToCombine, ifType.Type)
		if ifType.IsIncomplete {
			isIncomplete = true
		}
		if ifType.TypeErrors {
			typeErrors = true
		}
	}

	if (!known || !constExprValue) && evaluator.IsNodeReachable(node.D.ElseExpr, nil) {
		elseType := evaluator.GetTypeOfExpression(node.D.ElseExpr, flags, inferenceContext)
		typesToCombine = append(typesToCombine, elseType.Type)
		if elseType.IsIncomplete {
			isIncomplete = true
		}
		if elseType.TypeErrors {
			typeErrors = true
		}
	}

	return &TypeResult{
		Type:         CombineTypes(typesToCombine, nil),
		IsIncomplete: isIncomplete,
		TypeErrors:   typeErrors,
	}
}

// getTypeOfTernaryOperation reaches the operations.ts function of the same name.
func (e *typeEvaluator) getTypeOfTernaryOperation(
	node *parser.TernaryNode, flags EvalFlags, inferenceContext *InferenceContext,
) *TypeResult {
	return GetTypeOfTernaryOperation(e, node, flags, inferenceContext)
}
