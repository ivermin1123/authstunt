package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// NewIdentity is the input to CreateIdentity.
type NewIdentity struct {
	ProjectID string
	// PersonaID is required for a pooled identity, which points at the
	// persona holding that account's credentials, and empty for an
	// ephemeral one, which has none until its run creates them.
	PersonaID string
	Addr      string
	Role      string
	Mode      string
}

const identityColumns = `id, project_id, persona_id, addr, role, mode,
	cooldown_until, created_at`

// CreateIdentity records an address the project owns.
func (s *Store) CreateIdentity(ctx context.Context, in NewIdentity) (Identity, error) {
	var out Identity
	err := s.WithTx(ctx, func(tx *Tx) error {
		var err error
		out, err = tx.CreateIdentity(ctx, in)
		return err
	})
	if err != nil {
		return Identity{}, err
	}
	return out, nil
}

// CreateIdentity is the transactional implementation, so minting an
// ephemeral identity and leasing it can be one commit.
func (t *Tx) CreateIdentity(ctx context.Context, in NewIdentity) (Identity, error) {
	switch in.Mode {
	case ModeEphemeral:
		if in.PersonaID != "" {
			// An ephemeral identity exists for exactly one run and its
			// account is created during the test. Attaching a persona
			// would make it look like a pooled account and let it be
			// selected as one.
			return Identity{}, errors.New("store: an ephemeral identity has no persona")
		}
	case ModePooled:
		if in.PersonaID == "" {
			return Identity{}, errors.New("store: a pooled identity needs a persona")
		}
	default:
		return Identity{}, fmt.Errorf("store: %q is not an identity mode", in.Mode)
	}
	if in.ProjectID == "" {
		return Identity{}, errors.New("store: an identity needs a project")
	}
	addr := NormalizeAddress(in.Addr)
	if addr == "" {
		return Identity{}, errors.New("store: an identity needs an address")
	}

	id := Identity{
		ID:        NewID(),
		ProjectID: in.ProjectID,
		PersonaID: in.PersonaID,
		Addr:      addr,
		Role:      in.Role,
		Mode:      in.Mode,
		CreatedAt: t.s.Now(),
	}
	if err := t.exec(ctx,
		`INSERT INTO identities (`+identityColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, NULL, ?)`,
		id.ID, id.ProjectID, nullString(id.PersonaID), id.Addr, id.Role, id.Mode,
		timestamp(id.CreatedAt)); err != nil {
		return Identity{}, fmt.Errorf("store: insert identity: %w", err)
	}
	return id, nil
}

// Identity returns one identity by id.
func (s *Store) Identity(ctx context.Context, id string) (Identity, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT `+identityColumns+` FROM identities WHERE id = ?`, id)
	return scanIdentity(row)
}

// IdentityByAddr returns the identity owning an address.
//
// The address is canonicalized first, because the caller is usually
// holding whatever a sending application put in an envelope.
func (s *Store) IdentityByAddr(ctx context.Context, addr string) (Identity, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT `+identityColumns+` FROM identities WHERE addr = ?`, NormalizeAddress(addr))
	return scanIdentity(row)
}

// ListPooledIdentities returns the pooled identities of a role, oldest
// first, so a caller that wants the least recently added gets a stable
// order rather than whatever the planner chose.
func (s *Store) ListPooledIdentities(ctx context.Context, projectID, role string) ([]Identity, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT `+identityColumns+` FROM identities
		 WHERE project_id = ? AND mode = ? AND role = ?
		 ORDER BY created_at, id`,
		projectID, ModePooled, role)
	if err != nil {
		return nil, fmt.Errorf("store: list pooled identities: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Identity
	for rows.Next() {
		id, err := scanIdentityRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list pooled identities: %w", err)
	}
	return out, nil
}

// CountPooledIdentities reports how many pooled identities the project has,
// across every role.
//
// It exists so serve can say at startup that pooled mode was asked for and
// has nothing to serve. The per-role listing cannot answer that: it would
// have to be asked role by role, and the roles are not known until a caller
// names one.
func (s *Store) CountPooledIdentities(ctx context.Context, projectID string) (int, error) {
	var n int
	err := s.read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM identities WHERE project_id = ? AND mode = ?`,
		projectID, ModePooled).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count pooled identities: %w", err)
	}
	return n, nil
}

// SetCooldown holds a pooled identity out of the pool until an instant.
//
// It is applied on release and on a seed that failed part way. The second
// case is the one worth stating: an account whose seeding stopped half
// finished is not in a known state, and handing it to the next run would
// make that run's failure somebody else's mystery.
func (t *Tx) SetCooldown(ctx context.Context, identityID string, until time.Time) error {
	if err := t.exec(ctx,
		`UPDATE identities SET cooldown_until = ? WHERE id = ? AND mode = ?`,
		timestamp(until), identityID, ModePooled); err != nil {
		return fmt.Errorf("store: set cooldown: %w", err)
	}
	return nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type identityScanner interface {
	Scan(dest ...any) error
}

func scanIdentity(row *sql.Row) (Identity, error) {
	id, err := scanIdentityRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrNotFound
	}
	return id, err
}

func scanIdentityRow(row identityScanner) (Identity, error) {
	var (
		id                Identity
		persona, cooldown sql.NullString
		created           string
	)
	err := row.Scan(&id.ID, &id.ProjectID, &persona, &id.Addr, &id.Role, &id.Mode,
		&cooldown, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Identity{}, err
		}
		return Identity{}, fmt.Errorf("store: scan identity: %w", err)
	}
	id.PersonaID = persona.String
	if cooldown.Valid {
		if id.CooldownUntil, err = parseTimestamp(cooldown.String); err != nil {
			return Identity{}, err
		}
	}
	if id.CreatedAt, err = parseTimestamp(created); err != nil {
		return Identity{}, err
	}
	return id, nil
}
