/*
 * decorators.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/decorators.ts (pyright 1.1.412).
 *
 * Only getFunctionInfoFromDecorators so far, which is the part of the module
 * that answers "what kind of function is this" without transforming anything.
 * It is reached from three directions -- diagnostic suppression checking for
 * @no_type_check, function creation deciding a method's flags, and parameter
 * evaluation deciding whether the first parameter is self or cls -- which is
 * what put it on the frontier from three separate call sites at once.
 *
 * applyFunctionDecorator and applyClassDecorator, which do transform, are still
 * stubs in typeevaluator_function.go and typeevaluator_class.go; they need call
 * evaluation.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// implicitClassMethods is the original's local array: magic methods the runtime
// treats as class methods whether or not they are decorated.
var implicitClassMethods = []string{"__init_subclass__", "__class_getitem__"}

// GetFunctionInfoFromDecorators corresponds to the function of the same name.
// The original's comment: scans through the decorators to find a few built-in
// decorators that affect the function flags.
func GetFunctionInfoFromDecorators(
	evaluator TypeEvaluator,
	node *parser.FunctionNode,
	isInClass bool,
) *FunctionDecoratorInfo {
	fileInfo := GetFileInfo(node)
	flags := FunctionTypeFlagsNone
	var deprecationMessage *string

	if isInClass {
		// The original's comment: the "__new__" magic method is not an instance
		// method. It acts as a static method instead.
		if node.D.Name.D.Value == "__new__" {
			flags |= FunctionTypeFlagsConstructorMethod
		}

		// The original's comment: several magic methods are treated as class
		// methods implicitly by the runtime. Check for these here.
		for _, name := range implicitClassMethods {
			if node.D.Name.D.Value == name {
				flags |= FunctionTypeFlagsClassMethod
				break
			}
		}
	}

	for _, decoratorNode := range node.D.Decorators {
		// The original's comment: some stub files (e.g. builtins.pyi) rely on
		// forward declarations of decorators.
		evaluatorFlags := EvalFlagsNone
		if fileInfo.IsStubFile {
			evaluatorFlags = EvalFlagsForwardRefs
		}
		if decoratorNode.D.Expr.GetNodeType() != parser.ParseNodeTypeCall {
			evaluatorFlags |= EvalFlagsCallBaseDefaults
		}

		decoratorType := evaluator.GetTypeOfExpression(decoratorNode.D.Expr, evaluatorFlags, nil).Type

		if IsFunction(decoratorType) {
			fn := decoratorType.(*FunctionType)
			switch {
			case FunctionTypeIsBuiltIn(fn, "abstractmethod"):
				if isInClass {
					flags |= FunctionTypeFlagsAbstractMethod
				}
			case FunctionTypeIsBuiltIn(fn, "final"):
				flags |= FunctionTypeFlagsFinal
			case FunctionTypeIsBuiltIn(fn, "override"):
				flags |= FunctionTypeFlagsOverridden
			case FunctionTypeIsBuiltIn(fn, "type_check_only"):
				flags |= FunctionTypeFlagsTypeCheckOnly
			case FunctionTypeIsBuiltIn(fn, "no_type_check"):
				flags |= FunctionTypeFlagsNoTypeCheck
			case FunctionTypeIsBuiltIn(fn, "overload"):
				flags |= FunctionTypeFlagsOverloaded
			}
		} else if IsClass(decoratorType) {
			cls := decoratorType.(*ClassType)
			if decoratorType.Base().IsInstantiable() {
				if ClassTypeIsBuiltInNamed(cls, "staticmethod") {
					if isInClass {
						flags |= FunctionTypeFlagsStaticMethod
					}
				} else if ClassTypeIsBuiltInNamed(cls, "classmethod") {
					if isInClass {
						flags |= FunctionTypeFlagsClassMethod
					}
				}
			} else if ClassTypeIsBuiltInNamed(cls, "deprecated") {
				deprecationMessage = cls.Priv.DeprecatedInstanceMessage
			}
		}
	}

	return &FunctionDecoratorInfo{Flags: flags, DeprecationMessage: deprecationMessage}
}
