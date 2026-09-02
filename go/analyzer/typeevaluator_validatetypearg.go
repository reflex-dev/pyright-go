/*
 * typeevaluator_validatetypearg.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * validateTypeArg, transformTypeArgsForParamSpec and getTypeOfArg.
 *
 * validateTypeArg is the per-argument legality check every specialization path
 * runs. It is a sequence of "is this form allowed here" tests -- type lists,
 * ellipsis, modules, ParamSpecs, TypeVarTuples, the empty-tuple shorthand,
 * unpacked tuples -- each gated by an option, and it is called from
 * createSpecializedClassType, createSpecializedTypeAlias and
 * adjustTypeArgsForTypeVarTuple with a different set each time.
 *
 * transformTypeArgsForParamSpec implements the PEP 612 shorthand: a class whose
 * single type parameter is a ParamSpec may be written `Foo[int, str]` rather
 * than `Foo[[int, str]]`, so the arguments are packaged into a type list.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// ValidateTypeArg corresponds to validateTypeArg.
func (e *typeEvaluator) ValidateTypeArg(argResult *TypeResultWithNode, options *ValidateTypeArgsOptions) bool {
	if options == nil {
		options = &ValidateTypeArgsOptions{}
	}

	if argResult.TypeListPresent {
		if !options.AllowTypeArgList {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.TypeArgListNotAllowed(),
				argResult.Node,
				nil,
			)
			return false
		}

		// The recursive call passes no options, so a nested list is rejected.
		for _, typeArg := range argResult.TypeList {
			e.ValidateTypeArg(typeArg, nil)
		}
	}

	if IsEllipsisType(argResult.Type) {
		// Note the original gates this on allowTypeArgList, not on a dedicated
		// "allow ellipsis" option -- an ellipsis is legal exactly where a
		// ParamSpec argument list is.
		if !options.AllowTypeArgList {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.EllipsisContext(),
				argResult.Node,
				nil,
			)
			return false
		}
	}

	if IsModule(argResult.Type) {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.ModuleAsType(), argResult.Node, nil)
		return false
	}

	if IsParamSpec(argResult.Type) {
		if !options.AllowParamSpec {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.ParamSpecContext(),
				argResult.Node,
				nil,
			)
			return false
		}
	}

	if IsTypeVarTuple(argResult.Type) && !argResult.Type.(*TypeVarType).Priv.IsInUnion {
		if !options.AllowTypeVarTuple {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.TypeVarTupleContext(),
				argResult.Node,
				nil,
			)
			return false
		}

		// Note this does NOT propagate the result: an unpacked-TypeVarTuple
		// failure reports a diagnostic but validateTypeArg still returns true.
		e.validateTypeVarTupleIsUnpacked(argResult.Type.(*TypeVarType), argResult.Node)
	}

	if !options.AllowEmptyTuple && argResult.IsEmptyTupleShorthand {
		e.AddDiagnostic(
			DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.ZeroLengthTupleNotAllowed(),
			argResult.Node,
			nil,
		)
		return false
	}

	if IsUnpackedClass(argResult.Type) {
		if !options.AllowUnpackedTuples {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.UnpackedArgInTypeArgument(),
				argResult.Node,
				nil,
			)
			return false
		}
	}

	return true
}

// transformTypeArgsForParamSpec corresponds to the function of the same name.
// The original's comment: PEP 612 says that if the class has only one type
// parameter consisting of a ParamSpec, the list of arguments does not need to be
// enclosed in a list.
//
// The second result is the original's `undefined` return, which means the
// arguments were rejected.
func (e *typeEvaluator) transformTypeArgsForParamSpec(
	typeParams []*TypeVarType,
	typeArgs []*TypeResultWithNode,
	typeArgsPresent bool,
	errorNode parser.ExpressionNode,
) ([]*TypeResultWithNode, bool) {
	if len(typeParams) != 1 || !IsParamSpec(typeParams[0]) || !typeArgsPresent {
		return typeArgs, typeArgsPresent
	}

	if len(typeArgs) > 1 {
		for _, typeArg := range typeArgs {
			if IsParamSpec(typeArg.Type) {
				e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.ParamSpecContext(), typeArg.Node, nil)
				return nil, false
			}

			if IsEllipsisType(typeArg.Type) {
				e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.EllipsisContext(), typeArg.Node, nil)
				return nil, false
			}

			if IsInstantiableClass(typeArg.Type) && ClassTypeIsBuiltInNamed(typeArg.Type.(*ClassType), "Concatenate") {
				e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.ConcatenateContext(), typeArg.Node, nil)
				return nil, false
			}

			if typeArg.TypeListPresent {
				e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.TypeArgListNotAllowed(), typeArg.Node, nil)
				return nil, false
			}
		}
	}

	if len(typeArgs) == 1 {
		// The original's comment: don't transform a type list.
		if typeArgs[0].TypeListPresent {
			return typeArgs, true
		}

		typeArgType := typeArgs[0].Type

		// The original's comment: don't transform a single ParamSpec or
		// ellipsis.
		if IsParamSpec(typeArgType) || IsEllipsisType(typeArgType) {
			return typeArgs, true
		}

		// The original's comment: don't transform a Concatenate.
		if IsInstantiableClass(typeArgType) && ClassTypeIsBuiltInNamed(typeArgType.(*ClassType), "Concatenate") {
			return typeArgs, true
		}
	}

	// The original's comment: package up the type arguments into a type list.
	var node parser.ParseNode = errorNode
	if len(typeArgs) > 0 {
		node = typeArgs[0].Node
	}

	return []*TypeResultWithNode{
		{
			TypeResult: TypeResult{
				Type:            UnknownTypeCreate(false),
				TypeList:        typeArgs,
				TypeListPresent: true,
			},
			Node: node,
		},
	}, true
}

// GetTypeOfArg corresponds to getTypeOfArg.
func (e *typeEvaluator) GetTypeOfArg(arg *Arg, inferenceContext *InferenceContext) *TypeResult {
	if arg.TypeResult != nil {
		// `type?.props?.specialForm ?? type` -- a special form is unwrapped to
		// its runtime class here, unlike everywhere else, because an argument is
		// a value position.
		t := arg.TypeResult.Type
		if t != nil {
			if props := t.Base().Props; props != nil && props.SpecialForm != nil {
				t = props.SpecialForm
			}
		}
		return &TypeResult{Type: t, IsIncomplete: arg.TypeResult.IsIncomplete}
	}

	if arg.ValueExpression == nil {
		// The original's comment: we shouldn't ever get here, but just in case.
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	// The original's comment: if there was no defined type provided, there
	// should always be a value expression from which we can retrieve the type.
	return e.getTypeOfExpression(arg.ValueExpression, EvalFlagsNone, inferenceContext)
}
