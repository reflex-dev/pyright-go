/*
 * vfs.go
 *
 * An in-memory file system, built from a snapshot of one taken elsewhere.
 *
 * This is NOT a transliteration of pyright's tests/harness/vfs/filesystem.ts.
 * That file is 2,053 lines of copy-on-write layering, shadow roots and mount
 * points, none of which the import resolver can see. What it can see is the
 * answers: which paths exist, which are files, which are directories, which are
 * symbolic links and where they point, and what the files contain. So the
 * bridge walks the TypeScript TestFileSystem once, records those answers, and
 * this replays them.
 *
 * That keeps the differential honest in the direction that matters. The file
 * system is the *input* to import resolution, so both sides must be given the
 * same input; how each one stores it is not under test. Transliterating the
 * harness would put 2,000 lines of untested code underneath the thing being
 * tested.
 *
 * Case sensitivity is carried through, because the resolver's behaviour depends
 * on it and TestFileSystem is constructed with an explicit ignoreCase flag.
 */

package vfs

import (
	"errors"
	"sort"
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// EntryKind is what a snapshot entry is.
type EntryKind string

const (
	EntryFile    EntryKind = "file"
	EntryDir     EntryKind = "dir"
	EntrySymlink EntryKind = "symlink"
)

// Entry is one path in a snapshot. Content is set for files; Target is the
// resolved path for symbolic links.
type Entry struct {
	Path    string    `json:"path"`
	Kind    EntryKind `json:"kind"`
	Content string    `json:"content"`
	Target  string    `json:"target"`
}

// Snapshot is the whole file system as the bridge ships it.
type Snapshot struct {
	IgnoreCase bool    `json:"ignoreCase"`
	Cwd        string  `json:"cwd"`
	ModulePath string  `json:"modulePath"`
	Entries    []Entry `json:"entries"`
}

// ErrNotFound stands in for Node's ENOENT, which the TypeScript file systems
// throw and every caller in the analyzer catches.
var ErrNotFound = errors.New("ENOENT: no such file or directory")

// ErrNotDirectory stands in for ENOTDIR.
var ErrNotDirectory = errors.New("ENOTDIR: not a directory")

type node struct {
	kind     EntryKind
	content  []byte
	target   string
	children map[string]*node // keyed by the canonical (case-folded if needed) name
	names    map[string]string
}

// FileSystem is the in-memory file system. It satisfies uri.FileSystem.
type FileSystem struct {
	ignoreCase bool
	cwd        string
	modulePath string
	root       *node
}

var _ uri.FileSystem = (*FileSystem)(nil)

// New builds a file system from a snapshot.
func New(snapshot Snapshot) *FileSystem {
	fs := &FileSystem{
		ignoreCase: snapshot.IgnoreCase,
		cwd:        snapshot.Cwd,
		modulePath: snapshot.ModulePath,
		root:       newDirNode(),
	}
	if fs.cwd == "" {
		fs.cwd = "/"
	}

	// Directories are created before their contents so that an entry list in
	// any order still produces the right tree.
	sorted := append([]Entry{}, snapshot.Entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i].Path) < len(sorted[j].Path) })

	for _, entry := range sorted {
		fs.add(entry)
	}
	return fs
}

func newDirNode() *node {
	return &node{kind: EntryDir, children: map[string]*node{}, names: map[string]string{}}
}

func (fs *FileSystem) canonical(name string) string {
	if fs.ignoreCase {
		return strings.ToLower(name)
	}
	return name
}

// splitPath breaks an absolute path into its components, dropping the root.
func splitPath(path string) []string {
	components := common.GetPathComponents(path)
	if len(components) > 0 {
		components = components[1:]
	}
	out := make([]string, 0, len(components))
	for _, component := range components {
		if component != "" {
			out = append(out, component)
		}
	}
	return out
}

func (fs *FileSystem) add(entry Entry) {
	components := splitPath(entry.Path)
	if len(components) == 0 {
		return
	}

	current := fs.root
	for _, component := range components[:len(components)-1] {
		current = fs.child(current, component, true)
	}

	name := components[len(components)-1]
	leaf := fs.child(current, name, entry.Kind == EntryDir)
	leaf.kind = entry.Kind
	leaf.content = []byte(entry.Content)
	leaf.target = entry.Target
	if entry.Kind == EntryDir && leaf.children == nil {
		leaf.children = map[string]*node{}
		leaf.names = map[string]string{}
	}
}

// child looks up or creates a child of dir.
func (fs *FileSystem) child(dir *node, name string, asDir bool) *node {
	key := fs.canonical(name)
	if existing, ok := dir.children[key]; ok {
		return existing
	}
	created := &node{kind: EntryFile}
	if asDir {
		created = newDirNode()
	}
	dir.children[key] = created
	dir.names[key] = name
	return created
}

// lookup walks to a path without following a trailing symbolic link, which is
// what Node's lstat does. Intermediate symbolic links are followed.
func (fs *FileSystem) lookup(path string) (*node, bool) {
	components := splitPath(path)
	current := fs.root

	for _, component := range components {
		if current.kind == EntrySymlink {
			resolved, ok := fs.lookup(current.target)
			if !ok {
				return nil, false
			}
			current = resolved
		}
		if current.children == nil {
			return nil, false
		}
		next, ok := current.children[fs.canonical(component)]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

// resolve walks to a path and follows a trailing symbolic link, which is what
// Node's stat does.
func (fs *FileSystem) resolve(path string) (*node, bool) {
	// A symlink chain is bounded by the snapshot, but guard anyway rather than
	// hanging on a cycle the harness could produce.
	for i := 0; i < 40; i++ {
		found, ok := fs.lookup(path)
		if !ok {
			return nil, false
		}
		if found.kind != EntrySymlink {
			return found, true
		}
		path = found.target
	}
	return nil, false
}

// realPath resolves every symbolic link on the way to path and returns the
// path it lands on, in the tree's own casing.
func (fs *FileSystem) realPath(path string) (string, bool) {
	components := splitPath(path)
	current := fs.root
	resolved := "/"

	for _, component := range components {
		if current.children == nil {
			return "", false
		}
		key := fs.canonical(component)
		next, ok := current.children[key]
		if !ok {
			return "", false
		}
		resolved = common.CombinePaths(resolved, current.names[key])
		if next.kind == EntrySymlink {
			target, ok := fs.realPath(next.target)
			if !ok {
				return "", false
			}
			resolved = target
			next, ok = fs.resolve(next.target)
			if !ok {
				return "", false
			}
		}
		current = next
	}
	return resolved, true
}

/*
 * uri.ReadOnlyFileSystem
 */

func (fs *FileSystem) ExistsSync(u uri.Uri) bool {
	_, ok := fs.resolve(u.GetFilePath())
	return ok
}

func (fs *FileSystem) Chdir(u uri.Uri) { fs.cwd = u.GetFilePath() }

func (fs *FileSystem) ReaddirEntriesSync(u uri.Uri) ([]uri.Dirent, error) {
	dir, ok := fs.resolve(u.GetFilePath())
	if !ok {
		return nil, ErrNotFound
	}
	if dir.kind != EntryDir {
		return nil, ErrNotDirectory
	}

	parentPath := u.GetFilePath()
	entries := make([]uri.Dirent, 0, len(dir.children))
	for key, child := range dir.children {
		entries = append(entries, &dirent{name: dir.names[key], kind: child.kind, parentPath: parentPath})
	}
	// Node's readdir order is the file system's; the harness sorts what it
	// needs sorted, so a stable order here just keeps runs reproducible.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func (fs *FileSystem) ReaddirSync(u uri.Uri) ([]string, error) {
	entries, err := fs.ReaddirEntriesSync(u)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, nil
}

func (fs *FileSystem) ReadFileSync(u uri.Uri) ([]byte, error) {
	found, ok := fs.resolve(u.GetFilePath())
	if !ok {
		return nil, ErrNotFound
	}
	if found.kind != EntryFile {
		return nil, errors.New("EISDIR: illegal operation on a directory")
	}
	return found.content, nil
}

func (fs *FileSystem) StatSync(u uri.Uri) (uri.Stats, error) {
	found, ok := fs.resolve(u.GetFilePath())
	if !ok {
		return nil, ErrNotFound
	}
	return &stats{kind: found.kind, size: int64(len(found.content))}, nil
}

func (fs *FileSystem) RealpathSync(u uri.Uri) (uri.Uri, error) {
	resolved, ok := fs.realPath(u.GetFilePath())
	if !ok {
		return nil, ErrNotFound
	}
	return uri.UriExFile(resolved, u.IsCaseSensitive(), false), nil
}

func (fs *FileSystem) GetModulePath() uri.Uri {
	if fs.modulePath == "" {
		return uri.Empty()
	}
	return uri.UriExFile(fs.modulePath, !fs.ignoreCase, false)
}

// RealCasePath answers in the tree's own casing, which is what the harness's
// implementation does.
func (fs *FileSystem) RealCasePath(u uri.Uri) uri.Uri {
	resolved, ok := fs.realPath(u.GetFilePath())
	if !ok {
		return u
	}
	return uri.UriExFile(resolved, u.IsCaseSensitive(), false)
}

func (fs *FileSystem) IsMappedUri(u uri.Uri) bool { return false }

func (fs *FileSystem) GetOriginalUri(mappedUri uri.Uri) uri.Uri { return mappedUri }

func (fs *FileSystem) GetMappedUri(originalUri uri.Uri) uri.Uri { return originalUri }

func (fs *FileSystem) IsInZip(u uri.Uri) bool { return false }

/*
 * uri.FileSystem
 */

func (fs *FileSystem) MkdirSync(u uri.Uri, options uri.MkDirOptions) error {
	path := u.GetFilePath()
	if !options.Recursive {
		parent, ok := fs.resolve(common.GetDirectoryPath(path))
		if !ok || parent.kind != EntryDir {
			return ErrNotFound
		}
	}
	fs.add(Entry{Path: path, Kind: EntryDir})
	return nil
}

func (fs *FileSystem) WriteFileSync(u uri.Uri, data []byte) error {
	fs.add(Entry{Path: u.GetFilePath(), Kind: EntryFile, Content: string(data)})
	return nil
}

func (fs *FileSystem) UnlinkSync(u uri.Uri) error { return fs.remove(u.GetFilePath()) }

func (fs *FileSystem) RmdirSync(u uri.Uri) error { return fs.remove(u.GetFilePath()) }

func (fs *FileSystem) CopyFileSync(u uri.Uri, dst uri.Uri) error {
	data, err := fs.ReadFileSync(u)
	if err != nil {
		return err
	}
	return fs.WriteFileSync(dst, data)
}

func (fs *FileSystem) remove(path string) error {
	components := splitPath(path)
	if len(components) == 0 {
		return ErrNotFound
	}
	parent, ok := fs.resolve(common.GetDirectoryPath(path))
	if !ok || parent.children == nil {
		return ErrNotFound
	}
	key := fs.canonical(components[len(components)-1])
	if _, ok := parent.children[key]; !ok {
		return ErrNotFound
	}
	delete(parent.children, key)
	delete(parent.names, key)
	return nil
}

// dirent implements uri.Dirent.
type dirent struct {
	name       string
	kind       EntryKind
	parentPath string
}

func (d *dirent) Name() string            { return d.name }
func (d *dirent) ParentPath() string      { return d.parentPath }
func (d *dirent) IsFile() bool            { return d.kind == EntryFile }
func (d *dirent) IsDirectory() bool       { return d.kind == EntryDir }
func (d *dirent) IsBlockDevice() bool     { return false }
func (d *dirent) IsCharacterDevice() bool { return false }
func (d *dirent) IsSymbolicLink() bool    { return d.kind == EntrySymlink }
func (d *dirent) IsFIFO() bool            { return false }
func (d *dirent) IsSocket() bool          { return false }

// stats implements uri.Stats.
type stats struct {
	kind EntryKind
	size int64
}

func (s *stats) Size() int64             { return s.size }
func (s *stats) MtimeMs() float64        { return 0 }
func (s *stats) CtimeMs() float64        { return 0 }
func (s *stats) IsFile() bool            { return s.kind == EntryFile }
func (s *stats) IsDirectory() bool       { return s.kind == EntryDir }
func (s *stats) IsBlockDevice() bool     { return false }
func (s *stats) IsCharacterDevice() bool { return false }
func (s *stats) IsSymbolicLink() bool    { return s.kind == EntrySymlink }
func (s *stats) IsFIFO() bool            { return false }
func (s *stats) IsSocket() bool          { return false }
func (s *stats) IsZipDirectory() bool    { return false }
