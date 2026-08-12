package extract

import (
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Link kinds, classified from the URL and the anchor text (Brief 6.3).
//
// KindOther exists because every link is recorded whatever it turns out to be:
// a message whose unrecognized links silently vanished would look exactly like
// one that had none, and the dashboard could not show an operator what the
// mail actually contained.
const (
	KindVerify = "verify"
	KindMagic  = "magic"
	KindReset  = "reset"
	KindOther  = "other"
)

// Input is one message as the extractor reads it. Every field is optional; an
// SMS arrives with Text alone and plenty of mail carries no plain-text part.
type Input struct {
	Subject string
	Text    string
	HTML    string
}

// Link is one extracted URL.
//
// Actionable is separate from Kind because the two answer different questions.
// Kind is what the link appears to be for; Actionable is whether anything is
// allowed to follow it, which under design 3.18 means an http or https scheme
// and nothing else. A javascript: href is still reported, with Actionable
// false, so an operator can see it was there.
type Link struct {
	URL        string `json:"url"`
	Kind       string `json:"kind"`
	Actionable bool   `json:"actionable"`
}

// Result is the extraction output stored in extracted_json (Brief 6.2).
//
// Both slices are always non-nil, so the JSON carries [] rather than null and
// a consumer never has to tell "no candidates" apart from "no field". Missing
// single values are the empty string for the same reason.
type Result struct {
	OTPBest        string   `json:"otp_best"`
	OTPCandidates  []string `json:"otp_candidates"`
	Links          []Link   `json:"links"`
	VerifyLinkBest string   `json:"verify_link_best"`
}

// Extract runs the whole engine over one message. It is a pure function: no
// clock, no store, no network, so the corpus can pin its behavior exactly.
//
// Everything is normalized to NFC first. Real mail arrives in NFC and NFD
// both, and the two are indistinguishable on screen while being different
// byte strings, so matching "mã xác thực" without normalizing silently fails
// for a large share of the accented Vietnamese templates this is for.
func Extract(in Input) Result {
	subject := norm.NFC.String(in.Subject)
	text := norm.NFC.String(in.Text)
	htmlBody := norm.NFC.String(in.HTML)

	hrefs, visible := scanHTML(htmlBody)

	// The plain sources in a fixed order, so a candidate's position - and
	// therefore every tie-break below - is deterministic. Joining them also
	// lets a subject line score a code that appears in the body, which is how
	// "Subject: your verification code" plus a bare number in the body reads.
	plain := strings.Join([]string{subject, text, visible}, "\n")

	links := collectLinks(hrefs, plain)

	// Two views of the same text, each used for what it is good at. The folded
	// view drops case and diacritics so one keyword list matches both the
	// accented spelling and the way people type it without accents; it cannot
	// be used for URLs, because folding would corrupt a URL that contains
	// non-ASCII. The NFC view keeps URLs exactly as written.
	folded := fold(plain)
	candidates := otpCandidates(folded, urlSpans(folded))

	result := Result{
		OTPCandidates: make([]string, 0, len(candidates)),
		Links:         links,
	}
	for _, c := range candidates {
		result.OTPCandidates = append(result.OTPCandidates, c.code)
		// The ranking is score first, so the first positively scored candidate
		// is the best one. A candidate with no score at all is reported as a
		// candidate but never chosen: claiming a number is the OTP because it
		// was the only number in the mail turns a missing code into a
		// confusing test failure somewhere else.
		if result.OTPBest == "" && c.score > 0 {
			result.OTPBest = c.code
		}
	}
	result.VerifyLinkBest = verifyLinkBest(links)
	return result
}

// verifyLinkBest picks the link a fixture or MCP tool should open.
//
// A verify link wins, and a magic link is the fallback: a signup template
// carries kind verify, a passwordless login template carries kind magic, and
// both are "the link that finishes this flow". A reset link is never chosen,
// because handing a password-reset URL to a caller that asked to verify an
// address would quietly do the wrong thing.
func verifyLinkBest(links []Link) string {
	for _, kind := range []string{KindVerify, KindMagic} {
		for _, l := range links {
			if l.Actionable && l.Kind == kind {
				return l.URL
			}
		}
	}
	return ""
}

// fold reduces text to the form the keyword lists are written in: lower case,
// no diacritics.
//
// This is what makes one list cover "mã xác thực" and "ma xac thuc" at once,
// rather than two lists that drift apart. It runs on NFC input and decomposes
// again internally, because a combining mark can only be dropped once it is
// separate from the letter it sits on.
func fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// A non-spacing combining mark. Dropping it is the whole point.
		case r == 'đ':
			// Vietnamese d-with-stroke is a letter of its own rather than a
			// letter plus a mark, so NFD leaves it whole and it needs its own
			// mapping. Without this, "mã đăng nhập" would not match the
			// unaccented "ma dang nhap" people type.
			b.WriteRune('d')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// span is a half-open byte range [start, end) in one of the folded or NFC
// views of the message text.
type span struct {
	start int
	end   int
}

// mergeSpans sorts and coalesces spans into a disjoint ascending list, so a
// membership test can binary search instead of scanning.
//
// Scanning is the obvious version and it is quadratic against the candidate
// list: a body of many URLs each holding digits gives both lists a length
// proportional to the message, and this runs on mail nobody vouched for.
func mergeSpans(spans []span) []span {
	if len(spans) < 2 {
		return spans
	}
	slices.SortFunc(spans, func(a, b span) int {
		if a.start != b.start {
			return a.start - b.start
		}
		return a.end - b.end
	})
	out := spans[:1]
	for _, s := range spans[1:] {
		last := &out[len(out)-1]
		if s.start <= last.end {
			if s.end > last.end {
				last.end = s.end
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// inSpans reports whether s overlaps any span in a disjoint ascending list.
func inSpans(sorted []span, s span) bool {
	i := sort.Search(len(sorted), func(i int) bool { return sorted[i].end > s.start })
	return i < len(sorted) && sorted[i].start < s.end
}

// containsAny reports whether any of the needles appears in hay as a
// substring. Substring rather than whole-word, because URL paths glue words
// together ("resetPassword" folds to "resetpassword") and a word-boundary
// match would miss exactly the links that matter. The precedence order in
// classify is what keeps that from misfiring.
func containsAny(hay string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(hay, n) {
			return true
		}
	}
	return false
}

// findWords returns the spans where any of the words appears delimited by
// something other than a letter or a digit.
//
// Whole-word matching is right for prose, where the short keywords would
// otherwise fire constantly: "pin" sits inside "shipping" and "code" inside
// "encoded", and either would drag a random number to the top of the OTP
// ranking.
func findWords(hay string, words []string) []span {
	var out []span
	for _, w := range words {
		for from := 0; ; {
			i := strings.Index(hay[from:], w)
			if i < 0 {
				break
			}
			start := from + i
			end := start + len(w)
			if isWordBoundary(hay, start, end) {
				out = append(out, span{start: start, end: end})
			}
			from = start + 1
		}
	}
	return out
}

func isWordBoundary(hay string, start, end int) bool {
	if start > 0 {
		if r, _ := utf8.DecodeLastRuneInString(hay[:start]); isWordRune(r) {
			return false
		}
	}
	if end < len(hay) {
		if r, _ := utf8.DecodeRuneInString(hay[end:]); isWordRune(r) {
			return false
		}
	}
	return true
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
