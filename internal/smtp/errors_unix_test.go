//go:build !windows

package smtp

import (
	"syscall"
	"testing"
)

// TestWindowsDiskFullCodesAreIgnoredElsewhere is the other half of the
// GOOS guard in isOutOfSpace.
//
// 39 and 112 are ERROR_HANDLE_DISK_FULL and ERROR_DISK_FULL on Windows and
// something entirely unrelated on every other platform - ENOTEMPTY and
// EHOSTDOWN among them, depending on the system. Matching them
// unconditionally would answer 452 "insufficient system storage" to a
// caller whose real problem was a non-empty directory, so the guard is
// load-bearing and is pinned from both sides.
func TestWindowsDiskFullCodesAreIgnoredElsewhere(t *testing.T) {
	for _, code := range []syscall.Errno{39, 112} {
		if isOutOfSpace(code) {
			t.Errorf("errno %d was read as a full disk outside Windows", code)
		}
	}
}
