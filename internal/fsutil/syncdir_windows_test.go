//go:build windows

package fsutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ivermin1123/authstunt/internal/fsutil"
)

// TestSyncDirIsANoOpOnWindows pins the deliberate difference between the
// platforms.
//
// FlushFileBuffers rejects a directory handle, so the POSIX guarantee is
// not available here and NTFS journaling is what stands in for it. The
// consequence that matters is that SyncDir must never fail: every blob
// publish and every key file creation calls it, and returning an error
// would break all of them on Windows in exchange for nothing.
func TestSyncDirIsANoOpOnWindows(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "published"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := fsutil.SyncDir(dir); err != nil {
		t.Fatalf("SyncDir on a real directory: %v", err)
	}
}

// TestSyncDirOnWindowsIgnoresAMissingDirectory is the same claim stated
// where it is easiest to get wrong. A future implementation that starts
// opening the directory would fail here first, which is the point: the
// no-op is a decision, not an accident, and changing it should break a
// test rather than a user's write path.
func TestSyncDirOnWindowsIgnoresAMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-created")
	if err := fsutil.SyncDir(missing); err != nil {
		t.Errorf("SyncDir reported %v: on Windows it is a no-op and never fails", err)
	}
	if err := fsutil.SyncDir(""); err != nil {
		t.Errorf("SyncDir(\"\") reported %v: on Windows it is a no-op and never fails", err)
	}
}
