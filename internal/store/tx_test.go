package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/store"
)

// TestWithTxRollsBackEveryWrite is the seam's whole purpose. Ingest
// commits a message, its recipients and two ledger events together, and
// what makes that safe is that a failure anywhere leaves none of them.
func TestWithTxRollsBackEveryWrite(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	sentinel := errors.New("caller changed its mind")
	err := s.WithTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.InsertMessage(ctx, store.Message{
			ProjectID:  project.ID,
			FromAddr:   "a@acme.example",
			Subject:    "rolled back",
			TextBody:   "code 481920",
			ReceivedAt: time.Now(),
			Recipients: []store.Recipient{{Addr: "user@demo.test", Kind: store.RecipientEnvelope}},
		}); err != nil {
			return err
		}
		if _, err := tx.AppendLedger(ctx, store.LedgerEntry{
			ProjectID: project.ID,
			Actor:     store.ActorSystem,
			Action:    "mail.received",
		}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTx = %v, want the caller's error", err)
	}

	msgs, err := s.ListMessages(ctx, store.MessageFilter{IncludeQuarantined: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("%d messages survived a rolled back transaction", len(msgs))
	}
	entries, err := s.ListLedger(ctx, store.LedgerFilter{ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("%d ledger entries survived a rolled back transaction", len(entries))
	}
}

// TestWithTxCommitsEveryWriteTogether is the other direction: the same
// composition, allowed to finish.
func TestWithTxCommitsEveryWriteTogether(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	err := s.WithTx(ctx, func(tx *store.Tx) error {
		m, err := tx.InsertMessage(ctx, store.Message{
			ProjectID:  project.ID,
			FromAddr:   "a@acme.example",
			Subject:    "kept",
			TextBody:   "code 481920",
			ReceivedAt: time.Now(),
			Recipients: []store.Recipient{{Addr: "user@demo.test", Kind: store.RecipientEnvelope}},
		})
		if err != nil {
			return err
		}
		_, err = tx.AppendLedger(ctx, store.LedgerEntry{
			ProjectID:  project.ID,
			Actor:      store.ActorSystem,
			Action:     "mail.received",
			DetailJSON: `{"message_id":"` + m.ID + `"}`,
		})
		return err
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	msgs, err := s.ListMessages(ctx, store.MessageFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("%d messages committed, want 1", len(msgs))
	}
	if len(msgs[0].Recipients) != 1 {
		t.Errorf("the recipients did not commit with the message")
	}
	entries, err := s.ListLedger(ctx, store.LedgerFilter{ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("%d ledger entries committed, want 1", len(entries))
	}
}
