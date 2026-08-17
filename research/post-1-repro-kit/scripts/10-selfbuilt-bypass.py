#!/usr/bin/env python3
"""DF-4 dot kiem 2 / 13.4 - truong MOI: backdoor TU DUNG (khong phai cua hang).
Chi quet file trong duong dan E2E; loai unit test va mock thu vien. In ca hop voi backdoor hang."""
import re, os, glob, collections
D = os.path.dirname(os.path.abspath(__file__))
E2EPATH = re.compile(r'/(e2e|cypress|playwright|tests?/e2e|smoke)/|\.cy\.[jt]sx?$|playwright[^/]*\.config\.[jt]s$')
NOISE   = re.compile(r'(node_modules|playwright-report|test-results|/dist/|/build/|__tests__|\.test\.[jt]sx?$)')
INJECT  = re.compile(r"""(
 localStorage\.setItem\(\s*["'][^"']*(clerk|stytch|supabase|sb-|wos-|auth|session|token)|
 addCookies\(\s*\[?\s*\{?[^)]{0,80}(session|auth|token|jwt)|
 setCookie\(\s*["'][^"']*(session|auth|token)|
 (ALLOW|SKIP|BYPASS|DISABLE)_?(LOCAL_)?(DEV_)?(AUTH|IDENTITY|LOGIN|SESSION)|
 (AUTH|LOGIN|SESSION)_(BYPASS|DISABLED|SKIP)|E2E_(SESSION|AUTH)_(JWT|TOKEN)|
 x-e2e-|X-Test-User|TEST_USER_HEADER|DEV_USER_ID)""", re.I | re.X)
def prim(vs):
    s = set(vs.split('+'))
    for v in ['stytch','descope','supertokens','kinde','workos']:
        if v in s: return v
    for v in ['clerk','auth0','firebase','supabase']:
        if v in s: return v
    return sorted(s)[0]
rows = [l.rstrip('\n').split('\t') for l in open(f'{D}/fields2.tsv')]
vend = {l.split('\t')[0] for l in open(f'{D}/backdoor-strict.tsv') if l.strip()}
hit = {}; byv = collections.defaultdict(lambda: [0,0,0,0])
for r in rows:
    v = prim(r[2]); byv[v][3] += 1
    d = f'{D}/clones/{r[0].replace("/","_")}'
    if os.path.isdir(d):
        for f in glob.glob(f'{d}/**/*.*', recursive=True):
            if NOISE.search(f) or not re.search(r'\.[jt]sx?$', f) or not E2EPATH.search(f): continue
            try: t = open(f, errors='ignore').read()
            except Exception: continue
            m = INJECT.search(t)
            if m: hit[r[0]] = (f[len(d)+1:], ' '.join(m.group(0).split())[:70]); break
    a, b = r[0] in vend, r[0] in hit
    if a: byv[v][0] += 1
    if b: byv[v][1] += 1
    if a or b: byv[v][2] += 1
POP = {'clerk':1274,'auth0':1210,'firebase':547,'supabase':516}; SMALL_POP = 193
def wgt(i):
    t = w = 0.0
    for v, ww in POP.items():
        if v in byv and byv[v][3]: t += ww*byv[v][i]/byv[v][3]; w += ww
    sm = [byv[v] for v in ['workos','stytch','descope','kinde','supertokens'] if v in byv]
    if sm: t += SMALL_POP*sum(x[i] for x in sm)/sum(x[3] for x in sm); w += SMALL_POP
    return 100*t/w
N = len(rows)
for lbl, i in [('backdoor HANG',0), ('bypass TU DUNG',1), ('HOP (mot trong hai)',2)]:
    tot = sum(x[i] for x in byv.values())
    print(f'{lbl:22s}{tot:4d}/{N} = {100*tot/N:5.1f}% tho   {wgt(i):5.1f}% co trong so')
print()
for v, x in sorted(byv.items(), key=lambda k: -k[1][2]/k[1][3]):
    print(f'  {v:12s} hang={x[0]:3d} tu-dung={x[1]:3d} hop={x[2]:3d}/{x[3]:3d} = {100*x[2]//x[3]}%')
print('\n--- danh sach bypass tu dung, path:line ---')
for k, (f, m) in sorted(hit.items()): print(f'   {k} :: {f} :: {m}')
