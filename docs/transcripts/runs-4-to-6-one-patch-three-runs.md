# P1 transcripts - runs 4 to 6 (one documentation patch, measured three times)

**Date:** 2026-08-16. **What this is:** not three signups. One measurement, repeated three
times, of whether editing a README changes what a blind agent does.

Run 3 read a sentence in this file and concluded the HTTP API was locked. It was wrong, the
sentence was ours, and it cost that run the only question it existed to answer. We fixed the
sentence in `b5d8f8c`, thirty nine lines, zero lines of code. Runs 4, 5 and 6 ask whether the
fix took.

Three runs rather than one, decided and written down before run 5 started, because an agent is
a stochastic process: clean once can be luck exactly as dirty once can be luck. The verdict
table was fixed in advance. 3/3 clean closes it, 0/3 reopens it as a product bug, and anything
mixed also reopens it, because a wording that lowers the odds of a misreading without removing
them is a fence with a hole, and the odds of an agent reading carefully are not something a
maintainer can patch.

---

## What run 3 actually did, since the fix is aimed at it

Its fourth action, from the session file rather than from its report:

```
GET http://127.0.0.1:8925/api/v1/healthz     (no credential)
-> {"error":{"code":"unauthorized","message":"this route needs a project bearer or a run token"}}
```

Read the path slowly. **The route name is right. The prefix is wrong.** At the time of that run
the word `healthz` appeared exactly once in this README, in the provisional list, filed under
the sentence `Everything else under /api/v1`. The agent took the name from the only place the
document offered it, and the prefix from the same sentence. It assembled the two halves it had
been handed, went where it had been sent, found nothing there, and read the refusal as a lock.
It never called `/healthz`.

**A document that hands out correct pieces inside a wrong frame is worse than a document that
says nothing. Silence at least leaves a reader knowing they do not know.**

The patch fixed both halves: the bearer sentence now names `/api/v1`, the provisional list
stopped claiming that prefix covers everything, and `Operating it` gained a health route
section with the path, the body, and what it deliberately withholds.

---

## The stages, and the one variable

Three stages, one per run, each built by the same script from the same commit and torn down
after. Each was a copy of the repository with `docs/transcripts` and `.claude` removed, 229
files, `README.md` byte identical to the commit, no `.git`, and `.mcp.json` left in place
because that is what anyone cloning this repository actually gets.

Each ran against a brand new data directory, proven empty by counting rather than by assertion:
`runs=0 leases=0 claims=0 messages=0 identities=0`, one ledger row for the bearer that was just
provisioned, zero blobs. The MCP handshake was exercised before every run and the counts were
re-read afterwards to confirm the handshake touches nothing.

Between run 3 and these three, exactly one thing changed:

```
README.md   |  39 ++++++++--
```

No source file changed. The binary is the same 22,870,930 bytes. And the trap is still armed:
`/api/v1/healthz` still answers `unauthorized` to an unauthenticated caller, rechecked at each
of the three stage builds. Nothing stopped these agents from repeating run 3's mistake except
the wording.

The prompt was byte identical across all four runs, taken from the session file of run 3 rather
than retyped, and verified with `diff` before each paste. No tool names, no reason codes, no
ordering, no mention of MCP or REST or health.

---

## Result: three for three

Every run passed the same gate, which is that a single sentence of human help fails the run.
Counted from each session file rather than recalled: one human turn, the prompt, and no
interruption record anywhere. Twenty six to twenty seven tool results, one harness insertion.

| | run 3 (old README) | run 4 | run 5 | run 6 |
|---|---|---|---|---|
| Health route called | `/api/v1/healthz` | `/healthz` | `/healthz` | `"$AUTHSTUNT_URL/healthz"` |
| Answer | `401` | `200` | `200` | `200` |
| Sentence claiming HTTP was shut | **yes** | none | none | none |
| Dropped HTTP entirely | **yes** | no | no | no |

Zero occurrences of `/api/v1/health*` across all three later sessions. Scanning every line the
agents wrote for `bearer`, `unauthorized`, `401` or `healthz` returns nothing in any of them:
none of the three treated the question as open, because none was misled into opening it.

### The part worth more than the count: they did not read alike

| | How it read this README | When it probed health |
|---|---|---|
| run 4 | one `Read` of the whole file, action #1 | action #2, almost reflex |
| run 5 | grepped headings for a table of contents, then read sections | action #9 |
| run 6 | grepped headings, then four `sed` line ranges | action #8 |

Three entry points into the same document, three different amounts of care, one behavior. Three
repetitions of a single reading strategy would have proved much less. None of them opened
`examples/demo-app/server.js`; when they needed a form field name they read the HTML the app
serves. Three runs in a row where the README beat the source code.

### What this licenses, and what it does not

**Fair to say:** a documentation patch of zero lines of code removed a misreading that had been
strong enough to make an agent abandon an entire access surface, and it held across three
independent sessions that read the file three different ways.

**Not fair to say:** that the new health route section is what taught them. No run resolves
attention down to a single section. What was measured is that behavior changed and that the
only thing that changed was the README.

**One caveat we are not going to bury.** The three-run rule was written after run 4 came back
clean. Runs 5 and 6 were fully blind to it. Anyone who counts only the blind samples gets 2/2
and the same conclusion one notch weaker, and that is a legitimate way to count.

### The harness these runs were measured through, and what it costs the result

All four runs, run 3 included, received the prompt through a personal workflow command rather
than typed bare. That command carries a skill whose instructions **require scanning the
codebase before acting**. It is in the published session files, in the `isMeta` message next to
the prompt, so this is checkable rather than asserted.

Two consequences follow, and neither is comfortable.

**The result is conditional on the harness.** "3/3 clean" is a statement about this prompt
delivered this way. Someone who runs plain `claude` in a clone of this repository and pastes
the same prompt may or may not reproduce it, and nothing here entitles us to say they will. The
honest scope of the claim is: under a harness that pushes an agent to read first, the patched
wording produced correct behavior three times out of three, where the unpatched wording
produced the misreading.

**The early full read of the README is not the README's achievement.** Run 4 read this file end
to end as its first action, and it is entirely plausible that the skill's scan-first rule is
why, not any pull of the document. Runs 5 and 6 read it more selectively, headings first, which
looks more like a choice than a reflex, but none of that separates cleanly from the harness
either. So the reading behavior in these runs should not be quoted as evidence that this README
draws readers in. Only the health route behavior is being claimed, because that is the one
place where the patched and unpatched wording can be compared with everything else held
constant.

**We chose not to run a bare-harness fourth condition now, and the reason is the same one that
made us run three trials in the first place.** A single bare run would be n=1 under a different
harness, which cannot be compared cleanly against these three, and cannot be compared against
run 3 either, since run 3 was itself run through the command. It would produce a number that
looks like a comparison and is not one. Doing it properly means three bare runs and a rule
fixed in advance, which is a separate experiment with its own cost. Until someone pays that
cost, the conditional scope stated above is the whole of the claim.

---

## The refusal, measured rather than argued

The obvious follow-up is whether a server that answers `401` to a route it does not have is
lying about why it refused. That was settled with a measurement on a throwaway instance, on its
own port and data directory, torn down afterwards, deliberately not against any of the three
stages so their baselines stayed clean.

| Request | Credential | Answer |
|---|---|---|
| `GET /api/v1/healthz` | valid bearer | **`404 page not found`** |
| `GET /api/v1/nonexistent` | valid bearer | `404` |
| `GET /api/v1/runs/zzz/nothere` | valid bearer | `404` |
| `GET /api/v1/healthz` | none | `401 unauthorized` |
| `GET /api/v1/runs` (a real route) | valid bearer | `405`, wrong method |
| `GET /api/v1/runs` (a real route) | wrong bearer | `401` |

So the answer is 404, and **the behavior stands. No code changed and the frozen surface is
untouched.** A caller who has authenticated is told the truth: there is nothing at that path.
The `401` is reserved for callers who have not authenticated, and withholding the route map
from a stranger is a deliberate choice rather than a misleading one.

**The limiting clause is not optional.** The `401` only ever reaches an unauthenticated caller.
That happened to be run 3's exact situation, because it probed without a credential. Anyone
repeating the story of run 3 without that clause is telling it wrong.

---

## What the three runs found that has nothing to do with the patch

**Run 6 built a negative control nobody asked it for, and it is the strongest evidence any of
these runs produced.** It leased a second identity, signed it up, and then tried the *first*
account's real code against it. The answer was `400`. Runs 4 and 5 had shown that a wrong code
fails and a right code passes. Run 6 showed that a code is bound to the address it was mailed
to, which is much closer to the thing this service claims to guarantee. Its ledger in
`~/.authstunt/run6` holds the row for it.

**Nobody has ever located their own evidence on the weaker path.** Four sessions in a row, none
opened `Four mail paths, and which one you are on`, and none noticed that a demo app talking
straight to this server's SMTP port is the weaker branch, one this README says is weaker. These
were sessions that graded their own evidence carefully and unprompted, so this is not a failure
of readers. The idea does not reach them where they need it, and a README section alone does
not appear able to fix that.

**Three of four sessions could not close a run.** The frozen MCP surface has four tools and
none of them ends a run. Run 4 probed until it found the provisional REST route and closed
cleanly; runs 3, 5 and 6 accepted the limit and left the run to expire. The stores agree:
`complete` for run 4, `active` for the other two. Same gap, outcome decided by which agent
turned up, which is the signature of a surface that is documented but not usable. Adding a tool
would change a frozen contract, so it is filed rather than fixed.

---

## Raw material

The three session files are committed next to this document:

- `sessions/run-4.jsonl`
- `sessions/run-5.jsonl`
- `sessions/run-6.jsonl`

They are the primary evidence for every count above: the human turn, the absence of
interruptions, the exact URLs, the reading strategies, and the order things happened in.
Nothing here asks to be taken on trust.

They are redacted, and `sessions/REDACTION.md` says exactly how. No message was deleted,
reordered or retimed; what was replaced is the operator's machine, meaning absolute paths,
their private workflow skill text, their session hook output and their installed tool
inventory, each behind a labelled marker. The gate count, the URLs and the prompt all survive
verbatim, which was checked mechanically rather than by eye. The checksums of the unredacted
originals are recorded there too.

The per-run stores are kept as well, so the ledgers can be read against these claims.
