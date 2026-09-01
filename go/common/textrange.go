/*
 * textrange.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Specifies the range of text within a larger string.
 *
 * Transliterated from common/textRange.ts (pyright 1.1.412).
 */

package common

import "fmt"

// TextRange specifies a range of text within a larger string. Offsets are
// UTF-16 code unit offsets, matching the TypeScript sources.
type TextRange struct {
	Start  int
	Length int
}

// NewTextRange corresponds to TextRange.create().
func NewTextRange(start, length int) TextRange {
	if start < 0 {
		Fail("start must be non-negative")
	}
	if length < 0 {
		Fail("length must be non-negative")
	}
	return TextRange{Start: start, Length: length}
}

// TextRangeFromBounds corresponds to TextRange.fromBounds().
func TextRangeFromBounds(start, end int) TextRange {
	if start < 0 {
		Fail("start must be non-negative")
	}
	if start > end {
		Fail("end must be greater than or equal to start")
	}
	return NewTextRange(start, end-start)
}

// End corresponds to TextRange.getEnd().
func (r TextRange) End() int {
	return r.Start + r.Length
}

// GetRange satisfies RangeItem so that a bare TextRange can be stored in a
// TextRangeCollection, standing in for `T extends TextRange`.
func (r TextRange) GetRange() TextRange {
	return r
}

// Contains corresponds to TextRange.contains().
func (r TextRange) Contains(position int) bool {
	return position >= r.Start && position < r.End()
}

// ContainsRange corresponds to TextRange.containsRange().
func (r TextRange) ContainsRange(span TextRange) bool {
	return span.Start >= r.Start && span.End() <= r.End()
}

// Overlaps corresponds to TextRange.overlaps().
func (r TextRange) Overlaps(position int) bool {
	return position >= r.Start && position <= r.End()
}

// OverlapsRange corresponds to TextRange.overlapsRange().
func (r TextRange) OverlapsRange(other TextRange) bool {
	return r.Overlaps(other.Start) || other.Overlaps(r.Start)
}

// Extend corresponds to TextRange.extend().
func (r TextRange) Extend(extension TextRange) TextRange {
	result := r

	if extension.Start < result.Start {
		result = TextRange{
			Start:  extension.Start,
			Length: result.Length + result.Start - extension.Start,
		}
	}

	extensionEnd := extension.End()
	resultEnd := result.End()
	if extensionEnd > resultEnd {
		result = TextRange{
			Start:  result.Start,
			Length: result.Length + extensionEnd - resultEnd,
		}
	}

	return result
}

// CombineTextRanges corresponds to TextRange.combine(). It returns nil when
// ranges is empty, matching the `undefined` return in TypeScript.
func CombineTextRanges(ranges []TextRange) *TextRange {
	if len(ranges) == 0 {
		return nil
	}

	combined := TextRange{Start: ranges[0].Start, Length: ranges[0].Length}
	for i := 1; i < len(ranges); i++ {
		combined = combined.Extend(ranges[i])
	}
	return &combined
}

// Position is a zero-based line/character pair.
type Position struct {
	Line      int
	Character int
}

// String corresponds to Position.print().
func (p Position) String() string {
	return fmt.Sprintf("(%d:%d)", p.Line, p.Character)
}

// Range is a start/end pair of Positions.
type Range struct {
	Start Position
	End   Position
}

// String corresponds to Range.print().
func (r Range) String() string {
	return r.Start.String() + "-" + r.End.String()
}

// ComparePositions corresponds to comparePositions().
func ComparePositions(a, b Position) int {
	if a.Line < b.Line {
		return -1
	} else if a.Line > b.Line {
		return 1
	} else if a.Character < b.Character {
		return -1
	} else if a.Character > b.Character {
		return 1
	}
	return 0
}

// GetEmptyPosition corresponds to getEmptyPosition().
func GetEmptyPosition() Position {
	return Position{Line: 0, Character: 0}
}

// DoRangesOverlap corresponds to doRangesOverlap().
func DoRangesOverlap(a, b Range) bool {
	if ComparePositions(b.Start, a.End) >= 0 {
		return false
	} else if ComparePositions(a.Start, b.End) >= 0 {
		return false
	}
	return true
}

// DoRangesIntersect corresponds to doRangesIntersect().
func DoRangesIntersect(a, b Range) bool {
	if ComparePositions(b.Start, a.End) > 0 {
		return false
	} else if ComparePositions(a.Start, b.End) > 0 {
		return false
	}
	return true
}

// IsPositionInRange corresponds to isPositionInRange().
func IsPositionInRange(r Range, position Position) bool {
	return ComparePositions(r.Start, position) <= 0 && ComparePositions(r.End, position) >= 0
}

// IsRangeInRange corresponds to isRangeInRange().
func IsRangeInRange(r Range, containedRange Range) bool {
	return IsPositionInRange(r, containedRange.Start) && IsPositionInRange(r, containedRange.End)
}

// PositionsAreEqual corresponds to positionsAreEqual().
func PositionsAreEqual(a, b Position) bool {
	return ComparePositions(a, b) == 0
}

// RangesAreEqual corresponds to rangesAreEqual().
func RangesAreEqual(a, b Range) bool {
	return PositionsAreEqual(a.Start, b.Start) && PositionsAreEqual(a.End, b.End)
}

// GetEmptyRange corresponds to getEmptyRange().
func GetEmptyRange() Range {
	return Range{Start: GetEmptyPosition(), End: GetEmptyPosition()}
}

// IsEmptyPosition corresponds to isEmptyPosition().
func IsEmptyPosition(pos Position) bool {
	return pos.Character == 0 && pos.Line == 0
}

// IsEmptyRange corresponds to isEmptyRange().
func IsEmptyRange(r Range) bool {
	return IsEmptyPosition(r.Start) && IsEmptyPosition(r.End)
}

// ExtendRange corresponds to extendRange(). The TypeScript version mutates its
// first argument, so this takes a pointer.
func ExtendRange(r *Range, extension Range) {
	if ComparePositions(extension.Start, r.Start) < 0 {
		r.Start = extension.Start
	}

	if ComparePositions(extension.End, r.End) > 0 {
		r.End = extension.End
	}
}

// CombineRange corresponds to combineRange(). It returns nil when ranges is
// empty. Note that, like the TypeScript version, this mutates ranges[0].
func CombineRange(ranges []Range) *Range {
	if len(ranges) == 0 {
		return nil
	}

	combined := &ranges[0]
	for i := 1; i < len(ranges); i++ {
		ExtendRange(combined, ranges[i])
	}

	return combined
}
