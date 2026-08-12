-- AuthStunt schema v4: message correlation and the credential claim.
-- Versions 1 through 3 are shipped and stay byte-stable; every change
-- lands here.
--
-- The contract these two tables exist to enforce, in one sentence: a
-- message belongs to the lease that exclusively owned its recipient
-- address at the instant it arrived, and the fresh secret inside it is
-- handed over exactly once.

-- Cooldown suspicion is decided once, at acquire, and never recomputed.
--
-- The alternative was to read identities.cooldown_until when a message
-- binds, and that column is overwritten by the next release: the same
-- binding would mean one thing today and another tomorrow. A lease that
-- was granted while the previous holder's cooldown was still running
-- carries the doubt for its whole life, which is what this column says.
ALTER TABLE leases ADD COLUMN acquired_in_cooldown INTEGER NOT NULL DEFAULT 0;

-- 1. A binding is the answer to "whose mail is this", written at ingest.
--
-- It is a table rather than a column on messages because one message can
-- reach two leased envelope recipients - a signup that copies an admin,
-- for one - and both runs are entitled to see it. A column would have to
-- pick one of them.
--
-- The row is inserted in the same transaction as the message, before the
-- SMTP client is told 250. There is deliberately no path that stores a
-- message now and attributes it later: that window is where a message
-- could be claimed by whoever asks first.
CREATE TABLE message_bindings (
    message_id  TEXT NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    lease_id    TEXT NOT NULL REFERENCES leases (id) ON DELETE CASCADE,
    run_id      TEXT NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    identity_id TEXT NOT NULL REFERENCES identities (id) ON DELETE CASCADE,
    -- The address is denormalized on purpose. Evidence has to say which
    -- address this was about even after the identity row is gone, and the
    -- claim path filters on it without a join.
    addr        TEXT NOT NULL COLLATE NOCASE,
    bound_at    TEXT NOT NULL,
    -- Suspicion is evidence, not an error. A suspect binding is visible
    -- and is never claimable: 'cooldown' means the address may still be
    -- carrying the previous run's mail, 'after_release' means this run had
    -- already given the address back when the message was bound.
    suspect     TEXT NOT NULL DEFAULT ''
        CHECK (suspect IN ('', 'cooldown', 'after_release')),
    PRIMARY KEY (message_id, lease_id)
) STRICT;

-- The claim path asks one question - what has bound to this lease, oldest
-- first - and asks it on every wakeup of a long poll.
CREATE INDEX message_bindings_lease ON message_bindings (lease_id, bound_at);

-- 2. A claim is one secret handed over once.
--
-- The row holds no secret value. The value is re-derived from the bound
-- message's sealed extraction every time it is read, so an idempotent
-- replay returns the same code without a second copy of that code
-- existing at rest, and retention that removes the message removes the
-- ability to replay it.
--
-- Only successful claims are recorded here. A stored failure under an
-- idempotency key would make a Playwright retry replay that failure
-- forever; every failed outcome is in the ledger instead, which is the
-- audit trail either way.
CREATE TABLE claims (
    id              TEXT PRIMARY KEY,
    lease_id        TEXT NOT NULL REFERENCES leases (id) ON DELETE CASCADE,
    run_id          TEXT NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    kind            TEXT NOT NULL
        CHECK (kind IN ('email_otp', 'magic_link', 'totp')),
    -- Null for a kind computed rather than read from mail. Nothing
    -- computes one yet; the column is nullable because the contract says
    -- totp will.
    message_id      TEXT REFERENCES messages (id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    claimed_at      TEXT NOT NULL,
    expires_at      TEXT NOT NULL
) STRICT;

-- Idempotency, enforced where a refactor cannot bypass it: the second
-- insert under one key loses to the index, and the loser reads back the
-- winner's row rather than minting a second claim.
CREATE UNIQUE INDEX claims_idem ON claims (lease_id, idempotency_key);

-- One-time. A message backs at most one claim of a kind, across every
-- lease and every run, so a duplicate delivery leaves the second copy
-- visible and unclaimed instead of backing a second handover.
CREATE UNIQUE INDEX claims_one_per_message_kind ON claims (message_id, kind)
    WHERE message_id IS NOT NULL;
