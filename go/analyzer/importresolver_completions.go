/*
 * importresolver_completions.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The completion-suggestion half of analyzer/importResolver.ts (pyright
 * 1.1.412). See importresolver.go for how the file is split.
 *
 * These serve the language server rather than the analyzer, but they are in
 * importResolver.ts and they reach the same private state, so leaving them out
 * would mean leaving a hole in the middle of a class rather than skipping a
 * module.
 */

package analyzer

import (
	"regexp"
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

func (r *ImportResolver) GetCompletionSuggestions(
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	moduleDescriptor ImportedModuleDescriptor,
) *suggestionMap {
	suggestions := r.getCompletionSuggestionsStrict(sourceFileUri, execEnv, moduleDescriptor)

	// We only do parent import resolution for absolute paths.
	if moduleDescriptor.LeadingDots > 0 {
		return suggestions
	}

	root := GetParentImportResolutionRoot(sourceFileUri, execEnv.Root)
	origin := sourceFileUri.GetDirectory()

	current := origin
	for r.shouldWalkUp(current, root, execEnv) && current != nil {
		r.getCompletionSuggestionsAbsolute(sourceFileUri, execEnv, current, moduleDescriptor, suggestions, false /* strictOnly */)

		current = r.tryWalkUp(current)
	}

	return suggestions
}

// suggestionMap stands in for `Map<string, Uri>`: keyed by the suggestion
// name, holding the Uri. Not a UriMap, which is keyed the other way round --
// two different namespace packages both map to Uri.empty(), so keying by the
// Uri would collapse them.
type suggestionMap = common.OrderedMap[string, uri.Uri]

func (r *ImportResolver) getCompletionSuggestionsStrict(
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	moduleDescriptor ImportedModuleDescriptor,
) *suggestionMap {
	suggestions := common.NewOrderedMap[string, uri.Uri]()

	// Is it a relative import?
	if moduleDescriptor.LeadingDots > 0 {
		r.getCompletionSuggestionsRelative(sourceFileUri, execEnv, moduleDescriptor, suggestions)
	} else {
		// First check for a typeshed file.
		if len(moduleDescriptor.NameParts) > 0 {
			r.getCompletionSuggestionsTypeshedPath(sourceFileUri, execEnv, moduleDescriptor, true, suggestions)
		}

		// Look for it in the root directory of the execution environment.
		if execEnv.Root != nil {
			r.getCompletionSuggestionsAbsolute(sourceFileUri, execEnv, execEnv.Root, moduleDescriptor, suggestions, true)
		}

		for _, extraPath := range execEnv.ExtraPaths {
			r.getCompletionSuggestionsAbsolute(sourceFileUri, execEnv, extraPath, moduleDescriptor, suggestions, true)
		}

		// Check for a typings file.
		if r.configOptions.StubPath != nil {
			r.getCompletionSuggestionsAbsolute(sourceFileUri, execEnv, r.configOptions.StubPath, moduleDescriptor, suggestions, true)
		}

		// Check for a typeshed file.
		r.getCompletionSuggestionsTypeshedPath(sourceFileUri, execEnv, moduleDescriptor, false, suggestions)

		// Look for the import in the list of third-party packages.
		for _, searchPath := range r.GetPythonSearchPaths(nil) {
			r.getCompletionSuggestionsAbsolute(sourceFileUri, execEnv, searchPath, moduleDescriptor, suggestions, true)
		}
	}

	return suggestions
}

func (r *ImportResolver) getCompletionSuggestionsTypeshedPath(
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	moduleDescriptor ImportedModuleDescriptor,
	isStdLib bool,
	suggestions *suggestionMap,
) {
	var typeshedPaths []uri.Uri
	var typeshedPathEx uri.Uri
	if isStdLib {
		path := r.getStdlibTypeshedPath(
			r.configOptions.TypeshedPath, execEnv.PythonVersion, execEnv.PythonPlatform, nil, &moduleDescriptor)
		if path != nil {
			typeshedPaths = []uri.Uri{path}
		}
	} else {
		typeshedPaths = r.getThirdPartyTypeshedPackagePaths(moduleDescriptor, nil, false /* includeMatchOnly */)

		typeshedPathEx = r.getTypeshedPathEx(execEnv, nil)
	}

	if typeshedPaths == nil && typeshedPathEx == nil {
		return
	}

	all := append([]uri.Uri{}, typeshedPaths...)
	if typeshedPathEx != nil {
		all = append(all, typeshedPathEx)
	}
	for _, typeshedPath := range all {
		if r.dirExistsCached(typeshedPath) {
			r.getCompletionSuggestionsAbsolute(sourceFileUri, execEnv, typeshedPath, moduleDescriptor, suggestions, true)
		}
	}
}

func (r *ImportResolver) getCompletionSuggestionsRelative(
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	moduleDescriptor ImportedModuleDescriptor,
	suggestions *suggestionMap,
) {
	// Determine which search path this file is part of.
	directory := GetDirectoryLeadingDotsPointsTo(sourceFileUri.GetDirectory(), moduleDescriptor.LeadingDots)
	if directory == nil {
		return
	}

	// Now try to match the module parts from the current directory location.
	r.getCompletionSuggestionsAbsolute(sourceFileUri, execEnv, directory, moduleDescriptor, suggestions, true)
}

// getCompletionSuggestionsAbsolute corresponds to the method of the same name.
// The TypeScript defaults strictOnly to true.
func (r *ImportResolver) getCompletionSuggestionsAbsolute(
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	rootPath uri.Uri,
	moduleDescriptor ImportedModuleDescriptor,
	suggestions *suggestionMap,
	strictOnly bool,
) {
	// Starting at the specified path, walk the file system to find the
	// specified module.
	dirPath := rootPath

	// Copy the nameParts into a new array and add an extra empty part if there
	// is a trailing dot.
	nameParts := append([]string{}, moduleDescriptor.NameParts...)
	if moduleDescriptor.HasTrailingDot {
		nameParts = append(nameParts, "")
	}

	// The original's comment: we need to track this since a module might be
	// resolvable using a relative path but can't be resolved by absolute path.
	leadingDots := moduleDescriptor.LeadingDots
	parentNameParts := []string{}
	if len(nameParts) > 0 {
		parentNameParts = nameParts[:len(nameParts)-1]
	}

	// Handle the case where the user has typed the first dot (or multiple) in a
	// relative path.
	if len(nameParts) == 0 {
		r.addFilteredSuggestionsAbsolute(sourceFileUri, execEnv, dirPath, "", suggestions, leadingDots, parentNameParts, strictOnly)
	} else {
		for i := 0; i < len(nameParts); i++ {
			// Provide completions only if we're on the last part of the name.
			if i == len(nameParts)-1 {
				r.addFilteredSuggestionsAbsolute(
					sourceFileUri, execEnv, dirPath, nameParts[i], suggestions, leadingDots, parentNameParts, strictOnly)
			}

			dirPath = dirPath.CombinePaths(nameParts[i])
			if !r.dirExistsCached(dirPath) {
				break
			}
		}
	}
}

func (r *ImportResolver) addFilteredSuggestionsAbsolute(
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	currentPath uri.Uri,
	filter string,
	suggestions *suggestionMap,
	leadingDots int,
	parentNameParts []string,
	strictOnly bool,
) {
	// Enumerate all of the files and directories in the path, expanding links.
	dirEntries, _ := r.fileSystemCache.ReaddirEntriesSync(currentPath)
	entries := uri.GetFileSystemEntriesFromDirEntries(dirEntries, r.fileSystem, currentPath)

	for _, file := range entries.Files {
		// The original's comment: strip multi-dot extensions to handle file
		// names like "foo.cpython-32m.so". We want to detect the ".so" but
		// strip off the entire ".cpython-32m.so" extension.
		fileWithoutExtension := file.StripAllExtensions().FileName()

		if IsSupportedImportFile(file) {
			if fileWithoutExtension == "__init__" {
				continue
			}

			if filter != "" && !common.IsPatternInSymbol(filter, fileWithoutExtension) {
				continue
			}

			if !r.isUniqueValidSuggestion(fileWithoutExtension, suggestions) ||
				!r.isResolvableSuggestion(fileWithoutExtension, leadingDots, parentNameParts, sourceFileUri, execEnv, strictOnly) {
				continue
			}

			suggestions.Set(fileWithoutExtension, file)
		}
	}

	for _, dir := range entries.Directories {
		dirSuggestion := dir.FileName()
		if filter != "" && !strings.HasPrefix(dirSuggestion, filter) {
			continue
		}

		if !r.isUniqueValidSuggestion(dirSuggestion, suggestions) ||
			!r.isResolvableSuggestion(dirSuggestion, leadingDots, parentNameParts, sourceFileUri, execEnv, strictOnly) {
			continue
		}

		initPyiPath := dir.InitPyiUri()
		if r.fileExistsCached(initPyiPath) {
			suggestions.Set(dirSuggestion, initPyiPath)
			continue
		}

		initPyPath := dir.InitPyUri()
		if r.fileExistsCached(initPyPath) {
			suggestions.Set(dirSuggestion, initPyPath)
			continue
		}

		// It is a namespace package; there is no corresponding module path.
		suggestions.Set(dirSuggestion, uri.Empty())
	}
}

// isResolvableSuggestion carries the original's comment: fix for editable
// installed submodules where the suggested directory was a namespace directory
// that wouldn't resolve. Only used for absolute imports.
func (r *ImportResolver) isResolvableSuggestion(
	name string,
	leadingDots int,
	parentNameParts []string,
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	strictOnly bool,
) bool {
	// We always resolve names based on sourceFileUri.
	moduleDescriptor := ImportedModuleDescriptor{
		LeadingDots:     leadingDots,
		NameParts:       append(append([]string{}, parentNameParts...), name),
		ImportedSymbols: common.NewOrderedSet[string](),
	}

	// Make sure we don't use parent folder resolution when checking whether the
	// given name is resolvable.
	var importResult *ImportResult
	if strictOnly {
		importName := FormatImportName(moduleDescriptor)
		importResult = r.resolveImportStrict(importName, sourceFileUri, execEnv, moduleDescriptor, nil)
	} else {
		importResult = r.resolveImportInternal(sourceFileUri, execEnv, moduleDescriptor)
	}

	if importResult != nil && importResult.IsImportFound {
		// The original's comment: check the import isn't for a private or
		// protected module. If it is, then only allow it if there's no py.typed
		// file.
		if !IsPrivateOrProtectedName(name) || importResult.PyTypedInfo == nil {
			return true
		}
	}
	return false
}

// illegalModuleNameChars is `/[.-]/`.
var illegalModuleNameChars = regexp.MustCompile(`[.-]`)

func (r *ImportResolver) isUniqueValidSuggestion(suggestionToAdd string, suggestions *suggestionMap) bool {
	if suggestions.Has(suggestionToAdd) {
		return false
	}

	// Don't add directories with illegal module names.
	if illegalModuleNameChars.MatchString(suggestionToAdd) {
		return false
	}

	// Don't add directories with dunder names like "__pycache__".
	if IsDunderName(suggestionToAdd) && suggestionToAdd != "__future__" {
		return false
	}

	return true
}
