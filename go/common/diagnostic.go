/*
 * diagnostic.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Class that represents errors and warnings.
 *
 * Transliterated from common/diagnostic.ts (pyright 1.1.412).
 *
 * The JSON round-trip helpers (toJsonObj/fromJsonObj) exist to move
 * diagnostics across the Node worker-thread boundary and have no counterpart
 * in this port; they are omitted.
 */

package common

import (
	"fmt"
	"strings"
)

const (
	// DefaultMaxDiagnosticDepth corresponds to defaultMaxDiagnosticDepth.
	DefaultMaxDiagnosticDepth = 5
	// DefaultMaxDiagnosticLineCount corresponds to defaultMaxDiagnosticLineCount.
	DefaultMaxDiagnosticLineCount = 8

	maxRecursionCount = 64
)

// Uri stands in for common/uri/uri.ts, which the tokenizer/parser subset does
// not exercise. Only the identity that DiagnosticRelatedInfo needs is modeled;
// the full Uri port is not yet done.
type Uri interface {
	Key() string
	String() string
}

// TaskListPriority corresponds to the CommentTaskPriority enum.
type TaskListPriority = string

const (
	TaskListPriorityHigh   TaskListPriority = "High"
	TaskListPriorityNormal TaskListPriority = "Normal"
	TaskListPriorityLow    TaskListPriority = "Low"
)

// TaskListToken corresponds to the TaskListToken interface.
type TaskListToken struct {
	Text     string
	Priority TaskListPriority
}

// DiagnosticCategory corresponds to the DiagnosticCategory const enum.
type DiagnosticCategory int

const (
	DiagnosticCategoryError DiagnosticCategory = iota
	DiagnosticCategoryWarning
	DiagnosticCategoryInformation
	DiagnosticCategoryUnusedCode
	DiagnosticCategoryUnreachableCode
	DiagnosticCategoryDeprecated
	DiagnosticCategoryTaskItem
)

// DiagnosticLevel corresponds to the DiagnosticLevel union in configOptions.ts.
type DiagnosticLevel = string

const (
	DiagnosticLevelNone        DiagnosticLevel = "none"
	DiagnosticLevelInformation DiagnosticLevel = "information"
	DiagnosticLevelWarning     DiagnosticLevel = "warning"
	DiagnosticLevelError       DiagnosticLevel = "error"
)

// ConvertLevelToCategory corresponds to convertLevelToCategory(). It panics for
// unexpected levels, matching the `throw new Error` in TypeScript.
func ConvertLevelToCategory(level DiagnosticLevel) DiagnosticCategory {
	switch level {
	case DiagnosticLevelError:
		return DiagnosticCategoryError

	case DiagnosticLevelWarning:
		return DiagnosticCategoryWarning

	case DiagnosticLevelInformation:
		return DiagnosticCategoryInformation

	default:
		panic(fmt.Sprintf("%s is not expected", level))
	}
}

// DiagnosticAction corresponds to the DiagnosticAction interface.
type DiagnosticAction interface {
	ActionName() string
}

// DiagnosticWithinFile corresponds to the DiagnosticWithinFile interface.
type DiagnosticWithinFile struct {
	Uri        Uri
	Diagnostic *Diagnostic
}

// CreateTypeStubFileAction corresponds to the CreateTypeStubFileAction
// interface.
type CreateTypeStubFileAction struct {
	Action     string
	ModuleName string
}

// ActionName satisfies DiagnosticAction.
func (a *CreateTypeStubFileAction) ActionName() string { return a.Action }

// DiagnosticRelatedInfo corresponds to the DiagnosticRelatedInfo interface.
type DiagnosticRelatedInfo struct {
	Message  string
	Uri      Uri
	Range    Range
	Priority TaskListPriority
}

// Diagnostic represents a single error or warning.
type Diagnostic struct {
	Category DiagnosticCategory
	Message  string
	Range    Range
	Priority TaskListPriority

	actions     []DiagnosticAction
	rule        *string
	relatedInfo []DiagnosticRelatedInfo
	data        any
}

// NewDiagnostic constructs a Diagnostic with the default Normal priority.
func NewDiagnostic(category DiagnosticCategory, message string, r Range) *Diagnostic {
	return NewDiagnosticWithPriority(category, message, r, TaskListPriorityNormal)
}

// NewDiagnosticWithPriority constructs a Diagnostic with an explicit priority.
func NewDiagnosticWithPriority(category DiagnosticCategory, message string, r Range, priority TaskListPriority) *Diagnostic {
	return &Diagnostic{
		Category:    category,
		Message:     message,
		Range:       r,
		Priority:    priority,
		relatedInfo: []DiagnosticRelatedInfo{},
	}
}

// AddAction corresponds to addAction().
func (d *Diagnostic) AddAction(action DiagnosticAction) {
	d.actions = append(d.actions, action)
}

// SetData corresponds to setData().
func (d *Diagnostic) SetData(data any) {
	d.data = data
}

// GetData corresponds to getData().
func (d *Diagnostic) GetData() any {
	return d.data
}

// GetActions corresponds to getActions().
func (d *Diagnostic) GetActions() []DiagnosticAction {
	return d.actions
}

// SetRule corresponds to setRule().
func (d *Diagnostic) SetRule(rule string) {
	d.rule = &rule
}

// GetRule corresponds to getRule(). It returns nil when no rule is set.
func (d *Diagnostic) GetRule() *string {
	return d.rule
}

// AddRelatedInfo corresponds to addRelatedInfo() with the default priority.
func (d *Diagnostic) AddRelatedInfo(message string, fileUri Uri, r Range) {
	d.AddRelatedInfoWithPriority(message, fileUri, r, TaskListPriorityNormal)
}

// AddRelatedInfoWithPriority corresponds to addRelatedInfo().
func (d *Diagnostic) AddRelatedInfoWithPriority(message string, fileUri Uri, r Range, priority TaskListPriority) {
	d.relatedInfo = append(d.relatedInfo, DiagnosticRelatedInfo{
		Uri:      fileUri,
		Message:  message,
		Range:    r,
		Priority: priority,
	})
}

// GetRelatedInfo corresponds to getRelatedInfo().
func (d *Diagnostic) GetRelatedInfo() []DiagnosticRelatedInfo {
	return d.relatedInfo
}

// CompareDiagnostics compares two diagnostics by location for sorting.
func CompareDiagnostics(d1, d2 *Diagnostic) int {
	if d1.Range.Start.Line < d2.Range.Start.Line {
		return -1
	} else if d1.Range.Start.Line > d2.Range.Start.Line {
		return 1
	}

	if d1.Range.Start.Character < d2.Range.Start.Character {
		return -1
	} else if d1.Range.Start.Character > d2.Range.Start.Character {
		return 1
	}

	return 0
}

// DiagnosticAddendum helps to build additional information that can be
// appended to a diagnostic message. It supports hierarchical information and
// flexible formatting.
type DiagnosticAddendum struct {
	messages     []string
	childAddenda []*DiagnosticAddendum

	// The nest level is accurate only for the common case where all
	// addendum are created using CreateAddendum. This is an upper bound.
	// The actual nest level may be smaller.
	nestLevel *int

	// Addenda normally don't have their own ranges, but there are cases
	// where we want to track ranges that can influence the range of the
	// diagnostic.
	textRange *TextRange
}

// NewDiagnosticAddendum constructs an empty addendum.
func NewDiagnosticAddendum() *DiagnosticAddendum {
	return &DiagnosticAddendum{}
}

// AddMessage corresponds to addMessage().
func (a *DiagnosticAddendum) AddMessage(message string) {
	a.messages = append(a.messages, message)
}

// AddMessageMultiline corresponds to addMessageMultiline().
func (a *DiagnosticAddendum) AddMessageMultiline(message string) {
	for _, line := range strings.Split(message, "\n") {
		a.messages = append(a.messages, line)
	}
}

// AddTextRange corresponds to addTextRange().
func (a *DiagnosticAddendum) AddTextRange(r TextRange) {
	a.textRange = &r
}

// CreateAddendum creates a new (nested) addendum to which messages can be
// added.
func (a *DiagnosticAddendum) CreateAddendum() *DiagnosticAddendum {
	newAddendum := NewDiagnosticAddendum()
	level := a.GetNestLevel() + 1
	newAddendum.nestLevel = &level
	a.AddAddendum(newAddendum)
	return newAddendum
}

// GetString corresponds to getString() with the default depth and line count.
func (a *DiagnosticAddendum) GetString() string {
	return a.GetStringWithLimits(DefaultMaxDiagnosticDepth, DefaultMaxDiagnosticLineCount)
}

// GetStringWithLimits corresponds to getString().
func (a *DiagnosticAddendum) GetStringWithLimits(maxDepth, maxLineCount int) string {
	lines := a.getLinesRecursive(maxDepth, maxLineCount, 0)

	if len(lines) > maxLineCount {
		lines = append(append([]string{}, lines[:maxLineCount]...), "  ...")
	}

	text := strings.Join(lines, "\n")
	if len(text) > 0 {
		return "\n" + text
	}

	return ""
}

// IsEmpty corresponds to isEmpty().
func (a *DiagnosticAddendum) IsEmpty() bool {
	return a.getMessageCount(0) == 0
}

// AddAddendum corresponds to addAddendum().
func (a *DiagnosticAddendum) AddAddendum(addendum *DiagnosticAddendum) {
	a.childAddenda = append(a.childAddenda, addendum)
}

// GetChildren corresponds to getChildren().
func (a *DiagnosticAddendum) GetChildren() []*DiagnosticAddendum {
	return a.childAddenda
}

// GetMessages corresponds to getMessages().
func (a *DiagnosticAddendum) GetMessages() []string {
	return a.messages
}

// GetNestLevel corresponds to getNestLevel().
func (a *DiagnosticAddendum) GetNestLevel() int {
	if a.nestLevel == nil {
		return 0
	}
	return *a.nestLevel
}

// GetEffectiveTextRange returns nil if no range is associated with this
// addendum or its children. Returns a non-empty range if there is a single
// range associated.
func (a *DiagnosticAddendum) GetEffectiveTextRange() *TextRange {
	r := a.getTextRangeRecursive(0)

	// If we received an empty range, it means that there were multiple
	// non-overlapping ranges associated with this addendum.
	if r != nil && r.Length == 0 {
		return nil
	}

	return r
}

func (a *DiagnosticAddendum) getTextRangeRecursive(recursionCount int) *TextRange {
	if recursionCount > maxRecursionCount {
		return nil
	}
	recursionCount++

	var childRanges []*TextRange
	for _, child := range a.childAddenda {
		if r := child.getTextRangeRecursive(recursionCount); r != nil {
			childRanges = append(childRanges, r)
		}
	}

	if len(childRanges) > 1 {
		return &TextRange{Start: 0, Length: 0}
	}

	if len(childRanges) == 1 {
		return childRanges[0]
	}

	if a.textRange != nil {
		return a.textRange
	}

	return nil
}

func (a *DiagnosticAddendum) getMessageCount(recursionCount int) int {
	if recursionCount > maxRecursionCount {
		return 0
	}

	// Get the nested message count.
	messageCount := len(a.messages)

	for _, diag := range a.childAddenda {
		messageCount += diag.getMessageCount(recursionCount + 1)
	}

	return messageCount
}

func (a *DiagnosticAddendum) getLinesRecursive(maxDepth, maxLineCount, recursionCount int) []string {
	if maxDepth <= 0 || recursionCount > maxRecursionCount {
		return nil
	}

	var childLines []string
	for _, addendum := range a.childAddenda {
		maxDepthRemaining := maxDepth
		if len(a.messages) > 0 {
			maxDepthRemaining = maxDepth - 1
		}
		childLines = append(childLines, addendum.getLinesRecursive(maxDepthRemaining, maxLineCount, recursionCount+1)...)

		// If the number of lines exceeds our max line count, don't bother adding more.
		if len(childLines) >= maxLineCount {
			childLines = childLines[:maxLineCount]
			break
		}
	}

	// Prepend indentation for readability. Skip if there are no
	// messages at this level.
	//
	// Note that these are two non-breaking spaces (U+00A0), not ordinary
	// spaces; the original source has them written literally. Diagnostic text
	// is compared verbatim in places, so the distinction matters.
	extraSpace := ""
	if len(a.messages) > 0 {
		extraSpace = "  "
	}

	combined := make([]string, 0, len(a.messages)+len(childLines))
	combined = append(combined, a.messages...)
	combined = append(combined, childLines...)
	for i := range combined {
		combined[i] = extraSpace + combined[i]
	}
	return combined
}
