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
