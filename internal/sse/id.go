package sse

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrBadEventID marks a Last-Event-ID this server cannot have issued.
//
// It is not reported to the client as an error. A client holding an
// unreadable id still believes it has state, so the hub answers it the same
// way it answers an id that has aged out of the ring: one reset event, then
// live delivery.
var ErrBadEventID = errors.New("sse: malformed event id")

// EventID is an SSE event id, `generation-seq` (design 4.2 item 4).
//
// Generation is the counter the store persists and increments once per
// server start, so ids never repeat across restarts however the clock
// behaves, and two boots stay comparable: a reconnecting client whose id
// carries an older generation is told to refetch instead of being handed a
// replay that would silently skip whatever the previous boot published. Seq
// is assigned by the bus goroutine in publish order.
//
// The zero EventID means "no id at all". Generation counting starts at 1,
// so no published event ever carries generation 0.
type EventID struct {
	Generation int64
	Seq        int64
}

func (id EventID) String() string {
	return strconv.FormatInt(id.Generation, 10) + "-" + strconv.FormatInt(id.Seq, 10)
}

// IsZero reports the "no id at all" case, which a fresh SSE connection
// presents and no published event ever carries.
func (id EventID) IsZero() bool { return id == EventID{} }

// ParseEventID reads an id back out of a Last-Event-ID header.
func ParseEventID(s string) (EventID, error) {
	gen, seq, ok := strings.Cut(s, "-")
	if !ok {
		return EventID{}, fmt.Errorf("%w: %q has no separator", ErrBadEventID, s)
	}
	generation, err := parseIDPart(gen)
	if err != nil {
		return EventID{}, fmt.Errorf("%w: %q: generation: %w", ErrBadEventID, s, err)
	}
	sequence, err := parseIDPart(seq)
	if err != nil {
		return EventID{}, fmt.Errorf("%w: %q: seq: %w", ErrBadEventID, s, err)
	}
	return EventID{Generation: generation, Seq: sequence}, nil
}

// parseIDPart accepts decimal digits and nothing else. strconv alone would
// take "+7" and " 7" as 7 and "-7-1" as a negative generation, all of which
// would then compare against real ids as if the client had been issued them.
func parseIDPart(s string) (int64, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("%q is not a decimal number", s)
		}
	}
	return strconv.ParseInt(s, 10, 64)
}
