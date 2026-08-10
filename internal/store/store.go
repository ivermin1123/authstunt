package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver" // registers the "sqlite3" driver
)

// ErrNotFound is returned by every lookup that finds no row.
var ErrNotFound = errors.New("store: not found")

// busyTimeout is how long a connection waits on a locked database before
// giving up. Writes are already serialized, so this only absorbs the
// overlap between a checkpointing reader and the writer.
const busyTimeout = 5 * time.Second

// Store owns the database handles and the blob store.
type Store struct {
	// write is capped at one connection: it is the write executor, so
	// every mutation queues on the same connection instead of racing for
	// the file lock. BEGIN IMMEDIATE takes the write lock upfront rather
	// than discovering the conflict on first write and failing to upgrade.
	write *sql.DB
	// read is a pool; WAL lets these run concurrently with a live write.
	read *sql.DB

	Blobs *BlobStore

	// sealer covers the columns that carry credentials in their own
	// right: message bodies and extraction results.
	sealer  BlobSealer
	dataDir string
	now     func() time.Time
}

// Options configures Open. The zero value is valid.
type Options struct {
	// Now overrides the clock used for created_at and friends. Tests set
	// it; production leaves it nil.
	Now func() time.Time
	// MaxReadConns caps the reader pool. Defaults to 8.
	MaxReadConns int
}

// Open prepares dataDir and returns a ready Store: directories created,
// PRAGMAs applied, schema migrated forward.
func Open(ctx context.Context, dataDir string, sealer BlobSealer, opts Options) (*Store, error) {
	if sealer == nil {
		return nil, errors.New("store: a sealer is required: the store holds credentials")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.MaxReadConns <= 0 {
		opts.MaxReadConns = 8
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("store: data dir: %w", err)
	}
	// MkdirAll leaves an existing directory's mode alone, and SQLite
	// creates the database file 0644. Narrowing the directory blocks
	// traversal regardless of the modes of the files inside it.
	// nolint:gosec // G302 wants 0600 or less, which for a directory would
	// drop the execute bit and make it untraversable. 0700 is the
	// tightest workable mode for a directory.
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("store: restrict data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "authstunt.db")
	write, err := openDB(dbPath, "immediate")
	if err != nil {
		return nil, err
	}
	write.SetMaxOpenConns(1)

	read, err := openDB(dbPath, "deferred")
	if err != nil {
		_ = write.Close()
		return nil, err
	}
	read.SetMaxOpenConns(opts.MaxReadConns)

	s := &Store{write: write, read: read, sealer: sealer, dataDir: dataDir, now: opts.Now}
	if err := s.init(ctx, dataDir, sealer); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init(ctx context.Context, dataDir string, sealer BlobSealer) error {
	// The write handle runs the migration, so a fresh database is fully
	// built before any reader touches it.
	if err := migrate(ctx, s.write); err != nil {
		return err
	}
	blobs, err := newBlobStore(filepath.Join(dataDir, "blobs"), sealer)
	if err != nil {
		return err
	}
	s.Blobs = blobs
	return nil
}

// openDB builds the DSN. PRAGMAs travel in the DSN so that every
// connection the pool opens later is configured the same way; setting
// them once after sql.Open would only configure the first connection.
// Order matters to the driver: busy_timeout comes first.
func openDB(path, txlock string) (*sql.DB, error) {
	q := url.Values{}
	q.Set("_txlock", txlock)
	q["_pragma"] = []string{
		fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()),
		"journal_mode(wal)",
		"synchronous(normal)",
		"foreign_keys(on)",
	}
	dsn := "file:" + url.PathEscape(path) + "?" + q.Encode()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	return db, nil
}

// Close shuts the handles down, writers first.
func (s *Store) Close() error {
	var errs []error
	if s.write != nil {
		if err := s.write.Close(); err != nil {
			errs = append(errs, fmt.Errorf("store: close writer: %w", err))
		}
	}
	if s.read != nil {
		if err := s.read.Close(); err != nil {
			errs = append(errs, fmt.Errorf("store: close reader: %w", err))
		}
	}
	return errors.Join(errs...)
}

// DataDir reports the directory the store was opened on.
func (s *Store) DataDir() string { return s.dataDir }

// Now returns the store clock, so callers stamp times the same way the
// store does.
func (s *Store) Now() time.Time { return s.now().UTC().Truncate(time.Millisecond) }

// tx runs fn inside a write transaction on the write executor.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// exec runs a single statement on the write executor.
func (s *Store) exec(ctx context.Context, query string, args ...any) error {
	if _, err := s.write.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("store: exec: %w", err)
	}
	return nil
}

// Pragmas reports the PRAGMA values in force on a pooled read
// connection. Used by tests and the health endpoint.
type Pragmas struct {
	JournalMode  string
	Synchronous  int
	ForeignKeys  bool
	BusyTimeout  int
	UserVersion  int
	IsWAL        bool
	IsSyncNormal bool
}

// ReadPragmas queries the live settings rather than trusting the DSN.
func (s *Store) ReadPragmas(ctx context.Context) (Pragmas, error) {
	var p Pragmas
	scalars := []struct {
		query string
		dest  any
	}{
		{"PRAGMA journal_mode", &p.JournalMode},
		{"PRAGMA synchronous", &p.Synchronous},
		{"PRAGMA foreign_keys", &p.ForeignKeys},
		{"PRAGMA busy_timeout", &p.BusyTimeout},
		{"PRAGMA user_version", &p.UserVersion},
	}
	for _, sc := range scalars {
		if err := s.read.QueryRowContext(ctx, sc.query).Scan(sc.dest); err != nil {
			return Pragmas{}, fmt.Errorf("store: %s: %w", sc.query, err)
		}
	}
	p.IsWAL = p.JournalMode == "wal"
	p.IsSyncNormal = p.Synchronous == 1
	return p, nil
}

// timestamp renders a time the way every column in the schema stores it.
func timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTimestamp(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: bad timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}

// nullString maps Go's empty string to SQL NULL for nullable columns.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return timestamp(t)
}

func scanString(ns sql.NullString) string {
	if !ns.Valid {
		return ""
	}
	return ns.String
}

func scanTime(ns sql.NullString) (time.Time, error) {
	if !ns.Valid {
		return time.Time{}, nil
	}
	return parseTimestamp(ns.String)
}
