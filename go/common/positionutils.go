/*
 * positionutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Utility routines for converting between file offsets and
 * line/column positions.
 *
 * Transliterated from common/positionUtils.ts (pyright 1.1.412).
 *
 * getLineEndPosition / getLineEndOffset take a TokenizerOutput in TypeScript.
 * Since the parser package imports common, those two live here in terms of the
 * line collection they actually need, and the parser package provides thin
 * TokenizerOutput-shaped wrappers.
 */

package common

// ConvertOffsetToPosition translates a file offset into a line/column pair.
func ConvertOffsetToPosition(offset int, lines *TextRangeCollection[TextRange]) Position {
	// Handle the case where the file is empty.
	if lines.End() == 0 {
		return Position{Line: 0, Character: 0}
	}

	var itemIndex int
	if offset >= lines.End() {
		itemIndex = lines.Count() - 1
	} else {
		itemIndex = lines.GetItemContaining(offset)
	}
	Assert(itemIndex >= 0 && itemIndex <= lines.Count(), "")
	lineRange := lines.GetItemAt(itemIndex)
	return Position{
		Line:      itemIndex,
		Character: max(0, min(lineRange.Length, offset-lineRange.Start)),
	}
}

// ConvertOffsetsToRange translates a start/end file offset into a pair of
// line/column positions.
func ConvertOffsetsToRange(startOffset, endOffset int, lines *TextRangeCollection[TextRange]) Range {
	start := ConvertOffsetToPosition(startOffset, lines)

	// Fast path: when the end offset falls on the same line as the start, derive
	// the end position directly from the start's line range instead of doing a
	// second binary search. The overwhelming majority of ranges converted during
	// binding/analysis are single-line (identifier and keyword tokens), so this
	// avoids a large number of redundant lookups on big files. The result is
	// identical to calling ConvertOffsetToPosition(endOffset) for these cases.
	if lines.End() != 0 && endOffset >= startOffset {
		lineRange := lines.GetItemAt(start.Line)
		lineEnd := lineRange.Start + lineRange.Length
		if endOffset >= lineRange.Start && endOffset < lineEnd {
			return Range{
				Start: start,
				End: Position{
					Line:      start.Line,
					Character: endOffset - lineRange.Start,
				},
			}
		}
	}

	end := ConvertOffsetToPosition(endOffset, lines)
	return Range{Start: start, End: end}
}

// ConvertPositionToOffset translates a position (line and col) into a file
// offset. It returns false when the line is out of range, matching the
// `undefined` return in TypeScript.
func ConvertPositionToOffset(position Position, lines *TextRangeCollection[TextRange]) (int, bool) {
	if position.Line >= lines.Count() {
		return 0, false
	}

	return lines.GetItemAt(position.Line).Start + position.Character, true
}

// ConvertRangeToTextRange corresponds to convertRangeToTextRange().
func ConvertRangeToTextRange(r Range, lines *TextRangeCollection[TextRange]) (TextRange, bool) {
	start, ok := ConvertPositionToOffset(r.Start, lines)
	if !ok {
		return TextRange{}, false
	}

	end, ok := ConvertPositionToOffset(r.End, lines)
	if !ok {
		return TextRange{}, false
	}

	return TextRangeFromBounds(start, end), true
}

// ConvertTextRangeToRange corresponds to convertTextRangeToRange().
func ConvertTextRangeToRange(r TextRange, lines *TextRangeCollection[TextRange]) Range {
	return ConvertOffsetsToRange(r.Start, r.End(), lines)
}

// GetLineEndPositionInLines returns the position of the last character in a
// line (before the newline).
func GetLineEndPositionInLines(lines *TextRangeCollection[TextRange], text Text, line int) Position {
	return ConvertOffsetToPosition(GetLineEndOffsetInLines(lines, text, line), lines)
}

// GetLineEndOffsetInLines corresponds to getLineEndOffset().
func GetLineEndOffsetInLines(lines *TextRangeCollection[TextRange], text Text, line int) int {
	lineRange := lines.GetItemAt(line)

	lineEndOffset := lineRange.End()
	newLineLength := 0
	for i := lineEndOffset - 1; i >= lineRange.Start; i-- {
		char := text.CharCodeAt(i)
		if char != CharCarriageReturn && char != CharLineFeed {
			break
		}

		newLineLength++
	}

	// Character should be at the end of the line but before the newline.
	return lineEndOffset - newLineLength
}
