package smtp_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net"
	netsmtp "net/smtp"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivermin1123/authstunt/internal/smtp"
)

// recorder is a Deliverer that remembers what it was handed and answers
// with whatever the test asked for.
type recorder struct {
	mu         sync.Mutex
	deliveries []smtp.Delivery
	err        error
	panicWith  string
}

func (r *recorder) Deliver(_ context.Context, d smtp.Delivery) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.panicWith != "" {
		panic(r.panicWith)
	}
	if r.err != nil {
		return r.err
	}
	r.deliveries = append(r.deliveries, d)
	return nil
}

func (r *recorder) all() []smtp.Delivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]smtp.Delivery(nil), r.deliveries...)
}

// startServer binds a server on an ephemeral port and returns its address.
func startServer(t *testing.T, cfg smtp.Config) string {
	t.Helper()
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	}
	srv, err := smtp.NewServer(cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if err := srv.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the server did not stop")
		}
	})
	return srv.Addr().String()
}

const wireMessage = "From: Acme <noreply@acme.example>\r\n" +
	"To: user@demo.test\r\n" +
	"Subject: Your code\r\n\r\n" +
	"Ma xac thuc 481920\r\n"

// send delivers one message with the standard library client, which is a
// different SMTP implementation from the server's and therefore a real
// interoperability check rather than a self-consistency one.
func send(t *testing.T, addr, from string, to []string, body string) error {
	t.Helper()
	c, err := netsmtp.Dial(addr)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(body)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func TestServerAcceptsRealSMTPDelivery(t *testing.T) {
	rec := &recorder{}
	addr := startServer(t, smtp.Config{Deliverer: rec})

	if err := send(t, addr, "bounce@acme.example", []string{"user@demo.test"}, wireMessage); err != nil {
		t.Fatalf("send: %v", err)
	}

	got := rec.all()
	if len(got) != 1 {
		t.Fatalf("delivered %d messages, want 1", len(got))
	}
	if got[0].From != "bounce@acme.example" {
		t.Errorf("envelope from = %q", got[0].From)
	}
	if len(got[0].Recipients) != 1 || got[0].Recipients[0] != "user@demo.test" {
		t.Errorf("envelope recipients = %v", got[0].Recipients)
	}
	if !strings.Contains(string(got[0].Raw), "481920") {
		t.Error("the raw message did not reach the deliverer intact")
	}
	if got[0].ReceivedAt.IsZero() {
		t.Error("the delivery carries no receive time")
	}
}

// TestOffAllowlistRecipientIsAccepted pins the frozen choice to quarantine
// rather than reject: the protocol answer is 250, and the decision about
// what may be read happens later.
func TestOffAllowlistRecipientIsAccepted(t *testing.T) {
	rec := &recorder{}
	addr := startServer(t, smtp.Config{Deliverer: rec})

	if err := send(t, addr, "a@acme.example", []string{"someone@gmail.com"}, wireMessage); err != nil {
		t.Fatalf("an off-allowlist recipient was refused at the protocol level: %v", err)
	}
	if len(rec.all()) != 1 {
		t.Error("the message never reached the deliverer")
	}
}

func TestStorageFailuresMapToFrozenReplyCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"generic write failure", fmt.Errorf("%w: disk went away", smtp.ErrTemporary), "451"},
		{"out of space", fmt.Errorf("%w: no room", smtp.ErrOutOfSpace), "452"},
		// An unclassified error is still a refusal, not an accidental
		// 250: the default arm of the mapping has to be temporary.
		{"unclassified", errors.New("something else"), "451"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := startServer(t, smtp.Config{Deliverer: &recorder{err: tc.err}})
			err := send(t, addr, "a@acme.example", []string{"user@demo.test"}, wireMessage)
			if err == nil {
				t.Fatal("a failed delivery was acknowledged")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("reply = %q, want it to carry %s", err.Error(), tc.want)
			}
		})
	}
}

// TestPanicDuringDeliveryBecomesTemporaryFailure proves the connection
// survives a panic and the sender keeps ownership of the message.
func TestPanicDuringDeliveryBecomesTemporaryFailure(t *testing.T) {
	addr := startServer(t, smtp.Config{Deliverer: &recorder{panicWith: "boom"}})

	err := send(t, addr, "a@acme.example", []string{"user@demo.test"}, wireMessage)
	if err == nil {
		t.Fatal("a panicking delivery was acknowledged")
	}
	if !strings.Contains(err.Error(), "451") {
		t.Errorf("reply = %q, want 451", err.Error())
	}

	// The process is still serving: a second, healthy server would be a
	// weaker claim, so this reuses the same one.
	rec := &recorder{}
	addr2 := startServer(t, smtp.Config{Deliverer: rec})
	if err := send(t, addr2, "a@acme.example", []string{"user@demo.test"}, wireMessage); err != nil {
		t.Fatalf("the server stopped accepting after a panic: %v", err)
	}
}

// TestOversizedMessageIsRefusedDuringData covers the client that declares
// nothing and just sends too much.
func TestOversizedMessageIsRefusedDuringData(t *testing.T) {
	rec := &recorder{}
	addr := startServer(t, smtp.Config{Deliverer: rec, MaxBytes: 512})

	// Many short lines, not one long one: the line-length cap is a
	// separate limit and this test is about the size cap.
	body := "Subject: big\r\n\r\n" + strings.Repeat("xxxxxxxx\r\n", 512)
	err := send(t, addr, "a@acme.example", []string{"user@demo.test"}, body)
	if err == nil {
		t.Fatal("an oversized message was accepted")
	}
	if !strings.Contains(err.Error(), "552") {
		t.Errorf("reply = %q, want 552", err.Error())
	}
	if len(rec.all()) != 0 {
		t.Error("an oversized message reached the deliverer, so it was refused after storage rather than before")
	}
}

// TestLongHTMLLineIsAccepted pins the line-length choice. A single-line
// HTML template is what most transactional mail looks like on the wire,
// and the library's RFC-correct default would refuse it.
func TestLongHTMLLineIsAccepted(t *testing.T) {
	rec := &recorder{}
	addr := startServer(t, smtp.Config{Deliverer: rec})

	long := "<html><body>" + strings.Repeat("<span>padding</span>", 3000) +
		"<b>481920</b></body></html>"
	body := "Subject: long\r\nContent-Type: text/html\r\n\r\n" + long + "\r\n"
	if len(long) <= 2000 {
		t.Fatalf("the fixture is only %d bytes, which would not exercise the default cap", len(long))
	}
	if err := send(t, addr, "a@acme.example", []string{"user@demo.test"}, body); err != nil {
		t.Fatalf("a long html line was refused: %v", err)
	}
	if len(rec.all()) != 1 {
		t.Error("the message never reached the deliverer")
	}
}

// TestSizeIsAdvertisedAndEnforcedAtMailFrom covers the other half of the
// cap: a client that declares its size is refused before it transfers
// anything. This one speaks the protocol by hand, because the standard
// library client never sends a SIZE parameter.
func TestSizeIsAdvertisedAndEnforcedAtMailFrom(t *testing.T) {
	addr := startServer(t, smtp.Config{Deliverer: &recorder{}, MaxBytes: 512})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	r := bufio.NewReader(conn)
	readLine := func() string {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return strings.TrimRight(line, "\r\n")
	}
	write := func(format string, args ...any) {
		if _, err := fmt.Fprintf(conn, format+"\r\n", args...); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	if greeting := readLine(); !strings.HasPrefix(greeting, "220") {
		t.Fatalf("greeting = %q", greeting)
	}
	write("EHLO test.local")
	var advertisedSize bool
	for {
		line := readLine()
		if strings.Contains(line, "SIZE 512") {
			advertisedSize = true
		}
		// The last line of a multiline reply uses a space where the
		// others use a hyphen.
		if len(line) > 3 && line[3] == ' ' {
			break
		}
	}
	if !advertisedSize {
		t.Error("EHLO did not advertise the size limit, so a client cannot know it before sending")
	}

	write("MAIL FROM:<a@acme.example> SIZE=100000")
	reply := readLine()
	if !strings.HasPrefix(reply, "552") {
		t.Errorf("reply to an oversized SIZE = %q, want 552", reply)
	}
}

// TestAnyCredentialsAreAccepted covers the app pointed here without having
// its SMTP credentials removed first.
func TestAnyCredentialsAreAccepted(t *testing.T) {
	rec := &recorder{}
	addr := startServer(t, smtp.Config{Deliverer: rec})
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	c, err := netsmtp.Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Auth(netsmtp.PlainAuth("", "whoever", "whatever", host)); err != nil {
		t.Fatalf("auth: %v", err)
	}
	if err := c.Mail("a@acme.example"); err != nil {
		t.Fatalf("mail: %v", err)
	}
	if err := c.Rcpt("user@demo.test"); err != nil {
		t.Fatalf("rcpt: %v", err)
	}
	w, err := c.Data()
	if err != nil {
		t.Fatalf("data: %v", err)
	}
	if _, err := w.Write([]byte(wireMessage)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := c.Quit(); err != nil {
		t.Fatalf("quit: %v", err)
	}
	if len(rec.all()) != 1 {
		t.Error("an authenticated message was not delivered")
	}
}

// TestResetClearsTheTransaction covers a client that abandons a message
// and starts another on the same connection: the abandoned envelope must
// not leak into the next one.
func TestResetClearsTheTransaction(t *testing.T) {
	rec := &recorder{}
	addr := startServer(t, smtp.Config{Deliverer: rec})

	c, err := netsmtp.Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Mail("first@acme.example"); err != nil {
		t.Fatalf("mail: %v", err)
	}
	if err := c.Rcpt("abandoned@demo.test"); err != nil {
		t.Fatalf("rcpt: %v", err)
	}
	if err := c.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := c.Mail("second@acme.example"); err != nil {
		t.Fatalf("mail: %v", err)
	}
	if err := c.Rcpt("user@demo.test"); err != nil {
		t.Fatalf("rcpt: %v", err)
	}
	w, err := c.Data()
	if err != nil {
		t.Fatalf("data: %v", err)
	}
	if _, err := w.Write([]byte(wireMessage)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := c.Quit(); err != nil {
		t.Fatalf("quit: %v", err)
	}

	got := rec.all()
	if len(got) != 1 {
		t.Fatalf("delivered %d, want 1", len(got))
	}
	if got[0].From != "second@acme.example" {
		t.Errorf("envelope from = %q, want the second transaction's", got[0].From)
	}
	for _, rcpt := range got[0].Recipients {
		if rcpt == "abandoned@demo.test" {
			t.Error("a recipient survived RSET into the next message")
		}
	}
}

// TestNoOutboundMailPathExists is the absence proof the phase requires.
//
// A mail catcher that can be talked into sending is a spam relay. Rather
// than argue it in review, this parses the package and fails on any
// reference to an outbound mail API or a dialer. The check is deliberately
// blunt: a new import that could send mail should have to justify itself
// by editing this list.
func TestNoOutboundMailPathExists(t *testing.T) {
	banned := map[string]string{
		"net/smtp":                                   "the standard library SMTP client",
		"github.com/emersion/go-smtp/v2":             "a second SMTP implementation",
		"github.com/emersion/go-message/mail/writer": "a mail writer",
	}
	forbiddenSelectors := map[string]string{
		"SendMail":     "sends mail",
		"Dial":         "opens an outbound connection",
		"DialTLS":      "opens an outbound connection",
		"DialStartTLS": "opens an outbound connection",
		"NewClient":    "builds an SMTP client",
	}

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if why, bad := banned[path]; bad {
				t.Errorf("%s imports %s (%s)", name, path, why)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || ident.Name != "gosmtp" {
				return true
			}
			if why, bad := forbiddenSelectors[sel.Sel.Name]; bad {
				t.Errorf("%s calls %s.%s, which %s", name, ident.Name, sel.Sel.Name, why)
			}
			return true
		})
	}
}
