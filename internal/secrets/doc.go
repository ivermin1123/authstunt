// Package secrets implements at-rest encryption for AuthStunt.
//
// One random 256-bit key per project lives as a file in the data
// directory. Every sensitive value (secrets table, persona passwords,
// mail blobs on disk) is sealed with AES-256-GCM into a self-describing
// container that embeds the key id, so keys can be rotated later without
// guessing which key sealed which value.
//
// The threat model is documented and narrow: this protects at-rest copies
// and backups. It does not protect against root on the same box.
package secrets
