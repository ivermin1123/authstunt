// The reason vocabulary below is the frozen set exported verbatim from the
// server (internal/store/models.go, frozen by internal/api/doc.go). An
// existing code never changes meaning; the set only grows, so a reason this
// client has not heard of yet still arrives as a ClaimRefusedError.

/** A claim kind this build serves. totp is a refused stub on the server and
 * is deliberately not listed until it stops being one. */
export type ClaimKind = 'email_otp' | 'magic_link'

/** Reasons the server refuses to hand over a value. Mail may have arrived;
 * these say why none of it was handed over. */
export type ClaimRefusalReason =
  | 'claim_already_claimed'
  | 'claim_no_binding'
  | 'claim_stale_filtered'
  | 'claim_suspect_binding'
  | 'claim_expired'
  | 'lease_not_held'
  | 'lease_seed_failed'
  | 'run_not_active'
  | 'extraction_failed'

/** Every non-ok claim outcome. claim_timeout is the one outcome that is
 * genuinely about running out of time: something bound to the lease and had
 * not settled into a claimable value when the wait ran out. */
export type ClaimFailureReason = ClaimRefusalReason | 'claim_timeout'

/** Common root so `catch (e) { if (e instanceof ClaimError) ... }` catches
 * every non-ok outcome of claim() with one check. */
export abstract class ClaimError extends Error {
  readonly reason: ClaimFailureReason
  /** How long the server let the call park before it settled. */
  readonly waitedMs: number
  /** Mirrors the wire field: true only when the reason is claim_timeout. */
  readonly timedOut: boolean

  constructor(reason: ClaimFailureReason, waitedMs: number, timedOut: boolean, leaseId: string) {
    super(`claim failed: ${reason} (waited ${String(waitedMs)}ms) - lease ${leaseId}`)
    this.name = new.target.name
    this.reason = reason
    this.waitedMs = waitedMs
    this.timedOut = timedOut
  }
}

/** The server answered and said no. The reason code is the whole story and
 * it sits in the name, the message and the field. */
export class ClaimRefusedError extends ClaimError {
  declare readonly reason: ClaimRefusalReason

  // Not useless: it narrows the accepted reason from ClaimFailureReason to
  // ClaimRefusalReason, so a timeout cannot be constructed as a refusal.
  // eslint-disable-next-line @typescript-eslint/no-useless-constructor
  constructor(reason: ClaimRefusalReason, waitedMs: number, timedOut: boolean, leaseId: string) {
    super(reason, waitedMs, timedOut, leaseId)
  }
}

/** The wait ran out. On the wire this is a 200 with timed_out true, because
 * for the server nothing went wrong; for a test, "the code never became
 * claimable in time" is the result, so claim() throws it. */
export class ClaimTimeoutError extends ClaimError {
  declare readonly reason: 'claim_timeout'

  constructor(waitedMs: number, leaseId: string) {
    super('claim_timeout', waitedMs, true, leaseId)
  }
}
