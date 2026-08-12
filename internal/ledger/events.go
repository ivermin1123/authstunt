// Package ledger defines the typed events the audit trail can carry.
//
// The audit trail is the product's evidence, and evidence that quietly
// grew a field carrying an OTP would be worse than no evidence at all. So
// the shape of every event is a Go type here rather than a map built at a
// call site: there is no field to put a secret in, and adding one means
// editing this file and its test, where somebody will notice.
//
// Two mechanisms do the work. Event is a sealed interface - its only
// method is unexported - so no other package can invent an event, and the
// set below is the whole vocabulary. Addr redacts itself when it is
// marshaled, so a call site cannot forget to truncate a local part; the
// redaction is a property of the type, not a discipline.
//
// What is deliberately absent: any map, any `any`, any DetailJSON escape
// hatch. A generic payload would preserve exactly the leak class this
// package exists to close, while looking typed.
package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ivermin1123/authstunt/internal/store"
)

// Actions the ledger records. They are the stored `action` column and are
// part of what an operator greps for, so they are constants rather than
// literals at call sites.
const (
	ActionMailReceived    = "mail.received"
	ActionMailQuarantined = "mail.quarantined"
	ActionExtractionFail  = "extraction.failed"
	ActionLeaseAcquired   = "lease.acquired"
	ActionLeaseReleased   = "lease.released"
	ActionSeedSettled     = "lease.seed_settled"
)

// Addr is an email address in evidence.
//
// It marshals redacted, always. Evidence has to be enough to debug a
// failure and not enough to be a mailing list: the domain is the
// project's own and carries nothing, while a local part identifies a
// person exactly when the address is a real one that should never have
// reached this tool.
type Addr string

// MarshalJSON writes the truncated form.
func (a Addr) MarshalJSON() ([]byte, error) { return json.Marshal(a.Redacted()) }

// Redacted returns the form that reaches evidence.
func (a Addr) Redacted() string {
	addr := string(a)
	at := strings.LastIndex(addr, "@")
	if at <= 0 {
		return "[redacted]"
	}
	local, domain := addr[:at], addr[at+1:]
	const keep = 3
	if len(local) > keep {
		local = local[:keep] + "..."
	}
	return local + "@" + domain
}

// Addrs is a list of addresses, redacted the same way.
type Addrs []string

// MarshalJSON writes each address redacted.
func (a Addrs) MarshalJSON() ([]byte, error) {
	out := make([]string, len(a))
	for i, addr := range a {
		out[i] = Addr(addr).Redacted()
	}
	return json.Marshal(out)
}

// Event is one thing worth recording.
//
// The interface is sealed by its unexported method: only this package can
// implement it, so the events below are the complete vocabulary and a
// caller cannot pass a shape nobody reviewed.
type Event interface {
	// Action is the stored action column.
	Action() string
	// sealed keeps the interface closed.
	sealed()
}

// MailReceived records an accepted message.
//
// EnvelopeFrom is redacted like every other address. It is usually an
// application's bounce address and carries nothing, but "usually" is not
// a property the type can rely on: a misconfigured sender puts a real
// person there, and that is precisely the case worth not printing.
type MailReceived struct {
	MessageID    string `json:"message_id"`
	EnvelopeFrom Addr   `json:"envelope_from"`
}

// Action names this event in the stored trail.
func (MailReceived) Action() string { return ActionMailReceived }

// sealed keeps MailReceived inside this package.
func (MailReceived) sealed() {}

// MailQuarantined records a message held back, and which recipients
// caused it.
type MailQuarantined struct {
	MessageID  string `json:"message_id"`
	Recipients Addrs  `json:"recipients"`
}

// Action names this event in the stored trail.
func (MailQuarantined) Action() string { return ActionMailQuarantined }

// sealed keeps MailQuarantined inside this package.
func (MailQuarantined) sealed() {}

// ExtractionFailed records a message that reached a terminal failed
// extraction.
//
// Reason is a message this codebase generates, never a payload from the
// mail: an extractor's error says what it could not parse, not what it
// parsed. That is a constraint on the call sites, and it is why the field
// is named Reason rather than Error - there is nothing here to wrap.
type ExtractionFailed struct {
	MessageID string `json:"message_id"`
	Reason    string `json:"reason"`
}

// Action names this event in the stored trail.
func (ExtractionFailed) Action() string { return ActionExtractionFail }

// sealed keeps ExtractionFailed inside this package.
func (ExtractionFailed) sealed() {}

// LeaseAcquired records a run taking exclusive ownership of an identity.
type LeaseAcquired struct {
	LeaseID    string `json:"lease_id"`
	IdentityID string `json:"identity_id"`
	Role       string `json:"role"`
	Mode       string `json:"mode"`
	Addr       Addr   `json:"addr"`
}

// Action names this event in the stored trail.
func (LeaseAcquired) Action() string { return ActionLeaseAcquired }

// sealed keeps LeaseAcquired inside this package.
func (LeaseAcquired) sealed() {}

// LeaseReleased records an identity going back.
type LeaseReleased struct {
	LeaseID    string `json:"lease_id"`
	IdentityID string `json:"identity_id"`
	Addr       Addr   `json:"addr"`
}

// Action names this event in the stored trail.
func (LeaseReleased) Action() string { return ActionLeaseReleased }

// sealed keeps LeaseReleased inside this package.
func (LeaseReleased) sealed() {}

// SeedSettled records the outcome of the seed adapter.
//
// Fingerprint is the application's own opaque marker for the state it
// created. It is recorded because it is the only thing that makes two
// seeded runs distinguishable after the fact; an application that puts a
// secret in its fingerprint is publishing that secret to its own caller,
// which no schema here can prevent.
type SeedSettled struct {
	LeaseID     string `json:"lease_id"`
	IdentityID  string `json:"identity_id"`
	SeedState   string `json:"seed_state"`
	Addr        Addr   `json:"addr"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// Action names this event in the stored trail.
func (SeedSettled) Action() string { return ActionSeedSettled }

// sealed keeps SeedSettled inside this package.
func (SeedSettled) sealed() {}

// Meta is the context every event shares: which project, which run, which
// persona, and who caused it.
type Meta struct {
	ProjectID string
	RunID     string
	PersonaID string
	// Actor defaults to store.ActorSystem, which is what writes every
	// event this package currently defines.
	Actor string
}

// appender is satisfied by both *store.Store and *store.Tx, so an event
// can be written on its own or inside the transaction that made the
// change it describes. The second is the normal case.
type appender interface {
	AppendLedger(ctx context.Context, e store.LedgerEntry) (int64, error)
}

// Write records one event and returns its ledger id.
//
// It is the only way this package writes, and it builds the DetailJSON
// itself. A caller never sees that column, which is what keeps the typed
// schema from being one `DetailJSON:` assignment away from irrelevant.
func Write(ctx context.Context, to appender, meta Meta, ev Event) (int64, error) {
	if meta.Actor == "" {
		meta.Actor = store.ActorSystem
	}
	detail, err := json.Marshal(ev)
	if err != nil {
		// Every event above is a flat struct of strings, so this is not a
		// reachable branch; reporting it is still cheaper than writing an
		// entry whose detail silently became "{}".
		return 0, fmt.Errorf("ledger: encode %s: %w", ev.Action(), err)
	}
	return to.AppendLedger(ctx, store.LedgerEntry{
		ProjectID:  meta.ProjectID,
		Actor:      meta.Actor,
		RunID:      meta.RunID,
		PersonaID:  meta.PersonaID,
		Action:     ev.Action(),
		DetailJSON: string(detail),
	})
}
