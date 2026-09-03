/*
 * typeevaluator_createparamspec.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * createTypeVarTupleType and createParamSpecType.
 *
 * These are the runtime call forms `Ts = TypeVarTuple("Ts")` and
 * `P = ParamSpec("P")`. They are the two simpler siblings of createTypeVarType:
 * neither accepts constraints, a bound, covariance or contravariance, so the
 * whole of each is a name, an optional `default=`, and a rejection of anything
 * else.
 *
 * Both differ from createTypeVarType in a detail worth naming: an unrecognized
 * *keyword* argument is reported and the loop continues, but an unrecognized
 * *positional* argument stops the loop -- for a TypeVarTuple because a
 * positional argument would be a constraint and TypeVarTuples have none, for a
 * ParamSpec because the original explicitly breaks. Each also seeds
 * shared.defaultType before parsing arguments, so a TypeVarTuple with no
 * `default=` still defaults to `*tuple[Unknown, ...]` and a ParamSpec to
 * `(...) -> Unknown` rather than to nothing.
 *
 * PEP 696 defaults landed in Python 3.13; typing_extensions backports them, so
 * a TypeVarTuple or ParamSpec from there is exempt from the version check.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// typeVarNameFromFirstArg reads the string-literal first argument that names a
// TypeVar, TypeVarTuple or ParamSpec. A non-string first argument is reported
// with firstArgMessage and the name is left empty, which is what lets both
// callers keep building the type rather than bailing out.
func (e *typeEvaluator) typeVarNameFromFirstArg(
	firstArg *Arg, errorNode parser.ExpressionNode, firstArgMessage string,
) string {
	if stringList, ok := firstArg.ValueExpression.(*parser.StringListNode); ok {
		name := ""
		for _, s := range stringList.D.Strings {
			if stringNode, ok := s.(*parser.StringNode); ok {
				name += stringNode.D.Value.String()
			}
		}
		return name
	}

	e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, firstArgMessage,
		argOrErrorNode(firstArg, errorNode), nil)
	return ""
}

// reportTypeVarDefaultVersion emits the PEP 696 version diagnostic shared by
// createTypeVarType, createTypeVarTupleType and createParamSpecType.
func (e *typeEvaluator) reportTypeVarDefaultVersion(
	errorNode parser.ExpressionNode, classType *ClassType, expr parser.ExpressionNode,
) {
	fileInfo := GetFileInfo(errorNode)
	if !fileInfo.IsStubFile &&
		fileInfo.ExecutionEnvironment.PythonVersion.IsLessThan(common.PythonVersion3_13) &&
		classType.Shared.ModuleName != "typing_extensions" {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeVarDefaultIllegal(), expr, nil)
	}
}

// createTypeVarTupleType corresponds to the function of the same name.
func (e *typeEvaluator) createTypeVarTupleType(
	errorNode parser.ExpressionNode, classType *ClassType, argList []*Arg,
) Type {
	if len(argList) == 0 {
		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.TypeVarFirstArg(), errorNode, nil)
		return nil
	}

	typeVarName := e.typeVarNameFromFirstArg(
		argList[0], errorNode, localization.LocMessage.TypeVarFirstArg())

	typeVar := CloneAsSpecialForm(
		TypeVarTypeCreateInstantiable(typeVarName, TypeVarKindTypeVarTuple),
		ClassTypeCloneAsInstance(classType, true))
	typeVar.Shared.DefaultType = MakeTupleObject(e,
		[]*TupleTypeArg{{Type: UnknownTypeCreate(false), IsUnbounded: true}}, false)

	// The original's comment: parse the remaining parameters.
	for i := 1; i < len(argList); i++ {
		arg := argList[i]
		paramNameNode := arg.Name

		if paramNameNode == nil {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeVarTupleConstraints(),
				argOrErrorNode(arg, errorNode), nil)
			continue
		}

		paramName := paramNameNode.D.Value

		if paramName != "default" {
			// `argList[i].node?.d.name || valueExpression || errorNode` -- the name
			// node is preferred so the error underlines the keyword, not its value.
			var node parser.ExpressionNode
			if arg.Node != nil && arg.Node.D.Name != nil {
				node = arg.Node.D.Name
			} else {
				node = argOrErrorNode(arg, errorNode)
			}

			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeVarTupleUnknownParam().Format(paramName), node, nil)
			continue
		}

		expr := arg.ValueExpression
		if expr != nil {
			if defaultType := e.getTypeVarTupleDefaultType(expr, false); defaultType != nil {
				typeVar.Shared.DefaultType = defaultType
				typeVar.Shared.IsDefaultExplicit = true
			}
		}

		// The original dereferences expr unconditionally here, after having just
		// tested it. A missing value expression cannot occur for a keyword
		// argument, so the port skips the diagnostic rather than dereferencing nil.
		if expr != nil {
			e.reportTypeVarDefaultVersion(errorNode, classType, expr)
		}
	}

	return typeVar
}

// createParamSpecType corresponds to the function of the same name.
func (e *typeEvaluator) createParamSpecType(
	errorNode parser.ExpressionNode, classType *ClassType, argList []*Arg,
) Type {
	if len(argList) == 0 {
		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.ParamSpecFirstArg(), errorNode, nil)
		return nil
	}

	paramSpecName := e.typeVarNameFromFirstArg(
		argList[0], errorNode, localization.LocMessage.ParamSpecFirstArg())

	paramSpec := CloneAsSpecialForm(
		TypeVarTypeCreateInstantiable(paramSpecName, TypeVarKindParamSpec),
		ClassTypeCloneAsInstance(classType, true))

	paramSpec.Shared.DefaultType = ParamSpecTypeGetUnknown()

	// The original's comment: parse the remaining parameters.
	for i := 1; i < len(argList); i++ {
		arg := argList[i]
		paramNameNode := arg.Name

		if paramNameNode == nil {
			e.AddDiagnostic(DiagnosticRuleReportCallIssue,
				localization.LocMessage.ParamSpecUnknownArg(),
				argOrErrorNode(arg, errorNode), nil)
			break
		}

		paramName := paramNameNode.D.Value

		if paramName != "default" {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.ParamSpecUnknownParam().Format(paramName),
				paramNameNode, nil)
			continue
		}

		expr := arg.ValueExpression
		if expr != nil {
			if defaultType := e.getParamSpecDefaultType(expr, false); defaultType != nil {
				paramSpec.Shared.DefaultType = defaultType
				paramSpec.Shared.IsDefaultExplicit = true
			}
		}

		// See the note in createTypeVarTupleType about the original's unguarded
		// dereference of expr.
		if expr != nil {
			e.reportTypeVarDefaultVersion(errorNode, classType, expr)
		}
	}

	return paramSpec
}
