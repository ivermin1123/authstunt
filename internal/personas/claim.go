package personas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ivermin1123/authstunt/internal/extract"
	"github.com/ivermin1123/authstunt/internal/ledger"
	"github.com/ivermin1123/authstunt/internal/sse"
	"github.com/ivermin1123/authstunt/internal/store"
)

// DefaultClaimTTL is how long a claim can be replayed under its
// idempotency key. It is short because its only job is to cover a test
// retry, not to keep a code usable.
const DefaultClaimTTL = 120 * time.Second

// ErrUnsupportedKind reports a claim kind this build cannot serve.
//
// STUB(p3): `totp` is defined in the schema and not implemented. A
// correct RFC 6238 code needs either a new dependency or hand-written
// crypto, both owner decisions, and `secrets` is keyed by persona, so it
// could only ever serve persona-backed pooled identities. Recorded in
// DECISIONS.md and in the phase 3 contract, section 8.
var ErrUnsupportedKind = errors.New("personas: claim kind is not supported by this build")

// Claimed is the answer to a claim: one reason code, and a value only
// when the reason is claim_ok.
//
// Value is the single field in this package that carries a secret. It
// never reaches a ledger event, and the evidence written alongside it
// names the claim and the message instead.
type Claimed struct {
	Reason    string
	ClaimID   string
	MessageID string
	Value     string
	// Waited is how long the call parked before it settled, for the
	// latency the phase measures.
	Waited time.Duration
}

// OK reports whether a secret was handed over.
func (c Claimed) OK() bool { return c.Reason == store.ReasonOK }

// ClaimRequest is one attempt to take a fresh secret from a lease.
//
// There is no `since` field, and that absence is the contract: the lower
// bound is max(run.checkpoint_at, lease.acquired_at), which the server
// stamped before the browser acted. A client-supplied bound would be set
// by the party with a reason to widen it.
type ClaimRequest struct {
	LeaseID        string
	Kind           string
	IdempotencyKey string
	Timeout        time.Duration
}

// Claim hands over one fresh secret bound to this lease, exactly once.
//
// The order of the work is the contract. The gates run first and refuse
// early. The idempotency key is checked before any candidate is
// considered, so a retry cannot consume a second message. The bus
// subscription is registered before the first query, because a message
// committed in between would otherwise be a wakeup nobody was listening
// for and the caller would wait out its whole deadline for mail that had
// already arrived.
func (s *Service) Claim(ctx context.Context, req ClaimRequest) (Claimed, error) {
	started := time.Now()
	switch req.Kind {
	case store.ClaimEmailOTP, store.ClaimMagicLink:
	case store.ClaimTOTP:
		return Claimed{}, fmt.Errorf("%w: %s", ErrUnsupportedKind, req.Kind)
	default:
		return Claimed{}, fmt.Errorf("personas: %q is not a claim kind", req.Kind)
	}
	if req.IdempotencyKey == "" {
		return Claimed{}, errors.New("personas: a claim needs an idempotency key")
	}

	lease, err := s.store.Lease(ctx, req.LeaseID)
	if err != nil {
		return Claimed{}, err
	}
	identity, err := s.store.Identity(ctx, lease.IdentityID)
	if err != nil {
		return Claimed{}, err
	}
	run, err := s.store.Run(ctx, lease.RunID)
	if err != nil {
		return Claimed{}, err
	}

	if reason, refused := gate(run, lease); refused {
		return s.settleClaim(ctx, lease, identity, req, Claimed{Reason: reason}, started)
	}

	// Replay before anything else. A retried Playwright test reusing its
	// key must get the same code back, not a second one, and it must not
	// consume a candidate on the way to finding that out.
	if replay, found, err := s.replay(ctx, lease, req); err != nil {
		return Claimed{}, err
	} else if found {
		return s.settleClaim(ctx, lease, identity, req, replay, started)
	}

	lower := lowerBound(run, lease)

	// Registered before the first query: this is the lost-wakeup seam. A
	// message committed between a query and a subscription would be a
	// wakeup nobody was listening for, and the caller would wait out its
	// whole deadline for mail that had already landed.
	var waiter *sse.Waiter
	if s.bus != nil && req.Timeout > 0 {
		waiter, err = s.bus.SubscribeMatch(ctx, matchAddr(identity.Addr))
		if err != nil {
			return Claimed{}, fmt.Errorf("personas: claim cannot wait: %w", err)
		}
		defer waiter.Close()
	}

	deadline := time.Now().Add(req.Timeout)
	for {
		result, err := s.attempt(ctx, run, lease, req, lower)
		if err != nil {
			return Claimed{}, err
		}
		if result.Reason != "" {
			return s.settleClaim(ctx, lease, identity, req, result, started)
		}
		if waiter == nil || !time.Now().Before(deadline) {
			break
		}
		waitCtx, cancel := context.WithDeadline(ctx, deadline)
		_, woke := waiter.Wait(waitCtx)
		cancel()
		if !woke {
			break
		}
	}

	final, err := s.explain(ctx, lease, req, lower)
	if err != nil {
		return Claimed{}, err
	}
	return s.settleClaim(ctx, lease, identity, req, final, started)
}

// gate applies the three refusals that do not depend on any message.
func gate(run store.Run, lease store.Lease) (string, bool) {
	switch {
	case !run.Active():
		return store.ReasonRunNotActive, true
	case !lease.Held():
		return store.ReasonLeaseNotHeld, true
	case !lease.Claimable():
		// Held, but the seed never reached a confirmed state. Pending
		// fails closed here as much as failed does: the account may not be
		// in the state the run expects.
		return store.ReasonLeaseSeedFailed, true
	}
	return "", false
}

// lowerBound is the server-owned floor on message freshness.
//
// A lease is always acquired after its run was created, so the max is
// almost always the acquire instant. Both terms are kept because they
// answer different questions - when did this run start caring, and when
// did it take this address - and a future change that makes them diverge
// must not silently lose the stricter one.
func lowerBound(run store.Run, lease store.Lease) time.Time {
	if lease.AcquiredAt.After(run.CheckpointAt) {
		return lease.AcquiredAt
	}
	return run.CheckpointAt
}

// matchAddr builds the bus predicate. It matches the exact leased
// address on envelope recipients only, which under the exclusivity
// invariant means it can only ever wake this run.
func matchAddr(addr string) func(sse.Event) bool {
	want := store.NormalizeAddress(addr)
	return func(ev sse.Event) bool {
		if ev.Message == nil {
			return false
		}
		for _, r := range ev.Message.Recipients {
			if store.NormalizeAddress(r) == want {
				return true
			}
		}
		return false
	}
}

// replay answers a repeated idempotency key.
func (s *Service) replay(ctx context.Context, lease store.Lease, req ClaimRequest) (Claimed, bool, error) {
	existing, err := s.store.ClaimByKey(ctx, lease.ID, req.IdempotencyKey)
	if errors.Is(err, store.ErrNotFound) {
		return Claimed{}, false, nil
	}
	if err != nil {
		return Claimed{}, false, err
	}
	if !time.Now().Before(existing.ExpiresAt) {
		// The key is spent. Reporting expiry rather than re-deriving the
		// value is what keeps a claim TTL from being a suggestion.
		return Claimed{Reason: store.ReasonExpired, ClaimID: existing.ID}, true, nil
	}
	value, err := s.valueOf(ctx, existing.MessageID, req.Kind)
	if err != nil {
		return Claimed{}, false, err
	}
	if value == "" {
		// The message behind the claim is gone or no longer readable.
		// There is nothing to hand over and nothing to retry.
		return Claimed{Reason: store.ReasonExtractionFail, ClaimID: existing.ID}, true, nil
	}
	return Claimed{
		Reason:    store.ReasonOK,
		ClaimID:   existing.ID,
		MessageID: existing.MessageID,
		Value:     value,
	}, true, nil
}

// attempt tries once to take a candidate. An empty reason means nothing
// was eligible yet and the caller should keep waiting.
func (s *Service) attempt(ctx context.Context, run store.Run, lease store.Lease, req ClaimRequest, lower time.Time) (Claimed, error) {
	candidates, _, err := s.store.ClaimCandidates(ctx, store.CandidateFilter{
		LeaseID:   lease.ID,
		Kind:      req.Kind,
		NotBefore: lower,
	})
	if err != nil {
		return Claimed{}, err
	}
	for _, candidate := range candidates {
		value := valueFor(candidate.ExtractedJSON, req.Kind)
		if value == "" {
			// The message is eligible but carries nothing of this kind: a
			// welcome mail among the OTPs. Not a refusal, just not this.
			continue
		}
		claim, err := s.store.RecordClaim(ctx, store.NewClaim{
			LeaseID:        lease.ID,
			RunID:          run.ID,
			Kind:           req.Kind,
			MessageID:      candidate.MessageID,
			IdempotencyKey: req.IdempotencyKey,
			TTL:            s.claimTTL,
		})
		if errors.Is(err, store.ErrClaimTaken) {
			// One of the two unique indexes refused. Either this message
			// was claimed by another attempt, in which case the next
			// candidate is the right answer, or this key was recorded
			// concurrently, in which case the replay path owns it.
			if replay, found, err := s.replay(ctx, lease, req); err != nil {
				return Claimed{}, err
			} else if found {
				return replay, nil
			}
			continue
		}
		if err != nil {
			return Claimed{}, err
		}
		return Claimed{
			Reason:    store.ReasonOK,
			ClaimID:   claim.ID,
			MessageID: candidate.MessageID,
			Value:     value,
		}, nil
	}
	return Claimed{}, nil
}

// explain decides what to report when the deadline passed with nothing
// handed over.
//
// The order is most specific first. A bare timeout is the weakest answer
// this can give, so it is the last one: an operator debugging a missing
// code needs to know the mail arrived and was refused, and why, rather
// than that the wait ran out.
func (s *Service) explain(ctx context.Context, lease store.Lease, req ClaimRequest, lower time.Time) (Claimed, error) {
	_, refused, err := s.store.ClaimCandidates(ctx, store.CandidateFilter{
		LeaseID:   lease.ID,
		Kind:      req.Kind,
		NotBefore: lower,
	})
	if err != nil {
		return Claimed{}, err
	}
	switch {
	case refused.Suspect > 0:
		return Claimed{Reason: store.ReasonSuspectBinding}, nil
	case refused.AlreadyClaimed > 0:
		return Claimed{Reason: store.ReasonAlreadyClaimed}, nil
	case refused.Stale > 0:
		return Claimed{Reason: store.ReasonStaleFiltered}, nil
	case refused.ExtractionFail > 0:
		return Claimed{Reason: store.ReasonExtractionFail}, nil
	}
	if refused.Bindings == 0 {
		// Nothing was ever addressed to this lease. From the caller's
		// side that is the wrong-recipient symptom: the mail went
		// somewhere else, or to an address no lease owned when it
		// arrived, and the ledger's unbound_recipient events are where
		// the other half of that story is.
		return Claimed{Reason: store.ReasonNoBinding}, nil
	}
	// Something did bind and has not settled yet. This is the only
	// outcome that is genuinely about running out of time.
	return Claimed{Reason: store.ReasonTimeout}, nil
}

// valueOf re-derives a claimed value from the message behind a claim.
func (s *Service) valueOf(ctx context.Context, messageID, kind string) (string, error) {
	if messageID == "" {
		return "", nil
	}
	msg, err := s.store.Message(ctx, messageID)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrUnreadableMessage) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return valueFor(msg.ExtractedJSON, kind), nil
}

// valueFor reads the one field of an extraction a kind is about.
//
// A kind maps to exactly one field. Falling back to a second field when
// the first is empty would make "the OTP" mean whatever happened to be
// there, and the whole product is the promise that it does not.
func valueFor(extractedJSON, kind string) string {
	if extractedJSON == "" {
		return ""
	}
	var result extract.Result
	if err := json.Unmarshal([]byte(extractedJSON), &result); err != nil {
		return ""
	}
	switch kind {
	case store.ClaimEmailOTP:
		return result.OTPBest
	case store.ClaimMagicLink:
		return result.VerifyLinkBest
	default:
		return ""
	}
}

// settleClaim writes the evidence and returns the answer.
//
// Every outcome is recorded, not only the successful ones: a claim that
// found nothing is exactly the event somebody debugging a flaky test
// needs to find, and the reason code is the whole content of that record.
func (s *Service) settleClaim(ctx context.Context, lease store.Lease, identity store.Identity, req ClaimRequest, result Claimed, started time.Time) (Claimed, error) {
	result.Waited = time.Since(started)
	_, err := ledger.Write(ctx, s.store, s.meta(lease.RunID, identity), ledger.ClaimSettled{
		LeaseID:   lease.ID,
		ClaimID:   result.ClaimID,
		Kind:      req.Kind,
		MessageID: result.MessageID,
		Reason:    result.Reason,
		Addr:      ledger.Addr(identity.Addr),
	})
	if err != nil {
		return Claimed{}, err
	}
	return result, nil
}
