package relayconf

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/ivermin1123/authstunt/internal/extract"
)

// Status is a single rule's verdict.
//
// StatusSkipped is not a quiet pass: it means the fixture could not exercise
// the rule (no OTP in a magic-link mail, for instance). A run whose rules are
// mostly skipped has proved mostly nothing, so Report reports the count.
type Status string

// The three verdicts a rule can reach.
const (
	StatusPass    Status = "pass"
	StatusFail    Status = "fail"
	StatusSkipped Status = "skipped"
)

// Finding is one rule's outcome. Detail names what differed, never the secret
// itself: this output is meant to be pasteable into an issue.
type Finding struct {
	Rule   string
	Status Status
	Detail string
}

// Report is the whole conformance verdict for one before/after pair.
type Report struct {
	Findings []Finding
}

// OK reports whether the relay may be used. A skipped rule does not block.
func (r Report) OK() bool {
	for _, f := range r.Findings {
		if f.Status == StatusFail {
			return false
		}
	}
	return true
}

// Failures returns only the rules that failed, for a short operator summary.
func (r Report) Failures() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Status == StatusFail {
			out = append(out, f)
		}
	}
	return out
}

// Skipped counts rules the fixture could not exercise.
func (r Report) Skipped() int {
	n := 0
	for _, f := range r.Findings {
		if f.Status == StatusSkipped {
			n++
		}
	}
	return n
}

func (r Report) String() string {
	var b strings.Builder
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "%-22s %-7s %s\n", f.Rule, f.Status, f.Detail)
	}
	return b.String()
}

// Compare runs every conformance rule over a message as sent and as received
// through the relay. It is pure: no clock, no network, no relay.
func Compare(before, after Message) Report {
	var r Report
	add := func(rule string, status Status, detail string) {
		r.Findings = append(r.Findings, Finding{rule, status, detail})
	}

	beforeX := extract.Extract(extract.Input{
		Subject: before.Subject(), Text: before.Text, HTML: before.HTML,
	})
	afterX := extract.Extract(extract.Input{
		Subject: after.Subject(), Text: after.Text, HTML: after.HTML,
	})

	// 1. The OTP is compared exactly, before any canonicalization. There is no
	// benign transformation of a one-time code.
	switch beforeX.OTPBest {
	case "":
		add("otp_exact", StatusSkipped, "no OTP in the sent message")
	case afterX.OTPBest:
		add("otp_exact", StatusPass, "OTP preserved")
	default:
		add("otp_exact", StatusFail, describeOTPChange(beforeX, afterX))
	}

	// 2. Every link URL, exactly, in order. Click tracking and safe-link
	// wrappers both land here.
	compareLinks(add, beforeX, afterX)

	// 3. The link a caller is handed to complete the flow.
	switch beforeX.VerifyLinkBest {
	case "":
		add("verify_link_exact", StatusSkipped, "no verify link in the sent message")
	case afterX.VerifyLinkBest:
		add("verify_link_exact", StatusPass, "verify link preserved")
	default:
		add("verify_link_exact", StatusFail, "the best verify link changed in transit")
	}

	// 4. Identity and threading headers, after canonicalization.
	compareHeaders(add, before, after)

	// 5 and 6. Body parts, after canonicalization. One rule each, so that a
	// relay that damages only the plain-text alternative is named for it.
	comparePart(add, "text_part_preserved", before.Text, after.Text, canonicalText)
	comparePart(add, "html_part_preserved", before.HTML, after.HTML, canonicalHTML)

	// 7. The extractor must still read both parts as it would real mail. A
	// relay that drops the plain-text alternative leaves extraction working
	// only for HTML-capable paths, which is a silent narrowing.
	comparePartPresence(add, before, after)

	// 8. Whole-extraction parity: the catch-all for anything the specific
	// rules above did not name.
	if extractionEqual(beforeX, afterX) {
		add("extraction_parity", StatusPass, "extraction output identical")
	} else {
		add("extraction_parity", StatusFail,
			"extraction output differs beyond the rules above")
	}

	// 9. Hop metadata the relay added. Informational, never a failure.
	reportHopHeaders(add, before, after)
	return r
}

func describeOTPChange(before, after extract.Result) string {
	if after.OTPBest == "" {
		return "the OTP is gone from the relayed message"
	}
	if len(before.OTPBest) != len(after.OTPBest) {
		return fmt.Sprintf("OTP length changed %d -> %d",
			len(before.OTPBest), len(after.OTPBest))
	}
	return "the OTP value changed in transit"
}

func compareLinks(add func(string, Status, string), before, after extract.Result) {
	if len(before.Links) == 0 {
		add("links_exact", StatusSkipped, "no links in the sent message")
		return
	}
	beforeURLs := linkURLs(before)
	afterURLs := linkURLs(after)

	if equalStrings(beforeURLs, afterURLs) {
		add("links_exact", StatusPass,
			fmt.Sprintf("%d link(s) preserved", len(beforeURLs)))
		return
	}
	if wrapped, orig := detectWrapper(beforeURLs, afterURLs); wrapped != "" {
		add("links_exact", StatusFail, fmt.Sprintf(
			"link rewriting detected: %q is wrapped by a relay URL at %s",
			truncateURL(orig), hostOf(wrapped)))
		return
	}
	if len(beforeURLs) != len(afterURLs) {
		add("links_exact", StatusFail, fmt.Sprintf(
			"link count changed %d -> %d", len(beforeURLs), len(afterURLs)))
		return
	}
	add("links_exact", StatusFail, "at least one link URL changed in transit")
}

func linkURLs(r extract.Result) []string {
	out := make([]string, 0, len(r.Links))
	for _, l := range r.Links {
		out = append(out, l.URL)
	}
	return out
}

// detectWrapper spots the classic click-tracking shape: an original URL
// surviving inside a relay URL, plain or percent-encoded. Naming it explicitly
// matters because "a link changed" sends an operator looking at the template,
// while "your relay wraps links" sends them to the relay's settings.
func detectWrapper(before, after []string) (wrapper, original string) {
	for _, b := range before {
		if b == "" {
			continue
		}
		encoded := url.QueryEscape(b)
		for _, a := range after {
			if a == b {
				continue
			}
			if strings.Contains(a, b) || strings.Contains(a, encoded) {
				return a, b
			}
		}
	}
	return "", ""
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "an unparseable host"
	}
	return u.Host
}

// truncateURL keeps a failure message readable without leaking a whole token.
func truncateURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable url>"
	}
	return u.Scheme + "://" + u.Host + u.Path
}

func compareHeaders(add func(string, Status, string), before, after Message) {
	var changed []string
	for _, key := range preservedHeaders {
		b := canonicalHeader(strings.Join(before.Headers[key], " "))
		a := canonicalHeader(strings.Join(after.Headers[key], " "))
		if b == "" && a == "" {
			continue
		}
		if b != a {
			changed = append(changed, key)
		}
	}
	if len(changed) == 0 {
		add("headers_preserved", StatusPass,
			"From/To/Subject/Message-Id preserved")
		return
	}
	sort.Strings(changed)
	add("headers_preserved", StatusFail,
		"changed or dropped: "+strings.Join(changed, ", "))
}

func comparePart(
	add func(string, Status, string),
	rule, before, after string,
	canon func(string) string,
) {
	if strings.TrimSpace(before) == "" {
		add(rule, StatusSkipped, "part absent from the sent message")
		return
	}
	if canon(before) == canon(after) {
		add(rule, StatusPass, "semantic content preserved")
		return
	}
	if strings.TrimSpace(after) == "" {
		add(rule, StatusFail, "the relay dropped this part")
		return
	}
	add(rule, StatusFail, "content differs after canonicalization")
}

func comparePartPresence(add func(string, Status, string), before, after Message) {
	hadBoth := strings.TrimSpace(before.Text) != "" &&
		strings.TrimSpace(before.HTML) != ""
	if !hadBoth {
		add("multipart_alternative", StatusSkipped,
			"the sent message was not text+html")
		return
	}
	hasBoth := strings.TrimSpace(after.Text) != "" &&
		strings.TrimSpace(after.HTML) != ""
	if hasBoth {
		add("multipart_alternative", StatusPass, "both parts survived")
		return
	}
	add("multipart_alternative", StatusFail,
		"the relay collapsed a text+html message to a single part")
}

// reportHopHeaders is informational, never a failure: these are the headers a
// relay is supposed to add. Listing them tells an operator the message really
// did traverse the relay under test, which a clean pass alone does not.
func reportHopHeaders(add func(string, Status, string), before, after Message) {
	var added []string
	for key := range after.Headers {
		if !hopHeaders[key] {
			continue
		}
		if len(after.Headers[key]) > len(before.Headers[key]) {
			added = append(added, key)
		}
	}
	if len(added) == 0 {
		add("hop_headers", StatusSkipped, "no hop headers added")
		return
	}
	sort.Strings(added)
	add("hop_headers", StatusPass, "expected hop metadata added: "+
		strings.Join(added, ", "))
}

func extractionEqual(a, b extract.Result) bool {
	if a.OTPBest != b.OTPBest || a.VerifyLinkBest != b.VerifyLinkBest {
		return false
	}
	if !equalStrings(a.OTPCandidates, b.OTPCandidates) {
		return false
	}
	if len(a.Links) != len(b.Links) {
		return false
	}
	for i := range a.Links {
		if a.Links[i] != b.Links[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
