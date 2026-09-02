/*
 * typeevaluator_infer.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * inferTypeOfSymbolForUsage and getTypeOfSymbolForDecls.
 *
 * This is the other half of the effective-type fork. A symbol with a typed
 * declaration takes the declared path, which landed with getTypeForDeclaration;
 * a symbol without one arrives here, and most symbols in ordinary Python code
 * do. Until now every one of them answered Unknown.
 *
 * The work is deciding which of a symbol's declarations to believe, which is
 * more delicate than the declared path because the answer depends on where the
 * symbol is being used. Declarations after the usage in the same execution scope
 * are dropped -- they are reached only via loop back-edges and including them
 * makes the evaluation circular -- while declarations before it are kept, so
 * order-independent type aliases still resolve. The original carries a long
 * comment explaining why that asymmetry is safe; it is preserved verbatim.
 *
 * Both functions cache into effectiveTypeCache, keyed by usage node and the
 * useLastDecl flag, and getTypeOfSymbolForDecls counts its own attempts so a
 * cyclical dependency that cannot be broken eventually stops being reported as
 * incomplete.
 */

package analyzer

import (
	"strconv"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// inferTypeOfSymbolForUsage corresponds to the function of the same name.
func (e *typeEvaluator) inferTypeOfSymbolForUsage(
	symbol *Symbol,
	usageNode *parser.NameNode,
	useLastDecl bool,
) *EffectiveTypeResult {
	// The original's comment: look in the inferred type cache to see if we've
	// computed this already.
	cacheKey := effectiveTypeCacheKey(usageNode, useLastDecl)
	cacheEntries, _ := e.effectiveTypeCache.Get(symbol.ID)
	if cacheEntries != nil {
		if entry, ok := cacheEntries.Get(cacheKey); ok && entry != nil && !entry.IsIncomplete {
			return entry
		}
	}

	addToEffectiveTypeCache := func(result *EffectiveTypeResult) {
		// The original's comment: add the entry to the cache so we don't need to
		// compute it next time.
		if cacheEntries == nil {
			cacheEntries = common.NewOrderedMap[string, *EffectiveTypeResult]()
			e.effectiveTypeCache.Set(symbol.ID, cacheEntries)
		}

		cacheEntries.Set(cacheKey, result)
	}

	// Infer the type.
	decls := symbol.GetDeclarations()

	// The original's comment: limit the number of declarations to explore.
	if len(decls) > maxDeclarationsToUseForInference {
		includesIllegal := false
		for _, decl := range decls {
			if !e.isPossibleTypeAliasDeclaration(decl) {
				includesIllegal = true
				break
			}
		}

		result := &EffectiveTypeResult{
			Type:                         UnknownTypeCreate(false),
			IncludesIllegalTypeAliasDecl: includesIllegal,
		}

		addToEffectiveTypeCache(result)
		return result
	}

	// declIndexToConsider is the original's `number | undefined`; -1 stands in
	// for undefined, which means "consider every declaration".
	declIndexToConsider := -1

	if useLastDecl {
		// The original's comment: if the caller has requested that we use only
		// the last decl, we will use only the last one, but we'll ignore decls
		// that are in except clauses.
		for index, decl := range decls {
			if !decl.DeclBase().IsInExceptSuite {
				declIndexToConsider = index
			}
		}
	} else if len(decls) > 1 {
		// The original's comment: handle the case where there are multiple
		// imports -- one of them in a try block and one or more in except
		// blocks. In this case, we'll use the one in the try block rather than
		// the excepts.
		allAlias := true
		for _, decl := range decls {
			if decl.DeclBase().Type != DeclarationTypeAlias {
				allAlias = false
				break
			}
		}

		if allAlias {
			nonExceptIndex := -1
			nonExceptCount := 0
			for index, decl := range decls {
				if !decl.DeclBase().IsInExceptSuite {
					nonExceptIndex = index
					nonExceptCount++
				}
			}
			if nonExceptCount == 1 {
				declIndexToConsider = nonExceptIndex
			}
		}
	}

	declsToConsider, includesVariableDecl, includesIllegalTypeAliasDecl :=
		e.selectDeclsForInference(symbol, decls, usageNode, declIndexToConsider)

	result := e.getTypeOfSymbolForDecls(symbol, declsToConsider, cacheKey)
	result.IncludesVariableDecl = includesVariableDecl
	result.IncludesIllegalTypeAliasDecl = includesIllegalTypeAliasDecl

	// The original's comment: add the result to the effective type cache if it
	// doesn't include speculative results.
	if !result.IncludesSpeculativeResult {
		addToEffectiveTypeCache(result)
	}

	return result
}

// selectDeclsForInference is the original's `decls.forEach` body plus the
// augmented-assignment check that follows it, lifted out because it declares
// three results the caller needs.
func (e *typeEvaluator) selectDeclsForInference(
	symbol *Symbol,
	decls []Declaration,
	usageNode *parser.NameNode,
	declIndexToConsider int,
) (declsToConsider []Declaration, includesVariableDecl bool, includesIllegalTypeAliasDecl bool) {
	declsToConsider = []Declaration{}
	sawExplicitTypeAlias := false

	for index, decl := range decls {
		resolvedDecl := e.ResolveAliasDeclaration(decl, true, &EvaluatorResolveAliasOptions{
			AllowExternallyHiddenAccess: GetFileInfo(decl.DeclBase().Node).IsStubFile,
		})
		if resolvedDecl == nil {
			resolvedDecl = decl
		}

		if !e.isPossibleTypeAliasDeclaration(resolvedDecl) && !e.isExplicitTypeAliasDeclaration(resolvedDecl) {
			includesIllegalTypeAliasDecl = true
		}

		if includesVariableTypeDecl([]Declaration{resolvedDecl}) {
			includesVariableDecl = true
		}

		if declIndexToConsider >= 0 && declIndexToConsider != index {
			continue
		}

		// The original's comment: if we have already seen an explicit type
		// alias, do not consider additional decls. This can happen if multiple
		// TypeAlias declarations are provided -- normally an error, but it can
		// happen in stdlib stubs if the user sets the pythonPlatform to "All".
		if sawExplicitTypeAlias {
			continue
		}

		// The original's comment: if the symbol is explicitly marked as a
		// ClassVar, consider only the declarations that assign to it from within
		// the class body, not through a member access expression.
		if variableDecl, ok := decl.(*VariableDeclaration); ok &&
			IsEffectivelyClassVar(symbol, false) && variableDecl.IsDefinedByMemberAccess {
			continue
		}

		if usageNode != nil && decl.DeclBase().Type != DeclarationTypeAlias {
			// The original's comment: is the declaration in the same execution
			// scope as the "usageNode" node? If so, we can skip it because code
			// flow analysis will allow us to determine the type in this context.
			usageScope := GetExecutionScopeNode(usageNode)
			declScope := GetExecutionScopeNode(decl.DeclBase().Node)
			if usageScope == declScope {
				// The original's comment: skip declarations that appear after
				// the usage in the source. Such declarations are typically only
				// reached via loop back-edges, and including them causes
				// circular evaluation (producing Unknown). Declarations that
				// textually precede the usage are retained so that
				// order-independent type aliases resolve correctly; for
				// branching constructs (if/else, try/except), a retained
				// declaration may be flow-unreachable at the usage site, but the
				// code-flow engine overrides the effective type for local
				// variables, so the narrowed type at the usage site is correct.
				// Note: for the cases filtered out here (decl after usage in
				// same scope), the code-flow engine handles loop-carried
				// narrowing when it evaluates the type at the actual usage site.
				if decl.DeclBase().Node.NodeBase().Start >= usageNode.NodeBase().Start {
					continue
				}
			}
		}

		isExplicitTypeAlias := e.isExplicitTypeAliasDeclaration(resolvedDecl)
		isTypeAlias := isExplicitTypeAlias || e.isPossibleTypeAliasOrTypedDict(resolvedDecl)

		if isExplicitTypeAlias {
			sawExplicitTypeAlias = true
		}

		// The original's comment: if this is a type alias, evaluate it outside of
		// the recursive symbol resolution check so we can evaluate the full
		// assignment statement.
		if isTypeAlias {
			if variableDecl, ok := resolvedDecl.(*VariableDeclaration); ok && variableDecl.InferredTypeSource != nil {
				if assignment, ok := variableDecl.InferredTypeSource.NodeBase().Parent.(*parser.AssignmentNode); ok {
					e.evaluateTypesForAssignmentStatement(assignment)
				}
			}
		}

		declsToConsider = append(declsToConsider, resolvedDecl)
	}

	// The original's comment: if all of the decls come from augmented
	// assignments, we won't be able to determine its type. At least one
	// declaration must be a simple assignment.
	//
	// `[].every(...)` is true, so an empty list is spliced to empty, which is a
	// no-op; the Go form needs the length guard to match.
	if len(declsToConsider) > 0 {
		allAugmented := true
		for _, decl := range declsToConsider {
			if _, ok := decl.(*VariableDeclaration); !ok {
				allAugmented = false
				break
			}
			if !IsNodeContainedWithinNodeType(decl.DeclBase().Node, parser.ParseNodeTypeAugmentedAssignment) {
				allAugmented = false
				break
			}
		}
		if allAugmented {
			declsToConsider = declsToConsider[:0]
		}
	}

	return declsToConsider, includesVariableDecl, includesIllegalTypeAliasDecl
}

// getTypeOfSymbolForDecls corresponds to the function of the same name. The
// original's comment: returns the type of a symbol based on a subset of its
// declarations.
func (e *typeEvaluator) getTypeOfSymbolForDecls(
	symbol *Symbol,
	decls []Declaration,
	typeCacheKey string,
) *EffectiveTypeResult {
	typesToCombine := []Type{}
	isIncomplete := false
	sawPendingEvaluation := false
	includesSpeculativeResult := false

	for _, decl := range decls {
		if !e.pushSymbolResolution(symbol, decl) {
			if decl.DeclBase().Type == DeclarationTypeClass {
				classTypeInfo := e.GetTypeOfClass(decl.DeclBase().Node.(*parser.ClassNode))
				if classTypeInfo != nil && classTypeInfo.DecoratedType != nil {
					typesToCombine = append(typesToCombine, classTypeInfo.DecoratedType)
				}
			}

			isIncomplete = true

			// The original's comment: note that at least one decl could not be
			// evaluated because it was already in the process of being
			// evaluated.
			sawPendingEvaluation = true
			continue
		}

		t, popped, speculative := e.inferDeclWithinSymbolResolution(symbol, decl)
		if !popped {
			isIncomplete = true
		}

		if t == nil {
			isIncomplete = true
			continue
		}

		typesToCombine = append(typesToCombine, t)
		if speculative {
			includesSpeculativeResult = true
		}
	}

	// The original's comment: how many times have we already attempted to
	// evaluate this declaration already?
	evaluationAttempts := 1
	if cacheEntries, _ := e.effectiveTypeCache.Get(symbol.ID); cacheEntries != nil {
		if entry, ok := cacheEntries.Get(typeCacheKey); ok && entry != nil {
			evaluationAttempts = entry.EvaluationAttempts + 1
		}
	}

	var t Type

	if len(typesToCombine) > 0 {
		// The original's comment: ignore the pending evaluation flag if we've
		// already attempted the type evaluation many times because this probably
		// means there's a cyclical dependency that cannot be broken.
		isIncomplete = sawPendingEvaluation && evaluationAttempts < maxEffectiveTypeEvaluationAttempts

		t = CombineTypes(typesToCombine, nil)
	} else if symbol.IsClassVar() {
		// The original's comment: we can encounter this situation in the case of
		// a bare ClassVar annotation.
		t = UnknownTypeCreate(false)
		isIncomplete = false
	} else {
		t = UnboundTypeCreate()
	}

	return &EffectiveTypeResult{
		Type:                      t,
		IsIncomplete:              isIncomplete,
		IncludesSpeculativeResult: includesSpeculativeResult,
		EvaluationAttempts:        evaluationAttempts,
	}
}

// inferDeclWithinSymbolResolution is the original's try/catch around
// getInferredTypeOfDeclaration, plus the variable-declaration widening that
// follows it. As in getDeclaredTypeOfSymbol, the catch that pops the symbol
// resolution stack before rethrowing becomes a defer.
func (e *typeEvaluator) inferDeclWithinSymbolResolution(
	symbol *Symbol,
	decl Declaration,
) (t Type, popped bool, speculative bool) {
	symbolPopped := false
	defer func() {
		if !symbolPopped {
			// The original's comment: clean up the stack before rethrowing, but
			// only if we haven't already popped.
			e.popSymbolResolution(symbol)
		}
	}()

	t = e.getInferredTypeOfDeclaration(symbol, decl)

	popped = e.popSymbolResolution(symbol)
	symbolPopped = true

	if t == nil {
		return nil, popped, false
	}

	if variableDecl, ok := decl.(*VariableDeclaration); ok {
		isConstant := variableDecl.IsConstant || e.IsFinalVariableDeclaration(decl)

		// The original's comment: treat enum values declared within an enum
		// class as though they are const even though they may not be named as
		// such.
		if IsClassInstance(t) && ClassTypeIsEnumClass(t.(*ClassType)) && e.isDeclInEnumClass(decl) {
			isConstant = true
		}

		// The original's comment: if the symbol is constant, we can retain the
		// literal value and TypeForm types. Otherwise, strip literal values and
		// TypeForm types to widen.
		if t.Base().IsInstance() && !isConstant && !e.isExplicitTypeAliasDeclaration(decl) {
			t = StripTypeForm(e.StripLiteralValue(t))
		}
	}

	return t, popped, e.IsSpeculativeModeInUse(decl.DeclBase().Node)
}

// effectiveTypeCacheKey builds the original's template-literal cache key: the
// usage node's id or "." when there is none, followed by "*" when useLastDecl.
func effectiveTypeCacheKey(usageNode *parser.NameNode, useLastDecl bool) string {
	key := "."
	if usageNode != nil {
		key = strconv.Itoa(usageNode.NodeBase().ID)
	}
	if useLastDecl {
		key += "*"
	}
	return key
}

/*
 * The alias-resolution wrappers, which were stubs and are one call each.
 */

// ResolveAliasDeclaration corresponds to resolveAliasDeclaration.
func (e *typeEvaluator) ResolveAliasDeclaration(
	declaration Declaration,
	resolveLocalNames bool,
	options *EvaluatorResolveAliasOptions,
) Declaration {
	info := e.ResolveAliasDeclarationWithInfo(declaration, resolveLocalNames, options)
	if info == nil {
		return nil
	}
	return info.Declaration
}

// ResolveAliasDeclarationWithInfo corresponds to resolveAliasDeclarationWithInfo.
func (e *typeEvaluator) ResolveAliasDeclarationWithInfo(
	declaration Declaration,
	resolveLocalNames bool,
	options *EvaluatorResolveAliasOptions,
) *ResolvedAliasInfo {
	resolveOptions := ResolveAliasOptions{ResolveLocalNames: resolveLocalNames}
	if options != nil {
		resolveOptions.AllowExternallyHiddenAccess = options.AllowExternallyHiddenAccess
		resolveOptions.SkipFileNeededCheck = options.SkipFileNeededCheck
	}

	return ResolveAliasDeclaration(e.importLookup, declaration, resolveOptions)
}

/*
 * The type-alias predicates.
 */

// isExplicitTypeAliasDeclaration corresponds to the function of the same name.
func (e *typeEvaluator) isExplicitTypeAliasDeclaration(decl Declaration) bool {
	variableDecl, ok := decl.(*VariableDeclaration)
	if !ok || variableDecl.TypeAnnotationNode == nil {
		return false
	}

	switch variableDecl.TypeAnnotationNode.GetNodeType() {
	case parser.ParseNodeTypeName, parser.ParseNodeTypeMemberAccess, parser.ParseNodeTypeStringList:
	default:
		return false
	}

	t := e.GetTypeOfAnnotation(variableDecl.TypeAnnotationNode, &ExpectedTypeOptions{
		VarTypeAnnotation: true,
		AllowClassVar:     true,
	})
	return IsClassInstance(t) && ClassTypeIsBuiltInNamed(t.(*ClassType), "TypeAlias")
}

// IsExplicitTypeAliasDeclaration is the interface form.
func (e *typeEvaluator) IsExplicitTypeAliasDeclaration(decl Declaration) bool {
	return e.isExplicitTypeAliasDeclaration(decl)
}

// isPossibleTypeAliasOrTypedDict corresponds to the function of the same name.
func (e *typeEvaluator) isPossibleTypeAliasOrTypedDict(decl Declaration) bool {
	return e.isPossibleTypeAliasDeclaration(decl) || e.isPossibleTypeDictFactoryCall(decl)
}

// isPossibleTypeDictFactoryCall corresponds to the function of the same name.
func (e *typeEvaluator) isPossibleTypeDictFactoryCall(decl Declaration) bool {
	variableDecl, ok := decl.(*VariableDeclaration)
	if !ok || variableDecl.Node == nil {
		return false
	}

	assignment, ok := variableDecl.Node.NodeBase().Parent.(*parser.AssignmentNode)
	if !ok {
		return false
	}

	call, ok := assignment.D.RightExpr.(*parser.CallNode)
	if !ok {
		return false
	}

	callLeftNode := call.D.LeftExpr

	// The original's comment: use a simple heuristic to determine whether this is
	// potentially a call to the TypedDict call. This avoids the expensive (and
	// potentially recursive) call to getTypeOfExpression in cases where it's not
	// needed.
	//
	// The original's first disjunct is parenthesized oddly:
	//   (callLeftNode.nodeType === Name && callLeftNode.d.value) === 'TypedDict'
	// The `&&` yields the boolean false when the node is not a Name, and the
	// value string when it is, so comparing the whole thing against 'TypedDict'
	// gives the same answer as the intended `A && B === 'TypedDict'`. It reads
	// like a misplaced paren but is not a bug, so it is not in UPSTREAM-BUGS.md;
	// written out here as what it evaluates to.
	matches := false
	switch left := callLeftNode.(type) {
	case *parser.NameNode:
		matches = left.D.Value == "TypedDict"
	case *parser.MemberAccessNode:
		if left.D.Member.D.Value == "TypedDict" {
			_, matches = left.D.LeftExpr.(*parser.NameNode)
		}
	}

	if !matches {
		return false
	}

	// The original's comment: see if this is a call to TypedDict. We want to
	// support recursive type references in a TypedDict call.
	callType := e.getTypeOfExpression(callLeftNode, EvalFlagsCallBaseDefaults, nil).Type

	return IsInstantiableClass(callType) && ClassTypeIsBuiltInNamed(callType.(*ClassType), "TypedDict")
}

/*
 * The satellite this layer reaches. getInferredTypeOfDeclaration is in
 * typeevaluator_inferdecl.go.
 */

// isDeclInEnumClass corresponds to the enums.ts function of the same name.
func (e *typeEvaluator) isDeclInEnumClass(_ Declaration) bool {
	e.unported("isDeclInEnumClass")
	return false
}
