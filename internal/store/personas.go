package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const personaColumns = `id, project_id, name, email, password_enc, role, traits_json,
	seed_state, seed_output_ref, created_at, updated_at`

// CreatePersona inserts a persona. PasswordEnc must already be sealed by
// the caller: this layer never sees a plaintext credential.
func (s *Store) CreatePersona(ctx context.Context, p Persona) (Persona, error) {
	if p.ID == "" {
		p.ID = NewID()
	}
	if p.TraitsJSON == "" {
		p.TraitsJSON = "{}"
	}
	if p.SeedState == "" {
		p.SeedState = SeedPending
	}
	p.Email = NormalizeAddress(p.Email)
	now := s.Now()
	p.CreatedAt, p.UpdatedAt = now, now

	err := s.exec(ctx, `INSERT INTO personas (`+personaColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.ProjectID, p.Name, p.Email, p.PasswordEnc, p.Role, p.TraitsJSON,
		p.SeedState, nullString(p.SeedOutputRef), timestamp(p.CreatedAt), timestamp(p.UpdatedAt))
	if err != nil {
		return Persona{}, err
	}
	return p, nil
}

// Persona looks a persona up by id.
func (s *Store) Persona(ctx context.Context, id string) (Persona, error) {
	return scanPersona(s.read.QueryRowContext(ctx,
		`SELECT `+personaColumns+` FROM personas WHERE id = ?`, id))
}

// PersonaByName looks a persona up by its name within a project, the way
// authstunt.yaml refers to it.
func (s *Store) PersonaByName(ctx context.Context, projectID, name string) (Persona, error) {
	return scanPersona(s.read.QueryRowContext(ctx,
		`SELECT `+personaColumns+` FROM personas WHERE project_id = ? AND name = ?`,
		projectID, name))
}

// PersonaByEmail resolves the address mail was delivered to back to its
// persona.
func (s *Store) PersonaByEmail(ctx context.Context, email string) (Persona, error) {
	return scanPersona(s.read.QueryRowContext(ctx,
		`SELECT `+personaColumns+` FROM personas WHERE email = ?`, NormalizeAddress(email)))
}

// ListPersonas returns a project's personas ordered by name.
func (s *Store) ListPersonas(ctx context.Context, projectID string) ([]Persona, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT `+personaColumns+` FROM personas WHERE project_id = ? ORDER BY name`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: list personas: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Persona
	for rows.Next() {
		p, err := scanPersonaRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list personas: %w", err)
	}
	return out, nil
}

// UpdateSeedState records the outcome of a seed hook run.
func (s *Store) UpdateSeedState(ctx context.Context, personaID, state, outputRef string) error {
	res, err := s.write.ExecContext(ctx,
		`UPDATE personas SET seed_state = ?, seed_output_ref = ?, updated_at = ? WHERE id = ?`,
		state, nullString(outputRef), timestamp(s.Now()), personaID)
	if err != nil {
		return fmt.Errorf("store: update seed state: %w", err)
	}
	return expectOne(res, "persona")
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPersona(row *sql.Row) (Persona, error) {
	p, err := scanPersonaRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Persona{}, ErrNotFound
	}
	return p, err
}

func scanPersonaRow(row rowScanner) (Persona, error) {
	var p Persona
	var seedOutput sql.NullString
	var created, updated string
	err := row.Scan(&p.ID, &p.ProjectID, &p.Name, &p.Email, &p.PasswordEnc, &p.Role,
		&p.TraitsJSON, &p.SeedState, &seedOutput, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Persona{}, err
		}
		return Persona{}, fmt.Errorf("store: scan persona: %w", err)
	}
	p.SeedOutputRef = scanString(seedOutput)
	if p.CreatedAt, err = parseTimestamp(created); err != nil {
		return Persona{}, err
	}
	if p.UpdatedAt, err = parseTimestamp(updated); err != nil {
		return Persona{}, err
	}
	return p, nil
}

// SetRelation records a relation between two personas; rerunning it is a
// no-op, because `ensure` reconciles the same YAML on every test run.
func (s *Store) SetRelation(ctx context.Context, r Relation) error {
	return s.exec(ctx, `INSERT INTO persona_relations (project_id, from_persona, kind, to_persona)
		VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		r.ProjectID, r.FromPersona, r.Kind, r.ToPersona)
}

// Relations returns a project's persona relations.
func (s *Store) Relations(ctx context.Context, projectID string) ([]Relation, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT project_id, from_persona, kind, to_persona FROM persona_relations
		 WHERE project_id = ? ORDER BY from_persona, kind, to_persona`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: relations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Relation
	for rows.Next() {
		var r Relation
		if err := rows.Scan(&r.ProjectID, &r.FromPersona, &r.Kind, &r.ToPersona); err != nil {
			return nil, fmt.Errorf("store: scan relation: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: relations: %w", err)
	}
	return out, nil
}

// PutSecret stores a sealed secret value, replacing any previous value of
// the same kind for that persona.
func (s *Store) PutSecret(ctx context.Context, personaID, kind string, valueEnc []byte) error {
	return s.exec(ctx, `INSERT INTO secrets (persona_id, kind, value_enc, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (persona_id, kind) DO UPDATE SET value_enc = excluded.value_enc,
			created_at = excluded.created_at`,
		personaID, kind, valueEnc, timestamp(s.Now()))
}

// Secret returns a sealed secret value. Callers decrypt it and must write
// a ledger event: there are no silent secret reads.
func (s *Store) Secret(ctx context.Context, personaID, kind string) (Secret, error) {
	sec := Secret{PersonaID: personaID, Kind: kind}
	var created string
	err := s.read.QueryRowContext(ctx,
		`SELECT value_enc, created_at FROM secrets WHERE persona_id = ? AND kind = ?`,
		personaID, kind).Scan(&sec.ValueEnc, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Secret{}, ErrNotFound
		}
		return Secret{}, fmt.Errorf("store: secret: %w", err)
	}
	if sec.CreatedAt, err = parseTimestamp(created); err != nil {
		return Secret{}, err
	}
	return sec, nil
}

// PutSession saves a Playwright storage-state reference for a persona.
func (s *Store) PutSession(ctx context.Context, sess Session) error {
	if sess.SavedAt.IsZero() {
		sess.SavedAt = s.Now()
	}
	return s.exec(ctx, `INSERT INTO sessions (persona_id, name, blob_ref, saved_at, hint_expires_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (persona_id, name) DO UPDATE SET blob_ref = excluded.blob_ref,
			saved_at = excluded.saved_at, hint_expires_at = excluded.hint_expires_at`,
		sess.PersonaID, sess.Name, sess.BlobRef, timestamp(sess.SavedAt), nullTime(sess.HintExpiresAt))
}

// Session returns a saved session record.
func (s *Store) Session(ctx context.Context, personaID, name string) (Session, error) {
	sess := Session{PersonaID: personaID, Name: name}
	var saved string
	var hint sql.NullString
	err := s.read.QueryRowContext(ctx,
		`SELECT blob_ref, saved_at, hint_expires_at FROM sessions WHERE persona_id = ? AND name = ?`,
		personaID, name).Scan(&sess.BlobRef, &saved, &hint)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("store: session: %w", err)
	}
	if sess.SavedAt, err = parseTimestamp(saved); err != nil {
		return Session{}, err
	}
	if sess.HintExpiresAt, err = scanTime(hint); err != nil {
		return Session{}, err
	}
	return sess, nil
}

func expectOne(res sql.Result, what string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: %s: %w", what, ErrNotFound)
	}
	return nil
}
