/*
 * checker_visits.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * visitClass, visitFunction, visitTypeAnnotation, visitTypeParameterList,
 * visitComprehension, visitWith, visitMatch, visitSlice, visitUnpack,
 * visitTuple, visitAssignmentExpression and visitIndex's evaluation half.
 *
 * These are the walk's evaluation drivers. The evaluator is lazy: a type is
 * computed only when something asks for it, and the checker's walk is what asks.
 * That has a consequence beyond diagnostics, which is why this file had to land
 * alongside checker_symboltables.go rather than after it: evaluating a name is
 * also what marks its symbol accessed, so a scope the walk never evaluates looks
 * entirely unused. Without visitClass and visitFunction driving base-class
 * lists, decorators and parameter annotations, every `from typing import Any`
 * in the corpus reads as an unused import.
 *
 * visitClass and visitFunction also override the walk order rather than
 * returning true, because the type of the class or function has to be computed
 * before its body is walked -- so both compute the type first, then walk the
 * children explicitly, then return false to stop the default recursion.
 *
 * The per-construct validators these two originally call are not all here. Each
 * is named on the frontier so the ranking shows what it costs, which is the same
 * treatment the evaluator's satellites got.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// VisitTypeAnnotation corresponds to visitTypeAnnotation.
func (c *Checker) VisitTypeAnnotation(node *parser.TypeAnnotationNode) bool {
	c.evaluator.GetType(node.D.Annotation)
	return true
}

// VisitTypeParameterList corresponds to visitTypeParameterList.
func (c *Checker) VisitTypeParameterList(node *parser.TypeParameterListNode) bool {
	c.typeParamLists = append(c.typeParamLists, node)
	return true
}

// VisitComprehension corresponds to visitComprehension.
func (c *Checker) VisitComprehension(node *parser.ComprehensionNode) bool {
	c.scopedNodes = append(c.scopedNodes, node)
	return true
}

// VisitSlice corresponds to visitSlice.
func (c *Checker) VisitSlice(node *parser.SliceNode) bool {
	c.evaluator.GetType(node)
	return true
}

// VisitUnpack corresponds to visitUnpack.
func (c *Checker) VisitUnpack(node *parser.UnpackNode) bool {
	c.evaluator.GetType(node)
	return true
}

// VisitTuple corresponds to visitTuple.
func (c *Checker) VisitTuple(node *parser.TupleNode) bool {
	c.evaluator.GetType(node)
	return true
}

// VisitAssignmentExpression corresponds to visitAssignmentExpression.
func (c *Checker) VisitAssignmentExpression(node *parser.AssignmentExpressionNode) bool {
	c.evaluator.GetType(node)
	return true
}

// VisitMatch corresponds to visitMatch.
func (c *Checker) VisitMatch(node *parser.MatchNode) bool {
	c.evaluator.GetType(node.D.Expr)
	c.validateExhaustiveMatch(node)
	return true
}

// validateExhaustiveMatch corresponds to _validateExhaustiveMatch.
//
// The subject's type after every case has been applied is what answers the
// question: if narrowing has eliminated every subtype, the match handled them
// all, and anything left over is a value no case matches.
func (c *Checker) validateExhaustiveMatch(node *parser.MatchNode) {
	// The original's comment: this check can be expensive, so skip it if it's
	// disabled.
	if c.fileInfo.DiagnosticRuleSet.ReportMatchNotExhaustive == DiagnosticLevelNone {
		return
	}

	narrowedTypeResult := c.evaluator.EvaluateTypeForSubnode(node, func() {
		c.evaluator.EvaluateTypesForMatchStatement(node)
	})

	if narrowedTypeResult == nil || IsNever(narrowedTypeResult.Type) {
		return
	}

	diagAddendum := common.NewDiagnosticAddendum()
	diagAddendum.AddMessage(localization.LocAddendum.MatchIsNotExhaustiveType().Format(
		c.evaluator.PrintType(narrowedTypeResult.Type, nil)))
	diagAddendum.AddMessage(localization.LocAddendum.MatchIsNotExhaustiveHint())

	c.evaluator.AddDiagnostic(DiagnosticRuleReportMatchNotExhaustive,
		localization.LocMessage.MatchIsNotExhaustive()+diagAddendum.GetString(),
		node.D.Expr, nil)
}

// VisitWith corresponds to visitWith.
func (c *Checker) VisitWith(node *parser.WithNode) bool {
	for _, item := range node.D.WithItems {
		c.evaluator.EvaluateTypesForStatement(item)
	}

	if node.D.TypeComment != nil {
		c.evaluator.AddDiagnosticForTextRange(c.fileInfo,
			DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.AnnotationNotSupported(),
			node.D.TypeComment.GetRange())
	}

	return true
}

// VisitClass corresponds to visitClass. It computes the class type before
// walking the body, then walks the children itself and returns false, because
// the members' evaluation depends on the class type already existing.
func (c *Checker) VisitClass(node *parser.ClassNode) bool {
	classTypeResult := c.evaluator.GetTypeOfClass(node)

	if node.D.TypeParams != nil {
		c.Walk(node.D.TypeParams)
	}
	c.Walk(node.D.Suite)
	for _, decorator := range node.D.Decorators {
		c.Walk(decorator)
	}
	for _, arg := range node.D.Arguments {
		c.Walk(arg)
	}

	if classTypeResult != nil {
		// The original's comment: protocol classes cannot derive from
		// non-protocol classes.
		if ClassTypeIsProtocolClass(classTypeResult.ClassType) {
			for _, arg := range node.D.Arguments {
				if arg.D.Name != nil {
					continue
				}

				baseClassType := c.evaluator.GetType(arg.D.ValueExpr)
				if baseClassType == nil || !IsInstantiableClass(baseClassType) {
					continue
				}
				baseClass := baseClassType.(*ClassType)
				if ClassTypeIsBuiltInNamed(baseClass, "Protocol") ||
					ClassTypeIsBuiltInNamed(baseClass, "Generic") {
					continue
				}

				if !ClassTypeIsProtocolClass(baseClass) {
					c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
						localization.LocMessage.ProtocolBaseClass().Format(
							classTypeResult.ClassType.Shared.Name, baseClass.Shared.Name),
						arg.D.ValueExpr, nil)
				}
			}

			// The original's comment: if this is a generic protocol class,
			// verify that its type variables have the proper variance.
			c.validateProtocolTypeParamVariance(node, classTypeResult.ClassType)
		}

		// The original's comment: skip the slots check because class variables
		// declared in a stub file are interpreted as instance variables.
		if !c.fileInfo.IsStubFile {
			c.validateSlotsClassVarConflict(classTypeResult.ClassType)
		}

		c.validateBaseClassOverrides(classTypeResult.ClassType)
		c.validateTypedDictOverrides(classTypeResult.ClassType)
		c.validateOverloadDecoratorConsistency(classTypeResult.ClassType)
		c.validateDisjointBaseClass(classTypeResult.ClassType, node.D.Name)
		c.validateMultipleInheritanceBaseClasses(classTypeResult.ClassType, node.D.Name)
		c.validateMultipleInheritanceCompatibility(classTypeResult.ClassType, node.D.Name)
		c.validateConstructorConsistency(classTypeResult.ClassType, node.D.Name)
		c.validateFinalMemberOverrides(classTypeResult.ClassType)
		c.validateInstanceVariableInitialization(node, classTypeResult.ClassType)
		c.validateFinalClassNotAbstract(classTypeResult.ClassType, node)
		c.validateDataClassPostInit(classTypeResult.ClassType)
		c.validateEnumMembers(classTypeResult.ClassType, node)

		if ClassTypeIsTypedDictClass(classTypeResult.ClassType) {
			c.validateTypedDictClassSuite(node.D.Suite)
		}

		if ClassTypeIsEnumClass(classTypeResult.ClassType) {
			c.validateEnumClassOverride(node, classTypeResult.ClassType)
		}

		c.evaluator.ValidateInitSubclassArgs(node, classTypeResult.ClassType)
	}

	c.scopedNodes = append(c.scopedNodes, node)

	return false
}

// VisitFunction corresponds to visitFunction. As with VisitClass, the function's
// type is computed before its body is walked, and the children are walked
// explicitly.
func (c *Checker) VisitFunction(node *parser.FunctionNode) bool {
	if node.D.TypeParams != nil {
		c.Walk(node.D.TypeParams)
	}

	if !c.fileInfo.DiagnosticRuleSet.AnalyzeUnannotatedFunctions && !c.fileInfo.IsStubFile {
		if IsUnannotatedFunction(node) {
			c.evaluator.AddInformation(
				localization.LocMessage.UnannotatedFunctionSkipped().Format(node.D.Name.D.Value),
				node.D.Name, nil)
		}
	}

	functionTypeResult := c.evaluator.GetTypeOfFunction(node)
	containingClassNode := GetEnclosingClass(node, true)

	if functionTypeResult != nil {
		c.validateFunctionParams(node, functionTypeResult, containingClassNode)
	}

	for index, param := range node.D.Params {
		if param.D.DefaultValue != nil {
			c.Walk(param.D.DefaultValue)
		}
		if param.D.Annotation != nil {
			c.Walk(param.D.Annotation)
		}
		if param.D.AnnotationComment != nil {
			c.Walk(param.D.AnnotationComment)
		}

		// The original's comment: look for method parameters that are typed with
		// TypeVars that have the wrong variance.
		if functionTypeResult == nil {
			continue
		}

		annotationNode := param.D.Annotation
		if annotationNode == nil {
			annotationNode = param.D.AnnotationComment
		}
		if annotationNode == nil || index >= len(functionTypeResult.FunctionType.Shared.Parameters) {
			continue
		}

		paramType := FunctionTypeGetParamType(functionTypeResult.FunctionType, index)
		name := functionTypeResult.FunctionType.Shared.Name
		isExemptMethod := name == "__init__" || name == "__new__"

		if containingClassNode != nil && IsTypeVar(paramType) &&
			paramType.(*TypeVarType).Priv.ScopeType != nil &&
			*paramType.(*TypeVarType).Priv.ScopeType == TypeVarScopeTypeClass &&
			paramType.(*TypeVarType).Shared.DeclaredVariance == VarianceCovariant &&
			!paramType.(*TypeVarType).Shared.IsSynthesized && !isExemptMethod {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.ParamTypeCovariant(), annotationNode, nil)
		}
	}

	if node.D.ReturnAnnotation != nil {
		c.Walk(node.D.ReturnAnnotation)
	}

	if node.D.FuncAnnotationComment != nil {
		c.Walk(node.D.FuncAnnotationComment)

		if c.fileInfo.DiagnosticRuleSet.ReportTypeCommentUsage != DiagnosticLevelNone &&
			c.fileInfo.ExecutionEnvironment.PythonVersion.IsGreaterOrEqualTo(common.PythonVersion3_5) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportTypeCommentUsage,
				localization.LocMessage.TypeCommentDeprecated(), node.D.FuncAnnotationComment, nil)
		}
	}

	for _, decorator := range node.D.Decorators {
		c.Walk(decorator)
	}

	for _, param := range node.D.Params {
		if param.D.Name != nil {
			c.Walk(param.D.Name)
		}
	}

	codeComplexity := GetCodeFlowComplexity(node)
	isTooComplexToAnalyze := codeComplexity > MaxCodeComplexity

	// The original logs the complexity here when isPrintCodeComplexityEnabled, a
	// constant that is false in the shipped source.

	if isTooComplexToAnalyze {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.CodeTooComplexToAnalyze(), node.D.Name, nil)
	} else {
		c.Walk(node.D.Suite)
	}

	if functionTypeResult != nil {
		// The original's comment: validate that the function returns the declared
		// type.
		if !isTooComplexToAnalyze {
			c.validateFunctionReturn(node, functionTypeResult.FunctionType)
		}

		c.validateDunderSignatures(node, functionTypeResult.FunctionType, containingClassNode != nil)
		c.validateTypeGuardFunction(node, functionTypeResult.FunctionType, containingClassNode != nil)
		c.validateFunctionTypeVarUsage(node, functionTypeResult)
		c.validateGeneratorReturnType(node, functionTypeResult.FunctionType)
		c.reportDeprecatedClassProperty(node, functionTypeResult)

		// The original's comment: if this is not a method, @final is disallowed.
		if containingClassNode == nil && FunctionTypeIsFinal(functionTypeResult.FunctionType) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.FinalNonMethod().Format(node.D.Name.D.Value),
				node.D.Name, nil)
		}
	}

	// The original's comment: if we're at the module level within a stub file,
	// report a diagnostic if there is a '__getattr__' function defined when in
	// strict mode. This signifies an incomplete stub file that obscures type
	// errors.
	if c.fileInfo.IsStubFile && node.D.Name.D.Value == "__getattr__" {
		if scope := GetScopeForNode(node); scope != nil && scope.Type == ScopeTypeModule {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportIncompleteStub,
				localization.LocMessage.StubUsesGetAttr(), node.D.Name, nil)
		}
	}

	c.scopedNodes = append(c.scopedNodes, node)

	c.validateFunctionOverloads(node, functionTypeResult)

	return false
}

// validateFunctionOverloads is the original's trailing overload-consistency
// block, lifted out because it is three levels of nesting deep in the original.
func (c *Checker) validateFunctionOverloads(
	node *parser.FunctionNode, functionTypeResult *FunctionTypeResult,
) {
	if functionTypeResult == nil || !IsOverloaded(functionTypeResult.DecoratedType) ||
		functionTypeResult.FunctionType.Priv.Overloaded == nil {
		return
	}

	decorated := functionTypeResult.DecoratedType.(*OverloadedType)

	// The original's comment: if this is the implementation for the overloaded
	// function, skip overload consistency checks.
	if OverloadedTypeGetImplementation(decorated) != Type(functionTypeResult.FunctionType) {
		overloads := OverloadedTypeGetOverloads(decorated)
		if len(overloads) > 1 {
			// The original's comment: the check is n^2 in time, so if the number
			// of overloads is very large (which can happen for some generated
			// code), skip this check to avoid quadratic analysis time.
			const maxOverloadConsistencyCheckLength = 100

			if len(overloads) < maxOverloadConsistencyCheckLength {
				c.validateOverloadConsistency(node, overloads[len(overloads)-1],
					overloads[:len(overloads)-1])
			}
		}
	}

	c.validateOverloadAttributeConsistency(node, decorated)
}
