package store_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivermin1123/authstunt/internal/store"
)

func TestBlobRoundTrip(t *testing.T) {
	s := openTestStore(t)
	raw := bytes.Repeat([]byte("MIME-Version: 1.0\r\n"), 500)

	ref, err := s.Blobs.Put(raw)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if !store.ValidID(ref) {
		t.Fatalf("ref %q is not a valid id", ref)
	}
	got, err := s.Blobs.Get(ref)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("blob content changed in the round trip")
	}
}

func TestBlobStoredEncrypted(t *testing.T) {
	s := openTestStore(t)
	const marker = "Your verification code is 998877"

	ref, err := s.Blobs.Put([]byte(marker))
	if err != nil {
		t.Fatal(err)
	}
	// nolint:gosec // test-local path built from a ref this test created.
	onDisk, err := os.ReadFile(filepath.Join(s.DataDir(), "blobs", ref+".blob"))
	if err != nil {
		t.Fatalf("read blob file: %v", err)
	}
	if bytes.Contains(onDisk, []byte(marker)) {
		t.Fatal("blob file holds the plaintext")
	}
	if len(onDisk) <= len(marker) {
		t.Fatalf("blob file is %d bytes, too short to carry nonce and tag", len(onDisk))
	}
}

func TestBlobTamperDetected(t *testing.T) {
	s := openTestStore(t)
	ref, err := s.Blobs.Put([]byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.DataDir(), "blobs", ref+".blob")
	// nolint:gosec // test-local path built from a ref this test created.
	sealed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 0x01
	// nolint:gosec // test-local path built from a ref this test just
	// created; corrupting the file is the point of the test.
	if err := os.WriteFile(path, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Blobs.Get(ref); err == nil {
		t.Fatal("tampered blob was returned as if valid")
	}
}

func TestBlobMissingAndBadRefs(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Blobs.Get(store.NewID()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get missing = %v, want ErrNotFound", err)
	}
	// A reference that never came from Put must not reach the filesystem.
	for _, bad := range []string{"../../etc/passwd", "", "ZZZZZZZZZZZZ", "abc"} {
		if _, err := s.Blobs.Get(bad); !errors.Is(err, store.ErrBadRef) {
			t.Errorf("get %q = %v, want ErrBadRef", bad, err)
		}
		if err := s.Blobs.Delete(bad); !errors.Is(err, store.ErrBadRef) {
			t.Errorf("delete %q = %v, want ErrBadRef", bad, err)
		}
	}
}

func TestBlobDeleteIsRepeatable(t *testing.T) {
	s := openTestStore(t)
	ref, err := s.Blobs.Put([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Blobs.Delete(ref); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	// The retention sweep reruns on every tick, so a second delete must
	// not turn into an error.
	if err := s.Blobs.Delete(ref); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := s.Blobs.Get(ref); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get after delete = %v, want ErrNotFound", err)
	}
}

func TestBlobRefsListsOnlyBlobs(t *testing.T) {
	s := openTestStore(t)
	want := make(map[string]bool)
	for range 3 {
		ref, err := s.Blobs.Put([]byte("payload"))
		if err != nil {
			t.Fatal(err)
		}
		want[ref] = true
	}
	// A leftover temporary file must not be reported as a blob.
	stray := filepath.Join(s.DataDir(), "blobs", ".tmp-leftover")
	if err := os.WriteFile(stray, []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}

	refs, err := s.Blobs.Refs()
	if err != nil {
		t.Fatalf("refs: %v", err)
	}
	if len(refs) != len(want) {
		t.Fatalf("Refs returned %d entries, want %d", len(refs), len(want))
	}
	for _, ref := range refs {
		if !want[ref] {
			t.Errorf("unexpected ref %q", ref)
		}
	}
}

func TestBlobFilePermissions(t *testing.T) {
	s := openTestStore(t)
	ref, err := s.Blobs.Put([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(s.DataDir(), "blobs", ref+".blob"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("blob mode %o, want 600", perm)
	}
}
