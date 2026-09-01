/*
 * typeprinter.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Converts a type into a string representation.
 *
 * Transliterated from analyzer/typePrinter.ts (pyright 1.1.412).
 *
 * This file holds the flags enum, the three public entry points and the
 * literal-value helpers. The six mutually-recursive printer functions live in
 * typeprinter_print.go, and UniqueNameMap plus the name helpers in
 * typeprinter_names.go.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/parser"
)

// PrintTypeFlags corresponds to the const enum of the same name.
type PrintTypeFlags int

const (
	PrintTypeFlagsNone PrintTypeFlags = 0

	// PrintTypeFlagsPrintUnknownWithAny avoids printing "Unknown" and always
	// uses "Any" instead.
	PrintTypeFlagsPrintUnknownWithAny PrintTypeFlags = 1 << 0

	// PrintTypeFlagsOmitTypeArgsIfUnknown omits type arguments for generic
	// classes if they are "Unknown".
	PrintTypeFlagsOmitTypeArgsIfUnknown PrintTypeFlags = 1 << 1

	// PrintTypeFlagsOmitUnannotatedParamType omits printing the type for a
	// param if the type is not specified.
	PrintTypeFlagsOmitUnannotatedParamType PrintTypeFlags = 1 << 2

	// PrintTypeFlagsPEP604 prints Union and Optional in PEP 604 format.
	PrintTypeFlagsPEP604 PrintTypeFlags = 1 << 3

	// PrintTypeFlagsParenthesizeUnion includes parentheses around a union if
	// there's more than one subtype.
	PrintTypeFlagsParenthesizeUnion PrintTypeFlags = 1 << 4

	// PrintTypeFlagsExpandTypeAlias expands type aliases to display their
	// individual parts.
	PrintTypeFlagsExpandTypeAlias PrintTypeFlags = 1 << 5

	// PrintTypeFlagsOmitConditionalConstraint omits "*" for types that are
	// conditionally constrained when used with constrained TypeVars.
	PrintTypeFlagsOmitConditionalConstraint PrintTypeFlags = 1 << 6

	// PrintTypeFlagsParenthesizeCallable includes parentheses around a
	// callable.
	PrintTypeFlagsParenthesizeCallable PrintTypeFlags = 1 << 7

	// PrintTypeFlagsPythonSyntax limits output to legal Python syntax.
	PrintTypeFlagsPythonSyntax PrintTypeFlags = 1 << 8

	// PrintTypeFlagsUseTypingUnpack uses Unpack instead of "*" for unpacked
	// tuples and TypeVarTuples. Requires Python 3.11 or newer.
	PrintTypeFlagsUseTypingUnpack PrintTypeFlags = 1 << 9

	// PrintTypeFlagsExpandTypedDictArgs expands TypedDict kwargs to show the
	// keys from the TypedDict instead of **kwargs.
	PrintTypeFlagsExpandTypedDictArgs PrintTypeFlags = 1 << 10

	// PrintTypeFlagsPrintTypeVarVariance prints the variance of a type
	// parameter.
	PrintTypeFlagsPrintTypeVarVariance PrintTypeFlags = 1 << 11

	// PrintTypeFlagsUseFullyQualifiedNames uses the fully-qualified name of
	// classes, type aliases, modules and functions rather than short names.
	PrintTypeFlagsUseFullyQualifiedNames PrintTypeFlags = 1 << 12

	// PrintTypeFlagsOmitTypeVarScope omits TypeVar scopes.
	PrintTypeFlagsOmitTypeVarScope PrintTypeFlags = 1 << 13
)

// FunctionReturnTypeCallback corresponds to the type of the same name.
type FunctionReturnTypeCallback func(t *FunctionType) Type

// PrintType corresponds to printType.
func PrintType(t Type, printTypeFlags PrintTypeFlags, returnTypeCallback FunctionReturnTypeCallback) string {
	uniqueNameMap := NewUniqueNameMap(printTypeFlags, returnTypeCallback)
	uniqueNameMap.Build(t, nil, 0)

	recursionTypes := []Type{}
	return printTypeInternal(t, printTypeFlags, returnTypeCallback, uniqueNameMap, &recursionTypes, 0)
}

// PrintFunctionParts corresponds to printFunctionParts. The original returns
// the tuple [string[], string]; that becomes two results.
func PrintFunctionParts(
	t *FunctionType,
	printTypeFlags PrintTypeFlags,
	returnTypeCallback FunctionReturnTypeCallback,
) ([]string, string) {
	uniqueNameMap := NewUniqueNameMap(printTypeFlags, returnTypeCallback)
	uniqueNameMap.Build(t, nil, 0)

	recursionTypes := []Type{}
	return printFunctionPartsInternal(t, printTypeFlags, returnTypeCallback, uniqueNameMap, &recursionTypes, 0)
}

// PrintObjectTypeForClass corresponds to printObjectTypeForClass.
func PrintObjectTypeForClass(
	t *ClassType,
	printTypeFlags PrintTypeFlags,
	returnTypeCallback FunctionReturnTypeCallback,
) string {
	uniqueNameMap := NewUniqueNameMap(printTypeFlags, returnTypeCallback)
	uniqueNameMap.Build(t, nil, 0)

	recursionTypes := []Type{}
	return printObjectTypeForClassInternal(t, printTypeFlags, returnTypeCallback, uniqueNameMap, &recursionTypes, 0)
}

// maxLiteralStringLength is the point past which string literals are truncated.
const maxLiteralStringLength = 50

// IsLiteralValueTruncated corresponds to isLiteralValueTruncated.
func IsLiteralValueTruncated(t *ClassType) bool {
	if s, ok := t.Priv.LiteralValue.(LiteralString); ok {
		// The original compares against the JavaScript string length, i.e. the
		// UTF-16 code unit count. Go's len() counts bytes, so this counts runes
		// via utf16 length instead.
		if literalUTF16Length(string(s)) > maxLiteralStringLength {
			return true
		}
	}

	return false
}

// PrintLiteralValueTruncated corresponds to printLiteralValueTruncated.
func PrintLiteralValueTruncated(t *ClassType) string {
	if t.Shared.Name == "bytes" {
		return "bytes"
	}

	assert(t.Shared.Name == "str", "")
	return "LiteralString"
}

// PrintLiteralValue corresponds to printLiteralValue. The TypeScript defaults
// quotation to "'".
func PrintLiteralValue(t *ClassType, quotation string) string {
	literalValue := t.Priv.LiteralValue
	if literalValue == nil {
		return ""
	}

	var literalStr string
	switch v := literalValue.(type) {
	case LiteralString:
		effectiveLiteralValue := string(v)

		// Limit the length of the string literal.
		if literalUTF16Length(effectiveLiteralValue) > maxLiteralStringLength {
			effectiveLiteralValue = truncateUTF16(effectiveLiteralValue, maxLiteralStringLength) + "…"
		}

		if t.Shared.Name == "bytes" {
			literalStr = PrintBytesLiteral(effectiveLiteralValue)
		} else {
			literalStr = PrintStringLiteral(effectiveLiteralValue, quotation)
		}

	case LiteralBool:
		if v {
			literalStr = "True"
		} else {
			literalStr = "False"
		}

	case *EnumLiteral:
		literalStr = v.ClassName + "." + v.ItemName

	case LiteralInt:
		// The bigint arm. The original strips a trailing "n", which
		// BigInt.prototype.toString() never produces; preserved anyway.
		if v.Value == nil {
			literalStr = "0"
		} else {
			literalStr = v.Value.String()
		}
		literalStr = strings.TrimSuffix(literalStr, "n")

	default:
		// The `number` arm and SentinelLiteral both land here via toString().
		switch other := literalValue.(type) {
		case LiteralFloat:
			literalStr = jsFloatToStringForLiteral(float64(other))
		case *SentinelLiteral:
			// JavaScript would call Object.prototype.toString here, giving
			// "[object Object]". Reproduced rather than "improved", because
			// changing it would change printed output.
			literalStr = "[object Object]"
		}
	}

	return literalStr
}

// GetPrintTypeFlags corresponds to getPrintTypeFlags.
func GetPrintTypeFlags(configOptions *ConfigOptions) PrintTypeFlags {
	flags := PrintTypeFlagsNone

	if configOptions.DiagnosticRuleSet.PrintUnknownAsAny {
		flags |= PrintTypeFlagsPrintUnknownWithAny
	}

	if configOptions.DiagnosticRuleSet.OmitConditionalConstraint {
		flags |= PrintTypeFlagsOmitConditionalConstraint
	}

	if configOptions.DiagnosticRuleSet.OmitTypeArgsIfUnknown {
		flags |= PrintTypeFlagsOmitTypeArgsIfUnknown
	}

	if configOptions.DiagnosticRuleSet.OmitUnannotatedParamType {
		flags |= PrintTypeFlagsOmitUnannotatedParamType
	}

	if configOptions.DiagnosticRuleSet.Pep604Printing {
		flags |= PrintTypeFlagsPEP604
	}

	return flags
}

// literalUTF16Length returns the length the original would see, since
// JavaScript string length counts UTF-16 code units.
func literalUTF16Length(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// truncateUTF16 is substring(0, n) measured in UTF-16 code units. It will not
// split a surrogate pair, which JavaScript's substring would; a split pair
// cannot be represented in a Go string at all.
func truncateUTF16(s string, n int) string {
	count := 0
	for i, r := range s {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if count+width > n {
			return s[:i]
		}
		count += width
		if count == n {
			return s[:i+len(string(r))]
		}
	}
	return s
}

// jsFloatToStringForLiteral renders the `number` arm of LiteralValue the way
// JavaScript's Number.prototype.toString does, matching parser.NumberValue.
func jsFloatToStringForLiteral(f float64) string {
	return parser.NewFloatValue(f).String()
}
