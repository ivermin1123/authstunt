// Package relayconf checks that an inbound mail relay carried a message to
// AuthStunt without changing the parts AuthStunt has to read.
//
// The check exists because the failure it catches is invisible at the wrong
// layer. A relay that rewrites links for click tracking still delivers a
// well-formed message on time, and AuthStunt still ingests it; what breaks is
// that the magic link now points at the tracker instead of the auth provider,
// so a claim hands the caller a URL that authenticates nobody. The symptom
// looks exactly like an extraction bug, which is the most expensive place to
// go looking for it.
//
// # Why a canonical diff rather than a byte comparison
//
// "The message must arrive byte-for-byte identical" is the wrong acceptance
// rule: it fails every conforming relay. A relay is required by RFC 5321 to
// prepend a Received header, it may legitimately re-encode a body between
// quoted-printable and base64, and line endings are routinely rewritten in
// transit. A test that reports those as violations reports noise, and a noisy
// test gets ignored precisely when it starts telling the truth.
//
// So the comparison runs on a canonical form, and the rules are split by what
// each field is for:
//
//   - Hop metadata (Received, Return-Path, Authentication-Results, DKIM and
//     ARC signatures, Delivered-To) is expected to change and is ignored.
//   - Identity and threading headers (From, To, Subject, Message-ID) must
//     survive canonicalization unchanged.
//   - Body parts must survive with their semantic content intact: transfer
//     encoding, line endings, trailing whitespace and Unicode normal form may
//     differ, wording may not.
//   - The OTP and every link URL must be byte-exact. These are the two values
//     the product exists to deliver, and there is no benign transformation of
//     either. They are compared before canonicalization can touch them.
//
// # The nine rules
//
// Compare emits exactly these rule IDs, in this order, one finding each:
//
//	otp_exact             the one-time code, compared before canonicalization
//	links_exact           every link URL, exactly and in order
//	verify_link_exact     the link a caller is handed to complete the flow
//	headers_preserved     From, To, Subject and Message-ID after canonicalization
//	text_part_preserved   the plain-text part, semantic content intact
//	html_part_preserved   the HTML part, semantic content intact
//	multipart_alternative both parts of a text+html message survived
//	extraction_parity     the catch-all for anything the rules above did not name
//	hop_headers           informational: the metadata a relay is meant to add
//
// A rule that the fixture cannot exercise reports skipped rather than passing
// quietly, so the count of rules is stable across every run and a report can be
// read against this list.
//
// # What a pass does and does not mean
//
// This package is relay compatibility test infrastructure. It is not evidence
// that any relay conforms, and a green run against the fixtures bundled here
// proves only that the rules tell a conforming capture apart from a hostile
// one. Conformance is a statement about a specific relay, and it can only be
// made after that relay exists and its own before/after captures have been run
// through these rules.
//
// A pass on real captures means the relay preserved what AuthStunt reads, for
// the messages it was given. It is not a claim about deliverability, latency,
// or any message shape absent from the corpus. Compare is a pure function over
// two captures; it never talks to a relay and cannot tell you one is running.
package relayconf
