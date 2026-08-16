package main

import (
	"context"
	"flag"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ivermin1123/authstunt/internal/api"
	"github.com/ivermin1123/authstunt/internal/mcp"
)

// mcpUsage is printed by `authstunt mcp --help`.
//
// It names the two environment variables and says outright that the
// bearer's value is never printed, because the first thing anyone does
// with a credential problem is look for somewhere it might have been
// echoed.
const mcpUsage = `authstunt mcp - serve the frozen routes to an agent over stdio

Usage:
  authstunt mcp

Speaks the Model Context Protocol on stdin and stdout. It does not start a
server: point it at one that is already running, because the application
under test has to be able to send mail there.

Environment:
  AUTHSTUNT_URL     origin of a running AuthStunt server (default http://` + api.DefaultAddr + `)
  AUTHSTUNT_BEARER  the project bearer, from "authstunt project bearer provision".
                    Required. Its value is never printed, here or anywhere else.

Tool names and input shapes are experimental and may change. Result bodies
are the frozen /api/v1 bodies and will not.
`

// runMCP is the mcp subcommand.
//
// stdout belongs to the protocol: the transport requires that nothing but
// MCP messages go there, so every log line and every diagnostic goes to
// stderr.
func runMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = io.WriteString(stderr, mcpUsage) }
	if err := fs.Parse(args); err != nil {
		return err
	}

	baseURL := os.Getenv("AUTHSTUNT_URL")
	if baseURL == "" {
		baseURL = "http://" + api.DefaultAddr
	}

	server, err := mcp.New(mcp.Config{
		BaseURL: baseURL,
		Bearer:  os.Getenv("AUTHSTUNT_BEARER"),
		Version: version,
		Logger:  slog.New(slog.NewTextHandler(stderr, nil)),
	})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Serve(ctx, stdin, stdout)
}
