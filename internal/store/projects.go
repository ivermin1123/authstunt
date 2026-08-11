package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

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
