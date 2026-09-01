/*
 * filesystem.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * A "file system provider" abstraction that allows us to swap out a real file
 * system implementation for a virtual (mocked) implementation for testing.
 *
 * Transliterated from common/fileSystem.ts (pyright 1.1.412).
 *
 * The Uri type this is written against lives in common/uri, which imports this
 * package -- so the interfaces here take a `UriLike`, the subset of the Uri
 * interface a file system actually uses. Every uri.Uri satisfies it. This is
 * the one place the port has to break a TypeScript import cycle rather than
 * absorbing it into a single package, because common and common/uri cannot
 * merge without dragging the parser in with them.
 *
 * Four groups of members are deliberately dropped, all of them Node plumbing
 * that ANALYZER-PLAN.md puts out of scope:
 *
 *   - the async readFile / readFileText pair. Nothing in the analyzer calls
 *     them; the language server does.
 *   - createFileSystemWatcher, and with it the whole fileWatcher module.
 *   - createReadStream / createWriteStream, which exist for the zip reader.
 *   - mapDirectory and the Disposable it returns, which is the partial-stub
 *     service.
 *
 * The mapped-uri members (isMappedUri, getOriginalUri, getMappedUri) are kept:
 * the import resolver calls them, and a file system that maps nothing can
 * answer them trivially.
 */

package common

// UriLike is the part of the Uri interface a file system needs. See the header
// for why this is not simply uri.Uri.
type UriLike interface {
	Key() string
	FileName() string
	GetFilePath() string
	GetDirectory() UriLike
	CombinePaths(paths ...string) UriLike
	Equals(other UriLike) bool
	IsEmpty() bool
	String() string
}

// Stats corresponds to the interface of the same name, which is Node's fs.Stats
// narrowed to what pyright reads.
//
// IsZipDirectory is optional in the original (`isZipDirectory?: () => boolean`)
// and only the zip-aware file system defines it. Callers spell the absence as
// `stats?.isZipDirectory?.() ?? false`, so a plain false here is the same
// answer.
type Stats interface {
	Size() int64
	MtimeMs() float64
	CtimeMs() float64

	IsFile() bool
	IsDirectory() bool
	IsBlockDevice() bool
	IsCharacterDevice() bool
	IsSymbolicLink() bool
	IsFIFO() bool
	IsSocket() bool
	IsZipDirectory() bool
}

// Dirent corresponds to Node's fs.Dirent as pyright uses it.
type Dirent interface {
	Name() string
	ParentPath() string

	IsFile() bool
	IsDirectory() bool
	IsBlockDevice() bool
	IsCharacterDevice() bool
	IsSymbolicLink() bool
	IsFIFO() bool
	IsSocket() bool
}

// MkDirOptions corresponds to the interface of the same name. The original
// notes that `mode` is not supported on Windows and leaves it commented out.
type MkDirOptions struct {
	Recursive bool
}

// ReadOnlyFileSystem corresponds to the interface of the same name.
//
// The three sync readers report errors rather than throwing, because every
// caller in the analyzer already wraps them in try/catch -- see uriUtils'
// tryStat and tryRealpath, which exist for exactly that.
type ReadOnlyFileSystem interface {
	ExistsSync(u UriLike) bool
	Chdir(u UriLike)
	ReaddirEntriesSync(u UriLike) ([]Dirent, error)
	ReaddirSync(u UriLike) ([]string, error)
	ReadFileSync(u UriLike) ([]byte, error)

	StatSync(u UriLike) (Stats, error)
	RealpathSync(u UriLike) (UriLike, error)
	GetModulePath() UriLike

	// RealCasePath returns the path in the casing the OS uses.
	RealCasePath(u UriLike) UriLike

	// IsMappedUri reports whether the file is mapped to another location.
	IsMappedUri(u UriLike) bool

	// GetOriginalUri gets the original uri if the given uri is mapped.
	GetOriginalUri(mappedUri UriLike) UriLike

	// GetMappedUri gets the mapped uri if the given uri is mapped.
	GetMappedUri(originalUri UriLike) UriLike

	IsInZip(u UriLike) bool
}

// FileSystem corresponds to the interface of the same name.
type FileSystem interface {
	ReadOnlyFileSystem

	MkdirSync(u UriLike, options MkDirOptions) error
	WriteFileSync(u UriLike, data []byte) error

	UnlinkSync(u UriLike) error
	RmdirSync(u UriLike) error

	CopyFileSync(u UriLike, dst UriLike) error
}

// TmpfileOptions corresponds to the interface of the same name.
type TmpfileOptions struct {
	Postfix string
	Prefix  string
}

// TempFile corresponds to the interface of the same name. The original notes
// that the directory Tmpdir returns must exist and be the same every call.
type TempFile interface {
	Tmpdir() UriLike
	Tmpfile(options TmpfileOptions) UriLike
}

// VirtualDirent corresponds to the class of the same name: a Dirent that is not
// backed by a real directory entry.
type VirtualDirent struct {
	name       string
	file       bool
	parentPath string
}

func NewVirtualDirent(name string, file bool, parentPath string) *VirtualDirent {
	return &VirtualDirent{name: name, file: file, parentPath: parentPath}
}

func (d *VirtualDirent) Name() string { return d.name }

// ParentPath is also exposed as `path` in the original, which is deprecated
// since Node v20.12.
func (d *VirtualDirent) ParentPath() string { return d.parentPath }

func (d *VirtualDirent) IsFile() bool            { return d.file }
func (d *VirtualDirent) IsDirectory() bool       { return !d.file }
func (d *VirtualDirent) IsBlockDevice() bool     { return false }
func (d *VirtualDirent) IsCharacterDevice() bool { return false }
func (d *VirtualDirent) IsSymbolicLink() bool    { return false }
func (d *VirtualDirent) IsFIFO() bool            { return false }
func (d *VirtualDirent) IsSocket() bool          { return false }
