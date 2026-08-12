package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Tx is an open write transaction, seen through the operations the store
// is willing to compose.
//
// The raw *sql.Tx stays unexported on purpose. A seam that handed out the
// transaction would let any caller write arbitrary SQL inside the store's
// own atomicity boundary, and the invariants this package enforces - the
// derived extraction state, canonical addresses, the one-held-lease index -
// are only invariants because every write goes through a method that
// applies them.
//
// What the seam buys is the ability to make several of those writes one
// unit. A message row without its recipients is invisible to every
// matcher; a lease without its ledger event is a state change with no
// audit trail. Both are cases where a partial commit is worse than no
// commit.
type Tx struct {
	s  *Store
	tx *sql.Tx
}

// WithTx runs fn inside one write transaction, committing when it returns
// nil and rolling back on any error.
//
// The write executor holds a single connection, so a transaction here
// blocks every other writer in the process. Nothing slow belongs inside
// one: the seed adapter's HTTP call is deliberately made between two
// transactions rather than inside a single longer one.
func (s *Store) WithTx(ctx context.Context, fn func(*Tx) error) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		return fn(&Tx{s: s, tx: tx})
	})
}

// exec runs one statement inside the transaction.
func (t *Tx) exec(ctx context.Context, query string, args ...any) error {
	if _, err := t.tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("store: exec: %w", err)
	}
	return nil
}
