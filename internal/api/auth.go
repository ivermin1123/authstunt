package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/ivermin1123/authstunt/internal/store"
)

// principalKind is which credential authenticated a request.
type principalKind int

const (
	// principalNone is an unauthenticated request. It is the zero value on
	// purpose: a handler that forgets to check has no scope at all, so a
	// missing check fails closed instead of granting everything.
	principalNone principalKind = iota
	// principalProject is the project bearer: it may create runs and read
	// the project's evidence.
	principalProject
	// principalRun is a run token: it may act on its own run and nothing
	// else.
	principalRun
)

// principal is the authenticated caller.
type principal struct {
	kind      principalKind
	projectID string
	// runID is set only for principalRun and is the single run this
	// caller may touch.
	runID string
}

// ownsRun reports whether this principal may act on the run given.
//
// A run token is confined to its own run. The project bearer may read any
// run of its project, and the project check is not a formality: the
// process serves one project, so a run id from another project must not
// resolve just because the caller holds a valid credential.
func (p principal) ownsRun(run store.Run) bool {
	if p.projectID == "" || run.ProjectID != p.projectID {
		return false
	}
	switch p.kind {
	case principalProject:
		return true
	case principalRun:
		return p.runID == run.ID
	default:
		return false
	}
}

type principalCtxKey struct{}

func withPrincipal(ctx context.Context, p principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// principalOf returns the caller. A request that never passed the
// authenticate middleware yields the zero principal, which owns nothing.
func principalOf(ctx context.Context) principal {
	p, _ := ctx.Value(principalCtxKey{}).(principal)
	return p
}

// bearerToken pulls the credential out of an Authorization header.
//
// Only the Bearer scheme is accepted, and the scheme match is
// case-insensitive because RFC 7235 says it is. A token in a query
// parameter is deliberately not supported: it would land in access logs
// and browser history.
func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	const scheme = "bearer "
	if len(header) <= len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return ""
	}
	return strings.TrimSpace(header[len(scheme):])
}

// authenticate resolves the credential into a principal.
//
// The prefix decides which lookup runs, so a run token is never tried
// against the project column or the reverse. A token matching neither is
// 401 with nothing else said: which of the two it looked like is not the
// caller's business.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeError(w, s.logger, http.StatusUnauthorized, codeUnauthorized,
				"this route needs a project bearer or a run token")
			return
		}

		var p principal
		switch {
		case strings.HasPrefix(token, store.RunTokenPrefix):
			run, err := s.store.RunByToken(r.Context(), token)
			if err != nil {
				s.rejectCredential(w, err)
				return
			}
			p = principal{kind: principalRun, projectID: run.ProjectID, runID: run.ID}
		case strings.HasPrefix(token, store.ProjectBearerPrefix):
			project, err := s.store.ProjectByBearer(r.Context(), token)
			if err != nil {
				s.rejectCredential(w, err)
				return
			}
			p = principal{kind: principalProject, projectID: project.ID}
		default:
			writeError(w, s.logger, http.StatusUnauthorized, codeUnauthorized, "unrecognized credential")
			return
		}

		// One more gate that costs nothing and closes a whole class of
		// mistake: this process serves exactly one project, so a
		// credential belonging to another one is refused here rather than
		// relied on to fail later in a handler.
		if p.projectID != s.projectID {
			writeError(w, s.logger, http.StatusUnauthorized, codeUnauthorized, "unrecognized credential")
			return
		}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
	})
}

// rejectCredential answers a failed lookup.
//
// A miss and a database error are told apart for the log, never for the
// caller: both get the same 401, because "that token does not exist" and
// "the lookup broke" are equally none of an unauthenticated caller's
// business.
func (s *Server) rejectCredential(w http.ResponseWriter, err error) {
	if !errors.Is(err, store.ErrNotFound) {
		s.logger.Error("api: credential lookup", "error", err)
	}
	writeError(w, s.logger, http.StatusUnauthorized, codeUnauthorized, "unrecognized credential")
}

// requireProject rejects anything but the project bearer.
func (s *Server) requireProject(w http.ResponseWriter, r *http.Request) (principal, bool) {
	p := principalOf(r.Context())
	if p.kind != principalProject {
		writeError(w, s.logger, http.StatusForbidden, codeForbidden,
			"this route needs the project bearer")
		return principal{}, false
	}
	return p, true
}

// checkHostAndOrigin enforces the Host allowlist and the Origin rule.
//
// Both exist for the same attacker: a page in the developer's browser
// that knows the loopback port. DNS rebinding defeats the loopback bind by
// resolving an attacker-controlled name to 127.0.0.1, which is why the
// Host header is checked against a list rather than trusted; and a
// cross-site fetch would otherwise carry no Origin restriction at all.
//
// A request with no Origin is allowed: that is every CLI, every Go test
// and every server-side caller. A request that carries one must match a
// permitted host, because only a browser sends it.
func (s *Server) checkHostAndOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			writeError(w, s.logger, http.StatusForbidden, codeHostRejected,
				"this Host is not allowed to reach this server")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(origin) {
			writeError(w, s.logger, http.StatusForbidden, codeOriginRejected,
				"cross-site requests are not accepted")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostAllowed matches the request Host against the allowlist.
//
// The port is dropped before comparing: a listener on a different port is
// a different server, and the bind address already decided that. An empty
// Host is refused rather than defaulted, because HTTP/1.1 requires one.
func (s *Server) hostAllowed(host string) bool {
	if host == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	// A bracketed IPv6 literal arrives with its brackets when there was no
	// port to split off.
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	for _, allowed := range s.allowedHosts {
		if host == allowed {
			return true
		}
	}
	return false
}

// originAllowed accepts an Origin whose host is on the same allowlist.
//
// An unparsable Origin, or the literal "null" that a sandboxed frame
// sends, matches nothing and is refused.
func (s *Server) originAllowed(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return s.hostAllowed(u.Host)
}
