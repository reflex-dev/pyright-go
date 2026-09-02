/*
 * realfs.go
 *
 * A uri.FileSystem backed by the operating system.
 *
 * This is the "deliberate divergence" ANALYZER-PLAN.md sanctions for Stage C:
 *
 *   Port `Uri` *semantics* faithfully (case sensitivity, the `.key`
 *   normalization, root handling) because import resolution depends on them,
 *   but not `RealFileSystem`, the chokidar watchers, the background-thread
 *   machinery, or the service provider. Those are Node-isms with no bearing on
 *   results.
 *
 * So this is not a transliteration of common/realFileSystem.ts. That file is
 * 649 lines, and most of them are the zip/egg reader, the chokidar file
 * watchers, and the temp-file machinery -- none of which changes what the
 * analyzer computes. What is left is a straightforward mapping onto `os`, plus
 * two things that do matter and are reproduced:
 *
 *   - realCasePath, which the config path uses to normalize a project root. On
 *     a case-sensitive file system the original returns the path unchanged
 *     after a realpath; that is what happens here.
 *   - the mapped-uri members, which answer identity because nothing is mapped.
 *     A PyrightFileSystem wrapped around this is what does the mapping.
 */

package realfs

import (
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/microsoft/pyright/go/common/uri"
)

// FileSystem is a uri.FileSystem over the host's file system.
type FileSystem struct {
	// modulePath is what GetModulePath answers: the directory the bundled
	// typeshed sits beside. The original derives it from the running module's
	// location, which has no counterpart here, so it is supplied.
	modulePath uri.Uri

	// caseSensitive controls how a Uri built from a path answers
	// isCaseSensitive.
	caseSensitive bool
}

var _ uri.FileSystem = (*FileSystem)(nil)

func New(modulePath uri.Uri, caseSensitive bool) *FileSystem {
	return &FileSystem{modulePath: modulePath, caseSensitive: caseSensitive}
}

func (fs *FileSystem) uriOf(path string) uri.Uri {
	return uri.UriExFile(path, fs.caseSensitive, false)
}

func (fs *FileSystem) ExistsSync(u uri.Uri) bool {
	if u == nil || u.IsEmpty() {
		return false
	}
	_, err := os.Stat(u.GetFilePath())
	return err == nil
}

// Chdir is a no-op. The original changes the process's working directory; the
// port never reads it back through the file system, and changing it would
// affect the whole process rather than this file system.
func (fs *FileSystem) Chdir(u uri.Uri) {}

func (fs *FileSystem) ReaddirEntriesSync(u uri.Uri) ([]uri.Dirent, error) {
	entries, err := os.ReadDir(u.GetFilePath())
	if err != nil {
		return nil, err
	}

	// Node's readdir order is the file system's; sorting keeps runs
	// reproducible, and every caller that cares sorts anyway.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	parentPath := u.GetFilePath()
	out := make([]uri.Dirent, 0, len(entries))
	for _, entry := range entries {
		out = append(out, &dirent{name: entry.Name(), mode: entry.Type(), parentPath: parentPath})
	}
	return out, nil
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
	return os.ReadFile(u.GetFilePath())
}

func (fs *FileSystem) StatSync(u uri.Uri) (uri.Stats, error) {
	// os.Stat follows symbolic links, which is what Node's statSync does.
	info, err := os.Stat(u.GetFilePath())
	if err != nil {
		return nil, err
	}
	return &stats{info: info}, nil
}

func (fs *FileSystem) RealpathSync(u uri.Uri) (uri.Uri, error) {
	resolved, err := filepath.EvalSymlinks(u.GetFilePath())
	if err != nil {
		return nil, err
	}
	return fs.uriOf(resolved), nil
}

func (fs *FileSystem) GetModulePath() uri.Uri {
	if fs.modulePath == nil {
		return uri.Empty()
	}
	return fs.modulePath
}

// RealCasePath answers the path the file system actually uses.
//
// On a case-sensitive file system that is the path itself once symbolic links
// are resolved, which is what the original does there too. The original's
// Windows path, which walks the directory tree recovering each component's
// real casing, has no counterpart here and would be dead code on a
// case-sensitive host.
func (fs *FileSystem) RealCasePath(u uri.Uri) uri.Uri {
	resolved, err := filepath.EvalSymlinks(u.GetFilePath())
	if err != nil {
		// The original also answers the input unchanged when it cannot resolve.
		return u
	}
	return fs.uriOf(resolved)
}

func (fs *FileSystem) IsMappedUri(u uri.Uri) bool { return false }

func (fs *FileSystem) GetOriginalUri(mappedUri uri.Uri) uri.Uri { return mappedUri }

func (fs *FileSystem) GetMappedUri(originalUri uri.Uri) uri.Uri { return originalUri }

func (fs *FileSystem) IsInZip(u uri.Uri) bool { return false }

func (fs *FileSystem) MkdirSync(u uri.Uri, options uri.MkDirOptions) error {
	if options.Recursive {
		return os.MkdirAll(u.GetFilePath(), 0o755)
	}
	return os.Mkdir(u.GetFilePath(), 0o755)
}

func (fs *FileSystem) WriteFileSync(u uri.Uri, data []byte) error {
	return os.WriteFile(u.GetFilePath(), data, 0o644)
}

func (fs *FileSystem) UnlinkSync(u uri.Uri) error { return os.Remove(u.GetFilePath()) }

func (fs *FileSystem) RmdirSync(u uri.Uri) error { return os.Remove(u.GetFilePath()) }

func (fs *FileSystem) CopyFileSync(u uri.Uri, dst uri.Uri) error {
	data, err := os.ReadFile(u.GetFilePath())
	if err != nil {
		return err
	}
	return os.WriteFile(dst.GetFilePath(), data, 0o644)
}

// MapDirectory is not supported. The real file system cannot remap paths; a
// PyrightFileSystem wrapped around it is what does that, and it is where the
// partial-stub service installs its mappings.
func (fs *FileSystem) MapDirectory(mappedUri uri.Uri, originalUri uri.Uri, filter uri.MapDirectoryFilter) uri.Disposable {
	panic(errors.New("mapDirectory is not supported by the real file system"))
}

// dirent implements uri.Dirent over an os.DirEntry's type bits.
type dirent struct {
	name       string
	mode       os.FileMode
	parentPath string
}

func (d *dirent) Name() string       { return d.name }
func (d *dirent) ParentPath() string { return d.parentPath }
func (d *dirent) IsFile() bool       { return d.mode.Type() == 0 }
func (d *dirent) IsDirectory() bool  { return d.mode.IsDir() }
func (d *dirent) IsBlockDevice() bool {
	return d.mode&os.ModeDevice != 0 && d.mode&os.ModeCharDevice == 0
}
func (d *dirent) IsCharacterDevice() bool { return d.mode&os.ModeCharDevice != 0 }
func (d *dirent) IsSymbolicLink() bool    { return d.mode&os.ModeSymlink != 0 }
func (d *dirent) IsFIFO() bool            { return d.mode&os.ModeNamedPipe != 0 }
func (d *dirent) IsSocket() bool          { return d.mode&os.ModeSocket != 0 }

// stats implements uri.Stats over an os.FileInfo.
type stats struct{ info os.FileInfo }

func (s *stats) Size() int64      { return s.info.Size() }
func (s *stats) MtimeMs() float64 { return float64(s.info.ModTime().UnixMilli()) }

// CtimeMs answers the modification time. Go's portable FileInfo exposes no
// creation or inode-change time, and nothing in the analyzer reads this.
func (s *stats) CtimeMs() float64 { return float64(s.info.ModTime().UnixMilli()) }

func (s *stats) IsFile() bool      { return s.info.Mode().IsRegular() }
func (s *stats) IsDirectory() bool { return s.info.IsDir() }
func (s *stats) IsBlockDevice() bool {
	return s.info.Mode()&os.ModeDevice != 0 && s.info.Mode()&os.ModeCharDevice == 0
}
func (s *stats) IsCharacterDevice() bool { return s.info.Mode()&os.ModeCharDevice != 0 }
func (s *stats) IsSymbolicLink() bool    { return s.info.Mode()&os.ModeSymlink != 0 }
func (s *stats) IsFIFO() bool            { return s.info.Mode()&os.ModeNamedPipe != 0 }
func (s *stats) IsSocket() bool          { return s.info.Mode()&os.ModeSocket != 0 }
func (s *stats) IsZipDirectory() bool    { return false }
