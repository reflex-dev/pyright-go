/*
 * timing.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * A simple duration class that can be used to record and report
 * durations at the millisecond level of resolution.
 *
 * Transliterated from common/timing.ts (pyright 1.1.412).
 *
 * The TypeScript version measures with Date.now(), i.e. whole milliseconds of
 * wall-clock time. time.Since is monotonic and finer-grained, so it is
 * truncated to milliseconds here to keep totalTime in the same units that
 * printTime formats.
 */

package common

import (
	"math"
	"strconv"
	"time"
)

// Duration corresponds to the Duration class.
type Duration struct {
	startTime time.Time
}

// NewDuration corresponds to the Duration constructor.
func NewDuration() *Duration {
	return &Duration{startTime: time.Now()}
}

// GetDurationInMilliseconds corresponds to getDurationInMilliseconds().
func (d *Duration) GetDurationInMilliseconds() int64 {
	return time.Since(d.startTime).Milliseconds()
}

// GetDurationInSeconds corresponds to getDurationInSeconds().
func (d *Duration) GetDurationInSeconds() float64 {
	return float64(d.GetDurationInMilliseconds()) / 1000
}

// TimingStat corresponds to the TimingStat class.
type TimingStat struct {
	TotalTime int64
	CallCount int
	IsTiming  bool
}

// TimeOperation corresponds to timeOperation(). The TypeScript version is
// generic over the callback's return type; the parser only uses the void form,
// so this takes a plain func().
func (s *TimingStat) TimeOperation(callback func()) {
	s.CallCount++

	// Handle reentrancy.
	if s.IsTiming {
		callback()
		return
	}

	s.IsTiming = true
	duration := NewDuration()
	callback()
	s.TotalTime += duration.GetDurationInMilliseconds()
	s.IsTiming = false
}

// SubtractFromTime corresponds to subtractFromTime().
func (s *TimingStat) SubtractFromTime(callback func()) {
	if s.IsTiming {
		s.IsTiming = false
		duration := NewDuration()
		callback()
		s.TotalTime -= duration.GetDurationInMilliseconds()
		s.IsTiming = true
	} else {
		callback()
	}
}

// PrintTime corresponds to printTime(). It reproduces the JavaScript
// formatting: round to two decimal places, then render with Number.toString(),
// which drops trailing zeros ("1.5sec", not "1.50sec").
func (s *TimingStat) PrintTime() string {
	totalTimeInSec := float64(s.TotalTime) / 1000
	roundedTime := math.Floor(totalTimeInSec*100+0.5) / 100
	return strconv.FormatFloat(roundedTime, 'f', -1, 64) + "sec"
}

// TimingStats corresponds to the TimingStats class.
type TimingStats struct {
	TotalDuration      *Duration
	FindFilesTime      TimingStat
	ReadFileTime       TimingStat
	TokenizeFileTime   TimingStat
	ParseFileTime      TimingStat
	ResolveImportsTime TimingStat
	CycleDetectionTime TimingStat
	BindTime           TimingStat
	TypeCheckerTime    TimingStat
	TypeEvaluationTime TimingStat
}

// NewTimingStats constructs a TimingStats with a freshly started duration.
func NewTimingStats() *TimingStats {
	return &TimingStats{TotalDuration: NewDuration()}
}

// GetTotalDuration corresponds to getTotalDuration().
func (s *TimingStats) GetTotalDuration() float64 {
	return s.TotalDuration.GetDurationInSeconds()
}

// PrintSummary corresponds to printSummary(). The ConsoleInterface port is not
// done yet, so this takes the info callback directly.
func (s *TimingStats) PrintSummary(info func(string)) {
	info("Completed in " + strconv.FormatFloat(s.TotalDuration.GetDurationInSeconds(), 'f', -1, 64) + "sec")
}

// PrintDetails corresponds to printDetails().
func (s *TimingStats) PrintDetails(info func(string)) {
	info("")
	info("Timing stats")
	info("Find Source Files:    " + s.FindFilesTime.PrintTime())
	info("Read Source Files:    " + s.ReadFileTime.PrintTime())
	info("Tokenize:             " + s.TokenizeFileTime.PrintTime())
	info("Parse:                " + s.ParseFileTime.PrintTime())
	info("Resolve Imports:      " + s.ResolveImportsTime.PrintTime())
	info("Bind:                 " + s.BindTime.PrintTime())
	info("Check:                " + s.TypeCheckerTime.PrintTime())
	info("Detect Cycles:        " + s.CycleDetectionTime.PrintTime())
}

// TimingStatsInstance corresponds to the exported `timingStats` singleton.
var TimingStatsInstance = NewTimingStats()
