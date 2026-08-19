# Reproduction kit - "Almost every hosted identity provider ships a backdoor for your tests"

This is the data, the scripts, the pre-registration, and the quote ledger behind that post. It is
published so that you can disagree with a specific judgment rather than with a summary.

**Measurement date: 2026-08-16.** Every repository in this corpus was downloaded, and every vendor
documentation page was read, on that date. All 284 repositories existed on that date.

> **Warning: public repositories change.** Every `path:line` and every SHA below is what was true on
> 2026-08-16. Files get renamed, lines move, repositories get deleted or made private. If a line
> number no longer matches, check the SHA - `https://github.com/<owner>/<repo>/blob/<sha>/<path>#L<line>`
> pins the exact state that was read. If a repository is gone, the finding is no longer checkable,
> and you should treat it as such.

## What is here

| Path | What it is |
|---|---|
| `PREREG.md` | The pre-registration, verbatim. **Read the table at the top first**: it records which rules were written before any data was seen and which three (R9, R11, R12) were written after seeing raw data. |
| `docs/verified-artifacts.md` | The quote ledger. Every quotation cleared for publication, with its verification status, plus the notes (`N-*`) recording where earlier drafts had it wrong. |
| `QUOTED-LINES.md` | Exactly the lines quoted in the post, each with `path:line`, the SHA it was read at, and the read date. |
| `data/` | The measurement tables. See `data/SCHEMA.md` for the column definitions - the TSV files have no header row. `data/ledger-shas.tsv` holds the 19 repository SHAs recorded on the measurement date, and is honest about the other 265. |
| `scripts/` | The measurement pipeline (01-11) and the independent re-derivations (20-24). See "Redactions" below. |
| `scripts/verdicts-inbox.tsv`, `scripts/exclusions-selfbuilt.tsv` | Every judgement a human made rather than a machine, one line each, kept outside the code so the difference is a file you can read. |
| `outputs/` | The machine outputs that back specific numbers in the post, including `pattern-freeze.txt` - hashes of the detection patterns, recorded before any count existed. |
| `REDACTIONS.md` | Everything that was changed relative to the working originals. |

## What is deliberately not here

- **`evidence/`** - the per-repository grep extracts produced by `03-measure.sh`. It is ~105MB and
  it is a derived artifact, not a source: it contains only what the script's patterns matched. It is
  the wrong thing to audit against. If you want to check a finding, check the repository at the SHA.
  This matters: during verification, a string was briefly recorded as "not found" because it was
  absent from `evidence/`, and it turned out to be present in the actual source. Absence from a grep
  extract is not absence from a repository.
- **`trees/`** - cached GitHub file-tree responses. Same reason.
- The internal analysis report and the product roadmap. Neither is needed to check the numbers.

## `docs/verified-artifacts.md` in English

That file is in Vietnamese and is not translated here. This section says what it is and lists every
error it records, so a non-Vietnamese reader can still see what was found and go check any entry.

It is a quote ledger, written during a verification session on 2026-08-16 whose only job was to
re-open every quotation an earlier research pass had produced and decide whether it could be
published. 78 items were checked: **60 verified verbatim, 17 found to be off in some way and usable
only after correction, 1 not found at its cited source and banned outright.** Each item carries the
quotation, its source, `path:line`, the SHA it was read at, and a status. Corrections are numbered
`N-1` to `N-20`.

Its first rule is the one worth stealing: *a grep extract can prove presence, never absence.* The
session had already recorded one string as "not found" on the strength of its absence from an
extract, and was wrong - the string was in the real source.

| Note | What was wrong |
|---|---|
| N-1 | A file path was missing its monorepo prefix (`e2e/global.setup.ts` for `apps/web/e2e/global.setup.ts`). Content matched exactly. |
| N-2 | A quoted line range was wrong: the comment block runs 120-127, not 117-124. |
| N-3 | An off-by-one line number; the quoted sentence is on line 34, not 33. |
| N-4 | Another off-by-one, plus a worse problem: the line *after* the quoted one said the test uses the vendor backdoor. Quoting only the first line changed what it meant. A second line number in the same repo was also wrong (:77 for :80). |
| N-5 | The session's own near-miss, written up as a lesson: a string was declared "not found" because it was absent from a grep extract. Re-scanning actual source found it. Absence from a derived artifact is not absence. |
| N-6 | Environment variable names had been shortened in the write-up. `BYPASS_AUTH`, `SKIP_AUTH` and `ALLOW_LOCAL_DEV_IDENTITY` appear in no repository; the real names are all prefixed. |
| N-7 | One of the "cannot automate this" repositories is blocked by **SMS** OTP, not email, and must not be grouped with the email cases silently. |
| N-8 | A count was wrong: the summary table said 5 repositories state they cannot automate it; the machine output of the same pass listed 3, and a rescan confirmed 3. The same table also contradicted itself about one repository. |
| N-9 | A vendor quotation was truncated in a way that dropped its condition. |
| N-10 | A vendor's "this is an insecure method" warning was attached to the wrong feature. It sits on static OTP codes, not on the mechanism that vendor recommends. |
| N-11 | A Firebase sentence presented as a quotation was a paraphrase. The real text is two separate sentences. |
| N-12 | Sandbox values were attributed to the wrong documentation URL. |
| N-13 | A documentation citation pointed at a section index rather than the page that carries the sentence. |
| N-14 | **The one banned item.** A sentence attributed to `supabase/supabase-js` `docs/TESTING.md` does not exist there - `grep` and a repository-wide code search both return nothing. It may not be published in any form, including paraphrase. |
| N-15 | The "8 of 10 providers ship an official backdoor" count survives, but Supabase's supporting evidence had to be replaced after N-14 removed the original. |
| N-16 | A workaround from a GitHub issue had been promoted from what its author called a stopgap into "the community fix", displacing the primary suggestion in the same comment. |
| N-17 | A raw session transcript central to a claim had never been committed anywhere, so the claim rested on a retelling. Resolved: redacted, published, 7 of 7 checks passed. |
| N-18 | Concerns internal AuthStunt material not used in this post: two fragments were reported as separate when they are in one sentence. |
| N-19 | Concerns internal AuthStunt material not used in this post: a line count was 909 where a recount gives 911. |
| N-20 | Concerns internal AuthStunt material not used in this post: a result table had been described as stronger evidence than it is. It is a self-declared table, not a log, and the probe that produced it was deleted, so it cannot be re-checked. |

Two further corrections live in the body rather than in a numbered note. Section 2b records three
repositories the earlier keyword list missed and one false positive the verifier's own scan
produced. Section 5 records a claim about exclusivity that was re-measured on a live instance and
came back the other way, changing the conclusion.

Sections worth opening even without Vietnamese, because the tables and the `path:line` values read
in any language: section 1 (repository artifacts), section 2 (the two spine numbers and how they
were re-derived), section 3 (vendor documentation quotes), and section 7 (the list of material
banned from publication and why).

## Which numbers can you actually re-run

Being precise about this, because the post makes claims of two different strengths.

| Claim in the post | Status | Where |
|---|---|---|
| 31/284 build their own backdoor (the conservative figure) | **Script-reproducible** | `scripts/10-selfbuilt-bypass.py` over `data/fields2.tsv` → `outputs/round2-selfbuilt-bypass.txt` |
| 3 repositories state they cannot automate it | **Script-reproducible** | `scripts/08-verify-g-independent.py` → `outputs/round2-verify-g-independent.txt` |
| The hand check that self-reported 17 of 20 correct | **Reproducible as a record** | `outputs/manual20.tsv`, `outputs/manual20-dump.txt` |
| The corpus of 284, and the sampling frame it came from | **Script-reproducible** | `scripts/01-collect.sh`, `02-filter.sh`, `03-measure.sh`; `data/repos-uniq.txt` → `data/frame.txt` → `data/qualified.tsv` → `data/fields.tsv` → `data/fields2.tsv` |
| The per-vendor breakdown over the 284 | **Verified in place**: running `scripts/04-analyze.py` on the shipped `data/fields2.tsv` reproduces `outputs/analysis-dedup.txt` byte for byte | `data/SCHEMA.md`, "Provenance check" |
| **35/284** build their own backdoor - superseding the ledger's 34 | **Script-reproducible**, and re-run on 2026-08-17 | `scripts/21-selfbuilt-bypass-v2.py` → `outputs/selfbuilt-bypass-v2.txt`, attribution in `outputs/divergence-classification.txt` |
| 1/284 consumes a real code, confirmed by an independent inbox-capability scan | **Script-reproducible** at stages 2 and 3; stage 1 differs, see below | `scripts/22-inbox-capability.py` → `outputs/inbox-capability.txt` |
| The download audit (2 silently truncated repositories, refetched) | **Ledger-attested only** | `docs/verified-artifacts.md` §2. |

### What the 2026-08-17 re-run actually found

Both figures were rebuilt from source on 2026-08-17, against a freshly downloaded, length-verified
corpus. Neither was taken on the ledger's word, and one of them moved.

**Self-built backdoor: 31 → 34 → 35.**

| | count | what it is |
|---|---|---|
| DF-4, published | 31/284 | old corpus, old logic |
| Control: new corpus, this kit's patterns, pre-change traversal | 31/284 | **same total, different membership** - see below |
| Ledger's verification pass | 34/284 | DF-4's 31 plus three it had missed |
| **This re-run** | **35/284** | the ledger's 34 plus one more |

**The control's "+0" is the most misleading number on this page, so read it carefully.** DF-4 and
the control both total 31, and they are not the same 31: four repositories entered and four left,
so the identity of eight changed while the count sat still. Reporting the net alone would have said
"nothing moved". What actually happened is that the control's line-by-line traversal loses four
multi-line forged cookies DF-4 caught, and its patterns gain four DF-4's keyword list could not
express. The two errors happen to cancel. `outputs/divergence-classification.txt` prints the churn
and names all eight, because a cancelled pair of errors is still two errors.

The re-run's set is a strict superset of DF-4's 31: nothing DF-4 found failed to reappear. The
three additions the ledger already documented - `ubcdiscovery/ubc-discovery`, `kil-dev/kil.dev`,
`SDG-AI-Lab/Digital_Technologies_Radar` - all reappeared independently.

The thirty-fifth is new. `sefi-uzan/yanshuf-ai` keeps a hardcoded `next-auth.session-token` JWT in
`tests/e2e/fixtures/config.ts:8-13` and injects it with `addCookies(userCookies)` at
`tests/e2e/pages/website.ts:13` (SHA `26638529ef61e906e8a61d24edc8d1176d5023d3`). Both earlier
passes missed it for the same structural reason `kil-dev/kil.dev` was missed: the argument to
`addCookies` is a variable, so there is no string literal next to the call for a regex to match.
Finding it is what the re-run was for.

Attribution of the +4 over the control is measured, not estimated: all four are multi-line forged
cookies that only whole-text matching can see, listed with `path:line` in
`outputs/divergence-classification.txt`. Drift contributed zero.

**The inbox funnel: 15 → 6 → 1, against the ledger's 43 → 6 → 1.**

Stage 1 is much narrower here - 15 repositories with any inbox capability, against 43. Stages 2 and
3 land on exactly the same repositories: the same six candidates, and the same single confirmed
case, `stytchauth/stytch-browser` at the SHA the ledger recorded.

That is convergence, not a failed reproduction. Two nets of very different mesh were dragged
through the same 284 repositories and brought up the same six fish. The 28 extra candidates the
wider net caught all fall away at stage 2, which is what a necessary-but-not-sufficient condition
is supposed to do. The narrower stage 1 is worth knowing about, and it is stated rather than
smoothed over, but it does not weaken the "1 of 284" - it independently corroborates it.

## The serial-pinning column, and why no headline came out of it

`fields2.tsv` column 5 records, verbatim, every `workers` line found in each repository's Playwright
or Cypress config, and column 6 does the same for `fullyParallel`. A `||SERIALDECL||` marker appends
any suite-level `mode: 'serial'` declaration found in the tests. `PREREG.md` R6c defines "pinned to
serial" as `workers: 1` or `fullyParallel: false` or a suite-scope `mode: 'serial'`, counting the CI
branch where the value is branched on `process.env.CI`.

You can compute a rate from that column in about four lines, so here are the counts, and why we did
not put any of them in the post.

| | count |
|---|---|
| Pinned to serial under R6c's own reading (take the CI branch) | **152** of 284 |
| ...of which carry this **exact** line and nothing else | **96** |
| Pinned when the conditional CI branch is not counted (unconditional pins only) | **61** of 284 |

```ts
workers: process.env.CI ? 1 : undefined,
```

That is the line `npm init playwright` writes into a fresh config. Nobody chose it. Counting it as
evidence that a team hit a parallelism problem and retreated to one worker is counting a default as
a decision, and it is roughly two thirds of the larger figure.

So the measure moves from 152 to 61 on a single interpretation choice. There is also a third
number: the earlier research pass published this as 50.4%, and recounting from the shipped table
gives 152/284 = 53.5% - three points apart, which nobody has reconciled. A quantity that swings by
91 repositories on a definition and does not reproduce to within three points of its own earlier
report has no business carrying a headline.

The post therefore makes no claim about how many repositories pin serial. It quotes only individual
configs where the repository itself says why - `yravan/cashlens`, whose comment names the upstream
issue. One more example of what the raw column contains, from another repository in the corpus:

```
/* Use 1 worker on CI since we parallelize via shards (36 shards), not workers */
```

That is a documented reason with nothing to do with authentication. Counted mechanically, it would
have been indistinguishable from a team that gave up on parallel login.

The column stays in the published data, at full verbatim fidelity, and the counts are above rather
than left for you to discover. The weak thing here is the conclusion, not the data, so the
conclusion is what gets withheld.

## Known gaps

1. **The R12 dedup step has no script.** `data/fields.tsv` has 310 rows; `data/fields2.tsv` has 284.
   The 26 repositories removed in between are listed in `data/dropped-in-dedup.txt` (generated by
   diffing the two files while building this kit), and `PREREG.md` R12 defines the rule that removed
   them: repositories whose E2E config file was byte-identical to another repository's, keeping one
   per group in alphabetical order. The *result* is auditable - hash the config files yourself - but
   the exact command that produced it was not saved.
2. **The verification pass left prose, not files.** See the table above.
3. **`outputs/analysis.txt` cannot be reproduced from this kit.** It reports `n=306`; the shipped
   `fields.tsv` yields `n=307` through the same analyzer, because `analysis.txt` was written from an
   intermediate state of the working directory that was not preserved. It is shipped labelled rather
   than dropped. Nothing in the post depends on it. Its deduplicated sibling,
   `outputs/analysis-dedup.txt`, *does* reproduce byte for byte from `data/fields2.tsv`.
4. **`scripts/run-a.sh`, `run-b.sh`, `run-c.sh`** are near-identical shards of `03-measure.sh` used
   to parallelise the clone-and-measure step. They are included for completeness, not because they
   add anything.
5. **Only the 4-repository difference between 31 and 34 was opened by hand** during verification.
   The 31 shared repositories rest on the earlier hand check in `outputs/manual20.tsv`.

## Re-running the pipeline

Requirements: `bash`, `git`, `python3`, and the `gh` CLI authenticated (the measurement step calls
`gh api` for repository search and commit history).

### The original pipeline (scripts 01-11)

```sh
export DF4_DIR=/path/to/a/working/directory   # scripts read and write here
cp -r data "$DF4_DIR"/                         # seed with the published tables, or start from 01
scripts/01-collect.sh      # candidate search        -> repos-uniq.txt
scripts/02-filter.sh       # R2 qualification        -> qualified.tsv, measure.tsv
scripts/03-measure.sh      # clone + measure 9 fields -> fields.tsv, evidence/
python3 scripts/05-refine-d.py    # split field d into raw/code/cfg -> fields2.tsv
python3 scripts/04-analyze.py     # apply the criteria table
python3 scripts/10-selfbuilt-bypass.py
python3 scripts/08-verify-g-independent.py
```

### The independent re-derivations (scripts 20-22)

These rebuild the two figures that the verification session originally left as prose. They need
repository contents, so script 20 fetches them, pinning every download to a commit SHA it records.

```sh
export CORPUS_DIR=/path/with/a/few/GB/free

# fetch. SHARD_TOTAL/SHARD_INDEX run several copies at once; each writes its own shas file.
for i in 0 1 2 3 4 5; do SHARD_TOTAL=6 SHARD_INDEX=$i scripts/20-fetch-corpus.sh & done; wait
scripts/20b-merge-shards.sh     # refuses unless every repo appears exactly once, status OK
scripts/20c-verify-corpus.sh    # independent re-check of the finished corpus, one logic version

python3 scripts/24-pattern-selftest.py     # patterns, before trusting any count

# the count, and the control run that attributes any increase
SCAN_MODE=line   python3 scripts/21-selfbuilt-bypass-v2.py > outputs/selfbuilt-bypass-control.txt
SCAN_MODE=whole  python3 scripts/21-selfbuilt-bypass-v2.py > outputs/selfbuilt-bypass-v2.txt
CAP_MODE=legacy  python3 scripts/22-inbox-capability.py    > outputs/inbox-capability-control.txt
CAP_MODE=current python3 scripts/22-inbox-capability.py    > outputs/inbox-capability.txt
python3 scripts/23-classify-divergence.py  > outputs/divergence-classification.txt
```

**Why there is a control run.** Two of the declared changes to the re-derivation loosen detection,
so either can only raise the count. That makes any result above the ledger's 34 ambiguous between three
causes: repository drift, a genuine difference in measurement, and the loosening we introduced
ourselves. The control run is the old logic on the new corpus: it holds the corpus fixed and reverts
only the declared changes, which turns the third cause from an estimate into a measurement. Script
23 prints all the counts side by side, attributes each divergent repository to drift (with today's
SHA and whether it matches the one recorded on the measurement date) or to a named pattern (with
`path:line`). It exists to make the sentence "this much of the increase came from here" true rather
than plausible - not to offer a choice of numbers.

Script 20 is deliberately loud. The original download piped `curl` into `tar` with stderr swallowed,
so two repositories arrived truncated and nobody noticed until an audit. Script 20 checks the gzip
stream for integrity, then compares the extracted file count against GitHub's own tree listing for
the same commit under the same filter, and refuses to exit 0 if any repository is short, unresolved,
or has a tree listing the API declares truncated. Scripts 21 and 22 both refuse to print a count at
all unless every repository in the corpus fetched cleanly - a partial corpus can only produce a
quietly low number, which is the exact failure mode being guarded against.

Script 21's file-selection rule is copied verbatim from `10-selfbuilt-bypass.py`, so any difference
between the two is caused by what counts as a hit, not by which files were read. Script 22's
stage 3 is a human opening files; it is not automated, and the recorded verdicts live in
`scripts/verdicts-inbox.tsv` with one line per candidate. Script 21's single hand exclusion lives in
`scripts/exclusions-selfbuilt.tsv` for the same reason. Delete either file and the number changes,
visibly.

**Both re-derivations were written from a prose description and run once.** The patterns were not
adjusted until they matched the published figures. See "Which numbers can you actually re-run" for
what happened when they ran.

Expect drift. You are fetching today's repositories, not 2026-08-16's. Script 20 records the SHA it
read for every repository so you can tell drift from disagreement.

## One thing the pre-registration does not protect

`PREREG.md` fixes the decision thresholds before the data. It does **not** make the measurement
correct. The first measurement pass in this project produced two numbers that were later found to be
wrong, and both errors happened to point in the direction that favoured the product thesis. A
pre-registration protects the threshold; it does not protect the instrument. That is why this kit
ships the instrument.
