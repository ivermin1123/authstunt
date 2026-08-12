package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AppendLedger records one audit event and returns its id. Every secret
// read and every state change writes here; callers hand the id back to
// their caller so an agent can point at the exact event.
func (s *Store) AppendLedger(ctx context.Context, e LedgerEntry) (int64, error) {
	var id int64
	err := s.WithTx(ctx, func(tx *Tx) error {
		var err error
		id, err = tx.AppendLedger(ctx, e)
		return err
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// AppendLedger is the transactional implementation. A state change and the
// event describing it belong in one commit: an audit trail that can be
// missing the record of a write that happened is not an audit trail.
func (t *Tx) AppendLedger(ctx context.Context, e LedgerEntry) (int64, error) {
	if e.TS.IsZero() {
		e.TS = t.s.Now()
	}
	if e.DetailJSON == "" {
		e.DetailJSON = "{}"
	}
	res, err := t.tx.ExecContext(ctx,
		`INSERT INTO ledger (project_id, ts, actor, run_id, persona_id, action, detail_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.ProjectID, timestamp(e.TS), e.Actor, e.RunID, nullString(e.PersonaID),
		e.Action, e.DetailJSON)
	if err != nil {
		return 0, fmt.Errorf("store: append ledger: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: ledger id: %w", err)
	}
	return id, nil
}

// LedgerFilter narrows a ledger query.
type LedgerFilter struct {
	ProjectID string
	RunID     string
	PersonaID string
	Since     time.Time
	Limit     int
}

// ListLedger returns audit events oldest first: the ledger is a timeline
// and reads as one.
func (s *Store) ListLedger(ctx context.Context, f LedgerFilter) ([]LedgerEntry, error) {
	query := `SELECT id, project_id, ts, actor, run_id, persona_id, action, detail_json
		FROM ledger WHERE 1 = 1`
	var args []any
	if f.ProjectID != "" {
		query += " AND project_id = ?"
		args = append(args, f.ProjectID)
	}
	if f.RunID != "" {
		query += " AND run_id = ?"
		args = append(args, f.RunID)
	}
	if f.PersonaID != "" {
		query += " AND persona_id = ?"
		args = append(args, f.PersonaID)
	}
	if !f.Since.IsZero() {
		query += " AND ts > ?"
		args = append(args, timestamp(f.Since))
	}
	query += " ORDER BY ts, id"
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list ledger: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []LedgerEntry
	for rows.Next() {
		var e LedgerEntry
		var persona sql.NullString
		var ts string
		if err := rows.Scan(&e.ID, &e.ProjectID, &ts, &e.Actor, &e.RunID,
			&persona, &e.Action, &e.DetailJSON); err != nil {
			return nil, fmt.Errorf("store: scan ledger: %w", err)
		}
		e.PersonaID = scanString(persona)
		if e.TS, err = parseTimestamp(ts); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list ledger: %w", err)
	}
	return out, nil
}
