package store_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/store"
)

// These lock down defects found in review. Each one passed the original
// suite, so the tests here deliberately use the shapes that suite missed.

// TestTimestampsSortLexicographically is the guard for the ordering bug.
// Timestamps live in TEXT columns, so SQLite compares them byte by byte.
// The original suite spaced its fixtures one whole second apart, which
// happened to avoid the only region where the two orders disagree.
func TestTimestampsSortLexicographically(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)
	addr := "order@demo.test"

	// A whole second, then half a second later. Formats that trim
	// trailing zeros render these as "...:00Z" and "...:00.5Z", and 'Z'
	// sorts above '.', so the earlier message would win a DESC ordering.
	whole := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	half := whole.Add(500 * time.Millisecond)

	insert := func(subject string, at time.Time) store.Message {
		t.Helper()
		m, err := s.InsertMessage(ctx, store.Message{
			ProjectID: project.ID, FromAddr: "app@test", Subject: subject, ReceivedAt: at,
			Recipients: []store.Recipient{{Addr: addr, Kind: store.RecipientEnvelope}},
		})
		if err != nil {
			t.Fatalf("insert %s: %v", subject, err)
		}
		return m
	}
	earlier := insert("earlier-whole-second", whole)
	later := insert("later-half-second", half)

	earlierRaw, err := s.RawTimestampForTest(ctx, earlier.ID)
	if err != nil {
		t.Fatal(err)
	}
	laterRaw, err := s.RawTimestampForTest(ctx, later.ID)
	if err != nil {
		t.Fatal(err)
	}
	if earlierRaw >= laterRaw {
		t.Fatalf("stored timestamps do not sort by time: %q must be < %q", earlierRaw, laterRaw)
	}

	got, err := s.ListMessages(ctx, store.MessageFilter{ProjectID: project.ID, To: addr})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	if got[0].ID != later.ID {
		t.Fatalf("newest first violated: %q came first", got[0].Subject)
	}

	// A poller whose cursor is the earlier message must still see the
	// later one. Losing it here would lose it forever, not just delay it.
	after, err := s.ListMessages(ctx, store.MessageFilter{
		ProjectID: project.ID, To: addr, Since: whole,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].ID != later.ID {
		t.Fatalf("since=%v returned %d messages, want only the later one", whole, len(after))
	}

	// And a matcher scoped to "from now" must not match older mail: a
	// reused persona address must never return last night's OTP.
	stale, err := s.ListMessages(ctx, store.MessageFilter{
		ProjectID: project.ID, To: addr, Since: half,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("since=%v matched %d older messages", half, len(stale))
	}
}

// TestUnreadableRowStaysDeletable guards the recovery path: a message
// whose body no longer opens must not become permanent.
func TestUnreadableRowStaysDeletable(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	rawRef, err := s.Blobs.Put([]byte("raw mime"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.InsertMessage(ctx, store.Message{
		ProjectID: project.ID, FromAddr: "a@b.test", Subject: "s", RawRef: rawRef,
		TextBody:   "code 483920",
		Recipients: []store.Recipient{{Addr: "u@demo.test", Kind: store.RecipientEnvelope}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CorruptBodyForTest(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	// Reading it fails, which is correct: unauthenticated bytes are never
	// handed back as if they were the body.
	if _, err := s.Message(ctx, m.ID); err == nil {
		t.Fatal("a body that fails authentication was returned as valid")
	}
	// Deleting it must still work, and must still surrender the blob ref
	// so retention can reclaim the file.
	gotRaw, _, err := s.DeleteMessage(ctx, m.ID)
	if err != nil {
		t.Fatalf("unreadable message could not be deleted: %v", err)
	}
	if gotRaw != rawRef {
		t.Fatalf("blob ref = %q, want %q", gotRaw, rawRef)
	}
	if err := s.Blobs.Delete(gotRaw); err != nil {
		t.Fatalf("blob cleanup: %v", err)
	}
}

// TestQuarantinedMessageHiddenFromSingleRead covers the path behind
// /api/v1/messages/{id}, /otp and /links. Quarantine is unconditional
// outside the dashboard, so the default lookup must not serve one.
func TestQuarantinedMessageHiddenFromSingleRead(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	m, err := s.InsertMessage(ctx, store.Message{
		ProjectID: project.ID, FromAddr: "attacker@elsewhere.test", Subject: "off allowlist",
		TextBody: "code 000000", Quarantined: true,
		Recipients: []store.Recipient{{Addr: "someone@not-allowed.test", Kind: store.RecipientEnvelope}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Message(ctx, m.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("quarantined message served by Message: err = %v", err)
	}
	got, err := s.MessageIncludingQuarantined(ctx, m.ID)
	if err != nil {
		t.Fatalf("dashboard lookup: %v", err)
	}
	if !got.Quarantined || got.TextBody != "code 000000" {
		t.Fatalf("dashboard lookup returned %+v", got)
	}
}

// TestListMessagesBeyondRecipientBatch proves the listing survives more
// messages than fit in one IN clause. SQLite caps bound variables at
// 32766, and an unbounded listing used to build one placeholder per row.
func TestListMessagesBeyondRecipientBatch(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)
	addr := "bulk@demo.test"

	const count = 1100 // more than two batches
	base := s.Now()
	for i := range count {
		_, err := s.InsertMessage(ctx, store.Message{
			ProjectID: project.ID, FromAddr: "app@test",
			Subject:    fmt.Sprintf("msg %d", i),
			ReceivedAt: base.Add(time.Duration(i) * time.Millisecond),
			Recipients: []store.Recipient{{Addr: addr, Kind: store.RecipientEnvelope}},
		})
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	got, err := s.ListMessages(ctx, store.MessageFilter{ProjectID: project.ID, To: addr})
	if err != nil {
		t.Fatalf("unbounded listing: %v", err)
	}
	if len(got) != count {
		t.Fatalf("listed %d messages, want %d", len(got), count)
	}
	for _, m := range got {
		if len(m.Recipients) != 1 {
			t.Fatalf("message %s lost its recipients across the batch boundary", m.ID)
		}
	}
}

// TestAllowlistKeepsDeclarationOrder locks the ordering contract: persona
// emails are generated under the first pattern, so sorting would move new
// personas to a different domain.
func TestAllowlistKeepsDeclarationOrder(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	// Declared order is deliberately not alphabetical order.
	declared := []string{"*.zulu.test", "*.alpha.test", "*.mike.test"}
	if err := s.SetAllowlist(ctx, project.ID, declared); err != nil {
		t.Fatal(err)
	}
	got, err := s.Allowlist(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(declared) {
		t.Fatalf("got %d patterns, want %d", len(got), len(declared))
	}
	for i, want := range declared {
		if got[i] != want {
			t.Fatalf("pattern %d = %q, want %q (declaration order lost)", i, got[i], want)
		}
	}
}

// TestAddressMatchingIsCaseInsensitive covers what SMTP will actually
// deliver: the domain part is case-insensitive, so a target app sending to
// User@Demo.Test must reach the persona that asked for the lowercase
// address.
func TestAddressMatchingIsCaseInsensitive(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	persona, err := s.CreatePersona(ctx, store.Persona{
		ProjectID: project.ID, Name: "mixed", Email: "Free-User@Demo.Test",
		PasswordEnc: []byte("sealed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if persona.Email != "Free-User@demo.test" {
		t.Fatalf("stored email = %q, want the domain lowercased", persona.Email)
	}
	for _, lookup := range []string{"Free-User@Demo.Test", "free-user@demo.test"} {
		got, err := s.PersonaByEmail(ctx, lookup)
		if err != nil {
			t.Fatalf("PersonaByEmail(%q): %v", lookup, err)
		}
		if got.ID != persona.ID {
			t.Fatalf("PersonaByEmail(%q) found a different persona", lookup)
		}
	}

	if _, err := s.InsertMessage(ctx, store.Message{
		ProjectID: project.ID, FromAddr: "app@test", Subject: "Verify",
		Recipients: []store.Recipient{{Addr: "Free-User@Demo.Test", Kind: store.RecipientEnvelope}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, lookup := range []string{"free-user@demo.test", "FREE-USER@DEMO.TEST"} {
		found, err := s.ListMessages(ctx, store.MessageFilter{ProjectID: project.ID, To: lookup})
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != 1 {
			t.Fatalf("to=%q matched %d messages, want 1", lookup, len(found))
		}
	}
}

// TestLedgerSurvivesProjectDeletion holds the audit trail to its promise:
// an audit trail a delete can erase is not one.
func TestLedgerSurvivesProjectDeletion(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	if _, err := s.AppendLedger(ctx, store.LedgerEntry{
		ProjectID: project.ID, Actor: store.ActorREST, Action: "secret.read", RunID: "run-9",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProjectForTest(ctx, project.ID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	if _, err := s.Project(ctx, project.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("project still present: %v", err)
	}
	entries, err := s.ListLedger(ctx, store.LedgerFilter{RunID: "run-9"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Action != "secret.read" {
		t.Fatalf("audit trail lost with the project: %d entries", len(entries))
	}
}

// TestSetExtractionRejectsEmpty keeps the NULL result meaning "the
// extractor panicked" rather than "someone passed an empty string".
func TestSetExtractionRejectsEmpty(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	m, err := s.InsertMessage(ctx, store.Message{
		ProjectID: project.ID, FromAddr: "a@b.test", Subject: "s",
		ExtractedJSON: `{"otp_best":"111222"}`,
		Recipients:    []store.Recipient{{Addr: "u@demo.test", Kind: store.RecipientEnvelope}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetExtraction(ctx, m.ID, ""); err == nil {
		t.Fatal("empty extraction accepted, silently erasing a stored result")
	}
	got, err := s.Message(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExtractedJSON != `{"otp_best":"111222"}` {
		t.Fatalf("extraction was modified: %q", got.ExtractedJSON)
	}
}
