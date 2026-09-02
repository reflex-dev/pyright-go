/*
 * sourcefile.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Class that represents a single Python source or stub file.
 *
 * Transliterated from analyzer/sourceFile.ts (pyright 1.1.412), split across
 * sourcefile.go (state, lifecycle, parse, bind, check) and
 * sourcefile_diagnostics.go (the accumulated-diagnostic computation and the
 * task-list scan).
 *
 * The Stage D seam is `check()`. It builds a Checker over a TypeEvaluator, and
 * neither exists yet, so both are interfaces here with the evaluator opaque and
 * the checker supplied as a factory. When the factory is nil -- which is the
 * only configuration that can be built today -- check() marks checking done and
 * produces no diagnostics. Everything around it, including the reentrancy
 * flags, the timing and the diagnostic bookkeeping, is the original's, so
 * dropping a real checker in place changes one field.
 *
 * The ServiceProvider evaporates as it did for the import resolver: the
 * constructor takes the file system and console directly. The one service
 * sourceFile.ts reads beyond those is stateMutationListeners, which is a
 * language-server notification; it becomes an optional callback.
 */

package analyzer

import (
	"fmt"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// maxImportCyclesPerFile limits the number of import cycles tracked per source
// file.
const maxImportCyclesPerFile = 4

// MaxSourceFileSize allows files up to 50MB in length, same as VS Code.
// https://github.com/microsoft/vscode/blob/1e750a7514f365585d8dab1a7a82e0938481ea2f/src/vs/editor/common/model/textModel.ts#L194
const MaxSourceFileSize = 50 * 1024 * 1024

// resolveImportResult corresponds to the interface of the same name.
type resolveImportResult struct {
	Imports              []*ImportResult
	BuiltinsImportResult *ImportResult
}

// nextUniqueFileId is a monotonically increasing number used to create unique
// file IDs.
var nextUniqueFileId = 1

// TypeEvaluator stands in for analyzer/typeEvaluatorTypes.ts's 88-method
// interface, which is Stage D. Nothing in the program loop calls a method on
// it; it is only created, stored and handed to the checker.
type TypeEvaluator any

// Checker stands in for analyzer/checker.ts, which is Stage D.
type Checker interface {
	Check()
}

// CheckerFactory builds a Checker. A nil factory means no checking is
// performed; see the header.
type CheckerFactory func(
	importResolver *ImportResolver,
	evaluator TypeEvaluator,
	parserOutput *parser.ParserOutput,
	dependentFiles []*parser.ParserOutput,
) Checker

// SourceFileEditMode corresponds to the interface of the same name.
type SourceFileEditMode interface {
	IsEditMode() bool
}

// sourceFileWritableData corresponds to the WriteableData class.
type sourceFileWritableData struct {
	// DiagnosticVersion is incremented every time the diagnostics are updated.
	DiagnosticVersion int

	// FileContentsVersion is the generation count of the file contents. When
	// the contents change, this is incremented.
	FileContentsVersion int

	// SemanticVersion is incremented every time the semantics of the file might
	// have changed.
	SemanticVersion int

	// LastFileContentLength and LastFileContentHash are the length and hash of
	// the file the last time it was read from disk. Both are `number |
	// undefined`, and didContentsChangeOnDisk branches on the absence.
	LastFileContentLength *int
	LastFileContentHash   *int32

	// ClientDocumentContents is the client's version of the file. Undefined
	// implies that contents need to be read from disk.
	ClientDocumentContents *string
	ClientDocumentVersion  *int

	// AnalyzedFileContentsVersion is the version of the file contents that have
	// been analyzed.
	AnalyzedFileContentsVersion int

	// ParseTreeNeedsCleaning records whether we need to walk the parse tree and
	// clean the binder information hanging from it.
	ParseTreeNeedsCleaning bool

	ParsedFileContents *common.Text
	TokenizerLines     *common.TextRangeCollection[common.TextRange]
	TokenizerOutput    *parser.TokenizerOutput
	LineCount          *int

	ModuleSymbolTable *common.OrderedMap[string, *Symbol]

	// Reentrancy checks for binding and checking.
	IsBindingInProgress  bool
	IsCheckingInProgress bool

	// Diagnostics generated during different phases of analysis.
	ParseDiagnostics    []*common.Diagnostic
	CommentDiagnostics  []*common.Diagnostic
	BindDiagnostics     []*common.Diagnostic
	CheckerDiagnostics  []*common.Diagnostic
	TaskListDiagnostics []*common.Diagnostic
	TypeIgnoreLines     map[int]*parser.IgnoreComment
	TypeIgnoreAll       *parser.IgnoreComment
	PyrightIgnoreLines  map[int]*parser.IgnoreComment

	// AccumulatedDiagnostics combines all of the above information. The
	// original's comment: this needs to be recomputed any time the above
	// change.
	AccumulatedDiagnostics       []*common.Diagnostic
	DiagnosticsWithoutFileIgnore []*common.Diagnostic

	// CircularDependencies are the cycles that have been reported in this file.
	CircularDependencies          []*CircularDependency
	NoCircularDependencyConfirmed bool

	// HitMaxImportDepth is `number | undefined`.
	HitMaxImportDepth *int

	// IsBindingNeeded records whether we need to perform a binding step.
	IsBindingNeeded bool

	// IsCheckingNeeded records whether we have valid diagnostic results from a
	// checking pass.
	IsCheckingNeeded bool

	// CheckTime is the time (in ms) that the last check() call required for
	// this file.
	CheckTime *int64

	// Imports and BuiltinsImport hold information about implicit and explicit
	// imports from this file. Imports is `ImportResult[] | undefined` and the
	// absence is distinguishable: getImports() answers [] for it, while parse()
	// sets it to undefined on an internal error.
	Imports        []*ImportResult
	HasImports     bool
	BuiltinsImport *ImportResult

	// IsFileDeleted is true if the file appears to have been deleted.
	IsFileDeleted bool

	ParserOutput *parser.ParserOutput
}

func newSourceFileWritableData() *sourceFileWritableData {
	return &sourceFileWritableData{
		AnalyzedFileContentsVersion:  -1,
		ParseDiagnostics:             []*common.Diagnostic{},
		CommentDiagnostics:           []*common.Diagnostic{},
		BindDiagnostics:              []*common.Diagnostic{},
		CheckerDiagnostics:           []*common.Diagnostic{},
		TaskListDiagnostics:          []*common.Diagnostic{},
		TypeIgnoreLines:              map[int]*parser.IgnoreComment{},
		PyrightIgnoreLines:           map[int]*parser.IgnoreComment{},
		AccumulatedDiagnostics:       []*common.Diagnostic{},
		DiagnosticsWithoutFileIgnore: []*common.Diagnostic{},
		CircularDependencies:         []*CircularDependency{},
		IsBindingNeeded:              true,
		IsCheckingNeeded:             true,
	}
}

// SourceFile corresponds to the class of the same name.
type SourceFile struct {
	FileSystem uri.FileSystem

	console common.ConsoleInterface

	// uri is unique to this file within the workspace. It may not represent a
	// real file on disk.
	uri uri.Uri

	// fileID is a short string that is guaranteed to uniquely identify this
	// file.
	fileID string

	// moduleNameGetter lazily computes the module name from the file URI.
	moduleNameGetter func(file uri.Uri) string

	// cachedModuleName is the period-delimited import path for the module.
	cachedModuleName string

	isStubFile                 bool
	isThirdPartyImport         bool
	isTypingStubFile           bool
	isTypingExtensionsStubFile bool
	isTypeshedStubFile         bool
	isBuiltInStubFile          bool
	isThirdPartyPyTypedPresent bool

	editMode SourceFileEditMode

	// diagnosticRuleSet controls which diagnostics should be output. The
	// original's comment: the rules are initialized to the basic set. They
	// should be updated after the file is parsed.
	diagnosticRuleSet *DiagnosticRuleSet

	ipythonMode IPythonMode
	logTracker  *LogTracker

	// onFileDirty stands in for the stateMutationListeners service.
	onFileDirty func(fileUri uri.Uri)

	// checkerFactory is the Stage D seam; see the header.
	checkerFactory CheckerFactory

	preEditData  *sourceFileWritableData
	writableData *sourceFileWritableData
}

// NewSourceFile corresponds to the constructor. console, logTracker and
// ipythonMode are optional in the original; a nil console becomes a
// StandardConsole and a nil logTracker one named "FG", which is what the main
// thread gets.
func NewSourceFile(
	fileSystem uri.FileSystem,
	fileUri uri.Uri,
	moduleNameGetter func(file uri.Uri) string,
	isThirdPartyImport bool,
	isThirdPartyPyTypedPresent bool,
	editMode SourceFileEditMode,
	console common.ConsoleInterface,
	logTracker *LogTracker,
	ipythonMode IPythonMode,
) *SourceFile {
	if console == nil {
		console = common.NewStandardConsole(common.LogLevelLog)
	}
	if logTracker == nil {
		logTracker = NewLogTracker(console, "FG")
	}

	s := &SourceFile{
		FileSystem:                 fileSystem,
		console:                    console,
		writableData:               newSourceFileWritableData(),
		editMode:                   editMode,
		uri:                        fileUri,
		moduleNameGetter:           moduleNameGetter,
		isStubFile:                 fileUri.HasExtension(".pyi"),
		isThirdPartyImport:         isThirdPartyImport,
		isThirdPartyPyTypedPresent: isThirdPartyPyTypedPresent,
		diagnosticRuleSet:          GetBasicDiagnosticRuleSet(),
		logTracker:                 logTracker,
		ipythonMode:                ipythonMode,
	}
	s.fileID = s.makeFileID(fileUri)

	fileName := fileUri.FileName()
	s.isTypingStubFile = s.isStubFile &&
		(s.uri.PathEndsWith("stdlib/typing.pyi") || fileName == "typing_extensions.pyi")
	s.isTypingExtensionsStubFile = s.isStubFile && fileName == "typing_extensions.pyi"
	s.isTypeshedStubFile = s.isStubFile &&
		(s.uri.PathEndsWith("stdlib/_typeshed/__init__.pyi") ||
			s.uri.PathEndsWith("stdlib/_typeshed/_type_checker_internals.pyi"))

	s.isBuiltInStubFile = false
	if s.isStubFile {
		for _, suffix := range []string{
			"stdlib/collections/__init__.pyi",
			"stdlib/asyncio/futures.pyi",
			"stdlib/asyncio/tasks.pyi",
			"stdlib/builtins.pyi",
			"stdlib/_importlib_modulespec.pyi",
			"stdlib/dataclasses.pyi",
			"stdlib/abc.pyi",
			"stdlib/enum.pyi",
			"stdlib/queue.pyi",
			"stdlib/string/templatelib.pyi",
			"stdlib/types.pyi",
			"stdlib/warnings.pyi",
		} {
			if s.uri.PathEndsWith(suffix) {
				s.isBuiltInStubFile = true
				break
			}
		}
	}

	return s
}

// SetOnFileDirty installs the stand-in for the stateMutationListeners service.
func (s *SourceFile) SetOnFileDirty(callback func(fileUri uri.Uri)) { s.onFileDirty = callback }

// SetCheckerFactory installs the Stage D seam; see the header.
func (s *SourceFile) SetCheckerFactory(factory CheckerFactory) { s.checkerFactory = factory }

// SetInitialDiagnosticRuleSet sets the initial diagnostic rule set from the
// execution environment's config-level overrides.
//
// The original's comment: this should be called immediately after construction
// so the file has the correct rules before parse/bind.
func (s *SourceFile) SetInitialDiagnosticRuleSet(ruleSet *DiagnosticRuleSet) {
	s.diagnosticRuleSet = CloneDiagnosticRuleSet(ruleSet)
}

func (s *SourceFile) GetIPythonMode() IPythonMode { return s.ipythonMode }

func (s *SourceFile) GetUri() uri.Uri { return s.uri }

func (s *SourceFile) GetModuleName() string {
	if s.cachedModuleName == "" {
		// The original's comment: call the module name getter. If it returns ''
		// (which can happen if the file is not part of the project), fall back
		// to the file name.
		if name := s.moduleNameGetter(s.uri); name != "" {
			return name
		}
		return common.StripFileExtension(s.uri.FileName(), false)
	}

	return s.cachedModuleName
}

func (s *SourceFile) ClearCachedModuleName() { s.cachedModuleName = "" }

func (s *SourceFile) GetDiagnosticVersion() int { return s.writableData.DiagnosticVersion }

func (s *SourceFile) GetParseDiagnostics() []*common.Diagnostic {
	return s.writableData.ParseDiagnostics
}

func (s *SourceFile) IsStubFile() bool { return s.isStubFile }

func (s *SourceFile) IsTypingStubFile() bool { return s.isTypingStubFile }

func (s *SourceFile) IsTypeshedStubFile() bool { return s.isTypeshedStubFile }

func (s *SourceFile) IsBuiltInStubFile() bool { return s.isBuiltInStubFile }

func (s *SourceFile) IsThirdPartyPyTypedPresent() bool { return s.isThirdPartyPyTypedPresent }

// GetDiagnostics returns a list of cached diagnostics from the latest analysis
// job. It answers ok == false -- the original's undefined -- when the
// prevDiagnosticVersion matches, meaning the diagnostics haven't changed.
func (s *SourceFile) GetDiagnostics(configOptions *ConfigOptions, prevDiagnosticVersion *int) ([]*common.Diagnostic, bool) {
	if prevDiagnosticVersion != nil && s.writableData.DiagnosticVersion == *prevDiagnosticVersion {
		return nil, false
	}

	return s.writableData.AccumulatedDiagnostics, true
}

func (s *SourceFile) GetDiagnosticsWithoutFileIgnore() []*common.Diagnostic {
	return s.writableData.DiagnosticsWithoutFileIgnore
}

func (s *SourceFile) GetImports() []*ImportResult {
	if !s.writableData.HasImports {
		return []*ImportResult{}
	}
	return s.writableData.Imports
}

func (s *SourceFile) GetBuiltinsImport() *ImportResult { return s.writableData.BuiltinsImport }

func (s *SourceFile) GetModuleSymbolTable() *common.OrderedMap[string, *Symbol] {
	return s.writableData.ModuleSymbolTable
}

func (s *SourceFile) GetCheckTime() *int64 { return s.writableData.CheckTime }

// Restore returns "" where the original returns undefined.
func (s *SourceFile) Restore() string {
	// If we had an edit, return our text.
	if s.preEditData != nil {
		text := ""
		if s.writableData.ClientDocumentContents != nil {
			text = *s.writableData.ClientDocumentContents
		}
		s.writableData = s.preEditData
		s.preEditData = nil

		return text
	}

	return ""
}

// DidContentsChangeOnDisk indicates whether the contents of the file have
// changed since the last analysis was performed.
func (s *SourceFile) DidContentsChangeOnDisk() bool {
	// The original's comment: if this is an open file any content changes will
	// be provided through the editor. We can assume contents didn't change
	// without us knowing about them.
	//
	// The test is truthiness, so an open file with empty contents takes the
	// other branch.
	if s.writableData.ClientDocumentContents != nil && *s.writableData.ClientDocumentContents != "" {
		return false
	}

	// The original's comment: if the file was never read previously we can't
	// tell if the file has changed or not so we'll assume that it has.
	// Otherwise, we may fail to analyze a file that was changed.
	if s.writableData.LastFileContentLength == nil {
		return true
	}

	// Read in the latest file contents and see if the hash matches that of the
	// previous contents.
	if !s.FileSystem.ExistsSync(s.uri) {
		// No longer exists, so yes it has changed.
		return true
	}

	contents, err := s.FileSystem.ReadFileSync(s.uri)
	if err != nil {
		return true
	}
	fileContents := common.NewText(string(contents))

	if fileContents.Length() != *s.writableData.LastFileContentLength {
		return true
	}

	if s.writableData.LastFileContentHash == nil || common.HashText(fileContents) != *s.writableData.LastFileContentHash {
		return true
	}

	return false
}

// DropParseAndBindInfo drops parse and binding info to save memory.
//
// The original's comment: it is used in cases where memory is low. When info is
// needed, the file will be re-parsed and rebound.
func (s *SourceFile) DropParseAndBindInfo() {
	// The original's comment: if we are actively binding or checking this file,
	// we can't safely drop parse and binding info.
	if s.writableData.IsBindingInProgress || s.writableData.IsCheckingInProgress {
		return
	}

	s.fireFileDirtyEvent()

	s.writableData.ParserOutput = nil
	s.writableData.TokenizerLines = nil
	s.writableData.TokenizerOutput = nil
	s.writableData.ParsedFileContents = nil
	s.writableData.ModuleSymbolTable = nil
	s.writableData.IsBindingNeeded = true
	s.writableData.Imports = []*ImportResult{}
	s.writableData.HasImports = true
}

func (s *SourceFile) MarkDirty() {
	s.writableData.FileContentsVersion++
	s.writableData.SemanticVersion++
	s.writableData.NoCircularDependencyConfirmed = false
	s.writableData.IsCheckingNeeded = true
	s.writableData.IsBindingNeeded = true
	s.writableData.ModuleSymbolTable = nil
	s.writableData.LineCount = nil

	s.fireFileDirtyEvent()
}

// MarkReanalysisRequired keeps the parse info, but resets the analysis to the
// beginning.
func (s *SourceFile) MarkReanalysisRequired(forceRebinding bool) {
	s.writableData.SemanticVersion++
	s.writableData.IsCheckingNeeded = true
	s.writableData.NoCircularDependencyConfirmed = false

	// The original's comment: if the file contains a wildcard import or __all__
	// symbols, we need to rebind because a dependent import may have changed.
	if s.writableData.ParserOutput != nil {
		if s.writableData.ParserOutput.ContainsWildcardImport ||
			GetDunderAllInfo(s.writableData.ParserOutput.ParseTree) != nil ||
			forceRebinding {
			// The original's comment: we don't need to rebuild index data since
			// wildcard won't affect user file indices. User file indices don't
			// contain import alias info.
			s.writableData.ParseTreeNeedsCleaning = true
			s.writableData.IsBindingNeeded = true
			s.writableData.ModuleSymbolTable = nil
		}
	}
}

func (s *SourceFile) GetFileContentsVersion() int { return s.writableData.FileContentsVersion }

func (s *SourceFile) GetClientVersion() *int { return s.writableData.ClientDocumentVersion }

func (s *SourceFile) GetSemanticVersion() int { return s.writableData.SemanticVersion }

func (s *SourceFile) GetRange() common.Range {
	lineCount := 0
	if s.writableData.LineCount != nil {
		lineCount = *s.writableData.LineCount
	}
	return common.Range{
		Start: common.Position{Line: 0, Character: 0},
		End:   common.Position{Line: lineCount, Character: 0},
	}
}

// GetOpenFileContents returns ok == false where the original returns undefined.
func (s *SourceFile) GetOpenFileContents() (string, bool) {
	if s.writableData.ClientDocumentContents == nil {
		return "", false
	}
	return *s.writableData.ClientDocumentContents, true
}

// GetFileContent returns "" where the original returns undefined. The two are
// distinguishable to the original -- parse() throws on undefined -- but every
// caller that cares tests for undefined and an empty file reads as "", so the
// distinction is carried by getFileContentOk.
func (s *SourceFile) GetFileContent() string {
	content, _ := s.getFileContentOk()
	return content
}

func (s *SourceFile) getFileContentOk() (string, bool) {
	// Get current buffer content if the file is opened.
	if openFileContent, ok := s.GetOpenFileContents(); ok {
		return openFileContent, true
	}

	// The original's comment: ensure that the content used here is identical to
	// the content obtained from the parse results.
	if !s.IsParseRequired() && s.writableData.ParsedFileContents != nil {
		return s.writableData.ParsedFileContents.String(), true
	}

	// Otherwise, get content from the file system.
	fileStat, err := s.FileSystem.StatSync(s.uri)
	if err != nil {
		return "", false
	}
	if fileStat.Size() > MaxSourceFileSize {
		s.console.Error(fmt.Sprintf(
			`File length of "%s" is %d which exceeds the maximum supported file size of %d`,
			s.uri.String(), fileStat.Size(), MaxSourceFileSize))
		return "", false
	}

	contents, err := s.FileSystem.ReadFileSync(s.uri)
	if err != nil {
		return "", false
	}
	return string(contents), true
}

// SetClientVersion corresponds to the method of the same name. A nil version is
// the original's null, meaning the file is no longer open.
func (s *SourceFile) SetClientVersion(version *int, contents string) {
	// Save pre-edit state if in edit mode.
	s.cachePreEditState()

	if version == nil {
		s.writableData.ClientDocumentVersion = nil
		s.writableData.ClientDocumentContents = nil

		// The original's comment: since the file is no longer open, dump the
		// tokenizer output so it doesn't consume memory.
		s.writableData.TokenizerOutput = nil
		return
	}

	s.writableData.ClientDocumentVersion = version
	s.writableData.ClientDocumentContents = &contents

	text := common.NewText(contents)
	contentsHash := common.HashText(text)

	// Have the contents of the file changed?
	if s.writableData.LastFileContentLength == nil || text.Length() != *s.writableData.LastFileContentLength ||
		s.writableData.LastFileContentHash == nil || contentsHash != *s.writableData.LastFileContentHash {
		s.MarkDirty()
	}

	length := text.Length()
	s.writableData.LastFileContentLength = &length
	s.writableData.LastFileContentHash = &contentsHash
	s.writableData.IsFileDeleted = false
}

func (s *SourceFile) PrepareForClose() { s.fireFileDirtyEvent() }

func (s *SourceFile) IsFileDeleted() bool { return s.writableData.IsFileDeleted }

func (s *SourceFile) IsParseRequired() bool {
	return s.writableData.ParserOutput == nil ||
		s.writableData.AnalyzedFileContentsVersion != s.writableData.FileContentsVersion
}

func (s *SourceFile) IsBindingRequired() bool {
	if s.writableData.IsBindingInProgress {
		return false
	}

	if s.IsParseRequired() {
		return true
	}

	return s.writableData.IsBindingNeeded
}

func (s *SourceFile) IsCheckingRequired() bool { return s.writableData.IsCheckingNeeded }

// GetParseResults returns nil where the original returns undefined.
//
// The original's return value has a lazily-computed `tokenizerOutput` getter,
// so a file that is parsed but not open is not re-tokenized unless something
// asks. That is reproduced by ParseResults below rather than by materializing
// the tokenizer output here.
func (s *SourceFile) GetParseResults() *ParseResults {
	if s.IsParseRequired() {
		return nil
	}

	common.Assert(s.writableData.ParserOutput != nil && s.writableData.ParsedFileContents != nil, "")

	contentHash := common.HashText(*s.writableData.ParsedFileContents)
	if s.writableData.LastFileContentHash != nil && *s.writableData.LastFileContentHash != 0 {
		contentHash = *s.writableData.LastFileContentHash
	}

	return &ParseResults{
		sourceFile:      s,
		ContentHash:     contentHash,
		ParserOutput:    s.writableData.ParserOutput,
		Text:            *s.writableData.ParsedFileContents,
		tokenizerOutput: s.writableData.TokenizerOutput,
	}
}

// ParseResults is the ParseFileResults the original returns from
// getParseResults, with the lazy tokenizerOutput getter as a method.
type ParseResults struct {
	sourceFile *SourceFile

	ContentHash  int32
	ParserOutput *parser.ParserOutput
	Text         common.Text

	tokenizerOutput *parser.TokenizerOutput
}

// TokenizerOutput lazily tokenizes the file contents only when accessed for the
// first time.
func (r *ParseResults) TokenizerOutput() *parser.TokenizerOutput {
	if r.tokenizerOutput == nil {
		r.tokenizerOutput = r.sourceFile.tokenizeContents(r.Text, r.ContentHash)
	}
	return r.tokenizerOutput
}

// GetParserOutput returns nil where the original returns undefined.
func (s *SourceFile) GetParserOutput() *parser.ParserOutput {
	if s.IsParseRequired() {
		return nil
	}

	common.Assert(s.writableData.ParserOutput != nil, "")

	return s.writableData.ParserOutput
}

// AddCircularDependency adds a new circular dependency for this file but only
// if it hasn't already been added.
func (s *SourceFile) AddCircularDependency(configOptions *ConfigOptions, circDependency *CircularDependency) {
	updatedDependencyList := false

	// The original's comment: some topologies can result in a massive number of
	// cycles. We'll cut it off.
	if len(s.writableData.CircularDependencies) < maxImportCyclesPerFile {
		found := false
		for _, dep := range s.writableData.CircularDependencies {
			if dep.IsEqual(circDependency) {
				found = true
				break
			}
		}
		if !found {
			s.writableData.CircularDependencies = append(s.writableData.CircularDependencies, circDependency)
			updatedDependencyList = true
		}
	}

	if updatedDependencyList {
		s.recomputeDiagnostics(configOptions)
	}
}

func (s *SourceFile) SetNoCircularDependencyConfirmed() {
	s.writableData.NoCircularDependencyConfirmed = true
}

func (s *SourceFile) IsNoCircularDependencyConfirmed() bool {
	return !s.IsParseRequired() && s.writableData.NoCircularDependencyConfirmed
}

func (s *SourceFile) SetHitMaxImportDepth(maxImportDepth int) {
	s.writableData.HitMaxImportDepth = &maxImportDepth
}

// Parse parses the file and updates the state.
//
// The original's comment: callers should wait for completion (or at least
// cancel) prior to calling again. It returns true if a parse was required and
// false if the parse information was up to date already.
//
// `content` is optional in the original; "" with hasContent false is the
// absence.
func (s *SourceFile) Parse(configOptions *ConfigOptions, importResolver *ImportResolver, content string, hasContent bool) bool {
	logState := s.logTracker.Log("parsing: " + s.getPathForLogging(s.uri).String())
	defer logState.Done()

	// If the file is already parsed, we can skip.
	if !s.IsParseRequired() {
		logState.Suppress()
		return false
	}

	diagSink := common.NewDiagnosticSink()
	fileContents, haveContents := s.GetOpenFileContents()
	if !haveContents {
		read := func() (string, bool) {
			if hasContent {
				return content, true
			}
			return s.getFileContentOk()
		}
		if text, ok := read(); ok {
			fileContents = text
			// Remember the length and hash for comparison purposes.
			asText := common.NewText(fileContents)
			length := asText.Length()
			hash := common.HashText(asText)
			s.writableData.LastFileContentLength = &length
			s.writableData.LastFileContentHash = &hash
		} else {
			diagSink.AddError("Source file could not be read", common.GetEmptyRange())
			fileContents = ""

			if !s.FileSystem.ExistsSync(s.uri) {
				s.writableData.IsFileDeleted = true
			}
		}
	}

	s.parseContents(configOptions, importResolver, fileContents, diagSink)

	s.writableData.AnalyzedFileContentsVersion = s.writableData.FileContentsVersion
	s.writableData.IsBindingNeeded = true
	s.writableData.IsCheckingNeeded = true
	s.writableData.ParseTreeNeedsCleaning = false
	s.writableData.HitMaxImportDepth = nil

	s.recomputeDiagnostics(configOptions)

	return true
}

// parseContents is the body of the original's try block, with the catch as a
// recover. The original swallows the exception rather than rethrowing, with the
// comment that callers are not prepared to handle one.
func (s *SourceFile) parseContents(
	configOptions *ConfigOptions,
	importResolver *ImportResolver,
	fileContents string,
	diagSink *common.DiagnosticSink,
) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}

		message := fmt.Sprint(r)
		s.console.Error(localization.LocMessage.InternalParseError().Format(
			s.GetUri().ToUserVisibleString(), message))

		// Create dummy parse results.
		empty := common.NewText("")
		s.writableData.ParsedFileContents = &empty
		s.writableData.TokenizerLines = common.NewTextRangeCollection([]common.TextRange{})

		s.writableData.ParserOutput = &parser.ParserOutput{
			ParseTree:              parser.NewModuleNode(common.TextRange{Start: 0, Length: 0}),
			ImportedModules:        []*parser.ModuleImport{},
			FutureImports:          map[string]bool{},
			ContainsWildcardImport: false,
			TypingSymbolAliases:    map[string]string{},
			HasTypeAnnotations:     false,
			Lines:                  s.writableData.TokenizerLines,
		}

		s.writableData.TokenizerOutput = &parser.TokenizerOutput{
			Tokens:                          common.NewTextRangeCollection([]parser.Token{}),
			Lines:                           s.writableData.TokenizerLines,
			TypeIgnoreAll:                   nil,
			TypeIgnoreLines:                 map[int]*parser.IgnoreComment{},
			PyrightIgnoreLines:              map[int]*parser.IgnoreComment{},
			PredominantEndOfLineSequence:    "\n",
			HasPredominantTabSequence:       false,
			PredominantTabSequence:          "    ",
			PredominantSingleQuoteCharacter: "'",
		}

		s.writableData.Imports = nil
		s.writableData.HasImports = false
		s.writableData.BuiltinsImport = nil

		errorSink := common.NewDiagnosticSink()
		errorSink.AddError(
			localization.LocMessage.InternalParseError().Format(s.GetUri().ToUserVisibleString(), message),
			common.GetEmptyRange())
		s.writableData.ParseDiagnostics = errorSink.FetchAndClear()
		s.writableData.TaskListDiagnostics = errorSink.FetchAndClear()
	}()

	// Parse the token stream, building the abstract syntax tree.
	parseFileResults := s.parseFile(configOptions, s.uri, fileContents, s.ipythonMode != IPythonModeNone, diagSink)

	common.Assert(parseFileResults != nil && parseFileResults.TokenizerOutput != nil, "")
	s.writableData.ParserOutput = parseFileResults.ParserOutput
	s.writableData.TokenizerLines = parseFileResults.TokenizerOutput.Lines
	text := parseFileResults.Text
	s.writableData.ParsedFileContents = &text
	s.writableData.TypeIgnoreLines = parseFileResults.TokenizerOutput.TypeIgnoreLines
	s.writableData.TypeIgnoreAll = parseFileResults.TokenizerOutput.TypeIgnoreAll
	s.writableData.PyrightIgnoreLines = parseFileResults.TokenizerOutput.PyrightIgnoreLines
	lineCount := parseFileResults.TokenizerOutput.Lines.Count()
	s.writableData.LineCount = &lineCount

	// Cache the tokenizer output only if this file is open.
	if s.writableData.ClientDocumentContents != nil {
		s.writableData.TokenizerOutput = parseFileResults.TokenizerOutput
	}

	// Resolve imports.
	execEnvironment := configOptions.FindExecEnvironment(s.uri)
	importResult := s.resolveImports(importResolver, parseFileResults.ParserOutput.ImportedModules, execEnvironment)

	s.writableData.Imports = importResult.Imports
	s.writableData.HasImports = true
	s.writableData.BuiltinsImport = importResult.BuiltinsImportResult

	s.writableData.ParseDiagnostics = diagSink.FetchAndClear()

	s.writableData.TaskListDiagnostics = []*common.Diagnostic{}
	s.addTaskListDiagnostics(configOptions.TaskListTokens, parseFileResults, &s.writableData.TaskListDiagnostics)

	// Is this file in a "strict" path?
	useStrict := false
	for _, strictFileSpec := range configOptions.Strict {
		if s.uri.MatchesRegex(strictFileSpec.RegExp) {
			useStrict = true
			break
		}
	}

	commentDiags := []CommentDiagnostic{}
	s.diagnosticRuleSet = GetFileLevelDirectives(
		parseFileResults.TokenizerOutput.Tokens,
		parseFileResults.TokenizerOutput.Lines,
		execEnvironment.DiagnosticRuleSet,
		useStrict,
		&commentDiags,
	)

	s.writableData.CommentDiagnostics = []*common.Diagnostic{}
	for _, commentDiag := range commentDiags {
		s.writableData.CommentDiagnostics = append(s.writableData.CommentDiagnostics, common.NewDiagnostic(
			common.DiagnosticCategoryError,
			commentDiag.Message,
			common.ConvertTextRangeToRange(commentDiag.Range, parseFileResults.TokenizerOutput.Lines),
		))
	}
}

// Bind performs name binding.
func (s *SourceFile) Bind(
	configOptions *ConfigOptions,
	importLookup ImportLookup,
	builtinsScope *Scope,
	futureImports *common.OrderedSet[string],
	cellChainIndex CellChainIndexProvider,
) {
	common.Assert(!s.IsParseRequired(), "Bind called before parsing")
	common.Assert(s.IsBindingRequired(), "Bind called unnecessarily")
	common.Assert(!s.writableData.IsBindingInProgress, "Bind called while binding in progress")
	common.Assert(s.writableData.ParserOutput != nil, "Parse results not available")

	logState := s.logTracker.Log("binding: " + s.getPathForLogging(s.uri).String())
	defer logState.Done()

	s.bindContents(configOptions, importLookup, builtinsScope, futureImports, cellChainIndex)

	// Prepare for the next stage of the analysis.
	s.writableData.IsCheckingNeeded = true
	s.writableData.IsBindingNeeded = false

	s.recomputeDiagnostics(configOptions)
}

func (s *SourceFile) bindContents(
	configOptions *ConfigOptions,
	importLookup ImportLookup,
	builtinsScope *Scope,
	futureImports *common.OrderedSet[string],
	cellChainIndex CellChainIndexProvider,
) {
	defer func() {
		s.writableData.IsBindingInProgress = false

		r := recover()
		if r == nil {
			return
		}

		message := fmt.Sprint(r)
		s.console.Error(localization.LocMessage.InternalBindError().Format(
			s.GetUri().ToUserVisibleString(), message))

		diagSink := common.NewDiagnosticSink()
		diagSink.AddError(
			localization.LocMessage.InternalBindError().Format(s.GetUri().ToUserVisibleString(), message),
			common.GetEmptyRange())
		s.writableData.BindDiagnostics = diagSink.FetchAndClear()
	}()

	s.cleanParseTreeIfRequired()

	fileInfo := s.buildFileInfo(configOptions, importLookup, builtinsScope, futureImports)
	SetFileInfo(s.writableData.ParserOutput.ParseTree, fileInfo)

	indexGenerationMode := configOptions.IndexGenerationMode != nil && *configOptions.IndexGenerationMode
	binder := NewBinder(fileInfo, indexGenerationMode, cellChainIndex)
	s.writableData.IsBindingInProgress = true
	binder.BindModule(s.writableData.ParserOutput.ParseTree)

	// The original's comment: if we're in "test mode" (used for unit testing),
	// run an additional "test walker" over the parse tree to validate its
	// internal consistency.
	//
	// TestWalker is analyzer/testWalker.ts, which is harness-only; the port
	// does not have it, and the corpus differentials cover what it checks.
	_ = configOptions.InternalTestMode

	s.writableData.BindDiagnostics = fileInfo.DiagnosticSink.FetchAndClear()
	moduleScope := GetScope(s.writableData.ParserOutput.ParseTree)
	common.Assert(moduleScope != nil, "Module scope not returned by binder")
	s.writableData.ModuleSymbolTable = moduleScope.SymbolTable
}

// Check runs the checker over the bound file.
//
// This is the Stage D seam; see the header. With no checker factory installed
// the reentrancy flags, the timing and the diagnostic bookkeeping still run,
// and checkerDiagnostics stays empty.
func (s *SourceFile) Check(
	configOptions *ConfigOptions,
	importLookup ImportLookup,
	importResolver *ImportResolver,
	evaluator TypeEvaluator,
	dependentFiles []*parser.ParserOutput,
) {
	common.Assert(!s.IsParseRequired(), "Check called before parsing")
	common.Assert(!s.IsBindingRequired(), "Check called before binding")
	common.Assert(!s.writableData.IsBindingInProgress, "Check called while binding in progress")
	common.Assert(!s.writableData.IsCheckingInProgress, "Check called while checking in progress")
	common.Assert(s.IsCheckingRequired(), "Check called unnecessarily")
	common.Assert(s.writableData.ParserOutput != nil, "Parse results not available")

	logState := s.logTracker.Log("checking: " + s.getPathForLogging(s.uri).String())
	defer logState.Done()

	defer func() {
		s.writableData.IsCheckingInProgress = false

		// The original's comment: clear any circular dependencies associated
		// with this file. These will be detected by the program module and
		// associated with the source file right before it is finalized.
		s.writableData.CircularDependencies = []*CircularDependency{}

		s.recomputeDiagnostics(configOptions)
	}()

	checkDuration := common.NewDuration()
	s.writableData.IsCheckingInProgress = true

	if s.checkerFactory != nil {
		checker := s.checkerFactory(importResolver, evaluator, s.writableData.ParserOutput, dependentFiles)
		checker.Check()

		fileInfo := GetFileInfo(s.writableData.ParserOutput.ParseTree)
		s.writableData.CheckerDiagnostics = fileInfo.DiagnosticSink.FetchAndClear()
	}

	s.writableData.IsCheckingNeeded = false
	elapsed := checkDuration.GetDurationInMilliseconds()
	s.writableData.CheckTime = &elapsed
}

func (s *SourceFile) TestEnableIPythonMode(enable bool) {
	if enable {
		s.ipythonMode = IPythonModeCellDocs
	} else {
		s.ipythonMode = IPythonModeNone
	}
}

// makeFileID creates a short string that can be used to uniquely identify this
// file from all other files.
//
// The original's comment: it is used in the type evaluator to distinguish
// between types that are defined in different files or scopes.
func (s *SourceFile) makeFileID(fileUri uri.Uri) string {
	const maxNameLength = 8

	// Use a small portion of the file name to help with debugging.
	fileName := fileUri.FileNameWithoutExtensions()
	if len(fileName) > maxNameLength {
		fileName = fileName[len(fileName)-maxNameLength:]
	}

	// Append a number to guarantee uniqueness.
	uniqueNumber := nextUniqueFileId
	nextUniqueFileId++

	// The original's comment: use a "/" to separate the two components, since
	// this character will never appear in a file name.
	return fmt.Sprintf("%s/%d", fileName, uniqueNumber)
}

func (s *SourceFile) cachePreEditState() {
	// If this is our first write, then make a copy of the writable data.
	if !s.editMode.IsEditMode() || s.preEditData != nil {
		return
	}

	// Copy over the writable data.
	s.preEditData = s.writableData

	// Recreate all the writable data from scratch.
	s.writableData = newSourceFileWritableData()
}

func (s *SourceFile) buildFileInfo(
	configOptions *ConfigOptions,
	importLookup ImportLookup,
	builtinsScope *Scope,
	futureImports *common.OrderedSet[string],
) *AnalyzerFileInfo {
	common.Assert(s.writableData.ParserOutput != nil, "Parse results not available")
	analysisDiagnostics := common.NewTextRangeDiagnosticSink(s.writableData.TokenizerLines)

	// ConfigOptions stores the defined constants as `boolean | string`, which
	// the port spells as `any` there and as DefinedConstantValue here.
	definedConstants := common.NewOrderedMap[string, DefinedConstantValue]()
	configOptions.DefineConstant.ForEach(func(value any, name string) {
		switch v := value.(type) {
		case bool:
			definedConstants.Set(name, DefinedConstantValue{Bool: v})
		case string:
			definedConstants.Set(name, DefinedConstantValue{IsString: true, String: v})
		}
	})

	typingSymbolAliases := common.NewOrderedMap[string, string]()
	for name, alias := range s.writableData.ParserOutput.TypingSymbolAliases {
		typingSymbolAliases.Set(name, alias)
	}

	return &AnalyzerFileInfo{
		ImportLookup:               importLookup,
		FutureImports:              futureImports,
		BuiltinsScope:              builtinsScope,
		DiagnosticSink:             analysisDiagnostics,
		ExecutionEnvironment:       configOptions.FindExecEnvironment(s.uri),
		DiagnosticRuleSet:          s.diagnosticRuleSet,
		Lines:                      s.writableData.TokenizerLines,
		TypingSymbolAliases:        typingSymbolAliases,
		DefinedConstants:           definedConstants,
		FileID:                     s.fileID,
		FileUri:                    s.uri,
		ModuleName:                 s.GetModuleName(),
		IsStubFile:                 s.isStubFile,
		IsTypingStubFile:           s.isTypingStubFile,
		IsTypingExtensionsStubFile: s.isTypingExtensionsStubFile,
		IsTypeshedStubFile:         s.isTypeshedStubFile,
		IsBuiltInStubFile:          s.isBuiltInStubFile,
		IsInPyTypedPackage:         s.isThirdPartyPyTypedPresent,
		IPythonMode:                s.ipythonMode,
		AccessedSymbolSet:          common.NewOrderedSet[int](),
	}
}

func (s *SourceFile) cleanParseTreeIfRequired() {
	if s.writableData.ParserOutput != nil {
		if s.writableData.ParseTreeNeedsCleaning {
			cleanerWalker := NewParseTreeCleanerWalker(s.writableData.ParserOutput.ParseTree)
			cleanerWalker.Clean()
			s.writableData.ParseTreeNeedsCleaning = false
		}
	}
}

func (s *SourceFile) resolveImports(
	importResolver *ImportResolver,
	moduleImports []*parser.ModuleImport,
	execEnv *ExecutionEnvironment,
) resolveImportResult {
	imports := []*ImportResult{}

	resolveAndAddIfNotSelf := func(nameParts []string, skipMissingImport bool) *ImportResult {
		importResult := importResolver.ResolveImport(s.uri, execEnv, ImportedModuleDescriptor{
			LeadingDots:     0,
			NameParts:       nameParts,
			ImportedSymbols: nil,
		})

		if skipMissingImport && !importResult.IsImportFound {
			return nil
		}

		// Avoid importing the module from the module file itself. The original
		// compares with !==, which is reference inequality on interned Uris.
		if len(importResult.ResolvedUris) == 0 || importResult.ResolvedUris[0] != s.uri {
			imports = append(imports, importResult)
			return importResult
		}

		return nil
	}

	// Always include an implicit import of the builtins module.
	var builtinsImportResult *ImportResult

	// The original's comment: if this is a project source file (not a stub),
	// try to resolve the __builtins__ stub first.
	if !s.isThirdPartyImport && !s.isStubFile {
		builtinsImportResult = resolveAndAddIfNotSelf([]string{"__builtins__"}, true)
	}

	if builtinsImportResult == nil {
		builtinsImportResult = resolveAndAddIfNotSelf([]string{"builtins"}, false)
	}

	resolveAndAddIfNotSelf([]string{"_typeshed", "_type_checker_internals"}, true)

	for _, moduleImport := range moduleImports {
		var importedSymbols *common.OrderedSet[string]
		if moduleImport.ImportedSymbols != nil {
			importedSymbols = common.NewOrderedSet[string]()
			for symbol := range moduleImport.ImportedSymbols {
				importedSymbols.Add(symbol)
			}
		}

		importResult := importResolver.ResolveImport(s.uri, execEnv, ImportedModuleDescriptor{
			LeadingDots:     moduleImport.LeadingDots,
			NameParts:       moduleImport.NameParts,
			ImportedSymbols: importedSymbols,
		})

		imports = append(imports, importResult)

		// The original's comment: associate the import results with the module
		// import name node in the parse tree so we can access it later (for
		// hover and definition support).
		if len(moduleImport.NameParts) == len(moduleImport.NameNode.D.NameParts) {
			SetImportInfo(moduleImport.NameNode, importResult)
		} else {
			// The original's comment: for implicit imports of higher-level
			// modules within a multi-part module name, the
			// moduleImport.nameParts will refer to the subset of the multi-part
			// name rather than the full multi-part name. In this case, store
			// the import info on the name part node.
			common.Assert(len(moduleImport.NameParts) > 0, "")
			common.Assert(len(moduleImport.NameParts)-1 < len(moduleImport.NameNode.D.NameParts), "")
			SetImportInfo(moduleImport.NameNode.D.NameParts[len(moduleImport.NameParts)-1], importResult)
		}
	}

	return resolveImportResult{Imports: imports, BuiltinsImportResult: builtinsImportResult}
}

func (s *SourceFile) getPathForLogging(fileUri uri.Uri) uri.Uri {
	return uri.GetPathForLogging(s.FileSystem, fileUri)
}

func (s *SourceFile) parseFile(
	configOptions *ConfigOptions,
	fileUri uri.Uri,
	fileContents string,
	useNotebookMode bool,
	diagSink *common.DiagnosticSink,
) *parser.ParseFileResults {
	// The original's comment: use the configuration options to determine the
	// environment in which this source file will be executed.
	execEnvironment := configOptions.FindExecEnvironment(fileUri)

	parseOptions := parser.NewParseOptions()
	parseOptions.UseNotebookMode = useNotebookMode
	if fileUri.PathEndsWith("pyi") {
		parseOptions.IsStubFile = true
	}
	parseOptions.PythonVersion = execEnvironment.PythonVersion
	parseOptions.SkipFunctionAndClassBody = configOptions.IndexGenerationMode != nil && *configOptions.IndexGenerationMode

	// Parse the token stream, building the abstract syntax tree.
	p := parser.NewParser()
	return p.ParseSourceFile(common.NewText(fileContents), parseOptions, diagSink)
}

func (s *SourceFile) tokenizeContents(fileContents common.Text, contentHash int32) *parser.TokenizerOutput {
	tokenizer := parser.NewTokenizer()
	output := tokenizer.Tokenize(fileContents)

	// The original's comment: when the file is open, cache the tokenizer
	// results. Because the tokenizer is lazy, ensure that the state remains
	// unchanged before caching its output.
	if s.writableData.ClientDocumentContents != nil &&
		s.writableData.LastFileContentHash != nil && *s.writableData.LastFileContentHash == contentHash {
		s.writableData.TokenizerOutput = output

		// The original's comment: replace the existing tokenizerLines with the
		// newly-returned version. They should have the same contents, but we
		// want to use the same object so the older object can be deallocated.
		s.writableData.TokenizerLines = output.Lines
	}

	return output
}

func (s *SourceFile) fireFileDirtyEvent() {
	if s.onFileDirty == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			s.console.Error("State mutation listener exception: " + fmt.Sprint(r))
		}
	}()
	s.onFileDirty(s.uri)
}
