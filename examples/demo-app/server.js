// A signup flow small enough to read in one sitting, and real enough to
// test against: an email form, a six digit code delivered over SMTP, a
// verify form, and a resend button.
//
// There is no database. Pending signups live in a Map that dies with the
// process, because what this app exists to demonstrate is the email round
// trip, and storage would only be scenery. Everything a reader has to
// understand to follow the AuthStunt quickstart is in this one file.
//
// The mail goes to whatever SMTP server SMTP_HOST and SMTP_PORT name,
// which defaults to the address `authstunt serve` listens on. Point it at
// a real relay and the same code sends real mail.

import crypto from 'node:crypto'
import express from 'express'
import nodemailer from 'nodemailer'

const CODE_TTL_MS = 10 * 60 * 1000

/** Pending signups, keyed by address: { code, expiresAt }.
 *
 * One entry per address, and that is load bearing rather than a
 * simplification. Issuing a code overwrites the entry, so the previous
 * code stops working the instant a new one is sent - which is what makes
 * the resend button below a real invalidation and not a second valid
 * code in flight. */
const pending = new Map()

/** Addresses that completed the flow. */
const verified = new Set()

/** Six digits, from a CSPRNG. A demo is still a place where an
 * exhaustible code should not come out of Math.random. */
function newCode() {
  return String(crypto.randomInt(0, 1_000_000)).padStart(6, '0')
}

function escapeHtml(value) {
  return String(value).replace(
    /[&<>"']/g,
    (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c],
  )
}

function page(title, body) {
  return `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>${escapeHtml(title)}</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 32rem; margin: 4rem auto">
<h1>${escapeHtml(title)}</h1>
${body}
</body>
</html>
`
}

/**
 * @param {{ smtpHost?: string, smtpPort?: number, from?: string }} [options]
 */
export function createApp({ smtpHost, smtpPort, from = 'noreply@demo.example' } = {}) {
  const mailer = nodemailer.createTransport({
    host: smtpHost ?? process.env.SMTP_HOST ?? '127.0.0.1',
    port: Number(smtpPort ?? process.env.SMTP_PORT ?? 1025),
    secure: false,
  })

  /** Issues a code for this address and mails it.
   *
   * The write into `pending` happens before the send, so a code that is
   * in a mailbox is always the code this app will accept. Sending first
   * would leave a window where the old code was still valid and the new
   * one already delivered. */
  async function issueCode(email) {
    const code = newCode()
    pending.set(email, { code, expiresAt: Date.now() + CODE_TTL_MS })
    await mailer.sendMail({
      from,
      to: email,
      subject: 'Your verification code',
      text: `Your verification code is ${code}. It expires in 10 minutes.`,
    })
    return code
  }

  const app = express()
  app.use(express.urlencoded({ extended: false }))

  app.get('/', (_req, res) => { res.redirect(303, '/signup') })

  app.get('/signup', (_req, res) => {
    res.send(page('Sign up', `
<form method="post" action="/signup">
  <label for="email">Email address</label><br>
  <input id="email" name="email" type="email" required autocomplete="email" size="40"><br><br>
  <button type="submit">Send code</button>
</form>`))
  })

  app.post('/signup', async (req, res, next) => {
    const email = String(req.body.email ?? '').trim()
    if (email === '') {
      res.status(400).send(page('Sign up', '<p>An email address is required.</p><p><a href="/signup">Back</a></p>'))
      return
    }
    try {
      await issueCode(email)
    } catch (error) {
      next(error)
      return
    }
    res.redirect(303, `/verify?email=${encodeURIComponent(email)}`)
  })

  app.get('/verify', (req, res) => {
    const email = String(req.query.email ?? '')
    const resent = req.query.resent === '1'
    res.send(page('Enter your code', `
${resent ? '<p id="resent">A new code is on its way. The previous code no longer works.</p>' : ''}
<p>We sent a six digit code to <strong>${escapeHtml(email)}</strong>.</p>
<form method="post" action="/verify">
  <input type="hidden" name="email" value="${escapeHtml(email)}">
  <label for="code">Code</label><br>
  <input id="code" name="code" inputmode="numeric" autocomplete="one-time-code" required size="12"><br><br>
  <button type="submit">Verify</button>
</form>
<form method="post" action="/resend">
  <input type="hidden" name="email" value="${escapeHtml(email)}">
  <button type="submit">Resend code</button>
</form>`))
  })

  app.post('/verify', (req, res) => {
    const email = String(req.body.email ?? '').trim()
    const code = String(req.body.code ?? '').trim()
    const entry = pending.get(email)

    // One rejection message for every failure - unknown address, expired
    // entry, wrong code. Telling them apart would tell an attacker which
    // addresses have a signup in flight.
    if (entry === undefined || Date.now() > entry.expiresAt || entry.code !== code) {
      res.status(400).send(page('Enter your code', `
<p id="error">That code is not valid. Codes expire after 10 minutes, and asking for a new one retires the old one.</p>
<p><a href="/verify?email=${encodeURIComponent(email)}">Try again</a></p>`))
      return
    }

    pending.delete(email)
    verified.add(email)
    res.send(page('Welcome', `<p id="welcome">Welcome, ${escapeHtml(email)}. Your address is verified.</p>`))
  })

  app.post('/resend', async (req, res, next) => {
    const email = String(req.body.email ?? '').trim()
    if (!pending.has(email)) {
      res.redirect(303, '/signup')
      return
    }
    try {
      // Overwrites the pending entry, so the code already in the mailbox
      // stops being accepted here. This is the scene the AuthStunt
      // README and the MCP `attempt` parameter both describe.
      await issueCode(email)
    } catch (error) {
      next(error)
      return
    }
    res.redirect(303, `/verify?email=${encodeURIComponent(email)}&resent=1`)
  })

  return app
}

/** Starts the app and resolves once it is accepting connections.
 *
 * Returns the base URL and a stop function so a test can run this in the
 * same process it runs from, on a port the operating system chose.
 *
 * @param {{ port?: number, smtpHost?: string, smtpPort?: number, from?: string }} [options]
 * @returns {Promise<{ url: string, stop: () => Promise<void> }>}
 */
export function start({ port = 0, ...options } = {}) {
  const app = createApp(options)
  return new Promise((resolve) => {
    const server = app.listen(port, '127.0.0.1', () => {
      const { port: bound } = server.address()
      resolve({
        url: `http://127.0.0.1:${bound}`,
        stop: () => new Promise((done) => server.close(done)),
      })
    })
  })
}

// Started directly: bind the configured port and say where it is.
if (process.argv[1] === new URL(import.meta.url).pathname) {
  const { url } = await start({ port: Number(process.env.PORT ?? 3000) })
  const smtp = `${process.env.SMTP_HOST ?? '127.0.0.1'}:${process.env.SMTP_PORT ?? '1025'}`
  console.log(`demo app on ${url}, sending mail to ${smtp}`)
}
