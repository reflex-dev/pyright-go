/*
 * sourceenumerator.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Logic for enumerating all of the Python source files in a project.
 *
 * Transliterated from analyzer/sourceEnumerator.ts (pyright 1.1.412).
 *
 * The enumeration is an explicit work list rather than recursion so it can be
 * suspended on a time limit and resumed; that structure is preserved exactly,
 * including the reversals -- `include.slice(0).reverse()` and
 * `directories.slice().reverse()` -- which exist so that popping from the back
 * of the stack visits in the original order.
 */

package analyzer

import (
	"strconv"
	"time"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// SourceEnumerateResult corresponds to the interface of the same name.
type SourceEnumerateResult struct {
	Matches          *common.OrderedMap[string, uri.Uri]
	AutoExcludedDirs []uri.Uri
	IsComplete       bool
}

// envMarkers are the files whose presence marks a directory as a virtual
// environment.
var envMarkers = [][]string{{"bin", "activate"}, {"Scripts", "activate"}, {"pyvenv.cfg"}, {"conda-meta"}}

type dirToExplore struct {
	Uri                  uri.Uri
	IncludeRegExp        uri.Regexp
	HasDirectoryWildcard bool
}

// SourceEnumerator corresponds to the class of the same name.
type SourceEnumerator struct {
	excludes        []uri.FileSpec
	autoExcludeVenv bool
	fs              uri.FileSystem
	console         common.ConsoleInterface

	elapsedTimeInMs          int64
	includesToExplore        []uri.FileSpec
	dirsToExplore            []dirToExplore
	matches                  *common.OrderedMap[string, uri.Uri]
	autoExcludeDirs          []uri.Uri
	isComplete               bool
	numFilesVisited          int
	loggedLongOperationError bool
	seenDirs                 map[string]bool

	// symlinkedDirectoryRoots carries the original's comment: this tracks
	// symlinked directory roots across the entire enumeration cycle, potentially
	// spanning multiple include roots, so Pylance can later filter workspace
	// indexing against the full discovered set.
	symlinkedDirectoryRoots *common.OrderedMap[string, uri.Uri]
}

func NewSourceEnumerator(
	include []uri.FileSpec,
	excludes []uri.FileSpec,
	autoExcludeVenv bool,
	fs uri.FileSystem,
	console common.ConsoleInterface,
) *SourceEnumerator {
	// `include.slice(0).reverse()`.
	includesToExplore := make([]uri.FileSpec, 0, len(include))
	for i := len(include) - 1; i >= 0; i-- {
		includesToExplore = append(includesToExplore, include[i])
	}

	e := &SourceEnumerator{
		excludes:                excludes,
		autoExcludeVenv:         autoExcludeVenv,
		fs:                      fs,
		console:                 console,
		includesToExplore:       includesToExplore,
		matches:                 common.NewOrderedMap[string, uri.Uri](),
		autoExcludeDirs:         []uri.Uri{},
		seenDirs:                map[string]bool{},
		symlinkedDirectoryRoots: common.NewOrderedMap[string, uri.Uri](),
	}

	console.Log("Searching for source files")
	return e
}

func (e *SourceEnumerator) GetSymlinkedDirectoryRoots() []uri.Uri {
	return e.symlinkedDirectoryRoots.Values()
}

// Enumerate enumerates as many files as possible within the specified time
// limit and returns all matching files.
func (e *SourceEnumerator) Enumerate(timeLimitInMs int64) SourceEnumerateResult {
	startTime := time.Now()

	for !e.isComplete {
		if e.doNext() {
			if !e.isComplete {
				e.finish()
			}
		}

		elapsedTime := time.Since(startTime).Milliseconds()
		if timeLimitInMs > 0 && elapsedTime > timeLimitInMs {
			break
		}
	}

	e.elapsedTimeInMs += time.Since(startTime).Milliseconds()

	if !e.loggedLongOperationError {
		const longOperationLimitInMs = 10000
		const nFilesToSuggestSubfolder = 50

		// The original's comment: if this is taking a long time, log an error to
		// help the user diagnose and mitigate the problem.
		if e.elapsedTimeInMs >= longOperationLimitInMs && e.numFilesVisited >= nFilesToSuggestSubfolder {
			e.console.Error("Enumeration of workspace source files is taking longer than " +
				strconv.FormatFloat(longOperationLimitInMs*0.001, 'f', -1, 64) + " seconds.\n" +
				"This may be because:\n" +
				"* You have opened your home directory or entire hard drive as a workspace\n" +
				"* Your workspace contains a very large number of directories and files\n" +
				"* Your workspace contains a symlink to a directory with many files\n" +
				"* Your workspace is remote, and file enumeration is slow\n" +
				"To reduce this time, open a workspace directory with fewer files " +
				`or add a pyrightconfig.json configuration file with an "exclude" section to exclude ` +
				"subdirectories from your workspace. For more details, refer to " +
				"https://github.com/microsoft/pyright/blob/main/docs/configuration.md.")

			e.loggedLongOperationError = true
		}
	}

	return SourceEnumerateResult{
		Matches:          e.matches,
		AutoExcludedDirs: e.autoExcludeDirs,
		IsComplete:       e.isComplete,
	}
}

func (e *SourceEnumerator) recordSymlinkedDirectoryRoot(root uri.Uri) {
	for _, existingRoot := range e.symlinkedDirectoryRoots.Values() {
		if root.IsChild(existingRoot) {
			return
		}
	}

	for _, key := range e.symlinkedDirectoryRoots.Keys() {
		existingRoot, _ := e.symlinkedDirectoryRoots.Get(key)
		if existingRoot.IsChild(root) {
			e.symlinkedDirectoryRoots.Delete(key)
		}
	}

	e.symlinkedDirectoryRoots.Set(root.Key(), root)
}

// doNext performs the next enumeration action. It returns true if complete.
func (e *SourceEnumerator) doNext() bool {
	if len(e.dirsToExplore) > 0 {
		dir := e.dirsToExplore[len(e.dirsToExplore)-1]
		e.dirsToExplore = e.dirsToExplore[:len(e.dirsToExplore)-1]
		e.exploreDir(dir)
		return false
	}

	if len(e.includesToExplore) > 0 {
		include := e.includesToExplore[len(e.includesToExplore)-1]
		e.includesToExplore = e.includesToExplore[:len(e.includesToExplore)-1]
		e.exploreInclude(include)
		return false
	}

	return true
}

func (e *SourceEnumerator) exploreDir(dir dirToExplore) {
	realDirPath := uri.TryRealpath(e.fs, dir.Uri)
	if realDirPath == nil {
		e.console.Warn(`Skipping broken link "` + dir.Uri.String() + `"`)
		return
	}

	if realDirPath.Key() != dir.Uri.Key() {
		e.recordSymlinkedDirectoryRoot(dir.Uri)
	}

	if e.seenDirs[realDirPath.Key()] {
		e.console.Info(`Skipping recursive symlink "` + dir.Uri.String() + `" -> "` + realDirPath.String() + `"`)
		return
	}
	e.seenDirs[realDirPath.Key()] = true

	if e.autoExcludeVenv {
		for _, marker := range envMarkers {
			if e.fs.ExistsSync(dir.Uri.ResolvePaths(marker...)) {
				e.autoExcludeDirs = append(e.autoExcludeDirs, dir.Uri)
				e.console.Info("Auto-excluding " + dir.Uri.ToUserVisibleString())
				return
			}
		}
	}

	entries := uri.GetFileSystemEntriesWithSymlinkedDirectories(e.fs, dir.Uri)

	for _, symlinkedDir := range entries.SymlinkedDirectories {
		e.recordSymlinkedDirectoryRoot(symlinkedDir)
	}

	for _, file := range entries.Files {
		if uri.FileSpecMatchIncludeFileSpec(dir.IncludeRegExp, e.excludes, file, true) {
			e.numFilesVisited++
			e.matches.Set(file.Key(), file)
		}
	}

	// `directories.slice().reverse()`, so that popping visits them in order.
	for i := len(entries.Directories) - 1; i >= 0; i-- {
		subDir := entries.Directories[i]
		if subDir.MatchesRegex(dir.IncludeRegExp) || dir.HasDirectoryWildcard {
			if !uri.FileSpecIsInPath(subDir, e.excludes) {
				e.dirsToExplore = append(e.dirsToExplore, dirToExplore{
					Uri:                  subDir,
					IncludeRegExp:        dir.IncludeRegExp,
					HasDirectoryWildcard: dir.HasDirectoryWildcard,
				})
			}
		}
	}
}

func (e *SourceEnumerator) exploreInclude(includeSpec uri.FileSpec) {
	if uri.FileSpecIsInPath(includeSpec.WildcardRoot, e.excludes) {
		return
	}

	e.seenDirs = map[string]bool{}

	// The original's comment: skip enumeration for non-file URI schemes (e.g.,
	// memfs:, zowe-uss:). These require async file system access that isn't
	// available here.
	scheme := includeSpec.WildcardRoot.Scheme()
	if scheme != "file" && scheme != "" {
		e.console.Info(`Skipping file enumeration for non-file URI scheme "` + scheme + `".`)
		return
	}

	stat, ok := uri.TryStat(e.fs, includeSpec.WildcardRoot)
	switch {
	case ok && stat.IsFile():
		e.matches.Set(includeSpec.WildcardRoot.Key(), includeSpec.WildcardRoot)
	case ok && stat.IsDirectory():
		e.dirsToExplore = append(e.dirsToExplore, dirToExplore{
			Uri:                  includeSpec.WildcardRoot,
			IncludeRegExp:        includeSpec.RegExp,
			HasDirectoryWildcard: includeSpec.HasDirectoryWildcard,
		})
	default:
		e.console.Error(`File or directory "` + includeSpec.WildcardRoot.ToUserVisibleString() + `" does not exist.`)
	}
}

func (e *SourceEnumerator) finish() {
	e.isComplete = true

	fileCount := e.matches.Size()
	if fileCount == 0 {
		e.console.Info("No source files found.")
	} else {
		noun := "files"
		if fileCount == 1 {
			noun = "file"
		}
		e.console.Info("Found " + strconv.Itoa(fileCount) + " source " + noun)
	}
}
