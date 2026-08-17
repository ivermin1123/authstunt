#!/bin/bash
# Independently re-verify the whole fetched corpus, after the fact.
#
# WHY THIS EXISTS AS A SEPARATE PASS
# 20-fetch-corpus.sh checks each repository as it downloads it. That check is only as trustworthy as
# the script that was actually executing at the time - and during this run, the fetcher was edited
# while six copies of it were live (to fix symlink counting). Editing a shell script that bash is
# still reading is unsafe in general: bash reads the file lazily, so a running copy can end up
# executing a mixture of old and new bytes.
#
# The honest response is not to reason about whether that mattered. It is to re-check the finished
# corpus with one known version of the logic, in one pass, against GitHub's own tree listing for the
# recorded SHA. This pass touches nothing and rewrites nothing; it only reports. If it exits 0, the
# corpus is complete under the current rule regardless of what the fetcher did or did not do.
#
# The same principle the fetcher is built on: the presence of an artifact does not prove the thing
# the artifact is supposed to prove.
set -uo pipefail

KIT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="${CORPUS_DIR:?set CORPUS_DIR}"
SRC="$WORK/src"
SHAS="$WORK/corpus-shas.tsv"
[ -f "$SHAS" ] || { echo "$SHAS not found - run 20b-merge-shards.sh first"; exit 1; }

keep_re='\.(ts|tsx|js|jsx|mjs|cjs|json|ya?ml)$|(^|/)\.env'
drop_re='(^|/)(node_modules|\.git|dist|build|\.next|out|coverage|vendor|playwright-report|test-results)/|(package-lock\.json|yarn\.lock|pnpm-lock\.yaml)$|\.min\.js$|\.map$'
MAXBYTES=1000000

fail=0; n=0; bad=()
while IFS=$'\t' read -r repo sha cdate ondisk_rec intree_rec st; do
  [ "$repo" = "repo" ] && continue
  [ -z "${repo:-}" ] && continue
  n=$((n+1))
  dir="$SRC/${repo//\//_}"
  if [ ! -d "$dir" ]; then
    echo "MISSING DIRECTORY $repo"; bad+=("$repo:nodir"); fail=1; continue
  fi
  # recount on disk, current rule, symlinks included
  now=$(cd "$dir" && find . \( -type f -o -type l \) | sed 's|^\./||' | python3 -c "
import sys,re,os
keep=re.compile(r'''$keep_re'''); drop=re.compile(r'''$drop_re''')
n=0
for p in sys.stdin.read().splitlines():
    if drop.search(p) or not keep.search(p): continue
    try:
        if os.lstat(p).st_size > $MAXBYTES and not os.path.islink(p): continue
    except OSError: continue
    n+=1
print(n)
")
  tree=$(gh api "repos/$repo/git/trees/$sha?recursive=1" 2>/dev/null | python3 -c "
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
  if [ "$tree" = "TRUNCATED" ] || [ "$tree" = "ERR" ] || [ -z "$tree" ]; then
    echo "TREE UNREADABLE $repo ($tree)"; bad+=("$repo:tree"); fail=1; continue
  fi
  if [ "$now" -ne "$tree" ]; then
    echo "SHORT $repo :: $now on disk, $tree in tree (recorded $ondisk_rec/$intree_rec)"
    bad+=("$repo:short"); fail=1; continue
  fi
done < "$SHAS"

echo
echo "=============================================================="
echo "re-verified : $n repositories"
echo "complete    : $((n - ${#bad[@]}))"
echo "problems    : ${#bad[@]}"
[ "${#bad[@]}" -gt 0 ] && printf '  %s\n' "${bad[@]}"
[ "$fail" -eq 0 ] && echo "CORPUS VERIFIED COMPLETE under the current rule, independently of the fetcher."
echo "=============================================================="
exit $fail
