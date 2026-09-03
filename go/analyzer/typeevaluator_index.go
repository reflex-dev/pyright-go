/*
 * typeevaluator_index.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfIndex, plus the nonSubscriptableBuiltinTypes table it consults.
 *
 * An index expression is two different things wearing the same syntax --
 * `x[0]` is a subscript and `list[int]` is a specialization -- and this function
 * handles the parts common to both: evaluate the base, check that it is legal to
 * subscript at the configured Python version, hand off to
 * getTypeOfIndexWithBaseType, then narrow the result by code flow if the base
 * type supports it.
 *
 * The narrowing is restricted, and the original's comment says why: only
 * built-in types are known to have symmetric __getitem__ and __setitem__, so
 * only those can be narrowed by an assignment through the index. The write to
 * the type cache immediately before the code flow query is a recursion guard,
 * not a result -- it is deliberately marked incomplete.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// nonSubscriptableBuiltinTypes corresponds to the module-level constant of the
// same name: classes that only became subscriptable at runtime in a particular
// Python version.
var nonSubscriptableBuiltinTypes = map[string]common.PythonVersion{
	"asyncio.futures.Future":  common.PythonVersion3_9,
	"asyncio.tasks.Task":      common.PythonVersion3_9,
	"builtins.dict":           common.PythonVersion3_9,
	"builtins.frozenset":      common.PythonVersion3_9,
	"builtins.list":           common.PythonVersion3_9,
	"builtins._PathLike":      common.PythonVersion3_9,
	"builtins.set":            common.PythonVersion3_9,
	"builtins.tuple":          common.PythonVersion3_9,
	"collections.ChainMap":    common.PythonVersion3_9,
	"collections.Counter":     common.PythonVersion3_9,
	"collections.defaultdict": common.PythonVersion3_9,
	"collections.DefaultDict": common.PythonVersion3_9,
	"collections.deque":       common.PythonVersion3_9,
	"collections.OrderedDict": common.PythonVersion3_9,
	"queue.Queue":             common.PythonVersion3_9,
}

// getTypeOfIndex corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfIndex(node *parser.IndexNode, flags EvalFlags) *TypeResult {
	baseTypeResult := e.getTypeOfExpression(node.D.LeftExpr, flags|EvalFlagsIndexBaseDefaults, nil)

	// The original's comment: if this is meant to be a type and the base
	// expression is a string expression, emit an error because this is an
	// illegal annotation form and will generate a runtime exception.
	if (flags & EvalFlagsInstantiableType) != 0 {
		if node.D.LeftExpr.GetNodeType() == parser.ParseNodeTypeStringList {
			e.AddDiagnostic(
				DiagnosticRuleReportIndexIssue,
				localization.LocMessage.StringNotSubscriptable(),
				node.D.LeftExpr,
				nil,
			)
		}
	}

	e.checkRuntimeSubscriptable(node, baseTypeResult, flags)

	indexTypeResult := e.getTypeOfIndexWithBaseType(node, baseTypeResult, EvaluatorUsageGet(), flags)

	if IsCodeFlowSupportedForReference(node) {
		e.narrowIndexByCodeFlow(node, baseTypeResult, indexTypeResult, flags)
	}

	if baseTypeResult.IsIncomplete {
		indexTypeResult.IsIncomplete = true
	}

	return indexTypeResult
}

// checkRuntimeSubscriptable is the original's `if ((flags & EvalFlags.ForwardRefs) === 0)`
// block: the check for builtin classes that raise at runtime when subscripted on
// an older Python.
func (e *typeEvaluator) checkRuntimeSubscriptable(
	node *parser.IndexNode,
	baseTypeResult *TypeResult,
	flags EvalFlags,
) {
	if (flags & EvalFlagsForwardRefs) != 0 {
		return
	}

	// The original's comment: we can skip this check if the class is used within
	// a PEP 526 variable type annotation within a class or function. For some
	// undocumented reason, they don't result in runtime exceptions when used in
	// this manner.
	skipSubscriptCheck := (flags & EvalFlagsVarTypeAnnotation) != 0
	if skipSubscriptCheck {
		if scopeNode := GetExecutionScopeNode(node); scopeNode != nil &&
			scopeNode.GetNodeType() == parser.ParseNodeTypeModule {
			skipSubscriptCheck = false
		}
	}

	if skipSubscriptCheck {
		return
	}

	if !IsInstantiableClass(baseTypeResult.Type) {
		return
	}

	baseClass := baseTypeResult.Type.(*ClassType)
	if !ClassTypeIsBuiltIn(baseClass) || baseClass.Priv.AliasName() != nil {
		return
	}

	minPythonVersion, ok := nonSubscriptableBuiltinTypes[baseClass.Shared.FullName]
	if !ok {
		return
	}

	fileInfo := GetFileInfo(node)
	if fileInfo.ExecutionEnvironment.PythonVersion.IsLessThan(minPythonVersion) && !fileInfo.IsStubFile {
		// `type.priv.aliasName || type.shared.name` -- aliasName is nil here by
		// the guard above, so this always takes the class name; written out
		// because the guard and the message are far apart in the original.
		name := baseClass.Shared.Name
		if baseClass.Priv.AliasName() != nil && *baseClass.Priv.AliasName() != "" {
			name = *baseClass.Priv.AliasName()
		}

		e.AddDiagnostic(
			DiagnosticRuleReportIndexIssue,
			localization.LocMessage.ClassNotRuntimeSubscriptable().Format(name),
			node.D.LeftExpr,
			nil,
		)
	}
}

// narrowIndexByCodeFlow is the original's `if (isCodeFlowSupportedForReference(node))`
// block. It mutates indexTypeResult in place, as the original does.
func (e *typeEvaluator) narrowIndexByCodeFlow(
	node *parser.IndexNode,
	baseTypeResult *TypeResult,
	indexTypeResult *TypeResult,
	flags EvalFlags,
) {
	// The original's comment: we limit type narrowing for index expressions to
	// built-in types that are known to have symmetric __getitem__ and
	// __setitem__ methods (i.e. the value passed to __setitem__ is the same type
	// as the value returned by __getitem__).
	baseTypeSupportsIndexNarrowing := !IsAny(baseTypeResult.Type)
	e.MapSubtypesExpandTypeVars(baseTypeResult.Type, nil, func(subtype Type, _ Type) Type {
		if !IsClassInstance(subtype) ||
			!(ClassTypeIsBuiltIn(subtype.(*ClassType)) || ClassTypeIsTypedDictClass(subtype.(*ClassType))) {
			baseTypeSupportsIndexNarrowing = false
		}

		return nil
	})

	if !baseTypeSupportsIndexNarrowing {
		return
	}

	// The original's comment: before performing code flow analysis, update the
	// cache to prevent recursion.
	//
	// `{ ...indexTypeResult, isIncomplete: true }` -- a copy marked incomplete,
	// so the real result is not made incomplete by the guard.
	guard := *indexTypeResult
	guard.IsIncomplete = true
	e.writeTypeCache(node, &guard, &flags, nil, false)

	// The original's comment: see if we can refine the type based on code flow
	// analysis.
	codeFlowTypeResult := e.getFlowTypeOfReference(node, nil, &flowTypeOptions{
		TargetSymbolID: IndeterminateSymbolID,
		TypeAtStart: &TypeResult{
			Type:         indexTypeResult.Type,
			IsIncomplete: baseTypeResult.IsIncomplete || indexTypeResult.IsIncomplete,
		},
		SkipConditionalNarrowing: (flags & EvalFlagsTypeExpression) != 0,
	})

	if codeFlowTypeResult.Type != nil {
		indexTypeResult.Type = codeFlowTypeResult.Type
	}

	if codeFlowTypeResult.IsIncomplete {
		indexTypeResult.IsIncomplete = true
	}
}
