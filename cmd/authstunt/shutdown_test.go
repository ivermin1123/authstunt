package main_test

import (
	"bufio"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Nothing in this tree had ever run the shutdown sequence. Every test stopped
// the server by canceling the exec context, which kills the process
// outright, so signal.NotifyContext never fired and srv.Shutdown,
// apiSrv.Shutdown and ingest.Stop were unreachable from any test.
//
// Running it turned up something the comment in serve did not predict, and
// the owner has since ruled on it: stopping closes the SMTP server outright,
// which closes every active connection with it, and a graceful drain is a
// non-goal until something needs one. These tests therefore assert what the
// server actually does, not what the old comment described.
//
// # Known limitation: this file does not run on Windows
//
// Both tests below are skipped there, and nothing covers the shutdown path on
// Windows as a result. That is not the same situation as the POSIX mode-bit
// skips in internal/secrets, where a Windows-equivalent assertion exists and
// runs. Here there is no equivalent to write: os/exec_windows.go implements
// Process.Signal for os.Kill alone and returns EWINDOWS for everything else,
// with a "TODO(rsc): Handle Interrupt too?" still in the source, so a Go test
// cannot deliver SIGTERM to a child at all. Reaching the same code path would
// mean a console control event through GenerateConsoleCtrlEvent, which needs
// a shared console group and a golang.org/x/sys/windows call from the test
// process, and that is a larger piece of work than the gap justifies today.
//
// It is written down here rather than left as a bare t.Skip because CI runs
// go test without -v: the Windows job reports ok for this package whether
// these tests ran or not, so the skip is invisible in the log that would
// otherwise be taken as evidence.

// smtpSession is a hand-driven SMTP conversation, which the standard client
// cannot express: this test has to hold a message half-written across a
// signal.
type smtpSession struct {
	conn net.Conn
	br   *bufio.Reader
}

func dialSMTP(t *testing.T, addr string) *smtpSession {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	if err := conn.SetDeadline(time.Now().Add(45 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	s := &smtpSession{conn: conn, br: bufio.NewReader(conn)}
	t.Cleanup(func() { _ = conn.Close() })
	s.expect(t, "220")
	return s
}

// expect reads one reply, following multiline continuations, and checks its
// code.
func (s *smtpSession) expect(t *testing.T, code string) string {
	t.Helper()
	var last string
	for {
		line, err := s.br.ReadString('\n')
		if err != nil {
			t.Fatalf("read reply: %v", err)
		}
		last = strings.TrimRight(line, "\r\n")
		if len(last) < 4 || last[3] != '-' {
			break
		}
	}
	if !strings.HasPrefix(last, code) {
		t.Fatalf("reply %q, want %s", last, code)
	}
	return last
}

func (s *smtpSession) send(t *testing.T, format string, args ...any) {
	t.Helper()
	if _, err := fmt.Fprintf(s.conn, format+"\r\n", args...); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// openData walks a session up to the point where the body is being written
// and stops there, with the message neither finished nor abandoned.
func (s *smtpSession) openData(t *testing.T, from, rcpt string) {
	t.Helper()
	s.send(t, "EHLO shutdown.test")
	s.expect(t, "250")
	s.send(t, "MAIL FROM:<%s>", from)
	s.expect(t, "250")
	s.send(t, "RCPT TO:<%s>", rcpt)
	s.expect(t, "250")
	s.send(t, "DATA")
	s.expect(t, "354")
	s.send(t, "From: Acme <noreply@acme.example>")
	s.send(t, "To: %s", rcpt)
	s.send(t, "Subject: Your verification code")
	s.send(t, "")
}

// finishData writes the body and the terminating dot, which is what makes
// the server hand the message to ingest.
func (s *smtpSession) finishData(t *testing.T, code string) {
	t.Helper()
	s.send(t, "Ma xac thuc cua ban la %s. Ma het han sau 5 phut.", code)
	s.send(t, ".")
	s.expect(t, "250")
	s.send(t, "QUIT")
}

// tryFinishData is finishData for a session that is expected to lose the race
// with shutdown. It reports the failure instead of ending the test, because a
// severed connection is a valid outcome here and only the test goroutine may
// call Fatal.
func (s *smtpSession) tryFinishData(code string) error {
	body := fmt.Sprintf("Ma xac thuc cua ban la %s. Ma het han sau 5 phut.\r\n.\r\n", code)
	if _, err := s.conn.Write([]byte(body)); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	line, err := s.br.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read reply: %w", err)
	}
	if !strings.HasPrefix(line, "250") {
		return fmt.Errorf("reply %q", strings.TrimRight(line, "\r\n"))
	}
	return nil
}

// TestSigtermKeepsEverythingItAcked is the drain contract at the process
// boundary: a message the server answered 250 to is on disk after the
// process leaves, and the process leaves cleanly.
func TestSigtermKeepsEverythingItAcked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os/exec cannot deliver SIGTERM on Windows (EWINDOWS); see the known limitation above")
	}
	const messages = 12
	dataDir := t.TempDir()
	srv := startBinary(t, dataDir, "--project", "demo", "--domain", "demo.test")

	// Delivered as fast as the sessions can go, so the signal lands while
	// the extraction workers still have a backlog to get through.
	var wg sync.WaitGroup
	for i := range messages {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := dialSMTP(t, srv.addr)
			s.openData(t, "bounce@acme.example", fmt.Sprintf("user-%d@demo.test", i))
			s.finishData(t, fmt.Sprintf("%06d", 100000+i))
		}()
	}
	wg.Wait()

	if code := srv.terminate(t); code != 0 {
		t.Errorf("exit code = %d after SIGTERM, want 0\nstderr: %s", code, srv.logs())
	}
	if logs := srv.logs(); strings.Contains(logs, "panic") {
		t.Errorf("the shutdown path panicked:\n%s", logs)
	}

	// Read through a second process, the way a later tool would.
	stored := readBack(t, dataDir)
	if len(stored) != messages {
		t.Fatalf("stored %d of %d acked messages after shutdown", len(stored), messages)
	}
}

// TestShutdownNeverAcksAMessageItDidNotKeep holds the ack contract across a
// signal, for sessions that were mid-body when it arrived.
//
// Two things are asserted, and the second is why the first is not enough on
// its own.
//
// The invariant: a 250 is a promise that the message is on disk, so every
// session that was answered must be readable afterwards. That holds under any
// shutdown design. On its own, though, it is satisfied trivially by a server
// that answers nobody, so it would keep passing against a binary with no
// shutdown handling at all.
//
// The behavior: stopping severs sessions that are mid-body rather than
// draining them, which is the owner's ruling recorded in DECISIONS. So the
// test also asserts that these sessions really were cut off - that nothing
// silently started draining, and that the severed ones left nothing behind.
// If a drain is ever implemented this fails loudly and asks to be rewritten,
// which is the correct outcome for a test that pins a decision rather than a
// law.
func TestShutdownSeversInFlightSessionsAndAcksNothingItDropped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os/exec cannot deliver SIGTERM on Windows (EWINDOWS); see the known limitation above")
	}
	const inFlight = 10
	dataDir := t.TempDir()
	srv := startBinary(t, dataDir, "--project", "demo", "--domain", "demo.test")

	sessions := make([]*smtpSession, inFlight)
	for i := range sessions {
		sessions[i] = dialSMTP(t, srv.addr)
		sessions[i].openData(t, "bounce@acme.example", fmt.Sprintf("held-%d@demo.test", i))
	}

	// The finishers park briefly so the signal lands while every session is
	// still mid-body. terminate stays on the test goroutine, because it
	// reports failures with Fatal and only this goroutine may call it.
	var (
		mu      sync.Mutex
		acked   int
		refused []string
		wg      sync.WaitGroup
	)
	for i, s := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(300 * time.Millisecond)
			err := s.tryFinishData(fmt.Sprintf("%06d", 200000+i))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				refused = append(refused, err.Error())
				return
			}
			acked++
		}()
	}

	code := srv.terminate(t)
	wg.Wait()

	if code != 0 {
		t.Errorf("exit code = %d, want 0: the shutdown sequence did not finish "+
			"cleanly with sessions still delivering\nstderr: %s", code, srv.logs())
	}
	if logs := srv.logs(); strings.Contains(logs, "panic") {
		t.Fatalf("a session delivering during shutdown crashed the process:\n%s", logs)
	}

	stored := readBack(t, dataDir)

	mu.Lock()
	defer mu.Unlock()
	// The invariant, which must hold whatever shutdown does.
	if len(stored) < acked {
		t.Fatalf("%d sessions were answered 250 but only %d messages are on disk: "+
			"a message was acked and then dropped by shutdown", acked, len(stored))
	}
	// The decision, which this test exists to pin.
	if acked != 0 {
		t.Errorf("%d of %d sessions held open across the signal were answered 250; "+
			"stopping is supposed to sever them, so either a drain was "+
			"implemented or the signal landed too late", acked, inFlight)
	}
	if len(refused) != inFlight {
		t.Errorf("%d of %d sessions reported being cut off, want all of them",
			len(refused), inFlight)
	}
	if len(stored) != 0 {
		t.Errorf("%d messages are on disk from sessions that were never acked", len(stored))
	}
}
