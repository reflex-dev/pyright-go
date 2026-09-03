/*
 * typeevaluator_diagnostics.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * addDiagnostic, addDiagnosticForTextRange, addInformation, addUnreachableCode,
 * addDeprecated, and the two suppression predicates beneath them.
 *
 * This is the evaluator's only route to the diagnostic sink, and it was a stub.
 * That is why the evaluator gate had not moved: the gate asserts on six
 * diagnostic lists, so with addDiagnostic doing nothing, no conclusion the
 * evaluator reaches can reach a test, and every test expecting at least one
 * diagnostic fails no matter how correct the type is.
 *
 * The one shape change is the rule-to-level lookup. The original writes
 * `fileInfo.diagnosticRuleSet[rule]`, indexing a TypeScript interface by a
 * string that happens to be a field name. Go cannot index a struct, so the
 * mapping from rule name to field is built once by reflection over the
 * generated rule set -- the same trick configbridge.ts uses in reverse -- and
 * cached, so the per-diagnostic cost is a map lookup rather than a walk.
 */

package analyzer

import (
	"reflect"
	"strings"
	"sync"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// AddInformation corresponds to addInformation.
func (e *typeEvaluator) AddInformation(message string, node parser.ParseNode, textRange *common.TextRange) *common.Diagnostic {
	return e.addDiagnosticWithSuppressionCheck(DiagnosticLevelInformation, message, node, textRange)
}

// AddUnreachableCode corresponds to addUnreachableCode.
func (e *typeEvaluator) AddUnreachableCode(node parser.ParseNode, reachability Reachability, textRange common.TextRange) {
	if reachability == ReachabilityReachable {
		return
	}

	if e.isDiagnosticSuppressedForNode(node) {
		return
	}

	fileInfo := GetFileInfo(node)
	reportTypeReachability := fileInfo.DiagnosticRuleSet.EnableReachabilityAnalysis

	if reachability != ReachabilityUnreachableStructural &&
		reachability != ReachabilityUnreachableStaticCondition &&
		!reportTypeReachability {
		return
	}

	var message string
	switch reachability {
	case ReachabilityUnreachableStructural:
		message = localization.LocMessage.UnreachableCodeStructure()
	case ReachabilityUnreachableStaticCondition:
		message = localization.LocMessage.UnreachableCodeCondition()
	default:
		message = localization.LocMessage.UnreachableCodeType()
	}

	fileInfo.DiagnosticSink.AddUnreachableCodeWithTextRange(message, textRange, nil)
}

// AddDeprecated corresponds to addDeprecated.
func (e *typeEvaluator) AddDeprecated(message string, node parser.ParseNode) {
	if e.isDiagnosticSuppressedForNode(node) {
		return
	}

	fileInfo := GetFileInfo(node)
	fileInfo.DiagnosticSink.AddDeprecatedWithTextRange(message, node.NodeBase().TextRange, nil)
}

// addDiagnosticWithSuppressionCheck corresponds to the function of the same
// name. It returns nil where the original returns undefined.
func (e *typeEvaluator) addDiagnosticWithSuppressionCheck(
	diagLevel DiagnosticLevel,
	message string,
	node parser.ParseNode,
	textRange *common.TextRange,
) *common.Diagnostic {
	if e.isDiagnosticSuppressedForNode(node) {
		// The original's comment: see if this node is suppressed but the
		// diagnostic should be generated anyway so it can be used by the caller
		// that requested the suppression.
		for _, suppressedNode := range e.suppressedNodeStack {
			if IsNodeContainedWithin(node, suppressedNode.Node) && suppressedNode.HasSuppressed {
				suppressedNode.SuppressedDiags = append(suppressedNode.SuppressedDiags, message)
				break
			}
		}

		return nil
	}

	if e.IsNodeReachable(node, nil) {
		fileInfo := GetFileInfo(node)
		effectiveRange := node.NodeBase().TextRange
		if textRange != nil {
			effectiveRange = *textRange
		}
		return fileInfo.DiagnosticSink.AddDiagnosticWithTextRange(diagLevel, message, effectiveRange)
	}

	return nil
}

// isDiagnosticSuppressedForNode corresponds to the function of the same name.
func (e *typeEvaluator) isDiagnosticSuppressedForNode(node parser.ParseNode) bool {
	if e.speculativeTypeTracker.IsSpeculative(node, true) {
		return true
	}

	for _, suppressedNode := range e.suppressedNodeStack {
		if IsNodeContainedWithin(node, suppressedNode.Node) {
			return true
		}
	}

	return false
}

// canSkipDiagnosticForNode corresponds to the function of the same name. The
// original's comment: this function is similar to isDiagnosticSuppressedForNode
// except that it returns false if diagnostics are suppressed for the node but
// the caller has requested that diagnostics be generated anyway.
func (e *typeEvaluator) canSkipDiagnosticForNode(node parser.ParseNode) bool {
	if e.speculativeTypeTracker.IsSpeculative(node, true) {
		return true
	}

	found := false
	for _, suppressedNode := range e.suppressedNodeStack {
		if !IsNodeContainedWithin(node, suppressedNode.Node) {
			continue
		}
		found = true
		if suppressedNode.HasSuppressed {
			return false
		}
	}

	return found
}

// AddDiagnostic corresponds to addDiagnostic.
func (e *typeEvaluator) AddDiagnostic(
	rule DiagnosticRule,
	message string,
	node parser.ParseNode,
	textRange *common.TextRange,
) *common.Diagnostic {
	fileInfo := GetFileInfo(node)
	diagLevel := diagnosticLevelForRule(fileInfo.DiagnosticRuleSet, rule)

	if diagLevel == DiagnosticLevelNone {
		return nil
	}

	if containingFunction := GetEnclosingFunction(node); containingFunction != nil {
		// The original's comment: should we suppress this diagnostic because
		// it's within an unannotated function?
		//
		// The original re-reads fileInfo here from the same node; the value is
		// identical, so the outer binding is reused.
		if !fileInfo.DiagnosticRuleSet.AnalyzeUnannotatedFunctions {
			// The original's comment: is the target node within the body of the
			// function? If so, suppress the diagnostic.
			if IsUnannotatedFunction(containingFunction) &&
				IsNodeContainedWithin(node, containingFunction.D.Suite) {
				return nil
			}
		}

		// The original's comment: should we suppress this diagnostic because
		// it's within a no_type_check function?
		containingClassNode := GetEnclosingClass(containingFunction, true)
		functionInfo := e.getFunctionInfoFromDecorators(containingFunction, containingClassNode != nil)

		if (functionInfo.Flags & FunctionTypeFlagsNoTypeCheck) != 0 {
			return nil
		}
	}

	diagnostic := e.addDiagnosticWithSuppressionCheck(diagLevel, message, node, textRange)
	if diagnostic != nil {
		diagnostic.SetRule(rule)
	}

	return diagnostic
}

// AddDiagnosticForTextRange corresponds to addDiagnosticForTextRange.
func (e *typeEvaluator) AddDiagnosticForTextRange(
	fileInfo *AnalyzerFileInfo,
	rule DiagnosticRule,
	message string,
	textRange common.TextRange,
) *common.Diagnostic {
	diagLevel := diagnosticLevelForRule(fileInfo.DiagnosticRuleSet, rule)

	if diagLevel == DiagnosticLevelNone {
		return nil
	}

	diagnostic := fileInfo.DiagnosticSink.AddDiagnosticWithTextRange(diagLevel, message, textRange)
	if rule != "" {
		diagnostic.SetRule(rule)
	}

	return diagnostic
}

/*
 * The rule-to-level lookup.
 */

var (
	diagnosticRuleFieldOnce  sync.Once
	diagnosticRuleFieldIndex map[DiagnosticRule]int
)

// diagnosticLevelForRule is the original's `diagnosticRuleSet[rule]`. A rule
// whose field is not a DiagnosticLevel -- the rule set also carries plain
// booleans, which are not addressable by a DiagnosticRule -- answers "none",
// which is the original's behaviour for a level it does not recognize.
func diagnosticLevelForRule(ruleSet *DiagnosticRuleSet, rule DiagnosticRule) DiagnosticLevel {
	if ruleSet == nil {
		return DiagnosticLevelNone
	}

	diagnosticRuleFieldOnce.Do(func() {
		diagnosticRuleFieldIndex = map[DiagnosticRule]int{}
		typ := reflect.TypeOf(DiagnosticRuleSet{})
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.Type.Kind() != reflect.String {
				continue
			}
			name := strings.ToLower(field.Name[:1]) + field.Name[1:]
			diagnosticRuleFieldIndex[name] = i
		}
	})

	index, ok := diagnosticRuleFieldIndex[rule]
	if !ok {
		return DiagnosticLevelNone
	}

	return reflect.ValueOf(*ruleSet).Field(index).String()
}

/*
 * The decorators.ts types and wrapper this layer reaches.
 */

// FunctionDecoratorInfo corresponds to the interface of the same name in
// decorators.ts.
type FunctionDecoratorInfo struct {
	Flags              FunctionTypeFlags
	DeprecationMessage *string
}

// getFunctionInfoFromDecorators is the evaluator-side wrapper over the
// decorators.ts function of the same name, which takes the evaluator as its
// first argument.
func (e *typeEvaluator) getFunctionInfoFromDecorators(
	node *parser.FunctionNode,
	isInClass bool,
) *FunctionDecoratorInfo {
	return GetFunctionInfoFromDecorators(e, node, isInClass)
}
