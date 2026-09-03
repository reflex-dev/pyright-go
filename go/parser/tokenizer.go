/*
 * tokenizer.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Based on code from vscode-python repository:
 *  https://github.com/Microsoft/vscode-python
 *
 * Converts a Python program text stream into a stream of tokens.
 *
 * Transliterated from parser/tokenizer.ts (pyright 1.1.412).
 */

package parser

import (
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/microsoft/pyright/go/common"
)

// keywords corresponds to the _keywords map.
var keywords = map[string]KeywordType{
	"and":       KeywordTypeAnd,
	"as":        KeywordTypeAs,
	"assert":    KeywordTypeAssert,
	"async":     KeywordTypeAsync,
	"await":     KeywordTypeAwait,
	"break":     KeywordTypeBreak,
	"case":      KeywordTypeCase,
	"class":     KeywordTypeClass,
	"continue":  KeywordTypeContinue,
	"__debug__": KeywordTypeDebug,
	"def":       KeywordTypeDef,
	"del":       KeywordTypeDel,
	"elif":      KeywordTypeElif,
	"else":      KeywordTypeElse,
	"except":    KeywordTypeExcept,
	"finally":   KeywordTypeFinally,
	"for":       KeywordTypeFor,
	"from":      KeywordTypeFrom,
	"global":    KeywordTypeGlobal,
	"if":        KeywordTypeIf,
	"import":    KeywordTypeImport,
	"in":        KeywordTypeIn,
	"is":        KeywordTypeIs,
	"lambda":    KeywordTypeLambda,
	"lazy":      KeywordTypeLazy,
	"match":     KeywordTypeMatch,
	"nonlocal":  KeywordTypeNonlocal,
	"not":       KeywordTypeNot,
	"or":        KeywordTypeOr,
	"pass":      KeywordTypePass,
	"raise":     KeywordTypeRaise,
	"return":    KeywordTypeReturn,
	"try":       KeywordTypeTry,
	"type":      KeywordTypeType,
	"while":     KeywordTypeWhile,
	"with":      KeywordTypeWith,
	"yield":     KeywordTypeYield,
	"False":     KeywordTypeFalse,
	"None":      KeywordTypeNone,
	"True":      KeywordTypeTrue,
}

// softKeywordNames corresponds to the _softKeywords set. Note that it does not
// include "__debug__" even though KeywordToken.IsSoftKeyword treats
// KeywordTypeDebug as soft; that asymmetry is present in the original.
var softKeywordNames = map[string]bool{
	"match": true,
	"case":  true,
	"type":  true,
	"lazy":  true,
}

// Fast-reject table: keywords are 2-9 chars long and only start with these
// character codes. A 128-entry boolean table indexed by the first code unit
// rejects most identifiers without touching the keywords map.
var keywordFirstCharTable [128]bool

const (
	keywordMinLen = 2
	keywordMaxLen = 9 // __debug__
)

type keywordEntry struct {
	text        string
	keywordType KeywordType
}

// For keyword-like identifiers, compare directly against the source text slice
// to avoid creating temporary substrings on the keyword path.
var keywordEntriesByFirstChar [128][]keywordEntry

func init() {
	for kw, kwType := range keywords {
		code := kw[0]
		if code < 128 {
			keywordFirstCharTable[code] = true
			keywordEntriesByFirstChar[code] = append(keywordEntriesByFirstChar[code], keywordEntry{text: kw, keywordType: kwType})
		}
	}
}

// getKeywordTypeFromTextSlice corresponds to getKeywordTypeFromTextSlice(). The
// second return value stands in for `undefined`.
func getKeywordTypeFromTextSlice(text common.Text, start, length int) (KeywordType, bool) {
	if length < keywordMinLen || length > keywordMaxLen {
		return 0, false
	}

	firstCharCode := text.CharCodeAt(start)
	if firstCharCode >= 128 || !keywordFirstCharTable[firstCharCode] {
		return 0, false
	}

	candidates := keywordEntriesByFirstChar[firstCharCode]
	if candidates == nil {
		return 0, false
	}

	for _, candidate := range candidates {
		if len(candidate.text) == length && textStartsWithASCII(text, candidate.text, start) {
			return candidate.keywordType, true
		}
	}

	return 0, false
}

// textStartsWithASCII corresponds to String.prototype.startsWith for the
// all-ASCII needles used here, where one code unit is one byte.
func textStartsWithASCII(text common.Text, needle string, start int) bool {
	if start+len(needle) > text.Length() {
		return false
	}
	for i := 0; i < len(needle); i++ {
		if text[start+i] != uint16(needle[i]) {
			return false
		}
	}
	return true
}

// operatorInfo corresponds to the _operatorInfo table. OperatorTypeWalrus is
// deliberately absent, as it is in the original; a missing entry reads as 0,
// which behaves the same as `undefined` at every consumer.
var operatorInfo = map[OperatorType]OperatorFlags{
	OperatorTypeAdd:                 OperatorFlagsUnary | OperatorFlagsBinary,
	OperatorTypeAddEqual:            OperatorFlagsAssignment,
	OperatorTypeAssign:              OperatorFlagsAssignment,
	OperatorTypeBitwiseAnd:          OperatorFlagsBinary,
	OperatorTypeBitwiseAndEqual:     OperatorFlagsAssignment,
	OperatorTypeBitwiseInvert:       OperatorFlagsUnary,
	OperatorTypeBitwiseOr:           OperatorFlagsBinary,
	OperatorTypeBitwiseOrEqual:      OperatorFlagsAssignment,
	OperatorTypeBitwiseXor:          OperatorFlagsBinary,
	OperatorTypeBitwiseXorEqual:     OperatorFlagsAssignment,
	OperatorTypeDivide:              OperatorFlagsBinary,
	OperatorTypeDivideEqual:         OperatorFlagsAssignment,
	OperatorTypeEquals:              OperatorFlagsBinary | OperatorFlagsComparison,
	OperatorTypeFloorDivide:         OperatorFlagsBinary,
	OperatorTypeFloorDivideEqual:    OperatorFlagsAssignment,
	OperatorTypeGreaterThan:         OperatorFlagsBinary | OperatorFlagsComparison,
	OperatorTypeGreaterThanOrEqual:  OperatorFlagsBinary | OperatorFlagsComparison,
	OperatorTypeLeftShift:           OperatorFlagsBinary,
	OperatorTypeLeftShiftEqual:      OperatorFlagsAssignment,
	OperatorTypeLessOrGreaterThan:   OperatorFlagsBinary | OperatorFlagsComparison | OperatorFlagsDeprecated,
	OperatorTypeLessThan:            OperatorFlagsBinary | OperatorFlagsComparison,
	OperatorTypeLessThanOrEqual:     OperatorFlagsBinary | OperatorFlagsComparison,
	OperatorTypeMatrixMultiply:      OperatorFlagsBinary,
	OperatorTypeMatrixMultiplyEqual: OperatorFlagsAssignment,
	OperatorTypeMod:                 OperatorFlagsBinary,
	OperatorTypeModEqual:            OperatorFlagsAssignment,
	OperatorTypeMultiply:            OperatorFlagsBinary,
	OperatorTypeMultiplyEqual:       OperatorFlagsAssignment,
	OperatorTypeNotEquals:           OperatorFlagsBinary | OperatorFlagsComparison,
	OperatorTypePower:               OperatorFlagsBinary,
	OperatorTypePowerEqual:          OperatorFlagsAssignment,
	OperatorTypeRightShift:          OperatorFlagsBinary,
	OperatorTypeRightShiftEqual:     OperatorFlagsAssignment,
	OperatorTypeSubtract:            OperatorFlagsBinary,
	OperatorTypeSubtractEqual:       OperatorFlagsAssignment,

	OperatorTypeAnd:   OperatorFlagsBinary,
	OperatorTypeOr:    OperatorFlagsBinary,
	OperatorTypeNot:   OperatorFlagsUnary,
	OperatorTypeIs:    OperatorFlagsBinary,
	OperatorTypeIsNot: OperatorFlagsBinary,
	OperatorTypeIn:    OperatorFlagsBinary,
	OperatorTypeNotIn: OperatorFlagsBinary,
}

const unsetSingleCharOperatorType = -1

var (
	singleCharOperatorTypeTable      [128]int16
	singleCharEqualOperatorTypeTable [128]int16
	repeatedCharOperatorTypeTable    [128]int16
	repeatedCharEqualOperatorTable   [128]int16
)

func init() {
	for i := range singleCharOperatorTypeTable {
		singleCharOperatorTypeTable[i] = unsetSingleCharOperatorType
		singleCharEqualOperatorTypeTable[i] = unsetSingleCharOperatorType
		repeatedCharOperatorTypeTable[i] = unsetSingleCharOperatorType
		repeatedCharEqualOperatorTable[i] = unsetSingleCharOperatorType
	}

	singleCharOperatorTypeTable[common.CharEqual] = int16(OperatorTypeAssign)
	singleCharOperatorTypeTable[common.CharPlus] = int16(OperatorTypeAdd)
	singleCharOperatorTypeTable[common.CharHyphen] = int16(OperatorTypeSubtract)
	singleCharOperatorTypeTable[common.CharAsterisk] = int16(OperatorTypeMultiply)
	singleCharOperatorTypeTable[common.CharSlash] = int16(OperatorTypeDivide)
	singleCharOperatorTypeTable[common.CharAmpersand] = int16(OperatorTypeBitwiseAnd)
	singleCharOperatorTypeTable[common.CharBar] = int16(OperatorTypeBitwiseOr)
	singleCharOperatorTypeTable[common.CharCaret] = int16(OperatorTypeBitwiseXor)
	singleCharOperatorTypeTable[common.CharPercent] = int16(OperatorTypeMod)
	singleCharOperatorTypeTable[common.CharTilde] = int16(OperatorTypeBitwiseInvert)
	singleCharOperatorTypeTable[common.CharAt] = int16(OperatorTypeMatrixMultiply)
	singleCharOperatorTypeTable[common.CharLess] = int16(OperatorTypeLessThan)
	singleCharOperatorTypeTable[common.CharGreater] = int16(OperatorTypeGreaterThan)

	singleCharEqualOperatorTypeTable[common.CharPlus] = int16(OperatorTypeAddEqual)
	singleCharEqualOperatorTypeTable[common.CharHyphen] = int16(OperatorTypeSubtractEqual)
	singleCharEqualOperatorTypeTable[common.CharAsterisk] = int16(OperatorTypeMultiplyEqual)
	singleCharEqualOperatorTypeTable[common.CharSlash] = int16(OperatorTypeDivideEqual)
	singleCharEqualOperatorTypeTable[common.CharAmpersand] = int16(OperatorTypeBitwiseAndEqual)
	singleCharEqualOperatorTypeTable[common.CharBar] = int16(OperatorTypeBitwiseOrEqual)
	singleCharEqualOperatorTypeTable[common.CharCaret] = int16(OperatorTypeBitwiseXorEqual)
	singleCharEqualOperatorTypeTable[common.CharPercent] = int16(OperatorTypeModEqual)
	singleCharEqualOperatorTypeTable[common.CharAt] = int16(OperatorTypeMatrixMultiplyEqual)

	repeatedCharOperatorTypeTable[common.CharAsterisk] = int16(OperatorTypePower)
	repeatedCharOperatorTypeTable[common.CharSlash] = int16(OperatorTypeFloorDivide)
	repeatedCharOperatorTypeTable[common.CharLess] = int16(OperatorTypeLeftShift)
	repeatedCharOperatorTypeTable[common.CharGreater] = int16(OperatorTypeRightShift)

	repeatedCharEqualOperatorTable[common.CharAsterisk] = int16(OperatorTypePowerEqual)
	repeatedCharEqualOperatorTable[common.CharSlash] = int16(OperatorTypeFloorDivideEqual)
	repeatedCharEqualOperatorTable[common.CharLess] = int16(OperatorTypeLeftShiftEqual)
	repeatedCharEqualOperatorTable[common.CharGreater] = int16(OperatorTypeRightShiftEqual)
}

func getTwoCharKey(char1, char2 common.Char) int {
	return (int(char1) << 8) | int(char2)
}

var twoCharOperatorTypeMap = map[int]OperatorType{
	getTwoCharKey(common.CharEqual, common.CharEqual):           OperatorTypeEquals,
	getTwoCharKey(common.CharExclamationMark, common.CharEqual): OperatorTypeNotEquals,
	getTwoCharKey(common.CharLess, common.CharEqual):            OperatorTypeLessThanOrEqual,
	getTwoCharKey(common.CharGreater, common.CharEqual):         OperatorTypeGreaterThanOrEqual,
	getTwoCharKey(common.CharLess, common.CharGreater):          OperatorTypeLessOrGreaterThan,
}

var twoCharSpecialTokenTypeMap = map[int]TokenType{
	getTwoCharKey(common.CharHyphen, common.CharGreater): TokenTypeArrow,
}

const byteOrderMarker = 0xfeff

const defaultTabSize = 8

// canStartString is a fast-reject table: only these ASCII chars can begin a
// string literal (quote chars or valid string prefix chars f/r/b/u/t and their
// uppercase).
var canStartString [128]bool

// asciiIdentifierContinue / asciiIdentifierStart let the tight identifier loop
// avoid function-call overhead on the common ASCII path.
var (
	asciiIdentifierContinue [128]bool
	asciiIdentifierStart    [128]bool
)

func init() {
	canStartString[common.CharSingleQuote] = true
	canStartString[common.CharDoubleQuote] = true
	for _, ch := range []common.Char{
		common.CharLowerF, common.CharF,
		common.CharLowerR, common.CharR,
		common.CharLowerB, common.CharB,
		common.CharLowerU, common.CharU,
		common.CharLowerT, common.CharT,
	} {
		canStartString[ch] = true
	}

	for i := 0; i < 128; i++ {
		if IsIdentifierChar(common.Char(i), noNextChar) {
			asciiIdentifierContinue[i] = true
		}
		if IsIdentifierStartChar(common.Char(i), noNextChar) {
			asciiIdentifierStart[i] = true
		}
	}
}

// detachSubstring corresponds to detachSubstring(). In TypeScript this exists
// to avoid V8 pinning the parent string via a SlicedString; the same hazard
// exists in Go, where a subslice retains the whole backing array, so this
// really does copy.
func detachSubstring(text common.Text, start, end int) common.Text {
	if start < 0 {
		start = 0
	}
	if end > text.Length() {
		end = text.Length()
	}
	if start >= end {
		return common.Text{}
	}
	result := make(common.Text, end-start)
	copy(result, text[start:end])
	return result
}

// cloneStr corresponds to cloneStr() in common/core.ts, for the same
// backing-array-retention reason as detachSubstring.
func cloneStr(text common.Text) common.Text {
	return detachSubstring(text, 0, text.Length())
}

// removeUnderscoresFromRange strips underscore characters from a source text
// range without first creating an intermediate substring.
func removeUnderscoresFromRange(text common.Text, start, end int) common.Text {
	firstUnderscoreIndex := -1
	for i := start; i < end; i++ {
		if text.CharCodeAt(i) == common.CharUnderscore {
			firstUnderscoreIndex = i
			break
		}
	}

	if firstUnderscoreIndex < 0 {
		return text.Substring(start, end)
	}

	var result common.TextBuilder
	result.WriteText(text.Substring(start, firstUnderscoreIndex))
	for i := firstUnderscoreIndex + 1; i < end; i++ {
		if text.CharCodeAt(i) != common.CharUnderscore {
			result.WriteChar(text.CharCodeAt(i))
		}
	}
	return result.Text()
}

// endsWithBackslashContinuation is a manual replacement for the regex /\\\s*$/.
// It checks if a range [start, end) within text ends with a backslash followed
// by optional whitespace.
func endsWithBackslashContinuation(text common.Text, start, end int) bool {
	i := end - 1
	// Skip trailing whitespace
	for i >= start {
		ch := text.CharCodeAt(i)
		if ch == common.CharSpace || ch == common.CharTab || ch == common.CharFormFeed {
			i--
		} else {
			break
		}
	}
	return i >= start && text.CharCodeAt(i) == common.CharBackslash
}

// ignoreDirectiveMatch corresponds to the IgnoreDirectiveMatch interface. The
// TypeScript version stores the matched text; only the lengths and the index of
// '[' within fullMatch are read, but both are kept as Text so those lengths
// stay in code units.
type ignoreDirectiveMatch struct {
	fullMatch         common.Text // group 0: full matched text
	prefix            common.Text // group 1: prefix before directive keyword
	bracketContent    common.Text // group 4: content inside [...] if present
	hasBracketContent bool
	index             int // match position within the input string
}

// parseIgnoreBracketContent parses a bracketed rule list starting at pos (which
// must point at '['). It returns the bracket content (without brackets) and the
// position just past ']', or ok == false if the bracket is malformed.
func parseIgnoreBracketContent(text common.Text, pos, rangeEnd int, allowColon bool) (common.Text, int, bool) {
	pos++ // skip '['
	bracketStart := pos
	for pos < rangeEnd && text.CharCodeAt(pos) != common.CharCloseBracket {
		// Only allow valid bracket content chars: \s, \w, -, ,
		// (plus ':' for type: ignore to support tool-namespaced codes)
		bc := text.CharCodeAt(pos)
		if (bc >= common.CharLowerA && bc <= common.CharLowerZ) ||
			(bc >= common.CharA && bc <= common.CharZ) ||
			(bc >= common.Char0 && bc <= common.Char9) ||
			bc == common.CharUnderscore ||
			bc == common.CharHyphen ||
			bc == common.CharComma ||
			bc == common.CharSpace ||
			bc == common.CharTab ||
			(allowColon && bc == common.CharColon) {
			pos++
		} else {
			break
		}
	}
	if pos < rangeEnd && text.CharCodeAt(pos) == common.CharCloseBracket {
		return text.Substring(bracketStart, pos), pos + 1, true
	}
	return nil, 0, false
}

// matchIgnoreDirective is a manual replacement for typeIgnoreCommentRegEx /
// pyrightIgnoreCommentRegEx. It scans text within [rangeStart, rangeEnd) for
// `<directive>: ignore [rules]` where directive is 'type' or 'pyright'. The
// returned index is absolute within text.
func matchIgnoreDirective(text common.Text, rangeStart, rangeEnd int, directive string) (*ignoreDirectiveMatch, bool) {
	// The directive can be preceded by optional `#` and whitespace, or
	// appear at the start of the range with optional whitespace.
	// type: ignore allows tool-namespaced codes (e.g. "ty:rule-name") in brackets;
	// pyright: ignore does not.
	allowColonInBracket := directive == "type"
	searchFrom := rangeStart

	for searchFrom < rangeEnd {
		// Find the next occurrence of the directive keyword, bounded by
		// rangeEnd. A bounded hand-rolled scan is important here: an unbounded
		// indexOf, when the keyword is absent from the current comment but
		// present elsewhere in the file, can scan well past rangeEnd --
		// producing O(n) behavior per comment and O(n^2) overall on
		// comment-heavy files.
		firstCharCode := common.Char(directive[0])
		directiveIdx := -1
		scanLimit := rangeEnd - len(directive)
		for i := searchFrom; i <= scanLimit; i++ {
			if text.CharCodeAt(i) == firstCharCode {
				found := true
				for d := 1; d < len(directive); d++ {
					if text.CharCodeAt(i+d) != common.Char(directive[d]) {
						found = false
						break
					}
				}
				if found {
					directiveIdx = i
					break
				}
			}
		}
		if directiveIdx < 0 {
			return nil, false
		}

		// Determine the prefix: scan backward from directiveIdx to find
		// the `#` or start-of-range, collecting whitespace.
		prefixStart := directiveIdx
		foundAnchor := false

		// Walk backward over spaces/tabs
		j := directiveIdx - 1
		for j >= rangeStart && (text.CharCodeAt(j) == common.CharSpace || text.CharCodeAt(j) == common.CharTab) {
			j--
		}

		if j < rangeStart {
			// At start of range
			prefixStart = rangeStart
			foundAnchor = true
		} else if text.CharCodeAt(j) == common.CharHash {
			prefixStart = j
			foundAnchor = true
		}

		if !foundAnchor {
			searchFrom = directiveIdx + 1
			continue
		}

		// After directive keyword, expect ':'
		pos := directiveIdx + len(directive)
		if pos >= rangeEnd || text.CharCodeAt(pos) != common.CharColon {
			searchFrom = directiveIdx + 1
			continue
		}
		pos++ // skip ':'

		// Skip optional whitespace after ':'
		for pos < rangeEnd && (text.CharCodeAt(pos) == common.CharSpace || text.CharCodeAt(pos) == common.CharTab) {
			pos++
		}

		// Expect 'ignore'
		const ignoreStr = "ignore"
		if pos+len(ignoreStr) > rangeEnd {
			searchFrom = directiveIdx + 1
			continue
		}

		matched := true
		for k := 0; k < len(ignoreStr); k++ {
			if text.CharCodeAt(pos+k) != common.Char(ignoreStr[k]) {
				matched = false
				break
			}
		}
		if !matched {
			searchFrom = directiveIdx + 1
			continue
		}
		pos += len(ignoreStr)

		// After 'ignore', expect whitespace, '[', or end-of-range
		var bracketContent common.Text
		hasBracketContent := false

		if pos >= rangeEnd {
			// End of range -- valid
		} else {
			ch := text.CharCodeAt(pos)
			if ch == common.CharSpace || ch == common.CharTab {
				// Skip whitespace to check for optional bracket
				for pos < rangeEnd && (text.CharCodeAt(pos) == common.CharSpace || text.CharCodeAt(pos) == common.CharTab) {
					pos++
				}
				if pos < rangeEnd && text.CharCodeAt(pos) == common.CharOpenBracket {
					parsedContent, newPos, ok := parseIgnoreBracketContent(text, pos, rangeEnd, allowColonInBracket)
					if !ok {
						searchFrom = directiveIdx + 1
						continue
					}
					bracketContent = parsedContent
					hasBracketContent = true
					pos = newPos
				}
			} else if ch == common.CharOpenBracket {
				// Bracket immediately after 'ignore'
				parsedContent, newPos, ok := parseIgnoreBracketContent(text, pos, rangeEnd, allowColonInBracket)
				if !ok {
					searchFrom = directiveIdx + 1
					continue
				}
				bracketContent = parsedContent
				hasBracketContent = true
				pos = newPos
			} else {
				// No space, no bracket -- not a valid match
				searchFrom = directiveIdx + 1
				continue
			}
		}

		return &ignoreDirectiveMatch{
			fullMatch:         text.Substring(prefixStart, pos),
			prefix:            text.Substring(prefixStart, directiveIdx),
			bracketContent:    bracketContent,
			hasBracketContent: hasBracketContent,
			index:             prefixStart,
		}, true
	}

	return nil, false
}

// TokenizerOutput corresponds to the TokenizerOutput interface.
type TokenizerOutput struct {
	// List of all tokens.
	Tokens *common.TextRangeCollection[Token]

	// List of ranges that comprise the lines.
	Lines *common.TextRangeCollection[common.TextRange]

	// Map of all line numbers that end in a "type: ignore" comment.
	TypeIgnoreLines map[int]*IgnoreComment

	// Map of all line numbers that end in a "pyright: ignore" comment.
	PyrightIgnoreLines map[int]*IgnoreComment

	// Program starts with a "type: ignore" comment.
	TypeIgnoreAll *IgnoreComment

	// Line-end sequence ('\n', '\r', or '\r\n').
	PredominantEndOfLineSequence string

	// True if the tokenizer was able to identify the file's predominant
	// tab sequence. False if PredominantTabSequence is set to our default.
	HasPredominantTabSequence bool

	// Tab sequence ('\t' or consecutive spaces).
	PredominantTabSequence string

	// Does the code mostly use single or double quote
	// characters for string literals?
	PredominantSingleQuoteCharacter string
}

type stringScannerOutput struct {
	escapedValue common.Text
	flags        StringTokenFlags
}

type indentInfo struct {
	tab1Spaces     int
	tab8Spaces     int
	isSpacePresent bool
	isTabPresent   bool
}

// IgnoreCommentRule corresponds to the IgnoreCommentRule interface.
type IgnoreCommentRule struct {
	Text  common.Text
	Range common.TextRange
}

// IgnoreComment corresponds to the IgnoreComment interface.
type IgnoreComment struct {
	Range common.TextRange
	// RulesList is nil where the TypeScript version has `undefined`.
	RulesList []IgnoreCommentRule
}

type fStringReplacementFieldContext struct {
	inFormatSpecifier bool
	parenDepth        int
}

type fStringContext struct {
	startToken             *FStringStartToken
	replacementFieldStack  []*fStringReplacementFieldContext
	activeReplacementField *fStringReplacementFieldContext
}

type magicsKind int

const (
	magicsKindNone magicsKind = iota
	magicsKindLine
	magicsKindCell
)

const (
	identifierCacheSize = 2048
	identifierCacheMask = identifierCacheSize - 1
)

// Tokenizer corresponds to the Tokenizer class.
type Tokenizer struct {
	cs            *CharacterStream
	tokens        []Token
	prevLineStart int
	parenDepth    int
	lineRanges    []common.TextRange
	indentAmounts []indentInfo
	typeIgnoreAll *IgnoreComment

	// Cached answer to "are there any non-trivial tokens yet?" Once true it
	// stays true, so the O(n) scan in handleComment only runs while the token
	// stream consists purely of NewLine / Indent tokens.
	hasTokenBeforeIgnoreAll bool

	typeIgnoreLines    map[int]*IgnoreComment
	pyrightIgnoreLines map[int]*IgnoreComment
	comments           []*Comment
	fStringStack       []*fStringContext
	activeFString      *fStringContext

	// Total times CR, CR/LF, and LF are used to terminate
	// lines. Used to determine the predominant line ending.
	crCount   int
	crLfCount int
	lfCount   int

	// Number of times an indent token is emitted.
	indentCount int

	// Number of times an indent token is emitted and a tab character
	// is present (used to determine predominant tab sequence).
	indentTabCount int

	// Number of spaces that are added for an indent token
	// (used to determine predominant tab sequence).
	indentSpacesTotal int

	// Number of single or double quote string literals found
	// in the code.
	singleQuoteCount int
	doubleQuoteCount int

	// Assume Jupyter notebook tokenization rules?
	useNotebookMode bool

	// Direct-mapped identifier intern cache. Indexed by a cheap hash of
	// (firstChar, lastChar, length). On a hit (slot present and text equals
	// the current source range), reuse the cached value instead of
	// re-allocating via detachSubstring. Collisions simply overwrite the slot.
	identifierCache [identifierCacheSize]common.Text
}

// NewTokenizer constructs a Tokenizer.
func NewTokenizer() *Tokenizer {
	return &Tokenizer{
		cs:                 NewCharacterStream(common.Text{}),
		tokens:             []Token{},
		lineRanges:         []common.TextRange{},
		indentAmounts:      []indentInfo{},
		typeIgnoreLines:    map[int]*IgnoreComment{},
		pyrightIgnoreLines: map[int]*IgnoreComment{},
		fStringStack:       []*fStringContext{},
	}
}

// Tokenize corresponds to tokenize(text) with all optional arguments defaulted.
func (t *Tokenizer) Tokenize(text common.Text) *TokenizerOutput {
	return t.TokenizeRange(text, 0, text.Length(), 0, false)
}

// TokenizeRange corresponds to tokenize() with all arguments supplied.
func (t *Tokenizer) TokenizeRange(text common.Text, start, length, initialParenDepth int, useNotebookMode bool) *TokenizerOutput {
	if start < 0 || start > text.Length() {
		panic(fmt.Sprintf("Invalid range start (start=%d, text.length=%d)", start, text.Length()))
	}

	if length < 0 || start+length > text.Length() {
		panic(fmt.Sprintf("Invalid range length (start=%d, length=%d, text.length=%d)", start, length, text.Length()))
	} else if start+length < text.Length() {
		text = text.Substring(0, start+length)
	}

	t.cs = NewCharacterStream(text)
	t.cs.SetPosition(start)
	t.tokens = []Token{}
	t.prevLineStart = 0
	t.parenDepth = initialParenDepth
	t.lineRanges = []common.TextRange{}
	t.indentAmounts = []indentInfo{}
	t.useNotebookMode = useNotebookMode
	// Clear per-source identifier intern cache.
	for i := range t.identifierCache {
		t.identifierCache[i] = nil
	}

	end := start + length

	if start == 0 {
		t.readIndentationAfterNewLine()
	}

	for !t.cs.IsEndOfStream() {
		t.addNextToken()

		if t.cs.Position() >= end {
			break
		}
	}

	// Insert any implied FStringEnd tokens.
	for t.activeFString != nil {
		t.tokens = append(t.tokens, NewFStringEndToken(
			t.cs.Position(),
			0,
			t.activeFString.startToken.Flags|StringTokenFlagsUnterminated,
		))
		t.activeFString = t.popFStringStack()
	}

	// Insert an implied new line to make parsing easier.
	if len(t.tokens) == 0 || t.tokens[len(t.tokens)-1].GetType() != TokenTypeNewLine {
		if t.parenDepth == 0 {
			t.tokens = append(t.tokens, NewNewLineToken(t.cs.Position(), 0, NewLineTypeImplied, t.getComments()))
		}
	}

	// Insert any implied dedent tokens.
	t.setIndent(t.cs.Position(), 0, 0 /* isSpacePresent */, false /* isTabPresent */, false)

	// Add a final end-of-stream token to make parsing easier.
	t.tokens = append(t.tokens, NewToken(TokenTypeEndOfStream, t.cs.Position(), 0, t.getComments()))

	// Add the final line range.
	t.addLineRange()

	// If the last line ended in a line-end character, add an empty line.
	if len(t.lineRanges) > 0 {
		lastLine := t.lineRanges[len(t.lineRanges)-1]
		lastCharOfLastLine := text.CharCodeAt(lastLine.Start + lastLine.Length - 1)
		if lastCharOfLastLine == common.CharCarriageReturn || lastCharOfLastLine == common.CharLineFeed {
			t.lineRanges = append(t.lineRanges, common.TextRange{Start: t.cs.Position(), Length: 0})
		}
	}

	predominantEndOfLineSequence := "\n"
	if t.crCount > t.crLfCount && t.crCount > t.lfCount {
		predominantEndOfLineSequence = "\r"
	} else if t.crLfCount > t.crCount && t.crLfCount > t.lfCount {
		predominantEndOfLineSequence = "\r\n"
	}

	predominantTabSequence := "    "
	hasPredominantTabSequence := false
	// If more than half of the indents use tab sequences,
	// assume we're using tabs rather than spaces.
	if float64(t.indentTabCount) > float64(t.indentCount)/2 {
		hasPredominantTabSequence = true
		predominantTabSequence = "\t"
	} else if t.indentCount > 0 {
		hasPredominantTabSequence = true
		// Compute the average number of spaces per indent
		// to estimate the predominant tab value.
		averageSpacePerIndent := int(jsRound(float64(t.indentSpacesTotal) / float64(t.indentCount)))
		if averageSpacePerIndent < 1 {
			averageSpacePerIndent = 1
		} else if averageSpacePerIndent > defaultTabSize {
			averageSpacePerIndent = defaultTabSize
		}
		predominantTabSequence = strings.Repeat(" ", averageSpacePerIndent)
	}

	predominantSingleQuoteCharacter := `"`
	if t.singleQuoteCount >= t.doubleQuoteCount {
		predominantSingleQuoteCharacter = "'"
	}

	return &TokenizerOutput{
		Tokens:                          common.NewTextRangeCollection(t.tokens),
		Lines:                           common.NewTextRangeCollection(t.lineRanges),
		TypeIgnoreLines:                 t.typeIgnoreLines,
		TypeIgnoreAll:                   t.typeIgnoreAll,
		PyrightIgnoreLines:              t.pyrightIgnoreLines,
		PredominantEndOfLineSequence:    predominantEndOfLineSequence,
		HasPredominantTabSequence:       hasPredominantTabSequence,
		PredominantTabSequence:          predominantTabSequence,
		PredominantSingleQuoteCharacter: predominantSingleQuoteCharacter,
	}
}

// jsRound reproduces JavaScript's Math.round, which rounds halves toward
// positive infinity rather than away from zero.
func jsRound(value float64) float64 {
	return math.Floor(value + 0.5)
}

func (t *Tokenizer) popFStringStack() *fStringContext {
	if len(t.fStringStack) == 0 {
		return nil
	}
	last := t.fStringStack[len(t.fStringStack)-1]
	t.fStringStack = t.fStringStack[:len(t.fStringStack)-1]
	return last
}

// GetOperatorInfo corresponds to Tokenizer.getOperatorInfo(). A type with no
// entry (OperatorTypeWalrus) yields 0, matching `undefined` at every consumer.
func GetOperatorInfo(operatorType OperatorType) OperatorFlags {
	return operatorInfo[operatorType]
}

// IsWhitespaceToken corresponds to Tokenizer.isWhitespace().
func IsWhitespaceToken(token Token) bool {
	tokenType := token.GetType()
	return tokenType == TokenTypeNewLine || tokenType == TokenTypeIndent || tokenType == TokenTypeDedent
}

// IsPythonKeyword corresponds to Tokenizer.isPythonKeyword().
func IsPythonKeyword(name string, includeSoftKeywords bool) bool {
	_, ok := keywords[name]
	if !ok {
		return false
	}

	if includeSoftKeywords {
		return true
	}

	return !softKeywordNames[name]
}

// IsPythonIdentifier corresponds to Tokenizer.isPythonIdentifier().
//
// Note that, as in the original, an empty string returns true and surrogate
// pairs are not considered: the loop passes each code unit individually.
func IsPythonIdentifier(value common.Text) bool {
	for i := 0; i < value.Length(); i++ {
		if i == 0 {
			if !IsIdentifierStartChar(value.CharCodeAt(i), noNextChar) {
				return false
			}
		} else if !IsIdentifierChar(value.CharCodeAt(i), noNextChar) {
			return false
		}
	}

	return true
}

// IsOperatorAssignment corresponds to Tokenizer.isOperatorAssignment().
func IsOperatorAssignment(operatorType OperatorType) bool {
	flags, ok := operatorInfo[operatorType]
	if !ok {
		return false
	}
	return (flags & OperatorFlagsAssignment) != 0
}

// IsOperatorComparison corresponds to Tokenizer.isOperatorComparison().
func IsOperatorComparison(operatorType OperatorType) bool {
	flags, ok := operatorInfo[operatorType]
	if !ok {
		return false
	}
	return (flags & OperatorFlagsComparison) != 0
}

func (t *Tokenizer) addNextToken() {
	// Are we in the middle of an f-string but not in a replacement field?
	if t.activeFString != nil &&
		(t.activeFString.activeReplacementField == nil ||
			t.activeFString.activeReplacementField.inFormatSpecifier) {
		t.handleFStringMiddle()
	} else {
		t.cs.SkipWhitespace()
	}

	if t.cs.IsEndOfStream() {
		return
	}

	if !t.handleCharacter() {
		t.cs.MoveNext()
	}
}

// handleCharacter consumes one or more characters from the character stream and
// pushes tokens onto the token list. Returns true if the caller should advance
// to the next character.
func (t *Tokenizer) handleCharacter() bool {
	// f-strings, b-strings, etc -- only check if current char can start a string
	currentChar := t.cs.CurrentChar()
	if currentChar < 128 && canStartString[currentChar] {
		stringPrefixLength := t.getStringPrefixLength()

		if stringPrefixLength >= 0 {
			var stringPrefix common.Text
			if stringPrefixLength > 0 {
				stringPrefix = t.cs.GetText().Substring(t.cs.Position(), t.cs.Position()+stringPrefixLength)
				// Indeed a string
				t.cs.Advance(stringPrefixLength)
			}

			quoteTypeFlags := t.getQuoteTypeFlags(stringPrefix)
			if quoteTypeFlags != StringTokenFlagsNone {
				t.handleString(quoteTypeFlags, stringPrefixLength)
				return true
			}
		}
	}

	if t.cs.CurrentChar() == common.CharHash {
		t.handleComment()
		return true
	}

	if t.useNotebookMode {
		kind := t.getIPythonMagicsKind()
		if kind == magicsKindLine {
			commentType := CommentTypeIPythonShellEscape
			if t.cs.CurrentChar() == common.CharPercent {
				commentType = CommentTypeIPythonMagic
			}
			t.handleIPythonMagics(commentType)
			return true
		}

		if kind == magicsKindCell {
			commentType := CommentTypeIPythonCellShellEscape
			if t.cs.CurrentChar() == common.CharPercent {
				commentType = CommentTypeIPythonCellMagic
			}
			t.handleIPythonMagics(commentType)
			return true
		}
	}

	switch t.cs.CurrentChar() {
	case byteOrderMarker:
		// Skip the BOM if it's at the start of the file.
		if t.cs.Position() == 0 {
			return false
		}
		return t.handleInvalid()

	case common.CharCarriageReturn:
		length := 1
		newLineType := NewLineTypeCarriageReturn
		if t.cs.NextChar() == common.CharLineFeed {
			length = 2
			newLineType = NewLineTypeCarriageReturnLineFeed
		}
		t.handleNewLine(length, newLineType)
		return true

	case common.CharLineFeed:
		t.handleNewLine(1, NewLineTypeLineFeed)
		return true

	case common.CharBackslash:
		if t.cs.NextChar() == common.CharCarriageReturn {
			advance := 2
			if t.cs.LookAhead(2) == common.CharLineFeed {
				advance = 3
			}

			// If a line continuation (\\ + CR[LF]) appears at EOF, it's an error.
			if t.cs.Position()+advance >= t.cs.Length() {
				return t.handleInvalid()
			}

			t.cs.Advance(advance)
			t.addLineRange()

			if len(t.tokens) > 0 && t.tokens[len(t.tokens)-1].GetType() == TokenTypeNewLine {
				t.readIndentationAfterNewLine()
			}
			return true
		}

		if t.cs.NextChar() == common.CharLineFeed {
			advance := 2

			// If a line continuation (\\ + LF) appears at EOF, it's an error.
			if t.cs.Position()+advance >= t.cs.Length() {
				return t.handleInvalid()
			}

			t.cs.Advance(advance)
			t.addLineRange()

			if len(t.tokens) > 0 && t.tokens[len(t.tokens)-1].GetType() == TokenTypeNewLine {
				t.readIndentationAfterNewLine()
			}
			return true
		}

		return t.handleInvalid()

	case common.CharOpenParenthesis:
		t.parenDepth++
		t.tokens = append(t.tokens, NewToken(TokenTypeOpenParenthesis, t.cs.Position(), 1, t.getComments()))

	case common.CharCloseParenthesis:
		if t.parenDepth > 0 {
			t.parenDepth--
		}
		t.tokens = append(t.tokens, NewToken(TokenTypeCloseParenthesis, t.cs.Position(), 1, t.getComments()))

	case common.CharOpenBracket:
		t.parenDepth++
		t.tokens = append(t.tokens, NewToken(TokenTypeOpenBracket, t.cs.Position(), 1, t.getComments()))

	case common.CharCloseBracket:
		if t.parenDepth > 0 {
			t.parenDepth--
		}
		t.tokens = append(t.tokens, NewToken(TokenTypeCloseBracket, t.cs.Position(), 1, t.getComments()))

	case common.CharOpenBrace:
		t.parenDepth++
		t.tokens = append(t.tokens, NewToken(TokenTypeOpenCurlyBrace, t.cs.Position(), 1, t.getComments()))

		if t.activeFString != nil {
			// Are we starting a new replacement field?
			if t.activeFString.activeReplacementField == nil ||
				t.activeFString.activeReplacementField.inFormatSpecifier {
				// If there is already an active replacement field, push it
				// on the stack so we can pop it later.
				if t.activeFString.activeReplacementField != nil {
					t.activeFString.replacementFieldStack = append(
						t.activeFString.replacementFieldStack, t.activeFString.activeReplacementField)
				}

				// Create a new active replacement field context.
				t.activeFString.activeReplacementField = &fStringReplacementFieldContext{
					inFormatSpecifier: false,
					parenDepth:        t.parenDepth,
				}
			}
		}

	case common.CharCloseBrace:
		if t.activeFString != nil &&
			t.activeFString.activeReplacementField != nil &&
			t.activeFString.activeReplacementField.parenDepth == t.parenDepth {
			t.activeFString.activeReplacementField = t.popReplacementField(t.activeFString)
		}

		if t.parenDepth > 0 {
			t.parenDepth--
		}
		t.tokens = append(t.tokens, NewToken(TokenTypeCloseCurlyBrace, t.cs.Position(), 1, t.getComments()))

	case common.CharComma:
		t.tokens = append(t.tokens, NewToken(TokenTypeComma, t.cs.Position(), 1, t.getComments()))

	case common.CharBacktick:
		t.tokens = append(t.tokens, NewToken(TokenTypeBacktick, t.cs.Position(), 1, t.getComments()))

	case common.CharSemicolon:
		t.tokens = append(t.tokens, NewToken(TokenTypeSemicolon, t.cs.Position(), 1, t.getComments()))

	case common.CharColon:
		if t.cs.NextChar() == common.CharEqual {
			if t.activeFString == nil ||
				t.activeFString.activeReplacementField == nil ||
				t.activeFString.activeReplacementField.parenDepth != t.parenDepth {
				t.tokens = append(t.tokens, NewOperatorToken(t.cs.Position(), 2, OperatorTypeWalrus, t.getComments()))
				t.cs.Advance(1)
				return false
			}
		}

		t.tokens = append(t.tokens, NewToken(TokenTypeColon, t.cs.Position(), 1, t.getComments()))

		if t.activeFString != nil && t.activeFString.activeReplacementField != nil &&
			t.parenDepth == t.activeFString.activeReplacementField.parenDepth {
			t.activeFString.activeReplacementField.inFormatSpecifier = true
		}

	default:
		if t.isPossibleNumber() {
			if t.tryNumber() {
				return true
			}
		}

		if t.cs.CurrentChar() == common.CharPeriod {
			if t.cs.NextChar() == common.CharPeriod && t.cs.LookAhead(2) == common.CharPeriod {
				t.tokens = append(t.tokens, NewToken(TokenTypeEllipsis, t.cs.Position(), 3, t.getComments()))
				t.cs.Advance(3)
				return true
			}
			t.tokens = append(t.tokens, NewToken(TokenTypeDot, t.cs.Position(), 1, t.getComments()))
			return false
		}

		if !t.tryIdentifier() {
			if !t.tryOperator() {
				return t.handleInvalid()
			}
		}
		return true
	}
	return false
}

func (t *Tokenizer) popReplacementField(ctx *fStringContext) *fStringReplacementFieldContext {
	if len(ctx.replacementFieldStack) == 0 {
		return nil
	}
	last := ctx.replacementFieldStack[len(ctx.replacementFieldStack)-1]
	ctx.replacementFieldStack = ctx.replacementFieldStack[:len(ctx.replacementFieldStack)-1]
	return last
}

func (t *Tokenizer) addLineRange() {
	lineLength := t.cs.Position() - t.prevLineStart
	if lineLength > 0 {
		t.lineRanges = append(t.lineRanges, common.TextRange{Start: t.prevLineStart, Length: lineLength})
	}

	t.prevLineStart = t.cs.Position()
}

func (t *Tokenizer) handleNewLine(length int, newLineType NewLineType) {
	if t.parenDepth == 0 && newLineType != NewLineTypeImplied {
		// New lines are ignored within parentheses.
		// We'll also avoid adding multiple newlines in a row to simplify parsing.
		if len(t.tokens) == 0 || t.tokens[len(t.tokens)-1].GetType() != TokenTypeNewLine {
			t.tokens = append(t.tokens, NewNewLineToken(t.cs.Position(), length, newLineType, t.getComments()))
		}
	}
	if newLineType == NewLineTypeCarriageReturn {
		t.crCount++
	} else if newLineType == NewLineTypeCarriageReturnLineFeed {
		t.crLfCount++
	} else {
		t.lfCount++
	}
	t.cs.Advance(length)
	t.addLineRange()
	t.readIndentationAfterNewLine()
}

func (t *Tokenizer) readIndentationAfterNewLine() {
	tab1Spaces := 0
	tab8Spaces := 0
	isTabPresent := false
	isSpacePresent := false

	startOffset := t.cs.Position()

	for !t.cs.IsEndOfStream() {
		switch t.cs.CurrentChar() {
		case common.CharSpace:
			tab1Spaces++
			tab8Spaces++
			isSpacePresent = true
			t.cs.MoveNext()

		case common.CharTab:
			// Translate tabs into spaces assuming both 1-space
			// and 8-space tab stops.
			tab1Spaces++
			tab8Spaces += defaultTabSize - (tab8Spaces % defaultTabSize)
			isTabPresent = true
			t.cs.MoveNext()

		case common.CharFormFeed:
			tab1Spaces = 0
			tab8Spaces = 0
			isTabPresent = false
			isSpacePresent = false
			t.cs.MoveNext()

		case common.CharHash, common.CharLineFeed, common.CharCarriageReturn:
			// Blank line -- no need to adjust indentation.
			return

		default:
			// Non-blank line. Set the current indent level.
			t.setIndent(startOffset, tab1Spaces, tab8Spaces, isSpacePresent, isTabPresent)
			return
		}
	}
}

// setIndent takes two space count values. The first assumes that tabs are
// translated into one-space tab stops. The second assumes that tabs are
// translated into eight-space tab stops.
func (t *Tokenizer) setIndent(startOffset, tab1Spaces, tab8Spaces int, isSpacePresent, isTabPresent bool) {
	// Indentations are ignored within a parenthesized clause.
	if t.parenDepth > 0 {
		return
	}

	// Insert indent or dedent tokens as necessary.
	if len(t.indentAmounts) == 0 {
		if tab8Spaces > 0 {
			t.indentCount++
			if isTabPresent {
				t.indentTabCount++
			}
			t.indentSpacesTotal += tab8Spaces

			t.indentAmounts = append(t.indentAmounts, indentInfo{
				tab1Spaces:     tab1Spaces,
				tab8Spaces:     tab8Spaces,
				isSpacePresent: isSpacePresent,
				isTabPresent:   isTabPresent,
			})
			t.tokens = append(t.tokens, NewIndentToken(startOffset, tab1Spaces, tab8Spaces, false, t.getComments()))
		}
	} else {
		prevTabInfo := t.indentAmounts[len(t.indentAmounts)-1]
		if prevTabInfo.tab8Spaces < tab8Spaces {
			// The Python spec says that if there is ambiguity about how tabs should
			// be translated into spaces because the user has intermixed tabs and
			// spaces, it should be an error. We'll record this condition in the token
			// so the parser can later report it.
			isIndentAmbiguous := ((prevTabInfo.isSpacePresent && isTabPresent) ||
				(prevTabInfo.isTabPresent && isSpacePresent)) &&
				prevTabInfo.tab1Spaces >= tab1Spaces

			t.indentCount++
			if isTabPresent {
				t.indentTabCount++
			}
			t.indentSpacesTotal += tab8Spaces - t.indentAmounts[len(t.indentAmounts)-1].tab8Spaces

			t.indentAmounts = append(t.indentAmounts, indentInfo{
				tab1Spaces:     tab1Spaces,
				tab8Spaces:     tab8Spaces,
				isSpacePresent: isSpacePresent,
				isTabPresent:   isTabPresent,
			})

			t.tokens = append(t.tokens, NewIndentToken(startOffset, tab1Spaces, tab8Spaces, isIndentAmbiguous, t.getComments()))
		} else if prevTabInfo.tab8Spaces == tab8Spaces {
			// The Python spec says that if there is ambiguity about how tabs should
			// be translated into spaces because the user has intermixed tabs and
			// spaces, it should be an error. We'll record this condition in the token
			// so the parser can later report it.
			if (prevTabInfo.isSpacePresent && isTabPresent) || (prevTabInfo.isTabPresent && isSpacePresent) {
				t.tokens = append(t.tokens, NewIndentToken(startOffset, tab1Spaces, tab8Spaces, true, t.getComments()))
			}
		} else {
			// The Python spec says that if there is ambiguity about how tabs should
			// be translated into spaces because the user has intermixed tabs and
			// spaces, it should be an error. We'll record this condition in the token
			// so the parser can later report it.
			isDedentAmbiguous := (prevTabInfo.isSpacePresent && isTabPresent) ||
				(prevTabInfo.isTabPresent && isSpacePresent)

			// The Python spec says that dedent amounts need to match the indent
			// amount exactly. An error is generated at runtime if it doesn't.
			// We'll record that error condition within the token, allowing the
			// parser to report it later.
			dedentPoints := []int{}
			for len(t.indentAmounts) > 0 &&
				t.indentAmounts[len(t.indentAmounts)-1].tab8Spaces > tab8Spaces {
				if len(t.indentAmounts) > 1 {
					dedentPoints = append(dedentPoints, t.indentAmounts[len(t.indentAmounts)-2].tab8Spaces)
				} else {
					dedentPoints = append(dedentPoints, 0)
				}
				t.indentAmounts = t.indentAmounts[:len(t.indentAmounts)-1]
			}

			for index, dedentAmount := range dedentPoints {
				matchesIndent := index < len(dedentPoints)-1 || dedentAmount == tab8Spaces
				actualDedentAmount := tab8Spaces
				if index < len(dedentPoints)-1 {
					actualDedentAmount = dedentAmount
				}
				t.tokens = append(t.tokens, NewDedentToken(
					t.cs.Position(),
					0,
					actualDedentAmount,
					matchesIndent,
					isDedentAmbiguous,
					t.getComments(),
				))

				isDedentAmbiguous = false
			}
		}
	}
}

func (t *Tokenizer) tryIdentifier() bool {
	cs := t.cs
	text := cs.GetText()
	textLen := text.Length()
	start := cs.Position()

	// Fast path for ASCII identifier start. Avoids the function call and
	// surrogate logic for the common case (Python source is overwhelmingly
	// ASCII identifiers).
	firstChar := cs.CurrentChar()
	pos := start
	if firstChar < 128 {
		if !asciiIdentifierStart[firstChar] {
			// Not an identifier start and not a surrogate candidate.
			return false
		}
		pos++

		// Tight loop: advance while we're still in ASCII identifier chars.
		for pos < textLen {
			ch := text.CharCodeAt(pos)
			if ch < 128 && asciiIdentifierContinue[ch] {
				pos++
			} else {
				break
			}
		}

		// If we hit a non-ASCII char, fall back to the generic loop to
		// handle possible unicode identifier continue / surrogate pairs.
		if pos < textLen && text.CharCodeAt(pos) >= 128 {
			cs.Advance(pos - start)
			t.swallowNonAsciiIdentifierChars()
			pos = cs.Position()
		} else {
			cs.Advance(pos - start)
		}
	} else {
		// Non-ASCII start: use the generic path (supports surrogates).
		if IsIdentifierStartChar(firstChar, noNextChar) {
			cs.MoveNext()
		} else if IsIdentifierStartChar(firstChar, int(cs.NextChar())) {
			cs.MoveNext()
			cs.MoveNext()
		} else {
			return false
		}
		t.swallowNonAsciiIdentifierChars()
		pos = cs.Position()
	}

	if pos > start {
		end := pos
		length := end - start
		keywordType, isKeyword := getKeywordTypeFromTextSlice(text, start, length)

		if isKeyword {
			t.tokens = append(t.tokens, NewKeywordToken(start, length, keywordType, t.getComments()))
		} else {
			value := t.internIdentifier(text, start, end, length)
			t.tokens = append(t.tokens, NewIdentifierToken(start, length, value, t.getComments()))
		}
		return true
	}
	return false
}

// internIdentifier is a per-tokenize identifier intern cache. Direct-mapped, so
// collisions simply replace the slot. Common identifiers (self, cls, True,
// None, str, int, dict, etc.) get deduplicated to a single allocation.
func (t *Tokenizer) internIdentifier(text common.Text, start, end, length int) common.Text {
	firstChar := text.CharCodeAt(start)
	lastChar := text.CharCodeAt(end - 1)
	// Hash mixes length, first and last char; multiplier values chosen
	// to spread hits for common short identifiers across the table.
	hash := (int(firstChar)*31 + int(lastChar)*7 + length) & identifierCacheMask
	cached := t.identifierCache[hash]
	if cached != nil && cached.Length() == length && cached.Equal(text.Substring(start, end)) {
		return cached
	}
	value := detachSubstring(text, start, end)
	t.identifierCache[hash] = value
	return value
}

// swallowNonAsciiIdentifierChars is the generic identifier-continue loop that
// handles unicode + surrogate pairs. The fast ASCII loop falls back to this
// when it encounters a non-ASCII char.
func (t *Tokenizer) swallowNonAsciiIdentifierChars() {
	for {
		if IsIdentifierChar(t.cs.CurrentChar(), noNextChar) {
			t.cs.MoveNext()
		} else if IsIdentifierChar(t.cs.CurrentChar(), int(t.cs.NextChar())) {
			t.cs.MoveNext()
			t.cs.MoveNext()
		} else {
			break
		}
	}
}

func (t *Tokenizer) isPossibleNumber() bool {
	if IsDecimal(t.cs.CurrentChar()) {
		return true
	}

	if t.cs.CurrentChar() == common.CharPeriod && IsDecimal(t.cs.NextChar()) {
		return true
	}

	return false
}

func (t *Tokenizer) tryNumber() bool {
	start := t.cs.Position()

	if t.cs.CurrentChar() == common.Char0 {
		radix := 0
		leadingChars := 0

		// Try hex => hexinteger: "0" ("x" | "X") (["_"] hexdigit)+
		if (t.cs.NextChar() == common.CharLowerX || t.cs.NextChar() == common.CharX) && IsHex(t.cs.LookAhead(2)) {
			t.cs.Advance(2)
			leadingChars = 2
			for IsHex(t.cs.CurrentChar()) {
				t.cs.MoveNext()
			}
			radix = 16
		} else if (t.cs.NextChar() == common.CharLowerB || t.cs.NextChar() == common.CharB) && IsBinary(t.cs.LookAhead(2)) {
			// Try binary => bininteger: "0" ("b" | "B") (["_"] bindigit)+
			t.cs.Advance(2)
			leadingChars = 2
			for IsBinary(t.cs.CurrentChar()) {
				t.cs.MoveNext()
			}
			radix = 2
		} else if (t.cs.NextChar() == common.CharLowerO || t.cs.NextChar() == common.CharO) && IsOctal(t.cs.LookAhead(2)) {
			// Try octal => octinteger: "0" ("o" | "O") (["_"] octdigit)+
			t.cs.Advance(2)
			leadingChars = 2
			for IsOctal(t.cs.CurrentChar()) {
				t.cs.MoveNext()
			}
			radix = 8
		}

		if radix > 0 {
			end := t.cs.Position()
			text := t.cs.GetText()
			simpleIntText := removeUnderscoresFromRange(text, start, end)
			intValue, isNaN := jsParseInt(simpleIntText.Slice(leadingChars), radix)

			if !isNaN {
				bigIntValue, bigOK := jsBigInt(simpleIntText)
				value := NewFloatValue(intValue)
				if bigOK && (math.IsInf(intValue, 0) || intValue < minSafeInteger || intValue > maxSafeInteger) {
					value = NewBigIntValue(bigIntValue)
				}

				t.tokens = append(t.tokens, NewNumberToken(start, end-start, value, true, false, t.getComments()))
				return true
			}
		}
	}

	isDecimalInteger := false
	mightBeFloatingPoint := false
	// Try decimal int =>
	//    decinteger: nonzerodigit (["_"] digit)* | "0" (["_"] "0")*
	//    nonzerodigit: "1"..."9"
	//    digit: "0"..."9"
	if t.cs.CurrentChar() >= common.Char1 && t.cs.CurrentChar() <= common.Char9 {
		for IsDecimal(t.cs.CurrentChar()) {
			mightBeFloatingPoint = true
			t.cs.MoveNext()
		}
		isDecimalInteger = t.cs.CurrentChar() != common.CharPeriod &&
			t.cs.CurrentChar() != common.CharLowerE &&
			t.cs.CurrentChar() != common.CharE
	}

	// "0" (["_"] "0")*
	if t.cs.CurrentChar() == common.Char0 {
		mightBeFloatingPoint = true
		for t.cs.CurrentChar() == common.Char0 || t.cs.CurrentChar() == common.CharUnderscore {
			t.cs.MoveNext()
		}
		isDecimalInteger = t.cs.CurrentChar() != common.CharPeriod &&
			t.cs.CurrentChar() != common.CharLowerE &&
			t.cs.CurrentChar() != common.CharE &&
			(t.cs.CurrentChar() < common.Char1 || t.cs.CurrentChar() > common.Char9)
	}

	if isDecimalInteger {
		textEnd := t.cs.Position()
		sourceText := t.cs.GetText()
		simpleIntText := removeUnderscoresFromRange(sourceText, start, textEnd)
		intValue, isNaN := jsParseInt(simpleIntText, 10)

		if !isNaN {
			isImaginary := false
			tokenLength := textEnd - start

			bigIntValue, bigOK := jsBigInt(simpleIntText)
			value := NewFloatValue(intValue)
			// Note that, unlike the hex/octal/binary branch above, this
			// compares the exact bigint against the safe-integer bounds
			// rather than the already-rounded double.
			if bigOK && (math.IsInf(intValue, 0) ||
				bigIntValue.Cmp(minSafeIntegerBig) < 0 ||
				bigIntValue.Cmp(maxSafeIntegerBig) > 0) {
				value = NewBigIntValue(bigIntValue)
			}

			if t.cs.CurrentChar() == common.CharLowerJ || t.cs.CurrentChar() == common.CharJ {
				isImaginary = true
				t.cs.MoveNext()
				tokenLength += 1
			}

			t.tokens = append(t.tokens, NewNumberToken(start, tokenLength, value, true, isImaginary, t.getComments()))
			return true
		}
	}

	// Floating point. Sign and leading digits were already skipped over.
	t.cs.SetPosition(start)
	if mightBeFloatingPoint ||
		(t.cs.CurrentChar() == common.CharPeriod && t.cs.NextChar() >= common.Char0 && t.cs.NextChar() <= common.Char9) {
		if t.skipFloatingPointCandidate() {
			floatEnd := t.cs.Position()
			floatText := removeUnderscoresFromRange(t.cs.GetText(), start, floatEnd)
			value, isNaN := jsParseFloat(floatText)
			if !isNaN {
				isImaginary := false
				tokenLength := floatEnd - start
				if t.cs.CurrentChar() == common.CharLowerJ || t.cs.CurrentChar() == common.CharJ {
					isImaginary = true
					t.cs.MoveNext()
					tokenLength += 1
				}
				t.tokens = append(t.tokens, NewNumberToken(start, tokenLength, NewFloatValue(value), false, isImaginary, t.getComments()))
				return true
			}
		}
	}

	t.cs.SetPosition(start)
	return false
}

const (
	maxSafeInteger = 9007199254740991.0
	minSafeInteger = -9007199254740991.0
)

var (
	maxSafeIntegerBig = big.NewInt(9007199254740991)
	minSafeIntegerBig = big.NewInt(-9007199254740991)
)

func (t *Tokenizer) tryOperator() bool {
	currentChar := t.cs.CurrentChar()
	length := 0
	nextChar := t.cs.NextChar()
	var operatorType OperatorType

	if currentChar < 128 && nextChar < 128 {
		twoCharKey := (int(currentChar) << 8) | int(nextChar)
		if specialTokenType, ok := twoCharSpecialTokenTypeMap[twoCharKey]; ok {
			t.tokens = append(t.tokens, NewToken(specialTokenType, t.cs.Position(), 2, t.getComments()))
			t.cs.Advance(2)
			return true
		}

		if twoCharOperatorType, ok := twoCharOperatorTypeMap[twoCharKey]; ok {
			t.tokens = append(t.tokens, NewOperatorToken(t.cs.Position(), 2, twoCharOperatorType, t.getComments()))
			t.cs.Advance(2)
			return true
		}

		if currentChar == nextChar {
			repeatedOperatorType := repeatedCharOperatorTypeTable[currentChar]
			if repeatedOperatorType != unsetSingleCharOperatorType {
				hasTrailingEqual := t.cs.LookAhead(2) == common.CharEqual
				repeatedLength := 2
				resolvedType := OperatorType(repeatedOperatorType)
				if hasTrailingEqual {
					repeatedLength = 3
					resolvedType = OperatorType(repeatedCharEqualOperatorTable[currentChar])
				}
				t.tokens = append(t.tokens, NewOperatorToken(t.cs.Position(), repeatedLength, resolvedType, t.getComments()))
				t.cs.Advance(repeatedLength)
				return true
			}
		}
	}

	if currentChar < 128 {
		singleCharOperatorType := singleCharOperatorTypeTable[currentChar]
		if singleCharOperatorType != unsetSingleCharOperatorType {
			equalOperatorType := singleCharEqualOperatorTypeTable[currentChar]
			if nextChar == common.CharEqual && equalOperatorType != unsetSingleCharOperatorType {
				length = 2
				operatorType = OperatorType(equalOperatorType)
			} else {
				length = 1
				operatorType = OperatorType(singleCharOperatorType)
			}

			t.tokens = append(t.tokens, NewOperatorToken(t.cs.Position(), length, operatorType, t.getComments()))
			t.cs.Advance(length)
			return true
		}
	}

	// `!=` is handled by the 2-char fast path above.
	if currentChar == common.CharExclamationMark && t.activeFString != nil {
		// Handle the conversion separator (!) within an f-string.
		t.tokens = append(t.tokens, NewToken(TokenTypeExclamationMark, t.cs.Position(), 1, t.getComments()))
		t.cs.Advance(1)
		return true
	}

	return false
}

func (t *Tokenizer) handleInvalid() bool {
	start := t.cs.Position()
	for {
		if t.cs.CurrentChar() == common.CharLineFeed ||
			t.cs.CurrentChar() == common.CharCarriageReturn ||
			t.cs.IsAtWhiteSpace() ||
			t.cs.IsEndOfStream() {
			break
		}

		if IsSurrogateChar(t.cs.CurrentChar()) {
			t.cs.MoveNext()
			t.cs.MoveNext()
		} else {
			t.cs.MoveNext()
		}
	}
	length := t.cs.Position() - start
	if length > 0 {
		t.tokens = append(t.tokens, NewToken(TokenTypeInvalid, start, length, t.getComments()))
		return true
	}
	return false
}

func (t *Tokenizer) getComments() []*Comment {
	prevComments := t.comments
	t.comments = nil
	return prevComments
}

func (t *Tokenizer) getIPythonMagicsKind() magicsKind {
	curChar := t.cs.CurrentChar()
	if curChar != common.CharPercent && curChar != common.CharExclamationMark {
		return magicsKindNone
	}

	if len(t.tokens) > 0 {
		prevToken := t.tokens[len(t.tokens)-1]
		if !IsWhitespaceToken(prevToken) {
			return magicsKindNone
		}
	}

	if t.cs.NextChar() == curChar {
		// Eat up next magic char.
		t.cs.MoveNext()
		return magicsKindCell
	}

	return magicsKindLine
}

func (t *Tokenizer) handleIPythonMagics(commentType CommentType) {
	start := t.cs.Position() + 1
	sourceText := t.cs.GetText()

	begin := start
	for {
		t.cs.SkipToEol()

		if commentType == CommentTypeIPythonMagic || commentType == CommentTypeIPythonShellEscape {
			// is it multiline magics?
			// %magic command \
			//        next arguments
			if !endsWithBackslashContinuation(sourceText, begin, t.cs.Position()) {
				break
			}
		}

		t.cs.MoveNext()
		begin = t.cs.Position() + 1

		if t.cs.IsEndOfStream() {
			break
		}
	}

	length := t.cs.Position() - start
	comment := NewComment(start, length, sourceText.Substring(start, start+length), commentType)
	t.addComments(comment)
}

func (t *Tokenizer) handleComment() {
	start := t.cs.Position() + 1
	t.cs.SkipToEol()

	length := t.cs.Position() - start
	sourceText := t.cs.GetText()
	end := start + length

	// The original's comment: fast pre-filter: any ignore directive must contain
	// the substring 'ignore'. indexOf is a highly-optimized native call and lets
	// us skip the full directive scan for the vast majority of comments (which
	// are free-form text).
	//
	// `sourceText.indexOf('ignore', start)`, bounded to the comment -- see
	// indexOfFromWithin for why the bound is here and why it changes no answer.
	ignoreIdx := indexOfFromWithin(sourceText, "ignore", start, end)
	if ignoreIdx >= 0 && ignoreIdx < end {
		if typeIgnoreMatch, ok := matchIgnoreDirective(sourceText, start, end, "type"); ok {
			commentStart := typeIgnoreMatch.index
			textRange := common.TextRange{
				Start:  commentStart + typeIgnoreMatch.prefix.Length(),
				Length: typeIgnoreMatch.fullMatch.Length() - typeIgnoreMatch.prefix.Length(),
			}
			ignoreComment := &IgnoreComment{
				Range:     textRange,
				RulesList: t.getIgnoreCommentRulesList(commentStart, typeIgnoreMatch),
			}

			isIgnoreAll := false
			if !t.hasTokenBeforeIgnoreAll {
				// Are there any tokens other than NewLine / Indent yet?
				hasOther := false
				for _, token := range t.tokens {
					if token != nil && token.GetType() != TokenTypeNewLine && token.GetType() != TokenTypeIndent {
						hasOther = true
						break
					}
				}
				if hasOther {
					t.hasTokenBeforeIgnoreAll = true
				} else {
					isIgnoreAll = true
				}
			}

			if isIgnoreAll {
				t.typeIgnoreAll = ignoreComment
			} else {
				t.typeIgnoreLines[len(t.lineRanges)] = ignoreComment
			}
		}

		if pyrightIgnoreMatch, ok := matchIgnoreDirective(sourceText, start, end, "pyright"); ok {
			commentStart := pyrightIgnoreMatch.index
			textRange := common.TextRange{
				Start:  commentStart + pyrightIgnoreMatch.prefix.Length(),
				Length: pyrightIgnoreMatch.fullMatch.Length() - pyrightIgnoreMatch.prefix.Length(),
			}
			ignoreComment := &IgnoreComment{
				Range:     textRange,
				RulesList: t.getIgnoreCommentRulesList(commentStart, pyrightIgnoreMatch),
			}
			t.pyrightIgnoreLines[len(t.lineRanges)] = ignoreComment
		}
	}

	comment := NewComment(start, length, sourceText.Substring(start, end), CommentTypeRegular)
	t.addComments(comment)
}

// indexOfFromWithin is `text.indexOf(needle, from)` restricted to matches that
// *begin* before limit.
//
// The original passes no limit -- it searches to the end of the file and then
// discards any result at or past the comment's end. That is the same answer, and
// its own comment explains why it can afford it: "indexOf is a highly-optimized
// native call". V8's is a SIMD search; a hand-written scan over []uint16 is not,
// and running it from every comment to end-of-file makes the tokenizer
// O(comments x file size). It was 3% of a whole-project run.
//
// Two things keep this identical rather than merely equivalent. The needle may
// still run past limit, exactly as indexOf would -- what is bounded is where a
// match may *start*, which is precisely what the discarded comparison tested.
// And the scan checks the first code unit inline before calling out, so the
// per-position cost is a load and a compare.
func indexOfFromWithin(text common.Text, needle string, from int, limit int) int {
	if from < 0 {
		from = 0
	}
	if len(needle) == 0 {
		return from
	}

	last := text.Length() - len(needle)
	if limit-1 < last {
		last = limit - 1
	}

	first := uint16(needle[0])
	for i := from; i <= last; i++ {
		if text[i] != first {
			continue
		}
		if textStartsWithASCII(text, needle, i) {
			return i
		}
	}
	return -1
}

// getIgnoreCommentRulesList extracts the individual rules within a
// "type: ignore [x, y, z]" comment. It returns nil where the TypeScript version
// returns undefined.
func (t *Tokenizer) getIgnoreCommentRulesList(start int, match *ignoreDirectiveMatch) []IgnoreCommentRule {
	if !match.hasBracketContent {
		return nil
	}

	splitElements := splitText(match.bracketContent, common.CharComma)
	commentRules := []IgnoreCommentRule{}
	currentOffset := start + match.fullMatch.IndexOfString("[") + 1

	for _, element := range splitElements {
		frontTrimmed := trimStartText(element)
		currentOffset += element.Length() - frontTrimmed.Length()
		endTrimmed := trimEndText(frontTrimmed)

		if endTrimmed.Length() > 0 {
			commentRules = append(commentRules, IgnoreCommentRule{
				Range: common.TextRange{Start: currentOffset, Length: endTrimmed.Length()},
				Text:  cloneStr(endTrimmed),
			})
		}

		currentOffset += frontTrimmed.Length() + 1
	}

	return commentRules
}

// splitText corresponds to String.prototype.split for a single-character
// separator.
func splitText(text common.Text, separator common.Char) []common.Text {
	result := []common.Text{}
	segmentStart := 0
	for i := 0; i < text.Length(); i++ {
		if text.CharCodeAt(i) == separator {
			result = append(result, text.Substring(segmentStart, i))
			segmentStart = i + 1
		}
	}
	result = append(result, text.Substring(segmentStart, text.Length()))
	return result
}

// isJSWhitespace reports whether a code unit is trimmed by
// String.prototype.trimStart / trimEnd.
func isJSWhitespace(ch common.Char) bool {
	switch ch {
	case 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return ch >= 0x2000 && ch <= 0x200a
}

func trimStartText(text common.Text) common.Text {
	i := 0
	for i < text.Length() && isJSWhitespace(text.CharCodeAt(i)) {
		i++
	}
	return text.Substring(i, text.Length())
}

func trimEndText(text common.Text) common.Text {
	i := text.Length()
	for i > 0 && isJSWhitespace(text.CharCodeAt(i-1)) {
		i--
	}
	return text.Substring(0, i)
}

func (t *Tokenizer) addComments(comment *Comment) {
	t.comments = append(t.comments, comment)
}

func (t *Tokenizer) getStringPrefixLength() int {
	if t.cs.CurrentChar() == common.CharSingleQuote || t.cs.CurrentChar() == common.CharDoubleQuote {
		// Simple string, no prefix
		return 0
	}

	if t.cs.NextChar() == common.CharSingleQuote || t.cs.NextChar() == common.CharDoubleQuote {
		switch t.cs.CurrentChar() {
		case common.CharLowerF, common.CharF,
			common.CharLowerR, common.CharR,
			common.CharLowerB, common.CharB,
			common.CharLowerU, common.CharU,
			common.CharLowerT, common.CharT:
			// Single-char prefix like u"" or r""
			return 1
		}
	}

	if t.cs.LookAhead(2) == common.CharSingleQuote || t.cs.LookAhead(2) == common.CharDoubleQuote {
		// Only ASCII letters can produce one of the two-char prefixes below,
		// so ASCII lowering is equivalent to String.prototype.toLowerCase here.
		prefix := asciiToLower(t.cs.GetText().Substring(t.cs.Position(), t.cs.Position()+2)).String()
		switch prefix {
		case "rf", "fr", "rt", "tr", "br", "rb":
			return 2
		}
	}
	return -1
}

// asciiToLower lowercases the ASCII letters of text, leaving other code units
// untouched.
func asciiToLower(text common.Text) common.Text {
	result := make(common.Text, text.Length())
	for i := 0; i < text.Length(); i++ {
		ch := text[i]
		if ch >= 'A' && ch <= 'Z' {
			ch += 'a' - 'A'
		}
		result[i] = ch
	}
	return result
}

func (t *Tokenizer) getQuoteTypeFlags(prefix common.Text) StringTokenFlags {
	flags := StringTokenFlagsNone

	// The prefix is always drawn from the ASCII set validated by
	// getStringPrefixLength, so ASCII lowering matches toLowerCase.
	prefix = asciiToLower(prefix)
	for i := 0; i < prefix.Length(); i++ {
		switch prefix.CharCodeAt(i) {
		case common.CharLowerU:
			flags |= StringTokenFlagsUnicode

		case common.CharLowerB:
			flags |= StringTokenFlagsBytes

		case common.CharLowerR:
			flags |= StringTokenFlagsRaw

		case common.CharLowerF:
			flags |= StringTokenFlagsFormat

		case common.CharLowerT:
			flags |= StringTokenFlagsTemplate
		}
	}

	if t.cs.CurrentChar() == common.CharSingleQuote {
		flags |= StringTokenFlagsSingleQuote
		if t.cs.NextChar() == common.CharSingleQuote && t.cs.LookAhead(2) == common.CharSingleQuote {
			flags |= StringTokenFlagsTriplicate
		}
	} else if t.cs.CurrentChar() == common.CharDoubleQuote {
		flags |= StringTokenFlagsDoubleQuote
		if t.cs.NextChar() == common.CharDoubleQuote && t.cs.LookAhead(2) == common.CharDoubleQuote {
			flags |= StringTokenFlagsTriplicate
		}
	}

	return flags
}

func (t *Tokenizer) handleString(flags StringTokenFlags, stringPrefixLength int) {
	start := t.cs.Position() - stringPrefixLength

	if flags&(StringTokenFlagsFormat|StringTokenFlagsTemplate) != 0 {
		if flags&StringTokenFlagsTriplicate != 0 {
			t.cs.Advance(3)
		} else {
			t.cs.MoveNext()
		}

		end := t.cs.Position()

		fStringStartToken := NewFStringStartToken(
			start,
			end-start,
			flags,
			stringPrefixLength,
			t.getComments(),
		)

		// Create a new f-string context and push it on the stack.
		ctx := &fStringContext{
			startToken:            fStringStartToken,
			replacementFieldStack: []*fStringReplacementFieldContext{},
		}

		if t.activeFString != nil {
			t.fStringStack = append(t.fStringStack, t.activeFString)
		}
		t.activeFString = ctx

		t.tokens = append(t.tokens, fStringStartToken)
	} else {
		if flags&StringTokenFlagsTriplicate != 0 {
			t.cs.Advance(3)
		} else {
			t.cs.MoveNext()

			if flags&StringTokenFlagsSingleQuote != 0 {
				t.singleQuoteCount++
			} else {
				t.doubleQuoteCount++
			}
		}

		stringLiteralInfo := t.skipToEndOfStringLiteral(flags, false)
		end := t.cs.Position()

		// If this is an unterminated string, see if it matches the string type
		// of an active f-string. If so, we'll treat it as an f-string end
		// token rather than an unterminated regular string. This helps with
		// parse error recovery if a closing bracket is missing in an f-string.
		if (stringLiteralInfo.flags&StringTokenFlagsUnterminated) != 0 &&
			t.activeFString != nil && t.activeFString.activeReplacementField != nil {
			if flags&(StringTokenFlagsBytes|
				StringTokenFlagsUnicode|
				StringTokenFlagsRaw|
				StringTokenFlagsFormat|
				StringTokenFlagsTemplate) == 0 {
				quoteTypeMask := StringTokenFlagsTriplicate | StringTokenFlagsDoubleQuote | StringTokenFlagsSingleQuote
				if (t.activeFString.startToken.Flags & quoteTypeMask) == (flags & quoteTypeMask) {
					// Unwind to the start of this string token and terminate any replacement fields
					// that are active. This will cause the tokenizer to re-process the quote as an
					// FStringEnd token.
					t.cs.SetPosition(start)
					for len(t.activeFString.replacementFieldStack) > 0 {
						t.activeFString.activeReplacementField = t.popReplacementField(t.activeFString)
					}
					t.parenDepth = t.activeFString.activeReplacementField.parenDepth - 1
					t.activeFString.activeReplacementField = nil
					return
				}
			}
		}

		t.tokens = append(t.tokens, NewStringToken(
			start,
			end-start,
			stringLiteralInfo.flags,
			stringLiteralInfo.escapedValue,
			stringPrefixLength,
			t.getComments(),
		))
	}
}

// handleFStringMiddle scans for either the FString end token or a replacement
// field.
func (t *Tokenizer) handleFStringMiddle() {
	activeFString := t.activeFString
	inFormatSpecifier := activeFString.activeReplacementField != nil &&
		activeFString.activeReplacementField.inFormatSpecifier
	start := t.cs.Position()
	flags := activeFString.startToken.Flags
	stringLiteralInfo := t.skipToEndOfStringLiteral(flags, inFormatSpecifier)
	end := t.cs.Position()

	isUnterminated := (stringLiteralInfo.flags & StringTokenFlagsUnterminated) != 0
	sawReplacementFieldStart := (stringLiteralInfo.flags & StringTokenFlagsReplacementFieldStart) != 0
	sawReplacementFieldEnd := (stringLiteralInfo.flags & StringTokenFlagsReplacementFieldEnd) != 0
	sawEndQuote := !isUnterminated && !sawReplacementFieldStart && !sawReplacementFieldEnd

	middleTokenLength := end - start
	if sawEndQuote {
		middleTokenLength -= activeFString.startToken.QuoteMarkLength
	}

	if middleTokenLength > 0 || isUnterminated {
		t.tokens = append(t.tokens, NewFStringMiddleToken(
			start,
			middleTokenLength,
			stringLiteralInfo.flags,
			stringLiteralInfo.escapedValue,
		))
	}

	if sawEndQuote {
		t.tokens = append(t.tokens, NewFStringEndToken(
			start+middleTokenLength,
			activeFString.startToken.QuoteMarkLength,
			stringLiteralInfo.flags,
		))

		t.activeFString = t.popFStringStack()
	} else if isUnterminated {
		t.activeFString = t.popFStringStack()
	}
}

func (t *Tokenizer) skipToEndOfStringLiteral(flags StringTokenFlags, inFormatSpecifier bool) stringScannerOutput {
	quoteChar := common.CharDoubleQuote
	if flags&StringTokenFlagsSingleQuote != 0 {
		quoteChar = common.CharSingleQuote
	}
	isTriplicate := (flags & StringTokenFlagsTriplicate) != 0
	isFString := (flags & (StringTokenFlagsFormat | StringTokenFlagsTemplate)) != 0
	isInNamedUnicodeEscape := false
	start := t.cs.Position()
	escapedValueLength := 0
	getEscapedValue := func() common.Text {
		return cloneStr(t.cs.GetText().Substring(start, start+escapedValueLength))
	}

	for {
		if t.cs.IsEndOfStream() {
			// Hit the end of file without a termination.
			flags |= StringTokenFlagsUnterminated
			return stringScannerOutput{escapedValue: getEscapedValue(), flags: flags}
		}

		if t.cs.CurrentChar() == common.CharBackslash {
			escapedValueLength++

			// Move past the escape (backslash) character.
			t.cs.MoveNext()

			// Handle the special escape sequence /N{name} for unicode characters.
			if !isInNamedUnicodeEscape &&
				t.cs.GetCurrentChar() == common.CharN &&
				t.cs.NextChar() == common.CharOpenBrace {
				flags |= StringTokenFlagsNamedUnicodeEscape
				isInNamedUnicodeEscape = true
			} else {
				// If this is an f-string, the only escapes that are allowed is for
				// a single or double quote symbol or a newline/carriage return.
				isEscapedQuote := t.cs.GetCurrentChar() == common.CharSingleQuote ||
					t.cs.GetCurrentChar() == common.CharDoubleQuote
				isEscapedNewLine := t.cs.GetCurrentChar() == common.CharCarriageReturn ||
					t.cs.GetCurrentChar() == common.CharLineFeed
				isEscapedBackslash := t.cs.GetCurrentChar() == common.CharBackslash

				if !isFString || isEscapedBackslash || isEscapedQuote || isEscapedNewLine {
					if isEscapedNewLine {
						if t.cs.GetCurrentChar() == common.CharCarriageReturn &&
							t.cs.NextChar() == common.CharLineFeed {
							escapedValueLength++
							t.cs.MoveNext()
						}
						escapedValueLength++
						t.cs.MoveNext()
						t.addLineRange()
					} else {
						escapedValueLength++
						t.cs.MoveNext()
					}
				}
			}
		} else if t.cs.CurrentChar() == common.CharLineFeed || t.cs.CurrentChar() == common.CharCarriageReturn {
			if !isTriplicate {
				if !isFString || t.activeFString == nil || t.activeFString.activeReplacementField == nil {
					// Unterminated single-line string
					flags |= StringTokenFlagsUnterminated
					return stringScannerOutput{escapedValue: getEscapedValue(), flags: flags}
				}
			}

			// Skip over the new line (either one or two characters).
			if t.cs.CurrentChar() == common.CharCarriageReturn && t.cs.NextChar() == common.CharLineFeed {
				escapedValueLength++
				t.cs.MoveNext()
			}

			escapedValueLength++
			t.cs.MoveNext()
			t.addLineRange()
		} else if !isTriplicate && t.cs.CurrentChar() == quoteChar {
			t.cs.MoveNext()
			break
		} else if isTriplicate &&
			t.cs.CurrentChar() == quoteChar &&
			t.cs.NextChar() == quoteChar &&
			t.cs.LookAhead(2) == quoteChar {
			t.cs.Advance(3)
			break
		} else if !isInNamedUnicodeEscape && isFString && t.cs.CurrentChar() == common.CharOpenBrace {
			if inFormatSpecifier || t.cs.NextChar() != common.CharOpenBrace {
				flags |= StringTokenFlagsReplacementFieldStart
				break
			} else {
				escapedValueLength++
				t.cs.MoveNext()
				escapedValueLength++
				t.cs.MoveNext()
			}
		} else if isInNamedUnicodeEscape && t.cs.CurrentChar() == common.CharCloseBrace {
			isInNamedUnicodeEscape = false
			escapedValueLength++
			t.cs.MoveNext()
		} else if isFString && t.cs.CurrentChar() == common.CharCloseBrace {
			if inFormatSpecifier || t.cs.NextChar() != common.CharCloseBrace {
				flags |= StringTokenFlagsReplacementFieldEnd
				break
			} else {
				escapedValueLength++
				t.cs.MoveNext()
				escapedValueLength++
				t.cs.MoveNext()
			}
		} else {
			escapedValueLength++
			t.cs.MoveNext()
		}
	}

	return stringScannerOutput{escapedValue: getEscapedValue(), flags: flags}
}

func (t *Tokenizer) skipFloatingPointCandidate() bool {
	// Determine end of the potential floating point number
	start := t.cs.Position()
	t.skipFractionalNumber()
	if t.cs.Position() > start {
		// Optional exponent sign
		if t.cs.CurrentChar() == common.CharLowerE || t.cs.CurrentChar() == common.CharE {
			t.cs.MoveNext()

			// Skip exponent value
			t.skipDecimalNumber( /* allowSign */ true)
		}
	}
	return t.cs.Position() > start
}

func (t *Tokenizer) skipFractionalNumber() {
	t.skipDecimalNumber(false)
	if t.cs.CurrentChar() == common.CharPeriod {
		// Optional period
		t.cs.MoveNext()
	}
	t.skipDecimalNumber(false)
}

func (t *Tokenizer) skipDecimalNumber(allowSign bool) {
	if allowSign && (t.cs.CurrentChar() == common.CharHyphen || t.cs.CurrentChar() == common.CharPlus) {
		// Optional sign
		t.cs.MoveNext()
	}
	for IsDecimal(t.cs.CurrentChar()) {
		// Skip integer part
		t.cs.MoveNext()
	}
}

// GetLineEndOffset corresponds to getLineEndOffset() in common/positionUtils.ts,
// which takes a TokenizerOutput. It lives here because common cannot import
// parser.
func GetLineEndOffset(tokenizerOutput *TokenizerOutput, text common.Text, line int) int {
	return common.GetLineEndOffsetInLines(tokenizerOutput.Lines, text, line)
}

// GetLineEndPosition corresponds to getLineEndPosition() in
// common/positionUtils.ts.
func GetLineEndPosition(tokenizerOutput *TokenizerOutput, text common.Text, line int) common.Position {
	return common.ConvertOffsetToPosition(GetLineEndOffset(tokenizerOutput, text, line), tokenizerOutput.Lines)
}
