/*
 * importresolver_resolve.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The resolution algorithm of analyzer/importResolver.ts (pyright 1.1.412).
 * See importresolver.go for how the file is split.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// resolveImportInternal resolves the import and returns the path if it exists,
// otherwise returns undefined.
func (r *ImportResolver) resolveImportInternal(
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	moduleDescriptor ImportedModuleDescriptor,
) *ImportResult {
	importName := FormatImportName(moduleDescriptor)
	importResult := r.resolveImportStrict(importName, sourceFileUri, execEnv, moduleDescriptor, nil)

	if importResult.IsImportFound || moduleDescriptor.LeadingDots > 0 {
		return importResult
	}

	// The original's comment: if the import is absolute and no other method
	// works, try resolving the absolute in the importing file's directory, then
	// the parent directory, and so on, until the import root is reached.
	origin := sourceFileUri.GetDirectory()

	if result := r.cachedParentImportResults.GetImportResult(origin, importName, importResult); result != nil {
		// Already ran the parent directory resolution for this import name on
		// this location.
		return r.filterImplicitImports(result, moduleDescriptor.ImportedSymbols)
	}

	// Check whether the given file is in the parent directory import resolution
	// cache.
	root := GetParentImportResolutionRoot(sourceFileUri, execEnv.Root)
	if !r.cachedParentImportResults.CheckValidPath(r.fileSystem, sourceFileUri, root) {
		return importResult
	}

	var importLogger *ImportLogger
	if r.configOptions.VerboseOutput != nil && *r.configOptions.VerboseOutput {
		importLogger = NewImportLogger()
	}

	importPath := &ImportPath{}

	// Going up the given folder one by one until we can resolve the import.
	current := origin
	for r.shouldWalkUp(current, root, execEnv) && current != nil {
		result := r.resolveAbsoluteImport(
			sourceFileUri,
			current,
			execEnv,
			moduleDescriptor,
			importName,
			importLogger,
			false, // allowPartial
			false, // allowNativeLib
			false, // useStubPackage
			true,  // allowPyi
			false, // lookForPyTyped
		)

		r.cachedParentImportResults.Checked(current, importName, importPath)

		if result != nil && result.IsImportFound {
			// This will make the cache point to the actual path that contains
			// the module we found.
			importPath.ImportPath = current

			r.cachedParentImportResults.Add(result, current, importName)

			return r.filterImplicitImports(result, moduleDescriptor.ImportedSymbols)
		}

		current = r.tryWalkUp(current)
	}

	if current != nil {
		r.cachedParentImportResults.Checked(current, importName, importPath)
	}

	if importLogger != nil {
		for _, diag := range importLogger.GetLogs() {
			r.console.Log(diag)
		}
	}

	return importResult
}

func (r *ImportResolver) addResultsToCache(
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	importName string,
	importResult *ImportResult,
	moduleDescriptor *ImportedModuleDescriptor,
	fromUserFile bool,
) *ImportResult {
	// If the import is relative, include the source file path in the key.
	var relativeSourceFileUri uri.Uri
	if moduleDescriptor != nil && moduleDescriptor.LeadingDots > 0 {
		relativeSourceFileUri = sourceFileUri
	}

	key := execEnvCacheKey(execEnv)
	cache, ok := r.cachedImportResults.Get(key)
	if !ok {
		cache = common.NewOrderedMap[string, *ImportResult]()
		r.cachedImportResults.Set(key, cache)
	}
	cache.Set(r.getImportCacheKey(relativeSourceFileUri, importName, fromUserFile), importResult)

	var importedSymbols *common.OrderedSet[string]
	if moduleDescriptor != nil {
		importedSymbols = moduleDescriptor.ImportedSymbols
	}
	return r.filterImplicitImports(importResult, importedSymbols)
}

// execEnvCacheKey is `execEnv.root?.key`, used as a Map key. The original's
// addResultsToCache and getModuleNameForImport leave it undefined for a
// rootless environment while _lookUpResultsInCache spells it `?? ”` -- the two
// agree because a JavaScript Map treats undefined as its own key and nothing
// else produces the empty string.
func execEnvCacheKey(execEnv *ExecutionEnvironment) string {
	if execEnv.Root == nil {
		return ""
	}
	return execEnv.Root.Key()
}

// resolveAbsoluteImport follows the import resolution algorithm defined in
// PEP-420: https://www.python.org/dev/peps/pep-0420/
//
// The TypeScript defaults allowPartial, allowNativeLib and useStubPackage to
// false, allowPyi to true, and lookForPyTyped to false.
func (r *ImportResolver) resolveAbsoluteImport(
	sourceFileUri uri.Uri,
	rootPath uri.Uri,
	execEnv *ExecutionEnvironment,
	moduleDescriptor ImportedModuleDescriptor,
	importName string,
	importLogger *ImportLogger,
	allowPartial bool,
	allowNativeLib bool,
	useStubPackage bool,
	allowPyi bool,
	lookForPyTyped bool,
) *ImportResult {
	// Before we do additional work, see if this directory can possibly resolve
	// this import.
	if !r.isPossibleImportDir(rootPath, moduleDescriptor) {
		return nil
	}

	if allowPyi && useStubPackage {
		// The original's comment: look for packaged stubs first. PEP 561
		// indicates that package authors can ship their stubs separately from
		// their package implementation by appending the string '-stubs' to its
		// top-level directory name. We'll look there first.
		importResult := r.resolveAbsoluteImportImpl(
			rootPath, execEnv, moduleDescriptor, importName, importLogger,
			allowPartial, false /* allowNativeLib */, true /* useStubPackage */, true /* allowPyi */, true, /* lookForPyTyped */
		)

		// We found fully typed stub packages.
		if importResult.PackageDirectory != nil {
			// If this is a namespace package that wasn't resolved, assume that
			// it's a partial stub package and continue looking for a real
			// package.
			if !importResult.IsNamespacePackage || importResult.IsImportFound {
				return importResult
			}
		}
	}

	return r.resolveAbsoluteImportImpl(
		rootPath, execEnv, moduleDescriptor, importName, importLogger,
		allowPartial, allowNativeLib, false /* useStubPackage */, allowPyi, lookForPyTyped,
	)
}

// filterImplicitImports potentially modifies the ImportResult by removing some
// or all of the implicit import entries. Only the imported symbols should be
// included.
//
// `Object.assign({}, importResult)` is a shallow copy, which is what the struct
// copy below is.
func (r *ImportResolver) filterImplicitImports(importResult *ImportResult, importedSymbols *common.OrderedSet[string]) *ImportResult {
	if importedSymbols == nil {
		newImportResult := *importResult
		newImportResult.FilteredImplicitImports = nil
		return &newImportResult
	}

	// The original repeats the `importedSymbols === undefined` test here, which
	// is dead after the branch above; only the size test can fire.
	if importedSymbols.Size() == 0 {
		return importResult
	}

	if importResult.ImplicitImports == nil || importResult.ImplicitImports.Size() == 0 {
		return importResult
	}

	filteredImplicitImports := common.NewOrderedMap[string, *ImplicitImport]()
	importResult.ImplicitImports.ForEach(func(implicitImport *ImplicitImport, _ string) {
		if importedSymbols.Has(implicitImport.Name) {
			filteredImplicitImports.Set(implicitImport.Name, implicitImport)
		}
	})

	if filteredImplicitImports.Size() == importResult.ImplicitImports.Size() {
		return importResult
	}

	newImportResult := *importResult
	newImportResult.FilteredImplicitImports = filteredImplicitImports
	return &newImportResult
}

// findImplicitImports returns nil where the original returns undefined.
func (r *ImportResolver) findImplicitImports(importingModuleName string, dirPath uri.Uri, exclusions []uri.Uri) *common.OrderedMap[string, *ImplicitImport] {
	implicitImportMap := common.NewOrderedMap[string, *ImplicitImport]()

	// Enumerate all of the files and directories in the path, expanding links.
	dirEntries, _ := r.fileSystemCache.ReaddirEntriesSync(dirPath)
	entries := uri.GetFileSystemEntriesFromDirEntries(dirEntries, r.fileSystem, dirPath)

	// Add implicit file-based modules.
	for _, filePath := range entries.Files {
		fileExt := filePath.LastExtension()
		strippedFileName := ""
		isNativeLib := false

		if fileExt == ".py" || fileExt == ".pyi" {
			strippedFileName = common.StripFileExtension(filePath.FileName(), false)
		} else if isNativeModuleFileExtension(fileExt) &&
			!r.fileExistsCached(filePath.PackageUri()) &&
			!r.fileExistsCached(filePath.PackageStubUri()) {
			// Native module.
			strippedFileName = filePath.StripAllExtensions().FileName()
			isNativeLib = true
		} else {
			continue
		}

		excluded := false
		for _, exclusion := range exclusions {
			if exclusion.Equals(filePath) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}

		implicitImport := &ImplicitImport{
			IsStubFile:  filePath.HasExtension(".pyi"),
			IsNativeLib: isNativeLib,
			Name:        strippedFileName,
			Uri:         filePath,
		}

		// Always prefer stub files over non-stub files.
		entry, hasEntry := implicitImportMap.Get(implicitImport.Name)
		if !hasEntry || !entry.IsStubFile {
			// Try resolving a native lib to a custom stub.
			if isNativeLib {
				nativeLibPath := filePath
				nativeStubPath := r.resolveNativeImportEx(nativeLibPath, importingModuleName+"."+strippedFileName, nil)
				if nativeStubPath != nil {
					implicitImport.Uri = nativeStubPath
					implicitImport.IsNativeLib = false
				}
			}
			implicitImportMap.Set(implicitImport.Name, implicitImport)
		}
	}

	// Add implicit directory-based modules.
	for _, dirPath := range entries.Directories {
		pyFilePath := dirPath.InitPyUri()
		pyiFilePath := dirPath.InitPyiUri()
		isStubFile := false
		var path uri.Uri

		if r.fileExistsCached(pyiFilePath) {
			isStubFile = true
			path = pyiFilePath
		} else if r.fileExistsCached(pyFilePath) {
			path = pyFilePath
		}

		if path != nil {
			excluded := false
			for _, exclusion := range exclusions {
				if exclusion.Equals(path) {
					excluded = true
					break
				}
			}
			if !excluded {
				implicitImportMap.Set(dirPath.FileName(), &ImplicitImport{
					IsStubFile:  isStubFile,
					IsNativeLib: false,
					Name:        dirPath.FileName(),
					Uri:         path,
					PyTypedInfo: r.getPyTypedInfo(dirPath),
				})
			}
		}
	}

	if implicitImportMap.Size() > 0 {
		return implicitImportMap
	}
	return nil
}

func (r *ImportResolver) isPossibleImportDir(rootPath uri.Uri, moduleDescriptor ImportedModuleDescriptor) bool {
	resolvableNames := r.fileSystemCache.GetResolvableNamesInDirectory(rootPath)

	if len(moduleDescriptor.NameParts) > 0 {
		return resolvableNames.Has(moduleDescriptor.NameParts[0])
	}

	if moduleDescriptor.ImportedSymbols != nil {
		for _, key := range moduleDescriptor.ImportedSymbols.Values() {
			if resolvableNames.Has(key) {
				return true
			}
		}
	}

	return resolvableNames.Has("__init__")
}

func (r *ImportResolver) resolveImportStrict(
	importName string,
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	moduleDescriptor ImportedModuleDescriptor,
	importLogger *ImportLogger,
) *ImportResult {
	fromUserFile := MatchFileSpecs(r.configOptions, sourceFileUri, true)
	notFoundResult := &ImportResult{
		ImportName:        importName,
		ImportFailureInfo: importLogger.GetLogs(),
		ResolvedUris:      []uri.Uri{},
		ImportType:        ImportTypeLocal,
	}

	r.EnsurePartialStubPackages(execEnv)

	// Is it a relative import?
	if moduleDescriptor.LeadingDots > 0 {
		if cachedResults := r.lookUpResultsInCache(sourceFileUri, execEnv, importName, moduleDescriptor, fromUserFile); cachedResults != nil {
			return cachedResults
		}

		relativeImport := r.resolveRelativeImport(sourceFileUri, execEnv, moduleDescriptor, importName, importLogger)

		if relativeImport != nil {
			relativeImport.IsRelative = true

			return r.addResultsToCache(sourceFileUri, execEnv, importName, relativeImport, &moduleDescriptor, fromUserFile)
		}
	} else {
		if cachedResults := r.lookUpResultsInCache(sourceFileUri, execEnv, importName, moduleDescriptor, fromUserFile); cachedResults != nil {
			// The original's comment: in most cases, we can simply return a
			// cached entry. However, there are cases where the cached entry
			// refers to a previously-resolved namespace package that does not
			// resolve the symbols specified in the module descriptor. In this
			// case, we will ignore the cached value and run the full import
			// resolution again to try to find a package that resolves the
			// import.
			isUnresolvedNamespace := cachedResults.IsImportFound &&
				cachedResults.IsNamespacePackage &&
				!r.isNamespacePackageResolved(moduleDescriptor, cachedResults.ImplicitImports)

			if !isUnresolvedNamespace {
				return cachedResults
			}
		}

		bestImport := r.resolveBestAbsoluteImport(sourceFileUri, execEnv, moduleDescriptor, true)

		if bestImport != nil {
			if bestImport.IsStubFile {
				bestImport.NonStubImportResult = r.resolveBestAbsoluteImport(sourceFileUri, execEnv, moduleDescriptor, false)
				if bestImport.NonStubImportResult == nil {
					bestImport.NonStubImportResult = notFoundResult
				}
			}

			return r.addResultsToCache(sourceFileUri, execEnv, importName, bestImport, &moduleDescriptor, fromUserFile)
		}
	}

	return r.addResultsToCache(sourceFileUri, execEnv, importName, notFoundResult, nil, fromUserFile)
}

func (r *ImportResolver) getImportCacheKey(sourceFileUri uri.Uri, importName string, fromUserFile bool) string {
	key := ""
	if sourceFileUri != nil {
		key = sourceFileUri.Key()
	}
	fromUser := "false"
	if fromUserFile {
		fromUser = "true"
	}
	return key + "-" + importName + "-" + fromUser
}

// lookUpResultsInCache returns nil where the original returns undefined.
func (r *ImportResolver) lookUpResultsInCache(
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	importName string,
	moduleDescriptor ImportedModuleDescriptor,
	fromUserFile bool,
) *ImportResult {
	cacheForExecEnv, ok := r.cachedImportResults.Get(execEnvCacheKey(execEnv))
	if !ok {
		return nil
	}

	// If the import is relative, include the source file path in the key.
	var relativeSourceFileUri uri.Uri
	if moduleDescriptor.LeadingDots > 0 {
		relativeSourceFileUri = sourceFileUri
	}

	cachedEntry, ok := cacheForExecEnv.Get(r.getImportCacheKey(relativeSourceFileUri, importName, fromUserFile))
	if !ok {
		return nil
	}

	return r.filterImplicitImports(cachedEntry, moduleDescriptor.ImportedSymbols)
}

// isNamespacePackageResolved determines whether a namespace package resolves
// all of the symbols requested in the module descriptor.
//
// The original's comment: namespace packages have no "__init__.py" file, so the
// only way that symbols can be resolved is if submodules are present. If
// specific symbols were requested, make sure they are all satisfied by
// submodules (as listed in the implicit imports).
func (r *ImportResolver) isNamespacePackageResolved(
	moduleDescriptor ImportedModuleDescriptor,
	implicitImports *common.OrderedMap[string, *ImplicitImport],
) bool {
	if moduleDescriptor.ImportedSymbols != nil {
		found := false
		for _, symbol := range moduleDescriptor.ImportedSymbols.Values() {
			if implicitImports != nil && implicitImports.Has(symbol) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	} else if implicitImports == nil || implicitImports.Size() == 0 {
		return false
	}
	return true
}

// resolveRelativeImport returns nil where the original returns undefined.
func (r *ImportResolver) resolveRelativeImport(
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	moduleDescriptor ImportedModuleDescriptor,
	importName string,
	importLogger *ImportLogger,
) *ImportResult {
	importLogger.Log("Attempting to resolve relative import")

	// Determine which search path this file is part of.
	directory := GetDirectoryLeadingDotsPointsTo(sourceFileUri.GetDirectory(), moduleDescriptor.LeadingDots)
	if directory == nil {
		importLogger.Log("Invalid relative path '" + importName + "'")
		return nil
	}

	// Now try to match the module parts from the current directory location.
	absImport := r.resolveAbsoluteImport(
		sourceFileUri, directory, execEnv, moduleDescriptor, importName, importLogger,
		false /* allowPartial */, true /* allowNativeLib */, false /* useStubPackage */, true /* allowPyi */, false, /* lookForPyTyped */
	)

	if absImport != nil && absImport.IsStubFile {
		// The original's comment: if we found a stub for a relative import,
		// only search the same folder for the real module. Otherwise, it will
		// error out at runtime.
		absImport.NonStubImportResult = r.resolveAbsoluteImport(
			sourceFileUri, directory, execEnv, moduleDescriptor, importName, importLogger,
			false /* allowPartial */, true /* allowNativeLib */, false /* useStubPackage */, false /* allowPyi */, false, /* lookForPyTyped */
		)
		if absImport.NonStubImportResult == nil {
			absImport.NonStubImportResult = &ImportResult{
				ImportName:   importName,
				IsRelative:   true,
				ResolvedUris: []uri.Uri{},
				ImportType:   ImportTypeLocal,
			}
		}
	}

	return absImport
}

func (r *ImportResolver) resolveBestAbsoluteImport(
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	moduleDescriptor ImportedModuleDescriptor,
	allowPyi bool,
) *ImportResult {
	importName := FormatImportName(moduleDescriptor)
	var importLogger *ImportLogger
	if r.configOptions.VerboseOutput != nil && *r.configOptions.VerboseOutput {
		importLogger = NewImportLogger()
	}

	// Check for a local stub file using stubPath.
	if allowPyi && r.configOptions.StubPath != nil {
		importLogger.Log("Looking in stubPath '" + r.configOptions.StubPath.String() + "'")
		typingsImport := r.resolveAbsoluteImport(
			sourceFileUri, r.configOptions.StubPath, execEnv, moduleDescriptor, importName, importLogger,
			false /* allowPartial */, false /* allowNativeLib */, true /* useStubPackage */, allowPyi, false, /* lookForPyTyped */
		)

		if typingsImport != nil && typingsImport.IsImportFound {
			// We will treat typings files as "local" rather than "third party".
			typingsImport.ImportType = ImportTypeLocal
			typingsImport.IsLocalTypingsFile = true

			// The original's comment: if it's a namespace package that didn't
			// resolve to a file, make sure that the imported symbols are
			// present in the implicit imports. If not, we'll skip the typings
			// import and continue searching.
			if typingsImport.IsNamespacePackage &&
				typingsImport.ResolvedUris[len(typingsImport.ResolvedUris)-1].IsEmpty() {
				if r.isNamespacePackageResolved(moduleDescriptor, typingsImport.ImplicitImports) {
					return typingsImport
				}
			} else {
				return typingsImport
			}
		}
	}

	var bestResultSoFar *ImportResult
	var localImport *ImportResult

	// Look for it in the root directory of the execution environment.
	if execEnv.Root != nil {
		importLogger.Log("Looking in root directory of execution environment '" + execEnv.Root.String() + "'")

		localImport = r.resolveAbsoluteImport(
			sourceFileUri, execEnv.Root, execEnv, moduleDescriptor, importName, importLogger,
			false /* allowPartial */, true /* allowNativeLib */, true /* useStubPackage */, allowPyi, false, /* lookForPyTyped */
		)
		bestResultSoFar = localImport
	}

	for _, extraPath := range execEnv.ExtraPaths {
		importLogger.Log("Looking in extraPath '" + extraPath.String() + "'")
		localImport = r.resolveAbsoluteImport(
			sourceFileUri, extraPath, execEnv, moduleDescriptor, importName, importLogger,
			false /* allowPartial */, true /* allowNativeLib */, true /* useStubPackage */, allowPyi, false, /* lookForPyTyped */
		)
		bestResultSoFar = r.pickBestImport(bestResultSoFar, localImport, moduleDescriptor)
	}

	// Check for a stdlib typeshed file.
	if allowPyi && len(moduleDescriptor.NameParts) > 0 {
		importLogger.Log("Looking for typeshed stdlib path")
		typeshedStdlibImport := r.findTypeshedPath(execEnv, moduleDescriptor, importName, true, importLogger)

		if typeshedStdlibImport != nil {
			typeshedStdlibImport.IsStdlibTypeshedFile = true
			return typeshedStdlibImport
		}
	}

	// Look for the import in the list of third-party packages.
	thirdPartyNonStubImportFound := false
	pythonSearchPaths := r.GetPythonSearchPaths(importLogger)
	if len(pythonSearchPaths) > 0 {
		for _, searchPath := range pythonSearchPaths {
			importLogger.Log("Looking in python search path '" + searchPath.String() + "'")

			thirdPartyImport := r.resolveAbsoluteImport(
				sourceFileUri, searchPath, execEnv, moduleDescriptor, importName, importLogger,
				allowPartialResolutionForThirdPartyPackages, true /* allowNativeLib */, true /* useStubPackage */, allowPyi, true, /* lookForPyTyped */
			)

			if thirdPartyImport != nil {
				thirdPartyImport.ImportType = ImportTypeThirdParty

				if !thirdPartyImport.IsStubFile && (thirdPartyImport.IsImportFound || thirdPartyImport.IsPartlyResolved) {
					thirdPartyNonStubImportFound = true
				}

				bestResultSoFar = r.pickBestImport(bestResultSoFar, thirdPartyImport, moduleDescriptor)
			}
		}
	} else {
		importLogger.Log("No python interpreter search path")
	}

	// The original's comment: if a library is fully py.typed, then we have
	// found the best match, unless the execution environment is typeshed
	// itself, in which case we don't want to favor py.typed libraries. Use the
	// typeshed lookup below.
	//
	// The test is `execEnv.root !== <typeshed root>`, a reference comparison on
	// two Uris. Uris are interned, so Go's == on the interface is the same test.
	if execEnv.Root != r.typeshedInfoProvider.GetTypeshedRoot(r.configOptions.TypeshedPath, importLogger) {
		if bestResultSoFar != nil && bestResultSoFar.PyTypedInfo != nil && !bestResultSoFar.IsPartlyResolved {
			return bestResultSoFar
		}
	}

	// Call the extensibility hook for subclasses.
	if extraResults := r.resolveImportEx(sourceFileUri, execEnv, moduleDescriptor, importName, importLogger, allowPyi); extraResults != nil {
		return extraResults
	}

	// The original's comment: results from resolveImportEx return above, so
	// this fallback guard applies only to typeshed fallback imports.
	// Check for a third-party typeshed file.
	if allowPyi && len(moduleDescriptor.NameParts) > 0 {
		importLogger.Log("Looking for typeshed third-party path")
		typeshedImport := r.findTypeshedPath(execEnv, moduleDescriptor, importName, false, importLogger)

		if typeshedImport != nil {
			typeshedImport.IsThirdPartyTypeshedFile = true

			if r.shouldSkipThirdPartyTypeshedFallbackForLocalNamespace(bestResultSoFar, thirdPartyNonStubImportFound) {
				importLogger.Log("Skipping typeshed third-party fallback because a local namespace package was resolved " +
					"and no non-stub third-party package was found")
			} else {
				bestResultSoFar = r.pickBestImport(bestResultSoFar, typeshedImport, moduleDescriptor)
			}
		}
	}

	// We weren't able to find an exact match, so return the best partial match.
	return bestResultSoFar
}

func (r *ImportResolver) shouldSkipThirdPartyTypeshedFallbackForLocalNamespace(
	bestImportSoFar *ImportResult,
	thirdPartyNonStubImportFound bool,
) bool {
	return !thirdPartyNonStubImportFound &&
		bestImportSoFar != nil &&
		bestImportSoFar.ImportType == ImportTypeLocal &&
		bestImportSoFar.IsNamespacePackage &&
		bestImportSoFar.IsImportFound &&
		!bestImportSoFar.IsStubFile
}

// firstNonEmptyIndex is `resolvedUris.findIndex((path) => !path.isEmpty())`,
// which answers -1 when there is none.
func firstNonEmptyIndex(paths []uri.Uri) int {
	for i, path := range paths {
		if !path.IsEmpty() {
			return i
		}
	}
	return -1
}

func (r *ImportResolver) pickBestImport(
	bestImportSoFar *ImportResult,
	newImport *ImportResult,
	moduleDescriptor ImportedModuleDescriptor,
) *ImportResult {
	if bestImportSoFar == nil {
		return newImport
	}

	if newImport == nil {
		return bestImportSoFar
	}

	if newImport.IsImportFound {
		// Prefer traditional packages over namespace packages.
		soFarIndex := firstNonEmptyIndex(bestImportSoFar.ResolvedUris)
		newIndex := firstNonEmptyIndex(newImport.ResolvedUris)
		if soFarIndex != newIndex {
			if soFarIndex < 0 {
				return newImport
			} else if newIndex < 0 {
				return bestImportSoFar
			}
			if soFarIndex < newIndex {
				return bestImportSoFar
			}
			return newImport
		}

		// Prefer found over not found.
		if !bestImportSoFar.IsImportFound {
			return newImport
		}

		// If both are namespace imports, select the one that resolves the
		// symbols.
		if bestImportSoFar.IsNamespacePackage && newImport.IsNamespacePackage {
			if moduleDescriptor.ImportedSymbols != nil {
				if !r.isNamespacePackageResolved(moduleDescriptor, bestImportSoFar.ImplicitImports) {
					if r.isNamespacePackageResolved(moduleDescriptor, newImport.ImplicitImports) {
						return newImport
					}

					// Prefer the namespace package that has an __init__.py(i)
					// file present in the final directory over one that does
					// not.
					if bestImportSoFar.IsInitFilePresent && !newImport.IsInitFilePresent {
						return bestImportSoFar
					} else if !bestImportSoFar.IsInitFilePresent && newImport.IsInitFilePresent {
						return newImport
					}
				}
			}
		}

		// Prefer local over third-party. We check local first, so we should
		// never see the reverse.
		if bestImportSoFar.ImportType == ImportTypeLocal && newImport.ImportType == ImportTypeThirdParty {
			return bestImportSoFar
		}

		// Prefer py.typed over non-py.typed.
		if bestImportSoFar.PyTypedInfo != nil && newImport.PyTypedInfo == nil {
			return bestImportSoFar
		} else if bestImportSoFar.PyTypedInfo == nil && newImport.PyTypedInfo != nil {
			if bestImportSoFar.ImportType == newImport.ImportType {
				return newImport
			}
		}

		// Prefer pyi over py.
		if bestImportSoFar.IsStubFile && !newImport.IsStubFile {
			return bestImportSoFar
		} else if !bestImportSoFar.IsStubFile && newImport.IsStubFile {
			return newImport
		}

		// All else equal, prefer shorter resolution paths.
		if len(bestImportSoFar.ResolvedUris) > len(newImport.ResolvedUris) {
			return newImport
		}
	} else if newImport.IsPartlyResolved {
		// The original's comment: if the new import is a traditional package
		// but only partly resolves the import but the best import so far is a
		// namespace package, we need to consider whether the best import so far
		// also resolves the first part of the import with a traditional
		// package. Using the example "import a.b.c.d" and the symbol ~ to
		// represent a namespace package, consider the following cases:
		//   bestSoFar: a/~b/~c/~d   new: a      Result: bestSoFar wins
		//   bestSoFar: ~a/~b/~c/~d  new: a      Result: new wins
		//   bestSoFar: a/~b/~c/~d   new: a/b    Result: new wins
		soFarIndex := firstNonEmptyIndex(bestImportSoFar.ResolvedUris)
		newIndex := firstNonEmptyIndex(newImport.ResolvedUris)

		if soFarIndex != newIndex {
			if soFarIndex < 0 {
				return newImport
			} else if newIndex < 0 {
				return bestImportSoFar
			}
			if soFarIndex < newIndex {
				return bestImportSoFar
			}
			return newImport
		}
	}

	return bestImportSoFar
}

// resolveAbsoluteImportImpl is the private _resolveAbsoluteImport. Unlike its
// public sibling it always returns a result.
func (r *ImportResolver) resolveAbsoluteImportImpl(
	rootPath uri.Uri,
	execEnv *ExecutionEnvironment,
	moduleDescriptor ImportedModuleDescriptor,
	importName string,
	importLogger *ImportLogger,
	allowPartial bool,
	allowNativeLib bool,
	useStubPackage bool,
	allowPyi bool,
	lookForPyTyped bool,
) *ImportResult {
	if useStubPackage {
		importLogger.Log("Attempting to resolve stub package using root path '" + rootPath.String() + "'")
	} else {
		importLogger.Log("Attempting to resolve using root path '" + rootPath.String() + "'")
	}

	// Starting at the specified path, walk the file system to find the
	// specified module.
	resolvedPaths := []uri.Uri{}
	dirPath := rootPath
	isNamespacePackage := false
	isInitFilePresent := false
	isStubPackage := false
	isStubFile := false
	isNativeLib := false
	var implicitImports *common.OrderedMap[string, *ImplicitImport]
	var packageDirectory uri.Uri
	var pyTypedInfo *PyTypedInfo

	// Handle the "from . import XXX" case.
	if len(moduleDescriptor.NameParts) == 0 {
		pyFilePath := dirPath.InitPyUri()
		pyiFilePath := dirPath.InitPyiUri()

		if allowPyi && r.fileExistsCached(pyiFilePath) {
			importLogger.Log("Resolved import with file '" + pyiFilePath.String() + "'")
			resolvedPaths = append(resolvedPaths, pyiFilePath)
			isStubFile = true
		} else if r.fileExistsCached(pyFilePath) {
			importLogger.Log("Resolved import with file '" + pyFilePath.String() + "'")
			resolvedPaths = append(resolvedPaths, pyFilePath)
		} else {
			importLogger.Log("Partially resolved import with directory '" + dirPath.String() + "'")
			resolvedPaths = append(resolvedPaths, uri.Empty())
			isNamespacePackage = true
		}

		implicitImports = r.findImplicitImports(importName, dirPath, []uri.Uri{pyFilePath, pyiFilePath})
	} else {
		for i := 0; i < len(moduleDescriptor.NameParts); i++ {
			isFirstPart := i == 0
			isLastPart := i == len(moduleDescriptor.NameParts)-1
			dirPath = dirPath.CombinePaths(moduleDescriptor.NameParts[i])

			if useStubPackage && isFirstPart {
				dirPath = dirPath.AddPath(common.StubsSuffix)
				isStubPackage = true
			}

			foundDirectory := r.dirExistsCached(dirPath)

			if foundDirectory {
				if isFirstPart {
					packageDirectory = dirPath
				}

				// See if we can find an __init__.py[i] in this directory.
				pyFilePath := dirPath.InitPyUri()
				pyiFilePath := dirPath.InitPyiUri()
				isInitFilePresent = false

				if allowPyi && r.fileExistsCached(pyiFilePath) {
					importLogger.Log("Resolved import with file '" + pyiFilePath.String() + "'")
					resolvedPaths = append(resolvedPaths, pyiFilePath)
					if isLastPart {
						isStubFile = true
					}
					isInitFilePresent = true
				} else if r.fileExistsCached(pyFilePath) {
					importLogger.Log("Resolved import with file '" + pyFilePath.String() + "'")
					resolvedPaths = append(resolvedPaths, pyFilePath)
					isInitFilePresent = true
				}

				if pyTypedInfo == nil && lookForPyTyped {
					pyTypedInfo = r.getPyTypedInfo(dirPath)
				}

				if isInitFilePresent {
					if !isLastPart {
						// We are not at the last part, and we found a
						// directory, so continue to look for the next part.
						continue
					}

					implicitImports = r.findImplicitImports(
						strings.Join(moduleDescriptor.NameParts, "."),
						dirPath,
						[]uri.Uri{pyFilePath, pyiFilePath},
					)
					break
				}
			}

			// The original's comment: we weren't able to find a directory or we
			// found a directory with no __init__.py[i] file. See if we can find
			// a ".py" or ".pyi" file with this name.
			pyFilePath := dirPath.PackageUri()
			pyiFilePath := dirPath.PackageStubUri()
			fileDirectory := dirPath.GetDirectory()

			switch {
			case allowPyi && r.fileExistsCached(pyiFilePath):
				importLogger.Log("Resolved import with file '" + pyiFilePath.String() + "'")
				resolvedPaths = append(resolvedPaths, pyiFilePath)
				if isLastPart {
					isStubFile = true
				}

			case r.fileExistsCached(pyFilePath):
				importLogger.Log("Resolved import with file '" + pyFilePath.String() + "'")
				resolvedPaths = append(resolvedPaths, pyFilePath)

			case allowNativeLib && r.findAndResolveNativeModule(fileDirectory, dirPath, execEnv, importName, moduleDescriptor, importLogger, &resolvedPaths):
				isNativeLib = true
				importLogger.Log("Did not find file '" + pyiFilePath.String() + "' or '" + pyFilePath.String() + "'")

			case !allowPyi && allowNativeLib && isLastPart && r.fileExistsCached(dirPath.AddExtension(".pyc")):
				// The original's comment, in full, because it explains a
				// mechanism rather than a step:
				//
				// Sourceless distribution: a compiled `.pyc` module sits where
				// the `.py` source would be (e.g. `mod.pyc` beside `mod.pyi`,
				// with no `mod.py`) -- placed directly in the package
				// directory, not under `__pycache__`. Python imports such a
				// `.pyc` directly, so the module exists at runtime even though
				// it has no Python source.
				//
				// This branch only runs during the non-stub resolution
				// (`allowPyi === false`), whose result becomes
				// `nonStubImportResult`. Pushing the `.pyc` onto
				// `resolvedPaths` makes that non-stub result report
				// `isImportFound === true`, and the checker's
				// `_addMissingModuleSourceDiagnosticIfNeeded` short-circuits on
				// `nonStubImportResult.isImportFound` -- so the misleading
				// `reportMissingModuleSource` warning is suppressed via the
				// `isImportFound` path (not via `isNativeLib`, whose check the
				// checker never reaches once `isImportFound` is true).
				//
				// Gating mirrors the native-lib branch above: a compiled module
				// is a native artifact with no Python source, so it is only
				// recognized where native libs are allowed (`allowNativeLib`),
				// ensuring a stray `.pyc` in a stub-only root is not mistaken
				// for the real module. `isNativeLib = true` is set purely for
				// symmetry with that branch; it does not drive the suppression
				// here. The `.pyc` is likewise kept out of
				// `getSourceFilesFromStub` by that method's `.py`/`.pyi`
				// extension filter, independent of `isNativeLib`.
				pycFilePath := dirPath.AddExtension(".pyc")
				importLogger.Log("Resolved import with compiled file '" + pycFilePath.String() + "'")
				resolvedPaths = append(resolvedPaths, pycFilePath)
				isNativeLib = true

			case foundDirectory:
				if !isLastPart {
					// We are not at the last part, and we found a directory, so
					// continue to look for the next part assuming this is a
					// namespace package.
					resolvedPaths = append(resolvedPaths, uri.Empty())
					isNamespacePackage = true
					pyTypedInfo = nil
					continue
				}

				importLogger.Log("Partially resolved import with directory '" + dirPath.String() + "'")
				resolvedPaths = append(resolvedPaths, uri.Empty())

				// The original re-tests isLastPart here, which is already known
				// true on this arm.
				implicitImports = r.findImplicitImports(importName, dirPath, []uri.Uri{pyFilePath, pyiFilePath})
				isNamespacePackage = true
			}

			if pyTypedInfo == nil && lookForPyTyped {
				pyTypedInfo = r.getPyTypedInfo(fileDirectory)
			}
			break
		}
	}

	var importFound bool
	isPartlyResolved := len(resolvedPaths) > 0 && len(resolvedPaths) < len(moduleDescriptor.NameParts)
	if allowPartial {
		importFound = len(resolvedPaths) > 0
	} else {
		importFound = len(resolvedPaths) >= len(moduleDescriptor.NameParts)
	}

	return &ImportResult{
		ImportName:              importName,
		IsRelative:              false,
		IsNamespacePackage:      isNamespacePackage,
		IsInitFilePresent:       isInitFilePresent,
		IsStubPackage:           isStubPackage,
		IsImportFound:           importFound,
		IsPartlyResolved:        isPartlyResolved,
		ImportFailureInfo:       importLogger.GetLogs(),
		ImportType:              ImportTypeLocal,
		ResolvedUris:            resolvedPaths,
		SearchPath:              rootPath,
		IsStubFile:              isStubFile,
		IsNativeLib:             isNativeLib,
		ImplicitImports:         implicitImports,
		PyTypedInfo:             pyTypedInfo,
		FilteredImplicitImports: implicitImports,
		PackageDirectory:        packageDirectory,
	}
}

// getPyTypedInfo retrieves the py.typed info for a directory if it exists.
//
// The original's comment: this is a small perf optimization that allows
// skipping the search when the pytyped file doesn't exist.
func (r *ImportResolver) getPyTypedInfo(filePath uri.Uri) *PyTypedInfo {
	if !r.fileExistsCached(filePath.PytypedUri()) {
		return nil
	}

	return GetPyTypedInfoForPyTypedFile(r.fileSystem, filePath.PytypedUri())
}

func (r *ImportResolver) findAndResolveNativeModule(
	fileDirectory uri.Uri,
	dirPath uri.Uri,
	execEnv *ExecutionEnvironment,
	importName string,
	moduleDescriptor ImportedModuleDescriptor,
	importLogger *ImportLogger,
	resolvedPaths *[]uri.Uri,
) bool {
	isNativeLib := false

	if !execEnv.SkipNativeLibraries && r.dirExistsCached(fileDirectory) {
		filesInDir := r.fileSystemCache.GetFilesInDirectory(fileDirectory)
		dirName := dirPath.FileName()

		var nativeLibPath uri.Uri
		for _, f := range filesInDir {
			if r.isNativeModuleFileName(dirName, f) {
				nativeLibPath = f
				break
			}
		}

		if nativeLibPath != nil {
			// Try resolving the native library to a custom stub.
			isNativeLib = r.resolveNativeModuleWithStub(nativeLibPath, execEnv, importName, moduleDescriptor, importLogger, resolvedPaths)

			if isNativeLib {
				importLogger.Log("Resolved with native lib '" + nativeLibPath.ToUserVisibleString() + "'")
			}
		}
	}

	return isNativeLib
}

func (r *ImportResolver) resolveNativeModuleWithStub(
	nativeLibPath uri.Uri,
	execEnv *ExecutionEnvironment,
	importName string,
	moduleDescriptor ImportedModuleDescriptor,
	importLogger *ImportLogger,
	resolvedPaths *[]uri.Uri,
) bool {
	moduleFullName := importName

	if moduleDescriptor.LeadingDots > 0 {
		// Relative path. Convert `.mtrand` to `numpy.random.mtrand` based on
		// the search path.
		info := r.GetModuleNameForImport(nativeLibPath, execEnv, false, false)
		if len(info.ModuleName) > 0 {
			moduleFullName = info.ModuleName
		}
	}

	compiledStubPath := r.resolveNativeImportEx(nativeLibPath, moduleFullName, importLogger)
	if compiledStubPath != nil {
		importLogger.Log("Resolved native import " + importName + " with stub '" + compiledStubPath.String() + "'")
		*resolvedPaths = append(*resolvedPaths, compiledStubPath)
		return false // Resolved to a stub.
	}

	importLogger.Log("Resolved import with file '" + nativeLibPath.String() + "'")
	*resolvedPaths = append(*resolvedPaths, nativeLibPath)
	return true
}

func (r *ImportResolver) isNativeModuleFileName(moduleName string, fileUri uri.Uri) bool {
	// The original's comment: strip off the final file extension and the part
	// of the file name that excludes all (multi-part) file extensions. This
	// allows us to handle file names like "foo.cpython-32m.so".
	fileExtension := strings.ToLower(fileUri.LastExtension())
	withoutExtension := common.StripFileExtension(fileUri.FileName(), true)
	return isNativeModuleFileExtension(fileExtension) && common.EquateStringsCaseInsensitive(moduleName, withoutExtension)
}

// tryWalkUp returns nil where the original returns undefined.
func (r *ImportResolver) tryWalkUp(current uri.Uri) uri.Uri {
	if current == nil || current.IsEmpty() || current.IsRoot() {
		return nil
	}

	// Ensure we don't go around forever even if isRoot returns false.
	next := current.ResolvePaths("..")
	if next.Equals(current) {
		return nil
	}
	return next
}

func (r *ImportResolver) shouldWalkUp(current uri.Uri, root uri.Uri, execEnv *ExecutionEnvironment) bool {
	return current != nil &&
		!current.IsEmpty() &&
		(current.IsChild(root) || (current.Equals(root) && IsDefaultWorkspace(execEnv.Root)))
}
