package realfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/pyright/go/common/uri"
)

// TestRealCasePathDoesNotFollowSymlinks pins the guard that the original's
// realCasePath applies and that this port once dropped.
//
// It needs the real file system, which is the whole point: neither vfs nor the
// reference's TestFileSystem models symbolic links, so every corpus
// differential and every bridged test suite passed while a virtualenv's
// bin/python was silently normalizing to the system interpreter. See
// analyzer/STATUS-STAGE-D.md.
func TestRealCasePathDoesNotFollowSymlinks(t *testing.T) {
	dir := t.TempDir()

	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	fs := New(uri.UriExFile(dir, true, false), true)

	got := fs.RealCasePath(uri.UriExFile(link, true, false)).GetFilePath()
	if got != link {
		t.Errorf("RealCasePath followed the symlink:\n  got  %s\n  want %s", got, link)
	}
}

// TestRealCasePathResolvesNonSymlink covers the ordinary case: a path that is
// already real comes back unchanged rather than being rejected by the guard.
func TestRealCasePathResolvesNonSymlink(t *testing.T) {
	dir := t.TempDir()

	file := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs := New(uri.UriExFile(dir, true, false), true)

	got := fs.RealCasePath(uri.UriExFile(file, true, false)).GetFilePath()
	if got != file {
		t.Errorf("RealCasePath changed a real path:\n  got  %s\n  want %s", got, file)
	}
}

// TestRealCasePathMissingFile covers the original's first branch: a path that
// does not exist is answered unchanged without consulting the file system.
func TestRealCasePathMissingFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.txt")

	fs := New(uri.UriExFile(dir, true, false), true)

	got := fs.RealCasePath(uri.UriExFile(missing, true, false)).GetFilePath()
	if got != missing {
		t.Errorf("RealCasePath changed a missing path:\n  got  %s\n  want %s", got, missing)
	}
}
