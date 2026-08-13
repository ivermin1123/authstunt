package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivermin1123/authstunt/internal/api"
	"github.com/ivermin1123/authstunt/internal/personas"
	"github.com/ivermin1123/authstunt/internal/secrets"
	"github.com/ivermin1123/authstunt/internal/sse"
	"github.com/ivermin1123/authstunt/internal/store"
)

// harness is one server wired over a real store, which is what makes
// these tests worth running: the scope checks they assert are enforced by
// real rows, not by a stub that agrees with the handler.
type harness struct {
	t       *testing.T
	store   *store.Store
	service *personas.Service
	bus     *sse.Bus
	handler http.Handler
	project store.Project
	bearer  string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "keys"), "test")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, dir, key, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	project, err := st.CreateProject(ctx, "api-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAllowlist(ctx, project.ID, []string{"demo.test"}); err != nil {
		t.Fatal(err)
	}
	// The bus is wired here for the same reason serve wires it: without one
	// a claim answers from what is already stored and never parks, so every
	// test in this package would exercise the fast path only and the
	// long-poll contract would rest entirely on the one test that builds a
	// binary. The lost-wakeup seam is in this package's handler path, so it
	// has to be reachable from this package's tests.
	generation, err := st.NextEventGeneration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bus := sse.NewBus(generation)
	busCtx, stopBus := context.WithCancel(context.Background())
	go bus.Run(busCtx)
	t.Cleanup(stopBus)

	svc, err := personas.New(personas.Config{
		Store:     st,
		ProjectID: project.ID,
		Allowlist: []string{"demo.test"},
		Bus:       bus,
		Logger:    slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	bearer, err := st.SetProjectBearer(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := api.New(api.Config{
		Store:     st,
		Service:   svc,
		ProjectID: project.ID,
		Version:   "test",
		Logger:    slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{
		t: t, store: st, service: svc, bus: bus,
		handler: srv.Handler(), project: project, bearer: bearer,
	}
}

// do issues a request with the credential given. An empty token sends no
// Authorization header at all.
func (h *harness) do(method, path, token string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, reader)
	// httptest defaults the Host to example.com, which the allowlist
	// correctly refuses. Every test that is not about the Host check
	// therefore sends a loopback name, the way a real client would.
	req.Host = "localhost:8925"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

// newRun creates a run through the surface and returns its id and token.
func (h *harness) newRun() (string, string) {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/runs", h.bearer, nil)
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("create run: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		RunID    string `json:"run_id"`
		RunToken string `json:"run_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		h.t.Fatal(err)
	}
	return body.RunID, body.RunToken
}

func (h *harness) acquire(runID, token, role string) string {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/runs/"+runID+"/leases", token,
		map[string]string{"role": role})
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("acquire: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		LeaseID string `json:"lease_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		h.t.Fatal(err)
	}
	return body.LeaseID
}

func codeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func TestRunLifecycleThroughTheSurface(t *testing.T) {
	h := newHarness(t)
	runID, runToken := h.newRun()

	if !strings.HasPrefix(runToken, store.RunTokenPrefix) {
		t.Fatalf("run token %q lacks the redaction prefix", store.RunTokenPrefix)
	}

	leaseID := h.acquire(runID, runToken, "gv-free")

	// The lease reports the mode actually served, and the default is
	// ephemeral.
	rec := h.do(http.MethodPost, "/api/runs/"+runID+"/leases", runToken,
		map[string]string{"role": "gv-paid"})
	var grant struct {
		Mode         string `json:"mode"`
		Addr         string `json:"addr"`
		PooledPolicy any    `json:"pooled_policy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &grant); err != nil {
		t.Fatal(err)
	}
	if grant.Mode != store.ModeEphemeral {
		t.Fatalf("mode = %q, want ephemeral", grant.Mode)
	}
	if grant.PooledPolicy != nil {
		t.Fatal("pooled policy reported on a server that has none")
	}
	if !strings.HasSuffix(grant.Addr, "@demo.test") {
		t.Fatalf("addr %q is not under the allowlisted domain", grant.Addr)
	}

	// Release is idempotent, because teardown paths overlap.
	for range 2 {
		rec := h.do(http.MethodDelete, "/api/leases/"+leaseID, runToken, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("release: %d %s", rec.Code, rec.Body.String())
		}
	}

	// Ending twice the same way is not an error either.
	for range 2 {
		rec := h.do(http.MethodPost, "/api/runs/"+runID+"/end", runToken,
			map[string]string{"state": store.RunComplete})
		if rec.Code != http.StatusOK {
			t.Fatalf("end run: %d %s", rec.Code, rec.Body.String())
		}
	}

	// A terminal run refuses further work with its reason code.
	rec = h.do(http.MethodPost, "/api/runs/"+runID+"/leases", runToken,
		map[string]string{"role": "gv-free"})
	if got := codeOf(t, rec); got != store.ReasonRunNotActive {
		t.Fatalf("acquire on an ended run = %q, want %q", got, store.ReasonRunNotActive)
	}
}

func TestRunTokenCannotReadAnotherRun(t *testing.T) {
	h := newHarness(t)
	mineID, mineToken := h.newRun()
	theirsID, theirsToken := h.newRun()

	// Every run-scoped route, driven with the wrong run's token. Each
	// must answer 404 rather than 403: telling an unauthorized caller
	// that an id exists is a membership oracle, and the id is the only
	// thing separating two runs of one project.
	cases := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/runs/" + theirsID + "/evidence", nil},
		{http.MethodPost, "/api/runs/" + theirsID + "/end", map[string]string{"state": store.RunComplete}},
		{http.MethodPost, "/api/runs/" + theirsID + "/leases", map[string]string{"role": "gv-free"}},
	}
	for _, c := range cases {
		rec := h.do(c.method, c.path, mineToken, c.body)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s with a foreign run token = %d, want 404", c.method, c.path, rec.Code)
		}
	}

	// A lease belonging to another run is equally invisible.
	theirLease := h.acquire(theirsID, theirsToken, "gv-free")
	for _, c := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodDelete, "/api/leases/" + theirLease, nil},
		{http.MethodPost, "/api/leases/" + theirLease + "/claims", map[string]any{
			"kind": store.ClaimEmailOTP, "idempotency_key": "k",
		}},
	} {
		rec := h.do(c.method, c.path, mineToken, c.body)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s with a foreign run token = %d, want 404", c.method, c.path, rec.Code)
		}
	}

	// And the run token cannot create a run: that is the bearer's job,
	// and a token that could mint its own successor would be unbounded.
	if rec := h.do(http.MethodPost, "/api/runs", mineToken, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("create run with a run token = %d, want 403", rec.Code)
	}
	_ = mineID
}

func TestSecretReadsRequireAuthenticatedPrincipal(t *testing.T) {
	h := newHarness(t)
	runID, runToken := h.newRun()
	leaseID := h.acquire(runID, runToken, "gv-free")

	claimPath := "/api/leases/" + leaseID + "/claims"
	claimBody := map[string]any{"kind": store.ClaimEmailOTP, "idempotency_key": "k"}

	// No credential at all.
	if rec := h.do(http.MethodPost, claimPath, "", claimBody); rec.Code != http.StatusUnauthorized {
		t.Fatalf("claim with no credential = %d, want 401", rec.Code)
	}
	// A well-formed token that authenticates nothing.
	for _, bogus := range []string{
		store.RunTokenPrefix + "not-a-real-token",
		store.ProjectBearerPrefix + "not-a-real-token",
		"garbage-with-no-prefix",
	} {
		if rec := h.do(http.MethodPost, claimPath, bogus, claimBody); rec.Code != http.StatusUnauthorized {
			t.Fatalf("claim with %q = %d, want 401", bogus, rec.Code)
		}
	}

	// Every authenticated route refuses an anonymous caller too.
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/runs"},
		{http.MethodGet, "/api/runs/" + runID + "/evidence"},
		{http.MethodPost, "/api/runs/" + runID + "/leases"},
		{http.MethodDelete, "/api/leases/" + leaseID},
	} {
		if rec := h.do(c.method, c.path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s anonymous = %d, want 401", c.method, c.path, rec.Code)
		}
	}
}

func TestClaimReturnsNoSecretOnAnyFailurePath(t *testing.T) {
	h := newHarness(t)
	runID, runToken := h.newRun()
	leaseID := h.acquire(runID, runToken, "gv-free")

	// No mail has arrived, so this is the no-binding path. It must carry
	// its reason code and no value field at all.
	rec := h.do(http.MethodPost, "/api/leases/"+leaseID+"/claims", runToken, map[string]any{
		"kind": store.ClaimEmailOTP, "idempotency_key": "attempt-1", "timeout_ms": 0,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim = %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["value"]; ok {
		t.Fatalf("a failed claim carried a value field: %s", rec.Body.String())
	}
	reason, _ := body["reason"].(string)
	if reason == store.ReasonOK {
		t.Fatal("a claim with no mail reported success")
	}
	if reason == "" {
		t.Fatalf("a claim answered without a reason code: %s", rec.Body.String())
	}

	// TOTP is a refused stub, not a silent failure.
	rec = h.do(http.MethodPost, "/api/leases/"+leaseID+"/claims", runToken, map[string]any{
		"kind": store.ClaimTOTP, "idempotency_key": "attempt-2",
	})
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("totp claim = %d, want 501", rec.Code)
	}

	// An idempotency key is mandatory: without one a retry would consume
	// a second message.
	rec = h.do(http.MethodPost, "/api/leases/"+leaseID+"/claims", runToken, map[string]any{
		"kind": store.ClaimEmailOTP,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("claim without an idempotency key = %d, want 400", rec.Code)
	}
}

func TestEvidenceIsRedactedAndScoped(t *testing.T) {
	h := newHarness(t)
	runID, runToken := h.newRun()

	// The granted address is read from the acquire response, because it
	// is the exact string that must not reappear in evidence.
	rec := h.do(http.MethodPost, "/api/runs/"+runID+"/leases", runToken,
		map[string]string{"role": "gv-free"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("acquire: %d %s", rec.Code, rec.Body.String())
	}
	var grant struct {
		Addr string `json:"addr"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &grant); err != nil {
		t.Fatal(err)
	}
	localPart, _, ok := strings.Cut(grant.Addr, "@")
	if !ok || localPart == "" {
		t.Fatalf("granted address %q has no local part", grant.Addr)
	}

	rec = h.do(http.MethodGet, "/api/runs/"+runID+"/evidence", runToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("evidence = %d %s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()

	// The acquire wrote ledger events carrying the leased address. The
	// ledger redacts the local part on the way in and keeps the domain,
	// which is deliberate: the domain is the operator's own allowlist
	// entry and identifies nobody, while the local part is what ties an
	// address to one run. This asserts the API did not route around that
	// marshaller by rebuilding the JSON itself.
	if strings.Contains(raw, grant.Addr) {
		t.Fatalf("evidence exposed the full leased address: %s", raw)
	}
	if strings.Contains(raw, localPart) {
		t.Fatalf("evidence exposed the address local part %q: %s", localPart, raw)
	}
	if !strings.Contains(raw, "...@demo.test") {
		t.Fatalf("evidence did not carry the redacted address at all: %s", raw)
	}
	var body struct {
		RunID  string `json:"run_id"`
		Events []struct {
			Action string `json:"action"`
		} `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RunID != runID {
		t.Fatalf("evidence run_id = %q, want %q", body.RunID, runID)
	}
	if len(body.Events) == 0 {
		t.Fatal("the acquire produced no evidence")
	}

	// The project bearer may read any run of its project.
	if rec := h.do(http.MethodGet, "/api/runs/"+runID+"/evidence", h.bearer, nil); rec.Code != http.StatusOK {
		t.Fatalf("bearer evidence read = %d", rec.Code)
	}
}

func TestForeignHostRejected(t *testing.T) {
	h := newHarness(t)

	for _, host := range []string{
		"attacker.example",        // a rebound name pointing at 127.0.0.1
		"authstunt.local.evil.co", // a suffix that merely contains a good name
		"",                        // no Host at all
	} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("Host %q = %d, want 403", host, rec.Code)
		}
	}

	// The loopback names are accepted, with and without a port.
	for _, host := range []string{"localhost", "127.0.0.1:8925", "localhost:8925", "[::1]:8925"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Host %q = %d, want 200", host, rec.Code)
		}
	}
}

func TestOriginCheckBlocksCrossSite(t *testing.T) {
	h := newHarness(t)

	// A browser page on another origin, holding a stolen token, is
	// refused before the credential is even looked at.
	for _, origin := range []string{"https://evil.example", "null", "http://127.0.0.1.evil.example"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/runs", nil)
		req.Host = "localhost:8925"
		req.Header.Set("Origin", origin)
		req.Header.Set("Authorization", "Bearer "+h.bearer)
		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("Origin %q = %d, want 403", origin, rec.Code)
		}
	}

	// A same-origin browser request is fine, and so is a CLI that sends
	// no Origin at all - which the rest of these tests already prove.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/runs", nil)
	req.Host = "localhost:8925"
	req.Header.Set("Origin", "http://localhost:8925")
	req.Header.Set("Authorization", "Bearer "+h.bearer)
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("same-origin request = %d, want 201", rec.Code)
	}
}

func TestOversizedBodyRejected(t *testing.T) {
	h := newHarness(t)
	runID, runToken := h.newRun()

	huge := strings.Repeat("a", api.MaxRequestBytes+1)
	rec := h.do(http.MethodPost, "/api/runs/"+runID+"/leases", runToken,
		map[string]string{"role": huge})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d, want 413", rec.Code)
	}
}

func TestPooledIsNeverServedWithoutAPolicy(t *testing.T) {
	h := newHarness(t)
	runID, runToken := h.newRun()

	// The harness configures no pooled policy, so a pooled request is
	// refused by name. What must never happen is a silent downgrade to
	// ephemeral, which would hand back a working lease in the wrong mode.
	rec := h.do(http.MethodPost, "/api/runs/"+runID+"/leases", runToken,
		map[string]string{"role": "gv-free", "mode": store.ModePooled})
	if rec.Code == http.StatusCreated {
		t.Fatalf("a pooled request was served without a policy: %s", rec.Body.String())
	}
	if got := codeOf(t, rec); got != store.ReasonPooledPolicyMissing {
		t.Fatalf("pooled refusal = %q, want %q", got, store.ReasonPooledPolicyMissing)
	}

	// An unknown mode is refused rather than coerced.
	rec = h.do(http.MethodPost, "/api/runs/"+runID+"/leases", runToken,
		map[string]string{"role": "gv-free", "mode": "whatever"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown mode = %d, want 400", rec.Code)
	}
}

func TestHealthExposesNoSecretOrCount(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
	raw := rec.Body.String()
	// The project id, the bearer and anything address-shaped stay out of
	// an unauthenticated response.
	for _, forbidden := range []string{h.bearer, h.project.ID, h.project.Name, "@demo.test"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("healthz leaked %q: %s", forbidden, raw)
		}
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["pooled_configured"] != false {
		t.Fatalf("pooled_configured = %v, want false", body["pooled_configured"])
	}
	if body["default_mode"] != store.ModeEphemeral {
		t.Fatalf("default_mode = %v, want ephemeral", body["default_mode"])
	}
}
