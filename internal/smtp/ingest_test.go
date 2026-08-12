package smtp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/extract"
	"github.com/ivermin1123/authstunt/internal/ledger"
	"github.com/ivermin1123/authstunt/internal/secrets"
	"github.com/ivermin1123/authstunt/internal/smtp"
	"github.com/ivermin1123/authstunt/internal/sse"
	"github.com/ivermin1123/authstunt/internal/store"
)

// harness is a running pipeline over a temporary data directory: the same
// wiring serve builds, minus the listener.
type harness struct {
	t         *testing.T
	store     *store.Store
	bus       *sse.Bus
	ingest    *smtp.Ingest
	projectID string
	dataDir   string
}

func newHarness(t *testing.T, allowlist []string, opts ...func(*smtp.IngestConfig)) *harness {
	t.Helper()
	return newHarnessAt(t, t.TempDir(), allowlist, opts...)
}

// newHarnessAt is newHarness over a caller-owned directory, which is how
// the recovery tests reopen the same data a crashed run left behind.
func newHarnessAt(t *testing.T, dataDir string, allowlist []string, opts ...func(*smtp.IngestConfig)) *harness {
	t.Helper()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dataDir, "keys"), "test")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	st, err := store.Open(t.Context(), dataDir, key, store.Options{Logger: logger})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	project, err := st.ProjectByName(t.Context(), "test")
	if err != nil {
		project, err = st.CreateProject(t.Context(), "test")
		if err != nil {
			t.Fatalf("create project: %v", err)
		}
		if err := st.SetAllowlist(t.Context(), project.ID, allowlist); err != nil {
			t.Fatalf("set allowlist: %v", err)
		}
	}
	stored, err := st.Allowlist(t.Context(), project.ID)
	if err != nil {
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

	cfg := smtp.IngestConfig{
		Store:     st,
		Bus:       bus,
		ProjectID: project.ID,
		Allowlist: stored,
		Logger:    logger,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	ingest, err := smtp.NewIngest(cfg)
	if err != nil {
		t.Fatalf("new ingest: %v", err)
	}
	return &harness{t: t, store: st, bus: bus, ingest: ingest, projectID: project.ID, dataDir: dataDir}
}

// start runs the workers and stops them when the test ends.
func (h *harness) start(ctx context.Context) {
	h.t.Helper()
	h.ingest.Start(ctx)
	h.t.Cleanup(h.ingest.Stop)
}

// deliver hands one message to the pipeline as SMTP would.
func (h *harness) deliver(ctx context.Context, from string, rcpts []string, raw string) {
	h.t.Helper()
	err := h.ingest.Deliver(ctx, smtp.Delivery{
		From:       from,
		Recipients: rcpts,
		Raw:        []byte(raw),
		ReceivedAt: time.Now(),
	})
	if err != nil {
		h.t.Fatalf("deliver: %v", err)
	}
}

// awaitSettled polls until the message reaches a terminal extraction
// state, because extraction is asynchronous by design.
func (h *harness) awaitSettled(ctx context.Context) store.Message {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msgs, err := h.store.ListMessages(ctx, store.MessageFilter{IncludeQuarantined: true})
		if err != nil {
			h.t.Fatalf("list: %v", err)
		}
		if len(msgs) == 1 && msgs[0].ExtractionState != store.ExtractionPending {
			return msgs[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatal("message never reached a terminal extraction state")
	return store.Message{}
}

const otpMessage = `From: Acme <noreply@acme.example>
To: %s
Subject: Your verification code
Content-Type: text/plain; charset=utf-8

Ma xac thuc cua ban la 481920. Ma het han sau 5 phut.
`

func mailTo(addr string) string { return fmt.Sprintf(otpMessage, addr) }

func TestDeliverStoresExtractsAndPublishes(t *testing.T) {
	h := newHarness(t, []string{"demo.test"})
	ctx := t.Context()

	// Subscribe before delivering. The bus contract is subscribe-then-act,
	// and a test that subscribed afterwards would be testing the replay
	// ring instead of the publish path.
	waiter, err := h.bus.SubscribeMatch(ctx, func(ev sse.Event) bool {
		return ev.Message != nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer waiter.Close()

	h.start(ctx)
	h.deliver(ctx, "bounce@acme.example", []string{"user@demo.test"}, mailTo("user@demo.test"))

	msg := h.awaitSettled(ctx)
	if msg.ExtractionState != store.ExtractionSuccess {
		t.Fatalf("extraction state = %q, want success", msg.ExtractionState)
	}
	if msg.Quarantined {
		t.Error("an allowlisted recipient was quarantined")
	}
	// The frozen rule: from_addr is the header From, not the envelope
	// sender, and the envelope sender lives in the ledger instead.
	if msg.FromAddr != "noreply@acme.example" {
		t.Errorf("from_addr = %q, want the header From", msg.FromAddr)
	}
	if msg.Subject != "Your verification code" {
		t.Errorf("subject = %q", msg.Subject)
	}

	var res extract.Result
	if err := json.Unmarshal([]byte(msg.ExtractedJSON), &res); err != nil {
		t.Fatalf("extraction json: %v", err)
	}
	if res.OTPBest != "481920" {
		t.Errorf("otp_best = %q, want 481920", res.OTPBest)
	}

	ev, ok := awaitEvent(t, waiter)
	if !ok {
		t.Fatal("no message event was published")
	}
	if ev.Message.ID != msg.ID {
		t.Errorf("published id = %q, want %q", ev.Message.ID, msg.ID)
	}

	// The envelope sender reaches evidence redacted. The typed ledger
	// event owns that, so a call site cannot pass the raw address even by
	// accident.
	assertLedgerAction(t, h, ledger.ActionMailReceived, "bou...@acme.example")
	assertLedgerHasNo(t, h, "bounce@acme.example")
}

// TestPublishFollowsTerminalExtraction is the ordering half of the ack
// contract: a subscriber that sees the event must find the outcome already
// stored, never a row still pending.
func TestPublishFollowsTerminalExtraction(t *testing.T) {
	h := newHarness(t, []string{"demo.test"})
	ctx := t.Context()

	waiter, err := h.bus.SubscribeMatch(ctx, func(ev sse.Event) bool { return ev.Message != nil })
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer waiter.Close()

	h.start(ctx)
	h.deliver(ctx, "bounce@acme.example", []string{"user@demo.test"}, mailTo("user@demo.test"))

	ev, ok := awaitEvent(t, waiter)
	if !ok {
		t.Fatal("no message event was published")
	}
	msg, err := h.store.Message(ctx, ev.Message.ID)
	if err != nil {
		t.Fatalf("message: %v", err)
	}
	if msg.ExtractionState == store.ExtractionPending {
		t.Fatal("the event arrived before the extraction state was committed")
	}
}

// TestAckImpliesDurableRow is the other half: Deliver returning nil - the
// only thing that produces a 250 - means the row is already committed.
func TestAckImpliesDurableRow(t *testing.T) {
	// One worker, one queue slot, and the worker parked, so extraction
	// cannot have run by the time Deliver returns. Anything found in the
	// store afterwards was committed before the ack.
	release := make(chan struct{})
	h := newHarness(t, []string{"demo.test"}, func(cfg *smtp.IngestConfig) {
		cfg.Workers = 1
		cfg.QueueSize = 1
	})
	h.ingest.SetExtractorForTest(func(in extract.Input) extract.Result {
		<-release
		return extract.Extract(in)
	})
	ctx := t.Context()
	h.start(ctx)
	defer close(release)

	h.deliver(ctx, "bounce@acme.example", []string{"user@demo.test"}, mailTo("user@demo.test"))

	msgs, err := h.store.ListMessages(ctx, store.MessageFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("stored %d messages immediately after the ack, want 1", len(msgs))
	}
	if msgs[0].ExtractionState != store.ExtractionPending {
		t.Errorf("state at ack = %q, want pending", msgs[0].ExtractionState)
	}
}

func TestQuarantineOffAllowlistRecipient(t *testing.T) {
	cases := []struct {
		name        string
		recipients  []string
		quarantined bool
	}{
		{"allowlisted", []string{"user@demo.test"}, false},
		{"subdomain of a wildcard", []string{"user@mail.demo.test"}, false},
		{"uppercase is the same domain", []string{"user@DEMO.TEST"}, false},
		{"off allowlist", []string{"someone@gmail.com"}, true},
		// The mixed case is the reason the rule is "any", not "all": a
		// staging app that copies a real address must not hand that
		// person's mail to the automated read path.
		{"one allowlisted and one not", []string{"user@demo.test", "real@customer.example"}, true},
		// A domain that looks like the allowlisted one only after Unicode
		// folding is a different domain. IDNA canonicalization is what
		// keeps them apart; a NOCASE compare would not.
		{"unicode lookalike", []string{"user@dеmo.test"}, true},
		{"unparseable address", []string{"not-an-address"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, []string{"demo.test", "*.demo.test"})
			ctx := t.Context()
			h.start(ctx)
			h.deliver(ctx, "bounce@acme.example", tc.recipients, mailTo(tc.recipients[0]))

			msg := h.awaitSettled(ctx)
			if msg.Quarantined != tc.quarantined {
				t.Errorf("quarantined = %v, want %v", msg.Quarantined, tc.quarantined)
			}
			if tc.quarantined {
				assertLedgerAction(t, h, ledger.ActionMailQuarantined, "")
			}
		})
	}
}

// TestQuarantinedMessageIsHiddenByDefault pins the read consequence of
// quarantine, which is the whole reason the flag exists.
func TestQuarantinedMessageIsHiddenByDefault(t *testing.T) {
	h := newHarness(t, []string{"demo.test"})
	ctx := t.Context()
	h.start(ctx)
	h.deliver(ctx, "bounce@acme.example", []string{"someone@gmail.com"}, mailTo("someone@gmail.com"))
	h.awaitSettled(ctx)

	visible, err := h.store.ListMessages(ctx, store.MessageFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(visible) != 0 {
		t.Errorf("a quarantined message was visible to the default read path")
	}
}

func TestExtractionPanicCommitsTerminalFailure(t *testing.T) {
	h := newHarness(t, []string{"demo.test"})
	h.ingest.SetExtractorForTest(func(extract.Input) extract.Result {
		panic("extraction blew up")
	})
	ctx := t.Context()
	h.start(ctx)
	h.deliver(ctx, "bounce@acme.example", []string{"user@demo.test"}, mailTo("user@demo.test"))

	msg := h.awaitSettled(ctx)
	if msg.ExtractionState != store.ExtractionFailed {
		t.Fatalf("state = %q, want failed", msg.ExtractionState)
	}
	if msg.ExtractedJSON != "" {
		t.Errorf("a failed extraction stored %q, want nothing", msg.ExtractedJSON)
	}
	assertLedgerAction(t, h, ledger.ActionExtractionFail, "")

	// A terminal row is never revisited. Recovery running again - which is
	// what a restart does - must leave it alone, or a message that
	// reliably panics would be retried forever.
	if err := h.ingest.Recover(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}
	after := h.awaitSettled(ctx)
	if after.ExtractionState != store.ExtractionFailed {
		t.Errorf("state after a second recovery pass = %q, want failed", after.ExtractionState)
	}
}

func TestStartupRecoveryCompletesPendingExtraction(t *testing.T) {
	dataDir := t.TempDir()
	ctx := t.Context()

	// A run that acked the message and died before extraction: the worker
	// is parked, so the row is committed pending and nothing settles it.
	stall := make(chan struct{})
	first := newHarnessAt(t, dataDir, []string{"demo.test"}, func(cfg *smtp.IngestConfig) {
		cfg.Workers = 1
		cfg.QueueSize = 1
	})
	first.ingest.SetExtractorForTest(func(extract.Input) extract.Result {
		<-stall
		return extract.Result{}
	})
	first.ingest.Start(ctx)
	// The stalled worker is what makes this a crash and not a shutdown:
	// the row stays pending because extraction never ran. Releasing it is
	// deferred to the end so the first pipeline cannot settle the row
	// that recovery is supposed to find, and draining it before the test
	// returns keeps a worker from touching a store that cleanup closed.
	defer func() {
		close(stall)
		first.ingest.Stop()
	}()
	first.deliver(ctx, "bounce@acme.example", []string{"user@demo.test"}, mailTo("user@demo.test"))

	pending, err := first.store.ListPendingExtractions(ctx, 0)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending rows = %d, want 1", len(pending))
	}

	// The restart: a fresh pipeline over the same directory, with a real
	// extractor, doing what serve does before it binds a port.
	second := newHarnessAt(t, dataDir, nil)
	waiter, err := second.bus.SubscribeMatch(ctx, func(ev sse.Event) bool { return ev.Message != nil })
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer waiter.Close()
	if err := second.ingest.Recover(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}

	msg := second.awaitSettled(ctx)
	if msg.ExtractionState != store.ExtractionSuccess {
		t.Fatalf("state after recovery = %q, want success", msg.ExtractionState)
	}
	var res extract.Result
	if err := json.Unmarshal([]byte(msg.ExtractedJSON), &res); err != nil {
		t.Fatalf("extraction json: %v", err)
	}
	if res.OTPBest != "481920" {
		t.Errorf("otp after recovery = %q, want 481920", res.OTPBest)
	}
	if _, ok := awaitEvent(t, waiter); !ok {
		t.Fatal("recovery settled the row but published nothing")
	}
}

// TestBackpressureHoldsAndDropsNothing proves the queue bound is
// backpressure and not a drop policy: with every worker parked and the
// queue full, Deliver blocks, and once the workers are released every
// message is accounted for.
func TestBackpressureHoldsAndDropsNothing(t *testing.T) {
	release := make(chan struct{})
	h := newHarness(t, []string{"demo.test"}, func(cfg *smtp.IngestConfig) {
		cfg.Workers = 1
		cfg.QueueSize = 1
	})
	h.ingest.SetExtractorForTest(func(in extract.Input) extract.Result {
		<-release
		return extract.Extract(in)
	})
	ctx := t.Context()
	h.start(ctx)

	const total = 8
	var wg sync.WaitGroup
	blocked := make(chan struct{}, total)
	for n := range total {
		wg.Add(1)
		go func() {
			defer wg.Done()
			blocked <- struct{}{}
			h.deliver(ctx, "bounce@acme.example", []string{"user@demo.test"},
				fmt.Sprintf("Subject: code %d\r\n\r\nMa xac thuc 48192%d\r\n", n, n))
		}()
	}
	for range total {
		<-blocked
	}

	// The queue is bounded, so senders must be waiting rather than
	// piling up. One is in the worker and one is in the slot.
	waitFor(t, func() bool { return h.ingest.QueueDepthForTest() == 1 })
	close(release)
	wg.Wait()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		msgs, err := h.store.ListMessages(ctx, store.MessageFilter{IncludeQuarantined: true})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		settled := 0
		for _, m := range msgs {
			if m.ExtractionState != store.ExtractionPending {
				settled++
			}
		}
		if settled == total {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("not every held message settled: backpressure dropped mail")
}

// TestMalformedMIMEIsStoredNotRefused covers the shapes that make a naive
// parser give up. Every one of them must still produce a stored, settled
// message, because a message the tool refused to keep is one an operator
// cannot debug.
func TestMalformedMIMEIsStoredNotRefused(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"headers only", "Subject: nothing else\r\n"},
		{"no headers at all", "just a body with 481920 in it\r\n"},
		{"unterminated multipart", "Content-Type: multipart/alternative; boundary=xyz\r\n\r\n--xyz\r\nContent-Type: text/plain\r\n\r\ncode 481920\r\n"},
		{"bad content type", "Content-Type: ???\r\n\r\ncode 481920\r\n"},
		{"unknown charset", "Content-Type: text/plain; charset=definitely-not-a-charset\r\n\r\ncode 481920\r\n"},
		{"bare newlines", "Subject: bare\nFrom: a@b.test\n\ncode 481920\n"},
		{"nul bytes in the body", "Subject: nul\r\n\r\ncode \x00 481920\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, []string{"demo.test"})
			ctx := t.Context()
			h.start(ctx)
			h.deliver(ctx, "bounce@acme.example", []string{"user@demo.test"}, tc.raw)
			msg := h.awaitSettled(ctx)
			if msg.ExtractionState != store.ExtractionSuccess {
				t.Errorf("state = %q, want success: malformed input must settle, not fail",
					msg.ExtractionState)
			}
			if msg.RawRef == "" {
				t.Error("the raw message was not stored")
			}
		})
	}
}

// TestEnvelopeAndHeaderRecipientsAreBothStored pins which addresses are
// trusted for matching and which are kept only as evidence.
func TestEnvelopeAndHeaderRecipientsAreBothStored(t *testing.T) {
	h := newHarness(t, []string{"demo.test"})
	ctx := t.Context()
	h.start(ctx)

	raw := "From: a@acme.example\r\n" +
		"To: visible@demo.test\r\n" +
		"Cc: copied@demo.test\r\n" +
		"Subject: code\r\n\r\nMa xac thuc 481920\r\n"
	// The envelope carries an address that appears in no header, which is
	// exactly what a Bcc looks like on the wire.
	h.deliver(ctx, "bounce@acme.example", []string{"hidden@demo.test"}, raw)

	msg := h.awaitSettled(ctx)
	kinds := map[string]string{}
	for _, r := range msg.Recipients {
		kinds[r.Addr] = r.Kind
	}
	if kinds["hidden@demo.test"] != store.RecipientEnvelope {
		t.Errorf("envelope recipient kind = %q", kinds["hidden@demo.test"])
	}
	if kinds["visible@demo.test"] != store.RecipientTo {
		t.Errorf("To recipient kind = %q", kinds["visible@demo.test"])
	}
	if kinds["copied@demo.test"] != store.RecipientCC {
		t.Errorf("Cc recipient kind = %q", kinds["copied@demo.test"])
	}
}

// TestHTMLOnlyMessageExtractsFromHTML covers the common provider shape: no
// plain-text alternative at all.
func TestHTMLOnlyMessageExtractsFromHTML(t *testing.T) {
	h := newHarness(t, []string{"demo.test"})
	ctx := t.Context()
	h.start(ctx)

	raw := "From: a@acme.example\r\nSubject: verify\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<html><body><p>Mã xác thực: <b>481920</b></p>" +
		`<a href="https://acme.example/verify?t=abc">Verify</a></body></html>` + "\r\n"
	h.deliver(ctx, "bounce@acme.example", []string{"user@demo.test"}, raw)

	msg := h.awaitSettled(ctx)
	if msg.HTMLRef == "" {
		t.Error("the html part was not stored as a blob")
	}
	var res extract.Result
	if err := json.Unmarshal([]byte(msg.ExtractedJSON), &res); err != nil {
		t.Fatalf("extraction json: %v", err)
	}
	if res.OTPBest != "481920" {
		t.Errorf("otp_best = %q, want 481920", res.OTPBest)
	}
	if len(res.Links) == 0 {
		t.Error("no link was extracted from the html part")
	}
}

func assertLedgerAction(t *testing.T, h *harness, action, wantDetail string) {
	t.Helper()
	entries, err := h.store.ListLedger(t.Context(), store.LedgerFilter{ProjectID: h.projectID})
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	for _, e := range entries {
		if e.Action != action {
			continue
		}
		if wantDetail == "" || strings.Contains(e.DetailJSON, wantDetail) {
			return
		}
	}
	t.Errorf("no ledger entry with action %q and detail containing %q", action, wantDetail)
}

// awaitEvent parks on a waiter with a bounded deadline, so a test that
// never gets its event fails with its own message instead of the package
// timeout.
func awaitEvent(t *testing.T, w *sse.Waiter) (sse.Event, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return w.Wait(ctx)
}

// assertLedgerHasNo fails if any ledger detail carries the value at all.
// The positive assertion above proves the redacted form is present; this
// proves the raw one is not, which is the half that matters.
func assertLedgerHasNo(t *testing.T, h *harness, value string) {
	t.Helper()
	entries, err := h.store.ListLedger(t.Context(), store.LedgerFilter{ProjectID: h.projectID})
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.DetailJSON, value) {
			t.Errorf("a ledger entry carries %q in full: %s", value, e.DetailJSON)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition never became true")
}
