/*
 * typeddicts_create.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typedDicts.ts (pyright 1.1.412):
 * createTypedDictType, createTypedDictTypeInlined and
 * getTypedDictFieldsFromDictSyntax.
 *
 * These handle the *functional* TypedDict forms, where fields are given as call
 * arguments rather than class-body annotations. The original's comment names the
 * two spellings:
 *
 *   Point2D = TypedDict('Point2D', {'x': int, 'y': int})
 *   Point2D = TypedDict('Point2D', x=int, y=int)
 *
 * Only the dict form accepts the trailing configuration arguments -- `total=`,
 * `closed=` and `extra_items=` -- because in the keyword form those names would
 * be indistinguishable from fields. That is what `usingDictSyntax` gates, and it
 * is a real semantic difference rather than a parsing convenience.
 *
 * `closed=True` and `extra_items=` are mutually exclusive and each is
 * single-use; `sawClosedOrExtraItems` enforces both at once, which is why the
 * flag is set on every branch that consumes one of them.
 *
 * The field declarations are created with `isRuntimeTypeExpression: true`,
 * meaning the annotation lives in an expression evaluated at runtime rather than
 * in an annotation position. The inline form (`dict[{'x': int}]`) sets it false,
 * because there the dict *is* the annotation. Getting that backwards changes
 * whether the value expressions are treated as forward-referenceable.
 *
 * The final name check is worth keeping in mind when reading the diagnostic: at
 * runtime `X = TypedDict('Y', ...)` produces a class literally named 'Y' bound
 * to `X`, so the two disagreeing is a real inconsistency and not a style note.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// CreateTypedDictType corresponds to createTypedDictType.
func CreateTypedDictType(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	typedDictClass *ClassType,
	argList []*Arg,
) *ClassType {
	fileInfo := GetFileInfo(errorNode)

	className := ""
	hasClassName := false
	if len(argList) == 0 {
		evaluator.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.TypedDictFirstArg(), errorNode, nil)
	} else {
		nameArg := argList[0]
		if nameArg.ArgCategory != parser.ArgCategorySimple || nameArg.ValueExpression == nil ||
			nameArg.ValueExpression.GetNodeType() != parser.ParseNodeTypeStringList {
			var node parser.ExpressionNode = errorNode
			if argList[0].ValueExpression != nil {
				node = argList[0].ValueExpression
			}
			evaluator.AddDiagnostic(DiagnosticRuleReportArgumentType,
				localization.LocMessage.TypedDictFirstArg(), node, nil)
		} else {
			className = joinStringListValue(nameArg.ValueExpression.(*parser.StringListNode))
			hasClassName = true
		}
	}

	effectiveClassName := className
	if effectiveClassName == "" {
		effectiveClassName = "TypedDict"
	}
	classType := ClassTypeCreateInstantiable(
		effectiveClassName,
		GetClassFullName(errorNode, fileInfo.ModuleName, effectiveClassName),
		fileInfo.ModuleName,
		fileInfo.FileUri,
		ClassTypeFlagsTypedDictClass|ClassTypeFlagsValidTypeAliasClass,
		GetTypeSourceID(errorNode),
		nil,
		typedDictClass.Shared.EffectiveMetaclass,
		nil,
	)
	classType.Shared.BaseClasses = append(classType.Shared.BaseClasses, typedDictClass)
	ComputeMroLinearization(classType)

	classFields := ClassTypeGetSymbolTable(classType)
	classFields.Set("__class__", SymbolCreateWithType(
		SymbolFlagsClassMember|SymbolFlagsIgnoredForProtocolMatch, classType, nil))

	usingDictSyntax := false
	if len(argList) < 2 {
		evaluator.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.TypedDictSecondArgDict(), errorNode, nil)
	} else {
		entriesArg := argList[1]

		switch {
		case entriesArg.ArgCategory == parser.ArgCategorySimple && entriesArg.ValueExpression != nil &&
			entriesArg.ValueExpression.GetNodeType() == parser.ParseNodeTypeDictionary:
			usingDictSyntax = true
			getTypedDictFieldsFromDictSyntax(
				evaluator, entriesArg.ValueExpression.(*parser.DictionaryNode), classFields, false)

		case entriesArg.Name != nil:
			addTypedDictKeywordFields(evaluator, argList, classFields, fileInfo)

		default:
			evaluator.AddDiagnostic(DiagnosticRuleReportArgumentType,
				localization.LocMessage.TypedDictSecondArgDict(), errorNode, nil)
		}
	}

	if usingDictSyntax {
		applyTypedDictConfigArgs(evaluator, errorNode, classType, argList[2:])
	}

	SynthesizeTypedDictClassMethods(evaluator, errorNode, classType)

	// The original's comment: validate that the assigned variable name is
	// consistent with the provided name.
	if parentNode := errorNode.NodeBase().Parent; parentNode != nil &&
		parentNode.GetNodeType() == parser.ParseNodeTypeAssignment && hasClassName {
		target := parentNode.(*parser.AssignmentNode).D.LeftExpr
		typedDictTarget := target
		if target.GetNodeType() == parser.ParseNodeTypeTypeAnnotation {
			typedDictTarget = target.(*parser.TypeAnnotationNode).D.ValueExpr
		}

		if typedDictTarget.GetNodeType() == parser.ParseNodeTypeName {
			nameNode := typedDictTarget.(*parser.NameNode)
			if nameNode.D.Value != className {
				evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.TypedDictAssignedName().Format(className),
					typedDictTarget, nil)
			}
		}
	}

	return classType
}

// addTypedDictKeywordFields is the original's `else if (entriesArg.name)` arm:
// the `TypedDict('X', a=int, b=str)` spelling.
func addTypedDictKeywordFields(
	evaluator TypeEvaluator, argList []*Arg, classFields SymbolTable, fileInfo *AnalyzerFileInfo,
) {
	entrySet := map[string]bool{}

	for i := 1; i < len(argList); i++ {
		entry := argList[i]
		if entry.Name == nil || entry.ValueExpression == nil {
			continue
		}

		if entrySet[entry.Name.D.Value] {
			evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypedDictEntryUnique(), entry.ValueExpression, nil)
			continue
		}

		// The original's comment: record names in a map to detect duplicates.
		entrySet[entry.Name.D.Value] = true

		newSymbol := NewSymbol(SymbolFlagsInstanceMember)
		newSymbol.AddDeclaration(&VariableDeclaration{
			DeclarationBase: DeclarationBase{
				Type: DeclarationTypeVariable,
				Node: entry.Name,
				Uri:  fileInfo.FileUri,
				Range: common.ConvertOffsetsToRange(
					entry.Name.NodeBase().TextRange.Start,
					entry.ValueExpression.NodeBase().TextRange.End(),
					fileInfo.Lines),
				ModuleName: fileInfo.ModuleName,
			},
			TypeAnnotationNode:      entry.ValueExpression,
			IsRuntimeTypeExpression: true,
		})

		classFields.Set(entry.Name.D.Value, newSymbol)
	}
}

// applyTypedDictConfigArgs is the original's `if (usingDictSyntax)` block: the
// `total=`, `closed=` and `extra_items=` arguments.
func applyTypedDictConfigArgs(
	evaluator TypeEvaluator, errorNode parser.ExpressionNode, classType *ClassType, argsToConsider []*Arg,
) {
	sawClosedOrExtraItems := false

	for _, arg := range argsToConsider {
		argName := ""
		if arg.Name != nil {
			argName = arg.Name.D.Value
		}

		errNode := arg.ValueExpression
		if errNode == nil {
			errNode = errorNode
		}

		switch argName {
		case "total", "closed":
			constNode, isConst := arg.ValueExpression.(*parser.ConstantNode)
			isBoolLiteral := arg.ValueExpression != nil && isConst &&
				(constNode.D.ConstType == parser.KeywordTypeFalse ||
					constNode.D.ConstType == parser.KeywordTypeTrue)

			if !isBoolLiteral {
				evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.TypedDictBoolParam().Format(argName), errNode, nil)
				continue
			}

			if argName == "total" && constNode.D.ConstType == parser.KeywordTypeFalse {
				classType.Shared.Flags |= ClassTypeFlagsCanOmitDictValues
				continue
			}

			if argName != "closed" {
				continue
			}

			if constNode.D.ConstType == parser.KeywordTypeTrue {
				classType.Shared.Flags |=
					ClassTypeFlagsTypedDictMarkedClosed | ClassTypeFlagsTypedDictEffectivelyClosed
			}

			if sawClosedOrExtraItems {
				evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.TypedDictExtraItemsClosed(), errNode, nil)
			}

			// Note the placement: the flag is set even when `closed=False`, so a
			// second `closed=` or a following `extra_items=` is still reported.
			sawClosedOrExtraItems = true

		case "extra_items":
			classType.Shared.TypedDictExtraItemsExpr = arg.ValueExpression
			classType.Shared.Flags |= ClassTypeFlagsTypedDictEffectivelyClosed

			if sawClosedOrExtraItems {
				evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.TypedDictExtraItemsClosed(), errNode, nil)
			}

			sawClosedOrExtraItems = true

		default:
			evaluator.AddDiagnostic(DiagnosticRuleReportCallIssue,
				localization.LocMessage.TypedDictExtraArgs(), errNode, nil)
		}
	}
}

// CreateTypedDictTypeInlined corresponds to createTypedDictTypeInlined. The
// original's comment: creates a new anonymous TypedDict class from an inlined
// dict[{}] type annotation.
func CreateTypedDictTypeInlined(
	evaluator TypeEvaluator, dictNode *parser.DictionaryNode, typedDictClass *ClassType,
) *ClassType {
	fileInfo := GetFileInfo(dictNode)
	className := "<TypedDict>"

	classType := ClassTypeCreateInstantiable(
		className,
		GetClassFullName(dictNode, fileInfo.ModuleName, className),
		fileInfo.ModuleName,
		fileInfo.FileUri,
		ClassTypeFlagsTypedDictClass,
		GetTypeSourceID(dictNode),
		nil,
		typedDictClass.Shared.EffectiveMetaclass,
		nil,
	)
	classType.Shared.BaseClasses = append(classType.Shared.BaseClasses, typedDictClass)
	ComputeMroLinearization(classType)

	getTypedDictFieldsFromDictSyntax(evaluator, dictNode, ClassTypeGetSymbolTable(classType), true)
	SynthesizeTypedDictClassMethods(evaluator, dictNode, classType)

	return classType
}

// getTypedDictFieldsFromDictSyntax corresponds to the function of the same name.
func getTypedDictFieldsFromDictSyntax(
	evaluator TypeEvaluator, entryDict *parser.DictionaryNode, classFields SymbolTable, isInline bool,
) {
	entrySet := map[string]bool{}
	fileInfo := GetFileInfo(entryDict)

	for _, item := range entryDict.D.Items {
		entry, ok := item.(*parser.DictionaryKeyEntryNode)
		if !ok {
			evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypedDictSecondArgDictEntry(), item, nil)
			continue
		}

		if entry.D.KeyExpr.GetNodeType() != parser.ParseNodeTypeStringList {
			evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypedDictEntryName(), entry.D.KeyExpr, nil)
			continue
		}
		keyNode := entry.D.KeyExpr.(*parser.StringListNode)

		entryName := joinStringListValue(keyNode)
		if entryName == "" {
			evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypedDictEmptyName(), entry.D.KeyExpr, nil)
			continue
		}

		if entrySet[entryName] {
			evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypedDictEntryUnique(), entry.D.KeyExpr, nil)
			continue
		}

		// The original's comment: record names in a set to detect duplicates.
		entrySet[entryName] = true

		newSymbol := NewSymbol(SymbolFlagsInstanceMember)
		decl := &VariableDeclaration{
			DeclarationBase: DeclarationBase{
				Type: DeclarationTypeVariable,
				Node: entry.D.KeyExpr,
				Uri:  fileInfo.FileUri,
				Range: common.ConvertOffsetsToRange(
					keyNode.NodeBase().TextRange.Start,
					keyNode.NodeBase().TextRange.End(),
					fileInfo.Lines),
				ModuleName: fileInfo.ModuleName,
			},
			TypeAnnotationNode: entry.D.ValueExpr,
			// See the file header: in the inline form the dict *is* the
			// annotation, so the value expression is not a runtime expression.
			IsRuntimeTypeExpression: !isInline,
		}
		decl.IsInInlinedTypedDict = true
		newSymbol.AddDeclaration(decl)

		classFields.Set(entryName, newSymbol)
	}

	// The original's comment: set the type in the type cache for the dict node so
	// it doesn't get evaluated again.
	evaluator.SetTypeResultForNode(entryDict, &TypeResult{Type: UnknownTypeCreate(false)}, EvalFlagsNone)
}
