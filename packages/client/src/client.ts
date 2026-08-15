import { randomUUID } from 'node:crypto'
import {
  ClaimRefusedError,
  ClaimTimeoutError,
  type ClaimFailureReason,
  type ClaimKind,
} from './errors.js'
import { apiError, requestJson, type HttpResult, type RequestArgs } from './http.js'

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
      const wire = result.body as WireRun
      return makeRun(options.baseUrl, wire)
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
      return makeLease(baseUrl, runToken, result.body as WireLease)
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

    let lastFailure: unknown
    for (let attempt = 1; attempt <= maxClaimAttempts; attempt++) {
      // A retry asks only for the wait that is left, so a flaky socket
      // cannot stretch the caller's budget past what it asked for.
      const remainingMs = attempt === 1 ? timeoutMs : Math.max(0, deadline - Date.now())
      const args: RequestArgs = {
        baseUrl,
        token: runToken,
        method: 'POST',
        path: `/api/v1/leases/${leaseId}/claims`,
        body: { kind, idempotency_key: idempotencyKey, timeout_ms: remainingMs },
        watchdogMs: remainingMs + watchdogMarginMs,
      }
      let result: HttpResult
      try {
        result = await requestJson(args)
      } catch (transport) {
        // The socket died. That is distinct from the server answering
        // timed_out: this attempt got no answer at all, so it is retried
        // under the same key.
        lastFailure = transport
        if (attempt < maxClaimAttempts) {
          await sleep(retryDelayMs)
          continue
        }
        throw new Error(
          `authstunt: claim did not get an answer after ${String(attempt)} attempts - lease ${leaseId}`,
          { cause: lastFailure },
        )
      }
      if (result.status >= 500) {
        lastFailure = apiError(args, result)
        if (attempt < maxClaimAttempts) {
          await sleep(retryDelayMs)
          continue
        }
        throw lastFailure
      }
      if (result.status !== 200) {
        throw apiError(args, result)
      }
      return outcomeOf(result.body as WireClaim)
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

function outcomeOf(wire: WireClaim): ClaimOutcome {
  if (wire.reason === 'claim_ok') {
    return {
      reason: 'claim_ok',
      value: wire.value ?? '',
      messageId: wire.message_id ?? '',
      claimId: wire.claim_id ?? '',
      waitedMs: wire.waited_ms,
    }
  }
  return {
    // The vocabulary is append-only on the server, so a code minted after
    // this client shipped still flows through as a refusal rather than a
    // parse error.
    reason: wire.reason as ClaimFailureReason,
    waitedMs: wire.waited_ms,
    timedOut: wire.timed_out,
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}
