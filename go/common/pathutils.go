/*
 * pathutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Pathname utility functions.
 *
 * Transliterated from common/pathUtils.ts (pyright 1.1.412).
 *
 * Three things about the original shape everything below:
 *
 *  1. It is written against Node's `path` module, and `path` picks POSIX or
 *     Win32 semantics from the host. `getPathSeparator` ignores its argument
 *     and returns `path.sep`. PathSep below is `os.PathSeparator` for the same
 *     reason, so the two agree on whatever host they run on; the Node
 *     primitives reproduced at the bottom of this file are the POSIX ones,
 *     which is what both sides get on Linux.
 *
 *  2. Several exports are TypeScript overloads distinguished by the *type* of
 *     an optional argument. Go has no overloading, so each becomes its own
 *     function with a suffix, and the header comment says which overload it is.
 *
 *  3. `string | undefined` arguments (the variadic path lists) become plain
 *     `string`, because every one of them is tested for truthiness before use
 *     and an empty string is falsy in JavaScript. The one place where that is
 *     *not* interchangeable is `some(paths)`, which counts elements rather than
 *     looking at them; see ResolvePaths.
 */

package common

import (
	"os"
	"regexp"
	"strings"
)

// PathSep is Node's `path.sep`.
var PathSep = string(os.PathSeparator)

// GetCanonicalFileName corresponds to the type of the same name.
type GetCanonicalFileName func(fileName string) string

// FileSpec corresponds to the interface of the same name.
type FileSpec struct {
	// WildcardRoot is the first portion of the file spec that contains no
	// wildcard characters (**, *, ?).
	WildcardRoot string

	// RegExp matches against this file spec.
	RegExp *regexp.Regexp

	// HasDirectoryWildcard indicates whether the file spec has a directory
	// wildcard (**). When present, the search cannot terminate without
	// exploring to an arbitrary depth.
	HasDirectoryWildcard bool
}

// includeFileRegex and wildcardRootRegex correspond to the two module-level
// regular expressions. The first is a suffix test and the second a character
// test, so neither needs the regexp package.
func matchesIncludeFileSuffix(filePath string) bool {
	return strings.HasSuffix(filePath, ".py") || strings.HasSuffix(filePath, ".pyi")
}

func containsWildcardRootChar(component string) bool {
	return strings.ContainsAny(component, "*?")
}

// FileSpecIsInPath corresponds to FileSpec.isInPath.
func FileSpecIsInPath(path string, paths []FileSpec) bool {
	for _, p := range paths {
		if p.RegExp.MatchString(path) {
			return true
		}
	}
	return false
}

// FileSpecMatchesIncludeFileRegex corresponds to
// FileSpec.matchesIncludeFileRegex. The TypeScript defaults isFile to true.
func FileSpecMatchesIncludeFileRegex(filePath string, isFile bool) bool {
	if isFile {
		return matchesIncludeFileSuffix(filePath)
	}
	return true
}

// FileSpecMatchIncludeFileSpec corresponds to FileSpec.matchIncludeFileSpec.
// The TypeScript defaults isFile to true.
func FileSpecMatchIncludeFileSpec(includeRegExp *regexp.Regexp, exclude []FileSpec, filePath string, isFile bool) bool {
	if includeRegExp.MatchString(filePath) {
		if !FileSpecIsInPath(filePath, exclude) && FileSpecMatchesIncludeFileRegex(filePath, isFile) {
			return true
		}
	}

	return false
}

// FileSystemEntry is one element of FileSystemEntries.files.
type FileSystemEntry struct {
	Name string
	Size int64
}

// FileSystemEntries corresponds to the interface of the same name.
type FileSystemEntries struct {
	Files       []FileSystemEntry
	Directories []string
}

func GetDirectoryPath(pathString string) string {
	end := lastIndexOfString(pathString, PathSep)
	if rootLength := GetRootLength(pathString); rootLength > end {
		end = rootLength
	}
	return pathString[:end]
}

// GetRootLength returns the length of the root part of a path or URL (i.e. the
// length of "/", "x:/", "//server/"). The TypeScript defaults sep to path.sep;
// see GetRootLengthSep for the two callers that pass one.
func GetRootLength(pathString string) int {
	return GetRootLengthSep(pathString, PathSep)
}

// GetRootLengthSep is GetRootLength with the separator supplied explicitly.
func GetRootLengthSep(pathString string, sep string) int {
	// charAt is JavaScript's String.prototype.charAt, which answers '' rather
	// than throwing when the index is past the end.
	charAt := func(i int) string {
		if i < len(pathString) {
			return pathString[i : i+1]
		}
		return ""
	}

	if charAt(0) == sep {
		if charAt(1) != sep {
			return 1 // POSIX: "/" (or non-normalized "\")
		}
		p1 := strings.Index(pathString[2:], sep)
		if p1 < 0 {
			return len(pathString) // UNC: "//server" or "\\server"
		}
		return p1 + 2 + 1 // UNC: "//server/" or "\\server\"
	}
	if charAt(1) == ":" {
		if charAt(2) == sep {
			return 3 // DOS: "c:/" or "c:\"
		}
		if len(pathString) == 2 {
			return 2 // DOS: "c:" (but not "c:d")
		}
	}

	return 0
}

// GetPathSeparator ignores its argument, exactly as the original does.
func GetPathSeparator(pathString string) string {
	return PathSep
}

func GetPathComponents(pathString string) []string {
	normalizedPath := NormalizeSlashes(pathString)
	rootLength := GetRootLength(normalizedPath)
	root := normalizedPath[:rootLength]
	sep := GetPathSeparator(pathString)
	rest := strings.Split(normalizedPath[rootLength:], sep)
	if len(rest) > 0 && rest[len(rest)-1] == "" {
		rest = rest[:len(rest)-1]
	}

	return ReducePathComponents(append([]string{root}, rest...))
}

func ReducePathComponents(components []string) []string {
	if len(components) == 0 {
		return []string{}
	}

	// Reduce the path components by eliminating any '.' or '..'.
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

func CombinePathComponents(components []string) string {
	if len(components) == 0 {
		return ""
	}

	// `components[0] && ensureTrailingDirectorySeparator(components[0])` is ''
	// when the first component is empty, not the separator.
	root := ""
	if components[0] != "" {
		root = EnsureTrailingDirectorySeparator(components[0])
	}
	sep := GetPathSeparator(root)
	return NormalizeSlashes(root + strings.Join(components[1:], sep))
}

// GetRelativePath returns ("", false) where the TypeScript returns undefined.
func GetRelativePath(dirPath string, relativeTo string) (string, bool) {
	if !strings.HasPrefix(dirPath, EnsureTrailingDirectorySeparator(relativeTo)) {
		return "", false
	}

	pathComponents := GetPathComponents(dirPath)
	relativeToComponents := GetPathComponents(relativeTo)
	sep := GetPathSeparator(dirPath)

	relativePath := "."
	for i := len(relativeToComponents); i < len(pathComponents); i++ {
		relativePath += sep + pathComponents[i]
	}

	return relativePath, true
}

// getInvalidSeparator corresponds to the arrow function of the same name.
func getInvalidSeparator(sep string) string {
	if sep == "/" {
		return "\\"
	}
	return "/"
}

// NormalizeSlashes corresponds to normalizeSlashes with sep defaulted.
func NormalizeSlashes(pathString string) string {
	return NormalizeSlashesSep(pathString, PathSep)
}

// NormalizeSlashesSep is NormalizeSlashes with the separator supplied.
func NormalizeSlashesSep(pathString string, sep string) string {
	if strings.Contains(pathString, getInvalidSeparator(sep)) {
		// The original replaces /[\\/]/g, i.e. both separators, not just the
		// invalid one.
		return strings.NewReplacer("\\", sep, "/", sep).Replace(pathString)
	}

	return pathString
}

// ResolvePaths combines and resolves paths. If a path is absolute, it replaces
// any previous path. Any `.` and `..` path components are resolved. Trailing
// directory separators are preserved.
//
//	ResolvePaths("/path", "to", "file.ext")       == "path/to/file.ext"
//	ResolvePaths("/path", "to", "file.ext/")      == "path/to/file.ext/"
//	ResolvePaths("/path", "dir", "..", "to", "f") == "path/to/f"
//
// The `some(paths)` test is on the *count* of variadic arguments, not on
// whether any of them is non-empty, so ResolvePaths("a", "") takes the
// combinePaths branch while ResolvePaths("a") does not. Passing no arguments
// is the only way to get the second branch.
func ResolvePaths(path string, paths ...string) string {
	if len(paths) > 0 {
		return NormalizePath(CombinePaths(path, paths...))
	}
	return NormalizePath(NormalizeSlashes(path))
}

func CombinePaths(pathString string, paths ...string) string {
	if pathString != "" {
		pathString = NormalizeSlashes(pathString)
	}

	for _, relativePath := range paths {
		if relativePath == "" {
			continue
		}

		relativePath = NormalizeSlashes(relativePath)

		if pathString == "" || GetRootLength(relativePath) != 0 {
			pathString = relativePath
		} else {
			pathString = EnsureTrailingDirectorySeparator(pathString) + relativePath
		}
	}

	return pathString
}

// ContainsPath determines whether a `parent` path contains a `child` path using
// the provided case sensitivity. This is the (parent, child, ignoreCase)
// overload.
func ContainsPath(parent string, child string, ignoreCase bool) bool {
	if parent == child {
		return true
	}

	parentComponents := GetPathComponents(parent)
	childComponents := GetPathComponents(child)

	if len(childComponents) < len(parentComponents) {
		return false
	}

	for i := 0; i < len(parentComponents); i++ {
		// The root component is always compared case-insensitively.
		equal := false
		if i == 0 || ignoreCase {
			equal = EquateStringsCaseInsensitive(parentComponents[i], childComponents[i])
		} else {
			equal = EquateStringsCaseSensitive(parentComponents[i], childComponents[i])
		}
		if !equal {
			return false
		}
	}

	return true
}

// ContainsPathIn is the (parent, child, currentDirectory, ignoreCase) overload.
func ContainsPathIn(parent string, child string, currentDirectory string, ignoreCase bool) bool {
	return ContainsPath(CombinePaths(currentDirectory, parent), CombinePaths(currentDirectory, child), ignoreCase)
}

// ChangeAnyExtension changes the extension of a path to the provided extension.
//
//	ChangeAnyExtension("/path/to/file.ext", ".js") == "/path/to/file.js"
func ChangeAnyExtension(path string, ext string) string {
	return changeAnyExtensionWorker(path, ext, GetAnyExtensionFromPath(path))
}

// ChangeAnyExtensionIn changes the extension of a path to the provided
// extension if it already has one of the provided extensions.
//
//	ChangeAnyExtensionIn("/path/to/f.ext", ".js", [".ext"], true) == "/path/to/f.js"
//	ChangeAnyExtensionIn("/path/to/f.ext", ".js", [".ts"], true)  == "/path/to/f.ext"
func ChangeAnyExtensionIn(path string, ext string, extensions []string, ignoreCase bool) string {
	return changeAnyExtensionWorker(path, ext, GetAnyExtensionFromPathIn(path, extensions, ignoreCase))
}

func changeAnyExtensionWorker(path string, ext string, pathExt string) string {
	if pathExt == "" {
		return path
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return path[:len(path)-len(pathExt)] + ext
}

// GetAnyExtensionFromPath gets the file extension for a path.
//
//	GetAnyExtensionFromPath("/path/to/file.ext")  == ".ext"
//	GetAnyExtensionFromPath("/path/to/file.ext/") == ".ext"
//	GetAnyExtensionFromPath("/path/to/file")      == ""
//	GetAnyExtensionFromPath("/path/to.ext/file")  == ""
func GetAnyExtensionFromPath(path string) string {
	// Retrieves any string from the final "." onwards from a base file name.
	baseFileName := GetBaseFileName(path)
	extensionIndex := strings.LastIndex(baseFileName, ".")
	if extensionIndex >= 0 {
		return baseFileName[extensionIndex:]
	}
	return ""
}

// GetAnyExtensionFromPathIn gets the file extension for a path, provided it is
// one of the provided extensions.
//
//	GetAnyExtensionFromPathIn("/p/f.ext", [".ext"], true)        == ".ext"
//	GetAnyExtensionFromPathIn("/p/f.js", [".ext"], true)         == ""
//	GetAnyExtensionFromPathIn("/p/f.js", [".ext", ".js"], true)  == ".js"
//	GetAnyExtensionFromPathIn("/p/f.ext", [".EXT"], false)       == ""
//
// The TypeScript accepts `string | readonly string[]`; a lone string is a
// one-element slice here, which the worker treats identically.
func GetAnyExtensionFromPathIn(path string, extensions []string, ignoreCase bool) string {
	comparer := EquateStringsCaseSensitive
	if ignoreCase {
		comparer = EquateStringsCaseInsensitive
	}
	return getAnyExtensionFromPathWorker(StripTrailingDirectorySeparator(path), extensions, comparer)
}

// GetBaseFileName returns the path except for its containing directory name.
// Semantics align with Node's `path.basename` except that URLs are supported.
//
//	GetBaseFileName("/path/to/file.ext")   == "file.ext"
//	GetBaseFileName("/path/to/")           == "to"
//	GetBaseFileName("/")                   == ""
//	GetBaseFileName("c:/path/to/file.ext") == "file.ext"
//	GetBaseFileName("c:/")                 == ""
//	GetBaseFileName("c:")                  == ""
func GetBaseFileName(pathString string) string {
	return getBaseFileNameWorker(pathString, nil, false)
}

// GetBaseFileNameIn gets the portion of a path following the last
// (non-terminal) separator. If the base name has any one of the provided
// extensions, it is removed.
//
//	GetBaseFileNameIn("/p/f.ext", [".ext"], true)       == "f"
//	GetBaseFileNameIn("/p/f.js", [".ext"], true)        == "f.js"
//	GetBaseFileNameIn("/p/f.js", [".ext", ".js"], true) == "f"
//	GetBaseFileNameIn("/p/f.ext", [".EXT"], false)      == "f.ext"
func GetBaseFileNameIn(pathString string, extensions []string, ignoreCase bool) string {
	return getBaseFileNameWorker(pathString, extensions, ignoreCase)
}

// getBaseFileNameWorker takes nil extensions for the one-argument overload,
// which is how the original distinguishes them.
func getBaseFileNameWorker(pathString string, extensions []string, ignoreCase bool) string {
	pathString = NormalizeSlashes(pathString)

	// If the path provided is itself the root, then it has no file name.
	rootLength := GetRootLength(pathString)
	if rootLength == len(pathString) {
		return ""
	}

	// Return the trailing portion of the path starting after the last
	// (non-terminal) directory separator but not including any trailing
	// directory separator.
	pathString = StripTrailingDirectorySeparator(pathString)
	start := lastIndexOfString(pathString, PathSep) + 1
	if rootLength := GetRootLength(pathString); rootLength > start {
		start = rootLength
	}
	name := pathString[start:]

	if extensions != nil {
		if extension := GetAnyExtensionFromPathIn(name, extensions, ignoreCase); extension != "" {
			return name[:len(name)-len(extension)]
		}
	}

	return name
}

// GetRelativePathFromDirectory gets a relative path that can be used to
// traverse between `from` and `to`. This is the ignoreCase overload.
func GetRelativePathFromDirectory(fromDirectory string, to string, ignoreCase bool) string {
	return CombinePathComponents(GetRelativePathComponentsFromDirectory(fromDirectory, to, ignoreCase))
}

// GetRelativePathFromDirectoryCanonical is the getCanonicalFileName overload.
func GetRelativePathFromDirectoryCanonical(fromDirectory string, to string, getCanonicalFileName GetCanonicalFileName) string {
	return CombinePathComponents(GetRelativePathComponentsFromDirectoryCanonical(fromDirectory, to, getCanonicalFileName))
}

func GetRelativePathComponentsFromDirectory(fromDirectory string, to string, ignoreCase bool) []string {
	comparer := EquateStringsCaseSensitive
	if ignoreCase {
		comparer = EquateStringsCaseInsensitive
	}
	return getPathComponentsRelativeTo(fromDirectory, to, comparer, identityFileName)
}

func GetRelativePathComponentsFromDirectoryCanonical(fromDirectory string, to string, getCanonicalFileName GetCanonicalFileName) []string {
	// When a canonicalizer is supplied the original leaves ignoreCase false, so
	// the comparer is the case-sensitive one; case folding is the
	// canonicalizer's job.
	return getPathComponentsRelativeTo(fromDirectory, to, EquateStringsCaseSensitive, getCanonicalFileName)
}

func identityFileName(fileName string) string { return fileName }

func EnsureTrailingDirectorySeparator(pathString string) string {
	sep := GetPathSeparator(pathString)
	if !HasTrailingDirectorySeparator(pathString) {
		return pathString + sep
	}

	return pathString
}

func HasTrailingDirectorySeparator(pathString string) bool {
	if len(pathString) == 0 {
		return false
	}

	ch := pathString[len(pathString)-1]
	return ch == '/' || ch == '\\'
}

func StripTrailingDirectorySeparator(pathString string) string {
	if !HasTrailingDirectorySeparator(pathString) {
		return pathString
	}
	return pathString[:len(pathString)-1]
}

// GetFileExtension corresponds to getFileExtension. The TypeScript defaults
// multiDotExtension to false.
func GetFileExtension(fileName string, multiDotExtension bool) string {
	if !multiDotExtension {
		return nodeExtname(fileName)
	}

	fileName = GetFileName(fileName)
	firstDotIndex := strings.Index(fileName, ".")
	// `slice(-1)` on a name with no dot returns the last character, because
	// indexOf answered -1.
	return sliceFrom(fileName, firstDotIndex)
}

func GetFileName(pathString string) string {
	return nodeBasename(pathString)
}

// GetShortenedFileName corresponds to getShortenedFileName. The TypeScript
// defaults maxDirLength to 15.
func GetShortenedFileName(pathString string, maxDirLength int) string {
	fileName := GetFileName(pathString)
	dirName := GetDirectoryPath(pathString)
	if len(dirName) > maxDirLength {
		return "..." + dirName[len(dirName)-maxDirLength:] + PathSep + fileName
	}
	return pathString
}

// StripFileExtension corresponds to stripFileExtension. The TypeScript defaults
// multiDotExtension to false.
func StripFileExtension(fileName string, multiDotExtension bool) string {
	ext := GetFileExtension(fileName, multiDotExtension)
	return fileName[:len(fileName)-len(ext)]
}

func NormalizePath(pathString string) string {
	return NormalizeSlashes(nodeNormalize(pathString))
}

// GetWildcardRegexPattern transforms a relative file spec (one that potentially
// contains the escape characters **, * or ?) and returns a regular expression
// pattern that can be used for matching against.
//
// The result is a JavaScript regular expression source string, byte for byte
// what the original produces. Compile it with CompileWildcardRegexPattern
// rather than regexp.Compile -- see that function for why.
func GetWildcardRegexPattern(rootPath string, fileSpec string) string {
	absolutePath := NormalizePath(CombinePaths(rootPath, fileSpec))
	if !HasPythonExtension(absolutePath) {
		absolutePath = EnsureTrailingDirectorySeparator(absolutePath)
	}

	pathComponents := GetPathComponents(absolutePath)

	escapedSeparator := GetRegexEscapedSeparator(GetPathSeparator(rootPath))
	doubleAsteriskRegexFragment := "(" + escapedSeparator + "[^" + escapedSeparator + "][^" + escapedSeparator + "]*)*?"

	// Strip the directory separator from the root component.
	if len(pathComponents) > 0 {
		pathComponents[0] = StripTrailingDirectorySeparator(pathComponents[0])

		if strings.HasPrefix(pathComponents[0], `\\`) {
			pathComponents[0] = `\\` + pathComponents[0]
		}
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

			regExPattern += replaceReservedCharacters(component, escapedSeparator)

			firstComponent = false
		}
	}

	return regExPattern
}

// replaceReservedCharacters stands in for
// `component.replace(new RegExp('[^\\w\\s' + sep + ']', 'g'), cb)`.
//
// It is written out rather than compiled because JavaScript's \w is ASCII-only
// while its \s includes every Unicode space separator plus U+FEFF, and Go's
// regexp agrees on neither. The character class is negated, so getting it wrong
// escapes too much or too little rather than failing loudly.
func replaceReservedCharacters(component string, escapedSeparator string) string {
	var sb strings.Builder
	for _, r := range component {
		switch {
		case r == '*':
			sb.WriteString("[^" + escapedSeparator + "]*")
		case r == '?':
			sb.WriteString("[^" + escapedSeparator + "]")
		case isJSRegexWordChar(r) || isJSRegexSpaceChar(r) || strings.ContainsRune(escapedSeparator, r):
			sb.WriteRune(r)
		default:
			// Escaping anything that is not a reserved character --
			// word / space / separator.
			sb.WriteByte('\\')
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// isJSRegexWordChar is JavaScript's \w, which is ASCII-only.
func isJSRegexWordChar(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// isJSRegexSpaceChar is JavaScript's \s: the Unicode space separators plus the
// ASCII whitespace controls, U+00A0, U+1680, U+2028, U+2029 and U+FEFF.
func isJSRegexSpaceChar(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// CompileWildcardRegexPattern compiles a pattern produced by
// GetWildcardRegexPattern.
//
// It is not regexp.Compile because the pattern may contain `\` before a
// non-ASCII character: any character outside JavaScript's ASCII-only \w and its
// \s is escaped, so a path component such as "日本" arrives as `\日\本`. That is
// a no-op escape in JavaScript and a syntax error in Go. Dropping the backslash
// is exactly equivalent, since a non-ASCII rune is already a literal in RE2.
//
// Everything else the generator emits -- the lazy `(...)*?` group, the negated
// character classes, and the ASCII escapes -- means the same thing in both.
func CompileWildcardRegexPattern(pattern string) (*regexp.Regexp, error) {
	var sb strings.Builder
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) && runes[i+1] >= 0x80 {
			sb.WriteRune(runes[i+1])
			i++
			continue
		}
		sb.WriteRune(runes[i])
	}
	return regexp.Compile(sb.String())
}

// IsDirectoryWildcardPatternPresent determines whether the file spec contains a
// directory wildcard pattern ("**").
func IsDirectoryWildcardPatternPresent(fileSpec string) bool {
	path := NormalizePath(fileSpec)
	pathComponents := GetPathComponents(path)

	for _, component := range pathComponents {
		if component == "**" {
			return true
		}
	}

	return false
}

// GetWildcardRoot returns the topmost path that contains no wildcard
// characters.
func GetWildcardRoot(rootPath string, fileSpec string) string {
	absolutePath := NormalizePath(CombinePaths(rootPath, fileSpec))
	if !HasPythonExtension(absolutePath) {
		absolutePath = EnsureTrailingDirectorySeparator(absolutePath)
	}

	pathComponents := GetPathComponents(absolutePath)
	sep := GetPathSeparator(absolutePath)

	// Strip the directory separator from the root component.
	if len(pathComponents) > 0 {
		pathComponents[0] = StripTrailingDirectorySeparator(pathComponents[0])
	}

	if len(pathComponents) == 1 && pathComponents[0] == "" {
		return sep
	}

	wildcardRoot := ""
	firstComponent := true

	for _, component := range pathComponents {
		if component == "**" {
			break
		}

		if containsWildcardRootChar(component) {
			break
		}

		if !firstComponent {
			component = sep + component
		}

		wildcardRoot += component
		firstComponent = false
	}

	return wildcardRoot
}

func HasPythonExtension(path string) bool {
	return strings.HasSuffix(path, ".py") || strings.HasSuffix(path, ".pyi")
}

// GetRegexEscapedSeparator corresponds to getRegexEscapedSeparator. The
// TypeScript defaults pathSep to path.sep.
func GetRegexEscapedSeparator(pathSep string) string {
	// We don't need to escape "/" in a TypeScript regular expression.
	if pathSep == "/" {
		return "/"
	}
	return `\\`
}

// IsRootedDiskPath determines whether a path is an absolute disk path (e.g.
// starts with `/`, or a DOS path like `c:`, `c:\` or `c:/`).
func IsRootedDiskPath(path string) bool {
	return GetRootLength(path) > 0
}

// IsDiskPathRoot determines whether a path consists only of a path root.
func IsDiskPathRoot(path string) bool {
	rootLength := GetRootLength(path)
	return rootLength > 0 && rootLength == len(path)
}

func getAnyExtensionFromPathWorker(path string, extensions []string, stringEqualityComparer func(a, b string) bool) string {
	for _, extension := range extensions {
		if result, ok := tryGetExtensionFromPath(path, extension, stringEqualityComparer); ok {
			return result
		}
	}
	return ""
}

func tryGetExtensionFromPath(path string, extension string, stringEqualityComparer func(a, b string) bool) (string, bool) {
	if !strings.HasPrefix(extension, ".") {
		extension = "." + extension
	}
	if len(path) >= len(extension) && path[len(path)-len(extension)] == '.' {
		pathExtension := path[len(path)-len(extension):]
		if stringEqualityComparer(pathExtension, extension) {
			return pathExtension, true
		}
	}

	return "", false
}

func getPathComponentsRelativeTo(
	from string,
	to string,
	stringEqualityComparer func(a, b string) bool,
	getCanonicalFileName GetCanonicalFileName,
) []string {
	fromComponents := GetPathComponents(from)
	toComponents := GetPathComponents(to)

	start := 0
	for ; start < len(fromComponents) && start < len(toComponents); start++ {
		fromComponent := getCanonicalFileName(fromComponents[start])
		toComponent := getCanonicalFileName(toComponents[start])
		comparer := stringEqualityComparer
		if start == 0 {
			comparer = EquateStringsCaseInsensitive
		}
		if !comparer(fromComponent, toComponent) {
			break
		}
	}

	if start == 0 {
		return toComponents
	}

	components := toComponents[start:]
	relative := []string{""}
	for ; start < len(fromComponents); start++ {
		relative = append(relative, "..")
	}
	return append(relative, components...)
}

// lastIndexOfString is JavaScript's String.prototype.lastIndexOf, which answers
// -1 rather than being an error to use in arithmetic.
func lastIndexOfString(s string, substr string) int {
	return strings.LastIndex(s, substr)
}

// sliceFrom is JavaScript's String.prototype.slice with a possibly negative
// index, which counts back from the end.
func sliceFrom(s string, index int) string {
	if index < 0 {
		index += len(s)
		if index < 0 {
			index = 0
		}
	}
	if index > len(s) {
		return ""
	}
	return s[index:]
}

/*
 * The three Node `path` primitives pathUtils.ts leans on. They are the POSIX
 * implementations, transliterated from Node's lib/path.js so the arithmetic
 * matches rather than being approximated by Go's path/filepath -- which
 * differs on, among other things, "" (filepath.Clean answers "." like Node,
 * but filepath.Base answers "." where Node answers "").
 */

// nodeNormalize is path.posix.normalize.
func nodeNormalize(path string) string {
	if len(path) == 0 {
		return "."
	}

	isAbsolute := path[0] == '/'
	trailingSeparator := path[len(path)-1] == '/'

	// Normalize the path.
	path = normalizeStringPosix(path, !isAbsolute)

	if len(path) == 0 {
		if isAbsolute {
			return "/"
		}
		if trailingSeparator {
			return "./"
		}
		return "."
	}
	if trailingSeparator {
		path += "/"
	}

	if isAbsolute {
		return "/" + path
	}
	return path
}

// normalizeStringPosix resolves . and .. elements in a path with directory
// names.
func normalizeStringPosix(path string, allowAboveRoot bool) string {
	res := ""
	lastSegmentLength := 0
	lastSlash := -1
	dots := 0
	var code byte

	for i := 0; i <= len(path); i++ {
		if i < len(path) {
			code = path[i]
		} else if code == '/' {
			break
		} else {
			code = '/'
		}

		if code == '/' {
			if lastSlash == i-1 || dots == 1 {
				// NOOP
			} else if dots == 2 {
				if len(res) < 2 || lastSegmentLength != 2 ||
					res[len(res)-1] != '.' || res[len(res)-2] != '.' {
					if len(res) > 2 {
						lastSlashIndex := strings.LastIndexByte(res, '/')
						if lastSlashIndex == -1 {
							res = ""
							lastSegmentLength = 0
						} else {
							res = res[:lastSlashIndex]
							lastSegmentLength = len(res) - 1 - strings.LastIndexByte(res, '/')
						}
						lastSlash = i
						dots = 0
						continue
					} else if len(res) != 0 {
						res = ""
						lastSegmentLength = 0
						lastSlash = i
						dots = 0
						continue
					}
				}
				if allowAboveRoot {
					if len(res) > 0 {
						res += "/.."
					} else {
						res = ".."
					}
					lastSegmentLength = 2
				}
			} else {
				if len(res) > 0 {
					res += "/" + path[lastSlash+1:i]
				} else {
					res = path[lastSlash+1 : i]
				}
				lastSegmentLength = i - lastSlash - 1
			}
			lastSlash = i
			dots = 0
		} else if code == '.' && dots != -1 {
			dots++
		} else {
			dots = -1
		}
	}

	return res
}

// nodeBasename is path.posix.basename with no suffix argument.
func nodeBasename(path string) string {
	start := 0
	end := -1
	matchedSlash := true

	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			// If we reached a path separator that was not part of a set of path
			// separators at the end of the string, stop now.
			if !matchedSlash {
				start = i + 1
				break
			}
		} else if end == -1 {
			// We saw the first non-path separator, mark this as the end of our
			// path component.
			matchedSlash = false
			end = i + 1
		}
	}

	if end == -1 {
		return ""
	}
	return path[start:end]
}

// nodeExtname is path.posix.extname.
func nodeExtname(path string) string {
	startDot := -1
	startPart := 0
	end := -1
	matchedSlash := true
	// Track the state of characters (if any) we see before our first dot and
	// after any path separator we find.
	preDotState := 0

	for i := len(path) - 1; i >= 0; i-- {
		code := path[i]
		if code == '/' {
			// If we reached a path separator that was not part of a set of path
			// separators at the end of the string, stop now.
			if !matchedSlash {
				startPart = i + 1
				break
			}
			continue
		}
		if end == -1 {
			// We saw the first non-path separator, mark this as the end of our
			// extension.
			matchedSlash = false
			end = i + 1
		}
		if code == '.' {
			// If this is our first dot, mark it as the start of our extension.
			if startDot == -1 {
				startDot = i
			} else if preDotState != 1 {
				preDotState = 1
			}
		} else if startDot != -1 {
			// We saw a non-dot and non-path separator before our dot, so we
			// should have a good chance at having a non-empty extension.
			preDotState = -1
		}
	}

	if startDot == -1 || end == -1 ||
		// We saw a non-dot character immediately before the dot.
		preDotState == 0 ||
		// The (right-most) trimmed path component is exactly '..'.
		(preDotState == 1 && startDot == end-1 && startDot == startPart+1) {
		return ""
	}
	return path[startDot:end]
}
