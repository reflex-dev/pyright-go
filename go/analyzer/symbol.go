/*
 * symbol.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Represents an association between a name and the type (or multiple types)
 * that the symbol is associated with in the program.
 *
 * Transliterated from analyzer/symbol.ts (pyright 1.1.412).
 */

package analyzer

import (
	"sync/atomic"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// SymbolFlags corresponds to the const enum of the same name.
type SymbolFlags int

const (
	SymbolFlagsNone SymbolFlags = 0

	// SymbolFlagsInitiallyUnbound indicates that the symbol is unbound at the
	// start of execution. Some symbols are initialized by the module loader,
	// so they are bound even before the first statement in the module is
	// executed.
	SymbolFlagsInitiallyUnbound SymbolFlags = 1 << 0

	// SymbolFlagsExternallyHidden indicates that the symbol is not visible
	// from other files. Used for module-level symbols.
	SymbolFlagsExternallyHidden SymbolFlags = 1 << 1

	// SymbolFlagsClassMember indicates that the symbol is a class member of a
	// class.
	SymbolFlagsClassMember SymbolFlags = 1 << 2

	// SymbolFlagsInstanceMember indicates that the symbol is an instance
	// member of a class.
	SymbolFlagsInstanceMember SymbolFlags = 1 << 3

	// SymbolFlagsSlotsMember indicates that the symbol is specified in the
	// __slots__ declaration of a class. Such symbols act like instance members
	// in some respects but are actually implemented as class members using
	// descriptor objects.
	SymbolFlagsSlotsMember SymbolFlags = 1 << 4

	// SymbolFlagsPrivateMember indicates that the symbol is considered
	// "private" to the class or module and should not be accessed outside or
	// overridden.
	SymbolFlagsPrivateMember SymbolFlags = 1 << 5

	// SymbolFlagsIgnoredForProtocolMatch indicates that the symbol is not
	// considered for protocol matching. This applies to some built-in symbols
	// like __module__.
	SymbolFlagsIgnoredForProtocolMatch SymbolFlags = 1 << 6

	// SymbolFlagsClassVar indicates that the symbol is a ClassVar, so it
	// cannot be set when accessed through a class instance.
	SymbolFlagsClassVar SymbolFlags = 1 << 7

	// SymbolFlagsInDunderAll indicates that the symbol is in __all__.
	SymbolFlagsInDunderAll SymbolFlags = 1 << 8

	// SymbolFlagsPrivatePyTypedImport indicates that the symbol is a private
	// import in a py.typed module.
	SymbolFlagsPrivatePyTypedImport SymbolFlags = 1 << 9

	// SymbolFlagsInitVar indicates that the symbol is an InitVar as specified
	// in PEP 557.
	SymbolFlagsInitVar SymbolFlags = 1 << 10

	// SymbolFlagsNamedTupleMember indicates that the symbol is a field in a
	// NamedTuple class, which is modeled as an instance member but in some
	// respects acts as a class member.
	SymbolFlagsNamedTupleMember SymbolFlags = 1 << 11

	// SymbolFlagsIgnoredForOverrideChecks indicates that the symbol should be
	// exempt from override type checks.
	SymbolFlagsIgnoredForOverrideChecks SymbolFlags = 1 << 12

	// SymbolFlagsFinalVarInClassBody indicates that the symbol is marked Final
	// and is assigned a value in the class body. The typing spec indicates
	// that these should be considered ClassVars unless they are found in a
	// dataclass.
	SymbolFlagsFinalVarInClassBody SymbolFlags = 1 << 13

	// SymbolFlagsDataClassKeywordOnly indicates that the symbol is a KW_ONLY
	// separator in a dataclass.
	SymbolFlagsDataClassKeywordOnly SymbolFlags = 1 << 14
)

// nextSymbolID backs getUniqueSymbolID. The original is a plain module-level
// counter; JavaScript is single-threaded, so it needs no synchronization.
var nextSymbolID atomic.Int64

func init() {
	nextSymbolID.Store(1)
}

func getUniqueSymbolID() int {
	return int(nextSymbolID.Add(1) - 1)
}

// IndeterminateSymbolID indicates that there is no specific symbol.
const IndeterminateSymbolID = 0

// SynthesizedTypeInfo corresponds to the interface of the same name.
type SynthesizedTypeInfo struct {
	Type Type

	// Node is not used by the type evaluator but can be used by language
	// services to provide additional functionality (such as go-to-definition).
	Node *parser.NameNode
}

// Symbol corresponds to the class of the same name.
type Symbol struct {
	// declarations holds information about the node that declared the value --
	// i.e. where the editor will take the user if "show definition" is
	// selected. Multiple declarations can exist for variables, properties, and
	// functions (in the case of @overload).
	declarations []Declaration

	// flags provide information about the symbol.
	flags SymbolFlags

	// ID is a unique numeric ID for each symbol allocated.
	ID int

	// synthesizedTypeInfo holds the type for symbols that are completely
	// synthesized (i.e. have no corresponding declarations in the program).
	synthesizedTypeInfo *SynthesizedTypeInfo

	// typingSymbolAlias records whether this symbol is an alias for a symbol
	// originally imported from the typing or typing_extensions module (e.g.
	// "Final").
	typingSymbolAlias *string
}

// NewSymbol corresponds to the Symbol constructor.
func NewSymbol(flags SymbolFlags) *Symbol {
	return &Symbol{
		ID:    getUniqueSymbolID(),
		flags: flags,
	}
}

// SymbolCreateWithType corresponds to Symbol.createWithType. A nil node stands
// in for the optional parameter.
func SymbolCreateWithType(flags SymbolFlags, t Type, node *parser.NameNode) *Symbol {
	newSymbol := NewSymbol(flags)
	newSymbol.synthesizedTypeInfo = &SynthesizedTypeInfo{Type: t, Node: node}
	return newSymbol
}

func (s *Symbol) IsInitiallyUnbound() bool {
	return (s.flags & SymbolFlagsInitiallyUnbound) != 0
}

func (s *Symbol) SetIsExternallyHidden() {
	s.flags |= SymbolFlagsExternallyHidden
}

func (s *Symbol) IsExternallyHidden() bool {
	return (s.flags & SymbolFlagsExternallyHidden) != 0
}

func (s *Symbol) SetIsIgnoredForProtocolMatch() {
	s.flags |= SymbolFlagsIgnoredForProtocolMatch
}

func (s *Symbol) IsIgnoredForProtocolMatch() bool {
	return (s.flags & SymbolFlagsIgnoredForProtocolMatch) != 0
}

func (s *Symbol) SetIsClassMember() {
	s.flags |= SymbolFlagsClassMember
}

func (s *Symbol) IsClassMember() bool {
	return (s.flags & SymbolFlagsClassMember) != 0
}

func (s *Symbol) SetIsInstanceMember() {
	s.flags |= SymbolFlagsInstanceMember
}

func (s *Symbol) IsInstanceMember() bool {
	return (s.flags & SymbolFlagsInstanceMember) != 0
}

func (s *Symbol) SetIsSlotsMember() {
	s.flags |= SymbolFlagsClassMember | SymbolFlagsInstanceMember | SymbolFlagsSlotsMember
}

func (s *Symbol) IsSlotsMember() bool {
	return (s.flags & SymbolFlagsSlotsMember) != 0
}

func (s *Symbol) SetIsClassVar() {
	s.flags |= SymbolFlagsClassVar
}

func (s *Symbol) IsClassVar() bool {
	return (s.flags & SymbolFlagsClassVar) != 0
}

func (s *Symbol) SetIsFinalVarInClassBody() {
	s.flags |= SymbolFlagsFinalVarInClassBody
}

func (s *Symbol) IsFinalVarInClassBody() bool {
	return (s.flags & SymbolFlagsFinalVarInClassBody) != 0
}

func (s *Symbol) SetIsInitVar() {
	s.flags |= SymbolFlagsInitVar
}

func (s *Symbol) IsInitVar() bool {
	return (s.flags & SymbolFlagsInitVar) != 0
}

func (s *Symbol) SetIsDataClassKeywordOnly() {
	s.flags |= SymbolFlagsDataClassKeywordOnly
}

func (s *Symbol) IsDataClassKeywordOnly() bool {
	return (s.flags & SymbolFlagsDataClassKeywordOnly) != 0
}

func (s *Symbol) SetIsInDunderAll() {
	s.flags |= SymbolFlagsInDunderAll
}

func (s *Symbol) IsInDunderAll() bool {
	return (s.flags & SymbolFlagsInDunderAll) != 0
}

func (s *Symbol) SetIsPrivateMember() {
	s.flags |= SymbolFlagsPrivateMember
}

func (s *Symbol) IsPrivateMember() bool {
	return (s.flags & SymbolFlagsPrivateMember) != 0
}

func (s *Symbol) SetPrivatePyTypedImport() {
	s.flags |= SymbolFlagsPrivatePyTypedImport
}

func (s *Symbol) IsPrivatePyTypedImport() bool {
	return (s.flags & SymbolFlagsPrivatePyTypedImport) != 0
}

// IsNamedTupleMemberMember reproduces the doubled "Member" in the original
// method name.
func (s *Symbol) IsNamedTupleMemberMember() bool {
	return (s.flags & SymbolFlagsNamedTupleMember) != 0
}

func (s *Symbol) IsIgnoredForOverrideChecks() bool {
	return (s.flags & SymbolFlagsIgnoredForOverrideChecks) != 0
}

func (s *Symbol) SetTypingSymbolAlias(aliasedName string) {
	s.typingSymbolAlias = &aliasedName
}

// GetTypingSymbolAlias returns nil where the TypeScript returns undefined.
func (s *Symbol) GetTypingSymbolAlias() *string {
	return s.typingSymbolAlias
}

func (s *Symbol) AddDeclaration(declaration Declaration) {
	if s.declarations != nil {
		// See if this node was already identified as a declaration. If so,
		// replace it. Otherwise, add it as a new declaration to the end of the
		// list.
		declIndex := -1
		for i, decl := range s.declarations {
			if AreDeclarationsSame(decl, declaration, false, false) {
				declIndex = i
				break
			}
		}

		if declIndex < 0 {
			s.declarations = append(s.declarations, declaration)

			// If there is more than one declaration for a symbol, we will
			// assume it is not a type alias.
			for _, decl := range s.declarations {
				if varDecl, ok := IsVariableDeclaration(decl); ok && varDecl.TypeAliasName != nil {
					varDecl.TypeAliasName = nil
				}
			}
		} else {
			// If the new declaration has a defined type, it should replace the
			// existing one.
			curDecl := s.declarations[declIndex]
			if HasTypeForDeclaration(declaration) {
				s.declarations[declIndex] = declaration
				curVarDecl, curIsVar := IsVariableDeclaration(curDecl)
				newVarDecl, newIsVar := IsVariableDeclaration(declaration)
				if curIsVar && newIsVar {
					if newVarDecl.InferredTypeSource == nil && curVarDecl.InferredTypeSource != nil {
						newVarDecl.InferredTypeSource = curVarDecl.InferredTypeSource
					}
				}
			} else if newVarDecl, newIsVar := IsVariableDeclaration(declaration); newIsVar {
				// If it's marked "final" or "type alias", this should be
				// reflected in the existing declaration. Likewise, if the
				// existing declaration doesn't have a type source, add it.
				if curVarDecl, curIsVar := IsVariableDeclaration(curDecl); curIsVar {
					if newVarDecl.IsFinal {
						curVarDecl.IsFinal = true
					}

					curVarDecl.TypeAliasName = newVarDecl.TypeAliasName

					if curVarDecl.InferredTypeSource == nil && newVarDecl.InferredTypeSource != nil {
						curVarDecl.InferredTypeSource = newVarDecl.InferredTypeSource
					}
				}
			}
		}
	} else {
		s.declarations = []Declaration{declaration}
	}
}

func (s *Symbol) HasDeclarations() bool {
	return len(s.declarations) > 0
}

func (s *Symbol) GetDeclarations() []Declaration {
	if s.declarations != nil {
		return s.declarations
	}
	return []Declaration{}
}

func (s *Symbol) HasTypedDeclarations() bool {
	// We'll treat a synthesized type as an implicit declaration.
	if s.synthesizedTypeInfo != nil {
		return true
	}

	for _, decl := range s.GetDeclarations() {
		if HasTypeForDeclaration(decl) {
			return true
		}
	}
	return false
}

func (s *Symbol) GetTypedDeclarations() []Declaration {
	out := []Declaration{}
	for _, decl := range s.GetDeclarations() {
		if HasTypeForDeclaration(decl) {
			out = append(out, decl)
		}
	}
	return out
}

// GetSynthesizedType returns nil where the TypeScript returns undefined.
func (s *Symbol) GetSynthesizedType() *SynthesizedTypeInfo {
	return s.synthesizedTypeInfo
}

// SymbolTable maps names to symbol information.
type SymbolTable = *common.OrderedMap[string, *Symbol]

// NewSymbolTable returns an empty symbol table, standing in for
// `new Map<string, Symbol>()`.
func NewSymbolTable() SymbolTable {
	return common.NewOrderedMap[string, *Symbol]()
}
