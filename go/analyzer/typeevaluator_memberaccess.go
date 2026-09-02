/*
 * typeevaluator_memberaccess.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfMemberAccess.
 *
 * The `a.b` shell: evaluate the base, look the member up, then narrow the result
 * by code flow. It parallels getTypeOfName, and the middle step -- the actual
 * lookup -- lives in getTypeOfMemberAccessWithBaseType.
 *
 * Three things here are not obvious from the shape.
 *
 * The cache is written with isIncomplete before code flow runs, and the original
 * says why: code flow analysis can walk back through assignments to this very
 * expression, and without an entry to find it would recurse. The entry is marked
 * incomplete so nothing treats the placeholder as an answer.
 *
 * An unbound result is retried against the base class. `self.x` inside a method
 * of a subclass is unbound at that point in the flow, but a parent class may
 * declare it; the retry supplies the declared type as the flow's starting point
 * rather than leaving it unbound.
 *
 * The reportUnknownMemberType check is skipped for a bare class or special form
 * appearing as a call argument. The original names the cases: `defaultdict(list)`
 * and `isinstance(x, (list, dict))` pass unspecialized classes deliberately, and
 * reporting their unknown type arguments would be noise.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// getTypeOfMemberAccess corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfMemberAccess(
	node *parser.MemberAccessNode, flags EvalFlags,
) *TypeResult {
	// The original's comment: compute flags specifically for evaluating the left
	// expression.
	leftExprFlags := EvalFlagsMemberAccessBaseDefaults
	leftExprFlags |= flags & (EvalFlagsTypeExpression |
		EvalFlagsVarTypeAnnotation |
		EvalFlagsForwardRefs |
		EvalFlagsNotParsed |
		EvalFlagsNoTypeVarWithScopeId |
		EvalFlagsTypeVarGetsCurScope)

	// The original's comment: handle special casing for ParamSpec "args" and
	// "kwargs" accesses.
	//
	// `P.args` must reach the ParamSpec itself rather than the special form it
	// would otherwise be converted into.
	if (flags & EvalFlagsInstantiableType) != 0 {
		memberName := node.D.Member.D.Value
		if memberName == "args" || memberName == "kwargs" {
			leftExprFlags |= EvalFlagsNoConvertSpecialForm
		}
	}

	baseTypeResult := e.getTypeOfExpression(node.D.LeftExpr, leftExprFlags, nil)

	if IsTypeAliasPlaceholder(baseTypeResult.Type) {
		return &TypeResult{Type: UnknownTypeCreate(true), IsIncomplete: true}
	}

	typeResult := e.getTypeOfMemberAccessWithBaseType(
		node, baseTypeResult, &EvaluatorUsage{Method: "get"}, flags|EvalFlagsNoSpecialize)

	if IsCodeFlowSupportedForReference(node) {
		e.narrowMemberAccessByCodeFlow(node, baseTypeResult, typeResult, flags)
	}

	if baseTypeResult.IsIncomplete {
		typeResult.IsIncomplete = true
	}

	// The original's comment: see if we need to log an "unknown member access"
	// diagnostic.
	if !e.skipMemberPartialUnknownCheck(node, typeResult) {
		e.reportPossibleUnknownAssignment(
			GetFileInfo(node).DiagnosticRuleSet.ReportUnknownMemberType,
			DiagnosticRuleReportUnknownMemberType,
			node.D.Member,
			typeResult.Type,
			node,
			false,
		)
	}

	// The original's comment: cache the type information in the member name node.
	e.writeTypeCache(node.D.Member, typeResult, &flags, nil, false)

	return typeResult
}

// narrowMemberAccessByCodeFlow is the isCodeFlowSupportedForReference block. It
// mutates typeResult in place, as the original does.
func (e *typeEvaluator) narrowMemberAccessByCodeFlow(
	node *parser.MemberAccessNode,
	baseTypeResult *TypeResult,
	typeResult *TypeResult,
	flags EvalFlags,
) {
	// The original's comment: before performing code flow analysis, update the
	// cache to prevent recursion.
	incomplete := *typeResult
	incomplete.IsIncomplete = true
	e.writeTypeCache(node, &incomplete, &flags, nil, false)
	e.writeTypeCache(node.D.Member, &incomplete, &flags, nil, false)

	// The original's comment: if the type is initially unbound, see if there's a
	// parent class that potentially initialized the value.
	typeAtStart := typeResult.Type
	isTypeAtStartIncomplete := typeResult.IsIncomplete

	if IsUnbound(typeAtStart) {
		baseType := e.MakeTopLevelTypeVarsConcrete(baseTypeResult.Type, false)

		var classMemberInfo *ClassMember
		if IsInstantiableClass(baseType) {
			classMemberInfo = LookUpClassMember(baseType.(*ClassType),
				node.D.Member.D.Value, MemberAccessFlagsSkipOriginalClass, nil)
		} else if IsClassInstance(baseType) {
			classMemberInfo = LookUpObjectMember(baseType.(*ClassType),
				node.D.Member.D.Value, MemberAccessFlagsSkipOriginalClass, nil)
		}

		if classMemberInfo != nil {
			typeAtStart = e.GetTypeOfMember(classMemberInfo)
			isTypeAtStartIncomplete = false
		}
	}

	// The original's comment: see if we can refine the type based on code flow
	// analysis.
	codeFlowTypeResult := e.getFlowTypeOfReference(node, nil, &flowTypeOptions{
		TargetSymbolID:           IndeterminateSymbolID,
		TypeAtStart:              &TypeResult{Type: typeAtStart, IsIncomplete: isTypeAtStartIncomplete},
		SkipConditionalNarrowing: (flags & EvalFlagsTypeExpression) != 0,
	})

	if codeFlowTypeResult.Type != nil {
		typeResult.Type = codeFlowTypeResult.Type
	}

	if codeFlowTypeResult.IsIncomplete {
		typeResult.IsIncomplete = true
	}

	// The original's comment: detect, report, and fill in missing type arguments
	// if appropriate.
	typeResult.Type = e.ReportMissingTypeArgs(node, typeResult.Type, flags)

	// The original's comment: add TypeForm details if appropriate.
	typeResult.Type = e.addTypeFormForSymbol(node, typeResult.Type, flags, false)
}

// skipMemberPartialUnknownCheck is the original's skipPartialUnknownCheck.
func (e *typeEvaluator) skipMemberPartialUnknownCheck(
	node *parser.MemberAccessNode, typeResult *TypeResult,
) bool {
	if typeResult.IsIncomplete {
		return true
	}

	// The original's comment: don't report an error if the type is a
	// partially-specialized class being passed as an argument. This comes up
	// frequently in cases where a type is passed as an argument (e.g.
	// "defaultdict(list)"). It can also come up in cases like
	// "isinstance(x, (list, dict))". We need to check for functions as well to
	// handle Callable.
	isBareClass := IsInstantiableClass(typeResult.Type) &&
		!typeResult.Type.(*ClassType).Priv.IncludeSubclasses
	props := typeResult.Type.Base().Props
	isSpecialForm := props != nil && props.SpecialForm != nil

	if !isBareClass && !isSpecialForm {
		return false
	}

	argNode := GetParentNodeOfType(node, parser.ParseNodeTypeArgument)
	if argNode == nil {
		return false
	}

	parent := argNode.NodeBase().Parent
	return parent != nil && parent.GetNodeType() == parser.ParseNodeTypeCall
}

// getTypeOfMemberAccessWithBaseType corresponds to the function of the same
// name: the member lookup itself, which dispatches on the base type's category
// and handles descriptors, properties, __getattr__ and metaclass access.
func (e *typeEvaluator) getTypeOfMemberAccessWithBaseType(
	_ *parser.MemberAccessNode, baseTypeResult *TypeResult, _ *EvaluatorUsage, _ EvalFlags,
) *TypeResult {
	e.unported("getTypeOfMemberAccessWithBaseType")
	return &TypeResult{Type: UnknownTypeCreate(false), IsIncomplete: baseTypeResult.IsIncomplete}
}
