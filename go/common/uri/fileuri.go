/*
 * fileuri.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * URI class that represents a file path. These URIs are always 'file' schemed.
 *
 * Transliterated from common/uri/fileUri.ts (pyright 1.1.412).
 */

package uri

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
)

// FileUriSchema corresponds to the exported constant of the same name.
const FileUriSchema = "file"

// FileUri corresponds to the class of the same name.
//
// `originalString` is `string | undefined` in the original; the empty string
// stands in for undefined, which is safe because the only test on it is
// truthiness (`if (!this._formattedString)`) and an empty formatted string
// would be recomputed there too.
type FileUri struct {
	baseUri

	filePath        string
	query           string
	fragment        string
	originalString  string
	isCaseSensitive bool

	formattedString string
	normalizedPath  string
	hasNormalized   bool
	combineChildren map[string]Uri

	// The @cacheProperty() / @cacheMethodWithNoArgs() getters.
	fileName      *string
	lastExtension *string
	root          Uri
	directory     Uri
}

// fileUriSeparator is `FileUri._separator`, a static initialized from
// getPathSeparator(”) at class-definition time.
var fileUriSeparator = common.GetPathSeparator("")

func newFileUri(key, filePath, query, fragment, originalString string, isCaseSensitive bool) *FileUri {
	u := &FileUri{
		filePath:        filePath,
		query:           query,
		fragment:        fragment,
		originalString:  originalString,
		isCaseSensitive: isCaseSensitive,
	}
	if isCaseSensitive {
		u.key = key
	} else {
		u.key = strings.ToLower(key)
	}
	u.self = u
	return u
}

// CreateFileUri corresponds to the static of the same name. It is memoized by
// @cacheStaticFunc(), which interns the result -- see memoization.go.
func CreateFileUri(filePath, query, fragment, originalString string, isCaseSensitive bool) *FileUri {
	cacheKey := staticCacheKey("createFileUri", filePath, query, fragment, originalString, boolArg(isCaseSensitive))
	return cacheStaticFunc(cacheKey, func() any {
		path := filePath
		if common.IsDiskPathRoot(path) {
			path = common.EnsureTrailingDirectorySeparator(path)
		}

		key := fileUriCreateKey(path, query, fragment)
		return newFileUri(key, path, query, fragment, originalString, isCaseSensitive)
	}).(*FileUri)
}

// IsFileUri corresponds to FileUri.isFileUri, which in the TypeScript sniffs
// the private _filePath and _key off an arbitrary value.
func IsFileUri(u Uri) bool {
	_, ok := u.(*FileUri)
	return ok
}

func (u *FileUri) Scheme() string { return FileUriSchema }

func (u *FileUri) Fragment() string { return u.fragment }

func (u *FileUri) Query() string { return u.query }

func (u *FileUri) FileName() string {
	return *memoize(&u.mu, &u.fileName, func() *string {
		name := common.GetFileName(u.filePath)
		return &name
	})
}

func (u *FileUri) LastExtension() string {
	return *memoize(&u.mu, &u.lastExtension, func() *string {
		ext := common.GetFileExtension(u.filePath, false)
		return &ext
	})
}

func (u *FileUri) Root() Uri {
	return memoize(&u.mu, &u.root, func() Uri {
		rootPath := u.getRootPath()
		if rootPath != u.filePath {
			return CreateFileUri(rootPath, "", "", "", u.isCaseSensitive)
		}
		return u
	})
}

func (u *FileUri) IsCaseSensitive() bool { return u.isCaseSensitive }

// MatchesRegex compares the regex to the path, normalized for comparison. The
// regex assumes it is comparing itself to a URI path.
func (u *FileUri) MatchesRegex(regex Regexp) bool {
	return regex.MatchString(u.getNormalizedPath())
}

func (u *FileUri) String() string {
	return memoize(&u.mu, &u.formattedString, func() string {
		if u.originalString != "" {
			return u.originalString
		}
		q, f := u.query, u.fragment
		return vsURIFile(u.filePath).with(uriChange{query: &q, fragment: &f}).String()
	})
}

func (u *FileUri) ToUserVisibleString() string { return u.filePath }

func (u *FileUri) AddPath(extra string) Uri {
	return CreateFileUri(u.filePath+extra, "", "", "", u.isCaseSensitive)
}

func (u *FileUri) IsRoot() bool {
	return common.IsDiskPathRoot(u.filePath)
}

func (u *FileUri) IsChild(parent Uri) bool {
	parentFile, ok := parent.(*FileUri)
	if !ok {
		return false
	}

	return len(parentFile.filePath) < len(u.filePath) && u.StartsWith(parent)
}

func (u *FileUri) IsLocal() bool { return true }

func (u *FileUri) StartsWith(other Uri) bool {
	if other == nil || other.Scheme() != u.Scheme() {
		return false
	}
	otherFileUri, ok := other.(*FileUri)
	if !ok {
		return false
	}
	if len(u.filePath) >= len(otherFileUri.filePath) {
		// Make sure the other ends with a / when comparing longer paths,
		// otherwise we might say that /a/food is a child of /a/foo.
		otherPath := otherFileUri.filePath
		if len(u.filePath) > len(otherFileUri.filePath) && !common.HasTrailingDirectorySeparator(otherPath) {
			otherPath = common.EnsureTrailingDirectorySeparator(otherPath)
		}

		if !u.IsCaseSensitive() {
			return strings.HasPrefix(strings.ToLower(u.filePath), strings.ToLower(otherPath))
		}
		return strings.HasPrefix(u.filePath, otherPath)
	}
	return false
}

func (u *FileUri) GetPathLength() int { return len(u.filePath) }

func (u *FileUri) GetPath() string { return u.getNormalizedPath() }

func (u *FileUri) GetFilePath() string { return u.filePath }

func (u *FileUri) ResolvePaths(paths ...string) Uri {
	// Resolve and combine paths; never want URIs with '..' in the middle.
	combined := common.ResolvePaths(u.filePath, paths...)

	// Make sure to remove any trailing directory chars.
	if common.HasTrailingDirectorySeparator(combined) && len(combined) > 1 {
		combined = combined[:len(combined)-1]
	}
	if combined != u.filePath {
		return CreateFileUri(combined, "", "", "", u.isCaseSensitive)
	}
	return u
}

func (u *FileUri) CombinePaths(paths ...string) Uri {
	// Fast path: a single simple segment (no '.', '..', or separators) is by
	// far the most common case on the import-resolution hot path. Memoize the
	// resulting child per parent keyed by the short segment, so repeated walks
	// of the same directory tree avoid rebuilding the full child path and the
	// interning cache's long key.
	if len(paths) == 1 {
		segment := paths[0]
		if len(segment) > 0 &&
			segment != "." &&
			!strings.Contains(segment, "..") &&
			!strings.Contains(segment, fileUriSeparator) &&
			!strings.Contains(segment, "/") {
			u.mu.Lock()
			if cached, ok := u.combineChildren[segment]; ok {
				u.mu.Unlock()
				return cached
			}
			u.mu.Unlock()

			child := u.CombinePathsUnsafe(segment)

			u.mu.Lock()
			if cached, ok := u.combineChildren[segment]; ok {
				child = cached
			} else {
				if u.combineChildren == nil {
					u.combineChildren = map[string]Uri{}
				}
				u.combineChildren[segment] = child
			}
			u.mu.Unlock()
			return child
		}
	}

	for _, p := range paths {
		if strings.Contains(p, "..") || strings.Contains(p, fileUriSeparator) || strings.Contains(p, "/") || p == "." {
			// This is a slow path that handles paths that contain '..' or '.'.
			return u.ResolvePaths(paths...)
		}
	}

	// Paths don't have anything special that needs to be combined differently,
	// so just use the quick method.
	return u.CombinePathsUnsafe(paths...)
}

func (u *FileUri) CombinePathsUnsafe(paths ...string) Uri {
	// Combine paths using the quicker path implementation, as all data is
	// assumed to be normalized already.
	combined := combinePathElements(u.filePath, fileUriSeparator, paths...)
	if combined != u.filePath {
		return CreateFileUri(combined, "", "", "", u.isCaseSensitive)
	}
	return u
}

func (u *FileUri) GetDirectory() Uri {
	return memoize(&u.mu, &u.directory, func() Uri {
		filePath := u.filePath
		dir := common.GetDirectoryPath(filePath)
		if common.HasTrailingDirectorySeparator(dir) && len(dir) > 1 {
			dir = dir[:len(dir)-1]
		}
		if dir != filePath {
			return CreateFileUri(dir, "", "", "", u.isCaseSensitive)
		}
		return u
	})
}

func (u *FileUri) WithFragment(fragment string) Uri {
	return CreateFileUri(u.filePath, u.query, fragment, "", u.isCaseSensitive)
}

func (u *FileUri) WithQuery(query string) Uri {
	return CreateFileUri(u.filePath, query, u.fragment, "", u.isCaseSensitive)
}

func (u *FileUri) StripExtension() Uri {
	stripped := common.StripFileExtension(u.filePath, false)
	if stripped != u.filePath {
		return CreateFileUri(stripped, u.query, u.fragment, "", u.isCaseSensitive)
	}
	return u
}

func (u *FileUri) StripAllExtensions() Uri {
	stripped := common.StripFileExtension(u.filePath, true)
	if stripped != u.filePath {
		return CreateFileUri(stripped, u.query, u.fragment, "", u.isCaseSensitive)
	}
	return u
}

func (u *FileUri) getPathComponentsImpl() []string {
	components := common.GetPathComponents(u.filePath)
	// Remove the first one if it's empty. The new algorithm doesn't expect this
	// to be there.
	if len(components) > 0 && components[0] == "" {
		components = components[1:]
	}
	out := make([]string, 0, len(components))
	for _, component := range components {
		out = append(out, normalizeSlashes(component))
	}
	return out
}

func (u *FileUri) getRootPath() string {
	return u.filePath[:common.GetRootLength(u.filePath)]
}

func (u *FileUri) getComparablePath() string { return u.getNormalizedPath() }

func fileUriCreateKey(filePath, query, fragment string) string {
	key := filePath
	if query != "" {
		key += "?" + query
	}
	if fragment != "" {
		key += "#" + fragment
	}
	return key
}

// getNormalizedPath memoizes on an explicit flag rather than on emptiness,
// because the original's guard is `=== undefined` here rather than the
// truthiness test it uses elsewhere -- an empty path is cached.
func (u *FileUri) getNormalizedPath() string {
	// The lock is held across the compute: normalizeSlashes is pure, so this
	// cannot re-enter another lazy getter, and the flag plus value must be
	// published together.
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.hasNormalized {
		u.normalizedPath = normalizeSlashes(u.filePath)
		u.hasNormalized = true
	}
	return u.normalizedPath
}
