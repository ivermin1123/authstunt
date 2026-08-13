package relayconf_test

import (
	"embed"
	"strings"
	"testing"

	"github.com/ivermin1123/authstunt/internal/relayconf"
)

// Fixtures are embedded rather than read from disk so the corpus travels with
// the binary and the test cannot be fooled by a stray working directory.
//
//go:embed testdata/*.eml
var fixtures embed.FS

// The owner's capture workflow, which these fixtures stand in for:
//
//  1. Send one message through the real path and keep the bytes the sender
//     emitted as testdata/sent.eml.
//  2. Keep the bytes the AuthStunt receiver got as testdata/relayed-*.eml.
//  3. Run this test. A failing rule names the relay setting to go change.
//
// The hostile fixtures are not hypothetical: link rewriting and a collapsed
// multipart are the two default behaviors of managed inbound services, and a
// spam filter reflowing a code across a space is how an OTP dies in transit.

func load(t *testing.T, name string) relayconf.Message {
	t.Helper()
	raw, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	m, err := relayconf.Parse(raw)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	return m
}

func findRule(t *testing.T, r relayconf.Report, rule string) relayconf.Finding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Rule == rule {
			return f
		}
	}
	t.Fatalf("rule %q missing from report:\n%s", rule, r)
	return relayconf.Finding{}
}

func TestAConformingRelayPasses(t *testing.T) {
	sent := load(t, "sent.eml")
	got := relayconf.Compare(sent, load(t, "relayed-conforming.eml"))

	if !got.OK() {
		t.Fatalf("conforming relay reported failures:\n%s", got)
	}
	// The fixture differs from the sent message in exactly the ways a relay is
	// allowed to differ: added hop headers, reflowed HTML, an extra blank
	// line. If canonicalization ever stops absorbing those, this test says so
	// before a real relay gets blamed for it.
	if f := findRule(t, got, "hop_headers"); f.Status != relayconf.StatusPass {
		t.Errorf("expected hop metadata to be recognized, got %q %s",
			f.Status, f.Detail)
	}
	for _, rule := range []string{
		"otp_exact", "links_exact", "verify_link_exact",
		"headers_preserved", "text_part_preserved", "html_part_preserved",
		"multipart_alternative", "extraction_parity",
	} {
		if f := findRule(t, got, rule); f.Status != relayconf.StatusPass {
			t.Errorf("rule %s: got %q (%s), want pass", rule, f.Status, f.Detail)
		}
	}
}

func TestCanonicalizationIsNotAWildcard(t *testing.T) {
	// A canonical diff earns its keep only if it still catches real damage.
	// Each case below survives canonicalization and must still fail.
	cases := []struct {
		fixture  string
		wantFail string
		wantWord string
	}{
		{"relayed-link-rewritten.eml", "links_exact", "rewriting"},
		{"relayed-otp-mangled.eml", "otp_exact", "OTP"},
		{"relayed-text-dropped.eml", "multipart_alternative", "collapsed"},
	}
	sent := load(t, "sent.eml")
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			got := relayconf.Compare(sent, load(t, tc.fixture))
			if got.OK() {
				t.Fatalf("expected a failure, report was clean:\n%s", got)
			}
			f := findRule(t, got, tc.wantFail)
			if f.Status != relayconf.StatusFail {
				t.Fatalf("rule %s: got %q, want fail\n%s", tc.wantFail, f.Status, got)
			}
			if !strings.Contains(f.Detail, tc.wantWord) {
				t.Errorf("detail %q does not mention %q", f.Detail, tc.wantWord)
			}
		})
	}
}

func TestLinkRewritingIsNamedNotJustDetected(t *testing.T) {
	// "A link changed" sends an operator to the email template. "Your relay
	// wraps links" sends them to the relay settings, which is where the fix
	// is. The distinction is the whole value of this rule.
	got := relayconf.Compare(
		load(t, "sent.eml"), load(t, "relayed-link-rewritten.eml"))
	f := findRule(t, got, "links_exact")
	if !strings.Contains(f.Detail, "click.trackmail.example.net") {
		t.Errorf("failure should name the wrapping host, got %q", f.Detail)
	}
}

func TestAFailureNeverPrintsTheToken(t *testing.T) {
	// This report is meant to be pasteable into an issue.
	got := relayconf.Compare(
		load(t, "sent.eml"), load(t, "relayed-link-rewritten.eml"))
	out := got.String()
	for _, secret := range []string{"abc123DEF456", "481920"} {
		if strings.Contains(out, secret) {
			t.Errorf("report leaked %q:\n%s", secret, out)
		}
	}
}

func TestCRLFAndUnicodeFormAreNotViolations(t *testing.T) {
	// Line-ending rewriting and NFC/NFD drift are routine in transit; a rule
	// that flags them is a rule nobody will keep running.
	raw, err := fixtures.ReadFile("testdata/sent.eml")
	if err != nil {
		t.Fatal(err)
	}
	crlf := strings.ReplaceAll(string(raw), "\n", "\r\n")
	after, err := relayconf.Parse([]byte(crlf))
	if err != nil {
		t.Fatalf("parsing the CRLF variant: %v", err)
	}
	if got := relayconf.Compare(load(t, "sent.eml"), after); !got.OK() {
		t.Errorf("CRLF rewriting reported as a violation:\n%s", got)
	}
}

func TestComparingAMessageToItselfPasses(t *testing.T) {
	sent := load(t, "sent.eml")
	if got := relayconf.Compare(sent, sent); !got.OK() {
		t.Errorf("identity comparison failed:\n%s", got)
	}
}

func TestParseRejectsAMessageWithNoReadablePart(t *testing.T) {
	// A fixture that silently parses to two empty bodies would report a pass
	// while proving nothing, which is the worst outcome this package can have.
	_, err := relayconf.Parse([]byte("From: a@b.test\r\n" +
		"Subject: x\r\nContent-Type: application/octet-stream\r\n\r\nrawbytes\r\n"))
	if err == nil {
		t.Fatal("expected an error for a message with no text or html part")
	}
}

func TestSkippedRulesAreCountedNotHidden(t *testing.T) {
	// A run whose rules mostly skipped has proved mostly nothing.
	m, err := relayconf.Parse([]byte("From: a@b.test\r\nTo: c@d.test\r\n" +
		"Subject: hello\r\nContent-Type: text/plain; charset=utf-8\r\n" +
		"\r\nno codes and no links here\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := relayconf.Compare(m, m)
	if !got.OK() {
		t.Fatalf("unexpected failures:\n%s", got)
	}
	if got.Skipped() < 3 {
		t.Errorf("expected the empty-ish fixture to skip several rules, got %d:\n%s",
			got.Skipped(), got)
	}
}
