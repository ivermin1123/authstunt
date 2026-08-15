// The narrow claim, as executable code: two tests running at the same time
// in two workers do not step on each other.
//
// The two tests below meet at a file. Each writes what it was given, then
// waits for the other's file to appear before asserting. The wait is the
// part that makes the assertion mean something: if the runner had put both
// tests on one worker, or run them one after the other, neither would ever
// see the other's file and the test would fail on the barrier rather than
// pass on a comparison it never really made.
//
// They are in their own file because Playwright's unit of parallelism is
// the file: two tests in one file run in one worker unless the file opts
// into parallel mode, and what is under test here is two workers.

import { mkdir, readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { expect, test } from '../src/index.js'

test.describe.configure({ mode: 'parallel' })

interface Identity {
  runId: string
  addr: string
  leaseId: string
}

function shareDir(): string {
  const dir = process.env['AUTHSTUNT_SHARE_DIR']
  if (dir === undefined) {
    throw new Error('global setup did not report the share directory')
  }
  return dir
}

/** Publishes what this test was handed, then waits for its partner's file.
 * Returns the partner's identity, or throws if it never showed up - which
 * is the "they did not actually run together" failure. */
async function meet(name: string, partner: string, mine: Identity): Promise<Identity> {
  const dir = path.join(shareDir(), 'isolation')
  await mkdir(dir, { recursive: true })
  await writeFile(path.join(dir, `${name}.json`), JSON.stringify(mine), 'utf8')

  const partnerFile = path.join(dir, `${partner}.json`)
  const deadline = Date.now() + 30_000
  for (;;) {
    try {
      return JSON.parse(await readFile(partnerFile, 'utf8')) as Identity
    } catch {
      if (Date.now() > deadline) {
        throw new Error(
          `${name} never saw ${partner}: the two tests did not overlap, so this assertion would prove nothing`,
        )
      }
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
  }
}

function assertDistinct(mine: Identity, theirs: Identity): void {
  // Different identity: the whole point. Two tests that shared an address
  // would race for each other's mail.
  expect(theirs.addr).not.toBe(mine.addr)
  expect(theirs.leaseId).not.toBe(mine.leaseId)
  // Different run: the run is worker-scoped, so two workers holding one
  // run would mean the fixture scope is wrong even if the addresses
  // happened to differ.
  expect(theirs.runId).not.toBe(mine.runId)
}

test('parallel worker A holds an identity nobody else holds', async ({ lease, run }) => {
  const mine: Identity = { runId: run.id, addr: lease.addr, leaseId: lease.id }
  assertDistinct(mine, await meet('a', 'b', mine))
})

test('parallel worker B holds an identity nobody else holds', async ({ lease, run }) => {
  const mine: Identity = { runId: run.id, addr: lease.addr, leaseId: lease.id }
  assertDistinct(mine, await meet('b', 'a', mine))
})
