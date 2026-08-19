#!/usr/bin/env python3
"""Check the detection patterns in 21-selfbuilt-bypass-v2.py against the examples the ledger already
documents, without touching the corpus.

WHY THIS IS A SEPARATE, RUNNABLE FILE
    A re-derivation is worthless if its patterns silently fail to match cases that are already known
    to be true. But "adjust the patterns until the count comes out right" is exactly the circular
    move this whole exercise exists to avoid. The distinction is what gets checked:

        legitimate : does the pattern match a line the ledger records, with path:line and SHA, as a
                     genuine self-built backdoor? A miss here is a transcription bug.
        circular   : does the total come out at 34? Never a reason to touch a pattern.

    This file only ever does the first. It needs no corpus and no network, so it can be run before
    any count exists - which is when it was run, and when it caught the bug declared in script 21's
    header. Run it again after any edit to those patterns.

    The known false positive is included as a case that MUST still match. The regex is supposed to
    hit it; a human overrules it in exclusions-selfbuilt.tsv. Silently narrowing the pattern to make
    it disappear would hide a real property of the instrument.

USAGE
    python3 24-pattern-selftest.py        # exits non-zero if any case fails
"""
import re, ast, os, sys

HERE = os.path.dirname(os.path.abspath(__file__))
tree = ast.parse(open(os.path.join(HERE, '21-selfbuilt-bypass-v2.py')).read())
ns = {'re': re}
for node in tree.body:
    if isinstance(node, ast.Assign) and getattr(node.targets[0], 'id', '') in ('FORGE', 'SWITCH', 'HEADER', 'VENDOR'):
        exec(compile(ast.Module([node], []), '<patterns>', 'exec'), ns)
FORGE, SWITCH, HEADER, VENDOR = ns['FORGE'], ns['SWITCH'], ns['HEADER'], ns['VENDOR']
SIGNALS = (('FORGE', FORGE), ('SWITCH', SWITCH), ('HEADER', HEADER))

# (name, source text as it appears in the repository, must_match, ledger reference)
CASES = [
    ('fit-log', "    win.localStorage.setItem('clerk-db-jwt', 'mock-jwt-token');", True, '1.15'),
    ('ai-assistant', '''  await page.context().addCookies([
    {
      name: "wos-session",
      value: "mock-session-token",
      domain: "localhost",
    },
  ]);''', True, '1.14'),
    ('tensr cookie', '''  await page.context().addCookies([
    {
      name: 'stytch_session_token',
      value: 'e2e-playwright-session',
    },
  ]);''', True, '1.16'),
    ('tensr localStorage', "      localStorage.setItem('stytch_session_token', 'e2e-playwright-session');", True, '1.16b'),
    ('Digital_Technologies_Radar', "    window.localStorage.setItem('drr-current-user-id', 'admin');", True, 'section 2b'),
    ('ubc-discovery', '  sessionStorage.setItem("ubc-discovery-test-google-user", JSON.stringify({uid: "otp-first-uid"}))', True, 'section 2b'),
    ('kil.dev imported constant', "import { ADMIN_TEST_BYPASS_COOKIE } from '~/lib/admin-test-bypass';", True, 'section 2b'),
    ('mis-capstone switch', "    command: `VITE_APP_ENV=e2e VITE_E2E_BYPASS_AUTH=1 npm run dev -- --host 127.0.0.1`,", True, '1.17'),
    ('auto-core-platform switch', "    command: 'cross-env VITE_E2E_SKIP_AUTH=true vite --port 5174 --strictPort',", True, '1.18'),
    ('assistant-mk1 switch', "  command: 'WORKBENCH_ALLOW_LOCAL_DEV_IDENTITY=true WORKBENCH_DEV_USER_ID=e2e-owner pnpm dev',", True, '1.19'),
    ('betanxt switch', 'NEXT_PUBLIC_BYPASS_AUTH=true', True, '1.20'),
    # Must still match. It is a true property of the regex, overruled by a human, not hidden.
    ('MCPJam - known false positive', '  sessionStorage.setItem("oauth-debugger-e2e-started", "true")', True, 'section 2b'),
]

# Vendor mechanisms must be subtracted, so that a documented vendor backdoor is never counted as a
# home-made one.
VENDOR_CASES = [
    ('setupClerkTestingToken', '  await setupClerkTestingToken({ page });', True),
    ('+clerk_test address', '  const email = `user+clerk_test@example.com`;', True),
    ('firebase emulator', '  connectAuthEmulator(auth, "http://localhost:9099");', True),
    # and must NOT swallow a genuine self-built line that merely mentions a vendor's product name
    ('forged stytch cookie is NOT a vendor mechanism', "      name: 'stytch_session_token',", False),
]

fails = []
print(f'{"case":36s} {"expect":7s} {"got":7s} signal   ledger')
for name, text, must, ref in CASES:
    hit = None
    for sig, rx in SIGNALS:
        for m in rx.finditer(text):
            window = text[max(0, m.start() - 200): m.end() + 200]
            if VENDOR.search(window):
                continue
            hit = sig
            break
        if hit:
            break
    got = hit is not None
    if got != must:
        fails.append(name)
    print(f'{name:36s} {str(must):7s} {str(got):7s} {hit or "-":8s} {ref}')

print()
for name, text, must in VENDOR_CASES:
    got = bool(VENDOR.search(text))
    if got != must:
        fails.append(name)
    print(f'vendor-subtraction {name:50s} expect={must} got={got}')

# ---------------------------------------------------------------------------------------------
# Script 22: the capability patterns must match the evidence recorded for each of the six stage-2
# candidates. A miss here silently shortens the funnel, which would look like a cleaner result.
tree22 = ast.parse(open(os.path.join(HERE, '22-inbox-capability.py')).read())
ns22 = {'re': re}
for node in tree22.body:
    if isinstance(node, ast.Assign) and getattr(node.targets[0], 'id', '') in ('CAP', 'IDENTITY'):
        exec(compile(ast.Module([node], []), '<patterns22>', 'exec'), ns22)
CAP, IDENTITY = ns22['CAP'], ns22['IDENTITY']

CAP_CASES = [
    ('stytch-browser', "  cy.mailosaurGetMessage(MAILOSAUR_SERVER_ID, {", True, '2a, the one real case'),
    ('orgOS', "    // 3. Integrate with an email testing service (e.g., Mailinator, Mailtrap)", True, '2a'),
    ('CS-Grad-Tracker', "  const account = await nodemailer.createTestAccount(); // ethereal.email", True, '2a'),
    ('workscanner', "import { ImapFlow } from 'imapflow';", True, '2a'),
    ('kazuq', "export async function getMailhogEmails(to: string) {", True, '2a'),
    ('sebkopsi', "    cy.get('#email').type('test@testmail.com');", True, '2a'),
]
print()
for name, text, must, ref in CAP_CASES:
    hit = next((k for k, rx in CAP.items() if rx.search(text)), None)
    got = hit is not None
    if got != must:
        fails.append(f'CAP:{name}')
    print(f'capability {name:28s} expect={must} got={got} kind={hit or "-":16s} {ref}')

# =================================================================================================
# LAYER 2 - ISOLATION
#
# Everything above is a REGRESSION test: real evidence in, expected verdict out. It answers "does
# the scanner still get the known cases right", and it is worth keeping. It does not answer "is
# every mechanism in the pattern actually exercised", and the difference is not academic.
#
# The orgOS case above passed while `mailinator` was missing from the pattern entirely, because that
# same line names Mailtrap and matched on that instead. The test was green, the assertion was true,
# and the mechanism under test never ran. A test that is green for the wrong reason is worse than no
# test, because it sells confidence for nothing.
#
# So, for every token in every vocabulary: build a sample containing that token and no other token
# from the same vocabulary, assert it matches, and then MUTATE the pattern by disabling exactly that
# token and assert the sample stops matching. The mutation is what proves the token was the reason.
# A token with no isolation case is a failure, not a warning - otherwise coverage silently rots as
# tokens get added.
# =================================================================================================

def disable(rx, token):
    """Return the pattern with `token` replaced by a never-matching assertion, structure intact."""
    src = rx.pattern
    if token not in src:
        return None
    return re.compile(src.replace(token, '(?!x)x'), rx.flags)

# token -> a sample containing that token and nothing else from its vocabulary
TOKEN_SAMPLES = {
    'CAP': {
        'mailosaur': 'const id = process.env.MAILOSAUR_SERVER_ID;',
        'mailslurp': 'import { MailSlurp } from "mailslurp-client";',
        'mailtrap': '  host: "smtp.mailtrap.io",',
        'mailinator': '  const box = "qa-user@mailinator.com";',
        r'testmail\.app': '  // inbox provided by testmail.app',
        '@testmail': '  cy.get("#email").type("qa@testmail.com");',
        '1secmail': '  const url = "https://www.1secmail.com/api/v1/";',
        'maildrop': '  const inbox = "https://maildrop.cc/inbox";',
        'imapflow': 'import { ImapFlow } from "imapflow";',
        'node-imap': '    "node-imap": "^0.9.6",',
        'imap-simple': '    "imap-simple": "^5.0.0",',
        'mailparser': 'import { simpleParser } from "mailparser";',
        'poplib': '    "poplib": "^0.1.7",',
        'mailpit': '  image: axllent/mailpit:latest',
        'mailhog': '  image: mailhog/mailhog:v1.0.1',
        'maildev': '  image: maildev/maildev:2.0.0',
        'inbucket': '  image: inbucket/inbucket:stable',
        'greenmail': '  image: greenmail/standalone:2.0.0',
        r'ethereal\.email': '  // account created on ethereal.email',
        'createTestAccount': '  const acct = await nodemailer.createTestAccount();',
        r'gmail\.users\.messages': '  const res = await gmail.users.messages.list({ userId: "me" });',
        r'@microsoft/microsoft-graph-client': '    "@microsoft/microsoft-graph-client": "^3.0.0",',
        r'@types/imap': '    "@types/imap": "^0.8.35",',
        r'["\']imap["\']': '    "imap": "^0.8.19",',
        r'googleapis[^\n]{0,40}gmail': '  const googleapis = require("googleapis").gmail;',
        r'graph\.microsoft\.com[^\n]{0,40}messages': '  await fetch("https://graph.microsoft.com/v1.0/me/messages");',
    },
    'HEADER': {
        'x-e2e-': '  headers: { "x-e2e-actor": "owner" },',
        'X-Test-User': '  request.setHeader("X-Test-User", "alice");',
        'TEST_USER_HEADER': '  const h = process.env.TEST_USER_HEADER;',
        'DEV_USER_ID': '  env: { DEV_USER_ID: "e2e-owner" },',
    },
    'VENDOR': {
        r'\+clerk_test': '  const email = "qa+clerk_test@example.com";',
        'setupClerkTestingToken': '  await setupClerkTestingToken({ page });',
        'clerkSetup': '  await clerkSetup();',
        '@clerk/testing': 'import { clerk } from "@clerk/testing/playwright";',
        'FIREBASE_AUTH_EMULATOR_HOST': '  FIREBASE_AUTH_EMULATOR_HOST: "127.0.0.1:9099",',
        'connectAuthEmulator': '  connectAuthEmulator(auth, "http://127.0.0.1:9099");',
        'mailosaur': '  cy.mailosaurGetMessage(serverId, {});',
        'mailslurp': '  const mailslurp = new MailSlurp({ apiKey });',
        'mailtrap': '  host: "sandbox.mailtrap.io",',
        r'\+dynamic_test': '  const email = "qa+dynamic_test@example.com";',
        r'firebase\s+emulators': '  run: firebase emulators:exec --only auth "pnpm test"',
        r'descope[a-z-]*test': '  const u = descope-test-user;',
        r'stytch[a-z_]*sandbox': '  const v = stytch_sandbox_otp;',
        r'supabase[a-z_]*(test_helpers|admin_client)': '  from supabase_test_helpers import x',
    },
}
# FORGE and SWITCH are structural rather than a flat word list, so their two axes are covered
# separately: every sink, and every storage-key word.
TOKEN_SAMPLES['FORGE'] = {
    # sinks
    r'(local|session)Storage\.setItem': "  win.localStorage.setItem('app-session', 'x');",
    'addCookies': '  await ctx.addCookies([{ name: "app-session", value: "x" }]);',
    'setCookie': '  setCookie("app-session", "x");',
    r'cookies\(\)\.set': '  cookies().set("app-session", "x");',
    r'document\.cookie': '  document.cookie = "app-session=x";',
    # storage-key words. Each sample carries exactly one of them.
    'clerk': "  localStorage.setItem('clerk-db-x', 'y');",
    'stytch': "  localStorage.setItem('stytch_xyz', 'x');",
    'supabase': "  localStorage.setItem('supabase-key', 'x');",
    'sb-': "  localStorage.setItem('sb-abc-key', 'x');",
    'wos-': "  localStorage.setItem('wos-thing', 'x');",
    'auth': "  localStorage.setItem('app-auth', 'x');",
    'session': "  localStorage.setItem('app-session', 'x');",
    'token': "  localStorage.setItem('app-token', 'x');",
    'jwt': "  localStorage.setItem('id-jwt', 'x');",
    'user': "  sessionStorage.setItem('current-user-id', 'admin');",
}
TOKEN_SAMPLES['SWITCH'] = {
    'BYPASS': '  env: { VITE_E2E_BYPASS_AUTH: "1" },',
    'SKIP': '  env: { VITE_E2E_SKIP_AUTH: "true" },',
    'ALLOW': '  env: { WORKBENCH_ALLOW_LOCAL_DEV_IDENTITY: "true" },',
    'DISABLE': '  env: { DISABLE_AUTH: "1" },',
    'TEST_BYPASS': '  const TEST_BYPASS_FLAG = true;',
    'DISABLED': '  const flag = AUTH_DISABLED;',
    'COOKIE': "  import { BYPASS_COOKIE } from './x';",
    'IDENTITY': '  env: { ALLOW_IDENTITY: "1" },',
    'LOCAL_': '  env: { ALLOW_LOCAL_DEV_SESSION: "1" },',
    'LOGIN': '  env: { SKIP_LOGIN: "1" },',
    r'E2E_(SESSION|AUTH)_(JWT|TOKEN)': '  const t = process.env.E2E_SESSION_JWT;',
}

VOCAB = {'FORGE': FORGE, 'SWITCH': SWITCH, 'HEADER': HEADER, 'VENDOR': VENDOR}
VOCAB.update({'CAP': None})   # CAP is a dict of patterns; handled below

NEUTRAL = '  const total = items.reduce((a, b) => a + b.price, 0);'

print()
print('--- isolation layer: every token exercised alone, and proven load-bearing ---')
iso_checked = 0
for vname, samples in TOKEN_SAMPLES.items():
    if vname == 'CAP':
        combined = re.compile('|'.join(f'(?:{rx.pattern})' for rx in CAP.values()), re.I)
        rx_all = combined
    else:
        rx_all = VOCAB[vname]
    for token, sample in samples.items():
        iso_checked += 1
        matched = bool(rx_all.search(sample))
        # mutate: disable exactly this token, the sample must stop matching
        if vname == 'CAP':
            mutated = re.compile('|'.join(f'(?:{rx.pattern.replace(token, "(?!x)x")})' for rx in CAP.values()), re.I)
        else:
            mutated = disable(rx_all, token)
        if mutated is None:
            fails.append(f'ISO:{vname}:{token}:token-not-in-pattern')
            print(f'  {vname:7s} {token:36s} FAIL - token not found in pattern source')
            continue
        still = bool(mutated.search(sample))
        ok = matched and not still
        if not ok:
            fails.append(f'ISO:{vname}:{token}')
        flag = 'ok' if ok else ('NOT MATCHED' if not matched else 'MATCHES ANYWAY - sample not isolating')
        print(f'  {vname:7s} {token:36s} match={str(matched):5s} after-disable={str(still):5s} {flag}')

# a sample with no token from any vocabulary must match nothing
for vname, rx in list(VOCAB.items()):
    if vname == 'CAP':
        rx = re.compile('|'.join(f'(?:{r.pattern})' for r in CAP.values()), re.I)
    if rx.search(NEUTRAL):
        fails.append(f'ISO:{vname}:neutral-sample-matched')
        print(f'  {vname:7s} NEUTRAL SAMPLE MATCHED - pattern is too broad')

# coverage: every literal alternative in each vocabulary must have an isolation sample
def literals(pattern_src):
    src = re.sub(r'\[[^\]]*\]', ' ', pattern_src)      # character classes are not literals
    src = re.sub(r'\\[sSdDwWbBnrt]', ' ', src)          # nor are escape sequences
    src = re.sub(r'\{\d*,?\d*\}', ' ', src)             # nor repetition counts
    src = re.sub(r'\(\?[a-zA-Z:#=!<]*', '(', src)
    return {t for t in re.findall(r'[A-Za-z][A-Za-z0-9_.@+-]{2,}', src)
            if t not in {'re', 'IGNORECASE', 'VERBOSE'}}

print()
declared_all = {t.replace('\\', '') for s in TOKEN_SAMPLES.values() for t in s}
for vname in ('FORGE', 'SWITCH', 'HEADER', 'VENDOR'):
    lits = {l.replace('\\', '') for l in literals(VOCAB[vname].pattern)}
    undeclared = {l for l in lits if not any(l in d or d in l for d in declared_all)}
    if undeclared:
        fails.append(f'COVERAGE:{vname}:{sorted(undeclared)}')
        print(f'  COVERAGE GAP {vname}: no isolation sample for {sorted(undeclared)}')
for kind, rx in CAP.items():
    lits = {l.replace('\\', '') for l in literals(rx.pattern)}
    undeclared = {l for l in lits if not any(l in d or d in l for d in declared_all)}
    if undeclared:
        fails.append(f'COVERAGE:CAP/{kind}:{sorted(undeclared)}')
        print(f'  COVERAGE GAP CAP/{kind}: no isolation sample for {sorted(undeclared)}')

# =================================================================================================
# LAYER 3 - IS THE CONTROL RUN ACTUALLY A CONTROL?
#
# Script 23 attributes part of any increase over the ledger's 34 to "the declared changes", and it does so
# by differencing the main run against a control run that reverts them. That attribution is only
# meaningful if the control genuinely reverts the change rather than approximating it. So the
# control is tested too:
#
#   SCAN_MODE=line must MISS exactly the cases that change 1 was made to fix - the two forged
#   cookies written as multi-line object literals - and must still catch everything single-line.
#   If the line traversal caught them anyway, change 1 was not the cause of anything and the
#   attribution would be fiction.
#
#   CAP_MODE=legacy must MISS a Mailinator-only line and still catch a Mailtrap line. That second
#   half is the point: it reproduces the exact reason the regression test stayed green while the
#   token was missing.
print()
print('--- control-mode fidelity: the control must revert the declared changes, not approximate ---')

def scan_line_mode(text):
    """The pre-change-1 traversal, reproduced here so the control can be tested without a corpus."""
    for n, line in enumerate(text.splitlines(), 1):
        if VENDOR.search(line):
            continue
        for sig, rx in SIGNALS:
            if rx.search(line):
                return sig
    return None

def scan_whole_mode(text):
    for sig, rx in SIGNALS:
        for m in rx.finditer(text):
            window = text[max(0, m.start() - 200): m.end() + 200]
            if VENDOR.search(window):
                continue
            return sig
    return None

multiline = [c for c in CASES if c[0] in ('ai-assistant', 'tensr cookie')]
singleline = [c for c in CASES if c[0] in ('fit-log', 'ubc-discovery', 'betanxt switch')]
for name, text, _, ref in multiline:
    w, l = scan_whole_mode(text), scan_line_mode(text)
    ok = (w is not None) and (l is None)
    if not ok:
        fails.append(f'CONTROL:{name}')
    print(f'  multi-line {name:28s} whole={w or "-":7s} line={l or "-":7s} '
          f'{"ok - change 1 is the reason this is caught" if ok else "CONTROL IS NOT A REVERT"}')
for name, text, _, ref in singleline:
    w, l = scan_whole_mode(text), scan_line_mode(text)
    ok = (w is not None) and (l is not None)
    if not ok:
        fails.append(f'CONTROL:{name}')
    print(f'  single-line {name:27s} whole={w or "-":7s} line={l or "-":7s} '
          f'{"ok - unaffected by change 1" if ok else "CONTROL DIVERGES UNEXPECTEDLY"}')

legacy_cap = re.compile(CAP['test-inbox-sdk'].pattern.replace('mailinator|', ''), re.I)
for label, text, must_legacy in [
    ('mailinator only', '  const box = "qa-user@mailinator.com";', False),
    ('mailtrap only', '  host: "smtp.mailtrap.io",', True),
    ('orgOS line, names both', '    // 3. Integrate with an email testing service (e.g., Mailinator, Mailtrap)', True),
]:
    cur = bool(CAP['test-inbox-sdk'].search(text))
    leg = bool(legacy_cap.search(text))
    ok = cur and (leg == must_legacy)
    if not ok:
        fails.append(f'CONTROL:CAP:{label}')
    note = 'ok'
    if label == 'orgOS line, names both' and ok:
        note = 'ok - and this is exactly why the regression test stayed green without mailinator'
    print(f'  CAP {label:24s} current={cur} legacy={leg} (expect legacy={must_legacy}) {note}')

print()
if fails:
    print(f'SELF-TEST FAILED on {len(fails)} case(s):')
    for f in fails:
        print(f'    {f}')
    print()
    print('A regression miss means the pattern does not implement the described method.')
    print('An isolation miss means a token is never actually exercised, so its test is green for')
    print('the wrong reason. A coverage gap means a token was added without a test.')
    print('Fix, re-run, and declare the change in the header of the affected script.')
    sys.exit(1)
print(f'SELF-TEST PASSED')
print(f'  regression : {len(CASES) + len(VENDOR_CASES) + len(CAP_CASES)} cases from the ledger')
print(f'  isolation  : {iso_checked} tokens, each matched alone and proven load-bearing by mutation')
print(f'  coverage   : every literal in every vocabulary has an isolation sample')
