#!/usr/bin/env python3
"""Put the four self-built-backdoor counts side by side and attribute every difference to a cause.

WHY FOUR NUMBERS AND NOT ONE
    Two of the declared changes to the re-derivation (whole-text matching, and adding `mailinator`)
    both LOOSEN detection: each can only raise the count. So a result above the ledger's 34 has three
    possible causes that a single number cannot separate:

        (a) repository drift    - the corpus is a day younger than the measurement
        (b) genuine disagreement - the two instruments really do read the same code differently
        (c) the declared changes - the loosening we ourselves introduced

    Reporting one number and calling the excess "probably (b)" would be a guess dressed as a
    finding, in a post whose whole argument is that claims need receipts. So the control run exists:
    the OLD logic, on the NEW corpus. It holds the corpus constant and reverts only the declared
    changes, which turns (c) from an estimate into a measurement.

        1. DF-4        old corpus, old logic     the published 31
        2. CONTROL     new corpus, old logic     differs from 1 only by drift and by DF-4's own
                                                 file-selection, so 1 vs 2 isolates (a)
        3. MAIN        new corpus, new logic     the number the post would use
                                                 2 vs 3 isolates (c) exactly
        4. per-repo attribution for both gaps

    The control run is NOT here to offer a prettier number to pick. Neither run's patterns may be
    touched after seeing either result.

USAGE
    export CORPUS_DIR=...
    SCAN_MODE=line  python3 21-selfbuilt-bypass-v2.py > ../outputs/selfbuilt-bypass-control.txt
    SCAN_MODE=whole python3 21-selfbuilt-bypass-v2.py > ../outputs/selfbuilt-bypass-v2.txt
    python3 23-classify-divergence.py > ../outputs/divergence-classification.txt
"""
import re, os, sys, json, datetime

KIT = os.path.dirname(os.path.abspath(os.path.join(__file__, '..')))
WORK = os.environ.get('CORPUS_DIR') or sys.exit('set CORPUS_DIR')

shas, cdates = {}, {}
for line in open(os.path.join(WORK, 'corpus-shas.tsv')):
    p = line.rstrip('\n').split('\t')
    if len(p) >= 6 and p[0] != 'repo' and p[5] == 'OK':
        shas[p[0]] = p[1]; cdates[p[0]] = p[2]

ledger = {}
for line in open(os.path.join(KIT, 'data', 'ledger-shas.tsv')):
    if line.startswith('#') or not line.strip():
        continue
    p = line.rstrip('\n').split('\t')
    if len(p) >= 2:
        ledger[p[0]] = p[1]

pushed = {}
for line in open(os.path.join(KIT, 'data', 'remain.tsv')):
    p = line.rstrip('\n').split('\t')
    if len(p) >= 5:
        pushed[p[0]] = p[4]

def status(repo):
    """Drift status. LEDGER_* is proof; MOVED_SINCE is strong; NOT_IN_LEDGER is unknowable."""
    if repo in ledger:
        return 'LEDGER_MATCH' if shas.get(repo) == ledger[repo] else 'LEDGER_CHANGED'
    cd, pu = cdates.get(repo, ''), pushed.get(repo, '')
    if cd and pu:
        try:
            if datetime.date.fromisoformat(cd[:10]) > datetime.date.fromisoformat(pu[:10]):
                return 'MOVED_SINCE'
        except ValueError:
            pass
    return 'NOT_IN_LEDGER'

def load(mode):
    p = os.path.join(WORK, f'selfbuilt-v2.{mode}.json')
    if not os.path.exists(p):
        sys.exit(f'{p} not found - run 21-selfbuilt-bypass-v2.py with SCAN_MODE={mode} first')
    return json.load(open(p))

main, ctrl = load('whole'), load('line')
MAIN, CTRL = set(main['kept']), set(ctrl['kept'])
DF4 = set()
for line in open(os.path.join(KIT, 'outputs', 'round2-selfbuilt-bypass.txt')):
    m = re.match(r'\s+(\S+/\S+)\s+::', line)
    if m:
        DF4.add(m.group(1))
N = main['n']

print('=' * 88)
print('SELF-BUILT BACKDOOR - four counts, and what accounts for each gap')
print('=' * 88)
print(f'  1. DF-4     old corpus, old logic : {len(DF4):3d}/{N}')
print(f'  2. CONTROL  new corpus, old logic : {len(CTRL):3d}/{N}')
print(f'  3. MAIN     new corpus, new logic : {len(MAIN):3d}/{N}')
print()
print(f'  gap 1->2 (drift + file selection) : {len(CTRL) - len(DF4):+d}')
print(f'  gap 2->3 (declared changes only)  : {len(MAIN) - len(CTRL):+d}')
print(f'  gap 1->3 (everything)             : {len(MAIN) - len(DF4):+d}')
print()

# ---------------------------------------------------------------- gap 1 -> 2 : drift
print('-' * 88)
print('GAP 1 -> 2 : DF-4 versus CONTROL. NOT a clean drift measurement - read the caveat below.')
print('-' * 88)
print('  CAVEAT, and it limits what this gap can prove: CONTROL runs THIS script\'s detection')
print('  patterns, not DF-4\'s. Reverting SCAN_MODE reverts our own change 1; it does not turn')
print('  this scanner into DF-4\'s scanner. DF-4 searched whole file text, so its regex could')
print('  match across newlines, which the line-by-line control cannot. This gap therefore mixes')
print('  drift with known regex and traversal differences, and a divergence here on an UNCHANGED')
print('  repository is not by itself evidence that two instruments disagree about the same rule.')
print('  Each row below has to be read individually.')
print()
d12 = sorted((CTRL - DF4) | (DF4 - CTRL))
if not d12:
    print('  No divergence. The corpus reads the same today as it did on 2026-08-16 under the old')
    print('  logic, so nothing here is attributable to drift.')
else:
    print(f'{"repository":48s} {"side":12s} {"sha today":42s} status')
    for repo in d12:
        side = 'CONTROL-only' if repo in CTRL else 'DF-4-only'
        print(f'{repo:48s} {side:12s} {shas.get(repo,"?"):42s} {status(repo)}')
    tally = {}
    for repo in d12:
        tally[status(repo)] = tally.get(status(repo), 0) + 1
    print()
    print('  tally:', ', '.join(f'{k}={v}' for k, v in sorted(tally.items())))
    # A net of zero is not the same as no movement, and printing only the net would hide that.
    inn, out = len(CTRL - DF4), len(DF4 - CTRL)
    if inn or out:
        print(f'  membership churn: {inn} in, {out} out, net {inn - out:+d} over {inn + out} repositories')
        if inn == out:
            print('  NOTE: the totals are equal and the membership is NOT. A net of +0 would read as')
            print('  "nothing moved"; in fact the identity of ' + str(inn + out) + ' repositories changed.')
            print('  Read the rows, not the net.')

# ---------------------------------------------------------------- gap 2 -> 3 : declared changes
print()
print('-' * 88)
print('GAP 2 -> 3 : CONTROL versus MAIN. Same corpus, same patterns, only the declared changes.')
print('             Anything here is caused by us, not by the repositories.')
print('-' * 88)
d23 = sorted(MAIN - CTRL)
gone = sorted(CTRL - MAIN)
if not d23 and not gone:
    print('  No divergence. The declared changes did not move the count on this corpus.')
for repo in d23:
    print(f'  + {repo}   sha={shas.get(repo,"?")}')
    for sig, rel, ln, txt in main['hits'].get(repo, [])[:4]:
        print(f'      caught by {sig:6s} {rel}:{ln}  {txt}')
for repo in gone:
    print(f'  - {repo}   present under old logic, absent under new - investigate, this should not')
    print(f'    happen when the only changes are loosening ones')

# ---------------------------------------------------------------- reading
print()
print('=' * 88)
print('READING (fixed before any of these numbers were known)')
print('=' * 88)
unchanged = [r for r in d12 if status(r) == 'LEDGER_MATCH']
drifted   = [r for r in d12 if status(r) in ('LEDGER_CHANGED', 'MOVED_SINCE')]
unknown   = [r for r in d12 if status(r) == 'NOT_IN_LEDGER']

if len(MAIN) == 34:
    print('  MAIN == 34. Reproduced. 34/284 moves to script-reproducible in the kit README.')
else:
    print(f'  MAIN == {len(MAIN)}, not 34. The gap is attributed as:')
    print(f'    {len(MAIN) - len(CTRL):+d} from the declared changes (measured, listed above)')
    print(f'    {len(CTRL) - len(DF4):+d} from drift and file selection (repositories listed above)')
    if not d12:
        print('  No repository diverges under identical logic, so the difference is entirely ours,')
        print('  not the repositories. Report both numbers and say which change caused which repo.')
    elif drifted and not unchanged and not unknown:
        print('  Every repository that diverges under identical logic has demonstrably moved since')
        print('  2026-08-16. That part is drift, not the instrument. Report both numbers, keep the')
        print('  published figure labelled as attested at 2026-08-16, and state how many moved.')
    elif unchanged:
        print(f'  {len(unchanged)} repository(ies) diverge on provably UNCHANGED code. Because')
        print('  CONTROL is not DF-4 (see the caveat above), that is not automatically a')
        print('  disagreement about the same rule - it may be a known regex or traversal')
        print('  difference. Open each one. STOP here either way: report every number, say which')
        print('  repository each difference comes from, and do not pick whichever total is')
        print('  convenient. Falling back to 31/284 with an honest note is acceptable; adjusting a')
        print('  scan so the totals agree is not.')
    else:
        print(f'  {len(unknown)} divergent repository(ies) have no recorded SHA and no evidence of')
        print('  movement, so drift can be neither proven nor excluded. Treat as unresolved and say')
        print('  which repositories are unresolvable.')

json.dump({'df4': sorted(DF4), 'control': sorted(CTRL), 'main': sorted(MAIN),
           'gap12': {r: status(r) for r in d12}, 'gap23_added': d23, 'gap23_lost': gone},
          open(os.path.join(WORK, 'divergence.json'), 'w'), indent=1)
