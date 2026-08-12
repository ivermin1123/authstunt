package smtp

import (
	"errors"
	"fmt"
	"io/fs"
	"syscall"
	"testing"
)

// The disk-full mapping is the one storage failure with its own SMTP
// reply, and it is the one a manual smoke test never reaches: filling a
// disk on purpose is not something anybody does by hand. So it is pinned
// here, at the only level where the error values can be constructed
// directly.

func TestOutOfSpaceIsRecognizedThroughWrapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"bare errno", syscall.ENOSPC},
		{"wrapped once", fmt.Errorf("write blob: %w", syscall.ENOSPC)},
		{"wrapped twice", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", syscall.ENOSPC))},
		// The shape the standard library actually returns from a failed
		// write, rather than the bare errno a test would reach for first.
		{"os.PathError", &fs.PathError{Op: "write", Path: "/data/blobs/x", Err: syscall.ENOSPC}},
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

func TestOtherFailuresAreNotOutOfSpace(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"plain error", errors.New("database is locked")},
		{"a different errno", syscall.EACCES},
		{"not found", fs.ErrNotExist},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if isOutOfSpace(tc.err) {
				t.Errorf("isOutOfSpace(%v) = true, want false", tc.err)
			}
		})
	}
	// A non-space failure still refuses the message; it just refuses it
	// with the other code.
	if !errors.Is(classifyWrite(errors.New("database is locked")), ErrTemporary) {
		t.Error("an unclassified write failure was not temporary")
	}
}
