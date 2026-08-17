#!/usr/bin/env python3
"""DF-4 dot kiem 2 / 13.2 - quet lai TOAN BO repo d=KHONG bang dung 3 tu khoa goc,
tren moi file .ts/.js khong phai tai lieu. Muc dich: do false negative cua chinh pipeline."""
import re, os, glob
D = os.path.dirname(os.path.abspath(__file__))
KEY     = re.compile(r'storageState|globalSetup|cy\.session\(')
DOCLIKE = re.compile(r'\.(md|mdx|txt|rst)$|\.template\.[jt]s$|/(\.cursor|\.claude|docs?)/')
rows = [l.rstrip('\n').split('\t') for l in open(f'{D}/fields2.tsv')]
miss = []; n_no = 0
for r in rows:
    if r[18] == 'YES': continue
    n_no += 1
    d = f'{D}/clones/{r[0].replace("/","_")}'
    if not os.path.isdir(d): continue
    hits = []
    for f in glob.glob(f'{d}/**/*.*', recursive=True):
        if 'node_modules' in f or 'playwright-report' in f: continue
        if not re.search(r'\.[jt]sx?$', f) or DOCLIKE.search(f): continue
        try: t = open(f, errors='ignore').read()
        except Exception: continue
        for i, l in enumerate(t.splitlines()):
            if KEY.search(l) and not re.match(r'\s*(//|\*|/\*)', l):
                hits.append(f'{f[len(d)+1:]}:{i+1}: {l.strip()[:90]}'); break
        if len(hits) >= 2: break
    if hits: miss.append((r[0], r[2], hits))
print(f'repo d=KHONG: {n_no} | pipeline BO SOT: {len(miss)} = {100*len(miss)/n_no:.1f}%')
for a, v, h in miss:
    print(f'  {a} [{v}]')
    for x in h[:2]: print(f'      {x}')
