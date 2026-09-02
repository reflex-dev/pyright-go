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

// validateBaseClassOverride stands in for _validateBaseClassOverride, the
// per-member comparison of a single override against a single base declaration.
// It is 396 lines in the original and the last large piece of checker.ts.
func (c *Checker) validateBaseClassOverride(
	_ *ClassMember, _ *Symbol, _ Type, _ *ClassType, _ string,
) {
	c.noteUnported("checker.validateBaseClassOverride")
}

// validateInstanceVariableInitialization is ported but parked under wip/: it
// depends on ClassType.Shared.NamedTupleEntries, which only
// dataClasses.synthesizeDataClassMethods populates, and that is still a stub.
func (c *Checker) validateInstanceVariableInitialization(_ *parser.ClassNode, _ *ClassType) {
	c.noteUnported("checker.validateInstanceVariableInitialization")
}
