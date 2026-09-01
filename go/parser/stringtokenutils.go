/*
 * stringtokenutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Methods that handle unescaping of escaped string token
 * literal values.
 *
 * Transliterated from parser/stringTokenUtils.ts (pyright 1.1.412).
 *
 * Values here are common.Text (UTF-16 code units) rather than Go strings.
 * That is required, not stylistic: the TypeScript code builds output with
 * String.fromCharCode, which truncates its argument to 16 bits, so a `\U0001f600`
 * escape produces the single code unit 0xf600 rather than the astral code
 * point, and a `\ud800` escape produces an unpaired surrogate. Both are
 * representable in UTF-16 and neither survives a round trip through UTF-8.
 */

package parser

import "github.com/microsoft/pyright/go/common"

// UnescapeErrorType corresponds to the UnescapeErrorType const enum.
type UnescapeErrorType int

const (
	UnescapeErrorTypeInvalidEscapeSequence UnescapeErrorType = iota
)

// UnescapeError corresponds to the UnescapeError interface.
type UnescapeError struct {
	// Offset within the unescaped string where this error begins.
	Offset int

	// Length of section associated with error.
	Length int

	// Type of error.
	ErrorType UnescapeErrorType
}

// UnescapedString corresponds to the UnescapedString interface.
type UnescapedString struct {
	Value           common.Text
	UnescapeErrors  []UnescapeError
	NonAsciiInBytes bool
}

// StringTokenLike covers the StringToken | FStringMiddleToken parameter of
// getUnescapedString().
type StringTokenLike interface {
	stringFlags() StringTokenFlags
	escapedValue() common.Text
}

func (t *StringToken) stringFlags() StringTokenFlags { return t.Flags }
func (t *StringToken) escapedValue() common.Text     { return t.EscapedValue }

func (t *FStringMiddleToken) stringFlags() StringTokenFlags { return t.Flags }
func (t *FStringMiddleToken) escapedValue() common.Text     { return t.EscapedValue }

// GetUnescapedString corresponds to getUnescapedString() with elideCrlf true.
func GetUnescapedString(stringToken StringTokenLike) UnescapedString {
	return GetUnescapedStringEx(stringToken /* elideCrlf */, true)
}

// GetUnescapedStringEx corresponds to getUnescapedString().
func GetUnescapedStringEx(stringToken StringTokenLike, elideCrlf bool) UnescapedString {
	escapedString := stringToken.escapedValue()
	flags := stringToken.stringFlags()
	isRaw := (flags & StringTokenFlagsRaw) != 0

	if isRaw {
		return UnescapedString{
			Value:          escapedString,
			UnescapeErrors: []UnescapeError{},
		}
	}

	isBytes := (flags & StringTokenFlagsBytes) != 0

	// Scan once for the characters that force the slow (unescaping) path.
	hasEscapeOrNewLine := false
	nonAsciiInBytes := false
	for index := 0; index < escapedString.Length(); index++ {
		curChar := escapedString.CharCodeAt(index)
		if curChar == common.CharCarriageReturn || curChar == common.CharLineFeed || curChar == common.CharBackslash {
			hasEscapeOrNewLine = true
			// nonAsciiInBytes below is only consulted by the fast-path return,
			// which we won't take once an escape/newline is found (the slow
			// path recomputes it), so there's no need to keep scanning.
			break
		}
		if isBytes && curChar >= 128 {
			nonAsciiInBytes = true
		}
	}

	// Handle the common case in an expedited manner.
	if !hasEscapeOrNewLine {
		return UnescapedString{
			Value:           escapedString,
			UnescapeErrors:  []UnescapeError{},
			NonAsciiInBytes: nonAsciiInBytes,
		}
	}

	strOffset := 0
	var valueParts common.TextBuilder
	unescapeErrors := []UnescapeError{}
	outputNonAsciiInBytes := false

	addInvalidEscapeOffset := func() {
		// Invalid escapes are not reported for raw strings.
		if !isRaw {
			unescapeErrors = append(unescapeErrors, UnescapeError{
				Offset:    strOffset - 1,
				Length:    2,
				ErrorType: UnescapeErrorTypeInvalidEscapeSequence,
			})
		}
	}

	getEscapedCharacter := func(offset int) common.Char {
		if strOffset+offset >= escapedString.Length() {
			return common.CharEndOfText
		}

		return escapedString.CharCodeAt(strOffset + offset)
	}

	// scanHexEscape appends directly to localValue, standing in for the
	// TypeScript version's returned string.
	scanHexEscape := func(digitCount int, localValue *common.TextBuilder) {
		foundIllegalHexDigit := false
		hexValue := 0

		for i := 0; i < digitCount; i++ {
			charCode := getEscapedCharacter(1 + i)
			if !isHexCharCode(charCode) {
				foundIllegalHexDigit = true
				break
			}
			hexValue = 16*hexValue + getHexDigitValue(charCode)
		}

		if foundIllegalHexDigit {
			addInvalidEscapeOffset()
			localValue.WriteChar(common.CharBackslash)
			localValue.WriteChar(fromCharCode(getEscapedCharacter(0)))
			strOffset++
		} else {
			// String.fromCharCode truncates to 16 bits; \U0001f600 therefore
			// yields the single code unit 0xf600.
			localValue.WriteChar(fromCharCode(hexValue))
			strOffset += 1 + digitCount
		}
	}

	appendOutputChar := func(charCode common.Char) {
		valueParts.WriteChar(fromCharCode(charCode))
	}

	complete := func() UnescapedString {
		// The TypeScript version returns the original string object when the
		// unescaped text is identical, purely to avoid retaining a V8 sliced
		// string. That has no observable effect.
		return UnescapedString{
			Value:           valueParts.Text(),
			UnescapeErrors:  unescapeErrors,
			NonAsciiInBytes: outputNonAsciiInBytes,
		}
	}

	for {
		curChar := getEscapedCharacter(0)
		if curChar == common.CharEndOfText {
			return complete()
		}

		if curChar == common.CharBackslash {
			// Move past the escape (backslash) character.
			strOffset++

			if isRaw {
				appendOutputChar(curChar)
				continue
			}

			curChar = getEscapedCharacter(0)
			var localValue common.TextBuilder

			if curChar == common.CharCarriageReturn || curChar == common.CharLineFeed {
				if curChar == common.CharCarriageReturn && getEscapedCharacter(1) == common.CharLineFeed {
					if isRaw {
						localValue.WriteChar(fromCharCode(curChar))
					}
					strOffset++
					curChar = getEscapedCharacter(0)
				}
				if isRaw {
					// Rebuild as '\\' + localValue + fromCharCode(curChar).
					var rebuilt common.TextBuilder
					rebuilt.WriteChar(common.CharBackslash)
					rebuilt.WriteText(localValue.Text())
					rebuilt.WriteChar(fromCharCode(curChar))
					localValue = rebuilt
				}
				strOffset++
			} else {
				if isRaw {
					localValue.Reset()
					localValue.WriteChar(common.CharBackslash)
					localValue.WriteChar(fromCharCode(curChar))
					strOffset++
				} else {
					switch curChar {
					case common.CharBackslash, common.CharSingleQuote, common.CharDoubleQuote:
						localValue.WriteChar(fromCharCode(curChar))
						strOffset++

					case common.CharLowerA:
						localValue.WriteChar(0x0007)
						strOffset++

					case common.CharLowerB:
						localValue.WriteChar(0x0008)
						strOffset++

					case common.CharLowerF:
						localValue.WriteChar(0x000c)
						strOffset++

					case common.CharLowerN:
						localValue.WriteChar(0x000a)
						strOffset++

					case common.CharLowerR:
						localValue.WriteChar(0x000d)
						strOffset++

					case common.CharLowerT:
						localValue.WriteChar(0x0009)
						strOffset++

					case common.CharLowerV:
						localValue.WriteChar(0x000b)
						strOffset++

					case common.CharLowerX:
						scanHexEscape(2, &localValue)

					case common.CharN:
						foundIllegalChar := false
						charCount := 1

						// This type of escape isn't allowed for bytes.
						if isBytes {
							foundIllegalChar = true
						}

						if getEscapedCharacter(charCount) != common.CharOpenBrace {
							foundIllegalChar = true
						} else {
							charCount++
							for {
								lookaheadChar := getEscapedCharacter(charCount)
								if lookaheadChar == common.CharCloseBrace {
									break
								} else if !isAlphaNumericChar(lookaheadChar) &&
									lookaheadChar != common.CharHyphen &&
									!isWhitespaceChar(lookaheadChar) {
									foundIllegalChar = true
									break
								} else {
									charCount++
								}
							}
						}

						if foundIllegalChar {
							addInvalidEscapeOffset()
							localValue.WriteChar(common.CharBackslash)
							localValue.WriteChar(fromCharCode(curChar))
							strOffset++
						} else {
							// We don't have the Unicode name database handy, so
							// assume that the name is valid and use a '-' as a
							// replacement character.
							localValue.WriteChar(common.CharHyphen)
							strOffset += 1 + charCount
						}

					case common.CharLowerU, common.CharU:
						// This type of escape isn't allowed for bytes.
						if isBytes {
							addInvalidEscapeOffset()
						}
						digitCount := 8
						if curChar == common.CharLowerU {
							digitCount = 4
						}
						scanHexEscape(digitCount, &localValue)

					default:
						if isOctalCharCode(curChar) {
							octalCode := int(curChar - common.Char0)
							strOffset++
							curChar = getEscapedCharacter(0)
							if isOctalCharCode(curChar) {
								octalCode = octalCode*8 + int(curChar) - common.Char0
								strOffset++
								curChar = getEscapedCharacter(0)

								if isOctalCharCode(curChar) {
									octalCode = octalCode*8 + int(curChar) - common.Char0
									strOffset++
								}
							}

							localValue.WriteChar(fromCharCode(octalCode))
						} else {
							localValue.WriteChar(common.CharBackslash)
							addInvalidEscapeOffset()
						}
					}
				}
			}

			valueParts.WriteText(localValue.Text())
		} else if curChar == common.CharLineFeed || curChar == common.CharCarriageReturn {
			// Skip over the escaped new line (either one or two characters).
			if curChar == common.CharCarriageReturn && getEscapedCharacter(1) == common.CharLineFeed {
				if !elideCrlf {
					appendOutputChar(curChar)
				}
				strOffset++
				curChar = getEscapedCharacter(0)
			}

			appendOutputChar(curChar)
			strOffset++
		} else {
			// There's nothing to unescape, so output the escaped character directly.
			if isBytes && curChar >= 128 {
				outputNonAsciiInBytes = true
			}

			appendOutputChar(curChar)
			strOffset++
		}
	}
}

// fromCharCode reproduces String.fromCharCode, which coerces its argument to a
// UInt16 -- i.e. truncates modulo 65536.
func fromCharCode(charCode int) common.Char {
	return common.Char(uint16(charCode))
}

func isWhitespaceChar(charCode common.Char) bool {
	return charCode == common.CharSpace || charCode == common.CharTab
}

func isAlphaNumericChar(charCode common.Char) bool {
	if charCode >= common.Char0 && charCode <= common.Char9 {
		return true
	}

	if charCode >= common.CharLowerA && charCode <= common.CharLowerZ {
		return true
	}

	if charCode >= common.CharA && charCode <= common.CharZ {
		return true
	}

	return false
}

func isOctalCharCode(charCode common.Char) bool {
	return charCode >= common.Char0 && charCode <= common.Char7
}

func isHexCharCode(charCode common.Char) bool {
	if charCode >= common.Char0 && charCode <= common.Char9 {
		return true
	}

	if charCode >= common.CharLowerA && charCode <= common.CharLowerF {
		return true
	}

	if charCode >= common.CharA && charCode <= common.CharF {
		return true
	}

	return false
}

func getHexDigitValue(charCode common.Char) int {
	if charCode >= common.Char0 && charCode <= common.Char9 {
		return int(charCode - common.Char0)
	}

	if charCode >= common.CharLowerA && charCode <= common.CharLowerF {
		return int(charCode-common.CharLowerA) + 10
	}

	if charCode >= common.CharA && charCode <= common.CharF {
		return int(charCode-common.CharA) + 10
	}

	return 0
}
