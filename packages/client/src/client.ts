import { randomUUID } from 'node:crypto'
import {
  ClaimRefusedError,
  ClaimTimeoutError,
  type ClaimFailureReason,
  type ClaimKind,
} from './errors.js'
import { apiError, contractError, requestJson, type HttpResult, type RequestArgs } from './http.js'

/** Default long-poll wait, and the server's hard cap on one wait. */
const defaultTimeoutMs = 30_000
const maxTimeoutMs = 120_000

// The watchdog aborts a claim attempt whose socket died. It sits a margin
// ABOVE the requested long-poll wait: a server that answers "timed_out" at
// the deadline is the normal path, and a watchdog below the wait would turn
// every honest long-poll into a fake transport error.
const watchdogMarginMs = 30_000

// Internal retry: transport failures and 5xx are retried with the SAME
// idempotency key, so a retry replays the first answer instead of consuming
// a second message. Only the claim route retries: it is the one route the
// server makes idempotent, and retrying a lease acquire would leak leases.
const maxClaimAttempts = 3
const retryDelayMs = 250

export interface AuthstuntOptions {
  /** Origin of the server, e.g. http://127.0.0.1:8925 */
  baseUrl: string
  /** The project bearer. Used once, to create the run; every later call
   * runs on the run token the server minted for that run. */
  bearer: string
}

export interface LeaseSpec {
  role: string
}

export interface ClaimOptions {
  /** Long-poll wait in milliseconds. Default 30000, server cap 120000. */
  timeoutMs?: number
  /** Defaults to a fresh UUID per call. Pass one explicitly only to share
   * a claim across two processes. */
  idempotencyKey?: string
}

export interface Claim {
  reason: 'claim_ok'
  /** The OTP or the magic link. */
  value: string
  /** The one message this claim is bound to. */
  messageId: string
  claimId: string
  waitedMs: number
}

export interface ClaimFailure {
  reason: ClaimFailureReason
  waitedMs: number
  timedOut: boolean
}

export type ClaimOutcome = Claim | ClaimFailure

export interface Lease {
  readonly id: string
  /** The mailbox this run owns exclusively. */
  readonly addr: string
  readonly expiresAt: Date
  /** Waits for a fresh value of this kind and throws a ClaimError on every
   * non-ok outcome, so a broken claim turns a test red on its own. */
  claim(kind: ClaimKind, opts?: ClaimOptions): Promise<Claim>
  /** Same call, union answer: no outcome throws. For asserting on a reason
   * code without try/catch. */
  tryClaim(kind: ClaimKind, opts?: ClaimOptions): Promise<ClaimOutcome>
  /** DELETE the lease. Idempotent, like the server. */
  release(): Promise<void>
}

export interface Run {
  readonly id: string
  readonly checkpointAt: Date
  readonly expiresAt: Date
  lease(spec: LeaseSpec): Promise<Lease>
  /** Ends the run and releases every identity it still holds, so the pool
   * gets them back now rather than when the run's TTL expires. Safe to
   * call in a teardown that may run twice or run late: a run that is
   * already not active counts as ended.
   *
   * This is the one call here that is outside the frozen core - the route
   * is provisional - so it may change shape in a v1 release when the rest
   * cannot. It is worth having anyway, because the alternative for a
   * fixture is holding identities until a TTL expires. */
  end(): Promise<void>
}

export interface AuthstuntClient {
  run(): Promise<Run>
}

// Wire shapes of the frozen surface (F1-F3). Field names are commitments.
interface WireRun {
  run_id: string
  run_token: string
  checkpoint_at: string
  expires_at: string
}

interface WireLease {
  lease_id: string
  addr: string
  expires_at: string
}

interface WireClaim {
  reason: string
  claim_id?: string
  message_id?: string
  value?: string
  timed_out: boolean
  waited_ms: number
}

/** Builds a client. No connection is made until run() is called. */
export function authstunt(options: AuthstuntOptions): AuthstuntClient {
  // Fail here, not three retries deep: a malformed base URL or a bearer
  // with stray whitespace (the classic trailing newline from a file read)
  // would otherwise surface as a retried transport failure with the real
  // cause buried underneath.
  try {
    void new URL(options.baseUrl)
  } catch {
    throw new Error(`authstunt: baseUrl is not a valid URL: ${options.baseUrl}`)
  }
  if (options.bearer === '' || /\s/.test(options.bearer)) {
    throw new Error('authstunt: bearer must be a non-empty token without whitespace')
  }
  return {
    run: async (): Promise<Run> => {
      const args: RequestArgs = {
        baseUrl: options.baseUrl,
        token: options.bearer,
        method: 'POST',
        path: '/api/v1/runs',
        watchdogMs: watchdogMarginMs,
      }
      const result = await requestJson(args)
      if (result.status !== 201) {
        throw apiError(args, result)
      }
      const wire = result.body as Partial<WireRun> | undefined
      if (typeof wire?.run_id !== 'string' || typeof wire.run_token !== 'string') {
        throw contractError(args, result, 'run')
      }
      return makeRun(options.baseUrl, wire as WireRun)
    },
  }
}

function makeRun(baseUrl: string, wire: WireRun): Run {
  const runToken = wire.run_token
  return {
    id: wire.run_id,
    checkpointAt: new Date(wire.checkpoint_at),
    expiresAt: new Date(wire.expires_at),
    lease: async (spec: LeaseSpec): Promise<Lease> => {
      const args: RequestArgs = {
        baseUrl,
        token: runToken,
        method: 'POST',
        path: `/api/v1/runs/${wire.run_id}/leases`,
        body: { role: spec.role },
        watchdogMs: watchdogMarginMs,
      }
      const result = await requestJson(args)
      if (result.status !== 201) {
        throw apiError(args, result)
      }
      const lease = result.body as Partial<WireLease> | undefined
      if (typeof lease?.lease_id !== 'string' || typeof lease.addr !== 'string') {
        throw contractError(args, result, 'lease')
      }
      return makeLease(baseUrl, runToken, lease as WireLease)
    },
    end: async (): Promise<void> => {
      const args: RequestArgs = {
        baseUrl,
        token: runToken,
        method: 'POST',
        path: `/api/v1/runs/${wire.run_id}/end`,
        body: {},
        watchdogMs: watchdogMarginMs,
      }
      const result = await requestJson(args)
      if (result.status === 200) {
        return
      }
      // 409 run_not_active means the run already reached a terminal state,
      // by an earlier end() or by the server's expiry sweep. The caller
      // asked for a run that is no longer running and that is what it has,
      // so this is not a failure of the call. Anything else is.
      if (result.status === 409) {
        const envelope = result.body as { error?: { code?: string } } | undefined
        if (envelope?.error?.code === 'run_not_active') {
          return
        }
      }
      throw apiError(args, result)
    },
  }
}

function makeLease(baseUrl: string, runToken: string, wire: WireLease): Lease {
  const leaseId = wire.lease_id

  const tryClaim = async (kind: ClaimKind, opts?: ClaimOptions): Promise<ClaimOutcome> => {
    const timeoutMs = opts?.timeoutMs ?? defaultTimeoutMs
    if (!Number.isInteger(timeoutMs) || timeoutMs < 0 || timeoutMs > maxTimeoutMs) {
      throw new RangeError(
        `authstunt: timeoutMs must be an integer between 0 and ${String(maxTimeoutMs)}, got ${String(timeoutMs)}`,
      )
    }
    // One key for the whole call, including every internal retry. A retry
    // that minted a fresh key would consume a second message; reusing the
    // key makes the server replay the first answer instead.
    const idempotencyKey = opts?.idempotencyKey ?? randomUUID()
    const deadline = Date.now() + timeoutMs
    // The whole call, retries included, ends by hardStop: the requested
    // wait plus ONE watchdog margin, not one margin per attempt. Two
    // reasons. A caller's own step budget is set against timeoutMs, so
    // the client must not quietly multiply it. And the server replays a
    // recorded claim for 120s from the instant it committed; a retry
    // fired later than that gets claim_expired for a message that was
    // handed over, which reads like a refusal and is really a burn. Past
    // hardStop the honest answer is a transport error.
    const hardStop = deadline + watchdogMarginMs

    let lastFailure: unknown
    for (let attempt = 1; attempt <= maxClaimAttempts; attempt++) {
      const now = Date.now()
      // A retry asks only for the server-side wait that is left of the
      // caller's budget, and its watchdog never reaches past hardStop.
      const remainingMs = attempt === 1 ? timeoutMs : Math.max(0, deadline - now)
      const args: RequestArgs = {
        baseUrl,
        token: runToken,
        method: 'POST',
        path: `/api/v1/leases/${leaseId}/claims`,
        body: { kind, idempotency_key: idempotencyKey, timeout_ms: remainingMs },
        watchdogMs: Math.max(1, Math.min(remainingMs + watchdogMarginMs, hardStop - now)),
      }
      let result: HttpResult
      try {
        result = await requestJson(args)
      } catch (transport) {
        // The socket died. That is distinct from the server answering
        // timed_out: this attempt got no answer at all, so it is retried
        // under the same key while the budget allows.
        lastFailure = transport
        if (attempt < maxClaimAttempts && Date.now() + retryDelayMs < hardStop) {
          await sleep(retryDelayMs)
          continue
        }
        throw new Error(
          `authstunt: claim did not get an answer after ${String(attempt)} attempt(s) - lease ${leaseId}`,
          { cause: lastFailure },
        )
      }
      if (result.status >= 500) {
        lastFailure = apiError(args, result)
        if (attempt < maxClaimAttempts && Date.now() + retryDelayMs < hardStop) {
          await sleep(retryDelayMs)
          continue
        }
        throw lastFailure
      }
      if (result.status !== 200) {
        throw apiError(args, result)
      }
      return outcomeOf(args, result)
    }
    // Unreachable: every loop exit above returns or throws.
    throw new Error('authstunt: claim retry loop ended without an outcome')
  }

  return {
    id: leaseId,
    addr: wire.addr,
    expiresAt: new Date(wire.expires_at),
    tryClaim,
    claim: async (kind: ClaimKind, opts?: ClaimOptions): Promise<Claim> => {
      const outcome = await tryClaim(kind, opts)
      if (outcome.reason === 'claim_ok') {
        return outcome
      }
      if (outcome.reason === 'claim_timeout') {
        throw new ClaimTimeoutError(outcome.waitedMs, leaseId)
      }
      throw new ClaimRefusedError(outcome.reason, outcome.waitedMs, outcome.timedOut, leaseId)
    },
    release: async (): Promise<void> => {
      const args: RequestArgs = {
        baseUrl,
        token: runToken,
        method: 'DELETE',
        path: `/api/v1/leases/${leaseId}`,
        watchdogMs: watchdogMarginMs,
      }
      const result = await requestJson(args)
      if (result.status !== 204) {
        throw apiError(args, result)
      }
    },
  }
}

function outcomeOf(args: RequestArgs, result: HttpResult): ClaimOutcome {
  const wire = result.body as Partial<WireClaim> | undefined
  if (typeof wire?.reason !== 'string') {
    throw contractError(args, result, 'claim')
  }
  if (wire.reason === 'claim_ok') {
    // The freeze guarantees value and message_id are present exactly when
    // the reason is claim_ok. Defaulting them to '' here would hand a test
    // an empty OTP and let it fail three steps later with nothing pointing
    // at the claim, so a violated guarantee fails loudly instead.
    if (
      typeof wire.value !== 'string' || wire.value === '' ||
      typeof wire.message_id !== 'string' || wire.message_id === '' ||
      typeof wire.claim_id !== 'string' || wire.claim_id === ''
    ) {
      throw contractError(args, result, 'claim_ok')
    }
    return {
      reason: 'claim_ok',
      value: wire.value,
      messageId: wire.message_id,
      claimId: wire.claim_id,
      waitedMs: wire.waited_ms ?? 0,
    }
  }
  return {
    // The vocabulary is append-only on the server, so a code minted after
    // this client shipped still flows through as a refusal rather than a
    // parse error.
    reason: wire.reason as ClaimFailureReason,
    waitedMs: wire.waited_ms ?? 0,
    timedOut: wire.timed_out ?? false,
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}
