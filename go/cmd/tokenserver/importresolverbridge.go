/*
 * importresolverbridge.go
 *
 * The "importresolver" op, which lets pyright's own importResolver.test.ts run
 * unmodified against the Go port. See tools/ts-bridge/shim-importResolver.ts
 * for the protocol and why it looks like this.
 *
 * Each request carries the whole world the resolver reads -- a file system
 * snapshot, a ConfigOptions, an ExecutionEnvironment and the host's search
 * paths -- because client.ts spawns a fresh process per request and nothing
 * survives between them. The resolver is then built exactly as the test builds
 * it: an in-memory file system, wrapped in a PyrightFileSystem, with a
 * PartialStubService over that.
 */

package main

import (
	"encoding/json"
	"fmt"

	"github.com/microsoft/pyright/go/analyzer"
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/vfs"
)

type bridgeUri struct {
	Empty         bool   `json:"empty"`
	FilePath      string `json:"filePath"`
	CaseSensitive bool   `json:"caseSensitive"`
}

// toUri returns nil for a JSON null, which is the shim's undefined.
func (u *bridgeUri) toUri() uri.Uri {
	if u == nil {
		return nil
	}
	if u.Empty {
		return uri.Empty()
	}
	return uri.UriExFile(u.FilePath, u.CaseSensitive, false)
}

func uriToBridge(u uri.Uri) *bridgeUri {
	if u == nil {
		return nil
	}
	if u.IsEmpty() {
		return &bridgeUri{Empty: true, CaseSensitive: true}
	}
	return &bridgeUri{FilePath: u.GetFilePath(), CaseSensitive: u.IsCaseSensitive()}
}

func urisToBridge(us []uri.Uri) []*bridgeUri {
	out := make([]*bridgeUri, 0, len(us))
	for _, u := range us {
		out = append(out, uriToBridge(u))
	}
	return out
}

type bridgeFileSpec struct {
	WildcardRoot         *bridgeUri `json:"wildcardRoot"`
	Source               string     `json:"source"`
	IgnoreCase           bool       `json:"ignoreCase"`
	HasDirectoryWildcard bool       `json:"hasDirectoryWildcard"`
}

type bridgeConfig struct {
	ProjectRoot           *bridgeUri       `json:"projectRoot"`
	PythonPath            *bridgeUri       `json:"pythonPath"`
	PythonEnvironmentName string           `json:"pythonEnvironmentName"`
	TypeshedPath          *bridgeUri       `json:"typeshedPath"`
	StubPath              *bridgeUri       `json:"stubPath"`
	VenvPath              *bridgeUri       `json:"venvPath"`
	Venv                  string           `json:"venv"`
	DefaultPythonVersion  *string          `json:"defaultPythonVersion"`
	DefaultPythonPlatform string           `json:"defaultPythonPlatform"`
	DefaultExtraPaths     []*bridgeUri     `json:"defaultExtraPaths"`
	SkipNativeLibraries   bool             `json:"skipNativeLibraries"`
	VerboseOutput         bool             `json:"verboseOutput"`
	Include               []bridgeFileSpec `json:"include"`
	Exclude               []bridgeFileSpec `json:"exclude"`
}

type bridgeExecEnv struct {
	Root                *bridgeUri   `json:"root"`
	Name                string       `json:"name"`
	PythonVersion       *string      `json:"pythonVersion"`
	PythonPlatform      string       `json:"pythonPlatform"`
	ExtraPaths          []*bridgeUri `json:"extraPaths"`
	SkipNativeLibraries bool         `json:"skipNativeLibraries"`
}

type bridgeDescriptor struct {
	LeadingDots     int       `json:"leadingDots"`
	NameParts       []string  `json:"nameParts"`
	HasTrailingDot  bool      `json:"hasTrailingDot"`
	ImportedSymbols *[]string `json:"importedSymbols"`
}

type bridgeSearchPaths struct {
	Paths  []*bridgeUri `json:"paths"`
	Prefix *bridgeUri   `json:"prefix"`
}

type importResolverRequest struct {
	Which       string            `json:"which"`
	FS          vfs.Snapshot      `json:"fs"`
	Config      bridgeConfig      `json:"config"`
	ExecEnv     bridgeExecEnv     `json:"execEnv"`
	SearchPaths bridgeSearchPaths `json:"searchPaths"`

	SourceFileUri          *bridgeUri        `json:"sourceFileUri"`
	ModuleDescriptor       *bridgeDescriptor `json:"moduleDescriptor"`
	AllowInvalidModuleName bool              `json:"allowInvalidModuleName"`
	DetectPyTyped          bool              `json:"detectPyTyped"`
	MapCompiled            bool              `json:"mapCompiled"`
	ForLogging             bool              `json:"forLogging"`
}

// bridgeHost stands in for TestAccessHost, whose getPythonSearchPaths ignores
// all three of its arguments. The answer was computed on the Node side; see the
// shim's header for why that is exact here and would not be in general.
type bridgeHost struct {
	analyzer.NoAccessHost
	result analyzer.PythonPathResult
}

func (h *bridgeHost) GetPythonSearchPaths(pythonPath uri.Uri, failureLogger *analyzer.ImportLogger, cwd uri.Uri) analyzer.PythonPathResult {
	return h.result
}

func bridgeFileSpecs(specs []bridgeFileSpec) ([]uri.FileSpec, error) {
	out := make([]uri.FileSpec, 0, len(specs))
	for _, spec := range specs {
		source := spec.Source
		if spec.IgnoreCase {
			source = "(?i)" + source
		}
		compiled, err := common.CompileWildcardRegexPattern(source)
		if err != nil {
			return nil, err
		}
		out = append(out, uri.FileSpec{
			WildcardRoot:         spec.WildcardRoot.toUri(),
			RegExp:               compiled,
			HasDirectoryWildcard: spec.HasDirectoryWildcard,
		})
	}
	return out, nil
}

func bridgeVersion(s *string) *common.PythonVersion {
	if s == nil {
		return nil
	}
	return common.PythonVersionFromString(*s)
}

func bridgeUris(us []*bridgeUri) []uri.Uri {
	out := make([]uri.Uri, 0, len(us))
	for _, u := range us {
		out = append(out, u.toUri())
	}
	return out
}

// resolverBundle is everything one request builds. partialStubs is kept so the
// mappings it installed can be reported back; see handleImportResolver.
type resolverBundle struct {
	resolver     *analyzer.ImportResolver
	execEnv      *analyzer.ExecutionEnvironment
	partialStubs *analyzer.PartialStubService
}

func buildImportResolver(req *importResolverRequest) (*resolverBundle, error) {
	// The same chain the test builds: TestFileSystem -> PyrightFileSystem, with
	// a PartialStubService over the outer one.
	memFS := vfs.New(req.FS)
	fileSystem := analyzer.NewPyrightFileSystem(memFS)
	partialStubs := analyzer.NewPartialStubService(fileSystem)

	include, err := bridgeFileSpecs(req.Config.Include)
	if err != nil {
		return nil, err
	}
	exclude, err := bridgeFileSpecs(req.Config.Exclude)
	if err != nil {
		return nil, err
	}

	configOptions := analyzer.NewConfigOptions(req.Config.ProjectRoot.toUri())
	configOptions.PythonPath = req.Config.PythonPath.toUri()
	configOptions.PythonEnvironmentName = req.Config.PythonEnvironmentName
	configOptions.TypeshedPath = req.Config.TypeshedPath.toUri()
	configOptions.StubPath = req.Config.StubPath.toUri()
	configOptions.VenvPath = req.Config.VenvPath.toUri()
	configOptions.Venv = req.Config.Venv
	configOptions.DefaultPythonVersion = bridgeVersion(req.Config.DefaultPythonVersion)
	configOptions.DefaultPythonPlatform = req.Config.DefaultPythonPlatform
	configOptions.DefaultExtraPaths = bridgeUris(req.Config.DefaultExtraPaths)
	configOptions.SkipNativeLibraries = req.Config.SkipNativeLibraries
	verbose := req.Config.VerboseOutput
	configOptions.VerboseOutput = &verbose
	configOptions.Include = include
	configOptions.Exclude = exclude

	pythonVersion := common.LatestStablePythonVersion
	if v := bridgeVersion(req.ExecEnv.PythonVersion); v != nil {
		pythonVersion = *v
	}
	execEnv := &analyzer.ExecutionEnvironment{
		Root:                req.ExecEnv.Root.toUri(),
		Name:                req.ExecEnv.Name,
		PythonVersion:       pythonVersion,
		PythonPlatform:      req.ExecEnv.PythonPlatform,
		ExtraPaths:          bridgeUris(req.ExecEnv.ExtraPaths),
		DiagnosticRuleSet:   configOptions.DiagnosticRuleSet,
		SkipNativeLibraries: req.ExecEnv.SkipNativeLibraries,
	}

	host := &bridgeHost{result: analyzer.PythonPathResult{
		Paths:  bridgeUris(req.SearchPaths.Paths),
		Prefix: req.SearchPaths.Prefix.toUri(),
	}}

	resolver := analyzer.NewImportResolver(
		fileSystem,
		common.NewNullConsole(),
		partialStubs,
		configOptions,
		host,
		nil, // fileSystemCache: use the default
		nil, // typeshedInfoProvider: use the default
		analyzer.ImportResolverHooks{},
	)

	return &resolverBundle{resolver: resolver, execEnv: execEnv, partialStubs: partialStubs}, nil
}

func bridgeDescriptorToGo(d *bridgeDescriptor) analyzer.ImportedModuleDescriptor {
	descriptor := analyzer.ImportedModuleDescriptor{
		LeadingDots:    d.LeadingDots,
		NameParts:      d.NameParts,
		HasTrailingDot: d.HasTrailingDot,
	}
	if d.ImportedSymbols != nil {
		descriptor.ImportedSymbols = common.NewOrderedSetFrom(*d.ImportedSymbols)
	}
	return descriptor
}

type bridgePyTypedInfo struct {
	PyTypedPath      *bridgeUri `json:"pyTypedPath"`
	IsPartiallyTyped bool       `json:"isPartiallyTyped"`
}

func pyTypedToBridge(info *analyzer.PyTypedInfo) *bridgePyTypedInfo {
	if info == nil {
		return nil
	}
	return &bridgePyTypedInfo{PyTypedPath: uriToBridge(info.PyTypedPath), IsPartiallyTyped: info.IsPartiallyTyped}
}

type bridgeImplicitImport struct {
	IsStubFile  bool               `json:"isStubFile"`
	IsNativeLib bool               `json:"isNativeLib"`
	Name        string             `json:"name"`
	Uri         *bridgeUri         `json:"uri"`
	PyTypedInfo *bridgePyTypedInfo `json:"pyTypedInfo"`
}

// implicitImportsToBridge sends the map as an array, so the Node side can
// rebuild it in the same insertion order.
func implicitImportsToBridge(m *common.OrderedMap[string, *analyzer.ImplicitImport]) []bridgeImplicitImport {
	if m == nil {
		return nil
	}
	out := make([]bridgeImplicitImport, 0, m.Size())
	m.ForEach(func(value *analyzer.ImplicitImport, _ string) {
		out = append(out, bridgeImplicitImport{
			IsStubFile:  value.IsStubFile,
			IsNativeLib: value.IsNativeLib,
			Name:        value.Name,
			Uri:         uriToBridge(value.Uri),
			PyTypedInfo: pyTypedToBridge(value.PyTypedInfo),
		})
	})
	return out
}

type bridgeImportResult struct {
	ImportName               string                 `json:"importName"`
	IsRelative               bool                   `json:"isRelative"`
	IsImportFound            bool                   `json:"isImportFound"`
	IsPartlyResolved         bool                   `json:"isPartlyResolved"`
	IsNamespacePackage       bool                   `json:"isNamespacePackage"`
	IsInitFilePresent        bool                   `json:"isInitFilePresent"`
	IsStubPackage            bool                   `json:"isStubPackage"`
	ImportFailureInfo        []string               `json:"importFailureInfo"`
	ImportType               int                    `json:"importType"`
	ResolvedUris             []*bridgeUri           `json:"resolvedUris"`
	SearchPath               *bridgeUri             `json:"searchPath"`
	IsStubFile               bool                   `json:"isStubFile"`
	IsNativeLib              bool                   `json:"isNativeLib"`
	IsStdlibTypeshedFile     bool                   `json:"isStdlibTypeshedFile"`
	IsThirdPartyTypeshedFile bool                   `json:"isThirdPartyTypeshedFile"`
	IsLocalTypingsFile       bool                   `json:"isLocalTypingsFile"`
	ImplicitImports          []bridgeImplicitImport `json:"implicitImports"`
	FilteredImplicitImports  []bridgeImplicitImport `json:"filteredImplicitImports"`
	NonStubImportResult      *bridgeImportResult    `json:"nonStubImportResult"`
	PyTypedInfo              *bridgePyTypedInfo     `json:"pyTypedInfo"`
	PackageDirectory         *bridgeUri             `json:"packageDirectory"`
}

func importResultToBridge(result *analyzer.ImportResult) *bridgeImportResult {
	if result == nil {
		return nil
	}
	return &bridgeImportResult{
		ImportName:               result.ImportName,
		IsRelative:               result.IsRelative,
		IsImportFound:            result.IsImportFound,
		IsPartlyResolved:         result.IsPartlyResolved,
		IsNamespacePackage:       result.IsNamespacePackage,
		IsInitFilePresent:        result.IsInitFilePresent,
		IsStubPackage:            result.IsStubPackage,
		ImportFailureInfo:        result.ImportFailureInfo,
		ImportType:               int(result.ImportType),
		ResolvedUris:             urisToBridge(result.ResolvedUris),
		SearchPath:               uriToBridge(result.SearchPath),
		IsStubFile:               result.IsStubFile,
		IsNativeLib:              result.IsNativeLib,
		IsStdlibTypeshedFile:     result.IsStdlibTypeshedFile,
		IsThirdPartyTypeshedFile: result.IsThirdPartyTypeshedFile,
		IsLocalTypingsFile:       result.IsLocalTypingsFile,
		ImplicitImports:          implicitImportsToBridge(result.ImplicitImports),
		FilteredImplicitImports:  implicitImportsToBridge(result.FilteredImplicitImports),
		NonStubImportResult:      importResultToBridge(result.NonStubImportResult),
		PyTypedInfo:              pyTypedToBridge(result.PyTypedInfo),
		PackageDirectory:         uriToBridge(result.PackageDirectory),
	}
}

func handleImportResolver(payload json.RawMessage) (result any, errMsg string) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			errMsg = fmt.Sprint(r)
		}
	}()

	var req importResolverRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, "importresolver: " + err.Error()
	}

	bundle, err := buildImportResolver(&req)
	if err != nil {
		return nil, "importresolver: " + err.Error()
	}
	resolver, execEnv := bundle.resolver, bundle.execEnv

	var value any
	switch req.Which {
	case "resolveImport":
		value = importResultToBridge(resolver.ResolveImport(
			req.SourceFileUri.toUri(),
			execEnv,
			bridgeDescriptorToGo(req.ModuleDescriptor),
		))

	case "getModuleNameForImport":
		info := resolver.GetModuleNameForImport(
			req.SourceFileUri.toUri(),
			execEnv,
			req.AllowInvalidModuleName,
			req.DetectPyTyped,
		)
		value = map[string]any{
			"moduleName":                 info.ModuleName,
			"importType":                 int(info.ImportType),
			"isTypeshedFile":             info.IsTypeshedFile,
			"isLocalTypingsFile":         info.IsLocalTypingsFile,
			"isThirdPartyPyTypedPresent": info.IsThirdPartyPyTypedPresent,
		}

	case "getSourceFilesFromStub":
		value = urisToBridge(resolver.GetSourceFilesFromStub(req.SourceFileUri.toUri(), execEnv, req.MapCompiled))

	case "getImportRoots":
		value = urisToBridge(resolver.GetImportRoots(execEnv, req.ForLogging))

	default:
		return nil, "importresolver: unknown function " + req.Which
	}

	// Report the partial-stub merges this resolution installed.
	//
	// ensurePartialStubPackages mutates the file system it is given, and one
	// test reads the result back through its *own* file system: after resolving
	// "myLib.partialStub" it expects reading myLib/partialStub.pyi to answer
	// the contents of myLib-stubs/partialStub.pyi. Because the Go resolver
	// works on a snapshot, that side effect has to be carried back. What
	// crosses is the decision -- which stub directory gets merged onto which
	// package, which is what processPartialStubPackages exists to work out --
	// and the Node side replays it with mapDirectory.
	mappings := []map[string]any{}
	for _, mapping := range bundle.partialStubs.MappedDirectories() {
		mappings = append(mappings, map[string]any{
			"mappedUri":   uriToBridge(mapping.MappedUri),
			"originalUri": uriToBridge(mapping.OriginalUri),
		})
	}

	return map[string]any{"value": value, "partialStubMappings": mappings}, ""
}
