/*
 * program_analysis.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The parse / bind / check pipeline of analyzer/program.ts (pyright 1.1.412),
 * plus the import-graph maintenance and cycle detection. See program.go for
 * the rest and for what is deliberately dropped.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/parser"
)

// Analyze performs parsing and analysis of any source files in the program that
// require it.
//
// The original's comment: if a limit time is specified, the operation is
// interrupted when the time expires. The return value indicates whether the
// method needs to be called again to complete the analysis. In interactive
// mode, the timeout is always limited to the smaller value to maintain
// responsiveness.
//
// maxTime is optional; nil is the original's undefined, which means no limit
// *and* changes the control flow -- see the early return below.
func (p *Program) Analyze(maxTime *MaxAnalysisTime) bool {
	elapsedTime := common.NewDuration()

	openFiles := filterSourceFiles(p.sourceFileList, func(sf *SourceFileInfo) bool {
		return sf.IsOpenByClient() && sf.SourceFile.IsCheckingRequired()
	})

	if len(openFiles) > 0 {
		var effectiveMaxTime int64 = maxInt64
		if maxTime != nil {
			effectiveMaxTime = maxTime.OpenFilesTimeInMs
		}

		// Check the open files.
		for _, sourceFileInfo := range openFiles {
			if p.checkTypes(sourceFileInfo, nil, false) {
				if elapsedTime.GetDurationInMilliseconds() > effectiveMaxTime {
					return true
				}
			}
		}

		// The original's comment: if the caller specified a maxTime, return at
		// this point since we've finalized all open files. We want to get the
		// results to the user as quickly as possible.
		if maxTime != nil {
			return true
		}
	}

	if p.configOptions.CheckOnlyOpenFiles == nil || !*p.configOptions.CheckOnlyOpenFiles {
		var effectiveMaxTime int64 = maxInt64
		if maxTime != nil {
			effectiveMaxTime = maxTime.NoOpenFilesTimeInMs
		}

		// Now do type parsing and analysis of the remaining.
		for _, sourceFileInfo := range p.sourceFileList {
			if !IsUserCode(sourceFileInfo) {
				continue
			}

			if p.checkTypes(sourceFileInfo, nil, false) {
				if elapsedTime.GetDurationInMilliseconds() > effectiveMaxTime {
					return true
				}
			}
		}
	}

	return false
}

// maxInt64 stands in for Number.MAX_VALUE as a millisecond bound.
const maxInt64 = int64(^uint64(0) >> 1)

// AnalyzeFile performs parsing and analysis of a single file in the program. If
// the file is not part of the program it returns false to indicate analysis was
// not performed.
func (p *Program) AnalyzeFile(fileUri uri.Uri) bool {
	sourceFileInfo := p.GetSourceFileInfo(fileUri)
	if sourceFileInfo != nil && p.checkTypes(sourceFileInfo, nil, true) {
		return true
	}
	return false
}

// GetParserOutput returns nil where the original returns undefined.
func (p *Program) GetParserOutput(fileUri uri.Uri) *parser.ParserOutput {
	sourceFileInfo := p.GetSourceFileInfo(fileUri)
	if sourceFileInfo == nil {
		return nil
	}

	p.parseFile(sourceFileInfo, "", false, false)
	return sourceFileInfo.SourceFile.GetParserOutput()
}

// GetParseResults returns nil where the original returns undefined.
func (p *Program) GetParseResults(fileUri uri.Uri) *ParseResults {
	sourceFileInfo := p.GetSourceFileInfo(fileUri)
	if sourceFileInfo == nil {
		return nil
	}

	p.parseFile(sourceFileInfo, "", false, false)
	return sourceFileInfo.SourceFile.GetParseResults()
}

// GetParseDiagnostics returns nil where the original returns undefined.
func (p *Program) GetParseDiagnostics(fileUri uri.Uri) []*common.Diagnostic {
	sourceFileInfo := p.GetSourceFileInfo(fileUri)
	if sourceFileInfo == nil {
		return nil
	}

	p.parseFile(sourceFileInfo, "", false, false)
	return sourceFileInfo.SourceFile.GetParseDiagnostics()
}

// handleMemoryHighUsage corresponds to the private method of the same name.
func (p *Program) handleMemoryHighUsage() {
	cacheUsage := p.cacheManager.GetCacheUsage()
	usedHeapRatio := p.cacheManager.GetUsedHeapRatio()

	const heapRatioHighWaterMark = 0.9

	// The original's comment: if the total cache has exceeded 75%, determine
	// whether we should empty the cache. If the usedHeapRatio has exceeded our
	// high-water mark, we should definitely empty the cache. This can happen
	// before the cacheUsage maxes out because we might be on the background
	// thread and a bunch of the cacheUsage is on the main thread.
	if cacheUsage > 0.75 || usedHeapRatio > heapRatioHighWaterMark {
		// The original's comment: the type cache uses a Map, which has an
		// absolute limit of 2^24 entries before it will fail. If we cross the
		// 90% mark, we'll empty the cache.
		//
		// Go's map has no such limit, so this bound is inherited rather than
		// required. It stays because it is half of the condition that decides
		// whether a 75%-full cache is actually emptied, and dropping it would
		// empty the cache far more eagerly than the original does.
		const absoluteMaxCacheEntryCount = float64(int(1)<<24) * 0.9

		typeCacheEntryCount := 0
		if p.evaluator != nil {
			typeCacheEntryCount = p.evaluator.GetTypeCacheEntryCount()
		}

		if float64(typeCacheEntryCount) > absoluteMaxCacheEntryCount ||
			usedHeapRatio > heapRatioHighWaterMark {
			p.cacheManager.EmptyCache(p.console)
		}
	}
}

// discardCachedParseResults discards all cached parse results and file contents
// to free up memory. The original's comment: it does not discard cached index
// results or diagnostics for files.
func (p *Program) discardCachedParseResults() {
	for _, sourceFileInfo := range p.sourceFileList {
		sourceFileInfo.SourceFile.DropParseAndBindInfo()
	}
}

// removeUnneededFiles returns a list of empty file diagnostic entries for the
// files that have been removed.
//
// The original's comment: this is needed to clear out the errors for files that
// have been deleted or closed.
func (p *Program) removeUnneededFiles() []common.FileDiagnostics {
	fileDiagnostics := []common.FileDiagnostics{}

	// The original's comment: if a file is no longer tracked, opened or
	// shadowed, it can be removed from the program.
	for i := 0; i < len(p.sourceFileList); {
		fileInfo := p.sourceFileList[i]
		if !p.isFileNeeded(fileInfo, false) {
			// Clear only if there are any errors for this file.
			if fileInfo.DiagnosticsVersion() != nil {
				fileDiagnostics = append(fileDiagnostics, common.FileDiagnostics{
					FileUri:     fileInfo.Uri(),
					Version:     fileInfo.SourceFile.GetClientVersion(),
					Diagnostics: []*common.Diagnostic{},
				})
			}

			fileInfo.SourceFile.PrepareForClose()
			p.removeSourceFileFromListAndMap(fileInfo.Uri(), i)

			// The original's comment: unlink any imports and remove them from
			// the list if they are no longer referenced.
			for _, importedFile := range fileInfo.Imports() {
				indexToRemove := -1
				for j, fi := range importedFile.ImportedBy() {
					if fi == fileInfo {
						indexToRemove = j
						break
					}
				}
				if indexToRemove < 0 {
					continue
				}

				importedFile.Mutate(func(s *sourceFileWriteableData) {
					s.ImportedBy = append(s.ImportedBy[:indexToRemove], s.ImportedBy[indexToRemove+1:]...)
				})

				// The original's comment: see if we need to remove the imported
				// file because it is no longer needed. If its index is >= i, it
				// will be removed when we get to it.
				if !p.isFileNeeded(importedFile, false) {
					listIndex := -1
					for j, fi := range p.sourceFileList {
						if fi == importedFile {
							listIndex = j
							break
						}
					}
					if listIndex >= 0 && listIndex < i {
						// Clear if there are any errors for this import.
						if importedFile.DiagnosticsVersion() != nil {
							fileDiagnostics = append(fileDiagnostics, common.FileDiagnostics{
								FileUri:     importedFile.Uri(),
								Version:     importedFile.SourceFile.GetClientVersion(),
								Diagnostics: []*common.Diagnostic{},
							})
						}

						importedFile.SourceFile.PrepareForClose()
						p.removeSourceFileFromListAndMap(importedFile.Uri(), listIndex)
						i--
					}
				}
			}

			// Remove any shadowed files corresponding to this file.
			for _, shadowedFile := range fileInfo.ShadowedBy() {
				shadowedFile.Mutate(func(s *sourceFileWriteableData) {
					kept := []*SourceFileInfo{}
					for _, f := range s.Shadows {
						if f != fileInfo {
							kept = append(kept, f)
						}
					}
					s.Shadows = kept
				})
			}
			fileInfo.Mutate(func(s *sourceFileWriteableData) { s.ShadowedBy = []*SourceFileInfo{} })
		} else {
			// The original's comment: if we're showing the user errors only for
			// open files, clear out the errors for the now-closed file.
			if !p.shouldCheckFile(fileInfo) && fileInfo.DiagnosticsVersion() != nil {
				fileDiagnostics = append(fileDiagnostics, common.FileDiagnostics{
					FileUri:     fileInfo.Uri(),
					Version:     fileInfo.SourceFile.GetClientVersion(),
					Diagnostics: []*common.Diagnostic{},
				})
				fileInfo.SetDiagnosticsVersion(nil)
			}

			i++
		}
	}

	return fileDiagnostics
}

func (p *Program) isFileNeeded(fileInfo *SourceFileInfo, skipFileNeededCheck bool) bool {
	if fileInfo.SourceFile.IsFileDeleted() {
		return false
	}

	if skipFileNeededCheck || fileInfo.IsTracked() || fileInfo.IsOpenByClient() {
		return true
	}

	if len(fileInfo.Shadows()) > 0 {
		return true
	}

	if len(fileInfo.ImportedBy()) == 0 {
		return false
	}

	// The original's comment: it's possible for a cycle of files to be imported
	// by a tracked file but then abandoned. The import cycle will keep the
	// entire group "alive" if we don't detect the condition and garbage collect
	// them.
	return p.isImportNeededRecursive(fileInfo, map[string]bool{})
}

func (p *Program) isImportNeededRecursive(fileInfo *SourceFileInfo, recursionSet map[string]bool) bool {
	if fileInfo.IsTracked() || fileInfo.IsOpenByClient() || len(fileInfo.Shadows()) > 0 {
		return true
	}

	fileUri := fileInfo.Uri()

	// Avoid infinite recursion.
	if recursionSet[fileUri.Key()] {
		return false
	}

	recursionSet[fileUri.Key()] = true

	for _, importerInfo := range fileInfo.ImportedBy() {
		if p.isImportNeededRecursive(importerInfo, recursionSet) {
			return true
		}
	}

	return false
}

func (p *Program) isImportAllowed(importer *SourceFileInfo, importResult *ImportResult, isImportStubFile bool) bool {
	// The original's comment: don't import native libs. We don't want to track
	// these files, and we definitely don't want to attempt to parse them.
	if importResult.IsNativeLib {
		return false
	}

	useLibraryCodeForTypes := p.configOptions.UseLibraryCodeForTypes != nil && *p.configOptions.UseLibraryCodeForTypes

	thirdPartyImportAllowed := useLibraryCodeForTypes ||
		(importResult.ImportType == ImportTypeThirdParty && importResult.PyTypedInfo != nil) ||
		(importResult.ImportType == ImportTypeLocal && importer.IsThirdPartyPyTypedPresent)

	if importResult.ImportType == ImportTypeThirdParty ||
		(importer.IsThirdPartyImport && importResult.ImportType == ImportTypeLocal) {
		if p.hasAllowedThirdPartyImports {
			if importResult.IsRelative {
				// The original's comment: if it's a relative import, we'll allow
				// it because the importer was already deemed to be allowed.
				thirdPartyImportAllowed = true
			} else {
				for _, importName := range p.allowedThirdPartyImports {
					// The original's comment: if this import name is the one
					// that was explicitly allowed or is a child of that import
					// name, it's considered allowed.
					if importResult.ImportName == importName ||
						len(importResult.ImportName) > len(importName)+1 &&
							importResult.ImportName[:len(importName)+1] == importName+"." {
						thirdPartyImportAllowed = true
						break
					}
				}
			}
		} else if importer.IsThirdPartyImport && useLibraryCodeForTypes {
			// The original's comment: if the importing file is a third-party
			// import, allow importing of additional third-party imports. This
			// supports the case where the importer is in a py.typed library but
			// is importing from another non-py.typed library. It also supports
			// the case where someone explicitly opens a library source file in
			// their editor.
			thirdPartyImportAllowed = true
		} else if importResult.IsNamespacePackage && importResult.FilteredImplicitImports != nil {
			// The original's comment: handle the case where the import targets a
			// namespace package, and a submodule contained within it has a
			// py.typed marker.
			for _, implicitImport := range importResult.FilteredImplicitImports.Values() {
				if implicitImport.PyTypedInfo != nil {
					thirdPartyImportAllowed = true
					break
				}
			}
		}

		// The original's comment: some libraries ship with stub files that
		// import from non-stubs. Don't explore those. Don't explore any
		// third-party files unless they're type stub files or we've been told
		// explicitly that third-party imports are OK.
		if !isImportStubFile {
			return thirdPartyImportAllowed
		}
	}

	return true
}

// updateSourceFileImports rebuilds the import graph edges for one file and
// returns the files it caused to be added to the program.
func (p *Program) updateSourceFileImports(sourceFileInfo *SourceFileInfo, options *ConfigOptions) []*SourceFileInfo {
	filesAdded := []*SourceFileInfo{}

	// The original's comment: get the new list of imports and see if it changed
	// from the last list of imports for this file.
	imports := sourceFileInfo.SourceFile.GetImports()

	// The original's comment: create a local function that determines whether
	// the import should be considered a "third-party import" and whether it is
	// coming from a third-party package that claims to be typed. An import is
	// considered third-party if it is external to the importer or is internal
	// but the importer is itself a third-party package.
	getThirdPartyImportInfo := func(importResult *ImportResult) (bool, bool) {
		isThirdPartyImport := false
		isPyTypedPresent := false

		if importResult.ImportType == ImportTypeThirdParty {
			isThirdPartyImport = true
			if importResult.PyTypedInfo != nil {
				isPyTypedPresent = true
			}
		} else if sourceFileInfo.IsThirdPartyImport && importResult.ImportType == ImportTypeLocal {
			isThirdPartyImport = true
			if sourceFileInfo.IsThirdPartyPyTypedPresent {
				isPyTypedPresent = true
			}
		}

		return isThirdPartyImport, isPyTypedPresent
	}

	// Create a map of unique imports, since imports can appear more than once.
	newImportPathMap := common.NewOrderedMap[string, updateImportInfo]()

	// Add the chained source file as an import if it exists.
	if chained := sourceFileInfo.ChainedSourceFile(); chained != nil {
		if chained.SourceFile.IsFileDeleted() {
			sourceFileInfo.SetChainedSourceFile(nil)
		} else {
			fileUri := chained.Uri()
			newImportPathMap.Set(fileUri.Key(), updateImportInfo{Path: fileUri})
		}
	}

	for _, importResult := range imports {
		if importResult.IsImportFound {
			if p.isImportAllowed(sourceFileInfo, importResult, importResult.IsStubFile) {
				if len(importResult.ResolvedUris) > 0 {
					fileUri := importResult.ResolvedUris[len(importResult.ResolvedUris)-1]
					if !fileUri.IsEmpty() {
						isThirdPartyImport, isPyTypedPresent := getThirdPartyImportInfo(importResult)
						newImportPathMap.Set(fileUri.Key(), updateImportInfo{
							Path:               fileUri,
							IsTypeshedFile:     importResult.IsStdlibTypeshedFile || importResult.IsThirdPartyTypeshedFile,
							IsThirdPartyImport: isThirdPartyImport,
							IsPyTypedPresent:   isPyTypedPresent,
						})
					}
				}
			}

			if importResult.FilteredImplicitImports != nil {
				importResult.FilteredImplicitImports.ForEach(func(implicitImport *ImplicitImport, _ string) {
					if p.isImportAllowed(sourceFileInfo, importResult, implicitImport.IsStubFile) {
						if !implicitImport.IsNativeLib {
							isThirdPartyImport, isPyTypedPresent := getThirdPartyImportInfo(importResult)
							newImportPathMap.Set(implicitImport.Uri.Key(), updateImportInfo{
								Path:               implicitImport.Uri,
								IsTypeshedFile:     importResult.IsStdlibTypeshedFile || importResult.IsThirdPartyTypeshedFile,
								IsThirdPartyImport: isThirdPartyImport,
								IsPyTypedPresent:   isPyTypedPresent,
							})
						}
					}
				})
			}

			// The original's comment: if the stub was found but the non-stub
			// (source) file was not, dump the failure to the log for diagnostic
			// purposes. We'll skip this for imports from within stub files and
			// imports that target stdlib typeshed stubs because many of these
			// are known to not have associated source files, and we don't want
			// to fill the logs with noise. We also skip any '__builtins__'
			// import: a '__builtins__.pyi' is a stub-only
			// builtins-augmentation mechanism that intentionally has no source
			// file, so reporting a missing source for it is pure noise.
			if importResult.NonStubImportResult != nil && !importResult.NonStubImportResult.IsImportFound {
				if !sourceFileInfo.SourceFile.IsStubFile() &&
					!importResult.IsStdlibTypeshedFile &&
					importResult.ImportName != "__builtins__" {
					if options.VerboseOutput != nil && *options.VerboseOutput {
						p.console.Info("Could not resolve source for '" + importResult.ImportName +
							"' in file '" + sourceFileInfo.Uri().ToUserVisibleString() + "'")

						for _, diag := range importResult.NonStubImportResult.ImportFailureInfo {
							p.console.Info("  " + diag)
						}
					}
				}
			}
		} else if options.VerboseOutput != nil && *options.VerboseOutput {
			p.console.Info("Could not import '" + importResult.ImportName +
				"' in file '" + sourceFileInfo.Uri().ToUserVisibleString() + "'")
			for _, diag := range importResult.ImportFailureInfo {
				p.console.Info("  " + diag)
			}
		}
	}

	updatedImportMap := map[string]*SourceFileInfo{}
	for _, importInfo := range sourceFileInfo.Imports() {
		oldFilePath := importInfo.Uri()

		// A previous import was removed.
		if !newImportPathMap.Has(oldFilePath.Key()) {
			importInfo.Mutate(func(s *sourceFileWriteableData) {
				kept := []*SourceFileInfo{}
				for _, fi := range s.ImportedBy {
					if !fi.Uri().Equals(sourceFileInfo.Uri()) {
						kept = append(kept, fi)
					}
				}
				s.ImportedBy = kept
			})
		} else {
			updatedImportMap[oldFilePath.Key()] = importInfo
		}
	}

	// See if there are any new imports to be added.
	newImportPathMap.ForEach(func(importInfo updateImportInfo, normalizedImportPath string) {
		if _, ok := updatedImportMap[normalizedImportPath]; ok {
			return
		}

		// The original's comment: we found a new import to add. See if it's
		// already part of the program.
		importedFileInfo := p.GetSourceFileInfo(importInfo.Path)
		if importedFileInfo == nil {
			sourceFile := p.createSourceFile(
				importInfo.Path, importInfo.IsThirdPartyImport, importInfo.IsPyTypedPresent, IPythonModeNone)
			importedFileInfo = NewSourceFileInfo(
				sourceFile,
				importInfo.IsTypeshedFile,
				importInfo.IsThirdPartyImport,
				importInfo.IsPyTypedPresent,
				p.editModeTracker,
				SourceFileInfoArgs{},
			)

			p.addToSourceFileListAndMap(importedFileInfo)
			filesAdded = append(filesAdded, importedFileInfo)
		}

		added := importedFileInfo
		added.Mutate(func(s *sourceFileWriteableData) { s.ImportedBy = append(s.ImportedBy, sourceFileInfo) })
		updatedImportMap[normalizedImportPath] = added
	})

	// The original's comment: update the imports list. It should now map the
	// set of imports specified by the source file.
	newImports := []*SourceFileInfo{}
	for _, key := range newImportPathMap.Keys() {
		if newImport := p.getSourceFileInfoFromKey(key); newImport != nil {
			newImports = append(newImports, newImport)
		}
	}

	// The original's comment: mutate only when necessary to avoid extra binding
	// operations.
	if len(newImports) != len(sourceFileInfo.Imports()) || !containsAllSourceFiles(sourceFileInfo.Imports(), newImports) {
		sourceFileInfo.Mutate(func(s *sourceFileWriteableData) { s.Imports = newImports })
	}

	// The original's comment: resolve the builtins import for the file. This
	// needs to be analyzed before the file can be analyzed.
	sourceFileInfo.SetBuiltinsImport(nil)
	builtinsImport := sourceFileInfo.SourceFile.GetBuiltinsImport()
	if builtinsImport != nil && builtinsImport.IsImportFound {
		resolvedBuiltinsPath := builtinsImport.ResolvedUris[len(builtinsImport.ResolvedUris)-1]
		sourceFileInfo.SetBuiltinsImport(p.GetSourceFileInfo(resolvedBuiltinsPath))
	}

	return filesAdded
}

func containsAllSourceFiles(haystack []*SourceFileInfo, needles []*SourceFileInfo) bool {
	for _, needle := range needles {
		found := false
		for _, h := range haystack {
			if h == needle {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (p *Program) parseFile(fileToParse *SourceFileInfo, content string, hasContent bool, skipFileNeededCheck bool) {
	if !p.isFileNeeded(fileToParse, skipFileNeededCheck) || !fileToParse.SourceFile.IsParseRequired() {
		return
	}

	// The original's comment: SourceFile.parse should only be called here in
	// the program, as calling it elsewhere could break the entire dependency
	// graph maintained by the program. Other parts of the program should use
	// _parseFile to create ParseResults from the sourceFile. For standalone
	// parseResults, use parseFile or the Parser directly.
	if fileToParse.SourceFile.Parse(p.configOptions, p.importResolver, content, hasContent) {
		p.parsedFileCount++
		p.updateSourceFileImports(fileToParse, p.configOptions)
	}

	if fileToParse.SourceFile.IsFileDeleted() {
		fileToParse.SetIsTracked(false)

		// The original's comment: mark any files that depend on this file as
		// dirty also. This will retrigger analysis of these other files.
		p.markFileDirtyRecursive(fileToParse, map[string]bool{}, false)

		// Invalidate the import resolver's cache as well.
		p.importResolver.InvalidateCache()
	}
}

// getImplicitImports returns nil where the original returns undefined.
func (p *Program) getImplicitImports(file *SourceFileInfo) *SourceFileInfo {
	// The original's comment: if file is builtins.pyi, then chainedSourceFile
	// might not exist or be incorrect.
	if file.BuiltinsImport() == file {
		return nil
	}

	if chained := file.ChainedSourceFile(); chained != nil && !chained.SourceFile.IsFileDeleted() {
		return chained
	}

	return file.BuiltinsImport()
}

func (p *Program) bindImplicitImports(fileToAnalyze *SourceFileInfo, skipFileNeededCheck bool) {
	// Get all of the potential imports for this file.
	implicitImports := []*SourceFileInfo{}
	implicitSet := map[string]bool{}

	nextImplicitImport := p.getImplicitImports(fileToAnalyze)
	for nextImplicitImport != nil {
		implicitPath := nextImplicitImport.Uri()

		if implicitSet[implicitPath.Key()] {
			// We've found a cycle. Break out of the loop.
			common.Fail("Found a cycle in implicit imports files")
		}

		implicitSet[implicitPath.Key()] = true
		implicitImports = append(implicitImports, nextImplicitImport)

		p.parseFile(nextImplicitImport, "", false, skipFileNeededCheck)
		nextImplicitImport = p.getImplicitImports(nextImplicitImport)
	}

	if len(implicitImports) == 0 {
		return
	}

	// Reverse order, so the top of the chain is first.
	for i := len(implicitImports) - 1; i >= 0; i-- {
		// Bind this file but don't recurse into its imports.
		p.bindFile(implicitImports[i], "", false, skipFileNeededCheck, true)
	}
}

// bindFile binds the specified file. It returns true if the file was bound or
// it didn't need to be bound.
//
// The TypeScript defaults skipFileNeededCheck and isImplicitImport to false.
func (p *Program) bindFile(
	fileToBind *SourceFileInfo,
	content string,
	hasContent bool,
	skipFileNeededCheck bool,
	isImplicitImport bool,
) bool {
	if !p.isFileNeeded(fileToBind, skipFileNeededCheck) || !fileToBind.SourceFile.IsBindingRequired() {
		return !fileToBind.SourceFile.IsBindingRequired()
	}

	p.parseFile(fileToBind, content, hasContent, skipFileNeededCheck)

	// Create a function to get the scope info.
	getScopeIfAvailable := func(fileInfo *SourceFileInfo) *Scope {
		if fileInfo == nil || fileInfo == fileToBind {
			return nil
		}

		// If the file was deleted, there's no scope to return.
		if fileInfo.SourceFile.IsFileDeleted() {
			return nil
		}

		parseResults := fileInfo.SourceFile.GetParserOutput()
		if parseResults == nil {
			return nil
		}

		// The original's comment: file should already be bound because of the
		// chained file binding above.
		return GetScope(parseResults.ParseTree)
	}

	var builtinsScope *Scope
	if fileToBind.BuiltinsImport() != nil && fileToBind.BuiltinsImport() != fileToBind {
		// Bind all of the implicit imports first, so we don't recurse into them.
		if !isImplicitImport {
			p.bindImplicitImports(fileToBind, false)

			// The original's comment: binding the implicit imports may
			// indirectly cause the current file to be bound. If so, return now
			// to avoid the "Bind called unnecessarily" assert in
			// sourceFile.bind().
			if !fileToBind.SourceFile.IsBindingRequired() {
				return true
			}
		}

		// The original's comment: if it is not the builtin module itself, we
		// need to parse and bind the builtin module.
		builtinsScope = getScopeIfAvailable(fileToBind.ChainedSourceFile())
		if builtinsScope == nil {
			builtinsScope = getScopeIfAvailable(fileToBind.BuiltinsImport())
		}
	}

	if fileToBind.SourceFile.IsParseRequired() {
		// Ensure the file is parsed before binding.
		p.parseFile(fileToBind, content, hasContent, skipFileNeededCheck)
	}

	futureImports := common.NewOrderedSet[string]()
	for name := range fileToBind.SourceFile.GetParserOutput().FutureImports {
		futureImports.Add(name)
	}
	if chained := fileToBind.ChainedSourceFile(); chained != nil {
		futureImports = p.getEffectiveFutureImports(futureImports, chained)
	}
	if futureImports.Size() > 0 {
		fileToBind.SetEffectiveFutureImports(futureImports)
	} else {
		fileToBind.SetEffectiveFutureImports(nil)
	}

	var cellChainIndex CellChainIndexProvider
	if fileToBind.IPythonMode() == IPythonModeCellDocs {
		cellChainIndex = p.cellChainIndex
	}

	fileToBind.SourceFile.Bind(p.configOptions, p.lookUpImport, builtinsScope, futureImports, cellChainIndex)
	return true
}

func (p *Program) getEffectiveFutureImports(futureImports *common.OrderedSet[string], chainedSourceFile *SourceFileInfo) *common.OrderedSet[string] {
	effectiveFutureImports := common.NewOrderedSetFrom(futureImports.Values())

	if chained := chainedSourceFile.EffectiveFutureImports(); chained != nil {
		for _, value := range chained.Values() {
			effectiveFutureImports.Add(value)
		}
	}

	return effectiveFutureImports
}

// lookUpImport is the ImportLookup the binder is given. It returns nil where
// the original returns undefined.
func (p *Program) lookUpImport(fileUri uri.Uri, moduleDescriptor *AbsoluteModuleDescriptor, options *LookupImportOptions) *ImportLookupResult {
	var sourceFileInfo *SourceFileInfo

	if fileUri != nil {
		sourceFileInfo = p.GetSourceFileInfo(fileUri)
	} else if moduleDescriptor != nil {
		// Resolve the import.
		importResult := p.importResolver.ResolveImport(
			moduleDescriptor.ImportingFileUri,
			p.configOptions.FindExecEnvironment(moduleDescriptor.ImportingFileUri),
			ImportedModuleDescriptor{
				LeadingDots:     0,
				NameParts:       moduleDescriptor.NameParts,
				ImportedSymbols: nil,
			},
		)

		if importResult.IsImportFound && !importResult.IsNativeLib && len(importResult.ResolvedUris) > 0 {
			resolvedPath := importResult.ResolvedUris[len(importResult.ResolvedUris)-1]
			if !resolvedPath.IsEmpty() {
				// See if the source file already exists in the program.
				sourceFileInfo = p.GetSourceFileInfo(resolvedPath)

				if sourceFileInfo == nil {
					// Start tracking the source file.
					p.AddTrackedFile(resolvedPath, false, false)
					sourceFileInfo = p.GetSourceFileInfo(resolvedPath)
				}
			}
		}
	}

	if sourceFileInfo == nil {
		return nil
	}

	if options != nil && options.SkipParsing {
		// The original's comment: return dummy information if the caller has
		// indicated that parsing is unnecessary. This is used in cases where
		// the caller simply wants to know if the source file exists but is not
		// interested in the contents.
		return &ImportLookupResult{
			SymbolTable:                  common.NewOrderedMap[string, *Symbol](),
			DunderAllNames:               nil,
			UsesUnsupportedDunderAllForm: false,
			DocString:                    nil,
			IsInPyTypedPackage:           false,
		}
	}

	if sourceFileInfo.SourceFile.IsBindingRequired() {
		// If we're running low on memory, free up some space.
		p.handleMemoryHighUsage()

		skipFileNeededCheck := options != nil && options.SkipFileNeededCheck

		// The original's comment: bind the file if it's not already bound. Don't
		// count this time against the type checker.
		common.TimingStatsInstance.TypeCheckerTime.SubtractFromTime(func() {
			p.bindFile(sourceFileInfo, "", false, skipFileNeededCheck, false)
		})
	}

	symbolTable := sourceFileInfo.SourceFile.GetModuleSymbolTable()
	if symbolTable == nil {
		return nil
	}

	parseResults := sourceFileInfo.SourceFile.GetParserOutput()
	moduleNode := parseResults.ParseTree
	fileInfo := GetFileInfo(moduleNode)

	dunderAllInfo := GetDunderAllInfo(parseResults.ParseTree)

	var dunderAllNames []string
	usesUnsupportedDunderAllForm := false
	if dunderAllInfo != nil {
		dunderAllNames = dunderAllInfo.Names
		usesUnsupportedDunderAllForm = dunderAllInfo.UsesUnsupportedDunderAllForm
	}

	// The original's docString is a lazy getter; it is computed eagerly here,
	// which costs one walk of the module's leading statements.
	var docString *string
	if value, ok := GetDocString(moduleNode.D.Statements); ok {
		docString = &value
	}

	return &ImportLookupResult{
		SymbolTable:                  symbolTable,
		DunderAllNames:               dunderAllNames,
		UsesUnsupportedDunderAllForm: usesUnsupportedDunderAllForm,
		DocString:                    docString,
		IsInPyTypedPackage:           fileInfo.IsInPyTypedPackage,
	}
}

func (p *Program) shouldCheckFile(fileInfo *SourceFileInfo) bool {
	// Always do full checking for a file that's open in the editor.
	if fileInfo.IsOpenByClient() {
		return true
	}

	// The original's comment: if the file isn't currently open, only perform
	// full checking for files that are tracked, and only if checkOnlyOpenFiles
	// is disabled.
	checkOnlyOpenFiles := p.configOptions.CheckOnlyOpenFiles != nil && *p.configOptions.CheckOnlyOpenFiles
	if !checkOnlyOpenFiles && fileInfo.IsTracked() {
		return true
	}

	return false
}

// checkTypes corresponds to the private method of the same name. Its options
// object becomes two parameters.
func (p *Program) checkTypes(fileToCheck *SourceFileInfo, chainedByList []*SourceFileInfo, skipFileNeededCheck bool) bool {
	// The original's comment: for very large programs, we may need to discard
	// the evaluator and its cached types to avoid running out of heap space.
	p.handleMemoryHighUsage()

	logState := p.logTracker.Log("analyzing: " + fileToCheck.Uri().String())
	defer logState.Done()

	// The original's comment: if the file isn't needed because it was
	// eliminated from the transitive closure or deleted, skip the file rather
	// than wasting time on it.
	if !p.isFileNeeded(fileToCheck, false) {
		logState.Suppress()
		return false
	}

	if !fileToCheck.SourceFile.IsCheckingRequired() {
		logState.Suppress()
		return false
	}

	if !skipFileNeededCheck && !p.shouldCheckFile(fileToCheck) {
		logState.Suppress()
		return false
	}

	// The original's comment: bind the file if necessary even if we're not
	// going to run the checker. disableChecker means disable semantic errors,
	// not syntax errors. We need to bind again in order to generate syntax
	// errors.
	boundFile := p.bindFile(
		fileToCheck,
		"",
		false,
		// The original's comment: if binding is required we want to make sure
		// to bind the file, otherwise the sourceFile.check below will fail.
		fileToCheck.SourceFile.IsBindingRequired(),
		false,
	)

	if !p.disableChecker {
		// The original's comment: for ipython, make sure we check all its
		// dependent files first since their results can affect this file's
		// result.
		dependentFiles := p.checkDependentFiles(fileToCheck, chainedByList)

		if boundFile {
			fileToCheck.SourceFile.Check(
				p.configOptions,
				p.lookUpImport,
				p.importResolver,
				p.evaluator,
				dependentFiles,
			)
		}
	}

	// Detect import cycles that involve the file.
	if p.configOptions.DiagnosticRuleSet.ReportImportCycles != DiagnosticLevelNone {
		// The original's comment: don't detect import cycles when doing type
		// stub generation. Some third-party modules are pretty convoluted. Or if
		// the file is a notebook cell -- a notebook cell can't have cycles.
		if !p.hasAllowedThirdPartyImports && fileToCheck.IPythonMode() != IPythonModeCellDocs {
			// The original's comment: we need to force all of the files to be
			// parsed and build a closure map for the files.
			closureMap := common.NewOrderedMap[string, *SourceFileInfo]()
			p.getImportsRecursive(fileToCheck, closureMap, 0)

			for _, file := range closureMap.Values() {
				common.TimingStatsInstance.CycleDetectionTime.TimeOperation(func() {
					filesVisitedMap := common.NewOrderedMap[string, *SourceFileInfo]()

					if !p.detectAndReportImportCycles(file, filesVisitedMap, nil, map[string]bool{}) {
						// The original's comment: if no cycles were found in any
						// of the files we visited, set a flag that indicates we
						// don't need to visit them again on subsequent cycle
						// checks.
						for _, sourceFileInfo := range filesVisitedMap.Values() {
							sourceFileInfo.SourceFile.SetNoCircularDependencyConfirmed()
						}
					}
				})
			}
		}
	}

	return true
}

// checkDependentFiles corresponds to _checkDependentFiles. It returns nil where
// the original returns undefined.
//
// "Dependent" reads backwards here and it is worth being explicit about why. In
// a notebook, a CellDocs cell is chained to the cells *before* it, so those are
// already in scope by the time this cell is checked. What this collects is the
// cells *after* it -- the ones whose module scopes an earlier cell may still
// need to resolve a name through the later-cell declaration fallback. Hence
// startIndex = index + 1, and hence the checker being run over them first.
func (p *Program) checkDependentFiles(fileToCheck *SourceFileInfo, chainedByList []*SourceFileInfo) []*parser.ParserOutput {
	if fileToCheck.IPythonMode() != IPythonModeCellDocs {
		return nil
	}

	// The original's comment: if we don't have chainedByList, it means none of
	// them are checked yet.
	needToRunChecker := chainedByList == nil

	if chainedByList == nil {
		chainedByList = p.cellChainIndex.GetCellChainFiles(fileToCheck)
	}

	index := -1
	for i, v := range chainedByList {
		if v == fileToCheck {
			index = i
			break
		}
	}
	if index < 0 {
		return nil
	}

	startIndex := index + 1
	if startIndex >= len(chainedByList) {
		return nil
	}

	if needToRunChecker {
		// The original's comment: if the file is already analyzed, it will be no
		// op. And make sure we don't dump parse tree and etc while calling
		// checker. Otherwise, checkType can dump parse tree required by outer
		// check. Checking later cells in reverse order ensures their module
		// scopes are available before an earlier CellDocs cell resolves nested
		// or class-header names through the later-cell declaration fallback on
		// its first diagnostics pass.
		resume := p.cacheManager.PauseTracking()
		for i := len(chainedByList) - 1; i >= startIndex; i-- {
			p.checkTypes(chainedByList[i], chainedByList, false)
		}
		resume()
	}

	dependentFiles := []*parser.ParserOutput{}
	for i := startIndex; i < len(chainedByList); i++ {
		file := chainedByList[i]
		parserOutput := file.SourceFile.GetParserOutput()
		if parserOutput == nil {
			continue
		}

		// The original's comment: we might not have the file info if binding
		// failed for whatever reasons. Check whether the file has been bound.
		if file.SourceFile.IsBindingRequired() {
			continue
		}

		fileInfo := GetFileInfo(parserOutput.ParseTree)
		if fileInfo.AccessedSymbolSet != nil {
			dependentFiles = append(dependentFiles, parserOutput)
		}
	}

	return dependentFiles
}

// getImportsRecursive builds a map of files that includes the specified file
// and all of the files it imports (recursively).
func (p *Program) getImportsRecursive(file *SourceFileInfo, closureMap *common.OrderedMap[string, *SourceFileInfo], recursionCount int) {
	// The original's comment: if the file is already in the closure map, we
	// found a cyclical dependency. Don't recur further.
	fileUri := file.Uri()
	if closureMap.Has(fileUri.Key()) {
		return
	}

	// The original's comment: if the import chain is too long, emit an error.
	// Otherwise we risk blowing the stack.
	if recursionCount > maxImportDepth {
		file.SourceFile.SetHitMaxImportDepth(maxImportDepth)
		return
	}

	// Add the file to the closure map.
	closureMap.Set(fileUri.Key(), file)

	// The original's comment: if this file hasn't already been parsed, parse it
	// now. This will discover any files it imports. Skip this if the file is
	// part of a library. We'll assume that no cycles will be generated from
	// library code or typeshed stubs.
	if IsUserCode(file) {
		p.parseFile(file, "", false, false)
	}

	// Recursively add the file's imports.
	for _, importedFileInfo := range file.Imports() {
		p.getImportsRecursive(importedFileInfo, closureMap, recursionCount+1)
	}
}

func (p *Program) detectAndReportImportCycles(
	sourceFileInfo *SourceFileInfo,
	filesVisited *common.OrderedMap[string, *SourceFileInfo],
	dependencyChain []*SourceFileInfo,
	dependencyMap map[string]bool,
) bool {
	// Don't bother checking for typestub files or third-party files.
	if sourceFileInfo.SourceFile.IsStubFile() || sourceFileInfo.IsThirdPartyImport {
		return false
	}

	// The original's comment: if we've already confirmed that this source file
	// isn't part of a cycle, we can skip it entirely.
	if sourceFileInfo.SourceFile.IsNoCircularDependencyConfirmed() {
		return false
	}

	fileUri := sourceFileInfo.Uri()

	filesVisited.Set(fileUri.Key(), sourceFileInfo)

	detectedCycle := false

	if _, present := dependencyMap[fileUri.Key()]; present {
		// The original's comment: we detect a cycle (partial or full). A full
		// cycle is one that is rooted in the file at the start of our
		// dependency chain. A partial cycle loops back on some other file in
		// the dependency chain. We will report only full cycles here and leave
		// the reporting of partial cycles to other passes.
		detectedCycle = true

		// The original's comment: look for chains at least two in length. A
		// file that contains an "import . from X" will technically create a
		// cycle with itself, but those are not interesting to report.
		if len(dependencyChain) > 1 && sourceFileInfo == dependencyChain[0] {
			p.logImportCycle(dependencyChain)
		}
	} else {
		// The original re-tests dependencyMap.has here, which cannot be true on
		// this arm.

		// The original's comment: we use both a map (for fast lookups) and a
		// list (for ordering information). Set the dependency map entry to true
		// to indicate that we're actively exploring that dependency.
		dependencyMap[fileUri.Key()] = true
		dependencyChain = append(dependencyChain, sourceFileInfo)

		for _, imp := range sourceFileInfo.Imports() {
			if p.detectAndReportImportCycles(imp, filesVisited, dependencyChain, dependencyMap) {
				detectedCycle = true
			}
		}

		// The original's comment: set the dependencyMap entry to false to
		// indicate that we have already explored this file and don't need to
		// explore it again.
		dependencyMap[fileUri.Key()] = false
		dependencyChain = dependencyChain[:len(dependencyChain)-1]
	}

	return detectedCycle
}

func (p *Program) logImportCycle(dependencyChain []*SourceFileInfo) {
	circDep := NewCircularDependency()
	for _, sourceFileInfo := range dependencyChain {
		circDep.AppendPath(sourceFileInfo.Uri())
	}

	circDep.NormalizeOrder()
	firstFilePath := circDep.GetPaths()[0]
	firstSourceFile := p.GetSourceFileInfo(firstFilePath)
	common.Assert(firstSourceFile != nil, "")
	firstSourceFile.SourceFile.AddCircularDependency(p.configOptions, circDep)
}

// markFileDirtyRecursive corresponds to the private method of the same name.
// The TypeScript defaults forceRebinding to false.
func (p *Program) markFileDirtyRecursive(sourceFileInfo *SourceFileInfo, markSet map[string]bool, forceRebinding bool) {
	fileUri := sourceFileInfo.Uri()

	// Don't mark it again if it's already been visited.
	if markSet[fileUri.Key()] {
		return
	}

	sourceFileInfo.SourceFile.MarkReanalysisRequired(forceRebinding)
	markSet[fileUri.Key()] = true

	for _, dep := range sourceFileInfo.ImportedBy() {
		// The original's comment: changes on a chained source file can change
		// symbols in the symbol table and dependencies on the dependent file.
		// Force rebinding.
		p.markFileDirtyRecursive(dep, markSet, dep.ChainedSourceFile() == sourceFileInfo)
	}

	// The original's comment: a change in the current file could impact chained
	// notebook cells beyond checker-only results. Later CellDocs module
	// declarations participate in nested-scope name lookup for earlier cells, so
	// those earlier cells need rebinding when a later cell changes.
	reevaluationRequired := false
	chainedSourceFile := sourceFileInfo.ChainedSourceFile()
	for chainedSourceFile != nil {
		if chainedSourceFile.SourceFile.IsCheckingRequired() {
			// The original's comment: if the file is marked for checking, its
			// chained one should be marked as well. Stop here.
			break
		}

		reevaluationRequired = true
		chainedSourceFile.SourceFile.MarkReanalysisRequired(sourceFileInfo.IPythonMode() == IPythonModeCellDocs)
		chainedSourceFile = chainedSourceFile.ChainedSourceFile()
	}

	// The original's comment: if the checker is going to run again, we have to
	// recreate the type evaluator so that it actually reevaluates all the nodes
	// (instead of using the cache). This is necessary because the original file
	// change may not recreate the TypeEvaluator. For example, it might be a file
	// delete.
	if reevaluationRequired {
		p.createNewEvaluator()
	}
}
