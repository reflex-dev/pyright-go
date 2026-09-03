/*
 * typeevaluator_createtypevar.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * createTypeVarType and getBooleanValue.
 *
 * `T = TypeVar("T", bound=int)` -- reading a TypeVar out of its own constructor
 * call. The result is a TypeVarType, not an instance of the TypeVar class, which
 * is why this is intercepted before the constructor path runs at all.
 *
 * The argument shape encodes the distinction the type system cares about:
 * positional arguments after the name are CONSTRAINTS, keyword arguments are
 * everything else. A TypeVar may have a bound or constraints but never both, and
 * both directions of that are checked -- a `bound=` after positional arguments,
 * and a positional argument after a `bound=`.
 *
 * Several checks here exist because the value is read at runtime but only means
 * something statically:
 *
 *   - A single constraint is an error. `TypeVar("T", int)` reads as constrained
 *     to exactly int, which is the same as no TypeVar at all; the user almost
 *     certainly meant a bound.
 *   - A bound or constraint that requires specialization -- `bound=list[S]` --
 *     is rejected, because there is no scope in which S could be solved.
 *   - `default=` is a 3.13 feature, so a non-stub file targeting less than 3.13
 *     reports it, unless it came from typing_extensions, which backports it.
 *
 * The variance flags are three ways of writing one field, so each rejects the
 * combinations already excluded by the others rather than silently overwriting.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// createTypeVarType corresponds to the function of the same name. Returning nil
// is the original's undefined.
func (e *typeEvaluator) createTypeVarType(
	errorNode parser.ExpressionNode, classType *ClassType, argList []*Arg,
) Type {
	if len(argList) == 0 {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeVarFirstArg(), errorNode, nil)
		return nil
	}

	typeVarName := ""
	firstArg := argList[0]
	if stringList, ok := firstArg.ValueExpression.(*parser.StringListNode); ok {
		for _, s := range stringList.D.Strings {
			if stringNode, ok := s.(*parser.StringNode); ok {
				typeVarName += stringNode.D.Value.String()
			}
		}
	} else {
		// The name is still empty in this case, and the TypeVar is still built:
		// the original reports and carries on rather than returning.
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeVarFirstArg(),
			argOrErrorNode(firstArg, errorNode), nil)
	}

	typeVar := CloneAsSpecialForm(
		TypeVarTypeCreateInstantiable(typeVarName, TypeVarKindTypeVar),
		ClassTypeCloneAsInstance(classType, true))

	// The original's comment: parse the remaining parameters.
	paramNameMap := common.NewOrderedMap[string, string]()
	var firstConstraintArg *Arg
	var defaultValueNode parser.ExpressionNode

	for i := 1; i < len(argList); i++ {
		arg := argList[i]

		if arg.Name == nil {
			// Positional: a constraint.
			if TypeVarTypeHasBound(typeVar) {
				e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.TypeVarBoundAndConstrained(),
					argOrErrorNode(arg, errorNode), nil)
				continue
			}

			argType := e.typeVarArgType(arg, &ExpectedTypeOptions{TypeExpression: true})

			if RequiresSpecialization(argType, &RequiresSpecializationOptions{IgnorePseudoGeneric: true}, 0) {
				e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.TypeVarConstraintGeneric(),
					argOrErrorNode(arg, errorNode), nil)
			}

			TypeVarTypeAddConstraint(typeVar, ConvertToInstance(argType, true))
			if firstConstraintArg == nil {
				firstConstraintArg = arg
			}
			continue
		}

		paramName := arg.Name.D.Value

		if _, seen := paramNameMap.Get(paramName); seen {
			e.AddDiagnostic(DiagnosticRuleReportCallIssue,
				localization.LocMessage.DuplicateParam().Format(paramName),
				argOrErrorNode(arg, errorNode), nil)
		}

		e.applyTypeVarKeywordArg(errorNode, classType, typeVar, arg, paramName, &defaultValueNode)

		paramNameMap.Set(paramName, paramName)
	}

	if len(typeVar.Shared.Constraints) == 1 && firstConstraintArg != nil {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeVarSingleConstraint(),
			argOrErrorNode(firstConstraintArg, errorNode), nil)
	}

	// The original's comment: if a default is provided, make sure it is
	// compatible with the bound or constraint.
	if typeVar.Shared.IsDefaultExplicit && defaultValueNode != nil {
		e.verifyTypeVarDefaultIsCompatible(typeVar, defaultValueNode)
	}

	return typeVar
}

// applyTypeVarKeywordArg is the keyword half of the loop.
func (e *typeEvaluator) applyTypeVarKeywordArg(
	errorNode parser.ExpressionNode,
	classType *ClassType,
	typeVar *TypeVarType,
	arg *Arg,
	paramName string,
	defaultValueNode *parser.ExpressionNode,
) {
	switch paramName {
	case "bound":
		if TypeVarTypeHasConstraints(typeVar) {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeVarBoundAndConstrained(),
				argOrErrorNode(arg, errorNode), nil)
			return
		}

		argType := e.typeVarArgType(arg, &ExpectedTypeOptions{
			NoNonTypeSpecialForms: true,
			TypeExpression:        true,
			ParsesStringLiteral:   true,
		})

		if RequiresSpecialization(argType, &RequiresSpecializationOptions{
			IgnorePseudoGeneric: true, IgnoreImplicitTypeArgs: true}, 0) {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeVarBoundGeneric(),
				argOrErrorNode(arg, errorNode), nil)
		}

		typeVar.Shared.BoundType = ConvertToInstance(argType, true)

	case "covariant":
		if arg.ValueExpression != nil && e.getBooleanValue(arg.ValueExpression) {
			if typeVar.Shared.DeclaredVariance == VarianceContravariant ||
				typeVar.Shared.DeclaredVariance == VarianceAuto {
				e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.TypeVarVariance(), arg.ValueExpression, nil)
			} else {
				typeVar.Shared.DeclaredVariance = VarianceCovariant
			}
		}

	case "contravariant":
		if arg.ValueExpression != nil && e.getBooleanValue(arg.ValueExpression) {
			if typeVar.Shared.DeclaredVariance == VarianceCovariant ||
				typeVar.Shared.DeclaredVariance == VarianceAuto {
				e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.TypeVarVariance(), arg.ValueExpression, nil)
			} else {
				typeVar.Shared.DeclaredVariance = VarianceContravariant
			}
		}

	case "infer_variance":
		if arg.ValueExpression != nil && e.getBooleanValue(arg.ValueExpression) {
			if typeVar.Shared.DeclaredVariance == VarianceCovariant ||
				typeVar.Shared.DeclaredVariance == VarianceContravariant {
				e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.TypeVarVariance(), arg.ValueExpression, nil)
			} else {
				typeVar.Shared.DeclaredVariance = VarianceAuto
			}
		}

	case "default":
		*defaultValueNode = arg.ValueExpression

		argType := e.typeVarArgType(arg, &ExpectedTypeOptions{
			AllowTypeVarsWithoutScopeId: true,
			TypeExpression:              true,
		})

		typeVar.Shared.DefaultType = ConvertToInstance(argType, true)
		typeVar.Shared.IsDefaultExplicit = true

		// PEP 696 landed in 3.13; typing_extensions backports it, so a TypeVar
		// from there is exempt.
		fileInfo := GetFileInfo(errorNode)
		if !fileInfo.IsStubFile &&
			fileInfo.ExecutionEnvironment.PythonVersion.IsLessThan(common.PythonVersion3_13) &&
			classType.Shared.ModuleName != "typing_extensions" {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeVarDefaultIllegal(), *defaultValueNode, nil)
		}

	default:
		// `argList[i].node?.d.name || valueExpression || errorNode` -- the name
		// node is preferred so the error underlines the keyword, not its value.
		var node parser.ExpressionNode
		if arg.Node != nil && arg.Node.D.Name != nil {
			node = arg.Node.D.Name
		} else {
			node = argOrErrorNode(arg, errorNode)
		}

		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.TypeVarUnknownParam().Format(paramName), node, nil)
	}
}

// typeVarArgType is the original's `argList[i].typeResult?.type ??
// getTypeOfExpressionExpectingType(...)`: a pre-evaluated type wins, otherwise
// the expression is evaluated as a type expression.
func (e *typeEvaluator) typeVarArgType(arg *Arg, options *ExpectedTypeOptions) Type {
	if arg.TypeResult != nil {
		return arg.TypeResult.Type
	}
	return e.GetTypeOfExpressionExpectingType(arg.ValueExpression, options).Type
}

// argOrErrorNode is the original's `arg.valueExpression || errorNode`.
func argOrErrorNode(arg *Arg, errorNode parser.ExpressionNode) parser.ExpressionNode {
	if arg.ValueExpression != nil {
		return arg.ValueExpression
	}
	return errorNode
}

// getBooleanValue corresponds to the function of the same name: only a literal
// True or False counts, since the flag has to be known statically.
func (e *typeEvaluator) getBooleanValue(node parser.ExpressionNode) bool {
	if constNode, ok := node.(*parser.ConstantNode); ok {
		switch constNode.D.ConstType {
		case parser.KeywordTypeFalse:
			return false
		case parser.KeywordTypeTrue:
			return true
		}
	}

	e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.ExpectedBoolLiteral(), node, nil)
	return false
}
