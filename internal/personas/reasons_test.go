package personas_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/personas"
	"github.com/ivermin1123/authstunt/internal/store"
)

// Every reason code the contract defines gets a test that produces it.
// The set is closed: a code nothing can produce is a code phase 4 would
// map to an error nobody ever sees, and a code produced by nothing is
// indistinguishable from a code produced by a bug.

// bindDirectly writes a message and its binding through the store rather
// than through SMTP, for the states real mail cannot reach on demand: an
// extraction that failed, one that never settled, and a binding whose
// message predates the lease that owns it.
func (w *wedge) bindDirectly(ctx context.Context, m store.Message, at time.Time) store.Message {
	w.t.Helper()
	stored, err := w.store.InsertMessage(ctx, m)
	if err != nil {
		w.t.Fatalf("insert message: %v", err)
	}
	if err := w.store.WithTx(ctx, func(tx *store.Tx) error {
		addrs := make([]string, 0, len(m.Recipients))
		for _, r := range m.Recipients {
			addrs = append(addrs, r.Addr)
		}
		bound, _, err := tx.BindRecipients(ctx, stored.ID, addrs, at, at)
		if err != nil {
			return err
		}
		if len(bound) == 0 {
			w.t.Fatal("the message bound to no lease; the test's premise is wrong")
		}
		return nil
	}); err != nil {
		w.t.Fatalf("bind: %v", err)
	}
	return stored
}

func TestReasonRunNotActive(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	r, grant := w.run(ctx, "pro")
	if err := w.store.EndRun(ctx, r.ID, store.RunFailed, "the test said so"); err != nil {
		t.Fatal(err)
	}
	if got := w.claim(ctx, grant.LeaseID, "k"); got.Reason != store.ReasonRunNotActive {
		t.Errorf("reason = %s, want run_not_active", got.Reason)
	}
}

func TestReasonLeaseNotHeld(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	r, grant := w.run(ctx, "pro")
	if err := w.svc.Release(ctx, r.ID, grant.LeaseID); err != nil {
		t.Fatal(err)
	}
	if got := w.claim(ctx, grant.LeaseID, "k"); got.Reason != store.ReasonLeaseNotHeld {
		t.Errorf("reason = %s, want lease_not_held", got.Reason)
	}
}

// TestReasonLeaseSeedFailed covers the lease that is held and not
// claimable, which is what a crash between acquire and seed leaves
// behind. Pending fails closed exactly as failed does.
func TestReasonLeaseSeedFailed(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	run, _, err := w.svc.CreateRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := w.store.CreateIdentity(ctx, store.NewIdentity{
		ProjectID: w.projectID,
		Addr:      "half-seeded@demo.test",
		Role:      "pro",
		Mode:      store.ModeEphemeral,
	})
	if err != nil {
		t.Fatal(err)
	}
	var lease store.Lease
	if err := w.store.WithTx(ctx, func(tx *store.Tx) error {
		var err error
		lease, err = tx.AcquireLease(ctx, store.NewLease{
			RunID: run.ID, IdentityID: identity.ID, Role: "pro", TTL: time.Hour,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if lease.SeedState != store.SeedStatePending {
		t.Fatalf("seed state = %s, want pending", lease.SeedState)
	}

	w.send(ctx, identity.Addr, "123456")

	got := w.claim(ctx, lease.ID, "k")
	if got.Reason != store.ReasonLeaseSeedFailed {
		t.Errorf("reason = %s, want lease_seed_failed", got.Reason)
	}
	if got.Value != "" {
		t.Errorf("an unseeded lease was handed %q", got.Value)
	}
}

func TestReasonNoBinding(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")
	w.send(ctx, "somebody-else@demo.test", "654321")

	got, err := w.svc.Claim(ctx, personas.ClaimRequest{
		LeaseID: grant.LeaseID, Kind: store.ClaimEmailOTP,
		IdempotencyKey: "k", Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Reason != store.ReasonNoBinding {
		t.Errorf("reason = %s, want claim_no_binding", got.Reason)
	}
}

// TestReasonTimeout is the only outcome that is really about running out
// of time: something bound, and its extraction had not settled yet.
func TestReasonTimeout(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")

	w.bindDirectly(ctx, store.Message{
		ProjectID:  w.projectID,
		Subject:    "still being read",
		TextBody:   "code 777777",
		ReceivedAt: time.Now(),
		Recipients: []store.Recipient{{Addr: grant.Addr, Kind: store.RecipientEnvelope}},
	}, time.Now())

	got, err := w.svc.Claim(ctx, personas.ClaimRequest{
		LeaseID: grant.LeaseID, Kind: store.ClaimEmailOTP,
		IdempotencyKey: "k", Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Reason != store.ReasonTimeout {
		t.Errorf("reason = %s, want claim_timeout", got.Reason)
	}
}

// TestReasonExtractionFailed covers the message that arrived, bound, and
// could not be read. A caller must not wait out its deadline on a message
// that is already settled and empty.
func TestReasonExtractionFailed(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")

	stored := w.bindDirectly(ctx, store.Message{
		ProjectID:  w.projectID,
		Subject:    "unreadable",
		TextBody:   "code 888888",
		ReceivedAt: time.Now(),
		Recipients: []store.Recipient{{Addr: grant.Addr, Kind: store.RecipientEnvelope}},
	}, time.Now())
	if err := w.store.FailExtraction(ctx, stored.ID); err != nil {
		t.Fatal(err)
	}

	got, err := w.svc.Claim(ctx, personas.ClaimRequest{
		LeaseID: grant.LeaseID, Kind: store.ClaimEmailOTP,
		IdempotencyKey: "k", Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Reason != store.ReasonExtractionFail {
		t.Errorf("reason = %s, want extraction_failed", got.Reason)
	}
}

// TestReasonStaleFiltered exercises the server-owned lower bound.
//
// Under the binding rules this state is unreachable: a binding only
// exists inside its lease interval, and a lease is always acquired after
// its run's checkpoint. The row is written through the store's own API
// with a receive time below the bound, which is what a future change to
// binding would produce. The filter is the second lock on the door that
// matters most, and it must not be removed with the first.
func TestReasonStaleFiltered(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")

	lease, err := w.store.Lease(ctx, grant.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	stale := store.Message{
		ProjectID: w.projectID,
		Subject:   "from before this lease existed",
		TextBody:  "code 999999",
		// Received before the lease was acquired, but bound as though it
		// had arrived inside the interval.
		ReceivedAt: lease.AcquiredAt.Add(-time.Hour),
		ExtractedJSON: `{"otp_best":"999999","otp_candidates":["999999"],` +
			`"links":[],"verify_link_best":""}`,
		Recipients: []store.Recipient{{Addr: grant.Addr, Kind: store.RecipientEnvelope}},
	}
	w.bindDirectly(ctx, stale, lease.AcquiredAt.Add(time.Second))

	got, err := w.svc.Claim(ctx, personas.ClaimRequest{
		LeaseID: grant.LeaseID, Kind: store.ClaimEmailOTP,
		IdempotencyKey: "k", Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK() {
		t.Fatalf("a message from before the lease was handed over: %q", got.Value)
	}
	if got.Reason != store.ReasonStaleFiltered {
		t.Errorf("reason = %s, want claim_stale_filtered", got.Reason)
	}
}

// TestNoSecretInLedgerOrExport is the redaction property, run over a
// whole claim: the code that was sent, the code that was handed over and
// the full leased address must appear nowhere in the evidence.
func TestNoSecretInLedgerOrExport(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	run, grant := w.run(ctx, "pro")

	const code = "314159"
	w.send(ctx, grant.Addr, code)
	got := w.claim(ctx, grant.LeaseID, "redaction")
	if !got.OK() {
		t.Fatalf("reason = %s", got.Reason)
	}
	if got.Value != code {
		t.Fatalf("value = %q, want %q", got.Value, code)
	}

	entries, err := w.store.ListLedger(ctx, store.LedgerFilter{ProjectID: w.projectID})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("the run left no evidence at all")
	}
	local := grant.Addr[:strings.Index(grant.Addr, "@")]
	for _, e := range entries {
		if strings.Contains(e.DetailJSON, code) {
			t.Errorf("ledger %d (%s) carries the claimed code: %s", e.ID, e.Action, e.DetailJSON)
		}
		if strings.Contains(e.DetailJSON, local) {
			t.Errorf("ledger %d (%s) carries the full local part: %s", e.ID, e.Action, e.DetailJSON)
		}
	}

	// The claim records themselves are evidence too, and they are where a
	// stored value would be most convenient and most wrong.
	claims, err := w.store.ListRunClaims(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 {
		t.Fatalf("the run recorded %d claims, want 1", len(claims))
	}
	for _, c := range claims {
		if strings.Contains(c.ID+c.IdempotencyKey+c.Kind+c.MessageID, code) {
			t.Errorf("a claim record carries the code: %+v", c)
		}
	}
}

// TestOneMessageBindsToEveryLeasedRecipient is why binding is a table.
func TestOneMessageBindsToEveryLeasedRecipient(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	_, first := w.run(ctx, "pro")
	_, second := w.run(ctx, "admin")

	stored := w.bindDirectly(ctx, store.Message{
		ProjectID: w.projectID,
		Subject:   "one mail, two leased readers",
		TextBody:  "code 246813",
		ExtractedJSON: `{"otp_best":"246813","otp_candidates":["246813"],` +
			`"links":[],"verify_link_best":""}`,
		ReceivedAt: time.Now(),
		Recipients: []store.Recipient{
			{Addr: first.Addr, Kind: store.RecipientEnvelope},
			{Addr: second.Addr, Kind: store.RecipientEnvelope},
		},
	}, time.Now())

	bindings, err := w.store.ListMessageBindings(ctx, stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 {
		t.Fatalf("the message bound %d times, want one per leased recipient", len(bindings))
	}

	// Both runs can claim it, because both were addressed. One-time is
	// per message and kind, so the second claim loses.
	a := w.claim(ctx, first.LeaseID, "a")
	if !a.OK() {
		t.Fatalf("the first leased recipient could not claim: %s", a.Reason)
	}
	b, err := w.svc.Claim(ctx, personas.ClaimRequest{
		LeaseID: second.LeaseID, Kind: store.ClaimEmailOTP,
		IdempotencyKey: "b", Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.OK() {
		t.Error("both runs claimed the same message and kind; one-time is not enforced")
	}
	if b.Reason != store.ReasonAlreadyClaimed {
		t.Errorf("reason = %s, want claim_already_claimed", b.Reason)
	}
}

// TestBindingCommitsWithTheMessage states the ack contract: by the time
// the message is readable, its owner is already decided.
func TestBindingCommitsWithTheMessage(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")
	w.send(ctx, grant.Addr, "135790")

	stored := w.latestMessage(ctx)
	bindings, err := w.store.ListMessageBindings(ctx, stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 {
		t.Fatalf("a stored message has %d bindings, want exactly one owner", len(bindings))
	}
	if bindings[0].LeaseID != grant.LeaseID {
		t.Errorf("bound to %s, want the lease that owned the address", bindings[0].LeaseID)
	}
	if !bindings[0].BoundAt.Before(time.Now()) {
		t.Error("the binding has no time")
	}
}

// TestUnsupportedKindIsLoud pins the stub. A build that cannot serve totp
// says so; it does not return an empty value that reads like "no code
// arrived".
func TestUnsupportedKindIsLoud(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")

	_, err := w.svc.Claim(ctx, personas.ClaimRequest{
		LeaseID: grant.LeaseID, Kind: store.ClaimTOTP, IdempotencyKey: "k",
	})
	if err == nil {
		t.Fatal("totp answered without being implemented")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %v, want it to name the unsupported kind", err)
	}
}
