# Column definitions

The TSV files have **no header row**. Column names below are taken from `COLS` in
`scripts/04-analyze.py`; the values are written by `scripts/03-measure.sh` (columns 1-18) and
`scripts/05-refine-d.py` (columns 19-21). Field letters `c`-`h` refer to `PREREG.md` R6.

## `fields.tsv` - 310 rows, 18 columns

The measurement sample, before the R12 dedup.

| # | Name | Meaning |
|---|---|---|
| 1 | `repo` | `owner/name` |
| 2 | `tier` | `team` or `vendor` (PREREG R3 - vendor-owned repositories are reported separately and are not in the main denominator) |
| 3 | `vendors` | IdP SDKs detected, `+`-joined |
| 4 | `fw` | `playwright`, `cypress`, or `playwright+cypress` |
| 5 | `workers` | **verbatim** matching lines from the config file, `path:line:text`, truncated to 220 chars. A `\|\|SERIALDECL\|\|` marker appends the first `mode:'serial'` declaration found in the tests. `ABSENT` if no `workers` line exists. |
| 6 | `fullypar` | verbatim `fullyParallel` line, or `ABSENT` |
| 7 | `d` | field d, raw: `YES` if `storageState` / `globalSetup` / `cy.session(` matched anywhere |
| 8 | `dlogin` | `YES` if a file involved in that setup also contains sign-in-shaped code (`signIn`, `login`, `password`, a labelled email/password fill) |
| 9 | `ddep` | `YES` if the config declares Playwright project `dependencies:` |
| 10 | `e` | `YES` if a vendor backdoor pattern matched (PREREG R6e) |
| 11 | `ekind` | which backdoor patterns matched, `;`-joined |
| 12 | `fkind` | mail-catcher / test-inbox products detected, `;`-joined (mailpit, mailhog, maildev, inbucket, mailosaur, mailtrap, ethereal) |
| 13 | `g` | `YES` if the test directory mentions signup / verification code / OTP / magic link |
| 14 | `h` | `YES` if a pain signal was found: a comment within 3 lines of a `workers` line, or a commit message on the config file matching flake/parallel/race/serial/concurrency |
| 15 | `ntest` | count of E2E test files (PREREG R9) |
| 16 | `nauth` | count of those that touch auth (PREREG R9) |
| 17 | `stars` | stars at collection time |
| 18 | `pushed` | `pushed_at` date |

## `fields2.tsv` - 284 rows, 21 columns

**This is the denominator used in the post.** Same as `fields.tsv` for columns 1-18, restricted to
the 284 repositories that survive the R12 dedup, plus three columns from `05-refine-d.py`:

| # | Name | Meaning |
|---|---|---|
| 19 | `dcode` | `YES` if at least one field-d hit is in a non-documentation file; `DOCONLY` if every hit is in `.md`/`.mdx`/`.txt`/`.rst`; `NO` if no hits. PREREG R11 puts the threshold on this column, not on column 7. |
| 20 | `dcfg` | `YES` if a field-d hit is in the Playwright/Cypress config file itself |
| 21 | `dsample` | up to 3 sample hits, ` \|\| `-joined, each truncated to 150 chars |

`fields3.tsv` in the working directory is byte-identical to `fields2.tsv` and is not shipped.

## Other files

| File | Rows | What |
|---|---|---|
| `repos-uniq.txt` | 3697 | The candidate population: every unique repository whose `package.json` contained both a hosted-IdP SDK and an E2E runner (PREREG R1). No star or "best match" ordering was used in any query. |
| `frame.txt` | 1188 | The sampling frame (PREREG R8): a census of all 193 repositories for the five small vendors, plus 250 random per vendor for the four large ones, fixed seed `20260816`. |
| `qualified.tsv` | 679 | The subset of the frame meeting R2 (not a fork, not archived, pushed within 18 months, an E2E config present, and for Firebase/Supabase an actual auth call). |
| `remain.tsv` | 284 | Collection metadata - `repo`, `tier`, `vendors`, `stars`, `pushed`, `branch`, `configs` - for a 284-row list that is **not the same 284 as `fields2.tsv`**. See the warning below. |
| `dropped-in-dedup.txt` | 26 | The repositories present in `fields.tsv` but not in `fields2.tsv`, removed by the R12 byte-identical-config rule. **Generated while building this kit** by diffing the two files, not an original artifact - the dedup command itself was not saved. |

## The chain, in numbers

```
3697  candidate population        repos-uniq.txt      PREREG R1
1188  sampling frame              frame.txt           PREREG R8  (census + seeded random)
 679  qualified                   qualified.tsv       PREREG R2
 310  measured                    fields.tsv          PREREG R10 (stratified measurement sample)
 284  after byte-identical-config dedup   fields2.tsv PREREG R12   <- the post's denominator
```

## Two files have 284 rows and they are not the same 284

`fields2.tsv` and `remain.tsv` both have 284 rows, and **25 repositories differ between them**. 259
are common. This is a trap, and it caught the author of this kit: the re-derivation scripts were
first pointed at `remain.tsv` for fetching while iterating `fields2.tsv` for counting, which would
have scanned 259 repositories and reported the result over a denominator of 284. The guard that
refuses to count a repository with no verified row is what stopped it, and it is the reason that
guard checks for a verified row rather than merely for a directory on disk.

**`fields2.tsv` is the denominator.** It is the file that reproduces `outputs/analysis-dedup.txt`
byte for byte, so every published figure rests on it. `remain.tsv` is collection metadata from an
earlier stage of the pipeline; it is shipped because it carries `branch` and `pushed_at`, which
nothing else does, and `pushed_at` is what makes the drift check possible for repositories with no
recorded SHA. Use it for those columns, not as the corpus.

If you re-run the fetch, drive it from the repository column of `fields2.tsv`.

## Provenance check, and one gap in it

`fields2.tsv` is verified against its own analysis output: running
`python3 scripts/04-analyze.py` in a directory containing `fields2.tsv` reproduces
`outputs/analysis-dedup.txt` **byte for byte**. The 284-row denominator and every per-vendor
breakdown in that file therefore stand on the shipped table, not on a claim about it.

The 310-row `fields.tsv` does **not** have the same guarantee. Running the same analyzer on it
reports `n=307`, while the shipped `outputs/analysis.txt` reports `n=306`. `analysis.txt` was
produced from an intermediate state of the working directory that was not preserved, so it cannot
be reproduced from anything in this kit. It is shipped anyway, labelled, rather than quietly
dropped - but treat it as a historical output, not as a checkable one. Nothing in the post depends
on it.

`fields2.tsv` is a strict subset of `fields.tsv` by repository name: all 284 appear in the 310, and
the 26 in `dropped-in-dedup.txt` are the difference.
