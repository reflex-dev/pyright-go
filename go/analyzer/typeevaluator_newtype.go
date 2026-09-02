/*
 * typeevaluator_newtype.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412): createNewType.
 *
 * `UserId = NewType('UserId', int)` creates a distinct type that is assignable
 * *to* its base but not *from* it. There is no runtime class -- at runtime
 * NewType returns an identity function -- so the class synthesized here exists
 * purely to carry that one-way relationship, which is why it is marked Final and
 * NewTypeClass and why its `__init__` accepts exactly one argument of the base
 * type.
 *
 * As with TypeAliasType and Sentinel, the string name and the assigned variable
 * must agree, because the runtime object records the name it was given.
 *
 * The rejections are each for a different reason and are not interchangeable. A
 * protocol or TypedDict base is rejected because NewType's nominal identity is
 * meaningless over a structural type. A literal base is rejected because the
 * literal's value is not part of what the subtype would inherit. `Annotated` is
 * rejected separately from the general "not a class" case, since it *would*
 * otherwise resolve to its underlying class and silently drop the annotation.
 *
 * An Any base is reported but not fatal: the class is still built, with `object`
 * standing in, so downstream code sees a usable type rather than nothing. That is
 * also why the synthesized methods are skipped in that case -- an `__init__`
 * taking Any would accept everything and defeat the point.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// createNewType corresponds to the function of the same name. It returns nil
// where the original returns undefined.
func (e *typeEvaluator) createNewType(
	errorNode parser.ExpressionNode, argList []*Arg,
) *ClassType {
	fileInfo := GetFileInfo(errorNode)
	className := ""

	if len(argList) != 2 {
		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.NewTypeParamCount(), errorNode, nil)
		return nil
	}

	nameArg := argList[0]
	if nameArg.ArgCategory == parser.ArgCategorySimple && nameArg.ValueExpression != nil &&
		nameArg.ValueExpression.GetNodeType() == parser.ParseNodeTypeStringList {
		className = joinStringListValue(nameArg.ValueExpression.(*parser.StringListNode))
	}

	if className == "" {
		e.AddDiagnostic(DiagnosticRuleReportArgumentType,
			localization.LocMessage.NewTypeBadName(), argNodeOrErrorNode(argList[0], errorNode), nil)
		return nil
	}

	// The runtime object records the name it was given, so a mismatch with the
	// assigned variable is a real inconsistency.
	if parentNode := errorNode.NodeBase().Parent; parentNode != nil &&
		parentNode.GetNodeType() == parser.ParseNodeTypeAssignment {
		leftExpr := parentNode.(*parser.AssignmentNode).D.LeftExpr
		if leftExpr.GetNodeType() == parser.ParseNodeTypeName &&
			leftExpr.(*parser.NameNode).D.Value != className {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.NewTypeNameMismatch(), leftExpr, nil)
			return nil
		}
	}

	baseClass := e.getTypeOfArgExpectingType(argList[1], nil).Type
	isBaseClassAny := false

	if IsAnyOrUnknown(baseClass) {
		// Reported but not fatal: the class is still built with `object` standing
		// in, so downstream code sees a usable type.
		baseClass = UnknownTypeCreate(false)
		if e.prefetched != nil && e.prefetched.ObjectClass != nil {
			baseClass = e.prefetched.ObjectClass
		}

		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.NewTypeAnyOrUnknown(), argNodeOrErrorNode(argList[1], errorNode), nil)

		isBaseClassAny = true
	}

	// The original's comment: specifically disallow Annotated. It is rejected
	// separately from the general not-a-class case because it would otherwise
	// resolve to its underlying class and silently drop the annotation.
	if sf := propsSpecialForm(baseClass); sf != nil && IsClassInstance(sf) &&
		ClassTypeIsBuiltInNamed(sf, "Annotated") {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.NewTypeNotAClass(), argNodeOrErrorNode(argList[1], errorNode), nil)
		return nil
	}

	if !IsInstantiableClass(baseClass) {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.NewTypeNotAClass(), argNodeOrErrorNode(argList[1], errorNode), nil)
		return nil
	}
	baseClassType := baseClass.(*ClassType)

	if ClassTypeIsProtocolClass(baseClassType) || ClassTypeIsTypedDictClass(baseClassType) {
		// A nominal identity over a structural type is meaningless.
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.NewTypeProtocolClass(), argNodeOrErrorNode(argList[1], errorNode), nil)
	} else if baseClassType.Priv.LiteralValue != nil {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.NewTypeLiteral(), argNodeOrErrorNode(argList[1], errorNode), nil)
	}

	classType := ClassTypeCreateInstantiable(
		className,
		GetClassFullName(errorNode, fileInfo.ModuleName, className),
		fileInfo.ModuleName,
		fileInfo.FileUri,
		ClassTypeFlagsFinal|ClassTypeFlagsNewTypeClass|ClassTypeFlagsValidTypeAliasClass,
		GetTypeSourceID(errorNode),
		nil,
		baseClassType.Shared.EffectiveMetaclass,
		nil,
	)

	var baseToAdd Type = baseClassType
	if isBaseClassAny {
		baseToAdd = AnyTypeCreate(false)
	}
	classType.Shared.BaseClasses = append(classType.Shared.BaseClasses, baseToAdd)
	ComputeMroLinearization(classType)

	if isBaseClassAny {
		// An __init__ taking Any would accept everything and defeat the point.
		return classType
	}

	selfName, xName, clsName := "self", "_x", "cls"

	// The original's comment: synthesize an __init__ method that accepts only the
	// specified type.
	initType := FunctionTypeCreateSynthesizedInstance("__init__", FunctionTypeFlagsNone)
	FunctionTypeAddParam(initType, FunctionParamCreate(
		parser.ParamCategorySimple, AnyTypeCreate(false), FunctionParamFlagsTypeDeclared,
		&selfName, nil, nil))
	FunctionTypeAddParam(initType, FunctionParamCreate(
		parser.ParamCategorySimple, ClassTypeCloneAsInstance(baseClassType, false),
		FunctionParamFlagsTypeDeclared, &xName, nil, nil))
	initType.Shared.DeclaredReturnType = e.GetNoneType()
	ClassTypeGetSymbolTable(classType).Set("__init__",
		SymbolCreateWithType(SymbolFlagsClassMember, initType, nil))

	// The original's comment: synthesize a trivial __new__ method.
	newType := FunctionTypeCreateSynthesizedInstance("__new__", FunctionTypeFlagsConstructorMethod)
	FunctionTypeAddParam(newType, FunctionParamCreate(
		parser.ParamCategorySimple, AnyTypeCreate(false), FunctionParamFlagsTypeDeclared,
		&clsName, nil, nil))
	FunctionTypeAddDefaultParams(newType, false)
	newType.Shared.DeclaredReturnType = ClassTypeCloneAsInstance(classType, false)
	newType.Priv.ConstructorTypeVarScopeID = GetTypeVarScopeID(classType)
	ClassTypeGetSymbolTable(classType).Set("__new__",
		SymbolCreateWithType(SymbolFlagsClassMember, newType, nil))

	return classType
}

// argOrErrorNode is the original's repeated `arg.node ?? errorNode`.
func argNodeOrErrorNode(arg *Arg, errorNode parser.ExpressionNode) parser.ParseNode {
	if arg.Node != nil {
		return arg.Node
	}
	return errorNode
}

// classTypeOrNil widens a *ClassType into a Type without producing a typed nil.
// A nil *ClassType stored in a Type interface is non-nil as an interface value,
// so every downstream `IsNever(t)` and `IsClass(t)` dereferences it. The original
// has no such trap: `undefined` assigned to an optional field stays undefined.
func classTypeOrNil(t *ClassType) Type {
	if t == nil {
		return nil
	}
	return t
}
