package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/ivermin1123/authstunt/internal/personas"
	"github.com/ivermin1123/authstunt/internal/store"
)

type acquireLeaseRequest struct {
	Role string `json:"role"`
	// Mode is ephemeral or pooled. Empty means ephemeral, which is the
	// default the whole correctness argument rests on.
	Mode string `json:"mode"`
}

type acquireLeaseResponse struct {
	LeaseID    string `json:"lease_id"`
	IdentityID string `json:"identity_id"`
	Addr       string `json:"addr"`
	Role       string `json:"role"`
	// Mode is the mode actually served, read back from the identity row.
	// A request is never silently downgraded, so this always equals what
	// was asked for; it is reported so that a future divergence would be
	// visible in evidence rather than invisible.
	Mode      string `json:"mode"`
	SeedState string `json:"seed_state"`
	// PooledPolicy states the delivery bound in force, or null when
	// pooled mode is not configured. An operator reading a response can
	// see pooled is off instead of learning it from a refusal.
	PooledPolicy *pooledPolicyBody `json:"pooled_policy"`
	ExpiresAt    time.Time         `json:"expires_at"`
}

type pooledPolicyBody struct {
	MaxDeliveryLatencyMS int64 `json:"max_delivery_latency_ms"`
}

// handleAcquireLease grants the run an identity of the role asked for.
func (s *Server) handleAcquireLease(w http.ResponseWriter, r *http.Request) {
	run, ok := s.runFor(w, r)
	if !ok {
		return
	}
	var body acquireLeaseRequest
	if !decodeJSON(w, s.logger, r, &body) {
		return
	}
	if body.Role == "" {
		writeError(w, s.logger, http.StatusBadRequest, codeBadRequest, "a role is required")
		return
	}
	switch body.Mode {
	case store.ModeEphemeral, store.ModePooled:
	case "":
		body.Mode = store.ModeEphemeral
	default:
		writeError(w, s.logger, http.StatusBadRequest, codeBadRequest,
			"mode must be ephemeral or pooled")
		return
	}

	grant, err := s.service.Acquire(r.Context(), run.ID, body.Role, body.Mode)
	if err != nil {
		s.writeAcquireError(w, err, run.ID)
		return
	}

	resp := acquireLeaseResponse{
		LeaseID:    grant.LeaseID,
		IdentityID: grant.IdentityID,
		Addr:       grant.Addr,
		Role:       grant.Role,
		Mode:       grant.Mode,
		SeedState:  grant.SeedState,
		ExpiresAt:  grant.ExpiresAt,
	}
	if policy := s.service.PooledPolicy(); policy != nil {
		resp.PooledPolicy = &pooledPolicyBody{
			MaxDeliveryLatencyMS: policy.MaxDeliveryLatency.Milliseconds(),
		}
	}
	writeJSON(w, s.logger, http.StatusCreated, resp)
}

// writeAcquireError maps a refusal onto its reason code.
//
// Each of these is a decision the service already made and named; the
// handler's only job is to carry the name across the wire unchanged
// rather than flatten everything into a 500. A pooled request made
// against a service with no policy is refused as pooled_policy_missing
// and is never quietly served as ephemeral.
func (s *Server) writeAcquireError(w http.ResponseWriter, err error, runID string) {
	switch {
	case errors.Is(err, personas.ErrPooledPolicyMissing):
		writeError(w, s.logger, http.StatusConflict, store.ReasonPooledPolicyMissing,
			"pooled mode needs a declared delivery policy on this server")
	case errors.Is(err, store.ErrRunNotActive):
		writeError(w, s.logger, http.StatusConflict, store.ReasonRunNotActive,
			"this run is no longer active")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, s.logger, http.StatusNotFound, codeNotFound,
			"no identity is available for that role")
	default:
		// A seed failure carries the application's URL and response, so
		// the detail is logged and the caller gets the reason code only.
		s.logger.Error("api: acquire lease", "error", err, "run_id", runID)
		writeError(w, s.logger, http.StatusConflict, store.ReasonLeaseSeedFailed,
			"the identity could not be prepared for use")
	}
}

// handleReleaseLease gives an identity back.
//
// Releasing twice is not an error: teardown paths overlap, and a release
// that failed because it already happened would make cleanup a source of
// red builds.
func (s *Server) handleReleaseLease(w http.ResponseWriter, r *http.Request) {
	lease, ok := s.leaseFor(w, r)
	if !ok {
		return
	}
	if lease.State == store.LeaseReleased {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.service.Release(r.Context(), lease.RunID, lease.ID); err != nil {
		s.logger.Error("api: release lease", "error", err, "lease_id", lease.ID)
		writeError(w, s.logger, http.StatusInternalServerError, codeInternal,
			"could not release this lease")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// leaseFor resolves {lease_id} and authorizes the caller through the run
// that owns it.
//
// Authorization is by the lease's run, never by the lease id alone: a
// lease is only ever reachable by the run that holds it, and a project
// bearer reaches it through the same ownsRun check. A lease belonging to
// another run answers 404 for the same reason a foreign run does.
func (s *Server) leaseFor(w http.ResponseWriter, r *http.Request) (store.Lease, bool) {
	leaseID := r.PathValue("lease_id")
	if leaseID == "" {
		writeError(w, s.logger, http.StatusBadRequest, codeBadRequest, "a lease id is required")
		return store.Lease{}, false
	}
	lease, err := s.store.Lease(r.Context(), leaseID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.logger.Error("api: read lease", "error", err)
		}
		writeError(w, s.logger, http.StatusNotFound, codeNotFound, "no such lease")
		return store.Lease{}, false
	}
	run, err := s.store.Run(r.Context(), lease.RunID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.logger.Error("api: read the run behind a lease", "error", err)
		}
		writeError(w, s.logger, http.StatusNotFound, codeNotFound, "no such lease")
		return store.Lease{}, false
	}
	if !principalOf(r.Context()).ownsRun(run) {
		writeError(w, s.logger, http.StatusNotFound, codeNotFound, "no such lease")
		return store.Lease{}, false
	}
	return lease, true
}
