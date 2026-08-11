package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed schema.sql
var schemaV1 string

//go:embed schema_v2.sql
var schemaV2 string

// migrations are forward-only and applied in order. Never edit a shipped
// entry: append a new one. The applied count lives in PRAGMA
// user_version, which SQLite stores in the database header.
var migrations = []string{schemaV1, schemaV2}

// SchemaVersion is the schema version this binary writes.
const SchemaVersion = 2

func migrate(ctx context.Context, db *sql.DB) error {
	return migrateTo(ctx, db, len(migrations))
}

// migrateTo applies migrations up to target. Production always passes the
// full count; the tests pass a lower one to build a database as an older
// binary left it and then upgrade it through this same runner.
func migrateTo(ctx context.Context, db *sql.DB, target int) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("store: database schema version %d is newer than this binary (%d): upgrade authstunt",
			version, len(migrations))
	}
	for i := version; i < target; i++ {
		if err := applyMigration(ctx, db, i); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, index int) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: migration %d: begin: %w", index+1, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, migrations[index]); err != nil {
		return fmt.Errorf("store: migration %d: %w", index+1, err)
	}
	// PRAGMA user_version takes no bound parameters; index is an int we
	// control, so formatting it is safe.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", index+1)); err != nil {
		return fmt.Errorf("store: migration %d: set version: %w", index+1, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: migration %d: commit: %w", index+1, err)
	}
	return nil
}
