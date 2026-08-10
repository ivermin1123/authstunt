package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// BlobSealer is the encryption boundary the blob store depends on;
// internal/secrets provides the implementation. Keeping it an interface
// lets store tests run without a key file and keeps the dependency
// pointing one way.
type BlobSealer interface {
	Seal(plaintext []byte) ([]byte, error)
	Open(container []byte) ([]byte, error)
}

// ErrNoSealer is returned when blob operations run on a store opened
// without encryption.
var ErrNoSealer = errors.New("store: blob store opened without a sealer")

// ErrBadRef is returned for a reference that did not come from Put.
var ErrBadRef = errors.New("store: invalid blob reference")

const blobExt = ".blob"

// BlobStore keeps large payloads out of the database: raw and HTML mail,
// Playwright storage state. Every file is sealed, which protects a copy of
// these files taken on its own, such as a backup that excludes the key.
// It does not protect a copy of the whole data directory: the key lives in
// there too, by design, so that headless CI works without a keychain.
type BlobStore struct {
	dir    string
	sealer BlobSealer
}

func newBlobStore(dir string, sealer BlobSealer) (*BlobStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: blobs dir: %w", err)
	}
	return &BlobStore{dir: dir, sealer: sealer}, nil
}

// Put seals data, writes it under a fresh reference, and returns that
// reference for the caller to store in a row.
func (b *BlobStore) Put(data []byte) (string, error) {
	if b.sealer == nil {
		return "", ErrNoSealer
	}
	sealed, err := b.sealer.Seal(data)
	if err != nil {
		return "", fmt.Errorf("store: seal blob: %w", err)
	}
	ref := NewID()
	path := b.path(ref)

	// Write to a temporary file and rename, so a crash mid-write can
	// never leave a truncated blob behind a committed row.
	tmp, err := os.CreateTemp(b.dir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("store: blob temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("store: blob chmod: %w", err)
	}
	if _, err = tmp.Write(sealed); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("store: write blob: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("store: sync blob: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return "", fmt.Errorf("store: close blob: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("store: publish blob: %w", err)
	}
	return ref, nil
}

// Get reads and decrypts a blob. A tampered or truncated file fails
// authentication rather than returning partial plaintext.
func (b *BlobStore) Get(ref string) ([]byte, error) {
	if b.sealer == nil {
		return nil, ErrNoSealer
	}
	if !ValidID(ref) {
		return nil, ErrBadRef
	}
	sealed, err := os.ReadFile(b.path(ref))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: read blob: %w", err)
	}
	data, err := b.sealer.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("store: open blob %s: %w", ref, err)
	}
	return data, nil
}

// Delete removes a blob. Deleting an absent blob is not an error: the
// retention sweep must be safe to rerun.
func (b *BlobStore) Delete(ref string) error {
	if !ValidID(ref) {
		return ErrBadRef
	}
	if err := os.Remove(b.path(ref)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("store: delete blob: %w", err)
	}
	return nil
}

// Refs lists every stored reference. The retention sweep uses it to find
// blobs whose rows are gone.
func (b *BlobStore) Refs() ([]string, error) {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		return nil, fmt.Errorf("store: list blobs: %w", err)
	}
	refs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ref := strings.TrimSuffix(e.Name(), blobExt)
		if ref != e.Name() && ValidID(ref) {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

func (b *BlobStore) path(ref string) string {
	return filepath.Join(b.dir, ref+blobExt)
}
