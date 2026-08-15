package smtp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
)

// MaxMessageBytes is the hard message size cap (design 2.2). go-smtp
// enforces it twice on its own: it advertises "SIZE 10485760" in EHLO and
// answers an oversized MAIL FROM SIZE parameter with 552 5.3.4, then the
// DATA reader returns the same 552 for a client that declared nothing.
const MaxMessageBytes = 10 << 20

// MaxLineLength is the longest single line accepted inside DATA.
//
// RFC 5321 section 4.5.3.1.6 sets 1000 octets and go-smtp defaults to
// twice that, which is correct for a relay and wrong here. This server
// exists to capture what applications actually send, and real HTML
// templates routinely put a whole document on one line. Refusing them
// would report a product bug that is really a mail-catcher bug. The
// overall message cap still bounds memory, so a long line cannot cost more
// than a large message already could.
const MaxLineLength = 1 << 20

// Timeouts on an idle or stalled connection. SMTP has no framing that would
// let the server tell a slow sender from a dead one, so a connection that
// says nothing for this long is closed rather than held forever.
const (
	defaultReadTimeout  = 60 * time.Second
	defaultWriteTimeout = 60 * time.Second
)

// Delivery is one message as SMTP handed it over: the envelope, the bytes,
// and when the server took responsibility for them.
//
// From is the envelope MAIL FROM, which is not necessarily the From header
// the reader sees; ingest resolves that difference. Recipients holds every
// envelope RCPT TO in the order the client sent them, including addresses
// this project does not own - deciding that is quarantine's job, not the
// session's.
type Delivery struct {
	From       string
	Recipients []string
	Raw        []byte
	ReceivedAt time.Time
}

// Deliverer takes one delivered message and is responsible for durability.
//
// The contract that makes the SMTP ack honest lives on this seam: Deliver
// returns nil only once the message is stored durably enough that losing
// the process cannot lose the mail. The session answers 250 on nil and
// nothing else.
type Deliverer interface {
	Deliver(ctx context.Context, d Delivery) error
}

// ErrTemporary marks a failure the sender should retry: a write that did
// not land, a deadline that passed. It maps to 451 4.3.0.
var ErrTemporary = errors.New("smtp: temporary failure")

// ErrOutOfSpace marks the one temporary failure with its own reply code.
// RFC 5321 reserves 452 for "insufficient system storage", so a generic
// write failure must not borrow it.
var ErrOutOfSpace = errors.New("smtp: insufficient storage")

// Config is the server's settings. Only Deliverer is required.
type Config struct {
	// Addr is the listen address. Defaults to loopback on 1025, which is
	// the default that keeps a test tool off a machine's public
	// interfaces until someone deliberately says otherwise.
	Addr string
	// Domain is the name the server greets with.
	Domain string
	// Deliverer receives every accepted message.
	Deliverer Deliverer
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Now overrides the clock stamping ReceivedAt. Tests set it.
	Now func() time.Time

	// MaxBytes overrides the message size cap. It exists so a test can
	// prove the cap with a small message instead of pushing 10MB through
	// a loopback socket; production leaves it zero.
	MaxBytes int64

	// MaxLineLength overrides the per-line cap. Production leaves it zero.
	MaxLineLength int

	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// Server is the SMTP listener.
//
// It wraps go-smtp rather than exposing it, per design 3.3, so the library
// stays swappable and so every reply code the product emits is written
// down in one place.
//
// There is no outbound path here on purpose. This server receives mail and
// never sends any: no client, no dialer, no relay. A test tool that could
// be talked into forwarding a message would be a spam relay wearing a
// developer tool's name.
type Server struct {
	inner  *gosmtp.Server
	ln     net.Listener
	logger *slog.Logger
	now    func() time.Time

	// baseCtx is the context Serve was given. go-smtp hands no context to
	// a session, so this is how a shutdown reaches work already running
	// inside Data. It is written once before Serve accepts anything and
	// only read afterwards.
	baseCtx context.Context
}

// NewServer builds the listener. It does not bind: call Listen.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Deliverer == nil {
		return nil, errors.New("smtp: a deliverer is required")
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:1025"
	}
	if cfg.Domain == "" {
		cfg.Domain = "authstunt.local"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = defaultReadTimeout
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = defaultWriteTimeout
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = MaxMessageBytes
	}
	if cfg.MaxLineLength <= 0 {
		cfg.MaxLineLength = MaxLineLength
	}

	s := &Server{logger: cfg.Logger, now: cfg.Now}
	inner := gosmtp.NewServer(gosmtp.BackendFunc(func(c *gosmtp.Conn) (gosmtp.Session, error) {
		return &session{
			server:    s,
			deliverer: cfg.Deliverer,
			remote:    remoteAddr(c),
		}, nil
	}))
	inner.Addr = cfg.Addr
	inner.Domain = cfg.Domain
	inner.MaxMessageBytes = cfg.MaxBytes
	inner.MaxLineLength = cfg.MaxLineLength
	inner.ReadTimeout = cfg.ReadTimeout
	inner.WriteTimeout = cfg.WriteTimeout
	// AUTH is advertised and every credential is accepted. An app under
	// test is usually pointed here by changing a host and a port in a
	// config that still carries the credentials it used for a real
	// provider, and making that app fail on AUTH would be a support
	// question, not a security control. Advertising AUTH over a plaintext
	// loopback listener needs this flag; the credentials are ignored, so
	// there is nothing here to steal.
	inner.AllowInsecureAuth = true
	s.inner = inner
	return s, nil
}

// Listen binds the configured address. It is separate from Serve so a
// caller - a test, or a startup sequence that must fail loudly on a taken
// port - learns about a bind failure before anything else starts.
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.inner.Addr)
	if err != nil {
		return fmt.Errorf("smtp: listen %s: %w", s.inner.Addr, err)
	}
	s.ln = ln
	return nil
}

// Addr reports the bound address, which is how a caller that asked for
// port 0 finds out what it got.
func (s *Server) Addr() net.Addr {
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Serve accepts connections until the context is canceled or Shutdown is
// called. It returns nil on a clean stop.
func (s *Server) Serve(ctx context.Context) error {
	if s.ln == nil {
		if err := s.Listen(); err != nil {
			return err
		}
	}
	s.baseCtx = ctx
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			// Close is the only way to unblock Accept. A serve error
			// after this point is the expected consequence, not a
			// failure, which is why Serve reports nil below.
			_ = s.inner.Close()
		case <-done:
		}
	}()

	err := s.inner.Serve(s.ln)
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("smtp: serve: %w", err)
	}
	return nil
}

// Shutdown stops the server and reports nil if it was already stopped.
//
// Already stopped is the normal case, not a failure. Serve closes the server
// as soon as its context is canceled, so by the time a caller reaches its own
// shutdown step the work is usually done; treating that as an error made
// every clean stop log one. Shutdown is idempotent here for the same reason
// Close is: the caller asked for a stopped server and has one.
//
// It does not drain in-flight sessions, because closing the server closes
// their connections with it. See the shutdown sequence in cmd/authstunt.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.inner.Shutdown(ctx)
	switch {
	case err == nil, errors.Is(err, net.ErrClosed), errors.Is(err, gosmtp.ErrServerClosed):
		return nil
	default:
		return fmt.Errorf("smtp: shutdown: %w", err)
	}
}

// session is one client connection's mail transaction.
type session struct {
	server    *Server
	deliverer Deliverer
	remote    string

	from  string
	rcpts []string
}

var (
	_ gosmtp.Session     = (*session)(nil)
	_ gosmtp.AuthSession = (*session)(nil)
)

// AuthMechanisms advertises PLAIN only. LOGIN is obsolete and adds a second
// code path to accept the same ignored credentials.
func (s *session) AuthMechanisms() []string { return []string{sasl.Plain} }

// Auth accepts any credentials. See the AllowInsecureAuth comment above.
func (s *session) Auth(mech string) (sasl.Server, error) {
	if mech != sasl.Plain {
		return nil, gosmtp.ErrAuthUnknownMechanism
	}
	// Every argument is ignored on purpose: the identity, the username
	// and the password are all accepted as given.
	return sasl.NewPlainServer(func(_, _, _ string) error {
		return nil
	}), nil
}

func (s *session) Mail(from string, _ *gosmtp.MailOptions) error {
	s.from = from
	return nil
}

// Rcpt accepts every recipient, including addresses outside the project's
// allowlist.
//
// Rejecting them here with 550 would be the tidier SMTP answer, but it
// would also destroy the evidence: an app misconfigured to mail a real
// customer would get a bounce and the operator would never see what was
// sent. The frozen contract instead accepts the message and quarantines it,
// where it stays visible to a human and invisible to the automated read
// path.
func (s *session) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	s.rcpts = append(s.rcpts, to)
	return nil
}

// context returns the context sessions run under.
func (s *Server) context() context.Context {
	if s.baseCtx == nil {
		return context.Background()
	}
	return s.baseCtx
}

// reply turns a delivery failure into the reply code the owner froze.
//
// Only two temporary shapes exist. 452 is reserved for insufficient
// storage, which RFC 5321 gives its own code, and everything else that
// failed to store is 451: a generic write failure is not "out of storage"
// and saying so would send an operator looking at the wrong thing.
func (s *Server) reply(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrOutOfSpace):
		return &gosmtp.SMTPError{
			Code:         452,
			EnhancedCode: gosmtp.EnhancedCode{4, 3, 1},
			Message:      "Insufficient system storage, try again later",
		}
	default:
		return &gosmtp.SMTPError{
			Code:         451,
			EnhancedCode: gosmtp.EnhancedCode{4, 3, 0},
			Message:      "Temporary failure storing the message, try again later",
		}
	}
}

// Data consumes the message and decides the reply.
//
// Every exit path here is the ack contract: nil means 250 and means the
// message is stored and survives this process dying, and any error means
// the client still owns the message. Nothing between those two states is
// expressible in SMTP, so nothing in between may be returned. The exact
// reach of that promise, and where it stops short of power loss, is on
// Ingest.Deliver.
func (s *session) Data(r io.Reader) (err error) {
	// A panic in parsing or storage must not take the process down with
	// every other in-flight connection. It becomes 451: the message was
	// not stored, and the sender still owns it.
	defer func() {
		if p := recover(); p != nil {
			s.server.logger.Error("smtp: panic handling message",
				"remote", s.remote, "panic", p)
			err = s.server.reply(fmt.Errorf("%w: panic", ErrTemporary))
		}
	}()

	raw, readErr := io.ReadAll(r)
	if readErr != nil {
		// An oversized message arrives here as go-smtp's ErrDataTooLarge,
		// which is already a 552 5.3.4 SMTPError. Returning it unchanged
		// keeps the size reply in the library that measured the size.
		var smtpErr *gosmtp.SMTPError
		if errors.As(readErr, &smtpErr) {
			return smtpErr
		}
		return s.server.reply(fmt.Errorf("%w: read: %w", ErrTemporary, readErr))
	}

	d := Delivery{
		From:       s.from,
		Recipients: append([]string(nil), s.rcpts...),
		Raw:        raw,
		ReceivedAt: s.server.now(),
	}
	return s.server.reply(s.deliverer.Deliver(s.server.context(), d))
}

// Reset discards the current transaction. The connection stays open, so
// the next MAIL FROM starts clean.
func (s *session) Reset() {
	s.from = ""
	s.rcpts = nil
}

func (s *session) Logout() error { return nil }

func remoteAddr(c *gosmtp.Conn) string {
	if c == nil {
		return ""
	}
	if a := c.Conn(); a != nil {
		return a.RemoteAddr().String()
	}
	return ""
}
