import { test as base } from '@playwright/test'
import {
  authstuntFixtures,
  type AuthstuntOptions,
  type AuthstuntTestFixtures,
  type AuthstuntWorkerFixtures,
} from './fixtures.js'

export { authstuntFixtures } from './fixtures.js'
export type {
  AuthstuntOptions,
  AuthstuntTestFixtures,
  AuthstuntWorkerFixtures,
} from './fixtures.js'

/** `test` with the AuthStunt fixtures already on it - the three-line front
 * door:
 *
 * ```ts
 * import { test, expect } from '@authstunt/playwright'
 *
 * test('signup', async ({ page, lease }) => {
 *   await page.fill('#email', lease.addr)
 *   const { value: otp } = await lease.claim('email_otp', { timeoutMs: 15_000 })
 * })
 * ```
 *
 * It is one line built from `authstuntFixtures` and holds no logic of its
 * own. A suite that already has its own `test` object should extend that
 * one with `authstuntFixtures` instead of importing this; a test file runs
 * under exactly one `test`, so importing this one would discard theirs.
 * Both routes are the same fixtures, so they cannot drift apart. */
export const test: ReturnType<
  typeof base.extend<
    AuthstuntTestFixtures & Pick<AuthstuntOptions, 'leaseRole'>,
    AuthstuntWorkerFixtures & Omit<AuthstuntOptions, 'leaseRole'>
  >
> = base.extend<
  AuthstuntTestFixtures & Pick<AuthstuntOptions, 'leaseRole'>,
  AuthstuntWorkerFixtures & Omit<AuthstuntOptions, 'leaseRole'>
>(authstuntFixtures)

export { expect } from '@playwright/test'

// Re-exported so the types a user writes in their own fixtures or config
// come from here rather than from a deep import into @authstunt/client.
export type { Claim, ClaimOptions, ClaimOutcome, Lease, Run } from '@authstunt/client'
export {
  ClaimError,
  ClaimRefusedError,
  ClaimTimeoutError,
  type ClaimKind,
  type ClaimFailureReason,
  type ClaimRefusalReason,
} from '@authstunt/client'
