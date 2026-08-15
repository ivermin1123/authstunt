# Security policy

## Reporting a vulnerability

Email **security@authstunt.com**. Please do not open a public issue for a
security problem.

Include what you did, what happened, and what you expected. A minimal
reproduction is worth more than a severity rating. If a report needs a test
message, use an address under a domain you control rather than a real person's.

You will get a human reply. There is no PGP key published for this address; if
you need one before you can send, say so in a first message with no details in
it and one can be arranged.

## Supported versions

AuthStunt is pre-1.0 and only the latest release is supported. There are no
backported fixes to earlier tags.

## Threat model

The useful part of a threat model is the part that says what is *not* defended,
so that nobody builds on an assumption this project never made.

### In scope

- Anything that lets a caller read a message, a code, or a link belonging to a
  lease it does not hold.
- Anything that lets a run act outside its own scope, or that lets a run token
  do what only a project bearer should.
- Recovery of a project bearer from anything the server writes: the data
  directory, the audit ledger, log output, or evidence.
- Leaking a message body, an extracted code, or a link into a place that is not
  encrypted at rest.
- Escaping the domain allowlist: mail for an address outside it reaching the
  automated read path instead of quarantine.

### Out of scope, by design

**The host is trusted.** The HTTP API binds `127.0.0.1:8925` by default, and
loopback is the trust boundary: any process on the same machine that can reach
that port and holds the bearer is treated as authorized. There is no defense
against a hostile process on the test host, and adding one is not planned.

**SMTP accepts every credential.** The SMTP listener advertises `AUTH PLAIN`
and accepts whatever username and password it is given, including none. This is
deliberate: the job is to be the relay an application under test can always
reach, and rejecting an application's test credentials would only produce a
failure that teaches nothing. The consequence follows directly, and it is worth
stating: **any process that can reach the SMTP port can inject a message and
make a test pass for the wrong reason.** Run the server where you would run the
application under test, not somewhere untrusted parties can reach it.

Both of the above mean the same thing in practice. This is a test instrument,
not a production mail server. Do not expose either listener to a network you do
not control, and do not point a production system at it.

**Test data is not production data.** Personas, addresses and codes here exist
to be thrown away. Nothing in this project is designed to hold real user
credentials, and no part of it should be repurposed to.

## What the server does with secrets

**The project bearer is stored as a SHA-256 digest and nothing else.** The raw
value is printed once, at provision or rotation time, and is not recoverable
afterwards. It is never written into the data directory, never logged, and
never present in evidence. `serve` never creates one and never prints one,
because a long-running process puts its output into CI logs and log shippers.
Provisioning refuses a non-terminal destination unless you take responsibility
for it explicitly with `--out <path>` or `--allow-non-tty-reveal`. Rotation cuts
off the previous value immediately, and there is never more than one live
bearer for a project. The audit ledger records that a credential changed, never
which one.

**Run tokens are scoped to their run** and are what every call after run
creation uses, so the bearer itself does not travel with each request.

**Message bodies and extraction results are sealed at rest** with AES-256-GCM
under one key per project, generated when the data directory is initialized and
stored inside it. Protecting that key is protecting the mail.

## Durability of the SMTP ack

An SMTP `250` means the message is on disk. The database runs in WAL mode with
`synchronous=FULL` by default, so the commit backing the ack fsyncs the WAL and
the blobs are fsynced before it. The message survives the process dying and it
survives the machine losing power.

`serve --sync-mode=normal` is the one configuration where that is not true, and
it is a **known limitation of that mode only**: `250` then means durable across
a process crash, with power loss covered only as far as the last checkpoint. A
server started that way announces `sync normal` on its startup line so the trade
is recorded rather than hidden.

## Known limitations

- **`--sync-mode=normal` weakens the ack contract**, as described above. The
  default does not.
- **There is no encryption key rotation.** One key is generated per project and
  used for the life of the data directory. Replacing it today means starting a
  new data directory.
- **Nothing is ever deleted, including quarantined mail.** A message addressed
  to a recipient outside the allowlist is accepted, stored encrypted, and held
  back from the automated read path. It is kept as evidence, and there is no
  retention window, no cap, and no sweeper: it stays until the data directory
  is deleted. This matters because a staging system that copies a real customer
  address puts that person's mail on your disk. See "Status and stability" and
  "Operating it" in the [README](README.md) for the full description; this file
  does not restate the numbers so that there is one place they can be wrong.
- **Windows file permissions are weaker than on Unix.** The key file is
  protected by an ACL rather than mode bits.
