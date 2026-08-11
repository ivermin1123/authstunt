package store

import (
	"context"
	"database/sql"
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

// DeleteProjectForTest removes a project so tests can prove the ledger
// outlives it. Real project deletion belongs to a later phase.
func (s *Store) DeleteProjectForTest(ctx context.Context, id string) error {
	return s.exec(ctx, `DELETE FROM projects WHERE id = ?`, id)
}

// CorruptBodyForTest replaces a message body with bytes that will never
// authenticate, standing in for a row sealed under a key that is gone.
func (s *Store) CorruptBodyForTest(ctx context.Context, id string) error {
	return s.exec(ctx, `UPDATE messages SET text_body = ? WHERE id = ?`,
		[]byte("not a sealed container"), id)
}

// CorruptExtractionForTest does the same to the extraction result, which
// is sealed separately and can rot on its own.
func (s *Store) CorruptExtractionForTest(ctx context.Context, id string) error {
	return s.exec(ctx, `UPDATE messages SET extracted_json = ? WHERE id = ?`,
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

// OpenAtSchemaVersionForTest opens a store whose migrations stop at
// version, so a test can seed a database the way an older binary left it
// and then prove the real runner upgrades it on the next Open.
func OpenAtSchemaVersionForTest(ctx context.Context, dataDir string, sealer BlobSealer, version int) (*Store, error) {
	return open(ctx, dataDir, sealer, Options{}, version)
}

// ExecForTest runs one statement on the write executor. The migration
// tests use it to seed rows in the column shape of an older schema, which
// the typed insert methods can no longer produce.
func (s *Store) ExecForTest(ctx context.Context, query string, args ...any) error {
	return s.exec(ctx, query, args...)
}

// QueryStringsForTest runs a single-column query and returns the values,
// so a migration test can assert on indexes and states without a second
// database handle.
func (s *Store) QueryStringsForTest(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v sql.NullString
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v.String)
	}
	return out, rows.Err()
}

// SetSyncDirForTest swaps the directory fsync so a durability test can
// prove Put calls it, and returns a function that puts the real one back.
func SetSyncDirForTest(fn func(string) error) func() {
	previous := syncDir
	syncDir = fn
	return func() { syncDir = previous }
}
