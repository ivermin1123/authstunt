package store

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrBadCursor is returned for a continuation token this build did not
// produce. The REST layer maps it to 400 (design 4.2 item 5).
var ErrBadCursor = errors.New("store: malformed cursor")

// MessageCursor is the continuation point of a message listing: the
// (received_at, id) pair of the last item the previous page actually
// emitted.
//
// A bare timestamp cursor loses mail permanently. Two messages can share a
// millisecond - a signup that mails a code and a welcome note does exactly
// that - and a page that ends between them has no timestamp it can ask
// for next: the same value repeats the first, a later one skips the
// second. The id breaks the tie, and because the listing already orders by
// (received_at DESC, id DESC) the pair is a total order over the table.
type MessageCursor struct {
	ReceivedAt time.Time
	ID         string
}

// CursorFor builds the cursor that continues after m. Callers pass the
// last message they actually emitted, never the last one they read: a page
// truncated by a limit would otherwise skip the difference.
func CursorFor(m Message) MessageCursor {
	return MessageCursor{ReceivedAt: m.ReceivedAt, ID: m.ID}
}

// IsZero reports whether the cursor is unset, meaning "start at the
// newest message".
func (c MessageCursor) IsZero() bool { return c.ID == "" && c.ReceivedAt.IsZero() }

// Encode renders the cursor as one opaque URL-safe token.
//
// The encoding is deliberately not documented to clients: it is a
// continuation handle, and a client that parses it would start depending
// on the ordering key. Encoding, not signing - the pair is not a secret
// and a forged cursor can only reposition the caller's own listing.
func (c MessageCursor) Encode() string {
	return base64.RawURLEncoding.EncodeToString([]byte(timestamp(c.ReceivedAt) + " " + c.ID))
}

// DecodeMessageCursor parses a token from Encode, reporting ErrBadCursor
// for anything else.
func DecodeMessageCursor(token string) (MessageCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return MessageCursor{}, fmt.Errorf("%w: not a cursor token", ErrBadCursor)
	}
	ts, id, ok := strings.Cut(string(raw), " ")
	if !ok || !ValidID(id) {
		return MessageCursor{}, fmt.Errorf("%w: wrong shape", ErrBadCursor)
	}
	receivedAt, err := parseTimestamp(ts)
	if err != nil {
		return MessageCursor{}, fmt.Errorf("%w: bad timestamp", ErrBadCursor)
	}
	return MessageCursor{ReceivedAt: receivedAt, ID: id}, nil
}
