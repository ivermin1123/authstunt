// Starts one real AuthStunt server for the whole run and tells the workers
// where it is.
//
// The server is the same binary a user runs, started by the same harness
// @authstunt/client tests against - reused rather than reimplemented, so
// there is one definition of "a real server" in this repo. Nothing is
// mocked here either.
//
// globalSetup runs in Playwright's main process, before workers are
// forked, so environment variables set here are inherited by every worker.
// That is exactly the path a user takes: the fixtures read AUTHSTUNT_URL
// and AUTHSTUNT_BEARER from the environment, so this suite exercises the
// documented default rather than a private wiring of its own.

import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { startServer, type ServerHandle } from '../../client/test/harness.ts'

let server: ServerHandle | undefined

export default async function globalSetup(): Promise<() => Promise<void>> {
  server = await startServer()
  // A scratch directory the isolation test uses as its meeting point
  // between two worker processes. Workers share nothing but the file
  // system, which is the honest way to observe that they really did run
  // at the same time.
  const shareDir = await mkdtemp(path.join(tmpdir(), 'authstunt-pw-share-'))
  process.env['AUTHSTUNT_SHARE_DIR'] = shareDir
  process.env['AUTHSTUNT_URL'] = server.baseUrl
  // Handed to workers through the environment and never written to a file,
  // a report or the console.
  process.env['AUTHSTUNT_BEARER'] = server.bearer
  // The SMTP listener is on an ephemeral port, so the tests need to be
  // told where it is; a real suite would point at its own application.
  process.env['AUTHSTUNT_SMTP_HOST'] = server.smtpHost
  process.env['AUTHSTUNT_SMTP_PORT'] = String(server.smtpPort)

  return async (): Promise<void> => {
    await server?.stop()
    await rm(shareDir, { recursive: true, force: true })
  }
}
