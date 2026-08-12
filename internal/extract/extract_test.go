package extract

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// fixture is one corpus sample: the message as it arrives, and the exact
// result it must produce.
type fixture struct {
	Note    string `json:"note"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
	// NFD asks the loader to re-encode the inputs to decomposed form. Writing
	// combining sequences by hand in a fixture file is unreadable and easy to
	// get subtly wrong, so the test does the encoding and then proves the
	// bytes really did change.
	NFD  bool   `json:"nfd"`
	Want Result `json:"want"`
}

// corpusFS roots the fixtures, so the loader never builds a file path out of a
// variable and every read is confined to this directory.
var corpusFS = os.DirFS(filepath.Join("testdata", "corpus"))

// loadCorpus reads every fixture, keyed by file name.
//
// DisallowUnknownFields is what keeps a fixture honest: a typo in an
// expectation key would otherwise be silently ignored and the fixture would
// assert less than it appears to.
func loadCorpus(t *testing.T) map[string]fixture {
	t.Helper()
	names, err := fs.Glob(corpusFS, "*.json")
	if err != nil {
		t.Fatal(err)
	}
	// The corpus is the deliverable, so its size is asserted: quietly
	// shrinking it would weaken the acceptance proof without failing anything.
	if len(names) < 25 {
		t.Fatalf("corpus holds %d fixtures, the design requires at least 25", len(names))
	}
	out := make(map[string]fixture, len(names))
	for _, name := range names {
		raw, err := fs.ReadFile(corpusFS, name)
		if err != nil {
			t.Fatal(err)
		}
		var f fixture
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&f); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = f
	}
	return out
}

// TestExtractionCorpus is the P1 acceptance proof for extraction (Brief 11).
//
// One table over every fixture on disk. A provider template that this engine
// gets wrong becomes a new file here rather than a change to this test, which
// is why the corpus lives in testdata instead of in a Go literal.
func TestExtractionCorpus(t *testing.T) {
	for name, f := range loadCorpus(t) {
		t.Run(name, func(t *testing.T) {
			in := Input{Subject: f.Subject, Text: f.Text, HTML: f.HTML}
			if f.NFD {
				in = Input{
					Subject: norm.NFD.String(f.Subject),
					Text:    norm.NFD.String(f.Text),
					HTML:    norm.NFD.String(f.HTML),
				}
				// Without this the fixture could silently become a duplicate of
				// its NFC twin and still pass, proving nothing about
				// normalization.
				if in.Text == f.Text && in.Subject == f.Subject && in.HTML == f.HTML {
					t.Fatal("fixture is marked nfd but its decomposed form is byte-identical, so it tests nothing")
				}
			}

			got := Extract(in)
			if got.OTPBest != f.Want.OTPBest {
				t.Errorf("otp_best = %q, want %q", got.OTPBest, f.Want.OTPBest)
			}
			if !slices.Equal(got.OTPCandidates, f.Want.OTPCandidates) {
				t.Errorf("otp_candidates = %v, want %v", got.OTPCandidates, f.Want.OTPCandidates)
			}
			if !slices.Equal(got.Links, f.Want.Links) {
				t.Errorf("links = %+v, want %+v", got.Links, f.Want.Links)
			}
			if got.VerifyLinkBest != f.Want.VerifyLinkBest {
				t.Errorf("verify_link_best = %q, want %q", got.VerifyLinkBest, f.Want.VerifyLinkBest)
			}
		})
	}
}

// TestResultMarshalsEmptyArrays pins the wire shape the store seals: a message
// with nothing extracted still carries arrays, so no consumer has to tell
// "none" apart from "field missing".
func TestResultMarshalsEmptyArrays(t *testing.T) {
	out, err := json.Marshal(Extract(Input{Text: "nothing here"}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"otp_best":"","otp_candidates":[],"links":[],"verify_link_best":""}`
	if string(out) != want {
		t.Errorf("marshaled to %s, want %s", out, want)
	}
}

// TestNoActionableLinkEverCarriesADisallowedScheme is the scheme allowlist as
// an invariant over the whole corpus rather than one fixture: whatever the
// samples contain, nothing outside http and https can come back actionable.
func TestNoActionableLinkEverCarriesADisallowedScheme(t *testing.T) {
	checked := 0
	for path, f := range loadCorpus(t) {
		got := Extract(Input{Subject: f.Subject, Text: f.Text, HTML: f.HTML})
		for _, l := range got.Links {
			if l.Actionable {
				assertHTTPScheme(t, path, l.URL)
				checked++
			}
		}
		if got.VerifyLinkBest != "" {
			assertHTTPScheme(t, path, got.VerifyLinkBest)
			// The best link is always one of the reported links, never
			// something assembled on the way out.
			if !slices.ContainsFunc(got.Links, func(l Link) bool {
				return l.URL == got.VerifyLinkBest && l.Actionable
			}) {
				t.Errorf("%s: verify_link_best %q is not an actionable entry in links", path, got.VerifyLinkBest)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no actionable link in the whole corpus, so this invariant proved nothing")
	}
}

func assertHTTPScheme(t *testing.T, path, rawURL string) {
	t.Helper()
	scheme := strings.ToLower(rawURL)
	if !strings.HasPrefix(scheme, "http://") && !strings.HasPrefix(scheme, "https://") {
		t.Errorf("%s: %q came back actionable with a scheme outside the allowlist", path, rawURL)
	}
}
