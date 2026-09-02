/*
 * sourcefile_diagnostics.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * _recomputeDiagnostics and _addTaskListDiagnostics from
 * analyzer/sourceFile.ts (pyright 1.1.412). See sourcefile.go for the rest.
 *
 * _recomputeDiagnostics is where every `# type: ignore` and `# pyright: ignore`
 * comment is applied, and where the "this ignore comment was unnecessary"
 * diagnostics come from. The two ignore maps are cloned first and entries are
 * *removed* as they are used, so whatever is left over is unused -- that is the
 * mechanism, and it is why the clones exist.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// recomputeDiagnostics computes an updated set of accumulated diagnostics for
// the file based on the partial diagnostics from various analysis stages.
func (s *SourceFile) recomputeDiagnostics(configOptions *ConfigOptions) {
	s.writableData.DiagnosticVersion++

	includeWarningsAndErrors := true

	// The original's comment: if a file was imported as a third-party file,
	// don't report any errors for it. The user can't fix them anyway.
	if s.isThirdPartyImport {
		includeWarningsAndErrors = false
	}

	diagList := []*common.Diagnostic{}
	diagList = append(diagList, s.writableData.ParseDiagnostics...)
	diagList = append(diagList, s.writableData.CommentDiagnostics...)
	diagList = append(diagList, s.writableData.BindDiagnostics...)
	diagList = append(diagList, s.writableData.CheckerDiagnostics...)
	diagList = append(diagList, s.writableData.TaskListDiagnostics...)

	prefilteredDiagList := diagList
	typeIgnoreLinesClone := cloneIgnoreComments(s.writableData.TypeIgnoreLines)
	pyrightIgnoreLinesClone := cloneIgnoreComments(s.writableData.PyrightIgnoreLines)

	// Filter the diagnostics based on "type: ignore" lines.
	if s.diagnosticRuleSet.EnableTypeIgnoreComments {
		if len(s.writableData.TypeIgnoreLines) > 0 {
			diagList = filterDiagnostics(diagList, func(d *common.Diagnostic) bool {
				if !isSuppressionExemptCategory(d.Category) {
					for line := d.Range.Start.Line; line <= d.Range.End.Line; line++ {
						if _, ok := s.writableData.TypeIgnoreLines[line]; ok {
							delete(typeIgnoreLinesClone, line)
							return false
						}
					}
				}

				return true
			})
		}
	}

	// Filter the diagnostics based on "pyright: ignore" lines.
	if len(s.writableData.PyrightIgnoreLines) > 0 {
		diagList = filterDiagnostics(diagList, func(d *common.Diagnostic) bool {
			if !isSuppressionExemptCategory(d.Category) {
				for line := d.Range.Start.Line; line <= d.Range.End.Line; line++ {
					pyrightIgnoreComment, ok := s.writableData.PyrightIgnoreLines[line]
					if !ok {
						continue
					}

					if pyrightIgnoreComment.RulesList == nil {
						delete(pyrightIgnoreLinesClone, line)
						return false
					}

					diagRulePtr := d.GetRule()
					if diagRulePtr == nil {
						// The original's comment: if there's no diagnostic
						// rule, it won't match against a rules list.
						return true
					}
					diagRule := *diagRulePtr

					// Did we find this rule in the list?
					found := false
					for _, rule := range pyrightIgnoreComment.RulesList {
						if rule.Text.String() == diagRule {
							found = true
							break
						}
					}
					if found {
						// Update the clone to remove this rule.
						if oldClone, ok := pyrightIgnoreLinesClone[line]; ok && oldClone.RulesList != nil {
							filteredRulesList := []parser.IgnoreCommentRule{}
							for _, rule := range oldClone.RulesList {
								if rule.Text.String() != diagRule {
									filteredRulesList = append(filteredRulesList, rule)
								}
							}
							if len(filteredRulesList) == 0 {
								delete(pyrightIgnoreLinesClone, line)
							} else {
								pyrightIgnoreLinesClone[line] = &parser.IgnoreComment{
									Range:     oldClone.Range,
									RulesList: filteredRulesList,
								}
							}
						}

						return false
					}

					return true
				}
			}

			return true
		})
	}

	unnecessaryTypeIgnoreDiags := []*common.Diagnostic{}

	// The original's comment: skip this step if type checking is needed.
	// Otherwise we'll likely produce incorrect (false positive)
	// reportUnnecessaryTypeIgnoreComment diagnostics until checking is
	// performed on this file.
	if s.diagnosticRuleSet.ReportUnnecessaryTypeIgnoreComment != DiagnosticLevelNone && !s.writableData.IsCheckingNeeded {
		diagCategory := common.ConvertLevelToCategory(s.diagnosticRuleSet.ReportUnnecessaryTypeIgnoreComment)

		prefilteredErrorList := []*common.Diagnostic{}
		for _, diag := range prefilteredDiagList {
			if diag.Category == common.DiagnosticCategoryError ||
				diag.Category == common.DiagnosticCategoryWarning ||
				diag.Category == common.DiagnosticCategoryInformation {
				prefilteredErrorList = append(prefilteredErrorList, diag)
			}
		}

		isUnreachableCodeRange := func(r common.Range) bool {
			for _, diag := range prefilteredDiagList {
				if diag.Category == common.DiagnosticCategoryUnreachableCode &&
					diag.Range.Start.Line <= r.Start.Line &&
					diag.Range.End.Line >= r.End.Line {
					return true
				}
			}
			return false
		}

		addUnnecessary := func(r common.Range, message string) {
			diag := common.NewDiagnostic(diagCategory, message, r)
			diag.SetRule(DiagnosticRuleReportUnnecessaryTypeIgnoreComment)
			unnecessaryTypeIgnoreDiags = append(unnecessaryTypeIgnoreDiags, diag)
		}

		if len(prefilteredErrorList) == 0 && s.writableData.TypeIgnoreAll != nil {
			rangeStart := s.writableData.TypeIgnoreAll.Range.Start
			rangeEnd := rangeStart + s.writableData.TypeIgnoreAll.Range.Length
			r := common.ConvertOffsetsToRange(rangeStart, rangeEnd, s.writableData.TokenizerLines)

			if !isUnreachableCodeRange(r) && s.diagnosticRuleSet.EnableTypeIgnoreComments {
				addUnnecessary(r, localization.LocMessage.UnnecessaryTypeIgnore())
			}
		}

		// The two clones are iterated in line order rather than in map order,
		// so the diagnostics come out in a stable sequence. The original
		// iterates a JavaScript Map, which is in insertion order; the tokenizer
		// fills both in line order, so the two agree.
		for _, line := range sortedIgnoreLines(typeIgnoreLinesClone) {
			ignoreComment := typeIgnoreLinesClone[line]
			if s.writableData.TokenizerLines == nil {
				continue
			}
			rangeStart := ignoreComment.Range.Start
			rangeEnd := rangeStart + ignoreComment.Range.Length
			r := common.ConvertOffsetsToRange(rangeStart, rangeEnd, s.writableData.TokenizerLines)

			if !isUnreachableCodeRange(r) && s.diagnosticRuleSet.EnableTypeIgnoreComments {
				addUnnecessary(r, localization.LocMessage.UnnecessaryTypeIgnore())
			}
		}

		for _, line := range sortedIgnoreLines(pyrightIgnoreLinesClone) {
			ignoreComment := pyrightIgnoreLinesClone[line]
			if s.writableData.TokenizerLines == nil {
				continue
			}

			if ignoreComment.RulesList == nil {
				rangeStart := ignoreComment.Range.Start
				rangeEnd := rangeStart + ignoreComment.Range.Length
				r := common.ConvertOffsetsToRange(rangeStart, rangeEnd, s.writableData.TokenizerLines)

				if !isUnreachableCodeRange(r) {
					addUnnecessary(r, localization.LocMessage.UnnecessaryTypeIgnore())
				}
				continue
			}

			for _, unusedRule := range ignoreComment.RulesList {
				rangeStart := unusedRule.Range.Start
				rangeEnd := rangeStart + unusedRule.Range.Length
				r := common.ConvertOffsetsToRange(rangeStart, rangeEnd, s.writableData.TokenizerLines)

				if !isUnreachableCodeRange(r) {
					addUnnecessary(r, localization.LocMessage.UnnecessaryPyrightIgnoreRule().Format(unusedRule.Text.String()))
				}
			}
		}
	}

	if s.diagnosticRuleSet.ReportImportCycles != DiagnosticLevelNone && len(s.writableData.CircularDependencies) > 0 {
		category := common.ConvertLevelToCategory(s.diagnosticRuleSet.ReportImportCycles)

		for _, cirDep := range s.writableData.CircularDependencies {
			paths := []string{}
			for _, path := range cirDep.GetPaths() {
				paths = append(paths, "  "+path.ToUserVisibleString())
			}

			diag := common.NewDiagnostic(
				category,
				localization.LocMessage.ImportCycleDetected()+"\n"+strings.Join(paths, "\n"),
				common.GetEmptyRange(),
			)
			diag.SetRule(DiagnosticRuleReportImportCycles)
			diagList = append(diagList, diag)
		}
	}

	if s.writableData.HitMaxImportDepth != nil {
		diagList = append(diagList, common.NewDiagnostic(
			common.DiagnosticCategoryError,
			localization.LocMessage.ImportDepthExceeded().Format(*s.writableData.HitMaxImportDepth),
			common.GetEmptyRange(),
		))
	}

	// The original's comment: if there is a "type: ignore" comment at the top of
	// the file, clear the diagnostic list of all error, warning, and
	// information diagnostics.
	if s.diagnosticRuleSet.EnableTypeIgnoreComments {
		if s.writableData.TypeIgnoreAll != nil {
			diagList = filterDiagnostics(diagList, func(diag *common.Diagnostic) bool {
				return diag.Category != common.DiagnosticCategoryError &&
					diag.Category != common.DiagnosticCategoryWarning &&
					diag.Category != common.DiagnosticCategoryInformation
			})
		}
	}

	// Now add in the "unnecessary type ignore" diagnostics.
	diagList = append(diagList, unnecessaryTypeIgnoreDiags...)

	// The original's comment: if we're not returning any diagnostics, filter out
	// all of the errors and warnings, leaving only the unreachable code and
	// deprecated diagnostics.
	if !includeWarningsAndErrors {
		diagList = filterDiagnostics(diagList, func(diag *common.Diagnostic) bool {
			return diag.Category == common.DiagnosticCategoryUnusedCode ||
				diag.Category == common.DiagnosticCategoryUnreachableCode ||
				diag.Category == common.DiagnosticCategoryDeprecated
		})
	}

	// The original's comment: capture the fully-filtered diagnostics before any
	// file-level ignore clears them, so consumers that need the unsuppressed
	// list (e.g. ignored-file quick fixes) can access it. For non-ignored files
	// this is the same array reference as accumulatedDiagnostics below.
	s.writableData.DiagnosticsWithoutFileIgnore = diagList

	// If the file is in the ignore list, clear the diagnostic list.
	for _, ignoreFileSpec := range configOptions.Ignore {
		if s.uri.MatchesRegex(ignoreFileSpec.RegExp) {
			diagList = []*common.Diagnostic{}
			break
		}
	}

	s.writableData.AccumulatedDiagnostics = diagList
}

// isSuppressionExemptCategory is the three-category test both ignore filters
// make: an unused-code, unreachable-code or deprecated diagnostic is never
// suppressed by an ignore comment.
func isSuppressionExemptCategory(category common.DiagnosticCategory) bool {
	return category == common.DiagnosticCategoryUnusedCode ||
		category == common.DiagnosticCategoryUnreachableCode ||
		category == common.DiagnosticCategoryDeprecated
}

func filterDiagnostics(diagList []*common.Diagnostic, keep func(*common.Diagnostic) bool) []*common.Diagnostic {
	out := []*common.Diagnostic{}
	for _, d := range diagList {
		if keep(d) {
			out = append(out, d)
		}
	}
	return out
}

func cloneIgnoreComments(m map[int]*parser.IgnoreComment) map[int]*parser.IgnoreComment {
	out := make(map[int]*parser.IgnoreComment, len(m))
	for line, comment := range m {
		out[line] = comment
	}
	return out
}

// sortedIgnoreLines gives the clones a deterministic iteration order; see the
// note at the call site.
func sortedIgnoreLines(m map[int]*parser.IgnoreComment) []int {
	lines := make([]int, 0, len(m))
	for line := range m {
		lines = append(lines, line)
	}
	for i := 1; i < len(lines); i++ {
		for j := i; j > 0 && lines[j] < lines[j-1]; j-- {
			lines[j], lines[j-1] = lines[j-1], lines[j]
		}
	}
	return lines
}

// addTaskListDiagnostics gets all task list diagnostics for the current file
// and adds them to the specified diagnostic list.
func (s *SourceFile) addTaskListDiagnostics(
	taskListTokens []common.TaskListToken,
	parseFileResults *parser.ParseFileResults,
	diagList *[]*common.Diagnostic,
) {
	if len(taskListTokens) == 0 || diagList == nil {
		return
	}

	tokenizerOutput := parseFileResults.TokenizerOutput
	fileContents := parseFileResults.Text

	for i := 0; i < tokenizerOutput.Tokens.Count(); i++ {
		token := tokenizerOutput.Tokens.GetItemAt(i)

		// If there are no comments, skip this token.
		comments := token.GetComments()
		if len(comments) == 0 {
			continue
		}

		for _, comment := range comments {
			for _, taskToken := range taskListTokens {
				// The original's comment: match optional leading whitespace,
				// then taskToken.text (case-insensitive), then either
				// (whitespace to end) or (non-alphanumeric char).
				commentStart := comment.Start
				commentEnd := commentStart + comment.Length
				taskText := taskToken.Text
				taskLen := len(taskText)

				// Skip leading whitespace within the source text range.
				pos := commentStart
				for pos < commentEnd {
					ch := fileContents.CharCodeAt(pos)
					if ch == 0x20 || ch == 0x09 || ch == 0x0a || ch == 0x0d || ch == 0x0c || ch == 0x0b {
						pos++
					} else {
						break
					}
				}

				// Check if the task token text matches (case-insensitive).
				if pos+taskLen > commentEnd {
					continue
				}

				matched := true
				for k := 0; k < taskLen; k++ {
					a := uint16(fileContents.CharCodeAt(pos + k))
					b := uint16(taskText[k])
					if a != b && (a|0x20) != (b|0x20) {
						matched = false
						break
					}
				}
				if !matched {
					continue
				}

				// After the token, require whitespace-to-end or a non-word
				// character.
				afterPos := pos + taskLen
				if afterPos < commentEnd {
					ch := fileContents.CharCodeAt(afterPos)
					isWord := (ch >= 0x61 && ch <= 0x7a) ||
						(ch >= 0x41 && ch <= 0x5a) ||
						(ch >= 0x30 && ch <= 0x39) ||
						ch == 0x5f
					if isWord {
						continue
					}
				}

				// Match succeeded. pos is the offset of the task token in the
				// source text.
				rangeEnd := comment.Start + comment.Length
				r := common.ConvertOffsetsToRange(pos, rangeEnd, tokenizerOutput.Lines)

				*diagList = append(*diagList, common.NewDiagnosticWithPriority(
					common.DiagnosticCategoryTaskItem,
					common.TrimJSString(comment.Value.String()),
					r,
					taskToken.Priority,
				))
			}
		}
	}
}
