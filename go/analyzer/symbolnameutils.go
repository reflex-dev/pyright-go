/*
 * symbolnameutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Static methods that apply to symbols or symbol names.
 *
 * Transliterated from analyzer/symbolNameUtils.ts (pyright 1.1.412).
 */

package analyzer

import (
	"regexp"
	"strings"
)

var (
	constantRegEx       = regexp.MustCompile(`^[A-Z0-9_]+$`)
	underscoreOnlyRegEx = regexp.MustCompile(`^[_]+$`)
	camelCaseRegEx      = regexp.MustCompile(`^_{0,2}[A-Z][A-Za-z0-9_]+$`)
)

// IsPrivateName reports whether the name is private. Private symbol names start
// with a double underscore.
func IsPrivateName(name string) bool {
	return len(name) > 2 && strings.HasPrefix(name, "__") && !strings.HasSuffix(name, "__")
}

// IsProtectedName reports whether the name is protected. Protected symbol names
// start with a single underscore.
func IsProtectedName(name string) bool {
	return len(name) > 1 && strings.HasPrefix(name, "_") && !strings.HasPrefix(name, "__")
}

func IsPrivateOrProtectedName(name string) bool {
	return IsPrivateName(name) || IsProtectedName(name)
}

// IsDunderName reports whether the name is a "dunder" name. These start and end
// with two underscores.
func IsDunderName(name string) bool {
	return len(name) > 4 && strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__")
}

// IsSingleDunderName reports whether the name is a "single dunder" name. These
// start and end with single underscores.
func IsSingleDunderName(name string) bool {
	return len(name) > 2 && strings.HasPrefix(name, "_") && strings.HasSuffix(name, "_")
}

// IsConstantName reports whether the name looks like a constant: all-caps with
// possible numbers and underscores.
func IsConstantName(name string) bool {
	return constantRegEx.MatchString(name) && !underscoreOnlyRegEx.MatchString(name)
}

// IsTypeAliasName reports whether the name looks like a type alias: CamelCase
// with possible numbers and underscores.
func IsTypeAliasName(name string) bool {
	return camelCaseRegEx.MatchString(name)
}

func IsPublicConstantOrTypeAlias(name string) bool {
	return !IsPrivateOrProtectedName(name) && (IsConstantName(name) || IsTypeAliasName(name))
}
