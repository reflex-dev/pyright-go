/*
 * importresolver_typeshed.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The typeshed lookups of analyzer/importResolver.ts (pyright 1.1.412).
 * See importresolver.go for how the file is split.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

func (r *ImportResolver) GetTypeshedStdLibPath(execEnv *ExecutionEnvironment) uri.Uri {
	return r.getStdlibTypeshedPath(
		r.configOptions.TypeshedPath,
		execEnv.PythonVersion,
		execEnv.PythonPlatform,
		nil, // logger
		nil, // moduleDescriptor
	)
}

func (r *ImportResolver) GetTypeshedThirdPartyPath(execEnv *ExecutionEnvironment) uri.Uri {
	return r.getThirdPartyTypeshedPath(r.configOptions.TypeshedPath, nil)
}

func (r *ImportResolver) GetTypeshedStdlibExcludeList(
	customTypeshedPath uri.Uri,
	pythonVersion common.PythonVersion,
	pythonPlatform string,
) []uri.Uri {
	typeshedStdlibPath := r.getStdlibTypeshedPath(customTypeshedPath, pythonVersion, pythonPlatform, nil, nil)
	excludes := []uri.Uri{}

	if typeshedStdlibPath == nil {
		return excludes
	}

	versions := r.typeshedInfoProvider.GetStdLibModuleVersionInfo(customTypeshedPath, nil)

	versions.ForEach(func(versionInfo SupportedVersionInfo, moduleName string) {
		shouldExcludeModule := false

		if versionInfo.Max != nil && pythonVersion.IsGreaterThan(*versionInfo.Max) {
			shouldExcludeModule = true
		}

		// `pythonPlatform !== undefined`; "" is the absence here.
		if pythonPlatform != "" {
			pythonPlatformLower := strings.ToLower(pythonPlatform)

			// If there are supported platforms listed, and we are not using one
			// of those supported platforms, exclude it.
			if versionInfo.SupportedPlatforms != nil {
				every := true
				for _, p := range versionInfo.SupportedPlatforms {
					if strings.ToLower(p) == pythonPlatformLower {
						every = false
						break
					}
				}
				if every {
					shouldExcludeModule = true
				}
			}

			// If there are unsupported platforms listed, see if we're using one
			// of them.
			if versionInfo.UnsupportedPlatforms != nil {
				for _, p := range versionInfo.UnsupportedPlatforms {
					if strings.ToLower(p) == pythonPlatformLower {
						shouldExcludeModule = true
						break
					}
				}
			}
		}

		if shouldExcludeModule {
			// Add excludes for both the ".pyi" file and the directory that
			// contains it (in case it's using an "__init__.pyi" file).
			moduleDirPath := typeshedStdlibPath.CombinePaths(strings.Split(moduleName, ".")...)
			excludes = append(excludes, moduleDirPath)

			moduleFilePath := moduleDirPath.ReplaceExtension(".pyi")
			excludes = append(excludes, moduleFilePath)
		}
	})

	return excludes
}

// findTypeshedPath returns nil where the original returns undefined.
func (r *ImportResolver) findTypeshedPath(
	execEnv *ExecutionEnvironment,
	moduleDescriptor ImportedModuleDescriptor,
	importName string,
	isStdLib bool,
	importLogger *ImportLogger,
) *ImportResult {
	folderName := ThirdPartyFolderName
	if isStdLib {
		folderName = StdLibFolderName
	}
	importLogger.Log("Looking for typeshed " + folderName + " path")

	var typeshedPaths []uri.Uri
	if isStdLib {
		path := r.getStdlibTypeshedPath(
			r.configOptions.TypeshedPath,
			execEnv.PythonVersion,
			execEnv.PythonPlatform,
			importLogger,
			&moduleDescriptor,
		)

		if path != nil {
			typeshedPaths = []uri.Uri{path}
		}
	} else {
		typeshedPaths = r.getThirdPartyTypeshedPackagePaths(moduleDescriptor, importLogger, true)
	}

	for _, typeshedPath := range typeshedPaths {
		if r.dirExistsCached(typeshedPath) {
			importInfo := r.resolveAbsoluteImport(
				nil, typeshedPath, execEnv, moduleDescriptor, importName, importLogger,
				false /* allowPartial */, false /* allowNativeLib */, false /* useStubPackage */, true /* allowPyi */, false, /* lookForPyTyped */
			)

			if importInfo != nil && importInfo.IsImportFound {
				importType := ImportTypeThirdParty
				if isStdLib {
					importType = ImportTypeBuiltIn
				}

				// Handle 'typing_extensions' as a special case because it's
				// part of stdlib typeshed stubs, but it's not part of stdlib.
				if importName == "typing_extensions" {
					importType = ImportTypeThirdParty
				}

				importInfo.ImportType = importType
				return importInfo
			}
		}
	}

	importLogger.Log("Typeshed path not found")
	return nil
}

// buildStdlibCache finds all of the stdlib modules and returns a Set containing
// all of their names.
func (r *ImportResolver) buildStdlibCache(stdlibRoot uri.Uri, executionEnvironment *ExecutionEnvironment) *common.OrderedSet[string] {
	cache := common.NewOrderedSet[string]()

	if stdlibRoot != nil {
		// The original's comment: directory-backed package entries are new.
		// Version/platform-gate them against the configured typeshed's
		// stdlib/VERSIONS metadata. This only checks VERSIONS metadata, not
		// on-disk file presence (we already confirmed that via existsSync at
		// the call site).
		addDirectoryStdlibModule := func(moduleName string) {
			if r.isStdlibTypeshedStubValidForVersion(
				CreateImportedModuleDescriptor(moduleName),
				r.configOptions.TypeshedPath,
				executionEnvironment.PythonVersion,
				executionEnvironment.PythonPlatform,
				nil,
			) {
				cache.Add(moduleName)
			}
		}

		// The original's comment: file-backed entries are added
		// unconditionally and are NOT version/platform-gated today.
		// Historically the file path passed the scan `root` to
		// _isStdlibTypeshedStubValidForVersion, which resolves to a
		// non-existent `stdlib/stdlib` directory and therefore yields an empty
		// VERSIONS map, so the check always returned true. We preserve that
		// behavior here (rather than changing which file-backed modules get
		// cached). Keeping this in a separate helper from
		// addDirectoryStdlibModule makes the file-vs-directory intent explicit
		// so a future maintainer doesn't "fix" the file path and silently drop
		// cache entries.
		addFileStdlibModule := func(moduleName string) {
			cache.Add(moduleName)
		}

		var readDir func(root uri.Uri, prefix string, hasPrefix bool)
		readDir = func(root uri.Uri, prefix string, hasPrefix bool) {
			entries, _ := r.fileSystemCache.ReaddirEntriesSync(root)
			for _, entry := range entries {
				if entry.IsDirectory() {
					dirRoot := root.CombinePaths(entry.Name())
					moduleName := entry.Name()
					if hasPrefix {
						moduleName = prefix + "." + entry.Name()
					}

					if !strings.HasPrefix(entry.Name(), "_") {
						packageInit := dirRoot.CombinePaths("__init__.pyi")
						if r.fileSystemCache.ExistsSync(packageInit) {
							addDirectoryStdlibModule(moduleName)
						}
					}

					readDir(dirRoot, moduleName, true)
				} else if strings.Contains(entry.Name(), ".py") {
					stripped := common.StripFileExtension(entry.Name(), false)
					// Skip anything starting with an underscore.
					if !strings.HasPrefix(stripped, "_") {
						if hasPrefix {
							addFileStdlibModule(prefix + "." + stripped)
						} else {
							addFileStdlibModule(stripped)
						}
					}
				}
			}
		}
		readDir(stdlibRoot, "", false)
	}

	return cache
}

// getStdlibTypeshedPath returns the directory for a module within the stdlib
// typeshed directory. If moduleDescriptor is provided, it is filtered based on
// the VERSIONS file in the typeshed stubs. It returns nil where the original
// returns undefined.
func (r *ImportResolver) getStdlibTypeshedPath(
	customTypeshedPath uri.Uri,
	pythonVersion common.PythonVersion,
	pythonPlatform string,
	importLogger *ImportLogger,
	moduleDescriptor *ImportedModuleDescriptor,
) uri.Uri {
	subdirectory := r.typeshedInfoProvider.GetTypeshedSubdirectory(true, customTypeshedPath, importLogger)
	if subdirectory != nil &&
		moduleDescriptor != nil &&
		!r.isStdlibTypeshedStubValidForVersion(*moduleDescriptor, customTypeshedPath, pythonVersion, pythonPlatform, importLogger) {
		return nil
	}

	return subdirectory
}

func (r *ImportResolver) getThirdPartyTypeshedPath(customTypeshedPath uri.Uri, importLogger *ImportLogger) uri.Uri {
	return r.typeshedInfoProvider.GetTypeshedSubdirectory(false, customTypeshedPath, importLogger)
}

func (r *ImportResolver) isStdlibTypeshedStubValidForVersion(
	moduleDescriptor ImportedModuleDescriptor,
	customTypeshedPath uri.Uri,
	pythonVersion common.PythonVersion,
	pythonPlatform string,
	importLogger *ImportLogger,
) bool {
	versions := r.typeshedInfoProvider.GetStdLibModuleVersionInfo(customTypeshedPath, importLogger)

	// Loop through the name parts to make sure the module and submodules
	// referenced in the import statement are valid for this version of Python.
	for namePartCount := 1; namePartCount <= len(moduleDescriptor.NameParts); namePartCount++ {
		namePartsToConsider := moduleDescriptor.NameParts[:namePartCount]
		versionInfo, ok := versions.Get(strings.Join(namePartsToConsider, "."))

		if ok {
			if pythonVersion.IsLessThan(versionInfo.Min) {
				return false
			}

			if versionInfo.Max != nil && pythonVersion.IsGreaterThan(*versionInfo.Max) {
				return false
			}

			if pythonPlatform != "" {
				pythonPlatformLower := strings.ToLower(pythonPlatform)

				if versionInfo.SupportedPlatforms != nil {
					every := true
					for _, p := range versionInfo.SupportedPlatforms {
						if strings.ToLower(p) == pythonPlatformLower {
							every = false
							break
						}
					}
					if every {
						return false
					}
				}

				if versionInfo.UnsupportedPlatforms != nil {
					for _, p := range versionInfo.UnsupportedPlatforms {
						if strings.ToLower(p) == pythonPlatformLower {
							return false
						}
					}
				}
			}
		}
	}

	return true
}

// getThirdPartyTypeshedPackagePaths returns nil where the original returns
// undefined. The TypeScript defaults includeMatchOnly to true.
func (r *ImportResolver) getThirdPartyTypeshedPackagePaths(
	moduleDescriptor ImportedModuleDescriptor,
	importLogger *ImportLogger,
	includeMatchOnly bool,
) []uri.Uri {
	packagePaths := r.typeshedInfoProvider.GetThirdPartyPackageMap(r.configOptions.TypeshedPath, importLogger).PackagePaths

	firstNamePart := ""
	if len(moduleDescriptor.NameParts) > 0 {
		firstNamePart = moduleDescriptor.NameParts[0]
	}
	if includeMatchOnly {
		paths, _ := packagePaths.Get(firstNamePart)
		return paths
	}

	if firstNamePart != "" {
		// `flatten(getMapValues(packagePaths, (k) => k.startsWith(firstNamePart)))`.
		out := []uri.Uri{}
		for _, k := range packagePaths.Keys() {
			if strings.HasPrefix(k, firstNamePart) {
				paths, _ := packagePaths.Get(k)
				out = append(out, paths...)
			}
		}
		return out
	}

	return []uri.Uri{}
}

func (r *ImportResolver) getThirdPartyTypeshedPackageRoots(importLogger *ImportLogger) []uri.Uri {
	return r.typeshedInfoProvider.GetThirdPartyPackageMap(r.configOptions.TypeshedPath, importLogger).Paths
}
