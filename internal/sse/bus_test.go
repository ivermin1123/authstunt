package sse

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// startBus runs a bus for the length of the test and waits for its goroutine
// to exit during cleanup. Inside a synctest bubble that wait is also the
// goroutine leak check: the bubble fails the test if anything it started is
// still running when the test function returns.
func startBus(t *testing.T, generation int64) *Bus {
	t.Helper()
	b := NewBus(generation)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return b
}

// queryMissingTheRace stands in for the store query the API layer runs
// between registering a matcher and parking. It finds nothing, which is what
// a snapshot taken before the racing row commits sees.
func queryMissingTheRace() string { return "" }

func matchTo(addr string) func(Event) bool {
	return func(ev Event) bool {
		return ev.Message != nil && slices.Contains(ev.Message.Recipients, addr)
	}
}

func publishTo(t *testing.T, b *Bus, id, to string) {
	t.Helper()
	err := b.Publish(context.Background(), Publication{
		Name:    "message",
		Data:    []byte(`{"id":"` + id + `"}`),
		Message: &MessageRef{ID: id, ProjectID: "p1", Recipients: []string{to}},
	})
	if err != nil {
		t.Fatalf("publish %s: %v", id, err)
	}
}

// TestSubscribeThenQueryDeliversRaceMail is the P1 acceptance proof for the
// ordering contract in design 2.2.
//
// The waiter registers its matcher, a message then arrives, and only after
// that does the caller run the store query that finds nothing and park. Under
// query-before-subscribe that message is lost twice over - too late for the
// query, too early for the subscription - and the caller waits out its whole
// deadline reporting a timeout for mail that already arrived. Here it must
// come back immediately, and the fake clock proves "immediately": time inside
// a bubble only moves when every goroutine is durably blocked, so a waiter
// that really parked would show 30 seconds of it.
func TestSubscribeThenQueryDeliversRaceMail(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 3)

		w, err := b.SubscribeMatch(context.Background(), matchTo("persona@x.test"))
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		defer w.Close()

		// The gap between the matcher being registered and the query being
		// run is exactly where this message lands.
		publishTo(t, b, "m1", "persona@x.test")
		synctest.Wait()

		// The caller's query: its snapshot predates the row above, so it
		// finds nothing. That is what makes this a race rather than a slow
		// query.
		if found := queryMissingTheRace(); found != "" {
			t.Fatalf("fixture error: the query was meant to miss the racing message, got %q", found)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		start := time.Now()
		ev, ok := w.Wait(ctx)
		if !ok {
			t.Fatal("the waiter timed out on mail that had already arrived")
		}
		if parked := time.Since(start); parked != 0 {
			t.Errorf("the waiter parked for %v; the match was already buffered", parked)
		}
		if ev.Message.ID != "m1" {
			t.Errorf("woke on message %q, want m1", ev.Message.ID)
		}
		if want := (EventID{Generation: 3, Seq: 1}); ev.ID != want {
			t.Errorf("event id %v, want %v", ev.ID, want)
		}
	})
}

// TestWaiterTimesOutWithoutMatch pins the other half of the contract: a wait
// that finds nothing returns a timeout at its deadline, and never hangs.
func TestWaiterTimesOutWithoutMatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 1)
		w, err := b.SubscribeMatch(context.Background(), matchTo("wanted@x.test"))
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		defer w.Close()

		// Mail for somebody else must not wake this waiter.
		publishTo(t, b, "other", "someone-else@x.test")
		synctest.Wait()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		start := time.Now()
		if _, ok := w.Wait(ctx); ok {
			t.Fatal("a non-matching message woke the waiter")
		}
		if parked := time.Since(start); parked != 30*time.Second {
			t.Errorf("waiter returned after %v, want the full 30s deadline", parked)
		}
	})
}

// TestBusStopReleasesParkedWaiters proves the shutdown ordering the API layer
// depends on: a parked waiter is released rather than left to burn its own
// deadline, so a shutting-down server answers {timed_out: true} at once
// instead of holding the connection open for another half minute.
func TestBusStopReleasesParkedWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := NewBus(1)
		busCtx, stopBus := context.WithCancel(context.Background())
		busDone := make(chan struct{})
		go func() {
			defer close(busDone)
			b.Run(busCtx)
		}()

		w, err := b.SubscribeMatch(context.Background(), matchTo("wanted@x.test"))
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		defer w.Close()

		released := make(chan bool, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, ok := w.Wait(ctx)
			released <- ok
		}()
		synctest.Wait()

		start := time.Now()
		stopBus()
		<-busDone

		if ok := <-released; ok {
			t.Error("a released waiter reported a match")
		}
		if waited := time.Since(start); waited != 0 {
			t.Errorf("the waiter was released %v after shutdown, want immediately", waited)
		}
		// A bus that has stopped answers callers instead of parking them.
		if _, err := b.SubscribeMatch(context.Background(), matchTo("x@x.test")); err != ErrBusStopped {
			t.Errorf("subscribing to a stopped bus returned %v, want ErrBusStopped", err)
		}
	})
}

// TestEveryMatchingWaiterWakes: two fixtures waiting on the same address both
// hear about the message. "First match wins" is about one waiter taking the
// first event that matches it, not about one waiter consuming the event.
func TestEveryMatchingWaiterWakes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 1)
		var waiters []*Waiter
		for range 2 {
			w, err := b.SubscribeMatch(context.Background(), matchTo("shared@x.test"))
			if err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			defer w.Close()
			waiters = append(waiters, w)
		}

		publishTo(t, b, "m1", "shared@x.test")
		synctest.Wait()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		for i, w := range waiters {
			ev, ok := w.Wait(ctx)
			if !ok {
				t.Fatalf("waiter %d did not wake", i)
			}
			if ev.Message.ID != "m1" {
				t.Errorf("waiter %d woke on %q, want m1", i, ev.Message.ID)
			}
		}
	})
}

// TestWaiterKeepsMatchingUntilClosed: answering a waiter does not
// unsubscribe it. A caller that has to reject what it was woken by, which
// is every caller waiting for one kind of mail on an address that receives
// several, has to still be reachable by the message it wanted.
func TestWaiterKeepsMatchingUntilClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 1)
		w, err := b.SubscribeMatch(context.Background(), matchTo("a@x.test"))
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		defer w.Close()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		publishTo(t, b, "first", "a@x.test")
		synctest.Wait()
		ev, ok := w.Wait(ctx)
		if !ok || ev.Message.ID != "first" {
			t.Fatalf("first wake returned (%v, %v), want the first message", ev.Message, ok)
		}

		// The waiter has read its first event and parked again. Before,
		// it was gone from the registry by now and this second message
		// woke nobody.
		publishTo(t, b, "second", "a@x.test")
		synctest.Wait()
		ev, ok = w.Wait(ctx)
		if !ok {
			t.Fatal("the waiter was not woken by a second matching message")
		}
		if ev.Message.ID != "second" {
			t.Errorf("second wake returned %q, want the second message", ev.Message.ID)
		}
	})
}

// TestWaiterCoalescesHintsItHasNotRead pins the other half of the buffer
// contract: what a waiter carries is a hint that something matched, not a
// queue of everything that did. A second event arriving before the caller
// has read the first is dropped, and dropping it loses nothing, because
// the caller re-queries the store on the hint it does read and finds both.
func TestWaiterCoalescesHintsItHasNotRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 1)
		w, err := b.SubscribeMatch(context.Background(), matchTo("a@x.test"))
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		defer w.Close()

		publishTo(t, b, "first", "a@x.test")
		publishTo(t, b, "second", "a@x.test")
		synctest.Wait()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		ev, ok := w.Wait(ctx)
		if !ok || ev.Message.ID != "first" {
			t.Fatalf("first wake returned (%v, %v), want the first message", ev.Message, ok)
		}
		if _, ok := w.Wait(ctx); ok {
			t.Error("a hint the caller never read was queued instead of coalesced")
		}
	})
}

func TestFreshSubscriberGetsNeitherReplayNorReset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 7)
		publishTo(t, b, "before", "a@x.test")
		synctest.Wait()

		sub, err := b.SubscribeFrom(context.Background(), EventID{})
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		defer sub.Close()

		// A first connection has just loaded its data through REST. Replaying
		// history into it would duplicate what it already has, and resetting
		// it would make it fetch the same data twice.
		if sub.Reset {
			t.Error("a fresh connection was reset")
		}
		if len(sub.Replay) != 0 {
			t.Errorf("a fresh connection got %d replayed events", len(sub.Replay))
		}
	})
}

func TestReplayReturnsTheExactGap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 4)
		for _, id := range []string{"m1", "m2", "m3"} {
			publishTo(t, b, id, "a@x.test")
		}
		synctest.Wait()

		sub, err := b.SubscribeFrom(context.Background(), EventID{Generation: 4, Seq: 1})
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		defer sub.Close()

		if sub.Reset {
			t.Fatal("an id still inside the ring was reset")
		}
		if got := ids(sub.Replay); !slices.Equal(got, []string{"4-2", "4-3"}) {
			t.Errorf("replay %v, want the two events after 4-1", got)
		}
	})
}

// TestReplayToLiveHandoffLosesNothing walks the seam the ring exists for.
// Registration and the ring snapshot happen in one step of the bus
// goroutine's program order, so an event cannot fall between the replay a
// client is handed and the live stream it then reads.
func TestReplayToLiveHandoffLosesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 2)
		publishTo(t, b, "m1", "a@x.test")
		publishTo(t, b, "m2", "a@x.test")
		synctest.Wait()

		sub, err := b.SubscribeFrom(context.Background(), EventID{Generation: 2, Seq: 1})
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		defer sub.Close()

		publishTo(t, b, "m3", "a@x.test")
		publishTo(t, b, "m4", "a@x.test")
		synctest.Wait()

		seen := ids(sub.Replay)
		for range 2 {
			ev, ok := <-sub.Events()
			if !ok {
				t.Fatal("the live stream closed mid-handoff")
			}
			seen = append(seen, ev.ID.String())
		}
		// Contiguous from the client's last id, in order, no repeats.
		if want := []string{"2-2", "2-3", "2-4"}; !slices.Equal(seen, want) {
			t.Errorf("replay then live delivered %v, want %v", seen, want)
		}
	})
}

func TestGenerationMismatchResetsWithoutReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 5)
		publishTo(t, b, "m1", "a@x.test")
		synctest.Wait()

		// An id from a previous boot. Its seq numbers mean nothing here, so
		// replaying "everything after seq 1" would skip whatever the earlier
		// generation published after that point.
		sub, err := b.SubscribeFrom(context.Background(), EventID{Generation: 4, Seq: 1})
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		defer sub.Close()

		if !sub.Reset {
			t.Error("an id from an older generation was not reset")
		}
		if len(sub.Replay) != 0 {
			t.Errorf("a reset subscription also carried %d replayed events", len(sub.Replay))
		}
		if want := (EventID{Generation: 5, Seq: 1}); sub.Anchor != want {
			t.Errorf("reset anchor %v, want the current newest id %v", sub.Anchor, want)
		}
	})
}

func TestRingOverflowResets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 1)
		for i := range ringSize + 5 {
			publishTo(t, b, string(rune('a'+i%26)), "a@x.test")
		}
		synctest.Wait()

		// Seq 1 has been pushed out of the ring, so the gap cannot be filled.
		sub, err := b.SubscribeFrom(context.Background(), EventID{Generation: 1, Seq: 1})
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		defer sub.Close()
		if !sub.Reset {
			t.Error("an id older than the ring was not reset")
		}

		// The newest ids are still inside it.
		fresh, err := b.SubscribeFrom(context.Background(), EventID{Generation: 1, Seq: int64(ringSize + 4)})
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		defer fresh.Close()
		if fresh.Reset {
			t.Error("an id inside the ring was reset")
		}
		if got := ids(fresh.Replay); !slices.Equal(got, []string{"1-1029"}) {
			t.Errorf("replay %v, want just the last event", got)
		}
	})
}

// TestSubscriberAheadOfTheBusIsReset covers an id this generation cannot have
// issued. Trusting it would hand the client an empty replay and let it
// believe it is up to date.
func TestSubscriberAheadOfTheBusIsReset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 6)
		publishTo(t, b, "m1", "a@x.test")
		synctest.Wait()

		sub, err := b.SubscribeFrom(context.Background(), EventID{Generation: 6, Seq: 99})
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		defer sub.Close()
		if !sub.Reset {
			t.Error("an id ahead of the bus was not reset")
		}
	})
}

// TestSlowClientEvictedWithoutBlockingTheBus: a client that stops draining is
// dropped, and the bus keeps serving everybody else. Blocking here would stall
// ingest itself, since publication happens on the SMTP path.
func TestSlowClientEvictedWithoutBlockingTheBus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 1)

		slow, err := b.SubscribeFrom(context.Background(), EventID{})
		if err != nil {
			t.Fatalf("subscribe slow: %v", err)
		}
		defer slow.Close()

		total := subscriberBuffer + 1
		for i := range total {
			publishTo(t, b, "m"+string(rune('a'+i%26)), "a@x.test")
		}
		// Every publication above completed, so the bus was never waiting on
		// the client that stopped reading. Had it been, this bubble would
		// have deadlocked instead of reaching the assertions.
		synctest.Wait()

		if !slow.Evicted() {
			t.Error("the client that never drained was not evicted")
		}
		// It keeps what it had buffered, and then the stream ends.
		buffered := 0
		for range slow.Events() {
			buffered++
		}
		if buffered != subscriberBuffer {
			t.Errorf("evicted client held %d events, want the full buffer of %d", buffered, subscriberBuffer)
		}

		// The bus is still live: it accepted every publication above and
		// answers a new subscriber.
		after, err := b.SubscribeFrom(context.Background(), EventID{})
		if err != nil {
			t.Fatalf("the bus stopped serving after an eviction: %v", err)
		}
		defer after.Close()
		publishTo(t, b, "later", "a@x.test")
		synctest.Wait()
		ev, ok := <-after.Events()
		if !ok {
			t.Fatal("a new subscriber got nothing after an eviction")
		}
		if want := (EventID{Generation: 1, Seq: int64(total) + 1}); ev.ID != want {
			t.Errorf("event id %v, want %v", ev.ID, want)
		}
	})
}

func ids(events []Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.ID.String())
	}
	return out
}
