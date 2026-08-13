package relayconf

import (
	"bytes"
	"errors"
	"io"
	"net/textproto"
	"regexp"
	"strings"

	"github.com/emersion/go-message/mail"
	"golang.org/x/text/unicode/norm"
)

// Message is one capture of a mail message: either what the sender emitted or
// what arrived after the relay. Headers keeps every field, including the ones
// the comparison ignores, because an operator debugging a failure needs to see
// what the relay added and not only that something changed.
type Message struct {
	Headers map[string][]string
	Text    string
	HTML    string
}

// hopHeaders change legitimately in transit. Comparing them would fail every
// conforming relay, so they are excluded from the header rule and reported
// separately as informational.
var hopHeaders = map[string]bool{
	"Received":                   true,
	"Return-Path":                true,
	"Delivered-To":               true,
	"Authentication-Results":     true,
	"Arc-Authentication-Results": true,
	"Arc-Message-Signature":      true,
	"Arc-Seal":                   true,
	"Dkim-Signature":             true,
	"X-Received":                 true,
	"Content-Transfer-Encoding":  true,
	"Mime-Version":               true,
}

// preservedHeaders must survive the relay unchanged. From/To/Subject are what
// the recipient and the extractor read; Message-ID is what correlates a claim
// back to the send that produced it, so a relay that reissues it breaks
// correlation even when the body is perfect.
var preservedHeaders = []string{"From", "To", "Subject", "Message-Id"}

// Parse reads a raw RFC 5322 message into the shape the comparison works on.
//
// Unlike the ingest path, which is deliberately best-effort because catching
// broken application mail is the product, this returns an error: a fixture
// that does not parse is a broken fixture, and silently comparing two empty
// bodies would report a pass.
func Parse(raw []byte) (Message, error) {
	m := Message{Headers: map[string][]string{}}

	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return Message{}, err
	}
	defer func() { _ = r.Close() }()

	fields := r.Header.Fields()
	for fields.Next() {
		key := textproto.CanonicalMIMEHeaderKey(fields.Key())
		value, err := fields.Text()
		if err != nil {
			// An undecodable header is kept in its raw form rather than
			// dropped: a relay that mangles the encoding of a Subject is
			// exactly the kind of finding this package exists to surface.
			value = fields.Value()
		}
		m.Headers[key] = append(m.Headers[key], value)
	}

	var sawPart bool
	for {
		part, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil && part == nil {
			return Message{}, err
		}
		inline, ok := part.Header.(*mail.InlineHeader)
		if !ok {
			continue // an attachment; the extractor has no use for it
		}
		body, err := io.ReadAll(part.Body)
		if err != nil {
			return Message{}, err
		}
		contentType, _, err := inline.ContentType()
		if err != nil {
			return Message{}, err
		}
		switch {
		case strings.HasPrefix(contentType, "text/html") && m.HTML == "":
			m.HTML = string(body)
			sawPart = true
		case strings.HasPrefix(contentType, "text/plain") && m.Text == "":
			m.Text = string(body)
			sawPart = true
		}
	}
	if !sawPart {
		return Message{}, errors.New("relayconf: message has no text or html part")
	}
	return m, nil
}

// Subject returns the decoded Subject header, or the empty string.
func (m Message) Subject() string {
	if v := m.Headers["Subject"]; len(v) > 0 {
		return v[0]
	}
	return ""
}

var (
	// Whitespace that carries no meaning in HTML: between two tags.
	interTagSpace = regexp.MustCompile(`>\s+<`)
	trailingSpace = regexp.MustCompile(`[ \t]+\n`)
	manyNewlines  = regexp.MustCompile(`\n{3,}`)
)

// canonicalText collapses the differences a conforming relay is allowed to
// introduce, and nothing else.
//
// Line endings, trailing whitespace and Unicode normal form are all rewritten
// in transit by ordinary infrastructure. Words are not. Everything below is
// reversible in meaning; nothing here can hide a changed OTP or a rewritten
// URL, both of which are compared separately and exactly.
func canonicalText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = norm.NFC.String(s)
	s = trailingSpace.ReplaceAllString(s, "\n")
	s = manyNewlines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// canonicalHTML additionally drops whitespace between tags, which templating
// engines and relays both reflow without changing what is rendered.
func canonicalHTML(s string) string {
	s = canonicalText(s)
	return interTagSpace.ReplaceAllString(s, "><")
}

// canonicalHeader normalizes a header value for comparison. Header folding and
// the whitespace it introduces are a transport detail.
func canonicalHeader(s string) string {
	return strings.Join(strings.Fields(norm.NFC.String(s)), " ")
}
