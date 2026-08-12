package personas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/ivermin1123/authstunt/internal/store"
)

// maxSeedResponse caps what the adapter will read back.
//
// The endpoint belongs to the application under test, which is the thing
// most likely to be broken: a misconfigured route can answer with a whole
// HTML error page, and reading it into memory to discover it is not JSON
// helps nobody.
const maxSeedResponse = 64 << 10

// HTTPSeeder calls an endpoint the application under test owns.
//
// There is no shell anywhere on this path, by design and not by omission.
// A seed hook that ran a command would make the tool a way to execute
// whatever a configuration file said, and the whole value of seeding is a
// postcondition, which an HTTP call states just as well.
type HTTPSeeder struct {
	url    string
	client *http.Client
}

// NewHTTPSeeder validates the URL and returns a seeder.
//
// The URL is fixed here, at startup, and never taken per request. A caller
// holding a run token must not be able to choose where the server sends a
// POST: that would turn every run into a way to make this process talk to
// an address of the caller's choosing.
func NewHTTPSeeder(raw string, timeout time.Duration) (*HTTPSeeder, error) {
	if timeout <= 0 {
		timeout = DefaultSeedTimeout
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("personas: seed url: %w", err)
	}
	if !u.IsAbs() {
		return nil, fmt.Errorf("personas: seed url %q must be absolute", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("personas: seed url scheme %q is not http or https", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("personas: seed url %q has no host", raw)
	}
	if u.User != nil {
		// Credentials in a URL end up in logs and process listings. If
		// the endpoint needs authentication it belongs in a header the
		// operator configures, not in the address.
		return nil, fmt.Errorf("personas: seed url must not carry userinfo")
	}
	return &HTTPSeeder{
		url: u.String(),
		client: &http.Client{
			Timeout: timeout,
			// A redirect would move the request to a host the operator
			// did not configure, quietly. Refusing keeps the destination
			// exactly what was approved at startup.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("personas: seed endpoint attempted a redirect")
			},
		},
	}, nil
}

// seedPayload is the request body. It carries no secret: an address, some
// ids, and the role being seeded.
type seedPayload struct {
	RunID      string `json:"run_id"`
	LeaseID    string `json:"lease_id"`
	IdentityID string `json:"identity_id"`
	Addr       string `json:"addr"`
	Role       string `json:"role"`
	Mode       string `json:"mode"`
}

// seedResponse is the shape the application must answer with.
type seedResponse struct {
	Status      string `json:"status"`
	Fingerprint string `json:"fingerprint"`
}

// Seed posts one seed request and interprets the answer.
//
// Every failure is the same failure. A non-200, a timeout, a redirect, a
// body past the cap, a body that is not JSON, a status the schema does not
// name - all of them mean the account is not known to be ready, and the
// lease that asked must not become claimable. There is no partial success
// to represent, which is why this returns an error rather than a third
// status.
func (h *HTTPSeeder) Seed(ctx context.Context, req SeedRequest) (SeedResult, error) {
	body, err := json.Marshal(seedPayload{
		RunID:      req.RunID,
		LeaseID:    req.LeaseID,
		IdentityID: req.IdentityID,
		Addr:       req.Addr,
		Role:       req.Role,
		Mode:       req.Mode,
	})
	if err != nil {
		return SeedResult{}, fmt.Errorf("personas: encode seed request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		return SeedResult{}, fmt.Errorf("personas: seed request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)

	resp, err := h.client.Do(httpReq)
	if err != nil {
		return SeedResult{}, fmt.Errorf("personas: seed call: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxSeedResponse))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return SeedResult{}, fmt.Errorf("personas: seed endpoint answered %s", resp.Status)
	}

	// One byte past the cap is read on purpose, so an oversized body is
	// detected rather than silently truncated into something that might
	// still parse.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSeedResponse+1))
	if err != nil {
		return SeedResult{}, fmt.Errorf("personas: read seed response: %w", err)
	}
	if len(raw) > maxSeedResponse {
		return SeedResult{}, fmt.Errorf("personas: seed response exceeds %d bytes", maxSeedResponse)
	}

	var parsed seedResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return SeedResult{}, fmt.Errorf("personas: seed response is not the documented JSON: %w", err)
	}
	switch parsed.Status {
	case store.SeedStateSeeded, store.SeedStateSkipped:
		// The wire type and the result type carry the same fields, so
		// this is a conversion rather than a copy. Adding a field to one
		// without the other stops compiling, which is the point.
		return SeedResult(parsed), nil
	case "":
		return SeedResult{}, errors.New("personas: seed response carries no status")
	default:
		return SeedResult{}, fmt.Errorf("personas: seed response status %q is not seeded or skipped", parsed.Status)
	}
}

// detailJSON encodes a ledger detail object.
//
// The map is built at the call site from named fields, so an encoding
// failure is not a real branch; an empty object keeps the column valid
// JSON if it somehow happened.
func detailJSON(fields map[string]string) string {
	b, err := json.Marshal(fields)
	if err != nil {
		return "{}"
	}
	return string(b)
}
