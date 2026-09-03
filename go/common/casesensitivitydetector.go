/*
 * casesensitivitydetector.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Interface to determine whether the given uri string should be case sensitive
 * or not.
 *
 * Transliterated from common/caseSensitivityDetector.ts (pyright 1.1.412).
 *
 * The namespace's `is` guard exists so the Uri factory functions can accept
 * either a detector or a ServiceProvider. Go has no untagged unions, so each
 * factory takes a CaseSensitivityDetector and the service-provider overload is
 * dropped along with the rest of the DI plumbing; the port passes
 * dependencies directly.
 */

package common

// CaseSensitivityDetector corresponds to the interface of the same name.
type CaseSensitivityDetector interface {
	IsCaseSensitive(uri string) bool
}

// CaseSensitivityDetectorFunc adapts a plain function, which is how the
// original writes its two constant detectors in UriEx.
type CaseSensitivityDetectorFunc func(uri string) bool

func (f CaseSensitivityDetectorFunc) IsCaseSensitive(uri string) bool { return f(uri) }
