/*
 * typeevaluator_statementtypes.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * evaluateTypesForForStatement, evaluateTypesForExceptStatement,
 * evaluateTypesForWithStatement and evaluateTypesForImportFrom.
 *
 * These are the statements that bind a name to something other than the value of
 * an expression, so each has to derive the bound type from a protocol rather
 * than read it off an assignment. `for` goes through the iteration protocol;
 * `with` goes through __enter__/__aenter__; `except` unwraps a class, a tuple of
 * classes, or an iterable of them; a wildcard `import *` binds many names at
 * once and takes them from the flow node rather than the statement.
 *
 * Each begins with an isTypeCached guard and ends with a writeTypeCache, and
 * that pairing is the point: the code flow engine reaches these through the
 * statement node, so the cache entry on the statement is what makes the binding
 * visible to later flow queries without re-deriving it.
 *
 * evaluateTypesForWithStatement is the one with a subtlety worth naming. The
 * __exit__ pass looks like it computes a type -- its callback returns one -- but
 * it runs under doForEachSubtype, which discards return values. The pass exists
 * only for its diagnostics and its isIncomplete side effect. The port keeps the
 * dead returns out rather than writing values nothing reads.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// evaluateTypesForForStatement corresponds to the function of the same name.
func (e *typeEvaluator) evaluateTypesForForStatement(node *parser.ForNode) {
	if e.isTypeCached(node) {
		return
	}

	iteratorTypeResult := e.getTypeOfExpression(node.D.IterableExpr, EvalFlagsNone, nil)

	var iteratedType Type = UnknownTypeCreate(false)
	if result := e.GetTypeOfIterator(
		iteratorTypeResult, node.D.IsAsync, node.D.IterableExpr, nil); result != nil {
		iteratedType = result.Type
	}

	e.AssignTypeToExpression(node.D.TargetExpr,
		&TypeResult{Type: iteratedType, IsIncomplete: iteratorTypeResult.IsIncomplete},
		node.D.TargetExpr)

	noneFlags := EvalFlagsNone
	e.writeTypeCache(node,
		&TypeResult{Type: iteratedType, IsIncomplete: iteratorTypeResult.IsIncomplete},
		&noneFlags, nil, false)
}

// evaluateTypesForExceptStatement corresponds to the function of the same name.
func (e *typeEvaluator) evaluateTypesForExceptStatement(node *parser.ExceptNode) {
	// The original asserts node.d.typeExpr is defined; it is called only for an
	// except clause that has a target exception.
	if e.isTypeCached(node) {
		return
	}

	exceptionTypeResult := e.getTypeOfExpression(node.D.TypeExpr, EvalFlagsNone, nil)
	exceptionTypes := exceptionTypeResult.Type
	includesBaseException := false

	// getExceptionType corresponds to the local function of the same name. It
	// closes over includesBaseException, which the except-group wrapping below
	// reads.
	var getExceptionType func(exceptionType Type, errorNode parser.ExpressionNode) Type
	getExceptionType = func(exceptionType Type, errorNode parser.ExpressionNode) Type {
		exceptionType = e.makeTopLevelTypeVarsConcrete(exceptionType, false, nil)

		if IsAnyOrUnknown(exceptionType) {
			return exceptionType
		}

		if IsInstantiableClass(exceptionType) {
			if ClassTypeIsBuiltInNamed(exceptionType.(*ClassType), "BaseException") {
				includesBaseException = true
			}
			return ClassTypeCloneAsInstance(exceptionType.(*ClassType), true)
		}

		if IsClassInstance(exceptionType) {
			emitNotIterableError := false
			var iterableType Type = UnknownTypeCreate(false)
			if result := e.GetTypeOfIterator(
				&TypeResult{Type: exceptionType, IsIncomplete: exceptionTypeResult.IsIncomplete},
				false, errorNode, &emitNotIterableError); result != nil {
				iterableType = result.Type
			}

			return MapSubtypes(iterableType, func(subtype Type) Type {
				if IsAnyOrUnknown(subtype) {
					return subtype
				}

				return UnknownTypeCreate(false)
			}, nil)
		}

		return UnknownTypeCreate(false)
	}

	targetType := MapSubtypes(exceptionTypes, func(subType Type) Type {
		// The original's comment: if more than one type was specified for the
		// exception, we'll receive a specialized tuple object here.
		tupleType := GetSpecializedTupleType(subType)
		if tupleType != nil && tupleType.Priv.TupleTypeArgs != nil {
			entryTypes := make([]Type, 0, len(tupleType.Priv.TupleTypeArgs))
			for _, t := range tupleType.Priv.TupleTypeArgs {
				entryTypes = append(entryTypes, getExceptionType(t.Type, node.D.TypeExpr))
			}
			return CombineTypes(entryTypes, nil)
		}

		return getExceptionType(subType, node.D.TypeExpr)
	}, nil)

	// The original's comment: if this is an except group, wrap the exception type
	// in an ExceptionGroup or BaseExceptionGroup depending on whether the target
	// exception is a BaseException.
	if node.D.IsExceptGroup {
		groupName := "ExceptionGroup"
		if includesBaseException {
			groupName = "BaseExceptionGroup"
		}
		targetType = e.GetBuiltInObject(node, groupName, []Type{targetType})
	}

	if node.D.Name != nil {
		e.AssignTypeToExpression(node.D.Name, &TypeResult{Type: targetType}, node.D.Name)
	}

	noneFlags := EvalFlagsNone
	e.writeTypeCache(node, &TypeResult{Type: targetType}, &noneFlags, nil, false)
}

// evaluateTypesForWithStatement corresponds to the function of the same name.
func (e *typeEvaluator) evaluateTypesForWithStatement(node *parser.WithItemNode) {
	if e.isTypeCached(node) {
		return
	}

	exprTypeResult := e.getTypeOfExpression(node.D.Expr, EvalFlagsNone, nil)
	isIncomplete := exprTypeResult.IsIncomplete
	exprType := exprTypeResult.Type

	isAsync := false
	if withNode, ok := node.NodeBase().Parent.(*parser.WithNode); ok {
		isAsync = withNode.D.IsAsync
	}

	if IsOptionalType(exprType) {
		message := localization.LocMessage.NoneNotUsableWith()
		if isAsync {
			message = localization.LocMessage.NoneNotUsableWithAsync()
		}
		e.AddDiagnostic(DiagnosticRuleReportOptionalContextManager, message, node.D.Expr, nil)
		exprType = RemoveNoneFromUnion(exprType)
	}

	// The original's comment: verify that the target has an __enter__ or
	// __aenter__ method defined.
	enterMethodName := "__enter__"
	if isAsync {
		enterMethodName = "__aenter__"
	}

	scopedType := MapSubtypes(exprType, func(subtype Type) Type {
		subtype = e.makeTopLevelTypeVarsConcrete(subtype, false, nil)

		if IsAnyOrUnknown(subtype) {
			return subtype
		}

		enterDiag := common.NewDiagnosticAddendum()

		if IsClass(subtype) {
			enterTypeResult := e.getTypeOfMagicMethodCall(
				subtype, enterMethodName, nil, node.D.Expr, nil, enterDiag.CreateAddendum())

			if enterTypeResult != nil {
				if !isAsync {
					return enterTypeResult.Type
				}

				if enterTypeResult.IsIncomplete {
					isIncomplete = true
				}

				asyncResult := e.getTypeOfAwaitable(
					&TypeResult{Type: enterTypeResult.Type}, node.D.Expr)
				if asyncResult.IsIncomplete {
					isIncomplete = true
				}

				return asyncResult.Type
			}

			if !isAsync {
				// A synchronous `with` on an async context manager is a common
				// enough mistake to get its own hint.
				if asyncEnter := e.getTypeOfMagicMethodCall(
					subtype, "__aenter__", nil, node.D.Expr, nil, nil); asyncEnter != nil &&
					asyncEnter.Type != nil {
					enterDiag.AddMessage(localization.LocAddendum.AsyncHelp())
				}
			}
		}

		message := localization.LocMessage.TypeNotUsableWith().
			Format(e.PrintType(subtype, nil), enterMethodName)
		if isAsync {
			message = localization.LocMessage.TypeNotUsableWithAsync().
				Format(e.PrintType(subtype, nil), enterMethodName)
		}
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			message+enterDiag.GetString(), node.D.Expr, nil)
		return UnknownTypeCreate(false)
	}, nil)

	// The original's comment: verify that the target has an __exit__ or __aexit__
	// method defined.
	exitMethodName := "__exit__"
	if isAsync {
		exitMethodName = "__aexit__"
	}
	exitDiag := common.NewDiagnosticAddendum()

	DoForEachSubtype(exprType, func(subtype Type, _ int, _ []Type) {
		subtype = e.makeTopLevelTypeVarsConcrete(subtype, false, nil)

		if IsAnyOrUnknown(subtype) {
			return
		}

		if IsClass(subtype) {
			anyArg := &TypeResult{Type: AnyTypeCreate(false)}
			exitTypeResult := e.getTypeOfMagicMethodCall(
				subtype, exitMethodName,
				[]*TypeResult{anyArg, anyArg, anyArg}, node.D.Expr, nil, exitDiag)

			if exitTypeResult != nil {
				if exitTypeResult.IsIncomplete {
					isIncomplete = true
				}

				if isAsync {
					// The original returns the awaited type here, but the callback
					// runs under doForEachSubtype, which discards return values.
					// Only the isIncomplete side effect survives.
					asyncResult := e.getTypeOfAwaitable(
						&TypeResult{Type: exitTypeResult.Type}, node.D.Expr)
					if asyncResult.IsIncomplete {
						isIncomplete = true
					}
				}

				return
			}
		}

		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeNotUsableWith().
				Format(e.PrintType(subtype, nil), exitMethodName)+exitDiag.GetString(),
			node.D.Expr, nil)
	})

	if node.D.Target != nil {
		e.AssignTypeToExpression(node.D.Target,
			&TypeResult{Type: scopedType, IsIncomplete: isIncomplete}, node.D.Target)
	}

	noneFlags := EvalFlagsNone
	e.writeTypeCache(node,
		&TypeResult{Type: scopedType, IsIncomplete: isIncomplete}, &noneFlags, nil, false)
}

// evaluateTypesForImportFrom corresponds to the function of the same name.
func (e *typeEvaluator) evaluateTypesForImportFrom(node *parser.ImportFromNode) {
	if e.isTypeCached(node) {
		return
	}

	noneFlags := EvalFlagsNone

	if node.D.IsWildcardImport {
		// The original's comment: write back a dummy type so we don't evaluate
		// this node again.
		e.writeTypeCache(node, &TypeResult{Type: AnyTypeCreate(false)}, &noneFlags, nil, false)

		flowNode := GetFlowNode(node)
		wildcardFlowNode, isWildcard := flowNode.(*FlowWildcardImport)
		if flowNode == nil || (flowNode.FlowBase().Flags&FlowFlagsWildcardImport) == 0 || !isWildcard {
			return
		}

		for _, name := range wildcardFlowNode.Names {
			importedSymbolType := e.getAliasedSymbolTypeForName(node, name)
			if importedSymbolType == nil {
				continue
			}

			symbolWithScope := e.LookUpSymbolRecursive(node, name, false)
			if symbolWithScope == nil {
				continue
			}

			declaredTypeResult := e.GetDeclaredTypeOfSymbol(symbolWithScope.Symbol)
			if declaredTypeResult == nil || declaredTypeResult.Type == nil {
				continue
			}
			declaredType := declaredTypeResult.Type

			diagAddendum := common.NewDiagnosticAddendum()

			if !e.AssignType(declaredType, importedSymbolType, diagAddendum,
				nil, AssignTypeFlagsDefault, 0) {
				srcDestTypes := e.PrintSrcDestTypes(importedSymbolType, declaredType)

				errorRange := node.GetRange()
				if node.D.WildcardToken != nil {
					errorRange = node.D.WildcardToken.GetRange()
				}

				e.AddDiagnostic(DiagnosticRuleReportAssignmentType,
					localization.LocMessage.TypeAssignmentMismatchWildcard().Format(
						srcDestTypes.SourceType, srcDestTypes.DestType, name)+diagAddendum.GetString(),
					node, &errorRange)
			}
		}

		return
	}

	// The original's comment: use the first element of the name parts as the
	// symbol.
	symbolNameNode := node.D.Module.D.NameParts[0]

	// The original's comment: look up the symbol to find the alias declaration.
	symbolType := e.getAliasedSymbolTypeForName(node, symbolNameNode.D.Value)
	if symbolType == nil {
		return
	}

	// The original's comment: is there a cached module type associated with this
	// node? If so, use it instead of the type we just created.
	cachedModuleType := e.readTypeCache(node, &noneFlags)
	if cachedModuleType != nil && IsModule(cachedModuleType) {
		if IsTypeSame(symbolType, cachedModuleType, TypeSameOptions{}, 0) {
			symbolType = cachedModuleType
		}
	}

	e.assignTypeToNameNode(symbolNameNode, &TypeResult{Type: symbolType}, false, nil, false, nil)

	e.writeTypeCache(node, &TypeResult{Type: symbolType}, &noneFlags, nil, false)
}
