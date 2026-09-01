/*
 * importlogger.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Utilities for logging information about import resolution failures.
 *
 * Transliterated from analyzer/importLogger.ts (pyright 1.1.412), whose own
 * header still calls it importLogging.ts.
 */

package analyzer

// ImportLogger collects the messages produced while resolving an import, so
// they can be replayed only when the resolution fails.
//
// Every parameter of type `ImportLogger | undefined` in the original is a
// nilable *ImportLogger here, and every `importLogger?.log(...)` becomes a
// method that tolerates a nil receiver -- which is what the optional-call
// operator does.
type ImportLogger struct {
	logs []string
}

func NewImportLogger() *ImportLogger { return &ImportLogger{} }

func (l *ImportLogger) Log(message string) {
	if l == nil {
		return
	}
	l.logs = append(l.logs, message)
}

func (l *ImportLogger) GetLogs() []string {
	if l == nil {
		return nil
	}
	return l.logs
}
