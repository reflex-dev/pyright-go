/*
 * checker_stub.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateStubStatement and _reportUnusedDunderAllSymbols.
 *
 * A `.pyi` file is a declaration file, not a program: nothing in it executes, so
 * a statement that only makes sense at runtime is almost certainly a mistake
 * rather than an intent. validateStubStatement enumerates what is allowed and
 * reports everything else.
 *
 * The two exemptions are both `__all__` manipulation, and they are exemptions
 * rather than oversights: `__all__ += [...]` and `__all__.extend([...])` are how
 * a stub composes its export list from several sources, and there is no
 * declarative spelling for that. Note how narrow each check is -- the augmented
 * assignment must be `+=` specifically and the call must be a method *on*
 * `__all__` -- so an arbitrary call or an unrelated `-=` is still reported.
 *
 * reportUnusedDunderAllSymbols checks the opposite direction: every name listed
 * in `__all__` must actually exist in the module. A name that does not is an
 * AttributeError at import time for anyone doing `from m import *`.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateStubStatement corresponds to _validateStubStatement.
func (c *Checker) validateStubStatement(statement parser.ParseNode) {
	switch statement.GetNodeType() {
	case parser.ParseNodeTypeIf, parser.ParseNodeTypeFunction,
		parser.ParseNodeTypeClass, parser.ParseNodeTypeError:
		// The original's comment: these are allowed in a stub file.

	case parser.ParseNodeTypeWhile, parser.ParseNodeTypeFor,
		parser.ParseNodeTypeTry, parser.ParseNodeTypeWith:
		// The original's comment: these are not allowed.
		c.evaluator.AddDiagnostic(DiagnosticRuleReportInvalidStubStatement,
			localization.LocMessage.InvalidStubStatement(), statement, nil)

	case parser.ParseNodeTypeStatementList:
		for _, substatement := range statement.(*parser.StatementListNode).D.Statements {
			if isValidStubSubstatement(substatement) {
				continue
			}

			c.evaluator.AddDiagnostic(DiagnosticRuleReportInvalidStubStatement,
				localization.LocMessage.InvalidStubStatement(), substatement, nil)
		}
	}
}

// isValidStubSubstatement is the original's inner switch. Anything not named
// there is valid by default -- notably plain assignments and type annotations,
// which are what a stub is made of.
func isValidStubSubstatement(substatement parser.ParseNode) bool {
	switch substatement.GetNodeType() {
	case parser.ParseNodeTypeAssert,
		parser.ParseNodeTypeAssignmentExpression,
		parser.ParseNodeTypeAwait,
		parser.ParseNodeTypeBinaryOperation,
		parser.ParseNodeTypeConstant,
		parser.ParseNodeTypeDel,
		parser.ParseNodeTypeDictionary,
		parser.ParseNodeTypeIndex,
		parser.ParseNodeTypeFor,
		parser.ParseNodeTypeFormatString,
		parser.ParseNodeTypeGlobal,
		parser.ParseNodeTypeLambda,
		parser.ParseNodeTypeList,
		parser.ParseNodeTypeMemberAccess,
		parser.ParseNodeTypeName,
		parser.ParseNodeTypeNonlocal,
		parser.ParseNodeTypeNumber,
		parser.ParseNodeTypeRaise,
		parser.ParseNodeTypeReturn,
		parser.ParseNodeTypeSet,
		parser.ParseNodeTypeSlice,
		parser.ParseNodeTypeTernary,
		parser.ParseNodeTypeTuple,
		parser.ParseNodeTypeTry,
		parser.ParseNodeTypeUnaryOperation,
		parser.ParseNodeTypeUnpack,
		parser.ParseNodeTypeWhile,
		parser.ParseNodeTypeWith,
		parser.ParseNodeTypeWithItem,
		parser.ParseNodeTypeYield,
		parser.ParseNodeTypeYieldFrom:
		return false

	case parser.ParseNodeTypeAugmentedAssignment:
		// The original's comment: exempt __all__ manipulations. Deliberately
		// narrow: only `__all__ += ...`, not any augmented assignment.
		aug := substatement.(*parser.AugmentedAssignmentNode)
		return aug.D.Operator == parser.OperatorTypeAddEqual &&
			aug.D.LeftExpr.GetNodeType() == parser.ParseNodeTypeName &&
			aug.D.LeftExpr.(*parser.NameNode).D.Value == "__all__"

	case parser.ParseNodeTypeCall:
		// The original's comment: exempt __all__ manipulations. Again narrow:
		// the callee must be a member access *on* __all__.
		call := substatement.(*parser.CallNode)
		memberAccess, ok := call.D.LeftExpr.(*parser.MemberAccessNode)
		if !ok {
			return false
		}
		return memberAccess.D.LeftExpr.GetNodeType() == parser.ParseNodeTypeName &&
			memberAccess.D.LeftExpr.(*parser.NameNode).D.Value == "__all__"
	}

	return true
}

// reportUnusedDunderAllSymbols corresponds to _reportUnusedDunderAllSymbols: a
// name in __all__ that the module does not define is an AttributeError for any
// `from m import *`.
func (c *Checker) reportUnusedDunderAllSymbols(nodes []*parser.StringNode) {
	// The original's comment: if this rule is disabled, don't bother doing the
	// work.
	if c.fileInfo.DiagnosticRuleSet.ReportUnsupportedDunderAll == DiagnosticLevelNone {
		return
	}

	moduleScope := GetScope(c.moduleNode)
	if moduleScope == nil {
		return
	}

	for _, node := range nodes {
		name := node.D.Value.String()
		if !moduleScope.SymbolTable.Has(name) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportUnsupportedDunderAll,
				localization.LocMessage.DunderAllSymbolNotPresent().Format(name), node, nil)
		}
	}
}
