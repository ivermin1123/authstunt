package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver" // registers the "sqlite3" driver
)

// ErrNotFound is returned by every lookup that finds no row.
var ErrNotFound = errors.New("store: not found")

// ErrUnreadableMessage is returned by a by-id message read whose sealed
// body or extraction failed authentication (design 4.2 item 8). The REST
// layer maps it to 500 with error code "unreadable_message"; listings
// degrade to metadata with Unreadable set instead of failing the page.
var ErrUnreadableMessage = errors.New("store: message payload failed authentication")

// ErrExtractionSettled is returned when a message is asked to leave the
// pending extraction state twice. A message is extracted exactly once, so
// the second attempt is a bug in the caller, not a state to overwrite.
var ErrExtractionSettled = errors.New("store: extraction already settled")

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
	log     *slog.Logger

	// clock guards the monotonic tie-break in Now. Stamping is on every
	// write path, so the lock is held for two comparisons and nothing
	// else.
	clock   sync.Mutex
	lastNow time.Time
}

// Options configures Open. The zero value is valid.
type Options struct {
	// Now overrides the clock used for created_at and friends. Tests set
	// it; production leaves it nil.
	Now func() time.Time
	// MaxReadConns caps the reader pool. Defaults to 8.
	MaxReadConns int
	// Logger receives the events a read path can only report, never fix:
	// today that is a row whose sealed payload no longer authenticates.
	// Defaults to slog.Default().
	Logger *slog.Logger
}

// Open prepares dataDir and returns a ready Store: directories created,
// PRAGMAs applied, schema migrated forward.
func Open(ctx context.Context, dataDir string, sealer BlobSealer, opts Options) (*Store, error) {
	return open(ctx, dataDir, sealer, opts, len(migrations))
}

// open is Open with the schema target spelled out. Production always
// migrates all the way; the migration tests stop short so they can seed a
// database as an older binary left it and then upgrade it through this
// same path.
func open(ctx context.Context, dataDir string, sealer BlobSealer, opts Options, target int) (*Store, error) {
	if sealer == nil {
		return nil, errors.New("store: a sealer is required: the store holds credentials")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.MaxReadConns <= 0 {
		opts.MaxReadConns = 8
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
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

	s := &Store{
		write:   write,
		read:    read,
		sealer:  sealer,
		dataDir: dataDir,
		now:     opts.Now,
		log:     opts.Logger,
	}
	if err := s.init(ctx, dataDir, sealer, target); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init(ctx context.Context, dataDir string, sealer BlobSealer, target int) error {
	if err := waitForConnection(ctx, s.write); err != nil {
		return err
	}
	// The write handle runs the migration, so a fresh database is fully
	// built before any reader touches it.
	if err := migrateTo(ctx, s.write, target); err != nil {
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

// waitForConnection opens the first connection, retrying while the file
// is locked.
//
// Switching a fresh database into WAL needs brief exclusive access, and
// busy_timeout does not cover that switch: two processes opening the same
// new data directory at once (a fixture starting the server while `ensure`
// runs, say) would otherwise see one of them fail outright, with an error
// that names the pragma rather than the race.
func waitForConnection(ctx context.Context, db *sql.DB) error {
	const attempts = 10
	var err error
	for i := range attempts {
		if err = db.PingContext(ctx); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("store: connect: %w", ctx.Err())
		}
		if !strings.Contains(err.Error(), "database is locked") {
			return fmt.Errorf("store: connect: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("store: connect: %w", ctx.Err())
		case <-time.After(time.Duration(i+1) * 50 * time.Millisecond):
		}
	}
	return fmt.Errorf("store: another process is still initializing this data directory: %w", err)
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
//
// It never returns the same instant twice. That is not a nicety: a lease
// owns an address over the half-open interval [acquired_at, released_at),
// and two stamps that collapsed onto one instant would make that interval
// empty, so the lease would own no instant at all and mail that arrived
// while it was held would resolve to no owner. The underlying clock is
// truncated to a millisecond, and acquiring plus releasing inside one
// millisecond is ordinary, so the collision is the normal case rather
// than a rare one.
//
// The tie-break costs a nanosecond per collision, which is the smallest
// step the stored timestamp layout can represent. Drift against the wall
// clock is therefore bounded by the number of stamps issued inside one
// millisecond and is not observable at any resolution this product
// reports. It holds within one process only; AcquireLease carries the
// same guarantee across a restart or a clock that steps backwards.
func (s *Store) Now() time.Time {
	now := s.now().UTC().Truncate(time.Millisecond)
	s.clock.Lock()
	defer s.clock.Unlock()
	if !now.After(s.lastNow) {
		now = s.lastNow.Add(time.Nanosecond)
	}
	s.lastNow = now
	return now
}

// tx runs fn inside a write transaction on the write executor.
//
// fn must issue its statements on the tx it is handed. Calling any other
// store method from inside fn would ask the write handle for a second
// connection, and there is only one, so it would block until the context
// expires.
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

// timestampLayout is deliberately fixed width. Timestamps live in TEXT
// columns, so SQLite orders and compares them byte by byte: RFC3339Nano
// drops trailing zeros, which makes "12:00:00Z" sort after "12:00:00.5Z"
// because 'Z' outranks '.'. That would reverse the inbox order and let a
// `since` cursor skip messages permanently. Zeros in the layout force
// every value to the same width, so byte order matches time order.
const timestampLayout = "2006-01-02T15:04:05.000000000Z07:00"

// timestamp renders a time the way every column in the schema stores it.
func timestamp(t time.Time) string {
	return t.UTC().Format(timestampLayout)
}

func parseTimestamp(s string) (time.Time, error) {
	// RFC3339Nano parses both the fixed-width form and the trimmed form.
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
