-- AuthStunt schema v3: the run-scoped identity lease. schema.sql (v1) and
-- schema_v2.sql are shipped and stay byte-stable; every change lands here.
--
-- The contract these three tables exist to enforce, in one sentence: at
-- most one run may hold a given identity at a given instant, and the
-- record of who held what, when, survives the lease forever because
-- message correlation reads it.

-- 1. A run is one test execution: one Playwright worker, one CI job, one
-- agent session.
--
-- checkpoint_at is the reason a run exists as a row at all. It is stamped
-- at create, before the browser acts, and it is the earliest received time
-- any claim in this run may ever see. A client-supplied `since` could not
-- do this job: the client learns the time too late, and it is the party
-- with an incentive to widen the window.
CREATE TABLE runs (
    id            TEXT PRIMARY KEY,
    project_id    TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    -- The token is stored only as a SHA-256 digest of 32 random bytes.
    -- The raw value is returned once, at create, and never again: there is
    -- nothing here to leak into a log, a backup or a ledger detail.
    token_hash    BLOB NOT NULL UNIQUE,
    state         TEXT NOT NULL DEFAULT 'active'
        CHECK (state IN ('active', 'complete', 'failed', 'expired')),
    fail_reason   TEXT NOT NULL DEFAULT '',
    checkpoint_at TEXT NOT NULL,
    started_at    TEXT NOT NULL,
    expires_at    TEXT NOT NULL,
    ended_at      TEXT
) STRICT;

-- The sweeper asks one question - which active runs are past their expiry -
-- and asks it on a timer, so it gets its own partial index rather than a
-- scan of every run the project has ever recorded.
CREATE INDEX runs_active_expiry ON runs (expires_at) WHERE state = 'active';
CREATE INDEX runs_project ON runs (project_id, started_at);

-- 2. An identity is one email address the product owns.
--
-- persona_id deliberately carries NO cascade. A pooled identity points at
-- the persona holding that account's credentials, and deleting that
-- persona would take the identity and every lease ever granted on it with
-- it - which is the history correlation reads. SQLite's default NO ACTION
-- refuses the delete instead, which is the right answer: prune the pool
-- explicitly or not at all. Deleting the project still removes everything,
-- through project_id.
CREATE TABLE identities (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    persona_id     TEXT REFERENCES personas (id),
    addr           TEXT NOT NULL COLLATE NOCASE UNIQUE,
    role           TEXT NOT NULL DEFAULT '',
    mode           TEXT NOT NULL CHECK (mode IN ('ephemeral', 'pooled')),
    -- Set on a pooled identity when it is released, and when a seed fails
    -- half way. Until it passes, the identity is not leasable and any mail
    -- that binds to it is suspect: it may belong to the run that just
    -- finished.
    cooldown_until TEXT,
    created_at     TEXT NOT NULL
) STRICT;

-- Acquire in pooled mode looks for a free identity of a role. Ephemeral
-- rows are never selected by that query, so they are excluded from the
-- index rather than padding it: one is minted per run and they accumulate.
CREATE INDEX identities_pool_by_role ON identities (project_id, role)
    WHERE mode = 'pooled';
CREATE INDEX identities_persona ON identities (persona_id)
    WHERE persona_id IS NOT NULL;

-- 3. A lease is exclusive ownership of one identity by one run for a
-- bounded time.
--
-- role is a snapshot taken at acquire, not a join to the identity. An
-- identity's role can be changed afterwards; what this run was granted
-- cannot, and the evidence has to say what was true at the time.
--
-- seed_state starts at pending because the seed adapter runs outside this
-- row's first transaction. pending fails closed - it is not claimable, and
-- a successful acquire never returns it - so a crash mid-seed leaves a
-- lease that refuses to hand over a secret rather than one that pretends
-- the account is ready.
CREATE TABLE leases (
    id               TEXT PRIMARY KEY,
    run_id           TEXT NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    identity_id      TEXT NOT NULL REFERENCES identities (id) ON DELETE CASCADE,
    role             TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL DEFAULT 'held'
        CHECK (state IN ('held', 'released', 'expired', 'failed')),
    seed_state       TEXT NOT NULL DEFAULT 'pending'
        CHECK (seed_state IN ('pending', 'seeded', 'failed', 'skipped')),
    seed_fingerprint TEXT NOT NULL DEFAULT '',
    acquired_at      TEXT NOT NULL,
    expires_at       TEXT NOT NULL,
    released_at      TEXT
) STRICT;

-- Exclusivity, and the only place it is enforced. Application logic cannot
-- make this guarantee: two acquires can both read "free" before either
-- writes. Here the second INSERT fails at the SQL layer, which is not a
-- race that can be reasoned away.
CREATE UNIQUE INDEX leases_one_held_per_identity ON leases (identity_id)
    WHERE state = 'held';

CREATE INDEX leases_by_run ON leases (run_id);

-- Correlation asks, for one address and one receive time, which lease held
-- that identity then. That is an interval lookup keyed by identity and
-- ordered by acquire time, and it runs once per envelope recipient at
-- ingest, inside the transaction that answers the SMTP client.
CREATE INDEX leases_interval ON leases (identity_id, acquired_at);

-- The expiry sweeper's question, matching runs_active_expiry.
CREATE INDEX leases_held_expiry ON leases (expires_at) WHERE state = 'held';
