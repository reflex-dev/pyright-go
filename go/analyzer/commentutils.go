/*
 * commentutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Utility functions that parse comments and extract commands
 * or other directives from them.
 *
 * Transliterated from analyzer/commentUtils.ts (pyright 1.1.412).
 *
 * Two things shape the Go version:
 *
 *   - Comment text is common.Text (UTF-16 code units), not a Go string,
 *     because _trimTextWithRange asserts that the text length equals the range
 *     length and every offset it produces lands in a user-visible diagnostic
 *     range. Converting to UTF-8 first would shift them for any comment
 *     containing non-BMP or non-ASCII characters.
 *   - The original reaches into a rule set by name -- `(ruleSet as any)[rule]`
 *     -- which Go cannot do without reflection. The generated
 *     diagnosticRuleBoolFields / diagnosticRuleLevelFields maps in
 *     configoptions_gen.go stand in for that.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

const (
	strictSetting   = "strict"
	standardSetting = "standard"
	basicSetting    = "basic"
)

// CommentDiagnostic corresponds to the interface of the same name.
type CommentDiagnostic struct {
	Message string
	Range   common.TextRange
}

// GetFileLevelDirectives corresponds to getFileLevelDirectives. Diagnostics are
// appended through the pointer, standing in for the original's mutation of the
// caller's array.
//
// The return value is defaultRuleSet itself -- not a copy -- when nothing
// modified the rules, which is deliberate in the original: "use the default
// rule set to save memory".
func GetFileLevelDirectives(
	tokens *common.TextRangeCollection[parser.Token],
	lines *common.TextRangeCollection[common.TextRange],
	defaultRuleSet *DiagnosticRuleSet,
	useStrict bool,
	diagnostics *[]CommentDiagnostic,
) *DiagnosticRuleSet {
	ruleSet := CloneDiagnosticRuleSet(defaultRuleSet)
	isModified := false

	if useStrict {
		applyStrictRules(ruleSet)
		isModified = true
	}

	for i := 0; i < tokens.Count(); i++ {
		token := tokens.GetItemAt(i)
		for _, comment := range token.GetComments() {
			value, textRange := trimTextWithRange(comment.Value, common.TextRange{
				Start:  comment.Start,
				Length: comment.Length,
			})

			isCommentOnOwnLine := func() bool {
				curTokenLineOffset := common.ConvertOffsetToPosition(comment.Start, lines).Character
				return curTokenLineOffset <= 1
			}

			ruleSet = parsePyrightComment(value, textRange, isCommentOnOwnLine, ruleSet, diagnostics)
			isModified = true
		}
	}

	// If we didn't make any modifications, use the default rule set to save memory.
	if isModified {
		return ruleSet
	}
	return defaultRuleSet
}

func applyStrictRules(ruleSet *DiagnosticRuleSet) {
	overrideRules(ruleSet, GetStrictDiagnosticRuleSet(), GetStrictModeNotOverriddenRules())
}

func applyStandardRules(ruleSet *DiagnosticRuleSet) {
	overwriteRules(ruleSet, GetStandardDiagnosticRuleSet())
}

func applyBasicRules(ruleSet *DiagnosticRuleSet) {
	overwriteRules(ruleSet, GetBasicDiagnosticRuleSet())
}

func overrideRules(
	ruleSet *DiagnosticRuleSet,
	overrideRuleSet *DiagnosticRuleSet,
	skipRuleNames []DiagnosticRule,
) {
	// The TypeScript defaults includeNonOverridable to false.
	boolRuleNames := GetBooleanDiagnosticRules(false)
	diagRuleNames := GetDiagLevelDiagnosticRules()

	skip := func(ruleName DiagnosticRule) bool {
		for _, r := range skipRuleNames {
			if r == ruleName {
				return true
			}
		}
		return false
	}

	// Enable the strict rules as appropriate.
	for _, ruleName := range boolRuleNames {
		if skip(ruleName) {
			continue
		}

		field := diagnosticRuleBoolFields[ruleName]
		if *field(overrideRuleSet) {
			*field(ruleSet) = true
		}
	}

	for _, ruleName := range diagRuleNames {
		if skip(ruleName) {
			continue
		}

		field := diagnosticRuleLevelFields[ruleName]
		overrideValue := *field(overrideRuleSet)
		prevValue := *field(ruleSet)

		// Override only if the new value is more strict than the existing value.
		if overrideValue == DiagnosticLevelError ||
			(overrideValue == DiagnosticLevelWarning && prevValue != DiagnosticLevelError) ||
			(overrideValue == DiagnosticLevelInformation &&
				prevValue != DiagnosticLevelError && prevValue != DiagnosticLevelWarning) {
			*field(ruleSet) = overrideValue
		}
	}
}

func overwriteRules(ruleSet *DiagnosticRuleSet, overrideRuleSet *DiagnosticRuleSet) {
	boolRuleNames := GetBooleanDiagnosticRules(false)
	diagRuleNames := GetDiagLevelDiagnosticRules()

	for _, ruleName := range boolRuleNames {
		field := diagnosticRuleBoolFields[ruleName]
		*field(ruleSet) = *field(overrideRuleSet)
	}

	for _, ruleName := range diagRuleNames {
		field := diagnosticRuleLevelFields[ruleName]
		*field(ruleSet) = *field(overrideRuleSet)
	}
}

func parsePyrightComment(
	commentValue common.Text,
	commentRange common.TextRange,
	isCommentOnOwnLine func() bool,
	ruleSet *DiagnosticRuleSet,
	diagnostics *[]CommentDiagnostic,
) *DiagnosticRuleSet {
	// Is this a pyright comment?
	const commentPrefix = "pyright:"
	if commentValue.HasPrefixString(commentPrefix) {
		operands := commentValue.Slice(len(commentPrefix))

		// Handle (actual ignore) "ignore" directives.
		if operands.Trim().HasPrefixString("ignore") {
			return ruleSet
		}

		if !isCommentOnOwnLine() {
			diagAddendum := common.NewDiagnosticAddendum()
			diagAddendum.AddMessage(localization.LocAddendum.PyrightCommentIgnoreTip())
			*diagnostics = append(*diagnostics, CommentDiagnostic{
				Message: localization.LocMessage.PyrightCommentNotOnOwnLine() + diagAddendum.GetString(),
				Range:   commentRange,
			})
		}

		operandList := operands.SplitByChar(',')

		// If it contains a "strict" operand, replace the existing
		// diagnostic rules with their strict counterparts.
		if anyTrimmedEquals(operandList, strictSetting) {
			applyStrictRules(ruleSet)
		} else if anyTrimmedEquals(operandList, standardSetting) {
			applyStandardRules(ruleSet)
		} else if anyTrimmedEquals(operandList, basicSetting) {
			applyBasicRules(ruleSet)
		}

		rangeOffset := 0
		for _, operand := range operandList {
			trimmedOperand, operandRange := trimTextWithRange(operand, common.TextRange{
				Start:  commentRange.Start + len(commentPrefix) + rangeOffset,
				Length: operand.Length(),
			})

			ruleSet = parsePyrightOperand(trimmedOperand, operandRange, ruleSet, diagnostics)
			rangeOffset += operand.Length() + 1
		}
	}

	return ruleSet
}

// anyTrimmedEquals corresponds to `operandList.some((s) => s.trim() === x)`.
func anyTrimmedEquals(operandList []common.Text, value string) bool {
	for _, operand := range operandList {
		if operand.Trim().EqualString(value) {
			return true
		}
	}
	return false
}

func parsePyrightOperand(
	operand common.Text,
	operandRange common.TextRange,
	ruleSet *DiagnosticRuleSet,
	diagnostics *[]CommentDiagnostic,
) *DiagnosticRuleSet {
	operandSplit := operand.SplitByChar('=')
	trimmedRule, ruleRange := trimTextWithRange(operandSplit[0], common.TextRange{
		Start:  operandRange.Start,
		Length: operandSplit[0].Length(),
	})

	// Handle basic directives "basic", "standard" and "strict".
	if len(operandSplit) == 1 {
		if trimmedRule.Length() > 0 {
			for _, setting := range []string{strictSetting, standardSetting, basicSetting} {
				if trimmedRule.EqualString(setting) {
					return ruleSet
				}
			}
		}
	}

	// The original's `ruleValue` guard, `operandSplit.length > 0 ? ... : ''`, is
	// always taken: String.prototype.split never returns an empty array.
	ruleValue := common.JoinText(operandSplit[1:], "=")
	trimmedRuleValue, ruleValueRange := trimTextWithRange(ruleValue, common.TextRange{
		Start:  operandRange.Start + operandSplit[0].Length() + 1,
		Length: ruleValue.Length(),
	})

	// A rule name carrying an unpaired surrogate cannot match any rule either
	// way, so going through the UTF-8 form for the map lookup is safe here.
	ruleName := trimmedRule.String()
	ruleValueString := trimmedRuleValue.String()

	// The lookups replace `diagLevelRules.find(...)` and `boolRules.find(...)`;
	// the generated maps hold exactly the rules those two lists contain, plus
	// the two non-overridable bool rules, which the original's boolRules list
	// omits.
	if _, isDiagLevelRule := diagnosticRuleLevelFields[ruleName]; isDiagLevelRule {
		diagLevelValue, ok := parseDiagLevel(ruleValueString)
		if ok {
			*diagnosticRuleLevelFields[ruleName](ruleSet) = diagLevelValue
		} else {
			*diagnostics = append(*diagnostics, CommentDiagnostic{
				Message: localization.LocMessage.PyrightCommentInvalidDiagnosticSeverityValue(),
				Range:   pickRange(trimmedRuleValue, ruleValueRange, ruleRange),
			})
		}
	} else if isOverridableBoolRule(ruleName) {
		boolValue, ok := parseBoolSetting(ruleValueString)
		if ok {
			*diagnosticRuleBoolFields[ruleName](ruleSet) = boolValue
		} else {
			*diagnostics = append(*diagnostics, CommentDiagnostic{
				Message: localization.LocMessage.PyrightCommentInvalidDiagnosticBoolValue(),
				Range:   pickRange(trimmedRuleValue, ruleValueRange, ruleRange),
			})
		}
	} else if trimmedRule.Length() > 0 {
		message := localization.LocMessage.PyrightCommentUnknownDirective().Format(ruleName)
		if trimmedRuleValue.Length() > 0 {
			message = localization.LocMessage.PyrightCommentUnknownDiagnosticRule().Format(ruleName)
		}
		*diagnostics = append(*diagnostics, CommentDiagnostic{
			Message: message,
			Range:   ruleRange,
		})
	} else {
		*diagnostics = append(*diagnostics, CommentDiagnostic{
			Message: localization.LocMessage.PyrightCommentMissingDirective(),
			Range:   ruleRange,
		})
	}

	return ruleSet
}

// isOverridableBoolRule corresponds to `boolRules.find((r) => r === rule)`,
// where boolRules is getBooleanDiagnosticRules() with includeNonOverridable
// left at its default of false. The generated map holds every bool rule, so
// the two non-overridable ones have to be excluded explicitly -- a pyright
// comment cannot set enableTypeIgnoreComments or enableReachabilityAnalysis.
func isOverridableBoolRule(ruleName DiagnosticRule) bool {
	for _, r := range GetBooleanDiagnosticRules(false) {
		if r == ruleName {
			return true
		}
	}
	return false
}

// pickRange corresponds to `trimmedRuleValue ? ruleValueRange : ruleRange`,
// where the condition is JavaScript string truthiness: an empty value picks the
// rule's own range.
func pickRange(trimmedRuleValue common.Text, ruleValueRange, ruleRange common.TextRange) common.TextRange {
	if trimmedRuleValue.Length() > 0 {
		return ruleValueRange
	}
	return ruleRange
}

func parseDiagLevel(value string) (DiagnosticLevel, bool) {
	switch value {
	case "false", "none":
		return DiagnosticLevelNone, true
	case "true", "error":
		return DiagnosticLevelError, true
	case "warning":
		return DiagnosticLevelWarning, true
	case "information":
		return DiagnosticLevelInformation, true
	default:
		return "", false
	}
}

func parseBoolSetting(value string) (bool, bool) {
	if value == "false" {
		return false, true
	} else if value == "true" {
		return true, true
	}

	return false, false
}

// trimTextWithRange calls "trim" on the text and adjusts the corresponding
// range if characters are trimmed from the beginning or end.
func trimTextWithRange(text common.Text, r common.TextRange) (common.Text, common.TextRange) {
	assert(text.Length() == r.Length, "")
	value1 := text.TrimStart()

	updatedRange := r

	if value1.Length() != text.Length() {
		delta := text.Length() - value1.Length()
		updatedRange = common.TextRange{Start: updatedRange.Start + delta, Length: updatedRange.Length - delta}
	}

	value2 := value1.TrimEnd()
	if value2.Length() != value1.Length() {
		updatedRange = common.TextRange{
			Start:  updatedRange.Start,
			Length: updatedRange.Length - value1.Length() + value2.Length(),
		}
	}

	assert(value2.Length() == updatedRange.Length, "")
	return value2, updatedRange
}
