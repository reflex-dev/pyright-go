/*
 * checker_validators_unported.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * The per-class and per-function validators visitClass and visitFunction reach.
 * Each is a separate unit of upstream work -- several are a hundred lines or
 * more -- so each records itself separately and the frontier ranks them, the
 * same treatment the evaluator's satellites got.
 *
 * Splitting these out rather than leaving visitClass and visitFunction unported
 * is what lets the walk drive evaluation now: computing a class's or function's
 * type is also what marks the symbols its annotations reference as accessed, and
 * without that every typing import in the corpus reads as unused.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

func (c *Checker) validateProtocolTypeParamVariance(_ *parser.ClassNode, _ *ClassType) {
	c.noteUnported("checker.validateProtocolTypeParamVariance")
}

func (c *Checker) validateSlotsClassVarConflict(_ *ClassType) {
	c.noteUnported("checker.validateSlotsClassVarConflict")
}

func (c *Checker) validateBaseClassOverrides(_ *ClassType) {
	c.noteUnported("checker.validateBaseClassOverrides")
}

func (c *Checker) validateTypedDictOverrides(_ *ClassType) {
	c.noteUnported("checker.validateTypedDictOverrides")
}

func (c *Checker) validateOverloadDecoratorConsistency(_ *ClassType) {
	c.noteUnported("checker.validateOverloadDecoratorConsistency")
}

func (c *Checker) validateDisjointBaseClass(_ *ClassType, _ *parser.NameNode) {
	c.noteUnported("checker.validateDisjointBaseClass")
}

func (c *Checker) validateMultipleInheritanceBaseClasses(_ *ClassType, _ *parser.NameNode) {
	c.noteUnported("checker.validateMultipleInheritanceBaseClasses")
}

func (c *Checker) validateMultipleInheritanceCompatibility(_ *ClassType, _ *parser.NameNode) {
	c.noteUnported("checker.validateMultipleInheritanceCompatibility")
}

func (c *Checker) validateConstructorConsistency(_ *ClassType, _ *parser.NameNode) {
	c.noteUnported("checker.validateConstructorConsistency")
}

func (c *Checker) validateInstanceVariableInitialization(_ *parser.ClassNode, _ *ClassType) {
	c.noteUnported("checker.validateInstanceVariableInitialization")
}

func (c *Checker) validateFinalClassNotAbstract(_ *ClassType, _ *parser.ClassNode) {
	c.noteUnported("checker.validateFinalClassNotAbstract")
}

func (c *Checker) validateDataClassPostInit(_ *ClassType) {
	c.noteUnported("checker.validateDataClassPostInit")
}

func (c *Checker) validateEnumMembers(_ *ClassType, _ *parser.ClassNode) {
	c.noteUnported("checker.validateEnumMembers")
}

/*
 * The per-function validators.
 */

func (c *Checker) validateFunctionTypeVarUsage(_ *parser.FunctionNode, _ *FunctionTypeResult) {
	c.noteUnported("checker.validateFunctionTypeVarUsage")
}

func (c *Checker) validateGeneratorReturnType(_ *parser.FunctionNode, _ *FunctionType) {
	c.noteUnported("checker.validateGeneratorReturnType")
}

func (c *Checker) reportDeprecatedClassProperty(_ *parser.FunctionNode, _ *FunctionTypeResult) {
	c.noteUnported("checker.reportDeprecatedClassProperty")
}

func (c *Checker) validateOverloadConsistency(
	_ *parser.FunctionNode, _ *FunctionType, _ []*FunctionType,
) {
	c.noteUnported("checker.validateOverloadConsistency")
}

func (c *Checker) validateOverloadAttributeConsistency(_ *parser.FunctionNode, _ *OverloadedType) {
	c.noteUnported("checker.validateOverloadAttributeConsistency")
}

/*
 * The per-expression reporters the second batch of visits reaches.
 */

func (c *Checker) validateIsInstanceCall(_ *parser.CallNode) {
	c.noteUnported("checker.validateIsInstanceCall")
}

func (c *Checker) isTypeValidForUnusedValueTest(_ Type) bool {
	c.noteUnported("checker.isTypeValidForUnusedValueTest")
	return false
}

func (c *Checker) validateExceptionType(_ Type, _ parser.ExpressionNode, _ bool) {
	c.noteUnported("checker.validateExceptionType")
}

func (c *Checker) reportTupleIndexOutOfRange(_ *parser.IndexNode) {
	c.noteUnported("checker.reportTupleIndexOutOfRange")
}

func (c *Checker) checkBinaryOperation(_ *parser.BinaryOperationNode) {
	c.noteUnported("checker.checkBinaryOperation")
}

func (c *Checker) validateConditionalIsBool(_ parser.ExpressionNode) {
	c.noteUnported("checker.validateConditionalIsBool")
}

func (c *Checker) reportUnnecessaryConditionExpression(_ parser.ExpressionNode) {
	c.noteUnported("checker.reportUnnecessaryConditionExpression")
}

func (c *Checker) reportDeprecatedUseForType(_ *parser.NameNode, _ Type, _ bool) {
	c.noteUnported("checker.reportDeprecatedUseForType")
}

func (c *Checker) validateMethod(_ *parser.FunctionNode, _ *FunctionType, _ parser.ParseNode) {
	c.noteUnported("checker.validateMethod")
}
