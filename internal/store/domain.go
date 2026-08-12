package store

import (
	"fmt"
	"strings"

	"golang.org/x/net/idna"
)

// WildcardPrefix marks an allowlist pattern that covers every subdomain of
// its base domain, as written in authstunt.yaml.
const WildcardPrefix = "*."

// canonicalProfile is the one IDNA configuration in the product (design
// 4.2 item 17). It is spelled out rather than taken from idna.Lookup
// because that profile's documentation reserves the right to change its
// options, and a canonicalization that shifts under us would silently
// change what an existing stored allowlist matches.
//
//   - MapForLookup: NFC plus case folding plus the UTS #46 mappings, and
//     rejects anything outside RFC 1034's letter-digit-hyphen ASCII.
//   - BidiRule: RFC 5893, so a right-to-left label cannot be assembled
//     into something that displays as a different domain.
//   - VerifyDNSLength: rejects the empty domain, empty labels, and labels
//     past 63 bytes, none of which the Lookup profile rejects on its own.
var canonicalProfile = idna.New(
	idna.MapForLookup(),
	idna.BidiRule(),
	idna.VerifyDNSLength(true),
)

// CanonicalDomain returns the comparable form of a domain: IDNA to ASCII,
// then ASCII lowercase.
//
// Every domain comparison in the product runs on this form. SQLite's
// NOCASE collation folds ASCII only, so a persona at Bücher.test and a
// recipient at BÜCHER.TEST would otherwise miss each other, and an
// allowlist entry written in Unicode would never match the punycode the
// sending app actually resolved.
func CanonicalDomain(domain string) (string, error) {
	d := strings.TrimSpace(domain)
	// A single trailing dot is the DNS root and carries no meaning here;
	// "demo.test." and "demo.test" are the same domain. Any other
	// empty label is a typo and stays an error.
	d = strings.TrimSuffix(d, ".")
	ascii, err := canonicalProfile.ToASCII(d)
	if err != nil {
		return "", fmt.Errorf("store: domain %q is not usable: %w", domain, err)
	}
	return asciiLower(ascii), nil
}

// CanonicalDomainPattern canonicalizes an allowlist pattern, keeping a
// leading "*." wildcard.
//
// The wildcard label is not a legal DNS label, so it is held aside while
// the base domain goes through IDNA rather than being fed to it.
func CanonicalDomainPattern(pattern string) (string, error) {
	p := strings.TrimSpace(pattern)
	base, wildcard := strings.CutPrefix(p, WildcardPrefix)
	if strings.Contains(base, "*") {
		return "", fmt.Errorf("store: allowlist pattern %q: a wildcard is only allowed as the leading %q label",
			pattern, WildcardPrefix)
	}
	canonical, err := CanonicalDomain(base)
	if err != nil {
		return "", err
	}
	if wildcard {
		return WildcardPrefix + canonical, nil
	}
	return canonical, nil
}

// PatternBaseDomain returns the domain a pattern stands for: itself, or
// the part after "*." for a wildcard.
//
// Persona generation picks the first allowlisted pattern and builds an
// address under it, and "user@*.demo.test" is not an address. The
// wildcard's own base domain is allowlisted by the pattern that declared
// it, so it is the address to generate under.
func PatternBaseDomain(pattern string) string {
	return strings.TrimPrefix(strings.TrimSpace(pattern), WildcardPrefix)
}

// AllowlistMatches reports whether an address belongs to a domain the
// project owns.
//
// The patterns are the stored allowlist, already canonical. The address is
// whatever a sender put in an envelope, so its domain is canonicalized here
// before comparison: a sender that resolved an IDN to punycode and an
// allowlist written in Unicode describe the same domain and must match. An
// address that cannot be canonicalized at all matches nothing, which is the
// safe answer - it lands in quarantine rather than in a project's inbox.
//
// A wildcard pattern covers its own base domain as well as every subdomain,
// because "*.demo.test" is written to mean "this domain and anything
// under it" and a persona is generated at the base itself.
func AllowlistMatches(patterns []string, addr string) bool {
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return false
	}
	domain, err := CanonicalDomain(addr[at+1:])
	if err != nil {
		return false
	}
	for _, p := range patterns {
		base, wildcard := strings.CutPrefix(strings.TrimSpace(p), WildcardPrefix)
		canonical, err := CanonicalDomain(base)
		if err != nil {
			// A stored pattern that no longer canonicalizes cannot match
			// anything. Skipping it is not the same as matching it.
			continue
		}
		if domain == canonical {
			return true
		}
		if wildcard && strings.HasSuffix(domain, "."+canonical) {
			return true
		}
	}
	return false
}

// asciiLower lowercases ASCII bytes and leaves every other byte alone.
//
// strings.ToLower applies Unicode case folding, which on a value that
// failed IDNA could still map two distinct inputs onto one output. The
// canonical form is defined as ASCII lowercase, so that is what this does.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
