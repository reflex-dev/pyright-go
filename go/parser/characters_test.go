package parser

import (
	"testing"

	"github.com/microsoft/pyright/go/common"
)

// TestFastTableUnchangedByFullBuild pins the assumption documented on
// ensureIdentifierCharMap: the full (non-fastTableOnly) build in characters.ts
// refills the fast table with exactly the values the startup build already
// wrote, so skipping that refill in Go is behavior-preserving.
func TestFastTableUnchangedByFullBuild(t *testing.T) {
	afterStartupBuild := identifierCharFastTable

	// Reproduce what the TypeScript full build would write into the fast table.
	var fullBuild [identifierCharFastTableSize]charCategory
	fill := func(tables []UnicodeRangeTable, category charCategory) {
		for _, table := range tables {
			for _, entry := range table {
				for i := entry.Start; i <= entry.End; i++ {
					if i < identifierCharFastTableSize {
						fullBuild[i] = category
					}
				}
			}
		}
	}
	fill(identifierCharRanges, charCategoryIdentifierChar)
	fill(startIdentifierCharRanges, charCategoryStartIdentifierChar)

	if afterStartupBuild != fullBuild {
		for i := range fullBuild {
			if afterStartupBuild[i] != fullBuild[i] {
				t.Fatalf("fast table differs at %#x: startup build %d, full build %d",
					i, afterStartupBuild[i], fullBuild[i])
			}
		}
	}
}

func TestIsIdentifierStartChar(t *testing.T) {
	tests := []struct {
		ch   common.Char
		want bool
	}{
		{common.CharLowerA, true},
		{common.CharZ, true},
		{common.CharUnderscore, true},
		{common.Char0, false},
		{common.Char9, false},
		{common.CharSpace, false},
		{common.CharDollar, false},
		{0x00e9, true},  // é, category Ll
		{0x00b7, false}, // middle dot: identifier char but not a start char
		{0x0301, false}, // combining acute: category Mn
	}

	for _, tt := range tests {
		if got := IsIdentifierStartChar(tt.ch, noNextChar); got != tt.want {
			t.Errorf("IsIdentifierStartChar(%#x) = %v, want %v", tt.ch, got, tt.want)
		}
	}
}

func TestIsIdentifierChar(t *testing.T) {
	tests := []struct {
		ch   common.Char
		want bool
	}{
		{common.CharLowerA, true},
		{common.Char0, true},
		{common.CharUnderscore, true},
		{common.CharSpace, false},
		{common.CharHyphen, false},
		{0x00b7, true},  // middle dot has the Other_ID_Continue property
		{0x0301, true},  // combining acute, category Mn
		{0x2028, false}, // line separator
	}

	for _, tt := range tests {
		if got := IsIdentifierChar(tt.ch, noNextChar); got != tt.want {
			t.Errorf("IsIdentifierChar(%#x) = %v, want %v", tt.ch, got, tt.want)
		}
	}
}

func TestSurrogatePairIdentifiers(t *testing.T) {
	// U+10400 DESERET CAPITAL LETTER LONG I is category Lu and encodes as the
	// surrogate pair D801 DC00. It must be recognized only via the pair.
	const hi = 0xd801
	const lo = 0xdc00

	if !IsSurrogateChar(hi) {
		t.Fatalf("expected %#x to be a surrogate lead", hi)
	}
	if !IsIdentifierStartChar(hi, lo) {
		t.Errorf("expected the pair %#x %#x to start an identifier", hi, lo)
	}
	if IsIdentifierStartChar(hi, noNextChar) {
		t.Errorf("a lead surrogate alone must not start an identifier")
	}
	// A lead surrogate followed by something not in its table is not an
	// identifier character.
	if IsIdentifierStartChar(hi, 0x0041) {
		t.Errorf("expected an unmatched follower to be rejected")
	}
}

func TestCharacterClassPredicates(t *testing.T) {
	if !IsWhiteSpace(common.CharSpace) || !IsWhiteSpace(common.CharTab) || !IsWhiteSpace(common.CharFormFeed) {
		t.Error("IsWhiteSpace missed a whitespace character")
	}
	if IsWhiteSpace(common.CharLineFeed) {
		t.Error("a line feed is not whitespace for the tokenizer's purposes")
	}
	if !IsLineBreak(common.CharCarriageReturn) || !IsLineBreak(common.CharLineFeed) {
		t.Error("IsLineBreak missed a line break")
	}
	if !IsHex(common.CharLowerF) || !IsHex(common.CharF) || IsHex(common.CharLowerG) {
		t.Error("IsHex is wrong")
	}
	if !IsOctal(common.Char7) || IsOctal(common.Char8) {
		t.Error("IsOctal is wrong")
	}
	if !IsBinary(common.Char1) || IsBinary(common.Char2) {
		t.Error("IsBinary is wrong")
	}
	// Underscore is accepted by every numeric predicate, as in the original.
	if !IsDecimal(common.CharUnderscore) || !IsHex(common.CharUnderscore) ||
		!IsOctal(common.CharUnderscore) || !IsBinary(common.CharUnderscore) {
		t.Error("underscore must be accepted by the numeric predicates")
	}
}
