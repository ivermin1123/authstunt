# Redaction policy for the published session files

The `.jsonl` files here are the raw session records of the blind agent runs. `run-4`, `run-5` and
`run-6` are written up in `../runs-4-to-6-one-patch-three-runs.md`; `run-3` is the run those three
were the answer to. They are published so that every count in those documents can be checked by
someone who does not trust it.

`run-3` was redacted and published later than the other three, and it is worth saying why rather
than letting the timestamps imply something. The other three were published as the evidence that a
documentation patch worked. run-3 is the evidence that it was needed: it holds the one call the
whole account turns on, an agent building `http://127.0.0.1:8925/api/v1/healthz` out of two
fragments the README handed it separately, and getting `unauthorized` from a route that does not
exist. While that file was unpublished, the central claim rested on a summary of it. A later
verification pass over the published material flagged exactly that, so the file was redacted by the
same two scripts, against the same checks, and added here.

They are redacted, because a session file also records the machine it ran on. The rule is
**keep the structure, hide the content**.

## What is preserved

**Every message. No message was deleted, reordered, merged, or retimed.** Roles, `type`,
`timestamp`, `uuid`, `parentUuid` and the sequence they arrived in are exactly as recorded.
Every tool call, every tool result, every line the agent wrote, and the operator's single typed
turn are untouched, as are all `http://127.0.0.1:*` URLs, which are the primary evidence.

## What is replaced, and why

Each replacement is a labelled marker, so it is visible where something was removed:

| Marker | What it replaces | Why |
|---|---|---|
| `[redacted: absolute path]` | filesystem paths under `/Users`, `/private/tmp`, `/home` | They name the operator's machine and account. URLs are never touched: the pattern requires a path not preceded by `:`, so `http://127.0.0.1:8925/healthz` cannot match. |
| `[redacted: user's private skill text]` | the body of the personal workflow skill that was in context alongside the prompt | It is the operator's own tooling, not part of this project. Its trailing `ARGUMENTS:` section is **kept**, because that section is the neutral prompt itself, which is evidence. |
| `[redacted: user's private session hook output]` | the operator's session hook output | Personal working rules, local paths, machine load. |
| `[redacted: user's private tool/agent/skill inventory]` | the listings of installed agents, skills, deferred tools and MCP instruction blocks | The operator's installed tooling, which would otherwise disclose unrelated services. |

One nine character fragment of a Google API key prefix, already truncated by the agent itself
in its own output, is replaced with `[redacted]`.

## How the redaction was produced and checked

Mechanically, by two scripts kept in the private repository next to the originals:
`redact-session.py` performs the transformation and `verify-redaction.py` checks it. The check
fails unless all of the following hold, and it passed on all four files:

1. same line count
2. same message count per `type`
3. the same `timestamp` sequence, in the same order
4. the gate count is unchanged: one human turn, zero `Request interrupted` records
5. every `http://127.0.0.1:*` URL in the conversation survives, with the same multiplicity
6. no absolute path or key fragment is left anywhere in the output

Condition 5 counts URLs in `user` and `assistant` messages only. Some redacted inventory blocks
mention localhost URLs in prose; counting those would raise a false alarm about the part that
was deliberately removed. The measurement's evidence lives in the conversation, not in the
inventory.

The checks are also runnable directly against these published files. For example:

```sh
jq -r 'select(.type=="user")
       | if (.message.content|type)=="string" then "HUMAN" else "tool" end' run-4.jsonl \
  | sort | uniq -c
grep -c "Request interrupted" run-4.jsonl
```

which is how the gate for each run was closed: one human turn, no interruptions.

## Checksums of the unredacted originals

The originals stay in the private repository and are not published. Their SHA-256 sums are
recorded here so that a redacted file can later be shown to derive from a specific original:

```
4c6c2bc19c717d8efc6d23be445f66aea6928172eeb2462b5c6c3e5048c7dde0  run-3 (original)
69913bb5920f7845bf56ba582e6a2927c417bd706d563a9296f7d6588a1cc815  run-4 (original)
0a54c238c198fc9c0ee2c2dd4644140a84b4fdd39792a8c7b0296e9cc42d1ee8  run-5 (original)
9eb940d2da7dd22a1ddbaaf270906a109c62aa8f1f09805a8c602a8602df3925  run-6 (original)
```

These sums prove which file a redaction came from. They do not prove the redaction is faithful,
since the transformation is not reversible. That claim rests on the six checks above, which
anyone can rerun.
