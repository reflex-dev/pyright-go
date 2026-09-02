/*
 * typeevaluator_aliasspecialize.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * createSpecializedTypeAlias.
 *
 * The original's comment: handles index expressions that are providing type
 * arguments for a generic type alias.
 *
 * Structurally it mirrors createSpecializedClassType -- count arguments, check
 * them, solve -- but the mechanism differs in a way that matters. A class's type
 * arguments are assigned positionally; an alias's are assigned into a
 * ConstraintTracker via assignTypeVar and then SOLVED, so an argument that
 * appears twice in the alias body has to be consistent with itself. That is why
 * this ends at solveConstraints rather than at a positional zip.
 *
 * Four returns are possible before that: not an alias, not instantiable,
 * already specialized, or mypy_extensions.FlexibleAlias -- which the original
 * special-cases to return its first type argument unchanged.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// createSpecializedTypeAlias corresponds to the function of the same name. It
// returns nil where the original returns undefined, meaning "this index
// expression is not a type alias specialization".
func (e *typeEvaluator) createSpecializedTypeAlias(
	node *parser.IndexNode,
	baseType Type,
	flags EvalFlags,
) *TypeResultWithNode {
	props := baseType.Base().Props

	var aliasInfo *TypeAliasInfo
	aliasBaseType := baseType

	if props != nil {
		aliasInfo = props.TypeAliasInfo
	}

	if aliasInfo == nil && props != nil && props.TypeForm != nil {
		if typeFormProps := props.TypeForm.Base().Props; typeFormProps != nil {
			aliasInfo = typeFormProps.TypeAliasInfo
		}
		aliasBaseType = ConvertToInstantiable(props.TypeForm, false)
	}

	if aliasInfo == nil || aliasInfo.Shared == nil || aliasInfo.Shared.TypeParams == nil ||
		(len(aliasInfo.Shared.TypeParams) == 0 && aliasInfo.TypeArgs != nil) {
		return nil
	}

	// The original's comment: if this is not instantiable, then the index
	// expression isn't a specialization.
	if !aliasBaseType.Base().IsInstantiable() {
		return nil
	}

	// The original's comment: if this is already specialized, the index
	// expression isn't a specialization.
	if aliasInfo.TypeArgs != nil {
		return nil
	}

	e.inferVarianceForTypeAlias(baseType)

	typeParams := aliasInfo.Shared.TypeParams
	typeArgs := e.adjustTypeArgsForTypeVarTuple(e.getTypeArgs(node, flags, nil), typeParams, node)
	reportedError := false

	transformed, transformedPresent := e.transformTypeArgsForParamSpec(typeParams, typeArgs, true, node)
	if !transformedPresent {
		typeArgs = []*TypeResultWithNode{}
		reportedError = true
	} else {
		typeArgs = transformed
	}

	minTypeArgCount := len(typeParams)
	for index, param := range typeParams {
		if param.Shared.IsDefaultExplicit {
			minTypeArgCount = index
			break
		}
	}

	if len(typeArgs) > len(typeParams) {
		e.AddDiagnostic(
			DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.TypeArgsTooMany().Format(
				e.PrintType(aliasBaseType, nil), len(typeParams), len(typeArgs),
			),
			typeArgs[len(typeParams)].Node,
			nil,
		)
		reportedError = true
	} else if len(typeArgs) < minTypeArgCount {
		// Note the original reports typeParams.length as "expected" here, not
		// minTypeArgCount as createSpecializedClassType does for the same case.
		e.AddDiagnostic(
			DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.TypeArgsTooFew().Format(
				e.PrintType(aliasBaseType, nil), len(typeParams), len(typeArgs),
			),
			node.D.Items[len(node.D.Items)-1],
			nil,
		)
		reportedError = true
	}

	// The original's comment: handle the mypy_extensions.FlexibleAlias type
	// specially.
	if IsInstantiableClass(aliasBaseType) &&
		aliasBaseType.(*ClassType).Shared.FullName == "mypy_extensions.FlexibleAlias" &&
		len(typeArgs) >= 1 {
		return &TypeResultWithNode{TypeResult: TypeResult{Type: typeArgs[0].Type}, Node: node}
	}

	constraints := NewConstraintTracker()
	diag := common.NewDiagnosticAddendum()

	for index, param := range typeParams {
		if IsParamSpec(param) && index < len(typeArgs) {
			if !e.assignAliasParamSpecArg(param, typeArgs[index], constraints, diag) {
				e.AddDiagnostic(
					DiagnosticRuleReportInvalidTypeForm,
					localization.LocMessage.TypeArgListExpected(),
					typeArgs[index].Node,
					nil,
				)
				reportedError = true
			}
			continue
		}

		if index < len(typeArgs) && typeArgs[index].TypeListPresent {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.TypeArgListNotAllowed(),
				typeArgs[index].Node,
				nil,
			)
			reportedError = true
		}

		var typeArgType Type
		switch {
		case index < len(typeArgs):
			typeArgType = ConvertToInstance(typeArgs[index].Type, false)
		case param.Shared.IsDefaultExplicit:
			typeArgType = e.SolveAndApplyConstraints(param, constraints, &ApplyTypeVarOptions{
				ReplaceUnsolved: &ReplaceUnsolvedOptions{
					ScopeIDs:       []TypeVarScopeId{aliasInfo.Shared.TypeVarScopeId},
					TupleClassType: e.GetTupleClassType(),
				},
			}, nil)
		default:
			typeArgType = UnknownTypeCreate(false)
		}

		if (flags & EvalFlagsEnforceVarianceConsistency) != 0 {
			usageVariances := e.inferVarianceForTypeAlias(aliasBaseType)
			if index < len(usageVariances) {
				if !e.isVarianceOfTypeArgCompatible(typeArgType, usageVariances[index]) {
					messageDiag := diag.CreateAddendum()
					messageDiag.AddMessage(localization.LocAddendum.VarianceMismatchForTypeAlias().Format(
						e.PrintType(typeArgType, nil),
						e.PrintType(typeParams[index], nil),
					))
					messageDiag.AddTextRange(typeArgs[index].Node.NodeBase().TextRange)
				}
			}
		}

		if IsUnpacked(typeArgType) && !IsTypeVarTuple(param) {
			messageDiag := diag.CreateAddendum()
			messageDiag.AddMessage(localization.LocMessage.UnpackedArgInTypeArgument())
			messageDiag.AddTextRange(typeArgs[index].Node.NodeBase().TextRange)
			typeArgType = UnknownTypeCreate(false)
		}

		e.AssignTypeVar(param, typeArgType, diag, constraints, AssignTypeFlagsRetainLiteralsForTypeVar, 0)
	}

	if !diag.IsEmpty() {
		var textRange *common.TextRange
		if effective := diag.GetEffectiveTextRange(); effective != nil {
			textRange = effective
		} else {
			r := node.NodeBase().TextRange
			textRange = &r
		}

		e.AddDiagnostic(
			DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.TypeNotSpecializable().Format(e.PrintType(aliasBaseType, nil))+diag.GetString(),
			node,
			textRange,
		)
		reportedError = true
	}

	solutionSet := e.SolveConstraints(constraints, nil).GetMainSolutionSet()
	aliasTypeArgs := []Type{}

	for _, typeParam := range aliasInfo.Shared.TypeParams {
		typeVarType := solutionSet.GetType(typeParam)

		// The original's comment: fill in any unsolved type arguments with
		// unknown.
		if typeVarType == nil {
			typeVarType = GetUnknownForTypeVar(typeParam, e.GetTupleClassType())
			constraints.SetBounds(typeParam, typeVarType, nil, false)
		}

		aliasTypeArgs = append(aliasTypeArgs, typeVarType)
	}

	// `{ ...aliasInfo, typeArgs: aliasTypeArgs }`
	t := CloneForTypeAlias(
		e.SolveAndApplyConstraints(aliasBaseType, constraints, nil, nil),
		&TypeAliasInfo{Shared: aliasInfo.Shared, TypeArgs: aliasTypeArgs},
	)

	var typeFormType Type
	if !reportedError {
		typeFormType = ConvertToInstance(t, false)
	}
	t = CloneWithTypeForm(t, typeFormType)

	if props != nil && props.TypeAliasInfo != nil {
		return &TypeResultWithNode{TypeResult: TypeResult{Type: t}, Node: node}
	}

	return &TypeResultWithNode{
		TypeResult: TypeResult{Type: CloneWithTypeForm(baseType, ConvertToInstance(t, false))},
		Node:       node,
	}
}

// assignAliasParamSpecArg is the original's ParamSpec arm. It reports whether it
// recognized the argument; false means the caller emits typeArgListExpected.
func (e *typeEvaluator) assignAliasParamSpecArg(
	param *TypeVarType,
	typeArg *TypeResultWithNode,
	constraints *ConstraintTracker,
	diag *common.DiagnosticAddendum,
) bool {
	typeArgType := typeArg.Type

	if typeArg.TypeListPresent {
		functionType := FunctionTypeCreateSynthesizedInstance("", FunctionTypeFlagsParamSpecValue)

		for paramIndex, paramTypeResult := range typeArg.TypeList {
			paramType := paramTypeResult.Type

			if !e.ValidateTypeArg(paramTypeResult, nil) {
				paramType = UnknownTypeCreate(false)
			}

			name := "__p" + itoa(paramIndex)
			FunctionTypeAddParam(functionType, FunctionParamCreate(
				parser.ParamCategorySimple,
				ConvertToInstance(paramType, false),
				FunctionParamFlagsNameSynthesized|FunctionParamFlagsTypeDeclared,
				&name,
				nil,
				nil,
			))
		}

		if len(typeArg.TypeList) > 0 {
			FunctionTypeAddPositionOnlyParamSeparator(functionType)
		}

		e.AssignTypeVar(param, functionType, diag, constraints, AssignTypeFlagsRetainLiteralsForTypeVar, 0)
		return true
	}

	if IsParamSpec(typeArgType) {
		e.AssignTypeVar(param, ConvertToInstance(typeArgType, false), diag, constraints,
			AssignTypeFlagsRetainLiteralsForTypeVar, 0)
		return true
	}

	if IsInstantiableClass(typeArgType) && ClassTypeIsBuiltInNamed(typeArgType.(*ClassType), "Concatenate") {
		concatTypeArgs := typeArgType.(*ClassType).Priv.TypeArgs
		// Note: unlike the ParamSpec handling in createSpecializedClassType, this
		// one uses createInstance rather than createSynthesizedInstance and adds
		// the position-only separator BEFORE the trailing element rather than
		// after the group.
		functionType := FunctionTypeCreateInstance("", "", "", FunctionTypeFlagsNone, nil)

		for index, concatArg := range concatTypeArgs {
			if index == len(concatTypeArgs)-1 {
				FunctionTypeAddPositionOnlyParamSeparator(functionType)

				if IsParamSpec(concatArg) {
					FunctionTypeAddParamSpecVariadics(functionType, concatArg.(*TypeVarType))
				} else if IsEllipsisType(concatArg) {
					FunctionTypeAddDefaultParams(functionType, false)
					functionType.Shared.Flags |= FunctionTypeFlagsGradualCallableForm
				}
				continue
			}

			name := "__p" + itoa(index)
			FunctionTypeAddParam(functionType, FunctionParamCreate(
				parser.ParamCategorySimple,
				concatArg,
				FunctionParamFlagsNameSynthesized|FunctionParamFlagsTypeDeclared,
				&name,
				nil,
				nil,
			))
		}

		e.AssignTypeVar(param, functionType, diag, constraints, AssignTypeFlagsRetainLiteralsForTypeVar, 0)
		return true
	}

	if IsEllipsisType(typeArgType) {
		functionType := FunctionTypeCreateSynthesizedInstance("",
			FunctionTypeFlagsParamSpecValue|FunctionTypeFlagsGradualCallableForm)
		FunctionTypeAddDefaultParams(functionType, false)
		// Note: this call omits the RetainLiteralsForTypeVar flag the other
		// three pass.
		e.AssignTypeVar(param, functionType, diag, constraints, AssignTypeFlagsDefault, 0)
		return true
	}

	return false
}

/*
 * The three constraint-solver entry points and the variance inference this
 * reaches. All four are separate units of work.
 */

// AssignTypeVar and SolveConstraints are the evaluator-side wrappers over the
// constraintSolver.ts functions of the same names, which take the evaluator as
// their first argument.
func (e *typeEvaluator) AssignTypeVar(
	destType *TypeVarType,
	srcType Type,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	return AssignTypeVar(e, destType, srcType, diag, constraints, flags, recursionCount)
}

func (e *typeEvaluator) SolveConstraints(
	constraints *ConstraintTracker,
	options *SolveConstraintsOptions,
) *ConstraintSolution {
	return SolveConstraints(e, constraints, options)
}

// inferVarianceForTypeAlias corresponds to the function of the same name. The
// original's comment: determines the effective variance of the type parameters
// for a generic type alias. Normally variance is not important for type aliases,
// but it matters when the alias is used to specify a base class.
func (e *typeEvaluator) inferVarianceForTypeAlias(_ Type) []Variance {
	e.unported("inferVarianceForTypeAlias")
	return nil
}
