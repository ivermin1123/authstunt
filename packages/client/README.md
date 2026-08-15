# @authstunt/client

Typed, zero-dependency TypeScript client for the AuthStunt `/api/v1` surface:
open a run, lease an identity, claim the one message a code is bound to.

Not published yet. It ships alongside the first public release.

```ts
const lease = await run.lease({ role: 'signup' })
await signUp(lease.addr)
const { value: otp } = await lease.claim('email_otp', { timeoutMs: 15_000 })
```

Full context:

```ts
import { authstunt } from '@authstunt/client'

const client = authstunt({
  baseUrl: 'http://127.0.0.1:8925',
  bearer: process.env.AUTHSTUNT_BEARER!,
})
const run = await client.run()            // from here every call uses the run token
const lease = await run.lease({ role: 'signup' })
await signUp(lease.addr)
const { value: otp } = await lease.claim('email_otp', { timeoutMs: 15_000 })
// otp is the code; claim.messageId is the ONE message it is bound to
await lease.release()
```

## Surface

- `claim(kind, opts)` waits for a fresh value and throws a typed error on
  every non-ok outcome, so a broken claim turns a test red on its own. The
  reason code sits in the error name, the message and the `reason` field.
- `tryClaim(kind, opts)` is the same call with a union answer and no throw,
  for asserting on a reason code without try/catch.
- `ClaimError` is the abstract root; `ClaimRefusedError` (the server said
  no, by name) and `ClaimTimeoutError` (the wait ran out) are its two
  children. Every outcome carries `waitedMs` and `timedOut`.
- The reason vocabulary is the server's frozen set, exposed verbatim. It is
  append-only on the server, so an unknown future code still arrives as a
  `ClaimRefusedError` rather than a parse error.
- `timeoutMs` defaults to 30000 with the server cap of 120000. Values
  outside 0..120000 throw a `RangeError` before any request is made.
- Duplicate rule, inherited from the server: candidates are handed over
  oldest first. When two codes are in flight on one lease, the first claim
  is backed by the earlier message and the next claim gets the later one.

## Wire behavior worth knowing

- A claim that waits out its deadline is HTTP 200 with `timed_out: true` on
  the wire; the server is not wrong, the mail just never became claimable.
  `claim()` still throws `ClaimTimeoutError`, because for a test that is the
  result. `tryClaim()` keeps the wire semantics.
- The abort watchdog for a claim sits a margin above the requested wait,
  never below it, so an honest long-poll is not misread as a dead socket. A
  socket that actually dies, or a 5xx, is retried up to two more times under
  the SAME idempotency key: the server replays the first answer instead of
  consuming a second message. Only the claim route retries. The whole call,
  retries included, is hard-stopped at `timeoutMs` plus one 30s margin;
  past that the caller gets a transport error rather than a late retry
  that the server's 120s replay window may no longer answer.
- The project bearer is used once, to create the run. Everything after runs
  on the run token the server minted for that run.

## Decisions

- Build: tsup, dual ESM+CJS with a declaration file per format, verified by
  publint and arethetypeswrong in CI. A package that only resolves under one
  resolver is the failure mode this guards against.
- Tests: node:test through tsx. No test framework dependency; the runtime
  dependency count stays zero either way.
- Runtime: global fetch, so engines is `node >= 18`. The only builtin import
  is `node:crypto` for the idempotency key default.

## Development

```sh
npm ci
npm run verify   # typecheck, lint, build, publint + attw, tests
```

The tests build and run the real server binary from this repository (Go
toolchain required), provision a data dir with `--out`, drive real SMTP into
it and claim through this client. No HTTP is mocked.

Developing needs Node 20.11+ (the test harness uses `import.meta.dirname`);
the shipped package itself runs on 18.
