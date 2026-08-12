-- AuthStunt schema v5: the pooled handover guard.
-- Versions 1 through 4 are shipped and stay byte-stable; every change
-- lands here.
--
-- What v4 got wrong, stated plainly: a pooled address was protected from
-- the previous run's mail by a cooldown timer and nothing else. Set that
-- timer shorter than the application's real delivery latency and the
-- previous run's code binds to the next run *clean*, and the next run can
-- claim it. No invariant was broken; the window was simply open.
--
-- v5 widens the suspicion vocabulary so a binding can record the answer
-- to a question a timer cannot ask: was this message generated before the
-- lease it is about to bind to even started? A message that predates its
-- lease belongs to whoever held the address earlier, however late it
-- arrived.
--
-- 'predates_lease'  the message's own origination time is older than the
--                   lease it resolved to. On a pooled address that is the
--                   previous run's mail arriving late.
-- 'origin_unknown'  the message carries no usable origination time, so the
--                   question cannot be answered. Pooled mail fails closed:
--                   unprovable is refused, not assumed clean.
--
-- SQLite cannot widen a CHECK constraint in place, so the table is rebuilt
-- and its rows copied. Nothing references message_bindings, so the drop is
-- safe with foreign_keys on, and every existing suspicion value is still
-- legal under the new constraint.
CREATE TABLE message_bindings_v5 (
    message_id  TEXT NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    lease_id    TEXT NOT NULL REFERENCES leases (id) ON DELETE CASCADE,
    run_id      TEXT NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    identity_id TEXT NOT NULL REFERENCES identities (id) ON DELETE CASCADE,
    addr        TEXT NOT NULL COLLATE NOCASE,
    bound_at    TEXT NOT NULL,
    suspect     TEXT NOT NULL DEFAULT ''
        CHECK (suspect IN ('', 'cooldown', 'after_release',
                           'predates_lease', 'origin_unknown')),
    PRIMARY KEY (message_id, lease_id)
) STRICT;

INSERT INTO message_bindings_v5
    (message_id, lease_id, run_id, identity_id, addr, bound_at, suspect)
SELECT message_id, lease_id, run_id, identity_id, addr, bound_at, suspect
  FROM message_bindings;

DROP TABLE message_bindings;

ALTER TABLE message_bindings_v5 RENAME TO message_bindings;

CREATE INDEX message_bindings_lease ON message_bindings (lease_id, bound_at);
