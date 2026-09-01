/*
 * tokenizertypes.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Based on code from vscode-python repository:
 *  https://github.com/Microsoft/vscode-python
 *
 * Interface, enumeration and class definitions used within
 * the Python tokenizer.
 *
 * Transliterated from parser/tokenizerTypes.ts (pyright 1.1.412).
 *
 * TypeScript models tokens as a structural union discriminated by `type`, and
 * the parser narrows with `token as StringToken`. Go gets the same shape from
 * a Token interface plus concrete structs embedding TokenBase; the narrowing
 * casts become type assertions.
 *
 * The "two-shape" object pattern in the TypeScript factories (omitting the
 * `comments` slot when unused) is a V8 hidden-class optimization with no Go
 * equivalent -- a nil slice header costs nothing extra -- so the factories here
 * always set the field. The observable result is identical.
 */

package parser

import (
	"math/big"

	"github.com/microsoft/pyright/go/common"
	"golang.org/x/text/unicode/norm"
)

// TokenType corresponds to the TokenType const enum.
type TokenType int

const (
	TokenTypeInvalid TokenType = iota
	TokenTypeEndOfStream
	TokenTypeNewLine
	TokenTypeIndent
	TokenTypeDedent
	TokenTypeString
	TokenTypeNumber
	TokenTypeIdentifier
	TokenTypeKeyword
	TokenTypeOperator
	TokenTypeColon
	TokenTypeSemicolon
	TokenTypeComma
	TokenTypeOpenParenthesis
	TokenTypeCloseParenthesis
	TokenTypeOpenBracket
	TokenTypeCloseBracket
	TokenTypeOpenCurlyBrace
	TokenTypeCloseCurlyBrace
	TokenTypeEllipsis
	TokenTypeDot
	TokenTypeArrow
	TokenTypeBacktick
	TokenTypeExclamationMark
	TokenTypeFStringStart
	TokenTypeFStringMiddle
	TokenTypeFStringEnd
)

// NewLineType corresponds to the NewLineType const enum.
type NewLineType int

const (
	NewLineTypeCarriageReturn NewLineType = iota
	NewLineTypeLineFeed
	NewLineTypeCarriageReturnLineFeed
	NewLineTypeImplied
)

// OperatorType corresponds to the OperatorType const enum.
type OperatorType int

const (
	// These operators are used with tokens
	// of type TokenTypeOperator.
	OperatorTypeAdd OperatorType = iota
	OperatorTypeAddEqual
	OperatorTypeAssign
	OperatorTypeBitwiseAnd
	OperatorTypeBitwiseAndEqual
	OperatorTypeBitwiseInvert
	OperatorTypeBitwiseOr
	OperatorTypeBitwiseOrEqual
	OperatorTypeBitwiseXor
	OperatorTypeBitwiseXorEqual
	OperatorTypeDivide
	OperatorTypeDivideEqual
	OperatorTypeEquals
	OperatorTypeFloorDivide
	OperatorTypeFloorDivideEqual
	OperatorTypeGreaterThan
	OperatorTypeGreaterThanOrEqual
	OperatorTypeLeftShift
	OperatorTypeLeftShiftEqual
	OperatorTypeLessOrGreaterThan
	OperatorTypeLessThan
	OperatorTypeLessThanOrEqual
	OperatorTypeMatrixMultiply
	OperatorTypeMatrixMultiplyEqual
	OperatorTypeMod
	OperatorTypeModEqual
	OperatorTypeMultiply
	OperatorTypeMultiplyEqual
	OperatorTypeNotEquals
	OperatorTypePower
	OperatorTypePowerEqual
	OperatorTypeRightShift
	OperatorTypeRightShiftEqual
	OperatorTypeSubtract
	OperatorTypeSubtractEqual
	OperatorTypeWalrus

	// These operators are used with tokens
	// of type TokenTypeKeyword.
	OperatorTypeAnd
	OperatorTypeOr
	OperatorTypeNot
	OperatorTypeIs
	OperatorTypeIsNot
	OperatorTypeIn
	OperatorTypeNotIn
)

// OperatorFlags corresponds to the OperatorFlags const enum.
type OperatorFlags int

const (
	OperatorFlagsUnary      OperatorFlags = 1 << 0
	OperatorFlagsBinary     OperatorFlags = 1 << 1
	OperatorFlagsAssignment OperatorFlags = 1 << 2
	OperatorFlagsComparison OperatorFlags = 1 << 3
	OperatorFlagsDeprecated OperatorFlags = 1 << 4
)

// KeywordType corresponds to the KeywordType const enum.
type KeywordType int

const (
	KeywordTypeAnd KeywordType = iota
	KeywordTypeAs
	KeywordTypeAssert
	KeywordTypeAsync
	KeywordTypeAwait
	KeywordTypeBreak
	KeywordTypeCase
	KeywordTypeClass
	KeywordTypeContinue
	KeywordTypeDebug
	KeywordTypeDef
	KeywordTypeDel
	KeywordTypeElif
	KeywordTypeElse
	KeywordTypeExcept
	KeywordTypeFalse
	KeywordTypeFinally
	KeywordTypeFor
	KeywordTypeFrom
	KeywordTypeGlobal
	KeywordTypeIf
	KeywordTypeImport
	KeywordTypeIn
	KeywordTypeIs
	KeywordTypeLambda
	KeywordTypeLazy
	KeywordTypeMatch
	KeywordTypeNone
	KeywordTypeNonlocal
	KeywordTypeNot
	KeywordTypeOr
	KeywordTypePass
	KeywordTypeRaise
	KeywordTypeReturn
	KeywordTypeTrue
	KeywordTypeTry
	KeywordTypeType
	KeywordTypeWhile
	KeywordTypeWith
	KeywordTypeYield
)

// SoftKeywords corresponds to the softKeywords array.
var SoftKeywords = []KeywordType{
	KeywordTypeDebug,
	KeywordTypeMatch,
	KeywordTypeCase,
	KeywordTypeType,
	KeywordTypeLazy,
}

// StringTokenFlags corresponds to the StringTokenFlags const enum.
type StringTokenFlags int

const (
	StringTokenFlagsNone StringTokenFlags = 0

	// Quote types
	StringTokenFlagsSingleQuote StringTokenFlags = 1 << 0
	StringTokenFlagsDoubleQuote StringTokenFlags = 1 << 1
	StringTokenFlagsTriplicate  StringTokenFlags = 1 << 2

	// String content format
	StringTokenFlagsRaw      StringTokenFlags = 1 << 3
	StringTokenFlagsUnicode  StringTokenFlags = 1 << 4
	StringTokenFlagsBytes    StringTokenFlags = 1 << 5
	StringTokenFlagsFormat   StringTokenFlags = 1 << 6
	StringTokenFlagsTemplate StringTokenFlags = 1 << 7

	// Other conditions
	StringTokenFlagsReplacementFieldStart StringTokenFlags = 1 << 8
	StringTokenFlagsReplacementFieldEnd   StringTokenFlags = 1 << 9
	StringTokenFlagsNamedUnicodeEscape    StringTokenFlags = 1 << 10

	// Error conditions
	StringTokenFlagsUnterminated StringTokenFlags = 1 << 16
)

// CommentType corresponds to the CommentType const enum.
type CommentType int

const (
	CommentTypeRegular CommentType = iota
	CommentTypeIPythonMagic
	CommentTypeIPythonShellEscape
	CommentTypeIPythonCellMagic
	CommentTypeIPythonCellShellEscape
)

// Comment corresponds to the Comment interface.
type Comment struct {
	common.TextRange
	Type  CommentType
	Value common.Text
}

// NewComment corresponds to Comment.create().
func NewComment(start, length int, value common.Text, commentType CommentType) *Comment {
	return &Comment{
		TextRange: common.TextRange{Start: start, Length: length},
		Type:      commentType,
		Value:     value,
	}
}

// Token is the interface every token satisfies, standing in for the TokenBase
// interface that all the token shapes extend.
type Token interface {
	common.RangeItem
	GetType() TokenType
	GetComments() []*Comment
}

// TokenBase corresponds to the TokenBase interface.
type TokenBase struct {
	common.TextRange
	Type TokenType

	// Comments prior to the token.
	Comments []*Comment
}

// GetType returns the token's type.
func (t *TokenBase) GetType() TokenType { return t.Type }

// GetComments returns the comments preceding the token.
func (t *TokenBase) GetComments() []*Comment { return t.Comments }

// GetRange satisfies common.RangeItem.
func (t *TokenBase) GetRange() common.TextRange { return t.TextRange }

// NewToken corresponds to Token.create().
func NewToken(tokenType TokenType, start, length int, comments []*Comment) *TokenBase {
	return &TokenBase{
		TextRange: common.TextRange{Start: start, Length: length},
		Type:      tokenType,
		Comments:  comments,
	}
}

// IndentToken corresponds to the IndentToken interface.
type IndentToken struct {
	TokenBase
	IndentAmount      int
	IsIndentAmbiguous bool
}

// NewIndentToken corresponds to IndentToken.create().
func NewIndentToken(start, length, indentAmount int, isIndentAmbiguous bool, comments []*Comment) *IndentToken {
	return &IndentToken{
		TokenBase: TokenBase{
			TextRange: common.TextRange{Start: start, Length: length},
			Type:      TokenTypeIndent,
			Comments:  comments,
		},
		IndentAmount:      indentAmount,
		IsIndentAmbiguous: isIndentAmbiguous,
	}
}

// DedentToken corresponds to the DedentToken interface.
type DedentToken struct {
	TokenBase
	IndentAmount      int
	MatchesIndent     bool
	IsDedentAmbiguous bool
}

// NewDedentToken corresponds to DedentToken.create().
func NewDedentToken(start, length, indentAmount int, matchesIndent, isDedentAmbiguous bool, comments []*Comment) *DedentToken {
	return &DedentToken{
		TokenBase: TokenBase{
			TextRange: common.TextRange{Start: start, Length: length},
			Type:      TokenTypeDedent,
			Comments:  comments,
		},
		IndentAmount:      indentAmount,
		MatchesIndent:     matchesIndent,
		IsDedentAmbiguous: isDedentAmbiguous,
	}
}

// NewLineToken corresponds to the NewLineToken interface.
type NewLineToken struct {
	TokenBase
	NewLineType NewLineType
}

// NewNewLineToken corresponds to NewLineToken.create().
func NewNewLineToken(start, length int, newLineType NewLineType, comments []*Comment) *NewLineToken {
	return &NewLineToken{
		TokenBase: TokenBase{
			TextRange: common.TextRange{Start: start, Length: length},
			Type:      TokenTypeNewLine,
			Comments:  comments,
		},
		NewLineType: newLineType,
	}
}

// KeywordToken corresponds to the KeywordToken interface.
type KeywordToken struct {
	TokenBase
	KeywordType KeywordType
}

// NewKeywordToken corresponds to KeywordToken.create().
func NewKeywordToken(start, length int, keywordType KeywordType, comments []*Comment) *KeywordToken {
	return &KeywordToken{
		TokenBase: TokenBase{
			TextRange: common.TextRange{Start: start, Length: length},
			Type:      TokenTypeKeyword,
			Comments:  comments,
		},
		KeywordType: keywordType,
	}
}

// IsSoftKeyword corresponds to KeywordToken.isSoftKeyword().
func (t *KeywordToken) IsSoftKeyword() bool {
	for _, softKeyword := range SoftKeywords {
		if t.KeywordType == softKeyword {
			return true
		}
	}
	return false
}

// StringToken corresponds to the StringToken interface.
type StringToken struct {
	TokenBase
	Flags StringTokenFlags

	// Use the string token utils to convert escaped value to unescaped value.
	EscapedValue common.Text

	// Number of characters in token that appear before
	// the quote marks (e.g. "r" or "UR").
	PrefixLength int

	// Number of characters in token that make up the quote
	// (either 1 or 3).
	QuoteMarkLength int
}

// NewStringToken corresponds to StringToken.create().
func NewStringToken(start, length int, flags StringTokenFlags, escapedValue common.Text, prefixLength int, comments []*Comment) *StringToken {
	quoteMarkLength := 1
	if flags&StringTokenFlagsTriplicate != 0 {
		quoteMarkLength = 3
	}
	return &StringToken{
		TokenBase: TokenBase{
			TextRange: common.TextRange{Start: start, Length: length},
			Type:      TokenTypeString,
			Comments:  comments,
		},
		Flags:           flags,
		EscapedValue:    escapedValue,
		PrefixLength:    prefixLength,
		QuoteMarkLength: quoteMarkLength,
	}
}

// FStringStartToken corresponds to the FStringStartToken interface.
type FStringStartToken struct {
	TokenBase
	Flags StringTokenFlags

	// Number of characters in token that appear before
	// the quote marks (e.g. "r" or "UR").
	PrefixLength int

	// Number of characters in token that make up the quote
	// (either 1 or 3).
	QuoteMarkLength int
}

// NewFStringStartToken corresponds to FStringStartToken.create().
func NewFStringStartToken(start, length int, flags StringTokenFlags, prefixLength int, comments []*Comment) *FStringStartToken {
	quoteMarkLength := 1
	if flags&StringTokenFlagsTriplicate != 0 {
		quoteMarkLength = 3
	}
	return &FStringStartToken{
		TokenBase: TokenBase{
			TextRange: common.TextRange{Start: start, Length: length},
			Type:      TokenTypeFStringStart,
			Comments:  comments,
		},
		Flags:           flags,
		PrefixLength:    prefixLength,
		QuoteMarkLength: quoteMarkLength,
	}
}

// FStringMiddleToken corresponds to the FStringMiddleToken interface.
type FStringMiddleToken struct {
	TokenBase
	Flags StringTokenFlags

	// Use the string token utils to convert escaped value to unescaped value.
	EscapedValue common.Text
}

// NewFStringMiddleToken corresponds to FStringMiddleToken.create().
func NewFStringMiddleToken(start, length int, flags StringTokenFlags, escapedValue common.Text) *FStringMiddleToken {
	return &FStringMiddleToken{
		TokenBase: TokenBase{
			TextRange: common.TextRange{Start: start, Length: length},
			Type:      TokenTypeFStringMiddle,
		},
		Flags:        flags,
		EscapedValue: escapedValue,
	}
}

// FStringEndToken corresponds to the FStringEndToken interface.
type FStringEndToken struct {
	TokenBase
	Flags StringTokenFlags
}

// NewFStringEndToken corresponds to FStringEndToken.create().
func NewFStringEndToken(start, length int, flags StringTokenFlags) *FStringEndToken {
	return &FStringEndToken{
		TokenBase: TokenBase{
			TextRange: common.TextRange{Start: start, Length: length},
			Type:      TokenTypeFStringEnd,
		},
		Flags: flags,
	}
}

// NumberValue models the `number | bigint` union of NumberToken.value.
//
// The tokenizer produces a bigint only for integer literals that fall outside
// the IEEE-754 safe-integer range, and a double otherwise (for both floats and
// small integers). Collapsing the two would silently change the value of large
// integer literals, so the distinction is preserved.
type NumberValue struct {
	IsBigInt bool
	BigInt   *big.Int
	Float    float64
}

// NewFloatValue constructs the `number` arm of the union.
func NewFloatValue(value float64) NumberValue {
	return NumberValue{Float: value}
}

// NewBigIntValue constructs the `bigint` arm of the union.
func NewBigIntValue(value *big.Int) NumberValue {
	return NumberValue{IsBigInt: true, BigInt: value}
}

// NumberToken corresponds to the NumberToken interface.
type NumberToken struct {
	TokenBase
	Value       NumberValue
	IsInteger   bool
	IsImaginary bool
}

// NewNumberToken corresponds to NumberToken.create().
func NewNumberToken(start, length int, value NumberValue, isInteger, isImaginary bool, comments []*Comment) *NumberToken {
	return &NumberToken{
		TokenBase: TokenBase{
			TextRange: common.TextRange{Start: start, Length: length},
			Type:      TokenTypeNumber,
			Comments:  comments,
		},
		Value:       value,
		IsInteger:   isInteger,
		IsImaginary: isImaginary,
	}
}

// OperatorToken corresponds to the OperatorToken interface.
type OperatorToken struct {
	TokenBase
	OperatorType OperatorType
}

// NewOperatorToken corresponds to OperatorToken.create().
func NewOperatorToken(start, length int, operatorType OperatorType, comments []*Comment) *OperatorToken {
	return &OperatorToken{
		TokenBase: TokenBase{
			TextRange: common.TextRange{Start: start, Length: length},
			Type:      TokenTypeOperator,
			Comments:  comments,
		},
		OperatorType: operatorType,
	}
}

// IdentifierToken corresponds to the IdentifierToken interface.
//
// Value is a Go string rather than common.Text: an identifier cannot contain an
// unpaired surrogate, so the UTF-16 -> UTF-8 conversion is lossless here, and
// every consumer treats identifiers as comparable string keys.
type IdentifierToken struct {
	TokenBase
	Value string
}

// NewIdentifierToken corresponds to IdentifierToken.create().
func NewIdentifierToken(start, length int, value common.Text, comments []*Comment) *IdentifierToken {
	// Perform "NFKC normalization", as per the Python lexical spec.
	normalizedValue := value.String()
	for i := 0; i < value.Length(); i++ {
		if value.CharCodeAt(i) > 0x7f {
			normalizedValue = norm.NFKC.String(normalizedValue)
			break
		}
	}

	return &IdentifierToken{
		TokenBase: TokenBase{
			TextRange: common.TextRange{Start: start, Length: length},
			Type:      TokenTypeIdentifier,
			Comments:  comments,
		},
		Value: normalizedValue,
	}
}
