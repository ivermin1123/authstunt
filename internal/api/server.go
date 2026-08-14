// Package api serves the provisional HTTP surface a validation harness
// drives AuthStunt through.
//
// PROVISIONAL (phase 4a). Paths, parameters and field names may change
// without a major version until phase 4b freezes them. What is not
// provisional is the security behavior: principal scoping, the loopback
// default, the Host allowlist, the Origin check and redaction all ship
// here in full, because a real application's real credentials pass
// through this surface during gate A.
package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ivermin1123/authstunt/internal/personas"
	"github.com/ivermin1123/authstunt/internal/store"
)

// MaxRequestBytes caps a request body.
//
// Every body this surface accepts is a small JSON object; the largest
// legitimate one is a handful of short strings. The cap is generous
// against that and still small enough that a malicious body cannot cost
// meaningful memory.
const MaxRequestBytes = 64 << 10

// DefaultAddr is the listen address. Loopback, because this server hands
// out credentials and the operator has to opt in, in writing, to anything
// wider.
const DefaultAddr = "127.0.0.1:8925"

// writeTimeout bounds a response. It is longer than the longest claim
// long-poll, which parks on the bus with its own deadline; a write
// timeout shorter than that would cut off a claim that was behaving
// correctly.
const writeTimeout = 5 * time.Minute

// readHeaderTimeout bounds how long a connection may take to send its
// headers, which is what a slowloris client stalls on.
const readHeaderTimeout = 20 * time.Second

// Config builds a Server.
type Config struct {
	Store     *store.Store
	Service   *personas.Service
	ProjectID string
	// AllowedHosts is the Host allowlist. Empty means loopback names
	// only, which is the correct default for the default bind.
	AllowedHosts []string
	Version      string
	Logger       *slog.Logger
}

// Server is the HTTP surface.
type Server struct {
	store        *store.Store
	service      *personas.Service
	projectID    string
	allowedHosts []string
	version      string
	logger       *slog.Logger
	handler      http.Handler
}

// defaultAllowedHosts is what a Host header may say when the operator
// named nothing. These are the names that reach a loopback listener.
var defaultAllowedHosts = []string{"localhost", "127.0.0.1", "::1"}

// New validates the configuration and builds the server.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, errors.New("api: a store is required")
	}
	if cfg.Service == nil {
		return nil, errors.New("api: a lease service is required")
	}
	if cfg.ProjectID == "" {
		return nil, errors.New("api: a project id is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	hosts := cfg.AllowedHosts
	if len(hosts) == 0 {
		hosts = defaultAllowedHosts
	}
	normalized := make([]string, 0, len(hosts))
	for _, h := range hosts {
		normalized = append(normalized, normalizeHost(h))
	}

	s := &Server{
		store:        cfg.Store,
		service:      cfg.Service,
		projectID:    cfg.ProjectID,
		allowedHosts: normalized,
		version:      cfg.Version,
		logger:       cfg.Logger,
	}
	s.handler = s.routes()
	return s, nil
}

// Handler returns the configured handler, which is what a test drives.
//
// serve does not call this: it mounts the same handler through HTTPServer,
// which attaches the timeouts a listener needs. Both read one field set once
// in New, so there is a single mux and no way for the two to drift apart.
func (s *Server) Handler() http.Handler { return s.handler }

// HTTPServer returns an http.Server carrying this handler and the
// timeouts a public-facing listener needs, so a caller cannot forget
// them.
func (s *Server) HTTPServer(addr string) *http.Server {
	if addr == "" {
		addr = DefaultAddr
	}
	return &http.Server{
		Addr:              addr,
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelWarn),
	}
}

// routes builds the mux and wraps it in the middleware every route needs.
//
// The order is load bearing. Host and Origin run before authentication so
// a rebound browser page never reaches the credential lookup at all, and
// the body cap runs before any handler reads.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Health is deliberately outside the authenticated group: it is what
	// a supervisor polls, and it reports nothing an unauthenticated
	// caller could not learn by connecting.
	mux.HandleFunc("GET /healthz", s.handleHealth)

	authed := http.NewServeMux()
	authed.HandleFunc("POST /api/runs", s.handleCreateRun)
	authed.HandleFunc("POST /api/runs/{run_id}/end", s.handleEndRun)
	authed.HandleFunc("GET /api/runs/{run_id}/evidence", s.handleRunEvidence)
	authed.HandleFunc("POST /api/runs/{run_id}/leases", s.handleAcquireLease)
	authed.HandleFunc("DELETE /api/leases/{lease_id}", s.handleReleaseLease)
	authed.HandleFunc("POST /api/leases/{lease_id}/claims", s.handleClaim)
	mux.Handle("/api/", s.authenticate(authed))

	return s.recoverPanic(s.checkHostAndOrigin(s.limitBody(mux)))
}

// limitBody caps every request body before a handler can read one.
func (s *Server) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// recoverPanic turns a handler panic into a 500 without a stack in the
// response.
//
// The stack goes to the log, where it belongs. A panic message can carry
// anything that was on the stack at the time, including a secret, so it
// never reaches the wire.
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.logger.Error("api: panic", "path", r.URL.Path, "value", v)
				writeError(w, s.logger, http.StatusInternalServerError, codeInternal,
					"the server failed to handle this request")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// normalizeHost lowercases a host and drops the shapes a Host header may
// carry, so the allowlist compares like with like.
func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimSuffix(h, ".")
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	return h
}
