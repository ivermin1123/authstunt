// Package fsutil holds the filesystem durability helpers shared by the
// store and the key manager. Both publish a file by renaming a temporary
// one over its final name, and both need the directory entry itself on
// disk before the operation counts as done.
package fsutil

// SyncDir flushes a directory's own entries to disk.
//
// Syncing a file only guarantees its contents. The name that points at
// those contents lives in the parent directory, so a crash after
// rename(2) but before the directory metadata reaches the platter can
// leave a fully written blob under no name at all, or under its temporary
// one. Callers pass the directory that received the rename or the create.
func SyncDir(dir string) error { return syncDir(dir) }
