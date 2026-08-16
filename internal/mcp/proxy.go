package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// maxBodyBytes caps how much of a response is read.
//
// The largest frozen body is a lease grant: a handful of short strings.
// The cap is generous against that and stops a misconfigured URL - a
// proxy login page, say - from being pulled into memory and then into a
// model's context.
const maxBodyBytes = 1 << 20

// defaultClaimTimeoutMS is what claim_code sends when the model names no
// timeout.
//
// F3 defaults a missing timeout_ms to 0, meaning do not wait at all. An
// agent that claims right after submitting a form would get
// claim_no_binding every time and learn to poll, which is the guessing
// loop long-poll exists to end. Thirty seconds is the frozen surface's
// own documented working range, well under its 120000 cap.
const defaultClaimTimeoutMS = 30000

// requestMargin is how much longer than the claim's own deadline the HTTP
// request is given. A request canceled at exactly the long-poll deadline
// would turn a legitimate timed_out answer into a transport failure.
const requestMargin = 30 * time.Second

// httpResult is one answer from the AuthStunt server, kept as the status
// and the bytes exactly as they arrived.
//
// The body is never re-encoded on the way to a tool result: what H-1.5
// promises is the HTTP body verbatim, and a decode-then-encode round trip
// is the standard way that promise quietly stops being true.
type httpResult struct {
	status int
	body   []byte
}

func (r httpResult) ok() bool { return r.status >= 200 && r.status < 300 }

// proxy talks to a running AuthStunt server and remembers the credentials
// a model must never see.
//
// Two maps, both process-local and both deliberately not persisted:
//
//   - runTokens holds the rt_ token minted for each run. It is used for
//     every call after the run is opened, so a mistake inside this
//     process reaches one run and stops at a 404 rather than touching
//     another run of the same project.
//   - leaseRuns remembers which run a lease came from, because the claim
//     and release routes are addressed by lease id while the credential
//     that authorizes them belongs to the run.
//
// Both die with the process. A client that restarts this server mid
// session leaves the model holding ids this proxy can no longer act on,
// and the tools say so rather than failing obscurely; see notFoundResult.
type proxy struct {
	baseURL string
	bearer  string
	client  *http.Client

	mu        sync.Mutex
	runTokens map[string]string
	leaseRuns map[string]string
}

func newProxy(baseURL, bearer string, client *http.Client) *proxy {
	return &proxy{
		baseURL:   strings.TrimSuffix(baseURL, "/"),
		bearer:    bearer,
		client:    client,
		runTokens: map[string]string{},
		leaseRuns: map[string]string{},
	}
}

// openRun is F1. It is the only call that uses the project bearer,
// because it is the only route the frozen contract lets a bearer reach.
//
// On success the run token is lifted out of the body and kept here. The
// body handed back has that one field removed and nothing else changed.
func (p *proxy) openRun(ctx context.Context) (httpResult, error) {
	result, err := p.do(ctx, http.MethodPost, "/api/v1/runs", p.bearer, nil)
	if err != nil || !result.ok() {
		return result, err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result.body, &fields); err != nil {
		return httpResult{}, fmt.Errorf("mcp: run response is not an object: %w", err)
	}
	var runID, runToken string
	if err := json.Unmarshal(fields["run_id"], &runID); err != nil {
		return httpResult{}, fmt.Errorf("mcp: run response has no run id: %w", err)
	}
	if err := json.Unmarshal(fields["run_token"], &runToken); err != nil {
		return httpResult{}, fmt.Errorf("mcp: run response has no run token: %w", err)
	}
	delete(fields, "run_token")

	p.mu.Lock()
	p.runTokens[runID] = runToken
	p.mu.Unlock()

	stripped, err := json.Marshal(fields)
	if err != nil {
		return httpResult{}, err
	}
	return httpResult{status: result.status, body: stripped}, nil
}

// leaseIdentity is F2. Mode is sent explicitly rather than left to the
// server's default: pooled is outside the freeze, and a tool that omitted
// the field would inherit whatever the default becomes later.
func (p *proxy) leaseIdentity(ctx context.Context, runID, role string) (httpResult, error) {
	token, ok := p.runToken(runID)
	if !ok {
		return notFoundResult("no run token is held for that run_id; open a new run with open_run"), nil
	}
	body, err := json.Marshal(map[string]string{"role": role, "mode": "ephemeral"})
	if err != nil {
		return httpResult{}, err
	}
	result, err := p.do(ctx, http.MethodPost, "/api/v1/runs/"+runID+"/leases", token, body)
	if err != nil || !result.ok() {
		return result, err
	}

	var grant struct {
		LeaseID string `json:"lease_id"`
	}
	if err := json.Unmarshal(result.body, &grant); err != nil {
		return httpResult{}, fmt.Errorf("mcp: lease response is not an object: %w", err)
	}
	p.mu.Lock()
	p.leaseRuns[grant.LeaseID] = runID
	p.mu.Unlock()
	return result, nil
}

// claimCode is F3.
//
// The idempotency key is derived here and is not a tool parameter. See
// idempotencyKey for why that is a safety property rather than a
// convenience.
func (p *proxy) claimCode(ctx context.Context, leaseID, kind string, timeoutMS int64, attempt int64) (httpResult, error) {
	token, ok := p.tokenForLease(leaseID)
	if !ok {
		return notFoundResult("no run token is held for that lease_id; lease an identity with lease_identity"), nil
	}
	body, err := json.Marshal(map[string]any{
		"kind":            kind,
		"idempotency_key": idempotencyKey(leaseID, kind, attempt),
		"timeout_ms":      timeoutMS,
	})
	if err != nil {
		return httpResult{}, err
	}
	// The deadline outlives the long-poll it is carrying, so a claim that
	// waits its full timeout still gets to report that it did.
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond+requestMargin)
	defer cancel()
	return p.do(ctx, http.MethodPost, "/api/v1/leases/"+leaseID+"/claims", token, body)
}

// releaseLease is F4.
func (p *proxy) releaseLease(ctx context.Context, leaseID string) (httpResult, error) {
	token, ok := p.tokenForLease(leaseID)
	if !ok {
		return notFoundResult("no run token is held for that lease_id; it was never leased through this server, or the server restarted"), nil
	}
	return p.do(ctx, http.MethodDelete, "/api/v1/leases/"+leaseID, token, nil)
}

// idempotencyKey derives F3's required key from what the model already
// said, so the model never writes one.
//
// This is the single field of the frozen surface where a wrong value
// silently burns a real message. Replay is looked up by (lease, key), and
// a claim that timed out writes no claim row, so reusing a key waits
// again after a failure and replays the same secret after a success -
// exactly what a retry wants. A model asked for a field named "key"
// generates a fresh uuid every call, which turns every retry into a
// demand for a new message. Deriving it moves that from a judgment call
// to a property of the tool.
//
// attempt is how a caller says "a new message, please" - the resend case -
// and it has to be a deliberate, visible act rather than a side effect of
// calling the tool twice.
func idempotencyKey(leaseID, kind string, attempt int64) string {
	return fmt.Sprintf("mcp:%s:%s:%d", leaseID, kind, attempt)
}

func (p *proxy) runToken(runID string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	token, ok := p.runTokens[runID]
	return token, ok
}

func (p *proxy) tokenForLease(leaseID string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	runID, ok := p.leaseRuns[leaseID]
	if !ok {
		return "", false
	}
	token, ok := p.runTokens[runID]
	return token, ok
}

// notFoundResult is the answer to an id this process has no credential
// for.
//
// It wears the server's own error envelope and the server's own not_found
// code rather than a vocabulary of this package's invention, so a model
// branches on it the same way it branches on a real 404.
func notFoundResult(message string) httpResult {
	body, _ := json.Marshal(map[string]any{
		"error": map[string]string{"code": "not_found", "message": message},
	})
	return httpResult{status: http.StatusNotFound, body: body}
}

// do makes one request and reads the answer.
//
// The credential goes in the Authorization header and nowhere else. No
// error returned from here carries it, or the URL it was sent to.
func (p *proxy) do(ctx context.Context, method, path, token string, body []byte) (httpResult, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, reader)
	if err != nil {
		return httpResult{}, fmt.Errorf("mcp: could not build the request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		// The error string from net/http carries the full URL. It is not
		// a credential, but it is the address of a server the model has
		// no business learning, and a message this generic is what the
		// model can act on anyway.
		return httpResult{}, fmt.Errorf("mcp: the AuthStunt server named by AUTHSTUNT_URL could not be reached")
	}
	defer func() { _ = resp.Body.Close() }()

	read, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return httpResult{}, fmt.Errorf("mcp: the AuthStunt server's answer could not be read")
	}
	return httpResult{status: resp.StatusCode, body: bytes.TrimSpace(read)}, nil
}
