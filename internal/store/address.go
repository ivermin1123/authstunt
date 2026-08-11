package store

import "strings"

// NormalizeAddress canonicalizes the domain part of an email address and
// trims surrounding space.
//
// SMTP treats the domain part as case-insensitive, so a target app sending
// to User@Demo.Test must land in the same inbox as a matcher asking
// for user@demo.test. It goes through CanonicalDomain, so an
// internationalized domain lands in the same punycode form no matter which
// spelling the sender used. The local part is left alone: RFC 5321 makes it
// case-sensitive, and the columns carry a NOCASE collation so lookups
// stay lenient without this function rewriting what the sender wrote.
//
// A domain that cannot be canonicalized falls back to ASCII lowercase
// rather than being rejected. This function is on the ingest path, where
// mail has already been accepted: dropping an address here would lose the
// message. Deciding whether an address is allowed is the allowlist's job,
// and a domain that fails IDNA cannot match an allowlist entry, which
// canonicalizes on the way in.
func NormalizeAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return addr
	}
	local, domain := addr[:at], addr[at+1:]
	canonical, err := CanonicalDomain(domain)
	if err != nil {
		canonical = asciiLower(strings.TrimSpace(domain))
	}
	return local + "@" + canonical
}
