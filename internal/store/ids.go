package store

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
)

// idBytes yields 12 hex characters, 48 bits of entropy. Ids appear in
// REST paths and are opaque to clients: random rather than sequential so
// they leak no counts and never collide with ids from a wiped database.
const idBytes = 6

var idPattern = regexp.MustCompile(`^[0-9a-f]{12}$`)

// NewID returns a fresh random identifier.
func NewID() string {
	b := make([]byte, idBytes)
	// crypto/rand.Read is documented to never fail as of Go 1.24; a
	// failure here would mean the OS entropy source is gone.
	if _, err := rand.Read(b); err != nil {
		panic("store: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// ValidID reports whether s has the shape NewID produces. Handlers use it
// to reject junk before it reaches a query, and the blob store uses it to
// keep references from escaping the blobs directory.
func ValidID(s string) bool { return idPattern.MatchString(s) }
