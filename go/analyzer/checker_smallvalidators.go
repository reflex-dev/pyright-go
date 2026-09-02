/*
 * checker_smallvalidators.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateNonlocalTypeParam, _validateIllegalDefaultParamInitializer,
 * _validateStandardCollectionInstantiation and _reportUnusedExceptStatements.
 *
 * _reportUnusedExceptStatements is the substantial one. An `except` clause is
 * unreachable if every exception type it names is already caught by an earlier
 * clause, which is a subclass test against everything seen so far -- so the pass
 * accumulates types as it goes rather than comparing pairwise at the end.
 *
 * The sawUnknownExceptionType latch is what keeps it sound. As soon as one
 * clause names a type the checker cannot pin down -- an `Any`, or a class
 * variable whose value could be any subclass -- no later clause can be proven
 * redundant, because the unknown one might not catch what it appears to. The
 * flag is set and never cleared, and every subsequent clause is skipped.
 *
 * A clause found unreachable produces three separate outputs: the
 * reportUnusedExcept diagnostic naming the shadowing type, a reportUnreachable
 * diagnostic on the suite, and an unreachable-code range so the editor greys the
 * body out. They are independent because a user may have any of the three
 * enabled without the others.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateNonlocalTypeParam corresponds to _validateNonlocalTypeParam.
func (c *Checker) validateNonlocalTypeParam(node *parser.NameNode) {
	// The original's comment: look up the symbol to see if it's a type parameter.
	symbolWithScope := c.evaluator.LookUpSymbolRecursive(node, node.D.Value, false)
	if symbolWithScope == nil || symbolWithScope.Scope.Type != ScopeTypeTypeParameter {
		return
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.NonlocalTypeParam().Format(node.D.Value), node, nil)
}

// validateIllegalDefaultParamInitializer corresponds to
// _validateIllegalDefaultParamInitializer.
func (c *Checker) validateIllegalDefaultParamInitializer(node parser.ParseNode) {
	if c.fileInfo.DiagnosticRuleSet.ReportCallInDefaultInitializer == DiagnosticLevelNone {
		return
	}

	if IsWithinDefaultParamInitializer(node) && !c.fileInfo.IsStubFile {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportCallInDefaultInitializer,
			localization.LocMessage.DefaultValueContainsCall(), node, nil)
	}
}

// validateStandardCollectionInstantiation corresponds to
// _validateStandardCollectionInstantiation: `List()` is a typing alias and is
// not callable, unlike `list()`.
func (c *Checker) validateStandardCollectionInstantiation(node *parser.CallNode) {
	leftType := c.evaluator.GetType(node.D.LeftExpr)

	if leftType == nil || !IsInstantiableClass(leftType) {
		return
	}

	leftClass := leftType.(*ClassType)
	if !ClassTypeIsBuiltIn(leftClass) || leftClass.Priv.IncludeSubclasses ||
		leftClass.Priv.AliasName == nil || *leftClass.Priv.AliasName == "" {
		return
	}

	aliasName := *leftClass.Priv.AliasName
	switch aliasName {
	case "List", "Set", "Dict", "Tuple":
	default:
		return
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.CollectionAliasInstantiation().Format(aliasName, leftClass.Shared.Name),
		node.D.LeftExpr, nil)
}

// reportUnusedExceptStatements corresponds to _reportUnusedExceptStatements.
func (c *Checker) reportUnusedExceptStatements(node *parser.TryNode) {
	sawUnknownExceptionType := false
	var exceptionTypesSoFar []*ClassType

	for _, except := range node.D.ExceptClauses {
		if sawUnknownExceptionType || except.D.IsExceptGroup || except.D.TypeExpr == nil {
			continue
		}

		exceptionType := c.evaluator.GetType(except.D.TypeExpr)
		if exceptionType == nil || IsAnyOrUnknown(exceptionType) {
			sawUnknownExceptionType = true
			continue
		}

		var typesOfThisExcept []*ClassType

		switch {
		case IsInstantiableClass(exceptionType):
			// The original's comment: if the exception type is a variable whose
			// type could represent subclasses, the actual exception type is
			// statically unknown.
			if exceptionType.(*ClassType).Priv.IncludeSubclasses {
				sawUnknownExceptionType = true
			}
			typesOfThisExcept = append(typesOfThisExcept, exceptionType.(*ClassType))

		case IsClassInstance(exceptionType):
			emitNotIterableError := false
			var iterableType Type = UnknownTypeCreate(false)
			if result := c.evaluator.GetTypeOfIterator(
				&TypeResult{Type: exceptionType}, false, except.D.TypeExpr,
				&emitNotIterableError); result != nil {
				iterableType = result.Type
			}

			DoForEachSubtype(iterableType, func(subtype Type, _ int, _ []Type) {
				if IsAnyOrUnknown(subtype) {
					sawUnknownExceptionType = true
				}

				if IsInstantiableClass(subtype) {
					if subtype.(*ClassType).Priv.IncludeSubclasses {
						sawUnknownExceptionType = true
					}
					typesOfThisExcept = append(typesOfThisExcept, subtype.(*ClassType))
				}
			})

		default:
			sawUnknownExceptionType = true
		}

		if len(exceptionTypesSoFar) > 0 && !sawUnknownExceptionType {
			c.reportOneUnusedExcept(except, typesOfThisExcept, exceptionTypesSoFar)
		}

		exceptionTypesSoFar = append(exceptionTypesSoFar, typesOfThisExcept...)
	}
}

// reportOneUnusedExcept is the original's inner block, which reports only when
// *every* type this clause names is already caught.
func (c *Checker) reportOneUnusedExcept(
	except *parser.ExceptNode, typesOfThisExcept []*ClassType, exceptionTypesSoFar []*ClassType,
) {
	diagAddendum := common.NewDiagnosticAddendum()
	overriddenExceptionCount := 0

	for _, thisExceptType := range typesOfThisExcept {
		var shadowedBy *ClassType
		for _, previousExceptType := range exceptionTypesSoFar {
			if DerivesFromClassRecursive(thisExceptType, previousExceptType, true) {
				shadowedBy = previousExceptType
				break
			}
		}

		if shadowedBy == nil {
			continue
		}

		diagAddendum.AddMessage(localization.LocAddendum.UnreachableExcept().Format(
			c.evaluator.PrintType(ConvertToInstance(thisExceptType, false), nil),
			c.evaluator.PrintType(ConvertToInstance(shadowedBy, false), nil)))
		overriddenExceptionCount++
	}

	// The original's comment: were all of the exception types overridden?
	if len(typesOfThisExcept) == 0 || len(typesOfThisExcept) != overriddenExceptionCount {
		return
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportUnusedExcept,
		localization.LocMessage.UnreachableExcept()+diagAddendum.GetString(),
		except.D.TypeExpr, nil)

	exceptTokenRange := except.D.ExceptToken.GetRange()
	c.evaluator.AddDiagnostic(DiagnosticRuleReportUnreachable,
		localization.LocMessage.UnreachableCodeType(),
		except.D.ExceptSuite, &exceptTokenRange)

	c.evaluator.AddUnreachableCode(except, ReachabilityUnreachableByAnalysis,
		except.D.ExceptSuite.GetRange())
}
