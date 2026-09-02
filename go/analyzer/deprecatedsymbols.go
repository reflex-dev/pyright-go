/*
 * deprecatedsymbols.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/deprecatedSymbols.ts (pyright 1.1.412).
 *
 * The original's comment: a list of implicitly-deprecated symbols as defined in
 * PEP 585, etc.
 *
 * These are the typing aliases that PEP 585 and PEP 604 superseded -- `Tuple`
 * for `tuple`, `Optional[X]` for `X | None`. They are not marked deprecated in
 * typeshed, so the table is the only record that they are, and the Python
 * version is part of each entry because the replacement only exists from that
 * version on.
 *
 * TypingImportOnly separates the two halves of the problem. `Callable` is
 * deprecated when imported from `typing` but perfectly current when imported
 * from `collections.abc`, so for those entries the diagnostic depends on where
 * the name came from rather than on what it resolves to.
 *
 * Generated from the original's two Map literals; see the tables' order, which
 * is preserved.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
)

// DeprecatedForm corresponds to the interface of the same name.
type DeprecatedForm struct {
	// Version is the version of Python where this symbol becomes deprecated.
	Version common.PythonVersion

	// FullName is the full name of the deprecated type.
	FullName string

	// ReplacementText is the replacement form.
	ReplacementText string

	// TypingImportOnly indicates that the symbol is deprecated only if imported
	// from `typing`.
	TypingImportOnly bool
}

var deprecatedAliases = map[string]*DeprecatedForm{
	"Tuple":               {Version: common.PythonVersion3_9, FullName: "builtins.tuple", ReplacementText: "tuple"},
	"List":                {Version: common.PythonVersion3_9, FullName: "builtins.list", ReplacementText: "list"},
	"Dict":                {Version: common.PythonVersion3_9, FullName: "builtins.dict", ReplacementText: "dict"},
	"Set":                 {Version: common.PythonVersion3_9, FullName: "builtins.set", ReplacementText: "set"},
	"FrozenSet":           {Version: common.PythonVersion3_9, FullName: "builtins.frozenset", ReplacementText: "frozenset"},
	"Type":                {Version: common.PythonVersion3_9, FullName: "builtins.type", ReplacementText: "type"},
	"Deque":               {Version: common.PythonVersion3_9, FullName: "collections.deque", ReplacementText: "collections.deque"},
	"DefaultDict":         {Version: common.PythonVersion3_9, FullName: "collections.defaultdict", ReplacementText: "collections.defaultdict"},
	"OrderedDict":         {Version: common.PythonVersion3_9, FullName: "collections.OrderedDict", ReplacementText: "collections.OrderedDict", TypingImportOnly: true},
	"Counter":             {Version: common.PythonVersion3_9, FullName: "collections.Counter", ReplacementText: "collections.Counter", TypingImportOnly: true},
	"ChainMap":            {Version: common.PythonVersion3_9, FullName: "collections.ChainMap", ReplacementText: "collections.ChainMap", TypingImportOnly: true},
	"Awaitable":           {Version: common.PythonVersion3_9, FullName: "typing.Awaitable", ReplacementText: "collections.abc.Awaitable", TypingImportOnly: true},
	"Coroutine":           {Version: common.PythonVersion3_9, FullName: "typing.Coroutine", ReplacementText: "collections.abc.Coroutine", TypingImportOnly: true},
	"AsyncIterable":       {Version: common.PythonVersion3_9, FullName: "typing.AsyncIterable", ReplacementText: "collections.abc.AsyncIterable", TypingImportOnly: true},
	"AsyncIterator":       {Version: common.PythonVersion3_9, FullName: "typing.AsyncIterator", ReplacementText: "collections.abc.AsyncIterator", TypingImportOnly: true},
	"AsyncGenerator":      {Version: common.PythonVersion3_9, FullName: "typing.AsyncGenerator", ReplacementText: "collections.abc.AsyncGenerator", TypingImportOnly: true},
	"Iterable":            {Version: common.PythonVersion3_9, FullName: "typing.Iterable", ReplacementText: "collections.abc.Iterable", TypingImportOnly: true},
	"Iterator":            {Version: common.PythonVersion3_9, FullName: "typing.Iterator", ReplacementText: "collections.abc.Iterator", TypingImportOnly: true},
	"Generator":           {Version: common.PythonVersion3_9, FullName: "typing.Generator", ReplacementText: "collections.abc.Generator", TypingImportOnly: true},
	"Reversible":          {Version: common.PythonVersion3_9, FullName: "typing.Reversible", ReplacementText: "collections.abc.Reversible", TypingImportOnly: true},
	"Container":           {Version: common.PythonVersion3_9, FullName: "typing.Container", ReplacementText: "collections.abc.Container", TypingImportOnly: true},
	"Collection":          {Version: common.PythonVersion3_9, FullName: "typing.Collection", ReplacementText: "collections.abc.Collection", TypingImportOnly: true},
	"AbstractSet":         {Version: common.PythonVersion3_9, FullName: "typing.AbstractSet", ReplacementText: "collections.abc.Set", TypingImportOnly: true},
	"MutableSet":          {Version: common.PythonVersion3_9, FullName: "typing.MutableSet", ReplacementText: "collections.abc.MutableSet", TypingImportOnly: true},
	"Mapping":             {Version: common.PythonVersion3_9, FullName: "typing.Mapping", ReplacementText: "collections.abc.Mapping", TypingImportOnly: true},
	"MutableMapping":      {Version: common.PythonVersion3_9, FullName: "typing.MutableMapping", ReplacementText: "collections.abc.MutableMapping", TypingImportOnly: true},
	"Sequence":            {Version: common.PythonVersion3_9, FullName: "typing.Sequence", ReplacementText: "collections.abc.Sequence", TypingImportOnly: true},
	"MutableSequence":     {Version: common.PythonVersion3_9, FullName: "typing.MutableSequence", ReplacementText: "collections.abc.MutableSequence", TypingImportOnly: true},
	"ByteString":          {Version: common.PythonVersion3_9, FullName: "typing.ByteString", ReplacementText: "collections.abc.ByteString", TypingImportOnly: true},
	"MappingView":         {Version: common.PythonVersion3_9, FullName: "typing.MappingView", ReplacementText: "collections.abc.MappingView", TypingImportOnly: true},
	"KeysView":            {Version: common.PythonVersion3_9, FullName: "typing.KeysView", ReplacementText: "collections.abc.KeysView", TypingImportOnly: true},
	"ItemsView":           {Version: common.PythonVersion3_9, FullName: "typing.ItemsView", ReplacementText: "collections.abc.ItemsView", TypingImportOnly: true},
	"ValuesView":          {Version: common.PythonVersion3_9, FullName: "typing.ValuesView", ReplacementText: "collections.abc.ValuesView", TypingImportOnly: true},
	"ContextManager":      {Version: common.PythonVersion3_9, FullName: "typing.ContextManager", ReplacementText: "contextlib.AbstractContextManager"},
	"AsyncContextManager": {Version: common.PythonVersion3_9, FullName: "typing.AsyncContextManager", ReplacementText: "contextlib.AbstractAsyncContextManager"},
	"Pattern":             {Version: common.PythonVersion3_9, FullName: "re.Pattern", ReplacementText: "re.Pattern", TypingImportOnly: true},
	"Match":               {Version: common.PythonVersion3_9, FullName: "re.Match", ReplacementText: "re.Match", TypingImportOnly: true},
}

var deprecatedSpecialForms = map[string]*DeprecatedForm{
	"Optional": {Version: common.PythonVersion3_10, FullName: "typing.Optional", ReplacementText: "| None"},
	"Union":    {Version: common.PythonVersion3_10, FullName: "typing.Union", ReplacementText: "|"},
	"Callable": {Version: common.PythonVersion3_9, FullName: "typing.Callable", ReplacementText: "collections.abc.Callable", TypingImportOnly: true},
}
