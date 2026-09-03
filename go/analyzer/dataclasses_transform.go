/*
 * dataclasses_transform.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/dataClasses.ts (pyright 1.1.412):
 * validateDataClassTransformDecorator, and from typedDicts.ts:
 * narrowForKeyAssignment.
 *
 * `@dataclass_transform(...)` (PEP 681) lets a library declare that its own
 * decorator behaves like `@dataclass`. This reads the arguments and produces the
 * DataClassBehaviors the library's users will inherit. Every argument must be a
 * *statically known* boolean or a tuple of *statically known* callables, because
 * these values shape the synthesized `__init__` of every class the library
 * decorates -- a runtime-computed value could not be honored.
 *
 * `frozen_default` is stored twice, and the original explains why: a class that
 * does not explicitly pass `frozen=` inherits this default rather than its
 * parent's frozen-ness, so the default has to survive separately from the
 * current value. That is the same asymmetry the behavior-override chain relies
 * on.
 *
 * `field_descriptors` is the older spelling of `field_specifiers`, kept because
 * libraries shipped with it. Both are accepted; the original says so.
 *
 * narrowForKeyAssignment is unrelated but tiny and belongs with the TypedDict
 * narrowing it serves: assigning to a not-required key *proves the key is now
 * present*, so subsequent reads need no second check. Its two early returns are
 * both "nothing to record" -- a required key was always present, and a key
 * already marked provided is already narrowed.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// ValidateDataClassTransformDecorator corresponds to
// validateDataClassTransformDecorator.
func ValidateDataClassTransformDecorator(
	evaluator TypeEvaluator, node *parser.CallNode,
) *DataClassBehaviors {
	behaviors := &DataClassBehaviors{
		SkipGenerateInit:     false,
		SkipGenerateEq:       false,
		GenerateOrder:        false,
		GenerateSlots:        false,
		GenerateHash:         false,
		KeywordOnly:          false,
		Frozen:               false,
		FrozenDefault:        false,
		FieldDescriptorNames: []string{},
	}

	fileInfo := GetFileInfo(node)

	// The original's comment: parse the arguments to the call.
	for _, arg := range node.D.Args {
		if arg.D.Name == nil || arg.D.ArgCategory != parser.ArgCategorySimple {
			evaluator.AddDiagnostic(DiagnosticRuleReportCallIssue,
				localization.LocMessage.DataClassTransformPositionalParam(), arg, nil)
			continue
		}

		switch arg.D.Name.D.Value {
		case "kw_only_default":
			if value, ok := dataClassTransformBoolArg(evaluator, arg, fileInfo); ok {
				behaviors.KeywordOnly = value
			}

		case "eq_default":
			if value, ok := dataClassTransformBoolArg(evaluator, arg, fileInfo); ok {
				behaviors.SkipGenerateEq = !value
			}

		case "order_default":
			if value, ok := dataClassTransformBoolArg(evaluator, arg, fileInfo); ok {
				behaviors.GenerateOrder = value
			}

		case "frozen_default":
			if value, ok := dataClassTransformBoolArg(evaluator, arg, fileInfo); ok {
				behaviors.Frozen = value

				// The original's comment: store the frozen default separately
				// because any class that doesn't explicitly specify a frozen value
				// will inherit this value rather than the value from its parent.
				behaviors.FrozenDefault = value
			}

		// The original's comment: earlier versions of the dataclass_transform spec
		// used the name "field_descriptors" rather than "field_specifiers". The
		// older name is now deprecated but still supported for the time being
		// because some libraries shipped with the older __dataclass_transform__
		// form that supported this older parameter name.
		case "field_descriptors", "field_specifiers":
			addFieldSpecifierNames(evaluator, arg, behaviors)

		default:
			evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.DataClassTransformUnknownArgument().Format(arg.D.Name.D.Value),
				arg.D.ValueExpr, nil)
		}
	}

	return behaviors
}

// dataClassTransformBoolArg is the original's repeated evaluateStaticBoolExpression
// block. The second result is false where the original returns early after
// reporting.
func dataClassTransformBoolArg(
	evaluator TypeEvaluator, arg *parser.ArgumentNode, fileInfo *AnalyzerFileInfo,
) (bool, bool) {
	value, known := EvaluateStaticBoolExpression(arg.D.ValueExpr,
		fileInfo.ExecutionEnvironment, fileInfo.DefinedConstants, nil, nil)

	if !known {
		// These values shape every decorated class's __init__, so a
		// runtime-computed value cannot be honored.
		evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.DataClassTransformExpectedBoolLiteral(), arg.D.ValueExpr, nil)
		return false, false
	}

	return value, true
}

// addFieldSpecifierNames is the original's field_specifiers arm.
func addFieldSpecifierNames(
	evaluator TypeEvaluator, arg *parser.ArgumentNode, behaviors *DataClassBehaviors,
) {
	valueType := evaluator.GetTypeOfExpression(arg.D.ValueExpr, EvalFlagsNone, nil).Type

	valid := IsClassInstance(valueType) &&
		ClassTypeIsBuiltInNamed(valueType.(*ClassType), "tuple") &&
		valueType.(*ClassType).Priv.TupleTypeArgs != nil

	if valid {
		for _, entry := range valueType.(*ClassType).Priv.TupleTypeArgs {
			if !IsInstantiableClass(entry.Type) && !IsFunctionOrOverloaded(entry.Type) {
				valid = false
				break
			}
		}
	}

	if !valid {
		evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.DataClassTransformFieldSpecifier().Format(
				evaluator.PrintType(valueType, nil)),
			arg.D.ValueExpr, nil)
		return
	}

	for _, tupleArg := range valueType.(*ClassType).Priv.TupleTypeArgs {
		switch {
		case IsInstantiableClass(tupleArg.Type):
			behaviors.FieldDescriptorNames = append(behaviors.FieldDescriptorNames,
				tupleArg.Type.(*ClassType).Shared.FullName)

		case IsFunction(tupleArg.Type):
			behaviors.FieldDescriptorNames = append(behaviors.FieldDescriptorNames,
				tupleArg.Type.(*FunctionType).Shared.FullName)

		case IsOverloaded(tupleArg.Type):
			overloads := OverloadedTypeGetOverloads(tupleArg.Type.(*OverloadedType))
			if len(overloads) > 0 {
				behaviors.FieldDescriptorNames = append(behaviors.FieldDescriptorNames,
					overloads[0].Shared.FullName)
			}
		}
	}
}

// NarrowForKeyAssignment corresponds to the typedDicts.ts function of the same
// name. The original's comment: if the specified type has a non-required key,
// this method marks the key as present.
func NarrowForKeyAssignment(classType *ClassType, key string) *ClassType {
	// The original's comment: we should never be called if the classType is not a
	// TypedDict or if typedDictEntries is empty, but this can theoretically happen
	// in the presence of certain circular dependencies.
	if !ClassTypeIsTypedDictClass(classType) || classType.Shared.TypedDictEntries == nil {
		return classType
	}

	tdEntry, found := classType.Shared.TypedDictEntries.KnownItems.Get(key)
	if !found || tdEntry == nil || tdEntry.IsRequired {
		// A required key was always present; there is nothing to record.
		return classType
	}

	if classType.Priv.TypedDictNarrowedEntries() != nil {
		if narrowedTdEntry, ok := classType.Priv.TypedDictNarrowedEntries().Get(key); ok &&
			narrowedTdEntry != nil && narrowedTdEntry.IsProvided {
			return classType
		}
	}

	narrowedEntries := common.NewOrderedMap[string, *TypedDictEntry]()
	if existing := classType.Priv.TypedDictNarrowedEntries(); existing != nil {
		existing.ForEach(func(v *TypedDictEntry, k string) {
			narrowedEntries.Set(k, v)
		})
	}
	narrowedEntries.Set(key, &TypedDictEntry{
		IsProvided: true,
		IsRequired: false,
		IsReadOnly: tdEntry.IsReadOnly,
		ValueType:  tdEntry.ValueType,
	})

	return ClassTypeCloneForNarrowedTypedDictEntries(classType, narrowedEntries)
}
