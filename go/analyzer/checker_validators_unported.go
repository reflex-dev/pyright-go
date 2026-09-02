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

func (c *Checker) validateFinalMemberOverrides(_ *ClassType) {
	c.noteUnported("checker.validateFinalMemberOverrides")
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

func (c *Checker) validateTypedDictClassSuite(_ *parser.SuiteNode) {
	c.noteUnported("checker.validateTypedDictClassSuite")
}

func (c *Checker) validateEnumClassOverride(_ *parser.ClassNode, _ *ClassType) {
	c.noteUnported("checker.validateEnumClassOverride")
}

/*
 * The per-function validators.
 */

// validateFunctionParams stands in for the original's long parameter-checking
// block inside visitFunction: unknown and missing parameter types, keyword names
// after a `*args: P.args`, and the per-method checks.
func (c *Checker) validateFunctionParams(
	_ *parser.FunctionNode, _ *FunctionTypeResult, _ parser.ParseNode,
) {
	c.noteUnported("checker.validateFunctionParams")
}

func (c *Checker) validateFunctionReturn(_ *parser.FunctionNode, _ *FunctionType) {
	c.noteUnported("checker.validateFunctionReturn")
}

func (c *Checker) validateDunderSignatures(_ *parser.FunctionNode, _ *FunctionType, _ bool) {
	c.noteUnported("checker.validateDunderSignatures")
}

func (c *Checker) validateTypeGuardFunction(_ *parser.FunctionNode, _ *FunctionType, _ bool) {
	c.noteUnported("checker.validateTypeGuardFunction")
}

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
