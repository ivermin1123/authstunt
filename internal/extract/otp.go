package extract

import (
	"slices"
	"sort"
	"strconv"
	"unicode/utf8"
)

// otpContextWords are the folded forms, so each entry matches the accented
// spelling and the unaccented one at the same time: "ma xac thuc" is what both
// "mã xác thực" and "ma xac thuc" reduce to.
//
// English and Vietnamese together, because the product's mail is both and a
// Vietnamese template that only said "mã xác nhận" would otherwise score
// nothing at all.
var otpContextWords = []string{
	// English
	"code", "codes", "otp", "passcode", "pin",
	"verification", "verify", "confirmation", "confirm",
	"one time", "one-time", "onetime", "security", "token",
	// Vietnamese, folded
	"ma xac thuc", "ma xac nhan", "ma xac minh", "ma otp",
	"ma bao mat", "ma kich hoat", "ma dang nhap", "ma pin",
	"ma so", "xac thuc", "xac nhan",
}

// Scoring weights. The absolute numbers do not matter; the order they impose
// does, and it is pinned by the corpus rather than by these constants.
const (
	// Proximity is the dominant signal. Every real template puts the code
	// within a few words of what it calls it.
	scoreNear    = 100 // within nearWindow bytes of a context word
	scoreMedium  = 60  // same sentence, roughly
	scoreFar     = 30  // same paragraph, roughly
	nearWindow   = 40
	mediumWindow = 80
	farWindow    = 160

	// A context word immediately in front of the digits is the single most
	// reliable shape there is: "Your code: 482913", "mã xác thực của bạn là
	// 482913".
	scoreLeadingWord = 15
	leadingWindow    = 24

	// Six digits is the overwhelmingly common OTP length, so it breaks ties
	// between two equally well placed candidates, and it is enough on its own
	// to prefer a bare six-digit number over nothing.
	scoreSixDigits   = 10
	scoreEightDigits = 4

	// A four-digit year is the classic false positive: footers carry
	// copyright lines and templates carry dates, and a copyright line sits
	// close enough to the body to score well on proximity alone. The penalty
	// is large enough to push a year below zero even when it is perfectly
	// placed, so a mail whose only number is "2026" reports no code rather
	// than a wrong one. It still appears among the candidates.
	penaltyYear = -120

	minOTPLen = 4
	maxOTPLen = 8
)

// maxContextWordLen is the longest entry in otpContextWords, which bounds how
// far behind a candidate a keyword can start and still reach it.
var maxContextWordLen = func() int {
	longest := 0
	for _, w := range otpContextWords {
		if len(w) > longest {
			longest = len(w)
		}
	}
	return longest
}()

type candidate struct {
	code  string
	pos   int
	score int
}

// otpCandidates finds every standalone 4-8 digit run in the folded text and
// ranks it. exclude holds ranges the digits must not come from, which is how a
// token inside a URL stops being mistaken for a code.
//
// Ranking is score descending, then position ascending. Positions are unique,
// so the order is total and the corpus can assert an exact list.
func otpCandidates(folded string, exclude []span) []candidate {
	words := findWords(folded, otpContextWords)
	slices.SortFunc(words, func(a, b span) int { return a.start - b.start })
	exclude = mergeSpans(exclude)

	var found []candidate
	for _, digits := range digitRuns(folded) {
		if len(folded[digits.start:digits.end]) < minOTPLen {
			continue
		}
		if len(folded[digits.start:digits.end]) > maxOTPLen {
			// A longer run is not an OTP with extra characters, it is
			// something else entirely - an order number, an id - and taking
			// any 8 digits out of it would invent a code the mail never
			// contained.
			continue
		}
		if inSpans(exclude, digits) {
			continue
		}
		code := folded[digits.start:digits.end]
		found = append(found, candidate{
			code:  code,
			pos:   digits.start,
			score: scoreCandidate(code, digits, words),
		})
	}

	// Same digits twice in one mail is one candidate, keeping the better
	// placed occurrence: templates repeat the code in the subject and the
	// body, and reporting it twice would suggest two codes.
	slices.SortFunc(found, func(a, b candidate) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return a.pos - b.pos
	})
	out := make([]candidate, 0, len(found))
	seen := make(map[string]struct{}, len(found))
	for _, c := range found {
		if _, dup := seen[c.code]; dup {
			continue
		}
		seen[c.code] = struct{}{}
		out = append(out, c)
	}
	return out
}

// scoreCandidate ranks one candidate. words must be sorted by start.
//
// Only the words that could possibly fall inside the scoring windows are
// examined, found by binary search. Walking the whole list per candidate is
// quadratic on a body that repeats a context word, and the result is identical
// either way: a word outside this neighborhood is further away than
// farWindow, which scores nothing.
func scoreCandidate(code string, digits span, words []span) int {
	score := 0

	nearest := -1
	leading := false
	from := sort.Search(len(words), func(i int) bool {
		return words[i].start >= digits.start-farWindow-maxContextWordLen
	})
	limit := digits.end + farWindow
	for _, w := range words[from:] {
		if w.start > limit {
			break
		}
		distance := 0
		switch {
		case w.end <= digits.start:
			distance = digits.start - w.end
			if distance <= leadingWindow {
				leading = true
			}
		case w.start >= digits.end:
			distance = w.start - digits.end
		}
		if nearest < 0 || distance < nearest {
			nearest = distance
		}
	}
	switch {
	case nearest < 0:
		// No context word anywhere in the message.
	case nearest <= nearWindow:
		score += scoreNear
	case nearest <= mediumWindow:
		score += scoreMedium
	case nearest <= farWindow:
		score += scoreFar
	}
	if leading {
		score += scoreLeadingWord
	}

	switch len(code) {
	case 6:
		score += scoreSixDigits
	case 8:
		score += scoreEightDigits
	}
	if looksLikeYear(code) {
		score += penaltyYear
	}
	return score
}

func looksLikeYear(code string) bool {
	if len(code) != 4 {
		return false
	}
	n, err := strconv.Atoi(code)
	if err != nil {
		return false
	}
	return n >= 1900 && n <= 2099
}

// digitRuns returns the maximal runs of ASCII digits that are not glued to a
// letter or another digit on either side.
//
// The boundary check is what separates a code from part of a word or a longer
// identifier: "abc482913" and "482913x" are not codes. It decodes the
// neighboring rune rather than looking at the neighboring byte, so a
// multi-byte letter counts as a letter.
func digitRuns(s string) []span {
	var out []span
	for i := 0; i < len(s); {
		if !isASCIIDigit(s[i]) {
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
			continue
		}
		start := i
		for i < len(s) && isASCIIDigit(s[i]) {
			i++
		}
		if isWordBoundary(s, start, i) {
			out = append(out, span{start: start, end: i})
		}
	}
	return out
}

func isASCIIDigit(b byte) bool { return b >= '0' && b <= '9' }

// urlSpans locates the URLs so their contents can be kept out of the OTP scan:
// a path segment that happens to be six digits is not a code.
//
// It runs on the folded text and only needs the ranges, so it does not have to
// produce a usable URL; extracting the links themselves happens separately,
// against text that has not been folded. Both use the one scanner, so a URL
// this ignores is a URL the link list reports.
func urlSpans(folded string) []span {
	urls := findPlainURLs(folded)
	out := make([]span, 0, len(urls))
	for _, u := range urls {
		out = append(out, u.at)
	}
	return out
}
