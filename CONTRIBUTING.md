# Contributing

Thanks for looking. This is a small project with a high bar, and the bar is
written down rather than implied.

## Before a large change

Open an issue first for anything beyond a bug fix or a doc correction. A design
that arrives as a finished pull request is expensive to turn down, and turning
it down is the likely outcome if it was never discussed. Small fixes need no
preamble.

Security problems do not go in issues or pull requests. See
[SECURITY.md](SECURITY.md).

## Build, test, lint

The same three commands CI runs. Run them locally before pushing:

```
go build ./...
go test -race ./...
golangci-lint run
```

Every test run uses `-race`. The lint config is `.golangci.yml`, pinned to
golangci-lint v2. A `//nolint` needs a comment saying why. The toolchain comes
from `go.mod`, which is also what CI installs, so a version bump moves the
module and every job at once.

Two more gates run in CI and are worth knowing about, though neither usually
needs running by hand:

```
govulncheck ./...
```

That scans the module against the Go vulnerability database, on every pull
request and again once a week, because the database moves when the code does
not.

```
go build -cover -o /tmp/authstunt-cover ./cmd/authstunt
AUTHSTUNT_COVER_BIN=/tmp/authstunt-cover GOCOVERDIR=/tmp/covdata \
  go test -count=1 ./cmd/authstunt/
go tool covdata percent -i=/tmp/covdata
```

That is the coverage floor under `cmd/authstunt`. The package's tests drive the
real binary as a child process, so ordinary `go test -cover` sees none of it and
reports 0.0%; a binary built with `-cover` writes a profile as it exits instead.
The floor is set one point under the reading taken when the gate went in, and is
raised deliberately, never as a side effect.

The TypeScript packages each carry their own gate:

```
cd packages/client && npm run verify
cd packages/playwright && npm run verify
```

That type-checks, lints, builds, smoke-tests both module formats, and checks the
published shape with publint and arethetypeswrong before running the tests.

## What a change is expected to carry

- **A test that fails without it.** For a bug fix, the test should fail on the
  old code for the reason described, not for an unrelated reason.
- **No skipped or commented-out tests**, and no temporary shortcut without a
  loud `STUB(p<n>):` comment saying so.
- **Docs in the same commit as the behavior.** If a change alters what the
  README, `SECURITY.md` or a package doc comment describes, it updates them too.
- **Nothing new that is silently unimplemented.** If a surface accepts input it
  does not honor, say so where a caller will read it.

Public contract changes deserve their own conversation. The four frozen `/api/v1`
routes and the reason code vocabulary are append-only, and the rules are in
`internal/api/doc.go`.

## Commits

Conventional commits, scoped, and small:

```
fix(store): let the recovery read reach its partial index
docs(readme): quote the closed issue verbatim
```

The subject says what changed and, where it is not obvious, why. Commit
messages are part of the record this project keeps, so they are written for
somebody reading them a year from now.

## Review

Expect questions about evidence rather than style. The useful ones are usually:
what proves this works, what proves it did not break the thing next to it, and
what happens on the failure path. A change that answers those in its tests and
its commit message moves quickly.

CI must be green. A red pipeline stops feature work until it is fixed.

## Style

Go code is `gofmt` and `goimports` clean. Prose follows the house style used
throughout the docs: no em or en dashes, straight quotes, sentence case
headings.

## AI assistance

Development on this project uses AI assistance, under the quality bar documented
in [CLAUDE.md](CLAUDE.md). That file is tracked deliberately, so the standard is
public and checkable rather than implied. Contributions are held to it whoever
or whatever produced them: the tests, the lint gates and the review are the same
either way, and "an assistant wrote it" is neither a defense nor a disqualifier.

## License

By contributing you agree that your contribution is licensed under the MIT
license, the same as the rest of the project.
