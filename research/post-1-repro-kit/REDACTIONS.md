# Redactions and generated files

Everything in this kit is a byte-for-byte copy of the working artifact, except for what is listed
here.

## Redacted: one line in each of seven shell scripts

Each of these scripts began with a hardcoded absolute path to the author's local working directory.
That path contained a machine account name and a session identifier, and it is environment, not
evidence.

| File | Line | Was | Now |
|---|---|---|---|
| `scripts/01-collect.sh` | 4 | `D=<absolute path under the author's local scratch directory>` | `D="${DF4_DIR:?set DF4_DIR to the df4 working directory (see README)}"` |
| `scripts/02-filter.sh` | 3 | same | same |
| `scripts/03-measure.sh` | 3 | same | same |
| `scripts/07-manual-verify.sh` | 4 | same | same |
| `scripts/run-a.sh` | 3 | same | same |
| `scripts/run-b.sh` | 3 | same | same |
| `scripts/run-c.sh` | 3 | same | same |

Exactly one line changed in each file and nothing else. The seven Python scripts derive their
working directory from their own location and were not touched.

The replacement is functional, not a placeholder: set `DF4_DIR` and the scripts run. It fails loudly
if the variable is unset, rather than silently writing to the wrong place.

Verification, on the kit as published:

```sh
# no local path survives anywhere in the kit
grep -r "claude-501\|/Users/\|/home/" . ; echo "exit=$?"   # expect: no matches

# exactly one changed line per shell script, none in the Python
for f in scripts/*; do diff "$ORIGINAL/$f" "$f"; done
```

## Generated for this kit, not an original artifact

| File | How it was made |
|---|---|
| `data/dropped-in-dedup.txt` | `comm -23` of the repository names in `fields.tsv` against those in `fields2.tsv`. 26 rows. It reconstructs *which* repositories the R12 dedup removed; the command that originally removed them was not saved. |
| `data/ledger-shas.tsv` | Transcribed from `docs/verified-artifacts.md`. 19 repositories - every one the ledger recorded a SHA for. The file states plainly that the other 265 have no recorded SHA, which is a gap in the original work, not something this kit can fix. |
| `scripts/20-fetch-corpus.sh` … `scripts/24-pattern-selftest.py` | Written for this kit, to rebuild the two figures the original verification pass left as prose. See `README.md`, "The independent re-derivations". |
| `scripts/verdicts-inbox.tsv`, `scripts/exclusions-selfbuilt.tsv` | The human judgements, transcribed from `docs/verified-artifacts.md` section 2a and 2b, one line per case. Kept as data rather than as code so that the boundary between what a machine found and what a person decided is a file you can read. |
| `outputs/pattern-freeze.txt` | Hashes of the detection patterns plus the full change log, written before any count existed. |
| `README.md`, `QUOTED-LINES.md`, `data/SCHEMA.md`, this file | Written for publication. `SCHEMA.md`'s column names are taken from `COLS` in `scripts/04-analyze.py`. |

## One hazard in how this kit was produced, recorded rather than hidden

`20-fetch-corpus.sh` was edited while six copies of it were already running, to fix the symlink
counting described in `outputs/pattern-freeze.txt`. Editing a shell script that bash is still
reading is unsafe: bash reads the file lazily, so a live copy can execute a mixture of old and new
bytes. The effect was visible in this run - some repositories containing tracked symlinks passed
while others failed the same check.

Rather than argue about whether it mattered, `scripts/20c-verify-corpus.sh` re-checks the finished
corpus in a single pass with one known version of the logic, against GitHub's tree listing for each
recorded SHA. Its result, not the fetcher's log, is what the completeness claim rests on.

## Not redacted, on purpose

- `PREREG.md` ships verbatim, including the table at the top that records R9, R11 and R12 as written
  after raw data was seen, and including its own cross-references to internal documents that are not
  part of this kit. Trimming it would defeat the point of publishing it.
- `docs/verified-artifacts.md` ships verbatim, including the notes recording where earlier drafts
  were wrong, the one quotation banned from publication as unfindable at its cited source, and the
  section listing what may not be published and why. It is written in Vietnamese.
- `outputs/analysis.txt` ships even though it cannot be reproduced from any shipped input. See
  `README.md`, "Known gaps".

## Excluded

`evidence/` (~105MB of per-repository grep extracts) and `trees/` (~37MB of cached file-tree
responses). Reasons in `README.md`. Both are derived from the repositories, which are the thing to
audit against.
