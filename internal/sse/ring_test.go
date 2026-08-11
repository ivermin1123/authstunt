package sse

import (
	"slices"
	"testing"
)

func fillRing(size int, count int) *ring {
	r := newRing(size)
	for i := 1; i <= count; i++ {
		r.add(Event{ID: EventID{Generation: 1, Seq: int64(i)}})
	}
	return r
}

func TestRingKeepsTheNewestEventsInOrder(t *testing.T) {
	r := fillRing(4, 6)
	if got := ids(r.snapshot()); !slices.Equal(got, []string{"1-3", "1-4", "1-5", "1-6"}) {
		t.Errorf("snapshot %v", got)
	}
}

func TestRingSnapshotAfterExactWrap(t *testing.T) {
	// Exactly full and exactly wrapped is the off-by-one case: the write
	// position is back at zero, which also happens to be the oldest element.
	r := fillRing(4, 8)
	if got := ids(r.snapshot()); !slices.Equal(got, []string{"1-5", "1-6", "1-7", "1-8"}) {
		t.Errorf("snapshot %v", got)
	}
}

func TestRingAfter(t *testing.T) {
	tests := []struct {
		name       string
		size       int
		published  int
		from       int64
		want       []string
		replayable bool
	}{
		{name: "nothing published, client has seen nothing", size: 4, published: 0, from: 0, replayable: true},
		{name: "nothing published, client claims an event", size: 4, published: 0, from: 3},
		{name: "gap inside the ring", size: 4, published: 3, from: 1, want: []string{"1-2", "1-3"}, replayable: true},
		{name: "already current", size: 4, published: 3, from: 3, replayable: true},
		{name: "the event just before the oldest kept", size: 4, published: 6, from: 2, want: []string{"1-3", "1-4", "1-5", "1-6"}, replayable: true},
		{name: "one older than that", size: 4, published: 6, from: 1},
		{name: "ahead of the bus", size: 4, published: 3, from: 4},
		{name: "from the start with room to spare", size: 8, published: 3, from: 0, want: []string{"1-1", "1-2", "1-3"}, replayable: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := fillRing(tc.size, tc.published)
			got, ok := r.after(EventID{Generation: 1, Seq: tc.from})
			if ok != tc.replayable {
				t.Fatalf("replayable = %v, want %v", ok, tc.replayable)
			}
			if !ok {
				return
			}
			if gotIDs := ids(got); !slices.Equal(gotIDs, tc.want) {
				t.Errorf("after(%d) = %v, want %v", tc.from, gotIDs, tc.want)
			}
		})
	}
}
