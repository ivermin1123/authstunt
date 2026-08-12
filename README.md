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
stores the message encrypted, and extracts the OTP and the links. There is no
HTTP API, no dashboard and no client library yet, so nothing outside the
process can read a result. Those land in the phases after this one.

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
