#!/bin/bash
# Merge the per-shard outputs of 20-fetch-corpus.sh into one corpus-shas.tsv.
#
# The merge is not a formality. Sharding introduces exactly one new way to end up with a quietly
# short corpus: a shard that died without finishing. So this script does not simply concatenate.
# It checks that every repository in the source list appears exactly once with status OK, and exits
# non-zero otherwise. Scripts 21 and 22 read the merged file and refuse to count if anything in it
# is not OK, so a dead shard cannot become a low number.
set -uo pipefail

KIT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="${CORPUS_DIR:?set CORPUS_DIR}"
LIST="${CORPUS_LIST:-$KIT/data/remain.tsv}"
OUT="$WORK/corpus-shas.tsv"

python3 - "$LIST" "$OUT" "$WORK" <<'PY'
import sys, os, glob, collections
listp, outp, work = sys.argv[1], sys.argv[2], sys.argv[3]
want = [l.split('\t')[0] for l in open(listp) if l.strip()]

rows = []
for f in sorted(glob.glob(os.path.join(work, 'corpus-shas.*.tsv'))):
    for l in open(f):
        p = l.rstrip('\n').split('\t')
        if len(p) >= 6 and p[0] != 'repo':
            rows.append(p)

# A repository may legitimately appear more than once: a failure, then a successful retry after the
# fetcher was fixed. The later OK supersedes the earlier failure. What may NOT happen is a
# repository with no OK row at all, and that is what this refuses on. Both are reported either way,
# so a retry is visible rather than silently swallowed.
by_repo = collections.defaultdict(list)
for r in rows:
    by_repo[r[0]].append(r)

final, retried, failed_only = {}, [], []
for repo, rs in by_repo.items():
    oks = [r for r in rs if r[5] == 'OK']
    if oks:
        final[repo] = oks[-1]
        if len(rs) > len(oks):
            retried.append((repo, [r[5] for r in rs if r[5] != 'OK']))
    else:
        failed_only.append((repo, [r[5] for r in rs]))

with open(outp, 'w') as fh:
    fh.write('repo\tsha\tcommit_date\tfiles_on_disk\tfiles_in_tree\tstatus\n')
    for repo in sorted(final):
        fh.write('\t'.join(final[repo]) + '\n')

missing = [r for r in want if r not in final]
print(f'source list      : {len(want)}')
print(f'repositories seen: {len(by_repo)}')
print(f'status OK        : {len(final)}')
if retried:
    print(f'retried and now OK: {len(retried)}')
    for repo, sts in retried[:10]:
        print(f'    {repo}  earlier: {",".join(sts)}  -> OK')
if failed_only:
    print(f'FAILED, no OK row: {len(failed_only)} -> {failed_only[:10]}')
if missing:
    print(f'MISSING ENTIRELY : {len(missing)} -> {missing[:10]}')
    print('  (a shard probably died before finishing its slice - re-run that shard)')
if missing or failed_only:
    print()
    print('CORPUS INCOMPLETE. Scripts 21 and 22 will refuse to run against this.')
    sys.exit(1)
print()
print('corpus complete: every repository has a length-verified row.')
PY
rc=$?
echo "merged -> $OUT"
exit $rc
