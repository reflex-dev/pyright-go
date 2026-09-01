/*
 * circulardependency.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * A list of file paths that are part of a circular dependency chain (i.e. a
 * chain of imports). Since these are circular, there is no defined "start", but
 * this module helps normalize the start by picking the alphabetically-first
 * module in the cycle.
 *
 * Transliterated from analyzer/circularDependency.ts (pyright 1.1.412).
 */

package analyzer

import "github.com/microsoft/pyright/go/common/uri"

// CircularDependency corresponds to the class of the same name.
type CircularDependency struct {
	paths []uri.Uri
}

func NewCircularDependency() *CircularDependency {
	return &CircularDependency{}
}

func (c *CircularDependency) AppendPath(path uri.Uri) {
	c.paths = append(c.paths, path)
}

func (c *CircularDependency) GetPaths() []uri.Uri {
	return c.paths
}

// NormalizeOrder finds the path that is alphabetically first and reorders based
// on that.
//
// The original writes the comparison as `path < this._paths[firstIndex]` on two
// Uri *objects*. JavaScript's relational operators coerce each side to a
// primitive first, which for a Uri means toString(), so this compares the URI
// strings -- not the file paths, and not the keys.
func (c *CircularDependency) NormalizeOrder() {
	firstIndex := 0
	for index, path := range c.paths {
		if path.String() < c.paths[firstIndex].String() {
			firstIndex = index
		}
	}

	if firstIndex != 0 {
		reordered := append([]uri.Uri{}, c.paths[firstIndex:]...)
		c.paths = append(reordered, c.paths[:firstIndex]...)
	}
}

// IsEqual compares element by element.
//
// The original uses `!==`, which is reference inequality on Uri objects. That
// is not the same as Uri.equals -- but Uris are interned by createFileUri, so
// two Uris built from the same components are the same object, and the two
// agree in practice. Reproduced as written: Go's == on an interface compares
// the dynamic pointer, which is the same test.
func (c *CircularDependency) IsEqual(circDependency *CircularDependency) bool {
	if len(circDependency.paths) != len(c.paths) {
		return false
	}

	for i := range c.paths {
		if c.paths[i] != circDependency.paths[i] {
			return false
		}
	}

	return true
}
