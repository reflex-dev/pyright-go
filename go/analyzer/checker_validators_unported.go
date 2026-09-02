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

func (c *Checker) validateBaseClassOverrides(_ *ClassType) {
	c.noteUnported("checker.validateBaseClassOverrides")
}

func (c *Checker) validateTypedDictOverrides(_ *ClassType) {
	c.noteUnported("checker.validateTypedDictOverrides")
}

func (c *Checker) validateMultipleInheritanceCompatibility(_ *ClassType, _ *parser.NameNode) {
	c.noteUnported("checker.validateMultipleInheritanceCompatibility")
}

func (c *Checker) validateInstanceVariableInitialization(_ *parser.ClassNode, _ *ClassType) {
	c.noteUnported("checker.validateInstanceVariableInitialization")
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

func (c *Checker) validateExceptionType(_ Type, _ parser.ExpressionNode, _ bool) {
	c.noteUnported("checker.validateExceptionType")
}

func (c *Checker) reportDeprecatedUseForType(_ *parser.NameNode, _ Type, _ bool) {
	c.noteUnported("checker.reportDeprecatedUseForType")
}

func (c *Checker) validateSuperCallForMethod(
	_ *parser.FunctionNode, _ *FunctionType, _ *ClassType,
) {
	c.noteUnported("checker.validateSuperCallForMethod")
}

func (c *Checker) validateClsSelfParamType(
	_ *parser.FunctionNode, _ *FunctionType, _ *ClassType, _ bool,
) {
	c.noteUnported("checker.validateClsSelfParamType")
}
