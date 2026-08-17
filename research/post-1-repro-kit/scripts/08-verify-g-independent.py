#!/usr/bin/env python3
"""DF-4 dot kiem 2 / 13.1 - kiem lai truong g bang tin hieu DOC LAP voi dot 1.
Dot 1 loc theo TEN FILE. Day quet MOI file test bang tu vung SDK that cua tung hang,
loai file co jest.mock/vi.mock/@testing-library. Phan 4 loai: A / A2 / B / C / D."""
import re, os, glob, collections
D = os.path.dirname(os.path.abspath(__file__))
NOISE  = re.compile(r'(node_modules|playwright-report|test-results|/dist/|/build/)')
MOCKED = re.compile(r"(jest\.mock\(|vi\.mock\(|@testing-library/react|from '@?[\w@/.-]*mocks)")
REALMAIL = re.compile(r'(mailpit|mailhog|maildev|mailosaur|mailtrap|inbucket|ethereal\.email|/api/v1/message|imap)', re.I)
BACKDOOR = re.compile(r"(?<!\d)424242(?!\d)|\+clerk_test|emulator|escape-hatch|escapeHatch|getOtpCode|testOtp", re.I)
ENTER = re.compile(r"""(name:\s*["'/][^"'/]*(verification|one[- ]time|OTP)|
  \[(name|id|data-testid)=["'][^"']*(otp|verification|two.?factor)|
  otp\.verify|verifyOtp\(|attemptEmailAddressVerification|attemptFirstFactor|
  pressSequentially\(\s*["']\d{4,8}|fill\(\s*["']\d{6}["']|magicLinks?\.authenticate)""", re.I | re.X)
GAVEUP = re.compile(r"(cannot be automated|can'?t be automated|without access to the email|no access to (the )?inbox|skip.*email verification|requires? (a )?real email)", re.I)

rows = [l.rstrip('\n').split('\t') for l in open(f'{D}/fields2.tsv')]
cat = collections.defaultdict(set); nfile = 0
for r in rows:
    d = f'{D}/clones/{r[0].replace("/","_")}'
    if not os.path.isdir(d): continue
    for f in glob.glob(f'{d}/**/*.*', recursive=True):
        if NOISE.search(f) or not re.search(r'\.(spec|test|e2e|cy)\.[jt]sx?$', f): continue
        nfile += 1
        try: t = open(f, errors='ignore').read()
        except Exception: continue
        mocked = bool(MOCKED.search(t)); rel = f[len(d)+1:]
        if ENTER.search(t) and not mocked:
            if REALMAIL.search(t):  cat['A  nhap ma doc tu MAIL THAT'].add((r[0], rel))
            elif BACKDOOR.search(t): cat['B  nhap ma BACKDOOR/emulator'].add((r[0], rel))
            else:                    cat['C  nhap ma GIA / stub'].add((r[0], rel))
        elif REALMAIL.search(t) and not mocked:
            cat['A2 doc mail that (khong phai luong danh tinh)'].add((r[0], rel))
        if GAVEUP.search(t): cat['D  viet ra rang KHONG test duoc'].add((r[0], rel))
print(f'repo={len(rows)}  file test da quet={nfile}')
for k in sorted(cat):
    repos = sorted({x[0] for x in cat[k]})
    print(f'\n{k}: {len(repos)} repo')
    for rp in repos:
        for fpath in sorted(x[1] for x in cat[k] if x[0] == rp)[:2]:
            print(f'    {rp} :: {fpath}')
