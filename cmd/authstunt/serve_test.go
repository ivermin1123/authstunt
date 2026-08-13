package main_test

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	netsmtp "net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/extract"
	"github.com/ivermin1123/authstunt/internal/secrets"
	"github.com/ivermin1123/authstunt/internal/store"
)

// buildOnce compiles the binary the tests drive. Every test in this file
// runs the real program, not a library wired up to look like one, because
// the thing being proven is that the shipped binary does this.
var buildOnce struct {
	sync.Once
	path string
	err  error
}

func binary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "authstunt-build")
		if err != nil {
			buildOnce.err = err
			return
		}
		path := filepath.Join(dir, "authstunt")
		if runtimeIsWindows() {
			path += ".exe"
		}
		// nolint:gosec // G204 flags the variable output path. It is a
		// temporary directory this test just created, and building the
		// binary under test is the point of the whole file.
		cmd := exec.Command("go", "build", "-o", path, ".")
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildOnce.err = err
			buildOnce.path = string(out)
			return
		}
		buildOnce.path = path
	})
	if buildOnce.err != nil {
		t.Fatalf("build: %v\n%s", buildOnce.err, buildOnce.path)
	}
	return buildOnce.path
}

func runtimeIsWindows() bool { return os.PathSeparator == '\\' }

// running is a started server process and the address it announced.
type running struct {
	addr string
	// apiAddr is the host:port the HTTP API bound, parsed out of the ready
	// line so a test can drive the same surface a client would.
	apiAddr string
	dataDir string
	stop    func()
	// logs returns everything the process wrote to stderr so far, which is
	// where the structured log and every startup diagnostic go. A
	// credential must never appear in it.
	logs func() string
	// stdout returns the process's stdout, which carries the
	// machine-readable ready line and must carry nothing else.
	stdout func() string
}

// provisionBearer runs the explicit provisioning command and returns the
// credential it printed.
//
// Every test that drives the API surface needs one, and serve refuses to
// start the API without one, so this is the setup step that mirrors what
// an operator does once by hand. The opt-in flag is required here because
// a test's stdout is a pipe: that refusal is the behavior under test in
// bearer_test.go, and this helper is the deliberate override of it.
func provisionBearer(t *testing.T, dataDir string, bootstrapArgs ...string) string {
	t.Helper()
	args := append([]string{"project", "bearer", "provision",
		"--data-dir", dataDir, "--allow-non-tty-reveal"}, bootstrapArgs...)
	// nolint:gosec // G204 flags the variable program and arguments. Both
	// come from this test: the program is the binary it just built, and
	// the arguments are its own literals.
	cmd := exec.Command(binary(t), args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("provision the bearer: %v\n%s", err, stderr.String())
	}
	token := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(token, store.ProjectBearerPrefix) {
		t.Fatalf("the provision command printed no credential: %q", stdout.String())
	}
	return token
}

// provisionOnce provisions the bearer unless the directory already has
// one, which is the case every test that restarts a server on the same
// data directory hits.
func provisionOnce(t *testing.T, dataDir string, bootstrapArgs []string) {
	t.Helper()
	args := append([]string{"project", "bearer", "provision",
		"--data-dir", dataDir, "--allow-non-tty-reveal"}, bootstrapArgs...)
	// nolint:gosec // G204, same as above: the binary under test, run with
	// this test's own arguments.
	out, err := exec.Command(binary(t), args...).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "already has a bearer") {
		t.Fatalf("provision the bearer: %v\n%s", err, out)
	}
}

// bootstrapArgs picks the flags the provisioning command shares with
// serve out of a serve argument list, so a caller passes its flags once.
func bootstrapArgs(args []string) []string {
	var out []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--project" || args[i] == "--domain" {
			out = append(out, args[i], args[i+1])
		}
	}
	return out
}

// startBinary launches serve and waits for the line it prints once the
// port is accepting, which is what makes the test deterministic without a
// sleep.
//
// It provisions the project bearer first, because serve no longer mints
// one: the API refuses to bind for a project that has no credential.
func startBinary(t *testing.T, dataDir string, args ...string) *running {
	t.Helper()
	provisionOnce(t, dataDir, bootstrapArgs(args))
	ctx, cancel := context.WithCancel(context.Background())
	// Port 0 on both listeners: these tests run in parallel and alongside
	// whatever the developer already has bound, so a fixed API port would
	// make them fail for a reason that has nothing to do with the code.
	full := append([]string{"serve", "--data-dir", dataDir,
		"--smtp-listen", "127.0.0.1:0", "--api-listen", "127.0.0.1:0"}, args...)
	// nolint:gosec // G204 flags the variable program and arguments. Both
	// come from this test: the program is the binary it just built, and
	// the arguments are its own literals.
	cmd := exec.CommandContext(ctx, binary(t), full...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatalf("stdout: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start: %v", err)
	}

	stopped := make(chan struct{})
	stop := func() {
		cancel()
		_ = cmd.Wait()
		close(stopped)
	}
	t.Cleanup(func() {
		select {
		case <-stopped:
		default:
			stop()
		}
	})

	// Everything stdout carries is kept, not discarded: a test has to be
	// able to assert that the ready line is the whole of it and that no
	// credential ever joined it.
	var mu sync.Mutex
	var out strings.Builder
	line := make(chan string, 1)
	go func() {
		r := bufio.NewReader(stdout)
		text, err := r.ReadString('\n')
		if err != nil {
			return
		}
		mu.Lock()
		out.WriteString(text)
		mu.Unlock()
		line <- strings.TrimSpace(text)
		// Keep reading so the process never blocks on a full pipe.
		for {
			chunk, err := r.ReadString('\n')
			mu.Lock()
			out.WriteString(chunk)
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	readStdout := func() string {
		mu.Lock()
		defer mu.Unlock()
		return out.String()
	}

	select {
	case text := <-line:
		_, addr, ok := strings.Cut(text, "smtp ")
		if !ok {
			t.Fatalf("startup line did not carry an address: %q", text)
		}
		// The API address sits between its own label and the SMTP one. It
		// is read here rather than assumed, because these tests bind port
		// 0 and only the ready line knows what the kernel handed out.
		var apiAddr string
		if _, rest, found := strings.Cut(text, "api "); found {
			apiAddr, _, _ = strings.Cut(rest, ",")
		}
		return &running{
			addr: addr, apiAddr: apiAddr, dataDir: dataDir, stop: stop,
			logs: stderr.String, stdout: readStdout,
		}
	case <-time.After(30 * time.Second):
		stop()
		t.Fatalf("the server never announced a port\nstderr: %s", stderr.String())
		return nil
	}
}

// readBack opens the data directory as a second process and returns the
// stored messages.
//
// This is the phase's read path on purpose. Phase 1 creates no HTTP or CLI
// read contract, so the assertion goes through the store the same way a
// second tool would: WAL supports concurrent readers, and the store was
// already written to expect two processes over one directory.
func readBack(t *testing.T, dataDir string) []store.Message {
	t.Helper()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dataDir, "keys"), "project")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	st, err := store.Open(t.Context(), dataDir, key, store.Options{Logger: logger})
	if err != nil {
		t.Fatalf("open store as a second reader: %v", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	msgs, err := st.ListMessages(t.Context(), store.MessageFilter{IncludeQuarantined: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return msgs
}

// awaitExtraction polls the data directory until one message has settled.
func awaitExtraction(t *testing.T, dataDir string) store.Message {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		msgs := readBack(t, dataDir)
		if len(msgs) == 1 && msgs[0].ExtractionState != store.ExtractionPending {
			return msgs[0]
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("no message settled in the data directory")
	return store.Message{}
}

func deliver(t *testing.T, addr, from, to, body string) {
	t.Helper()
	c, err := netsmtp.Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Mail(from); err != nil {
		t.Fatalf("mail: %v", err)
	}
	if err := c.Rcpt(to); err != nil {
		t.Fatalf("rcpt: %v", err)
	}
	w, err := c.Data()
	if err != nil {
		t.Fatalf("data: %v", err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := c.Quit(); err != nil {
		t.Fatalf("quit: %v", err)
	}
}

// TestVerticalSMTPToOTP is the phase's reason to exist: a real binary, a
// real SMTP conversation, and the code that came out the other end.
func TestVerticalSMTPToOTP(t *testing.T) {
	dataDir := t.TempDir()
	srv := startBinary(t, dataDir, "--project", "demo", "--domain", "demo.test")

	const sentOTP = "739104"
	body := "From: Acme <noreply@acme.example>\r\n" +
		"To: user@demo.test\r\n" +
		"Subject: Xac thuc tai khoan\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<html><body><p>Mã xác thực của bạn là <b>" + sentOTP + "</b></p>" +
		`<p><a href="https://acme.example/verify?token=abc123">Xác nhận email</a></p>` +
		"</body></html>\r\n"
	deliver(t, srv.addr, "bounce@acme.example", "user@demo.test", body)

	msg := awaitExtraction(t, dataDir)
	if msg.ExtractionState != store.ExtractionSuccess {
		t.Fatalf("extraction state = %q, want success", msg.ExtractionState)
	}

	var res extract.Result
	if err := json.Unmarshal([]byte(msg.ExtractedJSON), &res); err != nil {
		t.Fatalf("extraction json: %v", err)
	}
	if res.OTPBest != sentOTP {
		t.Fatalf("extracted OTP = %q, want the one that was sent, %q", res.OTPBest, sentOTP)
	}
	if res.VerifyLinkBest == "" {
		t.Error("the verification link was not extracted")
	}
	if msg.Quarantined {
		t.Error("an allowlisted recipient was quarantined")
	}
	if msg.FromAddr != "noreply@acme.example" {
		t.Errorf("from_addr = %q, want the header From", msg.FromAddr)
	}
}

// TestBootstrapContract covers the startup rules that decide whether a
// data directory is safe to serve.
func TestBootstrapContract(t *testing.T) {
	t.Run("an empty directory needs both flags", func(t *testing.T) {
		out, err := runServeExpectingFailure(t, t.TempDir(), "--project", "demo")
		if err == nil {
			t.Fatal("serve started on an empty directory without a domain")
		}
		if !strings.Contains(out, "--domain") {
			t.Errorf("the error did not say what was missing: %q", out)
		}
	})

	t.Run("a mismatched project is refused", func(t *testing.T) {
		dataDir := t.TempDir()
		first := startBinary(t, dataDir, "--project", "demo", "--domain", "demo.test")
		first.stop()

		out, err := runServeExpectingFailure(t, dataDir, "--project", "somethingelse")
		if err == nil {
			t.Fatal("serve accepted a project name that does not match the stored one")
		}
		if !strings.Contains(out, "does not match") {
			t.Errorf("unexpected error: %q", out)
		}
	})

	t.Run("a mismatched allowlist is refused", func(t *testing.T) {
		dataDir := t.TempDir()
		first := startBinary(t, dataDir, "--project", "demo", "--domain", "demo.test")
		first.stop()

		out, err := runServeExpectingFailure(t, dataDir, "--project", "demo", "--domain", "other.test")
		if err == nil {
			t.Fatal("serve accepted an allowlist that does not match the stored one")
		}
		if !strings.Contains(out, "do not match") {
			t.Errorf("unexpected error: %q", out)
		}
	})

	t.Run("matching flags on an initialized directory start", func(t *testing.T) {
		dataDir := t.TempDir()
		first := startBinary(t, dataDir, "--project", "demo", "--domain", "demo.test")
		first.stop()

		// The same flags, and no flags at all, must both be accepted.
		second := startBinary(t, dataDir, "--project", "demo", "--domain", "demo.test")
		second.stop()
		third := startBinary(t, dataDir)
		third.stop()
	})
}

// runServeExpectingFailure runs serve to completion and returns its
// output. It is for the startup errors, which exit rather than serve.
func runServeExpectingFailure(t *testing.T, dataDir string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Port 0 on both listeners: these tests run in parallel and alongside
	// whatever the developer already has bound, so a fixed API port would
	// make them fail for a reason that has nothing to do with the code.
	full := append([]string{"serve", "--data-dir", dataDir,
		"--smtp-listen", "127.0.0.1:0", "--api-listen", "127.0.0.1:0"}, args...)
	// nolint:gosec // G204, same as above: the binary under test, run
	// with this test's own arguments.
	out, err := exec.CommandContext(ctx, binary(t), full...).CombinedOutput()
	return string(out), err
}

// TestRestartRecoversAndKeepsServing proves the durability claim end to
// end: a message acked by one process is complete after that process is
// replaced, without the sender doing anything again.
func TestRestartRecoversAndKeepsServing(t *testing.T) {
	dataDir := t.TempDir()
	first := startBinary(t, dataDir, "--project", "demo", "--domain", "demo.test")

	body := "From: a@acme.example\r\nTo: user@demo.test\r\nSubject: code\r\n\r\n" +
		"Ma xac thuc 481920\r\n"
	deliver(t, first.addr, "a@acme.example", "user@demo.test", body)
	msg := awaitExtraction(t, dataDir)
	first.stop()

	second := startBinary(t, dataDir)
	defer second.stop()

	after := awaitExtraction(t, dataDir)
	if after.ID != msg.ID {
		t.Errorf("the message changed identity across a restart: %q then %q", msg.ID, after.ID)
	}
	if after.ExtractionState != store.ExtractionSuccess {
		t.Errorf("state after restart = %q", after.ExtractionState)
	}

	// The restarted process is serving, not merely started.
	deliver(t, second.addr, "a@acme.example", "user@demo.test", body)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if len(readBack(t, dataDir)) == 2 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("the restarted process did not accept a new message")
}

// TestBadSeedURLIsAStartupError pins where the seed URL is validated. A
// bad URL discovered on the first acquire would fail a test run rather
// than a deployment, which is the wrong place to find out.
func TestBadSeedURLIsAStartupError(t *testing.T) {
	out, err := runServeExpectingFailure(t, t.TempDir(),
		"--project", "demo", "--domain", "demo.test", "--seed-url", "not-a-url")
	if err == nil {
		t.Fatal("serve started with an unusable seed url")
	}
	if !strings.Contains(out, "seed url") {
		t.Errorf("the error did not name the seed url: %q", out)
	}
}

// TestServeStartsWithTheLeaseFlags checks the approved flags are accepted
// and do not change the startup contract.
func TestServeStartsWithTheLeaseFlags(t *testing.T) {
	dataDir := t.TempDir()
	srv := startBinary(t, dataDir, "--project", "demo", "--domain", "demo.test",
		"--seed-url", "http://127.0.0.1:9/seed", "--pool-cooldown", "5s")
	defer srv.stop()

	// The server is serving, not merely started: the SMTP path still works
	// with the lease service wired in front of it.
	body := "From: a@acme.example\r\nTo: user@demo.test\r\nSubject: code\r\n\r\n" +
		"Ma xac thuc 481920\r\n"
	deliver(t, srv.addr, "a@acme.example", "user@demo.test", body)
	if msg := awaitExtraction(t, dataDir); msg.ExtractionState != store.ExtractionSuccess {
		t.Errorf("extraction state = %q", msg.ExtractionState)
	}
}

// TestNonLoopbackAPIBindNeedsAnExplicitHost is the counterpart to the
// SMTP bind contract: a surface that hands out credentials does not go
// wider than loopback because a flag happened to say so. Naming the Host
// values it should answer to is how the operator writes the decision down.
func TestNonLoopbackAPIBindNeedsAnExplicitHost(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8925", "192.0.2.10:8925", ":8925"} {
		out, err := runServeExpectingFailure(t, t.TempDir(),
			"--project", "demo", "--domain", "demo.test", "--api-listen", addr)
		if err == nil {
			t.Fatalf("serve bound %s without --api-host", addr)
		}
		if !strings.Contains(out, "--api-host") {
			t.Errorf("the error for %s did not name --api-host: %q", addr, out)
		}
	}
}

// TestServeNeverEmitsACredential is the whole point of moving
// provisioning out of serve.
//
// serve is the long-running process, so its output is the one thing that
// reliably ends up somewhere else: a CI log, a supervisor journal, a
// container runtime, a log shipper. None of those are destinations this
// program can see, so the rule is not "print it carefully" but "never hold
// it". The test asserts that on a first start, on a restart, and across
// both streams.
func TestServeNeverEmitsACredential(t *testing.T) {
	dataDir := t.TempDir()
	token := provisionBearer(t, dataDir, "--project", "demo", "--domain", "demo.test")

	for _, name := range []string{"first start", "restart"} {
		srv := startBinary(t, dataDir, "--project", "demo", "--domain", "demo.test")
		srv.stop()
		for stream, body := range map[string]string{"stderr": srv.logs(), "stdout": srv.stdout()} {
			if strings.Contains(body, token) {
				t.Errorf("%s: serve wrote the bearer to %s:\n%s", name, stream, body)
			}
			// The prefix too, so a future line that printed a different
			// or partial credential still fails.
			if strings.Contains(body, store.ProjectBearerPrefix) {
				t.Errorf("%s: serve wrote something bearer-shaped to %s:\n%s", name, stream, body)
			}
		}
	}

	// And the value is nowhere on disk: the operator moves it into a
	// secret store from the terminal, and the data directory never holds
	// it.
	assertNotOnDisk(t, dataDir, token)
}

// TestServeRefusesTheAPIWithoutAProvisionedBearer pins the fail-closed
// half of the contract.
//
// A serve that started an API nobody could authenticate against would be a
// surface with one reachable route and a confusing 401 on the rest. It
// stops instead, and names the command that fixes it.
func TestServeRefusesTheAPIWithoutAProvisionedBearer(t *testing.T) {
	dataDir := t.TempDir()
	out, err := runServeExpectingFailure(t, dataDir, "--project", "demo", "--domain", "demo.test")
	if err == nil {
		t.Fatal("serve started the API for a project with no bearer")
	}
	if !strings.Contains(out, "project bearer provision") {
		t.Errorf("the error did not name the provisioning command: %q", out)
	}
	if strings.Contains(out, store.ProjectBearerPrefix) {
		t.Errorf("the refusal leaked something bearer-shaped: %q", out)
	}
}

// TestServeRunsAsAMailCatcherWithoutABearer keeps the refusal proportional:
// the credential guards the API, so an instance with no API does not need
// one. Without this, adding the check would have quietly broken every
// mail-catcher-only deployment.
func TestServeRunsAsAMailCatcherWithoutABearer(t *testing.T) {
	dataDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// nolint:gosec // G204, same as above: the binary under test, run with
	// this test's own arguments.
	cmd := exec.CommandContext(ctx, binary(t), "serve", "--data-dir", dataDir,
		"--project", "demo", "--domain", "demo.test",
		"--smtp-listen", "127.0.0.1:0", "--api-listen", "")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()

	line := make(chan string, 1)
	go func() {
		text, err := bufio.NewReader(stdout).ReadString('\n')
		if err == nil {
			line <- text
		}
	}()
	select {
	case text := <-line:
		if !strings.Contains(text, "api off") {
			t.Errorf("the ready line did not report the API as off: %q", text)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the mail catcher never announced a port\nstderr: %s", stderr.String())
	}
}

// TestServeRejectsTheRemovedRotateFlag checks the flag that used to mint
// and print a credential fails with a pointer to its replacement rather
// than with the flag package's "not defined", which would leave an
// operator guessing where the capability went.
func TestServeRejectsTheRemovedRotateFlag(t *testing.T) {
	dataDir := t.TempDir()
	provisionBearer(t, dataDir, "--project", "demo", "--domain", "demo.test")

	out, err := runServeExpectingFailure(t, dataDir, "--rotate-bearer")
	if err == nil {
		t.Fatal("serve accepted --rotate-bearer")
	}
	if !strings.Contains(out, "project bearer rotate") {
		t.Errorf("the error did not name the replacement command: %q", out)
	}
	if strings.Contains(out, store.ProjectBearerPrefix) {
		t.Errorf("the refusal leaked something bearer-shaped: %q", out)
	}
}

// assertNotOnDisk fails if a credential appears anywhere under a data
// directory.
func assertNotOnDisk(t *testing.T, dataDir, token string) {
	t.Helper()
	if err := filepath.WalkDir(dataDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		// nolint:gosec // G304 flags the variable path. It comes from
		// WalkDir over this test's own t.TempDir(), not from input.
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if strings.Contains(string(body), token) {
			t.Errorf("the bearer was written to %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
