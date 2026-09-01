/*
 * localize.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Code that localizes user-visible strings.
 *
 * Transliterated from localization/localize.ts (pyright 1.1.412). The ~1500
 * message accessors live in localize_gen.go, generated from the same
 * localize.ts by gen/generate.js. This file is the hand-written runtime that
 * those accessors call into: locale selection and the string-table lookup.
 *
 * The message text is not duplicated into Go source; the package embeds the
 * same package.nls.*.json files the TypeScript build ships.
 */

package localization

import (
	"embed"
	"encoding/json"
	"os"
	"strings"
	"sync"
)

//go:embed nls/*.json
var nlsFS embed.FS

// StringLookupMap corresponds to the StringLookupMap type: a nested map whose
// leaves are either a string or a { message, comment } object.
type StringLookupMap = map[string]any

const defaultLocale = "en-us"

// localeFileNames maps a locale tag to its embedded string table, mirroring
// stringMapsByLocale.
var localeFileNames = map[string]string{
	"cs":       "nls/package.nls.cs.json",
	"de":       "nls/package.nls.de.json",
	"en-us":    "nls/package.nls.en-us.json",
	"en":       "nls/package.nls.en-us.json",
	"es":       "nls/package.nls.es.json",
	"fr":       "nls/package.nls.fr.json",
	"it":       "nls/package.nls.it.json",
	"ja":       "nls/package.nls.ja.json",
	"ko":       "nls/package.nls.ko.json",
	"pl":       "nls/package.nls.pl.json",
	"pt-br":    "nls/package.nls.pt-br.json",
	"qps-ploc": "nls/package.nls.qps-ploc.json",
	"ru":       "nls/package.nls.ru.json",
	"tr":       "nls/package.nls.tr.json",
	"zh-cn":    "nls/package.nls.zh-cn.json",
	"zh-tw":    "nls/package.nls.zh-tw.json",
}

var (
	// mu guards the mutable module state that localize.ts keeps in
	// module-level `let` bindings. TypeScript runs single-threaded; Go
	// callers may not, and diagnostics are produced from parallel parses.
	mu sync.Mutex

	localizedStrings     StringLookupMap
	localizedStringsInit bool
	defaultStrings       StringLookupMap = StringLookupMap{}

	localeOverride          string
	forceEnglishDiagnostics bool

	// getRawStringFunc corresponds to the `getRawString` binding, which
	// setGetRawString can swap out.
	getRawStringFunc = getRawStringDefault
)

// loadLocaleMap reads and parses one embedded string table.
func loadLocaleMap(locale string) (StringLookupMap, bool) {
	name, ok := localeFileNames[locale]
	if !ok {
		return nil, false
	}

	data, err := nlsFS.ReadFile(name)
	if err != nil {
		return nil, false
	}

	var parsed StringLookupMap
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, false
	}
	return parsed, true
}

// getRawString corresponds to the `getRawString` call in localize.ts.
func getRawString(key string) string {
	mu.Lock()
	fn := getRawStringFunc
	mu.Unlock()
	return fn(key)
}

func getRawStringDefault(key string) string {
	mu.Lock()
	defer mu.Unlock()

	if !localizedStringsInit {
		localizedStrings = initialize()
		localizedStringsInit = true
	}

	keyParts := strings.Split(key, ".")
	isDiagnostic := keyParts[0] == "Diagnostic" || keyParts[0] == "DiagnosticAddendum"

	var str string
	if isDiagnostic && forceEnglishDiagnostics {
		str = GetRawStringFromMap(defaultStrings, keyParts)
	} else {
		str = GetRawStringFromMap(localizedStrings, keyParts)
		if str == "" {
			str = GetRawStringFromMap(defaultStrings, keyParts)
		}
	}

	if str != "" {
		return str
	}

	panic(`Debug Failure. Missing localized string for key "` + key + `"`)
}

// SetGetRawString allows different strings to be used for messages. It returns
// the previous function used for getting messages.
func SetGetRawString(fn func(key string) string) func(key string) string {
	mu.Lock()
	defer mu.Unlock()
	oldLookup := getRawStringFunc
	getRawStringFunc = fn
	return oldLookup
}

// GetRawStringFromMap corresponds to getRawStringFromMap(). It returns "" where
// the TypeScript version returns undefined; the caller treats both the same
// way, since the TypeScript version also treats an empty string as absent (it
// tests `!curObj[keyPart]`).
func GetRawStringFromMap(m StringLookupMap, keyParts []string) string {
	var curObj any = m

	for _, keyPart := range keyParts {
		asMap, ok := curObj.(map[string]any)
		if !ok {
			return ""
		}
		next, ok := asMap[keyPart]
		if !ok || isFalsy(next) {
			return ""
		}
		curObj = next
	}

	if s, ok := curObj.(string); ok {
		return s
	}
	if asMap, ok := curObj.(map[string]any); ok {
		if message, ok := asMap["message"].(string); ok {
			return message
		}
	}
	return ""
}

// isFalsy mirrors the `!curObj[keyPart]` test in getRawStringFromMap.
func isFalsy(v any) bool {
	switch value := v.(type) {
	case nil:
		return true
	case string:
		return value == ""
	case bool:
		return !value
	case float64:
		return value == 0
	default:
		return false
	}
}

func initialize() StringLookupMap {
	defaultStrings = loadDefaultStrings()
	currentLocale := getLocaleFromEnvLocked()
	return loadStringsForLocale(currentLocale)
}

// SetLocaleOverride corresponds to setLocaleOverride().
func SetLocaleOverride(locale string) {
	mu.Lock()
	defer mu.Unlock()
	// Force a reload of the localized strings.
	localizedStrings = nil
	localizedStringsInit = false
	localeOverride = strings.ToLower(locale)
}

// SetForceEnglishDiagnostics corresponds to setForceEnglishDiagnostics().
func SetForceEnglishDiagnostics(force bool) {
	mu.Lock()
	defer mu.Unlock()
	forceEnglishDiagnostics = force
}

// GetLocaleFromEnv corresponds to getLocaleFromEnv().
func GetLocaleFromEnv() string {
	mu.Lock()
	defer mu.Unlock()
	return getLocaleFromEnvLocked()
}

func getLocaleFromEnvLocked() string {
	if localeOverride != "" {
		return localeOverride
	}

	// Start with the VSCode environment variables.
	if vscodeConfigString := os.Getenv("VSCODE_NLS_CONFIG"); vscodeConfigString != "" {
		var config map[string]any
		if err := json.Unmarshal([]byte(vscodeConfigString), &config); err == nil {
			if locale, ok := config["locale"].(string); ok && locale != "" {
				return locale
			}
			return defaultLocale
		}
		// Fall through
	}

	// See if there is a language env variable.
	localeString := ""
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG", "LANGUAGE"} {
		if v := os.Getenv(name); v != "" {
			localeString = v
			break
		}
	}
	if localeString != "" {
		// This string may contain a locale followed by an encoding (e.g. "en-us.UTF-8").
		localeStringSplit := strings.Split(localeString, ".")
		if len(localeStringSplit) > 0 && localeStringSplit[0] != "" {
			return localeStringSplit[0]
		}
	}

	// Fall back to the default locale.
	return defaultLocale
}

func loadDefaultStrings() StringLookupMap {
	if m, ok := loadLocaleMap(defaultLocale); ok {
		return m
	}
	// This should never happen; the tables are embedded in the binary.
	panic("Debug Failure. Could not load default strings")
}

// LoadStringsForLocale corresponds to loadStringsForLocale().
func LoadStringsForLocale(locale string) StringLookupMap {
	return loadStringsForLocale(locale)
}

func loadStringsForLocale(locale string) StringLookupMap {
	if locale == defaultLocale {
		// No need to load override if we're using the default.
		return StringLookupMap{}
	}

	if override, ok := loadLocaleMap(locale); ok {
		return override
	}

	// If we couldn't find the requested locale, try to fall back on a more
	// general version.
	localeSplit := strings.Split(locale, "-")
	if len(localeSplit) > 0 && localeSplit[0] != "" {
		if override, ok := loadLocaleMap(localeSplit[0]); ok {
			return override
		}
	}

	return StringLookupMap{}
}

// replaceAll performs the substitution that ParameterizedString.format does.
//
// The TypeScript version uses `str.replace(new RegExp('{key}', 'g'), value)`,
// where the string replacement argument gives `$&`, `$1`, `$'` and friends
// their special meaning inside `value`. That is an accident of the JavaScript
// API rather than intended behavior -- a substituted type or symbol name
// containing a `$` sequence would be mangled -- so this does a literal
// replacement. This is the one place the port deliberately diverges.
func replaceAll(str, placeholder, value string) string {
	return strings.ReplaceAll(str, placeholder, value)
}
