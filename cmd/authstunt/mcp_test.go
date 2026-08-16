package main_test

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/store"
)

// mcpSession is the `authstunt mcp` subcommand running as a child
// process, driven the way a client drives it: newline delimited JSON-RPC
// on its stdin and stdout.
//
// Nothing is mocked. The child talks HTTP to a real server started by
// startBinary, mail arrives over a real SMTP conversation, and what this
// file asserts is the path an agent walks.
type mcpSession struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID int

	mu         sync.Mutex
	stderr     strings.Builder
	transcript strings.Builder
}

func startMCP(t *testing.T, apiAddr, bearer string) *mcpSession {
	t.Helper()
	// nolint:gosec // G204 flags the variable program. It is the binary
	// this test just built, run with this test's own literals.
	cmd := exec.Command(binary(t), "mcp")
	cmd.Env = append(cmd.Environ(),
		"AUTHSTUNT_URL=http://"+apiAddr,
		"AUTHSTUNT_BEARER="+bearer,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout: %v", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mcp: %v", err)
	}

	s := &mcpSession{t: t, cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderrPipe.Read(buf)
			if n > 0 {
				s.mu.Lock()
				s.stderr.Write(buf[:n])
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	t.Cleanup(func() {
		// Closing stdin is how the stdio transport says goodbye. The
		// server is expected to notice and exit on its own.
		_ = stdin.Close()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			t.Error("the mcp server did not exit when its input closed")
		}
	})
	return s
}

// call sends one request and returns the result, failing the test on a
// protocol error.
func (s *mcpSession) call(method string, params any) json.RawMessage {
	s.t.Helper()
	s.nextID++
	encoded, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": s.nextID, "method": method, "params": params,
	})
	if err != nil {
		s.t.Fatalf("encode: %v", err)
	}
	s.record(string(encoded))
	if _, err := s.stdin.Write(append(encoded, '\n')); err != nil {
		s.t.Fatalf("write: %v\nstderr: %s", err, s.stderrText())
	}
	line, err := s.stdout.ReadBytes('\n')
	if err != nil {
		s.t.Fatalf("read: %v\nstderr: %s", err, s.stderrText())
	}
	s.record(string(line))

	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		s.t.Fatalf("decode %q: %v", line, err)
	}
	if resp.Error != nil {
		s.t.Fatalf("%s: protocol error %d: %s", method, resp.Error.Code, resp.Error.Message)
	}
	return resp.Result
}

// claimed is the body of a claim result, decoded out of the text content
// the tool returned.
type claimed struct {
	Reason    string `json:"reason"`
	Value     string `json:"value"`
	MessageID string `json:"message_id"`
	TimedOut  bool   `json:"timed_out"`
}

// callTool runs a tool and returns the body it carried plus its isError
// flag.
func (s *mcpSession) callTool(name string, args map[string]any) (string, bool) {
	s.t.Helper()
	raw := s.call("tools/call", map[string]any{"name": name, "arguments": args})
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		s.t.Fatalf("%s: decode result: %v", name, err)
	}
	if len(result.Content) == 0 {
		return "", result.IsError
	}
	return result.Content[0].Text, result.IsError
}

func (s *mcpSession) record(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transcript.WriteString(line)
	s.transcript.WriteString("\n")
}

func (s *mcpSession) transcriptText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transcript.String()
}

func (s *mcpSession) stderrText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stderr.String()
}

// TestMCPWalksTheFrozenPath is the end-to-end proof for this surface: one
// real server, one real SMTP delivery, and the four tools driven over
// stdio in the order an agent uses them.
//
// It also rehearses the non-happy branch on purpose. A claim made before
// the mail exists has to come back as a result carrying claim_timeout,
// not as an error, because that is the difference between an agent that
// waits longer and an agent that starts guessing.
func TestMCPWalksTheFrozenPath(t *testing.T) {
	dataDir := t.TempDir()
	bearer := provisionBearer(t, dataDir, "--project", "mcp", "--domain", "mcp.test")
	srv := startBinary(t, dataDir, "--project", "mcp", "--domain", "mcp.test")
	defer srv.stop()

	s := startMCP(t, srv.apiAddr, bearer)

	// The handshake, then the tool list. A client that could not read
	// these would never reach a tool at all.
	initialize := s.call("initialize", map[string]any{"protocolVersion": "2025-11-25"})
	if !strings.Contains(string(initialize), `"tools"`) {
		t.Fatalf("initialize declared no tools capability: %s", initialize)
	}
	var listed struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(s.call("tools/list", map[string]any{}), &listed); err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	want := []string{"open_run", "lease_identity", "claim_code", "release_lease"}
	if len(listed.Tools) != len(want) {
		t.Fatalf("got %d tools, want %d", len(listed.Tools), len(want))
	}
	for i, name := range want {
		if listed.Tools[i].Name != name {
			t.Fatalf("tool %d = %q, want %q", i, listed.Tools[i].Name, name)
		}
	}

	// F1.
	opened, isErr := s.callTool("open_run", map[string]any{})
	if isErr {
		t.Fatalf("open_run: %s", opened)
	}
	var run struct {
		RunID    string `json:"run_id"`
		RunToken string `json:"run_token"`
	}
	if err := json.Unmarshal([]byte(opened), &run); err != nil {
		t.Fatalf("open_run body: %v", err)
	}
	if run.RunID == "" {
		t.Fatalf("open_run returned no run id: %s", opened)
	}
	if run.RunToken != "" {
		t.Fatalf("open_run handed the run token to the caller: %s", opened)
	}

	// F2.
	granted, isErr := s.callTool("lease_identity", map[string]any{
		"run_id": run.RunID, "role": "signup",
	})
	if isErr {
		t.Fatalf("lease_identity: %s", granted)
	}
	var lease struct {
		LeaseID string `json:"lease_id"`
		Addr    string `json:"addr"`
		Mode    string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(granted), &lease); err != nil {
		t.Fatalf("lease body: %v", err)
	}
	if lease.LeaseID == "" || !strings.HasSuffix(lease.Addr, "@mcp.test") {
		t.Fatalf("lease did not carry a usable identity: %s", granted)
	}
	if lease.Mode != store.ModeEphemeral {
		t.Fatalf("mode = %q, want ephemeral", lease.Mode)
	}

	// F3, before the application has sent anything. This is the branch
	// the whole design turns on: waiting out a deadline is a result, and
	// a result is what has to come back.
	//
	// The reason is claim_no_binding rather than claim_timeout, and the
	// difference is worth stating because it is easy to expect the other
	// one. claim_timeout is reserved for the case where a message did
	// bind to this lease and has not settled yet; nothing addressed to
	// the lease at all is a different fact, and the server says so. Both
	// are 200s, and the mapping of every one of them is covered in
	// internal/mcp.
	early, isErr := s.callTool("claim_code", map[string]any{
		"lease_id": lease.LeaseID, "kind": "email_otp", "timeout_ms": 1000,
	})
	if isErr {
		t.Fatalf("a claim that found nothing came back as an error: %s", early)
	}
	var waited claimed
	if err := json.Unmarshal([]byte(early), &waited); err != nil {
		t.Fatalf("claim body: %v", err)
	}
	if waited.Reason != store.ReasonNoBinding {
		t.Fatalf("reason = %q, want %s", waited.Reason, store.ReasonNoBinding)
	}
	if waited.Value != "" {
		t.Fatalf("a claim that found nothing carried a value: %s", early)
	}

	// The application sends. Real SMTP, to the address the lease handed
	// out, exactly as an application under test would.
	const sentOTP = "418902"
	body := "From: Demo <noreply@demo.example>\r\n" +
		"To: " + lease.Addr + "\r\n" +
		"Subject: Your verification code\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Your verification code is " + sentOTP + ". It expires in 10 minutes.\r\n"
	deliver(t, srv.addr, "bounce@demo.example", lease.Addr, body)

	// F3 again, same attempt and a longer wait - which is exactly what
	// the claim_code description tells a caller to do after a timeout.
	settled, isErr := s.callTool("claim_code", map[string]any{
		"lease_id": lease.LeaseID, "kind": "email_otp", "timeout_ms": 30000,
	})
	if isErr {
		t.Fatalf("claim_code: %s", settled)
	}
	var got claimed
	if err := json.Unmarshal([]byte(settled), &got); err != nil {
		t.Fatalf("claim body: %v", err)
	}
	if got.Reason != store.ReasonOK {
		t.Fatalf("reason = %q, want %s: %s", got.Reason, store.ReasonOK, settled)
	}
	if got.Value != sentOTP {
		t.Fatalf("value = %q, want the code that was sent, %q", got.Value, sentOTP)
	}
	if got.MessageID == "" {
		t.Fatalf("a claimed secret with no message to trace it to: %s", settled)
	}

	// Retrying the same attempt replays the same secret rather than
	// demanding a second message. This is the property the derived
	// idempotency key exists for.
	replayed, isErr := s.callTool("claim_code", map[string]any{
		"lease_id": lease.LeaseID, "kind": "email_otp", "timeout_ms": 1000,
	})
	if isErr {
		t.Fatalf("replay: %s", replayed)
	}
	var again claimed
	if err := json.Unmarshal([]byte(replayed), &again); err != nil {
		t.Fatalf("replay body: %v", err)
	}
	if again.Reason != store.ReasonOK || again.Value != sentOTP {
		t.Fatalf("a retry did not replay the first answer: %s", replayed)
	}

	// A different attempt is a different ask: no second message was ever
	// sent, so it waits and reports that nothing arrived.
	fresh, isErr := s.callTool("claim_code", map[string]any{
		"lease_id": lease.LeaseID, "kind": "email_otp", "timeout_ms": 1000, "attempt": 2,
	})
	if isErr {
		t.Fatalf("attempt 2: %s", fresh)
	}
	var second claimed
	if err := json.Unmarshal([]byte(fresh), &second); err != nil {
		t.Fatalf("attempt 2 body: %v", err)
	}
	if second.Value == sentOTP {
		t.Fatalf("raising attempt replayed the first message: %s", fresh)
	}

	// F4. A 204 carries nothing, and releasing twice is not an error.
	for i := range 2 {
		released, isErr := s.callTool("release_lease", map[string]any{"lease_id": lease.LeaseID})
		if isErr {
			t.Fatalf("release %d: %s", i+1, released)
		}
		if released != "" {
			t.Fatalf("release %d returned a body: %q", i+1, released)
		}
	}

	// The mechanical check the P1 gate is measured by. Every byte that
	// crossed the transport, in both directions, and no long-lived
	// credential in any of it.
	transcript := s.transcriptText()
	for _, prefix := range []string{store.ProjectBearerPrefix, store.RunTokenPrefix} {
		if strings.Contains(transcript, prefix) {
			t.Errorf("a %s credential reached the transcript", prefix)
		}
	}
	// The claimed value did cross, and that is the point rather than a
	// leak: it is a one-time secret the agent has to type in.
	if !strings.Contains(transcript, sentOTP) {
		t.Error("the claimed code never reached the caller, so nothing could have used it")
	}
	// stderr is where the logs go, and a credential must not be there
	// either.
	stderr := s.stderrText()
	for _, prefix := range []string{store.ProjectBearerPrefix, store.RunTokenPrefix} {
		if strings.Contains(stderr, prefix) {
			t.Errorf("a %s credential reached the log", prefix)
		}
	}
}

// TestMCPRefusesToStartWithoutABearer keeps the failure at startup, where
// a human is still watching, rather than at the first tool call, where an
// agent would improvise around it.
func TestMCPRefusesToStartWithoutABearer(t *testing.T) {
	// nolint:gosec // G204: the binary this test built, with its own args.
	cmd := exec.Command(binary(t), "mcp")
	cmd.Env = append(cmd.Environ(), "AUTHSTUNT_BEARER=", "AUTHSTUNT_URL=http://127.0.0.1:1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("mcp started with no bearer")
	}
	if !strings.Contains(string(out), "AUTHSTUNT_BEARER") {
		t.Fatalf("the refusal has to name the variable: %s", out)
	}
}
