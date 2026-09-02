/*
 * checker_method.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateMethod.
 *
 * This is the `self`/`cls` naming check, and it is a cascade of early returns
 * rather than a set of independent tests: __new__, staticmethod, classmethod and
 * instance method are mutually exclusive shapes, each with its own expected
 * first parameter, and each returns as soon as it has been recognized.
 *
 * Three details are easy to get wrong.
 *
 * A metaclass is allowed to name its first parameter `mcls`/`mcs`/`metacls` in
 * addition to `cls`, and -- separately -- an instance method *of* a metaclass may
 * legitimately be named `cls`, because instances of a metaclass are classes.
 *
 * A decorator can change what the first parameter means, so the instance-method
 * check backs off entirely when one is present. @overload is a decorator but is
 * not one of those, hence the `isOverloaded || !decoratorIsPresent` guard rather
 * than a plain absence test.
 *
 * A first parameter whose name starts with an underscore is exempt: typeshed
 * uses that convention to mark a parameter that callers may not pass by name.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// selfParamNames, clsParamNames and metaclassClsParamNames are the original's
// three local arrays.
var (
	selfParamNames         = []string{"self", "_self", "__self"}
	clsParamNames          = []string{"cls", "_cls", "__cls"}
	metaclassClsParamNames = []string{"__mcls", "mcls", "mcs", "metacls"}
)

func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// validateMethod corresponds to _validateMethod.
func (c *Checker) validateMethod(
	node *parser.FunctionNode, functionType *FunctionType, classNodeParam parser.ParseNode,
) {
	classNode, ok := classNodeParam.(*parser.ClassNode)
	if !ok {
		return
	}

	classTypeInfo := c.evaluator.GetTypeOfClass(classNode)
	if classTypeInfo == nil {
		return
	}

	classType := classTypeInfo.ClassType
	methodName := node.D.Name.D.Value
	isMetaclass := IsInstantiableMetaclass(classType)

	superCheckMethods := []string{"__init__", "__init_subclass__", "__enter__", "__exit__"}
	if containsName(superCheckMethods, methodName) {
		if !FunctionTypeIsAbstractMethod(functionType) &&
			!FunctionTypeIsOverloaded(functionType) && !c.fileInfo.IsStubFile {
			c.validateSuperCallForMethod(node, functionType, classType)
		}
	}

	if methodName == "_generate_next_value_" {
		// The original's comment: skip this check for _generate_next_value_.
		return
	}

	// firstParamName is the original's repeated
	// `node.d.params[0].d.name?.d.value`, empty when there is no first parameter
	// or it is unnamed.
	firstParamName := ""
	if len(node.D.Params) > 0 && node.D.Params[0].D.Name != nil {
		firstParamName = node.D.Params[0].D.Name.D.Value
	}

	// firstParamOrName is the original's repeated
	// `node.d.params.length > 0 ? node.d.params[0] : node.d.name`.
	var firstParamOrName parser.ParseNode = node.D.Name
	if len(node.D.Params) > 0 {
		firstParamOrName = node.D.Params[0]
	}

	if methodName == "__new__" {
		// The original's comment: __new__ overrides should have a "cls" parameter.
		if len(node.D.Params) == 0 || node.D.Params[0].D.Name == nil {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportSelfClsParameterName,
				localization.LocMessage.NewClsParam(), node.D.Name, nil)
		} else if !containsName(clsParamNames, firstParamName) &&
			!(isMetaclass && containsName(metaclassClsParamNames, firstParamName)) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportSelfClsParameterName,
				localization.LocMessage.NewClsParam(), node.D.Params[0], nil)
		}

		c.validateClsSelfParamType(node, functionType, classType, true)
		return
	}

	if FunctionTypeIsStaticMethod(functionType) {
		if len(node.D.Params) == 0 || node.D.Params[0].D.Name == nil {
			return
		}

		// The original's comment: static methods should not have "self" or "cls"
		// parameters. Note that this tests the bare names only, not the
		// underscore-prefixed variants the other checks accept.
		if firstParamName == "self" || firstParamName == "cls" {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportSelfClsParameterName,
				localization.LocMessage.StaticClsSelfParam(), node.D.Params[0].D.Name, nil)
		}
		return
	}

	if FunctionTypeIsClassMethod(functionType) {
		// The original's comment: class methods should have a "cls" parameter.
		if !containsName(clsParamNames, firstParamName) &&
			!(isMetaclass && containsName(metaclassClsParamNames, firstParamName)) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportSelfClsParameterName,
				localization.LocMessage.ClassMethodClsParam(), firstParamOrName, nil)
		}

		c.validateClsSelfParamType(node, functionType, classType, true)
		return
	}

	decoratorIsPresent := len(node.D.Decorators) > 0
	isOverloaded := FunctionTypeIsOverloaded(functionType)

	// The original's comment: the presence of a decorator can change the
	// behavior, so we need to back off from this check if a decorator is present.
	// An overload is a decorator, but we'll ignore that here.
	if isOverloaded || !decoratorIsPresent {
		firstParamIsSimple := true
		if len(node.D.Params) > 0 && node.D.Params[0].D.Category != parser.ParamCategorySimple {
			firstParamIsSimple = false
		}

		// The original's comment: instance methods should have a "self"
		// parameter.
		if firstParamIsSimple && !containsName(selfParamNames, firstParamName) {
			// An instance method of a metaclass may legitimately be named `cls`,
			// because instances of a metaclass are classes.
			isLegalMetaclassName := isMetaclass && containsName(clsParamNames, firstParamName)

			// The original's comment: some typeshed stubs use a name that starts
			// with an underscore to designate a parameter that cannot be
			// positional.
			isPrivate := IsPrivateOrProtectedName(firstParamName)

			if !isLegalMetaclassName && !isPrivate {
				c.evaluator.AddDiagnostic(DiagnosticRuleReportSelfClsParameterName,
					localization.LocMessage.InstanceMethodSelfParam(), firstParamOrName, nil)
			}
		}
	}

	c.validateClsSelfParamType(node, functionType, classType, false)
}
