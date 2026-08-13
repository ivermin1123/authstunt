package api

import (
	"net/http"

	"github.com/ivermin1123/authstunt/internal/store"
)

// healthResponse is what a supervisor and a harness preflight read.
//
// It is unauthenticated, so it carries only facts a caller learns by
// connecting anyway: that the process is up, what contract it speaks and
// whether pooled mode is configured. Deliberately absent: the project id
// and name, any address, any count of runs, leases, identities or
// messages, and anything at all about credentials. A count is a side
// channel about activity, and this route has no principal to scope one
// to.
type healthResponse struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	SchemaVersion int    `json:"schema_version"`
	// Surface marks this contract as unfrozen so a client that pinned it
	// learns from the server, not from a changelog it did not read.
	Surface string `json:"surface"`
	// DefaultMode is the mode a lease request gets when it names none.
	DefaultMode string `json:"default_mode"`
	// PooledConfigured reports whether pooled mode could be served at
	// all. Pooled stays conditional and experimental; ephemeral is the
	// default and the mode every correctness claim rests on.
	PooledConfigured bool `json:"pooled_configured"`
}

// handleHealth answers the liveness probe.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.logger, http.StatusOK, healthResponse{
		Status:           "ok",
		Version:          s.version,
		SchemaVersion:    store.SchemaVersion,
		Surface:          "provisional-4a",
		DefaultMode:      store.ModeEphemeral,
		PooledConfigured: s.service.PooledPolicy() != nil,
	})
}
