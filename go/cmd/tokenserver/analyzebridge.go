/*
 * analyzebridge.go
 *
 * The "analyze" op: the evaluator gate (make bridge-evaluator-tests).
 *
 * tests/testUtils.ts funnels every one of the 1,279 test cases in
 * typeEvaluator1-8.test.ts and checker.test.ts through two functions --
 * typeAnalyzeSampleFiles and validateResults -- and the tests assert on nothing
 * but the six diagnostic lists the first of those returns. So the whole suite
 * can be pointed at this port by aliasing that one module; the .test.ts files
 * themselves stay byte for byte the originals, and validateResults stays the
 * original code as well.
 *
 * This op is the Go half: build a Program the way typeAnalyzeSampleFiles does,
 * analyze to completion, and hand back the diagnostics.
 *
 * See tools/ts-bridge/shim-evaluatorTestUtils.ts for the other half and for the
 * completeness guard on the config wire format.
 */

package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/microsoft/pyright/go/analyzer"
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/realfs"
)

type analyzeRequest struct {
	// RootDirectory is what the original sets as `global.__rootDirectory`, and
	// is the directory the bundled typeshed-fallback sits in. The TypeScript
	// takes it from process.cwd(); here it is passed explicitly because the Go
	// binary's own location has nothing to do with the reference tree.
	RootDirectory string            `json:"rootDirectory"`
	FileNames     []string          `json:"fileNames"`
	Config        analyzeConfigJSON `json:"config"`
}

// analyzeConfigJSON carries the parts of ConfigOptions the tests can reach.
//
// Every one of the 1,385 typeAnalyzeSampleFiles calls starts from
// `new ConfigOptions(Uri.empty())` and then sets some combination of
// defaultPythonVersion, defaultPythonPlatform, defineConstant and
// diagnosticRuleSet -- nothing else. Rather than trust that survey, the shim
// reconstructs a ConfigOptions from this payload on the TypeScript side and
// deep-compares it against the one the test built; a test that sets anything
// not carried here fails the harness instead of being silently ignored.
type analyzeConfigJSON struct {
	DefaultPythonVersion  *string        `json:"defaultPythonVersion"`
	DefaultPythonPlatform *string        `json:"defaultPythonPlatform"`
	DefineConstant        [][]any        `json:"defineConstant"`
	DiagnosticRuleSet     map[string]any `json:"diagnosticRuleSet"`
}

type analyzePositionJSON struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type analyzeRangeJSON struct {
	Start analyzePositionJSON `json:"start"`
	End   analyzePositionJSON `json:"end"`
}

type analyzeDiagnosticJSON struct {
	Category int              `json:"category"`
	Message  string           `json:"message"`
	Range    analyzeRangeJSON `json:"range"`
	Rule     *string          `json:"rule"`
}

type analyzeFileResultJSON struct {
	FileUri string `json:"fileUri"`
	// The original returns `parseResults: ParseFileResults | undefined`. Only
	// one assertion in the whole suite looks at it, and only for
	// undefined-ness, so a flag carries what can be carried; see the shim.
	HasParseResults bool `json:"hasParseResults"`

	Errors           []analyzeDiagnosticJSON `json:"errors"`
	Warnings         []analyzeDiagnosticJSON `json:"warnings"`
	Infos            []analyzeDiagnosticJSON `json:"infos"`
	UnusedCodes      []analyzeDiagnosticJSON `json:"unusedCodes"`
	UnreachableCodes []analyzeDiagnosticJSON `json:"unreachableCodes"`
	Deprecateds      []analyzeDiagnosticJSON `json:"deprecateds"`
}

func analyzeRangeToJson(r common.Range) analyzeRangeJSON {
	return analyzeRangeJSON{
		Start: analyzePositionJSON{Line: r.Start.Line, Character: r.Start.Character},
		End:   analyzePositionJSON{Line: r.End.Line, Character: r.End.Character},
	}
}

func analyzeDiagnosticToJson(diag *common.Diagnostic) analyzeDiagnosticJSON {
	return analyzeDiagnosticJSON{
		Category: int(diag.Category),
		Message:  diag.Message,
		Range:    analyzeRangeToJson(diag.Range),
		Rule:     diag.GetRule(),
	}
}

// applyRuleSetJson is the inverse of configbridge.go's ruleSetToJson: it takes
// the camelCase names the TypeScript uses and assigns them by reflection, so
// neither direction has to be maintained by hand as the rule set grows.
func applyRuleSetJson(ruleSet *analyzer.DiagnosticRuleSet, values map[string]any) error {
	value := reflect.ValueOf(ruleSet).Elem()
	typ := value.Type()

	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		jsonName := strings.ToLower(name[:1]) + name[1:]
		raw, ok := values[jsonName]
		if !ok {
			return fmt.Errorf("diagnosticRuleSet is missing %q", jsonName)
		}
		seen[jsonName] = true

		field := value.Field(i)
		switch field.Kind() {
		case reflect.Bool:
			b, ok := raw.(bool)
			if !ok {
				return fmt.Errorf("diagnosticRuleSet.%s is not a boolean", jsonName)
			}
			field.SetBool(b)
		case reflect.String:
			s, ok := raw.(string)
			if !ok {
				return fmt.Errorf("diagnosticRuleSet.%s is not a string", jsonName)
			}
			field.SetString(s)
		default:
			return fmt.Errorf("diagnosticRuleSet.%s has unexpected kind %s", jsonName, field.Kind())
		}
	}

	// A name the Go rule set does not have would otherwise be dropped in
	// silence, which is exactly the failure this gate exists to catch.
	for name := range values {
		if !seen[name] {
			return fmt.Errorf("diagnosticRuleSet has an unknown field %q", name)
		}
	}

	return nil
}

func buildAnalyzeConfigOptions(req *analyzeRequest) (*analyzer.ConfigOptions, error) {
	configOptions := analyzer.NewConfigOptions(uri.Empty())

	if req.Config.DefaultPythonVersion != nil {
		version := common.PythonVersionFromString(*req.Config.DefaultPythonVersion)
		if version == nil {
			return nil, fmt.Errorf("unrecognized pythonVersion %q", *req.Config.DefaultPythonVersion)
		}
		configOptions.DefaultPythonVersion = version
	} else {
		configOptions.DefaultPythonVersion = nil
	}

	if req.Config.DefaultPythonPlatform != nil {
		configOptions.DefaultPythonPlatform = analyzer.PythonPlatform(*req.Config.DefaultPythonPlatform)
	} else {
		configOptions.DefaultPythonPlatform = ""
	}

	// `Array.from(map.entries())`, in the original's insertion order -- which is
	// observable, because the binder reads these in order.
	configOptions.DefineConstant = common.NewOrderedMap[string, any]()
	for _, entry := range req.Config.DefineConstant {
		if len(entry) != 2 {
			return nil, fmt.Errorf("defineConstant entry is not a pair")
		}
		key, ok := entry[0].(string)
		if !ok {
			return nil, fmt.Errorf("defineConstant key is not a string")
		}
		configOptions.DefineConstant.Set(key, entry[1])
	}

	ruleSet := *configOptions.DiagnosticRuleSet
	if err := applyRuleSetJson(&ruleSet, req.Config.DiagnosticRuleSet); err != nil {
		return nil, err
	}
	configOptions.DiagnosticRuleSet = &ruleSet

	// typeAnalyzeSampleFiles always enables test mode; so does getAnalysisResults.
	internalTestMode := true
	configOptions.InternalTestMode = &internalTestMode

	return configOptions, nil
}

func handleAnalyze(payload json.RawMessage) (result any, errMsg string) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			errMsg = fmt.Sprint(r)
		}
	}()

	var req analyzeRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, "analyze: " + err.Error()
	}

	configOptions, err := buildAnalyzeConfigOptions(&req)
	if err != nil {
		return nil, "analyze: " + err.Error()
	}

	// The original uses createFromRealFileSystem, whose module path comes from
	// `global.__rootDirectory`; that is what locates typeshed-fallback.
	fileSystem := realfs.New(uri.UriExFile(req.RootDirectory, true, false), true)
	console := common.NewNullConsole()

	// The original builds a FullAccessHost, which shells out to a Python
	// interpreter for search paths. Nothing under tests/samples imports from
	// site-packages, so this port uses a NoAccessHost and the difference does
	// not reach the results; a deliberate divergence.
	host := analyzer.NewNoAccessHost()
	importResolver := analyzer.CreateImportResolver(fileSystem, console, configOptions, host)

	program := analyzer.NewProgram(importResolver, configOptions, console, nil, nil, false, "")
	installStageDFactories(program)

	fileUris := make([]uri.Uri, 0, len(req.FileNames))
	for _, name := range req.FileNames {
		// `fileNames.map((name) => UriEx.file(resolveSampleFilePath(name)))`;
		// the shim has already resolved the sample path.
		fileUris = append(fileUris, uri.UriExFile(name, true, false))
	}
	program.SetTrackedFiles(fileUris)

	// `while (program.analyze()) {}` -- no timeout, so it completes in one pass.
	for program.Analyze(nil) {
	}

	results := make([]analyzeFileResultJSON, 0, len(fileUris))
	for _, fileUri := range fileUris {
		sourceFile := program.GetSourceFile(fileUri)
		if sourceFile == nil {
			return nil, "analyze: source file not found for " + fileUri.GetFilePath()
		}

		diagnostics, _ := sourceFile.GetDiagnostics(configOptions, nil)
		fileResult := analyzeFileResultJSON{
			FileUri:          fileUri.GetFilePath(),
			HasParseResults:  sourceFile.GetParseResults() != nil,
			Errors:           []analyzeDiagnosticJSON{},
			Warnings:         []analyzeDiagnosticJSON{},
			Infos:            []analyzeDiagnosticJSON{},
			UnusedCodes:      []analyzeDiagnosticJSON{},
			UnreachableCodes: []analyzeDiagnosticJSON{},
			Deprecateds:      []analyzeDiagnosticJSON{},
		}

		for _, diag := range diagnostics {
			entry := analyzeDiagnosticToJson(diag)
			switch diag.Category {
			case common.DiagnosticCategoryError:
				fileResult.Errors = append(fileResult.Errors, entry)
			case common.DiagnosticCategoryWarning:
				fileResult.Warnings = append(fileResult.Warnings, entry)
			case common.DiagnosticCategoryInformation:
				fileResult.Infos = append(fileResult.Infos, entry)
			case common.DiagnosticCategoryUnusedCode:
				fileResult.UnusedCodes = append(fileResult.UnusedCodes, entry)
			case common.DiagnosticCategoryUnreachableCode:
				fileResult.UnreachableCodes = append(fileResult.UnreachableCodes, entry)
			case common.DiagnosticCategoryDeprecated:
				fileResult.Deprecateds = append(fileResult.Deprecateds, entry)
			}
		}

		results = append(results, fileResult)
	}

	unported := evaluatorUnportedCounts(program)

	program.Dispose()

	return map[string]any{"results": results, "unported": unported}, ""
}
