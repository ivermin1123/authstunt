package store_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/store"
)

// insertMessage adds one message addressed to addr.
func insertMessage(t *testing.T, s *store.Store, projectID, subject, addr string, receivedAt time.Time) store.Message {
	t.Helper()
	m, err := s.InsertMessage(t.Context(), store.Message{
		ProjectID: projectID, FromAddr: "app@demo.test", Subject: subject,
		TextBody: "code for " + subject, ReceivedAt: receivedAt,
		Recipients: []store.Recipient{{Addr: addr, Kind: store.RecipientEnvelope}},
	})
	if err != nil {
		t.Fatalf("insert %s: %v", subject, err)
	}
	return m
}

func TestListMessagesMarksUnreadableBody(t *testing.T) {
	ctx := t.Context()
	s, logs := openTestStoreWithLogs(t)
	project := newProject(t, s)
	readable := insertMessage(t, s, project.ID, "fine", "u@demo.test", time.Time{})
	broken := insertMessage(t, s, project.ID, "broken", "u@demo.test", time.Time{})
	if err := s.CorruptBodyForTest(ctx, broken.ID); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListMessages(ctx, store.MessageFilter{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("one unreadable row failed the whole page: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("listing returned %d messages, want 2", len(got))
	}
	byID := map[string]store.Message{got[0].ID: got[0], got[1].ID: got[1]}
	if m := byID[broken.ID]; !m.Unreadable || m.TextBody != "" {
		t.Errorf("broken row = %+v, want unreadable with no body", m)
	}
	if m := byID[broken.ID]; m.Subject != "broken" || len(m.Recipients) != 1 {
		t.Errorf("broken row lost its metadata: %+v", m)
	}
	if m := byID[readable.ID]; m.Unreadable || m.TextBody == "" {
		t.Errorf("readable row was degraded too: %+v", m)
	}
	if !strings.Contains(logs.String(), broken.ID) {
		t.Error("the unreadable row was not logged")
	}
}

func TestListMessagesMarksUnreadableExtraction(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)
	m, err := s.InsertMessage(ctx, store.Message{
		ProjectID: project.ID, FromAddr: "app@demo.test", Subject: "extracted",
		TextBody: "code 483920", ExtractedJSON: `{"otp":"483920"}`,
		Recipients: []store.Recipient{{Addr: "u@demo.test", Kind: store.RecipientEnvelope}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The body still opens; only the extraction is gone. The row is still
	// unreadable: half a message is not a message.
	if err := s.CorruptExtractionForTest(ctx, m.ID); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListMessages(ctx, store.MessageFilter{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("a broken extraction failed the page: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("listing returned %d messages, want 1", len(got))
	}
	if !got[0].Unreadable || got[0].ExtractedJSON != "" || got[0].TextBody != "" {
		t.Errorf("row = %+v, want unreadable with neither payload", got[0])
	}
}

func TestMessageReturnsCorruptPayloadError(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)
	m := insertMessage(t, s, project.ID, "broken", "u@demo.test", time.Time{})
	if err := s.CorruptBodyForTest(ctx, m.ID); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		read func() (store.Message, error)
	}{
		{"Message", func() (store.Message, error) { return s.Message(ctx, m.ID) }},
		{"MessageIncludingQuarantined", func() (store.Message, error) {
			return s.MessageIncludingQuarantined(ctx, m.ID)
		}},
	} {
		if _, err := tc.read(); !errors.Is(err, store.ErrUnreadableMessage) {
			t.Errorf("%s returned %v, want ErrUnreadableMessage", tc.name, err)
		}
	}
}

func TestUnreadableReadWritesNoLedgerEntry(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)
	m := insertMessage(t, s, project.ID, "broken", "u@demo.test", time.Time{})
	if err := s.CorruptBodyForTest(ctx, m.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ListMessages(ctx, store.MessageFilter{ProjectID: project.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Message(ctx, m.ID); err == nil {
		t.Fatal("by-id read of an unreadable row succeeded")
	}

	// The ledger records what actors did. A read that could not decrypt is
	// not an act, and a read path that writes would also turn any list
	// endpoint into an unbounded writer.
	entries, err := s.ListLedger(ctx, store.LedgerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("degraded reads wrote %d ledger entries, want 0: %+v", len(entries), entries)
	}
}

func TestExtractionStatePendingUntilTerminal(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)
	succeeding := insertMessage(t, s, project.ID, "ok", "u@demo.test", time.Time{})
	failing := insertMessage(t, s, project.ID, "panics", "u@demo.test", time.Time{})

	pending, err := s.ListPendingExtractions(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("%d rows pending right after insert, want 2", len(pending))
	}

	if err := s.SetExtraction(ctx, succeeding.ID, `{"otp":"483920"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.FailExtraction(ctx, failing.ID); err != nil {
		t.Fatal(err)
	}

	got, err := s.Message(ctx, succeeding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExtractionState != store.ExtractionSuccess || got.ExtractedJSON != `{"otp":"483920"}` {
		t.Errorf("succeeded row = (%q, %q)", got.ExtractionState, got.ExtractedJSON)
	}
	failed, err := s.Message(ctx, failing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.ExtractionState != store.ExtractionFailed || failed.ExtractedJSON != "" {
		t.Errorf("failed row = (%q, %q), want failed with no extraction",
			failed.ExtractionState, failed.ExtractedJSON)
	}

	// A row leaves pending exactly once. A second attempt is a bug in the
	// caller - two extractors on one message, or recovery picking up mail
	// already handled - so it is refused rather than applied.
	secondAttempts := []struct {
		name string
		run  func() error
	}{
		{"success then success", func() error { return s.SetExtraction(ctx, succeeding.ID, `{"otp":"000000"}`) }},
		{"success then failure", func() error { return s.FailExtraction(ctx, succeeding.ID) }},
		{"failure then success", func() error { return s.SetExtraction(ctx, failing.ID, `{"otp":"000000"}`) }},
		{"failure then failure", func() error { return s.FailExtraction(ctx, failing.ID) }},
	}
	for _, tc := range secondAttempts {
		if err := tc.run(); !errors.Is(err, store.ErrExtractionSettled) {
			t.Errorf("%s returned %v, want ErrExtractionSettled", tc.name, err)
		}
	}

	// Neither the state nor the result moved.
	after, err := s.Message(ctx, succeeding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ExtractionState != store.ExtractionSuccess || after.ExtractedJSON != `{"otp":"483920"}` {
		t.Errorf("refused transitions still changed the row: %+v", after)
	}

	// Terminal rows are invisible to recovery, so a failure is never
	// retried forever.
	pending, err = s.ListPendingExtractions(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("recovery still sees %d terminal rows, want 0", len(pending))
	}

	// A message that arrives with its extraction already computed has
	// nothing left to do and never enters the pending set.
	withResult, err := s.InsertMessage(ctx, store.Message{
		ProjectID: project.ID, FromAddr: "app@demo.test", Subject: "pre-extracted",
		TextBody: "code 111111", ExtractedJSON: `{"otp":"111111"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if withResult.ExtractionState != store.ExtractionSuccess {
		t.Errorf("pre-extracted insert = %q, want %q",
			withResult.ExtractionState, store.ExtractionSuccess)
	}
}

func TestSetExtractionOnMissingMessage(t *testing.T) {
	s := openTestStore(t)
	if err := s.SetExtraction(t.Context(), "ffffffffffff", `{}`); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetExtraction on an absent message = %v, want ErrNotFound", err)
	}
	if err := s.FailExtraction(t.Context(), "ffffffffffff"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("FailExtraction on an absent message = %v, want ErrNotFound", err)
	}
}

func TestMessageCursorDoesNotSkipSameMillisecond(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)
	at := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	// Two messages in one millisecond is the ordinary case, not an edge
	// one: a signup mails a code and a welcome note together.
	first := insertMessage(t, s, project.ID, "code", "u@demo.test", at)
	second := insertMessage(t, s, project.ID, "welcome", "u@demo.test", at)

	page, err := s.ListMessages(ctx, store.MessageFilter{ProjectID: project.ID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 {
		t.Fatalf("first page held %d messages, want 1", len(page))
	}
	next, err := s.ListMessages(ctx, store.MessageFilter{
		ProjectID: project.ID, Limit: 1, Cursor: store.CursorFor(page[0]),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 {
		t.Fatalf("the second message of the millisecond was skipped: %d rows", len(next))
	}
	if next[0].ID == page[0].ID {
		t.Fatal("the cursor repeated the message it pointed at")
	}
	seen := map[string]bool{page[0].ID: true, next[0].ID: true}
	if !seen[first.ID] || !seen[second.ID] {
		t.Fatalf("pages covered %v, want both %s and %s", seen, first.ID, second.ID)
	}
}

func TestMessageCursorPagesIdenticalTimestampsWithoutSkipOrDuplicate(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	// Every message shares one timestamp, so the id is the only thing that
	// can order them and the cursor is the only thing that can continue.
	at := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	const total = 25
	want := make(map[string]bool, total)
	for i := range total {
		m := insertMessage(t, s, project.ID, fmt.Sprintf("mail-%02d", i), "u@demo.test", at)
		want[m.ID] = true
	}
	// One older message the `since` bound must keep out of every page.
	older := insertMessage(t, s, project.ID, "history", "u@demo.test", at.Add(-time.Hour))

	for _, pageSize := range []int{1, 3, 7} {
		t.Run(fmt.Sprintf("limit-%d", pageSize), func(t *testing.T) {
			// since is the default of a live caller: "everything after the
			// moment I asked". It stays an independent lower bound and is
			// resent unchanged with every page, next to the cursor.
			since := at.Add(-time.Minute)
			filter := store.MessageFilter{ProjectID: project.ID, Limit: pageSize, Since: since}
			seen := map[string]int{}
			for pages := 0; ; pages++ {
				if pages > total+1 {
					t.Fatal("paging did not terminate")
				}
				page, err := s.ListMessages(ctx, filter)
				if err != nil {
					t.Fatal(err)
				}
				if len(page) == 0 {
					break
				}
				for _, m := range page {
					seen[m.ID]++
				}
				// next_cursor always comes from the last item actually
				// emitted, never from the last row read.
				filter.Cursor = store.CursorFor(page[len(page)-1])
			}
			if len(seen) != total {
				t.Fatalf("saw %d distinct messages, want %d", len(seen), total)
			}
			for id, count := range seen {
				if count != 1 {
					t.Errorf("message %s appeared %d times", id, count)
				}
				if !want[id] {
					t.Errorf("message %s was outside the since bound", id)
				}
			}
			if seen[older.ID] != 0 {
				t.Error("since was folded into the cursor: the older message leaked in")
			}
		})
	}
}

func TestMessageCursorTokenRoundTripsAndRejectsJunk(t *testing.T) {
	original := store.MessageCursor{
		ReceivedAt: time.Date(2026, 8, 11, 10, 0, 0, 123456789, time.UTC),
		ID:         "0123456789ab",
	}
	got, err := store.DecodeMessageCursor(original.Encode())
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if !got.ReceivedAt.Equal(original.ReceivedAt) || got.ID != original.ID {
		t.Errorf("round trip returned %+v, want %+v", got, original)
	}

	for _, token := range []string{
		"", "not base64 $$", "YWJj", // valid base64, wrong shape
		store.MessageCursor{ReceivedAt: original.ReceivedAt, ID: "nope"}.Encode(),
	} {
		if _, err := store.DecodeMessageCursor(token); !errors.Is(err, store.ErrBadCursor) {
			t.Errorf("DecodeMessageCursor(%q) = %v, want ErrBadCursor", token, err)
		}
	}
}
