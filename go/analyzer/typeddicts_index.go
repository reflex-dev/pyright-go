/*
 * typeddicts_index.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typedDicts.ts (pyright 1.1.412):
 * getTypeOfIndexedTypedDict.
 *
 * This is `d["x"]`, `d["x"] = v` and `del d["x"]` on a TypedDict. The index is
 * not a runtime lookup into a homogeneous container -- the key's *literal* type
 * selects a specific field, so a non-literal `str` key yields Unknown rather
 * than a union of every value type. That is deliberate: a union would let
 * `d[some_str]` typecheck against any field, which is both unsound for writes
 * and misleading for reads.
 *
 * The three usage methods diverge in what counts as an error, and the
 * distinctions are exactly the ones the TypedDict spec draws:
 *
 *   - reading a not-required key is *allowed but flagged*, because the key may
 *     be absent at runtime;
 *   - writing a read-only key is an error, reading it is not;
 *   - deleting a required key is an error, deleting a not-required one is fine.
 *
 * allDiagsInvolveNotRequiredKeys is what routes the result to the right rule.
 * If every complaint gathered is about not-required access, the whole thing is
 * reported under reportTypedDictNotRequiredAccess, which users commonly disable;
 * a single genuine error anywhere in the union demotes it to a general type
 * issue. That is why the flag is cleared on some branches and not others, and
 * why the not-required branch conspicuously leaves it alone.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// GetTypeOfIndexedTypedDict corresponds to getTypeOfIndexedTypedDict. It returns
// nil where the original returns undefined, meaning "not a TypedDict subscript
// after all; fall through to the general index path".
func GetTypeOfIndexedTypedDict(
	evaluator TypeEvaluator, node *parser.IndexNode, baseType *ClassType, usage *EvaluatorUsage,
) *TypeResult {
	if len(node.D.Items) != 1 {
		evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeArgsMismatchOne().Format(len(node.D.Items)), node, nil)
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	// The original's comment: look for subscript types that are not supported by
	// TypedDict.
	if node.D.TrailingComma || node.D.Items[0].D.Name != nil ||
		node.D.Items[0].D.ArgCategory != parser.ArgCategorySimple {
		return nil
	}

	// A narrowed TypedDict records which not-required keys are known present, and
	// that is only sound to consult for reads.
	entries := GetTypedDictMembersForClass(evaluator, baseType, usage.Method == "get")

	indexTypeResult := evaluator.GetTypeOfExpression(node.D.Items[0].D.ValueExpr, EvalFlagsNone, nil)
	indexType := indexTypeResult.Type
	diag := common.NewDiagnosticAddendum()
	allDiagsInvolveNotRequiredKeys := true

	resultingType := MapSubtypes(indexType, func(subtype Type) Type {
		if IsAnyOrUnknown(subtype) {
			return subtype
		}

		if !IsClassInstance(subtype) || !ClassTypeIsBuiltInNamed(subtype.(*ClassType), "str") {
			diag.AddMessage(localization.LocAddendum.TypeNotStringLiteral().Format(
				evaluator.PrintType(subtype, nil)))
			allDiagsInvolveNotRequiredKeys = false
			return UnknownTypeCreate(false)
		}

		strSubtype := subtype.(*ClassType)
		if strSubtype.Priv.LiteralValue == nil {
			// The original's comment: if it's a plain str with no literal value,
			// we can't make any determination about the resulting type.
			return UnknownTypeCreate(false)
		}

		// The original's comment: look up the entry in the typed dict to get its
		// type.
		entryName := ""
		if s, ok := strSubtype.Priv.LiteralValue.(LiteralString); ok {
			entryName = string(s)
		}
		entry, found := entries.KnownItems.Get(entryName)
		if !found || entry == nil {
			entry = entries.ExtraItems
		}

		if entry == nil || IsNever(entry.ValueType) {
			diag.AddMessage(localization.LocAddendum.KeyUndefined().Format(
				entryName, evaluator.PrintType(baseType, nil)))
			allDiagsInvolveNotRequiredKeys = false
			return UnknownTypeCreate(false)
		}

		switch {
		case !(entry.IsRequired || entry.IsProvided) && usage.Method == "get":
			// Note: allDiagsInvolveNotRequiredKeys is deliberately left true here.
			// See the file header.
			diag.AddMessage(localization.LocAddendum.KeyNotRequired().Format(
				entryName, evaluator.PrintType(baseType, nil)))

		case entry.IsReadOnly && usage.Method != "get":
			diag.AddMessage(localization.LocAddendum.KeyReadOnly().Format(
				entryName, evaluator.PrintType(baseType, nil)))
		}

		if usage.Method == "set" {
			var setType Type = AnyTypeCreate(false)
			if usage.SetType != nil {
				setType = usage.SetType.Type
			}
			if !evaluator.AssignType(entry.ValueType, setType, diag, nil, AssignTypeFlagsDefault, 0) {
				allDiagsInvolveNotRequiredKeys = false
			}
		} else if usage.Method == "del" && entry.IsRequired {
			diag.AddMessage(localization.LocAddendum.KeyRequiredDeleted().Format(entryName))
			allDiagsInvolveNotRequiredKeys = false
		}

		return entry.ValueType
	}, nil)

	// The original's comment: if we have an "expected type" diagnostic addendum
	// (used for assignments), use that rather than the local diagnostic
	// information because it will be more informative.
	if usage.SetExpectedTypeDiag != nil && !diag.IsEmpty() && !usage.SetExpectedTypeDiag.IsEmpty() {
		diag = usage.SetExpectedTypeDiag
	}

	if !diag.IsEmpty() {
		var typedDictDiag string
		switch usage.Method {
		case "set":
			typedDictDiag = localization.LocMessage.TypedDictSet()
		case "del":
			typedDictDiag = localization.LocMessage.TypedDictDelete()
		default:
			typedDictDiag = localization.LocMessage.TypedDictAccess()
		}

		rule := DiagnosticRuleReportGeneralTypeIssues
		if allDiagsInvolveNotRequiredKeys {
			rule = DiagnosticRuleReportTypedDictNotRequiredAccess
		}

		evaluator.AddDiagnostic(rule, typedDictDiag+diag.GetString(), node, nil)
	}

	return &TypeResult{Type: resultingType, IsIncomplete: indexTypeResult.IsIncomplete}
}
