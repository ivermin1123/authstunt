package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testKey(t *testing.T) *Key {
	t.Helper()
	k, err := LoadOrCreateKey(t.TempDir(), "proj")
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	k := testKey(t)
	for _, plaintext := range [][]byte{nil, {}, []byte("x"), bytes.Repeat([]byte("otp 483920 "), 1000)} {
		sealed, err := Seal(k, plaintext)
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		got, err := Open(k, sealed)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Fatalf("round trip mismatch: got %d bytes, want %d", len(got), len(plaintext))
		}
	}
}

func TestSealNonceUnique(t *testing.T) {
	k := testKey(t)
	a, err := Seal(k, []byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Seal(k, []byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext produced identical containers")
	}
}

func TestOpenTamperDetection(t *testing.T) {
	k := testKey(t)
	sealed, err := Seal(k, []byte("secret value"))
	if err != nil {
		t.Fatal(err)
	}
	// Flip one bit in every byte position; every mutation must fail closed.
	for i := range sealed {
		mutated := bytes.Clone(sealed)
		mutated[i] ^= 0x01
		if _, err := Open(k, mutated); err == nil {
			t.Fatalf("tampered byte %d accepted", i)
		}
	}
	if _, err := Open(k, sealed[:headerSize+tagSize-1]); !errors.Is(err, ErrTampered) {
		t.Fatalf("truncated container: got %v, want ErrTampered", err)
	}
}

func TestOpenWrongKey(t *testing.T) {
	k1 := testKey(t)
	k2 := testKey(t)
	sealed, err := Seal(k1, []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(k2, sealed); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("got %v, want ErrKeyMismatch", err)
	}
}

func TestKeyIDOf(t *testing.T) {
	k := testKey(t)
	sealed, err := Seal(k, []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := KeyIDOf(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if id != k.ID {
		t.Fatalf("embedded key id %s, want %s", id, k.ID)
	}
}

// TestOpenKnownVector decrypts a container assembled by hand with the
// cipher primitives, proving Open agrees with AES-256-GCM directly and
// that the header is bound as additional data.
func TestOpenKnownVector(t *testing.T) {
	material := bytes.Repeat([]byte{0x42}, keySize)
	k, err := keyFromMaterial(material, "vector")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(material)
	if !bytes.Equal(k.ID[:], sum[:keyIDSize]) {
		t.Fatalf("key id not the SHA-256 prefix of the material")
	}

	block, err := aes.NewCipher(material)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	header := append([]byte{containerVersion}, k.ID[:]...)
	container := append(bytes.Clone(header), nonce...)
	container = gcm.Seal(container, nonce, []byte("known plaintext"), header)

	got, err := Open(k, container)
	if err != nil {
		t.Fatalf("Open on hand-built container: %v", err)
	}
	if string(got) != "known plaintext" {
		t.Fatalf("got %q", got)
	}
}

// TestSealIsStandardGCM decrypts Seal's own output with the cipher
// primitives directly. Round-tripping through Open only proves the code
// agrees with itself; this proves the container is real AES-256-GCM with
// the header bound as additional data, and that building the output on a
// slice that aliases the header does not corrupt it.
func TestSealIsStandardGCM(t *testing.T) {
	dir := t.TempDir()
	k, err := LoadOrCreateKey(dir, "proj")
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("otp 483920")
	container, err := Seal(k, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	block, err := aes.NewCipher(k.material[:])
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	header := container[:1+keyIDSize]
	nonce := container[1+keyIDSize : headerSize]
	got, err := gcm.Open(nil, nonce, container[headerSize:], header)
	if err != nil {
		t.Fatalf("raw GCM rejected our container: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("raw GCM decrypted %q, want %q", got, plaintext)
	}
	// Decrypting with the wrong additional data must fail, or the header
	// is not actually bound and a container could be replayed under a
	// forged key id.
	if _, err := gcm.Open(nil, nonce, container[headerSize:], nil); err == nil {
		t.Fatal("container authenticates without its header as additional data")
	}
}

func TestLoadOrCreateKeyPersistence(t *testing.T) {
	dir := t.TempDir()
	k1, err := LoadOrCreateKey(dir, "p1")
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadOrCreateKey(dir, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if k1.ID != k2.ID || k1.material != k2.material {
		t.Fatal("second load returned a different key")
	}
	other, err := LoadOrCreateKey(dir, "p2")
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == k1.ID {
		t.Fatal("distinct projects share a key")
	}
}

func TestKeyFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits; Windows uses a DACL set at creation")
	}
	dir := t.TempDir()
	if _, err := LoadOrCreateKey(dir, "p1"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "p1.key"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key file mode %o, want 600", perm)
	}
}

func TestProjectIDCannotEscapeKeysDir(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"", "../escape", "a/b", `..\windows`, ".hidden", "x\x00y"} {
		if _, err := LoadOrCreateKey(dir, bad); err == nil {
			t.Errorf("project id %q was accepted", bad)
		}
	}
	for _, good := range []string{"demo", "proj-1", "proj_1.test", "A1"} {
		if _, err := LoadOrCreateKey(dir, good); err != nil {
			t.Errorf("project id %q rejected: %v", good, err)
		}
	}
}

func TestKeyFileWrongSizeRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.key"), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(dir, "bad"); err == nil {
		t.Fatal("truncated key file accepted")
	}
}
