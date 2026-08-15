package personas_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
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

// defaultSettleCeiling is how long a delivery is given to be reported
// settled on the bus. Every test uses it unless it says otherwise.
const defaultSettleCeiling = 5 * time.Second

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
	// dir is the store's data directory. It is kept so a failing test can
	// snapshot the database before t.TempDir removes it; the directory
	// holds the key file too, so a snapshot can be reopened with the real
	// store API rather than only read as raw SQLite.
	dir string
	// settleCeiling bounds how long deliver waits for the bus to report
	// the message settled. It is a field rather than a constant so a
	// single test can widen it without moving the ceiling for every other
	// test in the package; every wedge starts at the original value.
	settleCeiling time.Duration
	// settle, when non-nil, collects how long each successful settle
	// actually took. A ceiling only says the wait did not run out; the
	// distribution says how much room was left.
	settle *settleLog
}

// settleLog accumulates successful settle durations. deliver is called
// from concurrent goroutines, so the append is guarded; the lock is held
// for a slice append against waits measured in milliseconds, which is why
// a plain mutex is used rather than anything cleverer.
type settleLog struct {
	mu sync.Mutex
	d  []time.Duration
}

func (s *settleLog) add(d time.Duration) {
	s.mu.Lock()
	s.d = append(s.d, d)
	s.mu.Unlock()
}

// report logs the shape of the distribution against the ceiling in force.
// The question it answers is how close the passing waits came to failing.
func (s *settleLog) report(t *testing.T, ceiling time.Duration) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.d) == 0 {
		t.Logf("SETTLE no successful settle was recorded")
		return
	}
	d := slices.Clone(s.d)
	slices.Sort(d)
	at := func(q float64) time.Duration {
		i := int(q * float64(len(d)-1))
		return d[i]
	}
	maxd := d[len(d)-1]
	// Every duration goes through asciiDuration for the reason spelled
	// out there: this line is read off a Windows console, and p50 is
	// routinely sub-millisecond, which is exactly where Duration.String()
	// reaches for a micro sign.
	t.Logf("SETTLE n=%d ceiling=%s p50=%s p95=%s p99=%s max=%s headroom_at_max=%s",
		len(d), asciiDuration(ceiling), asciiDuration(at(0.50)), asciiDuration(at(0.95)),
		asciiDuration(at(0.99)), asciiDuration(maxd), asciiDuration(ceiling-maxd))
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
	return &wedge{
		t: t, store: st, bus: bus, ingest: ingest, svc: svc, projectID: project.ID, dir: dir,
		settleCeiling: defaultSettleCeiling,
	}
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

// otpTemplate is mail as an application sends it, Date header included.
// The header is not decoration: on a pooled address it is the only thing
// that says whether the message is older than the lease it is about to
// bind to.
const otpTemplate = "From: app@acme.example\r\n" +
	"To: %s\r\n" +
	"Date: %s\r\n" +
	"Subject: Your verification code\r\n" +
	"\r\n" +
	"Your code is %s. It expires in ten minutes.\r\n"

// undatedTemplate is the same mail from an application that omits the
// Date header. RFC 5322 requires one; real senders still skip it, and a
// pooled binding has to decide what to do about that.
const undatedTemplate = "From: app@acme.example\r\n" +
	"To: %s\r\n" +
	"Subject: Your verification code\r\n" +
	"\r\n" +
	"Your code is %s. It expires in ten minutes.\r\n"

// welcomeTemplate is mail to a leased address that carries no code of any
// kind. It exists to be noise: it binds to the lease, it publishes an
// event, and it is not what a claim is waiting for. There is no digit run
// anywhere in it, so the extractor finds no candidate at all rather than
// finding a weak one.
const welcomeTemplate = "From: app@acme.example\r\n" +
	"To: %s\r\n" +
	"Date: %s\r\n" +
	"Subject: Welcome to Acme\r\n" +
	"\r\n" +
	"Thanks for signing up. Nothing in this message needs to be typed in.\r\n"

// sendNoise delivers mail the claim must ignore and keep waiting through.
func (w *wedge) sendNoise(ctx context.Context, to string) {
	w.t.Helper()
	at := time.Now()
	w.deliver(ctx, to, at, []byte(fmt.Sprintf(welcomeTemplate, to, at.Format(time.RFC1123Z))))
}

// send delivers one OTP mail through the real pipeline and waits for the
// extraction to settle, so a claim that follows is racing nothing.
func (w *wedge) send(ctx context.Context, to, code string) {
	w.t.Helper()
	w.sendAt(ctx, to, code, time.Now())
}

// sendAt delivers mail that was generated at the instant it arrived, which
// is the ordinary case.
func (w *wedge) sendAt(ctx context.Context, to, code string, at time.Time) {
	w.t.Helper()
	w.deliver(ctx, to, at, []byte(fmt.Sprintf(otpTemplate, to, at.Format(time.RFC1123Z), code)))
}

// sendGeneratedAt delivers mail that says it was generated at one instant
// and arrives at another. That gap is the late delivery the pooled
// handover guard exists for.
func (w *wedge) sendGeneratedAt(ctx context.Context, to, code string, generated, received time.Time) {
	w.t.Helper()
	w.deliver(ctx, to, received,
		[]byte(fmt.Sprintf(otpTemplate, to, generated.Format(time.RFC1123Z), code)))
}

// sendUndated delivers mail that cannot say when it was generated.
func (w *wedge) sendUndated(ctx context.Context, to, code string) {
	w.t.Helper()
	w.deliver(ctx, to, time.Now(), []byte(fmt.Sprintf(undatedTemplate, to, code)))
}

// deliver fails with Errorf and returns, never Fatal.
//
// The gating test sends its batch from one goroutine per message, and
// t.Fatal outside the goroutine that runs the test does not stop the
// test: it calls runtime.Goexit on the caller only, so the test carries
// on with one sender silently gone. That is not theory - a single run
// accumulated 206 never-settled reports, every one of them a Fatal that
// stopped nothing. Errorf marks the test failed, the return ends this
// delivery, and the batch its caller is waiting on still completes.
func (w *wedge) deliver(ctx context.Context, to string, at time.Time, raw []byte) {
	w.t.Helper()
	waiter, err := w.bus.SubscribeMatch(ctx, func(ev sse.Event) bool {
		return ev.Message != nil
	})
	if err != nil {
		w.t.Errorf("subscribe for %s: %v", to, err)
		return
	}
	defer waiter.Close()

	if err := w.ingest.Deliver(ctx, smtp.Delivery{
		From:       "bounce@acme.example",
		Recipients: []string{to},
		Raw:        raw,
		ReceivedAt: at,
	}); err != nil {
		w.t.Errorf("deliver to %s: %v", to, err)
		return
	}
	waitCtx, cancel := context.WithTimeout(ctx, w.settleCeiling)
	defer cancel()
	started := time.Now()
	if _, ok := waiter.Wait(waitCtx); !ok {
		// The wait has already run out, so nothing here can lengthen or
		// shorten what it is describing.
		w.dumpSettleFailure(ctx, to, at, time.Since(started))
		w.t.Errorf("the message to %s never settled", to)
		return
	}
	if w.settle != nil {
		w.settle.add(time.Since(started))
	}
}

// dumpSettleFailure records what the store holds for an address whose mail
// the bus never reported. The distinction it exists to draw: a message row
// that is present says the write landed and only the notification was lost
// or late, while no row at all says the delivery itself did not complete.
//
// The wall clock is printed because these timeouts have been seen to fire
// in groups. A waiter here matches any message at all, so one settled
// message releases every waiter parked at that moment; several deadlines
// expiring within milliseconds of each other therefore means the whole
// pipeline published nothing for the length of the ceiling, which is a
// different fault from any one message going missing.
//
// GOMAXPROCS is printed because it sizes both the extraction worker pool
// and the queue in front of it, so it is what decides how deep a burst of
// concurrent deliveries has to sit before it is served.
// The dump is assembled in full and emitted through a SINGLE Logf.
// testing serializes each Logf call, not each dump, so a dump written a
// line at a time interleaves with any other dump running at the same
// moment - measured at 60-64% of lines when four deliveries time out
// together, which is exactly the shape the CI red of 2026-08-14 had.
// A dump that has to be unscrambled before it can be read is not
// evidence. One call, one block, no interleaving.
//
// Every line that answers a question carries the address it answers it
// for, because the count that matters most is zero, and a bare
// "message_count_for_addr=0" in a log with four dumps in it names
// nothing.
func (w *wedge) dumpSettleFailure(ctx context.Context, addr string, at time.Time, waited time.Duration) {
	w.t.Helper()
	var b strings.Builder
	f := func(format string, a ...any) {
		b.WriteString("SETTLE-DUMP ")
		fmt.Fprintf(&b, format, a...)
		b.WriteByte('\n')
	}

	f("addr=%s received_at_arg=%s waited=%s ceiling=%s",
		addr, stored(at), asciiDuration(waited), asciiDuration(w.settleCeiling))
	f("wall_now=%s gomaxprocs=%d numcpu=%d", stored(time.Now()), runtime.GOMAXPROCS(0), runtime.NumCPU())
	f("storage: acquired_at/released_at/received_at are TEXT; layout=%q (9 fractional digits)", storedLayout)

	msgs, err := w.store.ListMessages(ctx, store.MessageFilter{
		ProjectID: w.projectID, To: addr, Limit: 4,
	})
	if err != nil {
		f("messages for addr=%s: READ FAILED: %v", addr, err)
		w.t.Log(b.String())
		return
	}
	// The answer to "was it written at all" is this count, read after the
	// wait gave up. Zero and non-zero point at different subsystems.
	f("message_count_for_addr=%d addr=%s", len(msgs), addr)
	for _, m := range msgs {
		f("message id=%s received_at=%s", m.ID, stored(m.ReceivedAt))

		mb, err := w.store.ListMessageBindings(ctx, m.ID)
		if err != nil {
			f("message %s bindings: READ FAILED: %v", m.ID, err)
			continue
		}
		f("message %s bound_to_count=%d", m.ID, len(mb))
		for _, b := range mb {
			f("message %s bound_to lease=%s run=%s suspect=%q bound_at=%s",
				m.ID, b.LeaseID, b.RunID, b.Suspect, stored(b.BoundAt))
		}
	}

	owner, err := w.store.LeaseAt(ctx, addr, at)
	switch {
	case errors.Is(err, store.ErrNotFound):
		f("LeaseAt(addr=%s, received_at_arg) -> NO LEASE MATCHED", addr)
	case err != nil:
		f("LeaseAt(addr=%s, received_at_arg) -> READ FAILED: %v", addr, err)
	default:
		f("LeaseAt(addr=%s, received_at_arg) -> lease=%s run=%s acquired_at=%s released_at=%s",
			addr, owner.ID, owner.RunID, stored(owner.AcquiredAt), stored(owner.ReleasedAt))
	}

	w.t.Log(b.String())
}

// asciiDuration renders a duration without Go's sub-millisecond units.
//
// Duration.String() prints "292ns" and "1.5µs", and the µ is U+00B5. The
// console this dump is read from is a Windows runner, which encodes
// output as code page 437, where that byte sequence arrives as mojibake -
// observed as "┬║". A number that has to be decoded before it can be
// compared is a number nobody compares.
//
// CI writes the stream to a file as well, and a file could carry UTF-8
// safely, but ASCII closes the question everywhere at once and costs
// nothing: no reader of this output has to know which path it took.
func asciiDuration(d time.Duration) string {
	if d >= time.Second {
		return strconv.FormatFloat(d.Seconds(), 'f', 3, 64) + "s"
	}
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 3, 64) + "ms"
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
		w := newWedge(t, withPooledPolicy(time.Second))
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
		w := newWedge(t, withPooledPolicy(time.Second))
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
		Raw:        []byte(fmt.Sprintf(otpTemplate, grant.Addr, time.Now().Format(time.RFC1123Z), "424242")),
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

// TestClaimKeepsWaitingThroughNoiseMail is the regression test for the
// one-shot subscription: a claim that is woken by mail it cannot use must
// still be woken by the mail it can.
//
// The shape under test is Mesa condition-variable discipline. A wakeup is
// a hint, so the claim re-checks in a loop; that half was always right.
// What was wrong is that the subscription did not survive the first hint,
// so the second message arrived to an empty registry and the caller slept
// out its whole budget for mail that was already stored.
//
// Why this runs in rounds rather than once: nothing can synchronize the
// noise against the claim's registration from outside the service, and a
// round whose noise lands before the claim subscribes is a round that
// would pass either way. A round is only ever a false pass, never a false
// failure, so repeating it is what makes the test discriminate - with the
// bug present essentially every round fails, and with it fixed no round
// can fail whatever the order turns out to be.
func TestClaimKeepsWaitingThroughNoiseMail(t *testing.T) {
	// Short enough that a regression costs rounds x budget rather than
	// minutes, and still two orders of magnitude above the measured
	// settle (p99 ~20ms, max 35ms).
	const (
		rounds = 8
		budget = 2 * time.Second
		// A claim that answered from the bus never comes close to this.
		// A claim that slept out its budget is far past it.
		prompt = 500 * time.Millisecond
	)

	w := newWedge(t)
	ctx := t.Context()

	for i := range rounds {
		_, grant := w.run(ctx, "pro")
		code := fmt.Sprintf("77%04d", i)

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
				IdempotencyKey: fmt.Sprintf("noise-then-code-%d", i),
				Timeout:        budget,
			})
		}()

		// The welcome mail is addressed to the same lease, so it matches
		// the claim's predicate and wakes it. It carries nothing of the
		// claimed kind, so the claim has to go back to waiting.
		w.sendNoise(ctx, grant.Addr)
		w.send(ctx, grant.Addr, code)
		wg.Wait()

		if err != nil {
			t.Fatalf("round %d: claim: %v", i, err)
		}
		if !got.OK() {
			t.Fatalf("round %d: reason = %s, want claim_ok: the code arrived after noise woke the claim",
				i, got.Reason)
		}
		if got.Value != code {
			t.Errorf("round %d: value = %q, want %q", i, got.Value, code)
		}
		if got.Waited > prompt {
			t.Errorf("round %d: the claim waited %s for a code that was already delivered, budget %s",
				i, got.Waited, budget)
		}
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
	// This is the test that has been seen to run out of settle budget on
	// the Windows runner. Recording the settles it already performs costs
	// it nothing and turns every ordinary run into a measurement of how
	// much of the ceiling is actually being used.
	w.settle = &settleLog{}
	t.Cleanup(func() { w.settle.report(t, w.settleCeiling) })
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
			Raw:        []byte(fmt.Sprintf(otpTemplate, grant.Addr, time.Now().Format(time.RFC1123Z), code)),
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
