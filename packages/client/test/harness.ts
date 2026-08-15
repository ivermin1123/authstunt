// Test harness: builds the real server binary, provisions a bearer with
// --out (no TTY involved), starts serve on ephemeral ports and hands the
// caller everything a test needs. No HTTP is mocked anywhere in this suite;
// every assertion runs against the same binary a user runs.

import { spawn } from 'node:child_process'
import type { ChildProcessByStdio } from 'node:child_process'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import type { Readable } from 'node:stream'

const repoRoot = path.resolve(import.meta.dirname, '..', '..', '..')

export interface ServerHandle {
  baseUrl: string
  smtpHost: string
  smtpPort: number
  bearer: string
  stop(): Promise<void>
}

let builtBinary: Promise<string> | undefined

/** Builds cmd/authstunt once per test process and returns its path. */
export function buildBinary(): Promise<string> {
  builtBinary ??= (async (): Promise<string> => {
    const dir = await mkdtemp(path.join(tmpdir(), 'authstunt-client-bin-'))
    const bin = path.join(dir, 'authstunt')
    await run('go', ['build', '-o', bin, './cmd/authstunt'], repoRoot)
    return bin
  })()
  return builtBinary
}

/** Provisions a fresh data dir and starts serve on ephemeral ports. */
export async function startServer(): Promise<ServerHandle> {
  const bin = await buildBinary()
  const dataDir = await mkdtemp(path.join(tmpdir(), 'authstunt-client-data-'))
  const bearerPath = path.join(dataDir, 'bearer-out')

  // First consumer of provision --out: the credential lands in a 0600 file,
  // never on stdout, which is exactly the CI shape the flag was built for.
  await run(bin, [
    'project', 'bearer', 'provision',
    '--data-dir', dataDir,
    '--project', 'client-test',
    '--domain', '*.client.test',
    '--out', bearerPath,
  ], repoRoot)
  const bearer = (await readFile(bearerPath, 'utf8')).trim()

  const child = spawn(bin, [
    'serve',
    '--data-dir', dataDir,
    '--smtp-listen', '127.0.0.1:0',
    '--api-listen', '127.0.0.1:0',
  ], { cwd: repoRoot, stdio: ['ignore', 'pipe', 'pipe'] })

  const { apiAddr, smtpAddr } = await readyLine(child)
  const [smtpHost, smtpPort] = splitHostPort(smtpAddr)

  return {
    baseUrl: `http://${apiAddr}`,
    smtpHost,
    smtpPort,
    bearer,
    stop: async (): Promise<void> => {
      child.kill('SIGTERM')
      await new Promise<void>((resolve) => {
        child.once('exit', () => { resolve() })
      })
      await rm(dataDir, { recursive: true, force: true })
    },
  }
}

/** Reads the redacted evidence ledger for one run, using the project
 * bearer. This is harness-side verification machinery, deliberately not
 * part of the client surface. */
export async function evidence(
  handle: ServerHandle,
  runId: string,
): Promise<{ action: string; detail: Record<string, unknown> }[]> {
  const response = await fetch(`${handle.baseUrl}/api/v1/runs/${runId}/evidence`, {
    headers: { authorization: `Bearer ${handle.bearer}` },
  })
  if (response.status !== 200) {
    throw new Error(`evidence read failed: HTTP ${String(response.status)}`)
  }
  const body = await response.json() as {
    events: { action: string; detail: Record<string, unknown> | null }[]
  }
  return body.events.map((e) => ({ action: e.action, detail: e.detail ?? {} }))
}

// The startup line looks like:
// authstunt serving project X, api 127.0.0.1:PORT, long-poll on, pooled off, seeder off, smtp 127.0.0.1:PORT
const readyPattern = /^authstunt serving project .*, api ([^,]+),.*, smtp (.+)$/m

function readyLine(
  child: ChildProcessByStdio<null, Readable, Readable>,
): Promise<{ apiAddr: string; smtpAddr: string }> {
  return new Promise((resolve, reject) => {
    let out = ''
    let err = ''
    const timer = setTimeout(() => {
      child.kill('SIGKILL')
      reject(new Error(`serve did not report ready in 30s\nstdout: ${out}\nstderr: ${err}`))
    }, 30_000)
    child.stdout.setEncoding('utf8')
    child.stderr.setEncoding('utf8')
    child.stdout.on('data', (chunk: string) => {
      out += chunk
      const match = readyPattern.exec(out)
      if (match?.[1] !== undefined && match[2] !== undefined) {
        clearTimeout(timer)
        resolve({ apiAddr: match[1].trim(), smtpAddr: match[2].trim() })
      }
    })
    child.stderr.on('data', (chunk: string) => { err += chunk })
    child.once('exit', (code) => {
      clearTimeout(timer)
      reject(new Error(`serve exited early with code ${String(code)}\nstdout: ${out}\nstderr: ${err}`))
    })
  })
}

function splitHostPort(addr: string): [string, number] {
  const colon = addr.lastIndexOf(':')
  return [addr.slice(0, colon), Number(addr.slice(colon + 1))]
}

function run(command: string, args: string[], cwd: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { cwd, stdio: ['ignore', 'pipe', 'pipe'] })
    let output = ''
    child.stdout.setEncoding('utf8')
    child.stderr.setEncoding('utf8')
    child.stdout.on('data', (chunk: string) => { output += chunk })
    child.stderr.on('data', (chunk: string) => { output += chunk })
    child.once('error', reject)
    child.once('exit', (code) => {
      if (code === 0) {
        resolve()
      } else {
        reject(new Error(`${command} ${args.join(' ')} exited ${String(code)}:\n${output}`))
      }
    })
  })
}
