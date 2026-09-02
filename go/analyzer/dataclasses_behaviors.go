/*
 * dataclasses_behaviors.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/dataClasses.ts (pyright 1.1.412):
 * applyDataClassClassBehaviorOverrides, applyDataClassBehaviorOverride,
 * applyDataClassBehaviorOverrideValue and applyDataClassDecorator.
 *
 * This is the function that makes a class a dataclass. `ClassType.isDataClass`
 * is defined as `shared.dataClassBehaviors !== undefined`, and this is the only
 * place that assigns it -- so every dataclass predicate in the analyzer answers
 * false until this runs.
 *
 * The behaviors themselves come from the decorator (or from a
 * dataclass_transform metaclass) and are then overridden argument by argument.
 * `frozen` is the one that is deliberately *not* inherited: it is reset to
 * frozenDefault before the arguments are applied, because frozenness is a
 * property of the decorator invocation rather than of the base class.
 *
 * That reset is also why `frozen` is re-applied explicitly when no `frozen=`
 * argument was passed. The value would be unchanged, but running it through
 * applyDataClassBehaviorOverrideValue is what performs the base-class
 * consistency check -- a frozen dataclass may not derive from a non-frozen one
 * or vice versa -- and skipping it would skip that diagnostic.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// ApplyDataClassClassBehaviorOverrides corresponds to
// applyDataClassClassBehaviorOverrides.
func ApplyDataClassClassBehaviorOverrides(
	evaluator TypeEvaluator,
	errorNode parser.ParseNode,
	classType *ClassType,
	args []*Arg,
	defaultBehaviors *DataClassBehaviors,
) {
	sawFrozenArg := false

	behaviors := *defaultBehaviors

	// The original's comment: the "frozen" behavior is not inherited from the
	// parent class. Instead, it comes from the default.
	behaviors.Frozen = behaviors.FrozenDefault

	classType.Shared.DataClassBehaviors = &behaviors

	for _, arg := range args {
		if arg.ValueExpression == nil || arg.Name == nil {
			continue
		}

		applyDataClassBehaviorOverride(evaluator, arg.Name, classType,
			arg.Name.D.Value, arg.ValueExpression, &behaviors)

		if arg.Name.D.Value == "frozen" {
			sawFrozenArg = true
		}
	}

	// The original's comment: if there was no frozen argument, it is implicitly
	// set to the frozenDefault. This check validates that we're not overriding a
	// frozen class with a non-frozen class or vice versa.
	if !sawFrozenArg {
		applyDataClassBehaviorOverrideValue(evaluator, errorNode, classType,
			"frozen", &defaultBehaviors.FrozenDefault, &behaviors)
	}
}

// ApplyDataClassDecorator corresponds to applyDataClassDecorator.
func ApplyDataClassDecorator(
	evaluator TypeEvaluator,
	errorNode parser.ParseNode,
	classType *ClassType,
	defaultBehaviors *DataClassBehaviors,
	callNode *parser.CallNode,
) {
	args := []*Arg{}
	if callNode != nil {
		for _, arg := range callNode.D.Args {
			args = append(args, evaluator.ConvertNodeToArg(arg))
		}
	}

	ApplyDataClassClassBehaviorOverrides(evaluator, errorNode, classType, args, defaultBehaviors)
}

// applyDataClassBehaviorOverride corresponds to the function of the same name.
func applyDataClassBehaviorOverride(
	evaluator TypeEvaluator,
	errorNode parser.ParseNode,
	classType *ClassType,
	argName string,
	argValueExpr parser.ExpressionNode,
	behaviors *DataClassBehaviors,
) {
	fileInfo := GetFileInfo(errorNode)

	// The original passes only three arguments; the two alias lists are optional
	// there and absent here. `known` false is the original's `undefined`, meaning
	// the argument is not a statically known boolean and the behavior is left
	// alone.
	value, known := EvaluateStaticBoolExpression(argValueExpr,
		fileInfo.ExecutionEnvironment, fileInfo.DefinedConstants, nil, nil)

	var argValue *bool
	if known {
		argValue = &value
	}

	applyDataClassBehaviorOverrideValue(evaluator, errorNode, classType, argName, argValue, behaviors)
}

// applyDataClassBehaviorOverrideValue corresponds to the function of the same
// name. argValue is nil where the original's is undefined.
func applyDataClassBehaviorOverrideValue(
	evaluator TypeEvaluator,
	errorNode parser.ParseNode,
	classType *ClassType,
	argName string,
	argValue *bool,
	behaviors *DataClassBehaviors,
) {
	switch argName {
	case "order":
		if argValue != nil {
			behaviors.GenerateOrder = *argValue
		}

	case "kw_only":
		if argValue != nil {
			behaviors.KeywordOnly = *argValue
		}

	case "match_args":
		if argValue != nil {
			behaviors.MatchArgs = *argValue
		}

	case "frozen":
		applyFrozenOverride(evaluator, errorNode, classType, argValue, behaviors)

	case "init":
		if argValue != nil {
			behaviors.SkipGenerateInit = !*argValue
		}

	case "eq":
		if argValue != nil {
			behaviors.SkipGenerateEq = !*argValue
		}

	case "slots":
		if argValue != nil && *argValue {
			behaviors.GenerateSlots = true

			if classType.Shared.LocalSlotsNames != nil {
				evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.DataClassSlotsOverwrite(), errorNode, nil)
			}
		} else if argValue != nil && !*argValue {
			behaviors.GenerateSlots = false
		}

	case "hash", "unsafe_hash":
		if argValue != nil && *argValue {
			behaviors.GenerateHash = true
		}
	}
}

// applyFrozenOverride is the original's `frozen` case, lifted out because it is
// the only arm with a body.
func applyFrozenOverride(
	evaluator TypeEvaluator,
	errorNode parser.ParseNode,
	classType *ClassType,
	argValue *bool,
	behaviors *DataClassBehaviors,
) {
	hasUnfrozenBaseClass := false
	hasFrozenBaseClass := false

	if argValue != nil {
		behaviors.Frozen = *argValue
	}

	for _, baseClass := range classType.Shared.BaseClasses {
		if !IsInstantiableClass(baseClass) || !ClassTypeIsDataClass(baseClass.(*ClassType)) {
			continue
		}

		base := baseClass.(*ClassType)
		if ClassTypeIsDataClassFrozen(base) {
			hasFrozenBaseClass = true
			continue
		}

		metaclassHasTransform := base.Shared.DeclaredMetaclass != nil &&
			IsInstantiableClass(base.Shared.DeclaredMetaclass) &&
			base.Shared.DeclaredMetaclass.(*ClassType).Shared.ClassDataClassTransform != nil

		if base.Shared.ClassDataClassTransform == nil && !metaclassHasTransform {
			// The original's comment: if this base class is unfrozen and isn't the
			// class that directly references the metaclass that provides
			// dataclass-like behaviors, we'll assume we're deriving from an
			// unfrozen dataclass.
			hasUnfrozenBaseClass = true
		}
	}

	if argValue != nil && *argValue {
		// The original's comment: a frozen dataclass cannot derive from a
		// non-frozen dataclass.
		if hasUnfrozenBaseClass {
			evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.DataClassBaseClassNotFrozen(), errorNode, nil)
		}
		return
	}

	// The original's comment: a non-frozen dataclass cannot derive from a frozen
	// dataclass. Note the original's `else` covers argValue undefined as well as
	// false, so an unknown frozen= argument takes this branch.
	if hasFrozenBaseClass {
		evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.DataClassBaseClassFrozen(), errorNode, nil)
	}
}
