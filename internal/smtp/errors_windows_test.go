//go:build windows

package smtp

import (
	"errors"
	"fmt"
	"io/fs"
	"syscall"
	"testing"
)

// Windows reports a full disk as ERROR_DISK_FULL (112) or
// ERROR_HANDLE_DISK_FULL (39). Neither maps onto ENOSPC, so without the
// numeric check a Windows server would answer 451 for a condition that has
// its own reply code, and an operator would go looking for a database
// problem that is really a full volume.
//
// This runs only on Windows because the mapping is guarded by GOOS: the
// same errno values mean something else elsewhere, which is the reason the
// guard exists and is asserted from the other side in
// TestWindowsDiskFullCodesAreIgnoredElsewhere.
func TestWindowsDiskFullCodesAreOutOfSpace(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"ERROR_DISK_FULL bare", syscall.Errno(112)},
		{"ERROR_HANDLE_DISK_FULL bare", syscall.Errno(39)},
		{"ERROR_DISK_FULL wrapped", fmt.Errorf("write blob: %w", syscall.Errno(112))},
		{"ERROR_DISK_FULL as a PathError", &fs.PathError{
			Op: "write", Path: `C:\data\blobs\x`, Err: syscall.Errno(112),
		}},
		// ENOSPC still has to work here too: the check is additive, not a
		// replacement.
		{"ENOSPC", syscall.ENOSPC},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !isOutOfSpace(tc.err) {
				t.Errorf("isOutOfSpace(%v) = false, want true", tc.err)
			}
			if !errors.Is(classifyWrite(tc.err), ErrOutOfSpace) {
				t.Errorf("classifyWrite(%v) did not classify as out of space", tc.err)
			}
		})
	}
}

// TestUnrelatedWindowsErrnoIsNotOutOfSpace keeps the numeric check narrow.
// ERROR_ACCESS_DENIED is 5 and must stay a generic temporary failure.
func TestUnrelatedWindowsErrnoIsNotOutOfSpace(t *testing.T) {
	if isOutOfSpace(syscall.Errno(5)) {
		t.Error("ERROR_ACCESS_DENIED was classified as a full disk")
	}
}
