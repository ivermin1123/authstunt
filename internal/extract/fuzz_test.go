package extract

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// fuzzSeeds are the shapes worth starting from: each one is a place where the
// engine makes a decision, so the fuzzer mutates around a boundary rather than
// around empty input.
var fuzzSeeds = []Input{
	{},
	{Text: "Your verification code is 482913."},
	{Subject: "482913 is your code", Text: "Enter it to continue."},
	{Text: "Ma xac thuc cua ban la 728194."},
	{Text: "Mã xác thực của bạn là 728194."},
	{Text: "(c) 2026 Acme"},
	{Text: "Order 1234567890123 ref 123"},
	{Text: "code abc123456 x9876543"},
	{Text: "Confirm here: https://app.test/c/482913 - thanks."},
	{Text: "https://app.test/verify?t=aa."},
	{Text: "://nothing"},
	{Text: "www.app.test/verify?t=1 code 553311"},
	{HTML: `<a href="https://app.test/verify?t=1">Verify</a>`},
	{HTML: `<a href="javascript:alert(1)">Verify</a>`},
	{HTML: `<a href="data:text/html,x">Confirm</a>`},
	{HTML: `<a href="/verify?t=bb">Verify</a>`},
	{HTML: `<a href=https://app.test/a>one<a href="https://app.test/b">two<p>three`},
	{HTML: `<script>var x = 123456;</script><p>code 778899</p>`},
	{HTML: `<p>Your code is</p><h1>915430</h1>`},
	{HTML: `<p>code is <strong>915</strong>430</p><p>ref abc<span>123456</span></p>`},
	{HTML: `<area href="https://app.test/verify?t=1" alt="Verify">`},
	{HTML: `<a href="https://app.test/v"/>Confirm<script src="x"/>code 481920`},
	{Text: "www.app.test/verify?t=1 and https://www.app.test/verify?t=2"},
	{HTML: `<a href="https://app.test/x">`},
	{HTML: `<a>no href</a>`},
	{HTML: `<a href="https://app.test/%zz">bad escape</a>`},
	{HTML: strings.Repeat(`<a href="https://app.test/v">v</a>`, 40)},
	{Subject: "\x00\x01", Text: "\xff\xfe invalid utf8 482913"},
	{Text: strings.Repeat("1", 64)},
	{Text: strings.Repeat("code 123456 ", 20)},
}

// FuzzExtract asserts the invariants the rest of the system is built on, so a
// crash is not the only thing the fuzzer can find.
//
// Extract is handed attacker-influenced text: anyone who can send mail to a
// catch-all inbox controls every byte of it.
func FuzzExtract(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s.Subject, s.Text, s.HTML)
	}
	f.Fuzz(func(t *testing.T, subject, text, htmlBody string) {
		if oversized(subject, text, htmlBody) {
			return
		}
		got := Extract(Input{Subject: subject, Text: text, HTML: htmlBody})

		// The JSON contract: arrays are arrays, never null.
		if got.OTPCandidates == nil {
			t.Error("otp_candidates is nil, which marshals to null")
		}
		if got.Links == nil {
			t.Error("links is nil, which marshals to null")
		}

		seen := make(map[string]struct{}, len(got.Links))
		for _, l := range got.Links {
			if l.URL == "" {
				t.Error("an empty URL was reported as a link")
			}
			if _, dup := seen[l.URL]; dup {
				t.Errorf("link %q reported twice", l.URL)
			}
			seen[l.URL] = struct{}{}

			switch l.Kind {
			case KindVerify, KindMagic, KindReset, KindOther:
			default:
				t.Errorf("link %q carries kind %q, which is outside the contract", l.URL, l.Kind)
			}

			// The security invariant. Nothing outside the allowlist may ever
			// come back as something a caller is allowed to open, whatever the
			// input did to get there.
			if l.Actionable {
				u, err := url.Parse(l.URL)
				if err != nil {
					t.Errorf("actionable link %q does not parse", l.URL)
					continue
				}
				if s := strings.ToLower(u.Scheme); s != "http" && s != "https" {
					t.Errorf("actionable link %q has scheme %q", l.URL, s)
				}
				if u.Host == "" {
					t.Errorf("actionable link %q has no host", l.URL)
				}
			}
		}

		// The best link is always one of the reported ones, and always
		// actionable. A caller acts on this field directly.
		if got.VerifyLinkBest != "" {
			found := false
			for _, l := range got.Links {
				if l.URL == got.VerifyLinkBest {
					found = true
					if !l.Actionable {
						t.Errorf("verify_link_best %q is not actionable", l.URL)
					}
					if l.Kind != KindVerify && l.Kind != KindMagic {
						t.Errorf("verify_link_best %q has kind %q", l.URL, l.Kind)
					}
				}
			}
			if !found {
				t.Errorf("verify_link_best %q is not in links", got.VerifyLinkBest)
			}
		}

		// Candidates are digits of a legal length, unique, and otp_best is one
		// of them. A code that is not in the candidate list would mean the two
		// fields disagree about what was found.
		candidates := make(map[string]struct{}, len(got.OTPCandidates))
		for _, c := range got.OTPCandidates {
			if len(c) < minOTPLen || len(c) > maxOTPLen {
				t.Errorf("candidate %q has length %d, outside %d-%d", c, len(c), minOTPLen, maxOTPLen)
			}
			for i := 0; i < len(c); i++ {
				if !isASCIIDigit(c[i]) {
					t.Errorf("candidate %q is not all digits", c)
					break
				}
			}
			if _, dup := candidates[c]; dup {
				t.Errorf("candidate %q reported twice", c)
			}
			candidates[c] = struct{}{}
		}
		if got.OTPBest != "" {
			if _, ok := candidates[got.OTPBest]; !ok {
				t.Errorf("otp_best %q is not among the candidates", got.OTPBest)
			}
		}
	})
}

// FuzzExtractIsIdempotentUnderNormalization pins the property the whole
// Vietnamese story rests on: the composed and decomposed spellings of the same
// text are the same message, so they must extract identically. This is the
// invariant that a future change to fold or to the keyword lists could break
// without any single fixture noticing.
func FuzzExtractIsIdempotentUnderNormalization(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s.Subject, s.Text, s.HTML)
	}
	f.Fuzz(func(t *testing.T, subject, text, htmlBody string) {
		if oversized(subject, text, htmlBody) {
			return
		}
		in := Input{Subject: subject, Text: text, HTML: htmlBody}
		nfd := Input{
			Subject: nfdString(subject),
			Text:    nfdString(text),
			HTML:    nfdString(htmlBody),
		}
		before, after := Extract(in), Extract(nfd)
		// The whole result, field by field and in order. Counting candidates
		// and links instead would let the values, the kinds, the actionable
		// flags, or the ranking change while the property still passed, which
		// is exactly the kind of drift this is here to catch.
		if before.OTPBest != after.OTPBest {
			t.Errorf("otp_best changed under decomposition: %q then %q", before.OTPBest, after.OTPBest)
		}
		if !slices.Equal(before.OTPCandidates, after.OTPCandidates) {
			t.Errorf("otp_candidates changed under decomposition: %v then %v", before.OTPCandidates, after.OTPCandidates)
		}
		if before.VerifyLinkBest != after.VerifyLinkBest {
			t.Errorf("verify_link_best changed under decomposition: %q then %q", before.VerifyLinkBest, after.VerifyLinkBest)
		}
		if !slices.Equal(before.Links, after.Links) {
			t.Errorf("links changed under decomposition: %+v then %+v", before.Links, after.Links)
		}
	})
}

// oversized keeps the fuzzer off inputs large enough that its own minimizer
// spends minutes shrinking one of them, which showed up as the exec rate
// dropping to zero for a minute at a time.
//
// Nothing is lost by it. Size is not the interesting dimension here: what a
// large body costs is measured on the shapes that actually stress the engine by
// TestExtractSurvivesAdversarialBodies, which runs them at 2MB as its committed
// gate and states there what the full 10MB limit measured separately. This
// budget is better spent on the small malformed inputs only a fuzzer thinks of.
func oversized(parts ...string) bool {
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	return total > 32<<10
}

// nfdString is the decomposed form, spelled out here rather than inlined so
// the property above reads as one idea.
func nfdString(s string) string { return norm.NFD.String(s) }
