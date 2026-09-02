/*
 * serviceutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/serviceUtils.ts (pyright 1.1.412).
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// FindPyprojectTomlFileHereOrUp returns nil where the original returns
// undefined.
func FindPyprojectTomlFileHereOrUp(fs uri.ReadOnlyFileSystem, searchPath uri.Uri) uri.Uri {
	return uri.ForEachAncestorDirectory(searchPath, func(ancestor uri.Uri) uri.Uri {
		return FindPyprojectTomlFile(fs, ancestor)
	})
}

func FindPyprojectTomlFile(fs uri.ReadOnlyFileSystem, searchPath uri.Uri) uri.Uri {
	fileName := searchPath.ResolvePaths(common.PyprojectTomlName)
	if fs.ExistsSync(fileName) {
		return fs.RealCasePath(fileName)
	}
	return nil
}

func FindConfigFileHereOrUp(fs uri.ReadOnlyFileSystem, searchPath uri.Uri) uri.Uri {
	return uri.ForEachAncestorDirectory(searchPath, func(ancestor uri.Uri) uri.Uri {
		return FindConfigFile(fs, ancestor)
	})
}

func FindConfigFile(fs uri.ReadOnlyFileSystem, searchPath uri.Uri) uri.Uri {
	fileName := searchPath.ResolvePaths(common.ConfigFileName)
	if fs.ExistsSync(fileName) {
		return fs.RealCasePath(fileName)
	}

	return nil
}
