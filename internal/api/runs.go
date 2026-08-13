package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/ivermin1123/authstunt/internal/store"
)

// createRunResponse carries the one and only sight of a run token.
type createRunResponse struct {
	RunID string `json:"run_id"`
	// RunToken is returned here and never again. It is not stored in
	// plaintext anywhere, so a caller that loses it must start a new run.
	RunToken string `json:"run_token"`
	// CheckpointAt is the instant mail older than this run is excluded
	// from. A harness records it so an unexpected claim outcome can be
	// reasoned about afterwards.
	CheckpointAt time.Time `json:"checkpoint_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// handleCreateRun starts a run. Project bearer only: a run token cannot
// create the run that would authorize it.
func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireProject(w, r); !ok {
		return
	}
	run, token, err := s.service.CreateRun(r.Context())
	if err != nil {
		s.logger.Error("api: create run", "error", err)
		writeError(w, s.logger, http.StatusInternalServerError, codeInternal, "could not start a run")
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, createRunResponse{
		RunID:        run.ID,
		RunToken:     token,
		CheckpointAt: run.CheckpointAt,
		ExpiresAt:    run.ExpiresAt,
	})
}

type endRunRequest struct {
	// State is complete or failed. Expired is not accepted: only the
	// sweep decides that a run ran out of time.
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type endRunResponse struct {
	RunID  string `json:"run_id"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

// handleEndRun moves a run to a terminal state and releases its leases.
//
// Ending an already-ended run the same way is not an error, because a
// test teardown runs on paths that may have ended it already and failing
// there would turn cleanup into a source of red builds.
func (s *Server) handleEndRun(w http.ResponseWriter, r *http.Request) {
	run, ok := s.runFor(w, r)
	if !ok {
		return
	}
	var body endRunRequest
	if !decodeJSON(w, s.logger, r, &body) {
		return
	}
	switch body.State {
	case store.RunComplete, store.RunFailed:
	case "":
		body.State = store.RunComplete
	default:
		writeError(w, s.logger, http.StatusBadRequest, codeBadRequest,
			"state must be complete or failed")
		return
	}

	if err := s.service.EndRun(r.Context(), run.ID, body.State, body.Reason); err != nil {
		if errors.Is(err, store.ErrRunNotActive) {
			writeError(w, s.logger, http.StatusConflict, store.ReasonRunNotActive,
				"this run already reached a different terminal state")
			return
		}
		s.logger.Error("api: end run", "error", err, "run_id", run.ID)
		writeError(w, s.logger, http.StatusInternalServerError, codeInternal, "could not end this run")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, endRunResponse{
		RunID:  run.ID,
		State:  body.State,
		Reason: body.Reason,
	})
}

// evidenceEvent is one ledger entry as the wire sees it.
//
// Detail is the stored typed event, forwarded as raw JSON rather than
// re-encoded. That is deliberate: the ledger redacts on the way in, its
// event types cannot represent a secret, and rebuilding the object here
// would create a second path where a future field could escape the
// redaction the writer applied.
type evidenceEvent struct {
	TS     time.Time       `json:"ts"`
	Actor  string          `json:"actor"`
	RunID  string          `json:"run_id,omitempty"`
	Action string          `json:"action"`
	Detail json.RawMessage `json:"detail"`
}

type evidenceResponse struct {
	RunID  string          `json:"run_id"`
	Events []evidenceEvent `json:"events"`
}

// evidenceLimit caps one evidence read. A run that produced more events
// than this has a problem the harness should be told about by other
// means; the cap is here so one request cannot pull an unbounded result
// set into memory.
const evidenceLimit = 1000

// handleRunEvidence returns the redacted ledger for one run.
func (s *Server) handleRunEvidence(w http.ResponseWriter, r *http.Request) {
	run, ok := s.runFor(w, r)
	if !ok {
		return
	}
	entries, err := s.store.ListLedger(r.Context(), store.LedgerFilter{
		ProjectID: run.ProjectID,
		RunID:     run.ID,
		Limit:     evidenceLimit,
	})
	if err != nil {
		s.logger.Error("api: read evidence", "error", err, "run_id", run.ID)
		writeError(w, s.logger, http.StatusInternalServerError, codeInternal, "could not read evidence")
		return
	}
	events := make([]evidenceEvent, 0, len(entries))
	for _, e := range entries {
		detail := json.RawMessage(e.DetailJSON)
		if len(detail) == 0 {
			detail = json.RawMessage("null")
		}
		events = append(events, evidenceEvent{
			TS:     e.TS,
			Actor:  e.Actor,
			RunID:  e.RunID,
			Action: e.Action,
			Detail: detail,
		})
	}
	writeJSON(w, s.logger, http.StatusOK, evidenceResponse{RunID: run.ID, Events: events})
}

// runFor resolves the {run_id} path value and authorizes the caller for
// it.
//
// A run the caller does not own answers 404, not 403. Telling an
// unauthorized caller that an id exists is a membership oracle, and the
// id space is the only thing separating two runs of the same project.
func (s *Server) runFor(w http.ResponseWriter, r *http.Request) (store.Run, bool) {
	runID := r.PathValue("run_id")
	if runID == "" {
		writeError(w, s.logger, http.StatusBadRequest, codeBadRequest, "a run id is required")
		return store.Run{}, false
	}
	run, err := s.store.Run(r.Context(), runID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.logger.Error("api: read run", "error", err)
		}
		writeError(w, s.logger, http.StatusNotFound, codeNotFound, "no such run")
		return store.Run{}, false
	}
	if !principalOf(r.Context()).ownsRun(run) {
		writeError(w, s.logger, http.StatusNotFound, codeNotFound, "no such run")
		return store.Run{}, false
	}
	return run, true
}

// decodeJSON reads a JSON body, tolerating an empty one.
//
// An absent body means "all defaults", which is what a caller ending a
// run with no reason sends. Unknown fields are refused so a typo in a
// field name fails loudly instead of being silently ignored, which is
// exactly the failure mode a provisional surface must not have.
func decodeJSON(w http.ResponseWriter, logger *slog.Logger, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, logger, http.StatusRequestEntityTooLarge, codePayloadTooBig,
				"the request body is too large")
			return false
		}
		writeError(w, logger, http.StatusBadRequest, codeBadRequest, "the request body is not valid JSON")
		return false
	}
	return true
}
