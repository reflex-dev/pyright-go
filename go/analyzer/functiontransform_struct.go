/*
 * functiontransform_struct.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/functionTransform.ts (pyright 1.1.412):
 * applyStructUnpackTransform, parseStructFormat and getStructElementType.
 *
 * `struct.unpack("iid", data)` returns `tuple[int, int, float]` at runtime, but
 * typeshed can only declare `tuple[Any, ...]` -- the element types are encoded in
 * a *string literal*, which no annotation can express. This reads that string and
 * synthesizes the precise tuple.
 *
 * Every failure path returns the caller's result unchanged rather than reporting.
 * That is the right shape for a transform: a format string that is not a literal,
 * or contains a code this does not model, simply falls back to typeshed's
 * declared type. Reporting would turn a limitation of the transform into a user
 * error.
 *
 * The native-mode distinction is a real struct rule rather than a nicety. The
 * `n`, `N` and `P` codes exist only when no byte-order prefix is given (or `@`);
 * under `<`, `>`, `=` or `!` the runtime raises struct.error. Since this cannot
 * report, an explicit prefix with those codes falls back rather than pretending
 * they yield int.
 *
 * The count handling has three distinct cases and conflating them would be
 * wrong. `4i` is four ints; `4s` is *one* four-byte bytes value, because for `s`
 * and `p` the count is a length rather than a repeat; and `4x` is four pad bytes
 * that produce no elements at all.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// maxStructUnpackElementCount corresponds to the constant of the same name.
const maxStructUnpackElementCount = 256

// structElementType corresponds to the StructElementType union: the synthesized
// element type produced by a single struct format code.
type structElementType string

const (
	structElementInt   structElementType = "int"
	structElementFloat structElementType = "float"
	structElementBool  structElementType = "bool"
	structElementBytes structElementType = "bytes"
	// structElementPad is the original's separate 'pad' arm, which is not a
	// StructElementType there. It produces no element.
	structElementPad  structElementType = "pad"
	structElementNone structElementType = ""
)

// StructUnpackKind corresponds to the type alias of the same name: the kind of
// return type synthesized for a dispatched `struct` function -- a tuple
// (unpack/unpack_from) or an iterator of tuples (iter_unpack).
type StructUnpackKind string

const (
	StructUnpackKindTuple    StructUnpackKind = "tuple"
	StructUnpackKindIterator StructUnpackKind = "iterator"
)

// ApplyStructUnpackTransform corresponds to applyStructUnpackTransform.
func ApplyStructUnpackTransform(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	argList []*Arg,
	result *FunctionResult,
	kind StructUnpackKind,
) *FunctionResult {
	// The original's comment: the format string is always the first positional
	// argument.
	if len(argList) == 0 {
		return result
	}

	formatType := evaluator.GetTypeOfArg(argList[0], nil).Type
	if !IsClassInstance(formatType) {
		return result
	}
	formatClass := formatType.(*ClassType)

	// The original's comment: the format must be a `str` or `bytes` literal. Both
	// store their value as a string in `literalValue`.
	if !ClassTypeIsBuiltInNamed(formatClass, "str", "bytes") {
		return result
	}

	formatValue, ok := formatClass.Priv.LiteralValue.(LiteralString)
	if !ok {
		return result
	}

	elementKinds, ok := parseStructFormat(string(formatValue))
	if !ok || len(elementKinds) > maxStructUnpackElementCount {
		return result
	}

	elementTypeCache := map[structElementType]Type{}
	getElementType := func(elementKind structElementType) Type {
		if elementType, found := elementTypeCache[elementKind]; found {
			return elementType
		}
		builtInType := evaluator.GetBuiltInObject(errorNode, string(elementKind), nil)
		if !IsClassInstance(builtInType) {
			return nil
		}
		elementTypeCache[elementKind] = builtInType
		return builtInType
	}

	tupleArgs := []*TupleTypeArg{}
	for _, elementKind := range elementKinds {
		elementType := getElementType(elementKind)
		if elementType == nil {
			return result
		}
		tupleArgs = append(tupleArgs, &TupleTypeArg{Type: elementType, IsUnbounded: false})
	}

	tupleType := MakeTupleObject(evaluator, tupleArgs, false)

	if kind == StructUnpackKindTuple {
		transformed := *result
		transformed.ReturnType = tupleType
		return &transformed
	}

	// The original's comment: iter_unpack returns an Iterator of the synthesized
	// tuple type.
	iteratorType := evaluator.GetTypingType(errorNode, "Iterator")
	if iteratorType == nil || !IsInstantiableClass(iteratorType) {
		return result
	}

	iteratorInstance := ClassTypeCloneAsInstance(ClassTypeSpecialize(
		iteratorType.(*ClassType), []Type{tupleType}, nil, false, nil, nil), true)

	transformed := *result
	transformed.ReturnType = iteratorInstance
	return &transformed
}

// parseStructFormat corresponds to the function of the same name. The original's
// comment: parses a struct format string
// (https://docs.python.org/3/library/struct.html) into the sequence of element
// types produced by `struct.unpack`. Returns undefined if the format string
// contains an unrecognized format code.
func parseStructFormat(format string) ([]structElementType, bool) {
	elements := []structElementType{}
	index := 0

	// The original's comment: an optional leading byte-order/size/alignment
	// character. The 'n', 'N', and 'P' codes are only valid in native mode (no
	// prefix or '@'); under an explicit byte-order prefix ('=', '<', '>', '!')
	// they raise struct.error.
	isNativeMode := true
	if index < len(format) && isStructPrefixChar(format[index]) {
		isNativeMode = format[index] == '@'
		index++
	}

	for index < len(format) {
		ch := format[index]

		// The original's comment: whitespace between format codes is ignored.
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f' || ch == '\v' {
			index++
			continue
		}

		// The original's comment: an optional repeat count precedes the format
		// code.
		count := -1
		if ch >= '0' && ch <= '9' {
			count = 0
			for index < len(format) && format[index] >= '0' && format[index] <= '9' {
				// The original's comment: guard against pathologically large
				// counts. This bounds the accumulator itself; the produced element
				// count is bounded separately below so byte-length codes ('s'/'p')
				// aren't rejected for large counts.
				if count > (1<<53-1)/10 {
					return nil, false
				}
				count = count*10 + int(format[index]-'0')
				index++

				if count > 1<<53-1 {
					return nil, false
				}
			}

			// The original's comment: a count must be followed by a format code.
			if index >= len(format) {
				return nil, false
			}
		}

		code := format[index]
		index++

		elementKind := getStructElementType(code, isNativeMode)
		if elementKind == structElementNone {
			return nil, false
		}

		switch {
		case code == 's' || code == 'p':
			// The original's comment: for 's' and 'p', the count is the byte
			// length of a single value, so it always produces exactly one element
			// regardless of count.
			elements = append(elements, structElementBytes)

		case elementKind == structElementPad:
			// The original's comment: pad bytes ('x') produce no elements.

		default:
			repeat := count
			if count < 0 {
				repeat = 1
			}
			for i := 0; i < repeat; i++ {
				elements = append(elements, elementKind)

				// The original's comment: bound the produced element count to
				// avoid performance issues with very large repeat counts.
				if len(elements) > maxStructUnpackElementCount {
					return nil, false
				}
			}
		}
	}

	return elements, true
}

// isStructPrefixChar is the original's `'@=<>!'.includes(...)`.
func isStructPrefixChar(ch byte) bool {
	return ch == '@' || ch == '=' || ch == '<' || ch == '>' || ch == '!'
}

// getStructElementType corresponds to the function of the same name. It returns
// structElementNone where the original returns undefined.
func getStructElementType(code byte, isNativeMode bool) structElementType {
	switch code {
	case 'x':
		return structElementPad

	case 'c', 's', 'p':
		return structElementBytes

	case 'b', 'B', 'h', 'H', 'i', 'I', 'l', 'L', 'q', 'Q':
		return structElementInt

	case 'n', 'N', 'P':
		// The original's comment: these codes are only available in native mode
		// (no prefix or '@'). Under an explicit byte-order prefix they are
		// invalid, so fall back to the declared return type rather than
		// synthesizing 'int'.
		if isNativeMode {
			return structElementInt
		}
		return structElementNone

	case '?':
		return structElementBool

	case 'e', 'f', 'd':
		return structElementFloat

	default:
		return structElementNone
	}
}
