/*
 * typeevaluator_validateconstruct.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * validateCallForInstantiableClass; and from analyzer/decorators.ts:
 * getDeprecatedMessageFromCall.
 *
 * `Foo()` -- construction, and the largest single arm of call validation.
 *
 * Most of its length is not construction at all. Roughly a dozen builtins are
 * classes at runtime but type-system constructs when called: `TypeVar("T")`
 * produces a TypeVar rather than an instance of the TypeVar class, `NamedTuple(...)`
 * produces a new class, `NewType(...)` produces a distinct type. Each is
 * intercepted by name before the ordinary constructor path, and none of them
 * reaches __init__.
 *
 * Three of those interceptions are worth naming:
 *
 *   - Calling a metaclass. `type(x)` with one argument returns x's CLASS, so the
 *     return type is the argument's type made instantiable, per subtype. With
 *     three arguments it builds a class. An explicitly specialized metaclass is
 *     rejected outright.
 *   - `NamedTuple(...)` builds its class through createNamedTupleType, but still
 *     validates the arguments against the synthesized __init__ afterwards, so
 *     that a bad field list is reported.
 *   - Instantiating `type` itself, or a metaclass deriving from it, produces an
 *     instance that IS a class. The block near the end detects that by looking
 *     for `type` in the result's MRO and rebuilds it as an actual ClassType,
 *     naming it from the first argument when there are three.
 *
 * The abstract and protocol checks report but do not stop: an abstract class
 * that cannot be instantiated still has a well-defined instance type, and
 * returning Unknown instead would turn one error into many.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateCallForInstantiableClass corresponds to the function of the same name.
func (e *typeEvaluator) validateCallForInstantiableClass(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	expandedCallType *ClassType,
	unexpandedCallType Type,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
) *CallResult {
	if expandedCallType.Priv.LiteralValue != nil {
		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.LiteralNotCallable(), errorNode, nil)
		return &CallResult{ReturnType: UnknownTypeCreate(false), ArgumentErrors: true}
	}

	if ClassTypeIsBuiltIn(expandedCallType) {
		if result, handled := e.validateCallForBuiltInClass(
			errorNode, argList, expandedCallType, skipUnknownArgCheck, inferenceContext); handled {
			return result
		}
	}

	// The original's comment: is it a call to an Enum class factory?
	if expandedCallType.Shared.EffectiveMetaclass != nil {
		if metaclass, ok := expandedCallType.Shared.EffectiveMetaclass.(*ClassType); ok &&
			IsEnumMetaclass(metaclass) && !IsEnumClassWithMembers(e, expandedCallType) {
			var returnType Type
			if enumType := CreateEnumType(e, errorNode, expandedCallType, argList); enumType != nil {
				returnType = enumType
			} else {
				returnType = ConvertToInstance(unexpandedCallType, false)
			}
			return &CallResult{ReturnType: returnType}
		}
	}

	e.reportAbstractInstantiation(errorNode, expandedCallType, unexpandedCallType)

	if ClassTypeIsProtocolClass(expandedCallType) && !expandedCallType.Priv.IncludeSubclasses {
		// The original's comment: if the class is a protocol, it can't be
		// instantiated.
		e.AddDiagnostic(DiagnosticRuleReportAbstractUsage,
			localization.LocMessage.InstantiateProtocol().Format(expandedCallType.Shared.Name),
			errorNode, nil)
	}

	// The original's comment: assume this is a call to the constructor.
	constructorResult := ValidateConstructorArgs(
		e, errorNode, argList, expandedCallType, skipUnknownArgCheck, inferenceContext)

	returnType := constructorResult.ReturnType

	// The original's comment: if the expandedCallType originated from a TypeVar,
	// convert the constructed type back to the TypeVar. For example, if we have
	// `cls: Type[_T]` followed by `_T()`.
	if IsTypeVar(unexpandedCallType) {
		returnType = ConvertToInstance(unexpandedCallType, false)
	}

	// The original's comment: if we instantiated the "deprecated" class, attach
	// the deprecation message to the instance.
	if callNode, ok := errorNode.(*parser.CallNode); ok && returnType != nil &&
		IsClassInstance(returnType) && ClassTypeIsBuiltInNamed(returnType.(*ClassType), "deprecated") {
		deprecationMessage := GetDeprecatedMessageFromCall(callNode)
		returnType = ClassTypeCloneForDeprecatedInstance(returnType.(*ClassType), &deprecationMessage)
	}

	// The original's comment: if we instantiated a type, transform it into a
	// class. This can happen if someone directly instantiates a metaclass
	// deriving from type.
	if rebuilt := e.rebuildInstantiatedTypeAsClass(errorNode, argList, expandedCallType, returnType); rebuilt != nil {
		returnType = rebuilt
	}

	return &CallResult{
		ReturnType:           returnType,
		OverloadsUsedForCall: constructorResult.OverloadsUsedForCall,
		ArgumentErrors:       constructorResult.ArgumentErrors,
		IsTypeIncomplete:     constructorResult.IsTypeIncomplete,
	}
}

// validateCallForBuiltInClass is the original's `if (ClassType.isBuiltIn(...))`
// block: the dozen builtins that are classes at runtime but type-system
// constructs when called. The bool reports whether it handled the call.
func (e *typeEvaluator) validateCallForBuiltInClass(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	expandedCallType *ClassType,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
) (*CallResult, bool) {
	className := expandedCallType.Shared.Name
	if expandedCallType.Priv.AliasName != nil {
		className = *expandedCallType.Priv.AliasName
	}

	// The original's comment: handle a call to a metaclass explicitly.
	if IsInstantiableMetaclass(expandedCallType) {
		return e.validateCallForMetaclass(
			errorNode, argList, expandedCallType, skipUnknownArgCheck, inferenceContext), true
	}

	switch className {
	case "TypeVar":
		return &CallResult{ReturnType: e.createTypeVarType(errorNode, expandedCallType, argList)}, true

	case "TypeVarTuple":
		return &CallResult{ReturnType: e.createTypeVarTupleType(errorNode, expandedCallType, argList)}, true

	case "ParamSpec":
		return &CallResult{ReturnType: e.createParamSpecType(errorNode, expandedCallType, argList)}, true

	case "TypeAliasType":
		if newTypeAlias := e.createTypeAliasType(errorNode, argList); newTypeAlias != nil {
			return &CallResult{ReturnType: newTypeAlias}, true
		}

	case "NamedTuple":
		result := &CallResult{ReturnType: CreateNamedTupleType(e, errorNode, argList, true)}

		// The class is built above; the arguments are still validated against the
		// synthesized __init__ so a bad field list is reported.
		initTypeResult := GetBoundInitMethod(e, errorNode,
			ClassTypeCloneAsInstance(expandedCallType, false), nil, MemberAccessFlagsDefault)

		if initTypeResult != nil {
			if overloaded, ok := initTypeResult.Type.(*OverloadedType); ok {
				e.validateOverloadedArgTypes(errorNode, argList,
					&TypeResult{Type: overloaded}, nil, skipUnknownArgCheck, nil)
			}
		}

		return result, true

	case "NewType":
		return &CallResult{ReturnType: classTypeOrNil(e.createNewType(errorNode, argList))}, true

	case "TypedDict":
		return &CallResult{ReturnType: CreateTypedDictType(e, errorNode, expandedCallType, argList)}, true

	case "auto":
		if len(argList) == 0 {
			return &CallResult{ReturnType: GetEnumAutoValueType(e, errorNode)}, true
		}
	}

	// The original's comment: handle sentinel calls specially.
	//
	// Matched by full name rather than by class name, since `sentinel` is not a
	// reserved word and a user class of that name must not be intercepted.
	switch expandedCallType.Shared.FullName {
	case "builtins.sentinel", "typing_extensions.sentinel", "typing_extensions.Sentinel":
		return &CallResult{ReturnType: CreateSentinelType(e, errorNode, argList)}, true
	}

	if ClassTypeIsSpecialFormClass(expandedCallType) {
		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.TypeNotIntantiable().Format(className), errorNode, nil)
		return &CallResult{ReturnType: UnknownTypeCreate(false), ArgumentErrors: true}, true
	}

	return nil, false
}

// validateCallForMetaclass is the isInstantiableMetaclass arm: `type(x)`,
// `type(name, bases, dict)`, and calls to user metaclasses.
func (e *typeEvaluator) validateCallForMetaclass(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	expandedCallType *ClassType,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
) *CallResult {
	if expandedCallType.Priv.TypeArgs != nil && expandedCallType.Priv.IsTypeArgExplicit != nil &&
		*expandedCallType.Priv.IsTypeArgExplicit {
		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.ObjectNotCallable().Format(e.PrintType(expandedCallType, nil)),
			errorNode, nil)
		return &CallResult{ReturnType: UnknownTypeCreate(false), ArgumentErrors: true}
	}

	// The original's comment: validate the constructor arguments.
	//
	// The result is discarded; every branch below computes its own return type.
	ValidateConstructorArgs(e, errorNode, argList, expandedCallType, skipUnknownArgCheck, inferenceContext)

	// The original's comment: the one-parameter form of "type" returns the class
	// for the specified object.
	if expandedCallType.Shared.Name == "type" && len(argList) == 1 {
		argTypeResult := e.GetTypeOfArg(argList[0], nil)

		returnType := MapSubtypes(argTypeResult.Type, func(subtype Type) Type {
			if IsNever(subtype) {
				return subtype
			}

			if IsClass(subtype) {
				// The original's comment: specifically handle the case where the
				// subtype is a class-like object created by calling NewType. At
				// runtime, it's actually a FunctionType object.
				cls := subtype.(*ClassType)
				if IsClassInstance(subtype) && ClassTypeIsNewTypeClass(cls) && !cls.Priv.IncludeSubclasses {
					if e.prefetched != nil && e.prefetched.FunctionClass != nil {
						return e.prefetched.FunctionClass
					}
				}

				return ConvertToInstantiable(e.StripLiteralValue(subtype), false)
			}

			if subtype.Base().IsInstance() && (IsFunction(subtype) || IsTypeVar(subtype)) {
				return ConvertToInstantiable(subtype, false)
			}

			return ClassTypeSpecialize(ClassTypeCloneAsInstance(expandedCallType, false),
				[]Type{UnknownTypeCreate(false)}, nil, false, nil, nil)
		}, nil)

		return &CallResult{ReturnType: returnType, IsTypeIncomplete: argTypeResult.IsIncomplete}
	}

	if len(argList) >= 2 {
		// The original's comment: the two-parameter form of a call to a metaclass
		// returns a new class built from the specified base types.
		var returnType Type = AnyTypeCreate(false)
		if newClass := e.createClassFromMetaclass(errorNode, argList, expandedCallType); newClass != nil {
			returnType = newClass
		}
		return &CallResult{ReturnType: returnType}
	}

	// The original's comment: if the parameter to type() is not statically known,
	// fall back to Any.
	return &CallResult{ReturnType: AnyTypeCreate(false)}
}

// reportAbstractInstantiation is the supportsAbstractMethods block. It reports at
// most two unimplemented members by name and then a count of the rest.
func (e *typeEvaluator) reportAbstractInstantiation(
	errorNode parser.ExpressionNode, expandedCallType *ClassType, unexpandedCallType Type,
) {
	if !ClassTypeSupportsAbstractMethods(expandedCallType) {
		return
	}

	abstractSymbols := e.GetAbstractSymbols(expandedCallType)

	// includeSubclasses means the value may be a concrete subclass, and a TypeVar
	// may be solved as one; neither can be proven abstract here.
	if len(abstractSymbols) == 0 || expandedCallType.Priv.IncludeSubclasses || IsTypeVar(unexpandedCallType) {
		return
	}

	// The original's comment: if the class is abstract, it can't be instantiated.
	diagAddendum := common.NewDiagnosticAddendum()
	const errorsToDisplay = 2

	for index, abstractMethod := range abstractSymbols {
		if index == errorsToDisplay {
			diagAddendum.AddMessage(localization.LocAddendum.MemberIsAbstractMore().Format(
				len(abstractSymbols) - errorsToDisplay))
		} else if index < errorsToDisplay {
			if IsInstantiableClass(abstractMethod.ClassType) {
				diagAddendum.AddMessage(localization.LocAddendum.MemberIsAbstract().Format(
					abstractMethod.ClassType.(*ClassType).Shared.Name, abstractMethod.SymbolName))
			}
		}
	}

	e.AddDiagnostic(
		DiagnosticRuleReportAbstractUsage,
		localization.LocMessage.InstantiateAbstract().Format(expandedCallType.Shared.Name)+
			diagAddendum.GetString(),
		errorNode,
		nil,
	)
}

// rebuildInstantiatedTypeAsClass is the original's final block: an instance whose
// MRO contains `type` is a class, and has to be represented as one. It returns
// nil when the result is not such an instance.
func (e *typeEvaluator) rebuildInstantiatedTypeAsClass(
	errorNode parser.ExpressionNode, argList []*Arg, expandedCallType *ClassType, returnType Type,
) Type {
	if returnType == nil || !IsClassInstance(returnType) {
		return nil
	}
	returnClass := returnType.(*ClassType)

	derivesFromType := false
	for _, baseClass := range returnClass.Shared.Mro {
		if cls, ok := baseClass.(*ClassType); ok && IsInstantiableClass(baseClass) &&
			ClassTypeIsBuiltInNamed(cls, "type") {
			derivesFromType = true
			break
		}
	}
	if !derivesFromType {
		return nil
	}

	newClassName := "__class_" + returnClass.Shared.Name
	if len(argList) == 3 {
		firstArgType := e.GetTypeOfArg(argList[0], nil).Type

		if IsClassInstance(firstArgType) && ClassTypeIsBuiltInNamed(firstArgType.(*ClassType), "str") {
			if literal, ok := firstArgType.(*ClassType).Priv.LiteralValue.(LiteralString); ok {
				newClassName = string(literal)
			}
		}
	}

	newClassType := ClassTypeCreateInstantiable(
		newClassName, "", "",
		GetFileInfo(errorNode).FileUri,
		ClassTypeFlagsNone,
		GetTypeSourceID(errorNode),
		ClassTypeCloneAsInstantiable(returnClass, false),
		ClassTypeCloneAsInstantiable(returnClass, false),
		nil,
	)
	newClassType.Shared.BaseClasses = append(newClassType.Shared.BaseClasses,
		e.GetBuiltInType(errorNode, "object"))
	newClassType.Shared.EffectiveMetaclass = expandedCallType
	newClassType.Shared.Declaration = returnClass.Shared.Declaration

	ComputeMroLinearization(newClassType)
	return newClassType
}

// GetDeprecatedMessageFromCall corresponds to the decorators.ts function of the
// same name. The original's comment: given a @typing.deprecated call node,
// returns either ” or a custom deprecation message if one is provided.
func GetDeprecatedMessageFromCall(node *parser.CallNode) string {
	if len(node.D.Args) == 0 || node.D.Args[0].D.ArgCategory != parser.ArgCategorySimple {
		return ""
	}

	stringListNode, ok := node.D.Args[0].D.ValueExpr.(*parser.StringListNode)
	if !ok {
		return ""
	}

	message := ""
	for _, s := range stringListNode.D.Strings {
		if stringNode, ok := s.(*parser.StringNode); ok {
			message += stringNode.D.Value.String()
		}
	}

	return convertDocStringToPlainText(message)
}

// convertDocStringToPlainText stands in for the docStringConversion.ts function
// of the same name, which strips reStructuredText and epytext markup.
//
// It cannot record itself on the frontier: the counter lives on the evaluator
// and neither this nor its caller has one, since neither original takes an
// evaluator. Returning the string unchanged means a deprecation message with
// markup in it is displayed verbatim, which is wrong only in its formatting.
func convertDocStringToPlainText(docString string) string {
	return docString
}
