/*
 * uriutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Utility functions for manipulating URIs.
 *
 * Transliterated from common/uri/uriUtils.ts (pyright 1.1.412).
 *
 * PARTIAL: everything here is a pure function of Uris. The half of the module
 * that takes a ReadOnlyFileSystem -- tryStat, fileExists, getFileSystemEntries
 * and the rest -- lands with the filesystem abstraction; see
 * uriutils_filesystem.go.
 *
 * Note this file has its own getWildcardRegexPattern and getWildcardRoot,
 * distinct from the same-named pair in common/pathUtils.go. These take a Uri
 * root and always use '/' as the separator; those take a string root and use
 * the platform separator. Both exist in the original and neither is dead.
 */

package uri

import (
	"regexp"

	"github.com/microsoft/pyright/go/common"
)

// FileSpec corresponds to the interface of the same name -- the Uri-rooted one
// in uriUtils, not the string-rooted one in pathUtils.
type FileSpec struct {
	// WildcardRoot is the first portion of the file spec that contains no
	// wildcard characters (**, *, ?).
	WildcardRoot Uri

	// RegExp matches against this file spec.
	RegExp Regexp

	// HasDirectoryWildcard indicates whether the file spec has a directory
	// wildcard (**). When present, the search cannot terminate without
	// exploring to an arbitrary depth.
	HasDirectoryWildcard bool
}

var (
	includeFileRegex = regexp.MustCompile(`\.pyi?$`)
	wildcardRegex    = regexp.MustCompile(`[*?]`)
)

// FileSpecIsInPath corresponds to FileSpec.isInPath.
func FileSpecIsInPath(u Uri, paths []FileSpec) bool {
	for _, p := range paths {
		if u.MatchesRegex(p.RegExp) {
			return true
		}
	}
	return false
}

// FileSpecMatchesIncludeFileRegex corresponds to
// FileSpec.matchesIncludeFileRegex. The TypeScript defaults isFile to true.
func FileSpecMatchesIncludeFileRegex(u Uri, isFile bool) bool {
	if isFile {
		return u.MatchesRegex(includeFileRegex)
	}
	return true
}

// FileSpecMatchIncludeFileSpec corresponds to FileSpec.matchIncludeFileSpec.
// The TypeScript defaults isFile to true.
func FileSpecMatchIncludeFileSpec(includeRegExp Regexp, exclude []FileSpec, u Uri, isFile bool) bool {
	if u.MatchesRegex(includeRegExp) {
		if !FileSpecIsInPath(u, exclude) && FileSpecMatchesIncludeFileRegex(u, isFile) {
			return true
		}
	}

	return false
}

// FileSystemEntries corresponds to the interface of the same name.
type FileSystemEntries struct {
	Files       []Uri
	Directories []Uri
}

// FileSystemEntriesWithSymlinkedDirectories corresponds to the interface of the
// same name.
type FileSystemEntriesWithSymlinkedDirectories struct {
	FileSystemEntries
	SymlinkedDirectories []Uri
}

// ForEachAncestorDirectory walks up from directory, returning the first
// non-nil callback result. The TypeScript returns `Uri | undefined`.
func ForEachAncestorDirectory(directory Uri, callback func(directory Uri) Uri) Uri {
	for {
		if result := callback(directory); result != nil {
			return result
		}

		parentPath := directory.GetDirectory()
		if parentPath.Equals(directory) {
			return nil
		}

		directory = parentPath
	}
}

// GetWildcardRegexPattern transforms a relative file spec (one that potentially
// contains the escape characters **, * or ?) and returns a regular expression
// pattern that can be used for matching against.
//
// As with the pathUtils version, the result is a JavaScript regular expression
// source string; see common.CompileWildcardRegexPattern for why compiling it
// needs care.
func GetWildcardRegexPattern(root Uri, fileSpec string) string {
	absolutePath := root.ResolvePaths(fileSpec)
	pathComponents := append([]string{}, absolutePath.GetPathComponents()...)
	escapedSeparator := common.GetRegexEscapedSeparator("/")
	doubleAsteriskRegexFragment := "(" + escapedSeparator + "[^" + escapedSeparator + "][^" + escapedSeparator + "]*)*?"

	// Strip the directory separator from the root component.
	if len(pathComponents) > 0 {
		pathComponents[0] = common.StripTrailingDirectorySeparator(pathComponents[0])
	}

	regExPattern := ""
	firstComponent := true

	for _, component := range pathComponents {
		if component == "**" {
			regExPattern += doubleAsteriskRegexFragment
		} else {
			if !firstComponent {
				component = escapedSeparator + component
			}

			regExPattern += common.ReplaceWildcardReservedCharacters(component, escapedSeparator)

			firstComponent = false
		}
	}

	return regExPattern
}

// GetWildcardRoot returns the topmost path that contains no wildcard
// characters.
func GetWildcardRoot(root Uri, fileSpec string) Uri {
	absolutePath := root.ResolvePaths(fileSpec)
	// Make a copy of the path components so we can modify them.
	pathComponents := append([]string{}, absolutePath.GetPathComponents()...)
	wildcardRoot := absolutePath.Root()

	// Remove the root component.
	if len(pathComponents) > 0 {
		pathComponents = pathComponents[1:]
	}

	for _, component := range pathComponents {
		if component == "**" {
			break
		}

		if wildcardRegex.MatchString(component) {
			break
		}

		wildcardRoot = wildcardRoot.ResolvePaths(component)
	}

	return wildcardRoot
}

func HasPythonExtension(u Uri) bool {
	return u.HasExtension(".py") || u.HasExtension(".pyi")
}

// GetFileSpec builds the anchored, optionally case-insensitive matcher for one
// file spec.
func GetFileSpec(root Uri, fileSpec string) FileSpec {
	regExPattern := GetWildcardRegexPattern(root, fileSpec)
	escapedSeparator := common.GetRegexEscapedSeparator("/")
	regExPattern = "^(" + regExPattern + ")($|" + escapedSeparator + ")"

	// `new RegExp(pattern, isCaseSensitive ? undefined : 'i')`.
	if !root.IsCaseSensitive() {
		regExPattern = "(?i)" + regExPattern
	}

	regExp, err := common.CompileWildcardRegexPattern(regExPattern)
	if err != nil {
		panic(err)
	}

	return FileSpec{
		WildcardRoot:         GetWildcardRoot(root, fileSpec),
		RegExp:               regExp,
		HasDirectoryWildcard: common.IsDirectoryWildcardPatternPresent(fileSpec),
	}
}

// DirectoryChangeKind is the return type of GetDirectoryChangeKind.
type DirectoryChangeKind = string

const (
	DirectoryChangeSame    DirectoryChangeKind = "Same"
	DirectoryChangeRenamed DirectoryChangeKind = "Renamed"
	DirectoryChangeMoved   DirectoryChangeKind = "Moved"
)

func GetDirectoryChangeKind(oldDirectory Uri, newDirectory Uri) DirectoryChangeKind {
	if oldDirectory.Equals(newDirectory) {
		return DirectoryChangeSame
	}

	relativePaths := oldDirectory.GetRelativePathComponents(newDirectory)

	// 2 means only last folder name has changed.
	if len(relativePaths) == 2 && relativePaths[0] == ".." && relativePaths[1] != ".." {
		return DirectoryChangeRenamed
	}

	return DirectoryChangeMoved
}

// DeduplicateFolders keeps only the topmost of each set of nested folders. The
// TypeScript defaults excludes to [].
//
// The iteration order of the result is the Map's insertion order, so this
// carries an ordered map rather than a Go map.
func DeduplicateFolders(listOfFolders [][]Uri, excludes []Uri) []Uri {
	foldersToWatch := common.NewOrderedMap[string, Uri]()

	for _, folders := range listOfFolders {
		for _, p := range folders {
			if _, ok := foldersToWatch.Get(p.Key()); ok {
				// Bail out on exact match.
				continue
			}

			skip := false
			for _, exclude := range excludes {
				if p.StartsWith(exclude) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}

			for _, key := range foldersToWatch.Keys() {
				existing, _ := foldersToWatch.Get(key)

				// ex) p: "/user/test" existing: "/user"
				if p.StartsWith(existing) {
					// We already have the parent folder in the watch list.
					skip = true
					break
				}

				// ex) p: "/user" folderToWatch: "/user/test"
				if existing.StartsWith(p) {
					// We found a better one to watch: replace.
					foldersToWatch.Delete(key)
					foldersToWatch.Set(p.Key(), p)
					skip = true
					break
				}
			}
			if skip {
				continue
			}

			foldersToWatch.Set(p.Key(), p)
		}
	}

	return foldersToWatch.Values()
}

/*
 * UriEx: the two constant case-sensitivity detectors and the factories that use
 * them, for callers that have no service provider. `UriEx.file(path)` defaults
 * isCaseSensitive to true.
 */

var (
	caseSensitivityDetector = common.CaseSensitivityDetectorFunc(func(string) bool { return true })

	caseInsensitivityDetector = common.CaseSensitivityDetectorFunc(func(string) bool { return false })
)

// UriExDetector corresponds to UriEx._getCaseSensitivityDetector.
func UriExDetector(isCaseSensitive bool) common.CaseSensitivityDetector {
	if isCaseSensitive {
		return caseSensitivityDetector
	}
	return caseInsensitivityDetector
}

// UriExFile corresponds to UriEx.file. The TypeScript defaults isCaseSensitive
// to true and checkRelative to false.
func UriExFile(path string, isCaseSensitive bool, checkRelative bool) Uri {
	return File(path, UriExDetector(isCaseSensitive), checkRelative)
}

// UriExParse corresponds to UriEx.parse. The TypeScript defaults
// isCaseSensitive to true.
func UriExParse(value string, isCaseSensitive bool) Uri {
	return Parse(value, UriExDetector(isCaseSensitive))
}
