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
	"github.com/microsoft/pyright/go/parser"
)

func noteEvaluatorUnported(evaluator TypeEvaluator, name string) {
	if reporter, ok := evaluator.(interface{ noteUnported(string) }); ok {
		reporter.noteUnported(name)
	}
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

// IsEnumClassWithMembers corresponds to the enums.ts function of the same name:
// is this an enum class that actually declares at least one member?
//
// "Declares a member" is not the same as "has a class-level assignment". A name
// in an enum body becomes a member only if transformTypeForEnumMember says so,
// and the test that it did is that the resulting type is an instance of the enum
// class itself -- which is exactly what distinguishes `RED = 1` from a method or
// an annotated-but-unassigned name.
func IsEnumClassWithMembers(evaluator TypeEvaluator, classType *ClassType) bool {
	if classType == nil || !ClassTypeIsEnumClass(classType) {
		return false
	}

	// The original's comment: determine whether the enum class defines a member.
	symbolTable := ClassTypeGetSymbolTable(classType)
	for _, name := range symbolTable.Keys() {
		symbolType := TransformTypeForEnumMember(evaluator, classType, name)
		if symbolType != nil && IsClassInstance(symbolType) &&
			ClassTypeIsSameGenericClass(symbolType.(*ClassType),
				ClassTypeCloneAsInstance(classType, true), 0) {
			return true
		}
	}

	return false
}

/*
 * The evaluator's own.
 */

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
