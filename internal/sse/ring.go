package sse

// ringSize is how many events are kept for Last-Event-ID replay (design
// 2.2). At the volume a test inbox produces this is a long window; a client
// that falls further behind than this gets one reset rather than a partial
// replay, because a gap the server cannot fill is a correctness problem and
// guessing at it would hide one.
const ringSize = 1024

// ring holds the most recent events in publish order. It is owned by the bus
// goroutine and needs no lock of its own.
//
// While len < cap the events sit at [0:len) in order. Once the slice is
// full, next is both the write position and the oldest element.
type ring struct {
	events []Event
	next   int
}

func newRing(size int) *ring {
	return &ring{events: make([]Event, 0, size)}
}

func (r *ring) add(ev Event) {
	if len(r.events) < cap(r.events) {
		r.events = append(r.events, ev)
		return
	}
	r.events[r.next] = ev
	r.next = (r.next + 1) % len(r.events)
}

// snapshot returns the buffered events, oldest first.
func (r *ring) snapshot() []Event {
	if r.next == 0 {
		// Either not yet full, or full and wrapped exactly: the oldest
		// event is at index 0 in both cases.
		return append([]Event(nil), r.events...)
	}
	out := make([]Event, 0, len(r.events))
	out = append(out, r.events[r.next:]...)
	return append(out, r.events[:r.next]...)
}

// after returns the events published after id, and false when the gap
// between id and the ring cannot be closed. The caller answers false with
// exactly one reset event.
//
// Seqs are contiguous because the bus goroutine assigns them one at a time,
// so the ring's own range is all it takes to decide. The generation is not
// this function's business: the bus compares that before asking.
func (r *ring) after(id EventID) ([]Event, bool) {
	all := r.snapshot()
	if len(all) == 0 {
		// Nothing has been published this generation, so the only claim
		// that can be honored is "I have seen nothing yet". A client
		// asserting seq 5 here holds an id this boot never issued.
		return nil, id.Seq == 0
	}
	oldest, newest := all[0].ID.Seq, all[len(all)-1].ID.Seq
	// id.Seq == oldest-1 still replays the whole ring: the client saw
	// exactly the event before the oldest one kept, so nothing is missing
	// between the two.
	if id.Seq < oldest-1 || id.Seq > newest {
		return nil, false
	}
	return all[len(all)-int(newest-id.Seq):], true
}
