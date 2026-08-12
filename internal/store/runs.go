package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// RunTokenPrefix marks a run token so the value is recognizable in a log
// or a config file and can be redacted on sight.
//
// It is a convenience for redaction, not a security control: a scanner may
// or may not know this prefix, and treating it as a guarantee that leaked
// tokens get caught would be exactly the wrong conclusion.
const RunTokenPrefix = "rt_"

// runTokenBytes is the entropy behind a run token. 32 bytes is why the
// stored digest is a plain SHA-256 rather than a password KDF: there is no
// low-entropy secret here to make expensive to guess.
const runTokenBytes = 32

// ErrRunNotActive reports an operation on a run that has already reached a
// terminal state.
var ErrRunNotActive = errors.New("store: run is not active")

// NewRun is the input to CreateRun. The store owns every timestamp, so
// there is no field here to set one.
type NewRun struct {
	ProjectID string
	TTL       time.Duration
}

// CreateRun mints a run and its token.
//
// The raw token is returned exactly once, here, and never stored: the row
// keeps only a SHA-256 digest of it. Nothing downstream can leak the token
// back out, because nothing downstream has it.
func (s *Store) CreateRun(ctx context.Context, in NewRun) (Run, string, error) {
	var (
		run   Run
		token string
	)
	err := s.WithTx(ctx, func(tx *Tx) error {
		var err error
		run, token, err = tx.CreateRun(ctx, in)
		return err
	})
	if err != nil {
		return Run{}, "", err
	}
	return run, token, nil
}

// CreateRun is the transactional implementation. See Store.CreateRun.
func (t *Tx) CreateRun(ctx context.Context, in NewRun) (Run, string, error) {
	if in.ProjectID == "" {
		return Run{}, "", errors.New("store: a run needs a project")
	}
	if in.TTL <= 0 {
		return Run{}, "", errors.New("store: a run needs a positive TTL")
	}

	raw := make([]byte, runTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return Run{}, "", fmt.Errorf("store: run token: %w", err)
	}
	token := RunTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	digest := HashRunToken(token)

	now := t.s.Now()
	run := Run{
		ID:        NewID(),
		ProjectID: in.ProjectID,
		State:     RunActive,
		// checkpoint_at is the create instant, not the first claim's.
		// Setting it here is what removes the stale-mail race: every
		// message that predates the run is already excluded before the
		// browser has done anything.
		CheckpointAt: now,
		StartedAt:    now,
		ExpiresAt:    now.Add(in.TTL),
	}
	if err := t.exec(ctx,
		`INSERT INTO runs (id, project_id, token_hash, state, fail_reason,
			checkpoint_at, started_at, expires_at, ended_at)
		 VALUES (?, ?, ?, ?, '', ?, ?, ?, NULL)`,
		run.ID, run.ProjectID, digest[:], run.State,
		timestamp(run.CheckpointAt), timestamp(run.StartedAt), timestamp(run.ExpiresAt)); err != nil {
		return Run{}, "", fmt.Errorf("store: insert run: %w", err)
	}
	return run, token, nil
}

// HashRunToken returns the digest a run row stores for a token.
//
// SHA-256 over the whole token string, prefix included, so the value that
// authenticates is exactly the value the client was handed.
func HashRunToken(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}

const runColumns = `id, project_id, state, fail_reason, checkpoint_at,
	started_at, expires_at, ended_at`

// Run returns one run by id.
func (s *Store) Run(ctx context.Context, id string) (Run, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT `+runColumns+` FROM runs WHERE id = ?`, id)
	return scanRun(row)
}

// RunByToken authenticates a token and returns its run.
//
// The lookup is by digest, so the raw token never reaches a query plan, an
// error message or a slow-query log. A token that matches nothing is
// ErrNotFound, the same answer a well-formed but unknown id gets: there is
// nothing to learn from telling the two apart.
func (s *Store) RunByToken(ctx context.Context, token string) (Run, error) {
	digest := HashRunToken(token)
	row := s.read.QueryRowContext(ctx,
		`SELECT `+runColumns+` FROM runs WHERE token_hash = ?`, digest[:])
	return scanRun(row)
}

// EndRun moves an active run to a terminal state and releases every lease
// it holds, in one transaction.
//
// The two belong together: a completed run that still held an identity
// would block the next acquire on an identity nobody is using, and the
// window between two statements is exactly where a crash would leave that.
func (s *Store) EndRun(ctx context.Context, runID, state, reason string) error {
	return s.WithTx(ctx, func(tx *Tx) error {
		return tx.EndRun(ctx, runID, state, reason)
	})
}

// EndRun is the transactional implementation. See Store.EndRun.
func (t *Tx) EndRun(ctx context.Context, runID, state, reason string) error {
	switch state {
	case RunComplete, RunFailed, RunExpired:
	default:
		return fmt.Errorf("store: %q is not a terminal run state", state)
	}
	now := t.s.Now()
	res, err := t.tx.ExecContext(ctx,
		`UPDATE runs SET state = ?, fail_reason = ?, ended_at = ?
		 WHERE id = ? AND state = ?`,
		state, reason, timestamp(now), runID, RunActive)
	if err != nil {
		return fmt.Errorf("store: end run: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: end run: %w", err)
	}
	if affected == 0 {
		// Either the run does not exist or it is already terminal. Both
		// are answered by reading the row back, on the error path only.
		var current string
		err := t.tx.QueryRowContext(ctx, `SELECT state FROM runs WHERE id = ?`, runID).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("store: end run: %w", err)
		}
		// Ending a run twice is not an error worth failing a test
		// teardown over, as long as it ended the same way.
		if current == state {
			return nil
		}
		return fmt.Errorf("%w: run %s is already %s", ErrRunNotActive, runID, current)
	}
	return t.releaseRunLeases(ctx, runID, now)
}

// ExpireRuns moves every active run past its expiry to expired and
// releases the leases they held.
//
// It is idempotent and safe to run after a crash, which is the whole
// point: a process that dies holding leases must not leave identities
// locked until someone notices.
func (s *Store) ExpireRuns(ctx context.Context) (int, error) {
	var expired int
	err := s.WithTx(ctx, func(tx *Tx) error {
		now := tx.s.Now()
		rows, err := tx.tx.QueryContext(ctx,
			`SELECT id FROM runs WHERE state = ? AND expires_at <= ?`,
			RunActive, timestamp(now))
		if err != nil {
			return fmt.Errorf("store: expire runs: %w", err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return fmt.Errorf("store: expire runs: %w", err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: expire runs: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("store: expire runs: %w", err)
		}
		for _, id := range ids {
			if err := tx.EndRun(ctx, id, RunExpired, "run ttl elapsed"); err != nil {
				return err
			}
		}
		expired = len(ids)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return expired, nil
}

func scanRun(row *sql.Row) (Run, error) {
	var (
		r                            Run
		checkpoint, started, expires string
		ended                        sql.NullString
	)
	err := row.Scan(&r.ID, &r.ProjectID, &r.State, &r.FailReason,
		&checkpoint, &started, &expires, &ended)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("store: scan run: %w", err)
	}
	if r.CheckpointAt, err = parseTimestamp(checkpoint); err != nil {
		return Run{}, err
	}
	if r.StartedAt, err = parseTimestamp(started); err != nil {
		return Run{}, err
	}
	if r.ExpiresAt, err = parseTimestamp(expires); err != nil {
		return Run{}, err
	}
	if ended.Valid {
		if r.EndedAt, err = parseTimestamp(ended.String); err != nil {
			return Run{}, err
		}
	}
	return r, nil
}
