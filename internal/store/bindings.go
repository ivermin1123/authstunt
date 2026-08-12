package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const bindingColumns = `message_id, lease_id, run_id, identity_id, addr, bound_at, suspect`

// BindRecipients resolves each envelope recipient to the lease that owned
// it when the message arrived, and records the answer.
//
// It runs inside the caller's transaction, which is the whole point: the
// binding commits with the message row, before the SMTP client is told
// 250. Storing the message first and attributing it afterwards would open
// a window in which a message exists with no owner, and a claim arriving
// inside that window would be answered by whoever asked first.
//
// The addresses that resolved to no lease come back as the second result.
// They are not an error: mail arriving in the gap between one run
// releasing an address and the next acquiring it genuinely has no owner,
// and the caller records that rather than guessing one.
//
// originAt is when the message says it was generated, and it is what
// decides suspicion on a reused address. Zero means the message carried
// no usable origination time, which a pooled binding treats as
// unprovable rather than as clean.
func (t *Tx) BindRecipients(ctx context.Context, messageID string, addrs []string, receivedAt, originAt time.Time) ([]Binding, []string, error) {
	var (
		bound   []Binding
		unbound []string
		seen    = make(map[string]struct{}, len(addrs))
	)
	for _, raw := range addrs {
		addr := NormalizeAddress(raw)
		if addr == "" {
			continue
		}
		// One envelope can list the same address twice. The primary key
		// would refuse the second insert; skipping it keeps a duplicated
		// recipient from reading as a failure.
		if _, repeat := seen[addr]; repeat {
			continue
		}
		seen[addr] = struct{}{}

		owner, err := t.leaseAt(ctx, addr, receivedAt)
		if errors.Is(err, ErrNotFound) {
			unbound = append(unbound, addr)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		binding := Binding{
			MessageID:  messageID,
			LeaseID:    owner.leaseID,
			RunID:      owner.runID,
			IdentityID: owner.identityID,
			Addr:       addr,
			BoundAt:    t.s.Now(),
			Suspect:    owner.suspicion(originAt),
		}
		if err := t.exec(ctx,
			`INSERT INTO message_bindings (`+bindingColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			binding.MessageID, binding.LeaseID, binding.RunID, binding.IdentityID,
			binding.Addr, timestamp(binding.BoundAt), binding.Suspect); err != nil {
			return nil, nil, fmt.Errorf("store: bind recipient: %w", err)
		}
		bound = append(bound, binding)
	}
	return bound, unbound, nil
}

// leaseOwner is the lease that owned an address at an instant, with the
// facts that decide whether the binding is suspect.
type leaseOwner struct {
	leaseID            string
	runID              string
	identityID         string
	mode               string
	acquiredAt         time.Time
	releasedAt         sql.NullString
	acquiredInCooldown bool
}

// originGranularity is the resolution the comparison against a lease can
// honestly use.
//
// A message states when it was generated to the second - RFC 5322 has no
// finer field - while a lease is acquired at nanosecond resolution.
// Comparing them raw would mark a run's own mail as older than its own
// lease whenever the acquire landed mid-second, which would break pooled
// mode for correct applications. The lease instant is therefore truncated
// down to the second the message could have named.
const originGranularity = time.Second

// suspicion classifies a binding against the lease it resolved to.
//
// originAt is when the message says it was generated. On a pooled address
// it is the signal a timer cannot provide: mail generated before this
// lease started belongs to whoever held the address earlier, no matter how
// late it arrived, and that is exactly the handover case a cooldown set
// shorter than real delivery latency lets through.
//
// The order is proven facts first, unprovable last. Cooldown outranks
// everything because a lease granted while the previous holder was still
// cooling down carries the doubt for its whole life. Predating the lease
// outranks after-release because it names a wrong-run risk, while
// after-release only says this run had already finished with the address.
//
// Ephemeral identities are not guarded: their address is minted for one
// run and never reused, so no earlier run's mail can reach it and an
// application that omits an origination time keeps working.
func (o leaseOwner) suspicion(originAt time.Time) string {
	switch {
	case o.acquiredInCooldown:
		return SuspectCooldown
	case o.mode != ModePooled:
		if o.releasedAt.Valid {
			return SuspectAfterRelease
		}
		return SuspectNone
	case originAt.IsZero():
		return SuspectOriginUnknown
	case originAt.Before(o.acquiredAt.Truncate(originGranularity)):
		return SuspectPredatesLease
	case o.releasedAt.Valid:
		return SuspectAfterRelease
	}
	return SuspectNone
}

// leaseAt is LeaseAt inside a transaction, returning only what binding
// needs. The half-open comparison is the same rule, spelled the same way:
// a message received exactly at the release instant belongs to nobody.
func (t *Tx) leaseAt(ctx context.Context, addr string, at time.Time) (leaseOwner, error) {
	var (
		o        leaseOwner
		acquired string
	)
	err := t.tx.QueryRowContext(ctx,
		`SELECT l.id, l.run_id, l.identity_id, i.mode, l.acquired_at,
		        l.released_at, l.acquired_in_cooldown
		 FROM leases l
		 JOIN identities i ON i.id = l.identity_id
		 WHERE i.addr = ?
		   AND l.acquired_at <= ?
		   AND (l.released_at IS NULL OR ? < l.released_at)
		 ORDER BY l.acquired_at DESC
		 LIMIT 1`,
		addr, timestamp(at), timestamp(at)).
		Scan(&o.leaseID, &o.runID, &o.identityID, &o.mode, &acquired,
			&o.releasedAt, &o.acquiredInCooldown)
	if errors.Is(err, sql.ErrNoRows) {
		return leaseOwner{}, ErrNotFound
	}
	if err != nil {
		return leaseOwner{}, fmt.Errorf("store: resolve owner: %w", err)
	}
	if o.acquiredAt, err = parseTimestamp(acquired); err != nil {
		return leaseOwner{}, err
	}
	return o, nil
}

// ListLeaseBindings returns everything bound to a lease, oldest first,
// including the suspect ones. Evidence shows what arrived; the claim path
// is what filters.
func (s *Store) ListLeaseBindings(ctx context.Context, leaseID string) ([]Binding, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT `+bindingColumns+` FROM message_bindings
		 WHERE lease_id = ? ORDER BY bound_at, message_id`, leaseID)
	if err != nil {
		return nil, fmt.Errorf("store: list bindings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Binding
	for rows.Next() {
		b, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list bindings: %w", err)
	}
	return out, nil
}

// ListMessageBindings returns every lease a message bound to. A message
// carrying two leased envelope recipients has two, which is why binding is
// a table.
func (s *Store) ListMessageBindings(ctx context.Context, messageID string) ([]Binding, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT `+bindingColumns+` FROM message_bindings
		 WHERE message_id = ? ORDER BY lease_id`, messageID)
	if err != nil {
		return nil, fmt.Errorf("store: message bindings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Binding
	for rows.Next() {
		b, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: message bindings: %w", err)
	}
	return out, nil
}

func scanBinding(row rowScanner) (Binding, error) {
	var (
		b     Binding
		bound string
	)
	if err := row.Scan(&b.MessageID, &b.LeaseID, &b.RunID, &b.IdentityID,
		&b.Addr, &bound, &b.Suspect); err != nil {
		return Binding{}, fmt.Errorf("store: scan binding: %w", err)
	}
	var err error
	if b.BoundAt, err = parseTimestamp(bound); err != nil {
		return Binding{}, err
	}
	return b, nil
}
