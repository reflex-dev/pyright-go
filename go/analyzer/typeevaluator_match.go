/*
 * typeevaluator_match.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * evaluateTypesForMatchStatement and evaluateTypesForCaseStatement.
 *
 * These are the two entry points into patternMatching.ts. Both compute the
 * subject type by replaying the *negative* narrowing of every preceding
 * guard-free case, which is what gives a `match` statement its cumulative
 * behavior: by the last case the subject holds only what nothing above it
 * matched, and if that is Never the statement is exhaustive.
 *
 * A case with a guard (`case X() if cond:`) is skipped in that replay, and must
 * be: the guard may be false at runtime, so reaching a later case does not imply
 * the pattern failed to match.
 *
 * The match-statement form narrows through *every* case and caches that on the
 * match node; the case-statement form stops at the case being evaluated. The two
 * are separate because the code-flow engine asks each question independently.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// EvaluateTypesForMatchStatement corresponds to evaluateTypesForMatchStatement.
func (e *typeEvaluator) EvaluateTypesForMatchStatement(node *parser.MatchNode) {
	if e.isTypeCached(node) {
		return
	}

	subjectTypeResult := e.GetTypeOfExpression(node.D.Expr, EvalFlagsNone, nil)
	subjectType := subjectTypeResult.Type

	// The original's comment: apply negative narrowing for each of the cases that
	// doesn't have a guard statement.
	for _, caseStatement := range node.D.Cases {
		if caseStatement.D.GuardExpr == nil {
			subjectType = NarrowTypeBasedOnPattern(e, subjectType, caseStatement.D.Pattern, false)
		}
	}

	e.writeTypeCache(node,
		&TypeResult{Type: subjectType, IsIncomplete: subjectTypeResult.IsIncomplete},
		evalFlagsNonePtr(), nil, false)
}

// EvaluateTypesForCaseStatement corresponds to evaluateTypesForCaseStatement.
func (e *typeEvaluator) EvaluateTypesForCaseStatement(node *parser.CaseNode) {
	if e.isTypeCached(node) {
		return
	}

	parentNode := node.NodeBase().Parent
	if parentNode == nil || parentNode.GetNodeType() != parser.ParseNodeTypeMatch {
		// The original calls fail() here, which throws in a debug build and is a
		// no-op otherwise. The port declines to abort the analysis over a malformed
		// tree and simply returns, matching the release behavior.
		return
	}
	matchNode := parentNode.(*parser.MatchNode)

	fileInfo := GetFileInfo(node)
	subjectTypeResult := e.GetTypeOfExpression(matchNode.D.Expr, EvalFlagsNone, nil)
	subjectType := subjectTypeResult.Type

	// The original's comment: apply negative narrowing for each of the cases prior
	// to the current one except for those that have a guard expression.
	for _, caseStatement := range matchNode.D.Cases {
		if caseStatement == node {
			if fileInfo.DiagnosticRuleSet.ReportUnnecessaryComparison != DiagnosticLevelNone {
				if !subjectTypeResult.IsIncomplete {
					CheckForUnusedPattern(e, node.D.Pattern, subjectType)
				}
			}
			break
		}

		if caseStatement.D.GuardExpr == nil {
			subjectType = NarrowTypeBasedOnPattern(e, subjectType, caseStatement.D.Pattern, false)
		}
	}

	narrowedSubjectType := AssignTypeToPatternTargets(
		e, subjectType, subjectTypeResult.IsIncomplete, node.D.Pattern)

	e.writeTypeCache(node,
		&TypeResult{Type: narrowedSubjectType, IsIncomplete: subjectTypeResult.IsIncomplete},
		evalFlagsNonePtr(), nil, false)
}
