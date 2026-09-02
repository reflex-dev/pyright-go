/*
 * logtracker.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * A simple logging class that can be used to track nested loggings.
 *
 * Transliterated from common/logTracker.ts (pyright 1.1.412), plus
 * getPathForLogging, which is in the same file and lands in common because the
 * Uri package is where the file system is.
 *
 * The original's `log` takes a callback and wraps it, so a nested call indents
 * and a suppressed one prints nothing. Go has no equivalent of the promise arm,
 * and a callback that returns a value cannot be written generically over a
 * method, so `Log` returns a state whose `Done` the caller defers. Nesting,
 * indentation, suppression and the deferred header printing all work the same;
 * what is lost is the exception arm, which in the original prints the header
 * and rethrows -- the deferred Done runs on a panic too.
 *
 * Layout note: in `analyzer` rather than `common` because it is only reached
 * from there, and putting it in `common` would need another Uri-shaped
 * interface.
 */

package analyzer

import (
	"strconv"

	"github.com/microsoft/pyright/go/common"
)

// durationThresholdForInfoInMs: consider an operation "long running" if it goes
// longer than this.
const durationThresholdForInfoInMs = 2000

// LogTracker corresponds to the class of the same name.
type LogTracker struct {
	console common.ConsoleInterface
	name    string
	header  string

	previousTitles []string
	indentation    string
}

// NewLogTracker corresponds to the constructor. The original computes the
// header from a `name` the console may carry; the ConsoleInterface port has no
// such member, so the header is always `[name] `.
func NewLogTracker(console common.ConsoleInterface, name string) *LogTracker {
	return &LogTracker{console: console, name: name, header: "[" + name + "] "}
}

func (l *LogTracker) Name() string { return l.name }

// LogState corresponds to the interface of the same name, plus the Done the
// caller defers in place of the original's callback wrapper.
type LogState struct {
	tracker *LogTracker

	addendum string
	suppress bool

	start     *common.Duration
	enabled   bool
	current   string
	title     string
	completed bool
}

func (s *LogState) Add(addendum string) {
	if addendum != "" {
		s.addendum = addendum
	}
}

func (s *LogState) Suppress() { s.suppress = true }

// Done ends the logged operation. The original does this at the end of the
// callback, and in its catch and promise arms.
func (s *LogState) Done() {
	if !s.enabled || s.completed {
		return
	}
	s.completed = true
	s.tracker.onComplete(s, s.current, s.title)
}

// Log begins a logged operation. The caller defers the returned state's Done.
//
// The original also takes a minimalDuration and a logParsingPerf flag; neither
// has a caller in the files ported so far, so both are dropped along with the
// per-phase timing breakdown they control.
func (l *LogTracker) Log(title string) *LogState {
	// If no console is given, don't do anything.
	if l.console == nil {
		return &LogState{tracker: l, enabled: false}
	}

	// The original enables this only when the console's level is Log or Info,
	// or when it has no level at all. StandardConsole is the only levelled
	// console in the port.
	if standard, ok := l.console.(*common.StandardConsole); ok {
		level := standard.Level()
		if level != common.LogLevelLog && level != common.LogLevelInfo {
			return &LogState{tracker: l, enabled: false}
		}
	}

	// The original's comment: since this is only used when LogLevel.Log or
	// LogLevel.Info is set or BG, we don't care much about extra logging cost.
	current := l.indentation
	l.previousTitles = append(l.previousTitles, current+title+" ...")

	l.indentation += "  "

	return &LogState{
		tracker: l,
		enabled: true,
		current: current,
		title:   title,
		start:   common.NewDuration(),
	}
}

func (l *LogTracker) onComplete(state *LogState, current string, title string) {
	msDuration := state.start.GetDurationInMilliseconds()
	l.indentation = current

	// The original's comment: if we already printed our header (by nested
	// calls), then it can't be skipped.
	if len(l.previousTitles) > 0 && state.suppress {
		// Get rid of myself so we don't even show a header.
		l.previousTitles = l.previousTitles[:len(l.previousTitles)-1]
		return
	}

	l.printPreviousTitles()

	addendum := ""
	if state.addendum != "" {
		addendum = " [" + state.addendum + "]"
	}
	output := l.header + l.indentation + title + addendum +
		" (" + strconv.FormatInt(msDuration, 10) + "ms)"

	l.console.Log(output)

	// The original's comment: if the operation took really long, log it as
	// "info" so it is more visible.
	if msDuration >= durationThresholdForInfoInMs {
		l.console.Info(l.header + "Long operation: " + title +
			" (" + strconv.FormatInt(msDuration, 10) + "ms)")
	}
}

func (l *LogTracker) printPreviousTitles() {
	// Get rid of myself.
	if len(l.previousTitles) > 0 {
		l.previousTitles = l.previousTitles[:len(l.previousTitles)-1]
	}

	if len(l.previousTitles) == 0 {
		return
	}

	for _, previousTitle := range l.previousTitles {
		l.console.Log(l.header + previousTitle)
	}

	l.previousTitles = nil
}
