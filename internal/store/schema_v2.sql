-- AuthStunt schema v2, per design 4.2 items 6, 9, and the generation
-- counter of item 4. schema.sql (v1) is shipped and stays byte-stable;
-- every change lands here.

-- 1. The ledger id gains AUTOINCREMENT so retention deletes can never
-- cause an id to be reused. Ledger ids appear in MCP tool results, so a
-- reused id would make an old citation point at a different event. SQLite
-- can only add AUTOINCREMENT by rebuilding the table. Nothing references
-- ledger with a foreign key (an audit trail a delete can erase is not an
-- audit trail, schema.sql), so the rebuild needs no FK dance.
CREATE TABLE ledger_v2 (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  TEXT NOT NULL,
    ts          TEXT NOT NULL,
    actor       TEXT NOT NULL
        CHECK (actor IN ('mcp', 'rest', 'fixture', 'ui', 'system')),
    run_id      TEXT NOT NULL DEFAULT '',
    persona_id  TEXT,
    action      TEXT NOT NULL,
    detail_json TEXT NOT NULL DEFAULT '{}'
) STRICT;

-- Copying the ids explicitly carries sqlite_sequence to the highest one,
-- which is what makes the next insert land above every id ever used.
INSERT INTO ledger_v2 (id, project_id, ts, actor, run_id, persona_id, action, detail_json)
SELECT id, project_id, ts, actor, run_id, persona_id, action, detail_json
FROM ledger ORDER BY id;

DROP TABLE ledger;
ALTER TABLE ledger_v2 RENAME TO ledger;

CREATE INDEX ledger_project_ts ON ledger (project_id, ts);
CREATE INDEX ledger_run ON ledger (run_id) WHERE run_id <> '';
CREATE INDEX ledger_persona_ts ON ledger (persona_id, ts) WHERE persona_id IS NOT NULL;

-- 2. The event generation counter behind the `generation-seq` event ids
-- (design 4.2 item 4). It is persisted so that a restart within the same
-- second, or a clock that moves backwards, still produces ids no client
-- has seen. One row, enforced by the primary key CHECK.
CREATE TABLE event_generation (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    generation INTEGER NOT NULL
) STRICT;

INSERT INTO event_generation (id, generation) VALUES (1, 0);

-- 3. The message extraction state (design 4.2 item 6). SMTP acks mail
-- only after this row commits, so the row itself has to say whether
-- extraction still owes an answer: startup recovery scans pending rows and
-- drives each to a terminal state exactly once.
ALTER TABLE messages ADD COLUMN extraction_state TEXT NOT NULL DEFAULT 'pending'
    CHECK (extraction_state IN ('pending', 'success', 'failed'));

-- Backfill keeps the meaning v1 already gave the column: schema.sql
-- documents a NULL extraction as "the extractor panicked on this
-- message", which is the terminal failed state, and a present extraction
-- as a success. No existing row becomes pending, so recovery never
-- rescans mail that predates this migration.
UPDATE messages SET extraction_state =
    CASE WHEN extracted_json IS NULL THEN 'failed' ELSE 'success' END;

-- Recovery reads pending rows oldest first. The partial index keeps that
-- scan off the full table, which matters because it runs at startup while
-- the SMTP listener is already accepting mail.
CREATE INDEX messages_extraction_pending ON messages (received_at)
    WHERE extraction_state = 'pending';
