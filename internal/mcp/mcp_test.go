package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/store"
)

// fakeServer stands in for a running AuthStunt server.
//
// It records what it was sent, so the tests can assert on the request the
// proxy built rather than only on the answer it returned. Nothing here
// reimplements lease semantics: the point of these tests is the mapping
// between a tool call and an HTTP call, and the real semantics get their
// own end-to-end test against the real binary.
type fakeServer struct {
	*httptest.Server

	mu       sync.Mutex
	authSeen map[string]string
	bodySeen map[string]json.RawMessage

	claimStatus int
	claimBody   string
	leaseStatus int
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{
		authSeen:    map[string]string{},
		bodySeen:    map[string]json.RawMessage{},
		claimStatus: http.StatusOK,
		claimBody:   `{"reason":"claim_ok","claim_id":"cl_1","message_id":"ms_1","value":"418902","timed_out":false,"waited_ms":12}`,
		leaseStatus: http.StatusCreated,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		f.record(t, "open_run", r)
		write(w, http.StatusCreated, `{"run_id":"rn_1","run_token":"rt_secret","checkpoint_at":"2026-08-16T00:00:00Z","expires_at":"2026-08-16T01:00:00Z"}`)
	})
	mux.HandleFunc("POST /api/v1/runs/{run_id}/leases", func(w http.ResponseWriter, r *http.Request) {
		f.record(t, "lease", r)
		if f.leaseStatus != http.StatusCreated {
			write(w, f.leaseStatus, `{"error":{"code":"lease_seed_failed","message":"the identity could not be prepared for use"}}`)
			return
		}
		write(w, http.StatusCreated, `{"lease_id":"ls_1","identity_id":"id_1","addr":"a@demo.test","role":"signup","mode":"ephemeral","seed_state":"none","pooled_policy":null,"expires_at":"2026-08-16T00:30:00Z"}`)
	})
	mux.HandleFunc("POST /api/v1/leases/{lease_id}/claims", func(w http.ResponseWriter, r *http.Request) {
		f.record(t, "claim", r)
		write(w, f.claimStatus, f.claimBody)
	})
	mux.HandleFunc("DELETE /api/v1/leases/{lease_id}", func(w http.ResponseWriter, r *http.Request) {
		f.record(t, "release", r)
		w.WriteHeader(http.StatusNoContent)
	})

	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) record(t *testing.T, name string, r *http.Request) {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read %s body: %v", name, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authSeen[name] = r.Header.Get("Authorization")
	f.bodySeen[name] = body
}

func (f *fakeServer) auth(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authSeen[name]
}

func (f *fakeServer) body(name string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var decoded map[string]any
	if len(f.bodySeen[name]) > 0 {
		_ = json.Unmarshal(f.bodySeen[name], &decoded)
	}
	return decoded
}

func write(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// rpcResponse is a response as a test reads it back, with the result left
// as raw JSON so an assertion can look at the exact bytes.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	} `json:"error"`
}

// callResult is the tools/call result shape.
type callResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError    bool   `json:"isError"`
	ResultType string `json:"resultType"`
}

// session drives a Server over a pipe, the way a client drives it over
// stdio.
type session struct {
	t      *testing.T
	stdin  *io.PipeWriter
	out    *bufio.Reader
	nextID int
	done   chan error
}

func newSession(t *testing.T, baseURL string) *session {
	t.Helper()
	server, err := New(Config{BaseURL: baseURL, Bearer: "pb_test", Version: "test"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{t: t, stdin: inW, out: bufio.NewReader(outR), done: make(chan error, 1)}
	go func() { s.done <- server.Serve(context.Background(), inR, outW) }()
	t.Cleanup(func() {
		_ = inW.Close()
		<-s.done
		_ = outW.Close()
	})
	return s
}

func (s *session) call(method string, params any) rpcResponse {
	s.t.Helper()
	s.nextID++
	encoded, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      s.nextID,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		s.t.Fatalf("encode request: %v", err)
	}
	if _, err := s.stdin.Write(append(encoded, '\n')); err != nil {
		s.t.Fatalf("write request: %v", err)
	}
	line, err := s.out.ReadBytes('\n')
	if err != nil {
		s.t.Fatalf("read response: %v", err)
	}
	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		s.t.Fatalf("decode response %q: %v", line, err)
	}
	return resp
}

// callTool is the shorthand for a tools/call that is expected to produce
// a result rather than a protocol error.
func (s *session) callTool(name string, args map[string]any) callResult {
	s.t.Helper()
	resp := s.call("tools/call", map[string]any{"name": name, "arguments": args})
	if resp.Error != nil {
		s.t.Fatalf("%s: unexpected protocol error %d: %s", name, resp.Error.Code, resp.Error.Message)
	}
	var result callResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		s.t.Fatalf("%s: decode result: %v", name, err)
	}
	return result
}

func (r callResult) text() string {
	if len(r.Content) == 0 {
		return ""
	}
	return r.Content[0].Text
}

// openAndLease walks the first two tools so a test that is about claiming
// starts from a lease this server knows about.
func openAndLease(t *testing.T, s *session) {
	t.Helper()
	if got := s.callTool(toolOpenRun, map[string]any{}); got.IsError {
		t.Fatalf("open_run: %s", got.text())
	}
	if got := s.callTool(toolLeaseIdentity, map[string]any{"run_id": "rn_1", "role": "signup"}); got.IsError {
		t.Fatalf("lease_identity: %s", got.text())
	}
}

func TestIdempotencyKeyDerivation(t *testing.T) {
	// The formula is a contract with the AuthStunt server, not an
	// implementation detail: a retry finds the earlier claim only if it
	// rebuilds the same string, byte for byte.
	if got, want := idempotencyKey("ls_1", "email_otp", 1), "mcp:ls_1:email_otp:1"; got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
	if got, want := idempotencyKey("ls_1", "magic_link", 3), "mcp:ls_1:magic_link:3"; got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
	// The three inputs each have to move the key. A key that ignored one
	// of them would silently merge two different asks.
	seen := map[string]bool{}
	for _, key := range []string{
		idempotencyKey("ls_1", "email_otp", 1),
		idempotencyKey("ls_2", "email_otp", 1),
		idempotencyKey("ls_1", "magic_link", 1),
		idempotencyKey("ls_1", "email_otp", 2),
	} {
		if seen[key] {
			t.Fatalf("two different asks derive the same key %q", key)
		}
		seen[key] = true
	}
}

func TestClaimDerivesTheKeyAndDefaultsTheTimeout(t *testing.T) {
	fake := newFakeServer(t)
	s := newSession(t, fake.URL)
	openAndLease(t, s)

	s.callTool(toolClaimCode, map[string]any{"lease_id": "ls_1", "kind": "email_otp"})

	body := fake.body("claim")
	if got, want := body["idempotency_key"], "mcp:ls_1:email_otp:1"; got != want {
		t.Fatalf("idempotency_key = %v, want %v", got, want)
	}
	// Defaulting to 0 - which is what the frozen route does with a
	// missing field - would hand back claim_no_binding instantly and
	// teach an agent to poll.
	if got, want := body["timeout_ms"], float64(defaultClaimTimeoutMS); got != want {
		t.Fatalf("timeout_ms = %v, want %v", got, want)
	}
	if _, present := body["attempt"]; present {
		t.Fatal("attempt is a tool parameter, not a field of the frozen request")
	}
}

func TestAttemptAsksForANewMessage(t *testing.T) {
	fake := newFakeServer(t)
	s := newSession(t, fake.URL)
	openAndLease(t, s)

	s.callTool(toolClaimCode, map[string]any{"lease_id": "ls_1", "kind": "email_otp", "attempt": 2})

	if got, want := fake.body("claim")["idempotency_key"], "mcp:ls_1:email_otp:2"; got != want {
		t.Fatalf("idempotency_key = %v, want %v", got, want)
	}
}

// TestTimeoutIsNotAnError is the negative control for the whole mapping.
//
// A claim that waited out its deadline is a 200 saying nothing went
// wrong. If it arrived as isError, a client would flatten it into an
// error string and the model would retry blindly instead of calling again
// with a longer timeout, which is the exact guessing loop this surface
// exists to remove.
func TestTimeoutIsNotAnError(t *testing.T) {
	fake := newFakeServer(t)
	fake.claimBody = `{"reason":"claim_timeout","timed_out":true,"waited_ms":30000}`
	s := newSession(t, fake.URL)
	openAndLease(t, s)

	got := s.callTool(toolClaimCode, map[string]any{"lease_id": "ls_1", "kind": "email_otp", "timeout_ms": 30000})

	if got.IsError {
		t.Fatal("claim_timeout came back as isError; a 2xx is a result, never an error")
	}
	if got.text() != fake.claimBody {
		t.Fatalf("body = %q, want it verbatim: %q", got.text(), fake.claimBody)
	}
}

// TestEveryTwoHundredIsAResult widens the control to every reason code a
// 200 can carry, so a future special case for one of them fails here.
func TestEveryTwoHundredIsAResult(t *testing.T) {
	for _, row := range claimReasons {
		t.Run(row.code, func(t *testing.T) {
			fake := newFakeServer(t)
			fake.claimBody = `{"reason":"` + row.code + `","timed_out":false,"waited_ms":1}`
			s := newSession(t, fake.URL)
			openAndLease(t, s)

			got := s.callTool(toolClaimCode, map[string]any{"lease_id": "ls_1", "kind": "email_otp"})
			if got.IsError {
				t.Fatalf("%s arrived as isError", row.code)
			}
			if got.text() != fake.claimBody {
				t.Fatalf("body = %q, want %q", got.text(), fake.claimBody)
			}
		})
	}
}

// TestErrorEnvelopePassesThroughVerbatim covers the other half of the
// mapping: a refusal keeps the server's code and message, untranslated.
func TestErrorEnvelopePassesThroughVerbatim(t *testing.T) {
	fake := newFakeServer(t)
	fake.leaseStatus = http.StatusConflict
	s := newSession(t, fake.URL)
	if got := s.callTool(toolOpenRun, map[string]any{}); got.IsError {
		t.Fatalf("open_run: %s", got.text())
	}

	got := s.callTool(toolLeaseIdentity, map[string]any{"run_id": "rn_1", "role": "signup"})

	if !got.IsError {
		t.Fatal("a 409 has to be isError")
	}
	const want = `{"error":{"code":"lease_seed_failed","message":"the identity could not be prepared for use"}}`
	if got.text() != want {
		t.Fatalf("body = %q, want %q", got.text(), want)
	}
}

// TestRefusedKindIsNotTranslated keeps the totp stub honest. The schema
// is what stops a model picking totp; anything that gets past it is the
// server's refusal to state, not this package's.
func TestRefusedKindIsNotTranslated(t *testing.T) {
	fake := newFakeServer(t)
	fake.claimStatus = http.StatusNotImplemented
	fake.claimBody = `{"error":{"code":"bad_request","message":"totp is not implemented"}}`
	s := newSession(t, fake.URL)
	openAndLease(t, s)

	got := s.callTool(toolClaimCode, map[string]any{"lease_id": "ls_1", "kind": "totp"})

	if !got.IsError {
		t.Fatal("a 501 has to be isError")
	}
	if got.text() != fake.claimBody {
		t.Fatalf("body = %q, want %q", got.text(), fake.claimBody)
	}
}

// TestRunTokenNeverLeavesTheProcess is the mechanical form of the promise
// the README makes.
func TestRunTokenNeverLeavesTheProcess(t *testing.T) {
	fake := newFakeServer(t)
	s := newSession(t, fake.URL)

	opened := s.callTool(toolOpenRun, map[string]any{})
	if strings.Contains(opened.text(), "run_token") || strings.Contains(opened.text(), "rt_") {
		t.Fatalf("open_run result carries the run token: %s", opened.text())
	}
	// The rest of the body is untouched: the exception is one field, not
	// a license to reshape the response.
	for _, field := range []string{"run_id", "checkpoint_at", "expires_at"} {
		if !strings.Contains(opened.text(), field) {
			t.Fatalf("open_run result lost %s: %s", field, opened.text())
		}
	}

	s.callTool(toolLeaseIdentity, map[string]any{"run_id": "rn_1", "role": "signup"})
	s.callTool(toolClaimCode, map[string]any{"lease_id": "ls_1", "kind": "email_otp"})
	s.callTool(toolReleaseLease, map[string]any{"lease_id": "ls_1"})

	// The bearer authorizes exactly one route. Everything after it rides
	// on the run token, which is the scoping the HTTP surface already
	// built and this server declines to throw away.
	if got, want := fake.auth("open_run"), "Bearer pb_test"; got != want {
		t.Fatalf("open_run auth = %q, want %q", got, want)
	}
	for _, name := range []string{"lease", "claim", "release"} {
		if got, want := fake.auth(name), "Bearer rt_secret"; got != want {
			t.Fatalf("%s auth = %q, want %q", name, got, want)
		}
	}
}

func TestLeaseAlwaysAsksForEphemeral(t *testing.T) {
	fake := newFakeServer(t)
	s := newSession(t, fake.URL)
	openAndLease(t, s)

	// Pooled is outside the freeze. Sending the mode explicitly means a
	// later change to the server's default cannot quietly move this tool
	// into a mode it was never allowed to reach.
	if got, want := fake.body("lease")["mode"], "ephemeral"; got != want {
		t.Fatalf("mode = %v, want %v", got, want)
	}
}

func TestReleaseIsEmptyAndSucceeds(t *testing.T) {
	fake := newFakeServer(t)
	s := newSession(t, fake.URL)
	openAndLease(t, s)

	got := s.callTool(toolReleaseLease, map[string]any{"lease_id": "ls_1"})

	if got.IsError {
		t.Fatalf("a 204 is success, got isError with %q", got.text())
	}
	if len(got.Content) != 0 {
		t.Fatalf("a 204 has no body, got %v", got.Content)
	}
}

// TestUnknownIdsAreNotFound covers the state this process keeps. A client
// that restarted the server leaves the model holding ids no credential
// answers for, and the tool says so in the server's own vocabulary.
func TestUnknownIdsAreNotFound(t *testing.T) {
	fake := newFakeServer(t)
	s := newSession(t, fake.URL)

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{toolLeaseIdentity, map[string]any{"run_id": "rn_missing", "role": "signup"}},
		{toolClaimCode, map[string]any{"lease_id": "ls_missing", "kind": "email_otp"}},
		{toolReleaseLease, map[string]any{"lease_id": "ls_missing"}},
	} {
		got := s.callTool(tc.tool, tc.args)
		if !got.IsError {
			t.Fatalf("%s: an unknown id has to be an error", tc.tool)
		}
		if !strings.Contains(got.text(), `"code":"not_found"`) {
			t.Fatalf("%s: want the server's not_found code, got %s", tc.tool, got.text())
		}
	}
}

func TestToolListIsTheFourFrozenTools(t *testing.T) {
	fake := newFakeServer(t)
	s := newSession(t, fake.URL)

	resp := s.call("tools/list", map[string]any{})
	var result struct {
		Tools []toolDef `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := []string{toolOpenRun, toolLeaseIdentity, toolClaimCode, toolReleaseLease}
	if len(result.Tools) != len(want) {
		t.Fatalf("got %d tools, want %d", len(result.Tools), len(want))
	}
	for i, name := range want {
		if result.Tools[i].Name != name {
			t.Fatalf("tool %d = %q, want %q", i, result.Tools[i].Name, name)
		}
		if result.Tools[i].Description == "" {
			t.Fatalf("%s has no description", name)
		}
		if !json.Valid(result.Tools[i].InputSchema) {
			t.Fatalf("%s has an invalid input schema", name)
		}
	}
}

// TestClaimSchemaRefusesTOTPByEnum is the shape of the refusal, not the
// wording of it: a value absent from the enum is one a model cannot pick,
// which is a stronger guarantee than a sentence asking it not to.
func TestClaimSchemaRefusesTOTPByEnum(t *testing.T) {
	for _, tool := range tools() {
		if tool.Name != toolClaimCode {
			continue
		}
		var schema struct {
			Properties struct {
				Kind struct {
					Enum []string `json:"enum"`
				} `json:"kind"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("decode schema: %v", err)
		}
		got := strings.Join(schema.Properties.Kind.Enum, ",")
		if want := store.ClaimEmailOTP + "," + store.ClaimMagicLink; got != want {
			t.Fatalf("kind enum = %q, want %q", got, want)
		}
		return
	}
	t.Fatal("claim_code is missing from the tool list")
}

// TestDescriptionsCoverEveryReasonCode is the cross-check the descriptions
// have to survive: a reason code the server can emit but no description
// explains is a code that reaches a model as an unexplained string, and
// an unexplained string is where guessing comes back.
func TestDescriptionsCoverEveryReasonCode(t *testing.T) {
	// Every claim reason code the store defines. The list is spelled out
	// rather than derived so that adding a constant to the store shows up
	// here as a deliberate edit.
	all := []string{
		store.ReasonOK, store.ReasonTimeout, store.ReasonNoBinding,
		store.ReasonStaleFiltered, store.ReasonSuspectBinding,
		store.ReasonAlreadyClaimed, store.ReasonExpired,
		store.ReasonLeaseNotHeld, store.ReasonLeaseSeedFailed,
		store.ReasonRunNotActive, store.ReasonExtractionFail,
	}

	var descriptions strings.Builder
	for _, tool := range tools() {
		descriptions.WriteString(tool.Description)
		descriptions.WriteString("\n")
	}
	text := descriptions.String()

	for _, code := range all {
		if !strings.Contains(text, code) {
			t.Errorf("no tool description explains %s", code)
		}
	}
	// And the other direction: a row naming a code the server cannot
	// produce would be an instruction for a branch that never happens.
	for _, row := range claimReasons {
		found := false
		for _, code := range all {
			if row.code == code {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("claim_code explains %s, which package store does not define", row.code)
		}
	}
}

func TestLegacyInitializeNegotiates(t *testing.T) {
	fake := newFakeServer(t)
	s := newSession(t, fake.URL)

	for _, tc := range []struct{ asked, want string }{
		{"2025-11-25", "2025-11-25"},
		{"2025-06-18", "2025-06-18"},
		// A version this server does not implement is answered with one
		// it does, which is what the legacy revisions require.
		{"2024-11-05", latestLegacyVersion},
	} {
		resp := s.call("initialize", map[string]any{"protocolVersion": tc.asked})
		var result struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if result.ProtocolVersion != tc.want {
			t.Fatalf("asked %s, negotiated %s, want %s", tc.asked, result.ProtocolVersion, tc.want)
		}
		if _, ok := result.Capabilities["tools"]; !ok {
			t.Fatal("tools capability missing")
		}
		// No prompts and no resources: a mailbox exposed as a readable
		// resource is an invitation to read mail and pick a code by eye.
		for _, forbidden := range []string{"prompts", "resources"} {
			if _, ok := result.Capabilities[forbidden]; ok {
				t.Fatalf("%s must not be declared", forbidden)
			}
		}
		if result.ServerInfo.Name != serverName {
			t.Fatalf("serverInfo.name = %q", result.ServerInfo.Name)
		}
	}
}

func TestModernDiscoverAndVersionRefusal(t *testing.T) {
	fake := newFakeServer(t)
	s := newSession(t, fake.URL)

	resp := s.call("server/discover", map[string]any{
		"_meta": map[string]any{metaProtocolVersion: supportedVersions[0]},
	})
	var discover struct {
		ResultType        string   `json:"resultType"`
		SupportedVersions []string `json:"supportedVersions"`
	}
	if err := json.Unmarshal(resp.Result, &discover); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if discover.ResultType != "complete" {
		t.Fatalf("resultType = %q", discover.ResultType)
	}
	if strings.Join(discover.SupportedVersions, ",") != strings.Join(supportedVersions, ",") {
		t.Fatalf("supportedVersions = %v", discover.SupportedVersions)
	}

	// A version this server does not implement is refused with the list
	// it does, which is the only way a modern client can recover.
	refused := s.call("tools/list", map[string]any{
		"_meta": map[string]any{metaProtocolVersion: "1900-01-01"},
	})
	if refused.Error == nil || refused.Error.Code != codeUnsupportedProtocolVersion {
		t.Fatalf("want %d, got %+v", codeUnsupportedProtocolVersion, refused.Error)
	}
	if !strings.Contains(string(refused.Error.Data), supportedVersions[0]) {
		t.Fatalf("the refusal has to list what is supported, got %s", refused.Error.Data)
	}
}

// TestResultTypeOnlyOnTheModernPath keeps the two eras apart. A legacy
// revision never defined resultType, and adding fields a client's schema
// does not know is how an implementation drifts into being nobody's.
func TestResultTypeOnlyOnTheModernPath(t *testing.T) {
	fake := newFakeServer(t)
	s := newSession(t, fake.URL)
	openAndLease(t, s)

	legacy := s.callTool(toolReleaseLease, map[string]any{"lease_id": "ls_1"})
	if legacy.ResultType != "" {
		t.Fatalf("legacy result carries resultType %q", legacy.ResultType)
	}

	resp := s.call("tools/call", map[string]any{
		"_meta":     map[string]any{metaProtocolVersion: supportedVersions[0]},
		"name":      toolReleaseLease,
		"arguments": map[string]any{"lease_id": "ls_1"},
	})
	var modern callResult
	if err := json.Unmarshal(resp.Result, &modern); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if modern.ResultType != "complete" {
		t.Fatalf("modern result lacks resultType, got %q", modern.ResultType)
	}
}

func TestMalformedCallsAreProtocolErrors(t *testing.T) {
	fake := newFakeServer(t)
	s := newSession(t, fake.URL)

	for _, tc := range []struct {
		name string
		args map[string]any
		tool string
	}{
		{"unknown tool", map[string]any{}, "verify_signup"},
		{"missing lease", map[string]any{"kind": "email_otp"}, toolClaimCode},
		{"attempt below one", map[string]any{"lease_id": "ls_1", "kind": "email_otp", "attempt": 0}, toolClaimCode},
		{"timeout over the cap", map[string]any{"lease_id": "ls_1", "kind": "email_otp", "timeout_ms": 120001}, toolClaimCode},
		{"invented field", map[string]any{"lease_id": "ls_1", "kind": "email_otp", "idempotency_key": "mine"}, toolClaimCode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := s.call("tools/call", map[string]any{"name": tc.tool, "arguments": tc.args})
			if resp.Error == nil {
				t.Fatalf("want a protocol error, got result %s", resp.Result)
			}
			if resp.Error.Code != codeInvalidParams {
				t.Fatalf("code = %d, want %d", resp.Error.Code, codeInvalidParams)
			}
		})
	}
}

func TestNotificationsAreAnsweredWithSilence(t *testing.T) {
	fake := newFakeServer(t)
	s := newSession(t, fake.URL)

	// A notification carries no id, so any answer would be an unmatched
	// message on the wire. The ping after it is what proves the server
	// wrote nothing in between rather than that it hung.
	if _, err := s.stdin.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp := s.call("ping", map[string]any{})
	if resp.Error != nil {
		t.Fatalf("ping: %+v", resp.Error)
	}
	if string(resp.ID) != "1" {
		t.Fatalf("the ping answered id %s; something else was written first", resp.ID)
	}
}

func TestUnknownMethodIsMethodNotFound(t *testing.T) {
	fake := newFakeServer(t)
	s := newSession(t, fake.URL)

	resp := s.call("resources/list", map[string]any{})
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Fatalf("want %d, got %+v", codeMethodNotFound, resp.Error)
	}
}

func TestNewRefusesAMissingCredential(t *testing.T) {
	// Failing at startup rather than on the first tool call is the point:
	// an agent that has already told a user it is signing up improvises
	// around an authentication error, and what it would improvise around
	// is a configuration mistake only a human can fix.
	if _, err := New(Config{BaseURL: "http://127.0.0.1:8925"}); err == nil {
		t.Fatal("a missing bearer has to be a startup error")
	} else if !strings.Contains(err.Error(), "AUTHSTUNT_BEARER") {
		t.Fatalf("the error has to name the variable, got %q", err)
	}
	if _, err := New(Config{Bearer: "pb_x"}); err == nil {
		t.Fatal("a missing URL has to be a startup error")
	}
	// And the value is never echoed, however it was wrong.
	_, err := New(Config{BaseURL: "http://127.0.0.1:8925", Bearer: "   "})
	if err == nil || strings.Contains(err.Error(), "   ") {
		t.Fatalf("the error must not carry the value, got %v", err)
	}
}

// TestServeStopsOnCancellation covers the shutdown a signal produces.
//
// A read from stdin does not unblock when a context is canceled, so
// without a reader goroutine the process would sit there holding a port
// nobody is talking to until something killed it.
func TestServeStopsOnCancellation(t *testing.T) {
	fake := newFakeServer(t)
	server, err := New(Config{BaseURL: fake.URL, Bearer: "pb_test", Version: "test"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	// A pipe nobody writes to and nobody closes: the only way out is the
	// cancellation.
	inR, inW := io.Pipe()
	defer func() { _ = inW.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, inR, io.Discard) }()
	cancel()

	select {
	case err := <-done:
		// A signal is an ordinary end to a session, not a failure.
		if err != nil {
			t.Fatalf("Serve reported %v on a canceled context", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return when its context was canceled")
	}
}
