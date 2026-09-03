/*
 * checker_overloadoverlap.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateOverloadConsistency, _validateOverloadAttributeConsistency,
 * _findNodeForOverload, _isOverlappingOverload, _validateExceptionType and
 * _validateExceptionTypeRecursive.
 *
 * Two overloads "overlap" when the earlier one accepts every argument list the
 * later one does, which makes the later one unreachable. The check is expressed
 * as an assignability question with SkipReturnTypeCheck: can the later signature
 * be assigned to the earlier one considering parameters alone? If so the earlier
 * shadows it.
 *
 * The second loop asks the reverse question with PartialOverloadOverlap and is
 * not redundant. A *partial* overlap is legal -- overloads routinely accept
 * intersecting argument sets -- but only if the return types agree on the
 * intersection. So it fires only when the return types are also incompatible.
 *
 * Both directions bind their type variables against the declaration's live
 * scopes before comparing, otherwise two generic overloads would compare as
 * unrelated free variables. The asymmetry there is deliberate and the original
 * comments it: the previous overload uses its declaration's *parent* node, so
 * that function-local type variables are not turned into bound ones.
 *
 * _findNodeForOverload exists purely for diagnostic placement, and the original's
 * comment explains the constraint: mypy reports these on the earlier overload's
 * line, and typeshed carries `type: ignore` comments positioned for that. Moving
 * the diagnostic to the later line would make those suppressions miss.
 *
 * The exception checks are unrelated but share this file because they are the
 * last small validators in checker.ts. The tuple recursion there is one level
 * deep by construction -- `except (A, B):` is legal, `except ((A, B), C):` is
 * not -- which is why allowTuple is passed false on the recursive call.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateOverloadConsistency corresponds to _validateOverloadConsistency. The
// original's comment: validates that overloads do not overlap with inconsistent
// return results.
func (c *Checker) validateOverloadConsistency(
	node *parser.FunctionNode, functionType *FunctionType, prevOverloads []*FunctionType,
) {
	// The original's comment: skip the check entirely if it's disabled.
	if c.fileInfo.DiagnosticRuleSet.ReportOverlappingOverload == DiagnosticLevelNone {
		return
	}

	for i, prevOverload := range prevOverloads {
		if c.isOverlappingOverload(functionType, prevOverload, false) {
			c.evaluator.AddDiagnostic(
				DiagnosticRuleReportOverlappingOverload,
				localization.LocMessage.OverlappingOverload().Format(
					node.D.Name.D.Value, len(prevOverloads)+1, i+1),
				node.D.Name,
				nil,
			)
			break
		}
	}

	for i, prevOverload := range prevOverloads {
		if !c.isOverlappingOverload(prevOverload, functionType, true) {
			continue
		}

		prevReturnType := FunctionTypeGetEffectiveReturnType(prevOverload, true)
		returnType := FunctionTypeGetEffectiveReturnType(functionType, true)

		if IsNilType(prevReturnType) || IsNilType(returnType) {
			continue
		}

		if c.evaluator.AssignType(returnType, prevReturnType, nil, nil, AssignTypeFlagsDefault, 0) {
			continue
		}

		// See the file header: the diagnostic goes on the *earlier* overload's
		// line when it can be located, to line up with typeshed's suppressions.
		errorNode := node.D.Name
		if altNode := c.findNodeForOverload(node, prevOverload); altNode != nil {
			errorNode = altNode.D.Name
		}

		c.evaluator.AddDiagnostic(
			DiagnosticRuleReportOverlappingOverload,
			localization.LocMessage.OverloadReturnTypeMismatch().Format(
				node.D.Name.D.Value, len(prevOverloads)+1, i+1),
			errorNode,
			nil,
		)
		break
	}
}

// findNodeForOverload corresponds to _findNodeForOverload.
func (c *Checker) findNodeForOverload(
	functionNode *parser.FunctionNode, overloadType *FunctionType,
) *parser.FunctionNode {
	declInfo := c.evaluator.GetDeclInfoForNameNode(functionNode.D.Name, nil)
	if declInfo == nil || declInfo.Decls == nil {
		return nil
	}

	for _, decl := range declInfo.Decls {
		funcDecl, ok := decl.(*FunctionDeclaration)
		if !ok {
			continue
		}
		declNode, ok := funcDecl.Node.(*parser.FunctionNode)
		if !ok {
			continue
		}
		// The original compares with `===`: the same FunctionType object, not an
		// equivalent one.
		if functionType := c.evaluator.GetTypeOfFunction(declNode); functionType != nil &&
			functionType.FunctionType == overloadType {
			return declNode
		}
	}

	return nil
}

// isOverlappingOverload corresponds to _isOverlappingOverload.
func (c *Checker) isOverlappingOverload(
	functionType *FunctionType, prevOverload *FunctionType, partialOverlap bool,
) bool {
	// The original's comment: according to precedent, the __get__ method is
	// special-cased and is exempt from overlapping overload checks. It's not clear
	// why this is the case, but for consistency with other type checkers, we'll
	// honor this rule. See
	// https://github.com/python/typing/issues/253#issuecomment-389262904 for
	// details.
	if FunctionTypeIsInstanceMethod(functionType) && functionType.Shared.Name == "__get__" {
		return false
	}

	flags := AssignTypeFlagsSkipReturnTypeCheck |
		AssignTypeFlagsOverloadOverlap |
		AssignTypeFlagsDisallowExtraKwargsForTd
	if partialOverlap {
		flags |= AssignTypeFlagsPartialOverloadOverlap
	}

	if functionType.Shared.Declaration != nil {
		if functionNode := functionType.Shared.Declaration.DeclBase().Node; functionNode != nil {
			liveTypeVars := GetTypeVarScopesForNode(functionNode)
			if bound, ok := MakeTypeVarsBound(functionType, liveTypeVars, true).(*FunctionType); ok {
				functionType = bound
			}
		}
	}

	// The original's comment: use the parent node of the declaration in this case
	// so we don't transform function-local type variables into bound type
	// variables.
	if prevOverload.Shared.Declaration != nil {
		if declNode := prevOverload.Shared.Declaration.DeclBase().Node; declNode != nil {
			if prevOverloadNode := declNode.NodeBase().Parent; prevOverloadNode != nil {
				liveTypeVars := GetTypeVarScopesForNode(prevOverloadNode)
				if bound, ok := MakeTypeVarsBound(prevOverload, liveTypeVars, true).(*FunctionType); ok {
					prevOverload = bound
				}
			}
		}
	}

	return c.evaluator.AssignType(functionType, prevOverload, nil, nil, flags, 0)
}

// validateOverloadAttributeConsistency corresponds to
// _validateOverloadAttributeConsistency: every overload of a method must agree
// on whether it is static or a classmethod, since the runtime applies the
// decorator to the whole group.
func (c *Checker) validateOverloadAttributeConsistency(
	node *parser.FunctionNode, functionType *OverloadedType,
) {
	// The original's comment: don't bother with the check if it's suppressed.
	if c.fileInfo.DiagnosticRuleSet.ReportInconsistentOverload == DiagnosticLevelNone {
		return
	}

	staticMethodCount := 0
	classMethodCount := 0

	overloads := OverloadedTypeGetOverloads(functionType)
	if len(overloads) == 0 {
		return
	}
	totalMethods := len(overloads)

	for _, overload := range overloads {
		if FunctionTypeIsStaticMethod(overload) {
			staticMethodCount++
		}
		if FunctionTypeIsClassMethod(overload) {
			classMethodCount++
		}
	}

	if impl := OverloadedTypeGetImplementation(functionType); impl != nil && IsFunction(impl) {
		implFn := impl.(*FunctionType)
		totalMethods++
		if FunctionTypeIsStaticMethod(implFn) {
			staticMethodCount++
		}
		if FunctionTypeIsClassMethod(implFn) {
			classMethodCount++
		}
	}

	// A count of zero means no overload carries the decorator, which is
	// consistent; a count equal to the total means all of them do. Only a value
	// strictly between the two is a mismatch.
	if staticMethodCount > 0 && staticMethodCount < totalMethods {
		c.evaluator.AddDiagnostic(
			DiagnosticRuleReportInconsistentOverload,
			localization.LocMessage.OverloadStaticMethodInconsistent().Format(node.D.Name.D.Value),
			firstOverloadNameNode(overloads, node),
			nil,
		)
	}

	if classMethodCount > 0 && classMethodCount < totalMethods {
		c.evaluator.AddDiagnostic(
			DiagnosticRuleReportInconsistentOverload,
			localization.LocMessage.OverloadClassMethodInconsistent().Format(node.D.Name.D.Value),
			firstOverloadNameNode(overloads, node),
			nil,
		)
	}
}

// firstOverloadNameNode is the original's
// `overloads[0]?.shared.declaration?.node.d.name ?? node.d.name`.
func firstOverloadNameNode(
	overloads []*FunctionType, node *parser.FunctionNode,
) parser.ExpressionNode {
	if len(overloads) > 0 && overloads[0].Shared.Declaration != nil {
		if declNode, ok := overloads[0].Shared.Declaration.DeclBase().Node.(*parser.FunctionNode); ok {
			return declNode.D.Name
		}
	}
	return node.D.Name
}

// validateExceptionType corresponds to _validateExceptionType.
func (c *Checker) validateExceptionType(
	exceptionType Type, errorNode parser.ExpressionNode, isExceptGroup bool,
) {
	baseExceptionType := c.evaluator.GetBuiltInType(errorNode, "BaseException")
	baseExceptionGroupType := c.evaluator.GetBuiltInType(errorNode, "BaseExceptionGroup")
	diagAddendum := common.NewDiagnosticAddendum()

	c.validateExceptionTypeRecursive(
		exceptionType, diagAddendum, baseExceptionType, baseExceptionGroupType, true, isExceptGroup)

	if !diagAddendum.IsEmpty() {
		c.evaluator.AddDiagnostic(
			DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.ExceptionTypeNotClass().Format(
				c.evaluator.PrintType(exceptionType, nil)),
			errorNode,
			nil,
		)
	}
}

// validateExceptionTypeRecursive corresponds to _validateExceptionTypeRecursive.
func (c *Checker) validateExceptionTypeRecursive(
	exceptionType Type,
	diag *common.DiagnosticAddendum,
	baseExceptionType Type,
	baseExceptionGroupType Type,
	allowTuple bool,
	isExceptGroup bool,
) {
	// When the builtin type cannot be resolved the check answers "yes" rather
	// than reporting: a missing typeshed is not the user's error.
	derivesFromBaseException := func(classType *ClassType) bool {
		if IsNilType(baseExceptionType) || !IsInstantiableClass(baseExceptionType) {
			return true
		}
		return DerivesFromClassRecursive(classType, baseExceptionType.(*ClassType), false)
	}

	derivesFromBaseExceptionGroup := func(classType *ClassType) bool {
		if IsNilType(baseExceptionGroupType) || !IsInstantiableClass(baseExceptionGroupType) {
			return true
		}
		return DerivesFromClassRecursive(classType, baseExceptionGroupType.(*ClassType), false)
	}

	DoForEachSubtype(exceptionType, func(exceptionSubtype Type, _ int, _ []Type) {
		if IsAnyOrUnknown(exceptionSubtype) {
			return
		}

		if !IsClass(exceptionSubtype) {
			return
		}
		cls := exceptionSubtype.(*ClassType)

		if exceptionSubtype.Base().IsInstantiable() {
			if !derivesFromBaseException(cls) {
				diag.AddMessage(localization.LocMessage.ExceptionTypeIncorrect().Format(
					c.evaluator.PrintType(exceptionSubtype, nil)))
			}

			// `except*` is the inverse condition: an ExceptionGroup may not
			// itself be caught by an except* clause.
			if isExceptGroup && derivesFromBaseExceptionGroup(cls) {
				diag.AddMessage(localization.LocMessage.ExceptionGroupTypeIncorrect())
			}
			return
		}

		if allowTuple && cls.Priv.TupleTypeArgs != nil {
			for _, typeArg := range cls.Priv.TupleTypeArgs {
				c.validateExceptionTypeRecursive(
					typeArg.Type, diag, baseExceptionType, baseExceptionGroupType, false, isExceptGroup)
			}
			return
		}

		diag.AddMessage(localization.LocMessage.ExceptionTypeIncorrect().Format(
			c.evaluator.PrintType(exceptionSubtype, nil)))
	})
}
