/*
 * typeevaluator_typealias.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * transformTypeForTypeAlias.
 *
 * This is what turns an evaluated right-hand side into an actual type alias: it
 * works out the alias's type parameters, validates them, and attaches the
 * TypeAliasInfo that marks the type as an alias rather than a variable.
 *
 * That attachment is load bearing well beyond aliases. isSymbolValidTypeExpression
 * asks whether a symbol with a variable declaration carries TypeAliasInfo, and
 * answers "not valid in a type expression" when it does not -- so with this
 * stubbed, every type alias in the corpus was reported as
 * "Variable not allowed in type expression". That was 353 of the 1,182
 * diagnostics the gate's rule tally showed, and the largest remaining
 * false-positive family.
 */

package analyzer

import (
	"fmt"
	"strings"

	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// transformTypeForTypeAlias corresponds to the function of the same name. The
// original's typeParamNodes parameter is optional; a nil slice is the absent
// case.
func (e *typeEvaluator) transformTypeForTypeAlias(
	t Type,
	errorNode parser.ParseNode,
	typeAliasPlaceholder *TypeVarType,
	isPep695TypeVarType bool,
) Type {
	return e.transformTypeForTypeAliasEx(t, errorNode, typeAliasPlaceholder, isPep695TypeVarType, nil)
}

func (e *typeEvaluator) transformTypeForTypeAliasEx(
	t Type,
	errorNode parser.ParseNode,
	typeAliasPlaceholder *TypeVarType,
	isPep695TypeVarType bool,
	typeParamNodes []*parser.TypeParameterNode,
) Type {
	// The original's comment: if this is a recursive type alias that hasn't yet
	// been fully resolved (i.e. there is no boundType associated with it), don't
	// apply the transform.
	if IsTypeAliasPlaceholder(t) {
		return t
	}

	// The original asserts the placeholder carries recursiveAlias.
	sharedInfo := typeAliasPlaceholder.Shared.RecursiveAlias
	if sharedInfo == nil {
		return t
	}

	typeParams := sharedInfo.TypeParams
	if typeParams == nil {
		// The original's comment: determine if there are any generic type
		// parameters associated with this type alias.
		typeParams = AddTypeVarsToListIfUnique([]*TypeVarType{}, GetTypeVarArgsRecursive(t, 0), "")

		// The original's comment: don't include any synthesized type variables.
		filtered := make([]*TypeVarType, 0, len(typeParams))
		for _, typeVar := range typeParams {
			if !typeVar.Shared.IsSynthesized {
				filtered = append(filtered, typeVar)
			}
		}
		typeParams = filtered
	}

	// The original's comment: convert all type variables to instances.
	converted := make([]*TypeVarType, 0, len(typeParams))
	for _, typeVar := range typeParams {
		if typeVar.Base().IsInstance() {
			converted = append(converted, typeVar)
			continue
		}
		if asTypeVar, ok := ConvertToInstance(typeVar, true).(*TypeVarType); ok {
			converted = append(converted, asTypeVar)
		}
	}
	typeParams = converted

	e.checkAliasVariadics(errorNode, typeParams)

	// The original's comment: validate the default types for all type
	// parameters.
	for index, typeParam := range typeParams {
		bestErrorNode := errorNode
		if typeParamNodes != nil && index < len(typeParamNodes) {
			if typeParamNodes[index].D.DefaultExpr != nil {
				bestErrorNode = typeParamNodes[index].D.DefaultExpr
			} else {
				bestErrorNode = typeParamNodes[index].D.Name
			}
		}
		if asExpr, ok := bestErrorNode.(parser.ExpressionNode); ok {
			e.validateTypeParamDefault(asExpr, typeParam, typeParams[:index], sharedInfo.TypeVarScopeId)
		}
	}

	if !sharedInfo.IsTypeAliasType && !isPep695TypeVarType {
		var boundTypeVars []string
		for _, typeVar := range typeParams {
			if typeVar.Priv.ScopeID != sharedInfo.TypeVarScopeId &&
				typeVar.Priv.ScopeType != nil && *typeVar.Priv.ScopeType == TypeVarScopeTypeClass {
				boundTypeVars = append(boundTypeVars, typeVar.Shared.Name)
			}
		}

		if len(boundTypeVars) > 0 {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.GenericTypeAliasBoundTypeVar().Format(strings.Join(boundTypeVars, ", ")),
				errorNode,
				nil,
			)
		}
	}

	if !t.Base().IsInstantiable() {
		return t
	}

	// `typeParams.length > 0 ? typeParams : undefined` -- an empty list becomes
	// absent, which is what specializeTypeAliasWithDefaults tests for.
	if len(typeParams) > 0 {
		sharedInfo.TypeParams = typeParams
	} else {
		sharedInfo.TypeParams = nil
	}

	typeAlias := CloneForTypeAlias(t, &TypeAliasInfo{Shared: sharedInfo})

	// The original's comment: all PEP 695 type aliases are special forms because
	// they are TypeAliasType objects at runtime.
	if sharedInfo.IsTypeAliasType || isPep695TypeVarType {
		typeAliasTypeClass := e.getTypingType(errorNode, "TypeAliasType")
		if typeAliasTypeClass != nil && IsInstantiableClass(typeAliasTypeClass) {
			typeAlias = CloneAsSpecialForm(typeAlias,
				ClassTypeCloneAsInstance(typeAliasTypeClass.(*ClassType), false))
		}
	}

	// The original's comment: delete the TypeForm info. The type alias serves as
	// its own TypeForm info.
	if props := typeAlias.Base().Props; props != nil && props.TypeForm != nil {
		typeAlias = CloneWithTypeForm(typeAlias, nil)
	}

	return typeAlias
}

// checkAliasVariadics is the original's two TypeVarTuple checks, which mirror
// the pair in class creation but report different messages.
func (e *typeEvaluator) checkAliasVariadics(errorNode parser.ParseNode, typeParams []*TypeVarType) {
	// The original's comment: see if the type alias includes a TypeVarTuple
	// followed by a TypeVar with a default value. This isn't allowed.
	firstTypeVarTupleIndex := -1
	for i, typeVar := range typeParams {
		if IsTypeVarTuple(typeVar) {
			firstTypeVarTupleIndex = i
			break
		}
	}

	if firstTypeVarTupleIndex >= 0 {
		typeVarWithDefaultIndex := -1
		for i, typeVar := range typeParams {
			if i > firstTypeVarTupleIndex && !IsParamSpec(typeVar) && typeVar.Shared.IsDefaultExplicit {
				typeVarWithDefaultIndex = i
				break
			}
		}

		if typeVarWithDefaultIndex >= 0 {
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeVarWithDefaultFollowsVariadic().Format(
					typeParams[firstTypeVarTupleIndex].Shared.Name,
					typeParams[typeVarWithDefaultIndex].Shared.Name,
				),
				errorNode,
				nil,
			)
		}
	}

	// The original's comment: verify that we have at most one TypeVarTuple.
	var variadicNames []string
	for _, param := range typeParams {
		if IsTypeVarTuple(param) {
			variadicNames = append(variadicNames, fmt.Sprintf("%q", param.Shared.Name))
		}
	}

	if len(variadicNames) > 1 {
		e.AddDiagnostic(
			DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.VariadicTypeParamTooManyAlias().Format(strings.Join(variadicNames, ", ")),
			errorNode,
			nil,
		)
	}
}
