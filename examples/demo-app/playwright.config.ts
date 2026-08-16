import { defineConfig, devices } from '@playwright/test'

// Unlike the suite inside packages/playwright, this one opens a browser:
// what is under test here is an application's signup flow, driven the way
// a user drives it, with the codes coming from AuthStunt rather than from
// a fixture that planted them.
//
// globalSetup starts the AuthStunt server and this app; see test/global-setup.ts.
export default defineConfig({
  testDir: './test',
  globalSetup: './test/global-setup.ts',
  fullyParallel: true,
  retries: 0,
  reporter: process.env['CI'] === undefined ? 'list' : 'github',
  // Each test waits on real mail crossing a real SMTP hop, twice in the
  // resend case.
  timeout: 60_000,
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
