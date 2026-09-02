/*
 * typeevaluator_name.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfName.
 *
 * This joins the two halves that landed separately -- lookUpSymbolRecursive
 * finds the symbol, getEffectiveTypeOfSymbolForUsage decides where its type
 * comes from -- and then applies the dozen adjustments a name's type goes
 * through on the way out: code flow narrowing, missing type arguments, the
 * type-expression checks, ParamSpec and TypeVarTuple context rules, special
 * forms, and the TypeForm annotation.
 *
 * Those adjustments are the reason this is worth porting before the things it
 * still depends on. Each is a separate piece of the original, and each now
 * records itself, so the frontier ranks them instead of hiding them behind one
 * `getTypeOfName` entry.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

func (e *typeEvaluator) getTypeOfName(node *parser.NameNode, flags EvalFlags) *TypeResult {
	fileInfo := GetFileInfo(node)
	name := node.D.Value
	var symbol *Symbol
	var t Type
	isIncomplete := false
	allowForwardReferences := (flags&EvalFlagsForwardRefs) != 0 || fileInfo.IsStubFile

	// The original's comment: look for the scope that contains the value
	// definition and see if it has a declared type.
	preferGlobalScope := allowForwardReferences && (flags&EvalFlagsTypeExpression) != 0
	symbolWithScope := e.lookUpSymbolRecursive(node, name, !allowForwardReferences, preferGlobalScope)

	if symbolWithScope == nil {
		// The original's comment: if the node is part of a "from X import Y as
		// Z" statement and the node is the "Y" (non-aliased) name, we need to
		// look up the alias symbol since the non-aliased name is not in the
		// symbol table.
		if alias := e.getAliasFromImport(node); alias != nil {
			symbolWithScope = e.lookUpSymbolRecursive(alias, alias.D.Value, !allowForwardReferences, preferGlobalScope)
		}
	}

	if symbolWithScope != nil {
		useCodeFlowAnalysis := !allowForwardReferences

		// The original's comment: if the symbol is implicitly imported from the
		// builtin scope, there's no need to use code flow analysis.
		if symbolWithScope.Scope.Type == ScopeTypeBuiltin {
			useCodeFlowAnalysis = false
		}

		symbol = symbolWithScope.Symbol
		e.setSymbolAccessed(fileInfo, symbol, node)

		// The original's comment: if we're not supposed to be analyzing this
		// function, skip the remaining work to determine the name's type.
		// Simply evaluate its type as Any.
		if !fileInfo.DiagnosticRuleSet.AnalyzeUnannotatedFunctions {
			if containingFunction := GetEnclosingFunction(node); containingFunction != nil {
				if IsUnannotatedFunction(containingFunction) {
					return &TypeResult{Type: AnyTypeCreate(false), IsIncomplete: false}
				}
			}
		}

		// The original's comment: get the effective type (either the declared
		// type or the inferred type). If we're using code flow analysis, pass
		// the usage node so we consider only the assignment nodes that are
		// reachable from this usage.
		var usageNode *parser.NameNode
		if useCodeFlowAnalysis {
			usageNode = node
		}
		effectiveTypeInfo := e.GetEffectiveTypeOfSymbolForUsage(symbol, usageNode, false)
		effectiveType := TransformPossibleRecursiveTypeAlias(effectiveTypeInfo.Type, 0)

		if effectiveTypeInfo.IsIncomplete {
			if IsUnbound(effectiveType) {
				effectiveType = UnknownTypeCreate(true)
			}
			isIncomplete = true
		}

		if effectiveTypeInfo.IsRecursiveDefinition && e.IsNodeReachable(node, nil) {
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.RecursiveDefinition().Format(name),
				node,
				nil,
			)
		}

		isSpecialBuiltIn := effectiveType != nil && IsInstantiableClass(effectiveType) &&
			ClassTypeIsSpecialBuiltIn(effectiveType.(*ClassType))

		t = effectiveType
		if useCodeFlowAnalysis && !isSpecialBuiltIn {
			t, isIncomplete = e.narrowNameByCodeFlow(node, flags, symbol, symbolWithScope, effectiveType, t, isIncomplete)
		}

		// The original's comment: detect, report, and fill in missing type
		// arguments if appropriate.
		t = e.ReportMissingTypeArgs(node, t, flags)

		// The original's comment: report inappropriate use of variables in type
		// expressions.
		if (flags & EvalFlagsTypeExpression) != 0 {
			t = e.validateSymbolIsTypeExpression(node, t, effectiveTypeInfo.IncludesVariableDecl)
		}

		if IsTypeVar(t) && !t.(*TypeVarType).Shared.IsSynthesized {
			t = e.validateTypeVarUsage(node, t, flags)
		}

		// Add TypeForm details if appropriate.
		t = e.addTypeFormForSymbol(node, t, flags, effectiveTypeInfo.IncludesVariableDecl)
	} else {
		// The original's comment: handle the special case of "reveal_type" and
		// "reveal_locals".
		if name == "reveal_type" || name == "reveal_locals" {
			t = AnyTypeCreate(false)
		} else {
			e.AddDiagnostic(
				DiagnosticRuleReportUndefinedVariable,
				localization.LocMessage.SymbolIsUndefined().Format(name),
				node,
				nil,
			)

			t = UnknownTypeCreate(false)
		}
	}

	if IsParamSpec(t) && t.(*TypeVarType).Priv.ScopeID != "" {
		if flags&EvalFlagsNoParamSpec != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.ParamSpecContext(), node, nil)
			t = UnknownTypeCreate(false)
		}
	}

	if IsTypeVarTuple(t) && (flags&EvalFlagsNoTypeVarTuple) != 0 && !t.(*TypeVarType).Priv.IsInUnion {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.TypeVarTupleContext(), node, nil)
		t = UnknownTypeCreate(false)
	}

	// The original's comment: if we're expecting a type expression and got a
	// sentinel literal instance, treat it as its instantiable counterpart. This
	// is similar to how None is treated in a type expression context.
	if (flags&EvalFlagsInstantiableType) != 0 && IsClassInstance(t) && e.isSentinelLiteral(t) {
		t = ClassTypeCloneAsInstantiable(t.(*ClassType), false)
	}

	t = e.convertSpecialFormToRuntimeValue(t, flags)

	if (flags & EvalFlagsTypeExpression) == 0 {
		e.reportUseOfTypeCheckOnly(t, node)
	}

	if (flags & (EvalFlagsInstantiableType | EvalFlagsTypeFormArg)) != 0 {
		if (flags & EvalFlagsAllowGeneric) == 0 {
			if IsInstantiableClass(t) && ClassTypeIsBuiltInNamed(t.(*ClassType), "Generic") {
				e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, localization.LocMessage.GenericNotAllowed(), node, nil)
				if (flags & EvalFlagsTypeFormArg) != 0 {
					t = UnknownTypeCreate(false)
				}
			}
		}
	}

	return &TypeResult{Type: t, IsIncomplete: isIncomplete}
}

// narrowNameByCodeFlow is the original's `if (useCodeFlowAnalysis && !isSpecialBuiltIn)`
// block, lifted out because it declares four locals the surrounding function
// does not otherwise need.
func (e *typeEvaluator) narrowNameByCodeFlow(
	node *parser.NameNode,
	flags EvalFlags,
	symbol *Symbol,
	symbolWithScope *SymbolWithScope,
	effectiveType Type,
	t Type,
	isIncomplete bool,
) (Type, bool) {
	// The original's comment: see if code flow analysis can tell us anything
	// more about the type. If the symbol is declared outside of our execution
	// scope, use its effective type. If it's declared inside our execution
	// scope, it generally starts as unbound at the start of the code flow.
	typeAtStart := effectiveType
	isTypeAtStartIncomplete := false

	if !symbolWithScope.IsBeyondExecutionScope && symbol.IsInitiallyUnbound() {
		typeAtStart = UnboundTypeCreate()

		// The original's comment: is this a module-level scope? If so, see if
		// it's an alias of a builtin.
		if symbolWithScope.Scope.Type == ScopeTypeModule && symbolWithScope.Scope.Parent != nil {
			if builtInSymbol := symbolWithScope.Scope.Parent.LookUpSymbol(node.D.Value); builtInSymbol != nil {
				typeAtStart = e.GetEffectiveTypeOfSymbolForUsage(builtInSymbol, nil, false).Type
			}
		}
	}

	if symbolWithScope.IsBeyondExecutionScope {
		outerScopeTypeResult := e.getCodeFlowTypeForCapturedVariable(node, symbolWithScope, effectiveType)
		if outerScopeTypeResult != nil && outerScopeTypeResult.Type != nil {
			t = outerScopeTypeResult.Type
			typeAtStart = t
			isTypeAtStartIncomplete = outerScopeTypeResult.IsIncomplete
		}
	}

	codeFlowTypeResult := e.getFlowTypeOfReference(node, nil, &flowTypeOptions{
		TargetSymbolID:           symbol.ID,
		TypeAtStart:              &TypeResult{Type: typeAtStart, IsIncomplete: isTypeAtStartIncomplete},
		SkipConditionalNarrowing: (flags & EvalFlagsTypeExpression) != 0,
	})

	if codeFlowTypeResult.Type != nil {
		t = codeFlowTypeResult.Type
	}

	if codeFlowTypeResult.IsIncomplete {
		isIncomplete = true
	}

	return t, isIncomplete
}

// flowTypeOptions corresponds to the options object getFlowTypeOfReference
// takes.
type flowTypeOptions struct {
	TargetSymbolID           int
	TypeAtStart              *TypeResult
	SkipConditionalNarrowing bool
}

/*
 * What getTypeOfName still depends on. Each is a distinct piece of the
 * original, so each records itself; the point is the ranking among them.
 */

// getAliasFromImport corresponds to the function of the same name.
func (e *typeEvaluator) getAliasFromImport(_ *parser.NameNode) *parser.NameNode {
	e.unported("getAliasFromImport")
	return nil
}

// getCodeFlowTypeForCapturedVariable corresponds to the function of the same
// name.
func (e *typeEvaluator) getCodeFlowTypeForCapturedVariable(
	_ *parser.NameNode,
	_ *SymbolWithScope,
	_ Type,
) *TypeResult {
	e.unported("getCodeFlowTypeForCapturedVariable")
	return nil
}
