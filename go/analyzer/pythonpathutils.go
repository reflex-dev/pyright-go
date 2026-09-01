/*
 * pythonpathutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Utility routines used to resolve various paths in Python.
 *
 * Transliterated from analyzer/pythonPathUtils.ts (pyright 1.1.412).
 */

package analyzer

import (
	"regexp"
	"sort"
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// PythonPathResult corresponds to the interface of the same name. Prefix is
// `Uri | undefined`, so nil is the absence.
type PythonPathResult struct {
	Paths  []uri.Uri
	Prefix uri.Uri
}

const (
	StdLibFolderName     = "stdlib"
	ThirdPartyFolderName = "stubs"
)

// typeshedFallbackFS is the structural type `Pick<FileSystem, 'getModulePath' |
// 'existsSync' | 'realCasePath'>` the original narrows to here.
type typeshedFallbackFS interface {
	GetModulePath() uri.Uri
	ExistsSync(u uri.Uri) bool
	RealCasePath(u uri.Uri) uri.Uri
}

// GetTypeShedFallbackPath returns nil where the original returns undefined.
func GetTypeShedFallbackPath(fs typeshedFallbackFS) uri.Uri {
	moduleDirectory := fs.GetModulePath()
	if moduleDirectory == nil || moduleDirectory.IsEmpty() {
		return nil
	}

	typeshedPath := moduleDirectory.CombinePaths(common.TypeshedFallback)
	if fs.ExistsSync(typeshedPath) {
		return fs.RealCasePath(typeshedPath)
	}

	// In the debug version of Pyright, the code is one level deeper, so we need
	// to look one level up for the typeshed fallback.
	debugTypeshedPath := moduleDirectory.GetDirectory().CombinePaths(common.TypeshedFallback)
	if fs.ExistsSync(debugTypeshedPath) {
		return fs.RealCasePath(debugTypeshedPath)
	}

	return nil
}

func GetTypeshedSubdirectory(typeshedPath uri.Uri, isStdLib bool) uri.Uri {
	if isStdLib {
		return typeshedPath.CombinePaths(StdLibFolderName)
	}
	return typeshedPath.CombinePaths(ThirdPartyFolderName)
}

// FindPythonSearchPaths corresponds to the function of the same name. The last
// three parameters are optional in the original: a nil importLogger, a false
// includeWatchPathsOnly and a nil workspaceRoot are their absences.
func FindPythonSearchPaths(
	fs uri.FileSystem,
	configOptions *ConfigOptions,
	host Host,
	importLogger *ImportLogger,
	includeWatchPathsOnly bool,
	workspaceRoot uri.Uri,
) []uri.Uri {
	importLogger.Log("Finding python search paths")

	if configOptions.VenvPath != nil && configOptions.Venv != "" {
		venvDir := configOptions.Venv
		venvPath := configOptions.VenvPath.CombinePaths(venvDir)

		foundPaths := []uri.Uri{}
		sitePackagesPaths := []uri.Uri{}

		for _, libPath := range []string{common.Lib, common.Lib64, common.LibAlternate} {
			sitePackagesPath := findSitePackagesPath(
				fs,
				venvPath.CombinePaths(libPath),
				configOptions.DefaultPythonVersion,
				importLogger,
			)
			if sitePackagesPath != nil {
				foundPaths = AddPathIfUnique(foundPaths, sitePackagesPath)
				sitePackagesPaths = append(sitePackagesPaths, fs.RealCasePath(sitePackagesPath))
			}
		}

		// Now add paths from ".pth" files located in each of the site packages
		// folders.
		for _, sitePackagesPath := range sitePackagesPaths {
			for _, path := range GetPathsFromPthFiles(fs, sitePackagesPath) {
				foundPaths = AddPathIfUnique(foundPaths, path)
			}
		}

		if len(foundPaths) > 0 {
			if configOptions.PythonPath != nil {
				pathResult := host.GetPythonSearchPaths(configOptions.PythonPath, importLogger, configOptions.ProjectRoot)
				realVenvPath := fs.RealCasePath(venvPath)

				if pathResult.Prefix != nil && pathResult.Prefix.Equals(realVenvPath) {
					// The original's comment: a configured venv can still rely
					// on interpreter-reported stdlib/source roots that are not
					// site-packages. Preserve those roots so library-code
					// features can map typeshed stubs to the real stdlib
					// implementations while keeping site-packages controlled by
					// the configured venv.
					for _, path := range pathResult.Paths {
						realCasePath := fs.RealCasePath(path)
						if !realCasePath.PathEndsWith(common.SitePackages) &&
							!realCasePath.PathEndsWith(common.DistPackages) {
							foundPaths = AddPathIfUnique(foundPaths, realCasePath)
						}
					}
				}
			}

			importLogger.Log("Found the following '" + common.SitePackages + "' dirs")
			for _, path := range foundPaths {
				importLogger.Log("  " + path.String())
			}

			// Filter out any non-directory paths before returning.
			filtered := []uri.Uri{}
			for _, p := range foundPaths {
				if uri.IsDirectory(fs, p) {
					filtered = append(filtered, p)
				}
			}
			return filtered
		}

		importLogger.Log("Did not find any '" + common.SitePackages + "' dirs. Falling back on python interpreter.")
	}

	// Fall back on the python interpreter.
	pathResult := host.GetPythonSearchPaths(configOptions.PythonPath, importLogger, configOptions.ProjectRoot)
	if includeWatchPathsOnly && workspaceRoot != nil && !workspaceRoot.IsEmpty() {
		paths := []uri.Uri{}
		for _, p := range pathResult.Paths {
			if !p.StartsWith(workspaceRoot) || p.StartsWith(pathResult.Prefix) {
				paths = append(paths, fs.RealCasePath(p))
			}
		}
		return paths
	}

	// Host already filters out non-directory paths.
	paths := make([]uri.Uri, 0, len(pathResult.Paths))
	for _, p := range pathResult.Paths {
		paths = append(paths, fs.RealCasePath(p))
	}
	return paths
}

func IsPythonBinary(p string) bool {
	p = strings.TrimSpace(p)
	return p == "python" || p == "python3"
}

// findSitePackagesPath returns nil where the original returns undefined.
func findSitePackagesPath(
	fs uri.FileSystem,
	libPath uri.Uri,
	pythonVersion *common.PythonVersion,
	importLogger *ImportLogger,
) uri.Uri {
	if fs.ExistsSync(libPath) {
		importLogger.Log("Found path '" + libPath.String() + "'; looking for " + common.SitePackages)
	} else {
		importLogger.Log("Did not find '" + libPath.String() + "'")
		return nil
	}

	sitePackagesPath := libPath.CombinePaths(common.SitePackages)
	if fs.ExistsSync(sitePackagesPath) {
		importLogger.Log("Found path '" + sitePackagesPath.String() + "'")
		return sitePackagesPath
	}
	importLogger.Log("Did not find '" + sitePackagesPath.String() + "', so looking for python subdirectory")

	// We didn't find a site-packages directory directly in the lib directory.
	// Scan for a "python3.X" directory instead.
	entries := uri.GetFileSystemEntries(fs, libPath)

	// Candidate directories start with "python3.".
	candidateDirs := []uri.Uri{}
	for _, dirName := range entries.Directories {
		if strings.HasPrefix(dirName.FileName(), "python3.") {
			dirPath := dirName.CombinePaths(common.SitePackages)
			if fs.ExistsSync(dirPath) {
				candidateDirs = append(candidateDirs, dirName)
			}
		}
	}

	// If there is a python3.X directory (where 3.X matches the configured
	// python version), prefer that over other python directories.
	if pythonVersion != nil {
		wanted := "python" + pythonVersion.ToMajorMinorString()
		for _, dirName := range candidateDirs {
			if dirName.FileName() == wanted {
				dirPath := dirName.CombinePaths(common.SitePackages)
				importLogger.Log("Found path '" + dirPath.String() + "'")
				return dirPath
			}
		}
	}

	// If there was no python version or we didn't find an exact match, use the
	// first directory that starts with "python". Most of the time, there will
	// be only one.
	if len(candidateDirs) > 0 {
		dirPath := candidateDirs[0].CombinePaths(common.SitePackages)
		importLogger.Log("Found path '" + dirPath.String() + "'")
		return dirPath
	}

	return nil
}

// pthImportLine is `/^import\s/`, which readPthSearchPaths uses to skip the
// executable lines of a .pth file.
var pthImportLine = regexp.MustCompile(`^import\s`)

func ReadPthSearchPaths(pthFile uri.Uri, fs uri.FileSystem) []uri.Uri {
	searchPaths := []uri.Uri{}

	if fs.ExistsSync(pthFile) {
		data, err := fs.ReadFileSync(pthFile)
		if err != nil {
			return searchPaths
		}
		// `data.split(/\r?\n/)`.
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		for _, line := range lines {
			trimmedLine := common.TrimJSString(line)
			if len(trimmedLine) > 0 && !strings.HasPrefix(trimmedLine, "#") && !pthImportLine.MatchString(trimmedLine) {
				pthPath := pthFile.GetDirectory().CombinePaths(trimmedLine)
				if fs.ExistsSync(pthPath) && uri.IsDirectory(fs, pthPath) {
					searchPaths = append(searchPaths, fs.RealCasePath(pthPath))
				}
			}
		}
	}

	return searchPaths
}

func GetPathsFromPthFiles(fs uri.FileSystem, parentDir uri.Uri) []uri.Uri {
	searchPaths := []uri.Uri{}

	// Get a list of all *.pth files within the specified directory.
	entries, err := fs.ReaddirEntriesSync(parentDir)
	if err != nil {
		// The original lets readdirEntriesSync throw here, which is a real
		// difference: getPathsFromPthFiles has no try/catch, so a missing
		// directory propagates to its caller. Both callers -- findPythonSearchPaths
		// and ensureDefaultExtraPaths -- only reach it for a directory they
		// have already confirmed exists, so the throw is unreachable and an
		// empty result is the same answer.
		return searchPaths
	}

	pthFiles := []uri.Dirent{}
	for _, entry := range entries {
		if (entry.IsFile() || entry.IsSymbolicLink()) && strings.HasSuffix(entry.Name(), ".pth") {
			pthFiles = append(pthFiles, entry)
		}
	}
	sort.SliceStable(pthFiles, func(i, j int) bool { return pthFiles[i].Name() < pthFiles[j].Name() })

	for _, pthFile := range pthFiles {
		filePath := fs.RealCasePath(parentDir.CombinePaths(pthFile.Name()))
		fileStats, ok := uri.TryStat(fs, filePath)

		// Skip all files that are much larger than expected.
		if ok && fileStats.IsFile() && fileStats.Size() > 0 && fileStats.Size() < 64*1024 {
			searchPaths = append(searchPaths, ReadPthSearchPaths(filePath, fs)...)
		}
	}

	return searchPaths
}

// AddPathIfUnique corresponds to the function of the same name. The original
// mutates the array and reports whether it grew; every caller ignores the
// report, so this returns the list instead.
func AddPathIfUnique(pathList []uri.Uri, pathToAdd uri.Uri) []uri.Uri {
	for _, path := range pathList {
		if path.Key() == pathToAdd.Key() {
			return pathList
		}
	}
	return append(pathList, pathToAdd)
}
