#!/usr/bin/env python3
"""Re-derive the self-built-backdoor count (reported as 34/284) from source, independently of
`10-selfbuilt-bypass.py`.

WHAT THIS REPRODUCES
    The verification pass described in docs/verified-artifacts.md section 2b. That pass split
    "self-built" into SWITCH (a flag or environment variable that turns authentication off) and
    FORGE (a session, cookie or token that is fabricated and injected into the browser), plus
    HEADER (a trust header used only in tests); subtracted vendor mechanisms by name so that a
    vendor's own documented backdoor is not counted as a home-made one; and kept every hit with
    path:line instead of stopping at the first one per repository.

    To make the comparison against DF-4 an apples-to-apples one, the FILE SELECTION RULE below is
    copied verbatim from `10-selfbuilt-bypass.py` (same extensions, same E2E path test, same noise
    exclusions). Only the detection patterns differ. Any disagreement is therefore caused by what
    counts as a hit, not by which files were opened.

HONESTY NOTE, WHICH MATTERS MORE THAN THE NUMBER
    The original verification pass left prose and no code. These patterns were re-written from that
    prose description, not recovered from it. They were written once, run once, and the result
    reported as it came out. They were NOT adjusted until they reproduced 34. If this script
    disagrees with the published number, that disagreement is the finding, and it is recorded in
    outputs/ rather than tuned away.

    ONE CHANGE WAS MADE BEFORE THE FIRST RUN, AND IT IS DECLARED HERE.
    The first draft scanned line by line, to get line numbers cheaply. A check against the true
    positives already documented in the ledger - run before the corpus had finished downloading, so
    before any count existed - showed that draft failing on two of them: the forged WorkOS cookie in
    `Eliahhango/ai-assistant` and the forged Stytch cookie in `tensr-xyz/tensr-platform-web`. Both
    are written as an object literal spanning several lines, so the `addCookies(` call and the
    `name:` key that identifies it are never on the same line, and a line-oriented scan cannot see
    them. DF-4's own regex matched across newlines because it searched whole file text. The scan was
    changed to whole-text `finditer` with line numbers recovered from the match offset.

    This was a fix to make the implementation match the described method, made while the result was
    still unknown. It is not a threshold that was moved to reach a number. The check that caught it
    is reproducible: `python3 24-pattern-selftest.py`.

    A SECOND CHANGE WAS MADE AFTER THE FIRST RESULT, AND THAT IS DECLARED TOO.
    The first run of this script produced 36/284 and pulled in `QRun-IO/qqq-frontend-next`, whose
    only hit was `AUTH_META = { name: 'mockAuth', type: 'FULLY_ANONYMOUS' }` inside a file of
    Playwright route-interception mocks. That is a mocked API response, not a switch that turns
    authentication off. It matched because the first transcription of SWITCH included `MOCK|FAKE`,
    which the source description does not contain: the ledger defines this category as "a flag or
    environment variable that turns authentication off", and DF-4's own regex used only
    ALLOW|SKIP|BYPASS|DISABLE. `MOCK|FAKE` was an interpretation added while re-writing the regex
    from prose, and it was not among the four changes declared before the run.

    `MOCK|FAKE` was therefore REMOVED and the whole thing re-run. The token was removed rather than
    the repository excluded, deliberately: excluding one repository is a judgement about one case
    that anyone can call cherry-picking, while removing a token that was never in the definition is
    systematic, checkable, and can drop other repositories too - and if it does, that is information
    worth having rather than something to avoid.

    The test that separates a method fix from a number fix is whether the rule being applied is
    OLDER than the data. "A flag or environment variable that turns authentication off" was written
    before any of this was measured. `mockAuth` fails that pre-existing definition.

    Both counts are published, in outputs/selfbuilt-bypass-with-mockfake.txt and
    outputs/selfbuilt-bypass-v2.txt, so the delta is visible rather than argued.

USAGE
    export CORPUS_DIR=...           # the directory used by 20-fetch-corpus.sh
    python3 21-selfbuilt-bypass-v2.py > ../outputs/selfbuilt-bypass-v2.txt
"""
import re, os, sys, glob, json, collections

KIT = os.path.dirname(os.path.abspath(os.path.join(__file__, '..')))
HERE = os.path.dirname(os.path.abspath(__file__))
WORK = os.environ.get('CORPUS_DIR')
if not WORK:
    sys.exit('set CORPUS_DIR to the directory used by 20-fetch-corpus.sh')
SRC = os.path.join(WORK, 'src')
SHAS = os.path.join(WORK, 'corpus-shas.tsv')

# ---------------------------------------------------------------- refuse to run on a partial corpus
if not os.path.exists(SHAS):
    sys.exit(f'{SHAS} not found - run 20-fetch-corpus.sh first')
shas, cdates, bad = {}, {}, []
for line in open(SHAS):
    p = line.rstrip('\n').split('\t')
    if len(p) < 6 or p[0] == 'repo':
        continue
    if p[5] == 'OK':
        shas[p[0]] = p[1]; cdates[p[0]] = p[2]
    else:
        bad.append(p[0])
if bad:
    sys.exit(f'REFUSING TO COUNT: {len(bad)} repositories did not fetch cleanly: {bad[:5]}...\n'
             f'A short corpus produces a quietly low number. Fix 20-fetch-corpus.sh first.')

# ---------------------------------------------------------------- file selection: copied from DF-4
E2EPATH = re.compile(r'/(e2e|cypress|playwright|tests?/e2e|smoke)/|\.cy\.[jt]sx?$|playwright[^/]*\.config\.[jt]s$')
NOISE   = re.compile(r'(node_modules|playwright-report|test-results|/dist/|/build/|__tests__|\.test\.[jt]sx?$)')
CODEEXT = re.compile(r'\.[jt]sx?$')

# ---------------------------------------------------------------- detection: the part that is new
# FORGE - a credential the repository made up, put where the app will read it.
# The storage-key alternation deliberately matches as a SUBSTRING and includes `user`, which is what
# DF-4's list lacked. It is also what makes "oauth-debugger-e2e-started" match: see EXCLUSIONS.
FORGE = re.compile(r"""(
    (local|session)Storage\.setItem\(\s*["'`][^"'`]*(clerk|stytch|supabase|sb-|wos-|auth|session|token|jwt|user)
  | addCookies\(\s*\[?\s*\{?[^)]{0,120}(session|auth|token|jwt|user)
  | (setCookie|cookies\(\)\.set)\(\s*["'`][^"'`]*(session|auth|token|jwt|user)
  | document\.cookie\s*=\s*["'`][^"'`]*(session|auth|token|jwt)
)""", re.I | re.X)

# SWITCH - a named flag that turns authentication off for the run. Matching the IDENTIFIER (not only
# a string literal) is what catches a bypass whose cookie name is an imported constant.
SWITCH = re.compile(r"""(
    (ALLOW|SKIP|BYPASS|DISABLE)_?(LOCAL_)?(DEV_)?(AUTH|IDENTITY|LOGIN|SESSION|USER)
  | (AUTH|LOGIN|SESSION|IDENTITY)_(BYPASS|DISABLED|SKIP)
  | (BYPASS|SKIP)_?(COOKIE|TOKEN)
  | TEST_BYPASS
  | E2E_(SESSION|AUTH)_(JWT|TOKEN)
)""", re.I | re.X)

# HEADER - a trust header that exists only so tests can walk past the front door.
HEADER = re.compile(r'(x-e2e-|X-Test-User|TEST_USER_HEADER|DEV_USER_ID)', re.I)

# Vendor mechanisms, subtracted by name. A line that is really the vendor's own documented backdoor
# is not a home-made one, and must not be counted here.
VENDOR = re.compile(r"""(
    \+clerk_test | setupClerkTestingToken | clerkSetup | @clerk/testing
  | FIREBASE_AUTH_EMULATOR_HOST | connectAuthEmulator | firebase\s+emulators
  | mailosaur | mailslurp | mailtrap
  | \+dynamic_test | descope[a-z-]*test | stytch[a-z_]*sandbox
  | supabase[a-z_]*(test_helpers|admin_client)
)""", re.I | re.X)

SIGNALS = (('FORGE', FORGE), ('SWITCH', SWITCH), ('HEADER', HEADER))

# ---------------------------------------------------------------- control run
# SCAN_MODE=line reverts declared change 1 (whole-text matching) and nothing else. Its purpose is
# to measure, rather than guess, how much of any increase over DF-4's 34 is caused by that change
# as opposed to by repository drift or by a genuine difference in measurement. The detection
# patterns are identical in both modes - only the traversal differs - so the frozen pattern hash is
# unaffected by this switch existing.
SCAN_MODE = os.environ.get('SCAN_MODE', 'whole')
if SCAN_MODE not in ('whole', 'line'):
    sys.exit("SCAN_MODE must be 'whole' (default, current) or 'line' (control, pre-change-1)")

# ---------------------------------------------------------------- documented hand adjudications
# Loaded from a file, not hardcoded in the logic, so a reader can see exactly which machine hits a
# human overruled and why. Removing this file changes the number and the script says so.
EXCL = os.path.join(HERE, 'exclusions-selfbuilt.tsv')
excluded = {}
if os.path.exists(EXCL):
    for line in open(EXCL):
        if not line.strip() or line.startswith('#'):
            continue
        p = line.rstrip('\n').split('\t')
        if len(p) >= 3:
            excluded[p[0]] = (p[1], p[2])

def prim(vs):
    s = set(vs.split('+'))
    for v in ['stytch', 'descope', 'supertokens', 'kinde', 'workos']:
        if v in s: return v
    for v in ['clerk', 'auth0', 'firebase', 'supabase']:
        if v in s: return v
    return sorted(s)[0]

rows = [l.rstrip('\n').split('\t') for l in open(os.path.join(KIT, 'data', 'fields2.tsv')) if l.strip()]

hits = collections.defaultdict(list)
for r in rows:
    repo = r[0]
    d = os.path.join(SRC, repo.replace('/', '_'))
    # Both conditions matter. A directory can exist from a shard that died after extracting but
    # before recording its verification row, and scanning that directory would count a repository
    # nobody proved complete.
    if repo not in shas:
        sys.exit(f'REFUSING TO COUNT: {repo} has no verified row in corpus-shas.tsv.')
    if not os.path.isdir(d):
        sys.exit(f'REFUSING TO COUNT: {repo} is in fields2.tsv but not in the fetched corpus.')
    for f in glob.glob(os.path.join(d, '**', '*.*'), recursive=True):
        rel = f[len(d) + 1:]
        if NOISE.search(rel) or not CODEEXT.search(rel) or not E2EPATH.search('/' + rel):
            continue
        try:
            text = open(f, errors='ignore').read()
        except OSError:
            continue
        if SCAN_MODE == 'whole':
            # Whole-text search. A forged cookie is written as an object literal spanning several
            # lines, so the call and the key that identifies it are never on the same line; a
            # line-oriented scan cannot see it at all. DF-4's regex matched across newlines for the
            # same reason. Line numbers are recovered from the match offset.
            for name, rx in SIGNALS:
                for m in rx.finditer(text):
                    start = m.start()
                    # vendor subtraction over the surrounding window rather than "the line",
                    # because the match itself may span lines
                    window = text[max(0, start - 200): m.end() + 200]
                    if VENDOR.search(window):
                        continue               # the vendor's own backdoor, not a home-made one
                    n = text.count('\n', 0, start) + 1
                    hits[repo].append((name, rel, n, ' '.join(m.group(0).split())[:150]))
        else:
            # CONTROL: the pre-change-1 traversal, kept verbatim so the difference is measurable.
            for n, line in enumerate(text.splitlines(), 1):
                if VENDOR.search(line):
                    continue
                for name, rx in SIGNALS:
                    m = rx.search(line)
                    if m:
                        hits[repo].append((name, rel, n, ' '.join(line.split())[:150]))
                        break

raw = sorted(hits)
kept = [r for r in raw if r not in excluded]
dropped = [r for r in raw if r in excluded]

N = len(rows)
print('=' * 78)
print('SELF-BUILT BACKDOOR - independent re-derivation')
print('=' * 78)
print(f'run                        : SCAN_MODE={SCAN_MODE}' +
      ('   (CONTROL - pre-change-1 line-by-line traversal)' if SCAN_MODE == 'line' else '   (main)'))
print(f'corpus                     : {N} repositories, all fetched and length-verified')
print(f'file selection             : copied verbatim from 10-selfbuilt-bypass.py')
print(f'detection                   : SWITCH + FORGE + HEADER, vendor mechanisms subtracted by name')
print()
print(f'repositories with >=1 hit  : {len(raw)}/{N} = {100*len(raw)/N:.1f}%')
print(f'hand-excluded (see exclusions-selfbuilt.tsv) : {len(dropped)}')
for r in dropped:
    why, ev = excluded[r]
    print(f'    - {r} :: {why} :: {ev}')
print()
print(f'>>> SELF-BUILT BACKDOOR COUNT : {len(kept)}/{N} = {100*len(kept)/N:.1f}%')
print()

# ---------------------------------------------------------------- compare against DF-4's own list
df4 = set()
bs = os.path.join(KIT, 'data', 'backdoor-strict.tsv')
old = os.path.join(KIT, 'outputs', 'round2-selfbuilt-bypass.txt')
if os.path.exists(old):
    for line in open(old):
        m = re.match(r'\s+(\S+/\S+)\s+::', line)
        if m:
            df4.add(m.group(1))
if df4:
    k = set(kept)
    print(f'DF-4 published set         : {len(df4)}')
    print(f'both                       : {len(k & df4)}')
    print(f'only this script           : {len(k - df4)}   {sorted(k - df4)}')
    print(f'only DF-4                  : {len(df4 - k)}   {sorted(df4 - k)}')
    print(f'this set is a superset     : {"YES" if df4 <= k else "NO"}')
    print()

print('--- every hit, path:line ---')
for repo in raw:
    tag = '  [EXCLUDED]' if repo in excluded else ''
    print(f'{repo}  sha={shas.get(repo,"?")}{tag}')
    for name, rel, n, line in hits[repo]:
        print(f'    {name:6s} {rel}:{n}  {line}')

json.dump({'n': N, 'raw': raw, 'kept': kept, 'excluded': list(excluded),
           'hits': {k: v for k, v in hits.items()}, 'shas': shas},
          open(os.path.join(WORK, f'selfbuilt-v2.{SCAN_MODE}.json'), 'w'), indent=1)
