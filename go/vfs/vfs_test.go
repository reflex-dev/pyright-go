/*
 * vfs_test.go
 *
 * The in-memory file system is the one piece of the common layer that is not a
 * transliteration of anything -- it is harness code, so nothing upstream
 * validates it. These tests cover the three behaviours the import resolver
 * actually depends on and that a snapshot could plausibly get wrong: symbolic
 * links, case sensitivity, and the file/directory distinction in directory
 * listings.
 */

package vfs

import (
	"testing"

	"github.com/microsoft/pyright/go/common/uri"
)

func file(path string) uri.Uri { return uri.UriExFile(path, true, false) }

func newTestFS(ignoreCase bool, entries ...Entry) *FileSystem {
	return New(Snapshot{IgnoreCase: ignoreCase, Cwd: "/", Entries: entries})
}

func TestFilesAndDirectories(t *testing.T) {
	fs := newTestFS(false,
		Entry{Path: "/lib/site-packages/myLib/__init__.py", Kind: EntryFile, Content: "x = 1"},
		Entry{Path: "/lib/site-packages/myLib/mod.py", Kind: EntryFile},
	)

	if !fs.ExistsSync(file("/lib/site-packages/myLib/__init__.py")) {
		t.Fatal("expected the file to exist")
	}
	if !fs.ExistsSync(file("/lib/site-packages/myLib")) {
		t.Fatal("expected the intermediate directory to exist")
	}
	if fs.ExistsSync(file("/lib/site-packages/other")) {
		t.Fatal("did not expect a path that was never added to exist")
	}

	stats, err := fs.StatSync(file("/lib/site-packages/myLib"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !stats.IsDirectory() || stats.IsFile() {
		t.Fatal("expected a directory")
	}

	content, err := fs.ReadFileSync(file("/lib/site-packages/myLib/__init__.py"))
	if err != nil || string(content) != "x = 1" {
		t.Fatalf("readFile: %q %v", content, err)
	}

	entries, err := fs.ReaddirEntriesSync(file("/lib/site-packages/myLib"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, entry := range entries {
		if !entry.IsFile() {
			t.Fatalf("expected %s to be a file", entry.Name())
		}
	}

	if _, err := fs.ReaddirEntriesSync(file("/nope")); err != ErrNotFound {
		t.Fatalf("expected ENOENT for a missing directory, got %v", err)
	}
}

// Symbolic links are how importResolver.test.ts sets up its "symlinked partial
// stub" cases, and importResolverFileSystem branches on isSymbolicLink().
func TestSymbolicLinks(t *testing.T) {
	fs := newTestFS(false,
		Entry{Path: "/wheel/partialStub.pyi", Kind: EntryFile, Content: "def test(): ..."},
		Entry{Path: "/lib/myLib-stubs/partialStub.pyi", Kind: EntrySymlink, Target: "/wheel/partialStub.pyi"},
	)

	// stat follows the link; the directory entry does not.
	stats, err := fs.StatSync(file("/lib/myLib-stubs/partialStub.pyi"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !stats.IsFile() {
		t.Fatal("expected stat to follow the link to a file")
	}

	entries, err := fs.ReaddirEntriesSync(file("/lib/myLib-stubs"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || !entries[0].IsSymbolicLink() || entries[0].IsFile() {
		t.Fatal("expected the directory entry to report a symbolic link")
	}

	content, err := fs.ReadFileSync(file("/lib/myLib-stubs/partialStub.pyi"))
	if err != nil || string(content) != "def test(): ..." {
		t.Fatalf("readFile through the link: %q %v", content, err)
	}

	real, err := fs.RealpathSync(file("/lib/myLib-stubs/partialStub.pyi"))
	if err != nil {
		t.Fatalf("realpath: %v", err)
	}
	if real.GetFilePath() != "/wheel/partialStub.pyi" {
		t.Fatalf("realpath answered %q", real.GetFilePath())
	}
}

// A link through a directory, which realPath has to resolve mid-walk.
func TestSymbolicLinkToDirectory(t *testing.T) {
	fs := newTestFS(false,
		Entry{Path: "/real/pkg/mod.py", Kind: EntryFile},
		Entry{Path: "/link", Kind: EntrySymlink, Target: "/real"},
	)

	if !fs.ExistsSync(file("/link/pkg/mod.py")) {
		t.Fatal("expected to reach a file through a directory link")
	}
	real, err := fs.RealpathSync(file("/link/pkg/mod.py"))
	if err != nil {
		t.Fatalf("realpath: %v", err)
	}
	if real.GetFilePath() != "/real/pkg/mod.py" {
		t.Fatalf("realpath answered %q", real.GetFilePath())
	}
}

func TestCaseSensitivity(t *testing.T) {
	sensitive := newTestFS(false, Entry{Path: "/lib/MyLib/mod.py", Kind: EntryFile})
	if sensitive.ExistsSync(file("/lib/mylib/mod.py")) {
		t.Fatal("a case-sensitive file system should not match a differently-cased path")
	}

	insensitive := newTestFS(true, Entry{Path: "/lib/MyLib/mod.py", Kind: EntryFile})
	if !insensitive.ExistsSync(file("/lib/mylib/mod.py")) {
		t.Fatal("a case-insensitive file system should match a differently-cased path")
	}

	// realCasePath answers in the tree's own casing, not the caller's.
	real := insensitive.RealCasePath(file("/lib/mylib/mod.py"))
	if real.GetFilePath() != "/lib/MyLib/mod.py" {
		t.Fatalf("realCasePath answered %q", real.GetFilePath())
	}
}

func TestMutators(t *testing.T) {
	fs := newTestFS(false)

	if err := fs.MkdirSync(file("/a/b"), uri.MkDirOptions{Recursive: true}); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if !fs.ExistsSync(file("/a/b")) {
		t.Fatal("expected the created directory to exist")
	}

	if err := fs.WriteFileSync(file("/a/b/c.py"), []byte("y = 2")); err != nil {
		t.Fatalf("write: %v", err)
	}
	content, err := fs.ReadFileSync(file("/a/b/c.py"))
	if err != nil || string(content) != "y = 2" {
		t.Fatalf("readFile: %q %v", content, err)
	}

	if err := fs.UnlinkSync(file("/a/b/c.py")); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if fs.ExistsSync(file("/a/b/c.py")) {
		t.Fatal("expected the unlinked file to be gone")
	}

	// mkdir without recursive fails when the parent is missing, as Node's does.
	if err := fs.MkdirSync(file("/x/y"), uri.MkDirOptions{}); err != ErrNotFound {
		t.Fatalf("expected ENOENT for a missing parent, got %v", err)
	}
}
