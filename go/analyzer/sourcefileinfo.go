/*
 * sourcefileinfo.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Class that represents information around a single source file.
 *
 * Transliterated from analyzer/sourceFileInfo.ts and
 * analyzer/sourceFileInfoUtils.ts (pyright 1.1.412).
 *
 * The class is a facade over a `WriteableData` that is copied before its first
 * mutation while the program is in edit mode, so the whole set of changes can
 * be rolled back at once. Every setter goes through _cachePreEditState, and
 * that is reproduced exactly: it is what makes `restore()` work, and the
 * language server's edit mode depends on it.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// EditModeTracker corresponds to the interface of the same name. Program
// implements it.
type EditModeTracker interface {
	IsEditMode() bool
	AddMutatedFiles(file *SourceFileInfo)
}

// SourceFileInfoArgs corresponds to the OptionalArguments interface.
type SourceFileInfoArgs struct {
	IsTracked      bool
	IsOpenByClient bool
	IsVirtual      bool

	// DiagnosticsVersion is `number | undefined`.
	DiagnosticsVersion *int

	BuiltinsImport    *SourceFileInfo
	ChainedSourceFile *SourceFileInfo

	// EffectiveFutureImports is `ReadonlySet<string> | undefined`, and the
	// difference matters: program.ts tests for the absence.
	EffectiveFutureImports *common.OrderedSet[string]
}

// sourceFileWriteableData corresponds to the WriteableData interface.
type sourceFileWriteableData struct {
	DiagnosticsVersion *int

	BuiltinsImport *SourceFileInfo

	// ChainedSourceFile carries the original's comment: the chained source file
	// is not supposed to exist on the file system but must exist in the
	// program's source file list. The module-level scope of the chained source
	// file will be inserted before the current file's scope.
	ChainedSourceFile *SourceFileInfo

	EffectiveFutureImports *common.OrderedSet[string]

	// The rest is information about why the file is included in the program and
	// its relation to other source files in the program.
	IsTracked      bool
	IsOpenByClient bool
	IsVirtual      bool
	Imports        []*SourceFileInfo
	ImportedBy     []*SourceFileInfo
	Shadows        []*SourceFileInfo
	ShadowedBy     []*SourceFileInfo
}

// SourceFileInfo tracks information about each source file in a program,
// including the reason it was added to the program and any dependencies that it
// has on other files in the program.
type SourceFileInfo struct {
	SourceFile *SourceFile

	IsTypeshedFile             bool
	IsThirdPartyImport         bool
	IsThirdPartyPyTypedPresent bool

	IsCreatedInEditMode bool

	editModeTracker EditModeTracker

	writableData *sourceFileWriteableData
	preEditData  *sourceFileWriteableData
}

func NewSourceFileInfo(
	sourceFile *SourceFile,
	isTypeshedFile bool,
	isThirdPartyImport bool,
	isThirdPartyPyTypedPresent bool,
	editModeTracker EditModeTracker,
	args SourceFileInfoArgs,
) *SourceFileInfo {
	info := &SourceFileInfo{
		SourceFile:                 sourceFile,
		IsTypeshedFile:             isTypeshedFile,
		IsThirdPartyImport:         isThirdPartyImport,
		IsThirdPartyPyTypedPresent: isThirdPartyPyTypedPresent,
		editModeTracker:            editModeTracker,
		IsCreatedInEditMode:        editModeTracker.IsEditMode(),
	}

	info.writableData = info.createWriteableData(args)
	info.cachePreEditState()
	return info
}

func (s *SourceFileInfo) DiagnosticsVersion() *int { return s.writableData.DiagnosticsVersion }

func (s *SourceFileInfo) BuiltinsImport() *SourceFileInfo { return s.writableData.BuiltinsImport }

func (s *SourceFileInfo) ChainedSourceFile() *SourceFileInfo { return s.writableData.ChainedSourceFile }

func (s *SourceFileInfo) EffectiveFutureImports() *common.OrderedSet[string] {
	return s.writableData.EffectiveFutureImports
}

func (s *SourceFileInfo) IsTracked() bool { return s.writableData.IsTracked }

func (s *SourceFileInfo) IsOpenByClient() bool { return s.writableData.IsOpenByClient }

func (s *SourceFileInfo) IsVirtual() bool { return s.writableData.IsVirtual }

func (s *SourceFileInfo) Uri() uri.Uri { return s.SourceFile.GetUri() }

func (s *SourceFileInfo) Contents() string { return s.SourceFile.GetFileContent() }

func (s *SourceFileInfo) IPythonMode() IPythonMode { return s.SourceFile.GetIPythonMode() }

func (s *SourceFileInfo) IsStubFile() bool { return s.SourceFile.IsStubFile() }

func (s *SourceFileInfo) IsTypingStubFile() bool { return s.SourceFile.IsTypingStubFile() }

func (s *SourceFileInfo) HasTypeAnnotations() bool {
	if parseResults := s.SourceFile.GetParserOutput(); parseResults != nil {
		return parseResults.HasTypeAnnotations
	}
	return false
}

func (s *SourceFileInfo) Imports() []*SourceFileInfo { return s.writableData.Imports }

func (s *SourceFileInfo) ImportedBy() []*SourceFileInfo { return s.writableData.ImportedBy }

func (s *SourceFileInfo) Shadows() []*SourceFileInfo { return s.writableData.Shadows }

func (s *SourceFileInfo) ShadowedBy() []*SourceFileInfo { return s.writableData.ShadowedBy }

func (s *SourceFileInfo) ClientVersion() *int { return s.SourceFile.GetClientVersion() }

func (s *SourceFileInfo) SemanticVersion() int { return s.SourceFile.GetSemanticVersion() }

func (s *SourceFileInfo) SetDiagnosticsVersion(value *int) {
	s.cachePreEditState()
	s.writableData.DiagnosticsVersion = value
}

func (s *SourceFileInfo) SetBuiltinsImport(value *SourceFileInfo) {
	s.cachePreEditState()
	s.writableData.BuiltinsImport = value
}

func (s *SourceFileInfo) SetChainedSourceFile(value *SourceFileInfo) {
	s.cachePreEditState()
	s.writableData.ChainedSourceFile = value
}

func (s *SourceFileInfo) SetEffectiveFutureImports(value *common.OrderedSet[string]) {
	s.cachePreEditState()
	s.writableData.EffectiveFutureImports = value
}

func (s *SourceFileInfo) SetIsTracked(value bool) {
	s.cachePreEditState()
	s.writableData.IsTracked = value
}

func (s *SourceFileInfo) SetIsOpenByClient(value bool) {
	s.cachePreEditState()
	s.writableData.IsOpenByClient = value
}

func (s *SourceFileInfo) SetIsVirtual(value bool) {
	s.cachePreEditState()
	s.writableData.IsVirtual = value
}

// Mutate corresponds to the method of the same name: the escape hatch for the
// four list fields, which have no setters.
func (s *SourceFileInfo) Mutate(callback func(data *sourceFileWriteableData)) {
	s.cachePreEditState()
	callback(s.writableData)
}

func (s *SourceFileInfo) Restore() string {
	if s.preEditData != nil {
		s.writableData = s.preEditData
		s.preEditData = nil

		// Some states have changed. Force some of the info to be re-calculated.
		s.SourceFile.DropParseAndBindInfo()
	}

	return s.SourceFile.Restore()
}

func (s *SourceFileInfo) cachePreEditState() {
	if !s.editModeTracker.IsEditMode() || s.preEditData != nil {
		return
	}

	s.preEditData = s.writableData
	s.writableData = cloneWriteableData(s.writableData)

	s.editModeTracker.AddMutatedFiles(s)
}

func (s *SourceFileInfo) createWriteableData(args SourceFileInfoArgs) *sourceFileWriteableData {
	return &sourceFileWriteableData{
		IsTracked:              args.IsTracked,
		IsOpenByClient:         args.IsOpenByClient,
		IsVirtual:              args.IsVirtual,
		BuiltinsImport:         args.BuiltinsImport,
		ChainedSourceFile:      args.ChainedSourceFile,
		DiagnosticsVersion:     args.DiagnosticsVersion,
		EffectiveFutureImports: args.EffectiveFutureImports,
		Imports:                []*SourceFileInfo{},
		ImportedBy:             []*SourceFileInfo{},
		Shadows:                []*SourceFileInfo{},
		ShadowedBy:             []*SourceFileInfo{},
	}
}

func cloneWriteableData(data *sourceFileWriteableData) *sourceFileWriteableData {
	return &sourceFileWriteableData{
		IsTracked:              data.IsTracked,
		IsOpenByClient:         data.IsOpenByClient,
		IsVirtual:              data.IsVirtual,
		BuiltinsImport:         data.BuiltinsImport,
		ChainedSourceFile:      data.ChainedSourceFile,
		DiagnosticsVersion:     data.DiagnosticsVersion,
		EffectiveFutureImports: data.EffectiveFutureImports,
		Imports:                append([]*SourceFileInfo{}, data.Imports...),
		ImportedBy:             append([]*SourceFileInfo{}, data.ImportedBy...),
		Shadows:                append([]*SourceFileInfo{}, data.Shadows...),
		ShadowedBy:             append([]*SourceFileInfo{}, data.ShadowedBy...),
	}
}

/*
 * sourceFileInfoUtils.ts
 */

func IsUserCode(fileInfo *SourceFileInfo) bool {
	return fileInfo != nil && fileInfo.IsTracked() && !fileInfo.IsThirdPartyImport && !fileInfo.IsTypeshedFile
}

func CollectImportedByRecursively(fileInfo *SourceFileInfo, importedBy map[*SourceFileInfo]bool) {
	for _, dep := range fileInfo.ImportedBy() {
		if importedBy[dep] {
			// Already visited.
			continue
		}

		importedBy[dep] = true
		CollectImportedByRecursively(dep, importedBy)
	}
}

// VerifyNoCyclesInChainedFiles panics where the original calls debug.fail. The
// original consults a debugInfoInspector service for a richer message; that
// service is language-server plumbing, so only the fallback message is here.
func VerifyNoCyclesInChainedFiles(fileInfo *SourceFileInfo) {
	nextChainedFile := fileInfo.ChainedSourceFile()
	if nextChainedFile == nil {
		return
	}

	seen := map[string]bool{fileInfo.Uri().Key(): true}
	for nextChainedFile != nil {
		path := nextChainedFile.Uri().Key()
		if seen[path] {
			// We found a cycle.
			common.Fail("Found a cycle in implicit imports files for " + path)
		}

		seen[path] = true
		nextChainedFile = nextChainedFile.ChainedSourceFile()
	}
}

// CreateChainedByList corresponds to the function of the same name: the reverse
// map of all chained files.
func CreateChainedByList(sourceFileList []*SourceFileInfo, fileInfo *SourceFileInfo) []*SourceFileInfo {
	chainedTo := map[*SourceFileInfo]*SourceFileInfo{}
	for _, file := range sourceFileList {
		if file.ChainedSourceFile() == nil {
			continue
		}

		chainedTo[file.ChainedSourceFile()] = file
	}

	visited := map[*SourceFileInfo]bool{}

	chainedByList := []*SourceFileInfo{fileInfo}
	current := fileInfo
	for current != nil {
		if visited[current] {
			common.Fail("detected a cycle in chained files")
		}
		visited[current] = true

		current = chainedTo[current]
		if current != nil {
			chainedByList = append(chainedByList, current)
		}
	}

	return chainedByList
}
