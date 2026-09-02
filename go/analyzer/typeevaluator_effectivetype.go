/*
 * typeevaluator_effectivetype.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getEffectiveTypeOfSymbol and getEffectiveTypeOfSymbolForUsage, which decide
 * whether a symbol's type comes from a declaration or from inference.
 *
 * This is the fork every name eventually reaches. A symbol with a typed
 * declaration takes the declared path, which ends at getDeclaredTypeOfSymbol
 * and from there at whatever created the class, function or annotation. A
 * symbol without one takes the inference path. Both are still ahead; what is
 * here is the choice between them and the bookkeeping around it.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// GetEffectiveTypeOfSymbol corresponds to getEffectiveTypeOfSymbol.
func (e *typeEvaluator) GetEffectiveTypeOfSymbol(symbol *Symbol) Type {
	return e.GetEffectiveTypeOfSymbolForUsage(symbol, nil, false).Type
}

// GetEffectiveTypeOfSymbolForUsage corresponds to the function of the same
// name. The original's comment: if a "usageNode" node is specified, only
// declarations that are outside of the current execution scope or that are
// reachable (as determined by code flow analysis) are considered. This helps in
// cases where there are cyclical dependencies between symbols.
func (e *typeEvaluator) GetEffectiveTypeOfSymbolForUsage(
	symbol *Symbol,
	usageNode *parser.NameNode,
	useLastDecl bool,
) *EffectiveTypeResult {
	// The original's comment: if there's a declared type, it takes precedence
	// over inferred types.
	if symbol.HasTypedDeclarations() {
		declaredTypeInfo := e.getDeclaredTypeOfSymbol(symbol, usageNode)
		var declaredType Type
		if declaredTypeInfo != nil {
			declaredType = declaredTypeInfo.Type
		}

		isIncomplete := false
		if declaredType != nil {
			if IsFunction(declaredType) && FunctionTypeIsPartiallyEvaluated(declaredType.(*FunctionType)) {
				isIncomplete = true
			} else if IsClass(declaredType) && ClassTypeIsPartiallyEvaluated(declaredType.(*ClassType)) {
				isIncomplete = true
			}
		}

		// The original's comment: if the "declared" type uses a "TypeAlias"
		// type annotation, then we need to use the inferred type path to
		// evaluate its type.
		//
		// `declaredType || !declaredTypeInfo.isTypeAlias` dereferences
		// declaredTypeInfo unconditionally in the second operand, so a nil
		// result from getDeclaredTypeOfSymbol would throw in the original.
		if declaredType != nil || (declaredTypeInfo != nil && !declaredTypeInfo.IsTypeAlias) {
			typedDecls := symbol.GetTypedDeclarations()

			// The original's comment: if we received an undefined declared
			// type, this can be caused by exceeding the max number of type
			// declarations, speculative evaluation, or a recursive definition.
			isRecursiveDefinition := declaredType == nil &&
				declaredTypeInfo != nil && !declaredTypeInfo.ExceedsMaxDecls &&
				!e.speculativeTypeTracker.IsSpeculative(nil, false)

			resultType := declaredType
			if resultType == nil {
				resultType = UnknownTypeCreate(false)
			}

			includesIllegalTypeAliasDecl := false
			for _, decl := range typedDecls {
				if !e.isPossibleTypeAliasDeclaration(decl) {
					includesIllegalTypeAliasDecl = true
					break
				}
			}

			return &EffectiveTypeResult{
				Type:                         resultType,
				IsIncomplete:                 isIncomplete,
				IncludesVariableDecl:         includesVariableTypeDecl(typedDecls),
				IncludesIllegalTypeAliasDecl: includesIllegalTypeAliasDecl,
				IncludesSpeculativeResult:    false,
				IsRecursiveDefinition:        isRecursiveDefinition,
			}
		}
	}

	return e.inferTypeOfSymbolForUsage(symbol, usageNode, useLastDecl)
}

// includesVariableTypeDecl corresponds to the function of the same name.
func includesVariableTypeDecl(decls []Declaration) bool {
	for _, decl := range decls {
		switch decl.DeclBase().Type {
		case DeclarationTypeVariable:
			// The original's comment: exempt typing.pyi and
			// typingExtensions.pyi, which use variables to define some special
			// forms.
			fileInfo := GetFileInfo(decl.DeclBase().Node)
			if !fileInfo.IsTypingStubFile && !fileInfo.IsTypingExtensionsStubFile {
				return true
			}
		case DeclarationTypeParam:
			return true
		}
	}
	return false
}

// isPossibleTypeAliasDeclaration corresponds to the function of the same name.
func (e *typeEvaluator) isPossibleTypeAliasDeclaration(decl Declaration) bool {
	variableDecl, ok := decl.(*VariableDeclaration)
	if !ok || variableDecl.TypeAliasName == nil || variableDecl.TypeAnnotationNode != nil {
		return false
	}

	parent := variableDecl.Node.NodeBase().Parent
	assignment, ok := parent.(*parser.AssignmentNode)
	if !ok {
		return false
	}

	// The original's comment: perform a sanity check on the RHS expression.
	// Some expression forms should never be considered legitimate for type
	// aliases.
	return e.isLegalTypeAliasExpressionForm(assignment.D.RightExpr, false)
}

/*
 * The two paths out.
 */

// getDeclaredTypeOfSymbol and getTypeForDeclaration are in
// typeevaluator_decl.go; the fork above chooses between them and the inference
// path below.

// inferTypeOfSymbolForUsage corresponds to the function of the same name: the
// path taken by a symbol with no typed declaration.
func (e *typeEvaluator) inferTypeOfSymbolForUsage(
	_ *Symbol,
	_ *parser.NameNode,
	_ bool,
) *EffectiveTypeResult {
	e.unported("inferTypeOfSymbolForUsage")
	return &EffectiveTypeResult{Type: UnknownTypeCreate(false)}
}

// isLegalTypeAliasExpressionForm corresponds to the function of the same name.
func (e *typeEvaluator) isLegalTypeAliasExpressionForm(_ parser.ExpressionNode, _ bool) bool {
	e.unported("isLegalTypeAliasExpressionForm")
	return false
}
