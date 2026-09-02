/*
 * envvarutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Utility functions that handle environment variables.
 *
 * Transliterated from common/envVarUtils.ts (pyright 1.1.412).
 *
 * PARTIAL: only expandPathVariables. resolvePathWithEnvVariables takes a
 * `Workspace` from workspaceFactory.ts, which is the language server's model of
 * an open folder and is out of scope; nothing in the analyzer calls it.
 */

package common

import (
	"os"
	"regexp"
	"strings"
)

// PathVariableWorkspace is the part of `WorkspaceFolder` expandPathVariables
// reads.
type PathVariableWorkspace struct {
	WorkspaceName string

	// RootUri is nilable; the original skips a workspace without one.
	RootUri interface{ GetPath() string }
}

var (
	workspaceFolderVar = regexp.MustCompile(`\$\{workspaceFolder\}`)
	envHomeVar         = regexp.MustCompile(`\$\{env:HOME\}`)
	envUsernameVar     = regexp.MustCompile(`\$\{env:USERNAME\}`)
	envVirtualEnvVar   = regexp.MustCompile(`\$\{env:VIRTUAL_ENV\}`)

	// `/(?:^|\/)~(?=\/)/g` -- a '~' that begins the string or follows a slash
	// and is followed by a slash. RE2 has no lookahead, so the trailing slash is
	// consumed and put back by the replacement.
	homeDirVar = regexp.MustCompile(`(^|/)~/`)
)

// ExpandPathVariables expands certain predefined variables supported within VS
// Code settings.
//
// The original's comment: ideally, VS Code would provide an API for doing this
// expansion, but it doesn't. We'll handle the most common variables here as a
// convenience.
func ExpandPathVariables(path string, rootPath interface{ GetPath() string }, workspaces []PathVariableWorkspace) string {
	// Replace everything inline.
	path = workspaceFolderVar.ReplaceAllLiteralString(path, rootPath.GetPath())

	// The original's comment: this is for vscode multiroot workspace supports.
	// https://code.visualstudio.com/docs/editor/variables-reference#_variables-scoped-per-workspace-folder
	for _, workspace := range workspaces {
		if workspace.RootUri == nil {
			continue
		}

		escapedWorkspaceName := EscapeRegExp(workspace.WorkspaceName)
		wsRegexp, err := regexp.Compile(`\$\{workspaceFolder:` + escapedWorkspaceName + `\}`)
		if err != nil {
			continue
		}
		path = wsRegexp.ReplaceAllLiteralString(path, workspace.RootUri.GetPath())
	}

	// Each of these is guarded on the variable being *defined*, not on it being
	// non-empty -- so `HOME=` still substitutes, with the empty string.
	if value, ok := os.LookupEnv("HOME"); ok {
		path = envHomeVar.ReplaceAllLiteralString(path, value)
	}
	if value, ok := os.LookupEnv("USERNAME"); ok {
		path = envUsernameVar.ReplaceAllLiteralString(path, value)
	}
	if value, ok := os.LookupEnv("VIRTUAL_ENV"); ok {
		path = envVirtualEnvVar.ReplaceAllLiteralString(path, value)
	}

	// `os.homedir() || process.env.HOME || process.env.USERPROFILE || '~'`.
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		home = "~"
	}
	path = homeDirVar.ReplaceAllString(path, "${1}"+quoteReplacement(home)+"/")

	return path
}

// quoteReplacement escapes '$' so ReplaceAllString does not read it as a group
// reference. The '$1' the caller supplies is added separately.
func quoteReplacement(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}
