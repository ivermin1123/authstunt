//go:build !windows

package secrets

import "os"

// restrictToOwner is a no-op on POSIX: the 0600 mode passed at creation
// already limits the file to its owner.
func restrictToOwner(_ string, _ *os.File) error { return nil }
