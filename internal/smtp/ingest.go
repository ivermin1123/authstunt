package smtp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"syscall"

	// Importing charset for its side effect installs message.CharsetReader,
	// which is what lets a windows-1258 or ISO-8859-1 body decode instead
	// of surfacing as an unknown-charset error. It costs about 1MiB of
	// binary size for the full table, taken deliberately: Vietnamese mail
	// in a legacy encoding is a first-class case for this product.
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"

	"github.com/ivermin1123/authstunt/internal/extract"
	"github.com/ivermin1123/authstunt/internal/ledger"
	"github.com/ivermin1123/authstunt/internal/sse"
	"github.com/ivermin1123/authstunt/internal/store"
)

// EventMessage is the bus event name for a stored, extracted message.
//
// The event's payload shape is deliberately the smallest thing that
// identifies a message. The wire contract - what a dashboard or an SSE
// client actually receives - is frozen with the REST surface in phase 4,
// and anything richer written here would be a contract nobody approved.
const EventMessage = "message"

// Ingest is the pipeline behind the SMTP session: MIME to store to
// extraction to ledger to bus.
//
// The project is injected once at construction (design 4.2 item 16). The
// pipeline never asks the database which project a message belongs to,
// because a server serving one project has exactly one answer and a lookup
// per message would be a chance to get it wrong.
type Ingest struct {
	store     *store.Store
	bus       *sse.Bus
	projectID string
	allowlist []string
	logger    *slog.Logger

	// queue is bounded, and that bound is the backpressure contract: when
	// extraction falls behind, Deliver blocks, the SMTP session stops
	// reading, and TCP tells the sender to slow down. Mail is never
	// dropped to keep the queue short.
	queue   chan job
	workers int
	wg      sync.WaitGroup

	// extractFn is the extraction engine, held as a field so a test can
	// substitute one that panics. The panic path is a real requirement -
	// one malformed message must not take the server down - and a
	// requirement with no way to test it is a claim, not a guarantee.
	extractFn func(extract.Input) extract.Result
}

// job is one message waiting for extraction, carrying everything the
// worker needs so it never has to read the row back.
type job struct {
	msg store.Message
	in  extract.Input
}

// IngestConfig collects the pipeline's dependencies.
type IngestConfig struct {
	Store     *store.Store
	Bus       *sse.Bus
	ProjectID string
	// Allowlist is the project's stored domain patterns, already
	// canonical. A message whose envelope touches anything outside them is
	// quarantined.
	Allowlist []string
	Logger    *slog.Logger
	// Workers defaults to GOMAXPROCS.
	Workers int
	// QueueSize defaults to Workers, so a burst is absorbed but a stall
	// reaches the sender quickly rather than after a long invisible
	// backlog.
	QueueSize int
}

// NewIngest validates the configuration and returns a pipeline that is not
// yet running. Call Start.
func NewIngest(cfg IngestConfig) (*Ingest, error) {
	if cfg.Store == nil {
		return nil, errors.New("smtp: ingest needs a store")
	}
	if cfg.Bus == nil {
		return nil, errors.New("smtp: ingest needs a bus")
	}
	if cfg.ProjectID == "" {
		return nil, errors.New("smtp: ingest needs a project id")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Workers <= 0 {
		cfg.Workers = runtime.GOMAXPROCS(0)
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = cfg.Workers
	}
	return &Ingest{
		store:     cfg.Store,
		bus:       cfg.Bus,
		projectID: cfg.ProjectID,
		allowlist: append([]string(nil), cfg.Allowlist...),
		logger:    cfg.Logger,
		queue:     make(chan job, cfg.QueueSize),
		workers:   cfg.Workers,
		extractFn: extract.Extract,
	}, nil
}

// Start launches the extraction workers. Stop drains them.
func (i *Ingest) Start(ctx context.Context) {
	for range i.workers {
		i.wg.Add(1)
		go func() {
			defer i.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case j, ok := <-i.queue:
					if !ok {
						return
					}
					i.settle(ctx, j)
				}
			}
		}()
	}
}

// Stop closes the queue and waits for the workers to finish what they
// already took. It must be called after the SMTP listener has stopped
// accepting, or a session could publish onto a closed channel.
func (i *Ingest) Stop() {
	close(i.queue)
	i.wg.Wait()
}

// Deliver stores one message and hands it to extraction.
//
// The order here is the ack contract and is not an implementation detail:
// blobs are written and synced, the row commits with a durable pending
// extraction state, and only then does this return nil and the session
// answer 250. Everything after the commit - the ledger event, the queue
// handoff - cannot un-store the message, so none of it may turn into a
// refusal.
func (i *Ingest) Deliver(ctx context.Context, d Delivery) error {
	parsed := parseMessage(d.Raw, i.logger)

	rawRef, err := i.store.Blobs.Put(d.Raw)
	if err != nil {
		return fmt.Errorf("%w: raw blob: %w", classifyWrite(err), err)
	}
	htmlRef := ""
	if parsed.html != "" {
		htmlRef, err = i.store.Blobs.Put([]byte(parsed.html))
		if err != nil {
			return fmt.Errorf("%w: html blob: %w", classifyWrite(err), err)
		}
	}

	quarantined, offList := i.quarantine(d.Recipients)

	msg := store.Message{
		ProjectID:   i.projectID,
		FromAddr:    fromAddress(parsed.from, d.From),
		Subject:     parsed.subject,
		Channel:     store.ChannelEmail,
		RawRef:      rawRef,
		HTMLRef:     htmlRef,
		TextBody:    parsed.text,
		Quarantined: quarantined,
		ReceivedAt:  d.ReceivedAt,
		Recipients:  recipients(d.Recipients, parsed.to, parsed.cc),
	}
	// The row, its recipients and the events describing how it arrived
	// commit together. A message with no arrival trail, or a trail for a
	// message that is not there, would each be a lie the evidence path
	// cannot detect afterwards.
	var stored store.Message
	err = i.store.WithTx(ctx, func(tx *store.Tx) error {
		var err error
		stored, err = tx.InsertMessage(ctx, msg)
		if err != nil {
			return err
		}
		return i.audit(ctx, tx, stored, d.From, quarantined, offList)
	})
	if err != nil {
		return fmt.Errorf("%w: insert: %w", classifyWrite(err), err)
	}

	select {
	case i.queue <- job{msg: stored, in: parsed.input()}:
	case <-ctx.Done():
		// Shutdown caught the message between commit and extraction.
		// Startup recovery owns it now, which is exactly the case that
		// scan was written for, so the sender still gets its 250.
		i.logger.Warn("smtp: shutting down with a message left pending",
			"message_id", stored.ID)
	}
	return nil
}

// Recover completes messages that were acked but never extracted, which is
// what an application crash between the commit and the worker leaves
// behind.
//
// It scans pending rows only. A terminal row - including a failed one - is
// never revisited, so a message that reliably kills extraction cannot be
// retried forever.
func (i *Ingest) Recover(ctx context.Context) error {
	pending, err := i.store.ListPendingExtractions(ctx, 0)
	if err != nil {
		return fmt.Errorf("smtp: recover: %w", err)
	}
	for _, m := range pending {
		in, err := i.reread(m)
		if err != nil {
			// The row cannot be extracted and never will be. Settling it
			// failed keeps recovery from meeting it again on every boot.
			i.logger.Error("smtp: recovery cannot read a pending message",
				"message_id", m.ID, "error", err)
			i.fail(ctx, m, err)
			continue
		}
		i.settle(ctx, job{msg: m, in: in})
	}
	if len(pending) > 0 {
		i.logger.Info("smtp: completed pending extractions", "count", len(pending))
	}
	return nil
}

// reread rebuilds the extractor's input for a row recovery found.
func (i *Ingest) reread(m store.Message) (extract.Input, error) {
	if m.Unreadable {
		return extract.Input{}, errors.New("sealed payload does not authenticate")
	}
	in := extract.Input{Subject: m.Subject, Text: m.TextBody}
	if m.HTMLRef != "" {
		html, err := i.store.Blobs.Get(m.HTMLRef)
		if err != nil {
			return extract.Input{}, fmt.Errorf("html blob: %w", err)
		}
		in.HTML = string(html)
	}
	return in, nil
}

// settle runs extraction to exactly one terminal state and publishes after
// that state commits, so a long-poll match always carries the outcome.
func (i *Ingest) settle(ctx context.Context, j job) {
	res, err := runExtract(i.extractFn, j.in)
	if err != nil {
		i.logger.Error("smtp: extraction failed",
			"message_id", j.msg.ID, "error", err)
		i.fail(ctx, j.msg, err)
		return
	}

	encoded, err := json.Marshal(res)
	if err != nil {
		i.fail(ctx, j.msg, fmt.Errorf("encode extraction: %w", err))
		return
	}
	if err := i.store.SetExtraction(ctx, j.msg.ID, string(encoded)); err != nil {
		if errors.Is(err, store.ErrExtractionSettled) {
			// Recovery and a worker raced for the same row and the other
			// one won. The winner published; publishing again would
			// deliver the message twice.
			return
		}
		i.logger.Error("smtp: storing extraction failed",
			"message_id", j.msg.ID, "error", err)
		i.fail(ctx, j.msg, err)
		return
	}
	i.publish(ctx, j.msg)
}

// runExtract isolates the panic boundary. Extraction is a pure function
// over attacker-supplied bytes, which is exactly where a parser bug
// surfaces, and one malformed message must not take down a server holding
// other connections.
func runExtract(fn func(extract.Input) extract.Result, in extract.Input) (res extract.Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("extraction panicked: %v", p)
		}
	}()
	return fn(in), nil
}

// fail commits the failed terminal state, records why, and publishes.
//
// The publish matters: a caller waiting on this message must learn that it
// arrived and could not be read, rather than waiting out the full timeout
// on a message that is already settled.
func (i *Ingest) fail(ctx context.Context, m store.Message, cause error) {
	if err := i.store.FailExtraction(ctx, m.ID); err != nil {
		if errors.Is(err, store.ErrExtractionSettled) {
			return
		}
		i.logger.Error("smtp: settling failed extraction failed",
			"message_id", m.ID, "error", err)
		return
	}
	if _, err := ledger.Write(ctx, i.store, ledger.Meta{ProjectID: i.projectID},
		ledger.ExtractionFailed{MessageID: m.ID, Reason: cause.Error()}); err != nil {
		i.logger.Error("smtp: appending a ledger entry failed",
			"action", ledger.ActionExtractionFail, "error", err)
	}
	i.publish(ctx, m)
}

// publish announces a settled message.
func (i *Ingest) publish(ctx context.Context, m store.Message) {
	payload := detail(map[string]string{"id": m.ID, "project_id": m.ProjectID})
	err := i.bus.Publish(ctx, sse.Publication{
		Name: EventMessage,
		Data: []byte(payload),
		Message: &sse.MessageRef{
			ID:         m.ID,
			ProjectID:  m.ProjectID,
			Subject:    m.Subject,
			Recipients: envelopeAddrs(m.Recipients),
			ReceivedAt: m.ReceivedAt,
		},
	})
	if err != nil {
		i.logger.Error("smtp: publishing a message event failed",
			"message_id", m.ID, "error", err)
	}
}

// audit writes the arrival trail: what came in, from which envelope
// sender, and whether it was held back.
//
// It runs inside the caller's transaction, so a failure here rolls the
// message back rather than leaving a stored message nobody can account
// for. That is the opposite of the phase 1 behavior, where the store had
// no seam to compose the two writes and a failed ledger append was only
// logged.
func (i *Ingest) audit(ctx context.Context, tx *store.Tx, m store.Message, envelopeFrom string, quarantined bool, offList []string) error {
	if _, err := ledger.Write(ctx, tx, ledger.Meta{ProjectID: i.projectID}, ledger.MailReceived{
		MessageID: m.ID,
		// The envelope sender is recorded because from_addr holds the
		// header From a reader sees, and the two differ on most real
		// provider mail.
		EnvelopeFrom: ledger.Addr(envelopeFrom),
	}); err != nil {
		return err
	}
	if !quarantined {
		return nil
	}
	_, err := ledger.Write(ctx, tx, ledger.Meta{ProjectID: i.projectID}, ledger.MailQuarantined{
		MessageID:  m.ID,
		Recipients: ledger.Addrs(offList),
	})
	return err
}

// quarantine reports whether the message must be held back, and which
// addresses caused it.
//
// The rule is any, not all: a message carrying one address this project
// owns and one it does not is the dangerous case, not the safe one. It is
// how a staging app with a real customer address in copy would otherwise
// hand that customer's mail to an automated reader.
func (i *Ingest) quarantine(envelope []string) (bool, []string) {
	var off []string
	for _, addr := range envelope {
		if !store.AllowlistMatches(i.allowlist, addr) {
			off = append(off, addr)
		}
	}
	return len(off) > 0, off
}

// classifyWrite decides which temporary reply a storage failure earns.
func classifyWrite(err error) error {
	if isOutOfSpace(err) {
		return ErrOutOfSpace
	}
	return ErrTemporary
}

// Windows error codes for a full disk. Neither maps onto ENOSPC, so a
// server there would report every full disk as a generic failure unless
// they are named.
const (
	errorHandleDiskFull syscall.Errno = 39
	errorDiskFull       syscall.Errno = 112
)

// isOutOfSpace reports whether a write failed because the disk is full.
//
// This is the only storage failure with its own SMTP reply, so it is the
// only one worth telling apart. Everything else is temporary in the same
// undifferentiated way.
func isOutOfSpace(err error) bool {
	if errors.Is(err, syscall.ENOSPC) {
		return true
	}
	var errno syscall.Errno
	if runtime.GOOS == "windows" && errors.As(err, &errno) {
		return errno == errorDiskFull || errno == errorHandleDiskFull
	}
	return false
}

// parsedMessage is what MIME parsing recovered. Every field is best
// effort: a message that parses badly is still stored, because a message
// the tool refused to keep is a message an operator cannot debug.
type parsedMessage struct {
	subject string
	from    string
	to      []string
	cc      []string
	text    string
	html    string
}

func (p parsedMessage) input() extract.Input {
	return extract.Input{Subject: p.subject, Text: p.text, HTML: p.html}
}

// parseMessage reads the MIME structure, and never fails.
//
// Malformed mail is the normal case for this product: the whole point is
// to catch what an application under test actually sent, including the
// broken templates. So parsing collects what it can and reports the rest
// to the log; the raw bytes are stored either way and remain the record of
// what arrived.
func parseMessage(raw []byte, logger *slog.Logger) parsedMessage {
	var p parsedMessage
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		logger.Warn("smtp: message headers did not parse", "error", err)
		return p
	}
	defer func() { _ = r.Close() }()

	p.subject = headerSubject(r.Header)
	p.from = firstAddress(r.Header, "From")
	p.to = addressList(r.Header, "To")
	p.cc = addressList(r.Header, "Cc")

	for {
		part, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		// An unknown charset is reported alongside a usable part, not
		// instead of one, so the part is still read. Treating this error
		// as fatal would drop the body of any message in a legacy
		// encoding - windows-1258 Vietnamese mail, for one.
		if err != nil && part == nil {
			logger.Warn("smtp: message part did not parse", "error", err)
			break
		}
		if err != nil {
			logger.Warn("smtp: reading a part with an unusable charset",
				"error", err)
		}
		inline, ok := part.Header.(*mail.InlineHeader)
		if !ok {
			continue // an attachment; extraction has no use for it
		}
		body, err := io.ReadAll(part.Body)
		if err != nil {
			logger.Warn("smtp: reading a part body failed", "error", err)
			continue
		}
		contentType, _, err := inline.ContentType()
		if err != nil {
			logger.Warn("smtp: part content type did not parse", "error", err)
			continue
		}
		switch {
		case strings.HasPrefix(contentType, "text/html") && p.html == "":
			p.html = string(body)
		case strings.HasPrefix(contentType, "text/plain") && p.text == "":
			p.text = string(body)
		}
	}
	return p
}

func headerSubject(h mail.Header) string {
	subject, err := h.Subject()
	if err != nil {
		// A subject that fails to decode is still worth keeping in the
		// shape it arrived: the extractor scores codes in subjects.
		return h.Get("Subject")
	}
	return subject
}

func firstAddress(h mail.Header, key string) string {
	list, err := h.AddressList(key)
	if err != nil || len(list) == 0 {
		return ""
	}
	return list[0].Address
}

func addressList(h mail.Header, key string) []string {
	list, err := h.AddressList(key)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.Address)
	}
	return out
}

// fromAddress applies the frozen rule: the header From is what an operator
// sees, so it wins; the envelope sender is the fallback and is recorded in
// the ledger regardless.
func fromAddress(header, envelope string) string {
	if header != "" {
		return store.NormalizeAddress(header)
	}
	return store.NormalizeAddress(envelope)
}

// recipients builds the stored recipient rows.
//
// Envelope entries are the ones every matcher runs on, and they are the
// only ones that can be trusted: a Bcc recipient appears in no header at
// all, and a header To can say anything. The header lists are kept because
// a human debugging a delivery wants to see what the message claimed.
func recipients(envelope, to, cc []string) []store.Recipient {
	out := make([]store.Recipient, 0, len(envelope)+len(to)+len(cc))
	for _, addr := range envelope {
		out = append(out, store.Recipient{Addr: addr, Kind: store.RecipientEnvelope})
	}
	for _, addr := range to {
		out = append(out, store.Recipient{Addr: addr, Kind: store.RecipientTo})
	}
	for _, addr := range cc {
		out = append(out, store.Recipient{Addr: addr, Kind: store.RecipientCC})
	}
	return out
}

// envelopeAddrs pulls the envelope recipients back out for the bus, which
// matches on those alone.
func envelopeAddrs(rcpts []store.Recipient) []string {
	var out []string
	for _, r := range rcpts {
		if r.Kind == store.RecipientEnvelope {
			out = append(out, r.Addr)
		}
	}
	return out
}

// detail encodes a ledger detail object. The map is small and built here,
// so an encoding failure is not a real branch; an empty object keeps the
// column valid JSON if it ever happened.
func detail(fields map[string]string) string {
	b, err := json.Marshal(fields)
	if err != nil {
		return "{}"
	}
	return string(b)
}
