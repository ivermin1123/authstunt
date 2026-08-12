package personas_test

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/personas"
	"github.com/ivermin1123/authstunt/internal/secrets"
	"github.com/ivermin1123/authstunt/internal/smtp"
	"github.com/ivermin1123/authstunt/internal/sse"
	"github.com/ivermin1123/authstunt/internal/store"
)

// The suite in this file is the phase 3 gate, and it is deliberately
// written against the real composition rather than against the claim
// service alone: mail goes in through the real ingest pipeline, which is
// what writes the binding inside the message transaction. A suite that
// inserted bindings directly would prove the claim filter and nothing
// about the property that matters, which is that a message is never
// stored without an owner already decided.

// wedge is one project with a live bus, a real ingest pipeline and a real
// lease service, which is the smallest arrangement that can produce a
// wrong secret if the design is wrong.
type wedge struct {
	t         *testing.T
	store     *store.Store
	bus       *sse.Bus
	ingest    *smtp.Ingest
	svc       *personas.Service
	projectID string
}

func newWedge(t *testing.T, opts ...func(*personas.Config)) *wedge {
	t.Helper()
	dir := t.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "test")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	st, err := store.Open(t.Context(), dir, key, store.Options{Logger: logger})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	project, err := st.CreateProject(t.Context(), "wedge")
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	allowlist := []string{"demo.test"}
	if err := st.SetAllowlist(t.Context(), project.ID, allowlist); err != nil {
		t.Fatalf("allowlist: %v", err)
	}

	generation, err := st.NextEventGeneration(t.Context())
	if err != nil {
		t.Fatalf("generation: %v", err)
	}
	bus := sse.NewBus(generation)
	busCtx, stopBus := context.WithCancel(context.Background())
	go bus.Run(busCtx)
	t.Cleanup(stopBus)

	ingest, err := smtp.NewIngest(smtp.IngestConfig{
		Store: st, Bus: bus, ProjectID: project.ID, Allowlist: allowlist, Logger: logger,
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	ingest.Start(t.Context())
	t.Cleanup(ingest.Stop)

	cfg := personas.Config{
		Store:     st,
		ProjectID: project.ID,
		Allowlist: allowlist,
		Bus:       bus,
		Logger:    logger,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	svc, err := personas.New(cfg)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	return &wedge{t: t, store: st, bus: bus, ingest: ingest, svc: svc, projectID: project.ID}
}

// run creates a run and acquires one ephemeral lease of a role.
func (w *wedge) run(ctx context.Context, role string) (store.Run, personas.Grant) {
	w.t.Helper()
	r, _, err := w.svc.CreateRun(ctx)
	if err != nil {
		w.t.Fatalf("create run: %v", err)
	}
	grant, err := w.svc.Acquire(ctx, r.ID, role, store.ModeEphemeral)
	if err != nil {
		w.t.Fatalf("acquire: %v", err)
	}
	return r, grant
}

const otpTemplate = "From: app@acme.example\r\n" +
	"To: %s\r\n" +
	"Subject: Your verification code\r\n" +
	"\r\n" +
	"Your code is %s. It expires in ten minutes.\r\n"

// send delivers one OTP mail through the real pipeline and waits for the
// extraction to settle, so a claim that follows is racing nothing.
func (w *wedge) send(ctx context.Context, to, code string) {
	w.t.Helper()
	w.sendAt(ctx, to, code, time.Now())
}

func (w *wedge) sendAt(ctx context.Context, to, code string, at time.Time) {
	w.t.Helper()
	waiter, err := w.bus.SubscribeMatch(ctx, func(ev sse.Event) bool {
		return ev.Message != nil
	})
	if err != nil {
		w.t.Fatalf("subscribe: %v", err)
	}
	defer waiter.Close()

	if err := w.ingest.Deliver(ctx, smtp.Delivery{
		From:       "bounce@acme.example",
		Recipients: []string{to},
		Raw:        []byte(fmt.Sprintf(otpTemplate, to, code)),
		ReceivedAt: at,
	}); err != nil {
		w.t.Fatalf("deliver: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, ok := waiter.Wait(waitCtx); !ok {
		w.t.Fatal("the message never settled")
	}
}

// claim is the short form used everywhere below.
func (w *wedge) claim(ctx context.Context, leaseID, key string) personas.Claimed {
	w.t.Helper()
	got, err := w.svc.Claim(ctx, personas.ClaimRequest{
		LeaseID: leaseID, Kind: store.ClaimEmailOTP, IdempotencyKey: key,
	})
	if err != nil {
		w.t.Fatalf("claim: %v", err)
	}
	return got
}

// TestClaimHandsOverTheCodeFromItsOwnMail is the happy path, stated once
// so the failures below have something to be failures of.
func TestClaimHandsOverTheCodeFromItsOwnMail(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")

	w.send(ctx, grant.Addr, "482913")

	got := w.claim(ctx, grant.LeaseID, "attempt-1")
	if !got.OK() {
		t.Fatalf("reason = %s, want claim_ok", got.Reason)
	}
	if got.Value != "482913" {
		t.Errorf("value = %q, want the code that was sent", got.Value)
	}
}

// TestStaleOtpBeforeRunNeverClaimed covers the failure this product
// exists to prevent: a code sent before the run started is never the
// code the run receives.
func TestStaleOtpBeforeRunNeverClaimed(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()

	// A first run takes the address and receives a code. That code is
	// last night's, from the second run's point of view.
	first, firstGrant := w.run(ctx, "pro")
	w.send(ctx, firstGrant.Addr, "111111")
	if err := w.svc.Release(ctx, first.ID, firstGrant.LeaseID); err != nil {
		t.Fatalf("release: %v", err)
	}

	// The second run acquires a lease of the same role. Under ephemeral
	// mode it gets a different address, which is itself the isolation;
	// the claim must still find nothing rather than reaching back.
	_, second := w.run(ctx, "pro")
	got, err := w.svc.Claim(ctx, personas.ClaimRequest{
		LeaseID: second.LeaseID, Kind: store.ClaimEmailOTP,
		IdempotencyKey: "second-run", Timeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got.OK() {
		t.Fatalf("the second run received %q, which was sent before it started", got.Value)
	}
	if got.Value != "" {
		t.Errorf("a refused claim carried a value: %q", got.Value)
	}
}

// TestWrongRecipientHasNoBinding proves mail to an address no lease owned
// binds to nobody, and that the claim says so rather than reporting a
// bare timeout.
func TestWrongRecipientHasNoBinding(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")

	// Same domain, nobody's lease.
	w.send(ctx, "nobody-leased-this@demo.test", "999999")

	bindings, err := w.store.ListLeaseBindings(ctx, grant.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 0 {
		t.Fatalf("the lease bound %d messages addressed to somebody else", len(bindings))
	}

	got, err := w.svc.Claim(ctx, personas.ClaimRequest{
		LeaseID: grant.LeaseID, Kind: store.ClaimEmailOTP,
		IdempotencyKey: "wrong-recipient", Timeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK() {
		t.Fatalf("a claim was answered from mail nobody leased: %q", got.Value)
	}
}

// TestDuplicateMessageYieldsOneClaim covers the resend: two messages, one
// claim, and the duplicate stays visible and unclaimed.
func TestDuplicateMessageYieldsOneClaim(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")

	w.send(ctx, grant.Addr, "313131")
	w.send(ctx, grant.Addr, "313131")

	first := w.claim(ctx, grant.LeaseID, "first")
	if !first.OK() {
		t.Fatalf("reason = %s, want claim_ok", first.Reason)
	}

	second := w.claim(ctx, grant.LeaseID, "second")
	if !second.OK() {
		t.Fatalf("the duplicate was not claimable under a second key: %s", second.Reason)
	}
	if second.MessageID == first.MessageID {
		t.Error("both claims came from the same message; one-time is not enforced")
	}

	// A third attempt has nothing left, and both messages remain visible.
	third, err := w.svc.Claim(ctx, personas.ClaimRequest{
		LeaseID: grant.LeaseID, Kind: store.ClaimEmailOTP,
		IdempotencyKey: "third", Timeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if third.OK() {
		t.Error("a third claim found a secret where only two messages arrived")
	}
	if third.Reason != store.ReasonAlreadyClaimed {
		t.Errorf("reason = %s, want claim_already_claimed", third.Reason)
	}
	bindings, err := w.store.ListLeaseBindings(ctx, grant.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 {
		t.Errorf("the lease shows %d messages, want both copies visible", len(bindings))
	}
}

// TestClaimRetryIsIdempotent is the Playwright retry: the same key gives
// back the same secret, and never consumes a second message.
func TestClaimRetryIsIdempotent(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")

	w.send(ctx, grant.Addr, "246810")
	w.send(ctx, grant.Addr, "135791")

	first := w.claim(ctx, grant.LeaseID, "attempt-1")
	if !first.OK() {
		t.Fatalf("reason = %s", first.Reason)
	}
	for i := range 5 {
		replay := w.claim(ctx, grant.LeaseID, "attempt-1")
		if !replay.OK() {
			t.Fatalf("replay %d: reason = %s", i, replay.Reason)
		}
		if replay.Value != first.Value || replay.ClaimID != first.ClaimID {
			t.Fatalf("replay %d returned a different claim: %s/%s want %s/%s",
				i, replay.ClaimID, replay.Value, first.ClaimID, first.Value)
		}
	}

	// The second message is untouched: a retry must not eat it.
	other := w.claim(ctx, grant.LeaseID, "attempt-2")
	if !other.OK() || other.MessageID == first.MessageID {
		t.Errorf("the retry consumed the second message: %s %s", other.Reason, other.MessageID)
	}
}

// TestClaimRetryAfterTTLReportsExpired pins the other half of the TTL:
// past it, the key returns claim_expired and no value at all.
func TestClaimRetryAfterTTLReportsExpired(t *testing.T) {
	w := newWedge(t, func(c *personas.Config) { c.ClaimTTL = time.Millisecond })
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")

	w.send(ctx, grant.Addr, "606060")
	first := w.claim(ctx, grant.LeaseID, "expiring")
	if !first.OK() {
		t.Fatalf("reason = %s", first.Reason)
	}

	time.Sleep(5 * time.Millisecond)
	replay := w.claim(ctx, grant.LeaseID, "expiring")
	if replay.Reason != store.ReasonExpired {
		t.Fatalf("reason = %s, want claim_expired", replay.Reason)
	}
	if replay.Value != "" {
		t.Errorf("an expired replay carried a value: %q", replay.Value)
	}
}

// TestClaimAfterLeaseEndsRefused covers both terminal cases: the lease
// released, and the run ended under it.
func TestClaimAfterLeaseEndsRefused(t *testing.T) {
	t.Run("released lease", func(t *testing.T) {
		w := newWedge(t)
		ctx := t.Context()
		r, grant := w.run(ctx, "pro")
		w.send(ctx, grant.Addr, "707070")
		if err := w.svc.Release(ctx, r.ID, grant.LeaseID); err != nil {
			t.Fatal(err)
		}
		got := w.claim(ctx, grant.LeaseID, "after-release")
		if got.Reason != store.ReasonLeaseNotHeld {
			t.Errorf("reason = %s, want lease_not_held", got.Reason)
		}
		if got.Value != "" {
			t.Errorf("a refused claim carried a value: %q", got.Value)
		}
	})

	t.Run("ended run", func(t *testing.T) {
		w := newWedge(t)
		ctx := t.Context()
		r, grant := w.run(ctx, "pro")
		w.send(ctx, grant.Addr, "808080")
		if err := w.store.EndRun(ctx, r.ID, store.RunComplete, ""); err != nil {
			t.Fatal(err)
		}
		got := w.claim(ctx, grant.LeaseID, "after-run")
		if got.Reason != store.ReasonRunNotActive {
			t.Errorf("reason = %s, want run_not_active", got.Reason)
		}
	})
}

// newPooledIdentity registers one pre-provisioned account. It carries a
// persona, because a pooled identity is an account somebody already
// created and the persona is where its credentials live.
func (w *wedge) newPooledIdentity(ctx context.Context, addr, role string) store.Identity {
	w.t.Helper()
	persona, err := w.store.CreatePersona(ctx, store.Persona{
		ProjectID:   w.projectID,
		Name:        "pooled-" + role,
		Email:       addr,
		PasswordEnc: []byte("sealed by the caller"),
		Role:        role,
	})
	if err != nil {
		w.t.Fatalf("create persona: %v", err)
	}
	identity, err := w.store.CreateIdentity(ctx, store.NewIdentity{
		ProjectID: w.projectID,
		PersonaID: persona.ID,
		Addr:      addr,
		Role:      role,
		Mode:      store.ModePooled,
	})
	if err != nil {
		w.t.Fatalf("create pooled identity: %v", err)
	}
	return identity
}

// TestPooledHandoverMarksSuspect is the pooled mode's hard case, and it
// has two halves because the design has two mechanisms.
//
// The cooldown is the real protection: while it runs, the address belongs
// to nobody, so the previous run's late mail binds to nobody. The suspect
// flag is what remains when the cooldown was lost to a race - the pool is
// listed before the lease is taken, so a release landing in between can
// leave a run holding an identity that is still cooling.
func TestPooledHandoverMarksSuspect(t *testing.T) {
	t.Run("cooldown leaves the address unowned", func(t *testing.T) {
		w := newWedge(t)
		ctx := t.Context()
		pooled := w.newPooledIdentity(ctx, "pooled-pro@demo.test", "pro")

		runA, _, err := w.svc.CreateRun(ctx)
		if err != nil {
			t.Fatal(err)
		}
		grantA, err := w.svc.Acquire(ctx, runA.ID, "pro", store.ModePooled)
		if err != nil {
			t.Fatalf("acquire A: %v", err)
		}
		if err := w.svc.Release(ctx, runA.ID, grantA.LeaseID); err != nil {
			t.Fatal(err)
		}

		// A's mail arrives late, inside the cooldown.
		w.send(ctx, pooled.Addr, "555555")

		// Nothing owned the address at that instant, so nothing bound.
		bindings, err := w.store.ListMessageBindings(ctx, w.latestMessage(ctx).ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(bindings) != 0 {
			t.Fatalf("late mail bound to %d leases during the cooldown", len(bindings))
		}

		// And the pool refuses to hand the address to the next run at all
		// while it is cooling.
		runB, _, err := w.svc.CreateRun(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.svc.Acquire(ctx, runB.ID, "pro", store.ModePooled); err == nil {
			t.Fatal("a cooling identity was leased to the next run")
		}
	})

	t.Run("a lease granted inside the cooldown is suspect", func(t *testing.T) {
		w := newWedge(t)
		ctx := t.Context()
		pooled := w.newPooledIdentity(ctx, "pooled-pro@demo.test", "pro")

		runA, _, err := w.svc.CreateRun(ctx)
		if err != nil {
			t.Fatal(err)
		}
		grantA, err := w.svc.Acquire(ctx, runA.ID, "pro", store.ModePooled)
		if err != nil {
			t.Fatalf("acquire A: %v", err)
		}
		if err := w.svc.Release(ctx, runA.ID, grantA.LeaseID); err != nil {
			t.Fatal(err)
		}

		// B acquires through the store directly, which is what losing the
		// cooldown race looks like: the service listed the pool before A
		// released, and takes the lease after.
		runB, _, err := w.svc.CreateRun(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var leaseB store.Lease
		if err := w.store.WithTx(ctx, func(tx *store.Tx) error {
			var err error
			leaseB, err = tx.AcquireLease(ctx, store.NewLease{
				RunID: runB.ID, IdentityID: pooled.ID, Role: "pro", TTL: time.Hour,
			})
			if err != nil {
				return err
			}
			return tx.SettleSeed(ctx, leaseB.ID, store.SeedStateSkipped, "")
		}); err != nil {
			t.Fatalf("acquire B: %v", err)
		}
		if !leaseB.InCooldown {
			t.Fatal("a lease taken inside the cooldown was not marked; every binding to it would read clean")
		}

		// A's mail arrives after B already owns the address.
		w.send(ctx, pooled.Addr, "555555")

		bindings, err := w.store.ListLeaseBindings(ctx, leaseB.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(bindings) != 1 {
			t.Fatalf("B has %d bindings, want the late message bound and visible", len(bindings))
		}
		if bindings[0].Clean() {
			t.Error("the late message bound to B as clean; the handover gap is open")
		}
		if bindings[0].Suspect != store.SuspectCooldown {
			t.Errorf("suspect = %q, want cooldown", bindings[0].Suspect)
		}

		got, err := w.svc.Claim(ctx, personas.ClaimRequest{
			LeaseID: leaseB.ID, Kind: store.ClaimEmailOTP,
			IdempotencyKey: "handover", Timeout: 100 * time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.OK() {
			t.Fatalf("run B received %q, which may belong to run A", got.Value)
		}
		if got.Reason != store.ReasonSuspectBinding {
			t.Errorf("reason = %s, want claim_suspect_binding", got.Reason)
		}
	})
}

// latestMessage returns the newest message in the project, for the tests
// that assert on a message nobody's lease bound.
func (w *wedge) latestMessage(ctx context.Context) store.Message {
	w.t.Helper()
	messages, err := w.store.ListMessages(ctx, store.MessageFilter{
		ProjectID: w.projectID, Limit: 1,
	})
	if err != nil {
		w.t.Fatalf("list messages: %v", err)
	}
	if len(messages) == 0 {
		w.t.Fatal("no message was stored")
	}
	return messages[0]
}

// TestQuarantinedMailIsNeverClaimable covers the message whose envelope
// touched an address this project does not own.
func TestQuarantinedMailIsNeverClaimable(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")

	// One leased recipient, one real customer in copy. The message is
	// quarantined as a whole, and the leased run must not be able to read
	// it through a claim.
	waiter, err := w.bus.SubscribeMatch(ctx, func(ev sse.Event) bool { return ev.Message != nil })
	if err != nil {
		t.Fatal(err)
	}
	defer waiter.Close()
	if err := w.ingest.Deliver(ctx, smtp.Delivery{
		From:       "bounce@acme.example",
		Recipients: []string{grant.Addr, "realcustomer@gmail.com"},
		Raw:        []byte(fmt.Sprintf(otpTemplate, grant.Addr, "424242")),
		ReceivedAt: time.Now(),
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, ok := waiter.Wait(waitCtx); !ok {
		t.Fatal("the message never settled")
	}

	got, err := w.svc.Claim(ctx, personas.ClaimRequest{
		LeaseID: grant.LeaseID, Kind: store.ClaimEmailOTP,
		IdempotencyKey: "quarantined", Timeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK() {
		t.Fatalf("quarantined mail was handed over: %q", got.Value)
	}
}

// TestClaimWakesOnMailThatArrivesWhileWaiting is the lost-wakeup case, and
// the reason the subscription is registered before the first query.
func TestClaimWakesOnMailThatArrivesWhileWaiting(t *testing.T) {
	w := newWedge(t)
	ctx := t.Context()
	_, grant := w.run(ctx, "pro")

	var (
		wg  sync.WaitGroup
		got personas.Claimed
		err error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		got, err = w.svc.Claim(ctx, personas.ClaimRequest{
			LeaseID: grant.LeaseID, Kind: store.ClaimEmailOTP,
			IdempotencyKey: "waiting", Timeout: 5 * time.Second,
		})
	}()

	// The mail arrives after the claim has parked. Nothing here
	// synchronizes with the waiter's registration on purpose: if the
	// subscription happened after the first query, this is the race that
	// would strand the caller until its deadline.
	w.send(ctx, grant.Addr, "191919")
	wg.Wait()

	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !got.OK() {
		t.Fatalf("reason = %s, want claim_ok", got.Reason)
	}
	if got.Value != "191919" {
		t.Errorf("value = %q, want the code that arrived while waiting", got.Value)
	}
	if got.Waited > 4*time.Second {
		t.Errorf("the claim waited %s for mail that arrived immediately", got.Waited)
	}
}

// TestTwoConcurrentRunsNeverCrossClaim is the gating suite. Runs are
// interleaved deliberately: every run acquires before any mail is sent,
// so the addresses are live at the same time and a correlation bug has
// somewhere to go wrong.
func TestTwoConcurrentRunsNeverCrossClaim(t *testing.T) {
	iterations := 1000
	if testing.Short() {
		iterations = 50
	}
	w := newWedge(t)
	ctx := t.Context()

	type pair struct {
		leaseID string
		addr    string
		code    string
	}

	const batch = 20
	for start := 0; start < iterations; start += batch {
		size := min(batch, iterations-start)
		pairs := make([]pair, 0, size)
		for i := range size {
			_, grant := w.run(ctx, "pro")
			pairs = append(pairs, pair{
				leaseID: grant.LeaseID,
				addr:    grant.Addr,
				code:    fmt.Sprintf("%06d", start+i),
			})
		}
		// Every address exists before any mail moves.
		var send sync.WaitGroup
		for _, p := range pairs {
			send.Add(1)
			go func() {
				defer send.Done()
				w.send(ctx, p.addr, p.code)
			}()
		}
		send.Wait()

		var claim sync.WaitGroup
		results := make([]personas.Claimed, len(pairs))
		errs := make([]error, len(pairs))
		for i, p := range pairs {
			claim.Add(1)
			go func() {
				defer claim.Done()
				results[i], errs[i] = w.svc.Claim(ctx, personas.ClaimRequest{
					LeaseID: p.leaseID, Kind: store.ClaimEmailOTP,
					IdempotencyKey: "run-" + p.code, Timeout: 5 * time.Second,
				})
			}()
		}
		claim.Wait()

		for i, p := range pairs {
			if errs[i] != nil {
				t.Fatalf("iteration %s: claim: %v", p.code, errs[i])
			}
			if !results[i].OK() {
				t.Fatalf("iteration %s: reason = %s, want claim_ok", p.code, results[i].Reason)
			}
			// The whole phase is this line.
			if results[i].Value != p.code {
				t.Fatalf("iteration %s received %q: a run was handed another run's secret",
					p.code, results[i].Value)
			}
		}
	}
}

// TestClaimLatencyP95 measures the phase's performance gate: the time
// from the SMTP ack to a typed secret being available, at the 95th
// percentile.
//
// It is a test rather than a benchmark because it is a threshold, not a
// number to watch drift. The work it times is the real path: deliver,
// extract, settle, wake the waiter, select a candidate, record the claim.
func TestClaimLatencyP95(t *testing.T) {
	samples := 50
	if testing.Short() {
		samples = 10
	}
	w := newWedge(t)
	ctx := t.Context()

	latencies := make([]time.Duration, 0, samples)
	for i := range samples {
		_, grant := w.run(ctx, "pro")
		code := fmt.Sprintf("%06d", 500000+i)

		// Deliver returns when the message is durable, which is the
		// instant the session would answer 250.
		if err := w.ingest.Deliver(ctx, smtp.Delivery{
			From:       "bounce@acme.example",
			Recipients: []string{grant.Addr},
			Raw:        []byte(fmt.Sprintf(otpTemplate, grant.Addr, code)),
			ReceivedAt: time.Now(),
		}); err != nil {
			t.Fatalf("deliver: %v", err)
		}
		acked := time.Now()

		got, err := w.svc.Claim(ctx, personas.ClaimRequest{
			LeaseID: grant.LeaseID, Kind: store.ClaimEmailOTP,
			IdempotencyKey: code, Timeout: 5 * time.Second,
		})
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if !got.OK() || got.Value != code {
			t.Fatalf("sample %d: reason %s, value %q", i, got.Reason, got.Value)
		}
		latencies = append(latencies, time.Since(acked))
	}

	slices.Sort(latencies)
	p95 := latencies[(len(latencies)*95)/100]
	t.Logf("claim latency: median %s, p95 %s, max %s",
		latencies[len(latencies)/2], p95, latencies[len(latencies)-1])
	if p95 > time.Second {
		t.Errorf("p95 from ack to typed secret = %s, want under 1s", p95)
	}
}
