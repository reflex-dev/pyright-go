/*
 * importresolverfilesystem.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/importResolverFileSystem.ts (pyright 1.1.412).
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// cachedDir corresponds to the interface of the same name. The original's
// fields are ReadonlyMap / ReadonlySet / a plain array; nothing mutates them
// after _getCachedDir returns, so they are ordinary values here.
type cachedDir struct {
	EntriesByName   map[string]uri.Dirent
	EntriesArray    []uri.Dirent
	ResolvableNames *common.OrderedSet[string]
}

// CreateImportResolverFileSystem corresponds to the function of the same name.
func CreateImportResolverFileSystem(fileSystem uri.FileSystem) ImportResolverFileSystem {
	return &importResolverFileSystemImpl{
		fileSystem:                fileSystem,
		cachedDirInfoForPath:      map[string]*cachedDir{},
		cachedFilesForPath:        map[string][]uri.Uri{},
		cachedDirExistenceForRoot: map[string]bool{},
	}
}

type importResolverFileSystemImpl struct {
	fileSystem uri.FileSystem

	cachedDirInfoForPath      map[string]*cachedDir
	cachedFilesForPath        map[string][]uri.Uri
	cachedDirExistenceForRoot map[string]bool
}

var _ ImportResolverFileSystem = (*importResolverFileSystemImpl)(nil)

func (f *importResolverFileSystemImpl) InvalidateCache() {
	f.cachedDirInfoForPath = map[string]*cachedDir{}
	f.cachedFilesForPath = map[string][]uri.Uri{}
	f.cachedDirExistenceForRoot = map[string]bool{}
}

func (f *importResolverFileSystemImpl) ReaddirEntriesSync(u uri.Uri) ([]uri.Dirent, error) {
	// The original returns the cached array directly; a directory that could
	// not be read is cached as empty rather than as an error, which is what
	// swallowing the exception in _getCachedDir amounts to.
	return f.getCachedDir(u).EntriesArray, nil
}

func (f *importResolverFileSystemImpl) GetResolvableNamesInDirectory(dirPath uri.Uri) *common.OrderedSet[string] {
	return f.getCachedDir(dirPath).ResolvableNames
}

func (f *importResolverFileSystemImpl) FileExists(u uri.Uri) bool {
	directory := u.GetDirectory()
	if directory.Equals(u) {
		// Started at root, so this can't be a file.
		return false
	}

	cached := f.getCachedDir(directory)
	entry := cached.EntriesByName[u.FileName()]
	if entry != nil && entry.IsFile() {
		return true
	}

	if entry != nil && entry.IsSymbolicLink() {
		realPath := uri.TryRealpath(f.fileSystem, u)
		if realPath != nil && f.fileSystem.ExistsSync(realPath) && uri.IsFile(f.fileSystem, realPath, false) {
			return true
		}
	}

	return false
}

func (f *importResolverFileSystemImpl) DirExists(u uri.Uri) bool {
	parent := u.GetDirectory()
	if parent.Equals(u) {
		// Started at root. No entries to read, so we have to check ourselves.
		if cachedExistence, ok := f.cachedDirExistenceForRoot[u.Key()]; ok {
			return cachedExistence
		}

		stat, ok := uri.TryStat(f.fileSystem, u)
		exists := ok && stat.IsDirectory()
		f.cachedDirExistenceForRoot[u.Key()] = exists
		return exists
	}

	cached := f.getCachedDir(parent)
	entry := cached.EntriesByName[u.FileName()]
	if entry != nil && entry.IsDirectory() {
		return true
	}

	if entry != nil && entry.IsSymbolicLink() {
		realPath := uri.TryRealpath(f.fileSystem, u)
		if realPath != nil && f.fileSystem.ExistsSync(realPath) && uri.IsDirectory(f.fileSystem, realPath) {
			return true
		}
	}

	return false
}

func (f *importResolverFileSystemImpl) GetFilesInDirectory(dirPath uri.Uri) []uri.Uri {
	if cachedValue, ok := f.cachedFilesForPath[dirPath.Key()]; ok {
		return cachedValue
	}

	newCacheValue := []uri.Uri{}
	entriesInDir := f.getCachedDir(dirPath)
	filesInDir := []uri.Dirent{}

	// Add any files or symbolic links that point to files.
	for _, entry := range entriesInDir.EntriesArray {
		if entry.IsFile() {
			filesInDir = append(filesInDir, entry)
		} else if entry.IsSymbolicLink() {
			if stat, ok := uri.TryStat(f.fileSystem, dirPath.CombinePaths(entry.Name())); ok && stat.IsFile() {
				filesInDir = append(filesInDir, entry)
			}
		}
	}

	for _, file := range filesInDir {
		newCacheValue = append(newCacheValue, dirPath.CombinePaths(file.Name()))
	}

	f.cachedFilesForPath[dirPath.Key()] = newCacheValue
	return newCacheValue
}

func (f *importResolverFileSystemImpl) ExistsSync(u uri.Uri) bool {
	return f.fileSystem.ExistsSync(u)
}

func (f *importResolverFileSystemImpl) ReadFileSync(u uri.Uri) ([]byte, error) {
	return f.fileSystem.ReadFileSync(u)
}

func (f *importResolverFileSystemImpl) StatSync(u uri.Uri) (uri.Stats, error) {
	return f.fileSystem.StatSync(u)
}

func (f *importResolverFileSystemImpl) RealCasePath(u uri.Uri) uri.Uri {
	return f.fileSystem.RealCasePath(u)
}

func (f *importResolverFileSystemImpl) GetModulePath() uri.Uri {
	return f.fileSystem.GetModulePath()
}

func (f *importResolverFileSystemImpl) getCachedDir(dirPath uri.Uri) *cachedDir {
	if cachedValue, ok := f.cachedDirInfoForPath[dirPath.Key()]; ok {
		return cachedValue
	}

	entriesByName := map[string]uri.Dirent{}
	resolvableNames := common.NewOrderedSet[string]()
	entriesArray := []uri.Dirent{}

	// The original wraps this in a try/catch that swallows the error, so an
	// unreadable directory is cached as an empty one.
	if entries, err := f.fileSystem.ReaddirEntriesSync(dirPath); err == nil {
		entriesArray = entries

		for _, entry := range entries {
			entriesByName[entry.Name()] = entry

			isFile := entry.IsFile()
			isDirectory := entry.IsDirectory()
			if entry.IsSymbolicLink() {
				stat, ok := uri.TryStat(f.fileSystem, dirPath.CombinePaths(entry.Name()))
				isFile = ok && stat.IsFile()
				isDirectory = ok && stat.IsDirectory()
			}

			resolvableName := entry.Name()
			if isFile {
				resolvableName = common.StripFileExtension(entry.Name(), true)
			}
			resolvableNames.Add(resolvableName)

			if isDirectory && strings.HasSuffix(entry.Name(), common.StubsSuffix) {
				resolvableNames.Add(resolvableName[:len(resolvableName)-len(common.StubsSuffix)])
			}
		}
	}

	frozen := &cachedDir{
		EntriesByName:   entriesByName,
		EntriesArray:    entriesArray,
		ResolvableNames: resolvableNames,
	}

	f.cachedDirInfoForPath[dirPath.Key()] = frozen
	return frozen
}
