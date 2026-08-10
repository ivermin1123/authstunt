package store

import (
	"context"
	"fmt"
)

// This file is compiled only into the test binary. It exposes the few
// operations the external tests need but the running server has no
// business calling: persona deletion belongs to `authstunt prune`, and
// nothing writes the schema version except the migration runner.

// DeletePersonaForTest removes a persona so tests can prove the schema
// cascades to its secrets, sessions, and relations.
func (s *Store) DeletePersonaForTest(ctx context.Context, id string) error {
	return s.exec(ctx, `DELETE FROM personas WHERE id = ?`, id)
}

// BumpSchemaVersionForTest fakes a database written by a newer binary.
func (s *Store) BumpSchemaVersionForTest(ctx context.Context, version int) error {
	return s.exec(ctx, fmt.Sprintf("PRAGMA user_version = %d", version))
}
