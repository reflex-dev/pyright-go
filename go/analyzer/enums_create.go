/*
 * enums_create.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/enums.ts (pyright 1.1.412): createEnumType, and
 * from analyzer/sentinel.ts: createSentinelType.
 *
 * createEnumType handles the *functional* enum forms, where members are supplied
 * as a runtime value rather than as a class body. The original enumerates the
 * seven accepted spellings in a comment reproduced below; they fall into three
 * shapes, and each is handled separately because they disagree about where the
 * member *values* come from.
 *
 * A bare name list -- whether given as one delimited string or as a list of
 * strings -- assigns values 1, 2, 3... by position, matching the runtime. A list
 * of (name, value) pairs or a dict supplies them explicitly. Mixing the two
 * within a single call is rejected outright rather than guessed at, which is why
 * `isSimpleString` is fixed by the first entry and every later entry must agree.
 *
 * Anything unrecognized returns nil, and that is not an error path: the caller
 * falls back to treating the call as an ordinary constructor invocation. Only a
 * statically analyzable member list can produce a synthesized enum class.
 *
 * createSentinelType is PEP 661. The runtime object it creates carries the name
 * it was given, so `X = Sentinel("Y")` is a genuine inconsistency and is
 * reported. The result is wrapped with cloneWithTypeForm because a sentinel is
 * usable both as a value and in a type expression -- it is its own type.
 */

package analyzer

import (
	"math/big"
	"strings"

	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// CreateEnumType corresponds to createEnumType. It returns nil where the
// original returns undefined, meaning "not a statically analyzable functional
// enum".
func CreateEnumType(
	evaluator TypeEvaluator, errorNode parser.ExpressionNode, enumClass *ClassType, argList []*Arg,
) *ClassType {
	fileInfo := GetFileInfo(errorNode)
	isReprEnum := isReprEnumClass(enumClass)

	if len(argList) == 0 {
		return nil
	}

	nameArg := argList[0]
	if nameArg.ArgCategory != parser.ArgCategorySimple || nameArg.ValueExpression == nil ||
		nameArg.ValueExpression.GetNodeType() != parser.ParseNodeTypeStringList {
		return nil
	}
	nameStringList := nameArg.ValueExpression.(*parser.StringListNode)
	if len(nameStringList.D.Strings) != 1 ||
		nameStringList.D.Strings[0].GetNodeType() != parser.ParseNodeTypeString {
		return nil
	}

	className := joinStringListValue(nameStringList)
	classType := ClassTypeCreateInstantiable(
		className,
		GetClassFullName(errorNode, fileInfo.ModuleName, className),
		fileInfo.ModuleName,
		fileInfo.FileUri,
		ClassTypeFlagsEnumClass|ClassTypeFlagsValidTypeAliasClass,
		GetTypeSourceID(errorNode),
		nil,
		enumClass.Shared.EffectiveMetaclass,
		nil,
	)
	classType.Shared.BaseClasses = append(classType.Shared.BaseClasses, enumClass)
	ComputeMroLinearization(classType)

	classFields := ClassTypeGetSymbolTable(classType)
	classFields.Set("__class__", SymbolCreateWithType(
		SymbolFlagsClassMember|SymbolFlagsIgnoredForProtocolMatch, classType, nil))

	if len(argList) < 2 {
		return nil
	}

	initArg := argList[1]
	if initArg.ArgCategory != parser.ArgCategorySimple || initArg.ValueExpression == nil {
		return nil
	}

	intClassType := evaluator.GetBuiltInType(errorNode, "int")
	if intClassType == nil || !IsInstantiableClass(intClassType) {
		return nil
	}
	classInstanceType := ClassTypeCloneAsInstance(classType, true)

	b := &enumMemberBuilder{
		evaluator:         evaluator,
		classType:         classType,
		classInstanceType: classInstanceType,
		classFields:       classFields,
		intClassType:      intClassType.(*ClassType),
		isReprEnum:        isReprEnum,
	}

	// The original's comment: the Enum functional form supports various forms of
	// arguments:
	//   Enum('name', 'a b c')
	//   Enum('name', 'a,b,c')
	//   Enum('name', ['a', 'b', 'c'])
	//   Enum('name', ('a', 'b', 'c'))
	//   Enum('name', (('a', 1), ('b', 2), ('c', 3)))
	//   Enum('name', [('a', 1), ('b', 2), ('c', 3))]
	//   Enum('name', {'a': 1, 'b': 2, 'c': 3})
	switch initArg.ValueExpression.GetNodeType() {
	case parser.ParseNodeTypeStringList:
		if !b.addMembersFromNameString(initArg.ValueExpression.(*parser.StringListNode)) {
			return nil
		}
		return classType

	case parser.ParseNodeTypeList, parser.ParseNodeTypeTuple:
		if !b.addMembersFromSequence(initArg.ValueExpression) {
			return nil
		}

	case parser.ParseNodeTypeDictionary:
		if !b.addMembersFromDict(initArg.ValueExpression.(*parser.DictionaryNode)) {
			return nil
		}
	}

	return classType
}

// enumMemberBuilder carries what the three member-list shapes share.
type enumMemberBuilder struct {
	evaluator         TypeEvaluator
	classType         *ClassType
	classInstanceType *ClassType
	classFields       SymbolTable
	intClassType      *ClassType
	isReprEnum        bool
}

// addMember records one enum member. Every shape funnels through here.
func (b *enumMemberBuilder) addMember(entryName string, valueType Type) {
	enumLiteral := &EnumLiteral{
		ClassFullName: b.classType.Shared.FullName,
		ClassName:     b.classType.Shared.Name,
		ItemName:      entryName,
		ItemType:      valueType,
		IsReprEnum:    b.isReprEnum,
	}

	b.classFields.Set(entryName, SymbolCreateWithType(SymbolFlagsClassMember,
		ClassTypeCloneWithLiteral(b.classInstanceType, enumLiteral), nil))
}

// intLiteral is the implicit 1-based value the runtime assigns to a bare name.
func (b *enumMemberBuilder) intLiteral(index int) Type {
	return ClassTypeCloneWithLiteral(ClassTypeCloneAsInstance(b.intClassType, true),
		LiteralInt{Value: big.NewInt(int64(index + 1))})
}

// addMembersFromNameString handles `Enum('name', 'a b c')`.
func (b *enumMemberBuilder) addMembersFromNameString(node *parser.StringListNode) bool {
	// The original's comment: don't allow format strings in the init arg.
	for _, str := range node.D.Strings {
		if str.GetNodeType() != parser.ParseNodeTypeString {
			return false
		}
	}

	initStr := strings.TrimSpace(joinStringListValue(node))

	// The original's comment: split by comma or whitespace.
	entryNames := splitNamedTupleNames(initStr)

	for index, entryName := range entryNames {
		if entryName == "" {
			return false
		}

		b.addMember(entryName, b.intLiteral(index))
	}

	return true
}

// addMembersFromSequence handles the list and tuple forms, both bare names and
// (name, value) pairs.
func (b *enumMemberBuilder) addMembersFromSequence(valueExpr parser.ExpressionNode) bool {
	var entries []parser.ExpressionNode
	if valueExpr.GetNodeType() == parser.ParseNodeTypeList {
		entries = valueExpr.(*parser.ListNode).D.Items
	} else {
		entries = valueExpr.(*parser.TupleNode).D.Items
	}

	if len(entries) == 0 {
		return false
	}

	// The original's comment: entries can be either string literals or tuples of a
	// string literal and a value. All entries must follow the same pattern.
	isSimpleString := false
	for index, entry := range entries {
		if index == 0 {
			isSimpleString = entry.GetNodeType() == parser.ParseNodeTypeStringList
		}

		var nameNode parser.ParseNode
		var valueType Type

		switch entry.GetNodeType() {
		case parser.ParseNodeTypeStringList:
			if !isSimpleString {
				return false
			}
			nameNode = entry
			valueType = b.intLiteral(index)

		case parser.ParseNodeTypeTuple:
			if isSimpleString {
				return false
			}
			tupleEntry := entry.(*parser.TupleNode)
			if len(tupleEntry.D.Items) != 2 {
				return false
			}
			nameNode = tupleEntry.D.Items[0]
			valueType = b.evaluator.GetTypeOfExpression(tupleEntry.D.Items[1], EvalFlagsNone, nil).Type

		default:
			return false
		}

		entryName, ok := singleStringLiteralValue(nameNode)
		if !ok {
			return false
		}

		b.addMember(entryName, valueType)
	}

	return true
}

// addMembersFromDict handles `Enum('name', {'a': 1})`.
func (b *enumMemberBuilder) addMembersFromDict(node *parser.DictionaryNode) bool {
	if len(node.D.Items) == 0 {
		return false
	}

	for _, item := range node.D.Items {
		// The original's comment: don't support dictionary expansion expressions.
		entry, ok := item.(*parser.DictionaryKeyEntryNode)
		if !ok {
			return false
		}

		valueType := b.evaluator.GetTypeOfExpression(entry.D.ValueExpr, EvalFlagsNone, nil).Type

		entryName, ok := singleStringLiteralValue(entry.D.KeyExpr)
		if !ok {
			return false
		}

		b.addMember(entryName, valueType)
	}

	return true
}

// singleStringLiteralValue is the original's repeated check that a node is a
// StringList holding exactly one plain (non-format) string.
func singleStringLiteralValue(node parser.ParseNode) (string, bool) {
	stringList, ok := node.(*parser.StringListNode)
	if !ok || len(stringList.D.Strings) != 1 ||
		stringList.D.Strings[0].GetNodeType() != parser.ParseNodeTypeString {
		return "", false
	}
	return stringOrFormatValue(stringList.D.Strings[0]), true
}

// CreateSentinelType corresponds to sentinel.ts createSentinelType.
func CreateSentinelType(
	evaluator TypeEvaluator, errorNode parser.ExpressionNode, argList []*Arg,
) Type {
	className := ""

	if len(argList) != 1 {
		evaluator.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.SentinelParamCount(), errorNode, nil)
		return nil
	}

	nameArg := argList[0]
	if nameArg.ArgCategory == parser.ArgCategorySimple && nameArg.ValueExpression != nil &&
		nameArg.ValueExpression.GetNodeType() == parser.ParseNodeTypeStringList {
		className = joinStringListValue(nameArg.ValueExpression.(*parser.StringListNode))
	}

	if className == "" {
		var node parser.ParseNode = errorNode
		if argList[0].Node != nil {
			node = argList[0].Node
		}
		evaluator.AddDiagnostic(DiagnosticRuleReportArgumentType,
			localization.LocMessage.SentinelBadName(), node, nil)
		return nil
	}

	// The runtime object records the name it was given, so a mismatch with the
	// assigned variable is a real inconsistency.
	if parentNode := errorNode.NodeBase().Parent; parentNode != nil &&
		parentNode.GetNodeType() == parser.ParseNodeTypeAssignment {
		leftExpr := parentNode.(*parser.AssignmentNode).D.LeftExpr
		if leftExpr.GetNodeType() == parser.ParseNodeTypeName &&
			leftExpr.(*parser.NameNode).D.Value != className {
			evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.SentinelNameMismatch(), leftExpr, nil)
			return nil
		}
	}

	fileInfo := GetFileInfo(errorNode)
	fullClassName := GetClassFullName(errorNode, fileInfo.ModuleName, className)
	classType := ClassTypeCreateInstantiable(
		className,
		fullClassName,
		fileInfo.ModuleName,
		fileInfo.FileUri,
		ClassTypeFlagsFinal|ClassTypeFlagsValidTypeAliasClass,
		GetTypeSourceID(errorNode),
		nil,
		evaluator.GetTypeClassType(),
		nil,
	)

	classType.Shared.BaseClasses = append(classType.Shared.BaseClasses, evaluator.GetObjectType())
	ComputeMroLinearization(classType)
	classType = ClassTypeCloneWithLiteral(classType,
		&SentinelLiteral{ClassFullName: fullClassName, ClassName: className})

	instanceType := ClassTypeCloneAsInstance(classType, true)

	// A sentinel is its own type: usable as a value and in a type expression.
	return CloneWithTypeForm(instanceType, instanceType)
}
