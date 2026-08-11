package sse

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// recordingSink stands in for the HTTP response. Real socket writes are the
// one thing a synctest bubble cannot see through, which is why Serve takes a
// Sink at all.
type recordingSink struct {
	mu      sync.Mutex
	written bytes.Buffer
	flushes int
	failOn  int // 1-based write to fail on; 0 never fails
	writes  int
}

func (s *recordingSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if s.failOn != 0 && s.writes == s.failOn {
		return 0, errors.New("client went away")
	}
	return s.written.Write(p)
}

func (s *recordingSink) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushes++
	return nil
}

func (s *recordingSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written.String()
}

func (s *recordingSink) flushCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushes
}

// serve runs Serve for the length of the bubble and hands back the sink and
// the error it finished with.
func serve(t *testing.T, b *Bus, sink *recordingSink, lastEventID string) (stop func(), result chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	result = make(chan error, 1)
	// Cleanup waits on its own channel rather than on result, which a test
	// that reads the error itself would already have emptied.
	done := make(chan struct{})
	go func() {
		defer close(done)
		result <- Serve(ctx, b, sink, lastEventID)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return cancel, result
}

func TestServeWritesOneFramePerEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 2)
		sink := &recordingSink{}
		serve(t, b, sink, "")
		synctest.Wait()

		publishTo(t, b, "m1", "a@x.test")
		publishTo(t, b, "m2", "a@x.test")
		synctest.Wait()

		want := "id: 2-1\nevent: message\ndata: {\"id\":\"m1\"}\n\n" +
			"id: 2-2\nevent: message\ndata: {\"id\":\"m2\"}\n\n"
		if got := sink.String(); got != want {
			t.Errorf("stream:\n%q\nwant:\n%q", got, want)
		}
		// Every frame is flushed, or the client sits waiting on a buffer the
		// server has already filled.
		if got := sink.flushCount(); got != 2 {
			t.Errorf("%d flushes for 2 events", got)
		}
	})
}

func TestServeReplaysBeforeLiveEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 1)
		publishTo(t, b, "m1", "a@x.test")
		publishTo(t, b, "m2", "a@x.test")
		synctest.Wait()

		sink := &recordingSink{}
		serve(t, b, sink, "1-1")
		synctest.Wait()

		publishTo(t, b, "m3", "a@x.test")
		synctest.Wait()

		if got, want := frameIDs(sink.String()), []string{"1-2", "1-3"}; strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("frames %v, want the replayed gap then the live event %v", got, want)
		}
		if strings.Contains(sink.String(), "event: "+ResetEventName) {
			t.Error("a client whose id was still in the ring was reset")
		}
	})
}

func TestServeSendsExactlyOneResetOnGenerationMismatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 9)
		publishTo(t, b, "m1", "a@x.test")
		synctest.Wait()

		sink := &recordingSink{}
		serve(t, b, sink, "8-4")
		synctest.Wait()
		publishTo(t, b, "m2", "a@x.test")
		synctest.Wait()

		stream := sink.String()
		if n := strings.Count(stream, "event: "+ResetEventName); n != 1 {
			t.Errorf("%d reset events, want exactly 1:\n%s", n, stream)
		}
		// The reset carries a usable id, so a client that drops right after
		// it reconnects from here instead of presenting the same stale id and
		// being reset again.
		if !strings.HasPrefix(stream, "id: 9-1\nevent: "+ResetEventName+"\ndata: {}\n\n") {
			t.Errorf("reset frame not anchored to the current id:\n%q", stream)
		}
		// And the live stream continues straight after it.
		if got, want := frameIDs(stream), []string{"9-1", "9-2"}; strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("frames %v, want %v", got, want)
		}
	})
}

func TestServeResetsAMalformedLastEventID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 3)
		sink := &recordingSink{}
		serve(t, b, sink, "not-an-id")
		synctest.Wait()

		// A client holding an id we cannot read still believes it has state,
		// so it is told to refetch rather than handed an error it cannot act
		// on or a silent empty stream.
		if n := strings.Count(sink.String(), "event: "+ResetEventName); n != 1 {
			t.Errorf("a malformed Last-Event-ID produced %d resets, want 1", n)
		}
	})
}

func TestServeSplitsMultiLineData(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 1)
		sink := &recordingSink{}
		serve(t, b, sink, "")
		synctest.Wait()

		// A payload with a line break has to become one data field per line,
		// because that is how the receiver rebuilds it. Emitting the raw
		// break would end the frame early and turn the rest into fields the
		// client cannot parse.
		if err := b.Publish(context.Background(), Publication{Name: "note", Data: []byte("first\r\nsecond")}); err != nil {
			t.Fatalf("publish: %v", err)
		}
		synctest.Wait()

		want := "id: 1-1\nevent: note\ndata: first\ndata: second\n\n"
		if got := sink.String(); got != want {
			t.Errorf("stream:\n%q\nwant:\n%q", got, want)
		}
	})
}

// TestServeStillDispatchesAnEmptyPayload: a frame whose data field is missing
// is never dispatched by the receiver, so an event with no payload still
// carries an empty data line rather than none.
func TestServeStillDispatchesAnEmptyPayload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 1)
		sink := &recordingSink{}
		serve(t, b, sink, "")
		synctest.Wait()

		if err := b.Publish(context.Background(), Publication{Name: "ping"}); err != nil {
			t.Fatalf("publish: %v", err)
		}
		synctest.Wait()

		if want := "id: 1-1\nevent: ping\ndata: \n\n"; sink.String() != want {
			t.Errorf("stream %q, want %q", sink.String(), want)
		}
	})
}

func TestServeReportsEviction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 1)
		// A sink that fails its first write leaves Serve holding a live
		// subscription it never drains, which is exactly the slow client the
		// bus evicts.
		sink := &recordingSink{failOn: 1}
		_, result := serve(t, b, sink, "")
		synctest.Wait()

		publishTo(t, b, "m1", "a@x.test")
		synctest.Wait()

		if err := <-result; err == nil || !strings.Contains(err.Error(), "client went away") {
			t.Errorf("Serve returned %v, want the sink's write error", err)
		}
	})
}

func TestServeEndsWhenTheBusStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := NewBus(1)
		busCtx, stopBus := context.WithCancel(context.Background())
		busDone := make(chan struct{})
		go func() {
			defer close(busDone)
			b.Run(busCtx)
		}()

		sink := &recordingSink{}
		result := make(chan error, 1)
		go func() { result <- Serve(context.Background(), b, sink, "") }()
		synctest.Wait()

		stopBus()
		<-busDone

		if err := <-result; !errors.Is(err, ErrBusStopped) {
			t.Errorf("Serve returned %v, want ErrBusStopped", err)
		}
	})
}

func TestServeStopsOnContextCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 1)
		sink := &recordingSink{}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- Serve(ctx, b, sink, "") }()
		synctest.Wait()

		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Errorf("Serve returned %v, want context.Canceled", err)
		}
		// The subscription is gone with it, so the bus is not left fanning
		// out to a connection nobody is reading.
		publishTo(t, b, "after", "a@x.test")
		synctest.Wait()
		if _, _, found := strings.Cut(sink.String(), "after"); found {
			t.Error("a canceled connection was still being written to")
		}
	})
}

// TestWaiterAndSubscriberSeeTheSameEvent: one message serves the machine
// client that is blocked on it and the dashboard watching live, and the
// waiter is woken first as design 2.2 orders it.
func TestWaiterAndSubscriberSeeTheSameEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := startBus(t, 4)
		w, err := b.SubscribeMatch(context.Background(), matchTo("a@x.test"))
		if err != nil {
			t.Fatalf("subscribe match: %v", err)
		}
		defer w.Close()
		sink := &recordingSink{}
		serve(t, b, sink, "")
		synctest.Wait()

		publishTo(t, b, "m1", "a@x.test")
		synctest.Wait()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, ok := w.Wait(ctx); !ok {
			t.Error("the waiter did not see the message")
		}
		if !strings.Contains(sink.String(), "id: 4-1\n") {
			t.Errorf("the subscriber did not see the message:\n%q", sink.String())
		}
	})
}

// frameIDs pulls the id of every frame out of a stream, in order.
func frameIDs(stream string) []string {
	var out []string
	for _, line := range strings.Split(stream, "\n") {
		if id, ok := strings.CutPrefix(line, "id: "); ok {
			out = append(out, id)
		}
	}
	return out
}
