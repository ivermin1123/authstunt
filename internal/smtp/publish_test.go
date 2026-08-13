package smtp_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/extract"
	"github.com/ivermin1123/authstunt/internal/personas"
	"github.com/ivermin1123/authstunt/internal/store"
)

// TestAClaimIsNotStrandedWhenTheAnnouncementLosesItsContext is the proof for
// the failure the publish path used to swallow.
//
// The window is real and narrow: a worker commits a message's terminal
// extraction state, shutdown cancels the context that worker was carrying,
// and the announcement made a moment later is refused by the bus because the
// context is done. Nothing is wrong with the message. It is stored, settled
// and claimable. The only thing missing is that nobody woke the caller
// parked on it, so the caller waits out its whole budget and is told the
// wait ran out - about mail that had already arrived and been read.
//
// The test builds that state directly rather than trying to hit the race by
// timing: the message is stored and settled, and the announcement is made
// with a context that is already done.
func TestAClaimIsNotStrandedWhenTheAnnouncementLosesItsContext(t *testing.T) {
	h := newHarness(t, []string{"demo.test"})
	ctx := t.Context()

	leases, err := personas.New(personas.Config{
		Store:     h.store,
		ProjectID: h.projectID,
		Allowlist: []string{"demo.test"},
		Bus:       h.bus,
		Logger:    slog.New(slog.NewTextHandler(&strings.Builder{}, nil)),
	})
	if err != nil {
		t.Fatalf("lease service: %v", err)
	}

	run, _, err := leases.CreateRun(ctx)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	lease, err := leases.Acquire(ctx, run.ID, "signup", store.ModeEphemeral)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// The claim has to be parked before the mail exists. A claim that finds
	// the message on its first query never reaches the wait, which is the
	// half of the path this test is about.
	type outcome struct {
		claimed personas.Claimed
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		c, err := leases.Claim(ctx, personas.ClaimRequest{
			LeaseID:        lease.LeaseID,
			Kind:           store.ClaimEmailOTP,
			IdempotencyKey: "stranded-1",
			Timeout:        4 * time.Second,
		})
		done <- outcome{c, err}
	}()

	// Registering the matcher is the first thing Claim does, so this only
	// has to outlast a few statements. The claim's own budget is what keeps
	// the test bounded if this is ever not enough.
	time.Sleep(500 * time.Millisecond)

	// The message arrives and settles exactly as a worker would leave it,
	// with no announcement yet.
	h.deliver(ctx, "bounce@acme.example", []string{lease.Addr}, mailTo(lease.Addr))
	msgs, err := h.store.ListMessages(ctx, store.MessageFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected one stored message, got %d", len(msgs))
	}
	msg := msgs[0]
	res := extract.Extract(extract.Input{
		Subject: msg.Subject, Text: msg.TextBody,
	})
	encoded, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("encode extraction: %v", err)
	}
	if err := h.store.SetExtraction(ctx, msg.ID, string(encoded)); err != nil {
		t.Fatalf("settle: %v", err)
	}

	// Shutdown got here between the commit and the announcement.
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	h.ingest.PublishForTest(dead, msg)

	got := <-done
	if got.err != nil {
		t.Fatalf("claim: %v", got.err)
	}
	if got.claimed.Reason != store.ReasonOK {
		t.Fatalf("claim reason = %q, want %q: a settled message left a caller "+
			"waiting because the announcement carried a canceled context",
			got.claimed.Reason, store.ReasonOK)
	}
	if got.claimed.Value != "481920" {
		t.Errorf("claimed value = %q, want the code from the message", got.claimed.Value)
	}
}
