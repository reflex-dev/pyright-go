/*
 * emptyuri.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * URI type that represents an empty URI.
 *
 * Transliterated from common/uri/emptyUri.ts (pyright 1.1.412).
 */

package uri

const emptyKey = "<empty>"

// EmptyUri is the single empty URI, returned by Empty. The TypeScript makes it
// a private-constructor singleton for the same reason ConstantUri compares by
// reference: there must be exactly one.
type EmptyUri struct {
	ConstantUri
}

var emptyUriInstance = newEmptyUri()

func newEmptyUri() *EmptyUri {
	u := &EmptyUri{}
	u.key = emptyKey
	u.self = u
	return u
}

// IsEmptyUri corresponds to EmptyUri.isEmptyUri, which in the TypeScript
// sniffs the private _key off an arbitrary value.
func IsEmptyUri(u Uri) bool {
	if u == nil {
		return false
	}
	return u.Key() == emptyKey
}

func (u *EmptyUri) IsEmpty() bool { return true }

func (u *EmptyUri) String() string { return "" }
