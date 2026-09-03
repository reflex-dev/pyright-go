/*
 * typeevaluator_createspecial.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * createSpecialType.
 *
 * The shared back end of the special forms. createSpecialFormType decides which
 * form a subscript names and what its argument rules are; this one applies those
 * rules and produces the specialized class.
 *
 * Three parameters carry those rules:
 *
 *   - paramLimit fixes an exact arity. Extra arguments are reported and dropped;
 *     missing ones are filled with Unknown, so the result always has the shape
 *     the form promises even when the source is wrong. A nil paramLimit means
 *     variadic, and only then are unpacked arguments allowed at all.
 *   - allowParamSpec permits a bare ParamSpec, which Callable needs and no other
 *     form does.
 *   - isSpecialForm marks the result so it prints as `Optional` rather than as
 *     the union it desugars to.
 *
 * Tuple is the one shape that does not fit. It carries its arguments in
 * tupleTypeArgs rather than typeArgs, `()` means the empty tuple rather than no
 * arguments, `[X, ...]` collapses two arguments into one unbounded entry, and an
 * unpacked tuple splices its entries in rather than nesting. All four are handled
 * in the isTupleTypeParam branch.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
)

// createSpecialType corresponds to the function of the same name. paramLimit,
// allowParamSpec and isSpecialForm are pointers because each of the original's
// defaults is not the Go zero value at every call site.
func (e *typeEvaluator) createSpecialType(
	classType *ClassType,
	typeArgs []*TypeResultWithNode,
	paramLimit *int,
	allowParamSpec *bool,
	isSpecialForm *bool,
) Type {
	// The original's defaults: allowParamSpec = false, isSpecialForm = true.
	allowParamSpecValue := allowParamSpec != nil && *allowParamSpec
	isSpecialFormValue := isSpecialForm == nil || *isSpecialForm

	isTupleTypeParam := ClassTypeIsTupleClass(classType)

	// The original tests `typeArgs !== undefined` several times below, and the
	// empty-tuple-shorthand branch reassigns it to a present-but-empty array. A
	// separate flag keeps the two apart without depending on nil-vs-empty.
	hasTypeArgs := typeArgs != nil

	if hasTypeArgs {
		if isTupleTypeParam && len(typeArgs) == 1 && typeArgs[0].IsEmptyTupleShorthand {
			// `tuple[()]` is the empty tuple, not a tuple with one argument.
			typeArgs = []*TypeResultWithNode{}
		} else {
			e.validateSpecialTypeArgs(typeArgs, isTupleTypeParam, paramLimit, allowParamSpecValue)
		}
	}

	typeArgTypes := make([]Type, 0, len(typeArgs))
	for _, typeArg := range typeArgs {
		typeArgTypes = append(typeArgTypes, ConvertToInstance(typeArg.Type, true))
	}

	// The original's comment: make sure the argument list count is correct.
	if paramLimit != nil {
		if hasTypeArgs && len(typeArgTypes) > *paramLimit {
			name := classType.Shared.Name
			if classType.Priv.AliasName != nil && *classType.Priv.AliasName != "" {
				name = *classType.Priv.AliasName
			}

			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.TypeArgsTooMany().Format(name, *paramLimit, len(typeArgTypes)),
				typeArgs[*paramLimit].Node,
				nil,
			)
			typeArgTypes = typeArgTypes[:*paramLimit]
		} else if len(typeArgTypes) < *paramLimit {
			// The original's comment: fill up the remainder of the slots with
			// unknown types.
			for len(typeArgTypes) < *paramLimit {
				typeArgTypes = append(typeArgTypes, UnknownTypeCreate(false))
			}
		}
	}

	var returnType Type
	if isTupleTypeParam {
		returnType = SpecializeTupleClass(
			classType, e.buildSpecialTupleArgs(typeArgs, typeArgTypes, hasTypeArgs), hasTypeArgs, false)
	} else {
		returnType = ClassTypeSpecialize(classType, typeArgTypes, &hasTypeArgs, false, nil, nil)
	}

	if isSpecialFormValue {
		returnType = CloneAsSpecialForm(returnType, classType)
	}

	return returnType
}

// validateSpecialTypeArgs is the original's "verify that we didn't receive any
// inappropriate types" loop.
//
// The unpacked-argument rules are only reachable when paramLimit is nil, since a
// form with a fixed arity has no room for one. sawUnpacked tracks whether a
// second unpacked argument has appeared: a type expression may contain at most
// one, because two would make the split between them ambiguous.
func (e *typeEvaluator) validateSpecialTypeArgs(
	typeArgs []*TypeResultWithNode,
	isTupleTypeParam bool,
	paramLimit *int,
	allowParamSpec bool,
) {
	sawUnpacked := false
	reportedUnpackedError := false

	noteSawUnpacked := func(typeArg *TypeResultWithNode) {
		if sawUnpacked && !reportedUnpackedError {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.VariadicTypeArgsTooMany(),
				typeArg.Node,
				nil,
			)
			reportedUnpackedError = true
		}
		sawUnpacked = true
	}

	for index, typeArg := range typeArgs {
		switch {
		case IsEllipsisType(typeArg.Type):
			e.validateSpecialEllipsisArg(typeArgs, typeArg, index, isTupleTypeParam, allowParamSpec)

		case IsParamSpec(typeArg.Type) && allowParamSpec:
			// The original's comment: nothing to do - this is allowed.

		case paramLimit == nil && IsTypeVarTuple(typeArg.Type):
			if !typeArg.Type.(*TypeVarType).Priv.IsInUnion {
				noteSawUnpacked(typeArg)
			}
			e.validateTypeVarTupleIsUnpacked(typeArg.Type.(*TypeVarType), typeArg.Node)

		case paramLimit == nil && IsUnpackedClass(typeArg.Type):
			if IsUnboundedTupleClass(typeArg.Type.(*ClassType)) {
				noteSawUnpacked(typeArg)
			}
			e.ValidateTypeArg(typeArg, &ValidateTypeArgsOptions{AllowUnpackedTuples: true})

		default:
			e.ValidateTypeArg(typeArg, nil)
		}
	}
}

// validateSpecialEllipsisArg is the ellipsis arm of that loop. An ellipsis is
// legal in exactly two places: as Callable's parameter list, and as the second
// of a tuple's two arguments, where it means "unbounded".
func (e *typeEvaluator) validateSpecialEllipsisArg(
	typeArgs []*TypeResultWithNode,
	typeArg *TypeResultWithNode,
	index int,
	isTupleTypeParam bool,
	allowParamSpec bool,
) {
	if !isTupleTypeParam {
		if !allowParamSpec {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.EllipsisContext(), typeArg.Node, nil)
		}
		return
	}

	if len(typeArgs) != 2 || index != 1 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.EllipsisSecondArg(), typeArg.Node, nil)
		return
	}

	// `tuple[*Ts, ...]` and `tuple[*tuple[int, ...], ...]` are both rejected:
	// the first argument already describes an unknown number of entries, so the
	// ellipsis has nothing to repeat.
	first := typeArgs[0]
	if IsTypeVarTuple(first.Type) && !first.Type.(*TypeVarType).Priv.IsInUnion {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.TypeVarTupleContext(), first.Node, nil)
	} else if IsUnpackedClass(first.Type) {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.EllipsisAfterUnpacked(), typeArg.Node, nil)
	}
}

// buildSpecialTupleArgs is the original's tupleTypeArgTypes construction.
func (e *typeEvaluator) buildSpecialTupleArgs(
	typeArgs []*TypeResultWithNode,
	typeArgTypes []Type,
	hasTypeArgs bool,
) []*TupleTypeArg {
	tupleTypeArgTypes := []*TupleTypeArg{}

	// The original's comment: if no type args are provided and it's a tuple,
	// default to [Unknown, ...].
	if !hasTypeArgs {
		return append(tupleTypeArgTypes, &TupleTypeArg{Type: UnknownTypeCreate(false), IsUnbounded: true})
	}

	for index, typeArg := range typeArgs {
		switch {
		// typeArgTypes is indexed in step with typeArgs. The paramLimit
		// truncation above could in principle make it shorter, but no call site
		// passes both a paramLimit and the tuple class.
		case index == 1 && IsEllipsisType(typeArgTypes[index]):
			// `tuple[X, ...]`: the ellipsis does not become an entry, it makes
			// the entry before it unbounded. The spread rather than an in-place
			// write is the original's, and the TupleTypeArg may be shared.
			if len(tupleTypeArgTypes) == 1 && !tupleTypeArgTypes[0].IsUnbounded {
				tupleTypeArgTypes[0] = &TupleTypeArg{Type: tupleTypeArgTypes[0].Type, IsUnbounded: true}
			}

		case IsUnpackedClass(typeArg.Type) && typeArg.Type.(*ClassType).Priv.TupleTypeArgs != nil:
			// `tuple[int, *tuple[str, str]]` splices rather than nests.
			tupleTypeArgTypes = append(tupleTypeArgTypes, typeArg.Type.(*ClassType).Priv.TupleTypeArgs...)

		default:
			tupleTypeArgTypes = append(tupleTypeArgTypes,
				&TupleTypeArg{Type: typeArgTypes[index], IsUnbounded: false})
		}
	}

	return tupleTypeArgTypes
}
