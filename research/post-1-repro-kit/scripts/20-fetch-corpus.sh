#!/bin/bash
# Fetch the 284-repository corpus for the re-derivation scripts (21, 22).
#
# WHY THIS EXISTS: the verification pass that produced 34/284 and the 43 -> 6 -> 1 chain left prose
# and no files. These scripts rebuild both numbers from scratch.
#
# WHY IT IS NOISY: the original download piped `curl` into `tar` and swallowed stderr, so two repos
# were silently truncated and every later count was quietly short. That is the failure this script
# is built to make impossible:
#   - curl is called with -f and --retry, and its stderr is kept
#   - the gzip stream is integrity-checked before anything is extracted
#   - the extracted file count is compared against GitHub's own tree listing for the same commit,
#     under the same filter
#   - a truncated tree response from the API is itself a failure
#   - any repo that fails is listed at the end and the script exits non-zero
# A short download cannot pass quietly. If you get a clean exit, every repo is complete.
#
# SHARDING: set SHARD_TOTAL and SHARD_INDEX (0-based) to run several copies at once. Each shard
# takes every Nth row of the list and writes its own corpus-shas.<i>.tsv, so concurrent shards never
# write the same file and there is no race to lose a row to. Completeness is checked per repository,
# so splitting the list weakens nothing: a short download in shard 4 fails shard 4 exactly as it
# would fail a single sequential run. Merge with 20b-merge-shards.sh, which re-checks the total.
#
# Requires: gh (authenticated), curl, tar, python3.
set -uo pipefail

KIT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="${CORPUS_DIR:?set CORPUS_DIR to a working directory with a few GB free}"
LIST="${CORPUS_LIST:-$KIT/data/remain.tsv}"
SHARD_TOTAL="${SHARD_TOTAL:-1}"
SHARD_INDEX="${SHARD_INDEX:-0}"
# SHARD_TAG names this run's output files. It defaults to the shard index, and exists so that a
# retry of a few repositories can write its own shas file instead of colliding with a shard that is
# still running. 20b-merge-shards.sh picks up every corpus-shas.<tag>.tsv and lets a later OK row
# supersede an earlier failure.
SHARD_TAG="${SHARD_TAG:-$SHARD_INDEX}"

SRC="$WORK/src"; TGZ="$WORK/tgz/$SHARD_TAG"; LOG="$WORK/fetch.$SHARD_TAG.log"
mkdir -p "$SRC" "$TGZ"
SHAS="$WORK/corpus-shas.$SHARD_TAG.tsv"
: > "$LOG"
# commit_date is recorded so that drift can be told apart from disagreement later: a repository
# whose HEAD commit is newer than the pushed_at recorded at collection time has moved since the
# measurement, and any change in its classification may be the repository's doing, not the scan's.
[ -f "$SHAS" ] || printf 'repo\tsha\tcommit_date\tfiles_on_disk\tfiles_in_tree\tstatus\n' > "$SHAS"

# Files the scanners care about. Both sides of the count use this same rule.
keep_re='\.(ts|tsx|js|jsx|mjs|cjs|json|ya?ml)$|(^|/)\.env'
drop_re='(^|/)(node_modules|\.git|dist|build|\.next|out|coverage|vendor|playwright-report|test-results)/|(package-lock\.json|yarn\.lock|pnpm-lock\.yaml)$|\.min\.js$|\.map$'
MAXBYTES=1000000   # skip generated bundles; applied identically to disk and tree

fail=0; failed_repos=()
SHARDLIST="$WORK/list.$SHARD_TAG.tsv"
awk -v i="$SHARD_INDEX" -v n="$SHARD_TOTAL" 'NF && (NR-1)%n==i' "$LIST" > "$SHARDLIST"
total=$(grep -c . "$SHARDLIST"); i=0
echo "shard $SHARD_INDEX/$SHARD_TOTAL (tag $SHARD_TAG) : $total repositories" >> "$LOG"

while IFS=$'\t' read -r repo tier vendors stars pushed branch cfgs; do
  [ -z "${repo:-}" ] && continue
  i=$((i+1))
  slug=${repo//\//_}
  dir="$SRC/$slug"

  if awk -F'\t' -v r="$repo" '$1==r && $6=="OK"{f=1} END{exit !f}' "$SHAS" && [ -d "$dir" ]; then
    echo "[$i/$total] $repo - already complete, skipping" >> "$LOG"; continue
  fi

  # --- resolve the commit we are about to read, and pin the download to it
  meta=$(gh api "repos/$repo/commits/${branch:-HEAD}" --jq '.sha + "\t" + .commit.committer.date' 2>>"$LOG")
  sha=${meta%%$'\t'*}; cdate=${meta#*$'\t'}
  if [ -z "$sha" ]; then
    echo "FAIL $repo :: cannot resolve commit sha for branch '${branch:-HEAD}'" | tee -a "$LOG"
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$repo" "-" "-" "0" "0" "FAIL_SHA" >> "$SHAS"
    fail=1; failed_repos+=("$repo"); continue
  fi

  # --- download, to disk, with errors visible
  tarball="$TGZ/$slug.tar.gz"
  if ! curl -fsSL --retry 4 --retry-delay 2 --retry-all-errors --max-time 900 \
        -o "$tarball" "https://codeload.github.com/$repo/tar.gz/$sha" 2>>"$LOG"; then
    echo "FAIL $repo :: download error (see $LOG)" | tee -a "$LOG"
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$repo" "$sha" "$cdate" "0" "0" "FAIL_DOWNLOAD" >> "$SHAS"
    fail=1; failed_repos+=("$repo"); rm -f "$tarball"; continue
  fi

  # --- the stream must be a complete gzip member before we trust a single byte of it
  if ! gzip -t "$tarball" 2>>"$LOG"; then
    echo "FAIL $repo :: truncated or corrupt gzip stream" | tee -a "$LOG"
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$repo" "$sha" "$cdate" "0" "0" "FAIL_GZIP" >> "$SHAS"
    fail=1; failed_repos+=("$repo"); rm -f "$tarball"; continue
  fi

  rm -rf "$dir"; mkdir -p "$dir"
  if ! tar -xzf "$tarball" -C "$dir" --strip-components=1 2>>"$LOG"; then
    echo "FAIL $repo :: extract error" | tee -a "$LOG"
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$repo" "$sha" "$cdate" "0" "0" "FAIL_EXTRACT" >> "$SHAS"
    fail=1; failed_repos+=("$repo"); rm -f "$tarball"; continue
  fi
  rm -f "$tarball"

  # --- prune to the files the scanners read, then count what survived
  # -type f OR -type l: git tracks symlinks and GitHub's tree API lists them as blobs (mode 120000),
  # so counting only regular files makes any repository containing a tracked symlink look short.
  # That is a false alarm, and a false alarm in a completeness check is expensive: it trains you to
  # wave the check through. Both sides of the comparison must count the same things.
  ondisk=$(cd "$dir" && find . \( -type f -o -type l \) | sed 's|^\./||' | python3 -c "
import sys,re,os
keep=re.compile(r'''$keep_re'''); drop=re.compile(r'''$drop_re''')
n=0
for p in sys.stdin.read().splitlines():
    if drop.search(p) or not keep.search(p):
        try: os.remove(p)
        except OSError: pass
        continue
    try:
        # lstat, not getsize: a symlink must be measured as itself, never followed. Following a
        # broken link raises and would silently drop the file from the count.
        if os.lstat(p).st_size > $MAXBYTES and not os.path.islink(p):
            os.remove(p); continue
    except OSError: continue
    n+=1
print(n)
")

  # --- independent count from GitHub's own tree listing, same filter, same commit
  intree=$(gh api "repos/$repo/git/trees/$sha?recursive=1" 2>>"$LOG" | python3 -c "
import sys,json,re
keep=re.compile(r'''$keep_re'''); drop=re.compile(r'''$drop_re''')
try: t=json.load(sys.stdin)
except Exception: print('ERR'); sys.exit()
if t.get('truncated'): print('TRUNCATED'); sys.exit()
n=0
for e in t.get('tree',[]):
    if e.get('type')!='blob': continue
    p=e['path']
    if drop.search(p) or not keep.search(p): continue
    if e.get('size',0) > $MAXBYTES: continue
    n+=1
print(n)
")

  if [ "$intree" = "TRUNCATED" ]; then
    # The API refused to list the whole tree. We cannot prove completeness, so we do not claim it.
    echo "FAIL $repo :: GitHub tree response was truncated - completeness unprovable" | tee -a "$LOG"
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$repo" "$sha" "$cdate" "$ondisk" "-1" "FAIL_TREE_TRUNCATED" >> "$SHAS"
    fail=1; failed_repos+=("$repo"); continue
  fi
  if [ "$intree" = "ERR" ] || [ -z "$intree" ]; then
    echo "FAIL $repo :: tree listing unreadable" | tee -a "$LOG"
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$repo" "$sha" "$cdate" "$ondisk" "-1" "FAIL_TREE" >> "$SHAS"
    fail=1; failed_repos+=("$repo"); continue
  fi
  if [ "$ondisk" -ne "$intree" ]; then
    echo "FAIL $repo :: SHORT EXTRACT - $ondisk files on disk, $intree in tree" | tee -a "$LOG"
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$repo" "$sha" "$cdate" "$ondisk" "$intree" "FAIL_SHORT" >> "$SHAS"
    fail=1; failed_repos+=("$repo"); continue
  fi

  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$repo" "$sha" "$cdate" "$ondisk" "$intree" "OK" >> "$SHAS"
  echo "[$i/$total] $repo $sha $ondisk files OK" >> "$LOG"
done < "$SHARDLIST"

ok=$(awk -F'\t' '$6=="OK"' "$SHAS" | wc -l | tr -d ' ')
echo
echo "=============================================================="
echo "fetched OK : $ok"
echo "failed     : ${#failed_repos[@]}"
if [ "${#failed_repos[@]}" -gt 0 ]; then
  printf '  %s\n' "${failed_repos[@]}"
  echo
  echo "REFUSING TO REPORT A NUMBER ON AN INCOMPLETE CORPUS."
  echo "Re-run this script; completed repos are skipped. Counts from scripts 21 and 22 are"
  echo "meaningless until this exits 0."
fi
echo "shas       : $SHAS"
echo "=============================================================="
exit $fail
