/*
 * uriutils_filesystem.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * The half of common/uri/uriUtils.ts (pyright 1.1.412) that takes a
 * ReadOnlyFileSystem. The pure half is in uriutils.go.
 *
 * The original wraps most of these in try/catch and answers undefined or an
 * empty result on failure, because Node's fs throws for a missing path. The Go
 * file system reports the same conditions as errors instead of panicking, so
 * each catch becomes an error check at the same place and with the same
 * fallback. Nothing here can fail in a way the original would not have caught.
 */

package uri

import "sort"

// MakeDirectories creates a directory hierarchy for a path, starting from some
// ancestor path.
func MakeDirectories(fs FileSystem, dir Uri, startingFrom Uri) error {
	if !dir.StartsWith(startingFrom) {
		return nil
	}

	pathComponents := dir.GetPathComponents()
	relativeToComponents := startingFrom.GetPathComponents()
	curPath := startingFrom

	for i := len(relativeToComponents); i < len(pathComponents); i++ {
		curPath = curPath.CombinePaths(pathComponents[i])
		if !fs.ExistsSync(curPath) {
			if err := fs.MkdirSync(curPath, MkDirOptions{Recursive: true}); err != nil {
				return err
			}
		}
	}
	return nil
}

func GetFileSize(fs ReadOnlyFileSystem, u Uri) int64 {
	if stat, ok := TryStat(fs, u); ok && stat.IsFile() {
		return stat.Size()
	}
	return 0
}

func FileExists(fs ReadOnlyFileSystem, u Uri) bool {
	return fileSystemEntryExists(fs, u, fileSystemEntryKindFile)
}

func DirectoryExists(fs ReadOnlyFileSystem, u Uri) bool {
	return fileSystemEntryExists(fs, u, fileSystemEntryKindDirectory)
}

func IsDirectory(fs ReadOnlyFileSystem, u Uri) bool {
	stat, ok := TryStat(fs, u)
	return ok && stat.IsDirectory()
}

// IsUsableDirectory reports whether u resolves to an existing directory on the
// given file system.
//
// The original's comment: `fs` is an explicit parameter so callers that decide
// a usable cwd/workspace root must name the file system they are validating
// against, rather than relying on a coincidental match between independent fs
// handles.
func IsUsableDirectory(fs ReadOnlyFileSystem, u Uri) bool {
	return fs.ExistsSync(u) && IsDirectory(fs, u)
}

// GetUsableUriPath returns the file-system path of u only when it is usable as
// a working directory: it is defined, has a non-empty file path, and resolves
// to an existing directory on fs. Otherwise it returns ("", false), where the
// original returns undefined.
func GetUsableUriPath(fs ReadOnlyFileSystem, u Uri) (string, bool) {
	if u == nil {
		return "", false
	}

	uriPath := u.GetFilePath()
	if uriPath == "" {
		return "", false
	}

	if IsUsableDirectory(fs, u) {
		return uriPath, true
	}
	return "", false
}

// IsFile corresponds to the function of the same name. The TypeScript defaults
// treatZipDirectoryAsFile to false.
func IsFile(fs ReadOnlyFileSystem, u Uri, treatZipDirectoryAsFile bool) bool {
	stats, ok := TryStat(fs, u)
	if ok && stats.IsFile() {
		return true
	}

	if !treatZipDirectoryAsFile {
		return false
	}

	return ok && stats.IsZipDirectory()
}

// TryStat returns (nil, false) where the TypeScript returns undefined.
func TryStat(fs ReadOnlyFileSystem, u Uri) (Stats, bool) {
	if fs.ExistsSync(u) {
		stats, err := fs.StatSync(u)
		if err != nil {
			return nil, false
		}
		return stats, true
	}
	return nil, false
}

// TryRealpath returns nil where the TypeScript returns undefined.
func TryRealpath(fs ReadOnlyFileSystem, u Uri) Uri {
	result, err := fs.RealpathSync(u)
	if err != nil {
		return nil
	}
	return result
}

func GetFileSystemEntries(fs ReadOnlyFileSystem, u Uri) FileSystemEntries {
	entries, err := fs.ReaddirEntriesSync(u)
	if err != nil {
		return FileSystemEntries{Files: []Uri{}, Directories: []Uri{}}
	}
	return getFileSystemEntriesWithSymlinkedDirectoriesFromDirEntries(entries, fs, u).FileSystemEntries
}

func GetFileSystemEntriesWithSymlinkedDirectories(fs ReadOnlyFileSystem, u Uri) FileSystemEntriesWithSymlinkedDirectories {
	entries, err := fs.ReaddirEntriesSync(u)
	if err != nil {
		return FileSystemEntriesWithSymlinkedDirectories{
			FileSystemEntries:    FileSystemEntries{Files: []Uri{}, Directories: []Uri{}},
			SymlinkedDirectories: []Uri{},
		}
	}
	return getFileSystemEntriesWithSymlinkedDirectoriesFromDirEntries(entries, fs, u)
}

// GetFileSystemEntriesFromDirEntries sorts the entries into files and
// directories, including any symbolic links.
func GetFileSystemEntriesFromDirEntries(dirEntries []Dirent, fs ReadOnlyFileSystem, u Uri) FileSystemEntries {
	return getFileSystemEntriesWithSymlinkedDirectoriesFromDirEntries(dirEntries, fs, u).FileSystemEntries
}

func getFileSystemEntriesWithSymlinkedDirectoriesFromDirEntries(
	dirEntries []Dirent,
	fs ReadOnlyFileSystem,
	u Uri,
) FileSystemEntriesWithSymlinkedDirectories {
	entries := append([]Dirent{}, dirEntries...)
	// The original's comparator is a plain three-way on name, and
	// Array.prototype.sort is stable, so this is sort.SliceStable.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	files := []Uri{}
	directories := []Uri{}
	symlinkedDirectories := []Uri{}

	for _, entry := range entries {
		// This is necessary because on some file systems node fails to exclude
		// "." and "..". See https://github.com/nodejs/node/issues/4002
		if entry.Name() == "." || entry.Name() == ".." {
			continue
		}

		entryUri := u.CombinePaths(entry.Name())
		switch {
		case entry.IsFile():
			files = append(files, entryUri)
		case entry.IsDirectory():
			directories = append(directories, entryUri)
		case entry.IsSymbolicLink():
			stat, ok := TryStat(fs, entryUri)
			if ok && stat.IsFile() {
				files = append(files, entryUri)
			} else if ok && stat.IsDirectory() {
				directories = append(directories, entryUri)
				symlinkedDirectories = append(symlinkedDirectories, entryUri)
			}
		}
	}

	return FileSystemEntriesWithSymlinkedDirectories{
		FileSystemEntries:    FileSystemEntries{Files: files, Directories: directories},
		SymlinkedDirectories: symlinkedDirectories,
	}
}

type fileSystemEntryKind int

const (
	fileSystemEntryKindFile fileSystemEntryKind = iota
	fileSystemEntryKindDirectory
)

func fileSystemEntryExists(fs ReadOnlyFileSystem, u Uri, entryKind fileSystemEntryKind) bool {
	stat, err := fs.StatSync(u)
	if err != nil {
		return false
	}
	switch entryKind {
	case fileSystemEntryKindFile:
		return stat.IsFile()
	case fileSystemEntryKindDirectory:
		return stat.IsDirectory()
	}
	return false
}

// ConvertUriToLspUriString converts to a URI string that the LSP client
// understands; mapped files are only local to the server.
func ConvertUriToLspUriString(fs ReadOnlyFileSystem, u Uri) string {
	return fs.GetOriginalUri(u).String()
}
