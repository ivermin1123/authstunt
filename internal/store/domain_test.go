package store_test

import (
	"strings"
	"testing"

	"github.com/ivermin1123/authstunt/internal/store"
)

func TestCanonicalDomainUnicodeAndWildcard(t *testing.T) {
	accepted := []struct {
		name string
		in   string
		want string
	}{
		{"already canonical", "demo.test", "demo.test"},
		{"mixed case", "Demo.TEST", "demo.test"},
		{"surrounding space", "  demo.test  ", "demo.test"},
		{"root dot", "demo.test.", "demo.test"},
		// SQLite NOCASE folds ASCII only, so without IDNA these three would
		// be three different inboxes.
		{"unicode", "démo.test", "xn--dmo-bma.test"},
		{"unicode upper", "DÉMO.TEST", "xn--dmo-bma.test"},
		{"already punycode", "xn--dmo-bma.test", "xn--dmo-bma.test"},
		{"subdomain", "Mail.Demo.Test", "mail.demo.test"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.CanonicalDomain(tc.in)
			if err != nil {
				t.Fatalf("CanonicalDomain(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("CanonicalDomain(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	rejected := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"leading dot", ".demo.test"},
		{"empty label", "mail..demo.test"},
		{"underscore", "mail_server.test"},
		{"wildcard as a bare domain", "*.demo.test"},
		{"label past 63 bytes", strings.Repeat("a", 64) + ".test"},
		{"space inside", "de mo.test"},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			if got, err := store.CanonicalDomain(tc.in); err == nil {
				t.Errorf("CanonicalDomain(%q) = %q, want an error", tc.in, got)
			}
		})
	}
}

func TestCanonicalDomainPatternKeepsWildcard(t *testing.T) {
	accepted := []struct {
		in   string
		want string
	}{
		{"*.demo.test", "*.demo.test"},
		{"*.Demo.Test", "*.demo.test"},
		{"*.démo.test", "*.xn--dmo-bma.test"},
		{"demo.test", "demo.test"},
	}
	for _, tc := range accepted {
		got, err := store.CanonicalDomainPattern(tc.in)
		if err != nil {
			t.Fatalf("CanonicalDomainPattern(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("CanonicalDomainPattern(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// A wildcard is a leading label or nothing. "mail.*.test" and
	// "*demo.test" look like they mean something and do not.
	for _, in := range []string{"mail.*.test", "*demo.test", "*.", "*.*.test"} {
		if got, err := store.CanonicalDomainPattern(in); err == nil {
			t.Errorf("CanonicalDomainPattern(%q) = %q, want an error", in, got)
		}
	}
}

func TestPatternBaseDomainForGeneration(t *testing.T) {
	// Persona generation picks the first allowlisted pattern and builds an
	// address under it, and "user@*.demo.test" is not an address.
	for in, want := range map[string]string{
		"*.demo.test": "demo.test",
		"demo.test":   "demo.test",
	} {
		if got := store.PatternBaseDomain(in); got != want {
			t.Errorf("PatternBaseDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeAddressCanonicalizesDomainOnly(t *testing.T) {
	cases := map[string]string{
		"User@Demo.Test":   "User@demo.test",
		"user@démo.test":   "user@xn--dmo-bma.test",
		"  user@DEMO.test": "user@demo.test",
		// The local part is case-sensitive per RFC 5321 and stays as the
		// sender wrote it.
		"Bob.Smith@demo.test": "Bob.Smith@demo.test",
		// A domain IDNA cannot process still yields a deterministic value:
		// dropping the address would lose accepted mail, and the allowlist
		// is what decides whether it was wanted.
		"user@mail_server.TEST": "user@mail_server.test",
		"not-an-address":        "not-an-address",
	}
	for in, want := range cases {
		if got := store.NormalizeAddress(in); got != want {
			t.Errorf("NormalizeAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMessageMatchingIsUnicodeInsensitive(t *testing.T) {
	ctx := t.Context()
	s := openTestStore(t)
	project := newProject(t, s)

	// The app sends to the Unicode spelling; the fixture asks for the
	// punycode one. NOCASE alone would miss.
	if _, err := s.InsertMessage(ctx, store.Message{
		ProjectID: project.ID, FromAddr: "app@démo.test", Subject: "code",
		TextBody:   "483920",
		Recipients: []store.Recipient{{Addr: "User@DÉMO.test", Kind: store.RecipientEnvelope}},
	}); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"User@xn--dmo-bma.test", "User@démo.test"} {
		got, err := s.ListMessages(ctx, store.MessageFilter{ProjectID: project.ID, To: query})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Errorf("matching %q returned %d messages, want 1", query, len(got))
		}
	}
}

func TestAllowlistMatches(t *testing.T) {
	patterns := []string{"demo.test", "*.example.test"}
	cases := []struct {
		name string
		addr string
		want bool
	}{
		{"exact domain", "user@demo.test", true},
		{"case is folded", "user@Demo.TEST", true},
		{"trailing root dot is the same domain", "user@demo.test.", true},
		{"a wildcard covers its own base", "user@example.test", true},
		{"a wildcard covers a subdomain", "user@mail.example.test", true},
		{"a wildcard covers a deep subdomain", "user@a.b.example.test", true},
		{"a different domain", "user@gmail.com", false},
		{"a suffix that is not a subdomain", "user@notexample.test", false},
		{"a subdomain of a non-wildcard entry", "user@mail.demo.test", false},
		{"the base domain as a prefix", "user@demo.test.evil.com", false},
		{"no at sign", "demo.test", false},
		{"empty domain", "user@", false},
		{"empty address", "", false},
		// The local part may contain an at sign, so the domain is what
		// follows the last one, not the first.
		{"quoted local part with an at sign", `"weird@local"@demo.test`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := store.AllowlistMatches(patterns, tc.addr); got != tc.want {
				t.Errorf("AllowlistMatches(%q) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

// TestAllowlistMatchesUnicodeIsNotFolded pins the reason canonicalization
// is IDNA and not a lowercase compare: a dotless i is a different domain,
// however similar it looks.
func TestAllowlistMatchesUnicodeIsNotFolded(t *testing.T) {
	if store.AllowlistMatches([]string{"demo.test"}, "user@dеmo.test") {
		t.Error("a Unicode lookalike domain matched the allowlist")
	}
	// The punycode a sender would actually resolve is the same domain as
	// the Unicode spelling of the pattern.
	if !store.AllowlistMatches([]string{"bücher.test"}, "user@xn--bcher-kva.test") {
		t.Error("punycode did not match the Unicode allowlist entry it encodes")
	}
}

// TestAllowlistMatchesSkipsUnusablePatterns proves a stored pattern that
// no longer canonicalizes cannot make everything match, and cannot stop
// the entries after it from matching either.
func TestAllowlistMatchesSkipsUnusablePatterns(t *testing.T) {
	patterns := []string{"not a domain at all", "demo.test"}
	if !store.AllowlistMatches(patterns, "user@demo.test") {
		t.Error("an unusable pattern stopped a later, valid one from matching")
	}
	if store.AllowlistMatches([]string{"not a domain at all"}, "user@anything.test") {
		t.Error("an unusable pattern matched an unrelated address")
	}
}
