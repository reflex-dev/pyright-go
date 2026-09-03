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
 * applyClassDecorator is here too. applyFunctionDecorator, which does the same
 * job for a `def`, is still a stub in typeevaluator_function.go.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
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
				deprecationMessage = cls.Priv.DeprecatedInstanceMessage()
			}
		}
	}

	return &FunctionDecoratorInfo{Flags: flags, DeprecationMessage: deprecationMessage}
}

// ApplyClassDecorator corresponds to applyClassDecorator. The original's
// comment: transforms the input class type into an output type based on the
// decorator function described by the decoratorNode.
func ApplyClassDecorator(
	evaluator TypeEvaluator,
	inputClassType Type,
	originalClassType *ClassType,
	decoratorNode *parser.DecoratorNode,
) Type {
	fileInfo := GetFileInfo(decoratorNode)
	flags := EvalFlagsNone
	if fileInfo.IsStubFile {
		flags = EvalFlagsForwardRefs
	}
	if decoratorNode.D.Expr.GetNodeType() != parser.ParseNodeTypeCall {
		flags |= EvalFlagsCallBaseDefaults
	}
	decoratorType := evaluator.GetTypeOfExpression(decoratorNode.D.Expr, flags, nil).Type

	// The `__dataclass_transform__` recognition runs before the main dispatch
	// and mutates the class rather than returning, so a decorator can both
	// register a transform and go on to be applied normally.
	if callNode, ok := decoratorNode.D.Expr.(*parser.CallNode); ok {
		decoratorCallType := evaluator.GetTypeOfExpression(callNode.D.LeftExpr, flags|EvalFlagsCallBaseDefaults, nil).Type

		if IsFunction(decoratorCallType) {
			fn := decoratorCallType.(*FunctionType)
			if fn.Shared.Name == "__dataclass_transform__" || FunctionTypeIsBuiltIn(fn, "dataclass_transform") {
				originalClassType.Shared.ClassDataClassTransform =
					validateDataClassTransformDecorator(evaluator, callNode)
			}
		}
	}

	// applyDataclassTransform is the original's local closure. It reports
	// whether a dataclass decorator was recognized and applied.
	applyDataclassTransform := func() bool {
		var dataclassBehaviors *DataClassBehaviors
		var callNode *parser.CallNode

		if asCall, ok := decoratorNode.D.Expr.(*parser.CallNode); ok {
			callNode = asCall
			decoratorCallType := evaluator.GetTypeOfExpression(
				callNode.D.LeftExpr, flags|EvalFlagsCallBaseDefaults, nil).Type
			dataclassBehaviors = getDataclassDecoratorBehaviors(evaluator, decoratorCallType)
		} else {
			t := evaluator.GetTypeOfExpression(decoratorNode.D.Expr, flags, nil).Type
			dataclassBehaviors = getDataclassDecoratorBehaviors(evaluator, t)
		}

		if dataclassBehaviors != nil {
			ApplyDataClassDecorator(evaluator, decoratorNode, originalClassType, dataclassBehaviors, callNode)
			return true
		}

		return false
	}

	switch {
	case IsOverloaded(decoratorType):
		if dataclassBehaviors := getDataclassDecoratorBehaviors(evaluator, decoratorType); dataclassBehaviors != nil {
			ApplyDataClassDecorator(evaluator, decoratorNode, originalClassType, dataclassBehaviors, nil)
			return inputClassType
		}

	case IsFunction(decoratorType):
		fn := decoratorType.(*FunctionType)

		if FunctionTypeIsBuiltIn(fn, "disjoint_base") {
			switch {
			case ClassTypeIsTypedDictClass(originalClassType):
				evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.DisjointBaseTypedDict(), decoratorNode.D.Expr, nil)
			case ClassTypeIsProtocolClass(originalClassType):
				evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.DisjointBaseProtocol(), decoratorNode.D.Expr, nil)
			default:
				originalClassType.Shared.Flags |= ClassTypeFlagsDisjointBase
			}

			return inputClassType
		}

		if FunctionTypeIsBuiltIn(fn, "final") {
			originalClassType.Shared.Flags |= ClassTypeFlagsFinal

			// The original's comment: don't call getTypeOfDecorator for final.
			// We'll hard-code its behavior because its function definition
			// results in a cyclical dependency between builtins, typing and
			// _typeshed stubs.
			return inputClassType
		}

		if FunctionTypeIsBuiltIn(fn, "type_check_only") {
			originalClassType.Shared.Flags |= ClassTypeFlagsTypeCheckOnly
			return inputClassType
		}

		if FunctionTypeIsBuiltIn(fn, "runtime_checkable") {
			originalClassType.Shared.Flags |= ClassTypeFlagsRuntimeCheckable

			// The original's comment: don't call getTypeOfDecorator for
			// runtime_checkable. It appears frequently in stubs, and it's a
			// waste of time to validate its parameters.
			return inputClassType
		}

		if applyDataclassTransform() {
			return inputClassType
		}

	case IsClassInstance(decoratorType):
		cls := decoratorType.(*ClassType)
		if ClassTypeIsBuiltInNamed(cls, "deprecated") {
			originalClassType.Shared.DeprecatedMessage = cls.Priv.DeprecatedInstanceMessage()
			return inputClassType
		}

		if applyDataclassTransform() {
			return inputClassType
		}
	}

	return getTypeOfDecorator(evaluator, decoratorNode, inputClassType)
}

// getTypeOfDecorator corresponds to the function of the same name: the general
// path, which calls the decorator with the decorated object as its argument.
func getTypeOfDecorator(evaluator TypeEvaluator, node *parser.DecoratorNode, functionOrClassType Type) Type {
	// The original's comment: evaluate the type of the decorator expression.
	flags := EvalFlagsNone
	if GetFileInfo(node).IsStubFile {
		flags = EvalFlagsForwardRefs
	}
	if node.D.Expr.GetNodeType() != parser.ParseNodeTypeCall {
		flags |= EvalFlagsCallBaseDefaults
	}

	decoratorTypeResult := evaluator.GetTypeOfExpression(node.D.Expr, flags, nil)

	// The original's comment: special-case the combination of a classmethod
	// decorator applied to a property. This is allowed in Python 3.9, but it's
	// not reflected in the builtins.pyi stub for classmethod.
	if IsInstantiableClass(decoratorTypeResult.Type) &&
		ClassTypeIsBuiltInNamed(decoratorTypeResult.Type.(*ClassType), "classmethod") &&
		IsProperty(functionOrClassType) {
		return functionOrClassType
	}

	argList := []*Arg{
		{
			ArgCategory: parser.ArgCategorySimple,
			TypeResult:  &TypeResult{Type: functionOrClassType},
		},
	}

	callTypeResult := evaluator.ValidateCallArgs(node.D.Expr, argList, decoratorTypeResult, nil, true, nil)
	if callTypeResult == nil {
		return UnknownTypeCreate(false)
	}

	returnType := callTypeResult.ReturnType
	if returnType == nil {
		returnType = UnknownTypeCreate(false)
	}

	evaluator.SetTypeResultForNode(node, &TypeResult{
		Type:                 returnType,
		OverloadsUsedForCall: callTypeResult.OverloadsUsedForCall,
		IsIncomplete:         callTypeResult.IsTypeIncomplete,
	}, EvalFlagsNone)

	// The original's comment: if the return type is a function that has no
	// annotations and just *args and **kwargs parameters, assume that it
	// preserves the type of the input function.
	if IsFunction(returnType) && returnType.(*FunctionType).Shared.DeclaredReturnType == nil {
		if !decoratorReturnHasTypedParams(returnType.(*FunctionType)) {
			return functionOrClassType
		}
	}

	// The original's comment: if the decorator is completely unannotated and the
	// return type includes unknowns, assume that it preserves the type of the
	// input function.
	if IsPartlyUnknown(returnType, 0) {
		if IsFunction(decoratorTypeResult.Type) {
			decoratorFn := decoratorTypeResult.Type.(*FunctionType)
			hasDeclared := false
			for _, param := range decoratorFn.Shared.Parameters {
				if FunctionParamIsTypeDeclared(param) {
					hasDeclared = true
					break
				}
			}
			if !hasDeclared && decoratorFn.Shared.DeclaredReturnType == nil {
				return functionOrClassType
			}
		}
	}

	return returnType
}

// decoratorReturnHasTypedParams is the original's `parameters.some(...)` inside
// getTypeOfDecorator, which asks whether the decorator's return type has any
// parameter that would stop it from being treated as identity-preserving.
func decoratorReturnHasTypedParams(returnType *FunctionType) bool {
	for index, param := range returnType.Shared.Parameters {
		// The original's comment: don't allow * or / separators or params with
		// declared types.
		if param.Name == nil || FunctionParamIsTypeDeclared(param) {
			return true
		}

		// The original's comment: allow *args or **kwargs parameters.
		if param.Category != parser.ParamCategorySimple {
			continue
		}

		// The original's comment: allow inferred "self" or "cls" parameters.
		if index != 0 || !FunctionParamIsTypeInferred(param) {
			return true
		}
	}

	return false
}

/*
 * The three dataClasses.ts helpers this reaches.
 */

// getDataclassDecoratorBehaviors corresponds to the dataClasses.ts function of
// the same name: the PEP 681 behaviors a decorator imparts, or nil when it is
// not a dataclass-like decorator at all.
//
// The overload search is ordered by PEP 681: the first overload carrying a
// dataclass_transform decorator wins, then the implementation, and only if
// neither has one does it fall back to the first overload -- which will then
// almost certainly answer nil, but reaches the `dataclasses.dataclass` check
// below on the way.
func getDataclassDecoratorBehaviors(evaluator TypeEvaluator, t Type) *DataClassBehaviors {
	var functionType *FunctionType

	if IsFunction(t) {
		functionType = t.(*FunctionType)
	} else if IsOverloaded(t) {
		// The original's comment: find the first overload or implementation that
		// contains a dataclass_transform decorator. If more than one have such a
		// decorator, only the first one will be honored, as per PEP 681.
		overloads := OverloadedTypeGetOverloads(t.(*OverloadedType))
		impl := OverloadedTypeGetImplementation(t.(*OverloadedType))

		for _, overload := range overloads {
			if overload.Shared.DecoratorDataClassBehaviors != nil {
				functionType = overload
				break
			}
		}

		if functionType == nil && impl != nil && IsFunction(impl) &&
			impl.(*FunctionType).Shared.DecoratorDataClassBehaviors != nil {
			functionType = impl.(*FunctionType)
		}

		if functionType == nil && len(overloads) > 0 {
			functionType = overloads[0]
		}
	}

	if functionType == nil {
		return nil
	}

	if functionType.Shared.DecoratorDataClassBehaviors != nil {
		return functionType.Shared.DecoratorDataClassBehaviors
	}

	// The original's comment: is this the built-in dataclass? If so, return the
	// default behaviors.
	if functionType.Shared.FullName == "dataclasses.dataclass" {
		return &DataClassBehaviors{
			FieldDescriptorNames: []string{"dataclasses.field", "dataclasses.Field"},
		}
	}

	return nil
}

func validateDataClassTransformDecorator(evaluator TypeEvaluator, node *parser.CallNode) *DataClassBehaviors {
	return ValidateDataClassTransformDecorator(evaluator, node)
}
