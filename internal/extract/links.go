package extract

import (
	"net/url"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Classification keyword lists, folded. They are checked in the order the
// rules below apply them, not as one pool, because real templates carry
// several of these words at once and the order is what resolves them.
var (
	magicStrongWords = []string{"passwordless", "magiclink", "magic-link", "magic_link", "magic link", "one-click", "otl"}
	resetStrongWords = []string{"reset", "recover", "forgot", "khoi phuc", "dat lai", "quen mat khau", "doi mat khau"}
	verifyWords      = []string{"verify", "verification", "verif", "confirm", "activate", "activation", "xac thuc", "xac nhan", "xac minh", "kich hoat"}
	magicWeakWords   = []string{"signin", "sign-in", "sign_in", "sign in", "login", "log-in", "log_in", "dang nhap"}
	resetWeakWords   = []string{"password", "mat khau"}
)

// classify decides what a link is for from the URL plus its anchor text.
//
// The precedence is deliberate, because the obvious "check each kind once"
// version gets real providers wrong:
//
//   - A Supabase magic link is `/auth/v1/verify?type=magiclink`. It contains
//     "verify", so verify has to lose to the stronger magiclink signal.
//   - A Supabase recovery link is `/auth/v1/verify?type=recovery`, so reset
//     has to beat verify too.
//   - "Passwordless sign-in" contains "password", so the weak reset signal has
//     to come last or every magic link becomes a reset link.
//
// Anchor text is read here and then dropped. It earns its keep as a signal -
// a bare `/t/abc123` link with "Confirm my email" on it is classifiable only
// from the text - but persisting it would freeze user-visible copy into a
// durable contract for no consumer that asked for it.
func classify(rawURL, anchor string) string {
	hay := fold(rawURL) + " " + fold(anchor)
	switch {
	case containsAny(hay, magicStrongWords):
		return KindMagic
	case containsAny(hay, resetStrongWords):
		return KindReset
	case containsAny(hay, verifyWords):
		return KindVerify
	case containsAny(hay, magicWeakWords):
		return KindMagic
	case containsAny(hay, resetWeakWords):
		return KindReset
	}
	return KindOther
}

// actionable reports whether anything is allowed to follow this URL.
//
// Design 3.18 allows http and https and nothing else. A javascript: or data:
// href is recorded as inert text and never handed to a fixture, a tool, or the
// dashboard as something to open. A relative href is inert too: without the
// base URL the mail was written against, it is not a place this can send
// anyone.
func actionable(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if strings.ToLower(u.Scheme) != "http" && strings.ToLower(u.Scheme) != "https" {
		return false
	}
	return u.Host != ""
}

type rawLink struct {
	url    string
	anchor string
}

// openAnchor is the anchor currently being read. Its href is already recorded
// at position at, so hrefs stay in document order however the markup nests;
// only the text has to wait for the closing tag.
//
// The text goes through a builder because one anchor can hold many text tokens
// - a button wrapped in spans and bold tags is ordinary markup - and appending
// to a string once per token is quadratic in the anchor's length.
type openAnchor struct {
	at     int
	anchor strings.Builder
}

// collectLinks merges the HTML hrefs with the URLs printed in plain text,
// in that order, and reports each URL once.
//
// Templates routinely include the same link twice, once as a button and once
// spelled out for clients that do not render HTML, and two entries for one
// destination would make a fixture look at a list of two links where a person
// sees one.
func collectLinks(hrefs []rawLink, plain string) []Link {
	links := make([]Link, 0, len(hrefs))
	index := make(map[string]int, len(hrefs))

	add := func(raw rawLink) {
		if raw.url == "" {
			return
		}
		kind := classify(raw.url, raw.anchor)
		if at, seen := index[raw.url]; seen {
			// The duplicate may carry the signal the first occurrence lacked:
			// the plain-text copy has no anchor text, the HTML one does.
			if links[at].Kind == KindOther && kind != KindOther {
				links[at].Kind = kind
			}
			return
		}
		index[raw.url] = len(links)
		links = append(links, Link{URL: raw.url, Kind: kind, Actionable: actionable(raw.url)})
	}

	for _, h := range hrefs {
		add(h)
	}
	plainURLs := findPlainURLs(plain)
	if len(plainURLs) > 0 {
		lines := newLineIndex(plain)
		for _, u := range plainURLs {
			// The line around a spelled-out URL is the plain-text counterpart
			// of anchor text, and it is often the only signal there is:
			// "Confirm here: https://app.test/c/8a2f" says what the link is
			// for while the URL itself says nothing.
			add(rawLink{url: u.url, anchor: lines.around(plain, u.at)})
		}
	}
	return links
}

// lineIndex holds the newline offsets of a string.
//
// It exists so the line around a position can be found by binary search rather
// than by scanning outwards for a line break. Scanning is the obvious version
// and it is quadratic on a body that is one long line of many URLs, which is
// what a machine-generated mail often is.
type lineIndex []int

func newLineIndex(s string) lineIndex {
	var out lineIndex
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, i)
		}
	}
	return out
}

// contextWindow bounds how far on either side of a URL its context reaches.
//
// The line is the natural unit, but a line is not necessarily short: a
// machine-generated body can be one enormous line with thousands of URLs on
// it. Handing each of them the whole line would be quadratic, because the
// context is folded once per URL, and it would also be meaningless - words
// half a megabyte away say nothing about this link. What actually carries the
// signal is the phrase right next to it, "Confirm here:" or "Nhan vao day de
// xac thuc".
const contextWindow = 120

// around returns the text on the same line as at, reaching no further than
// contextWindow bytes to either side. Mail wraps its sentences, so stopping at
// a line break avoids borrowing words from an unrelated one.
func (idx lineIndex) around(s string, at span) string {
	start := 0
	if i := sort.SearchInts(idx, at.start); i > 0 {
		start = idx[i-1] + 1
	}
	end := len(s)
	if j := sort.SearchInts(idx, at.end); j < len(idx) {
		end = idx[j]
	}
	if at.start-start > contextWindow {
		start = at.start - contextWindow
		// Clipping mid-rune would hand fold an invalid sequence.
		for start < at.start && !utf8.RuneStart(s[start]) {
			start++
		}
	}
	if end-at.end > contextWindow {
		end = at.end + contextWindow
		for end > at.end && !utf8.RuneStart(s[end]) {
			end--
		}
	}
	return s[start:end]
}

// separatingTags are the elements whose boundary a reader can see, so the text
// on either side of one is two separate things rather than one word.
//
// The distinction matters in both directions and neither is hypothetical.
// "<p>Your code is</p><h1>915430</h1>" hands the tokenizer adjacent pieces that
// concatenate to "Your code is915430", where the digits are glued to a letter
// and stop looking like a standalone code. But "<strong>915</strong>430" is one
// number a reader sees as 915430, and "abc<span>123456</span>" is one word that
// is not a code at all: separating either of those invents a boundary that is
// not on screen, which loses a real code in the first case and manufactures a
// false one in the second.
//
// The list is the CSS default rendering, which is what "a reader can see"
// means in practice: block-level boxes, table and list structure, the line
// break, and the replaced elements that occupy a box of their own - a spacer
// image between two pieces of text does separate them. Everything else,
// including an element nobody has heard of, is text-level and joins, because an
// unknown element renders inline.
//
// An anchor is text-level too, and deliberately not in this list. Color and an
// underline are not whitespace: "ref<a>123456</a>" reads as one token on
// screen, so it is not a code, and treating the anchor as a boundary would
// invent one code per linked number. The anchor's own text is collected
// separately for classification, which needs no boundary in the document text
// to work.
var separatingTags = func() map[string]struct{} {
	tags := []string{
		"address", "article", "aside", "blockquote", "body", "br", "caption",
		"center", "col", "colgroup", "dd", "details", "dialog", "dir", "div",
		"dl", "dt", "fieldset", "figcaption", "figure", "footer", "form",
		"frame", "frameset", "h1", "h2", "h3", "h4", "h5", "h6", "head",
		"header", "hgroup", "hr", "html", "legend", "li", "listing", "main",
		"marquee", "menu", "nav", "ol", "optgroup", "option", "p", "plaintext",
		"pre", "search", "section", "summary", "table", "tbody", "td", "tfoot",
		"th", "thead", "title", "tr", "ul", "xmp",
		"audio", "button", "canvas", "embed", "iframe", "img", "input",
		"object", "picture", "select", "svg", "textarea", "video",
	}
	set := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		set[t] = struct{}{}
	}
	return set
}()

func separatesText(tag string) bool {
	_, ok := separatingTags[tag]
	return ok
}

// scanHTML tokenizes an HTML body into its hrefs, each anchor carrying its
// text, plus the visible text of the whole document.
//
// The visible text is what the OTP scan reads, so a six-digit number in a
// class name, a tracking URL, or a script never becomes a candidate code. The
// tokenizer is HTML5-compliant and does not fail on malformed markup, which is
// most of what mail templates are made of.
func scanHTML(htmlBody string) (hrefs []rawLink, visible string) {
	if htmlBody == "" {
		return nil, ""
	}
	z := html.NewTokenizer(strings.NewReader(htmlBody))
	var text strings.Builder
	var open *openAnchor
	// rawTextDepth counts script and style elements, whose contents the
	// tokenizer hands over as text but a reader never sees.
	rawTextDepth := 0
	// pendingBreak remembers that a visible boundary was crossed since the last
	// text token, so the separator is written once between two pieces instead
	// of after every piece. A newline can only separate; it can never join two
	// things that were apart.
	pendingBreak := false

	closeAnchor := func() {
		if open != nil {
			hrefs[open.at].anchor = open.anchor.String()
			open = nil
		}
	}

	for {
		switch z.Next() {
		case html.ErrorToken:
			// Either io.EOF or a read error; both mean there is no more
			// markup, and a partially tokenized template still yields
			// everything found so far.
			closeAnchor()
			return hrefs, text.String()

		case html.TextToken:
			if rawTextDepth > 0 {
				continue
			}
			// Text() may be overwritten by the next Next call, so this copies.
			chunk := string(z.Text())
			if pendingBreak {
				pendingBreak = false
				if text.Len() > 0 {
					text.WriteByte('\n')
				}
				if open != nil && open.anchor.Len() > 0 {
					open.anchor.WriteByte(' ')
				}
			}
			text.WriteString(chunk)
			if open != nil {
				open.anchor.WriteString(chunk)
			}

		case html.StartTagToken, html.SelfClosingTagToken:
			// Both token kinds are handled alike on purpose. HTML5 makes the
			// self-closing flag a parse error on every element that is not
			// void and ignores it there, so `<a href="x"/>` opens an anchor
			// over the text that follows and `<script src="x"/>` starts script
			// data, exactly as they would without the slash. Fixture 42 pins
			// it. Reading the slash as a close instead would extract text that
			// no mail client displays.
			name, hasAttr := z.TagName()
			tag := string(name)
			if separatesText(tag) {
				pendingBreak = true
			}
			href := strings.TrimSpace(attr(z, hasAttr, "href"))
			switch tag {
			case "script", "style":
				rawTextDepth++
			case "a":
				// A nested or unclosed anchor is malformed but common. Closing
				// the previous one keeps its text rather than dropping it.
				closeAnchor()
				open = &openAnchor{at: len(hrefs)}
				hrefs = append(hrefs, rawLink{url: href})
			default:
				// Brief 6.3 says every href, not every anchor: the area of an
				// image map is a link a reader clicks, and an href this did not
				// report would be invisible to an operator too. Only an anchor
				// has text saying what its link is for, so the rest carry none.
				if href != "" {
					hrefs = append(hrefs, rawLink{url: href})
				}
			}

		case html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if separatesText(tag) {
				pendingBreak = true
			}
			switch tag {
			case "script", "style":
				if rawTextDepth > 0 {
					rawTextDepth--
				}
			case "a":
				closeAnchor()
			}
		}
	}
}

// attr returns the value of the named attribute of the current tag token.
func attr(z *html.Tokenizer, hasAttr bool, want string) string {
	for hasAttr {
		var key, val []byte
		key, val, hasAttr = z.TagAttr()
		if string(key) == want {
			return string(val)
		}
	}
	return ""
}

type plainURL struct {
	url string
	at  span
}

// findPlainURLs finds the URLs written out in text, in the order they appear.
//
// One scanner serves two callers that must agree: the link list, which reports
// the URLs a reader would follow, and the OTP scan, which has to ignore the
// digits inside them. When these disagreed, a spelled-out "www." host was
// excluded from the OTP scan and then silently absent from links.
func findPlainURLs(s string) []plainURL {
	out := findSchemeURLs(s)
	// findSchemeURLs returns ascending, non-overlapping ranges, which is what
	// the containment test below needs.
	spans := make([]span, len(out))
	for i, u := range out {
		spans[i] = u.at
	}
	if bare := findBareHostURLs(s, spans); len(bare) > 0 {
		out = append(out, bare...)
		slices.SortFunc(out, func(a, b plainURL) int { return a.at.start - b.at.start })
	}
	return out
}

// bareHostPrefix is the one scheme-less shape worth recognizing. A URL without
// a scheme cannot be told apart from ordinary prose in general - "example.com"
// is a sentence away from "e.g." - but templates print "www." hosts often
// enough, and the prefix is unambiguous.
const bareHostPrefix = "www."

// indexBareHostPrefix returns the offset of the next "www.", in any casing.
//
// The match is case-insensitive and the search runs on the caller's own bytes,
// rather than on a lower-cased copy, for two reasons: lower-casing can change
// a string's length, which would move every offset this function reports, and
// the URL has to come back spelled the way the message spelled it. A host is
// ASCII by the time it means anything, so a byte-wise compare is enough.
func indexBareHostPrefix(s string) int {
	for i := 0; i+len(bareHostPrefix) <= len(s); i++ {
		if s[i+3] == '.' && lowerASCII(s[i]) == 'w' && lowerASCII(s[i+1]) == 'w' && lowerASCII(s[i+2]) == 'w' {
			return i
		}
	}
	return -1
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// findBareHostURLs finds the scheme-less hosts, skipping any that falls inside
// a URL that already has one: "https://www.a.test/x" is one link, not two.
func findBareHostURLs(s string, exclude []span) []plainURL {
	var out []plainURL
	for from := 0; ; {
		i := indexBareHostPrefix(s[from:])
		if i < 0 {
			return out
		}
		start := from + i
		end := start
		for end < len(s) && !isURLTerminator(s[end]) {
			end++
		}
		from = end
		if !startsToken(s, start) || inSpans(exclude, span{start: start, end: end}) {
			continue
		}
		raw := trimURLTail(s[start:end])
		if !isBareHost(raw) {
			continue
		}
		out = append(out, plainURL{url: raw, at: span{start: start, end: start + len(raw)}})
	}
}

// startsToken reports whether at begins a word rather than continuing one, so
// the "www." inside a longer token is not read as the start of a host.
func startsToken(s string, at int) bool {
	if at == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:at])
	return !isWordRune(r) && r != '.' && r != '@'
}

// isBareHost reports whether raw is "www." followed by a host with at least one
// more label, so a sentence that happens to end in "www." is not a link.
func isBareHost(raw string) bool {
	if len(raw) <= len(bareHostPrefix) {
		return false
	}
	rest := raw[len(bareHostPrefix):]
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	label, _, found := strings.Cut(rest, ".")
	return found && label != ""
}

// findSchemeURLs finds absolute URLs written out in text.
//
// It anchors on "://" and walks outwards, which finds any scheme without
// needing a list of them, and keeps the byte range so the OTP scan can ignore
// whatever sits inside a URL.
func findSchemeURLs(s string) []plainURL {
	var out []plainURL
	for from := 0; ; {
		i := strings.Index(s[from:], "://")
		if i < 0 {
			return out
		}
		mark := from + i

		start := mark
		for start > 0 {
			r := s[start-1]
			if !isSchemeByte(r) {
				break
			}
			start--
		}
		end := mark + len("://")
		for end < len(s) && !isURLTerminator(s[end]) {
			end++
		}

		from = end
		if start == mark {
			// "://" with no scheme in front of it is not a URL.
			continue
		}
		raw := trimURLTail(s[start:end])
		if raw == "" {
			continue
		}
		out = append(out, plainURL{url: raw, at: span{start: start, end: start + len(raw)}})
	}
}

func isSchemeByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '+', b == '-', b == '.':
		return true
	}
	return false
}

// isURLTerminator reports the bytes that cannot continue a URL in running
// text: whitespace, control characters, and the quoting and bracketing
// characters that surround a URL rather than belong to it.
func isURLTerminator(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n', '\v', '\f', '<', '>', '"', '\'', '`':
		return true
	}
	return b < 0x20 || b == 0x7f
}

// trimURLTail drops the punctuation that ends the sentence rather than the
// URL. A closing bracket is only dropped when it has no opener inside the URL,
// so a link that legitimately contains one survives.
//
// The bracket tallies are counted once and then kept up to date as characters
// come off the end. Recounting inside the loop reads naturally and is
// quadratic, which matters because this runs on a message body: a mail whose
// body is one URL followed by a long run of ")" is a plausible thing for
// somebody to send, and at the 10MB size limit the quadratic version spends
// most of an hour on it.
func trimURLTail(raw string) string {
	var tally [3]int // openers: ( [ {
	var closers [3]int
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '(':
			tally[0]++
		case '[':
			tally[1]++
		case '{':
			tally[2]++
		case ')':
			closers[0]++
		case ']':
			closers[1]++
		case '}':
			closers[2]++
		}
	}
	for len(raw) > 0 {
		var bracket int
		switch raw[len(raw)-1] {
		case '.', ',', ';', ':', '!', '?':
			raw = raw[:len(raw)-1]
			continue
		case ')':
			bracket = 0
		case ']':
			bracket = 1
		case '}':
			bracket = 2
		default:
			return raw
		}
		if tally[bracket] >= closers[bracket] {
			// Balanced, so this bracket belongs to the URL.
			return raw
		}
		closers[bracket]--
		raw = raw[:len(raw)-1]
	}
	return raw
}
