package api_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/smtp"
	"github.com/ivermin1123/authstunt/internal/store"
)

// The long-poll contract used to be proved in exactly one place, by a test
// that builds a binary. That made the slowest and most skippable test in the
// tree the only thing standing between a wiring mistake and a claim that
// accepts timeout_ms and ignores it. These tests hold the same contract at
// the handler, where the seam actually lives.

const otpMail = `From: Acme <noreply@acme.example>
To: %s
Subject: Your verification code
Content-Type: text/plain; charset=utf-8

Ma xac thuc cua ban la 481920. Ma het han sau 5 phut.
`

// grant acquires a lease and returns its id and the address mail must be
// sent to, which the existing helper does not expose.
func (h *harness) grant(runID, token, role string) (leaseID, addr string) {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/v1/runs/"+runID+"/leases", token,
		map[string]string{"role": role})
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("acquire: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		LeaseID string `json:"lease_id"`
		Addr    string `json:"addr"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		h.t.Fatal(err)
	}
	return body.LeaseID, body.Addr
}

// ingest builds the real delivery pipeline over this harness's store and
// bus. Mail is delivered the way SMTP delivers it, so the wakeup under test
// is the one production publishes.
func (h *harness) ingest() *smtp.Ingest {
	h.t.Helper()
	in, err := smtp.NewIngest(smtp.IngestConfig{
		Store:     h.store,
		Bus:       h.bus,
		ProjectID: h.project.ID,
		Allowlist: []string{"demo.test"},
		Logger:    slog.New(slog.DiscardHandler),
	})
	if err != nil {
		h.t.Fatalf("ingest: %v", err)
	}
	in.Start(h.t.Context())
	h.t.Cleanup(in.Stop)
	return in
}

type claimBody struct {
	Reason    string `json:"reason"`
	Value     string `json:"value"`
	MessageID string `json:"message_id"`
	TimedOut  bool   `json:"timed_out"`
	WaitedMS  int64  `json:"waited_ms"`
}

func (h *harness) claim(leaseID, token string, timeoutMS int64, key string) claimBody {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/v1/leases/"+leaseID+"/claims", token, map[string]any{
		"kind": store.ClaimEmailOTP, "idempotency_key": key, "timeout_ms": timeoutMS,
	})
	if rec.Code != http.StatusOK {
		h.t.Fatalf("claim = %d %s", rec.Code, rec.Body.String())
	}
	var body claimBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		h.t.Fatal(err)
	}
	return body
}

// TestAClaimParksUntilTheMailArrives is the long-poll contract stated at the
// surface: a claim issued before the mail exists waits for it instead of
// answering from an empty store.
//
// waited_ms is asserted above zero because a claim that returns instantly is
// exactly what a missing event bus produces, and the response would otherwise
// be indistinguishable from a correct one that got lucky on timing.
func TestAClaimParksUntilTheMailArrives(t *testing.T) {
	h := newHarness(t)
	in := h.ingest()
	runID, runToken := h.newRun()
	leaseID, addr := h.grant(runID, runToken, "signup")

	// The mail lands well after the claim is issued, so a claim that does
	// not park cannot pass this test by finding it on the first query.
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = in.Deliver(h.t.Context(), smtp.Delivery{
			From:       "bounce@acme.example",
			Recipients: []string{addr},
			Raw:        []byte(fmt.Sprintf(otpMail, addr)),
			ReceivedAt: time.Now(),
		})
	}()

	got := h.claim(leaseID, runToken, 10_000, "parked-1")
	if got.Reason != store.ReasonOK {
		t.Fatalf("reason = %q, want %q", got.Reason, store.ReasonOK)
	}
	if got.Value != "481920" {
		t.Errorf("value = %q, want the code from the message", got.Value)
	}
	if got.TimedOut {
		t.Error("timed_out was true on a claim that got its code")
	}
	if got.WaitedMS <= 0 {
		t.Errorf("waited_ms = %d, want a positive wait: a claim that returned "+
			"instantly never parked, which is what a missing bus looks like",
			got.WaitedMS)
	}
	if got.MessageID == "" {
		t.Error("a successful claim did not name the message it came from")
	}
}

// TestAClaimThatWaitsAndGetsNothingReportsTheWait covers the other half of
// the same two fields: the timeout path has to say it timed out and how long
// it actually waited.
func TestAClaimThatWaitsAndGetsNothingReportsTheWait(t *testing.T) {
	h := newHarness(t)
	h.ingest()
	runID, runToken := h.newRun()
	leaseID, _ := h.grant(runID, runToken, "signup")

	started := time.Now()
	got := h.claim(leaseID, runToken, 700, "waited-1")
	elapsed := time.Since(started)

	if got.Reason != store.ReasonNoBinding {
		t.Fatalf("reason = %q, want %q: no mail was ever addressed to this lease",
			got.Reason, store.ReasonNoBinding)
	}
	// timed_out is reserved for the bare timeout reason, so a claim that can
	// say something more specific reports false here even though it waited.
	if got.TimedOut {
		t.Errorf("timed_out = true alongside reason %q", got.Reason)
	}
	if got.WaitedMS <= 0 {
		t.Errorf("waited_ms = %d, want a positive wait", got.WaitedMS)
	}
	if elapsed < 600*time.Millisecond {
		t.Errorf("the call returned after %s, so it did not park for its budget", elapsed)
	}
}

// TestClaimTimeoutBoundsAreEnforced covers the refusal branch, which nothing
// in this package reached before: every claim test sent timeout_ms 0.
func TestClaimTimeoutBoundsAreEnforced(t *testing.T) {
	h := newHarness(t)
	runID, runToken := h.newRun()
	leaseID, _ := h.grant(runID, runToken, "signup")

	for _, timeout := range []int64{-1, -60_000, 120_001, 600_000} {
		rec := h.do(http.MethodPost, "/api/v1/leases/"+leaseID+"/claims", runToken,
			map[string]any{
				"kind": store.ClaimEmailOTP, "idempotency_key": "bounds-1",
				"timeout_ms": timeout,
			})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("timeout_ms %d = %d, want 400", timeout, rec.Code)
			continue
		}
		if got := codeOf(t, rec); got != "bad_request" {
			t.Errorf("timeout_ms %d error code = %q", timeout, got)
		}
	}

	// The upper bound is inclusive: the documented maximum is a value a
	// caller may send, not the first one that is refused. The mail is sent
	// first so the claim settles on its first query; a claim that has to
	// wait this budget out would park for two minutes to prove one
	// comparison.
	in := h.ingest()
	boundedLease, addr := h.grant(runID, runToken, "bounded")
	if err := in.Deliver(t.Context(), smtp.Delivery{
		From:       "bounce@acme.example",
		Recipients: []string{addr},
		Raw:        []byte(fmt.Sprintf(otpMail, addr)),
		ReceivedAt: time.Now(),
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	rec := h.do(http.MethodPost, "/api/v1/leases/"+boundedLease+"/claims", runToken,
		map[string]any{
			"kind": store.ClaimEmailOTP, "idempotency_key": "bounds-max",
			"timeout_ms": 120_000,
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("timeout_ms 120000 = %d %s, want it accepted",
			rec.Code, rec.Body.String())
	}
}
