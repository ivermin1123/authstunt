package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
)

// Protocol revisions this server implements, newest first.
//
// The list is short on purpose: a version is listed here only after its
// specification was read, because claiming one and then guessing at its
// wire is the failure this package exists to argue against.
//
// 2026-07-28 is the modern revision: no handshake, the protocol version
// travels in each request's _meta, and server/discover is mandatory.
// 2025-11-25 and 2025-06-18 are legacy: an initialize handshake opens the
// session and every later message inherits what it negotiated.
var supportedVersions = []string{"2026-07-28", "2025-11-25", "2025-06-18"}

// latestLegacyVersion is what an initialize handshake falls back to when
// the client asked for a version this server does not implement. The
// legacy rule is to answer with a version the server does support and let
// the client decide whether it can live with it.
const latestLegacyVersion = "2025-11-25"

// metaProtocolVersion is the _meta key a modern request declares its
// protocol version in.
const metaProtocolVersion = "io.modelcontextprotocol/protocolVersion"

// metaServerInfo is where a DiscoverResult carries the server identity.
const metaServerInfo = "io.modelcontextprotocol/serverInfo"

// serverName is what this server calls itself. Clients that prefix tool
// names with the server name produce mcp__authstunt__open_run from it.
const serverName = "authstunt"

// Config builds a Server.
type Config struct {
	// BaseURL is the origin of a running AuthStunt server. This process
	// never starts one: the application under test has to be able to
	// send mail to it, which means it outlives any agent session.
	BaseURL string
	// Bearer is the project bearer, read from the environment by the
	// caller. It authorizes exactly one route, F1, and never appears in
	// a tool result, an error message or a log line.
	Bearer string
	// Version is reported as the server version.
	Version string
	// HTTPClient is optional. The default has no client-level timeout
	// because a claim's deadline is set per request and can be two
	// minutes long; a blanket timeout here would cut a correct long-poll
	// short.
	HTTPClient *http.Client
	Logger     *slog.Logger
}

// Server is the MCP surface: four tools over a stdio JSON-RPC stream,
// proxied to a running AuthStunt server.
type Server struct {
	proxy   *proxy
	version string
	logger  *slog.Logger
}

// New validates the configuration and builds the server.
//
// A missing credential is refused here, at startup, rather than turned
// into a 401 on the first tool call: an agent that has already told a
// user "let me sign up" and then hits an authentication error tends to
// improvise, and the thing it would improvise around is a configuration
// mistake only a human can fix.
func New(cfg Config) (*Server, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("mcp: the AuthStunt server URL is required (AUTHSTUNT_URL)")
	}
	if strings.TrimSpace(cfg.Bearer) == "" {
		return nil, errors.New("mcp: the project bearer is required (AUTHSTUNT_BEARER); its value is never printed")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	return &Server{
		proxy:   newProxy(cfg.BaseURL, cfg.Bearer, client),
		version: cfg.Version,
		logger:  cfg.Logger,
	}, nil
}

// Serve runs the session until the peer closes its side or ctx is done.
//
// Each request is handled on its own goroutine and responses are
// serialized by the writer. That is not throughput for its own sake: a
// claim can park for two minutes, and a client that sends a keepalive
// ping in the meantime would otherwise get no answer until the claim
// returned, and would conclude the server had hung.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)

	var wg sync.WaitGroup
	// Deferred calls run last in, first out, and the order matters:
	// cancel runs first so a claim parked on a long-poll unwinds, then
	// the wait lets the handlers finish. Waiting first would hold the
	// session open for up to two minutes after the client had already
	// gone away.
	defer wg.Wait()
	defer cancel()

	w := newWriter(out)
	lines, readErr := readLoop(ctx, in)

	for {
		var line []byte
		select {
		case <-ctx.Done():
			// A signal is an ordinary way for a stdio session to end,
			// so it is not reported as a failure.
			return nil
		case next, open := <-lines:
			if !open {
				select {
				case err := <-readErr:
					if errors.Is(err, io.EOF) {
						return nil
					}
					return err
				default:
					return nil
				}
			}
			line = next
		}

		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			// A parse failure has no id to answer under, which is the
			// one case JSON-RPC answers with a null id.
			_ = w.write(newError(json.RawMessage("null"), codeParseError, "the message is not valid JSON", nil))
			continue
		}
		if req.JSONRPC != "2.0" || req.Method == "" {
			if !req.isNotification() {
				_ = w.write(newError(req.ID, codeInvalidRequest, "a JSON-RPC 2.0 request needs jsonrpc and method", nil))
			}
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, answer := s.handle(ctx, req)
			if !answer {
				return
			}
			if err := w.write(resp); err != nil {
				s.logger.Error("mcp: write response", "method", req.Method, "error", err)
			}
		}()
	}
}

// readLoop pumps messages off the input stream so the session can also
// end on a signal.
//
// A read from stdin does not unblock when a context is canceled, so the
// read has to happen somewhere the main loop is not waiting. On
// cancellation this goroutine stays parked in that read until the process
// exits, which is the right trade for a process whose whole lifetime is
// one session: the alternative is a half-closed stdin the client did not
// ask for.
func readLoop(ctx context.Context, in io.Reader) (<-chan []byte, <-chan error) {
	lines := make(chan []byte)
	failed := make(chan error, 1)
	go func() {
		defer close(lines)
		r := newReader(in)
		for {
			line, err := r.next()
			if err != nil {
				failed <- err
				return
			}
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	return lines, failed
}

// handle dispatches one message. The second return reports whether
// anything should be written, which is false for every notification.
func (s *Server) handle(ctx context.Context, req request) (response, bool) {
	// A notification is answered with silence even when it is a method
	// this server does not know. Replying would put an unmatched
	// response on the wire.
	if req.isNotification() {
		return response{}, false
	}

	requested, modern := declaredVersion(req.Params)
	if modern && !supports(requested) {
		return newError(req.ID, codeUnsupportedProtocolVersion, "Unsupported protocol version", map[string]any{
			"supported": supportedVersions,
			"requested": requested,
		}), true
	}

	switch req.Method {
	case "initialize":
		return newResult(req.ID, s.initializeResult(req.Params)), true
	case "server/discover":
		return newResult(req.ID, s.discoverResult()), true
	case "ping":
		return newResult(req.ID, struct{}{}), true
	case "tools/list":
		result := map[string]any{"tools": tools()}
		if modern {
			result["resultType"] = "complete"
		}
		return newResult(req.ID, result), true
	case "tools/call":
		return s.callTool(ctx, req, modern), true
	default:
		return newError(req.ID, codeMethodNotFound, fmt.Sprintf("unknown method: %s", req.Method), nil), true
	}
}

// declaredVersion reads the modern per-request protocol version. The
// second return reports whether the request declared one at all, which is
// what tells a modern caller from a legacy one.
func declaredVersion(params json.RawMessage) (string, bool) {
	if len(params) == 0 {
		return "", false
	}
	var envelope struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return "", false
	}
	raw, ok := envelope.Meta[metaProtocolVersion]
	if !ok {
		return "", false
	}
	var version string
	if err := json.Unmarshal(raw, &version); err != nil {
		return "", false
	}
	return version, true
}

func supports(version string) bool {
	for _, v := range supportedVersions {
		if v == version {
			return true
		}
	}
	return false
}

// initializeResult answers the legacy handshake.
//
// The negotiation rule of those revisions is: echo the client's version
// when it is supported, otherwise name one that is. There is no error
// path - a client that cannot live with the answer disconnects.
func (s *Server) initializeResult(params json.RawMessage) map[string]any {
	var body struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &body)
	negotiated := latestLegacyVersion
	if supports(body.ProtocolVersion) && body.ProtocolVersion != supportedVersions[0] {
		// The modern revision has no initialize, so a client asking for
		// it through this door is answered with the newest legacy one.
		negotiated = body.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": negotiated,
		"capabilities":    s.capabilities(),
		"serverInfo":      s.serverInfo(),
		"instructions":    instructions,
	}
}

func (s *Server) discoverResult() map[string]any {
	return map[string]any{
		"resultType":        "complete",
		"supportedVersions": supportedVersions,
		"capabilities":      s.capabilities(),
		"instructions":      instructions,
		"_meta":             map[string]any{metaServerInfo: s.serverInfo()},
	}
}

// capabilities declares tools and nothing else.
//
// No prompts and no resources, and that is a decision rather than an
// omission: a prompt is text a model interprets, and a resource exposing
// a mailbox would invite the model to read mail and pick a code out of it
// by eye - which is the guessing this whole surface replaces.
func (s *Server) capabilities() map[string]any {
	return map[string]any{"tools": map[string]any{}}
}

func (s *Server) serverInfo() map[string]any {
	return map[string]any{"name": serverName, "version": s.version}
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// callTool runs one tool.
func (s *Server) callTool(ctx context.Context, req request, modern bool) response {
	var params callParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return newError(req.ID, codeInvalidParams, "tools/call needs a name and arguments", nil)
	}

	result, rpcErr := s.invoke(ctx, params)
	if rpcErr != nil {
		return response{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}
	}
	s.logger.Info("mcp: tool call", "tool", params.Name, "status", result.status)
	return newResult(req.ID, toolResult(result, modern))
}

// invoke decodes the arguments for one tool and makes the call.
//
// A malformed call - unknown tool, missing field, wrong type - is a
// protocol error, because there is nothing the server could answer and
// nothing a model learns from a round trip. Everything the AuthStunt
// server did answer, including a refusal, comes back as a result.
func (s *Server) invoke(ctx context.Context, params callParams) (httpResult, *rpcError) {
	switch params.Name {
	case toolOpenRun:
		var args struct{}
		if err := decodeArgs(params.Arguments, &args); err != nil {
			return httpResult{}, err
		}
		return s.proxied(s.proxy.openRun(ctx))

	case toolLeaseIdentity:
		var args struct {
			RunID string `json:"run_id"`
			Role  string `json:"role"`
		}
		if err := decodeArgs(params.Arguments, &args); err != nil {
			return httpResult{}, err
		}
		if args.RunID == "" || args.Role == "" {
			return httpResult{}, &rpcError{Code: codeInvalidParams, Message: "run_id and role are required"}
		}
		return s.proxied(s.proxy.leaseIdentity(ctx, args.RunID, args.Role))

	case toolClaimCode:
		var args struct {
			LeaseID   string `json:"lease_id"`
			Kind      string `json:"kind"`
			TimeoutMS *int64 `json:"timeout_ms"`
			Attempt   *int64 `json:"attempt"`
		}
		if err := decodeArgs(params.Arguments, &args); err != nil {
			return httpResult{}, err
		}
		if args.LeaseID == "" || args.Kind == "" {
			return httpResult{}, &rpcError{Code: codeInvalidParams, Message: "lease_id and kind are required"}
		}
		// kind is deliberately not checked here. The enum in the schema
		// is what stops a model reaching for a kind that does not exist,
		// and anything that gets past it is the server's answer to give,
		// verbatim, including the 501 that refuses totp.
		timeout := int64(defaultClaimTimeoutMS)
		if args.TimeoutMS != nil {
			timeout = *args.TimeoutMS
		}
		if timeout < 0 || timeout > maxClaimTimeoutMS {
			return httpResult{}, &rpcError{Code: codeInvalidParams, Message: "timeout_ms must be between 0 and 120000"}
		}
		attempt := int64(1)
		if args.Attempt != nil {
			attempt = *args.Attempt
		}
		if attempt < 1 {
			return httpResult{}, &rpcError{Code: codeInvalidParams, Message: "attempt starts at 1"}
		}
		return s.proxied(s.proxy.claimCode(ctx, args.LeaseID, args.Kind, timeout, attempt))

	case toolReleaseLease:
		var args struct {
			LeaseID string `json:"lease_id"`
		}
		if err := decodeArgs(params.Arguments, &args); err != nil {
			return httpResult{}, err
		}
		if args.LeaseID == "" {
			return httpResult{}, &rpcError{Code: codeInvalidParams, Message: "lease_id is required"}
		}
		return s.proxied(s.proxy.releaseLease(ctx, args.LeaseID))

	default:
		return httpResult{}, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("unknown tool: %s", params.Name)}
	}
}

// maxClaimTimeoutMS mirrors the cap F3 enforces.
const maxClaimTimeoutMS = 120000

// proxied turns a transport failure into a tool result.
//
// A server that could not be reached is not a protocol error: the call
// was well formed, and telling the model plainly is what lets it stop
// rather than retry a URL that is not there.
func (s *Server) proxied(result httpResult, err error) (httpResult, *rpcError) {
	if err == nil {
		return result, nil
	}
	s.logger.Error("mcp: call the AuthStunt server", "error", err)
	return httpResult{status: 0, body: []byte(err.Error())}, nil
}

// decodeArgs reads a tool's arguments, refusing anything not in its
// schema.
//
// Unknown fields are rejected rather than ignored for the same reason the
// HTTP surface rejects them: a model that invented a field believes it
// had an effect, and silence would confirm that belief.
func decodeArgs(raw json.RawMessage, v any) *rpcError {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &rpcError{Code: codeInvalidParams, Message: "the arguments do not match this tool's schema"}
	}
	return nil
}

// textContent is one entry of a tool result's content array.
type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolResult maps one HTTP answer onto a tool result.
//
// Two rules, and there is deliberately no third:
//
//   - A 2xx is a success, whatever it says. claim_timeout,
//     claim_suspect_binding and claim_already_claimed are answers, not
//     failures, and flagging them as errors would make most clients
//     flatten them into an error string - at which point the model does
//     what models do with errors, which is retry blindly or invent an
//     explanation. The reason code has to arrive as data for the branch
//     to be a function of the code.
//   - A 4xx or 5xx is an error carrying the server's own envelope
//     verbatim. This package has no error vocabulary of its own: it never
//     renames a code, never merges two, and never rewrites a message.
//
// A 204 carries no body and so carries no content. Releasing a lease
// twice lands here too, which is why the tool description says plainly
// that an empty result is success.
func toolResult(result httpResult, modern bool) map[string]any {
	content := []textContent{}
	if len(result.body) > 0 {
		content = append(content, textContent{Type: "text", Text: string(result.body)})
	}
	out := map[string]any{
		"content": content,
		"isError": !result.ok(),
	}
	if modern {
		out["resultType"] = "complete"
	}
	return out
}
