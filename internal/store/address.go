package store

import "strings"

// NormalizeAddress lowercases the domain part of an email address and
// trims surrounding space.
//
// SMTP treats the domain part as case-insensitive, so a target app sending
// to User@Demo.Test must land in the same inbox as a matcher asking
// for user@demo.test. The local part is left alone: RFC 5321 makes it
// case-sensitive, and the columns carry a NOCASE collation so lookups
// stay lenient without this function rewriting what the sender wrote.
func NormalizeAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return addr
	}
	return addr[:at] + "@" + strings.ToLower(addr[at+1:])
}
