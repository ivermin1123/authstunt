package api

import (
	"net/http"
	"time"

	"github.com/ivermin1123/authstunt/internal/personas"
	"github.com/ivermin1123/authstunt/internal/store"
)

// maxClaimTimeout caps how long one claim may park.
//
// It sits under the server's write timeout so a claim that waits the
// whole way still gets to write its answer.
const maxClaimTimeout = 2 * time.Minute

type claimRequest struct {
	// Kind is email_otp or magic_link. totp is refused here, not left to
	// fail deeper: it is a phase 3 stub and the surface says so plainly.
	Kind string `json:"kind"`
	// IdempotencyKey makes a retry replay the first answer instead of
	// consuming a second message. A Playwright retry is a retry, not a
	// new attempt.
	IdempotencyKey string `json:"idempotency_key"`
	TimeoutMS      int64  `json:"timeout_ms"`
}

// claimResponse is the only route that can carry a secret.
//
// Value is present only when Reason is claim_ok, and `omitempty` is not
// what enforces that: the handler leaves it empty on every other path.
// Every failure carries its reason code and nothing else, so a caller
// cannot learn anything about a message it was not allowed to claim.
type claimResponse struct {
	Reason    string `json:"reason"`
	ClaimID   string `json:"claim_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Value     string `json:"value,omitempty"`
	// TimedOut is the frozen long-poll envelope: a claim that waited out
	// its deadline is a 200 with this set, not an HTTP error, because
	// nothing went wrong.
	TimedOut bool  `json:"timed_out"`
	WaitedMS int64 `json:"waited_ms"`
}

// handleClaim hands over one fresh secret bound to this lease, once.
func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	lease, ok := s.leaseFor(w, r)
	if !ok {
		return
	}
	var body claimRequest
	if !decodeJSON(w, s.logger, r, &body) {
		return
	}
	switch body.Kind {
	case store.ClaimEmailOTP, store.ClaimMagicLink:
	case "":
		writeError(w, s.logger, http.StatusBadRequest, codeBadRequest,
			"kind is required: email_otp or magic_link")
		return
	case store.ClaimTOTP:
		// STUB(p3): TOTP has no implementation and this surface refuses
		// it rather than letting a caller believe it was attempted.
		writeError(w, s.logger, http.StatusNotImplemented, codeBadRequest,
			"totp is not implemented")
		return
	default:
		writeError(w, s.logger, http.StatusBadRequest, codeBadRequest,
			"kind must be email_otp or magic_link")
		return
	}
	if body.IdempotencyKey == "" {
		writeError(w, s.logger, http.StatusBadRequest, codeBadRequest,
			"an idempotency key is required so a retry cannot consume a second message")
		return
	}
	timeout := time.Duration(body.TimeoutMS) * time.Millisecond
	if timeout < 0 || timeout > maxClaimTimeout {
		writeError(w, s.logger, http.StatusBadRequest, codeBadRequest,
			"timeout_ms must be between 0 and 120000")
		return
	}

	claimed, err := s.service.Claim(r.Context(), personas.ClaimRequest{
		LeaseID:        lease.ID,
		Kind:           body.Kind,
		IdempotencyKey: body.IdempotencyKey,
		Timeout:        timeout,
	})
	if err != nil {
		// A refusal the service named arrives as a reason on Claimed, not
		// as an error; reaching here means something broke, and the
		// detail can carry an address, so only the code crosses the wire.
		s.logger.Error("api: claim", "error", err, "lease_id", lease.ID)
		writeError(w, s.logger, http.StatusInternalServerError, codeInternal,
			"could not settle this claim")
		return
	}

	resp := claimResponse{
		Reason:   claimed.Reason,
		ClaimID:  claimed.ClaimID,
		TimedOut: claimed.Reason == store.ReasonTimeout,
		WaitedMS: claimed.Waited.Milliseconds(),
	}
	// The secret and the message it came from are attached on exactly one
	// path. Anything else, including a suspect binding or a stale filter,
	// answers with its reason and nothing more.
	if claimed.OK() {
		resp.MessageID = claimed.MessageID
		resp.Value = claimed.Value
	}
	writeJSON(w, s.logger, http.StatusOK, resp)
}
