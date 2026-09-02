/*
 * typeevaluator_typealiasform.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * validateSymbolIsTypeExpression, isLegalTypeAliasExpressionForm and
 * isLegalImplicitTypeAliasType, plus the evaluator's isSentinelLiteral wrapper.
 *
 * These four are the gatekeepers for implicit type aliases. Python has no syntax
 * that distinguishes `X = int | str` (a type alias) from `x = some_value` (an
 * assignment), so the evaluator decides by looking at the shape of the
 * right-hand side before evaluating it -- isLegalTypeAliasExpressionForm -- and
 * again at the resulting type afterwards -- isLegalImplicitTypeAliasType. A
 * speculative alias that fails either test reverts to being an ordinary
 * variable.
 *
 * The syntactic test is a long list of node kinds rather than a rule, which is
 * exactly how the original writes it, so it is transliterated as a switch with
 * the same membership rather than condensed.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateSymbolIsTypeExpression corresponds to the function of the same name.
// The original's comment: reports diagnostics if type isn't valid within a type
// expression.
func (e *typeEvaluator) validateSymbolIsTypeExpression(
	node parser.ExpressionNode,
	t Type,
	includesVarDecl bool,
) Type {
	if e.isSymbolValidTypeExpression(t, includesVarDecl) {
		return t
	}

	// The original's comment: disable for assignments in the typings.pyi file,
	// since it defines special forms.
	fileInfo := GetFileInfo(node)
	if fileInfo.IsTypingStubFile {
		return t
	}

	e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.TypeAnnotationVariable(), node, nil)
	return UnknownTypeCreate(false)
}

// isLegalTypeAliasExpressionForm corresponds to the function of the same name.
// The original's comment at its call site: perform a sanity check on the RHS
// expression. Some expression forms should never be considered legitimate for
// type aliases.
func (e *typeEvaluator) isLegalTypeAliasExpressionForm(node parser.ExpressionNode, allowStrLiteral bool) bool {
	switch node.GetNodeType() {
	case parser.ParseNodeTypeError,
		parser.ParseNodeTypeUnaryOperation,
		parser.ParseNodeTypeAssignmentExpression,
		parser.ParseNodeTypeTypeAnnotation,
		parser.ParseNodeTypeAwait,
		parser.ParseNodeTypeTernary,
		parser.ParseNodeTypeUnpack,
		parser.ParseNodeTypeTuple,
		parser.ParseNodeTypeCall,
		parser.ParseNodeTypeComprehension,
		parser.ParseNodeTypeSlice,
		parser.ParseNodeTypeYield,
		parser.ParseNodeTypeYieldFrom,
		parser.ParseNodeTypeLambda,
		parser.ParseNodeTypeNumber,
		parser.ParseNodeTypeDictionary,
		parser.ParseNodeTypeList,
		parser.ParseNodeTypeSet:
		return false

	case parser.ParseNodeTypeStringList, parser.ParseNodeTypeString:
		return allowStrLiteral

	case parser.ParseNodeTypeConstant:
		return node.(*parser.ConstantNode).D.ConstType == parser.KeywordTypeNone

	case parser.ParseNodeTypeBinaryOperation:
		binOp := node.(*parser.BinaryOperationNode)
		// Both recursive calls pass true regardless of the caller's
		// allowStrLiteral, so a string literal is legal on either side of a
		// union even when it would not be legal on its own.
		return binOp.D.Operator == parser.OperatorTypeBitwiseOr &&
			e.isLegalTypeAliasExpressionForm(binOp.D.LeftExpr, true) &&
			e.isLegalTypeAliasExpressionForm(binOp.D.RightExpr, true)

	case parser.ParseNodeTypeIndex:
		return e.isLegalTypeAliasExpressionForm(node.(*parser.IndexNode).D.LeftExpr, allowStrLiteral)

	case parser.ParseNodeTypeMemberAccess:
		return e.isLegalTypeAliasExpressionForm(node.(*parser.MemberAccessNode).D.LeftExpr, allowStrLiteral)
	}

	return true
}

// isLegalImplicitTypeAliasType corresponds to the function of the same name.
func (e *typeEvaluator) isLegalImplicitTypeAliasType(t Type) bool {
	// The original's comment: we explicitly exclude "..." and "Unknown".
	if IsEllipsisType(t) {
		return false
	}

	if IsUnknown(t) {
		// The original's comment: if this is a union type, we'll assume that it
		// was meant as a type alias even though all of the union subtypes are
		// Unknown.
		if props := t.Base().Props; props != nil && props.SpecialForm != nil &&
			ClassTypeIsBuiltInNamed(props.SpecialForm, "UnionType") {
			return true
		}
		return false
	}

	// The original's comment: look at the subtypes within the union. If any of
	// them are not instantiable (other than "None" which is special-cased), it is
	// not a legal type alias type.
	isLegal := true
	DoForEachSubtype(t, func(subtype Type, _ int, _ []Type) {
		if !subtype.Base().IsInstantiable() && !IsNoneInstance(subtype) {
			isLegal = false
		}
	})

	return isLegal
}

// isSentinelLiteral corresponds to the typeUtils function of the same name. It
// is an evaluator method here only because the call sites reach it through the
// evaluator; it consults no evaluator state.
func (e *typeEvaluator) isSentinelLiteral(t Type) bool {
	return IsSentinelLiteral(t)
}
