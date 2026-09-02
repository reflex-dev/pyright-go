/*
 * checker_dataclass.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateDataClassPostInit.
 *
 * A dataclass's `__post_init__` receives exactly the InitVar fields, in
 * declaration order, after `self`. Nothing in the type system enforces that --
 * the synthesized `__init__` calls it positionally at runtime -- so this pass
 * reconstructs the expected signature and compares.
 *
 * "Declaration order" is why the InitVars are collected over the *reverse* MRO:
 * a base class's InitVars are passed before a subclass's, and a subclass
 * redeclaring a field keeps the base's position. Using an ordered map with the
 * reverse MRO gets both properties for free -- a later Set on an existing key
 * updates the value without moving it.
 *
 * The arity comparison is deliberately two-sided and asymmetric. Too few
 * parameters to receive every InitVar is an error, and so is requiring more
 * non-defaulted parameters than there are InitVars -- but a trailing defaulted
 * parameter is fine, which is why the lower bound is checked against
 * nonDefaultParams and the upper bound against the full list.
 *
 * A `*args`, `**kwargs` or keyword-only separator abandons the check entirely,
 * since positional matching no longer describes the call.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateDataClassPostInit corresponds to _validateDataClassPostInit.
func (c *Checker) validateDataClassPostInit(classType *ClassType) {
	if !ClassTypeIsDataClass(classType) {
		return
	}

	postInitMember := LookUpClassMember(classType, "__post_init__",
		MemberAccessFlagsSkipBaseClasses|MemberAccessFlagsDeclaredTypesOnly, nil)

	// The original's comment: if there's no __post_init__ method, there's nothing
	// to check.
	if postInitMember == nil {
		return
	}

	// The original's comment: if the class derives from Any, we can't reliably
	// apply the check.
	if ClassTypeDerivesFromAnyOrUnknown(classType) {
		return
	}

	// The original's comment: collect the list of init-only variables in the
	// order they were declared.
	initOnlySymbolMap := common.NewOrderedMap[string, *Symbol]()
	for _, mroClass := range ClassTypeGetReverseMro(classType) {
		if !IsClass(mroClass) || !ClassTypeIsDataClass(mroClass.(*ClassType)) {
			continue
		}
		symbolTable := ClassTypeGetSymbolTable(mroClass.(*ClassType))
		for _, name := range symbolTable.Keys() {
			symbol, _ := symbolTable.Get(name)
			if symbol.IsInitVar() {
				initOnlySymbolMap.Set(name, symbol)
			}
		}
	}

	postInitType := c.evaluator.GetTypeOfMember(postInitMember)
	if !IsFunction(postInitType) {
		return
	}
	postInitFn := postInitType.(*FunctionType)
	if !FunctionTypeIsInstanceMethod(postInitFn) || postInitFn.Shared.Declaration == nil {
		return
	}

	paramListDetails := GetParamListDetails(postInitFn, nil)

	// The original's comment: if there is an *args or **kwargs parameter or a
	// keyword-only separator, don't bother checking.
	if paramListDetails.ArgsIndex != nil || paramListDetails.KwargsIndex != nil ||
		paramListDetails.FirstKeywordOnlyIndex != nil {
		return
	}

	// The original's comment: verify that the parameter count matches.
	nonDefaultParamCount := 0
	for index := range paramListDetails.Params {
		if FunctionTypeGetParamDefaultType(postInitFn, index) == nil {
			nonDefaultParamCount++
		}
	}

	// The original's comment: we expect to see one param for "self" plus one for
	// each of the InitVars.
	expectedParamCount := initOnlySymbolMap.Size() + 1

	postInitNode, ok := postInitFn.Shared.Declaration.Node.(*parser.FunctionNode)
	if !ok {
		return
	}

	if expectedParamCount < nonDefaultParamCount || expectedParamCount > len(paramListDetails.Params) {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.DataClassPostInitParamCount().Format(initOnlySymbolMap.Size()),
			postInitNode.D.Name, nil)
	}

	// The original's comment: verify that the parameter types match.
	paramIndex := 1

	for _, fieldName := range initOnlySymbolMap.Keys() {
		if paramIndex >= len(paramListDetails.Params) {
			// The original returns from the forEach callback here, which continues
			// the iteration rather than ending it. paramIndex is not incremented on
			// that path either, so the guard stays true for every later field.
			continue
		}

		symbol, _ := initOnlySymbolMap.Get(fieldName)
		param := paramListDetails.Params[paramIndex].Param

		var paramNode *parser.ParameterNode
		for _, node := range postInitNode.D.Params {
			if node.D.Name != nil && param.Name != nil && node.D.Name.D.Value == *param.Name {
				paramNode = node
				break
			}
		}

		var annotationNode parser.ExpressionNode
		if paramNode != nil {
			annotationNode = paramNode.D.Annotation
			if annotationNode == nil {
				annotationNode = paramNode.D.AnnotationComment
			}
		}

		if FunctionParamIsTypeDeclared(param) && annotationNode != nil {
			var fieldType Type
			if declared := c.evaluator.GetDeclaredTypeOfSymbol(symbol); declared != nil {
				fieldType = declared.Type
			}
			paramType := FunctionTypeGetParamType(postInitFn, paramListDetails.Params[paramIndex].Index)
			assignTypeDiag := common.NewDiagnosticAddendum()

			if fieldType != nil && !c.evaluator.AssignType(paramType, fieldType,
				assignTypeDiag, nil, AssignTypeFlagsDefault, 0) {
				diagnostic := c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.DataClassPostInitType().Format(fieldName)+
						assignTypeDiag.GetString(),
					annotationNode, nil)

				if diagnostic != nil {
					fieldDecls := symbol.GetTypedDeclarations()
					if len(fieldDecls) > 0 {
						diagnostic.AddRelatedInfo(localization.LocAddendum.DataClassFieldLocation(),
							fieldDecls[0].DeclBase().Uri, fieldDecls[0].DeclBase().Range)
					}
				}
			}
		}

		paramIndex++
	}
}
