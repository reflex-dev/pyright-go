/*
 * scope.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Represents an evaluation scope and its defined symbols. It also contains a
 * link to a parent scope (except for the top-most built-in scope).
 *
 * Transliterated from analyzer/scope.ts (pyright 1.1.412).
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
)

// ScopeType corresponds to the const enum of the same name.
type ScopeType int

const (
	// ScopeTypeTypeParameter is used for PEP 695-style type parameters.
	ScopeTypeTypeParameter ScopeType = iota

	// ScopeTypeComprehension is used for comprehension nodes.
	ScopeTypeComprehension

	// ScopeTypeFunction scopes are used for lambdas and functions.
	ScopeTypeFunction

	// ScopeTypeClass scopes are used for classes.
	ScopeTypeClass

	// ScopeTypeModule scopes are used for modules.
	ScopeTypeModule

	// ScopeTypeBuiltin scopes are used for all ambient symbols provided by the
	// Python environment.
	ScopeTypeBuiltin
)

// NameBindingType corresponds to the const enum of the same name.
//
// The TypeScript stores this in a `Map<string, NameBindingType>` where a
// missing key is distinguishable from either value. NameBindingTypeNone stands
// in for that missing key so getBindingType can return a single value.
type NameBindingType int

const (
	// NameBindingTypeNone stands in for `undefined`; it has no counterpart in
	// the original enum.
	NameBindingTypeNone NameBindingType = iota

	// NameBindingTypeNonlocal is a binding made with the "nonlocal" keyword.
	NameBindingTypeNonlocal

	// NameBindingTypeGlobal is a binding made with the "global" keyword.
	NameBindingTypeGlobal
)

// SymbolWithScope provides information for recursive scope lookups.
type SymbolWithScope struct {
	// Symbol is the found symbol.
	Symbol *Symbol

	// Scope is the scope in which the symbol was found.
	Scope *Scope

	// IsOutsideCallerModule indicates that the recursion needed to proceed
	// outside of the module's scope into the builtins scope.
	IsOutsideCallerModule bool

	// IsBeyondExecutionScope indicates that the recursion needed to proceed to
	// a scope that is beyond the current execution scope. An execution scope is
	// defined as a function, module, or lambda. Classes are not considered
	// execution scopes because they are "executed" immediately as part of the
	// scope in which they are contained.
	IsBeyondExecutionScope bool

	// UsesNonlocalBinding and UsesGlobalBinding record that the symbol was
	// accessed through a nonlocal or global binding.
	UsesNonlocalBinding bool
	UsesGlobalBinding   bool
}

// GlobalScopeResult corresponds to the interface of the same name.
type GlobalScopeResult struct {
	Scope                  *Scope
	IsBeyondExecutionScope bool
}

// LookupSymbolOptions corresponds to the interface of the same name. Every
// field is optional in the original and defaults to false, which is the Go zero
// value, so a nil *LookupSymbolOptions and a zero value behave alike.
type LookupSymbolOptions struct {
	IsOutsideCallerModule       bool
	IsBeyondExecutionScope      bool
	UseProxyScope               bool
	UseChainedModuleLevelScopes bool
	UsesNonlocalBinding         bool
	UsesGlobalBinding           bool
}

// ChainedModuleLevelLookupContext corresponds to the interface of the same
// name.
type ChainedModuleLevelLookupContext struct {
	IsOutsideCallerModule  bool
	IsBeyondExecutionScope bool
	UsesNonlocalBinding    bool
	UsesGlobalBinding      bool
}

// ScopeChainedModuleLevelLookup corresponds to the type of the same name. It
// returns nil where the TypeScript returns undefined.
type ScopeChainedModuleLevelLookup func(name string, context *ChainedModuleLevelLookupContext) *SymbolWithScope

// Scope corresponds to the class of the same name.
type Scope struct {
	// Type is the scope type, as defined in the enumeration.
	Type ScopeType

	// Parent is the next scope in the hierarchy, or nil if it's the top-most
	// scope.
	Parent *Scope

	// Proxy is an alternate parent scope that can be used to resolve symbols in
	// certain contexts. Used for TypeParam scopes.
	Proxy *Scope

	// ChainedModuleLevelScopeLookup is an optional lookup for chained
	// module-level scopes (notebook cells), consulted after the current scope
	// is checked.
	ChainedModuleLevelScopeLookup ScopeChainedModuleLevelLookup

	// SymbolTable is the association between names and symbols.
	SymbolTable SymbolTable

	// NotLocalBindings holds names within this scope that are bound to other
	// scopes (either nonlocal or global).
	NotLocalBindings *common.OrderedMap[string, NameBindingType]

	// SlotsNames holds names defined by __slots__ within this scope (used only
	// for class scopes).
	SlotsNames []string

	// HasNonEmptySlots indicates that __slots__ definitely contains at least
	// one name, even if the complete set of names is unknown.
	HasNonEmptySlots bool

	// HasPotentiallyDynamicSymbolTable indicates that the class body contains
	// an assignment target that cannot be represented as a statically-known
	// class symbol or exposes its namespace to runtime mutation.
	HasPotentiallyDynamicSymbolTable bool
}

// NewScope corresponds to the Scope constructor. A nil parent, proxy or
// chainedModuleLevelScopeLookup stands in for the omitted optional argument.
func NewScope(
	scopeType ScopeType,
	parent *Scope,
	proxy *Scope,
	chainedModuleLevelScopeLookup ScopeChainedModuleLevelLookup,
) *Scope {
	return &Scope{
		Type:                          scopeType,
		Parent:                        parent,
		Proxy:                         proxy,
		ChainedModuleLevelScopeLookup: chainedModuleLevelScopeLookup,
		SymbolTable:                   NewSymbolTable(),
		NotLocalBindings:              common.NewOrderedMap[string, NameBindingType](),
	}
}

// GetGlobalScope corresponds to Scope.getGlobalScope.
func (s *Scope) GetGlobalScope() GlobalScopeResult {
	curScope := s
	isBeyondExecutionScope := false

	for curScope != nil {
		if curScope.Type == ScopeTypeModule || curScope.Type == ScopeTypeBuiltin {
			return GlobalScopeResult{Scope: curScope, IsBeyondExecutionScope: isBeyondExecutionScope}
		}

		if curScope.Type == ScopeTypeFunction {
			isBeyondExecutionScope = true
		}

		curScope = curScope.Parent
	}

	fail("failed to find scope")
	return GlobalScopeResult{Scope: s, IsBeyondExecutionScope: isBeyondExecutionScope}
}

// IsIndependentlyExecutable reports whether the scope is executed independently
// of its parent scope. Classes are executed in the context of their parent
// scope, so they don't fit this category.
func (s *Scope) IsIndependentlyExecutable() bool {
	return s.Type == ScopeTypeModule || s.Type == ScopeTypeFunction
}

// LookUpSymbol corresponds to Scope.lookUpSymbol. It returns nil where the
// TypeScript returns undefined.
func (s *Scope) LookUpSymbol(name string) *Symbol {
	symbol, _ := s.SymbolTable.Get(name)
	return symbol
}

// LookUpSymbolRecursive corresponds to Scope.lookUpSymbolRecursive. A nil
// options stands in for the omitted optional argument.
func (s *Scope) LookUpSymbolRecursive(name string, options *LookupSymbolOptions) *SymbolWithScope {
	if options == nil {
		options = &LookupSymbolOptions{}
	}

	effectiveScope := s
	symbol, _ := s.SymbolTable.Get(name)

	if symbol == nil && options.UseProxyScope && s.Proxy != nil {
		symbol, _ = s.Proxy.SymbolTable.Get(name)
		effectiveScope = s.Proxy
	}

	if symbol != nil {
		// If we're searching outside of the original caller's module (global)
		// scope, hide any names that are not meant to be visible to importers.
		if options.IsOutsideCallerModule && symbol.IsExternallyHidden() {
			return nil
		}

		// If the symbol is a class variable that is defined only in terms of
		// member accesses, it is not accessible directly by name, so hide it.
		decls := symbol.GetDeclarations()
		accessibleByName := len(decls) == 0
		for _, decl := range decls {
			varDecl, isVar := IsVariableDeclaration(decl)
			if !isVar || !varDecl.IsDefinedByMemberAccess {
				accessibleByName = true
				break
			}
		}

		if accessibleByName {
			return &SymbolWithScope{
				Symbol:                 symbol,
				IsOutsideCallerModule:  options.IsOutsideCallerModule,
				IsBeyondExecutionScope: options.IsBeyondExecutionScope,
				Scope:                  effectiveScope,
				UsesNonlocalBinding:    options.UsesNonlocalBinding,
				UsesGlobalBinding:      options.UsesGlobalBinding,
			}
		}
	}

	var parentScope *Scope
	isNextScopeBeyondExecutionScope := options.IsBeyondExecutionScope || s.IsIndependentlyExecutable()

	notLocalBinding, _ := s.NotLocalBindings.Get(name)
	if notLocalBinding == NameBindingTypeGlobal {
		globalScopeResult := s.GetGlobalScope()
		if globalScopeResult.Scope != s {
			parentScope = globalScopeResult.Scope
			if globalScopeResult.IsBeyondExecutionScope {
				isNextScopeBeyondExecutionScope = true
			}
		}
	} else {
		parentScope = s.Parent
	}

	if options.UseChainedModuleLevelScopes && s.ChainedModuleLevelScopeLookup != nil {
		fallbackResult := s.ChainedModuleLevelScopeLookup(name, &ChainedModuleLevelLookupContext{
			IsOutsideCallerModule:  options.IsOutsideCallerModule,
			IsBeyondExecutionScope: isNextScopeBeyondExecutionScope,
			UsesNonlocalBinding:    notLocalBinding == NameBindingTypeNonlocal || options.UsesNonlocalBinding,
			UsesGlobalBinding:      notLocalBinding == NameBindingTypeGlobal || options.UsesGlobalBinding,
		})

		if fallbackResult != nil {
			return fallbackResult
		}
	}

	if parentScope != nil {
		// If our recursion is about to take us outside the scope of the current
		// module (i.e. into a built-in scope), indicate as such with the second
		// parameter.
		return parentScope.LookUpSymbolRecursive(name, &LookupSymbolOptions{
			IsOutsideCallerModule:       options.IsOutsideCallerModule || s.Type == ScopeTypeModule,
			IsBeyondExecutionScope:      isNextScopeBeyondExecutionScope,
			UseChainedModuleLevelScopes: options.UseChainedModuleLevelScopes,
			UsesNonlocalBinding:         notLocalBinding == NameBindingTypeNonlocal || options.UsesNonlocalBinding,
			UsesGlobalBinding:           notLocalBinding == NameBindingTypeGlobal || options.UsesGlobalBinding,
		})
	}

	return nil
}

// AddSymbol corresponds to Scope.addSymbol.
func (s *Scope) AddSymbol(name string, flags SymbolFlags) *Symbol {
	symbol := NewSymbol(flags)
	s.SymbolTable.Set(name, symbol)
	return symbol
}

// GetBindingType corresponds to Scope.getBindingType. It returns
// NameBindingTypeNone where the TypeScript returns undefined.
func (s *Scope) GetBindingType(name string) NameBindingType {
	bindingType, _ := s.NotLocalBindings.Get(name)
	return bindingType
}

// SetBindingType corresponds to Scope.setBindingType.
func (s *Scope) SetBindingType(name string, bindingType NameBindingType) {
	s.NotLocalBindings.Set(name, bindingType)
}

// SetSlotsNames corresponds to Scope.setSlotsNames.
func (s *Scope) SetSlotsNames(names []string) {
	s.SlotsNames = names
}

// GetSlotsNames corresponds to Scope.getSlotsNames. It returns nil where the
// TypeScript returns undefined.
func (s *Scope) GetSlotsNames() []string {
	return s.SlotsNames
}

// SetHasNonEmptySlots corresponds to Scope.setHasNonEmptySlots.
func (s *Scope) SetHasNonEmptySlots(value bool) {
	s.HasNonEmptySlots = value
}
