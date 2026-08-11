//go:build !windows

package fsutil

import (
	"fmt"
	"os"
)

func syncDir(dir string) error {
	// nolint:gosec // G304: opening a caller-named directory is the entire
	// function. The path comes from the store's own data directory layout,
	// never from a request, and the handle is only ever fsynced.
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("fsutil: open dir %s: %w", dir, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsutil: sync dir %s: %w", dir, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("fsutil: close dir %s: %w", dir, err)
	}
	return nil
}
