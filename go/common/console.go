/*
 * console.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Provides an abstraction for console logging and error-reporting methods.
 *
 * Transliterated from common/console.ts (pyright 1.1.412).
 *
 * PARTIAL: the ConsoleInterface, its level machinery, and the two
 * implementations the analyzer reaches. ConsoleWithLogLevel and the
 * chattyness-filtering wrappers exist for the language server, which
 * ANALYZER-PLAN.md puts out of scope.
 */

package common

import (
	"fmt"
	"os"
)

// LogLevel corresponds to the enum of the same name.
type LogLevel = string

const (
	LogLevelError LogLevel = "error"
	LogLevelWarn  LogLevel = "warn"
	LogLevelInfo  LogLevel = "info"
	LogLevelLog   LogLevel = "log"
)

// ConsoleInterface corresponds to the interface of the same name.
type ConsoleInterface interface {
	Error(message string)
	Warn(message string)
	Info(message string)
	Log(message string)
}

// GetLevelNumber corresponds to the function of the same name. The original
// answers 3 for an unrecognized level.
func GetLevelNumber(level LogLevel) int {
	switch level {
	case LogLevelError:
		return 0
	case LogLevelWarn:
		return 1
	case LogLevelInfo:
		return 2
	case LogLevelLog:
		return 3
	}
	return 3
}

// NullConsole avoids outputting errors to the console but counts the number of
// logs and errors, which the original notes is useful for unit tests.
type NullConsole struct {
	LogCount   int
	InfoCount  int
	WarnCount  int
	ErrorCount int
}

func NewNullConsole() *NullConsole { return &NullConsole{} }

func (c *NullConsole) Log(message string)   { c.LogCount++ }
func (c *NullConsole) Info(message string)  { c.InfoCount++ }
func (c *NullConsole) Warn(message string)  { c.WarnCount++ }
func (c *NullConsole) Error(message string) { c.ErrorCount++ }

// StandardConsole writes to the process console, filtered by a maximum level.
type StandardConsole struct {
	maxLevel LogLevel
}

// NewStandardConsole corresponds to the constructor. The TypeScript defaults
// maxLevel to LogLevel.Log.
func NewStandardConsole(maxLevel LogLevel) *StandardConsole {
	return &StandardConsole{maxLevel: maxLevel}
}

func (c *StandardConsole) Level() LogLevel { return c.maxLevel }

func (c *StandardConsole) Error(message string) { c.write(LogLevelError, message) }
func (c *StandardConsole) Warn(message string)  { c.write(LogLevelWarn, message) }
func (c *StandardConsole) Info(message string)  { c.write(LogLevelInfo, message) }
func (c *StandardConsole) Log(message string)   { c.write(LogLevelLog, message) }

func (c *StandardConsole) write(level LogLevel, message string) {
	if GetLevelNumber(c.maxLevel) < GetLevelNumber(level) {
		return
	}
	if level == LogLevelError {
		fmt.Fprintln(os.Stderr, message)
		return
	}
	fmt.Fprintln(os.Stdout, message)
}
