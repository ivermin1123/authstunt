// Package secrets implements at-rest encryption for AuthStunt.
//
// One random 256-bit key per project lives as a file in the data
// directory. Every sensitive value (secrets table, persona passwords,
// mail blobs on disk) is sealed with AES-256-GCM into a self-describing
// container that embeds the key id, so keys can be rotated later without
// guessing which key sealed which value.
//
// The threat model is narrow and worth stating plainly: this protects a
// copy of the database and blob files taken without the key, such as a
// backup that excludes it. It does not protect a copy of the whole data
// directory, because the key file lives there so that headless CI works
// without a keychain, and it does not protect against root on the box.
package secrets
