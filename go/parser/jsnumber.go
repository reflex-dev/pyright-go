/*
 * jsnumber.go
 *
 * There is no TypeScript counterpart to this file. The tokenizer converts
 * numeric literals with the JavaScript built-ins parseInt, parseFloat and
 * BigInt, and the exact value it stores in a NumberToken (including when it
 * decides to switch from a double to a bigint) depends on their precise
 * semantics. These reimplement the three of them over UTF-16 text.
 */

package parser

import (
	"math"
	"math/big"
	"strconv"

	"github.com/microsoft/pyright/go/common"
	"strings"
)

// jsParseInt reproduces parseInt(string, radix) for radix 2, 8, 10 and 16.
// The second return value reports NaN.
//
// Per ECMA-262, the result is the Number value for the mathematical integer
// denoted by the digits -- i.e. the exact integer correctly rounded to a
// double, not a left-to-right accumulation in floating point -- so this
// accumulates exactly and rounds once at the end.
func jsParseInt(text common.Text, radix int) (float64, bool) {
	i := 0
	length := text.Length()

	// Skip leading whitespace.
	for i < length && isJSWhitespace(text.CharCodeAt(i)) {
		i++
	}

	negative := false
	if i < length {
		if ch := text.CharCodeAt(i); ch == common.CharPlus || ch == common.CharHyphen {
			negative = ch == common.CharHyphen
			i++
		}
	}

	// An explicit 0x/0X prefix is stripped when the radix is 16.
	if radix == 16 && i+1 < length &&
		text.CharCodeAt(i) == common.Char0 &&
		(text.CharCodeAt(i+1) == common.CharLowerX || text.CharCodeAt(i+1) == common.CharX) {
		i += 2
	}

	digitsStart := i
	value := new(big.Int)
	radixBig := big.NewInt(int64(radix))
	digit := new(big.Int)

	for i < length {
		d, ok := digitValue(text.CharCodeAt(i), radix)
		if !ok {
			break
		}
		value.Mul(value, radixBig)
		digit.SetInt64(int64(d))
		value.Add(value, digit)
		i++
	}

	if i == digitsStart {
		// No digits at all: NaN.
		return 0, true
	}

	result, _ := new(big.Float).SetInt(value).Float64()
	if negative {
		result = -result
	}
	return result, false
}

func digitValue(ch common.Char, radix int) (int, bool) {
	var value int
	switch {
	case ch >= common.Char0 && ch <= common.Char9:
		value = int(ch - common.Char0)
	case ch >= common.CharLowerA && ch <= common.CharLowerZ:
		value = int(ch-common.CharLowerA) + 10
	case ch >= common.CharA && ch <= common.CharZ:
		value = int(ch-common.CharA) + 10
	default:
		return 0, false
	}

	if value >= radix {
		return 0, false
	}
	return value, true
}

// jsBigInt reproduces the BigInt(string) constructor for the forms the
// tokenizer produces: an optionally 0x/0o/0b-prefixed integer literal. Where
// BigInt throws a SyntaxError, this reports ok == false.
func jsBigInt(text common.Text) (*big.Int, bool) {
	i := 0
	length := text.Length()

	for i < length && isJSWhitespace(text.CharCodeAt(i)) {
		i++
	}
	end := length
	for end > i && isJSWhitespace(text.CharCodeAt(end-1)) {
		end--
	}

	if i == end {
		// BigInt("") is 0n.
		return big.NewInt(0), true
	}

	negative := false
	if ch := text.CharCodeAt(i); ch == common.CharPlus || ch == common.CharHyphen {
		negative = ch == common.CharHyphen
		i++
	}

	radix := 10
	if i+1 < end && text.CharCodeAt(i) == common.Char0 {
		switch text.CharCodeAt(i + 1) {
		case common.CharLowerX, common.CharX:
			radix = 16
			i += 2
		case common.CharLowerO, common.CharO:
			radix = 8
			i += 2
		case common.CharLowerB, common.CharB:
			radix = 2
			i += 2
		}
	}

	if i >= end {
		return nil, false
	}

	value := new(big.Int)
	radixBig := big.NewInt(int64(radix))
	digit := new(big.Int)
	for ; i < end; i++ {
		d, ok := digitValue(text.CharCodeAt(i), radix)
		if !ok {
			return nil, false
		}
		value.Mul(value, radixBig)
		digit.SetInt64(int64(d))
		value.Add(value, digit)
	}

	if negative {
		value.Neg(value)
	}
	return value, true
}

// jsParseFloat reproduces parseFloat(string): it parses the longest prefix that
// forms a valid StrDecimalLiteral and ignores the rest, returning NaN when no
// such prefix exists. The second return value reports NaN.
//
// Go's strconv.ParseFloat is stricter than parseFloat -- it rejects trailing
// garbage and forms like "1." or ".5e" -- so the valid prefix is measured first
// and handed to strconv, which does the correctly-rounded conversion.
func jsParseFloat(text common.Text) (float64, bool) {
	i := 0
	length := text.Length()

	for i < length && isJSWhitespace(text.CharCodeAt(i)) {
		i++
	}

	start := i

	if i < length {
		if ch := text.CharCodeAt(i); ch == common.CharPlus || ch == common.CharHyphen {
			i++
		}
	}

	// "Infinity" is accepted by parseFloat.
	if matchesASCII(text, "Infinity", i) {
		i += len("Infinity")
		if text.CharCodeAt(start) == common.CharHyphen {
			return math.Inf(-1), false
		}
		return math.Inf(1), false
	}

	intDigits := 0
	for i < length && isASCIIDigit(text.CharCodeAt(i)) {
		i++
		intDigits++
	}

	fracDigits := 0
	if i < length && text.CharCodeAt(i) == common.CharPeriod {
		i++
		for i < length && isASCIIDigit(text.CharCodeAt(i)) {
			i++
			fracDigits++
		}
	}

	if intDigits == 0 && fracDigits == 0 {
		return 0, true
	}

	// The exponent is only consumed if it is well formed; otherwise the valid
	// prefix ends before it.
	validEnd := i
	if i < length && (text.CharCodeAt(i) == common.CharLowerE || text.CharCodeAt(i) == common.CharE) {
		j := i + 1
		if j < length {
			if ch := text.CharCodeAt(j); ch == common.CharPlus || ch == common.CharHyphen {
				j++
			}
		}
		expDigits := 0
		for j < length && isASCIIDigit(text.CharCodeAt(j)) {
			j++
			expDigits++
		}
		if expDigits > 0 {
			validEnd = j
		}
	}

	literal := text.Substring(start, validEnd).String()

	// strconv rejects a trailing '.', which parseFloat accepts.
	if len(literal) > 0 && literal[len(literal)-1] == '.' {
		literal = literal[:len(literal)-1]
	}
	// It likewise rejects a leading '.', which parseFloat accepts.
	if len(literal) > 0 && literal[0] == '.' {
		literal = "0" + literal
	} else if len(literal) > 1 && (literal[0] == '+' || literal[0] == '-') && literal[1] == '.' {
		literal = string(literal[0]) + "0" + literal[1:]
	}

	value, err := strconv.ParseFloat(literal, 64)
	if err != nil {
		// A range error still yields the (possibly infinite or zero) value that
		// strconv computed, which is what parseFloat returns too.
		if numErr, ok := err.(*strconv.NumError); ok && numErr.Err == strconv.ErrRange {
			return value, false
		}
		return 0, true
	}
	return value, false
}

func isASCIIDigit(ch common.Char) bool {
	return ch >= common.Char0 && ch <= common.Char9
}

func matchesASCII(text common.Text, needle string, start int) bool {
	return textStartsWithASCII(text, needle, start)
}

// String renders a NumberValue the way JavaScript's Number.prototype.toString
// and BigInt.prototype.toString do with no radix argument.
//
// This matters because both codeFlowTypes.createKeyForReference and
// parseTreeUtils.printExpression interpolate `node.d.value.toString()` straight
// into user-visible strings, so any difference in formatting shows up in
// diagnostics and in code-flow reference keys.
//
// Go's strconv does not have the same thresholds: ECMA-262 switches to
// exponential notation only when the decimal exponent n satisfies n > 21 or
// n <= -6, whereas strconv's 'g' verb switches based on the precision. The
// digits themselves are the same in both -- the shortest decimal that round
// trips -- so this takes strconv's digits and re-places the decimal point.
func (v NumberValue) String() string {
	if v.IsBigInt {
		if v.BigInt == nil {
			return "0"
		}
		return v.BigInt.String()
	}
	return jsFloatToString(v.Float)
}

// jsFloatToString implements ECMA-262 Number::toString for radix 10.
func jsFloatToString(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if f == 0 {
		// JavaScript prints both 0 and -0 as "0".
		return "0"
	}
	if f < 0 {
		return "-" + jsFloatToString(-f)
	}
	if math.IsInf(f, 1) {
		return "Infinity"
	}

	// 'e' with precision -1 gives the shortest round-tripping digit string,
	// formatted as d.dddde±dd. Split it into the digit string s (k digits) and
	// the exponent, from which n = exponent + 1 as the spec defines it.
	formatted := strconv.FormatFloat(f, 'e', -1, 64)
	mantissa, expPart, _ := strings.Cut(formatted, "e")
	exp, err := strconv.Atoi(expPart)
	if err != nil {
		// Unreachable for a finite float; fall back rather than panic.
		return strconv.FormatFloat(f, 'g', -1, 64)
	}

	s := strings.Replace(mantissa, ".", "", 1)
	k := len(s)
	n := exp + 1

	switch {
	case k <= n && n <= 21:
		// s followed by n-k zeros.
		return s + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		// s with a decimal point inserted after n digits.
		return s[:n] + "." + s[n:]
	case -6 < n && n <= 0:
		// "0." then -n zeros then s.
		return "0." + strings.Repeat("0", -n) + s
	}

	// Exponential form. JavaScript writes a single leading digit, drops the
	// fraction entirely when there is only one digit, and always signs the
	// exponent.
	sign := "+"
	e := n - 1
	if e < 0 {
		sign = "-"
		e = -e
	}
	if k == 1 {
		return s + "e" + sign + strconv.Itoa(e)
	}
	return s[:1] + "." + s[1:] + "e" + sign + strconv.Itoa(e)
}
