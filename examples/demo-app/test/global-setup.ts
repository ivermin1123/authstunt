// Brings up everything the spec needs and tells the workers where it is:
// one AuthStunt server, and this app pointed at its SMTP listener.
//
// If AUTHSTUNT_URL and AUTHSTUNT_BEARER are already set, the server is
// yours and nothing is started - which is the path a reader takes when
// running this example against a server they already have. Otherwise the
// binary in this repository is built and started on ephemeral ports.
//
// The app runs in this process rather than behind Playwright's webServer
// option, because that is what lets the SMTP target be an ephemeral port
// discovered a moment earlier instead of a number two config files have
// to agree on.

import { spawn } from 'node:child_process'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { start } from '../server.js'

const repoRoot = path.resolve(import.meta.dirname, '..', '..', '..')

interface Authstunt {
  url: string
  bearer: string
  smtpHost: string
  smtpPort: number
  stop: () => Promise<void>
}

export default async function globalSetup(): Promise<() => Promise<void>> {
  const server = await resolveAuthstunt()
  process.env['AUTHSTUNT_URL'] = server.url
  // Handed to the workers through the environment, the way the fixtures
  // document. It is never written to a file or a report.
  process.env['AUTHSTUNT_BEARER'] = server.bearer

  const app = await start({ smtpHost: server.smtpHost, smtpPort: server.smtpPort })
  process.env['DEMO_URL'] = app.url

  return async (): Promise<void> => {
    await app.stop()
    await server.stop()
  }
}

async function resolveAuthstunt(): Promise<Authstunt> {
  const url = process.env['AUTHSTUNT_URL']
  const bearer = process.env['AUTHSTUNT_BEARER']
  if (url !== undefined && url !== '' && bearer !== undefined && bearer !== '') {
    return {
      url,
      bearer,
      smtpHost: process.env['SMTP_HOST'] ?? '127.0.0.1',
      smtpPort: Number(process.env['SMTP_PORT'] ?? 1025),
      stop: async (): Promise<void> => {},
    }
  }
  return startAuthstunt()
}

/** Builds and starts the server from this repository. */
async function startAuthstunt(): Promise<Authstunt> {
  const dataDir = await mkdtemp(path.join(tmpdir(), 'authstunt-demo-'))
  const bin = process.env['AUTHSTUNT_BIN'] ?? path.join(dataDir, 'authstunt')
  if (process.env['AUTHSTUNT_BIN'] === undefined) {
    await runToCompletion('go', ['build', '-o', bin, './cmd/authstunt'])
  }

  // provision --out puts the credential in a 0600 file rather than on
  // stdout, so it never reaches a terminal or a CI log.
  const bearerPath = path.join(dataDir, 'bearer')
  await runToCompletion(bin, [
    'project', 'bearer', 'provision',
    '--data-dir', dataDir,
    '--project', 'demo',
    '--domain', '*.demo.test',
    '--out', bearerPath,
  ])
  const bearer = (await readFile(bearerPath, 'utf8')).trim()

  const child = spawn(bin, [
    'serve',
    '--data-dir', dataDir,
    '--smtp-listen', '127.0.0.1:0',
    '--api-listen', '127.0.0.1:0',
  ], { cwd: repoRoot, stdio: ['ignore', 'pipe', 'pipe'] })

  const { apiAddr, smtpAddr } = await readyLine(child)
  const colon = smtpAddr.lastIndexOf(':')

  return {
    url: `http://${apiAddr}`,
    bearer,
    smtpHost: smtpAddr.slice(0, colon),
    smtpPort: Number(smtpAddr.slice(colon + 1)),
    stop: async (): Promise<void> => {
      const exited = new Promise<void>((resolve) => {
        if (child.exitCode !== null || child.signalCode !== null) {
          resolve()
          return
        }
        child.once('exit', () => { resolve() })
      })
      child.kill('SIGTERM')
      await exited
      await rm(dataDir, { recursive: true, force: true })
    },
  }
}

// authstunt serving project demo, api 127.0.0.1:PORT, ..., smtp 127.0.0.1:PORT
const readyPattern = /authstunt serving project [^\n]*, api ([^,\n]+),[^\n]*, smtp ([^\n]+)\n/

function readyLine(
  child: ReturnType<typeof spawn>,
): Promise<{ apiAddr: string; smtpAddr: string }> {
  return new Promise((resolve, reject) => {
    let out = ''
    let err = ''
    const timer = setTimeout(() => {
      child.kill('SIGKILL')
      reject(new Error(`authstunt serve did not report ready in 30s\nstdout: ${out}\nstderr: ${err}`))
    }, 30_000)
    child.stdout?.setEncoding('utf8')
    child.stderr?.setEncoding('utf8')
    child.stdout?.on('data', (chunk: string) => {
      out += chunk
      const match = readyPattern.exec(out)
      if (match?.[1] !== undefined && match[2] !== undefined) {
        clearTimeout(timer)
        resolve({ apiAddr: match[1].trim(), smtpAddr: match[2].trim() })
      }
    })
    child.stderr?.on('data', (chunk: string) => { err += chunk })
    child.once('exit', (code) => {
      clearTimeout(timer)
      reject(new Error(`authstunt serve exited with ${String(code)}\nstdout: ${out}\nstderr: ${err}`))
    })
  })
}

function runToCompletion(command: string, args: string[]): Promise<void> {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { cwd: repoRoot, stdio: ['ignore', 'pipe', 'pipe'] })
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
