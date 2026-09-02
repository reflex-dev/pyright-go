/*
 * decorators_function.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/decorators.ts (pyright 1.1.412):
 * applyFunctionDecorator.
 *
 * Applying one decorator to a function. The general rule is simple -- call the
 * decorator with the function as its argument and take the result -- and most of
 * this file is the decorators that do not follow it.
 *
 * Four kinds break the rule, for four different reasons:
 *
 *   - @overload, @abstractmethod, @type_check_only and @dataclass_transform set
 *     a FLAG on the function and return it unchanged. Their runtime effect is
 *     irrelevant to the type; what matters is the marking. @overload writes to
 *     both the input and the undecorated type, because the overload chain is
 *     assembled later from the undecorated ones.
 *   - @classmethod and @staticmethod rewrite the function's binding flags. The
 *     three binding flags are mutually exclusive, so the other two are cleared
 *     rather than the new one merely being added -- a function already wrapped
 *     by another decorator may carry the wrong one.
 *   - @property and its subclasses build a property object, and `@x.setter` and
 *     `@x.deleter` clone an existing one. Those are recognized by the SHAPE of
 *     the decorator expression -- a member access whose base is a property --
 *     rather than by name.
 *   - @disjoint_base is rejected outright on a function.
 *
 * The PartiallyEvaluated flag is stripped from the argument before the call, so
 * a class still under construction does not leak that state into the decorated
 * result.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// ApplyFunctionDecorator corresponds to applyFunctionDecorator.
func ApplyFunctionDecorator(
	evaluator TypeEvaluator,
	inputFunctionType Type,
	undecoratedType *FunctionType,
	decoratorNode *parser.DecoratorNode,
	functionNode *parser.FunctionNode,
) Type {
	fileInfo := GetFileInfo(decoratorNode)

	// The original's comment: some stub files (e.g. builtins.pyi) rely on forward
	// declarations of decorators.
	evaluatorFlags := EvalFlagsNone
	if fileInfo.IsStubFile {
		evaluatorFlags = EvalFlagsForwardRefs
	}
	if decoratorNode.D.Expr.GetNodeType() != parser.ParseNodeTypeCall {
		evaluatorFlags |= EvalFlagsCallBaseDefaults
	}

	decoratorType := evaluator.GetTypeOfExpression(decoratorNode.D.Expr, evaluatorFlags, nil).Type

	if fn, ok := decoratorType.(*FunctionType); ok && FunctionTypeIsBuiltIn(fn, "disjoint_base") {
		evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.DisjointBaseFunction(), decoratorNode.D.Expr, nil)
		return inputFunctionType
	}

	// The original's comment: special-case the "overload" because it has no
	// definition. Older versions of typeshed defined "overload" as an object, but
	// newer versions define it as a function.
	if isOverloadDecorator(decoratorType) {
		if inputFn, ok := inputFunctionType.(*FunctionType); ok {
			inputFn.Shared.Flags |= FunctionTypeFlagsOverloaded
			undecoratedType.Shared.Flags |= FunctionTypeFlagsOverloaded
			return inputFunctionType
		}
	}

	if callNode, ok := decoratorNode.D.Expr.(*parser.CallNode); ok {
		decoratorCallType := evaluator.GetTypeOfExpression(
			callNode.D.LeftExpr, evaluatorFlags|EvalFlagsCallBaseDefaults, nil).Type

		if fn, ok := decoratorCallType.(*FunctionType); ok {
			if fn.Shared.Name == "__dataclass_transform__" ||
				FunctionTypeIsBuiltIn(fn, "dataclass_transform") {
				undecoratedType.Shared.DecoratorDataClassBehaviors =
					validateDataClassTransformDecorator(evaluator, callNode)
				return inputFunctionType
			}
		}
	}

	// The original's comment: clear the PartiallyEvaluated flag in the input if
	// it's set so it doesn't propagate to the decorated type.
	decoratorArg := inputFunctionType
	if inputFn, ok := inputFunctionType.(*FunctionType); ok && FunctionTypeIsPartiallyEvaluated(inputFn) {
		decoratorArg = FunctionTypeCloneWithNewFlags(inputFn,
			inputFn.Shared.Flags&^FunctionTypeFlagsPartiallyEvaluated)
	}

	returnType := getTypeOfDecorator(evaluator, decoratorNode, decoratorArg)

	// The original's comment: check for some built-in decorator types with known
	// semantics.
	if fn, ok := decoratorType.(*FunctionType); ok {
		if result, handled := applyFunctionDecoratorFromFunction(
			evaluator, fn, inputFunctionType, undecoratedType, decoratorNode,
			functionNode, evaluatorFlags); handled {
			return result
		}
	} else if IsInstantiableClass(decoratorType) {
		if result, handled := applyFunctionDecoratorFromClass(
			evaluator, decoratorType.(*ClassType), inputFunctionType, decoratorNode); handled {
			return result
		}
	}

	if inputFn, ok := inputFunctionType.(*FunctionType); ok {
		if returnFn, ok := returnType.(*FunctionType); ok {
			returnFn = FunctionTypeClone(returnFn, false, nil)

			// The original's comment: copy the overload flag from the input function
			// type.
			if FunctionTypeIsOverloaded(inputFn) {
				returnFn.Shared.Flags |= FunctionTypeFlagsOverloaded
			}

			// The original's comment: copy the docstrings from the input function
			// type if the decorator didn't have its own docstring.
			if returnFn.Shared.DocString == nil {
				returnFn.Shared.DocString = inputFn.Shared.DocString
			}

			returnType = returnFn
		}
	}

	return returnType
}

// isOverloadDecorator is the original's two-shape test for @overload, which
// typeshed has defined both as a class and as a function over time.
func isOverloadDecorator(decoratorType Type) bool {
	if IsInstantiableClass(decoratorType) &&
		ClassTypeIsSpecialBuiltInNamed(decoratorType.(*ClassType), "overload") {
		return true
	}
	fn, ok := decoratorType.(*FunctionType)
	return ok && FunctionTypeIsBuiltIn(fn, "overload")
}

// applyFunctionDecoratorFromFunction is the `isFunction(decoratorType)` arm.
func applyFunctionDecoratorFromFunction(
	evaluator TypeEvaluator,
	decoratorType *FunctionType,
	inputFunctionType Type,
	undecoratedType *FunctionType,
	decoratorNode *parser.DecoratorNode,
	functionNode *parser.FunctionNode,
	evaluatorFlags EvalFlags,
) (Type, bool) {
	if FunctionTypeIsBuiltIn(decoratorType, "abstractmethod") {
		// The abstract marking is applied by getFunctionInfoFromDecorators, which
		// runs before this; here the function passes through unchanged.
		return inputFunctionType, true
	}

	if FunctionTypeIsBuiltIn(decoratorType, "type_check_only") {
		undecoratedType.Shared.Flags |= FunctionTypeFlagsTypeCheckOnly
		return inputFunctionType, true
	}

	// The original's comment: handle property setters and deleters.
	memberAccess, ok := decoratorNode.D.Expr.(*parser.MemberAccessNode)
	if !ok {
		return nil, false
	}

	baseType := evaluator.GetTypeOfExpression(
		memberAccess.D.LeftExpr, evaluatorFlags|EvalFlagsMemberAccessBaseDefaults, nil).Type
	if !IsProperty(baseType) {
		return nil, false
	}

	memberName := memberAccess.D.Member.D.Value
	if memberName != "setter" && memberName != "deleter" {
		return nil, false
	}

	inputFn, isFunc := inputFunctionType.(*FunctionType)
	if !isFunc {
		return inputFunctionType, true
	}

	ValidatePropertyMethod(evaluator, inputFn, decoratorNode)

	if memberName == "setter" {
		return ClonePropertyWithSetter(evaluator, baseType, inputFn, functionNode), true
	}
	return ClonePropertyWithDeleter(evaluator, baseType, inputFn, functionNode), true
}

// applyFunctionDecoratorFromClass is the `isInstantiableClass(decoratorType)` arm.
func applyFunctionDecoratorFromClass(
	evaluator TypeEvaluator,
	decoratorType *ClassType,
	inputFunctionType Type,
	decoratorNode *parser.DecoratorNode,
) (Type, bool) {
	if ClassTypeIsBuiltIn(decoratorType) {
		switch decoratorType.Shared.Name {
		case "classmethod", "staticmethod":
			requiredFlag := FunctionTypeFlagsStaticMethod
			if decoratorType.Shared.Name == "classmethod" {
				requiredFlag = FunctionTypeFlagsClassMethod
			}

			// The original's comment: if the function isn't currently a class method
			// or static method (which can happen if the function was wrapped in a
			// decorator), add the appropriate flag.
			if inputFn, ok := inputFunctionType.(*FunctionType); ok &&
				(inputFn.Shared.Flags&requiredFlag) == 0 {
				newFunction := FunctionTypeClone(inputFn, false, nil)

				// The three binding flags are mutually exclusive, so the other two
				// are cleared rather than the new one merely being added.
				newFunction.Shared.Flags &^= FunctionTypeFlagsConstructorMethod |
					FunctionTypeFlagsStaticMethod | FunctionTypeFlagsClassMethod
				newFunction.Shared.Flags |= requiredFlag
				return newFunction, true
			}

			return inputFunctionType, true

		case "decorator":
			return inputFunctionType, true
		}
	}

	// The original's comment: handle properties and subclasses of properties
	// specially.
	if !ClassTypeIsPropertyClass(decoratorType) {
		return nil, false
	}

	if inputFn, ok := inputFunctionType.(*FunctionType); ok {
		ValidatePropertyMethod(evaluator, inputFn, decoratorNode)
		return CreateProperty(evaluator, decoratorNode, decoratorType, inputFn), true
	}

	if IsClassInstance(inputFunctionType) {
		// A callable object decorated with @property: its __call__ becomes the
		// property's getter.
		boundMethod := evaluator.GetBoundMagicMethod(
			inputFunctionType.(*ClassType), "__call__", nil, nil, nil, 0)

		if boundFn, ok := boundMethod.(*FunctionType); ok {
			return CreateProperty(evaluator, decoratorNode, decoratorType, boundFn), true
		}

		return UnknownTypeCreate(false), true
	}

	return nil, false
}

/*
 * The four properties.ts entries this reaches.
 */

// ValidatePropertyMethod corresponds to the properties.ts function of the same
// name, which checks a property method's parameter count and self annotation.
func ValidatePropertyMethod(evaluator TypeEvaluator, _ *FunctionType, _ *parser.DecoratorNode) {
	noteEvaluatorUnported(evaluator, "properties.validatePropertyMethod")
}

// CreateProperty corresponds to the properties.ts function of the same name,
// which synthesizes the class that a property object's type is.
func CreateProperty(
	evaluator TypeEvaluator, _ *parser.DecoratorNode, _ *ClassType, fget *FunctionType,
) Type {
	noteEvaluatorUnported(evaluator, "properties.createProperty")
	return fget
}

// ClonePropertyWithSetter corresponds to the properties.ts function of the same
// name.
func ClonePropertyWithSetter(
	evaluator TypeEvaluator, prop Type, _ *FunctionType, _ *parser.FunctionNode,
) Type {
	noteEvaluatorUnported(evaluator, "properties.clonePropertyWithSetter")
	return prop
}

// ClonePropertyWithDeleter corresponds to the properties.ts function of the same
// name.
func ClonePropertyWithDeleter(
	evaluator TypeEvaluator, prop Type, _ *FunctionType, _ *parser.FunctionNode,
) Type {
	noteEvaluatorUnported(evaluator, "properties.clonePropertyWithDeleter")
	return prop
}
