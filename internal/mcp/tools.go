package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ivermin1123/authstunt/internal/store"
)

// Tool names. They map one to one onto the four frozen routes and are
// deliberately not named after user stories: a story encodes an
// intention, and what this server sells is a contract.
const (
	toolOpenRun       = "open_run"
	toolLeaseIdentity = "lease_identity"
	toolClaimCode     = "claim_code"
	toolReleaseLease  = "release_lease"
)

// toolDef is one entry of tools/list.
type toolDef struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// reasonAction is one row of the table a tool description carries.
//
// The table is the whole point of this server. A reason code that reaches
// the model as prose is a code the model has to interpret; a code paired
// with the action it implies is a branch the model can take without
// judgment. Keeping the rows as data rather than as a paragraph is what
// lets a test check them against the reason codes the server can
// actually emit.
type reasonAction struct {
	code   string
	action string
}

// claimReasons is every reason code a 200 from F3 can carry, with what to
// do about it.
//
// Checked against package store by TestClaimDescriptionCoversEveryReason:
// a reason code added to the server without a row here fails that test
// rather than reaching a model as an unexplained string.
var claimReasons = []reasonAction{
	{store.ReasonOK, "the secret is in `value`. Type it into the application."},
	{store.ReasonTimeout, "a message bound to this lease but has not settled yet. Call again with the SAME attempt and a larger timeout_ms. Nothing is wrong."},
	{store.ReasonNoBinding, "no message was ever addressed to this lease. Check the application really sent to `addr`, and that the send happened after the lease."},
	{store.ReasonStaleFiltered, "a message exists but predates this run. It belongs to an earlier run and will never become claimable here. Do not wait for it."},
	{store.ReasonSuspectBinding, "a message bound to this lease cannot be proven to be this run's. Stop and report it: this is evidence, not a transient fault."},
	{store.ReasonAlreadyClaimed, "the only candidate was already handed to an earlier claim. Calling again will not produce it. Have the application send a new message and raise `attempt`."},
	{store.ReasonExpired, "this attempt's claim outlived its own TTL. Raise `attempt` and have the application send again."},
	{store.ReasonExtractionFail, "a message arrived but no code or link could be read out of it. The message shape is the problem; retrying will not change it."},
	{store.ReasonLeaseNotHeld, "the lease was released or expired. Call lease_identity for a new one."},
	{store.ReasonRunNotActive, "the run ended or expired. Call open_run, then lease_identity."},
}

// leaseErrors is the error envelope vocabulary F2 answers with.
var leaseErrors = []reasonAction{
	{"not_found", "no such run, or this server holds no credential for it. Call open_run again."},
	{store.ReasonRunNotActive, "the run ended or expired. Call open_run again."},
	{store.ReasonLeaseSeedFailed, "the application refused to prepare this identity. The seeding endpoint is the problem; retrying will not help."},
}

// releaseErrors is the error envelope vocabulary F4 answers with.
var releaseErrors = []reasonAction{
	{"not_found", "no such lease, or this server holds no credential for it. If it was never leased in this session there is nothing to release."},
}

// table renders rows for a tool description, aligned so the codes read as
// a column rather than as prose a model has to parse.
func table(rows []reasonAction) string {
	width := 0
	for _, row := range rows {
		if len(row.code) > width {
			width = len(row.code)
		}
	}
	var b strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, row.code, row.action)
	}
	return strings.TrimRight(b.String(), "\n")
}

// tools is the tool list, built once.
//
// The order is fixed and is the order a session uses them in. tools/list
// is required to be deterministic, and a caller reading the list top to
// bottom is reading the lifecycle.
func tools() []toolDef {
	return []toolDef{
		{
			Name:  toolOpenRun,
			Title: "Open a run",
			Description: `Open a run. A run is the scope every identity, message and claim belongs to,
and it is the first call of a session: lease_identity needs the run_id this
returns.

Takes no parameters. Returns run_id, checkpoint_at and expires_at.
checkpoint_at is the instant before which mail is not claimable by this run,
which is how a leftover message from an earlier run cannot be mistaken for
this one's.

The run's credential is held inside this server and never appears in a result.`,
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		},
		{
			Name:  toolLeaseIdentity,
			Title: "Lease an identity",
			Description: `Lease one identity for this run and get the email address to type into the
application under test.

The address is yours until you release it. No other run is handed the same
one, and mail arriving at it is bound to this lease, which is what makes the
claim below unambiguous rather than a search of a shared mailbox.

Returns lease_id, addr, identity_id, role, mode, seed_state, pooled_policy and
expires_at.

Errors carry the server's code verbatim:
` + table(leaseErrors) + `

Identities are always ephemeral. Pooled mode is outside the frozen surface and
this tool cannot ask for it.`,
			InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "run_id": {"type": "string", "description": "The run_id returned by open_run."},
    "role": {"type": "string", "description": "A label for what this identity is for in your test, for example \"signup\" or \"invitee\". Free-form; it appears in the evidence ledger."}
  },
  "required": ["run_id", "role"],
  "additionalProperties": false
}`),
		},
		{
			Name:  toolClaimCode,
			Title: "Claim a code or magic link",
			Description: `Claim the secret delivered to a leased identity. It covers BOTH kinds: a one
time code (email_otp) and a sign-in link (magic_link).

The call waits. It returns as soon as a message bound to this lease is
claimable, or when timeout_ms runs out - so there is never a reason to poll or
to sleep between calls.

Every outcome carries a ` + "`reason`" + `, including the ones that are not failures.
Branch on it:
` + table(claimReasons) + `

A reason is a result, not an error. Only a refused request - an unknown lease,
a malformed call - comes back as an error.

There is no idempotency_key parameter. The key is derived from the lease, the
kind and ` + "`attempt`" + `, so calling this tool again with the SAME attempt replays
the same answer rather than consuming a second message. That is what makes a
retry safe.

Raise ` + "`attempt`" + ` only when you want a NEW message, after asking the
application to send one - the resend case. Raising it without a resend just
waits for a message that is not coming.`,
			InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "lease_id": {"type": "string", "description": "The lease_id returned by lease_identity."},
    "kind": {
      "type": "string",
      "enum": ["email_otp", "magic_link"],
      "description": "What to read out of the message: a one time code, or a sign-in link."
    },
    "timeout_ms": {
      "type": "integer",
      "minimum": 0,
      "maximum": 120000,
      "description": "How long to wait for a claimable message. Defaults to 30000."
    },
    "attempt": {
      "type": "integer",
      "minimum": 1,
      "description": "Which message of this lease and kind you are asking for. Defaults to 1. Raise it only after the application has sent a new message, for example after pressing a resend button."
    }
  },
  "required": ["lease_id", "kind"],
  "additionalProperties": false
}`),
		},
		{
			Name:  toolReleaseLease,
			Title: "Release a lease",
			Description: `Give a leased identity back. Call it when you are done with the identity,
including when the test failed: a lease nobody released stays held until the
run expires.

Returns no content on success. Releasing the same lease twice is not an error.

Errors carry the server's code verbatim:
` + table(releaseErrors),
			InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "lease_id": {"type": "string", "description": "The lease_id returned by lease_identity."}
  },
  "required": ["lease_id"],
  "additionalProperties": false
}`),
		},
	}
}

// instructions is the one paragraph a client may show a model before it
// has read any tool description.
//
// It is not a prompt primitive - this server declares no prompts - and it
// says nothing a tool description does not repeat, because a client is
// free to ignore it.
const instructions = `AuthStunt hands out throwaway email identities and the codes sent to them, so a
test never has to read a mailbox or guess.

The order is always: open_run, then lease_identity for an address, then
claim_code once the application has sent something to that address, then
release_lease. Every claim outcome carries a reason code; branch on the code
rather than on whether a value came back.`
