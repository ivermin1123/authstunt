//go:build windows

package fsutil

// syncDir is a no-op on Windows.
//
// FlushFileBuffers, which os.File.Sync calls, rejects a directory handle,
// so there is no portable way to ask for the same guarantee here. NTFS
// journals its own metadata and orders a rename against it, which is the
// property the POSIX fsync is being used to obtain. Returning an error
// instead would make every write fail on Windows for no gain.
func syncDir(string) error { return nil }
