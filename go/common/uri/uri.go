/*
 * uri.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * URI interface definition and the namespace-level helpers.
 *
 * Transliterated from common/uri/uriInterface.ts and common/uri/uri.ts
 * (pyright 1.1.412).
 *
 * Scope note: this package currently provides the Uri interface, the shared
 * BaseUri behavior, and the ConstantUri / EmptyUri implementations. FileUri and
 * WebUri are not ported yet -- they need vscode-uri's parsing and normalization
 * plus the whole of common/pathUtils, and nothing that consumes this package so
 * far constructs one. They belong with the import resolver.
 *
 * The Uri methods that only FileUri and WebUri would ever reach are declared on
 * the interface anyway, so the interface is the real one and adding those two
 * types later is additive.
 */

package uri

import "strings"

// Uri is the interface every URI implementation satisfies.
//
// In the TypeScript this is an interface in uriInterface.ts plus a same-named
// namespace of static helpers in uri.ts. Go has no namespace merging, so the
// static helpers are free functions at the bottom of this file, and the
// property getters become methods.
type Uri interface {
	// Key is the unique key for storing in maps.
	Key() string

	// Scheme returns the scheme of the URI.
	Scheme() string

	// FileName returns the last segment of the URI, similar to the UNIX
	// basename command.
	FileName() string

	// LastExtension returns the extension of the URI, similar to the UNIX
	// extname command. This includes '.' on the extension.
	LastExtension() string

	// Root returns a URI where the path just contains the root folder.
	Root() Uri

	// PackageUri returns a URI where the path contains the directory name
	// with .py appended.
	PackageUri() Uri

	// PackageStubUri returns a URI where the path contains the directory
	// name with .pyi appended.
	PackageStubUri() Uri

	// InitPyUri returns a URI where the path has __init__.py appended.
	InitPyUri() Uri

	// InitPyiUri returns a URI where the path has __init__.pyi appended.
	InitPyiUri() Uri

	// PytypedUri returns a URI where the path has py.typed appended.
	PytypedUri() Uri

	// FileNameWithoutExtensions returns the filename without any extensions.
	FileNameWithoutExtensions() string

	// IsCaseSensitive indicates if the underlying file system for this URI
	// is case sensitive or not. This should never be used to create another
	// Uri.
	IsCaseSensitive() bool

	// Fragment returns the fragment part of a URI.
	Fragment() string

	// Query returns the query part of a URI.
	Query() string

	IsEmpty() bool
	String() string
	ToUserVisibleString() string

	// IsRoot determines whether a path consists only of a path root.
	IsRoot() bool

	// IsChild determines whether a Uri is a child of some parent Uri,
	// meaning the parent Uri is a prefix of this Uri.
	IsChild(parent Uri) bool

	IsLocal() bool
	IsUntitled() bool
	Equals(other Uri) bool

	// StartsWith returns true if other is the parent of this, meaning other
	// is a prefix of this.
	StartsWith(other Uri) bool

	PathStartsWith(name string) bool
	PathEndsWith(name string) bool
	PathIncludes(include string) bool

	// MatchesRegex takes a compiled pattern rather than a JavaScript RegExp.
	MatchesRegex(regex Regexp) bool

	AddPath(extra string) Uri

	// GetDirectory returns a URI where the path is the directory name of the
	// original URI, similar to the UNIX dirname command.
	GetDirectory() Uri

	GetRootPathLength() int

	// GetPathLength is how long the path for this Uri is.
	GetPathLength() int

	// ResolvePaths combines paths with the URI and resolves any relative
	// paths. This should be used for combining paths with user input.
	ResolvePaths(paths ...string) Uri

	// CombinePaths combines paths with the URI and resolves any relative
	// paths. When the paths contain separators or '..' this uses
	// ResolvePaths; otherwise it calls the quicker version.
	CombinePaths(paths ...string) Uri

	// CombinePathsUnsafe combines paths with the URI and does NOT resolve any
	// '..' or '.' in the path. Only for input known to be relative and free
	// of separators.
	CombinePathsUnsafe(paths ...string) Uri

	// GetRelativePath returns the relative path, or "" and false when there
	// is none. The TypeScript returns `string | undefined`.
	GetRelativePath(child Uri) (string, bool)

	GetPathComponents() []string
	GetPath() string
	GetFilePath() string
	GetRelativePathComponents(to Uri) []string

	// GetShortenedFileName takes the max directory length explicitly. The
	// TypeScript defaults it to 15; see GetShortenedFileNameDefault.
	GetShortenedFileName(maxDirLength int) string

	StripExtension() Uri
	StripAllExtensions() Uri
	ReplaceExtension(ext string) Uri
	AddExtension(ext string) Uri
	HasExtension(ext string) bool
	ContainsExtension(ext string) bool
	WithFragment(fragment string) Uri
	WithQuery(query string) Uri
}

// Regexp is the subset of a compiled regular expression that MatchesRegex
// needs. It exists so this package does not have to decide between
// regexp.Regexp and a hand-written matcher on behalf of its callers; the only
// implementation so far (ConstantUri) ignores the argument entirely.
type Regexp interface {
	MatchString(s string) bool

	// String is the compiled pattern's source. regexp.Regexp already has it;
	// it is on the interface so a caller can report what it compiled, which the
	// config differential needs.
	String() string
}

// DefaultShortenedFileNameDirLength is the default for the maxDirLength
// parameter of GetShortenedFileName, which is a default argument in the
// TypeScript.
const DefaultShortenedFileNameDirLength = 15

// DefaultWorkspaceRootComponent is referenced by pathValidation.ts in the
// fourslash harness. The original notes that if the value changes, the Excel
// team should be told.
const DefaultWorkspaceRootComponent = "<default workspace root>"

// DefaultWorkspaceRootPath is DefaultWorkspaceRootComponent as an absolute
// path.
const DefaultWorkspaceRootPath = "/" + DefaultWorkspaceRootComponent

// Constant corresponds to Uri.constant.
func Constant(markerName string) Uri {
	return newConstantUri(markerName)
}

// Empty corresponds to Uri.empty.
func Empty() Uri {
	return emptyUriInstance
}

// IsEmpty corresponds to Uri.isEmpty. A nil Uri stands in for `undefined`.
func IsEmpty(u Uri) bool {
	return u == nil || u.IsEmpty()
}

// Equals corresponds to Uri.equals.
func Equals(a, b Uri) bool {
	if a == b {
		return true
	}
	if a == nil {
		return false
	}
	return a.Equals(b)
}

// IsDefaultWorkspace corresponds to Uri.isDefaultWorkspace.
func IsDefaultWorkspace(u Uri) bool {
	return strings.Contains(u.FileName(), DefaultWorkspaceRootComponent)
}

// Compile-time assertions that every implementation in this package satisfies
// the full interface, including the three protected members of BaseUri.
var (
	_ uriInternals = (*ConstantUri)(nil)
	_ uriInternals = (*EmptyUri)(nil)
	_ uriInternals = (*FileUri)(nil)
	_ uriInternals = (*WebUri)(nil)
)
