/*
 * charcodes.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Based on code from typescript-char:
 *  https://github.com/mason-lang/typescript-char
 *
 * Character code definitions.
 *
 * Transliterated from common/charCodes.ts (pyright 1.1.412).
 */

package common

// Char enumerates UTF-16 code unit values that the tokenizer cares about.
type Char = int

const (
	CharNull                    Char = 0
	CharStartOfHeading          Char = 1
	CharStartOfText             Char = 2
	CharEndOfText               Char = 3
	CharEndOfTransmission       Char = 4
	CharEnquiry                 Char = 5
	CharAcknowledge             Char = 6
	CharBell                    Char = 7
	CharBackspace               Char = 8
	CharTab                     Char = 9
	CharLineFeed                Char = 0xa
	CharVerticalTab             Char = 0xb
	CharFormFeed                Char = 0xc
	CharCarriageReturn          Char = 0xd
	CharShiftOut                Char = 0xe
	CharShirtIn                 Char = 0xf
	CharDataLineEscape          Char = 0x10
	CharDeviceControl1          Char = 0x11
	CharDeviceControl2          Char = 0x12
	CharDeviceControl3          Char = 0x13
	CharDeviceControl4          Char = 0x14
	CharNegativeAcknowledgement Char = 0x15
	CharSynchronousIdle         Char = 0x16
	CharEndOfTransmitBlock      Char = 0x17
	CharCancel                  Char = 0x18
	CharEndOfMedium             Char = 0x19
	CharSubstitute              Char = 0x1a
	CharEscape                  Char = 0x1b
	CharFileSeparator           Char = 0x1c
	CharGroupSeparator          Char = 0x1d
	CharRecordSeparator         Char = 0x1e
	CharUnitSeparator           Char = 0x1f
)

// Printable characters
const (
	CharSpace            Char = 0x20
	CharExclamationMark  Char = 0x21
	CharDoubleQuote      Char = 0x22
	CharHash             Char = 0x23
	CharDollar           Char = 0x24
	CharPercent          Char = 0x25
	CharAmpersand        Char = 0x26
	CharSingleQuote      Char = 0x27
	CharOpenParenthesis  Char = 0x28
	CharCloseParenthesis Char = 0x29
	CharAsterisk         Char = 0x2a
	CharPlus             Char = 0x2b
	CharComma            Char = 0x2c
	CharHyphen           Char = 0x2d
	CharPeriod           Char = 0x2e
	CharSlash            Char = 0x2f
	Char0                Char = 0x30
	Char1                Char = 0x31
	Char2                Char = 0x32
	Char3                Char = 0x33
	Char4                Char = 0x34
	Char5                Char = 0x35
	Char6                Char = 0x36
	Char7                Char = 0x37
	Char8                Char = 0x38
	Char9                Char = 0x39
	CharColon            Char = 0x3a
	CharSemicolon        Char = 0x3b
	CharLess             Char = 0x3c
	CharEqual            Char = 0x3d
	CharGreater          Char = 0x3e
	CharQuestionMark     Char = 0x3f
	CharAt               Char = 0x40
	CharA                Char = 0x41
	CharB                Char = 0x42
	CharC                Char = 0x43
	CharD                Char = 0x44
	CharE                Char = 0x45
	CharF                Char = 0x46
	CharG                Char = 0x47
	CharH                Char = 0x48
	CharI                Char = 0x49
	CharJ                Char = 0x4a
	CharK                Char = 0x4b
	CharL                Char = 0x4c
	CharM                Char = 0x4d
	CharN                Char = 0x4e
	CharO                Char = 0x4f
	CharP                Char = 0x50
	CharQ                Char = 0x51
	CharR                Char = 0x52
	CharS                Char = 0x53
	CharT                Char = 0x54
	CharU                Char = 0x55
	CharV                Char = 0x56
	CharW                Char = 0x57
	CharX                Char = 0x58
	CharY                Char = 0x59
	CharZ                Char = 0x5a
	CharOpenBracket      Char = 0x5b
	CharBackslash        Char = 0x5c
	CharCloseBracket     Char = 0x5d
	CharCaret            Char = 0x5e
	CharUnderscore       Char = 0x5f
	CharBacktick         Char = 0x60
	CharLowerA           Char = 0x61
	CharLowerB           Char = 0x62
	CharLowerC           Char = 0x63
	CharLowerD           Char = 0x64
	CharLowerE           Char = 0x65
	CharLowerF           Char = 0x66
	CharLowerG           Char = 0x67
	CharLowerH           Char = 0x68
	CharLowerI           Char = 0x69
	CharLowerJ           Char = 0x6a
	CharLowerK           Char = 0x6b
	CharLowerL           Char = 0x6c
	CharLowerM           Char = 0x6d
	CharLowerN           Char = 0x6e
	CharLowerO           Char = 0x6f
	CharLowerP           Char = 0x70
	CharLowerQ           Char = 0x71
	CharLowerR           Char = 0x72
	CharLowerS           Char = 0x73
	CharLowerT           Char = 0x74
	CharLowerU           Char = 0x75
	CharLowerV           Char = 0x76
	CharLowerW           Char = 0x77
	CharLowerX           Char = 0x78
	CharLowerY           Char = 0x79
	CharLowerZ           Char = 0x7a
	CharOpenBrace        Char = 0x7b
	CharBar              Char = 0x7c
	CharCloseBrace       Char = 0x7d
	CharTilde            Char = 0x7e
	CharDelete           Char = 0x7f
)

// Other space characters
const (
	CharNonBreakingSpace   Char = 0xa0
	CharEnQuad             Char = 0x2000
	CharEmQuad             Char = 0x2001
	CharEnSpace            Char = 0x2002
	CharEmSpace            Char = 0x2003
	CharThreePerEmSpace    Char = 0x2004
	CharFourPerEmSpace     Char = 0x2005
	CharSixPerEmSpace      Char = 0x2006
	CharFigureSpace        Char = 0x2007
	CharPunctuationSpace   Char = 0x2008
	CharThinSpace          Char = 0x2009
	CharHairSpace          Char = 0x200a
	CharZeroWidthSpace     Char = 0x200b
	CharNarrowNoBreakSpace Char = 0x202f
	CharIdeographicSpace   Char = 0x3000
	CharMathematicalSpace  Char = 0x205f
	CharOgham              Char = 0x1680
)
