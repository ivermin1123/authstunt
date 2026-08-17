#!/bin/bash
# Fallback transport for repositories whose tarball is impractical to download.
#
# WHY THIS EXISTS
# A few repositories in the corpus are enormous - 375MB and 512MB in this run - almost entirely
# because of assets the scanners discard anyway. Their tarballs do not finish inside any sane
# timeout, and a download that times out is a repository missing from the corpus, which is a
# quietly low number.
#
# The filtered content of those same repositories is tiny: 139 files and 1.3MB, 355 files and
# 2.2MB. So this script changes the transport and nothing else. It asks GitHub for the tree at the
# recorded SHA, filters to exactly the files 20-fetch-corpus.sh would have kept, and pulls each one
# individually from raw.githubusercontent.
#
# This is not a new idea and not a shortcut invented here: docs/verified-artifacts.md section 2
# records the original verification pass doing precisely this for a 243MB repository, for the same
# reason.
#
# The completeness guarantee is unchanged and is what matters: every filtered blob in the tree must
# arrive, each fetch is checked individually, and any shortfall fails the repository loudly. A
# truncated tree listing is a failure, because completeness would then be unprovable.
#
# Usage:  CORPUS_DIR=... ./20d-fetch-large.sh owner/repo [owner/repo ...]
set -uo pipefail

KIT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="${CORPUS_DIR:?set CORPUS_DIR}"
LIST="${CORPUS_LIST:-$KIT/data/remain.tsv}"
SRC="$WORK/src"; LOG="$WORK/fetch.large.log"
SHAS="$WORK/corpus-shas.large.tsv"
mkdir -p "$SRC"
: > "$LOG"
[ -f "$SHAS" ] || printf 'repo\tsha\tcommit_date\tfiles_on_disk\tfiles_in_tree\tstatus\n' > "$SHAS"

keep_re='\.(ts|tsx|js|jsx|mjs|cjs|json|ya?ml)$|(^|/)\.env'
drop_re='(^|/)(node_modules|\.git|dist|build|\.next|out|coverage|vendor|playwright-report|test-results)/|(package-lock\.json|yarn\.lock|pnpm-lock\.yaml)$|\.min\.js$|\.map$'
MAXBYTES=1000000

fail=0
for repo in "$@"; do
  branch=$(awk -F'\t' -v r="$repo" '$1==r{print $6}' "$LIST" | head -1)
  meta=$(gh api "repos/$repo/commits/${branch:-HEAD}" --jq '.sha + "\t" + .commit.committer.date' 2>>"$LOG")
  sha=${meta%%$'\t'*}; cdate=${meta#*$'\t'}
  if [ -z "$sha" ]; then
    echo "FAIL $repo :: cannot resolve sha" | tee -a "$LOG"
    printf '%s\t-\t-\t0\t0\tFAIL_SHA\n' "$repo" >> "$SHAS"; fail=1; continue
  fi

  dir="$SRC/${repo//\//_}"
  rm -rf "$dir"; mkdir -p "$dir"

  # the file list, and the count that must be matched exactly
  gh api "repos/$repo/git/trees/$sha?recursive=1" 2>>"$LOG" > "$WORK/.tree.json"
  intree=$(python3 - "$WORK/.tree.json" "$keep_re" "$drop_re" "$MAXBYTES" <<'PY'
import sys, json, re
tree, keep_re, drop_re, maxb = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
keep, drop = re.compile(keep_re), re.compile(drop_re)
try: t = json.load(open(tree))
except Exception: print('ERR'); sys.exit()
if t.get('truncated'): print('TRUNCATED'); sys.exit()
paths = [e['path'] for e in t.get('tree', [])
         if e.get('type') == 'blob' and not drop.search(e['path'])
         and keep.search(e['path']) and e.get('size', 0) <= maxb]
open(tree + '.paths', 'w').write('\n'.join(paths))
print(len(paths))
PY
)
  if [ "$intree" = "TRUNCATED" ] || [ "$intree" = "ERR" ] || [ -z "$intree" ]; then
    echo "FAIL $repo :: tree unusable ($intree) - completeness unprovable" | tee -a "$LOG"
    printf '%s\t%s\t%s\t0\t-1\tFAIL_TREE\n' "$repo" "$sha" "$cdate" >> "$SHAS"; fail=1; continue
  fi

  # pull each file, checking each one
  got=0; missed=0
  while IFS= read -r p || [ -n "$p" ]; do
    [ -z "$p" ] && continue
    out="$dir/$p"; mkdir -p "$(dirname "$out")"
    if curl -fsSL --retry 3 --retry-delay 1 --max-time 60 \
         -o "$out" "https://raw.githubusercontent.com/$repo/$sha/$p" 2>>"$LOG"; then
      got=$((got+1))
    else
      echo "  missed $repo :: $p" >> "$LOG"; missed=$((missed+1)); rm -f "$out"
    fi
  # `|| [ -n "$p" ]`: read returns false on a final line with no trailing newline, which silently
  # drops the last file while reporting zero failures. The count check below caught exactly that.
  done < "$WORK/.tree.json.paths"

  if [ "$got" -ne "$intree" ]; then
    echo "FAIL $repo :: SHORT - $got of $intree files fetched ($missed failed)" | tee -a "$LOG"
    printf '%s\t%s\t%s\t%s\t%s\tFAIL_SHORT\n' "$repo" "$sha" "$cdate" "$got" "$intree" >> "$SHAS"
    fail=1; continue
  fi
  printf '%s\t%s\t%s\t%s\t%s\tOK\n' "$repo" "$sha" "$cdate" "$got" "$intree" >> "$SHAS"
  echo "$repo $sha $got/$intree files OK (per-file transport)" | tee -a "$LOG"
done
rm -f "$WORK/.tree.json" "$WORK/.tree.json.paths"
exit $fail
