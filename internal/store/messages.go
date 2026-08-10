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
	text_body, extracted_json, quarantined, received_at`

// messageColumnsAliased is the same list qualified for the listing query,
// which joins against message_recipients.
const messageColumnsAliased = `m.id, m.project_id, m.from_addr, m.subject, m.channel,
	m.raw_ref, m.html_ref, m.text_body, m.extracted_json, m.quarantined, m.received_at`

// InsertMessage writes a message and its recipients atomically: a message
// row without its envelope recipients would be invisible to every
// matcher, so the two must land together.
func (s *Store) InsertMessage(ctx context.Context, m Message) (Message, error) {
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
	if m.ExtractedJSON != "" {
		sealed, err := s.sealer.Seal([]byte(m.ExtractedJSON))
		if err != nil {
			return Message{}, fmt.Errorf("store: seal extraction: %w", err)
		}
		sealedExtraction = sealed
	}

	err = s.tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO messages (`+messageColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.ID, m.ProjectID, m.FromAddr, m.Subject, m.Channel,
			nullString(m.RawRef), nullString(m.HTMLRef), sealedBody,
			sealedExtraction, m.Quarantined, timestamp(m.ReceivedAt))
		if err != nil {
			return fmt.Errorf("store: insert message: %w", err)
		}
		for _, r := range m.Recipients {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO message_recipients (message_id, addr, kind) VALUES (?, ?, ?)
				 ON CONFLICT DO NOTHING`,
				m.ID, NormalizeAddress(r.Addr), r.Kind)
			if err != nil {
				return fmt.Errorf("store: insert recipient: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	return m, nil
}

// Message returns one message with its recipients attached, and reports
// ErrNotFound for a quarantined one.
//
// Quarantine is invisible to everything except the dashboard, and that
// includes single-message reads: /api/messages/{id}, /otp, and /links all
// land here. Making the safe behavior the default one keeps the rule from
// depending on each handler remembering to check a flag.
func (s *Store) Message(ctx context.Context, id string) (Message, error) {
	m, err := s.MessageIncludingQuarantined(ctx, id)
	if err != nil {
		return Message{}, err
	}
	if m.Quarantined {
		return Message{}, ErrNotFound
	}
	return m, nil
}

// MessageIncludingQuarantined returns a message whether or not it is
// quarantined. Only the dashboard may call it.
func (s *Store) MessageIncludingQuarantined(ctx context.Context, id string) (Message, error) {
	m, err := s.scanMessage(s.read.QueryRowContext(ctx,
		`SELECT `+messageColumns+` FROM messages WHERE id = ?`, id))
	if err != nil {
		return Message{}, err
	}
	if m.Recipients, err = s.recipients(ctx, m.ID); err != nil {
		return Message{}, err
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
	// last result never sees that result twice.
	Since time.Time
	Limit int
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
		m, err := s.scanMessageRow(rows)
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

// SetExtraction records the extraction result once the pipeline has run
// it. A message whose extractor panicked keeps a NULL result, which is
// how the API reports `extracted: null`.
func (s *Store) SetExtraction(ctx context.Context, messageID, extractedJSON string) error {
	if extractedJSON == "" {
		// A NULL result means the extractor panicked on this message.
		// Reaching that state by passing an empty string would let a
		// caller erase a real result by accident.
		return errors.New("store: empty extraction: leave the column NULL at insert instead")
	}
	sealed, err := s.sealer.Seal([]byte(extractedJSON))
	if err != nil {
		return fmt.Errorf("store: seal extraction: %w", err)
	}
	res, err := s.write.ExecContext(ctx,
		`UPDATE messages SET extracted_json = ? WHERE id = ?`, sealed, messageID)
	if err != nil {
		return fmt.Errorf("store: set extraction: %w", err)
	}
	return expectOne(res, "message")
}

func (s *Store) scanMessage(row *sql.Row) (Message, error) {
	m, err := s.scanMessageRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	return m, err
}

func (s *Store) scanMessageRow(row rowScanner) (Message, error) {
	var m Message
	var rawRef, htmlRef sql.NullString
	var sealedBody, sealedExtraction []byte
	var received string
	err := row.Scan(&m.ID, &m.ProjectID, &m.FromAddr, &m.Subject, &m.Channel,
		&rawRef, &htmlRef, &sealedBody, &sealedExtraction, &m.Quarantined, &received)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Message{}, err
		}
		return Message{}, fmt.Errorf("store: scan message: %w", err)
	}
	body, err := s.sealer.Open(sealedBody)
	if err != nil {
		return Message{}, fmt.Errorf("store: open body of %s: %w", m.ID, err)
	}
	m.TextBody = string(body)
	if sealedExtraction != nil {
		extraction, err := s.sealer.Open(sealedExtraction)
		if err != nil {
			return Message{}, fmt.Errorf("store: open extraction of %s: %w", m.ID, err)
		}
		m.ExtractedJSON = string(extraction)
	}
	m.RawRef = scanString(rawRef)
	m.HTMLRef = scanString(htmlRef)
	if m.ReceivedAt, err = parseTimestamp(received); err != nil {
		return Message{}, err
	}
	return m, nil
}
