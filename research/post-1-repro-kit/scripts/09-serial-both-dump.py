#!/usr/bin/env python3
"""DF-4 dot kiem 2 / 13.3 - do TOAN BO repo ghim serial ca local lan CI,
in kem 4 dong context quanh workers/fullyParallel de doc TAY (khong phan loai bang regex)."""
import re, os, glob
D = os.path.dirname(os.path.abspath(__file__))
def sclass(w, fp):
    if re.search(r"fullyParallel\s*:\s*false", fp): return 'both'
    h = w.split('||')[0]
    if re.search(r'workers\s*:\s*1\b', h) and '?' not in h: return 'both'
    if re.search(r'\?\s*1\s*:\s*1\b', h): return 'both'
    if re.search(r'\?\s*1\s*:', h) or re.search(r'CI\s*\?\s*1', h): return 'ci'
    if re.search(r'workers\s*:\s*1\b', h): return 'both'
    return 'none'
rows = [l.rstrip('\n').split('\t') for l in open(f'{D}/fields2.tsv')]
n = 0
for r in rows:
    if sclass(r[4], r[5]) != 'both': continue
    n += 1
    d = f'{D}/clones/{r[0].replace("/","_")}'
    print(f'\n=== [{n}] {r[0]}  ({r[2]})')
    cfgs = sorted(glob.glob(f'{d}/playwright*.config.*') + glob.glob(f'{d}/*/playwright*.config.*')
                  + glob.glob(f'{d}/*/*/playwright*.config.*') + glob.glob(f'{d}/cypress.config.*'))[:2]
    for f in cfgs:
        L = open(f, errors='ignore').read().splitlines()
        for i, l in enumerate(L):
            if re.search(r'workers|fullyParallel', l):
                for j in range(max(0, i-4), min(len(L), i+2)):
                    if L[j].strip(): print(f'   {f[len(d)+1:]}:{j+1}: {L[j].strip()[:135]}')
                break
print(f'\nTONG serial-both (may phan loai, CHUA tru 2 ca sai khi doc tay): {n}')
