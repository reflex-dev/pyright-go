/*
 * importresolver.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Provides the logic for resolving imports according to the module search
 * paths defined for the execution environment.
 *
 * Transliterated from analyzer/importResolver.ts (pyright 1.1.412), split
 * across importresolver*.go: this file holds the types, the constructor, the
 * public entry points and the module-level helpers; importresolver_resolve.go
 * the resolution algorithm; importresolver_typeshed.go the typeshed lookups;
 * importresolver_modulename.go the inverse mapping from a path back to a module
 * name; and importresolver_completions.go the completion suggestions.
 *
 * Two structural notes.
 *
 * The original takes a ServiceProvider and pulls five things out of it: the
 * file system, the temp-file provider, the partial-stub service, and optional
 * overrides for the cached filesystem facade and the typeshed info provider.
 * ANALYZER-PLAN.md calls for the DI plumbing to become small interfaces at the
 * point of consumption, so the constructor takes them directly. `tmp` is
 * dropped: nothing in ImportResolver reads it.
 *
 * Five methods are `protected` and documented as extension points for
 * subclasses -- getTypeshedPathEx, resolveImportEx, resolveNativeImportEx, and
 * the two they call. Go has no subclassing, so each is a field holding a hook
 * function; nil means the base behaviour, which in every case is "return
 * undefined".
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/parser"
)

// ImportedModuleDescriptor corresponds to the interface of the same name.
//
// ImportedSymbols is `Set<string> | undefined` and the difference matters:
// filterImplicitImports and _isNamespacePackageResolved both branch on the
// absence, not on emptiness.
type ImportedModuleDescriptor struct {
	LeadingDots     int
	NameParts       []string
	HasTrailingDot  bool
	ImportedSymbols *common.OrderedSet[string]
}

// ModuleNameAndType corresponds to the interface of the same name.
type ModuleNameAndType struct {
	ModuleName         string
	ImportType         ImportType
	IsLocalTypingsFile bool
}

// ModuleImportInfo corresponds to the interface of the same name, which extends
// ModuleNameAndType.
type ModuleImportInfo struct {
	ModuleNameAndType

	IsTypeshedFile             bool
	IsThirdPartyPyTypedPresent bool
}

// ModuleNameInfoFromPath corresponds to the interface of the same name.
type ModuleNameInfoFromPath struct {
	ModuleName                string
	ContainsInvalidCharacters bool
}

// CreateImportedModuleDescriptor corresponds to the function of the same name.
func CreateImportedModuleDescriptor(moduleName string) ImportedModuleDescriptor {
	if len(moduleName) == 0 {
		return ImportedModuleDescriptor{
			LeadingDots:     0,
			NameParts:       []string{},
			ImportedSymbols: common.NewOrderedSet[string](),
		}
	}

	startIndex := 0
	leadingDots := 0
	for ; startIndex < len(moduleName); startIndex++ {
		if moduleName[startIndex] != '.' {
			break
		}

		leadingDots++
	}

	return ImportedModuleDescriptor{
		LeadingDots:     leadingDots,
		NameParts:       strings.Split(moduleName[startIndex:], "."),
		ImportedSymbols: common.NewOrderedSet[string](),
	}
}

// cachedImportResults corresponds to the type alias of the same name. It is
// ordered because getSourceFilesFromStub iterates it and appends to an array
// the caller sees.
type cachedImportResults = common.OrderedMap[string, *ImportResult]

var supportedNativeLibExtensions = []string{".pyd", ".so", ".dylib"}

var SupportedSourceFileExtensions = []string{".py", ".pyi"}

var SupportedFileExtensions = append(append([]string{}, SupportedSourceFileExtensions...), supportedNativeLibExtensions...)

// allowPartialResolutionForThirdPartyPackages carries the original's comment:
// should we allow partial resolution for third-party packages? Some use tricks
// to populate their package namespaces, so we might be able to partially
// resolve a multi-part import (e.g. "a.b.c") but not fully resolve it. If this
// is set to false, we will have some false positives. If it is set to true, we
// won't report errors when these partial-resolutions fail.
const allowPartialResolutionForThirdPartyPackages = false

// cachedPythonSearchPaths corresponds to the anonymous type of
// _cachedPythonSearchPaths.
type cachedPythonSearchPaths struct {
	Paths       []uri.Uri
	FailureInfo *ImportLogger
}

// ImportResolverHooks are the `protected` extension points the original expects
// a subclass to override. A nil hook is the base class's implementation, which
// returns undefined in every case.
type ImportResolverHooks struct {
	// GetTypeshedPathEx provides additional stub path capabilities.
	GetTypeshedPathEx func(execEnv *ExecutionEnvironment, importLogger *ImportLogger) uri.Uri

	// ResolveImportEx provides additional stub resolving capabilities.
	ResolveImportEx func(
		sourceFileUri uri.Uri,
		execEnv *ExecutionEnvironment,
		moduleDescriptor ImportedModuleDescriptor,
		importName string,
		importLogger *ImportLogger,
		allowPyi bool,
	) *ImportResult

	// ResolveNativeImportEx provides additional stub resolving capabilities for
	// native (compiled) modules.
	ResolveNativeImportEx func(libraryFileUri uri.Uri, importName string, importLogger *ImportLogger) uri.Uri
}

// ImportResolver corresponds to the class of the same name.
type ImportResolver struct {
	fileSystem   uri.FileSystem
	console      common.ConsoleInterface
	partialStubs SupportPartialStubs
	host         Host
	hooks        ImportResolverHooks

	configOptions *ConfigOptions

	cachedPythonSearchPaths *cachedPythonSearchPaths

	// cachedImportResults is keyed by `execEnv.root?.key`, where undefined
	// becomes the empty string -- see the note on _lookUpResultsInCache.
	cachedImportResults     *common.OrderedMap[string, *cachedImportResults]
	cachedModuleNameResults map[string]map[string]ModuleImportInfo

	// stdlibModules is `Set<string> | undefined`; nil is the unbuilt state.
	stdlibModules *common.OrderedSet[string]

	fileSystemCache      ImportResolverFileSystem
	typeshedInfoProvider TypeshedInfoProvider

	// cachedParentImportResults is `protected readonly` in the original.
	cachedParentImportResults *ParentDirectoryCache
}

// NewImportResolver corresponds to the constructor.
//
// fileSystemCache and typeshedInfoProvider are the two optional services the
// original pulls out of the ServiceProvider, with the original's comment: these
// are optionally provided so callers/tests can share caching across
// ImportResolver/Typeshed operations and avoid re-walking the same filesystem
// paths when multiple resolvers are created. Pass nil for the defaults.
//
// partialStubs is `serviceProvider.partialStubs()`, which the original treats
// as possibly absent -- `this.partialStubs?.clearPartialStubs()` -- so nil is
// allowed here too.
func NewImportResolver(
	fileSystem uri.FileSystem,
	console common.ConsoleInterface,
	partialStubs SupportPartialStubs,
	configOptions *ConfigOptions,
	host Host,
	fileSystemCache ImportResolverFileSystem,
	typeshedInfoProvider TypeshedInfoProvider,
	hooks ImportResolverHooks,
) *ImportResolver {
	r := &ImportResolver{
		fileSystem:              fileSystem,
		console:                 console,
		partialStubs:            partialStubs,
		host:                    host,
		hooks:                   hooks,
		configOptions:           configOptions,
		cachedImportResults:     common.NewOrderedMap[string, *cachedImportResults](),
		cachedModuleNameResults: map[string]map[string]ModuleImportInfo{},
	}

	r.cachedParentImportResults = NewParentDirectoryCache(func() []uri.Uri {
		return r.GetPythonSearchPaths(nil)
	})

	r.fileSystemCache = fileSystemCache
	if r.fileSystemCache == nil {
		r.fileSystemCache = CreateImportResolverFileSystem(fileSystem)
	}
	r.typeshedInfoProvider = typeshedInfoProvider
	if r.typeshedInfoProvider == nil {
		r.typeshedInfoProvider = CreateDefaultTypeshedInfoProvider(r.fileSystemCache)
	}

	return r
}

func (r *ImportResolver) FileSystem() uri.FileSystem { return r.fileSystem }

func (r *ImportResolver) Host() Host { return r.host }

func (r *ImportResolver) PartialStubs() SupportPartialStubs { return r.partialStubs }

// IsSupportedImportSourceFile corresponds to the static of the same name.
func IsSupportedImportSourceFile(u uri.Uri) bool {
	fileExtension := strings.ToLower(u.LastExtension())
	for _, ext := range SupportedSourceFileExtensions {
		if fileExtension == ext {
			return true
		}
	}
	return false
}

// IsSupportedImportFile corresponds to the static of the same name.
func IsSupportedImportFile(u uri.Uri) bool {
	fileExtension := strings.ToLower(u.LastExtension())
	for _, ext := range SupportedFileExtensions {
		if fileExtension == ext {
			return true
		}
	}
	return false
}

func (r *ImportResolver) InvalidateCache() {
	r.cachedImportResults = common.NewOrderedMap[string, *cachedImportResults]()
	r.cachedModuleNameResults = map[string]map[string]ModuleImportInfo{}
	r.cachedParentImportResults.Reset()
	r.stdlibModules = nil

	r.invalidateFileSystemCache()

	if r.partialStubs != nil {
		r.partialStubs.ClearPartialStubs()
	}
}

// ResolveImport resolves the import and returns the path if it exists,
// otherwise returns undefined.
//
// The original's comment: wrap internal call to resolveImportInternal() to
// prevent calling any child class version of resolveImport().
func (r *ImportResolver) ResolveImport(
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	moduleDescriptor ImportedModuleDescriptor,
) *ImportResult {
	return r.resolveImportInternal(sourceFileUri, execEnv, moduleDescriptor)
}

func (r *ImportResolver) GetConfigOptions() *ConfigOptions { return r.configOptions }

func (r *ImportResolver) SetConfigOptions(configOptions *ConfigOptions) {
	r.configOptions = configOptions
	r.InvalidateCache()
}

// GetSourceFilesFromStub returns the implementation file(s) for the given stub
// file.
func (r *ImportResolver) GetSourceFilesFromStub(stubFileUri uri.Uri, execEnv *ExecutionEnvironment, mapCompiled bool) []uri.Uri {
	sourceFileUris := []uri.Uri{}

	// The original's comment: when ImportResolver resolves an import to a stub
	// file, a second resolve is done ignoring stub files, which gives us an
	// approximation of where the implementation for that stub is located.
	for _, m := range r.cachedImportResults.Values() {
		for _, result := range m.Values() {
			if result.IsStubFile && result.IsImportFound && result.NonStubImportResult != nil {
				if result.ResolvedUris[len(result.ResolvedUris)-1].Equals(stubFileUri) {
					if result.NonStubImportResult.IsImportFound {
						nonEmptyUri := result.NonStubImportResult.ResolvedUris[len(result.NonStubImportResult.ResolvedUris)-1]

						if nonEmptyUri.HasExtension(".py") || nonEmptyUri.HasExtension(".pyi") {
							// We allow pyi in case there are multiple pyi for a
							// compiled module such as numpy.random.mtrand.
							sourceFileUris = append(sourceFileUris, nonEmptyUri)
						}
					}
				}
			}
		}
	}

	// We haven't seen an import of that stub; attempt to find the source in
	// some other ways.
	if len(sourceFileUris) == 0 {
		// Simple case where the stub and source files are next to each other.
		sourceFileUri := stubFileUri.ReplaceExtension(".py")
		if r.dirExistsCached(sourceFileUri) {
			sourceFileUris = append(sourceFileUris, sourceFileUri)
		}
	}

	if len(sourceFileUris) == 0 {
		// The original's comment: the stub and the source file may have the
		// same name, but be located in different folder hierarchies. Example:
		//   <stubPath>\package\module.pyi
		//   <site-packages>\package\module.py
		// We get the relative path(s) of the stub to its import root(s), in
		// theory there can be more than one, then look for source files in all
		// the import roots using the same relative path(s).
		importRoots := r.GetImportRoots(execEnv, false)

		relativeStubPaths := []string{}
		for _, importRootUri := range importRoots {
			if stubFileUri.IsChild(importRootUri) {
				parts := append([]string{}, importRootUri.GetRelativePathComponents(stubFileUri)...)

				if len(parts) >= 1 {
					// Handle the case where the symbol was resolved to a stubs
					// package rather than the real package. We'll strip off the
					// "-stubs" suffix in this case.
					if strings.HasSuffix(parts[0], common.StubsSuffix) {
						parts[0] = parts[0][:len(parts[0])-len(common.StubsSuffix)]
					}

					relativeStubPaths = append(relativeStubPaths, strings.Join(parts, "/"))
				}
			}
		}

		for _, relativeStubPath := range relativeStubPaths {
			for _, importRootUri := range importRoots {
				absoluteStubPath := importRootUri.ResolvePaths(relativeStubPath)
				absoluteSourcePath := absoluteStubPath.ReplaceExtension(".py")
				if r.fileExistsCached(absoluteSourcePath) {
					sourceFileUris = append(sourceFileUris, absoluteSourcePath)
				} else {
					filePathWithoutExtension := absoluteSourcePath.StripExtension()

					if filePathWithoutExtension.PathEndsWith("__init__") {
						// Did not match: <root>/package/__init__.py
						// Try equivalent: <root>/package.py
						absoluteSourcePath = filePathWithoutExtension.GetDirectory().PackageUri()
						if r.fileExistsCached(absoluteSourcePath) {
							sourceFileUris = append(sourceFileUris, absoluteSourcePath)
						}
					} else {
						// Did not match: <root>/package.py
						// Try equivalent: <root>/package/__init__.py
						absoluteSourcePath = filePathWithoutExtension.InitPyUri()
						if r.fileExistsCached(absoluteSourcePath) {
							sourceFileUris = append(sourceFileUris, absoluteSourcePath)
						}
					}
				}
			}
		}
	}

	return sourceFileUris
}

func (r *ImportResolver) IsStdlibModule(module ImportedModuleDescriptor, execEnv *ExecutionEnvironment) bool {
	if r.stdlibModules == nil {
		// The original's comment: the cache is built once (lazily) and memoized
		// until invalidateCache(), without keying on the execution environment.
		// The directory-backed gating in _buildStdlibCache is
		// pythonVersion/pythonPlatform-sensitive, so for a config with multiple
		// execution environments at different Python versions, whichever
		// execEnv builds the cache first decides whether version-gated
		// directory packages (e.g. zoneinfo) are visible to all execEnvs. This
		// is best-effort for the cache-building execEnv; that ambiguous
		// multi-execEnv config is uncommon and not worth the cost of keying
		// (and re-building) the cache per version+platform.
		r.stdlibModules = r.buildStdlibCache(r.GetTypeshedStdLibPath(execEnv), execEnv)
	}

	return r.stdlibModules.Has(strings.Join(module.NameParts, "."))
}

// GetImportRoots corresponds to the method of the same name. The TypeScript
// defaults forLogging to false.
func (r *ImportResolver) GetImportRoots(execEnv *ExecutionEnvironment, forLogging bool) []uri.Uri {
	roots := []uri.Uri{}

	stdTypeshed := r.getStdlibTypeshedPath(r.configOptions.TypeshedPath, execEnv.PythonVersion, execEnv.PythonPlatform, nil, nil)
	if stdTypeshed != nil {
		roots = append(roots, stdTypeshed)
	}

	// The "default" workspace has a root-less execution environment; ignore it.
	if execEnv.Root != nil {
		roots = append(roots, execEnv.Root)
	}

	roots = append(roots, execEnv.ExtraPaths...)

	if r.configOptions.StubPath != nil {
		roots = append(roots, r.configOptions.StubPath)
	}

	if forLogging {
		// The original's comment: there's one path for each third party
		// package, which blows up logging. Just get the root directly and show
		// it with `...` to indicate that this is where the third party folder
		// is in the roots.
		thirdPartyRoot := r.getThirdPartyTypeshedPath(r.configOptions.TypeshedPath, nil)
		if thirdPartyRoot != nil {
			roots = append(roots, thirdPartyRoot.ResolvePaths("..."))
		}
	} else {
		roots = append(roots, r.getThirdPartyTypeshedPackageRoots(nil)...)
	}

	if typeshedPathEx := r.getTypeshedPathEx(execEnv, nil); typeshedPathEx != nil {
		roots = append(roots, typeshedPathEx)
	}

	pythonSearchPaths := r.GetPythonSearchPaths(nil)
	if len(pythonSearchPaths) > 0 {
		roots = append(roots, pythonSearchPaths...)
	}

	return roots
}

func (r *ImportResolver) EnsurePartialStubPackages(execEnv *ExecutionEnvironment) bool {
	if r.partialStubs == nil {
		return false
	}

	if r.partialStubs.IsPartialStubPackagesScanned(execEnv) {
		return false
	}

	ps := r.partialStubs
	paths := []uri.Uri{}
	typeshedPathEx := r.getTypeshedPathEx(execEnv, nil)

	addPaths := func(path uri.Uri) {
		if path == nil || ps.IsPathScanned(path) {
			return
		}
		paths = append(paths, path)
	}

	// Add paths to search stub packages.
	addPaths(r.configOptions.StubPath)
	if execEnv.Root != nil {
		addPaths(execEnv.Root)
	} else {
		addPaths(r.configOptions.ProjectRoot)
	}
	for _, p := range execEnv.ExtraPaths {
		addPaths(p)
	}
	addPaths(typeshedPathEx)

	for _, p := range r.GetPythonSearchPaths(nil) {
		addPaths(p)
	}

	r.partialStubs.ProcessPartialStubPackages(paths, r.GetImportRoots(execEnv, false), typeshedPathEx, nil)
	r.invalidateFileSystemCache()
	return true
}

// GetPythonSearchPaths finds the site packages for the configured virtual
// environment.
func (r *ImportResolver) GetPythonSearchPaths(importLogger *ImportLogger) []uri.Uri {
	if r.cachedPythonSearchPaths == nil {
		found := FindPythonSearchPaths(r.fileSystem, r.configOptions, r.host, importLogger, false, nil)
		paths := make([]uri.Uri, 0, len(found))
		for _, p := range found {
			paths = append(paths, r.fileSystem.RealCasePath(p))
		}

		// Remove duplicates (yes, it happens). The original's `new Set(paths)`
		// dedupes by object identity, which for interned Uris is the same as by
		// key.
		seen := map[uri.Uri]bool{}
		unique := []uri.Uri{}
		for _, p := range paths {
			if !seen[p] {
				seen[p] = true
				unique = append(unique, p)
			}
		}

		r.cachedPythonSearchPaths = &cachedPythonSearchPaths{Paths: unique, FailureInfo: importLogger}
	}

	return r.cachedPythonSearchPaths.Paths
}

func (r *ImportResolver) fileExistsCached(u uri.Uri) bool {
	return r.fileSystemCache.FileExists(u)
}

func (r *ImportResolver) dirExistsCached(u uri.Uri) bool {
	return r.fileSystemCache.DirExists(u)
}

func (r *ImportResolver) invalidateFileSystemCache() {
	r.fileSystemCache.InvalidateCache()
}

// getTypeshedPathEx is the extension point; the base implementation returns
// undefined.
func (r *ImportResolver) getTypeshedPathEx(execEnv *ExecutionEnvironment, importLogger *ImportLogger) uri.Uri {
	if r.hooks.GetTypeshedPathEx == nil {
		return nil
	}
	return r.hooks.GetTypeshedPathEx(execEnv, importLogger)
}

func (r *ImportResolver) resolveImportEx(
	sourceFileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	moduleDescriptor ImportedModuleDescriptor,
	importName string,
	importLogger *ImportLogger,
	allowPyi bool,
) *ImportResult {
	if r.hooks.ResolveImportEx == nil {
		return nil
	}
	return r.hooks.ResolveImportEx(sourceFileUri, execEnv, moduleDescriptor, importName, importLogger, allowPyi)
}

func (r *ImportResolver) resolveNativeImportEx(libraryFileUri uri.Uri, importName string, importLogger *ImportLogger) uri.Uri {
	if r.hooks.ResolveNativeImportEx == nil {
		return nil
	}
	return r.hooks.ResolveNativeImportEx(libraryFileUri, importName, importLogger)
}

// GetNativeModuleName returns "" where the original returns undefined.
func (r *ImportResolver) GetNativeModuleName(u uri.Uri) string {
	fileExtension := strings.ToLower(u.LastExtension())
	if isNativeModuleFileExtension(fileExtension) {
		return common.StripFileExtension(u.FileName(), true)
	}
	return ""
}

/*
 * Module-level helpers.
 */

// FormatImportName corresponds to the function of the same name.
func FormatImportName(moduleDescriptor ImportedModuleDescriptor) string {
	return strings.Repeat(".", moduleDescriptor.LeadingDots) + strings.Join(moduleDescriptor.NameParts, ".")
}

// GetParentImportResolutionRoot corresponds to the function of the same name.
func GetParentImportResolutionRoot(sourceFileUri uri.Uri, executionRoot uri.Uri) uri.Uri {
	if !IsDefaultWorkspace(executionRoot) {
		return executionRoot
	}

	return sourceFileUri.GetDirectory()
}

// GetModuleNameFromPath returns "" where the original returns undefined. The
// TypeScript defaults stripTopContainerDir to false.
func GetModuleNameFromPath(containerPath uri.Uri, fileUri uri.Uri, stripTopContainerDir bool) string {
	moduleNameInfo, ok := getModuleNameInfoFromPath(containerPath, fileUri, stripTopContainerDir)
	if !ok || moduleNameInfo.ContainsInvalidCharacters {
		return ""
	}

	return moduleNameInfo.ModuleName
}

// getModuleNameInfoFromPath returns ok == false where the original returns
// undefined.
func getModuleNameInfoFromPath(containerPath uri.Uri, fileUri uri.Uri, stripTopContainerDir bool) (ModuleNameInfoFromPath, bool) {
	if !fileUri.StartsWith(containerPath) {
		return ModuleNameInfoFromPath{}, false
	}

	parts := append([]string{}, containerPath.GetRelativePathComponents(fileUri)...)
	if len(parts) > 0 {
		origLastPart := parts[len(parts)-1]

		// Strip the file extension from the last part.
		newLastPart := common.StripFileExtension(origLastPart, false)

		// If the module is native, strip the platform part, such as
		// 'cp36-win_amd64' in 'mtrand.cp36-win_amd64'.
		if isNativeModuleFileExtension(common.GetFileExtension(origLastPart, false)) {
			newLastPart = common.StripFileExtension(newLastPart, false)
		}

		parts[len(parts)-1] = newLastPart

		// Strip off the '/__init__' if it's present.
		if newLastPart == "__init__" {
			parts = parts[:len(parts)-1]
		}
	}

	if stripTopContainerDir {
		if len(parts) == 0 {
			return ModuleNameInfoFromPath{}, false
		}
		parts = parts[1:]
	}

	if len(parts) == 0 {
		return ModuleNameInfoFromPath{}, false
	}

	// Handle the case where the symbol was resolved to a stubs package rather
	// than the real package. We'll strip off the "-stubs" suffix in this case.
	if strings.HasSuffix(parts[0], common.StubsSuffix) {
		parts[0] = parts[0][:len(parts[0])-len(common.StubsSuffix)]
	}

	// Check whether parts contains invalid characters.
	containsInvalidCharacters := false
	for _, p := range parts {
		if !parser.IsPythonIdentifier(common.NewText(p)) {
			containsInvalidCharacters = true
			break
		}
	}

	return ModuleNameInfoFromPath{
		ModuleName:                strings.Join(parts, "."),
		ContainsInvalidCharacters: containsInvalidCharacters,
	}, true
}

func isNativeModuleFileExtension(fileExtension string) bool {
	for _, ext := range supportedNativeLibExtensions {
		if ext == fileExtension {
			return true
		}
	}
	return false
}

// IsDefaultWorkspace corresponds to the function of the same name in
// importResolver.ts, not to Uri.isDefaultWorkspace, which it also calls.
func IsDefaultWorkspace(u uri.Uri) bool {
	return u == nil || u.IsEmpty() || uri.IsDefaultWorkspace(u)
}

// GetDirectoryLeadingDotsPointsTo corresponds to the function of the same name
// in importStatementUtils.ts. It returns nil where the original returns
// undefined.
func GetDirectoryLeadingDotsPointsTo(fromDirectory uri.Uri, leadingDots int) uri.Uri {
	currentDirectory := fromDirectory
	for i := 1; i < leadingDots; i++ {
		if currentDirectory.IsRoot() {
			return nil
		}

		currentDirectory = currentDirectory.GetDirectory()
	}

	return currentDirectory
}
