#!/usr/bin/env python3
"""Field h, strict. Separate the create-playwright scaffold comment from human-written pain,
then classify the CAUSE the human named. Nothing here changes the v1.38 criteria table."""
import os, re, sys, collections

D = os.path.dirname(os.path.abspath(__file__))
SCAFFOLD = re.compile(r'opt out of parallel tests on ci', re.I)
# also scaffold: the stock 'Run tests in files in parallel' / 'Retry on CI only' lines
SCAFFOLD2 = re.compile(r'(run tests in files in parallel|retry on ci only|fail the build on ci if|reporter to use\.? see|opt out of parallel)', re.I)

CAUSE = [
 ('identity/account', re.compile(r'\b(user|account|login|auth|session|tenant|clerk|signin|sign-in|credential)\b', re.I)),
 ('db/data',          re.compile(r'\b(database|db|sqlite|postgres|neon|seed|seeded|row|record|mutation|data)\b', re.I)),
 ('server/port/fs',   re.compile(r'\b(server|port|filesystem|artifact|file|webserver|memory|cpu|thread|starve|proxy)\b', re.I)),
]

def pain_lines(slug):
    p = f'{D}/evidence/{slug}.txt'
    if not os.path.exists(p): return [], []
    txt = open(p, errors='ignore').read()
    m = re.search(r'-- comments near workers --(.*?)-- commit msgs --(.*?)-- code mentions --', txt, re.S)
    if not m: return [], []
    cm = [l.strip() for l in m.group(1).splitlines() if l.strip()]
    cl = [l.strip() for l in m.group(2).splitlines() if l.strip()]
    return cm, cl

rows = [l.rstrip('\n').split('\t') for l in open(f'{D}/fields2.tsv')]
tot = len(rows)
scaffold_only = 0; human = []; nocomment = 0
for r in rows:
    slug = r[0].replace('/', '_')
    cm, cl = pain_lines(slug)
    real_cm = [l for l in cm if not SCAFFOLD.search(l) and not SCAFFOLD2.search(l)]
    real_cl = [l for l in cl if re.search(r'flak|parallel|worker|race|concurren|serial|collision|conflict', l, re.I)]
    if not cm and not cl: nocomment += 1; continue
    if not real_cm and not real_cl:
        scaffold_only += 1; continue
    causes = set()
    blob = ' '.join(real_cm + real_cl)
    for name, rx in CAUSE:
        if rx.search(blob): causes.add(name)
    human.append((r[0], r[2], sorted(causes) or ['unclear'], (real_cm + real_cl)[:2]))

print(f'n={tot}')
print(f'  khong co comment/commit nao quanh workers : {nocomment}')
print(f'  CHI co comment scaffold create-playwright : {scaffold_only}  ({100*scaffold_only//tot}%)')
print(f'  co comment/commit NGUOI VIET              : {len(human)}  ({100*len(human)//tot}%)')
cc = collections.Counter()
for _, _, cs, _ in human:
    for c in cs: cc[c] += 1
print('\n  nguyen nhan nguoi do TU NEU (mot repo co the co nhieu):')
for c, n in cc.most_common(): print(f'    {c:18s} {n}')
print('\n--- repo co ly do NHAN DANG (identity/account) ---')
for repo, v, cs, ls in human:
    if 'identity/account' in cs:
        print(f'* {repo} [{v}]')
        for l in ls: print(f'    {l[:180]}')
print('\n--- toan bo comment nguoi viet (de tay kiem) ---')
for repo, v, cs, ls in human:
    print(f'{repo}\t{v}\t{",".join(cs)}\t{" | ".join(x[:130] for x in ls)}')
