/*
 * typeevaluator_stringlist.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfStringList.
 *
 * A StringListNode is one or more adjacent string literals, which Python
 * concatenates at parse time. Four different types can come out of it, and which
 * one depends on more than the characters:
 *
 *   - In a type-expression context (StrLiteralAsType), the string is a forward
 *     reference and is parsed as a type. That branch leaves immediately.
 *   - A t-string yields string.templatelib.Template.
 *   - An f-string yields plain `str`, or `LiteralString` when every piece of it
 *     is itself a literal string -- the interpolations included.
 *   - Anything else yields `Literal["..."]` with the concatenated value.
 *
 * Mixing bytes and str in one concatenation is an error, and the check runs
 * before any of that: it finds the first of each kind and reports on whichever
 * came second, so the error points at the piece that broke the run.
 *
 * The TypeForm block at the end is the subtle part. A plain string literal in a
 * TypeForm context may ALSO denote a type, so the same node gets both a str
 * literal type and a TypeForm attached. It is guarded three ways -- only when
 * something actually wants a TypeForm, only for a single simple string, and only
 * under 256 characters -- because parsing every string literal as a type would
 * be both expensive and recursion-prone.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// maxTypeFormStringLength is the original's constant of the same name.
const maxTypeFormStringLength = 256

// getTypeOfStringList corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfStringList(
	node *parser.StringListNode, flags EvalFlags, inferenceContext *InferenceContext,
) *TypeResult {
	if (flags&EvalFlagsStrLiteralAsType) != 0 &&
		((flags&EvalFlagsTypeFormArg) == 0 || (flags&EvalFlagsNoConvertSpecialForm) != 0) {
		return e.getTypeOfStringListAsType(node, flags)
	}

	// The original's comment: check for mixing of bytes and str, which is not
	// allowed.
	firstStrIndex, firstBytesIndex := -1, -1
	for i, str := range node.D.Strings {
		if isBytesStringNode(str) {
			if firstBytesIndex < 0 {
				firstBytesIndex = i
			}
		} else if firstStrIndex < 0 {
			firstStrIndex = i
		}
	}

	if firstStrIndex >= 0 && firstBytesIndex >= 0 {
		// Report on whichever came second, which is the piece that broke the run.
		later := firstBytesIndex
		if firstStrIndex > later {
			later = firstStrIndex
		}
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.MixingBytesAndStr(), node.D.Strings[later], nil)
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	isBytes := firstBytesIndex >= 0
	isLiteralString := true
	isIncomplete := false
	isTemplate := false

	for _, expr := range node.D.Strings {
		// The original's comment: handle implicit concatenation.
		strTypeResult := e.getTypeOfString(expr)

		if strTypeResult.IsIncomplete {
			isIncomplete = true
		}

		isExprLiteralString := false

		if IsClassInstance(strTypeResult.Type) {
			cls := strTypeResult.Type.(*ClassType)
			if ClassTypeIsBuiltInNamed(cls, "str") && cls.Priv.LiteralValue != nil {
				isExprLiteralString = true
			} else if ClassTypeIsBuiltInNamed(cls, "LiteralString") {
				isExprLiteralString = true
			}

			if cls.Shared.Name == "Template" {
				isTemplate = true
			}
		}

		if !isExprLiteralString {
			isLiteralString = false
		}
	}

	typeResult := e.stringListValueType(node, isBytes, isLiteralString, isIncomplete, isTemplate)

	if len(node.D.Strings) != 1 || node.D.Strings[0].GetNodeType() != parser.ParseNodeTypeString {
		return typeResult
	}

	e.attachTypeFormToStringLiteral(node, flags, inferenceContext, typeResult)
	return typeResult
}

// stringListValueType is the original's three-way choice of value type.
func (e *typeEvaluator) stringListValueType(
	node *parser.StringListNode, isBytes, isLiteralString, isIncomplete, isTemplate bool,
) *TypeResult {
	if isTemplate {
		var templateType Type = UnknownTypeCreate(false)
		if e.prefetched != nil && IsInstantiableClass(e.prefetched.TemplateClass) {
			templateType = ClassTypeCloneAsInstance(e.prefetched.TemplateClass.(*ClassType), false)
		}
		return &TypeResult{Type: templateType, IsIncomplete: isIncomplete}
	}

	hasFormatString := false
	for _, str := range node.D.Strings {
		if str.GetNodeType() == parser.ParseNodeTypeFormatString {
			hasFormatString = true
			break
		}
	}

	if hasFormatString {
		// An f-string has no literal value, but if every piece of it is a literal
		// string then the result is still known to be one.
		if isLiteralString {
			if literalStringType := e.getTypingType(node, "LiteralString"); IsInstantiableClass(literalStringType) {
				return &TypeResult{Type: ClassTypeCloneAsInstance(literalStringType.(*ClassType), false)}
			}
		}

		return &TypeResult{
			Type:         e.GetBuiltInObject(node, builtinStringClassName(isBytes), nil),
			IsIncomplete: isIncomplete,
		}
	}

	value := ""
	for _, s := range node.D.Strings {
		value += stringNodeValue(s)
	}

	return &TypeResult{
		Type:         e.cloneBuiltinObjectWithLiteral(node, builtinStringClassName(isBytes), LiteralString(value)),
		IsIncomplete: isIncomplete,
	}
}

// attachTypeFormToStringLiteral is the original's final block. It mutates
// typeResult in place, as the original does.
func (e *typeEvaluator) attachTypeFormToStringLiteral(
	node *parser.StringListNode,
	flags EvalFlags,
	inferenceContext *InferenceContext,
	typeResult *TypeResult,
) {
	// The original's comment: only attempt to interpret the string as a TypeForm
	// forward reference when there's a signal that a TypeForm value is wanted in
	// this context. Doing it unconditionally can trigger expensive (and
	// recursion-prone) type lookups for plain string literals in non-type
	// contexts.
	wantsTypeForm := (flags&EvalFlagsTypeFormArg) != 0 ||
		(inferenceContext != nil && expectedTypeWantsTypeForm(inferenceContext.ExpectedType))
	if !wantsTypeForm {
		return
	}

	stringNode, ok := node.D.Strings[0].(*parser.StringNode)
	if !ok {
		return
	}

	// The original's comment: for performance reasons, do not attempt to treat
	// the string literal as a TypeForm if it's going to fail anyway or is unlikely
	// to be a TypeForm (really long, triple-quoted, etc.).
	disallowedTokenFlags := parser.StringTokenFlagsBytes |
		parser.StringTokenFlagsRaw |
		parser.StringTokenFlagsFormat |
		parser.StringTokenFlagsTemplate |
		parser.StringTokenFlagsTriplicate

	if (stringNode.D.Token.Flags&disallowedTokenFlags) != 0 ||
		len(stringNode.D.Token.EscapedValue) >= maxTypeFormStringLength {
		return
	}

	typeFormResult := e.getTypeOfStringListAsType(node, flags)
	if props := typeFormResult.Type.Base().Props; props != nil && props.TypeForm != nil {
		typeResult.Type = CloneWithTypeForm(typeResult.Type, props.TypeForm)
	}
}

// isBytesStringNode is the original's `isBytesNode` closure. Both StringNode and
// FormatStringNode carry a token.
func isBytesStringNode(node parser.StringOrFormatStringNode) bool {
	switch typed := node.(type) {
	case *parser.StringNode:
		return typed.D.Token.Flags&parser.StringTokenFlagsBytes != 0
	case *parser.FormatStringNode:
		return typed.D.Token.Flags&parser.StringTokenFlagsBytes != 0
	}
	return false
}

// stringNodeValue reads `.d.value` off the same union.
func stringNodeValue(node parser.StringOrFormatStringNode) string {
	switch typed := node.(type) {
	case *parser.StringNode:
		return typed.D.Value.String()
	case *parser.FormatStringNode:
		return typed.D.Value.String()
	}
	return ""
}

func builtinStringClassName(isBytes bool) string {
	if isBytes {
		return "bytes"
	}
	return "str"
}

/*
 * The two things this reaches.
 */

// getTypeOfString corresponds to the function of the same name: one piece of the
// concatenation.
//
// A plain string is a literal. An f-string is not, but it is still LiteralString
// when every interpolated expression is itself a literal string -- which is the
// property that makes LiteralString useful, since it is what lets a formatted
// query string stay safe. bytes is excluded because there are no bytes literals
// in the LiteralString sense.
func (e *typeEvaluator) getTypeOfString(node parser.StringOrFormatStringNode) *TypeResult {
	isBytes := isBytesStringNode(node)

	if stringNode, ok := node.(*parser.StringNode); ok {
		return &TypeResult{
			Type: e.cloneBuiltinObjectWithLiteral(stringNode, builtinStringClassName(isBytes),
				LiteralString(stringNode.D.Value.String())),
		}
	}

	formatNode, ok := node.(*parser.FormatStringNode)
	if !ok {
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	isTemplateString := formatNode.D.Token.Flags&parser.StringTokenFlagsTemplate != 0
	isLiteralString := true
	isIncomplete := false

	// The original's comment: if all of the format expressions are of type
	// LiteralString, then the resulting formatted string is also LiteralString.
	for _, expr := range formatNode.D.FieldExprs {
		exprTypeResult := e.getTypeOfExpression(expr, EvalFlagsNone, nil)

		if exprTypeResult.IsIncomplete {
			isIncomplete = true
		}

		DoForEachSubtype(exprTypeResult.Type, func(exprSubtype Type, _ int, _ []Type) {
			if !IsClassInstance(exprSubtype) {
				isLiteralString = false
				return
			}

			cls := exprSubtype.(*ClassType)
			if ClassTypeIsBuiltInNamed(cls, "LiteralString") {
				return
			}
			if ClassTypeIsBuiltInNamed(cls, "str") && cls.Priv.LiteralValue != nil {
				return
			}

			isLiteralString = false
		})
	}

	if isTemplateString {
		var templateType Type = UnknownTypeCreate(false)
		if e.prefetched != nil && IsInstantiableClass(e.prefetched.TemplateClass) {
			templateType = ClassTypeCloneAsInstance(e.prefetched.TemplateClass.(*ClassType), false)
		}
		return &TypeResult{Type: templateType, IsIncomplete: isIncomplete}
	}

	if !isBytes && isLiteralString {
		if literalStringType := e.getTypingType(formatNode, "LiteralString"); IsInstantiableClass(literalStringType) {
			return &TypeResult{
				Type:         ClassTypeCloneAsInstance(literalStringType.(*ClassType), false),
				IsIncomplete: isIncomplete,
			}
		}
	}

	typeResult := &TypeResult{
		Type:         e.GetBuiltInObject(formatNode, builtinStringClassName(isBytes), nil),
		IsIncomplete: isIncomplete,
	}

	// An f-string result must not carry `str`'s promotion to `bytes`: the
	// promotion is a property of the declared type, not of a computed value.
	if cls, ok := typeResult.Type.(*ClassType); ok &&
		cls.Priv.IncludePromotions != nil && *cls.Priv.IncludePromotions {
		typeResult.Type = ClassTypeCloneRemoveTypePromotions(cls)
	}

	return typeResult
}

// getTypeOfStringListAsType corresponds to the function of the same name: parse
// the string's contents as a type annotation, which is how a forward reference
// is resolved.
func (e *typeEvaluator) getTypeOfStringListAsType(
	_ *parser.StringListNode, _ EvalFlags,
) *TypeResult {
	e.unported("getTypeOfStringListAsType")
	return &TypeResult{Type: UnknownTypeCreate(false)}
}
