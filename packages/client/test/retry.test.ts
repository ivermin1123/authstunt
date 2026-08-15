// Proves the internal retry reuses the SAME idempotency key, end to end
// against the real server. A small HTTP proxy sits between the client and
// the server; it forwards the first claim request, lets the server finish
// answering, then kills the client's socket without passing the response
// back. The client sees a dead socket, retries under the same key, and the
// server's replay path hands back the first answer instead of consuming a
// second message.

import assert from 'node:assert/strict'
import http from 'node:http'
import type { AddressInfo } from 'node:net'
import test, { after, before } from 'node:test'
import { authstunt } from '../src/index.js'
import { evidence, startServer, type ServerHandle } from './harness.ts'
import { otpBody, sendMail } from './smtp.ts'

const claimPath = /^\/api\/v1\/leases\/[^/]+\/claims$/

let server: ServerHandle
let proxy: http.Server
let proxyUrl: string
const claimBodies: { idempotency_key: string }[] = []

before(async () => {
  server = await startServer()

  proxy = http.createServer((req, res) => {
    const chunks: Buffer[] = []
    req.on('data', (chunk: Buffer) => chunks.push(chunk))
    req.on('end', () => {
      void (async (): Promise<void> => {
        const body = Buffer.concat(chunks)
        const isClaim = req.method === 'POST' && claimPath.test(req.url ?? '')
        if (isClaim) {
          claimBodies.push(JSON.parse(body.toString()) as { idempotency_key: string })
        }
        const upstream = await fetch(`${server.baseUrl}${req.url ?? ''}`, {
          method: req.method ?? 'GET',
          headers: {
            ...(req.headers.authorization === undefined ? {} : { authorization: req.headers.authorization }),
            ...(req.headers['content-type'] === undefined ? {} : { 'content-type': req.headers['content-type'] }),
          },
          ...(body.length > 0 ? { body } : {}),
        })
        const text = await upstream.text()
        if (isClaim && claimBodies.length === 1) {
          // The server has fully answered this first claim; the client
          // never hears it. This is the "socket died mid-flight" shape,
          // not a refused request.
          res.socket?.destroy()
          return
        }
        res.writeHead(upstream.status, { 'content-type': 'application/json' })
        res.end(text)
      })().catch((err: unknown) => {
        res.writeHead(502)
        res.end(String(err))
      })
    })
  })
  await new Promise<void>((resolve) => {
    proxy.listen(0, '127.0.0.1', () => { resolve() })
  })
  const addr = proxy.address() as AddressInfo
  proxyUrl = `http://127.0.0.1:${String(addr.port)}`
})

after(async () => {
  await new Promise<void>((resolve) => { proxy.close(() => { resolve() }) })
  await server.stop()
})

void test('a dropped socket is retried under the same idempotency key and replayed, not re-consumed', async () => {
  const client = authstunt({ baseUrl: proxyUrl, bearer: server.bearer })
  const run = await client.run()
  const lease = await run.lease({ role: 'signup' })

  await sendMail({
    host: server.smtpHost,
    port: server.smtpPort,
    from: 'noreply@app.example',
    to: lease.addr,
    subject: 'Your verification code',
    text: otpBody('314159'),
  })
  // Let the message bind and settle before claiming, so the first claim
  // answers immediately instead of parking through the drop window.
  await new Promise((resolve) => setTimeout(resolve, 750))

  const claim = await lease.claim('email_otp', { timeoutMs: 10_000 })
  assert.equal(claim.value, '314159')

  // The client sent the claim twice, both times under one key.
  assert.equal(claimBodies.length, 2)
  assert.ok(claimBodies[0]?.idempotency_key !== '')
  assert.equal(claimBodies[0]?.idempotency_key, claimBodies[1]?.idempotency_key)

  // And the server saw a replay of one claim, not two claims: both settle
  // events carry the same claim id, and the value was handed over for one
  // message only.
  const events = await evidence(server, run.id)
  const settled = events.filter(
    (e) => e.action === 'claim.settled' && e.detail['reason'] === 'claim_ok',
  )
  assert.equal(settled.length, 2)
  assert.equal(settled[0]?.detail['claim_id'], settled[1]?.detail['claim_id'])
  assert.equal(settled[0]?.detail['claim_id'], claim.claimId)

  await lease.release()
})
