/*
 * parentdirectorycache.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Cache to hold parent directory import result to make sure we don't repeatedly
 * search folders.
 *
 * Transliterated from analyzer/parentDirectoryCache.ts (pyright 1.1.412).
 */

package analyzer

import "github.com/microsoft/pyright/go/common/uri"

// ImportPath corresponds to the type of the same name: a box around
// `Uri | undefined`.
//
// The box is load-bearing. checked() records that a directory was searched
// whether or not anything was found, so getImportResult has to tell "searched,
// found nothing" from "never searched" -- which a bare nil Uri could not do.
type ImportPath struct {
	ImportPath uri.Uri
}

// parentDirCacheEntry corresponds to the local CacheEntry type.
type parentDirCacheEntry struct {
	ImportResult *ImportResult
	Path         uri.Uri
	ImportName   string
}

// ParentDirectoryCache corresponds to the class of the same name.
type ParentDirectoryCache struct {
	importChecked map[string]map[string]*ImportPath
	cachedResults map[string]map[string]*ImportResult

	// libPathCache is `Uri[] | undefined`; nil is the uncomputed state, which
	// reset() restores.
	libPathCache []uri.Uri

	importRootGetter func() []uri.Uri
}

func NewParentDirectoryCache(importRootGetter func() []uri.Uri) *ParentDirectoryCache {
	return &ParentDirectoryCache{
		importChecked:    map[string]map[string]*ImportPath{},
		cachedResults:    map[string]map[string]*ImportResult{},
		importRootGetter: importRootGetter,
	}
}

// GetImportResult returns nil where the original returns undefined.
func (c *ParentDirectoryCache) GetImportResult(path uri.Uri, importName string, importResult *ImportResult) *ImportResult {
	if result := c.cachedResults[importName][path.Key()]; result != nil {
		// We already checked for the importName at the path.
		return result
	}

	if checked := c.importChecked[importName][path.Key()]; checked != nil {
		// We already checked for the importName at the path.
		if checked.ImportPath == nil {
			return importResult
		}

		if cached := c.cachedResults[importName][checked.ImportPath.Key()]; cached != nil {
			return cached
		}
		return importResult
	}

	return nil
}

func (c *ParentDirectoryCache) CheckValidPath(fs uri.FileSystem, sourceFileUri uri.Uri, root uri.Uri) bool {
	if !sourceFileUri.StartsWith(root) {
		// We don't search containing folders for libs.
		return false
	}

	if c.libPathCache == nil {
		cache := []uri.Uri{}
		for _, r := range c.importRootGetter() {
			realCase := fs.RealCasePath(r)
			if !realCase.Equals(root) && realCase.StartsWith(root) {
				cache = append(cache, realCase)
			}
		}
		c.libPathCache = cache
	}

	for _, p := range c.libPathCache {
		if sourceFileUri.StartsWith(p) {
			// Make sure it is not lib folders under user code root.
			// ex) .venv folder
			return false
		}
	}

	return true
}

func (c *ParentDirectoryCache) Checked(path uri.Uri, importName string, importPath *ImportPath) {
	byPath, ok := c.importChecked[importName]
	if !ok {
		byPath = map[string]*ImportPath{}
		c.importChecked[importName] = byPath
	}
	byPath[path.Key()] = importPath
}

func (c *ParentDirectoryCache) Add(importResult *ImportResult, path uri.Uri, importName string) {
	byPath, ok := c.cachedResults[importName]
	if !ok {
		byPath = map[string]*ImportResult{}
		c.cachedResults[importName] = byPath
	}
	byPath[path.Key()] = importResult
}

func (c *ParentDirectoryCache) Reset() {
	c.importChecked = map[string]map[string]*ImportPath{}
	c.cachedResults = map[string]map[string]*ImportResult{}
	c.libPathCache = nil
}
