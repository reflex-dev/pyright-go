/*
 * readonlyaugmentedfilesystem.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * A file system that lets one augment a backing file system but not modify the
 * backing file system.
 *
 * Transliterated from readonlyAugmentedFileSystem.ts (pyright 1.1.412), which
 * lives at the top of src/ rather than under analyzer/. It is here because
 * PartialStubService is, and that reaches ExecutionEnvironment.
 *
 * The writes throw "Operation is not allowed." in the original. They return an
 * error here rather than panicking, because the Go FileSystem interface already
 * reports errors and no caller of these ever catches -- so a returned error and
 * a thrown one reach the same place.
 */

package analyzer

import (
	"errors"

	"github.com/microsoft/pyright/go/common/uri"
)

// errOperationNotAllowed is the original's `new Error('Operation is not
// allowed.')`.
var errOperationNotAllowed = errors.New("Operation is not allowed.")

// errPathDoesNotExist is statSync's `new Error('ENOENT: path does not exist')`.
var errPathDoesNotExist = errors.New("ENOENT: path does not exist")

// mappedEntry corresponds to the interface of the same name.
type mappedEntry struct {
	MappedUri   uri.Uri
	OriginalUri uri.Uri
	Filter      uri.MapDirectoryFilter
}

// ReadOnlyAugmentedFileSystem corresponds to the class of the same name.
//
// PyrightFileSystem subclasses it and overrides six methods. TypeScript's
// `super` is a `self` pointer here, the same device the binder uses for
// ParseTreeWalker: the base methods that must reach an override go through
// `self`, which each constructor sets.
type ReadOnlyAugmentedFileSystem struct {
	// realFS is `protected realFS` in the original.
	realFS uri.FileSystem

	// self is the outermost subclass, for the methods PyrightFileSystem
	// overrides.
	self uri.FileSystem

	// entryMap maps a mapped (fake location) directory to an original
	// directory; reverseEntryMap goes the other way.
	entryMap        *uri.UriMap[*mappedEntry]
	reverseEntryMap *uri.UriMap[*mappedEntry]
}

var _ uri.FileSystem = (*ReadOnlyAugmentedFileSystem)(nil)

func NewReadOnlyAugmentedFileSystem(realFS uri.FileSystem) *ReadOnlyAugmentedFileSystem {
	fs := &ReadOnlyAugmentedFileSystem{
		realFS:          realFS,
		entryMap:        uri.NewUriMap[*mappedEntry](),
		reverseEntryMap: uri.NewUriMap[*mappedEntry](),
	}
	fs.self = fs
	return fs
}

func (fs *ReadOnlyAugmentedFileSystem) ExistsSync(u uri.Uri) bool {
	if fs.isOriginalPath(u) {
		// Pretend original files don't exist anymore. They are only in their
		// mapped location.
		return false
	}

	return fs.realFS.ExistsSync(fs.getInternalOriginalUri(u))
}

func (fs *ReadOnlyAugmentedFileSystem) MkdirSync(u uri.Uri, options uri.MkDirOptions) error {
	return errOperationNotAllowed
}

func (fs *ReadOnlyAugmentedFileSystem) Chdir(u uri.Uri) {
	panic(errOperationNotAllowed)
}

func (fs *ReadOnlyAugmentedFileSystem) ReaddirEntriesSync(u uri.Uri) ([]uri.Dirent, error) {
	// Stick all entries in a map by name to make sure we don't have duplicates.
	entries := uri.NewDirentMap()

	// Handle the case where the directory has children that are remappings.
	// Example:
	//   uri: /lib/site-packages
	//   mapping: /lib/site-packages/foo -> /lib/site-packages/foo-stubs
	// We should show 'foo' as a directory in this case.
	for _, key := range fs.entryMap.Keys() {
		if key.IsChild(u) && len(key.GetRelativePathComponents(u)) == 1 {
			entries.Set(key.FileName(), uri.NewVirtualDirent(key.FileName(), false, u.GetFilePath()))
		}
	}

	// Handle the case where we're looking at a mapped directory (or a child).
	// Example:
	//   uri: /lib/site-packages/foo/module
	//   mapping: /lib/site-packages/foo -> /lib/site-packages/foo-stubs
	// We should list all of the children of /lib/site-packages/foo-stubs/module.
	if mapped := fs.getOriginalEntry(u); mapped != nil {
		originalUri := fs.getInternalOriginalUri(u)
		originalEntries, err := fs.realFS.ReaddirEntriesSync(originalUri)
		if err != nil {
			return nil, err
		}
		for _, entry := range originalEntries {
			originalEntryUri := originalUri.CombinePaths(entry.Name())
			if !mapped.Filter(originalEntryUri, fs.realFS) {
				continue
			}

			// The original's comment: mapped entries are virtual, so resolve
			// types that readdir cannot classify, including symlinks and
			// DT_UNKNOWN entries.
			isFile := entry.IsFile()
			isDirectory := entry.IsDirectory()
			if !isFile && !isDirectory {
				stat, ok := uri.TryStat(fs.realFS, originalEntryUri)
				if !ok {
					continue
				}
				isFile = stat.IsFile()
				isDirectory = stat.IsDirectory()
			}
			if !isFile && !isDirectory {
				continue
			}

			entries.Set(entry.Name(), uri.NewVirtualDirent(entry.Name(), isFile, u.GetFilePath()))
		}
	}

	if fs.realFS.ExistsSync(u) {
		// Get our real entries, but filter out entries that are mapped to a
		// different location.
		// Example:
		//   uri: /lib/site-packages/foo-stubs
		//   mapping: /lib/site-packages/foo -> /lib/site-packages/foo-stubs
		// We should list all of the children of /lib/site-packages/foo-stubs
		// but only if they don't match the filter.
		realEntries, err := fs.realFS.ReaddirEntriesSync(u)
		if err != nil {
			return nil, err
		}
		for _, entry := range realEntries {
			if fs.isOriginalPath(u.CombinePaths(entry.Name())) {
				continue
			}
			entries.Set(entry.Name(), entry)
		}
	}

	return entries.Values(), nil
}

func (fs *ReadOnlyAugmentedFileSystem) ReaddirSync(u uri.Uri) ([]string, error) {
	entries, err := fs.self.ReaddirEntriesSync(u)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, p := range entries {
		names = append(names, p.Name())
	}
	return names, nil
}

func (fs *ReadOnlyAugmentedFileSystem) ReadFileSync(u uri.Uri) ([]byte, error) {
	return fs.realFS.ReadFileSync(fs.getInternalOriginalUri(u))
}

func (fs *ReadOnlyAugmentedFileSystem) WriteFileSync(u uri.Uri, data []byte) error {
	return errOperationNotAllowed
}

func (fs *ReadOnlyAugmentedFileSystem) StatSync(u uri.Uri) (uri.Stats, error) {
	if fs.isOriginalPath(u) {
		// Pretend original files don't exist anymore. They are only in their
		// mapped location.
		return nil, errPathDoesNotExist
	}
	return fs.realFS.StatSync(fs.getInternalOriginalUri(u))
}

func (fs *ReadOnlyAugmentedFileSystem) RmdirSync(u uri.Uri) error {
	return errOperationNotAllowed
}

func (fs *ReadOnlyAugmentedFileSystem) UnlinkSync(u uri.Uri) error {
	return errOperationNotAllowed
}

func (fs *ReadOnlyAugmentedFileSystem) RealpathSync(u uri.Uri) (uri.Uri, error) {
	if fs.entryMap.Has(u) {
		return u, nil
	}

	return fs.realFS.RealpathSync(u)
}

func (fs *ReadOnlyAugmentedFileSystem) GetModulePath() uri.Uri {
	return fs.realFS.GetModulePath()
}

func (fs *ReadOnlyAugmentedFileSystem) CopyFileSync(src uri.Uri, dst uri.Uri) error {
	return errOperationNotAllowed
}

func (fs *ReadOnlyAugmentedFileSystem) RealCasePath(u uri.Uri) uri.Uri {
	return fs.realFS.RealCasePath(u)
}

// IsMappedUri reports whether the file is mapped to another location.
func (fs *ReadOnlyAugmentedFileSystem) IsMappedUri(fileUri uri.Uri) bool {
	if fs.getOriginalEntry(fileUri) != nil {
		return true
	}
	return fs.realFS.IsMappedUri(fileUri)
}

// GetOriginalUri gets the original filepath if the given filepath is mapped.
func (fs *ReadOnlyAugmentedFileSystem) GetOriginalUri(mappedFileUri uri.Uri) uri.Uri {
	internalUri := fs.getInternalOriginalUri(mappedFileUri)
	return fs.realFS.GetOriginalUri(internalUri)
}

// GetMappedUri gets the mapped filepath if the given filepath is mapped.
func (fs *ReadOnlyAugmentedFileSystem) GetMappedUri(originalFileUri uri.Uri) uri.Uri {
	entry := fs.getMappedEntry(originalFileUri)
	if entry == nil {
		return fs.realFS.GetMappedUri(originalFileUri)
	}
	relative := entry.OriginalUri.GetRelativePathComponents(originalFileUri)
	return entry.MappedUri.CombinePaths(relative...)
}

func (fs *ReadOnlyAugmentedFileSystem) IsInZip(u uri.Uri) bool {
	return fs.realFS.IsInZip(u)
}

// MapDirectory records a mapping and returns the handle that undoes it. A nil
// filter is the original's `filter ?? (() => true)`.
func (fs *ReadOnlyAugmentedFileSystem) MapDirectory(mappedUri uri.Uri, originalUri uri.Uri, filter uri.MapDirectoryFilter) uri.Disposable {
	if filter == nil {
		filter = func(uri.Uri, uri.FileSystem) bool { return true }
	}
	entry := &mappedEntry{OriginalUri: originalUri, MappedUri: mappedUri, Filter: filter}
	fs.entryMap.Set(mappedUri, entry)
	fs.reverseEntryMap.Set(originalUri, entry)
	return uri.DisposableFunc(func() {
		fs.entryMap.Delete(mappedUri)
		fs.reverseEntryMap.Delete(originalUri)
	})
}

// Clear corresponds to the protected method of the same name.
func (fs *ReadOnlyAugmentedFileSystem) Clear() {
	fs.entryMap.Clear()
	fs.reverseEntryMap.Clear()
}

// findClosestMatch searches through the map of directories to find the closest
// match: the longest path that is a parent of the uri.
func (fs *ReadOnlyAugmentedFileSystem) findClosestMatch(u uri.Uri, m *uri.UriMap[*mappedEntry]) *mappedEntry {
	for {
		if entry, ok := m.Get(u); ok {
			return entry
		}

		parent := u.GetDirectory()
		if parent.Equals(u) {
			return nil
		}

		u = parent
	}
}

func (fs *ReadOnlyAugmentedFileSystem) getOriginalEntry(u uri.Uri) *mappedEntry {
	return fs.findClosestMatch(u, fs.entryMap)
}

// getInternalOriginalUri returns the original uri if the given uri is a mapped
// uri in this file system's internal mapping. GetOriginalUri is different in
// that it will also ask the realFS if it has a mapping too.
func (fs *ReadOnlyAugmentedFileSystem) getInternalOriginalUri(u uri.Uri) uri.Uri {
	entry := fs.getOriginalEntry(u)
	if entry == nil {
		return u
	}
	relative := entry.MappedUri.GetRelativePathComponents(u)
	original := entry.OriginalUri.CombinePaths(relative...)

	// Make sure this original URI passes the filter too.
	if entry.Filter(original, fs.realFS) {
		return original
	}

	return u
}

func (fs *ReadOnlyAugmentedFileSystem) getMappedEntry(u uri.Uri) *mappedEntry {
	reverseMatch := fs.findClosestMatch(u, fs.reverseEntryMap)

	// Uri in this case is an original Uri. It should also match the filter.
	if reverseMatch != nil && reverseMatch.Filter(u, fs.realFS) {
		return reverseMatch
	}
	return nil
}

// isOriginalPath reports whether the uri is a child of, or equal to, any
// reverse entry.
func (fs *ReadOnlyAugmentedFileSystem) isOriginalPath(u uri.Uri) bool {
	return fs.getMappedEntry(u) != nil
}
