/*
 * binder_scopes.go
 *
 * The binder's scope and symbol-binding machinery, transliterated from
 * analyzer/binder.ts (pyright 1.1.412): _bindNameToScope and friends,
 * _addSymbolToCurrentScope, _createNewScope, and the chained module-level
 * lookup used for notebook cells.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// isStaticClassAssignmentTarget corresponds to the free function of the same
// name.
func isStaticClassAssignmentTarget(target parser.ExpressionNode) bool {
	switch typed := target.(type) {
	case *parser.NameNode:
		return true

	case *parser.TypeAnnotationNode:
		return isStaticClassAssignmentTarget(typed.D.ValueExpr)

	case *parser.TupleNode:
		for _, item := range typed.D.Items {
			if !isStaticClassAssignmentTarget(item) {
				return false
			}
		}
		return true

	case *parser.ListNode:
		for _, item := range typed.D.Items {
			if !isStaticClassAssignmentTarget(item) {
				return false
			}
		}
		return true

	case *parser.UnpackNode:
		return isStaticClassAssignmentTarget(typed.D.Expr)

	default:
		return false
	}
}

// bindNameToScope corresponds to _bindNameToScope. The TypeScript leaves
// addedSymbols undefined; pass nil for that. It returns nil where the
// TypeScript returns undefined.
func (b *Binder) bindNameToScope(
	scope *Scope,
	node *parser.NameNode,
	addedSymbols *common.OrderedMap[string, *Symbol],
) *Symbol {
	return b.bindNameValueToScope(scope, node.D.Value, addedSymbols)
}

// bindNameValueToScope corresponds to _bindNameValueToScope.
func (b *Binder) bindNameValueToScope(
	scope *Scope,
	name string,
	addedSymbols *common.OrderedMap[string, *Symbol],
) *Symbol {
	// Is this name already bound to a scope other than the local one?
	bindingType := b.currentScope.GetBindingType(name)

	if bindingType != NameBindingTypeNone {
		var scopeToUse *Scope
		if bindingType == NameBindingTypeNonlocal {
			scopeToUse = b.currentScope.Parent
		} else {
			scopeToUse = b.currentScope.GetGlobalScope().Scope
		}
		symbolWithScope := scopeToUse.LookUpSymbolRecursive(name, nil)
		if symbolWithScope != nil {
			return symbolWithScope.Symbol
		}
	} else {
		// Don't overwrite an existing symbol.
		symbol := scope.LookUpSymbol(name)
		if symbol == nil {
			symbol = scope.AddSymbol(name, SymbolFlagsInitiallyUnbound|SymbolFlagsClassMember)

			if b.currentScope.Type == ScopeTypeModule || b.currentScope.Type == ScopeTypeBuiltin {
				if IsPrivateOrProtectedName(name) {
					if IsPrivateName(name) {
						// Private names within classes are mangled, so they are
						// always externally hidden.
						if scope.Type == ScopeTypeClass {
							symbol.SetIsExternallyHidden()
						} else {
							b.potentialPrivateSymbols.Set(name, symbol)
						}
					} else if b.currentScope.Type == ScopeTypeBuiltin {
						// Don't include private-named symbols in the builtin
						// scope.
						symbol.SetIsExternallyHidden()
					} else {
						// Defer the private/protected decision until __all__ is
						// processed so an explicit __all__ entry can promote the
						// symbol to public.
						b.potentialPrivateSymbols.Set(name, symbol)
					}
				}
			}

			if addedSymbols != nil {
				addedSymbols.Set(name, symbol)
			}
		}

		return symbol
	}

	return nil
}

// bindPossibleTupleNamedTarget corresponds to _bindPossibleTupleNamedTarget.
// The TypeScript leaves addedSymbols undefined; pass nil for that.
func (b *Binder) bindPossibleTupleNamedTarget(
	target parser.ExpressionNode,
	addedSymbols *common.OrderedMap[string, *Symbol],
) {
	if b.currentScope.Type == ScopeTypeClass && !isStaticClassAssignmentTarget(target) {
		b.currentScope.HasPotentiallyDynamicSymbolTable = true
	}

	switch typed := target.(type) {
	case *parser.NameNode:
		b.bindNameToScope(b.currentScope, typed, addedSymbols)

	case *parser.TupleNode:
		for _, expr := range typed.D.Items {
			b.bindPossibleTupleNamedTarget(expr, addedSymbols)
		}

	case *parser.ListNode:
		for _, expr := range typed.D.Items {
			b.bindPossibleTupleNamedTarget(expr, addedSymbols)
		}

	case *parser.TypeAnnotationNode:
		b.bindPossibleTupleNamedTarget(typed.D.ValueExpr, addedSymbols)

	case *parser.UnpackNode:
		b.bindPossibleTupleNamedTarget(typed.D.Expr, addedSymbols)
	}
}

// addImplicitSymbolToCurrentScope corresponds to
// _addImplicitSymbolToCurrentScope. The node is a ModuleNode, ClassNode,
// FunctionNode or LambdaNode. The TypeScript defaults isClassMember to true.
func (b *Binder) addImplicitSymbolToCurrentScope(
	nameValue string,
	node parser.ParseNode,
	intrinsicType IntrinsicType,
	isClassMember bool,
) {
	symbol := b.addSymbolToCurrentScope(nameValue, false /* isInitiallyUnbound */, isClassMember)
	if symbol != nil {
		symbol.AddDeclaration(&IntrinsicDeclaration{
			DeclarationBase: DeclarationBase{
				Type:            DeclarationTypeIntrinsic,
				Node:            node,
				Uri:             b.fileInfo.FileUri,
				Range:           common.GetEmptyRange(),
				ModuleName:      b.fileInfo.ModuleName,
				IsInExceptSuite: b.isInExceptSuite,
			},
			Name:          nameValue,
			IntrinsicType: intrinsicType,
		})
		symbol.SetIsIgnoredForProtocolMatch()
	}
}

// addSymbolToCurrentScope adds a new symbol with the specified name if it
// doesn't already exist. It corresponds to _addSymbolToCurrentScope; the
// TypeScript defaults isClassMember to true.
func (b *Binder) addSymbolToCurrentScope(nameValue string, isInitiallyUnbound bool, isClassMember bool) *Symbol {
	symbol := b.currentScope.LookUpSymbol(nameValue)

	if symbol == nil {
		symbolFlags := SymbolFlagsNone

		if isInitiallyUnbound {
			symbolFlags |= SymbolFlagsInitiallyUnbound
		}

		if b.currentScope.Type == ScopeTypeClass && isClassMember {
			symbolFlags |= SymbolFlagsClassMember
		}

		if b.fileInfo.IsStubFile && IsPrivateOrProtectedName(nameValue) {
			symbolFlags |= SymbolFlagsExternallyHidden
		}

		// Add the symbol. Assume that symbols with a default type source ID are
		// "implicit" symbols added to the scope. These are not initially
		// unbound.
		symbol = b.currentScope.AddSymbol(nameValue, symbolFlags)
	}

	return symbol
}

// createNewScope corresponds to _createNewScope. A nil parentScope, proxyScope
// or chainedModuleLevelScopeLookup stands in for the omitted optional argument.
func (b *Binder) createNewScope(
	scopeType ScopeType,
	parentScope *Scope,
	proxyScope *Scope,
	chainedModuleLevelScopeLookup ScopeChainedModuleLevelLookup,
	callback func(),
) *Scope {
	prevScope := b.currentScope
	newScope := NewScope(scopeType, parentScope, proxyScope, chainedModuleLevelScopeLookup)
	b.currentScope = newScope

	// If this scope is an execution scope, allocate a new reference map.
	isExecutionScope := scopeType == ScopeTypeBuiltin ||
		scopeType == ScopeTypeModule ||
		scopeType == ScopeTypeFunction
	prevExpressions := b.currentScopeCodeFlowExpressions

	if isExecutionScope {
		b.currentScopeCodeFlowExpressions = common.NewOrderedSet[string]()
	}

	callback()

	b.currentScopeCodeFlowExpressions = prevExpressions
	b.currentScope = prevScope

	return newScope
}

// createCellChainModuleLevelLookup corresponds to
// _createCellChainModuleLevelLookup. It returns nil where the TypeScript
// returns undefined.
//
// The chained module-level lookup is installed only on Module scope. This
// ensures it is consulted exactly once during recursive lookup -- after the
// module's own symbol table but before ascending to builtins -- when
// `useChainedModuleLevelScopes` is set by a nested evaluation context (function
// body, lambda, class header, or comprehension inside a function). Non-module
// scopes pass nil so they never trigger a redundant search.
func (b *Binder) createCellChainModuleLevelLookup() ScopeChainedModuleLevelLookup {
	if b.cellChainIndex == nil {
		return nil
	}

	cellChainIndex := b.cellChainIndex
	fileUri := b.fileInfo.FileUri

	// The callback preserves the caller's beyond-execution-scope state so that
	// hits from later cells are correctly marked.
	return func(name string, context *ChainedModuleLevelLookupContext) *SymbolWithScope {
		laterModuleNodes := cellChainIndex.GetLaterModuleNodes(fileUri)
		if laterModuleNodes == nil {
			// `?? []` in the original.
			return nil
		}

		for moduleNode := range laterModuleNodes {
			moduleScope := GetScope(moduleNode)
			if moduleScope == nil {
				continue
			}

			symbol := moduleScope.LookUpSymbol(name)
			if symbol == nil {
				continue
			}

			if context != nil && context.IsOutsideCallerModule && symbol.IsExternallyHidden() {
				continue
			}

			// Skip symbols whose only declarations are attribute assignments
			// (e.g. `self.x = ...`); these are instance-level, not module
			// globals.
			decls := symbol.GetDeclarations()
			if len(decls) > 0 && !anyDeclIsNotMemberAccessVariable(decls) {
				continue
			}

			result := &SymbolWithScope{
				Symbol: symbol,
				Scope:  moduleScope,
			}
			if context != nil {
				result.IsOutsideCallerModule = context.IsOutsideCallerModule
				result.IsBeyondExecutionScope = context.IsBeyondExecutionScope
				result.UsesNonlocalBinding = context.UsesNonlocalBinding
				result.UsesGlobalBinding = context.UsesGlobalBinding
			}
			return result
		}

		return nil
	}
}

// anyDeclIsNotMemberAccessVariable corresponds to
// `decls.some((decl) => decl.type !== DeclarationType.Variable || !decl.isDefinedByMemberAccess)`.
func anyDeclIsNotMemberAccessVariable(decls []Declaration) bool {
	for _, decl := range decls {
		variable, ok := decl.(*VariableDeclaration)
		if !ok || !variable.IsDefinedByMemberAccess {
			return true
		}
	}
	return false
}
