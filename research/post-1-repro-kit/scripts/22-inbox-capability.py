#!/usr/bin/env python3
"""Re-derive the inbox-capability funnel (reported as 43 -> 6 -> 1) from source.

WHAT THIS REPRODUCES
    The second, independent route to the "1 out of 284" figure, described in
    docs/verified-artifacts.md section 2a. It asks a different question from DF-4's scan. DF-4
    looked for vendor SDK verbs inside test bodies. This asks: CAN this repository talk to an inbox
    at all? A test cannot read a code out of a real mailbox if nothing in the repository is able to
    reach a mailbox. Capability is a necessary condition, so this scan is deliberately over-broad
    and every survivor is opened by hand.

    Stage 1 (reported 43): any inbox capability anywhere - a test-inbox SDK, an IMAP client, a local
                           mail catcher, or a Gmail/Graph mail API - in dependency manifests,
                           docker-compose files, CI workflows, or test code.
    Stage 2 (reported  6): that capability sits in a test file, and the file also carries identity
                           context (login / signup / verification / OTP / magic link).
    Stage 3 (reported  1): a human opens all of stage 2 and decides.

    Stage 3 is not automatable and this script does not pretend otherwise. It writes the stage-2
    candidates to outputs/inbox-candidates.tsv with the recorded verdict for each, so that anyone
    can re-open exactly the same files and disagree with a specific judgment.

HONESTY NOTE
    As with script 21, these patterns were re-written from a prose description, run once, and
    reported as they came out. They were not tuned until they reproduced 43, 6 and 1.

    ONE CHANGE WAS MADE BEFORE THE FIRST RUN, AND IT IS DECLARED HERE.
    `mailinator` was added to the test-inbox-sdk pattern. The ledger records the orgOS stage-2 hit
    as a Mailinator mention, so Mailinator was plainly part of the original capability vocabulary
    (the ledger's own list ends in an ellipsis), but the first transcription of that list omitted
    it. The self-test did not catch this on its own, because the orgOS line happens to name Mailtrap
    in the same breath and matched on that instead; the omission was found by reading the recorded
    evidence rather than by trusting a green test. A repository that named only Mailinator would
    have been missed.

    This widens stage 1 and can only raise the count. It was made while the corpus was still
    downloading and no result existed. See outputs/pattern-freeze.txt for the hashes and timeline.

HOW THE RESULT WILL BE READ - decided and written down before the script was ever run
    stage 3 == 1, and it is stytchauth/stytch-browser
        Reproduced. The "1 of 284" moves to script-reproducible.

    stage 3 > 1
        The most prominent number in the post is WRONG. Stop, report, and open every new hit by
        hand. The post says the "1 out of 284" is the number that would embarrass its author most,
        and this is exactly that case arriving. The post gets corrected; the script does not.

    stage 3 == 0
        Either the original 1 was wrong, or the repository has drifted. Check the SHA against
        data/ledger-shas.tsv before concluding anything - stytchauth/stytch-browser has a recorded
        SHA, so this one is decidable.

    stage 1 or 2 differ from 43 and 6
        Adding `mailinator` widened stage 1, so a larger stage 1 is expected and is not by itself a
        disagreement. A wider stage 1 can produce stage-2 candidates that nobody has judged. The
        script says so loudly rather than quietly counting them out, and the stage-3 number is
        incomplete until a human opens them and records a verdict.

USAGE
    export CORPUS_DIR=...
    python3 22-inbox-capability.py > ../outputs/inbox-capability.txt
"""
import re, os, sys, glob, json

KIT = os.path.dirname(os.path.abspath(os.path.join(__file__, '..')))
HERE = os.path.dirname(os.path.abspath(__file__))
WORK = os.environ.get('CORPUS_DIR')
if not WORK:
    sys.exit('set CORPUS_DIR to the directory used by 20-fetch-corpus.sh')
SRC = os.path.join(WORK, 'src')
SHAS = os.path.join(WORK, 'corpus-shas.tsv')

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
    sys.exit(f'REFUSING TO COUNT: {len(bad)} repositories did not fetch cleanly: {bad[:5]}...')

# ---------------------------------------------------------------- capability patterns
CAP = {
    'test-inbox-sdk': re.compile(r'(mailosaur|mailslurp|mailtrap|mailinator|testmail\.app|@testmail|1secmail|maildrop)', re.I),
    'imap':           re.compile(r'(imapflow|node-imap|imap-simple|mailparser|["\']imap["\']|@types/imap|poplib)', re.I),
    'mail-catcher':   re.compile(r'(mailpit|mailhog|maildev|inbucket|greenmail|ethereal\.email|createTestAccount)', re.I),
    'provider-api':   re.compile(r'(gmail\.users\.messages|googleapis[^\n]{0,40}gmail|graph\.microsoft\.com[^\n]{0,40}messages'
                                 r'|@microsoft/microsoft-graph-client)', re.I),
}
# Where a capability may be declared.
MANIFEST = re.compile(r'(^|/)(package\.json|docker-compose[^/]*\.ya?ml|compose\.ya?ml)$')
CI       = re.compile(r'(^|/)\.github/workflows/[^/]+\.ya?ml$')
TESTFILE = re.compile(r'(^|/)(e2e|cypress|playwright|tests?|specs?|__tests__)/|'
                      r'\.(spec|test|cy|e2e)\.[jt]sx?$', re.I)
CODEEXT  = re.compile(r'\.(ts|tsx|js|jsx|mjs|cjs)$')
NOISE    = re.compile(r'(node_modules|/dist/|/build/|playwright-report|test-results)')

# ---------------------------------------------------------------- control run
# CAP_MODE=legacy reverts declared change 2 (the `mailinator` addition) and nothing else, so that
# any widening of stage 1 can be attributed rather than guessed at. The CAP literal above is NOT
# edited - the token is stripped from a copy at runtime - so the frozen pattern hash is unaffected.
CAP_MODE = os.environ.get('CAP_MODE', 'current')
if CAP_MODE not in ('current', 'legacy'):
    sys.exit("CAP_MODE must be 'current' (default) or 'legacy' (control, without mailinator)")
if CAP_MODE == 'legacy':
    CAP = dict(CAP)
    CAP['test-inbox-sdk'] = re.compile(
        CAP['test-inbox-sdk'].pattern.replace('mailinator|', ''), re.I)

# Stage 2 additionally requires identity context in the same file.
IDENTITY = re.compile(r'(sign\s?up|signUp|register|createAccount|verif(y|ication)\s?code|\botp\b|'
                      r'magic\s?link|one[\s-]?time\s?(code|password)|confirm(ation)?\s?(code|email)|'
                      r'sign\s?in|signIn|log\s?in|logIn)', re.I)

rows = [l.rstrip('\n').split('\t') for l in open(os.path.join(KIT, 'data', 'fields2.tsv')) if l.strip()]

stage1, stage2 = {}, {}
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
    anywhere, in_test = [], []
    for f in glob.glob(os.path.join(d, '**', '*.*'), recursive=True):
        rel = f[len(d) + 1:]
        if NOISE.search(rel):
            continue
        is_manifest = bool(MANIFEST.search('/' + rel))
        is_ci       = bool(CI.search('/' + rel))
        is_test     = bool(TESTFILE.search('/' + rel)) and bool(CODEEXT.search(rel))
        if not (is_manifest or is_ci or is_test):
            continue
        try:
            text = open(f, errors='ignore').read()
        except OSError:
            continue
        for kind, rx in CAP.items():
            for n, line in enumerate(text.splitlines(), 1):
                m = rx.search(line)
                if not m:
                    continue
                where = 'manifest' if is_manifest else ('ci' if is_ci else 'test')
                anywhere.append((kind, where, rel, n, ' '.join(line.split())[:140]))
                if is_test and IDENTITY.search(text):
                    in_test.append((kind, rel, n, ' '.join(line.split())[:140]))
                break
    if anywhere:
        stage1[repo] = anywhere
    if in_test:
        stage2[repo] = in_test

# ---------------------------------------------------------------- recorded stage-3 verdicts
VERD = os.path.join(HERE, 'verdicts-inbox.tsv')
verdicts = {}
if os.path.exists(VERD):
    for line in open(VERD):
        if not line.strip() or line.startswith('#'):
            continue
        p = line.rstrip('\n').split('\t')
        if len(p) >= 3:
            verdicts[p[0]] = (p[1], p[2])

confirmed = [r for r in sorted(stage2) if verdicts.get(r, ('', ''))[0] == 'REAL']
unjudged  = [r for r in sorted(stage2) if r not in verdicts]

N = len(rows)
print('=' * 78)
print('INBOX CAPABILITY FUNNEL - independent re-derivation of the "1 of 284"')
print('=' * 78)
print(f'run                                           : CAP_MODE={CAP_MODE}' +
      ('   (CONTROL - without mailinator)' if CAP_MODE == 'legacy' else '   (main)'))
print(f'corpus                                        : {N} repositories, length-verified')
print(f'stage 1  any inbox capability anywhere        : {len(stage1)}/{N}')
print(f'stage 2  capability in a test file with identity context : {len(stage2)}/{N}')
print(f'stage 3  hand verdict REAL (see verdicts-inbox.tsv)      : {len(confirmed)}/{N}')
if unjudged:
    print()
    print(f'!!! {len(unjudged)} stage-2 candidate(s) have NO recorded verdict: {unjudged}')
    print('!!! The stage-3 number is incomplete until a human opens these and records a verdict.')
print()
print('--- stage 2 candidates, all of them, with the recorded verdict ---')
for repo in sorted(stage2):
    v, why = verdicts.get(repo, ('UNJUDGED', 'no verdict recorded'))
    print(f'{repo}  sha={shas.get(repo,"?")}')
    print(f'    verdict: {v} - {why}')
    for kind, rel, n, line in stage2[repo][:4]:
        print(f'    {kind:15s} {rel}:{n}  {line}')

with open(os.path.join(KIT, 'outputs', f'inbox-candidates.{CAP_MODE}.tsv'), 'w') as fh:
    fh.write('repo\tsha\tverdict\treason\tkind\tpath\tline\tevidence\n')
    for repo in sorted(stage2):
        v, why = verdicts.get(repo, ('UNJUDGED', 'no verdict recorded'))
        for kind, rel, n, line in stage2[repo]:
            fh.write(f'{repo}\t{shas.get(repo,"?")}\t{v}\t{why}\t{kind}\t{rel}\t{n}\t{line}\n')

print()
print('--- stage 1, repositories with any capability anywhere ---')
for repo in sorted(stage1):
    kinds = sorted({k for k, *_ in stage1[repo]})
    wheres = sorted({w for _, w, *_ in stage1[repo]})
    print(f'  {repo:55s} {",".join(kinds):30s} in {",".join(wheres)}')

json.dump({'n': N, 'stage1': list(stage1), 'stage2': list(stage2), 'confirmed': confirmed,
           'stage2_detail': stage2},
          open(os.path.join(WORK, f'inbox-capability.{CAP_MODE}.json'), 'w'), indent=1)
