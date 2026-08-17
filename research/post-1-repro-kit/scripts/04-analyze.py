#!/usr/bin/env python3
"""DF-4 phase 4: apply the v1.38 criteria table verbatim. No re-interpretation."""
import re, collections, sys

COLS = ['repo','tier','vendors','fw','workers','fullypar','d','dlogin','ddep',
        'e','ekind','fkind','g','h','ntest','nauth','stars','pushed','dcode','dcfg','dsample']
SMALL = {'workos','stytch','descope','kinde','supertokens'}
# candidate-population sizes per vendor (from candidates.tsv pools, PREREG R10)
POP = {'clerk':1274,'auth0':1210,'firebase':547,'supabase':516,
       'workos':None,'stytch':None,'descope':None,'kinde':None,'supertokens':None}
SMALL_POP = 193  # all five small vendors together, census

def prim(vs):
    s=set(vs.split('+'))
    for v in ['stytch','descope','supertokens','kinde','workos']:
        if v in s: return v
    for v in ['clerk','auth0','firebase','supabase']:
        if v in s: return v
    return sorted(s)[0]

rows=[]
import os
FF = 'fields2.tsv' if os.path.exists('fields2.tsv') else 'fields.tsv'
for l in open(FF):
    p=l.rstrip('\n').split('\t')
    if len(p)<18: continue
    p = p + ['NA']*(len(COLS)-len(p))
    r=dict(zip(COLS,p)); r['pv']=prim(r['vendors'])
    r['ntest']=int(r['ntest']) if str(r['ntest']).isdigit() else 0; r['nauth']=int(r['nauth']) if str(r['nauth']).isdigit() else 0
    rows.append(r)

def serial_class(r):
    """3-way: 'both' = serial local AND CI; 'ci' = serial only on CI (PW template);
       'none' = parallel everywhere. Per PREREG R6 the CI branch is what the table counts."""
    w=r['workers']; fp=r['fullypar']
    if re.search(r"fullyParallel\s*:\s*false", fp): return 'both'
    flat = re.search(r'workers\s*:\s*1\s*[,;}\s]', w) and '?' not in w.split('||')[0]
    if flat: return 'both'
    if re.search(r'workers\s*:\s*1\b', w) and '?' not in w.split('||')[0]: return 'both'
    if re.search(r'\?\s*1\s*:\s*(1\b)', w): return 'both'
    if re.search(r'\?\s*1\s*:', w) or re.search(r'CI\s*\?\s*1', w): return 'ci'
    if re.search(r'workers\s*:\s*1\b', w): return 'both'
    return 'none'

def serial_decl(r):
    return 'SERIALDECL' in r['workers']

def pin_serial(r):
    """workers pinned to 1 / serial, per PREREG R6. CI branch counts if branched."""
    w=r['workers']
    if 'SERIALDECL' in w: return True
    if w=='ABSENT': return False
    # verbatim examples: "workers: 1", "workers: process.env.CI ? 1 : undefined"
    if re.search(r'workers\s*:\s*(process\.env\.CI\s*\?\s*)?1\b', w): return True
    if re.search(r'workers\s*:\s*.*\?\s*1\s*:', w): return True
    if re.search(r"fullyParallel\s*:\s*false", r['fullypar']): return True
    return False

def has_workers_key(r):
    return r['workers']!='ABSENT' and 'workers' in r['workers']

def stats(sub, name):
    n=len(sub)
    if n==0: return None
    return dict(name=name, n=n,
        d=sum(1 for r in sub if r['d']=='YES'),
        dlogin=sum(1 for r in sub if r['dlogin']=='YES'),
        ddep=sum(1 for r in sub if r['ddep']=='YES'),
        d_or_dep=sum(1 for r in sub if r['d']=='YES' or r['ddep']=='YES'),
        e=sum(1 for r in sub if r['e']=='YES'),
        f=sum(1 for r in sub if r['fkind'].strip()),
        g=sum(1 for r in sub if r['g']=='YES'),
        h=sum(1 for r in sub if r['h']=='YES'),
        serial=sum(1 for r in sub if pin_serial(r)),
        s_both=sum(1 for r in sub if serial_class(r)=='both'),
        s_ci=sum(1 for r in sub if serial_class(r)=='ci'),
        s_none=sum(1 for r in sub if serial_class(r)=='none'),
        sdecl=sum(1 for r in sub if serial_decl(r)),
        dcode=sum(1 for r in sub if len(r.get('dcode',''))>0 and r['dcode']=='YES'),
        dcfg=sum(1 for r in sub if r.get('dcfg')=='YES'),
        wkey=sum(1 for r in sub if has_workers_key(r)))

def weighted(field, groups):
    """stratified estimator weighted by candidate-population share (PREREG R10)"""
    tot=0.0; wsum=0.0
    for v,w in [('clerk',1274),('auth0',1210),('firebase',547),('supabase',516)]:
        g=groups.get(v)
        if not g: continue
        tot += w * (g[field]/g['n']); wsum += w
    smalls=[groups[v] for v in SMALL if v in groups]
    if smalls:
        num=sum(g[field] for g in smalls); den=sum(g['n'] for g in smalls)
        tot += SMALL_POP*(num/den); wsum += SMALL_POP
    return 100*tot/wsum if wsum else 0.0

def report(rows, label):
    print(f'\n{"="*72}\n### {label}  (n={len(rows)})\n{"="*72}')
    groups={}
    for v in sorted(set(r['pv'] for r in rows)):
        s=stats([r for r in rows if r['pv']==v], v)
        groups[v]=s
    hdr=f'{"vendor":12s} {"n":>4s} {"d":>9s} {"d+dep":>9s} {"serial":>9s} {"backdoor":>9s} {"mail":>7s} {"g":>9s} {"pain":>8s}'
    print(hdr); print('-'*len(hdr))
    for v,s in groups.items():
        print(f'{v:12s} {s["n"]:4d} {s["d"]:4d}/{100*s["d"]//s["n"]:3d}% {s["d_or_dep"]:4d}/{100*s["d_or_dep"]//s["n"]:3d}% '
              f'{s["serial"]:4d}/{100*s["serial"]//s["n"]:3d}% {s["e"]:4d}/{100*s["e"]//s["n"]:3d}% '
              f'{s["f"]:3d}/{100*s["f"]//s["n"]:2d}% {s["g"]:4d}/{100*s["g"]//s["n"]:3d}% {s["h"]:3d}/{100*s["h"]//s["n"]:3d}%')
    a=stats(rows,'ALL')
    print('-'*len(hdr))
    print(f'{"RAW":12s} {a["n"]:4d} {a["d"]:4d}/{100*a["d"]//a["n"]:3d}% {a["d_or_dep"]:4d}/{100*a["d_or_dep"]//a["n"]:3d}% '
          f'{a["serial"]:4d}/{100*a["serial"]//a["n"]:3d}% {a["e"]:4d}/{100*a["e"]//a["n"]:3d}% '
          f'{a["f"]:3d}/{100*a["f"]//a["n"]:2d}% {a["g"]:4d}/{100*a["g"]//a["n"]:3d}% {a["h"]:3d}/{100*a["h"]//a["n"]:3d}%')
    W={k:weighted(k,groups) for k in ['d','d_or_dep','dlogin','serial','e','f','g','h','s_both','s_ci','s_none','sdecl','dcode','dcfg']}
    print(f'{"WEIGHTED":12s} {"":4s} {W["d"]:8.1f}% {W["d_or_dep"]:8.1f}% {W["serial"]:8.1f}% {W["e"]:8.1f}% {W["f"]:6.1f}% {W["g"]:8.1f}% {W["h"]:7.1f}%')
    print(f'  dlogin (setup thuc su DANG NHAP): raw {a["dlogin"]}/{a["n"]}={100*a["dlogin"]//a["n"]}%  weighted {W["dlogin"]:.1f}%')
    print(f'  co key `workers` trong config:    raw {a["wkey"]}/{a["n"]}={100*a["wkey"]//a["n"]}%')
    print(f'  TRUONG c tach local/CI: serial ca hai={a["s_both"]} ({W["s_both"]:.1f}%) | serial CHI tren CI={a["s_ci"]} ({W["s_ci"]:.1f}%) | song song ca hai={a["s_none"]} ({W["s_none"]:.1f}%) | describe.configure serial={a["sdecl"]} ({W["sdecl"]:.1f}%)')
    print(f'  TRUONG d ba muc: d_raw={a["d"]} ({W["d"]:.1f}%) | d_code={a["dcode"]} ({W["dcode"]:.1f}%) | d_cfg={a["dcfg"]} ({W["dcfg"]:.1f}%)')
    return a, W

def verdict(a, W, npop_qualified):
    print(f'\n--- BANG TIEU CHI v1.38, AP Y NGUYEN ---')
    hits=[]
    r1 = W['dcode'] >= 50
    print(f'[1] storageState/globalSetup dang-nhap-mot-lan >=50%? (d_code, PREREG R11) {W["dcode"]:.1f}%  -> {"HIT (A DONG)" if r1 else "khong"}')
    if r1: hits.append('A DONG (dong 1)')
    r2 = npop_qualified < 20
    print(f'[2] <20 repo dat chuan trong toan quan the?             {npop_qualified}  -> {"HIT (A DONG)" if r2 else "khong"}')
    if r2: hits.append('A DONG (dong 2)')
    r3 = W['e'] >= 50
    print(f'[3] >=50% dung backdoor chinh hang?                     {W["e"]:.1f}%  -> {"HIT (A DONG)" if r3 else "khong"}')
    if r3: hits.append('A DONG (dong 3)')
    npain = a['h']
    r4 = W['serial'] >= 30 and npain >= 5
    print(f'[4] >=30% ghim serial VA >=5 repo co dau dau?           {W["serial"]:.1f}% / {npain} repo -> {"HIT (A MO LAI)" if r4 else "khong"}')
    if r4: hits.append('A MO LAI (dong 4)')
    r5 = 15 <= W['serial'] < 30
    print(f'[5] khoang giua 15-30% ghim serial?                     {W["serial"]:.1f}%  -> {"HIT (C)" if r5 else "khong"}')
    if r5: hits.append('C (dong 5)')
    print(f'\nO ROI VAO: {hits if hits else "KHONG O NAO - bang khong luong truoc"}')
    return hits

if __name__=='__main__':
    NQ = int(sys.argv[1]) if len(sys.argv)>1 else 679
    a,W = report(rows, 'TOAN BO REPO DAT CHUAN (mau do)')
    v1 = verdict(a,W,NQ)
    sub=[r for r in rows if r['ntest']>=3 and r['nauth']>=1]
    if sub:
        a2,W2 = report(sub, 'CHI SUBSTANTIVE (ntest>=3 va nauth>=1) — PREREG R9')
        v2 = verdict(a2,W2,NQ)
        print(f'\n>>> HAI MAU SO: {v1}  ||  {v2}')
