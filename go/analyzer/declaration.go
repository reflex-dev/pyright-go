/*
 * declaration.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Tracks the location within the code where a named entity is declared and its
 * associated declared type (if the type is explicitly declared).
 *
 * Transliterated from analyzer/declaration.ts (pyright 1.1.412).
 *
 * The `Declaration` discriminated union becomes an interface plus nine structs
 * embedding DeclarationBase, the same shape the parse node union uses. The
 * `decl is XDeclaration` type guards become type assertions, wrapped in the
 * IsXDeclaration helpers so call sites read like the originals.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/parser"
)

// UnresolvedModuleMarker marks an alias declaration whose module could not be
// resolved. It is a ConstantUri, so it compares by reference: no real file URI
// can ever equal it.
var UnresolvedModuleMarker = uri.Constant("*** unresolved module ***")

// DeclarationType corresponds to the const enum of the same name.
type DeclarationType int

const (
	DeclarationTypeIntrinsic DeclarationType = iota
	DeclarationTypeVariable
	DeclarationTypeParam
	DeclarationTypeTypeParam
	DeclarationTypeTypeAlias
	DeclarationTypeFunction
	DeclarationTypeClass
	DeclarationTypeSpecialBuiltInClass
	DeclarationTypeAlias
)

// IntrinsicType corresponds to the string union of the same name.
type IntrinsicType = string

const (
	IntrinsicTypeAny                IntrinsicType = "Any"
	IntrinsicTypeStr                IntrinsicType = "str"
	IntrinsicTypeStrOrNone          IntrinsicType = "str | None"
	IntrinsicTypeInt                IntrinsicType = "int"
	IntrinsicTypeMutableSequenceStr IntrinsicType = "MutableSequence[str]"
	IntrinsicTypeDunderClass        IntrinsicType = "__class__"
	IntrinsicTypeDictStrAny         IntrinsicType = "dict[str, Any]"
)

// DeclarationBase holds the fields common to every declaration form.
type DeclarationBase struct {
	// Type is the category of this symbol (function, variable, etc.). Used by
	// the hover provider to display helpful text.
	Type DeclarationType

	// Node is the parse node associated with the declaration. It does not
	// necessarily match the path and range.
	Node parser.ParseNode

	// Uri and Range are the file and range within that file that contains the
	// declaration. Unless this is an alias, in which case Uri refers to the
	// file the alias is referring to.
	Uri   uri.Uri
	Range common.Range

	// ModuleName is the dot-separated import name for the file that contains
	// the declaration. It may not be definitive, because a source file can be
	// accessed via different import names in some cases.
	ModuleName string

	// IsInExceptSuite reports that the declaration is within an except clause
	// of a try statement. We may want to ignore such declarations.
	IsInExceptSuite bool

	// IsInInlinedTypedDict reports that this declaration is within an inlined
	// TypedDict definition.
	IsInInlinedTypedDict bool
}

// Declaration corresponds to the union of the same name.
type Declaration interface {
	// DeclBase returns the embedded DeclarationBase, standing in for reading
	// the shared fields off the union.
	DeclBase() *DeclarationBase
}

// DeclBase satisfies Declaration for every form through embedding.
func (d *DeclarationBase) DeclBase() *DeclarationBase { return d }

// IntrinsicDeclaration corresponds to the interface of the same name.
//
// Node is typed ParseNode rather than the original's
// `ModuleNode | FunctionNode | LambdaNode | ClassNode`; the narrower union has
// no Go counterpart and every read of it re-narrows anyway.
type IntrinsicDeclaration struct {
	DeclarationBase
	Name          string
	IntrinsicType IntrinsicType
}

// ClassDeclaration corresponds to the interface of the same name. Node is a
// ClassNode.
type ClassDeclaration struct {
	DeclarationBase
}

// SpecialBuiltInClassDeclaration is used only for a few special built-in class
// types defined in typing.pyi. Node is a TypeAnnotationNode.
type SpecialBuiltInClassDeclaration struct {
	DeclarationBase
}

// FunctionDeclaration corresponds to the interface of the same name. Node is a
// FunctionNode.
type FunctionDeclaration struct {
	DeclarationBase
	IsMethod         bool
	IsGenerator      bool
	ReturnStatements []*parser.ReturnNode
	// YieldStatements holds `(YieldNode | YieldFromNode)[]`.
	YieldStatements []parser.ParseNode
	RaiseStatements []*parser.RaiseNode
}

// ParamDeclaration corresponds to the interface of the same name. Node is a
// ParameterNode.
type ParamDeclaration struct {
	DeclarationBase

	// InferredName is the actual 'name' as the user thinks of it. Inferred
	// parameters can be inferred from pieces of an actual NameNode.
	InferredName string

	// InferredTypeNodes are the nodes that potentially make up the type of an
	// inferred parameter.
	InferredTypeNodes []parser.ExpressionNode
}

// TypeParamDeclaration corresponds to the interface of the same name. Node is
// a TypeParameterNode.
type TypeParamDeclaration struct {
	DeclarationBase
}

// TypeAliasDeclaration corresponds to the interface of the same name. Node is
// a TypeAliasNode.
type TypeAliasDeclaration struct {
	DeclarationBase

	// DocString holds a docstring (based on PEP 258) if present.
	DocString *string
}

// VariableDeclaration corresponds to the interface of the same name. Node is a
// NameNode or StringListNode.
type VariableDeclaration struct {
	DeclarationBase

	// TypeAnnotationNode is an explicit type annotation, if provided.
	TypeAnnotationNode parser.ExpressionNode

	// InferredTypeSource is a source of the inferred type.
	InferredTypeSource parser.ParseNode

	// IsConstant reports whether the declaration is considered "constant"
	// (i.e. reassignment is not permitted).
	IsConstant bool

	// IsFinal reports whether the declaration is considered "final" (similar
	// to constant in that reassignment is not permitted).
	IsFinal bool

	// IsDefinedBySlots reports whether the declaration is an entry in
	// __slots__.
	IsDefinedBySlots bool

	// IsInferenceAllowedInPyTyped permits inference for symbols in a "py.typed"
	// file where it is normally disallowed, as with __match_args__ or
	// __slots__.
	IsInferenceAllowedInPyTyped bool

	// IsRuntimeTypeExpression reports whether the declaration uses a
	// runtime-evaluated type expression rather than an annotation. This is
	// used for TypedDicts, NamedTuples, and other complex (more dynamic) class
	// definitions with typed variables.
	IsRuntimeTypeExpression bool

	// TypeAliasName points to the alias name, if the declaration is a type
	// alias.
	TypeAliasName *parser.NameNode

	// IsDefinedByMemberAccess reports whether the declaration is a class or
	// instance variable defined by a member access, rather than a direct
	// variable declaration within the class.
	IsDefinedByMemberAccess bool

	// DocString holds an "attribute docstring" (as defined in PEP 258) if
	// present.
	DocString *string

	// AlternativeTypeNode, if set, indicates an alternative node to use to
	// determine the type of the variable.
	AlternativeTypeNode parser.ExpressionNode

	// IsExplicitBinding reports whether the declaration is an assignment
	// through an explicit nonlocal or global binding.
	IsExplicitBinding bool
}

// AliasDeclaration is used for imports. They are resolved after the binding
// phase. Node is an ImportAsNode, ImportFromAsNode or ImportFromNode.
type AliasDeclaration struct {
	DeclarationBase

	// UsesLocalName reports whether this declaration uses a local name or uses
	// the imported symbol directly. This is used to find and rename
	// references.
	UsesLocalName bool

	// LoadSymbolsFromPath indicates whether symbols can be loaded from the
	// path.
	LoadSymbolsFromPath bool

	// SymbolName is the name of the symbol being imported. Used for
	// "from X import Y" statements; not applicable to "import X" statements.
	SymbolName *string

	// SubmoduleFallback handles the case where a symbol name can't be resolved
	// within the target module (defined by "path") but might refer to a
	// submodule with the same name.
	SubmoduleFallback *AliasDeclaration

	// FirstNamePart is the first part of the multi-part name used in the
	// import statement (e.g. for "import a.b.c", FirstNamePart would be "a").
	FirstNamePart *string

	// ImplicitImports holds other modules that also need to be resolved and
	// inserted implicitly into the module's namespace to emulate the behavior
	// of the python module loader, when the alias targets a module. This can
	// be recursive (e.g. in the case of an "import a.b.c.d" statement).
	ImplicitImports *common.OrderedMap[string, *ModuleLoaderActions]

	// IsUnresolved reports whether this is a dummy entry for an unresolved
	// import.
	IsUnresolved bool

	// IsNativeLib reports whether this is a dummy entry for an import that
	// cannot be resolved directly because it targets a native library.
	IsNativeLib bool

	// IsLazy reports whether this import was declared with the "lazy" keyword
	// (PEP 810).
	IsLazy bool
}

// ModuleLoaderActions represents a set of actions that the python loader
// performs when a module import is encountered.
type ModuleLoaderActions struct {
	// Uri is the resolved uri of the implicit import. This can be empty if the
	// resolved uri doesn't reference a module (e.g. it's a directory).
	Uri uri.Uri

	// IsUnresolved reports whether this is a dummy entry for an unresolved
	// import.
	IsUnresolved bool

	// LoadSymbolsFromPath indicates whether symbols can be loaded from the
	// path.
	LoadSymbolsFromPath bool

	// ImplicitImports mirrors the field of the same name in AliasDeclaration.
	ImplicitImports *common.OrderedMap[string, *ModuleLoaderActions]
}

func IsFunctionDeclaration(decl Declaration) (*FunctionDeclaration, bool) {
	d, ok := decl.(*FunctionDeclaration)
	return d, ok
}

func IsClassDeclaration(decl Declaration) (*ClassDeclaration, bool) {
	d, ok := decl.(*ClassDeclaration)
	return d, ok
}

func IsParamDeclaration(decl Declaration) (*ParamDeclaration, bool) {
	d, ok := decl.(*ParamDeclaration)
	return d, ok
}

func IsTypeParamDeclaration(decl Declaration) (*TypeParamDeclaration, bool) {
	d, ok := decl.(*TypeParamDeclaration)
	return d, ok
}

func IsTypeAliasDeclaration(decl Declaration) (*TypeAliasDeclaration, bool) {
	d, ok := decl.(*TypeAliasDeclaration)
	return d, ok
}

func IsVariableDeclaration(decl Declaration) (*VariableDeclaration, bool) {
	d, ok := decl.(*VariableDeclaration)
	return d, ok
}

func IsAliasDeclaration(decl Declaration) (*AliasDeclaration, bool) {
	d, ok := decl.(*AliasDeclaration)
	return d, ok
}

func IsSpecialBuiltInClassDeclaration(decl Declaration) (*SpecialBuiltInClassDeclaration, bool) {
	d, ok := decl.(*SpecialBuiltInClassDeclaration)
	return d, ok
}

func IsIntrinsicDeclaration(decl Declaration) (*IntrinsicDeclaration, bool) {
	d, ok := decl.(*IntrinsicDeclaration)
	return d, ok
}

func IsUnresolvedAliasDeclaration(decl Declaration) bool {
	if _, ok := IsAliasDeclaration(decl); !ok {
		return false
	}
	return decl.DeclBase().Uri.Equals(UnresolvedModuleMarker)
}
