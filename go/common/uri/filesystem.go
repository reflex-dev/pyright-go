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
 * Layout note: the original is `common/fileSystem.ts`, one directory up from
 * `common/uri/`. It lives here instead because fileSystem.ts imports
 * uri/uri.ts and uri/uriUtils.ts imports fileSystem.ts -- a cycle TypeScript
 * does not mind and Go forbids between packages. It is the same situation
 * the analyzer package is in, and it gets the same answer:
 * the cycle collapses into one package rather than the port refactoring
 * pyright's architecture around it. Every member here is defined in terms of
 * Uri, so `uri` is where it goes.
 *
 * Four groups of members are deliberately dropped, all of them Node plumbing
 * that is out of scope for this port:
 *
 *   - the async readFile / readFileText pair. Nothing in the analyzer calls
 *     them; the language server does.
 *   - createFileSystemWatcher, and with it the whole fileWatcher module.
 *   - createReadStream / createWriteStream, which exist for the zip reader.
 *
 * mapDirectory and the mapped-uri members are kept. They look like plumbing but
 * are not: they are how a partial stub package gets merged onto the library it
 * augments, which is behaviour the import resolver's tests assert on directly.
 */

package uri

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
// The readers report errors rather than throwing, because every caller in the
// analyzer already wraps them in try/catch -- uriUtils' TryStat and
// TryRealpath exist for exactly that.
type ReadOnlyFileSystem interface {
	ExistsSync(u Uri) bool
	Chdir(u Uri)
	ReaddirEntriesSync(u Uri) ([]Dirent, error)
	ReaddirSync(u Uri) ([]string, error)
	ReadFileSync(u Uri) ([]byte, error)

	StatSync(u Uri) (Stats, error)
	RealpathSync(u Uri) (Uri, error)
	GetModulePath() Uri

	// RealCasePath returns the path in the casing the OS uses.
	RealCasePath(u Uri) Uri

	// IsMappedUri reports whether the file is mapped to another location.
	IsMappedUri(u Uri) bool

	// GetOriginalUri gets the original uri if the given uri is mapped.
	GetOriginalUri(mappedUri Uri) Uri

	// GetMappedUri gets the mapped uri if the given uri is mapped.
	GetMappedUri(originalUri Uri) Uri

	IsInZip(u Uri) bool
}

// Disposable corresponds to vscode-jsonrpc's interface of the same name, which
// MapDirectory returns to undo itself.
type Disposable interface {
	Dispose()
}

// DisposableFunc adapts a plain function, which is the shape every
// implementation in pyright uses.
type DisposableFunc func()

func (f DisposableFunc) Dispose() { f() }

// MapDirectoryFilter decides whether one mapped path is really mapped. It is
// optional in the original, where its absence means "everything".
type MapDirectoryFilter func(originalUri Uri, fs FileSystem) bool

// FileSystem corresponds to the interface of the same name.
type FileSystem interface {
	ReadOnlyFileSystem

	MkdirSync(u Uri, options MkDirOptions) error
	WriteFileSync(u Uri, data []byte) error

	UnlinkSync(u Uri) error
	RmdirSync(u Uri) error

	CopyFileSync(u Uri, dst Uri) error

	MapDirectory(mappedUri Uri, originalUri Uri, filter MapDirectoryFilter) Disposable
}

// TmpfileOptions corresponds to the interface of the same name.
type TmpfileOptions struct {
	Postfix string
	Prefix  string
}

// TempFile corresponds to the interface of the same name. The original notes
// that the directory Tmpdir returns must exist and be the same every call.
type TempFile interface {
	Tmpdir() Uri
	Tmpfile(options TmpfileOptions) Uri
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

// DirentMap is an insertion-ordered map of Dirents by name.
//
// ReadOnlyAugmentedFileSystem builds its directory listing in a `Map<string,
// fs.Dirent>` "to make sure we don't have duplicates", then returns
// `[...entries.values()]`. The order that produces is insertion order with
// later writes keeping their original position, which is what this preserves.
type DirentMap struct {
	order  []string
	byName map[string]Dirent
}

func NewDirentMap() *DirentMap {
	return &DirentMap{byName: map[string]Dirent{}}
}

func (m *DirentMap) Set(name string, entry Dirent) {
	if _, ok := m.byName[name]; !ok {
		m.order = append(m.order, name)
	}
	m.byName[name] = entry
}

func (m *DirentMap) Values() []Dirent {
	out := make([]Dirent, 0, len(m.order))
	for _, name := range m.order {
		out = append(out, m.byName[name])
	}
	return out
}
