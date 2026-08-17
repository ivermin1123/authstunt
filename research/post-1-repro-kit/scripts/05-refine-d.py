#!/usr/bin/env python3
"""Refine field d from the evidence files: separate doc-only hits from real code hits.
This tightens MEASUREMENT (grep precision). It does not touch the v1.38 criteria."""
import os, re, sys, glob

DOC = re.compile(r'\.(md|mdx|txt|rst|mdc)(:|$)', re.I)
CFG = re.compile(r'(playwright[^/]*\.config\.[cm]?[jt]s|cypress\.config\.[cm]?[jt]s|cypress\.json):')

def parse(path):
    sec = None; out = {'ss': [], 'gs': [], 'sess': [], 'dep': [], 'files': []}
    for line in open(path, errors='ignore'):
        l = line.rstrip('\n')
        if l.startswith('== '):
            sec = l; continue
        if sec and sec.startswith('== D'):
            if l.startswith('-- storageState'): cur = 'ss'; continue
            if l.startswith('-- globalSetup'): cur = 'gs'; continue
            if l.startswith('-- cy.session'): cur = 'sess'; continue
            if l.startswith('-- project deps'): cur = 'dep'; continue
            if l.startswith('-- setup files'): cur = 'files'; continue
            if l.strip() and 'cur' in dir(): out[cur].append(l)
    return out

def classify(ev):
    hits = ev['ss'] + ev['gs'] + ev['sess']
    code = [h for h in hits if not DOC.search(h.split(':')[0] + ':')]
    cfg  = [h for h in hits if CFG.search(h)]
    return ('YES' if code else ('DOCONLY' if hits else 'NO')), ('YES' if cfg else 'NO'), (code[:3] or hits[:2])

if __name__ == '__main__':
    D = os.path.dirname(os.path.abspath(__file__))
    rows = [l.rstrip('\n').split('\t') for l in open(f'{D}/fields.tsv') if l.strip()]
    out = open(f'{D}/fields2.tsv', 'w')
    n_doc = 0
    for r in rows:
        slug = r[0].replace('/', '_')
        p = f'{D}/evidence/{slug}.txt'
        if not os.path.exists(p):
            r = r + ["NA"]*(18-len(r)); r += ["NA","NA",""]; out.write('\t'.join(r) + '\n'); continue
        ev = parse(p)
        dcode, dcfg, sample = classify(ev)
        r = r + ['NA']*(18-len(r))
        if r[6] == 'YES' and dcode == 'DOCONLY': n_doc += 1
        r += [dcode, dcfg, ' || '.join(x[:150] for x in sample)]
        out.write('\t'.join(r) + '\n')
    out.close()
    print(f'refined {len(rows)} rows; doc-only false positives on field d: {n_doc}')
