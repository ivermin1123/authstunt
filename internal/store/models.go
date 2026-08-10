package store

import "time"

// Seed states a persona moves through, per Brief 6.1.
const (
	SeedPending  = "pending"
	SeedSeeded   = "seeded"
	SeedFailed   = "failed"
	SeedOrphaned = "orphaned"
)

// Recipient kinds. Matching for list, wait, and MCP runs on
// RecipientEnvelope only: Bcc recipients never appear in headers.
const (
	RecipientTo       = "to"
	RecipientCC       = "cc"
	RecipientBCC      = "bcc"
	RecipientEnvelope = "envelope"
)

// Message channels.
const (
	ChannelEmail = "email"
	ChannelSMS   = "sms"
)

// Ledger actors.
const (
	ActorMCP     = "mcp"
	ActorREST    = "rest"
	ActorFixture = "fixture"
	ActorUI      = "ui"
	ActorSystem  = "system"
)

// SecretTOTPSeed is the secret kind holding a persona TOTP seed.
// Custom kinds carry a "custom:" prefix.
const SecretTOTPSeed = "totp_seed"

// Project is a workspace: one target application under test.
type Project struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// Persona is a test identity. PasswordEnc and every secret are sealed
// before they reach the store, which never sees a plaintext credential.
type Persona struct {
	ID            string
	ProjectID     string
	Name          string
	Email         string
	PasswordEnc   []byte
	Role          string
	TraitsJSON    string
	SeedState     string
	SeedOutputRef string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Relation records how two personas relate, so a flow can ask for "the
// approver of this user" instead of hardcoding one.
type Relation struct {
	ProjectID   string
	FromPersona string
	Kind        string
	ToPersona   string
}

// Secret is a sealed per-persona secret value.
type Secret struct {
	PersonaID string
	Kind      string
	ValueEnc  []byte
	CreatedAt time.Time
}

// Session is a saved Playwright storage state, so a repeat login can be
// skipped entirely.
type Session struct {
	PersonaID     string
	Name          string
	BlobRef       string
	SavedAt       time.Time
	HintExpiresAt time.Time
}

// Recipient is one address a message was addressed to. Matching runs on
// RecipientEnvelope entries only.
type Recipient struct {
	Addr string
	Kind string
}

// Message is a captured mail or SMS with its extraction result.
type Message struct {
	ID            string
	ProjectID     string
	FromAddr      string
	Subject       string
	Channel       string
	RawRef        string
	HTMLRef       string
	TextBody      string
	ExtractedJSON string
	Quarantined   bool
	ReceivedAt    time.Time
	Recipients    []Recipient
}

// LedgerEntry is one audit event. Every secret read writes one.
type LedgerEntry struct {
	ID         int64
	ProjectID  string
	TS         time.Time
	Actor      string
	RunID      string
	PersonaID  string
	Action     string
	DetailJSON string
}
