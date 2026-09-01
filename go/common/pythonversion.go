/*
 * pythonversion.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Types and functions that relate to the Python language version.
 *
 * Transliterated from common/pythonVersion.ts (pyright 1.1.412).
 *
 * micro, releaseLevel and serial are optional in TypeScript, and several
 * comparisons branch specifically on `=== undefined`, so they are modeled as
 * pointers rather than zero values.
 */

package common

import (
	"strconv"
	"strings"
)

// PythonReleaseLevel corresponds to the 'alpha' | 'beta' | 'candidate' |
// 'final' union.
type PythonReleaseLevel = string

const (
	PythonReleaseLevelAlpha     PythonReleaseLevel = "alpha"
	PythonReleaseLevelBeta      PythonReleaseLevel = "beta"
	PythonReleaseLevelCandidate PythonReleaseLevel = "candidate"
	PythonReleaseLevelFinal     PythonReleaseLevel = "final"
)

// PythonVersion describes a Python language version.
type PythonVersion struct {
	Major        int
	Minor        int
	Micro        *int
	ReleaseLevel *PythonReleaseLevel
	Serial       *int
}

// NewPythonVersion corresponds to PythonVersion.create().
func NewPythonVersion(major, minor int, micro *int, releaseLevel *PythonReleaseLevel, serial *int) PythonVersion {
	return PythonVersion{
		Major:        major,
		Minor:        minor,
		Micro:        micro,
		ReleaseLevel: releaseLevel,
		Serial:       serial,
	}
}

// NewPythonVersionMajorMinor is the common two-argument form of create().
func NewPythonVersionMajorMinor(major, minor int) PythonVersion {
	return NewPythonVersion(major, minor, nil, nil, nil)
}

// IsEqualTo corresponds to PythonVersion.isEqualTo().
func (version PythonVersion) IsEqualTo(other PythonVersion) bool {
	if version.Major != other.Major || version.Minor != other.Minor {
		return false
	}

	if version.Micro == nil || other.Micro == nil {
		return true
	} else if *version.Micro != *other.Micro {
		return false
	}

	if version.ReleaseLevel == nil || other.ReleaseLevel == nil {
		return true
	} else if *version.ReleaseLevel != *other.ReleaseLevel {
		return false
	}

	if version.Serial == nil || other.Serial == nil {
		return true
	} else if *version.Serial != *other.Serial {
		return false
	}

	return true
}

// IsGreaterThan corresponds to PythonVersion.isGreaterThan().
func (version PythonVersion) IsGreaterThan(other PythonVersion) bool {
	if version.Major > other.Major {
		return true
	} else if version.Major < other.Major {
		return false
	}

	if version.Minor > other.Minor {
		return true
	} else if version.Minor < other.Minor {
		return false
	}

	if version.Micro == nil || other.Micro == nil || *version.Micro < *other.Micro {
		return false
	} else if *version.Micro > *other.Micro {
		return true
	}

	// We leverage the fact that the alphabetical ordering
	// of the release level designators are ordered by increasing
	// release level.
	if version.ReleaseLevel == nil || other.ReleaseLevel == nil || *version.ReleaseLevel < *other.ReleaseLevel {
		return false
	} else if *version.ReleaseLevel > *other.ReleaseLevel {
		return true
	}

	if version.Serial == nil || other.Serial == nil || *version.Serial < *other.Serial {
		return false
	} else if *version.Serial > *other.Serial {
		return true
	}

	// They are exactly equal!
	return false
}

// IsGreaterOrEqualTo corresponds to PythonVersion.isGreaterOrEqualTo().
func (version PythonVersion) IsGreaterOrEqualTo(other PythonVersion) bool {
	return version.IsEqualTo(other) || version.IsGreaterThan(other)
}

// IsLessThan corresponds to PythonVersion.isLessThan().
func (version PythonVersion) IsLessThan(other PythonVersion) bool {
	return !version.IsGreaterOrEqualTo(other)
}

// IsLessOrEqualTo corresponds to PythonVersion.isLessOrEqualTo().
func (version PythonVersion) IsLessOrEqualTo(other PythonVersion) bool {
	return !version.IsGreaterThan(other)
}

// ToMajorMinorString corresponds to PythonVersion.toMajorMinorString().
func (version PythonVersion) ToMajorMinorString() string {
	return strconv.Itoa(version.Major) + "." + strconv.Itoa(version.Minor)
}

// String corresponds to PythonVersion.toString().
func (version PythonVersion) String() string {
	versString := version.ToMajorMinorString()

	if version.Micro == nil {
		return versString
	}

	versString += "." + strconv.Itoa(*version.Micro)

	if version.ReleaseLevel == nil {
		return versString
	}

	versString += "." + *version.ReleaseLevel

	if version.Serial == nil {
		return versString
	}

	versString += "." + strconv.Itoa(*version.Serial)
	return versString
}

// PythonVersionFromString corresponds to PythonVersion.fromString(). It
// returns nil where TypeScript returns undefined.
func PythonVersionFromString(val string) *PythonVersion {
	split := strings.Split(val, ".")

	if len(split) < 2 {
		return nil
	}

	major, majorOK := parseIntBase10(split[0])
	minor, minorOK := parseIntBase10(split[1])

	if !majorOK || !minorOK {
		return nil
	}

	var micro *int
	if len(split) >= 3 {
		if v, ok := parseIntBase10(split[2]); ok {
			micro = &v
		}
	}

	var releaseLevel *PythonReleaseLevel
	if len(split) >= 4 {
		releaseLevels := []PythonReleaseLevel{
			PythonReleaseLevelAlpha,
			PythonReleaseLevelBeta,
			PythonReleaseLevelCandidate,
			PythonReleaseLevelFinal,
		}
		for _, level := range releaseLevels {
			if level == split[3] {
				v := split[3]
				releaseLevel = &v
				break
			}
		}
	}

	var serial *int
	if len(split) >= 5 {
		if v, ok := parseIntBase10(split[4]); ok {
			serial = &v
		}
	}

	version := NewPythonVersion(major, minor, micro, releaseLevel, serial)
	return &version
}

// parseIntBase10 mimics JavaScript's parseInt(s, 10): leading whitespace and a
// sign are allowed, parsing stops at the first non-digit, and an empty digit
// run yields NaN (reported here as ok == false).
func parseIntBase10(s string) (int, bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\v' || s[i] == '\f') {
		i++
	}

	negative := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		negative = s[i] == '-'
		i++
	}

	digitsStart := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}

	if i == digitsStart {
		return 0, false
	}

	value, err := strconv.Atoi(s[digitsStart:i])
	if err != nil {
		return 0, false
	}

	if negative {
		value = -value
	}
	return value, true
}

// Predefine some versions.
var (
	PythonVersion3_0  = NewPythonVersionMajorMinor(3, 0)
	PythonVersion3_1  = NewPythonVersionMajorMinor(3, 1)
	PythonVersion3_2  = NewPythonVersionMajorMinor(3, 2)
	PythonVersion3_3  = NewPythonVersionMajorMinor(3, 3)
	PythonVersion3_4  = NewPythonVersionMajorMinor(3, 4)
	PythonVersion3_5  = NewPythonVersionMajorMinor(3, 5)
	PythonVersion3_6  = NewPythonVersionMajorMinor(3, 6)
	PythonVersion3_7  = NewPythonVersionMajorMinor(3, 7)
	PythonVersion3_8  = NewPythonVersionMajorMinor(3, 8)
	PythonVersion3_9  = NewPythonVersionMajorMinor(3, 9)
	PythonVersion3_10 = NewPythonVersionMajorMinor(3, 10)
	PythonVersion3_11 = NewPythonVersionMajorMinor(3, 11)
	PythonVersion3_12 = NewPythonVersionMajorMinor(3, 12)
	PythonVersion3_13 = NewPythonVersionMajorMinor(3, 13)
	PythonVersion3_14 = NewPythonVersionMajorMinor(3, 14)
	PythonVersion3_15 = NewPythonVersionMajorMinor(3, 15)

	LatestStablePythonVersion = PythonVersion3_14
)
