/*
 * typeevaluator_annotation.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfAnnotation and getTypeOfExpressionExpectingType.
 *
 * Every declared type in a Python program arrives through here. A parameter
 * annotation, a variable annotation, a return annotation, a base class in a
 * type expression -- all of them end at getTypeOfExpressionExpectingType, whose
 * entire job is turning an ExpectedTypeOptions struct into the EvalFlags bitset
 * that tells the expression dispatch it is reading a type rather than a value.
 *
 * The flag translation is nineteen conditions long and three of them invert
 * (allowFinal, allowClassVar and allowParamSpec set a No* flag when absent
 * rather than a yes flag when present). That is exactly the kind of thing a
 * transliteration gets wrong by tidying, so it is written out one condition per
 * line in the original's order.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// GetTypeOfAnnotation corresponds to getTypeOfAnnotation.
func (e *typeEvaluator) GetTypeOfAnnotation(node parser.ExpressionNode, options *ExpectedTypeOptions) Type {
	fileInfo := GetFileInfo(node)

	// The original's comment: special-case the typing.pyi file, which contains
	// some special types that the type analyzer needs to interpret differently.
	if fileInfo.IsTypingStubFile || fileInfo.IsTypingExtensionsStubFile {
		if specialType := e.handleTypingStubTypeAnnotation(node); specialType != nil {
			return specialType
		}
	}

	// `options ? { ...options } : {}` -- a copy, so the caller's options are not
	// mutated by the two assignments below.
	adjustedOptions := ExpectedTypeOptions{}
	if options != nil {
		adjustedOptions = *options
	}

	adjustedOptions.TypeExpression = true
	adjustedOptions.ConvertEllipsisToAny = true

	// The original's comment: if the annotation is part of a comment, allow
	// forward references even if it's not enclosed in quotes.
	switch parent := node.NodeBase().Parent.(type) {
	case *parser.AssignmentNode:
		if parser.ParseNode(parent.D.AnnotationComment) == parser.ParseNode(node) {
			adjustedOptions.ForwardRefs = true
			adjustedOptions.NotParsed = true
		}

	case *parser.FunctionAnnotationNode:
		matches := parser.ParseNode(parent.D.ReturnAnnotation) == parser.ParseNode(node)
		if !matches {
			for _, n := range parent.D.ParamAnnotations {
				if parser.ParseNode(n) == parser.ParseNode(node) {
					matches = true
					break
				}
			}
		}
		if matches {
			adjustedOptions.ForwardRefs = true
			adjustedOptions.NotParsed = true
		}

	case *parser.ParameterNode:
		if parser.ParseNode(parent.D.AnnotationComment) == parser.ParseNode(node) {
			adjustedOptions.ForwardRefs = true
			adjustedOptions.NotParsed = true
		}
	}

	annotationType := e.GetTypeOfExpressionExpectingType(node, &adjustedOptions).Type

	if IsModule(annotationType) {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, localization.LocMessage.ModuleAsType(), node, nil)
	}

	return ConvertToInstance(annotationType, false)
}

// GetTypeOfExpressionExpectingType corresponds to
// getTypeOfExpressionExpectingType.
func (e *typeEvaluator) GetTypeOfExpressionExpectingType(
	node parser.ExpressionNode,
	options *ExpectedTypeOptions,
) *TypeResult {
	if options == nil {
		options = &ExpectedTypeOptions{}
	}

	flags := EvalFlagsInstantiableType | EvalFlagsStrLiteralAsType

	if options.AllowTypeVarsWithoutScopeId {
		flags |= EvalFlagsAllowTypeVarWithoutScopeId
	}

	if options.TypeVarGetsCurScope {
		flags |= EvalFlagsTypeVarGetsCurScope
	}

	if options.EnforceClassTypeVarScope {
		flags |= EvalFlagsEnforceClassTypeVarScope
	}

	fileInfo := GetFileInfo(node)
	if (IsAnnotationEvaluationPostponed(fileInfo) || options.ForwardRefs) && !options.RuntimeTypeExpression {
		flags |= EvalFlagsForwardRefs
	} else if options.ParsesStringLiteral {
		flags |= EvalFlagsParsesStringLiteral
	}

	// The next three invert: the flag is set when the option is ABSENT.
	if !options.AllowFinal {
		flags |= EvalFlagsNoFinal
	}

	if options.AllowRequired {
		flags |= EvalFlagsAllowRequired | EvalFlagsTypeExpression
	}

	if options.AllowReadOnly {
		flags |= EvalFlagsAllowReadOnly | EvalFlagsTypeExpression
	}

	if options.AllowUnpackedTuple {
		flags |= EvalFlagsAllowUnpackedTuple
	} else {
		flags |= EvalFlagsNoTypeVarTuple
	}

	if options.AllowUnpackedTypedDict {
		flags |= EvalFlagsAllowUnpackedTypedDict
	}

	if !options.AllowParamSpec {
		flags |= EvalFlagsNoParamSpec
	}

	if options.TypeExpression {
		flags |= EvalFlagsTypeExpression
	}

	if options.ConvertEllipsisToAny {
		flags |= EvalFlagsConvertEllipsisToAny
	}

	if options.AllowEllipsis {
		flags |= EvalFlagsAllowEllipsis
	}

	if options.NoNonTypeSpecialForms {
		flags |= EvalFlagsNoNonTypeSpecialForms
	}

	if !options.AllowClassVar {
		flags |= EvalFlagsNoClassVar
	}

	if options.VarTypeAnnotation {
		flags |= EvalFlagsVarTypeAnnotation
	}

	if options.NotParsed {
		flags |= EvalFlagsNotParsed
	}

	if options.TypeFormArg {
		flags |= EvalFlagsTypeFormArg
	}

	return e.getTypeOfExpression(node, flags, nil)
}

// handleTypingStubTypeAnnotation corresponds to the function of the same name.
// It creates the special built-in class types that typing.pyi declares as bare
// annotations (Tuple, Generic, Protocol, Callable, ClassVar, Final, Literal and
// the rest), which is a separate unit of work.
func (e *typeEvaluator) handleTypingStubTypeAnnotation(_ parser.ExpressionNode) Type {
	e.unported("handleTypingStubTypeAnnotation")
	return nil
}
