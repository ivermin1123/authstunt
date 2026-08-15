import { authstunt, type Lease, type Run } from '@authstunt/client'
import type { Fixtures } from '@playwright/test'

/** Options a suite sets in defineConfig `use`, or leaves to the
 * environment. Each one is a Playwright option fixture, which is what
 * makes `use` in a config file the override mechanism rather than a second
 * configuration system of our own. */
export interface AuthstuntOptions {
  /** Origin of the AuthStunt server. Defaults to AUTHSTUNT_URL. */
  authstuntUrl: string
  /** The project bearer. Defaults to AUTHSTUNT_BEARER.
   *
   * It is read here, in worker setup, and used to open the run. It never
   * reaches a test: a test is handed a lease, and every call a lease makes
   * travels on the run token the server minted for that run. Nothing in
   * this package prints it, including the errors below. */
  authstuntBearer: string
  /** Role each test's identity is leased under. Defaults to "signup". */
  leaseRole: string
}

/** What a worker gets: one run, opened once and ended when the worker
 * finishes. */
export interface AuthstuntWorkerFixtures {
  run: Run
}

/** What a test gets: one identity of its own. */
export interface AuthstuntTestFixtures {
  lease: Lease
}

/** The fixtures, defined once. `test` in this package is built from this
 * object and adds nothing, so a suite that composes these into its own
 * `test` gets identical behavior rather than a second implementation. */
export const authstuntFixtures: Fixtures<
  AuthstuntTestFixtures & Pick<AuthstuntOptions, 'leaseRole'>,
  AuthstuntWorkerFixtures & Omit<AuthstuntOptions, 'leaseRole'>
> = {
  authstuntUrl: [process.env['AUTHSTUNT_URL'] ?? '', { option: true, scope: 'worker' }],
  authstuntBearer: [process.env['AUTHSTUNT_BEARER'] ?? '', { option: true, scope: 'worker' }],
  leaseRole: ['signup', { option: true }],

  // One run per worker. Playwright runs each worker in its own process and
  // reuses it for the tests it is given, so a worker that runs twenty tests
  // sequentially opens exactly one run and leases twenty identities from
  // it. That is the intended shape, not an accident of caching: the run is
  // the unit the server scopes identities and evidence to, and one per
  // worker is what makes "no two tests share an identity" true without any
  // coordination between workers.
  run: [
    async ({ authstuntUrl, authstuntBearer }, use): Promise<void> => {
      // Both messages name what is missing and never echo a value. A
      // bearer printed into a CI log by a validation error is the same
      // leak as printing it on purpose.
      if (authstuntUrl === '') {
        throw new Error(
          '@authstunt/playwright: the server URL is missing. Set AUTHSTUNT_URL, or set authstuntUrl in the `use` block of your Playwright config.',
        )
      }
      if (authstuntBearer === '') {
        throw new Error(
          '@authstunt/playwright: the project bearer is missing. Set AUTHSTUNT_BEARER, or set authstuntBearer in the `use` block of your Playwright config. Its value is never printed.',
        )
      }
      const client = authstunt({ baseUrl: authstuntUrl, bearer: authstuntBearer })
      const opened = await client.run()
      try {
        await use(opened)
      } finally {
        // Ending the run hands every identity it still holds back to the
        // pool now, rather than when the run's TTL runs out. end() treats
        // an already-ended run as success, so this is safe on a worker
        // that is tearing down after a failure.
        await opened.end()
      }
    },
    { scope: 'worker' },
  ],

  // One identity per test, from the run its worker owns.
  lease: async ({ run, leaseRole }, use): Promise<void> => {
    const leased = await run.lease({ role: leaseRole })
    try {
      await use(leased)
    } finally {
      // Released whether the test passed or failed - the finally is the
      // point, because the failing tests are exactly the ones that would
      // otherwise hold an identity until the run expires.
      //
      // A failure here is reported and not thrown. Releasing is idempotent
      // on the server and a lease its run already released answers 204, so
      // this should not fire; if it ever does, throwing from teardown
      // would replace the test's own failure with this one, and the
      // failure a developer needs to see is the first one.
      try {
        await leased.release()
      } catch (error) {
        console.warn(
          `@authstunt/playwright: releasing lease ${leased.id} failed; it will be swept when the run ends`,
          error,
        )
      }
    }
  },
}
