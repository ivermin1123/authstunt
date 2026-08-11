package sse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
)

// ResetEventName is the one event name this package fixes. It tells the
// client that continuity is broken and it must refetch everything, and it is
// the only correctness fallback there is: replay is best effort, reset is the
// answer whenever the gap cannot be filled. There is no third state and a gap
// is never silently skipped.
const ResetEventName = "reset"

// resetData is a payload rather than nothing because an event whose data
// field is empty is not dispatched at all: the HTML event-stream algorithm
// returns without firing when the data buffer is empty, so a reset with no
// body would be a reset the browser never hears.
var resetData = []byte("{}")

// ErrEvicted reports that this client was dropped for not draining its
// buffer. The client reconnects with Last-Event-ID and the ring replays what
// it missed, so this is ordinary operation rather than a failure.
var ErrEvicted = errors.New("sse: client evicted for not keeping up")

// Sink is where the hub writes frames: a writer that can be flushed.
//
// It is an interface rather than an http.ResponseWriter because real socket
// writes are the one thing testing/synctest cannot see through - a goroutine
// blocked on network I/O is not durably blocked, so a bubble containing one
// never goes idle. The HTTP adapter is a few lines at the API layer and is
// covered there with httptest.
type Sink interface {
	io.Writer
	Flush() error
}

// Serve streams events to one client until the context is canceled, the
// client is evicted, or the bus stops. lastEventID is the client's
// Last-Event-ID header, empty on a first connection.
func Serve(ctx context.Context, b *Bus, sink Sink, lastEventID string) error {
	var from EventID
	malformed := false
	if lastEventID != "" {
		parsed, err := ParseEventID(lastEventID)
		if err != nil {
			// A client holding an id we cannot read still believes it has
			// state, so it gets the same answer as one whose id aged out of
			// the ring rather than an error it has no way to act on.
			malformed = true
		} else {
			from = parsed
		}
	}

	sub, err := b.SubscribeFrom(ctx, from)
	if err != nil {
		return err
	}
	defer sub.Close()

	if sub.Reset || malformed {
		reset := Event{ID: sub.Anchor, Name: ResetEventName, Data: resetData}
		if err := writeFrame(sink, reset); err != nil {
			return err
		}
	}
	for _, ev := range sub.Replay {
		if err := writeFrame(sink, ev); err != nil {
			return err
		}
	}

	for {
		select {
		case ev, ok := <-sub.Events():
			if !ok {
				if sub.Evicted() {
					return ErrEvicted
				}
				return ErrBusStopped
			}
			if err := writeFrame(sink, ev); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// writeFrame writes one event as a single frame and flushes it. One Write per
// frame keeps a partially written event off the wire, where a client would
// read it as a different event than the one we sent.
func writeFrame(sink Sink, ev Event) error {
	var frame bytes.Buffer
	fmt.Fprintf(&frame, "id: %s\nevent: %s\n", ev.ID, ev.Name)
	// One data line per line of payload, which is how the receiver rebuilds
	// a multi-line body: it joins the values with newlines. Splitting an
	// empty payload yields one empty line, which still dispatches the event.
	// A trailing CR is dropped because the receiver treats CR, LF, and CRLF
	// alike as line ends, so leaving one in would append a stray character
	// to the value.
	for _, line := range bytes.Split(ev.Data, []byte("\n")) {
		fmt.Fprintf(&frame, "data: %s\n", bytes.TrimSuffix(line, []byte("\r")))
	}
	frame.WriteByte('\n')

	if _, err := sink.Write(frame.Bytes()); err != nil {
		return fmt.Errorf("sse: write event %s: %w", ev.ID, err)
	}
	if err := sink.Flush(); err != nil {
		return fmt.Errorf("sse: flush event %s: %w", ev.ID, err)
	}
	return nil
}
