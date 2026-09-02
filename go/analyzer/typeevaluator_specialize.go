/*
 * typeevaluator_specialize.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * specializeTypeAliasWithDefaults.
 *
 * A generic type alias used without type arguments -- `MyAlias` where the alias
 * was declared as `MyAlias = list[T]` -- is specialized here with each type
 * parameter's default. A parameter with no explicit default gets Unknown (or
 * `*tuple[Unknown, ...]` for a TypeVarTuple) and triggers the
 * reportMissingTypeArgument diagnostic, reported once for the alias rather than
 * once per parameter.
 *
 * The defaults are accumulated into a ConstraintTracker as they are computed,
 * not solved independently, because a later parameter's default may refer to an
 * earlier one: `MyAlias = dict[K, V]` with `V = list[K]` needs K already bound
 * when V is solved.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// specializeTypeAliasWithDefaults corresponds to the function of the same name.
// errorNode is nil where the original's is undefined, which suppresses the
// diagnostic but not the specialization.
func (e *typeEvaluator) specializeTypeAliasWithDefaults(t Type, errorNode parser.ExpressionNode) Type {
	// Is this a type alias?
	props := t.Base().Props
	if props == nil || props.TypeAliasInfo == nil {
		return t
	}
	aliasInfo := props.TypeAliasInfo

	// The original's comment: is this a generic type alias that needs
	// specializing?
	if aliasInfo.Shared == nil || len(aliasInfo.Shared.TypeParams) == 0 || aliasInfo.TypeArgs != nil {
		return t
	}

	reportDiag := false
	defaultTypeArgs := []Type{}
	constraints := NewConstraintTracker()

	replaceUnsolved := func() *ApplyTypeVarOptions {
		return &ApplyTypeVarOptions{
			ReplaceUnsolved: &ReplaceUnsolvedOptions{
				ScopeIDs:       []TypeVarScopeId{aliasInfo.Shared.TypeVarScopeId},
				TupleClassType: e.GetTupleClassType(),
			},
		}
	}

	for _, param := range aliasInfo.Shared.TypeParams {
		if !param.Shared.IsDefaultExplicit {
			reportDiag = true
		}

		var defaultType Type
		switch {
		case param.Shared.IsDefaultExplicit || IsParamSpec(param):
			defaultType = e.SolveAndApplyConstraints(param, constraints, replaceUnsolved(), nil)

		case IsTypeVarTuple(param) && e.prefetched != nil &&
			e.prefetched.TupleClass != nil && IsInstantiableClass(e.prefetched.TupleClass):
			defaultType = MakeTupleObject(e, []*TupleTypeArg{{Type: UnknownTypeCreate(false), IsUnbounded: true}}, true)

		default:
			defaultType = UnknownTypeCreate(false)
		}

		defaultTypeArgs = append(defaultTypeArgs, defaultType)

		// The original passes only the type var and one bound; the remaining
		// parameters of this port's SetBounds are the original's defaults.
		constraints.SetBounds(param, defaultType, nil, false)
	}

	if reportDiag && errorNode != nil {
		e.AddDiagnostic(
			DiagnosticRuleReportMissingTypeArgument,
			localization.LocMessage.TypeArgsMissingForAlias().Format(aliasInfo.Shared.Name),
			errorNode,
			nil,
		)
	}

	// `{ ...aliasInfo, typeArgs: defaultTypeArgs }` -- a copy carrying the same
	// shared info with the computed arguments attached.
	specializedInfo := &TypeAliasInfo{
		Shared:   aliasInfo.Shared,
		TypeArgs: defaultTypeArgs,
	}

	return CloneForTypeAlias(
		e.SolveAndApplyConstraints(t, constraints, replaceUnsolved(), nil),
		specializedInfo,
	)
}
