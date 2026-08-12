package main_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
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
	addr    string
	dataDir string
	stop    func()
}

// startBinary launches serve and waits for the line it prints once the
// port is accepting, which is what makes the test deterministic without a
// sleep.
func startBinary(t *testing.T, dataDir string, args ...string) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	full := append([]string{"serve", "--data-dir", dataDir, "--smtp-listen", "127.0.0.1:0"}, args...)
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

	line := make(chan string, 1)
	go func() {
		r := bufio.NewReader(stdout)
		text, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line <- strings.TrimSpace(text)
		// Keep draining so the process never blocks on a full pipe.
		_, _ = io.Copy(io.Discard, r)
	}()

	select {
	case text := <-line:
		_, addr, ok := strings.Cut(text, "smtp ")
		if !ok {
			t.Fatalf("startup line did not carry an address: %q", text)
		}
		return &running{addr: addr, dataDir: dataDir, stop: stop}
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
	full := append([]string{"serve", "--data-dir", dataDir, "--smtp-listen", "127.0.0.1:0"}, args...)
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
