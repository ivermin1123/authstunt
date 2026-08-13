package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
)

// ProjectBearerPrefix marks a project bearer so the value is recognizable
// in a log or a config file and can be redacted on sight.
//
// Like RunTokenPrefix it is a convenience for redaction, not a security
// control, and it is deliberately different from the run prefix so the two
// principals cannot be confused for one another by eye or by grep.
const ProjectBearerPrefix = "pb_"

// bearerTokenBytes is the entropy behind a project bearer. It matches
// runTokenBytes for the same reason: 32 random bytes is why the stored
// digest is a plain SHA-256 rather than a password KDF.
const bearerTokenBytes = 32

// ErrNoBearer reports a project that has not been provisioned with a
// bearer yet. It is distinct from ErrNotFound on purpose: "this project
// has no credential" is a startup condition the API answers by telling the
// operator to provision one, while "no project matches this credential" is
// an authentication failure that must say nothing at all.
var ErrNoBearer = errors.New("store: this project has no bearer")

// HashProjectBearer returns the digest a project row stores for a bearer.
//
// SHA-256 over the whole token string, prefix included, exactly as
// HashRunToken does for a run token. The two principals share the
// algorithm on purpose: one convention, one place to change it.
func HashProjectBearer(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}

// SetProjectBearer mints a bearer for a project and returns it once.
//
// This is both provisioning and rotation: it overwrites whatever digest
// the row held, so the previous bearer stops authenticating the moment
// this returns. There is deliberately no way to have two live bearers for
// one project - an extra credential nobody is tracking is the thing this
// shape exists to prevent.
//
// The raw token is returned exactly once, here, and never stored.
func (s *Store) SetProjectBearer(ctx context.Context, projectID string) (string, error) {
	var token string
	err := s.WithTx(ctx, func(tx *Tx) error {
		var err error
		token, err = tx.SetProjectBearer(ctx, projectID)
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// SetProjectBearer is the transactional implementation.
//
// Provisioning composes with the ledger event that records it, for the
// reason every other credential write does: a bearer that changed with no
// entry saying so is a state change nobody can reconstruct, and the audit
// trail for a credential is the one that matters most.
func (t *Tx) SetProjectBearer(ctx context.Context, projectID string) (string, error) {
	raw := make([]byte, bearerTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("store: project bearer: %w", err)
	}
	token := ProjectBearerPrefix + base64.RawURLEncoding.EncodeToString(raw)
	digest := HashProjectBearer(token)

	res, err := t.tx.ExecContext(ctx, `UPDATE projects SET bearer_hash = ? WHERE id = ?`,
		digest[:], projectID)
	if err != nil {
		return "", fmt.Errorf("store: set project bearer: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("store: set project bearer: %w", err)
	}
	if affected == 0 {
		return "", ErrNotFound
	}
	return token, nil
}

// RevokeProjectBearer clears a project's bearer, leaving it unprovisioned.
//
// Revoking a project that has no bearer is not an error: the caller asked
// for a state, and the state already holds.
func (s *Store) RevokeProjectBearer(ctx context.Context, projectID string) error {
	return s.WithTx(ctx, func(tx *Tx) error {
		return tx.RevokeProjectBearer(ctx, projectID)
	})
}

// RevokeProjectBearer is the transactional implementation, so a revocation
// and its ledger entry commit together.
func (t *Tx) RevokeProjectBearer(ctx context.Context, projectID string) error {
	res, err := t.tx.ExecContext(ctx, `UPDATE projects SET bearer_hash = NULL WHERE id = ?`, projectID)
	if err != nil {
		return fmt.Errorf("store: revoke project bearer: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: revoke project bearer: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ProjectHasBearer reports whether a project has been provisioned.
//
// serve uses it to decide between "print the bearer you must configure"
// and "carry on", without ever reading a digest into a caller's hands.
func (s *Store) ProjectHasBearer(ctx context.Context, projectID string) (bool, error) {
	var digest []byte
	err := s.read.QueryRowContext(ctx,
		`SELECT bearer_hash FROM projects WHERE id = ?`, projectID).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("store: project bearer state: %w", err)
	}
	return len(digest) > 0, nil
}

// ProjectByBearer authenticates a bearer and returns its project.
//
// The lookup is by digest, so the raw token never reaches a query plan, an
// error message or a slow-query log - the same property RunByToken relies
// on, and the reason no constant-time comparison appears here: nothing
// compares the secret in Go at all, and the index is matching a 32-byte
// digest of a 32-byte random value.
//
// A token that matches nothing is ErrNotFound, the same answer an unknown
// id gets. An empty token is rejected before the query so that it can
// never be confused with the NULL an unprovisioned project holds.
func (s *Store) ProjectByBearer(ctx context.Context, token string) (Project, error) {
	if token == "" {
		return Project{}, ErrNotFound
	}
	digest := HashProjectBearer(token)
	return s.scanProject(s.read.QueryRowContext(ctx,
		`SELECT id, name, created_at FROM projects WHERE bearer_hash = ?`, digest[:]))
}

// CreateProject inserts a project and returns it with its generated id
// and timestamp.
func (s *Store) CreateProject(ctx context.Context, name string) (Project, error) {
	p := Project{ID: NewID(), Name: name, CreatedAt: s.Now()}
	err := s.exec(ctx, `INSERT INTO projects (id, name, created_at) VALUES (?, ?, ?)`,
		p.ID, p.Name, timestamp(p.CreatedAt))
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

// ProjectByName looks a project up by its unique name.
func (s *Store) ProjectByName(ctx context.Context, name string) (Project, error) {
	return s.scanProject(s.read.QueryRowContext(ctx,
		`SELECT id, name, created_at FROM projects WHERE name = ?`, name))
}

// Project looks a project up by id.
func (s *Store) Project(ctx context.Context, id string) (Project, error) {
	return s.scanProject(s.read.QueryRowContext(ctx,
		`SELECT id, name, created_at FROM projects WHERE id = ?`, id))
}

// ErrNoProject is returned by SingleProject when the data directory has
// not been bootstrapped yet. serve turns it into the bootstrap path
// (design 4.2 item 2).
var ErrNoProject = errors.New("store: this data directory holds no project")

// ErrMultipleProjects is returned by SingleProject when the database holds
// more than one project. A server process owns exactly one project and one
// key; two would mean mail landing under the wrong key.
var ErrMultipleProjects = errors.New("store: this data directory holds more than one project")

// SingleProject returns THE project of this data directory (design 4.2
// items 2 and 16).
//
// One server process serves one project, so the project id and its
// allowlist are resolved once at startup and handed to the SMTP pipeline
// and the API layer, instead of every message re-deriving which project it
// belongs to. Zero or several rows is a startup error, never a guess: an
// arbitrary pick would silently seal mail under a key the reader does not
// have.
func (s *Store) SingleProject(ctx context.Context) (Project, error) {
	// LIMIT 2 answers "exactly one?" without reading a whole table, and
	// the ordering makes the error deterministic across runs.
	rows, err := s.read.QueryContext(ctx,
		`SELECT id, name, created_at FROM projects ORDER BY created_at, id LIMIT 2`)
	if err != nil {
		return Project{}, fmt.Errorf("store: single project: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var found []Project
	for rows.Next() {
		var p Project
		var created string
		if err := rows.Scan(&p.ID, &p.Name, &created); err != nil {
			return Project{}, fmt.Errorf("store: scan project: %w", err)
		}
		if p.CreatedAt, err = parseTimestamp(created); err != nil {
			return Project{}, err
		}
		found = append(found, p)
	}
	if err := rows.Err(); err != nil {
		return Project{}, fmt.Errorf("store: single project: %w", err)
	}
	switch len(found) {
	case 0:
		return Project{}, ErrNoProject
	case 1:
		return found[0], nil
	default:
		return Project{}, fmt.Errorf("%w: %s and %s", ErrMultipleProjects, found[0].Name, found[1].Name)
	}
}

// ListProjects returns every project, oldest first.
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT id, name, created_at FROM projects ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Project
	for rows.Next() {
		var p Project
		var created string
		if err := rows.Scan(&p.ID, &p.Name, &created); err != nil {
			return nil, fmt.Errorf("store: scan project: %w", err)
		}
		if p.CreatedAt, err = parseTimestamp(created); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	return out, nil
}

func (s *Store) scanProject(row *sql.Row) (Project, error) {
	var p Project
	var created string
	if err := row.Scan(&p.ID, &p.Name, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Project{}, ErrNotFound
		}
		return Project{}, fmt.Errorf("store: scan project: %w", err)
	}
	var err error
	if p.CreatedAt, err = parseTimestamp(created); err != nil {
		return Project{}, err
	}
	return p, nil
}

// SetAllowlist replaces a project's allowlist patterns in one
// transaction, so a reload never leaves the list half-applied.
//
// Patterns are canonicalized on the way in (design 4.2 item 17), so the
// stored form is the one recipient matching compares against and an
// unusable pattern is rejected at configuration time rather than silently
// matching nothing at delivery time.
func (s *Store) SetAllowlist(ctx context.Context, projectID string, patterns []string) error {
	canonical := make([]string, len(patterns))
	for i, pattern := range patterns {
		c, err := CanonicalDomainPattern(pattern)
		if err != nil {
			return err
		}
		canonical[i] = c
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM allowlist WHERE project_id = ?`, projectID); err != nil {
			return fmt.Errorf("store: clear allowlist: %w", err)
		}
		for i, pattern := range canonical {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO allowlist (project_id, pattern, ord) VALUES (?, ?, ?)`,
				projectID, pattern, i)
			if err != nil {
				return fmt.Errorf("store: insert allowlist: %w", err)
			}
		}
		return nil
	})
}

// Allowlist returns a project's patterns in the order they were declared
// in authstunt.yaml. The order is load bearing: persona emails are
// generated under the first pattern, so sorting them here would silently
// move new personas to a different domain.
func (s *Store) Allowlist(ctx context.Context, projectID string) ([]string, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT pattern FROM allowlist WHERE project_id = ? ORDER BY ord`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: allowlist: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var pattern string
		if err := rows.Scan(&pattern); err != nil {
			return nil, fmt.Errorf("store: scan allowlist: %w", err)
		}
		out = append(out, pattern)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: allowlist: %w", err)
	}
	return out, nil
}
