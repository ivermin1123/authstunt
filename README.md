# AuthStunt

A self-hosted test identity manager for E2E testing in the agent era. One Go
binary that owns everything about who is testing: personas, declarative auth
flows, a catch-all mail server with OTP extraction, a virtual TOTP
authenticator, a Playwright session vault, and a passive audit ledger.

Test personas are stunt doubles for your real users: they take every risky
auth scene (signups, OTP retries, broken flows) so no real identity ever
appears in a test environment.

AuthStunt is the brain for authentication during tests. It never drives a
browser. Playwright and AI agents (via MCP) are the hands.

Status: early alpha. The mail path works end to end - the server accepts SMTP,
stores the message encrypted, and extracts the OTP and the links. A provisional
HTTP API is bound on loopback for validation work; its paths and fields are not
frozen and may change. There is no dashboard and no client library yet.

## Running it

```
authstunt serve --project demo --domain demo.test
```

The first run initializes the data directory (`~/.authstunt/<project>` by
default, `--data-dir` overrides) with the project and its ordered domain
allowlist, and creates the encryption key that every stored body and
extraction result is sealed with. Later runs may repeat the flags, which must
match what is stored, or omit them; serve never silently reconciles a
difference.

SMTP listens on `127.0.0.1:1025` by default (`--smtp-listen` overrides). Point
the application under test at it and drop the credentials or keep them -
authentication is accepted either way.

A message addressed to any recipient outside the allowlist is accepted,
stored, and quarantined: it is kept as evidence and held back from the
automated read path, so a staging app that copies a real customer address
cannot hand that person's mail to a test.

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

## Layout

- `cmd/authstunt` - main binary
- `internal/*` - server internals (smtp, extract, api, mcp, store, secrets, flows, personas, ledger, sse, fsutil)
- `web/` - dashboard (P1)
- `packages/` - `@authstunt/playwright` and `@authstunt/client` (P2)
- `examples/demo-app` - demo target app for integration tests (P1)
- `docs/` - documentation (published in P5)

## Development

Requires Go 1.26+. `go build ./...` builds, `go test -race ./...` tests,
`golangci-lint run` lints. CI runs all three, repeats the tests on Windows
(where the key file is protected by an ACL rather than mode bits), and adds a
cross-platform build matrix and a goreleaser config check.

Development uses AI assistance under the quality bar documented in
`CLAUDE.md`.
