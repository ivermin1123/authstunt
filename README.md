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

Status: P0 (foundation). Not usable yet.

## Layout

- `cmd/authstunt` - main binary
- `internal/*` - server internals (smtp, extract, api, mcp, store, secrets, flows, personas, ledger, sse)
- `web/` - dashboard (P1)
- `packages/` - `@authstunt/playwright` and `@authstunt/client` (P2)
- `examples/demo-app` - demo target app for integration tests (P1)
- `docs/` - documentation (published in P5)

## Development

Requires Go 1.26+. `go build ./...` builds, `go test -race ./...` tests,
`golangci-lint run` lints. CI runs all three plus a cross-platform build
matrix and a goreleaser config check.
