-- AuthStunt schema v6: the project bearer.
-- Versions 1 through 5 are shipped and stay byte-stable; every change
-- lands here.
--
-- Phase 4a needs a principal above the run: something that may create a
-- run in the first place, and read a project's evidence afterwards. A run
-- token cannot do it, because a run token is minted by the act it would
-- have to authorize.
--
-- The bearer lives on the project row rather than in a second file beside
-- the key, so it inherits the transaction, the backup and the rotation
-- story the rest of the database already has, and so it does not open a
-- second credential lifecycle while the Windows data-dir DACL is still an
-- open item.
--
-- Storage matches runs.token_hash exactly: a SHA-256 digest of 32 random
-- bytes, never the raw value, which is returned once at provision or
-- rotation and never again.
--
-- The column is nullable on purpose. NULL means "no bearer provisioned
-- yet", which is the state every database upgraded from v5 starts in and
-- a state the API must be able to tell apart from a wrong credential.
-- SQLite's ALTER TABLE cannot carry a UNIQUE constraint, so uniqueness is
-- a separate index; it treats NULLs as distinct, so any number of
-- projects may sit unprovisioned.
ALTER TABLE projects ADD COLUMN bearer_hash BLOB;

CREATE UNIQUE INDEX projects_bearer_hash ON projects (bearer_hash);
