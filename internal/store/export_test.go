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

// CorruptBodyForTest replaces a message body with bytes that will never
// authenticate, standing in for a row sealed under a key that is gone.
func (s *Store) CorruptBodyForTest(ctx context.Context, id string) error {
	return s.exec(ctx, `UPDATE messages SET text_body = ? WHERE id = ?`,
		[]byte("not a sealed container"), id)
}

// RawTimestampForTest returns the received_at text exactly as stored, so
// a test can assert the on-disk ordering property rather than the parsed
// value.
func (s *Store) RawTimestampForTest(ctx context.Context, id string) (string, error) {
	var ts string
	err := s.read.QueryRowContext(ctx, `SELECT received_at FROM messages WHERE id = ?`, id).Scan(&ts)
	return ts, err
}
