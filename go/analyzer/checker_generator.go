/*
 * checker_generator.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateGeneratorReturnType, _reportDeprecatedClassProperty,
 * _isTypeValidForUnusedValueTest and visitIndex's tuple range check.
 *
 * _validateGeneratorReturnType asks one question: if this function yields, can
 * its declared return type actually hold a generator? It answers by building
 * `Generator[Any, Any, Any]` and testing assignability, which is the weakest
 * generator there is -- so a failure means the annotation is not
 * generator-shaped at all, rather than that the yield types disagree. Those are
 * checked separately, where each `yield` is evaluated.
 *
 * The tuple range check is worth reading for its last line. Everything before it
 * is cheap: a literal integer index, a tuple of statically known length, a
 * bounds test in both directions. Only once a diagnostic is about to be emitted
 * does it call isTypeSubsumedByOtherType, and the original says why -- that call
 * is expensive, and the overwhelmingly common case is that the index is in
 * range and the question never arises. The subsumption test exists because a
 * union like `tuple[int] | tuple[int, str]` reaches this per subtype, and
 * indexing [1] is legal for the union even though it is out of range for the
 * first member.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateGeneratorReturnType corresponds to _validateGeneratorReturnType.
func (c *Checker) validateGeneratorReturnType(node *parser.FunctionNode, functionType *FunctionType) {
	if !FunctionTypeIsGenerator(functionType) {
		return
	}

	declaredReturnType := functionType.Shared.DeclaredReturnType
	if declaredReturnType == nil || IsNever(declaredReturnType) {
		return
	}

	functionDecl := functionType.Shared.Declaration
	if functionDecl == nil || len(functionDecl.YieldStatements) == 0 {
		return
	}

	var generatorType Type
	if !node.D.IsAsync && IsClassInstance(declaredReturnType) &&
		ClassTypeIsBuiltInNamed(declaredReturnType.(*ClassType), "AwaitableGenerator") {
		// The original's comment: handle the old-style (pre-await) generator case
		// if the return type explicitly uses AwaitableGenerator.
		generatorType = c.evaluator.GetTypeCheckerInternalsType(node, "AwaitableGenerator")
		if generatorType == nil {
			generatorType = c.evaluator.GetTypingType(node, "AwaitableGenerator")
		}
	} else {
		name := "Generator"
		if node.D.IsAsync {
			name = "AsyncGenerator"
		}
		generatorType = c.evaluator.GetTypingType(node, name)
	}

	if generatorType == nil || !IsInstantiableClass(generatorType) {
		return
	}

	specializedGenerator := ClassTypeCloneAsInstance(
		ClassTypeSpecialize(generatorType.(*ClassType),
			[]Type{AnyTypeCreate(false), AnyTypeCreate(false), AnyTypeCreate(false)},
			nil, false, nil, nil), true)

	diagAddendum := common.NewDiagnosticAddendum()
	if c.evaluator.AssignType(declaredReturnType, specializedGenerator, diagAddendum,
		nil, AssignTypeFlagsDefault, 0) {
		return
	}

	message := localization.LocMessage.GeneratorSyncReturnType().
		Format(c.evaluator.PrintType(AnyTypeCreate(false), nil))
	if node.D.IsAsync {
		message = localization.LocMessage.GeneratorAsyncReturnType().
			Format(c.evaluator.PrintType(AnyTypeCreate(false), nil))
	}

	var errorNode parser.ParseNode = node.D.Name
	if node.D.ReturnAnnotation != nil {
		errorNode = node.D.ReturnAnnotation
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
		message+diagAddendum.GetString(), errorNode, nil)
}

// reportDeprecatedClassProperty corresponds to _reportDeprecatedClassProperty.
func (c *Checker) reportDeprecatedClassProperty(
	node *parser.FunctionNode, functionTypeResult *FunctionTypeResult,
) {
	if !IsClassInstance(functionTypeResult.DecoratedType) ||
		!ClassTypeIsClassProperty(functionTypeResult.DecoratedType.(*ClassType)) {
		return
	}

	c.reportDeprecatedDiagnostic(node.D.Name,
		localization.LocMessage.ClassPropertyDeprecated(), "")
}

// isTypeValidForUnusedValueTest corresponds to _isTypeValidForUnusedValueTest.
// The original's comment: determines whether the specified type is one that
// should trigger an "unused" value diagnostic.
func (c *Checker) isTypeValidForUnusedValueTest(t Type) bool {
	return !IsNoneInstance(t) && !IsNever(t) && !IsAnyOrUnknown(t)
}

// reportTupleIndexOutOfRange corresponds to the tuple-length half of visitIndex.
func (c *Checker) reportTupleIndexOutOfRange(node *parser.IndexNode) {
	baseType := c.evaluator.GetType(node.D.LeftExpr)
	if baseType == nil {
		return
	}

	DoForEachSubtype(baseType, func(subtype Type, _ int, _ []Type) {
		tupleType := GetSpecializedTupleType(subtype)

		if !IsClassInstance(subtype) || tupleType == nil ||
			tupleType.Priv.TupleTypeArgs == nil || IsUnboundedTupleClass(tupleType) {
			return
		}

		tupleLength := len(tupleType.Priv.TupleTypeArgs)

		if len(node.D.Items) != 1 || node.D.TrailingComma ||
			node.D.Items[0].D.ArgCategory != parser.ArgCategorySimple ||
			node.D.Items[0].D.Name != nil {
			return
		}

		subscriptType := c.evaluator.GetType(node.D.Items[0].D.ValueExpr)
		if subscriptType == nil || !IsClassInstance(subscriptType) ||
			!ClassTypeIsBuiltInNamed(subscriptType.(*ClassType), "int") ||
			!IsLiteralType(subscriptType.(*ClassType)) {
			return
		}

		// `typeof literalValue !== 'number'` -- the `number` arm of pyright's
		// `number | bigint` int literal is LiteralFloat here.
		literal, isNumber := subscriptType.(*ClassType).Priv.LiteralValue.(LiteralFloat)
		if !isNumber {
			return
		}
		index := int(literal)

		if (index < 0 || index < tupleLength) && (index >= 0 || index+tupleLength >= 0) {
			return
		}

		// The original's comment: this can be an expensive check, so we save it
		// for the end once we are about to emit a diagnostic.
		if c.evaluator.IsTypeSubsumedByOtherType(tupleType, baseType, false) {
			return
		}

		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TupleIndexOutOfRange().Format(
				c.evaluator.PrintType(subtype, nil), index),
			node, nil)
	})
}
