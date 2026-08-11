package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func skipOnWindows(t *testing.T, why string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip(why)
	}
}

func TestCreateKeySyncsFileAndParentDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	var syncedFiles, syncedDirs int
	restore := setSyncHooksForTest(
		func(f *os.File) error { syncedFiles++; return f.Sync() },
		func(string) error { syncedDirs++; return nil },
	)
	defer restore()

	if _, err := LoadOrCreateKey(dir, "p1"); err != nil {
		t.Fatal(err)
	}
	// Everything sealed afterwards is unrecoverable without this key, so
	// both the contents and the directory entry naming them have to be on
	// disk before the store starts writing sealed rows.
	if syncedFiles != 1 {
		t.Errorf("key file synced %d times, want 1", syncedFiles)
	}
	if syncedDirs != 1 {
		t.Errorf("keys directory synced %d times, want 1", syncedDirs)
	}

	// A load that finds an existing key writes nothing, so it syncs
	// nothing either.
	syncedFiles, syncedDirs = 0, 0
	if _, err := LoadOrCreateKey(dir, "p1"); err != nil {
		t.Fatal(err)
	}
	if syncedFiles != 0 || syncedDirs != 0 {
		t.Errorf("a read-only load synced %d files and %d dirs, want 0 and 0", syncedFiles, syncedDirs)
	}
}

func TestCreateKeyFailsWhenSyncFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	restore := setSyncHooksForTest(
		func(*os.File) error { return errors.New("disk is gone") },
		func(string) error { return nil },
	)
	defer restore()

	if _, err := LoadOrCreateKey(dir, "p1"); err == nil {
		t.Fatal("a key that never reached disk was reported as created")
	}
	// The half-created file is removed, so the next attempt is a clean
	// creation rather than a load of a zero-length key.
	if _, err := os.Lstat(filepath.Join(dir, "p1.key")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("failed creation left a key file behind: %v", err)
	}
}

func TestKeysDirectoryModeNarrowedWhenExisting(t *testing.T) {
	skipOnWindows(t, "POSIX mode bits; os.Chmod only toggles read-only on Windows")
	dir := filepath.Join(t.TempDir(), "keys")
	// MkdirAll applies its mode only to directories it creates, so a keys
	// directory that already exists keeps whatever mode it had.
	// nolint:gosec // G301: the too-permissive mode is the fixture. This
	// test exists to prove LoadOrCreateKey narrows a directory it did not
	// create.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(dir, "p1"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("keys dir mode %o, want 700", perm)
	}
}

func TestLoadOrCreateKeyRejectsSymlink(t *testing.T) {
	skipOnWindows(t, "creating a symlink needs elevation on Windows")
	dir := t.TempDir()
	original, err := LoadOrCreateKey(dir, "real")
	if err != nil {
		t.Fatal(err)
	}
	// A link planted in the keys directory would otherwise decide which
	// file is read as the key, and later which file is written.
	if err := os.Symlink(filepath.Join(dir, "real.key"), filepath.Join(dir, "linked.key")); err != nil {
		t.Fatal(err)
	}
	got, err := LoadOrCreateKey(dir, "linked")
	if !errors.Is(err, ErrUnsafeKeyFile) {
		t.Fatalf("symlinked key returned (%v, %v), want ErrUnsafeKeyFile", got, err)
	}
	if got != nil && got.ID == original.ID {
		t.Fatal("the symlink was followed")
	}
}

func TestLoadOrCreateKeyRejectsNonRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "weird.key")
	// A directory standing where the key file belongs is the portable
	// non-regular file. Reading one yields no key material on any
	// platform, and the point is that the type is checked rather than the
	// read being allowed to fail in its own way.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(dir, "weird"); !errors.Is(err, ErrUnsafeKeyFile) {
		t.Fatalf("non-regular key returned %v, want ErrUnsafeKeyFile", err)
	}

	// The same check has to cover the O_EXCL race path: when another
	// process wins the creation, the file this one rereads gets the same
	// validation as any other existing key.
	if _, err := readKeyFile(path); !errors.Is(err, ErrUnsafeKeyFile) {
		t.Fatalf("the reread path accepted a non-regular file: %v", err)
	}
	if _, err := createKey(path, dir); !errors.Is(err, ErrUnsafeKeyFile) {
		t.Fatalf("the O_EXCL loser accepted a non-regular file: %v", err)
	}
}

func TestLoadOrCreateKeyRejectsUnsafePermissions(t *testing.T) {
	skipOnWindows(t, "POSIX mode bits; the Windows check is on the DACL")
	dir := t.TempDir()
	if _, err := LoadOrCreateKey(dir, "p1"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "p1.key")
	// nolint:gosec // G302: the unsafe mode is the fixture. This test
	// exists to prove an exposed key file is refused rather than repaired.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadOrCreateKey(dir, "p1")
	if !errors.Is(err, ErrUnsafeKeyFile) {
		t.Fatalf("world-readable key returned %v, want ErrUnsafeKeyFile", err)
	}
	// No auto-repair. The key was readable for as long as the mode said so
	// and chmod cannot take that back; narrowing it here would hide the
	// exposure and keep using the key.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("the key file mode was repaired to %o; refusing must not rewrite it", perm)
	}
}

func TestLoadOrCreateKeyConcurrentCreation(t *testing.T) {
	dir := t.TempDir()
	const racers = 8
	keys := make([]*Key, racers)
	errs := make([]error, racers)
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for i := range racers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			keys[i], errs[i] = LoadOrCreateKey(dir, "raced")
		}()
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v", i, err)
		}
	}
	// Exactly one key exists: O_EXCL picks the creator and every loser
	// rereads what the winner wrote. A racer that overwrote the file would
	// have made every row sealed before it unreadable.
	for i, k := range keys {
		if k.ID != keys[0].ID || k.material != keys[0].material {
			t.Fatalf("racer %d got a different key", i)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("keys dir holds %d entries, want 1", len(entries))
	}
}

func TestPrepareKeysDirRejectsSymlink(t *testing.T) {
	skipOnWindows(t, "creating a symlink needs elevation on Windows")
	base := t.TempDir()
	target := filepath.Join(base, "elsewhere")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "keys")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(link, "p1"); !errors.Is(err, ErrUnsafeKeyFile) {
		t.Fatalf("symlinked keys dir returned %v, want ErrUnsafeKeyFile", err)
	}
}

// setSyncHooksForTest swaps the two durability calls the creation path
// makes and returns a function that puts the real ones back. An fsync
// leaves nothing behind to assert on, so it is observed here instead.
func setSyncHooksForTest(file func(*os.File) error, dir func(string) error) func() {
	previousFile, previousDir := syncFile, syncDir
	syncFile, syncDir = file, dir
	return func() { syncFile, syncDir = previousFile, previousDir }
}
