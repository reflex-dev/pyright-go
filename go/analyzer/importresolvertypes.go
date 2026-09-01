/*
 * importresolvertypes.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/importResolverTypes.ts (pyright 1.1.412).
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// SupportedVersionInfo corresponds to the interface of the same name. Max is
// `PythonVersion | undefined`, so it is a pointer; the two platform lists are
// optional arrays, where nil is the absence.
type SupportedVersionInfo struct {
	Min                  common.PythonVersion
	Max                  *common.PythonVersion
	UnsupportedPlatforms []string
	SupportedPlatforms   []string
}

// TypeshedThirdPartyPackageMapResult corresponds to the readonly tuple of the
// same name, which Go has no direct spelling for.
type TypeshedThirdPartyPackageMapResult struct {
	// PackagePaths maps a package name to the typeshed directories that
	// provide it.
	PackagePaths *common.OrderedMap[string, []uri.Uri]

	// Paths is every directory in PackagePaths, deduplicated and sorted.
	Paths []uri.Uri
}

// TypeshedInfoProvider is an optional hook used to override how
// typeshed-derived information is computed and cached.
//
// The original's comment: ImportResolver will consult this service (if
// registered) before falling back to a default implementation. Tests can use
// this to provide a memoized implementation so expensive typeshed
// scanning/reading work is performed once and reused across many ImportResolver
// instances.
//
// Every method returns nil where the original returns undefined, and takes a
// nilable customTypeshedPath and importLogger for the same reason.
type TypeshedInfoProvider interface {
	GetTypeshedRoot(customTypeshedPath uri.Uri, importLogger *ImportLogger) uri.Uri

	GetTypeshedSubdirectory(isStdLib bool, customTypeshedPath uri.Uri, importLogger *ImportLogger) uri.Uri

	GetThirdPartyPackageMap(customTypeshedPath uri.Uri, importLogger *ImportLogger) TypeshedThirdPartyPackageMapResult

	GetStdLibModuleVersionInfo(customTypeshedPath uri.Uri, importLogger *ImportLogger) *common.OrderedMap[string, SupportedVersionInfo]
}

// ImportResolverFileSystem is the minimal cached filesystem facade used by
// ImportResolver.
//
// The original's comment: it caches directory enumeration and a few
// existence/file-list lookups to avoid repeated IO. The API is intentionally
// small and tailored to ImportResolver's needs.
//
// The original declares this as `Pick<FileSystem, ...>` plus five methods of
// its own; the picked members are written out here, because Go has no way to
// name a subset of an interface.
type ImportResolverFileSystem interface {
	ExistsSync(u uri.Uri) bool
	RealCasePath(u uri.Uri) uri.Uri
	GetModulePath() uri.Uri
	ReaddirEntriesSync(u uri.Uri) ([]uri.Dirent, error)
	ReadFileSync(u uri.Uri) ([]byte, error)
	StatSync(u uri.Uri) (uri.Stats, error)

	// ImportResolver-specific helpers.
	FileExists(u uri.Uri) bool
	DirExists(u uri.Uri) bool
	GetFilesInDirectory(dirPath uri.Uri) []uri.Uri
	GetResolvableNamesInDirectory(dirPath uri.Uri) *common.OrderedSet[string]
	InvalidateCache()
}
