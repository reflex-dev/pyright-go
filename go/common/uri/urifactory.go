/*
 * urifactory.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * The factory half of the `Uri` namespace: create, file, parse and
 * defaultWorkspace, plus the two module-level helpers they share.
 *
 * Transliterated from common/uri/uri.ts (pyright 1.1.412). The predicates and
 * constants from the same namespace are in uri.go, which was written when only
 * ConstantUri and EmptyUri existed.
 *
 * Every factory in the original is overloaded on `IServiceProvider |
 * CaseSensitivityDetector` and immediately narrows to the detector. Go has no
 * untagged unions and the DI plumbing is out of scope, so these take the
 * detector directly.
 */

package uri

import (
	"os"
	"regexp"
	"strings"

	"github.com/microsoft/pyright/go/common"
)

var (
	dosPathRegex = regexp.MustCompile(`^/[a-zA-Z]:/`)

	windowsUriRegEx = regexp.MustCompile(`^[a-zA-Z]:\\?`)
	uriSchemeRegEx  = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:/?/?`)
)

// getFilePathOf returns just the fsPath path portion of a vscode URI.
func getFilePathOf(u *vsURI) string {
	var filePath string

	// Compute the file path ourselves. The vscode.URI class doesn't treat UNC
	// shares with a single slash as UNC paths.
	// https://github.com/microsoft/vscode-uri/blob/53e4ca6263f2e4ddc35f5360c62bc1b1d30f27dd/src/uri.ts#L567
	if u.authority != "" && len(u.path) == 1 && u.path[0] == '/' {
		filePath = "//" + u.authority + u.path
	} else {
		// Otherwise use the vscode.URI version.
		filePath = u.getFsPath()
	}

	// If this is a DOS-style path with a drive letter, remove the leading slash.
	if dosPathRegex.MatchString(filePath) {
		filePath = filePath[1:]
	}

	// vscode.URI normalizes the path to use the correct path separators. We
	// need to do the same.
	if isWindows {
		filePath = strings.ReplaceAll(filePath, "/", "\\")
	}

	return filePath
}

// normalizedUri is the { uri, str } pair normalizeUri returns.
type normalizedUri struct {
	uri *vsURI
	str string
}

// normalizeUri normalizes an input URI: it gets rid of '..' and '.' in the
// path, and removes any '/' on the end. The original notes this is slow but
// should only be called when the URI is first created.
func normalizeUri(u *vsURI) normalizedUri {
	// Original URI may not have resolved all the `..` in the path, so remove
	// them. Note: this also has the effect of removing any trailing slashes.
	finalURI := u
	if len(u.path) > 0 {
		finalURI = utilsResolvePath(u)
	}
	return normalizedUri{uri: finalURI, str: finalURI.String()}
}

// MaybeUri corresponds to Uri.maybeUri.
func MaybeUri(value string) bool {
	return uriSchemeRegEx.MatchString(value) && !windowsUriRegEx.MatchString(value)
}

// Create corresponds to Uri.create. The TypeScript defaults checkRelative to
// false.
func Create(value string, detector common.CaseSensitivityDetector, checkRelative bool) Uri {
	if MaybeUri(value) {
		return Parse(value, detector)
	}

	return File(value, detector, checkRelative)
}

// File corresponds to Uri.file. The TypeScript defaults checkRelative to false.
func File(path string, detector common.CaseSensitivityDetector, checkRelative bool) Uri {
	// Fix path if we're checking for relative paths and this is not a rooted
	// path.
	if checkRelative && !common.IsRootedDiskPath(path) {
		cwd, err := os.Getwd()
		if err != nil {
			panic(err)
		}
		path = common.CombinePaths(cwd, path)
	}

	// If this already starts with 'file:', then we can parse it normally: it is
	// actually a uri string. Otherwise parse it as a file path.
	var normalized normalizedUri
	if strings.HasPrefix(path, "file:") {
		normalized = normalizeUri(vsURIParse(path, false))
	} else {
		normalized = normalizeUri(vsURIFile(common.NormalizeSlashes(path)))
	}

	// Turn the path into a file URI.
	return CreateFileUri(
		getFilePathOf(normalized.uri),
		normalized.uri.query,
		normalized.uri.fragment,
		normalized.str,
		detector.IsCaseSensitive(normalized.str),
	)
}

// Parse corresponds to Uri.parse. The empty string stands in for the
// `string | undefined` argument, which the original tests for truthiness.
func Parse(uriStr string, detector common.CaseSensitivityDetector) Uri {
	if uriStr == "" {
		return Empty()
	}

	// Normalize the value here. This gets rid of '..' and '.' in the path. It
	// also removes any '/' on the end of the path.
	normalized := normalizeUri(vsURIParse(uriStr, false))
	if normalized.uri.scheme == FileUriSchema {
		return CreateFileUri(
			getFilePathOf(normalized.uri),
			normalized.uri.query,
			normalized.uri.fragment,
			normalized.str,
			detector.IsCaseSensitive(normalized.str),
		)
	}

	// Web URIs are always case sensitive.
	return CreateWebUri(
		normalized.uri.scheme,
		normalized.uri.authority,
		normalized.uri.path,
		normalized.uri.query,
		normalized.uri.fragment,
		normalized.str,
	)
}

// DefaultWorkspace corresponds to Uri.defaultWorkspace.
func DefaultWorkspace(detector common.CaseSensitivityDetector) Uri {
	return File(DefaultWorkspaceRootPath, detector, false)
}

// Is corresponds to Uri.is, which in the TypeScript sniffs for a string _key.
func Is(thing any) bool {
	if thing == nil {
		return false
	}
	_, ok := thing.(Uri)
	return ok
}
