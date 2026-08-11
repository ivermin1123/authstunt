//go:build !windows

package secrets

import (
	"fmt"
	"io/fs"
	"os"
)

// restrictToOwner is a no-op on POSIX: the 0600 mode passed at creation
// already limits the file to its owner.
func restrictToOwner(_ string, _ *os.File) error { return nil }

// narrowDir tightens an existing keys directory to 0700.
//
// The directory holds no secret itself, so narrowing it silently loses
// nothing: what the mode buys is that nobody else can traverse into the
// key files, whatever mode those ended up with.
func narrowDir(path string, info fs.FileInfo) error {
	if info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	// nolint:gosec // G302 wants 0600 or less, which for a directory drops
	// the execute bit and makes it untraversable. 0700 is the tightest
	// workable mode for a directory.
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secrets: restrict keys dir: %w", err)
	}
	return nil
}

// checkKeyFileAccess refuses a key file that anyone but its owner can
// reach.
//
// It does not repair the mode. A key that group or world could read was
// exposed for as long as that was true, and chmod does not un-expose it:
// the only safe answer is rotation, which is the operator's call. Any
// proposal to auto-repair here goes back to the owner first.
func checkKeyFileAccess(path string, info fs.FileInfo) error {
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%w: %s is mode %#o, want 0600: rotate it (delete the key and the data sealed under it) rather than relaxing this check",
			ErrUnsafeKeyFile, path, perm)
	}
	return nil
}
