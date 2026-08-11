package sse

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// ErrBusStopped is returned once the bus goroutine has exited. Callers see
// it instead of blocking forever on a bus that is no longer running.
var ErrBusStopped = errors.New("sse: bus stopped")

const (
	// publishBuffer absorbs a burst of ingest without making the SMTP path
	// wait on fan-out. A full channel makes Publish block rather than drop:
	// an event the bus never sees is a message a parked fixture never hears
	// about, and losing that is worse than slowing ingest down.
	publishBuffer = 64
	// subscriberBuffer is the per-client buffer size fixed by design 2.2.
	subscriberBuffer = 64
)

// MessageRef carries what a parked long-poll waiter matches on, and nothing
// else. It exists so the waiter registry can decide a match without a store
// handle: matching happens on the bus goroutine, where a database query has
// no business being.
type MessageRef struct {
	ID        string
	ProjectID string
	// Subject is the plaintext subject header. The frozen contract matches
	// subject_re against this alone and never against the body, which would
	// mean unsealing every candidate row.
	Subject string
	// Recipients holds the canonical envelope recipients only. List, wait,
	// and MCP all match on those, because a Bcc recipient never appears in
	// any header.
	Recipients []string
	ReceivedAt time.Time
}

// Publication is an event handed to the bus. The bus assigns the id, so
// there is no id field here to set and have ignored.
type Publication struct {
	// Name is the SSE event name. This package fixes only ResetEventName;
	// the names carried by real events belong to the wire contract that
	// lands with the REST surface. It must be a single line: a name with a
	// newline in it would end its own frame and the rest would be read as
	// another event, so names come from constants rather than from input.
	Name string
	// Data is the payload written to the SSE data field, expected to be
	// JSON. The bus never looks inside it, and holds it by reference for as
	// long as it stays in the replay ring, so a caller must not modify a
	// slice it has published.
	Data []byte
	// Message is set on message events and is what long-poll matchers read.
	Message *MessageRef
}

// Event is a published event as its subscribers receive it.
type Event struct {
	ID      EventID
	Name    string
	Data    []byte
	Message *MessageRef
}

// Bus is the in-process event bus: one goroutine owns the subscriber set,
// the waiter registry, and the replay ring, so every decision that has to be
// atomic against publication simply happens in program order there.
type Bus struct {
	generation int64

	publishCh    chan Publication
	subscribeCh  chan subscribeReq
	matchCh      chan matchReq
	dropSubCh    chan *Subscription
	dropWaiterCh chan *Waiter

	// stopped is closed when Run returns, so no caller can park on a bus
	// that has already released everyone.
	stopped chan struct{}
}

// NewBus returns a bus stamping ids with the given generation, which the
// caller takes from the store's persisted counter once per server start.
func NewBus(generation int64) *Bus {
	return &Bus{
		generation:   generation,
		publishCh:    make(chan Publication, publishBuffer),
		subscribeCh:  make(chan subscribeReq),
		matchCh:      make(chan matchReq),
		dropSubCh:    make(chan *Subscription),
		dropWaiterCh: make(chan *Waiter),
		stopped:      make(chan struct{}),
	}
}

type subscribeReq struct {
	from  EventID
	reply chan *Subscription
}

type matchReq struct {
	match func(Event) bool
	reply chan *Waiter
}

// Run owns the bus state until ctx is canceled. Callers run it in its own
// goroutine and wait for it to return as part of shutdown. Publish and both
// Subscribe calls park until it is running, so it starts before the surfaces
// that feed it.
func (b *Bus) Run(ctx context.Context) {
	subscribers := make(map[*Subscription]struct{})
	waiters := make(map[*Waiter]struct{})
	replay := newRing(ringSize)
	var seq int64

	defer func() {
		// Shutdown releases everyone instead of leaving parked callers to
		// wait out their own deadlines. Phase 6 turns a released waiter into
		// the {timed_out: true} envelope the contract promises, and an SSE
		// client sees its stream end and reconnects with Last-Event-ID.
		for s := range subscribers {
			close(s.events)
		}
		for w := range waiters {
			close(w.events)
		}
		close(b.stopped)
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case p := <-b.publishCh:
			seq++
			ev := Event{
				ID:      EventID{Generation: b.generation, Seq: seq},
				Name:    p.Name,
				Data:    p.Data,
				Message: p.Message,
			}
			replay.add(ev)
			// Waiters before SSE fan-out, in the order design 2.2 states:
			// a machine client blocked on this exact message is the one
			// waiting synchronously.
			for w := range waiters {
				if w.match(ev) {
					w.deliver(ev)
					// One shot. First match wins, and the waiter is gone
					// from the registry before the next event is assigned
					// an id.
					delete(waiters, w)
				}
			}
			for s := range subscribers {
				if !s.deliver(ev) {
					delete(subscribers, s)
				}
			}

		case req := <-b.subscribeCh:
			// Registering the subscriber and snapshotting its replay happen
			// here, in one step of this goroutine's program order. Any split
			// between them is a window where a published event reaches
			// neither the snapshot nor the live channel, and a lost event is
			// exactly the failure the ring exists to prevent.
			s := &Subscription{
				events: make(chan Event, subscriberBuffer),
				bus:    b,
				// Anchor stamps the reset frame with an id that is valid
				// now, so a client that reconnects after a reset continues
				// from here instead of presenting the same stale id and
				// being reset again.
				Anchor: EventID{Generation: b.generation, Seq: seq},
			}
			switch {
			case req.from.IsZero():
				// A fresh connection: the dashboard has just fetched
				// through REST, so there is nothing to replay and nothing
				// to reset.
			case req.from.Generation != b.generation:
				s.Reset = true
			default:
				missed, ok := replay.after(req.from)
				if !ok {
					s.Reset = true
				} else {
					s.Replay = missed
				}
			}
			subscribers[s] = struct{}{}
			req.reply <- s

		case req := <-b.matchCh:
			w := &Waiter{events: make(chan Event, 1), bus: b, match: req.match}
			waiters[w] = struct{}{}
			req.reply <- w

		case s := <-b.dropSubCh:
			// Closing here, on the one goroutine that ever sends, is what
			// makes Events() safe to range over: an unsubscribed stream ends
			// instead of leaving its reader parked forever. The map check
			// keeps a second Close, or a Close after an eviction, from
			// closing an already closed channel.
			if _, live := subscribers[s]; live {
				delete(subscribers, s)
				close(s.events)
			}

		case w := <-b.dropWaiterCh:
			if _, live := waiters[w]; live {
				delete(waiters, w)
				close(w.events)
			}
		}
	}
}

// Publish hands an event to the bus, blocking while the publish buffer is
// full so a burst slows ingest rather than losing events.
func (b *Bus) Publish(ctx context.Context, p Publication) error {
	select {
	case b.publishCh <- p:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-b.stopped:
		return ErrBusStopped
	}
}

// SubscribeFrom registers an SSE subscriber. from is the id the client last
// saw; the zero EventID means a fresh connection with nothing to replay.
func (b *Bus) SubscribeFrom(ctx context.Context, from EventID) (*Subscription, error) {
	req := subscribeReq{from: from, reply: make(chan *Subscription, 1)}
	select {
	case b.subscribeCh <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.stopped:
		return nil, ErrBusStopped
	}
	// The reply is buffered and sent without blocking, so waiting on it can
	// only end when the bus answers or stops. Selecting on ctx here would
	// abandon a subscriber the bus has already registered.
	select {
	case s := <-req.reply:
		return s, nil
	case <-b.stopped:
		return nil, ErrBusStopped
	}
}

// SubscribeMatch registers a long-poll matcher and returns before any query
// runs, which is the whole point: see the ordering contract on Waiter.
//
// match runs on the bus goroutine, so it must be cheap and must never block.
// Comparing addresses and running one regexp over a subject header qualifies;
// Go's regexp is linear in the input, so a hostile pattern costs time
// proportional to a subject line and cannot stall the bus.
func (b *Bus) SubscribeMatch(ctx context.Context, match func(Event) bool) (*Waiter, error) {
	req := matchReq{match: match, reply: make(chan *Waiter, 1)}
	select {
	case b.matchCh <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.stopped:
		return nil, ErrBusStopped
	}
	select {
	case w := <-req.reply:
		return w, nil
	case <-b.stopped:
		return nil, ErrBusStopped
	}
}

// Subscription is one SSE client's view of the bus: what it missed, whether
// it has to refetch, and the live stream from there on.
type Subscription struct {
	// Replay holds the events published between the client's last id and its
	// reconnection, oldest first. Empty when Reset is set.
	Replay []Event
	// Reset means the requested id cannot be continued, either because it
	// belongs to an earlier generation or because the events between it and
	// the ring are gone. The client refetches everything.
	Reset bool
	// Anchor is the id to stamp on the reset event.
	Anchor EventID

	events  chan Event
	bus     *Bus
	evicted atomic.Bool
}

// Events is the live stream. It is closed when the subscription is closed,
// when the subscriber is evicted, or when the bus stops; Evicted tells an
// eviction apart from the other two.
func (s *Subscription) Events() <-chan Event { return s.events }

// Evicted reports whether this client was dropped for not keeping up.
func (s *Subscription) Evicted() bool { return s.evicted.Load() }

// Close unsubscribes and ends the stream. It is safe to call more than once,
// after an eviction, and after the bus has stopped.
func (s *Subscription) Close() {
	select {
	case s.bus.dropSubCh <- s:
	case <-s.bus.stopped:
	}
}

// deliver is called on the bus goroutine and reports whether the subscriber
// is still live.
func (s *Subscription) deliver(ev Event) bool {
	select {
	case s.events <- ev:
		return true
	default:
		// The buffer is full, so this client is not draining. Waiting for it
		// would stall ingest and every other client, which is why design 2.2
		// drops it instead: the browser reconnects with Last-Event-ID and the
		// ring replays what it missed.
		s.evicted.Store(true)
		close(s.events)
		return false
	}
}
