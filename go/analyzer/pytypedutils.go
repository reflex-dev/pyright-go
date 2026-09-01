/*
 * pytypedutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Parser for py.typed files.
 *
 * Transliterated from analyzer/pyTypedUtils.ts (pyright 1.1.412).
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common/uri"
)

// PyTypedInfo corresponds to the interface of the same name.
type PyTypedInfo struct {
	PyTypedPath      uri.Uri
	IsPartiallyTyped bool
}

// GetPyTypedInfo retrieves information about a py.typed file, if it exists,
// under the given path. It returns nil where the original returns undefined.
func GetPyTypedInfo(fileSystem uri.FileSystem, dirPath uri.Uri) *PyTypedInfo {
	if !fileSystem.ExistsSync(dirPath) || !uri.IsDirectory(fileSystem, dirPath) {
		return nil
	}

	pyTypedPath := dirPath.PytypedUri()
	if !fileSystem.ExistsSync(pyTypedPath) || !uri.IsFile(fileSystem, pyTypedPath, false) {
		return nil
	}

	return GetPyTypedInfoForPyTypedFile(fileSystem, pyTypedPath)
}

// GetPyTypedInfoForPyTypedFile retrieves information about a py.typed file. The
// pyTypedPath provided must be a valid path.
//
// The original's comment: this function intentionally doesn't check whether the
// given py.typed path exists or not, as filesystem access is expensive if done
// repeatedly. The caller should verify the file's validity before calling this
// method and use a cache if possible to avoid high filesystem access costs.
//
// statSync throws there if the path is not valid, which is the caller's problem
// either way; here the error is treated as a zero-size file, which takes the
// same branch.
func GetPyTypedInfoForPyTypedFile(fileSystem uri.FileSystem, pyTypedPath uri.Uri) *PyTypedInfo {
	isPartiallyTyped := false

	// Read the contents of the file as text.
	fileStats, err := fileSystem.StatSync(pyTypedPath)

	// Do a quick sanity check on the size before we attempt to read it. This
	// file should always be really small - typically zero bytes in length.
	if err == nil && fileStats.Size() > 0 && fileStats.Size() < 64*1024 {
		pyTypedContents, err := fileSystem.ReadFileSync(pyTypedPath)
		if err == nil {
			// PEP 561 doesn't specify the format of "py.typed" in any detail
			// other than to say that "If a stub package is partial it MUST
			// include partial\n in a top level py.typed file."
			contents := string(pyTypedContents)
			if strings.Contains(contents, "partial\n") || strings.Contains(contents, "partial\r\n") {
				isPartiallyTyped = true
			}
		}
	}

	return &PyTypedInfo{
		PyTypedPath:      pyTypedPath,
		IsPartiallyTyped: isPartiallyTyped,
	}
}
