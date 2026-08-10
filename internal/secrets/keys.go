package secrets

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

const keySize = 32

// safeProjectID keeps a project identifier from steering the key path
// somewhere else: it reaches this package from authstunt.yaml, and a
// value like "../../id_rsa" would otherwise pick the file to read.
var safeProjectID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

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
func LoadOrCreateKey(keysDir, projectID string) (*Key, error) {
	if !safeProjectID.MatchString(projectID) {
		return nil, fmt.Errorf("secrets: unsafe project id %q", projectID)
	}
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return nil, fmt.Errorf("secrets: keys dir: %w", err)
	}
	path := filepath.Join(keysDir, projectID+".key")

	// nolint:gosec // path is built from keysDir plus a projectID that
	// safeProjectID has already constrained to a single path element.
	material, err := os.ReadFile(path)
	switch {
	case err == nil:
		return keyFromMaterial(material, path)
	case errors.Is(err, fs.ErrNotExist):
		return createKey(path)
	default:
		return nil, fmt.Errorf("secrets: read key: %w", err)
	}
}

func createKey(path string) (*Key, error) {
	material := make([]byte, keySize)
	if _, err := rand.Read(material); err != nil {
		return nil, fmt.Errorf("secrets: generate key: %w", err)
	}
	// O_EXCL: if two processes race, exactly one creates the key and the
	// loser rereads it instead of silently overwriting.
	// nolint:gosec // path is validated by safeProjectID; see LoadOrCreateKey.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			// nolint:gosec // same validated path as above.
			existing, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil, fmt.Errorf("secrets: reread key: %w", rerr)
			}
			return keyFromMaterial(existing, path)
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
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("secrets: close key: %w", err)
	}
	return keyFromMaterial(material, path)
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
