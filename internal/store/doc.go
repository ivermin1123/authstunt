// Package store owns the SQLite database and the encrypted blob store.
//
// Two database handles sit behind one Store: a single-connection write
// handle (the write executor, so writers never fight over the file lock)
// and a pooled read handle. WAL mode lets readers run while a write is in
// flight. Both handles set their PRAGMAs through the DSN, so every
// connection the pool opens is configured identically.
//
// Large payloads (raw and HTML mail, Playwright storage state) live as
// AES-256-GCM sealed files under <data-dir>/blobs; rows carry only the
// blob reference.
package store
