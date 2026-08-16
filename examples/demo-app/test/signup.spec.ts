// The signup flow, driven through a browser, with every code coming from
// AuthStunt.
//
// Nothing here reads a mailbox, polls for delivery, or knows what the app
// generated. The fixture leases an address, the app mails a code to it,
// and lease.claim long-polls until the server can hand over the value
// bound to this lease and nobody else's.

import { expect, test } from '@authstunt/playwright'

function demoUrl(): string {
  const url = process.env['DEMO_URL']
  if (url === undefined) {
    throw new Error('global setup did not report the demo app URL')
  }
  return url
}

test('a signup completes with the code claimed for this test', async ({ page, lease }) => {
  await page.goto(`${demoUrl()}/signup`)
  await page.fill('#email', lease.addr)
  await page.getByRole('button', { name: 'Send code' }).click()

  const { value: code } = await lease.claim('email_otp', { timeoutMs: 15_000 })

  await page.fill('#code', code)
  await page.getByRole('button', { name: 'Verify' }).click()

  await expect(page.locator('#welcome')).toContainText('verified')
})

test('resending retires the first code', async ({ page, lease }) => {
  await page.goto(`${demoUrl()}/signup`)
  await page.fill('#email', lease.addr)
  await page.getByRole('button', { name: 'Send code' }).click()

  // Two claims on one lease, told apart by their idempotency keys. A
  // shared key would replay the first answer rather than wait for the
  // second message, which is the behavior a retry wants and the opposite
  // of what a resend wants.
  const first = await lease.claim('email_otp', { timeoutMs: 15_000, idempotencyKey: 'attempt-1' })

  await page.getByRole('button', { name: 'Resend code' }).click()
  await expect(page.locator('#resent')).toBeVisible()

  const second = await lease.claim('email_otp', { timeoutMs: 15_000, idempotencyKey: 'attempt-2' })
  expect(second.value).not.toBe(first.value)

  // The retired code is refused. This is the assertion the resend button
  // exists for: an app that left both codes valid would pass every other
  // check in this file.
  await page.fill('#code', first.value)
  await page.getByRole('button', { name: 'Verify' }).click()
  await expect(page.locator('#error')).toBeVisible()

  await page.goto(`${demoUrl()}/verify?email=${encodeURIComponent(lease.addr)}`)
  await page.fill('#code', second.value)
  await page.getByRole('button', { name: 'Verify' }).click()
  await expect(page.locator('#welcome')).toContainText('verified')
})
