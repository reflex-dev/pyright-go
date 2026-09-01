/*
 * constanturi.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * URI type that represents a constant/marker URI.
 *
 * Transliterated from common/uri/constantUri.ts (pyright 1.1.412).
 */

package uri

// ConstantUri is a marker URI, created by Constant. Almost every operation on
// it is inert: it has no path, is not a child of anything, and every derived
// URI is itself.
type ConstantUri struct {
	baseUri
}

func newConstantUri(name string) *ConstantUri {
	u := &ConstantUri{}
	u.key = name
	u.self = u
	return u
}

func (u *ConstantUri) Scheme() string { return "" }

func (u *ConstantUri) IsCaseSensitive() bool { return true }

func (u *ConstantUri) FileName() string { return "" }

func (u *ConstantUri) LastExtension() string { return "" }

func (u *ConstantUri) Root() Uri { return u.self }

func (u *ConstantUri) Fragment() string { return "" }

func (u *ConstantUri) Query() string { return "" }

// Equals uses reference equality rather than value equality, as the original
// notes: two distinct markers with the same name are not the same marker.
func (u *ConstantUri) Equals(other Uri) bool {
	return Uri(u.self) == other
}

func (u *ConstantUri) String() string { return u.Key() }

func (u *ConstantUri) ToUserVisibleString() string { return "" }

func (u *ConstantUri) MatchesRegex(regex Regexp) bool { return false }

func (u *ConstantUri) WithFragment(fragment string) Uri { return u.self }

func (u *ConstantUri) WithQuery(query string) Uri { return u.self }

func (u *ConstantUri) AddPath(extra string) Uri { return u.self }

func (u *ConstantUri) GetDirectory() Uri { return u.self }

func (u *ConstantUri) IsRoot() bool { return false }

func (u *ConstantUri) IsChild(parent Uri) bool { return false }

func (u *ConstantUri) IsLocal() bool { return false }

func (u *ConstantUri) StartsWith(other Uri) bool { return false }

func (u *ConstantUri) GetPathLength() int { return 0 }

func (u *ConstantUri) ResolvePaths(paths ...string) Uri { return u.self }

func (u *ConstantUri) CombinePaths(paths ...string) Uri { return u.self }

func (u *ConstantUri) CombinePathsUnsafe(paths ...string) Uri { return u.self }

func (u *ConstantUri) GetPath() string { return "" }

func (u *ConstantUri) GetFilePath() string { return "" }

func (u *ConstantUri) StripExtension() Uri { return u.self }

func (u *ConstantUri) StripAllExtensions() Uri { return u.self }

func (u *ConstantUri) getRootPath() string { return "" }

func (u *ConstantUri) getComparablePath() string { return "" }

func (u *ConstantUri) getPathComponentsImpl() []string { return []string{} }
