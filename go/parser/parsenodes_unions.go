/*
 * parsenodes_unions.go
 *
 * Union membership for the parse node types.
 *
 * TypeScript expresses these as `export type ExpressionNode = ErrorNode | ...`
 * unions. Go has no union types, so each union is an interface carrying a
 * private marker method, and every member declares that method here. Keeping
 * the declarations together makes the membership lists directly checkable
 * against the `export type` declarations at the end of parseNodes.ts.
 */

package parser

// ExpressionNode members.
func (*ErrorNode) isExpressionNode()                {}
func (*UnaryOperationNode) isExpressionNode()       {}
func (*BinaryOperationNode) isExpressionNode()      {}
func (*AssignmentNode) isExpressionNode()           {}
func (*TypeAnnotationNode) isExpressionNode()       {}
func (*AssignmentExpressionNode) isExpressionNode() {}
func (*AugmentedAssignmentNode) isExpressionNode()  {}
func (*AwaitNode) isExpressionNode()                {}
func (*TernaryNode) isExpressionNode()              {}
func (*UnpackNode) isExpressionNode()               {}
func (*TupleNode) isExpressionNode()                {}
func (*CallNode) isExpressionNode()                 {}
func (*ComprehensionNode) isExpressionNode()        {}
func (*IndexNode) isExpressionNode()                {}
func (*SliceNode) isExpressionNode()                {}
func (*YieldNode) isExpressionNode()                {}
func (*YieldFromNode) isExpressionNode()            {}
func (*MemberAccessNode) isExpressionNode()         {}
func (*LambdaNode) isExpressionNode()               {}
func (*NameNode) isExpressionNode()                 {}
func (*ConstantNode) isExpressionNode()             {}
func (*EllipsisNode) isExpressionNode()             {}
func (*NumberNode) isExpressionNode()               {}
func (*StringNode) isExpressionNode()               {}
func (*FormatStringNode) isExpressionNode()         {}
func (*StringListNode) isExpressionNode()           {}
func (*DictionaryNode) isExpressionNode()           {}
func (*ListNode) isExpressionNode()                 {}
func (*SetNode) isExpressionNode()                  {}

// StatementNode members.
func (*IfNode) isStatementNode()            {}
func (*WhileNode) isStatementNode()         {}
func (*ForNode) isStatementNode()           {}
func (*TryNode) isStatementNode()           {}
func (*FunctionNode) isStatementNode()      {}
func (*ClassNode) isStatementNode()         {}
func (*WithNode) isStatementNode()          {}
func (*StatementListNode) isStatementNode() {}
func (*MatchNode) isStatementNode()         {}
func (*TypeAliasNode) isStatementNode()     {}
func (*ErrorNode) isStatementNode()         {}

// PatternAtomNode members.
func (*PatternSequenceNode) isPatternAtomNode() {}
func (*PatternLiteralNode) isPatternAtomNode()  {}
func (*PatternClassNode) isPatternAtomNode()    {}
func (*PatternAsNode) isPatternAtomNode()       {}
func (*PatternCaptureNode) isPatternAtomNode()  {}
func (*PatternMappingNode) isPatternAtomNode()  {}
func (*PatternValueNode) isPatternAtomNode()    {}
func (*ErrorNode) isPatternAtomNode()           {}

// DictionaryEntryNode members.
func (*DictionaryKeyEntryNode) isDictionaryEntryNode()    {}
func (*DictionaryExpandEntryNode) isDictionaryEntryNode() {}
func (*ComprehensionNode) isDictionaryEntryNode()         {}

// ComprehensionForIfNode members.
func (*ComprehensionForNode) isComprehensionForIfNode() {}
func (*ComprehensionIfNode) isComprehensionForIfNode()  {}

// PatternMappingEntryNode members.
func (*PatternMappingKeyEntryNode) isPatternMappingEntryNode()    {}
func (*PatternMappingExpandEntryNode) isPatternMappingEntryNode() {}
func (*ErrorNode) isPatternMappingEntryNode()                     {}

// SuiteOrIfNode members, for IfNode.d.elseSuite.
func (*SuiteNode) isSuiteOrIfNode() {}
func (*IfNode) isSuiteOrIfNode()    {}

// PatternKeyNode members, for PatternMappingKeyEntryNode.d.keyPattern.
func (*PatternLiteralNode) isPatternKeyNode() {}
func (*PatternValueNode) isPatternKeyNode()   {}
func (*ErrorNode) isPatternKeyNode()          {}

// PatternValueTargetNode members, for PatternMappingKeyEntryNode.d.valuePattern.
func (*PatternAsNode) isPatternValueTargetNode() {}
func (*ErrorNode) isPatternValueTargetNode()     {}

// ClassNameNode members, for PatternClassNode.d.className.
func (*NameNode) isClassNameNode()         {}
func (*MemberAccessNode) isClassNameNode() {}

// StringOrFormatStringNode members, for StringListNode.d.strings.
func (*StringNode) isStringOrFormatStringNode()       {}
func (*FormatStringNode) isStringOrFormatStringNode() {}

// EvaluationScopeNode members.
func (*LambdaNode) isEvaluationScopeNode()            {}
func (*FunctionNode) isEvaluationScopeNode()          {}
func (*ModuleNode) isEvaluationScopeNode()            {}
func (*ClassNode) isEvaluationScopeNode()             {}
func (*ComprehensionNode) isEvaluationScopeNode()     {}
func (*TypeParameterListNode) isEvaluationScopeNode() {}

// ExecutionScopeNode members.
func (*LambdaNode) isExecutionScopeNode()            {}
func (*FunctionNode) isExecutionScopeNode()          {}
func (*ModuleNode) isExecutionScopeNode()            {}
func (*TypeParameterListNode) isExecutionScopeNode() {}

// TypeParameterScopeNode members.
func (*FunctionNode) isTypeParameterScopeNode()  {}
func (*ClassNode) isTypeParameterScopeNode()     {}
func (*TypeAliasNode) isTypeParameterScopeNode() {}

// Compile-time assertions that every node type satisfies ParseNode. The
// TypeScript ParseNode union lists all 78; if a node is added or renamed
// without updating this list, the build breaks here rather than at a use site.
var _ = []ParseNode{
	(*ErrorNode)(nil),
	(*ArgumentNode)(nil),
	(*AssertNode)(nil),
	(*AssignmentExpressionNode)(nil),
	(*AssignmentNode)(nil),
	(*AugmentedAssignmentNode)(nil),
	(*AwaitNode)(nil),
	(*BinaryOperationNode)(nil),
	(*BreakNode)(nil),
	(*CallNode)(nil),
	(*CaseNode)(nil),
	(*ClassNode)(nil),
	(*ComprehensionNode)(nil),
	(*ComprehensionForNode)(nil),
	(*ComprehensionIfNode)(nil),
	(*ConstantNode)(nil),
	(*ContinueNode)(nil),
	(*DecoratorNode)(nil),
	(*DelNode)(nil),
	(*DictionaryNode)(nil),
	(*DictionaryExpandEntryNode)(nil),
	(*DictionaryKeyEntryNode)(nil),
	(*EllipsisNode)(nil),
	(*ExceptNode)(nil),
	(*ForNode)(nil),
	(*FormatStringNode)(nil),
	(*FunctionNode)(nil),
	(*FunctionAnnotationNode)(nil),
	(*GlobalNode)(nil),
	(*IfNode)(nil),
	(*ImportNode)(nil),
	(*ImportAsNode)(nil),
	(*ImportFromNode)(nil),
	(*ImportFromAsNode)(nil),
	(*IndexNode)(nil),
	(*LambdaNode)(nil),
	(*ListNode)(nil),
	(*MatchNode)(nil),
	(*MemberAccessNode)(nil),
	(*ModuleNode)(nil),
	(*ModuleNameNode)(nil),
	(*NameNode)(nil),
	(*NonlocalNode)(nil),
	(*NumberNode)(nil),
	(*ParameterNode)(nil),
	(*PassNode)(nil),
	(*PatternAsNode)(nil),
	(*PatternCaptureNode)(nil),
	(*PatternClassNode)(nil),
	(*PatternClassArgumentNode)(nil),
	(*PatternLiteralNode)(nil),
	(*PatternMappingNode)(nil),
	(*PatternMappingExpandEntryNode)(nil),
	(*PatternMappingKeyEntryNode)(nil),
	(*PatternSequenceNode)(nil),
	(*PatternValueNode)(nil),
	(*RaiseNode)(nil),
	(*ReturnNode)(nil),
	(*SetNode)(nil),
	(*SliceNode)(nil),
	(*StatementListNode)(nil),
	(*StringNode)(nil),
	(*StringListNode)(nil),
	(*SuiteNode)(nil),
	(*TernaryNode)(nil),
	(*TryNode)(nil),
	(*TupleNode)(nil),
	(*TypeAliasNode)(nil),
	(*TypeAnnotationNode)(nil),
	(*TypeParameterNode)(nil),
	(*TypeParameterListNode)(nil),
	(*UnaryOperationNode)(nil),
	(*UnpackNode)(nil),
	(*WhileNode)(nil),
	(*WithNode)(nil),
	(*WithItemNode)(nil),
	(*YieldNode)(nil),
	(*YieldFromNode)(nil),
}

// asExpression wraps a node that is not in the ExpressionNode union so it can
// be passed where one is required. The embedded ParseNode promotes GetNodeType,
// NodeBase and the range accessors, so the wrapper behaves as the node it wraps
// for everything except a type assertion to the concrete node type.
type asExpression struct{ ParseNode }

func (asExpression) isExpressionNode() {}

// AsExpressionNode stands in for the original's `node as any as ExpressionNode`.
// pyright uses that cast in exactly one place -- patternMatching.ts passes a
// PatternClassArgumentNode as the error node of a member lookup, with a comment
// saying it is "OK to use it in this context" -- and TypeScript's structural
// typing lets it. Go's ExpressionNode carries an unexported marker method, so
// the same lie has to be told explicitly.
//
// Passing nil instead is not equivalent: the error node reaches
// validateCallArgs, which a descriptor's __get__ invocation goes through, so a
// nil node makes a property read return the property object rather than the
// getter's return type.
func AsExpressionNode(node ParseNode) ExpressionNode {
	if node == nil {
		return nil
	}
	if expr, ok := node.(ExpressionNode); ok {
		return expr
	}
	return asExpression{node}
}
