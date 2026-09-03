/*
 * program.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * An object that tracks all of the source files being analyzed and all of their
 * associated state.
 *
 * Transliterated from analyzer/program.ts (pyright 1.1.412), split across
 * program.go (state, file lifecycle, the public surface) and
 * program_analysis.go (parse, bind, check, the import graph and cycle
 * detection).
 *
 * Four groups of members are deliberately dropped, all four out of scope for
 * this port:
 *
 *  - getSourceMapper / _createSourceMapper / bindShadowFile / _addShadowedFile.
 *    sourceMapper.ts is 1,148 lines that exist for go-to-definition on a stub.
 *  - getTextOnRange, getDiagnosticsForRange,
 *    getDiagnosticsForRangeWithoutFileIgnore, printDependencies,
 *    printDetailedAnalysisTimes, getTypeOfSymbol, printType. Language-server
 *    and CLI reporting.
 *  - clone() and runEditMode's promise arm, which serve the background-analysis
 *    threads.
 *  - The CancellationToken threading. Cancellation is the language server's,
 *    and _runEvaluatorWithCancellationToken exists to discard a
 *    half-cancelled evaluator -- which there is not one of yet.
 *
 * enterEditMode / exitEditMode, the edit-mode tracker and the SourceFileInfo
 * copy-on-write it drives are all kept: they are how the file list stays
 * consistent, not a language-server nicety.
 *
 * The evaluator plugs in through a factory. _createNewEvaluator builds one and hands
 * it to the checker; both are behind the interfaces sourceFile.go declares, so
 * with no factory installed the program parses, binds, resolves the import
 * graph, detects cycles and reports parse and bind diagnostics -- everything
 * except type checking.
 */

package analyzer

import (
	"strconv"
	"sync/atomic"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// maxImportDepth is the recursion limit _getImportsRecursive enforces.
const maxImportDepth = 256

// isTaggedHintDiagnostic corresponds to the module-level function of the same
// name.
func isTaggedHintDiagnostic(diag *common.Diagnostic) bool {
	return diag.Category == common.DiagnosticCategoryUnreachableCode ||
		diag.Category == common.DiagnosticCategoryUnusedCode ||
		diag.Category == common.DiagnosticCategoryDeprecated
}

// MaxAnalysisTime corresponds to the interface of the same name.
type MaxAnalysisTime struct {
	// OpenFilesTimeInMs is the maximum number of ms to analyze when there are
	// open files that require analysis. The original's comment: this number is
	// usually kept relatively small to guarantee responsiveness during typing.
	OpenFilesTimeInMs int64

	// NoOpenFilesTimeInMs is the maximum number of ms to analyze when all open
	// files and their dependencies have been analyzed. The original's comment:
	// this number can be higher to reduce overall analysis time but needs to be
	// short enough to remain responsive if an open file is modified.
	NoOpenFilesTimeInMs int64
}

// updateImportInfo corresponds to the interface of the same name.
type updateImportInfo struct {
	Path               uri.Uri
	IsTypeshedFile     bool
	IsThirdPartyImport bool
	IsPyTypedPresent   bool
}

// ChangedRange corresponds to the interface of the same name.
type ChangedRange struct {
	Range common.TextRange
	Delta int
}

// OpenFileOptions corresponds to the interface of the same name.
type OpenFileOptions struct {
	IPythonMode    IPythonMode
	ChainedFileUri uri.Uri
	ChangedRange   *ChangedRange
	IsVirtual      bool
}

// RequiringAnalysisCount corresponds to the interface of the same name in
// analyzer/analysis.ts.
type RequiringAnalysisCount struct {
	Files int
	Cells int
}

// editModeTracker tracks edit-mode related information.
type editModeTracker struct {
	isEditMode   bool
	mutatedFiles []*SourceFileInfo
}

func (t *editModeTracker) IsEditMode() bool { return t.isEditMode }

func (t *editModeTracker) AddMutatedFiles(file *SourceFileInfo) {
	t.mutatedFiles = append(t.mutatedFiles, file)
}

func (t *editModeTracker) enable() {
	t.isEditMode = true
	t.mutatedFiles = nil
}

func (t *editModeTracker) disable() []*SourceFileInfo {
	t.isEditMode = false

	files := t.mutatedFiles
	t.mutatedFiles = nil

	return files
}

// programNextId backs the `Prog_N` identifiers. Atomic because --threads
// creates one Program per worker goroutine; JavaScript's single thread made
// the original's plain increment safe.
var programNextId atomic.Int64

// Program is the container for all of the files that are being analyzed.
//
// The original's comment: files can fall into one or more of the following
// categories: Tracked (specified by the config options), Referenced (part of
// the transitive closure), Opened (temporarily opened in the editor), Shadowed
// (implementation file that shadows a type stub file).
type Program struct {
	console        common.ConsoleInterface
	sourceFileList []*SourceFileInfo
	sourceFileMap  map[string]*SourceFileInfo
	cellChainIndex *CellChainIndex

	logTracker   *LogTracker
	cacheManager *CacheManager
	id           string

	// allowedThirdPartyImports is `string[] | undefined`, and the absence is
	// distinguishable in _isImportAllowed and _checkTypes.
	allowedThirdPartyImports    []string
	hasAllowedThirdPartyImports bool

	configOptions   *ConfigOptions
	importResolver  *ImportResolver
	evaluator       TypeEvaluator
	disposed        bool
	parsedFileCount int

	editModeTracker *editModeTracker

	// disableChecker corresponds to the constructor's optional flag. The
	// original's comment at its use: disableChecker means disable semantic
	// errors, not syntax errors.
	disableChecker bool

	// evaluatorFactory is the evaluator seam; nil means no evaluator and no
	// checking.
	evaluatorFactory func(program *Program) TypeEvaluator

	// checkerFactory is handed to each SourceFile; see sourcefile.go.
	checkerFactory CheckerFactory

	// onFileDirty is handed to each SourceFile; see sourcefile.go.
	onFileDirty func(fileUri uri.Uri)
}

// NewProgram corresponds to the constructor. logTracker, disableChecker and id
// are optional in the original.
func NewProgram(
	initialImportResolver *ImportResolver,
	initialConfigOptions *ConfigOptions,
	console common.ConsoleInterface,
	logTracker *LogTracker,
	cacheManager *CacheManager,
	disableChecker bool,
	id string,
) *Program {
	if console == nil {
		console = common.NewStandardConsole(common.LogLevelLog)
	}
	if logTracker == nil {
		logTracker = NewLogTracker(console, "FG")
	}
	if cacheManager == nil {
		cacheManager = NewCacheManager()
	}

	p := &Program{
		console:         console,
		sourceFileMap:   map[string]*SourceFileInfo{},
		logTracker:      logTracker,
		cacheManager:    cacheManager,
		importResolver:  initialImportResolver,
		configOptions:   initialConfigOptions,
		editModeTracker: &editModeTracker{},
		disableChecker:  disableChecker,
	}

	p.cellChainIndex = NewCellChainIndex(
		func() []*SourceFileInfo { return p.sourceFileList },
		func(u uri.Uri) *SourceFileInfo { return p.GetSourceFileInfo(u) },
	)

	p.cacheManager.RegisterCacheOwner(p)
	p.createNewEvaluator()

	nextId := programNextId.Add(1) - 1
	if id == "" {
		id = "Prog_" + strconv.FormatInt(nextId, 10)
	}
	p.id = id

	return p
}

// SetEvaluatorFactory and SetCheckerFactory install the evaluator and checker factories; see the
// header.
func (p *Program) SetEvaluatorFactory(factory func(program *Program) TypeEvaluator) {
	p.evaluatorFactory = factory
	p.createNewEvaluator()
}

func (p *Program) SetCheckerFactory(factory CheckerFactory) { p.checkerFactory = factory }

// SetOnFileDirty installs the stand-in for the stateMutationListeners service.
func (p *Program) SetOnFileDirty(callback func(fileUri uri.Uri)) { p.onFileDirty = callback }

func (p *Program) ID() string { return p.id }

func (p *Program) Console() common.ConsoleInterface { return p.console }

func (p *Program) RootPath() uri.Uri { return p.configOptions.ProjectRoot }

func (p *Program) Evaluator() TypeEvaluator { return p.evaluator }

func (p *Program) ConfigOptions() *ConfigOptions { return p.configOptions }

func (p *Program) ImportResolver() *ImportResolver { return p.importResolver }

func (p *Program) FileSystem() uri.FileSystem { return p.importResolver.FileSystem() }

func (p *Program) IsDisposed() bool { return p.disposed }

func (p *Program) LookUpImport() ImportLookup { return p.lookUpImport }

func (p *Program) CellChainIndex() CellChainIndexProvider { return p.cellChainIndex }

func (p *Program) Dispose() {
	p.cacheManager.UnregisterCacheOwner(p)
	p.disposed = true
}

func (p *Program) EnterEditMode() { p.editModeTracker.enable() }

// ExitEditMode stops applying edit mode to new source files, restores the ones
// that were mutated, and reports the edits that undoes.
func (p *Program) ExitEditMode() []common.FileEditAction {
	mutatedFiles := p.editModeTracker.disable()

	filesToDelete := map[*SourceFileInfo]bool{}
	edits := []common.FileEditAction{}

	// The original's comment: tell all source files we're no longer in edit
	// mode. Gather up all of their edits and find files that are no longer
	// needed.
	for _, fileInfo := range mutatedFiles {
		if fileInfo.IsCreatedInEditMode {
			filesToDelete[fileInfo] = true
		}

		newContents := fileInfo.Restore()
		if newContents != "" {
			// The original builds a TextDocument only to read its lineCount,
			// which for vscode-languageserver-textdocument is the number of
			// line starts -- one more than the number of newlines, unless the
			// text is empty.
			lineCount := countTextDocumentLines(fileInfo.Contents())

			edits = append(edits, common.FileEditAction{
				FileUri: fileInfo.Uri(),
				TextEditAction: common.TextEditAction{
					Range: common.Range{
						Start: common.Position{Line: 0, Character: 0},
						End:   common.Position{Line: lineCount, Character: 0},
					},
					ReplacementText: newContents,
				},
			})
		}
	}

	// Delete files added while in edit mode.
	if len(filesToDelete) > 0 {
		// Delete from the back to make sure the index is valid.
		for i := len(p.sourceFileList) - 1; i >= 0; i-- {
			v := p.sourceFileList[i]
			if filesToDelete[v] {
				// The original's comment: we don't need to care about file
				// diagnostics since in edit mode the checker won't run.
				v.SourceFile.PrepareForClose()
				p.removeSourceFileFromListAndMap(v.Uri(), i)
			}
		}
	}

	if len(mutatedFiles) > 0 {
		// All cache is invalid now.
		p.createNewEvaluator()
	}

	return edits
}

func (p *Program) SetConfigOptions(configOptions *ConfigOptions) {
	p.configOptions = configOptions
	p.importResolver.SetConfigOptions(configOptions)

	// Create a new evaluator with the updated config options.
	p.createNewEvaluator()
}

func (p *Program) SetImportResolver(importResolver *ImportResolver) {
	p.importResolver = importResolver

	// The original's comment: create a new evaluator with the updated import
	// resolver. Otherwise, the lookup import passed to the type evaluator might
	// use an older import resolver when resolving imports after parsing.
	p.createNewEvaluator()
}

// SetTrackedFiles sets the list of tracked files that make up the program.
func (p *Program) SetTrackedFiles(fileUris []uri.Uri) []common.FileDiagnostics {
	if len(p.sourceFileList) > 0 {
		// We need to determine which files to remove from the existing file
		// list.
		newFileMap := map[string]bool{}
		for _, path := range fileUris {
			newFileMap[path.Key()] = true
		}

		// The original's comment: files that are not in the tracked file list
		// are marked as no longer tracked, but only for non-virtual files that
		// participate in source enumeration. Virtual documents (notebook cells,
		// chat blocks, stubs) are managed by the open/close lifecycle and should
		// not be untracked by the disk-based refresh path.
		for _, oldFile := range p.sourceFileList {
			if !newFileMap[oldFile.Uri().Key()] && !oldFile.IsVirtual() {
				oldFile.SetIsTracked(false)
			}
		}
	}

	// Add the new files. Only the new items will be added.
	p.AddTrackedFiles(fileUris, false, false)

	return p.removeUnneededFiles()
}

// SetAllowedThirdPartyImports carries the original's comment: by default, no
// third-party imports are allowed. This enables third-party imports for a
// specified import and its children. For example, if importNames is
// ['tensorflow'], then third-party (absolute) imports are allowed for 'import
// tensorflow', 'import tensorflow.optimizers', etc.
func (p *Program) SetAllowedThirdPartyImports(importNames []string) {
	p.allowedThirdPartyImports = importNames
	p.hasAllowedThirdPartyImports = true
}

// AddTrackedFiles corresponds to the method of the same name. The TypeScript
// defaults isThirdPartyImport and isInPyTypedPackage to false.
func (p *Program) AddTrackedFiles(fileUris []uri.Uri, isThirdPartyImport bool, isInPyTypedPackage bool) {
	for _, fileUri := range fileUris {
		p.AddTrackedFile(fileUri, isThirdPartyImport, isInPyTypedPackage)
	}
}

func (p *Program) AddInterimFile(fileUri uri.Uri) *SourceFileInfo {
	// Double check it's not already there.
	fileInfo := p.GetSourceFileInfo(fileUri)
	if fileInfo == nil {
		fileInfo = p.createInterimFileInfo(fileUri)
		p.addToSourceFileListAndMap(fileInfo)
	}
	return fileInfo
}

func (p *Program) AddTrackedFile(fileUri uri.Uri, isThirdPartyImport bool, isInPyTypedPackage bool) *SourceFile {
	if sourceFileInfo := p.GetSourceFileInfo(fileUri); sourceFileInfo != nil {
		// The original's comment: the module name may have changed based on
		// updates to the search paths. Clear any cached module name so it is
		// recomputed.
		sourceFileInfo.SourceFile.ClearCachedModuleName()
		sourceFileInfo.SetIsTracked(true)
		return sourceFileInfo.SourceFile
	}

	// The original's comment: detect py.typed status if not explicitly
	// provided. This ensures that files from py.typed packages are correctly
	// marked even when added directly to check paths (e.g., via command line).
	effectiveIsInPyTypedPackage := isInPyTypedPackage
	if !isInPyTypedPackage {
		moduleImportInfo := p.getModuleImportInfoForFile(fileUri)
		effectiveIsInPyTypedPackage = moduleImportInfo.IsThirdPartyPyTypedPresent
	}

	sourceFile := p.createSourceFile(fileUri, isThirdPartyImport, effectiveIsInPyTypedPackage, IPythonModeNone)

	// The original's comment: set the initial diagnostic rule set from the
	// execution environment so the file has config-level overrides (e.g.
	// reportPrivateImportUsage: false) from the start. Without this, files
	// added via positional args (which override configOptions.include) would
	// use the basic defaults until parse() runs.
	execEnv := p.configOptions.FindExecEnvironment(fileUri)
	sourceFile.SetInitialDiagnosticRuleSet(execEnv.DiagnosticRuleSet)

	sourceFileInfo := NewSourceFileInfo(
		sourceFile,
		sourceFile.IsTypingStubFile() || sourceFile.IsTypeshedStubFile() || sourceFile.IsBuiltInStubFile(),
		isThirdPartyImport,
		effectiveIsInPyTypedPackage,
		p.editModeTracker,
		SourceFileInfoArgs{IsTracked: true},
	)
	p.addToSourceFileListAndMap(sourceFileInfo)
	return sourceFile
}

// SetFileOpened corresponds to the method of the same name. A nil version is
// the original's null; nil options is its undefined.
func (p *Program) SetFileOpened(fileUri uri.Uri, version *int, contents string, options *OpenFileOptions) {
	sourceFileInfo := p.GetSourceFileInfo(fileUri)
	if sourceFileInfo == nil {
		moduleImportInfo := p.getModuleImportInfoForFile(fileUri)

		ipythonMode := IPythonModeNone
		var chainedFilePath uri.Uri
		isVirtual := false
		if options != nil {
			ipythonMode = options.IPythonMode
			chainedFilePath = options.ChainedFileUri
			isVirtual = options.IsVirtual
		}

		sourceFile := p.createSourceFile(fileUri, false, moduleImportInfo.IsThirdPartyPyTypedPresent, ipythonMode)

		var chainedSourceFile *SourceFileInfo
		if chainedFilePath != nil {
			chainedSourceFile = p.GetSourceFileInfo(chainedFilePath)
		}

		sourceFileInfo = NewSourceFileInfo(
			sourceFile,
			false, // isTypeshedFile
			false, // isThirdPartyImport
			false, // isThirdPartyPyTypedPresent
			p.editModeTracker,
			SourceFileInfoArgs{
				// The original's comment: tracking is determined internally --
				// virtual files are always tracked, otherwise check workspace
				// config.
				IsTracked:         isVirtual || MatchFileSpecs(p.configOptions, fileUri, true),
				IsVirtual:         isVirtual,
				ChainedSourceFile: chainedSourceFile,
				IsOpenByClient:    true,
			},
		)
		p.addToSourceFileListAndMap(sourceFileInfo)
	} else {
		sourceFileInfo.SetIsOpenByClient(true)

		// The original's comment: reset the diagnostic version so we force an
		// update to the diagnostics, which can change based on whether the file
		// is open. We do not set the version to undefined here because that
		// implies there are no diagnostics currently reported for this file.
		zero := 0
		sourceFileInfo.SetDiagnosticsVersion(&zero)
	}

	VerifyNoCyclesInChainedFiles(sourceFileInfo)
	if sourceFileInfo.IPythonMode() == IPythonModeCellDocs {
		p.cellChainIndex.Invalidate()
	}
	sourceFileInfo.SourceFile.SetClientVersion(version, contents)
}

// GetChainedUri returns nil where the original returns undefined.
func (p *Program) GetChainedUri(fileUri uri.Uri) uri.Uri {
	sourceFileInfo := p.GetSourceFileInfo(fileUri)
	if sourceFileInfo == nil || sourceFileInfo.ChainedSourceFile() == nil {
		return nil
	}
	return sourceFileInfo.ChainedSourceFile().Uri()
}

func (p *Program) UpdateChainedUri(fileUri uri.Uri, chainedFileUri uri.Uri) {
	sourceFileInfo := p.GetSourceFileInfo(fileUri)
	if sourceFileInfo == nil {
		return
	}

	if chainedFileUri != nil {
		sourceFileInfo.SetChainedSourceFile(p.GetSourceFileInfo(chainedFileUri))
	} else {
		sourceFileInfo.SetChainedSourceFile(nil)
	}
	sourceFileInfo.SourceFile.MarkDirty()
	p.markFileDirtyRecursive(sourceFileInfo, map[string]bool{}, false)
	p.cellChainIndex.Invalidate()

	VerifyNoCyclesInChainedFiles(sourceFileInfo)
}

func (p *Program) SetFileClosed(fileUri uri.Uri) []common.FileDiagnostics {
	if sourceFileInfo := p.GetSourceFileInfo(fileUri); sourceFileInfo != nil {
		sourceFileInfo.SetIsOpenByClient(false)

		// The original's comment: virtual documents (notebook cells, chat
		// blocks, synthetic stubs) exist only while the client keeps them open.
		// Once closed, they should become untracked so the regular cleanup path
		// (_removeUnneededFiles) can evict them.
		if sourceFileInfo.IsVirtual() {
			sourceFileInfo.SetIsTracked(false)
		}

		sourceFileInfo.SourceFile.SetClientVersion(nil, "")

		if sourceFileInfo.IPythonMode() == IPythonModeCellDocs {
			p.cellChainIndex.Invalidate()
		}

		// The original's comment: there is no guarantee that content is saved
		// before the file is closed. We need to mark the file dirty so we can
		// re-analyze next time. This won't matter much for OpenFileOnly users,
		// but it will matter for people who use diagnosticMode Workspace.
		if sourceFileInfo.SourceFile.DidContentsChangeOnDisk() {
			sourceFileInfo.SourceFile.MarkDirty()
			p.markFileDirtyRecursive(sourceFileInfo, map[string]bool{}, false)
		}
	}

	return p.removeUnneededFiles()
}

func (p *Program) MarkAllFilesDirty(evenIfContentsAreSame bool) {
	markDirtySet := map[string]bool{}

	for _, sourceFileInfo := range p.sourceFileList {
		if evenIfContentsAreSame {
			sourceFileInfo.SourceFile.MarkDirty()
		} else if sourceFileInfo.SourceFile.DidContentsChangeOnDisk() {
			sourceFileInfo.SourceFile.MarkDirty()

			// The original's comment: mark any files that depend on this file as
			// dirty also. This will retrigger analysis of these other files.
			p.markFileDirtyRecursive(sourceFileInfo, markDirtySet, false)
		}
	}

	if len(markDirtySet) > 0 {
		p.createNewEvaluator()
	}
}

func (p *Program) MarkFilesDirty(fileUris []uri.Uri, evenIfContentsAreSame bool) {
	markDirtySet := map[string]bool{}
	for _, fileUri := range fileUris {
		sourceFileInfo := p.GetSourceFileInfo(fileUri)
		if sourceFileInfo == nil {
			continue
		}

		fileName := fileUri.FileName()

		// The original's comment: handle builtins and __builtins__ specially.
		// They are implicitly included by all source files.
		if fileName == "builtins.pyi" || fileName == "__builtins__.pyi" {
			p.MarkAllFilesDirty(evenIfContentsAreSame)
			// The original returns from the forEach callback, which continues
			// the loop rather than ending it.
			continue
		}

		// The original's comment: if !evenIfContentsAreSame, see if the on-disk
		// contents have changed. If the file is open, the on-disk contents
		// don't matter because we'll receive updates directly from the client.
		if evenIfContentsAreSame ||
			(!sourceFileInfo.IsOpenByClient() && sourceFileInfo.SourceFile.DidContentsChangeOnDisk()) {
			sourceFileInfo.SourceFile.MarkDirty()

			p.markFileDirtyRecursive(sourceFileInfo, markDirtySet, false)
		}
	}

	if len(markDirtySet) > 0 {
		p.createNewEvaluator()
	}
}

// GetFileCount corresponds to the method of the same name. The TypeScript
// defaults userFileOnly to true.
func (p *Program) GetFileCount(userFileOnly bool) int {
	if userFileOnly {
		return len(p.GetUserFiles())
	}

	return len(p.sourceFileList)
}

// GetUserFileCount returns the number of files that are considered "user" files
// and therefore are checked.
func (p *Program) GetUserFileCount() int { return len(p.GetUserFiles()) }

func (p *Program) GetUserFiles() []*SourceFileInfo {
	return filterSourceFiles(p.sourceFileList, IsUserCode)
}

func (p *Program) GetOpened() []*SourceFileInfo {
	return filterSourceFiles(p.sourceFileList, func(s *SourceFileInfo) bool { return s.IsOpenByClient() })
}

func (p *Program) GetOwnedFiles() []*SourceFileInfo {
	return filterSourceFiles(p.sourceFileList, func(s *SourceFileInfo) bool {
		return IsUserCode(s) && p.Owns(s.Uri())
	})
}

func (p *Program) GetCheckingRequiredFiles() []*SourceFileInfo {
	return filterSourceFiles(p.sourceFileList, func(s *SourceFileInfo) bool {
		return s.IsOpenByClient() && p.Owns(s.Uri()) && s.SourceFile.IsCheckingRequired()
	})
}

func (p *Program) GetFilesToAnalyzeCount() RequiringAnalysisCount {
	filesToAnalyzeCount := 0
	cellsToAnalyzeCount := 0

	if p.disableChecker {
		return RequiringAnalysisCount{}
	}

	for _, fileInfo := range p.sourceFileList {
		sourceFile := fileInfo.SourceFile
		if sourceFile.IsCheckingRequired() {
			if p.shouldCheckFile(fileInfo) {
				if sourceFile.GetIPythonMode() == IPythonModeCellDocs {
					cellsToAnalyzeCount++
				} else {
					filesToAnalyzeCount++
				}
			}
		}
	}

	return RequiringAnalysisCount{Files: filesToAnalyzeCount, Cells: cellsToAnalyzeCount}
}

func (p *Program) IsCheckingOnlyOpenFiles() bool {
	return p.configOptions.CheckOnlyOpenFiles != nil && *p.configOptions.CheckOnlyOpenFiles
}

func (p *Program) FunctionSignatureDisplay() SignatureDisplayType {
	return p.configOptions.FunctionSignatureDisplay
}

func (p *Program) ContainsSourceFileIn(folder uri.Uri) bool {
	for _, fileInfo := range p.sourceFileList {
		if fileInfo.Uri().StartsWith(folder) {
			return true
		}
	}

	return false
}

func (p *Program) Owns(u uri.Uri) bool {
	if fileInfo := p.GetSourceFileInfo(u); fileInfo != nil {
		// The original's comment: if we already determined whether the file is
		// tracked or not, don't do it again. This will make sure we have a
		// consistent look at the state once it is loaded into memory.
		return fileInfo.IsTracked()
	}

	return MatchFileSpecs(p.configOptions, u, true)
}

// GetSourceFile returns nil where the original returns undefined.
func (p *Program) GetSourceFile(u uri.Uri) *SourceFile {
	sourceFileInfo := p.GetSourceFileInfo(u)
	if sourceFileInfo == nil {
		return nil
	}

	return sourceFileInfo.SourceFile
}

func (p *Program) GetBoundSourceFile(u uri.Uri) *SourceFile {
	info := p.GetBoundSourceFileInfo(u, "", false, false)
	if info == nil {
		return nil
	}
	return info.SourceFile
}

func (p *Program) GetSourceFileInfoList() []*SourceFileInfo { return p.sourceFileList }

func (p *Program) GetSourceFileInfo(u uri.Uri) *SourceFileInfo {
	if !u.IsEmpty() {
		return p.sourceFileMap[u.Key()]
	}
	return nil
}

func (p *Program) GetModuleSymbolTable(fileUri uri.Uri) *common.OrderedMap[string, *Symbol] {
	if sourceFileInfo := p.GetSourceFileInfo(fileUri); sourceFileInfo != nil {
		return sourceFileInfo.SourceFile.GetModuleSymbolTable()
	}
	return nil
}

func (p *Program) GetBoundSourceFileInfo(u uri.Uri, content string, hasContent bool, force bool) *SourceFileInfo {
	sourceFileInfo := p.GetSourceFileInfo(u)
	if sourceFileInfo == nil {
		return nil
	}

	p.bindFile(sourceFileInfo, content, hasContent, force, false)
	return sourceFileInfo
}

// GetDiagnostics corresponds to the method of the same name. The TypeScript
// defaults reportDeltasOnly to true.
func (p *Program) GetDiagnostics(options *ConfigOptions, reportDeltasOnly bool) []common.FileDiagnostics {
	fileDiagnostics := p.removeUnneededFiles()

	for _, sourceFileInfo := range p.sourceFileList {
		if p.shouldCheckFile(sourceFileInfo) {
			var prevVersion *int
			if reportDeltasOnly {
				prevVersion = sourceFileInfo.DiagnosticsVersion()
			}

			diagnostics, ok := sourceFileInfo.SourceFile.GetDiagnostics(options, prevVersion)
			if ok {
				// Filter out all categories that are translated to tagged hints.
				if options.DisableTaggedHints {
					filtered := []*common.Diagnostic{}
					for _, diag := range diagnostics {
						if !isTaggedHintDiagnostic(diag) {
							filtered = append(filtered, diag)
						}
					}
					diagnostics = filtered
				}

				fileDiagnostics = append(fileDiagnostics, common.FileDiagnostics{
					FileUri:     sourceFileInfo.Uri(),
					Version:     sourceFileInfo.SourceFile.GetClientVersion(),
					Diagnostics: diagnostics,
				})

				// The original's comment: update the cached diagnosticsVersion
				// so we can determine whether there are any updates next time
				// we call getDiagnostics.
				version := sourceFileInfo.SourceFile.GetDiagnosticVersion()
				sourceFileInfo.SetDiagnosticsVersion(&version)
			}
		} else if !sourceFileInfo.IsOpenByClient() &&
			p.configOptions.CheckOnlyOpenFiles != nil && *p.configOptions.CheckOnlyOpenFiles &&
			sourceFileInfo.DiagnosticsVersion() != nil {
			// The original's comment: this condition occurs when the user
			// switches from workspace to "open files only" mode. Clear all
			// diagnostics for this file.
			fileDiagnostics = append(fileDiagnostics, common.FileDiagnostics{
				FileUri:     sourceFileInfo.Uri(),
				Version:     sourceFileInfo.SourceFile.GetClientVersion(),
				Diagnostics: []*common.Diagnostic{},
			})
			sourceFileInfo.SetDiagnosticsVersion(nil)
		}
	}

	return fileDiagnostics
}

/*
 * CacheOwner
 */

func (p *Program) GetCacheUsage() float64 {
	// The original divides the evaluator's type cache entry count by a maximum;
	// with no evaluator there is nothing cached.
	return 0
}

func (p *Program) EmptyCache() {
	p.createNewEvaluator()
}

// HandleMemoryHighUsage corresponds to the public method of the same name.
func (p *Program) HandleMemoryHighUsage() { p.handleMemoryHighUsage() }

/*
 * File-list bookkeeping.
 */

func (p *Program) removeSourceFileFromListAndMap(fileUri uri.Uri, indexToRemove int) {
	delete(p.sourceFileMap, fileUri.Key())
	p.sourceFileList = append(p.sourceFileList[:indexToRemove], p.sourceFileList[indexToRemove+1:]...)
}

func (p *Program) addToSourceFileListAndMap(fileInfo *SourceFileInfo) {
	fileUri := fileInfo.Uri()

	// We should never add a file with the same path twice.
	_, exists := p.sourceFileMap[fileUri.Key()]
	common.Assert(!exists, "")

	// We should never have an empty URI for a source file.
	common.Assert(!fileUri.IsEmpty(), "")

	p.sourceFileList = append(p.sourceFileList, fileInfo)
	p.sourceFileMap[fileUri.Key()] = fileInfo
}

func (p *Program) getSourceFileInfoFromKey(key string) *SourceFileInfo {
	return p.sourceFileMap[key]
}

func (p *Program) getModuleName(fileUri uri.Uri) string {
	return p.getModuleImportInfoForFile(fileUri).ModuleName
}

func (p *Program) getModuleImportInfoForFile(fileUri uri.Uri) ModuleImportInfo {
	// The original's comment: we allow illegal module names (e.g. names that
	// include "-" in them) because we want a unique name for each module even
	// if it cannot be imported through an "import" statement. It's important to
	// have a unique name in case two modules declare types with the same local
	// name. The type checker uses the fully-qualified (unique) module name to
	// differentiate between such types.
	return p.importResolver.GetModuleNameForImport(
		fileUri,
		p.configOptions.GetDefaultExecEnvironment(),
		true, // allowIllegalModuleName
		true, // detectPyTyped
	)
}

// createSourceFile stands in for the ISourceFileFactory service, whose only
// implementation constructs a SourceFile.
func (p *Program) createSourceFile(
	fileUri uri.Uri,
	isThirdPartyImport bool,
	isThirdPartyPyTypedPresent bool,
	ipythonMode IPythonMode,
) *SourceFile {
	sourceFile := NewSourceFile(
		p.FileSystem(),
		fileUri,
		func(u uri.Uri) string { return p.getModuleName(u) },
		isThirdPartyImport,
		isThirdPartyPyTypedPresent,
		p.editModeTracker,
		p.console,
		p.logTracker,
		ipythonMode,
	)
	sourceFile.SetCheckerFactory(p.checkerFactory)
	sourceFile.SetOnFileDirty(p.onFileDirty)
	return sourceFile
}

func (p *Program) createInterimFileInfo(fileUri uri.Uri) *SourceFileInfo {
	moduleImportInfo := p.getModuleImportInfoForFile(fileUri)
	sourceFile := p.createSourceFile(fileUri, false, moduleImportInfo.IsThirdPartyPyTypedPresent, IPythonModeNone)

	return NewSourceFileInfo(
		sourceFile,
		moduleImportInfo.IsTypeshedFile,
		false, // isThirdPartyImport
		moduleImportInfo.IsThirdPartyPyTypedPresent,
		p.editModeTracker,
		SourceFileInfoArgs{},
	)
}

func (p *Program) createNewEvaluator() {
	if p.evaluatorFactory == nil {
		p.evaluator = nil
		return
	}
	p.evaluator = p.evaluatorFactory(p)
}

// countTextDocumentLines is TextDocument.lineCount: the number of line starts.
// An empty document has one line; every newline adds another.
func countTextDocumentLines(text string) int {
	lines := 1
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			lines++
		}
	}
	return lines
}

func filterSourceFiles(list []*SourceFileInfo, keep func(*SourceFileInfo) bool) []*SourceFileInfo {
	out := []*SourceFileInfo{}
	for _, s := range list {
		if keep(s) {
			out = append(out, s)
		}
	}
	return out
}
