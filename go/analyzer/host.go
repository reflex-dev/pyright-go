/*
 * host.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Provides access to the host environment the language service is running on.
 *
 * Transliterated from common/host.ts (pyright 1.1.412).
 *
 * Layout note: the original is in common/, but it references PythonPlatform
 * from common/configOptions.ts and PythonPathResult from
 * analyzer/pythonPathUtils.ts, both of which live in this package. It comes
 * along for the same reason configOptions did.
 *
 * PARTIAL: the three members that run a Python interpreter -- runScript,
 * runSnippet and spawnProcess -- are dropped along with ScriptOutput,
 * SpawnedProcess and ProcessSpawnOptions. They exist to shell out to the
 * configured interpreter, they are asynchronous and cancellable, and nothing
 * in the analyzer calls them: their callers are the language server and the
 * type-stub generator, both out of scope. What remains is the three
 * synchronous queries the import resolver and ConfigOptions actually make.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// HostKind corresponds to the const enum of the same name.
type HostKind int

const (
	HostKindFullAccess HostKind = iota
	HostKindLimitedAccess
	HostKindNoAccess
)

// Host corresponds to the interface of the same name.
//
// Every argument the original marks optional is nilable here: a nil Uri is the
// absent path, and a nil *ImportLogger is the absent logger, which
// ImportLogger's methods tolerate.
type Host interface {
	Kind() HostKind
	GetPythonSearchPaths(pythonPath uri.Uri, failureLogger *ImportLogger, cwd uri.Uri) PythonPathResult

	// GetPythonVersion returns nil where the original returns undefined.
	GetPythonVersion(pythonPath uri.Uri, failureLogger *ImportLogger) *common.PythonVersion

	// GetPythonPlatform returns "" where the original returns undefined,
	// which is the same test every caller makes.
	GetPythonPlatform(failureLogger *ImportLogger) PythonPlatform
}

// NoAccessHost corresponds to the class of the same name: a host that knows
// nothing about any Python installation.
type NoAccessHost struct{}

var _ Host = (*NoAccessHost)(nil)

func NewNoAccessHost() *NoAccessHost { return &NoAccessHost{} }

func (h *NoAccessHost) Kind() HostKind { return HostKindNoAccess }

func (h *NoAccessHost) GetPythonSearchPaths(pythonPath uri.Uri, failureLogger *ImportLogger, cwd uri.Uri) PythonPathResult {
	failureLogger.Log("No access to python executable.")

	return PythonPathResult{Paths: []uri.Uri{}, Prefix: nil}
}

func (h *NoAccessHost) GetPythonVersion(pythonPath uri.Uri, failureLogger *ImportLogger) *common.PythonVersion {
	return nil
}

func (h *NoAccessHost) GetPythonPlatform(failureLogger *ImportLogger) PythonPlatform { return "" }

// HostFactory corresponds to the type of the same name.
type HostFactory func() Host
