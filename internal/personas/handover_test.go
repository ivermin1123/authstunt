package personas_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/ledger"
	"github.com/ivermin1123/authstunt/internal/personas"
	"github.com/ivermin1123/authstunt/internal/store"
)

// The suite in this file is the corrective slice for the pooled handover
// gap phase 3 found and could not close: while the cooldown was the only
// protection, an application whose mail took longer to arrive than the
// cooldown lasted would deliver one run's code into the next run's lease
// as a clean binding. Every test here runs through the real ingest
// pipeline and the real claim service, because the property at stake is
// what the composition does, not what the filter would do if a binding
// were handed to it.

// elapseCooldown moves a pooled identity's cooldown into the past.
//
// The alternative is sleeping out a real cooldown, and the shortest legal
// one is a second: a suite that paid that for every handover would buy
// nothing, because what these tests are about is what happens once the
// address is handed over, not how the clock got there.
func (w *wedge) elapseCooldown(ctx context.Context, identityID string) {
	w.t.Helper()
	if err := w.store.WithTx(ctx, func(tx *store.Tx) error {
		return tx.SetCooldown(ctx, identityID, time.Now().Add(-time.Second))
	}); err != nil {
		w.t.Fatalf("elapse cooldown: %v", err)
	}
}

// acquirePooled takes the pooled identity of a role for a fresh run.
func (w *wedge) acquirePooled(ctx context.Context, role string) (store.Run, personas.Grant) {
	w.t.Helper()
	r, _, err := w.svc.CreateRun(ctx)
	if err != nil {
		w.t.Fatalf("create run: %v", err)
	}
	grant, err := w.svc.Acquire(ctx, r.ID, role, store.ModePooled)
	if err != nil {
		w.t.Fatalf("acquire pooled: %v", err)
	}
	return r, grant
}

// handOver runs one full pooled handover: the holder gives the address
// back and the cooldown elapses, which is the state the next run acquires
// from.
func (w *wedge) handOver(ctx context.Context, runID string, grant personas.Grant) {
	w.t.Helper()
	if err := w.svc.Release(ctx, runID, grant.LeaseID); err != nil {
		w.t.Fatalf("release: %v", err)
	}
	w.elapseCooldown(ctx, grant.IdentityID)
}

// TestPooledAcquireWithoutPolicyIsRefused is the first of the three
// enforcement points: a pooled address is reused across runs, and without
// a declared delivery bound there is no configuration under which the
// handover is known to be safe.
func TestPooledAcquireWithoutPolicyIsRefused(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	w.newPooledIdentity(ctx, "pooled-pro@demo.test", "pro")

	run, _, err := w.svc.CreateRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.svc.Acquire(ctx, run.ID, "pro", store.ModePooled)
	if !errors.Is(err, personas.ErrPooledPolicyMissing) {
		t.Fatalf("acquire without a pooled policy = %v, want ErrPooledPolicyMissing", err)
	}

	// The refusal is evidence, not just an error return: a suite that
	// suddenly cannot acquire has to be able to find out why from the
	// ledger.
	if !w.ledgerHas(ctx, ledger.ActionLeaseRefused, store.ReasonPooledPolicyMissing) {
		t.Error("the refusal left no ledger entry carrying its reason code")
	}

	// Ephemeral mode needs no policy and is untouched by the gate.
	if _, err := w.svc.Acquire(ctx, run.ID, "pro", store.ModeEphemeral); err != nil {
		t.Errorf("ephemeral acquire was refused for want of a pooled policy: %v", err)
	}
}

// TestPooledCooldownBelowThePolicyFloorIsRefused covers the second
// enforcement point. A cooldown shorter than the declared delivery bound
// is the exact configuration that produced the gap, and it is refused at
// construction rather than raised to the floor: an operator who thinks
// they have a five second handover must learn otherwise from a startup
// error, not from a test that received somebody else's code.
func TestPooledCooldownBelowThePolicyFloorIsRefused(t *testing.T) {
	s, project := openStore(t)
	base := func() personas.Config {
		return personas.Config{
			Store:     s,
			ProjectID: project.ID,
			Allowlist: []string{"demo.test"},
		}
	}

	t.Run("below the floor", func(t *testing.T) {
		cfg := base()
		cfg.Pooled = &personas.PooledPolicy{MaxDeliveryLatency: 90 * time.Second}
		cfg.PoolCooldown = 5 * time.Second
		svc, err := personas.New(cfg)
		if err == nil {
			t.Fatal("a cooldown below the declared delivery bound was accepted")
		}
		if svc != nil {
			t.Error("a refused configuration still produced a service")
		}
		if !strings.Contains(err.Error(), store.ReasonPooledCooldownBelowFloor) {
			t.Errorf("error %q carries no reason code", err)
		}
	})

	t.Run("a policy tighter than a message can state", func(t *testing.T) {
		cfg := base()
		cfg.Pooled = &personas.PooledPolicy{MaxDeliveryLatency: time.Millisecond}
		if _, err := personas.New(cfg); err == nil {
			t.Fatal("a delivery bound below the resolution of a Date header was accepted")
		}
	})

	t.Run("an unset cooldown is raised to the floor", func(t *testing.T) {
		// Observed where it matters rather than by reading a field back:
		// the cooldown a release actually writes onto the identity is what
		// holds the address out of the pool.
		w := newWedge(t, withPooledPolicy(5*time.Minute))
		ctx := t.Context()
		w.newPooledIdentity(ctx, "floor-pro@demo.test", "pro")

		run, grant := w.acquirePooled(ctx, "pro")
		released := time.Now()
		if err := w.svc.Release(ctx, run.ID, grant.LeaseID); err != nil {
			t.Fatalf("release: %v", err)
		}
		identity, err := w.store.Identity(ctx, grant.IdentityID)
		if err != nil {
			t.Fatal(err)
		}
		if identity.CooldownUntil.Before(released.Add(5 * time.Minute)) {
			t.Errorf("cooldown ends at %s, less than the declared bound after release at %s",
				identity.CooldownUntil, released)
		}
	})

	t.Run("a claim may not outlive its lease", func(t *testing.T) {
		cfg := base()
		cfg.LeaseTTL = time.Minute
		cfg.ClaimTTL = time.Minute
		if _, err := personas.New(cfg); err == nil {
			t.Fatal("a claim TTL equal to the lease TTL was accepted")
		}
	})
}

// TestLateMailAfterHandoverIsNeverClaimableByTheNextRun is the corrective
// slice's reason to exist.
//
// The sequence is the one the phase 3 report described and could not
// refuse: run A acts, run A releases, the cooldown ends, run B acquires
// the same address, and only then does A's mail arrive. Under the cooldown
// alone that binding was clean and B could claim A's code. The message's
// own origination time is what refuses it now, and it refuses it for any
// delivery latency rather than for latencies the operator guessed
// correctly.
func TestLateMailAfterHandoverIsNeverClaimableByTheNextRun(t *testing.T) {
	w := newWedge(t, withPooledPolicy(time.Second))
	ctx := t.Context()
	pooled := w.newPooledIdentity(ctx, "pooled-pro@demo.test", "pro")

	runA, grantA := w.acquirePooled(ctx, "pro")
	// A acts here: the application generates A's code while A holds the
	// address. Nothing has been delivered yet.
	generated := time.Now().Add(-5 * time.Second)
	w.handOver(ctx, runA.ID, grantA)

	_, grantB := w.acquirePooled(ctx, "pro")
	if grantB.IdentityID != pooled.ID {
		t.Fatal("the pool of one handed out a second identity")
	}

	// A's mail finally arrives, long after B took the address.
	w.sendGeneratedAt(ctx, pooled.Addr, "555555", generated, time.Now())

	bindings, err := w.store.ListLeaseBindings(ctx, grantB.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 {
		t.Fatalf("B has %d bindings, want the late message bound and visible", len(bindings))
	}
	if bindings[0].Clean() {
		t.Fatal("A's late mail bound to B as clean: the handover gap is open")
	}
	if bindings[0].Suspect != store.SuspectPredatesLease {
		t.Errorf("suspect = %q, want predates_lease", bindings[0].Suspect)
	}

	got, err := w.svc.Claim(ctx, personas.ClaimRequest{
		LeaseID: grantB.LeaseID, Kind: store.ClaimEmailOTP,
		IdempotencyKey: "handover", Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK() {
		t.Fatalf("run B was handed %q, a code generated for run A", got.Value)
	}
	if got.Value != "" {
		t.Errorf("a refused claim carried a value: %q", got.Value)
	}
	if got.Reason != store.ReasonSuspectBinding {
		t.Errorf("reason = %s, want claim_suspect_binding", got.Reason)
	}
}

// TestPooledMailWithoutAnOriginDateIsRefused fixes the direction the guard
// fails in. A message that cannot say when it was generated cannot be
// shown to belong to the run reading it, and on a reused address that is
// enough to refuse it.
func TestPooledMailWithoutAnOriginDateIsRefused(t *testing.T) {
	w := newWedge(t, withPooledPolicy(time.Second))
	ctx := t.Context()
	pooled := w.newPooledIdentity(ctx, "pooled-pro@demo.test", "pro")

	_, grant := w.acquirePooled(ctx, "pro")
	w.sendUndated(ctx, pooled.Addr, "313131")

	bindings, err := w.store.ListLeaseBindings(ctx, grant.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 {
		t.Fatalf("the lease has %d bindings, want the message visible", len(bindings))
	}
	if bindings[0].Suspect != store.SuspectOriginUnknown {
		t.Errorf("suspect = %q, want origin_unknown", bindings[0].Suspect)
	}

	got, err := w.svc.Claim(ctx, personas.ClaimRequest{
		LeaseID: grant.LeaseID, Kind: store.ClaimEmailOTP,
		IdempotencyKey: "undated", Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK() {
		t.Fatalf("a message that cannot state its own age was handed over: %q", got.Value)
	}
}

// TestPooledMailGeneratedAfterAcquireIsClaimable is the other direction,
// and it is the test that keeps the guard from being a way to break pooled
// mode entirely. A run's own mail still reaches it.
func TestPooledMailGeneratedAfterAcquireIsClaimable(t *testing.T) {
	w := newWedge(t, withPooledPolicy(time.Second))
	ctx := t.Context()
	pooled := w.newPooledIdentity(ctx, "pooled-pro@demo.test", "pro")

	// A held the address first, so the lease under test is a handover
	// rather than a first use: this is the same shape as the test above,
	// with only the message's age changed.
	runA, grantA := w.acquirePooled(ctx, "pro")
	w.handOver(ctx, runA.ID, grantA)

	_, grantB := w.acquirePooled(ctx, "pro")
	w.send(ctx, pooled.Addr, "778899")

	bindings, err := w.store.ListLeaseBindings(ctx, grantB.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || !bindings[0].Clean() {
		t.Fatalf("B's own mail did not bind clean: %+v", bindings)
	}
	got := w.claim(ctx, grantB.LeaseID, "own-mail")
	if !got.OK() {
		t.Fatalf("reason = %s, want claim_ok", got.Reason)
	}
	if got.Value != "778899" {
		t.Errorf("value = %q, want the code sent to this run", got.Value)
	}
}

// TestEphemeralIgnoresTheOriginGuard is the regression this slice must not
// cause. An ephemeral address is minted for one run and never reused, so
// no earlier run's mail can reach it and there is nothing for the guard to
// decide. An application that omits a Date header keeps working exactly as
// it did before.
func TestEphemeralIgnoresTheOriginGuard(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")

	w.sendUndated(ctx, grant.Addr, "246810")

	bindings, err := w.store.ListLeaseBindings(ctx, grant.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 {
		t.Fatalf("the lease has %d bindings, want one", len(bindings))
	}
	if !bindings[0].Clean() {
		t.Fatalf("an ephemeral binding was marked %q; the guard reached a mode it does not apply to",
			bindings[0].Suspect)
	}
	got := w.claim(ctx, grant.LeaseID, "undated-ephemeral")
	if !got.OK() || got.Value != "246810" {
		t.Fatalf("reason = %s, value = %q, want the code that was sent", got.Reason, got.Value)
	}
}

// TestConcurrentHandoverNeverCrossClaims is the gate for this slice: a
// thousand handovers with the previous run's mail arriving while the next
// run is already claiming.
//
// Delivery and claim run against each other on purpose. Whichever wins,
// two things have to hold afterwards: the claim never carried the previous
// run's code, and the message is on record as bound and refused rather
// than silently missing.
func TestConcurrentHandoverNeverCrossClaims(t *testing.T) {
	iterations := 1000
	if testing.Short() {
		iterations = 50
	}
	w := newWedge(t, withPooledPolicy(time.Second))
	ctx := t.Context()
	pooled := w.newPooledIdentity(ctx, "pooled-pro@demo.test", "pro")

	for i := range iterations {
		code := fmt.Sprintf("%06d", 100000+i)

		runA, grantA := w.acquirePooled(ctx, "pro")
		// A's code is generated while A holds the address, and the gap is
		// wider than the resolution the comparison can use.
		generated := time.Now().Add(-5 * time.Second)
		w.handOver(ctx, runA.ID, grantA)

		runB, grantB := w.acquirePooled(ctx, "pro")

		var (
			wg       sync.WaitGroup
			got      personas.Claimed
			claimErr error
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			w.sendGeneratedAt(ctx, pooled.Addr, code, generated, time.Now())
		}()
		go func() {
			defer wg.Done()
			got, claimErr = w.svc.Claim(ctx, personas.ClaimRequest{
				LeaseID: grantB.LeaseID, Kind: store.ClaimEmailOTP,
				IdempotencyKey: "handover-" + code, Timeout: 10 * time.Millisecond,
			})
		}()
		wg.Wait()

		if claimErr != nil {
			t.Fatalf("iteration %d: claim: %v", i, claimErr)
		}
		if got.OK() {
			t.Fatalf("iteration %d: run B was handed %q across a handover", i, got.Value)
		}
		if got.Value != "" {
			t.Fatalf("iteration %d: a refused claim carried %q", i, got.Value)
		}

		bindings, err := w.store.ListLeaseBindings(ctx, grantB.LeaseID)
		if err != nil {
			t.Fatal(err)
		}
		if len(bindings) != 1 {
			t.Fatalf("iteration %d: B has %d bindings, want the late message visible", i, len(bindings))
		}
		if bindings[0].Suspect != store.SuspectPredatesLease {
			t.Fatalf("iteration %d: suspect = %q, want predates_lease", i, bindings[0].Suspect)
		}
		w.handOver(ctx, runB.ID, grantB)
	}
}

// TestClaimCannotOutliveItsLease covers the second lock on the replay
// window. The gates already refuse a replay once the lease has ended;
// this is the one that does not depend on gate order surviving a later
// change.
func TestClaimCannotOutliveItsLease(t *testing.T) {
	w := newWedge(t, func(c *personas.Config) {
		// The run is the shorter of the two grants here, which is what the
		// replay window has to be clamped to. Configuring the lease that
		// way instead is impossible: a claim TTL at or above the lease TTL
		// is refused at construction.
		c.RunTTL = 5 * time.Second
		c.ClaimTTL = 2 * time.Minute
	})
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")
	w.send(ctx, grant.Addr, "135791")

	got := w.claim(ctx, grant.LeaseID, "clamped")
	if !got.OK() {
		t.Fatalf("reason = %s, want claim_ok", got.Reason)
	}
	claim, err := w.store.ClaimByKey(ctx, grant.LeaseID, "clamped")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := w.store.Lease(ctx, grant.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := w.store.Run(ctx, lease.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if claim.ExpiresAt.After(lease.ExpiresAt) {
		t.Errorf("the claim expires at %s, after its lease at %s",
			claim.ExpiresAt, lease.ExpiresAt)
	}
	if claim.ExpiresAt.After(run.ExpiresAt) {
		t.Errorf("the claim expires at %s, after its run at %s",
			claim.ExpiresAt, run.ExpiresAt)
	}
}

// TestClaimIsRefusedWhenTheGrantHasNoTimeLeft is the end of the same rule.
// A grant that has already run out cannot be shortened into a usable
// window, so no secret is handed over at all.
//
// The run here is past its expiry while the sweeper has not reached it, so
// the state gates still read active: this is exactly the gap the cap
// exists to cover.
func TestClaimIsRefusedWhenTheGrantHasNoTimeLeft(t *testing.T) {
	w := newWedge(t, func(c *personas.Config) {
		c.RunTTL = time.Nanosecond
	})
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")
	w.send(ctx, grant.Addr, "864209")

	got := w.claim(ctx, grant.LeaseID, "expired-grant")
	if got.OK() {
		t.Fatalf("a secret was handed over against a run that had already run out: %q", got.Value)
	}
	if got.Reason != store.ReasonLeaseNotHeld {
		t.Errorf("reason = %s, want lease_not_held", got.Reason)
	}
	if _, err := w.store.ClaimByKey(ctx, grant.LeaseID, "expired-grant"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a claim row was recorded for a closed grant: %v", err)
	}
}

// ledgerHas reports whether the trail carries an action whose detail
// mentions a reason code.
func (w *wedge) ledgerHas(ctx context.Context, action, reason string) bool {
	w.t.Helper()
	entries, err := w.store.ListLedger(ctx, store.LedgerFilter{ProjectID: w.projectID})
	if err != nil {
		w.t.Fatalf("ledger: %v", err)
	}
	for _, e := range entries {
		if e.Action != action {
			continue
		}
		var detail struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(e.DetailJSON), &detail); err != nil {
			w.t.Fatalf("ledger detail: %v", err)
		}
		if detail.Reason == reason {
			return true
		}
	}
	return false
}
