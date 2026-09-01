/*
 * importresult.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Transliterated from analyzer/importResult.ts and the PyTypedInfo half of
 * analyzer/pyTypedUtils.ts (pyright 1.1.412).
 *
 * getPyTypedInfo and getPyTypedInfoForPyTypedFile are not here: they read the
 * filesystem through common/fileSystem.ts, which lands with the import
 * resolver. Only the PyTypedInfo shape is needed by ImportResult.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// PyTypedInfo corresponds to the interface of the same name in pyTypedUtils.ts.
type PyTypedInfo struct {
	PyTypedPath      uri.Uri
	IsPartiallyTyped bool
}

// ImportType corresponds to the const enum of the same name.
type ImportType int

const (
	ImportTypeBuiltIn ImportType = iota
	ImportTypeThirdParty
	ImportTypeLocal
)

// ImplicitImport corresponds to the interface of the same name.
type ImplicitImport struct {
	IsStubFile  bool
	IsNativeLib bool
	Name        string
	Uri         uri.Uri
	PyTypedInfo *PyTypedInfo
}

// ImportResult corresponds to the interface of the same name.
type ImportResult struct {
	// ImportName is the formatted import name. Useful for error messages.
	ImportName string

	// IsRelative indicates whether the import name was relative (starts with
	// one or more dots).
	IsRelative bool

	// IsImportFound is true if the import was resolved to a module or file.
	IsImportFound bool

	// IsPartlyResolved means the specific submodule was not found but a part of
	// its path was resolved.
	IsPartlyResolved bool

	// IsNamespacePackage is true if the import refers to a namespace package (a
	// folder without an __init__.py(i) file at the last level). To determine if
	// any intermediate level is a namespace package, look at the ResolvedUris
	// slice; namespace package entries have an empty URI.
	IsNamespacePackage bool

	// IsInitFilePresent is true if there is an __init__.py(i) file in the final
	// directory resolved.
	IsInitFilePresent bool

	// IsStubPackage records whether the import resolved to a stub within a stub
	// package.
	IsStubPackage bool

	// ImportFailureInfo may contain strings that help diagnose the import
	// resolution failure, when IsImportFound is false.
	ImportFailureInfo []string

	// ImportType is the type of import (built-in, local, third-party).
	ImportType ImportType

	// ResolvedUris holds the resolved absolute paths for each of the files in
	// the module name. Parts that have no files (e.g. directories within a
	// namespace package) have an empty URI.
	ResolvedUris []uri.Uri

	// SearchPath is the search path that was used to resolve (or partially
	// resolve) an absolute import.
	SearchPath uri.Uri

	// IsStubFile is true if the resolved file is a type hint (.pyi) file rather
	// than a python (.py) file.
	IsStubFile bool

	// IsNativeLib is true if the resolved file is a native DLL.
	IsNativeLib bool

	// IsStdlibTypeshedFile and IsThirdPartyTypeshedFile are true if the
	// resolved file is a type hint (.pyi) file that comes from typeshed in the
	// stdlib or third-party stubs.
	IsStdlibTypeshedFile     bool
	IsThirdPartyTypeshedFile bool

	// IsLocalTypingsFile is true if the resolved file is a type hint (.pyi)
	// file that comes from the configured typings directory.
	IsLocalTypingsFile bool

	// ImplicitImports lists files within the final resolved path that are
	// implicitly imported as part of the package -- used for both traditional
	// and namespace packages.
	ImplicitImports *common.OrderedMap[string, *ImplicitImport]

	// FilteredImplicitImports holds implicit imports filtered to include only
	// those symbols that are explicitly imported in a "from x import y"
	// statement.
	FilteredImplicitImports *common.OrderedMap[string, *ImplicitImport]

	// NonStubImportResult stores the import result from the .py file, if this
	// was resolved from a type hint (.pyi).
	NonStubImportResult *ImportResult

	// PyTypedInfo records whether there is a "py.typed" file (as described in
	// PEP 561) present in the package that was used to resolve the import.
	PyTypedInfo *PyTypedInfo

	// PackageDirectory is the directory of the package, if found.
	PackageDirectory uri.Uri
}
