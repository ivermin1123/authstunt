import { defineConfig } from '@playwright/test'

// No browser project is declared and no test here opens a page. What is
// under test is the fixture wiring - one run per worker, one identity per
// test - and a browser would add minutes and a system dependency to a
// suite that proves neither.
//
// Two workers, fullyParallel, and no retries: the isolation test needs two
// workers to be running at once for its assertion to mean anything, and a
// retry would let a genuine collision pass on the second attempt.
export default defineConfig({
  testDir: './test',
  globalSetup: './test/global-setup.ts',
  fullyParallel: true,
  workers: 2,
  retries: 0,
  reporter: process.env['CI'] === undefined ? 'list' : 'github',
  timeout: 60_000,
})
