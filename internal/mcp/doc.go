// Package mcp serves the four frozen routes to an agent as Model Context
// Protocol tools, over stdio, as the `authstunt mcp` subcommand.
//
// # What replaced the earlier premise
//
// This package used to say MCP would ship "only if an agent workflow is
// recorded as having failed on the REST, CLI and Playwright paths first",
// and to promise both a streamable HTTP endpoint at /mcp and a stdio
// proxy. That premise is gone, deliberately and on the record.
//
// It rested on MCP being an adapter with no goal of its own. What changed
// is not the adapter but what the missing piece turned out to be: every
// agent that meets a signup flow today either invents a code, or asks a
// language model to read an inbox and pick one out by eye. That is a
// judgment call in the middle of a test, and it is the exact thing a
// lease and a reason code remove. Waiting for the other paths to fail
// first would have been waiting for evidence of a gap already visible in
// every competing tool.
//
// The half of the old note that survived is the half about scope. What
// ships here is thin, and the promise of a second transport is not part
// of it; see the transport section below.
//
// # Four tools, one per frozen route
//
//	open_run        F1  POST   /api/v1/runs
//	lease_identity  F2  POST   /api/v1/runs/{run_id}/leases
//	claim_code      F3  POST   /api/v1/leases/{lease_id}/claims
//	release_lease   F4  DELETE /api/v1/leases/{lease_id}
//
// One tool per route, and no convenience tool that runs several at once.
// A combined signup tool would have to decide, on the caller's behalf,
// how long to wait, how many times to retry, and whether a claim that
// timed out is news or a failure - and when a middle step refused, its
// reason code would be flattened into one sentence from the outermost
// tool. That reason code is the product.
//
// Nothing outside the freeze gets a tool. The provisional routes, pooled
// mode, the totp kind and flow assertions are all absent, and the totp
// refusal is expressed as an enum in the claim schema rather than as a
// sentence in a description: a value that is not in the schema is one a
// model cannot pick.
//
// # A proxy, not an embedded server
//
// This process speaks HTTP to an AuthStunt server that is already
// running, and never starts one. The application under test has to send
// mail to that server, so it has to exist before the agent session does
// and outlive it - a server started inside this process would be one no
// application could be pointed at. The cost is real and is not hidden:
// running this means running two things.
//
// stdio is the only transport. It is the configuration shape every client
// supports, it carries credentials through the process environment rather
// than through a per-client header convention, and it has no listener, no
// port and no session of its own to design and freeze. A streamable HTTP
// endpoint is deferred, not planned: it becomes the right answer when an
// agent and its server sit on opposite sides of a container boundary.
//
// # What the model never sees
//
// The project bearer is read from the environment at startup and
// authorizes exactly one route, F1. The run token minted by F1 is lifted
// out of the response, kept in memory keyed by run id, and used for
// F2 to F4 - so it never enters a transcript, and a mistake in this
// process reaches one run and stops at a 404 instead of touching another
// run of the same project.
//
// This matters more here than in an ordinary client, because this agent
// reads text that somebody else wrote: the body of an email sent by the
// application under test. A tool that accepted a credential as a
// parameter would be a tool a malicious message could ask the model to
// call. A credential that was never in the context window has nothing to
// offer an injection.
//
// The claimed value itself does travel, and saying otherwise would be
// overselling: the point of the system is that the agent types the code
// into the application. It is a one-time secret, scoped to one lease, and
// spent at the moment it is handed over. Long-lived credentials are the
// ones that never appear.
//
// # Protocol revisions
//
// Both eras of the protocol are served. The modern revision (2026-07-28)
// carries the version in each request's _meta and answers server/discover;
// the legacy revisions (2025-11-25, 2025-06-18) open with an initialize
// handshake. The wire is implemented here rather than taken from the
// official SDK, which would have added eight modules to serve, in the
// main, the parts of the protocol this package refuses on purpose.
//
// # Experimental
//
// Tool names and input shapes are not part of the /api/v1 freeze and may
// change. Result bodies are, because they are the HTTP bodies. The names
// freeze once a real agent has been recorded completing a signup through
// them, and not before: freezing is a promise, and there is no evidence
// yet to make it against.
package mcp
