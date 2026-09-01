/*
 * characterstream.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Based on code from vscode-python repository:
 *  https://github.com/Microsoft/vscode-python
 *
 * Class that represents a stream of characters.
 *
 * Transliterated from parser/characterStream.ts (pyright 1.1.412).
 */

package parser

import "github.com/microsoft/pyright/go/common"

// CharacterStream represents a stream of characters.
type CharacterStream struct {
	text        common.Text
	position    int
	currentChar common.Char
	isEndOfStr  bool
}

// NewCharacterStream corresponds to the CharacterStream constructor.
func NewCharacterStream(text common.Text) *CharacterStream {
	cs := &CharacterStream{
		text:       text,
		position:   0,
		isEndOfStr: text.Length() == 0,
	}
	if text.Length() > 0 {
		cs.currentChar = text.CharCodeAt(0)
	} else {
		cs.currentChar = 0
	}
	return cs
}

// Position corresponds to the `position` getter.
func (cs *CharacterStream) Position() int {
	return cs.position
}

// SetPosition corresponds to the `position` setter.
func (cs *CharacterStream) SetPosition(value int) {
	cs.position = value
	cs.checkBounds()
}

// CurrentChar corresponds to the `currentChar` getter.
func (cs *CharacterStream) CurrentChar() common.Char {
	return cs.currentChar
}

// NextChar corresponds to the `nextChar` getter.
func (cs *CharacterStream) NextChar() common.Char {
	if cs.position+1 < cs.text.Length() {
		return cs.text.CharCodeAt(cs.position + 1)
	}
	return 0
}

// PrevChar corresponds to the `prevChar` getter.
func (cs *CharacterStream) PrevChar() common.Char {
	if cs.position-1 >= 0 {
		return cs.text.CharCodeAt(cs.position - 1)
	}
	return 0
}

// Length corresponds to the `length` getter.
func (cs *CharacterStream) Length() int {
	return cs.text.Length()
}

// GetText corresponds to getText().
func (cs *CharacterStream) GetText() common.Text {
	return cs.text
}

// GetCurrentChar is the (non-property) equivalent of CurrentChar above. In the
// TypeScript sources this exists to work around assumptions in the TypeScript
// compiler that method calls (e.g. moveNext()) don't modify properties; it is
// kept so the transliterated call sites stay recognizable.
func (cs *CharacterStream) GetCurrentChar() common.Char {
	return cs.currentChar
}

// IsEndOfStream corresponds to isEndOfStream().
func (cs *CharacterStream) IsEndOfStream() bool {
	return cs.isEndOfStr
}

// LookAhead corresponds to lookAhead().
func (cs *CharacterStream) LookAhead(offset int) common.Char {
	pos := cs.position + offset
	if pos < 0 || pos >= cs.text.Length() {
		return 0
	}
	return cs.text.CharCodeAt(pos)
}

// Advance corresponds to advance().
func (cs *CharacterStream) Advance(offset int) {
	cs.SetPosition(cs.position + offset)
}

// MoveNext corresponds to moveNext().
func (cs *CharacterStream) MoveNext() bool {
	if cs.position < cs.text.Length()-1 {
		// Most common case, no need to check bounds extensively
		cs.position += 1
		cs.currentChar = cs.text.CharCodeAt(cs.position)
		return true
	}
	cs.Advance(1)
	return !cs.IsEndOfStream()
}

// IsAtWhiteSpace corresponds to isAtWhiteSpace().
func (cs *CharacterStream) IsAtWhiteSpace() bool {
	return IsWhiteSpace(cs.CurrentChar())
}

// IsAtLineBreak corresponds to isAtLineBreak().
func (cs *CharacterStream) IsAtLineBreak() bool {
	return IsLineBreak(cs.CurrentChar())
}

// SkipLineBreak corresponds to skipLineBreak().
func (cs *CharacterStream) SkipLineBreak() {
	if cs.currentChar == common.CharCarriageReturn {
		cs.MoveNext()
		if cs.CurrentChar() == common.CharLineFeed {
			cs.MoveNext()
		}
	} else if cs.currentChar == common.CharLineFeed {
		cs.MoveNext()
	}
}

// SkipWhitespace corresponds to skipWhitespace().
func (cs *CharacterStream) SkipWhitespace() {
	// Tight loop: advance position/currentChar directly while the
	// current char is a space/tab/form-feed. Avoids the method-call
	// overhead of MoveNext() + IsAtWhiteSpace() + IsWhiteSpace() per
	// iteration, which is one of the hottest paths in tokenization.
	text := cs.text
	length := text.Length()
	pos := cs.position
	for pos < length {
		ch := text.CharCodeAt(pos)
		if ch == common.CharSpace || ch == common.CharTab || ch == common.CharFormFeed {
			pos++
		} else {
			break
		}
	}
	if pos != cs.position {
		cs.position = pos
		if pos >= length {
			cs.isEndOfStr = true
			cs.position = length
			cs.currentChar = 0
		} else {
			cs.currentChar = text.CharCodeAt(pos)
		}
	}
}

// SkipToEol corresponds to skipToEol().
func (cs *CharacterStream) SkipToEol() {
	for !cs.IsEndOfStream() && !cs.IsAtLineBreak() {
		cs.MoveNext()
	}
}

// SkipToWhitespace corresponds to skipToWhitespace().
func (cs *CharacterStream) SkipToWhitespace() {
	for !cs.IsEndOfStream() && !cs.IsAtWhiteSpace() {
		cs.MoveNext()
	}
}

// CharCodeAt corresponds to charCodeAt().
func (cs *CharacterStream) CharCodeAt(index int) common.Char {
	return cs.text.CharCodeAt(index)
}

func (cs *CharacterStream) checkBounds() {
	if cs.position < 0 {
		cs.position = 0
	}

	cs.isEndOfStr = cs.position >= cs.text.Length()
	if cs.isEndOfStr {
		cs.position = cs.text.Length()
	}

	if cs.isEndOfStr {
		cs.currentChar = 0
	} else {
		cs.currentChar = cs.text.CharCodeAt(cs.position)
	}
}
