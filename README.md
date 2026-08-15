# AuthStunt

[![ci](https://github.com/ivermin1123/authstunt/actions/workflows/ci.yml/badge.svg)](https://github.com/ivermin1123/authstunt/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![go 1.26](https://img.shields.io/badge/go-1.26-00ADD8.svg)](go.mod)

**The `acquireAccount()` the Playwright docs tell you to write yourself.**

Playwright's authentication guide hands you the shape for one account per
parallel worker and then stops:

```ts
const account = await acquireAccount(id);
// Acquire a unique account, for example create a new one.
```

The function is yours to build. [Issue #18062][pw18062] asked Playwright for a
synchronized user pool ("if a worker is using one user, don't make them
available for another"), was assigned to a maintainer, and was closed in
September 2025 with the note that it lacked "engagement (upvotes/activity)" and
was of "insufficient actionability". Four more issues ([#27749][pw27749],
[#27213][pw27213], [#35111][pw35111], [#30566][pw30566]) are people failing to
write it themselves.

AuthStunt is that function, packaged. One self-hosted binary owns the test
identities, catches their mail over real SMTP, extracts the code, and hands it
to exactly one claimant. `@authstunt/playwright` wires it in as fixtures: **one
run per worker, one identity per test, released even when the test fails**.

You never write Go. You run a container and install a package.

Scope, said narrowly: this covers the **signup and verification** half of
`acquireAccount()`, where each worker needs a fresh identity nobody else holds.
Leasing accounts that already exist inside your application is pooled mode, and
pooled mode is accepted but unsupported today.

[pw18062]: https://github.com/microsoft/playwright/issues/18062
[pw27749]: https://github.com/microsoft/playwright/issues/27749
[pw27213]: https://github.com/microsoft/playwright/issues/27213
[pw35111]: https://github.com/microsoft/playwright/issues/35111
[pw30566]: https://github.com/microsoft/playwright/issues/30566

## Quickstart

Two container commands and a test file. Nothing to sign up for.

Mint the credential the API authenticates with. It prints once, on the
terminal, and is not recoverable afterwards:

```
docker run --rm -it -v authstunt:/data ghcr.io/ivermin1123/authstunt \
  project bearer provision --data-dir /data --project demo --domain demo.test
```

```
pb_<the value, printed once and never shown again>
```

Start the server:

```
docker run --rm -v authstunt:/data -p 1025:1025 -p 8925:8925 \
  ghcr.io/ivermin1123/authstunt
```

Install the fixtures and point them at it:

```
npm i -D @authstunt/playwright @authstunt/client
export AUTHSTUNT_URL=http://127.0.0.1:8925
export AUTHSTUNT_BEARER=<the pb_ value printed above>
```

Then a whole test, in one file:

```ts
import { test, expect } from '@authstunt/playwright'

test('a new user verifies with the code that was emailed', async ({ page, lease }) => {
  // lease.addr is an address nothing else in this run holds.
  await page.goto('http://localhost:3000/signup')
  await page.fill('#email', lease.addr)
  await page.click('button[type=submit]')

  // Parks until the mail lands, so there is no sleep and no polling.
  const { value: otp } = await lease.claim('email_otp', { timeoutMs: 15_000 })

  await page.fill('#code', otp)
  await page.click('button[type=submit]')
  await expect(page.getByText('Welcome')).toBeVisible()
})
```

Point your application's SMTP at `127.0.0.1:1025`. Credentials are accepted or
omitted, either way, because the point of a test relay is not to be picky.

Run it with `npx playwright test --workers=4` and every worker gets its own run,
every test its own identity, and every identity is handed back when the test
ends, pass or fail.

## Why this exists

> **Your provider's test mode skips the exact path you wanted to test.** Clerk's
> test email addresses all verify with the fixed code `424242`, and its own
> documentation is explicit about the trade: "When testing email verification
> codes, no email with the verification code will be sent."[^clerk-test] That is
> the right call for a smoke test and the wrong one for the bug you are actually
> hunting, which lives in the template, the relay, the link, or the inbox. A
> green test on `424242` proves the form posts.

**If your auth provider sends the mail for you, start here.** Clerk and Auth0 in
their hosted default deliver their own messages, and there is no SMTP host to
repoint at AuthStunt. Clerk has no custom SMTP setting at all: sending it
yourself means turning off ["Delivered by Clerk"][clerk-templates] per template
and listening for the `email.created` webhook to send from [your own
infrastructure][clerk-deliverability]. Until you make that switch, use your
provider's test mode. AuthStunt becomes useful the day you own the sending.

**If you would rather not run anything, run nothing.** A hosted inbox service
(Mailosaur, Mailtrap) gives you addresses, a dashboard, and support, with no
container to keep alive. Take one when the test suite is small, when the team
would rather buy the problem away, and when sending test mail to a third party's
domain is fine with whoever asks that question at your company. AuthStunt is for
the other case: your domain, your disk, your CI, offline, no per-message cost,
and no rate limit imposed from outside.

**And the relay can break the link without breaking anything visible.** Send
auth mail through a relay with click tracking and the relay rewrites the URLs.
The magic link now points at the tracker instead of the auth provider. The
message still arrives, on time and well formed, and it authenticates nobody.
`internal/relayconf` exists for that failure and says why it deserves a package:

> The symptom looks exactly like an extraction bug.

That is what makes it expensive: the break is invisible at the layer you go
looking for it in. So the OTP and every link URL are compared byte for byte
between what a relay was given and what it delivered, while the headers a relay
is allowed to rewrite are ignored. The rules ship as a test package rather than
a command. Five fixtures check them here, and running
`go test ./internal/relayconf/` against your own before and after captures
answers the same question about your relay.

[^clerk-test]: Verified against Clerk's testing documentation on 2026-08-16.

[clerk-templates]: https://clerk.com/docs/guides/customizing-clerk/email-sms-templates
[clerk-deliverability]: https://clerk.com/docs/guides/development/troubleshooting/email-deliverability

## What it actually guarantees

Identity first, not mail first. Mailpit catches email. AuthStunt manages test
identities, and also catches email.

A test does not ask a mailbox what arrived. It asks for an identity, gets an
address that belongs to that identity, and later asks that identity for its
code. The mailbox is a consequence of the identity rather than the unit of work,
which is what lets a lease expire, an address be quarantined, or a claim be
bound to one message and refused to everybody else.

### A claim is bound to one message

Not a flag on a row. A claim binds one message, records the idempotency key it
was made under, and every outcome carries a reason code rather than an empty
result. A claim that hands back nothing still says which nothing it was: nothing
was ever addressed to this lease, something arrived and was refused, or the code
was already claimed by somebody else.

Exclusivity is a property of the schema, not a promise in a README. An ephemeral
address is minted once from `crypto/rand` and never reused; `identities.addr`
carries a `UNIQUE` constraint; one lease per identity is a partial `UNIQUE`
index; and one claim per message and kind is `claims_one_per_message_kind`,
also `UNIQUE`. Two racing claims do not need to agree with each other, because
the loser of an insert reads back the winner. The invariant tests for this are
merged and run under `-race` on every push.

### The resend, told honestly

A test signs up and an OTP goes out. The tester clicks resend and a second OTP
goes out. Two codes are now in flight and the application has probably
invalidated the first one.

AuthStunt picks **oldest first**, on purpose, and the rule is written down in
the code that implements it:

> Oldest first is the duplicate rule: when an application sends the same code
> twice, the first copy backs the claim and the second stays visible and
> unclaimed. Taking the newest would make a resend silently invalidate the code
> the user already typed.

So this is the promise, and it is narrower than the one you might expect:
**a duplicate is never silently swallowed, and each claim takes the next code in
arrival order.** It is not a promise that you receive the code that is currently
valid. If your application kills the old code on resend, a claim issued after
the resend hands you the dead one. That is a deliberate one-sided choice, not a
bug, and knowing which side it falls on is the difference between a test you can
debug and a test you cannot.

What you get instead is exclusivity plus a vocabulary. Two messages arrive; the
first claim takes the first, the second claim takes the second, and a third
claim gets `claim_already_claimed` while both messages stay visible in the
lease. That third outcome is an assertion you can write. There is a test in this
repository that writes it.

Compare that with a `seen` flag, which is display state, not ownership: two
waiters filtering the same way both get handed the same message, and neither is
told that it happened.

## Where it sits

|  | Provider test mode | Mail catcher | Hosted inbox service | AuthStunt |
|---|---|---|---|---|
| Examples | Clerk, Supabase, better-auth test utils | Mailpit, MailHog, maildev, smtp4dev | Mailosaur, Mailtrap | this |
| Does a real message travel the real path? | No. Fixed code, nothing sent | Yes, to a local catcher | Yes, to their domain | Yes, to a server you run |
| Who owns the address? | The provider's fixture list | Nobody. One shared catch-all | Your account | A lease held by one run, with an expiry |
| Two workers, two codes in flight | Same fixed code for everyone | Both can read the same message | Unique addresses are easy; message ownership is yours to arrange | Each claim binds one message; the next gets `claim_already_claimed` |
| Offline, no account, no domain | Yes | Yes | No | Yes |
| Cost per message | None | None | Per plan | None |

The mail catchers are why this category exists at all, and their numbers say the
audience is real: the Mailpit image has been pulled about 212 million times and
MailHog about 313 million.[^pulls] They are excellent at catching mail. They are
not trying to own identities, which is why running one has never answered "which
worker gets this code".

Multi-tenancy on a single instance has been asked of Mailpit
([#301](https://github.com/axllent/mailpit/issues/301)) and declined as not
planned. It keeps being asked because a shared catch-all box is the wrong shape
for parallel tests, and repointing SMTP is the easy part.

[^pulls]: Docker Hub pull counts read on 2026-08-16.

### Prior art

AuthStunt did not invent the ephemeral inbox. The local SMTP server with an API
in front of it is MailHog's and Mailpit's and maildev's shape, and the
throwaway address that expires on a timer is the disposable-inbox lineage. The
`"ephemeral"` lease mode here is a descendant of both, and the only thing added
on top is ownership.

The closest neighbour is [Fana](https://github.com/JastinXyz/fana), and it
deserves naming rather than silence. It is self-hosted, it runs real SMTP with
SPF, DKIM and DMARC verdicts, and it has a genuine address reservation: a
`reservations` table with a unique address, a token, an owner and an expiry, a
concurrency cap, and a release endpoint. Its own schema calls that a soft claim
on an address, which is exactly what it is.

What it does not have is a claim on a message. Its `GET /wait` filters on
mailbox, owner, sender, subject and time, orders oldest first, takes one, and
*then* marks it seen. The predicate never filters on `seen`, so two waiters with
the same filter are handed the same message.[^fana] That is the line drawn here:
a reservation holds an address, a claim owns a message.

Also worth crediting for showing where the demand is: better-auth ships its own
test utilities, and Supabase and next-auth users have been asking for this in
public for years. next-auth
[discussion #2053](https://github.com/nextauthjs/next-auth/discussions/2053)
(opened 2021) and
[#7748](https://github.com/nextauthjs/next-auth/discussions/7748) (opened 2023)
both ask how to test an email-based auth flow in CI, and neither has an accepted
answer. Every provider is patching its own half of this, one house at a time.

[^fana]: Read from `apps/api/src/routes/v1.ts` on the default branch on
    2026-08-16.

## Install

Docker first, because that is how this category is installed:

```
docker run --rm -v authstunt:/data -p 1025:1025 -p 8925:8925 \
  ghcr.io/ivermin1123/authstunt
```

The image is multi-platform, amd64 and arm64. `/data` holds the store and the
encryption key, so give it a volume or it goes away with the container.

For a JavaScript or TypeScript test suite:

```
npm i -D @authstunt/playwright @authstunt/client
```

`@authstunt/playwright` is the fixtures. `@authstunt/client` is the typed client
underneath, usable on its own from any Node test runner.

To run the server outside a container, download the archive for your platform
from the [latest release][rel], verify it, and extract it:

```
shasum -a 256 -c checksums.txt --ignore-missing
tar xzf authstunt_0.1.0_darwin_arm64.tar.gz
./authstunt version
```

Windows ships a `.zip` rather than a `.tar.gz`.

Finally, if you write Go and would rather build it:

```
go install github.com/ivermin1123/authstunt/cmd/authstunt@latest
```

Needs Go 1.26 or newer. This is the last entry on purpose. Nobody should have to
install a Go toolchain to test a signup form.

[rel]: https://github.com/ivermin1123/authstunt/releases/latest

## Talking to the API directly

The fixtures are a thin layer over four HTTP calls, and the calls are frozen, so
using them directly is a first-class option. `$BEARER` is the value provisioned
above, and it buys a short-lived run token that every later call uses instead.

Open a run, then lease an identity for it:

```
curl -sX POST http://127.0.0.1:8925/api/v1/runs \
  -H "Authorization: Bearer $BEARER"

curl -sX POST http://127.0.0.1:8925/api/v1/runs/$RUN_ID/leases \
  -H "Authorization: Bearer $RUN_TOKEN" \
  -d '{"role":"signup"}'
```

```json
{
  "lease_id": "df11513e200b",
  "identity_id": "493816489583",
  "addr": "signup-8627b23ca1e5@demo.test",
  "role": "signup",
  "mode": "ephemeral",
  "seed_state": "skipped",
  "pooled_policy": null,
  "expires_at": "2026-08-16T15:37:47.830Z"
}
```

Sign that address up in the application under test, then ask the lease for the
code:

```
curl -sX POST http://127.0.0.1:8925/api/v1/leases/$LEASE_ID/claims \
  -H "Authorization: Bearer $RUN_TOKEN" \
  -d '{"kind":"email_otp","idempotency_key":"signup-1","timeout_ms":15000}'
```

```json
{
  "reason": "claim_ok",
  "claim_id": "5105bdd2febd",
  "message_id": "59b818ca328e",
  "value": "481920",
  "timed_out": false,
  "waited_ms": 3103
}
```

`timeout_ms` is a long poll: the claim parks until the mail lands, so a test
issues it as soon as it triggers the signup rather than sleeping first and
hoping. `waited_ms` reports how long it actually waited, and `timed_out` says
whether the budget ran out. A wait that expires is a `200` with `timed_out`
true, not an HTTP error, because from the server's side nothing went wrong.

`kind` is `email_otp` or `magic_link`. `totp` is defined in the schema and
refused by the surface until it is implemented.

Release the lease when the test is done, or let the run's expiry do it:

```
curl -sX DELETE http://127.0.0.1:8925/api/v1/leases/$LEASE_ID \
  -H "Authorization: Bearer $RUN_TOKEN"
```

Releasing twice is not an error.

## Status and stability

Early alpha, with a frozen core inside it.

**Frozen.** Four routes under `/api/v1` and every field they name: create a run
(F1), lease an identity (F2), claim a code (F3), release a lease (F4). Frozen
with them are the claim reason code vocabulary, the error envelope
`{"error":{"code","message"}}`, the Bearer scheme and its principal scoping, and
the long-poll semantics on F3. These are append-only: a response may gain new
optional fields and a request field may gain new optional values, but nothing is
renamed, retyped, repurposed or removed, and a documented status code does not
change. A client that ignores fields it has not heard of stays correct across
every v1 release. The full text is in `internal/api/doc.go`.

**Provisional.** Everything else under `/api/v1`: ending a run, the evidence
route, the healthz body, and the `totp` claim kind. Paths and fields there may
still change. `@authstunt/client` touches exactly one provisional route,
`run.end()`, and says so in the comment above it.

**Accepted but unsupported.** `mode: "pooled"` on a lease. The request is
accepted and the frozen contract covers `""` and `"ephemeral"` only. Pooled mode
means handing the same address to one run after another, which needs a cooldown
policy that has no safe default; treat it as reserved surface rather than a
feature.

**Not written yet.** The dashboard, the YAML flow loader, the MCP server, and
TOTP.

**No retention, and this is worth knowing before you point a long-running suite
at an instance.** Nothing is ever deleted. Every message keeps its row, roughly
two encrypted blob files, and its ledger entries for as long as the data
directory exists. The periodic sweep expires runs and leases so their identities
return to the pool; it frees no disk. Size tracks how much mail an instance has
ever accepted, not how old it is, and the remedy today is to delete the data
directory or give each suite a fresh one.

## Operating it

### The project bearer

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
evidence; only its SHA-256 digest is stored. Move it into a secret manager from
the terminal that printed it.

By default the value is only shown on a terminal. A pipe, a redirect to a file,
or a CI job is refused, because the credential would land in whatever collects
that output. `--out <path>` is the way to provision where no terminal exists: it
skips stdout entirely and writes to a file created with mode `0600` whose path
must not already exist. `--allow-non-tty-reveal` overrides the refusal instead
and makes the caller responsible for the destination. Both checks run before
anything is written, so a refused rotation leaves the current credential
working.

Rotation replaces the value and the previous one stops authenticating
immediately; there is never more than one live bearer for a project. Every
provision, rotation and revocation is recorded in the audit ledger as the
operation alone, so the trail says a credential changed, never which one.

An instance that serves no API needs no bearer: `--api-listen ""` runs it as a
mail catcher only.

### Running it without Docker

```
authstunt project bearer provision --project demo --domain demo.test
authstunt serve --project demo --domain demo.test
```

```
authstunt serving project demo, api 127.0.0.1:8925, long-poll on, pooled off, seeder off, smtp 127.0.0.1:1025
```

The startup line reports the capabilities that are actually wired, not the flags
that asked for them, so an instance running with less than you expected says so
at startup instead of at the first test that depends on it.

### The data directory

The first run initializes the data directory (`~/.authstunt/<project>` by
default, `--data-dir` overrides) with the project and its ordered domain
allowlist, and creates the encryption key that every stored body and extraction
result is sealed with. Later runs may repeat the flags, which must match what is
stored, or omit them; serve never silently reconciles a difference.

An SMTP 250 means the message is on disk. It survives the server process dying
and it survives the machine losing power: the database runs in WAL mode with
`synchronous=FULL`, so the commit that backs the ack fsyncs the WAL, and the
blobs are fsynced before it.

`serve --sync-mode=normal` trades that back for the older, narrower promise, the
process and not the machine, with power loss covered only as far as the last
checkpoint. It is worth about 1ms at p95 per message and nothing outside noise
across a whole suite, so take it only if you know why you want it. A server
started that way announces `sync normal` on its startup line, so a CI log
records the trade rather than hiding it.

Ack latency itself is around 7ms at p50 and 9ms at p95 in either mode. It is set
by the blob fsyncs and by waiting for the single writer, not by this pragma, so
changing the mode is not the lever for making delivery faster.

### Mail for an address you did not allow

A message addressed to any recipient outside the allowlist is accepted, stored,
and quarantined: kept as evidence and held back from the automated read path, so
a staging app that copies a real customer address cannot hand that person's mail
to a test.

## Layout

- `cmd/authstunt` - main binary
- `internal/*` - server internals: smtp, extract, api, store, secrets,
  personas, ledger, sse, relayconf, fsutil
- `internal/mcp` - MCP server (not yet written)
- `internal/flows` - YAML flow loading and lint (not yet written)
- `packages/client` - `@authstunt/client`, the typed TypeScript client
- `packages/playwright` - `@authstunt/playwright`, the fixtures
- `web/` - dashboard (not yet written)
- `examples/demo-app` - demo target app for integration tests (not yet written)
- `docs/` - documentation (not yet written)

## Development

Requires Go 1.26+. `go build ./...` builds, `go test -race ./...` tests,
`golangci-lint run` lints. The TypeScript packages each run `npm run verify`,
which type-checks, lints, builds, smoke-tests both module formats, and checks
the published shape with publint and arethetypeswrong before the tests.

CI runs all of it, repeats the Go tests on Windows (where the key file is
protected by an ACL rather than mode bits), and adds a cross-platform build
matrix and a goreleaser config check.

Development uses AI assistance under the quality bar documented in `CLAUDE.md`.

## License

MIT. See [LICENSE](LICENSE).
