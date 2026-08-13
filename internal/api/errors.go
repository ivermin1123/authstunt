package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// errorBody is the one error shape every route answers with.
//
// Code is machine-readable and, wherever a reason code from the plan's
// section 3.4 applies, it is that reason code verbatim rather than a
// paraphrase. Message is for a human reading a test failure and carries
// no identifiers a caller did not already send.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// API-level codes. Claim outcomes use store's reason codes instead; these
// cover the cases that happen before any domain call is reached.
const (
	codeUnauthorized   = "unauthorized"
	codeForbidden      = "forbidden"
	codeNotFound       = "not_found"
	codeBadRequest     = "bad_request"
	codeMethodNotAllow = "method_not_allowed"
	codePayloadTooBig  = "payload_too_large"
	codeHostRejected   = "host_rejected"
	codeOriginRejected = "origin_rejected"
	codeInternal       = "internal_error"
)

// writeJSON sends a value with the status given.
//
// An encode failure after the header is out cannot be reported to the
// client, so it is logged and dropped: the alternative is a second
// WriteHeader call that only produces a confusing panic in the log.
func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Nothing here is cacheable and some of it is a credential.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Error("api: write response", "error", err)
	}
}

// writeError sends the error envelope.
//
// The message is never derived from an internal error string. A store or
// service error can carry an address, an id or a path, and the envelope is
// the one place that would leak it to a caller who has no scope for it;
// handlers log the detail and pass a fixed message here.
func writeError(w http.ResponseWriter, logger *slog.Logger, status int, code, message string) {
	writeJSON(w, logger, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}
