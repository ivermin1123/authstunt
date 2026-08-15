// The end-to-end shape from the README, run against the real server: a
// fixture hands the test an identity, mail arrives over real SMTP, and the
// test claims the code inside the test body.
//
// The claim long-polls, so nothing here polls or sleeps waiting for
// extraction to settle: the server answers when the value is claimable.

import { sendMail, otpBody } from '../../client/test/smtp.ts'
import { expect, test } from '../src/index.js'

function smtpTarget(): { host: string; port: number } {
  const host = process.env['AUTHSTUNT_SMTP_HOST']
  const port = process.env['AUTHSTUNT_SMTP_PORT']
  if (host === undefined || port === undefined) {
    throw new Error('global setup did not report the SMTP listener')
  }
  return { host, port: Number(port) }
}

test('a test claims the code sent to the address its fixture leased', async ({ lease }) => {
  // What an application would do with lease.addr: send mail to it.
  const { host, port } = smtpTarget()
  await sendMail({
    host,
    port,
    from: 'noreply@app.example',
    to: lease.addr,
    subject: 'Your verification code',
    text: otpBody('418902'),
  })

  const { value, messageId } = await lease.claim('email_otp', { timeoutMs: 15_000 })

  expect(value).toBe('418902')
  // The claim is bound to one message, and the id is how a failure gets
  // traced back to it. An empty string here would mean the client had
  // degraded a broken contract into a usable-looking value.
  expect(messageId).not.toBe('')
})

test('the second test in the same worker gets a different address', async ({ lease }) => {
  // Same worker, same run, new lease. This is the sequential half of the
  // isolation claim; the parallel half is in isolation.spec.ts.
  expect(lease.addr).toMatch(/@/)

  const { host, port } = smtpTarget()
  await sendMail({
    host,
    port,
    from: 'noreply@app.example',
    to: lease.addr,
    subject: 'Your verification code',
    text: otpBody('735514'),
  })

  const claimed = await lease.claim('email_otp', { timeoutMs: 15_000 })
  expect(claimed.value).toBe('735514')
})
