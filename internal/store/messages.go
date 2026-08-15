package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const messageColumns = `id, project_id, from_addr, subject, channel, raw_ref, html_ref,
	text_body, extracted_json, quarantined, received_at, extraction_state`

// messageColumnsAliased is the same list qualified for the listing query,
// which joins against message_recipients.
const messageColumnsAliased = `m.id, m.project_id, m.from_addr, m.subject, m.channel,
	m.raw_ref, m.html_ref, m.text_body, m.extracted_json, m.quarantined, m.received_at,
	m.extraction_state`

// InsertMessage writes a message and its recipients atomically: a message
// row without its envelope recipients would be invisible to every
// matcher, so the two must land together.
// InsertMessage writes a message and its recipients atomically, using the
// caller's transaction so a message can be committed together with the
// ledger events that describe how it arrived.
func (s *Store) InsertMessage(ctx context.Context, m Message) (Message, error) {
	var out Message
	err := s.WithTx(ctx, func(tx *Tx) error {
		var err error
		out, err = tx.InsertMessage(ctx, m)
		return err
	})
	if err != nil {
		return Message{}, err
	}
	return out, nil
}

// InsertMessage is the transactional implementation. See Store.InsertMessage.
func (t *Tx) InsertMessage(ctx context.Context, m Message) (Message, error) {
	s := t.s
	if m.ID == "" {
		m.ID = NewID()
	}
	if m.Channel == "" {
		m.Channel = ChannelEmail
	}
	if m.ReceivedAt.IsZero() {
		m.ReceivedAt = s.Now()
	}
	sealedBody, err := s.sealer.Seal([]byte(m.TextBody))
	if err != nil {
		return Message{}, fmt.Errorf("store: seal body: %w", err)
	}
	var sealedExtraction any
	// The state is derived from the payload, never taken from the caller:
	// mail arrives with nothing extracted yet and SMTP acks it in that
	// state, while a fixture that supplies a result has nothing left to
	// extract. Deriving it keeps the column and the payload from ever
	// disagreeing.
	m.ExtractionState = ExtractionPending
	if m.ExtractedJSON != "" {
		sealed, err := s.sealer.Seal([]byte(m.ExtractedJSON))
		if err != nil {
			return Message{}, fmt.Errorf("store: seal extraction: %w", err)
		}
		sealedExtraction = sealed
		m.ExtractionState = ExtractionSuccess
	}

	if err := t.exec(ctx, `INSERT INTO messages (`+messageColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ProjectID, m.FromAddr, m.Subject, m.Channel,
		nullString(m.RawRef), nullString(m.HTMLRef), sealedBody,
		sealedExtraction, m.Quarantined, timestamp(m.ReceivedAt), m.ExtractionState); err != nil {
		return Message{}, fmt.Errorf("store: insert message: %w", err)
	}
	for _, r := range m.Recipients {
		if err := t.exec(ctx,
			`INSERT INTO message_recipients (message_id, addr, kind) VALUES (?, ?, ?)
			 ON CONFLICT DO NOTHING`,
			m.ID, NormalizeAddress(r.Addr), r.Kind); err != nil {
			return Message{}, fmt.Errorf("store: insert recipient: %w", err)
		}
	}
	return m, nil
}

// Message returns one message with its recipients attached, and reports
// ErrNotFound for a quarantined one.
//
// Quarantine is invisible to everything except the dashboard, and that
// includes single-message reads: /api/v1/messages/{id}, /otp, and /links all
// land here. Making the safe behavior the default one keeps the rule from
// depending on each handler remembering to check a flag.
// A row whose payload no longer opens reports ErrUnreadableMessage, per
// design 4.2 item 8. Quarantine is checked first: a quarantined message
// must stay invisible whatever shape its payload is in.
func (s *Store) Message(ctx context.Context, id string) (Message, error) {
	m, err := s.message(ctx, id)
	if err != nil {
		return Message{}, err
	}
	if m.Quarantined {
		return Message{}, ErrNotFound
	}
	return unreadableGuard(m)
}

// MessageIncludingQuarantined returns a message whether or not it is
// quarantined. Only the dashboard may call it.
func (s *Store) MessageIncludingQuarantined(ctx context.Context, id string) (Message, error) {
	m, err := s.message(ctx, id)
	if err != nil {
		return Message{}, err
	}
	return unreadableGuard(m)
}

// message reads one row with its recipients. An unreadable payload comes
// back marked rather than as an error, because the two callers above
// differ in what they do about it.
func (s *Store) message(ctx context.Context, id string) (Message, error) {
	m, err := s.scanMessage(ctx, s.read.QueryRowContext(ctx,
		`SELECT `+messageColumns+` FROM messages WHERE id = ?`, id))
	if err != nil {
		return Message{}, err
	}
	if m.Recipients, err = s.recipients(ctx, m.ID); err != nil {
		return Message{}, err
	}
	return m, nil
}

// unreadableGuard turns the listing's degraded marker into the by-id
// error. A single-message read has no partial answer to give: /otp and
// /links exist to hand back a credential from the body, and metadata
// alone would let a caller mistake "cannot be read" for "no code in it".
func unreadableGuard(m Message) (Message, error) {
	if m.Unreadable {
		return Message{}, fmt.Errorf("%w: message %s", ErrUnreadableMessage, m.ID)
	}
	return m, nil
}

// MessageFilter narrows a message listing.
type MessageFilter struct {
	ProjectID string
	// To matches envelope recipients only, never the To header: Bcc
	// recipients never appear in headers.
	To string
	// Since is exclusive, so a caller polling with the timestamp of its
	// last result never sees that result twice. It is an independent lower
	// bound and is never folded into Cursor (design 4.2 item 5): the two
	// answer different questions, "how far back does this caller care"
	// versus "where did the last page stop".
	Since time.Time
	// Cursor continues a previous page. Traversal is newest-first, so the
	// next page holds the rows whose (received_at, id) pair sorts strictly
	// below it.
	Cursor MessageCursor
	Limit  int
	// IncludeQuarantined is for the dashboard alone. List, wait, and MCP
	// leave it false, and quarantined mail stays invisible to them.
	IncludeQuarantined bool
}

// ListMessages returns matching messages newest first, recipients
// attached.
func (s *Store) ListMessages(ctx context.Context, f MessageFilter) ([]Message, error) {
	var where []string
	var args []any
	if f.ProjectID != "" {
		where = append(where, "m.project_id = ?")
		args = append(args, f.ProjectID)
	}
	if !f.IncludeQuarantined {
		where = append(where, "m.quarantined = 0")
	}
	if !f.Since.IsZero() {
		where = append(where, "m.received_at > ?")
		args = append(args, timestamp(f.Since))
	}
	if !f.Cursor.IsZero() {
		// Row-value comparison, which SQLite evaluates as the strict
		// lexicographic order of the pair. Spelling it as
		// "received_at < ? OR (received_at = ? AND id < ?)" is the same
		// order but invites the off-by-one where the tie case is dropped.
		where = append(where, "(m.received_at, m.id) < (?, ?)")
		args = append(args, timestamp(f.Cursor.ReceivedAt), f.Cursor.ID)
	}
	if f.To != "" {
		where = append(where, `EXISTS (SELECT 1 FROM message_recipients r
			WHERE r.message_id = m.id AND r.kind = ? AND r.addr = ?)`)
		args = append(args, RecipientEnvelope, NormalizeAddress(f.To))
	}

	query := `SELECT ` + messageColumnsAliased + ` FROM messages m`
	if len(where) > 0 {
		// nolint:gosec // G202: every fragment in `where` is a constant
		// defined above; the filter values travel as bound parameters in
		// `args` and never reach the query text.
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY m.received_at DESC, m.id DESC"
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Message
	for rows.Next() {
		m, err := s.scanMessageRow(ctx, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list messages: %w", err)
	}
	if err := s.attachRecipients(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListPendingExtractions returns the messages whose extraction never
// reached a terminal state, oldest first, for the startup recovery pass of
// design 4.2 item 6.
//
// Only pending rows come back. A failed row is terminal on purpose: mail
// that made the extractor panic once will panic on it again, and retrying
// it at every boot would turn one bad message into a permanent startup
// cost. Oldest first because recovery publishes as it goes, and an inbox
// that fills in backwards reads as if history were arriving now.
func (s *Store) ListPendingExtractions(ctx context.Context, limit int) ([]Message, error) {
	query := `SELECT ` + messageColumnsAliased + ` FROM messages m
		WHERE m.extraction_state = ? ORDER BY m.received_at, m.id`
	args := []any{ExtractionPending}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list pending extractions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Message
	for rows.Next() {
		m, err := s.scanMessageRow(ctx, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list pending extractions: %w", err)
	}
	if err := s.attachRecipients(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// recipientBatch bounds how many ids go into one IN clause. SQLite
// rejects a statement with more than 32766 bound variables, and an
// unbounded listing would hit that; batching keeps the query planner on
// the covering index either way.
const recipientBatch = 500

// attachRecipients fills in the recipients for a page of messages in
// batches. Querying per message would put a round trip per row on the
// inbox, the hottest read path in the product.
func (s *Store) attachRecipients(ctx context.Context, messages []Message) error {
	byID := make(map[string]int, len(messages))
	for i, m := range messages {
		byID[m.ID] = i
	}
	for start := 0; start < len(messages); start += recipientBatch {
		end := min(start+recipientBatch, len(messages))
		batch := messages[start:end]
		args := make([]any, len(batch))
		for i, m := range batch {
			args[i] = m.ID
		}
		if err := s.attachRecipientBatch(ctx, args, byID, messages); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) attachRecipientBatch(ctx context.Context, args []any, byID map[string]int, messages []Message) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")

	// nolint:gosec // G202: `placeholders` is a run of "?" separators
	// derived from the slice length alone. The ids are bound parameters.
	rows, err := s.read.QueryContext(ctx,
		`SELECT message_id, addr, kind FROM message_recipients
		 WHERE message_id IN (`+placeholders+`) ORDER BY message_id, kind, addr`, args...)
	if err != nil {
		return fmt.Errorf("store: recipients: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var messageID string
		var r Recipient
		if err := rows.Scan(&messageID, &r.Addr, &r.Kind); err != nil {
			return fmt.Errorf("store: scan recipient: %w", err)
		}
		if i, ok := byID[messageID]; ok {
			messages[i].Recipients = append(messages[i].Recipients, r)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: recipients: %w", err)
	}
	return nil
}

// DeleteMessage removes a message; recipients cascade. Blob cleanup is
// the caller's job, which is why the refs come back.
//
// The refs are read without decrypting anything. Retention has to be able
// to remove a message whose body no longer opens, and reading both rows
// inside one write transaction also keeps the lookup and the delete from
// disagreeing about what was there.
func (s *Store) DeleteMessage(ctx context.Context, id string) (rawRef, htmlRef string, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var raw, html sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT raw_ref, html_ref FROM messages WHERE id = ?`, id).Scan(&raw, &html)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("store: message refs: %w", err)
		}
		rawRef, htmlRef = scanString(raw), scanString(html)
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, id); err != nil {
			return fmt.Errorf("store: delete message: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return rawRef, htmlRef, nil
}

func (s *Store) recipients(ctx context.Context, messageID string) ([]Recipient, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT addr, kind FROM message_recipients WHERE message_id = ? ORDER BY kind, addr`,
		messageID)
	if err != nil {
		return nil, fmt.Errorf("store: recipients: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Recipient
	for rows.Next() {
		var r Recipient
		if err := rows.Scan(&r.Addr, &r.Kind); err != nil {
			return nil, fmt.Errorf("store: scan recipient: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: recipients: %w", err)
	}
	return out, nil
}

// SetExtraction commits the success terminal state: the result is sealed
// into the row and the message leaves the pending state for good.
//
// The message must be pending. Extraction runs once per message, and a
// second result would mean either two extractors raced over one message or
// a recovery pass picked up mail that had already been handled - both are
// bugs the caller needs to hear about, not overwrite.
func (s *Store) SetExtraction(ctx context.Context, messageID, extractedJSON string) error {
	if extractedJSON == "" {
		// An empty result is not "no result": FailExtraction is how a
		// message reaches `extracted: null`. Accepting "" here would let a
		// caller erase a real result by accident.
		return errors.New("store: empty extraction: call FailExtraction instead")
	}
	sealed, err := s.sealer.Seal([]byte(extractedJSON))
	if err != nil {
		return fmt.Errorf("store: seal extraction: %w", err)
	}
	return s.settleExtraction(ctx, messageID, ExtractionSuccess,
		`UPDATE messages SET extracted_json = ?, extraction_state = ?
		 WHERE id = ? AND extraction_state = ?`,
		sealed, ExtractionSuccess, messageID, ExtractionPending)
}

// FailExtraction commits the failed terminal state: the extraction stays
// NULL, which is how the API reports `extracted: null`, and recovery never
// looks at the row again.
//
// The caller writes the ledger error event; this layer only records that
// the message is settled.
func (s *Store) FailExtraction(ctx context.Context, messageID string) error {
	return s.settleExtraction(ctx, messageID, ExtractionFailed,
		`UPDATE messages SET extraction_state = ?
		 WHERE id = ? AND extraction_state = ?`,
		ExtractionFailed, messageID, ExtractionPending)
}

// settleExtraction runs a guarded pending-to-terminal update and explains
// which of the two reasons made it match nothing.
func (s *Store) settleExtraction(ctx context.Context, messageID, target, query string, args ...any) error {
	res, err := s.write.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: settle extraction as %s: %w", target, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: settle extraction as %s: %w", target, err)
	}
	if affected == 1 {
		return nil
	}
	// The guard swallows both "no such message" and "already settled", so
	// read the state back to say which one happened. This runs on the
	// error path only.
	var state string
	err = s.read.QueryRowContext(ctx,
		`SELECT extraction_state FROM messages WHERE id = ?`, messageID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store: settle extraction as %s: %w", target, err)
	}
	return fmt.Errorf("%w: message %s is already %s", ErrExtractionSettled, messageID, state)
}

func (s *Store) scanMessage(ctx context.Context, row *sql.Row) (Message, error) {
	m, err := s.scanMessageRow(ctx, row)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	return m, err
}

// scanMessageRow reads one row. A sealed payload that fails
// authentication marks the message Unreadable and drops both payloads
// instead of failing: one row sealed under a key that is gone must not
// take the whole inbox down with it. The condition is logged, never
// written to the ledger - the ledger records what actors did, and a read
// that could not decrypt is not an act.
func (s *Store) scanMessageRow(ctx context.Context, row rowScanner) (Message, error) {
	var m Message
	var rawRef, htmlRef sql.NullString
	var sealedBody, sealedExtraction []byte
	var received string
	err := row.Scan(&m.ID, &m.ProjectID, &m.FromAddr, &m.Subject, &m.Channel,
		&rawRef, &htmlRef, &sealedBody, &sealedExtraction, &m.Quarantined, &received,
		&m.ExtractionState)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Message{}, err
		}
		return Message{}, fmt.Errorf("store: scan message: %w", err)
	}
	body, err := s.sealer.Open(sealedBody)
	if err != nil {
		s.logUnreadable(ctx, m.ID, "body", err)
		m.Unreadable = true
	} else {
		m.TextBody = string(body)
	}
	if sealedExtraction != nil {
		extraction, err := s.sealer.Open(sealedExtraction)
		if err != nil {
			s.logUnreadable(ctx, m.ID, "extraction", err)
			m.Unreadable = true
		} else {
			m.ExtractedJSON = string(extraction)
		}
	}
	if m.Unreadable {
		// Either payload failing hides both: a body that opens next to an
		// extraction that does not is still a row whose contents cannot be
		// trusted to belong together.
		m.TextBody, m.ExtractedJSON = "", ""
	}
	m.RawRef = scanString(rawRef)
	m.HTMLRef = scanString(htmlRef)
	if m.ReceivedAt, err = parseTimestamp(received); err != nil {
		return Message{}, err
	}
	return m, nil
}

func (s *Store) logUnreadable(ctx context.Context, messageID, field string, err error) {
	// The error text can name the key id and the container length; it
	// never carries plaintext, so it is safe to log.
	s.log.WarnContext(ctx, "store: sealed message payload failed authentication",
		"message_id", messageID, "field", field, "error", err)
}
