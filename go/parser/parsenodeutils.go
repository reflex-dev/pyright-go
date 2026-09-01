/*
 * parsenodeutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * ParseNodeType is a const enum which strips out the string keys.
 * This file is used to map the string keys to the const enum values.
 *
 * Transliterated from parser/parseNodeUtils.ts (pyright 1.1.412).
 *
 * The TypeScript version derives the reverse maps at module load with
 * Object.entries(...).reduce(...). Here they are built in init() from the same
 * forward maps, so the two directions cannot drift apart.
 */

package parser

// ParseNodeTypeMap corresponds to ParseNodeTypeMap: node type name -> value.
var ParseNodeTypeMap = map[string]ParseNodeType{
	"Error":                     ParseNodeTypeError,
	"Argument":                  ParseNodeTypeArgument,
	"Assert":                    ParseNodeTypeAssert,
	"Assignment":                ParseNodeTypeAssignment,
	"AssignmentExpression":      ParseNodeTypeAssignmentExpression,
	"AugmentedAssignment":       ParseNodeTypeAugmentedAssignment,
	"Await":                     ParseNodeTypeAwait,
	"BinaryOperation":           ParseNodeTypeBinaryOperation,
	"Break":                     ParseNodeTypeBreak,
	"Call":                      ParseNodeTypeCall,
	"Class":                     ParseNodeTypeClass,
	"Comprehension":             ParseNodeTypeComprehension,
	"ComprehensionFor":          ParseNodeTypeComprehensionFor,
	"ComprehensionIf":           ParseNodeTypeComprehensionIf,
	"Constant":                  ParseNodeTypeConstant,
	"Continue":                  ParseNodeTypeContinue,
	"Decorator":                 ParseNodeTypeDecorator,
	"Del":                       ParseNodeTypeDel,
	"Dictionary":                ParseNodeTypeDictionary,
	"DictionaryExpandEntry":     ParseNodeTypeDictionaryExpandEntry,
	"DictionaryKeyEntry":        ParseNodeTypeDictionaryKeyEntry,
	"Ellipsis":                  ParseNodeTypeEllipsis,
	"If":                        ParseNodeTypeIf,
	"Import":                    ParseNodeTypeImport,
	"ImportAs":                  ParseNodeTypeImportAs,
	"ImportFrom":                ParseNodeTypeImportFrom,
	"ImportFromAs":              ParseNodeTypeImportFromAs,
	"Index":                     ParseNodeTypeIndex,
	"Except":                    ParseNodeTypeExcept,
	"For":                       ParseNodeTypeFor,
	"FormatString":              ParseNodeTypeFormatString,
	"Function":                  ParseNodeTypeFunction,
	"Global":                    ParseNodeTypeGlobal,
	"Lambda":                    ParseNodeTypeLambda,
	"List":                      ParseNodeTypeList,
	"MemberAccess":              ParseNodeTypeMemberAccess,
	"Module":                    ParseNodeTypeModule,
	"ModuleName":                ParseNodeTypeModuleName,
	"Name":                      ParseNodeTypeName,
	"Nonlocal":                  ParseNodeTypeNonlocal,
	"Number":                    ParseNodeTypeNumber,
	"Parameter":                 ParseNodeTypeParameter,
	"Pass":                      ParseNodeTypePass,
	"Raise":                     ParseNodeTypeRaise,
	"Return":                    ParseNodeTypeReturn,
	"Set":                       ParseNodeTypeSet,
	"Slice":                     ParseNodeTypeSlice,
	"StatementList":             ParseNodeTypeStatementList,
	"StringList":                ParseNodeTypeStringList,
	"String":                    ParseNodeTypeString,
	"Suite":                     ParseNodeTypeSuite,
	"Ternary":                   ParseNodeTypeTernary,
	"Tuple":                     ParseNodeTypeTuple,
	"Try":                       ParseNodeTypeTry,
	"TypeAnnotation":            ParseNodeTypeTypeAnnotation,
	"UnaryOperation":            ParseNodeTypeUnaryOperation,
	"Unpack":                    ParseNodeTypeUnpack,
	"While":                     ParseNodeTypeWhile,
	"With":                      ParseNodeTypeWith,
	"WithItem":                  ParseNodeTypeWithItem,
	"Yield":                     ParseNodeTypeYield,
	"YieldFrom":                 ParseNodeTypeYieldFrom,
	"FunctionAnnotation":        ParseNodeTypeFunctionAnnotation,
	"Match":                     ParseNodeTypeMatch,
	"Case":                      ParseNodeTypeCase,
	"PatternSequence":           ParseNodeTypePatternSequence,
	"PatternAs":                 ParseNodeTypePatternAs,
	"PatternLiteral":            ParseNodeTypePatternLiteral,
	"PatternClass":              ParseNodeTypePatternClass,
	"PatternCapture":            ParseNodeTypePatternCapture,
	"PatternMapping":            ParseNodeTypePatternMapping,
	"PatternMappingKeyEntry":    ParseNodeTypePatternMappingKeyEntry,
	"PatternMappingExpandEntry": ParseNodeTypePatternMappingExpandEntry,
	"PatternValue":              ParseNodeTypePatternValue,
	"PatternClassArgument":      ParseNodeTypePatternClassArgument,
	"TypeParameter":             ParseNodeTypeTypeParameter,
	"TypeParameterList":         ParseNodeTypeTypeParameterList,
	"TypeAlias":                 ParseNodeTypeTypeAlias,
}

// ParseNodeTypeNameMap corresponds to ParseNodeTypeNameMap: value -> name.
var ParseNodeTypeNameMap = map[ParseNodeType]string{}

// OperatorTypeMap corresponds to OperatorTypeMap: operator text -> value.
//
// Note the two entries whose keys are not bare operator text: "not " carries a
// trailing space and "is not" / "not in" carry an embedded one. Those are the
// keys the original uses, so they are preserved verbatim.
var OperatorTypeMap = map[string]OperatorType{
	"+":   OperatorTypeAdd,
	"+=":  OperatorTypeAddEqual,
	"=":   OperatorTypeAssign,
	"&":   OperatorTypeBitwiseAnd,
	"&=":  OperatorTypeBitwiseAndEqual,
	"~":   OperatorTypeBitwiseInvert,
	"|":   OperatorTypeBitwiseOr,
	"|=":  OperatorTypeBitwiseOrEqual,
	"^":   OperatorTypeBitwiseXor,
	"^=":  OperatorTypeBitwiseXorEqual,
	"/":   OperatorTypeDivide,
	"/=":  OperatorTypeDivideEqual,
	"==":  OperatorTypeEquals,
	"//":  OperatorTypeFloorDivide,
	"//=": OperatorTypeFloorDivideEqual,
	">":   OperatorTypeGreaterThan,
	">=":  OperatorTypeGreaterThanOrEqual,
	"<<":  OperatorTypeLeftShift,
	"<<=": OperatorTypeLeftShiftEqual,
	"<>":  OperatorTypeLessOrGreaterThan,
	"<":   OperatorTypeLessThan,
	"<=":  OperatorTypeLessThanOrEqual,
	"@":   OperatorTypeMatrixMultiply,
	"@=":  OperatorTypeMatrixMultiplyEqual,
	"%":   OperatorTypeMod,
	"%=":  OperatorTypeModEqual,
	"*":   OperatorTypeMultiply,
	"*=":  OperatorTypeMultiplyEqual,
	"!=":  OperatorTypeNotEquals,
	"**":  OperatorTypePower,
	"**=": OperatorTypePowerEqual,
	">>":  OperatorTypeRightShift,
	">>=": OperatorTypeRightShiftEqual,
	"-":   OperatorTypeSubtract,
	"-=":  OperatorTypeSubtractEqual,

	"and":    OperatorTypeAnd,
	"or":     OperatorTypeOr,
	"not ":   OperatorTypeNot,
	"is":     OperatorTypeIs,
	"is not": OperatorTypeIsNot,
	"in":     OperatorTypeIn,
	"not in": OperatorTypeNotIn,
}

// OperatorTypeNameMap corresponds to OperatorTypeNameMap: value -> text.
var OperatorTypeNameMap = map[OperatorType]string{}

func init() {
	for name, value := range ParseNodeTypeMap {
		ParseNodeTypeNameMap[value] = name
	}
	for name, value := range OperatorTypeMap {
		OperatorTypeNameMap[value] = name
	}
}
