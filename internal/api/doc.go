// Package api serves the HTTP surface a validation harness drives
// AuthStunt through, mounted under /api/v1.
//
// # Frozen core
//
// Four routes are frozen. They are the minimal path a client needs, from
// opening a run to handing the identity back, and every field named here
// is a commitment:
//
//	F1  POST   /api/v1/runs
//	    request:  empty body
//	    response: 201 with run_id, run_token, checkpoint_at, expires_at
//
//	F2  POST   /api/v1/runs/{run_id}/leases
//	    request:  role (required); mode, where the frozen contract covers
//	              "" and "ephemeral" only. "pooled" is accepted but
//	              unsupported and sits outside the freeze.
//	    response: 201 with lease_id, identity_id, addr, role, mode,
//	              seed_state, pooled_policy, expires_at
//
//	F3  POST   /api/v1/leases/{lease_id}/claims
//	    request:  kind ("email_otp" or "magic_link"), idempotency_key
//	              (required), timeout_ms (0 to 120000)
//	    response: 200 with reason, claim_id, message_id, value, timed_out,
//	              waited_ms; value and message_id are present only when
//	              reason is claim_ok
//
//	F4  DELETE /api/v1/leases/{lease_id}
//	    response: 204; releasing twice is not an error
//
// Frozen along with the routes:
//
//   - The reason code vocabulary in package store. An existing code is
//     never renamed and never changes meaning; the set only grows.
//   - The error envelope, {"error":{"code","message"}}. The structure and
//     the code vocabulary are frozen; message is prose and may change.
//   - Authentication: the Bearer scheme, the project bearer as the only
//     principal that can create a run, and a run token confined to its
//     own run.
//   - Long-poll semantics on F3: a claim that waits out its deadline is a
//     200 with timed_out true, not an HTTP error.
//
// Append-only means a response may gain new optional fields, and request
// fields may gain new optional values, but an existing field is never
// renamed, retyped, repurposed or removed, and a documented status code
// never changes. A client that ignores fields it does not know stays
// correct across every v1 release. A change that cannot fit that rule
// gets a new prefix (/api/v2); it does not bend this one.
//
// # Provisional remainder
//
// Everything else under /api/v1 is provisional until the full surface
// freeze: POST /api/v1/runs/{run_id}/end, GET /api/v1/runs/{run_id}/evidence,
// the healthz body, pooled mode semantics, and the totp claim kind (a
// refused stub). Paths and fields there may still change without a major
// version.
//
// What is not provisional in either half is the security behavior:
// principal scoping, the loopback default, the Host allowlist, the Origin
// check and redaction all ship here in full, because a real application's
// real credentials pass through this surface.
package api
