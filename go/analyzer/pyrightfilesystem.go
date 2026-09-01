/*
 * pyrightfilesystem.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * A file system that knows how to deal with remapping files from one folder to
 * another.
 *
 * Transliterated from pyrightFileSystem.ts (pyright 1.1.412).
 *
 * The class is six overrides on ReadOnlyAugmentedFileSystem, each of which
 * lets one operation through to the backing file system instead of refusing it.
 * Writes go to the *original* location, not the mapped one.
 */

package analyzer

import "github.com/microsoft/pyright/go/common/uri"

// PyrightFileSystem corresponds to the class of the same name.
//
// IPyrightFileSystem in the original is `interface IPyrightFileSystem extends
// FileSystem {}` -- an alias with no members of its own, so uri.FileSystem
// stands in for it.
type PyrightFileSystem struct {
	*ReadOnlyAugmentedFileSystem
}

var _ uri.FileSystem = (*PyrightFileSystem)(nil)

func NewPyrightFileSystem(realFS uri.FileSystem) *PyrightFileSystem {
	fs := &PyrightFileSystem{ReadOnlyAugmentedFileSystem: NewReadOnlyAugmentedFileSystem(realFS)}
	// The base class calls back into the subclass through self; see the note on
	// ReadOnlyAugmentedFileSystem.
	fs.self = fs
	return fs
}

func (fs *PyrightFileSystem) MkdirSync(u uri.Uri, options uri.MkDirOptions) error {
	return fs.realFS.MkdirSync(u, options)
}

func (fs *PyrightFileSystem) Chdir(u uri.Uri) {
	fs.realFS.Chdir(u)
}

func (fs *PyrightFileSystem) WriteFileSync(u uri.Uri, data []byte) error {
	return fs.realFS.WriteFileSync(fs.GetOriginalUri(u), data)
}

func (fs *PyrightFileSystem) RmdirSync(u uri.Uri) error {
	return fs.realFS.RmdirSync(fs.GetOriginalUri(u))
}

func (fs *PyrightFileSystem) UnlinkSync(u uri.Uri) error {
	return fs.realFS.UnlinkSync(fs.GetOriginalUri(u))
}

func (fs *PyrightFileSystem) CopyFileSync(src uri.Uri, dst uri.Uri) error {
	return fs.realFS.CopyFileSync(fs.GetOriginalUri(src), fs.GetOriginalUri(dst))
}
