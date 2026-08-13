package main_test

import (
	"bufio"
	"fmt"
	"net"
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
// Running it turned up something the comment in serve did not predict. That
// comment describes draining the listener before the extraction workers, so a
// session still inside Data cannot publish onto a closed queue. The drain does
// not happen: cancellation closes the SMTP server outright, which closes every
// active connection with it, so srv.Shutdown afterwards only ever reports
// "server already closed" and no session survives to reach the queue. The
// ordering it protects is therefore inert today - reversing the two lines
// fails nothing - and that is an open question for the owner rather than
// something these tests quietly encode.

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
	if runtimeIsWindows() {
		t.Skip("SIGTERM is not deliverable on Windows; the drain path is covered on unix")
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
// The contract is one-directional and survives any shutdown design: a 250 is
// a promise that the message is on disk, so every session that was answered
// must be readable afterwards. A session that was cut off instead has no
// promise attached to it - its client saw a broken connection and will send
// again - so this asserts against what was acked rather than against what was
// attempted.
//
// Written that way on purpose. Today the SMTP listener is closed on
// cancellation rather than drained, so these sessions are severed and none of
// them is acked; the assertion passes without pinning that behavior in place.
// If the drain the shutdown comment describes is ever implemented, the same
// assertion gets stronger on its own: the sessions would then be answered,
// and every one of them would have to be on disk.
func TestShutdownNeverAcksAMessageItDidNotKeep(t *testing.T) {
	if runtimeIsWindows() {
		t.Skip("SIGTERM is not deliverable on Windows; the path is covered on unix")
	}
	const inFlight = 10
	dataDir := t.TempDir()
	srv := startBinary(t, dataDir, "--project", "demo", "--domain", "demo.test")

	sessions := make([]*smtpSession, inFlight)
	for i := range sessions {
		sessions[i] = dialSMTP(t, srv.addr)
		sessions[i].openData(t, "bounce@acme.example", fmt.Sprintf("held-%d@demo.test", i))
	}

	// Every session is now mid-body when the signal lands.
	exit := make(chan int, 1)
	go func() {
		exit <- srv.terminate(t)
	}()
	time.Sleep(300 * time.Millisecond)

	var (
		mu     sync.Mutex
		acked  int
		wg     sync.WaitGroup
		logged = make([]string, 0, inFlight)
	)
	for i, s := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.tryFinishData(fmt.Sprintf("%06d", 200000+i)); err != nil {
				mu.Lock()
				logged = append(logged, err.Error())
				mu.Unlock()
				return
			}
			mu.Lock()
			acked++
			mu.Unlock()
		}()
	}
	wg.Wait()

	if code := <-exit; code != 0 {
		t.Errorf("exit code = %d, want 0: the shutdown sequence did not finish "+
			"cleanly with sessions still delivering\nstderr: %s", code, srv.logs())
	}
	if logs := srv.logs(); strings.Contains(logs, "panic") {
		t.Fatalf("a session delivering during shutdown crashed the process:\n%s", logs)
	}

	stored := readBack(t, dataDir)
	if len(stored) < acked {
		t.Fatalf("%d sessions were answered 250 but only %d messages are on disk: "+
			"a message was acked and then dropped by shutdown", acked, len(stored))
	}
	t.Logf("of %d sessions held open across SIGTERM, %d were acked and %d messages "+
		"are on disk; the rest were cut off before any promise was made: %v",
		inFlight, acked, len(stored), logged)
}
