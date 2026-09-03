/*
 * baseuri.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Base URI behavior shared by every implementation.
 *
 * Transliterated from common/uri/baseUri.ts (pyright 1.1.412).
 *
 * TypeScript's `abstract class BaseUri` becomes an embeddable struct here. The
 * shared methods have to reach the subclass's overrides -- virtual dispatch --
 * so baseUri carries a `self` pointer back to the concrete value, set by each
 * constructor. Go embedding alone would call baseUri's own (nonexistent)
 * versions instead.
 */

package uri

import (
	"strings"
	"sync"

	"github.com/microsoft/pyright/go/common"
)

// uriInternals is Uri plus the three `protected abstract` members of BaseUri.
// Only implementations in this package satisfy it.
type uriInternals interface {
	Uri

	getRootPath() string
	getComparablePath() string
	getPathComponentsImpl() []string
}

type baseUri struct {
	key  string
	self uriInternals

	// mu guards every lazily memoized field on this instance and on the
	// embedding FileUri/WebUri. Uris are interned (see memoization.go), so
	// instances are shared across --threads worker goroutines; JavaScript's
	// single thread made the original's unguarded writes safe.
	mu sync.Mutex

	// The @cacheProperty() getters. TypeScript memoizes these on the
	// instance; the point is not just speed but that repeated reads return
	// the same object.
	packageUri     Uri
	packageStubUri Uri
	initPyUri      Uri
	initPyiUri     Uri
	pytypedUri     Uri
}

// memoize fills a lazily computed slot exactly once. The compute runs with mu
// released -- lazy getters call one another (WebUri.LastExtension reads
// FileName) and sync.Mutex does not re-enter -- and the first stored result
// wins, so repeated reads keep returning the same object, which is the point
// of @cacheProperty(). A zero-valued result is not stored, matching the
// original's truthiness guards.
func memoize[T comparable](mu *sync.Mutex, slot *T, compute func() T) T {
	var zero T
	mu.Lock()
	if *slot != zero {
		v := *slot
		mu.Unlock()
		return v
	}
	mu.Unlock()

	v := compute()

	mu.Lock()
	defer mu.Unlock()
	if *slot == zero {
		*slot = v
	}
	return *slot
}

// Key is the unique key for storing in maps.
func (b *baseUri) Key() string { return b.key }

// FileNameWithoutExtensions returns just the fileName without any extensions.
func (b *baseUri) FileNameWithoutExtensions() string {
	fileName := b.self.FileName()
	index := strings.LastIndex(fileName, ".")
	if index > 0 {
		return fileName[:index]
	}
	return fileName
}

// PackageUri returns a URI where the path contains the path with .py appended.
func (b *baseUri) PackageUri() Uri {
	// This is assuming that the current path is a file already.
	return memoize(&b.mu, &b.packageUri, func() Uri { return b.self.AddExtension(".py") })
}

// PackageStubUri returns a URI where the path contains the path with .pyi
// appended.
func (b *baseUri) PackageStubUri() Uri {
	// This is assuming that the current path is a file already.
	return memoize(&b.mu, &b.packageStubUri, func() Uri { return b.self.AddExtension(".pyi") })
}

// InitPyUri returns a URI where the path has __init__.py appended.
func (b *baseUri) InitPyUri() Uri {
	// This is assuming that the current path is a directory already.
	return memoize(&b.mu, &b.initPyUri, func() Uri { return b.self.CombinePathsUnsafe("__init__.py") })
}

// InitPyiUri returns a URI where the path has __init__.pyi appended.
func (b *baseUri) InitPyiUri() Uri {
	// This is assuming that the current path is a directory already.
	return memoize(&b.mu, &b.initPyiUri, func() Uri { return b.self.CombinePathsUnsafe("__init__.pyi") })
}

// PytypedUri returns a URI where the path has py.typed appended.
func (b *baseUri) PytypedUri() Uri {
	// This is assuming that the current path is a directory already.
	return memoize(&b.mu, &b.pytypedUri, func() Uri { return b.self.CombinePathsUnsafe("py.typed") })
}

func (b *baseUri) IsEmpty() bool { return false }

func (b *baseUri) ReplaceExtension(ext string) Uri {
	dir := b.self.GetDirectory()
	base := b.self.FileName()
	newBase := base[:len(base)-len(b.self.LastExtension())] + ext
	return dir.CombinePathsUnsafe(newBase)
}

func (b *baseUri) AddExtension(ext string) Uri {
	return b.self.AddPath(ext)
}

func (b *baseUri) HasExtension(ext string) bool {
	if b.self.IsCaseSensitive() {
		return b.self.LastExtension() == ext
	}
	return strings.ToLower(b.self.LastExtension()) == strings.ToLower(ext)
}

func (b *baseUri) ContainsExtension(ext string) bool {
	fileName := b.self.FileName()
	// The TypeScript splits on /(?=\.)/g -- a zero-width lookahead -- so the
	// '.' stays on the front of each piece rather than being consumed.
	extensions := splitKeepingDots(fileName)
	for _, e := range extensions {
		if b.self.IsCaseSensitive() {
			if e == ext {
				return true
			}
		} else if strings.ToLower(e) == strings.ToLower(ext) {
			return true
		}
	}
	return false
}

func (b *baseUri) GetRootPathLength() int {
	return len(b.self.getRootPath())
}

func (b *baseUri) IsUntitled() bool {
	return b.self.Scheme() == "untitled"
}

func (b *baseUri) Equals(other Uri) bool {
	if other == nil {
		return false
	}
	return b.self.Key() == other.Key()
}

func (b *baseUri) PathStartsWith(name string) bool {
	// We're making an assumption here that the name is already normalized.
	return strings.HasPrefix(b.self.getComparablePath(), name)
}

func (b *baseUri) PathEndsWith(name string) bool {
	// We're making an assumption here that the name is already normalized.
	return strings.HasSuffix(b.self.getComparablePath(), name)
}

func (b *baseUri) PathIncludes(include string) bool {
	// We're making an assumption here that the name is already normalized.
	return strings.Contains(b.self.getComparablePath(), include)
}

// GetRelativePath returns ("", false) where the TypeScript returns undefined.
func (b *baseUri) GetRelativePath(child Uri) (string, bool) {
	if b.self.Scheme() != child.Scheme() {
		return "", false
	}

	// Unlike GetRelativePathComponents, this function should not return
	// relative path markers for non children.
	if child.IsChild(b.self) {
		relativeToComponents := b.self.GetRelativePathComponents(child)
		if len(relativeToComponents) > 0 {
			return "./" + strings.Join(relativeToComponents, "/"), true
		}
	}
	return "", false
}

// GetPathComponents returns the path components. The TypeScript freezes the
// array before handing it out; the equivalent here is that callers must not
// modify the result.
func (b *baseUri) GetPathComponents() []string {
	return b.self.getPathComponentsImpl()
}

func (b *baseUri) GetRelativePathComponents(to Uri) []string {
	fromComponents := b.self.GetPathComponents()
	toComponents := to.GetPathComponents()

	start := 0
	for ; start < len(fromComponents) && start < len(toComponents); start++ {
		fromComponent := fromComponents[start]
		toComponent := toComponents[start]

		var match bool
		if b.self.IsCaseSensitive() {
			match = fromComponent == toComponent
		} else {
			match = strings.ToLower(fromComponent) == strings.ToLower(toComponent)
		}

		if !match {
			break
		}
	}

	if start == 0 {
		return toComponents
	}

	components := toComponents[start:]
	relative := []string{}
	for ; start < len(fromComponents); start++ {
		relative = append(relative, "..")
	}
	return append(relative, components...)
}

func (b *baseUri) GetShortenedFileName(maxDirLength int) string {
	return common.GetShortenedFileName(b.self.GetPath(), maxDirLength)
}

// normalizeSlashes corresponds to the protected method of the same name.
func normalizeSlashes(path string) string {
	if strings.Contains(path, "\\") {
		return strings.ReplaceAll(path, "\\", "/")
	}
	return path
}

// combinePathElements corresponds to the protected static of the same name.
//
// The original notes that this algorithm is borrowed from pathUtils'
// combinePaths, and is quicker because all paths are assumed normalized.
func combinePathElements(pathString string, separator string, paths ...string) string {
	for _, relativePath := range paths {
		if relativePath == "" {
			continue
		}
		if pathString == "" || common.GetRootLength(relativePath) != 0 {
			pathString = relativePath
		} else if strings.HasSuffix(pathString, separator) {
			pathString += relativePath
		} else {
			pathString += separator + relativePath
		}
	}

	return pathString
}

// reducePathComponents corresponds to the protected method of the same name.
func reducePathComponents(components []string) []string {
	if len(components) == 0 {
		return []string{}
	}

	// Reduce the path components by eliminating any '.' or '..'. We start at
	// 1 because the first component is always the root.
	reduced := []string{components[0]}
	for i := 1; i < len(components); i++ {
		component := components[i]
		if component == "" || component == "." {
			continue
		}

		if component == ".." {
			if len(reduced) > 1 {
				if reduced[len(reduced)-1] != ".." {
					reduced = reduced[:len(reduced)-1]
					continue
				}
			} else if reduced[0] != "" {
				continue
			}
		}
		reduced = append(reduced, component)
	}

	return reduced
}

// splitKeepingDots splits at the zero-width position before each '.', which is
// what the JavaScript /(?=\.)/g split does: "file.tar.gz" becomes
// ["file", ".tar", ".gz"].
//
// A match at index 0 is skipped, per String.prototype.split, so ".gitignore"
// stays a single element rather than gaining an empty one -- and "..a" comes
// out as [".", ".a"]. Scanning bytes is safe because '.' is ASCII and UTF-8 is
// self-synchronizing.
func splitKeepingDots(s string) []string {
	var out []string
	start := 0
	for i := 1; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, s[start:i])
			start = i
		}
	}
	return append(out, s[start:])
}
