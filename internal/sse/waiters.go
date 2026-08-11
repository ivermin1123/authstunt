package sse

import "context"

// Waiter is one parked long-poll request.
//
// The ordering contract (design 2.2, normative) is the reason this type
// exists separately from a plain channel read. A `?wait=` request must:
//
//	w, err := bus.SubscribeMatch(ctx, match)  // 1. register the matcher
//	defer w.Close()                           // unsubscribe on every exit
//	if found := store.Query(...); found {     // 2. then query the store
//	    return found
//	}
//	ev, ok := w.Wait(ctx)                     // 3. only then park
//
// Querying before registering loses any message that arrives between the two
// steps: the query does not see it because it has not committed yet, and the
// matcher does not see it because it does not exist yet, so the caller parks
// for its full deadline and reports a timeout for mail that has already
// arrived. Registering first turns that window harmless - the event lands in
// this waiter's buffer while the query is still running, and Wait returns it
// immediately.
//
// Composing the two halves is the API layer's job; this package deliberately
// knows nothing about the store.
type Waiter struct {
	events chan Event
	bus    *Bus
	match  func(Event) bool
}

// Wait parks until an event matches, the context deadline passes, or the bus
// stops. ok is false in the latter two cases, which the caller reports as a
// timeout: a long-poll that found nothing is a valid result, not an error.
//
// The buffer holds one event, so a match that arrived before this call
// returns without parking at all.
func (w *Waiter) Wait(ctx context.Context) (ev Event, ok bool) {
	select {
	case ev, ok = <-w.events:
		return ev, ok
	case <-ctx.Done():
		return Event{}, false
	}
}

// Close unsubscribes and releases the waiter. It is safe to call more than
// once, after a match, and after the bus has stopped, so `defer w.Close()`
// covers every exit path.
func (w *Waiter) Close() {
	select {
	case w.bus.dropWaiterCh <- w:
	case <-w.bus.stopped:
	}
}

// deliver is called on the bus goroutine. The buffer holds one event and the
// bus drops the waiter from the registry immediately afterwards, so the send
// cannot block; the default case is here because a bus that could be stalled
// by one waiter would be the wrong shape whatever the arithmetic says.
func (w *Waiter) deliver(ev Event) {
	select {
	case w.events <- ev:
	default:
	}
}
