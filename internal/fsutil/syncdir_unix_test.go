//go:build !windows

package fsutil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivermin1123/authstunt/internal/fsutil"
)

// Only the ENOENT shape of the open failure is covered. An unreadable
// directory reaches the same os.Open error branch by a different errno, and
// restoring its mode so the temp dir can be cleaned up needs a chmod that
// gosec flags. A lint suppression is not worth a second path through code
// that is already proven.

// TestSyncDirFlushesARealDirectory is the path every published blob and
// every created key file takes. It is one line of assertion because the
// guarantee it buys is invisible from inside the process: what is being
// checked is that the call succeeds on an ordinary directory rather than
// erroring and failing a write that was otherwise fine.
func TestSyncDirFlushesARealDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "published"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := fsutil.SyncDir(dir); err != nil {
		t.Fatalf("SyncDir on a real directory: %v", err)
	}
}

// TestSyncDirIsRepeatable matters because callers sync the same data
// directory after every publish, not once.
func TestSyncDirIsRepeatable(t *testing.T) {
	dir := t.TempDir()
	for range 3 {
		if err := fsutil.SyncDir(dir); err != nil {
			t.Fatalf("SyncDir: %v", err)
		}
	}
}

// TestSyncDirReportsAMissingDirectory pins the error path, and pins that
// the message names the directory. A durability helper that fails silently
// or anonymously is worse than one that does not exist, because the caller
// believes the rename is on disk.
func TestSyncDirReportsAMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-created")
	err := fsutil.SyncDir(missing)
	if err == nil {
		t.Fatal("SyncDir on a missing directory returned nil")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error %q does not name the directory it failed on", err)
	}
	if !os.IsNotExist(err) && !strings.Contains(err.Error(), "no such file") {
		t.Errorf("error %q does not report what went wrong", err)
	}
}
