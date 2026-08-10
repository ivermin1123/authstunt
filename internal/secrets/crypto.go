package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// Container format, versioned so it can evolve without guessing:
//
//	[1]  version (0x01)
//	[8]  key id
//	[12] GCM nonce
//	[..] ciphertext || 16-byte GCM tag
const (
	containerVersion = 0x01
	keyIDSize        = 8
	nonceSize        = 12
	tagSize          = 16
	headerSize       = 1 + keyIDSize + nonceSize
)

// ErrTampered is returned when a container fails authentication or is
// structurally invalid.
var ErrTampered = errors.New("secrets: container failed authentication")

// ErrKeyMismatch is returned when a container was sealed by a key other
// than the one supplied.
var ErrKeyMismatch = errors.New("secrets: container sealed by a different key")

// Seal encrypts plaintext with AES-256-GCM under k using a fresh random
// 96-bit nonce and returns the self-describing container.
func Seal(k *Key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(k)
	if err != nil {
		return nil, err
	}
	out := make([]byte, headerSize, headerSize+len(plaintext)+tagSize)
	out[0] = containerVersion
	copy(out[1:], k.ID[:])
	nonce := out[1+keyIDSize : headerSize]
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secrets: nonce: %w", err)
	}
	return gcm.Seal(out, nonce, plaintext, out[:1+keyIDSize]), nil
}

// Seal is the method form, so a *Key satisfies the small sealer
// interfaces other packages declare instead of importing this one.
func (k *Key) Seal(plaintext []byte) ([]byte, error) { return Seal(k, plaintext) }

// Open is the method form of the package-level Open.
func (k *Key) Open(container []byte) ([]byte, error) { return Open(k, container) }

// Open authenticates and decrypts a container produced by Seal.
func Open(k *Key, container []byte) ([]byte, error) {
	if len(container) < headerSize+tagSize {
		return nil, ErrTampered
	}
	if container[0] != containerVersion {
		return nil, fmt.Errorf("secrets: unknown container version %d", container[0])
	}
	if string(container[1:1+keyIDSize]) != string(k.ID[:]) {
		return nil, ErrKeyMismatch
	}
	gcm, err := newGCM(k)
	if err != nil {
		return nil, err
	}
	nonce := container[1+keyIDSize : headerSize]
	plaintext, err := gcm.Open(nil, nonce, container[headerSize:], container[:1+keyIDSize])
	if err != nil {
		return nil, ErrTampered
	}
	return plaintext, nil
}

// KeyIDOf reports the key id embedded in a container without decrypting
// it, so callers can pick the right key once rotation exists.
func KeyIDOf(container []byte) (KeyID, error) {
	if len(container) < headerSize || container[0] != containerVersion {
		return KeyID{}, ErrTampered
	}
	var id KeyID
	copy(id[:], container[1:1+keyIDSize])
	return id, nil
}

func newGCM(k *Key) (cipher.AEAD, error) {
	block, err := aes.NewCipher(k.material[:])
	if err != nil {
		return nil, fmt.Errorf("secrets: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: gcm: %w", err)
	}
	return gcm, nil
}
