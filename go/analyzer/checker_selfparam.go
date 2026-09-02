/*
 * checker_selfparam.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateSuperCallForMethod and _validateClsSelfParamType.
 *
 * _validateSuperCallForMethod is a syntactic search, not a type check: it walks
 * the method body looking for a call to a same-named method on `super()` or on
 * an explicit class. That is deliberately loose -- an `X.__init__(self)` call
 * counts just as much as `super().__init__()` -- because the point is only to
 * catch a constructor that forgets to chain at all.
 *
 * The `@final` special case is worth reading. Skipping `object` is safe only
 * when the class is final, because a non-final class may later be combined with
 * others in a multiple-inheritance chain, and then `object` is no longer what
 * follows it in the MRO. Finality is what makes the MRO knowable statically.
 *
 * _validateClsSelfParamType checks an *annotated* `self` or `cls`, and most of
 * its body is exemptions -- protocols on either side, a `*args: P.args` first
 * parameter, an overload using the annotation as a filter, and typeshed's
 * `LiteralString` on `str`. The one check that is not an exemption is the
 * __init__ rule: a class-scoped TypeVar in a `self` annotation is forbidden by
 * the typing spec, because __init__ runs before the class's type arguments are
 * known.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateSuperCallForMethod corresponds to _validateSuperCallForMethod. The
// original's comment: determines whether the method properly calls through to
// the same method in all parent classes that expose a same-named method.
func (c *Checker) validateSuperCallForMethod(
	node *parser.FunctionNode, methodType *FunctionType, classType *ClassType,
) {
	// The original's comment: this is an expensive test, so if it's not enabled,
	// don't do any work.
	if c.fileInfo.DiagnosticRuleSet.ReportMissingSuperCall == DiagnosticLevelNone {
		return
	}

	// The original's comment: if the class is marked final, we can skip the
	// "object" base class because we know that the `__init__` method in `object`
	// doesn't do anything. It's not safe to do this if the class isn't final
	// because it could be combined with other classes in a multi-inheritance
	// situation that effectively adds new superclasses that we don't know about
	// statically.
	effectiveFlags := MemberAccessFlagsSkipInstanceMembers | MemberAccessFlagsSkipOriginalClass
	if ClassTypeIsFinal(classType) {
		effectiveFlags |= MemberAccessFlagsSkipObjectBaseClass
	}

	if LookUpClassMember(classType, methodType.Shared.Name, effectiveFlags, nil) == nil {
		return
	}

	foundCallOfMember := false

	// The original's comment: now scan the implementation of the method to
	// determine whether super().<method> has been called for all of the required
	// base classes.
	callNodeWalker := NewCallNodeWalker(func(callNode *parser.CallNode) {
		memberAccess, ok := callNode.D.LeftExpr.(*parser.MemberAccessNode)
		if !ok {
			return
		}

		// The original's comment: is it accessing the method by the same name?
		if memberAccess.D.Member.D.Value != methodType.Shared.Name {
			return
		}

		memberBaseExpr := memberAccess.D.LeftExpr

		// The original's comment: is it a "super" call?
		if baseCall, ok := memberBaseExpr.(*parser.CallNode); ok {
			if nameNode, ok := baseCall.D.LeftExpr.(*parser.NameNode); ok &&
				nameNode.D.Value == "super" {
				foundCallOfMember = true
				return
			}
		}

		// The original's comment: is it an X.<method> direct call?
		baseType := c.evaluator.GetType(memberBaseExpr)
		if baseType != nil && IsInstantiableClass(baseType) {
			foundCallOfMember = true
		}
	})
	callNodeWalker.Walk(node.D.Suite)

	// The original's comment: if we didn't find a call to at least one base
	// class, report the problem.
	if !foundCallOfMember {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportMissingSuperCall,
			localization.LocMessage.MissingSuperCall().Format(methodType.Shared.Name),
			node.D.Name, nil)
	}
}

// validateClsSelfParamType corresponds to _validateClsSelfParamType. The
// original's comment: validates that the annotated type of a "self" or "cls"
// parameter is compatible with the type of the class that contains it.
func (c *Checker) validateClsSelfParamType(
	node *parser.FunctionNode, functionType *FunctionType, classType *ClassType, isCls bool,
) {
	if len(node.D.Params) < 1 || len(functionType.Shared.Parameters) < 1 {
		return
	}

	// The original's comment: if there is no type annotation, there's nothing to
	// check because the type will be inferred.
	paramInfo := functionType.Shared.Parameters[0]
	paramType := FunctionTypeGetParamType(functionType, 0)

	paramAnnotation := node.D.Params[0].D.Annotation
	if paramAnnotation == nil {
		paramAnnotation = node.D.Params[0].D.AnnotationComment
	}
	if paramAnnotation == nil || paramInfo.Name == nil {
		return
	}

	// The original's comment: if this is an __init__ method, we need to
	// specifically check for the use of class-scoped TypeVars, which are not
	// allowed in this context according to the typing spec.
	if functionType.Shared.Name == "__init__" && functionType.Shared.MethodClass != nil {
		for _, typeVar := range GetTypeVarArgsRecursive(paramType, 0) {
			if typeVar.Priv.ScopeID == functionType.Shared.MethodClass.Shared.TypeVarScopeID &&
				!TypeVarTypeIsSelf(typeVar) {
				c.evaluator.AddDiagnostic(DiagnosticRuleReportInvalidTypeVarUse,
					localization.LocMessage.InitMethodSelfParamTypeVar(), paramAnnotation, nil)
				break
			}
		}
	}

	// The original's comment: if this is a protocol class, the self and cls
	// parameters can be bound to something other than the class.
	if ClassTypeIsProtocolClass(classType) {
		return
	}

	concreteParamType := c.evaluator.MakeTopLevelTypeVarsConcrete(paramType, false)

	var expectedType Type = classType
	if !isCls {
		expectedType = ConvertToInstance(classType, false)
	}

	// The original's comment: if the declared type is a protocol class or
	// instance, skip the check. This has legitimate uses for mix-in classes.
	if IsInstantiableClass(concreteParamType) &&
		ClassTypeIsProtocolClass(concreteParamType.(*ClassType)) {
		return
	}
	if IsClassInstance(concreteParamType) &&
		ClassTypeIsProtocolClass(concreteParamType.(*ClassType)) {
		return
	}

	// The original's comment: if the method starts with a `*args: P.args`, skip
	// the check.
	if paramInfo.Category == parser.ParamCategoryArgsList && IsParamSpec(paramType) &&
		paramType.(*TypeVarType).Priv.ParamSpecAccess == ParamSpecAccessArgs {
		return
	}

	// The original's comment: don't enforce this for an overloaded method because
	// the "self" param annotation can be used as a filter for the overload. This
	// differs from mypy, which enforces this check for overloads, but there are
	// legitimate uses for this in an overloaded method.
	if FunctionTypeIsOverloaded(functionType) {
		return
	}

	// The original's comment: if the declared type is LiteralString and the class
	// is str, exempt this case. It's used in the typeshed stubs.
	if IsClassInstance(paramType) &&
		ClassTypeIsBuiltInNamed(paramType.(*ClassType), "LiteralString") &&
		ClassTypeIsBuiltInNamed(classType, "str") {
		return
	}

	if c.evaluator.AssignType(paramType, expectedType, nil, nil, AssignTypeFlagsDefault, 0) {
		return
	}

	// The original's comment: we exempt Never from this check because it has a
	// legitimate use in this case.
	if IsNever(paramType) {
		return
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.ClsSelfParamTypeMismatch().Format(
			*paramInfo.Name, c.evaluator.PrintType(expectedType, nil)),
		paramAnnotation, nil)
}
