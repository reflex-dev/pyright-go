/*
 * diagnosticsink.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Class that collects and deduplicates diagnostics.
 *
 * Transliterated from common/diagnosticSink.ts (pyright 1.1.412).
 */

package common

import (
	"fmt"
	"strconv"
)

// FileDiagnostics represents a collection of diagnostics within a file.
type FileDiagnostics struct {
	FileUri     Uri
	Version     *int
	Diagnostics []*Diagnostic
}

// DiagnosticSink creates and tracks a list of diagnostics.
type DiagnosticSink struct {
	diagnosticList []*Diagnostic
	diagnosticMap  map[string]*Diagnostic
}

// NewDiagnosticSink constructs an empty sink.
func NewDiagnosticSink() *DiagnosticSink {
	return NewDiagnosticSinkFrom(nil)
}

// NewDiagnosticSinkFrom constructs a sink seeded with existing diagnostics.
func NewDiagnosticSinkFrom(diagnostics []*Diagnostic) *DiagnosticSink {
	if diagnostics == nil {
		diagnostics = []*Diagnostic{}
	}
	return &DiagnosticSink{
		diagnosticList: diagnostics,
		diagnosticMap:  map[string]*Diagnostic{},
	}
}

// FetchAndClear corresponds to fetchAndClear().
func (s *DiagnosticSink) FetchAndClear() []*Diagnostic {
	prevDiagnostics := s.diagnosticList
	s.diagnosticList = []*Diagnostic{}
	s.diagnosticMap = map[string]*Diagnostic{}
	return prevDiagnostics
}

// AddError corresponds to addError().
func (s *DiagnosticSink) AddError(message string, r Range) *Diagnostic {
	return s.AddDiagnostic(NewDiagnostic(DiagnosticCategoryError, message, r))
}

// AddWarning corresponds to addWarning().
func (s *DiagnosticSink) AddWarning(message string, r Range) *Diagnostic {
	return s.AddDiagnostic(NewDiagnostic(DiagnosticCategoryWarning, message, r))
}

// AddInformation corresponds to addInformation().
func (s *DiagnosticSink) AddInformation(message string, r Range) *Diagnostic {
	return s.AddDiagnostic(NewDiagnostic(DiagnosticCategoryInformation, message, r))
}

// AddUnusedCode corresponds to addUnusedCode(). Pass nil for action to omit it.
func (s *DiagnosticSink) AddUnusedCode(message string, r Range, action DiagnosticAction) *Diagnostic {
	diag := NewDiagnostic(DiagnosticCategoryUnusedCode, message, r)
	if action != nil {
		diag.AddAction(action)
	}
	return s.AddDiagnostic(diag)
}

// AddUnreachableCode corresponds to addUnreachableCode().
func (s *DiagnosticSink) AddUnreachableCode(message string, r Range, action DiagnosticAction) *Diagnostic {
	diag := NewDiagnostic(DiagnosticCategoryUnreachableCode, message, r)
	if action != nil {
		diag.AddAction(action)
	}
	return s.AddDiagnostic(diag)
}

// AddDeprecated corresponds to addDeprecated().
func (s *DiagnosticSink) AddDeprecated(message string, r Range, action DiagnosticAction) *Diagnostic {
	diag := NewDiagnostic(DiagnosticCategoryDeprecated, message, r)
	if action != nil {
		diag.AddAction(action)
	}
	return s.AddDiagnostic(diag)
}

// AddDiagnostic corresponds to addDiagnostic().
func (s *DiagnosticSink) AddDiagnostic(diag *Diagnostic) *Diagnostic {
	// Create a unique key for the diagnostic to prevent
	// adding duplicates.
	key := strconv.Itoa(diag.Range.Start.Line) + "," + strconv.Itoa(diag.Range.Start.Character) + "-" +
		strconv.Itoa(diag.Range.End.Line) + "-" + strconv.Itoa(diag.Range.End.Character) + ":" +
		strconv.Itoa(int(HashString(diag.Message))) + "}"
	if _, ok := s.diagnosticMap[key]; !ok {
		s.diagnosticList = append(s.diagnosticList, diag)
		s.diagnosticMap[key] = diag
	}
	return diag
}

// AddDiagnostics corresponds to addDiagnostics().
func (s *DiagnosticSink) AddDiagnostics(diagsToAdd []*Diagnostic) {
	s.diagnosticList = append(s.diagnosticList, diagsToAdd...)
}

func (s *DiagnosticSink) filter(category DiagnosticCategory) []*Diagnostic {
	result := []*Diagnostic{}
	for _, diag := range s.diagnosticList {
		if diag.Category == category {
			result = append(result, diag)
		}
	}
	return result
}

// GetErrors corresponds to getErrors().
func (s *DiagnosticSink) GetErrors() []*Diagnostic { return s.filter(DiagnosticCategoryError) }

// GetWarnings corresponds to getWarnings().
func (s *DiagnosticSink) GetWarnings() []*Diagnostic { return s.filter(DiagnosticCategoryWarning) }

// GetInformation corresponds to getInformation().
func (s *DiagnosticSink) GetInformation() []*Diagnostic {
	return s.filter(DiagnosticCategoryInformation)
}

// GetUnusedCode corresponds to getUnusedCode().
func (s *DiagnosticSink) GetUnusedCode() []*Diagnostic { return s.filter(DiagnosticCategoryUnusedCode) }

// GetUnreachableCode corresponds to getUnreachableCode().
func (s *DiagnosticSink) GetUnreachableCode() []*Diagnostic {
	return s.filter(DiagnosticCategoryUnreachableCode)
}

// GetDeprecated corresponds to getDeprecated().
func (s *DiagnosticSink) GetDeprecated() []*Diagnostic { return s.filter(DiagnosticCategoryDeprecated) }

// TextRangeDiagnosticSink is a specialized version of DiagnosticSink that
// works with TextRange objects and converts text ranges to line and column
// numbers.
type TextRangeDiagnosticSink struct {
	DiagnosticSink
	lines *TextRangeCollection[TextRange]
}

// NewTextRangeDiagnosticSink constructs a sink over the given line collection.
func NewTextRangeDiagnosticSink(lines *TextRangeCollection[TextRange]) *TextRangeDiagnosticSink {
	return NewTextRangeDiagnosticSinkFrom(lines, nil)
}

// NewTextRangeDiagnosticSinkFrom constructs a sink seeded with existing
// diagnostics.
func NewTextRangeDiagnosticSinkFrom(lines *TextRangeCollection[TextRange], diagnostics []*Diagnostic) *TextRangeDiagnosticSink {
	if diagnostics == nil {
		diagnostics = []*Diagnostic{}
	}
	return &TextRangeDiagnosticSink{
		DiagnosticSink: DiagnosticSink{
			diagnosticList: diagnostics,
			diagnosticMap:  map[string]*Diagnostic{},
		},
		lines: lines,
	}
}

// AddDiagnosticWithTextRange corresponds to addDiagnosticWithTextRange().
func (s *TextRangeDiagnosticSink) AddDiagnosticWithTextRange(level DiagnosticLevel, message string, r TextRange) *Diagnostic {
	positionRange := ConvertOffsetsToRange(r.Start, r.Start+r.Length, s.lines)
	switch level {
	case DiagnosticLevelError:
		return s.AddError(message, positionRange)

	case DiagnosticLevelWarning:
		return s.AddWarning(message, positionRange)

	case DiagnosticLevelInformation:
		return s.AddInformation(message, positionRange)

	default:
		panic(fmt.Sprintf("%s is not expected value", level))
	}
}

// AddUnusedCodeWithTextRange corresponds to addUnusedCodeWithTextRange().
func (s *TextRangeDiagnosticSink) AddUnusedCodeWithTextRange(message string, r TextRange, action DiagnosticAction) *Diagnostic {
	return s.AddUnusedCode(message, ConvertOffsetsToRange(r.Start, r.Start+r.Length, s.lines), action)
}

// AddUnreachableCodeWithTextRange corresponds to
// addUnreachableCodeWithTextRange().
func (s *TextRangeDiagnosticSink) AddUnreachableCodeWithTextRange(message string, r TextRange, action DiagnosticAction) *Diagnostic {
	return s.AddUnreachableCode(message, ConvertOffsetsToRange(r.Start, r.Start+r.Length, s.lines), action)
}

// AddDeprecatedWithTextRange corresponds to addDeprecatedWithTextRange().
func (s *TextRangeDiagnosticSink) AddDeprecatedWithTextRange(message string, r TextRange, action DiagnosticAction) *Diagnostic {
	return s.AddDeprecated(message, ConvertOffsetsToRange(r.Start, r.Start+r.Length, s.lines), action)
}
