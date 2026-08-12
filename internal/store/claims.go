package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrClaimTaken reports that the insert lost to one of the two unique
// indexes on claims.
//
// Losing is a normal outcome under concurrency and under retry, not a
// fault: it is the one-time and idempotency guarantees working. The
// caller reads the winning row back rather than minting a second claim.
var ErrClaimTaken = errors.New("store: claim already exists")

const claimColumns = `id, lease_id, run_id, kind, message_id, idempotency_key,
	claimed_at, expires_at`

// Candidate is one bound message a claim may be able to use, carrying the
// extraction it would read the secret from.
type Candidate struct {
	MessageID  string
	ReceivedAt time.Time
	// ExtractedJSON is the unsealed extraction. It carries the secret, so
	// it never reaches evidence and never leaves the claim path.
	ExtractedJSON string
}

// CandidateFilter narrows the search for a message that can back a claim.
type CandidateFilter struct {
	LeaseID string
	Kind    string
	// NotBefore is the server-owned lower bound,
	// max(run.checkpoint_at, lease.acquired_at). There is no field here
	// for a client-supplied bound, which is the point: a caller cannot
	// widen its own window to reach last night's code.
	NotBefore time.Time
	Limit     int
}

// Rejections counts the candidates a filter refused, so a claim that
// found nothing can say why rather than reporting a bare timeout.
type Rejections struct {
	Suspect        int
	AlreadyClaimed int
	Stale          int
	ExtractionFail int
	Pending        int
	Bindings       int
}

// ClaimCandidates returns the messages bound to a lease that may back a
// claim, oldest first, along with a count of what was refused and why.
//
// Oldest first is the duplicate rule: when an application sends the same
// code twice, the first copy backs the claim and the second stays visible
// and unclaimed. Taking the newest would make a resend silently invalidate
// the code the user already typed.
func (s *Store) ClaimCandidates(ctx context.Context, f CandidateFilter) ([]Candidate, Rejections, error) {
	if f.LeaseID == "" || f.Kind == "" {
		return nil, Rejections{}, errors.New("store: candidates need a lease and a kind")
	}
	query := `SELECT m.id, m.received_at, m.extracted_json, m.extraction_state,
		       m.quarantined, b.suspect,
		       EXISTS (SELECT 1 FROM claims c
		               WHERE c.message_id = m.id AND c.kind = ?) AS claimed
		  FROM message_bindings b
		  JOIN messages m ON m.id = b.message_id
		 WHERE b.lease_id = ?
		 ORDER BY m.received_at, m.id`
	args := []any{f.Kind, f.LeaseID}
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, Rejections{}, fmt.Errorf("store: claim candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var (
		out      []Candidate
		refused  Rejections
		notFirst = timestamp(f.NotBefore)
	)
	for rows.Next() {
		var (
			id, received, state, suspect string
			sealed                       []byte
			quarantined, claimed         bool
		)
		if err := rows.Scan(&id, &received, &sealed, &state, &quarantined, &suspect, &claimed); err != nil {
			return nil, Rejections{}, fmt.Errorf("store: scan candidate: %w", err)
		}
		refused.Bindings++
		// Order matters only for what the caller reports, not for what it
		// hands over: every branch below refuses the message.
		switch {
		case quarantined:
			// Quarantine means the envelope touched an address this
			// project does not own. It is invisible to every automated
			// read, and a claim is the most automated read there is.
			continue
		case suspect != SuspectNone:
			refused.Suspect++
			continue
		case claimed:
			refused.AlreadyClaimed++
			continue
		case !f.NotBefore.IsZero() && received < notFirst:
			refused.Stale++
			continue
		case state == ExtractionFailed:
			refused.ExtractionFail++
			continue
		case state != ExtractionSuccess:
			// Still pending. The message is real and may become a
			// candidate in a moment, so it is not a refusal to report.
			refused.Pending++
			continue
		}
		extracted, err := s.sealer.Open(sealed)
		if err != nil {
			// A row whose extraction no longer authenticates cannot hand
			// over a secret, and it is a terminal condition for this
			// message rather than something to wait out.
			s.logUnreadable(ctx, id, "extraction", err)
			refused.ExtractionFail++
			continue
		}
		receivedAt, err := parseTimestamp(received)
		if err != nil {
			return nil, Rejections{}, err
		}
		out = append(out, Candidate{
			MessageID:     id,
			ReceivedAt:    receivedAt,
			ExtractedJSON: string(extracted),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, Rejections{}, fmt.Errorf("store: claim candidates: %w", err)
	}
	return out, refused, nil
}

// NewClaim is the input to RecordClaim.
type NewClaim struct {
	LeaseID        string
	RunID          string
	Kind           string
	MessageID      string
	IdempotencyKey string
	TTL            time.Duration
	// NotAfter caps the replay window at the end of the grant it came
	// from, normally the earlier of the lease and the run expiry. It is an
	// instant rather than a shorter TTL because the comparison has to
	// happen against the clock that stamps the row: this store's clock is
	// monotonic and steps ahead of the wall clock to keep instants
	// distinct, so a caller subtracting wall times would compute a window
	// that lands past the deadline it was trying to respect. Zero means no
	// cap.
	NotAfter time.Time
}

// ErrClaimWindowClosed reports that the grant a claim would be recorded
// under has no time left in it.
//
// It is a refusal, not a failure: handing the secret over anyway would
// mint a claim whose whole replayable life is outside the lease that
// authorized it.
var ErrClaimWindowClosed = errors.New("store: the claim window is already closed")

// RecordClaim writes a successful claim, or reports that one of the two
// unique indexes refused it.
//
// Only successes are recorded. A stored failure under an idempotency key
// would make a Playwright retry replay that failure for the rest of the
// key's life; every failed outcome goes to the ledger instead, which is
// the audit trail either way.
func (s *Store) RecordClaim(ctx context.Context, in NewClaim) (Claim, error) {
	if in.LeaseID == "" || in.RunID == "" || in.Kind == "" || in.IdempotencyKey == "" {
		return Claim{}, errors.New("store: a claim needs a lease, a run, a kind and a key")
	}
	if in.TTL <= 0 {
		return Claim{}, errors.New("store: a claim needs a positive TTL")
	}
	now := s.Now()
	expires := now.Add(in.TTL)
	if !in.NotAfter.IsZero() && in.NotAfter.Before(expires) {
		expires = in.NotAfter
	}
	if !now.Before(expires) {
		return Claim{}, ErrClaimWindowClosed
	}
	claim := Claim{
		ID:             NewID(),
		LeaseID:        in.LeaseID,
		RunID:          in.RunID,
		Kind:           in.Kind,
		MessageID:      in.MessageID,
		IdempotencyKey: in.IdempotencyKey,
		ClaimedAt:      now,
		ExpiresAt:      expires,
	}
	err := s.exec(ctx, `INSERT INTO claims (`+claimColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		claim.ID, claim.LeaseID, claim.RunID, claim.Kind, nullString(claim.MessageID),
		claim.IdempotencyKey, timestamp(claim.ClaimedAt), timestamp(claim.ExpiresAt))
	if err != nil {
		if isUniqueViolation(err) {
			return Claim{}, ErrClaimTaken
		}
		return Claim{}, fmt.Errorf("store: record claim: %w", err)
	}
	return claim, nil
}

// ClaimByKey returns the claim recorded under an idempotency key.
func (s *Store) ClaimByKey(ctx context.Context, leaseID, key string) (Claim, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT `+claimColumns+` FROM claims WHERE lease_id = ? AND idempotency_key = ?`,
		leaseID, key)
	return scanClaim(row)
}

// ListRunClaims returns every claim a run made, oldest first, for the
// evidence surface.
func (s *Store) ListRunClaims(ctx context.Context, runID string) ([]Claim, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT `+claimColumns+` FROM claims WHERE run_id = ? ORDER BY claimed_at, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: list claims: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Claim
	for rows.Next() {
		c, err := scanClaimRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list claims: %w", err)
	}
	return out, nil
}

func scanClaim(row *sql.Row) (Claim, error) {
	c, err := scanClaimRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Claim{}, ErrNotFound
	}
	return c, err
}

func scanClaimRow(row rowScanner) (Claim, error) {
	var (
		c                Claim
		messageID        sql.NullString
		claimed, expires string
	)
	err := row.Scan(&c.ID, &c.LeaseID, &c.RunID, &c.Kind, &messageID,
		&c.IdempotencyKey, &claimed, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Claim{}, err
		}
		return Claim{}, fmt.Errorf("store: scan claim: %w", err)
	}
	c.MessageID = scanString(messageID)
	if c.ClaimedAt, err = parseTimestamp(claimed); err != nil {
		return Claim{}, err
	}
	if c.ExpiresAt, err = parseTimestamp(expires); err != nil {
		return Claim{}, err
	}
	return c, nil
}
