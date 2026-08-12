package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ncruces/go-sqlite3"
)

// isUniqueViolation reports whether an insert lost a race to a unique
// index.
//
// It reads the driver's extended result code rather than matching the
// error text: the message is a human-facing string that upstream is free
// to reword, while CONSTRAINT_UNIQUE is part of SQLite's own interface.
func isUniqueViolation(err error) bool {
	return errors.Is(err, sqlite3.CONSTRAINT_UNIQUE) ||
		errors.Is(err, sqlite3.CONSTRAINT_PRIMARYKEY)
}

// ErrIdentityHeld reports that another run already holds the identity.
//
// It exists so a caller can tell a lost race from a broken query. Losing
// the race is normal: it is the exclusivity guarantee working.
var ErrIdentityHeld = errors.New("store: identity is already leased")

// ErrLeaseNotHeld reports an operation on a lease that no longer holds its
// identity.
var ErrLeaseNotHeld = errors.New("store: lease is not held")

const leaseColumns = `id, run_id, identity_id, role, state, seed_state,
	seed_fingerprint, acquired_at, expires_at, released_at`

// leaseColumnsAliased is the same list qualified for the correlation
// query, which joins against identities. Spelled out rather than built at
// runtime, matching messageColumnsAliased: a query assembled by a function
// is one a reader has to run to know.
const leaseColumnsAliased = `l.id, l.run_id, l.identity_id, l.role, l.state, l.seed_state,
	l.seed_fingerprint, l.acquired_at, l.expires_at, l.released_at`

// NewLease is the input to AcquireLease.
type NewLease struct {
	RunID      string
	IdentityID string
	Role       string
	TTL        time.Duration
}

// AcquireLease takes exclusive ownership of an identity for a run.
//
// The lease is written with seed_state pending, because the seed adapter
// runs after this transaction commits and must not hold the write
// connection while it waits on somebody else's HTTP server. Pending fails
// closed: nothing may be claimed against it until a later transaction
// settles it.
//
// Exclusivity is not checked here. It is enforced by
// leases_one_held_per_identity, so two acquires that both saw the identity
// as free still cannot both succeed; the loser gets ErrIdentityHeld from a
// constraint violation rather than from a read that raced.
func (t *Tx) AcquireLease(ctx context.Context, in NewLease) (Lease, error) {
	if in.RunID == "" || in.IdentityID == "" {
		return Lease{}, errors.New("store: a lease needs a run and an identity")
	}
	if in.TTL <= 0 {
		return Lease{}, errors.New("store: a lease needs a positive TTL")
	}

	// A run that has already finished must not be able to take anything
	// new, and reading its state inside this transaction is what makes
	// the check meaningful against a concurrent EndRun.
	var runState string
	err := t.tx.QueryRowContext(ctx, `SELECT state FROM runs WHERE id = ?`, in.RunID).Scan(&runState)
	if errors.Is(err, sql.ErrNoRows) {
		return Lease{}, ErrNotFound
	}
	if err != nil {
		return Lease{}, fmt.Errorf("store: acquire lease: %w", err)
	}
	if runState != RunActive {
		return Lease{}, fmt.Errorf("%w: run %s is %s", ErrRunNotActive, in.RunID, runState)
	}

	now := t.s.Now()
	lease := Lease{
		ID:         NewID(),
		RunID:      in.RunID,
		IdentityID: in.IdentityID,
		Role:       in.Role,
		State:      LeaseHeld,
		SeedState:  SeedStatePending,
		AcquiredAt: now,
		ExpiresAt:  now.Add(in.TTL),
	}
	_, err = t.tx.ExecContext(ctx,
		`INSERT INTO leases (`+leaseColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, '', ?, ?, NULL)`,
		lease.ID, lease.RunID, lease.IdentityID, lease.Role, lease.State, lease.SeedState,
		timestamp(lease.AcquiredAt), timestamp(lease.ExpiresAt))
	if err != nil {
		if isUniqueViolation(err) {
			return Lease{}, fmt.Errorf("%w: identity %s", ErrIdentityHeld, in.IdentityID)
		}
		return Lease{}, fmt.Errorf("store: insert lease: %w", err)
	}
	return lease, nil
}

// SettleSeed records the outcome of the seed adapter.
//
// A seeded or skipped lease keeps its hold and becomes claimable. A failed
// one is released in the same statement that records the failure: the
// account is not usable, so holding the identity would only keep it out of
// the pool. The row itself stays forever, because correlation reads which
// lease owned an address at a past instant and a deleted lease would make
// that history lie.
func (t *Tx) SettleSeed(ctx context.Context, leaseID, seedState, fingerprint string) error {
	switch seedState {
	case SeedStateSeeded, SeedStateSkipped, SeedStateFailed:
	default:
		return fmt.Errorf("store: %q is not a seed outcome", seedState)
	}

	now := t.s.Now()
	var (
		res sql.Result
		err error
	)
	if seedState == SeedStateFailed {
		res, err = t.tx.ExecContext(ctx,
			`UPDATE leases SET seed_state = ?, seed_fingerprint = ?, state = ?, released_at = ?
			 WHERE id = ? AND state = ?`,
			seedState, fingerprint, LeaseFailed, timestamp(now), leaseID, LeaseHeld)
	} else {
		res, err = t.tx.ExecContext(ctx,
			`UPDATE leases SET seed_state = ?, seed_fingerprint = ?
			 WHERE id = ? AND state = ?`,
			seedState, fingerprint, leaseID, LeaseHeld)
	}
	if err != nil {
		return fmt.Errorf("store: settle seed: %w", err)
	}
	return expectHeld(res, leaseID, ErrLeaseNotHeld, "settle seed")
}

// ReleaseLease gives an identity back. It is idempotent: releasing a lease
// that already ended is not an error, because a fixture's cleanup runs
// whether or not the test body got there first.
func (s *Store) ReleaseLease(ctx context.Context, leaseID string) error {
	return s.WithTx(ctx, func(tx *Tx) error {
		return tx.ReleaseLease(ctx, leaseID)
	})
}

// ReleaseLease is the transactional implementation. See Store.ReleaseLease.
func (t *Tx) ReleaseLease(ctx context.Context, leaseID string) error {
	res, err := t.tx.ExecContext(ctx,
		`UPDATE leases SET state = ?, released_at = ? WHERE id = ? AND state = ?`,
		LeaseReleased, timestamp(t.s.Now()), leaseID, LeaseHeld)
	if err != nil {
		return fmt.Errorf("store: release lease: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: release lease: %w", err)
	}
	if affected == 1 {
		return nil
	}
	// Nothing changed: either the lease is gone, or it already ended.
	var state string
	err = t.tx.QueryRowContext(ctx, `SELECT state FROM leases WHERE id = ?`, leaseID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store: release lease: %w", err)
	}
	return nil
}

// releaseRunLeases ends every lease a run still holds. It runs inside
// EndRun, so finishing a run and freeing its identities is one commit.
func (t *Tx) releaseRunLeases(ctx context.Context, runID string, at time.Time) error {
	if err := t.exec(ctx,
		`UPDATE leases SET state = ?, released_at = ? WHERE run_id = ? AND state = ?`,
		LeaseReleased, timestamp(at), runID, LeaseHeld); err != nil {
		return fmt.Errorf("store: release run leases: %w", err)
	}
	return nil
}

// ExpireLeases ends held leases past their expiry.
//
// A lease outliving its run is the case this covers: the run may still be
// active while one of its leases has run out, and the identity has to come
// back either way.
func (s *Store) ExpireLeases(ctx context.Context) (int, error) {
	var expired int
	err := s.WithTx(ctx, func(tx *Tx) error {
		res, err := tx.tx.ExecContext(ctx,
			`UPDATE leases SET state = ?, released_at = ?
			 WHERE state = ? AND expires_at <= ?`,
			LeaseExpired, timestamp(tx.s.Now()), LeaseHeld, timestamp(tx.s.Now()))
		if err != nil {
			return fmt.Errorf("store: expire leases: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: expire leases: %w", err)
		}
		expired = int(affected)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return expired, nil
}

// Lease returns one lease by id.
func (s *Store) Lease(ctx context.Context, id string) (Lease, error) {
	row := s.read.QueryRowContext(ctx, `SELECT `+leaseColumns+` FROM leases WHERE id = ?`, id)
	l, err := scanLeaseRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Lease{}, ErrNotFound
	}
	return l, err
}

// ListRunLeases returns every lease a run has ever held, oldest first.
func (s *Store) ListRunLeases(ctx context.Context, runID string) ([]Lease, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT `+leaseColumns+` FROM leases WHERE run_id = ? ORDER BY acquired_at, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: list run leases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Lease
	for rows.Next() {
		l, err := scanLeaseRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list run leases: %w", err)
	}
	return out, nil
}

// LeaseAt resolves an address and a receive time to the lease that
// exclusively owned that address at that instant.
//
// This is the correlation rule the whole design rests on, and it is a
// half-open interval on purpose: a message received exactly at the release
// instant belongs to the run that was still holding the lease when it
// arrived, not to whatever comes next. A message that matches no interval
// returns ErrNotFound, which the caller records as an unbound recipient
// rather than guessing an owner.
func (s *Store) LeaseAt(ctx context.Context, addr string, at time.Time) (Lease, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT `+leaseColumnsAliased+` FROM leases l
		 JOIN identities i ON i.id = l.identity_id
		 WHERE i.addr = ?
		   AND l.acquired_at <= ?
		   AND (l.released_at IS NULL OR ? < l.released_at)
		 ORDER BY l.acquired_at DESC
		 LIMIT 1`,
		NormalizeAddress(addr), timestamp(at), timestamp(at))
	l, err := scanLeaseRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Lease{}, ErrNotFound
	}
	return l, err
}

// expectHeld turns "the guarded update matched nothing" into the caller's
// error, because every guarded update here is guarded on state = 'held'.
func expectHeld(res sql.Result, id string, notHeld error, op string) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: %s: %w", op, err)
	}
	if affected == 1 {
		return nil
	}
	return fmt.Errorf("%w: %s", notHeld, id)
}

func scanLeaseRow(row identityScanner) (Lease, error) {
	var (
		l                 Lease
		acquired, expires string
		released          sql.NullString
	)
	err := row.Scan(&l.ID, &l.RunID, &l.IdentityID, &l.Role, &l.State, &l.SeedState,
		&l.SeedFingerprint, &acquired, &expires, &released)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Lease{}, err
		}
		return Lease{}, fmt.Errorf("store: scan lease: %w", err)
	}
	if l.AcquiredAt, err = parseTimestamp(acquired); err != nil {
		return Lease{}, err
	}
	if l.ExpiresAt, err = parseTimestamp(expires); err != nil {
		return Lease{}, err
	}
	if released.Valid {
		if l.ReleasedAt, err = parseTimestamp(released.String); err != nil {
			return Lease{}, err
		}
	}
	return l, nil
}
