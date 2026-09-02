/*
 * checker_return.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateFunctionReturn, _validateReturnTypeIsNotContravariant,
 * _reportUnknownReturnResult and _validateFinalMemberOverrides.
 *
 * Every `return x` statement was already checked against the declared return
 * type as it was evaluated. What is left for this pass is the return nobody
 * wrote: the implicit `return None` that falls off the end of the suite. That is
 * why the whole check hangs on isAfterNodeReachable(node.d.suite) -- if control
 * can reach the end of the body, the function can return None, and the declared
 * type has to admit it.
 *
 * The exemptions are as interesting as the check. A body that is nothing but
 * `...` is taken to be an abstract method or a protocol stub and is not required
 * to return anything, and so is an @overload signature, whose body is never the
 * real implementation. Both are recognized structurally rather than by
 * decorator, which is why isSuiteEmpty exists.
 *
 * _validateReturnTypeIsNotContravariant catches a class-scoped contravariant
 * TypeVar in a return position, which is unsound for the same reason a covariant
 * one is unsound in a parameter position.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateFunctionReturn corresponds to _validateFunctionReturn.
func (c *Checker) validateFunctionReturn(node *parser.FunctionNode, functionType *FunctionType) {
	// The original's comment: stub files are allowed not to return an actual
	// value, so skip this if it's a stub file.
	if c.fileInfo.IsStubFile {
		return
	}

	returnAnnotation := node.D.ReturnAnnotation
	if returnAnnotation == nil && node.D.FuncAnnotationComment != nil {
		returnAnnotation = node.D.FuncAnnotationComment.D.ReturnAnnotation
	}

	if returnAnnotation == nil {
		inferredReturnType := c.evaluator.GetInferredReturnType(functionType, nil)
		c.reportUnknownReturnResult(node, inferredReturnType)
		c.validateReturnTypeIsNotContravariant(inferredReturnType, node.D.Name)
		return
	}

	functionNeverReturns := !c.evaluator.IsAfterNodeReachable(node)
	implicitlyReturnsNone := c.evaluator.IsAfterNodeReachable(node.D.Suite)

	declaredReturnType := functionType.Shared.DeclaredReturnType

	if declaredReturnType != nil {
		c.reportUnknownReturnResult(node, declaredReturnType)
		c.validateReturnTypeIsNotContravariant(declaredReturnType, returnAnnotation)

		liveScopes := GetTypeVarScopesForNode(node)
		declaredReturnType = MakeTypeVarsBound(declaredReturnType, liveScopes, false)
	}

	// The original's comment: wrap the declared type in a generator type if the
	// function is a generator.
	if FunctionTypeIsGenerator(functionType) {
		declaredReturnType = GetDeclaredGeneratorReturnType(functionType)
	}

	// The original's comment: the types of all return statement expressions were
	// already checked against the declared type, but we need to verify the
	// implicit None at the end of the function.
	if declaredReturnType == nil || functionNeverReturns || !implicitlyReturnsNone {
		return
	}

	if IsNever(declaredReturnType) {
		// The original's comment: if the function consists entirely of "...",
		// assume that it's an abstract method or a protocol method and don't
		// require that the return type matches. This check can also be skipped
		// for an overload.
		if !IsSuiteEmpty(node.D.Suite) && !FunctionTypeIsOverloaded(functionType) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportReturnType,
				localization.LocMessage.NoReturnReturnsNone(), returnAnnotation, nil)
		}
		return
	}

	if FunctionTypeIsAbstractMethod(functionType) {
		return
	}

	isEmptySuite := IsSuiteEmpty(node.D.Suite) || FunctionTypeIsOverloaded(functionType)

	// The original's comment: make sure that the function doesn't implicitly
	// return None if the declared type doesn't allow it. Skip this check for
	// abstract methods.
	var diagAddendum *common.DiagnosticAddendum
	if !isEmptySuite {
		diagAddendum = common.NewDiagnosticAddendum()
	}

	// The original's comment: if the declared type isn't compatible with 'None',
	// flag an error.
	if c.evaluator.AssignType(declaredReturnType, c.evaluator.GetNoneType(),
		diagAddendum, nil, AssignTypeFlagsDefault, 0) {
		return
	}

	if isEmptySuite {
		return
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportReturnType,
		localization.LocMessage.ReturnMissing().Format(
			c.evaluator.PrintType(declaredReturnType, nil))+diagAddendum.GetString(),
		returnAnnotation, nil)
}

// validateReturnTypeIsNotContravariant corresponds to
// _validateReturnTypeIsNotContravariant.
func (c *Checker) validateReturnTypeIsNotContravariant(returnType Type, errorNode parser.ExpressionNode) {
	isContraTypeVar := false

	DoForEachSubtype(returnType, func(subtype Type, _ int, _ []Type) {
		if !IsTypeVar(subtype) {
			return
		}
		tv := subtype.(*TypeVarType)
		if tv.Shared.DeclaredVariance == VarianceContravariant &&
			tv.Priv.ScopeType != nil && *tv.Priv.ScopeType == TypeVarScopeTypeClass {
			isContraTypeVar = true
		}
	})

	if isContraTypeVar {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.ReturnTypeContravariant(), errorNode, nil)
	}
}

// reportUnknownReturnResult corresponds to _reportUnknownReturnResult.
func (c *Checker) reportUnknownReturnResult(node *parser.FunctionNode, returnType Type) {
	if IsUnknown(returnType) {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportUnknownParameterType,
			localization.LocMessage.ReturnTypeUnknown(), node.D.Name, nil)
		return
	}

	if IsPartlyUnknown(returnType, 0) {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportUnknownParameterType,
			localization.LocMessage.ReturnTypePartiallyUnknown().Format(
				c.evaluator.PrintType(returnType, &PrintTypeOptions{ExpandTypeAlias: true})),
			node.D.Name, nil)
	}
}

// validateFinalMemberOverrides corresponds to _validateFinalMemberOverrides.
// The original's comment: validates that any overridden member variables are not
// marked as Final in parent classes.
func (c *Checker) validateFinalMemberOverrides(classType *ClassType) {
	symbolTable := ClassTypeGetSymbolTable(classType)

	for _, name := range symbolTable.Keys() {
		localSymbol, _ := symbolTable.Get(name)

		parentSymbol := LookUpClassMember(classType, name, MemberAccessFlagsSkipOriginalClass, nil)
		if parentSymbol == nil || !IsInstantiableClass(parentSymbol.ClassType) || IsPrivateName(name) {
			continue
		}

		// The original's comment: did the parent class explicitly declare the
		// variable as final?
		if !c.evaluator.IsFinalVariable(parentSymbol.Symbol) {
			continue
		}

		decls := localSymbol.GetDeclarations()
		if len(decls) == 0 {
			// The original indexes [0] unconditionally; a symbol in a class
			// symbol table always has at least one declaration, but Go would
			// panic rather than pass undefined to addDiagnostic.
			continue
		}

		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.FinalRedeclarationBySubclass().Format(
				name, parentSymbol.ClassType.(*ClassType).Shared.Name),
			decls[0].DeclBase().Node, nil)
	}
}

// validateTypeGuardFunction corresponds to _validateTypeGuardFunction.
//
// TypeGuard and TypeIs differ in exactly one way that matters here. TypeGuard is
// a one-way narrowing assertion: the narrowed type need bear no relation to the
// parameter's type. TypeIs asserts a genuine subtype relationship, which the
// runtime `isinstance`-like check must be able to establish, so its narrowed
// type has to be assignable to the first parameter's type. That is the only
// extra check the isTypeIs branch performs.
func (c *Checker) validateTypeGuardFunction(
	node *parser.FunctionNode, functionType *FunctionType, isMethod bool,
) {
	returnType := functionType.Shared.DeclaredReturnType
	if returnType == nil {
		return
	}

	if !IsClassInstance(returnType) || len(returnType.(*ClassType).Priv.TypeArgs) < 1 {
		return
	}

	returnClass := returnType.(*ClassType)
	isTypeGuard := ClassTypeIsBuiltInNamed(returnClass, "TypeGuard")
	isTypeIs := ClassTypeIsBuiltInNamed(returnClass, "TypeIs")

	if !isTypeGuard && !isTypeIs {
		return
	}

	// The original's comment: make sure there's at least one input parameter
	// provided.
	paramCount := len(functionType.Shared.Parameters)
	if isMethod {
		if FunctionTypeIsInstanceMethod(functionType) ||
			FunctionTypeIsConstructorMethod(functionType) ||
			FunctionTypeIsClassMethod(functionType) {
			paramCount--
		}
	}

	if paramCount < 1 {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeGuardParamCount(), node.D.Name, nil)
	}

	if !isTypeIs {
		return
	}

	scopeIds := GetTypeVarScopeIds(functionType)
	narrowedType := returnClass.Priv.TypeArgs[0]
	typeGuardType := MakeTypeVarsBound(narrowedType, scopeIds, false)
	typeGuardType = CloneWithTypeForm(typeGuardType, typeGuardType)

	// The original's comment: determine the type of the first parameter.
	paramIndex := 0
	if isMethod && !FunctionTypeIsStaticMethod(functionType) {
		paramIndex = 1
	}
	if paramIndex >= len(functionType.Shared.Parameters) {
		return
	}

	paramType := MakeTypeVarsBound(
		FunctionTypeGetParamType(functionType, paramIndex), scopeIds, false)

	// The original's comment: verify that the typeGuardType is a narrower type
	// than the paramType.
	if c.evaluator.AssignType(paramType, typeGuardType, nil, nil, AssignTypeFlagsDefault, 0) {
		return
	}

	returnAnnotation := node.D.ReturnAnnotation
	if returnAnnotation == nil && node.D.FuncAnnotationComment != nil {
		returnAnnotation = node.D.FuncAnnotationComment.D.ReturnAnnotation
	}
	if returnAnnotation == nil {
		return
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.TypeIsReturnType().Format(
			c.evaluator.PrintType(paramType, nil),
			c.evaluator.PrintType(narrowedType, nil)),
		returnAnnotation, nil)
}

// validateDunderSignatures corresponds to _validateDunderSignatures.
func (c *Checker) validateDunderSignatures(
	node *parser.FunctionNode, functionType *FunctionType, isMethod bool,
) {
	// The original's comment: is this an '__init__' method? Verify that it
	// returns None.
	if !isMethod || functionType.Shared.Name != "__init__" {
		return
	}

	returnAnnotation := node.D.ReturnAnnotation
	if returnAnnotation == nil && node.D.FuncAnnotationComment != nil {
		returnAnnotation = node.D.FuncAnnotationComment.D.ReturnAnnotation
	}
	declaredReturnType := functionType.Shared.DeclaredReturnType

	if returnAnnotation != nil && declaredReturnType != nil {
		if !IsNoneInstance(declaredReturnType) && !IsNever(declaredReturnType) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.InitMustReturnNone(), returnAnnotation, nil)
		}
		return
	}

	inferredReturnType := c.evaluator.GetInferredReturnType(functionType, nil)
	if !IsNever(inferredReturnType) && !IsNoneInstance(inferredReturnType) &&
		!IsAnyOrUnknown(inferredReturnType) {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.InitMustReturnNone(), node.D.Name, nil)
	}
}
