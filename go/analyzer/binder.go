/*
 * binder.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * A parse tree walker that performs basic name binding (creation of scopes and
 * associated symbol tables).
 *
 * The binder walks the parse tree by scopes starting at the module level. When
 * a new scope is detected, it is pushed onto a list and walked separately at a
 * later time. (The exception is a class scope, which is immediately walked.)
 * Walking the tree in this manner simulates the order in which execution
 * normally occurs in a Python file. The binder attempts to statically detect
 * runtime errors that would be reported by the python interpreter when
 * executing the code. This binder doesn't perform any static type checking.
 *
 * Transliterated from analyzer/binder.ts (pyright 1.1.412), split across
 * binder*.go by concern. This file holds the walker itself: the state, the
 * constructor, BindModule, and the scope and deferred-binding machinery.
 *
 * Three things shape the whole port and are worth knowing before reading any of
 * it:
 *
 *   - The original is one class with 48 visit overrides and 98 private helpers,
 *     all sharing mutable walk state through `this`. That maps directly onto a
 *     Go struct with methods; the split into files is purely for navigation and
 *     the analyzer package sees them all as one type.
 *   - Almost every helper that takes a callback saves a field, calls back, and
 *     restores it. Go closures capture the variable rather than its value, so
 *     each of those becomes an explicit save/restore around a func() call --
 *     never a defer, because several of them deliberately skip the restore on
 *     one path.
 *   - The original leans on `undefined` in three distinguishable ways that Go's
 *     zero value collapses: an absent map entry, an absent optional field, and
 *     an empty array (which is truthy in JavaScript). Each site says which one
 *     it is.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// memberAccessInfo corresponds to the interface of the same name.
type memberAccessInfo struct {
	ClassNode        *parser.ClassNode
	MethodNode       *parser.FunctionNode
	ClassScope       *Scope
	IsInstanceMember bool
}

// deferredBindingTask corresponds to the interface of the same name.
type deferredBindingTask struct {
	Scope               *Scope
	CodeFlowExpressions *common.OrderedSet[string]
	Callback            func()
}

// finalInfo corresponds to the interface of the same name.
type finalInfo struct {
	IsFinal       bool
	FinalTypeNode parser.ExpressionNode
}

// classVarInfo corresponds to the interface of the same name.
type classVarInfo struct {
	IsClassVar       bool
	ClassVarTypeNode parser.ExpressionNode
}

// narrowExprOptions corresponds to the interface of the same name. All three
// fields are optional in the original and default to false.
type narrowExprOptions struct {
	FilterForNeverNarrowing     bool
	IsComplexExpression         bool
	AllowDiscriminatedNarrowing bool
}

// flowNodeComplexityContribution is the amount added to the complexity factor
// for each flow node within an execution context. Without this, the complexity
// calculation fails to take into account large numbers of non-cyclical flow
// nodes. This number is somewhat arbitrary and is tuned empirically.
const flowNodeComplexityContribution = 0.025

// unreachableStaticConditionFlowNode and unreachableStructuralFlowNode
// correspond to the two static fields of the same names. They are allocated
// once at package initialization, exactly as the class's static initializers
// are evaluated once when the module is loaded, so they consume the first two
// flow node ids of the process.
var (
	unreachableStaticConditionFlowNode FlowNode = &FlowNodeBase{
		Flags: FlowFlagsUnreachableStaticCondition,
		ID:    GetUniqueFlowNodeID(),
	}

	unreachableStructuralFlowNode FlowNode = &FlowNodeBase{
		Flags: FlowFlagsUnreachableStructural,
		ID:    GetUniqueFlowNodeID(),
	}
)

// Binder corresponds to the class of the same name.
type Binder struct {
	*ParseTreeWalker

	fileInfo *AnalyzerFileInfo

	// moduleSymbolOnly and cellChainIndex are the two constructor parameters
	// the original declares inline.
	moduleSymbolOnly bool
	cellChainIndex   CellChainIndexProvider

	// deferredBindingTasks is a queue of deferred analysis operations.
	deferredBindingTasks []*deferredBindingTask

	// currentScope is the current scope in effect. The original declares it
	// with `!` because it is assigned before any use rather than in the
	// constructor.
	currentScope *Scope

	// currentFlowNode is the current control-flow node.
	currentFlowNode FlowNode

	// targetFunctionDeclaration is the current target function declaration, if
	// currently binding a function. This allows return and yield statements to
	// be added to the function declaration.
	targetFunctionDeclaration *FunctionDeclaration

	// currentBreakTarget is the flow node label that is the target of a "break"
	// statement.
	currentBreakTarget *FlowLabel

	// currentContinueTarget is the flow node label that is the target of a
	// "continue" statement.
	currentContinueTarget *FlowLabel

	// currentTrueTarget and currentFalseTarget are the flow nodes used for
	// if/else and while/else statements.
	currentTrueTarget  *FlowLabel
	currentFalseTarget *FlowLabel

	// currentExceptTargets holds the flow nodes used within try blocks.
	currentExceptTargets []*FlowLabel

	// finallyTargets holds the flow nodes used within try/finally flows.
	finallyTargets []*FlowLabel

	// currentReturnTarget is the flow node used for return statements.
	currentReturnTarget *FlowLabel

	// currentScopeCodeFlowExpressions is the set of expressions within the
	// current execution scope that require code flow analysis to resolve.
	currentScopeCodeFlowExpressions *common.OrderedSet[string]

	// currentMatchSubjExpr is the current match expression, if a match
	// statement is actively being bound.
	currentMatchSubjExpr parser.ExpressionNode

	// typingImportAliases holds aliases of "typing" and "typing_extensions".
	typingImportAliases []string

	// sysImportAliases holds aliases of "sys".
	sysImportAliases []string

	// dataclassesImportAliases holds aliases of "dataclasses".
	dataclassesImportAliases []string

	// typingSymbolAliases maps imports of specific symbols imported from
	// "typing" and "typing_extensions" to the names they alias to.
	typingSymbolAliases *common.OrderedMap[string, string]

	// dataclassesSymbolAliases maps imports of specific symbols imported from
	// "dataclasses" to the names they alias to.
	dataclassesSymbolAliases *common.OrderedMap[string, string]

	// dunderAllNames is the list of names statically assigned to the __all__
	// symbol. The original distinguishes "no __all__ seen" (undefined) from
	// "__all__ = []" (an empty array, which is truthy), so dunderAllNamesSet
	// carries the distinction Go's nil slice cannot.
	dunderAllNames    []string
	dunderAllNamesSet bool

	// dunderAllStringNodes is the list of string nodes associated with the
	// "__all__" symbol.
	dunderAllStringNodes []*parser.StringNode

	// usesUnsupportedDunderAllForm records that one or more statements are
	// manipulating __all__ in a manner that a static analyzer doesn't
	// understand.
	usesUnsupportedDunderAllForm bool

	// isInExceptSuite records whether code located within an except block is
	// currently being bound.
	isInExceptSuite bool

	// isInAnnotatedAnnotation records whether the type arguments to an
	// Annotated type annotation are currently being walked.
	isInAnnotatedAnnotation bool

	// dunderSlotsEntries is the list of names assigned to __slots__ within a
	// class. nil means undefined, which is distinct from an empty list.
	dunderSlotsEntries    []*parser.StringListNode
	dunderSlotsEntriesSet bool

	// potentialHiddenSymbols maps symbols at the module level that may be
	// externally hidden depending on whether they are listed in the __all__
	// list.
	potentialHiddenSymbols *common.OrderedMap[string, *Symbol]

	// potentialWildcardReexportSymbols maps symbols imported via wildcard
	// import in a py.typed (non-stub) module that should be treated as private
	// if this module defines __all__ and the symbol is not listed there.
	potentialWildcardReexportSymbols *common.OrderedMap[string, *Symbol]

	// potentialPrivateSymbols maps symbols at the module level that may be
	// private depending on whether they are listed in the __all__ list.
	potentialPrivateSymbols *common.OrderedMap[string, *Symbol]

	// codeFlowComplexity estimates the overall complexity of the code flow
	// graph for the current function.
	codeFlowComplexity float64
}

// NewBinder corresponds to the Binder constructor. The TypeScript defaults
// moduleSymbolOnly to false and leaves cellChainIndex undefined; pass false and
// nil for those.
func NewBinder(
	fileInfo *AnalyzerFileInfo,
	moduleSymbolOnly bool,
	cellChainIndex CellChainIndexProvider,
) *Binder {
	binder := &Binder{
		fileInfo:                         fileInfo,
		moduleSymbolOnly:                 moduleSymbolOnly,
		cellChainIndex:                   cellChainIndex,
		deferredBindingTasks:             []*deferredBindingTask{},
		currentExceptTargets:             []*FlowLabel{},
		finallyTargets:                   []*FlowLabel{},
		typingImportAliases:              []string{},
		sysImportAliases:                 []string{},
		dataclassesImportAliases:         []string{},
		typingSymbolAliases:              common.NewOrderedMap[string, string](),
		dataclassesSymbolAliases:         common.NewOrderedMap[string, string](),
		dunderAllStringNodes:             []*parser.StringNode{},
		potentialHiddenSymbols:           common.NewOrderedMap[string, *Symbol](),
		potentialWildcardReexportSymbols: common.NewOrderedMap[string, *Symbol](),
		potentialPrivateSymbols:          common.NewOrderedMap[string, *Symbol](),
	}
	binder.ParseTreeWalker = NewParseTreeWalker(binder)
	return binder
}

// BindModule corresponds to bindModule.
func (b *Binder) BindModule(node *parser.ModuleNode) {
	// We'll assume that if there is no builtins scope provided, we must be
	// binding the builtins module itself.
	isBuiltInModule := b.fileInfo.BuiltinsScope == nil
	chainedModuleLevelScopeLookup := b.createCellChainModuleLevelLookup()

	b.addTypingImportAliasesFromBuiltinsScope()

	scopeType := ScopeTypeModule
	if isBuiltInModule {
		scopeType = ScopeTypeBuiltin
	}

	b.createNewScope(
		scopeType,
		b.fileInfo.BuiltinsScope,
		nil, // proxyScope
		chainedModuleLevelScopeLookup,
		func() {
			SetScope(node, b.currentScope)
			SetFlowNode(node, b.currentFlowNode)

			// Bind implicit names.
			// List taken from https://docs.python.org/3/reference/import.html#__name__
			b.addImplicitSymbolToCurrentScope("__name__", node, IntrinsicTypeStr, true)
			b.addImplicitSymbolToCurrentScope("__loader__", node, IntrinsicTypeAny, true)
			b.addImplicitSymbolToCurrentScope("__package__", node, IntrinsicTypeStrOrNone, true)
			b.addImplicitSymbolToCurrentScope("__spec__", node, IntrinsicTypeAny, true)
			b.addImplicitSymbolToCurrentScope("__path__", node, IntrinsicTypeMutableSequenceStr, true)
			b.addImplicitSymbolToCurrentScope("__file__", node, IntrinsicTypeStr, true)
			b.addImplicitSymbolToCurrentScope("__cached__", node, IntrinsicTypeStr, true)
			b.addImplicitSymbolToCurrentScope("__annotations__", node, IntrinsicTypeDictStrAny, true)
			b.addImplicitSymbolToCurrentScope("__dict__", node, IntrinsicTypeDictStrAny, true)
			b.addImplicitSymbolToCurrentScope("__builtins__", node, IntrinsicTypeAny, true)
			b.addImplicitSymbolToCurrentScope("__doc__", node, IntrinsicTypeStrOrNone, true)

			// Create a start node for the module.
			b.currentFlowNode = b.createStartFlowNode()

			b.walkStatementsAndReportUnreachable(node.D.Statements)

			// Associate the code flow node at the end of the module with the
			// module.
			SetAfterFlowNode(node, b.currentFlowNode)

			SetCodeFlowExpressions(node, b.currentScopeCodeFlowExpressions)
			SetCodeFlowComplexity(node, b.codeFlowComplexity)
		},
	)

	// Perform all analysis that was deferred during the first pass.
	b.bindDeferred()

	// Use the __all__ list to determine whether any potential private symbols
	// should be made externally hidden or private. When __all__ uses an
	// unsupported form (e.g., dynamic construction like
	// __all__ = _components + [...]), we can't determine membership statically;
	// fall back to name-convention heuristics so that underscore-prefixed names
	// are still treated as private while normally-named symbols avoid false
	// positives.
	shouldProcess := func(name string) bool {
		return !b.usesUnsupportedDunderAllForm || IsPrivateOrProtectedName(name)
	}

	// `!this._dunderAllNames?.some(...)` is true both when __all__ was never
	// seen and when it does not contain the name.
	inDunderAll := func(name string) bool {
		if !b.dunderAllNamesSet {
			return false
		}
		for _, sym := range b.dunderAllNames {
			if sym == name {
				return true
			}
		}
		return false
	}

	b.potentialHiddenSymbols.ForEach(func(symbol *Symbol, name string) {
		if shouldProcess(name) && !inDunderAll(name) {
			if b.fileInfo.IsStubFile {
				symbol.SetIsExternallyHidden()
			} else {
				symbol.SetPrivatePyTypedImport()
			}
		}
	})

	// Wildcard imports are considered a re-export form, but if this module
	// defines __all__, that list determines the public interface and should
	// restrict which wildcard-imported symbols are exposed.
	if b.dunderAllNamesSet {
		b.potentialWildcardReexportSymbols.ForEach(func(symbol *Symbol, name string) {
			if shouldProcess(name) && !inDunderAll(name) {
				symbol.SetPrivatePyTypedImport()
			}
		})
	}

	// Single-underscore module-level names remain private even when __all__
	// uses an unsupported/computed form. Since every entry in
	// potentialPrivateSymbols already has an underscore prefix, shouldProcess
	// always returns true here, preserving the behavior of upstream pyright
	// test `Private3`.
	b.potentialPrivateSymbols.ForEach(func(symbol *Symbol, name string) {
		if shouldProcess(name) && !inDunderAll(name) {
			symbol.SetIsPrivateMember()
		}
	})

	if b.dunderAllNamesSet {
		SetDunderAllInfo(node, &DunderAllInfo{
			Names:                        b.dunderAllNames,
			StringNodes:                  b.dunderAllStringNodes,
			UsesUnsupportedDunderAllForm: b.usesUnsupportedDunderAllForm,
		})
	} else {
		SetDunderAllInfo(node, nil)
	}

	// Set __all__ flags on the module symbols.
	scope := GetScope(node)
	if scope != nil && b.dunderAllNamesSet {
		for _, name := range b.dunderAllNames {
			if symbol, ok := scope.SymbolTable.Get(name); ok {
				symbol.SetIsInDunderAll()
			}
		}
	}
}

// VisitModule corresponds to visitModule.
func (b *Binder) VisitModule(node *parser.ModuleNode) bool {
	// Tree walking should start with the children of the node, so we should
	// never get here.
	fail("We should never get here")
	return false
}

// VisitSuite corresponds to visitSuite.
func (b *Binder) VisitSuite(node *parser.SuiteNode) bool {
	b.walkStatementsAndReportUnreachable(node.D.Statements)
	return false
}

// isNonEmptyListOrTupleLiteral corresponds to _isNonEmptyListOrTupleLiteral.
func (b *Binder) isNonEmptyListOrTupleLiteral(expr parser.ExpressionNode) bool {
	switch typed := expr.(type) {
	case *parser.ListNode:
		if len(typed.D.Items) == 0 {
			return false
		}
		for _, item := range typed.D.Items {
			nodeType := item.GetNodeType()
			if nodeType == parser.ParseNodeTypeUnpack || nodeType == parser.ParseNodeTypeComprehension {
				return false
			}
		}
		return true

	case *parser.TupleNode:
		if len(typed.D.Items) == 0 {
			return false
		}
		for _, item := range typed.D.Items {
			if item.GetNodeType() == parser.ParseNodeTypeUnpack {
				return false
			}
		}
		return true
	}

	return false
}

// addTypingImportAliasesFromBuiltinsScope corresponds to
// _addTypingImportAliasesFromBuiltinsScope.
func (b *Binder) addTypingImportAliasesFromBuiltinsScope() {
	if b.fileInfo.BuiltinsScope == nil {
		return
	}

	b.fileInfo.BuiltinsScope.SymbolTable.ForEach(func(symbol *Symbol, name string) {
		typingImportAlias := symbol.GetTypingSymbolAlias()
		if typingImportAlias != nil && !symbol.IsExternallyHidden() {
			b.typingSymbolAliases.Set(name, *typingImportAlias)
		}
	})
}

// formatModuleName corresponds to _formatModuleName.
func (b *Binder) formatModuleName(node *parser.ModuleNameNode) string {
	parts := make([]string, 0, len(node.D.NameParts))
	for _, part := range node.D.NameParts {
		parts = append(parts, part.D.Value)
	}
	return repeatString(".", node.D.LeadingDots) + joinStrings(parts, ".")
}

// getNonClassParentScope corresponds to _getNonClassParentScope.
func (b *Binder) getNonClassParentScope() *Scope {
	// We may not be able to use the current scope if it's a class scope.
	// Walk up until we find a non-class scope instead.
	parentScope := b.currentScope
	for parentScope.Type == ScopeTypeClass {
		parentScope = parentScope.Parent
	}

	return parentScope
}

// addSlotsToCurrentScope corresponds to _addSlotsToCurrentScope.
func (b *Binder) addSlotsToCurrentScope(slotNameNodes []*parser.StringListNode) {
	assert(b.currentScope.Type == ScopeTypeClass, "")

	slotsContainsDict := false

	for _, slotNameNode := range slotNameNodes {
		slotName := stringOrFormatValue(slotNameNode.D.Strings[0])

		if slotName == "__dict__" {
			slotsContainsDict = true
			continue
		}

		symbol := b.currentScope.LookUpSymbol(slotName)
		if symbol != nil {
			symbol.SetIsSlotsMember()
		} else {
			symbol = b.currentScope.AddSymbol(
				slotName,
				SymbolFlagsInitiallyUnbound|
					SymbolFlagsClassMember|
					SymbolFlagsInstanceMember|
					SymbolFlagsSlotsMember,
			)
			honorPrivateNaming := b.fileInfo.DiagnosticRuleSet.ReportPrivateUsage != DiagnosticLevelNone
			if IsPrivateOrProtectedName(slotName) && honorPrivateNaming {
				symbol.SetIsPrivateMember()
			}
		}

		symbol.AddDeclaration(&VariableDeclaration{
			DeclarationBase: DeclarationBase{
				Type:            DeclarationTypeVariable,
				Node:            slotNameNode,
				Uri:             b.fileInfo.FileUri,
				Range:           common.ConvertTextRangeToRange(slotNameNode.GetRange(), b.fileInfo.Lines),
				ModuleName:      b.fileInfo.ModuleName,
				IsInExceptSuite: b.isInExceptSuite,
			},
			IsConstant:        IsConstantName(slotName),
			IsDefinedBySlots:  true,
			IsExplicitBinding: b.currentScope.GetBindingType(slotName) != NameBindingTypeNone,
		})
	}

	if !slotsContainsDict {
		names := make([]string, 0, len(slotNameNodes))
		for _, node := range slotNameNodes {
			names = append(names, stringOrFormatValue(node.D.Strings[0]))
		}
		b.currentScope.SetSlotsNames(names)
	}
}

// isInComprehension corresponds to _isInComprehension. The TypeScript defaults
// ignoreOutermostIterable to false.
func (b *Binder) isInComprehension(node parser.ParseNode, ignoreOutermostIterable bool) bool {
	var curNode parser.ParseNode = node
	var prevNode parser.ParseNode
	var prevPrevNode parser.ParseNode

	for curNode != nil {
		if comprehension, ok := curNode.(*parser.ComprehensionNode); ok {
			if ignoreOutermostIterable && len(comprehension.D.ForIfNodes) > 0 {
				if outermostCompr, ok := comprehension.D.ForIfNodes[0].(*parser.ComprehensionForNode); ok {
					// Unlike the walks in parseTreeUtils, prevNode is never
					// undefined here: the Comprehension case cannot be reached
					// on the first iteration unless node is itself the
					// comprehension, in which case forIfNodes[0] is not nil and
					// the comparison is false.
					if prevNode == parser.ParseNode(outermostCompr) {
						if prevPrevNode == childOrNil(outermostCompr.D.IterableExpr) {
							return false
						}
					}
				}
			}

			return true
		}

		prevPrevNode = prevNode
		prevNode = curNode
		curNode = curNode.NodeBase().Parent
	}
	return false
}

// addPatternCaptureTarget corresponds to _addPatternCaptureTarget.
func (b *Binder) addPatternCaptureTarget(target *parser.NameNode) {
	symbol := b.bindNameToScope(b.currentScope, target, nil)
	b.createAssignmentTargetFlowNodes(target, false /* walkTargets */, false /* unbound */)

	// See if the target overwrites all or a portion of the subject expression.
	if b.currentMatchSubjExpr != nil {
		if IsMatchingExpression(target, b.currentMatchSubjExpr, nil) ||
			IsPartialMatchingExpression(target, b.currentMatchSubjExpr) {
			b.currentMatchSubjExpr = nil
		}
	}

	if symbol != nil {
		symbol.AddDeclaration(&VariableDeclaration{
			DeclarationBase: DeclarationBase{
				Type:            DeclarationTypeVariable,
				Node:            target,
				Uri:             b.fileInfo.FileUri,
				Range:           common.ConvertTextRangeToRange(target.GetRange(), b.fileInfo.Lines),
				ModuleName:      b.fileInfo.ModuleName,
				IsInExceptSuite: b.isInExceptSuite,
			},
			IsConstant:         IsConstantName(target.D.Value),
			InferredTypeSource: target.NodeBase().Parent,
			IsExplicitBinding:  b.currentScope.GetBindingType(target.D.Value) != NameBindingTypeNone,
		})
	}
}

// useExceptTargets corresponds to _useExceptTargets.
func (b *Binder) useExceptTargets(targets []*FlowLabel, callback func()) {
	prevExceptTargets := b.currentExceptTargets
	b.currentExceptTargets = targets
	callback()
	b.currentExceptTargets = prevExceptTargets
}

// walkStatementsAndReportUnreachable corresponds to
// _walkStatementsAndReportUnreachable. The original returns false and no caller
// reads it.
func (b *Binder) walkStatementsAndReportUnreachable(statements []parser.StatementNode) {
	foundUnreachableStatement := false

	for _, statement := range statements {
		SetFlowNode(statement, b.currentFlowNode)

		if !foundUnreachableStatement {
			foundUnreachableStatement = b.isCodeUnreachable()
		}

		if !foundUnreachableStatement {
			b.Walk(statement)
		} else {
			// If we're within a function, we need to look for unreachable yield
			// statements because they affect the behavior of the function
			// (making it a generator) even if they're never executed.
			if b.targetFunctionDeclaration != nil && !b.targetFunctionDeclaration.IsGenerator {
				yieldFinder := NewYieldFinder()
				if yieldFinder.CheckContainsYield(statement) {
					b.targetFunctionDeclaration.IsGenerator = true
				}
			}

			// In case there are any class or function statements within this
			// subtree, we need to create dummy scopes for them. The type
			// analyzer depends on scopes being present.
			if !b.moduleSymbolOnly {
				dummyScopeGenerator := NewDummyScopeGenerator(b.currentScope)
				dummyScopeGenerator.Walk(statement)
			}
		}
	}
}

// deferBinding corresponds to _deferBinding.
func (b *Binder) deferBinding(callback func()) {
	// Defer the binding task.
	b.deferredBindingTasks = append(b.deferredBindingTasks, &deferredBindingTask{
		Scope:               b.currentScope,
		CodeFlowExpressions: b.currentScopeCodeFlowExpressions,
		Callback:            callback,
	})
}

// bindDeferred corresponds to _bindDeferred.
func (b *Binder) bindDeferred() {
	// The queue grows while it is drained -- a deferred function body can defer
	// a nested one -- so this pops from the front rather than ranging.
	for len(b.deferredBindingTasks) > 0 {
		nextItem := b.deferredBindingTasks[0]
		b.deferredBindingTasks = b.deferredBindingTasks[1:]

		// Reset the state
		b.currentScope = nextItem.Scope
		b.currentScopeCodeFlowExpressions = nextItem.CodeFlowExpressions

		nextItem.Callback()
	}
}

// getUniqueFlowNodeID corresponds to _getUniqueFlowNodeId, which bumps the
// complexity estimate as a side effect of allocating an id.
func (b *Binder) getUniqueFlowNodeID() int {
	b.codeFlowComplexity += flowNodeComplexityContribution

	return GetUniqueFlowNodeID()
}

// addDiagnostic corresponds to _addDiagnostic. It returns nil where the
// TypeScript returns undefined.
func (b *Binder) addDiagnostic(
	rule DiagnosticRule,
	message string,
	textRange common.TextRange,
) *common.Diagnostic {
	// `this._fileInfo.diagnosticRuleSet[rule] as DiagnosticLevel` -- the cast
	// is unchecked in the original, and every caller passes a diag-level rule.
	diagLevel := *diagnosticRuleLevelFields[rule](b.fileInfo.DiagnosticRuleSet)

	var diagnostic *common.Diagnostic
	switch diagLevel {
	case DiagnosticLevelError, DiagnosticLevelWarning, DiagnosticLevelInformation:
		diagnostic = b.fileInfo.DiagnosticSink.AddDiagnosticWithTextRange(diagLevel, message, textRange)

	case DiagnosticLevelNone:

	default:
		common.AssertNever(diagLevel, diagLevel+" is not expected")
		return nil
	}

	if diagnostic != nil {
		diagnostic.SetRule(rule)
	}

	return diagnostic
}

// addSyntaxError corresponds to _addSyntaxError.
func (b *Binder) addSyntaxError(message string, textRange common.TextRange) *common.Diagnostic {
	return b.fileInfo.DiagnosticSink.AddDiagnosticWithTextRange(DiagnosticLevelError, message, textRange)
}

// repeatString corresponds to `'x'.repeat(n)`.
func repeatString(s string, count int) string {
	if count <= 0 {
		return ""
	}
	out := ""
	for i := 0; i < count; i++ {
		out += s
	}
	return out
}

// joinStrings corresponds to `array.join(sep)`.
func joinStrings(parts []string, sep string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += sep
		}
		out += part
	}
	return out
}

// YieldFinder corresponds to the class of the same name.
type YieldFinder struct {
	*ParseTreeWalker
	containsYield bool
}

// NewYieldFinder constructs a YieldFinder.
func NewYieldFinder() *YieldFinder {
	finder := &YieldFinder{}
	finder.ParseTreeWalker = NewParseTreeWalker(finder)
	return finder
}

// CheckContainsYield corresponds to checkContainsYield.
func (f *YieldFinder) CheckContainsYield(node parser.ParseNode) bool {
	f.Walk(node)
	return f.containsYield
}

// VisitYield corresponds to visitYield.
func (f *YieldFinder) VisitYield(node *parser.YieldNode) bool {
	f.containsYield = true
	return false
}

// VisitYieldFrom corresponds to visitYieldFrom.
func (f *YieldFinder) VisitYieldFrom(node *parser.YieldFromNode) bool {
	f.containsYield = true
	return false
}

// ReturnFinder corresponds to the class of the same name.
type ReturnFinder struct {
	*ParseTreeWalker
	containsReturn bool
}

// NewReturnFinder constructs a ReturnFinder.
func NewReturnFinder() *ReturnFinder {
	finder := &ReturnFinder{}
	finder.ParseTreeWalker = NewParseTreeWalker(finder)
	return finder
}

// CheckContainsReturn corresponds to checkContainsReturn.
func (f *ReturnFinder) CheckContainsReturn(node parser.ParseNode) bool {
	f.Walk(node)
	return f.containsReturn
}

// VisitReturn corresponds to visitReturn.
func (f *ReturnFinder) VisitReturn(node *parser.ReturnNode) bool {
	f.containsReturn = true
	return false
}

// DummyScopeGenerator creates dummy scopes for classes or functions within a
// parse tree. This is needed in cases where the parse tree has been determined
// to be unreachable. There are code paths where the type evaluator will still
// evaluate these types, and it depends on the presence of a scope.
type DummyScopeGenerator struct {
	*ParseTreeWalker
	currentScope *Scope
}

// NewDummyScopeGenerator corresponds to the constructor.
func NewDummyScopeGenerator(currentScope *Scope) *DummyScopeGenerator {
	generator := &DummyScopeGenerator{currentScope: currentScope}
	generator.ParseTreeWalker = NewParseTreeWalker(generator)
	return generator
}

// VisitClass corresponds to visitClass.
func (g *DummyScopeGenerator) VisitClass(node *parser.ClassNode) bool {
	newScope := g.createNewScope(ScopeTypeClass, func() {
		g.Walk(node.D.Suite)
	})

	if GetScope(node) == nil {
		SetScope(node, newScope)
	}

	return false
}

// VisitFunction corresponds to visitFunction.
func (g *DummyScopeGenerator) VisitFunction(node *parser.FunctionNode) bool {
	newScope := g.createNewScope(ScopeTypeFunction, func() {
		g.Walk(node.D.Suite)
	})

	if GetScope(node) == nil {
		SetScope(node, newScope)
	}

	return false
}

// createNewScope corresponds to DummyScopeGenerator._createNewScope.
func (g *DummyScopeGenerator) createNewScope(scopeType ScopeType, callback func()) *Scope {
	prevScope := g.currentScope
	newScope := NewScope(scopeType, g.currentScope, nil, nil)
	g.currentScope = newScope

	callback()

	g.currentScope = prevScope
	return newScope
}
