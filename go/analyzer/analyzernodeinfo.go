/*
 * analyzernodeinfo.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Defines objects that the analyzer(s) hang off the parse nodes in the parse
 * tree. It contains information collected during the binder phase that can be
 * used for later analysis steps or for language services (e.g. hover
 * information).
 *
 * Transliterated from analyzer/analyzerNodeInfo.ts (pyright 1.1.412).
 *
 * The TypeScript stores this on the untyped `a` slot of ParseNodeBase and casts
 * on the way out. Go's equivalent slot is `ParseNodeBase.A any`, and this file
 * is its only reader and writer.
 *
 * The representation diverges from the original for memory's sake, invisibly
 * outside this file. V8 lays a JavaScript object out with only the properties
 * actually set, and the overwhelmingly common shape here -- by an order of
 * magnitude -- is a node carrying a flowNode and nothing else. A Go struct
 * with all ten fields costs 104 bytes for every such node. So the `A` slot
 * holds either:
 *
 *   - a FlowNode directly (the flow-only shape, no allocation at all), or
 *   - a *AnalyzerNodeInfo, from the moment any other field is set.
 *
 * Every accessor handles both; SetFlowNode on an upgraded node writes the
 * struct field, and getAnalyzerInfoForWrite upgrades a direct FlowNode into
 * the struct it becomes a field of. The observable behavior of every Get/Set
 * pair is identical to storing the struct always.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// DunderAllInfo corresponds to the interface of the same name.
type DunderAllInfo struct {
	Names                        []string
	StringNodes                  []*parser.StringNode
	UsesUnsupportedDunderAllForm bool
}

// AnalyzerNodeInfo corresponds to the interface of the same name.
type AnalyzerNodeInfo struct {
	//-----------------------------------------------------------------
	// Set as part of import resolution

	// ImportInfo holds information about an import; used for import nodes only.
	ImportInfo *ImportResult

	//-----------------------------------------------------------------
	// Set by Binder

	// Scope is set for nodes that introduce scopes: modules, functions,
	// classes, lambdas, and list comprehensions. A scope is used to store
	// symbol names and their associated types and declarations.
	Scope *Scope

	// Declaration is set for functions and classes only.
	Declaration Declaration

	// FlowNode is the control flow information for this node.
	FlowNode FlowNode

	// AfterFlowNode is the control flow information at the end of this node.
	AfterFlowNode FlowNode

	// FileInfo holds info about the source file, used only on module nodes.
	FileInfo *AnalyzerFileInfo

	// CodeFlowExpressions is the set of expressions used within an execution
	// scope (module, function or lambda) that requires code flow analysis.
	CodeFlowExpressions *common.OrderedSet[string]

	// CodeFlowComplexity is a number that represents the complexity of a
	// function's code flow graph.
	CodeFlowComplexity float64

	// StaticConditionValue is the statically evaluated value of an if
	// statement's condition. Nil means "not statically evaluated", which the
	// original distinguishes from false.
	StaticConditionValue *bool

	// DunderAllInfo lists the __all__ symbols in the module.
	DunderAllInfo *DunderAllInfo
}

// ScopedNode corresponds to the union
// `ModuleNode | ClassNode | FunctionNode | LambdaNode | ComprehensionNode`.
// Go has no counterpart, so it aliases ParseNode.
type ScopedNode = parser.ParseNode

// CleanNodeAnalysisInfo cleans out all fields that are added by the analyzer
// phases (after the post-parse walker).
//
// The original clears each field with a separate guarded assignment; the effect
// is to reset every field except importInfo, which is set during import
// resolution rather than by an analyzer phase.
func CleanNodeAnalysisInfo(node parser.ParseNode) {
	nodeBase := node.NodeBase()
	switch info := nodeBase.A.(type) {
	case *AnalyzerNodeInfo:
		info.Scope = nil
		info.Declaration = nil
		info.FlowNode = nil
		info.AfterFlowNode = nil
		info.FileInfo = nil
		info.CodeFlowExpressions = nil
		info.CodeFlowComplexity = 0
		info.StaticConditionValue = nil
		info.DunderAllInfo = nil
	case FlowNode:
		// A directly stored FlowNode is an analyzer-phase field and nothing
		// else; clearing it empties the slot.
		nodeBase.A = nil
	}
}

// GetImportInfo returns nil where the TypeScript returns undefined.
func GetImportInfo(node parser.ParseNode) *ImportResult {
	info := getAnalyzerInfo(node)
	if info == nil {
		return nil
	}
	return info.ImportInfo
}

func SetImportInfo(node parser.ParseNode, importInfo *ImportResult) {
	getAnalyzerInfoForWrite(node).ImportInfo = importInfo
}

// GetScope returns nil where the TypeScript returns undefined.
func GetScope(node parser.ParseNode) *Scope {
	info := getAnalyzerInfo(node)
	if info == nil {
		return nil
	}
	return info.Scope
}

func SetScope(node parser.ParseNode, scope *Scope) {
	getAnalyzerInfoForWrite(node).Scope = scope
}

// GetDeclaration returns nil where the TypeScript returns undefined.
func GetDeclaration(node parser.ParseNode) Declaration {
	info := getAnalyzerInfo(node)
	if info == nil {
		return nil
	}
	return info.Declaration
}

func SetDeclaration(node parser.ParseNode, decl Declaration) {
	getAnalyzerInfoForWrite(node).Declaration = decl
}

// GetFlowNode returns nil where the TypeScript returns undefined.
func GetFlowNode(node parser.ParseNode) FlowNode {
	switch a := node.NodeBase().A.(type) {
	case *AnalyzerNodeInfo:
		return a.FlowNode
	case FlowNode:
		return a
	}
	return nil
}

func SetFlowNode(node parser.ParseNode, flowNode FlowNode) {
	nodeBase := node.NodeBase()
	if info, ok := nodeBase.A.(*AnalyzerNodeInfo); ok {
		info.FlowNode = flowNode
		return
	}
	// The flow-only shape: the FlowNode sits in the slot directly, replacing
	// nil or a previous FlowNode.
	nodeBase.A = flowNode
}

// GetAfterFlowNode returns nil where the TypeScript returns undefined.
func GetAfterFlowNode(node parser.ParseNode) FlowNode {
	info := getAnalyzerInfo(node)
	if info == nil {
		return nil
	}
	return info.AfterFlowNode
}

func SetAfterFlowNode(node parser.ParseNode, flowNode FlowNode) {
	getAnalyzerInfoForWrite(node).AfterFlowNode = flowNode
}

// GetFileInfo walks up to the enclosing module node and returns its file info.
//
// The original asserts both the walk and the result with non-null assertions,
// so a node that is not inside a module, or a module with no file info, is a
// programming error rather than a case to handle.
func GetFileInfo(node parser.ParseNode) *AnalyzerFileInfo {
	for node.GetNodeType() != parser.ParseNodeTypeModule {
		node = node.NodeBase().Parent
	}
	return getAnalyzerInfo(node).FileInfo
}

func SetFileInfo(node *parser.ModuleNode, fileInfo *AnalyzerFileInfo) {
	getAnalyzerInfoForWrite(node).FileInfo = fileInfo
}

// GetCodeFlowExpressions returns nil where the TypeScript returns undefined.
func GetCodeFlowExpressions(node ScopedNode) *common.OrderedSet[string] {
	info := getAnalyzerInfo(node)
	if info == nil {
		return nil
	}
	return info.CodeFlowExpressions
}

func SetCodeFlowExpressions(node ScopedNode, expressions *common.OrderedSet[string]) {
	getAnalyzerInfoForWrite(node).CodeFlowExpressions = expressions
}

// GetCodeFlowComplexity returns 0 where the TypeScript coalesces undefined
// to 0.
func GetCodeFlowComplexity(node ScopedNode) float64 {
	info := getAnalyzerInfo(node)
	if info == nil {
		return 0
	}
	return info.CodeFlowComplexity
}

func SetCodeFlowComplexity(node ScopedNode, complexity float64) {
	getAnalyzerInfoForWrite(node).CodeFlowComplexity = complexity
}

// GetStaticConditionValue returns nil where the TypeScript returns undefined,
// which it distinguishes from false.
func GetStaticConditionValue(node *parser.IfNode) *bool {
	info := getAnalyzerInfo(node)
	if info == nil {
		return nil
	}
	return info.StaticConditionValue
}

func SetStaticConditionValue(node *parser.IfNode, value *bool) {
	getAnalyzerInfoForWrite(node).StaticConditionValue = value
}

// GetDunderAllInfo returns nil where the TypeScript returns undefined.
func GetDunderAllInfo(node *parser.ModuleNode) *DunderAllInfo {
	info := getAnalyzerInfo(node)
	if info == nil {
		return nil
	}
	return info.DunderAllInfo
}

func SetDunderAllInfo(node *parser.ModuleNode, names *DunderAllInfo) {
	getAnalyzerInfoForWrite(node).DunderAllInfo = names
}

// IsCodeUnreachable corresponds to isCodeUnreachable.
func IsCodeUnreachable(node parser.ParseNode) bool {
	curNode := node

	// Walk up the parse tree until we find a node with an associated flow node.
	for curNode != nil {
		flowNode := GetFlowNode(curNode)
		if flowNode != nil {
			return (flowNode.FlowBase().Flags &
				(FlowFlagsUnreachableStaticCondition | FlowFlagsUnreachableStructural)) != 0
		}
		curNode = curNode.NodeBase().Parent
	}

	return false
}

// getAnalyzerInfo returns nil when the node has no analyzer info attached
// beyond, possibly, a directly stored FlowNode -- for which every field this
// function serves reads as its zero value, exactly as it would from a struct
// holding only FlowNode.
func getAnalyzerInfo(node parser.ParseNode) *AnalyzerNodeInfo {
	info, _ := node.NodeBase().A.(*AnalyzerNodeInfo)
	return info
}

// getAnalyzerInfoForWrite creates the analyzer info if it is not there yet,
// upgrading a directly stored FlowNode into the struct's field.
func getAnalyzerInfoForWrite(node parser.ParseNode) *AnalyzerNodeInfo {
	nodeBase := node.NodeBase()
	switch a := nodeBase.A.(type) {
	case *AnalyzerNodeInfo:
		return a
	case FlowNode:
		info := &AnalyzerNodeInfo{FlowNode: a}
		nodeBase.A = info
		return info
	}
	info := &AnalyzerNodeInfo{}
	nodeBase.A = info
	return info
}
