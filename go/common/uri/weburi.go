/*
 * weburi.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * URI class that represents a URI that isn't 'file' schemed. This can be URIs
 * like:
 *  - http://www.microsoft.com/file.txt
 *  - untitled:Untitled-1
 *  - vscode:extension/ms-python.python
 *  - vscode-vfs://github.com/microsoft/debugpy/debugpy/launcher/debugAdapter.py
 *
 * Transliterated from common/uri/webUri.ts (pyright 1.1.412).
 */

package uri

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
)

// WebUri corresponds to the class of the same name.
type WebUri struct {
	baseUri

	scheme         string
	authority      string
	path           string
	query          string
	fragment       string
	originalString string

	// The @cacheProperty() / @cacheMethodWithNoArgs() getters.
	root          Uri
	fileName      *string
	lastExtension *string
	directory     Uri
}

func newWebUri(key, scheme, authority, path, query, fragment, originalString string) *WebUri {
	u := &WebUri{
		scheme:         scheme,
		authority:      authority,
		path:           path,
		query:          query,
		fragment:       fragment,
		originalString: originalString,
	}
	u.key = key
	u.self = u
	return u
}

// CreateWebUri corresponds to the static of the same name, memoized by
// @cacheStaticFunc().
func CreateWebUri(scheme, authority, path, query, fragment, originalString string) *WebUri {
	cacheKey := staticCacheKey("createWebUri", scheme, authority, path, query, fragment, originalString)
	return cacheStaticFunc(cacheKey, func() any {
		key := webUriCreateKey(scheme, authority, path, query, fragment)
		return newWebUri(key, scheme, authority, path, query, fragment, originalString)
	}).(*WebUri)
}

// IsWebUri corresponds to WebUri.isWebUri.
func IsWebUri(u Uri) bool {
	_, ok := u.(*WebUri)
	return ok
}

func (u *WebUri) Scheme() string { return u.scheme }

// IsCaseSensitive is always true: web URIs are always case sensitive.
func (u *WebUri) IsCaseSensitive() bool { return true }

func (u *WebUri) Fragment() string { return u.fragment }

func (u *WebUri) Query() string { return u.query }

func (u *WebUri) Root() Uri {
	if u.root == nil {
		rootPath := u.getRootPath()
		if rootPath != u.path {
			u.root = CreateWebUri(u.scheme, u.authority, rootPath, "", "", "")
		} else {
			u.root = u
		}
	}
	return u.root
}

func (u *WebUri) FileName() string {
	if u.fileName == nil {
		// Path should already be normalized, just get the last on a split of '/'.
		components := strings.Split(u.path, "/")
		name := components[len(components)-1]
		u.fileName = &name
	}
	return *u.fileName
}

func (u *WebUri) LastExtension() string {
	if u.lastExtension == nil {
		basename := u.FileName()
		ext := ""
		if index := strings.LastIndex(basename, "."); index >= 0 {
			ext = basename[index:]
		}
		u.lastExtension = &ext
	}
	return *u.lastExtension
}

func (u *WebUri) String() string {
	if u.originalString == "" {
		// URI.revive takes the object-form constructor, so the components are
		// used as-is: no scheme fix, no reference resolution, no validation.
		revived := newVsURIFromComponents(u.scheme, u.authority, u.path, u.query, u.fragment)
		u.originalString = revived.String()
	}
	return u.originalString
}

func (u *WebUri) ToUserVisibleString() string { return u.String() }

func (u *WebUri) MatchesRegex(regex Regexp) bool {
	return regex.MatchString(u.path)
}

func (u *WebUri) AddPath(extra string) Uri {
	newPath := u.path + extra
	return CreateWebUri(u.scheme, u.authority, newPath, u.query, u.fragment, "")
}

func (u *WebUri) IsRoot() bool {
	return u.path == u.getRootPath() && len(u.path) > 0
}

func (u *WebUri) IsChild(parent Uri) bool {
	parentWeb, ok := parent.(*WebUri)
	if !ok {
		return false
	}

	return len(parentWeb.path) < len(u.path) && u.StartsWith(parent)
}

func (u *WebUri) IsLocal() bool { return false }

func (u *WebUri) StartsWith(other Uri) bool {
	if other == nil || other.Scheme() != u.Scheme() {
		return false
	}
	otherWebUri, ok := other.(*WebUri)
	if !ok {
		return false
	}
	if len(u.path) >= len(otherWebUri.path) {
		// Make sure the other ends with a / when comparing longer paths,
		// otherwise we might say that /a/food is a child of /a/foo.
		otherPath := otherWebUri.path
		if len(u.path) > len(otherWebUri.path) && !common.HasTrailingDirectorySeparator(otherPath) {
			otherPath += "/"
		}

		return strings.HasPrefix(u.path, otherPath)
	}
	return false
}

func (u *WebUri) GetPathLength() int { return len(u.path) }

func (u *WebUri) GetPath() string { return u.path }

// GetFilePath is always empty: web URIs don't have file paths.
func (u *WebUri) GetFilePath() string { return "" }

func (u *WebUri) ResolvePaths(paths ...string) Uri {
	// Resolve and combine paths; never want URIs with '..' in the middle.
	combined := normalizeSlashes(common.ResolvePaths(u.path, paths...))

	// Make sure to remove any trailing directory chars.
	if common.HasTrailingDirectorySeparator(combined) && len(combined) > 1 {
		combined = combined[:len(combined)-1]
	}
	if combined != u.path {
		return CreateWebUri(u.scheme, u.authority, combined, "", "", "")
	}
	return u
}

func (u *WebUri) CombinePaths(paths ...string) Uri {
	for _, p := range paths {
		if strings.Contains(p, "..") || strings.Contains(p, "/") || p == "." {
			// This is a slow path that handles paths that contain '..' or '.'.
			return u.ResolvePaths(paths...)
		}
	}

	// Paths don't have anything special that needs to be combined differently,
	// so just use the quick method.
	return u.CombinePathsUnsafe(paths...)
}

func (u *WebUri) CombinePathsUnsafe(paths ...string) Uri {
	// Combine paths using the quick path implementation.
	combined := combinePathElements(u.path, "/", paths...)
	if combined != u.path {
		return CreateWebUri(u.scheme, u.authority, combined, "", "", "")
	}
	return u
}

func (u *WebUri) GetDirectory() Uri {
	if u.directory == nil {
		if len(u.path) == 0 {
			u.directory = u
		} else {
			index := strings.LastIndex(u.path, "/")
			newPath := ""
			if index > 0 {
				newPath = u.path[:index]
			} else if index == 0 {
				newPath = "/"
			}

			u.directory = CreateWebUri(u.scheme, u.authority, newPath, u.query, u.fragment, "")
		}
	}
	return u.directory
}

func (u *WebUri) WithFragment(fragment string) Uri {
	return CreateWebUri(u.scheme, u.authority, u.path, u.query, fragment, "")
}

func (u *WebUri) WithQuery(query string) Uri {
	return CreateWebUri(u.scheme, u.authority, u.path, query, u.fragment, "")
}

func (u *WebUri) StripExtension() Uri {
	path := u.path
	if index := strings.LastIndex(path, "."); index > 0 {
		return CreateWebUri(u.scheme, u.authority, path[:index], u.query, u.fragment, "")
	}
	return u
}

func (u *WebUri) StripAllExtensions() Uri {
	path := u.path
	sepIndex := strings.LastIndex(path, "/")
	from := 0
	if sepIndex > 0 {
		from = sepIndex
	}
	relative := strings.Index(path[from:], ".")
	if relative < 0 {
		return u
	}
	if index := from + relative; index > 0 {
		return CreateWebUri(u.scheme, u.authority, path[:index], u.query, u.fragment, "")
	}
	return u
}

func (u *WebUri) getPathComponentsImpl() []string {
	// Get the root path and the rest of the path components.
	rootPath := u.getRootPath()
	otherPaths := strings.Split(u.path[len(rootPath):], "/")
	reduced := reducePathComponents(append([]string{rootPath}, otherPaths...))
	out := make([]string, 0, len(reduced))
	for _, component := range reduced {
		out = append(out, normalizeSlashes(component))
	}
	return out
}

func (u *WebUri) getRootPath() string {
	rootLength := common.GetRootLengthSep(u.path, "/")
	return u.path[:rootLength]
}

// getComparablePath returns the path, which should already have the correct '/'.
func (u *WebUri) getComparablePath() string { return u.path }

func webUriCreateKey(scheme, authority, path, query, fragment string) string {
	key := scheme + ":" + authority + path
	if query != "" {
		key += "?" + query
	}
	if fragment != "" {
		key += "#" + fragment
	}
	return key
}
