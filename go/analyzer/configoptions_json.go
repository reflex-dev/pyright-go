/*
 * configoptions_json.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * initializeFromJson, setupExecutionEnvironments and their helpers, from
 * common/configOptions.ts (pyright 1.1.412). The rest of the class is in
 * configoptions_class.go.
 *
 * The config object arrives as whatever the JSON or TOML reader produced, so
 * every read is `configObj.x !== undefined` followed by a `typeof` check and an
 * error message. That shape carries over directly: the Go value is `any`, and
 * the helpers below are the typeof tests. Getting a *wrong* type is not an
 * error that stops the load -- the field is skipped and the message logged --
 * so each check has to be at the same place with the same message.
 *
 * A note on the value model. encoding/json produces map[string]any, []any,
 * string, float64, bool and nil; smol-toml and jsonc-parser produce the same
 * JavaScript shapes. So `typeof x === 'number'` is a float64 assertion here,
 * and an integer setting has to be converted rather than asserted -- see
 * jsonNumber.
 */

package analyzer

import (
	"sort"
	"strconv"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// jsonObject is `configObj` after the original's
// `configObj && typeof configObj === 'object'` guard.
func jsonObject(value any) (map[string]any, bool) {
	obj, ok := value.(map[string]any)
	return obj, ok
}

// jsonDefined stands in for `configObj.x !== undefined`. A JSON null is *not*
// undefined in JavaScript, so an explicit null reaches the typeof check and is
// reported as the wrong type -- which is what happens here too.
func jsonDefined(obj map[string]any, key string) (any, bool) {
	value, ok := obj[key]
	return value, ok
}

func jsonString(value any) (string, bool) {
	s, ok := value.(string)
	return s, ok
}

func jsonBool(value any) (bool, bool) {
	b, ok := value.(bool)
	return b, ok
}

func jsonArray(value any) ([]any, bool) {
	a, ok := value.([]any)
	return a, ok
}

// jsonNumber is `typeof x === 'number'`. Both readers produce a float64 for
// every numeric literal, so an integer setting converts rather than asserts.
func jsonNumber(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int64:
		// smol-toml distinguishes integers; go-toml/v2 answers int64 for them.
		return float64(v), true
	}
	return 0, false
}

// isAbsolutePath is Node's `path.isAbsolute`, which on POSIX is a leading
// slash. It gates the "not relative" complaints below.
func isAbsolutePath(p string) bool {
	return len(p) > 0 && p[0] == '/'
}

// InitializeFromJson initializes the structure from a JSON object.
func (c *ConfigOptions) InitializeFromJson(configObjValue any, configDirUri uri.Uri, console common.ConsoleInterface) {
	c.InitializedFromJson = true
	if console == nil {
		console = common.NewNullConsole()
	}

	configObj, isObject := jsonObject(configObjValue)
	if !isObject {
		configObj = map[string]any{}
	}

	unusedConfigKeys := common.NewOrderedSet[string]()
	for _, key := range sortedKeys(configObj) {
		unusedConfigKeys.Add(key)
	}

	// readFileSpecs is the shape the "include", "exclude", "ignore" and
	// "strict" entries share. allowAbsolute is true only for "ignore"; the
	// original's comment there: we'll allow absolute paths in the ignore list.
	// While it is not recommended to use absolute paths anywhere in the config
	// file, there are a few legit use cases for ignore paths when the conf file
	// is used with a language server.
	readFileSpecs := func(key string, allowAbsolute bool, target *[]uri.FileSpec) {
		value, ok := jsonDefined(configObj, key)
		if !ok {
			return
		}
		unusedConfigKeys.Delete(key)

		filesList, ok := jsonArray(value)
		if !ok {
			console.Error(`Config "` + key + `" entry must contain an array.`)
			return
		}

		*target = []uri.FileSpec{}
		for index, entry := range filesList {
			fileSpec, ok := jsonString(entry)
			if !ok {
				console.Error("Index " + strconv.Itoa(index) + ` of "` + key + `" array should be a string.`)
			} else if !allowAbsolute && isAbsolutePath(fileSpec) {
				console.Error(`Ignoring path "` + fileSpec + `" in "` + key + `" array because it is not relative.`)
			} else {
				*target = append(*target, uri.GetFileSpec(configDirUri, fileSpec))
			}
		}
	}

	readFileSpecs("include", false, &c.Include)
	readFileSpecs("exclude", false, &c.Exclude)
	readFileSpecs("ignore", true, &c.Ignore)
	readFileSpecs("strict", false, &c.Strict)

	// If there is a "typeCheckingMode", it can override the provided setting.
	if value, ok := jsonDefined(configObj, "typeCheckingMode"); ok {
		unusedConfigKeys.Delete("typeCheckingMode")
		mode, _ := jsonString(value)
		switch mode {
		case TypeCheckingModeOff, TypeCheckingModeBasic, TypeCheckingModeStandard, TypeCheckingModeStrict:
			c.InitializeTypeCheckingMode(mode, nil)
		default:
			console.Error(`Config "typeCheckingMode" entry must contain "off", "basic", "standard", or "strict".`)
		}
	}

	// The original reads useLibraryCodeForTypes twice, here and again further
	// down, with different messages. Both are reproduced in order.
	if value, ok := jsonDefined(configObj, "useLibraryCodeForTypes"); ok {
		unusedConfigKeys.Delete("useLibraryCodeForTypes")
		if b, ok := jsonBool(value); ok {
			c.UseLibraryCodeForTypes = &b
		} else {
			console.Error(`Config "useLibraryCodeForTypes" entry must be true or false.`)
		}
	}

	// Apply overrides from the config file for the boolean rules.
	configRuleSet := CloneDiagnosticRuleSet(c.DiagnosticRuleSet)
	for _, ruleName := range GetBooleanDiagnosticRules(true) {
		unusedConfigKeys.Delete(ruleName)
		if field := diagnosticRuleBoolFields[ruleName]; field != nil {
			target := field(configRuleSet)
			*target = convertBoolean(console, configObj[ruleName], ruleName, *target)
		}
	}

	// Apply overrides from the config file for the diagnostic level rules.
	for _, ruleName := range GetDiagLevelDiagnosticRules() {
		unusedConfigKeys.Delete(ruleName)
		if field := diagnosticRuleLevelFields[ruleName]; field != nil {
			target := field(configRuleSet)
			*target = convertDiagnosticLevel(console, configObj[ruleName], ruleName, *target)
		}
	}
	c.DiagnosticRuleSet = CloneDiagnosticRuleSet(configRuleSet)

	// readPath is the shape the single-path settings share.
	readPath := func(key string, message string, target *uri.Uri) {
		value, ok := jsonDefined(configObj, key)
		if !ok {
			return
		}
		unusedConfigKeys.Delete(key)
		s, ok := jsonString(value)
		if !ok {
			console.Error(message)
			return
		}
		*target = configDirUri.ResolvePaths(s)
	}

	readPath("venvPath", `Config "venvPath" field must contain a string.`, &c.VenvPath)

	// Read the "venv" name.
	if value, ok := jsonDefined(configObj, "venv"); ok {
		unusedConfigKeys.Delete("venv")
		if s, ok := jsonString(value); ok {
			c.Venv = s
		} else {
			console.Error(`Config "venv" field must contain a string.`)
		}
	}

	// Read the config "extraPaths".
	if value, ok := jsonDefined(configObj, "extraPaths"); ok {
		unusedConfigKeys.Delete("extraPaths")
		pathList, ok := jsonArray(value)
		if !ok {
			console.Error(`Config "extraPaths" field must contain an array.`)
		} else {
			configExtraPaths := []uri.Uri{}
			for pathIndex, entry := range pathList {
				path, ok := jsonString(entry)
				if !ok {
					console.Error(`Config "extraPaths" field ` + strconv.Itoa(pathIndex) + " must be a string.")
				} else {
					configExtraPaths = append(configExtraPaths, configDirUri.ResolvePaths(path))
				}
			}
			c.DefaultExtraPaths = append([]uri.Uri{}, configExtraPaths...)
		}
	}

	// Read the default "pythonVersion".
	if value, ok := jsonDefined(configObj, "pythonVersion"); ok {
		unusedConfigKeys.Delete("pythonVersion")
		if s, ok := jsonString(value); ok {
			if version := common.PythonVersionFromString(s); version != nil {
				c.DefaultPythonVersion = version
			} else {
				console.Error(`Config "pythonVersion" field contains unsupported version.`)
			}
		} else {
			console.Error(`Config "pythonVersion" field must contain a string.`)
		}
	}

	// Read the default "pythonPlatform".
	if value, ok := jsonDefined(configObj, "pythonPlatform"); ok {
		unusedConfigKeys.Delete("pythonPlatform")
		if s, ok := jsonString(value); ok {
			c.DefaultPythonPlatform = s
		} else {
			console.Error(`Config "pythonPlatform" field must contain a string.`)
		}
	}

	// The original's comment: read the skipNativeLibraries flag. This isn't
	// officially documented or supported. It was added specifically to improve
	// initialization performance for playgrounds or web-based environments
	// where native libraries will not be present.
	if value, ok := jsonDefined(configObj, "skipNativeLibraries"); ok {
		unusedConfigKeys.Delete("skipNativeLibraries")
		if b, ok := jsonBool(value); ok {
			c.SkipNativeLibraries = b
		} else {
			console.Error(`Config "skipNativeLibraries" field must contain a boolean.`)
		}
	}

	// Read the "typeshedPath" setting.
	if value, ok := jsonDefined(configObj, "typeshedPath"); ok {
		unusedConfigKeys.Delete("typeshedPath")
		if s, ok := jsonString(value); ok {
			// The original's `configObj.typeshedPath ? ... : undefined` clears
			// the setting for an empty string rather than resolving it.
			if s != "" {
				c.TypeshedPath = configDirUri.ResolvePaths(s)
			} else {
				c.TypeshedPath = nil
			}
		} else {
			console.Error(`Config "typeshedPath" field must contain a string.`)
		}
	}

	// Read the "stubPath" setting. typingsPath is kept for backward
	// compatibility.
	if value, ok := jsonDefined(configObj, "typingsPath"); ok {
		unusedConfigKeys.Delete("typingsPath")
		if s, ok := jsonString(value); ok {
			console.Error(`Config "typingsPath" is now deprecated. Please, use stubPath instead.`)
			c.StubPath = configDirUri.ResolvePaths(s)
		} else {
			console.Error(`Config "typingsPath" field must contain a string.`)
		}
	}

	readPath("stubPath", `Config "stubPath" field must contain a string.`, &c.StubPath)

	// The original's comment on "verboseOutput": don't initialize to a default
	// value because we want the command-line "verbose" switch to apply if this
	// setting isn't specified in the config file.
	if value, ok := jsonDefined(configObj, "verboseOutput"); ok {
		unusedConfigKeys.Delete("verboseOutput")
		if b, ok := jsonBool(value); ok {
			c.VerboseOutput = &b
		} else {
			console.Error(`Config "verboseOutput" field must be true or false.`)
		}
	}

	// Read the "defineConstant" setting.
	if value, ok := jsonDefined(configObj, "defineConstant"); ok {
		unusedConfigKeys.Delete("defineConstant")
		constants, isObj := jsonObject(value)
		if _, isArray := jsonArray(value); !isObj || isArray {
			console.Error(`Config "defineConstant" field must contain a map indexed by constant names.`)
		} else {
			for _, key := range sortedKeys(constants) {
				entry := constants[key]
				if b, ok := jsonBool(entry); ok {
					c.DefineConstant.Set(key, b)
				} else if s, ok := jsonString(entry); ok {
					c.DefineConstant.Set(key, s)
				} else {
					console.Error(`Defined constant "` + key + `" must be associated with a boolean or string value.`)
				}
			}
		}
	}

	// The second read of "useLibraryCodeForTypes"; see the note above.
	if value, ok := jsonDefined(configObj, "useLibraryCodeForTypes"); ok {
		unusedConfigKeys.Delete("useLibraryCodeForTypes")
		if b, ok := jsonBool(value); ok {
			c.UseLibraryCodeForTypes = &b
		} else {
			console.Error(`Config "useLibraryCodeForTypes" field must be true or false.`)
		}
	}

	// Read the "autoImportCompletions" setting.
	if value, ok := jsonDefined(configObj, "autoImportCompletions"); ok {
		unusedConfigKeys.Delete("autoImportCompletions")
		if b, ok := jsonBool(value); ok {
			c.AutoImportCompletions = b
		} else {
			console.Error(`Config "autoImportCompletions" field must be true or false.`)
		}
	}

	// Read the "indexing" setting.
	if value, ok := jsonDefined(configObj, "indexing"); ok {
		unusedConfigKeys.Delete("indexing")
		if b, ok := jsonBool(value); ok {
			c.Indexing = b
		} else {
			console.Error(`Config "indexing" field must be true or false.`)
		}
	}

	// Read the "logTypeEvaluationTime" setting.
	if value, ok := jsonDefined(configObj, "logTypeEvaluationTime"); ok {
		unusedConfigKeys.Delete("logTypeEvaluationTime")
		if b, ok := jsonBool(value); ok {
			c.LogTypeEvaluationTime = b
		} else {
			console.Error(`Config "logTypeEvaluationTime" field must be true or false.`)
		}
	}

	// Read the "typeEvaluationTimeThreshold" setting.
	if value, ok := jsonDefined(configObj, "typeEvaluationTimeThreshold"); ok {
		unusedConfigKeys.Delete("typeEvaluationTimeThreshold")
		if n, ok := jsonNumber(value); ok {
			c.TypeEvaluationTimeThreshold = int(n)
		} else {
			console.Error(`Config "typeEvaluationTimeThreshold" field must be a number.`)
		}
	}

	// Read the "functionSignatureDisplay" setting. The original's message says
	// "true or false", which is wrong but is what it prints.
	if value, ok := jsonDefined(configObj, "functionSignatureDisplay"); ok {
		unusedConfigKeys.Delete("functionSignatureDisplay")
		if s, ok := jsonString(value); ok {
			if s == SignatureDisplayCompact || s == SignatureDisplayFormatted {
				c.FunctionSignatureDisplay = s
			}
		} else {
			console.Error(`Config "functionSignatureDisplay" field must be true or false.`)
		}
	}

	unusedConfigKeys.Delete("executionEnvironments")
	unusedConfigKeys.Delete("extends")

	for _, unknownKey := range unusedConfigKeys.Values() {
		console.Error(`Config contains unrecognized setting "` + unknownKey + `".`)
	}
}

// ResolveExtends corresponds to the static of the same name. It returns nil
// where the original returns undefined.
//
// The original logs through the global `console`, not the injected one -- the
// method is static and has none. The injected console is used here, which
// changes where the message goes and nothing else.
func ResolveExtends(configObjValue any, configDirUri uri.Uri, console common.ConsoleInterface) uri.Uri {
	configObj, ok := jsonObject(configObjValue)
	if !ok {
		return nil
	}

	if value, ok := jsonDefined(configObj, "extends"); ok {
		if s, ok := jsonString(value); ok {
			return configDirUri.ResolvePaths(s)
		}
		console.Error(`Config "extends" field must contain a string.`)
	}

	return nil
}

// SetupExecutionEnvironments reads the "executionEnvironments" array.
//
// The original's comment: this should be done at the end after we've
// established default values.
func (c *ConfigOptions) SetupExecutionEnvironments(configObjValue any, configDirUri uri.Uri, console common.ConsoleInterface) {
	configObj, ok := jsonObject(configObjValue)
	if !ok {
		return
	}

	value, ok := jsonDefined(configObj, "executionEnvironments")
	if !ok {
		return
	}

	execEnvironments, ok := jsonArray(value)
	if !ok {
		console.Error(`Config "executionEnvironments" field must contain an array.`)
		return
	}

	c.ExecutionEnvironments = []*ExecutionEnvironment{}

	for index, env := range execEnvironments {
		defaultExtraPaths := c.DefaultExtraPaths
		if defaultExtraPaths == nil {
			defaultExtraPaths = []uri.Uri{}
		}

		execEnv := c.initExecutionEnvironmentFromJson(
			env,
			configDirUri,
			index,
			console,
			c.DiagnosticRuleSet,
			c.DefaultPythonVersion,
			c.DefaultPythonPlatform,
			defaultExtraPaths,
		)

		if execEnv != nil {
			c.ExecutionEnvironments = append(c.ExecutionEnvironments, execEnv)
		}
	}
}

func convertBoolean(console common.ConsoleInterface, value any, fieldName string, defaultValue bool) bool {
	if value == nil {
		return defaultValue
	}
	if b, ok := jsonBool(value); ok {
		return b
	}

	console.Log(`Config "` + fieldName + `" entry must be true or false.`)
	return defaultValue
}

func convertDiagnosticLevel(console common.ConsoleInterface, value any, fieldName string, defaultValue DiagnosticLevel) DiagnosticLevel {
	if value == nil {
		return defaultValue
	}
	if b, ok := jsonBool(value); ok {
		if b {
			return DiagnosticLevelError
		}
		return DiagnosticLevelNone
	}
	if s, ok := jsonString(value); ok {
		switch s {
		case DiagnosticLevelError, DiagnosticLevelWarning, DiagnosticLevelInformation, DiagnosticLevelNone:
			return s
		}
	}

	console.Log(`Config "` + fieldName + `" entry must be true, false, "error", "warning", "information" or "none".`)
	return defaultValue
}

// initExecutionEnvironmentFromJson returns nil where the original returns
// undefined. Its try/catch, which reports "is not accessible", cannot be
// reached from a decoded JSON value; the recover is here anyway so a panic
// produces the original's message rather than unwinding the whole load.
func (c *ConfigOptions) initExecutionEnvironmentFromJson(
	envObjValue any,
	configDirUri uri.Uri,
	index int,
	console common.ConsoleInterface,
	configDiagnosticRuleSet *DiagnosticRuleSet,
	configPythonVersion *common.PythonVersion,
	configPythonPlatform string,
	configExtraPaths []uri.Uri,
) (result *ExecutionEnvironment) {
	defer func() {
		if r := recover(); r != nil {
			console.Error("Config executionEnvironments index " + strconv.Itoa(index) + " is not accessible.")
			result = nil
		}
	}()

	envObj, isObject := jsonObject(envObjValue)
	if !isObject {
		envObj = map[string]any{}
	}

	unusedEnvKeys := common.NewOrderedSet[string]()
	for _, key := range sortedKeys(envObj) {
		unusedEnvKeys.Add(key)
	}

	newExecEnv := NewExecutionEnvironment(
		c.getEnvironmentName(),
		configDirUri,
		configDiagnosticRuleSet,
		configPythonVersion,
		configPythonPlatform,
		configExtraPaths,
		false,
	)

	// Validate the root. The original's test is truthiness *and* a string
	// check, so an empty string takes the error branch.
	unusedEnvKeys.Delete("root")
	if root, ok := jsonString(envObj["root"]); ok && root != "" {
		newExecEnv.Root = configDirUri.ResolvePaths(root)
	} else {
		console.Error("Config executionEnvironments index " + strconv.Itoa(index) + ": missing root value.")
	}

	// Validate the extraPaths.
	unusedEnvKeys.Delete("extraPaths")
	if extraPaths, present := envObj["extraPaths"]; present && isTruthy(extraPaths) {
		pathList, ok := jsonArray(extraPaths)
		if !ok {
			console.Error("Config executionEnvironments index " + strconv.Itoa(index) +
				": extraPaths field must contain an array.")
		} else {
			// The original's comment: if specified, this overrides the default
			// extra paths inherited from the top-level config.
			newExecEnv.ExtraPaths = []uri.Uri{}

			for pathIndex, entry := range pathList {
				path, ok := jsonString(entry)
				if !ok {
					console.Error("Config executionEnvironments index " + strconv.Itoa(index) +
						": extraPaths field " + strconv.Itoa(pathIndex) + " must be a string.")
				} else {
					newExecEnv.ExtraPaths = append(newExecEnv.ExtraPaths, configDirUri.ResolvePaths(path))
				}
			}
		}
	}

	// Validate the pythonVersion.
	unusedEnvKeys.Delete("pythonVersion")
	if pythonVersion, present := envObj["pythonVersion"]; present && isTruthy(pythonVersion) {
		if s, ok := jsonString(pythonVersion); ok {
			if version := common.PythonVersionFromString(s); version != nil {
				newExecEnv.PythonVersion = *version
			} else {
				console.Warn("Config executionEnvironments index " + strconv.Itoa(index) +
					" contains unsupported pythonVersion.")
			}
		} else {
			console.Error("Config executionEnvironments index " + strconv.Itoa(index) +
				" pythonVersion must be a string.")
		}
	}

	// Validate the pythonPlatform.
	unusedEnvKeys.Delete("pythonPlatform")
	if pythonPlatform, present := envObj["pythonPlatform"]; present && isTruthy(pythonPlatform) {
		if s, ok := jsonString(pythonPlatform); ok {
			newExecEnv.PythonPlatform = s
		} else {
			console.Error("Config executionEnvironments index " + strconv.Itoa(index) +
				" pythonPlatform must be a string.")
		}
	}

	// Validate the name.
	unusedEnvKeys.Delete("name")
	if name, present := envObj["name"]; present && isTruthy(name) {
		if s, ok := jsonString(name); ok {
			newExecEnv.Name = s
		} else {
			console.Error("Config executionEnvironments index " + strconv.Itoa(index) + " name must be a string.")
		}
	}

	// Apply overrides from the config file for the boolean overrides.
	for _, ruleName := range GetBooleanDiagnosticRules(true) {
		unusedEnvKeys.Delete(ruleName)
		if field := diagnosticRuleBoolFields[ruleName]; field != nil {
			target := field(newExecEnv.DiagnosticRuleSet)
			*target = convertBoolean(console, envObj[ruleName], ruleName, *target)
		}
	}

	// Apply overrides from the config file for the diagnostic level overrides.
	for _, ruleName := range GetDiagLevelDiagnosticRules() {
		unusedEnvKeys.Delete(ruleName)
		if field := diagnosticRuleLevelFields[ruleName]; field != nil {
			target := field(newExecEnv.DiagnosticRuleSet)
			*target = convertDiagnosticLevel(console, envObj[ruleName], ruleName, *target)
		}
	}

	for _, unknownKey := range unusedEnvKeys.Values() {
		console.Error("Config executionEnvironments index " + strconv.Itoa(index) +
			`: unrecognized setting "` + unknownKey + `".`)
	}

	return newExecEnv
}

// isTruthy is JavaScript truthiness for the values a config reader produces.
// The execution-environment reads are guarded by `if (envObj.x)` rather than
// `!== undefined`, so an empty string, a zero and a false all skip the field.
func isTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	case int64:
		return v != 0
	}
	return true
}

// sortedKeys stands in for Object.getOwnPropertyNames, which answers a JSON
// object's keys in insertion order. Go maps have no order, so the unrecognized-
// setting messages would otherwise vary between runs; sorting makes them
// deterministic. The set of messages is the same either way.
func sortedKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
