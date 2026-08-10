-- AuthStunt schema v1, per Brief section 5.
-- Text ids are short random hex generated in Go (public API contract).
-- Timestamps are RFC 3339 UTC text written by the store layer.

CREATE TABLE projects (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
) STRICT;

CREATE TABLE personas (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    email           TEXT NOT NULL COLLATE NOCASE UNIQUE,
    password_enc    BLOB NOT NULL,
    role            TEXT NOT NULL DEFAULT '',
    traits_json     TEXT NOT NULL DEFAULT '{}',
    seed_state      TEXT NOT NULL DEFAULT 'pending'
        CHECK (seed_state IN ('pending', 'seeded', 'failed', 'orphaned')),
    seed_output_ref TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (project_id, name)
) STRICT;

CREATE TABLE persona_relations (
    project_id   TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    from_persona TEXT NOT NULL REFERENCES personas (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    to_persona   TEXT NOT NULL REFERENCES personas (id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, from_persona, kind, to_persona)
) STRICT, WITHOUT ROWID;

CREATE TABLE secrets (
    persona_id TEXT NOT NULL REFERENCES personas (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    value_enc  BLOB NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (persona_id, kind)
) STRICT, WITHOUT ROWID;

CREATE TABLE sessions (
    persona_id      TEXT NOT NULL REFERENCES personas (id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    blob_ref        TEXT NOT NULL,
    saved_at        TEXT NOT NULL,
    hint_expires_at TEXT,
    PRIMARY KEY (persona_id, name)
) STRICT, WITHOUT ROWID;

CREATE TABLE messages (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    from_addr      TEXT NOT NULL,
    subject        TEXT NOT NULL,
    channel        TEXT NOT NULL CHECK (channel IN ('email', 'sms')),
    raw_ref        TEXT,
    html_ref       TEXT,
    -- Sealed: the body and the extraction result carry the OTPs and magic
    -- links themselves. Encrypting the raw mail blob but leaving these in
    -- plaintext beside it would protect nothing. Neither column is ever
    -- filtered in SQL (subject matching runs on subject), so sealing them
    -- costs no query capability. NULL extraction means the extractor
    -- panicked on this message.
    text_body      BLOB NOT NULL,
    extracted_json BLOB,
    quarantined    INTEGER NOT NULL DEFAULT 0 CHECK (quarantined IN (0, 1)),
    received_at    TEXT NOT NULL
) STRICT;

CREATE INDEX messages_project_received ON messages (project_id, received_at DESC);

CREATE TABLE message_recipients (
    message_id TEXT NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    addr       TEXT NOT NULL COLLATE NOCASE,
    kind       TEXT NOT NULL CHECK (kind IN ('to', 'cc', 'bcc', 'envelope')),
    PRIMARY KEY (message_id, addr, kind)
) STRICT, WITHOUT ROWID;

-- Matching (list, wait, MCP) runs on kind='envelope' rows only.
CREATE INDEX message_recipients_addr ON message_recipients (addr, kind);

-- The ledger is append-only and deliberately carries no foreign key to
-- projects: an audit trail that a delete can erase is not an audit trail.
-- Rows for a removed project stay behind as orphans on purpose.
CREATE TABLE ledger (
    id          INTEGER PRIMARY KEY,
    project_id  TEXT NOT NULL,
    ts          TEXT NOT NULL,
    actor       TEXT NOT NULL
        CHECK (actor IN ('mcp', 'rest', 'fixture', 'ui', 'system')),
    run_id      TEXT NOT NULL DEFAULT '',
    persona_id  TEXT,
    action      TEXT NOT NULL,
    detail_json TEXT NOT NULL DEFAULT '{}'
) STRICT;

CREATE INDEX ledger_project_ts ON ledger (project_id, ts);
CREATE INDEX ledger_run ON ledger (run_id) WHERE run_id <> '';
CREATE INDEX ledger_persona_ts ON ledger (persona_id, ts) WHERE persona_id IS NOT NULL;

-- ord preserves the order the patterns were declared in authstunt.yaml.
-- Persona emails are generated under the first one, so the order carries
-- meaning and cannot be recovered by sorting the patterns themselves.
CREATE TABLE allowlist (
    project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    pattern    TEXT NOT NULL COLLATE NOCASE,
    ord        INTEGER NOT NULL,
    PRIMARY KEY (project_id, pattern)
) STRICT, WITHOUT ROWID;
