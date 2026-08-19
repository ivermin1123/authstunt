# AuthStunt

[![ci](https://github.com/ivermin1123/authstunt/actions/workflows/ci.yml/badge.svg)](https://github.com/ivermin1123/authstunt/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![go 1.26](https://img.shields.io/badge/go-1.26-00ADD8.svg)](go.mod)

**The `acquireAccount()` the Playwright docs tell you to write yourself.**

Playwright's authentication guide hands you the shape for one account per
parallel worker in an end-to-end (E2E) test suite, and then stops:

```ts
// Acquire a unique account, for example create a new one.
// Alternatively, you can have a list of precreated accounts for testing.
// Make sure that accounts are unique, so that multiple team members
// can run tests at the same time without interference.
const account = await acquireAccount(id);
```

Said outside the code block, because that comment is the requirement: your tests
have to run **at the same time without interference**. One test must not leave
another one logged out, and two must not burn the same code. AuthStunt does not
manage your application's sessions - it removes the shared thing those failures
need, so that **every worker and every test gets its own identity, and they do
not step on each other**.

The function is yours to build. [Issue #18062][pw18062] asked Playwright for
exactly that handout, in the words of somebody whose parallel workers were
burning the same OTP: "if a worker is using one user, don't make them available
for another user, instead provide them next user from the test data". It was
assigned to a maintainer and closed in September 2025 "due to limited engagement
(upvotes/activity), lack of recent activity, and insufficient actionability".
Four more issues ([#27749][pw27749], [#27213][pw27213], [#35111][pw35111],
[#30566][pw30566]) are people failing to write it themselves.

AuthStunt is that function, packaged. One self-hosted binary owns the test
identities, catches their mail over real SMTP, extracts the code, and hands it
to exactly one claimant. `@authstunt/playwright` wires it in as fixtures: **one
run per worker, one identity per test, released even when the test fails**.

Why bother, as one measurement: across **284** public repositories that use a
hosted identity provider and have end-to-end tests, exactly **1** reads a real
verification code out of a real inbox, and **35** build their own way around the
flow instead. The write-up is at [authstunt.com](https://authstunt.com/); the
corpus, the detection scripts and the per-case verdicts ship in
[`research/post-1-repro-kit`](research/post-1-repro-kit).

Two benefits come with that, in the order most suites need them:

1. **A real message travels the real path.** Your template renders, your
   provider or relay carries it, and the code your test types is the code that
   was really delivered. A provider test mode that answers with a fixed code
   proves the form posts and nothing after it.
2. **Exclusivity.** A claim binds one message, so two parallel workers cannot
   burn the same code.

The second is why the Playwright issue above exists. The first is why this is
worth running even at `workers: 1`: **a serial suite gets nothing from
exclusivity and everything from the mail being real.**

You never write Go. You run a container and install a package.

Scope, said narrowly: this covers the **signup and email-verification** half of
`acquireAccount()` in an **end-to-end (E2E) test suite**, where each parallel
worker needs a fresh identity nobody else holds.
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

If your application has no SMTP host to repoint - because it sends over an HTTP
API, or because a provider or another team sends for it - that line does not
fit you, and the fix is a paragraph away in
[four mail paths](#four-mail-paths-and-which-one-you-are-on).

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

Most suites do not read the message at all. Across 284 public repositories that
use a hosted identity provider and have end-to-end tests, thirty-five **bypass**
the flow instead, and exactly one reads a real verification code out of a real
inbox. The shapes repeat: a **forged** session cookie, a **mock** JWT
**hardcoded** into a fixture, or a switch in the harness's own
`webServer.command` named for the job - `VITE_E2E_SKIP_AUTH`,
`NEXT_PUBLIC_BYPASS_AUTH`. Teams reach for those because the alternatives look
worse: disable email verification in a test instance and assert on a fixed code,
or write the inbox plumbing yourself.

That **backdoor** is usually the right trade, and this README is not here to
argue otherwise. AuthStunt is for when it stops being one - because a bypass
removes the template, the relay, the link and the inbox from the test, and those
are exactly where auth-mail bugs live. The repositories, the detection scripts
and the per-case verdicts are in
[`research/post-1-repro-kit`](research/post-1-repro-kit); the write-up is at
[authstunt.com](https://authstunt.com/).

### Four mail paths, and which one you are on

The Quickstart line above - repoint SMTP at `127.0.0.1:1025` - assumes you have
an SMTP host to repoint. Often you do not. The question that actually decides
whether this tool fits is not which provider you use, it is **who can change
where your test mail goes.** There are four answers.

**1. You send over SMTP yourself.** The Quickstart is the whole setup. Change
the host and port in your test environment and stop reading here.

**2. You send over an HTTP API: Resend, Postmark, SES, SendGrid, Mailgun.**
This is the common shape now, and it is the case the Quickstart line does not
cover: there is no SMTP host in your configuration to repoint. It is not a
dead end, because every one of those providers also runs an SMTP relay. In your
test environment you swap the transport, not the provider.

| Provider | SMTP relay host | Ports | Credentials |
|---|---|---|---|
| Resend | `smtp.resend.com` | 25, 465, 587, 2465, 2587 | user `resend`, password is your API key |
| Postmark | `smtp.postmarkapp.com` | 25, 587, 2525 | the server API token as both user and password |
| Amazon SES | the SMTP endpoint for your region | 25, 587, 2587 (STARTTLS), 465, 2465 (TLS wrapper) | SES SMTP credentials, which are not your AWS access keys |
| SendGrid | `smtp.sendgrid.net` | 587 | user `apikey`, password is your API key |
| Mailgun | `smtp.mailgun.org` | 25, 465, 587, 2525 | per-domain SMTP credentials |

Read from each provider's own documentation on 2026-08-16.

That swap gives you two routes, and they are not worth the same:

- **Through the provider.** Send over the provider's SMTP relay exactly as
  production does, addressed to a test domain whose MX lands on infrastructure
  you control and forwards into the network where AuthStunt runs. The message
  still renders through your template and still passes through the provider, so
  a test on this path is evidence about your template and your provider, not
  only about your code. This is the honest path, not a workaround.
- **Straight at AuthStunt.** Point the SMTP transport at `127.0.0.1:1025` and
  skip the provider. It needs no DNS and no MX, which makes it the right way to
  iterate, but it removes the template rendering and provider hops that most
  auth-mail bugs live in. A green test here says less than one on the route
  above, and it is worth saying so out loud in your own notes.

The receiving side of the first route is a real cost, and it is the reason the
second exists. Do not answer it by putting the SMTP listener on the public
internet: it accepts every credential it is given by design, so anyone who
reaches it can inject a message and make a test pass for the wrong reason. See
[SECURITY.md](SECURITY.md).

**3. Your provider sends the mail for you.** Clerk and Auth0 in their hosted
default deliver their own messages, and there is no SMTP host anywhere in your
configuration. Clerk has no custom SMTP setting at all: sending it yourself
means turning off ["Delivered by Clerk"][clerk-templates] per template and
listening for the `email.created` webhook to send from [your own
infrastructure][clerk-deliverability].

Rather than a list of vendors, which will always be missing yours, three
questions to put to whichever one you use:

- Can I set a custom SMTP host, on a per-environment or per-template basis?
- Is there a webhook that fires **at send time** and carries the message, so I
  can send it myself?
- Can the provider's own delivery be turned off for a template or an
  environment, so a message is not sent twice?

A yes to the first is case 1. A yes to the second and third together is case 2.
Three noes means your provider's test mode is what you have today, and
AuthStunt becomes useful the day you own the sending.

**4. Your company sends it, and you are not your company.** The most common
wall in a team of any size, and the one nothing technical solves: the mail goes
out through a backend, a platform team, or a shared mail service in a repository
you do not own. Nothing above is available to you, because none of it is yours
to change.

What this actually needs is small, and naming it that way is what gets it
approved:

- **Who has to agree.** Whoever owns the sending service's test or staging
  configuration. Not a security review, not a procurement cycle.
- **What to ask for.** One SMTP host and port, overridable in the test
  environment only. That is the entire ask. No production change, no new vendor,
  no data leaving the company, and no dependency added to their service.
- **What to do while you wait.** Point AuthStunt at whatever you *do* control
  and get the identity half working: leases, addresses and claim reason codes
  are testable against `examples/demo-app` without your company's mail path.
  When the variable lands, the tests are already written.

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

### What extraction reads, exactly

You cannot estimate the work of adopting this without knowing whether the
extractor can read *your* template, and that question has to be answerable
before you install anything. So here is the contract, stated narrowly enough to
be wrong if it is wrong. The behavior below was read from `internal/extract` and
`internal/smtp` and checked by running them on 2026-08-16.

**Which parts get read.** Both. The first `text/html` part and the first
`text/plain` part of the message, whichever exist. An HTML-only message is a
first-class case, not a degraded one, and so is a text-only one. Attachments are
skipped. Transfer encodings are decoded, so quoted-printable and base64 bodies
arrive as text; so are character sets, including legacy ones such as
windows-1258 and ISO-8859-1, not only UTF-8.

**Subjects, including non-ASCII ones.** RFC 2047 encoded-words in the `Subject`
header are decoded, in both the `B` and the `Q` forms, so
`=?UTF-8?B?TcOjIHjDoWMgdGjhu7FjOiA0ODI5MTM=?=` is read as `Mã xác thực: 482913`
and the code in it is found. A subject sent as raw UTF-8 with no encoded-word
works too. A subject that fails to decode is kept exactly as it arrived rather
than dropped, because a code in an undecodable subject is still a code.

**Subject or body.** Both, and it is worth being precise about what happens when
a template puts the code in both places, which most do. The subject, the plain
text and the visible text of the HTML are scanned together, in that order. Codes
are then deduplicated by their digits: the same code in the subject and the body
is **one** candidate, not two, keeping the better-placed occurrence. Two
*different* numbers stay two candidates.

**How a code is chosen.** A candidate is a standalone run of four to eight ASCII
digits, not glued to a letter or another digit, and not inside a URL. Candidates
are ranked by how close they sit to a context word - `code`, `otp`, `passcode`,
`verification`, and the Vietnamese `mã xác thực`, `mã xác nhận`, `mã đăng nhập`
and others - with a bonus for a word immediately in front of the digits, a bonus
for six digits, and a heavy penalty for something that looks like a year.

Matching is diacritic-folded and case-folded, so one keyword list covers
`mã xác thực` and `ma xac thuc` alike, and everything is normalized to NFC first
so a decomposed and a precomposed spelling behave the same. **Extraction is not
language-independent in general; it is English and Vietnamese.** A template in
another language whose only cue is a word not on that list still lists its number
as a candidate, but `otp_best` stays empty, because `otp_best` is only filled by
a positively scored candidate. That is deliberate: reporting a number as the code
because it was the only number in the message turns a missing code into a
confusing failure somewhere else.

**Links, and what `magic_link` gives you.** Every `href` is recorded, along with
URLs spelled out in plain text, deduplicated by URL. Each one is classified
`verify`, `magic`, `reset` or `other` from the URL and its anchor text, and
marked actionable only when it is `http` or `https` with a host - a
`javascript:`, `data:` or relative href is recorded and never actionable.

Claiming `magic_link` returns **the full URL, not a token**: it is the first
actionable `verify` link, falling back to the first actionable `magic` link,
never a `reset` link. Entities in an HTML `href` are unescaped, so
`?token=abc&amp;u=1` is claimed as `?token=abc&u=1` and is ready to open.

**Known limitations.** These are real, they are current, and each has a backlog
entry rather than a softer sentence:

- **`&amp;` is only unescaped in HTML.** A URL written out in a `text/plain`
  body with a literal `&amp;` in it is claimed with the `&amp;` still there,
  because nothing in a plain-text part is HTML and unescaping it would corrupt a
  URL that legitimately contains that text. If your plain-text alternative
  carries HTML-escaped links, use the HTML part or fix the template.
- **`magic_link` prefers a `verify` link over a `magic` one.** A message that
  carries both, a passwordless sign-in link and a separate confirm-address link,
  hands back the `verify` one. Templates that send a single link are unaffected.
- **Only the first part of each type is read.** A message with two `text/html`
  parts is read from the first; the second is stored but not extracted.
- **The keyword lists are English and Vietnamese only.** See above.

**Checking it against your own mail.** Two ways. The first needs no Go at all:
send a real message through a running server and read what comes back, using
only frozen routes.

```
RUN=$(curl -sX POST $URL/api/v1/runs -H "Authorization: Bearer $BEARER")
# lease an identity, send your real template to the address it returns, then:
curl -sX POST $URL/api/v1/leases/$LEASE_ID/claims -H "Authorization: Bearer $RUN_TOKEN" \
  -d '{"kind":"email_otp","idempotency_key":"probe-1","timeout_ms":15000}'
```

The second sends nothing and needs a clone and a Go toolchain, but no Go you
write: drop one JSON file holding your own subject, text and HTML into
`internal/extract/testdata/corpus/` alongside the result you expect, and run
`go test ./internal/extract/`. The corpus is loaded by glob, so a new file needs
no registration, and a disagreement between your template and this contract
prints as a diff.

## Where it sits

|  | Provider test mode | Mail catcher | Hosted inbox service | AuthStunt |
|---|---|---|---|---|
| Does a real message travel the real path? | No. Fixed code, nothing sent | Yes, to a local catcher | Yes, to their domain | Yes, to a server you run |
| Examples | Clerk, Supabase, better-auth test utils | Mailpit, MailHog, maildev, smtp4dev | Mailosaur, Mailtrap | this |
| Who owns the address? | The provider's fixture list | Nobody. One shared catch-all | Your account | A lease held by one run, with an expiry |
| Two workers, two codes in flight | Same fixed code for everyone | Both can read the same message | Unique addresses are easy; message ownership is yours to arrange | Each claim binds one message; the next gets `claim_already_claimed` |
| Offline, no account, no domain | Yes | Yes | No | Yes |
| Cost per message | None | None | Per plan | None |

The first row is deliberately the first row. Exclusivity is what the Playwright
issue asked for, but it only pays when workers run in parallel; the real message
on the real path pays whatever your worker count is.

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
tar xzf authstunt_0.2.0_darwin_arm64.tar.gz
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

Three fields in that response are worth naming, because they show up in output
before anything explains them:

- **`role`** is the free-form string you asked for. It is not an enum and there
  is no fixed vocabulary: `signup`, `admin`, `teacher-with-no-credits` are all
  valid. It is required and must not be empty. In ephemeral mode it is lowercased
  and stripped to `a-z`, `0-9`, `-` and `_` to become the local part prefix of
  the minted address, which is why `signup` produced
  `signup-8627b23ca1e5@demo.test` above. In pooled mode it is the key the pool
  is searched by, and a role with no free identity is refused rather than
  substituted.
- **`mode`** is `ephemeral` (the default) or `pooled`, read back from the stored
  identity rather than echoed from your request, so it says what was served.
- **`seed_state`** is the answer to "was this account prepared before I got it",
  and it is `skipped` here because no seed endpoint is configured. See
  [seeding an identity](#seeding-an-identity-before-it-is-handed-over) below.

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

## Giving the same contract to an agent (MCP)

`authstunt mcp` serves the same four routes to an AI agent as Model Context
Protocol tools, over stdio. The point is not that there is an MCP server; it is
that an agent doing a signup gets the same reason codes a test does, instead of
being asked to read an inbox and pick a code out by eye.

Four tools, one per frozen route: `open_run`, `lease_identity`, `claim_code`,
`release_lease`. There is no combined "sign this user up" tool, because that
tool would have to decide on your behalf how long to wait and whether a claim
that waited out its deadline is news or a failure - and it would flatten the
middle step's reason code into one sentence.

It is a proxy, not a server: point it at an instance that is already running,
because your application has to be able to send mail there.

```json
{
  "mcpServers": {
    "authstunt": {
      "command": "authstunt",
      "args": ["mcp"],
      "env": {
        "AUTHSTUNT_URL": "${AUTHSTUNT_URL:-http://127.0.0.1:8925}",
        "AUTHSTUNT_BEARER": "${AUTHSTUNT_BEARER}"
      }
    }
  }
}
```

That block is checked in as [`.mcp.json`](.mcp.json) at the root of this
repository, so a clone opened in Claude Code offers the server on the spot -
export `AUTHSTUNT_BEARER` first, and the file holds no secret of its own.
Copy it into your own project to get the same thing there.

The bearer travels through the process environment and nothing else. No tool
takes a credential as a parameter, no result carries one, and the run token
minted when a run opens is kept inside the server process - so none of it ever
reaches a transcript. That matters more here than in most servers, because this
agent reads text somebody else wrote: the body of an email your application
sent. A credential that was never in the context window has nothing to offer a
malicious message.

What does cross is the claimed code or link itself, and saying otherwise would
be overselling: typing it into your application is the whole job. It is a
one-time secret, scoped to one lease, spent when it is handed over.

Two more things worth knowing before you wire it up:

- **You never write an idempotency key.** It is derived from the lease, the kind
  and an `attempt` number, so calling `claim_code` again with the same `attempt`
  replays the same answer instead of burning a second message. Raise `attempt`
  only after the application has actually sent a new one - the resend case.
- **A reason is a result.** `claim_timeout`, `claim_already_claimed` and
  `claim_suspect_binding` come back as successful tool results carrying their
  code, not as errors, so the agent branches on the code rather than retrying
  blindly. Only a refused request is an error.

Tool names are frozen since v0.2.0; any future rename is an append-only alias.
Result bodies are the frozen `/api/v1` bodies. `examples/demo-app` is a signup
flow to point it at, and `docs/transcripts` records real agents walking it.

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

**Provisional.** Everything else this server answers: ending a run, the evidence
route, the healthz body, and the `totp` claim kind. Paths and fields there may
still change. `@authstunt/client` touches exactly one provisional route,
`run.end()`, and says so in the comment above it.

**Accepted but unsupported.** `mode: "pooled"` on a lease. The request is
accepted and the frozen contract covers `""` and `"ephemeral"` only. Pooled mode
means handing the same address to one run after another, which needs a cooldown
policy that has no safe default; treat it as reserved surface rather than a
feature.

**Frozen names.** MCP tool names are frozen since v0.2.0 (open_run ·
lease_identity · claim_code · release_lease); any future rename is an
append-only alias. They froze on evidence rather than on schedule:
`docs/transcripts` records real agents completing a signup through them. The
bodies the tools return are the frozen `/api/v1` bodies. Input shapes are not
part of the freeze.

**Not written yet.** The dashboard, the YAML flow loader, and TOTP.

**No retention.** Nothing is ever deleted, and there is no purge command. That
is a storage fact and a privacy one, so it is written out under
[retention and deletion](#retention-and-deletion) in Security rather than here.

## Operating it

### The project bearer

Every route under `/api/v1` authenticates callers with a project bearer, and
serve refuses to bind the API for a project that has none. There is one route
outside that prefix and it is deliberately open: see
[the health route](#the-health-route) below. serve never creates a bearer and
never prints one: it is a long-running process, so its output ends up in a CI
log, a supervisor journal or a log shipper, and a credential must not be carried
into any of them.

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

### The health route

`GET /healthz` is the one route that takes no credential, on purpose, because it
is what a supervisor polls and a supervisor has no bearer to give it.

```
curl -s http://127.0.0.1:8925/healthz
```

```json
{"status":"ok","version":"…","schema_version":6,"surface":"provisional-4a",
 "default_mode":"ephemeral","pooled_configured":false}
```

It answers only what a caller learns by connecting anyway: that the process is
up, which contract it speaks, and whether pooled mode is configured. Absent by
design are the project id and name, every address, and any count of runs, leases,
identities or messages, because a count is a side channel about activity and this
route has no principal to scope one to. A 200 here says the process is alive; it
says nothing about whether your bearer is right, so a preflight that only checks
health has not checked authentication.

The body is not frozen, and [Status and stability](#status-and-stability) above
says so.

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

### Seeding an identity before it is handed over

A leased address is an address, and quite often a test needs more than that: an
account that already exists, a user with a subscription, a teacher whose credits
have run out. `--seed-url` is the hook for that, and it exists today - it is not
planned work.

```
authstunt serve --project demo --domain demo.test \
  --seed-url http://127.0.0.1:3000/api/test/seed
```

The URL must be absolute, `http` or `https`, must carry no userinfo, and is
fixed at startup rather than taken per request, so a caller holding a run token
can never choose where this process sends a POST. A bad value fails at startup
instead of at the first lease. Redirects are refused for the same reason.

Every acquire then POSTs to that endpoint before the lease is handed back, with
an `Idempotency-Key` header holding the lease id:

```json
{
  "run_id": "…", "lease_id": "…", "identity_id": "…",
  "addr": "signup-8627b23ca1e5@demo.test", "role": "signup", "mode": "ephemeral"
}
```

Your application creates whatever `role` means to it and answers:

```json
{ "status": "seeded", "fingerprint": "user_8412" }
```

`status` must be `seeded` or `skipped`. `fingerprint` is optional, free-form, and
recorded on the lease and in the audit ledger so a run can be traced back to the
row it touched; it is not returned to the caller. There is no shell on this path
by design: a seed hook that ran a command would turn a configuration file into
arbitrary code execution, and a postcondition is something an HTTP call states
just as well.

That is what `seed_state` reports back on the lease:

- **`skipped`** - no `--seed-url` is configured, or your endpoint answered
  `skipped`. This is what an out-of-the-box instance always returns.
- **`seeded`** - your endpoint ran and reported the account ready.

A lease never comes back `pending`: the acquire has already settled by the time
it returns, so a caller never has to ask whether the account it was handed is
ready. Every failure is the same failure - a non-200, a timeout, a redirect, an
oversized body, a body that is not JSON, a status the schema does not name - and
all of them refuse the acquire with `lease_seed_failed` rather than handing back
an identity that is not known to be prepared. The endpoint's URL and response are
logged, not returned, because they belong to the application under test.

`internal/personas` is the package all of this lives in: it turns a request for
an identity of some role into a lease, mints ephemeral addresses, walks the
pooled candidates, and calls the seed hook. It appears in `Layout` above and is
named here so the two match.

One limitation, stated plainly: **nothing in this binary puts an identity into
the pool.** The pool is only ever read, so `mode: "pooled"` on a server with an
empty pool refuses every lease. Seeding prepares an identity that has already
been leased; it is not a way to populate a pool. Pooled mode is accepted but
unsupported, and this is part of why.

### Mail for an address you did not allow

A message addressed to any recipient outside the allowlist is accepted, stored,
and quarantined: kept as evidence and held back from the automated read path, so
a staging app that copies a real customer address cannot hand that person's mail
to a test.

## Security

Report a vulnerability to **security@authstunt.com** rather than in a public
issue. [SECURITY.md](SECURITY.md) says what is in scope and what is not, and
the "out of scope, by design" part is the half worth reading first: the host is
trusted, and the SMTP listener accepts every credential it is given.

### Retention and deletion

**Nothing is ever deleted.** Every message keeps its row, roughly two encrypted
blob files, and its ledger entries for as long as the data directory exists. The
periodic sweep expires runs and leases so their identities return to the pool; it
frees no disk. Size tracks how much mail an instance has ever accepted, not how
old it is.

That is a storage note for a suite you point at one long-lived instance. It is a
more serious note if your mail carries personal data, and auth mail usually does:

- The **raw message** is stored, not just the extracted code. Subject, bodies,
  `From`, `To` and `Cc` - whatever your template puts in a message about a real
  person is what lands on disk.
- **Quarantined mail is stored too.** Mail to an address outside the allowlist is
  held back from the automated read path, which is the point, but it is kept as
  evidence. A staging application that copies a real customer means that
  customer's mail is on your disk until you remove it.
- Bodies and extraction results are **encrypted at rest** with a key in the data
  directory. That protects the files if they are copied off the host. It is not
  deletion, and it is not a defense against anyone who can read the data
  directory, because the key is in it.

**How to delete, today.** Delete the data directory. That is the whole mechanism,
and there is no finer-grained one: no retention window, no per-message delete, no
`authstunt purge`. So the deletion story is a layout decision you make up front
rather than an operation you run later:

- Give each suite, or each CI job, its own `--data-dir` and remove it in
  teardown. An ephemeral container volume does this for you, and it is the
  reason the Quickstart uses one.
- Keep a shared long-lived instance only for a domain you own end to end, and put
  removing its data directory on a schedule somebody actually owns.
- If your organization has a data retention policy that applies to test systems,
  this instrument does not enforce it for you. Assume the directory is the unit
  of compliance.

A finer-grained retention or purge surface is open backlog, not a planned
release.

## Layout

- `cmd/authstunt` - main binary
- `internal/*` - server internals: smtp, extract, api, store, secrets,
  personas, ledger, sse, relayconf, fsutil, mcp
- `packages/client` - `@authstunt/client`, the typed TypeScript client
- `packages/playwright` - `@authstunt/playwright`, the fixtures
- `examples/demo-app` - a signup flow with an emailed code, to test against
- `docs/transcripts` - recorded agent runs against the MCP surface

Planned, not yet written: `web/` dashboard · `internal/flows`

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
