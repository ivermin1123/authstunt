package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// This file covers a class of defect the rest of the suite cannot see.
//
// Every other test either drives a component with its dependencies handed
// over by hand, which proves the component works, or drives the binary
// without touching the API, which proves the mail path works. Neither one
// proves the shipped binary wired the component graph together. A
// dependency the service treats as optional can go missing in serve.go and
// every test still passes, because the tests supply it themselves.
//
// That is not hypothetical: personas.Config.Bus went unpassed, so a claim
// could not park on the bus and a caller's timeout_ms was accepted,
// validated, echoed back in waited_ms, and silently ignored. The tests for
// the claim path passed throughout, because they built the service with a
// bus of their own.
//
// So the assertion here is deliberately made through the outermost surface
// there is: the real binary, its real HTTP API, and a real SMTP delivery.

// apiRequest posts a JSON body to the running API and decodes the reply.
//
// It returns an error rather than failing the test, because the claim in
// this file is issued from a goroutine and only the test goroutine may
// call Fatal. apiCall is the wrapper for the calls made in line.
func apiRequest(addr, method, path, token string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode %s %s: %w", method, path, err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, "http://"+addr+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// The claim under test parks for as long as its own timeout allows, so
	// the client budget has to be the looser of the two or the test would
	// fail on the transport rather than on the behavior.
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s %s: %w", method, path, err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("%s %s = %d: %s", method, path, resp.StatusCode, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode %s %s: %w\n%s", method, path, err, raw)
	}
	return out, nil
}

func apiCall(t *testing.T, addr, method, path, token string, body any) map[string]any {
	t.Helper()
	out, err := apiRequest(addr, method, path, token, body)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func str(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok || v == "" {
		t.Fatalf("response carried no %s: %v", key, m)
	}
	return v
}

// TestClaimWaitsForMailThatArrivesAfterTheClaimOpens proves the binary
// wired the event bus into the lease service.
//
// The claim opens before any mail exists and asks for a long timeout. Mail
// is delivered afterwards. A claim that can park wakes on the delivery and
// returns the code; a claim that cannot park answers from the empty store
// and reports no binding straight away. The two outcomes are far enough
// apart that the assertion cannot pass by accident.
func TestClaimWaitsForMailThatArrivesAfterTheClaimOpens(t *testing.T) {
	dataDir := t.TempDir()
	bootstrap := []string{"--project", "demo", "--domain", "demo.test"}
	bearer := provisionBearer(t, dataDir, bootstrap...)
	srv := startBinary(t, dataDir, bootstrap...)
	if srv.apiAddr == "" {
		t.Fatal("the ready line carried no API address")
	}

	run := apiCall(t, srv.apiAddr, http.MethodPost, "/api/v1/runs", bearer, nil)
	runID, runToken := str(t, run, "run_id"), str(t, run, "run_token")

	lease := apiCall(t, srv.apiAddr, http.MethodPost,
		"/api/v1/runs/"+runID+"/leases", runToken, map[string]string{"role": "signup"})
	leaseID, addr := str(t, lease, "lease_id"), str(t, lease, "addr")

	// The claim runs on its own goroutine so the delivery can happen while
	// it is parked. Assertions stay on the test goroutine.
	const sentOTP = "481920"
	const deliverAfter = 500 * time.Millisecond
	type result struct {
		body map[string]any
		took time.Duration
		err  error
	}
	done := make(chan result, 1)
	go func() {
		started := time.Now()
		body, err := apiRequest(srv.apiAddr, http.MethodPost,
			"/api/v1/leases/"+leaseID+"/claims", runToken, map[string]any{
				"kind": "email_otp", "idempotency_key": "wiring-1",
				"timeout_ms": 30000,
			})
		done <- result{body: body, took: time.Since(started), err: err}
	}()

	time.Sleep(deliverAfter)
	deliver(t, srv.addr, "bounce@acme.example", addr,
		fmt.Sprintf("From: Acme <noreply@acme.example>\r\n"+
			"To: %s\r\n"+
			"Subject: Your verification code\r\n\r\n"+
			"Your verification code is %s. It expires in 10 minutes.\r\n", addr, sentOTP))

	var got result
	select {
	case got = <-done:
	case <-time.After(45 * time.Second):
		t.Fatal("the claim never returned")
	}
	if got.err != nil {
		t.Fatalf("the claim call failed: %v", got.err)
	}

	if reason, _ := got.body["reason"].(string); reason != "claim_ok" {
		t.Fatalf("reason = %q, want claim_ok: a claim that cannot park reports "+
			"no binding here, which is what an unwired bus looks like\n%v",
			reason, got.body)
	}
	if value, _ := got.body["value"].(string); value != sentOTP {
		t.Errorf("value = %q, want the OTP that was sent, %q", value, sentOTP)
	}
	if timedOut, _ := got.body["timed_out"].(bool); timedOut {
		t.Error("timed_out was set on a claim that received its mail")
	}
	// waited_ms is the field that separates a claim that parked from one
	// that answered immediately, so it is asserted rather than reported.
	waited, _ := got.body["waited_ms"].(float64)
	if waited <= 0 {
		t.Errorf("waited_ms = %v, want more than zero: the claim opened %s "+
			"before the mail was delivered, so it must have waited",
			waited, deliverAfter)
	}
	if got.took < deliverAfter {
		t.Errorf("the claim returned after %s, sooner than the %s delivery delay",
			got.took, deliverAfter)
	}
}
