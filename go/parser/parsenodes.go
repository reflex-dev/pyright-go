/*
 * parsenodes.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Definition of parse nodes that make up the Python abstract
 * syntax tree (AST).
 *
 * Transliterated from parser/parseNodes.ts (pyright 1.1.412).
 *
 * Shape notes:
 *
 *   - Every node is a struct embedding ParseNodeBase, which carries the range,
 *     id, parent and analyzer slot that ParseNodeBase<T> carries in TypeScript.
 *     The per-node `d` details object becomes an exported `D` struct field of a
 *     named type, so `node.d.testExpr` transliterates to `node.D.TestExpr`.
 *
 *   - TypeScript's node unions (ExpressionNode, StatementNode, PatternAtomNode,
 *     ...) become Go marker interfaces. A union member gets a private marker
 *     method, so assigning the wrong node type to a field is still a compile
 *     error, as it is in TypeScript.
 *
 *   - Optional `?: boolean` details are plain bools. JavaScript's `undefined`
 *     is falsy, so every read of those fields behaves identically; no call site
 *     distinguishes absent from false.
 */

package parser

import (
	"reflect"
	"sync/atomic"

	"github.com/microsoft/pyright/go/common"
)

// ParseNodeType corresponds to the ParseNodeType const enum. The numbering is
// load-bearing: it is asserted against in tests and serialized across the
// language server boundary.
type ParseNodeType int

const (
	ParseNodeTypeError ParseNodeType = iota // 0

	ParseNodeTypeArgument
	ParseNodeTypeAssert
	ParseNodeTypeAssignment
	ParseNodeTypeAssignmentExpression
	ParseNodeTypeAugmentedAssignment
	ParseNodeTypeAwait
	ParseNodeTypeBinaryOperation
	ParseNodeTypeBreak
	ParseNodeTypeCall

	ParseNodeTypeClass // 10
	ParseNodeTypeComprehension
	ParseNodeTypeComprehensionFor
	ParseNodeTypeComprehensionIf
	ParseNodeTypeConstant
	ParseNodeTypeContinue
	ParseNodeTypeDecorator
	ParseNodeTypeDel
	ParseNodeTypeDictionary
	ParseNodeTypeDictionaryExpandEntry

	ParseNodeTypeDictionaryKeyEntry // 20
	ParseNodeTypeEllipsis
	ParseNodeTypeIf
	ParseNodeTypeImport
	ParseNodeTypeImportAs
	ParseNodeTypeImportFrom
	ParseNodeTypeImportFromAs
	ParseNodeTypeIndex
	ParseNodeTypeExcept
	ParseNodeTypeFor

	ParseNodeTypeFormatString // 30
	ParseNodeTypeFunction
	ParseNodeTypeGlobal
	ParseNodeTypeLambda
	ParseNodeTypeList
	ParseNodeTypeMemberAccess
	ParseNodeTypeModule
	ParseNodeTypeModuleName
	ParseNodeTypeName
	ParseNodeTypeNonlocal

	ParseNodeTypeNumber // 40
	ParseNodeTypeParameter
	ParseNodeTypePass
	ParseNodeTypeRaise
	ParseNodeTypeReturn
	ParseNodeTypeSet
	ParseNodeTypeSlice
	ParseNodeTypeStatementList
	ParseNodeTypeStringList
	ParseNodeTypeString

	ParseNodeTypeSuite // 50
	ParseNodeTypeTernary
	ParseNodeTypeTuple
	ParseNodeTypeTry
	ParseNodeTypeTypeAnnotation
	ParseNodeTypeUnaryOperation
	ParseNodeTypeUnpack
	ParseNodeTypeWhile
	ParseNodeTypeWith
	ParseNodeTypeWithItem

	ParseNodeTypeYield // 60
	ParseNodeTypeYieldFrom
	ParseNodeTypeFunctionAnnotation
	ParseNodeTypeMatch
	ParseNodeTypeCase
	ParseNodeTypePatternSequence
	ParseNodeTypePatternAs
	ParseNodeTypePatternLiteral
	ParseNodeTypePatternClass
	ParseNodeTypePatternCapture

	ParseNodeTypePatternMapping // 70
	ParseNodeTypePatternMappingKeyEntry
	ParseNodeTypePatternMappingExpandEntry
	ParseNodeTypePatternValue
	ParseNodeTypePatternClassArgument
	ParseNodeTypeTypeParameter
	ParseNodeTypeTypeParameterList
	ParseNodeTypeTypeAlias
)

// ErrorExpressionCategory corresponds to the ErrorExpressionCategory const enum.
type ErrorExpressionCategory int

const (
	ErrorExpressionCategoryMissingIn ErrorExpressionCategory = iota
	ErrorExpressionCategoryMissingElse
	ErrorExpressionCategoryMissingExpression
	ErrorExpressionCategoryMissingIndexOrSlice
	ErrorExpressionCategoryMissingDecoratorCallName
	ErrorExpressionCategoryMissingCallCloseParen
	ErrorExpressionCategoryMissingIndexCloseBracket
	ErrorExpressionCategoryMissingMemberAccessName
	ErrorExpressionCategoryMissingTupleCloseParen
	ErrorExpressionCategoryMissingListCloseBracket
	ErrorExpressionCategoryMissingFunctionParameterList
	ErrorExpressionCategoryMissingPattern
	ErrorExpressionCategoryMissingPatternSubject
	ErrorExpressionCategoryMissingDictValue
	ErrorExpressionCategoryMissingKeywordArgValue
	ErrorExpressionCategoryMaxDepthExceeded
)

// ParamCategory corresponds to the ParamCategory const enum.
type ParamCategory int

const (
	ParamCategorySimple ParamCategory = iota
	ParamCategoryArgsList
	ParamCategoryKwargsDict
)

// ArgCategory corresponds to the ArgCategory const enum.
type ArgCategory int

const (
	ArgCategorySimple ArgCategory = iota
	ArgCategoryUnpackedList
	ArgCategoryUnpackedDictionary
)

// TypeParamKind corresponds to the TypeParamKind enum.
type TypeParamKind int

const (
	TypeParamKindTypeVar TypeParamKind = iota
	TypeParamKindTypeVarTuple
	TypeParamKindParamSpec
)

// ParseNode corresponds to the ParseNode union: everything that is a node.
type ParseNode interface {
	common.RangeItem
	GetNodeType() ParseNodeType
	NodeBase() *ParseNodeBase
}

// ParseNodeBase corresponds to ParseNodeBase<T>.
type ParseNodeBase struct {
	common.TextRange
	NodeType ParseNodeType

	// A unique ID given to each parse node.
	ID int

	Parent ParseNode

	// A reference to information computed in later passes.
	A any
}

// GetNodeType returns the node's type.
func (n *ParseNodeBase) GetNodeType() ParseNodeType { return n.NodeType }

// NodeBase returns the embedded base, giving generic code access to the range,
// id and parent regardless of the concrete node type.
func (n *ParseNodeBase) NodeBase() *ParseNodeBase { return n }

// GetRange satisfies common.RangeItem.
func (n *ParseNodeBase) GetRange() common.TextRange { return n.TextRange }

// nextNodeID corresponds to the _nextNodeId module counter. It is atomic
// because Go callers may parse files in parallel; as in the original, ids are
// assigned in node-allocation order and are not stable across runs that parse
// different sets of files.
var nextNodeID atomic.Int64

// GetNextNodeId corresponds to getNextNodeId().
func GetNextNodeId() int {
	return int(nextNodeID.Add(1))
}

func init() {
	// The TypeScript counter starts at 1.
	nextNodeID.Store(0)
}

func newBase(nodeType ParseNodeType, r common.TextRange) ParseNodeBase {
	return ParseNodeBase{
		TextRange: r,
		NodeType:  nodeType,
		ID:        GetNextNodeId(),
	}
}

// ExtendRange corresponds to extendRange().
func ExtendRange(node ParseNode, newRange common.TextRange) {
	base := node.NodeBase()
	extended := base.TextRange.Extend(newRange)
	base.Start = extended.Start
	base.Length = extended.Length
}

// extendToken is the common case of extending a node's range over a token.
func extendToken(node ParseNode, token Token) {
	ExtendRange(node, token.GetRange())
}

// setParent assigns a parent, tolerating a nil child the way `if (child)`
// guards do in the original.
func setParent(child ParseNode, parent ParseNode) {
	if child != nil {
		child.NodeBase().Parent = parent
	}
}

// ParseNodeArray corresponds to `(ParseNode | undefined)[]`.
type ParseNodeArray = []ParseNode

// -----------------------------------------------------------------------------
// Node unions
//
// Marker methods keep TypeScript's compile-time union checking. A node type
// carries one marker method per union it belongs to.
// -----------------------------------------------------------------------------

// ExpressionNode corresponds to the ExpressionNode union.
type ExpressionNode interface {
	ParseNode
	isExpressionNode()
}

// StatementNode corresponds to the StatementNode union.
type StatementNode interface {
	ParseNode
	isStatementNode()
}

// PatternAtomNode corresponds to the PatternAtomNode union.
type PatternAtomNode interface {
	ParseNode
	isPatternAtomNode()
}

// DictionaryEntryNode corresponds to the DictionaryEntryNode union.
type DictionaryEntryNode interface {
	ParseNode
	isDictionaryEntryNode()
}

// ComprehensionForIfNode corresponds to the ComprehensionForIfNode union.
type ComprehensionForIfNode interface {
	ParseNode
	isComprehensionForIfNode()
}

// PatternMappingEntryNode corresponds to the PatternMappingEntryNode union.
type PatternMappingEntryNode interface {
	ParseNode
	isPatternMappingEntryNode()
}

// SuiteOrIfNode covers `SuiteNode | IfNode`, the type of IfNode.d.elseSuite.
type SuiteOrIfNode interface {
	ParseNode
	isSuiteOrIfNode()
}

// PatternKeyNode covers `PatternLiteralNode | PatternValueNode | ErrorNode`.
type PatternKeyNode interface {
	ParseNode
	isPatternKeyNode()
}

// PatternValueTargetNode covers `PatternAsNode | ErrorNode`.
type PatternValueTargetNode interface {
	ParseNode
	isPatternValueTargetNode()
}

// ClassNameNode covers `NameNode | MemberAccessNode`.
type ClassNameNode interface {
	ParseNode
	isClassNameNode()
}

// StringOrFormatStringNode covers `StringNode | FormatStringNode`.
type StringOrFormatStringNode interface {
	ParseNode
	isStringOrFormatStringNode()
}

// EvaluationScopeNode corresponds to the EvaluationScopeNode union.
type EvaluationScopeNode interface {
	ParseNode
	isEvaluationScopeNode()
}

// ExecutionScopeNode corresponds to the ExecutionScopeNode union.
type ExecutionScopeNode interface {
	ParseNode
	isExecutionScopeNode()
}

// TypeParameterScopeNode corresponds to the TypeParameterScopeNode union.
type TypeParameterScopeNode interface {
	ParseNode
	isTypeParameterScopeNode()
}

// IsExpressionNode corresponds to isExpressionNode(). It is kept as a runtime
// predicate over ParseNodeType, exactly as in the original, because the parser
// calls it on values whose static type is only ParseNode.
//
// Note that the switch in the original omits ParseNodeType.Assignment and
// ParseNodeType.AugmentedAssignment even though the ExpressionNode union
// includes both, so this predicate returns false for them. The behavior is
// preserved as-is.
func IsExpressionNode(node ParseNode) bool {
	switch node.GetNodeType() {
	case ParseNodeTypeError,
		ParseNodeTypeUnaryOperation,
		ParseNodeTypeBinaryOperation,
		ParseNodeTypeAssignmentExpression,
		ParseNodeTypeTypeAnnotation,
		ParseNodeTypeAwait,
		ParseNodeTypeTernary,
		ParseNodeTypeUnpack,
		ParseNodeTypeTuple,
		ParseNodeTypeCall,
		ParseNodeTypeComprehension,
		ParseNodeTypeIndex,
		ParseNodeTypeSlice,
		ParseNodeTypeYield,
		ParseNodeTypeYieldFrom,
		ParseNodeTypeMemberAccess,
		ParseNodeTypeLambda,
		ParseNodeTypeName,
		ParseNodeTypeConstant,
		ParseNodeTypeEllipsis,
		ParseNodeTypeNumber,
		ParseNodeTypeString,
		ParseNodeTypeFormatString,
		ParseNodeTypeStringList,
		ParseNodeTypeDictionary,
		ParseNodeTypeList,
		ParseNodeTypeSet:
		return true

	default:
		return false
	}
}

// -----------------------------------------------------------------------------
// Nodes
// -----------------------------------------------------------------------------

// ModuleNode corresponds to ModuleNode.
type ModuleNode struct {
	ParseNodeBase
	D ModuleNodeDetails
}

// ModuleNodeDetails corresponds to ModuleNode's `d`.
type ModuleNodeDetails struct {
	Statements []StatementNode
}

// NewModuleNode corresponds to ModuleNode.create().
func NewModuleNode(r common.TextRange) *ModuleNode {
	return &ModuleNode{
		ParseNodeBase: newBase(ParseNodeTypeModule, r),
		D:             ModuleNodeDetails{Statements: []StatementNode{}},
	}
}

// SuiteNode corresponds to SuiteNode.
type SuiteNode struct {
	ParseNodeBase
	D SuiteNodeDetails
}

// SuiteNodeDetails corresponds to SuiteNode's `d`.
type SuiteNodeDetails struct {
	Statements  []StatementNode
	TypeComment *StringToken
}

// NewSuiteNode corresponds to SuiteNode.create().
func NewSuiteNode(r common.TextRange) *SuiteNode {
	return &SuiteNode{
		ParseNodeBase: newBase(ParseNodeTypeSuite, r),
		D:             SuiteNodeDetails{Statements: []StatementNode{}},
	}
}

// IfNode corresponds to IfNode.
type IfNode struct {
	ParseNodeBase
	D IfNodeDetails
}

// IfNodeDetails corresponds to IfNode's `d`.
type IfNodeDetails struct {
	FirstToken Token
	TestExpr   ExpressionNode
	IfSuite    *SuiteNode
	ElseSuite  SuiteOrIfNode
}

// NewIfNode corresponds to IfNode.create(). Pass nil for elseSuite to omit it.
func NewIfNode(ifOrElifToken Token, testExpr ExpressionNode, ifSuite *SuiteNode, elseSuite SuiteOrIfNode) *IfNode {
	node := &IfNode{
		ParseNodeBase: newBase(ParseNodeTypeIf, ifOrElifToken.GetRange()),
		D: IfNodeDetails{
			FirstToken: ifOrElifToken,
			TestExpr:   testExpr,
			IfSuite:    ifSuite,
			ElseSuite:  elseSuite,
		},
	}

	setParent(testExpr, node)
	setParent(ifSuite, node)

	ExtendRange(node, testExpr.GetRange())
	ExtendRange(node, ifSuite.GetRange())
	if elseSuite != nil {
		ExtendRange(node, elseSuite.GetRange())
		setParent(elseSuite, node)
	}

	return node
}

// WhileNode corresponds to WhileNode.
type WhileNode struct {
	ParseNodeBase
	D WhileNodeDetails
}

// WhileNodeDetails corresponds to WhileNode's `d`.
type WhileNodeDetails struct {
	FirstToken Token
	TestExpr   ExpressionNode
	WhileSuite *SuiteNode
	ElseSuite  *SuiteNode
}

// NewWhileNode corresponds to WhileNode.create().
func NewWhileNode(whileToken Token, testExpr ExpressionNode, whileSuite *SuiteNode) *WhileNode {
	node := &WhileNode{
		ParseNodeBase: newBase(ParseNodeTypeWhile, whileToken.GetRange()),
		D: WhileNodeDetails{
			FirstToken: whileToken,
			TestExpr:   testExpr,
			WhileSuite: whileSuite,
		},
	}

	setParent(testExpr, node)
	setParent(whileSuite, node)

	ExtendRange(node, whileSuite.GetRange())

	return node
}

// ForNode corresponds to ForNode.
type ForNode struct {
	ParseNodeBase
	D ForNodeDetails
}

// ForNodeDetails corresponds to ForNode's `d`.
type ForNodeDetails struct {
	FirstToken   Token
	IsAsync      bool
	AsyncToken   Token
	TargetExpr   ExpressionNode
	IterableExpr ExpressionNode
	ForSuite     *SuiteNode
	ElseSuite    *SuiteNode
	TypeComment  *StringToken
}

// NewForNode corresponds to ForNode.create().
func NewForNode(forToken Token, targetExpr, iterableExpr ExpressionNode, forSuite *SuiteNode) *ForNode {
	node := &ForNode{
		ParseNodeBase: newBase(ParseNodeTypeFor, forToken.GetRange()),
		D: ForNodeDetails{
			FirstToken:   forToken,
			TargetExpr:   targetExpr,
			IterableExpr: iterableExpr,
			ForSuite:     forSuite,
		},
	}

	setParent(targetExpr, node)
	setParent(iterableExpr, node)
	setParent(forSuite, node)

	ExtendRange(node, forSuite.GetRange())

	return node
}

// ComprehensionForNode corresponds to ComprehensionForNode.
type ComprehensionForNode struct {
	ParseNodeBase
	D ComprehensionForNodeDetails
}

// ComprehensionForNodeDetails corresponds to ComprehensionForNode's `d`.
type ComprehensionForNodeDetails struct {
	IsAsync      bool
	AsyncToken   Token
	TargetExpr   ExpressionNode
	IterableExpr ExpressionNode
}

// NewComprehensionForNode corresponds to ComprehensionForNode.create().
func NewComprehensionForNode(startToken Token, targetExpr, iterableExpr ExpressionNode) *ComprehensionForNode {
	node := &ComprehensionForNode{
		ParseNodeBase: newBase(ParseNodeTypeComprehensionFor, startToken.GetRange()),
		D: ComprehensionForNodeDetails{
			TargetExpr:   targetExpr,
			IterableExpr: iterableExpr,
		},
	}

	setParent(targetExpr, node)
	setParent(iterableExpr, node)

	ExtendRange(node, targetExpr.GetRange())
	ExtendRange(node, iterableExpr.GetRange())

	return node
}

// ComprehensionIfNode corresponds to ComprehensionIfNode.
type ComprehensionIfNode struct {
	ParseNodeBase
	D ComprehensionIfNodeDetails
}

// ComprehensionIfNodeDetails corresponds to ComprehensionIfNode's `d`.
type ComprehensionIfNodeDetails struct {
	TestExpr ExpressionNode
}

// NewComprehensionIfNode corresponds to ComprehensionIfNode.create().
func NewComprehensionIfNode(ifToken Token, testExpr ExpressionNode) *ComprehensionIfNode {
	node := &ComprehensionIfNode{
		ParseNodeBase: newBase(ParseNodeTypeComprehensionIf, ifToken.GetRange()),
		D:             ComprehensionIfNodeDetails{TestExpr: testExpr},
	}

	setParent(testExpr, node)
	ExtendRange(node, testExpr.GetRange())

	return node
}

// TryNode corresponds to TryNode.
type TryNode struct {
	ParseNodeBase
	D TryNodeDetails
}

// TryNodeDetails corresponds to TryNode's `d`.
type TryNodeDetails struct {
	FirstToken    Token
	TrySuite      *SuiteNode
	ExceptClauses []*ExceptNode
	ElseSuite     *SuiteNode
	FinallySuite  *SuiteNode
}

// NewTryNode corresponds to TryNode.create().
func NewTryNode(tryToken Token, trySuite *SuiteNode) *TryNode {
	node := &TryNode{
		ParseNodeBase: newBase(ParseNodeTypeTry, tryToken.GetRange()),
		D: TryNodeDetails{
			FirstToken:    tryToken,
			TrySuite:      trySuite,
			ExceptClauses: []*ExceptNode{},
		},
	}

	setParent(trySuite, node)
	ExtendRange(node, trySuite.GetRange())

	return node
}

// ExceptNode corresponds to ExceptNode.
type ExceptNode struct {
	ParseNodeBase
	D ExceptNodeDetails
}

// ExceptNodeDetails corresponds to ExceptNode's `d`.
type ExceptNodeDetails struct {
	TypeExpr      ExpressionNode
	Name          *NameNode
	ExceptSuite   *SuiteNode
	IsExceptGroup bool
	ExceptToken   Token
}

// NewExceptNode corresponds to ExceptNode.create().
func NewExceptNode(exceptToken Token, exceptSuite *SuiteNode, isExceptGroup bool) *ExceptNode {
	node := &ExceptNode{
		ParseNodeBase: newBase(ParseNodeTypeExcept, exceptToken.GetRange()),
		D: ExceptNodeDetails{
			ExceptSuite:   exceptSuite,
			IsExceptGroup: isExceptGroup,
			ExceptToken:   exceptToken,
		},
	}

	setParent(exceptSuite, node)
	ExtendRange(node, exceptSuite.GetRange())

	return node
}

// FunctionNode corresponds to FunctionNode.
type FunctionNode struct {
	ParseNodeBase
	D FunctionNodeDetails
}

// FunctionNodeDetails corresponds to FunctionNode's `d`.
type FunctionNodeDetails struct {
	FirstToken            Token
	Decorators            []*DecoratorNode
	IsAsync               bool
	Name                  *NameNode
	TypeParams            *TypeParameterListNode
	Params                []*ParameterNode
	ReturnAnnotation      ExpressionNode
	FuncAnnotationComment *FunctionAnnotationNode
	Suite                 *SuiteNode
}

// NewFunctionNode corresponds to FunctionNode.create(). Pass nil for typeParams
// to omit them.
func NewFunctionNode(defToken Token, name *NameNode, suite *SuiteNode, typeParams *TypeParameterListNode) *FunctionNode {
	node := &FunctionNode{
		ParseNodeBase: newBase(ParseNodeTypeFunction, defToken.GetRange()),
		D: FunctionNodeDetails{
			FirstToken: defToken,
			Decorators: []*DecoratorNode{},
			IsAsync:    false,
			Name:       name,
			TypeParams: typeParams,
			Params:     []*ParameterNode{},
			Suite:      suite,
		},
	}

	setParent(name, node)
	setParent(suite, node)
	if typeParams != nil {
		setParent(typeParams, node)
	}

	ExtendRange(node, suite.GetRange())

	return node
}

// ParameterNode corresponds to ParameterNode.
type ParameterNode struct {
	ParseNodeBase
	D ParameterNodeDetails
}

// ParameterNodeDetails corresponds to ParameterNode's `d`.
type ParameterNodeDetails struct {
	Category          ParamCategory
	Name              *NameNode
	Annotation        ExpressionNode
	AnnotationComment ExpressionNode
	DefaultValue      ExpressionNode
}

// NewParameterNode corresponds to ParameterNode.create().
func NewParameterNode(startToken Token, paramCategory ParamCategory) *ParameterNode {
	return &ParameterNode{
		ParseNodeBase: newBase(ParseNodeTypeParameter, startToken.GetRange()),
		D:             ParameterNodeDetails{Category: paramCategory},
	}
}

// ClassNode corresponds to ClassNode.
type ClassNode struct {
	ParseNodeBase
	D ClassNodeDetails
}

// ClassNodeDetails corresponds to ClassNode's `d`.
type ClassNodeDetails struct {
	FirstToken Token
	Decorators []*DecoratorNode
	Name       *NameNode
	TypeParams *TypeParameterListNode
	Arguments  []*ArgumentNode
	Suite      *SuiteNode
}

// NewClassNode corresponds to ClassNode.create().
func NewClassNode(classToken Token, name *NameNode, suite *SuiteNode, typeParams *TypeParameterListNode) *ClassNode {
	node := &ClassNode{
		ParseNodeBase: newBase(ParseNodeTypeClass, classToken.GetRange()),
		D: ClassNodeDetails{
			FirstToken: classToken,
			Decorators: []*DecoratorNode{},
			Name:       name,
			TypeParams: typeParams,
			Arguments:  []*ArgumentNode{},
			Suite:      suite,
		},
	}

	setParent(name, node)
	setParent(suite, node)
	if typeParams != nil {
		setParent(typeParams, node)
	}

	ExtendRange(node, suite.GetRange())

	return node
}

// NewClassNodeDummyForDecorators corresponds to
// ClassNode.createDummyForDecorators(). This variant is used to create a dummy
// class when the parser encounters decorators with no function or class
// declaration.
//
// The synthesized first token, name and suite are built with the same zero
// ranges and zero ids the original uses.
func NewClassNodeDummyForDecorators(decorators []*DecoratorNode) *ClassNode {
	dummyName := &NameNode{
		ParseNodeBase: ParseNodeBase{
			TextRange: common.TextRange{Start: decorators[0].Start, Length: 0},
			NodeType:  ParseNodeTypeName,
			ID:        0,
		},
		D: NameNodeDetails{
			Token: &IdentifierToken{
				TokenBase: TokenBase{
					TextRange: common.TextRange{Start: 0, Length: 0},
					Type:      TokenTypeIdentifier,
					Comments:  []*Comment{},
				},
				Value: "",
			},
			Value: "",
		},
	}

	dummySuite := &SuiteNode{
		ParseNodeBase: ParseNodeBase{
			TextRange: common.TextRange{Start: decorators[0].Start, Length: 0},
			NodeType:  ParseNodeTypeSuite,
			ID:        0,
		},
		D: SuiteNodeDetails{Statements: []StatementNode{}},
	}

	node := &ClassNode{
		ParseNodeBase: ParseNodeBase{
			TextRange: common.TextRange{Start: decorators[0].Start, Length: 0},
			NodeType:  ParseNodeTypeClass,
			ID:        GetNextNodeId(),
		},
		D: ClassNodeDetails{
			FirstToken: &TokenBase{
				TextRange: common.TextRange{Start: 0, Length: 0},
				Type:      TokenTypeKeyword,
				Comments:  []*Comment{},
			},
			Decorators: decorators,
			Name:       dummyName,
			TypeParams: nil,
			Arguments:  []*ArgumentNode{},
			Suite:      dummySuite,
		},
	}

	for _, decorator := range decorators {
		setParent(decorator, node)
		ExtendRange(node, decorator.GetRange())
	}

	setParent(node.D.Name, node)
	setParent(node.D.Suite, node)

	return node
}

// WithNode corresponds to WithNode.
type WithNode struct {
	ParseNodeBase
	D WithNodeDetails
}

// WithNodeDetails corresponds to WithNode's `d`.
type WithNodeDetails struct {
	FirstToken  Token
	IsAsync     bool
	AsyncToken  Token
	WithItems   []*WithItemNode
	Suite       *SuiteNode
	TypeComment *StringToken
}

// NewWithNode corresponds to WithNode.create().
func NewWithNode(withToken Token, suite *SuiteNode) *WithNode {
	node := &WithNode{
		ParseNodeBase: newBase(ParseNodeTypeWith, withToken.GetRange()),
		D: WithNodeDetails{
			FirstToken: withToken,
			WithItems:  []*WithItemNode{},
			Suite:      suite,
		},
	}

	setParent(suite, node)
	ExtendRange(node, suite.GetRange())

	return node
}

// WithItemNode corresponds to WithItemNode.
type WithItemNode struct {
	ParseNodeBase
	D WithItemNodeDetails
}

// WithItemNodeDetails corresponds to WithItemNode's `d`.
type WithItemNodeDetails struct {
	Expr   ExpressionNode
	Target ExpressionNode
}

// NewWithItemNode corresponds to WithItemNode.create().
func NewWithItemNode(expr ExpressionNode) *WithItemNode {
	node := &WithItemNode{
		ParseNodeBase: newBase(ParseNodeTypeWithItem, expr.GetRange()),
		D:             WithItemNodeDetails{Expr: expr},
	}

	setParent(expr, node)

	return node
}

// DecoratorNode corresponds to DecoratorNode.
type DecoratorNode struct {
	ParseNodeBase
	D DecoratorNodeDetails
}

// DecoratorNodeDetails corresponds to DecoratorNode's `d`.
type DecoratorNodeDetails struct {
	Expr ExpressionNode
}

// NewDecoratorNode corresponds to DecoratorNode.create().
func NewDecoratorNode(atToken Token, expr ExpressionNode) *DecoratorNode {
	node := &DecoratorNode{
		ParseNodeBase: newBase(ParseNodeTypeDecorator, atToken.GetRange()),
		D:             DecoratorNodeDetails{Expr: expr},
	}

	setParent(expr, node)
	ExtendRange(node, expr.GetRange())

	return node
}

// StatementListNode corresponds to StatementListNode.
type StatementListNode struct {
	ParseNodeBase
	D StatementListNodeDetails
}

// StatementListNodeDetails corresponds to StatementListNode's `d`.
type StatementListNodeDetails struct {
	FirstToken Token
	Statements []ParseNode
}

// NewStatementListNode corresponds to StatementListNode.create().
func NewStatementListNode(atToken Token) *StatementListNode {
	return &StatementListNode{
		ParseNodeBase: newBase(ParseNodeTypeStatementList, atToken.GetRange()),
		D:             StatementListNodeDetails{FirstToken: atToken, Statements: []ParseNode{}},
	}
}

// ErrorNode corresponds to ErrorNode.
type ErrorNode struct {
	ParseNodeBase
	D ErrorNodeDetails
}

// ErrorNodeDetails corresponds to ErrorNode's `d`.
type ErrorNodeDetails struct {
	Category   ErrorExpressionCategory
	Child      ExpressionNode
	Decorators []*DecoratorNode
}

// NewErrorNode corresponds to ErrorNode.create(). Pass nil for child and
// decorators to omit them.
func NewErrorNode(initialRange common.TextRange, category ErrorExpressionCategory, child ExpressionNode, decorators []*DecoratorNode) *ErrorNode {
	node := &ErrorNode{
		ParseNodeBase: newBase(ParseNodeTypeError, initialRange),
		D: ErrorNodeDetails{
			Category:   category,
			Child:      child,
			Decorators: decorators,
		},
	}

	if child != nil {
		setParent(child, node)
		ExtendRange(node, child.GetRange())
	}

	if decorators != nil {
		for _, decorator := range decorators {
			setParent(decorator, node)
		}

		if len(decorators) > 0 {
			ExtendRange(node, decorators[0].GetRange())
		}
	}

	return node
}

// UnaryOperationNode corresponds to UnaryOperationNode.
type UnaryOperationNode struct {
	ParseNodeBase
	D UnaryOperationNodeDetails
}

// UnaryOperationNodeDetails corresponds to UnaryOperationNode's `d`.
type UnaryOperationNodeDetails struct {
	Expr          ExpressionNode
	OperatorToken Token
	Operator      OperatorType
	HasParens     bool
}

// NewUnaryOperationNode corresponds to UnaryOperationNode.create().
func NewUnaryOperationNode(operatorToken Token, expr ExpressionNode, operator OperatorType) *UnaryOperationNode {
	node := &UnaryOperationNode{
		ParseNodeBase: newBase(ParseNodeTypeUnaryOperation, operatorToken.GetRange()),
		D: UnaryOperationNodeDetails{
			Operator:      operator,
			OperatorToken: operatorToken,
			Expr:          expr,
			HasParens:     false,
		},
	}

	setParent(expr, node)
	ExtendRange(node, expr.GetRange())

	return node
}

// BinaryOperationNode corresponds to BinaryOperationNode.
type BinaryOperationNode struct {
	ParseNodeBase
	D BinaryOperationNodeDetails
}

// BinaryOperationNodeDetails corresponds to BinaryOperationNode's `d`.
type BinaryOperationNodeDetails struct {
	LeftExpr      ExpressionNode
	OperatorToken Token
	Operator      OperatorType
	RightExpr     ExpressionNode
	HasParens     bool
}

// NewBinaryOperationNode corresponds to BinaryOperationNode.create().
func NewBinaryOperationNode(leftExpr, rightExpr ExpressionNode, operatorToken Token, operator OperatorType) *BinaryOperationNode {
	node := &BinaryOperationNode{
		ParseNodeBase: newBase(ParseNodeTypeBinaryOperation, leftExpr.GetRange()),
		D: BinaryOperationNodeDetails{
			LeftExpr:      leftExpr,
			OperatorToken: operatorToken,
			Operator:      operator,
			RightExpr:     rightExpr,
			HasParens:     false,
		},
	}

	setParent(leftExpr, node)
	setParent(rightExpr, node)
	ExtendRange(node, rightExpr.GetRange())

	return node
}

// AssignmentExpressionNode corresponds to AssignmentExpressionNode.
type AssignmentExpressionNode struct {
	ParseNodeBase
	D AssignmentExpressionNodeDetails
}

// AssignmentExpressionNodeDetails corresponds to AssignmentExpressionNode's `d`.
type AssignmentExpressionNodeDetails struct {
	Name        *NameNode
	WalrusToken Token
	RightExpr   ExpressionNode
	HasParens   bool
}

// NewAssignmentExpressionNode corresponds to AssignmentExpressionNode.create().
func NewAssignmentExpressionNode(name *NameNode, walrusToken Token, rightExpr ExpressionNode) *AssignmentExpressionNode {
	node := &AssignmentExpressionNode{
		ParseNodeBase: newBase(ParseNodeTypeAssignmentExpression, name.GetRange()),
		D: AssignmentExpressionNodeDetails{
			Name:        name,
			WalrusToken: walrusToken,
			RightExpr:   rightExpr,
			HasParens:   false,
		},
	}

	setParent(name, node)
	setParent(rightExpr, node)
	ExtendRange(node, rightExpr.GetRange())

	return node
}

// AssignmentNode corresponds to AssignmentNode.
type AssignmentNode struct {
	ParseNodeBase
	D AssignmentNodeDetails
}

// AssignmentNodeDetails corresponds to AssignmentNode's `d`.
type AssignmentNodeDetails struct {
	LeftExpr                 ExpressionNode
	RightExpr                ExpressionNode
	AnnotationComment        ExpressionNode
	ChainedAnnotationComment ExpressionNode
}

// NewAssignmentNode corresponds to AssignmentNode.create().
func NewAssignmentNode(leftExpr, rightExpr ExpressionNode) *AssignmentNode {
	node := &AssignmentNode{
		ParseNodeBase: newBase(ParseNodeTypeAssignment, leftExpr.GetRange()),
		D: AssignmentNodeDetails{
			LeftExpr:  leftExpr,
			RightExpr: rightExpr,
		},
	}

	setParent(leftExpr, node)
	setParent(rightExpr, node)
	ExtendRange(node, rightExpr.GetRange())

	return node
}

// TypeParameterNode corresponds to TypeParameterNode.
type TypeParameterNode struct {
	ParseNodeBase
	D TypeParameterNodeDetails
}

// TypeParameterNodeDetails corresponds to TypeParameterNode's `d`.
type TypeParameterNodeDetails struct {
	Name          *NameNode
	TypeParamKind TypeParamKind
	BoundExpr     ExpressionNode
	DefaultExpr   ExpressionNode
}

// NewTypeParameterNode corresponds to TypeParameterNode.create().
func NewTypeParameterNode(name *NameNode, typeParamKind TypeParamKind, boundExpr, defaultExpr ExpressionNode) *TypeParameterNode {
	node := &TypeParameterNode{
		ParseNodeBase: newBase(ParseNodeTypeTypeParameter, name.GetRange()),
		D: TypeParameterNodeDetails{
			Name:          name,
			TypeParamKind: typeParamKind,
			BoundExpr:     boundExpr,
			DefaultExpr:   defaultExpr,
		},
	}

	setParent(name, node)
	if boundExpr != nil {
		setParent(boundExpr, node)
		ExtendRange(node, boundExpr.GetRange())
	}
	if defaultExpr != nil {
		setParent(defaultExpr, node)
		ExtendRange(node, defaultExpr.GetRange())
	}

	return node
}

// TypeParameterListNode corresponds to TypeParameterListNode.
type TypeParameterListNode struct {
	ParseNodeBase
	D TypeParameterListNodeDetails
}

// TypeParameterListNodeDetails corresponds to TypeParameterListNode's `d`.
type TypeParameterListNodeDetails struct {
	Params []*TypeParameterNode
}

// NewTypeParameterListNode corresponds to TypeParameterListNode.create().
func NewTypeParameterListNode(startToken, endToken Token, params []*TypeParameterNode) *TypeParameterListNode {
	node := &TypeParameterListNode{
		ParseNodeBase: newBase(ParseNodeTypeTypeParameterList, startToken.GetRange()),
		D:             TypeParameterListNodeDetails{Params: params},
	}

	extendToken(node, endToken)
	for _, param := range params {
		ExtendRange(node, param.GetRange())
		setParent(param, node)
	}

	return node
}

// TypeAliasNode corresponds to TypeAliasNode.
type TypeAliasNode struct {
	ParseNodeBase
	D TypeAliasNodeDetails
}

// TypeAliasNodeDetails corresponds to TypeAliasNode's `d`.
type TypeAliasNodeDetails struct {
	FirstToken Token
	Name       *NameNode
	TypeParams *TypeParameterListNode
	Expr       ExpressionNode
}

// NewTypeAliasNode corresponds to TypeAliasNode.create().
func NewTypeAliasNode(typeToken *KeywordToken, name *NameNode, expr ExpressionNode, typeParams *TypeParameterListNode) *TypeAliasNode {
	node := &TypeAliasNode{
		ParseNodeBase: newBase(ParseNodeTypeTypeAlias, typeToken.GetRange()),
		D: TypeAliasNodeDetails{
			FirstToken: typeToken,
			Name:       name,
			TypeParams: typeParams,
			Expr:       expr,
		},
	}

	setParent(name, node)
	setParent(expr, node)
	if typeParams != nil {
		setParent(typeParams, node)
	}

	ExtendRange(node, expr.GetRange())

	return node
}

// TypeAnnotationNode corresponds to TypeAnnotationNode.
type TypeAnnotationNode struct {
	ParseNodeBase
	D TypeAnnotationNodeDetails
}

// TypeAnnotationNodeDetails corresponds to TypeAnnotationNode's `d`.
type TypeAnnotationNodeDetails struct {
	ValueExpr  ExpressionNode
	Annotation ExpressionNode
}

// NewTypeAnnotationNode corresponds to TypeAnnotationNode.create().
func NewTypeAnnotationNode(valueExpr, annotation ExpressionNode) *TypeAnnotationNode {
	node := &TypeAnnotationNode{
		ParseNodeBase: newBase(ParseNodeTypeTypeAnnotation, valueExpr.GetRange()),
		D: TypeAnnotationNodeDetails{
			ValueExpr:  valueExpr,
			Annotation: annotation,
		},
	}

	setParent(valueExpr, node)
	setParent(annotation, node)
	ExtendRange(node, annotation.GetRange())

	return node
}

// FunctionAnnotationNode corresponds to FunctionAnnotationNode.
type FunctionAnnotationNode struct {
	ParseNodeBase
	D FunctionAnnotationNodeDetails
}

// FunctionAnnotationNodeDetails corresponds to FunctionAnnotationNode's `d`.
type FunctionAnnotationNodeDetails struct {
	IsEllipsis       bool
	ParamAnnotations []ExpressionNode
	ReturnAnnotation ExpressionNode
}

// NewFunctionAnnotationNode corresponds to FunctionAnnotationNode.create().
func NewFunctionAnnotationNode(openParenToken Token, isEllipsis bool, paramAnnotations []ExpressionNode, returnAnnotation ExpressionNode) *FunctionAnnotationNode {
	node := &FunctionAnnotationNode{
		ParseNodeBase: newBase(ParseNodeTypeFunctionAnnotation, openParenToken.GetRange()),
		D: FunctionAnnotationNodeDetails{
			IsEllipsis:       isEllipsis,
			ParamAnnotations: paramAnnotations,
			ReturnAnnotation: returnAnnotation,
		},
	}

	for _, p := range paramAnnotations {
		setParent(p, node)
	}
	setParent(returnAnnotation, node)
	ExtendRange(node, returnAnnotation.GetRange())

	return node
}

// AugmentedAssignmentNode corresponds to AugmentedAssignmentNode.
type AugmentedAssignmentNode struct {
	ParseNodeBase
	D AugmentedAssignmentNodeDetails
}

// AugmentedAssignmentNodeDetails corresponds to AugmentedAssignmentNode's `d`.
type AugmentedAssignmentNodeDetails struct {
	LeftExpr  ExpressionNode
	Operator  OperatorType
	RightExpr ExpressionNode

	// DestExpr is a copy of the LeftExpr node. We use it as a place to hang
	// the result type, as opposed to the source type.
	DestExpr ExpressionNode
}

// NewAugmentedAssignmentNode corresponds to AugmentedAssignmentNode.create().
func NewAugmentedAssignmentNode(leftExpr, rightExpr ExpressionNode, operator OperatorType, destExpr ExpressionNode) *AugmentedAssignmentNode {
	node := &AugmentedAssignmentNode{
		ParseNodeBase: newBase(ParseNodeTypeAugmentedAssignment, leftExpr.GetRange()),
		D: AugmentedAssignmentNodeDetails{
			LeftExpr:  leftExpr,
			Operator:  operator,
			RightExpr: rightExpr,
			DestExpr:  destExpr,
		},
	}

	setParent(leftExpr, node)
	setParent(rightExpr, node)
	setParent(destExpr, node)
	ExtendRange(node, rightExpr.GetRange())

	return node
}

// AwaitNode corresponds to AwaitNode.
type AwaitNode struct {
	ParseNodeBase
	D AwaitNodeDetails
}

// AwaitNodeDetails corresponds to AwaitNode's `d`.
type AwaitNodeDetails struct {
	Expr       ExpressionNode
	AwaitToken Token
	HasParens  bool
}

// NewAwaitNode corresponds to AwaitNode.create().
func NewAwaitNode(awaitToken Token, expr ExpressionNode) *AwaitNode {
	node := &AwaitNode{
		ParseNodeBase: newBase(ParseNodeTypeAwait, awaitToken.GetRange()),
		D:             AwaitNodeDetails{Expr: expr, AwaitToken: awaitToken, HasParens: false},
	}

	setParent(expr, node)
	ExtendRange(node, expr.GetRange())

	return node
}

// TernaryNode corresponds to TernaryNode.
type TernaryNode struct {
	ParseNodeBase
	D TernaryNodeDetails
}

// TernaryNodeDetails corresponds to TernaryNode's `d`.
type TernaryNodeDetails struct {
	IfExpr   ExpressionNode
	TestExpr ExpressionNode
	ElseExpr ExpressionNode
}

// NewTernaryNode corresponds to TernaryNode.create().
func NewTernaryNode(ifExpr, testExpr, elseExpr ExpressionNode) *TernaryNode {
	node := &TernaryNode{
		ParseNodeBase: newBase(ParseNodeTypeTernary, ifExpr.GetRange()),
		D: TernaryNodeDetails{
			IfExpr:   ifExpr,
			TestExpr: testExpr,
			ElseExpr: elseExpr,
		},
	}

	setParent(ifExpr, node)
	setParent(testExpr, node)
	setParent(elseExpr, node)
	ExtendRange(node, elseExpr.GetRange())

	return node
}

// UnpackNode corresponds to UnpackNode.
type UnpackNode struct {
	ParseNodeBase
	D UnpackNodeDetails
}

// UnpackNodeDetails corresponds to UnpackNode's `d`.
type UnpackNodeDetails struct {
	Expr      ExpressionNode
	StarToken Token
}

// NewUnpackNode corresponds to UnpackNode.create().
func NewUnpackNode(starToken Token, expr ExpressionNode) *UnpackNode {
	node := &UnpackNode{
		ParseNodeBase: newBase(ParseNodeTypeUnpack, starToken.GetRange()),
		D: UnpackNodeDetails{
			Expr:      expr,
			StarToken: starToken,
		},
	}

	setParent(expr, node)
	ExtendRange(node, expr.GetRange())

	return node
}

// TupleNode corresponds to TupleNode.
type TupleNode struct {
	ParseNodeBase
	D TupleNodeDetails
}

// TupleNodeDetails corresponds to TupleNode's `d`.
type TupleNodeDetails struct {
	Items     []ExpressionNode
	HasParens bool
}

// NewTupleNode corresponds to TupleNode.create().
func NewTupleNode(r common.TextRange, hasParens bool) *TupleNode {
	return &TupleNode{
		ParseNodeBase: newBase(ParseNodeTypeTuple, r),
		D: TupleNodeDetails{
			Items:     []ExpressionNode{},
			HasParens: hasParens,
		},
	}
}

// CallNode corresponds to CallNode.
type CallNode struct {
	ParseNodeBase
	D CallNodeDetails
}

// CallNodeDetails corresponds to CallNode's `d`.
type CallNodeDetails struct {
	LeftExpr      ExpressionNode
	Args          []*ArgumentNode
	TrailingComma bool
}

// NewCallNode corresponds to CallNode.create().
func NewCallNode(leftExpr ExpressionNode, args []*ArgumentNode, trailingComma bool) *CallNode {
	node := &CallNode{
		ParseNodeBase: newBase(ParseNodeTypeCall, leftExpr.GetRange()),
		D: CallNodeDetails{
			LeftExpr:      leftExpr,
			Args:          args,
			TrailingComma: trailingComma,
		},
	}

	setParent(leftExpr, node)
	if len(args) > 0 {
		for _, arg := range args {
			setParent(arg, node)
		}
		ExtendRange(node, args[len(args)-1].GetRange())
	}

	return node
}

// ComprehensionNode corresponds to ComprehensionNode.
type ComprehensionNode struct {
	ParseNodeBase
	D ComprehensionNodeDetails
}

// ComprehensionNodeDetails corresponds to ComprehensionNode's `d`.
type ComprehensionNodeDetails struct {
	Expr        ParseNode
	ForIfNodes  []ComprehensionForIfNode
	IsGenerator bool
	HasParens   bool
}

// NewComprehensionNode corresponds to ComprehensionNode.create().
func NewComprehensionNode(expr ParseNode, isGenerator bool) *ComprehensionNode {
	node := &ComprehensionNode{
		ParseNodeBase: newBase(ParseNodeTypeComprehension, expr.GetRange()),
		D: ComprehensionNodeDetails{
			Expr:        expr,
			ForIfNodes:  []ComprehensionForIfNode{},
			IsGenerator: isGenerator,
			HasParens:   false,
		},
	}

	setParent(expr, node)

	return node
}

// IndexNode corresponds to IndexNode.
type IndexNode struct {
	ParseNodeBase
	D IndexNodeDetails
}

// IndexNodeDetails corresponds to IndexNode's `d`.
type IndexNodeDetails struct {
	LeftExpr      ExpressionNode
	Items         []*ArgumentNode
	TrailingComma bool
}

// NewIndexNode corresponds to IndexNode.create().
func NewIndexNode(leftExpr ExpressionNode, items []*ArgumentNode, trailingComma bool, closeBracketToken Token) *IndexNode {
	node := &IndexNode{
		ParseNodeBase: newBase(ParseNodeTypeIndex, leftExpr.GetRange()),
		D: IndexNodeDetails{
			LeftExpr:      leftExpr,
			Items:         items,
			TrailingComma: trailingComma,
		},
	}

	setParent(leftExpr, node)
	for _, item := range items {
		setParent(item, node)
	}
	extendToken(node, closeBracketToken)

	return node
}

// SliceNode corresponds to SliceNode.
type SliceNode struct {
	ParseNodeBase
	D SliceNodeDetails
}

// SliceNodeDetails corresponds to SliceNode's `d`.
type SliceNodeDetails struct {
	StartValue ExpressionNode
	EndValue   ExpressionNode
	StepValue  ExpressionNode
}

// NewSliceNode corresponds to SliceNode.create().
func NewSliceNode(r common.TextRange) *SliceNode {
	return &SliceNode{
		ParseNodeBase: newBase(ParseNodeTypeSlice, r),
	}
}

// YieldNode corresponds to YieldNode.
type YieldNode struct {
	ParseNodeBase
	D YieldNodeDetails
}

// YieldNodeDetails corresponds to YieldNode's `d`.
type YieldNodeDetails struct {
	Expr ExpressionNode
}

// NewYieldNode corresponds to YieldNode.create(). Pass nil for expr to omit it.
func NewYieldNode(yieldToken Token, expr ExpressionNode) *YieldNode {
	node := &YieldNode{
		ParseNodeBase: newBase(ParseNodeTypeYield, yieldToken.GetRange()),
		D:             YieldNodeDetails{Expr: expr},
	}

	if expr != nil {
		setParent(expr, node)
		ExtendRange(node, expr.GetRange())
	}

	return node
}

// YieldFromNode corresponds to YieldFromNode.
type YieldFromNode struct {
	ParseNodeBase
	D YieldFromNodeDetails
}

// YieldFromNodeDetails corresponds to YieldFromNode's `d`.
type YieldFromNodeDetails struct {
	Expr ExpressionNode
}

// NewYieldFromNode corresponds to YieldFromNode.create().
func NewYieldFromNode(yieldToken Token, expr ExpressionNode) *YieldFromNode {
	node := &YieldFromNode{
		ParseNodeBase: newBase(ParseNodeTypeYieldFrom, yieldToken.GetRange()),
		D:             YieldFromNodeDetails{Expr: expr},
	}

	setParent(expr, node)
	ExtendRange(node, expr.GetRange())

	return node
}

// MemberAccessNode corresponds to MemberAccessNode.
type MemberAccessNode struct {
	ParseNodeBase
	D MemberAccessNodeDetails
}

// MemberAccessNodeDetails corresponds to MemberAccessNode's `d`.
type MemberAccessNodeDetails struct {
	LeftExpr ExpressionNode
	Member   *NameNode
}

// NewMemberAccessNode corresponds to MemberAccessNode.create().
func NewMemberAccessNode(leftExpr ExpressionNode, member *NameNode) *MemberAccessNode {
	node := &MemberAccessNode{
		ParseNodeBase: newBase(ParseNodeTypeMemberAccess, leftExpr.GetRange()),
		D: MemberAccessNodeDetails{
			LeftExpr: leftExpr,
			Member:   member,
		},
	}

	setParent(leftExpr, node)
	setParent(member, node)
	ExtendRange(node, member.GetRange())

	return node
}

// LambdaNode corresponds to LambdaNode.
type LambdaNode struct {
	ParseNodeBase
	D LambdaNodeDetails
}

// LambdaNodeDetails corresponds to LambdaNode's `d`.
type LambdaNodeDetails struct {
	Params []*ParameterNode
	Expr   ExpressionNode
}

// NewLambdaNode corresponds to LambdaNode.create().
func NewLambdaNode(lambdaToken Token, expr ExpressionNode) *LambdaNode {
	node := &LambdaNode{
		ParseNodeBase: newBase(ParseNodeTypeLambda, lambdaToken.GetRange()),
		D: LambdaNodeDetails{
			Params: []*ParameterNode{},
			Expr:   expr,
		},
	}

	setParent(expr, node)
	ExtendRange(node, expr.GetRange())

	return node
}

// NameNode corresponds to NameNode.
type NameNode struct {
	ParseNodeBase
	D NameNodeDetails
}

// NameNodeDetails corresponds to NameNode's `d`.
type NameNodeDetails struct {
	Token *IdentifierToken
	Value string
}

// NewNameNode corresponds to NameNode.create().
func NewNameNode(nameToken *IdentifierToken) *NameNode {
	return &NameNode{
		ParseNodeBase: newBase(ParseNodeTypeName, nameToken.GetRange()),
		D: NameNodeDetails{
			Token: nameToken,
			Value: nameToken.Value,
		},
	}
}

// ConstantNode corresponds to ConstantNode.
type ConstantNode struct {
	ParseNodeBase
	D ConstantNodeDetails
}

// ConstantNodeDetails corresponds to ConstantNode's `d`.
type ConstantNodeDetails struct {
	ConstType KeywordType
}

// NewConstantNode corresponds to ConstantNode.create().
func NewConstantNode(token *KeywordToken) *ConstantNode {
	return &ConstantNode{
		ParseNodeBase: newBase(ParseNodeTypeConstant, token.GetRange()),
		D:             ConstantNodeDetails{ConstType: token.KeywordType},
	}
}

// EllipsisNode corresponds to EllipsisNode.
type EllipsisNode struct {
	ParseNodeBase
	D struct{}
}

// NewEllipsisNode corresponds to EllipsisNode.create().
func NewEllipsisNode(r common.TextRange) *EllipsisNode {
	return &EllipsisNode{ParseNodeBase: newBase(ParseNodeTypeEllipsis, r)}
}

// NumberNode corresponds to NumberNode.
type NumberNode struct {
	ParseNodeBase
	D NumberNodeDetails
}

// NumberNodeDetails corresponds to NumberNode's `d`.
type NumberNodeDetails struct {
	Value       NumberValue
	IsInteger   bool
	IsImaginary bool
}

// NewNumberNode corresponds to NumberNode.create().
func NewNumberNode(token *NumberToken) *NumberNode {
	return &NumberNode{
		ParseNodeBase: newBase(ParseNodeTypeNumber, token.GetRange()),
		D: NumberNodeDetails{
			Value:       token.Value,
			IsInteger:   token.IsInteger,
			IsImaginary: token.IsImaginary,
		},
	}
}

// StringNode corresponds to StringNode.
type StringNode struct {
	ParseNodeBase
	D StringNodeDetails
}

// StringNodeDetails corresponds to StringNode's `d`. Value is common.Text
// because an unescaped string literal can contain unpaired surrogates.
type StringNodeDetails struct {
	Token *StringToken
	Value common.Text
}

// NewStringNode corresponds to StringNode.create().
func NewStringNode(token *StringToken, value common.Text) *StringNode {
	return &StringNode{
		ParseNodeBase: newBase(ParseNodeTypeString, token.GetRange()),
		D: StringNodeDetails{
			Token: token,
			Value: value,
		},
	}
}

// FormatStringNode corresponds to FormatStringNode.
type FormatStringNode struct {
	ParseNodeBase
	D FormatStringNodeDetails
}

// FormatStringNodeDetails corresponds to FormatStringNode's `d`.
type FormatStringNodeDetails struct {
	Token        *FStringStartToken
	MiddleTokens []*FStringMiddleToken
	FieldExprs   []ExpressionNode
	FormatExprs  []ExpressionNode

	// A dummy "value" to simplify other code.
	Value common.Text
}

// NewFormatStringNode corresponds to FormatStringNode.create(). Pass nil for
// endToken to omit it.
func NewFormatStringNode(startToken *FStringStartToken, endToken *FStringEndToken, middleTokens []*FStringMiddleToken, fieldExprs, formatExprs []ExpressionNode) *FormatStringNode {
	node := &FormatStringNode{
		ParseNodeBase: newBase(ParseNodeTypeFormatString, startToken.GetRange()),
		D: FormatStringNodeDetails{
			Token:        startToken,
			MiddleTokens: middleTokens,
			FieldExprs:   fieldExprs,
			FormatExprs:  formatExprs,
			Value:        common.Text{},
		},
	}

	for _, expr := range fieldExprs {
		setParent(expr, node)
		ExtendRange(node, expr.GetRange())
	}
	for _, expr := range formatExprs {
		setParent(expr, node)
		ExtendRange(node, expr.GetRange())
	}
	if endToken != nil {
		extendToken(node, endToken)
	}

	return node
}

// StringListNode corresponds to StringListNode.
type StringListNode struct {
	ParseNodeBase
	D StringListNodeDetails
}

// StringListNodeDetails corresponds to StringListNode's `d`.
type StringListNodeDetails struct {
	Strings []StringOrFormatStringNode

	// If strings are found within the context of a type annotation, they are
	// further parsed into an expression.
	Annotation ExpressionNode

	// Indicates that the string list is enclosed in parens.
	HasParens bool
}

// NewStringListNode corresponds to StringListNode.create().
func NewStringListNode(strings []StringOrFormatStringNode) *StringListNode {
	node := &StringListNode{
		ParseNodeBase: newBase(ParseNodeTypeStringList, strings[0].GetRange()),
		D: StringListNodeDetails{
			Strings:   strings,
			HasParens: false,
		},
	}

	if len(strings) > 0 {
		for _, str := range strings {
			setParent(str, node)
		}
		ExtendRange(node, strings[len(strings)-1].GetRange())
	}

	return node
}

// DictionaryNode corresponds to DictionaryNode.
type DictionaryNode struct {
	ParseNodeBase
	D DictionaryNodeDetails
}

// DictionaryNodeDetails corresponds to DictionaryNode's `d`.
type DictionaryNodeDetails struct {
	Items              []DictionaryEntryNode
	TrailingCommaToken Token
}

// NewDictionaryNode corresponds to DictionaryNode.create().
func NewDictionaryNode(r common.TextRange) *DictionaryNode {
	return &DictionaryNode{
		ParseNodeBase: newBase(ParseNodeTypeDictionary, r),
		D:             DictionaryNodeDetails{Items: []DictionaryEntryNode{}},
	}
}

// DictionaryKeyEntryNode corresponds to DictionaryKeyEntryNode.
type DictionaryKeyEntryNode struct {
	ParseNodeBase
	D DictionaryKeyEntryNodeDetails
}

// DictionaryKeyEntryNodeDetails corresponds to DictionaryKeyEntryNode's `d`.
type DictionaryKeyEntryNodeDetails struct {
	KeyExpr   ExpressionNode
	ValueExpr ExpressionNode
}

// NewDictionaryKeyEntryNode corresponds to DictionaryKeyEntryNode.create().
func NewDictionaryKeyEntryNode(keyExpr, valueExpr ExpressionNode) *DictionaryKeyEntryNode {
	node := &DictionaryKeyEntryNode{
		ParseNodeBase: newBase(ParseNodeTypeDictionaryKeyEntry, keyExpr.GetRange()),
		D: DictionaryKeyEntryNodeDetails{
			KeyExpr:   keyExpr,
			ValueExpr: valueExpr,
		},
	}

	setParent(keyExpr, node)
	setParent(valueExpr, node)
	ExtendRange(node, valueExpr.GetRange())

	return node
}

// DictionaryExpandEntryNode corresponds to DictionaryExpandEntryNode.
type DictionaryExpandEntryNode struct {
	ParseNodeBase
	D DictionaryExpandEntryNodeDetails
}

// DictionaryExpandEntryNodeDetails corresponds to DictionaryExpandEntryNode's `d`.
type DictionaryExpandEntryNodeDetails struct {
	Expr ExpressionNode
}

// NewDictionaryExpandEntryNode corresponds to DictionaryExpandEntryNode.create().
func NewDictionaryExpandEntryNode(expr ExpressionNode) *DictionaryExpandEntryNode {
	node := &DictionaryExpandEntryNode{
		ParseNodeBase: newBase(ParseNodeTypeDictionaryExpandEntry, expr.GetRange()),
		D:             DictionaryExpandEntryNodeDetails{Expr: expr},
	}

	setParent(expr, node)

	return node
}

// SetNode corresponds to SetNode.
type SetNode struct {
	ParseNodeBase
	D SetNodeDetails
}

// SetNodeDetails corresponds to SetNode's `d`.
type SetNodeDetails struct {
	Items []ExpressionNode
}

// NewSetNode corresponds to SetNode.create().
func NewSetNode(r common.TextRange) *SetNode {
	return &SetNode{
		ParseNodeBase: newBase(ParseNodeTypeSet, r),
		D:             SetNodeDetails{Items: []ExpressionNode{}},
	}
}

// ListNode corresponds to ListNode.
type ListNode struct {
	ParseNodeBase
	D ListNodeDetails
}

// ListNodeDetails corresponds to ListNode's `d`.
type ListNodeDetails struct {
	Items []ExpressionNode
}

// NewListNode corresponds to ListNode.create().
func NewListNode(r common.TextRange) *ListNode {
	return &ListNode{
		ParseNodeBase: newBase(ParseNodeTypeList, r),
		D:             ListNodeDetails{Items: []ExpressionNode{}},
	}
}

// ArgumentNode corresponds to ArgumentNode.
type ArgumentNode struct {
	ParseNodeBase
	D ArgumentNodeDetails
}

// ArgumentNodeDetails corresponds to ArgumentNode's `d`.
type ArgumentNodeDetails struct {
	ArgCategory ArgCategory
	Name        *NameNode
	ValueExpr   ExpressionNode
}

// NewArgumentNode corresponds to ArgumentNode.create(). Pass nil for startToken
// to take the range from valueExpr.
func NewArgumentNode(startToken Token, valueExpr ExpressionNode, argCategory ArgCategory) *ArgumentNode {
	r := valueExpr.GetRange()
	if startToken != nil {
		r = startToken.GetRange()
	}

	node := &ArgumentNode{
		ParseNodeBase: newBase(ParseNodeTypeArgument, r),
		D: ArgumentNodeDetails{
			ArgCategory: argCategory,
			ValueExpr:   valueExpr,
		},
	}

	setParent(valueExpr, node)
	ExtendRange(node, valueExpr.GetRange())

	return node
}

// DelNode corresponds to DelNode.
type DelNode struct {
	ParseNodeBase
	D DelNodeDetails
}

// DelNodeDetails corresponds to DelNode's `d`.
type DelNodeDetails struct {
	Targets []ExpressionNode
}

// NewDelNode corresponds to DelNode.create().
func NewDelNode(delToken Token) *DelNode {
	return &DelNode{
		ParseNodeBase: newBase(ParseNodeTypeDel, delToken.GetRange()),
		D:             DelNodeDetails{Targets: []ExpressionNode{}},
	}
}

// PassNode corresponds to PassNode.
type PassNode struct {
	ParseNodeBase
	D struct{}
}

// NewPassNode corresponds to PassNode.create().
func NewPassNode(passToken common.TextRange) *PassNode {
	return &PassNode{ParseNodeBase: newBase(ParseNodeTypePass, passToken)}
}

// ImportNode corresponds to ImportNode.
type ImportNode struct {
	ParseNodeBase
	D ImportNodeDetails
}

// ImportNodeDetails corresponds to ImportNode's `d`.
type ImportNodeDetails struct {
	List      []*ImportAsNode
	IsLazy    bool
	LazyToken *KeywordToken
}

// NewImportNode corresponds to ImportNode.create().
func NewImportNode(importToken common.TextRange) *ImportNode {
	return &ImportNode{
		ParseNodeBase: newBase(ParseNodeTypeImport, importToken),
		D:             ImportNodeDetails{List: []*ImportAsNode{}},
	}
}

// ModuleNameNode corresponds to ModuleNameNode.
type ModuleNameNode struct {
	ParseNodeBase
	D ModuleNameNodeDetails
}

// ModuleNameNodeDetails corresponds to ModuleNameNode's `d`.
type ModuleNameNodeDetails struct {
	LeadingDots int
	NameParts   []*NameNode

	// This is an error condition used only for type completion.
	HasTrailingDot bool
}

// NewModuleNameNode corresponds to ModuleNameNode.create().
func NewModuleNameNode(r common.TextRange) *ModuleNameNode {
	return &ModuleNameNode{
		ParseNodeBase: newBase(ParseNodeTypeModuleName, r),
		D: ModuleNameNodeDetails{
			LeadingDots: 0,
			NameParts:   []*NameNode{},
		},
	}
}

// ImportAsNode corresponds to ImportAsNode.
type ImportAsNode struct {
	ParseNodeBase
	D ImportAsNodeDetails
}

// ImportAsNodeDetails corresponds to ImportAsNode's `d`.
type ImportAsNodeDetails struct {
	Module *ModuleNameNode
	Alias  *NameNode
}

// NewImportAsNode corresponds to ImportAsNode.create().
func NewImportAsNode(module *ModuleNameNode) *ImportAsNode {
	node := &ImportAsNode{
		ParseNodeBase: newBase(ParseNodeTypeImportAs, module.GetRange()),
		D:             ImportAsNodeDetails{Module: module},
	}

	setParent(module, node)

	return node
}

// ImportFromNode corresponds to ImportFromNode.
type ImportFromNode struct {
	ParseNodeBase
	D ImportFromNodeDetails
}

// ImportFromNodeDetails corresponds to ImportFromNode's `d`.
type ImportFromNodeDetails struct {
	Module           *ModuleNameNode
	Imports          []*ImportFromAsNode
	IsWildcardImport bool
	UsesParens       bool
	WildcardToken    Token
	MissingImport    bool
	IsLazy           bool
	LazyToken        *KeywordToken
}

// NewImportFromNode corresponds to ImportFromNode.create().
func NewImportFromNode(fromToken Token, module *ModuleNameNode) *ImportFromNode {
	node := &ImportFromNode{
		ParseNodeBase: newBase(ParseNodeTypeImportFrom, fromToken.GetRange()),
		D: ImportFromNodeDetails{
			Module:           module,
			Imports:          []*ImportFromAsNode{},
			IsWildcardImport: false,
			UsesParens:       false,
		},
	}

	setParent(module, node)
	ExtendRange(node, module.GetRange())

	return node
}

// ImportFromAsNode corresponds to ImportFromAsNode.
type ImportFromAsNode struct {
	ParseNodeBase
	D ImportFromAsNodeDetails
}

// ImportFromAsNodeDetails corresponds to ImportFromAsNode's `d`.
type ImportFromAsNodeDetails struct {
	Name  *NameNode
	Alias *NameNode
}

// NewImportFromAsNode corresponds to ImportFromAsNode.create().
func NewImportFromAsNode(name *NameNode) *ImportFromAsNode {
	node := &ImportFromAsNode{
		ParseNodeBase: newBase(ParseNodeTypeImportFromAs, name.GetRange()),
		D:             ImportFromAsNodeDetails{Name: name},
	}

	setParent(name, node)

	return node
}

// GlobalNode corresponds to GlobalNode.
type GlobalNode struct {
	ParseNodeBase
	D GlobalNodeDetails
}

// GlobalNodeDetails corresponds to GlobalNode's `d`.
type GlobalNodeDetails struct {
	Targets []*NameNode
}

// NewGlobalNode corresponds to GlobalNode.create().
func NewGlobalNode(r common.TextRange) *GlobalNode {
	return &GlobalNode{
		ParseNodeBase: newBase(ParseNodeTypeGlobal, r),
		D:             GlobalNodeDetails{Targets: []*NameNode{}},
	}
}

// NonlocalNode corresponds to NonlocalNode.
type NonlocalNode struct {
	ParseNodeBase
	D NonlocalNodeDetails
}

// NonlocalNodeDetails corresponds to NonlocalNode's `d`.
type NonlocalNodeDetails struct {
	Targets []*NameNode
}

// NewNonlocalNode corresponds to NonlocalNode.create().
func NewNonlocalNode(r common.TextRange) *NonlocalNode {
	return &NonlocalNode{
		ParseNodeBase: newBase(ParseNodeTypeNonlocal, r),
		D:             NonlocalNodeDetails{Targets: []*NameNode{}},
	}
}

// AssertNode corresponds to AssertNode.
type AssertNode struct {
	ParseNodeBase
	D AssertNodeDetails
}

// AssertNodeDetails corresponds to AssertNode's `d`.
type AssertNodeDetails struct {
	TestExpr      ExpressionNode
	ExceptionExpr ExpressionNode
}

// NewAssertNode corresponds to AssertNode.create().
func NewAssertNode(assertToken Token, testExpr ExpressionNode) *AssertNode {
	node := &AssertNode{
		ParseNodeBase: newBase(ParseNodeTypeAssert, assertToken.GetRange()),
		D:             AssertNodeDetails{TestExpr: testExpr},
	}

	setParent(testExpr, node)
	ExtendRange(node, testExpr.GetRange())

	return node
}

// BreakNode corresponds to BreakNode.
type BreakNode struct {
	ParseNodeBase
	D struct{}
}

// NewBreakNode corresponds to BreakNode.create().
func NewBreakNode(r common.TextRange) *BreakNode {
	return &BreakNode{ParseNodeBase: newBase(ParseNodeTypeBreak, r)}
}

// ContinueNode corresponds to ContinueNode.
type ContinueNode struct {
	ParseNodeBase
	D struct{}
}

// NewContinueNode corresponds to ContinueNode.create().
func NewContinueNode(r common.TextRange) *ContinueNode {
	return &ContinueNode{ParseNodeBase: newBase(ParseNodeTypeContinue, r)}
}

// ReturnNode corresponds to ReturnNode.
type ReturnNode struct {
	ParseNodeBase
	D ReturnNodeDetails
}

// ReturnNodeDetails corresponds to ReturnNode's `d`.
type ReturnNodeDetails struct {
	Expr ExpressionNode
}

// NewReturnNode corresponds to ReturnNode.create().
func NewReturnNode(r common.TextRange) *ReturnNode {
	return &ReturnNode{ParseNodeBase: newBase(ParseNodeTypeReturn, r)}
}

// RaiseNode corresponds to RaiseNode.
type RaiseNode struct {
	ParseNodeBase
	D RaiseNodeDetails
}

// RaiseNodeDetails corresponds to RaiseNode's `d`.
type RaiseNodeDetails struct {
	Expr     ExpressionNode
	FromExpr ExpressionNode
}

// NewRaiseNode corresponds to RaiseNode.create().
func NewRaiseNode(r common.TextRange) *RaiseNode {
	return &RaiseNode{ParseNodeBase: newBase(ParseNodeTypeRaise, r)}
}

// MatchNode corresponds to MatchNode.
type MatchNode struct {
	ParseNodeBase
	D MatchNodeDetails
}

// MatchNodeDetails corresponds to MatchNode's `d`.
type MatchNodeDetails struct {
	FirstToken Token
	Expr       ExpressionNode
	Cases      []*CaseNode
}

// NewMatchNode corresponds to MatchNode.create().
func NewMatchNode(matchToken Token, expr ExpressionNode) *MatchNode {
	node := &MatchNode{
		ParseNodeBase: newBase(ParseNodeTypeMatch, matchToken.GetRange()),
		D: MatchNodeDetails{
			FirstToken: matchToken,
			Expr:       expr,
			Cases:      []*CaseNode{},
		},
	}

	setParent(expr, node)
	ExtendRange(node, expr.GetRange())

	return node
}

// CaseNode corresponds to CaseNode.
type CaseNode struct {
	ParseNodeBase
	D CaseNodeDetails
}

// CaseNodeDetails corresponds to CaseNode's `d`.
type CaseNodeDetails struct {
	Pattern       PatternAtomNode
	IsIrrefutable bool
	GuardExpr     ExpressionNode
	Suite         *SuiteNode
}

// NewCaseNode corresponds to CaseNode.create().
func NewCaseNode(caseToken common.TextRange, pattern PatternAtomNode, isIrrefutable bool, guardExpr ExpressionNode, suite *SuiteNode) *CaseNode {
	node := &CaseNode{
		ParseNodeBase: newBase(ParseNodeTypeCase, caseToken),
		D: CaseNodeDetails{
			Pattern:       pattern,
			IsIrrefutable: isIrrefutable,
			GuardExpr:     guardExpr,
			Suite:         suite,
		},
	}

	ExtendRange(node, suite.GetRange())
	setParent(pattern, node)
	setParent(suite, node)
	if guardExpr != nil {
		setParent(guardExpr, node)
	}

	return node
}

// PatternSequenceNode corresponds to PatternSequenceNode.
type PatternSequenceNode struct {
	ParseNodeBase
	D PatternSequenceNodeDetails
}

// PatternSequenceNodeDetails corresponds to PatternSequenceNode's `d`.
type PatternSequenceNodeDetails struct {
	Entries []*PatternAsNode

	// StarEntryIndex is nil where the TypeScript version has `undefined`.
	StarEntryIndex *int
}

// NewPatternSequenceNode corresponds to PatternSequenceNode.create().
func NewPatternSequenceNode(firstToken common.TextRange, entries []*PatternAsNode) *PatternSequenceNode {
	starEntryIndex := -1
	for i, entry := range entries {
		if len(entry.D.OrPatterns) == 1 {
			if capture, ok := entry.D.OrPatterns[0].(*PatternCaptureNode); ok && capture.D.IsStar {
				starEntryIndex = i
				break
			}
		}
	}

	var starEntry *int
	if starEntryIndex >= 0 {
		value := starEntryIndex
		starEntry = &value
	}

	node := &PatternSequenceNode{
		ParseNodeBase: newBase(ParseNodeTypePatternSequence, firstToken),
		D: PatternSequenceNodeDetails{
			Entries:        entries,
			StarEntryIndex: starEntry,
		},
	}

	if len(entries) > 0 {
		ExtendRange(node, entries[len(entries)-1].GetRange())
	}
	for _, entry := range entries {
		setParent(entry, node)
	}

	return node
}

// PatternAsNode corresponds to PatternAsNode.
type PatternAsNode struct {
	ParseNodeBase
	D PatternAsNodeDetails
}

// PatternAsNodeDetails corresponds to PatternAsNode's `d`.
type PatternAsNodeDetails struct {
	OrPatterns []PatternAtomNode
	Target     *NameNode
}

// NewPatternAsNode corresponds to PatternAsNode.create(). Pass nil for target
// to omit it.
func NewPatternAsNode(orPatterns []PatternAtomNode, target *NameNode) *PatternAsNode {
	node := &PatternAsNode{
		ParseNodeBase: newBase(ParseNodeTypePatternAs, orPatterns[0].GetRange()),
		D: PatternAsNodeDetails{
			OrPatterns: orPatterns,
			Target:     target,
		},
	}

	if len(orPatterns) > 1 {
		ExtendRange(node, orPatterns[len(orPatterns)-1].GetRange())
	}
	for _, pattern := range orPatterns {
		setParent(pattern, node)
	}
	if target != nil {
		ExtendRange(node, target.GetRange())
		setParent(target, node)
	}

	return node
}

// PatternLiteralNode corresponds to PatternLiteralNode.
type PatternLiteralNode struct {
	ParseNodeBase
	D PatternLiteralNodeDetails
}

// PatternLiteralNodeDetails corresponds to PatternLiteralNode's `d`.
type PatternLiteralNodeDetails struct {
	Expr ExpressionNode
}

// NewPatternLiteralNode corresponds to PatternLiteralNode.create().
func NewPatternLiteralNode(expr ExpressionNode) *PatternLiteralNode {
	node := &PatternLiteralNode{
		ParseNodeBase: newBase(ParseNodeTypePatternLiteral, expr.GetRange()),
		D:             PatternLiteralNodeDetails{Expr: expr},
	}

	setParent(expr, node)

	return node
}

// PatternClassNode corresponds to PatternClassNode.
type PatternClassNode struct {
	ParseNodeBase
	D PatternClassNodeDetails
}

// PatternClassNodeDetails corresponds to PatternClassNode's `d`.
type PatternClassNodeDetails struct {
	ClassName ClassNameNode
	Args      []*PatternClassArgumentNode
}

// NewPatternClassNode corresponds to PatternClassNode.create().
func NewPatternClassNode(className ClassNameNode, args []*PatternClassArgumentNode) *PatternClassNode {
	node := &PatternClassNode{
		ParseNodeBase: newBase(ParseNodeTypePatternClass, className.GetRange()),
		D: PatternClassNodeDetails{
			ClassName: className,
			Args:      args,
		},
	}

	setParent(className, node)
	for _, arg := range args {
		setParent(arg, node)
	}
	if len(args) > 0 {
		ExtendRange(node, args[len(args)-1].GetRange())
	}

	return node
}

// PatternClassArgumentNode corresponds to PatternClassArgumentNode.
type PatternClassArgumentNode struct {
	ParseNodeBase
	D PatternClassArgumentNodeDetails
}

// PatternClassArgumentNodeDetails corresponds to PatternClassArgumentNode's `d`.
type PatternClassArgumentNodeDetails struct {
	Name    *NameNode
	Pattern *PatternAsNode
}

// NewPatternClassArgumentNode corresponds to PatternClassArgumentNode.create().
func NewPatternClassArgumentNode(pattern *PatternAsNode, name *NameNode) *PatternClassArgumentNode {
	node := &PatternClassArgumentNode{
		ParseNodeBase: newBase(ParseNodeTypePatternClassArgument, pattern.GetRange()),
		D: PatternClassArgumentNodeDetails{
			Pattern: pattern,
			Name:    name,
		},
	}

	setParent(pattern, node)
	if name != nil {
		ExtendRange(node, name.GetRange())
		setParent(name, node)
	}

	return node
}

// PatternCaptureNode corresponds to PatternCaptureNode.
type PatternCaptureNode struct {
	ParseNodeBase
	D PatternCaptureNodeDetails
}

// PatternCaptureNodeDetails corresponds to PatternCaptureNode's `d`.
type PatternCaptureNodeDetails struct {
	Target     *NameNode
	IsStar     bool
	IsWildcard bool
}

// NewPatternCaptureNode corresponds to PatternCaptureNode.create(). Pass nil
// for starToken to omit it.
func NewPatternCaptureNode(target *NameNode, starToken *common.TextRange) *PatternCaptureNode {
	node := &PatternCaptureNode{
		ParseNodeBase: newBase(ParseNodeTypePatternCapture, target.GetRange()),
		D: PatternCaptureNodeDetails{
			Target:     target,
			IsStar:     starToken != nil,
			IsWildcard: target.D.Value == "_",
		},
	}

	setParent(target, node)
	if starToken != nil {
		ExtendRange(node, *starToken)
	}

	return node
}

// PatternMappingNode corresponds to PatternMappingNode.
type PatternMappingNode struct {
	ParseNodeBase
	D PatternMappingNodeDetails
}

// PatternMappingNodeDetails corresponds to PatternMappingNode's `d`.
type PatternMappingNodeDetails struct {
	Entries []PatternMappingEntryNode
}

// NewPatternMappingNode corresponds to PatternMappingNode.create().
func NewPatternMappingNode(startToken common.TextRange, entries []PatternMappingEntryNode) *PatternMappingNode {
	node := &PatternMappingNode{
		ParseNodeBase: newBase(ParseNodeTypePatternMapping, startToken),
		D:             PatternMappingNodeDetails{Entries: entries},
	}

	if len(entries) > 0 {
		ExtendRange(node, entries[len(entries)-1].GetRange())
	}
	for _, entry := range entries {
		setParent(entry, node)
	}

	return node
}

// PatternMappingKeyEntryNode corresponds to PatternMappingKeyEntryNode.
type PatternMappingKeyEntryNode struct {
	ParseNodeBase
	D PatternMappingKeyEntryNodeDetails
}

// PatternMappingKeyEntryNodeDetails corresponds to PatternMappingKeyEntryNode's `d`.
type PatternMappingKeyEntryNodeDetails struct {
	KeyPattern   PatternKeyNode
	ValuePattern PatternValueTargetNode
}

// NewPatternMappingKeyEntryNode corresponds to PatternMappingKeyEntryNode.create().
func NewPatternMappingKeyEntryNode(keyPattern PatternKeyNode, valuePattern PatternValueTargetNode) *PatternMappingKeyEntryNode {
	node := &PatternMappingKeyEntryNode{
		ParseNodeBase: newBase(ParseNodeTypePatternMappingKeyEntry, keyPattern.GetRange()),
		D: PatternMappingKeyEntryNodeDetails{
			KeyPattern:   keyPattern,
			ValuePattern: valuePattern,
		},
	}

	setParent(keyPattern, node)
	setParent(valuePattern, node)
	ExtendRange(node, valuePattern.GetRange())

	return node
}

// PatternMappingExpandEntryNode corresponds to PatternMappingExpandEntryNode.
type PatternMappingExpandEntryNode struct {
	ParseNodeBase
	D PatternMappingExpandEntryNodeDetails
}

// PatternMappingExpandEntryNodeDetails corresponds to
// PatternMappingExpandEntryNode's `d`.
type PatternMappingExpandEntryNodeDetails struct {
	Target *NameNode
}

// NewPatternMappingExpandEntryNode corresponds to
// PatternMappingExpandEntryNode.create().
func NewPatternMappingExpandEntryNode(starStarToken common.TextRange, target *NameNode) *PatternMappingExpandEntryNode {
	node := &PatternMappingExpandEntryNode{
		ParseNodeBase: newBase(ParseNodeTypePatternMappingExpandEntry, starStarToken),
		D:             PatternMappingExpandEntryNodeDetails{Target: target},
	}

	setParent(target, node)
	ExtendRange(node, target.GetRange())

	return node
}

// PatternValueNode corresponds to PatternValueNode.
type PatternValueNode struct {
	ParseNodeBase
	D PatternValueNodeDetails
}

// PatternValueNodeDetails corresponds to PatternValueNode's `d`.
type PatternValueNodeDetails struct {
	Expr *MemberAccessNode
}

// NewPatternValueNode corresponds to PatternValueNode.create().
func NewPatternValueNode(expr *MemberAccessNode) *PatternValueNode {
	node := &PatternValueNode{
		ParseNodeBase: newBase(ParseNodeTypePatternValue, expr.GetRange()),
		D:             PatternValueNodeDetails{Expr: expr},
	}

	setParent(expr, node)

	return node
}

// shallowCopyWithNewID corresponds to the
//
//	const destExpr = Object.assign({}, leftExpr);
//	destExpr.id = getNextNodeId();
//
// pair in _parseExpressionStatement. The result is a distinct node with the same
// concrete type and the same field values -- including the same Parent, and the
// same (shared, not cloned) child nodes, whose Parent still points at the
// original -- and a fresh ID.
//
// This is done reflectively rather than with a 78-case type switch: the
// TypeScript is itself type-agnostic, and enumerating the cases by hand would be
// a much larger surface for a copy-paste mistake with no behavioral difference.
func shallowCopyWithNewID(node ExpressionNode) ExpressionNode {
	original := reflect.ValueOf(node).Elem()
	copied := reflect.New(original.Type())
	copied.Elem().Set(original)

	result := copied.Interface().(ExpressionNode)
	result.NodeBase().ID = GetNextNodeId()
	return result
}
