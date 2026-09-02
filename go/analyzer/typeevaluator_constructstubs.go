/*
 * typeevaluator_constructstubs.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * The satellites validateCallForInstantiableClass reaches, each a named stub so
 * the frontier ranks them separately. Every one is a distinct unit of upstream
 * work -- a whole file in most cases -- rather than a piece of typeEvaluator.ts.
 *
 * The free functions take the TypeEvaluator interface, as their originals do,
 * and reach the unported counter through the noteUnported assertion.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

func noteEvaluatorUnported(evaluator TypeEvaluator, name string) {
	if reporter, ok := evaluator.(interface{ noteUnported(string) }); ok {
		reporter.noteUnported(name)
	}
}

// ValidateConstructorArgs corresponds to the constructors.ts function of the same
// name: the __call__ / __new__ / __init__ sequence that a class call goes
// through. It is the whole of the ordinary construction path.
func ValidateConstructorArgs(
	evaluator TypeEvaluator,
	_ parser.ExpressionNode, _ []*Arg, classType *ClassType, _ bool, _ *InferenceContext,
) *CallResult {
	noteEvaluatorUnported(evaluator, "constructors.validateConstructorArgs")
	return &CallResult{ReturnType: ConvertToInstance(classType, false)}
}

// GetBoundInitMethod corresponds to the constructors.ts function of the same name.
func GetBoundInitMethod(
	evaluator TypeEvaluator, _ parser.ExpressionNode, _ *ClassType,
	_ *common.DiagnosticAddendum, _ MemberAccessFlags,
) *TypeResult {
	noteEvaluatorUnported(evaluator, "constructors.getBoundInitMethod")
	return nil
}

// CreateNamedTupleType corresponds to the namedTuples.ts function of the same
// name, which synthesizes a class from a NamedTuple(...) call.
func CreateNamedTupleType(
	evaluator TypeEvaluator, _ parser.ExpressionNode, _ []*Arg, _ bool,
) Type {
	noteEvaluatorUnported(evaluator, "namedTuples.createNamedTupleType")
	return UnknownTypeCreate(false)
}

// CreateTypedDictType corresponds to the typedDicts.ts function of the same name.
func CreateTypedDictType(
	evaluator TypeEvaluator, _ parser.ExpressionNode, _ *ClassType, _ []*Arg,
) Type {
	noteEvaluatorUnported(evaluator, "typedDicts.createTypedDictType")
	return UnknownTypeCreate(false)
}

// CreateSentinelType corresponds to the sentinels.ts function of the same name.
func CreateSentinelType(evaluator TypeEvaluator, _ parser.ExpressionNode, _ []*Arg) Type {
	noteEvaluatorUnported(evaluator, "sentinels.createSentinelType")
	return UnknownTypeCreate(false)
}

// CreateEnumType corresponds to the enums.ts function of the same name, which
// builds a class from the functional `Color = Enum("Color", "RED GREEN")` form.
// Returning nil is the original's undefined, and the caller falls back.
func CreateEnumType(
	evaluator TypeEvaluator, _ parser.ExpressionNode, _ *ClassType, _ []*Arg,
) Type {
	noteEvaluatorUnported(evaluator, "enums.createEnumType")
	return nil
}

// GetEnumAutoValueType corresponds to the enums.ts function of the same name.
func GetEnumAutoValueType(evaluator TypeEvaluator, _ parser.ExpressionNode) Type {
	noteEvaluatorUnported(evaluator, "enums.getEnumAutoValueType")
	return UnknownTypeCreate(false)
}

// IsEnumClassWithMembers corresponds to the enums.ts function of the same name.
// Answering false routes a genuine enum class into the functional-form path, so
// this stub is deliberately conservative in the direction that reports nothing.
func IsEnumClassWithMembers(evaluator TypeEvaluator, _ *ClassType) bool {
	noteEvaluatorUnported(evaluator, "enums.isEnumClassWithMembers")
	return true
}

/*
 * The evaluator's own.
 */

func (e *typeEvaluator) createTypeVarTupleType(
	_ parser.ExpressionNode, _ *ClassType, _ []*Arg,
) Type {
	e.unported("createTypeVarTupleType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) createParamSpecType(
	_ parser.ExpressionNode, _ *ClassType, _ []*Arg,
) Type {
	e.unported("createParamSpecType")
	return UnknownTypeCreate(false)
}

// createTypeAliasType corresponds to the function of the same name: the PEP 695
// TypeAliasType(...) runtime form. Returning nil is the original's undefined.
func (e *typeEvaluator) createTypeAliasType(_ parser.ExpressionNode, _ []*Arg) Type {
	e.unported("createTypeAliasType")
	return nil
}

// createNewType corresponds to the function of the same name.
func (e *typeEvaluator) createNewType(_ parser.ExpressionNode, _ []*Arg) Type {
	e.unported("createNewType")
	return UnknownTypeCreate(false)
}

// createClassFromMetaclass corresponds to the function of the same name: the
// three-argument `type(name, bases, dict)` form.
func (e *typeEvaluator) createClassFromMetaclass(
	_ parser.ExpressionNode, _ []*Arg, _ *ClassType,
) Type {
	e.unported("createClassFromMetaclass")
	return nil
}

// getAbstractSymbols corresponds to the function of the same name. The original's
// comment: returns a list of unimplemented abstract symbols (methods or
// variables) for the specified class.
func (e *typeEvaluator) getAbstractSymbols(_ *ClassType) []*AbstractSymbol {
	e.unported("getAbstractSymbols")
	return nil
}

// validateOverloadedArgTypes corresponds to the function of the same name, which
// tries each overload in order and reports against the best partial match.
func (e *typeEvaluator) validateOverloadedArgTypes(
	_ parser.ExpressionNode, _ []*Arg, _ *TypeResult,
	_ *ConstraintTracker, _ bool, _ *InferenceContext,
) *CallResult {
	e.unported("validateOverloadedArgTypes")
	return &CallResult{ArgumentErrors: true}
}
