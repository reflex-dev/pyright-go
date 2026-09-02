/*
 * configbridge.go
 *
 * The "config" op: builds an AnalyzerService over a project directory and dumps
 * the resulting ConfigOptions and file list, so compare-config.js can diff it
 * against the same thing produced by the TypeScript.
 *
 * The dump has to be field for field what tools/ts-bridge/dump-config.ts
 * produces, including the 96-field diagnostic rule set, which is emitted with
 * the original's camelCase names rather than the Go field names.
 */

package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/microsoft/pyright/go/analyzer"
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/realfs"
)

type configRequest struct {
	ProjectRoot        string `json:"projectRoot"`
	FromLanguageServer bool   `json:"fromLanguageServer"`
}

// configUri renders a Uri the way dump-config.ts does: null for absent, the
// empty string for Uri.empty(), the file path otherwise.
func configUri(u uri.Uri) any {
	if u == nil {
		return nil
	}
	if u.IsEmpty() {
		return ""
	}
	return u.GetFilePath()
}

// ruleSetToJson emits the DiagnosticRuleSet with the original's field names.
//
// The Go struct is generated from the TypeScript interface with the field order
// and comments preserved, so the names differ only in their first letter --
// which is what the lowercasing below undoes. Doing it reflectively rather than
// by hand keeps this from drifting when the rule set grows.
func ruleSetToJson(ruleSet *analyzer.DiagnosticRuleSet) map[string]any {
	out := map[string]any{}
	value := reflect.ValueOf(*ruleSet)
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		out[strings.ToLower(name[:1])+name[1:]] = value.Field(i).Interface()
	}
	return out
}

func fileSpecsToJson(specs []uri.FileSpec) []any {
	out := []any{}
	for _, spec := range specs {
		// The Go pattern is what CompileWildcardRegexPattern produced, which
		// may carry a leading "(?i)" where the TypeScript carries an 'i' flag,
		// and may have dropped a no-op backslash before a non-ASCII rune. The
		// source is reported without the inline flag and the flag separately,
		// so the two line up.
		source := spec.RegExp.String()
		flags := ""
		if strings.HasPrefix(source, "(?i)") {
			source = source[len("(?i)"):]
			flags = "i"
		}
		out = append(out, map[string]any{
			"wildcardRoot":         configUri(spec.WildcardRoot),
			"source":               source,
			"flags":                flags,
			"hasDirectoryWildcard": spec.HasDirectoryWildcard,
		})
	}
	return out
}

func execEnvToJson(env *analyzer.ExecutionEnvironment) map[string]any {
	extraPaths := []any{}
	for _, p := range env.ExtraPaths {
		extraPaths = append(extraPaths, configUri(p))
	}
	return map[string]any{
		"name":                env.Name,
		"root":                configUri(env.Root),
		"pythonVersion":       env.PythonVersion.String(),
		"pythonPlatform":      env.PythonPlatform,
		"extraPaths":          extraPaths,
		"skipNativeLibraries": env.SkipNativeLibraries,
		"diagnosticRuleSet":   ruleSetToJson(env.DiagnosticRuleSet),
	}
}

func boolPtrToJson(v *bool) any {
	if v == nil {
		return nil
	}
	return *v
}

func versionPtrToJson(v *common.PythonVersion) any {
	if v == nil {
		return nil
	}
	return v.String()
}

func configOptionsToJson(c *analyzer.ConfigOptions) map[string]any {
	execEnvs := []any{}
	for _, env := range c.ExecutionEnvironments {
		execEnvs = append(execEnvs, execEnvToJson(env))
	}

	// `Array.from(map.entries())` is an array of [key, value] pairs.
	defineConstant := []any{}
	c.DefineConstant.ForEach(func(value any, key string) {
		defineConstant = append(defineConstant, []any{key, value})
	})

	defaultExtraPaths := []any{}
	for _, p := range c.DefaultExtraPaths {
		defaultExtraPaths = append(defaultExtraPaths, configUri(p))
	}

	return map[string]any{
		"projectRoot":                 configUri(c.ProjectRoot),
		"pythonPath":                  configUri(c.PythonPath),
		"pythonEnvironmentName":       c.PythonEnvironmentName,
		"typeshedPath":                configUri(c.TypeshedPath),
		"stubPath":                    configUri(c.StubPath),
		"venvPath":                    configUri(c.VenvPath),
		"venv":                        c.Venv,
		"include":                     fileSpecsToJson(c.Include),
		"exclude":                     fileSpecsToJson(c.Exclude),
		"ignore":                      fileSpecsToJson(c.Ignore),
		"strict":                      fileSpecsToJson(c.Strict),
		"autoExcludeVenv":             boolPtrToJson(c.AutoExcludeVenv),
		"defineConstant":              defineConstant,
		"verboseOutput":               boolPtrToJson(c.VerboseOutput),
		"checkOnlyOpenFiles":          boolPtrToJson(c.CheckOnlyOpenFiles),
		"useLibraryCodeForTypes":      boolPtrToJson(c.UseLibraryCodeForTypes),
		"autoImportCompletions":       c.AutoImportCompletions,
		"indexing":                    c.Indexing,
		"logTypeEvaluationTime":       c.LogTypeEvaluationTime,
		"typeEvaluationTimeThreshold": c.TypeEvaluationTimeThreshold,
		"initializedFromJson":         c.InitializedFromJson,
		"disableTaggedHints":          c.DisableTaggedHints,
		"diagnosticRuleSet":           ruleSetToJson(c.DiagnosticRuleSet),
		"executionEnvironments":       execEnvs,
		"defaultPythonVersion":        versionPtrToJson(c.DefaultPythonVersion),
		"defaultPythonPlatform":       c.DefaultPythonPlatform,
		"defaultExtraPaths":           defaultExtraPaths,
		"skipNativeLibraries":         c.SkipNativeLibraries,
		"functionSignatureDisplay":    c.FunctionSignatureDisplay,
		"configFileSource":            configUri(c.ConfigFileSource),
		"effectiveTypeCheckingMode":   c.EffectiveTypeCheckingMode,
	}
}

func handleConfig(payload json.RawMessage) (result any, errMsg string) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			errMsg = fmt.Sprint(r)
		}
	}()

	var req configRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, "config: " + err.Error()
	}

	// TestAccessHost, which the TypeScript side uses, is a NoAccessHost with an
	// overridden getPythonSearchPaths that answers an empty list here -- the
	// oracle constructs it with no search paths.
	fileSystem := realfs.New(uri.Empty(), true)

	service := analyzer.NewAnalyzerService("<default>", analyzer.AnalyzerServiceOptions{
		Console:     common.NewNullConsole(),
		FileSystem:  fileSystem,
		HostFactory: func() analyzer.Host { return analyzer.NewNoAccessHost() },
	})

	commandLineOptions := analyzer.NewCommandLineOptions(req.ProjectRoot, nil, true, req.FromLanguageServer)
	service.SetOptions(commandLineOptions)

	configOptions := service.TestGetConfigOptions(commandLineOptions)

	files := []string{}
	for _, u := range service.TestGetFileNamesFromFileSpecs() {
		files = append(files, u.GetFilePath())
	}
	sort.Strings(files)

	service.Dispose()

	return map[string]any{
		"config": configOptionsToJson(configOptions),
		"files":  files,
	}, ""
}
