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

// Extraction states a message moves through, per design 4.2 item 6. A row
// is committed pending, and exactly one terminal state follows: success,
// or failed with a NULL extraction. Startup recovery scans pending rows
// only, so a failed row is never retried forever.
const (
	ExtractionPending = "pending"
	ExtractionSuccess = "success"
	ExtractionFailed  = "failed"
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
	// ExtractionState is owned by the store: InsertMessage derives it from
	// the payload and SetExtraction or FailExtraction move it on. Setting
	// it on the way in has no effect, because a state that disagreed with
	// the stored extraction would send recovery after a message that has
	// already been extracted, or leave one waiting forever.
	ExtractionState string
	// Unreadable marks a row whose sealed body or extraction failed
	// authentication, per design 4.2 item 8. In a listing the metadata is
	// still returned with both payloads omitted; a by-id read fails with
	// ErrUnreadableMessage instead.
	Unreadable bool
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

// Run states. A run is one test execution, and only an active one may
// acquire leases or claim secrets.
const (
	RunActive   = "active"
	RunComplete = "complete"
	RunFailed   = "failed"
	RunExpired  = "expired"
)

// Identity modes. Ephemeral mints a fresh address per run; pooled grants
// exclusive use of a pre-provisioned account.
const (
	ModeEphemeral = "ephemeral"
	ModePooled    = "pooled"
)

// Lease states. Only held occupies an identity, which is what the partial
// unique index keys on.
const (
	LeaseHeld     = "held"
	LeaseReleased = "released"
	LeaseExpired  = "expired"
	LeaseFailed   = "failed"
)

// Seed states a lease moves through. Pending is internal and fails
// closed: it is never claimable and a successful acquire never returns it.
const (
	SeedStatePending = "pending"
	SeedStateSeeded  = "seeded"
	SeedStateFailed  = "failed"
	SeedStateSkipped = "skipped"
)

// Run is one test execution.
//
// CheckpointAt is the run's own lower bound on message freshness, stamped
// at create before the browser acts. Every claim is filtered at or after
// it, and the server owns it: a client-supplied bound would be set too
// late and by the party with a reason to widen it.
type Run struct {
	ID           string
	ProjectID    string
	State        string
	FailReason   string
	CheckpointAt time.Time
	StartedAt    time.Time
	ExpiresAt    time.Time
	EndedAt      time.Time
}

// Active reports whether the run may still acquire and claim.
func (r Run) Active() bool { return r.State == RunActive }

// Identity is one email address the product owns.
//
// PersonaID is set for a pooled identity, which points at the persona
// holding that account's credentials, and empty for an ephemeral one,
// which has no credentials before its run creates them.
type Identity struct {
	ID            string
	ProjectID     string
	PersonaID     string
	Addr          string
	Role          string
	Mode          string
	CooldownUntil time.Time
	CreatedAt     time.Time
}

// Suspicion values on a binding. The empty string is a clean binding and
// is the only one a claim will look at.
//
// The last two are the pooled handover guard. A timer can only say how
// long ago the previous run gave the address back; these say whether the
// message itself is older than the lease it resolved to, which is the
// question that decides whether late mail belongs to the run that is
// about to read it.
const (
	SuspectNone         = ""
	SuspectCooldown     = "cooldown"
	SuspectAfterRelease = "after_release"
	// SuspectPredatesLease marks a pooled binding whose message was
	// generated before its lease began. On a reused address that is the
	// previous holder's mail, however late it arrived.
	SuspectPredatesLease = "predates_lease"
	// SuspectOriginUnknown marks a pooled binding whose message carries no
	// usable origination time. Unprovable is refused rather than assumed
	// clean: this is the whole reason the guard cannot be bypassed by an
	// application that omits a header.
	SuspectOriginUnknown = "origin_unknown"
)

// Claim kinds.
const (
	ClaimEmailOTP  = "email_otp"
	ClaimMagicLink = "magic_link"
	ClaimTOTP      = "totp"
)

// Reason codes every claim outcome carries, frozen by the narrow plan's
// section 3.4. They become the machine-readable error codes when phase 4
// freezes the wire contract, so they are spelled here once and never
// shortened at a call site.
const (
	ReasonOK              = "claim_ok"
	ReasonTimeout         = "claim_timeout"
	ReasonNoBinding       = "claim_no_binding"
	ReasonStaleFiltered   = "claim_stale_filtered"
	ReasonSuspectBinding  = "claim_suspect_binding"
	ReasonAlreadyClaimed  = "claim_already_claimed"
	ReasonExpired         = "claim_expired"
	ReasonLeaseNotHeld    = "lease_not_held"
	ReasonLeaseSeedFailed = "lease_seed_failed"
	ReasonRunNotActive    = "run_not_active"
	ReasonExtractionFail  = "extraction_failed"
)

// Reason codes a lease acquisition is refused with.
//
// They are deliberately not members of the claim set above, which the
// narrow plan froze at eleven codes: these describe a pooled
// configuration that was never safe to use, which is a different
// question from what one claim found.
const (
	// ReasonPooledPolicyMissing refuses a pooled acquire made without the
	// operator declaring what the application's delivery behavior is.
	// Pooled mode reuses an address across runs, and there is no safe
	// default for how long the previous run's mail can still be in flight.
	ReasonPooledPolicyMissing = "pooled_policy_missing"
	// ReasonPooledCooldownBelowFloor refuses a cooldown shorter than the
	// declared delivery bound. It is a construction-time refusal: a
	// service that quietly raised the value instead would be deciding a
	// safety parameter on the operator's behalf and hiding it.
	ReasonPooledCooldownBelowFloor = "pooled_cooldown_below_floor"
)

// Binding records which lease owned a message's recipient address at the
// instant the message arrived.
//
// Suspect is evidence rather than an error: the binding is real and
// visible, and it is never claimable.
type Binding struct {
	MessageID  string
	LeaseID    string
	RunID      string
	IdentityID string
	Addr       string
	BoundAt    time.Time
	Suspect    string
}

// Clean reports whether a claim may consider this binding.
func (b Binding) Clean() bool { return b.Suspect == SuspectNone }

// Claim is one secret handed over once.
//
// It holds no secret value. The value is re-derived from the bound
// message every time, so a replay inside the TTL returns the same code
// without a second copy of it existing at rest.
type Claim struct {
	ID             string
	LeaseID        string
	RunID          string
	Kind           string
	MessageID      string
	IdempotencyKey string
	ClaimedAt      time.Time
	ExpiresAt      time.Time
}

// Lease is exclusive ownership of one identity by one run.
//
// Role is a snapshot taken at acquire rather than a join to the identity:
// an identity's role can change afterwards, and the evidence has to say
// what this run was actually granted.
type Lease struct {
	ID              string
	RunID           string
	IdentityID      string
	Role            string
	State           string
	SeedState       string
	SeedFingerprint string
	AcquiredAt      time.Time
	ExpiresAt       time.Time
	ReleasedAt      time.Time
	// InCooldown records that this lease was granted while the identity's
	// previous holder was still cooling down. It is decided at acquire and
	// never recomputed, because it is the reason every binding to this
	// lease is suspect: mail from the previous run may still be arriving.
	InCooldown bool
}

// Held reports whether the lease still occupies its identity.
func (l Lease) Held() bool { return l.State == LeaseHeld }

// Claimable reports whether a secret may be handed over against this
// lease. Both halves matter: a lease that lost its hold cannot claim, and
// neither can one whose seed never reached a confirmed state.
func (l Lease) Claimable() bool {
	return l.State == LeaseHeld &&
		(l.SeedState == SeedStateSeeded || l.SeedState == SeedStateSkipped)
}
