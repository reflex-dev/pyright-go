/*
 * typeevaluator_stringtype.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfStringListAsType and parseStringAsTypeAnnotation.
 *
 * A forward reference -- `def f() -> "Foo":` -- is a string at parse time and a
 * type expression at evaluation time. Resolving it means running the parser
 * again, on the string's contents, in the middle of type evaluation.
 *
 * parseStringAsTypeAnnotation is where the interesting trick lives. It does not
 * parse the string's text on its own; it builds a dummy file consisting of
 * valueOffset spaces followed by the text, so that every token in the re-parsed
 * subtree carries the offset it would have had in the real file. That is what
 * keeps error squiggles and hover ranges pointing inside the original string
 * literal rather than at the start of a synthetic buffer.
 *
 * The reportErrors flag does double duty: it decides whether parse diagnostics
 * reach the sink, and it decides whether the re-parsed subtree is grafted onto
 * node.d.annotation. Grafting is what lets language-server operations find
 * references inside a forward reference, and it must not happen speculatively --
 * hence the caller's pairing of reportTypeErrors with useSpeculativeMode.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getTypeOfStringListAsType corresponds to the function of the same name: parse
// the string's contents as a type annotation, which is how a forward reference
// is resolved.
func (e *typeEvaluator) getTypeOfStringListAsType(
	node *parser.StringListNode, flags EvalFlags,
) *TypeResult {
	reportTypeErrors := (flags & EvalFlagsStrLiteralAsType) != 0
	updatedFlags := flags | EvalFlagsForwardRefs | EvalFlagsInstantiableType
	var typeResult *TypeResult

	// The original's comment: in most cases, annotations within a string are not
	// parsed by the interpreter. There are a few exceptions (e.g. the "bound"
	// value for a TypeVar constructor).
	if (flags & EvalFlagsParsesStringLiteral) == 0 {
		updatedFlags |= EvalFlagsNotParsed
	}

	if node.D.Annotation != nil && (flags&EvalFlagsTypeExpression) != 0 {
		return e.getTypeOfExpression(node.D.Annotation, updatedFlags, nil)
	}

	if len(node.D.Strings) == 1 {
		var tokenFlags parser.StringTokenFlags
		switch s := node.D.Strings[0].(type) {
		case *parser.StringNode:
			tokenFlags = s.D.Token.Flags
		case *parser.FormatStringNode:
			tokenFlags = s.D.Token.Flags
		}

		// The four string kinds that cannot be a forward reference, each with its
		// own message. The original spells these as four separate ifs.
		var rejectMessage string
		switch {
		case (tokenFlags & parser.StringTokenFlagsBytes) != 0:
			rejectMessage = localization.LocMessage.AnnotationBytesString()
		case (tokenFlags & parser.StringTokenFlagsRaw) != 0:
			rejectMessage = localization.LocMessage.AnnotationRawString()
		case (tokenFlags & parser.StringTokenFlagsFormat) != 0:
			rejectMessage = localization.LocMessage.AnnotationFormatString()
		case (tokenFlags & parser.StringTokenFlagsTemplate) != 0:
			rejectMessage = localization.LocMessage.AnnotationTemplateString()
		}

		if rejectMessage != "" {
			if reportTypeErrors {
				e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, rejectMessage, node, nil)
			}
			return &TypeResult{Type: UnknownTypeCreate(false)}
		}

		// The original's comment: we didn't know at parse time that this string
		// node was going to be evaluated as a forward-referenced type. We need to
		// re-invoke the parser at this stage.
		expr := e.parseStringAsTypeAnnotation(node, reportTypeErrors)
		if expr != nil {
			// `useSpeculativeMode(reportTypeErrors ? undefined : node, ...)`: a
			// nil speculative node runs the callback outright, so passing errors
			// through and running speculatively are the same switch.
			var speculativeNode parser.ParseNode
			if !reportTypeErrors {
				speculativeNode = node
			}

			e.UseSpeculativeMode(speculativeNode, func() {
				typeResult = e.getTypeOfExpression(expr, updatedFlags, nil)
			}, nil)
		}
	}

	if typeResult == nil {
		if reportTypeErrors {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.ExpectedTypeNotString(), node, nil)
		}
		typeResult = &TypeResult{Type: UnknownTypeCreate(false)}
	}

	return typeResult
}

// parseStringAsTypeAnnotation corresponds to the function of the same name.
func (e *typeEvaluator) parseStringAsTypeAnnotation(
	node *parser.StringListNode, reportErrors bool,
) parser.ExpressionNode {
	fileInfo := GetFileInfo(node)
	p := parser.NewParser()

	var textValue common.Text
	valueOffset := node.D.Strings[0].GetRange().Start

	switch s := node.D.Strings[0].(type) {
	case *parser.StringNode:
		textValue = s.D.Value
		// The original's comment: determine the offset within the file where the
		// string literal's contents begin. Only a plain String node adjusts past
		// the prefix and quote marks; a FormatString node is rejected earlier.
		valueOffset += s.D.Token.PrefixLength + s.D.Token.QuoteMarkLength
	case *parser.FormatStringNode:
		textValue = s.D.Value
	}

	// The original's comment: construct a temporary dummy string with the text
	// value at the appropriate offset so as to mimic the original file. This will
	// keep all of the token and diagnostic offsets correct.
	dummyFileContents := common.NewText(strings.Repeat(" ", valueOffset) + textValue.String())

	parseOptions := parser.NewParseOptions()
	parseOptions.IsStubFile = fileInfo.IsStubFile
	parseOptions.PythonVersion = fileInfo.ExecutionEnvironment.PythonVersion
	parseOptions.ReportErrorsForParsedStringContents = true

	var typingSymbolAliases map[string]string
	if fileInfo.TypingSymbolAliases != nil {
		typingSymbolAliases = make(map[string]string, fileInfo.TypingSymbolAliases.Size())
		for _, key := range fileInfo.TypingSymbolAliases.Keys() {
			value, _ := fileInfo.TypingSymbolAliases.Get(key)
			typingSymbolAliases[key] = value
		}
	}

	parseResults := p.ParseTextExpression(
		dummyFileContents,
		valueOffset,
		textValue.Length(),
		parseOptions,
		parser.ParseTextModeExpression,
		0,
		typingSymbolAliases,
	)

	if parseResults.ParseTree == nil {
		return nil
	}

	// ParseTextModeExpression always yields an expression node -- an ErrorNode is
	// one too -- so the original types parseTree as ExpressionNode directly. Go
	// needs the assertion spelled out.
	parseTree, ok := parseResults.ParseTree.(parser.ExpressionNode)
	if !ok {
		return nil
	}

	// The original's comment: if there are errors but we are not reporting them,
	// return undefined to indicate that the parse failed.
	if !reportErrors && len(parseResults.Diagnostics) > 0 {
		return nil
	}

	for _, diag := range parseResults.Diagnostics {
		fileInfo.DiagnosticSink.AddDiagnosticWithTextRange(
			common.DiagnosticLevelError, diag.Message, node.GetRange())
	}

	parseTree.NodeBase().Parent = node

	// The original's comment: optionally add the new subtree to the parse tree so
	// it can participate in language server operations like find and replace.
	if reportErrors {
		node.D.Annotation = parseTree
	}

	return parseTree
}
