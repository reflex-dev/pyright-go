/*
 * types_simple.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The type categories with little or no per-instance state: Unbound, Unknown,
 * Any, Never and Module.
 *
 * Transliterated from analyzer/types.ts (pyright 1.1.412). Split out of
 * types.go only so no single Go file has to carry all 4000 lines.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common/uri"
)

// ---------------------------------------------------------------------------
// UnboundType
// ---------------------------------------------------------------------------

// UnboundType corresponds to the interface of the same name.
type UnboundType struct {
	TypeBase
}

func (t *UnboundType) cloneSelf() Type {
	clone := *t
	clone.cloneBaseInto()
	return &clone
}

func (t *UnboundType) isUnionable() {}

// unboundInstance is the shared instance. All Unbound objects are the same.
var unboundInstance = &UnboundType{
	TypeBase: TypeBase{
		Category: TypeCategoryUnbound,
		Flags:    TypeFlagsInstantiable | TypeFlagsInstance,
	},
}

// UnboundTypeCreate corresponds to UnboundType.create.
func UnboundTypeCreate() *UnboundType {
	// All Unbound objects are the same, so use a shared instance.
	return unboundInstance
}

// UnboundTypeConvertToInstance corresponds to UnboundType.convertToInstance.
func UnboundTypeConvertToInstance(t *UnboundType) *UnboundType {
	// Remove the "special form" if present. Otherwise return the existing type.
	if t.Props != nil && t.Props.SpecialForm != nil {
		return UnboundTypeCreate()
	}
	return t
}

// ---------------------------------------------------------------------------
// UnknownType
// ---------------------------------------------------------------------------

// UnknownDetailsPriv corresponds to the interface of the same name.
type UnknownDetailsPriv struct {
	// IsIncomplete indicates whether the type is a placeholder for an
	// incomplete type during code flow analysis.
	IsIncomplete bool

	// PossibleType is a form of a "weak union" where the actual type is
	// unknown, but it could be one of the subtypes in the union. This is used
	// for overload matching in cases where more than one overload matches due
	// to an argument that evaluates to Any or Unknown.
	PossibleType Type
}

// UnknownType corresponds to the interface of the same name.
type UnknownType struct {
	TypeBase
	Priv UnknownDetailsPriv
}

func (t *UnknownType) cloneSelf() Type {
	clone := *t
	clone.cloneBaseInto()
	return &clone
}

func (t *UnknownType) isUnionable() {}

var unknownInstance = &UnknownType{
	TypeBase: TypeBase{
		Category: TypeCategoryUnknown,
		Flags:    TypeFlagsInstantiable | TypeFlagsInstance,
	},
	Priv: UnknownDetailsPriv{IsIncomplete: false},
}

var unknownIncompleteInstance = &UnknownType{
	TypeBase: TypeBase{
		Category: TypeCategoryUnknown,
		Flags:    TypeFlagsInstantiable | TypeFlagsInstance,
	},
	Priv: UnknownDetailsPriv{IsIncomplete: true},
}

// UnknownTypeCreate corresponds to UnknownType.create. The TypeScript defaults
// isIncomplete to false.
func UnknownTypeCreate(isIncomplete bool) *UnknownType {
	if isIncomplete {
		return unknownIncompleteInstance
	}
	return unknownInstance
}

// UnknownTypeCreatePossibleType corresponds to UnknownType.createPossibleType.
func UnknownTypeCreatePossibleType(possibleType Type, isIncomplete bool) *UnknownType {
	return &UnknownType{
		TypeBase: TypeBase{
			Category: TypeCategoryUnknown,
			Flags:    TypeFlagsInstantiable | TypeFlagsInstance,
		},
		Priv: UnknownDetailsPriv{
			IsIncomplete: isIncomplete,
			PossibleType: possibleType,
		},
	}
}

// UnknownTypeConvertToInstance corresponds to UnknownType.convertToInstance.
func UnknownTypeConvertToInstance(t *UnknownType) *UnknownType {
	// Remove the "special form" if present. Otherwise return the existing type.
	if t.Props != nil && t.Props.SpecialForm != nil {
		return UnknownTypeCreate(t.Priv.IsIncomplete)
	}
	return t
}

// ---------------------------------------------------------------------------
// ModuleType
// ---------------------------------------------------------------------------

// ModuleDetailsPriv corresponds to the interface of the same name.
type ModuleDetailsPriv struct {
	Fields    SymbolTable
	DocString *string

	// NotPresentFieldType controls whether the type of a field that is not
	// found should be Any/Unknown or treated as an error. The TypeScript
	// types it as `AnyType | UnknownType | undefined`.
	NotPresentFieldType Type

	// LoaderFields holds symbols that were injected by the module loader. We
	// keep these separate so we don't pollute the symbols exported by the
	// module itself.
	LoaderFields SymbolTable

	// ModuleName is the period-delimited import name of this module.
	ModuleName string

	FileUri uri.Uri
}

// ModuleType corresponds to the interface of the same name.
type ModuleType struct {
	TypeBase
	Priv ModuleDetailsPriv
}

func (t *ModuleType) cloneSelf() Type {
	clone := *t
	clone.cloneBaseInto()
	return &clone
}

func (t *ModuleType) isUnionable() {}

// ModuleTypeCreate corresponds to ModuleType.create. A nil symbolTable stands
// in for the optional parameter.
//
// Note the flags: the original writes
// `TypeFlags.Instantiable | TypeFlags.Instantiable`, using Instantiable twice
// rather than combining it with Instance. That is reproduced verbatim; the
// result is simply TypeFlags.Instantiable. See UPSTREAM-BUGS.md #5.
func ModuleTypeCreate(moduleName string, fileUri uri.Uri, symbolTable SymbolTable) *ModuleType {
	fields := symbolTable
	if fields == nil {
		fields = NewSymbolTable()
	}

	return &ModuleType{
		TypeBase: TypeBase{
			Category: TypeCategoryModule,
			Flags:    TypeFlagsInstantiable | TypeFlagsInstantiable,
		},
		Priv: ModuleDetailsPriv{
			Fields:       fields,
			LoaderFields: NewSymbolTable(),
			ModuleName:   moduleName,
			FileUri:      fileUri,
		},
	}
}

// ModuleTypeGetField corresponds to ModuleType.getField.
func ModuleTypeGetField(moduleType *ModuleType, name string) *Symbol {
	// Always look for the symbol in the module's fields before consulting the
	// loader fields. The loader runs before the module, so its values will be
	// overwritten by the module.
	symbol, _ := moduleType.Priv.Fields.Get(name)

	if moduleType.Priv.LoaderFields != nil {
		if symbol == nil {
			symbol, _ = moduleType.Priv.LoaderFields.Get(name)
		} else if len(symbol.GetDeclarations()) == 1 {
			// If the symbol is hidden when accessed via the module but is also
			// accessible through a loader field, use the latter so it isn't
			// flagged as an error.
			loaderSymbol, _ := moduleType.Priv.LoaderFields.Get(name)
			if loaderSymbol != nil && !loaderSymbol.IsExternallyHidden() {
				symbol = loaderSymbol
			}
		}
	}
	return symbol
}
