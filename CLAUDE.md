# AuthStunt - session rules

Development on this project uses AI assistance. This file is the standard that
work is held to, whoever or whatever produces it. It is tracked deliberately,
so the bar is public and checkable rather than implied.

## Quality bar: no MVP thinking, ever

Built once, correctly, as a flagship.

- Go: golangci-lint v2 with errcheck, govet, staticcheck, revive, gosec,
  misspell, and depguard banning gopkg.in/yaml.v2 and gopkg.in/yaml.v3 in
  favor of github.com/goccy/go-yaml. Formatters: gofmt and goimports.
  Every test run uses `-race`. A `//nolint` requires a justification comment.
- TypeScript (P2, not yet written): when `packages/` ships it will use
  `strict` plus `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`,
  `verbatimModuleSyntax`, and `isolatedDeclarations`; typescript-eslint at
  strict-type-checked; publint and arethetypeswrong gating them in CI.
- CI is law: a red pipeline stops feature work until green. Conventional
  commits, small commits, a git tag at every phase boundary.
- No silent stubs: any temporary shortcut carries a loud `STUB(p<n>):`
  comment. No skipped or commented-out tests.
- Docs are part of done: README and the relevant doc sections update in the
  same commit as the behavior they describe. House style: no em or en dashes
  (plain hyphen), straight quotes, sentence case.

## Development

Go 1.26.x. These are the commands CI runs. Run them locally before pushing.

- `go test -race ./...`
- `golangci-lint run` (pinned to v2.12.2)
- `go build ./...` across GOOS linux, darwin, windows and GOARCH amd64,
  arm64 with `CGO_ENABLED=0`
- `goreleaser check`

Benchmarks are reported, never gating:
`go test -run '^$' -bench . -benchtime 10x ./...`. They exist so the storage
choices stay falsifiable, not to fail a build on a noisy runner.

Phase rhythm: plan, build with continuous verification, report the phase
against its acceptance criteria, then wait for the owner's go on the next
phase.
