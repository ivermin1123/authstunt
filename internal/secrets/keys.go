package secrets

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/ivermin1123/authstunt/internal/fsutil"
)

const keySize = 32

// safeProjectID keeps a project identifier from steering the key path
// somewhere else: it reaches this package from authstunt.yaml, and a
// value like "../../id_rsa" would otherwise pick the file to read.
var safeProjectID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ErrUnsafeKeyFile is returned when the key file exists but its ownership,
// type, or permissions cannot be trusted.
//
// Nothing repairs it. A key that has been readable by other users was
// exposed while it was readable, and chmod cannot take that back; silently
// narrowing the mode would hide the exposure and keep using the key. The
// operator is told to rotate it, which means deleting the key file and the
// data sealed under it.
var ErrUnsafeKeyFile = errors.New("secrets: key file is not safe to use")

// syncFile and syncDir are variables so the durability test can prove the
// creation path calls both; production always runs the real ones.
var (
	syncFile = (*os.File).Sync
	syncDir  = fsutil.SyncDir
)

// KeyID identifies a key: the first 8 bytes of SHA-256 over the key
// material. It is embedded in every container and is safe to log.
type KeyID [keyIDSize]byte

func (id KeyID) String() string { return hex.EncodeToString(id[:]) }

// Key is a per-project AES-256 key loaded from the data directory.
type Key struct {
	ID       KeyID
	material [keySize]byte
}

// LoadOrCreateKey returns the key for projectID, creating
// <keysDir>/<projectID>.key with a fresh random key on first use.
// The file is created user-only: 0600 on POSIX, and on Windows a
// protected DACL granting access to the current user alone, because
// mode bits are a no-op there.
//
// An existing key file is validated before it is used, and an unsafe one
// is refused rather than repaired (see ErrUnsafeKeyFile).
func LoadOrCreateKey(keysDir, projectID string) (*Key, error) {
	if !safeProjectID.MatchString(projectID) {
		return nil, fmt.Errorf("secrets: unsafe project id %q", projectID)
	}
	if err := prepareKeysDir(keysDir); err != nil {
		return nil, err
	}
	path := filepath.Join(keysDir, projectID+".key")

	material, err := readKeyFile(path)
	switch {
	case err == nil:
		if k, kerr := keyFromMaterial(material, path); kerr == nil {
			return k, nil
		}
		// A file of the wrong size may be a key another process created a
		// moment ago and has not written yet. awaitKey settles which it is
		// before this reports a corrupt key.
		return awaitKey(path)
	case errors.Is(err, fs.ErrNotExist):
		return createKey(path, keysDir)
	default:
		return nil, err
	}
}

// prepareKeysDir creates the keys directory, or narrows an existing one.
//
// MkdirAll applies the mode to directories it creates and leaves an
// existing one alone, so a keys directory that predates this check, or one
// created by a different tool, can still be world-readable. Narrowing a
// directory is safe to do silently: unlike the key file, the directory
// carries no secret of its own, and traversal is what the mode blocks.
func prepareKeysDir(keysDir string) error {
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return fmt.Errorf("secrets: keys dir: %w", err)
	}
	info, err := os.Lstat(keysDir)
	if err != nil {
		return fmt.Errorf("secrets: keys dir: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		// Inside a 0700 data directory there is no legitimate reason for
		// this one entry to be a link, and following it would put the keys
		// somewhere the rest of the durability and permission work does not
		// cover.
		return fmt.Errorf("%w: %s is a symlink", ErrUnsafeKeyFile, keysDir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrUnsafeKeyFile, keysDir)
	}
	return narrowDir(keysDir, info)
}

// readKeyFile reads an existing key file after proving it is one.
//
// Lstat rejects a symlink before anything is opened, so a link planted in
// the keys directory cannot redirect the read to another file - and,
// because the same path is later written, cannot redirect a write either.
// The mode is then checked on the opened file rather than on the path, so
// a swap between the two calls cannot slip an unsafe file past the check.
func readKeyFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		// fs.ErrNotExist travels up unwrapped: the caller creates the key.
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %s is a symlink", ErrUnsafeKeyFile, path)
	}

	// nolint:gosec // path is built from keysDir plus a projectID that
	// safeProjectID has already constrained to a single path element.
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("secrets: read key: %w", err)
	}
	defer func() { _ = f.Close() }()

	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("secrets: stat key: %w", err)
	}
	if !opened.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrUnsafeKeyFile, path)
	}
	if err := checkKeyFileAccess(path, opened); err != nil {
		return nil, err
	}

	// One byte past the key size, so an oversized file is reported as the
	// wrong size by keyFromMaterial instead of being silently truncated to
	// a valid-looking key.
	material, err := io.ReadAll(io.LimitReader(f, keySize+1))
	if err != nil {
		return nil, fmt.Errorf("secrets: read key: %w", err)
	}
	return material, nil
}

func createKey(path, keysDir string) (*Key, error) {
	material := make([]byte, keySize)
	if _, err := rand.Read(material); err != nil {
		return nil, fmt.Errorf("secrets: generate key: %w", err)
	}
	// O_EXCL: if two processes race, exactly one creates the key and the
	// loser rereads it instead of silently overwriting.
	// nolint:gosec // path is validated by safeProjectID; see LoadOrCreateKey.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		// Platforms disagree on how O_EXCL reports a path that is already
		// taken: POSIX answers EEXIST whatever stands there, Windows
		// answers "is a directory" for a directory. Deciding on the path
		// itself rather than on the error keeps one refusal reason on both,
		// so a directory planted at the key path is reported as unsafe
		// instead of as whichever syscall noticed it first.
		if _, statErr := os.Lstat(path); statErr == nil {
			return awaitKey(path)
		}
		return nil, fmt.Errorf("secrets: create key: %w", err)
	}
	if err := restrictToOwner(path, f); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("secrets: restrict key file: %w", err)
	}
	if _, err := f.Write(material); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("secrets: write key: %w", err)
	}
	// Everything sealed from here on is unrecoverable without this key, so
	// the key reaches disk before any of it does: file contents first, then
	// the directory entry that names them.
	if err := syncFile(f); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("secrets: sync key: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("secrets: close key: %w", err)
	}
	if err := syncDir(keysDir); err != nil {
		return nil, err
	}
	return keyFromMaterial(material, path)
}

// awaitKey reads the key the winner of the O_EXCL race is writing.
//
// O_EXCL decides who creates the file, not when its contents arrive: the
// winner creates it empty and writes the material a moment later, so a
// loser that reads immediately can find zero bytes. Two processes opening
// one data directory at once is an ordinary event here - a fixture
// starting the server while `ensure` runs - and it must not fail with a
// message about a truncated key.
//
// Only the size mismatch is retried. An unsafe file is refused at once,
// and a key file that is still the wrong size after the wait is reported
// as such: silently deleting or rewriting key material is never this
// function's call to make.
func awaitKey(path string) (*Key, error) {
	const attempts = 10
	var err error
	for i := range attempts {
		var material []byte
		if material, err = readKeyFile(path); err != nil {
			if errors.Is(err, ErrUnsafeKeyFile) {
				return nil, err
			}
			return nil, fmt.Errorf("secrets: reread key: %w", err)
		}
		var k *Key
		if k, err = keyFromMaterial(material, path); err == nil {
			return k, nil
		}
		time.Sleep(time.Duration(i+1) * 10 * time.Millisecond)
	}
	return nil, err
}

func keyFromMaterial(material []byte, path string) (*Key, error) {
	if len(material) != keySize {
		return nil, fmt.Errorf("secrets: key file %s has %d bytes, want %d", path, len(material), keySize)
	}
	k := &Key{}
	copy(k.material[:], material)
	sum := sha256.Sum256(material)
	copy(k.ID[:], sum[:keyIDSize])
	return k, nil
}
