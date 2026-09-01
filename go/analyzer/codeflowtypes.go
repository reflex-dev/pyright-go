/*
 * codeflowtypes.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Data structures that track the code flow (or more accurately the inverse of
 * code flow) starting with return statements and working back to the entry.
 * This allows us to work out the types at each point of the code flow.
 *
 * This is largely based on the code flow engine in the TypeScript compiler.
 *
 * Transliterated from analyzer/codeFlowTypes.ts (pyright 1.1.412).
 *
 * The FlowNode hierarchy is a set of TypeScript interfaces that extend one
 * another and are distinguished at runtime by bits in `flags`. Here each is a
 * struct embedding FlowNodeBase, and FlowNode is an interface -- the same shape
 * the parse node and Type unions use. Code that switches on the flags still
 * works; code that reaches for a subtype's fields does a type assertion.
 */

package analyzer

import (
	"sync/atomic"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// FlowFlags corresponds to the enum of the same name.
//
// Note that bit 13 is unused: the original jumps from PostFinally (1 << 12) to
// VariableAnnotation (1 << 14).
type FlowFlags int

const (
	// FlowFlagsUnreachableStructural is code that is structurally unreachable
	// (e.g. following a return statement).
	FlowFlagsUnreachableStructural FlowFlags = 1 << 0

	// FlowFlagsUnreachableStaticCondition is code that is unreachable due to a
	// condition that the binder evaluates to False.
	FlowFlagsUnreachableStaticCondition FlowFlags = 1 << 1

	// FlowFlagsStart is the entry point.
	FlowFlagsStart FlowFlags = 1 << 2

	// FlowFlagsBranchLabel is a junction for forward control flow.
	FlowFlagsBranchLabel FlowFlags = 1 << 3

	// FlowFlagsLoopLabel is a junction for backward control flow.
	FlowFlagsLoopLabel FlowFlags = 1 << 4

	// FlowFlagsAssignment is an assignment statement.
	FlowFlagsAssignment FlowFlags = 1 << 5

	// FlowFlagsUnbind is used with assignment to indicate the target should be
	// unbound.
	FlowFlagsUnbind FlowFlags = 1 << 6

	// FlowFlagsWildcardImport is for "from X import *" statements.
	FlowFlagsWildcardImport FlowFlags = 1 << 7

	// FlowFlagsTrueCondition is a condition known to be true.
	FlowFlagsTrueCondition FlowFlags = 1 << 8

	// FlowFlagsFalseCondition is a condition known to be false.
	FlowFlagsFalseCondition FlowFlags = 1 << 9

	// FlowFlagsCall is a call node.
	FlowFlagsCall FlowFlags = 1 << 10

	// FlowFlagsPreFinallyGate is an injected edge that links the pre-finally
	// label and the pre-try flow.
	FlowFlagsPreFinallyGate FlowFlags = 1 << 11

	// FlowFlagsPostFinally is an injected edge that links post-finally flow
	// with the rest of the graph.
	FlowFlagsPostFinally FlowFlags = 1 << 12

	// FlowFlagsVariableAnnotation separates a variable annotation from its name
	// node.
	FlowFlagsVariableAnnotation FlowFlags = 1 << 14

	// FlowFlagsPostContextManager is a label used for context managers that
	// suppress exceptions.
	FlowFlagsPostContextManager FlowFlags = 1 << 15

	// FlowFlagsTrueNeverCondition is a condition whose type evaluates to Never
	// when narrowed in a positive test.
	FlowFlagsTrueNeverCondition FlowFlags = 1 << 16

	// FlowFlagsFalseNeverCondition is a condition whose type evaluates to Never
	// when narrowed in a negative test.
	FlowFlagsFalseNeverCondition FlowFlags = 1 << 17

	// FlowFlagsNarrowForPattern narrows the type of the subject expression
	// within a case statement.
	FlowFlagsNarrowForPattern FlowFlags = 1 << 18

	// FlowFlagsExhaustedMatch is a control flow gate that is closed when a
	// match is provably exhaustive.
	FlowFlagsExhaustedMatch FlowFlags = 1 << 19
)

// nextFlowNodeID backs GetUniqueFlowNodeID. The original is a plain
// module-level counter; JavaScript is single-threaded, so it needs no
// synchronization.
var nextFlowNodeID atomic.Int64

func init() {
	nextFlowNodeID.Store(1)
}

// GetUniqueFlowNodeID corresponds to getUniqueFlowNodeId.
func GetUniqueFlowNodeID() int {
	return int(nextFlowNodeID.Add(1) - 1)
}

// CodeFlowReferenceExpressionNode corresponds to the union
// `NameNode | MemberAccessNode | IndexNode | AssignmentExpressionNode`. Go has
// no counterpart, so it aliases ExpressionNode; IsCodeFlowSupportedForReference
// is the runtime test the TypeScript uses as a type guard.
type CodeFlowReferenceExpressionNode = parser.ExpressionNode

// FlowNode is the interface every flow node satisfies.
type FlowNode interface {
	// FlowBase returns the embedded FlowNodeBase, standing in for reading
	// `.flags` and `.id` off the union.
	FlowBase() *FlowNodeBase
}

// FlowNodeBase corresponds to the FlowNode interface.
type FlowNodeBase struct {
	Flags FlowFlags
	ID    int
}

// FlowBase satisfies FlowNode for every form through embedding.
func (f *FlowNodeBase) FlowBase() *FlowNodeBase { return f }

// FlowLabel represents a junction with multiple possible preceding control
// flows.
type FlowLabel struct {
	FlowNodeBase
	Antecedents []FlowNode

	// AffectedExpressions is the set of all expressions that require code flow
	// analysis through the loop or in branch paths to determine their types. If
	// an expression is not within this set, branch or loop analysis can be
	// skipped and determined from the first antecedent only.
	AffectedExpressions *common.OrderedSet[string]
}

// FlowBranchLabel corresponds to the interface of the same name.
type FlowBranchLabel struct {
	FlowLabel

	// PreBranchAntecedent, if set, represents a flow node that precedes (i.e.
	// is higher up in the control flow graph than) all of the antecedents of
	// this branch label. If an expression is not affected by the branch label,
	// the entire flow node can be skipped, and processing can proceed at this
	// label.
	PreBranchAntecedent FlowNode
}

// FlowAssignment represents a node that assigns a value.
type FlowAssignment struct {
	FlowNodeBase
	Node           CodeFlowReferenceExpressionNode
	Antecedent     FlowNode
	TargetSymbolID int
}

// FlowVariableAnnotation separates a variable annotation node from its type
// annotation. For example, in the declaration "foo: bar", the "bar" needs to be
// associated with a flow node that precedes the "foo". This is important if the
// same name is used for both (e.g. "foo: foo") and we need to determine that
// the annotation refers to a symbol within an outer scope.
type FlowVariableAnnotation struct {
	FlowNodeBase
	Antecedent FlowNode
}

// FlowWildcardImport is similar to FlowAssignment but used specifically for
// wildcard "from X import *" statements.
type FlowWildcardImport struct {
	FlowNodeBase
	Node       *parser.ImportFromNode
	Names      []string
	Antecedent FlowNode
}

// FlowCondition represents a condition that is known to be true or false at the
// node's location in the control flow.
type FlowCondition struct {
	FlowNodeBase
	Expression parser.ExpressionNode
	Reference  *parser.NameNode
	Antecedent FlowNode
}

// FlowNarrowForPattern corresponds to the interface of the same name. Statement
// is a CaseNode or MatchNode.
type FlowNarrowForPattern struct {
	FlowNodeBase
	SubjectExpression parser.ExpressionNode
	Statement         parser.ParseNode
	Antecedent        FlowNode
}

// FlowExhaustedMatch represents a control flow gate that is "closed" if a match
// statement can be statically proven to exhaust all cases (i.e. the narrowed
// type of the subject expression is Never at the bottom).
type FlowExhaustedMatch struct {
	FlowNodeBase
	Node              *parser.MatchNode
	SubjectExpression parser.ExpressionNode
	Antecedent        FlowNode
}

// FlowCall records a call, which may raise exceptions, thus affecting the code
// flow and making subsequent code unreachable.
type FlowCall struct {
	FlowNodeBase
	Node       *parser.CallNode
	Antecedent FlowNode
}

// FlowPreFinallyGate is described by the comment in binder.ts's visitTry method,
// together with FlowPostFinally.
type FlowPreFinallyGate struct {
	FlowNodeBase
	Antecedent FlowNode
}

// FlowPostFinally corresponds to the interface of the same name.
type FlowPostFinally struct {
	FlowNodeBase
	Antecedent     FlowNode
	FinallyNode    *parser.SuiteNode
	PreFinallyGate *FlowPreFinallyGate
}

// FlowPostContextManagerLabel corresponds to the interface of the same name.
type FlowPostContextManagerLabel struct {
	FlowLabel
	Expressions []parser.ExpressionNode
	IsAsync     bool

	// BlockIfSwallowsExceptions blocks code flow analysis along this path when
	// it is true and the context manager swallows exceptions. Conversely, if
	// the context manager does not swallow exceptions and this value is false,
	// analysis along this path is also blocked.
	BlockIfSwallowsExceptions bool
}

// IsCodeFlowSupportedForReference corresponds to
// isCodeFlowSupportedForReference. In the TypeScript this is a type guard
// narrowing to CodeFlowReferenceExpressionNode.
func IsCodeFlowSupportedForReference(reference parser.ExpressionNode) bool {
	switch node := reference.(type) {
	case *parser.NameNode:
		return true

	case *parser.MemberAccessNode:
		return IsCodeFlowSupportedForReference(node.D.LeftExpr)

	case *parser.AssignmentExpressionNode:
		return true

	case *parser.IndexNode:
		// Allow index expressions that have a single subscript that is a
		// literal integer or string value.
		if len(node.D.Items) != 1 ||
			node.D.TrailingComma ||
			node.D.Items[0].D.Name != nil ||
			node.D.Items[0].D.ArgCategory != parser.ArgCategorySimple {
			return false
		}

		subscriptNode := node.D.Items[0].D.ValueExpr

		isIntegerIndex := false
		if numberNode, ok := subscriptNode.(*parser.NumberNode); ok {
			isIntegerIndex = !numberNode.D.IsImaginary && numberNode.D.IsInteger
		}

		isNegativeIntegerIndex := false
		if unaryNode, ok := subscriptNode.(*parser.UnaryOperationNode); ok &&
			unaryNode.D.Operator == parser.OperatorTypeSubtract {
			if numberNode, ok := unaryNode.D.Expr.(*parser.NumberNode); ok {
				isNegativeIntegerIndex = !numberNode.D.IsImaginary && numberNode.D.IsInteger
			}
		}

		isStringIndex := false
		if stringListNode, ok := subscriptNode.(*parser.StringListNode); ok &&
			len(stringListNode.D.Strings) == 1 {
			_, isStringIndex = stringListNode.D.Strings[0].(*parser.StringNode)
		}

		if !isIntegerIndex && !isNegativeIntegerIndex && !isStringIndex {
			return false
		}

		return IsCodeFlowSupportedForReference(node.D.LeftExpr)
	}

	return false
}

// CreateKeyForReference corresponds to createKeyForReference.
func CreateKeyForReference(reference CodeFlowReferenceExpressionNode) string {
	var key string

	switch node := reference.(type) {
	case *parser.NameNode:
		key = node.D.Value

	case *parser.AssignmentExpressionNode:
		key = node.D.Name.D.Value

	case *parser.MemberAccessNode:
		leftKey := CreateKeyForReference(node.D.LeftExpr)
		key = leftKey + "." + node.D.Member.D.Value

	case *parser.IndexNode:
		leftKey := CreateKeyForReference(node.D.LeftExpr)
		assert(len(node.D.Items) == 1, "")
		expr := node.D.Items[0].D.ValueExpr

		switch valueNode := expr.(type) {
		case *parser.NumberNode:
			key = leftKey + "[" + valueNode.D.Value.String() + "]"

		case *parser.StringListNode:
			assert(len(valueNode.D.Strings) == 1, "")
			stringNode, ok := valueNode.D.Strings[0].(*parser.StringNode)
			assert(ok, "")
			key = leftKey + "[\"" + stringNode.D.Value.String() + "\"]"

		case *parser.UnaryOperationNode:
			operandNode, ok := valueNode.D.Expr.(*parser.NumberNode)
			if valueNode.D.Operator != parser.OperatorTypeSubtract || !ok {
				fail("createKeyForReference received unexpected index type")
				return ""
			}
			key = leftKey + "[-" + operandNode.D.Value.String() + "]"

		default:
			fail("createKeyForReference received unexpected index type")
			return ""
		}

	default:
		fail("createKeyForReference received unexpected expression type")
		return ""
	}

	return key
}

// CreateKeysForReferenceSubexpressions corresponds to
// createKeysForReferenceSubexpressions.
func CreateKeysForReferenceSubexpressions(reference CodeFlowReferenceExpressionNode) []string {
	switch node := reference.(type) {
	case *parser.NameNode:
		return []string{CreateKeyForReference(node)}

	case *parser.AssignmentExpressionNode:
		return []string{CreateKeyForReference(node.D.Name)}

	case *parser.MemberAccessNode:
		return append(CreateKeysForReferenceSubexpressions(node.D.LeftExpr), CreateKeyForReference(node))

	case *parser.IndexNode:
		return append(CreateKeysForReferenceSubexpressions(node.D.LeftExpr), CreateKeyForReference(node))
	}

	fail("createKeyForReference received unexpected expression type")
	return nil
}

// WildcardImportReferenceKey is the reference key that corresponds to a
// wildcard import.
const WildcardImportReferenceKey = "*"
