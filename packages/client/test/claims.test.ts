// Contract tests against the real server: real binary, real SQLite data
// dir, real SMTP ingestion, no mocked HTTP anywhere. Each test opens its
// own run and lease so nothing leaks between them.

import assert from 'node:assert/strict'
import test, { after, before } from 'node:test'
import { authstunt, ClaimTimeoutError, type AuthstuntClient, type Lease, type Run } from '../src/index.js'
import { evidence, startServer, type ServerHandle } from './harness.ts'
import { otpBody, sendMail } from './smtp.ts'

let server: ServerHandle
let client: AuthstuntClient

before(async () => {
  server = await startServer()
  client = authstunt({ baseUrl: server.baseUrl, bearer: server.bearer })
})

after(async () => {
  await server.stop()
})

async function openLease(): Promise<{ run: Run; lease: Lease }> {
  const run = await client.run()
  const lease = await run.lease({ role: 'signup' })
  return { run, lease }
}

function sendOTP(lease: Lease, code: string): Promise<void> {
  return sendMail({
    host: server.smtpHost,
    port: server.smtpPort,
    from: 'noreply@app.example',
    to: lease.addr,
    subject: 'Your verification code',
    text: otpBody(code),
  })
}

/** Polls the run's evidence until `count` messages are bound to the lease.
 * The 250 ack commits the message, but binding visibility to the claim
 * query is what these tests must order themselves against. */
async function waitForBound(run: Run, leaseId: string, count: number): Promise<string[]> {
  const deadline = Date.now() + 10_000
  for (;;) {
    const events = await evidence(server, run.id)
    const bound = events
      .filter((e) => e.action === 'mail.bound' && e.detail['lease_id'] === leaseId)
      .map((e) => String(e.detail['message_id']))
    if (bound.length >= count) {
      return bound
    }
    if (Date.now() > deadline) {
      throw new Error(`expected ${String(count)} bound messages, saw ${String(bound.length)}`)
    }
    await new Promise((resolve) => setTimeout(resolve, 50))
  }
}

/** Extraction settles asynchronously after the bind. The measured settle
 * distribution is p99 under 20ms with a 35ms observed max, so this wait
 * carries an order of magnitude of headroom without slowing the suite. */
function settleWait(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 750))
}

void test('happy path: lease, mail, claim returns the code bound to that one message', async () => {
  const { run, lease } = await openLease()

  // Claim first, then send: this exercises the long-poll wakeup, not a
  // poll-after-the-fact.
  const pending = lease.claim('email_otp', { timeoutMs: 15_000 })
  await sendOTP(lease, '482913')
  const claim = await pending

  assert.equal(claim.reason, 'claim_ok')
  assert.equal(claim.value, '482913')
  assert.ok(claim.claimId !== '')
  assert.ok(claim.waitedMs >= 0)

  // The messageId in the answer is the message the SMTP dialogue created:
  // the run's evidence shows exactly one message bound to this lease and
  // the claim names it.
  const bound = await waitForBound(run, lease.id, 1)
  assert.equal(bound.length, 1)
  assert.equal(claim.messageId, bound[0])

  await lease.release()
})

void test('tryClaim with timeoutMs 0 and no mail resolves to a named outcome instead of throwing', async () => {
  const { lease } = await openLease()

  const outcome = await lease.tryClaim('email_otp', { timeoutMs: 0 })

  // Real server semantics: with no mail ever addressed to the lease there
  // is nothing bound, and the server names that claim_no_binding. The bare
  // claim_timeout is reserved for mail that bound but never settled.
  assert.ok(outcome.reason !== 'claim_ok', 'a lease with no mail must not yield a value')
  assert.equal(outcome.reason, 'claim_no_binding')
  assert.equal(outcome.timedOut, false)
  assert.ok(outcome.waitedMs >= 0)

  await lease.release()
})

void test('claim that waits out its deadline throws ClaimTimeoutError with the wait on it', async () => {
  const { run, lease } = await openLease()

  // An OTP mail binds to the lease but carries no magic link, so a
  // magic_link claim has something bound and nothing claimable: the one
  // outcome the server genuinely calls claim_timeout.
  await sendOTP(lease, '555444')
  await waitForBound(run, lease.id, 1)

  await assert.rejects(
    lease.claim('magic_link', { timeoutMs: 1500 }),
    (err: unknown) => {
      assert.ok(err instanceof ClaimTimeoutError)
      assert.equal(err.reason, 'claim_timeout')
      assert.equal(err.timedOut, true)
      assert.ok(err.waitedMs >= 1000)
      assert.match(err.message, /claim_timeout/)
      return true
    },
  )

  await lease.release()
})

void test('release is idempotent: releasing twice is not an error', async () => {
  const { lease } = await openLease()
  await lease.release()
  await lease.release()
})

void test('RESEND: two codes in flight on one lease, then two claims', async () => {
  const { run, lease } = await openLease()

  // The resend scenario: the application sends OTP #1, the user clicks
  // resend, OTP #2 lands on the same address. Only then does the first
  // claim happen. This test asserts what the server actually does, not
  // what a reader might wish it did.
  await sendOTP(lease, '111111')
  await sendOTP(lease, '222222')
  const bound = await waitForBound(run, lease.id, 2)
  await settleWait()

  // Observed server semantics (store/claims.go ClaimCandidates): oldest
  // first. The FIRST claim is backed by OTP #1, the message that arrived
  // first, and OTP #2 stays visible for the next claim.
  //
  // bound[] comes from the ledger (ordered by event ts) while candidates
  // order by received_at; the two agree here only because the two SMTP
  // dialogues were awaited sequentially, each past its 250 ack.
  const first = await lease.claim('email_otp', { timeoutMs: 5000 })
  assert.equal(first.value, '111111')
  assert.equal(first.messageId, bound[0])

  const second = await lease.claim('email_otp', { timeoutMs: 5000 })
  assert.equal(second.value, '222222')
  assert.equal(second.messageId, bound[1])
  assert.notEqual(first.messageId, second.messageId)
  assert.notEqual(first.claimId, second.claimId)

  // With both messages claimed, a third ask is refused by name.
  const third = await lease.tryClaim('email_otp', { timeoutMs: 0 })
  assert.equal(third.reason, 'claim_already_claimed')

  await lease.release()
})

void test('one message, two claim calls: the second is claim_already_claimed', async () => {
  const { run, lease } = await openLease()

  await sendOTP(lease, '987654')
  await waitForBound(run, lease.id, 1)

  const first = await lease.claim('email_otp', { timeoutMs: 5000 })
  assert.equal(first.value, '987654')

  // The README example from the ADR, pinned: a second claim with its own
  // key does not get the same code again and does not hang; it is told
  // the message is already claimed.
  const second = await lease.tryClaim('email_otp', { timeoutMs: 0 })
  assert.equal(second.reason, 'claim_already_claimed')

  await lease.release()
})

void test('end releases the run, and ending it twice is not an error', async () => {
  const { run, lease } = await openLease()

  await run.end()

  // The identity went back with the run: a claim on a lease of an ended
  // run is refused by name rather than parking until its deadline.
  const after = await lease.tryClaim('email_otp', { timeoutMs: 0 })
  assert.equal(after.reason, 'run_not_active')

  // Teardown safety, which is the whole reason this method exists: a
  // fixture that ends the run on a path that already ended it must not
  // turn cleanup into a failure.
  await run.end()

  // Same for the lease underneath it. Release after the run is gone is
  // the ordinary teardown order when a test failed early.
  await lease.release()
})
