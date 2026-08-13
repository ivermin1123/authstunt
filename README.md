# AuthStunt

[![ci](https://github.com/ivermin1123/authstunt/actions/workflows/ci.yml/badge.svg)](https://github.com/ivermin1123/authstunt/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![go 1.26](https://img.shields.io/badge/go-1.26-00ADD8.svg)](go.mod)

A self-hosted test identity manager for end-to-end auth testing. One Go binary
that owns who is testing: personas that each hold a real mailbox, a catch-all
SMTP server that extracts the OTP and the links, and a passive audit ledger.

Test personas are stunt doubles for your real users. They take every risky auth
scene (signups, OTP retries, broken flows) so no real identity ever appears in
a test environment.

AuthStunt is the brain for authentication during tests. It never drives a
browser. Playwright and AI agents are the hands.

## The problem it was built for

Not every auth provider will send your mail for you. Clerk has no custom SMTP:
to send auth mail yourself you turn off "Delivered by Clerk" per template,
listen for the `email.created` webhook, and send the message from your own
infrastructure.

Send it through a relay with click tracking and the relay rewrites the links.
The magic link now points at the tracker rather than at the auth provider. The
message still arrives, on time and well formed, and it authenticates nobody.

`internal/relayconf` exists because of that failure, and says why it is worth a
package:

> The symptom looks exactly like an extraction bug.

That is what makes it expensive. The break is invisible at the layer you go
looking for it in. So the OTP and every link URL are compared byte for byte
between what a relay was given and what it delivered, while headers a relay is
allowed to rewrite are ignored.

## What makes it different

Identity-first, not mail-first. Mailpit catches email. AuthStunt manages test
identities, and also catches email.

A test does not ask a mailbox what arrived. It asks for an identity, gets an
address that belongs to that identity, and later asks that identity for its
code. The mailbox is a consequence of the identity rather than the unit of
work, which is what lets a lease expire, an address be quarantined, or a claim
be bound to one message and refused to everyone else.

## Status

Early alpha. The mail path works end to end: the server accepts SMTP, stores
the message encrypted, extracts the OTP and the links, and hands them to the
run that leased the address. A provisional HTTP API is bound on loopback for
validation work; its paths and fields are not frozen and may change. There is
no dashboard and no client library yet, TOTP is defined in the schema and
refused by the surface, and YAML flows and the MCP server are not written.

## Install

Download the archive for your platform from the [latest release][rel], verify
it against `checksums.txt`, then extract it:

```
tar xzf authstunt_0.1.0_darwin_arm64.tar.gz
./authstunt version
```

Windows ships a `.zip` rather than a `.tar.gz`.

Verify the download before trusting it:

```
shasum -a 256 -c checksums.txt --ignore-missing
```

Or build from source, which needs Go 1.26 or newer:

```
go install github.com/ivermin1123/authstunt/cmd/authstunt@latest
```

[rel]: https://github.com/ivermin1123/authstunt/releases/latest

## Quickstart

Initialize a project and mint the credential its API will use. On a terminal
the value is printed once and is not recoverable afterwards, so move it into a
secret manager now:

```
authstunt project bearer provision --project demo --domain demo.test
```

Then run it:

```
authstunt serve --project demo --domain demo.test
```

```
authstunt serving project demo, api 127.0.0.1:1080, smtp 127.0.0.1:1025
```

Point the application under test at SMTP on `127.0.0.1:1025` and either drop
the credentials or keep them, since authentication is accepted either way.

## Getting a code out of a test

The calls below are the provisional API described under Status: they work
today, and their paths and fields may change before the surface is frozen.
`$BEARER` is the value provisioned above, and it buys a short-lived run token
that every later call uses instead.

Open a run, then lease an identity for it:

```
curl -sX POST http://127.0.0.1:1080/api/runs \
  -H "Authorization: Bearer $BEARER"

curl -sX POST http://127.0.0.1:1080/api/runs/$RUN_ID/leases \
  -H "Authorization: Bearer $RUN_TOKEN" \
  -d '{"role":"signup"}'
```

```json
{
  "lease_id": "732ca49f57d4",
  "identity_id": "67f9b32d4731",
  "addr": "signup-2a774ec42f89@demo.test",
  "role": "signup",
  "mode": "ephemeral",
  "seed_state": "skipped",
  "pooled_policy": null,
  "expires_at": "2026-08-13T15:14:36.295Z"
}
```

Sign that address up in the application under test. When its mail arrives, ask
the lease for the code:

```
curl -sX POST http://127.0.0.1:1080/api/leases/$LEASE_ID/claims \
  -H "Authorization: Bearer $RUN_TOKEN" \
  -d '{"kind":"email_otp","idempotency_key":"signup-1","timeout_ms":0}'
```

```json
{
  "reason": "claim_ok",
  "claim_id": "2cac009b14df",
  "message_id": "f2f0fb9c1f3a",
  "value": "481920",
  "timed_out": false,
  "waited_ms": 0
}
```

`kind` is `email_otp` or `magic_link`; `totp` is defined in the schema and
refused until it is implemented. A claim is bound to one message, and every
outcome carries a reason code rather than an empty result, so a claim that
hands back nothing still says whether nothing was ever addressed to the lease,
something arrived and was refused, or the code was already claimed.

The API authenticates callers with a project bearer, and serve refuses to bind
the API for a project that has none. See [the project bearer] for how one is
provisioned, rotated, and kept out of logs.

[the project bearer]: #the-project-bearer

## Mail for an address you did not allow

A message addressed to any recipient outside the allowlist is accepted,
stored, and quarantined: it is kept as evidence and held back from the
automated read path, so a staging app that copies a real customer address
cannot hand that person's mail to a test.

## Layout

- `cmd/authstunt` - main binary
- `internal/*` - server internals: smtp, extract, api, store, secrets,
  personas, ledger, sse, relayconf, fsutil
- `internal/mcp` - MCP server (P2, not yet written)
- `internal/flows` - YAML flow loading and lint (P3, not yet written)
- `web/` - dashboard (P1, not yet written)
- `packages/client` - `@authstunt/client` (P2, not yet written)
- `packages/playwright` - `@authstunt/playwright` (P2, not yet written)
- `examples/demo-app` - demo target app for integration tests (P1, not yet
  written)
- `docs/` - documentation (P5, not yet written)

## The project bearer

The HTTP API authenticates callers with a project bearer, and serve refuses to
bind the API for a project that has none. serve never creates one and never
prints one: it is a long-running process, so its output ends up in a CI log, a
supervisor journal or a log shipper, and a credential must not be carried into
any of them.

Provisioning is a command an operator runs once, deliberately:

```
authstunt project bearer provision --data-dir ~/.authstunt/demo
authstunt project bearer rotate    --data-dir ~/.authstunt/demo
authstunt project bearer revoke    --data-dir ~/.authstunt/demo
```

The value is printed once, on stdout, and is not recoverable afterwards. It is
never written into the data directory, never logged, and never present in
evidence - only its SHA-256 digest is stored. Move it into a secret manager
from the terminal that printed it.

By default the value is only shown on a terminal. A pipe, a redirect to a file,
or a CI job is refused, because the credential would land in whatever collects
that output. `--allow-non-tty-reveal` overrides the refusal and makes the
caller responsible for the destination. The check runs before anything is
written, so a refused rotation leaves the current credential working.

Rotation replaces the value and the previous one stops authenticating
immediately; there is never more than one live bearer for a project. Every
provision, rotation and revocation is recorded in the audit ledger as the
operation alone - the trail says a credential changed, never which one.

An instance that serves no API needs no bearer: `--api-listen ""` runs it as a
mail catcher only.

## Data directory

The first run initializes the data directory (`~/.authstunt/<project>` by
default, `--data-dir` overrides) with the project and its ordered domain
allowlist, and creates the encryption key that every stored body and
extraction result is sealed with. Later runs may repeat the flags, which must
match what is stored, or omit them; serve never silently reconciles a
difference.

## Development

Requires Go 1.26+. `go build ./...` builds, `go test -race ./...` tests,
`golangci-lint run` lints. CI runs all three, repeats the tests on Windows
(where the key file is protected by an ACL rather than mode bits), and adds a
cross-platform build matrix and a goreleaser config check.

Development uses AI assistance under the quality bar documented in
`CLAUDE.md`.

## License

MIT. See [LICENSE](LICENSE).
